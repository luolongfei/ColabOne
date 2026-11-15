"""JSON snapshots on the remote Colab DriveFS mount; no separate Drive API client."""

from __future__ import annotations
import json
import os
import re
import threading
import shutil
from pathlib import Path
from common.files import atomic_json
from common.creative import CreationRecord


class DriveStore:
    def __init__(self, mount_root=Path("/content/drive"), mounted=None, flush=None):
        self.mount_root = Path(mount_root)
        self.mounted = mounted or (lambda: os.path.ismount(self.mount_root))
        self.flush_hook = flush
        self.lock = threading.RLock()
        self.pending_flush = False

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
            self.pending_flush = True

    def conversations(self):
        with self.lock:
            folder = self.folder()
            result = [self.get(p.name) for p in folder.glob("conversation-*.json")]
            return sorted(result, key=lambda c: c.get("updated_at", 0), reverse=True)

    def media_file(self, name):
        if not re.fullmatch(r"[a-f0-9]{32}\.(?:png|webm|json)", name):
            raise ValueError("Invalid media key")
        folder = self.folder() / "creations"
        if folder.is_symlink():
            raise ValueError("作品目录不能是符号链接")
        folder.mkdir(parents=True, exist_ok=True)
        path = folder / name
        if path.is_symlink():
            raise ValueError("作品不能是符号链接")
        return path

    def put_media(self, source, item):
        with self.lock:
            target = self.media_file(item["id"] + "." + item["extension"])
            temporary = target.with_name(target.name + ".uploading")
            if temporary.is_symlink():
                raise ValueError("上传临时文件不能是符号链接")
            shutil.copyfile(source, temporary)
            temporary.replace(target)
            atomic_json(self.media_file(item["id"] + ".json"), item)
            self.pending_flush = True

    def media_list(self):
        with self.lock:
            folder = self.folder() / "creations"
            if folder.is_symlink():
                raise ValueError("作品目录不能是符号链接")
            items = []
            for path in folder.glob("*.json"):
                try:
                    item = CreationRecord.model_validate_json(
                        self.media_file(path.name).read_text(encoding="utf-8")
                    ).model_dump()
                    if item["id"] != path.stem or item["extension"] not in (
                        "png",
                        "webm",
                    ):
                        continue
                    if not 0 < item["size"] <= 64 * 1024**2 or not re.fullmatch(
                        r"[a-f0-9]{64}", item["sha256"]
                    ):
                        continue
                    item["synced"] = True
                    items.append(item)
                except (ValueError, KeyError, TypeError, OSError):
                    continue
            return items

    def trash_media(self, identity, extension):
        with self.lock:
            target = self.media_file(identity + "." + extension)
            trash = target.parent / ".trash"
            if trash.is_symlink():
                raise ValueError("回收目录不能是符号链接")
            trash.mkdir(exist_ok=True)
            for path in (target, self.media_file(identity + ".json")):
                if path.exists():
                    path.replace(trash / path.name)
            self.pending_flush = True

    def flush(self):
        with self.lock:
            if not self.available():
                if self.pending_flush:
                    raise RuntimeError(
                        "云盘已断开，无法确认待上传文件；请重新连接后重试"
                    )
                return
            if self.flush_hook:
                self.flush_hook()
            else:
                from google.colab import drive

                drive.flush_and_unmount(timeout_ms=30000)
            self.pending_flush = False
