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

import requests
from google.auth.exceptions import TransportError

COLABONE_DRIVEFS_MOUNT_ROOT = "/content/drive"
COLABONE_DRIVEFS_MYDRIVE = "/content/drive/MyDrive"

_COLAB_REQUEST_HEADERS = {
    "Accept": "application/json",
    "X-Colab-Client-Agent": "colab-cli",
}


def _emit_log(on_log, message: str, source: str = "云盘", level: str = "info") -> None:
    if not callable(on_log) or not message:
        return
    try:
        on_log(message, source, level)
    except TypeError:
        on_log(message)


def _parse_colab_json_lenient(response) -> dict:
    """Parse XSSI-prefixed or plain JSON defensively; return `{}` on HTML/empty bodies."""
    text = str(getattr(response, "text", "") or "")
    for candidate in (text, text.split("\n", 1)[-1], text.rsplit("\n", 1)[-1]):
        stripped = candidate.strip()
        if not stripped:
            continue
        try:
            parsed = json.loads(stripped)
        except Exception:
            continue
        if isinstance(parsed, dict):
            return parsed
    return {}


def _parse_colab_json(response) -> dict:
    status = getattr(response, "status_code", 0)
    parsed = _parse_colab_json_lenient(response)
    if not parsed:
        raise RuntimeError(
            f"Google Colab 云盘授权接口返回异常状态（HTTP {status}），请重试"
        )
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
        confirm_event: threading.Event | None = None,
        probe_event: threading.Event | None = None,
        on_hint=None,
        account_email: str = "",
        timeout: float = 240.0,
        poll_interval: float = 2.5,
    ):
        self.client = client
        self.endpoint = str(endpoint)
        self.on_auth_url = on_auth_url
        self.on_log = on_log
        self.cancel_event = cancel_event or threading.Event()
        self.confirm_event = confirm_event or threading.Event()
        self.probe_event = probe_event or threading.Event()
        self.on_hint = on_hint
        self.account_email = str(account_email or "").strip()
        self.timeout = max(15.0, float(timeout))
        self.poll_interval = max(0.05, float(poll_interval))
        self._stop_event = threading.Event()
        self._hook_lock = threading.Lock()
        self._hook_started = False
        self.error: str = ""

    def stop(self) -> None:
        self._stop_event.set()

    def _is_aborted(self) -> bool:
        return self.cancel_event.is_set() or self._stop_event.is_set()

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

    def _emit_hint(self, text: str) -> None:
        if not callable(self.on_hint):
            return
        try:
            self.on_hint(text)
        except Exception:
            pass

    def _not_confirmed_hint(self) -> str:
        if self.account_email:
            return (
                f"暂未检测到 {self.account_email} 的云盘授权确认：请确认浏览器中选择了同一 Google 账号，"
                "并在授权页勾选全部云盘权限后重试"
            )
        return (
            "暂未检测到 Google 云盘授权确认：请确认浏览器中选择了与 ColabOne 相同的 Google 账号，"
            "并在授权页勾选全部云盘权限后重试"
        )

    def _timeout_message(self) -> str:
        if self.account_email:
            return (
                f"等待浏览器确认 {self.account_email} 的 Google 云盘授权超时。"
                "请确认浏览器中登录了同一 Google 账号并在授权页勾选全部云盘权限后重试；本机登录仍保留"
            )
        return (
            "等待浏览器确认 Google 云盘授权超时。"
            "请确认浏览器中选择了与 ColabOne 相同的 Google 账号并勾选全部云盘权限后重试；本机登录仍保留"
        )

    def _fetch_xsrf_token(self, dryrun: bool = True) -> str:
        resp = self.client.session.get(
            self._endpoint_url(),
            params=self._base_params(dryrun),
            headers=dict(_COLAB_REQUEST_HEADERS),
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
        headers = dict(_COLAB_REQUEST_HEADERS)
        headers["x-goog-colab-token"] = token
        return self.client.session.post(
            self._endpoint_url(),
            params=self._base_params(dryrun),
            headers=headers,
            files={"file_id": (None, "empty.ipynb")},
            timeout=15,
        )

    def _request_propagation(self, dryrun: bool):
        """Fetch a fresh XSRF token and POST credentials-propagation once."""
        get_resp = self.client.session.get(
            self._endpoint_url(),
            params=self._base_params(dryrun),
            headers=dict(_COLAB_REQUEST_HEADERS),
            timeout=15,
        )
        if get_resp.status_code != 200:
            return get_resp.status_code, _parse_colab_json_lenient(get_resp)
        get_data = _parse_colab_json_lenient(get_resp)
        token = str(get_data.get("token") or "")
        if not token:
            raise RuntimeError("Google Colab 云盘授权握手缺少令牌，请重试")
        post_resp = self._post_propagation(token, dryrun=dryrun)
        return post_resp.status_code, _parse_colab_json_lenient(post_resp)

    @staticmethod
    def _is_propagation_success(status: int, data: dict) -> bool:
        if status != 200 or not data:
            return False
        if data.get("success") is True:
            return True
        # Some responses omit `success` on HTTP 200 when no redirect challenge is present.
        if "success" not in data and not data.get("unauthorized_redirect_uri"):
            return True
        return False

    def _wait_for_wakeup(self, seconds: float) -> str:
        """Wait up to `seconds`, waking immediately on cancel, stop, explicit confirm, or window focus probe."""
        end = time.monotonic() + max(0.0, seconds)
        while True:
            if self._is_aborted():
                return "cancel"
            if self.confirm_event.is_set():
                return "confirm"
            if self.probe_event.is_set():
                return "probe"
            remaining = end - time.monotonic()
            if remaining <= 0:
                return "poll"
            if self.cancel_event.wait(min(0.1, remaining)) or self._stop_event.is_set():
                return "cancel"

    def dryrun_check(self) -> tuple[bool, str]:
        """Fast read-only check: returns `(authorized, consent_url)` for this VM."""
        try:
            token = self._fetch_xsrf_token(dryrun=True)
            resp = self._post_propagation(token, dryrun=True)
        except (requests.RequestException, TransportError):
            raise RuntimeError(
                "检查 Google 云盘授权状态时网络异常，请检查网络或代理后重试"
            ) from None
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
        consent_uri = ""

        try:
            # Phase 1: Single read-only dry-run check to see if already authorized or get consent URL.
            dryrun_ok = False
            transient_errors = 0
            while True:
                if self._is_aborted():
                    raise RuntimeError("已取消 Google 云盘挂载")
                if time.monotonic() >= deadline:
                    raise RuntimeError(self._timeout_message())
                try:
                    status, data = self._request_propagation(dryrun=True)
                except (requests.RequestException, TransportError):
                    transient_errors += 1
                    if transient_errors > 4:
                        raise RuntimeError(
                            "连接 Google Colab 云盘授权接口网络异常，请检查网络或代理后重试"
                        ) from None
                    if self._wait_for_wakeup(min(1.5, self.poll_interval)) == "cancel":
                        raise RuntimeError("已取消 Google 云盘挂载")
                    continue

                if status in (400, 403, 500, 502, 503, 504):
                    transient_errors += 1
                    if transient_errors > 4:
                        raise RuntimeError(
                            f"Google 云盘授权确认失败（HTTP {status}），请重试"
                        )
                    if self._wait_for_wakeup(min(1.5, self.poll_interval)) == "cancel":
                        raise RuntimeError("已取消 Google 云盘挂载")
                    continue
                if status != 200:
                    raise RuntimeError(f"Google 云盘连接返回 HTTP {status}，请重试")

                if data.get("success") is True:
                    dryrun_ok = True
                    break

                consent_uri = _validate_consent_url(
                    data.get("unauthorized_redirect_uri", "")
                )
                if not consent_uri:
                    raise RuntimeError(
                        "Google 未接受本次云盘授权请求，请稍后重试；无需重新登录 Google"
                    )
                break

            # Phase 1b: Already authorized fast path — immediately propagate with a fresh dryrun=false token.
            if dryrun_ok:
                _emit_log(
                    self.on_log,
                    "已收到云盘授权，正在将凭据同步到远端 GPU 实例",
                    "云盘",
                )
                for attempt in range(2):
                    if self._is_aborted():
                        raise RuntimeError("已取消 Google 云盘挂载")
                    try:
                        status, res_data = self._request_propagation(dryrun=False)
                    except (requests.RequestException, TransportError):
                        if attempt == 0:
                            if self._wait_for_wakeup(min(1.0, self.poll_interval)) == "cancel":
                                raise RuntimeError("已取消 Google 云盘挂载")
                            continue
                        raise RuntimeError(
                            "向远端实例同步云盘凭据时网络异常，请检查网络或代理后重试"
                        ) from None
                    if self._is_propagation_success(status, res_data):
                        _emit_log(
                            self.on_log,
                            "云盘凭据已同步至远端实例，正在完成 DriveFS 挂载",
                            "云盘",
                        )
                        return True
                    maybe_uri = _validate_consent_url(
                        res_data.get("unauthorized_redirect_uri", "")
                    )
                    if maybe_uri:
                        consent_uri = maybe_uri
                        break
                    if status in (400, 403, 500, 502, 503, 504) and attempt == 0:
                        continue
                    raise RuntimeError(
                        f"向远端实例同步云盘凭据失败（HTTP {status}），请重试"
                    )
                if not consent_uri:
                    return False

            # Phase 2: Open browser consent and actively redeem via fresh dryrun=false calls.
            # Note: On Colab's backend, dryrun=true only checks already-propagated state /
            # returns the OAuth challenge URL; calling dryrun=false with a fresh XSRF token
            # is what actually redeems the browser's ephemeral consent into the VM.
            consent_opened = True
            self.confirm_event.clear()
            self.probe_event.clear()
            if self.account_email:
                _emit_log(
                    self.on_log,
                    f"请在浏览器中使用 {self.account_email} 勾选全部云盘权限并点击允许（仅本实例确认一次）",
                    "云盘",
                )
            else:
                _emit_log(
                    self.on_log,
                    "请在浏览器中勾选全部云盘权限并点击允许，以确认当前实例挂载 Google 云盘（仅本实例确认一次）",
                    "云盘",
                )
            if callable(self.on_auth_url) and not self._stop_event.is_set():
                self.on_auth_url(consent_uri)

            auth_errors = 0
            while True:
                if self._is_aborted():
                    raise RuntimeError("已取消 Google 云盘挂载")
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise RuntimeError(self._timeout_message())

                wake = self._wait_for_wakeup(min(self.poll_interval, remaining))
                if wake == "cancel":
                    raise RuntimeError("已取消 Google 云盘挂载")
                explicit_confirm = (wake == "confirm") or self.confirm_event.is_set()
                self.confirm_event.clear()
                self.probe_event.clear()

                if self._is_aborted():
                    raise RuntimeError("已取消 Google 云盘挂载")

                try:
                    status, res_data = self._request_propagation(dryrun=False)
                except (requests.RequestException, TransportError):
                    if explicit_confirm:
                        _emit_log(
                            self.on_log,
                            "核验云盘授权时网络暂时不可用，将自动重试",
                            "云盘",
                            "warning",
                        )
                    continue

                if self._is_propagation_success(status, res_data):
                    self._emit_hint("")
                    _emit_log(
                        self.on_log,
                        "云盘凭据已同步至远端实例，正在完成 DriveFS 挂载",
                        "云盘",
                    )
                    return True

                # HTTP 400 or HTTP 200 with success=false means browser consent is not yet
                # recorded for this account (still reading consent page, unchecked granular
                # Drive scopes, or approved under a different browser Google account).
                if status == 400 or (status == 200 and res_data.get("success") is False):
                    auth_errors = 0
                    if explicit_confirm:
                        hint = self._not_confirmed_hint()
                        self._emit_hint(hint)
                        _emit_log(self.on_log, hint, "云盘", "warning")
                    continue

                if status in (401, 403):
                    auth_errors += 1
                    if auth_errors > 8:
                        raise RuntimeError(
                            f"Google 云盘授权确认失败（HTTP {status}），请重试"
                        )
                    continue

                if status in (500, 502, 503, 504):
                    continue

                raise RuntimeError(
                    f"向远端实例同步云盘凭据失败（HTTP {status}），请重试"
                )
        finally:
            if not self._stop_event.is_set():
                self._emit_hint("")
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

        with self._hook_lock:
            if self._hook_started or self._stop_event.is_set():
                return True
            self._hook_started = True

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
