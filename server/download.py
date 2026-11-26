"""Remote range downloader, adapted from colab-gpu-llm-service (MIT).

Fixed-size ranges share a bounded thread pool. Only fsynced completed ranges are
recorded for resumption; sparse file length is never treated as downloaded data.
"""

from __future__ import annotations

from collections import deque
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, wait
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from http.cookiejar import DefaultCookiePolicy
import json
import math
import os
import random
import socket
import threading
import time
import requests
from common.files import atomic_json

CHUNK = 64 * 1024**2
ITER_CHUNK_BYTES = 256 * 1024
MAX_RETRY_AFTER_SECONDS = 60.0


class ColabOneRangeUnsupportedError(RuntimeError):
    pass


def _map_progress(fraction: float, progress_range: tuple[float, float] | None) -> float:
    clamped = max(0.0, min(1.0, float(fraction)))
    if progress_range is None:
        return clamped
    lo = max(0.0, min(1.0, float(progress_range[0])))
    hi = max(lo, min(1.0, float(progress_range[1])))
    return lo + (hi - lo) * clamped


def _set_task_progress(task, value: float, *, reset: bool = False) -> None:
    numeric = float(value)
    if not math.isfinite(numeric):
        return
    clamped = max(0.0, min(1.0, numeric))
    task_lock = getattr(task, "lock", None)
    if task_lock is not None:
        with task_lock:
            current = getattr(task, "progress", 0.0)
            if (
                reset
                or not isinstance(current, (int, float))
                or isinstance(current, bool)
                or not math.isfinite(current)
                or clamped > current
            ):
                task.progress = clamped
    else:
        current = getattr(task, "progress", 0.0)
        if (
            reset
            or not isinstance(current, (int, float))
            or isinstance(current, bool)
            or not math.isfinite(current)
            or clamped > current
        ):
            task.progress = clamped


def parse_retry_after(
    response, fallback: float, max_delay: float = MAX_RETRY_AFTER_SECONDS
) -> float:
    """Parse an HTTP Retry-After header (seconds or HTTP-date), falling back safely."""
    headers = getattr(response, "headers", None)
    if headers is None or not hasattr(headers, "get"):
        return max(0.0, float(fallback))
    raw = headers.get("Retry-After")
    if raw is None:
        return max(0.0, float(fallback))
    text = str(raw).strip()
    if not text:
        return max(0.0, float(fallback))
    try:
        seconds = float(text)
        if math.isfinite(seconds):
            return max(0.0, min(max_delay, seconds))
    except ValueError:
        pass
    try:
        dt = parsedate_to_datetime(text)
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=timezone.utc)
        delta = (dt - datetime.now(timezone.utc)).total_seconds()
        return max(0.0, min(max_delay, delta))
    except (TypeError, ValueError, OverflowError):
        return max(0.0, float(fallback))


def _abort_response(response) -> None:
    """Best-effort immediate shutdown of an in-flight response socket."""
    raw = getattr(response, "raw", None)
    if raw is None:
        return
    conn = getattr(raw, "_connection", None)
    sock = getattr(conn, "sock", None)
    if sock is None:
        fp = getattr(raw, "_fp", None)
        sock = getattr(getattr(getattr(fp, "fp", None), "raw", None), "_sock", None)
    if sock is not None:
        try:
            sock.shutdown(socket.SHUT_RDWR)
        except Exception:
            pass


class ColabOneTransfer:
    def __init__(
        self,
        total,
        threads,
        progress_range: tuple[float, float] | None = None,
    ):
        self.lock = threading.RLock()
        self.started = time.monotonic()
        self.total, self.threads = total, threads
        self.progress_range = progress_range
        self.downloaded = self.resumed = self.received = 0
        self.active = 0
        self.phase, self.file = "connecting", ""
        self.index, self.count = 0, 0
        self.samples = deque([(self.started, 0)], maxlen=16384)
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
    if partial.is_symlink() or meta.is_symlink():
        return identity, set()
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
    meta = metadata_path(partial)
    if partial.is_symlink() or meta.is_symlink():
        return 0
    if not meta.exists():
        if not partial.exists():
            return 0
        length = partial.stat().st_size
        if length > size:
            return 0
        return length
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
    progress_range: tuple[float, float] | None = None,
):
    stats = task.transfer
    active_range = (
        progress_range
        if progress_range is not None
        else getattr(stats, "progress_range", None)
    )
    meta = metadata_path(partial)
    if partial.is_symlink():
        raise ValueError(f"下载临时文件不能是符号链接：{partial.name}")
    if meta.is_symlink():
        raise ValueError(f"下载元数据文件不能是符号链接：{meta.name}")
    identity, finished = resume_state(partial, size, digest, chunk)
    lock = threading.RLock()
    abort = threading.Event()
    lengths = {i: min(chunk, size - i * chunk) for i in range(math.ceil(size / chunk))}
    positions = {i: lengths[i] if i in finished else 0 for i in lengths}
    completed_bytes = sum(positions.values())
    with stats.lock:
        stats.resumed += completed_bytes
        stats.downloaded = done + completed_bytes
        initial_total = stats.total
    if initial_total > 0 and (done + completed_bytes) > 0:
        _set_task_progress(
            task, _map_progress((done + completed_bytes) / initial_total, active_range)
        )
    task.note(
        f"分块下载：{len(lengths)} 块，每块最多 {chunk // 1024**2} MiB，并发上限 {threads}；复用 {completed_bytes / 1024**2:.1f} MiB"
    )
    partial.parent.mkdir(parents=True, exist_ok=True)
    atomic_json(meta, identity | {"completed": sorted(finished)})
    with partial.open("r+b" if partial.exists() else "wb") as handle:
        handle.truncate(size)
    local = threading.local()
    sessions: list[requests.Session] = []
    active_responses: dict[int, object] = {}

    def abort_active_responses() -> None:
        with lock:
            open_responses = list(active_responses.values())
        for resp in open_responses:
            _abort_response(resp)

    def close_sessions() -> None:
        with lock:
            open_sessions = list(sessions)
            sessions.clear()
        for session in open_sessions:
            try:
                session.close()
            except Exception:
                pass

    def check():
        task.check()
        if abort.is_set():
            raise RuntimeError("其他分块失败，已暂停下载")

    def update(index, amount):
        should_report = False
        with lock:
            positions[index] = amount
            downloaded = done + sum(positions.values())
            with stats.lock:
                stats.downloaded = downloaded
                stats.phase = "downloading"
                total_bytes = stats.total
                now = time.monotonic()
                if now - stats.last_report >= 5:
                    stats.last_report = now
                    should_report = True
        if total_bytes > 0:
            _set_task_progress(
                task, _map_progress(downloaded / total_bytes, active_range)
            )
        if should_report:
            view = stats.snapshot()
            task.note(
                f"进度 {100 * downloaded / max(1, total_bytes):.1f}% · "
                f"{downloaded / 1024**3:.2f}/{total_bytes / 1024**3:.2f} GiB · "
                f"{view['speed_bps'] / 1024**2:.1f} MiB/s · {view['connections']} 个连接"
            )

    def worker(index):
        start, end = index * chunk, min((index + 1) * chunk, size) - 1
        expected = end - start + 1
        if get is None:
            if not hasattr(local, "session"):
                local.session = requests.Session()
                local.session.trust_env = False
                local.session.cookies.set_policy(DefaultCookiePolicy(allowed_domains=[]))
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
                    resp_key = id(response)
                    with lock:
                        active_responses[resp_key] = response
                    try:
                        check()
                        response.raise_for_status()
                        if response.status_code == 200:
                            raise ColabOneRangeUnsupportedError(
                                "下载源不支持分段请求，切换为单连接"
                            )
                        if (
                            response.status_code != 206
                            or response.headers.get("Content-Range", "").strip()
                            != f"bytes {start}-{end}/{size}"
                        ):
                            raise RuntimeError("分块范围与清单不一致")
                        written = 0
                        with partial.open("r+b") as handle:
                            handle.seek(start)
                            for block in response.iter_content(ITER_CHUNK_BYTES):
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
                    finally:
                        with lock:
                            active_responses.pop(resp_key, None)
                with lock:
                    finished.add(index)
                    atomic_json(meta, identity | {"completed": sorted(finished)})
                return
            except ColabOneRangeUnsupportedError:
                raise
            except requests.HTTPError as exc:
                check()
                response_obj = getattr(exc, "response", None)
                status_code = getattr(response_obj, "status_code", None)
                if status_code in (401, 403):
                    raise RuntimeError(
                        f"模型下载鉴权失败（HTTP {status_code}），请在设置中配置有效的 Hugging Face Token"
                    ) from None
                if (
                    status_code in (404, 410)
                    or (
                        status_code is not None
                        and 400 <= status_code < 500
                        and status_code not in (408, 429)
                    )
                    or attempt + 1 >= attempts
                ):
                    raise RuntimeError(
                        f"分块 {index + 1}/{len(lengths)} 下载失败（HTTP {status_code or type(exc).__name__}），已保留完成的分块，可重试续传"
                    ) from None
                if status_code == 429:
                    retry_after = parse_retry_after(
                        response_obj, retry_delay * (2**attempt)
                    )
                    jitter = random.uniform(0.0, min(1.0, retry_after * 0.25))
                    delay = retry_after + jitter
                else:
                    delay = retry_delay * (2**attempt)
                task.note(
                    f"分块 {index + 1}/{len(lengths)} 重试 {attempt + 1}/{attempts - 1}（HTTP {status_code or type(exc).__name__}）",
                    "warning",
                )
                wait_until = time.monotonic() + delay
                while time.monotonic() < wait_until:
                    check()
                    if task.cancel.wait(
                        min(0.1, max(0.0, wait_until - time.monotonic()))
                    ):
                        task.check()
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
                wait_until = time.monotonic() + retry_delay * 2**attempt
                while time.monotonic() < wait_until:
                    check()
                    if task.cancel.wait(
                        min(0.1, max(0.0, wait_until - time.monotonic()))
                    ):
                        task.check()
            finally:
                with stats.lock:
                    stats.active -= 1

    cancel_event = getattr(task, "cancel", None)
    executor = ThreadPoolExecutor(max_workers=threads)
    pending: set = set()
    try:
        pending = {executor.submit(worker, i) for i in lengths if i not in finished}
        try:
            while pending:
                if cancel_event is not None and cancel_event.is_set():
                    abort.set()
                    for future in pending:
                        future.cancel()
                    abort_active_responses()
                    task.check()
                done_futures, pending = wait(
                    pending, timeout=0.1, return_when=FIRST_COMPLETED
                )
                if cancel_event is not None and cancel_event.is_set():
                    abort.set()
                    for future in pending:
                        future.cancel()
                    abort_active_responses()
                    task.check()
                for future in done_futures:
                    future.result()
        except BaseException:
            abort.set()
            for future in pending:
                future.cancel()
            abort_active_responses()
            raise
    finally:
        abort_active_responses()
        executor.shutdown(wait=True)
        close_sessions()
