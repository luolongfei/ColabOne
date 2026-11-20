"""Remote range downloader, adapted from colab-gpu-llm-service (MIT).

Fixed-size ranges share a bounded thread pool. Only fsynced completed ranges are
recorded for resumption; sparse file length is never treated as downloaded data.
"""

from __future__ import annotations
from collections import deque
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import math
import os
import threading
import time
import requests
from common.files import atomic_json

CHUNK = 64 * 1024**2


class ColabOneRangeUnsupportedError(RuntimeError):
    pass


class ColabOneTransfer:
    def __init__(self, total, threads):
        self.lock = threading.RLock()
        self.started = time.monotonic()
        self.total, self.threads = total, threads
        self.downloaded = self.resumed = self.received = 0
        self.active = 0
        self.phase, self.file = "connecting", ""
        self.index, self.count = 0, 0
        self.samples = deque([(self.started, 0)], maxlen=4096)
        self.last_report = self.started

    def wire(self, size):
        with self.lock:
            self.received += size
            now = time.monotonic()
            self.samples.append((now, self.received))
            while len(self.samples) > 2 and self.samples[1][0] < now - 5:
                self.samples.popleft()

    def snapshot(self):
        with self.lock:
            now = time.monotonic()
            while len(self.samples) > 1 and self.samples[1][0] < now - 5:
                self.samples.popleft()
            t0, b0 = self.samples[0]
            speed = (
                (self.received - b0) / max(now - t0, 0.1)
                if self.phase == "downloading" and now - self.samples[-1][0] < 5
                else 0
            )
            return {
                "downloaded_bytes": self.downloaded,
                "total_bytes": self.total,
                "speed_bps": speed,
                "eta_seconds": max(0, self.total - self.downloaded) / speed
                if speed
                else None,
                "elapsed_seconds": now - self.started,
                "resumed_bytes": self.resumed,
                "threads": self.threads,
                "connections": self.active,
                "phase": self.phase,
                "filename": self.file,
                "file_index": self.index,
                "file_count": self.count,
            }


def metadata_path(partial):
    return partial.with_name(partial.name + ".ranges.json")


def resume_state(partial, size, digest, chunk=CHUNK):
    meta = metadata_path(partial)
    identity = {"size": size, "sha256": digest, "chunk": chunk}
    length = partial.stat().st_size if partial.exists() else 0
    if meta.exists():
        try:
            value = json.loads(meta.read_text(encoding="utf-8"))
            if any(value.get(k) != v for k, v in identity.items()):
                return identity, set()
            finished = {
                i
                for i in value.get("completed", [])
                if type(i) is int
                and 0 <= i < math.ceil(size / chunk)
                and min((i + 1) * chunk, size) <= length
            }
            return identity, finished
        except (ValueError, TypeError, OSError):
            return identity, set()
    # Compatibility with old sequential .part files: keep complete prefix ranges.
    if length > size:
        return identity, set()
    return identity, {
        i
        for i in range(math.ceil(size / chunk))
        if min((i + 1) * chunk, size) <= length
    }


def cached_bytes(partial, size, digest, chunk=CHUNK):
    if not metadata_path(partial).exists():
        return min(partial.stat().st_size, size) if partial.exists() else 0
    _, finished = resume_state(partial, size, digest, chunk)
    return sum(min(chunk, size - i * chunk) for i in finished)


def parallel_file(
    url,
    headers,
    partial,
    size,
    digest,
    task,
    done,
    threads=16,
    chunk=CHUNK,
    get=None,
    attempts=4,
    retry_delay=1,
):
    stats = task.transfer
    identity, finished = resume_state(partial, size, digest, chunk)
    meta = metadata_path(partial)
    lock = threading.RLock()
    abort = threading.Event()
    lengths = {i: min(chunk, size - i * chunk) for i in range(math.ceil(size / chunk))}
    positions = {i: lengths[i] if i in finished else 0 for i in lengths}
    completed_bytes = sum(positions.values())
    with stats.lock:
        stats.resumed += completed_bytes
        stats.downloaded = done + completed_bytes
    task.note(
        f"分块下载：{len(lengths)} 块，每块最多 {chunk // 1024**2} MiB，并发上限 {threads}；复用 {completed_bytes / 1024**2:.1f} MiB"
    )
    partial.parent.mkdir(parents=True, exist_ok=True)
    atomic_json(meta, identity | {"completed": sorted(finished)})
    with partial.open("r+b" if partial.exists() else "wb") as handle:
        handle.truncate(size)
    local = threading.local()
    sessions = []

    def check():
        task.check()
        if abort.is_set():
            raise RuntimeError("其他分块失败，已暂停下载")

    def update(index, amount):
        with lock:
            positions[index] = amount
            downloaded = done + sum(positions.values())
            with stats.lock:
                stats.downloaded = downloaded
                stats.phase = "downloading"
            task.progress = downloaded / stats.total
            now = time.monotonic()
            if now - stats.last_report >= 5:
                stats.last_report = now
                view = stats.snapshot()
                task.note(
                    f"进度 {100 * downloaded / stats.total:.1f}% · {downloaded / 1024**3:.2f}/{stats.total / 1024**3:.2f} GiB · {view['speed_bps'] / 1024**2:.1f} MiB/s · {view['connections']} 个连接"
                )

    def worker(index):
        start, end = index * chunk, min((index + 1) * chunk, size) - 1
        expected = end - start + 1
        if get is None:
            if not hasattr(local, "session"):
                local.session = requests.Session()
                local.session.trust_env = False
                with lock:
                    sessions.append(local.session)
            fetch = local.session.get
        else:
            fetch = get
        for attempt in range(attempts):
            check()
            update(index, 0)
            with stats.lock:
                stats.active += 1
            try:
                with fetch(
                    url,
                    headers=dict(
                        headers,
                        Range=f"bytes={start}-{end}",
                        **{"Accept-Encoding": "identity"},
                    ),
                    stream=True,
                    timeout=(10, 10),
                ) as response:
                    response.raise_for_status()
                    if response.status_code == 200:
                        raise ColabOneRangeUnsupportedError("下载源不支持分段请求，切换为单连接")
                    if (
                        response.status_code != 206
                        or response.headers.get("Content-Range", "").strip()
                        != f"bytes {start}-{end}/{size}"
                    ):
                        raise RuntimeError("分块范围与清单不一致")
                    written = 0
                    with partial.open("r+b") as handle:
                        handle.seek(start)
                        for block in response.iter_content(1024**2):
                            check()
                            if not block:
                                continue
                            if written + len(block) > expected:
                                raise RuntimeError("分块返回了超出范围的数据")
                            handle.write(block)
                            written += len(block)
                            stats.wire(len(block))
                            update(index, written)
                        if written != expected:
                            raise RuntimeError("分块传输未完成")
                        handle.flush()
                        os.fsync(handle.fileno())
                with lock:
                    finished.add(index)
                    atomic_json(meta, identity | {"completed": sorted(finished)})
                return
            except ColabOneRangeUnsupportedError:
                raise
            except Exception as exc:
                check()
                if attempt + 1 >= attempts:
                    raise RuntimeError(
                        f"分块 {index + 1}/{len(lengths)} 下载失败（{type(exc).__name__}），已保留完成的分块，可重试续传"
                    ) from None
                task.note(
                    f"分块 {index + 1}/{len(lengths)} 重试 {attempt + 1}/{attempts - 1}（{type(exc).__name__}）",
                    "warning",
                )
                if task.cancel.wait(retry_delay * 2**attempt):
                    task.check()
            finally:
                with stats.lock:
                    stats.active -= 1

    try:
        with ThreadPoolExecutor(max_workers=threads) as pool:
            futures = [pool.submit(worker, i) for i in lengths if i not in finished]
            try:
                for future in as_completed(futures):
                    future.result()
            except BaseException:
                abort.set()
                for future in futures:
                    future.cancel()
                raise
    finally:
        for session in sessions:
            session.close()
