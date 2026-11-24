#!/usr/bin/env python3
"""Validate MyGo update manifests/archives and inject annotated-tag bilingual notes."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import sys
from typing import Sequence

DEFAULT_TARGETS = ("windows-amd64", "darwin-arm64")


class ColabOneUpdateManifestError(ValueError):
    """Raised when signed MyGo update artifacts are missing or inconsistent."""


def _load_manifest(path: Path) -> dict:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ColabOneUpdateManifestError(
            f"Missing MyGo update manifest {path.name}; ensure MYGO_UPDATER_PRIVATE_KEY is configured in GitHub Actions secrets."
        ) from exc
    except json.JSONDecodeError as exc:
        raise ColabOneUpdateManifestError(
            f"Invalid JSON in {path.name}: {exc}"
        ) from exc
    if not isinstance(data, dict):
        raise ColabOneUpdateManifestError(f"Expected JSON object in {path.name}")
    return data


def _asset_name_from_url(raw_url: str, label: str) -> str:
    if not isinstance(raw_url, str) or not raw_url.startswith("https://"):
        raise ColabOneUpdateManifestError(f"{label} must have an https URL: {raw_url!r}")
    name = raw_url.rstrip("/").rsplit("/", 1)[-1]
    if not name or name in (".", ".."):
        raise ColabOneUpdateManifestError(f"{label} has invalid asset filename: {raw_url!r}")
    return name


def colabone_prepare_update_manifests(
    dist_dir: Path,
    version: str,
    notes: str,
    targets: Sequence[str] = DEFAULT_TARGETS,
) -> list[Path]:
    """Verify signed update artifacts for all targets and inject release notes."""
    dist_dir = Path(dist_dir)
    expected_version = version.lstrip("vV").strip()
    if not expected_version:
        raise ColabOneUpdateManifestError("Release version must not be empty")

    cleaned_notes = notes.replace("\r\n", "\n").strip()
    updated_paths: list[Path] = []

    for target in targets:
        manifest_path = dist_dir / f"update-{target}.json"
        manifest = _load_manifest(manifest_path)

        got_version = str(manifest.get("version") or "").lstrip("vV").strip()
        if got_version != expected_version:
            raise ColabOneUpdateManifestError(
                f"{manifest_path.name} version {got_version!r} does not match expected {expected_version!r}"
            )

        sig = manifest.get("signature")
        if not isinstance(sig, str) or not sig.strip():
            raise ColabOneUpdateManifestError(
                f"{manifest_path.name} is missing Ed25519 signature"
            )

        archive_name = _asset_name_from_url(manifest.get("url", ""), f"{manifest_path.name}.url")
        archive_path = dist_dir / archive_name
        if not archive_path.is_file():
            raise ColabOneUpdateManifestError(
                f"Missing signed update archive {archive_name} referenced by {manifest_path.name}"
            )

        expected_size = manifest.get("size")
        actual_size = archive_path.stat().st_size
        if not isinstance(expected_size, int) or expected_size <= 0 or expected_size != actual_size:
            raise ColabOneUpdateManifestError(
                f"{archive_name} size mismatch: manifest={expected_size!r}, disk={actual_size}"
            )

        deltas = manifest.get("deltas")
        if deltas is not None:
            if not isinstance(deltas, list):
                raise ColabOneUpdateManifestError(f"{manifest_path.name}.deltas must be a list")
            for idx, delta in enumerate(deltas):
                if not isinstance(delta, dict):
                    raise ColabOneUpdateManifestError(
                        f"{manifest_path.name}.deltas[{idx}] must be an object"
                    )
                if not isinstance(delta.get("signature"), str) or not delta["signature"].strip():
                    raise ColabOneUpdateManifestError(
                        f"{manifest_path.name}.deltas[{idx}] is missing signature"
                    )
                delta_name = _asset_name_from_url(
                    delta.get("url", ""), f"{manifest_path.name}.deltas[{idx}].url"
                )
                delta_path = dist_dir / delta_name
                if not delta_path.is_file():
                    raise ColabOneUpdateManifestError(
                        f"Missing delta patch {delta_name} referenced by {manifest_path.name}"
                    )
                delta_size = delta.get("size")
                disk_delta_size = delta_path.stat().st_size
                if (
                    not isinstance(delta_size, int)
                    or delta_size <= 0
                    or delta_size != disk_delta_size
                ):
                    raise ColabOneUpdateManifestError(
                        f"{delta_name} size mismatch: manifest={delta_size!r}, disk={disk_delta_size}"
                    )

        if cleaned_notes:
            manifest["notes"] = cleaned_notes

        manifest_path.write_text(
            json.dumps(manifest, indent=2, ensure_ascii=False) + "\n",
            encoding="utf-8",
            newline="\n",
        )
        updated_paths.append(manifest_path)

    return updated_paths


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Validate MyGo signed update manifests and inject bilingual release notes."
    )
    parser.add_argument("--dist", required=True, type=Path, help="Directory holding release dist artifacts")
    parser.add_argument("--version", required=True, help="Expected SemVer release version (e.g. 0.1.7)")
    parser.add_argument("--notes-file", type=Path, default=None, help="Markdown file with bilingual release notes")
    parser.add_argument(
        "--targets",
        nargs="+",
        default=list(DEFAULT_TARGETS),
        help="MyGo update target names (default: windows-amd64 darwin-arm64)",
    )
    args = parser.parse_args(argv)

    notes = ""
    if args.notes_file is not None and args.notes_file.is_file():
        notes = args.notes_file.read_text(encoding="utf-8")

    try:
        updated = colabone_prepare_update_manifests(
            dist_dir=args.dist,
            version=args.version,
            notes=notes,
            targets=args.targets,
        )
    except ColabOneUpdateManifestError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1

    for path in updated:
        print(path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
