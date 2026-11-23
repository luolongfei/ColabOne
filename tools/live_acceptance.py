"""Explicit live smoke test against a running ColabOne remote runtime.

The desktop controller is implemented in pure Go under ``console/`` (authorize
and start a fresh T4 runtime from the ColabOne Go desktop app or via
``cd console && go run .``). The remote control API (``server/app.py``) only
binds to the VM loopback address (``http://127.0.0.1:7860/api/v1``) and is
never exposed as an HTTPS public endpoint. Run this script inside the VM
against ``http://127.0.0.1:7860`` or over an explicit local loopback forward.
Never run from CI. Always enforce <= 5 CU budget and release the runtime in
ColabOne after completion.

Manual usage after starting a fresh T4 runtime in ColabOne:
    python tools/live_acceptance.py --max-cu 5 --base-url http://127.0.0.1:7860 --token <token>
"""

from __future__ import annotations

import argparse
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request

SMOKE_MODEL = {
    "id": "qwen25-05b",
    "name": "Qwen2.5 · 0.5B",
    "description": "轻量验收模型 · Q4_K_M",
    "min_vram_mib": 2048,
    "context_length": 8192,
    "temperature": 0.7,
    "source": "Qwen",
    "files": [
        {
            "repo": "Qwen/Qwen2.5-0.5B-Instruct-GGUF",
            "revision": "9217f5db79a29953eb74d5343926648285ec7e67",
            "path": "qwen2.5-0.5b-instruct-q4_k_m.gguf",
            "size": 491400032,
            "sha256": "74a4da8c9fdbcd15bd1f6d01d621410d31c6fc00986f5eb687824e7b93d7a9db",
            "role": "model",
        }
    ],
}


def _validate_base_url(base_url: str) -> str:
    cleaned = base_url.strip().rstrip("/")
    parsed = urllib.parse.urlparse(cleaned)
    if parsed.scheme == "http" and parsed.hostname in {"127.0.0.1", "localhost", "::1"}:
        return cleaned
    raise SystemExit(
        "Acceptance --base-url must target the VM loopback control API "
        "(http://127.0.0.1:7860 inside the VM or over an explicit local forward), "
        "not an HTTPS public endpoint"
    )


def _build_api_url(base_url: str, path: str) -> str:
    base = base_url.strip().rstrip("/")
    clean_path = path.lstrip("/")
    if base.endswith("/api/v1"):
        if clean_path == "api/v1":
            clean_path = ""
        elif clean_path.startswith("api/v1/"):
            clean_path = clean_path.removeprefix("api/v1/")
        return f"{base}/{clean_path}" if clean_path else base
    if clean_path == "api/v1" or clean_path.startswith("api/v1/"):
        return f"{base}/{clean_path}"
    return f"{base}/api/v1/{clean_path}" if clean_path else f"{base}/api/v1"


def _build_no_proxy_opener() -> urllib.request.OpenerDirector:
    return urllib.request.build_opener(urllib.request.ProxyHandler({}))


_NO_PROXY_OPENER = _build_no_proxy_opener()


def _remote_request(
    base_url: str, token: str, method: str, path: str, payload: dict | None = None
) -> dict:
    url = _build_api_url(base_url, path)
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/json",
    }
    if data is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with _NO_PROXY_OPENER.open(req, timeout=60) as resp:
            body = resp.read().decode("utf-8")
            return json.loads(body) if body else {}
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"Remote {method} {url} failed ({exc.code}): {detail}") from exc


def main():
    parser = argparse.ArgumentParser(
        description=(
            "Manual live smoke test against the ColabOne VM loopback control API "
            "(http://127.0.0.1:7860, <= 5 CU)."
        )
    )
    parser.add_argument("--max-cu", type=float, required=True)
    parser.add_argument(
        "--base-url",
        default=os.environ.get("COLABONE_ACCEPTANCE_URL", ""),
        help=(
            "VM loopback control API base URL (e.g. http://127.0.0.1:7860 inside "
            "the VM or over an explicit local forward; not an HTTPS public endpoint)"
        ),
    )
    parser.add_argument(
        "--token",
        default=os.environ.get("COLABONE_ACCEPTANCE_TOKEN", ""),
        help="Bearer token for the VM loopback control API",
    )
    args = parser.parse_args()
    if not 0 < args.max_cu <= 5:
        raise SystemExit("Acceptance budget must be within 5 CU")
    if not args.base_url or not args.token:
        raise SystemExit(
            "ColabOne desktop control now runs in pure Go under console/. "
            "Start a fresh T4 runtime in the ColabOne app (or via `cd console && go run .`), "
            "then run this script inside the VM (or over an explicit local forward) "
            "targeting the VM loopback control API (e.g. --base-url http://127.0.0.1:7860, "
            "not an HTTPS public endpoint) with --token (or set COLABONE_ACCEPTANCE_URL / "
            "COLABONE_ACCEPTANCE_TOKEN). Release the runtime in the app afterwards."
        )

    base_url = _validate_base_url(args.base_url)
    token = args.token.strip()
    budget_deadline = time.time() + min(1800, max(300, int(args.max_cu * 900)))
    events: list[str] = []

    def remote(method: str, path: str, payload: dict | None = None) -> dict:
        return _remote_request(base_url, token, method, path, payload)

    def wait_task(task: dict) -> None:
        while True:
            if time.time() >= budget_deadline - 30:
                raise RuntimeError("Stopping before the acceptance budget deadline")
            status = remote("GET", "status")
            item = next(t for t in status["tasks"] if t["id"] == task["id"])
            if item["state"] != "running":
                if item["state"] != "completed":
                    raise RuntimeError(item["error"] or item["state"])
                return
            time.sleep(2)

    try:
        status = remote("GET", "status")
        events.append("runtime reachable")
        resources = status.get("resources") or {}
        if not (
            resources.get("gpu", {}).get("total_mib", 0) > 0
            and resources.get("memory", {}).get("total_bytes", 0) > 0
            and resources.get("disk", {}).get("total_bytes", 0) > 0
            and resources.get("cpu", {}).get("cores", 0) > 0
        ):
            raise RuntimeError("Telemetry resources missing from remote status")
        events.append("telemetry verified")
        remote("POST", "models", SMOKE_MODEL)
        wait_task(remote("POST", "models/qwen25-05b/download", {}))
        events.append("remote download verified")
        wait_task(remote("POST", "models/qwen25-05b/load"))
        events.append("GPU model ready")
        loaded_status = remote("GET", "status")
        if (
            loaded_status.get("protocol", 0) < 7
            or loaded_status.get("loaded_model") != "qwen25-05b"
        ):
            raise RuntimeError("Loaded model status verification failed")
        if not isinstance(loaded_status.get("comfyui"), dict):
            raise RuntimeError("ComfyUI status missing from remote status")
        events.append("protocol 7 & comfyui status verified")
        remote("POST", "unload")
        events.append("model unloaded")
        remote("POST", "prepare-release")
        events.append("prepare release verified")
    finally:
        print(json.dumps({"events": events}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
