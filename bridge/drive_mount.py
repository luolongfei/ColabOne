# Copyright 2026 Google LLC
# Licensed under the Apache License, Version 2.0 (see LICENSE).
# Credential propagation adapted from google-colab-cli 0.7.5 automation.py.
# ColabOne changes: asynchronous handshake, automatic consent polling, cancellation,
# safe errors, bounded waits and no terminal input or credential logs.
from __future__ import annotations
import json
import threading
import time
from urllib.parse import urlparse, parse_qs, urlencode, urlunparse


class DriveMount:
    def __init__(self, client, endpoint, notify, cancel=None, timeout=300):
        self.client, self.endpoint, self.notify = client, endpoint, notify
        self.cancel = cancel or threading.Event()
        self.deadline = time.monotonic() + timeout
        self.error = ""
        self.worker = None

    @staticmethod
    def data(response):
        if response.status_code != 200:
            raise RuntimeError(
                f"Google 云盘连接返回 HTTP {response.status_code}，请重试"
            )
        try:
            return json.loads(
                response.text.split("\n", 1)[-1]
                if response.text.startswith(")]}'")
                else response.text
            )
        except ValueError:
            raise RuntimeError("Google 云盘连接返回无效结果，请重试") from None

    def propagate(self):
        url = f"{self.client.colab_domain.rstrip('/')}/tun/m/credentials-propagation/{self.endpoint}"
        params = {
            "authuser": "0",
            "authtype": "dfs_ephemeral",
            "version": "2",
            "dryrun": "true",
            "propagate": "true",
            "record": "false",
        }
        http = self.client.session
        token = self.data(http.get(url, params=params, timeout=20)).get("token")
        if not token:
            raise RuntimeError("未取得 Colab 云盘连接许可，请重新登录")
        headers = {"x-goog-colab-token": token}
        consent_opened = False
        while True:
            if self.cancel.is_set():
                raise RuntimeError("已跳过云盘连接，可在设置中重新连接")
            if time.monotonic() >= self.deadline:
                raise RuntimeError("等待云盘授权超时，可在设置中重新连接")
            data = self.data(
                http.post(
                    url,
                    params=params,
                    headers=headers,
                    files={"file_id": (None, "empty.ipynb")},
                    timeout=20,
                )
            )
            if data.get("success"):
                break
            uri = data.get("unauthorized_redirect_uri", "")
            parsed = urlparse(uri)
            if parsed.scheme != "https" or parsed.hostname not in (
                "accounts.google.com",
                "colab.research.google.com",
            ):
                raise RuntimeError("Google 没有返回有效的云盘授权页，请重试")
            # Google's returned login_hint identifies the same account as Colab.
            # Also pass it as authuser to avoid the browser's default-account mismatch.
            query = parse_qs(parsed.query)
            if query.get("login_hint"):
                query["authuser"] = query["login_hint"]
                uri = urlunparse(parsed._replace(query=urlencode(query, doseq=True)))
            if not consent_opened:
                self.notify(uri)
                consent_opened = True
            self.cancel.wait(3)
        params["dryrun"] = "false"
        result = self.data(
            http.post(
                url,
                params=params,
                headers=headers,
                files={"file_id": (None, "empty.ipynb")},
                timeout=20,
            )
        )
        if result.get("success") is False:
            raise RuntimeError(
                "云盘授权尚未生效，请确认浏览器中选择了同一个 Google 账户"
            )

    def hook(self, message, wsclient):
        if (
            message.get("content", {}).get("request", {}).get("authType")
            != "dfs_ephemeral"
        ):
            return False
        if self.worker:
            return True

        def run():
            value = {
                "type": "colab_reply",
                "colab_msg_id": message.get("metadata", {}).get("colab_msg_id"),
            }
            try:
                self.propagate()
            except Exception as exc:
                self.error = (
                    str(exc)
                    if isinstance(exc, RuntimeError)
                    else "云盘连接中断，请检查网络后重试"
                )
                value["error"] = self.error
            reply = wsclient.session.msg("input_reply", {"value": value})
            if "header" in message:
                reply["parent_header"] = message["header"]
            try:
                wsclient.stdin_channel.send(reply)
            except Exception:
                self.error = self.error or "Colab 控制连接已断开，请重试连接云盘"

        self.worker = threading.Thread(target=run, daemon=True)
        self.worker.start()
        return True
