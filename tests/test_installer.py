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
