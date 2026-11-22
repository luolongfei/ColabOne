"""Authenticated inference gateway. Runs on the GPU, separate from its control API."""

import asyncio
import hmac
import json
import queue
import secrets
import threading
import time
from collections import OrderedDict, deque
from typing import Any

import httpx
from fastapi import Depends, FastAPI, Header, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel, ConfigDict

from common.models import (
    COLABONE_AUTO_API_MODEL_ID,
    colabone_model_owner,
    resolve_colabone_api_model_id,
)
from common.public_api import ColabOnePublicConfig
from server.openai_compat import (
    COMPACTION_SYSTEM_PROMPT,
    MAX_BODY_BYTES,
    MAX_RESPONSE_BYTES,
    ColabOneResponsesStreamTranslator,
    build_compaction_response,
    chat_completion_to_response,
    chunk_transcript_blocks,
    encode_compaction_content,
    estimate_tokens,
    extract_anchor_user_item,
    normalize_chat_request,
    normalize_compact_request,
    normalize_responses_request,
    render_items_for_compaction,
    response_items_to_chat_messages,
)
from server.tunnel import ColabOneTunnel

MAX_STORED_RESPONSES = 64
MAX_REQUEST_HISTORY = 50
_ENDPOINT_BY_KIND = {
    "chat": "/v1/chat/completions",
    "responses": "/v1/responses",
    "compact": "/v1/responses/compact",
}


def _positive_float(val: Any) -> float | None:
    if isinstance(val, bool) or not isinstance(val, (int, float)):
        return None
    f = float(val)
    if f != f or f <= 0 or f == float("inf"):
        return None
    return f


def _nonneg_int(val: Any) -> int | None:
    if isinstance(val, bool) or not isinstance(val, int):
        return None
    return val if val >= 0 else None


def _extract_timings(obj: Any) -> dict[str, Any]:
    if not isinstance(obj, dict):
        return {}
    raw = obj.get("timings")
    if not isinstance(raw, dict):
        return {}
    out: dict[str, Any] = {}
    for key in ("predicted_n", "prompt_n"):
        v = _nonneg_int(raw.get(key))
        if v is not None:
            out[key] = v
    for key in ("predicted_ms", "predicted_per_second", "prompt_ms", "prompt_per_second"):
        v = _positive_float(raw.get(key))
        if v is not None:
            out[key] = v
    return out


def _extract_usage(obj: Any) -> tuple[int | None, int | None]:
    if not isinstance(obj, dict):
        return None, None
    usage = obj.get("usage")
    if not isinstance(usage, dict):
        return None, None
    prompt_tok = _nonneg_int(usage.get("prompt_tokens"))
    if prompt_tok is None:
        prompt_tok = _nonneg_int(usage.get("input_tokens"))
    comp_tok = _nonneg_int(usage.get("completion_tokens"))
    if comp_tok is None:
        comp_tok = _nonneg_int(usage.get("output_tokens"))
    return prompt_tok, comp_tok


def _extract_delta_text(chunk: Any) -> str:
    if not isinstance(chunk, dict):
        return ""
    choices = chunk.get("choices") or []
    if not choices or not isinstance(choices[0], dict):
        return ""
    delta = choices[0].get("delta") or {}
    if not isinstance(delta, dict):
        return ""
    parts: list[str] = []
    for key in ("content", "reasoning_content", "reasoning"):
        text = delta.get(key)
        if isinstance(text, str) and text:
            parts.append(text)
    for tc in delta.get("tool_calls") or []:
        if not isinstance(tc, dict):
            continue
        fn = tc.get("function") or {}
        if isinstance(fn, dict):
            for key in ("name", "arguments"):
                text = fn.get(key)
                if isinstance(text, str) and text:
                    parts.append(text)
    return "".join(parts)


def _extract_completion_text(resp_json: Any) -> str:
    if not isinstance(resp_json, dict):
        return ""
    choices = resp_json.get("choices") or []
    if not choices or not isinstance(choices[0], dict):
        return ""
    msg = choices[0].get("message") or {}
    if not isinstance(msg, dict):
        return ""
    parts: list[str] = []
    for key in ("content", "reasoning_content", "reasoning"):
        text = msg.get(key)
        if isinstance(text, str) and text:
            parts.append(text)
    for tc in msg.get("tool_calls") or []:
        if not isinstance(tc, dict):
            continue
        fn = tc.get("function") or {}
        if isinstance(fn, dict):
            for key in ("name", "arguments"):
                text = fn.get(key)
                if isinstance(text, str) and text:
                    parts.append(text)
    return "".join(parts)


def _compute_token_speed(
    completion_tokens: int | None,
    elapsed_sec: float,
    first_token_elapsed_sec: float | None = None,
    timings: dict[str, Any] | None = None,
) -> float | None:
    if timings:
        pps = _positive_float(timings.get("predicted_per_second"))
        if pps is not None:
            return round(pps, 2)
        pn = _nonneg_int(timings.get("predicted_n"))
        pms = _positive_float(timings.get("predicted_ms"))
        if pn and pms:
            return round(pn / (pms / 1000.0), 2)
    if completion_tokens is None or completion_tokens <= 0:
        return None
    if first_token_elapsed_sec is not None:
        decode_sec = elapsed_sec - first_token_elapsed_sec
        if completion_tokens > 1 and decode_sec >= 0.01:
            return round((completion_tokens - 1) / decode_sec, 2)
    if elapsed_sec > 0:
        return round(completion_tokens / max(elapsed_sec, 0.001), 2)
    return None


def _format_duration_label(duration_ms: int) -> str:
    if duration_ms < 1000:
        return f"{max(0, duration_ms)}ms"
    return f"{duration_ms / 1000.0:.2f}s"


def _format_request_finish_log(rec: dict[str, Any]) -> str:
    seq = rec.get("seq", 0)
    state = rec.get("state")
    duration = _format_duration_label(int(rec.get("duration_ms") or 0))
    if state == "completed":
        parts = [f"API 请求 #{seq} 完成"]
        tps = rec.get("tokens_per_second")
        if isinstance(tps, (int, float)) and tps > 0:
            prefix = "≈" if rec.get("estimated") else ""
            parts.append(f"{prefix}{tps:.1f} tokens/s")
        comp = rec.get("completion_tokens")
        if isinstance(comp, int) and comp > 0:
            parts.append(f"输出 {comp} tokens")
        ttft = rec.get("ttft_ms")
        if isinstance(ttft, int) and ttft >= 0:
            parts.append(f"首字 {_format_duration_label(ttft)}")
        parts.append(f"耗时 {duration}")
        return " · ".join(parts)
    if state == "cancelled":
        return f"API 请求 #{seq} 已取消 · 耗时 {duration}"
    err = str(rec.get("error") or "").strip()[:120]
    suffix = f" · {err}" if err else ""
    return f"API 请求 #{seq} 失败 · 耗时 {duration}{suffix}"


class ColabOneChatRequest(BaseModel):
    model_config = ConfigDict(extra="allow", allow_inf_nan=False)
    model: str
    messages: list[dict[str, Any]]
    stream: bool = False


class ColabOnePublicGateway:
    def __init__(self, runtime):
        self.runtime = runtime
        self.enabled, self.key = False, ""
        self.phase, self.mode, self.url, self.error = "stopped", "named", "", ""
        self.instance = secrets.token_hex(16)
        self.checked_at = 0
        self.logs, self.log_seq = deque(maxlen=100), 0
        self.request_history: deque[dict[str, Any]] = deque(maxlen=MAX_REQUEST_HISTORY)
        self.jobs = {}
        self.responses: OrderedDict[str, dict[str, Any]] = OrderedDict()
        self.completed, self.failed, self.total = 0, 0, 0
        self.last_request = 0
        self.lock = threading.RLock()
        self.control_lock = threading.Lock()
        self.tunnel = ColabOneTunnel(self)
        self.server = None
        self.server_thread = None

    def note(self, message, level="info"):
        with self.lock:
            self.log_seq += 1
            self.logs.append(
                {
                    "seq": self.log_seq,
                    "time": time.time(),
                    "message": message,
                    "level": level,
                }
            )

    def _serialize_request_history(self) -> tuple[list[dict[str, Any]], float | None, float | None]:
        now_mono = time.monotonic()
        recent: list[dict[str, Any]] = []
        active_tps: float | None = None
        latest_completed_tps: float | None = None
        completed_speeds: list[float] = []
        for rec in reversed(self.request_history):
            item = {k: v for k, v in rec.items() if not k.startswith("_")}
            if item["state"] == "running":
                started_mono = rec.get("_started_mono")
                if isinstance(started_mono, (int, float)):
                    item["duration_ms"] = max(0, int(round((now_mono - started_mono) * 1000)))
                tps = item.get("tokens_per_second")
                if active_tps is None and isinstance(tps, (int, float)) and tps > 0:
                    active_tps = round(float(tps), 1)
            elif item["state"] == "completed":
                tps = item.get("tokens_per_second")
                if isinstance(tps, (int, float)) and tps > 0:
                    val = round(float(tps), 1)
                    if latest_completed_tps is None:
                        latest_completed_tps = val
                    completed_speeds.append(float(tps))
            recent.append(item)
        last_tps = active_tps if active_tps is not None else latest_completed_tps
        avg_tps = (
            round(sum(completed_speeds) / len(completed_speeds), 1)
            if completed_speeds
            else None
        )
        return recent, last_tps, avg_tps

    def snapshot(self):
        with self.lock:
            jobs = list(self.jobs.values())
            recent_requests, last_tps, avg_tps = self._serialize_request_history()
            return {
                "state": self.phase,
                "enabled": self.enabled,
                "mode": "named",
                "url": self.url if self.enabled else "",
                "error": self.error,
                "streaming": True,
                "checked_at": self.checked_at,
                "requests": self.total,
                "active": sum(j["task"].state == "running" for j in jobs),
                "completed": self.completed
                + sum(j["task"].state == "completed" for j in jobs),
                "failed": self.failed
                + sum(j["task"].state in ("failed", "cancelled") for j in jobs),
                "last_request": self.last_request,
                "last_tokens_per_second": last_tps,
                "avg_tokens_per_second": avg_tps,
                "recent_requests": recent_requests,
                "logs": list(self.logs),
            }

    def serve(self, port=7861):
        if self.server_thread and self.server_thread.is_alive():
            return
        import socket
        import uvicorn

        # Bind before starting the thread: a port collision must not be mistaken for readiness.
        sock = socket.socket()
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(("127.0.0.1", port))
        self.port = sock.getsockname()[1]
        sock.listen(128)
        config = uvicorn.Config(
            create_colabone_public_app(self),
            host="127.0.0.1",
            port=7861,
            access_log=False,
            log_level="warning",
            limit_concurrency=32,
            timeout_graceful_shutdown=5,
        )
        self.server = uvicorn.Server(config)
        self.server_thread = threading.Thread(
            target=self.server.run, kwargs={"sockets": [sock]}, daemon=True
        )
        self.server_thread.start()
        deadline = time.monotonic() + 5
        while not self.server.started:
            if not self.server_thread.is_alive() or time.monotonic() > deadline:
                self.server.should_exit = True
                sock.close()
                raise RuntimeError("远端 API 网关未能启动")
            time.sleep(0.05)

    def start(self, value):
        config = ColabOnePublicConfig.model_validate(value)
        with self.control_lock:
            if self.enabled or (self.tunnel.thread and self.tunnel.thread.is_alive()):
                raise ValueError("请先关闭当前 API 服务，再更换配置")
            with self.runtime.lock:
                if self.runtime.quiescing:
                    raise ValueError("实例正在释放")
                self.runtime.touch(180)
            self.serve()
            self.key, self.mode = config.api_key, "named"
            self.enabled, self.phase, self.url, self.error = True, "starting", "", ""
            self.note("正在开启远端 API · 固定域名")
            self.tunnel.start(config)
        return self.snapshot()

    def cancel_jobs(self):
        with self.lock:
            for job in self.jobs.values():
                if job["task"].state == "running":
                    job["task"].request_cancel()

    def stop(self):
        with self.control_lock:
            self.enabled, self.url, self.phase = False, "", "stopping"
            self.cancel_jobs()
            self.tunnel.stop()
            with self.lock:
                self.responses.clear()
            self.phase, self.error, self.key = "stopped", "", ""
            self.note("对外 API 已关闭，远端模型仍保留在显存中")
        return self.snapshot()

    def shutdown(self):
        try:
            self.stop()
        finally:
            if self.server:
                self.server.should_exit = True
            if self.server_thread:
                self.server_thread.join(timeout=7)

    def require_model(self, identity):
        from server.runtime import ColabOneConflictError

        if not self.enabled or self.runtime.quiescing:
            raise ColabOneConflictError("API 服务已关闭或实例正在释放")
        loaded_id = self.runtime.loaded
        model = self.runtime.models.get(loaded_id) if loaded_id else None
        if not loaded_id or model is None:
            raise ColabOneConflictError("请先在 ColabOne 加载请求的模型；API 不会自动下载或切换模型")
        if getattr(model, "kind", "chat") != "chat":
            raise ColabOneConflictError("请先在 ColabOne 加载 GGUF 聊天模型")
        public_id = resolve_colabone_api_model_id(model)
        requested = (identity or "").strip()
        if (
            requested != public_id
            and requested != loaded_id
            and requested.lower() != COLABONE_AUTO_API_MODEL_ID
        ):
            raise ColabOneConflictError("请先在 ColabOne 加载请求的模型；API 不会自动下载或切换模型")
        return loaded_id, public_id

    def _active_context_length(self, model_id: str) -> int:
        with self.runtime.lock:
            opts = getattr(self.runtime, "active_options", None) or {}
            ctx = opts.get("context_length")
            if not ctx:
                model = self.runtime.models.get(model_id)
                ctx = getattr(model, "context_length", None) if model else None
            return int(ctx) if isinstance(ctx, int) and ctx >= 512 else 8192

    def _store_response(
        self, response_obj: dict[str, Any], input_items: list[dict[str, Any]]
    ) -> None:
        rid = response_obj.get("id")
        if not rid:
            return
        combined_items = list(input_items) + list(response_obj.get("output") or [])
        approx_bytes = len(
            json.dumps(
                {"response": response_obj, "items": combined_items},
                ensure_ascii=False,
            ).encode("utf-8")
        )
        max_total_bytes = 32 * 1024 * 1024
        with self.lock:
            self.responses[rid] = {
                "response": response_obj,
                "items": combined_items,
                "_bytes": approx_bytes,
            }
            self.responses.move_to_end(rid)
            total_bytes = sum(
                int(v.get("_bytes") or 0) for v in self.responses.values()
            )
            while len(self.responses) > MAX_STORED_RESPONSES or (
                len(self.responses) > 1 and total_bytes > max_total_bytes
            ):
                _, evicted = self.responses.popitem(last=False)
                total_bytes -= int(evicted.get("_bytes") or 0)

    def get_response(self, response_id: str) -> dict[str, Any]:
        with self.lock:
            entry = self.responses.get(response_id)
            if not entry:
                raise KeyError(response_id)
            return entry["response"]

    def delete_response(self, response_id: str) -> dict[str, Any]:
        with self.lock:
            if response_id not in self.responses:
                raise KeyError(response_id)
            del self.responses[response_id]
        return {"id": response_id, "object": "response.deleted", "deleted": True}

    def _responses_snapshot(self) -> dict[str, dict[str, Any]]:
        with self.lock:
            return dict(self.responses)

    @staticmethod
    def _init_slot_metric(
        kind: str, model: str, stream: bool
    ) -> tuple[dict[str, Any], dict[str, Any]]:
        now = time.time()
        started_mono = time.monotonic()
        metric: dict[str, Any] = {
            "seq": 0,
            "id": "",
            "kind": kind,
            "endpoint": _ENDPOINT_BY_KIND.get(kind, f"/v1/{kind}"),
            "model": model,
            "stream": bool(stream),
            "state": "running",
            "started_at": now,
            "finished_at": None,
            "duration_ms": 0,
            "ttft_ms": None,
            "prompt_tokens": None,
            "completion_tokens": 0,
            "tokens_per_second": None,
            "prompt_tokens_per_second": None,
            "estimated": False,
            "error": "",
            "_started_mono": started_mono,
            "_registered": False,
            "_finish_logged": False,
        }
        acc: dict[str, Any] = {
            "started_mono": started_mono,
            "first_token_mono": None,
            "delta_chunks": 0,
            "delta_chars": 0,
            "prompt_tokens": None,
            "completion_tokens": None,
            "timings": {},
        }
        return metric, acc

    def _record_stream_delta(
        self, slot: dict[str, Any], chunk: dict[str, Any], now_mono: float
    ) -> None:
        acc = slot.get("acc")
        metric = slot.get("metric")
        if not isinstance(acc, dict) or not isinstance(metric, dict):
            return
        started_mono = acc["started_mono"]
        delta_text = _extract_delta_text(chunk)
        if delta_text:
            if acc["first_token_mono"] is None:
                acc["first_token_mono"] = now_mono
            acc["delta_chunks"] += 1
            acc["delta_chars"] += len(delta_text)
        p_tok, c_tok = _extract_usage(chunk)
        if p_tok is not None:
            acc["prompt_tokens"] = p_tok
        if c_tok is not None:
            acc["completion_tokens"] = c_tok
        timings = _extract_timings(chunk)
        if timings:
            acc["timings"].update(timings)

        elapsed_sec = max(0.0, now_mono - started_mono)
        first_elapsed = (
            max(0.0, acc["first_token_mono"] - started_mono)
            if acc["first_token_mono"] is not None
            else None
        )
        exact_comp = acc["completion_tokens"]
        if exact_comp is None and "predicted_n" in acc["timings"]:
            exact_comp = acc["timings"]["predicted_n"]
        if exact_comp is not None:
            comp_tokens = exact_comp
            estimated = False
        elif acc["delta_chunks"] > 0:
            est_chars = (acc["delta_chars"] + 3) // 4
            comp_tokens = max(acc["delta_chunks"], est_chars) if acc["delta_chunks"] == 1 and acc["delta_chars"] > 12 else acc["delta_chunks"]
            estimated = True
        else:
            comp_tokens = 0
            estimated = False

        tps = _compute_token_speed(
            comp_tokens if comp_tokens > 0 else None,
            elapsed_sec,
            first_elapsed,
            acc["timings"] or None,
        )
        prompt_tok = acc["prompt_tokens"]
        if prompt_tok is None and "prompt_n" in acc["timings"]:
            prompt_tok = acc["timings"]["prompt_n"]
        prompt_tps = _positive_float(acc["timings"].get("prompt_per_second"))
        if prompt_tps is not None:
            prompt_tps = round(prompt_tps, 1)

        with self.lock:
            metric["duration_ms"] = max(0, int(round(elapsed_sec * 1000)))
            if first_elapsed is not None:
                metric["ttft_ms"] = max(0, int(round(first_elapsed * 1000)))
            if prompt_tok is not None:
                metric["prompt_tokens"] = prompt_tok
            metric["completion_tokens"] = comp_tokens
            if tps is not None:
                metric["tokens_per_second"] = round(tps, 1)
            if prompt_tps is not None:
                metric["prompt_tokens_per_second"] = prompt_tps
            metric["estimated"] = estimated

    def _finalize_request(
        self,
        slot: dict[str, Any],
        *,
        state: str,
        error: str = "",
        response_obj: dict[str, Any] | None = None,
        usage_override: dict[str, int] | None = None,
        timings_override: dict[str, Any] | None = None,
        fallback_text: str = "",
        fallback_prompt_tokens: int | None = None,
    ) -> None:
        metric = slot.get("metric")
        acc = slot.get("acc")
        if not isinstance(metric, dict) or not isinstance(acc, dict):
            return
        now_mono = time.monotonic()
        elapsed_sec = max(0.0, now_mono - acc["started_mono"])
        first_elapsed = (
            max(0.0, acc["first_token_mono"] - acc["started_mono"])
            if acc["first_token_mono"] is not None
            else None
        )

        timings = dict(acc.get("timings") or {})
        if response_obj is not None:
            timings.update(_extract_timings(response_obj))
        if timings_override:
            timings.update(timings_override)

        prompt_tok = acc.get("prompt_tokens")
        comp_tok = acc.get("completion_tokens")
        if response_obj is not None:
            rp, rc = _extract_usage(response_obj)
            if rp is not None:
                prompt_tok = rp
            if rc is not None:
                comp_tok = rc
        if usage_override:
            up, uc = _extract_usage({"usage": usage_override})
            if up is not None:
                prompt_tok = up
            if uc is not None:
                comp_tok = uc

        if prompt_tok is None and "prompt_n" in timings:
            prompt_tok = timings["prompt_n"]
        if prompt_tok is None and fallback_prompt_tokens is not None:
            prompt_tok = fallback_prompt_tokens

        estimated = False
        if comp_tok is None and "predicted_n" in timings:
            comp_tok = timings["predicted_n"]
        if comp_tok is None:
            if acc.get("delta_chunks", 0) > 0:
                est_chars = (acc.get("delta_chars", 0) + 3) // 4
                comp_tok = max(acc["delta_chunks"], est_chars) if acc["delta_chunks"] == 1 and acc.get("delta_chars", 0) > 12 else acc["delta_chunks"]
                estimated = True
            elif fallback_text:
                comp_tok = estimate_tokens(fallback_text)
                estimated = True
            elif state == "completed":
                comp_tok = 0

        tps = (
            _compute_token_speed(
                comp_tok,
                elapsed_sec,
                first_elapsed,
                timings or None,
            )
            if (comp_tok and comp_tok > 0) or timings
            else None
        )
        if timings and (
            _positive_float(timings.get("predicted_per_second")) is not None
            or (_nonneg_int(timings.get("predicted_n")) and _positive_float(timings.get("predicted_ms")))
        ):
            estimated = False

        prompt_tps = _positive_float(timings.get("prompt_per_second"))
        if prompt_tps is None:
            pn = _nonneg_int(timings.get("prompt_n"))
            pms = _positive_float(timings.get("prompt_ms"))
            if pn and pms:
                prompt_tps = pn / (pms / 1000.0)
        if prompt_tps is not None:
            prompt_tps = round(prompt_tps, 1)

        with self.lock:
            metric["state"] = state
            metric["finished_at"] = time.time()
            metric["duration_ms"] = max(0, int(round(elapsed_sec * 1000)))
            if first_elapsed is not None:
                metric["ttft_ms"] = max(0, int(round(first_elapsed * 1000)))
            metric["prompt_tokens"] = prompt_tok
            metric["completion_tokens"] = comp_tok
            metric["tokens_per_second"] = round(tps, 1) if tps is not None else None
            metric["prompt_tokens_per_second"] = prompt_tps
            metric["estimated"] = bool(estimated)
            metric["error"] = str(error or "")[:160] if state != "completed" else ""
            if metric.get("_registered") and not metric.get("_finish_logged"):
                metric["_finish_logged"] = True
                self.note(
                    _format_request_finish_log(metric),
                    level="error" if state == "failed" else "info",
                )

    def register(self, result, slot):
        # Called under the runtime lock, making enable/model/task ownership atomic.
        with self.lock:
            while len(self.jobs) >= 100:
                old = next(
                    (k for k, v in self.jobs.items() if v["task"].state != "running"),
                    None,
                )
                if old is None:
                    raise RuntimeError("API 任务已满")
                task = self.jobs.pop(old)["task"]
                self.completed += task.state == "completed"
                self.failed += task.state in ("failed", "cancelled")
                self.runtime.tasks.pop(old, None)
            slot["task"] = self.runtime.tasks[result["id"]]
            self.jobs[result["id"]] = slot
            self.total += 1
            self.last_request = time.time()
            display_model = slot.get("public_model") or slot["task"].model_id
            metric = slot.get("metric")
            if not isinstance(metric, dict):
                metric, acc = self._init_slot_metric(
                    slot["kind"], display_model, bool(slot.get("stream", False))
                )
                slot["metric"] = metric
                slot.setdefault("acc", acc)
            metric["seq"] = self.total
            metric["id"] = result["id"]
            metric["model"] = display_model
            metric["_registered"] = True
            self.request_history.append(metric)
            self.note(
                f"API 请求 #{self.total} · {slot['kind']} · 模型 {display_model}"
            )
            if metric["state"] != "running" and not metric.get("_finish_logged"):
                metric["_finish_logged"] = True
                self.note(
                    _format_request_finish_log(metric),
                    level="error" if metric["state"] == "failed" else "info",
                )
        return result["id"]

    async def _enqueue(self, slot: dict[str, Any], task, chunk: bytes) -> None:
        while True:
            try:
                slot["queue"].put_nowait(chunk)
                return
            except queue.Full:
                task.check()
                await asyncio.sleep(0.02)

    async def _post_chat_json(
        self, http: httpx.AsyncClient, task, payload: dict[str, Any]
    ) -> dict[str, Any]:
        async with http.stream(
            "POST",
            "http://127.0.0.1:8081/v1/chat/completions",
            json=payload,
        ) as response:
            if response.status_code != 200:
                raise RuntimeError(f"推理引擎返回 HTTP {response.status_code}")
            data = bytearray()
            async for chunk in response.aiter_bytes():
                task.check()
                data.extend(chunk)
                if len(data) > MAX_RESPONSE_BYTES:
                    raise RuntimeError("推理响应超过大小限制")
            return json.loads(data)

    async def _run_compaction_summary(
        self,
        http: httpx.AsyncClient,
        task,
        model_id: str,
        items: list[dict[str, Any]],
        instructions: str | None,
    ) -> tuple[str, dict[str, int], dict[str, Any]]:
        blocks = render_items_for_compaction(items)
        if not blocks:
            return (
                "No prior conversation history.",
                {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
                {},
            )
        sys_prompt = COMPACTION_SYSTEM_PROMPT
        if isinstance(instructions, str) and instructions.strip():
            trimmed_inst = instructions.strip()[:2000]
            sys_prompt += (
                "\n\nActive session instructions context (for reference, do not copy verbatim):\n"
                + trimmed_inst
            )

        ctx_tokens = self._active_context_length(model_id)
        summary_max_tokens = min(2048, max(256, ctx_tokens // 4))
        reserved_tokens = summary_max_tokens + estimate_tokens(sys_prompt) + 256
        avail_tokens = max(256, ctx_tokens - reserved_tokens)
        char_budget = max(800, int(avail_tokens * 2.8))
        chunks = chunk_transcript_blocks(blocks, char_budget)

        running_summary = ""
        total_in = 0
        total_out = 0
        pred_n = 0
        pred_ms = 0.0
        prompt_n = 0
        prompt_ms = 0.0
        for idx, chunk_text in enumerate(chunks):
            task.check()
            if idx == 0:
                user_prompt = (
                    "Compact the following conversation history into a concise continuation handoff summary:\n\n"
                    + chunk_text
                )
            else:
                user_prompt = (
                    "Update the running continuation summary by incorporating the next segment of conversation history.\n\n"
                    f"[RUNNING_SUMMARY]\n{running_summary}\n\n"
                    f"[NEXT_HISTORY_SEGMENT]\n{chunk_text}"
                )
            compact_payload = {
                "model": model_id,
                "messages": [
                    {"role": "system", "content": sys_prompt},
                    {"role": "user", "content": user_prompt},
                ],
                "stream": False,
                "temperature": 0.2,
                "max_tokens": summary_max_tokens,
            }
            resp_json = await self._post_chat_json(http, task, compact_payload)
            choices = resp_json.get("choices") or []
            msg = (choices[0] or {}).get("message") or {} if choices else {}
            content = msg.get("content")
            if not isinstance(content, str) or not content.strip():
                content = msg.get("reasoning_content") or msg.get("reasoning") or ""
            running_summary = str(content).strip() or running_summary

            raw_usage = resp_json.get("usage") or {}
            in_tok = raw_usage.get("prompt_tokens")
            if not isinstance(in_tok, int) or in_tok < 0:
                in_tok = estimate_tokens(sys_prompt + user_prompt)
            out_tok = raw_usage.get("completion_tokens")
            if not isinstance(out_tok, int) or out_tok < 0:
                out_tok = estimate_tokens(running_summary)
            total_in += in_tok
            total_out += out_tok

            t = _extract_timings(resp_json)
            if t.get("predicted_n") and t.get("predicted_ms"):
                pred_n += int(t["predicted_n"])
                pred_ms += float(t["predicted_ms"])
            if t.get("prompt_n") and t.get("prompt_ms"):
                prompt_n += int(t["prompt_n"])
                prompt_ms += float(t["prompt_ms"])

        if not running_summary:
            running_summary = "Prior conversation compacted."
        agg_timings: dict[str, Any] = {}
        if pred_n > 0 and pred_ms > 0:
            agg_timings["predicted_n"] = pred_n
            agg_timings["predicted_ms"] = pred_ms
            agg_timings["predicted_per_second"] = pred_n / (pred_ms / 1000.0)
        if prompt_n > 0 and prompt_ms > 0:
            agg_timings["prompt_n"] = prompt_n
            agg_timings["prompt_ms"] = prompt_ms
            agg_timings["prompt_per_second"] = prompt_n / (prompt_ms / 1000.0)
        return (
            running_summary,
            {
                "input_tokens": total_in,
                "output_tokens": total_out,
                "total_tokens": total_in + total_out,
            },
            agg_timings,
        )

    def chat(self, body):
        raw = body.model_dump(exclude_none=True) if hasattr(body, "model_dump") else body
        model, stream, payload = normalize_chat_request(raw)
        client_wants_usage = bool(
            stream
            and isinstance(raw, dict)
            and isinstance(raw.get("stream_options"), dict)
            and raw["stream_options"].get("include_usage") is True
        )
        upstream_payload = dict(payload)
        if stream:
            upstream_payload["stream_options"] = {"include_usage": True}
        est_prompt_tokens = estimate_tokens(
            json.dumps(payload.get("messages") or [], ensure_ascii=False)
        )
        metric, acc = self._init_slot_metric("chat", model, stream)
        slot: dict[str, Any] = {
            "kind": "chat",
            "stream": stream,
            "result": None,
            "queue": queue.Queue(maxsize=256),
            "metric": metric,
            "acc": acc,
        }

        def run(task):
            async def consume():
                from server.runtime import ColabOneCancelledError

                loop, current = asyncio.get_running_loop(), asyncio.current_task()
                task.cancel_hook = lambda: loop.call_soon_threadsafe(current.cancel)
                try:
                    task.check()
                    async with asyncio.timeout(600 if stream else 300):
                        async with httpx.AsyncClient(
                            trust_env=False,
                            transport=self.runtime.chat_transport,
                            timeout=httpx.Timeout(300, connect=10),
                        ) as http:
                            if stream:
                                async with http.stream(
                                    "POST",
                                    "http://127.0.0.1:8081/v1/chat/completions",
                                    json=upstream_payload,
                                ) as response:
                                    if response.status_code != 200:
                                        raise RuntimeError(
                                            f"推理引擎返回 HTTP {response.status_code}"
                                        )
                                    finished = False
                                    async for line in response.aiter_lines():
                                        task.check()
                                        if len(line) > 2 * 1024 * 1024:
                                            raise RuntimeError("推理响应超过大小限制")
                                        if not line.startswith("data:"):
                                            continue
                                        raw_data = line[5:].strip()
                                        if raw_data == "[DONE]":
                                            finished = True
                                            await self._enqueue(
                                                slot, task, (line + "\n\n").encode("utf-8")
                                            )
                                            continue
                                        suppress_chunk = False
                                        out_line = line
                                        if raw_data:
                                            try:
                                                chunk = json.loads(raw_data)
                                            except ValueError:
                                                chunk = None
                                            if isinstance(chunk, dict):
                                                self._record_stream_delta(
                                                    slot, chunk, time.monotonic()
                                                )
                                                if (
                                                    not client_wants_usage
                                                    and not (chunk.get("choices") or [])
                                                    and (
                                                        chunk.get("usage") is not None
                                                        or chunk.get("timings") is not None
                                                    )
                                                ):
                                                    suppress_chunk = True
                                                elif (
                                                    "model" in chunk
                                                    and slot.get("public_model")
                                                    and chunk.get("model") != slot["public_model"]
                                                ):
                                                    rewritten = dict(chunk)
                                                    rewritten["model"] = slot["public_model"]
                                                    out_line = "data: " + json.dumps(
                                                        rewritten, ensure_ascii=False
                                                    )
                                        if not suppress_chunk:
                                            await self._enqueue(
                                                slot, task, (out_line + "\n\n").encode("utf-8")
                                            )
                                    if not finished:
                                        raise RuntimeError("推理连接提前中断")
                                self._finalize_request(
                                    slot,
                                    state="completed",
                                    fallback_prompt_tokens=est_prompt_tokens,
                                )
                            else:
                                result_json = await self._post_chat_json(
                                    http, task, upstream_payload
                                )
                                if (
                                    isinstance(result_json, dict)
                                    and "model" in result_json
                                    and slot.get("public_model")
                                ):
                                    result_json["model"] = slot["public_model"]
                                slot["result"] = result_json
                                self._finalize_request(
                                    slot,
                                    state="completed",
                                    response_obj=result_json,
                                    fallback_text=_extract_completion_text(result_json),
                                    fallback_prompt_tokens=est_prompt_tokens,
                                )
                    task.note("外部 API 聊天生成完成")
                except (asyncio.CancelledError, ColabOneCancelledError):
                    self._finalize_request(
                        slot,
                        state="cancelled",
                        error="API 请求已取消",
                        fallback_prompt_tokens=est_prompt_tokens,
                    )
                    raise ColabOneCancelledError("API 请求已取消") from None
                except (TimeoutError, httpx.TimeoutException):
                    msg = "推理超时；请减少 max_tokens，或开启流式调用（stream=true）"
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=msg,
                        fallback_prompt_tokens=est_prompt_tokens,
                    )
                    raise RuntimeError(msg) from None
                except httpx.HTTPError:
                    msg = "无法连接远端推理引擎"
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=msg,
                        fallback_prompt_tokens=est_prompt_tokens,
                    )
                    raise RuntimeError(msg) from None
                except Exception as exc:
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=str(exc) or "推理失败",
                        fallback_prompt_tokens=est_prompt_tokens,
                    )
                    raise
                finally:
                    task.cancel_hook = None

            asyncio.run(consume())

        with self.runtime.lock:
            internal_id, public_id = self.require_model(model)
            upstream_payload["model"] = internal_id
            slot["internal_model"] = internal_id
            slot["public_model"] = public_id
            metric["model"] = public_id
            result = self.runtime.submit("public_chat", internal_id, run)
            return self.register(result, slot)

    def respond(self, raw_body: Any):
        spec = normalize_responses_request(raw_body, self._responses_snapshot())
        stream = spec["stream"]
        metric, acc = self._init_slot_metric("responses", spec["model"], stream)
        slot: dict[str, Any] = {
            "kind": "responses",
            "stream": stream,
            "result": None,
            "queue": queue.Queue(maxsize=512),
            "fail_event": None,
            "metric": metric,
            "acc": acc,
        }

        def run(task):
            async def consume():
                from server.runtime import ColabOneCancelledError

                loop, current = asyncio.get_running_loop(), asyncio.current_task()
                task.cancel_hook = lambda: loop.call_soon_threadsafe(current.cancel)
                translator: ColabOneResponsesStreamTranslator | None = None
                est_in: int | None = None
                try:
                    task.check()
                    async with asyncio.timeout(600 if stream else 300):
                        async with httpx.AsyncClient(
                            trust_env=False,
                            transport=self.runtime.chat_transport,
                            timeout=httpx.Timeout(300, connect=10),
                        ) as http:
                            chat_payload = dict(spec["chat_payload"])
                            est_in = estimate_tokens(
                                json.dumps(chat_payload["messages"], ensure_ascii=False)
                            )
                            prefix_output: list[dict[str, Any]] = []
                            extra_usage: dict[str, int] | None = None
                            stored_input_items = list(spec["input_items"])

                            should_compact = bool(spec["trigger_compaction"]) or (
                                spec["compact_threshold"] is not None
                                and est_in >= spec["compact_threshold"]
                            )
                            if should_compact and spec["input_items"]:
                                summary, extra_usage, _ = await self._run_compaction_summary(
                                    http,
                                    task,
                                    slot.get("internal_model") or spec["model"],
                                    spec["input_items"],
                                    spec["instructions"],
                                )
                                comp_item = {
                                    "id": f"cmp_{secrets.token_hex(12)}",
                                    "type": "compaction",
                                    "encrypted_content": encode_compaction_content(summary),
                                }
                                prefix_output.append(comp_item)
                                compacted_items: list[dict[str, Any]] = []
                                anchor = extract_anchor_user_item(spec["input_items"])
                                if anchor is not None:
                                    compacted_items.append(anchor)
                                compacted_items.append(comp_item)
                                # Keep the trailing user turn if distinct from the anchor so the model answers it.
                                last_item = spec["input_items"][-1]
                                if (
                                    last_item.get("role") == "user"
                                    and last_item.get("type") in (None, "message")
                                    and (
                                        anchor is None
                                        or last_item is not anchor
                                    )
                                ):
                                    compacted_items.append(last_item)
                                compacted_msgs, _ = response_items_to_chat_messages(
                                    compacted_items, spec["instructions"]
                                )
                                if (
                                    compacted_msgs
                                    and compacted_msgs[-1].get("role") == "assistant"
                                ):
                                    compacted_msgs.append(
                                        {
                                            "role": "user",
                                            "content": "Continue with the task based on the compacted summary above.",
                                        }
                                    )
                                chat_payload["messages"] = compacted_msgs
                                stored_input_items = compacted_items
                                est_in = estimate_tokens(
                                    json.dumps(compacted_msgs, ensure_ascii=False)
                                )

                            if stream:
                                translator = ColabOneResponsesStreamTranslator(
                                    spec,
                                    fallback_input_tokens=est_in,
                                    prefix_output=prefix_output,
                                    extra_usage=extra_usage,
                                )
                                for ev in translator.start():
                                    await self._enqueue(slot, task, ev)
                                async with http.stream(
                                    "POST",
                                    "http://127.0.0.1:8081/v1/chat/completions",
                                    json=chat_payload,
                                ) as response:
                                    if response.status_code != 200:
                                        raise RuntimeError(
                                            f"推理引擎返回 HTTP {response.status_code}"
                                        )
                                    finished = False
                                    async for line in response.aiter_lines():
                                        task.check()
                                        if len(line) > 2 * 1024 * 1024:
                                            raise RuntimeError("推理响应超过大小限制")
                                        if not line.startswith("data:"):
                                            continue
                                        raw_data = line[5:].strip()
                                        if not raw_data:
                                            continue
                                        if raw_data == "[DONE]":
                                            finished = True
                                            break
                                        try:
                                            chunk = json.loads(raw_data)
                                        except ValueError:
                                            continue
                                        if isinstance(chunk, dict):
                                            self._record_stream_delta(
                                                slot, chunk, time.monotonic()
                                            )
                                        for ev in translator.feed_chunk(chunk):
                                            await self._enqueue(slot, task, ev)
                                    if not finished:
                                        raise RuntimeError("推理连接提前中断")
                                for ev in translator.finish():
                                    await self._enqueue(slot, task, ev)
                                if spec["store"] and translator.final_response:
                                    self._store_response(
                                        translator.final_response, stored_input_items
                                    )
                                final_usage = (
                                    translator.final_response.get("usage")
                                    if isinstance(translator.final_response, dict)
                                    else None
                                )
                                self._finalize_request(
                                    slot,
                                    state="completed",
                                    usage_override=final_usage
                                    if isinstance(final_usage, dict)
                                    else None,
                                    fallback_prompt_tokens=est_in,
                                )
                            else:
                                chat_json = await self._post_chat_json(
                                    http, task, chat_payload
                                )
                                resp_obj = chat_completion_to_response(
                                    chat_json,
                                    spec,
                                    prefix_output=prefix_output,
                                    extra_usage=extra_usage,
                                    fallback_input_tokens=est_in,
                                )
                                if spec["store"]:
                                    self._store_response(resp_obj, stored_input_items)
                                slot["result"] = resp_obj
                                self._finalize_request(
                                    slot,
                                    state="completed",
                                    response_obj=chat_json,
                                    usage_override=resp_obj.get("usage")
                                    if isinstance(resp_obj.get("usage"), dict)
                                    else None,
                                    fallback_text=_extract_completion_text(chat_json),
                                    fallback_prompt_tokens=est_in,
                                )
                    task.note("外部 API Responses 生成完成")
                except (asyncio.CancelledError, ColabOneCancelledError):
                    if translator is not None:
                        slot["fail_event"] = translator.fail("API 请求已取消")
                    self._finalize_request(
                        slot,
                        state="cancelled",
                        error="API 请求已取消",
                        fallback_prompt_tokens=est_in,
                    )
                    raise ColabOneCancelledError("API 请求已取消") from None
                except (TimeoutError, httpx.TimeoutException):
                    msg = "推理超时；请减少 max_output_tokens，或开启流式调用（stream=true）"
                    if translator is not None:
                        slot["fail_event"] = translator.fail(msg)
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=msg,
                        fallback_prompt_tokens=est_in,
                    )
                    raise RuntimeError(msg) from None
                except httpx.HTTPError:
                    msg = "无法连接远端推理引擎"
                    if translator is not None:
                        slot["fail_event"] = translator.fail(msg)
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=msg,
                        fallback_prompt_tokens=est_in,
                    )
                    raise RuntimeError(msg) from None
                except Exception as exc:
                    if translator is not None:
                        slot["fail_event"] = translator.fail(str(exc) or "推理失败")
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=str(exc) or "推理失败",
                        fallback_prompt_tokens=est_in,
                    )
                    raise
                finally:
                    task.cancel_hook = None

            asyncio.run(consume())

        with self.runtime.lock:
            internal_id, public_id = self.require_model(spec["model"])
            spec["model"] = public_id
            spec["chat_payload"]["model"] = internal_id
            slot["internal_model"] = internal_id
            slot["public_model"] = public_id
            metric["model"] = public_id
            result = self.runtime.submit("public_responses", internal_id, run)
            return self.register(result, slot), stream

    def compact(self, raw_body: Any):
        spec = normalize_compact_request(raw_body, self._responses_snapshot())
        metric, acc = self._init_slot_metric("compact", spec["model"], False)
        slot: dict[str, Any] = {
            "kind": "compact",
            "stream": False,
            "result": None,
            "queue": queue.Queue(maxsize=4),
            "metric": metric,
            "acc": acc,
        }

        def run(task):
            async def consume():
                from server.runtime import ColabOneCancelledError

                loop, current = asyncio.get_running_loop(), asyncio.current_task()
                task.cancel_hook = lambda: loop.call_soon_threadsafe(current.cancel)
                try:
                    task.check()
                    async with asyncio.timeout(300):
                        async with httpx.AsyncClient(
                            trust_env=False,
                            transport=self.runtime.chat_transport,
                            timeout=httpx.Timeout(300, connect=10),
                        ) as http:
                            summary, usage, timings = await self._run_compaction_summary(
                                http,
                                task,
                                slot.get("internal_model") or spec["model"],
                                spec["items"],
                                spec["instructions"],
                            )
                            slot["result"] = build_compaction_response(
                                spec["items"], summary, usage
                            )
                            self._finalize_request(
                                slot,
                                state="completed",
                                usage_override=usage,
                                timings_override=timings or None,
                            )
                    task.note("外部 API 上下文压缩完成")
                except (asyncio.CancelledError, ColabOneCancelledError):
                    self._finalize_request(
                        slot, state="cancelled", error="上下文压缩请求已取消"
                    )
                    raise ColabOneCancelledError("上下文压缩请求已取消") from None
                except (TimeoutError, httpx.TimeoutException):
                    msg = "上下文压缩超时，请稍后重试"
                    self._finalize_request(slot, state="failed", error=msg)
                    raise RuntimeError(msg) from None
                except httpx.HTTPError:
                    msg = "无法连接远端推理引擎"
                    self._finalize_request(slot, state="failed", error=msg)
                    raise RuntimeError(msg) from None
                except Exception as exc:
                    self._finalize_request(
                        slot, state="failed", error=str(exc) or "上下文压缩失败"
                    )
                    raise
                finally:
                    task.cancel_hook = None

            asyncio.run(consume())

        with self.runtime.lock:
            internal_id, public_id = self.require_model(spec["model"])
            slot["internal_model"] = internal_id
            slot["public_model"] = public_id
            metric["model"] = public_id
            result = self.runtime.submit("public_compact", internal_id, run)
            return self.register(result, slot)


class ColabOneBodyLimit:
    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)
        body = bytearray()
        while True:
            message = await receive()
            if message["type"] == "http.disconnect":
                return
            body.extend(message.get("body", b""))
            if len(body) > MAX_BODY_BYTES:
                return await JSONResponse(
                    {
                        "error": {
                            "message": f"请求体最大 {MAX_BODY_BYTES // (1024 * 1024)} MB"
                        }
                    },
                    status_code=413,
                )(scope, receive, send)
            if not message.get("more_body"):
                break
        sent = False

        async def replay():
            nonlocal sent
            if not sent:
                sent = True
                return {"type": "http.request", "body": bytes(body), "more_body": False}
            return await receive()

        return await self.app(scope, replay, send)


def create_colabone_public_app(g):
    from server.runtime import ColabOneConflictError

    app = FastAPI(
        title="ColabOne Inference",
        docs_url=None,
        redoc_url=None,
        openapi_url=None,
    )
    app.add_middleware(ColabOneBodyLimit)
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_methods=["GET", "POST", "DELETE"],
        allow_headers=["*"],
        allow_credentials=False,
    )

    async def authorize(authorization: str = Header(default="")):
        if not g.enabled or not g.key:
            raise HTTPException(503, "API 服务未开启")
        if not hmac.compare_digest(
            authorization.encode(), ("Bearer " + g.key).encode()
        ):
            raise HTTPException(401, "无效的 API Key")

    auth = [Depends(authorize)]

    @app.exception_handler(HTTPException)
    async def http_error(_, exc):
        return JSONResponse(
            {
                "error": {
                    "message": str(exc.detail),
                    "type": "api_error",
                    "code": exc.status_code,
                }
            },
            status_code=exc.status_code,
        )

    @app.exception_handler(ColabOneConflictError)
    async def conflict(_, exc):
        return JSONResponse(
            {"error": {"message": str(exc), "type": "model_busy"}}, status_code=409
        )

    @app.exception_handler(ValueError)
    @app.exception_handler(RequestValidationError)
    async def invalid(_, exc):
        # Do not echo request bodies, prompts or secrets in validation errors.
        return JSONResponse(
            {
                "error": {
                    "message": "请求参数不受支持，请查看 ColabOne「对外 API」中的接口列表与限制",
                    "type": "invalid_request_error",
                }
            },
            status_code=400,
        )

    @app.exception_handler(KeyError)
    async def missing(_, exc):
        return JSONResponse(
            {"error": {"message": "结果不存在或已删除"}}, status_code=404
        )

    async def read_json(request: Request) -> Any:
        try:
            return await request.json()
        except Exception:
            raise ValueError("请求体必须为合法 JSON") from None

    @app.get("/health")
    def health():
        return {"service": "ColabOne inference", "instance": g.instance}

    def _loaded_model_card_and_id() -> tuple[dict[str, Any] | None, str | None]:
        with g.runtime.lock:
            identity = g.runtime.loaded
            model = g.runtime.models.get(identity)
            if not model or getattr(model, "kind", "chat") != "chat":
                return None, None
            return (
                {
                    "id": resolve_colabone_api_model_id(model),
                    "object": "model",
                    "created": 0,
                    "owned_by": colabone_model_owner(model),
                    "kind": "chat",
                },
                identity,
            )

    @app.get("/v1/models", dependencies=auth)
    def models():
        card, _ = _loaded_model_card_and_id()
        return {"object": "list", "data": [card] if card else []}

    @app.get("/v1/models/{model_id:path}", dependencies=auth)
    def model_detail(model_id: str):
        card, loaded_id = _loaded_model_card_and_id()
        if not card:
            raise KeyError(model_id)
        if (
            model_id in (card["id"], loaded_id)
            or model_id.lower() == COLABONE_AUTO_API_MODEL_ID
        ):
            return card
        raise KeyError(model_id)

    async def wait(identity, request, timeout_seconds: int = 305):
        task = g.jobs[identity]["task"]
        try:
            async with asyncio.timeout(timeout_seconds):
                while task.state == "running":
                    if await request.is_disconnected():
                        raise asyncio.CancelledError()
                    await asyncio.sleep(0.05)
        except (TimeoutError, asyncio.CancelledError):
            task.request_cancel()
            raise HTTPException(
                504,
                "等待超时或调用已断开；若为生成接口，可减小输出长度上限或开启流式调用（stream=true）",
            ) from None
        if task.state != "completed":
            raise HTTPException(502, task.error or "任务已取消")

    @app.post("/v1/chat/completions", dependencies=auth)
    async def chat(request: Request):
        raw = await read_json(request)
        identity = g.chat(raw)
        slot = g.jobs[identity]
        is_stream = bool(isinstance(raw, dict) and raw.get("stream"))
        if not is_stream:
            await wait(identity, request)
            return slot["result"]

        async def stream():
            task = slot["task"]
            try:
                while task.state == "running" or not slot["queue"].empty():
                    try:
                        yield slot["queue"].get_nowait()
                    except queue.Empty:
                        await asyncio.sleep(0.05)
                if task.state != "completed":
                    yield (
                        "data: "
                        + json.dumps(
                            {"error": {"message": task.error or "API 请求已取消"}},
                            ensure_ascii=False,
                        )
                        + "\n\n"
                    ).encode("utf-8")
            finally:
                if task.state == "running":
                    task.request_cancel()

        return StreamingResponse(
            stream(),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    @app.post("/v1/responses/compact", dependencies=auth)
    async def compact_responses(request: Request):
        raw = await read_json(request)
        identity = g.compact(raw)
        slot = g.jobs[identity]
        await wait(identity, request)
        return slot["result"]

    @app.post("/v1/responses", dependencies=auth)
    async def responses(request: Request):
        raw = await read_json(request)
        identity, is_stream = g.respond(raw)
        slot = g.jobs[identity]
        if not is_stream:
            await wait(identity, request)
            return slot["result"]

        async def stream():
            task = slot["task"]
            try:
                while task.state == "running" or not slot["queue"].empty():
                    try:
                        yield slot["queue"].get_nowait()
                    except queue.Empty:
                        await asyncio.sleep(0.05)
                if task.state != "completed":
                    if slot.get("fail_event"):
                        yield slot["fail_event"]
                    else:
                        yield (
                            "event: response.failed\n"
                            "data: "
                            + json.dumps(
                                {
                                    "type": "response.failed",
                                    "error": {
                                        "code": "server_error",
                                        "message": task.error or "API 请求已取消",
                                    },
                                },
                                ensure_ascii=False,
                            )
                            + "\n\n"
                        ).encode("utf-8")
            finally:
                if task.state == "running":
                    task.request_cancel()

        return StreamingResponse(
            stream(),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    @app.get("/v1/responses/{response_id}", dependencies=auth)
    def get_response(response_id: str):
        return g.get_response(response_id)

    @app.delete("/v1/responses/{response_id}", dependencies=auth)
    def delete_response(response_id: str):
        return g.delete_response(response_id)

    return app
