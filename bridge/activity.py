"""Bounded, credential-free activity feed. Never record chat text or HTTP bodies."""

from __future__ import annotations
from collections import deque
import re
import threading
import time


def safe_message(message):
    text = str(message)
    text = re.sub(r"https?://[^\s<>\"']+", "[链接已隐藏]", text)
    text = re.sub(r"(?i)Bearer\s+\S+", "Bearer [已隐藏]", text)
    text = re.sub(r"(?:ya29\.[\w.-]+|hf_[\w-]+|4/[\w./+%-]{12,})", "[凭据已隐藏]", text)
    text = re.sub(
        r"(?i)((?:access_token|refresh_token|client_secret|code|token)\s*[=:]\s*)[^\s,;]+",
        r"\1[已隐藏]",
        text,
    )
    return text[:600]


class Activity:
    def __init__(self, limit=300):
        self.items = deque(maxlen=limit)
        self.sequence = 0
        self.lock = threading.RLock()

    def add(self, message, source="客户端", level="info"):
        with self.lock:
            self.sequence += 1
            self.items.append(
                {
                    "id": self.sequence,
                    "time": time.time(),
                    "source": source,
                    "level": level,
                    "message": safe_message(message),
                }
            )

    def snapshot(self):
        with self.lock:
            return list(self.items)

    def clear(self):
        with self.lock:
            self.items.clear()
