"""Small JSON snapshots in Drive appDataFolder; no weights or refresh tokens."""

from __future__ import annotations

import json
import threading
import requests


class DriveStore:
    def __init__(self, token="", session=None):
        self.token = token
        self.http = session or requests.Session()
        self.lock = threading.RLock()

    def request(self, method, path, **kwargs):
        if not self.token:
            raise RuntimeError("请先授权 Google Drive")
        headers = kwargs.pop("headers", {}) | {"Authorization": f"Bearer {self.token}"}
        r = self.http.request(
            method,
            "https://www.googleapis.com/" + path,
            headers=headers,
            timeout=25,
            **kwargs,
        )
        r.raise_for_status()
        return r

    def listing(self, name=None):
        query = "trashed = false"
        if name:
            if not name.replace("-", "").replace(".", "").isalnum():
                raise ValueError("Invalid storage key")
            query += f" and name = '{name}'"
        files, page = [], None
        while True:
            params = {
                "spaces": "appDataFolder",
                "q": query,
                "fields": "nextPageToken,files(id,name,modifiedTime)",
                "pageSize": 1000,
            }
            if page:
                params["pageToken"] = page
            data = self.request("GET", "drive/v3/files", params=params).json()
            files.extend(data.get("files", []))
            page = data.get("nextPageToken")
            if not page:
                return files

    def get(self, name, default=None):
        with self.lock:
            items = sorted(
                self.listing(name),
                key=lambda x: x.get("modifiedTime", ""),
                reverse=True,
            )
            if not items:
                return default
            return self.request(
                "GET", f"drive/v3/files/{items[0]['id']}", params={"alt": "media"}
            ).json()

    def put(self, name, data):
        with self.lock:
            items = self.listing(name)
            if items:
                file_id = items[0]["id"]
            else:
                file_id = self.request(
                    "POST",
                    "drive/v3/files",
                    json={
                        "name": name,
                        "parents": ["appDataFolder"],
                        "mimeType": "application/json",
                    },
                ).json()["id"]
            self.request(
                "PATCH",
                f"upload/drive/v3/files/{file_id}",
                params={"uploadType": "media"},
                headers={"Content-Type": "application/json"},
                data=json.dumps(data, ensure_ascii=False).encode(),
            )

    def conversations(self):
        result = []
        for item in self.listing():
            if item["name"].startswith("conversation-"):
                data = self.request(
                    "GET", f"drive/v3/files/{item['id']}", params={"alt": "media"}
                ).json()
                result.append(data)
        return sorted(result, key=lambda x: x.get("updated_at", 0), reverse=True)
