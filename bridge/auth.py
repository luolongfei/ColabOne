"""Credentials stay in the operating system credential store, never in repo files."""

from __future__ import annotations
import json
import threading
import hmac
import re
import time
import requests
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse, parse_qs, unquote
from importlib import resources

import keyring
from google.oauth2.credentials import Credentials
from google.auth.transport.requests import Request
from google_auth_oauthlib.flow import InstalledAppFlow
from colab_cli.auth import PUBLIC_SCOPES, REMOTE_REDIRECT_URI


def parse_authorization_reply(text):
    text = text.strip()
    if text.startswith(("https://", "http://")):
        query = parse_qs(urlparse(text).query)
        if query.get("code"):
            text = query["code"][0]
    matches = list(
        dict.fromkeys(re.findall(r"4/[A-Za-z0-9_./+\-]{20,}", unquote(text)))
    )
    if len(matches) != 1:
        raise ValueError(
            "未识别到唯一的 Google 授权码。请粘贴授权页全部文字、回调链接或授权码。"
        )
    return matches[0]


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
        self.login_event = threading.Event()

    def profile(self):
        profile = self.vault.get("google_profile")
        if profile is None:
            profile = self.fetch_profile(self.credentials("colab"))
            self.vault.set("google_profile", profile)
        return profile

    @staticmethod
    def fetch_profile(credentials):
        response = requests.get(
            "https://www.googleapis.com/oauth2/v3/userinfo",
            headers={"Authorization": "Bearer " + credentials.token},
            timeout=20,
        )
        response.raise_for_status()
        data = response.json()
        return {"email": data.get("email", ""), "sub": data["sub"]}

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
            if self.vault.get("runtime_session"):
                raise RuntimeError("切换账号前请先释放当前实例")
            self.login_event.clear()
            cfg = json.loads(
                resources.files("colab_cli")
                .joinpath("oauth_config.json")
                .read_text(encoding="utf-8")
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
            parsed = parse_authorization_reply(code)
            try:
                self.flow.fetch_token(code=parsed)
            except Exception:
                raise RuntimeError(
                    "授权回显已失效或不属于本次登录，请重新打开登录页"
                ) from None
            profile = self.fetch_profile(self.flow.credentials)
            previous = self.vault.get("google_profile") or {}
            if profile.get("sub") != previous.get("sub"):
                self.vault.delete("cloud_project")
                self.vault.delete("drive")
            self.vault.set("google_profile", profile)
            self.vault.set("colab", json.loads(self.flow.credentials.to_json()))
            self.flow = None
            self.login_event.set()
            return {"ok": True}

    def colab_login(self, ready_callback):
        if self.vault.get("runtime_session"):
            raise RuntimeError("切换账号前请先释放当前实例")
        auth = self
        state = ""
        errors = []

        class Callback(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass  # Callback URLs contain an authorization code.

            def do_GET(self):
                params = parse_qs(urlparse(self.path).query)
                if not hmac.compare_digest(params.get("state", [""])[0], state):
                    self.send_error(400, "Invalid OAuth state")
                    return
                try:
                    if params.get("error"):
                        raise RuntimeError("Google 授权已取消，请重新登录")
                    auth.colab_finish(params.get("code", [""])[0])
                    message = "ColabOne 登录成功。你可以关闭此页面，返回客户端。"
                except Exception:
                    errors.append("登录未完成，请返回 ColabOne 重试")
                    auth.login_event.set()
                    message = errors[-1]
                content = (
                    '<!doctype html><meta charset="utf-8"><title>ColabOne</title><body style="font:18px system-ui;padding:64px;color:#22634b"><h1>ColabOne</h1><p>'
                    + message
                    + "</p></body>"
                ).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.send_header("Content-Length", str(len(content)))
                self.end_headers()
                self.wfile.write(content)

        with HTTPServer(("127.0.0.1", 0), Callback) as server:
            server.timeout = 0.5
            cfg = json.loads(
                resources.files("colab_cli")
                .joinpath("oauth_config.json")
                .read_text(encoding="utf-8")
            )
            with self.lock:
                self.login_event.clear()
                self.flow = InstalledAppFlow.from_client_config(
                    cfg, PUBLIC_SCOPES, autogenerate_code_verifier=True
                )
                self.flow.redirect_uri = f"http://localhost:{server.server_port}/"
                url, state = self.flow.authorization_url(
                    prompt="select_account consent", access_type="offline"
                )
            ready_callback(url)
            deadline = time.monotonic() + 180
            while not self.login_event.is_set() and time.monotonic() < deadline:
                server.handle_request()
            if not self.login_event.is_set():
                raise RuntimeError(
                    "等待登录超时。可点击“使用授权回执”，粘贴浏览器回显自动完成。"
                )
            if errors:
                raise RuntimeError(errors[0])
