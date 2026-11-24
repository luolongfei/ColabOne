"""Authenticated inference gateway. Runs on the GPU, separate from its control API."""

import asyncio
import hashlib
import hmac
import json
import math
import queue
import re
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
    assemble_compacted_chat_messages,
    build_compaction_response,
    chat_completion_to_response,
    chunk_grouped_transcript_blocks_by_tokens,
    chunk_transcript_blocks_by_tokens,
    clip_text_to_token_budget,
    encode_compaction_content,
    estimate_messages_tokens,
    estimate_tokens,
    extract_anchor_user_item,
    group_chat_messages,
    hash_message_group,
    normalize_chat_request,
    normalize_compact_request,
    normalize_responses_request,
    prune_older_tool_outputs,
    render_chat_messages_for_compaction,
    render_items_for_compaction,
    response_items_to_chat_messages,
    split_groups_for_summary,
    truncate_messages_to_budget,
)
from server.tunnel import ColabOneTunnel

MAX_STORED_RESPONSES = 64
MAX_REQUEST_HISTORY = 50
MAX_COMPACTION_CACHE = 64
MAX_COMPACTION_CACHE_BYTES = 2 * 1024 * 1024
_MIN_TOKEN_SCALE = 0.78
_MAX_TOKEN_SCALE = 2.5
_ENDPOINT_BY_KIND = {
    "chat": "/v1/chat/completions",
    "responses": "/v1/responses",
    "compact": "/v1/responses/compact",
}
_CTX_EXCEEDED_RE = re.compile(
    r"(\d+)\s*tokens?[^\d]{0,80}?(?:context\s*(?:size|length|window)|limit|n_ctx)[^\d]{0,40}?(\d+)",
    re.IGNORECASE,
)


class ColabOneEngineError(RuntimeError):
    """Structured upstream inference-engine error mapped to OpenAI-compatible wire codes."""

    def __init__(
        self,
        message: str,
        *,
        status_code: int = 502,
        error_code: str = "server_error",
        error_type: str = "api_error",
        param: str | None = None,
        prompt_tokens: int | None = None,
        context_length: int | None = None,
    ):
        super().__init__(message)
        self.message = message
        self.status_code = status_code
        self.error_code = error_code
        self.error_type = error_type
        self.param = param
        self.prompt_tokens = prompt_tokens
        self.context_length = context_length

    def to_info(self) -> dict[str, Any]:
        info: dict[str, Any] = {
            "status_code": self.status_code,
            "code": self.error_code,
            "type": self.error_type,
            "message": self.message,
        }
        if self.param:
            info["param"] = self.param
        return info


def _parse_auto_compact_header(raw_value: str | None) -> bool:
    if not isinstance(raw_value, str):
        return True
    cleaned = raw_value.strip().lower()
    return cleaned not in ("0", "false", "off", "no", "none", "disabled")


def _parse_llama_error(
    status_code: int,
    raw_body: bytes | str,
    *,
    default_ctx: int | None = None,
    param: str = "input",
) -> ColabOneEngineError:
    text = (
        raw_body.decode("utf-8", errors="replace")
        if isinstance(raw_body, (bytes, bytearray))
        else str(raw_body or "")
    ).strip()
    err_obj: dict[str, Any] = {}
    if text:
        try:
            parsed = json.loads(text)
            if isinstance(parsed, dict):
                inner = parsed.get("error")
                err_obj = inner if isinstance(inner, dict) else parsed
        except ValueError:
            err_obj = {}

    raw_msg = str(err_obj.get("message") or text[:400]).strip()
    raw_type = str(err_obj.get("type") or "").strip().lower()
    raw_code = str(err_obj.get("code") or "").strip().lower()
    low_msg = raw_msg.lower()

    is_ctx_overflow = (
        raw_type == "exceed_context_size_error"
        or raw_code == "context_length_exceeded"
        or "exceeds the available context size" in low_msg
        or "exceed_context_size" in low_msg
        or "context_length_exceeded" in low_msg
        or "maximum context length" in low_msg
        or "exceeds context window" in low_msg
        or "prompt is too long" in low_msg
    )

    if is_ctx_overflow:
        n_prompt = _nonneg_int(err_obj.get("n_prompt_tokens"))
        n_ctx = _nonneg_int(err_obj.get("n_ctx"))
        if n_prompt is None or n_ctx is None:
            match = _CTX_EXCEEDED_RE.search(raw_msg)
            if match:
                if n_prompt is None:
                    n_prompt = int(match.group(1))
                if n_ctx is None:
                    n_ctx = int(match.group(2))
        if n_ctx is None and isinstance(default_ctx, int) and default_ctx > 0:
            n_ctx = default_ctx
        if n_prompt is not None and n_ctx is not None:
            msg = f"输入上下文过长（{n_prompt} tokens），超过当前模型上下文上限（{n_ctx} tokens）"
        elif n_ctx is not None:
            msg = f"输入上下文超过当前模型上限（{n_ctx} tokens）"
        else:
            msg = "输入上下文超过当前模型上限"
        return ColabOneEngineError(
            msg,
            status_code=400,
            error_code="context_length_exceeded",
            error_type="invalid_request_error",
            param=param,
            prompt_tokens=n_prompt,
            context_length=n_ctx,
        )

    return ColabOneEngineError(
        f"推理引擎返回 HTTP {status_code}",
        status_code=502,
        error_code="server_error",
        error_type="api_error",
    )


_HIGH_REASONING_EFFORTS = frozenset({"high", "xhigh", "ultra", "max", "persistent"})


def _compute_target_input_budget(
    ctx_tokens: int,
    max_tokens_opt: Any = None,
    *,
    reasoning_effort: str | None = None,
    extra_reserve: int = 0,
) -> int:
    """Compute safe prompt token ceiling leaving ample headroom for reasoning/tool output and Jinja framing."""
    effort_low = reasoning_effort.strip().lower() if isinstance(reasoning_effort, str) else ""
    output_reserve = (
        min(max(512, ctx_tokens // 4), 28672)
        if effort_low in _HIGH_REASONING_EFFORTS
        else min(max(512, ctx_tokens // 5), 24576)
    )
    if (
        isinstance(max_tokens_opt, int)
        and not isinstance(max_tokens_opt, bool)
        and 1 <= max_tokens_opt < output_reserve
    ):
        output_reserve = max(256, max_tokens_opt + 128)
    # Apply learned KV-exhaustion boost AFTER base/explicit cap so recovery pressure is never wiped out.
    if isinstance(extra_reserve, int) and extra_reserve > 0:
        output_reserve = min(max(output_reserve, ctx_tokens // 2), output_reserve + extra_reserve)
    # Also keep ~4% margin for tokenizer & chat-template differences.
    framing_margin = max(128, min(6144, ctx_tokens // 25))
    raw_budget = ctx_tokens - output_reserve - framing_margin
    if ctx_tokens >= 1024:
        return max(256, min(ctx_tokens - 256, max(ctx_tokens // 3, raw_budget)))
    return max(256, raw_budget)


def _clamp_upstream_max_tokens(payload: dict[str, Any], ctx_tokens: int) -> int | None:
    """Record explicit client max_tokens and clamp oversized values so llama-server never rejects them."""
    raw_max = payload.get("max_tokens")
    if not isinstance(raw_max, int) or isinstance(raw_max, bool) or raw_max < 1:
        return None
    if raw_max >= ctx_tokens:
        payload["max_tokens"] = max(256, ctx_tokens - 256)
    return raw_max


def _extract_choice_finish_reason(obj: Any) -> str | None:
    if not isinstance(obj, dict):
        return None
    choices = obj.get("choices") or []
    if not choices or not isinstance(choices[0], dict):
        return None
    reason = choices[0].get("finish_reason")
    return reason if isinstance(reason, str) and reason else None


def _is_context_exhausted_length_stop(
    finish_reason: str | None,
    prompt_tokens: int | None,
    completion_tokens: int | None,
    ctx_tokens: int,
    explicit_max_tokens: int | None,
) -> bool:
    """Return True when llama-server hit finish_reason='length' due to KV-cache exhaustion, not a client cap."""
    if finish_reason != "length":
        return False
    if not isinstance(prompt_tokens, int) or not isinstance(completion_tokens, int):
        return False
    if explicit_max_tokens is not None and completion_tokens >= max(1, explicit_max_tokens - 16):
        return False
    return (prompt_tokens + completion_tokens) >= max(256, ctx_tokens - 64)


def _serialize_payload_for_tokenize(
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None,
) -> tuple[str, int]:
    """Flatten chat messages + tools for an optional cold-start llama-server /tokenize probe."""
    parts: list[str] = []
    framing = 12
    for msg in messages:
        if not isinstance(msg, dict):
            continue
        framing += 8
        content = msg.get("content")
        if isinstance(content, str) and content:
            parts.append(content)
        name = msg.get("name")
        if isinstance(name, str) and name:
            parts.append(name)
            framing += 2
        tool_call_id = msg.get("tool_call_id")
        if isinstance(tool_call_id, str) and tool_call_id:
            parts.append(tool_call_id)
            framing += 2
        tool_calls = msg.get("tool_calls")
        if isinstance(tool_calls, list):
            for tc in tool_calls:
                if not isinstance(tc, dict):
                    continue
                framing += 12
                fn = tc.get("function") or {}
                if isinstance(fn, dict):
                    if isinstance(fn.get("name"), str) and fn["name"]:
                        parts.append(fn["name"])
                    args = fn.get("arguments")
                    if isinstance(args, str) and args:
                        parts.append(args)
    if tools:
        framing += 32
        parts.append(json.dumps(tools, ensure_ascii=False, separators=(",", ":")))
    return "\n".join(parts), framing


def _compute_reactive_tight_budget(
    real_ctx: int,
    cur_est_prompt: int,
    real_prompt_tokens: int | None,
    max_tokens_opt: Any = None,
    *,
    reasoning_effort: str | None = None,
) -> int:
    """Compute a strictly tighter estimator-space budget after an engine context-overflow 400.

    Calibrates against llama-server's reported ``n_prompt_tokens`` when available so that
    tokenizer underestimation never causes reactive retry to no-op.
    """
    safe_real = min(
        int(real_ctx * 0.72),
        _compute_target_input_budget(
            real_ctx, max_tokens_opt, reasoning_effort=reasoning_effort
        ),
    )
    candidates = [safe_real]
    if cur_est_prompt > 0:
        candidates.append(int(cur_est_prompt * 0.65))
        if isinstance(real_prompt_tokens, int) and real_prompt_tokens > 0:
            calibrated = int(safe_real * (cur_est_prompt / real_prompt_tokens) * 0.90)
            candidates.append(calibrated)
    return max(256, min(candidates))


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
    for key in ("predicted_n", "prompt_n", "cache_n"):
        v = _nonneg_int(raw.get(key))
        if v is not None:
            out[key] = v
    for key in ("predicted_ms", "predicted_per_second", "prompt_ms", "prompt_per_second"):
        v = _positive_float(raw.get(key))
        if v is not None:
            out[key] = v
    return out


def _authoritative_prompt_tokens(
    usage_prompt_tokens: int | None,
    timings: dict[str, Any] | None,
) -> tuple[int | None, bool]:
    """Return ``(prompt_tokens, allow_downward_calibration)``.

    ``usage.prompt_tokens`` (and ``timings.cache_n + timings.prompt_n`` when both are
    reported) reflects the full prompt length and is safe for bidirectional calibration.
    Bare ``timings.prompt_n`` excludes KV-cached prefix tokens, so it may only protect
    upward and must never drag calibration below 1.0.
    """
    usage_prompt = _nonneg_int(usage_prompt_tokens)
    if usage_prompt is not None:
        return usage_prompt, True
    if isinstance(timings, dict):
        prompt_n = _nonneg_int(timings.get("prompt_n"))
        cache_n = _nonneg_int(timings.get("cache_n"))
        if prompt_n is not None and cache_n is not None:
            return cache_n + prompt_n, True
        if prompt_n is not None:
            return prompt_n, False
    return None, False


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
    if elapsed_sec >= 0:
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
        if rec.get("compacted"):
            orig_in = rec.get("original_prompt_tokens")
            after_in = rec.get("prompt_tokens")
            if (
                isinstance(orig_in, int)
                and isinstance(after_in, int)
                and orig_in > 0
                and after_in > 0
                and orig_in > after_in
            ):
                parts.append(f"自动压缩 {orig_in}→{after_in} tokens")
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
        self.compaction_cache: OrderedDict[str, str] = OrderedDict()
        self._token_scale_by_model: dict[str, float] = {}
        self._output_reserve_boost_by_model: dict[str, int] = {}
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
        now_mono = time.perf_counter()
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
                self.compaction_cache.clear()
                self._token_scale_by_model.clear()
                self._output_reserve_boost_by_model.clear()
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
        started_mono = time.perf_counter()
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
            "original_prompt_tokens": None,
            "compacted": False,
            "compaction_stage": "",
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
            "finish_reason": None,
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
        finish_reason = _extract_choice_finish_reason(chunk)
        if finish_reason:
            acc["finish_reason"] = finish_reason
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
        now_mono = time.perf_counter()
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
            pending_notes = metric.pop("_pending_compaction_notes", None) or []
            for orig_toks, comp_toks, ctx_toks in pending_notes:
                self.note(
                    f"API 请求 #{self.total} 上下文超限（约 {orig_toks} / {ctx_toks} tokens），已自动压缩为约 {comp_toks} tokens",
                    level="warning",
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

    @staticmethod
    async def _read_error_body(response: httpx.Response, limit: int = 65536) -> bytes:
        buf = bytearray()
        async for chunk in response.aiter_bytes():
            buf.extend(chunk)
            if len(buf) >= limit:
                break
        return bytes(buf[:limit])

    def _has_compaction_cache(self) -> bool:
        with self.lock:
            return bool(self.compaction_cache)

    def _compaction_cache_get(self, key: str) -> str | None:
        with self.lock:
            val = self.compaction_cache.get(key)
            if val is not None:
                self.compaction_cache.move_to_end(key)
            return val

    def _compaction_cache_put(self, key: str, summary: str) -> None:
        if not key or not summary:
            return
        with self.lock:
            self.compaction_cache[key] = summary
            self.compaction_cache.move_to_end(key)
            total_bytes = sum(len(v.encode("utf-8")) for v in self.compaction_cache.values())
            while len(self.compaction_cache) > MAX_COMPACTION_CACHE or (
                len(self.compaction_cache) > 1 and total_bytes > MAX_COMPACTION_CACHE_BYTES
            ):
                _, evicted = self.compaction_cache.popitem(last=False)
                total_bytes -= len(evicted.encode("utf-8"))

    @staticmethod
    def _calibration_key(internal_model: str, ctx_tokens: int) -> str:
        return f"{internal_model}:{int(ctx_tokens)}"

    def _has_token_scale(self, cal_key: str) -> bool:
        with self.lock:
            return cal_key in self._token_scale_by_model

    def _get_token_scale(self, cal_key: str) -> float:
        with self.lock:
            return float(self._token_scale_by_model.get(cal_key, 1.0))

    def _observe_real_prompt_tokens(
        self,
        cal_key: str,
        est_tokens: int | None,
        real_prompt_tokens: int | None,
        *,
        allow_downward: bool = True,
    ) -> float:
        if (
            not isinstance(est_tokens, int)
            or isinstance(est_tokens, bool)
            or not isinstance(real_prompt_tokens, int)
            or isinstance(real_prompt_tokens, bool)
            or est_tokens < 256
            or real_prompt_tokens < 256
        ):
            return self._get_token_scale(cal_key)
        raw_ratio = real_prompt_tokens / est_tokens
        if not allow_downward:
            # Non-authoritative lower-bound sources (bare timings.prompt_n excluding cache_n,
            # or non-Jinja /tokenize cold probes) may ONLY push scale upward; they must never
            # erode an existing >1.0 calibration or snap a learned <1.0 calibration back to 1.0.
            upward_obs = min(_MAX_TOKEN_SCALE, raw_ratio)
            with self.lock:
                prev = self._token_scale_by_model.get(cal_key)
                if prev is None:
                    updated = round(max(1.0, upward_obs), 4)
                    if len(self._token_scale_by_model) >= 16:
                        self._token_scale_by_model.pop(next(iter(self._token_scale_by_model)))
                    self._token_scale_by_model[cal_key] = updated
                    return updated
                if upward_obs > prev:
                    updated = round(upward_obs, 4)
                    self._token_scale_by_model[cal_key] = updated
                    return updated
                return float(prev)

        # Authoritative full-prompt observation: ignore sub-1.0 samples from short turns
        # (< 1024 est tokens) or implausible outliers (< 0.50x) without overwriting a
        # previously learned scale.
        if raw_ratio < 1.0 and (est_tokens < 1024 or raw_ratio < 0.50):
            return self._get_token_scale(cal_key)

        adjusted = min(1.0, raw_ratio * 1.03) if raw_ratio < 1.0 else raw_ratio
        observed = max(_MIN_TOKEN_SCALE, min(_MAX_TOKEN_SCALE, adjusted))
        with self.lock:
            prev = self._token_scale_by_model.get(cal_key)
            if prev is None:
                # Jump immediately upward on underestimation; relax smoothly downward from 1.0
                # so a single unusually sparse turn cannot under-calibrate a dense follow-up.
                updated = (
                    observed
                    if observed >= 1.0
                    else max(_MIN_TOKEN_SCALE, 1.0 * 0.70 + observed * 0.30)
                )
                if len(self._token_scale_by_model) >= 16:
                    self._token_scale_by_model.pop(next(iter(self._token_scale_by_model)))
            elif observed >= prev:
                # Jump upward immediately on underestimation so the very next turn is protected.
                updated = observed
            else:
                # Relax smoothly downward toward the real tokenizer ratio.
                updated = max(_MIN_TOKEN_SCALE, prev * 0.70 + observed * 0.30)
            updated = round(updated, 4)
            self._token_scale_by_model[cal_key] = updated
            return updated

    def _get_output_reserve_boost(self, cal_key: str) -> int:
        with self.lock:
            return int(self._output_reserve_boost_by_model.get(cal_key, 0))

    def _record_context_exhaustion_pressure(self, cal_key: str, ctx_tokens: int) -> int:
        step = min(16384, max(2048, ctx_tokens // 8))
        cap = max(2048, ctx_tokens // 3)
        with self.lock:
            prev = int(self._output_reserve_boost_by_model.get(cal_key, 0))
            updated = min(cap, prev + step)
            if len(self._output_reserve_boost_by_model) >= 16 and cal_key not in self._output_reserve_boost_by_model:
                self._output_reserve_boost_by_model.pop(
                    next(iter(self._output_reserve_boost_by_model))
                )
            self._output_reserve_boost_by_model[cal_key] = updated
            self.note(
                f"检测到生成阶段耗尽上下文上限（{ctx_tokens} tokens），已自动提高后续请求预留输出空间（+{updated} tokens）",
                level="warning",
            )
            return updated

    async def _probe_exact_prompt_tokens(
        self,
        http: httpx.AsyncClient,
        messages: list[dict[str, Any]],
        tools: list[dict[str, Any]] | None,
    ) -> int | None:
        """Fail-open cold-start token count probe via llama-server's local /tokenize endpoint.

        Bounds the probed slice to 400K chars (head + tail) and scales proportionally on multi-MB
        payloads so /tokenize stays fast and bounded in memory.
        """
        text, framing = _serialize_payload_for_tokenize(messages, tools)
        if not text:
            return None
        max_sample_chars = 400_000
        if len(text) > max_sample_chars:
            half = max_sample_chars // 2
            sample = text[:half] + "\n" + text[-half:]
            scale_factor = len(text) / len(sample)
        else:
            sample = text
            scale_factor = 1.0
        try:
            resp = await http.post(
                "http://127.0.0.1:8081/tokenize",
                json={"content": sample, "add_special": False},
                timeout=4.0,
            )
            if resp.status_code != 200:
                return None
            data = resp.json()
            tokens = data.get("tokens") if isinstance(data, dict) else None
            if isinstance(tokens, list):
                return int(round(len(tokens) * scale_factor)) + framing
        except Exception:
            return None
        return None

    def _record_compaction_metric(
        self,
        slot: dict[str, Any],
        *,
        original_tokens: int,
        compacted_tokens: int,
        stage: str,
        ctx_tokens: int,
        log_note: bool = True,
    ) -> None:
        metric = slot.get("metric")
        emit_now = False
        seq = 0
        with self.lock:
            if isinstance(metric, dict):
                prev_orig = metric.get("original_prompt_tokens")
                metric["compacted"] = True
                metric["original_prompt_tokens"] = (
                    max(prev_orig, original_tokens)
                    if isinstance(prev_orig, int) and prev_orig > 0
                    else original_tokens
                )
                metric["compaction_stage"] = stage
                if log_note:
                    if metric.get("_registered"):
                        seq = int(metric.get("seq") or 0)
                        emit_now = True
                    else:
                        metric.setdefault("_pending_compaction_notes", []).append(
                            (original_tokens, compacted_tokens, ctx_tokens)
                        )
            elif log_note:
                emit_now = True
            if emit_now:
                if seq > 0:
                    self.note(
                        f"API 请求 #{seq} 上下文超限（约 {original_tokens} / {ctx_tokens} tokens），已自动压缩为约 {compacted_tokens} tokens",
                        level="warning",
                    )
                else:
                    self.note(
                        f"API 请求上下文超限（约 {original_tokens} / {ctx_tokens} tokens），已自动压缩为约 {compacted_tokens} tokens",
                        level="warning",
                    )

    async def _post_chat_json(
        self,
        http: httpx.AsyncClient,
        task,
        payload: dict[str, Any],
        *,
        default_ctx: int | None = None,
        error_param: str = "input",
    ) -> dict[str, Any]:
        async with http.stream(
            "POST",
            "http://127.0.0.1:8081/v1/chat/completions",
            json=payload,
        ) as response:
            if response.status_code != 200:
                err_body = await self._read_error_body(response)
                raise _parse_llama_error(
                    response.status_code,
                    err_body,
                    default_ctx=default_ctx,
                    param=error_param,
                )
            data = bytearray()
            async for chunk in response.aiter_bytes():
                task.check()
                data.extend(chunk)
                if len(data) > MAX_RESPONSE_BYTES:
                    raise RuntimeError("推理响应超过大小限制")
            return json.loads(data)

    async def _run_compaction_blocks(
        self,
        http: httpx.AsyncClient,
        task,
        model_id: str,
        blocks: list[str],
        instructions: str | None,
        *,
        prior_summary: str = "",
        ctx_tokens: int | None = None,
        max_output_tokens: int | None = None,
        keepalive_cb=None,
        grouped_blocks: list[tuple[int, str]] | None = None,
        on_checkpoint=None,
        status_out: dict[str, Any] | None = None,
    ) -> tuple[str, dict[str, int], dict[str, Any]]:
        if not blocks and not grouped_blocks:
            if status_out is not None:
                status_out["all_chunks_clean"] = True
                status_out["had_clean_update"] = False
                status_out["last_clean_checkpoint"] = None
            return (
                prior_summary.strip() or "No prior conversation history.",
                {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
                {},
            )
        configured_ctx = self._active_context_length(model_id)
        effective_ctx = (
            min(configured_ctx, ctx_tokens)
            if isinstance(ctx_tokens, int) and ctx_tokens >= 512
            else configured_ctx
        )
        sys_prompt = COMPACTION_SYSTEM_PROMPT
        if isinstance(instructions, str) and instructions.strip() and effective_ctx >= 1536:
            inst_cap = min(450, max(64, effective_ctx // 12))
            trimmed_inst = clip_text_to_token_budget(instructions.strip(), inst_cap)
            sys_prompt += (
                "\n\nActive session instructions context (for reference, do not copy verbatim):\n"
                + trimmed_inst
            )

        summary_max_tokens = min(1024, max(128, effective_ctx // 6))
        if isinstance(max_output_tokens, int) and max_output_tokens >= 128:
            summary_max_tokens = min(summary_max_tokens, max_output_tokens)
        running_summary_cap = min(1024, max(128, effective_ctx // 8))
        reserved_tokens = (
            summary_max_tokens + running_summary_cap + estimate_tokens(sys_prompt) + 128
        )
        avail_tokens = max(128, effective_ctx - reserved_tokens)
        chunk_token_budget = max(128, int(avail_tokens * 0.85))
        if grouped_blocks is not None:
            chunk_pairs = chunk_grouped_transcript_blocks_by_tokens(
                grouped_blocks, chunk_token_budget
            )
        else:
            chunk_pairs = [
                (chunk_text, None)
                for chunk_text in chunk_transcript_blocks_by_tokens(
                    blocks, chunk_token_budget
                )
            ]

        running_summary = prior_summary.strip()
        total_in = 0
        total_out = 0
        pred_n = 0
        pred_ms = 0.0
        prompt_n = 0
        prompt_ms = 0.0
        use_optional_hints = True
        clean_prefix = True
        all_chunks_clean = True
        had_clean_update = False
        last_clean_checkpoint: int | None = None

        for idx, (chunk_text, checkpoint_idx) in enumerate(chunk_pairs):
            task.check()
            if keepalive_cb is not None:
                await keepalive_cb()
            cur_chunk = chunk_text
            cur_running = (
                clip_text_to_token_budget(running_summary, running_summary_cap)
                if running_summary
                else ""
            )
            cur_max_tokens = summary_max_tokens
            resp_json: dict[str, Any] | None = None
            user_prompt = ""
            shrink_attempts = 0
            while True:
                if not cur_running:
                    user_prompt = (
                        "Compact the following conversation history into a concise continuation handoff summary:\n\n"
                        + cur_chunk
                    )
                else:
                    user_prompt = (
                        "Update the running continuation summary by incorporating the next segment of conversation history.\n\n"
                        f"[RUNNING_SUMMARY]\n{cur_running}\n\n"
                        f"[NEXT_HISTORY_SEGMENT]\n{cur_chunk}"
                    )
                compact_payload: dict[str, Any] = {
                    "model": model_id,
                    "messages": [
                        {"role": "system", "content": sys_prompt},
                        {"role": "user", "content": user_prompt},
                    ],
                    "stream": False,
                    "temperature": 0.2,
                    "max_tokens": cur_max_tokens,
                }
                if use_optional_hints:
                    # Disable chain-of-thought on internal compaction calls and avoid
                    # polluting llama-server's host RAM prompt cache with one-off summaries.
                    compact_payload["reasoning_effort"] = "none"
                    compact_payload["chat_template_kwargs"] = {"enable_thinking": False}
                    compact_payload["cache_prompt"] = False
                try:
                    resp_json = await self._post_chat_json(
                        http,
                        task,
                        compact_payload,
                        default_ctx=effective_ctx,
                        error_param="input",
                    )
                    break
                except ColabOneEngineError as exc:
                    if exc.error_code != "context_length_exceeded":
                        # If a custom GGUF template or future engine rejects optional hints,
                        # fall back cleanly to the minimal OpenAI chat payload once.
                        if use_optional_hints and exc.status_code in (400, 422, 502):
                            use_optional_hints = False
                            continue
                        raise
                    if shrink_attempts >= 2:
                        raise
                    shrink_attempts += 1
                    # Self-heal if CJK/special tokens or smaller real n_ctx caused a chunk to overflow.
                    if (
                        isinstance(exc.context_length, int)
                        and exc.context_length >= 512
                        and exc.context_length < effective_ctx
                    ):
                        effective_ctx = exc.context_length
                    cur_max_tokens = max(128, min(cur_max_tokens // 2, effective_ctx // 6))
                    cur_toks = estimate_tokens(cur_chunk)
                    ratio = 0.55
                    if (
                        isinstance(exc.prompt_tokens, int)
                        and exc.prompt_tokens > 0
                        and effective_ctx > 0
                    ):
                        ratio = min(ratio, max(0.25, (effective_ctx * 0.65) / exc.prompt_tokens))
                    cur_chunk = clip_text_to_token_budget(
                        cur_chunk, max(96, int(cur_toks * ratio))
                    )
                    if cur_running:
                        cur_running = clip_text_to_token_budget(
                            cur_running, max(96, int(estimate_tokens(cur_running) * 0.65))
                        )

            assert resp_json is not None
            choices = resp_json.get("choices") or []
            msg = (choices[0] or {}).get("message") or {} if choices else {}
            content = msg.get("content")
            if isinstance(content, str) and content.strip():
                running_summary = content.strip()
                had_clean_update = True
                if clean_prefix and checkpoint_idx is not None:
                    last_clean_checkpoint = checkpoint_idx
                    if on_checkpoint is not None:
                        on_checkpoint(checkpoint_idx, running_summary)
            else:
                # Model returned no clean summary content (e.g. exhausted tokens inside <think>).
                # Stop publishing checkpoints so we never poison compaction_cache with skipped turns.
                clean_prefix = False
                all_chunks_clean = False
                if not running_summary:
                    fallback = msg.get("reasoning_content") or msg.get("reasoning") or ""
                    if isinstance(fallback, str) and fallback.strip():
                        running_summary = clip_text_to_token_budget(
                            fallback.strip(),
                            min(cur_max_tokens, running_summary_cap),
                        )

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
            all_chunks_clean = False
        if status_out is not None:
            status_out["all_chunks_clean"] = all_chunks_clean
            status_out["had_clean_update"] = had_clean_update
            status_out["last_clean_checkpoint"] = last_clean_checkpoint
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

    async def _run_compaction_summary(
        self,
        http: httpx.AsyncClient,
        task,
        model_id: str,
        items: list[dict[str, Any]],
        instructions: str | None,
        *,
        keepalive_cb=None,
    ) -> tuple[str, dict[str, int], dict[str, Any]]:
        blocks = render_items_for_compaction(items)
        return await self._run_compaction_blocks(
            http,
            task,
            model_id,
            blocks,
            instructions,
            keepalive_cb=keepalive_cb,
        )

    async def _auto_compact_chat_payload(
        self,
        http: httpx.AsyncClient,
        task,
        model_id: str,
        chat_payload: dict[str, Any],
        *,
        ctx_tokens: int,
        target_input_budget: int,
        instructions: str | None = None,
        force: bool = False,
        keepalive_cb=None,
        allow_cache: bool = True,
    ) -> dict[str, Any]:
        """Three-stage automatic context compactor:
        0. Monotonic pinned-checkpoint hysteresis hold (zero GPU overhead, ~99% KV cache hit).
        1. Fast pruning of bulky older tool outputs/arguments (zero GPU overhead).
        2. Rolling semantic summary of older turns with incremental prefix cache.
        3. Extreme single-turn / tail safety clamp so messages always fit inside ctx_tokens.
        """
        raw_msgs = list(chat_payload.get("messages") or [])
        tools = chat_payload.get("tools")
        orig_tokens = estimate_messages_tokens(raw_msgs, tools)
        if not force and orig_tokens <= target_input_budget:
            return {
                "compacted": False,
                "stage": "",
                "original_tokens": orig_tokens,
                "compacted_tokens": orig_tokens,
                "messages": raw_msgs,
                "extra_usage": None,
            }

        leading_system, groups = group_chat_messages(raw_msgs)
        sys_hint = instructions
        if not sys_hint and leading_system and isinstance(leading_system.get("content"), str):
            sys_hint = leading_system["content"]
        # Hash BOTH explicit instructions and merged leading_system content (which may contain
        # project-specific leading developer messages) so distinct sessions never collide.
        leading_sys_text = (
            leading_system["content"].strip()
            if leading_system and isinstance(leading_system.get("content"), str)
            else ""
        )
        sys_key_material = f"{(instructions or '').strip()}\n---\n{leading_sys_text}"
        sys_hash = hashlib.sha256(sys_key_material.encode("utf-8")).hexdigest()[:16]

        all_chain_hashes: list[str] = []
        pinned_idx = -1
        pinned_summary = ""

        def ensure_chain_hashes() -> None:
            nonlocal pinned_idx, pinned_summary
            if all_chain_hashes or len(groups) <= 1:
                return
            running_hash = hashlib.sha256(
                f"{model_id}:{ctx_tokens}:{sys_hash}".encode("utf-8")
            ).hexdigest()
            for grp in groups[:-1]:
                running_hash = hashlib.sha256(
                    f"{running_hash}:{hash_message_group(grp)}".encode("utf-8")
                ).hexdigest()
                all_chain_hashes.append(running_hash)
            if allow_cache:
                for i in range(len(all_chain_hashes) - 1, -1, -1):
                    hit = self._compaction_cache_get(all_chain_hashes[i])
                    if hit is not None:
                        pinned_idx = i
                        pinned_summary = hit
                        break

        # Stage 0: Monotonic pinned-checkpoint hysteresis hold BEFORE Stage 1 pruning.
        # If a conversation is already running under a cached summary checkpoint, hold it
        # monotonically while it fits so Stage 1 never bounces back to raw pruned turns and
        # destroys llama-server's KV prefix cache.
        if allow_cache and len(groups) > 1 and self._has_compaction_cache():
            ensure_chain_hashes()
            if pinned_idx >= 0 and pinned_summary:
                pinned_msgs = assemble_compacted_chat_messages(
                    leading_system, pinned_summary, groups[pinned_idx + 1 :]
                )
                pinned_tokens = estimate_messages_tokens(pinned_msgs, tools)
                if pinned_tokens <= target_input_budget and pinned_tokens < orig_tokens:
                    return {
                        "compacted": True,
                        "stage": "summary",
                        "original_tokens": orig_tokens,
                        "compacted_tokens": pinned_tokens,
                        "messages": pinned_msgs,
                        "extra_usage": None,
                    }

        # Stage 1: fast tool output & argument pruning on older turns.
        pruned_msgs, pruned_changed = prune_older_tool_outputs(
            raw_msgs, tools, target_input_budget
        )
        pruned_tokens = estimate_messages_tokens(pruned_msgs, tools)
        if pruned_changed and pruned_tokens <= target_input_budget:
            return {
                "compacted": True,
                "stage": "prune",
                "original_tokens": orig_tokens,
                "compacted_tokens": pruned_tokens,
                "messages": pruned_msgs,
                "extra_usage": None,
            }

        # Stage 2: rolling semantic summary of older atomic turn groups + hysteresis reset.
        candidate_msgs = pruned_msgs if pruned_changed else raw_msgs
        extra_usage: dict[str, int] | None = None
        had_summary = False

        if len(groups) > 1:
            ensure_chain_hashes()
            # Hysteresis reset (or cold start): split back down to the low-water mark so the next
            # sequence of turns enjoys a wide append-only runway before compaction fires again.
            summary_reserve = (
                max(256, estimate_tokens(pinned_summary) + 64)
                if pinned_summary
                else None
            )
            older_groups, recent_groups = split_groups_for_summary(
                leading_system,
                groups,
                tools,
                target_input_budget,
                summary_reserve=summary_reserve,
            )
            split_at = len(older_groups)
            # Never step backward past an existing pinned checkpoint (would discard cache and trigger
            # a full cold re-summarization). If split_at <= pinned_idx + 1, advance smoothly past
            # pinned_idx + 1 only as far as needed to satisfy target_input_budget while keeping as
            # many recent turns verbatim as fit.
            if pinned_idx >= 0 and split_at <= pinned_idx + 1:
                tail_start = pinned_idx + 1
                if len(groups) - tail_start >= 2:
                    tools_and_sys = estimate_messages_tokens(
                        [leading_system] if leading_system else [], tools
                    )
                    base_toks = tools_and_sys + (summary_reserve or 512)
                    chosen_split = len(groups) - 1
                    for cand_split in range(tail_start + 1, len(groups)):
                        cand_tail_toks = estimate_messages_tokens(
                            [m for grp in groups[cand_split:] for m in grp], None
                        )
                        if base_toks + cand_tail_toks <= target_input_budget:
                            chosen_split = cand_split
                            break
                    split_at = chosen_split
                else:
                    split_at = tail_start
                older_groups = groups[:split_at]
                recent_groups = groups[split_at:]

            older_tokens = (
                estimate_messages_tokens([m for grp in older_groups for m in grp], None)
                if older_groups
                else 0
            )
            # Only invoke LLM summarization when older turns are substantial enough to benefit from compression.
            if older_groups and older_tokens > 320:
                chain_hashes = all_chain_hashes[:split_at]
                cached_idx = -1
                prior_summary = ""
                if allow_cache:
                    for i in range(len(chain_hashes) - 1, -1, -1):
                        hit = self._compaction_cache_get(chain_hashes[i])
                        if hit is not None:
                            cached_idx = i
                            prior_summary = hit
                            break

                summary_text = ""
                should_cache_final = False
                skip_stage2_summary = False
                if cached_idx == len(chain_hashes) - 1 and prior_summary:
                    summary_text = prior_summary
                else:
                    delta_groups = older_groups[cached_idx + 1 :]
                    grouped_blocks: list[tuple[int, str]] = []
                    delta_tokens = 0
                    for offset, grp in enumerate(delta_groups):
                        grp_idx = cached_idx + 1 + offset
                        delta_tokens += estimate_messages_tokens(grp, None)
                        for blk in render_chat_messages_for_compaction(grp):
                            grouped_blocks.append((grp_idx, blk))

                    # Only skip an incremental LLM round-trip for a tiny delta (<= 256 tokens)
                    # when fast tool-output pruning on prior_summary + full tail already fits
                    # target_input_budget with ZERO dropped turns. Otherwise summarize delta_groups
                    # so concise instructions in delta_groups are never silently dropped by Stage 3a.
                    shortcut_pruned_msgs: list[dict[str, Any]] | None = None
                    if prior_summary and delta_tokens <= 256:
                        candidate_tail_msgs = assemble_compacted_chat_messages(
                            leading_system, prior_summary, groups[cached_idx + 1 :]
                        )
                        pruned_cand, pruned_cand_changed = prune_older_tool_outputs(
                            candidate_tail_msgs, tools, target_input_budget
                        )
                        if (
                            pruned_cand_changed
                            and estimate_messages_tokens(pruned_cand, tools) <= target_input_budget
                        ):
                            shortcut_pruned_msgs = pruned_cand

                    if shortcut_pruned_msgs is not None:
                        return {
                            "compacted": True,
                            "stage": "summary",
                            "original_tokens": orig_tokens,
                            "compacted_tokens": estimate_messages_tokens(shortcut_pruned_msgs, tools),
                            "messages": shortcut_pruned_msgs,
                            "extra_usage": None,
                        }

                    if prior_summary:
                        prior_toks = estimate_tokens(prior_summary)
                        summary_out_cap = min(
                            1024,
                            max(256, prior_toks + min(384, max(128, delta_tokens // 3))),
                        )
                        if delta_tokens > 0:
                            summary_out_cap = min(
                                summary_out_cap,
                                max(256, prior_toks + max(0, delta_tokens - 128)),
                            )
                    else:
                        summary_out_cap = min(1024, max(192, older_tokens // 2))

                    # Request slightly tighter real BPE output cap when cap is at ceiling so
                    # normal high-vocab (e.g. Qwen3 ~0.84x) summaries stay under heuristic clip.
                    request_max_tokens = (
                        min(896, summary_out_cap)
                        if summary_out_cap >= 960
                        else summary_out_cap
                    )
                    hard_clip_limit = summary_out_cap

                    # Cumulative token count for each older_groups prefix so intermediate checkpoints
                    # are only cached when they provide genuine net compression over that prefix.
                    prefix_tokens_by_idx: list[int] = []
                    cum_prefix_toks = 0
                    for grp in older_groups:
                        cum_prefix_toks += estimate_messages_tokens(grp, None)
                        prefix_tokens_by_idx.append(cum_prefix_toks)

                    def bound_summary(text: str) -> str:
                        if estimate_tokens(text) > hard_clip_limit:
                            return clip_text_to_token_budget(text, hard_clip_limit)
                        return text

                    def cache_intermediate(grp_idx: int, partial_summary: str) -> None:
                        # Cache strict intermediate checkpoints immediately when they strictly
                        # compress the covered prefix; the final checkpoint chain_hashes[-1]
                        # is cached only after all_clean and net-savings checks pass below.
                        if allow_cache and 0 <= grp_idx < len(chain_hashes) - 1:
                            bounded = bound_summary(partial_summary)
                            prefix_toks = prefix_tokens_by_idx[grp_idx]
                            if prefix_toks > 320 and estimate_tokens(bounded) < prefix_toks:
                                self._compaction_cache_put(chain_hashes[grp_idx], bounded)

                    compact_status: dict[str, Any] = {}
                    summary_text, extra_usage, _ = await self._run_compaction_blocks(
                        http,
                        task,
                        model_id,
                        [blk for _, blk in grouped_blocks],
                        sys_hint,
                        prior_summary=prior_summary,
                        ctx_tokens=ctx_tokens,
                        max_output_tokens=request_max_tokens,
                        keepalive_cb=keepalive_cb,
                        grouped_blocks=grouped_blocks,
                        on_checkpoint=cache_intermediate,
                        status_out=compact_status,
                    )
                    all_clean = bool(compact_status.get("all_chunks_clean"))
                    had_clean_update = bool(compact_status.get("had_clean_update"))
                    last_clean_ckpt = compact_status.get("last_clean_checkpoint")
                    if not prior_summary and not had_clean_update:
                        # Cold-start compaction produced zero clean summaries (only boilerplate or
                        # raw CoT fallback): fall through to Stage 3 truncation on raw/pruned turns
                        # instead of replacing the entire history with empty boilerplate.
                        skip_stage2_summary = True
                    elif not all_clean:
                        # Partial failure: preserve every unsummarized group after the last clean
                        # checkpoint (or prior_summary's cached_idx) so no turns are silently dropped.
                        if isinstance(last_clean_ckpt, int) and last_clean_ckpt > cached_idx:
                            summary_text = bound_summary(summary_text)
                            recent_groups = groups[last_clean_ckpt + 1 :]
                        elif prior_summary:
                            summary_text = prior_summary
                            recent_groups = groups[cached_idx + 1 :]
                        else:
                            skip_stage2_summary = True
                    else:
                        summary_text = bound_summary(summary_text)
                        should_cache_final = allow_cache

                if not skip_stage2_summary:
                    summarized_msgs = assemble_compacted_chat_messages(
                        leading_system, summary_text, recent_groups
                    )
                    sum_tokens = estimate_messages_tokens(summarized_msgs, tools)
                    if sum_tokens < orig_tokens:
                        if should_cache_final:
                            self._compaction_cache_put(chain_hashes[-1], summary_text)
                        had_summary = True
                        candidate_msgs = summarized_msgs
                        if sum_tokens <= target_input_budget:
                            return {
                                "compacted": True,
                                "stage": "summary",
                                "original_tokens": orig_tokens,
                                "compacted_tokens": sum_tokens,
                                "messages": summarized_msgs,
                                "extra_usage": extra_usage,
                            }
                        # Before dropping entire recent turns in Stage 3, prune bulky tool outputs inside recent_groups.
                        pruned_sum_msgs, pruned_sum_changed = prune_older_tool_outputs(
                            summarized_msgs, tools, target_input_budget
                        )
                        if pruned_sum_changed:
                            pruned_sum_tokens = estimate_messages_tokens(pruned_sum_msgs, tools)
                            candidate_msgs = pruned_sum_msgs
                            if pruned_sum_tokens <= target_input_budget:
                                return {
                                    "compacted": True,
                                    "stage": "summary",
                                    "original_tokens": orig_tokens,
                                    "compacted_tokens": pruned_sum_tokens,
                                    "messages": pruned_sum_msgs,
                                    "extra_usage": extra_usage,
                                }

        # Stage 3: extreme clamp for oversized single messages / system prompts / final tool outputs.
        clamped_msgs, clamped_changed = truncate_messages_to_budget(
            candidate_msgs, tools, target_input_budget
        )
        clamped_tokens = estimate_messages_tokens(clamped_msgs, tools)
        return {
            "compacted": bool(clamped_changed or had_summary or pruned_changed),
            "stage": "truncate" if clamped_changed else ("summary" if had_summary else "prune"),
            "original_tokens": orig_tokens,
            "compacted_tokens": clamped_tokens,
            "messages": clamped_msgs,
            "extra_usage": extra_usage,
        }

    def chat(self, body, *, auto_compact: bool = True):
        raw = body.model_dump(exclude_none=True) if hasattr(body, "model_dump") else body
        model, stream, payload = normalize_chat_request(raw)
        raw_reasoning_effort = (
            raw.get("reasoning_effort")
            if isinstance(raw, dict) and isinstance(raw.get("reasoning_effort"), str)
            else payload.get("reasoning_effort")
        )
        client_wants_usage = bool(
            stream
            and isinstance(raw, dict)
            and isinstance(raw.get("stream_options"), dict)
            and raw["stream_options"].get("include_usage") is True
        )
        upstream_payload = dict(payload)
        if stream:
            upstream_payload["stream_options"] = {"include_usage": True}
        est_prompt_tokens = estimate_messages_tokens(
            payload.get("messages") or [], payload.get("tools")
        )
        metric, acc = self._init_slot_metric("chat", model, stream)
        slot: dict[str, Any] = {
            "kind": "chat",
            "stream": stream,
            "result": None,
            "error_info": None,
            "queue": queue.Queue(maxsize=256),
            "metric": metric,
            "acc": acc,
        }

        def run(task):
            async def consume():
                from server.runtime import ColabOneCancelledError

                loop, current = asyncio.get_running_loop(), asyncio.current_task()
                task.cancel_hook = lambda: loop.call_soon_threadsafe(current.cancel)
                cur_est_prompt = est_prompt_tokens
                cal_key = ""
                try:
                    task.check()
                    async with asyncio.timeout(600 if stream else 300):
                        async with httpx.AsyncClient(
                            trust_env=False,
                            transport=self.runtime.chat_transport,
                            timeout=httpx.Timeout(300, connect=10),
                        ) as http:
                            internal_model = slot.get("internal_model") or model
                            ctx_tokens = self._active_context_length(internal_model)
                            effective_ctx = ctx_tokens
                            cal_key = self._calibration_key(internal_model, effective_ctx)
                            explicit_max_tokens = _clamp_upstream_max_tokens(
                                upstream_payload, effective_ctx
                            )
                            target_budget = _compute_target_input_budget(
                                effective_ctx,
                                explicit_max_tokens,
                                reasoning_effort=raw_reasoning_effort,
                                extra_reserve=self._get_output_reserve_boost(cal_key),
                            )

                            if (
                                auto_compact
                                and self.runtime.chat_transport is None
                                and not self._has_token_scale(cal_key)
                                and cur_est_prompt >= int(target_budget * 0.45)
                            ):
                                probed = await self._probe_exact_prompt_tokens(
                                    http,
                                    upstream_payload.get("messages") or [],
                                    upstream_payload.get("tools"),
                                )
                                task.check()
                                if probed is not None:
                                    self._observe_real_prompt_tokens(
                                        cal_key,
                                        cur_est_prompt,
                                        probed,
                                        allow_downward=False,
                                    )

                            scale = self._get_token_scale(cal_key)
                            calibrated_prompt = int(math.ceil(cur_est_prompt * scale))
                            chat_auto_compacted = False

                            async def send_keepalive():
                                if stream:
                                    await self._enqueue(slot, task, b": compacting\n\n")

                            if auto_compact and calibrated_prompt > target_budget:
                                est_target_budget = max(256, int(target_budget / scale))
                                comp_res = await self._auto_compact_chat_payload(
                                    http,
                                    task,
                                    internal_model,
                                    upstream_payload,
                                    ctx_tokens=ctx_tokens,
                                    target_input_budget=est_target_budget,
                                    keepalive_cb=send_keepalive,
                                )
                                if comp_res["compacted"]:
                                    upstream_payload["messages"] = comp_res["messages"]
                                    cur_est_prompt = comp_res["compacted_tokens"]
                                    chat_auto_compacted = True
                                    self._record_compaction_metric(
                                        slot,
                                        original_tokens=max(
                                            calibrated_prompt,
                                            int(math.ceil(comp_res["original_tokens"] * scale)),
                                        ),
                                        compacted_tokens=int(math.ceil(cur_est_prompt * scale)),
                                        stage=comp_res["stage"],
                                        ctx_tokens=ctx_tokens,
                                    )

                            if stream:
                                reactive_retried = False
                                while True:
                                    async with http.stream(
                                        "POST",
                                        "http://127.0.0.1:8081/v1/chat/completions",
                                        json=upstream_payload,
                                    ) as response:
                                        if response.status_code != 200:
                                            err_body = await self._read_error_body(response)
                                            err = _parse_llama_error(
                                                response.status_code,
                                                err_body,
                                                default_ctx=ctx_tokens,
                                                param="messages",
                                            )
                                            if err.error_code == "context_length_exceeded":
                                                real_ctx = err.context_length or effective_ctx
                                                if real_ctx != effective_ctx:
                                                    effective_ctx = real_ctx
                                                    cal_key = self._calibration_key(
                                                        internal_model, effective_ctx
                                                    )
                                                self._observe_real_prompt_tokens(
                                                    cal_key, cur_est_prompt, err.prompt_tokens
                                                )
                                            if (
                                                auto_compact
                                                and not reactive_retried
                                                and err.error_code == "context_length_exceeded"
                                            ):
                                                reactive_retried = True
                                                real_ctx = effective_ctx
                                                real_in = err.prompt_tokens or int(
                                                    math.ceil(
                                                        cur_est_prompt
                                                        * self._get_token_scale(cal_key)
                                                    )
                                                )
                                                tight_budget = _compute_reactive_tight_budget(
                                                    real_ctx,
                                                    cur_est_prompt,
                                                    err.prompt_tokens,
                                                    explicit_max_tokens,
                                                    reasoning_effort=raw_reasoning_effort,
                                                )
                                                comp_res = await self._auto_compact_chat_payload(
                                                    http,
                                                    task,
                                                    internal_model,
                                                    upstream_payload,
                                                    ctx_tokens=real_ctx,
                                                    target_input_budget=tight_budget,
                                                    force=True,
                                                    keepalive_cb=send_keepalive,
                                                    allow_cache=not chat_auto_compacted,
                                                )
                                                if comp_res["compacted"]:
                                                    upstream_payload["messages"] = comp_res["messages"]
                                                    cur_est_prompt = comp_res["compacted_tokens"]
                                                    chat_auto_compacted = True
                                                    new_scale = self._get_token_scale(cal_key)
                                                    self._record_compaction_metric(
                                                        slot,
                                                        original_tokens=max(
                                                            real_in,
                                                            int(
                                                                math.ceil(
                                                                    comp_res["original_tokens"]
                                                                    * new_scale
                                                                )
                                                            ),
                                                        ),
                                                        compacted_tokens=int(
                                                            math.ceil(cur_est_prompt * new_scale)
                                                        ),
                                                        stage=comp_res["stage"],
                                                        ctx_tokens=real_ctx,
                                                    )
                                                    continue
                                            raise err

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
                                                        slot, chunk, time.perf_counter()
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
                                    break
                                real_in, allow_downward = _authoritative_prompt_tokens(
                                    acc.get("prompt_tokens"),
                                    acc.get("timings"),
                                )
                                real_out = acc.get("completion_tokens")
                                if real_out is None:
                                    real_out = acc.get("timings", {}).get("predicted_n")
                                self._observe_real_prompt_tokens(
                                    cal_key,
                                    cur_est_prompt,
                                    real_in,
                                    allow_downward=allow_downward,
                                )
                                if _is_context_exhausted_length_stop(
                                    acc.get("finish_reason"),
                                    real_in,
                                    real_out,
                                    effective_ctx,
                                    explicit_max_tokens,
                                ):
                                    self._record_context_exhaustion_pressure(
                                        cal_key, effective_ctx
                                    )
                                self._finalize_request(
                                    slot,
                                    state="completed",
                                    fallback_prompt_tokens=int(
                                        math.ceil(cur_est_prompt * self._get_token_scale(cal_key))
                                    ),
                                )
                            else:
                                try:
                                    result_json = await self._post_chat_json(
                                        http,
                                        task,
                                        upstream_payload,
                                        default_ctx=effective_ctx,
                                        error_param="messages",
                                    )
                                except ColabOneEngineError as err:
                                    if err.error_code == "context_length_exceeded":
                                        real_ctx = err.context_length or effective_ctx
                                        if real_ctx != effective_ctx:
                                            effective_ctx = real_ctx
                                            cal_key = self._calibration_key(
                                                internal_model, effective_ctx
                                            )
                                        self._observe_real_prompt_tokens(
                                            cal_key, cur_est_prompt, err.prompt_tokens
                                        )
                                    if auto_compact and err.error_code == "context_length_exceeded":
                                        real_ctx = effective_ctx
                                        real_in = err.prompt_tokens or int(
                                            math.ceil(
                                                cur_est_prompt * self._get_token_scale(cal_key)
                                            )
                                        )
                                        tight_budget = _compute_reactive_tight_budget(
                                            real_ctx,
                                            cur_est_prompt,
                                            err.prompt_tokens,
                                            explicit_max_tokens,
                                            reasoning_effort=raw_reasoning_effort,
                                        )
                                        comp_res = await self._auto_compact_chat_payload(
                                            http,
                                            task,
                                            internal_model,
                                            upstream_payload,
                                            ctx_tokens=real_ctx,
                                            target_input_budget=tight_budget,
                                            force=True,
                                            allow_cache=not chat_auto_compacted,
                                        )
                                        if not comp_res["compacted"]:
                                            raise
                                        upstream_payload["messages"] = comp_res["messages"]
                                        cur_est_prompt = comp_res["compacted_tokens"]
                                        chat_auto_compacted = True
                                        new_scale = self._get_token_scale(cal_key)
                                        self._record_compaction_metric(
                                            slot,
                                            original_tokens=max(
                                                real_in,
                                                int(
                                                    math.ceil(
                                                        comp_res["original_tokens"] * new_scale
                                                    )
                                                ),
                                            ),
                                            compacted_tokens=int(
                                                math.ceil(cur_est_prompt * new_scale)
                                            ),
                                            stage=comp_res["stage"],
                                            ctx_tokens=real_ctx,
                                        )
                                        result_json = await self._post_chat_json(
                                            http,
                                            task,
                                            upstream_payload,
                                            default_ctx=real_ctx,
                                            error_param="messages",
                                        )
                                    else:
                                        raise
                                usage_in, real_out = _extract_usage(result_json)
                                res_timings = _extract_timings(result_json)
                                real_in, allow_downward = _authoritative_prompt_tokens(
                                    usage_in, res_timings
                                )
                                if real_out is None:
                                    real_out = res_timings.get("predicted_n")
                                self._observe_real_prompt_tokens(
                                    cal_key,
                                    cur_est_prompt,
                                    real_in,
                                    allow_downward=allow_downward,
                                )
                                if _is_context_exhausted_length_stop(
                                    _extract_choice_finish_reason(result_json),
                                    real_in,
                                    real_out,
                                    effective_ctx,
                                    explicit_max_tokens,
                                ):
                                    new_boost = self._record_context_exhaustion_pressure(
                                        cal_key, effective_ctx
                                    )
                                    if auto_compact:
                                        new_scale = self._get_token_scale(cal_key)
                                        retry_target = _compute_target_input_budget(
                                            effective_ctx,
                                            explicit_max_tokens,
                                            reasoning_effort=raw_reasoning_effort,
                                            extra_reserve=new_boost,
                                        )
                                        est_retry_target = max(
                                            256,
                                            min(
                                                int(retry_target / new_scale),
                                                int(cur_est_prompt * 0.72),
                                            ),
                                        )
                                        comp_res = await self._auto_compact_chat_payload(
                                            http,
                                            task,
                                            internal_model,
                                            upstream_payload,
                                            ctx_tokens=effective_ctx,
                                            target_input_budget=est_retry_target,
                                            force=True,
                                            allow_cache=not chat_auto_compacted,
                                        )
                                        if comp_res["compacted"]:
                                            upstream_payload["messages"] = comp_res["messages"]
                                            cur_est_prompt = comp_res["compacted_tokens"]
                                            chat_auto_compacted = True
                                            self._record_compaction_metric(
                                                slot,
                                                original_tokens=max(
                                                    real_in or 0,
                                                    int(
                                                        math.ceil(
                                                            comp_res["original_tokens"]
                                                            * new_scale
                                                        )
                                                    ),
                                                ),
                                                compacted_tokens=int(
                                                    math.ceil(cur_est_prompt * new_scale)
                                                ),
                                                stage=comp_res["stage"],
                                                ctx_tokens=effective_ctx,
                                            )
                                            result_json = await self._post_chat_json(
                                                http,
                                                task,
                                                upstream_payload,
                                                default_ctx=effective_ctx,
                                                error_param="messages",
                                            )
                                            retry_usage_in, _ = _extract_usage(result_json)
                                            retry_in, retry_allow_down = (
                                                _authoritative_prompt_tokens(
                                                    retry_usage_in,
                                                    _extract_timings(result_json),
                                                )
                                            )
                                            self._observe_real_prompt_tokens(
                                                cal_key,
                                                cur_est_prompt,
                                                retry_in,
                                                allow_downward=retry_allow_down,
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
                                    fallback_prompt_tokens=int(
                                        math.ceil(cur_est_prompt * self._get_token_scale(cal_key))
                                    ),
                                )
                    task.note("外部 API 聊天生成完成")
                except (asyncio.CancelledError, ColabOneCancelledError):
                    self._finalize_request(
                        slot,
                        state="cancelled",
                        error="API 请求已取消",
                        fallback_prompt_tokens=int(
                            math.ceil(cur_est_prompt * self._get_token_scale(cal_key))
                        ),
                    )
                    raise ColabOneCancelledError("API 请求已取消") from None
                except (TimeoutError, httpx.TimeoutException):
                    msg = "推理超时；请减少 max_tokens，或开启流式调用（stream=true）"
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=msg,
                        fallback_prompt_tokens=int(
                            math.ceil(cur_est_prompt * self._get_token_scale(cal_key))
                        ),
                    )
                    raise RuntimeError(msg) from None
                except httpx.HTTPError:
                    msg = "无法连接远端推理引擎"
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=msg,
                        fallback_prompt_tokens=int(
                            math.ceil(cur_est_prompt * self._get_token_scale(cal_key))
                        ),
                    )
                    raise RuntimeError(msg) from None
                except ColabOneEngineError as exc:
                    slot["error_info"] = exc.to_info()
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=exc.message,
                        fallback_prompt_tokens=exc.prompt_tokens
                        or int(
                            math.ceil(cur_est_prompt * self._get_token_scale(cal_key))
                        ),
                    )
                    raise
                except Exception as exc:
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=str(exc) or "推理失败",
                        fallback_prompt_tokens=int(
                            math.ceil(cur_est_prompt * self._get_token_scale(cal_key))
                        ),
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

    def respond(self, raw_body: Any, *, auto_compact: bool = True):
        spec = normalize_responses_request(raw_body, self._responses_snapshot())
        stream = spec["stream"]
        metric, acc = self._init_slot_metric("responses", spec["model"], stream)
        slot: dict[str, Any] = {
            "kind": "responses",
            "stream": stream,
            "result": None,
            "error_info": None,
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
                cal_key = ""
                try:
                    task.check()
                    async with asyncio.timeout(600 if stream else 300):
                        async with httpx.AsyncClient(
                            trust_env=False,
                            transport=self.runtime.chat_transport,
                            timeout=httpx.Timeout(300, connect=10),
                        ) as http:
                            chat_payload = dict(spec["chat_payload"])
                            internal_model = slot.get("internal_model") or spec["model"]
                            ctx_tokens = self._active_context_length(internal_model)
                            raw_est_in = estimate_messages_tokens(
                                chat_payload["messages"], chat_payload.get("tools")
                            )
                            est_in = raw_est_in
                            prefix_output: list[dict[str, Any]] = []
                            extra_usage: dict[str, int] | None = None
                            stored_input_items = list(spec["input_items"])
                            min_wire_input_tokens: int | None = None
                            gateway_auto_compacted = False

                            async def send_keepalive():
                                if stream:
                                    await self._enqueue(slot, task, b": compacting\n\n")

                            should_compact = bool(spec["trigger_compaction"]) or (
                                spec["compact_threshold"] is not None
                                and est_in >= spec["compact_threshold"]
                            )
                            if should_compact and spec["input_items"]:
                                summary, extra_usage, _ = await self._run_compaction_summary(
                                    http,
                                    task,
                                    internal_model,
                                    spec["input_items"],
                                    spec["instructions"],
                                    keepalive_cb=send_keepalive,
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
                                est_in = estimate_messages_tokens(
                                    compacted_msgs, chat_payload.get("tools")
                                )
                                self._record_compaction_metric(
                                    slot,
                                    original_tokens=raw_est_in,
                                    compacted_tokens=est_in,
                                    stage="inline",
                                    ctx_tokens=ctx_tokens,
                                    log_note=False,
                                )

                            effective_ctx = ctx_tokens
                            cal_key = self._calibration_key(internal_model, effective_ctx)
                            raw_reasoning_effort = spec.get("raw_reasoning_effort")
                            explicit_max_tokens = _clamp_upstream_max_tokens(
                                chat_payload, effective_ctx
                            )
                            target_budget = _compute_target_input_budget(
                                effective_ctx,
                                explicit_max_tokens,
                                reasoning_effort=raw_reasoning_effort,
                                extra_reserve=self._get_output_reserve_boost(cal_key),
                            )

                            if (
                                auto_compact
                                and self.runtime.chat_transport is None
                                and not self._has_token_scale(cal_key)
                                and est_in >= int(target_budget * 0.45)
                            ):
                                probed = await self._probe_exact_prompt_tokens(
                                    http,
                                    chat_payload.get("messages") or [],
                                    chat_payload.get("tools"),
                                )
                                task.check()
                                if probed is not None:
                                    self._observe_real_prompt_tokens(
                                        cal_key,
                                        est_in,
                                        probed,
                                        allow_downward=False,
                                    )

                            scale = self._get_token_scale(cal_key)
                            calibrated_est_in = int(math.ceil(est_in * scale))
                            calibrated_raw_in = int(math.ceil(raw_est_in * scale))
                            if auto_compact and calibrated_est_in > target_budget:
                                est_target_budget = max(256, int(target_budget / scale))
                                comp_res = await self._auto_compact_chat_payload(
                                    http,
                                    task,
                                    internal_model,
                                    chat_payload,
                                    ctx_tokens=ctx_tokens,
                                    target_input_budget=est_target_budget,
                                    instructions=spec["instructions"],
                                    keepalive_cb=send_keepalive,
                                )
                                if comp_res["compacted"]:
                                    chat_payload["messages"] = comp_res["messages"]
                                    est_in = comp_res["compacted_tokens"]
                                    gateway_auto_compacted = True
                                    orig_toks = max(
                                        calibrated_raw_in,
                                        int(math.ceil(comp_res["original_tokens"] * scale)),
                                    )
                                    # Report uncompacted context pressure on the wire so Codex CLI
                                    # sees last_token_usage >= auto_compact_limit and triggers native compaction.
                                    min_wire_input_tokens = orig_toks
                                    self._record_compaction_metric(
                                        slot,
                                        original_tokens=orig_toks,
                                        compacted_tokens=int(math.ceil(est_in * scale)),
                                        stage=comp_res["stage"],
                                        ctx_tokens=ctx_tokens,
                                    )

                            if stream:
                                translator = ColabOneResponsesStreamTranslator(
                                    spec,
                                    fallback_input_tokens=int(
                                        math.ceil(est_in * self._get_token_scale(cal_key))
                                    ),
                                    prefix_output=prefix_output,
                                    extra_usage=extra_usage,
                                    min_input_tokens=min_wire_input_tokens,
                                )
                                for ev in translator.start():
                                    await self._enqueue(slot, task, ev)
                                reactive_retried = False
                                while True:
                                    async with http.stream(
                                        "POST",
                                        "http://127.0.0.1:8081/v1/chat/completions",
                                        json=chat_payload,
                                    ) as response:
                                        if response.status_code != 200:
                                            err_body = await self._read_error_body(response)
                                            err = _parse_llama_error(
                                                response.status_code,
                                                err_body,
                                                default_ctx=ctx_tokens,
                                                param="input",
                                            )
                                            if err.error_code == "context_length_exceeded":
                                                real_ctx = err.context_length or effective_ctx
                                                if real_ctx != effective_ctx:
                                                    effective_ctx = real_ctx
                                                    cal_key = self._calibration_key(
                                                        internal_model, effective_ctx
                                                    )
                                                self._observe_real_prompt_tokens(
                                                    cal_key, est_in, err.prompt_tokens
                                                )
                                            if (
                                                auto_compact
                                                and not reactive_retried
                                                and err.error_code == "context_length_exceeded"
                                            ):
                                                reactive_retried = True
                                                real_ctx = effective_ctx
                                                new_scale = self._get_token_scale(cal_key)
                                                real_in = max(
                                                    int(math.ceil(raw_est_in * new_scale)),
                                                    err.prompt_tokens
                                                    or int(math.ceil(est_in * new_scale)),
                                                )
                                                tight_budget = _compute_reactive_tight_budget(
                                                    real_ctx,
                                                    est_in,
                                                    err.prompt_tokens,
                                                    explicit_max_tokens,
                                                    reasoning_effort=raw_reasoning_effort,
                                                )
                                                comp_res = await self._auto_compact_chat_payload(
                                                    http,
                                                    task,
                                                    internal_model,
                                                    chat_payload,
                                                    ctx_tokens=real_ctx,
                                                    target_input_budget=tight_budget,
                                                    instructions=spec["instructions"],
                                                    force=True,
                                                    keepalive_cb=send_keepalive,
                                                    allow_cache=not gateway_auto_compacted,
                                                )
                                                if comp_res["compacted"]:
                                                    chat_payload["messages"] = comp_res["messages"]
                                                    est_in = comp_res["compacted_tokens"]
                                                    gateway_auto_compacted = True
                                                    min_wire_input_tokens = max(
                                                        min_wire_input_tokens or 0, real_in
                                                    )
                                                    compacted_disp = int(
                                                        math.ceil(est_in * new_scale)
                                                    )
                                                    translator.fallback_input_tokens = compacted_disp
                                                    translator.min_input_tokens = (
                                                        min_wire_input_tokens
                                                    )
                                                    self._record_compaction_metric(
                                                        slot,
                                                        original_tokens=real_in,
                                                        compacted_tokens=compacted_disp,
                                                        stage=comp_res["stage"],
                                                        ctx_tokens=real_ctx,
                                                    )
                                                    continue
                                            raise err

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
                                                    slot, chunk, time.perf_counter()
                                                )
                                            for ev in translator.feed_chunk(chunk):
                                                await self._enqueue(slot, task, ev)
                                        if not finished:
                                            raise RuntimeError("推理连接提前中断")
                                    break
                                usage_in, raw_out = _extract_usage(
                                    {"usage": translator.raw_usage}
                                    if isinstance(translator.raw_usage, dict)
                                    else {}
                                )
                                raw_in, allow_downward = _authoritative_prompt_tokens(
                                    usage_in, acc.get("timings")
                                )
                                if raw_out is None:
                                    raw_out = acc.get("timings", {}).get("predicted_n")
                                self._observe_real_prompt_tokens(
                                    cal_key,
                                    est_in,
                                    raw_in,
                                    allow_downward=allow_downward,
                                )
                                if _is_context_exhausted_length_stop(
                                    translator.finish_reason,
                                    raw_in,
                                    raw_out,
                                    effective_ctx,
                                    explicit_max_tokens,
                                ):
                                    self._record_context_exhaustion_pressure(
                                        cal_key, effective_ctx
                                    )
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
                                # Keep GPU-side post-compaction prompt_tokens accurate in desktop telemetry
                                # while reporting uncompacted input_tokens on the wire for Codex auto-compact.
                                metric_usage = (
                                    translator.raw_usage
                                    if gateway_auto_compacted and isinstance(translator.raw_usage, dict)
                                    else (
                                        None
                                        if gateway_auto_compacted
                                        else (final_usage if isinstance(final_usage, dict) else None)
                                    )
                                )
                                self._finalize_request(
                                    slot,
                                    state="completed",
                                    usage_override=metric_usage,
                                    fallback_prompt_tokens=int(
                                        math.ceil(est_in * self._get_token_scale(cal_key))
                                    ),
                                )
                            else:
                                try:
                                    chat_json = await self._post_chat_json(
                                        http,
                                        task,
                                        chat_payload,
                                        default_ctx=effective_ctx,
                                        error_param="input",
                                    )
                                except ColabOneEngineError as err:
                                    if err.error_code == "context_length_exceeded":
                                        real_ctx = err.context_length or effective_ctx
                                        if real_ctx != effective_ctx:
                                            effective_ctx = real_ctx
                                            cal_key = self._calibration_key(
                                                internal_model, effective_ctx
                                            )
                                        self._observe_real_prompt_tokens(
                                            cal_key, est_in, err.prompt_tokens
                                        )
                                    if auto_compact and err.error_code == "context_length_exceeded":
                                        real_ctx = effective_ctx
                                        new_scale = self._get_token_scale(cal_key)
                                        real_in = max(
                                            int(math.ceil(raw_est_in * new_scale)),
                                            err.prompt_tokens
                                            or int(math.ceil(est_in * new_scale)),
                                        )
                                        tight_budget = _compute_reactive_tight_budget(
                                            real_ctx,
                                            est_in,
                                            err.prompt_tokens,
                                            explicit_max_tokens,
                                            reasoning_effort=raw_reasoning_effort,
                                        )
                                        comp_res = await self._auto_compact_chat_payload(
                                            http,
                                            task,
                                            internal_model,
                                            chat_payload,
                                            ctx_tokens=real_ctx,
                                            target_input_budget=tight_budget,
                                            instructions=spec["instructions"],
                                            force=True,
                                            allow_cache=not gateway_auto_compacted,
                                        )
                                        if not comp_res["compacted"]:
                                            raise
                                        chat_payload["messages"] = comp_res["messages"]
                                        est_in = comp_res["compacted_tokens"]
                                        gateway_auto_compacted = True
                                        min_wire_input_tokens = max(
                                            min_wire_input_tokens or 0, real_in
                                        )
                                        self._record_compaction_metric(
                                            slot,
                                            original_tokens=real_in,
                                            compacted_tokens=int(math.ceil(est_in * new_scale)),
                                            stage=comp_res["stage"],
                                            ctx_tokens=real_ctx,
                                        )
                                        chat_json = await self._post_chat_json(
                                            http,
                                            task,
                                            chat_payload,
                                            default_ctx=real_ctx,
                                            error_param="input",
                                        )
                                    else:
                                        raise
                                usage_in, real_out = _extract_usage(chat_json)
                                chat_timings = _extract_timings(chat_json)
                                real_in, allow_downward = _authoritative_prompt_tokens(
                                    usage_in, chat_timings
                                )
                                if real_out is None:
                                    real_out = chat_timings.get("predicted_n")
                                self._observe_real_prompt_tokens(
                                    cal_key,
                                    est_in,
                                    real_in,
                                    allow_downward=allow_downward,
                                )
                                if _is_context_exhausted_length_stop(
                                    _extract_choice_finish_reason(chat_json),
                                    real_in,
                                    real_out,
                                    effective_ctx,
                                    explicit_max_tokens,
                                ):
                                    new_boost = self._record_context_exhaustion_pressure(
                                        cal_key, effective_ctx
                                    )
                                    if auto_compact:
                                        new_scale = self._get_token_scale(cal_key)
                                        retry_target = _compute_target_input_budget(
                                            effective_ctx,
                                            explicit_max_tokens,
                                            reasoning_effort=raw_reasoning_effort,
                                            extra_reserve=new_boost,
                                        )
                                        est_retry_target = max(
                                            256,
                                            min(
                                                int(retry_target / new_scale),
                                                int(est_in * 0.72),
                                            ),
                                        )
                                        comp_res = await self._auto_compact_chat_payload(
                                            http,
                                            task,
                                            internal_model,
                                            chat_payload,
                                            ctx_tokens=effective_ctx,
                                            target_input_budget=est_retry_target,
                                            instructions=spec["instructions"],
                                            force=True,
                                            allow_cache=not gateway_auto_compacted,
                                        )
                                        if comp_res["compacted"]:
                                            chat_payload["messages"] = comp_res["messages"]
                                            est_in = comp_res["compacted_tokens"]
                                            gateway_auto_compacted = True
                                            orig_toks = max(
                                                real_in or 0,
                                                int(math.ceil(raw_est_in * new_scale)),
                                            )
                                            min_wire_input_tokens = max(
                                                min_wire_input_tokens or 0, orig_toks
                                            )
                                            self._record_compaction_metric(
                                                slot,
                                                original_tokens=orig_toks,
                                                compacted_tokens=int(
                                                    math.ceil(est_in * new_scale)
                                                ),
                                                stage=comp_res["stage"],
                                                ctx_tokens=effective_ctx,
                                            )
                                            chat_json = await self._post_chat_json(
                                                http,
                                                task,
                                                chat_payload,
                                                default_ctx=effective_ctx,
                                                error_param="input",
                                            )
                                            retry_usage_in, _ = _extract_usage(chat_json)
                                            retry_in, retry_allow_down = (
                                                _authoritative_prompt_tokens(
                                                    retry_usage_in,
                                                    _extract_timings(chat_json),
                                                )
                                            )
                                            self._observe_real_prompt_tokens(
                                                cal_key,
                                                est_in,
                                                retry_in,
                                                allow_downward=retry_allow_down,
                                            )
                                fallback_in_disp = int(
                                    math.ceil(est_in * self._get_token_scale(cal_key))
                                )
                                resp_obj = chat_completion_to_response(
                                    chat_json,
                                    spec,
                                    prefix_output=prefix_output,
                                    extra_usage=extra_usage,
                                    fallback_input_tokens=fallback_in_disp,
                                    min_input_tokens=min_wire_input_tokens,
                                )
                                if spec["store"]:
                                    self._store_response(resp_obj, stored_input_items)
                                slot["result"] = resp_obj
                                metric_usage = (
                                    None
                                    if gateway_auto_compacted
                                    else (
                                        resp_obj.get("usage")
                                        if isinstance(resp_obj.get("usage"), dict)
                                        else None
                                    )
                                )
                                self._finalize_request(
                                    slot,
                                    state="completed",
                                    response_obj=chat_json,
                                    usage_override=metric_usage,
                                    fallback_text=_extract_completion_text(chat_json),
                                    fallback_prompt_tokens=fallback_in_disp,
                                )
                    task.note("外部 API Responses 生成完成")
                except (asyncio.CancelledError, ColabOneCancelledError):
                    if stream:
                        if translator is None:
                            translator = ColabOneResponsesStreamTranslator(spec)
                        slot["fail_event"] = translator.fail("API 请求已取消")
                    cal_fallback = (
                        int(math.ceil(est_in * self._get_token_scale(cal_key)))
                        if isinstance(est_in, int)
                        else None
                    )
                    self._finalize_request(
                        slot,
                        state="cancelled",
                        error="API 请求已取消",
                        fallback_prompt_tokens=cal_fallback,
                    )
                    raise ColabOneCancelledError("API 请求已取消") from None
                except (TimeoutError, httpx.TimeoutException):
                    msg = "推理超时；请减少 max_output_tokens，或开启流式调用（stream=true）"
                    if stream:
                        if translator is None:
                            translator = ColabOneResponsesStreamTranslator(spec)
                        slot["fail_event"] = translator.fail(msg)
                    cal_fallback = (
                        int(math.ceil(est_in * self._get_token_scale(cal_key)))
                        if isinstance(est_in, int)
                        else None
                    )
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=msg,
                        fallback_prompt_tokens=cal_fallback,
                    )
                    raise RuntimeError(msg) from None
                except httpx.HTTPError:
                    msg = "无法连接远端推理引擎"
                    if stream:
                        if translator is None:
                            translator = ColabOneResponsesStreamTranslator(spec)
                        slot["fail_event"] = translator.fail(msg)
                    cal_fallback = (
                        int(math.ceil(est_in * self._get_token_scale(cal_key)))
                        if isinstance(est_in, int)
                        else None
                    )
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=msg,
                        fallback_prompt_tokens=cal_fallback,
                    )
                    raise RuntimeError(msg) from None
                except ColabOneEngineError as exc:
                    slot["error_info"] = exc.to_info()
                    if stream:
                        if translator is None:
                            translator = ColabOneResponsesStreamTranslator(spec)
                        slot["fail_event"] = translator.fail(
                            exc.message, code=exc.error_code
                        )
                    cal_fallback = (
                        int(math.ceil(est_in * self._get_token_scale(cal_key)))
                        if isinstance(est_in, int)
                        else None
                    )
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=exc.message,
                        fallback_prompt_tokens=exc.prompt_tokens or cal_fallback,
                    )
                    raise
                except Exception as exc:
                    if stream:
                        if translator is None:
                            translator = ColabOneResponsesStreamTranslator(spec)
                        slot["fail_event"] = translator.fail(str(exc) or "推理失败")
                    cal_fallback = (
                        int(math.ceil(est_in * self._get_token_scale(cal_key)))
                        if isinstance(est_in, int)
                        else None
                    )
                    self._finalize_request(
                        slot,
                        state="failed",
                        error=str(exc) or "推理失败",
                        fallback_prompt_tokens=cal_fallback,
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
            "error_info": None,
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
                except ColabOneEngineError as exc:
                    slot["error_info"] = exc.to_info()
                    self._finalize_request(
                        slot, state="failed", error=exc.message
                    )
                    raise
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

    class ColabOneAPIError(Exception):
        def __init__(self, info: dict[str, Any]):
            self.info = info

    @app.exception_handler(ColabOneAPIError)
    async def api_error(_, exc: ColabOneAPIError):
        info = exc.info
        err_body: dict[str, Any] = {
            "message": str(info.get("message") or "请求失败"),
            "type": str(info.get("type") or "api_error"),
            "code": str(info.get("code") or "server_error"),
        }
        if info.get("param"):
            err_body["param"] = str(info["param"])
        status = int(info.get("status_code") or 502)
        return JSONResponse({"error": err_body}, status_code=status)

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
            ctx_len = g._active_context_length(identity)
            return (
                {
                    "id": resolve_colabone_api_model_id(model),
                    "object": "model",
                    "created": 0,
                    "owned_by": colabone_model_owner(model),
                    "kind": "chat",
                    "context_length": ctx_len,
                    "context_window": ctx_len,
                    "max_model_len": ctx_len,
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
        slot = g.jobs[identity]
        task = slot["task"]
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
            err_info = slot.get("error_info")
            if isinstance(err_info, dict) and err_info.get("code"):
                raise ColabOneAPIError(err_info)
            raise HTTPException(502, task.error or "任务已取消")

    @app.post("/v1/chat/completions", dependencies=auth)
    async def chat(request: Request):
        raw = await read_json(request)
        auto_compact = _parse_auto_compact_header(
            request.headers.get("x-colabone-auto-compact")
        )
        identity = g.chat(raw, auto_compact=auto_compact)
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
                    err_info = slot.get("error_info")
                    err_payload: dict[str, Any] = {
                        "message": task.error or "API 请求已取消"
                    }
                    if isinstance(err_info, dict):
                        if err_info.get("type"):
                            err_payload["type"] = err_info["type"]
                        if err_info.get("code"):
                            err_payload["code"] = err_info["code"]
                        if err_info.get("param"):
                            err_payload["param"] = err_info["param"]
                    yield (
                        "data: "
                        + json.dumps({"error": err_payload}, ensure_ascii=False)
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
        auto_compact = _parse_auto_compact_header(
            request.headers.get("x-colabone-auto-compact")
        )
        identity, is_stream = g.respond(raw, auto_compact=auto_compact)
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
                        err_info = slot.get("error_info") or {}
                        err_code = str(err_info.get("code") or "server_error")
                        err_msg = str(
                            err_info.get("message") or task.error or "API 请求已取消"
                        )
                        yield (
                            "event: response.failed\n"
                            "data: "
                            + json.dumps(
                                {
                                    "type": "response.failed",
                                    "response": {
                                        "status": "failed",
                                        "error": {
                                            "code": err_code,
                                            "message": err_msg,
                                        },
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
