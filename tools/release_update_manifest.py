#!/usr/bin/env python3
"""Validate MyGo update manifests/archives and inject annotated-tag bilingual notes."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import stat
import sys
import tempfile
import time
from typing import Sequence
import urllib.parse

DEFAULT_TARGETS = ("windows-amd64", "darwin-arm64")
DEFAULT_GITHUB_REPO = "luolongfei/ColabOne"
_GITHUB_REPO_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
_TARGET_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9_-]{0,63}$")
_BILINGUAL_BLANK_DIVIDER_RE = re.compile(r"\n\n[ \t]*---[ \t]*\n\n")
_BILINGUAL_DIVIDER_RE = re.compile(r"\n[ \t]*---[ \t]*\n")
_ZH_DOWNLOADS_HEADING_RE = re.compile(r"^##\s+下载安装\s*$", re.MULTILINE)
_EN_DOWNLOADS_HEADING_RE = re.compile(r"^##\s+Downloads\s*$", re.MULTILINE)


class ColabOneUpdateManifestError(ValueError):
    """Raised when signed MyGo update artifacts are missing or inconsistent."""


def _atomic_write_text(path: Path, text: str) -> None:
    if path.is_symlink():
        raise ColabOneUpdateManifestError(f"Refusing to write through symlink: {path}")
    if path.exists() and not path.is_file():
        raise ColabOneUpdateManifestError(f"Refusing to overwrite non-file path: {path}")
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(
        prefix=f".{path.name}.", suffix=".tmp", dir=str(path.parent)
    )
    tmp_path = Path(tmp_name)
    try:
        try:
            stream = os.fdopen(fd, "w", encoding="utf-8", newline="\n")
        except BaseException:
            os.close(fd)
            raise
        with stream:
            stream.write(text)
            stream.flush()
            os.fsync(stream.fileno())
        if os.name == "nt" and path.exists() and not path.is_symlink():
            try:
                os.chmod(path, stat.S_IMODE(path.stat().st_mode) | stat.S_IWRITE)
            except OSError:
                pass
        max_attempts = 5 if os.name == "nt" else 1
        for attempt in range(max_attempts):
            try:
                os.replace(tmp_path, path)
                break
            except OSError as exc:
                if (
                    attempt + 1 >= max_attempts
                    or os.name != "nt"
                    or getattr(exc, "winerror", None) not in (5, 32, 33)
                ):
                    raise
                time.sleep(0.05 * (attempt + 1))
    except BaseException:
        tmp_path.unlink(missing_ok=True)
        raise


def colabone_render_github_release_body(
    notes: str,
    version: str,
    *,
    repo: str = DEFAULT_GITHUB_REPO,
    tag: str | None = None,
) -> str:
    """Append bilingual direct installer download links to GitHub Release notes."""
    clean_version = version.strip().lstrip("vV").strip()
    if not clean_version:
        raise ColabOneUpdateManifestError("Release version must not be empty")
    clean_tag = (tag or f"v{clean_version}").strip()
    if not clean_tag:
        raise ColabOneUpdateManifestError("Release tag must not be empty")
    clean_repo = (repo or DEFAULT_GITHUB_REPO).strip().strip("/")
    if not _GITHUB_REPO_RE.fullmatch(clean_repo):
        raise ColabOneUpdateManifestError(f"Invalid GitHub repository {repo!r}; expected owner/repo")

    win_name = f"ColabOne-{clean_version}-windows-x64-setup.exe"
    mac_name = f"ColabOne-{clean_version}-macos-arm64.dmg"
    base_url = (
        f"https://github.com/{clean_repo}/releases/download/"
        f"{urllib.parse.quote(clean_tag, safe='')}"
    )
    win_url = f"{base_url}/{urllib.parse.quote(win_name, safe='')}"
    mac_url = f"{base_url}/{urllib.parse.quote(mac_name, safe='')}"

    zh_block = "\n".join(
        [
            "## 下载安装",
            f"- **Windows (x64)**：[{win_name}]({win_url})（双击安装，无需管理员权限；若系统提示未知发布者，点「更多信息 / 仍要运行」）",
            f"- **macOS (Apple Silicon)**：[{mac_name}]({mac_url})（打开后拖入「应用程序」）",
            "  - **macOS 首次打开提示“未打开 / 无法验证”**：点「完成」，打开「系统设置 / 隐私与安全性」，底部点「仍要打开」，或在终端运行 `xattr -dr com.apple.quarantine /Applications/ColabOne.app`（开源构建未购买商业签名，操作一次即可）。",
            "- 其余 `.tar.gz`、`.delta`、`update-*.json` 为应用内自动更新专用文件，无需手动下载。",
        ]
    )
    en_block = "\n".join(
        [
            "## Downloads",
            f"- **Windows (x64)**: [{win_name}]({win_url}) (no admin required; if SmartScreen prompts, click **More info / Run anyway**)",
            f"- **macOS (Apple Silicon)**: [{mac_name}]({mac_url}) (open and drag to **Applications**)",
            '  - **macOS first-launch warning ("Not Opened" / cannot verify)**: click **Done**, open **System Settings / Privacy & Security**, then click **Open Anyway**, or run `xattr -dr com.apple.quarantine /Applications/ColabOne.app` (unsigned open-source build; one-time step).',
            "- `.tar.gz`, `.delta`, and `update-*.json` files are for in-app auto-updates only — no manual download needed.",
        ]
    )

    cleaned = notes.replace("\r\n", "\n").replace("\r", "\n").strip()
    if not cleaned:
        return f"ColabOne {clean_tag}\n\n{zh_block}\n\n---\n\n{en_block}\n"
    has_zh = bool(_ZH_DOWNLOADS_HEADING_RE.search(cleaned))
    has_en = bool(_EN_DOWNLOADS_HEADING_RE.search(cleaned))
    if has_zh and has_en:
        return cleaned + "\n"

    match = _BILINGUAL_BLANK_DIVIDER_RE.search(cleaned) or _BILINGUAL_DIVIDER_RE.search(cleaned)
    if match is not None:
        zh_part = cleaned[: match.start()].rstrip()
        en_part = cleaned[match.end() :].strip()
        zh_section = zh_part if has_zh else f"{zh_part}\n\n{zh_block}"
        en_section = en_part if has_en else f"{en_part}\n\n{en_block}"
        return f"{zh_section}\n\n---\n\n{en_section}\n"

    if has_zh:
        return f"{cleaned}\n\n---\n\n{en_block}\n"
    if has_en:
        return f"{zh_block}\n\n---\n\n{cleaned}\n"
    return f"{cleaned}\n\n{zh_block}\n\n---\n\n{en_block}\n"


def _load_manifest(path: Path) -> dict:
    if path.is_symlink():
        raise ColabOneUpdateManifestError(f"Refusing symlink manifest {path.name}")
    if path.exists() and not path.is_file():
        raise ColabOneUpdateManifestError(f"Expected regular file manifest {path.name}")
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
    if (
        not isinstance(raw_url, str)
        or not raw_url.startswith("https://")
        or "?" in raw_url
        or "#" in raw_url
        or any(ch.isspace() or ord(ch) < 32 or ord(ch) == 127 for ch in raw_url)
    ):
        raise ColabOneUpdateManifestError(f"{label} must have an https URL: {raw_url!r}")
    try:
        parsed = urllib.parse.urlsplit(raw_url)
    except ValueError as exc:
        raise ColabOneUpdateManifestError(f"{label} must have an https URL: {raw_url!r}") from exc
    if (
        parsed.scheme != "https"
        or not parsed.netloc
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
    ):
        raise ColabOneUpdateManifestError(f"{label} must have an https URL: {raw_url!r}")
    raw_path = parsed.path
    if not raw_path or raw_path.endswith("/") or "/" not in raw_path:
        raise ColabOneUpdateManifestError(f"{label} has invalid asset filename: {raw_url!r}")
    name = urllib.parse.unquote(raw_path.rsplit("/", 1)[-1])
    if (
        not name
        or name in (".", "..")
        or "/" in name
        or "\\" in name
        or ":" in name
        or ".." in name
        or any(ord(ch) < 32 or ord(ch) == 127 for ch in name)
    ):
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
    expected_version = version.strip().lstrip("vV").strip()
    if not expected_version:
        raise ColabOneUpdateManifestError("Release version must not be empty")
    if not targets:
        raise ColabOneUpdateManifestError("At least one update target must be specified")

    cleaned_notes = notes.replace("\r\n", "\n").replace("\r", "\n").strip()
    prepared_writes: list[tuple[Path, str]] = []

    for target in targets:
        if not isinstance(target, str) or not _TARGET_NAME_RE.fullmatch(target):
            raise ColabOneUpdateManifestError(f"Invalid update target name: {target!r}")
        manifest_path = dist_dir / f"update-{target}.json"
        manifest = _load_manifest(manifest_path)

        got_version = str(manifest.get("version") or "").strip().lstrip("vV").strip()
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
        if archive_path.is_symlink() or not archive_path.is_file():
            raise ColabOneUpdateManifestError(
                f"Missing signed update archive {archive_name} referenced by {manifest_path.name}"
            )

        expected_size = manifest.get("size")
        actual_size = archive_path.stat().st_size
        if (
            isinstance(expected_size, bool)
            or not isinstance(expected_size, int)
            or expected_size <= 0
            or expected_size != actual_size
        ):
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
                if delta_path.is_symlink() or not delta_path.is_file():
                    raise ColabOneUpdateManifestError(
                        f"Missing delta patch {delta_name} referenced by {manifest_path.name}"
                    )
                delta_size = delta.get("size")
                disk_delta_size = delta_path.stat().st_size
                if (
                    isinstance(delta_size, bool)
                    or not isinstance(delta_size, int)
                    or delta_size <= 0
                    or delta_size != disk_delta_size
                ):
                    raise ColabOneUpdateManifestError(
                        f"{delta_name} size mismatch: manifest={delta_size!r}, disk={disk_delta_size}"
                    )

        if cleaned_notes:
            manifest["notes"] = cleaned_notes

        prepared_writes.append(
            (manifest_path, json.dumps(manifest, indent=2, ensure_ascii=False) + "\n")
        )

    updated_paths: list[Path] = []
    for manifest_path, serialized in prepared_writes:
        _atomic_write_text(manifest_path, serialized)
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
        "--release-body-output",
        type=Path,
        default=None,
        help="Optional output path for GitHub Release body enriched with direct installer download links",
    )
    parser.add_argument(
        "--repo",
        default=DEFAULT_GITHUB_REPO,
        help="GitHub owner/repo used when rendering direct installer download links",
    )
    parser.add_argument(
        "--tag",
        default="",
        help="GitHub release tag (defaults to v<version>) used when rendering download links",
    )
    parser.add_argument(
        "--targets",
        nargs="+",
        default=list(DEFAULT_TARGETS),
        help="MyGo update target names (default: windows-amd64 darwin-arm64)",
    )
    args = parser.parse_args(argv)

    notes = ""
    if args.notes_file is not None:
        if args.notes_file.is_symlink():
            print(f"Error: Refusing symlink notes file: {args.notes_file}", file=sys.stderr)
            return 1
        if not args.notes_file.is_file():
            raise FileNotFoundError(f"Release notes file not found: {args.notes_file}")
        notes = args.notes_file.read_text(encoding="utf-8")

    try:
        rendered_body: str | None = None
        if args.release_body_output is not None:
            if args.release_body_output.is_symlink():
                raise ColabOneUpdateManifestError(
                    f"Refusing to write through symlink: {args.release_body_output}"
                )
            if args.release_body_output.exists() and not args.release_body_output.is_file():
                raise ColabOneUpdateManifestError(
                    f"Refusing to overwrite non-file path: {args.release_body_output}"
                )
            rendered_body = colabone_render_github_release_body(
                notes,
                args.version,
                repo=args.repo,
                tag=args.tag or None,
            )
        updated = colabone_prepare_update_manifests(
            dist_dir=args.dist,
            version=args.version,
            notes=notes,
            targets=args.targets,
        )
        if args.release_body_output is not None and rendered_body is not None:
            _atomic_write_text(args.release_body_output, rendered_body)
    except ColabOneUpdateManifestError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1

    for path in updated:
        print(path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
