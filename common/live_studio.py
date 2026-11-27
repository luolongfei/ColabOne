"""Data-only Live Studio contracts, binary framing, VRAM budget, and directive parser.

Runs cleanly with Python stdlib and Pydantic v2 only (zero PyTorch or ML imports).
Shared between the remote Colab service and contract tests.
"""

from __future__ import annotations

import ipaddress
import re
import socket
import struct
from pathlib import PurePosixPath
from typing import Any, Callable, Literal
from urllib.parse import urlparse

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from common.comfyui import ComfyCanvasConfig, validate_colabone_hf_token

# ---------------------------------------------------------------------------
# Protocol & transport constants
# ---------------------------------------------------------------------------

COLABONE_LIVE_PORT = 8190
COLABONE_LIVE_WS_PATH = "/_colabone_live/ws"
COLABONE_LIVE_HEALTH_PATH = "/_colabone_live/health"
COLABONE_LIVE_COMPANION_PATH = "/_colabone_live/remote"
COLABONE_LIVE_COMPANION_REDEEM_PATH = "/_colabone_live/remote/redeem"

COLABONE_LIVE_FRAME_MIC_PCM = 0x01
COLABONE_LIVE_FRAME_VIDEO = 0x02
COLABONE_LIVE_FRAME_AUDIO_PCM = 0x03
COLABONE_LIVE_FRAME_FMP4 = 0x04
COLABONE_LIVE_FRAME_CAMERA_JPEG = 0x05

COLABONE_LIVE_VALID_FRAME_TYPES = frozenset(
    {
        COLABONE_LIVE_FRAME_MIC_PCM,
        COLABONE_LIVE_FRAME_VIDEO,
        COLABONE_LIVE_FRAME_AUDIO_PCM,
        COLABONE_LIVE_FRAME_FMP4,
        COLABONE_LIVE_FRAME_CAMERA_JPEG,
    }
)

COLABONE_LIVE_MAX_CAMERA_FRAME_BYTES = 512 * 1024
COLABONE_LIVE_CAMERA_MODES: tuple[str, ...] = (
    "off",
    "vision",
    "motion",
    "vision_motion",
)
COLABONE_LIVE_WS_REVOKED_CLOSE_CODE = 4403
COLABONE_LIVE_WS_REPLACED_CLOSE_CODE = 4409

COLABONE_LIVE_HEADER_FORMAT = "!BIQ"
COLABONE_LIVE_HEADER_SIZE = 13

COLABONE_LIVE_DIRECTOR_STATES: tuple[str, ...] = (
    "IDLE",
    "LISTENING",
    "THINKING",
    "SPEAKING",
    "PERFORMING",
)

COLABONE_LIVE_AUDIO_IN_RATE = 16000
COLABONE_LIVE_AUDIO_OUT_RATE = 24000
COLABONE_LIVE_AUDIO_CHUNK_MS = 20
COLABONE_LIVE_AUDIO_IN_CHUNK_BYTES = 640
COLABONE_LIVE_AUDIO_OUT_CHUNK_BYTES = 960

COLABONE_LIVE_SAFETY_MARGIN_MIB = 3072
COLABONE_LIVE_NVENC_BUFFER_MIB = 1024
COLABONE_LIVE_DEFAULT_LOCAL_LLM_ESTIMATE_MIB = 6144

COLABONE_LIVE_MAX_PORTRAIT_BYTES = 16 * 1024 * 1024
COLABONE_LIVE_MAX_CLIP_BYTES = 128 * 1024 * 1024
COLABONE_LIVE_MAX_BGM_BYTES = 32 * 1024 * 1024

COLABONE_LIVE_CLIP_EXTENSIONS = frozenset({"mp4", "webm", "mov", "mkv"})
COLABONE_LIVE_BGM_EXTENSIONS = frozenset({"mp3", "wav", "ogg", "flac", "m4a"})
COLABONE_LIVE_IMAGE_EXTENSIONS = frozenset({"png", "jpg", "jpeg", "webp"})

_SAFE_ID_RE = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$", re.ASCII)
_SAFE_REVISION_RE = re.compile(r"^[a-zA-Z0-9._/-]{0,120}$", re.ASCII)
_RESOLUTION_RE = re.compile(r"^(\d{3,4})x(\d{3,4})$", re.ASCII)
_ASSET_ID_RE = re.compile(r"^(?:[a-f0-9]{32})?$", re.ASCII)

_LOOPBACK_HOSTS = frozenset({"127.0.0.1", "localhost", "::1"})
_BLOCKED_METADATA_HOSTS = frozenset(
    {
        "metadata.google.internal",
        "metadata.goog",
        "instance-data",
        "metadata",
    }
)

_BEARER_SECRET_RE = re.compile(r"(?i)\bBearer\s+[^\s'\"`,;}\]]+")
_OPENAI_KEY_RE = re.compile(r"(?<![A-Za-z0-9_])sk-[A-Za-z0-9._-]{6,}")
_HF_TOKEN_RE = re.compile(
    r"\bhf_(?!(?:token|hub|home|cache|transfer|endpoint|xet)(?:_|\b))[A-Za-z0-9_-]{6,}\b"
)
_COLABONE_KEY_RE = re.compile(r"\bco_[A-Za-z0-9_-]{20,}\b")
_RTMP_USERINFO_RE = re.compile(r"(?i)\b(rtmps?://)([^/\s@]+)@")
_RTMP_SECRET_RE = re.compile(r"(?i)\b(rtmps?://[^/\s]+/[^/\s]+/)([^\s'\"`?]+)")
_RTMP_SINGLE_SEG_SECRET_RE = re.compile(
    r"(?i)\b(rtmps?://[^/\s]+/)([^/\s'\"`?]+)(?=$|[\s'\"`?])"
)
_RTMP_QUERY_RE = re.compile(r"(?i)(\brtmps?://[^\s'\"`?]+)\?([^\s'\"`]+)")

_NON_ROUTABLE_IPV4_NETWORKS: tuple[ipaddress.IPv4Network, ...] = (
    ipaddress.IPv4Network("0.0.0.0/8"),
    ipaddress.IPv4Network("100.64.0.0/10"),      # CGNAT (RFC 6598)
    ipaddress.IPv4Network("192.0.0.0/24"),       # IETF protocol assignments
    ipaddress.IPv4Network("192.0.2.0/24"),       # TEST-NET-1 documentation
    ipaddress.IPv4Network("198.18.0.0/15"),      # Benchmarking (RFC 2544)
    ipaddress.IPv4Network("198.51.100.0/24"),    # TEST-NET-2 documentation
    ipaddress.IPv4Network("203.0.113.0/24"),     # TEST-NET-3 documentation
    ipaddress.IPv4Network("240.0.0.0/4"),        # Reserved / future use
)
_NON_ROUTABLE_IPV6_NETWORKS: tuple[ipaddress.IPv6Network, ...] = (
    ipaddress.IPv6Network("100::/64"),           # Discard-only (RFC 6666)
    ipaddress.IPv6Network("64:ff9b::/96"),       # NAT64 well-known prefix
    ipaddress.IPv6Network("64:ff9b:1::/48"),     # NAT64 local-use prefix
    ipaddress.IPv6Network("2001::/32"),          # Teredo
    ipaddress.IPv6Network("2001:2::/48"),        # Benchmarking
    ipaddress.IPv6Network("2001:db8::/32"),      # Documentation
    ipaddress.IPv6Network("2002::/16"),          # 6to4
    ipaddress.IPv6Network("fec0::/10"),          # Deprecated site-local
)

_DIRECTIVE_TAG_RE = re.compile(
    r"^\[\s*(ACTION|CARD|MOOD)\s*:\s*([^\[\]\r\n]{0,120})\]$",
    re.IGNORECASE,
)
_DIRECTIVE_NAMES = ("ACTION", "CARD", "MOOD")
_DIRECTIVE_PREFIXES = ("ACTION:", "CARD:", "MOOD:")
_HARD_PUNCT = frozenset("。！？；!?;")
_SOFT_PUNCT = frozenset("，、：,:")
_CLOSING_PUNCT = frozenset("”’\"'」』）》】）)]")


# ---------------------------------------------------------------------------
# Pure validation & redaction helpers
# ---------------------------------------------------------------------------


def validate_colabone_live_id(value: str, label: str = "ID") -> str:
    """Validate a safe identifier usable as a filename/directory segment."""
    if not isinstance(value, str):
        raise ValueError(f"{label} 格式不合法")
    cleaned = value.strip()
    if (
        not cleaned
        or cleaned in (".", "..")
        or ".." in cleaned
        or "/" in cleaned
        or "\\" in cleaned
        or "\x00" in cleaned
        or not _SAFE_ID_RE.fullmatch(cleaned)
    ):
        raise ValueError(f"{label} 不合法: {value!r}")
    return cleaned


def validate_colabone_live_relative_path(value: str, label: str = "路径") -> str:
    """Validate an optional safe relative POSIX path with no traversal or drive letters."""
    if value is None or value == "":
        return ""
    if not isinstance(value, str):
        raise ValueError(f"{label} 格式不合法")
    cleaned = value.strip()
    if not cleaned:
        return ""
    if (
        len(cleaned) > 512
        or "\\" in cleaned
        or ":" in cleaned
        or "\x00" in cleaned
        or cleaned.startswith("/")
        or cleaned.endswith("/")
    ):
        raise ValueError(f"{label} 不合法")
    posix = PurePosixPath(cleaned)
    if posix.is_absolute() or any(part in ("", ".", "..") for part in posix.parts):
        raise ValueError(f"{label} 包含非法路径段")
    return posix.as_posix()


def _has_disallowed_control_chars(value: str) -> bool:
    """Return True if value contains C0 (< 0x20), DEL (0x7f), or C1 (0x80-0x9f) controls."""
    for ch in value:
        code = ord(ch)
        if code < 0x20 or code == 0x7F or (0x80 <= code <= 0x9F):
            return True
    return False


def _validate_resolution_bounds(value: str) -> str:
    match = _RESOLUTION_RE.fullmatch(str(value or "").strip())
    if not match:
        raise ValueError("分辨率格式必须为 宽x高（例如 720x1280）")
    w, h = int(match.group(1)), int(match.group(2))
    if not (256 <= w <= 2560 and 256 <= h <= 2560):
        raise ValueError("分辨率宽高必须在 256 到 2560 之间")
    if w % 2 != 0 or h % 2 != 0:
        raise ValueError("分辨率宽高必须为偶数")
    return f"{w}x{h}"


def _is_ip_globally_routable(ip_obj: ipaddress.IPv4Address | ipaddress.IPv6Address) -> bool:
    effective: ipaddress.IPv4Address | ipaddress.IPv6Address = ip_obj
    if isinstance(effective, ipaddress.IPv6Address) and effective.ipv4_mapped is not None:
        effective = effective.ipv4_mapped
    if not (
        effective.is_global
        and not effective.is_private
        and not effective.is_loopback
        and not effective.is_link_local
        and not effective.is_multicast
        and not effective.is_reserved
        and not effective.is_unspecified
    ):
        return False
    if isinstance(effective, ipaddress.IPv6Address):
        if effective.is_site_local:
            return False
        if any(effective in net for net in _NON_ROUTABLE_IPV6_NETWORKS):
            return False
    else:
        if any(effective in net for net in _NON_ROUTABLE_IPV4_NETWORKS):
            return False
    return True


def _is_non_canonical_ip_literal(host: str) -> bool:
    """Detect hex (0x...), integer-only, octal, or partial-octet IPv4 literals."""
    if not host:
        return False
    try:
        socket.inet_aton(host)
        return True
    except OSError:
        pass
    labels = host.split(".")
    if not labels:
        return True
    if any(lbl == "" for lbl in labels):
        return True
    if all(
        lbl.isdigit()
        or (
            lbl.startswith("0x")
            and len(lbl) > 2
            and all(ch in "0123456789abcdef" for ch in lbl[2:])
        )
        for lbl in labels
    ):
        return True
    # DNS TLDs are never purely numeric.
    if labels[-1].isdigit():
        return True
    return False


def _validate_public_host(raw_host: str, *, label: str) -> None:
    if not raw_host or "%" in raw_host:
        raise ValueError(f"{label}主机名不合法")
    host = raw_host[:-1] if raw_host.endswith(".") else raw_host
    if not host or host.startswith(".") or host.endswith("."):
        raise ValueError(f"{label}缺少有效主机名")

    if (
        host in _LOOPBACK_HOSTS
        or host in _BLOCKED_METADATA_HOSTS
        or host.endswith(".internal")
        or host.endswith(".local")
        or host.endswith(".localhost")
    ):
        raise ValueError(f"{label}禁止访问回环、内网或元数据域名")

    ip_obj: ipaddress.IPv4Address | ipaddress.IPv6Address | None = None
    try:
        ip_obj = ipaddress.ip_address(host)
    except ValueError:
        ip_obj = None

    if ip_obj is not None:
        if not _is_ip_globally_routable(ip_obj):
            raise ValueError(f"{label}禁止访问非公网、私有、回环、CGNAT 或元数据 IP 地址")
        return

    if _is_non_canonical_ip_literal(host):
        raise ValueError(f"{label}禁止使用非标准 IP 地址字面量")


def _has_empty_port_colon(netloc: str) -> bool:
    hostport = (netloc or "").rsplit("@", 1)[-1]
    if not hostport:
        return True
    if hostport.startswith("["):
        return hostport.endswith("]:") or hostport.endswith(":")
    return hostport.endswith(":")


def validate_colabone_external_llm_url(url: str) -> str:
    """Validate external OpenAI-compatible LLM base URL against SSRF rules.

    Allows HTTPS URLs to public hosts (blocking private/CGNAT/link-local/metadata/
    loopback/non-canonical IP literals) and HTTP URLs strictly to exact loopback
    hosts (127.0.0.1, localhost, ::1).
    """
    if not isinstance(url, str):
        raise ValueError("外部 LLM 接口地址必须为字符串")
    if _has_disallowed_control_chars(url):
        raise ValueError("外部 LLM 接口地址包含非法空白或控制字符")
    cleaned = url.strip()
    if not cleaned or len(cleaned) > 512:
        raise ValueError("外部 LLM 接口地址为空或过长")
    if any(ch.isspace() for ch in cleaned):
        raise ValueError("外部 LLM 接口地址包含非法空白或控制字符")

    parsed = urlparse(cleaned)
    scheme = (parsed.scheme or "").lower()
    if scheme not in ("https", "http"):
        raise ValueError("外部 LLM 接口仅支持 https:// 或本地回环 http:// 地址")
    if parsed.username or parsed.password:
        raise ValueError("外部 LLM 接口地址不允许携带内嵌账号或密码")
    if parsed.params or ";" in (parsed.path or ""):
        raise ValueError("外部 LLM 接口地址不允许携带路径参数")
    if parsed.query:
        raise ValueError("外部 LLM 接口地址不允许携带查询参数")
    if parsed.fragment:
        raise ValueError("外部 LLM 接口地址不支持片段标识符")
    if _has_empty_port_colon(parsed.netloc):
        raise ValueError("外部 LLM 接口端口不合法")

    raw_host = (parsed.hostname or "").strip().lower()
    if not raw_host:
        raise ValueError("外部 LLM 接口地址缺少主机名")

    if scheme == "http":
        if raw_host not in _LOOPBACK_HOSTS:
            raise ValueError("非显式本地回环地址（127.0.0.1 / localhost / ::1）必须使用 HTTPS 协议")
    else:
        _validate_public_host(raw_host, label="外部 LLM 接口")

    try:
        port = parsed.port
    except ValueError as exc:
        raise ValueError("外部 LLM 接口端口不合法") from exc
    if port is not None and not (1 <= port <= 65535):
        raise ValueError("外部 LLM 接口端口超出范围")

    return cleaned.rstrip("/")


def validate_colabone_external_llm_resolved_ips(
    url: str,
    *,
    resolver: Callable[..., Any] | None = None,
) -> list[str]:
    """Resolve external LLM hostname and verify every resolved IP is globally routable.

    Returns the deduplicated list of validated canonical IP strings (or an empty
    list for explicit loopback HTTP endpoints).
    """
    cleaned = validate_colabone_external_llm_url(url)
    parsed = urlparse(cleaned)
    scheme = (parsed.scheme or "").lower()
    raw_host = (parsed.hostname or "").strip().lower()
    if scheme == "http" and raw_host in _LOOPBACK_HOSTS:
        return []
    host = raw_host[:-1] if raw_host.endswith(".") else raw_host
    port = parsed.port or (443 if scheme == "https" else 80)
    resolve_fn = resolver or socket.getaddrinfo

    try:
        infos = resolve_fn(host, port)
    except OSError as exc:
        raise ValueError(f"外部 LLM 域名解析失败: {host}") from exc
    if not infos:
        raise ValueError(f"外部 LLM 域名未解析到有效公网 IP: {host}")

    resolved_ips: list[str] = []
    for entry in infos:
        try:
            sockaddr = entry[4]
            ip_raw = str(sockaddr[0]).split("%", 1)[0]
            ip_obj = ipaddress.ip_address(ip_raw)
        except Exception as exc:
            raise ValueError("外部 LLM 域名返回了非法 IP 解析结果") from exc
        if not _is_ip_globally_routable(ip_obj):
            raise ValueError("外部 LLM 域名解析到了非公网、内网或保留 IP 地址，已拦截")
        canonical = str(ip_obj)
        if canonical not in resolved_ips:
            resolved_ips.append(canonical)
    if not resolved_ips:
        raise ValueError(f"外部 LLM 域名未解析到有效公网 IP: {host}")
    return resolved_ips


def validate_colabone_live_rtmp_url(url: str) -> str:
    """Validate optional RTMP/RTMPS push URL and block internal/metadata/CGNAT hosts."""
    if url is None or url == "":
        return ""
    if not isinstance(url, str):
        raise ValueError("推流地址必须为字符串")
    if _has_disallowed_control_chars(url):
        raise ValueError("推流地址包含非法字符或过长")
    cleaned = url.strip()
    if not cleaned:
        return ""
    if len(cleaned) > 1024 or any(ch.isspace() for ch in cleaned):
        raise ValueError("推流地址包含非法字符或过长")
    parsed = urlparse(cleaned)
    if (parsed.scheme or "").lower() not in ("rtmp", "rtmps"):
        raise ValueError("推流地址必须以 rtmp:// 或 rtmps:// 开头")
    if parsed.params or ";" in (parsed.path or ""):
        raise ValueError("推流地址不允许携带路径参数")
    if _has_empty_port_colon(parsed.netloc):
        raise ValueError("推流地址端口不合法")
    raw_host = (parsed.hostname or "").strip().lower()
    if not raw_host:
        raise ValueError("推流地址缺少主机名")
    _validate_public_host(raw_host, label="推流地址")
    try:
        port = parsed.port
    except ValueError as exc:
        raise ValueError("推流地址端口不合法") from exc
    if port is not None and not (1 <= port <= 65535):
        raise ValueError("推流地址端口超出范围")
    return cleaned


def validate_colabone_live_rtmp_resolved_ips(
    url: str,
    *,
    resolver: Callable[..., Any] | None = None,
) -> list[str]:
    """Resolve RTMP/RTMPS hostname and verify every resolved IP is globally routable."""
    cleaned = validate_colabone_live_rtmp_url(url)
    if not cleaned:
        return []
    parsed = urlparse(cleaned)
    scheme = (parsed.scheme or "").lower()
    raw_host = (parsed.hostname or "").strip().lower()
    host = raw_host[:-1] if raw_host.endswith(".") else raw_host
    port = parsed.port or (443 if scheme == "rtmps" else 1935)
    resolve_fn = resolver or socket.getaddrinfo

    try:
        infos = resolve_fn(host, port)
    except OSError as exc:
        raise ValueError(f"RTMP 推流域名解析失败: {host}") from exc
    if not infos:
        raise ValueError(f"RTMP 推流域名未解析到有效公网 IP: {host}")

    resolved_ips: list[str] = []
    for entry in infos:
        try:
            sockaddr = entry[4]
            ip_raw = str(sockaddr[0]).split("%", 1)[0]
            ip_obj = ipaddress.ip_address(ip_raw)
        except Exception as exc:
            raise ValueError("RTMP 推流域名返回了非法 IP 解析结果") from exc
        if not _is_ip_globally_routable(ip_obj):
            raise ValueError("RTMP 推流域名解析到了非公网、内网或保留 IP 地址，已拦截")
        canonical = str(ip_obj)
        if canonical not in resolved_ips:
            resolved_ips.append(canonical)
    if not resolved_ips:
        raise ValueError(f"RTMP 推流域名未解析到有效公网 IP: {host}")
    return resolved_ips


def scrub_colabone_live_secrets(text: str, *secrets: str) -> str:
    """Scrub API keys, Bearer tokens, HF tokens, and RTMP credentials/keys from text."""
    result = str(text or "")
    if not result:
        return ""
    candidates: list[str] = []
    for secret in secrets:
        if not isinstance(secret, str):
            continue
        stripped = secret.strip()
        if not stripped:
            continue
        for variant in (secret, stripped, repr(secret)[1:-1], repr(stripped)[1:-1]):
            if variant and variant not in candidates:
                candidates.append(variant)
    for cand in sorted(candidates, key=len, reverse=True):
        if cand in result:
            result = result.replace(cand, "***")
    result = _BEARER_SECRET_RE.sub("Bearer ***", result)
    result = _OPENAI_KEY_RE.sub("***", result)
    result = _HF_TOKEN_RE.sub("***", result)
    result = _COLABONE_KEY_RE.sub("***", result)
    result = _RTMP_USERINFO_RE.sub(r"\1***@", result)
    result = _RTMP_SECRET_RE.sub(r"\1***", result)
    result = _RTMP_SINGLE_SEG_SECRET_RE.sub(r"\1***", result)
    result = _RTMP_QUERY_RE.sub(r"\1?***", result)
    return result


# ---------------------------------------------------------------------------
# Binary WebSocket frame pack / unpack (!BIQ = 13-byte header)
# ---------------------------------------------------------------------------


def pack_colabone_live_frame(
    frame_type: int, epoch: int, pts_ms: int, payload: bytes
) -> bytes:
    """Pack a Live Studio binary frame with a 13-byte network-order header (!BIQ)."""
    if (
        not isinstance(frame_type, int)
        or isinstance(frame_type, bool)
        or frame_type not in COLABONE_LIVE_VALID_FRAME_TYPES
    ):
        raise ValueError(f"非法的二进制帧类型: {frame_type!r}")
    if (
        not isinstance(epoch, int)
        or isinstance(epoch, bool)
        or not (0 <= epoch <= 0xFFFFFFFF)
    ):
        raise ValueError(f"非法的 epoch 值: {epoch!r}")
    if (
        not isinstance(pts_ms, int)
        or isinstance(pts_ms, bool)
        or not (0 <= pts_ms <= 0xFFFFFFFFFFFFFFFF)
    ):
        raise ValueError(f"非法的 pts_ms 值: {pts_ms!r}")
    if not isinstance(payload, (bytes, bytearray, memoryview)):
        raise ValueError("二进制帧载荷必须为字节序列")
    header = struct.pack(COLABONE_LIVE_HEADER_FORMAT, frame_type, epoch, pts_ms)
    return header + bytes(payload)


def unpack_colabone_live_frame(data: bytes) -> tuple[int, int, int, bytes]:
    """Unpack a Live Studio binary frame into (frame_type, epoch, pts_ms, payload)."""
    if not isinstance(data, (bytes, bytearray, memoryview)):
        raise ValueError("二进制帧数据必须为字节序列")
    raw = bytes(data)
    if len(raw) < COLABONE_LIVE_HEADER_SIZE:
        raise ValueError(
            f"二进制帧长度不足（至少 {COLABONE_LIVE_HEADER_SIZE} 字节，实际 {len(raw)} 字节）"
        )
    frame_type, epoch, pts_ms = struct.unpack(
        COLABONE_LIVE_HEADER_FORMAT, raw[:COLABONE_LIVE_HEADER_SIZE]
    )
    if frame_type not in COLABONE_LIVE_VALID_FRAME_TYPES:
        raise ValueError(f"未知的二进制帧类型: {frame_type:#x}")
    return frame_type, epoch, pts_ms, raw[COLABONE_LIVE_HEADER_SIZE:]


# ---------------------------------------------------------------------------
# Catalog Pydantic schemas
# ---------------------------------------------------------------------------


def _validate_revision_ref(value: str) -> str:
    cleaned = (value or "").strip()
    if not cleaned:
        return ""
    if (
        ".." in cleaned
        or cleaned.startswith("/")
        or cleaned.endswith("/")
        or not _SAFE_REVISION_RE.fullmatch(cleaned)
    ):
        raise ValueError(f"非法的模型版本标识: {value!r}")
    return cleaned


class ColabOneLiveCatalogAsrVadSpec(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(min_length=1, max_length=64)
    name: str = Field(min_length=1, max_length=120)
    role: Literal["vad", "asr"]
    description: str = Field(default="", max_length=600)
    repo: str = Field(default="", max_length=300)
    revision: str = Field(default="", max_length=120)
    source_repo: str = Field(default="", max_length=300)
    device: Literal["cpu", "cuda"] = "cuda"
    license: str = Field(min_length=1, max_length=120)
    commercial_ok: bool = True
    license_notice: str = Field(default="", max_length=600)
    min_vram_mib: int = Field(default=0, ge=0)
    min_disk_mib: int = Field(default=0, ge=0)
    sample_rate: int = Field(default=16000, ge=8000, le=48000)
    pip_packages: list[str] = Field(default_factory=list, max_length=32)

    @field_validator("id")
    @classmethod
    def check_id(cls, value: str) -> str:
        return validate_colabone_live_id(value, "ASR/VAD 引擎 ID")

    @field_validator("revision")
    @classmethod
    def check_revision(cls, value: str) -> str:
        return _validate_revision_ref(value)


class ColabOneLiveCatalogTtsSpec(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(min_length=1, max_length=64)
    name: str = Field(min_length=1, max_length=120)
    description: str = Field(default="", max_length=600)
    repo: str = Field(min_length=1, max_length=300)
    revision: str = Field(default="", max_length=120)
    aux_repo: str = Field(default="", max_length=300)
    aux_revision: str = Field(default="", max_length=120)
    custom_voice_repo: str = Field(default="", max_length=300)
    custom_voice_revision: str = Field(default="", max_length=120)
    source_repo: str = Field(default="", max_length=300)
    source_commit: str = Field(default="", max_length=120)
    license: str = Field(min_length=1, max_length=120)
    commercial_ok: bool = True
    license_notice: str = Field(default="", max_length=600)
    min_vram_mib: int = Field(default=0, ge=0)
    min_disk_mib: int = Field(default=0, ge=0)
    sample_rate: int = Field(default=24000, ge=8000, le=48000)
    native_sample_rate: int = Field(default=24000, ge=8000, le=48000)
    ttfa_ms: int = Field(default=100, ge=10, le=5000)
    pip_packages: list[str] = Field(default_factory=list, max_length=32)

    @field_validator("id")
    @classmethod
    def check_id(cls, value: str) -> str:
        return validate_colabone_live_id(value, "流式 TTS 引擎 ID")

    @field_validator("revision", "aux_revision", "custom_voice_revision", "source_commit")
    @classmethod
    def check_revisions(cls, value: str) -> str:
        return _validate_revision_ref(value)


class ColabOneLiveCatalogVisualSpec(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(min_length=1, max_length=64)
    name: str = Field(min_length=1, max_length=120)
    description: str = Field(default="", max_length=600)
    mode: Literal["single_photo", "action_bank", "driving_video"]
    repo: str = Field(min_length=1, max_length=300)
    revision: str = Field(default="", max_length=120)
    source_repo: str = Field(default="", max_length=300)
    license: str = Field(min_length=1, max_length=120)
    commercial_ok: bool = True
    license_notice: str = Field(default="", max_length=600)
    experimental: bool = False
    min_vram_mib: int = Field(default=0, ge=0)
    min_disk_mib: int = Field(default=0, ge=0)
    default_fps: int = Field(default=25, ge=10, le=60)
    default_resolution: str = Field(default="720x1280", pattern=r"^\d{3,4}x\d{3,4}$")
    pip_packages: list[str] = Field(default_factory=list, max_length=32)

    @field_validator("id")
    @classmethod
    def check_id(cls, value: str) -> str:
        return validate_colabone_live_id(value, "视觉引擎 ID")

    @field_validator("revision")
    @classmethod
    def check_revision(cls, value: str) -> str:
        return _validate_revision_ref(value)


class ColabOneLiveCatalogActionSpec(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(min_length=1, max_length=64)
    label: str = Field(min_length=1, max_length=80)
    kind: Literal["loop", "oneshot", "dance", "personalive_drive"] = "loop"
    description: str = Field(default="", max_length=400)
    default_duration_seconds: float = Field(default=4.0, ge=0.0, le=600.0)
    trigger_keywords: list[str] = Field(default_factory=list, max_length=32)
    prompt_hint: str = Field(default="", max_length=400)

    @field_validator("id")
    @classmethod
    def check_id(cls, value: str) -> str:
        return validate_colabone_live_id(value, "默认动作 ID")


class ColabOneLiveCatalog(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    port: int = Field(default=COLABONE_LIVE_PORT, ge=1024, le=65535)
    ws_path: str = Field(default=COLABONE_LIVE_WS_PATH, min_length=2, max_length=120)
    default_fps: int = Field(default=25, ge=10, le=60)
    audio_input_sample_rate: int = Field(default=16000, ge=8000, le=48000)
    audio_output_sample_rate: int = Field(default=24000, ge=8000, le=48000)
    encoding_note: str = Field(default="", max_length=600)
    asr_vad: list[ColabOneLiveCatalogAsrVadSpec] = Field(min_length=1)
    streaming_tts: list[ColabOneLiveCatalogTtsSpec] = Field(min_length=1)
    visual_engines: list[ColabOneLiveCatalogVisualSpec] = Field(min_length=1)
    default_actions: list[ColabOneLiveCatalogActionSpec] = Field(min_length=1)

    @field_validator("ws_path")
    @classmethod
    def check_ws_path(cls, value: str) -> str:
        if (
            not isinstance(value, str)
            or not value.startswith("/")
            or ".." in value
            or "//" in value
            or not re.fullmatch(r"/[A-Za-z0-9_./-]{1,119}", value, re.ASCII)
        ):
            raise ValueError("ws_path 格式不合法")
        return value

    @model_validator(mode="after")
    def check_unique_ids(self) -> "ColabOneLiveCatalog":
        for field_name in ("asr_vad", "streaming_tts", "visual_engines", "default_actions"):
            items = getattr(self, field_name)
            ids = [item.id for item in items]
            if len(ids) != len(set(ids)):
                raise ValueError(f"目录字段 {field_name} 包含重复 ID")
        return self


# ---------------------------------------------------------------------------
# Domain & API Pydantic v2 models
# ---------------------------------------------------------------------------


class ColabOneLiveExternalLLMConfig(BaseModel):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    base_url: str = Field(min_length=1, max_length=512)
    api_key: str = Field(default="", max_length=512, repr=False)
    model: str = Field(min_length=1, max_length=160)
    temperature: float = Field(default=0.7, ge=0.0, le=2.0)
    max_tokens: int = Field(default=256, ge=16, le=4096)

    @field_validator("base_url")
    @classmethod
    def check_base_url(cls, value: str) -> str:
        return validate_colabone_external_llm_url(value)

    @field_validator("api_key")
    @classmethod
    def check_api_key(cls, value: str) -> str:
        if _has_disallowed_control_chars(value):
            raise ValueError("API Key 包含非法控制字符")
        return value.strip()

    @field_validator("model")
    @classmethod
    def check_model(cls, value: str) -> str:
        if _has_disallowed_control_chars(value):
            raise ValueError("外部模型名称不能为空或包含控制字符")
        cleaned = value.strip()
        if not cleaned:
            raise ValueError("外部模型名称不能为空或包含控制字符")
        return cleaned

    def redacted_dict(self) -> dict[str, Any]:
        data = self.model_dump()
        data["api_key"] = "***" if self.api_key else ""
        return data


class ColabOneLiveProductItem(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(min_length=1, max_length=64)
    name: str = Field(min_length=1, max_length=120)
    price: str = Field(default="", max_length=64)
    highlights: str = Field(default="", max_length=600)
    script_hint: str = Field(default="", max_length=1000)
    tag: str = Field(default="", max_length=40)
    image_input_id: str = Field(default="", max_length=64, pattern=_ASSET_ID_RE.pattern)

    @field_validator("id")
    @classmethod
    def check_id(cls, value: str) -> str:
        return validate_colabone_live_id(value, "商品 ID")

    @field_validator("name")
    @classmethod
    def check_name(cls, value: str) -> str:
        cleaned = value.strip()
        if not cleaned:
            raise ValueError("商品名称不能为空")
        return cleaned


class ColabOneLiveActionSpec(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(min_length=1, max_length=64)
    label: str = Field(min_length=1, max_length=80)
    kind: Literal["loop", "oneshot", "dance", "personalive_drive"] = "loop"
    clip_path: str = Field(default="", max_length=512)
    bgm_path: str = Field(default="", max_length=512)
    duration_seconds: float = Field(default=0.0, ge=0.0, le=600.0)
    trigger_keywords: list[str] = Field(default_factory=list, max_length=32)

    @field_validator("id")
    @classmethod
    def check_id(cls, value: str) -> str:
        return validate_colabone_live_id(value, "动作 ID")

    @field_validator("label")
    @classmethod
    def check_label(cls, value: str) -> str:
        cleaned = value.strip()
        if not cleaned:
            raise ValueError("动作名称不能为空")
        return cleaned

    @field_validator("clip_path")
    @classmethod
    def check_clip_path(cls, value: str) -> str:
        return validate_colabone_live_relative_path(value, "动作片段路径")

    @field_validator("bgm_path")
    @classmethod
    def check_bgm_path(cls, value: str) -> str:
        return validate_colabone_live_relative_path(value, "背景音乐路径")

    @field_validator("trigger_keywords")
    @classmethod
    def check_keywords(cls, value: list[str]) -> list[str]:
        cleaned: list[str] = []
        for kw in value:
            s = str(kw or "").strip()
            if s and len(s) <= 40 and s not in cleaned:
                cleaned.append(s)
        return cleaned


class ColabOneLiveAvatarManifest(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(min_length=1, max_length=64)
    name: str = Field(min_length=1, max_length=120)
    persona_prompt: str = Field(default="", max_length=4000)
    portrait_path: str = Field(default="", max_length=512)
    default_tts_engine: Literal["qwen3_tts", "voxcpm2"] = "qwen3_tts"
    default_voice_preset: str = Field(
        default="shen_zhiyi", max_length=64, pattern=r"^(?:[a-zA-Z0-9_-]{1,64})?$"
    )
    actions: list[ColabOneLiveActionSpec] = Field(default_factory=list, max_length=64)
    products: list[ColabOneLiveProductItem] = Field(default_factory=list, max_length=64)
    created_at: float = Field(default=0.0, ge=0.0)
    updated_at: float = Field(default=0.0, ge=0.0)

    @field_validator("id")
    @classmethod
    def check_id(cls, value: str) -> str:
        return validate_colabone_live_id(value, "主播包 ID")

    @field_validator("name")
    @classmethod
    def check_name(cls, value: str) -> str:
        cleaned = value.strip()
        if not cleaned:
            raise ValueError("主播名称不能为空")
        return cleaned

    @field_validator("portrait_path")
    @classmethod
    def check_portrait_path(cls, value: str) -> str:
        return validate_colabone_live_relative_path(value, "人像路径")

    @model_validator(mode="after")
    def check_unique_children(self) -> "ColabOneLiveAvatarManifest":
        if len({a.id for a in self.actions}) != len(self.actions):
            raise ValueError("主播动作列表中存在重复的动作 ID")
        if len({p.id for p in self.products}) != len(self.products):
            raise ValueError("主播商品列表中存在重复的商品 ID")
        return self


class ColabOneLiveSessionConfig(BaseModel):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    visual_engine: Literal["flashhead_lite", "musetalk_bank"] = "flashhead_lite"
    personalive_enabled: bool = False
    tts_engine: Literal["qwen3_tts", "voxcpm2"] = "qwen3_tts"
    voice_preset: str = Field(
        default="shen_zhiyi", max_length=64, pattern=r"^(?:[a-zA-Z0-9_-]{1,64})?$"
    )
    llm_mode: Literal["local_gguf", "external_api"] = "local_gguf"
    local_llm_model_id: str = Field(default="", max_length=120)
    director_mode: Literal["dialogue", "director"] = "dialogue"
    mic_mode: Literal["vad", "ptt"] = "vad"
    avatar_id: str = Field(default="", max_length=64)
    target_fps: int = Field(default=25, ge=10, le=60)
    resolution: str = Field(default="720x1280", pattern=r"^\d{3,4}x\d{3,4}$")
    enable_rtmp: bool = False
    rtmp_url: str = Field(default="", max_length=1024, repr=False)

    @field_validator("avatar_id")
    @classmethod
    def check_avatar_id(cls, value: str) -> str:
        if not value:
            return ""
        return validate_colabone_live_id(value, "主播包 ID")

    @field_validator("resolution")
    @classmethod
    def check_resolution(cls, value: str) -> str:
        return _validate_resolution_bounds(value)

    @field_validator("rtmp_url")
    @classmethod
    def check_rtmp_url(cls, value: str) -> str:
        return validate_colabone_live_rtmp_url(value)

    @model_validator(mode="after")
    def check_rtmp_consistency(self) -> "ColabOneLiveSessionConfig":
        if self.enable_rtmp and not self.rtmp_url:
            raise ValueError("启用 RTMP 推流时必须提供有效的 rtmp:// 或 rtmps:// 地址")
        return self


class ColabOneLiveStartRequest(ColabOneLiveSessionConfig):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    external_llm: ColabOneLiveExternalLLMConfig | None = None
    hf_token: str = Field(default="", max_length=512, repr=False)
    portrait_input_id: str = Field(
        default="", max_length=64, pattern=_ASSET_ID_RE.pattern
    )
    canvas_config: ComfyCanvasConfig | None = None

    @field_validator("hf_token")
    @classmethod
    def check_hf_token(cls, value: str) -> str:
        return validate_colabone_hf_token(value)

    @model_validator(mode="after")
    def check_external_llm_consistency(self) -> "ColabOneLiveStartRequest":
        if self.llm_mode == "external_api" and self.external_llm is None:
            raise ValueError("使用外部 API 模式（external_api）时必须提供 external_llm 配置")
        return self

    def to_session_config(self) -> ColabOneLiveSessionConfig:
        fields = ColabOneLiveSessionConfig.model_fields.keys()
        return ColabOneLiveSessionConfig.model_validate(
            {k: getattr(self, k) for k in fields}
        )

    def redacted_summary(self) -> dict[str, Any]:
        data = self.to_session_config().model_dump()
        data["rtmp_url"] = scrub_colabone_live_secrets(data.get("rtmp_url", ""))
        if self.external_llm is not None:
            data["external_llm"] = self.external_llm.redacted_dict()
        else:
            data["external_llm"] = None
        data["portrait_input_id"] = self.portrait_input_id
        return data


class ColabOneLiveExternalLLMTestRequest(BaseModel):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    base_url: str = Field(min_length=1, max_length=512)
    api_key: str = Field(default="", max_length=512, repr=False)
    model: str = Field(min_length=1, max_length=160)
    timeout_seconds: float = Field(default=10.0, ge=1.0, le=60.0)

    @field_validator("base_url")
    @classmethod
    def check_base_url(cls, value: str) -> str:
        return validate_colabone_external_llm_url(value)

    @field_validator("api_key")
    @classmethod
    def check_api_key(cls, value: str) -> str:
        if _has_disallowed_control_chars(value):
            raise ValueError("API Key 包含非法控制字符")
        return value.strip()

    @field_validator("model")
    @classmethod
    def check_model(cls, value: str) -> str:
        if _has_disallowed_control_chars(value):
            raise ValueError("外部模型名称不能为空或包含控制字符")
        cleaned = value.strip()
        if not cleaned:
            raise ValueError("外部模型名称不能为空或包含控制字符")
        return cleaned


class ColabOneLiveAvatarSaveRequest(BaseModel):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    id: str = Field(default="", max_length=64)
    name: str = Field(min_length=1, max_length=120)
    persona_prompt: str = Field(default="", max_length=4000)
    portrait_input_id: str = Field(
        default="", max_length=64, pattern=_ASSET_ID_RE.pattern
    )
    portrait_b64: str = Field(default="", max_length=24 * 1024 * 1024, repr=False)
    portrait_filename: str = Field(default="", max_length=255)
    default_tts_engine: Literal["qwen3_tts", "voxcpm2"] = "qwen3_tts"
    default_voice_preset: str = Field(
        default="shen_zhiyi", max_length=64, pattern=r"^(?:[a-zA-Z0-9_-]{1,64})?$"
    )
    actions: list[ColabOneLiveActionSpec] | None = Field(default=None, max_length=64)
    products: list[ColabOneLiveProductItem] | None = Field(default=None, max_length=64)

    @field_validator("id")
    @classmethod
    def check_id(cls, value: str) -> str:
        if not value:
            return ""
        return validate_colabone_live_id(value, "主播包 ID")

    @field_validator("name")
    @classmethod
    def check_name(cls, value: str) -> str:
        cleaned = value.strip()
        if not cleaned:
            raise ValueError("主播名称不能为空")
        return cleaned


def _validate_trigger_keywords(value: list[str]) -> list[str]:
    cleaned: list[str] = []
    for kw in value:
        raw = str(kw or "")
        if _has_disallowed_control_chars(raw):
            raise ValueError("触发关键词包含非法控制字符")
        s = raw.strip()
        if not s:
            continue
        if len(s) > 40:
            raise ValueError("触发关键词长度不能超过 40 个字符")
        if s not in cleaned:
            cleaned.append(s)
    return cleaned


class ColabOneLiveClipUploadRequest(BaseModel):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    action_id: str = Field(min_length=1, max_length=64)
    label: str = Field(default="", max_length=80)
    kind: Literal["loop", "oneshot", "dance", "personalive_drive"] = "oneshot"
    filename: str = Field(min_length=1, max_length=255)
    data: str = Field(min_length=1, max_length=180 * 1024 * 1024, repr=False)
    bgm_filename: str = Field(default="", max_length=255)
    bgm_data: str = Field(default="", max_length=48 * 1024 * 1024, repr=False)
    duration_seconds: float = Field(default=0.0, ge=0.0, le=600.0)
    trigger_keywords: list[str] = Field(default_factory=list, max_length=32)

    @field_validator("action_id")
    @classmethod
    def check_action_id(cls, value: str) -> str:
        return validate_colabone_live_id(value, "动作 ID")

    @field_validator("trigger_keywords")
    @classmethod
    def check_keywords(cls, value: list[str]) -> list[str]:
        return _validate_trigger_keywords(value)


class ColabOneLiveBuildPackRequest(BaseModel):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    avatar_id: str = Field(min_length=1, max_length=64)
    name: str = Field(default="", max_length=120)
    portrait_input_id: str = Field(
        default="", max_length=64, pattern=_ASSET_ID_RE.pattern
    )
    portrait_b64: str = Field(default="", max_length=24 * 1024 * 1024, repr=False)
    portrait_filename: str = Field(default="", max_length=255)
    actions: list[str] = Field(
        default_factory=lambda: [
            "idle_loop",
            "talk_explain",
            "talk_product",
            "action_wave",
            "action_heart",
        ],
        max_length=16,
    )
    dance_ids: list[str] = Field(default_factory=lambda: ["dance_01"], max_length=8)
    resolution: str = Field(default="720x1280", pattern=r"^\d{3,4}x\d{3,4}$")
    fps: int = Field(default=25, ge=12, le=60)
    hf_token: str = Field(default="", max_length=512, repr=False)
    overwrite: bool = False

    @field_validator("avatar_id")
    @classmethod
    def check_avatar_id(cls, value: str) -> str:
        return validate_colabone_live_id(value, "主播包 ID")

    @field_validator("actions", "dance_ids")
    @classmethod
    def check_action_ids(cls, value: list[str]) -> list[str]:
        return [validate_colabone_live_id(item, "动作 ID") for item in value]

    @field_validator("resolution")
    @classmethod
    def check_resolution(cls, value: str) -> str:
        return _validate_resolution_bounds(value)

    @field_validator("hf_token")
    @classmethod
    def check_hf_token(cls, value: str) -> str:
        return validate_colabone_hf_token(value)


class ColabOneLiveDrivingUploadRequest(BaseModel):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    id: str = Field(default="", max_length=64)
    label: str = Field(min_length=1, max_length=80)
    filename: str = Field(min_length=1, max_length=255)
    data: str = Field(min_length=1, max_length=180 * 1024 * 1024, repr=False)
    bgm_filename: str = Field(default="", max_length=255)
    bgm_data: str = Field(default="", max_length=48 * 1024 * 1024, repr=False)
    duration_seconds: float = Field(default=0.0, ge=0.0, le=600.0)
    trigger_keywords: list[str] = Field(default_factory=list, max_length=32)

    @field_validator("id")
    @classmethod
    def check_id(cls, value: str) -> str:
        if not value:
            return ""
        return validate_colabone_live_id(value, "驱动动作 ID")

    @field_validator("label")
    @classmethod
    def check_label(cls, value: str) -> str:
        cleaned = value.strip()
        if not cleaned:
            raise ValueError("驱动动作名称不能为空")
        return cleaned

    @field_validator("trigger_keywords")
    @classmethod
    def check_keywords(cls, value: list[str]) -> list[str]:
        return _validate_trigger_keywords(value)


# ---------------------------------------------------------------------------
# VRAM Budget Co-Location Calculator
# ---------------------------------------------------------------------------

_DEFAULT_CATALOG_VRAM_MIB: dict[str, int] = {
    "silero_vad": 0,
    "sensevoice_small": 1024,
    "qwen3_tts": 8192,
    "voxcpm2": 8192,
    "flashhead_lite": 6656,
    "musetalk_bank": 7680,
    "personalive_dance": 12288,
}


def _lookup_catalog_vram(
    catalog: dict[str, Any] | ColabOneLiveCatalog | None,
    section: str,
    item_id: str,
) -> int:
    if catalog is not None:
        raw = catalog.model_dump() if hasattr(catalog, "model_dump") else catalog
        if isinstance(raw, dict):
            entries = raw.get(section)
            if isinstance(entries, list):
                for entry in entries:
                    if isinstance(entry, dict) and entry.get("id") == item_id:
                        return max(0, int(entry.get("min_vram_mib", 0) or 0))
            elif isinstance(entries, dict) and item_id in entries:
                entry = entries[item_id]
                if isinstance(entry, dict):
                    return max(0, int(entry.get("min_vram_mib", 0) or 0))
    return _DEFAULT_CATALOG_VRAM_MIB.get(item_id, 0)


def evaluate_colabone_live_vram_budget(
    config: dict[str, Any] | ColabOneLiveSessionConfig,
    gpu_total_mib: int,
    local_llm_vram_mib: int = 0,
    catalog: dict[str, Any] | ColabOneLiveCatalog | None = None,
) -> dict[str, Any]:
    """Compute resident & hot-swap VRAM breakdown and co-location feasibility."""
    if isinstance(config, ColabOneLiveSessionConfig):
        cfg = config
    elif isinstance(config, dict):
        allowed = set(ColabOneLiveSessionConfig.model_fields.keys())
        cfg = ColabOneLiveSessionConfig.model_validate(
            {k: v for k, v in config.items() if k in allowed}
        )
    else:
        raise ValueError("config 必须为字典或 ColabOneLiveSessionConfig")

    available_mib = max(0, int(gpu_total_mib or 0))
    provided_llm_mib = max(0, int(local_llm_vram_mib or 0))

    asr_vad_mib = _lookup_catalog_vram(
        catalog, "asr_vad", "silero_vad"
    ) + _lookup_catalog_vram(catalog, "asr_vad", "sensevoice_small")
    tts_mib = _lookup_catalog_vram(catalog, "streaming_tts", cfg.tts_engine)
    visual_mib = _lookup_catalog_vram(catalog, "visual_engines", cfg.visual_engine)
    personalive_catalog_mib = _lookup_catalog_vram(
        catalog, "visual_engines", "personalive_dance"
    )
    nvenc_buffer_mib = COLABONE_LIVE_NVENC_BUFFER_MIB
    safety_margin_mib = COLABONE_LIVE_SAFETY_MARGIN_MIB

    if cfg.llm_mode == "local_gguf":
        local_llm_mib = (
            provided_llm_mib
            if provided_llm_mib > 0
            else COLABONE_LIVE_DEFAULT_LOCAL_LLM_ESTIMATE_MIB
        )
    else:
        local_llm_mib = 0

    base_resident_no_personalive = (
        asr_vad_mib + tts_mib + visual_mib + nvenc_buffer_mib + local_llm_mib + safety_margin_mib
    )
    co_locate_personalive_total = base_resident_no_personalive + personalive_catalog_mib
    can_co_locate_personalive = available_mib >= co_locate_personalive_total

    llm_probe_mib = (
        provided_llm_mib
        if provided_llm_mib > 0
        else COLABONE_LIVE_DEFAULT_LOCAL_LLM_ESTIMATE_MIB
    )
    base_without_llm = asr_vad_mib + tts_mib + visual_mib + nvenc_buffer_mib + safety_margin_mib

    hot_swap_peak_mib = (
        asr_vad_mib
        + tts_mib
        + max(visual_mib, personalive_catalog_mib)
        + nvenc_buffer_mib
        + local_llm_mib
        + safety_margin_mib
    )
    hot_swap_with_llm_mib = (
        asr_vad_mib
        + tts_mib
        + max(visual_mib, personalive_catalog_mib)
        + nvenc_buffer_mib
        + llm_probe_mib
        + safety_margin_mib
    )

    warnings: list[str] = []
    recommendations: list[str] = []

    if cfg.personalive_enabled:
        if can_co_locate_personalive:
            personalive_mib = personalive_catalog_mib
            personalive_mode = "co_located"
            total_required_mib = co_locate_personalive_total
        elif available_mib >= base_resident_no_personalive and available_mib >= hot_swap_peak_mib:
            personalive_mib = personalive_catalog_mib
            personalive_mode = "hot_swap"
            total_required_mib = hot_swap_peak_mib
            warnings.append(
                "当前 GPU 显存不足以同时常驻主视觉引擎与 PersonaLive，已切换为按需热切换模式（hot-swap），触发跳舞才艺时会有短暂停顿。"
            )
            recommendations.append(
                "如需主画面与 PersonaLive 零停顿常驻同驻，建议使用 A100 80GB 或 G4 96GB。"
            )
        else:
            personalive_mib = personalive_catalog_mib
            personalive_mode = "unsupported"
            total_required_mib = hot_swap_peak_mib
            warnings.append(
                "当前 GPU 显存不足以运行 PersonaLive（即便热切换也会超过显存上限）。"
            )
            recommendations.append(
                "请关闭 PersonaLive 跳舞扩展，或升级至 A100 80GB / G4 96GB。"
            )
    else:
        personalive_mib = 0
        personalive_mode = "disabled"
        total_required_mib = base_resident_no_personalive

    if cfg.personalive_enabled:
        if personalive_mode == "co_located":
            co_locate_llm_total = base_without_llm + personalive_catalog_mib + llm_probe_mib
        else:
            co_locate_llm_total = max(base_without_llm + llm_probe_mib, hot_swap_with_llm_mib)
    else:
        co_locate_llm_total = base_without_llm + llm_probe_mib
    can_co_locate_llm = available_mib >= co_locate_llm_total

    if cfg.llm_mode == "external_api" and provided_llm_mib > 0:
        warnings.append(
            f"当前远端已加载本地 LLM（约 {provided_llm_mib} MiB）；使用外部 API 模式时建议先卸载本地模型以释放显存。"
        )
        recommendations.append("在模型管理页卸载本地 GGUF 模型以腾出完整直播显存。")

    ok = available_mib >= total_required_mib and personalive_mode != "unsupported"

    if not ok:
        deficit_mib = max(0, total_required_mib - available_mib)
        warnings.append(
            f"实时直播所需显存约 {total_required_mib} MiB，超过当前 GPU 可用显存 {available_mib} MiB（缺口 {deficit_mib} MiB）。"
        )
        if cfg.llm_mode == "local_gguf":
            recommendations.append(
                "请将对话大模型切换为外部 OpenAI 兼容 API（external_api）以省下本地 GGUF 显存占用。"
            )
        if cfg.visual_engine == "musetalk_bank":
            recommendations.append(
                "可尝试将视觉引擎切换为更轻量的 SoulX-FlashHead-1.3B Lite（约 6.5 GB 显存）。"
            )
        if available_mib < 20480:
            recommendations.append(
                "当前 GPU（如 T4 16GB）显存偏紧，实时数字人直播推荐至少使用 L4 24GB、A100 80GB 或 G4 96GB。"
            )

    return {
        "asr_vad_mib": asr_vad_mib,
        "tts_mib": tts_mib,
        "visual_mib": visual_mib,
        "personalive_mib": personalive_mib,
        "nvenc_buffer_mib": nvenc_buffer_mib,
        "local_llm_mib": local_llm_mib,
        "safety_margin_mib": safety_margin_mib,
        "total_required_mib": total_required_mib,
        "co_located_required_mib": co_locate_personalive_total if cfg.personalive_enabled else base_resident_no_personalive,
        "hot_swap_peak_mib": hot_swap_peak_mib if cfg.personalive_enabled else base_resident_no_personalive,
        "available_mib": available_mib,
        "ok": ok,
        "can_co_locate_llm": can_co_locate_llm,
        "can_co_locate_personalive": can_co_locate_personalive,
        "personalive_mode": personalive_mode,
        "warnings": warnings,
        "recommendations": recommendations,
    }


# ---------------------------------------------------------------------------
# Streaming LLM inline directive parser
# ---------------------------------------------------------------------------


def _could_be_directive_prefix(fragment: str) -> bool:
    """Return True if fragment (starting with '[') could still complete a directive tag."""
    if not fragment.startswith("["):
        return False
    if "\n" in fragment or "\r" in fragment or len(fragment) > 140:
        return False
    body = fragment[1:]
    if not body:
        return True
    upper = body.upper()
    if ":" in upper:
        head = upper.split(":", 1)[0].strip()
        return head in _DIRECTIVE_NAMES
    stripped = upper.lstrip()
    if not stripped:
        return True
    no_trail = stripped.rstrip()
    if stripped != no_trail:
        return no_trail in _DIRECTIVE_NAMES
    return any(name.startswith(stripped) for name in _DIRECTIVE_NAMES)


class ColabOneLiveDirectiveParser:
    """Incremental parser for streaming LLM tokens with inline [ACTION]/[CARD]/[MOOD] tags.

    Splits spoken text on CJK and Latin clause punctuation so the first clause
    reaches streaming TTS immediately, while never splitting inside a directive tag.
    """

    def __init__(self, *, min_soft_clause_chars: int = 2) -> None:
        self._min_soft_clause_chars = max(1, int(min_soft_clause_chars))
        self._speech_buf: str = ""
        self._bracket_buf: str = ""
        self._in_bracket: bool = False
        self._pending_dot: bool = False
        self._pending_hard_emit: bool = False

    def reset(self) -> None:
        self._speech_buf = ""
        self._bracket_buf = ""
        self._in_bracket = False
        self._pending_dot = False
        self._pending_hard_emit = False

    def _emit_speech(self, events: list[dict[str, str]]) -> None:
        text = self._speech_buf.strip()
        self._speech_buf = ""
        self._pending_hard_emit = False
        if text:
            events.append({"kind": "speech", "text": text})

    def _append_speech_char(
        self,
        ch: str,
        events: list[dict[str, str]],
        *,
        allow_colon_split: bool = True,
    ) -> None:
        if self._pending_hard_emit:
            if ch in _CLOSING_PUNCT or ch in _HARD_PUNCT:
                self._speech_buf += ch
                return
            self._emit_speech(events)
            if ch == "\n" or ch.isspace():
                return

        if self._pending_dot:
            self._pending_dot = False
            if ch.isdigit():
                # Decimal number (e.g., 99.9) or version (v1.5): do not split.
                self._speech_buf += ch
                return
            if ch in _CLOSING_PUNCT:
                self._speech_buf += ch
                self._pending_hard_emit = True
                return
            if ch == "\n" or ch.isspace():
                self._emit_speech(events)
                return
            if ch == ".":
                # Keep ellipsis ("...") intact.
                self._speech_buf += ch
                self._pending_dot = True
                return
            if ch in _HARD_PUNCT or ch in _SOFT_PUNCT:
                # Re-evaluate immediately-following hard/soft punctuation below.
                pass
            elif ord(ch) > 127:
                self._emit_speech(events)
                # Fall through so non-ASCII character is processed normally.
            else:
                # Non-space ASCII after dot (e.g. domain/abbrev): keep together.
                self._speech_buf += ch
                return

        if ch == "\n":
            self._emit_speech(events)
            return

        self._speech_buf += ch

        if ch in _HARD_PUNCT:
            self._pending_hard_emit = True
        elif ch in _SOFT_PUNCT:
            if ch == ":" and not allow_colon_split:
                return
            non_ws = sum(
                1 for c in self._speech_buf if not c.isspace() and c not in _SOFT_PUNCT
            )
            if non_ws >= self._min_soft_clause_chars:
                self._emit_speech(events)
        elif ch == ".":
            self._pending_dot = True

    def _release_bracket_as_speech(self, events: list[dict[str, str]]) -> None:
        raw = self._bracket_buf
        self._bracket_buf = ""
        self._in_bracket = False
        for c in raw:
            self._append_speech_char(c, events, allow_colon_split=False)

    def feed(self, delta: str) -> list[dict[str, str]]:
        """Feed an incremental text chunk and return ordered directive/speech events."""
        if not delta:
            return []
        events: list[dict[str, str]] = []
        for ch in delta:
            if self._in_bracket:
                if ch == "[":
                    # Previous '[' was unclosed normal text; start fresh candidate.
                    self._release_bracket_as_speech(events)
                    self._in_bracket = True
                    self._bracket_buf = "["
                    continue
                self._bracket_buf += ch
                if ch == "]":
                    match = _DIRECTIVE_TAG_RE.fullmatch(self._bracket_buf)
                    if match:
                        tag_kind = match.group(1).lower()
                        tag_value = match.group(2).strip()
                        if tag_value or tag_kind == "card":
                            self._pending_dot = False
                            self._emit_speech(events)
                            events.append(
                                {
                                    "kind": tag_kind,
                                    "value": tag_value,
                                }
                            )
                            self._bracket_buf = ""
                            self._in_bracket = False
                        else:
                            self._release_bracket_as_speech(events)
                    else:
                        self._release_bracket_as_speech(events)
                elif not _could_be_directive_prefix(self._bracket_buf):
                    self._release_bracket_as_speech(events)
                continue

            if ch == "[":
                if self._pending_hard_emit:
                    self._emit_speech(events)
                self._in_bracket = True
                self._bracket_buf = "["
                continue

            self._append_speech_char(ch, events)
        return events

    def flush(self) -> list[dict[str, str]]:
        """Flush any remaining buffered text at end of stream."""
        events: list[dict[str, str]] = []
        self._pending_dot = False
        if self._in_bracket:
            self._release_bracket_as_speech(events)
        self._emit_speech(events)
        return events


# ---------------------------------------------------------------------------
# System prompt builder (dialogue vs director mode)
# ---------------------------------------------------------------------------


def build_colabone_live_system_prompt(
    director_mode: str,
    avatar: dict[str, Any] | None,
    products: list[dict[str, Any]] | None,
    actions: list[dict[str, Any]] | None,
) -> str:
    """Build the LLM system prompt tailored to dialogue or backstage director mode."""
    mode = str(director_mode or "dialogue").strip().lower()
    if mode not in ("dialogue", "director"):
        mode = "dialogue"

    avatar_dict = avatar if isinstance(avatar, dict) else {}
    avatar_name = str(avatar_dict.get("name") or "直播间主播").strip()
    persona_prompt = str(avatar_dict.get("persona_prompt") or "").strip()

    action_items = list(actions or [])
    if not action_items and isinstance(avatar_dict.get("actions"), list):
        action_items = list(avatar_dict["actions"])

    product_items = list(products or [])
    if not product_items and isinstance(avatar_dict.get("products"), list):
        product_items = list(avatar_dict["products"])

    lines: list[str] = [f"你是实时数字人主播「{avatar_name}」。"]
    if persona_prompt:
        lines.append(f"【人设设定】\n{persona_prompt}")

    if mode == "director":
        lines.append(
            "【工作模式：后台导播指令模式（director）】\n"
            "你收到的每一条用户输入都是后台导播发给你的秘密调度指令，不是直播间观众的发言。\n"
            "1. 绝对禁止说「好的导演」「收到指令」「明白」「我这就执行」等任何暴露导播或指令存在的台词。\n"
            "2. 请立刻以第一人称直接面向镜头对观众开口表演，自然衔接直播间氛围。\n"
            "3. 根据指令内容，在台词合适位置主动插入 [ACTION:动作ID]、[CARD:商品ID] 或 [MOOD:情绪] 标签来配合动作与弹商品卡。"
        )
    else:
        lines.append(
            "【工作模式：观众实时互动对话模式（dialogue）】\n"
            "你正在直播间与观众实时一对一连麦或弹幕对话。\n"
            "1. 请用自然亲切的第一人称口语直接回应观众的问题、寒暄或点播请求。\n"
            "2. 回复尽量简短上口、分句清晰，避免长篇书面语、Markdown 表格或代码块。\n"
            "3. 在合适节点可自然插入 [ACTION:动作ID]、[CARD:商品ID] 或 [MOOD:情绪] 标签增强互动表现力。"
        )

    if action_items:
        action_lines = []
        for item in action_items:
            if not isinstance(item, dict):
                continue
            aid = str(item.get("id") or "").strip()
            if not aid:
                continue
            label = str(item.get("label") or aid).strip()
            kind = str(item.get("kind") or "loop").strip()
            action_lines.append(f"- [ACTION:{aid}] {label}（{kind}）")
        if action_lines:
            lines.append("【可用动作标签】\n" + "\n".join(action_lines))

    if product_items:
        prod_lines = []
        for item in product_items:
            if not isinstance(item, dict):
                continue
            pid = str(item.get("id") or "").strip()
            if not pid:
                continue
            pname = str(item.get("name") or pid).strip()
            price = str(item.get("price") or "").strip()
            highlights = str(item.get("highlights") or "").strip()
            script_hint = str(item.get("script_hint") or "").strip()
            parts = [f"[CARD:{pid}] {pname}"]
            if price:
                parts.append(f"价格: {price}")
            if highlights:
                parts.append(f"卖点: {highlights}")
            if script_hint:
                parts.append(f"话术提示: {script_hint}")
            prod_lines.append("- " + " | ".join(parts))
        if prod_lines:
            lines.append("【可弹出商品卡】\n" + "\n".join(prod_lines))

    lines.append(
        "【输出规范】\n"
        "- 仅输出主播口播台词与可选行内标签（[ACTION:...]、[CARD:...]、[MOOD:...]）。\n"
        "- 每句控制在 10 到 25 字左右并用标准标点停顿，以便流式语音实时播报。\n"
        "- 不要输出括号舞台说明或 Markdown 标记。"
    )
    return "\n\n".join(lines)
