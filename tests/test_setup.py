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


def test_locale_command_persists_manual_choice_and_localizes_oauth_html(tmp_path):
    from bridge.auth import _OAUTH_CALLBACK_TEXT
    from bridge.controller import ColabOneController

    ctl = ColabOneController(
        tmp_path, vault=MemoryVault(), config_dir=tmp_path, background=False
    )
    # Non-persisted OS auto-detected sync updates runtime auth locale without writing settings.json
    res = ctl.command("locale.set", {"locale": "ja-JP", "persist": False})
    assert res == {"ok": True, "locale": "ja-JP", "persisted": False}
    assert ctl.auth.locale == "ja-JP"
    assert ctl.settings.get("locale", "") == ""

    # Manual switch persists to settings.json and survives controller restart
    res = ctl.command("locale.set", {"locale": "zh-HK", "persist": True})
    assert res == {"ok": True, "locale": "zh-HK", "persisted": True}
    assert ctl.settings["locale"] == "zh-HK"
    assert ctl.auth.locale == "zh-HK"

    reloaded = ColabOneController(
        tmp_path, vault=MemoryVault(), config_dir=tmp_path, background=False
    )
    assert reloaded.settings["locale"] == "zh-HK"
    assert reloaded.auth.locale == "zh-HK"

    with pytest.raises(ValueError):
        ctl.command("locale.set", {"locale": "xx-XX", "persist": True})

    # Verify localized OAuth callback texts exist for all 6 supported locales
    expected_snippets = {
        "zh-CN": ("登录成功", "重试"),
        "en-US": ("Signed in to ColabOne", "try again"),
        "zh-TW": ("返回用戶端", "重試"),
        "zh-HK": ("返回應用程式", "再試"),
        "ja-JP": ("ログインが完了しました", "再試行"),
        "ko-KR": ("로그인을 완료했습니다", "다시 시도"),
    }
    assert set(_OAUTH_CALLBACK_TEXT.keys()) == set(expected_snippets.keys())
    for loc, (ok_snippet, err_snippet) in expected_snippets.items():
        ok_msg, err_msg = _OAUTH_CALLBACK_TEXT[loc]
        assert ok_snippet in ok_msg
        assert err_snippet in err_msg


def test_shortcuts_description_localized_across_locales(tmp_path, monkeypatch):
    from bridge.shortcuts import (
        COLABONE_APP_DESCRIPTIONS,
        colabone_app_description,
        manage_shortcuts,
    )

    assert set(COLABONE_APP_DESCRIPTIONS.keys()) == {
        "zh-CN",
        "en-US",
        "zh-TW",
        "zh-HK",
        "ja-JP",
        "ko-KR",
    }
    assert colabone_app_description("en-US") == "ColabOne — Remote GPU Workspace"
    assert colabone_app_description("ja-JP") == "ColabOne — リモート GPU ワークスペース"
    assert colabone_app_description("ko-KR") == "ColabOne — 원격 GPU 워크스페이스"

    # Verify Linux .desktop entry uses the requested locale description
    monkeypatch.setattr("pathlib.Path.home", lambda: tmp_path)
    root = tmp_path / "app"
    root.mkdir()
    (root / "ColabOne").write_text("#!/bin/sh\n", encoding="utf-8")
    manage_shortcuts(
        root, target="desktop", platform_name="linux", locale="en-US"
    )
    desktop_file = tmp_path / "Desktop" / "ColabOne.desktop"
    content = desktop_file.read_text(encoding="utf-8")
    assert "Comment=ColabOne — Remote GPU Workspace" in content


def test_comfy_proxy_401_html_respects_accept_language():
    import socket
    from server.comfy_proxy import (
        ColabOneCanvasProxyServer,
        _UNAUTHORIZED_HTML_TEXT,
        _resolve_accept_language,
    )

    assert _resolve_accept_language("") == "en-US"
    assert _resolve_accept_language("fr-FR,fr;q=0.9") == "en-US"
    assert _resolve_accept_language("en-US,en;q=0.9,zh-CN;q=0.8") == "en-US"
    assert _resolve_accept_language("zh-TW,zh;q=0.9,en;q=0.8") == "zh-TW"
    assert _resolve_accept_language("zh-HK,zh-Hant;q=0.9") == "zh-HK"
    assert _resolve_accept_language("zh-CN,zh;q=0.9") == "zh-CN"
    assert _resolve_accept_language("ja-JP,ja;q=0.9") == "ja-JP"
    assert _resolve_accept_language("ko-KR,ko;q=0.9") == "ko-KR"
    assert set(_UNAUTHORIZED_HTML_TEXT.keys()) == {
        "zh-CN",
        "en-US",
        "zh-TW",
        "zh-HK",
        "ja-JP",
        "ko-KR",
    }

    # Verify live proxy 401 HTML output follows Accept-Language
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]

    proxy = ColabOneCanvasProxyServer(
        listen_port=port,
        upstream_port=1,
        canvas_token="tok-test",
    )
    proxy.start()
    try:
        for header, expected_lang, expected_text in [
            ("en-US,en;q=0.9", "en-US", "Unauthorized: please open ComfyUI Native Canvas"),
            ("ja-JP,ja;q=0.9", "ja-JP", "ColabOne アプリから"),
            ("zh-TW,zh;q=0.9", "zh-TW", "ColabOne 用戶端"),
            ("", "en-US", "Unauthorized: please open ComfyUI Native Canvas"),
        ]:
            with socket.create_connection(("127.0.0.1", port), timeout=5) as conn:
                req = (
                    "GET / HTTP/1.1\r\n"
                    f"Host: 127.0.0.1:{port}\r\n"
                    "Accept: text/html\r\n"
                    + (f"Accept-Language: {header}\r\n" if header else "")
                    + "Connection: close\r\n\r\n"
                )
                conn.sendall(req.encode("utf-8"))
                chunks = []
                while True:
                    buf = conn.recv(4096)
                    if not buf:
                        break
                    chunks.append(buf)
            resp = b"".join(chunks).decode("utf-8", errors="replace")
            assert "401 Unauthorized" in resp
            assert f"lang='{expected_lang}'" in resp
            assert expected_text in resp
    finally:
        proxy.stop()


def test_frontend_locations_cover_all_python_countries_and_subregions():
    import re
    from pathlib import Path
    from common.location import COUNTRY_NAMES_ZH, GCP_REGIONS, REGION_NAMES_ZH

    root = Path(__file__).resolve().parents[1]
    loc_ts = (root / "frontend" / "src" / "i18n" / "locations.ts").read_text(
        encoding="utf-8"
    )

    all_countries = set(COUNTRY_NAMES_ZH.keys()) | {
        code for code, _, _ in GCP_REGIONS.values()
    }
    for locale in ("zh-CN", "en-US", "zh-TW", "zh-HK", "ja-JP", "ko-KR"):
        m = re.search(rf'"{locale}":\s*\{{([^}}]+)\}}', loc_ts)
        assert m, f"Missing COUNTRY_OVERRIDES block for {locale}"
        present = set(re.findall(r"\b([A-Z]{2}):", m.group(1)))
        missing = sorted(all_countries - present)
        assert not missing, f"Missing country translations in {locale}: {missing}"

    all_subregions = {
        sub for _, _, sub in GCP_REGIONS.values() if sub
    } | set(REGION_NAMES_ZH.values())
    sub_block = loc_ts.split("SUBREGION_TRANSLATIONS")[1].split(
        "const ZH_COUNTRY_TO_CODE"
    )[0]
    missing_subs = [
        sub
        for sub in sorted(all_subregions)
        if not re.search(
            rf'(?:^|\s)"?{re.escape(sub)}"?\s*:', sub_block, re.MULTILINE
        )
    ]
    assert not missing_subs, (
        f"Missing subregion translations in locations.ts: {missing_subs}"
    )


