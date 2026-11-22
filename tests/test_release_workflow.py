from __future__ import annotations

import json
import re
from pathlib import Path

import pytest

from tools.release_version import (
    ColabOneReleaseVersionError,
    colabone_parse_release_tag,
    colabone_set_release_version,
    colabone_validate_release_versions,
    main as release_version_main,
)

ROOT = Path(__file__).resolve().parents[1]


def _read(relative_path: str) -> str:
    return (ROOT / relative_path).read_text(encoding="utf-8")


def _extract_on_block(workflow: str) -> str:
    match = re.search(r"^on:\n(.*?)(?=^[A-Za-z_][\w-]*:)", workflow, re.MULTILINE | re.DOTALL)
    assert match is not None, "Expected top-level `on:` trigger block"
    return match.group(1)


def test_ci_workflow_does_not_trigger_on_branch_pushes_or_pull_requests():
    workflow = _read(".github/workflows/ci.yml")
    on_block = _extract_on_block(workflow)

    assert "workflow_dispatch:" in on_block
    assert "workflow_call:" in on_block
    assert "push:" not in on_block
    assert "branches:" not in on_block
    assert "pull_request:" not in on_block
    assert "ref: ${{ inputs.ref || github.sha }}" in workflow


def test_release_workflow_triggers_only_on_version_tags_and_manual_dispatch():
    workflow = _read(".github/workflows/release.yml")
    on_block = _extract_on_block(workflow)

    assert "push:" in on_block
    assert "tags:" in on_block
    assert '- "v*"' in on_block
    assert "workflow_dispatch:" in on_block
    assert "branches:" not in on_block
    assert "pull_request:" not in on_block
    assert "python3 tools/release_version.py --tag" in workflow
    assert "uses: ./.github/workflows/ci.yml" in workflow
    assert "needs: [release-metadata, build]" in workflow
    assert "SHA256SUMS.txt" in workflow
    assert "gh release create" in workflow
    assert "gh release upload" in workflow
    assert "group: release-${{ inputs.tag || github.ref_name }}" in workflow
    assert "format('refs/tags/{0}', inputs.tag)" in workflow
    assert "%(contents:subject)" in workflow


def test_repository_version_manifests_are_consistent_and_default_to_valid_semver():
    version = colabone_validate_release_versions(ROOT)
    assert colabone_parse_release_tag(f"v{version}") == version
    assert colabone_validate_release_versions(ROOT, f"v{version}") == version
    assert release_version_main(["--root", str(ROOT), "--check"]) == 0
    assert release_version_main(["--root", str(ROOT), "--tag", f"v{version}"]) == 0


@pytest.mark.parametrize(
    ("tag", "expected"),
    [
        ("v0.1.0", "0.1.0"),
        ("v1.2.3", "1.2.3"),
        ("v0.2.0-beta.1", "0.2.0-beta.1"),
        ("v10.20.30-rc.1", "10.20.30-rc.1"),
    ],
)
def test_parse_release_tag_accepts_valid_semver_tags(tag: str, expected: str):
    assert colabone_parse_release_tag(tag) == expected


@pytest.mark.parametrize(
    "bad_tag",
    [
        "",
        "0.1.0",
        "V0.1.0",
        "v01.0.0",
        "v0.1",
        "v0.1.0.0",
        "v0.1.0+build.1",
        "vfoo",
        "release-0.1.0",
    ],
)
def test_parse_release_tag_rejects_invalid_tags(bad_tag: str):
    with pytest.raises(ColabOneReleaseVersionError):
        colabone_parse_release_tag(bad_tag)


def test_release_version_detects_mismatches_and_updates_all_manifests(tmp_path: Path):
    (tmp_path / "console").mkdir()
    (tmp_path / "frontend").mkdir()
    (tmp_path / "console/mygo.json").write_text(
        json.dumps({"name": "ColabOne", "version": "0.1.0"}, indent=2) + "\n",
        encoding="utf-8",
    )
    (tmp_path / "frontend/package.json").write_text(
        json.dumps({"name": "colabone-ui", "version": "0.1.0"}, indent=2) + "\n",
        encoding="utf-8",
    )
    (tmp_path / "frontend/package-lock.json").write_text(
        json.dumps(
            {
                "name": "colabone-ui",
                "version": "0.1.0",
                "lockfileVersion": 3,
                "packages": {"": {"name": "colabone-ui", "version": "0.1.0"}},
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )

    assert colabone_validate_release_versions(tmp_path, "v0.1.0") == "0.1.0"

    with pytest.raises(ColabOneReleaseVersionError, match="Release versions do not match"):
        colabone_validate_release_versions(tmp_path, "v0.2.0")

    updated = colabone_set_release_version(tmp_path, "v0.2.0-rc.1")
    assert updated == "0.2.0-rc.1"
    assert colabone_validate_release_versions(tmp_path, "v0.2.0-rc.1") == "0.2.0-rc.1"

    lock_data = json.loads(
        (tmp_path / "frontend/package-lock.json").read_text(encoding="utf-8")
    )
    assert lock_data["version"] == "0.2.0-rc.1"
    assert lock_data["packages"][""]["version"] == "0.2.0-rc.1"

    # Introduce drift in package.json and confirm validation catches it.
    (tmp_path / "frontend/package.json").write_text(
        json.dumps({"name": "colabone-ui", "version": "0.1.9"}, indent=2) + "\n",
        encoding="utf-8",
    )
    with pytest.raises(ColabOneReleaseVersionError, match="frontend/package.json=0.1.9"):
        colabone_validate_release_versions(tmp_path)
    assert release_version_main(["--root", str(tmp_path), "--check"]) == 1

    # Setting version fails atomically without partially mutating console/mygo.json when package.json is corrupt.
    before_console = (tmp_path / "console/mygo.json").read_bytes()
    (tmp_path / "frontend/package.json").write_text("{not-json", encoding="utf-8")
    with pytest.raises(ColabOneReleaseVersionError, match="Invalid JSON"):
        colabone_set_release_version(tmp_path, "0.3.0")
    assert (tmp_path / "console/mygo.json").read_bytes() == before_console
    assert b"\r\n" not in before_console
