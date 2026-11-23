#!/usr/bin/env python3
"""Prepare ColabOne release version manifests and bilingual annotated-tag release notes."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from functools import total_ordering
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from typing import Any


COLABONE_REPO_ROOT = Path(__file__).resolve().parents[4]
_SEMVER_CORE = r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
_SEMVER_PRERELEASE_BODY = (
    r"(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
    r"(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*"
)
COLABONE_VERSION_RE = re.compile(
    rf"^{_SEMVER_CORE}(?:-({_SEMVER_PRERELEASE_BODY}))?$"
)
COLABONE_RELEASE_TAG_RE = re.compile(
    rf"^v({_SEMVER_CORE}(?:-({_SEMVER_PRERELEASE_BODY}))?)$"
)
COLABONE_MANIFEST_PATHS = (
    Path("console/mygo.json"),
    Path("frontend/package.json"),
    Path("frontend/package-lock.json"),
)
COLABONE_PROTECTED_PATHS = (
    *COLABONE_MANIFEST_PATHS,
    Path("tools/release_version.py"),
    Path(".github/workflows/release.yml"),
)


@total_ordering
@dataclass(frozen=True)
class ColabOneSemVer:
    """SemVer 2.0.0 version (MAJOR.MINOR.PATCH with optional -prerelease)."""

    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()

    @classmethod
    def parse(cls, raw: str, *, allow_prefix: bool = True) -> ColabOneSemVer:
        value = raw.strip()
        if allow_prefix and value.startswith(("v", "V")):
            value = value[1:]
        match = COLABONE_VERSION_RE.fullmatch(value)
        if match is None:
            raise SystemExit(
                f"invalid version (expected MAJOR.MINOR.PATCH or -prerelease): {raw}"
            )
        major, minor, patch, pre = match.groups()
        prerelease = tuple(pre.split(".")) if pre else ()
        return cls(int(major), int(minor), int(patch), prerelease)

    @classmethod
    def parse_tag(cls, tag: str) -> ColabOneSemVer | None:
        match = COLABONE_RELEASE_TAG_RE.fullmatch(tag.strip())
        if match is None:
            return None
        return cls.parse(match.group(1), allow_prefix=False)

    def __str__(self) -> str:
        core = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            return f"{core}-{'.'.join(self.prerelease)}"
        return core

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, ColabOneSemVer):
            return NotImplemented
        self_core = (self.major, self.minor, self.patch)
        other_core = (other.major, other.minor, other.patch)
        if self_core != other_core:
            return self_core < other_core
        if self.prerelease == other.prerelease:
            return False
        if not self.prerelease:
            return False
        if not other.prerelease:
            return True
        for left, right in zip(self.prerelease, other.prerelease):
            if left == right:
                continue
            left_num = left.isdigit()
            right_num = right.isdigit()
            if left_num and right_num:
                return int(left) < int(right)
            if left_num != right_num:
                return left_num
            return left < right
        return len(self.prerelease) < len(other.prerelease)

    def bump_patch(self) -> ColabOneSemVer:
        if self.prerelease:
            stable = f"{self.major}.{self.minor}.{self.patch}"
            raise SystemExit(
                f"cannot auto-increment patch from prerelease version {self}; "
                f"pass --version explicitly (for example {stable})"
            )
        return ColabOneSemVer(self.major, self.minor, self.patch + 1)


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Prepare ColabOne release manifests and annotated-tag release notes."
    )
    parser.add_argument(
        "--version",
        default="",
        help=(
            "Target semver with or without v prefix. Defaults to incrementing "
            "the last version number (patch) by +1."
        ),
    )
    parser.add_argument(
        "--added-zh",
        "--added",
        dest="added_zh",
        action="append",
        default=[],
        help="User-visible new feature item in Chinese (top section). Repeat as needed.",
    )
    parser.add_argument(
        "--changed-zh",
        "--changed",
        dest="changed_zh",
        action="append",
        default=[],
        help="User-visible improvement or change item in Chinese (top section). Repeat as needed.",
    )
    parser.add_argument(
        "--fixed-zh",
        "--fixed",
        dest="fixed_zh",
        action="append",
        default=[],
        help="User-visible bug fix item in Chinese (top section). Repeat as needed.",
    )
    parser.add_argument(
        "--added-en",
        dest="added_en",
        action="append",
        default=[],
        help="User-visible new feature item in English (bottom section). Must pair with --added-zh.",
    )
    parser.add_argument(
        "--changed-en",
        dest="changed_en",
        action="append",
        default=[],
        help="User-visible improvement or change item in English (bottom section). Must pair with --changed-zh.",
    )
    parser.add_argument(
        "--fixed-en",
        dest="fixed_en",
        action="append",
        default=[],
        help="User-visible bug fix item in English (bottom section). Must pair with --fixed-zh.",
    )
    parser.add_argument(
        "--repo-root",
        default=str(COLABONE_REPO_ROOT),
        help="ColabOne repository root directory.",
    )
    parser.add_argument(
        "--notes-output",
        default="",
        help=(
            "Release notes output path. Defaults to "
            "colabone-vVERSION-release-notes.md in the system temporary directory."
        ),
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Validate and preview the next version and release notes without modifying files.",
    )
    parser.add_argument(
        "--json",
        dest="json_output",
        action="store_true",
        help="Print a machine-readable JSON summary.",
    )
    return parser.parse_args(argv)


def _reject_duplicate_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise SystemExit(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def _load_json(path: Path) -> dict[str, Any]:
    try:
        data = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=_reject_duplicate_keys,
        )
    except FileNotFoundError as error:
        raise SystemExit(f"missing release version file: {path}") from error
    except (json.JSONDecodeError, OSError) as error:
        raise SystemExit(f"invalid JSON in {path}: {error}") from error
    if not isinstance(data, dict):
        raise SystemExit(f"expected JSON object in {path}")
    return data


def _require_manifest_version(data: dict[str, Any], label: str) -> ColabOneSemVer:
    value = data.get("version")
    if not isinstance(value, str):
        raise SystemExit(f"invalid semantic version in {label}: {value!r}")
    return ColabOneSemVer.parse(value, allow_prefix=False)


def colabone_run_git(repo_root: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", "-C", str(repo_root), *args],
        stdin=subprocess.DEVNULL,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
    )


def colabone_ensure_git_repository(repo_root: Path) -> None:
    result = colabone_run_git(repo_root, "rev-parse", "--is-inside-work-tree")
    if result.returncode != 0 or result.stdout.strip() != "true":
        raise SystemExit(f"not a Git repository: {repo_root}")


def colabone_collect_tag_versions(repo_root: Path) -> list[ColabOneSemVer]:
    result = colabone_run_git(repo_root, "tag", "--list", "v*")
    if result.returncode != 0:
        raise SystemExit("unable to read Git release tags")

    versions: list[ColabOneSemVer] = []
    for line in result.stdout.splitlines():
        parsed = ColabOneSemVer.parse_tag(line)
        if parsed is not None:
            versions.append(parsed)
    return versions


def colabone_tag_exists(repo_root: Path, tag: str) -> bool:
    result = colabone_run_git(
        repo_root, "show-ref", "--verify", "--quiet", f"refs/tags/{tag}"
    )
    return result.returncode == 0


def colabone_read_manifest_versions(
    repo_root: Path,
) -> list[tuple[str, ColabOneSemVer]]:
    console_rel = COLABONE_MANIFEST_PATHS[0]
    pkg_rel = COLABONE_MANIFEST_PATHS[1]
    lock_rel = COLABONE_MANIFEST_PATHS[2]

    versions: list[tuple[str, ColabOneSemVer]] = [
        (
            console_rel.as_posix(),
            _require_manifest_version(
                _load_json(repo_root / console_rel), console_rel.as_posix()
            ),
        ),
        (
            pkg_rel.as_posix(),
            _require_manifest_version(
                _load_json(repo_root / pkg_rel), pkg_rel.as_posix()
            ),
        ),
    ]

    lock_path = repo_root / lock_rel
    if lock_path.is_file():
        lock_data = _load_json(lock_path)
        lock_label = lock_rel.as_posix()
        versions.append((lock_label, _require_manifest_version(lock_data, lock_label)))
        packages = lock_data.get("packages")
        if isinstance(packages, dict) and isinstance(packages.get(""), dict):
            root_pkg_label = f"{lock_label}#packages['']"
            versions.append(
                (
                    root_pkg_label,
                    _require_manifest_version(packages[""], root_pkg_label),
                )
            )

    return versions


def colabone_require_aligned_source_version(repo_root: Path) -> ColabOneSemVer:
    manifest_versions = colabone_read_manifest_versions(repo_root)
    distinct = {version for _, version in manifest_versions}
    if len(distinct) != 1:
        details = ", ".join(
            f"{source}={version}" for source, version in manifest_versions
        )
        raise SystemExit(f"release source versions do not match: {details}")
    return manifest_versions[0][1]


def colabone_resolve_target_version(
    raw: str,
    source_version: ColabOneSemVer,
    tag_versions: list[ColabOneSemVer],
) -> tuple[ColabOneSemVer, str | None]:
    value = raw.strip()
    if value:
        target = ColabOneSemVer.parse(value, allow_prefix=True)
        if target in tag_versions:
            raise SystemExit(f"release tag v{target} already exists")
        if tag_versions:
            latest = max(tag_versions)
            if target <= latest:
                raise SystemExit(
                    f"target version {target} must be newer than latest tag v{latest}"
                )
        if target < source_version:
            raise SystemExit(
                f"target version {target} must not downgrade source version "
                f"{source_version}"
            )
        return target, None

    if tag_versions:
        latest = max(tag_versions)
        if source_version != latest:
            raise SystemExit(
                "cannot infer next version because release source version "
                f"{source_version} does not match latest tag v{latest}"
            )
        return latest.bump_patch(), f"latest tag v{latest}"

    return source_version.bump_patch(), f"current source version {source_version}"


def _normalize_cli_text(value: str) -> str:
    try:
        value.encode("utf-8")
    except UnicodeEncodeError:
        try:
            value = os.fsencode(value).decode("utf-8")
        except UnicodeDecodeError as error:
            raise SystemExit("release note item must be valid UTF-8") from error
    return value


@dataclass(frozen=True)
class ColabOneReleaseCopy:
    """Validated bilingual (Chinese top, English bottom) release note items."""

    added_zh: list[str]
    changed_zh: list[str]
    fixed_zh: list[str]
    added_en: list[str]
    changed_en: list[str]
    fixed_en: list[str]


def _validate_release_note_item(value: str) -> str:
    normalized = _normalize_cli_text(value).strip()
    if not normalized:
        raise SystemExit("release note item must not be empty")
    if len(normalized.splitlines()) != 1 or any(
        ord(character) < 32 or ord(character) == 127
        for character in normalized
    ):
        raise SystemExit(
            "release note item must be a single line without control characters"
        )
    return normalized


def colabone_validate_release_copy(
    *,
    added_zh: list[str],
    changed_zh: list[str],
    fixed_zh: list[str],
    added_en: list[str],
    changed_en: list[str],
    fixed_en: list[str],
) -> ColabOneReleaseCopy:
    copy = ColabOneReleaseCopy(
        added_zh=[_validate_release_note_item(item) for item in added_zh],
        changed_zh=[_validate_release_note_item(item) for item in changed_zh],
        fixed_zh=[_validate_release_note_item(item) for item in fixed_zh],
        added_en=[_validate_release_note_item(item) for item in added_en],
        changed_en=[_validate_release_note_item(item) for item in changed_en],
        fixed_en=[_validate_release_note_item(item) for item in fixed_en],
    )
    pairs = (
        ("added", copy.added_zh, copy.added_en),
        ("changed", copy.changed_zh, copy.changed_en),
        ("fixed", copy.fixed_zh, copy.fixed_en),
    )
    if not any(zh or en for _, zh, en in pairs):
        raise SystemExit("at least one bilingual release note item is required")

    for category, zh_items, en_items in pairs:
        if zh_items and not en_items:
            raise SystemExit(
                f"bilingual release notes require --{category}-en items "
                f"to match --{category}-zh"
            )
        if en_items and not zh_items:
            raise SystemExit(
                f"bilingual release notes require --{category}-zh items "
                f"to match --{category}-en"
            )
        if len(zh_items) != len(en_items):
            raise SystemExit(
                f"bilingual release notes count mismatch for {category}: "
                f"got {len(zh_items)} Chinese item(s) and "
                f"{len(en_items)} English item(s)"
            )

    return copy


def _render_language_block(
    sections: list[tuple[str, list[str]]],
) -> list[str]:
    lines: list[str] = []
    for title, items in sections:
        if not items:
            continue
        lines.append(title)
        lines.extend(f"- {item}" for item in items)
        lines.append("")
    return lines


def colabone_render_release_notes(
    version: ColabOneSemVer,
    copy: ColabOneReleaseCopy,
) -> str:
    zh_sections = [
        ("## 新增功能", copy.added_zh),
        ("## 修改点", copy.changed_zh),
        ("## 修复的 bug", copy.fixed_zh),
    ]
    en_sections = [
        ("## New Features", copy.added_en),
        ("## Changes", copy.changed_en),
        ("## Bug Fixes", copy.fixed_en),
    ]
    lines = [f"ColabOne v{version}", ""]
    lines.extend(_render_language_block(zh_sections))
    lines.extend(["---", ""])
    lines.extend(_render_language_block(en_sections))
    return "\n".join(lines).rstrip() + "\n"


def colabone_render_manifest_updates(
    repo_root: Path, version: ColabOneSemVer
) -> dict[Path, str]:
    version_text = str(version)
    rendered: dict[Path, str] = {}

    for rel in COLABONE_MANIFEST_PATHS[:2]:
        path = repo_root / rel
        data = _load_json(path)
        data["version"] = version_text
        rendered[path] = json.dumps(data, indent=2, ensure_ascii=False) + "\n"

    lock_path = repo_root / COLABONE_MANIFEST_PATHS[2]
    if lock_path.is_file():
        lock_data = _load_json(lock_path)
        lock_data["version"] = version_text
        packages = lock_data.get("packages")
        if isinstance(packages, dict) and isinstance(packages.get(""), dict):
            packages[""]["version"] = version_text
        rendered[lock_path] = (
            json.dumps(lock_data, indent=2, ensure_ascii=False) + "\n"
        )

    return rendered


def colabone_write_prepared_files(files: dict[Path, str]) -> None:
    staged: dict[Path, Path] = {}
    backups: dict[Path, Path | None] = {}
    replaced: list[Path] = []
    retained_backups: set[Path] = set()
    try:
        for path, content in files.items():
            path.parent.mkdir(parents=True, exist_ok=True)
            descriptor, temporary_name = tempfile.mkstemp(
                prefix=f".{path.name}.", dir=path.parent
            )
            temporary_path = Path(temporary_name)
            staged[path] = temporary_path
            with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as handle:
                handle.write(content)
                handle.flush()
                os.fsync(handle.fileno())
            if path.exists():
                os.chmod(temporary_path, path.stat().st_mode)

        for path in files:
            if path.exists():
                descriptor, backup_name = tempfile.mkstemp(
                    prefix=f".{path.name}.backup.", dir=path.parent
                )
                os.close(descriptor)
                backup_path = Path(backup_name)
                backups[path] = backup_path
                shutil.copy2(path, backup_path)
            else:
                backups[path] = None
            os.replace(staged[path], path)
            replaced.append(path)
    except BaseException as error:
        retained_backups.update(
            backup for path in replaced if (backup := backups[path]) is not None
        )
        failures: list[str] = []
        for path in reversed(replaced):
            backup = backups[path]
            try:
                if backup is None:
                    path.unlink(missing_ok=True)
                else:
                    os.replace(backup, path)
            except OSError as rollback_error:
                failures.append(
                    f"{path}: {rollback_error} (recovery backup: {backup})"
                )
        if failures:
            raise RuntimeError(
                "Release preparation failed and rollback was incomplete: "
                + "; ".join(failures)
            ) from error
        raise
    finally:
        for path in staged.values():
            path.unlink(missing_ok=True)
        for path in backups.values():
            if path is not None and path not in retained_backups:
                path.unlink(missing_ok=True)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    repo_root = Path(args.repo_root).resolve()
    colabone_ensure_git_repository(repo_root)
    source_version = colabone_require_aligned_source_version(repo_root)
    tag_versions = colabone_collect_tag_versions(repo_root)
    target_version, inferred_from = colabone_resolve_target_version(
        args.version, source_version, tag_versions
    )
    tag = f"v{target_version}"
    if colabone_tag_exists(repo_root, tag):
        raise SystemExit(f"release tag {tag} already exists")

    release_copy = colabone_validate_release_copy(
        added_zh=args.added_zh,
        changed_zh=args.changed_zh,
        fixed_zh=args.fixed_zh,
        added_en=args.added_en,
        changed_en=args.changed_en,
        fixed_en=args.fixed_en,
    )

    notes_path = (
        Path(args.notes_output).expanduser().resolve()
        if args.notes_output
        else (
            Path(tempfile.gettempdir()) / f"colabone-{tag}-release-notes.md"
        ).resolve()
    )
    protected_paths = {(repo_root / rel).resolve() for rel in COLABONE_PROTECTED_PATHS}
    if notes_path in protected_paths:
        raise SystemExit(
            "release notes output must not overwrite a release-managed file"
        )

    manifest_updates = colabone_render_manifest_updates(repo_root, target_version)
    release_notes = colabone_render_release_notes(target_version, release_copy)
    updated_rel_paths = [
        path.relative_to(repo_root).as_posix() for path in manifest_updates
    ]

    if not args.dry_run:
        colabone_write_prepared_files({**manifest_updates, notes_path: release_notes})

    payload = {
        "version": str(target_version),
        "tag": tag,
        "previous_version": str(source_version),
        "inferred_from": inferred_from,
        "updated_files": updated_rel_paths,
        "notes_path": str(notes_path),
        "dry_run": bool(args.dry_run),
    }
    if args.json_output:
        if args.dry_run:
            payload["release_notes"] = release_notes
        print(json.dumps(payload))
        return 0

    if inferred_from:
        print(f"Resolved target version {tag} from {inferred_from}")
    if args.dry_run:
        print(f"[dry-run] Would prepare ColabOne {tag}")
    else:
        print(f"Prepared ColabOne {tag}")
    print(f"Updated files: {', '.join(updated_rel_paths)}")
    print(f"Release notes: {notes_path}")
    if args.dry_run:
        print("\n--- Release notes preview ---")
        encoding = sys.stdout.encoding or "utf-8"
        sys.stdout.write(
            release_notes.encode(encoding, errors="replace").decode(encoding)
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
