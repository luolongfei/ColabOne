from __future__ import annotations

from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, HTTPServer
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from threading import Thread
import unittest
from unittest.mock import MagicMock, patch
import urllib.error


ROOT = Path(__file__).resolve().parents[1]
SKILL_DIR = ROOT / ".agents/skills/colabone-release"
PREPARE_SCRIPT = SKILL_DIR / "scripts/prepare_release.py"
ACTIONS_SCRIPT = SKILL_DIR / "scripts/github_actions.py"
RELEASE_VERSION_SCRIPT = ROOT / "tools/release_version.py"


def _load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


PREPARE = _load_module("colabone_prepare_release", PREPARE_SCRIPT)
ACTIONS = _load_module("colabone_github_actions", ACTIONS_SCRIPT)


def _run_git(root: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", "-C", str(root), *args],
        stdin=subprocess.DEVNULL,
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
    )


def _create_fixture(root: Path, version: str = "0.1.0") -> None:
    (root / "console").mkdir(parents=True, exist_ok=True)
    (root / "frontend").mkdir(parents=True, exist_ok=True)
    (root / "console/mygo.json").write_text(
        json.dumps({"name": "ColabOne", "version": version}, indent=2) + "\n",
        encoding="utf-8",
        newline="\n",
    )
    (root / "frontend/package.json").write_text(
        json.dumps({"name": "colabone-ui", "version": version}, indent=2) + "\n",
        encoding="utf-8",
        newline="\n",
    )
    (root / "frontend/package-lock.json").write_text(
        json.dumps(
            {
                "name": "colabone-ui",
                "version": version,
                "lockfileVersion": 3,
                "packages": {
                    "": {
                        "name": "colabone-ui",
                        "version": version,
                    }
                },
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
        newline="\n",
    )
    _run_git(root, "init", "--initial-branch=main")
    _run_git(root, "config", "user.name", "ColabOne Release Test")
    _run_git(root, "config", "user.email", "release-test@example.com")
    _run_git(root, "add", ".")
    _run_git(root, "commit", "-m", "initial release fixture")


def _run_prepare(
    root: Path, *args: str, env: dict[str, str] | None = None
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(PREPARE_SCRIPT), "--repo-root", str(root), *args],
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        encoding="utf-8",
        check=False,
        env=env,
    )


def _sample_run(**changes):
    return {
        "id": 12,
        "run_attempt": 2,
        "path": ".github/workflows/release.yml",
        "event": "push",
        "head_branch": "v0.1.1",
        "head_sha": "a" * 40,
        "status": "completed",
        "conclusion": "failure",
        **changes,
    }


def _sample_release(tag: str = "v0.1.1", **changes):
    version = tag.removeprefix("v")
    return {
        "id": 77,
        "tag_name": tag,
        "target_commitish": "main",
        "name": f"ColabOne {tag}",
        "body": f"ColabOne {tag}\n\n## 修改点\n- 示例更新\n\n---\n\n## Changes\n- Sample update\n",
        "draft": False,
        "prerelease": "-" in version,
        "html_url": f"https://github.com/owner/repo/releases/tag/{tag}",
        "published_at": "2026-11-22T00:00:00Z",
        "assets": [
            {
                "id": 1,
                "name": f"ColabOne-{version}-windows-x64-setup.exe",
                "size": 1024,
                "state": "uploaded",
                "browser_download_url": f"https://example.invalid/ColabOne-{version}-windows-x64-setup.exe",
            },
            {
                "id": 2,
                "name": f"ColabOne-{version}-macos-arm64.dmg",
                "size": 2048,
                "state": "uploaded",
                "browser_download_url": f"https://example.invalid/ColabOne-{version}-macos-arm64.dmg",
            },
            {
                "id": 3,
                "name": f"colabone-{version}-windows-amd64.tar.gz",
                "size": 1024,
                "state": "uploaded",
                "browser_download_url": f"https://example.invalid/colabone-{version}-windows-amd64.tar.gz",
            },
            {
                "id": 4,
                "name": f"colabone-{version}-darwin-arm64.tar.gz",
                "size": 2048,
                "state": "uploaded",
                "browser_download_url": f"https://example.invalid/colabone-{version}-darwin-arm64.tar.gz",
            },
            {
                "id": 5,
                "name": "update-windows-amd64.json",
                "size": 320,
                "state": "uploaded",
                "browser_download_url": "https://example.invalid/update-windows-amd64.json",
            },
            {
                "id": 6,
                "name": "update-darwin-arm64.json",
                "size": 320,
                "state": "uploaded",
                "browser_download_url": "https://example.invalid/update-darwin-arm64.json",
            },
            {
                "id": 7,
                "name": "SHA256SUMS.txt",
                "size": 160,
                "state": "uploaded",
                "browser_download_url": "https://example.invalid/SHA256SUMS.txt",
            },
        ],
        **changes,
    }


class SkillPackageTests(unittest.TestCase):
    def test_skill_frontmatter_and_referenced_files_exist(self) -> None:
        skill_md = (SKILL_DIR / "SKILL.md").read_text(encoding="utf-8")
        match = re.match(r"^---\n(.*?)\n---\n", skill_md, re.DOTALL)
        self.assertIsNotNone(match)
        frontmatter = match.group(1)
        self.assertIn("name: colabone-release", frontmatter)
        self.assertIn("description:", frontmatter)
        for token in (
            "## 新增功能",
            "## 修改点",
            "## 修复的 bug",
            "---",
            "## New Features",
            "## Changes",
            "## Bug Fixes",
            "--added-zh",
            "--added-en",
            "--with-e2e",
            "不在本地执行",
            "远端发布检查",
            "dispatch --workflow ci.yml",
            "preflight-status",
            "--require-green",
            "同版本号直至发布成功原则",
            "git push origin :refs/tags/vX.Y.Z",
        ):
            self.assertIn(token, skill_md)
        actions_doc = (SKILL_DIR / "references/github-actions.md").read_text(encoding="utf-8")
        self.assertIn("同一个目标版本", actions_doc)
        self.assertIn("git push origin :refs/tags/vX.Y.Z", actions_doc)
        self.assertNotIn("严禁删除或强制移动已推送的发布标签", actions_doc)
        self.assertNotIn("严禁删除或强制移动已推送标签", skill_md)
        self.assertNotIn("## 中文", skill_md)
        self.assertNotIn("## English", skill_md)
        self.assertLess(skill_md.index("## 新增功能"), skill_md.index("## New Features"))
        self.assertTrue((SKILL_DIR / "agents/openai.yaml").is_file())
        self.assertTrue((SKILL_DIR / "references/release-checklist.md").is_file())
        self.assertTrue((SKILL_DIR / "references/github-actions.md").is_file())
        self.assertTrue(PREPARE_SCRIPT.is_file())
        self.assertTrue(ACTIONS_SCRIPT.is_file())

    def test_skill_files_contain_no_external_reference_app_names(self) -> None:
        forbidden = (
            "AppleHelper",
            "applehelper",
            "Antigravity-Manager",
            "COLAB_YOGA",
        )
        for path in SKILL_DIR.rglob("*"):
            if not path.is_file() or "__pycache__" in path.parts:
                continue
            text = path.read_text(encoding="utf-8")
            for token in forbidden:
                self.assertNotIn(token, text, f"Unexpected {token!r} in {path}")


class PrepareReleaseTests(unittest.TestCase):
    def test_default_version_bumps_patch_from_source_when_no_tags_exist(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.0")
            notes_path = root / "notes.md"

            result = _run_prepare(
                root,
                "--added-zh",
                "新增项目级发版技能",
                "--fixed-zh",
                "修复发布说明格式问题",
                "--added-en",
                "Add workspace release skill",
                "--fixed-en",
                "Fix release notes formatting",
                "--notes-output",
                str(notes_path),
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("v0.1.1", result.stdout)
            validation = subprocess.run(
                [
                    sys.executable,
                    str(RELEASE_VERSION_SCRIPT),
                    "--root",
                    str(root),
                    "--tag",
                    "v0.1.1",
                ],
                stdin=subprocess.DEVNULL,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(validation.returncode, 0, validation.stderr)
            self.assertEqual(validation.stdout.strip(), "0.1.1")

            for rel in PREPARE.COLABONE_MANIFEST_PATHS:
                self.assertNotIn(b"\r\n", (root / rel).read_bytes())

            notes = notes_path.read_text(encoding="utf-8")
            expected = (
                "ColabOne v0.1.1\n\n"
                "## 新增功能\n"
                "- 新增项目级发版技能\n\n"
                "## 修复的 bug\n"
                "- 修复发布说明格式问题\n\n"
                "---\n\n"
                "## New Features\n"
                "- Add workspace release skill\n\n"
                "## Bug Fixes\n"
                "- Fix release notes formatting\n"
            )
            self.assertEqual(notes, expected)
            self.assertNotIn("## 中文", notes)
            self.assertNotIn("## English", notes)
            self.assertNotIn("## 修改点", notes)
            self.assertNotIn("## Changes", notes)
            self.assertLess(notes.index("## 新增功能"), notes.index("\n---\n"))
            self.assertLess(notes.index("\n---\n"), notes.index("## New Features"))

    def test_default_version_uses_semver_max_tag_and_bumps_patch(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.10")
            _run_git(root, "tag", "v0.1.2")
            _run_git(root, "tag", "v0.1.9")
            _run_git(root, "tag", "v0.1.10-rc.1")
            _run_git(root, "tag", "v0.1.10")
            notes_path = root / "notes.md"

            result = _run_prepare(
                root,
                "--changed",
                "基于 v0.1.10 递增补丁版本",
                "--changed-en",
                "Bump patch after v0.1.10",
                "--notes-output",
                str(notes_path),
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("v0.1.11", result.stdout)
            self.assertEqual(
                json.loads((root / "console/mygo.json").read_text(encoding="utf-8"))[
                    "version"
                ],
                "0.1.11",
            )
            lock_data = json.loads(
                (root / "frontend/package-lock.json").read_text(encoding="utf-8")
            )
            self.assertEqual(lock_data["version"], "0.1.11")
            self.assertEqual(lock_data["packages"][""]["version"], "0.1.11")
            notes = notes_path.read_text(encoding="utf-8")
            self.assertIn("## 修改点\n- 基于 v0.1.10 递增补丁版本\n", notes)
            self.assertIn("## Changes\n- Bump patch after v0.1.10\n", notes)

    def test_annotated_tag_roundtrip_matches_release_workflow_extraction(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.0")
            notes_path = root / "notes.md"

            result = _run_prepare(
                root,
                "--added-zh",
                "支持自动递增补丁版本发版",
                "--changed-zh",
                "完善发布检查清单",
                "--added-en",
                "Support automated patch releases",
                "--changed-en",
                "Refine release checklist",
                "--notes-output",
                str(notes_path),
            )
            self.assertEqual(result.returncode, 0, result.stderr)

            _run_git(
                root,
                "tag",
                "-a",
                "v0.1.1",
                "-F",
                str(notes_path),
                "--cleanup=verbatim",
            )
            subject = _run_git(
                root, "for-each-ref", "--format=%(contents:subject)", "refs/tags/v0.1.1"
            ).stdout.strip()
            body = _run_git(
                root, "for-each-ref", "--format=%(contents:body)", "refs/tags/v0.1.1"
            ).stdout.strip()

            self.assertEqual(subject, "ColabOne v0.1.1")
            self.assertIn("## 新增功能\n- 支持自动递增补丁版本发版", body)
            self.assertIn("## 修改点\n- 完善发布检查清单", body)
            self.assertIn("\n\n---\n\n## New Features\n- Support automated patch releases", body)
            self.assertIn("## Changes\n- Refine release checklist", body)
            self.assertNotIn("## 中文", body)
            self.assertNotIn("## English", body)
            self.assertLess(body.index("## 新增功能"), body.index("## New Features"))

    def test_dry_run_previews_without_modifying_files(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.0")
            before = {
                rel: (root / rel).read_bytes() for rel in PREPARE.COLABONE_MANIFEST_PATHS
            }
            notes_path = root / "preview-notes.md"

            result = _run_prepare(
                root,
                "--dry-run",
                "--json",
                "--changed-zh",
                "预览下一补丁版本发布说明",
                "--changed-en",
                "Preview next patch release",
                "--notes-output",
                str(notes_path),
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            payload = json.loads(result.stdout)
            self.assertEqual(payload["version"], "0.1.1")
            self.assertEqual(payload["tag"], "v0.1.1")
            self.assertTrue(payload["dry_run"])
            self.assertIn("## 修改点", payload["release_notes"])
            self.assertIn("预览下一补丁版本发布说明", payload["release_notes"])
            self.assertIn("## Changes", payload["release_notes"])
            self.assertIn("Preview next patch release", payload["release_notes"])
            self.assertNotIn("## 中文", payload["release_notes"])
            self.assertNotIn("## English", payload["release_notes"])
            self.assertLess(
                payload["release_notes"].index("## 修改点"),
                payload["release_notes"].index("## Changes"),
            )
            self.assertFalse(notes_path.exists())
            self.assertEqual(
                {
                    rel: (root / rel).read_bytes()
                    for rel in PREPARE.COLABONE_MANIFEST_PATHS
                },
                before,
            )

    def test_explicit_prerelease_and_stable_versions_are_supported(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.0")
            notes_path = root / "rc-notes.md"

            result = _run_prepare(
                root,
                "--version",
                "v0.2.0-rc.1",
                "--added-zh",
                "准备候选发布版本",
                "--added-en",
                "Prepare release candidate",
                "--notes-output",
                str(notes_path),
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                json.loads((root / "console/mygo.json").read_text(encoding="utf-8"))[
                    "version"
                ],
                "0.2.0-rc.1",
            )
            _run_git(root, "tag", "v0.2.0-rc.1")

            auto_from_rc = _run_prepare(
                root,
                "--changed-zh",
                "预发布版本必须显式指定下一版本号",
                "--changed-en",
                "Should require explicit version from prerelease",
                "--notes-output",
                str(root / "fail-notes.md"),
            )
            self.assertNotEqual(auto_from_rc.returncode, 0)
            self.assertIn("prerelease", auto_from_rc.stderr)

            promote = _run_prepare(
                root,
                "--version",
                "0.2.0",
                "--changed-zh",
                "将候选版本升级为正式版",
                "--changed-en",
                "Promote release candidate to stable",
                "--notes-output",
                str(root / "stable-notes.md"),
            )
            self.assertEqual(promote.returncode, 0, promote.stderr)
            self.assertEqual(
                json.loads((root / "console/mygo.json").read_text(encoding="utf-8"))[
                    "version"
                ],
                "0.2.0",
            )

    def test_rejects_mismatched_manifests_before_writes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.0")
            (root / "frontend/package.json").write_text(
                json.dumps({"name": "colabone-ui", "version": "0.1.9"}, indent=2) + "\n",
                encoding="utf-8",
            )
            before = {
                rel: (root / rel).read_bytes() for rel in PREPARE.COLABONE_MANIFEST_PATHS
            }

            result = _run_prepare(
                root,
                "--changed-zh",
                "清单版本不一致时应拒绝写入",
                "--changed-en",
                "Should fail on drift",
                "--notes-output",
                str(root / "notes.md"),
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("release source versions do not match", result.stderr)
            self.assertEqual(
                {
                    rel: (root / rel).read_bytes()
                    for rel in PREPARE.COLABONE_MANIFEST_PATHS
                },
                before,
            )

    def test_rejects_duplicate_json_keys_before_writes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.0")
            (root / "console/mygo.json").write_text(
                '{\n  "version": "0.1.0",\n  "version": "0.1.0"\n}\n',
                encoding="utf-8",
            )

            result = _run_prepare(
                root,
                "--changed-zh",
                "检测重复 JSON 键",
                "--changed-en",
                "Duplicate key check",
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("duplicate JSON key", result.stderr)

    def test_rejects_existing_tag_downgrade_divergence_and_leading_zero(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.5")
            _run_git(root, "tag", "v0.1.4")

            diverged = _run_prepare(
                root,
                "--changed-zh",
                "源码与最新标签不一致",
                "--changed-en",
                "Diverged source and tag",
            )
            self.assertNotEqual(diverged.returncode, 0)
            self.assertIn("does not match latest tag", diverged.stderr)

            existing = _run_prepare(
                root,
                "--version",
                "0.1.4",
                "--changed-zh",
                "标签已存在",
                "--changed-en",
                "Existing tag",
            )
            self.assertNotEqual(existing.returncode, 0)
            self.assertIn("already exists", existing.stderr)

            downgrade = _run_prepare(
                root,
                "--version",
                "0.1.3",
                "--changed-zh",
                "版本回退",
                "--changed-en",
                "Downgrade",
            )
            self.assertNotEqual(downgrade.returncode, 0)
            self.assertIn("must be newer than latest tag", downgrade.stderr)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.2.0")
            downgrade_source = _run_prepare(
                root,
                "--version",
                "0.1.9",
                "--changed-zh",
                "低于当前源码版本",
                "--changed-en",
                "Downgrade source",
            )
            self.assertNotEqual(downgrade_source.returncode, 0)
            self.assertIn("must not downgrade source version", downgrade_source.stderr)

        with self.assertRaises(SystemExit):
            PREPARE.ColabOneSemVer.parse("v01.2.3")

    def test_rejects_invalid_release_notes_and_protected_notes_path(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.0")
            before = {
                rel: (root / rel).read_bytes() for rel in PREPARE.COLABONE_MANIFEST_PATHS
            }

            empty = _run_prepare(root)
            self.assertNotEqual(empty.returncode, 0)
            self.assertIn("at least one bilingual release note item", empty.stderr)

            zh_only = _run_prepare(root, "--added-zh", "仅提供中文条目")
            self.assertNotEqual(zh_only.returncode, 0)
            self.assertIn("--added-en", zh_only.stderr)

            en_only = _run_prepare(root, "--fixed-en", "Only English item")
            self.assertNotEqual(en_only.returncode, 0)
            self.assertIn("--fixed-zh", en_only.stderr)

            category_mismatch = _run_prepare(
                root,
                "--added-zh",
                "新增功能条目",
                "--fixed-en",
                "Fix bug item",
            )
            self.assertNotEqual(category_mismatch.returncode, 0)
            self.assertIn("--added-en", category_mismatch.stderr)

            count_mismatch = _run_prepare(
                root,
                "--added-zh",
                "功能一",
                "--added-zh",
                "功能二",
                "--added-en",
                "Feature one",
            )
            self.assertNotEqual(count_mismatch.returncode, 0)
            self.assertIn("count mismatch for added", count_mismatch.stderr)

            multiline_zh = _run_prepare(
                root,
                "--changed-zh",
                "line 1\nline 2",
                "--changed-en",
                "Single line English",
            )
            self.assertNotEqual(multiline_zh.returncode, 0)
            self.assertIn("single line", multiline_zh.stderr)

            multiline_en = _run_prepare(
                root,
                "--changed-zh",
                "单行中文说明",
                "--changed-en",
                "line 1\nline 2",
            )
            self.assertNotEqual(multiline_en.returncode, 0)
            self.assertIn("single line", multiline_en.stderr)

            blank_en = _run_prepare(
                root,
                "--changed-zh",
                "有效中文说明",
                "--changed-en",
                "   ",
            )
            self.assertNotEqual(blank_en.returncode, 0)
            self.assertIn("must not be empty", blank_en.stderr)

            for protected in PREPARE.COLABONE_MANIFEST_PATHS:
                collision = _run_prepare(
                    root,
                    "--changed-zh",
                    "路径冲突检测",
                    "--changed-en",
                    "Collision check",
                    "--notes-output",
                    str(root / protected),
                )
                self.assertNotEqual(collision.returncode, 0)
                self.assertIn("must not overwrite", collision.stderr)

            self.assertEqual(
                {
                    rel: (root / rel).read_bytes()
                    for rel in PREPARE.COLABONE_MANIFEST_PATHS
                },
                before,
            )

    def test_relocated_skill_resolves_its_own_repo_from_any_cwd(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "portable checkout"
            _create_fixture(root, "0.1.0")
            _run_git(root, "tag", "v0.1.0")
            relocated = (
                root / ".agents/skills/colabone-release/scripts/prepare_release.py"
            )
            relocated.parent.mkdir(parents=True)
            shutil.copy2(PREPARE_SCRIPT, relocated)

            temp_notes_dir = Path(directory) / "temp notes"
            temp_notes_dir.mkdir()
            expected_notes = temp_notes_dir / "colabone-v0.1.1-release-notes.md"

            result = subprocess.run(
                [
                    sys.executable,
                    str(relocated),
                    "--changed-zh",
                    "验证可移植技能跨目录执行",
                    "--changed-en",
                    "Verify portable skill execution",
                ],
                cwd=directory,
                stdin=subprocess.DEVNULL,
                env={
                    **os.environ,
                    "TMPDIR": str(temp_notes_dir),
                    "TEMP": str(temp_notes_dir),
                    "TMP": str(temp_notes_dir),
                },
                capture_output=True,
                text=True,
                encoding="utf-8",
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                json.loads((root / "console/mygo.json").read_text(encoding="utf-8"))[
                    "version"
                ],
                "0.1.1",
            )
            notes_text = expected_notes.read_text(encoding="utf-8")
            self.assertIn("验证可移植技能跨目录执行", notes_text)
            self.assertIn("Verify portable skill execution", notes_text)

    def test_utf8_release_notes_work_under_ascii_locale(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _create_fixture(root, "0.1.0")
            manifest = json.loads(
                (root / "console/mygo.json").read_text(encoding="utf-8")
            )
            manifest["description"] = "远程 GPU 工作台"
            (root / "console/mygo.json").write_text(
                json.dumps(manifest, indent=2, ensure_ascii=False) + "\n",
                encoding="utf-8",
            )
            notes_path = root / "notes.md"
            ascii_env = {
                **os.environ,
                "LC_ALL": "C",
                "PYTHONUTF8": "0",
                "PYTHONCOERCECLOCALE": "0",
            }

            preview = _run_prepare(
                root,
                "--dry-run",
                "--added-zh",
                "支持版本号最后一位自动加一",
                "--added-en",
                "Support automatic patch version bumping",
                "--notes-output",
                str(notes_path),
                env=ascii_env,
            )
            self.assertEqual(preview.returncode, 0, preview.stderr)
            self.assertFalse(notes_path.exists())

            result = _run_prepare(
                root,
                "--added-zh",
                "支持版本号最后一位自动加一",
                "--added-en",
                "Support automatic patch version bumping",
                "--notes-output",
                str(notes_path),
                env=ascii_env,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            rendered = notes_path.read_text(encoding="utf-8")
            self.assertIn("支持版本号最后一位自动加一", rendered)
            self.assertIn("Support automatic patch version bumping", rendered)
            self.assertNotIn("## 中文", rendered)
            self.assertNotIn("## English", rendered)
            self.assertLess(rendered.index("## 新增功能"), rendered.index("## New Features"))
            self.assertEqual(
                json.loads((root / "console/mygo.json").read_text(encoding="utf-8"))[
                    "description"
                ],
                "远程 GPU 工作台",
            )

    def test_atomic_write_success_and_rollback_behavior(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            existing = root / "mygo.json"
            created = root / "notes.md"
            existing.write_text("original", encoding="utf-8")

            PREPARE.colabone_write_prepared_files(
                {existing: "updated", created: "new"}
            )
            self.assertEqual(existing.read_text(encoding="utf-8"), "updated")
            self.assertEqual(created.read_text(encoding="utf-8"), "new")
            self.assertEqual(set(root.iterdir()), {existing, created})

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            existing = root / "mygo.json"
            existing.write_text("original", encoding="utf-8")
            created = root / "package.json"
            rejected = root / "notes.md"
            real_replace = os.replace

            def fail_last(source: Path, target: Path) -> None:
                if target == rejected:
                    raise PermissionError("injected write failure")
                real_replace(source, target)

            with patch.object(PREPARE.os, "replace", side_effect=fail_last):
                with self.assertRaises(PermissionError):
                    PREPARE.colabone_write_prepared_files(
                        {existing: "updated", created: "new", rejected: "notes"}
                    )

            self.assertEqual(existing.read_text(encoding="utf-8"), "original")
            self.assertEqual(set(root.iterdir()), {existing})

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first = root / "mygo.json"
            second = root / "package.json"
            rejected = root / "notes.md"
            first.write_text("first original", encoding="utf-8")
            second.write_text("second original", encoding="utf-8")
            real_replace = os.replace
            failed_backup: list[Path] = []
            second_writes = 0

            def fail_write_and_restore(source: Path, target: Path) -> None:
                nonlocal second_writes
                if target == rejected:
                    raise PermissionError("injected write failure")
                if target == second:
                    second_writes += 1
                    if second_writes == 2:
                        failed_backup.append(Path(source))
                        raise PermissionError("injected rollback failure")
                real_replace(source, target)

            with patch.object(
                PREPARE.os, "replace", side_effect=fail_write_and_restore
            ):
                with self.assertRaises(RuntimeError) as raised:
                    PREPARE.colabone_write_prepared_files(
                        {
                            first: "first updated",
                            second: "second updated",
                            rejected: "notes",
                        }
                    )

            self.assertEqual(first.read_text(encoding="utf-8"), "first original")
            self.assertEqual(len(failed_backup), 1)
            self.assertEqual(
                failed_backup[0].read_text(encoding="utf-8"), "second original"
            )
            self.assertIn(str(failed_backup[0]), str(raised.exception))
            self.assertIsInstance(raised.exception.__cause__, PermissionError)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original = root / "mygo.json"
            original.write_text("original", encoding="utf-8")
            with patch.object(
                PREPARE.shutil, "copy2", side_effect=OSError("injected copy failure")
            ):
                with self.assertRaises(OSError):
                    PREPARE.colabone_write_prepared_files({original: "updated"})
            self.assertEqual(original.read_text(encoding="utf-8"), "original")
            self.assertEqual(set(root.iterdir()), {original})


class GitHubActionsRepositoryTests(unittest.TestCase):
    def test_https_and_ssh_remotes(self) -> None:
        for remote in (
            "https://github.com/owner/repo.git",
            "https://user:token@github.com/owner/repo.git",
            "git@github.com:owner/repo.git",
            "ssh://git@github.com/owner/repo.git",
        ):
            with self.subTest(remote=remote):
                self.assertEqual(
                    ACTIONS.colabone_parse_repository(remote),
                    ("github.com", "owner/repo"),
                )

    def test_rejects_non_github_and_invalid_paths(self) -> None:
        for remote in (
            "/tmp/repo",
            "https://example.com/owner/repo",
            "https://github.com/owner/repo/extra",
        ):
            with self.subTest(remote=remote), self.assertRaises(
                ACTIONS.ColabOneActionsError
            ):
                ACTIONS.colabone_parse_repository(remote)

    @patch.dict(os.environ, {"GH_TOKEN": "first", "GITHUB_TOKEN": "second"}, clear=True)
    @patch.object(ACTIONS, "colabone_run_command")
    def test_explicit_token_precedence(self, command) -> None:
        self.assertEqual(
            ACTIONS.colabone_resolve_credential(
                Path.cwd(), "git@github.com:o/r", "github.com"
            ),
            "first",
        )
        del os.environ["GH_TOKEN"]
        self.assertEqual(
            ACTIONS.colabone_resolve_credential(
                Path.cwd(), "git@github.com:o/r", "github.com"
            ),
            "second",
        )
        command.assert_not_called()

    @patch.dict(os.environ, {}, clear=True)
    @patch.object(ACTIONS, "colabone_run_command", return_value="gh-token")
    def test_gh_before_git_credential(self, command) -> None:
        self.assertEqual(
            ACTIONS.colabone_resolve_credential(
                Path.cwd(), "https://github.com/o/r", "github.com"
            ),
            "gh-token",
        )
        self.assertEqual(command.call_count, 1)

    @patch.dict(os.environ, {}, clear=True)
    @patch.object(
        ACTIONS,
        "colabone_run_command",
        side_effect=["", "username=user\npassword=git-token=a\n"],
    )
    def test_git_helper_receives_original_remote(self, command) -> None:
        remote = "https://user:token@github.com/o/r.git"
        self.assertEqual(
            ACTIONS.colabone_resolve_credential(Path.cwd(), remote, "github.com"),
            "git-token=a",
        )
        call = command.call_args
        self.assertEqual(call.args[1], ["git", "credential", "fill"])
        self.assertEqual(call.kwargs["input_text"], f"url={remote}\n\n")
        self.assertEqual(call.kwargs["env"]["GIT_TERMINAL_PROMPT"], "0")

    @patch.dict(os.environ, {}, clear=True)
    @patch.object(ACTIONS, "colabone_run_command", return_value="")
    def test_ssh_key_is_not_api_authentication(self, command) -> None:
        with self.assertRaises(ACTIONS.ColabOneActionsError) as error:
            ACTIONS.colabone_resolve_credential(
                Path.cwd(), "git@github.com:o/r", "github.com"
            )
        self.assertEqual(error.exception.details["code"], "credential_unavailable")
        self.assertEqual(command.call_count, 1)


class GitHubActionsClientTests(unittest.TestCase):
    def setUp(self) -> None:
        self.client = ACTIONS.ColabOneActionsClient("owner/repo", "token")

    def test_pagination_preserves_filters(self) -> None:
        with patch.object(
            self.client,
            "json",
            side_effect=[
                {"workflow_runs": [_sample_run()] * 100},
                {"workflow_runs": [_sample_run(id=13)]},
            ],
        ) as request:
            result = self.client.pages(
                "actions/runs", "workflow_runs", head_sha="a" * 40
            )
        self.assertEqual(len(result), 101)
        self.assertIn("page=2", request.call_args.args[0])
        self.assertIn("head_sha=" + "a" * 40, request.call_args.args[0])

    def test_run_identity_requires_all_fields_and_supports_prerelease_tags(self) -> None:
        self.assertTrue(
            ACTIONS.colabone_run_matches(
                _sample_run(head_branch="v0.2.0-rc.1"),
                "release.yml",
                "v0.2.0-rc.1",
                "a" * 40,
            )
        )
        for changes in (
            {"path": ".github/workflows/ci.yml"},
            {"head_branch": "main"},
            {"head_sha": "b" * 40},
            {"event": "workflow_dispatch"},
        ):
            self.assertFalse(
                ACTIONS.colabone_run_matches(
                    _sample_run(**changes), "release.yml", "v0.1.1", "a" * 40
                )
            )

    def test_show_uses_resolved_attempt_jobs(self) -> None:
        job = {"id": 99, "steps": [{"name": "Build", "status": "in_progress"}]}
        with patch.object(
            self.client, "json", return_value=_sample_run(run_attempt=3)
        ), patch.object(self.client, "pages", return_value=[job]) as pages:
            result = ACTIONS.colabone_show_run(self.client, 12)
        pages.assert_called_once_with("actions/runs/12/attempts/3/jobs", "jobs")
        self.assertEqual(result["jobs"][0]["id"], 99)
        self.assertEqual(len(result["jobs"][0]["active_steps"]), 1)

    def test_log_redirect_does_not_forward_authorization_and_rejects_http(self) -> None:
        response = MagicMock()
        stream = response.__enter__.return_value
        stream.read.side_effect = [b"original ", b"log\n", b""]
        stream.headers = {"Content-Length": "13"}
        with tempfile.TemporaryDirectory() as directory, patch.object(
            self.client,
            "request",
            return_value=(302, {"Location": "https://storage.example/log"}, b""),
        ), patch.object(
            ACTIONS.urllib.request, "urlopen", return_value=response
        ) as download:
            output = Path(directory) / "job.log"
            self.client.download("actions/jobs/99/logs", output)
            self.assertEqual(output.read_bytes(), b"original log\n")
            download.assert_called_once_with("https://storage.example/log", timeout=30)

        with tempfile.TemporaryDirectory() as directory, patch.object(
            self.client,
            "request",
            return_value=(302, {"Location": "http://storage.example/log"}, b""),
        ):
            output = Path(directory) / "job.log"
            with self.assertRaises(ACTIONS.ColabOneActionsError):
                self.client.download("actions/jobs/99/logs", output)
            self.assertFalse(output.exists())

    def test_download_cleans_partial_files_and_preserves_existing_file(self) -> None:
        response = MagicMock()
        stream = response.__enter__.return_value
        stream.read.side_effect = [b"partial", b""]
        stream.headers = {"Content-Length": "100"}
        with tempfile.TemporaryDirectory() as directory, patch.object(
            self.client,
            "request",
            return_value=(302, {"Location": "https://storage.example/log"}, b""),
        ), patch.object(ACTIONS.urllib.request, "urlopen", return_value=response):
            output = Path(directory) / "job.log"
            with self.assertRaises(ACTIONS.ColabOneActionsError) as error:
                self.client.download("actions/jobs/99/logs", output)
            self.assertEqual(error.exception.details["code"], "incomplete_log_download")
            self.assertFalse(output.exists())

        with tempfile.TemporaryDirectory() as directory, patch.object(
            self.client, "request", return_value=(200, {}, b"new")
        ):
            output = Path(directory) / "job.log"
            output.write_bytes(b"old")
            with self.assertRaises(FileExistsError):
                self.client.download("actions/jobs/99/logs", output)
            self.assertEqual(output.read_bytes(), b"old")

    def test_inspect_release_verifies_expected_assets_and_flags_gaps(self) -> None:
        with patch.object(
            self.client, "json", return_value=_sample_release("v0.1.1")
        ):
            result = ACTIONS.colabone_inspect_release(self.client, "v0.1.1")
        self.assertTrue(result["assets_complete"])
        self.assertEqual(result["missing_assets"], [])
        self.assertEqual(result["incomplete_assets"], [])
        self.assertIn("## 修改点", result["body"])
        self.assertIn("## Changes", result["body"])
        self.assertLess(result["body"].index("## 修改点"), result["body"].index("## Changes"))

        missing_mac = _sample_release("v0.1.1")
        missing_mac["assets"] = [
            asset
            for asset in missing_mac["assets"]
            if "macos" not in asset["name"]
        ]
        with patch.object(self.client, "json", return_value=missing_mac):
            result = ACTIONS.colabone_inspect_release(self.client, "v0.1.1")
        self.assertFalse(result["assets_complete"])
        self.assertEqual(result["missing_assets"], ["ColabOne-0.1.1-macos-arm64.dmg"])

        empty_asset = _sample_release("v0.1.1")
        empty_asset["assets"][-1]["size"] = 0
        with patch.object(self.client, "json", return_value=empty_asset):
            result = ACTIONS.colabone_inspect_release(self.client, "v0.1.1")
        self.assertFalse(result["assets_complete"])
        self.assertEqual(result["incomplete_assets"], ["SHA256SUMS.txt"])

        draft_release = _sample_release("v0.2.0-rc.1", draft=True)
        with patch.object(self.client, "json", return_value=draft_release):
            result = ACTIONS.colabone_inspect_release(self.client, "v0.2.0-rc.1")
        self.assertFalse(result["assets_complete"])
        self.assertTrue(result["prerelease"])


class GitHubActionsRetryAndOutputTests(unittest.TestCase):
    def retry(self, client):
        return ACTIONS.colabone_rerun_failed(
            client, 12, "release.yml", "v0.1.1", "a" * 40, 2
        )

    def test_rerun_failed_checks_identity_and_reconciles_uncertain_errors(self) -> None:
        for changes in (
            {"head_branch": "v0.1.2"},
            {"head_sha": "b" * 40},
            {"path": ".github/workflows/ci.yml"},
            {"event": "workflow_dispatch"},
            {"run_attempt": 3},
            {"status": "in_progress"},
            {"conclusion": "success"},
            {"conclusion": "cancelled"},
        ):
            with self.subTest(changes=changes):
                client = MagicMock()
                client.json.return_value = _sample_run(**changes)
                with self.assertRaises(ACTIONS.ColabOneActionsError):
                    self.retry(client)
                client.request.assert_not_called()

        client = MagicMock()
        client.json.side_effect = [_sample_run(), _sample_run(run_attempt=3)]
        client.request.side_effect = ACTIONS.ColabOneActionsError("transport_error")
        result = self.retry(client)
        self.assertTrue(result["observed_after_request_error"])
        self.assertEqual(client.request.call_count, 1)

        client = MagicMock()
        client.json.return_value = _sample_run()
        client.request.side_effect = ACTIONS.ColabOneActionsError("transport_error")
        with self.assertRaises(ACTIONS.ColabOneActionsError) as error:
            self.retry(client)
        self.assertEqual(error.exception.details["code"], "rerun_outcome_unknown")
        self.assertEqual(client.request.call_count, 1)

    def test_json_output_survives_ascii_pipes(self) -> None:
        output = io.BytesIO()
        errors = io.BytesIO()
        with io.TextIOWrapper(output, encoding="ascii") as stdout, io.TextIOWrapper(
            errors, encoding="ascii"
        ) as stderr, patch.object(
            ACTIONS.sys, "stdout", stdout
        ), patch.object(
            ACTIONS.sys, "stderr", stderr
        ), patch.object(
            ACTIONS, "colabone_run_command", return_value="https://github.com/owner/repo"
        ), patch.object(
            ACTIONS, "colabone_resolve_credential", return_value="token"
        ), patch.object(
            ACTIONS,
            "colabone_show_run",
            return_value={"name": "\u53d1\u5e03 \U0001f680"},
        ):
            self.assertEqual(ACTIONS.main(["show", "--run-id", "12"]), 0)
            stdout.flush()
            self.assertEqual(
                json.loads(output.getvalue()), {"name": "\u53d1\u5e03 \U0001f680"}
            )


@contextmanager
def _local_api(responses):
    pending = iter(responses)
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            length = int(self.headers.get("Content-Length") or "0")
            body_bytes = self.rfile.read(length) if length > 0 else b""
            requests.append(
                (self.command, self.path, self.headers.get("Authorization"), body_bytes)
            )
            status, body = next(pending, (500, b"Unexpected request"))
            self.send_response(status)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        do_POST = do_GET

        def log_message(self, *args):
            pass

    with HTTPServer(("127.0.0.1", 0), Handler) as server:
        thread = Thread(
            target=server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True
        )
        thread.start()
        client = ACTIONS.ColabOneActionsClient("owner/repo", "fixture-token")
        client.base = f"http://127.0.0.1:{server.server_port}/repos/owner/repo/"
        try:
            yield client, requests
        finally:
            server.shutdown()
            thread.join(timeout=5)


class GitHubActionsHTTPContractTests(unittest.TestCase):
    def test_rerun_and_release_http_contracts(self) -> None:
        with _local_api(
            [(200, json.dumps(_sample_run()).encode()), (201, b"Created")]
        ) as (client, requests):
            result = ACTIONS.colabone_rerun_failed(
                client, 12, "release.yml", "v0.1.1", "a" * 40, 2
            )
        self.assertTrue(result["accepted"])
        self.assertEqual(
            requests,
            [
                (
                    "GET",
                    "/repos/owner/repo/actions/runs/12",
                    "Bearer fixture-token",
                    b"",
                ),
                (
                    "POST",
                    "/repos/owner/repo/actions/runs/12/rerun-failed-jobs",
                    "Bearer fixture-token",
                    b"",
                ),
            ],
        )

        with _local_api(
            [(200, json.dumps(_sample_release("v0.1.1")).encode())]
        ) as (client, requests):
            release = ACTIONS.colabone_inspect_release(client, "v0.1.1")
        self.assertTrue(release["assets_complete"])
        self.assertEqual(
            requests,
            [
                (
                    "GET",
                    "/repos/owner/repo/releases/tags/v0.1.1",
                    "Bearer fixture-token",
                    b"",
                )
            ],
        )

        body = json.dumps({"message": "Access unavailable"}).encode()
        with _local_api(
            [(200, json.dumps(_sample_run()).encode()), (403, body)]
        ) as (client, requests):
            with self.assertRaises(ACTIONS.ColabOneActionsError) as error:
                ACTIONS.colabone_rerun_failed(
                    client, 12, "release.yml", "v0.1.1", "a" * 40, 2
                )
        self.assertEqual(error.exception.details["status"], 403)
        self.assertEqual(error.exception.details["response"], body.decode())
        self.assertEqual([method for method, _, _, _ in requests], ["GET", "POST"])

    def test_preflight_dispatch_and_status_http_contracts(self) -> None:
        sha = "c" * 40
        ci_run = _sample_run(
            id=55,
            run_attempt=1,
            path=".github/workflows/ci.yml",
            event="workflow_dispatch",
            head_branch="main",
            head_sha=sha,
            status="completed",
            conclusion="success",
            created_at="2026-11-25T10:00:00Z",
        )
        green_jobs = {
            "jobs": [
                {
                    "id": 101,
                    "name": "check (windows-latest)",
                    "status": "completed",
                    "conclusion": "success",
                    "steps": [],
                },
                {
                    "id": 102,
                    "name": "check (macos-latest)",
                    "status": "completed",
                    "conclusion": "success",
                    "steps": [],
                },
            ]
        }

        # 1. No prior run -> dispatches ci.yml on main pinned to exact SHA via inputs.ref
        with _local_api(
            [
                (200, json.dumps({"workflow_runs": []}).encode()),
                (204, b""),
            ]
        ) as (client, requests):
            res = ACTIONS.colabone_dispatch_ci(
                client, workflow="ci.yml", ref="main", sha=sha
            )
        self.assertTrue(res["dispatched"])
        self.assertFalse(res["already_passed"])
        self.assertEqual(res["ref"], "main")
        self.assertEqual(res["sha"], sha)
        self.assertRegex(res["dispatched_at"], r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")
        self.assertEqual(requests[1][0], "POST")
        self.assertEqual(
            requests[1][1], "/repos/owner/repo/actions/workflows/ci.yml/dispatches"
        )
        self.assertEqual(
            json.loads(requests[1][3].decode("utf-8")),
            {"ref": "main", "inputs": {"ref": sha}},
        )

        # 2. Already passed on both platforms -> dispatch skips idempotently
        with _local_api(
            [
                (200, json.dumps({"workflow_runs": [ci_run]}).encode()),
                (200, json.dumps(ci_run).encode()),
                (200, json.dumps(green_jobs).encode()),
            ]
        ) as (client, requests):
            res = ACTIONS.colabone_dispatch_ci(
                client, workflow="ci.yml", ref="main", sha=sha
            )
        self.assertFalse(res["dispatched"])
        self.assertTrue(res["already_passed"])
        self.assertEqual(
            res["platforms"],
            {"windows-latest": "success", "macos-latest": "success"},
        )
        self.assertEqual([m for m, _, _, _ in requests], ["GET", "GET", "GET"])

        # 3. Already running -> dispatch skips without duplicate run
        active_run = {**ci_run, "status": "in_progress", "conclusion": None}
        active_jobs = {
            "jobs": [
                {
                    "id": 101,
                    "name": "check (windows-latest)",
                    "status": "in_progress",
                    "conclusion": None,
                    "steps": [],
                }
            ]
        }
        with _local_api(
            [
                (200, json.dumps({"workflow_runs": [active_run]}).encode()),
                (200, json.dumps(active_run).encode()),
                (200, json.dumps(active_jobs).encode()),
            ]
        ) as (client, requests):
            res = ACTIONS.colabone_dispatch_ci(
                client, workflow="ci.yml", ref="main", sha=sha
            )
        self.assertFalse(res["dispatched"])
        self.assertTrue(res["already_running"])
        self.assertEqual([m for m, _, _, _ in requests], ["GET", "GET", "GET"])

        # 4. --force dispatches even without checking prior runs
        with _local_api([(204, b"")]) as (client, requests):
            res = ACTIONS.colabone_dispatch_ci(
                client, workflow="ci.yml", ref="main", sha=sha, force=True
            )
        self.assertTrue(res["dispatched"])
        self.assertEqual([m for m, _, _, _ in requests], ["POST"])

        # 5. preflight-status fails if only one platform succeeded (missing macos-latest)
        partial_jobs = {
            "jobs": [
                {
                    "id": 101,
                    "name": "check (windows-latest)",
                    "status": "completed",
                    "conclusion": "success",
                    "steps": [],
                }
            ]
        }
        with _local_api(
            [
                (200, json.dumps({"workflow_runs": [ci_run]}).encode()),
                (200, json.dumps(ci_run).encode()),
                (200, json.dumps(partial_jobs).encode()),
            ]
        ) as (client, _requests):
            status = ACTIONS.colabone_preflight_status(
                client, workflow="ci.yml", branch="main", sha=sha
            )
        self.assertFalse(status["ready"])
        self.assertEqual(status["state"], "failed")
        self.assertEqual(status["platforms"]["windows-latest"], "success")
        self.assertEqual(status["platforms"]["macos-latest"], "missing")

        # 6. CLI preflight-status --require-green returns 0 when ready and 1 when not ready
        with patch.object(
            ACTIONS, "colabone_run_command", return_value="https://github.com/owner/repo"
        ), patch.object(
            ACTIONS, "colabone_resolve_credential", return_value="token"
        ), patch.object(
            ACTIONS,
            "colabone_preflight_status",
            return_value={"ready": True, "state": "ready"},
        ):
            self.assertEqual(
                ACTIONS.main(
                    ["preflight-status", "--branch", "main", "--sha", sha, "--require-green"]
                ),
                0,
            )

        with patch.object(
            ACTIONS, "colabone_run_command", return_value="https://github.com/owner/repo"
        ), patch.object(
            ACTIONS, "colabone_resolve_credential", return_value="token"
        ), patch.object(
            ACTIONS,
            "colabone_preflight_status",
            return_value={"ready": False, "state": "missing"},
        ):
            self.assertEqual(
                ACTIONS.main(
                    ["preflight-status", "--branch", "main", "--sha", sha, "--require-green"]
                ),
                1,
            )

        # 7. CLI list/rerun-failed accept --branch with workflow_dispatch default event
        older = {**ci_run, "id": 54, "created_at": "2026-11-25T09:50:00Z"}
        with _local_api(
            [(200, json.dumps({"workflow_runs": [older, ci_run]}).encode())]
        ) as (client, _requests):
            filtered = ACTIONS.colabone_list_runs(
                client,
                workflow="ci.yml",
                ref_name="main",
                sha=sha,
                event="workflow_dispatch",
                created_after="2026-11-25T09:55:00Z",
            )
        self.assertEqual([item["id"] for item in filtered], [55])

        failed_ci = {**ci_run, "conclusion": "failure"}
        with _local_api(
            [(200, json.dumps(failed_ci).encode()), (201, b"Created")]
        ) as (client, requests):
            retried = ACTIONS.colabone_rerun_failed(
                client, 55, "ci.yml", "main", sha, 1, event="workflow_dispatch"
            )
        self.assertTrue(retried["accepted"])
        self.assertEqual([m for m, _, _, _ in requests], ["GET", "POST"])


if __name__ == "__main__":
    unittest.main()
