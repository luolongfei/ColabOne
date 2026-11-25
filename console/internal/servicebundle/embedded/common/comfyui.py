"""Data-only ComfyUI workflow schemas and pure helpers shared by desktop and remote."""

from __future__ import annotations

import copy
import json
import math
import re
from pathlib import PurePosixPath
from typing import Any, Literal

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StrictBool,
    StrictStr,
    field_validator,
    model_validator,
)

from common.public_api import (
    public_origin as _normalize_domain,
    tunnel_token as _extract_tunnel_token,
)

MAX_WORKFLOW_BYTES = 2 * 1024 * 1024  # 2 MiB
MAX_WORKFLOW_NESTING_DEPTH = 32
MAX_OUTPUT_BYTES = 512 * 1024 * 1024  # 512 MiB
MAX_IMAGE_INPUT_BYTES = 16 * 1024 * 1024  # 16 MiB
MAX_AUDIO_INPUT_BYTES = 64 * 1024 * 1024  # 64 MiB
MAX_OVERRIDE_STRING_LENGTH = 200_000

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

ALLOWED_MODEL_TARGET_DIRS: tuple[str, ...] = (
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

_MAX_WORKFLOW_NODES = 500
_MAX_AUTO_FIELDS = 32
_NODE_ID_RE = re.compile(r"^[a-zA-Z0-9_:-]{1,80}$", re.ASCII)
_WORKFLOW_ID_RE = re.compile(r"^[a-zA-Z0-9_-]{1,80}$", re.ASCII)
_REPO_SLUG_RE = re.compile(r"^[\w.-]+/[\w.-]+$", re.ASCII)
_GITHUB_REPO_RE = re.compile(
    r"^https://github\.com/([\w.-]+)/([\w.-]+?)(?:\.git)?/?$", re.ASCII
)
_ASSET_ID_RE = re.compile(r"^[a-f0-9]{32}$", re.ASCII)
_PIP_PKG_RE = re.compile(
    r"^(?!.*--)(?!.*\s-)[A-Za-z0-9][A-Za-z0-9._\-\[\],<>=!~ ]{0,199}$",
    re.ASCII,
)
_COMFY_MODEL_EXTENSIONS = (".safetensors", ".gguf", ".ckpt", ".pt", ".pth", ".bin")


def _validate_safe_node_name(value: str) -> str:
    name = value.strip()
    if (
        not name
        or name.startswith(".")
        or name in (".", "..")
        or ".." in name
        or "/" in name
        or "\\" in name
        or ":" in name
        or "\x00" in name
    ):
        raise ValueError("自定义节点名称不合法")
    return name


def _validate_pip_packages(value: list[str]) -> list[str]:
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
    if (
        (node_id != "pipeline" and not _NODE_ID_RE.fullmatch(node_id))
        or not input_name
        or len(input_name) > 80
    ):
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
    if node_id.isascii() and node_id.isdigit():
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
        return _validate_safe_node_name(value)

    @field_validator("repo")
    @classmethod
    def valid_repo(cls, value: str) -> str:
        return _validate_github_or_slug(value)


class ComfyNodePatch(BaseModel):
    model_config = ConfigDict(extra="forbid")
    node_name: str = Field(min_length=1, max_length=120)
    kind: Literal["wav2vec2_hidden_states_hook"]

    @field_validator("node_name")
    @classmethod
    def safe_node_name(cls, value: str) -> str:
        return _validate_safe_node_name(value)


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


class ComfyTtsVoicePreset(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(min_length=1, max_length=64, pattern=r"^[a-zA-Z0-9_-]{1,64}$")
    name: str = Field(min_length=1, max_length=80)
    gender: Literal["female", "male", "neutral"] = "female"
    style: str = Field(default="", max_length=80)
    description: str = Field(default="", max_length=240)
    prompt: str = Field(default="", max_length=600)
    preview_text: str = Field(default="", max_length=400)
    speaker_id: str = Field(default="", max_length=64, pattern=r"^(?:[a-zA-Z0-9_-]{1,64})?$")
    seed: int = Field(default=42, ge=0, le=2147483647)

    @field_validator("name")
    @classmethod
    def check_name(cls, value: str) -> str:
        cleaned = value.strip()
        if not cleaned:
            raise ValueError("内置音色名称不能为空")
        return cleaned


class ComfyTtsEngineSpec(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(pattern=r"^[a-zA-Z0-9_-]{1,80}$")
    name: str = Field(min_length=1, max_length=160)
    description: str = Field(default="", max_length=1000)
    repo: str = Field(min_length=1, max_length=300)
    revision: str = Field(pattern=r"^[a-fA-F0-9]{40}$")
    aux_repo: str = Field(default="", max_length=300)
    aux_revision: str = Field(default="", pattern=r"^(?:[a-fA-F0-9]{40})?$")
    custom_voice_repo: str = Field(default="", max_length=300)
    custom_voice_revision: str = Field(default="", pattern=r"^(?:[a-fA-F0-9]{40})?$")
    license: str = Field(default="Apache-2.0", min_length=1, max_length=160)
    commercial_ok: bool = True
    license_notice: str = Field(default="", max_length=600)
    min_vram_mib: int = Field(default=0, ge=0)
    min_disk_mib: int = Field(default=0, ge=0)
    sample_rate: int = Field(default=48000, ge=8000, le=96000)
    max_segment_chars: int = Field(default=120, ge=40, le=1000)
    supports_voice_clone: bool = True
    supports_voice_design: bool = False
    supports_emotion_ref: bool = False
    supports_inline_tags: bool = False
    supports_custom_speakers: bool = False
    pip_packages: list[str] = Field(default_factory=list, max_length=64)
    source_repo: str = Field(default="", max_length=300)
    source_commit: str = Field(default="", pattern=r"^(?:[a-fA-F0-9]{40})?$")
    voice_presets: list[ComfyTtsVoicePreset] = Field(default_factory=list, max_length=24)

    @field_validator("name")
    @classmethod
    def non_blank_name(cls, value: str) -> str:
        name = value.strip()
        if not name:
            raise ValueError("TTS 引擎名称不能为空")
        return name

    @field_validator("license")
    @classmethod
    def non_blank_license(cls, value: str) -> str:
        lic = value.strip()
        if not lic:
            raise ValueError("许可协议名称不能为空")
        return lic

    @field_validator("repo")
    @classmethod
    def valid_repo(cls, value: str) -> str:
        return _validate_owner_repo(value, "需要 Hugging Face 的 owner/repository")

    @field_validator("aux_repo")
    @classmethod
    def valid_aux_repo(cls, value: str) -> str:
        if not value:
            return ""
        return _validate_owner_repo(value, "需要 Hugging Face 的 owner/repository")

    @field_validator("custom_voice_repo")
    @classmethod
    def valid_custom_voice_repo(cls, value: str) -> str:
        if not value:
            return ""
        return _validate_owner_repo(value, "需要 Hugging Face 的 owner/repository")

    @field_validator("pip_packages")
    @classmethod
    def safe_pip_packages(cls, value: list[str]) -> list[str]:
        return _validate_pip_packages(value)

    @field_validator("source_repo")
    @classmethod
    def valid_source_repo(cls, value: str) -> str:
        if not value:
            return ""
        return _validate_github_or_slug(value)

    @model_validator(mode="after")
    def consistent_aux(self):
        if self.aux_repo and not self.aux_revision:
            raise ValueError("设置 aux_repo 时必须提供 40 位十六进制 aux_revision")
        if self.custom_voice_repo and not self.custom_voice_revision:
            raise ValueError(
                "设置 custom_voice_repo 时必须提供 40 位十六进制 custom_voice_revision"
            )
        if self.source_repo and not self.source_commit:
            raise ValueError("设置 source_repo 时必须提供 40 位十六进制 source_commit")
        seen_presets: set[str] = set()
        for preset in self.voice_presets:
            if preset.id in seen_presets:
                raise ValueError(f"内置音色 ID 重复：{preset.id}")
            seen_presets.add(preset.id)
        return self


class ComfyPipelineSpec(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    kind: Literal["comfy_standard", "digital_human"] = "comfy_standard"
    video_backend: Literal[
        "infinitetalk", "longcat_avatar", "longcat_avatar_native", "custom"
    ] = "custom"
    default_tts_engine: str = Field(
        default="voxcpm2", max_length=80, pattern=r"^(?:[a-zA-Z0-9_-]{1,80})?$"
    )
    supported_tts_engines: list[str] = Field(default_factory=list, max_length=16)
    max_segment_seconds: float = Field(default=45.0, ge=3.0, le=300.0)
    tail_frame_offset_seconds: float = Field(default=0.08, ge=0.0, le=1.0)
    output_fps: int = Field(default=25, ge=1, le=60)
    target_width: int = Field(default=1280, ge=64, le=4096)
    target_height: int = Field(default=720, ge=64, le=4096)
    upscale_mode: Literal[
        "none",
        "ai_2x",
        "lanczos",
        "auto",
        "seedvr2_7b",
        "seedvr2_7b_sharp",
        "flashvsr_plus",
    ] = "none"
    audio_node_key: str = Field(default="", max_length=160)
    image_node_key: str = Field(default="", max_length=160)
    prompt_node_key: str = Field(default="", max_length=160)
    frames_node_key: str = Field(default="", max_length=160)
    width_node_key: str = Field(default="", max_length=160)
    height_node_key: str = Field(default="", max_length=160)
    fps_node_key: str = Field(default="", max_length=160)
    steps_node_key: str = Field(default="", max_length=160)
    seed_node_key: str = Field(default="", max_length=160)

    @field_validator("supported_tts_engines")
    @classmethod
    def valid_supported_tts_engines(cls, value: list[str]) -> list[str]:
        for engine_id in value:
            if not isinstance(engine_id, str) or not _WORKFLOW_ID_RE.fullmatch(engine_id):
                raise ValueError(f"非法的 TTS 引擎 ID: {engine_id!r}")
        return value

    @field_validator(
        "audio_node_key",
        "image_node_key",
        "prompt_node_key",
        "frames_node_key",
        "width_node_key",
        "height_node_key",
        "fps_node_key",
        "steps_node_key",
        "seed_node_key",
    )
    @classmethod
    def valid_node_key(cls, value: str) -> str:
        if not value:
            return ""
        _validate_field_key(value)
        return value

    @model_validator(mode="after")
    def consistent_pipeline(self):
        if (
            self.video_backend == "longcat_avatar_native"
            and self.kind != "digital_human"
        ):
            raise ValueError(
                "video_backend 为 longcat_avatar_native 时流水线类型必须为 digital_human"
            )
        return self


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
    options: list[str] = Field(default_factory=list, max_length=128)
    min_value: float | None = None
    max_value: float | None = None
    step: float | None = None
    description: str = Field(default="", max_length=400)
    group: str = Field(default="", max_length=64)
    required: bool = False
    visible_when: dict[str, StrictStr | StrictBool] | None = None
    option_labels: dict[str, str] = Field(default_factory=dict, max_length=128)

    @field_validator("visible_when", mode="before")
    @classmethod
    def valid_visible_when(
        cls, value: Any
    ) -> dict[str, str | bool] | None:
        if value is None:
            return None
        if not isinstance(value, dict):
            raise ValueError("visible_when 必须为对象")
        if len(value) > 16:
            raise ValueError("visible_when 条件数量不能超过 16 个")
        for key, cond in value.items():
            _validate_field_key(key)
            if not isinstance(cond, (str, bool)):
                raise ValueError(f"visible_when 条件 {key} 的值必须为字符串或布尔值")
            if isinstance(cond, str) and len(cond) > 160:
                raise ValueError(f"visible_when 条件 {key} 的字符串值长度不能超过 160")
        return value

    @model_validator(mode="after")
    def consistent(self):
        expected_key = f"{self.node_id}.{self.input_name}"
        if self.key != expected_key:
            raise ValueError("字段 key 必须为 {node_id}.{input_name}")
        parsed_node_id, parsed_input_name = _validate_field_key(self.key)
        if parsed_node_id != self.node_id or parsed_input_name != self.input_name:
            raise ValueError(f"非法字段键: {self.key!r}")
        if (
            self.min_value is not None
            and self.max_value is not None
            and self.min_value > self.max_value
        ):
            raise ValueError("min_value 不能大于 max_value")
        if self.step is not None and self.step <= 0:
            raise ValueError("step 必须大于 0")
        if len(self.options) > 128:
            raise ValueError("选项数量不能超过 128 个")
        if len(self.option_labels) > 128:
            raise ValueError("选项标签数量不能超过 128 个")
        if self.default is not None:
            if self.field_type == "boolean":
                if not isinstance(self.default, bool):
                    raise ValueError(f"布尔字段 {self.key} 的默认值必须为布尔值")
            elif self.field_type == "integer":
                if isinstance(self.default, bool):
                    raise ValueError(f"整数字段 {self.key} 的默认值不能为布尔值")
                if isinstance(self.default, float):
                    if not math.isfinite(self.default) or not self.default.is_integer():
                        raise ValueError(f"整数字段 {self.key} 的默认值必须为整数")
                elif isinstance(self.default, str):
                    if not re.fullmatch(r"[+-]?\d+", self.default.strip()):
                        raise ValueError(f"整数字段 {self.key} 的默认值必须为整数")
                elif not isinstance(self.default, int):
                    raise ValueError(f"整数字段 {self.key} 的默认值必须为整数")
            elif self.field_type == "number":
                if isinstance(self.default, bool):
                    raise ValueError(f"数值字段 {self.key} 的默认值不能为布尔值")
                if isinstance(self.default, str):
                    try:
                        parsed_num = float(self.default.strip())
                    except ValueError:
                        raise ValueError(f"数值字段 {self.key} 的默认值必须为数字") from None
                    if not math.isfinite(parsed_num):
                        raise ValueError(f"数值字段 {self.key} 的默认值必须为有限数字")
                elif isinstance(self.default, (int, float)):
                    if not math.isfinite(float(self.default)):
                        raise ValueError(f"数值字段 {self.key} 的默认值必须为有限数字")
                else:
                    raise ValueError(f"数值字段 {self.key} 的默认值必须为数字")
        if (
            self.field_type == "select"
            and self.options
            and isinstance(self.default, str)
            and self.default != ""
            and self.default not in self.options
        ):
            raise ValueError(f"下拉字段 {self.key} 的默认值不在 options 列表中")
        return self


def _check_workflow_nesting_depth(
    value: Any, max_depth: int = MAX_WORKFLOW_NESTING_DEPTH
) -> None:
    """Iteratively cap JSON container nesting depth before serialization/deepcopy."""
    stack: list[tuple[Any, int]] = [(value, 1)]
    while stack:
        current, depth = stack.pop()
        if isinstance(current, dict):
            children = current.values()
        elif isinstance(current, (list, tuple)):
            children = current
        else:
            continue
        if depth > max_depth:
            raise ValueError(f"工作流 JSON 嵌套层级超过 {max_depth} 层上限")
        next_depth = depth + 1
        for child in children:
            if isinstance(child, (dict, list, tuple)):
                if next_depth > max_depth:
                    raise ValueError(f"工作流 JSON 嵌套层级超过 {max_depth} 层上限")
                stack.append((child, next_depth))


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
    _check_workflow_nesting_depth(workflow, MAX_WORKFLOW_NESTING_DEPTH)
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

_NON_PROMPT_STRING_INPUT_NAMES = {
    "model_name",
    "precision",
    "quantization",
    "device",
    "load_device",
    "base_precision",
    "attention_mode",
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
        if node_id != "pipeline" and not _NODE_ID_RE.fullmatch(node_id):
            continue
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

            if (
                isinstance(val, str)
                and name_lower not in _NON_PROMPT_STRING_INPUT_NAMES
                and (
                    any(tok in ct_lower for tok in ("text", "prompt", "string"))
                    or name_lower in _TEXT_INPUT_NAMES
                )
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
                existing = inputs[input_name]
                if not isinstance(existing, bool):
                    raise ValueError(f"字段 {key} 不接受布尔值覆盖")
                inputs[input_name] = value
            elif isinstance(value, int):
                inputs[input_name] = value
            elif isinstance(value, float):
                if not math.isfinite(value):
                    raise ValueError(f"字段 {key} 包含非法浮点值")
                existing = inputs[input_name]
                if (
                    isinstance(existing, int)
                    and not isinstance(existing, bool)
                    and value.is_integer()
                ):
                    inputs[input_name] = int(value)
                else:
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
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(pattern=r"^[a-zA-Z0-9_-]{1,80}$")
    name: str = Field(min_length=1, max_length=160)
    description: str = Field(default="", max_length=1000)
    source: Literal["builtin", "custom"] = "custom"
    min_vram_mib: int = Field(default=0, ge=0)
    min_disk_mib: int = Field(default=0, ge=0)
    recommended_gpu: str = Field(default="", max_length=120)
    license: str = Field(default="", max_length=160)
    commercial_ok: bool = True
    license_notice: str = Field(default="", max_length=600)
    created_at: float = Field(default=0.0, ge=0)
    updated_at: float = Field(default=0.0, ge=0)
    custom_nodes: list[ComfyNodeSpec] = Field(default_factory=list, max_length=32)
    node_patches: list[ComfyNodePatch] = Field(default_factory=list, max_length=16)
    pip_packages: list[str] = Field(default_factory=list, max_length=64)
    models: list[ComfyModelRef] = Field(default_factory=list, max_length=32)
    pipeline: ComfyPipelineSpec | None = None
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
        return _validate_pip_packages(value)

    @field_validator("workflow")
    @classmethod
    def valid_workflow(cls, value: Any) -> dict[str, dict[str, Any]]:
        return validate_comfy_api_workflow(value)

    @model_validator(mode="after")
    def populate_defaults(self):
        if self.source != "builtin" and (
            self.custom_nodes
            or self.pip_packages
            or self.node_patches
            or self.models
            or self.pipeline is not None
        ):
            raise ValueError(
                "自定义工作流模板不允许声明远端依赖包、模型下载或流水线配置"
            )
        if self.node_patches:
            custom_node_names = {node.name for node in self.custom_nodes}
            for patch in self.node_patches:
                if patch.node_name not in custom_node_names:
                    raise ValueError(
                        f"节点补丁引用了未在 custom_nodes 中声明的节点: {patch.node_name!r}"
                    )
        self.node_count = len(self.workflow)
        if not self.fields:
            self.fields = extract_workflow_fields(self.workflow)
        field_keys = {field.key for field in self.fields}
        for field in self.fields:
            if field.visible_when:
                for ref_key in field.visible_when:
                    if ref_key not in field_keys:
                        raise ValueError(
                            f"字段 {field.key} 的 visible_when 引用了未声明的字段: {ref_key!r}"
                        )
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


def validate_colabone_hf_token(value: str) -> str:
    if not isinstance(value, str):
        raise ValueError("hf_token 必须为字符串")
    if len(value) > 512:
        raise ValueError("hf_token 长度不能超过 512")
    if any(
        ord(c) < 32 or ord(c) == 127 or 0x80 <= ord(c) <= 0x9F
        for c in value
    ):
        raise ValueError("hf_token 包含非法空白或控制字符")
    token = value.strip(" ")
    if any(c.isspace() for c in token):
        raise ValueError("hf_token 包含非法空白或控制字符")
    if len(token) > 512:
        raise ValueError("hf_token 长度不能超过 512")
    return token


_validate_hf_token = validate_colabone_hf_token


class WorkflowRunRequest(BaseModel):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    workflow_id: str = Field(pattern=r"^[a-zA-Z0-9_-]{1,80}$")
    overrides: dict[str, str | int | float | bool] = Field(default_factory=dict)
    inputs: dict[str, str] = Field(default_factory=dict)
    hf_token: str = Field(default="", max_length=512, repr=False)

    @field_validator("hf_token")
    @classmethod
    def valid_hf_token(cls, value: str) -> str:
        return validate_colabone_hf_token(value)

    @field_validator("overrides")
    @classmethod
    def valid_overrides(
        cls, value: dict[str, str | int | float | bool]
    ) -> dict[str, str | int | float | bool]:
        if len(value) > 128:
            raise ValueError("参数覆盖项数量不能超过 128 个")
        for key, item in value.items():
            _validate_field_key(key)
            if isinstance(item, str) and len(item) > MAX_OVERRIDE_STRING_LENGTH:
                raise ValueError(
                    f"字段 {key} 的文本长度不能超过 {MAX_OVERRIDE_STRING_LENGTH} 个字符"
                )
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


class ComfyTtsPreviewRequest(BaseModel):
    model_config = ConfigDict(
        extra="forbid", allow_inf_nan=False, hide_input_in_errors=True
    )
    engine_id: str = Field(
        min_length=1, max_length=80, pattern=r"^[a-zA-Z0-9_-]{1,80}$"
    )
    voice_preset: str = Field(
        default="", max_length=64, pattern=r"^(?:[a-zA-Z0-9_-]{1,64})?$"
    )
    preview_text: str = Field(default="", max_length=600)
    voice_prompt: str = Field(default="", max_length=600)
    emotion_prompt: str = Field(default="", max_length=240)
    ref_audio_input_id: str = Field(
        default="", max_length=64, pattern=r"^(?:[a-f0-9]{32})?$"
    )
    ref_text: str = Field(default="", max_length=600)
    emotion_ref_input_id: str = Field(
        default="", max_length=64, pattern=r"^(?:[a-f0-9]{32})?$"
    )
    hf_token: str = Field(default="", max_length=512, repr=False)

    @field_validator("hf_token")
    @classmethod
    def valid_hf_token(cls, value: str) -> str:
        return validate_colabone_hf_token(value)


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
    model_config = ConfigDict(extra="forbid", hide_input_in_errors=True)
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
    tts_engines: list[ComfyTtsEngineSpec] = Field(default_factory=list)

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
        engine_ids = {e.id for e in self.tts_engines}
        if len(engine_ids) != len(self.tts_engines):
            raise ValueError("内置 TTS 引擎 ID 重复")
        if engine_ids:
            for tmpl in self.templates:
                pipeline = tmpl.pipeline
                if pipeline is None:
                    continue
                if (
                    pipeline.default_tts_engine
                    and pipeline.default_tts_engine not in engine_ids
                ):
                    raise ValueError(
                        f"模板 {tmpl.id!r} 的默认 TTS 引擎未在 tts_engines 中声明: {pipeline.default_tts_engine!r}"
                    )
                for engine_id in pipeline.supported_tts_engines:
                    if engine_id not in engine_ids:
                        raise ValueError(
                            f"模板 {tmpl.id!r} 引用了未在 tts_engines 中声明的 TTS 引擎: {engine_id!r}"
                        )
        return self


def format_colabone_duration_zh(seconds: float, *, subsecond: bool = False) -> str:
    """Format elapsed seconds into a human-friendly Chinese duration string (秒 / 分 / 小时)."""
    try:
        val = float(seconds)
    except (TypeError, ValueError):
        return "0 秒"
    if not math.isfinite(val) or val <= 0.0:
        return "0.0 秒" if subsecond else "0 秒"

    if subsecond and val < 60.0:
        rounded_tenths = round(val, 1)
        if rounded_tenths < 60.0:
            return f"{rounded_tenths:.1f} 秒"
        return "1 分钟"

    total_seconds = int(round(val)) if subsecond else int(val)
    if total_seconds < 60:
        return f"{total_seconds} 秒"

    hours, rem = divmod(total_seconds, 3600)
    mins, secs = divmod(rem, 60)
    if hours == 0:
        if secs == 0:
            return f"{mins} 分钟"
        return f"{mins} 分 {secs} 秒"
    if mins == 0 and secs == 0:
        return f"{hours} 小时"
    if secs == 0:
        return f"{hours} 小时 {mins} 分"
    return f"{hours} 小时 {mins} 分 {secs} 秒"
