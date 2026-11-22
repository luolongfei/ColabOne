"""Credentials stay in the operating system credential store, never in repo files."""

from __future__ import annotations
import hashlib
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
from google.auth.transport.requests import AuthorizedSession, Request
from google.auth.exceptions import RefreshError, TransportError
from google_auth_oauthlib.flow import InstalledAppFlow
from colab_cli.auth import PUBLIC_SCOPES, REMOTE_REDIRECT_URI

from bridge.oauth_page import render_colabone_oauth_page

_VAULT_MISSING = object()


class _BoundedTimeoutSession(requests.Session):
    def __init__(self, default_timeout: float = 20.0):
        super().__init__()
        self._default_timeout = default_timeout

    def request(self, method, url, **kwargs):
        kwargs.setdefault("timeout", self._default_timeout)
        return super().request(method, url, **kwargs)


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


COLABONE_KEYRING_SERVICE = "ColabOne"
COLABONE_GCP_ADC_CLIENT_PREFIX = "764086051850-"
COLABONE_QUOTA_PROJECT_PREFIX = "colabone-drive-"
_GCP_PROJECT_ID_RE = re.compile(r"^[a-z][a-z0-9-]{4,28}[a-z0-9]$")
_QUOTA_NEGATIVE_CACHE_TTL = 600.0


def _emit_auth_log(on_log, message: str, source: str = "云盘", level: str = "info") -> None:
    if not callable(on_log) or not message:
        return
    try:
        on_log(message, source, level)
    except TypeError:
        on_log(message)

_OAUTH_CALLBACK_TEXT = {
    "zh-CN": (
        "ColabOne 登录成功。你可以关闭此页面，返回客户端。",
        "登录未完成，请返回 ColabOne 重试",
    ),
    "en-US": (
        "Signed in to ColabOne. You can close this tab and return to the app.",
        "Sign-in was not completed. Please return to ColabOne and try again.",
    ),
    "zh-TW": (
        "已成功登入 ColabOne。你可以關閉此分頁並返回用戶端。",
        "登入尚未完成，請返回 ColabOne 重試。",
    ),
    "zh-HK": (
        "已成功登入 ColabOne。你可以關閉此分頁並返回應用程式。",
        "登入尚未完成，請返回 ColabOne 再試。",
    ),
    "ja-JP": (
        "ColabOne へのログインが完了しました。このタブを閉じてアプリに戻ってください。",
        "ログインが完了しませんでした。ColabOne に戻って再試行してください。",
    ),
    "ko-KR": (
        "ColabOne 로그인을 완료했습니다. 이 탭을 닫고 앱으로 돌아가 주세요.",
        "로그인이 완료되지 않았습니다. ColabOne으로 돌아가 다시 시도해 주세요.",
    ),
}


class ColabOneVault:
    def __init__(self):
        self.lock = threading.RLock()
        self._cache: dict[str, object] = {}

    def get(self, name, default=None):
        with self.lock:
            cached = self._cache.get(name, _VAULT_MISSING)
            if cached is not _VAULT_MISSING:
                return json.loads(cached) if cached is not None else default
            raw = keyring.get_password(COLABONE_KEYRING_SERVICE, name)
            self._cache[name] = raw
            return json.loads(raw) if raw else default

    def set(self, name, value):
        encoded = json.dumps(value)
        with self.lock:
            keyring.set_password(COLABONE_KEYRING_SERVICE, name, encoded)
            self._cache[name] = encoded

    def delete(self, name):
        with self.lock:
            try:
                keyring.delete_password(COLABONE_KEYRING_SERVICE, name)
            except keyring.errors.PasswordDeleteError:
                pass
            self._cache[name] = None


class ColabOneAuth:
    def __init__(self, vault):
        self.vault = vault
        self.flow = None
        self.lock = threading.RLock()
        self._quota_lock = threading.RLock()
        self.login_event = threading.Event()
        self.loopback_active = False
        self.locale = "zh-CN"
        self._quota_probe_cache: tuple[float, str, str] | None = None

    def profile(self):
        profile = self.vault.get("google_profile")
        if profile is None:
            profile = self.fetch_profile(self.credentials("colab"))
            self.vault.set("google_profile", profile)
        return profile

    @staticmethod
    def fetch_profile(credentials):
        last_exc = None
        for attempt in range(2):
            try:
                response = requests.get(
                    "https://www.googleapis.com/oauth2/v3/userinfo",
                    headers={"Authorization": "Bearer " + credentials.token},
                    timeout=20,
                )
                if response.status_code in (500, 502, 503, 504) and attempt == 0:
                    time.sleep(0.5)
                    continue
                response.raise_for_status()
                data = response.json()
                return {"email": data.get("email", ""), "sub": data["sub"]}
            except requests.RequestException as exc:
                last_exc = exc
                if attempt == 0:
                    time.sleep(0.5)
                    continue
                raise
        raise last_exc  # pragma: no cover

    def credentials(self, name):
        # Serialize refresh and account switching so a stale request cannot overwrite
        # credentials belonging to a newer login. Never erase tokens on network errors.
        with self.lock:
            raw = self.vault.get(name)
            if not raw:
                raise RuntimeError("请先完成 Google 登录")
            creds = Credentials.from_authorized_user_info(raw)
            if not creds.valid:
                if not creds.refresh_token:
                    raise RuntimeError("Google 授权无法自动续期，请在设置中重新登录")
                session = _BoundedTimeoutSession(default_timeout=20.0)
                try:
                    creds.refresh(Request(session=session))
                except RefreshError as exc:
                    if getattr(exc, "retryable", False):
                        raise RuntimeError(
                            "Google 暂时无法续期，已保留本机登录，请稍后重试"
                        ) from None
                    raise RuntimeError(
                        "Google 授权已失效，请在设置中重新登录"
                    ) from None
                except (TransportError, requests.RequestException):
                    raise RuntimeError(
                        "Google 暂时无法续期，已保留本机登录，请稍后重试"
                    ) from None
                finally:
                    session.close()
                self.vault.set(name, json.loads(creds.to_json()))
            return creds

    def has_drive_scope(self) -> bool:
        raw = self.vault.get("colab")
        if not isinstance(raw, dict) or not raw.get("refresh_token"):
            return False
        scopes = raw.get("scopes") or []
        return any("drive" in str(s).lower() for s in scopes)

    def requires_drive_quota_project(self) -> bool:
        """Return True when the saved OAuth token uses Google's public ADC client.

        Google disables `drive.googleapis.com` on project `764086051850` unless
        requests carry an `X-Goog-User-Project` header naming an active GCP
        project with Drive API enabled.
        """
        raw = self.vault.get("colab")
        if not isinstance(raw, dict):
            return False
        client_id = str(raw.get("client_id") or "")
        return client_id.startswith(COLABONE_GCP_ADC_CLIENT_PREFIX)

    def invalidate_drive_quota_project(self) -> None:
        with self._quota_lock:
            self.vault.delete("cloud_project")
            self._quota_probe_cache = None

    @staticmethod
    def _is_valid_project_id(project_id: str) -> bool:
        return bool(isinstance(project_id, str) and _GCP_PROJECT_ID_RE.match(project_id))

    @staticmethod
    def _drive_api_enabled(session: AuthorizedSession, project_id: str) -> bool:
        resp = session.get(
            f"https://serviceusage.googleapis.com/v1/projects/{project_id}/services/drive.googleapis.com",
            headers={"X-Goog-User-Project": project_id},
            timeout=10,
        )
        if resp.status_code != 200:
            return False
        return str((resp.json() or {}).get("state") or "").upper() == "ENABLED"

    @staticmethod
    def _enable_drive_api(session: AuthorizedSession, project_id: str) -> bool:
        resp = None
        for attempt in range(3):
            resp = session.post(
                f"https://serviceusage.googleapis.com/v1/projects/{project_id}/services/drive.googleapis.com:enable",
                headers={"X-Goog-User-Project": project_id},
                timeout=20,
            )
            if resp.status_code == 200:
                break
            # Freshly created GCP projects can take a couple seconds to propagate to Service Usage.
            if resp.status_code in (403, 404, 500, 503) and attempt < 2:
                time.sleep(2.0)
                continue
            return False
        if resp is None or resp.status_code != 200:
            return False
        op = resp.json() or {}
        op_name = str(op.get("name") or "")
        deadline = time.monotonic() + 30.0
        while not op.get("done") and op_name and time.monotonic() < deadline:
            time.sleep(1.2)
            poll = session.get(
                f"https://serviceusage.googleapis.com/v1/{op_name}",
                headers={"X-Goog-User-Project": project_id},
                timeout=12,
            )
            if poll.status_code != 200:
                break
            op = poll.json() or {}
        if op.get("error"):
            return False
        return bool(op.get("done")) or ColabOneAuth._drive_api_enabled(session, project_id)

    def drive_quota_status(self) -> dict:
        """Return offline readiness status for headless Drive quota-project mode."""
        needs_quota = self.requires_drive_quota_project()
        project_id = str(self.vault.get("cloud_project") or "")
        has_project = self._is_valid_project_id(project_id)
        with self._quota_lock:
            reason = self._quota_probe_cache[2] if self._quota_probe_cache else ""
        if not needs_quota:
            return {
                "needs_quota": False,
                "ready": self.has_drive_scope(),
                "project_id": "",
                "reason": "not_required",
            }
        if has_project:
            return {
                "needs_quota": True,
                "ready": True,
                "project_id": project_id,
                "reason": "ready",
            }
        return {
            "needs_quota": True,
            "ready": False,
            "project_id": "",
            "reason": reason,
        }

    def ensure_drive_quota_project(
        self, on_log=None, force_refresh: bool = False
    ) -> tuple[str, str]:
        """Find or provision a free Drive-enabled GCP quota project for headless rclone.

        Returns `(project_id, reason)`. Never raises on missing GCP ToS or org
        restrictions; callers cleanly fall back to Colab native DriveFS instead.
        """
        if not self.requires_drive_quota_project():
            return "", "not_required"

        with self._quota_lock:
            if not force_refresh:
                cached = self.vault.get("cloud_project")
                if self._is_valid_project_id(cached):
                    return cached, "cached"
                if self._quota_probe_cache is not None:
                    ts, pid, reason = self._quota_probe_cache
                    if pid and self._is_valid_project_id(pid):
                        return pid, reason
                    if (time.monotonic() - ts) < _QUOTA_NEGATIVE_CACHE_TTL:
                        return "", reason

        saved = self.vault.get("colab") or {}
        scopes = saved.get("scopes") or []
        if not any("cloud-platform" in str(s).lower() for s in scopes):
            with self._quota_lock:
                self._quota_probe_cache = (time.monotonic(), "", "no_cloud_scope")
            return "", "no_cloud_scope"

        initial_sub = str((self.vault.get("google_profile") or {}).get("sub") or "")

        def _persist_project(project_id: str, reason_tag: str) -> tuple[str, str]:
            with self._quota_lock:
                cur_sub = str((self.vault.get("google_profile") or {}).get("sub") or "")
                if initial_sub and cur_sub and cur_sub != initial_sub:
                    return "", "account_changed"
                self.vault.set("cloud_project", project_id)
                self._quota_probe_cache = (time.monotonic(), project_id, reason_tag)
            return project_id, reason_tag

        session = None
        try:
            creds = self.credentials("colab")
            session = AuthorizedSession(creds)
            resp = session.get(
                "https://cloudresourcemanager.googleapis.com/v1/projects",
                params={"filter": "lifecycleState:ACTIVE", "pageSize": 50},
                timeout=12,
            )
            if resp.status_code != 200:
                if resp.status_code in (429, 500, 502, 503, 504):
                    return "", "network_error"
                text_lower = (resp.text or "").lower()
                reason = (
                    "gcp_tos_required"
                    if ("terms of service" in text_lower or '"tos"' in text_lower)
                    else "gcp_project_unavailable"
                )
                with self._quota_lock:
                    self._quota_probe_cache = (time.monotonic(), "", reason)
                return "", reason

            projects = [
                p
                for p in ((resp.json() or {}).get("projects") or [])
                if isinstance(p, dict)
                and self._is_valid_project_id(str(p.get("projectId") or ""))
                and str(p.get("lifecycleState") or "").upper() == "ACTIVE"
            ]

            # 1) Reuse an existing ColabOne-owned free quota project first.
            for proj in projects:
                pid = str(proj["projectId"])
                if not pid.startswith(COLABONE_QUOTA_PROJECT_PREFIX):
                    continue
                if self._drive_api_enabled(session, pid) or self._enable_drive_api(
                    session, pid
                ):
                    return _persist_project(pid, "reused_colabone_project")

            # 2) Read-only check: reuse an existing active project that already
            #    has drive.googleapis.com enabled (never mutate unrelated projects).
            for proj in projects[:10]:
                pid = str(proj["projectId"])
                if pid.startswith(COLABONE_QUOTA_PROJECT_PREFIX):
                    continue
                if self._drive_api_enabled(session, pid):
                    return _persist_project(pid, "reused_existing_project")

            # 3) Attempt once to create a single free dedicated quota project.
            profile = self.profile()
            sub = str((profile or {}).get("sub") or "")
            if not sub:
                return "", "gcp_project_unavailable"
            if not initial_sub:
                initial_sub = sub
            candidates = [
                COLABONE_QUOTA_PROJECT_PREFIX
                + hashlib.sha256(f"colabone-drive:{sub}".encode("utf-8")).hexdigest()[:10],
                COLABONE_QUOTA_PROJECT_PREFIX
                + hashlib.sha256(f"colabone-drive:v2:{sub}".encode("utf-8")).hexdigest()[:10],
            ]
            _emit_auth_log(
                on_log,
                "正在检测或初始化免费 Google Drive API 配额项目（仅首次检测）",
                "云盘",
            )
            pid = candidates[0]
            create_resp = None
            for idx, cand in enumerate(candidates):
                pid = cand
                create_resp = session.post(
                    "https://cloudresourcemanager.googleapis.com/v1/projects",
                    json={"projectId": pid, "name": "ColabOne Drive"},
                    timeout=15,
                )
                if create_resp.status_code == 409 and idx + 1 < len(candidates):
                    continue
                break
            if create_resp is None or create_resp.status_code != 200:
                if (
                    create_resp is not None
                    and create_resp.status_code in (429, 500, 502, 503, 504)
                ):
                    return "", "network_error"
                text_lower = (getattr(create_resp, "text", "") or "").lower()
                reason = (
                    "gcp_tos_required"
                    if ("terms of service" in text_lower or '"tos"' in text_lower)
                    else "gcp_project_unavailable"
                )
                with self._quota_lock:
                    self._quota_probe_cache = (time.monotonic(), "", reason)
                return "", reason

            op = create_resp.json() or {}
            op_name = str(op.get("name") or "")
            deadline = time.monotonic() + 25.0
            while not op.get("done") and op_name and time.monotonic() < deadline:
                time.sleep(1.5)
                poll = session.get(
                    f"https://cloudresourcemanager.googleapis.com/v1/{op_name}",
                    timeout=12,
                )
                if poll.status_code != 200:
                    break
                op = poll.json() or {}

            op_err = op.get("error")
            if op_err or not op.get("done"):
                err_str = json.dumps(op_err or {}, ensure_ascii=False).lower()
                reason = (
                    "gcp_tos_required"
                    if ("terms of service" in err_str or '"tos"' in err_str)
                    else "gcp_project_unavailable"
                )
                with self._quota_lock:
                    self._quota_probe_cache = (time.monotonic(), "", reason)
                return "", reason

            if self._enable_drive_api(session, pid):
                res_pid, res_reason = _persist_project(pid, "created_colabone_project")
                if res_pid:
                    _emit_auth_log(
                        on_log,
                        f"已自动初始化免费 Drive 配额项目 ({pid})，后续所有实例免弹窗自动挂载",
                        "云盘",
                    )
                return res_pid, res_reason

            with self._quota_lock:
                self._quota_probe_cache = (
                    time.monotonic(),
                    "",
                    "gcp_project_unavailable",
                )
            return "", "gcp_project_unavailable"
        except Exception:
            return "", "network_error"
        finally:
            if session is not None:
                try:
                    session.close()
                except Exception:
                    pass

    def build_rclone_conf(self, quota_project: str | None = None) -> str:
        """Build [colabone_gdrive] rclone config from the saved Google OAuth token."""
        if not self.has_drive_scope():
            return ""
        creds = self.credentials("colab")
        raw = json.loads(creds.to_json())
        saved = self.vault.get("colab") or {}
        scopes = raw.get("scopes") or saved.get("scopes") or []
        drive_scope = (
            "drive"
            if any(str(s).endswith("/drive") for s in scopes)
            else (
                "drive.file"
                if any("drive.file" in str(s) for s in scopes)
                else ""
            )
        )
        if not drive_scope or not raw.get("token") or not raw.get("refresh_token"):
            return ""
        token_obj = {
            "access_token": raw["token"],
            "token_type": "Bearer",
            "refresh_token": raw["refresh_token"],
        }
        if raw.get("expiry"):
            token_obj["expiry"] = raw["expiry"]
        qp = (
            quota_project
            if quota_project is not None
            else str(self.vault.get("cloud_project") or "")
        )
        lines = []
        if self._is_valid_project_id(qp):
            lines.append(f"# colabone_quota_project = {qp}")
        lines.extend(
            [
                "[colabone_gdrive]",
                "type = drive",
                f"client_id = {raw.get('client_id', '')}",
                f"client_secret = {raw.get('client_secret', '')}",
                f"scope = {drive_scope}",
                f"token = {json.dumps(token_obj, separators=(',', ':'))}",
                "",
            ]
        )
        return "\n".join(lines)

    def _has_active_sessions(self):
        return bool(
            self.vault.get("runtime_session_ids")
            or self.vault.get("runtime_session")
            or self.vault.get("runtime_sessions")
        )

    def colab_begin(self):
        with self.lock:
            if self._has_active_sessions():
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
                prompt="select_account consent",
                token_usage="remote",
                access_type="offline",
            )
            return {"url": url}

    def colab_finish(self, code, flow=None):
        with self.lock:
            target_flow = flow if flow is not None else self.flow
            if not target_flow:
                raise RuntimeError("请先打开登录页面")
            if self._has_active_sessions():
                raise RuntimeError("切换账号前请先释放当前实例")
            parsed = parse_authorization_reply(code)
            try:
                if isinstance(target_flow, InstalledAppFlow):
                    target_flow.fetch_token(code=parsed, timeout=20)
                else:
                    target_flow.fetch_token(code=parsed)
            except Exception:
                raise RuntimeError(
                    "授权回显已失效或不属于本次登录，请重新打开登录页"
                ) from None
            try:
                profile = self.fetch_profile(target_flow.credentials)
            except Exception:
                raise RuntimeError(
                    "读取 Google 账号信息失败，请检查网络连接后重试"
                ) from None
            previous = self.vault.get("google_profile") or {}
            if profile.get("sub") != previous.get("sub"):
                self.vault.delete("cloud_project")
            with self._quota_lock:
                self._quota_probe_cache = None
            self.vault.set("google_profile", profile)
            self.vault.set("colab", json.loads(target_flow.credentials.to_json()))
            if flow is None or self.flow is target_flow:
                self.flow = None
            self.login_event.set()
            return {"ok": True}

    def colab_login(self, ready_callback):
        if self._has_active_sessions():
            raise RuntimeError("切换账号前请先释放当前实例")
        auth = self
        state = ""
        errors = []
        local_flow = None

        class Callback(BaseHTTPRequestHandler):
            timeout = 5

            def log_message(self, *_args):
                pass  # Callback URLs contain an authorization code.

            def do_GET(self):
                parsed_req = urlparse(self.path)
                if parsed_req.path == "/favicon.ico":
                    self.send_response(204)
                    self.send_header("Cache-Control", "no-store")
                    self.end_headers()
                    return
                params = parse_qs(parsed_req.query)
                if not hmac.compare_digest(params.get("state", [""])[0], state):
                    self.send_error(400, "Invalid OAuth state")
                    return
                ok_msg, err_msg = _OAUTH_CALLBACK_TEXT.get(
                    auth.locale, _OAUTH_CALLBACK_TEXT["zh-CN"]
                )
                succeeded = False
                email = ""
                try:
                    if params.get("error"):
                        raise RuntimeError("Google 授权已取消，请重新登录")
                    auth.colab_finish(params.get("code", [""])[0], flow=local_flow)
                    succeeded = True
                    profile = auth.vault.get("google_profile") or {}
                    email = str(profile.get("email") or "")
                    message = ok_msg
                except RuntimeError as exc:
                    errors.append(str(exc) or err_msg)
                    auth.login_event.set()
                    message = err_msg
                except Exception:
                    errors.append(err_msg)
                    auth.login_event.set()
                    message = err_msg
                lang_attr = auth.locale if auth.locale in _OAUTH_CALLBACK_TEXT else "zh-CN"
                content = render_colabone_oauth_page(
                    locale=lang_attr,
                    ok=succeeded,
                    message=message,
                    email=email,
                ).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.send_header("Content-Length", str(len(content)))
                self.send_header("Cache-Control", "no-store")
                self.send_header("Referrer-Policy", "no-referrer")
                self.send_header("X-Content-Type-Options", "nosniff")
                self.send_header(
                    "Content-Security-Policy",
                    "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:;",
                )
                self.end_headers()
                self.wfile.write(content)

        self.loopback_active = True
        try:
            with HTTPServer(("127.0.0.1", 0), Callback) as server:
                server.timeout = 0.5
                cfg = json.loads(
                    resources.files("colab_cli")
                    .joinpath("oauth_config.json")
                    .read_text(encoding="utf-8")
                )
                with self.lock:
                    self.login_event.clear()
                    local_flow = InstalledAppFlow.from_client_config(
                        cfg, PUBLIC_SCOPES, autogenerate_code_verifier=True
                    )
                    local_flow.redirect_uri = f"http://localhost:{server.server_port}/"
                    self.flow = local_flow
                    url, state = local_flow.authorization_url(
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
        finally:
            self.loopback_active = False
