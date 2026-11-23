"""Data-only ComfyUI workflow schemas and pure helpers shared by desktop and remote."""

from __future__ import annotations

import copy
import json
import math
import re
from pathlib import PurePosixPath
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from common.public_api import (
    public_origin as _normalize_domain,
    tunnel_token as _extract_tunnel_token,
)

MAX_WORKFLOW_BYTES = 2 * 1024 * 1024  # 2 MiB
MAX_OUTPUT_BYTES = 512 * 1024 * 1024  # 512 MiB
MAX_IMAGE_INPUT_BYTES = 16 * 1024 * 1024  # 16 MiB
MAX_AUDIO_INPUT_BYTES = 64 * 1024 * 1024  # 64 MiB

ALLOWED_OUTPUT_MIME: dict[str, tuple[Literal["image", "video", "audio"], str]] = {
    "png": ("image", "image/png"),
    "jpg": ("image", "image/jpeg"),
    "jpeg": ("image", "image/jpeg"),
    "webp": ("image", "image/webp"),
    "gif": ("image", "image/gif"),
    "mp4": ("video", "video/mp4"),
    "webm": ("video", "video/webm"),
    "wav": ("audio", "audio/wav"),
    "mp3": ("audio", "audio/mpeg"),
    "flac": ("audio", "audio/flac"),
}

ALLOWED_INPUT_EXTENSIONS: set[str] = {
    "png",
    "jpg",
    "jpeg",
    "webp",
    "wav",
    "mp3",
    "flac",
    "ogg",
    "m4a",
}

_MAX_WORKFLOW_NODES = 500
_MAX_AUTO_FIELDS = 32
_NODE_ID_RE = re.compile(r"^[a-zA-Z0-9_:-]{1,80}$")
_REPO_SLUG_RE = re.compile(r"^[\w.-]+/[\w.-]+$")
_GITHUB_REPO_RE = re.compile(r"^https://github\.com/([\w.-]+)/([\w.-]+?)(?:\.git)?/?$")
_ASSET_ID_RE = re.compile(r"^[a-f0-9]{32}$")
_PIP_PKG_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._\-\[\],<>=!~ ]{0,199}$")
_COMFY_MODEL_EXTENSIONS = (".safetensors", ".gguf", ".ckpt", ".pt", ".pth", ".bin")


def _validate_owner_repo(value: str, message: str = "需要 owner/repository") -> str:
    value = value.strip()
    if not _REPO_SLUG_RE.fullmatch(value) or any(
        part in (".", "..") for part in value.split("/")
    ):
        raise ValueError(message)
    return value


def _validate_github_or_slug(value: str) -> str:
    value = value.strip()
    match = _GITHUB_REPO_RE.fullmatch(value)
    if match:
        owner, repo = match.group(1), match.group(2)
        if owner in (".", "..") or repo in (".", ".."):
            raise ValueError("GitHub 仓库地址不合法")
        return value
    return _validate_owner_repo(value, "需要 https://github.com/owner/repo 或 owner/repo")


def _validate_field_key(key: str) -> tuple[str, str]:
    if not isinstance(key, str) or not key or len(key) > 160 or "." not in key:
        raise ValueError(f"非法字段键: {key!r}")
    node_id, input_name = key.split(".", 1)
    if not _NODE_ID_RE.fullmatch(node_id) or not input_name or len(input_name) > 80:
        raise ValueError(f"非法字段键: {key!r}")
    return node_id, input_name


def _is_node_link(value: Any) -> bool:
    return (
        isinstance(value, list)
        and len(value) == 2
        and isinstance(value[0], (str, int))
        and not isinstance(value[0], bool)
        and isinstance(value[1], int)
        and not isinstance(value[1], bool)
    )


def _node_sort_key(node_id: str) -> tuple[int, int, str]:
    if node_id.isdigit():
        return (0, int(node_id), node_id)
    return (1, 0, node_id)


def _node_title(node: dict[str, Any], class_type: str) -> str:
    meta = node.get("_meta")
    raw_title = meta.get("title") if isinstance(meta, dict) else None
    if isinstance(raw_title, str) and raw_title.strip():
        return raw_title.strip()
    return class_type


def _trim_label(text: str) -> str:
    return text[:160]


class ComfyNodeSpec(BaseModel):
    model_config = ConfigDict(extra="forbid")
    name: str = Field(min_length=1, max_length=120)
    repo: str = Field(min_length=1, max_length=300)
    commit: str = Field(pattern=r"^[a-fA-F0-9]{40}$")
    install_requirements: bool = True

    @field_validator("name")
    @classmethod
    def safe_name(cls, value: str) -> str:
        name = value.strip()
        if (
            not name
            or name in (".", "..")
            or ".." in name
            or "/" in name
            or "\\" in name
            or ":" in name
            or "\x00" in name
        ):
            raise ValueError("自定义节点名称不合法")
        return name

    @field_validator("repo")
    @classmethod
    def valid_repo(cls, value: str) -> str:
        return _validate_github_or_slug(value)


class ComfyModelRef(BaseModel):
    model_config = ConfigDict(extra="forbid")
    repo: str
    revision: str = Field(pattern=r"^[a-fA-F0-9]{40}$")
    path: str
    target_dir: Literal[
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
    ]
    size: int = Field(gt=0)
    sha256: str = Field(pattern=r"^[a-fA-F0-9]{64}$")

    @field_validator("repo")
    @classmethod
    def valid_repo(cls, value: str) -> str:
        return _validate_owner_repo(value, "需要 Hugging Face 的 owner/repository")

    @field_validator("path")
    @classmethod
    def relative_file(cls, value: str) -> str:
        p = PurePosixPath(value)
        if (
            not value
            or p.is_absolute()
            or ".." in p.parts
            or "." in p.parts
            or ":" in value
            or "\\" in value
            or "\x00" in value
            or value.endswith("/")
            or len(value) > 512
            or p.suffix.lower() not in _COMFY_MODEL_EXTENSIONS
            or len(p.name) <= len(p.suffix)
        ):
            raise ValueError("模型文件路径不合法")
        return value

    @field_validator("sha256")
    @classmethod
    def normalize_sha256(cls, value: str) -> str:
        return value.lower()


class WorkflowField(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    key: str = Field(min_length=1, max_length=160)
    label: str = Field(min_length=1, max_length=160)
    node_id: str = Field(min_length=1, max_length=80)
    input_name: str = Field(min_length=1, max_length=80)
    field_type: Literal[
        "text",
        "textarea",
        "number",
        "integer",
        "boolean",
        "select",
        "image",
        "audio",
    ]
    default: str | int | float | bool | None = None
    options: list[str] = Field(default_factory=list)
    min_value: float | None = None
    max_value: float | None = None
    step: float | None = None

    @model_validator(mode="after")
    def consistent(self):
        expected_key = f"{self.node_id}.{self.input_name}"
        if self.key != expected_key:
            raise ValueError("字段 key 必须为 {node_id}.{input_name}")
        if (
            self.min_value is not None
            and self.max_value is not None
            and self.min_value > self.max_value
        ):
            raise ValueError("min_value 不能大于 max_value")
        return self


def validate_comfy_api_workflow(workflow: Any) -> dict[str, dict[str, Any]]:
    if not isinstance(workflow, dict):
        raise ValueError("工作流必须为 JSON 对象")
    if not workflow:
        raise ValueError("工作流不能为空")
    if (
        "nodes" in workflow
        and isinstance(workflow["nodes"], list)
        and ("links" in workflow or "version" in workflow)
    ):
        raise ValueError(
            "检测到这是 ComfyUI 画布保存格式，请在 ComfyUI 中开启 Dev Mode 并使用「Save (API Format)」导出后再导入"
        )
    if len(workflow) > _MAX_WORKFLOW_NODES:
        raise ValueError(f"工作流节点数量不能超过 {_MAX_WORKFLOW_NODES} 个")
    try:
        encoded = json.dumps(
            workflow, ensure_ascii=False, allow_nan=False, separators=(",", ":")
        ).encode("utf-8")
    except (TypeError, ValueError) as exc:
        raise ValueError("工作流包含无法序列化的内容") from exc
    if len(encoded) > MAX_WORKFLOW_BYTES:
        raise ValueError(
            f"工作流体积超过限制（最大 {MAX_WORKFLOW_BYTES // (1024 * 1024)} MiB）"
        )

    normalized: dict[str, dict[str, Any]] = {}
    for node_id, node in workflow.items():
        if not isinstance(node_id, str) or not _NODE_ID_RE.fullmatch(node_id):
            raise ValueError(f"非法节点 ID: {node_id!r}")
        if not isinstance(node, dict):
            raise ValueError(f"节点 {node_id} 必须为对象")
        class_type = node.get("class_type")
        if (
            not isinstance(class_type, str)
            or not class_type.strip()
            or len(class_type.strip()) > 160
        ):
            raise ValueError(f"节点 {node_id} 缺少有效的 class_type")
        inputs = node.get("inputs")
        if not isinstance(inputs, dict):
            raise ValueError(f"节点 {node_id} 缺少有效的 inputs 对象")
        if any(not isinstance(k, str) or not k for k in inputs):
            raise ValueError(f"节点 {node_id} 包含非法输入名称")
        node_copy = copy.deepcopy(node)
        node_copy["class_type"] = class_type.strip()
        normalized[node_id] = node_copy
    return normalized


_NUMERIC_SPECS: dict[
    str,
    tuple[
        Literal["integer", "number"],
        str,
        int,
        float | None,
        float | None,
        float | None,
    ],
] = {
    "width": ("integer", "宽度", 10, 64, 4096, 8),
    "height": ("integer", "高度", 11, 64, 4096, 8),
    "length": ("integer", "帧数", 12, 1, None, None),
    "frame_count": ("integer", "帧数", 12, 1, None, None),
    "fps": ("number", "帧率 (FPS)", 13, 1, 60, None),
    "batch_size": ("integer", "批次大小", 14, 1, None, None),
    "steps": ("integer", "采样步数", 20, 1, 200, None),
    "cfg": ("number", "CFG 引导系数", 21, 0, 30, 0.1),
    "denoise": ("number", "重绘幅度", 22, 0, 1, 0.05),
    "seed": ("integer", "随机种子", 23, 0, None, None),
    "noise_seed": ("integer", "随机种子", 23, 0, None, None),
}

_TEXT_INPUT_NAMES = {
    "text",
    "prompt",
    "positive",
    "negative",
    "string",
    "positive_prompt",
    "negative_prompt",
}

_IMAGE_LOADER_INPUT_NAMES = {"image", "image_path", "file", "filename"}
_AUDIO_LOADER_INPUT_NAMES = {"audio", "audio_file", "audio_path", "file", "filename"}


def _prompt_label_and_priority(
    title: str, class_type_lower: str, input_name_lower: str
) -> tuple[str, int]:
    title_lower = title.lower()
    if (
        "negative" in title_lower
        or "反向" in title
        or "负向" in title
        or "negative" in input_name_lower
    ):
        return _trim_label(f"{title} · 反向提示词"), 4
    if (
        "positive" in title_lower
        or "正向" in title
        or "positive" in input_name_lower
        or "prompt" in title_lower
        or "prompt" in input_name_lower
        or "encode" in class_type_lower
    ):
        return _trim_label(f"{title} · 正向提示词"), 2
    return _trim_label(f"{title} · 文本输入"), 3


def extract_workflow_fields(workflow: dict[str, Any]) -> list[WorkflowField]:
    if not isinstance(workflow, dict):
        return []
    extracted: list[WorkflowField] = []
    for node_id in sorted(
        (k for k in workflow if isinstance(k, str)), key=_node_sort_key
    ):
        node = workflow.get(node_id)
        if not isinstance(node, dict):
            continue
        class_type = node.get("class_type")
        inputs = node.get("inputs")
        if (
            not isinstance(class_type, str)
            or not class_type.strip()
            or not isinstance(inputs, dict)
        ):
            continue
        class_type = class_type.strip()
        ct_lower = class_type.lower()
        title = _node_title(node, class_type)

        node_fields: list[tuple[int, str, WorkflowField]] = []
        for input_name, val in inputs.items():
            if not isinstance(input_name, str) or not input_name:
                continue
            if len(node_id) > 80 or len(input_name) > 80:
                continue
            key = f"{node_id}.{input_name}"
            if len(key) > 160 or _is_node_link(val):
                continue

            name_lower = input_name.lower()

            if isinstance(val, str) and (
                (
                    "loadimage" in ct_lower
                    and name_lower != "upload"
                    and (name_lower in _IMAGE_LOADER_INPUT_NAMES or "image" in name_lower)
                )
                or (name_lower == "image" and "load" in ct_lower)
            ):
                node_fields.append(
                    (
                        0,
                        input_name,
                        WorkflowField(
                            key=key,
                            label=_trim_label(f"{title} · 参考图片"),
                            node_id=node_id,
                            input_name=input_name,
                            field_type="image",
                            default=val,
                        ),
                    )
                )
                continue

            if isinstance(val, str) and (
                (
                    "loadaudio" in ct_lower
                    and name_lower not in ("upload", "audioui")
                    and (name_lower in _AUDIO_LOADER_INPUT_NAMES or "audio" in name_lower)
                )
                or (name_lower == "audio" and "load" in ct_lower)
            ):
                node_fields.append(
                    (
                        1,
                        input_name,
                        WorkflowField(
                            key=key,
                            label=_trim_label(f"{title} · 输入音频"),
                            node_id=node_id,
                            input_name=input_name,
                            field_type="audio",
                            default=val,
                        ),
                    )
                )
                continue

            if isinstance(val, str) and (
                any(tok in ct_lower for tok in ("text", "prompt", "string"))
                or name_lower in _TEXT_INPUT_NAMES
            ):
                label, priority = _prompt_label_and_priority(title, ct_lower, name_lower)
                node_fields.append(
                    (
                        priority,
                        input_name,
                        WorkflowField(
                            key=key,
                            label=label,
                            node_id=node_id,
                            input_name=input_name,
                            field_type="textarea",
                            default=val,
                        ),
                    )
                )
                continue

            if (
                name_lower in _NUMERIC_SPECS
                and isinstance(val, (int, float))
                and not isinstance(val, bool)
                and math.isfinite(float(val))
            ):
                field_type, cn_name, priority, min_v, max_v, step_v = _NUMERIC_SPECS[
                    name_lower
                ]
                default_val: int | float = (
                    int(val) if field_type == "integer" else float(val)
                )
                node_fields.append(
                    (
                        priority,
                        input_name,
                        WorkflowField(
                            key=key,
                            label=_trim_label(f"{title} · {cn_name}"),
                            node_id=node_id,
                            input_name=input_name,
                            field_type=field_type,
                            default=default_val,
                            min_value=min_v,
                            max_value=max_v,
                            step=step_v,
                        ),
                    )
                )

        node_fields.sort(key=lambda item: (item[0], item[1]))
        for _, _, field in node_fields:
            extracted.append(field)
            if len(extracted) >= _MAX_AUTO_FIELDS:
                return extracted
    return extracted


def apply_workflow_overrides(
    workflow: dict[str, Any],
    overrides: dict[str, Any] | None = None,
    media_map: dict[str, str] | None = None,
) -> dict[str, Any]:
    result = copy.deepcopy(workflow)

    def _target_inputs(key: str) -> tuple[dict[str, Any], str] | None:
        if not isinstance(key, str) or "." not in key:
            return None
        node_id, input_name = key.split(".", 1)
        node = result.get(node_id)
        if not isinstance(node, dict):
            return None
        inputs = node.get("inputs")
        if not isinstance(inputs, dict) or input_name not in inputs:
            return None
        if _is_node_link(inputs[input_name]):
            return None
        return inputs, input_name

    if overrides:
        for key, value in overrides.items():
            target = _target_inputs(key)
            if target is None:
                continue
            inputs, input_name = target
            if isinstance(value, bool):
                inputs[input_name] = value
            elif isinstance(value, int):
                inputs[input_name] = value
            elif isinstance(value, float):
                if not math.isfinite(value):
                    raise ValueError(f"字段 {key} 包含非法浮点值")
                inputs[input_name] = value
            elif isinstance(value, str):
                inputs[input_name] = value

    if media_map:
        for key, filename in media_map.items():
            if not isinstance(filename, str) or not filename:
                continue
            target = _target_inputs(key)
            if target is None:
                continue
            inputs, input_name = target
            inputs[input_name] = filename

    return result


class WorkflowTemplate(BaseModel):
    model_config = ConfigDict(extra="forbid")
    id: str = Field(pattern=r"^[a-zA-Z0-9_-]{1,80}$")
    name: str = Field(min_length=1, max_length=160)
    description: str = Field(default="", max_length=1000)
    source: Literal["builtin", "custom"] = "custom"
    min_vram_mib: int = Field(default=0, ge=0)
    created_at: float = Field(default=0.0, ge=0)
    updated_at: float = Field(default=0.0, ge=0)
    custom_nodes: list[ComfyNodeSpec] = Field(default_factory=list, max_length=32)
    pip_packages: list[str] = Field(default_factory=list, max_length=64)
    models: list[ComfyModelRef] = Field(default_factory=list, max_length=32)
    workflow: dict[str, Any]
    fields: list[WorkflowField] = Field(default_factory=list, max_length=64)
    node_count: int = Field(default=0, ge=0)

    @field_validator("name")
    @classmethod
    def non_blank_name(cls, value: str) -> str:
        name = value.strip()
        if not name:
            raise ValueError("工作流名称不能为空")
        return name

    @field_validator("pip_packages")
    @classmethod
    def safe_pip_packages(cls, value: list[str]) -> list[str]:
        cleaned: list[str] = []
        for item in value:
            pkg = item.strip()
            if (
                not pkg
                or pkg.startswith("-")
                or "://" in pkg
                or ".." in pkg
                or not _PIP_PKG_RE.fullmatch(pkg)
            ):
                raise ValueError(f"非法的 pip 依赖声明: {item!r}")
            cleaned.append(pkg)
        return cleaned

    @field_validator("workflow")
    @classmethod
    def valid_workflow(cls, value: Any) -> dict[str, dict[str, Any]]:
        return validate_comfy_api_workflow(value)

    @model_validator(mode="after")
    def populate_defaults(self):
        self.node_count = len(self.workflow)
        if not self.fields:
            self.fields = extract_workflow_fields(self.workflow)
        return self


class WorkflowImportRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    id: str = Field(default="", pattern=r"^(?:[a-zA-Z0-9_-]{1,80})?$")
    name: str = Field(min_length=1, max_length=160)
    description: str = Field(default="", max_length=1000)
    workflow: dict[str, Any]

    @field_validator("name")
    @classmethod
    def non_blank_name(cls, value: str) -> str:
        name = value.strip()
        if not name:
            raise ValueError("工作流名称不能为空")
        return name

    @field_validator("workflow")
    @classmethod
    def valid_workflow(cls, value: Any) -> dict[str, dict[str, Any]]:
        return validate_comfy_api_workflow(value)


class WorkflowRunRequest(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    workflow_id: str = Field(pattern=r"^[a-zA-Z0-9_-]{1,80}$")
    overrides: dict[str, str | int | float | bool] = Field(default_factory=dict)
    inputs: dict[str, str] = Field(default_factory=dict)

    @field_validator("overrides")
    @classmethod
    def valid_overrides(
        cls, value: dict[str, str | int | float | bool]
    ) -> dict[str, str | int | float | bool]:
        if len(value) > 128:
            raise ValueError("参数覆盖项数量不能超过 128 个")
        for key in value:
            _validate_field_key(key)
        return value

    @field_validator("inputs")
    @classmethod
    def valid_inputs(cls, value: dict[str, str]) -> dict[str, str]:
        if len(value) > 32:
            raise ValueError("输入素材数量不能超过 32 个")
        for key, asset_id in value.items():
            _validate_field_key(key)
            if not isinstance(asset_id, str) or not _ASSET_ID_RE.fullmatch(asset_id):
                raise ValueError(f"非法素材 ID: {asset_id!r}")
        return value


class WorkflowOutputRecord(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(pattern=r"^[a-f0-9]{32}$")
    workflow_id: str = Field(pattern=r"^[a-zA-Z0-9_-]{1,80}$")
    workflow_name: str = Field(min_length=1, max_length=160)
    kind: Literal["image", "video", "audio"]
    filename: str = Field(min_length=1, max_length=255)
    extension: Literal[
        "png", "jpg", "jpeg", "webp", "gif", "mp4", "webm", "wav", "mp3", "flac"
    ]
    mime: str = Field(min_length=1, max_length=100)
    size: int = Field(gt=0, le=MAX_OUTPUT_BYTES)
    sha256: str = Field(pattern=r"^[a-f0-9]{64}$")
    created_at: float = Field(ge=0)
    elapsed: float = Field(default=0.0, ge=0)
    prompt_id: str = Field(default="", max_length=120)

    @field_validator("filename")
    @classmethod
    def safe_filename(cls, value: str) -> str:
        if (
            value in (".", "..")
            or ".." in value
            or "/" in value
            or "\\" in value
            or "\x00" in value
        ):
            raise ValueError("输出文件名不合法")
        return value

    @model_validator(mode="after")
    def consistent(self):
        expected = ALLOWED_OUTPUT_MIME.get(self.extension)
        if expected != (self.kind, self.mime):
            raise ValueError("输出文件格式与类型不匹配")
        return self


class ComfyCanvasConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")
    mode: Literal["named"] = "named"
    domain: str = Field(default="", max_length=300)
    tunnel_token: str = Field(default="", max_length=8000, repr=False)

    @model_validator(mode="after")
    def valid(self):
        self.domain = _normalize_domain(self.domain)
        self.tunnel_token = _extract_tunnel_token(self.tunnel_token)
        return self


class ComfyCatalog(BaseModel):
    model_config = ConfigDict(extra="forbid")
    repo: str
    tag: str = Field(min_length=1, max_length=120)
    commit: str = Field(pattern=r"^[a-fA-F0-9]{40}$")
    port: int = Field(default=8188, ge=1024, le=65535)
    proxy_port: int = Field(default=8189, ge=1024, le=65535)
    templates: list[WorkflowTemplate] = Field(default_factory=list)

    @field_validator("repo")
    @classmethod
    def valid_repo(cls, value: str) -> str:
        return _validate_github_or_slug(value)

    @model_validator(mode="after")
    def valid_catalog(self):
        if self.port == self.proxy_port:
            raise ValueError("ComfyUI 端口与代理端口不能相同")
        if len({t.id for t in self.templates}) != len(self.templates):
            raise ValueError("内置工作流模板 ID 重复")
        return self
