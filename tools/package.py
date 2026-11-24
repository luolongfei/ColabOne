"""Create a portable pure Go distribution zip, excluding state and credentials."""

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
                ".cloudflared-token",
                ".env",
            }
            or path.suffix == ".gguf"
        ):
            raise RuntimeError("Private state found in package: " + path.name)
        if path.suffix in (".py", ".json", ".md", ".txt"):
            if sensitive.search(path.read_bytes()):
                raise RuntimeError("Credential pattern in package: " + path.name)


def stage_mygo_updater_artifacts(
    platform_dir: Path,
    out: Path,
    version: str,
    goos: str,
    goarch: str,
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

    archive_src = platform_dir / f"colabone-{version}-{target}.tar.gz"
    if not archive_src.is_file():
        raise RuntimeError(
            f"MyGo updater manifest {manifest_src.name} is missing archive {archive_src.name}"
        )

    copied: list[Path] = []
    candidates = [
        archive_src,
        *sorted(platform_dir.glob(f"colabone-*-to-{version}-{target}.delta")),
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
    a = p.parse_args(argv)
    out = Path(a.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    stage_root = install.ROOT / "work/package-stage"
    if stage_root.exists():
        shutil.rmtree(stage_root)
    target = install.install(str(stage_root))
    audit(target)
    version = json.loads(
        (install.ROOT / "console/mygo.json").read_text(encoding="utf-8")
    )["version"]
    goos = install._host_goos()
    goarch = install._host_goarch()
    platform = _archive_platform(goos, goarch)
    file = shutil.make_archive(
        str(out / f"ColabOne-{version}-{platform}"), "zip", target.parent, target.name
    )
    platform_dir = install.ROOT / "console/dist" / f"{goos}-{goarch}"
    stage_mygo_updater_artifacts(platform_dir, out, version, goos, goarch)
    print(file)


if __name__ == "__main__":
    main()
