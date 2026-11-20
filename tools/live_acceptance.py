"""Explicit live smoke test after app authorization. Uses <=5 CU budget and releases its own instance.
Run manually: python tools/live_acceptance.py --max-cu 5
Never run from CI. Refuses to reuse an existing instance.
"""

from __future__ import annotations
import argparse
import json
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from bridge.controller import ColabOneController

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


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--max-cu", type=float, required=True)
    args = parser.parse_args()
    if not 0 < args.max_cu <= 5:
        raise SystemExit("Acceptance budget must be within 5 CU")
    root = Path(__file__).resolve().parents[1]
    control = ColabOneController(root, background=False)
    if control.vault.get("runtime_session") or control.vault.get("runtime_sessions"):
        raise SystemExit(
            "An owned runtime already exists. Release it in the app before acceptance."
        )
    control.settings.update(gpu="T4")
    budget_deadline = time.time() + min(1800, max(300, int(args.max_cu * 900)))
    events = []

    def wait_task(task):
        while True:
            remote = control.provider.remote_info() or {}
            remote_deadline = remote.get("deadline", 0) or 0
            effective_deadline = (
                min(remote_deadline, budget_deadline)
                if remote_deadline > 0
                else budget_deadline
            )
            if time.time() >= effective_deadline - 30:
                raise RuntimeError("Stopping before the acceptance budget deadline")
            status = control.remote("GET", "status")
            item = next(t for t in status["tasks"] if t["id"] == task["id"])
            if item["state"] != "running":
                if item["state"] != "completed":
                    raise RuntimeError(item["error"] or item["state"])
                return
            time.sleep(2)

    try:
        control.start()
        events.append("runtime ready")
        status = control.remote("GET", "status")
        resources = status.get("resources") or {}
        if not (
            resources.get("gpu", {}).get("total_mib", 0) > 0
            and resources.get("memory", {}).get("total_bytes", 0) > 0
            and resources.get("disk", {}).get("total_bytes", 0) > 0
            and resources.get("cpu", {}).get("cores", 0) > 0
        ):
            raise RuntimeError("Telemetry resources missing from remote status")
        events.append("telemetry verified")
        control.remote("POST", "models", SMOKE_MODEL)
        wait_task(control.remote("POST", "models/qwen25-05b/download", {}))
        events.append("remote download verified")
        wait_task(control.remote("POST", "models/qwen25-05b/load"))
        events.append("GPU model ready")
        loaded_status = control.remote("GET", "status")
        if loaded_status.get("protocol", 0) < 7 or loaded_status.get("loaded_model") != "qwen25-05b":
            raise RuntimeError("Loaded model status verification failed")
        if not isinstance(loaded_status.get("comfyui"), dict):
            raise RuntimeError("ComfyUI status missing from remote status")
        events.append("protocol 7 & comfyui status verified")
        control.remote("POST", "unload")
        events.append("model unloaded")
        control.remote("POST", "prepare-release")
        events.append("prepare release verified")
    finally:
        if control.vault.get("runtime_session") or control.vault.get("runtime_sessions"):
            control.provider.release()
            events.append("release verified")
        print(json.dumps({"events": events}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
