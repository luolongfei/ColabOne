"""Credentials stay in the operating system credential store, never in repo files."""

from __future__ import annotations
import json
import threading
from importlib import resources

import keyring
from google.oauth2.credentials import Credentials
from google.auth.transport.requests import Request
from google_auth_oauthlib.flow import InstalledAppFlow
from colab_cli.auth import PUBLIC_SCOPES, REMOTE_REDIRECT_URI


class Vault:
    def __init__(self):
        self.lock = threading.RLock()

    def get(self, name, default=None):
        with self.lock:
            raw = keyring.get_password("ColabOne", name)
            return json.loads(raw) if raw else default

    def set(self, name, value):
        with self.lock:
            keyring.set_password("ColabOne", name, json.dumps(value))

    def delete(self, name):
        with self.lock:
            try:
                keyring.delete_password("ColabOne", name)
            except keyring.errors.PasswordDeleteError:
                pass


class Auth:
    def __init__(self, vault):
        self.vault = vault
        self.flow = None
        self.lock = threading.RLock()

    def credentials(self, name):
        raw = self.vault.get(name)
        if not raw:
            raise RuntimeError(
                "请先完成 " + ("Colab" if name == "colab" else "Google Drive") + " 登录"
            )
        creds = Credentials.from_authorized_user_info(raw)
        if not creds.valid:
            if not creds.refresh_token:
                raise RuntimeError("授权已失效，请重新登录")
            creds.refresh(Request())
            self.vault.set(name, json.loads(creds.to_json()))
        return creds

    def colab_begin(self):
        with self.lock:
            cfg = json.loads(
                resources.files("colab_cli").joinpath("oauth_config.json").read_text()
            )
            self.flow = InstalledAppFlow.from_client_config(
                cfg, PUBLIC_SCOPES, autogenerate_code_verifier=True
            )
            self.flow.redirect_uri = REMOTE_REDIRECT_URI
            url, _ = self.flow.authorization_url(
                prompt="select_account consent", token_usage="remote"
            )
            return {"url": url}

    def colab_finish(self, code):
        with self.lock:
            if not self.flow:
                raise RuntimeError("请先打开登录页面")
            if self.vault.get("runtime_session"):
                raise RuntimeError("切换账号前请先释放当前实例")
            self.flow.fetch_token(code=code.strip())
            self.vault.set("colab", json.loads(self.flow.credentials.to_json()))
            self.flow = None
            return {"ok": True}

    def drive_login(self, config, ready_callback):
        cfg = json.loads(config)
        if "installed" not in cfg:
            raise ValueError("请使用 Google Cloud 创建的 Desktop app OAuth 客户端 JSON")
        # Desktop public client with loopback redirect and PKCE. The local callback
        # remains inside google-auth-oauthlib, which verifies the OAuth state.
        flow = InstalledAppFlow.from_client_config(
            cfg,
            ["https://www.googleapis.com/auth/drive.appdata"],
            autogenerate_code_verifier=True,
        )

        # run_local_server prints the URL. Capture it through its supported message hook.
        class AuthURL(str):
            def format(self, **kwargs):
                ready_callback(kwargs["url"])
                return "Google Drive authorization opened."

        creds = flow.run_local_server(
            host="127.0.0.1",
            port=0,
            open_browser=False,
            timeout_seconds=180,
            authorization_prompt_message=AuthURL("{url}"),
            success_message="ColabOne 已获得授权，可以关闭此窗口。",
            access_type="offline",
            prompt="consent",
        )
        self.vault.set("drive", json.loads(creds.to_json()))
        return {"ok": True}
