"""Remote GPU multi-engine TTS runner and pure offline audio/script utilities.

Supports four remote GPU TTS engines (VoxCPM2, Qwen3-TTS, Fish Speech S2 Pro,
and IndexTTS2). All audio preprocessing, script segmentation, silence trimming,
resampling, loudness normalization, and master WAV assembly work 100% offline
with Python stdlib (with optional numpy/scipy/soxr/pyloudnorm acceleration when
installed on the remote Colab GPU runtime).
"""

from __future__ import annotations

import array
import copy
import importlib.metadata
import inspect
import json
import math
import os
import re
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import textwrap
import threading
import time
import wave
from pathlib import Path
from typing import Any, Callable, Sequence

try:
    import tomllib  # type: ignore[import-not-found]
except ImportError:  # pragma: no cover - Python 3.10 fallback
    tomllib = None  # type: ignore[assignment]

from common.files import atomic_json

TORCH_SKIP_PACKAGES = frozenset(
    {"torch", "torchvision", "torchaudio", "xformers", "triton", "flash-attn"}
)
_UNBUILDABLE_SKIP_PACKAGES = frozenset({"pyaudio"})
_PIP_SKIP_PACKAGES = TORCH_SKIP_PACKAGES | _UNBUILDABLE_SKIP_PACKAGES
_TORCH_CONSTRAINT_PACKAGES = ("torch", "torchvision", "torchaudio", "xformers")
SUPPORTED_TTS_ENGINE_IDS = frozenset(
    {"voxcpm2", "qwen3_tts", "fish_s2_pro", "indextts2"}
)
_PIP_PACKAGED_ENGINES = frozenset({"voxcpm2", "qwen3_tts"})
_SOURCE_CHECKOUT_ENGINES = frozenset({"fish_s2_pro", "indextts2"})

_ENGINE_ID_RE = re.compile(r"^[a-zA-Z0-9_-]{1,80}$")
_REPO_SLUG_RE = re.compile(r"^[\w.-]+/[\w.-]+$")
_GIT_COMMIT_SHA_RE = re.compile(r"^[0-9a-fA-F]{40}$")
_GITHUB_SEGMENT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$")
_GITHUB_REPO_URL_RE = re.compile(
    r"^https://github\.com/(?P<owner>[A-Za-z0-9][A-Za-z0-9_.-]{0,99})/(?P<repo>[A-Za-z0-9][A-Za-z0-9_.-]{0,99}?)(?:\.git)?$"
)
_INLINE_TAG_RE = re.compile(r"\[[^\[\]\n]{1,80}\]")
_PURE_INLINE_TAG_RE = re.compile(r"^(?:\[[^\[\]\n]{1,80}\]\s*)+$")
_WHITESPACE_RUN_RE = re.compile(r"[ \t\u00a0\u3000]+")

_CJK_HARD_SENTENCE_DELIMS = frozenset("。！？；")
_ASCII_HARD_SENTENCE_DELIMS = frozenset("!?;")
_HARD_SENTENCE_DELIMS = _CJK_HARD_SENTENCE_DELIMS | _ASCII_HARD_SENTENCE_DELIMS
_CLAUSE_DELIMS = frozenset("，、：,:—–")
_CLOSING_TRAILING = frozenset("”’\"'」』）》】）)]")
_CJK_PUNCT_END = frozenset("。！？；，、：”’」』）》】）")
_ENGLISH_ABBREVS = frozenset(
    {
        "mr",
        "mrs",
        "ms",
        "dr",
        "prof",
        "sr",
        "jr",
        "st",
        "vs",
        "etc",
        "e.g",
        "i.e",
        "fig",
        "no",
        "inc",
        "ltd",
        "co",
    }
)

_MIN_SHORT_FRAGMENT_CHARS = 12
_SOFT_LIMITER_KNEE = 0.85
_SOFT_LIMITER_CEILING = 0.96
_SILENCE_FLOOR_RMS = 10.0 ** (-70.0 / 20.0)

DEFAULT_COLABONE_VOICE_PRESET = "shen_zhiyi"
COLABONE_VOICE_PRESET_ANCHOR_TEXT = (
    "大家好，欢迎来到今天的节目。我们将用清晰自然的声音，为您娓娓道来每一个精彩细节，"
    "希望这段分享能带给您轻松愉快的收听体验。"
)
COLABONE_TTS_VOICE_PRESETS: dict[str, dict[str, Any]] = {
    "shen_zhiyi": {
        "id": "shen_zhiyi",
        "name": "知性女主播 · 沈知意",
        "gender": "female",
        "style": "知性主播",
        "seed": 202601,
        "voxcpm_prompt": (
            "A 26-year-old Chinese female host, clear warm articulate studio microphone voice, "
            "natural smooth pacing, friendly confident professional tone"
        ),
        "qwen_instruct": (
            "26岁年轻知性女性主播，清澈温润的现代标准普通话，吐字清晰圆润，"
            "棚录近麦高保真质感，语速自然流畅，专业沉稳且富有亲和力。"
        ),
    },
    "lin_xiaoman": {
        "id": "lin_xiaoman",
        "name": "甜美活力 · 林小满",
        "gender": "female",
        "style": "甜美活力",
        "seed": 202602,
        "voxcpm_prompt": (
            "A 22-year-old Chinese young woman, bright sweet lively voice with a gentle smile, "
            "clear crisp articulation, cheerful energetic natural rhythm"
        ),
        "qwen_instruct": (
            "22岁甜美活泼的年轻女生，嗓音清亮灵动略带笑意，元气自然不腻耳，"
            "咬字轻快清晰，节奏明快富有感染力。"
        ),
    },
    "su_wanqing": {
        "id": "su_wanqing",
        "name": "温柔治愈 · 苏晚晴",
        "gender": "female",
        "style": "温柔治愈",
        "seed": 202603,
        "voxcpm_prompt": (
            "A 25-year-old Chinese young woman, soft gentle healing radio voice, "
            "warm delicate breathy tone, calm unhurried soothing studio narration"
        ),
        "qwen_instruct": (
            "25岁温柔治愈系年轻女声，声线柔和细腻、气息温暖舒缓，语调轻柔自然，"
            "像深夜电台好友娓娓道来，耐听且有安全感。"
        ),
    },
    "gu_qinglan": {
        "id": "gu_qinglan",
        "name": "干练职场 · 顾清岚",
        "gender": "female",
        "style": "干练御姐",
        "seed": 202604,
        "voxcpm_prompt": (
            "A 30-year-old mature Chinese professional woman, rich poised warm mid-low voice, "
            "crisp decisive articulation, persuasive confident executive tone"
        ),
        "qwen_instruct": (
            "30岁成熟干练的职场女性，中低音区醇厚清亮，吐字干净利落，"
            "逻辑停顿清晰，语气温和而坚定，极富专业说服力。"
        ),
    },
    "lu_mingwei": {
        "id": "lu_mingwei",
        "name": "播音气质 · 陆明薇",
        "gender": "female",
        "style": "新闻播音",
        "seed": 202605,
        "voxcpm_prompt": (
            "A 28-year-old professional Chinese female broadcast news anchor, "
            "resonant polished studio voice, crisp standard Mandarin diction, "
            "steady authoritative warm cadence"
        ),
        "qwen_instruct": (
            "28岁专业新闻女播音员，胸腔共鸣饱满大气，标准普通话字正腔圆，"
            "重音与停连考究，沉稳大方且自然流畅。"
        ),
    },
}
_VOICE_PRESET_ID_RE = re.compile(r"^[a-zA-Z0-9_-]{1,64}$")


def resolve_colabone_voice_preset(
    engine: dict[str, Any], preset_id: str | None
) -> dict[str, Any] | None:
    """Resolve a built-in named voice preset for a TTS engine, merging catalog & canonical defaults."""
    raw = str(preset_id or "").strip()
    if raw.lower() == "custom":
        return None
    target_id = raw or DEFAULT_COLABONE_VOICE_PRESET

    if hasattr(engine, "model_dump"):
        engine_dict = engine.model_dump()
    elif isinstance(engine, dict):
        engine_dict = engine
    else:
        engine_dict = {}
    engine_id = str(engine_dict.get("id") or "").strip()

    engine_entry: dict[str, Any] | None = None
    for item in engine_dict.get("voice_presets") or []:
        if hasattr(item, "model_dump"):
            candidate = item.model_dump()
        elif isinstance(item, dict):
            candidate = copy.deepcopy(item)
        else:
            continue
        if str(candidate.get("id") or "").strip() == target_id:
            engine_entry = candidate
            break

    fallback_entry = COLABONE_TTS_VOICE_PRESETS.get(target_id)
    if engine_entry is None and fallback_entry is None:
        return None

    merged: dict[str, Any] = copy.deepcopy(fallback_entry) if fallback_entry is not None else {}
    if engine_entry is not None:
        for key, val in engine_entry.items():
            if key == "seed":
                if val is not None and not isinstance(val, bool):
                    try:
                        merged["seed"] = max(0, int(val))
                    except (TypeError, ValueError):
                        pass
            elif key == "prompt":
                if str(val or "").strip():
                    merged["prompt"] = str(val).strip()
            elif val not in (None, ""):
                merged[key] = copy.deepcopy(val)

    merged["id"] = str(merged.get("id") or target_id).strip()
    merged["name"] = str(merged.get("name") or merged["id"]).strip()
    merged["gender"] = str(merged.get("gender") or "female").strip() or "female"
    merged["style"] = str(merged.get("style") or "").strip()

    seed_val = merged.get("seed")
    if isinstance(seed_val, bool) or seed_val is None:
        seed_val = int(fallback_entry["seed"]) if fallback_entry is not None else 42
    else:
        try:
            seed_val = int(seed_val)
        except (TypeError, ValueError):
            seed_val = int(fallback_entry["seed"]) if fallback_entry is not None else 42
    merged["seed"] = max(0, seed_val)

    prompt_val = str(merged.get("prompt") or "").strip()
    if not prompt_val:
        if engine_id == "qwen3_tts":
            prompt_val = str(
                merged.get("qwen_instruct") or merged.get("voxcpm_prompt") or ""
            ).strip()
        else:
            prompt_val = str(
                merged.get("voxcpm_prompt") or merged.get("qwen_instruct") or ""
            ).strip()
    merged["prompt"] = prompt_val
    if not str(merged.get("voxcpm_prompt") or "").strip():
        merged["voxcpm_prompt"] = prompt_val
    if not str(merged.get("qwen_instruct") or "").strip():
        merged["qwen_instruct"] = prompt_val
    return merged


def _atomic_write_text(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix=".save-", suffix=".tmp", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            stream.write(text)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


# ---------------------------------------------------------------------------
# 1. Pure script segmentation helpers
# ---------------------------------------------------------------------------


def _inline_tag_spans(text: str) -> list[tuple[int, int]]:
    return [(m.start(), m.end()) for m in _INLINE_TAG_RE.finditer(text)]


def _in_spans(index: int, spans: list[tuple[int, int]]) -> tuple[int, int] | None:
    for start, end in spans:
        if start <= index < end:
            return (start, end)
        if start > index:
            break
    return None


def _is_cjk_char(ch: str) -> bool:
    code = ord(ch)
    return (
        0x4E00 <= code <= 0x9FFF
        or 0x3400 <= code <= 0x4DBF
        or 0x3000 <= code <= 0x303F
        or 0x3040 <= code <= 0x30FF
        or 0xAC00 <= code <= 0xD7AF
        or 0xFF00 <= code <= 0xFFEF
        or 0x20000 <= code <= 0x2A6DF
    )


def _is_ascii_sentence_delim(text: str, idx: int) -> bool:
    """Return True if ASCII '!', '?', or ';' ends a sentence (not inside a URL/query)."""
    n = len(text)
    if text[idx] not in _ASCII_HARD_SENTENCE_DELIMS:
        return False
    after = idx + 1
    while after < n and (
        text[after] in _HARD_SENTENCE_DELIMS or text[after] == "."
    ):
        after += 1
    while after < n and text[after] in _CLOSING_TRAILING:
        after += 1
    if after >= n:
        return True
    nxt = text[after]
    return nxt.isspace() or _is_cjk_char(nxt)


def _is_english_sentence_period(text: str, idx: int) -> bool:
    """Return True if '.' at idx marks an English sentence boundary (not decimal/abbrev)."""
    n = len(text)
    if text[idx] != ".":
        return False
    # Skip ellipsis run except at the end of the run.
    if idx + 1 < n and text[idx + 1] == ".":
        return False
    # Look past optional closing quotes after the period.
    after = idx + 1
    while after < n and text[after] in _CLOSING_TRAILING:
        after += 1
    # Must be followed by whitespace, a CJK character, or end-of-line (prevents splitting URLs/versions/decimals).
    if after < n and not (text[after].isspace() or _is_cjk_char(text[after])):
        return False
    # Avoid splitting numbered list prefixes like "1. " or "12. " at the start of a line/segment,
    # while still splitting sentences ending in years like "in 2026. It gained...".
    if idx > 0 and text[idx - 1].isdigit():
        j = idx - 1
        while j >= 0 and text[j].isdigit():
            j -= 1
        digit_len = idx - (j + 1)
        if digit_len <= 2 and (j < 0 or text[: j + 1].strip() == ""):
            return False
    # Avoid common abbreviations like "Mr.", "Dr.", "e.g.".
    k = idx - 1
    while k >= 0 and (text[k].isalpha() or text[k] == "."):
        k -= 1
    token = text[k + 1 : idx].lower()
    if token in _ENGLISH_ABBREVS:
        return False
    # Single uppercase initial like "J. K. Rowling".
    if len(token) == 1 and text[idx - 1].isupper():
        return False
    return True


def _split_line_sentences(line: str) -> list[str]:
    """Split one cleaned line on Chinese/English sentence boundaries, preserving [...] tags."""
    spans = _inline_tag_spans(line)
    parts: list[str] = []
    n = len(line)
    start = 0
    i = 0
    while i < n:
        span = _in_spans(i, spans)
        if span is not None:
            i = span[1]
            continue
        ch = line[i]
        is_delim = (
            ch in _CJK_HARD_SENTENCE_DELIMS
            or (ch in _ASCII_HARD_SENTENCE_DELIMS and _is_ascii_sentence_delim(line, i))
            or (ch == "." and _is_english_sentence_period(line, i))
        )
        if is_delim:
            j = i + 1
            while j < n and _in_spans(j, spans) is None and (
                line[j] in _HARD_SENTENCE_DELIMS or line[j] == "."
            ):
                j += 1
            while j < n and _in_spans(j, spans) is None and line[j] in _CLOSING_TRAILING:
                j += 1
            piece = line[start:j].strip()
            if piece:
                parts.append(piece)
            start = j
            i = j
            continue
        i += 1
    tail = line[start:].strip()
    if tail:
        parts.append(tail)
    return parts


def _split_clause_boundaries(text: str) -> list[str]:
    """Split text on clause punctuation outside [...] inline tags."""
    spans = _inline_tag_spans(text)
    parts: list[str] = []
    n = len(text)
    start = 0
    i = 0
    while i < n:
        span = _in_spans(i, spans)
        if span is not None:
            i = span[1]
            continue
        ch = text[i]
        if ch in _CLAUSE_DELIMS:
            if ch == ":" and i + 1 < n and (text[i + 1] == "/" or text[i + 1].isdigit()):
                i += 1
                continue
            if (
                ch == ","
                and i > 0
                and text[i - 1].isdigit()
                and i + 1 < n
                and text[i + 1].isdigit()
            ):
                i += 1
                continue
            j = i + 1
            while j < n and _in_spans(j, spans) is None and text[j] in _CLAUSE_DELIMS:
                j += 1
            while j < n and _in_spans(j, spans) is None and text[j] in _CLOSING_TRAILING:
                j += 1
            piece = text[start:j].strip()
            if piece:
                parts.append(piece)
            start = j
            i = j
            continue
        i += 1
    tail = text[start:].strip()
    if tail:
        parts.append(tail)
    return parts


def _join_fragments(left: str, right: str) -> str:
    left = left.strip()
    right = right.strip()
    if not left:
        return right
    if not right:
        return left
    if left[-1] in _CJK_PUNCT_END:
        return left + right
    if left[-1] == "]":
        if right[0] in _CJK_PUNCT_END or right[0] in ".!?;,:":
            return left + right
        return f"{left} {right}"
    if left[-1].isascii() and right[0].isascii():
        return f"{left} {right}"
    if left[-1] in ".!?;,:":
        return f"{left} {right}"
    if not left[-1].isascii() and not right[0].isascii():
        return f"{left} {right}" if left[-1] not in _CJK_PUNCT_END and right[0] not in _CJK_PUNCT_END else left + right
    return f"{left} {right}"


def _atomic_units(text: str) -> list[str]:
    """Tokenize text into indivisible units so [...] inline tags and ASCII words stay intact."""
    spans = _inline_tag_spans(text)
    units: list[str] = []
    n = len(text)
    i = 0
    while i < n:
        span = _in_spans(i, spans)
        if span is not None:
            units.append(text[span[0] : span[1]])
            i = span[1]
            continue
        ch = text[i]
        if ch.isspace():
            units.append(" ")
            while i < n and text[i].isspace():
                i += 1
            continue
        if ch.isascii() and (ch.isalnum() or ch in "_'-"):
            j = i + 1
            while (
                j < n
                and _in_spans(j, spans) is None
                and text[j].isascii()
                and (text[j].isalnum() or text[j] in "_'-")
            ):
                j += 1
            # Keep immediately attached ASCII punctuation with the word.
            while (
                j < n
                and _in_spans(j, spans) is None
                and text[j] in ".,!?;:)\"'"
            ):
                j += 1
            units.append(text[i:j])
            i = j
            continue
        units.append(ch)
        i += 1
    return units


def _split_overlong_fragment(fragment: str, max_chars: int) -> list[str]:
    """Break a single fragment exceeding max_chars without ever breaking inside [...]."""
    fragment = fragment.strip()
    if len(fragment) <= max_chars:
        return [fragment] if fragment else []

    clauses = _split_clause_boundaries(fragment)
    if len(clauses) > 1:
        packed: list[str] = []
        current = ""
        for clause in clauses:
            if not current:
                if len(clause) <= max_chars:
                    current = clause
                else:
                    packed.extend(_split_by_units(clause, max_chars))
            else:
                candidate = _join_fragments(current, clause)
                if len(candidate) <= max_chars:
                    current = candidate
                else:
                    packed.append(current)
                    if len(clause) <= max_chars:
                        current = clause
                    else:
                        packed.extend(_split_by_units(clause, max_chars))
                        current = ""
        if current:
            packed.append(current)
        return [p for p in packed if p]

    return _split_by_units(fragment, max_chars)


def _is_inline_tag_unit(unit: str) -> bool:
    return bool(_INLINE_TAG_RE.fullmatch(unit))


def _has_speakable_text(text: str) -> bool:
    return bool(_INLINE_TAG_RE.sub("", text).strip())


def _split_by_units(text: str, max_chars: int) -> list[str]:
    raw_units = _atomic_units(text)
    units: list[str] = []
    for unit in raw_units:
        if len(unit) > max_chars and not _is_inline_tag_unit(unit):
            for pos in range(0, len(unit), max_chars):
                units.append(unit[pos : pos + max_chars])
        else:
            units.append(unit)

    chunks: list[str] = []
    buf = ""
    for unit in units:
        if not buf and unit == " ":
            continue
        if not buf:
            buf = unit
            continue
        if len(buf) + len(unit) <= max_chars:
            buf += unit
        else:
            cleaned = buf.strip()
            if cleaned:
                chunks.append(cleaned)
            buf = "" if unit == " " else unit
    cleaned = buf.strip()
    if cleaned:
        chunks.append(cleaned)
    return chunks


def split_colabone_tts_script(text: str, max_chars: int = 120) -> list[str]:
    """Split a TTS script into clean, speakable segments respecting max_chars.

    - Cleans blank lines and normalizes whitespace.
    - Splits on paragraph/line breaks and Chinese/English sentence boundaries.
    - Never splits inside inline emotion/style bracket tags like [laugh] or [whisper].
    - Merges overly short fragments (< 12 chars) with neighbors while respecting max_chars.
    - Drops scripts or isolated fragments containing only inline tags with no speakable text.
    """
    if max_chars < 1:
        raise ValueError("max_chars 必须大于 0")
    if not isinstance(text, str):
        return []
    normalized = text.replace("\r\n", "\n").replace("\r", "\n")
    raw_lines = [
        _WHITESPACE_RUN_RE.sub(" ", line).strip()
        for line in normalized.split("\n")
    ]
    lines = [line for line in raw_lines if line]
    if not lines or not _has_speakable_text(normalized):
        return []

    fragments: list[str] = []
    for line in lines:
        for sentence in _split_line_sentences(line):
            if len(sentence) <= max_chars:
                fragments.append(sentence)
            else:
                fragments.extend(_split_overlong_fragment(sentence, max_chars))

    if len(fragments) <= 1:
        return [seg for seg in fragments if seg and _has_speakable_text(seg)]

    min_chars = min(_MIN_SHORT_FRAGMENT_CHARS, max_chars)
    merged = list(fragments)
    changed = True
    while changed and len(merged) > 1:
        changed = False
        for idx in range(len(merged)):
            frag = merged[idx]
            is_pure_tag = bool(_PURE_INLINE_TAG_RE.fullmatch(frag))
            if len(frag) >= min_chars and not is_pure_tag:
                continue

            left_cand = (
                _join_fragments(merged[idx - 1], frag) if idx > 0 else None
            )
            right_cand = (
                _join_fragments(frag, merged[idx + 1])
                if idx + 1 < len(merged)
                else None
            )
            can_left = left_cand is not None and len(left_cand) <= max_chars
            can_right = right_cand is not None and len(right_cand) <= max_chars

            if not can_left and not can_right:
                continue

            # Inline tags like [laugh] / [whisper] attach forward by preference.
            if is_pure_tag and can_right:
                merged[idx : idx + 2] = [right_cand]  # type: ignore[list-item]
                changed = True
                break

            if can_left and can_right:
                # Prefer the neighbor that yields the more balanced (shorter) combined chunk.
                if len(right_cand) < len(left_cand):  # type: ignore[arg-type]
                    merged[idx : idx + 2] = [right_cand]  # type: ignore[list-item]
                else:
                    merged[idx - 1 : idx + 1] = [left_cand]  # type: ignore[list-item]
                changed = True
                break
            elif can_right:
                merged[idx : idx + 2] = [right_cand]  # type: ignore[list-item]
                changed = True
                break
            else:
                merged[idx - 1 : idx + 1] = [left_cand]  # type: ignore[list-item]
                changed = True
                break

    return [seg for seg in merged if seg and _has_speakable_text(seg)]


# ---------------------------------------------------------------------------
# 2. Pure offline WAV I/O, resampling, trimming, loudness & master assembly
# ---------------------------------------------------------------------------


def _decode_pcm_bytes(raw: bytes, sampwidth: int, nchannels: int) -> list[float]:
    """Decode little-endian 8/16/24/32-bit PCM bytes into mono float samples in [-1, 1]."""
    if not raw or nchannels <= 0:
        return []
    if sampwidth not in (1, 2, 3, 4):
        raise ValueError(f"不支持的 PCM 位宽：{sampwidth} 字节")
    frame_bytes = sampwidth * nchannels
    usable = len(raw) - (len(raw) % frame_bytes)
    if usable <= 0:
        return []
    payload = raw[:usable]

    try:
        import numpy as np  # type: ignore[import-not-found]

        if sampwidth == 1:
            arr = (np.frombuffer(payload, dtype=np.uint8).astype(np.float64) - 128.0) / 128.0
        elif sampwidth == 2:
            arr = np.frombuffer(payload, dtype="<i2").astype(np.float64) / 32768.0
        elif sampwidth == 3:
            b = np.frombuffer(payload, dtype=np.uint8).reshape(-1, 3)
            vals = (
                b[:, 0].astype(np.int32)
                | (b[:, 1].astype(np.int32) << 8)
                | (b[:, 2].astype(np.int32) << 16)
            )
            vals = np.where(vals >= 0x800000, vals - 0x1000000, vals)
            arr = vals.astype(np.float64) / 8388608.0
        else:
            arr = np.frombuffer(payload, dtype="<i4").astype(np.float64) / 2147483648.0
        if nchannels > 1:
            arr = arr.reshape(-1, nchannels).mean(axis=1)
        return arr.tolist()
    except ImportError:
        pass

    if sampwidth == 1:
        floats = [(b - 128) / 128.0 for b in payload]
    elif sampwidth == 2:
        arr_py = array.array("h")
        arr_py.frombytes(payload)
        if sys.byteorder != "little":
            arr_py.byteswap()
        scale = 1.0 / 32768.0
        floats = [x * scale for x in arr_py]
    elif sampwidth == 3:
        scale = 1.0 / 8388608.0
        floats = []
        mv = memoryview(payload)
        for i in range(0, len(payload) - 2, 3):
            b0, b1, b2 = mv[i], mv[i + 1], mv[i + 2]
            val = b0 | (b1 << 8) | (b2 << 16)
            if val & 0x800000:
                val -= 0x1000000
            floats.append(val * scale)
    else:
        arr_py = array.array("i")
        arr_py.frombytes(payload)
        if sys.byteorder != "little":
            arr_py.byteswap()
        scale = 1.0 / 2147483648.0
        floats = [x * scale for x in arr_py]

    if nchannels <= 1:
        return floats

    inv_ch = 1.0 / nchannels
    mono: list[float] = []
    limit = len(floats) - (len(floats) % nchannels)
    for i in range(0, limit, nchannels):
        mono.append(sum(floats[i : i + nchannels]) * inv_ch)
    return mono


def _read_ieee_float_wav(path: Path) -> tuple[list[float], int]:
    """Fallback RIFF WAV reader supporting PCM (format 1) and IEEE float (format 3), including WAVE_FORMAT_EXTENSIBLE."""
    data = path.read_bytes()
    if len(data) < 12 or data[:4] != b"RIFF" or data[8:12] != b"WAVE":
        raise ValueError(f"无效的 WAV 文件头：{path}")
    pos = 12
    audio_format: int | None = None
    nchannels = 1
    sample_rate = 0
    bits_per_sample = 32
    pcm_bytes: bytes | None = None

    while pos + 8 <= len(data):
        chunk_id = data[pos : pos + 4]
        chunk_size = struct.unpack_from("<I", data, pos + 4)[0]
        chunk_start = pos + 8
        chunk_end = min(len(data), chunk_start + chunk_size)
        if chunk_id == b"fmt " and chunk_end - chunk_start >= 16:
            audio_format, nchannels, sample_rate, _, _, bits_per_sample = (
                struct.unpack_from("<HHIIHH", data, chunk_start)
            )
            if audio_format == 0xFFFE and chunk_end - chunk_start >= 40:
                subformat_tag = struct.unpack_from("<H", data, chunk_start + 24)[0]
                audio_format = subformat_tag
        elif chunk_id == b"data":
            pcm_bytes = data[chunk_start:chunk_end]
        pos = chunk_start + chunk_size + (chunk_size & 1)

    if (
        audio_format not in (1, 3)
        or sample_rate <= 0
        or nchannels <= 0
        or pcm_bytes is None
    ):
        raise ValueError(f"不支持的 WAV 编码格式：{path}")

    if audio_format == 1:
        if bits_per_sample not in (8, 16, 24, 32):
            raise ValueError(f"不支持的 PCM WAV 位深：{bits_per_sample}")
        return (
            _decode_pcm_bytes(pcm_bytes, bits_per_sample // 8, nchannels),
            int(sample_rate),
        )

    if bits_per_sample == 32:
        typecode = "f"
        np_dtype = "<f4"
        step = 4
    elif bits_per_sample == 64:
        typecode = "d"
        np_dtype = "<f8"
        step = 8
    else:
        raise ValueError(f"不支持的浮点 WAV 位深：{bits_per_sample}")

    usable = len(pcm_bytes) - (len(pcm_bytes) % (step * max(1, nchannels)))
    if usable <= 0:
        return [], int(sample_rate)

    try:
        import numpy as np  # type: ignore[import-not-found]

        arr_np = np.frombuffer(pcm_bytes[:usable], dtype=np_dtype).astype(np.float64)
        arr_np = np.where(np.isfinite(arr_np), arr_np, 0.0)
        if nchannels > 1:
            arr_np = arr_np.reshape(-1, nchannels).mean(axis=1)
        return arr_np.tolist(), int(sample_rate)
    except ImportError:
        pass

    arr = array.array(typecode)
    arr.frombytes(pcm_bytes[:usable])
    if sys.byteorder != "little":
        arr.byteswap()
    if nchannels <= 1:
        return [float(x) if math.isfinite(x) else 0.0 for x in arr], int(sample_rate)
    inv_ch = 1.0 / nchannels
    out: list[float] = []
    for idx in range(0, len(arr), nchannels):
        acc = 0.0
        for ch in range(nchannels):
            val = arr[idx + ch]
            if math.isfinite(val):
                acc += val
        out.append(acc * inv_ch)
    return out, int(sample_rate)


def read_wav_mono_float(path: Path) -> tuple[list[float], int]:
    """Read a WAV file (8/16/24/32-bit PCM, WAVE_FORMAT_EXTENSIBLE, or 32/64-bit IEEE float) as mono float samples in [-1, 1]."""
    wav_path = Path(path)
    if not wav_path.is_file():
        raise FileNotFoundError(f"WAV 文件不存在：{wav_path}")

    try:
        with wave.open(str(wav_path), "rb") as wf:
            nchannels = wf.getnchannels()
            sampwidth = wf.getsampwidth()
            sample_rate = wf.getframerate()
            nframes = wf.getnframes()
            raw = wf.readframes(nframes)
    except wave.Error:
        return _read_ieee_float_wav(wav_path)

    if sample_rate <= 0:
        raise ValueError(f"WAV 采样率无效：{sample_rate}")
    if nframes == 0 or not raw:
        return [], int(sample_rate)

    return _decode_pcm_bytes(raw, sampwidth, nchannels), int(sample_rate)


def write_wav_pcm16(path: Path, samples: Sequence[float], sample_rate: int) -> None:
    """Write mono floating-point samples in [-1, 1] as a 16-bit PCM WAV file atomically."""
    sr = int(sample_rate)
    if sr <= 0:
        raise ValueError(f"采样率必须大于 0：{sample_rate}")
    out_path = Path(path)
    out_path.parent.mkdir(parents=True, exist_ok=True)

    pcm_bytes: bytes
    try:
        import numpy as np  # type: ignore[import-not-found]

        arr = np.asarray(samples, dtype=np.float64).reshape(-1)
        arr = np.nan_to_num(arr, nan=0.0, posinf=1.0, neginf=-1.0)
        arr = np.clip(arr, -1.0, 1.0)
        int16_arr = np.clip(np.rint(arr * 32767.0), -32768, 32767).astype("<i2")
        pcm_bytes = int16_arr.tobytes()
    except ImportError:
        pcm = array.array("h")
        for sample in samples:
            val = float(sample)
            if not math.isfinite(val):
                pcm.append(0)
                continue
            if val > 1.0:
                val = 1.0
            elif val < -1.0:
                val = -1.0
            q = int(round(val * 32767.0))
            if q > 32767:
                q = 32767
            elif q < -32768:
                q = -32768
            pcm.append(q)
        if sys.byteorder != "little":
            pcm.byteswap()
        pcm_bytes = pcm.tobytes()

    fd, tmp_name = tempfile.mkstemp(prefix=".wav-", suffix=".tmp", dir=out_path.parent)
    tmp_path = Path(tmp_name)
    try:
        with os.fdopen(fd, "wb") as raw_f:
            with wave.open(raw_f, "wb") as wf:
                wf.setnchannels(1)
                wf.setsampwidth(2)
                wf.setframerate(sr)
                wf.writeframes(pcm_bytes)
            raw_f.flush()
            os.fsync(raw_f.fileno())
        os.replace(tmp_path, out_path)
    finally:
        tmp_path.unlink(missing_ok=True)


def _resample_audio_np(arr: Any, src_sr: int, dst_sr: int) -> Any:
    """Vectorized NumPy/soxr/scipy resampling returning a 1-D float64 ndarray."""
    import numpy as np  # type: ignore[import-not-found]

    s_sr = int(src_sr)
    d_sr = int(dst_sr)
    arr_f64 = np.asarray(arr, dtype=np.float64).reshape(-1)
    n = arr_f64.size
    if n == 0 or s_sr == d_sr:
        return arr_f64
    out_len = max(1, int(round(n * d_sr / s_sr)))
    if n == 1:
        return np.full(out_len, float(arr_f64[0]), dtype=np.float64)

    if n >= 64:
        try:
            import soxr  # type: ignore[import-not-found]

            resampled = soxr.resample(
                arr_f64.astype(np.float32), s_sr, d_sr, quality="HQ"
            )
            return np.asarray(resampled, dtype=np.float64).reshape(-1)
        except ImportError:
            pass

        try:
            from scipy import signal as sp_signal  # type: ignore[import-not-found]

            divisor = math.gcd(s_sr, d_sr)
            up = d_sr // divisor
            down = s_sr // divisor
            # Bound polyphase filter length so prime sample rates cannot allocate gigabyte FIR filters.
            if max(up, down) <= 1000:
                resampled = sp_signal.resample_poly(arr_f64, up, down)
                return np.asarray(resampled, dtype=np.float64).reshape(-1)
        except ImportError:
            pass

    positions = np.arange(out_len, dtype=np.float64) * (s_sr / d_sr)
    positions = np.clip(positions, 0.0, float(n - 1))
    return np.interp(positions, np.arange(n, dtype=np.float64), arr_f64)


def resample_audio_linear(
    samples: Sequence[float], src_sr: int, dst_sr: int
) -> list[float]:
    """Resample mono audio from src_sr to dst_sr.

    Uses soxr or bounded scipy.signal.resample_poly when installed (for clips >= 64 samples),
    and falls back cleanly to linear interpolation using numpy or Python stdlib.
    """
    s_sr = int(src_sr)
    d_sr = int(dst_sr)
    if s_sr <= 0 or d_sr <= 0:
        raise ValueError(f"采样率必须大于 0：src_sr={src_sr}, dst_sr={dst_sr}")

    n = len(samples)
    if n == 0:
        return []
    if s_sr == d_sr:
        try:
            import numpy as np  # type: ignore[import-not-found]

            return np.asarray(samples, dtype=np.float64).reshape(-1).tolist()
        except ImportError:
            return [float(x) for x in samples]

    out_len = max(1, int(round(n * d_sr / s_sr)))
    if n == 1:
        return [float(samples[0])] * out_len

    try:
        return _resample_audio_np(samples, s_sr, d_sr).tolist()
    except ImportError:
        pass

    # Pure-Python linear interpolation fallback (zero third-party dependencies).
    src = [float(x) for x in samples]
    ratio = s_sr / d_sr
    last_idx = n - 1
    out: list[float] = [0.0] * out_len
    for i in range(out_len):
        pos = i * ratio
        if pos >= last_idx:
            out[i] = src[last_idx]
        else:
            idx = int(pos)
            frac = pos - idx
            out[i] = src[idx] + (src[idx + 1] - src[idx]) * frac
    return out


def _trim_wav_silence_np(
    arr: Any,
    sample_rate: int,
    threshold_db: float = -42.0,
    pad_ms: float = 35.0,
) -> Any:
    import numpy as np  # type: ignore[import-not-found]

    arr_f64 = np.asarray(arr, dtype=np.float64).reshape(-1)
    n = arr_f64.size
    if n == 0:
        return arr_f64
    threshold = 10.0 ** (float(threshold_db) / 20.0)
    pad_samples = max(0, int(round(sample_rate * max(0.0, float(pad_ms)) / 1000.0)))
    active = np.flatnonzero(np.abs(arr_f64) >= threshold)
    if active.size == 0:
        return np.empty(0, dtype=np.float64)
    lo = max(0, int(active[0]) - pad_samples)
    hi = min(n, int(active[-1]) + 1 + pad_samples)
    return arr_f64[lo:hi]


def trim_wav_silence(
    samples: Sequence[float],
    sample_rate: int,
    threshold_db: float = -42.0,
    pad_ms: float = 35.0,
) -> list[float]:
    """Trim leading and trailing silence below threshold_db while preserving pad_ms margin."""
    sr = int(sample_rate)
    if sr <= 0:
        raise ValueError(f"采样率必须大于 0：{sample_rate}")
    n = len(samples)
    if n == 0:
        return []

    try:
        return _trim_wav_silence_np(
            samples, sr, threshold_db=threshold_db, pad_ms=pad_ms
        ).tolist()
    except ImportError:
        pass

    threshold = 10.0 ** (float(threshold_db) / 20.0)
    pad_samples = max(0, int(round(sr * max(0.0, float(pad_ms)) / 1000.0)))

    first_idx = -1
    for i in range(n):
        if abs(float(samples[i])) >= threshold:
            first_idx = i
            break
    if first_idx < 0:
        return []

    last_idx = first_idx
    for i in range(n - 1, first_idx - 1, -1):
        if abs(float(samples[i])) >= threshold:
            last_idx = i
            break

    lo = max(0, first_idx - pad_samples)
    hi = min(n, last_idx + 1 + pad_samples)
    return [float(x) for x in samples[lo:hi]]


def _soft_limit_sample(
    value: float,
    knee: float = _SOFT_LIMITER_KNEE,
    ceiling: float = _SOFT_LIMITER_CEILING,
) -> float:
    if not math.isfinite(value):
        return 0.0
    mag = abs(value)
    if mag <= knee:
        return float(value)
    headroom = ceiling - knee
    limited = knee + headroom * math.tanh((mag - knee) / headroom)
    return math.copysign(min(ceiling, limited), value)


def _soft_limit_array_np(
    arr: Any,
    knee: float = _SOFT_LIMITER_KNEE,
    ceiling: float = _SOFT_LIMITER_CEILING,
) -> Any:
    import numpy as np  # type: ignore[import-not-found]

    clean = np.asarray(arr, dtype=np.float64).reshape(-1)
    clean = np.where(np.isfinite(clean), clean, 0.0)
    mag = np.abs(clean)
    over = mag > knee
    if not np.any(over):
        return clean
    headroom = ceiling - knee
    limited = knee + headroom * np.tanh((mag[over] - knee) / headroom)
    limited = np.minimum(ceiling, limited)
    out = clean.copy()
    out[over] = np.copysign(limited, clean[over])
    return out


def _soft_limit_samples(samples: Sequence[float]) -> list[float]:
    try:
        return _soft_limit_array_np(samples).tolist()
    except ImportError:
        return [_soft_limit_sample(v) for v in samples]


def _normalize_audio_loudness_np(
    arr: Any, sample_rate: int, target_lufs: float = -23.0
) -> Any:
    import numpy as np  # type: ignore[import-not-found]

    clean = np.asarray(arr, dtype=np.float64).reshape(-1)
    if clean.size == 0:
        return clean
    clean = np.where(np.isfinite(clean), clean, 0.0)
    rms = float(np.sqrt(np.mean(clean * clean)))
    # Never boost near-silence / noise floor (below -70 dBFS RMS).
    if rms < _SILENCE_FLOOR_RMS:
        return clean

    try:
        import pyloudnorm as pyln  # type: ignore[import-not-found]

        if clean.size > int(sample_rate * 0.4) + 1:
            meter = pyln.Meter(sample_rate)
            loudness = float(meter.integrated_loudness(clean))
            # Silence or sub-gate audio (<= -70 LUFS) must be returned unchanged, never RMS-boosted.
            if not math.isfinite(loudness) or loudness <= -70.0:
                return clean
            gain_db = max(-40.0, min(40.0, float(target_lufs) - loudness))
            gain = 10.0 ** (gain_db / 20.0)
            return _soft_limit_array_np(clean * gain)
    except ImportError:
        pass
    except Exception:
        pass

    target_rms = 10.0 ** (float(target_lufs) / 20.0)
    max_gain = 10.0 ** (40.0 / 20.0)
    min_gain = 10.0 ** (-40.0 / 20.0)
    gain = max(min_gain, min(max_gain, target_rms / rms))
    return _soft_limit_array_np(clean * gain)


def _try_pyloudnorm(
    vals: list[float], sample_rate: int, target_lufs: float
) -> list[float] | None:
    try:
        import pyloudnorm as pyln  # type: ignore[import-not-found]
    except ImportError:
        return None
    # ITU-R BS.1770 gating block requires > 400 ms of audio.
    if len(vals) <= int(sample_rate * 0.4) + 1:
        return None
    try:
        try:
            import numpy as np  # type: ignore[import-not-found]

            data: Any = np.asarray(vals, dtype=np.float64)
        except ImportError:
            data = vals
        meter = pyln.Meter(sample_rate)
        loudness = float(meter.integrated_loudness(data))
        # Return unchanged (not None!) so silence/hiss <= -70 LUFS never falls through to +40 dB RMS gain.
        if not math.isfinite(loudness) or loudness <= -70.0:
            return list(vals)
        gain_db = max(-40.0, min(40.0, float(target_lufs) - loudness))
        gain = 10.0 ** (gain_db / 20.0)
        return _soft_limit_samples([v * gain for v in vals])
    except Exception:
        return None


def normalize_audio_loudness(
    samples: Sequence[float], sample_rate: int, target_lufs: float = -23.0
) -> list[float]:
    """Normalize mono audio loudness to target_lufs with a soft peak limiter (<= 0.96).

    Uses pyloudnorm.Meter(sample_rate) when installed (matching EBU R128 -23 LUFS),
    and deterministically falls back to RMS loudness normalization + soft peak limiting.
    Near-silence (RMS < -70 dBFS or integrated loudness <= -70 LUFS) is returned unchanged.
    """
    sr = int(sample_rate)
    if sr <= 0:
        raise ValueError(f"采样率必须大于 0：{sample_rate}")
    if len(samples) == 0:
        return []

    try:
        return _normalize_audio_loudness_np(
            samples, sr, target_lufs=float(target_lufs)
        ).tolist()
    except ImportError:
        pass

    vals = [float(x) if math.isfinite(float(x)) else 0.0 for x in samples]
    sum_sq = sum(v * v for v in vals)
    rms = math.sqrt(sum_sq / len(vals))
    if rms < _SILENCE_FLOOR_RMS:
        return vals

    pyln_out = _try_pyloudnorm(vals, sr, float(target_lufs))
    if pyln_out is not None:
        return pyln_out

    target_rms = 10.0 ** (float(target_lufs) / 20.0)
    max_gain = 10.0 ** (40.0 / 20.0)
    min_gain = 10.0 ** (-40.0 / 20.0)
    gain = max(min_gain, min(max_gain, target_rms / rms))
    return _soft_limit_samples([v * gain for v in vals])


def _apply_edge_fade_np(arr: Any, sample_rate: int, fade_ms: float = 3.0) -> Any:
    """Apply a subtle symmetric fade-in/fade-out at trimmed segment boundaries to eliminate clicks."""
    import numpy as np  # type: ignore[import-not-found]

    n = int(arr.size)
    if n < 2 or sample_rate <= 0 or fade_ms <= 0.0:
        return arr
    fade_len = min(n // 2, int(round(sample_rate * fade_ms / 1000.0)))
    if fade_len <= 1:
        return arr
    out = arr.copy()
    ramp = np.linspace(0.0, 1.0, fade_len, endpoint=False, dtype=np.float64)
    out[:fade_len] *= ramp
    out[-fade_len:] *= ramp[::-1]
    return out


def _apply_edge_fade(
    samples: Sequence[float], sample_rate: int, fade_ms: float = 3.0
) -> list[float]:
    """Pure-Python symmetric fade-in/fade-out at trimmed segment boundaries."""
    out = [float(x) for x in samples]
    n = len(out)
    if n < 2 or sample_rate <= 0 or fade_ms <= 0.0:
        return out
    fade_len = min(n // 2, int(round(sample_rate * fade_ms / 1000.0)))
    if fade_len <= 1:
        return out
    inv = 1.0 / fade_len
    for i in range(fade_len):
        gain = i * inv
        out[i] *= gain
        out[n - 1 - i] *= gain
    return out


def assemble_colabone_tts_master(
    segment_wavs: list[tuple[str, Path]],
    out_wav: Path,
    manifest_path: Path,
    pause_seconds: float = 0.28,
    target_sr: int = 48000,
    target_lufs: float = -23.0,
) -> dict[str, Any]:
    """Trim, resample, stitch, normalize, and write master WAV + timeline manifest.json."""
    if not segment_wavs:
        raise ValueError("没有可拼接的语音片段")
    sr = int(target_sr)
    if sr <= 0:
        raise ValueError(f"目标采样率必须大于 0：{target_sr}")
    pause_sec = max(0.0, float(pause_seconds))
    pause_sample_count = int(round(pause_sec * sr))

    segments_meta: list[dict[str, Any]] = []
    silence_valleys: list[float] = []
    total_segments = len(segment_wavs)
    out_wav_path = Path(out_wav)
    manifest_file = Path(manifest_path)

    try:
        import numpy as np  # type: ignore[import-not-found]

        use_numpy = True
    except ImportError:
        np = None  # type: ignore[assignment]
        use_numpy = False

    if use_numpy:
        chunks_np: list[Any] = []
        pause_arr = (
            np.zeros(pause_sample_count, dtype=np.float64)
            if pause_sample_count > 0
            else None
        )
        cursor = 0
        for idx, (seg_text, wav_path) in enumerate(segment_wavs):
            raw_samples, src_sr = read_wav_mono_float(Path(wav_path))
            raw_arr = np.asarray(raw_samples, dtype=np.float64).reshape(-1)
            trimmed_arr = _trim_wav_silence_np(raw_arr, src_sr)
            if trimmed_arr.size == 0 and raw_arr.size > 0:
                trimmed_arr = raw_arr
            resampled_arr = _resample_audio_np(trimmed_arr, src_sr, sr)
            resampled_arr = _apply_edge_fade_np(resampled_arr, sr, fade_ms=3.0)

            start_sample = cursor
            if resampled_arr.size > 0:
                chunks_np.append(resampled_arr)
                cursor += int(resampled_arr.size)
            end_sample = cursor

            start_sec = round(start_sample / sr, 4)
            end_sec = round(end_sample / sr, 4)
            pause_midpoint: float | None = None

            if idx < total_segments - 1 and pause_sec > 0.0 and pause_sample_count > 0:
                if pause_arr is not None:
                    chunks_np.append(pause_arr)
                    cursor += pause_sample_count
                pause_midpoint = round(
                    (end_sample + pause_sample_count / 2.0) / sr, 4
                )
                silence_valleys.append(pause_midpoint)

            segments_meta.append(
                {
                    "index": idx,
                    "text": str(seg_text),
                    "start": start_sec,
                    "end": end_sec,
                    "pause_midpoint": pause_midpoint,
                }
            )

        master_arr = (
            np.concatenate(chunks_np)
            if chunks_np
            else np.empty(0, dtype=np.float64)
        )
        normalized_arr = _normalize_audio_loudness_np(
            master_arr, sr, target_lufs=float(target_lufs)
        )
        write_wav_pcm16(out_wav_path, normalized_arr, sr)
        sample_count = int(normalized_arr.size)
    else:
        pause_zeros = [0.0] * pause_sample_count
        master_samples: list[float] = []

        for idx, (seg_text, wav_path) in enumerate(segment_wavs):
            raw_samples, src_sr = read_wav_mono_float(Path(wav_path))
            trimmed = trim_wav_silence(raw_samples, src_sr)
            if not trimmed and raw_samples:
                trimmed = raw_samples
            resampled = resample_audio_linear(trimmed, src_sr, sr)
            resampled = _apply_edge_fade(resampled, sr, fade_ms=3.0)

            start_sample = len(master_samples)
            master_samples.extend(resampled)
            end_sample = len(master_samples)

            start_sec = round(start_sample / sr, 4)
            end_sec = round(end_sample / sr, 4)
            pause_midpoint: float | None = None

            if idx < total_segments - 1 and pause_sec > 0.0 and pause_sample_count > 0:
                master_samples.extend(pause_zeros)
                pause_midpoint = round(
                    (end_sample + pause_sample_count / 2.0) / sr, 4
                )
                silence_valleys.append(pause_midpoint)

            segments_meta.append(
                {
                    "index": idx,
                    "text": str(seg_text),
                    "start": start_sec,
                    "end": end_sec,
                    "pause_midpoint": pause_midpoint,
                }
            )

        normalized = normalize_audio_loudness(
            master_samples, sr, target_lufs=float(target_lufs)
        )
        write_wav_pcm16(out_wav_path, normalized, sr)
        sample_count = len(normalized)

    duration = round(sample_count / sr, 4)
    manifest: dict[str, Any] = {
        "sample_rate": sr,
        "pause_seconds": round(pause_sec, 4),
        "target_lufs": float(target_lufs),
        "duration": duration,
        "segments": segments_meta,
        "silence_valleys": silence_valleys,
    }
    atomic_json(manifest_file, manifest)
    return manifest


# ---------------------------------------------------------------------------
# 3. Subprocess & package installation helpers
# ---------------------------------------------------------------------------


_PEP508_IDENT = r"[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?"
_PEP508_VERSION_CLAUSE = (
    r"(?:===|==|!=|<=|>=|~=|<|>)\s*[A-Za-z0-9][A-Za-z0-9.*_+!-]*"
)
_PEP508_VERSION_LIST = (
    rf"{_PEP508_VERSION_CLAUSE}(?:\s*,\s*{_PEP508_VERSION_CLAUSE})*"
)
_PEP508_NAME_REQ_RE = re.compile(
    rf"^(?P<name>{_PEP508_IDENT})"
    rf"(?:\s*\[\s*{_PEP508_IDENT}(?:\s*,\s*{_PEP508_IDENT})*\s*\])?"
    rf"(?:\s*(?:\(\s*{_PEP508_VERSION_LIST}\s*\)|{_PEP508_VERSION_LIST}))?$"
)
_PEP508_MARKER_VARS = frozenset(
    {
        "python_version",
        "python_full_version",
        "os_name",
        "sys_platform",
        "platform_release",
        "platform_system",
        "platform_version",
        "platform_machine",
        "platform_python_implementation",
        "implementation_name",
        "implementation_version",
        "extra",
    }
)
_PEP508_MARKER_TOKEN_RE = re.compile(
    r"""\s*(?:
        (?P<str>'[^'\r\n\x00]*'|"[^"\r\n\x00]*")
        |(?P<op>===|==|!=|<=|>=|~=|<=|<|>)
        |(?P<paren>[()])
        |(?P<word>[A-Za-z_][A-Za-z0-9_]*)
    )""",
    re.VERBOSE,
)
_DIRECT_URL_OR_VCS_RE = re.compile(
    r"(?:://|\b(?:git|hg|svn|bzr)\+|\bfile:)",
    re.IGNORECASE,
)
_CLI_FLAG_TOKEN_RE = re.compile(r"(?:^|\s)(?:--|-[A-Za-z])")


def _normalize_pkg_name(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name.strip()).lower()


def _req_package_name(spec: str) -> str:
    raw = re.sub(r"\s+#.*$", "", str(spec)).strip()
    if not raw or raw.startswith("-"):
        return ""
    match = re.match(r"^([A-Za-z0-9_.-]+)", raw)
    if not match:
        return ""
    return _normalize_pkg_name(match.group(1))


def _validate_pep508_marker(marker: str) -> bool:
    s = marker.strip()
    if not s:
        return False
    if _DIRECT_URL_OR_VCS_RE.search(s):
        return False
    pos = 0
    depth = 0
    n = len(s)
    while pos < n:
        m = _PEP508_MARKER_TOKEN_RE.match(s, pos)
        if not m or m.end() == pos:
            return False
        pos = m.end()
        if m.group("paren"):
            depth += 1 if m.group("paren") == "(" else -1
            if depth < 0:
                return False
        elif m.group("word"):
            w = m.group("word")
            if w not in _PEP508_MARKER_VARS and w not in ("and", "or", "in", "not"):
                return False
    return depth == 0


def _sanitize_requirement_spec(raw: str) -> str:
    """Strictly validate a PEP 508 named package requirement; reject flags, newlines, and URLs."""
    raw_str = str(raw or "")
    if "\r" in raw_str or "\n" in raw_str or "\x00" in raw_str:
        return ""
    spec = raw_str.lstrip("\ufeff").strip()
    if not spec or spec.startswith("#"):
        return ""
    spec = re.sub(r"\s+#.*$", "", spec).strip()
    if not spec or spec.startswith("-"):
        return ""
    if _DIRECT_URL_OR_VCS_RE.search(spec):
        return ""

    if ";" in spec:
        req_part, marker_part = spec.split(";", 1)
        req_part = req_part.strip()
        marker_part = marker_part.strip()
        if not req_part or not _validate_pep508_marker(marker_part):
            return ""
    else:
        req_part = spec
        marker_part = ""

    if _CLI_FLAG_TOKEN_RE.search(req_part):
        return ""
    match = _PEP508_NAME_REQ_RE.fullmatch(req_part)
    if not match:
        return ""
    pkg_name = _normalize_pkg_name(match.group("name"))
    if pkg_name in _PIP_SKIP_PACKAGES:
        return ""
    return f"{req_part}; {marker_part}" if marker_part else req_part


def _join_requirement_lines(text: str) -> list[str]:
    joined: list[str] = []
    buf = ""
    for raw_line in text.splitlines():
        line = raw_line.rstrip()
        if line.endswith("\\"):
            buf += line[:-1] + " "
            continue
        if buf:
            joined.append(buf + line)
            buf = ""
        else:
            joined.append(line)
    if buf:
        joined.append(buf)
    return joined


def _filter_pip_packages(packages: Sequence[str]) -> list[str]:
    kept: list[str] = []
    for raw in packages:
        cleaned = _sanitize_requirement_spec(raw)
        if cleaned:
            kept.append(cleaned)
    return kept


def _extract_requirements_file_packages(req_path: Path) -> list[str]:
    text = req_path.read_text(encoding="utf-8-sig")
    return _filter_pip_packages(_join_requirement_lines(text))


def _strip_toml_header_comment(line: str) -> str:
    in_quote: str | None = None
    escaped = False
    for idx, ch in enumerate(line):
        if in_quote is not None:
            if in_quote == '"' and ch == "\\" and not escaped:
                escaped = True
                continue
            if ch == in_quote and not escaped:
                in_quote = None
            escaped = False
        else:
            if ch in ('"', "'"):
                in_quote = ch
            elif ch == "#":
                return line[:idx].strip()
    return line.strip()


def _update_toml_multiline_state(line: str, ml_quote: str | None) -> str | None:
    """Track whether TOML scanning is currently inside a triple-quoted multiline string."""
    i = 0
    n = len(line)
    in_single: str | None = None
    escaped = False
    while i < n:
        ch = line[i]
        if ml_quote is not None:
            if ml_quote == '"""':
                if ch == "\\" and not escaped:
                    escaped = True
                    i += 1
                    continue
                if not escaped and line[i : i + 3] == '"""':
                    ml_quote = None
                    i += 3
                    continue
                escaped = False
            else:
                if line[i : i + 3] == "'''":
                    ml_quote = None
                    i += 3
                    continue
            i += 1
            continue

        if in_single is not None:
            if in_single == '"' and ch == "\\" and not escaped:
                escaped = True
                i += 1
                continue
            if ch == in_single and not escaped:
                in_single = None
            escaped = False
            i += 1
            continue

        if ch == "#":
            break
        if line[i : i + 3] in ('"""', "'''"):
            ml_quote = line[i : i + 3]
            escaped = False
            i += 3
            continue
        if ch in ('"', "'"):
            in_single = ch
            escaped = False
            i += 1
            continue
        i += 1
    return ml_quote


def _parse_pyproject_dependencies_fallback(raw_text: str) -> list[str]:
    """Safe zero-dependency fallback parser for [project].dependencies on Python 3.10."""
    in_project = False
    collecting = False
    ml_quote: str | None = None
    depth = 0
    quote: str | None = None
    escaped = False
    current_token: list[str] = []
    deps: list[str] = []

    def _consume_chunk(chunk: str) -> bool:
        nonlocal depth, quote, escaped, current_token
        i = 0
        n = len(chunk)
        while i < n:
            ch = chunk[i]
            if quote is not None:
                if quote in ('"""', "'''"):
                    if chunk[i : i + 3] == quote and not escaped:
                        deps.append("".join(current_token))
                        current_token = []
                        quote = None
                        i += 3
                        continue
                    if quote == '"""' and ch == "\\" and not escaped:
                        escaped = True
                        i += 1
                        continue
                    if escaped:
                        escape_map = {"n": "\n", "t": "\t", "r": "\r", '"': '"', "\\": "\\"}
                        current_token.append(escape_map.get(ch, ch))
                        escaped = False
                    else:
                        current_token.append(ch)
                    i += 1
                    continue
                if quote == '"' and ch == "\\" and not escaped:
                    escaped = True
                    i += 1
                    continue
                if ch == quote and not escaped:
                    deps.append("".join(current_token))
                    current_token = []
                    quote = None
                    i += 1
                    continue
                if escaped:
                    escape_map = {"n": "\n", "t": "\t", "r": "\r", '"': '"', "\\": "\\"}
                    current_token.append(escape_map.get(ch, ch))
                    escaped = False
                else:
                    current_token.append(ch)
                i += 1
                continue

            if ch == "#":
                break
            if ch == "[":
                depth += 1
                i += 1
                continue
            if ch == "]":
                depth -= 1
                i += 1
                if depth <= 0:
                    return True
                continue
            if ch in ('"', "'"):
                if chunk[i : i + 3] == ch * 3:
                    quote = ch * 3
                    current_token = []
                    escaped = False
                    i += 3
                    continue
                quote = ch
                current_token = []
                escaped = False
                i += 1
                continue
            i += 1
        return False

    for raw_line in raw_text.splitlines():
        if not collecting:
            if ml_quote is not None:
                ml_quote = _update_toml_multiline_state(raw_line, ml_quote)
                continue
            stripped = raw_line.strip()
            if not stripped or stripped.startswith("#"):
                continue
            if stripped.startswith("["):
                header = _strip_toml_header_comment(stripped)
                in_project = header == "[project]"
                continue
            if in_project:
                match = re.match(r"^dependencies\s*=\s*(.*)$", stripped)
                if match:
                    rhs = match.group(1)
                    bracket_pos = rhs.find("[")
                    if bracket_pos >= 0:
                        collecting = True
                        depth = 0
                        if _consume_chunk(rhs[bracket_pos:]):
                            break
                        continue
            ml_quote = _update_toml_multiline_state(raw_line, ml_quote)
        else:
            if _consume_chunk(raw_line):
                break
    return deps


def _extract_pyproject_dependencies(pyproject_path: Path) -> list[str]:
    """Extract and filter [project].dependencies from pyproject.toml."""
    raw_text = pyproject_path.read_text(encoding="utf-8-sig")
    raw_deps: list[str] = []
    if tomllib is not None:
        try:
            parsed = tomllib.loads(raw_text)
            project = parsed.get("project") if isinstance(parsed, dict) else None
            deps = project.get("dependencies") if isinstance(project, dict) else None
            if isinstance(deps, list):
                raw_deps = [str(item) for item in deps if isinstance(item, str)]
        except Exception:
            raw_deps = _parse_pyproject_dependencies_fallback(raw_text)
    else:
        raw_deps = _parse_pyproject_dependencies_fallback(raw_text)
    return _filter_pip_packages(raw_deps)


def _collect_installed_torch_constraints() -> list[str]:
    lines: list[str] = []
    for pkg in _TORCH_CONSTRAINT_PACKAGES:
        try:
            ver = importlib.metadata.version(pkg)
        except importlib.metadata.PackageNotFoundError:
            continue
        except Exception:
            continue
        ver_str = str(ver or "").strip()
        if ver_str:
            lines.append(f"{pkg}=={ver_str}")
    return lines


def _write_torch_constraints_file(
    out_path: Path, constraints: Sequence[str] | None = None
) -> Path:
    if out_path.is_symlink():
        raise ValueError("PyTorch 约束文件不能是符号链接")
    lines = (
        list(constraints)
        if constraints is not None
        else _collect_installed_torch_constraints()
    )
    out_path.parent.mkdir(parents=True, exist_ok=True)
    body = "# ColabOne pinned PyTorch stack constraints\n"
    if lines:
        body += "\n".join(lines) + "\n"
    out_path.write_text(body, encoding="utf-8")
    return out_path


def _find_venv_python_on_disk(venv_dir: Path) -> Path | None:
    """Return the venv Python executable path if it exists on disk, else None."""
    for candidate in (
        venv_dir / "bin" / "python",
        venv_dir / "bin" / "python3",
        venv_dir / "Scripts" / "python.exe",
    ):
        if candidate.is_file():
            return candidate
    return None


def _resolve_venv_python(venv_dir: Path) -> Path:
    """Resolve an isolated per-engine venv Python executable, falling back to sys.executable."""
    found = _find_venv_python_on_disk(venv_dir)
    if found is not None:
        return found
    return Path(sys.executable)


def _is_standard_mono_pcm16_wav(path: Path) -> bool:
    """Return True if path is a standard uncompressed mono 16-bit PCM WAV."""
    try:
        with wave.open(str(path), "rb") as wf:
            return (
                wf.getcomptype() == "NONE"
                and wf.getnchannels() == 1
                and wf.getsampwidth() == 2
                and wf.getframerate() > 0
            )
    except (wave.Error, EOFError, OSError):
        return False


_is_standard_pcm_wav = _is_standard_mono_pcm16_wav


def _normalize_git_repo_url(repo: str) -> str:
    cleaned = str(repo or "").strip()
    if _REPO_SLUG_RE.fullmatch(cleaned):
        owner, name = cleaned.split("/", 1)
        if not _GITHUB_SEGMENT_RE.fullmatch(owner) or not _GITHUB_SEGMENT_RE.fullmatch(name):
            raise ValueError(f"非法的 GitHub 仓库地址：{repo!r}")
        return f"https://github.com/{owner}/{name}.git"
    match = _GITHUB_REPO_URL_RE.fullmatch(cleaned)
    if not match:
        raise ValueError(
            f"TTS 引擎源码仓库仅允许使用 https://github.com/<owner>/<repo> 地址：{repo!r}"
        )
    owner, name = match.group("owner"), match.group("repo")
    return f"https://github.com/{owner}/{name}.git"


def _scrub_secret(text: str, secret: str) -> str:
    if not secret or not text:
        return text
    return text.replace(secret, "***")


def _pgid_alive(pgid: int) -> bool:
    if not hasattr(os, "killpg"):
        return False
    try:
        os.killpg(pgid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False


def _terminate_process_group(proc: subprocess.Popen, timeout: float = 5.0) -> None:
    """Terminate the entire process group (SIGTERM -> wait -> SIGKILL), even after leader exit."""
    pid = getattr(proc, "pid", None)
    has_killpg = isinstance(pid, int) and pid > 0 and hasattr(os, "killpg")
    if has_killpg:
        try:
            os.killpg(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except (PermissionError, OSError):
            if proc.poll() is None:
                try:
                    proc.terminate()
                except OSError:
                    pass
    elif proc.poll() is None:
        try:
            proc.terminate()
        except OSError:
            pass

    deadline = time.monotonic() + max(0.0, float(timeout))
    if proc.poll() is None:
        remaining = max(0.0, deadline - time.monotonic())
        try:
            proc.wait(timeout=remaining)
        except subprocess.TimeoutExpired:
            pass

    if has_killpg:
        # Even if the leader has already exited, orphaned child processes may remain in the group.
        # Poll inside the loop to reap a zombie leader immediately so _pgid_alive clears without spinning.
        while True:
            proc.poll()
            if not _pgid_alive(pid) or time.monotonic() >= deadline:
                break
            time.sleep(0.05)
        kill_deadline = time.monotonic() + min(1.0, max(0.15, float(timeout)))
        while True:
            proc.poll()
            if not _pgid_alive(pid) or time.monotonic() >= kill_deadline:
                break
            try:
                os.killpg(pid, signal.SIGKILL)
            except (ProcessLookupError, PermissionError, OSError):
                break
            time.sleep(0.05)
        proc.poll()

    if proc.poll() is None:
        try:
            proc.kill()
        except OSError:
            pass
        try:
            proc.wait(timeout=max(0.5, float(timeout)))
        except subprocess.TimeoutExpired:
            pass


def _read_log_tail(log_path: Path, max_bytes: int = 4096) -> str:
    """Read only the tail of a worker log file by seeking near EOF without splitting UTF-8 codepoints."""
    try:
        with log_path.open("rb") as fh:
            fh.seek(0, os.SEEK_END)
            size = fh.tell()
            truncated = size > max_bytes
            fh.seek(max(0, size - max_bytes), os.SEEK_SET)
            raw = fh.read()
        if truncated and raw:
            idx = 0
            n = len(raw)
            while idx < n and (raw[idx] & 0xC0) == 0x80:
                idx += 1
            if idx:
                raw = raw[idx:]
        return raw.decode("utf-8", errors="replace")
    except OSError:
        return ""


def _run_tts_subprocess(
    args: Sequence[str],
    task: Any,
    log: Path,
    *,
    timeout: int = 1800,
    cwd: Path | str | None = None,
    env: dict[str, str] | None = None,
    stopping: threading.Event | None = None,
    secret_to_scrub: str = "",
    append_log: bool = False,
) -> None:
    """Run a cancelable subprocess in its own process group so VRAM is freed on exit."""
    if log.is_symlink():
        raise ValueError("TTS 日志文件路径不能是符号链接")
    log.parent.mkdir(parents=True, exist_ok=True)
    with log.open("ab" if append_log else "wb") as out:
        proc = subprocess.Popen(
            list(args),
            cwd=str(cwd) if cwd is not None else None,
            env=env,
            stdout=out,
            stderr=subprocess.STDOUT,
            stdin=subprocess.DEVNULL,
            start_new_session=True,
        )

    has_cancel_hook = task is not None and hasattr(task, "cancel_hook")
    task_lock = getattr(task, "lock", None) if has_cancel_hook else None
    prev_cancel_hook = None
    term_lock = threading.Lock()

    def _cancel_proc() -> None:
        with term_lock:
            _terminate_process_group(proc, timeout=2.0)
        if callable(prev_cancel_hook):
            try:
                prev_cancel_hook()
            except Exception:
                pass

    if has_cancel_hook:
        if task_lock is not None:
            with task_lock:
                prev_cancel_hook = getattr(task, "cancel_hook", None)
                task.cancel_hook = _cancel_proc
        else:
            prev_cancel_hook = getattr(task, "cancel_hook", None)
            task.cancel_hook = _cancel_proc

    end = time.monotonic() + timeout
    noted = 0.0
    try:
        while proc.poll() is None:
            check_fn = getattr(task, "check", None)
            if callable(check_fn):
                check_fn()
            if isinstance(stopping, threading.Event) and stopping.is_set():
                from server.runtime import ColabOneCancelledError

                raise ColabOneCancelledError("实例正在停止，已取消远端 TTS 任务")
            if time.monotonic() > end:
                raise RuntimeError("远端 TTS 执行超时，请查看运行日志")
            if time.monotonic() - noted > 5.0:
                noted = time.monotonic()
                scrubbed_tail = _scrub_secret(_read_log_tail(log, 4096), secret_to_scrub)
                tail_lines = scrubbed_tail[-1000:].splitlines()
                if tail_lines:
                    line = tail_lines[-1][:250]
                    _safe_task_note(task, line)
            time.sleep(0.25)
        check_fn = getattr(task, "check", None)
        if callable(check_fn):
            check_fn()
        if isinstance(stopping, threading.Event) and stopping.is_set():
            from server.runtime import ColabOneCancelledError

            raise ColabOneCancelledError("实例正在停止，已取消远端 TTS 任务")
        if proc.returncode:
            scrubbed_tail = _scrub_secret(_read_log_tail(log, 4096), secret_to_scrub)
            tail = scrubbed_tail[-800:]
            raise RuntimeError(f"远端 TTS 执行失败（退出码 {proc.returncode}）：{tail}")
    finally:
        try:
            with term_lock:
                _terminate_process_group(proc)
        finally:
            if has_cancel_hook:
                if task_lock is not None:
                    with task_lock:
                        if getattr(task, "cancel_hook", None) is _cancel_proc:
                            task.cancel_hook = prev_cancel_hook
                elif getattr(task, "cancel_hook", None) is _cancel_proc:
                    task.cancel_hook = prev_cancel_hook


def _safe_task_note(task: Any, message: str, level: str = "info") -> None:
    note_fn = getattr(task, "note", None)
    if not callable(note_fn):
        return
    try:
        note_fn(message, level=level)
    except TypeError:
        note_fn(message)


def _safe_task_update(
    task: Any, *, progress: float | None = None, message: str | None = None
) -> None:
    """Thread-safely update task.progress and/or task.message under task.lock when present."""
    if task is None:
        return
    lock = getattr(task, "lock", None)
    if lock is not None:
        with lock:
            if progress is not None and hasattr(task, "progress"):
                task.progress = progress
            if message is not None and hasattr(task, "message"):
                task.message = message
    else:
        if progress is not None and hasattr(task, "progress"):
            task.progress = progress
        if message is not None and hasattr(task, "message"):
            task.message = message


def _call_with_compatible_kwargs(
    fn: Callable[..., Any],
    positional: tuple[Any, ...],
    optional_kwargs: dict[str, Any],
) -> Any:
    """Invoke a user-injected test callback passing only kwargs it accepts."""
    try:
        sig = inspect.signature(fn)
    except (TypeError, ValueError):
        return fn(*positional, **optional_kwargs)

    params = list(sig.parameters.values())
    has_var_kw = any(p.kind == inspect.Parameter.VAR_KEYWORD for p in params)
    has_var_pos = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in params)

    pos_params = [
        p
        for p in params
        if p.kind
        in (
            inspect.Parameter.POSITIONAL_ONLY,
            inspect.Parameter.POSITIONAL_OR_KEYWORD,
        )
    ]
    used_pos = positional if has_var_pos else positional[: len(pos_params)]
    bound_names = {p.name for p in pos_params[: len(used_pos)]}

    if has_var_kw:
        filtered_kw = {
            k: v for k, v in optional_kwargs.items() if k not in bound_names
        }
        return fn(*used_pos, **filtered_kw)

    accepted_kw = {
        p.name
        for p in params
        if p.kind
        in (
            inspect.Parameter.POSITIONAL_OR_KEYWORD,
            inspect.Parameter.KEYWORD_ONLY,
        )
        and p.name not in bound_names
    }
    filtered_kw = {k: v for k, v in optional_kwargs.items() if k in accepted_kw}
    return fn(*used_pos, **filtered_kw)


# ---------------------------------------------------------------------------
# 4. Self-contained per-engine worker script generators
# ---------------------------------------------------------------------------

_WORKER_COMMON_HEADER = textwrap.dedent(
    """\
    #!/usr/bin/env python3
    from __future__ import annotations

    import argparse
    import array
    import gc
    import inspect
    import io
    import json
    import math
    import os
    import sys
    import wave
    from pathlib import Path


    def _hf_token() -> str | None:
        return (
            os.environ.get("HF_TOKEN")
            or os.environ.get("HUGGING_FACE_HUB_TOKEN")
            or None
        )


    def _to_mono_list(audio) -> list[float]:
        if hasattr(audio, "detach"):
            tensor = audio.detach().cpu()
            dtype_name = str(getattr(tensor, "dtype", ""))
            if "bfloat16" in dtype_name or "float16" in dtype_name:
                tensor = tensor.float()
            try:
                audio = tensor.numpy()
            except TypeError:
                audio = tensor.float().numpy()
        try:
            import numpy as np

            arr = np.asarray(audio)
            if np.issubdtype(arr.dtype, np.integer):
                scale = (
                    32768.0
                    if arr.dtype == np.int16
                    else float(np.iinfo(arr.dtype).max)
                )
                arr = arr.astype(np.float32) / scale
            else:
                arr = arr.astype(np.float32)
            if arr.ndim == 0:
                arr = arr.reshape(1)
            elif arr.ndim == 2:
                if arr.shape[0] <= 8 and arr.shape[0] < arr.shape[1]:
                    arr = arr.mean(axis=0)
                else:
                    arr = arr.mean(axis=1)
            elif arr.ndim > 2:
                arr = arr.reshape(-1)
            arr = np.nan_to_num(arr, nan=0.0, posinf=1.0, neginf=-1.0)
            return arr.tolist()
        except ImportError:
            return [float(x) for x in audio]


    def _write_pcm16_wav(out_path: str, audio, sample_rate: int) -> None:
        target = Path(out_path)
        target.parent.mkdir(parents=True, exist_ok=True)
        try:
            import numpy as np
            import soundfile as sf

            arr = np.asarray(_to_mono_list(audio), dtype=np.float32)
            arr = np.clip(arr, -1.0, 1.0)
            sf.write(str(target), arr, int(sample_rate), subtype="PCM_16")
            return
        except ImportError:
            pass

        mono = _to_mono_list(audio)
        pcm = array.array("h")
        for sample in mono:
            val = float(sample)
            if not math.isfinite(val):
                pcm.append(0)
                continue
            val = max(-1.0, min(1.0, val))
            pcm.append(max(-32768, min(32767, int(round(val * 32767.0)))))
        if sys.byteorder != "little":
            pcm.byteswap()
        with wave.open(str(target), "wb") as wf:
            wf.setnchannels(1)
            wf.setsampwidth(2)
            wf.setframerate(int(sample_rate))
            wf.writeframes(pcm.tobytes())
    """
)

_VOXCPM2_WORKER_BODY = textwrap.dedent(
    """\

    def _load_voxcpm_model(repo: str, revision: str, cache_dir: str, token: str | None):
        from huggingface_hub import snapshot_download
        import voxcpm

        sig = inspect.signature(voxcpm.VoxCPM.from_pretrained)
        base_kwargs = {}
        if "load_denoiser" in sig.parameters:
            base_kwargs["load_denoiser"] = False
        if "optimize" in sig.parameters:
            base_kwargs["optimize"] = False

        try:
            kwargs = dict(base_kwargs)
            if "revision" in sig.parameters and revision:
                kwargs["revision"] = revision
            if "token" in sig.parameters and token:
                kwargs["token"] = token
            if "cache_dir" in sig.parameters and cache_dir:
                kwargs["cache_dir"] = cache_dir
            if "revision" in kwargs or not revision:
                return voxcpm.VoxCPM.from_pretrained(repo, **kwargs)
        except TypeError:
            pass

        local_dir = snapshot_download(
            repo_id=repo,
            revision=revision or None,
            cache_dir=cache_dir or None,
            token=token,
        )
        return voxcpm.VoxCPM.from_pretrained(local_dir, **base_kwargs)


    def main() -> None:
        parser = argparse.ArgumentParser(description="ColabOne VoxCPM2 remote TTS worker")
        parser.add_argument("--config", required=True)
        args = parser.parse_args()

        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        token = _hf_token()
        repo = cfg["repo"]
        revision = cfg.get("revision") or ""
        cache_dir = cfg.get("cache_dir") or ""
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        ref_text = (cfg.get("ref_text") or "").strip()
        voice_prompt = (cfg.get("voice_prompt") or "").strip()
        voice_seed = cfg.get("voice_seed")
        default_voice_prompt = (
            "A 26-year-old Chinese female host, clear warm articulate studio microphone voice, "
            "natural smooth pacing, friendly confident professional tone"
        )
        segments = cfg.get("segments") or []

        model = _load_voxcpm_model(repo, revision, cache_dir, token)
        sample_rate = int(
            getattr(getattr(model, "tts_model", None), "sample_rate", None)
            or getattr(model, "sample_rate", None)
            or cfg.get("sample_rate")
            or 48000
        )
        gen_sig = inspect.signature(model.generate)
        gen_params = gen_sig.parameters
        named_gen_params = [
            p
            for p in gen_params.values()
            if p.kind
            not in (inspect.Parameter.VAR_POSITIONAL, inspect.Parameter.VAR_KEYWORD)
        ]
        if not named_gen_params:
            target_fn = getattr(model, "_generate", model.generate)
            if target_fn is not model.generate and callable(target_fn):
                try:
                    target_sig = inspect.signature(target_fn)
                    target_params = target_sig.parameters
                    if any(
                        p.kind
                        not in (
                            inspect.Parameter.VAR_POSITIONAL,
                            inspect.Parameter.VAR_KEYWORD,
                        )
                        for p in target_params.values()
                    ):
                        gen_sig = target_sig
                        gen_params = target_params
                except (TypeError, ValueError):
                    pass
        has_var_kw = any(
            p.kind == inspect.Parameter.VAR_KEYWORD for p in gen_params.values()
        )

        if ref_audio_path and not ref_text and "reference_wav_path" not in gen_params:
            raise ValueError("VoxCPM2 当前版本克隆音色时需要同时填写参考音频对应文本")

        active_ref_audio = ref_audio_path
        active_ref_text = ref_text

        if isinstance(voice_seed, int) and not isinstance(voice_seed, bool) and voice_seed >= 0:
            try:
                import random as _py_random

                _py_random.seed(voice_seed)
            except Exception:
                pass
            try:
                import numpy as _np

                _np.random.seed(voice_seed % (2**32))
            except Exception:
                pass
            try:
                import torch

                torch.manual_seed(voice_seed)
                cuda_mod = getattr(torch, "cuda", None)
                if cuda_mod is not None and callable(getattr(cuda_mod, "manual_seed_all", None)):
                    cuda_mod.manual_seed_all(voice_seed)
            except Exception:
                pass

        def _sanitize_voxcpm_prompt(raw_prompt: str) -> str:
            return " ".join(raw_prompt.replace("(", " ").replace(")", " ").split())

        total = len(segments)
        for idx, seg in enumerate(segments):
            seg_text = str(seg["text"])
            out_wav = str(seg["output_wav"])
            kwargs = {
                "text": seg_text,
                "inference_timesteps": 18,
                "cfg_value": 2.0,
                "normalize": True,
            }
            if active_ref_audio:
                if active_ref_text:
                    kwargs["prompt_wav_path"] = active_ref_audio
                    kwargs["prompt_text"] = active_ref_text
                    if "reference_wav_path" in gen_params:
                        kwargs["reference_wav_path"] = active_ref_audio
                elif "reference_wav_path" in gen_params:
                    kwargs["reference_wav_path"] = active_ref_audio
                    safe_vp = _sanitize_voxcpm_prompt(voice_prompt)
                    if safe_vp:
                        kwargs["text"] = f"({safe_vp}){seg_text}"
                else:
                    raise ValueError(
                        "VoxCPM2 当前版本克隆音色时需要同时填写参考音频对应文本"
                    )
            else:
                effective_voice_prompt = (
                    _sanitize_voxcpm_prompt(voice_prompt) or default_voice_prompt
                )
                if "voice_prompt" in gen_params:
                    kwargs["voice_prompt"] = effective_voice_prompt
                else:
                    kwargs["text"] = f"({effective_voice_prompt}){seg_text}"

            if not has_var_kw:
                kwargs = {k: v for k, v in kwargs.items() if k in gen_params}

            wav = model.generate(**kwargs)
            _write_pcm16_wav(out_wav, wav, sample_rate)
            if idx == 0 and not active_ref_audio and total > 1:
                active_ref_audio = out_wav
                active_ref_text = seg_text
                print(
                    f"[colabone-tts] voxcpm2 locked speaker timbre from segment 1: {out_wav}",
                    flush=True,
                )
            print(f"[colabone-tts] voxcpm2 segment {idx + 1}/{total} done", flush=True)


    if __name__ == "__main__":
        main()
    """
)

_QWEN3_TTS_WORKER_BODY = textwrap.dedent(
    """\

    def _load_qwen3_model(repo: str, revision: str, cache_dir: str, token: str | None):
        import torch
        from huggingface_hub import snapshot_download
        import qwen_tts

        use_cuda = torch.cuda.is_available()
        device_map = "cuda:0" if use_cuda else "cpu"
        dtype = (
            torch.bfloat16
            if (use_cuda and torch.cuda.is_bf16_supported())
            else (torch.float16 if use_cuda else torch.float32)
        )

        local_dir = snapshot_download(
            repo_id=repo,
            revision=revision or None,
            cache_dir=cache_dir or None,
            token=token,
        )
        try:
            return qwen_tts.Qwen3TTSModel.from_pretrained(
                local_dir,
                device_map=device_map,
                dtype=dtype,
            )
        except TypeError:
            return qwen_tts.Qwen3TTSModel.from_pretrained(
                local_dir,
                device_map=device_map,
                torch_dtype=dtype,
            )


    def main() -> None:
        parser = argparse.ArgumentParser(description="ColabOne Qwen3-TTS remote worker")
        parser.add_argument("--config", required=True)
        args = parser.parse_args()

        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        token = _hf_token()
        repo = (cfg.get("repo") or "").strip()
        revision = cfg.get("revision") or ""
        aux_repo = (cfg.get("aux_repo") or "").strip()
        aux_revision = (cfg.get("aux_revision") or "").strip()
        cache_dir = cfg.get("cache_dir") or ""
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        ref_text = (cfg.get("ref_text") or "").strip()
        voice_prompt = (cfg.get("voice_prompt") or "").strip()
        voice_seed = cfg.get("voice_seed")
        segments = cfg.get("segments") or []

        if not ref_audio_path and not aux_repo:
            raise ValueError("Qwen3-TTS 缺少音色参考音频或 VoiceDesign 辅助模型配置")

        # Auto-select 1.7B-Base for 3-second zero-shot cloning vs 1.7B-VoiceDesign for prompt design.
        use_voice_design = (not ref_audio_path) and bool(aux_repo)
        if use_voice_design:
            selected_repo = aux_repo
            selected_rev = aux_revision
            effective_instruct = (
                voice_prompt
                or "26岁年轻知性女性主播，清澈温润的现代标准普通话，吐字清晰圆润，棚录近麦高保真质感，语速自然流畅，专业沉稳且富有亲和力。"
            )
        else:
            selected_repo = repo
            selected_rev = revision
            effective_instruct = voice_prompt

        print(
            f"[colabone-tts] qwen3_tts mode={'voice_design' if use_voice_design else 'voice_clone'} repo={selected_repo}",
            flush=True,
        )
        model = _load_qwen3_model(selected_repo, selected_rev, cache_dir, token)

        clone_prompt = None
        if not use_voice_design and ref_audio_path and hasattr(model, "create_voice_clone_prompt"):
            clone_prompt = model.create_voice_clone_prompt(
                ref_audio=ref_audio_path,
                ref_text=ref_text or None,
                x_vector_only_mode=(not bool(ref_text)),
            )

        if isinstance(voice_seed, int) and not isinstance(voice_seed, bool) and voice_seed >= 0:
            try:
                import random as _py_random

                _py_random.seed(voice_seed)
            except Exception:
                pass
            try:
                import numpy as _np

                _np.random.seed(voice_seed % (2**32))
            except Exception:
                pass
            try:
                import torch

                torch.manual_seed(voice_seed)
                cuda_mod = getattr(torch, "cuda", None)
                if cuda_mod is not None and callable(getattr(cuda_mod, "manual_seed_all", None)):
                    cuda_mod.manual_seed_all(voice_seed)
            except Exception:
                pass

        total = len(segments)
        default_sr = int(cfg.get("sample_rate") or 24000)
        start_idx = 0

        # Lock speaker timbre across multi-sentence voice_design runs by generating segment 0 with
        # VoiceDesign, then transitioning to the Base model with a voice_clone_prompt from segment 0.
        if use_voice_design and total > 1 and repo:
            import torch

            seg0 = segments[0]
            seg0_text = str(seg0["text"])
            seg0_out = str(seg0["output_wav"])
            wavs0, sr0 = model.generate_voice_design(
                text=seg0_text,
                language="Auto",
                instruct=effective_instruct,
            )
            wav0 = wavs0[0] if isinstance(wavs0, (list, tuple)) else wavs0
            seg0_sr = int(sr0 or default_sr)
            _write_pcm16_wav(seg0_out, wav0, seg0_sr)
            print(f"[colabone-tts] qwen3_tts segment 1/{total} done", flush=True)
            start_idx = 1

            base_model = None
            try:
                print(
                    f"[colabone-tts] qwen3_tts locking speaker timbre via base repo={repo}",
                    flush=True,
                )
                del model
                model = None
                gc.collect()
                if getattr(torch, "cuda", None) is not None and torch.cuda.is_available():
                    torch.cuda.empty_cache()

                base_model = _load_qwen3_model(repo, revision, cache_dir, token)
                try:
                    import numpy as np

                    seg0_ref_audio = (
                        np.asarray(_to_mono_list(wav0), dtype=np.float32),
                        seg0_sr,
                    )
                except ImportError:
                    seg0_ref_audio = (_to_mono_list(wav0), seg0_sr)

                if hasattr(base_model, "create_voice_clone_prompt"):
                    clone_prompt = base_model.create_voice_clone_prompt(
                        ref_audio=seg0_ref_audio,
                        ref_text=seg0_text,
                        x_vector_only_mode=False,
                    )
                model = base_model
                use_voice_design = False
                ref_audio_path = seg0_out
                ref_text = seg0_text
            except Exception as exc:
                print(
                    f"[colabone-tts] qwen3_tts timbre lock fallback to voice_design: {exc}",
                    flush=True,
                )
                if base_model is not None:
                    del base_model
                    gc.collect()
                    if getattr(torch, "cuda", None) is not None and torch.cuda.is_available():
                        torch.cuda.empty_cache()
                if model is None:
                    model = _load_qwen3_model(aux_repo, aux_revision, cache_dir, token)
                use_voice_design = True
                clone_prompt = None

        for idx in range(start_idx, total):
            seg = segments[idx]
            seg_text = str(seg["text"])
            out_wav = str(seg["output_wav"])
            if use_voice_design:
                wavs, sr = model.generate_voice_design(
                    text=seg_text,
                    language="Auto",
                    instruct=effective_instruct,
                )
            elif clone_prompt is not None:
                wavs, sr = model.generate_voice_clone(
                    text=seg_text,
                    language="Auto",
                    voice_clone_prompt=clone_prompt,
                )
            else:
                wavs, sr = model.generate_voice_clone(
                    text=seg_text,
                    language="Auto",
                    ref_audio=ref_audio_path or None,
                    ref_text=ref_text or None,
                    x_vector_only_mode=(not bool(ref_text)),
                )

            wav = wavs[0] if isinstance(wavs, (list, tuple)) else wavs
            _write_pcm16_wav(out_wav, wav, int(sr or default_sr))
            print(f"[colabone-tts] qwen3_tts segment {idx + 1}/{total} done", flush=True)


    if __name__ == "__main__":
        main()
    """
)

_FISH_S2_PRO_WORKER_BODY = textwrap.dedent(
    """\

    def _apply_emotion_tag(text: str, emotion_prompt: str) -> str:
        prompt = emotion_prompt.strip()
        if not prompt:
            return text
        tag = prompt if (prompt.startswith("[") and prompt.endswith("]")) else f"[{prompt}]"
        if text.lstrip().startswith(tag):
            return text
        return f"{tag} {text}"


    def _find_decoder_checkpoint(model_dir: Path) -> Path:
        candidates = [
            "codec.pth",
            "firefly-gan-vq-fsq-8x1024-21hz-generator.pth",
        ]
        for name in candidates:
            candidate = model_dir / name
            if candidate.is_file():
                return candidate
        for path in sorted(model_dir.glob("*.pth")):
            return path
        raise FileNotFoundError(f"在 {model_dir} 中未找到 Fish Speech 解码器权重文件")


    def _load_audio_soundfile(reference_audio, sr):
        import numpy as np
        import soundfile as sf

        target_sr = int(sr)
        if isinstance(reference_audio, (bytes, bytearray, memoryview)):
            source = io.BytesIO(bytes(reference_audio))
        elif isinstance(reference_audio, (str, Path, os.PathLike)):
            source = str(reference_audio)
        elif hasattr(reference_audio, "read"):
            source = reference_audio
        else:
            source = io.BytesIO(bytes(reference_audio))

        audio, orig_sr = sf.read(source, dtype="float32", always_2d=False)
        arr = np.asarray(audio, dtype=np.float32)
        if arr.ndim > 1:
            arr = arr.mean(axis=1, dtype=np.float32)
        arr = arr.reshape(-1)
        orig_sr = int(orig_sr)

        if orig_sr > 0 and target_sr > 0 and orig_sr != target_sr and arr.size > 0:
            resampled = None
            try:
                from scipy import signal as sp_signal

                divisor = math.gcd(orig_sr, target_sr)
                up = target_sr // divisor
                down = orig_sr // divisor
                if max(up, down) <= 1000:
                    resampled = np.asarray(
                        sp_signal.resample_poly(arr, up, down), dtype=np.float32
                    ).reshape(-1)
            except ImportError:
                pass
            if resampled is None:
                try:
                    import torch
                    import torchaudio.functional as ta_F

                    tensor = torch.from_numpy(arr.copy()).unsqueeze(0)
                    resampled = (
                        ta_F.resample(tensor, orig_sr, target_sr)
                        .squeeze(0)
                        .cpu()
                        .numpy()
                        .astype(np.float32)
                        .reshape(-1)
                    )
                except Exception:
                    out_len = max(1, int(round(arr.size * target_sr / orig_sr)))
                    positions = np.clip(
                        np.arange(out_len, dtype=np.float64) * (orig_sr / target_sr),
                        0.0,
                        float(arr.size - 1),
                    )
                    resampled = np.interp(
                        positions,
                        np.arange(arr.size, dtype=np.float64),
                        arr.astype(np.float64),
                    ).astype(np.float32)
            arr = resampled

        return np.asarray(arr, dtype=np.float32).reshape(-1)


    def main() -> None:
        parser = argparse.ArgumentParser(description="ColabOne Fish Speech S2 Pro remote worker")
        parser.add_argument("--config", required=True)
        args = parser.parse_args()

        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        token = _hf_token()
        source_dir = (cfg.get("source_dir") or "").strip()
        if source_dir and source_dir not in sys.path:
            sys.path.insert(0, source_dir)

        import torch
        from huggingface_hub import snapshot_download
        from fish_speech.inference_engine import TTSInferenceEngine
        from fish_speech.models.dac.inference import load_model as load_decoder_model
        from fish_speech.models.text2semantic.inference import launch_thread_safe_queue
        from fish_speech.utils.schema import ServeReferenceAudio, ServeTTSRequest

        repo = cfg["repo"]
        revision = cfg.get("revision") or ""
        cache_dir = cfg.get("cache_dir") or ""
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        ref_text = (cfg.get("ref_text") or "").strip()
        emotion_prompt = (cfg.get("emotion_prompt") or "").strip()
        segments = cfg.get("segments") or []

        use_cuda = torch.cuda.is_available()
        device = "cuda" if use_cuda else "cpu"
        dtype = (
            torch.bfloat16
            if (use_cuda and torch.cuda.is_bf16_supported())
            else (torch.float16 if use_cuda else torch.float32)
        )
        dtype_label = (
            "bf16"
            if dtype == torch.bfloat16
            else ("fp16" if dtype == torch.float16 else "fp32")
        )
        vram_mib = (
            int(torch.cuda.get_device_properties(0).total_memory // (1024 * 1024))
            if use_cuda
            else 0
        )

        print(
            f"[colabone-tts] fish_s2_pro repo={repo} dtype={dtype_label} vram_mib={vram_mib}",
            flush=True,
        )

        local_dir = Path(
            snapshot_download(
                repo_id=repo,
                revision=revision or None,
                cache_dir=cache_dir or None,
                token=token,
            )
        )
        decoder_ckpt = _find_decoder_checkpoint(local_dir)

        llama_sig = inspect.signature(launch_thread_safe_queue)
        llama_kwargs = {
            "checkpoint_path": str(local_dir),
            "device": device,
            "precision": dtype,
            "compile": False,
        }
        llama_kwargs = {k: v for k, v in llama_kwargs.items() if k in llama_sig.parameters}
        llama_queue = launch_thread_safe_queue(**llama_kwargs)

        dec_sig = inspect.signature(load_decoder_model)
        dec_kwargs = {
            "config_name": "modded_dac_vq",
            "checkpoint_path": str(decoder_ckpt),
            "device": device,
        }
        dec_kwargs = {k: v for k, v in dec_kwargs.items() if k in dec_sig.parameters}
        decoder_model = load_decoder_model(**dec_kwargs)

        engine = TTSInferenceEngine(
            llama_queue=llama_queue,
            decoder_model=decoder_model,
            precision=dtype,
            compile=False,
        )
        # Bypass torchaudio.load (torchcodec on PyTorch >= 2.9) using soundfile + scipy/torchaudio.functional.
        engine.load_audio = _load_audio_soundfile

        references = []
        if ref_audio_path:
            references.append(
                ServeReferenceAudio(
                    audio=Path(ref_audio_path).read_bytes(),
                    text=ref_text,
                )
            )

        try:
            req_sig = inspect.signature(ServeTTSRequest)
            req_params = req_sig.parameters
            req_has_var_kw = any(
                p.kind == inspect.Parameter.VAR_KEYWORD for p in req_params.values()
            )
        except (TypeError, ValueError):
            req_params = None
            req_has_var_kw = True

        total = len(segments)
        default_sr = int(cfg.get("sample_rate") or 44100)
        for idx, seg in enumerate(segments):
            # Preserve inline emotion tags like [laugh] / [whisper] and prepend emotion_prompt if set.
            seg_text = _apply_emotion_tag(str(seg["text"]), emotion_prompt)
            out_wav = str(seg["output_wav"])
            req_kwargs = {
                "text": seg_text,
                "references": references,
                "format": "wav",
                "streaming": False,
            }
            if references:
                req_kwargs["use_memory_cache"] = "on"
            if req_params is not None and not req_has_var_kw:
                req_kwargs = {k: v for k, v in req_kwargs.items() if k in req_params}
            req = ServeTTSRequest(**req_kwargs)
            sample_rate = default_sr
            final_audio = None
            for result in engine.inference(req):
                if getattr(result, "code", "") == "error":
                    raise RuntimeError(f"Fish Speech S2 Pro 推理失败：{getattr(result, 'error', result)}")
                if getattr(result, "code", "") == "final" and getattr(result, "audio", None) is not None:
                    sample_rate, final_audio = result.audio
            if final_audio is None:
                raise RuntimeError(f"Fish Speech S2 Pro 未返回第 {idx + 1} 段音频")
            _write_pcm16_wav(out_wav, final_audio, int(sample_rate or default_sr))
            if idx == 0 and not references and total > 1:
                references = [
                    ServeReferenceAudio(
                        audio=Path(out_wav).read_bytes(),
                        text=seg_text,
                    )
                ]
                print(
                    f"[colabone-tts] fish_s2_pro locked speaker timbre from segment 1: {out_wav}",
                    flush=True,
                )
            print(f"[colabone-tts] fish_s2_pro segment {idx + 1}/{total} done", flush=True)


    if __name__ == "__main__":
        main()
    """
)

_INDEXTTS2_WORKER_BODY = textwrap.dedent(
    """\

    def _colabone_save_pcm_wav(*args, **kwargs) -> None:
        out_path = (
            kwargs.get("output_path")
            or kwargs.get("path")
            or kwargs.get("filepath")
            or kwargs.get("uri")
        )
        audio = kwargs.get("wav")
        if audio is None:
            audio = kwargs.get("audio")
        if audio is None:
            audio = kwargs.get("waveform")
        if audio is None:
            audio = kwargs.get("src")
        sr = (
            kwargs.get("sampling_rate")
            or kwargs.get("sample_rate")
            or kwargs.get("sr")
        )
        remaining = list(args)
        if out_path is None:
            for idx, val in enumerate(remaining):
                if isinstance(val, (str, Path, os.PathLike)):
                    out_path = val
                    remaining.pop(idx)
                    break
        if sr is None:
            for idx in range(len(remaining) - 1, -1, -1):
                val = remaining[idx]
                if isinstance(val, (int, float)) and not isinstance(val, bool):
                    sr = int(val)
                    remaining.pop(idx)
                    break
        if audio is None and remaining:
            audio = remaining[0]
        if out_path is None or audio is None:
            raise ValueError("save_pcm_wav 缺少输出路径或音频数据")
        _write_pcm16_wav(str(out_path), audio, int(sr or 22050))


    def _patch_indextts_audio_save() -> None:
        try:
            import indextts.utils.common as _indextts_common

            _indextts_common.save_pcm_wav = _colabone_save_pcm_wav
        except Exception:
            pass
        for _mod_name in ("indextts.infer_v2", "indextts.infer"):
            _mod = sys.modules.get(_mod_name)
            if _mod is not None and hasattr(_mod, "save_pcm_wav"):
                setattr(_mod, "save_pcm_wav", _colabone_save_pcm_wav)
        try:
            import torchaudio as _ta

            _orig_ta_save = getattr(_ta, "save", None)
            if callable(_orig_ta_save) and not getattr(_orig_ta_save, "_colabone_patched", False):
                def _safe_ta_save(uri, src, sample_rate, *args, **kwargs):
                    if isinstance(uri, (str, Path, os.PathLike)):
                        _write_pcm16_wav(str(uri), src, int(sample_rate))
                        return None
                    return _orig_ta_save(uri, src, sample_rate, *args, **kwargs)

                _safe_ta_save._colabone_patched = True  # type: ignore[attr-defined]
                _ta.save = _safe_ta_save
        except Exception:
            pass


    def main() -> None:
        parser = argparse.ArgumentParser(description="ColabOne IndexTTS2 remote worker")
        parser.add_argument("--config", required=True)
        args = parser.parse_args()

        cfg = json.loads(Path(args.config).read_text(encoding="utf-8"))
        token = _hf_token()
        source_dir = (cfg.get("source_dir") or "").strip()
        if source_dir and source_dir not in sys.path:
            sys.path.insert(0, source_dir)

        import torch
        from huggingface_hub import snapshot_download

        _patch_indextts_audio_save()
        try:
            from indextts.infer_v2 import IndexTTS2 as IndexTTSClass
        except ImportError:
            from indextts.infer import IndexTTS as IndexTTSClass
        _patch_indextts_audio_save()

        repo = cfg["repo"]
        revision = cfg.get("revision") or ""
        cache_dir = cfg.get("cache_dir") or ""
        ref_audio_path = (cfg.get("ref_audio_path") or "").strip()
        emotion_ref_path = (cfg.get("emotion_ref_path") or "").strip()
        emotion_prompt = (cfg.get("emotion_prompt") or "").strip()
        segments = cfg.get("segments") or []

        if not ref_audio_path:
            raise ValueError("IndexTTS2 需要提供音色参考音频（ref_audio_path）")

        model_dir = Path(
            snapshot_download(
                repo_id=repo,
                revision=revision or None,
                cache_dir=cache_dir or None,
                token=token,
            )
        )
        cfg_path = model_dir / "config.yaml"
        if not cfg_path.is_file():
            raise FileNotFoundError(f"在 {model_dir} 中未找到 IndexTTS2 配置文件 config.yaml")

        init_sig = inspect.signature(IndexTTSClass.__init__)
        init_kwargs = {
            "cfg_path": str(cfg_path),
            "model_dir": str(model_dir),
            "use_fp16": torch.cuda.is_available(),
            "use_cuda_kernel": False,
            "use_deepspeed": False,
            "use_qwen_emo": bool(emotion_prompt),
        }
        init_kwargs = {k: v for k, v in init_kwargs.items() if k in init_sig.parameters}
        tts = IndexTTSClass(**init_kwargs)

        infer_sig = inspect.signature(tts.infer)
        infer_params = infer_sig.parameters
        has_var_kw = any(
            p.kind == inspect.Parameter.VAR_KEYWORD for p in infer_params.values()
        )
        default_sr = int(cfg.get("sample_rate") or 24000)

        total = len(segments)
        for idx, seg in enumerate(segments):
            seg_text = str(seg["text"])
            out_wav = str(seg["output_wav"])
            kwargs = {
                "spk_audio_prompt": ref_audio_path,
                "text": seg_text,
                "output_path": out_wav,
            }
            if "audio_prompt" in infer_params and "spk_audio_prompt" not in infer_params:
                kwargs["audio_prompt"] = ref_audio_path
                kwargs.pop("spk_audio_prompt", None)
            if emotion_ref_path:
                kwargs["emo_audio_prompt"] = emotion_ref_path
                if "use_emo_text" in infer_params:
                    kwargs["use_emo_text"] = False
            elif emotion_prompt:
                kwargs["emo_text"] = emotion_prompt
                kwargs["use_emo_text"] = True
            if not has_var_kw:
                kwargs = {k: v for k, v in kwargs.items() if k in infer_params}

            result = tts.infer(**kwargs)
            if not Path(out_wav).is_file():
                if isinstance(result, tuple) and len(result) == 2:
                    sr, wav = result
                    _write_pcm16_wav(out_wav, wav, int(sr or default_sr))
                elif result is not None:
                    _write_pcm16_wav(out_wav, result, default_sr)
                else:
                    raise RuntimeError(f"IndexTTS2 未生成第 {idx + 1} 段音频：{out_wav}")
            print(f"[colabone-tts] indextts2 segment {idx + 1}/{total} done", flush=True)


    if __name__ == "__main__":
        main()
    """
)

_WORKER_BODIES: dict[str, str] = {
    "voxcpm2": _VOXCPM2_WORKER_BODY,
    "qwen3_tts": _QWEN3_TTS_WORKER_BODY,
    "fish_s2_pro": _FISH_S2_PRO_WORKER_BODY,
    "indextts2": _INDEXTTS2_WORKER_BODY,
}


# ---------------------------------------------------------------------------
# 5. Remote GPU Multi-Engine TTS Runner
# ---------------------------------------------------------------------------


class ColabOneRemoteTtsRunner:
    """Manages remote GPU TTS engine environments and isolated subprocess synthesis."""

    def __init__(
        self,
        runtime: Any,
        engines_by_id: dict[str, Any],
        run_cmd_fn: Callable[..., Any] | None = None,
        synth_fn: Callable[..., Any] | None = None,
    ) -> None:
        self.runtime = runtime
        self.run_cmd_fn = run_cmd_fn
        self.synth_fn = synth_fn
        self.engines_dir = Path(runtime.root) / "tts-engines"
        self._lock = threading.RLock()

        normalized: dict[str, dict[str, Any]] = {}
        for eid, spec in (engines_by_id or {}).items():
            raw = (
                spec.model_dump()
                if hasattr(spec, "model_dump")
                else copy.deepcopy(dict(spec))
            )
            key = str(raw.get("id") or eid).strip()
            raw["id"] = key
            normalized[key] = raw
        self.engines_by_id: dict[str, dict[str, Any]] = normalized

    def list_engines(self) -> list[dict[str, Any]]:
        return [copy.deepcopy(spec) for spec in self.engines_by_id.values()]

    def get_engine(self, engine_id: str) -> dict[str, Any]:
        key = str(engine_id or "").strip()
        if key not in self.engines_by_id:
            raise ValueError(f"未知的远端 TTS 引擎：{engine_id!r}")
        return copy.deepcopy(self.engines_by_id[key])

    def check_vram(
        self, engine: dict[str, Any], *, allow_unavailable: bool = False
    ) -> None:
        min_vram_mib = int(engine.get("min_vram_mib") or 0)
        if min_vram_mib <= 0:
            return
        label = str(engine.get("name") or engine.get("id") or "TTS 引擎")
        gpu_fn = getattr(self.runtime, "gpu_info", None)
        if not callable(gpu_fn):
            if allow_unavailable:
                return
            raise RuntimeError(
                f"未检测到可用远端 GPU，{label} 需要至少 {min_vram_mib} MiB 显存"
            )
        gpu = gpu_fn() or {}
        total_mib = int(gpu.get("total_mib") or 0)
        if total_mib <= 0:
            if allow_unavailable:
                return
            raise RuntimeError(
                f"未检测到可用远端 GPU，{label} 需要至少 {min_vram_mib} MiB 显存"
            )
        if total_mib < min_vram_mib:
            raise RuntimeError(
                f"远端 TTS 引擎 {label} 需要至少 {min_vram_mib} MiB 显存，当前仅 {total_mib} MiB"
            )

    @staticmethod
    def _needs_source_checkout(engine: dict[str, Any]) -> bool:
        source_repo = str(engine.get("source_repo") or "").strip()
        source_commit = str(engine.get("source_commit") or "").strip()
        if not source_repo or not source_commit:
            return False
        engine_id = str(engine.get("id") or "").strip()
        if engine_id in _SOURCE_CHECKOUT_ENGINES:
            return True
        if bool(engine.get("require_source_checkout")):
            return True
        return engine_id not in _PIP_PACKAGED_ENGINES

    def _invoke_command(
        self,
        args: Sequence[str],
        task: Any,
        log: Path,
        *,
        timeout: int = 1800,
        cwd: Path | str | None = None,
        env: dict[str, str] | None = None,
        secret_to_scrub: str = "",
        append_log: bool = False,
    ) -> None:
        stopping = getattr(self.runtime, "stopping", None)
        if self.run_cmd_fn is not None:
            _call_with_compatible_kwargs(
                self.run_cmd_fn,
                (list(args),),
                {
                    "task": task,
                    "log": log,
                    "timeout": timeout,
                    "cwd": cwd,
                    "env": env,
                    "stopping": stopping,
                },
            )
            return
        _run_tts_subprocess(
            args,
            task,
            log,
            timeout=timeout,
            cwd=cwd,
            env=env,
            stopping=stopping,
            secret_to_scrub=secret_to_scrub,
            append_log=append_log,
        )

    def ensure_engine_env(
        self, task: Any, engine: dict[str, Any], hf_token: str = ""
    ) -> Path:
        """Create isolated per-engine venv, clone pinned source when needed, and install deps in one pass."""
        engine_id = str(engine.get("id") or "").strip()
        if not _ENGINE_ID_RE.fullmatch(engine_id):
            raise ValueError(f"非法的 TTS 引擎编号：{engine_id!r}")
        engine_name = str(engine.get("name") or engine_id)

        filtered_packages = _filter_pip_packages(engine.get("pip_packages") or [])
        needs_source = self._needs_source_checkout(engine)
        source_repo = str(engine.get("source_repo") or "").strip()
        source_commit = str(engine.get("source_commit") or "").strip()
        repo_url = ""
        if needs_source:
            if not _GIT_COMMIT_SHA_RE.fullmatch(source_commit):
                raise ValueError(
                    f"TTS 引擎源码提交哈希必须为完整 40 位十六进制 SHA：{source_commit!r}"
                )
            repo_url = _normalize_git_repo_url(source_repo)
        elif source_commit and not _GIT_COMMIT_SHA_RE.fullmatch(source_commit):
            raise ValueError(
                f"TTS 引擎源码提交哈希必须为完整 40 位十六进制 SHA：{source_commit!r}"
            )

        if self.engines_dir.is_symlink():
            raise ValueError("TTS 引擎目录不能是符号链接")
        self.engines_dir.mkdir(parents=True, exist_ok=True)
        env_dir = self.engines_dir / engine_id
        if env_dir.is_symlink():
            raise ValueError("TTS 引擎子目录不能是符号链接")
        env_dir.mkdir(parents=True, exist_ok=True)

        marker_path = env_dir / ".ready.json"
        venv_dir = env_dir / ".venv"
        if venv_dir.is_symlink():
            raise ValueError("TTS 引擎虚拟环境目录不能是符号链接")
        source_dir = env_dir / "source"
        torch_constraints = _collect_installed_torch_constraints()

        expected_fingerprint = {
            "engine_id": engine_id,
            "repo": str(engine.get("repo") or ""),
            "revision": str(engine.get("revision") or ""),
            "aux_repo": str(engine.get("aux_repo") or ""),
            "aux_revision": str(engine.get("aux_revision") or ""),
            "source_repo": source_repo,
            "source_commit": source_commit,
            "pip_packages": filtered_packages,
            "needs_source_checkout": needs_source,
            "venv_mode": "system-site-packages",
            "torch_constraints": torch_constraints,
        }

        with self._lock:
            venv_present = (
                (
                    (venv_dir / "pyvenv.cfg").is_file()
                    and _find_venv_python_on_disk(venv_dir) is not None
                )
                or self.run_cmd_fn is not None
            )
            if marker_path.is_file() and not marker_path.is_symlink():
                try:
                    cached = json.loads(marker_path.read_text(encoding="utf-8"))
                    if (
                        isinstance(cached, dict)
                        and all(
                            cached.get(k) == v for k, v in expected_fingerprint.items()
                        )
                        and venv_present
                        and (not needs_source or source_dir.is_dir())
                    ):
                        _safe_task_note(
                            task, f"复用已就绪的远端 TTS 引擎环境：{engine_name}"
                        )
                        return env_dir
                except (OSError, ValueError):
                    pass

            log_path = env_dir / "setup.log"
            if log_path.is_symlink():
                raise ValueError("TTS 安装日志路径不能是符号链接")
            # Strictly isolate HF credentials from venv/pip/git subprocesses.
            sub_env = dict(os.environ)
            sub_env.pop("HF_TOKEN", None)
            sub_env.pop("HUGGING_FACE_HUB_TOKEN", None)

            has_venv_files = (
                (venv_dir / "pyvenv.cfg").is_file()
                and _find_venv_python_on_disk(venv_dir) is not None
            )
            if not has_venv_files:
                if venv_dir.exists():
                    shutil.rmtree(venv_dir, ignore_errors=True)
                msg = f"正在为远端 TTS 引擎创建隔离虚拟环境：{engine_name}"
                _safe_task_update(task, message=msg)
                _safe_task_note(task, msg)
                try:
                    self._invoke_command(
                        [
                            sys.executable,
                            "-m",
                            "venv",
                            "--without-pip",
                            "--system-site-packages",
                            str(venv_dir),
                        ],
                        task,
                        log_path,
                        timeout=300,
                        env=sub_env,
                        secret_to_scrub=hf_token,
                        append_log=True,
                    )
                    venv_python_on_disk = _find_venv_python_on_disk(venv_dir)
                    if venv_python_on_disk is not None:
                        self._invoke_command(
                            [str(venv_python_on_disk), "-m", "pip", "--version"],
                            task,
                            log_path,
                            timeout=120,
                            env=sub_env,
                            secret_to_scrub=hf_token,
                            append_log=True,
                        )
                    elif self.run_cmd_fn is None:
                        raise RuntimeError(
                            f"远端 TTS 引擎虚拟环境创建不完整，缺少 Python 可执行文件：{venv_dir}"
                        )
                except BaseException:
                    shutil.rmtree(venv_dir, ignore_errors=True)
                    raise

            venv_python = str(_resolve_venv_python(venv_dir))
            constraints_path = _write_torch_constraints_file(
                env_dir / ".colabone-torch-constraints.txt",
                torch_constraints,
            )

            source_deps: list[str] = []
            if needs_source:
                if self.run_cmd_fn is None and not shutil.which("git"):
                    raise RuntimeError(
                        f"远端缺少 Git，无法拉取 TTS 引擎源码：{engine_name}"
                    )
                if source_dir.is_symlink():
                    raise ValueError("TTS 源码目录不能是符号链接")
                git_dir = source_dir / ".git"
                if git_dir.exists() and not git_dir.is_dir():
                    shutil.rmtree(source_dir, ignore_errors=True)
                source_dir.mkdir(parents=True, exist_ok=True)
                msg = f"正在拉取远端 TTS 引擎源码：{engine_name}@{source_commit[:10]}"
                _safe_task_update(task, message=msg)
                _safe_task_note(task, msg)
                if git_dir.is_dir():
                    for lock_file in git_dir.rglob("*.lock"):
                        try:
                            lock_file.unlink(missing_ok=True)
                        except OSError:
                            pass
                    try:
                        self._invoke_command(
                            [
                                "git",
                                "-C",
                                str(source_dir),
                                "remote",
                                "set-url",
                                "--",
                                "origin",
                                repo_url,
                            ],
                            task,
                            log_path,
                            timeout=300,
                            env=sub_env,
                            secret_to_scrub=hf_token,
                            append_log=True,
                        )
                    except RuntimeError:
                        self._invoke_command(
                            [
                                "git",
                                "-C",
                                str(source_dir),
                                "remote",
                                "add",
                                "--",
                                "origin",
                                repo_url,
                            ],
                            task,
                            log_path,
                            timeout=300,
                            env=sub_env,
                            secret_to_scrub=hf_token,
                            append_log=True,
                        )
                else:
                    self._invoke_command(
                        ["git", "init", "--", str(source_dir)],
                        task,
                        log_path,
                        timeout=300,
                        env=sub_env,
                        secret_to_scrub=hf_token,
                        append_log=True,
                    )
                    self._invoke_command(
                        [
                            "git",
                            "-C",
                            str(source_dir),
                            "remote",
                            "add",
                            "--",
                            "origin",
                            repo_url,
                        ],
                        task,
                        log_path,
                        timeout=300,
                        env=sub_env,
                        secret_to_scrub=hf_token,
                        append_log=True,
                    )

                self._invoke_command(
                    [
                        "git",
                        "-C",
                        str(source_dir),
                        "fetch",
                        "--depth",
                        "1",
                        "--",
                        "origin",
                        source_commit,
                    ],
                    task,
                    log_path,
                    timeout=600,
                    env=sub_env,
                    secret_to_scrub=hf_token,
                    append_log=True,
                )
                self._invoke_command(
                    [
                        "git",
                        "-C",
                        str(source_dir),
                        "checkout",
                        "--force",
                        "--detach",
                        source_commit,
                    ],
                    task,
                    log_path,
                    timeout=300,
                    env=sub_env,
                    secret_to_scrub=hf_token,
                    append_log=True,
                )
                self._invoke_command(
                    [
                        "git",
                        "-C",
                        str(source_dir),
                        "clean",
                        "-fdx",
                    ],
                    task,
                    log_path,
                    timeout=300,
                    env=sub_env,
                    secret_to_scrub=hf_token,
                    append_log=True,
                )

                seen_deps: set[str] = set()
                req_file = source_dir / "requirements.txt"
                if req_file.is_file() and not req_file.is_symlink():
                    for spec in _extract_requirements_file_packages(req_file):
                        if spec not in seen_deps:
                            seen_deps.add(spec)
                            source_deps.append(spec)
                pyproject_file = source_dir / "pyproject.toml"
                if pyproject_file.is_file() and not pyproject_file.is_symlink():
                    for spec in _extract_pyproject_dependencies(pyproject_file):
                        if spec not in seen_deps:
                            seen_deps.add(spec)
                            source_deps.append(spec)

            merged_packages: list[str] = []
            seen_merged: set[str] = set()
            for spec in (*filtered_packages, *source_deps):
                if spec not in seen_merged:
                    seen_merged.add(spec)
                    merged_packages.append(spec)

            if merged_packages:
                merged_req = env_dir / ".colabone-requirements.txt"
                if merged_req.is_symlink():
                    raise ValueError("TTS 依赖清单文件不能是符号链接")
                merged_req.write_text(
                    "\n".join(merged_packages) + "\n", encoding="utf-8"
                )
                msg = (
                    f"正在安装远端 TTS 引擎依赖：{engine_name}"
                    f"（共 {len(merged_packages)} 个包，保留现有 PyTorch）"
                )
                _safe_task_update(task, message=msg)
                _safe_task_note(task, msg)
                self._invoke_command(
                    [
                        venv_python,
                        "-m",
                        "pip",
                        "install",
                        "--no-input",
                        "--disable-pip-version-check",
                        "-c",
                        str(constraints_path),
                        "-r",
                        str(merged_req),
                    ],
                    task,
                    log_path,
                    timeout=1200,
                    env=sub_env,
                    secret_to_scrub=hf_token,
                    append_log=True,
                )

            marker_payload = dict(expected_fingerprint)
            marker_payload["ready_at"] = time.time()
            atomic_json(marker_path, marker_payload)
            _safe_task_note(task, f"远端 TTS 引擎环境就绪：{engine_name}")
            return env_dir

    def _prepare_ref_audio_wav(
        self,
        task: Any,
        src_path: Path,
        work_root: Path,
        role: str,
        target_sr: int = 24000,
    ) -> Path:
        """Ensure reference audio is standard mono 16-bit PCM WAV; transcode via ffmpeg when needed."""
        if src_path.suffix.lower() == ".wav" and _is_standard_mono_pcm16_wav(src_path):
            return src_path
        if self.synth_fn is not None and self.run_cmd_fn is None:
            return src_path
        sr = max(24000, int(target_sr or 24000))
        dst_path = work_root / f"ref_{role}.wav"
        if dst_path.is_symlink():
            raise ValueError("参考音频转码目标不能是符号链接")
        if self.run_cmd_fn is None and not shutil.which("ffmpeg"):
            raise RuntimeError(
                f"远端缺少 ffmpeg，无法将参考音频转换为标准 WAV 格式：{src_path.name}"
            )
        ffmpeg_env = dict(os.environ)
        ffmpeg_env.pop("HF_TOKEN", None)
        ffmpeg_env.pop("HUGGING_FACE_HUB_TOKEN", None)
        _safe_task_note(
            task,
            f"正在将参考音频转码为标准 {sr} Hz 单声道 PCM16 WAV：{src_path.name}",
        )
        self._invoke_command(
            [
                "ffmpeg",
                "-y",
                "-v",
                "error",
                "-protocol_whitelist",
                "file,pipe",
                "-i",
                str(src_path),
                "-ac",
                "1",
                "-ar",
                str(sr),
                "-c:a",
                "pcm_s16le",
                str(dst_path),
            ],
            task,
            work_root / f"transcode_{role}.log",
            timeout=300,
            cwd=work_root,
            env=ffmpeg_env,
        )
        if self.run_cmd_fn is None and (
            not dst_path.is_file()
            or dst_path.stat().st_size <= 0
            or not _is_standard_mono_pcm16_wav(dst_path)
        ):
            raise RuntimeError(
                f"参考音频转码失败，未生成有效的单声道 PCM16 WAV：{dst_path}"
            )
        return dst_path

    def build_worker_script(self, engine_id: str) -> str:
        """Generate a self-contained Python worker script for one of the 4 TTS engines."""
        key = str(engine_id or "").strip()
        body = _WORKER_BODIES.get(key)
        if body is None:
            raise ValueError(f"不支持的 TTS 引擎脚本：{engine_id!r}")
        return _WORKER_COMMON_HEADER + body

    def _ensure_preset_reference_audio(
        self,
        task: Any,
        preset: dict[str, Any],
        *,
        hf_token: str = "",
    ) -> tuple[Path, str]:
        """Return a cached 48 kHz studio female WAV anchor & transcript, synthesizing once via VoxCPM2."""
        preset_id = str(preset.get("id") or "").strip()
        if not _VOICE_PRESET_ID_RE.fullmatch(preset_id):
            raise ValueError(f"非法的内置音色预设编号：{preset_id!r}")
        preset_name = str(preset.get("name") or preset_id).strip()
        anchor_text = COLABONE_VOICE_PRESET_ANCHOR_TEXT

        if self.engines_dir.is_symlink():
            raise ValueError("TTS 引擎目录不能是符号链接")
        self.engines_dir.mkdir(parents=True, exist_ok=True)
        presets_dir = self.engines_dir / "_voice_presets"
        if presets_dir.is_symlink():
            raise ValueError("内置女声缓存目录不能是符号链接")
        presets_dir.mkdir(parents=True, exist_ok=True)

        wav_path = presets_dir / f"{preset_id}.wav"
        txt_path = presets_dir / f"{preset_id}.txt"
        if wav_path.is_symlink() or txt_path.is_symlink():
            raise ValueError("内置女声缓存文件不能是符号链接")

        with self._lock:
            if (
                wav_path.is_file()
                and not wav_path.is_symlink()
                and wav_path.stat().st_size > 0
                and txt_path.is_file()
                and not txt_path.is_symlink()
            ):
                cached_text = txt_path.read_text(encoding="utf-8").strip() or anchor_text
                _safe_task_note(task, f"复用已缓存的内置女声参考样本：{preset_name}")
                return wav_path, cached_text

            check_fn = getattr(task, "check", None)
            if callable(check_fn):
                check_fn()

            if "voxcpm2" not in self.engines_by_id:
                raise RuntimeError(
                    f"缺少 VoxCPM2 引擎配置，无法生成内置女声参考样本：{preset_name}"
                )
            vox_engine = self.get_engine("voxcpm2")
            self.check_vram(
                vox_engine,
                allow_unavailable=(self.synth_fn is not None or self.run_cmd_fn is not None),
            )

            gen_msg = f"正在生成并缓存内置女声参考样本：{preset_name}"
            _safe_task_update(task, message=gen_msg)
            _safe_task_note(task, gen_msg)

            canonical = COLABONE_TTS_VOICE_PRESETS.get(preset_id) or {}
            vox_prompt = str(
                preset.get("voxcpm_prompt")
                or canonical.get("voxcpm_prompt")
                or preset.get("prompt")
                or COLABONE_TTS_VOICE_PRESETS[DEFAULT_COLABONE_VOICE_PRESET]["voxcpm_prompt"]
            ).strip()
            raw_seed = preset.get("seed", canonical.get("seed", 42))
            preset_seed = (
                int(raw_seed)
                if isinstance(raw_seed, int)
                and not isinstance(raw_seed, bool)
                and raw_seed >= 0
                else 42
            )

            tmp_work = Path(
                tempfile.mkdtemp(prefix=f".preset-{preset_id}-", dir=str(presets_dir))
            )
            tmp_wav_target: Path | None = None
            try:
                raw_anchor_wav = tmp_work / "anchor_raw.wav"
                seg_entries = [
                    {
                        "index": 0,
                        "text": anchor_text,
                        "output_wav": str(raw_anchor_wav),
                    }
                ]
                anchor_config: dict[str, Any] = {
                    "engine_id": "voxcpm2",
                    "repo": str(vox_engine.get("repo") or ""),
                    "revision": str(vox_engine.get("revision") or ""),
                    "aux_repo": str(vox_engine.get("aux_repo") or ""),
                    "aux_revision": str(vox_engine.get("aux_revision") or ""),
                    "source_dir": "",
                    "cache_dir": str(self.engines_dir / "_hf_cache"),
                    "sample_rate": int(vox_engine.get("sample_rate") or 48000),
                    "segments": seg_entries,
                    "ref_audio_path": "",
                    "ref_text": "",
                    "voice_prompt": vox_prompt,
                    "voice_preset": preset_id,
                    "voice_seed": preset_seed,
                    "emotion_ref_path": "",
                    "emotion_prompt": "",
                }

                generated_wav = raw_anchor_wav
                if self.synth_fn is not None:
                    segment_wavs = self._invoke_synth_fn(
                        task=task,
                        engine=vox_engine,
                        segments=[anchor_text],
                        segments_dir=tmp_work,
                        seg_entries=seg_entries,
                        config=anchor_config,
                        ref_audio_path=None,
                        ref_text="",
                        voice_prompt=vox_prompt,
                        voice_preset=preset_id,
                        voice_seed=preset_seed,
                        emotion_ref_path=None,
                        emotion_prompt="",
                        hf_token=hf_token,
                    )
                    if segment_wavs:
                        generated_wav = Path(segment_wavs[0][1])
                else:
                    env_dir = self.ensure_engine_env(task, vox_engine, hf_token="")
                    worker_python = str(_resolve_venv_python(env_dir / ".venv"))
                    source_dir = env_dir / "source"
                    if source_dir.is_dir():
                        anchor_config["source_dir"] = str(source_dir)

                    worker_path = tmp_work / "worker_voxcpm2.py"
                    config_path = tmp_work / "config_voxcpm2.json"
                    log_path = tmp_work / "preset_voxcpm2.log"
                    if (
                        worker_path.is_symlink()
                        or config_path.is_symlink()
                        or log_path.is_symlink()
                    ):
                        raise ValueError("内置女声生成临时路径不能是符号链接")

                    worker_path.write_text(
                        self.build_worker_script("voxcpm2"), encoding="utf-8"
                    )
                    atomic_json(config_path, anchor_config)

                    worker_env = dict(os.environ)
                    worker_env["PYTHONUNBUFFERED"] = "1"
                    if hf_token:
                        worker_env["HF_TOKEN"] = hf_token
                        worker_env["HUGGING_FACE_HUB_TOKEN"] = hf_token
                    else:
                        worker_env.pop("HF_TOKEN", None)
                        worker_env.pop("HUGGING_FACE_HUB_TOKEN", None)

                    self._invoke_command(
                        [worker_python, str(worker_path), "--config", str(config_path)],
                        task,
                        log_path,
                        timeout=1800,
                        cwd=tmp_work,
                        env=worker_env,
                        secret_to_scrub=hf_token,
                    )

                if not generated_wav.is_file() or generated_wav.stat().st_size <= 0:
                    raise RuntimeError(f"内置女声参考样本生成失败：{preset_name}")

                fd, tmp_wav_str = tempfile.mkstemp(
                    prefix=f".{preset_id}-", suffix=".wav.tmp", dir=str(presets_dir)
                )
                os.close(fd)
                tmp_wav_target = Path(tmp_wav_str)

                try:
                    assemble_colabone_tts_master(
                        [(anchor_text, generated_wav)],
                        out_wav=tmp_wav_target,
                        manifest_path=tmp_work / "anchor_manifest.json",
                        pause_seconds=0.0,
                        target_sr=48000,
                        target_lufs=-23.0,
                    )
                except (wave.Error, EOFError, ValueError):
                    if self.run_cmd_fn is None:
                        raise
                    shutil.copyfile(generated_wav, tmp_wav_target)

                with open(tmp_wav_target, "r+b") as stream:
                    os.fsync(stream.fileno())
                os.replace(tmp_wav_target, wav_path)
                tmp_wav_target = None
                _atomic_write_text(txt_path, anchor_text + "\n")
                return wav_path, anchor_text
            finally:
                if tmp_wav_target is not None and tmp_wav_target.exists():
                    try:
                        tmp_wav_target.unlink()
                    except OSError:
                        pass
                shutil.rmtree(tmp_work, ignore_errors=True)

    def _invoke_synth_fn(
        self,
        *,
        task: Any,
        engine: dict[str, Any],
        segments: list[str],
        segments_dir: Path,
        seg_entries: list[dict[str, Any]],
        config: dict[str, Any],
        ref_audio_path: Path | None,
        ref_text: str,
        voice_prompt: str,
        emotion_ref_path: Path | None,
        emotion_prompt: str,
        hf_token: str,
        voice_preset: str = "",
        voice_seed: int | None = None,
    ) -> list[tuple[str, Path]]:
        assert self.synth_fn is not None
        fn = self.synth_fn
        result: Any

        try:
            sig = inspect.signature(fn)
            params = list(sig.parameters.values())
            first_name = params[0].name if params else ""
        except (TypeError, ValueError):
            params = []
            first_name = ""

        kw_pool: dict[str, Any] = {
            "task": task,
            "engine": copy.deepcopy(engine),
            "engine_id": engine["id"],
            "segments": list(segments),
            "segments_dir": segments_dir,
            "work_dir": segments_dir.parent,
            "ref_audio_path": ref_audio_path,
            "ref_text": ref_text,
            "voice_prompt": voice_prompt,
            "voice_preset": voice_preset,
            "voice_seed": voice_seed,
            "emotion_ref_path": emotion_ref_path,
            "emotion_prompt": emotion_prompt,
            "hf_token": hf_token,
            "config": copy.deepcopy(config),
        }

        if len(params) == 1 and first_name in ("config", "payload"):
            result = fn(copy.deepcopy(config))
        elif first_name == "task":
            result = _call_with_compatible_kwargs(
                fn,
                (task, copy.deepcopy(engine), list(segments), segments_dir),
                kw_pool,
            )
        else:
            result = _call_with_compatible_kwargs(
                fn,
                (copy.deepcopy(engine), list(segments), segments_dir),
                kw_pool,
            )

        if result is None:
            return [(str(s["text"]), Path(s["output_wav"])) for s in seg_entries]

        segment_wavs: list[tuple[str, Path]] = []
        if isinstance(result, (list, tuple)):
            for idx, item in enumerate(result):
                if isinstance(item, (list, tuple)) and len(item) == 2:
                    segment_wavs.append((str(item[0]), Path(item[1])))
                else:
                    seg_text = segments[idx] if idx < len(segments) else ""
                    segment_wavs.append((seg_text, Path(item)))
            return segment_wavs

        raise RuntimeError(f"synth_fn 返回了不支持的结果类型：{type(result).__name__}")

    def synthesize(
        self,
        task: Any,
        *,
        engine_id: str,
        script_text: str,
        work_dir: Path,
        ref_audio_path: Path | None = None,
        ref_text: str = "",
        voice_prompt: str = "",
        emotion_ref_path: Path | None = None,
        emotion_prompt: str = "",
        pause_seconds: float = 0.28,
        hf_token: str = "",
        voice_preset: str = "",
    ) -> dict[str, Any]:
        """Run remote GPU TTS synthesis in an isolated subprocess and assemble master WAV."""
        check_fn = getattr(task, "check", None)
        if callable(check_fn):
            check_fn()

        engine = self.get_engine(engine_id)
        engine_name = str(engine.get("name") or engine["id"])

        if not isinstance(script_text, str) or not script_text.strip():
            raise ValueError("配音脚本文本不能为空")
        segments = split_colabone_tts_script(script_text)
        if not segments:
            raise ValueError("配音脚本文本不能为空")

        resolved_ref_audio: Path | None = None
        if ref_audio_path is not None and str(ref_audio_path).strip():
            resolved_ref_audio = Path(ref_audio_path)
            if not resolved_ref_audio.is_file():
                raise ValueError(f"音色参考音频文件不存在：{resolved_ref_audio}")

        resolved_emo_audio: Path | None = None
        if emotion_ref_path is not None and str(emotion_ref_path).strip():
            resolved_emo_audio = Path(emotion_ref_path)
            if not resolved_emo_audio.is_file():
                raise ValueError(f"情绪参考音频文件不存在：{resolved_emo_audio}")

        raw_voice_preset = str(voice_preset or "").strip()
        user_voice_prompt = str(voice_prompt or "").strip()
        effective_ref_text = str(ref_text or "")
        effective_voice_prompt = str(voice_prompt or "")
        effective_voice_preset = ""
        effective_voice_seed: int | None = None
        resolved_preset: dict[str, Any] | None = None

        if resolved_ref_audio is None:
            resolved_preset = resolve_colabone_voice_preset(engine, raw_voice_preset)

        # Enforce IndexTTS2 reference audio requirement for custom/unknown voices
        # and legacy offline synth_fn invocations that omit voice_preset.
        if engine["id"] == "indextts2" and resolved_ref_audio is None:
            if (
                raw_voice_preset.lower() == "custom"
                or resolved_preset is None
                or (raw_voice_preset == "" and self.synth_fn is not None)
            ):
                raise ValueError("IndexTTS2 需要上传音色参考音频才能进行语音合成")

        if not engine.get("commercial_ok", True):
            lic = str(engine.get("license") or "Non-Commercial").strip()
            notice = str(engine.get("license_notice") or "").strip()
            msg = f"非商用许可提示：当前 TTS 引擎「{engine_name}」采用 {lic}"
            if notice:
                msg += f" — {notice}"
            _safe_task_note(task, msg, level="warning")

        self.check_vram(engine, allow_unavailable=(self.synth_fn is not None))

        work_root = Path(work_dir)
        if work_root.is_symlink():
            raise ValueError("TTS 工作目录不能是符号链接")
        work_root.mkdir(parents=True, exist_ok=True)
        segments_dir = work_root / "segments"
        if segments_dir.is_symlink():
            raise ValueError("TTS 片段目录不能是符号链接")
        segments_dir.mkdir(parents=True, exist_ok=True)

        if resolved_ref_audio is None and resolved_preset is not None:
            preset_id = str(resolved_preset["id"])
            preset_seed = int(resolved_preset["seed"])
            preset_prompt = str(
                resolved_preset.get("prompt")
                or (
                    resolved_preset.get("qwen_instruct")
                    if engine["id"] == "qwen3_tts"
                    else resolved_preset.get("voxcpm_prompt")
                )
                or ""
            ).strip()
            effective_voice_preset = preset_id
            effective_voice_seed = preset_seed

            if engine["id"] in ("voxcpm2", "qwen3_tts"):
                if not user_voice_prompt and preset_prompt:
                    effective_voice_prompt = preset_prompt
            elif engine["id"] in ("fish_s2_pro", "indextts2"):
                if not user_voice_prompt and preset_prompt:
                    effective_voice_prompt = preset_prompt
                presets_dir = self.engines_dir / "_voice_presets"
                cached_wav = presets_dir / f"{preset_id}.wav"
                cached_txt = presets_dir / f"{preset_id}.txt"
                has_symlink_preset = (
                    self.engines_dir.is_symlink()
                    or presets_dir.is_symlink()
                    or cached_wav.is_symlink()
                    or cached_txt.is_symlink()
                )
                has_cached_preset = (
                    not has_symlink_preset
                    and cached_wav.is_file()
                    and cached_wav.stat().st_size > 0
                    and cached_txt.is_file()
                )
                anchor_helper_overridden = (
                    getattr(self._ensure_preset_reference_audio, "__func__", None)
                    is not ColabOneRemoteTtsRunner._ensure_preset_reference_audio
                )
                if (
                    self.synth_fn is None
                    or has_cached_preset
                    or has_symlink_preset
                    or anchor_helper_overridden
                ):
                    anchor_wav, anchor_text = self._ensure_preset_reference_audio(
                        task,
                        resolved_preset,
                        hf_token=hf_token,
                    )
                    resolved_ref_audio = Path(anchor_wav)
                    if not effective_ref_text.strip():
                        effective_ref_text = str(
                            anchor_text or COLABONE_VOICE_PRESET_ANCHOR_TEXT
                        )

        ref_target_sr = max(24000, int(engine.get("sample_rate") or 24000))
        if resolved_ref_audio is not None:
            resolved_ref_audio = self._prepare_ref_audio_wav(
                task,
                resolved_ref_audio,
                work_root,
                "voice",
                target_sr=ref_target_sr,
            )
        if resolved_emo_audio is not None:
            resolved_emo_audio = self._prepare_ref_audio_wav(
                task,
                resolved_emo_audio,
                work_root,
                "emotion",
                target_sr=ref_target_sr,
            )

        seg_entries = [
            {
                "index": idx,
                "text": seg_text,
                "output_wav": str(segments_dir / f"seg_{idx:04d}.wav"),
            }
            for idx, seg_text in enumerate(segments)
        ]

        # Config written to disk strictly omits hf_token (passed ONLY via subprocess env).
        base_config: dict[str, Any] = {
            "engine_id": engine["id"],
            "repo": str(engine.get("repo") or ""),
            "revision": str(engine.get("revision") or ""),
            "aux_repo": str(engine.get("aux_repo") or ""),
            "aux_revision": str(engine.get("aux_revision") or ""),
            "source_dir": "",
            "cache_dir": str(self.engines_dir / "_hf_cache"),
            "sample_rate": int(engine.get("sample_rate") or 48000),
            "segments": seg_entries,
            "ref_audio_path": str(resolved_ref_audio) if resolved_ref_audio else "",
            "ref_text": effective_ref_text,
            "voice_prompt": effective_voice_prompt,
            "voice_preset": effective_voice_preset,
            "voice_seed": effective_voice_seed,
            "emotion_ref_path": str(resolved_emo_audio) if resolved_emo_audio else "",
            "emotion_prompt": str(emotion_prompt or ""),
        }

        start_msg = f"开始远端 TTS 合成：{engine_name}（共 {len(segments)} 段）"
        _safe_task_update(task, message=start_msg)
        _safe_task_note(task, start_msg)

        if self.synth_fn is not None:
            segment_wavs = self._invoke_synth_fn(
                task=task,
                engine=engine,
                segments=segments,
                segments_dir=segments_dir,
                seg_entries=seg_entries,
                config=base_config,
                ref_audio_path=resolved_ref_audio,
                ref_text=effective_ref_text,
                voice_prompt=effective_voice_prompt,
                voice_preset=effective_voice_preset,
                voice_seed=effective_voice_seed,
                emotion_ref_path=resolved_emo_audio,
                emotion_prompt=str(emotion_prompt or ""),
                hf_token=hf_token,
            )
        else:
            env_dir = self.ensure_engine_env(task, engine, hf_token="")
            worker_python = str(_resolve_venv_python(env_dir / ".venv"))
            source_dir = env_dir / "source"
            if source_dir.is_dir():
                base_config["source_dir"] = str(source_dir)

            worker_path = work_root / f"worker_{engine['id']}.py"
            config_path = work_root / f"config_{engine['id']}.json"
            log_path = work_root / f"tts_{engine['id']}.log"
            if worker_path.is_symlink():
                raise ValueError("TTS 工作脚本路径不能是符号链接")
            if config_path.is_symlink():
                raise ValueError("TTS 配置文件路径不能是符号链接")
            if log_path.is_symlink():
                raise ValueError("TTS 日志文件路径不能是符号链接")

            worker_path.write_text(
                self.build_worker_script(engine["id"]), encoding="utf-8"
            )
            atomic_json(config_path, base_config)

            # Pass HF_TOKEN strictly via worker subprocess environment (never CLI args, config JSON, or pip/git).
            worker_env = dict(os.environ)
            worker_env["PYTHONUNBUFFERED"] = "1"
            if hf_token:
                worker_env["HF_TOKEN"] = hf_token
                worker_env["HUGGING_FACE_HUB_TOKEN"] = hf_token
            else:
                worker_env.pop("HF_TOKEN", None)
                worker_env.pop("HUGGING_FACE_HUB_TOKEN", None)

            self._invoke_command(
                [worker_python, str(worker_path), "--config", str(config_path)],
                task,
                log_path,
                timeout=1800,
                cwd=work_root,
                env=worker_env,
                secret_to_scrub=hf_token,
            )
            segment_wavs = [
                (str(seg["text"]), Path(seg["output_wav"])) for seg in seg_entries
            ]

        for idx, (_, wav_file) in enumerate(segment_wavs):
            if not Path(wav_file).is_file():
                raise RuntimeError(
                    f"远端 TTS 引擎 {engine_name} 未生成第 {idx + 1} 段音频：{wav_file}"
                )

        master_wav = work_root / "master.wav"
        manifest_path = work_root / "manifest.json"
        manifest = assemble_colabone_tts_master(
            segment_wavs,
            out_wav=master_wav,
            manifest_path=manifest_path,
            pause_seconds=pause_seconds,
            target_sr=48000,
            target_lufs=-23.0,
        )

        done_msg = (
            f"远端 TTS 合成完成：{engine_name}"
            f"（总时长 {manifest['duration']:.2f} 秒，{manifest['sample_rate']} Hz）"
        )
        _safe_task_update(task, message=done_msg)
        _safe_task_note(task, done_msg)
        return {
            "audio_path": master_wav,
            "manifest_path": manifest_path,
            "manifest": manifest,
            "duration": float(manifest["duration"]),
            "engine_id": engine["id"],
            "sample_rate": int(manifest["sample_rate"]),
        }
