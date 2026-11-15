"""Private JSON-lines RPC over the desktop child's stdin/stdout, no listening port."""

from __future__ import annotations
import concurrent.futures
import json
import sys
import threading
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from bridge.controller import Controller


def main():
    # The Go bridge always sends UTF-8, including on Windows with a non-UTF-8
    # system locale. Pipes do not inherit the console's Unicode handling.
    for stream in (sys.stdin, sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8")
    if "--self-test" in sys.argv:
        from common.models import ModelSpec
        import keyring, fastapi, colab_cli  # noqa: F401

        presets = json.loads(
            (Path(__file__).resolve().parents[1] / "catalog/presets.json").read_text(
                encoding="utf-8"
            )
        )
        for spec in presets:
            ModelSpec.model_validate(spec)
        print("ColabOne runtime OK; no network or credentials accessed")
        return
    protocol = sys.stdout
    sys.stdout = (
        sys.stderr
    )  # Third-party OAuth/Colab progress must never corrupt the protocol.
    controller = Controller(Path(__file__).resolve().parents[1])
    lock = threading.Lock()

    def handle(request):
        try:
            value = (
                controller.snapshot()
                if request["method"] == "snapshot"
                else controller.command(request["method"], request.get("payload", {}))
            )
            response = {"id": request["id"], "result": value}
        except Exception as exc:
            response = {"id": request["id"], "error": str(exc)[:500]}
        with lock:
            protocol.write(json.dumps(response, ensure_ascii=False) + "\n")
            protocol.flush()

    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        for line in sys.stdin:
            try:
                request = json.loads(line)
                pool.submit(handle, request)
            except ValueError:
                continue
        controller.stop.set()


if __name__ == "__main__":
    main()
