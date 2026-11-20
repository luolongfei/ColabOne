from pathlib import Path

import pytest

from tools.install import replace_tree


def test_reinstall_removes_obsolete_resources_and_keeps_user_state(tmp_path):
    source, target = tmp_path / "source", tmp_path / "installed" / "app"
    source.mkdir()
    target.mkdir(parents=True)
    (source / "current.py").write_text("new", encoding="utf-8")
    (target / "obsolete.py").write_text("old", encoding="utf-8")
    state = target.parent / "settings.json"
    state.write_text("user settings", encoding="utf-8")
    replace_tree(source, target)
    assert sorted(p.name for p in target.iterdir()) == ["current.py"]
    assert state.read_text(encoding="utf-8") == "user settings"


def test_failed_install_restores_previous_tree(tmp_path, monkeypatch):
    source, target = tmp_path / "source", tmp_path / "app"
    source.mkdir()
    target.mkdir()
    (source / "new").touch()
    (target / "previous").touch()
    rename = Path.rename

    def fail(self, destination):
        if self.name == "new" and self.parent.name.startswith(".colabone-update-"):
            raise OSError("replacement failed")
        return rename(self, destination)

    monkeypatch.setattr(Path, "rename", fail)
    with pytest.raises(OSError, match="replacement failed"):
        replace_tree(source, target)
    assert sorted(p.name for p in target.iterdir()) == ["previous"]


def test_failed_rollback_keeps_the_recoverable_backup(tmp_path, monkeypatch):
    source, target = tmp_path / "source", tmp_path / "app"
    source.mkdir()
    target.mkdir()
    (target / "original").touch()
    rename = Path.rename

    def fail(self, destination):
        if Path(destination) == target:
            raise OSError("destination temporarily locked")
        return rename(self, destination)

    monkeypatch.setattr(Path, "rename", fail)
    with pytest.raises(OSError, match="locked"):
        replace_tree(source, target)
    assert len(list(tmp_path.glob(".colabone-update-*/previous/original"))) == 1


def test_gpl3_license_notices_and_catalog_packaging_invariants():
    root = Path(__file__).resolve().parents[1]
    license_text = (root / "LICENSE").read_text(encoding="utf-8")
    assert "GNU GENERAL PUBLIC LICENSE" in license_text
    assert "Version 3" in license_text
    notice_text = (root / "NOTICE").read_text(encoding="utf-8")
    assert "GPL-3.0" in notice_text
    assert "Apache License, Version 2.0" in notice_text
    assert "ComfyUI" in notice_text
    assert (root / "catalog/comfyui.json").is_file()
    assert not (root / "catalog/creative.json").exists()
    assert not (root / "third_party/stable-diffusion.cpp.LICENSE").exists()
    assert (root / "third_party/rclone.LICENSE").is_file()
    assert (root / "third_party/sarasa-gothic.LICENSE").is_file()
    assert "Sarasa UI SC" in notice_text
    assert "SIL OFL 1.1" in notice_text
    for weight in ("regular", "semibold", "bold"):
        font_file = root / f"frontend/src/assets/fonts/sarasa-ui-sc-{weight}.woff2"
        assert font_file.is_file()
        assert font_file.read_bytes()[:4] == b"wOF2"
