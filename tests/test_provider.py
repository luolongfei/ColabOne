from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock
import pytest
from bridge.auth import Auth
from bridge.provider import Provider
from bridge.controller import Controller
from conftest import MemoryVault


def make_provider():
    vault = MemoryVault()
    provider = Provider(Auth(vault), vault, Path("."))
    return provider, vault


def assignment(endpoint):
    return SimpleNamespace(endpoint=endpoint)


def store_session(vault):
    vault.set(
        "runtime_session",
        {
            "name": "colabone",
            "endpoint": "owned",
            "token": "not-real",
            "url": "https://runtime.invalid",
        },
    )


def test_release_only_owned_endpoint():
    p, vault = make_provider()
    store_session(vault)
    client = Mock()
    client.list_assignments.side_effect = [
        [assignment("owned"), assignment("unrelated")],
        [assignment("unrelated")],
    ]
    p.client_factory = lambda: client
    p.release()
    client.unassign.assert_called_once_with("owned")
    assert not vault.get("runtime_session")


def test_no_adoption_of_unrelated_runtime():
    p, vault = make_provider()
    client = Mock()
    p.client_factory = lambda: client
    assert p.session(refresh=True) is None
    p.release()
    client.unassign.assert_not_called()


def test_release_failure_retains_ownership():
    p, vault = make_provider()
    store_session(vault)
    client = Mock()
    client.list_assignments.side_effect = OSError("offline")
    p.client_factory = lambda: client
    with pytest.raises(OSError):
        p.release()
    assert vault.get("runtime_session")["endpoint"] == "owned"


def test_unknown_rate_rolls_back_new_allocation(monkeypatch):
    p, vault = make_provider()
    monkeypatch.setattr(p, "account", lambda: {"rate": 0, "balance": 100})
    monkeypatch.setattr(p, "allocate", lambda _gpu: SimpleNamespace(endpoint="new"))
    release = Mock()
    monkeypatch.setattr(p, "release", release)
    with pytest.raises(RuntimeError, match="费率"):
        p.start("T4", 5, lambda _: None)
    release.assert_called_once()


def test_new_allocation_deploy_failure_rolls_back(monkeypatch):
    p, vault = make_provider()
    monkeypatch.setattr(p, "account", lambda: {"rate": 1, "balance": 100})
    monkeypatch.setattr(p, "allocate", lambda _gpu: SimpleNamespace(endpoint="new"))
    monkeypatch.setattr(p, "ready", Mock(side_effect=RuntimeError("boot failed")))
    release = Mock()
    monkeypatch.setattr(p, "release", release)
    with pytest.raises(RuntimeError, match="boot failed"):
        p.start("T4", 5, lambda _: None)
    release.assert_called_once()


def test_snapshot_and_catalog_do_not_allocate(tmp_path):
    root = Path(__file__).resolve().parents[1]
    c = Controller(root, vault=MemoryVault(), config_dir=tmp_path, background=False)
    c.provider = Mock()
    assert c.snapshot()["runtime"]["state"] == "stopped"
    assert c.snapshot()["models"]
    c.provider.allocate.assert_not_called()


def test_settings_never_write_secret_to_plaintext(tmp_path):
    c = Controller(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.save_settings(
        {
            "hf_token": "hf-test",
            "max_cu": 5,
        }
    )
    text = (tmp_path / "settings.json").read_text(encoding="utf-8")
    assert "secret-value" not in text and "hf-test" not in text
    assert c.vault.get("hf") == "hf-test"
    assert "secret-value" not in str(c.snapshot())


def test_release_blocks_on_unsynced_data(tmp_path):
    c = Controller(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.remote = Mock(side_effect=RuntimeError("会话尚未同步"))
    c.provider = Mock()
    with pytest.raises(RuntimeError, match="尚未同步"):
        c.release()
    c.provider.release.assert_not_called()
    c.release(force=True)
    c.provider.release.assert_called_once()


def test_kernel_connection_reused_and_rotated_without_replaying(monkeypatch):
    p, vault = make_provider()
    store_session(vault)
    s = p.session()
    first, second = Mock(), Mock()
    first.execute_code.return_value = [{"text": "first"}]
    factory = Mock(side_effect=[first, second])
    monkeypatch.setattr("bridge.provider.ColabRuntime", factory)
    p.execute(s, "pass")
    p.execute(s, "pass")
    assert factory.call_count == 1
    assert first.execute_code.call_count == 2
    s.token = "rotated"
    second.execute_code.side_effect = TimeoutError("uncertain mutation outcome")
    with pytest.raises(TimeoutError):
        p.execute(s, "mutate()")
    first.stop.assert_called_once_with(shutdown_kernel=False)
    second.execute_code.assert_called_once()
    second.stop.assert_called_once_with(shutdown_kernel=False)
    assert p.kernel_runtime is None
