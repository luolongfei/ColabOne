"""Official Google Colab native DriveFS credentials propagation (`dfs_ephemeral`).

Used as the zero-GCP-config automatic fallback whenever headless rclone cannot
obtain a Drive-enabled GCP quota project (e.g., personal accounts that have not
accepted GCP Terms of Service or Workspace orgs restricting project creation).
"""

from __future__ import annotations

import json
import threading
import time
from urllib.parse import urlparse

COLABONE_DRIVEFS_MOUNT_ROOT = "/content/drive"
COLABONE_DRIVEFS_MYDRIVE = "/content/drive/MyDrive"


def _emit_log(on_log, message: str, source: str = "云盘", level: str = "info") -> None:
    if not callable(on_log) or not message:
        return
    try:
        on_log(message, source, level)
    except TypeError:
        on_log(message)


def _parse_colab_json(response) -> dict:
    status = getattr(response, "status_code", 0)
    text = str(getattr(response, "text", "") or "")
    body = text.split("\n", 1)[-1] if text.startswith(")]}'") else text
    try:
        parsed = json.loads(body)
    except Exception:
        raise RuntimeError(
            f"Google Colab 云盘授权接口返回异常状态（HTTP {status}），请重试"
        ) from None
    if not isinstance(parsed, dict):
        raise RuntimeError("Google Colab 云盘授权接口返回格式异常，请重试")
    return parsed


def _validate_consent_url(raw_url: str) -> str:
    uri = str(raw_url or "").strip()
    if not uri:
        return ""
    parsed = urlparse(uri)
    if parsed.scheme != "https" or (parsed.hostname or "").lower() != "accounts.google.com":
        raise RuntimeError("Google Colab 返回了未受信任的云盘授权地址，已中止")
    return uri


class ColabOneDriveMount:
    """Handles `dfs_ephemeral` credentials propagation for `google.colab.drive.mount`."""

    def __init__(
        self,
        client,
        endpoint: str,
        on_auth_url=None,
        on_log=None,
        cancel_event: threading.Event | None = None,
        timeout: float = 180.0,
        poll_interval: float = 2.0,
    ):
        self.client = client
        self.endpoint = str(endpoint)
        self.on_auth_url = on_auth_url
        self.on_log = on_log
        self.cancel_event = cancel_event or threading.Event()
        self.timeout = max(15.0, float(timeout))
        self.poll_interval = max(0.2, float(poll_interval))
        self.error: str = ""

    def _endpoint_url(self) -> str:
        domain = str(getattr(self.client, "colab_domain", "https://colab.research.google.com"))
        return f"{domain.rstrip('/')}/tun/m/credentials-propagation/{self.endpoint}"

    @staticmethod
    def _base_params(dryrun: bool) -> dict[str, str]:
        return {
            "authuser": "0",
            "authtype": "dfs_ephemeral",
            "version": "2",
            "dryrun": "true" if dryrun else "false",
            "propagate": "true",
            "record": "false",
        }

    def _fetch_xsrf_token(self, dryrun: bool = True) -> str:
        resp = self.client.session.get(
            self._endpoint_url(),
            params=self._base_params(dryrun),
            timeout=15,
        )
        if resp.status_code != 200:
            raise RuntimeError(
                f"未取得 Google Colab 云盘连接许可（HTTP {resp.status_code}），请重试"
            )
        data = _parse_colab_json(resp)
        token = str(data.get("token") or "")
        if not token:
            raise RuntimeError("Google Colab 云盘授权握手缺少令牌，请重试")
        return token

    def _post_propagation(self, token: str, dryrun: bool):
        return self.client.session.post(
            self._endpoint_url(),
            params=self._base_params(dryrun),
            headers={"x-goog-colab-token": token},
            files={"file_id": (None, "empty.ipynb")},
            timeout=15,
        )

    def dryrun_check(self) -> tuple[bool, str]:
        """Fast read-only check: returns `(authorized, consent_url)` for this VM."""
        token = self._fetch_xsrf_token(dryrun=True)
        resp = self._post_propagation(token, dryrun=True)
        if resp.status_code != 200:
            raise RuntimeError(
                f"检查 Google 云盘授权状态失败（HTTP {resp.status_code}），请重试"
            )
        data = _parse_colab_json(resp)
        if data.get("success"):
            return True, ""
        uri = _validate_consent_url(data.get("unauthorized_redirect_uri", ""))
        return False, uri

    def propagate(self) -> bool:
        """Ensure Colab DriveFS authorization for this VM and propagate credentials."""
        deadline = time.monotonic() + self.timeout
        consent_opened = False
        token = ""
        transient_errors = 0

        try:
            while True:
                if self.cancel_event.is_set():
                    raise RuntimeError("已取消 Google 云盘挂载")
                if time.monotonic() >= deadline:
                    raise RuntimeError(
                        "等待浏览器确认 Google 云盘授权超时，请重试；本机 Google 登录仍保留，无需重新登录"
                    )

                if not token:
                    token = self._fetch_xsrf_token(dryrun=True)

                resp = self._post_propagation(token, dryrun=True)
                if resp.status_code in (400, 403, 500, 502, 503, 504):
                    transient_errors += 1
                    token = ""
                    if transient_errors > 8:
                        raise RuntimeError(
                            f"Google 云盘授权确认失败（HTTP {resp.status_code}），请重试"
                        )
                    if self.cancel_event.wait(self.poll_interval):
                        raise RuntimeError("已取消 Google 云盘挂载")
                    continue

                transient_errors = 0
                if resp.status_code != 200:
                    raise RuntimeError(
                        f"Google 云盘连接返回 HTTP {resp.status_code}，请重试"
                    )

                data = _parse_colab_json(resp)
                if data.get("success"):
                    break

                uri = _validate_consent_url(data.get("unauthorized_redirect_uri", ""))
                if not uri:
                    raise RuntimeError(
                        "Google 未接受本次云盘授权请求，请稍后重试；无需重新登录 Google"
                    )
                if not consent_opened:
                    consent_opened = True
                    _emit_log(
                        self.on_log,
                        "请在浏览器中点击允许，以确认当前实例挂载 Google 云盘（仅本实例确认一次）",
                        "云盘",
                    )
                    if callable(self.on_auth_url):
                        self.on_auth_url(uri)

                if self.cancel_event.wait(self.poll_interval):
                    raise RuntimeError("已取消 Google 云盘挂载")

            # Authorized — perform actual credentials propagation into the VM.
            _emit_log(self.on_log, "已收到云盘授权，正在将凭据同步到远端 GPU 实例", "云盘")
            for attempt in range(2):
                if not token:
                    token = self._fetch_xsrf_token(dryrun=False)
                resp = self._post_propagation(token, dryrun=False)
                if resp.status_code == 200:
                    res_data = _parse_colab_json(resp)
                    if res_data.get("success") is False:
                        if attempt == 0:
                            token = ""
                            continue
                        raise RuntimeError(
                            "Google 尚未确认本次云盘授权，请在浏览器点击允许后重试"
                        )
                    _emit_log(
                        self.on_log,
                        "云盘凭据已同步至远端实例，正在完成 DriveFS 挂载",
                        "云盘",
                    )
                    return True
                if resp.status_code in (400, 403) and attempt == 0:
                    token = ""
                    continue
                raise RuntimeError(
                    f"向远端实例同步云盘凭据失败（HTTP {resp.status_code}），请重试"
                )
            return False
        finally:
            if consent_opened and callable(self.on_auth_url):
                try:
                    self.on_auth_url("")
                except Exception:
                    pass

    def hook(self, deserialize_msg: dict, wsclient) -> bool:
        """ColabRuntime `colab_request_hook` callback.

        Runs propagation in a daemon thread so the Jupyter WebSocket receiver
        thread stays responsive to ping/pong and IOPub frames while awaiting
        browser confirmation.
        """
        content = deserialize_msg.get("content") or {}
        if content.get("request", {}).get("authType") != "dfs_ephemeral":
            return False

        msg_id = (deserialize_msg.get("metadata") or {}).get("colab_msg_id")

        def _send_reply(error: str = ""):
            payload = {"type": "colab_reply", "colab_msg_id": msg_id}
            if error:
                payload["error"] = error
            reply = wsclient.session.msg("input_reply", {"value": payload})
            if "header" in deserialize_msg:
                reply["parent_header"] = deserialize_msg["header"]
            try:
                wsclient.stdin_channel.send(reply)
            except Exception:
                pass

        def _worker():
            try:
                self.propagate()
            except Exception as exc:
                self.error = str(exc)
                _send_reply(error=self.error)
                return
            _send_reply()

        threading.Thread(target=_worker, daemon=True).start()
        return True
