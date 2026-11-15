#!/usr/bin/env python3
"""Reproducible developer check and portable install; never starts a GPU."""

from __future__ import annotations
import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path
import runtime_bundle as bundle

ROOT = Path(__file__).resolve().parents[1]


def run(args, cwd=ROOT, **kwargs):
    subprocess.run(args, cwd=cwd, check=True, **kwargs)


def check():
    run(
        [
            sys.executable,
            "-m",
            "compileall",
            "-q",
            "bridge",
            "server",
            "common",
            "tools",
        ]
    )
    run(["go", "tool", "mygo", "generate"], ROOT / "console")
    run(["npm.cmd" if os.name == "nt" else "npm", "run", "build"], ROOT / "frontend")
    run(["go", "vet", "./..."], ROOT / "console")
    run(["go", "test", "./..."], ROOT / "console")
    run([sys.executable, "-m", "pytest", "-q", "tests"])
    run(["npm.cmd" if os.name == "nt" else "npm", "test"], ROOT / "frontend")
    run([sys.executable, "bridge/main.py", "--self-test"])


def install(root, check_only=False):
    paths = bundle.InstallPaths(root, cache_dir=ROOT / "work/runtime-cache")
    if check_only:
        check()
        return paths.target_install
    bundle.ensure_runtime(paths, False)
    bundle.ensure_site_packages(paths, False)
    run(
        [
            "go",
            "tool",
            "mygo",
            "build",
            "-skip-dmg" if sys.platform == "darwin" else "-skip-installer",
        ],
        ROOT / "console",
    )
    platform_dir = (
        ROOT
        / "console/dist"
        / ("darwin-arm64" if sys.platform == "darwin" else "windows-amd64")
    )
    apps = list(platform_dir.glob("*.app" if sys.platform == "darwin" else "*.exe"))
    if not apps:
        raise RuntimeError("Desktop build artifact not found")
    target = paths.target_install
    app = apps[0]
    if app.is_dir():
        shutil.copytree(app, target / app.name, dirs_exist_ok=True, symlinks=True)
    else:
        shutil.copy2(app, target / app.name)
    for name in ("bridge", "server", "common", "catalog", "third_party"):
        shutil.copytree(
            ROOT / name,
            target / name,
            dirs_exist_ok=True,
            ignore=shutil.ignore_patterns("__pycache__", "*.pyc", "test_*", "*.in"),
        )
    for name in ("README.md", "LICENSE", "NOTICE"):
        shutil.copy2(ROOT / name, target / name)
    env = dict(
        os.environ, PYTHONPATH=str(target / "site-packages") + os.pathsep + str(target)
    )
    run(
        [
            str(bundle.bundled_python(target)),
            str(target / "bridge/main.py"),
            "--self-test",
        ],
        cwd=target,
        env=env,
    )
    print("Portable installation:", target)
    return target


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, help="Isolated installation root")
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    install(args.root, args.check)


if __name__ == "__main__":
    main()
