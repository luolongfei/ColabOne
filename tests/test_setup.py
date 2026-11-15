import json
import threading
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock
from urllib.parse import quote
import pytest
from bridge.auth import parse_authorization_reply
from bridge.drive_mount import DriveMount
from common.storage import DriveStore
from conftest import MemoryVault
from bridge.controller import Controller

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


def response(data):
    return SimpleNamespace(status_code=200, text=")]}'\n" + json.dumps(data))


def test_drive_consent_is_async_and_automatically_resumes():
    client = Mock(colab_domain="https://colab.research.google.com")
    client.session.get.return_value = response({"token": "test-token"})
    client.session.post.side_effect = [
        response(
            {
                "unauthorized_redirect_uri": "https://accounts.google.com/o/oauth2/auth?login_hint=user%40example.com"
            }
        ),
        response(
            {"unauthorized_redirect_uri": "https://accounts.google.com/changed-state"}
        ),
        response({"success": True}),
    ]
    cancel = threading.Event()
    cancel.wait = Mock(return_value=False)
    notify = Mock()
    mount = DriveMount(client, "owned-runtime", notify, cancel)
    ws = Mock()
    ws.session.msg.side_effect = lambda kind, data: {"kind": kind, "content": data}
    msg = {
        "content": {"request": {"authType": "dfs_ephemeral"}},
        "metadata": {"colab_msg_id": 12},
        "header": {"session": "owned"},
    }
    assert mount.hook(msg, ws)
    mount.worker.join(2)
    assert not mount.worker.is_alive()
    notify.assert_called_once()
    assert "authuser=user%40example.com" in notify.call_args.args[0]
    reply = ws.stdin_channel.send.call_args.args[0]
    assert reply["content"]["value"] == {"type": "colab_reply", "colab_msg_id": 12}
    assert [
        call.kwargs["params"]["dryrun"] for call in client.session.post.call_args_list
    ] == ["true", "false", "false"]


def test_drive_cancel_replies_to_kernel_without_hanging():
    client = Mock(colab_domain="https://colab.research.google.com")
    client.session.get.return_value = response({"token": "test-token"})
    event = threading.Event()
    event.set()
    mount = DriveMount(client, "owned", Mock(), event)
    ws = Mock()
    ws.session.msg.side_effect = lambda kind, data: data
    mount.hook(
        {
            "content": {"request": {"authType": "dfs_ephemeral"}},
            "metadata": {"colab_msg_id": 2},
        },
        ws,
    )
    mount.worker.join(2)
    assert "error" in ws.stdin_channel.send.call_args.args[0]["value"]
    client.session.post.assert_not_called()


def test_drive_mount_never_opens_untrusted_redirect():
    client = Mock(colab_domain="https://colab.research.google.com")
    client.session.get.return_value = response({"token": "test-token"})
    client.session.post.return_value = response(
        {"unauthorized_redirect_uri": "https://attacker.invalid/"}
    )
    notify = Mock()
    with pytest.raises(RuntimeError, match="有效"):
        DriveMount(client, "owned", notify).propagate()
    notify.assert_not_called()


def test_unmounted_drive_never_creates_local_backup_directory(tmp_path):
    store = DriveStore(tmp_path / "drive", mounted=lambda: False)
    with pytest.raises(RuntimeError):
        store.put("models.json", [])
    assert not (tmp_path / "drive").exists()


def test_mounted_drive_roundtrip_unicode_and_safe_paths(tmp_path):
    (tmp_path / "MyDrive").mkdir()
    flushed = []
    store = DriveStore(
        tmp_path, mounted=lambda: True, flush=lambda: flushed.append(True)
    )
    data = {"id": "demo", "updated_at": 1, "messages": [{"content": "你好"}]}
    store.put("conversation-demo.json", data)
    assert store.conversations() == [data]
    assert store.get("models.json", []) == []
    with pytest.raises(ValueError):
        store.put("../../other.json", {})
    store.flush()
    assert flushed == [True]


def test_gpu_start_does_not_require_drive_or_cloud_project(tmp_path):
    c = Controller(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.auth = Mock()
    c.provider = Mock()
    c.remote = Mock()
    c.refresh_remote = Mock()
    c.settings["auto_drive"] = False
    c.start()
    c.provider.start.assert_called_once()
    c.provider.mount_drive.assert_not_called()
    assert c.remote.call_args_list[0].args[:2] == ("POST", "configure")


def test_google_loopback_checks_state_and_stores_login(monkeypatch):
    import requests
    from bridge.auth import Auth

    vault = MemoryVault()
    auth = Auth(vault)
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
    monkeypatch.setattr(
        "bridge.auth.InstalledAppFlow.from_client_config", lambda *_a, **_k: flow
    )
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
    flow.fetch_token.assert_called_once_with(code=CODE)
    assert vault.get("colab")["token"] == "test-token"
    assert auth.flow is None


def test_drive_disconnect_cannot_falsely_confirm_pending_uploads(tmp_path):
    (tmp_path / "MyDrive").mkdir()
    mounted = [True]
    store = DriveStore(tmp_path, mounted=lambda: mounted[0], flush=lambda: None)
    store.put("models.json", [])
    mounted[0] = False
    with pytest.raises(RuntimeError, match="无法确认"):
        store.flush()
    assert store.pending_flush
    mounted[0] = True
    store.flush()
    assert not store.pending_flush


def test_private_bridge_uses_utf8_even_with_ascii_pipe_locale(tmp_path):
    import os, subprocess, sys

    root = Path(__file__).resolve().parents[1]
    script = """
import runpy,sys,types,threading
class Controller:
    def __init__(self,root): self.stop=threading.Event()
    def command(self,method,payload): return payload
sys.modules['bridge.controller']=types.SimpleNamespace(Controller=Controller)
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


def test_drive_success_requires_real_propagation_and_explicit_success():
    client = Mock(colab_domain="https://colab.research.google.com")
    client.session.get.return_value = response({"token": "test"})
    client.session.post.side_effect = [response({"success": True}), response({})]
    with pytest.raises(RuntimeError):
        DriveMount(client, "owned", Mock()).propagate()
    assert [
        call.kwargs["params"]["dryrun"] for call in client.session.post.call_args_list
    ] == ["true", "false"]


def test_saved_credentials_refresh_once_across_concurrent_requests(monkeypatch):
    from bridge.auth import Auth
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
    auth = Auth(vault)
    with ThreadPoolExecutor(max_workers=4) as pool:
        list(pool.map(lambda _: auth.credentials("colab"), range(4)))
    assert refreshed == [True]
    assert vault.get("colab")["refresh_token"] == "saved-refresh"
    # A new client process restores the same persistent authorization.
    assert Auth(vault).credentials("colab").valid


def test_refresh_network_error_preserves_saved_login(monkeypatch):
    from bridge.auth import Auth

    vault = MemoryVault()
    raw = {"token": "expired", "refresh_token": "saved"}
    vault.set("colab", raw)
    credential = Mock(valid=False, refresh_token="saved")
    credential.refresh.side_effect = OSError("network unavailable")
    monkeypatch.setattr(
        "bridge.auth.Credentials.from_authorized_user_info", lambda _: credential
    )
    with pytest.raises(OSError):
        Auth(vault).credentials("colab")
    assert vault.get("colab") == raw
