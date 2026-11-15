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
        if not re.fullmatch(r"[\w.-]+/[\w.-]+", value):
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
            or "\\" in value
            or not value.lower().endswith(".gguf")
        ):
            raise ValueError("GGUF 文件路径不合法")
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
    files: list[ModelFile] = Field(min_length=1, max_length=128)

    @model_validator(mode="after")
    def file_set(self):
        if sum(f.role == "model" for f in self.files) != 1:
            raise ValueError("需要且只能有一个入口 GGUF 文件")
        if len({f.path for f in self.files}) != len(self.files):
            raise ValueError("文件路径重复")
        return self


def resolve_import(
    repo: str, filename: str, revision="main", token="", session=None
) -> ModelSpec:
    """Resolve metadata only. The desktop never reads a model's download URL."""
    import requests

    session = session or requests.Session()
    if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
        raise ValueError("请输入 owner/repository")
    if not re.fullmatch(r"[\w.-]+", revision):
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
    return ModelSpec(
        id=f"hf-{identity}",
        name=repo.split("/")[-1],
        description=selected[0],
        files=files,
    )
