"""Create a portable development zip, excluding state and credentials."""

import argparse
import json
import re
import shutil
import sys
from pathlib import Path
import install


def audit(root):
    sensitive = re.compile(
        rb"(?:ya29\.[\w-]{20,}|1//[\w-]{40,}|tskey-[a-z]+-[A-Za-z0-9]{10,}|hf_[A-Za-z0-9]{30,})"
    )
    for path in root.rglob("*"):
        if not path.is_file():
            continue
        if (
            path.name in {"settings.json", "token.json", "credentials.json", ".env"}
            or path.suffix == ".gguf"
        ):
            raise RuntimeError("Private state found in package: " + path.name)
        if (
            path.suffix in (".py", ".json", ".md", ".txt")
            and "site-packages" not in path.parts
        ):
            if sensitive.search(path.read_bytes()):
                raise RuntimeError("Credential pattern in package: " + path.name)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--out", default="dist")
    a = p.parse_args()
    out = Path(a.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    target = install.install(str(install.ROOT / "work/package-stage"))
    audit(target)
    version = json.loads((install.ROOT / "console/mygo.json").read_text(encoding="utf-8"))["version"]
    platform = "macos-arm64" if sys.platform == "darwin" else "windows-x64"
    file = shutil.make_archive(
        str(out / f"ColabOne-{version}-{platform}"), "zip", target.parent, target.name
    )
    print(file)


if __name__ == "__main__":
    main()
