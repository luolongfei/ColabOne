"""JSON snapshots on the remote Colab DriveFS mount; no separate Drive API client."""

from __future__ import annotations
import json
import os
import re
import threading
from pathlib import Path
from common.files import atomic_json


class DriveStore:
    def __init__(self, mount_root=Path("/content/drive"), mounted=None, flush=None):
        self.mount_root = Path(mount_root)
        self.mounted = mounted or (lambda: os.path.ismount(self.mount_root))
        self.flush_hook = flush
        self.lock = threading.RLock()

    def available(self):
        return self.mounted() and (self.mount_root / "MyDrive").is_dir()

    def folder(self):
        # Never silently write to a plain local directory after a failed mount.
        if not self.available():
            raise RuntimeError("请先在设置中连接 Google 云盘")
        folder = self.mount_root / "MyDrive" / "ColabOne"
        if folder.is_symlink():
            raise ValueError("ColabOne 云盘目录不能是符号链接")
        return folder

    def file(self, name):
        if not re.fullmatch(r"(?:models|conversation-[a-zA-Z0-9_-]{1,80})\.json", name):
            raise ValueError("Invalid storage key")
        path = self.folder() / name
        if path.is_symlink():
            raise ValueError("Cloud file must not be a symbolic link")
        return path

    def get(self, name, default=None):
        with self.lock:
            path = self.file(name)
            return (
                json.loads(path.read_text(encoding="utf-8"))
                if path.exists()
                else default
            )

    def put(self, name, data):
        with self.lock:
            atomic_json(self.file(name), data)

    def conversations(self):
        with self.lock:
            folder = self.folder()
            result = [self.get(p.name) for p in folder.glob("conversation-*.json")]
            return sorted(result, key=lambda c: c.get("updated_at", 0), reverse=True)

    def flush(self):
        if not self.available():
            return
        if self.flush_hook:
            self.flush_hook()
        else:
            from google.colab import drive

            drive.flush_and_unmount(timeout_ms=30000)
