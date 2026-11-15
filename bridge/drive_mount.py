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
    def __init__(
        self, client, endpoint, notify, cancel=None, timeout=300, progress=None
    ):
        self.client, self.endpoint, self.notify = client, endpoint, notify
        self.cancel = cancel or threading.Event()
        self.deadline = time.monotonic() + timeout
        self.error = ""
        self.worker = None
        self.progress = progress or (lambda message: None)

    @staticmethod
    def data(response):
        if response.status_code != 200:
            raise RuntimeError(
                f"Google 云盘连接返回 HTTP {response.status_code}，请重试"
            )
        try:
            data = json.loads(
                response.text.split("\n", 1)[-1]
                if response.text.startswith(")]}'")
                else response.text
            )
            if not isinstance(data, dict):
                raise ValueError()
            return data
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
        self.progress("检查并复用此 GPU 的云盘授权")
        # A dry run can request consent even when a previous browser grant is usable.
        # Obtain a challenge, then try real propagation BEFORE opening any browser.
        if self.cancel.is_set():
            raise RuntimeError("已跳过云盘连接，可在设置中重新连接")
        if time.monotonic() >= self.deadline:
            raise RuntimeError("等待云盘确认超时，请重试")
        challenge = self.data(
            http.post(
                url,
                params=params.copy(),
                headers=headers,
                files={"file_id": (None, "empty.ipynb")},
                timeout=20,
            )
        )
        uri = challenge.get("unauthorized_redirect_uri", "")
        params["dryrun"] = "false"
        consent_opened = False
        pending_400 = False
        while True:
            if self.cancel.is_set():
                raise RuntimeError("已跳过云盘连接，可在设置中重新连接")
            if time.monotonic() >= self.deadline:
                suffix = (
                    "；请确认授权页选择的账户与 ColabOne 一致" if pending_400 else ""
                )
                raise RuntimeError(
                    "等待本台 GPU 的云盘确认超时"
                    + suffix
                    + "。Google 登录仍保存在本机，无需重新登录"
                )
            response = http.post(
                url,
                params=params.copy(),
                headers=headers,
                files={"file_id": (None, "empty.ipynb")},
                timeout=20,
            )
            # Before the user finishes consent the propagation endpoint can return
            # 400 HTML. This is not evidence that the saved OAuth login expired.
            # Keep the same challenge alive, bounded by cancellation and budget.
            if response.status_code == 400 and uri:
                pending_400 = True
                data = {}
            else:
                data = self.data(response)
            if data.get("success") is True:
                self.progress("已收到云盘授权，正在挂载到远端 GPU")
                return
            uri = uri or data.get("unauthorized_redirect_uri", "")
            parsed = urlparse(uri)
            if parsed.scheme != "https" or parsed.hostname not in (
                "accounts.google.com",
                "colab.research.google.com",
            ):
                raise RuntimeError(
                    "Google 未接受云盘连接，请在设置中重试连接；无需重新登录 Google"
                )
            if not consent_opened:
                query = parse_qs(parsed.query)
                if query.get("login_hint"):
                    query["authuser"] = query["login_hint"]
                    uri = urlunparse(
                        parsed._replace(query=urlencode(query, doseq=True))
                    )
                self.notify(uri)
                consent_opened = True
            self.cancel.wait(3)

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
