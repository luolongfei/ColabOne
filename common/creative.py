"""Validated creative jobs. No desktop inference or filesystem access."""

from typing import Literal
from pydantic import BaseModel, ConfigDict, Field, model_validator


class CreativeRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    model_id: str = Field(pattern=r"^[a-zA-Z0-9_-]{1,80}$")
    kind: Literal["image", "video"] = "image"
    prompt: str = Field(min_length=1, max_length=12000)
    negative_prompt: str = Field(default="", max_length=12000)
    width: int = Field(default=1024, ge=256, le=4096)
    height: int = Field(default=1024, ge=256, le=4096)
    steps: int = Field(default=9, ge=1, le=80)
    count: int = Field(default=1, ge=1, le=4)
    seed: int = Field(default=-1, ge=-1, le=2147483647)
    strength: float = Field(default=0.65, ge=0.05, le=1)
    reference_id: str = Field(default="", pattern=r"^(?:[a-f0-9]{32})?$")
    frames: int = Field(default=33, ge=9, le=121)
    fps: int = Field(default=16, ge=8, le=24)

    @model_validator(mode="after")
    def limits(self):
        self.prompt = self.prompt.strip()
        if not self.prompt:
            raise ValueError("请描述希望生成的内容")
        if self.width % 32 or self.height % 32:
            raise ValueError("尺寸需要为 32 的倍数")
        if self.kind == "video" and (
            self.width * self.height > 1280 * 736
            or self.count != 1
            or self.frames % 4 != 1
        ):
            raise ValueError("视频最多 720P、一次一段，帧数需为 4n+1")
        return self


class CreationRecord(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)
    id: str = Field(pattern=r"^[a-f0-9]{32}$")
    kind: Literal["image", "video"]
    model: str = Field(min_length=1, max_length=200)
    created_at: float = Field(ge=0)
    params: CreativeRequest
    size: int = Field(gt=0, le=64 * 1024**2)
    sha256: str = Field(pattern=r"^[a-f0-9]{64}$")
    extension: Literal["png", "webm"]
    mime: Literal["image/png", "video/webm"]
    elapsed: float = Field(ge=0)
    frame_count: int | None = Field(default=None, ge=1, le=121)
    synced: bool = False

    @model_validator(mode="after")
    def consistent(self):
        expected = (
            ("png", "image/png") if self.kind == "image" else ("webm", "video/webm")
        )
        if (self.extension, self.mime) != expected or self.params.kind != self.kind:
            raise ValueError("作品格式与类型不匹配")
        return self
