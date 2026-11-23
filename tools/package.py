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
    platform = _archive_platform(install._host_goos(), install._host_goarch())
    file = shutil.make_archive(
        str(out / f"ColabOne-{version}-{platform}"), "zip", target.parent, target.name
    )
    print(file)


if __name__ == "__main__":
    main()
