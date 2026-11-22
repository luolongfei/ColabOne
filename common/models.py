"""Data-only model manifests shared by the desktop controller and remote service."""

from __future__ import annotations

import hashlib
import re
from pathlib import PurePosixPath
from typing import Literal
from urllib.parse import unquote

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


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
    name: str = Field(min_length=1, max_length=200)
    description: str = ""
    min_vram_mib: int = Field(default=0, ge=0)
    context_length: int = Field(default=8192, ge=512, le=262144)
    temperature: float = Field(default=0.7, ge=0, le=2)
    cpu_moe_layers: int = Field(default=0, ge=0, le=256)
    source: str = "Hugging Face"
    kind: Literal["chat"] = "chat"
    files: list[ModelFile] = Field(min_length=1, max_length=128)

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
            "该模型属于图片/视频生成类（ComfyUI 专用），无法在对外文本 API 中装载，请前往 ComfyUI 工作区使用"
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
    return ModelSpec(
        id=f"hf-{identity}",
        name=display_name,
        description=description,
        min_vram_mib=0,
        files=files,
    )
