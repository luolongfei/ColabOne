"""Build native pure Go installers (.exe/.dmg) and updater artifacts, excluding state and credentials."""

from __future__ import annotations

import argparse
import json
import re
import shutil
from pathlib import Path

if __package__:
    from . import install
else:
    import install

FORBIDDEN_LEGACY_DIRS = ("bridge", "runtime", "site-packages", "server", "common", "catalog")

_ARCHIVE_PLATFORM_MAP = {
    ("darwin", "arm64"): "macos-arm64",
    ("darwin", "amd64"): "macos-x64",
    ("windows", "amd64"): "windows-x64",
    ("windows", "arm64"): "windows-arm64",
    ("linux", "amd64"): "linux-x64",
    ("linux", "arm64"): "linux-arm64",
}


def _archive_platform(goos: str | None = None, goarch: str | None = None) -> str:
    target_os = (goos or install._host_goos()).lower()
    target_arch = (goarch or install._host_goarch()).lower()
    mapped = _ARCHIVE_PLATFORM_MAP.get((target_os, target_arch))
    if mapped:
        return mapped
    os_label = "macos" if target_os == "darwin" else target_os
    arch_label = "x64" if target_arch == "amd64" else target_arch
    return f"{os_label}-{arch_label}"


def colabone_installer_filename(version: str, goos: str, goarch: str) -> str | None:
    """Return the user-facing release installer filename for Windows/macOS, or None."""
    target_os = goos.lower()
    platform = _archive_platform(target_os, goarch)
    if target_os == "windows":
        return f"ColabOne-{version}-{platform}-setup.exe"
    if target_os == "darwin":
        return f"ColabOne-{version}-{platform}.dmg"
    return None


def audit(root):
    root = Path(root)
    sensitive = re.compile(
        rb"(?:ya29\.[\w-]{20,}|1//[\w-]{40,}|tskey-[a-z]+-[A-Za-z0-9]{10,}|hf_[A-Za-z0-9]{30,}|co_[A-Za-z0-9_-]{40,}|eyJ[A-Za-z0-9_+/=-]{30,})"
    )
    for path in root.rglob("*"):
        rel_parts = path.relative_to(root).parts
        for forbidden in FORBIDDEN_LEGACY_DIRS:
            if forbidden in rel_parts:
                raise RuntimeError(
                    f"Forbidden legacy directory found in pure Go package: {forbidden}/"
                )
        if not path.is_file():
            continue
        if (
            path.name
            in {
                "settings.json",
                "token.json",
                "credentials.json",
                "vault.enc",
                ".cloudflared-token",
                ".env",
            }
            or path.name.startswith("vault.enc.")
            or path.suffix in (".gguf", ".colabone")
        ):
            raise RuntimeError("Private state found in package: " + path.name)
        if path.name.startswith("update-") and path.suffix == ".json":
            continue
        if path.suffix in (".py", ".json", ".md", ".txt"):
            if sensitive.search(path.read_bytes()):
                raise RuntimeError("Credential pattern in package: " + path.name)


def stage_platform_installer(
    platform_dir: Path,
    out: Path,
    version: str,
    goos: str,
    goarch: str,
    slug: str = "colabone",
) -> Path | None:
    """Copy the MyGo-built native installer (.exe on Windows, .dmg on macOS) into out."""
    target_os = goos.lower()
    target_arch = goarch.lower()
    dst_name = colabone_installer_filename(version, target_os, target_arch)
    if dst_name is None:
        return None

    if target_os == "windows":
        src = platform_dir / f"{slug}-setup-{version}-{target_arch}.exe"
        label = "Windows NSIS installer"
    else:
        src = platform_dir / f"{slug}-{version}-{target_arch}.dmg"
        label = "macOS disk image"

    if not src.is_file() or src.stat().st_size <= 0:
        raise RuntimeError(
            f"Missing or empty {label} {src.name} in {platform_dir}"
        )

    dst = (out / dst_name).resolve()
    shutil.copy2(src, dst)
    return dst


def stage_mygo_updater_artifacts(
    platform_dir: Path,
    out: Path,
    version: str,
    goos: str,
    goarch: str,
    slug: str = "colabone",
) -> list[Path]:
    """Copy MyGo signed updater artifacts (.tar.gz, .delta, update-<target>.json) into out."""
    target = f"{goos}-{goarch}"
    manifest_src = platform_dir / f"update-{target}.json"
    if not manifest_src.is_file():
        return []

    manifest_raw = manifest_src.read_text(encoding="utf-8")
    manifest = json.loads(manifest_raw)
    if str(manifest.get("version") or "").lstrip("vV") != version:
        raise RuntimeError(
            f"MyGo updater manifest {manifest_src.name} version {manifest.get('version')!r} != {version!r}"
        )
    if not manifest.get("signature"):
        raise RuntimeError(f"MyGo updater manifest {manifest_src.name} is missing signature")

    archive_src = platform_dir / f"{slug}-{version}-{target}.tar.gz"
    if not archive_src.is_file() or archive_src.stat().st_size <= 0:
        raise RuntimeError(
            f"MyGo updater manifest {manifest_src.name} is missing archive {archive_src.name}"
        )

    copied: list[Path] = []
    candidates = [
        archive_src,
        *sorted(platform_dir.glob(f"{slug}-*-to-{version}-{target}.delta")),
        manifest_src,
    ]
    for src in candidates:
        if not src.is_file():
            continue
        dst = out / src.name
        shutil.copy2(src, dst)
        copied.append(dst)
    return copied


def main(argv: list[str] | None = None):
    p = argparse.ArgumentParser()
    p.add_argument("--out", default="dist")
    p.add_argument(
        "--portable-zip",
        action="store_true",
        help="Also emit a portable .zip archive alongside native installers on Windows/macOS",
    )
    a = p.parse_args(argv)
    out = Path(a.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    stage_root = install.ROOT / "work/package-stage"
    if stage_root.exists():
        shutil.rmtree(stage_root)
    goos = install._host_goos()
    goarch = install._host_goarch()
    build_installer = goos in ("windows", "darwin")
    target = install.install(str(stage_root), build_installer=build_installer)
    audit(target)
    manifest = json.loads(
        (install.ROOT / "console/mygo.json").read_text(encoding="utf-8")
    )
    version = manifest["version"]
    slug = str(manifest.get("slug") or manifest.get("name") or "colabone").strip().lower()
    platform_dir = install.ROOT / "console/dist" / f"{goos}-{goarch}"
    if platform_dir.is_dir():
        audit(platform_dir)

    written: list[Path] = []
    try:
        primary = stage_platform_installer(
            platform_dir, out, version, goos, goarch, slug=slug
        )
        if primary is not None:
            written.append(primary)
        if primary is None or a.portable_zip:
            platform = _archive_platform(goos, goarch)
            zip_file = Path(
                shutil.make_archive(
                    str(out / f"ColabOne-{version}-{platform}"),
                    "zip",
                    target.parent,
                    target.name,
                )
            ).resolve()
            written.append(zip_file)
            if primary is None:
                primary = zip_file

        written.extend(
            stage_mygo_updater_artifacts(
                platform_dir, out, version, goos, goarch, slug=slug
            )
        )
    except BaseException:
        for artifact in written:
            artifact.unlink(missing_ok=True)
        raise

    print(primary)


if __name__ == "__main__":
    main()
