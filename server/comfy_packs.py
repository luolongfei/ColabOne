"""Isolated ComfyUI workflow pack provisioning, node patching, and model caching.

Runs exclusively on the remote GPU runtime: checks hardware/disk preflight,
checks out pinned custom node commits per workflow pack, applies strictly
allowlisted compatibility patches, atomically swaps ColabOne-managed symlinks
inside ComfyUI's ``custom_nodes`` directory without touching user-placed nodes,
and downloads missing Hugging Face model weights with parallel range transfer
and exact SHA-256 verification.
"""

from __future__ import annotations

import ast
from bisect import bisect_left
from collections.abc import Callable
from contextlib import nullcontext
import hashlib
import importlib.metadata
import importlib.util
import inspect
import io
import json
import os
import random
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import threading
import time
import tokenize
import uuid
import zipfile
from pathlib import Path, PurePosixPath
from typing import Any
from urllib.parse import quote

import requests

from common.files import atomic_json
from server.download import (
    ColabOneRangeUnsupportedError,
    ColabOneTransfer,
    ITER_CHUNK_BYTES,
    _abort_response,
    cached_bytes,
    metadata_path,
    parallel_file,
    parse_retry_after,
)

COMFY_MODEL_SUBDIRS: tuple[str, ...] = (
    "checkpoints",
    "diffusion_models",
    "loras",
    "vae",
    "text_encoders",
    "clip_vision",
    "wav2vec2",
    "controlnet",
    "upscale_models",
    "audio_encoders",
)
_COMFY_MODEL_SUBDIR_SET = frozenset(COMFY_MODEL_SUBDIRS)
_COMFY_MODEL_EXTENSIONS: tuple[str, ...] = (
    ".safetensors",
    ".ckpt",
    ".pt",
    ".pth",
    ".bin",
    ".onnx",
    ".gguf",
    ".sft",
)

TORCH_SKIP_PACKAGES = frozenset({"torch", "torchvision", "torchaudio", "xformers"})
ALLOWED_NODE_PATCH_KINDS = frozenset({"wav2vec2_hidden_states_hook"})
WAV2VEC2_PATCH_MARKER = "# COLABONE_WAV2VEC2_HOOK_PATCH"
PACK_PATCH_VERSION = 2

DEFAULT_DOWNLOAD_THREADS = 16
PREFLIGHT_DISK_HEADROOM_BYTES = 4 * 1024**3

SAGEATTN_REPO_URL = "https://github.com/thu-ml/SageAttention.git"
SAGEATTN_COMMIT = "d1a57a546c3d395b1ffcbeecc66d81db76f3b4b5"
_BLACKWELL_COMPUTE_CAPABILITIES = frozenset({(10, 0), (12, 0), (12, 1)})
_SAGEATTN_WHEEL_LOCK = threading.RLock()
_SAGEATTN_FAILED_BUILDS: set[tuple[str, str]] = set()
_SAGEATTN_INSTALL_GENERATION: int = 0
_MAX_BUILD_LOG_BYTES = 16 * 1024 * 1024
_STRIPPED_SUBPROCESS_ENV_KEYS = frozenset(
    {
        "HF_TOKEN",
        "HUGGING_FACE_HUB_TOKEN",
        "HUGGINGFACE_HUB_TOKEN",
        "HF_API_TOKEN",
        "PIP_USER",
        "PIP_TARGET",
        "PIP_PREFIX",
        "PIP_CONSTRAINT",
        "PIP_REQUIRE_VIRTUALENV",
        "PYTHONUSERBASE",
        "PYTHONPATH",
        "PYTHONNOUSERSITE",
    }
)


def is_blackwell_cc(cc_major: int, cc_minor: int) -> bool:
    return (int(cc_major), int(cc_minor)) in _BLACKWELL_COMPUTE_CAPABILITIES


def _current_sageattn_install_generation() -> int:
    with _SAGEATTN_WHEEL_LOCK:
        return _SAGEATTN_INSTALL_GENERATION
_CACHE_KEY_UNSAFE_RE = re.compile(r"[^a-zA-Z0-9._-]+")
_VERSION_PREFIX_RE = re.compile(r"^\s*v?(\d+(?:\.\d+)*)")
_NVCC_RELEASE_RE = re.compile(r"release\s+(\d+\.\d+(?:\.\d+)?)", re.IGNORECASE)
_NVCC_V_RE = re.compile(r"\bV(\d+\.\d+(?:\.\d+)?)")
_NVCC_NUM_RE = re.compile(r"\b(\d+\.\d+(?:\.\d+)?)\b")
_TORCH_CU_TAG_RE = re.compile(r"\+cu(\d{2})(\d)\b", re.IGNORECASE)

_WORKFLOW_ID_RE = re.compile(r"^[a-zA-Z0-9_-]{1,80}$")
_COMMIT_SHA_RE = re.compile(r"^[a-fA-F0-9]{40}$")
_SHA256_RE = re.compile(r"^[a-fA-F0-9]{64}$")
_REPO_SLUG_RE = re.compile(r"^[\w.-]+/[\w.-]+$")
_GITHUB_REPO_RE = re.compile(r"^https://github\.com/([\w.-]+)/([\w.-]+?)(?:\.git)?/?$")
_PIP_PKG_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._\-\[\],<>=!~ ]{0,199}$")
_PEP263_CODING_RE = re.compile(r"^[ \t\f]*#.*?coding[:=][ \t]*([-\w.]+)", re.ASCII)
_PIP_OPTION_FLAG_RE = re.compile(r"(?:^|\s)(?:--|-[A-Za-z])")
_DIRECT_URL_OR_VCS_RE = re.compile(
    r"://|(?:^|[\s@])(?:git|hg|svn|bzr)\+|(?:^|[\s@])file:",
    re.IGNORECASE,
)
_PEP508_SPEC_START_RE = re.compile(
    r"^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?(?:\s*\[|\s*(?:===|==|!=|<=|>=|~=|<|>|\()|$)"
)
_LOCAL_ARCHIVE_SUFFIXES: tuple[str, ...] = (
    ".whl",
    ".tar.gz",
    ".tgz",
    ".tar.bz2",
    ".tar.xz",
    ".zip",
)
_EGG_NAME_RE = re.compile(r"#egg=([A-Za-z0-9_.-]+)", re.IGNORECASE)
_WHEEL_TORCH_RE = re.compile(
    r"^(?:torch|torchvision|torchaudio|xformers)[-_][0-9]", re.IGNORECASE
)

_WAV2VEC2_HELPER_SNIPPET = f"""{WAV2VEC2_PATCH_MARKER}
def _colabone_wav2vec2_with_hidden_states(_colabone_wav2vec2_model, /, *args, **kwargs):
    _colabone_layers = []
    _colabone_first = []
    _colabone_handles = []
    _encoder = getattr(_colabone_wav2vec2_model, "encoder", None)
    if _encoder is None:
        _inner = getattr(_colabone_wav2vec2_model, "wav2vec2", None)
        _encoder = getattr(_inner, "encoder", None) if _inner is not None else None
    _enc_layers = getattr(_encoder, "layers", None)
    if _enc_layers is not None:
        for _idx, _layer in enumerate(_enc_layers):
            if _idx == 0:
                def _hook_first(_mod, _inp, _out):
                    if _inp and _inp[0] is not None:
                        _colabone_first.append(_inp[0])
                    _colabone_layers.append(_out[0] if isinstance(_out, tuple) else _out)
                _colabone_handles.append(_layer.register_forward_hook(_hook_first))
            else:
                def _hook_layer(_mod, _inp, _out):
                    _colabone_layers.append(_out[0] if isinstance(_out, tuple) else _out)
                _colabone_handles.append(_layer.register_forward_hook(_hook_layer))
    try:
        _embeddings = _colabone_wav2vec2_model(*args, **kwargs)
    finally:
        for _h in _colabone_handles:
            _h.remove()
    if (
        _embeddings is not None
        and getattr(_embeddings, "hidden_states", None) is None
        and _colabone_layers
    ):
        _lhs = getattr(_embeddings, "last_hidden_state", None)
        _head = (
            _colabone_first[0]
            if _colabone_first
            else (_lhs if _lhs is not None else _colabone_layers[0])
        )
        _collected = tuple([_head, *_colabone_layers])
        try:
            _embeddings.hidden_states = _collected
            if isinstance(_embeddings, dict):
                _embeddings["hidden_states"] = _collected
        except Exception:
            class _ColabOneWav2Vec2OutputProxy:
                def __init__(self, wrapped, hidden_states):
                    self._wrapped = wrapped
                    self.hidden_states = hidden_states

                def __getattr__(self, name):
                    return getattr(self._wrapped, name)

                def __len__(self):
                    return len(self._wrapped)

                def __getitem__(self, key):
                    if key == "hidden_states":
                        return self.hidden_states
                    return self._wrapped[key]

            _embeddings = _ColabOneWav2Vec2OutputProxy(_embeddings, _collected)
    return _embeddings
"""


def _as_dict(item: Any) -> dict[str, Any]:
    if isinstance(item, dict):
        return item
    dump = getattr(item, "model_dump", None)
    if callable(dump):
        return dump()
    raise TypeError(f"期望 dict 或模型对象，实际为 {type(item).__name__}")


def _ensure_dir(path: Path, label: str) -> None:
    if path.is_symlink():
        sep = " " if label and label[-1].isascii() else ""
        raise ValueError(f"{label}{sep}不能是符号链接")
    path.mkdir(parents=True, exist_ok=True)


def _validate_safe_node_name(value: Any) -> str:
    if not isinstance(value, str):
        raise ValueError("自定义节点名称不合法")
    name = value.strip()
    if (
        not name
        or len(name) > 120
        or name in (".", "..")
        or name.startswith(".")
        or ".." in name
        or "/" in name
        or "\\" in name
        or ":" in name
        or "\x00" in name
    ):
        raise ValueError(f"自定义节点名称不合法：{value!r}")
    return name


def _validate_commit_sha(value: Any, label: str = "提交哈希") -> str:
    if not isinstance(value, str):
        raise ValueError(f"{label}不合法")
    sha = value.strip().lower()
    if not _COMMIT_SHA_RE.fullmatch(sha):
        raise ValueError(f"{label}必须为 40 位十六进制 SHA：{value!r}")
    return sha


def _normalize_git_repo_url(value: Any) -> str:
    if not isinstance(value, str):
        raise ValueError("自定义节点仓库地址不合法")
    raw = value.strip()
    match = _GITHUB_REPO_RE.fullmatch(raw)
    if match:
        owner, repo = match.group(1), match.group(2)
        if owner in (".", "..") or repo in (".", ".."):
            raise ValueError(f"自定义节点仓库地址不合法：{value!r}")
        return raw
    if _REPO_SLUG_RE.fullmatch(raw) and not any(
        part in (".", "..") for part in raw.split("/")
    ):
        return f"https://github.com/{raw}.git"
    raise ValueError(f"自定义节点仓库地址不合法：{value!r}")


def _validate_hf_repo(value: Any) -> str:
    if not isinstance(value, str):
        raise ValueError("模型仓库地址不合法")
    raw = value.strip()
    if not _REPO_SLUG_RE.fullmatch(raw) or any(
        part in (".", "..") for part in raw.split("/")
    ):
        raise ValueError(f"模型仓库地址不合法：{value!r}")
    return raw


def _validate_pip_packages(packages: list[Any]) -> list[str]:
    cleaned: list[str] = []
    for item in packages:
        if not isinstance(item, str):
            raise ValueError(f"非法的 pip 依赖声明：{item!r}")
        pkg = item.strip()
        if (
            not pkg
            or pkg.startswith("-")
            or "://" in pkg
            or ".." in pkg
            or not _PIP_PKG_RE.fullmatch(pkg)
        ):
            raise ValueError(f"非法的 pip 依赖声明：{item!r}")
        cleaned.append(pkg)
    return cleaned


def _set_task_attr(task, **attrs: Any) -> None:
    if task is None:
        return
    task_lock = getattr(task, "lock", None)
    if task_lock is not None:
        with task_lock:
            for key, val in attrs.items():
                setattr(task, key, val)
    else:
        for key, val in attrs.items():
            setattr(task, key, val)


def _map_progress(fraction: float, progress_range: tuple[float, float] | None) -> float:
    clamped = max(0.0, min(1.0, float(fraction)))
    if progress_range is None:
        return clamped
    lo = max(0.0, min(1.0, float(progress_range[0])))
    hi = max(lo, min(1.0, float(progress_range[1])))
    return lo + (hi - lo) * clamped


def _req_package_name(line: str) -> str:
    raw = re.sub(r"(?:^|\s)#.*$", "", line).strip()
    if not raw or raw.startswith("-"):
        return ""
    match = re.match(r"^([A-Za-z0-9_.-]+)", raw)
    if not match:
        return ""
    return re.sub(r"[-_.]+", "-", match.group(1)).lower()


def _join_continued_lines(text: str) -> list[str]:
    joined: list[str] = []
    buf = ""
    for raw_line in text.replace("\r\n", "\n").split("\n"):
        combined = (buf + raw_line.lstrip()) if buf else raw_line
        if not combined.lstrip().startswith("#") and combined.endswith("\\"):
            trailing_bs = len(combined) - len(combined.rstrip("\\"))
            if trailing_bs % 2 == 1:
                buf = combined[:-1]
                continue
        joined.append(combined)
        buf = ""
    if buf:
        joined.append(buf)
    return joined


def _strip_quoted_marker_literals(text: str) -> str | None:
    """Replace balanced single/double-quoted PEP 508 literals with placeholders."""
    out: list[str] = []
    quote_char = ""
    for ch in text:
        if quote_char:
            if ch == quote_char:
                quote_char = ""
                out.append('""')
        elif ch in ("'", '"'):
            quote_char = ch
        else:
            out.append(ch)
    if quote_char:
        return None
    return "".join(out)


def _sanitize_requirement_line(raw_line: str) -> str | None:
    """Return a clean PEP 508 package requirement line, or ``None`` to drop it."""
    if (
        "\r" in raw_line
        or "\n" in raw_line
        or "\x00" in raw_line
        or any(ord(ch) < 32 and ch != "\t" for ch in raw_line)
        or any(ch in raw_line for ch in ("\x85", "\u2028", "\u2029"))
    ):
        return None
    stripped = raw_line.strip()
    if not stripped or stripped.startswith("#"):
        return None
    code_part = re.sub(r"(?:^|\s)#.*$", "", stripped).strip()
    if not code_part:
        return None
    if _DIRECT_URL_OR_VCS_RE.search(code_part):
        return None
    if _PIP_OPTION_FLAG_RE.search(code_part):
        return None

    spec_part, has_marker, marker_part = code_part.partition(";")
    spec_part = spec_part.strip()
    marker_part = marker_part.strip()
    if (
        not spec_part
        or spec_part.startswith(("-", ".", "/", "\\"))
        or any(ch in spec_part for ch in ("@", "/", "\\", '"', "'"))
        or re.search(r"(?:^|\s)-", spec_part)
        or spec_part.lower().endswith(_LOCAL_ARCHIVE_SUFFIXES)
        or not _PEP508_SPEC_START_RE.match(spec_part)
    ):
        return None

    if has_marker:
        if not marker_part:
            return None
        unquoted_marker = _strip_quoted_marker_literals(marker_part)
        if (
            unquoted_marker is None
            or ";" in unquoted_marker
            or re.search(r"(?:^|\s)-", unquoted_marker)
        ):
            return None

    return code_part


def _is_torch_requirement_line(line: str) -> bool:
    if _req_package_name(line) in TORCH_SKIP_PACKAGES:
        return True
    egg_match = _EGG_NAME_RE.search(line)
    if egg_match:
        egg_norm = re.sub(r"[-_.]+", "-", egg_match.group(1)).lower()
        if egg_norm in TORCH_SKIP_PACKAGES or egg_norm.startswith(
            ("torch", "xformers")
        ):
            return True
    code_part = re.sub(r"(?:^|\s)#.*$", "", line).strip()
    if "://" in code_part:
        url_token = code_part.split()[0]
        url_path = url_token.split("#", 1)[0].split("?", 1)[0]
        basename = PurePosixPath(url_path).name
        if _WHEEL_TORCH_RE.match(basename):
            return True
    return False


def _filter_requirements(req_path: Path, out_path: Path, skip_torch: bool) -> bool:
    if out_path.is_symlink():
        raise ValueError(f"过滤后的依赖文件不能是符号链接：{out_path.name}")
    kept: list[str] = []
    raw_text = req_path.read_bytes().decode("utf-8-sig")
    for raw_line in _join_continued_lines(raw_text):
        safe_line = _sanitize_requirement_line(raw_line)
        if not safe_line:
            continue
        if skip_torch and _is_torch_requirement_line(safe_line):
            continue
        kept.append(safe_line)
    if not kept:
        if out_path.exists() and not out_path.is_symlink():
            out_path.unlink(missing_ok=True)
        return False
    _atomic_write_text(out_path, "\n".join(kept) + "\n")
    return True


def _write_torch_constraints(dest_dir: Path) -> Path | None:
    """Pin currently installed torch/CUDA packages to prevent pip from replacing them."""
    pins: list[str] = []
    for pkg in sorted(TORCH_SKIP_PACKAGES):
        try:
            ver = importlib.metadata.version(pkg)
        except importlib.metadata.PackageNotFoundError:
            continue
        except Exception:
            continue
        clean_ver = str(ver or "").strip()
        if clean_ver:
            pins.append(f"{pkg}=={clean_ver}")
    if not pins:
        return None
    dest = Path(dest_dir)
    _ensure_dir(dest, "依赖约束目录")
    constraints_path = dest / ".colabone-torch-constraints.txt"
    _atomic_write_text(constraints_path, "\n".join(pins) + "\n")
    return constraints_path


def _sha256_file(path: Path, task=None) -> str:
    hasher = hashlib.sha256()
    with path.open("rb") as stream:
        while chunk := stream.read(8 * 1024**2):
            if task is not None and callable(getattr(task, "check", None)):
                task.check()
            hasher.update(chunk)
    return hasher.hexdigest()


def _format_gb_from_mib(mib: int) -> str:
    gb = mib / 1024.0
    if abs(gb - round(gb)) < 0.05:
        return f"{int(round(gb))} GB"
    return f"{gb:.1f} GB"


def _atomic_write_text(path: Path, content: str) -> None:
    if path.is_symlink():
        raise ValueError(f"目标文件不能是符号链接：{path.name}")
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix=".patch-", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def _pos_to_byte_offset(offsets: list[int], lineno: int, col_offset: int) -> int:
    return offsets[lineno - 1] + col_offset


def _is_wav2vec2_patch_target(node: ast.Call) -> bool:
    func = node.func
    if isinstance(func, ast.Name) and func.id == "_colabone_wav2vec2_with_hidden_states":
        return False
    ohs_kw = next(
        (kw for kw in node.keywords if kw.arg == "output_hidden_states"),
        None,
    )
    if ohs_kw is None:
        return False
    if isinstance(ohs_kw.value, ast.Constant) and ohs_kw.value.value is False:
        return False
    if isinstance(func, ast.Name) and "wav2vec" in func.id.lower():
        return True
    if isinstance(func, ast.Attribute) and "wav2vec" in func.attr.lower():
        return True
    if any(
        (isinstance(sub, ast.Name) and "wav2vec" in sub.id.lower())
        or (isinstance(sub, ast.Attribute) and "wav2vec" in sub.attr.lower())
        for sub in ast.walk(func)
    ):
        return True
    return isinstance(ohs_kw.value, ast.Constant) and ohs_kw.value.value is True


def _build_token_byte_index(
    source: str, str_lines: list[str], offsets: list[int]
) -> tuple[list[int], list[tuple[int, int, int]]]:
    starts: list[int] = []
    tokens: list[tuple[int, int, int]] = []
    for tok in tokenize.generate_tokens(io.StringIO(source).readline):
        if tok.type in (tokenize.ENCODING, tokenize.ENDMARKER):
            continue
        s_line, s_col = tok.start
        e_line, e_col = tok.end
        if (
            s_line < 1
            or s_line > len(str_lines)
            or e_line < 1
            or e_line > len(str_lines)
        ):
            continue
        start_b = offsets[s_line - 1] + len(
            str_lines[s_line - 1][:s_col].encode("utf-8")
        )
        end_b = offsets[e_line - 1] + len(
            str_lines[e_line - 1][:e_col].encode("utf-8")
        )
        starts.append(start_b)
        tokens.append((tok.exact_type, start_b, end_b))
    return starts, tokens


_SKIP_BEFORE_CALL_LPAR = frozenset(
    {
        tokenize.COMMENT,
        tokenize.NL,
        tokenize.NEWLINE,
        tokenize.INDENT,
        tokenize.DEDENT,
        tokenize.RPAR,
    }
)


def _find_call_lpar_end(
    token_starts: list[int],
    tokens: list[tuple[int, int, int]],
    func_end: int,
    call_end: int,
) -> int:
    idx = bisect_left(token_starts, func_end)
    while idx < len(tokens):
        tok_type, start_b, end_b = tokens[idx]
        if end_b > call_end:
            break
        if tok_type in _SKIP_BEFORE_CALL_LPAR:
            idx += 1
            continue
        if tok_type == tokenize.LPAR:
            return end_b
        break
    return -1


def _patch_wav2vec2_source(source: str) -> tuple[str, bool]:
    """Patch wav2vec2(..., output_hidden_states=...) calls idempotently.

    Returns ``(new_source, changed)``.
    """
    if source.startswith("\ufeff"):
        source = source[1:]
    if "\r" in source:
        source = source.replace("\r\n", "\n").replace("\r", "\n")
    if WAV2VEC2_PATCH_MARKER in source:
        return source, False

    try:
        tree = ast.parse(source)
    except SyntaxError as exc:
        raise RuntimeError(f"无法解析 multitalk/nodes.py：{exc}") from exc

    str_lines = io.StringIO(source).readlines()
    offsets = [0]
    for line in str_lines:
        offsets.append(offsets[-1] + len(line.encode("utf-8")))
    source_bytes = source.encode("utf-8")

    try:
        token_starts, tokens = _build_token_byte_index(source, str_lines, offsets)
    except tokenize.TokenError as exc:
        raise RuntimeError(f"无法词法解析 multitalk/nodes.py：{exc}") from exc

    raw_replacements: list[tuple[int, int, bytes]] = []

    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        if not _is_wav2vec2_patch_target(node):
            continue
        func = node.func
        if (
            node.lineno is None
            or node.end_lineno is None
            or node.col_offset is None
            or node.end_col_offset is None
            or func.lineno is None
            or func.end_lineno is None
            or func.col_offset is None
            or func.end_col_offset is None
        ):
            continue
        call_start = _pos_to_byte_offset(offsets, node.lineno, node.col_offset)
        call_end = _pos_to_byte_offset(offsets, node.end_lineno, node.end_col_offset)
        func_start = _pos_to_byte_offset(offsets, func.lineno, func.col_offset)
        func_end = _pos_to_byte_offset(offsets, func.end_lineno, func.end_col_offset)
        lpar_end = _find_call_lpar_end(token_starts, tokens, func_end, call_end)
        if lpar_end < 0:
            continue
        callee_expr = source_bytes[func_start:func_end]
        if call_start < func_start:
            callee_expr = b"(" + callee_expr + b")"
        raw_replacements.append(
            (
                call_start,
                lpar_end,
                b"_colabone_wav2vec2_with_hidden_states(" + callee_expr + b", ",
            )
        )

    if not raw_replacements:
        raise RuntimeError(
            "未在 multitalk/nodes.py 中找到预期的 wav2vec2(..., output_hidden_states=...) 调用"
        )

    # Deduplicate/guard against overlapping spans before applying in reverse order.
    replacements: list[tuple[int, int, bytes]] = []
    last_end = -1
    for start, end, repl in sorted(raw_replacements, key=lambda item: (item[0], -item[1])):
        if start < last_end:
            continue
        replacements.append((start, end, repl))
        last_end = end

    patched_bytes = source_bytes
    for start, end, repl in sorted(replacements, key=lambda item: item[0], reverse=True):
        patched_bytes = patched_bytes[:start] + repl + patched_bytes[end:]
    patched = patched_bytes.decode("utf-8")

    # Insert helper after optional module docstring and `from __future__` imports.
    insert_after_line = 0
    for idx, stmt in enumerate(tree.body):
        if (
            idx == 0
            and isinstance(stmt, ast.Expr)
            and isinstance(stmt.value, ast.Constant)
            and isinstance(stmt.value.value, str)
        ):
            insert_after_line = max(insert_after_line, stmt.end_lineno or stmt.lineno)
            continue
        if isinstance(stmt, ast.ImportFrom) and stmt.module == "__future__":
            insert_after_line = max(insert_after_line, stmt.end_lineno or stmt.lineno)
            continue
        break

    patched_lines = io.StringIO(patched).readlines()
    if insert_after_line <= 0 and patched_lines:
        # Preserve optional shebang and PEP 263 coding header on lines 1-2,
        # even when line 2's coding comment is preceded by a license comment or blank line.
        if patched_lines[0].startswith("#!"):
            insert_after_line = 1
        if _PEP263_CODING_RE.match(patched_lines[0]):
            insert_after_line = max(insert_after_line, 1)
        elif (
            len(patched_lines) >= 2
            and (
                not patched_lines[0].strip()
                or patched_lines[0].lstrip().startswith("#")
            )
            and _PEP263_CODING_RE.match(patched_lines[1])
        ):
            insert_after_line = max(insert_after_line, 2)

    prefix = "".join(patched_lines[:insert_after_line])
    suffix = "".join(patched_lines[insert_after_line:])
    if prefix and not prefix.endswith("\n"):
        prefix += "\n"
    final_source = prefix + "\n" + _WAV2VEC2_HELPER_SNIPPET + "\n" + suffix

    try:
        ast.parse(final_source)
    except SyntaxError as exc:
        raise RuntimeError(f"应用 wav2vec2 兼容补丁后语法校验失败：{exc}") from exc

    return final_source, True


def _pack_content_payload(workflow_template: dict) -> dict[str, Any]:
    wf = _as_dict(workflow_template)
    normalized_nodes: list[tuple[str, str, str, bool]] = []
    for raw_node in wf.get("custom_nodes") or []:
        node = _as_dict(raw_node)
        raw_repo = str(node.get("repo") or "").strip()
        try:
            norm_repo = _normalize_git_repo_url(raw_repo)
        except ValueError:
            norm_repo = raw_repo
        normalized_nodes.append(
            (
                str(node.get("name") or "").strip(),
                norm_repo,
                str(node.get("commit") or "").strip().lower(),
                bool(node.get("install_requirements", True)),
            )
        )
    normalized_nodes.sort()
    patches = sorted(
        (
            str(_as_dict(patch).get("node_name") or "").strip(),
            str(_as_dict(patch).get("kind") or "").strip(),
        )
        for patch in (wf.get("node_patches") or [])
    )
    pip_packages = sorted(
        {
            str(pkg).strip()
            for pkg in (wf.get("pip_packages") or [])
            if str(pkg).strip()
        }
    )
    return {
        "custom_nodes": normalized_nodes,
        "node_patches": patches,
        "pip_packages": pip_packages,
        "patch_version": PACK_PATCH_VERSION,
    }


def compute_pack_content_signature(workflow_template: dict) -> str:
    """Compute a deterministic SHA-256 content signature (excluding workflow ID).

    Used for checkout directory reuse and ComfyUI restart detection so workflows
    sharing identical custom_nodes, patches, and pip dependencies never re-clone
    or restart ComfyUI unnecessarily.
    """
    payload = _pack_content_payload(workflow_template)
    encoded = json.dumps(
        payload, ensure_ascii=False, sort_keys=True, separators=(",", ":")
    ).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def _sanitized_subprocess_env(
    extra_env: dict[str, str] | None = None,
) -> dict[str, str]:
    env = {
        k: v
        for k, v in os.environ.items()
        if k not in _STRIPPED_SUBPROCESS_ENV_KEYS
    }
    if extra_env:
        for k, v in extra_env.items():
            if k not in _STRIPPED_SUBPROCESS_ENV_KEYS and v is not None:
                env[str(k)] = str(v)
    return env


def _is_cancelled_exception(exc: BaseException) -> bool:
    return any(
        cls.__name__ == "ColabOneCancelledError" for cls in type(exc).__mro__
    )


def _safe_nonneg_int(value: Any, default: int = 0) -> int:
    if isinstance(value, bool) or value is None:
        return default
    try:
        parsed = int(float(value))
    except (TypeError, ValueError, OverflowError):
        return default
    return max(0, parsed)


def _parse_version_tuple(raw: Any) -> tuple[int, ...]:
    if not isinstance(raw, str):
        raw = str(raw or "")
    match = _VERSION_PREFIX_RE.match(raw)
    if not match:
        return ()
    return tuple(int(part) for part in match.group(1).split("."))


def _parse_nvcc_version(output: str) -> str:
    text = str(output or "")
    if not text.strip():
        return ""
    for pattern in (_NVCC_RELEASE_RE, _NVCC_V_RE, _NVCC_NUM_RE):
        match = pattern.search(text)
        if match:
            return match.group(1).strip()
    return ""


def _probe_nvcc_version() -> str:
    nvcc_bin = shutil.which("nvcc")
    if not nvcc_bin:
        return ""
    try:
        res = subprocess.run(
            [nvcc_bin, "--version"],
            capture_output=True,
            text=True,
            timeout=5,
            check=False,
            stdin=subprocess.DEVNULL,
            env=_sanitized_subprocess_env(),
        )
        if res.returncode != 0:
            return ""
        return _parse_nvcc_version(res.stdout or res.stderr or "")
    except Exception:
        return ""


def _cuda_version_from_torch_tag(torch_version: str) -> str:
    match = _TORCH_CU_TAG_RE.search(str(torch_version or ""))
    if not match:
        return ""
    return f"{int(match.group(1))}.{int(match.group(2))}"


def _probe_nvidia_smi_gpu_caps() -> tuple[str, int, int, int]:
    """Query GPU name, VRAM (MiB), and compute capability via nvidia-smi without importing torch."""
    smi_bin = shutil.which("nvidia-smi")
    if not smi_bin:
        return ("", 0, 0, 0)
    try:
        res = subprocess.run(
            [
                smi_bin,
                "--query-gpu=name,memory.total,compute_cap",
                "--format=csv,noheader,nounits",
            ],
            capture_output=True,
            text=True,
            timeout=5,
            check=False,
            stdin=subprocess.DEVNULL,
            env=_sanitized_subprocess_env(),
        )
        if res.returncode != 0:
            return ("", 0, 0, 0)
        first = next(
            (ln.strip() for ln in (res.stdout or "").splitlines() if ln.strip()), ""
        )
        if not first:
            return ("", 0, 0, 0)
        parts = [p.strip() for p in first.split(",")]
        if len(parts) < 3:
            return ("", 0, 0, 0)
        cc_raw = parts[-1]
        vram_raw = parts[-2]
        name = ", ".join(parts[:-2]).strip()
        vram_mib = _safe_nonneg_int(vram_raw)
        cc_tuple = _parse_version_tuple(cc_raw)
        cc_major = cc_tuple[0] if len(cc_tuple) >= 1 else 0
        cc_minor = cc_tuple[1] if len(cc_tuple) >= 2 else 0
        return (name, vram_mib, cc_major, cc_minor)
    except Exception:
        return ("", 0, 0, 0)


def _has_module_spec(module_name: str) -> bool:
    try:
        return importlib.util.find_spec(module_name) is not None
    except Exception:
        return False


def _min_nvcc_for_sage2(cc_major: int, cc_minor: int) -> tuple[int, int]:
    cc = (cc_major, cc_minor)
    if cc >= (10, 0):
        return (12, 8)
    if cc == (9, 0):
        return (12, 3)
    if cc == (8, 9):
        return (12, 4)
    if cc >= (8, 0):
        return (12, 0)
    return (99, 0)


def _sanitize_cache_key(raw: str) -> str:
    cleaned = _CACHE_KEY_UNSAFE_RE.sub("_", str(raw or ""))
    cleaned = re.sub(r"\.{2,}", ".", cleaned).strip("._-")
    if not cleaned or cleaned in (".", ".."):
        return "default"
    return cleaned[:160]


def _wheel_version_from_filename(filename: str) -> str:
    stem = filename[:-4] if filename.lower().endswith(".whl") else filename
    parts = stem.split("-")
    if len(parts) < 2:
        return ""
    return parts[1].strip()


def _detect_total_ram_gib() -> float:
    """Return total host RAM in GiB (0.0 if unavailable) for safe NVCC build concurrency."""
    try:
        page_size = os.sysconf("SC_PAGE_SIZE")
        phys_pages = os.sysconf("SC_PHYS_PAGES")
        if isinstance(page_size, int) and isinstance(phys_pages, int) and page_size > 0 and phys_pages > 0:
            return (page_size * phys_pages) / float(1024**3)
    except (AttributeError, ValueError, OSError):
        pass
    try:
        for line in Path("/proc/meminfo").read_text(encoding="utf-8", errors="ignore").splitlines():
            if line.startswith("MemTotal:"):
                parts = line.split()
                if len(parts) >= 2:
                    return int(parts[1]) / float(1024**2)
    except (OSError, ValueError):
        pass
    return 0.0


def _sageattention_build_env(cc_major: int, cc_minor: int) -> dict[str, str]:
    """Build NVCC/CUDA extension env with MAX_JOBS scaled to host RAM to prevent OOM-kills."""
    cpus = max(1, os.cpu_count() or 4)
    ram_gib = _detect_total_ram_gib()
    if ram_gib > 0.0 and ram_gib < 32.0:
        max_jobs = str(min(4, cpus))
    elif ram_gib > 0.0 and ram_gib < 64.0:
        max_jobs = str(min(6, cpus))
    else:
        max_jobs = str(min(12, cpus))
    return {
        "TORCH_CUDA_ARCH_LIST": f"{cc_major}.{cc_minor}",
        "EXT_PARALLEL": "4",
        "NVCC_APPEND_FLAGS": "--threads 8",
        "MAX_JOBS": max_jobs,
    }


def _is_wheel_metadata_entry(info: zipfile.ZipInfo) -> bool:
    if info.is_dir():
        return False
    norm = info.filename.replace("\\", "/")
    parts = PurePosixPath(norm).parts
    return (
        len(parts) == 2
        and parts[0].endswith(".dist-info")
        and len(parts[0]) > len(".dist-info")
        and parts[1] == "WHEEL"
    )


def _find_cached_wheel(
    cache_dir: Path,
    *,
    dist_prefixes: tuple[str, ...],
    min_major: int | None = None,
    reject_symlinks: bool = True,
    allow_empty: bool = False,
) -> Path | None:
    if cache_dir.is_symlink():
        if reject_symlinks:
            raise ValueError(f"加速包缓存目录不能是符号链接：{cache_dir.name}")
        return None
    if not cache_dir.is_dir():
        return None
    try:
        entries = sorted(cache_dir.iterdir(), key=lambda p: p.name, reverse=True)
    except OSError:
        return None
    for entry in entries:
        lower_name = entry.name.lower()
        if entry.name.startswith(".") or not lower_name.endswith(".whl"):
            continue
        dist_part, sep, _ = lower_name.partition("-")
        if not sep or dist_part not in dist_prefixes:
            continue
        if min_major is not None:
            ver_tuple = _parse_version_tuple(_wheel_version_from_filename(entry.name))
            if not ver_tuple or ver_tuple[0] < min_major:
                continue
        if entry.is_symlink():
            if reject_symlinks:
                raise ValueError(f"加速包缓存文件不能是符号链接：{entry.name}")
            continue
        if not entry.is_file():
            continue
        if not allow_empty:
            try:
                if entry.stat().st_size <= 0:
                    continue
                if not zipfile.is_zipfile(entry):
                    continue
                with zipfile.ZipFile(entry, "r") as zf:
                    infolist = zf.infolist()
                    if not infolist or not any(
                        _is_wheel_metadata_entry(info) for info in infolist
                    ):
                        continue
            except (zipfile.BadZipFile, OSError):
                continue
        return entry
    return None


def _copy_wheel_atomic(src: Path, dst: Path) -> Path:
    if src.is_symlink() or not src.is_file():
        raise ValueError(f"加速包源文件不合法：{src.name}")
    if dst.parent.is_symlink():
        raise ValueError(f"加速包目标目录不能是符号链接：{dst.parent.name}")
    if dst.is_symlink():
        raise ValueError(f"加速包目标文件不能是符号链接：{dst.name}")
    dst.parent.mkdir(parents=True, exist_ok=True)
    tmp = dst.parent / f".{dst.name}.{uuid.uuid4().hex[:8]}.tmp"
    try:
        with src.open("rb") as reader, tmp.open("wb") as writer:
            shutil.copyfileobj(reader, writer, length=1024 * 1024)
            writer.flush()
            try:
                os.fsync(writer.fileno())
            except OSError:
                pass
        os.replace(tmp, dst)
    except OSError:
        tmp.unlink(missing_ok=True)
        if dst.is_symlink():
            raise ValueError(f"加速包目标文件不能是符号链接：{dst.name}")
        try:
            with src.open("rb") as reader, dst.open("wb") as writer:
                shutil.copyfileobj(reader, writer, length=1024 * 1024)
                writer.flush()
                try:
                    os.fsync(writer.fileno())
                except OSError:
                    pass
        except BaseException:
            if not dst.is_symlink():
                try:
                    dst.unlink(missing_ok=True)
                except OSError:
                    pass
            raise
    finally:
        if tmp.exists() or tmp.is_symlink():
            tmp.unlink(missing_ok=True)
    return dst


def _read_log_tail(
    path: Path, max_bytes: int = 4096, *, min_offset: int = 0
) -> str:
    try:
        if path.is_symlink() or not path.is_file():
            return ""
        with path.open("rb") as fh:
            fh.seek(0, os.SEEK_END)
            size = fh.tell()
            floor = min_offset if 0 <= min_offset <= size else 0
            start = max(floor, size - max_bytes)
            if start >= size:
                return ""
            fh.seek(start, os.SEEK_SET)
            data = fh.read(size - start)
        return data.decode("utf-8", errors="replace")
    except OSError:
        return ""


def _open_secure_append_log(
    log_path: Path, *, max_bytes: int = _MAX_BUILD_LOG_BYTES
) -> Any:
    if log_path.parent.is_symlink():
        raise ValueError(f"运行日志目录不能是符号链接：{log_path.parent.name}")
    if log_path.is_symlink():
        raise ValueError(f"运行日志文件不能是符号链接：{log_path.name}")
    log_path.parent.mkdir(parents=True, exist_ok=True)
    truncate = False
    try:
        st = log_path.lstat()
        if stat.S_ISLNK(st.st_mode):
            raise ValueError(f"运行日志文件不能是符号链接：{log_path.name}")
        if st.st_size > max_bytes:
            truncate = True
    except FileNotFoundError:
        truncate = False
    flags = os.O_WRONLY | os.O_CREAT | (os.O_TRUNC if truncate else os.O_APPEND)
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    fd = os.open(str(log_path), flags, 0o600)
    try:
        try:
            os.fchmod(fd, 0o600)
        except (AttributeError, OSError):
            try:
                os.chmod(log_path, 0o600)
            except OSError:
                pass
        return os.fdopen(fd, "wb")
    except BaseException:
        try:
            os.close(fd)
        except OSError:
            pass
        raise


def _run_command(
    args: list[str],
    task,
    log: Path,
    *,
    timeout: int = 1200,
    cwd: Path | str | None = None,
    stopping=None,
    env: dict[str, str] | None = None,
) -> None:
    """Cancelable process group runner with sanitized environment overrides."""
    from server import comfyui as _comfyui_mod

    log = Path(log)
    check_fn = getattr(task, "check", None) if task is not None else None
    note_fn = getattr(task, "note", None) if task is not None else None

    def _check_cancelled() -> None:
        if callable(check_fn):
            check_fn()
        if isinstance(stopping, threading.Event) and stopping.is_set():
            from server.runtime import ColabOneCancelledError

            raise ColabOneCancelledError("实例正在停止，已取消远端加速包准备")

    _check_cancelled()
    has_cancel_hook = task is not None and hasattr(task, "cancel_hook")
    task_lock = getattr(task, "lock", None) if has_cancel_hook else None
    prev_hook = None
    cancel_hook_registered = False
    term_lock = threading.Lock()
    pgrp_cleaned = False
    proc: subprocess.Popen[Any] | None = None

    def _cleanup_pgrp(term_timeout: float = 5.0) -> None:
        nonlocal pgrp_cleaned
        if proc is None:
            return
        with term_lock:
            if not pgrp_cleaned:
                pgrp_cleaned = True
                _comfyui_mod._terminate_process_group(proc, timeout=term_timeout)

    def _cancel_cmd() -> None:
        _cleanup_pgrp(term_timeout=2.0)
        if callable(prev_hook):
            try:
                prev_hook()
            except Exception:
                pass

    with _open_secure_append_log(log) as out:
        try:
            log_start_offset = int(os.fstat(out.fileno()).st_size)
        except OSError:
            log_start_offset = 0
        proc = subprocess.Popen(
            args,
            cwd=cwd,
            env=_sanitized_subprocess_env(env),
            stdout=out,
            stderr=subprocess.STDOUT,
            stdin=subprocess.DEVNULL,
            start_new_session=True,
        )
    try:
        if has_cancel_hook:
            with task_lock if task_lock is not None else nullcontext():
                prev_hook = getattr(task, "cancel_hook", None)
                task.cancel_hook = _cancel_cmd
                cancel_hook_registered = True

        now = time.monotonic()
        end = now + timeout
        noted = now
        last_noted_line = ""
        while proc.poll() is None:
            _check_cancelled()
            now = time.monotonic()
            if now > end:
                raise RuntimeError("远端加速包准备超时，请查看运行日志")
            if now - noted > 5:
                noted = now
                tail = [
                    ln.strip()
                    for ln in _read_log_tail(
                        log, 4096, min_offset=log_start_offset
                    )[-1000:].splitlines()
                    if ln.strip()
                ]
                if tail and tail[-1] != last_noted_line and callable(note_fn):
                    last_noted_line = tail[-1]
                    note_fn(last_noted_line[:250])
            time.sleep(0.25)
        _check_cancelled()
        if proc.returncode:
            err_tail = (
                _read_log_tail(log, 4096, min_offset=log_start_offset)[-600:]
                or _read_log_tail(log, 4096)[-600:]
            )
            raise RuntimeError("远端命令执行失败：" + err_tail)
    finally:
        try:
            _cleanup_pgrp(term_timeout=5.0)
        finally:
            if has_cancel_hook and cancel_hook_registered:
                with task_lock if task_lock is not None else nullcontext():
                    if getattr(task, "cancel_hook", None) is _cancel_cmd:
                        task.cancel_hook = prev_hook


def probe_gpu_acceleration_capabilities(
    gpu_info: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Probe remote GPU architecture and optional kernel acceleration libraries.

    Safe to call on CPU-only or offline environments where ``torch``, ``triton``,
    ``nvcc``, or CUDA are absent. Avoids cold-importing ``torch`` (which would
    pin ~300-500 MiB of CUDA context VRAM inside the control server) by querying
    ``nvidia-smi`` and ``importlib.metadata`` whenever ``torch`` is not already
    loaded in ``sys.modules``. Explicitly treats PyPI ``sageattention 1.x``
    (such as ``1.0.6``) as unavailable because only SageAttention >= 2 is valid.
    """
    info = gpu_info if isinstance(gpu_info, dict) else {}

    # Reuse already-loaded torch (e.g. worker or test mock) without cold-importing CUDA.
    torch_mod = sys.modules.get("torch")

    cuda_available = False
    if torch_mod is not None:
        try:
            cuda_ns = getattr(torch_mod, "cuda", None)
            is_avail = getattr(cuda_ns, "is_available", None)
            cuda_available = bool(is_avail()) if callable(is_avail) else False
        except Exception:
            cuda_available = False

    raw_gpu_name = str(info.get("gpu_name") or info.get("name") or "").strip()
    gpu_name = "" if raw_gpu_name == "GPU 信息不可用" else raw_gpu_name

    vram_mib = 0
    has_explicit_vram = False
    if "vram_mib" in info and info["vram_mib"] is not None:
        vram_mib = _safe_nonneg_int(info["vram_mib"])
        has_explicit_vram = True
    elif "total_mib" in info and info["total_mib"] is not None:
        vram_mib = _safe_nonneg_int(info["total_mib"])
        has_explicit_vram = vram_mib > 0

    if cuda_available and torch_mod is not None and (not gpu_name or not has_explicit_vram):
        try:
            props = torch_mod.cuda.get_device_properties(0)
            if not gpu_name:
                gpu_name = str(getattr(props, "name", "") or "").strip()
            if not has_explicit_vram:
                total_mem = _safe_nonneg_int(getattr(props, "total_memory", 0))
                if total_mem > 0:
                    vram_mib = total_mem // (1024 * 1024)
                    has_explicit_vram = True
        except Exception:
            pass

    has_explicit_cc = "cc_major" in info and info["cc_major"] is not None
    if has_explicit_cc:
        cc_major = _safe_nonneg_int(info["cc_major"])
        cc_minor = _safe_nonneg_int(info.get("cc_minor", 0))
    elif cuda_available and torch_mod is not None:
        try:
            cap = torch_mod.cuda.get_device_capability(0)
            cc_major = _safe_nonneg_int(cap[0])
            cc_minor = _safe_nonneg_int(cap[1])
        except Exception:
            cc_major, cc_minor = 0, 0
    else:
        cc_major, cc_minor = 0, 0

    if (not has_explicit_cc and cc_major == 0) or not gpu_name or not has_explicit_vram:
        smi_name, smi_vram, smi_major, smi_minor = _probe_nvidia_smi_gpu_caps()
        if not gpu_name and smi_name:
            gpu_name = smi_name
        if not has_explicit_vram and smi_vram > 0:
            vram_mib = smi_vram
        if not has_explicit_cc and cc_major == 0 and smi_major > 0:
            cc_major, cc_minor = smi_major, smi_minor

    torch_version = str(info.get("torch_version") or "").strip()
    cuda_version = str(info.get("cuda_version") or "").strip()
    if torch_mod is not None:
        if not torch_version:
            torch_version = str(getattr(torch_mod, "__version__", "") or "").strip()
        if not cuda_version:
            ver_ns = getattr(torch_mod, "version", None)
            cuda_version = str(getattr(ver_ns, "cuda", "") or "").strip()
    if not torch_version:
        try:
            torch_version = str(importlib.metadata.version("torch") or "").strip()
        except Exception:
            torch_version = ""

    nvcc_version = str(info.get("nvcc_version") or "").strip()
    if not nvcc_version:
        nvcc_version = _probe_nvcc_version()

    if not cuda_version:
        cuda_version = _cuda_version_from_torch_tag(torch_version) or nvcc_version

    triton_available = _has_module_spec("triton")
    torch_ver_tuple = _parse_version_tuple(torch_version)
    torch_compile_supported = bool(
        triton_available and len(torch_ver_tuple) >= 2 and torch_ver_tuple[:2] >= (2, 7)
    )
    fp8_fast_supported = (cc_major, cc_minor) >= (8, 9)

    sageattn_version = ""
    try:
        sageattn_version = str(importlib.metadata.version("sageattention") or "").strip()
    except Exception:
        sageattn_version = ""
    sage_ver_tuple = _parse_version_tuple(sageattn_version)
    sageattn_available = bool(
        _has_module_spec("sageattention")
        and bool(sage_ver_tuple)
        and sage_ver_tuple[0] >= 2
    )

    sageattn3_available = bool(
        _has_module_spec("sageattn3")
        and is_blackwell_cc(cc_major, cc_minor)
    )
    flash_attn_2_available = _has_module_spec("flash_attn")
    flash_attn_3_available = bool(
        cc_major == 9 and _has_module_spec("flash_attn_interface")
    )

    return {
        "gpu_name": gpu_name,
        "vram_mib": vram_mib,
        "cc_major": cc_major,
        "cc_minor": cc_minor,
        "torch_version": torch_version,
        "cuda_version": cuda_version,
        "nvcc_version": nvcc_version,
        "triton_available": triton_available,
        "torch_compile_supported": torch_compile_supported,
        "fp8_fast_supported": fp8_fast_supported,
        "sageattn_version": sageattn_version,
        "sageattn_available": sageattn_available,
        "sageattn3_available": sageattn3_available,
        "flash_attn_2_available": flash_attn_2_available,
        "flash_attn_3_available": flash_attn_3_available,
    }


def _invoke_run_cmd_compat(
    args: list[str],
    task,
    log: Path,
    *,
    timeout: int = 1200,
    cwd: Path | str | None = None,
    env: dict[str, str] | None = None,
    stopping=None,
    run_cmd_fn: Callable[..., Any] | None = None,
) -> None:
    if run_cmd_fn is None:
        if env is not None:
            _run_command(
                args,
                task,
                log,
                timeout=timeout,
                cwd=cwd,
                stopping=stopping,
                env=env,
            )
        else:
            from server import comfyui as _comfyui_mod

            _comfyui_mod._run_command(
                args, task, log, timeout=timeout, cwd=cwd, stopping=stopping
            )
        return

    sanitized_env = _sanitized_subprocess_env(env) if env is not None else None
    try:
        sig = inspect.signature(run_cmd_fn)
    except (TypeError, ValueError):
        run_cmd_fn(args, task, log)
        return

    params = list(sig.parameters.values())
    has_var_pos = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in params)
    has_var_kw = any(p.kind == inspect.Parameter.VAR_KEYWORD for p in params)
    pos_params = [
        p
        for p in params
        if p.kind
        in (
            inspect.Parameter.POSITIONAL_ONLY,
            inspect.Parameter.POSITIONAL_OR_KEYWORD,
        )
    ]
    param_names = {p.name for p in params}
    kwargs: dict[str, Any] = {}
    if has_var_kw or "timeout" in param_names:
        kwargs["timeout"] = timeout
    if has_var_kw or "cwd" in param_names:
        kwargs["cwd"] = cwd
    if has_var_kw or "stopping" in param_names:
        kwargs["stopping"] = stopping
    if sanitized_env is not None and (has_var_kw or "env" in param_names):
        kwargs["env"] = sanitized_env

    if has_var_pos or len(pos_params) >= 3:
        run_cmd_fn(args, task, log, **kwargs)
    elif len(pos_params) == 2:
        run_cmd_fn(args, task, **kwargs)
    elif len(pos_params) == 1:
        run_cmd_fn(args, **kwargs)
    else:
        run_cmd_fn(args, task, log)


def _resolve_drive_wheel_cache_root(drive: Any) -> Path | None:
    if drive is None:
        return None
    mode = str(getattr(drive, "mode", "") or "").strip()
    if mode in ("disabled", "rclone_sync"):
        return None
    if mode == "colab_drivefs":
        is_mounted = getattr(drive, "is_colab_drivefs_mounted", None)
        if callable(is_mounted):
            try:
                if not is_mounted():
                    return None
            except Exception:
                return None
        ws_fn = getattr(drive, "_drivefs_workspace_dir", None)
        if not callable(ws_fn):
            return None
        try:
            ws_val = ws_fn()
            if not isinstance(ws_val, (str, os.PathLike)) or not str(ws_val).strip():
                return None
            ws_dir = Path(ws_val)
            folder_dir = ws_dir.parent
            mydrive_dir = folder_dir.parent
            if folder_dir.is_symlink() or ws_dir.is_symlink():
                return None
            if not ws_dir.is_dir():
                if ws_dir.exists():
                    return None
                if not (
                    folder_dir.is_dir()
                    or (
                        not folder_dir.exists()
                        and not mydrive_dir.is_symlink()
                        and mydrive_dir.is_dir()
                    )
                ):
                    return None
                ws_dir.mkdir(parents=True, exist_ok=True)
            if ws_dir.is_symlink() or not ws_dir.is_dir():
                return None
            cache_root = ws_dir / "wheel-cache"
            if cache_root.is_symlink():
                return None
            if cache_root.exists() and not cache_root.is_dir():
                return None
            return cache_root
        except OSError:
            return None
    if mode == "mount":
        is_fuse = getattr(drive, "is_fuse_mounted", None)
        mount_root = getattr(drive, "mount_root", None)
        if (
            not callable(is_fuse)
            or not isinstance(mount_root, (str, os.PathLike))
            or not str(mount_root).strip()
        ):
            return None
        try:
            mount_p = Path(mount_root)
            if not is_fuse() or mount_p.is_symlink() or not mount_p.is_dir():
                return None
            ws_dir = mount_p / "workspace"
            if ws_dir.is_symlink():
                return None
            if not ws_dir.exists():
                ws_dir.mkdir(parents=True, exist_ok=True)
            if ws_dir.is_symlink() or not ws_dir.is_dir():
                return None
            cache_root = ws_dir / "wheel-cache"
            if cache_root.is_symlink():
                return None
            if cache_root.exists() and not cache_root.is_dir():
                return None
            return cache_root
        except Exception:
            return None
    for attr in ("wheel_cache_root", "persistent_cache_root", "cache_root"):
        val = getattr(drive, attr, None)
        if callable(val):
            try:
                val = val()
            except Exception:
                val = None
        if isinstance(val, (str, os.PathLike)) and str(val).strip():
            cand = Path(val)
            try:
                if cand.is_symlink() or cand.parent.is_symlink():
                    continue
                if cand.is_dir() or cand.parent.is_dir():
                    cache_root = cand if cand.name == "wheel-cache" else (cand / "wheel-cache")
                    if not cache_root.is_symlink() and (
                        not cache_root.exists() or cache_root.is_dir()
                    ):
                        return cache_root
            except OSError:
                continue
    return None


def _try_pull_wheel_from_drive(
    drive: Any,
    *,
    cache_key: str,
    local_cache_root: Path,
    local_cache_dir: Path,
    dist_prefixes: tuple[str, ...],
    min_major: int | None = None,
    allow_empty: bool = False,
) -> Path | None:
    try:
        drive_root = _resolve_drive_wheel_cache_root(drive)
        if drive_root is None:
            return None
        drive_cache_dir = drive_root / cache_key
        drive_wheel = _find_cached_wheel(
            drive_cache_dir,
            dist_prefixes=dist_prefixes,
            min_major=min_major,
            reject_symlinks=False,
            allow_empty=allow_empty,
        )
        if drive_wheel is None:
            return None
        wheel_name = drive_wheel.name
        if (
            not wheel_name
            or wheel_name.startswith(".")
            or "/" in wheel_name
            or "\\" in wheel_name
            or ".." in wheel_name
        ):
            return None
        _ensure_dir(local_cache_root, "加速包缓存根目录")
        _ensure_dir(local_cache_dir, "加速包缓存目录")
        return _copy_wheel_atomic(drive_wheel, local_cache_dir / wheel_name)
    except Exception as exc:
        if _is_cancelled_exception(exc):
            raise
        return None


def _try_mirror_wheel_to_drive(
    drive: Any,
    *,
    cache_key: str,
    wheel_path: Path,
    force: bool = False,
) -> bool:
    try:
        if wheel_path.is_symlink() or not wheel_path.is_file():
            return False
        drive_root = _resolve_drive_wheel_cache_root(drive)
        if drive_root is None:
            return False
        _ensure_dir(drive_root, "云盘加速包缓存根目录")
        drive_cache_dir = drive_root / cache_key
        _ensure_dir(drive_cache_dir, "云盘加速包缓存目录")
        drive_dst = drive_cache_dir / wheel_path.name
        if drive_dst.is_symlink():
            return False
        if not force and drive_dst.is_file():
            try:
                if drive_dst.stat().st_size == wheel_path.stat().st_size:
                    return True
            except OSError:
                pass
        _copy_wheel_atomic(wheel_path, drive_dst)
        return True
    except Exception as exc:
        if _is_cancelled_exception(exc):
            raise
        return False


def _unlink_drive_cached_wheel(
    drive: Any,
    *,
    cache_key: str,
    wheel_name: str,
) -> None:
    if (
        not wheel_name
        or wheel_name.startswith(".")
        or "/" in wheel_name
        or "\\" in wheel_name
        or ".." in wheel_name
    ):
        return
    try:
        drive_root = _resolve_drive_wheel_cache_root(drive)
        if drive_root is None or drive_root.is_symlink():
            return
        drive_cache_dir = drive_root / cache_key
        if drive_cache_dir.is_symlink() or not drive_cache_dir.is_dir():
            return
        drive_wheel = drive_cache_dir / wheel_name
        if not drive_wheel.is_symlink() and drive_wheel.is_file():
            drive_wheel.unlink(missing_ok=True)
    except Exception as exc:
        if _is_cancelled_exception(exc):
            raise


def _checkout_sageattention_source(
    task,
    src_dir: Path,
    build_log: Path,
    *,
    stopping=None,
    run_cmd_fn: Callable[..., Any] | None = None,
) -> None:
    if run_cmd_fn is None and not shutil.which("git"):
        raise RuntimeError("此远端缺少 Git，无法拉取 SageAttention 源码")
    build_root = src_dir.parent
    _ensure_dir(build_root, "加速包构建目录")
    if src_dir.is_symlink():
        raise ValueError("SageAttention 源码目录不能是符号链接")
    git_dir = src_dir / ".git"
    if git_dir.is_symlink():
        raise ValueError("SageAttention .git 目录不能是符号链接")
    if git_dir.exists() and not git_dir.is_dir():
        shutil.rmtree(src_dir, ignore_errors=True)
    _ensure_dir(src_dir, "SageAttention 源码目录")

    if git_dir.is_dir():
        for lock_file in git_dir.rglob("*.lock"):
            try:
                lock_file.unlink(missing_ok=True)
            except OSError:
                pass
        try:
            _invoke_run_cmd_compat(
                [
                    "git",
                    "-C",
                    str(src_dir),
                    "remote",
                    "set-url",
                    "origin",
                    SAGEATTN_REPO_URL,
                ],
                task,
                build_log,
                env={},
                stopping=stopping,
                run_cmd_fn=run_cmd_fn,
            )
        except RuntimeError as exc:
            if _is_cancelled_exception(exc):
                raise
            _invoke_run_cmd_compat(
                [
                    "git",
                    "-C",
                    str(src_dir),
                    "remote",
                    "add",
                    "origin",
                    SAGEATTN_REPO_URL,
                ],
                task,
                build_log,
                env={},
                stopping=stopping,
                run_cmd_fn=run_cmd_fn,
            )
    else:
        _invoke_run_cmd_compat(
            ["git", "init", str(src_dir)],
            task,
            build_log,
            env={},
            stopping=stopping,
            run_cmd_fn=run_cmd_fn,
        )
        _invoke_run_cmd_compat(
            [
                "git",
                "-C",
                str(src_dir),
                "remote",
                "add",
                "origin",
                SAGEATTN_REPO_URL,
            ],
            task,
            build_log,
            env={},
            stopping=stopping,
            run_cmd_fn=run_cmd_fn,
        )

    _invoke_run_cmd_compat(
        [
            "git",
            "-C",
            str(src_dir),
            "fetch",
            "--depth",
            "1",
            "origin",
            SAGEATTN_COMMIT,
        ],
        task,
        build_log,
        env={},
        stopping=stopping,
        run_cmd_fn=run_cmd_fn,
    )
    _invoke_run_cmd_compat(
        [
            "git",
            "-C",
            str(src_dir),
            "checkout",
            "--force",
            "--detach",
            SAGEATTN_COMMIT,
        ],
        task,
        build_log,
        env={},
        stopping=stopping,
        run_cmd_fn=run_cmd_fn,
    )
    _invoke_run_cmd_compat(
        ["git", "-C", str(src_dir), "clean", "-fdx"],
        task,
        build_log,
        env={},
        stopping=stopping,
        run_cmd_fn=run_cmd_fn,
    )


def ensure_sageattention_wheels_shared(
    task: Any = None,
    *,
    base_root: Path | str | None = None,
    runtime: Any = None,
    drive: Any = None,
    gpu_caps: dict[str, Any] | None = None,
    gpu_info: dict[str, Any] | None = None,
    allow_sage3: bool = True,
    stopping: threading.Event | None = None,
    run_cmd_fn: Callable[..., Any] | None = None,
    build_log: Path | str | None = None,
) -> dict[str, Any]:
    """Ensure SageAttention 2 (and SageAttention 3 on Blackwell) wheels are ready.

    Shared across ComfyUI, LongCat 1.6 native avatar, and diffusion VSR engines
    without requiring ComfyUI to be installed or started. Checks fast already-installed
    state first, then local NVMe wheel cache and optional Google Drive mirror before
    falling back to a pinned source build installed into ``sys.executable`` (inherited
    by child virtualenvs via ``--system-site-packages`` and parent-venv ``.pth``).
    Never raises on build/install/cache errors (except task cancellation).
    """
    eff_stopping = stopping if stopping is not None else getattr(runtime, "stopping", None)

    def _check_task() -> None:
        if task is not None and callable(getattr(task, "check", None)):
            task.check()
        if isinstance(eff_stopping, threading.Event) and eff_stopping.is_set():
            from server.runtime import ColabOneCancelledError

            raise ColabOneCancelledError("实例正在停止，已取消远端加速包准备")

    def _note(message: str, level: str = "info") -> None:
        note_fn = getattr(task, "note", None) if task is not None else None
        if not callable(note_fn):
            return
        if level == "info":
            note_fn(message)
            return
        try:
            note_fn(message, level=level)
        except TypeError:
            note_fn(message, level)

    _check_task()
    if gpu_caps is None:
        if gpu_info is not None:
            raw_gpu = gpu_info
        else:
            gpu_fn = getattr(runtime, "gpu_info", None) if runtime is not None else None
            raw_gpu = (gpu_fn() if callable(gpu_fn) else None) or None
        caps = probe_gpu_acceleration_capabilities(raw_gpu)
    else:
        caps = dict(gpu_caps)

    cc_major = _safe_nonneg_int(caps.get("cc_major", 0))
    cc_minor = _safe_nonneg_int(caps.get("cc_minor", 0))
    cc_tuple = (cc_major, cc_minor)
    caps["installed_any"] = False

    if cc_major < 8:
        _note(
            f"当前 GPU 计算能力为 sm_{cc_major}{cc_minor}（低于 sm_80），跳过 SageAttention 加速包安装"
        )
        return caps

    need_sage2 = not bool(caps.get("sageattn_available"))
    is_blackwell = is_blackwell_cc(cc_major, cc_minor)
    need_sage3 = bool(
        allow_sage3 and is_blackwell and not bool(caps.get("sageattn3_available"))
    )

    if not need_sage2 and not need_sage3:
        return caps

    resolved_drive = drive if drive is not None else getattr(runtime, "drive", None)
    if base_root is not None:
        if not isinstance(base_root, (str, os.PathLike)) or not str(base_root).strip():
            raise ValueError("加速包缓存根目录不能为空")
        resolved_base_root = Path(base_root)
    else:
        runtime_root = getattr(runtime, "root", None) if runtime is not None else None
        if isinstance(runtime_root, (str, os.PathLike)) and str(runtime_root).strip():
            resolved_base_root = Path(runtime_root)
        else:
            resolved_base_root = Path(tempfile.gettempdir()) / "colabone-remote"

    if resolved_base_root.is_symlink():
        raise ValueError(f"加速包缓存根目录不能是符号链接：{resolved_base_root.name}")

    allow_empty_wheel = run_cmd_fn is not None
    nvcc_version = str(caps.get("nvcc_version") or "").strip()
    nvcc_tuple = _parse_version_tuple(nvcc_version)
    nvcc_present = len(nvcc_tuple) >= 2 and (
        run_cmd_fn is not None or bool(shutil.which("nvcc"))
    )

    py_tag = f"{sys.version_info.major}{sys.version_info.minor}"
    torch_tag = str(caps.get("torch_version") or "").strip() or "unknown"
    cu_tag = nvcc_version or str(caps.get("cuda_version") or "").strip() or "unknown"
    cache_key = _sanitize_cache_key(
        f"sageattention-{SAGEATTN_COMMIT[:10]}-py{py_tag}-torch{torch_tag}-cu{cu_tag}-sm{cc_major}{cc_minor}"
    )

    local_cache_root = resolved_base_root / "wheel-cache"
    local_cache_dir = local_cache_root / cache_key
    build_src_dir = resolved_base_root / "wheel-build" / "SageAttention"
    if build_log is not None:
        if not isinstance(build_log, (str, os.PathLike)) or not str(build_log).strip():
            raise ValueError("加速包构建日志路径不能为空")
        eff_build_log = Path(build_log)
    else:
        eff_build_log = resolved_base_root / "comfyui-sageattention-build.log"
    if eff_build_log.parent.is_symlink():
        raise ValueError(f"运行日志目录不能是符号链接：{eff_build_log.parent.name}")
    if eff_build_log.is_symlink():
        raise ValueError(f"运行日志文件不能是符号链接：{eff_build_log.name}")

    build_env = _sageattention_build_env(cc_major, cc_minor)
    sage3_dist_prefixes = (
        "sageattn3",
        "sageattention3",
        "sageattention3_blackwell",
    )

    installed_any = False
    source_checked_out = False

    def _build_wheel_into_cache(
        src_path: Path,
        *,
        dist_prefixes: tuple[str, ...],
        min_major: int | None,
        label: str,
    ) -> Path:
        """Build a wheel inside an isolated staging dir and atomically promote into local_cache_dir."""
        _ensure_dir(local_cache_root, "加速包缓存根目录")
        _ensure_dir(local_cache_dir, "加速包缓存目录")
        if run_cmd_fn is not None:
            _invoke_run_cmd_compat(
                [
                    sys.executable,
                    "-m",
                    "pip",
                    "wheel",
                    "--no-input",
                    "--disable-pip-version-check",
                    "--no-build-isolation",
                    "--no-deps",
                    "--wheel-dir",
                    str(local_cache_dir),
                    str(src_path),
                ],
                task,
                eff_build_log,
                env=build_env,
                stopping=eff_stopping,
                run_cmd_fn=run_cmd_fn,
            )
            built = _find_cached_wheel(
                local_cache_dir,
                dist_prefixes=dist_prefixes,
                min_major=min_major,
                reject_symlinks=True,
                allow_empty=True,
            )
            if built is None:
                raise RuntimeError(f"未在构建目录中找到生成的 {label} wheel 文件")
            return built

        stage_parent = resolved_base_root / "wheel-build"
        _ensure_dir(stage_parent, "加速包构建目录")
        stage_dir = stage_parent / f"stage-{uuid.uuid4().hex[:10]}"
        stage_dir.mkdir(parents=True, exist_ok=False)
        try:
            _invoke_run_cmd_compat(
                [
                    sys.executable,
                    "-m",
                    "pip",
                    "wheel",
                    "--no-input",
                    "--disable-pip-version-check",
                    "--no-build-isolation",
                    "--no-deps",
                    "--wheel-dir",
                    str(stage_dir),
                    str(src_path),
                ],
                task,
                eff_build_log,
                env=build_env,
                stopping=eff_stopping,
                run_cmd_fn=run_cmd_fn,
            )
            staged_wheel = _find_cached_wheel(
                stage_dir,
                dist_prefixes=dist_prefixes,
                min_major=min_major,
                reject_symlinks=True,
                allow_empty=False,
            )
            if staged_wheel is None:
                raise RuntimeError(f"未在构建目录中找到生成的 {label} wheel 文件")
            return _copy_wheel_atomic(
                staged_wheel, local_cache_dir / staged_wheel.name
            )
        finally:
            shutil.rmtree(stage_dir, ignore_errors=True)

    def _ensure_source_checked_out() -> None:
        nonlocal source_checked_out
        if source_checked_out:
            return
        _check_task()
        _checkout_sageattention_source(
            task,
            build_src_dir,
            eff_build_log,
            stopping=eff_stopping,
            run_cmd_fn=run_cmd_fn,
        )
        source_checked_out = True

    def _install_wheel(wheel_path: Path) -> None:
        _check_task()
        if wheel_path.is_symlink() or not wheel_path.is_file():
            raise ValueError(f"加速包 wheel 文件不能是符号链接：{wheel_path.name}")
        _invoke_run_cmd_compat(
            [
                sys.executable,
                "-m",
                "pip",
                "install",
                "--no-input",
                "--disable-pip-version-check",
                "--no-deps",
                "--force-reinstall",
                str(wheel_path),
            ],
            task,
            eff_build_log,
            env={},
            stopping=eff_stopping,
            run_cmd_fn=run_cmd_fn,
        )
        importlib.invalidate_caches()

    def _refresh_sage2_state(wheel_path: Path | None = None) -> None:
        if run_cmd_fn is not None:
            caps["sageattn_available"] = True
            if wheel_path is not None:
                ver = _wheel_version_from_filename(wheel_path.name)
                if ver:
                    caps["sageattn_version"] = ver
            return
        live_ver = ""
        try:
            live_ver = str(
                importlib.metadata.version("sageattention") or ""
            ).strip()
        except Exception:
            live_ver = ""
        live_tuple = _parse_version_tuple(live_ver)
        caps["sageattn_version"] = live_ver
        caps["sageattn_available"] = bool(
            _has_module_spec("sageattention")
            and bool(live_tuple)
            and live_tuple[0] >= 2
        )

    def _refresh_sage3_state() -> None:
        if run_cmd_fn is not None:
            caps["sageattn3_available"] = True
            return
        caps["sageattn3_available"] = bool(
            _has_module_spec("sageattn3") and is_blackwell
        )

    global _SAGEATTN_INSTALL_GENERATION
    with _SAGEATTN_WHEEL_LOCK:
        _check_task()
        # In live execution, always invalidate import caches and re-probe live
        # availability inside the lock so a concurrent build that just completed
        # is reused immediately without triggering a duplicate --force-reinstall.
        if run_cmd_fn is None:
            importlib.invalidate_caches()
            _refresh_sage2_state()
            _refresh_sage3_state()
            need_sage2 = not bool(caps.get("sageattn_available"))
            need_sage3 = bool(
                allow_sage3 and is_blackwell and not bool(caps.get("sageattn3_available"))
            )
            if not need_sage2 and not need_sage3:
                return caps

        try:
            if local_cache_root.is_symlink():
                raise ValueError("加速包缓存根目录不能是符号链接")
            if local_cache_dir.is_symlink():
                raise ValueError("加速包缓存目录不能是符号链接")

            if need_sage2:
                _check_task()
                building_sage2 = False
                try:
                    pulled_sage2_from_drive = False
                    force_mirror_sage2 = False
                    wheel_path = _find_cached_wheel(
                        local_cache_dir,
                        dist_prefixes=("sageattention",),
                        min_major=2,
                        reject_symlinks=True,
                        allow_empty=allow_empty_wheel,
                    )
                    if wheel_path is None:
                        wheel_path = _try_pull_wheel_from_drive(
                            resolved_drive,
                            cache_key=cache_key,
                            local_cache_root=local_cache_root,
                            local_cache_dir=local_cache_dir,
                            dist_prefixes=("sageattention",),
                            min_major=2,
                            allow_empty=allow_empty_wheel,
                        )
                        if wheel_path is not None:
                            pulled_sage2_from_drive = True

                    cached_sage2_installed = False
                    if wheel_path is not None:
                        _note(
                            f"复用已缓存的 SageAttention 2 加速包（sm_{cc_major}{cc_minor}）"
                        )
                        try:
                            _install_wheel(wheel_path)
                            cached_sage2_installed = True
                        except Exception as cache_exc:
                            if _is_cancelled_exception(cache_exc):
                                raise
                            force_mirror_sage2 = True
                            if not wheel_path.is_symlink():
                                try:
                                    wheel_path.unlink(missing_ok=True)
                                except OSError:
                                    pass
                            if pulled_sage2_from_drive:
                                _unlink_drive_cached_wheel(
                                    resolved_drive,
                                    cache_key=cache_key,
                                    wheel_name=wheel_path.name,
                                )
                            _note(
                                f"已缓存的 SageAttention 2 加速包安装失败，已清理损坏缓存并尝试重新构建：{cache_exc}",
                                level="warning",
                            )
                        if cached_sage2_installed:
                            _refresh_sage2_state(wheel_path)
                            if run_cmd_fn is not None or bool(
                                caps.get("sageattn_available")
                            ):
                                _try_mirror_wheel_to_drive(
                                    resolved_drive,
                                    cache_key=cache_key,
                                    wheel_path=wheel_path,
                                )
                                installed_any = True
                                if run_cmd_fn is None:
                                    _SAGEATTN_FAILED_BUILDS.discard(
                                        (cache_key, "sage2")
                                    )
                            else:
                                cached_sage2_installed = False
                                force_mirror_sage2 = True
                                if not wheel_path.is_symlink():
                                    try:
                                        wheel_path.unlink(missing_ok=True)
                                    except OSError:
                                        pass
                                if pulled_sage2_from_drive:
                                    _unlink_drive_cached_wheel(
                                        resolved_drive,
                                        cache_key=cache_key,
                                        wheel_name=wheel_path.name,
                                    )
                                _note(
                                    f"已缓存的 SageAttention 2 加速包安装后未检测到可用模块（sm_{cc_major}{cc_minor}），已清理缓存并尝试重新构建",
                                    level="warning",
                                )

                    if not cached_sage2_installed:
                        min_nvcc = _min_nvcc_for_sage2(cc_major, cc_minor)
                        if not nvcc_present or nvcc_tuple[:2] < min_nvcc:
                            _note(
                                f"未检测到满足 sm_{cc_major}{cc_minor} 要求的 nvcc"
                                f"（需要 CUDA >= {min_nvcc[0]}.{min_nvcc[1]}），跳过 SageAttention 2 源码构建"
                            )
                        elif (
                            run_cmd_fn is None
                            and (cache_key, "sage2") in _SAGEATTN_FAILED_BUILDS
                        ):
                            _note(
                                f"本次会话中 SageAttention 2（sm_{cc_major}{cc_minor}）源码构建曾失败，跳过重复构建"
                            )
                        else:
                            building_sage2 = True
                            _ensure_dir(local_cache_root, "加速包缓存根目录")
                            _ensure_dir(local_cache_dir, "加速包缓存目录")
                            _set_task_attr(
                                task,
                                message=f"构建 SageAttention 2 加速包 · sm_{cc_major}{cc_minor}",
                            )
                            _note(
                                f"正在为 sm_{cc_major}{cc_minor} 构建 SageAttention 2 加速包（首次构建后将缓存复用）"
                            )
                            _ensure_source_checked_out()
                            _check_task()
                            built_wheel = _build_wheel_into_cache(
                                build_src_dir,
                                dist_prefixes=("sageattention",),
                                min_major=2,
                                label="SageAttention 2",
                            )
                            _install_wheel(built_wheel)
                            _refresh_sage2_state(built_wheel)
                            if run_cmd_fn is not None or bool(
                                caps.get("sageattn_available")
                            ):
                                _try_mirror_wheel_to_drive(
                                    resolved_drive,
                                    cache_key=cache_key,
                                    wheel_path=built_wheel,
                                    force=force_mirror_sage2,
                                )
                                installed_any = True
                                if run_cmd_fn is None:
                                    _SAGEATTN_FAILED_BUILDS.discard(
                                        (cache_key, "sage2")
                                    )
                                _note(
                                    f"SageAttention 2 加速包构建并安装完成（sm_{cc_major}{cc_minor}）"
                                )
                            else:
                                if not built_wheel.is_symlink():
                                    try:
                                        built_wheel.unlink(missing_ok=True)
                                    except OSError:
                                        pass
                                if run_cmd_fn is None:
                                    _SAGEATTN_FAILED_BUILDS.add((cache_key, "sage2"))
                                _note(
                                    f"SageAttention 2 加速包安装后未检测到可用模块（sm_{cc_major}{cc_minor}），将自动回退",
                                    level="warning",
                                )
                except Exception as exc:
                    if _is_cancelled_exception(exc):
                        raise
                    if building_sage2 and run_cmd_fn is None:
                        _SAGEATTN_FAILED_BUILDS.add((cache_key, "sage2"))
                    _note(
                        f"SageAttention 加速包构建或安装跳过，将自动回退：{exc}",
                        level="warning",
                    )

            if need_sage3 and bool(caps.get("sageattn_available")):
                _check_task()
                building_sage3 = False
                try:
                    torch_tuple = _parse_version_tuple(caps.get("torch_version") or "")
                    pulled_sage3_from_drive = False
                    force_mirror_sage3 = False
                    sage3_wheel = _find_cached_wheel(
                        local_cache_dir,
                        dist_prefixes=sage3_dist_prefixes,
                        min_major=None,
                        reject_symlinks=True,
                        allow_empty=allow_empty_wheel,
                    )
                    if sage3_wheel is None:
                        sage3_wheel = _try_pull_wheel_from_drive(
                            resolved_drive,
                            cache_key=cache_key,
                            local_cache_root=local_cache_root,
                            local_cache_dir=local_cache_dir,
                            dist_prefixes=sage3_dist_prefixes,
                            min_major=None,
                            allow_empty=allow_empty_wheel,
                        )
                        if sage3_wheel is not None:
                            pulled_sage3_from_drive = True

                    cached_sage3_installed = False
                    if sage3_wheel is not None:
                        _note(
                            f"复用已缓存的 SageAttention 3 加速包（sm_{cc_major}{cc_minor}）"
                        )
                        try:
                            _install_wheel(sage3_wheel)
                            cached_sage3_installed = True
                        except Exception as cache_exc:
                            if _is_cancelled_exception(cache_exc):
                                raise
                            force_mirror_sage3 = True
                            if not sage3_wheel.is_symlink():
                                try:
                                    sage3_wheel.unlink(missing_ok=True)
                                except OSError:
                                    pass
                            if pulled_sage3_from_drive:
                                _unlink_drive_cached_wheel(
                                    resolved_drive,
                                    cache_key=cache_key,
                                    wheel_name=sage3_wheel.name,
                                )
                            _note(
                                f"已缓存的 SageAttention 3 加速包安装失败，已清理损坏缓存并尝试重新构建：{cache_exc}",
                                level="warning",
                            )
                        if cached_sage3_installed:
                            _refresh_sage3_state()
                            if run_cmd_fn is not None or bool(
                                caps.get("sageattn3_available")
                            ):
                                _try_mirror_wheel_to_drive(
                                    resolved_drive,
                                    cache_key=cache_key,
                                    wheel_path=sage3_wheel,
                                )
                                installed_any = True
                                if run_cmd_fn is None:
                                    _SAGEATTN_FAILED_BUILDS.discard(
                                        (cache_key, "sage3")
                                    )
                            else:
                                cached_sage3_installed = False
                                force_mirror_sage3 = True
                                if not sage3_wheel.is_symlink():
                                    try:
                                        sage3_wheel.unlink(missing_ok=True)
                                    except OSError:
                                        pass
                                if pulled_sage3_from_drive:
                                    _unlink_drive_cached_wheel(
                                        resolved_drive,
                                        cache_key=cache_key,
                                        wheel_name=sage3_wheel.name,
                                    )
                                _note(
                                    f"已缓存的 SageAttention 3 加速包安装后未检测到可用模块（sm_{cc_major}{cc_minor}），已清理缓存并尝试重新构建",
                                    level="warning",
                                )

                    if not cached_sage3_installed:
                        if not nvcc_present or nvcc_tuple[:2] < (12, 8):
                            _note(
                                f"未检测到满足 Blackwell sm_{cc_major}{cc_minor} 要求的 nvcc"
                                "（需要 CUDA >= 12.8），跳过 SageAttention 3 源码构建"
                            )
                        elif len(torch_tuple) >= 2 and torch_tuple[:2] < (2, 8):
                            _note(
                                f"当前 PyTorch {caps.get('torch_version')} 低于 2.8，跳过 SageAttention 3 源码构建（已启用 SageAttention 2）"
                            )
                        elif (
                            run_cmd_fn is None
                            and (cache_key, "sage3") in _SAGEATTN_FAILED_BUILDS
                        ):
                            _note(
                                f"本次会话中 SageAttention 3（sm_{cc_major}{cc_minor}）源码构建曾失败，跳过重复构建"
                            )
                        else:
                            building_sage3 = True
                            _ensure_dir(local_cache_root, "加速包缓存根目录")
                            _ensure_dir(local_cache_dir, "加速包缓存目录")
                            _set_task_attr(
                                task,
                                message=f"构建 SageAttention 3 加速包 · sm_{cc_major}{cc_minor}",
                            )
                            _note(
                                f"正在为 Blackwell sm_{cc_major}{cc_minor} 构建 SageAttention 3 加速包（首次构建后将缓存复用）"
                            )
                            _ensure_source_checked_out()
                            sage3_src_dir = build_src_dir / "sageattention3_blackwell"
                            if sage3_src_dir.is_symlink():
                                raise ValueError("SageAttention 3 源码目录不能是符号链接")
                            if run_cmd_fn is None and not sage3_src_dir.is_dir():
                                raise RuntimeError(
                                    "SageAttention 仓库中未找到 sageattention3_blackwell 子目录"
                                )
                            _check_task()
                            built_sage3 = _build_wheel_into_cache(
                                sage3_src_dir,
                                dist_prefixes=sage3_dist_prefixes,
                                min_major=None,
                                label="SageAttention 3",
                            )
                            _install_wheel(built_sage3)
                            _refresh_sage3_state()
                            if run_cmd_fn is not None or bool(
                                caps.get("sageattn3_available")
                            ):
                                _try_mirror_wheel_to_drive(
                                    resolved_drive,
                                    cache_key=cache_key,
                                    wheel_path=built_sage3,
                                    force=force_mirror_sage3,
                                )
                                installed_any = True
                                if run_cmd_fn is None:
                                    _SAGEATTN_FAILED_BUILDS.discard(
                                        (cache_key, "sage3")
                                    )
                                _note(
                                    f"SageAttention 3 加速包构建并安装完成（sm_{cc_major}{cc_minor}）"
                                )
                            else:
                                if not built_sage3.is_symlink():
                                    try:
                                        built_sage3.unlink(missing_ok=True)
                                    except OSError:
                                        pass
                                if run_cmd_fn is None:
                                    _SAGEATTN_FAILED_BUILDS.add((cache_key, "sage3"))
                                _note(
                                    f"SageAttention 3 加速包安装后未检测到可用模块（sm_{cc_major}{cc_minor}），将自动回退",
                                    level="warning",
                                )
                except Exception as exc:
                    if _is_cancelled_exception(exc):
                        raise
                    if building_sage3 and run_cmd_fn is None:
                        _SAGEATTN_FAILED_BUILDS.add((cache_key, "sage3"))
                    _note(
                        f"SageAttention 加速包构建或安装跳过，将自动回退：{exc}",
                        level="warning",
                    )
        except Exception as exc:
            if _is_cancelled_exception(exc):
                raise
            _note(
                f"SageAttention 加速包构建或安装跳过，将自动回退：{exc}",
                level="warning",
            )

        if installed_any:
            _SAGEATTN_INSTALL_GENERATION += 1

    caps["installed_any"] = installed_any
    return caps


class ColabOneComfyPackManager:
    """Manages isolated ComfyUI custom-node packs and verified model weights."""

    def __init__(
        self,
        runtime,
        install_dir: Path,
        models_dir: Path,
        run_cmd_fn=None,
        download_fn=None,
    ):
        self.runtime = runtime
        self.install_dir = Path(install_dir)
        self.models_dir = Path(models_dir)
        runtime_root = getattr(runtime, "root", None)
        if isinstance(runtime_root, (str, os.PathLike)) and str(runtime_root).strip():
            self.root = Path(runtime_root)
        else:
            self.root = self.install_dir.parent
        self.packs_root = self.root / "comfyui-packs"
        self.run_cmd_fn = run_cmd_fn
        self.download_fn = download_fn
        self.lock = threading.RLock()
        self._active_pack_id = ""
        self.active_pack_signature = ""
        self._pending_pip_restart = False
        self._seen_sageattn_generation = _current_sageattn_install_generation()
        self._load_active_state_from_disk()

    @property
    def active_pack_id(self) -> str:
        with self.lock:
            return self._active_pack_id

    @active_pack_id.setter
    def active_pack_id(self, value: str) -> None:
        with self.lock:
            self._active_pack_id = str(value or "")

    def needs_pip_restart(self) -> bool:
        """Return True if pip packages or out-of-band SageAttention wheels require a restart."""
        current_gen = _current_sageattn_install_generation()
        with self.lock:
            if current_gen > self._seen_sageattn_generation:
                self._pending_pip_restart = True
            return bool(self._pending_pip_restart)

    def clear_pending_pip_restart(self, *, generation: int | None = None) -> None:
        """Clear pending pip-restart flag after caller restarts ComfyUI."""
        cur_gen = _current_sageattn_install_generation()
        target_gen = cur_gen if generation is None else min(int(generation), cur_gen)
        with self.lock:
            self._seen_sageattn_generation = max(
                self._seen_sageattn_generation, target_gen
            )
            if generation is None or cur_gen <= self._seen_sageattn_generation:
                self._pending_pip_restart = False

    def _managed_manifest_path(self) -> Path:
        return self.install_dir / "custom_nodes" / ".colabone-managed-nodes.json"

    def _read_managed_state(self) -> dict[str, Any]:
        manifest_path = self._managed_manifest_path()
        if manifest_path.is_symlink() or not manifest_path.is_file():
            return {"pack_id": "", "signature": "", "nodes": {}}
        try:
            data = json.loads(manifest_path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return {"pack_id": "", "signature": "", "nodes": {}}
        if not isinstance(data, dict):
            return {"pack_id": "", "signature": "", "nodes": {}}
        raw_nodes = data.get("nodes")
        nodes: dict[str, str] = {}
        if isinstance(raw_nodes, dict):
            for key, val in raw_nodes.items():
                if isinstance(key, str) and isinstance(val, str):
                    nodes[key] = val
        elif isinstance(raw_nodes, list):
            for key in raw_nodes:
                if isinstance(key, str):
                    nodes[key] = ""
        return {
            "pack_id": str(data.get("pack_id") or ""),
            "signature": str(data.get("signature") or ""),
            "nodes": nodes,
        }

    def _load_active_state_from_disk(self) -> None:
        state = self._read_managed_state()
        sig = state["signature"]
        nodes = state["nodes"]
        if not sig or not nodes:
            return
        custom_nodes_dir = self.install_dir / "custom_nodes"
        for node_name, expected_target in nodes.items():
            try:
                safe_name = _validate_safe_node_name(node_name)
            except ValueError:
                return
            link_path = custom_nodes_dir / safe_name
            if not link_path.is_symlink() or not link_path.is_dir():
                return
            if expected_target:
                try:
                    if str(link_path.resolve()) != str(Path(expected_target).resolve()):
                        return
                except OSError:
                    return
        self._active_pack_id = state["pack_id"]
        self.active_pack_signature = sig

    def model_target_path(self, model_ref: dict) -> Path:
        ref = _as_dict(model_ref)
        target_dir = str(ref.get("target_dir") or "").strip()
        if target_dir not in _COMFY_MODEL_SUBDIR_SET:
            raise ValueError(f"不支持的模型子目录：{target_dir!r}")

        raw_path = ref.get("path")
        if not isinstance(raw_path, str) or not raw_path:
            raise ValueError("模型文件路径不能为空")
        posix = PurePosixPath(raw_path)
        if (
            posix.is_absolute()
            or ".." in posix.parts
            or "." in posix.parts
            or any(part.startswith(".") for part in posix.parts)
            or ":" in raw_path
            or "\\" in raw_path
            or "\x00" in raw_path
            or raw_path.endswith("/")
            or len(raw_path) > 512
        ):
            raise ValueError(f"模型文件路径不合法：{raw_path!r}")

        filename = posix.name
        if (
            not filename
            or filename in (".", "..")
            or filename.startswith(".")
            or "/" in filename
            or "\\" in filename
            or ":" in filename
            or "\x00" in filename
        ):
            raise ValueError(f"模型文件名不合法：{filename!r}")

        lower_name = filename.lower()
        if (
            lower_name == "verified.json"
            or lower_name.endswith(".part")
            or lower_name.endswith(".ranges.json")
        ):
            raise ValueError(f"模型文件名与保留控制文件冲突：{filename!r}")

        suffix = posix.suffix.lower()
        if suffix not in _COMFY_MODEL_EXTENSIONS or len(filename) <= len(suffix):
            raise ValueError(f"不支持的模型文件扩展名：{filename!r}")

        if self.models_dir.is_symlink():
            raise ValueError("ComfyUI 模型目录不能是符号链接")
        subdir = self.models_dir / target_dir
        if subdir.is_symlink():
            raise ValueError(f"ComfyUI 模型子目录 {target_dir} 不能是符号链接")
        target = subdir / filename
        if target.is_symlink():
            raise ValueError(f"模型文件不能是符号链接：{filename}")
        return target

    @staticmethod
    def _read_verified_receipt(subdir: Path) -> dict[str, str]:
        receipt_path = subdir / "verified.json"
        if receipt_path.is_symlink() or not receipt_path.is_file():
            return {}
        try:
            data = json.loads(receipt_path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return {}
        if not isinstance(data, dict):
            return {}
        return {
            str(k): str(v).lower()
            for k, v in data.items()
            if isinstance(k, str) and isinstance(v, str)
        }

    def _record_verified(self, subdir: Path, filename: str, sha256: str) -> None:
        with self.lock:
            receipt_path = subdir / "verified.json"
            if receipt_path.is_symlink():
                raise ValueError("verified.json 不能是符号链接")
            receipt = self._read_verified_receipt(subdir)
            receipt[filename] = sha256.lower()
            atomic_json(receipt_path, receipt)

    def is_model_verified(self, model_ref: dict) -> bool:
        ref = _as_dict(model_ref)
        target = self.model_target_path(ref)
        expected_sha = str(ref.get("sha256") or "").strip().lower()
        try:
            expected_size = int(ref.get("size") or 0)
        except (TypeError, ValueError):
            return False
        if not _SHA256_RE.fullmatch(expected_sha) or expected_size <= 0:
            return False
        receipt = self._read_verified_receipt(target.parent)
        if receipt.get(target.name) != expected_sha:
            return False
        try:
            if target.is_symlink() or not target.is_file():
                return False
            return target.stat().st_size == expected_size
        except OSError:
            return False

    def _deduplicate_models(self, models: list[dict]) -> list[tuple[dict[str, Any], Path]]:
        seen: dict[Path, tuple[str, int]] = {}
        ordered: list[tuple[dict[str, Any], Path]] = []
        for item in models or []:
            ref = _as_dict(item)
            target = self.model_target_path(ref)
            sha = str(ref.get("sha256") or "").strip().lower()
            size = max(0, int(ref.get("size") or 0))
            if target in seen:
                prev_sha, prev_size = seen[target]
                if prev_sha != sha or prev_size != size:
                    raise ValueError(f"存在同路径但校验信息冲突的模型声明：{target.name}")
                continue
            seen[target] = (sha, size)
            ordered.append((ref, target))
        return ordered

    def models_total_bytes(self, models: list[dict]) -> int:
        total = 0
        for ref, _ in self._deduplicate_models(models):
            total += max(0, int(ref.get("size") or 0))
        return total

    def models_cached_bytes(self, models: list[dict]) -> int:
        total = 0
        for ref, target in self._deduplicate_models(models):
            size = max(0, int(ref.get("size") or 0))
            sha256 = str(ref.get("sha256") or "").strip().lower()
            if size <= 0:
                continue
            if self.is_model_verified(ref):
                total += size
                continue
            partial = target.with_name(target.name + ".part")
            if partial.is_symlink():
                continue
            try:
                total += min(size, max(0, int(cached_bytes(partial, size, sha256))))
            except OSError:
                continue
        return total

    def check_preflight(self, workflow_template: dict) -> None:
        wf = _as_dict(workflow_template)
        min_vram_mib = int(wf.get("min_vram_mib") or 0)
        recommended_gpu = str(wf.get("recommended_gpu") or "").strip()

        if min_vram_mib > 0:
            gpu_fn = getattr(self.runtime, "gpu_info", None)
            gpu = (gpu_fn() if callable(gpu_fn) else None) or {}
            total_mib = int(gpu.get("total_mib") or 0)
            gpu_name = str(gpu.get("name") or "").strip()
            if total_mib <= 0:
                raise RuntimeError(
                    "当前远端实例未检测到可用 NVIDIA GPU，无法运行该工作流"
                )
            if total_mib < min_vram_mib:
                current_desc = (
                    f"{gpu_name}（{_format_gb_from_mib(total_mib)}）"
                    if gpu_name
                    else _format_gb_from_mib(total_mib)
                )
                rec_hint = f"，推荐切换至 {recommended_gpu}" if recommended_gpu else ""
                raise RuntimeError(
                    f"当前远端 GPU 显存不足：此工作流至少需要 {_format_gb_from_mib(min_vram_mib)} 显存，"
                    f"当前 GPU 为 {current_desc}{rec_hint}"
                )

        models = list(wf.get("models") or [])
        min_disk_mib = int(wf.get("min_disk_mib") or 0)
        cached_model_bytes = self.models_cached_bytes(models)
        remaining_bytes = max(0, self.models_total_bytes(models) - cached_model_bytes)
        effective_min_disk = max(0, min_disk_mib * 1024**2 - cached_model_bytes)
        if min_disk_mib > 0 or remaining_bytes > 0:
            required_disk = max(
                effective_min_disk,
                remaining_bytes + PREFLIGHT_DISK_HEADROOM_BYTES,
            )
            usage_path = self.models_dir if self.models_dir.exists() else self.root
            if not usage_path.exists():
                usage_path = usage_path.parent if usage_path.parent.exists() else Path("/")
            free_bytes = shutil.disk_usage(usage_path).free
            if free_bytes < required_disk:
                req_gib = required_disk / 1024**3
                free_gib = free_bytes / 1024**3
                raise RuntimeError(
                    f"远端磁盘空间不足：至少需要 {req_gib:.1f} GiB 可用空间"
                    f"（当前仅剩 {free_gib:.1f} GiB），请清理其他模型或更换磁盘空间更大的远端实例"
                )

    def apply_node_patches(
        self, pack_dir: Path, patches: list[dict], task=None
    ) -> list[str]:
        pack_dir = Path(pack_dir)
        if pack_dir.is_symlink():
            raise ValueError("节点包目录不能是符号链接")

        applied: list[str] = []
        note_fn = getattr(task, "note", None) if task is not None else None
        check_fn = getattr(task, "check", None) if task is not None else None

        for raw_patch in patches or []:
            if callable(check_fn):
                check_fn()
            patch = _as_dict(raw_patch)
            node_name = _validate_safe_node_name(patch.get("node_name"))
            kind = str(patch.get("kind") or "").strip()
            if kind not in ALLOWED_NODE_PATCH_KINDS:
                raise ValueError(f"不允许的自定义节点补丁类型：{kind!r}")

            if kind == "wav2vec2_hidden_states_hook":
                node_dir = pack_dir / node_name
                multitalk_dir = node_dir / "multitalk"
                target_file = multitalk_dir / "nodes.py"
                for candidate, label in (
                    (node_dir, f"自定义节点目录 {node_name}"),
                    (multitalk_dir, f"{node_name}/multitalk"),
                    (target_file, f"{node_name}/multitalk/nodes.py"),
                ):
                    if candidate.is_symlink():
                        raise ValueError(f"{label} 不能是符号链接")
                if not target_file.is_file():
                    raise RuntimeError(f"节点补丁目标文件不存在：{target_file}")

                original = target_file.read_text(encoding="utf-8-sig")
                updated, changed = _patch_wav2vec2_source(original)
                if changed:
                    _atomic_write_text(target_file, updated)
                    patch_label = f"{node_name}:{kind}"
                    applied.append(patch_label)
                    if callable(note_fn):
                        note_fn(f"已应用节点兼容补丁：{patch_label}")
        return applied

    def _invoke_run_cmd(
        self,
        args: list[str],
        task,
        log: Path,
        *,
        timeout: int = 1200,
        cwd: Path | str | None = None,
        env: dict[str, str] | None = None,
        run_cmd_fn: Callable[..., Any] | None = None,
    ) -> None:
        _invoke_run_cmd_compat(
            args,
            task,
            log,
            timeout=timeout,
            cwd=cwd,
            env=env,
            stopping=getattr(self.runtime, "stopping", None),
            run_cmd_fn=run_cmd_fn if run_cmd_fn is not None else self.run_cmd_fn,
        )

    def _resolve_drive_wheel_cache_root(self) -> Path | None:
        return _resolve_drive_wheel_cache_root(getattr(self.runtime, "drive", None))

    def _try_pull_wheel_from_drive(
        self,
        *,
        cache_key: str,
        local_cache_root: Path,
        local_cache_dir: Path,
        dist_prefixes: tuple[str, ...],
        min_major: int | None = None,
        allow_empty: bool = False,
    ) -> Path | None:
        return _try_pull_wheel_from_drive(
            getattr(self.runtime, "drive", None),
            cache_key=cache_key,
            local_cache_root=local_cache_root,
            local_cache_dir=local_cache_dir,
            dist_prefixes=dist_prefixes,
            min_major=min_major,
            allow_empty=allow_empty,
        )

    def _try_mirror_wheel_to_drive(
        self,
        *,
        cache_key: str,
        wheel_path: Path,
        force: bool = False,
    ) -> bool:
        return _try_mirror_wheel_to_drive(
            getattr(self.runtime, "drive", None),
            cache_key=cache_key,
            wheel_path=wheel_path,
            force=force,
        )

    def _checkout_sageattention_source(
        self,
        task,
        src_dir: Path,
        build_log: Path,
        *,
        run_cmd_fn: Callable[..., Any] | None = None,
    ) -> None:
        _checkout_sageattention_source(
            task,
            src_dir,
            build_log,
            stopping=getattr(self.runtime, "stopping", None),
            run_cmd_fn=run_cmd_fn if run_cmd_fn is not None else self.run_cmd_fn,
        )

    def ensure_sageattention_wheels(
        self,
        task,
        *,
        gpu_caps: dict[str, Any] | None = None,
        allow_sage3: bool = True,
        run_cmd_fn: Callable[..., Any] | None = None,
    ) -> dict[str, Any]:
        """Ensure SageAttention 2 (and SageAttention 3 on Blackwell) wheels are ready.

        Delegates to ``ensure_sageattention_wheels_shared`` using the shared runtime
        wheel cache (local NVMe + optional Drive mirror), and marks ComfyUI for
        restart if new acceleration kernels were installed.
        """
        runtime_root = getattr(self.runtime, "root", None)
        base_root = (
            Path(runtime_root)
            if isinstance(runtime_root, (str, os.PathLike)) and str(runtime_root).strip()
            else self.root
        )
        effective_runner = run_cmd_fn if run_cmd_fn is not None else self.run_cmd_fn
        caps = ensure_sageattention_wheels_shared(
            task,
            base_root=base_root,
            runtime=self.runtime,
            drive=getattr(self.runtime, "drive", None),
            gpu_caps=gpu_caps,
            allow_sage3=allow_sage3,
            stopping=getattr(self.runtime, "stopping", None),
            run_cmd_fn=effective_runner,
        )
        current_gen = _current_sageattn_install_generation()
        with self.lock:
            if caps.get("installed_any") or current_gen > self._seen_sageattn_generation:
                self._pending_pip_restart = True
        return caps

    def _prepare_pack_checkout(
        self,
        task,
        *,
        pack_id: str,
        wf_name: str,
        pack_sig: str,
        custom_nodes: list[dict[str, Any]],
        node_patches: list[dict[str, Any]],
        pip_packages: list[str],
    ) -> tuple[Path, bool]:
        _ensure_dir(self.packs_root, "ComfyUI 节点包根目录")
        pack_dir_name = f"pack-{pack_sig[:16]}"
        pack_dir = self.packs_root / pack_dir_name
        _ensure_dir(pack_dir, f"节点包目录 {pack_dir_name}")

        normalized_nodes: list[dict[str, Any]] = []
        seen_names: set[str] = set()
        for raw_node in custom_nodes:
            name = _validate_safe_node_name(raw_node.get("name"))
            if name in seen_names:
                raise ValueError(f"自定义节点名称重复：{name}")
            seen_names.add(name)
            normalized_nodes.append(
                {
                    "name": name,
                    "repo": _normalize_git_repo_url(raw_node.get("repo")),
                    "commit": _validate_commit_sha(
                        raw_node.get("commit"), f"节点 {name} 提交哈希"
                    ),
                    "install_requirements": bool(
                        raw_node.get("install_requirements", True)
                    ),
                }
            )
        clean_pips = _validate_pip_packages(pip_packages)

        complete_marker = pack_dir / ".pack-complete.json"
        pack_ready = False
        if not complete_marker.is_symlink() and complete_marker.is_file():
            try:
                meta = json.loads(complete_marker.read_text(encoding="utf-8"))
                if (
                    isinstance(meta, dict)
                    and meta.get("signature") == pack_sig
                    and all(
                        (pack_dir / n["name"]).is_dir()
                        and not (pack_dir / n["name"]).is_symlink()
                        for n in normalized_nodes
                    )
                ):
                    pack_ready = True
            except (OSError, ValueError):
                pack_ready = False

        if pack_ready:
            task.note(f"复用远端已就绪的节点包：{wf_name}")
            if node_patches:
                self.apply_node_patches(pack_dir, node_patches, task=task)
            return pack_dir, False

        if normalized_nodes and self.run_cmd_fn is None and not shutil.which("git"):
            raise RuntimeError("此远端缺少 Git，无法拉取 ComfyUI 自定义节点包")

        pack_log = self.root / f"comfyui-pack-{pack_id}.log"
        constraints_path: Path | None = None
        constraints_checked = False
        pip_installed = False

        def _get_torch_constraints() -> Path | None:
            nonlocal constraints_path, constraints_checked
            if not constraints_checked:
                constraints_path = _write_torch_constraints(pack_dir)
                constraints_checked = True
            return constraints_path

        for node in normalized_nodes:
            task.check()
            node_name = node["name"]
            repo_url = node["repo"]
            commit = node["commit"]
            node_dir = pack_dir / node_name
            git_dir = node_dir / ".git"
            if git_dir.exists() and not git_dir.is_dir():
                shutil.rmtree(node_dir, ignore_errors=True)
            _ensure_dir(node_dir, f"自定义节点目录 {node_name}")

            _set_task_attr(task, message=f"准备自定义节点 · {node_name}")
            task.note(f"正在拉取节点包 {node_name}（{commit[:10]}）")

            if git_dir.is_dir():
                for lock_file in git_dir.rglob("*.lock"):
                    try:
                        lock_file.unlink(missing_ok=True)
                    except OSError:
                        pass
                try:
                    self._invoke_run_cmd(
                        [
                            "git",
                            "-C",
                            str(node_dir),
                            "remote",
                            "set-url",
                            "origin",
                            repo_url,
                        ],
                        task,
                        pack_log,
                    )
                except RuntimeError:
                    self._invoke_run_cmd(
                        [
                            "git",
                            "-C",
                            str(node_dir),
                            "remote",
                            "add",
                            "origin",
                            repo_url,
                        ],
                        task,
                        pack_log,
                    )
            else:
                self._invoke_run_cmd(
                    ["git", "init", str(node_dir)], task, pack_log
                )
                self._invoke_run_cmd(
                    [
                        "git",
                        "-C",
                        str(node_dir),
                        "remote",
                        "add",
                        "origin",
                        repo_url,
                    ],
                    task,
                    pack_log,
                )

            self._invoke_run_cmd(
                [
                    "git",
                    "-C",
                    str(node_dir),
                    "fetch",
                    "--depth",
                    "1",
                    "origin",
                    commit,
                ],
                task,
                pack_log,
            )
            self._invoke_run_cmd(
                [
                    "git",
                    "-C",
                    str(node_dir),
                    "checkout",
                    "--force",
                    "--detach",
                    commit,
                ],
                task,
                pack_log,
            )
            self._invoke_run_cmd(
                [
                    "git",
                    "-C",
                    str(node_dir),
                    "clean",
                    "-fdx",
                ],
                task,
                pack_log,
            )

            if node["install_requirements"]:
                req_file = node_dir / "requirements.txt"
                if req_file.is_file() and not req_file.is_symlink():
                    filtered_req = pack_dir / f".req-{node_name}.txt"
                    if _filter_requirements(req_file, filtered_req, skip_torch=True):
                        _set_task_attr(task, message=f"安装节点依赖 · {node_name}")
                        task.note(f"正在安装 {node_name} 依赖（保留现有 CUDA PyTorch）")
                        pip_cmd = [
                            sys.executable,
                            "-m",
                            "pip",
                            "install",
                            "--no-input",
                            "--disable-pip-version-check",
                        ]
                        cpath = _get_torch_constraints()
                        if cpath is not None:
                            pip_cmd.extend(["-c", str(cpath)])
                        pip_cmd.extend(["-r", str(filtered_req)])
                        self._invoke_run_cmd(
                            pip_cmd,
                            task,
                            pack_log,
                        )
                        pip_installed = True

        filtered_pips = [
            pkg for pkg in clean_pips if _req_package_name(pkg) not in TORCH_SKIP_PACKAGES
        ]
        if filtered_pips:
            task.check()
            _set_task_attr(task, message=f"安装工作流依赖包 · {wf_name}")
            task.note(f"正在安装工作流额外依赖（{len(filtered_pips)} 个包）")
            pip_cmd = [
                sys.executable,
                "-m",
                "pip",
                "install",
                "--no-input",
                "--disable-pip-version-check",
            ]
            cpath = _get_torch_constraints()
            if cpath is not None:
                pip_cmd.extend(["-c", str(cpath)])
            pip_cmd.extend(filtered_pips)
            self._invoke_run_cmd(
                pip_cmd,
                task,
                pack_log,
            )
            pip_installed = True

        if node_patches:
            self.apply_node_patches(pack_dir, node_patches, task=task)

        if complete_marker.is_symlink():
            raise ValueError(".pack-complete.json 不能是符号链接")
        atomic_json(
            complete_marker,
            {
                "pack_id": pack_id,
                "signature": pack_sig,
                "custom_nodes": [
                    {
                        "name": n["name"],
                        "repo": n["repo"],
                        "commit": n["commit"],
                        "install_requirements": n["install_requirements"],
                    }
                    for n in normalized_nodes
                ],
                "node_patches": [
                    {
                        "node_name": _validate_safe_node_name(p.get("node_name")),
                        "kind": str(p.get("kind") or "").strip(),
                    }
                    for p in node_patches
                ],
                "pip_packages": sorted(set(clean_pips)),
                "patch_version": PACK_PATCH_VERSION,
                "completed_at": time.time(),
            },
        )
        task.note(f"工作流节点包准备完成：{wf_name}")
        return pack_dir, pip_installed

    def _activate_custom_nodes(
        self,
        *,
        pack_id: str,
        pack_sig: str,
        custom_nodes: list[dict[str, Any]],
        pack_dir: Path | None,
    ) -> bool:
        with self.lock:
            prev_state = self._read_managed_state()
            prev_managed = set(prev_state["nodes"].keys())

            if custom_nodes:
                if pack_dir is None:
                    raise RuntimeError("节点包目录未初始化")
                desired: dict[str, str] = {}
                for raw_node in custom_nodes:
                    node_name = _validate_safe_node_name(raw_node.get("name"))
                    desired[node_name] = str((pack_dir / node_name).resolve())
                desired_pack_id = pack_id
                desired_sig = pack_sig
            else:
                desired = {}
                desired_pack_id = ""
                desired_sig = ""

            if not desired and not prev_managed and not self.active_pack_signature:
                return False

            if self.install_dir.is_symlink():
                raise ValueError("ComfyUI 安装目录不能是符号链接")
            custom_nodes_dir = self.install_dir / "custom_nodes"
            _ensure_dir(custom_nodes_dir, "ComfyUI 自定义节点目录")

            # Pre-validate ALL desired targets BEFORE removing any previous managed symlinks
            # so a collision never leaves custom_nodes in a half-uninstalled state.
            for node_name, target_str in desired.items():
                link_path = custom_nodes_dir / node_name
                if link_path.is_symlink():
                    if node_name not in prev_managed:
                        try:
                            if link_path.is_dir() and str(link_path.resolve()) == target_str:
                                continue
                        except OSError:
                            pass
                        raise RuntimeError(
                            f"自定义节点符号链接 {node_name} 已存在且非 ColabOne 托管，请手动移走后重试"
                        )
                elif link_path.exists():
                    raise RuntimeError(
                        f"自定义节点目录 {node_name} 已存在且非 ColabOne 托管，请手动移走后重试"
                    )

            disk_changed = False

            # Remove obsolete ColabOne-managed symlinks; never touch user-placed directories/files.
            for old_name in sorted(prev_managed - set(desired.keys())):
                try:
                    safe_old = _validate_safe_node_name(old_name)
                except ValueError:
                    continue
                old_link = custom_nodes_dir / safe_old
                if old_link.is_symlink():
                    old_link.unlink()
                    disk_changed = True

            # Atomically activate symlinks for this pack's custom nodes.
            for node_name, target_str in desired.items():
                link_path = custom_nodes_dir / node_name
                if link_path.is_symlink():
                    try:
                        if link_path.is_dir() and str(link_path.resolve()) == target_str:
                            continue
                    except OSError:
                        pass
                tmp_link = custom_nodes_dir / f".{node_name}.{uuid.uuid4().hex[:8]}.tmp"
                try:
                    os.symlink(target_str, tmp_link)
                    os.replace(tmp_link, link_path)
                finally:
                    if tmp_link.is_symlink() or tmp_link.exists():
                        tmp_link.unlink(missing_ok=True)
                disk_changed = True

            sig_changed = self.active_pack_signature != desired_sig
            if sig_changed or disk_changed or prev_state["pack_id"] != desired_pack_id:
                manifest_path = self._managed_manifest_path()
                if manifest_path.is_symlink():
                    raise ValueError("托管节点清单文件不能是符号链接")
                atomic_json(
                    manifest_path,
                    {
                        "pack_id": desired_pack_id,
                        "signature": desired_sig,
                        "nodes": desired,
                        "updated_at": time.time(),
                    },
                )

            self._active_pack_id = desired_pack_id
            self.active_pack_signature = desired_sig
            return bool(sig_changed or disk_changed)

    def _download_single(
        self,
        url: str,
        headers: dict[str, str],
        partial: Path,
        size: int,
        task,
        done: int,
        progress_range: tuple[float, float] | None = None,
    ) -> None:
        if partial.is_symlink():
            raise ValueError(f"模型临时文件不能是符号链接：{partial.name}")
        if partial.is_dir():
            raise ValueError(f"模型临时路径不能是目录：{partial.name}")
        offset = partial.stat().st_size if partial.exists() else 0
        if offset > size:
            partial.unlink()
            offset = 0
        stats = task.transfer
        active_range = (
            progress_range
            if progress_range is not None
            else getattr(stats, "progress_range", None)
        )
        initial_resumed = offset
        with stats.lock:
            stats.downloaded = done + offset
            stats.resumed += offset
            stats.phase = "downloading"
            initial_total = stats.total
        if initial_total > 0 and (done + offset) > 0:
            _set_task_attr(
                task,
                progress=_map_progress((done + offset) / initial_total, active_range),
            )
        if offset == size:
            return

        http_client = getattr(self.runtime, "http", None)
        own_session: requests.Session | None = None
        if http_client is None or isinstance(http_client, requests.Session):
            own_session = requests.Session()
            own_session.trust_env = False
            get_fn = own_session.get
        else:
            get_fn = http_client.get

        resp_lock = threading.Lock()
        active_response: list[Any] = [None]
        task_lock = getattr(task, "lock", None)
        has_cancel_hook_attr = hasattr(task, "cancel_hook")
        prev_cancel_hook = None
        if has_cancel_hook_attr:
            with task_lock if task_lock is not None else nullcontext():
                prev_cancel_hook = task.cancel_hook

        def _single_cancel_hook() -> None:
            with resp_lock:
                resp = active_response[0]
            if resp is not None:
                _abort_response(resp)
            if callable(prev_cancel_hook):
                try:
                    prev_cancel_hook()
                except Exception:
                    pass

        if has_cancel_hook_attr:
            with task_lock if task_lock is not None else nullcontext():
                task.cancel_hook = _single_cancel_hook

        max_attempts = 3
        try:
            for attempt in range(max_attempts):
                task.check()
                req_headers = dict(headers)
                req_headers["Accept-Encoding"] = "identity"
                if offset:
                    req_headers["Range"] = f"bytes={offset}-"
                with stats.lock:
                    stats.active = 1
                try:
                    with get_fn(
                        url, headers=req_headers, stream=True, timeout=(10, 30)
                    ) as response:
                        with resp_lock:
                            active_response[0] = response
                        try:
                            task.check()
                            if response.status_code == 416 and offset > 0:
                                with stats.lock:
                                    if initial_resumed:
                                        stats.resumed = max(
                                            0, stats.resumed - initial_resumed
                                        )
                                    stats.downloaded = done
                                initial_resumed = 0
                                partial.unlink(missing_ok=True)
                                offset = 0
                                continue
                            response.raise_for_status()
                            if offset and response.status_code != 206:
                                with stats.lock:
                                    if initial_resumed:
                                        stats.resumed = max(
                                            0, stats.resumed - initial_resumed
                                        )
                                    stats.downloaded = done
                                initial_resumed = 0
                                offset = 0
                            if response.status_code == 206:
                                cr = response.headers.get("Content-Range", "").strip()
                                expected_cr = f"bytes {offset}-{size - 1}/{size}"
                                if cr != expected_cr:
                                    raise RuntimeError("下载范围响应不匹配，请重试")
                            with partial.open("ab" if offset else "wb") as stream:
                                for block in response.iter_content(ITER_CHUNK_BYTES):
                                    task.check()
                                    if not block:
                                        continue
                                    if offset + len(block) > size:
                                        raise RuntimeError("远端文件大于清单声明的大小")
                                    stream.write(block)
                                    offset += len(block)
                                    stats.wire(len(block))
                                    should_report = False
                                    with stats.lock:
                                        stats.downloaded = done + offset
                                        total_bytes = stats.total
                                        now = time.monotonic()
                                        if now - stats.last_report >= 5:
                                            stats.last_report = now
                                            should_report = True
                                    if total_bytes > 0:
                                        _set_task_attr(
                                            task,
                                            progress=_map_progress(
                                                (done + offset) / total_bytes,
                                                active_range,
                                            ),
                                        )
                                    if should_report:
                                        view = stats.snapshot()
                                        task.note(
                                            f"进度 {100 * (done + offset) / max(1, total_bytes):.1f}% · "
                                            f"{(done + offset) / 1024**3:.2f}/{total_bytes / 1024**3:.2f} GiB · "
                                            f"{view['speed_bps'] / 1024**2:.1f} MiB/s · 单连接"
                                        )
                                stream.flush()
                                os.fsync(stream.fileno())
                        finally:
                            with resp_lock:
                                active_response[0] = None
                    task.check()
                    if offset < size:
                        raise requests.RequestException("下载连接提前中断，准备续传")
                    return
                except requests.HTTPError as exc:
                    task.check()
                    response_obj = getattr(exc, "response", None)
                    status_code = getattr(response_obj, "status_code", None)
                    if status_code in (401, 403):
                        raise RuntimeError(
                            f"模型下载鉴权失败（HTTP {status_code}），请在设置中配置有效的 Hugging Face Token"
                        ) from None
                    if (
                        status_code not in (429, 500, 502, 503, 504)
                        or attempt + 1 >= max_attempts
                    ):
                        raise RuntimeError(
                            f"模型单连接下载失败（HTTP {status_code or type(exc).__name__}）"
                        ) from None
                    if status_code == 429:
                        retry_after = parse_retry_after(response_obj, float(2**attempt))
                        jitter = random.uniform(0.0, min(1.0, retry_after * 0.25))
                        delay = retry_after + jitter
                    else:
                        delay = float(2**attempt)
                    task.note(
                        f"单连接下载重试 {attempt + 1}/{max_attempts - 1}（HTTP {status_code}）",
                        "warning",
                    )
                    if task.cancel.wait(delay):
                        task.check()
                except requests.RequestException as exc:
                    task.check()
                    if attempt + 1 >= max_attempts:
                        raise RuntimeError(
                            f"模型单连接下载失败（{type(exc).__name__}）"
                        ) from None
                    task.note(
                        f"单连接下载重试 {attempt + 1}/{max_attempts - 1}（{type(exc).__name__}）",
                        "warning",
                    )
                    if task.cancel.wait(2**attempt):
                        task.check()
                except OSError:
                    task.check()
                    raise
                finally:
                    with stats.lock:
                        stats.active = 0
        finally:
            if has_cancel_hook_attr:
                with task_lock if task_lock is not None else nullcontext():
                    if task.cancel_hook is _single_cancel_hook:
                        task.cancel_hook = prev_cancel_hook
            if own_session is not None:
                own_session.close()

    def _invoke_download_fn(
        self,
        *,
        url: str,
        headers: dict[str, str],
        partial: Path,
        size: int,
        sha256: str,
        task,
        done: int,
        threads: int,
        model_ref: dict[str, Any],
        hf_token: str = "",
    ) -> None:
        fn = self.download_fn
        try:
            sig = inspect.signature(fn)
            params = list(sig.parameters.values())
            has_var_pos = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in params)
            has_var_kw = any(p.kind == inspect.Parameter.VAR_KEYWORD for p in params)
            pos_params = [
                p
                for p in params
                if p.kind
                in (
                    inspect.Parameter.POSITIONAL_ONLY,
                    inspect.Parameter.POSITIONAL_OR_KEYWORD,
                )
            ]
            param_names = [p.name for p in params]

            if not has_var_pos and len(pos_params) < 7:
                available = {
                    "model": model_ref,
                    "model_ref": model_ref,
                    "ref": model_ref,
                    "url": url,
                    "headers": headers,
                    "partial": partial,
                    "dest": partial,
                    "dest_path": partial,
                    "target": partial,
                    "target_path": partial,
                    "path": partial,
                    "size": size,
                    "sha256": sha256,
                    "digest": sha256,
                    "task": task,
                    "done": done,
                    "threads": threads,
                    "hf_token": hf_token,
                    "token": hf_token,
                }
                if all(
                    p.name in available or p.default is not inspect.Parameter.empty
                    for p in pos_params
                ):
                    call_kwargs = {
                        name: available[name]
                        for name in param_names
                        if name in available
                    }
                    fn(**call_kwargs)
                    return

            kwargs: dict[str, Any] = {}
            if "threads" in param_names or has_var_kw:
                kwargs["threads"] = threads
            fn(url, headers, partial, size, sha256, task, done, **kwargs)
        except (TypeError, ValueError):
            fn(url, headers, partial, size, sha256, task, done)

    def _ensure_models(
        self,
        task,
        models: list[dict],
        hf_token: str = "",
        progress_range: tuple[float, float] | None = None,
    ) -> None:
        entries = self._deduplicate_models(models)
        if not entries:
            return

        if all(self.is_model_verified(ref) for ref, _ in entries):
            if progress_range is not None:
                _set_task_attr(task, progress=_map_progress(1.0, progress_range))
            task.note(f"工作流所需的 {len(entries)} 个模型权重均已校验就绪，跳过下载")
            return

        threads = DEFAULT_DOWNLOAD_THREADS
        total = sum(max(0, int(ref.get("size") or 0)) for ref, _ in entries)
        stats = ColabOneTransfer(total, threads, progress_range=progress_range)
        stats.count = len(entries)
        _set_task_attr(task, transfer=stats)

        try:
            task.note(
                f"准备下载工作流权重：{len(entries)} 个文件，共 {total / 1024**3:.2f} GiB，{threads} 线程"
            )

            clean_token = (hf_token or "").strip()
            headers = {"Authorization": f"Bearer {clean_token}"} if clean_token else {}
            http_client = getattr(self.runtime, "http", None)
            custom_get = (
                None
                if http_client is None or isinstance(http_client, requests.Session)
                else http_client.get
            )

            done = 0
            for index, (ref, target) in enumerate(entries, 1):
                task.check()
                size = int(ref.get("size") or 0)
                sha256 = str(ref.get("sha256") or "").strip().lower()
                if size <= 0 or not _SHA256_RE.fullmatch(sha256):
                    raise ValueError(f"模型校验元数据不合法：{target.name}")
                repo = _validate_hf_repo(ref.get("repo"))
                revision = _validate_commit_sha(ref.get("revision"), "模型修订版本")
                rel_path = str(ref.get("path") or "").strip()

                _ensure_dir(self.models_dir, "ComfyUI 模型目录")
                _ensure_dir(target.parent, f"ComfyUI 模型子目录 {ref['target_dir']}")

                partial = target.with_name(target.name + ".part")
                if partial.is_symlink():
                    raise ValueError(f"模型临时文件不能是符号链接：{partial.name}")
                if partial.is_dir():
                    raise ValueError(f"模型临时路径不能是目录：{partial.name}")

                with stats.lock:
                    stats.file = target.name
                    stats.index = index

                if self.is_model_verified(ref):
                    done += size
                    with stats.lock:
                        stats.downloaded = done
                        stats.resumed += size
                    if total > 0:
                        _set_task_attr(
                            task,
                            progress=_map_progress(done / total, progress_range),
                        )
                    task.note(
                        f"模型 {index}/{len(entries)} 已存在且校验通过，跳过：{target.name}"
                    )
                    continue

                # Re-verify an existing unreceipted file before overwriting it.
                if target.exists():
                    if target.is_dir():
                        raise ValueError(f"模型目标路径已存在同名目录：{target.name}")
                    if (
                        not target.is_symlink()
                        and target.is_file()
                        and target.stat().st_size == size
                        and _sha256_file(target, task) == sha256
                    ):
                        self._record_verified(target.parent, target.name, sha256)
                        done += size
                        with stats.lock:
                            stats.downloaded = done
                            stats.resumed += size
                        if total > 0:
                            _set_task_attr(
                                task,
                                progress=_map_progress(done / total, progress_range),
                            )
                        task.note(
                            f"模型 {index}/{len(entries)} 已存在且校验通过，跳过：{target.name}"
                        )
                        continue
                    target.unlink()
                    task.note(f"发现未通过校验的模型文件，重新下载：{target.name}", "warning")

                url = f"https://huggingface.co/{repo}/resolve/{revision}/{quote(rel_path)}"
                _set_task_attr(
                    task, message=f"下载模型 {index}/{len(entries)} · {target.name}"
                )
                task.note(
                    f"模型 {index}/{len(entries)} · {target.name} · {size / 1024**3:.2f} GiB"
                )

                with stats.lock:
                    resumed_before = stats.resumed
                try:
                    if self.download_fn is not None:
                        self._invoke_download_fn(
                            url=url,
                            headers=headers,
                            partial=partial,
                            size=size,
                            sha256=sha256,
                            task=task,
                            done=done,
                            threads=threads,
                            model_ref=ref,
                            hf_token=clean_token,
                        )
                    else:
                        parallel_file(
                            url,
                            headers,
                            partial,
                            size,
                            sha256,
                            task,
                            done,
                            threads,
                            get=custom_get,
                            progress_range=progress_range,
                        )
                except ColabOneRangeUnsupportedError:
                    task.note("下载源不支持分段请求，自动切换为单连接下载", "warning")
                    partial.unlink(missing_ok=True)
                    metadata_path(partial).unlink(missing_ok=True)
                    with stats.lock:
                        stats.resumed = resumed_before
                    self._download_single(
                        url,
                        headers,
                        partial,
                        size,
                        task,
                        done,
                        progress_range=progress_range,
                    )

                with stats.lock:
                    stats.phase = "verifying"
                _set_task_attr(task, message=f"校验 SHA-256 · {target.name}")
                task.note(f"模型 {index}/{len(entries)} 传输完成，正在校验 SHA-256")

                verify_path = partial if partial.exists() else target
                if (
                    not verify_path.is_file()
                    or verify_path.is_symlink()
                    or verify_path.stat().st_size != size
                    or _sha256_file(verify_path, task) != sha256
                ):
                    if partial.exists() and not partial.is_dir():
                        partial.unlink(missing_ok=True)
                    metadata_path(partial).unlink(missing_ok=True)
                    if (
                        verify_path is target
                        and target.exists()
                        and not target.is_symlink()
                        and not target.is_dir()
                    ):
                        target.unlink(missing_ok=True)
                    raise RuntimeError(f"模型文件校验失败：{target.name}，请重新下载")

                if verify_path is partial:
                    os.replace(partial, target)
                metadata_path(partial).unlink(missing_ok=True)
                self._record_verified(target.parent, target.name, sha256)
                done += size
                with stats.lock:
                    stats.downloaded = done
                if total > 0:
                    _set_task_attr(
                        task,
                        progress=_map_progress(done / total, progress_range),
                    )
                task.note(
                    f"模型 {index}/{len(entries)} 校验通过，已保存到远端：{target.name}"
                )

            elapsed = max(time.monotonic() - stats.started, 0.1)
            with stats.lock:
                stats.phase = "completed"
                stats.downloaded = total
                received = stats.received
                resumed = stats.resumed
            task.note(
                f"工作流权重全部已验证：{total / 1024**3:.2f} GiB，用时 {elapsed:.1f} 秒，"
                f"平均传输 {received / elapsed / 1024**2:.1f} MiB/s；复用 {resumed / 1024**2:.1f} MiB"
            )
            # Clear task.transfer upon successful download/verify completion so subsequent
            # rendering/TTS/ffmpeg stages never leak download state or log false download errors.
            if total > 0:
                _set_task_attr(
                    task,
                    progress=_map_progress(1.0, progress_range),
                    transfer=None,
                )
            else:
                _set_task_attr(task, transfer=None)
        except BaseException as exc:
            cancel_ev = getattr(task, "cancel", None)
            is_cancelled = any(
                cls.__name__ == "ColabOneCancelledError" for cls in type(exc).__mro__
            ) or (cancel_ev is not None and cancel_ev.is_set())
            with stats.lock:
                stats.phase = "cancelled" if is_cancelled else "failed"
            raise

    def ensure_pack(
        self,
        task,
        workflow_template: dict,
        hf_token: str = "",
        progress_range: tuple[float, float] | None = None,
    ) -> bool:
        wf = _as_dict(workflow_template)
        pack_id = str(wf.get("id") or "").strip()
        if not pack_id or not _WORKFLOW_ID_RE.fullmatch(pack_id):
            raise ValueError(f"工作流编号不合法：{pack_id!r}")
        wf_name = str(wf.get("name") or pack_id).strip() or pack_id

        custom_nodes = [_as_dict(n) for n in (wf.get("custom_nodes") or [])]
        node_patches = [_as_dict(p) for p in (wf.get("node_patches") or [])]
        pip_packages = [
            str(p).strip() for p in (wf.get("pip_packages") or []) if str(p).strip()
        ]
        models = [_as_dict(m) for m in (wf.get("models") or [])]

        content_sig = compute_pack_content_signature(wf)
        pack_dir: Path | None = None
        pip_installed = False

        if custom_nodes or pip_packages or node_patches:
            pack_dir, pip_installed = self._prepare_pack_checkout(
                task,
                pack_id=pack_id,
                wf_name=wf_name,
                pack_sig=content_sig,
                custom_nodes=custom_nodes,
                node_patches=node_patches,
                pip_packages=pip_packages,
            )
            if pip_installed:
                with self.lock:
                    self._pending_pip_restart = True

        # Download and verify all models BEFORE activating custom_nodes symlinks so a
        # failed or cancelled multi-GB transfer never leaves active_pack_signature/symlinks
        # out of sync with the running ComfyUI process.
        self._ensure_models(
            task,
            models,
            hf_token=hf_token,
            progress_range=progress_range,
        )

        activation_changed = self._activate_custom_nodes(
            pack_id=pack_id,
            pack_sig=content_sig,
            custom_nodes=custom_nodes,
            pack_dir=pack_dir,
        )
        current_gen = _current_sageattn_install_generation()
        with self.lock:
            pending_pip = self._pending_pip_restart or (
                current_gen > self._seen_sageattn_generation
            )
            self._pending_pip_restart = False
            self._seen_sageattn_generation = current_gen
        return bool(activation_changed or pip_installed or pending_pip)
