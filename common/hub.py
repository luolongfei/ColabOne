"""Hugging Face metadata only: model weights are fetched exclusively by the GPU."""

from __future__ import annotations
import re
from urllib.parse import urlparse, parse_qs
import requests

API = "https://huggingface.co/api/models"
SORTS = {"downloads": "downloads", "updated": "lastModified", "likes": "likes"}


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


def search_models(query="", sort="downloads", cursor="", token="", session=None):
    if not isinstance(query, str) or len(query) > 200 or sort not in SORTS:
        raise ValueError("模型搜索条件不合法")
    if not isinstance(cursor, str) or len(cursor) > 4096:
        raise ValueError("搜索分页不合法")
    params = {
        "search": query.strip(),
        "filter": "gguf",
        "sort": SORTS[sort],
        "direction": -1,
        "limit": 20,
    }
    if cursor:
        params["cursor"] = cursor
    response = request(params=params, token=token, session=session)
    items = response.json()
    if not isinstance(items, list):
        raise RuntimeError("模型社区返回了无效结果")
    models = []
    for item in items:
        repo = item.get("id", "")
        if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
            continue
        models.append(
            {
                "repo": repo,
                "downloads": item.get("downloads", 0),
                "likes": item.get("likes", 0),
                "updated": item.get("lastModified", ""),
                "gated": bool(item.get("gated")),
                "pipeline": item.get("pipeline_tag", ""),
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
    info = request(
        "/" + repo + "/revision/main", {"blobs": "true"}, token, session
    ).json()
    revision = info.get("sha", "")
    if not re.fullmatch(r"[a-fA-F0-9]{40}", revision):
        raise RuntimeError("模型社区未返回固定版本，请重试")
    siblings = {
        item["rfilename"]: item
        for item in info.get("siblings", [])
        if item.get("rfilename")
    }
    files = []
    for name, item in siblings.items():
        if not name.lower().endswith(".gguf") or re.search(
            r"(?:^|/)(?:mmproj|projector)", name, re.I
        ):
            continue
        match = re.fullmatch(r"(.+)-(\d{5})-of-(\d{5})\.gguf", name)
        if match and match[2] != "00001":
            continue
        count = int(match[3]) if match else 1
        group = (
            [f"{match[1]}-{i:05d}-of-{count:05d}.gguf" for i in range(1, count + 1)]
            if match and count <= 128
            else [name]
        )
        metadata = [siblings.get(path, {}).get("lfs", {}) for path in group]
        supported = 0 < count <= 128 and all(
            m.get("size", 0) > 0
            and re.fullmatch(r"[a-fA-F0-9]{64}", m.get("sha256", ""))
            for m in metadata
        )
        quant = re.search(
            r"(?:BF16|F16|F32|(?:IQ|Q|TQ)\d[^/]*?)(?=(?:-\d{5}-of-\d{5})?\.gguf$)",
            name,
            re.I,
        )
        files.append(
            {
                "filename": name,
                "size": sum(m.get("size", 0) for m in metadata),
                "shards": count,
                "quantization": quant[0] if quant else "GGUF",
                "supported": bool(supported),
                "reason": "" if supported else "缺少完整分片或 SHA-256 元数据",
            }
        )
    return {
        "repo": repo,
        "revision": revision,
        "files": sorted(files, key=lambda f: (f["size"], f["filename"])),
    }
