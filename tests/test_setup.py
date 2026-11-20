import json
import threading
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock
from urllib.parse import quote
import pytest
from bridge.auth import PUBLIC_SCOPES, parse_authorization_reply
from conftest import MemoryVault
from bridge.controller import ColabOneController

CODE = "4/valid-example-code-that-is-not-a-real-secret"


@pytest.mark.parametrize(
    "text",
    [
        CODE,
        "Authorization complete\nCopy this code:\n" + CODE,
        "https://localhost/?state=xyz&code=" + quote(CODE, safe=""),
    ],
)
def test_authorization_reply_accepts_complete_page_or_url(text):
    assert parse_authorization_reply(text) == CODE


@pytest.mark.parametrize(
    "text", ["", "not a code", CODE + "\n4/another-code-long-enough-to-be-ambiguous"]
)
def test_authorization_reply_rejects_missing_or_ambiguous_code(text):
    with pytest.raises(ValueError):
        parse_authorization_reply(text)


def test_colab_oauth_scopes_include_drive_file_for_one_time_mount():
    assert PUBLIC_SCOPES
    assert "https://www.googleapis.com/auth/drive.file" in PUBLIC_SCOPES
    assert "https://www.googleapis.com/auth/colaboratory" in PUBLIC_SCOPES


def test_gpu_start_runs_without_browser_drive_consent_or_cloud_project(tmp_path):
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.auth = Mock()
    c.auth.build_rclone_conf.return_value = ""
    c.auth.has_drive_scope.return_value = True
    c.provider = Mock()
    c.provider.account.return_value = {"logged_in": True, "assignments": 1, "rate": 1.5}
    c.remote = Mock()
    c.refresh_remote = Mock()
    c.start()
    c.provider.start.assert_called_once()
    assert c.remote.call_args_list[0].args[:2] == ("POST", "configure")
    assert c.snapshot()["setup"]["drive_authorized"] is True
    assert c.snapshot()["settings"]["auto_drive"] is True


def test_google_loopback_checks_state_and_stores_login(monkeypatch):
    import requests
    from bridge.auth import ColabOneAuth

    vault = MemoryVault()
    auth = ColabOneAuth(vault)
    captured_scopes = []

    def make_flow(_cfg, scopes, **_kwargs):
        captured_scopes.extend(scopes)
        return flow

    flow = SimpleNamespace(
        authorization_url=lambda **_: (
            "https://accounts.google.com/test",
            "expected-state",
        ),
        fetch_token=Mock(),
        credentials=SimpleNamespace(
            to_json=lambda: json.dumps({"token": "test-token"})
        ),
    )
    monkeypatch.setattr("bridge.auth.InstalledAppFlow.from_client_config", make_flow)
    monkeypatch.setattr(
        auth,
        "fetch_profile",
        lambda _: {"sub": "test-user", "email": "test@example.com"},
    )
    results = []
    workers = []

    def ready(_):
        def callback():
            with requests.Session() as http:
                http.trust_env = False
                results.append(
                    http.get(
                        flow.redirect_uri,
                        params={"state": "wrong", "code": CODE},
                        timeout=3,
                    ).status_code
                )
                results.append(
                    http.get(
                        flow.redirect_uri,
                        params={"state": "expected-state", "code": CODE},
                        timeout=3,
                    ).status_code
                )

        t = threading.Thread(target=callback)
        workers.append(t)
        t.start()

    auth.colab_login(ready)
    workers[0].join(3)
    assert results == [400, 200]
    assert "https://www.googleapis.com/auth/drive.file" in captured_scopes
    flow.fetch_token.assert_called_once_with(code=CODE)
    assert vault.get("colab")["token"] == "test-token"
    assert auth.flow is None


def test_private_bridge_uses_utf8_even_with_ascii_pipe_locale(tmp_path):
    import os, subprocess, sys

    root = Path(__file__).resolve().parents[1]
    script = """
import runpy,sys,types,threading
class ColabOneController:
    def __init__(self,root): self.stop=threading.Event()
    def command(self,method,payload): return payload
sys.modules['bridge.controller']=types.SimpleNamespace(ColabOneController=ColabOneController)
runpy.run_path(%r,run_name='__main__')
""" % str(root / "bridge/main.py")
    body = {"id": 1, "method": "echo", "payload": {"text": "你好，远端 GPU"}}
    result = subprocess.run(
        [sys.executable, "-c", script],
        input=(json.dumps(body, ensure_ascii=False) + "\n").encode("utf-8"),
        capture_output=True,
        env={**os.environ, "PYTHONIOENCODING": "ascii"},
        cwd=tmp_path,
        timeout=10,
        check=True,
    )
    assert json.loads(result.stdout.decode("utf-8"))["result"] == body["payload"]


def test_saved_credentials_refresh_once_across_concurrent_requests(monkeypatch):
    from bridge.auth import ColabOneAuth
    from concurrent.futures import ThreadPoolExecutor

    vault = MemoryVault()
    vault.set("colab", {"token": "expired", "refresh_token": "saved-refresh"})
    refreshed = []

    class Credential:
        def __init__(self, raw):
            self.valid = raw["token"] == "new"
            self.refresh_token = raw["refresh_token"]

        def refresh(self, _):
            refreshed.append(True)

        def to_json(self):
            return json.dumps({"token": "new", "refresh_token": self.refresh_token})

    monkeypatch.setattr("bridge.auth.Credentials.from_authorized_user_info", Credential)
    auth = ColabOneAuth(vault)
    with ThreadPoolExecutor(max_workers=4) as pool:
        list(pool.map(lambda _: auth.credentials("colab"), range(4)))
    assert refreshed == [True]
    assert vault.get("colab")["refresh_token"] == "saved-refresh"
    # A new client process restores the same persistent authorization.
    assert ColabOneAuth(vault).credentials("colab").valid


def test_refresh_network_error_preserves_saved_login(monkeypatch):
    from bridge.auth import ColabOneAuth

    vault = MemoryVault()
    raw = {"token": "expired", "refresh_token": "saved"}
    vault.set("colab", raw)
    credential = Mock(valid=False, refresh_token="saved")
    credential.refresh.side_effect = OSError("network unavailable")
    monkeypatch.setattr(
        "bridge.auth.Credentials.from_authorized_user_info", lambda _: credential
    )
    with pytest.raises(OSError):
        ColabOneAuth(vault).credentials("colab")
    assert vault.get("colab") == raw
