"""Zero-dependency RFC 6455 WebSocket client for ComfyUI real-time progress.

Runs on the remote Colab runtime alongside ComfyUI to stream node execution,
sampler step progress (`progress` and v0.46+ `progress_state`), and execution
errors over `/ws?clientId=...` without any third-party WebSocket dependency.
"""

from __future__ import annotations

import base64
import hashlib
import inspect
import json
import math
import secrets
import selectors
import socket
import struct
import threading
import time
from collections import deque
from typing import Any, Callable
from urllib.parse import quote

WS_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

WS_OP_CONTINUATION = 0x0
WS_OP_TEXT = 0x1
WS_OP_BINARY = 0x2
WS_OP_CLOSE = 0x8
WS_OP_PING = 0x9
WS_OP_PONG = 0xA

MAX_HANDSHAKE_BYTES = 16 * 1024
MAX_WS_FRAME_BYTES = 32 * 1024 * 1024
MAX_WS_BUFFER_BYTES = 32 * 1024 * 1024
MAX_PENDING_BUFFER_ITEMS = 256
MAX_PENDING_BUFFER_BYTES = 4 * 1024 * 1024
MAX_WS_FRAGMENTS = 1024

__all__ = [
    "WS_GUID",
    "WS_OP_CONTINUATION",
    "WS_OP_TEXT",
    "WS_OP_BINARY",
    "WS_OP_CLOSE",
    "WS_OP_PING",
    "WS_OP_PONG",
    "compute_ws_accept",
    "encode_ws_client_frame",
    "decode_ws_server_frame",
    "parse_comfy_ws_message",
    "ColabOneComfyWsMonitor",
]


def compute_ws_accept(sec_websocket_key: str) -> str:
    """Compute the RFC 6455 Sec-WebSocket-Accept header value for a client key."""
    digest = hashlib.sha1(
        (sec_websocket_key.strip() + WS_GUID).encode("ascii")
    ).digest()
    return base64.b64encode(digest).decode("ascii")


def _apply_ws_mask(data: bytes, mask_key: bytes) -> bytes:
    """XOR-mask or unmask payload bytes using a 4-byte RFC 6455 masking key."""
    n = len(data)
    if n == 0:
        return b""
    repeated = (mask_key * ((n // 4) + 1))[:n]
    return (
        int.from_bytes(data, "big") ^ int.from_bytes(repeated, "big")
    ).to_bytes(n, "big")


def encode_ws_client_frame(
    payload: bytes,
    opcode: int = WS_OP_TEXT,
    mask_key: bytes | None = None,
    *,
    fin: bool = True,
) -> bytes:
    """Encode an RFC 6455 client-to-server masked WebSocket frame.

    Supports 7-bit (< 126), 16-bit (126..65535), and 64-bit (>= 65536) payload
    lengths.
    """
    if not isinstance(opcode, int) or isinstance(opcode, bool) or not (0 <= opcode <= 0xF):
        raise ValueError(f"Invalid WebSocket opcode: {opcode!r}")

    if isinstance(payload, str):
        data = payload.encode("utf-8")
    else:
        data = bytes(payload)

    if mask_key is None:
        key = secrets.token_bytes(4)
    else:
        key = bytes(mask_key)
        if len(key) != 4:
            raise ValueError("WebSocket mask_key must be exactly 4 bytes")

    first_byte = (0x80 if fin else 0x00) | opcode
    length = len(data)
    header = bytearray([first_byte])
    if length < 126:
        header.append(0x80 | length)
    elif length <= 0xFFFF:
        header.append(0x80 | 126)
        header.extend(struct.pack("!H", length))
    else:
        header.append(0x80 | 127)
        header.extend(struct.pack("!Q", length))

    header.extend(key)
    if length:
        header.extend(_apply_ws_mask(data, key))
    return bytes(header)


def _decode_ws_frame_ex(
    buf: bytearray,
    *,
    max_payload_bytes: int | None = MAX_WS_FRAME_BYTES,
) -> tuple[bool, int, bytes] | None:
    """Incrementally decode one RFC 6455 frame as ``(fin, opcode, payload)``.

    Consumes the decoded frame bytes from ``buf`` in place. Returns ``None``
    without mutating ``buf`` if the frame is not yet complete.
    """
    if len(buf) < 2:
        return None

    b0 = buf[0]
    b1 = buf[1]
    fin = bool(b0 & 0x80)
    rsv = b0 & 0x70
    opcode = b0 & 0x0F
    if rsv != 0:
        raise ValueError(f"Invalid WebSocket RSV bits: 0x{rsv:02x}")
    if opcode >= 0x8 and not fin:
        raise ValueError(f"Fragmented WebSocket control frame (opcode 0x{opcode:x})")
    masked = bool(b1 & 0x80)
    length_code = b1 & 0x7F
    if opcode >= 0x8 and length_code > 125:
        raise ValueError(f"WebSocket control frame payload too large (opcode 0x{opcode:x})")
    offset = 2

    if length_code < 126:
        payload_len = length_code
    elif length_code == 126:
        if len(buf) < offset + 2:
            return None
        (payload_len,) = struct.unpack_from("!H", buf, offset)
        offset += 2
    else:
        if len(buf) < offset + 8:
            return None
        (payload_len,) = struct.unpack_from("!Q", buf, offset)
        offset += 8
        if payload_len & (1 << 63):
            raise ValueError("Invalid 64-bit WebSocket payload length")

    if max_payload_bytes is not None and payload_len > max_payload_bytes:
        raise ValueError(
            f"WebSocket frame payload length {payload_len} exceeds limit {max_payload_bytes}"
        )

    mask_key: bytes | None = None
    if masked:
        if len(buf) < offset + 4:
            return None
        mask_key = bytes(buf[offset : offset + 4])
        offset += 4

    frame_end = offset + payload_len
    if len(buf) < frame_end:
        return None

    payload = bytes(buf[offset:frame_end])
    del buf[:frame_end]
    if mask_key is not None:
        payload = _apply_ws_mask(payload, mask_key)
    return fin, opcode, payload


def decode_ws_server_frame(
    buf: bytearray,
    *,
    max_payload_bytes: int | None = MAX_WS_FRAME_BYTES,
) -> tuple[int, bytes] | None:
    """Incrementally decode one RFC 6455 frame from ``buf``.

    Returns ``(opcode, payload)`` and consumes the frame bytes from ``buf``,
    or returns ``None`` if ``buf`` does not yet contain a complete frame.
    Supports both unmasked and masked frames as well as 7/16/64-bit lengths.
    """
    decoded = _decode_ws_frame_ex(buf, max_payload_bytes=max_payload_bytes)
    if decoded is None:
        return None
    _, opcode, payload = decoded
    return opcode, payload


def _as_finite_float(value: Any) -> float | None:
    if isinstance(value, bool) or value is None:
        return None
    if isinstance(value, (int, float)):
        num = float(value)
        return num if math.isfinite(num) else None
    try:
        num = float(value)
    except (TypeError, ValueError):
        return None
    return num if math.isfinite(num) else None


def _as_int_step(value: Any) -> int | None:
    num = _as_finite_float(value)
    if num is None:
        return None
    if isinstance(value, int) and not isinstance(value, bool):
        return value
    return int(round(num))


def _clamp_fraction(value: float, max_val: float) -> float | None:
    if max_val <= 0:
        return None
    frac = value / max_val
    if frac < 0.0:
        return 0.0
    if frac > 1.0:
        return 1.0
    return frac


def _resolve_node_title(
    node_id: str,
    node_titles: dict[str, str],
    display_node_id: str = "",
) -> str:
    if not node_id and not display_node_id:
        return ""
    if node_id and node_id in node_titles and node_titles[node_id]:
        return str(node_titles[node_id])
    if display_node_id and display_node_id in node_titles and node_titles[display_node_id]:
        return str(node_titles[display_node_id])
    return node_id or display_node_id


def _extract_message_prompt_id(data: dict[str, Any]) -> str:
    raw = data.get("prompt_id")
    if raw is not None:
        pid = str(raw).strip()
        if pid:
            return pid
    nodes = data.get("nodes")
    if isinstance(nodes, dict):
        for entry in nodes.values():
            if isinstance(entry, dict) and entry.get("prompt_id") is not None:
                pid = str(entry.get("prompt_id")).strip()
                if pid:
                    return pid
    elif isinstance(nodes, list):
        for entry in nodes:
            if isinstance(entry, dict) and entry.get("prompt_id") is not None:
                pid = str(entry.get("prompt_id")).strip()
                if pid:
                    return pid
    return ""


def _parse_progress_state(
    data: dict[str, Any],
    titles: dict[str, str],
) -> dict[str, Any] | None:
    raw_nodes = data.get("nodes")
    items: list[tuple[str, dict[str, Any]]] = []
    if isinstance(raw_nodes, dict):
        for key, entry in raw_nodes.items():
            if isinstance(entry, dict):
                items.append((str(key), entry))
    elif isinstance(raw_nodes, list):
        for entry in raw_nodes:
            if isinstance(entry, dict):
                items.append((str(entry.get("node_id") or ""), entry))
    else:
        return None

    total_nodes = len(items)
    completed_count = 0
    running: list[
        tuple[int, str, str, int | None, int | None, float | None]
    ] = []
    for key, entry in items:
        state = str(entry.get("state") or "").strip().lower()
        if state in ("finished", "completed", "done"):
            completed_count += 1
            continue
        if state != "running":
            continue
        node_id = str(entry.get("node_id") or key or "")
        display_id = str(entry.get("display_node_id") or "")
        val_f = _as_finite_float(entry.get("value"))
        max_f = _as_finite_float(entry.get("max"))
        if val_f is not None and max_f is not None and max_f > 0:
            step_int = _as_int_step(entry.get("value"))
            max_int = _as_int_step(entry.get("max"))
            frac = _clamp_fraction(val_f, max_f)
            # Prefer multi-step running nodes (e.g. samplers) over wrapper/prep nodes with max <= 1.
            priority = 2 if max_f > 1.0 else 1
            running.append((priority, node_id, display_id, step_int, max_int, frac))
        else:
            running.append((0, node_id, display_id, None, None, None))

    if not running:
        return None

    best = max(running, key=lambda item: item[0])
    priority, node_id, display_id, step_int, max_int, frac = best
    node_title = _resolve_node_title(node_id, titles, display_id)
    phase = "sampling" if priority >= 2 else "executing"
    if priority < 2:
        # Avoid letting a single-step prep/loader node (1/1) pin progress to 100%.
        if total_nodes > 1:
            running_fraction = frac if frac is not None else 0.0
            weighted = (completed_count + running_fraction) / total_nodes
            frac = min(0.97, max(0.0, weighted))
        else:
            frac = None
    return {
        "phase": phase,
        "node_id": node_id,
        "node_title": node_title,
        "step": step_int,
        "max_steps": max_int,
        "fraction": frac,
    }


def _load_ws_json(raw_text: str) -> dict[str, Any] | None:
    if not isinstance(raw_text, str) or not raw_text.strip():
        return None
    try:
        payload = json.loads(raw_text)
    except (ValueError, TypeError):
        return None
    return payload if isinstance(payload, dict) else None


def _parse_comfy_ws_payload(
    payload: dict[str, Any],
    prompt_id: str = "",
    node_titles: dict[str, str] | None = None,
    *,
    require_prompt_id: bool = False,
) -> tuple[dict[str, Any] | None, str]:
    msg_type = payload.get("type")
    data = payload.get("data")
    if not isinstance(msg_type, str) or not isinstance(data, dict):
        return None, ""

    target_pid = str(prompt_id or "").strip()
    msg_pid = _extract_message_prompt_id(data)
    if target_pid:
        if msg_pid:
            if msg_pid != target_pid:
                return None, msg_pid
        elif require_prompt_id or msg_type != "progress":
            # Older ComfyUI builds occasionally omitted prompt_id on `progress`
            # frames, whereas execution lifecycle events always carry it.
            # Buffered pre-bind replay always requires an exact prompt_id match.
            return None, msg_pid

    titles = node_titles if isinstance(node_titles, dict) else {}

    if msg_type == "execution_start":
        return (
            {
                "phase": "starting",
                "node_id": "",
                "node_title": "",
                "step": None,
                "max_steps": None,
                "fraction": 0.05,
            },
            msg_pid,
        )

    if msg_type == "executing":
        raw_node = data.get("node")
        if raw_node is None:
            return (
                {
                    "phase": "done",
                    "node_id": "",
                    "node_title": "",
                    "step": None,
                    "max_steps": None,
                    "fraction": 1.0,
                },
                msg_pid,
            )
        node_id = str(raw_node)
        raw_display = data.get("display_node")
        display_id = "" if raw_display is None else str(raw_display)
        node_title = _resolve_node_title(node_id, titles, display_id)
        return (
            {
                "phase": "executing",
                "node_id": node_id,
                "node_title": node_title,
                "step": None,
                "max_steps": None,
                "fraction": None,
            },
            msg_pid,
        )

    if msg_type == "executed":
        raw_node = data.get("node")
        node_id = "" if raw_node is None else str(raw_node)
        raw_display = data.get("display_node")
        display_id = "" if raw_display is None else str(raw_display)
        node_title = _resolve_node_title(node_id, titles, display_id)
        raw_output = data.get("output")
        return (
            {
                "phase": "executed",
                "node_id": node_id,
                "node_title": node_title,
                "step": None,
                "max_steps": None,
                "fraction": None,
                "has_output": isinstance(raw_output, dict) and bool(raw_output),
            },
            msg_pid,
        )

    if msg_type == "execution_success":
        return (
            {
                "phase": "done",
                "node_id": "",
                "node_title": "",
                "step": None,
                "max_steps": None,
                "fraction": 1.0,
            },
            msg_pid,
        )

    if msg_type == "progress":
        val_f = _as_finite_float(data.get("value"))
        max_f = _as_finite_float(data.get("max"))
        if val_f is None or max_f is None:
            return None, msg_pid
        step_int = _as_int_step(data.get("value"))
        max_int = _as_int_step(data.get("max"))
        raw_node = data.get("node")
        node_id = "" if raw_node is None else str(raw_node)
        node_title = _resolve_node_title(node_id, titles)
        return (
            {
                "phase": "sampling",
                "node_id": node_id,
                "node_title": node_title,
                "step": step_int,
                "max_steps": max_int,
                "fraction": _clamp_fraction(val_f, max_f) if max_f > 1.0 else None,
            },
            msg_pid,
        )

    if msg_type == "progress_state":
        return _parse_progress_state(data, titles), msg_pid

    if msg_type == "execution_error":
        node_id = "" if data.get("node_id") is None else str(data.get("node_id"))
        node_type = "" if data.get("node_type") is None else str(data.get("node_type"))
        exc_msg = str(
            data.get("exception_message")
            or data.get("message")
            or data.get("exception_type")
            or "未知执行错误"
        ).strip()
        if node_id and node_type:
            prefix = f"节点 {node_id}({node_type}): "
        elif node_id:
            prefix = f"节点 {node_id}: "
        elif node_type:
            prefix = f"{node_type}: "
        else:
            prefix = ""
        error_text = f"{prefix}{exc_msg}"[:600]
        node_title = _resolve_node_title(node_id, titles) or node_type
        return (
            {
                "phase": "error",
                "node_id": node_id,
                "node_title": node_title,
                "node_type": node_type,
                "step": None,
                "max_steps": None,
                "fraction": None,
                "error": error_text,
            },
            msg_pid,
        )

    if msg_type == "execution_interrupted":
        node_id = "" if data.get("node_id") is None else str(data.get("node_id"))
        node_type = "" if data.get("node_type") is None else str(data.get("node_type"))
        if node_id and node_type:
            error_text = f"节点 {node_id}({node_type}): 工作流执行已中断"
        elif node_id:
            error_text = f"节点 {node_id}: 工作流执行已中断"
        else:
            error_text = "工作流执行已中断"
        node_title = _resolve_node_title(node_id, titles) or node_type
        return (
            {
                "phase": "error",
                "node_id": node_id,
                "node_title": node_title,
                "node_type": node_type,
                "step": None,
                "max_steps": None,
                "fraction": None,
                "error": error_text,
            },
            msg_pid,
        )

    if msg_type == "execution_cached":
        raw_nodes = data.get("nodes")
        cached_nodes = (
            [str(item) for item in raw_nodes] if isinstance(raw_nodes, list) else []
        )
        return (
            {
                "phase": "cached",
                "node_id": "",
                "node_title": "",
                "step": None,
                "max_steps": None,
                "fraction": None,
                "nodes": cached_nodes,
            },
            msg_pid,
        )

    return None, msg_pid


def parse_comfy_ws_message(
    raw_text: str,
    prompt_id: str = "",
    node_titles: dict[str, str] | None = None,
) -> dict[str, Any] | None:
    """Parse a ComfyUI `/ws` JSON text message into a normalized progress dict.

    Returns ``None`` for unrecognized, malformed, or non-matching ``prompt_id``
    messages. When ``prompt_id`` is empty, messages are not filtered by prompt.
    """
    payload = _load_ws_json(raw_text)
    if payload is None:
        return None
    event, _ = _parse_comfy_ws_payload(payload, prompt_id, node_titles)
    return event


def _accepts_argument(fn: Callable[..., Any]) -> bool:
    """Return True if ``fn`` can be invoked with one positional argument."""
    try:
        sig = inspect.signature(fn)
    except (TypeError, ValueError):
        return True
    for param in sig.parameters.values():
        if param.kind in (
            inspect.Parameter.POSITIONAL_ONLY,
            inspect.Parameter.POSITIONAL_OR_KEYWORD,
            inspect.Parameter.VAR_POSITIONAL,
        ):
            return True
    return False


def _invoke_callback(fn: Callable[..., Any] | None, arg: Any) -> None:
    if fn is None:
        return
    try:
        if _accepts_argument(fn):
            fn(arg)
        else:
            fn()
    except Exception:
        pass


def _parse_handshake_response(head_bytes: bytes, expected_accept: str) -> bool:
    try:
        text = head_bytes.decode("iso-8859-1")
    except UnicodeDecodeError:
        return False
    lines = text.split("\r\n")
    if not lines:
        return False
    status_parts = lines[0].split(" ", 2)
    if (
        len(status_parts) < 2
        or not status_parts[0].upper().startswith("HTTP/1.")
        or status_parts[1] != "101"
    ):
        return False

    headers: dict[str, list[str]] = {}
    for line in lines[1:]:
        if ":" not in line:
            continue
        name, value = line.split(":", 1)
        headers.setdefault(name.strip().lower(), []).append(value.strip())

    upgrade_tokens: set[str] = set()
    for val in headers.get("upgrade", []):
        for token in val.split(","):
            tok = token.strip().lower()
            if tok:
                upgrade_tokens.add(tok)
    if "websocket" not in upgrade_tokens:
        return False

    conn_tokens: set[str] = set()
    for val in headers.get("connection", []):
        for token in val.split(","):
            tok = token.strip().lower()
            if tok:
                conn_tokens.add(tok)
    if "upgrade" not in conn_tokens:
        return False

    accept_values = headers.get("sec-websocket-accept", [])
    if len(accept_values) != 1:
        return False
    return secrets.compare_digest(
        accept_values[0].encode("latin-1"), expected_accept.encode("latin-1")
    )


class ColabOneComfyWsMonitor:
    """Thread-safe RFC 6455 WebSocket monitor for ComfyUI execution progress."""

    def __init__(
        self,
        host: str,
        port: int,
        client_id: str,
        prompt_id: str = "",
        node_titles: dict[str, str] | None = None,
        on_progress: Callable[[dict[str, Any]], None] | None = None,
        on_error: Callable[..., None] | None = None,
        on_done: Callable[..., None] | None = None,
        connect_timeout: float = 3.0,
    ) -> None:
        # Support both `(host, port, client_id, prompt_id, node_titles, ...)`
        # and `(host, port, client_id, node_titles, on_progress, ...)` positional calls.
        if isinstance(prompt_id, dict):
            if callable(node_titles):
                on_done = on_error
                on_error = on_progress
                on_progress = node_titles
            node_titles = prompt_id
            prompt_id = ""

        self.host = str(host)
        self.port = int(port)
        self.client_id = str(client_id or "")
        self.connect_timeout = max(0.1, float(connect_timeout))

        self._lock = threading.RLock()
        self._dispatch_lock = threading.RLock()
        self._send_lock = threading.Lock()
        self._stop_event = threading.Event()

        self._prompt_id = str(prompt_id or "").strip()
        self._node_titles: dict[str, str] = (
            {str(k): str(v) for k, v in node_titles.items()}
            if isinstance(node_titles, dict)
            else {}
        )
        self._on_progress = on_progress
        self._on_error = on_error
        self._on_done = on_done

        self._sock: socket.socket | None = None
        self._thread: threading.Thread | None = None
        self._connected = False
        self._last_event: dict[str, Any] | None = None
        self._error_message = ""
        self._done_fired = False
        self._had_error = False
        self._error_prompt_id = ""
        self._last_done_prompt_id = ""
        self._pending_buffer: deque[str] = deque()
        self._pending_buffer_sizes: deque[int] = deque()
        self._pending_buffer_bytes: int = 0

    def _clear_pending_locked(self) -> None:
        self._pending_buffer.clear()
        self._pending_buffer_sizes.clear()
        self._pending_buffer_bytes = 0

    def _append_pending_locked(self, text: str) -> None:
        size = len(text.encode("utf-8"))
        if size > MAX_PENDING_BUFFER_BYTES:
            return
        while self._pending_buffer and (
            len(self._pending_buffer) >= MAX_PENDING_BUFFER_ITEMS
            or self._pending_buffer_bytes + size > MAX_PENDING_BUFFER_BYTES
        ):
            self._pending_buffer.popleft()
            evicted_size = self._pending_buffer_sizes.popleft()
            self._pending_buffer_bytes = max(
                0, self._pending_buffer_bytes - evicted_size
            )
        self._pending_buffer.append(text)
        self._pending_buffer_sizes.append(size)
        self._pending_buffer_bytes += size

    @property
    def connected(self) -> bool:
        with self._lock:
            return self._connected

    @property
    def last_event(self) -> dict[str, Any] | None:
        with self._lock:
            return dict(self._last_event) if self._last_event is not None else None

    @property
    def error_message(self) -> str:
        with self._lock:
            return self._error_message

    @property
    def prompt_id(self) -> str:
        with self._lock:
            return self._prompt_id

    def set_prompt_id(self, prompt_id: str) -> None:
        """Update the active prompt_id filter thread-safely.

        Allows connecting `/ws?clientId=...` before `POST /prompt` so early
        events are never missed, then binding the returned `prompt_id` and
        replaying only buffered messages belonging to that prompt.
        """
        new_pid = str(prompt_id or "").strip()
        actions_to_fire: list[tuple[str, Any]] = []
        with self._dispatch_lock:
            with self._lock:
                prev_pid = self._prompt_id
                self._prompt_id = new_pid
                if new_pid != prev_pid:
                    self._last_event = None
                if not new_pid or new_pid != self._last_done_prompt_id:
                    self._done_fired = False
                if not new_pid or new_pid != self._error_prompt_id:
                    self._error_message = ""
                    self._had_error = False
                    self._error_prompt_id = ""
                if not new_pid or self._stop_event.is_set():
                    self._clear_pending_locked()
                    return
                buffered = list(self._pending_buffer)
                self._clear_pending_locked()
                titles = dict(self._node_titles)
                for raw_text in buffered:
                    parsed = _load_ws_json(raw_text)
                    if parsed is None:
                        continue
                    event, raw_msg_pid = _parse_comfy_ws_payload(
                        parsed,
                        new_pid,
                        titles,
                        require_prompt_id=True,
                    )
                    if event is None:
                        continue
                    action = self._record_event_locked(event, raw_msg_pid)
                    if action is not None:
                        actions_to_fire.append(action)
            for action in actions_to_fire:
                if self._stop_event.is_set():
                    break
                self._fire_action(action)

    def set_node_titles(self, node_titles: dict[str, str] | None) -> None:
        with self._lock:
            self._node_titles = (
                {str(k): str(v) for k, v in node_titles.items()}
                if isinstance(node_titles, dict)
                else {}
            )

    def start(self) -> bool:
        """Connect to ComfyUI `/ws`, complete RFC 6455 handshake, and start reader thread.

        Returns ``True`` on success, or ``False`` cleanly if unreachable or if
        the WebSocket upgrade handshake fails. Never raises.
        """
        with self._lock:
            if self._connected and self._thread is not None and self._thread.is_alive():
                return True
            self._stop_event.clear()
            self._last_event = None
            self._done_fired = False
            self._error_message = ""
            self._had_error = False
            self._error_prompt_id = ""
            self._last_done_prompt_id = ""
            self._clear_pending_locked()

        if "\r" in self.host or "\n" in self.host or not self.host.strip():
            return False

        sock: socket.socket | None = None
        try:
            deadline = time.monotonic() + self.connect_timeout
            sock = socket.create_connection(
                (self.host, self.port),
                timeout=self.connect_timeout,
            )
            try:
                sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
            except OSError:
                pass

            ws_key = base64.b64encode(secrets.token_bytes(16)).decode("ascii")
            expected_accept = compute_ws_accept(ws_key)
            if ":" in self.host and not self.host.startswith("["):
                host_header = f"[{self.host}]:{self.port}"
            else:
                host_header = f"{self.host}:{self.port}"
            path = (
                f"/ws?clientId={quote(self.client_id, safe='')}"
                if self.client_id
                else "/ws"
            )
            request = (
                f"GET {path} HTTP/1.1\r\n"
                f"Host: {host_header}\r\n"
                f"Upgrade: websocket\r\n"
                f"Connection: Upgrade\r\n"
                f"Sec-WebSocket-Key: {ws_key}\r\n"
                f"Sec-WebSocket-Version: 13\r\n"
                f"\r\n"
            ).encode("ascii")

            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("WebSocket handshake timed out")
            sock.settimeout(remaining)
            sock.sendall(request)

            raw_buf = bytearray()
            header_end = -1
            while header_end < 0:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError("WebSocket handshake timed out")
                sock.settimeout(remaining)
                chunk = sock.recv(4096)
                if not chunk:
                    raise ConnectionError("EOF during WebSocket handshake")
                raw_buf.extend(chunk)
                header_end = raw_buf.find(b"\r\n\r\n")
                if header_end < 0 and len(raw_buf) > MAX_HANDSHAKE_BYTES:
                    raise ValueError("WebSocket handshake response header too large")

            head_bytes = bytes(raw_buf[:header_end])
            leftover = bytearray(raw_buf[header_end + 4 :])
            if not _parse_handshake_response(head_bytes, expected_accept):
                raise ValueError("Invalid WebSocket upgrade response")

            sock.setblocking(False)
            thread = threading.Thread(
                target=self._reader_loop,
                args=(sock, leftover),
                name=f"colabone-comfy-ws-{self.client_id[:16] or 'anon'}",
                daemon=True,
            )
            with self._lock:
                if self._stop_event.is_set():
                    raise RuntimeError("WebSocket monitor stopped during startup")
                self._sock = sock
                self._thread = thread
                self._connected = True
            thread.start()
            return True
        except Exception:
            if sock is not None:
                try:
                    sock.close()
                except OSError:
                    pass
            with self._lock:
                self._sock = None
                self._thread = None
                self._connected = False
            return False

    def stop(self) -> None:
        """Stop the background reader thread and close the WebSocket cleanly."""
        self._stop_event.set()
        try:
            close_payload = struct.pack("!H", 1000)
            self._send_frame(
                encode_ws_client_frame(close_payload, opcode=WS_OP_CLOSE),
                timeout=0.2,
            )
        except Exception:
            pass

        with self._lock:
            thread = self._thread
        if thread is not None and thread is not threading.current_thread():
            thread.join(timeout=1.5)

        self._close_socket()
        with self._lock:
            self._connected = False
            if self._thread is thread:
                self._thread = None

    def __enter__(self) -> ColabOneComfyWsMonitor:
        self.start()
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.stop()

    def _close_socket(self, target_sock: socket.socket | None = None) -> None:
        with self._lock:
            sock = self._sock
            if target_sock is not None and sock is not target_sock:
                sock = target_sock
            elif sock is not None:
                self._sock = None
        if sock is not None:
            try:
                sock.close()
            except OSError:
                pass

    def _send_frame(self, frame: bytes, timeout: float = 0.5) -> bool:
        with self._send_lock:
            with self._lock:
                sock = self._sock
            if sock is None:
                return False
            view = memoryview(frame)
            sent = 0
            deadline = time.monotonic() + timeout
            while sent < len(view):
                try:
                    written = sock.send(view[sent:])
                    if written <= 0:
                        return False
                    sent += written
                except BlockingIOError:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        return False
                    try:
                        with selectors.DefaultSelector() as sel:
                            sel.register(sock, selectors.EVENT_WRITE)
                            if not sel.select(timeout=remaining):
                                return False
                    except (OSError, ValueError):
                        return False
                except OSError:
                    return False
            return True

    def _reader_loop(self, sock: socket.socket, initial_buf: bytearray) -> None:
        buf = bytearray(initial_buf)
        frag_opcode: int | None = None
        frag_chunks: list[bytes] = []
        frag_size = 0
        frag_count = 0

        def drain_frames() -> bool:
            nonlocal frag_opcode, frag_size, frag_count
            while not self._stop_event.is_set():
                try:
                    decoded = _decode_ws_frame_ex(
                        buf,
                        max_payload_bytes=MAX_WS_FRAME_BYTES,
                    )
                except ValueError:
                    return False
                if decoded is None:
                    return True

                fin, opcode, payload = decoded
                if opcode == WS_OP_PING:
                    self._send_frame(
                        encode_ws_client_frame(payload[:125], opcode=WS_OP_PONG),
                        timeout=0.5,
                    )
                    continue
                if opcode == WS_OP_PONG:
                    continue
                if opcode == WS_OP_CLOSE:
                    echo_payload = payload[:2] if len(payload) >= 2 else b""
                    self._send_frame(
                        encode_ws_client_frame(echo_payload, opcode=WS_OP_CLOSE),
                        timeout=0.2,
                    )
                    return False

                if opcode == WS_OP_CONTINUATION:
                    if frag_opcode is None:
                        continue
                    frag_count += 1
                    if frag_count > MAX_WS_FRAGMENTS:
                        frag_opcode = None
                        frag_chunks.clear()
                        frag_size = 0
                        frag_count = 0
                        return False
                    if not payload and not fin:
                        continue
                    if payload:
                        frag_size += len(payload)
                        if (
                            frag_size > MAX_WS_FRAME_BYTES
                            or len(frag_chunks) >= MAX_WS_FRAGMENTS
                        ):
                            frag_opcode = None
                            frag_chunks.clear()
                            frag_size = 0
                            frag_count = 0
                            return False
                        frag_chunks.append(payload)
                    if fin:
                        full_payload = b"".join(frag_chunks)
                        completed_opcode = frag_opcode
                        frag_opcode = None
                        frag_chunks.clear()
                        frag_size = 0
                        frag_count = 0
                        self._handle_message(completed_opcode, full_payload)
                    continue

                if opcode in (WS_OP_TEXT, WS_OP_BINARY):
                    if frag_opcode is not None:
                        # RFC 6455 section 5.4: a new data frame cannot interleave
                        # with an unfinished fragmented message; fail connection.
                        frag_opcode = None
                        frag_chunks.clear()
                        frag_size = 0
                        frag_count = 0
                        return False
                    if fin:
                        frag_opcode = None
                        frag_chunks.clear()
                        frag_size = 0
                        frag_count = 0
                        self._handle_message(opcode, payload)
                    else:
                        frag_opcode = opcode
                        frag_chunks.clear()
                        if payload:
                            frag_chunks.append(payload)
                        frag_size = len(payload)
                        frag_count = 1
                    continue
            return False

        sel = selectors.DefaultSelector()
        try:
            sel.register(sock, selectors.EVENT_READ)
            if buf and not drain_frames():
                return

            while not self._stop_event.is_set():
                try:
                    ready = sel.select(timeout=0.05)
                except (OSError, ValueError):
                    break
                if not ready:
                    continue
                try:
                    chunk = sock.recv(65536)
                except BlockingIOError:
                    continue
                except OSError:
                    break
                if not chunk:
                    break
                buf.extend(chunk)
                if not drain_frames():
                    break
                if len(buf) > MAX_WS_BUFFER_BYTES:
                    break
        finally:
            try:
                sel.unregister(sock)
            except Exception:
                pass
            try:
                sel.close()
            except Exception:
                pass
            self._close_socket(sock)
            with self._lock:
                self._connected = False

    def _record_event_locked(
        self, event: dict[str, Any], raw_msg_pid: str
    ) -> tuple[str, Any] | None:
        phase = event.get("phase")
        if phase == "error":
            self._last_event = dict(event)
            err_text = str(event.get("error") or "工作流执行出错")
            self._error_message = err_text
            self._had_error = True
            eff_pid = raw_msg_pid or self._prompt_id
            self._error_prompt_id = eff_pid
            return ("error", err_text)
        if phase == "done":
            if self._had_error and (
                not raw_msg_pid
                or not self._error_prompt_id
                or raw_msg_pid == self._error_prompt_id
            ):
                # ComfyUI emits executing(node=None) right after execution_error or
                # execution_interrupted; preserve the error state and suppress on_done.
                return None
            self._last_event = dict(event)
            if raw_msg_pid:
                self._last_done_prompt_id = raw_msg_pid
            if not self._done_fired:
                self._done_fired = True
                return ("done", dict(event))
            return None
        self._last_event = dict(event)
        return ("progress", dict(event))

    def _fire_action(self, action: tuple[str, Any]) -> None:
        kind, payload = action
        if kind == "progress":
            _invoke_callback(self._on_progress, payload)
        elif kind == "error":
            _invoke_callback(self._on_error, payload)
        elif kind == "done":
            _invoke_callback(self._on_done, payload)

    def _handle_message(self, opcode: int, payload: bytes) -> None:
        if opcode == WS_OP_BINARY:
            # ComfyUI binary preview frames are ignored cleanly.
            return
        if opcode != WS_OP_TEXT:
            return

        text = payload.decode("utf-8", errors="replace")
        action: tuple[str, Any] | None = None
        with self._dispatch_lock:
            if self._stop_event.is_set():
                return
            with self._lock:
                prompt_id = self._prompt_id
                if not prompt_id:
                    self._append_pending_locked(text)
                    return
                titles = dict(self._node_titles)

            parsed = _load_ws_json(text)
            if parsed is None:
                return
            event, raw_msg_pid = _parse_comfy_ws_payload(parsed, prompt_id, titles)
            if event is None:
                return

            with self._lock:
                if self._stop_event.is_set():
                    return
                action = self._record_event_locked(event, raw_msg_pid)

            if action is not None and not self._stop_event.is_set():
                self._fire_action(action)
