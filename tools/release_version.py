#!/usr/bin/env python3
"""Validate and update ColabOne release versions across repository manifests."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any

COLABONE_DEFAULT_VERSION = "0.1.0"
_SEMVER_CORE = r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
_SEMVER_PRERELEASE = r"(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?"
COLABONE_VERSION_RE = re.compile(rf"^{_SEMVER_CORE}{_SEMVER_PRERELEASE}$")
COLABONE_RELEASE_TAG_RE = re.compile(rf"^v({_SEMVER_CORE}{_SEMVER_PRERELEASE})$")

COLABONE_VERSION_FILES = {
    "console": Path("console/mygo.json"),
    "frontend_package": Path("frontend/package.json"),
    "frontend_lock": Path("frontend/package-lock.json"),
}


class ColabOneReleaseVersionError(ValueError):
    """Raised when a release tag or manifest version is invalid or inconsistent."""


def _reject_duplicate_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ColabOneReleaseVersionError(f"Duplicate JSON key: {key}")
        result[key] = value
    return result


def _load_json(path: Path) -> dict[str, Any]:
    try:
        data = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=_reject_duplicate_keys,
        )
    except FileNotFoundError as error:
        raise ColabOneReleaseVersionError(f"Version file not found: {path}") from error
    except json.JSONDecodeError as error:
        raise ColabOneReleaseVersionError(f"Invalid JSON in {path}: {error}") from error
    if not isinstance(data, dict):
        raise ColabOneReleaseVersionError(f"Expected JSON object in {path}")
    return data


def _require_version_string(data: dict[str, Any], label: str) -> str:
    value = data.get("version")
    if not isinstance(value, str) or not COLABONE_VERSION_RE.fullmatch(value):
        raise ColabOneReleaseVersionError(
            f"Invalid semantic version in {label}: {value!r}"
        )
    return value


def colabone_parse_version(raw: str) -> str:
    """Validate a bare SemVer string (or v-prefixed version) and return bare SemVer."""
    candidate = raw[1:] if raw.startswith(("v", "V")) else raw
    if not COLABONE_VERSION_RE.fullmatch(candidate):
        raise ColabOneReleaseVersionError(
            f"Version must match MAJOR.MINOR.PATCH (optional -prerelease): {raw!r}"
        )
    return candidate


def colabone_parse_release_tag(tag: str) -> str:
    """Validate a Git release tag (vMAJOR.MINOR.PATCH[-prerelease]) and return bare SemVer."""
    match = COLABONE_RELEASE_TAG_RE.fullmatch(tag)
    if match is None:
        raise ColabOneReleaseVersionError(
            f"Release tag must match vMAJOR.MINOR.PATCH (optional -prerelease): {tag!r}"
        )
    return match.group(1)


def colabone_read_release_versions(root: Path) -> dict[str, str]:
    """Read version declarations from all release-managed manifests under root."""
    console_rel = COLABONE_VERSION_FILES["console"]
    pkg_rel = COLABONE_VERSION_FILES["frontend_package"]
    lock_rel = COLABONE_VERSION_FILES["frontend_lock"]
    console_path = root / console_rel
    pkg_path = root / pkg_rel
    lock_path = root / lock_rel

    console_key = console_rel.as_posix()
    pkg_key = pkg_rel.as_posix()
    lock_key = lock_rel.as_posix()

    versions: dict[str, str] = {
        console_key: _require_version_string(_load_json(console_path), console_key),
        pkg_key: _require_version_string(_load_json(pkg_path), pkg_key),
    }

    if lock_path.is_file():
        lock_data = _load_json(lock_path)
        versions[lock_key] = _require_version_string(lock_data, lock_key)
        packages = lock_data.get("packages")
        if isinstance(packages, dict) and isinstance(packages.get(""), dict):
            root_pkg_key = f"{lock_key}#packages['']"
            versions[root_pkg_key] = _require_version_string(
                packages[""],
                root_pkg_key,
            )

    return versions


def colabone_validate_release_versions(
    root: Path,
    release_tag: str | None = None,
) -> str:
    """Validate that all manifest versions are valid, consistent, and match release_tag if given."""
    versions = colabone_read_release_versions(root)
    expected = (
        colabone_parse_release_tag(release_tag)
        if release_tag is not None
        else versions[COLABONE_VERSION_FILES["console"].as_posix()]
    )
    mismatches = {
        source: value for source, value in versions.items() if value != expected
    }
    if mismatches:
        details = ", ".join(
            f"{source}={value}" for source, value in sorted(mismatches.items())
        )
        raise ColabOneReleaseVersionError(
            f"Release versions do not match expected {expected}: {details}. "
            f"Run: python tools/release_version.py --set {expected}"
        )
    return expected


def colabone_set_release_version(root: Path, raw_version: str) -> str:
    """Update all release-managed manifests under root to raw_version."""
    version = colabone_parse_version(raw_version)

    pending_writes: list[tuple[Path, dict[str, Any]]] = []
    for key in ("console", "frontend_package"):
        path = root / COLABONE_VERSION_FILES[key]
        data = _load_json(path)
        data["version"] = version
        pending_writes.append((path, data))

    lock_path = root / COLABONE_VERSION_FILES["frontend_lock"]
    if lock_path.is_file():
        lock_data = _load_json(lock_path)
        lock_data["version"] = version
        packages = lock_data.get("packages")
        if isinstance(packages, dict) and isinstance(packages.get(""), dict):
            packages[""]["version"] = version
        pending_writes.append((lock_path, lock_data))

    for path, data in pending_writes:
        path.write_text(
            json.dumps(data, indent=2, ensure_ascii=False) + "\n",
            encoding="utf-8",
            newline="\n",
        )

    return version


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Validate or update ColabOne release version manifests."
    )
    parser.add_argument(
        "--root",
        type=Path,
        default=Path(__file__).resolve().parents[1],
        help="Repository root directory.",
    )
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument(
        "--check",
        action="store_true",
        help="Check that all version manifests are valid and consistent.",
    )
    group.add_argument(
        "--tag",
        help="Validate that all version manifests match this release tag (e.g. v0.1.0).",
    )
    group.add_argument(
        "--set",
        dest="set_version",
        help="Update all version manifests to this version (e.g. 0.1.1).",
    )
    args = parser.parse_args(argv)

    try:
        if args.set_version is not None:
            updated = colabone_set_release_version(args.root, args.set_version)
            print(updated)
            return 0
        version = colabone_validate_release_versions(args.root, args.tag)
        print(version)
        return 0
    except ColabOneReleaseVersionError as error:
        print(f"Error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
