from __future__ import annotations

import json
from pathlib import Path

import pytest

from tools.package import stage_mygo_updater_artifacts
from tools.release_update_manifest import (
    ColabOneUpdateManifestError,
    colabone_prepare_update_manifests,
    main as release_update_manifest_main,
)


def _create_signed_target_artifacts(
    dist_dir: Path,
    *,
    target: str = "windows-amd64",
    version: str = "0.1.7",
    archive_bytes: bytes = b"signed-archive-payload",
    deltas: list[tuple[str, bytes]] | None = None,
    extra_manifest_fields: dict | None = None,
    manifest_overrides: dict | None = None,
) -> tuple[Path, dict]:
    """Write a consistent MyGo update manifest, archive, and optional deltas into dist_dir."""
    dist_dir.mkdir(parents=True, exist_ok=True)
    archive_name = f"colabone-{version}-{target}.tar.gz"
    archive_path = dist_dir / archive_name
    archive_path.write_bytes(archive_bytes)

    manifest: dict = {
        "version": version,
        "date": "2026-11-23T18:00:00Z",
        "url": f"https://github.com/luolongfei/ColabOne/releases/download/v{version}/{archive_name}",
        "size": len(archive_bytes),
        "signature": "ed25519-base64-manifest-sig==",
    }

    if deltas is not None:
        delta_entries = []
        for from_ver, delta_bytes in deltas:
            delta_name = f"colabone-{from_ver}-to-{version}-{target}.delta"
            (dist_dir / delta_name).write_bytes(delta_bytes)
            delta_entries.append(
                {
                    "from": from_ver,
                    "url": f"https://github.com/luolongfei/ColabOne/releases/download/v{version}/{delta_name}",
                    "size": len(delta_bytes),
                    "signature": f"ed25519-delta-{from_ver}-sig==",
                }
            )
        manifest["deltas"] = delta_entries

    if extra_manifest_fields:
        manifest.update(extra_manifest_fields)
    if manifest_overrides:
        manifest.update(manifest_overrides)

    manifest_path = dist_dir / f"update-{target}.json"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return manifest_path, manifest


# ---------------------------------------------------------------------------
# tools/release_update_manifest.py
# ---------------------------------------------------------------------------


def test_version_mismatch_and_empty_version_rejected(tmp_path: Path):
    dist = tmp_path / "dist"
    _create_signed_target_artifacts(dist, target="windows-amd64", version="0.1.6")

    with pytest.raises(ColabOneUpdateManifestError, match="does not match expected"):
        colabone_prepare_update_manifests(
            dist, version="0.1.7", notes="notes", targets=("windows-amd64",)
        )

    with pytest.raises(ColabOneUpdateManifestError, match="must not be empty"):
        colabone_prepare_update_manifests(
            dist, version="v", notes="notes", targets=("windows-amd64",)
        )

    # Matching with optional leading "v" prefix succeeds cleanly.
    updated = colabone_prepare_update_manifests(
        dist, version="v0.1.6", notes="notes", targets=("windows-amd64",)
    )
    assert updated == [dist / "update-windows-amd64.json"]


@pytest.mark.parametrize("bad_sig", [None, "", "   ", 12345])
def test_missing_ed25519_signature_on_manifest_rejected(tmp_path: Path, bad_sig):
    dist = tmp_path / "dist"
    manifest_path, manifest = _create_signed_target_artifacts(dist, target="windows-amd64")
    if bad_sig is None:
        manifest.pop("signature")
    else:
        manifest["signature"] = bad_sig
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")

    with pytest.raises(ColabOneUpdateManifestError, match="missing Ed25519 signature"):
        colabone_prepare_update_manifests(
            dist, version="0.1.7", notes="notes", targets=("windows-amd64",)
        )


@pytest.mark.parametrize("bad_delta_sig", [None, "", "   ", 42])
def test_missing_ed25519_signature_on_delta_rejected(tmp_path: Path, bad_delta_sig):
    dist = tmp_path / "dist"
    manifest_path, manifest = _create_signed_target_artifacts(
        dist,
        target="darwin-arm64",
        deltas=[("0.1.6", b"delta-bytes")],
    )
    if bad_delta_sig is None:
        manifest["deltas"][0].pop("signature")
    else:
        manifest["deltas"][0]["signature"] = bad_delta_sig
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")

    with pytest.raises(ColabOneUpdateManifestError, match=r"deltas\[0\] is missing signature"):
        colabone_prepare_update_manifests(
            dist, version="0.1.7", notes="notes", targets=("darwin-arm64",)
        )


@pytest.mark.parametrize(
    "bad_url",
    [
        "http://github.com/luolongfei/ColabOne/releases/download/v0.1.7/colabone-0.1.7-windows-amd64.tar.gz",
        "file:///tmp/colabone-0.1.7-windows-amd64.tar.gz",
        "colabone-0.1.7-windows-amd64.tar.gz",
        "",
    ],
)
def test_non_https_url_rejected_on_manifest_and_delta(tmp_path: Path, bad_url: str):
    # Reject non-HTTPS manifest archive URL.
    dist_manifest = tmp_path / "dist-manifest"
    _create_signed_target_artifacts(
        dist_manifest,
        target="windows-amd64",
        manifest_overrides={"url": bad_url},
    )
    with pytest.raises(ColabOneUpdateManifestError, match="must have an https URL"):
        colabone_prepare_update_manifests(
            dist_manifest, version="0.1.7", notes="notes", targets=("windows-amd64",)
        )

    # Reject non-HTTPS delta patch URL.
    dist_delta = tmp_path / "dist-delta"
    manifest_path, manifest = _create_signed_target_artifacts(
        dist_delta,
        target="windows-amd64",
        deltas=[("0.1.6", b"delta-bytes")],
    )
    manifest["deltas"][0]["url"] = bad_url
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    with pytest.raises(ColabOneUpdateManifestError, match="must have an https URL"):
        colabone_prepare_update_manifests(
            dist_delta, version="0.1.7", notes="notes", targets=("windows-amd64",)
        )


@pytest.mark.parametrize("bad_size", [999999, 0, -5, "22", None])
def test_archive_size_mismatch_rejected(tmp_path: Path, bad_size):
    dist = tmp_path / "dist"
    _create_signed_target_artifacts(
        dist,
        target="windows-amd64",
        archive_bytes=b"0123456789abcdefghij",
        manifest_overrides={"size": bad_size},
    )

    with pytest.raises(ColabOneUpdateManifestError, match="size mismatch"):
        colabone_prepare_update_manifests(
            dist, version="0.1.7", notes="notes", targets=("windows-amd64",)
        )


def test_missing_delta_file_and_delta_size_mismatch_rejected(tmp_path: Path):
    dist = tmp_path / "dist"
    manifest_path, manifest = _create_signed_target_artifacts(
        dist,
        target="windows-amd64",
        deltas=[("0.1.5", b"delta-5-to-7"), ("0.1.6", b"delta-6-to-7")],
    )

    # Delta size mismatch against disk bytes.
    manifest["deltas"][1]["size"] = 999
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    with pytest.raises(ColabOneUpdateManifestError, match="size mismatch"):
        colabone_prepare_update_manifests(
            dist, version="0.1.7", notes="notes", targets=("windows-amd64",)
        )

    # Fix size back, then delete the delta file from disk.
    manifest["deltas"][1]["size"] = len(b"delta-6-to-7")
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    (dist / "colabone-0.1.6-to-0.1.7-windows-amd64.delta").unlink()
    with pytest.raises(ColabOneUpdateManifestError, match="Missing delta patch"):
        colabone_prepare_update_manifests(
            dist, version="0.1.7", notes="notes", targets=("windows-amd64",)
        )


def test_invalid_deltas_structure_rejected(tmp_path: Path):
    dist = tmp_path / "dist"
    _create_signed_target_artifacts(
        dist,
        target="windows-amd64",
        manifest_overrides={"deltas": "not-a-list"},
    )
    with pytest.raises(ColabOneUpdateManifestError, match="deltas must be a list"):
        colabone_prepare_update_manifests(
            dist, version="0.1.7", notes="notes", targets=("windows-amd64",)
        )

    _create_signed_target_artifacts(
        dist,
        target="windows-amd64",
        manifest_overrides={"deltas": ["not-an-object"]},
    )
    with pytest.raises(ColabOneUpdateManifestError, match=r"deltas\[0\] must be an object"):
        colabone_prepare_update_manifests(
            dist, version="0.1.7", notes="notes", targets=("windows-amd64",)
        )


def test_crlf_normalization_and_existing_manifest_fields_preserved(tmp_path: Path):
    dist = tmp_path / "dist"
    previous_history = [
        {
            "version": "0.1.6",
            "date": "2026-11-10T10:00:00Z",
            "url": "https://github.com/luolongfei/ColabOne/releases/download/v0.1.6/colabone-0.1.6-windows-amd64.tar.gz",
            "size": 123456,
            "signature": "prev-sig==",
        }
    ]
    manifest_path, original = _create_signed_target_artifacts(
        dist,
        target="windows-amd64",
        archive_bytes=b"signed-full-archive-bytes",
        deltas=[("0.1.5", b"patch-5"), ("0.1.6", b"patch-6")],
        extra_manifest_fields={
            "previous": previous_history,
            "notes": "placeholder notes",
        },
    )

    crlf_notes = (
        "\r\nColabOne v0.1.7\r\n\r\n"
        "## 新增功能\r\n- 支持应用内自动更新\r\n\r\n"
        "---\r\n\r\n"
        "## New Features\r\n- Add in-app auto-update\r\n\r\n"
    )
    updated_paths = colabone_prepare_update_manifests(
        dist,
        version="0.1.7",
        notes=crlf_notes,
        targets=("windows-amd64",),
    )
    assert updated_paths == [manifest_path]

    raw_bytes = manifest_path.read_bytes()
    assert b"\r\n" not in raw_bytes
    assert raw_bytes.endswith(b"\n")

    updated = json.loads(raw_bytes.decode("utf-8"))
    assert updated["notes"] == (
        "ColabOne v0.1.7\n\n"
        "## 新增功能\n- 支持应用内自动更新\n\n"
        "---\n\n"
        "## New Features\n- Add in-app auto-update"
    )
    assert "\r" not in updated["notes"]
    assert updated["previous"] == previous_history
    assert updated["deltas"] == original["deltas"]
    assert updated["signature"] == original["signature"]
    assert updated["url"] == original["url"]
    assert updated["size"] == original["size"]
    assert updated["date"] == original["date"]

    # Blank notes should leave the already-injected notes untouched.
    colabone_prepare_update_manifests(
        dist, version="0.1.7", notes="   \r\n  ", targets=("windows-amd64",)
    )
    after_blank = json.loads(manifest_path.read_text(encoding="utf-8"))
    assert after_blank["notes"] == updated["notes"]


def test_cli_exit_codes_for_invalid_and_missing_files(tmp_path: Path, capsys):
    empty_dist = tmp_path / "empty-dist"
    empty_dist.mkdir()

    # Missing manifest -> exit code 1 with clear stderr diagnostic.
    rc = release_update_manifest_main(
        ["--dist", str(empty_dist), "--version", "0.1.7", "--targets", "windows-amd64"]
    )
    captured = capsys.readouterr()
    assert rc == 1
    assert "Error:" in captured.err
    assert "MYGO_UPDATER_PRIVATE_KEY" in captured.err

    # Invalid JSON manifest -> exit code 1.
    bad_json_dist = tmp_path / "bad-json"
    bad_json_dist.mkdir()
    (bad_json_dist / "update-windows-amd64.json").write_text("{not-json", encoding="utf-8")
    rc = release_update_manifest_main(
        ["--dist", str(bad_json_dist), "--version", "0.1.7", "--targets", "windows-amd64"]
    )
    captured = capsys.readouterr()
    assert rc == 1
    assert "Invalid JSON" in captured.err

    # Non-object JSON manifest -> exit code 1.
    list_json_dist = tmp_path / "list-json"
    list_json_dist.mkdir()
    (list_json_dist / "update-windows-amd64.json").write_text("[]", encoding="utf-8")
    rc = release_update_manifest_main(
        ["--dist", str(list_json_dist), "--version", "0.1.7", "--targets", "windows-amd64"]
    )
    captured = capsys.readouterr()
    assert rc == 1
    assert "Expected JSON object" in captured.err

    # Valid dist with CRLF notes file -> exit code 0 and prints updated manifest path.
    good_dist = tmp_path / "good-dist"
    manifest_path, _ = _create_signed_target_artifacts(good_dist, target="darwin-arm64")
    notes_file = tmp_path / "notes.md"
    notes_file.write_bytes(b"## \xe4\xb8\xad\xe6\x96\x87\r\n---\r\n## English\r\n")
    rc = release_update_manifest_main(
        [
            "--dist",
            str(good_dist),
            "--version",
            "0.1.7",
            "--notes-file",
            str(notes_file),
            "--targets",
            "darwin-arm64",
        ]
    )
    captured = capsys.readouterr()
    assert rc == 0
    assert str(manifest_path) in captured.out
    assert json.loads(manifest_path.read_text(encoding="utf-8"))["notes"] == "## 中文\n---\n## English"


# ---------------------------------------------------------------------------
# tools/package.py stage_mygo_updater_artifacts
# ---------------------------------------------------------------------------


def test_stage_mygo_updater_artifacts_noop_when_manifest_absent(tmp_path: Path):
    platform_dir = tmp_path / "console-dist" / "windows-amd64"
    platform_dir.mkdir(parents=True)
    out = tmp_path / "dist"
    out.mkdir()

    # Even if stray archive/delta files exist, absence of update-<target>.json is a clean no-op.
    (platform_dir / "colabone-0.1.7-windows-amd64.tar.gz").write_bytes(b"stray-archive")
    (platform_dir / "colabone-0.1.6-to-0.1.7-windows-amd64.delta").write_bytes(b"stray-delta")

    copied = stage_mygo_updater_artifacts(
        platform_dir=platform_dir,
        out=out,
        version="0.1.7",
        goos="windows",
        goarch="amd64",
    )
    assert copied == []
    assert list(out.iterdir()) == []


def test_stage_mygo_updater_artifacts_rejects_version_mismatch_or_missing_signature(tmp_path: Path):
    out = tmp_path / "dist"
    out.mkdir()

    # Version mismatch -> RuntimeError.
    mismatch_dir = tmp_path / "mismatch"
    _create_signed_target_artifacts(mismatch_dir, target="darwin-arm64", version="0.1.6")
    with pytest.raises(RuntimeError, match="version"):
        stage_mygo_updater_artifacts(
            platform_dir=mismatch_dir,
            out=out,
            version="0.1.7",
            goos="darwin",
            goarch="arm64",
        )
    assert list(out.iterdir()) == []

    # Missing signature -> RuntimeError.
    unsigned_dir = tmp_path / "unsigned"
    manifest_path, manifest = _create_signed_target_artifacts(
        unsigned_dir, target="darwin-arm64", version="0.1.7"
    )
    manifest["signature"] = ""
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    with pytest.raises(RuntimeError, match="missing signature"):
        stage_mygo_updater_artifacts(
            platform_dir=unsigned_dir,
            out=out,
            version="0.1.7",
            goos="darwin",
            goarch="arm64",
        )
    assert list(out.iterdir()) == []


def test_stage_mygo_updater_artifacts_copies_archive_multiple_deltas_and_manifest(tmp_path: Path):
    platform_dir = tmp_path / "console-dist" / "windows-amd64"
    out = tmp_path / "dist"
    out.mkdir()

    _create_signed_target_artifacts(
        platform_dir,
        target="windows-amd64",
        version="0.1.7",
        archive_bytes=b"full-archive-0.1.7",
        deltas=[
            ("0.1.4", b"delta-4-to-7"),
            ("0.1.5", b"delta-5-to-7"),
            ("0.1.6", b"delta-6-to-7"),
        ],
        # Also verify leading "v" in manifest version is accepted cleanly.
        manifest_overrides={"version": "v0.1.7"},
    )
    # Unrelated files in platform_dir must not be copied.
    (platform_dir / "ColabOne.exe").write_bytes(b"binary-exe")
    (platform_dir / "colabone-0.1.6-to-0.1.7-darwin-arm64.delta").write_bytes(b"other-target")
    (platform_dir / "colabone-0.1.5-to-0.1.6-windows-amd64.delta").write_bytes(b"older-release")

    copied = stage_mygo_updater_artifacts(
        platform_dir=platform_dir,
        out=out,
        version="0.1.7",
        goos="windows",
        goarch="amd64",
    )

    expected_names = [
        "colabone-0.1.7-windows-amd64.tar.gz",
        "colabone-0.1.4-to-0.1.7-windows-amd64.delta",
        "colabone-0.1.5-to-0.1.7-windows-amd64.delta",
        "colabone-0.1.6-to-0.1.7-windows-amd64.delta",
        "update-windows-amd64.json",
    ]
    assert [p.name for p in copied] == expected_names
    assert sorted(p.name for p in out.iterdir()) == sorted(expected_names)
    assert (out / "colabone-0.1.7-windows-amd64.tar.gz").read_bytes() == b"full-archive-0.1.7"
    assert (out / "colabone-0.1.4-to-0.1.7-windows-amd64.delta").read_bytes() == b"delta-4-to-7"
    assert (out / "colabone-0.1.5-to-0.1.7-windows-amd64.delta").read_bytes() == b"delta-5-to-7"
    assert (out / "colabone-0.1.6-to-0.1.7-windows-amd64.delta").read_bytes() == b"delta-6-to-7"
    assert (out / "update-windows-amd64.json").read_bytes() == (
        platform_dir / "update-windows-amd64.json"
    ).read_bytes()


def test_stage_mygo_updater_artifacts_rejects_missing_archive(tmp_path: Path):
    platform_dir = tmp_path / "console-dist" / "darwin-arm64"
    out = tmp_path / "dist"
    out.mkdir()

    _create_signed_target_artifacts(platform_dir, target="darwin-arm64", version="0.1.7")
    (platform_dir / "colabone-0.1.7-darwin-arm64.tar.gz").unlink()

    with pytest.raises(RuntimeError, match="missing archive"):
        stage_mygo_updater_artifacts(
            platform_dir=platform_dir,
            out=out,
            version="0.1.7",
            goos="darwin",
            goarch="arm64",
        )
    assert list(out.iterdir()) == []
