"""Hugging Face metadata only: model weights are fetched exclusively by the GPU."""

from __future__ import annotations
import re
from urllib.parse import urlparse, parse_qs
import requests

API = "https://huggingface.co/api/models"
SORTS = {"downloads": "downloads", "updated": "lastModified", "likes": "likes"}
MODALITIES = frozenset({"all", "text", "image", "video"})

_VIDEO_PIPELINES = frozenset(
    {"text-to-video", "image-to-video", "video-to-video", "any-to-video"}
)
_IMAGE_PIPELINES = frozenset(
    {
        "text-to-image",
        "image-to-image",
        "unconditional-image-generation",
        "inpainting",
    }
)
_TEXT_PIPELINES = frozenset(
    {
        "text-generation",
        "conversational",
        "image-text-to-text",
        "text2text-generation",
    }
)
_NON_CHAT_PIPELINES = frozenset(
    {
        "feature-extraction",
        "sentence-similarity",
        "fill-mask",
        "token-classification",
        "text-classification",
        "automatic-speech-recognition",
        "text-to-speech",
        "text-to-audio",
        "audio-classification",
        "image-classification",
        "object-detection",
    }
)
_VIDEO_REPO_RE = re.compile(
    r"(?:^|[-_/.])(?:wan2(?:\.[12])?|wan[-_]?2|ltx[-_]?video|ltx[-_]?2|"
    r"hunyuan[-_]?video|cogvideox?|mochi[-_]?1|skyreels|framepack|"
    r"animatediff|t2v|i2v|v2v)(?:$|[-_/.])",
    re.I,
)
_IMAGE_REPO_RE = re.compile(
    r"(?:^|[-_/.])(?:flux(?:\.?[12])?|sdxl|sd3(?:\.?5)?|stable[-_]?diffusion|"
    r"z[-_]?image|qwen[-_]?image|hidream|lumina[-_]?image|kolors|pixart|"
    r"auraflow|chroma|kontext|omnigen)(?:$|[-_/.])",
    re.I,
)
_FAMILY_PATTERNS: tuple[tuple[str, re.Pattern[str]], ...] = (
    ("Qwen", re.compile(r"qwen|qwq", re.I)),
    ("DeepSeek", re.compile(r"deepseek", re.I)),
    ("GLM", re.compile(r"(?:^|[-_/])glm[-_.\d]|chatglm", re.I)),
    ("Gemma", re.compile(r"gemma", re.I)),
    ("Llama", re.compile(r"llama", re.I)),
    ("Mistral", re.compile(r"mistral|mixtral|pixtral|devstral|magistral", re.I)),
    ("FLUX", re.compile(r"(?:^|[-_/])flux(?:\.?[12]|[-_.]|$)", re.I)),
    ("Wan", re.compile(r"(?:^|[-_/])wan2(?:\.[12])?|wan[-_]?2", re.I)),
    ("LTX", re.compile(r"(?:^|[-_/])ltx[-_]?(?:video|2)", re.I)),
    ("Hunyuan", re.compile(r"hunyuan", re.I)),
)
_PARAMS_RE = re.compile(
    r"(?:^|[-_/.])(\d+(?:\.\d+)?[BM](?:-A\d+(?:\.\d+)?[BM])?)(?=$|[-_/.])",
    re.I,
)
_QUANT_PATTERNS: tuple[re.Pattern[str], ...] = (
    re.compile(r"(?<![A-Za-z0-9])(UD-(?:IQ|Q|TQ)\d[A-Za-z0-9_]*)(?![A-Za-z0-9])", re.I),
    re.compile(
        r"(?<![A-Za-z0-9])((?:IQ|Q|TQ)\d(?:_[01K](?:_[A-Za-z0-9]+)?|_[A-Za-z]{1,4}))(?![A-Za-z0-9])",
        re.I,
    ),
    re.compile(
        r"(?<![A-Za-z0-9])(MXFP4(?:_MOE)?|FP8(?:_E4M3(?:FN)?|_E5M2)?|BF16|F16|F32)(?![A-Za-z0-9])",
        re.I,
    ),
    re.compile(r"(?<![A-Za-z0-9])(Q[2-8])(?![A-Za-z0-9])", re.I),
)


def extract_quantization(path: str) -> str:
    """Extract a normalized GGUF quantization label from a filename or parent directory."""
    if not path:
        return "GGUF"
    parts = [p for p in path.split("/") if p]
    if not parts:
        return "GGUF"
    base = re.sub(r"(?:-\d{5}-of-\d{5})?\.gguf$", "", parts[-1], flags=re.I)
    candidates = [base]
    if len(parts) > 1:
        candidates.append("/".join(parts[:-1]))
    for text in candidates:
        for pattern in _QUANT_PATTERNS:
            matches = pattern.findall(text)
            if matches:
                return matches[-1].upper()
    return "GGUF"


def is_auxiliary_gguf(path: str) -> bool:
    """Return True for non-runnable auxiliary GGUF artifacts (mmproj, imatrix, vocab, lora)."""
    base = path.rsplit("/", 1)[-1].lower()
    if re.search(
        r"(?:^|[._-])(?:mmproj|projector|vision[-_]?proj)(?:[._-]|\.gguf$)", base
    ):
        return True
    if re.match(r"^imatrix(?:[._\d-]|\.gguf$)", base):
        return True
    if re.search(r"[._-]imatrix(?:[._-]?(?:micro\d*|v\d+))?\.gguf$", base):
        stem = re.sub(r"\.gguf$", "", base)
        if extract_quantization(stem) == "GGUF":
            return True
    if re.match(r"^(?:vocab|tokenizer|adapter)(?:[._-]|\.gguf$)", base):
        return True
    if re.search(r"[._-]lora\.gguf$", base):
        return True
    return False


def classify_modality(
    pipeline_tag: str = "", tags: list[str] | None = None, repo: str = ""
) -> str:
    """Classify a Hugging Face repo as text, image, or video."""
    pipeline = (pipeline_tag or "").strip().lower()
    if pipeline in _VIDEO_PIPELINES:
        return "video"
    if pipeline in _IMAGE_PIPELINES:
        return "image"
    if pipeline in _TEXT_PIPELINES:
        return "text"
    tag_set = {str(t).strip().lower() for t in (tags or []) if isinstance(t, str)}
    if tag_set & _VIDEO_PIPELINES:
        return "video"
    if tag_set & _IMAGE_PIPELINES:
        return "image"
    repo_name = (repo or "").strip()
    if _VIDEO_REPO_RE.search(repo_name):
        return "video"
    if _IMAGE_REPO_RE.search(repo_name):
        return "image"
    return "text"


def infer_family(repo: str) -> str:
    """Infer a recognizable model family tag from a repository slug."""
    short = (repo or "").rsplit("/", 1)[-1]
    for label, pattern in _FAMILY_PATTERNS:
        if pattern.search(short):
            return label
    return ""


def infer_params(repo: str) -> str:
    """Infer a human-readable parameter scale badge (e.g. 27B, 125B-A6B) from repo slug."""
    short = (repo or "").rsplit("/", 1)[-1]
    match = _PARAMS_RE.search(short)
    return match.group(1).upper() if match else ""


def request(path="", params=None, token="", session=None):
    try:
        response = (session or requests).get(
            API + path,
            params=params,
            headers={"Authorization": "Bearer " + token} if token else {},
            timeout=20,
        )
        if response.status_code in (401, 403):
            raise RuntimeError(
                "这个仓库需要访问权限，请在设置中保存有权限的 Hugging Face Token"
            )
        if response.status_code == 429:
            raise RuntimeError("Hugging Face 请求较多，请稍后重试")
        response.raise_for_status()
        return response
    except requests.RequestException:
        raise RuntimeError("无法连接 Hugging Face，请检查网络后重试") from None


def search_models(
    query="",
    sort="downloads",
    cursor="",
    token="",
    session=None,
    modality="all",
    family="",
):
    if not isinstance(query, str) or len(query) > 200 or sort not in SORTS:
        raise ValueError("模型搜索条件不合法")
    if not isinstance(cursor, str) or len(cursor) > 4096:
        raise ValueError("搜索分页不合法")
    norm_modality = str(modality or "all").strip().lower()
    if norm_modality not in MODALITIES:
        raise ValueError("模型类型筛选条件不合法")
    if not isinstance(family, str) or len(family) > 64:
        raise ValueError("模型系列筛选条件不合法")
    clean_query = query.strip()
    clean_family = family.strip()
    if clean_family and clean_family.lower() not in clean_query.lower():
        effective_query = f"{clean_family} {clean_query}".strip()
    else:
        effective_query = clean_query
    params: dict = {
        "search": effective_query,
        "filter": "gguf",
        "sort": SORTS[sort],
        "direction": -1,
        "limit": 30 if norm_modality == "text" else 20,
    }
    if norm_modality == "image":
        params["pipeline_tag"] = "text-to-image"
    elif norm_modality == "video":
        params["pipeline_tag"] = "text-to-video"
    if cursor:
        params["cursor"] = cursor
    response = request(params=params, token=token, session=session)
    try:
        items = response.json()
    except ValueError:
        raise RuntimeError("模型社区返回了无效结果") from None
    if not isinstance(items, list):
        raise RuntimeError("模型社区返回了无效结果")
    models = []
    for item in items:
        repo = item.get("id", "")
        if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
            continue
        pipeline = str(item.get("pipeline_tag", "") or "")
        tags = item.get("tags") if isinstance(item.get("tags"), list) else []
        item_modality = classify_modality(pipeline, tags, repo)
        if norm_modality == "image":
            item_modality = "image"
        elif norm_modality == "video":
            item_modality = "video"
        elif norm_modality == "text":
            if item_modality != "text" or pipeline.strip().lower() in _NON_CHAT_PIPELINES:
                continue
        models.append(
            {
                "repo": repo,
                "downloads": item.get("downloads", 0),
                "likes": item.get("likes", 0),
                "updated": item.get("lastModified", ""),
                "gated": bool(item.get("gated")),
                "pipeline": pipeline,
                "modality": item_modality,
                "comfy_only": item_modality in ("image", "video"),
                "family": infer_family(repo),
                "params": infer_params(repo),
            }
        )
    next_url = response.links.get("next", {}).get("url", "")
    parsed = urlparse(next_url)
    next_cursor = ""
    if (
        parsed.scheme == "https"
        and parsed.netloc == "huggingface.co"
        and parsed.path == "/api/models"
    ):
        next_cursor = parse_qs(parsed.query).get("cursor", [""])[0]
    return {"models": models, "next_cursor": next_cursor}


def model_files(repo, token="", session=None):
    if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
        raise ValueError("模型仓库不合法")
    resp = request("/" + repo + "/revision/main", {"blobs": "true"}, token, session)
    try:
        info = resp.json()
    except ValueError:
        raise RuntimeError("模型社区返回了无效结果") from None
    if not isinstance(info, dict):
        raise RuntimeError("模型社区返回了无效结果")
    revision = str(info.get("sha") or "")
    if not re.fullmatch(r"[a-fA-F0-9]{40}", revision):
        raise RuntimeError("模型社区未返回固定版本，请重试")
    pipeline = str(info.get("pipeline_tag", "") or "")
    tags = info.get("tags") if isinstance(info.get("tags"), list) else []
    item_modality = classify_modality(pipeline, tags, repo)
    siblings = {
        item["rfilename"]: item
        for item in (info.get("siblings") or [])
        if isinstance(item, dict) and isinstance(item.get("rfilename"), str) and item.get("rfilename")
    }
    files = []
    for name, item in siblings.items():
        if not name.lower().endswith(".gguf") or is_auxiliary_gguf(name):
            continue
        match = re.fullmatch(r"(.+)-(\d{5})-of-(\d{5})\.gguf", name, re.IGNORECASE)
        if match and match[2] != "00001":
            continue
        count = int(match[3]) if match else 1
        suffix = name[-5:]
        group = (
            [f"{match[1]}-{i:05d}-of-{count:05d}{suffix}" for i in range(1, count + 1)]
            if match and 1 <= count <= 128
            else [name]
        )
        metadata = [
            (siblings.get(path) or {}).get("lfs")
            if isinstance((siblings.get(path) or {}).get("lfs"), dict)
            else {}
            for path in group
        ]
        supported = 0 < count <= 128 and all(
            isinstance(m.get("size"), int)
            and not isinstance(m.get("size"), bool)
            and m.get("size", 0) > 0
            and isinstance(m.get("sha256"), str)
            and re.fullmatch(r"[a-fA-F0-9]{64}", m.get("sha256", ""))
            for m in metadata
        )
        quant = extract_quantization(name)
        files.append(
            {
                "filename": name,
                "size": sum(
                    m["size"]
                    for m in metadata
                    if isinstance(m.get("size"), int)
                    and not isinstance(m.get("size"), bool)
                    and m["size"] > 0
                ),
                "shards": count,
                "quantization": quant,
                "supported": bool(supported),
                "reason": "" if supported else "缺少完整分片或 SHA-256 元数据",
            }
        )
    return {
        "repo": repo,
        "revision": revision,
        "pipeline": pipeline,
        "modality": item_modality,
        "comfy_only": item_modality in ("image", "video"),
        "family": infer_family(repo),
        "params": infer_params(repo),
        "files": sorted(files, key=lambda f: (f["size"], f["filename"])),
    }
