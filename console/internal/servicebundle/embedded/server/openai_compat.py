"""OpenAI Chat Completions, Responses API, and Codex compaction translation helpers."""

from __future__ import annotations

import base64
import hashlib
import json
import secrets
import time
import zlib
from typing import Any

MAX_BODY_BYTES = 16 * 1024 * 1024
MAX_RESPONSE_BYTES = 16 * 1024 * 1024
MAX_COMPACTION_DECOMPRESSED_BYTES = 256 * 1024
COLABONE_COMPACTION_PREFIX = "co_cmp_v1."

_UNSUPPORTED_MEDIA_TYPES = frozenset(
    {"image_url", "input_image", "input_audio", "input_file", "file"}
)
_CLOUD_ONLY_TOOL_TYPES = frozenset(
    {
        "web_search",
        "web_search_preview",
        "web_search_preview_2025_03_11",
        "file_search",
        "code_interpreter",
        "computer_use_preview",
        "image_generation",
        "mcp",
    }
)

COMPACTED_SUMMARY_HEADER = "[Compacted earlier conversation summary]"
COMPACTED_SUMMARY_FOOTER = "[End of compacted summary]"
_COMPACTED_CONTINUATION_NOTE = "Continue the conversation from the compacted summary above."

COMPACTION_SYSTEM_PROMPT = (
    "You are a precise conversation context compactor for an autonomous coding assistant (Codex). "
    "Produce a concise, high-signal continuation handoff summary of the conversation so the assistant "
    "can seamlessly continue the task without needing the full prior transcript.\n"
    "Preserve:\n"
    "1. User's primary goal, requirements, and explicit constraints.\n"
    "2. Concrete progress already made, files created/modified, key symbols/functions, and verified command/test results.\n"
    "3. Important technical facts, decisions, configurations, and error messages discovered.\n"
    "4. Remaining tasks, open questions, and the exact next step.\n"
    "Be factual and compact. Do not invent facts or repeat verbose raw tool output verbatim. "
    "Output only the handoff summary directly without chain-of-thought preamble."
)


_HIGH_REASONING_EFFORTS = frozenset({"high", "xhigh", "ultra", "max", "persistent"})
_DIRECT_REASONING_EFFORTS = frozenset({"none", "minimal", "low", "medium", "high"})


def normalize_reasoning_effort(raw_effort: Any) -> tuple[str | None, str | None]:
    """Normalize client reasoning effort tiers (including Codex xhigh/ultra/max) for llama-server.

    Returns ``(upstream_effort, raw_effort_lower)``.
    """
    if not isinstance(raw_effort, str):
        return None, None
    cleaned = raw_effort.strip().lower()
    if not cleaned:
        return None, None
    if cleaned in _DIRECT_REASONING_EFFORTS:
        return cleaned, cleaned
    if cleaned in _HIGH_REASONING_EFFORTS:
        return "high", cleaned
    return None, cleaned


def estimate_tokens(text: str) -> int:
    """Conservative CJK/code-aware token estimate (avoids 3-4x undercount on Chinese)."""
    if not text:
        return 0
    if text.isascii():
        return max(1, (len(text) * 29 + 99) // 100)
    n_chars = len(text)
    extra_bytes = len(text.encode("utf-8")) - n_chars
    return max(1, int(n_chars * 0.29 + extra_bytes * 0.46 + 0.99))


def clip_text_to_token_budget(
    text: str,
    max_tokens: int,
    *,
    marker_template: str = "\n[... omitted {omitted} chars ...]\n",
) -> str:
    """Truncate text keeping head (2/3) + tail (1/3) so estimate_tokens(result) <= max_tokens."""
    cleaned = (text or "").strip()
    if not cleaned:
        return ""
    budget = max(16, int(max_tokens))
    est = estimate_tokens(cleaned)
    if est <= budget:
        return cleaned

    def clip_prefix(limit_toks: int) -> str:
        # Guaranteed fallback: binary-search the longest prefix whose prefix + "..." fits limit_toks.
        suffix = "..."
        if estimate_tokens(suffix) >= limit_toks:
            return suffix
        lo, hi = 1, len(cleaned) - 1
        best = suffix
        while lo <= hi:
            mid = (lo + hi) // 2
            cand = cleaned[:mid] + suffix
            if estimate_tokens(cand) <= limit_toks:
                best = cand
                lo = mid + 1
            else:
                hi = mid - 1
        return best

    marker_est = estimate_tokens(marker_template.format(omitted=len(cleaned)))
    content_budget = budget - marker_est
    if content_budget < 8:
        return clip_prefix(budget)

    chars_per_tok = len(cleaned) / max(1, est)
    char_limit = max(4, int(content_budget * chars_per_tok))
    best_candidate: str | None = None
    for _ in range(8):
        if char_limit >= len(cleaned) - 1:
            break
        head = max(2, (char_limit * 2) // 3)
        tail = max(1, char_limit - head)
        if head + tail >= len(cleaned):
            char_limit = max(4, len(cleaned) - 2)
            head = max(2, (char_limit * 2) // 3)
            tail = max(1, char_limit - head)
            if head + tail >= len(cleaned):
                break
        omitted = len(cleaned) - head - tail
        candidate = (
            f"{cleaned[:head]}"
            f"{marker_template.format(omitted=omitted)}"
            f"{cleaned[-tail:]}"
        )
        cand_toks = estimate_tokens(candidate)
        if cand_toks <= budget and cand_toks < est:
            return candidate
        if best_candidate is None or cand_toks < estimate_tokens(best_candidate):
            best_candidate = candidate
        if char_limit <= 4:
            break
        char_limit = max(4, int(char_limit * 0.72))

    if best_candidate is not None and estimate_tokens(best_candidate) <= budget:
        return best_candidate
    return clip_prefix(budget)


def estimate_messages_tokens(
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None = None,
) -> int:
    """Estimate total prompt tokens for Chat Completions messages plus optional tool schemas."""
    total = 12
    for msg in messages:
        if not isinstance(msg, dict):
            continue
        total += 8
        content = msg.get("content")
        if isinstance(content, str) and content:
            total += estimate_tokens(content)
        name = msg.get("name")
        if isinstance(name, str) and name:
            total += estimate_tokens(name) + 2
        tool_call_id = msg.get("tool_call_id")
        if isinstance(tool_call_id, str) and tool_call_id:
            total += estimate_tokens(tool_call_id) + 2
        tool_calls = msg.get("tool_calls")
        if isinstance(tool_calls, list):
            for tc in tool_calls:
                if not isinstance(tc, dict):
                    continue
                total += 12
                fn = tc.get("function") or {}
                if isinstance(fn, dict):
                    if isinstance(fn.get("name"), str):
                        total += estimate_tokens(fn["name"])
                    args = fn.get("arguments")
                    if isinstance(args, str) and args:
                        total += estimate_tokens(args)
    if tools:
        total += 32 + estimate_tokens(
            json.dumps(tools, ensure_ascii=False, separators=(",", ":"))
        )
    return total


def encode_compaction_content(summary: str) -> str:
    cleaned = summary.strip()
    raw = json.dumps(
        {"v": 1, "summary": cleaned}, ensure_ascii=False, separators=(",", ":")
    ).encode("utf-8")
    if len(raw) > MAX_COMPACTION_DECOMPRESSED_BYTES:
        clipped = cleaned[: max(1000, MAX_COMPACTION_DECOMPRESSED_BYTES // 4)]
        raw = json.dumps(
            {"v": 1, "summary": clipped}, ensure_ascii=False, separators=(",", ":")
        ).encode("utf-8")
    compressed = zlib.compress(raw, level=6)
    token = base64.urlsafe_b64encode(compressed).decode("ascii").rstrip("=")
    return COLABONE_COMPACTION_PREFIX + token


def decode_compaction_content(blob: Any) -> str | None:
    if not isinstance(blob, str) or not blob.startswith(COLABONE_COMPACTION_PREFIX):
        return None
    encoded = blob[len(COLABONE_COMPACTION_PREFIX) :]
    if not encoded or len(encoded) > MAX_COMPACTION_DECOMPRESSED_BYTES * 2:
        return None
    try:
        padded = encoded + "=" * (-len(encoded) % 4)
        compressed = base64.urlsafe_b64decode(padded.encode("ascii"))
        decompressor = zlib.decompressobj()
        raw = decompressor.decompress(compressed, MAX_COMPACTION_DECOMPRESSED_BYTES + 1)
        if (
            len(raw) > MAX_COMPACTION_DECOMPRESSED_BYTES
            or decompressor.unconsumed_tail
        ):
            return None
        payload = json.loads(raw.decode("utf-8"))
        if isinstance(payload, dict) and isinstance(payload.get("summary"), str):
            return payload["summary"].strip()
    except Exception:
        return None
    return None


def flatten_content(content: Any, *, allow_none: bool = False) -> str | None:
    if content is None:
        return None if allow_none else ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts: list[str] = []
        for part in content:
            if isinstance(part, str):
                parts.append(part)
                continue
            if not isinstance(part, dict):
                raise ValueError("消息内容格式无效")
            part_type = part.get("type")
            if "image_url" in part or part_type in _UNSUPPORTED_MEDIA_TYPES:
                raise ValueError("当前远端 GGUF 文本模型暂不支持图像或音频输入")
            if part_type in ("text", "input_text", "output_text", "summary_text"):
                text = part.get("text")
                if not isinstance(text, str):
                    raise ValueError("文本内容片段必须为字符串")
                parts.append(text)
            elif part_type == "refusal":
                refusal = part.get("refusal")
                if isinstance(refusal, str):
                    parts.append(refusal)
            elif isinstance(part.get("text"), str):
                parts.append(part["text"])
            else:
                raise ValueError("消息内容片段不受支持")
        return "\n".join(parts)
    raise ValueError("消息内容必须为字符串或文本内容数组")


def _flatten_tool_output(output: Any) -> str:
    if output is None:
        return ""
    if isinstance(output, str):
        return output
    if isinstance(output, list):
        pieces: list[str] = []
        for item in output:
            if isinstance(item, str):
                pieces.append(item)
            elif isinstance(item, dict):
                item_type = item.get("type")
                if "image_url" in item or item_type in _UNSUPPORTED_MEDIA_TYPES:
                    pieces.append("[omitted non-text tool output]")
                elif isinstance(item.get("text"), str):
                    pieces.append(item["text"])
                else:
                    pieces.append(json.dumps(item, ensure_ascii=False))
            else:
                pieces.append(str(item))
        return "\n".join(pieces)
    if isinstance(output, (dict, int, float, bool)):
        return json.dumps(output, ensure_ascii=False)
    return str(output)


def _normalize_chat_tool(tool: Any) -> dict[str, Any]:
    if not isinstance(tool, dict):
        raise ValueError("工具定义必须为对象")
    tool_type = tool.get("type")
    if tool_type != "function":
        raise ValueError("Chat Completions 仅支持 function 类型工具")
    fn = tool.get("function")
    if not isinstance(fn, dict):
        if isinstance(tool.get("name"), str) and tool["name"].strip():
            fn = {
                k: tool[k]
                for k in ("name", "description", "parameters", "strict")
                if k in tool
            }
        else:
            raise ValueError("function 工具缺少函数定义")
    name = fn.get("name")
    if not isinstance(name, str) or not name.strip() or len(name) > 128:
        raise ValueError("工具名称无效")
    normalized: dict[str, Any] = {"name": name.strip()}
    if isinstance(fn.get("description"), str):
        normalized["description"] = fn["description"]
    if isinstance(fn.get("parameters"), dict):
        normalized["parameters"] = fn["parameters"]
    if isinstance(fn.get("strict"), bool):
        normalized["strict"] = fn["strict"]
    return {"type": "function", "function": normalized}


def _normalize_tool_calls(raw_calls: Any) -> list[dict[str, Any]]:
    if not isinstance(raw_calls, list):
        raise ValueError("tool_calls 必须为数组")
    normalized: list[dict[str, Any]] = []
    for call in raw_calls:
        if not isinstance(call, dict):
            raise ValueError("tool_calls 元素必须为对象")
        fn = call.get("function")
        if not isinstance(fn, dict) or not isinstance(fn.get("name"), str):
            raise ValueError("tool_calls 缺少有效的 function 字段")
        args = fn.get("arguments", "{}")
        if not isinstance(args, str):
            args = json.dumps(args, ensure_ascii=False)
        call_id = call.get("id")
        if not isinstance(call_id, str) or not call_id:
            call_id = f"call_{secrets.token_hex(8)}"
        normalized.append(
            {
                "id": call_id,
                "type": "function",
                "function": {"name": fn["name"], "arguments": args},
            }
        )
    return normalized


def _validate_float(value: Any, name: str, lo: float, hi: float, *, gt_lo: bool = False) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{name} 必须为数字")
    f = float(value)
    if f != f or f in (float("inf"), float("-inf")):
        raise ValueError(f"{name} 不能为 NaN 或无穷大")
    if (f <= lo if gt_lo else f < lo) or f > hi:
        raise ValueError(f"{name} 超出允许范围")
    return f


def _validate_int(value: Any, name: str, lo: int, hi: int) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"{name} 必须为整数")
    if value < lo or value > hi:
        raise ValueError(f"{name} 超出允许范围")
    return value


def normalize_chat_request(raw: Any) -> tuple[str, bool, dict[str, Any]]:
    """Validate and normalize an OpenAI Chat Completions request for llama-server."""
    if not isinstance(raw, dict):
        raise ValueError("请求体必须为 JSON 对象")
    model = raw.get("model")
    if not isinstance(model, str) or not model.strip() or len(model) > 160:
        raise ValueError("model 必须为非空字符串")
    model = model.strip()

    raw_messages = raw.get("messages")
    if not isinstance(raw_messages, list) or not raw_messages or len(raw_messages) > 2048:
        raise ValueError("messages 必须为非空数组")

    messages: list[dict[str, Any]] = []
    for item in raw_messages:
        if not isinstance(item, dict):
            raise ValueError("消息项必须为对象")
        role = item.get("role")
        if role not in ("system", "developer", "user", "assistant", "tool"):
            raise ValueError("不支持的消息角色")
        mapped_role = "system" if role == "developer" else role
        if mapped_role == "assistant" and item.get("tool_calls") is not None:
            tool_calls = _normalize_tool_calls(item["tool_calls"])
            content = flatten_content(item.get("content"), allow_none=True)
            msg: dict[str, Any] = {
                "role": "assistant",
                "content": content,
                "tool_calls": tool_calls,
            }
        elif mapped_role == "tool":
            tool_call_id = item.get("tool_call_id")
            if not isinstance(tool_call_id, str) or not tool_call_id.strip():
                raise ValueError("tool 消息必须提供 tool_call_id")
            msg = {
                "role": "tool",
                "tool_call_id": tool_call_id.strip(),
                "content": _flatten_tool_output(item.get("content", "")),
            }
            if isinstance(item.get("name"), str) and item["name"].strip():
                msg["name"] = item["name"].strip()
        else:
            content = flatten_content(item.get("content"), allow_none=False)
            msg = {"role": mapped_role, "content": content}
            if isinstance(item.get("name"), str) and item["name"].strip():
                msg["name"] = item["name"].strip()
        messages.append(msg)

    stream = raw.get("stream", False)
    if not isinstance(stream, bool):
        raise ValueError("stream 必须为布尔值")

    payload: dict[str, Any] = {
        "model": model,
        "messages": _normalize_role_sequence(messages),
        "stream": stream,
    }

    if raw.get("temperature") is not None:
        payload["temperature"] = _validate_float(raw["temperature"], "temperature", 0, 2)
    if raw.get("top_p") is not None:
        payload["top_p"] = _validate_float(raw["top_p"], "top_p", 0, 1, gt_lo=True)
    if raw.get("presence_penalty") is not None:
        payload["presence_penalty"] = _validate_float(
            raw["presence_penalty"], "presence_penalty", -2, 2
        )
    if raw.get("frequency_penalty") is not None:
        payload["frequency_penalty"] = _validate_float(
            raw["frequency_penalty"], "frequency_penalty", -2, 2
        )

    max_tokens = raw.get("max_tokens")
    if max_tokens is None:
        max_tokens = raw.get("max_completion_tokens")
    if max_tokens is not None:
        payload["max_tokens"] = _validate_int(max_tokens, "max_tokens", 1, 1048576)

    if raw.get("seed") is not None:
        payload["seed"] = _validate_int(raw["seed"], "seed", -(2**63), 2**63 - 1)
    if raw.get("n") is not None:
        if _validate_int(raw["n"], "n", 1, 1) != 1:
            raise ValueError("当前仅支持 n=1")

    stop = raw.get("stop")
    if stop is not None:
        if isinstance(stop, str):
            payload["stop"] = stop
        elif isinstance(stop, list) and all(isinstance(s, str) for s in stop):
            payload["stop"] = stop[:16]
        else:
            raise ValueError("stop 必须为字符串或字符串数组")

    if raw.get("response_format") is not None:
        rf = raw["response_format"]
        if not isinstance(rf, dict) or rf.get("type") not in (
            "text",
            "json_object",
            "json_schema",
        ):
            raise ValueError("response_format 不受支持")
        payload["response_format"] = rf

    if raw.get("tools") is not None:
        if not isinstance(raw["tools"], list):
            raise ValueError("tools 必须为数组")
        tools = [_normalize_chat_tool(t) for t in raw["tools"]]
        if tools:
            payload["tools"] = tools
            if raw.get("tool_choice") is not None:
                tc = raw["tool_choice"]
                if isinstance(tc, str) and tc in ("none", "auto", "required"):
                    payload["tool_choice"] = tc
                elif (
                    isinstance(tc, dict)
                    and tc.get("type") == "function"
                    and isinstance((tc.get("function") or {}).get("name"), str)
                ):
                    payload["tool_choice"] = tc
                else:
                    raise ValueError("tool_choice 格式无效")
            if isinstance(raw.get("parallel_tool_calls"), bool):
                payload["parallel_tool_calls"] = raw["parallel_tool_calls"]

    if stream and isinstance(raw.get("stream_options"), dict):
        if isinstance(raw["stream_options"].get("include_usage"), bool):
            payload["stream_options"] = {
                "include_usage": raw["stream_options"]["include_usage"]
            }

    upstream_effort, _ = normalize_reasoning_effort(raw.get("reasoning_effort"))
    if upstream_effort is not None:
        payload["reasoning_effort"] = upstream_effort

    return model, stream, payload


def _normalize_role_sequence(messages: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Ensure system prompts stay at index 0 so strict GGUF Jinja templates never reject mid-stream developer/system roles."""
    leading_systems: list[str] = []
    rest: list[dict[str, Any]] = []
    seen_non_system = False

    for msg in messages:
        role = msg.get("role")
        if role == "system":
            text = (msg.get("content") or "").strip()
            if not text:
                continue
            if not seen_non_system:
                leading_systems.append(text)
            else:
                # Mid-conversation developer/system context (common in Codex): attach cleanly as user context.
                note = f"[Developer instructions]\n{text}"
                if rest and rest[-1].get("role") == "user" and "tool_calls" not in rest[-1]:
                    prev = rest[-1].get("content") or ""
                    rest[-1] = {
                        **rest[-1],
                        "content": (prev + "\n\n" + note).strip() if prev else note,
                    }
                else:
                    rest.append({"role": "user", "content": note})
        else:
            seen_non_system = True
            rest.append(msg)

    out: list[dict[str, Any]] = []
    if leading_systems:
        out.append({"role": "system", "content": "\n\n".join(leading_systems)})
    out.extend(rest)
    return out


def _normalize_response_tools(raw_tools: Any) -> tuple[list[dict[str, Any]], set[str]]:
    """Convert Responses API tools (function + Codex custom/apply_patch) into Chat tools."""
    if raw_tools is None:
        return [], set()
    if not isinstance(raw_tools, list):
        raise ValueError("tools 必须为数组")
    chat_tools: list[dict[str, Any]] = []
    custom_names: set[str] = set()
    for tool in raw_tools:
        if not isinstance(tool, dict):
            raise ValueError("工具定义必须为对象")
        t_type = tool.get("type")
        if t_type == "function":
            if isinstance(tool.get("function"), dict):
                chat_tools.append(_normalize_chat_tool(tool))
            else:
                name = tool.get("name")
                if not isinstance(name, str) or not name.strip() or len(name) > 128:
                    raise ValueError("function 工具缺少有效的 name")
                fn: dict[str, Any] = {"name": name.strip()}
                if isinstance(tool.get("description"), str):
                    fn["description"] = tool["description"]
                if isinstance(tool.get("parameters"), dict):
                    fn["parameters"] = tool["parameters"]
                if isinstance(tool.get("strict"), bool):
                    fn["strict"] = tool["strict"]
                chat_tools.append({"type": "function", "function": fn})
        elif t_type == "custom":
            name = tool.get("name")
            if not isinstance(name, str) or not name.strip() or len(name) > 128:
                raise ValueError("custom 工具缺少有效的 name")
            name = name.strip()
            custom_names.add(name)
            desc = str(tool.get("description") or f"Custom tool {name}").strip()
            fmt = tool.get("format")
            if isinstance(fmt, dict) and fmt.get("type") == "grammar":
                syntax = fmt.get("syntax") or "grammar"
                desc += f"\nFormat ({syntax}): pass the raw formatted text in the `input` field."
            chat_tools.append(
                {
                    "type": "function",
                    "function": {
                        "name": name,
                        "description": desc,
                        "parameters": {
                            "type": "object",
                            "properties": {
                                "input": {
                                    "type": "string",
                                    "description": f"Raw input payload for custom tool `{name}`.",
                                }
                            },
                            "required": ["input"],
                            "additionalProperties": False,
                        },
                    },
                }
            )
        elif t_type == "apply_patch":
            custom_names.add("apply_patch")
            chat_tools.append(
                {
                    "type": "function",
                    "function": {
                        "name": "apply_patch",
                        "description": "Apply a unified/V4A patch to workspace files. Pass the complete patch text in `input`.",
                        "parameters": {
                            "type": "object",
                            "properties": {
                                "input": {
                                    "type": "string",
                                    "description": "Complete patch text.",
                                }
                            },
                            "required": ["input"],
                            "additionalProperties": False,
                        },
                    },
                }
            )
        elif t_type in _CLOUD_ONLY_TOOL_TYPES:
            # Skip OpenAI-hosted cloud tools gracefully so Codex default configs work out of the box.
            continue
        else:
            # Tolerate future/vendor tool wrappers cleanly rather than failing the whole Codex turn.
            continue
    return chat_tools, custom_names


def _normalize_response_tool_choice(raw_choice: Any, chat_tools: list[dict[str, Any]]) -> Any:
    if raw_choice is None or not chat_tools:
        return None
    if isinstance(raw_choice, str) and raw_choice in ("none", "auto", "required"):
        return raw_choice
    if isinstance(raw_choice, dict):
        t_type = raw_choice.get("type")
        if t_type in ("function", "custom") and isinstance(raw_choice.get("name"), str):
            return {"type": "function", "function": {"name": raw_choice["name"]}}
        if (
            t_type == "function"
            and isinstance(raw_choice.get("function"), dict)
            and isinstance(raw_choice["function"].get("name"), str)
        ):
            return raw_choice
        if t_type == "allowed_tools":
            mode = raw_choice.get("mode")
            if mode in ("auto", "required", "none"):
                return mode
    return "auto"


def _normalize_input_items(raw_input: Any, response_store: dict[str, dict[str, Any]] | None = None) -> list[dict[str, Any]]:
    if raw_input is None:
        return []
    if isinstance(raw_input, str):
        return [
            {
                "type": "message",
                "role": "user",
                "content": [{"type": "input_text", "text": raw_input}],
            }
        ]
    if not isinstance(raw_input, list):
        raise ValueError("input 必须为字符串或消息项数组")
    items: list[dict[str, Any]] = []
    for entry in raw_input:
        if isinstance(entry, str):
            items.append(
                {
                    "type": "message",
                    "role": "user",
                    "content": [{"type": "input_text", "text": entry}],
                }
            )
        elif isinstance(entry, dict):
            if entry.get("type") == "item_reference" and response_store:
                ref_id = entry.get("id")
                found = False
                for saved in response_store.values():
                    for candidate in saved.get("items") or []:
                        if candidate.get("id") == ref_id:
                            items.append(candidate)
                            found = True
                            break
                    if found:
                        break
            else:
                items.append(entry)
        else:
            raise ValueError("input 元素格式无效")
    return items


def response_items_to_chat_messages(
    items: list[dict[str, Any]],
    instructions: str | None = None,
) -> tuple[list[dict[str, Any]], bool]:
    """Translate Responses API input items (including compaction items & tool calls) into Chat messages."""
    messages: list[dict[str, Any]] = []
    if isinstance(instructions, str) and instructions.strip():
        messages.append({"role": "system", "content": instructions.strip()})

    seen_call_ids: set[str] = set()
    trigger_compaction = False

    def append_tool_call(call_id: str, name: str, arguments: str) -> None:
        seen_call_ids.add(call_id)
        tc = {
            "id": call_id,
            "type": "function",
            "function": {"name": name, "arguments": arguments},
        }
        if messages and messages[-1].get("role") == "assistant":
            prev = dict(messages[-1])
            calls = list(prev.get("tool_calls") or [])
            calls.append(tc)
            prev["tool_calls"] = calls
            if prev.get("content") == "":
                prev["content"] = None
            messages[-1] = prev
        else:
            messages.append({"role": "assistant", "content": None, "tool_calls": [tc]})

    for item in items:
        item_type = item.get("type")
        if item_type == "message" or (item_type is None and "role" in item):
            role = item.get("role", "user")
            if role not in ("system", "developer", "user", "assistant"):
                raise ValueError("不支持的消息角色")
            mapped_role = "system" if role == "developer" else role
            text = flatten_content(item.get("content", ""), allow_none=False) or ""
            messages.append({"role": mapped_role, "content": text})
        elif item_type == "function_call":
            name = item.get("name")
            if not isinstance(name, str) or not name.strip():
                raise ValueError("function_call 缺少有效的 name")
            call_id = str(item.get("call_id") or item.get("id") or f"call_{secrets.token_hex(8)}")
            args = item.get("arguments", "{}")
            if not isinstance(args, str):
                args = json.dumps(args, ensure_ascii=False)
            append_tool_call(call_id, name.strip(), args)
        elif item_type == "custom_tool_call":
            name = str(item.get("name") or "apply_patch").strip()
            call_id = str(item.get("call_id") or item.get("id") or f"call_{secrets.token_hex(8)}")
            raw_in = item.get("input", "")
            raw_in_str = (
                raw_in if isinstance(raw_in, str) else json.dumps(raw_in, ensure_ascii=False)
            )
            args = json.dumps({"input": raw_in_str}, ensure_ascii=False)
            append_tool_call(call_id, name, args)
        elif item_type in ("function_call_output", "custom_tool_call_output"):
            call_id = str(item.get("call_id") or "").strip()
            output_text = _flatten_tool_output(item.get("output", ""))
            if call_id and call_id in seen_call_ids:
                messages.append(
                    {
                        "role": "tool",
                        "tool_call_id": call_id,
                        "content": output_text,
                    }
                )
            else:
                # Standalone / cross-thread tool output without a paired call_id (Codex app-server edge case).
                messages.append(
                    {
                        "role": "user",
                        "content": f"[Tool output]\n{output_text}",
                    }
                )
        elif item_type in ("compaction", "compaction_summary"):
            summary = decode_compaction_content(item.get("encrypted_content"))
            if not summary and isinstance(item.get("summary"), str):
                summary = item["summary"].strip()
            if summary:
                block = f"[Compacted conversation summary]\n{summary}"
                has_user = any(m.get("role") == "user" for m in messages)
                if (
                    messages
                    and messages[-1].get("role") == "assistant"
                    and not messages[-1].get("tool_calls")
                ):
                    prev = messages[-1].get("content") or ""
                    messages[-1] = {
                        **messages[-1],
                        "content": (prev + "\n\n" + block).strip() if prev else block,
                    }
                elif has_user:
                    # Preserves clean system -> user -> assistant -> user role alternation across all GGUF templates.
                    messages.append({"role": "assistant", "content": block})
                else:
                    messages.append({"role": "system", "content": block})
        elif item_type == "compaction_trigger":
            trigger_compaction = True
        elif item_type == "reasoning":
            continue
        elif item_type in ("input_text", "output_text", "text") and isinstance(
            item.get("text"), str
        ):
            messages.append({"role": "user", "content": item["text"]})
        else:
            continue

    normalized = _normalize_role_sequence(messages)
    if not normalized or all(m.get("role") == "system" for m in normalized):
        # Ensure llama-server always receives at least one user turn.
        normalized.append({"role": "user", "content": "Continue."})
    return normalized, trigger_compaction


def normalize_responses_request(
    raw: Any,
    response_store: dict[str, dict[str, Any]] | None = None,
) -> dict[str, Any]:
    """Validate and prepare a Responses API request for execution against llama-server."""
    if not isinstance(raw, dict):
        raise ValueError("请求体必须为 JSON 对象")
    model = raw.get("model")
    if not isinstance(model, str) or not model.strip() or len(model) > 160:
        raise ValueError("model 必须为非空字符串")
    model = model.strip()

    stream = raw.get("stream", False)
    if not isinstance(stream, bool):
        raise ValueError("stream 必须为布尔值")

    instructions = raw.get("instructions")
    if instructions is not None and not isinstance(instructions, str):
        raise ValueError("instructions 必须为字符串")

    previous_response_id = raw.get("previous_response_id")
    prior_items: list[dict[str, Any]] = []
    if previous_response_id is not None:
        if not isinstance(previous_response_id, str) or not previous_response_id.strip():
            raise ValueError("previous_response_id 格式无效")
        previous_response_id = previous_response_id.strip()
        if not response_store or previous_response_id not in response_store:
            raise ValueError("指定的 previous_response_id 不存在或已过期")
        prior_items = list(response_store[previous_response_id].get("items") or [])

    new_items = _normalize_input_items(raw.get("input"), response_store)
    all_items = prior_items + new_items
    if not all_items and not instructions:
        raise ValueError("input 不能为空")

    chat_messages, trigger_compaction = response_items_to_chat_messages(
        all_items, instructions
    )
    chat_tools, custom_names = _normalize_response_tools(raw.get("tools"))
    tool_choice = _normalize_response_tool_choice(raw.get("tool_choice"), chat_tools)

    chat_payload: dict[str, Any] = {
        "model": model,
        "messages": chat_messages,
        "stream": stream,
    }
    if stream:
        chat_payload["stream_options"] = {"include_usage": True}

    temperature = raw.get("temperature")
    if temperature is not None:
        chat_payload["temperature"] = _validate_float(temperature, "temperature", 0, 2)
    top_p = raw.get("top_p")
    if top_p is not None:
        chat_payload["top_p"] = _validate_float(top_p, "top_p", 0, 1, gt_lo=True)

    max_output_tokens = raw.get("max_output_tokens")
    if max_output_tokens is not None:
        chat_payload["max_tokens"] = _validate_int(
            max_output_tokens, "max_output_tokens", 1, 1048576
        )

    if chat_tools:
        chat_payload["tools"] = chat_tools
        if tool_choice is not None:
            chat_payload["tool_choice"] = tool_choice
        if isinstance(raw.get("parallel_tool_calls"), bool):
            chat_payload["parallel_tool_calls"] = raw["parallel_tool_calls"]

    text_cfg = raw.get("text")
    if isinstance(text_cfg, dict) and isinstance(text_cfg.get("format"), dict):
        fmt = text_cfg["format"]
        fmt_type = fmt.get("type")
        if fmt_type == "json_object":
            chat_payload["response_format"] = {"type": "json_object"}
        elif fmt_type == "json_schema" and isinstance(fmt.get("schema"), dict):
            chat_payload["response_format"] = {
                "type": "json_schema",
                "json_schema": {
                    "name": str(fmt.get("name") or "response"),
                    "schema": fmt["schema"],
                    **({"strict": fmt["strict"]} if isinstance(fmt.get("strict"), bool) else {}),
                },
            }

    reasoning_cfg = raw.get("reasoning")
    raw_reasoning_effort: str | None = None
    if isinstance(reasoning_cfg, dict):
        upstream_effort, raw_reasoning_effort = normalize_reasoning_effort(
            reasoning_cfg.get("effort")
        )
        if upstream_effort is not None:
            chat_payload["reasoning_effort"] = upstream_effort

    compact_threshold: int | None = None
    cm = raw.get("context_management")
    if isinstance(cm, list):
        for entry in cm:
            if isinstance(entry, dict) and entry.get("type") == "compaction":
                threshold = entry.get("compact_threshold")
                if isinstance(threshold, int) and not isinstance(threshold, bool) and threshold > 0:
                    compact_threshold = threshold

    store = raw.get("store", True)
    if not isinstance(store, bool):
        raise ValueError("store 必须为布尔值")

    return {
        "model": model,
        "stream": stream,
        "instructions": instructions,
        "previous_response_id": previous_response_id,
        "input_items": all_items,
        "chat_payload": chat_payload,
        "custom_tool_names": custom_names,
        "tools_echo": raw.get("tools") if isinstance(raw.get("tools"), list) else [],
        "tool_choice_echo": raw.get("tool_choice") if raw.get("tool_choice") is not None else "auto",
        "parallel_tool_calls": raw.get("parallel_tool_calls", True)
        if isinstance(raw.get("parallel_tool_calls"), bool)
        else True,
        "max_output_tokens": max_output_tokens,
        "temperature": chat_payload.get("temperature", 1.0),
        "top_p": chat_payload.get("top_p", 1.0),
        "text_echo": text_cfg if isinstance(text_cfg, dict) else {"format": {"type": "text"}},
        "reasoning_echo": reasoning_cfg
        if isinstance(reasoning_cfg, dict)
        else {"effort": None, "summary": None},
        "raw_reasoning_effort": raw_reasoning_effort,
        "metadata": raw.get("metadata") if isinstance(raw.get("metadata"), dict) else {},
        "user": raw.get("user") if isinstance(raw.get("user"), str) else None,
        "store": store,
        "trigger_compaction": trigger_compaction,
        "compact_threshold": compact_threshold,
    }


def normalize_compact_request(
    raw: Any,
    response_store: dict[str, dict[str, Any]] | None = None,
) -> dict[str, Any]:
    """Validate and prepare a POST /v1/responses/compact request."""
    if not isinstance(raw, dict):
        raise ValueError("请求体必须为 JSON 对象")
    model = raw.get("model")
    if not isinstance(model, str) or not model.strip() or len(model) > 160:
        raise ValueError("model 必须为非空字符串")
    model = model.strip()

    instructions = raw.get("instructions")
    if instructions is not None and not isinstance(instructions, str):
        raise ValueError("instructions 必须为字符串")

    previous_response_id = raw.get("previous_response_id")
    prior_items: list[dict[str, Any]] = []
    if previous_response_id is not None:
        if not isinstance(previous_response_id, str) or not previous_response_id.strip():
            raise ValueError("previous_response_id 格式无效")
        previous_response_id = previous_response_id.strip()
        if not response_store or previous_response_id not in response_store:
            raise ValueError("指定的 previous_response_id 不存在或已过期")
        prior_items = list(response_store[previous_response_id].get("items") or [])

    new_items = _normalize_input_items(raw.get("input"), response_store)
    return {
        "model": model,
        "instructions": instructions,
        "items": prior_items + new_items,
    }


def render_items_for_compaction(items: list[dict[str, Any]], max_item_chars: int = 8000) -> list[str]:
    """Render ResponseItems into clean transcript blocks for summarization."""
    blocks: list[str] = []

    def clip(text: str, limit: int = max_item_chars) -> str:
        text = text.strip()
        if len(text) <= limit:
            return text
        head = limit * 2 // 3
        tail = limit - head
        omitted = len(text) - head - tail
        return f"{text[:head]}\n[... omitted {omitted} chars ...]\n{text[-tail:]}"

    for item in items:
        item_type = item.get("type")
        if item_type == "message" or (item_type is None and "role" in item):
            role = str(item.get("role", "user")).upper()
            text = flatten_content(item.get("content", ""), allow_none=False) or ""
            if text.strip():
                blocks.append(f"[{role}]\n{clip(text)}")
        elif item_type == "function_call":
            name = item.get("name", "tool")
            args = item.get("arguments", "")
            if not isinstance(args, str):
                args = json.dumps(args, ensure_ascii=False)
            blocks.append(f"[TOOL_CALL: {name}]\n{clip(args, 3000)}")
        elif item_type == "custom_tool_call":
            name = item.get("name", "apply_patch")
            raw_in = item.get("input", "")
            if not isinstance(raw_in, str):
                raw_in = json.dumps(raw_in, ensure_ascii=False)
            blocks.append(f"[CUSTOM_TOOL_CALL: {name}]\n{clip(raw_in, 4000)}")
        elif item_type in ("function_call_output", "custom_tool_call_output"):
            out_text = _flatten_tool_output(item.get("output", ""))
            blocks.append(f"[TOOL_OUTPUT]\n{clip(out_text, 4000)}")
        elif item_type in ("compaction", "compaction_summary"):
            prev = decode_compaction_content(item.get("encrypted_content"))
            if not prev and isinstance(item.get("summary"), str):
                prev = item["summary"].strip()
            if prev:
                blocks.append(f"[PRIOR_SUMMARY]\n{clip(prev, max_item_chars)}")
    return blocks


def extract_anchor_user_item(items: list[dict[str, Any]], max_chars: int = 1200) -> dict[str, Any] | None:
    """Extract a concise preserved user message item matching OpenAI /responses/compact output."""
    for item in items:
        item_type = item.get("type")
        if (item_type == "message" or (item_type is None and "role" in item)) and item.get(
            "role"
        ) == "user":
            text = (flatten_content(item.get("content", ""), allow_none=False) or "").strip()
            if not text:
                continue
            if len(text) > max_chars:
                text = text[: max_chars - 3].rstrip() + "..."
            return {
                "id": f"msg_{secrets.token_hex(12)}",
                "type": "message",
                "status": "completed",
                "role": "user",
                "content": [{"type": "input_text", "text": text}],
            }
    return None


def chunk_grouped_transcript_blocks_by_tokens(
    grouped_blocks: list[tuple[int, str]],
    token_budget: int,
    *,
    max_chunks: int = 6,
) -> list[tuple[str, int | None]]:
    """Pack ``(group_index, block)`` pairs into chunks bounded by ``token_budget``.

    Keeps atomic groups intact inside one chunk whenever a single group fits within
    ``token_budget``. Returns ``(chunk_text, end_group_index)`` pairs where
    ``end_group_index`` is non-``None`` **only** when the chunk ends cleanly on a
    complete group boundary with an unbroken prefix from the beginning (never mid-group
    and never after middle chunks were dropped by ``max_chunks``).
    """
    if not grouped_blocks:
        return []
    budget = max(128, int(token_budget))
    chunk_limit = max(2, int(max_chunks))

    # Group consecutive blocks sharing the same group_index so multi-block tool groups
    # are kept atomic whenever possible and mid-group splits are detected accurately.
    atomic_groups: list[tuple[int, list[str]]] = []
    for grp_idx, raw_block in grouped_blocks:
        block = raw_block
        if estimate_tokens(block) > budget:
            block = clip_text_to_token_budget(
                block,
                budget,
                marker_template="\n[... truncated {omitted} chars ...]\n",
            )
        if atomic_groups and atomic_groups[-1][0] == grp_idx:
            atomic_groups[-1][1].append(block)
        else:
            atomic_groups.append((grp_idx, [block]))

    chunks: list[tuple[str, int | None]] = []
    current: list[str] = []
    current_toks = 0
    current_end_idx: int | None = None

    for grp_idx, grp_blocks in atomic_groups:
        block_toks_list = [estimate_tokens(b) for b in grp_blocks]
        grp_total_toks = sum(block_toks_list) + 2 * max(0, len(grp_blocks) - 1)

        if grp_total_toks <= budget:
            sep_toks = 2 if current else 0
            if current and current_toks + sep_toks + grp_total_toks > budget:
                chunks.append(("\n\n".join(current), current_end_idx))
                current = list(grp_blocks)
                current_toks = grp_total_toks
                current_end_idx = grp_idx
            else:
                current.extend(grp_blocks)
                current_toks += sep_toks + grp_total_toks
                current_end_idx = grp_idx
        else:
            # Flush any completed prior groups cleanly before splitting this oversized group.
            if current:
                chunks.append(("\n\n".join(current), current_end_idx))
                current = []
                current_toks = 0
                current_end_idx = None
            for b_pos, (block, b_toks) in enumerate(zip(grp_blocks, block_toks_list)):
                sep_toks = 2 if current else 0
                if current and current_toks + sep_toks + b_toks > budget:
                    # Flushing mid-group: remaining blocks of grp_idx have not been included yet,
                    # so this chunk MUST NOT publish a cacheable checkpoint index.
                    chunks.append(("\n\n".join(current), None))
                    current = [block]
                    current_toks = b_toks
                else:
                    current.append(block)
                    current_toks += sep_toks + b_toks
                if b_pos == len(grp_blocks) - 1:
                    current_end_idx = grp_idx
                else:
                    current_end_idx = None

    if current:
        chunks.append(("\n\n".join(current), current_end_idx))

    if len(chunks) > chunk_limit:
        # Keep earliest context + most recent (chunk_limit - 1) chunks so compaction finishes fast.
        # Chunk 0 retains its end_group_index ONLY if it ended cleanly on a complete group boundary;
        # trailing chunks after dropped middle chunks never represent an unbroken prefix.
        kept_tail = [(text, None) for text, _ in chunks[-(chunk_limit - 1) :]]
        chunks = [chunks[0], *kept_tail]
    return chunks


def chunk_transcript_blocks_by_tokens(
    blocks: list[str],
    token_budget: int,
    *,
    max_chunks: int = 6,
) -> list[str]:
    """Pack rendered transcript blocks into chunks bounded by CJK-aware token_budget."""
    return [
        text
        for text, _ in chunk_grouped_transcript_blocks_by_tokens(
            list(enumerate(blocks)),
            token_budget,
            max_chunks=max_chunks,
        )
    ]


def chunk_transcript_blocks(blocks: list[str], char_budget: int) -> list[str]:
    """Pack rendered transcript blocks into chunks bounded by char_budget (compat wrapper)."""
    approx_tok_budget = max(128, int(char_budget * 0.30))
    return chunk_transcript_blocks_by_tokens(blocks, approx_tok_budget)


def render_chat_messages_for_compaction(
    messages: list[dict[str, Any]],
    max_item_chars: int = 6000,
) -> list[str]:
    """Render Chat Completions messages into transcript blocks for rolling summarization."""
    blocks: list[str] = []

    def clip(text: str, limit: int = max_item_chars) -> str:
        text = text.strip()
        if len(text) <= limit:
            return text
        head = limit * 2 // 3
        tail = limit - head
        omitted = len(text) - head - tail
        return f"{text[:head]}\n[... omitted {omitted} chars ...]\n{text[-tail:]}"

    for msg in messages:
        if not isinstance(msg, dict):
            continue
        role = str(msg.get("role") or "user").upper()
        content = msg.get("content")
        if isinstance(content, str) and content.strip():
            if role == "TOOL":
                blocks.append(f"[TOOL_OUTPUT]\n{clip(content, 4000)}")
            else:
                blocks.append(f"[{role}]\n{clip(content)}")
        tool_calls = msg.get("tool_calls")
        if isinstance(tool_calls, list):
            for tc in tool_calls:
                if not isinstance(tc, dict):
                    continue
                fn = tc.get("function") or {}
                if not isinstance(fn, dict):
                    continue
                name = str(fn.get("name") or "tool")
                args = fn.get("arguments", "")
                if not isinstance(args, str):
                    args = json.dumps(args, ensure_ascii=False)
                blocks.append(f"[TOOL_CALL: {name}]\n{clip(args, 3000)}")
    return blocks


def group_chat_messages(
    messages: list[dict[str, Any]],
) -> tuple[dict[str, Any] | None, list[list[dict[str, Any]]]]:
    """Split leading system prompt and group remaining messages into atomic Jinja-safe turns.

    An assistant message containing ``tool_calls`` is kept together with all immediately
    following ``role == "tool"`` responses so splitting or pruning never breaks tool pairing.
    """
    if not messages:
        return None, []
    idx = 0
    leading_system: dict[str, Any] | None = None
    if messages[0].get("role") == "system":
        leading_system = dict(messages[0])
        idx = 1
    groups: list[list[dict[str, Any]]] = []
    n = len(messages)
    while idx < n:
        msg = messages[idx]
        if msg.get("role") == "assistant" and msg.get("tool_calls"):
            group = [msg]
            idx += 1
            while idx < n and messages[idx].get("role") == "tool":
                group.append(messages[idx])
                idx += 1
            groups.append(group)
        else:
            groups.append([msg])
            idx += 1
    return leading_system, groups


def hash_message_group(group: list[dict[str, Any]]) -> str:
    """Stable short content digest for one atomic message group."""
    raw = json.dumps(group, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()


def _prune_tool_call_arguments(args: str, max_tokens: int) -> str:
    budget = max(16, int(max_tokens))
    if not args or estimate_tokens(args) <= budget:
        return args
    # Preserve valid JSON when the original arguments were a JSON object so strict Jinja templates never fail.
    is_json_dict = False
    try:
        parsed = json.loads(args)
        is_json_dict = isinstance(parsed, dict)
    except Exception:
        is_json_dict = False

    if is_json_dict:
        minimal = json.dumps(
            {"_pruned": True, "_omitted_chars": len(args)},
            ensure_ascii=False,
            separators=(",", ":"),
        )
        if budget <= 36:
            return minimal
        preview_budget = max(16, budget - 28)
        for _ in range(3):
            clipped_preview = clip_text_to_token_budget(
                args,
                preview_budget,
                marker_template=" ... ({omitted} chars pruned) ... ",
            )
            wrapped = json.dumps(
                {
                    "_pruned": True,
                    "_omitted_chars": max(0, len(args) - len(clipped_preview)),
                    "_preview": clipped_preview,
                },
                ensure_ascii=False,
                separators=(",", ":"),
            )
            if estimate_tokens(wrapped) <= budget:
                return wrapped
            if preview_budget <= 16:
                return minimal
            preview_budget = max(16, int(preview_budget * 0.6))
        return minimal

    return clip_text_to_token_budget(
        args,
        budget,
        marker_template=" ... ({omitted} chars pruned) ... ",
    )


def prune_older_tool_outputs(
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None,
    target_tokens: int,
) -> tuple[list[dict[str, Any]], bool]:
    """Stage 1 fast compaction: collapse bulky older tool outputs/arguments while keeping recent turns intact."""
    leading_system, groups = group_chat_messages(messages)
    if len(groups) < 2:
        return messages, False

    def rebuild(sys_msg: dict[str, Any] | None, gs: list[list[dict[str, Any]]]) -> list[dict[str, Any]]:
        out: list[dict[str, Any]] = []
        if sys_msg is not None:
            out.append(sys_msg)
        for g in gs:
            out.extend(g)
        return out

    keep_recent = min(4, max(1, len(groups) // 4))
    if len(groups) - keep_recent < 1:
        keep_recent = len(groups) - 1
    cutoff = len(groups) - keep_recent

    changed = False
    current_groups = [list(g) for g in groups]

    for tool_tok_cap, arg_tok_cap in ((160, 140), (64, 64)):
        next_groups: list[list[dict[str, Any]]] = []
        for g_idx, group in enumerate(current_groups):
            if g_idx >= cutoff:
                next_groups.append(group)
                continue
            pruned_group: list[dict[str, Any]] = []
            for msg in group:
                role = msg.get("role")
                if role == "tool" and isinstance(msg.get("content"), str):
                    content = msg["content"]
                    if estimate_tokens(content) > tool_tok_cap:
                        pruned_text = clip_text_to_token_budget(
                            content,
                            tool_tok_cap,
                            marker_template="\n[... pruned {omitted} chars of earlier tool output ...]\n",
                        )
                        pruned_group.append({**msg, "content": pruned_text})
                        changed = True
                    else:
                        pruned_group.append(msg)
                elif role == "assistant" and isinstance(msg.get("tool_calls"), list):
                    new_calls = []
                    call_changed = False
                    for tc in msg["tool_calls"]:
                        if not isinstance(tc, dict):
                            new_calls.append(tc)
                            continue
                        fn = tc.get("function")
                        if isinstance(fn, dict) and isinstance(fn.get("arguments"), str):
                            pruned_args = _prune_tool_call_arguments(
                                fn["arguments"], arg_tok_cap
                            )
                            if pruned_args != fn["arguments"]:
                                call_changed = True
                                new_calls.append(
                                    {**tc, "function": {**fn, "arguments": pruned_args}}
                                )
                                continue
                        new_calls.append(tc)
                    if call_changed:
                        changed = True
                        pruned_group.append({**msg, "tool_calls": new_calls})
                    else:
                        pruned_group.append(msg)
                else:
                    pruned_group.append(msg)
            next_groups.append(pruned_group)
        current_groups = next_groups
        candidate = rebuild(leading_system, current_groups)
        if estimate_messages_tokens(candidate, tools) <= target_tokens:
            return candidate, changed

    return rebuild(leading_system, current_groups), changed


def split_groups_for_summary(
    leading_system: dict[str, Any] | None,
    groups: list[list[dict[str, Any]]],
    tools: list[dict[str, Any]] | None,
    target_tokens: int,
    *,
    summary_reserve: int | None = None,
    recent_ratio: float = 0.40,
) -> tuple[list[list[dict[str, Any]]], list[list[dict[str, Any]]]]:
    """Split non-system turn groups into (older_to_summarize, recent_to_keep_verbatim).

    Targets a low-water mark (``recent_ratio`` of non-summary headroom) so subsequent
    turns can append under a pinned summary checkpoint up to ``target_tokens`` without
    re-triggering LLM summarization on every turn.
    """
    if len(groups) <= 1:
        return [], list(groups)

    tools_and_sys = estimate_messages_tokens(
        [leading_system] if leading_system else [], tools
    )
    avail = max(512, target_tokens - tools_and_sys)
    if summary_reserve is None:
        effective_summary_reserve = min(1536, max(256, int(avail * 0.15)))
    else:
        effective_summary_reserve = max(0, int(summary_reserve))
    non_summary_avail = max(256, avail - effective_summary_reserve)
    clamped_ratio = max(0.15, min(0.75, float(recent_ratio)))
    recent_budget = max(256, int(non_summary_avail * clamped_ratio))

    kept: list[list[dict[str, Any]]] = []
    kept_tokens = 0
    for group in reversed(groups):
        g_toks = estimate_messages_tokens(group, None)
        if not kept:
            # Keep the most recent turn verbatim unless it alone dominates the window and there are older turns.
            kept.append(group)
            kept_tokens = g_toks
            continue
        if len(kept) < len(groups) - 1 and kept_tokens + g_toks <= recent_budget:
            kept.append(group)
            kept_tokens += g_toks
        else:
            break
    kept.reverse()
    split_at = len(groups) - len(kept)
    return groups[:split_at], kept


def _format_compacted_summary_block(summary_text: str) -> str:
    # Neutralize any literal occurrences of our framing markers inside the LLM summary
    # (e.g. when the user is editing this codebase) so extraction never splits mid-summary.
    body = (
        summary_text.strip()
        .replace(COMPACTED_SUMMARY_HEADER, "(Compacted earlier conversation summary)")
        .replace(COMPACTED_SUMMARY_FOOTER, "(End of compacted summary)")
    )
    return f"{COMPACTED_SUMMARY_HEADER}\n{body}\n{COMPACTED_SUMMARY_FOOTER}"


def _extract_sticky_summary_block(text: str) -> tuple[str | None, str]:
    """Split a leading compacted summary block from any appended user message text."""
    stripped = (text or "").strip()
    if not stripped.startswith(COMPACTED_SUMMARY_HEADER):
        return None, stripped
    # Require the closing footer on its own line boundary so mid-line quotes never split early.
    line_marker = f"\n{COMPACTED_SUMMARY_FOOTER}"
    search_from = len(COMPACTED_SUMMARY_HEADER)
    while True:
        idx = stripped.find(line_marker, search_from)
        if idx < 0:
            break
        end = idx + len(line_marker)
        if end == len(stripped) or stripped[end] in ("\n", "\r"):
            return stripped[:end].strip(), stripped[end:].strip()
        search_from = end
    return stripped, ""


def _clip_sticky_summary_block(summary_block: str, max_tokens: int) -> str:
    """Clip a sticky summary block while preserving its header/footer framing whenever budget permits."""
    if estimate_tokens(summary_block) <= max_tokens:
        return summary_block
    body = summary_block
    if body.startswith(COMPACTED_SUMMARY_HEADER):
        body = body[len(COMPACTED_SUMMARY_HEADER) :].strip()
    if body.endswith(COMPACTED_SUMMARY_FOOTER):
        body = body[: -len(COMPACTED_SUMMARY_FOOTER)].strip()
    framing_toks = estimate_tokens(f"{COMPACTED_SUMMARY_HEADER}\n\n{COMPACTED_SUMMARY_FOOTER}")
    inner_budget = max_tokens - framing_toks
    if inner_budget >= 24 and body:
        clipped_inner = clip_text_to_token_budget(
            body,
            inner_budget,
            marker_template="\n[... truncated {omitted} chars to fit context window ...]\n",
        )
        wrapped = _format_compacted_summary_block(clipped_inner)
        if estimate_tokens(wrapped) <= max_tokens:
            return wrapped
    return clip_text_to_token_budget(
        summary_block,
        max_tokens,
        marker_template="\n[... truncated {omitted} chars to fit context window ...]\n",
    )


def _attach_summary_block_to_tail(
    summary_block: str,
    tail_msgs: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    """Attach ``summary_block`` as the leading user context with strict Jinja-safe role order."""
    out = list(tail_msgs)
    if not out:
        out.append(
            {
                "role": "user",
                "content": f"{summary_block}\n\nContinue with the task based on the compacted summary above.",
            }
        )
    elif out[0].get("role") == "user" and "tool_calls" not in out[0]:
        raw_first = out[0].get("content")
        first_content = raw_first.strip() if isinstance(raw_first, str) else ""
        merged_content = (
            f"{summary_block}\n\n{first_content}" if first_content else summary_block
        )
        out[0] = {**out[0], "content": merged_content}
    else:
        # Strict templates (Gemma/Mistral/Qwen) require a user turn after system before assistant/tool turns.
        out.insert(
            0,
            {
                "role": "user",
                "content": f"{summary_block}\n\n{_COMPACTED_CONTINUATION_NOTE}",
            },
        )
    return out


def assemble_compacted_chat_messages(
    leading_system: dict[str, Any] | None,
    summary_text: str,
    recent_groups: list[list[dict[str, Any]]],
) -> list[dict[str, Any]]:
    """Combine immutable system prompt + rolling summary + recent groups with Jinja-safe role order.

    Keeps ``leading_system`` untouched in ``messages[0]`` so templates that render tool
    definitions immediately after the system message (e.g. Qwen3 ``# Tools``) retain an
    immutable KV/recurrent prefix across re-compactions.
    """
    summary_block = _format_compacted_summary_block(summary_text)
    msgs: list[dict[str, Any]] = []
    if leading_system and isinstance(leading_system.get("content"), str) and leading_system["content"].strip():
        msgs.append(
            {
                "role": "system",
                "content": leading_system["content"].strip(),
            }
        )

    tail_msgs: list[dict[str, Any]] = []
    for group in recent_groups:
        tail_msgs.extend(group)

    msgs.extend(_attach_summary_block_to_tail(summary_block, tail_msgs))
    return _normalize_role_sequence(msgs)


def truncate_messages_to_budget(
    messages: list[dict[str, Any]],
    tools: list[dict[str, Any]] | None,
    target_tokens: int,
) -> tuple[list[dict[str, Any]], bool]:
    """Stage 3 safety clamp: guarantee remaining messages fit inside target_tokens."""
    if estimate_messages_tokens(messages, tools) <= target_tokens:
        return messages, False

    leading_system, groups = group_chat_messages(messages)
    changed = False

    # Preserve any leading compacted summary block across Stage 3a group dropping so a huge
    # newest tool output never causes Stage 3a to erase the rolling conversation summary.
    sticky_summary: str | None = None
    if (
        groups
        and len(groups[0]) == 1
        and groups[0][0].get("role") == "user"
        and "tool_calls" not in groups[0][0]
        and isinstance(groups[0][0].get("content"), str)
    ):
        extracted_summary, remainder = _extract_sticky_summary_block(groups[0][0]["content"])
        if extracted_summary:
            if remainder and remainder != _COMPACTED_CONTINUATION_NOTE:
                sticky_summary = extracted_summary
                groups = [[{**groups[0][0], "content": remainder}], *groups[1:]]
            elif len(groups) > 1:
                sticky_summary = extracted_summary
                groups = groups[1:]

    def rebuild(
        sys_msg: dict[str, Any] | None,
        gs: list[list[dict[str, Any]]],
        summary_blk: str | None = None,
    ) -> list[dict[str, Any]]:
        out: list[dict[str, Any]] = []
        if sys_msg is not None:
            out.append(sys_msg)
        tail: list[dict[str, Any]] = []
        for g in gs:
            tail.extend(g)
        if summary_blk:
            tail = _attach_summary_block_to_tail(summary_blk, tail)
        elif tail and tail[0].get("role") != "user":
            tail.insert(
                0,
                {
                    "role": "user",
                    "content": "Continue based on the context above.",
                },
            )
        out.extend(tail)
        return _normalize_role_sequence(out)

    # 3a. Drop earliest remaining groups while keeping sticky_summary and at least the newest group.
    while len(groups) > 1:
        candidate = rebuild(leading_system, groups, sticky_summary)
        if estimate_messages_tokens(candidate, tools) <= target_tokens:
            return candidate, (changed or candidate != messages)
        groups = groups[1:]
        changed = True

    # Re-check immediately after dropping down to the final remaining group so we do not
    # fall into Stage 3b and needlessly clip an immutable leading_system or sticky_summary.
    if changed:
        candidate = rebuild(leading_system, groups, sticky_summary)
        if estimate_messages_tokens(candidate, tools) <= target_tokens:
            return candidate, True

    # 3b. Fit remaining messages (and clip system prompt / sticky summary proportionally if needed).
    tools_toks = estimate_messages_tokens([], tools)
    avail_for_msgs = max(128, target_tokens - tools_toks)
    if leading_system and isinstance(leading_system.get("content"), str):
        sys_cap = max(128, int(avail_for_msgs * 0.40))
        if estimate_tokens(leading_system["content"]) > sys_cap:
            leading_system = {
                **leading_system,
                "content": clip_text_to_token_budget(
                    leading_system["content"],
                    sys_cap,
                    marker_template="\n[... truncated {omitted} chars to fit context window ...]\n",
                ),
            }
            changed = True

    sys_toks = estimate_messages_tokens([leading_system] if leading_system else [], None)
    body_budget = max(96, avail_for_msgs - sys_toks)

    if sticky_summary:
        sum_cap = max(64, int(body_budget * 0.35))
        if estimate_tokens(sticky_summary) > sum_cap:
            sticky_summary = _clip_sticky_summary_block(sticky_summary, sum_cap)
            changed = True
        body_budget = max(64, body_budget - estimate_tokens(sticky_summary) - 16)

    flat_tail = [msg for g in groups for msg in g]
    if flat_tail:
        per_msg_budget = max(48, (body_budget - len(flat_tail) * 16) // len(flat_tail))
        clipped_tail: list[dict[str, Any]] = []
        for msg in flat_tail:
            new_msg = dict(msg)
            if isinstance(new_msg.get("content"), str) and estimate_tokens(new_msg["content"]) > per_msg_budget:
                new_msg["content"] = clip_text_to_token_budget(
                    new_msg["content"],
                    per_msg_budget,
                    marker_template="\n[... truncated {omitted} chars to fit context window ...]\n",
                )
                changed = True
            if isinstance(new_msg.get("tool_calls"), list):
                call_cap = max(32, per_msg_budget // max(1, len(new_msg["tool_calls"])))
                new_calls = []
                for tc in new_msg["tool_calls"]:
                    if isinstance(tc, dict) and isinstance((tc.get("function") or {}).get("arguments"), str):
                        fn = tc["function"]
                        pruned_args = _prune_tool_call_arguments(fn["arguments"], call_cap)
                        if pruned_args != fn["arguments"]:
                            changed = True
                            new_calls.append({**tc, "function": {**fn, "arguments": pruned_args}})
                            continue
                    new_calls.append(tc)
                new_msg["tool_calls"] = new_calls
            clipped_tail.append(new_msg)
        groups = [clipped_tail]

    final_msgs = rebuild(leading_system, groups, sticky_summary)
    # 3c. Final tight pass on the largest message content or tool_call arguments if framing still pushed over.
    max_passes = min(32, max(8, len(final_msgs) * 2))
    for _ in range(max_passes):
        cur_toks = estimate_messages_tokens(final_msgs, tools)
        if cur_toks <= target_tokens:
            break
        excess = cur_toks - target_tokens + 48
        best_kind = ""
        best_msg_idx = -1
        best_call_idx = -1
        best_toks = 0
        for i, m in enumerate(final_msgs):
            c = m.get("content")
            if isinstance(c, str):
                t = estimate_tokens(c)
                if t > best_toks:
                    best_toks = t
                    best_msg_idx = i
                    best_call_idx = -1
                    best_kind = "content"
            tcs = m.get("tool_calls")
            if isinstance(tcs, list):
                for j, tc in enumerate(tcs):
                    if isinstance(tc, dict) and isinstance((tc.get("function") or {}).get("arguments"), str):
                        t = estimate_tokens(tc["function"]["arguments"])
                        if t > best_toks:
                            best_toks = t
                            best_msg_idx = i
                            best_call_idx = j
                            best_kind = "tool_call"
        if best_msg_idx < 0 or best_toks <= 36:
            break
        new_cap = max(24, best_toks - excess)
        if best_kind == "content":
            clipped = clip_text_to_token_budget(
                final_msgs[best_msg_idx]["content"],
                new_cap,
                marker_template="\n[... truncated {omitted} chars to fit context window ...]\n",
            )
            if clipped == final_msgs[best_msg_idx]["content"]:
                break
            final_msgs[best_msg_idx] = {
                **final_msgs[best_msg_idx],
                "content": clipped,
            }
            changed = True
        else:
            msg = final_msgs[best_msg_idx]
            calls = list(msg["tool_calls"])
            tc = calls[best_call_idx]
            fn = tc["function"]
            pruned_args = _prune_tool_call_arguments(fn["arguments"], new_cap)
            if pruned_args == fn["arguments"]:
                break
            calls[best_call_idx] = {**tc, "function": {**fn, "arguments": pruned_args}}
            final_msgs[best_msg_idx] = {**msg, "tool_calls": calls}
            changed = True
    return final_msgs, changed


def build_compaction_response(
    items: list[dict[str, Any]],
    summary_text: str,
    usage: dict[str, Any],
) -> dict[str, Any]:
    """Assemble an OpenAI-compatible response.compaction JSON payload."""
    output: list[dict[str, Any]] = []
    anchor = extract_anchor_user_item(items)
    if anchor is not None:
        output.append(anchor)
    output.append(
        {
            "id": f"cmp_{secrets.token_hex(12)}",
            "type": "compaction",
            "encrypted_content": encode_compaction_content(summary_text),
        }
    )
    return {
        "id": f"resp_{secrets.token_hex(12)}",
        "object": "response.compaction",
        "created_at": int(time.time()),
        "output": output,
        "usage": normalize_responses_usage(usage),
    }


def _unwrap_custom_tool_input(arguments: str) -> str:
    try:
        parsed = json.loads(arguments)
        if isinstance(parsed, dict) and isinstance(parsed.get("input"), str):
            return parsed["input"]
    except Exception:
        pass
    return arguments


def normalize_responses_usage(
    raw_usage: dict[str, Any] | None,
    *,
    fallback_input_tokens: int = 0,
    fallback_output_tokens: int = 0,
    fallback_reasoning_tokens: int = 0,
) -> dict[str, Any]:
    raw_usage = raw_usage or {}
    input_tokens = raw_usage.get("input_tokens")
    if input_tokens is None:
        input_tokens = raw_usage.get("prompt_tokens")
    if not isinstance(input_tokens, int) or input_tokens < 0:
        input_tokens = max(0, int(fallback_input_tokens))

    output_tokens = raw_usage.get("output_tokens")
    if output_tokens is None:
        output_tokens = raw_usage.get("completion_tokens")
    if not isinstance(output_tokens, int) or output_tokens < 0:
        output_tokens = max(0, int(fallback_output_tokens))

    total_tokens = raw_usage.get("total_tokens")
    if not isinstance(total_tokens, int) or total_tokens < 0:
        total_tokens = input_tokens + output_tokens

    prompt_details = raw_usage.get("input_tokens_details") or raw_usage.get("prompt_tokens_details") or {}
    cached_tokens = prompt_details.get("cached_tokens", 0)
    if not isinstance(cached_tokens, int) or cached_tokens < 0:
        cached_tokens = 0

    out_details = raw_usage.get("output_tokens_details") or raw_usage.get("completion_tokens_details") or {}
    reasoning_tokens = out_details.get("reasoning_tokens", fallback_reasoning_tokens)
    if not isinstance(reasoning_tokens, int) or reasoning_tokens < 0:
        reasoning_tokens = max(0, int(fallback_reasoning_tokens))

    return {
        "input_tokens": input_tokens,
        "input_tokens_details": {"cached_tokens": cached_tokens},
        "output_tokens": output_tokens,
        "output_tokens_details": {"reasoning_tokens": reasoning_tokens},
        "total_tokens": total_tokens,
    }


def make_response_envelope(
    spec: dict[str, Any],
    *,
    response_id: str,
    created_at: int,
    status: str,
    output: list[dict[str, Any]],
    usage: dict[str, Any] | None,
    error: dict[str, Any] | None = None,
    incomplete_details: dict[str, Any] | None = None,
) -> dict[str, Any]:
    return {
        "id": response_id,
        "object": "response",
        "created_at": created_at,
        "status": status,
        "error": error,
        "incomplete_details": incomplete_details,
        "instructions": spec.get("instructions"),
        "max_output_tokens": spec.get("max_output_tokens"),
        "model": spec["model"],
        "output": output,
        "parallel_tool_calls": spec.get("parallel_tool_calls", True),
        "previous_response_id": spec.get("previous_response_id"),
        "reasoning": spec.get("reasoning_echo", {"effort": None, "summary": None}),
        "store": spec.get("store", True),
        "temperature": spec.get("temperature", 1.0),
        "text": spec.get("text_echo", {"format": {"type": "text"}}),
        "tool_choice": spec.get("tool_choice_echo", "auto"),
        "tools": spec.get("tools_echo", []),
        "top_p": spec.get("top_p", 1.0),
        "truncation": "disabled",
        "usage": usage,
        "user": spec.get("user"),
        "metadata": spec.get("metadata", {}),
    }


def chat_completion_to_response(
    chat_json: dict[str, Any],
    spec: dict[str, Any],
    *,
    response_id: str | None = None,
    created_at: int | None = None,
    prefix_output: list[dict[str, Any]] | None = None,
    extra_usage: dict[str, Any] | None = None,
    fallback_input_tokens: int = 0,
    min_input_tokens: int | None = None,
) -> dict[str, Any]:
    """Convert a non-streaming Chat Completions JSON response into an OpenAI Responses API object."""
    rid = response_id or f"resp_{secrets.token_hex(12)}"
    ts = created_at or int(time.time())
    choices = chat_json.get("choices") or []
    first = choices[0] if choices else {}
    message = first.get("message") or {}
    finish_reason = first.get("finish_reason") or "stop"

    output: list[dict[str, Any]] = list(prefix_output or [])
    custom_names: set[str] = spec.get("custom_tool_names") or set()

    reasoning = message.get("reasoning_content") or message.get("reasoning") or ""
    if isinstance(reasoning, str) and reasoning:
        output.append(
            {
                "id": f"rs_{secrets.token_hex(12)}",
                "type": "reasoning",
                "status": "completed",
                "summary": [{"type": "summary_text", "text": reasoning}],
            }
        )

    content = message.get("content")
    if isinstance(content, str) and content:
        output.append(
            {
                "id": f"msg_{secrets.token_hex(12)}",
                "type": "message",
                "status": "completed",
                "role": "assistant",
                "content": [
                    {
                        "type": "output_text",
                        "text": content,
                        "annotations": [],
                    }
                ],
            }
        )

    tool_calls = message.get("tool_calls") or []
    for tc in tool_calls:
        if not isinstance(tc, dict):
            continue
        fn = tc.get("function") or {}
        name = str(fn.get("name") or "")
        args = fn.get("arguments", "{}")
        if not isinstance(args, str):
            args = json.dumps(args, ensure_ascii=False)
        call_id = str(tc.get("id") or f"call_{secrets.token_hex(8)}")
        if name in custom_names:
            output.append(
                {
                    "id": f"ctc_{secrets.token_hex(12)}",
                    "type": "custom_tool_call",
                    "status": "completed",
                    "call_id": call_id,
                    "name": name,
                    "input": _unwrap_custom_tool_input(args),
                }
            )
        else:
            output.append(
                {
                    "id": f"fc_{secrets.token_hex(12)}",
                    "type": "function_call",
                    "status": "completed",
                    "call_id": call_id,
                    "name": name,
                    "arguments": args,
                }
            )

    if not output:
        output.append(
            {
                "id": f"msg_{secrets.token_hex(12)}",
                "type": "message",
                "status": "completed",
                "role": "assistant",
                "content": [{"type": "output_text", "text": "", "annotations": []}],
            }
        )

    raw_usage = dict(chat_json.get("usage") or {})
    if extra_usage:
        base_in = raw_usage.get("prompt_tokens", raw_usage.get("input_tokens", fallback_input_tokens))
        base_out = raw_usage.get("completion_tokens", raw_usage.get("output_tokens", 0))
        raw_usage["input_tokens"] = int(base_in or 0) + int(extra_usage.get("input_tokens") or 0)
        raw_usage["output_tokens"] = int(base_out or 0) + int(extra_usage.get("output_tokens") or 0)
        raw_usage["total_tokens"] = raw_usage["input_tokens"] + raw_usage["output_tokens"]

    est_out = estimate_tokens(
        (reasoning if isinstance(reasoning, str) else "")
        + (content if isinstance(content, str) else "")
        + "".join(str((tc.get("function") or {}).get("arguments", "")) for tc in tool_calls if isinstance(tc, dict))
    )
    usage = normalize_responses_usage(
        raw_usage,
        fallback_input_tokens=fallback_input_tokens,
        fallback_output_tokens=est_out,
        fallback_reasoning_tokens=estimate_tokens(reasoning) if isinstance(reasoning, str) else 0,
    )
    if isinstance(min_input_tokens, int) and min_input_tokens > usage["input_tokens"]:
        usage["input_tokens"] = min_input_tokens
        usage["total_tokens"] = usage["input_tokens"] + usage["output_tokens"]

    is_incomplete = finish_reason == "length"
    return make_response_envelope(
        spec,
        response_id=rid,
        created_at=ts,
        status="incomplete" if is_incomplete else "completed",
        output=output,
        usage=usage,
        incomplete_details={"reason": "max_output_tokens"} if is_incomplete else None,
    )


def format_sse_event(event_type: str, payload: dict[str, Any]) -> bytes:
    return (
        f"event: {event_type}\n"
        f"data: {json.dumps(payload, ensure_ascii=False, separators=(',', ':'))}\n\n"
    ).encode("utf-8")


class ColabOneResponsesStreamTranslator:
    """Stateful translator from Chat Completions SSE chunks into spec-compliant OpenAI Responses API SSE events."""

    def __init__(
        self,
        spec: dict[str, Any],
        *,
        fallback_input_tokens: int = 0,
        prefix_output: list[dict[str, Any]] | None = None,
        extra_usage: dict[str, Any] | None = None,
        min_input_tokens: int | None = None,
    ):
        self.spec = spec
        self.response_id = f"resp_{secrets.token_hex(12)}"
        self.created_at = int(time.time())
        self.seq = 0
        self.output: list[dict[str, Any]] = []
        self.custom_names: set[str] = spec.get("custom_tool_names") or set()
        self.fallback_input_tokens = fallback_input_tokens
        self.extra_usage = extra_usage
        self.min_input_tokens = min_input_tokens
        self.prefix_output = list(prefix_output or [])

        self.reasoning_id: str | None = None
        self.reasoning_index: int | None = None
        self.reasoning_text = ""
        self.reasoning_closed = False

        self.msg_id: str | None = None
        self.msg_index: int | None = None
        self.msg_text = ""
        self.msg_closed = False

        self.tool_calls: dict[int, dict[str, Any]] = {}
        self.finish_reason: str | None = None
        self.raw_usage: dict[str, Any] | None = None
        self.final_response: dict[str, Any] | None = None

    def _emit(self, event_type: str, data: dict[str, Any]) -> bytes:
        payload = {"type": event_type, "sequence_number": self.seq, **data}
        self.seq += 1
        return format_sse_event(event_type, payload)

    def start(self) -> list[bytes]:
        events: list[bytes] = []
        initial = make_response_envelope(
            self.spec,
            response_id=self.response_id,
            created_at=self.created_at,
            status="in_progress",
            output=[],
            usage=None,
        )
        events.append(self._emit("response.created", {"response": initial}))
        events.append(self._emit("response.in_progress", {"response": initial}))
        for item in self.prefix_output:
            idx = len(self.output)
            self.output.append(item)
            events.append(
                self._emit("response.output_item.added", {"output_index": idx, "item": item})
            )
            events.append(
                self._emit("response.output_item.done", {"output_index": idx, "item": item})
            )
        return events

    def _close_reasoning(self) -> list[bytes]:
        if self.reasoning_id is None or self.reasoning_closed:
            return []
        self.reasoning_closed = True
        idx = self.reasoning_index
        part = {"type": "summary_text", "text": self.reasoning_text}
        item = {
            "id": self.reasoning_id,
            "type": "reasoning",
            "status": "completed",
            "summary": [part],
        }
        self.output[idx] = item
        return [
            self._emit(
                "response.reasoning_summary_text.done",
                {
                    "item_id": self.reasoning_id,
                    "output_index": idx,
                    "summary_index": 0,
                    "text": self.reasoning_text,
                },
            ),
            self._emit(
                "response.reasoning_summary_part.done",
                {
                    "item_id": self.reasoning_id,
                    "output_index": idx,
                    "summary_index": 0,
                    "part": part,
                },
            ),
            self._emit("response.output_item.done", {"output_index": idx, "item": item}),
        ]

    def _close_message(self) -> list[bytes]:
        if self.msg_id is None or self.msg_closed:
            return []
        self.msg_closed = True
        idx = self.msg_index
        part = {"type": "output_text", "text": self.msg_text, "annotations": []}
        item = {
            "id": self.msg_id,
            "type": "message",
            "status": "completed",
            "role": "assistant",
            "content": [part],
        }
        self.output[idx] = item
        return [
            self._emit(
                "response.output_text.done",
                {
                    "item_id": self.msg_id,
                    "output_index": idx,
                    "content_index": 0,
                    "text": self.msg_text,
                },
            ),
            self._emit(
                "response.content_part.done",
                {
                    "item_id": self.msg_id,
                    "output_index": idx,
                    "content_index": 0,
                    "part": part,
                },
            ),
            self._emit("response.output_item.done", {"output_index": idx, "item": item}),
        ]

    def feed_chunk(self, chunk: dict[str, Any]) -> list[bytes]:
        events: list[bytes] = []
        if isinstance(chunk.get("usage"), dict):
            self.raw_usage = chunk["usage"]
        choices = chunk.get("choices") or []
        if not choices:
            return events
        first = choices[0] or {}
        if first.get("finish_reason"):
            self.finish_reason = first["finish_reason"]
        delta = first.get("delta") or {}

        reasoning_delta = delta.get("reasoning_content") or delta.get("reasoning")
        if isinstance(reasoning_delta, str) and reasoning_delta:
            if self.reasoning_id is None or self.reasoning_closed:
                self.reasoning_id = f"rs_{secrets.token_hex(12)}"
                self.reasoning_index = len(self.output)
                self.reasoning_text = ""
                self.reasoning_closed = False
                item = {
                    "id": self.reasoning_id,
                    "type": "reasoning",
                    "status": "in_progress",
                    "summary": [],
                }
                self.output.append(item)
                events.append(
                    self._emit(
                        "response.output_item.added",
                        {"output_index": self.reasoning_index, "item": item},
                    )
                )
                events.append(
                    self._emit(
                        "response.reasoning_summary_part.added",
                        {
                            "item_id": self.reasoning_id,
                            "output_index": self.reasoning_index,
                            "summary_index": 0,
                            "part": {"type": "summary_text", "text": ""},
                        },
                    )
                )
            self.reasoning_text += reasoning_delta
            events.append(
                self._emit(
                    "response.reasoning_summary_text.delta",
                    {
                        "item_id": self.reasoning_id,
                        "output_index": self.reasoning_index,
                        "summary_index": 0,
                        "delta": reasoning_delta,
                    },
                )
            )

        text_delta = delta.get("content")
        if isinstance(text_delta, str) and text_delta:
            events.extend(self._close_reasoning())
            if self.msg_id is None or self.msg_closed:
                self.msg_id = f"msg_{secrets.token_hex(12)}"
                self.msg_index = len(self.output)
                self.msg_text = ""
                self.msg_closed = False
                item = {
                    "id": self.msg_id,
                    "type": "message",
                    "status": "in_progress",
                    "role": "assistant",
                    "content": [],
                }
                self.output.append(item)
                events.append(
                    self._emit(
                        "response.output_item.added",
                        {"output_index": self.msg_index, "item": item},
                    )
                )
                events.append(
                    self._emit(
                        "response.content_part.added",
                        {
                            "item_id": self.msg_id,
                            "output_index": self.msg_index,
                            "content_index": 0,
                            "part": {"type": "output_text", "text": "", "annotations": []},
                        },
                    )
                )
            self.msg_text += text_delta
            events.append(
                self._emit(
                    "response.output_text.delta",
                    {
                        "item_id": self.msg_id,
                        "output_index": self.msg_index,
                        "content_index": 0,
                        "delta": text_delta,
                    },
                )
            )

        tc_deltas = delta.get("tool_calls") or []
        if tc_deltas:
            events.extend(self._close_reasoning())
            events.extend(self._close_message())
            for tc in tc_deltas:
                if not isinstance(tc, dict):
                    continue
                raw_idx = tc.get("index")
                tc_idx = int(raw_idx) if isinstance(raw_idx, int) else 0
                fn = tc.get("function") or {}
                entry = self.tool_calls.get(tc_idx)
                if entry is None:
                    name = str(fn.get("name") or "")
                    call_id = str(tc.get("id") or f"call_{secrets.token_hex(8)}")
                    is_custom = name in self.custom_names
                    item_id = (
                        f"ctc_{secrets.token_hex(12)}"
                        if is_custom
                        else f"fc_{secrets.token_hex(12)}"
                    )
                    out_idx = len(self.output)
                    entry = {
                        "item_id": item_id,
                        "output_index": out_idx,
                        "call_id": call_id,
                        "name": name,
                        "is_custom": is_custom,
                        "arguments": "",
                        "buffered_args": [],
                        "added_emitted": False,
                    }
                    self.tool_calls[tc_idx] = entry
                    if is_custom:
                        item = {
                            "id": item_id,
                            "type": "custom_tool_call",
                            "status": "in_progress",
                            "call_id": call_id,
                            "name": name,
                            "input": "",
                        }
                        self.output.append(item)
                    else:
                        item = {
                            "id": item_id,
                            "type": "function_call",
                            "status": "in_progress",
                            "call_id": call_id,
                            "name": name,
                            "arguments": "",
                        }
                        self.output.append(item)
                else:
                    if isinstance(tc.get("id"), str) and tc["id"]:
                        entry["call_id"] = tc["id"]
                        self.output[entry["output_index"]]["call_id"] = tc["id"]
                    if isinstance(fn.get("name"), str) and fn["name"]:
                        entry["name"] += fn["name"]
                        if entry["name"] in self.custom_names and not entry["added_emitted"]:
                            entry["is_custom"] = True
                            entry["item_id"] = f"ctc_{secrets.token_hex(12)}"
                            entry["buffered_args"].clear()
                            self.output[entry["output_index"]] = {
                                "id": entry["item_id"],
                                "type": "custom_tool_call",
                                "status": "in_progress",
                                "call_id": entry["call_id"],
                                "name": entry["name"],
                                "input": "",
                            }
                        else:
                            self.output[entry["output_index"]]["name"] = entry["name"]

                arg_delta = fn.get("arguments")
                has_arg_delta = isinstance(arg_delta, str) and bool(arg_delta)
                could_be_custom_prefix = (
                    not entry["is_custom"]
                    and bool(entry["name"])
                    and not has_arg_delta
                    and any(
                        cn.startswith(entry["name"]) and cn != entry["name"]
                        for cn in self.custom_names
                    )
                )
                if (
                    not entry["is_custom"]
                    and not entry["added_emitted"]
                    and entry["name"]
                    and not could_be_custom_prefix
                ):
                    entry["added_emitted"] = True
                    events.append(
                        self._emit(
                            "response.output_item.added",
                            {
                                "output_index": entry["output_index"],
                                "item": self.output[entry["output_index"]],
                            },
                        )
                    )
                    for buffered in entry["buffered_args"]:
                        events.append(
                            self._emit(
                                "response.function_call_arguments.delta",
                                {
                                    "item_id": entry["item_id"],
                                    "output_index": entry["output_index"],
                                    "delta": buffered,
                                },
                            )
                        )
                    entry["buffered_args"].clear()

                if has_arg_delta:
                    entry["arguments"] += arg_delta
                    if not entry["is_custom"]:
                        if entry["added_emitted"]:
                            events.append(
                                self._emit(
                                    "response.function_call_arguments.delta",
                                    {
                                        "item_id": entry["item_id"],
                                        "output_index": entry["output_index"],
                                        "delta": arg_delta,
                                    },
                                )
                            )
                        else:
                            entry["buffered_args"].append(arg_delta)
        return events

    def finish(self) -> list[bytes]:
        events: list[bytes] = []
        events.extend(self._close_reasoning())
        events.extend(self._close_message())

        for tc_idx in sorted(self.tool_calls):
            entry = self.tool_calls[tc_idx]
            out_idx = entry["output_index"]
            if entry["is_custom"]:
                unwrapped = _unwrap_custom_tool_input(entry["arguments"])
                in_progress_item = {
                    "id": entry["item_id"],
                    "type": "custom_tool_call",
                    "status": "in_progress",
                    "call_id": entry["call_id"],
                    "name": entry["name"],
                    "input": "",
                }
                events.append(
                    self._emit(
                        "response.output_item.added",
                        {"output_index": out_idx, "item": in_progress_item},
                    )
                )
                if unwrapped:
                    events.append(
                        self._emit(
                            "response.custom_tool_call_input.delta",
                            {
                                "item_id": entry["item_id"],
                                "output_index": out_idx,
                                "delta": unwrapped,
                            },
                        )
                    )
                events.append(
                    self._emit(
                        "response.custom_tool_call_input.done",
                        {
                            "item_id": entry["item_id"],
                            "output_index": out_idx,
                            "input": unwrapped,
                        },
                    )
                )
                done_item = {
                    "id": entry["item_id"],
                    "type": "custom_tool_call",
                    "status": "completed",
                    "call_id": entry["call_id"],
                    "name": entry["name"],
                    "input": unwrapped,
                }
                self.output[out_idx] = done_item
                events.append(
                    self._emit(
                        "response.output_item.done",
                        {"output_index": out_idx, "item": done_item},
                    )
                )
            else:
                if not entry["added_emitted"]:
                    entry["added_emitted"] = True
                    events.append(
                        self._emit(
                            "response.output_item.added",
                            {"output_index": out_idx, "item": self.output[out_idx]},
                        )
                    )
                    for buffered in entry.get("buffered_args") or ():
                        events.append(
                            self._emit(
                                "response.function_call_arguments.delta",
                                {
                                    "item_id": entry["item_id"],
                                    "output_index": out_idx,
                                    "delta": buffered,
                                },
                            )
                        )
                    entry["buffered_args"] = []
                events.append(
                    self._emit(
                        "response.function_call_arguments.done",
                        {
                            "item_id": entry["item_id"],
                            "output_index": out_idx,
                            "name": entry["name"],
                            "arguments": entry["arguments"],
                        },
                    )
                )
                done_item = {
                    "id": entry["item_id"],
                    "type": "function_call",
                    "status": "completed",
                    "call_id": entry["call_id"],
                    "name": entry["name"],
                    "arguments": entry["arguments"],
                }
                self.output[out_idx] = done_item
                events.append(
                    self._emit(
                        "response.output_item.done",
                        {"output_index": out_idx, "item": done_item},
                    )
                )

        raw_usage = dict(self.raw_usage or {})
        if self.extra_usage:
            base_in = raw_usage.get(
                "prompt_tokens",
                raw_usage.get("input_tokens", self.fallback_input_tokens),
            )
            base_out = raw_usage.get("completion_tokens", raw_usage.get("output_tokens", 0))
            raw_usage["input_tokens"] = int(base_in or 0) + int(
                self.extra_usage.get("input_tokens") or 0
            )
            raw_usage["output_tokens"] = int(base_out or 0) + int(
                self.extra_usage.get("output_tokens") or 0
            )
            raw_usage["total_tokens"] = raw_usage["input_tokens"] + raw_usage["output_tokens"]

        est_out = estimate_tokens(
            self.reasoning_text
            + self.msg_text
            + "".join(entry["arguments"] for entry in self.tool_calls.values())
        )
        usage = normalize_responses_usage(
            raw_usage,
            fallback_input_tokens=self.fallback_input_tokens,
            fallback_output_tokens=est_out,
            fallback_reasoning_tokens=estimate_tokens(self.reasoning_text),
        )
        if (
            isinstance(self.min_input_tokens, int)
            and self.min_input_tokens > usage["input_tokens"]
        ):
            usage["input_tokens"] = self.min_input_tokens
            usage["total_tokens"] = usage["input_tokens"] + usage["output_tokens"]
        is_incomplete = self.finish_reason == "length"
        final = make_response_envelope(
            self.spec,
            response_id=self.response_id,
            created_at=self.created_at,
            status="incomplete" if is_incomplete else "completed",
            output=list(self.output),
            usage=usage,
            incomplete_details={"reason": "max_output_tokens"} if is_incomplete else None,
        )
        self.final_response = final
        events.append(
            self._emit(
                "response.incomplete" if is_incomplete else "response.completed",
                {"response": final},
            )
        )
        return events

    def fail(self, message: str, *, code: str = "server_error") -> bytes:
        failed = make_response_envelope(
            self.spec,
            response_id=self.response_id,
            created_at=self.created_at,
            status="failed",
            output=list(self.output),
            usage=None,
            error={"code": code or "server_error", "message": message},
        )
        self.final_response = failed
        return self._emit("response.failed", {"response": failed})
