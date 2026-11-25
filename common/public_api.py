"""Public API configuration shared by the desktop and remote service."""

import base64
import json
import re
from urllib.parse import urlsplit
from typing import Literal
from pydantic import BaseModel, ConfigDict, Field, model_validator


def tunnel_token(text):
    matches = list(dict.fromkeys(re.findall(r"eyJ[A-Za-z0-9_+/=-]{20,}", text.strip())))
    if len(matches) != 1:
        raise ValueError("请粘贴 Cloudflare 的完整安装命令或唯一的 Tunnel Token")
    value = matches[0]
    try:
        data = json.loads(
            base64.b64decode(value + "=" * (-len(value) % 4), altchars=b"-_")
        )
        if not all(isinstance(data.get(k), str) and data[k] for k in ("a", "t", "s")):
            raise ValueError()
    except Exception:
        raise ValueError("未识别到有效的 Cloudflare Tunnel Token") from None
    return value


def public_origin(value):
    value = value.strip().rstrip("/")
    if not value.startswith("https://"):
        value = "https://" + value
    url = urlsplit(value)
    host = (url.hostname or "").lower()
    if (
        url.scheme != "https"
        or url.username
        or url.password
        or url.port
        or url.path not in ("", "/v1")
        or url.query
        or url.fragment
        or not re.fullmatch(
            r"(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}", host
        )
        or host == "trycloudflare.com"
        or host.endswith(
            (
                ".localhost",
                ".local",
                ".internal",
                ".test",
                ".invalid",
                ".trycloudflare.com",
            )
        )
    ):
        raise ValueError(
            "请填写公网 HTTPS 固定域名，例如 api.example.com（不含端口或其他路径，不支持 trycloudflare.com 临时域名）"
        )
    return "https://" + host


class ColabOnePublicConfig(BaseModel):
    model_config = ConfigDict(extra="forbid", hide_input_in_errors=True)
    mode: Literal["named"] = "named"
    domain: str = Field(default="", max_length=300)
    tunnel_token: str = Field(default="", max_length=8000, repr=False)
    api_key: str = Field(pattern=r"^co_[a-zA-Z0-9_-]{40,100}$", repr=False)

    @model_validator(mode="after")
    def valid(self):
        self.domain = public_origin(self.domain)
        self.tunnel_token = tunnel_token(self.tunnel_token)
        return self
