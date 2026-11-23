"""Data-only model manifests shared by the desktop controller and remote service."""

from __future__ import annotations

import hashlib
import re
from collections.abc import Iterable, Mapping
from pathlib import PurePosixPath
from typing import Any, Literal
from urllib.parse import unquote

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

COLABONE_API_MODEL_PREFIX = "colabone/"
COLABONE_AUTO_API_MODEL_ID = "colabone/auto"
COLABONE_API_MODEL_ID_RE = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,119}$")

_KNOWN_PACKAGER_PREFIXES = frozenset(
    {
        "huihui",
        "unsloth",
        "bartowski",
        "mradermacher",
        "lmstudio",
        "maziyarpanahi",
        "quantfactory",
        "tensorblock",
        "ggml",
        "bullerwins",
    }
)
_FORMAT_NOISE_TOKENS = frozenset(
    {
        "gguf",
        "ggml",
        "imatrix",
        "exl2",
        "awq",
        "gptq",
        "mlx",
        "onnx",
        "bnb",
        "4bit",
        "8bit",
        "fp8",
        "fp16",
        "bf16",
        "f16",
        "f32",
        "quant",
        "quantized",
        "ud",
        "mxfp4",
        "nvfp4",
    }
)
_REPACK_MODIFIER_TOKENS = frozenset(
    {
        "abliterated",
        "uncensored",
        "heretic",
        "ara",
        "rvn",
        "rco",
        "gsq",
        "lorablated",
        "obliterated",
        "unfiltered",
        "decensored",
    }
)
_CHAT_SUFFIX_TOKENS = frozenset({"instruct", "it"})
_EMBEDDED_QUANT_RE = re.compile(
    r"(?:^|[-_\s])(?:ud[-_])?(?:i|t)?q[1-8](?:_[0-9a-z]+)*(?=$|[-_\s])",
    re.IGNORECASE,
)
_QUANT_TOKEN_RE = re.compile(
    r"^(?:(?:i|t)?q[1-8](?:_[0-9a-z]+)*|k_(?:s|m|l|xl|xxl))$",
    re.IGNORECASE,
)
_DATE_TAG_RE = re.compile(
    r"^(?:(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])|2[3-9](?:0[1-9]|1[0-2])|20\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01]))$"
)
_REPACK_VERSION_RE = re.compile(r"^v\d+(?:\.\d+)?$", re.IGNORECASE)
_COMPACT_QUANT_RE = re.compile(
    r"(mxfp4|nvfp4|bf16|fp16|f16|f32|fp8|(?:i|t)?q[1-8])",
    re.IGNORECASE,
)


def validate_colabone_api_model_id(
    value: Any, *, allow_empty: bool = False
) -> str:
    if value is None:
        raw = ""
    elif isinstance(value, str):
        raw = value.strip()
    else:
        raise ValueError("模型 API ID 必须为字符串")
    if not raw:
        if allow_empty:
            return ""
        raise ValueError("模型 API ID 不能为空")
    if raw.lower() == COLABONE_AUTO_API_MODEL_ID:
        raise ValueError("colabone/auto 为系统保留别名，请使用其他模型 ID")
    if (
        len(raw) > 120
        or not COLABONE_API_MODEL_ID_RE.fullmatch(raw)
        or ".." in raw
        or "//" in raw
        or any(seg in (".", "..") for seg in raw.split("/"))
        or raw.endswith("/")
        or raw.endswith(".")
    ):
        raise ValueError(
            "模型 API ID 仅支持字母、数字、点、下划线、连字符和斜杠，且不能以斜杠或点结尾"
        )
    return raw


def _sanitize_slug_part(text: str, max_len: int = 56) -> str:
    cleaned = re.sub(r"[^a-zA-Z0-9.-]+", "-", (text or "").strip().lower())
    cleaned = re.sub(r"\.{2,}", ".", cleaned)
    cleaned = re.sub(r"-{2,}", "-", cleaned).strip("-.")
    if len(cleaned) > max_len:
        cleaned = cleaned[:max_len].rstrip("-.")
    return cleaned


def colabone_default_api_model_id(
    repo: str = "", filename: str = "", name: str = ""
) -> str:
    """Build a clean, human-readable `colabone/<model>` API identifier."""
    repo_clean = (repo or "").strip().strip("/")
    owner = ""
    raw_source = ""
    if "/" in repo_clean:
        owner, raw_source = repo_clean.split("/", 1)
    elif repo_clean:
        raw_source = repo_clean
    elif (name or "").strip():
        raw_source = name.strip()
    elif (filename or "").strip():
        raw_source = PurePosixPath(filename.strip()).name

    raw_source = re.sub(
        r"(?:-\d{5}-of-\d{5})?\.gguf$", "", raw_source, flags=re.IGNORECASE
    )
    # Remove composite quantization suffixes (e.g. UD-Q4_K_XL, Q8_0) before splitting underscores.
    stripped_quant = _EMBEDDED_QUANT_RE.sub("-", raw_source)
    raw_tokens = [
        tok.strip(".")
        for tok in re.split(r"[^a-zA-Z0-9.]+", stripped_quant.lower())
        if tok.strip(".")
    ]

    owner_prefixes: set[str] = set(_KNOWN_PACKAGER_PREFIXES)
    if owner:
        owner_low = owner.lower()
        owner_prefixes.add(owner_low)
        first_owner_seg = re.split(r"[^a-z0-9]+", owner_low)[0]
        if first_owner_seg:
            owner_prefixes.add(first_owner_seg)

    tokens = list(raw_tokens)
    while len(tokens) > 1 and tokens[0] in owner_prefixes:
        tokens.pop(0)

    kept: list[str] = []
    seen_repack = False
    for idx, tok in enumerate(tokens):
        if tok in _FORMAT_NOISE_TOKENS or _QUANT_TOKEN_RE.fullmatch(tok):
            continue
        if tok in _REPACK_MODIFIER_TOKENS:
            seen_repack = True
            continue
        if tok in _CHAT_SUFFIX_TOKENS:
            continue
        if idx > 0 and _DATE_TAG_RE.fullmatch(tok):
            continue
        if seen_repack and _REPACK_VERSION_RE.fullmatch(tok):
            continue
        kept.append(tok)

    if not kept:
        kept = [
            tok
            for tok in tokens
            if tok not in _FORMAT_NOISE_TOKENS and not _QUANT_TOKEN_RE.fullmatch(tok)
        ]
    if not kept:
        kept = raw_tokens

    slug = _sanitize_slug_part("-".join(kept), max_len=56)
    if not slug:
        slug = _sanitize_slug_part(raw_source or name or "model", max_len=56) or "model"
    if slug == "auto":
        slug = "auto-model"
    return f"{COLABONE_API_MODEL_PREFIX}{slug}"


def colabone_compact_quant_tag(filename: str) -> str:
    """Return a concise lowercase quant suffix (e.g. q4, q8, bf16) for collision fallback."""
    from common.hub import extract_quantization

    quant = extract_quantization(filename or "")
    if not quant or quant == "GGUF":
        return ""
    match = _COMPACT_QUANT_RE.search(quant)
    if match:
        return match.group(1).lower()
    return _sanitize_slug_part(quant, max_len=16)


def colabone_unique_api_model_id(
    base_api_id: str,
    existing_ids: Iterable[str],
    *,
    repo: str = "",
    filename: str = "",
) -> str:
    """Return `base_api_id` cleanly, appending a concise disambiguator only on collision."""
    base = validate_colabone_api_model_id(base_api_id)
    taken = {
        str(item).strip().lower()
        for item in existing_ids
        if isinstance(item, str) and item.strip()
    }
    taken.add(COLABONE_AUTO_API_MODEL_ID)
    if base.lower() not in taken:
        return base

    candidates: list[str] = []
    compact_q = colabone_compact_quant_tag(filename)
    if compact_q:
        candidates.append(f"{base}-{compact_q}")
    if filename:
        from common.hub import extract_quantization

        full_q = extract_quantization(filename)
        if full_q and full_q != "GGUF":
            full_q_slug = re.sub(r"[^a-z0-9_-]+", "-", full_q.lower()).strip("-_")
            if full_q_slug and full_q_slug != compact_q:
                candidates.append(f"{base}-{full_q_slug}")
    if "/" in (repo or ""):
        owner_seg = _sanitize_slug_part(
            re.split(r"[^a-zA-Z0-9]+", repo.split("/", 1)[0])[0], max_len=16
        )
        if owner_seg and len(owner_seg) >= 2 and owner_seg not in base.lower():
            candidates.append(f"{base}-{owner_seg}")

    for cand in candidates:
        cand = cand[:120].rstrip("-.")
        if cand.lower() not in taken:
            try:
                return validate_colabone_api_model_id(cand)
            except ValueError:
                continue

    for idx in range(2, 1000):
        suffix = f"-{idx}"
        cand = f"{base[: 120 - len(suffix)].rstrip('-.')}{suffix}"
        if cand.lower() not in taken:
            return cand
    return base


def _model_attr(model: ModelSpec | Mapping[str, Any], key: str, default: Any = "") -> Any:
    if isinstance(model, Mapping):
        return model.get(key, default)
    return getattr(model, key, default)


def colabone_model_owner(model: ModelSpec | Mapping[str, Any]) -> str:
    files = _model_attr(model, "files", None) or []
    first_repo = ""
    if files:
        first = files[0]
        first_repo = (
            str(first.get("repo") or "")
            if isinstance(first, Mapping)
            else str(getattr(first, "repo", "") or "")
        )
    if not first_repo:
        first_repo = str(_model_attr(model, "repo", "") or "")
    if "/" in first_repo:
        owner = first_repo.split("/", 1)[0].strip()
        if owner:
            return owner
    return "colabone"


def resolve_colabone_api_model_id(model: ModelSpec | Mapping[str, Any]) -> str:
    """Resolve the public OpenAI API model ID for a model spec or serialized dict."""
    explicit = str(_model_attr(model, "api_id", "") or "").strip()
    if explicit:
        return explicit
    internal_id = str(_model_attr(model, "id", "") or "").strip()
    if internal_id.startswith("hf-"):
        files = _model_attr(model, "files", None) or []
        first_repo = ""
        first_path = ""
        if files:
            first = files[0]
            if isinstance(first, Mapping):
                first_repo = str(first.get("repo") or "")
                first_path = str(first.get("path") or "")
            else:
                first_repo = str(getattr(first, "repo", "") or "")
                first_path = str(getattr(first, "path", "") or "")
        if not first_repo:
            first_repo = str(_model_attr(model, "repo", "") or "")
        name = str(_model_attr(model, "name", "") or "")
        return colabone_default_api_model_id(
            repo=first_repo, filename=first_path, name=name
        )
    return internal_id


class ModelFile(BaseModel):
    model_config = ConfigDict(extra="forbid")
    repo: str
    revision: str
    path: str
    size: int = Field(gt=0)
    sha256: str
    role: Literal["model", "shard"] = "model"

    @field_validator("repo")
    @classmethod
    def repository(cls, value):
        if not re.fullmatch(r"[\w.-]+/[\w.-]+", value) or any(
            part in (".", "..") for part in value.split("/")
        ):
            raise ValueError("需要 Hugging Face 的 owner/repository")
        return value

    @field_validator("revision")
    @classmethod
    def revision_hash(cls, value):
        if not re.fullmatch(r"[a-fA-F0-9]{40}", value):
            raise ValueError("模型必须固定到完整 commit SHA")
        return value

    @field_validator("path")
    @classmethod
    def relative_file(cls, value):
        p = PurePosixPath(value)
        if (
            p.is_absolute()
            or ".." in p.parts
            or "." in p.parts
            or ":" in value
            or "\\" in value
            or "\x00" in value
            or len(value) > 512
            or not value.lower().endswith(".gguf")
        ):
            raise ValueError("模型文件路径不合法")
        return value

    @field_validator("sha256")
    @classmethod
    def digest(cls, value):
        if not re.fullmatch(r"[a-fA-F0-9]{64}", value or ""):
            raise ValueError("文件需要可信的 SHA-256")
        return value.lower()


class ModelSpec(BaseModel):
    model_config = ConfigDict(extra="forbid")
    id: str = Field(pattern=r"^[a-zA-Z0-9_-]{1,80}$")
    api_id: str = Field(default="", max_length=120)
    name: str = Field(min_length=1, max_length=200)
    description: str = ""
    min_vram_mib: int = Field(default=0, ge=0)
    context_length: int = Field(default=8192, ge=512, le=262144)
    temperature: float = Field(default=0.7, ge=0, le=2)
    cpu_moe_layers: int = Field(default=0, ge=0, le=256)
    source: str = "Hugging Face"
    kind: Literal["chat"] = "chat"
    files: list[ModelFile] = Field(min_length=1, max_length=128)

    @field_validator("api_id", mode="before")
    @classmethod
    def check_api_id(cls, value):
        return validate_colabone_api_model_id(value, allow_empty=True)

    @model_validator(mode="after")
    def file_set(self):
        if sum(f.role == "model" for f in self.files) != 1:
            raise ValueError("需要且只能有一个入口模型文件")
        if len({f.path for f in self.files}) != len(self.files):
            raise ValueError("文件路径重复")
        if any(
            f.role not in ("model", "shard") or not f.path.lower().endswith(".gguf")
            for f in self.files
        ):
            raise ValueError("聊天模型只能包含 GGUF 模型和分片")
        return self


def parse_hf_input(
    repo_or_url: str, filename: str = "", revision: str = "main"
) -> tuple[str, str, str]:
    raw = (repo_or_url or "").strip()
    fn = (filename or "").strip()
    rev = (revision or "main").strip() or "main"
    match = re.match(
        r"^https?://(?:www\.)?huggingface\.co/([\w.-]+/[\w.-]+)(?:/(?:blob|resolve|tree)/([^/?#]+)(?:/([^?#]+))?)?/?(?:\?[^#]*)?(?:#.*)?$",
        raw,
        re.IGNORECASE,
    )
    if match:
        raw = match.group(1)
        if match.group(2) and rev == "main":
            rev = match.group(2)
        if match.group(3) and not fn:
            candidate = unquote(match.group(3))
            if candidate.lower().endswith(".gguf"):
                fn = candidate
    return raw.strip("/"), fn, rev


def resolve_import(
    repo: str,
    filename: str,
    revision="main",
    token="",
    session=None,
    custom_name: str = "",
) -> ModelSpec:
    """Resolve metadata only. The desktop never reads a model's download URL."""
    import requests

    own_session = session is None
    http = session or requests.Session()
    try:
        repo, filename, revision = parse_hf_input(repo, filename, revision)
        if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo) or any(
            part in (".", "..") for part in repo.split("/")
        ):
            raise ValueError("请输入 owner/repository 或 Hugging Face 仓库链接")
        if not filename:
            raise ValueError("请选择或输入 GGUF 模型文件")
        if not re.fullmatch(r"[\w.-]+", revision) or ".." in revision or revision == ".":
            raise ValueError("revision 不合法")
        headers = {"Authorization": f"Bearer {token}"} if token else {}
        r = http.get(
            f"https://huggingface.co/api/models/{repo}/revision/{revision}",
            params={"blobs": "true"},
            headers=headers,
            timeout=30,
        )
        r.raise_for_status()
        try:
            info = r.json()
        except ValueError:
            raise RuntimeError("模型社区返回了无效结果") from None
        if not isinstance(info, dict):
            raise RuntimeError("模型社区返回了无效结果")
    finally:
        if own_session:
            http.close()
    from common.hub import classify_modality, extract_quantization, is_auxiliary_gguf

    if is_auxiliary_gguf(filename):
        raise ValueError("该文件是量化校准或辅助文件，不是可直接装载的模型权重")
    modality = classify_modality(
        str(info.get("pipeline_tag") or ""),
        info.get("tags") if isinstance(info.get("tags"), list) else [],
        repo,
    )
    if modality in ("image", "video"):
        raise ValueError(
            "该模型属于图片/视频生成类（ComfyUI 专用），无法在「对外 API」文本引擎中装载，请前往 ComfyUI 工作区使用"
        )
    raw_siblings = info.get("siblings") or []
    siblings = {
        f["rfilename"]: f
        for f in raw_siblings
        if isinstance(f, dict) and isinstance(f.get("rfilename"), str)
    }
    selected = [filename]
    match = re.fullmatch(r"(.+)-\d{5}-of-(\d{5})\.gguf", filename, re.IGNORECASE)
    if match:
        count = int(match[2])
        if count < 1 or count > 128:
            raise ValueError("分片数量需在 1 到 128 之间")
        suffix = filename[-5:]
        selected = [
            f"{match[1]}-{i:05d}-of-{count:05d}{suffix}" for i in range(1, count + 1)
        ]
    files = []
    commit_sha = str(info.get("sha") or "")
    for name in selected:
        item = siblings.get(name) or {}
        lfs = item.get("lfs") if isinstance(item.get("lfs"), dict) else {}
        raw_size = lfs.get("size", 0)
        raw_sha = lfs.get("sha256", "")
        files.append(
            ModelFile(
                repo=repo,
                revision=commit_sha,
                path=name,
                size=raw_size if isinstance(raw_size, int) and not isinstance(raw_size, bool) else 0,
                sha256=raw_sha if isinstance(raw_sha, str) else "",
                role="model" if not files else "shard",
            )
        )
    identity = hashlib.sha256(
        f"{repo}@{commit_sha}:{selected[0]}".encode()
    ).hexdigest()[:16]
    display_name = (custom_name or "").strip()[:200] or repo.split("/")[-1]
    quant = extract_quantization(selected[0])
    description = (
        f"{quant} · {selected[0]}" if quant and quant != "GGUF" else selected[0]
    )
    default_api_id = colabone_default_api_model_id(
        repo=repo, filename=selected[0], name=display_name
    )
    return ModelSpec(
        id=f"hf-{identity}",
        api_id=default_api_id,
        name=display_name,
        description=description,
        min_vram_mib=0,
        files=files,
    )
