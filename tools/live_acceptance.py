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
from bridge.controller import Controller


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--max-cu", type=float, required=True)
    parser.add_argument(
        "--without-drive",
        action="store_true",
        help="GPU-only check; skip cloud persistence",
    )
    args = parser.parse_args()
    if not 0 < args.max_cu <= 5:
        raise SystemExit("Acceptance budget must be within 5 CU")
    root = Path(__file__).resolve().parents[1]
    control = Controller(root, background=False)
    if control.vault.get("runtime_session"):
        raise SystemExit(
            "An owned runtime already exists. Release it in the app before acceptance."
        )
    control.settings.update(
        gpu="T4", max_cu=args.max_cu, auto_drive=not args.without_drive
    )
    # A normal desktop run opens this URL automatically. CLI acceptance prints
    # the consent link for the operator, once, and never prints credentials.
    original_update = control.update
    opened = set()

    def progress(**values):
        url = values.get("auth_url")
        if url and url not in opened:
            opened.add(url)
            print("Open this Google consent page in your browser:", url, flush=True)
        original_update(**values)

    control.update = progress
    events = []

    def wait_task(task):
        while True:
            remote = control.vault.get("remote")
            if time.time() >= remote["deadline"] - 30:
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
        wait_task(control.remote("POST", "models/qwen25-05b/download", {}))
        events.append("remote download verified")
        wait_task(control.remote("POST", "models/qwen25-05b/load"))
        events.append("GPU model ready")
        turn = control.remote(
            "POST",
            "turns",
            {"text": "Reply with one short greeting.", "conversation_id": ""},
        )
        wait_task(turn)
        conversations = control.remote("GET", "conversations")
        convo = next(c for c in conversations if c["id"] == turn["conversation_id"])
        if not convo["messages"][-1]["content"].strip():
            raise RuntimeError("Model returned no text")
        events.append("chat returned text")
        if not args.without_drive:
            if not control.remote("POST", "sync")["ok"]:
                raise RuntimeError("Drive sync failed")
            events.append("Drive snapshots written")
        control.remote("POST", "unload")
        events.append("model unloaded")
        if not args.without_drive:
            control.remote("POST", "prepare-release")
            events.append("Drive upload flush verified")
    finally:
        if control.vault.get("runtime_session"):
            control.provider.release()
            events.append("release verified")
        print(json.dumps({"events": events}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
