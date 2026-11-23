"""Bounded, credential-free activity feed. Never record chat text or HTTP bodies."""

from __future__ import annotations
from collections import deque
from datetime import datetime
import os
from pathlib import Path
import re
import threading
import time


def safe_message(message):
    text = str(message)
    text = re.sub(
        r"(?:https?|wss?|socks5h?)://[^\s<>\"'\)\]]+?(?=[:;,.)]*(?:\s|[\"'<>)\]]|$))",
        "[链接已隐藏]",
        text,
    )
    text = re.sub(r"(?i)Bearer\s+[^\s,;\"'\}]+", "Bearer [已隐藏]", text)
    text = re.sub(
        r"(?:ya29\.[\w.-]+|1//[\w./+-]{12,}|hf_[\w-]+|co_[\w-]{40,}|eyJ[A-Za-z0-9_+/=-]{20,}|4/[\w./+%-]{12,})",
        "[凭据已隐藏]",
        text,
    )
    text = re.sub(
        r"(?i)([\"']?(?:access_token|refresh_token|client_secret|code|token)[\"']?\s*[=:]\s*[\"']?)[^\s,;\"'\}]+",
        r"\1[已隐藏]",
        text,
    )
    return text[:600]


class ColabOneActivityLog:
    def __init__(self, limit=300, log_path=None, max_bytes=256 * 1024):
        self.items = deque(maxlen=limit)
        self.sequence = 0
        self.lock = threading.RLock()
        self.log_path = Path(log_path) if log_path else None
        self.max_bytes = max(16 * 1024, int(max_bytes))

    def _append_disk(self, ts: float, level: str, source: str, message: str) -> None:
        if self.log_path is None:
            return
        try:
            self.log_path.parent.mkdir(parents=True, exist_ok=True)
            if (
                self.log_path.exists()
                and self.log_path.stat().st_size >= self.max_bytes
            ):
                backup = self.log_path.with_name(self.log_path.name + ".1")
                os.replace(self.log_path, backup)
            stamp = datetime.fromtimestamp(ts).strftime("%Y-%m-%d %H:%M:%S")
            line_msg = message.replace("\r", " ").replace("\n", " ")
            line = f"{stamp} [{level.upper()}] [{source}] {line_msg}\n"
            created = not self.log_path.exists()
            with self.log_path.open("a", encoding="utf-8") as fh:
                fh.write(line)
            if created:
                try:
                    self.log_path.chmod(0o600)
                except OSError:
                    pass
        except OSError:
            pass

    def add(self, message, source="客户端", level="info"):
        with self.lock:
            self.sequence += 1
            now = time.time()
            clean_msg = safe_message(message)
            clean_src = safe_message(source)[:40]
            clean_lvl = str(level or "info").lower()[:16]
            self.items.append(
                {
                    "id": self.sequence,
                    "time": now,
                    "source": clean_src,
                    "level": clean_lvl,
                    "message": clean_msg,
                }
            )
            self._append_disk(now, clean_lvl, clean_src, clean_msg)

    def snapshot(self):
        with self.lock:
            return list(self.items)

    def clear(self):
        with self.lock:
            self.items.clear()
