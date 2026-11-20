"""Data-only model manifests shared by the desktop controller and remote service."""

from __future__ import annotations

import hashlib
import re
from pathlib import PurePosixPath
from typing import Literal

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
        if match.group(3) and not fn and match.group(3).lower().endswith(".gguf"):
            fn = match.group(3)
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

    session = session or requests.Session()
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
    r = session.get(
        f"https://huggingface.co/api/models/{repo}/revision/{revision}",
        params={"blobs": "true"},
        headers=headers,
        timeout=30,
    )
    r.raise_for_status()
    info = r.json()
    siblings = {f["rfilename"]: f for f in info["siblings"]}
    selected = [filename]
    match = re.fullmatch(r"(.+)-\d{5}-of-(\d{5})\.gguf", filename)
    if match:
        count = int(match[2])
        if count > 128:
            raise ValueError("分片数量超过 128")
        selected = [
            f"{match[1]}-{i:05d}-of-{count:05d}.gguf" for i in range(1, count + 1)
        ]
    files = []
    for name in selected:
        item = siblings.get(name, {})
        lfs = item.get("lfs", {})
        files.append(
            ModelFile(
                repo=repo,
                revision=info["sha"],
                path=name,
                size=lfs.get("size", 0),
                sha256=lfs.get("sha256", ""),
                role="model" if not files else "shard",
            )
        )
    identity = hashlib.sha256(
        f"{repo}@{info['sha']}:{selected[0]}".encode()
    ).hexdigest()[:16]
    display_name = (custom_name or "").strip()[:200] or repo.split("/")[-1]
    return ModelSpec(
        id=f"hf-{identity}",
        name=display_name,
        description=selected[0],
        files=files,
    )
