import hashlib
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from concurrent.futures import ThreadPoolExecutor
import pytest
from server.download import (
    Transfer,
    parallel_file,
    metadata_path,
    cached_bytes,
    RangeUnsupported,
)
from server.runtime import Task, Cancelled


@pytest.fixture
def source():
    payload = bytes(range(256)) * 128
    state = {
        "active": 0,
        "peak": 0,
        "ranges": [],
        "fail_once": False,
        "ignore": False,
        "bad_range": False,
    }
    lock = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            start, end = map(int, self.headers["Range"][6:].split("-"))
            with lock:
                state["active"] += 1
                state["peak"] = max(state["peak"], state["active"])
                state["ranges"].append((start, end))
                fail = state["fail_once"]
                state["fail_once"] = False
            try:
                time.sleep(0.02)
                if fail:
                    self.send_error(503)
                    return
                if state["ignore"]:
                    self.send_response(200)
                    self.send_header("Content-Length", str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)
                    return
                self.send_response(206)
                self.send_header(
                    "Content-Range",
                    f"bytes {start + int(state['bad_range'])}-{end}/{len(payload)}",
                )
                self.send_header("Content-Length", str(end - start + 1))
                self.end_headers()
                self.wfile.write(payload[start : end + 1])
            except (BrokenPipeError, ConnectionResetError):
                pass
            finally:
                with lock:
                    state["active"] -= 1

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield f"http://127.0.0.1:{server.server_port}/model", payload, state
    server.shutdown()
    thread.join()
    server.server_close()


def run(source, partial, threads=8, **kwargs):
    url, payload, _ = source
    task = Task("download")
    task.transfer = Transfer(len(payload), threads)
    parallel_file(
        url,
        {},
        partial,
        len(payload),
        hashlib.sha256(payload).hexdigest(),
        task,
        0,
        threads,
        chunk=1024,
        retry_delay=0.001,
        **kwargs,
    )
    return task


@pytest.mark.parametrize("threads", [8, 32])
def test_real_http_ranges_are_parallel_verified_and_bounded(source, tmp_path, threads):
    part = tmp_path / "model.part"
    task = run(source, part, threads)
    assert part.read_bytes() == source[1]
    assert 1 < source[2]["peak"] <= threads
    assert task.transfer.snapshot()["downloaded_bytes"] == len(source[1])
    assert task.transfer.snapshot()["connections"] == 0
    assert task.transfer.snapshot()["speed_bps"] > 0


def test_retry_and_sparse_resume_only_fetch_missing_ranges(source, tmp_path):
    url, payload, state = source
    part = tmp_path / "model.part"
    with part.open("wb") as f:
        f.truncate(len(payload))
        f.seek(1024)
        f.write(payload[1024:2048])
    digest = hashlib.sha256(payload).hexdigest()
    metadata_path(part).write_text(
        json.dumps(
            {"size": len(payload), "sha256": digest, "chunk": 1024, "completed": [1]}
        )
    )
    assert cached_bytes(part, len(payload), digest, 1024) == 1024
    state["fail_once"] = True
    task = run(source, part)
    assert part.read_bytes() == payload
    assert (1024, 2047) not in state["ranges"]
    assert any("重试" in line["message"] for line in task.logs)
    assert task.transfer.resumed == 1024


def test_cancel_retains_completed_ranges_and_can_resume(source, tmp_path):
    part = tmp_path / "model.part"
    url, payload, _ = source
    digest = hashlib.sha256(payload).hexdigest()
    task = Task("download")
    task.transfer = Transfer(len(payload), 8)
    with ThreadPoolExecutor(1) as pool:
        future = pool.submit(
            parallel_file, url, {}, part, len(payload), digest, task, 0, 8, 1024
        )
        deadline = time.monotonic() + 3
        while (
            cached_bytes(part, len(payload), digest, 1024) == 0
            and time.monotonic() < deadline
        ):
            time.sleep(0.005)
        task.request_cancel()
        with pytest.raises(Cancelled):
            future.result(timeout=3)
    completed = cached_bytes(part, len(payload), digest, 1024)
    assert 0 < completed < len(payload)
    task = run(source, part)
    assert task.transfer.resumed == completed
    assert part.read_bytes() == payload


def test_ignored_ranges_signal_fallback_and_never_mark_sparse_file_complete(
    source, tmp_path
):
    source[2]["ignore"] = True
    part = tmp_path / "model.part"
    with pytest.raises(RangeUnsupported):
        run(source, part)
    assert (
        cached_bytes(part, len(source[1]), hashlib.sha256(source[1]).hexdigest(), 1024)
        == 0
    )


def test_wrong_ranges_cannot_be_committed(source, tmp_path):
    source[2]["bad_range"] = True
    part = tmp_path / "model.part"
    with pytest.raises(RuntimeError, match="下载失败"):
        run(source, part, attempts=1)
    assert not json.loads(metadata_path(part).read_text())["completed"]
