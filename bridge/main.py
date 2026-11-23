"""Private JSON-lines RPC over the desktop child's stdin/stdout, no listening port."""

from __future__ import annotations
import concurrent.futures
import json
import math
import sys
import threading
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from bridge.activity import safe_message
from bridge.controller import ColabOneController

_FAST_RPC_METHODS = frozenset(
    {
        "snapshot",
        "logs.clear",
        "error.clear",
        "drive.cancel",
        "drive.confirm",
        "drive.probe",
        "locale.set",
        "api.key",
        "api.tunnel_token",
        "comfyui.canvas.tunnel_token",
        "settings.hf_token",
    }
)


def _sanitize_json_value(value):
    if isinstance(value, float):
        return value if math.isfinite(value) else None
    if isinstance(value, dict):
        return {k: _sanitize_json_value(v) for k, v in value.items()}
    if isinstance(value, list):
        return [_sanitize_json_value(item) for item in value]
    if isinstance(value, tuple):
        return [_sanitize_json_value(item) for item in value]
    return value


def main():
    # The Go bridge always sends UTF-8, including on Windows with a non-UTF-8
    # system locale. Pipes do not inherit the console's Unicode handling.
    for stream in (sys.stdin, sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8")
    if "--self-test" in sys.argv:
        from common.models import ModelSpec
        from common.comfyui import ComfyCatalog
        import keyring, fastapi, colab_cli  # noqa: F401

        root = Path(__file__).resolve().parents[1]
        presets = json.loads(
            (root / "catalog/presets.json").read_text(encoding="utf-8")
        )
        for spec in presets:
            ModelSpec.model_validate(spec)
        comfy_catalog = json.loads(
            (root / "catalog/comfyui.json").read_text(encoding="utf-8")
        )
        ComfyCatalog.model_validate(comfy_catalog)
        print("ColabOne runtime OK; no network or credentials accessed")
        return
    protocol = sys.stdout
    sys.stdout = (
        sys.stderr
    )  # Third-party OAuth/Colab progress must never corrupt the protocol.
    controller = ColabOneController(Path(__file__).resolve().parents[1])
    lock = threading.Lock()

    def handle(request):
        try:
            method = request["method"]
            raw_payload = request.get("payload")
            payload = raw_payload if isinstance(raw_payload, dict) else {}
            value = (
                controller.snapshot()
                if method == "snapshot"
                else controller.command(method, payload)
            )
            response = {"id": request["id"], "result": _sanitize_json_value(value)}
        except Exception as exc:
            response = {"id": request["id"], "error": safe_message(exc)[:500]}
        encoded = json.dumps(response, ensure_ascii=False, allow_nan=False)
        with lock:
            protocol.write(encoded + "\n")
            protocol.flush()

    fast_pool = concurrent.futures.ThreadPoolExecutor(max_workers=4)
    slow_pool = concurrent.futures.ThreadPoolExecutor(max_workers=8)
    try:
        for line in sys.stdin:
            try:
                request = json.loads(line)
                if not isinstance(request, dict) or "method" not in request or "id" not in request:
                    continue
                target_pool = (
                    fast_pool
                    if request["method"] in _FAST_RPC_METHODS
                    else slow_pool
                )
                target_pool.submit(handle, request)
            except ValueError:
                continue
    finally:
        close_fn = getattr(controller, "close", None)
        if callable(close_fn):
            close_fn()
        else:
            controller.stop.set()
        fast_pool.shutdown(wait=False, cancel_futures=True)
        slow_pool.shutdown(wait=False, cancel_futures=True)


if __name__ == "__main__":
    main()
