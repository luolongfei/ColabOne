from __future__ import annotations
import copy
import inspect
import json
import os
import sys
import threading
import time
import secrets
import re
from pathlib import Path

import requests

from bridge.auth import ColabOneAuth, ColabOneVault
from bridge.activity import ColabOneActivityLog, safe_message
from bridge.provider import ColabOneProvider
from bridge.shortcuts import manage_shortcuts, shortcut_status
from common.files import atomic_json
from common.location import infer_location_from_endpoint
from common.models import ModelSpec, parse_hf_input, resolve_import
from common.hub import search_models, model_files
from common.public_api import ColabOnePublicConfig
from common.comfyui import (
    ComfyCanvasConfig,
    ComfyCatalog,
    WorkflowImportRequest,
    WorkflowRunRequest,
    WorkflowTemplate,
)


COLABONE_DEFAULT_COMMON_VERSION = 1
COLABONE_DEFAULT_COMMON_REPOS: tuple[dict[str, str], ...] = (
    {
        "repo": "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        "name": "Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        "note": "默认模型",
    },
    {
        "repo": "huihui-ai/Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
        "name": "Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
        "note": "默认模型",
    },
    {
        "repo": "JonathanColetti/Qwen3.8-27B-Uncensored",
        "name": "Qwen3.8-27B-Uncensored",
        "note": "默认模型",
    },
)


def colabone_config_dir():
    if os.environ.get("COLABONE_CONFIG_DIR"):
        return Path(os.environ["COLABONE_CONFIG_DIR"])
    if sys.platform == "darwin":
        return Path.home() / "Library/Application Support/ColabOne"
    return (
        Path(os.environ.get("LOCALAPPDATA", str(Path.home() / ".local/share")))
        / "ColabOne"
    )


class ColabOneOperationCancelled(RuntimeError):
    """Raised when a user voluntarily cancels a long-running desktop operation."""


class ColabOneController:
    def __init__(self, root, vault=None, config_dir=None, background=True):
        self.root = root
        self.vault = vault or ColabOneVault()
        self.auth = ColabOneAuth(self.vault)
        self.provider = ColabOneProvider(self.auth, self.vault, root)
        self.directory = config_dir or colabone_config_dir()
        self.shortcut_sandbox = (
            (Path(config_dir) / "shortcuts")
            if config_dir is not None
            else (
                Path(os.environ["COLABONE_SHORTCUT_DIR"])
                if os.environ.get("COLABONE_SHORTCUT_DIR")
                else None
            )
        )
        self.settings = {
            "gpu": "T4",
            "idle_minutes": 30,
            "idle_countdown_seconds": 60,
            "auto_drive": True,
            "download_threads": 16,
            "locale": "",
        }
        if (self.directory / "settings.json").exists():
            saved = json.loads(
                (self.directory / "settings.json").read_text(encoding="utf-8")
            )
            for k in (
                "gpu",
                "idle_minutes",
                "idle_countdown_seconds",
                "auto_drive",
                "download_threads",
                "idle_policy",
                "rclone_drive_policy",
                "locale",
            ):
                if k in saved:
                    self.settings[k] = saved[k]
        if self.settings.get("locale") not in (
            "",
            "zh-CN",
            "en-US",
            "zh-TW",
            "zh-HK",
            "ja-JP",
            "ko-KR",
        ):
            self.settings["locale"] = ""
        if self.settings.get("locale"):
            self.auth.locale = self.settings["locale"]
        # Migrate legacy 8-thread setting to the 16 / 32 concurrency options.
        if self.settings.get("download_threads") not in (16, 32):
            self.settings["download_threads"] = 16
        # Migrate legacy 3-minute policy to the 30-minute idle auto-release default.
        if self.settings.get("idle_policy") != 3:
            self.settings.update(
                idle_minutes=30, idle_countdown_seconds=60, idle_policy=3
            )
        # Enable headless OAuth/rclone Drive persistence by default once upgraded.
        if self.settings.get("rclone_drive_policy") != 1:
            self.settings.update(auto_drive=True, rclone_drive_policy=1)
        self.lock = threading.RLock()
        self.operation_lock = threading.Lock()
        self.stop = threading.Event()
        self.drive_cancel = threading.Event()
        self.instance_states: dict[str, dict] = {}
        self.common_ids: set[str] = set()
        self.custom_models: dict[str, dict] = {}
        self.common_repos: list[dict] = []
        self.default_common_version: int = 0
        self._load_library()
        self.custom_workflows: dict[str, dict] = {}
        self.remote_workflow_versions: dict[str, float] = {}
        self._load_workflows()
        self.data = {
            "runtime": {"state": "stopped"},
            "account": {"logged_in": False},
            "models": self.presets(),
            "workflows": self._merge_workflows(None),
            "comfy_outputs": [],
            "operation": None,
            "error": "",
            "auth_url": "",
        }
        self.last_auth = self.last_config = self.last_heartbeat = 0
        self.activity = ColabOneActivityLog()
        self.task_activity = {}
        self.task_log_cursor = {}
        self.pending_touch = False
        self.last_touch = 0
        self.touch_lock = threading.Lock()
        self.upgrade_attempted = set()
        self.release_plan = None
        self.monitor_lock = threading.RLock()
        self.monitor_mode: str = "auto"
        self.monitor_endpoint: str = ""
        self.monitor_paused: bool = False
        self.external_observations: dict[str, dict] = {}
        self._observer_wake = threading.Event()
        self._observer_poll_lock = threading.Lock()
        self._observer_thread: threading.Thread | None = None
        self.activity.add("客户端已启动")
        if self.vault.get("colab"):
            self.activity.add("已恢复本机保存的 Google 登录；到期自动续期", "登录")
        if background:
            threading.Thread(target=self.monitor, daemon=True).start()
            self._observer_thread = threading.Thread(
                target=self._external_monitor_loop, daemon=True
            )
            self._observer_thread.start()

    def close(self):
        self.stop.set()
        self.drive_cancel.set()
        self._observer_wake.set()
        close_obs = getattr(self.provider, "close_observer", None)
        if callable(close_obs):
            try:
                close_obs(shutdown_kernel=True)
            except Exception:
                pass

    def _vault_sessions(self) -> dict[str, dict]:
        raw_map = self.vault.get("runtime_sessions")
        sessions = dict(raw_map) if isinstance(raw_map, dict) else {}
        legacy = self.vault.get("runtime_session")
        if isinstance(legacy, dict) and legacy.get("endpoint"):
            sessions[legacy["endpoint"]] = legacy
        return sessions

    def _vault_remotes(self) -> dict[str, dict]:
        raw_map = self.vault.get("remotes")
        remotes = dict(raw_map) if isinstance(raw_map, dict) else {}
        legacy = self.vault.get("remote")
        if isinstance(legacy, dict) and legacy.get("endpoint"):
            remotes[legacy["endpoint"]] = legacy
        return remotes

    def _active_endpoint(self) -> str | None:
        sessions = self._vault_sessions()
        remotes = self._vault_remotes()
        preferred = self.vault.get("active_endpoint")
        if preferred and (preferred in sessions or preferred in remotes):
            return preferred
        legacy = self.vault.get("runtime_session") or self.vault.get("remote")
        if isinstance(legacy, dict) and legacy.get("endpoint"):
            return legacy["endpoint"]
        return next(iter(sessions), next(iter(remotes), None))

    def _ensure_instance_state(self, endpoint: str) -> dict:
        with self.lock:
            state = self.instance_states.get(endpoint)
            if state is None:
                state = {
                    "runtime": {"state": "stopped", "id": endpoint},
                    "models": self.presets(),
                    "workflows": [],
                    "comfy_outputs": [],
                    "remote_workflow_versions": {},
                    "last_heartbeat": 0,
                    "last_config": 0,
                }
                self.instance_states[endpoint] = state
            return state

    def _save_active_to_cache(self):
        with self.lock:
            if self.data["runtime"].get("state") in ("stopped", "starting"):
                return
            ep = self.data["runtime"].get("id") or self._active_endpoint()
            if not ep or ep not in self._vault_sessions():
                return
            entry = self._ensure_instance_state(ep)
            entry["runtime"] = copy.deepcopy(self.data["runtime"])
            entry["models"] = copy.deepcopy(self.data["models"])
            entry["workflows"] = copy.deepcopy(self.data.get("workflows", []))
            entry["comfy_outputs"] = copy.deepcopy(self.data.get("comfy_outputs", []))
            entry["remote_workflow_versions"] = dict(self.remote_workflow_versions)

    def _activate_cached(self, endpoint: str | None):
        with self.lock:
            if endpoint and endpoint in self._vault_sessions():
                entry = self._ensure_instance_state(endpoint)
                self.data["runtime"] = copy.deepcopy(entry["runtime"])
                self.data["models"] = copy.deepcopy(entry["models"])
                self.data["workflows"] = self._merge_workflows(
                    entry.get("workflows", [])
                )
                self.data["comfy_outputs"] = copy.deepcopy(
                    entry.get("comfy_outputs", [])
                )
                self.remote_workflow_versions = dict(
                    entry.get("remote_workflow_versions") or {}
                )
            else:
                self.data["runtime"] = {"state": "stopped"}
                self.data["models"] = self.presets()
                self.data["workflows"] = self._merge_workflows(None)
                self.data["comfy_outputs"] = []
                self.remote_workflow_versions = {}

    def _enrich_instances(self, account: dict) -> list[dict]:
        sessions = self._vault_sessions()
        remotes = self._vault_remotes()
        active_ep = self._active_endpoint()
        raw_list = [dict(item) for item in (account.get("instances") or [])]
        seen = {item["id"] for item in raw_list if item.get("id")}
        for ep, sess in sessions.items():
            if ep not in seen:
                raw_list.append(
                    {
                        "id": ep,
                        "managed": True,
                        "active": ep == active_ep,
                        "accelerator": (
                            remotes.get(ep, {}).get("gpu")
                            or str(sess.get("accelerator") or "")
                        ).upper(),
                        "variant": str(sess.get("variant") or "GPU").upper(),
                        "shape": str(sess.get("machine_shape") or "STANDARD").upper(),
                    }
                )
        result = []
        for item in raw_list:
            ep = item["id"]
            managed = bool(item.get("managed")) or (ep in sessions)
            is_active = managed and (ep == active_ep)
            enriched = dict(item)
            enriched["managed"] = managed
            enriched["active"] = is_active
            if managed:
                sess = sessions.get(ep) or {}
                rem = remotes.get(ep) or {}
                if not enriched.get("accelerator"):
                    enriched["accelerator"] = (
                        rem.get("gpu") or str(sess.get("accelerator") or "")
                    ).upper()
                if not enriched.get("variant"):
                    enriched["variant"] = str(sess.get("variant") or "GPU").upper()
                if not enriched.get("shape"):
                    enriched["shape"] = str(
                        sess.get("machine_shape") or "STANDARD"
                    ).upper()
                with self.lock:
                    cached = (
                        self.data
                        if is_active
                        and self.data["runtime"].get("state") != "stopped"
                        else self.instance_states.get(ep)
                    )
                    rt = copy.deepcopy((cached or {}).get("runtime") or {})
                    models = (cached or {}).get("models") or []
                loaded_id = rt.get("loaded_model") or ""
                loaded_name = next(
                    (
                        m.get("name", loaded_id)
                        for m in models
                        if m.get("id") == loaded_id
                    ),
                    loaded_id,
                )
                running = next(
                    (
                        t
                        for t in (rt.get("tasks") or [])
                        if t.get("state") == "running"
                    ),
                    None,
                )
                gpu = rt.get("gpu") or {}
                resources = rt.get("resources")
                loc = (
                    rt.get("location")
                    or rem.get("location")
                    or enriched.get("location")
                    or infer_location_from_endpoint(ep, str(sess.get("url") or ""))
                )
                enriched.update(
                    state=rt.get("state") or ("ready" if ep in remotes else "starting"),
                    gpu_name=gpu.get("name", ""),
                    vram_used_mib=gpu.get("used_mib"),
                    vram_total_mib=gpu.get("total_mib"),
                    disk_free=rt.get("disk_free"),
                    loaded_model=loaded_name,
                    active_task=running.get("kind", "") if running else "",
                    drive_connected=bool((rt.get("drive") or {}).get("connected")),
                )
                if isinstance(resources, dict) and resources:
                    enriched["resources"] = resources
                    mem = resources.get("memory")
                    if isinstance(mem, dict):
                        if "used_bytes" in mem:
                            enriched["ram_used_bytes"] = mem["used_bytes"]
                        if "total_bytes" in mem:
                            enriched["ram_total_bytes"] = mem["total_bytes"]
                    disk = resources.get("disk")
                    if isinstance(disk, dict):
                        if "used_bytes" in disk:
                            enriched["disk_used_bytes"] = disk["used_bytes"]
                        if "total_bytes" in disk:
                            enriched["disk_total_bytes"] = disk["total_bytes"]
                        if "free_bytes" in disk:
                            enriched["disk_free_bytes"] = disk["free_bytes"]
                    for rk in (
                        "ram_used_bytes",
                        "ram_total_bytes",
                        "disk_used_bytes",
                        "disk_total_bytes",
                        "disk_free_bytes",
                    ):
                        if rk in resources:
                            enriched[rk] = resources[rk]
                if loc:
                    enriched["location"] = loc
            result.append(enriched)
        return result

    def _load_library(self):
        path = self.directory / "library.json"
        existed = path.is_file()
        raw = None
        if existed:
            try:
                parsed = json.loads(path.read_text(encoding="utf-8"))
                if isinstance(parsed, dict):
                    raw = parsed
            except (OSError, ValueError, TypeError):
                raw = None

        seen_repos: set[str] = set()
        common_repos: list[dict] = []
        if raw is not None:
            raw_ids = (
                raw.get("common_ids")
                if "common_ids" in raw
                else raw.get("starred_ids")
            ) or []
            self.common_ids = {
                str(x)
                for x in raw_ids
                if re.fullmatch(r"[a-zA-Z0-9_-]{1,80}", str(x))
            }
            for item in raw.get("custom_models") or []:
                try:
                    spec = ModelSpec.model_validate(item).model_dump()
                    if spec["id"].startswith("hf-"):
                        spec["min_vram_mib"] = 0
                    self.custom_models[spec["id"]] = spec
                except Exception:
                    continue
            raw_repos = (
                raw.get("common_repos")
                if "common_repos" in raw
                else raw.get("bookmarks")
            ) or []
            for item in raw_repos:
                if not isinstance(item, dict):
                    continue
                repo = str(item.get("repo") or "").strip()
                if (
                    not re.fullmatch(r"[\w.-]+/[\w.-]+", repo)
                    or repo.lower() in seen_repos
                ):
                    continue
                seen_repos.add(repo.lower())
                common_repos.append(
                    {
                        "repo": repo,
                        "name": str(item.get("name") or repo.split("/")[-1])[:120],
                        "note": str(item.get("note") or "")[:200],
                        "filename": str(item.get("filename") or "")[:200],
                        "downloads": int(item.get("downloads") or 0),
                        "likes": int(item.get("likes") or 0),
                        "added_at": float(item.get("added_at") or time.time()),
                    }
                )
            try:
                self.default_common_version = int(raw.get("default_common_version") or 0)
            except (TypeError, ValueError):
                self.default_common_version = 0

        # Seed default common model repositories once (preserving any manual edits thereafter).
        seeded = False
        if "qwen25-05b" in self.common_ids and "qwen25-05b" not in self.custom_models:
            self.common_ids.discard("qwen25-05b")
            seeded = True
        if self.default_common_version < COLABONE_DEFAULT_COMMON_VERSION:
            now = time.time()
            for default in COLABONE_DEFAULT_COMMON_REPOS:
                key = default["repo"].lower()
                if key in seen_repos:
                    continue
                seen_repos.add(key)
                common_repos.append(
                    {
                        "repo": default["repo"],
                        "name": default["name"],
                        "note": default.get("note", ""),
                        "filename": "",
                        "downloads": 0,
                        "likes": 0,
                        "added_at": now,
                    }
                )
            self.default_common_version = COLABONE_DEFAULT_COMMON_VERSION
            seeded = True

        self.common_repos = common_repos
        if existed and seeded:
            self._save_library()

    def _save_library(self):
        with self.lock:
            payload = {
                "default_common_version": self.default_common_version,
                "common_ids": sorted(self.common_ids),
                "custom_models": list(self.custom_models.values()),
                "common_repos": list(self.common_repos),
            }
            atomic_json(self.directory / "library.json", payload)

    def _load_workflows(self):
        path = self.directory / "workflows.json"
        if not path.is_file():
            return
        try:
            raw = json.loads(path.read_text(encoding="utf-8"))
            if isinstance(raw, list):
                items = raw
            elif isinstance(raw, dict):
                items = (
                    raw["workflows"]
                    if isinstance(raw.get("workflows"), list)
                    else list(raw.values())
                )
            else:
                return
            for item in items:
                try:
                    wf = WorkflowTemplate.model_validate(item).model_dump()
                    self.custom_workflows[wf["id"]] = wf
                except Exception:
                    continue
        except (OSError, ValueError, TypeError):
            pass

    def _save_workflows(self):
        with self.lock:
            atomic_json(
                self.directory / "workflows.json",
                list(self.custom_workflows.values()),
            )

    def _builtin_workflows(self) -> list[dict]:
        path = self.root / "catalog/comfyui.json"
        if not path.is_file():
            return []
        try:
            raw = json.loads(path.read_text(encoding="utf-8"))
            catalog = ComfyCatalog.model_validate(raw)
            return [t.model_dump() for t in catalog.templates]
        except Exception:
            return []

    def _merge_workflows(self, remote_workflows: list[dict] | None) -> list[dict]:
        builtins = self._builtin_workflows()
        builtin_order = {
            w["id"]: i
            for i, w in enumerate(builtins)
            if isinstance(w, dict) and w.get("id")
        }
        merged: dict[str, dict] = {}
        for wf in builtins:
            if isinstance(wf, dict) and wf.get("id"):
                merged[wf["id"]] = copy.deepcopy(wf)
        with self.lock:
            local_map = copy.deepcopy(self.custom_workflows)
        for wid, wf in local_map.items():
            if not isinstance(wf, dict) or not wid:
                continue
            prev = merged.get(wid)
            if prev is None or float(wf.get("updated_at") or 0) >= float(
                prev.get("updated_at") or 0
            ):
                merged[wid] = wf
        if isinstance(remote_workflows, list):
            for wf in remote_workflows:
                if not isinstance(wf, dict) or not wf.get("id"):
                    continue
                wid = wf["id"]
                prev = merged.get(wid)
                wf_ts = float(wf.get("updated_at") or 0)
                prev_ts = (
                    float(prev.get("updated_at") or 0) if prev is not None else -1.0
                )
                if prev is None or wf_ts > prev_ts or (
                    wf_ts == prev_ts and wid not in local_map
                ):
                    merged[wid] = copy.deepcopy(wf)
        result = list(merged.values())
        result.sort(
            key=lambda w: (
                0 if w.get("source") == "builtin" else 1,
                builtin_order.get(w["id"], 9999)
                if w.get("source") == "builtin"
                else -float(w.get("updated_at") or 0),
                str(w.get("id") or ""),
            )
        )
        return result

    @staticmethod
    def _is_not_found_error(exc: Exception) -> bool:
        if isinstance(exc, KeyError):
            return True
        if isinstance(exc, requests.HTTPError) and getattr(
            getattr(exc, "response", None), "status_code", None
        ) == 404:
            return True
        msg = str(exc)
        return "404" in msg or "不存在" in msg or "not found" in msg.lower()

    def _ensure_remote_workflow(self, workflow_id: str):
        with self.lock:
            local_wf = copy.deepcopy(self.custom_workflows.get(workflow_id))
            remote_ver = self.remote_workflow_versions.get(workflow_id)
        if not local_wf:
            return
        local_ver = float(local_wf.get("updated_at") or 0)
        if remote_ver is not None and remote_ver >= local_ver:
            return
        req = WorkflowImportRequest(
            id=local_wf["id"],
            name=local_wf["name"],
            description=local_wf.get("description", ""),
            workflow=local_wf["workflow"],
        )
        synced = self.remote("POST", "comfyui/workflows", req.model_dump())
        with self.lock:
            if isinstance(synced, dict) and synced.get("id"):
                self.remote_workflow_versions[synced["id"]] = float(
                    synced.get("updated_at") or local_ver
                )
            else:
                self.remote_workflow_versions[workflow_id] = local_ver

    def _decorate_models(self, base_models: list[dict]) -> list[dict]:
        preset_path = self.root / "catalog/presets.json"
        preset_ids = (
            {m["id"] for m in json.loads(preset_path.read_text(encoding="utf-8"))}
            if preset_path.exists()
            else set()
        )
        with self.lock:
            common_ids = set(self.common_ids)
            custom_map = copy.deepcopy(self.custom_models)
        combined = [copy.deepcopy(m) for m in base_models]
        present = {m["id"] for m in combined}
        for mid, spec in custom_map.items():
            if mid not in present:
                combined.append(
                    spec
                    | {
                        "downloaded": False,
                        "loaded": False,
                        "size": sum(f["size"] for f in spec["files"]),
                        "_local_only": True,
                    }
                )
        out = []
        for m in combined:
            m.pop("_local_only", None)
            files = m.get("files") or []
            repo = (
                str(files[0].get("repo") or "")
                if files and isinstance(files[0], dict)
                else str(m.get("repo") or "")
            )
            out.append(
                m
                | {
                    "repo": repo,
                    "common": m["id"] in common_ids,
                    "custom": m["id"] not in preset_ids,
                }
            )
        return out

    def presets(self):
        path = self.root / "catalog/presets.json"
        if not path.exists():
            return []
        return [
            m
            | {
                "downloaded": False,
                "loaded": False,
                "size": sum(f["size"] for f in m["files"]),
            }
            for m in json.loads(path.read_text(encoding="utf-8"))
        ]

    @staticmethod
    def _is_gpu_instance(inst: dict) -> bool:
        variant = str(inst.get("variant") or "").upper()
        accel = str(inst.get("accelerator") or "").upper()
        if variant == "GPU":
            return True
        return accel not in ("", "CPU", "NONE", "TPU")

    def _close_external_observer(
        self, endpoint: str | None = None, shutdown_kernel: bool = True
    ) -> None:
        close_obs = getattr(self.provider, "close_observer", None)
        if callable(close_obs):
            try:
                close_obs(endpoint=endpoint, shutdown_kernel=shutdown_kernel)
            except TypeError:
                try:
                    close_obs()
                except Exception:
                    pass
            except Exception:
                pass

    def _reset_monitor_to_auto(
        self, close_observer: bool = False, clear_observations: bool = False
    ) -> None:
        with self.monitor_lock:
            self.monitor_mode = "auto"
            self.monitor_endpoint = ""
            self.monitor_paused = False
            if clear_observations:
                self.external_observations.clear()
        if close_observer:
            self._close_external_observer(shutdown_kernel=True)
        self._observer_wake.set()

    def _resolve_monitor_instances(
        self,
        enriched: list[dict],
        active_runtime: dict,
        active_ep: str | None,
    ) -> tuple[list[dict], str, bool]:
        """Order selectable instances and resolve the effective monitor target."""
        by_id: dict[str, dict] = {}
        managed_items: list[dict] = []
        ext_gpu_items: list[dict] = []
        ext_other_items: list[dict] = []
        for item in enriched:
            ep = str(item.get("id") or "")
            if not ep or ep in by_id:
                continue
            by_id[ep] = item
            if item.get("managed"):
                managed_items.append(item)
            elif self._is_gpu_instance(item):
                ext_gpu_items.append(item)
            else:
                ext_other_items.append(item)
        managed_items.sort(
            key=lambda x: (0 if (x.get("active") or x.get("id") == active_ep) else 1)
        )
        ordered = managed_items + ext_gpu_items + ext_other_items

        rt_state = str(active_runtime.get("state") or "stopped")
        with self.monitor_lock:
            mode = self.monitor_mode
            selected = self.monitor_endpoint
            if mode == "manual" and selected in by_id:
                return ordered, selected, False
            if mode == "manual" and selected and selected not in by_id:
                # Selected instance stopped or disappeared -> fall back cleanly to auto.
                self.monitor_mode = "auto"
                self.monitor_endpoint = ""
                self.monitor_paused = False

        # Auto selection priority:
        # 1. Current machine's active managed instance
        # 2. Any other managed instance on this machine
        # 3. Local starting/releasing/disconnected runtime before allocation lands
        # 4. First running remote GPU instance
        # 5. First running remote non-GPU instance
        if active_ep and active_ep in by_id:
            return ordered, active_ep, True
        if managed_items:
            return ordered, str(managed_items[0]["id"]), True
        if rt_state in ("starting", "releasing", "disconnected"):
            return ordered, str(active_runtime.get("id") or ""), True
        if ext_gpu_items:
            return ordered, str(ext_gpu_items[0]["id"]), True
        if ext_other_items:
            return ordered, str(ext_other_items[0]["id"]), True
        if rt_state != "stopped":
            return ordered, str(active_runtime.get("id") or ""), True
        return ordered, "", True

    def _build_monitor_snapshot(
        self,
        enriched: list[dict],
        active_runtime: dict,
        decorated_models: list[dict],
        active_ep: str | None,
    ) -> dict:
        ordered, target_ep, is_auto = self._resolve_monitor_instances(
            enriched, active_runtime, active_ep
        )
        with self.monitor_lock:
            paused = self.monitor_paused
            obs_map = copy.deepcopy(self.external_observations)

        by_id = {str(item["id"]): item for item in ordered if item.get("id")}
        options: list[dict] = []
        for item in ordered:
            ep = str(item["id"])
            is_managed = bool(item.get("managed"))
            if is_managed:
                opt_source = "local"
                opt_state = str(item.get("state") or "ready")
                gpu_name = str(item.get("gpu_name") or "")
                loaded_model = str(item.get("loaded_model") or "")
                loc = item.get("location")
            else:
                obs = obs_map.get(ep) or {}
                opt_source = (
                    "remote_colabone"
                    if obs.get("source") == "remote_colabone"
                    else "remote_readonly"
                )
                if paused and ep == target_ep:
                    opt_state = "paused"
                elif obs.get("runtime"):
                    opt_state = str(obs.get("state") or "ready")
                elif obs.get("error"):
                    opt_state = "error"
                elif ep == target_ep:
                    opt_state = "connecting"
                else:
                    opt_state = "idle"
                obs_rt = obs.get("runtime") if isinstance(obs.get("runtime"), dict) else {}
                obs_gpu = obs_rt.get("gpu") if isinstance(obs_rt.get("gpu"), dict) else {}
                gpu_name = str(obs_gpu.get("name") or item.get("gpu_name") or "")
                loaded_model = str(
                    obs.get("loaded_model_name") or item.get("loaded_model") or ""
                )
                loc = obs_rt.get("location") or item.get("location")
            opt = {
                "id": ep,
                "managed": is_managed,
                "active": bool(item.get("active") and is_managed),
                "accelerator": str(item.get("accelerator") or ""),
                "variant": str(item.get("variant") or ""),
                "shape": str(item.get("shape") or ""),
                "gpu_name": gpu_name,
                "loaded_model": loaded_model,
                "source": opt_source,
                "state": opt_state,
            }
            if loc:
                opt["location"] = loc
            options.append(opt)

        mode = "auto" if is_auto else "manual"
        active_loaded_id = str(active_runtime.get("loaded_model") or "")
        active_loaded_name = next(
            (
                str(m.get("name") or active_loaded_id)
                for m in decorated_models
                if m.get("id") == active_loaded_id
            ),
            active_loaded_id,
        )

        # Case 1: Active local runtime (or local starting/ready without vault entry)
        target_item = by_id.get(target_ep)
        if not target_ep:
            rt_state = str(active_runtime.get("state") or "stopped")
            if rt_state != "stopped":
                drive = active_runtime.get("drive") or {}
                return {
                    "mode": mode,
                    "endpoint": "",
                    "managed": True,
                    "source": "local",
                    "paused": False,
                    "error": "",
                    "runtime": copy.deepcopy(active_runtime),
                    "loaded_model_name": active_loaded_name,
                    "drive_connected": bool(drive.get("connected")),
                    "drive_free_bytes": drive.get("free_bytes"),
                    "options": options,
                }
            return {
                "mode": mode,
                "endpoint": "",
                "managed": False,
                "source": "none",
                "paused": False,
                "error": "",
                "runtime": {"state": "stopped"},
                "loaded_model_name": "",
                "drive_connected": False,
                "drive_free_bytes": None,
                "options": options,
            }

        if target_item is None or target_item.get("managed"):
            if target_ep == active_ep or target_item is None:
                rt = copy.deepcopy(active_runtime)
                if target_ep and not rt.get("id"):
                    rt["id"] = target_ep
                drive = rt.get("drive") or {}
                return {
                    "mode": mode,
                    "endpoint": target_ep,
                    "managed": True,
                    "source": "local",
                    "paused": False,
                    "error": "",
                    "runtime": rt,
                    "loaded_model_name": active_loaded_name,
                    "drive_connected": bool(drive.get("connected")),
                    "drive_free_bytes": drive.get("free_bytes"),
                    "options": options,
                }
            # Secondary managed instance on this client
            with self.lock:
                cached = copy.deepcopy(self.instance_states.get(target_ep) or {})
            rt = copy.deepcopy(cached.get("runtime") or {"state": "ready", "id": target_ep})
            if not rt.get("id"):
                rt["id"] = target_ep
            if target_item.get("location") and not rt.get("location"):
                rt["location"] = copy.deepcopy(target_item["location"])
            sec_models = cached.get("models") or []
            sec_loaded_id = str(rt.get("loaded_model") or "")
            sec_loaded_name = next(
                (
                    str(m.get("name") or sec_loaded_id)
                    for m in sec_models
                    if m.get("id") == sec_loaded_id
                ),
                sec_loaded_id,
            )
            drive = rt.get("drive") or {}
            return {
                "mode": mode,
                "endpoint": target_ep,
                "managed": True,
                "source": "local",
                "paused": False,
                "error": "",
                "runtime": rt,
                "loaded_model_name": sec_loaded_name,
                "drive_connected": bool(drive.get("connected")),
                "drive_free_bytes": drive.get("free_bytes"),
                "options": options,
            }

        # Case 2: External (non-managed) remote instance (read-only observer)
        obs = obs_map.get(target_ep) or {}
        source = (
            "remote_colabone"
            if obs.get("source") == "remote_colabone"
            else "remote_readonly"
        )
        obs_rt = (
            copy.deepcopy(obs["runtime"])
            if isinstance(obs.get("runtime"), dict)
            else None
        )
        error_msg = "" if paused else str(obs.get("error") or "")
        if obs_rt is not None:
            if not obs_rt.get("id"):
                obs_rt["id"] = target_ep
            if target_item.get("location") and not obs_rt.get("location"):
                obs_rt["location"] = copy.deepcopy(target_item["location"])
            if not paused and error_msg and obs.get("consecutive_failures", 0) >= 3:
                obs_rt["state"] = "disconnected"
            else:
                obs_rt["state"] = "ready"
            rt = obs_rt
        else:
            rt = {
                "id": target_ep,
                "state": "stopped"
                if paused
                else ("disconnected" if error_msg else "connecting"),
            }
            if target_item.get("location"):
                rt["location"] = copy.deepcopy(target_item["location"])
        return {
            "mode": mode,
            "endpoint": target_ep,
            "managed": False,
            "source": source,
            "paused": paused,
            "error": error_msg,
            "runtime": rt,
            "loaded_model_name": str(obs.get("loaded_model_name") or ""),
            "drive_connected": bool(obs.get("drive_connected")),
            "drive_free_bytes": obs.get("drive_free_bytes"),
            "options": options,
        }

    def select_monitor(self, endpoint: str = ""):
        ep = str(endpoint or "").strip()
        active_ep = self._active_endpoint() or ""
        if not ep:
            self._reset_monitor_to_auto(close_observer=True)
            return {"ok": True, "mode": "auto", "endpoint": ""}
        with self.lock:
            account = copy.deepcopy(self.data.get("account") or {})
        enriched = self._enrich_instances(account)
        target_item = next((item for item in enriched if item.get("id") == ep), None)
        if target_item is None:
            raise ValueError("所选实例不存在或已停止")
        is_managed = bool(target_item.get("managed"))
        with self.monitor_lock:
            if is_managed and ep == active_ep:
                self.monitor_mode = "auto"
                self.monitor_endpoint = ""
            else:
                self.monitor_mode = "manual"
                self.monitor_endpoint = ep
            self.monitor_paused = False
            if not is_managed:
                obs = self.external_observations.get(ep)
                if isinstance(obs, dict):
                    obs["consecutive_failures"] = 0
                    obs["last_attempt"] = 0.0
                    obs["error"] = ""
            mode = self.monitor_mode
        if is_managed:
            self._close_external_observer(shutdown_kernel=True)
        else:
            self._observer_wake.set()
        return {"ok": True, "mode": mode, "endpoint": ep}

    def set_monitor_paused(self, paused: bool | None = None):
        with self.monitor_lock:
            next_paused = (not self.monitor_paused) if paused is None else bool(paused)
            self.monitor_paused = next_paused
            if not next_paused:
                for obs in self.external_observations.values():
                    if isinstance(obs, dict):
                        obs["consecutive_failures"] = 0
                        obs["last_attempt"] = 0.0
                        obs["error"] = ""
        if next_paused:
            self._close_external_observer(shutdown_kernel=True)
        else:
            self._observer_wake.set()
        return {"ok": True, "paused": next_paused}

    def retry_monitor(self):
        with self.monitor_lock:
            self.monitor_paused = False
            for obs in self.external_observations.values():
                if isinstance(obs, dict):
                    obs["consecutive_failures"] = 0
                    obs["last_attempt"] = 0.0
                    obs["error"] = ""
        self._observer_wake.set()
        return {"ok": True}

    def poll_external_monitor_once(self, force: bool = False) -> bool:
        """Sample hardware telemetry for the currently targeted external instance if active."""
        if not self._observer_poll_lock.acquire(blocking=False):
            return False
        try:
            with self.lock:
                account = copy.deepcopy(self.data.get("account") or {})
                active_runtime = copy.deepcopy(self.data.get("runtime") or {})
            if not account.get("logged_in") and not account.get("instances"):
                self._close_external_observer(shutdown_kernel=True)
                return False
            active_ep = self._active_endpoint()
            enriched = self._enrich_instances(account)
            known_ids = {str(item["id"]) for item in enriched if item.get("id")}

            # Evict stale observations for instances that no longer exist.
            with self.monitor_lock:
                stale = [
                    ep for ep in list(self.external_observations.keys()) if ep not in known_ids
                ]
                for ep in stale:
                    self.external_observations.pop(ep, None)
            for ep in stale:
                self._close_external_observer(endpoint=ep, shutdown_kernel=False)

            ordered, target_ep, _ = self._resolve_monitor_instances(
                enriched, active_runtime, active_ep
            )
            by_id = {str(item["id"]): item for item in ordered if item.get("id")}
            target_item = by_id.get(target_ep)

            with self.monitor_lock:
                paused = self.monitor_paused

            if (
                not target_ep
                or target_item is None
                or target_item.get("managed")
                or paused
                or self.operation_lock.locked()
            ):
                if paused or not target_ep or (target_item and target_item.get("managed")):
                    self._close_external_observer(shutdown_kernel=True)
                return False

            now = time.time()
            with self.monitor_lock:
                prev = self.external_observations.get(target_ep) or {}
                failures = int(prev.get("consecutive_failures") or 0)
                last_attempt = float(prev.get("last_attempt") or 0.0)
            min_gap = (
                2.5
                if failures <= 0
                else min(15.0, 3.0 * (2 ** min(failures - 1, 3)))
            )
            if not force and last_attempt > 0 and (now - last_attempt) < min_gap:
                return False

            observe_fn = getattr(self.provider, "observe_external_telemetry", None)
            if not callable(observe_fn):
                return False

            with self.monitor_lock:
                slot = self.external_observations.setdefault(target_ep, {})
                slot["last_attempt"] = now

            try:
                sample = observe_fn(target_ep)
            except LookupError:
                with self.monitor_lock:
                    self.external_observations.pop(target_ep, None)
                self._close_external_observer(endpoint=target_ep, shutdown_kernel=False)
                self.last_auth = 0
                return False
            except Exception as exc:
                with self.monitor_lock:
                    slot = self.external_observations.setdefault(target_ep, {})
                    slot["consecutive_failures"] = int(slot.get("consecutive_failures") or 0) + 1
                    slot["last_attempt"] = time.time()
                    msg = safe_message(str(exc)[:180])
                    slot["error"] = msg or "暂无法读取该远端实例的硬件负载，将自动重试"
                return False

            # Re-verify that the user did not pause or switch away while RPC was in flight.
            with self.lock:
                cur_account = copy.deepcopy(self.data.get("account") or {})
                cur_runtime = copy.deepcopy(self.data.get("runtime") or {})
            cur_enriched = self._enrich_instances(cur_account)
            _, cur_target, _ = self._resolve_monitor_instances(
                cur_enriched, cur_runtime, self._active_endpoint()
            )
            with self.monitor_lock:
                still_active = (not self.monitor_paused) and (cur_target == target_ep)
                if not still_active:
                    pass
                else:
                    rt_data = (
                        copy.deepcopy(sample["runtime"])
                        if isinstance(sample.get("runtime"), dict)
                        else {"id": target_ep, "state": "ready"}
                    )
                    if target_item.get("location") and not rt_data.get("location"):
                        rt_data["location"] = copy.deepcopy(target_item["location"])
                    self.external_observations[target_ep] = {
                        "state": "ready",
                        "source": str(sample.get("source") or "remote_readonly"),
                        "runtime": rt_data,
                        "loaded_model_name": str(sample.get("loaded_model_name") or ""),
                        "drive_connected": bool(sample.get("drive_connected")),
                        "drive_free_bytes": sample.get("drive_free_bytes"),
                        "error": "",
                        "updated_at": time.time(),
                        "last_attempt": time.time(),
                        "consecutive_failures": 0,
                    }
            if not still_active:
                self._close_external_observer(endpoint=target_ep, shutdown_kernel=True)
                return False
            return True
        finally:
            self._observer_poll_lock.release()

    def _external_monitor_loop(self):
        while not self.stop.is_set():
            try:
                self.poll_external_monitor_once(force=False)
            except Exception:
                pass
            self._observer_wake.wait(1.0)
            self._observer_wake.clear()

    def snapshot(self):
        with self.lock:
            value = copy.deepcopy(self.data)
            common_repos = copy.deepcopy(self.common_repos)
        value["models"] = self._decorate_models(value.get("models") or [])
        value["workflows"] = self._merge_workflows(value.get("workflows"))
        value["comfy_outputs"] = value.get("comfy_outputs", [])
        value["common_repos"] = common_repos
        account = value.get("account") or {}
        enriched: list[dict] = []
        if account.get("instances") is not None or self._vault_sessions():
            enriched = self._enrich_instances(account)
            value["account"] = dict(account, instances=enriched)
            if "assignments" not in value["account"] and enriched:
                value["account"]["assignments"] = len(enriched)
        active_ep = self._active_endpoint()
        value["active_endpoint"] = active_ep or ""
        value["monitor"] = self._build_monitor_snapshot(
            enriched, value["runtime"], value["models"], active_ep
        )
        value["settings"] = {
            k: v
            for k, v in self.settings.items()
            if k not in ("idle_policy", "rclone_drive_policy")
        }
        api = self.vault.get("public_api", {})
        if not isinstance(api, dict):
            api = {}
        value["api_settings"] = {
            "mode": "named",
            "domain": api.get("domain", ""),
            "has_token": bool(api.get("tunnel_token")),
            "has_key": bool(api.get("api_key")),
        }
        canvas = self.vault.get("comfyui_canvas", {})
        if not isinstance(canvas, dict):
            canvas = {}
        value["comfy_canvas_settings"] = {
            "mode": "named",
            "domain": canvas.get("domain", ""),
            "has_token": bool(canvas.get("tunnel_token")),
        }
        value["logs"] = self.activity.snapshot()
        drive_info = value["runtime"].get("drive") or {}
        drive_auth_fn = getattr(self.auth, "has_drive_scope", None)
        drive_authorized = bool(drive_auth_fn()) if callable(drive_auth_fn) else False
        if not isinstance(drive_authorized, bool):
            drive_authorized = False
        req_quota_fn = getattr(self.auth, "requires_drive_quota_project", None)
        needs_quota = bool(req_quota_fn()) if callable(req_quota_fn) else False
        quota_status_fn = getattr(self.auth, "drive_quota_status", None)
        quota_status = quota_status_fn() if callable(quota_status_fn) else None
        drive_quota_reason = (
            str(quota_status.get("reason") or "")
            if isinstance(quota_status, dict)
            else ""
        )
        drive_headless = drive_authorized and (
            (not needs_quota) or bool(self.vault.get("cloud_project"))
        )
        shortcuts = shortcut_status(sandbox_dir=self.shortcut_sandbox)
        value["setup"] = {
            "colab": bool(self.vault.get("colab")),
            "persistent_login": bool(
                (self.vault.get("colab") or {}).get("refresh_token")
            ),
            "drive_authorized": drive_authorized,
            "drive_headless": drive_headless,
            "drive_quota_reason": drive_quota_reason,
            "drive": bool(drive_info.get("connected")),
            "drive_mode": str(drive_info.get("mode") or "disabled"),
            "hf": bool(self.vault.get("hf")),
            "start_menu_shortcut": bool(shortcuts.get("start_menu")),
            "desktop_shortcut": bool(shortcuts.get("desktop")),
        }
        return value

    def update(self, **values):
        with self.lock:
            operation = values.get("operation")
            if operation and operation != self.data.get("operation"):
                self.activity.add(operation["title"])
            if values.get("error") and values["error"] != self.data.get("error"):
                self.activity.add(values["error"], level="error")
            runtime = values.get("runtime")
            if runtime:
                previous = self.data["runtime"]
                for sec, old_sec, tag in (
                    (runtime.get("public_api"), previous.get("public_api"), "API"),
                    (
                        (runtime.get("comfyui") or {}).get("canvas")
                        if isinstance(runtime.get("comfyui"), dict)
                        else None,
                        (previous.get("comfyui") or {}).get("canvas")
                        if isinstance(previous.get("comfyui"), dict)
                        else None,
                        "ComfyUI",
                    ),
                ):
                    if not isinstance(sec, dict):
                        continue
                    old_logs = old_sec.get("logs") if isinstance(old_sec, dict) else None
                    cursor = old_logs[-1].get("seq", 0) if old_logs else 0
                    for entry in sec.get("logs") or []:
                        if isinstance(entry, dict) and entry.get("seq", 0) > cursor:
                            self.activity.add(
                                entry["message"], tag, entry.get("level", "info")
                            )
                labels = {
                    "ready": "远端 GPU 已连接",
                    "disconnected": "远端控制连接中断，实例可能仍在计费",
                    "stopped": "当前客户端管理的 GPU 已停止",
                    "releasing": "正在释放远端 GPU",
                }
                if (
                    runtime.get("state") != previous.get("state")
                    and runtime.get("state") in labels
                ):
                    self.activity.add(labels[runtime["state"]], "GPU")
                for task in runtime.get("tasks", []):
                    cursor = self.task_log_cursor.get(task["id"], 0)
                    for entry in task.get("logs", []):
                        if entry["seq"] > cursor:
                            self.activity.add(
                                entry["message"], "远端", entry.get("level", "info")
                            )
                            cursor = entry["seq"]
                    self.task_log_cursor[task["id"]] = cursor
                    bucket = int(task.get("progress", 0) * 10)
                    signature = (
                        task["kind"],
                        task["state"],
                        bucket,
                        task.get("message", ""),
                    )
                    if self.task_activity.get(task["id"]) == signature:
                        continue
                    self.task_activity[task["id"]] = signature
                    kind = {
                        "download": "模型下载",
                        "load": "模型加载",
                        "public_chat": "外部 API 对话",
                        "public_responses": "外部 API Responses",
                        "public_compact": "外部 API 上下文压缩",
                        "comfy_start": "ComfyUI 启动",
                        "comfy_run": "ComfyUI 工作流",
                        "comfyui": "ComfyUI 任务",
                        "comfyui_start": "ComfyUI 启动",
                        "comfyui_run": "ComfyUI 工作流",
                    }.get(task["kind"], "远端任务")
                    status = {
                        "running": "进行中",
                        "completed": "已完成",
                        "failed": "失败",
                        "cancelled": "已取消",
                    }.get(task["state"], task["state"])
                    detail = f" · {bucket * 10}%" if task["kind"] == "download" else ""
                    detail += (
                        (" · " + task.get("message", ""))
                        if task.get("message")
                        else ""
                    )
                    if task.get("error"):
                        detail += " · " + task["error"]
                    self.activity.add(
                        kind + "：" + status + detail,
                        "远端",
                        "error" if task["state"] == "failed" else "info",
                    )
                idle = runtime.get("idle", {})
                if (
                    idle.get("phase") == "countdown"
                    and previous.get("idle", {}).get("phase") != "countdown"
                ):
                    rem_sec = int(round(idle.get("remaining_seconds", 60)))
                    self.activity.add(
                        f"空闲自动释放倒计时剩余 {rem_sec} 秒；继续操作可重置倒计时",
                        "GPU",
                        "warning",
                    )
                self.task_log_cursor = {
                    t["id"]: self.task_log_cursor.get(t["id"], 0)
                    for t in runtime.get("tasks", [])
                }
                # Bound task deduplication state to the current snapshot.
                self.task_activity = {
                    t["id"]: self.task_activity[t["id"]]
                    for t in runtime.get("tasks", [])
                }
            self.data.update(values)
            if any(
                k in values
                for k in ("runtime", "models", "workflows", "comfy_outputs")
            ):
                self._save_active_to_cache()

    def async_operation(self, title, fn):
        if not self.operation_lock.acquire(blocking=False):
            raise RuntimeError("请等待当前操作完成")
        self.update(operation={"title": title, "running": True}, error="", auth_url="")

        def run():
            try:
                fn()
                self.activity.add(title + "：完成")
            except ColabOneOperationCancelled as exc:
                self.activity.add(safe_message(str(exc)[:300]), "云盘", "info")
            except Exception as exc:
                # HTTP exception URLs may contain OAuth codes: surface only a safe reason.
                if isinstance(exc, requests.HTTPError):
                    message = (
                        f"服务返回 HTTP {exc.response.status_code}，请检查授权和配置"
                    )
                else:
                    message = str(exc)[:500]
                self.update(error=safe_message(message))
            finally:
                self.update(operation=None)
                self.operation_lock.release()

        threading.Thread(target=run, daemon=True).start()
        return {"ok": True}

    def remote(
        self,
        method,
        path,
        body=None,
        endpoint: str | None = None,
        timeout: int | None = None,
    ):
        kwargs = {}
        if endpoint is not None:
            kwargs["endpoint"] = endpoint
        if timeout is not None and ColabOneController._accepts_kwarg(
            self.provider.rpc, "timeout"
        ):
            kwargs["timeout"] = timeout
        return self.provider.rpc(method, path, body, **kwargs)

    def _remote_long(self, method, path, body=None, timeout: int = 180):
        if getattr(self.remote, "__func__", None) is ColabOneController.remote:
            return self.remote(method, path, body, timeout=timeout)
        if body is None:
            return self.remote(method, path)
        return self.remote(method, path, body)

    def configure_remote(self, endpoint: str | None = None):
        rt = (
            self.data["runtime"]
            if endpoint is None or endpoint == self._active_endpoint()
            else self._ensure_instance_state(endpoint)["runtime"]
        )
        self.remote(
            "POST",
            "configure",
            {
                "idle_minutes": self.settings["idle_minutes"]
                if rt.get("protocol", 0) >= 2
                else 4,
                "idle_countdown_seconds": self.settings["idle_countdown_seconds"],
                "deadline": 0,
            },
            endpoint=endpoint,
        )
        if endpoint is None or endpoint == self._active_endpoint():
            self.last_config = time.time()
        if endpoint:
            self._ensure_instance_state(endpoint)["last_config"] = time.time()

    def _accepts_kwarg(fn, name: str) -> bool:
        try:
            params = inspect.signature(fn).parameters
            return name in params or any(
                p.kind == inspect.Parameter.VAR_KEYWORD for p in params.values()
            )
        except (TypeError, ValueError):
            return False

    def _model_label(self, model_id: str) -> str:
        with self.lock:
            for item in self.data.get("models") or []:
                if isinstance(item, dict) and item.get("id") == model_id:
                    return str(item.get("name") or model_id)
            custom = self.custom_models.get(model_id)
            if isinstance(custom, dict) and custom.get("name"):
                return str(custom["name"])
        return model_id

    def upgrade_service(self):
        self.activity.add("更新远端管理服务，保留模型缓存与工作流", "GPU")
        if ColabOneController._accepts_kwarg(self.provider.upgrade_service, "on_log"):
            self.provider.upgrade_service(on_log=self.activity.add)
        else:
            self.provider.upgrade_service()
        self.activity.add("正在刷新远端服务状态", "GPU")
        self.refresh_remote()
        self.activity.add("正在重新同步空闲自动释放策略", "GPU")
        self.configure_remote()
        self.activity.add("远端管理服务已更新，支持模型 API、ComfyUI 与闲置保护", "GPU")

    def refresh_remote(self, endpoint: str | None = None):
        had_sessions = bool(self._vault_sessions())
        state = self.remote("GET", "snapshot", endpoint=endpoint)
        effective = endpoint or state["runtime"].get("id")
        target = effective or self._active_endpoint()
        sessions = self._vault_sessions()
        still_owned = (not had_sessions) or (bool(target) and target in sessions)
        raw_workflows = state.get("workflows", [])
        merged_workflows = self._merge_workflows(raw_workflows)
        comfy_outputs = state.get("comfy_outputs", [])
        remote_versions = {
            w["id"]: float(w.get("updated_at") or 0)
            for w in (raw_workflows if isinstance(raw_workflows, list) else [])
            if isinstance(w, dict) and w.get("id")
        }
        if target and still_owned:
            remotes = self._vault_remotes()
            rem = remotes.get(target) or {}
            loc = (
                state["runtime"].get("location")
                or rem.get("location")
                or infer_location_from_endpoint(
                    target, str((sessions.get(target) or {}).get("url") or "")
                )
            )
            if loc:
                state["runtime"]["location"] = loc
                if (
                    target in sessions
                    and rem.get("endpoint") == target
                    and rem.get("location") != loc
                ):
                    self.provider.save_remote(rem | {"location": loc})
            with self.lock:
                entry = self._ensure_instance_state(target)
                entry["runtime"] = copy.deepcopy(state["runtime"])
                entry["models"] = copy.deepcopy(state["models"])
                entry["workflows"] = copy.deepcopy(merged_workflows)
                entry["comfy_outputs"] = copy.deepcopy(comfy_outputs)
                entry["remote_workflow_versions"] = dict(remote_versions)
        active_ep = self._active_endpoint()
        if (
            still_owned
            and (endpoint is None or endpoint == active_ep)
            and (not effective or not active_ep or effective == active_ep)
        ):
            with self.lock:
                self.remote_workflow_versions = dict(remote_versions)
            self.update(
                runtime=state["runtime"],
                models=state["models"],
                workflows=merged_workflows,
                comfy_outputs=comfy_outputs,
            )

    def select_instance(self, endpoint: str):
        sessions = self._vault_sessions()
        if not endpoint or endpoint not in sessions:
            raise ValueError("所选实例不存在或不属于当前客户端")
        self._save_active_to_cache()
        self.provider.set_active(endpoint)
        self._activate_cached(endpoint)
        self._reset_monitor_to_auto(close_observer=True)
        self.last_config = 0
        self.activity.add(f"已切换当前工作实例：{endpoint}", "GPU")
        if not self.operation_lock.locked():
            def _bg_refresh():
                try:
                    self.refresh_remote(endpoint)
                except Exception:
                    pass

            threading.Thread(target=_bg_refresh, daemon=True).start()
        return {"ok": True, "endpoint": endpoint}

    def _resolve_headless_rclone_conf(
        self, force_refresh: bool = False
    ) -> tuple[str, str]:
        """Return `(rclone_conf, reason)` for headless rclone mode when supported.

        For Google's public ADC OAuth client (`764086051850-*`), Google blocks
        `drive.googleapis.com` unless an active Drive-enabled GCP quota project
        is provided via `X-Goog-User-Project`. When no quota project can be
        discovered or provisioned (e.g. GCP ToS not accepted), returns `("", reason)`
        so callers automatically fall back to official Colab native DriveFS.
        """
        build_fn = getattr(self.auth, "build_rclone_conf", None)
        if not callable(build_fn):
            return "", "no_drive_scope"
        req_quota_fn = getattr(self.auth, "requires_drive_quota_project", None)
        needs_quota = bool(req_quota_fn()) if callable(req_quota_fn) else False
        if not isinstance(needs_quota, bool):
            needs_quota = False
        if needs_quota:
            ensure_fn = getattr(self.auth, "ensure_drive_quota_project", None)
            if not callable(ensure_fn):
                return "", "gcp_project_unavailable"
            ensure_kwargs = {"on_log": self.activity.add}
            if force_refresh and ColabOneController._accepts_kwarg(
                ensure_fn, "force_refresh"
            ):
                ensure_kwargs["force_refresh"] = True
            project_id, reason = ensure_fn(**ensure_kwargs)
            if not project_id:
                return "", reason or "gcp_project_unavailable"
            conf = (
                build_fn(quota_project=project_id)
                if ColabOneController._accepts_kwarg(build_fn, "quota_project")
                else build_fn()
            )
            if isinstance(conf, str) and "[colabone_gdrive]" in conf:
                return conf, "rclone"
            return "", "no_drive_scope"
        conf = build_fn()
        if isinstance(conf, str) and "[colabone_gdrive]" in conf:
            return conf, "rclone"
        return "", "no_drive_scope"

    @staticmethod
    def _is_quota_project_error(exc: Exception) -> bool:
        msg = str(exc).lower()
        return any(
            needle in msg
            for needle in (
                "accessnotconfigured",
                "service_disabled",
                "quota project",
                "配额项目",
            )
        )

    def _connect_colab_drivefs(self, silent_only: bool = False) -> dict | None:
        mount_fn = getattr(self.provider, "mount_colab_drivefs", None)
        if not callable(mount_fn):
            return None
        if silent_only:
            check_fn = getattr(self.provider, "check_colab_drivefs_auth", None)
            if not callable(check_fn):
                return None
            authorized, _ = check_fn()
            if not authorized:
                return None
        self.drive_cancel.clear()
        self.update(
            operation={
                "title": "正在通过 Colab 原生驱动挂载 Google 云盘 (ColabOne_Drive)",
                "running": True,
            },
            auth_url="",
        )
        try:
            mount_fn(
                on_auth_url=lambda u: self.update(auth_url=u),
                on_log=self.activity.add,
                cancel_event=self.drive_cancel,
            )
        except Exception:
            if self.drive_cancel.is_set():
                raise ColabOneOperationCancelled("已取消 Google 云盘挂载") from None
            raise
        finally:
            self.update(auth_url="")
        self.activity.add("正在初始化 ColabOne_Drive 工作区并恢复持久化数据", "云盘")
        return self._remote_long("POST", "drive/connect", timeout=180)

    def connect_drive(self):
        self.activity.add("正在检查 Google 云盘挂载方式与授权状态", "云盘")
        # Bypass the negative probe cache on explicit user clicks when no quota
        # project is cached yet, so accepting GCP ToS takes effect immediately.
        rclone_conf, reason = self._resolve_headless_rclone_conf(
            force_refresh=not bool(self.vault.get("cloud_project"))
        )
        if rclone_conf:
            try:
                self.activity.add("正在上传无感云盘凭据到远端实例", "云盘")
                self.provider.upload_drive_config(rclone_conf)
                self.activity.add("正在远端初始化 rclone 并挂载 ColabOne_Drive", "云盘")
                info = self._remote_long("POST", "drive/connect", timeout=180)
                self.activity.add("正在刷新云盘与远端工作区状态", "云盘")
                self.refresh_remote()
                mode = info.get("mode")
                if mode == "mount":
                    self.activity.add(
                        "已通过 Google OAuth 自动挂载云盘 (/content/drive/MyDrive → ColabOne_Drive)",
                        "云盘",
                    )
                else:
                    self.activity.add(
                        "已通过 Google OAuth 连接云盘持久化目录 (ColabOne_Drive)",
                        "云盘",
                    )
                return info
            except Exception as exc:
                if not self._is_quota_project_error(exc):
                    raise
                inv_fn = getattr(self.auth, "invalidate_drive_quota_project", None)
                if callable(inv_fn):
                    inv_fn()
                self.activity.add(
                    "无感配额项目暂不可用，自动切换为 Google Colab 官方原生驱动挂载",
                    "云盘",
                    "warning",
                )

        if reason == "gcp_tos_required":
            self.activity.add(
                "当前账号尚未开通免费 GCP 配额项目，自动切换为 Google Colab 官方原生驱动挂载",
                "云盘",
            )
        elif not rclone_conf:
            self.activity.add(
                "正在通过 Google Colab 官方原生驱动挂载云盘 (ColabOne_Drive)",
                "云盘",
            )

        info = self._connect_colab_drivefs(silent_only=False)
        if info is None:
            raise RuntimeError(
                "当前保存的登录缺少云盘权限，请在设置中点击一次「重新登录」完成授权，后续所有实例永久自动生效"
            )
        self.activity.add("正在刷新云盘与远端工作区状态", "云盘")
        self.refresh_remote()
        self.activity.add(
            "已通过 Google Colab 原生驱动挂载云盘 (/content/drive/MyDrive → ColabOne_Drive)",
            "云盘",
        )
        return info

    def setup_headless_drive(self):
        if not self.vault.get("colab"):
            raise RuntimeError("请先登录 Google 账号")
        has_scope_fn = getattr(self.auth, "has_drive_scope", None)
        if callable(has_scope_fn) and not bool(has_scope_fn()):
            raise RuntimeError(
                "当前登录凭据缺少云盘免弹窗所需权限，请在设置中点击「重新登录」后再试。"
            )
        ensure_fn = getattr(self.auth, "ensure_drive_quota_project", None)
        if not callable(ensure_fn):
            raise RuntimeError(
                "暂未能自动创建免费 GCP 配额项目（可能受账号或组织策略限制），仍可正常使用 Colab 原生云盘挂载。"
            )
        self.activity.add("正在检测并开启 Google 云盘永久免弹窗挂载模式", "云盘")
        ensure_kwargs = {"on_log": self.activity.add}
        if ColabOneController._accepts_kwarg(ensure_fn, "force_refresh"):
            ensure_kwargs["force_refresh"] = True
        project_id, reason = ensure_fn(**ensure_kwargs)
        if reason == "not_required":
            self.activity.add("当前 Google 凭据已支持云盘永久免弹窗自动挂载", "云盘")
            return {"ok": True, "headless": True, "project_id": "", "reason": reason}
        if project_id:
            if reason != "created_colabone_project":
                self.activity.add(
                    f"已确认免弹窗云盘配额项目 ({project_id})，后续所有实例免弹窗自动挂载",
                    "云盘",
                )
            return {
                "ok": True,
                "headless": True,
                "project_id": project_id,
                "reason": reason,
            }
        if reason == "gcp_tos_required":
            raise RuntimeError(
                "尚未检测到 Google Cloud 服务条款同意记录。请先打开 Google Cloud 同意一次服务条款（免费、无需绑卡），再重试检测。"
            )
        if reason == "no_cloud_scope":
            raise RuntimeError(
                "当前登录凭据缺少云盘免弹窗所需权限，请在设置中点击「重新登录」后再试。"
            )
        if reason == "gcp_project_unavailable":
            raise RuntimeError(
                "暂未能自动创建免费 GCP 配额项目（可能受账号或组织策略限制），仍可正常使用 Colab 原生云盘挂载。"
            )
        raise RuntimeError(
            "检测 Google Cloud 配额项目时网络异常，请检查网络或代理后重试。"
        )

    def sync_drive(self):
        if not self._vault_sessions():
            raise RuntimeError("请先启动远端 GPU 实例")
        self.activity.add("正在将远端配置、工作流与作品同步至 Google 云盘", "云盘")
        result = self._remote_long("POST", "drive/sync", timeout=180)
        self.refresh_remote()
        if not result.get("ok"):
            raise RuntimeError(result.get("error") or "Google 云盘同步未完成，请重试")
        self.activity.add("Google 云盘同步已完成 (ColabOne_Drive)", "云盘")
        return result

    def start(self, create_new: bool = False, gpu: str | None = None):
        self.activity.add("校验 Google 登录凭据", "登录")
        self.auth.credentials("colab")
        target_gpu = gpu or self.settings["gpu"]
        if target_gpu not in ("T4", "L4", "A100", "G4", "H100"):
            raise ValueError("GPU 型号不合法")

        if create_new:
            self.activity.add(
                f"准备新增远端 {target_gpu} 实例（保留现有运行实例）",
                "GPU",
            )
            self._save_active_to_cache()
            with self.lock:
                self.data["models"] = self.presets()
                self.data["workflows"] = self._merge_workflows(None)
                self.data["comfy_outputs"] = []
                self.remote_workflow_versions = {}

        previous_active = self._active_endpoint()
        existing_sessions = set(self._vault_sessions().keys())
        rclone_conf = ""
        drive_reason = ""
        if self.settings.get("auto_drive", True):
            try:
                rclone_conf, drive_reason = self._resolve_headless_rclone_conf()
            except Exception:
                rclone_conf, drive_reason = "", "error"
            if rclone_conf:
                self.activity.add("已准备 Google 云盘自动挂载凭据", "云盘")
            elif drive_reason in ("gcp_tos_required", "gcp_project_unavailable"):
                self.activity.add(
                    "当前账号未配置免费 GCP 配额项目，GPU 就绪后将使用 Colab 官方原生云盘模式",
                    "云盘",
                )
            else:
                self.activity.add("未检测到云盘无感凭据，本次启动跳过无感挂载", "云盘")
        else:
            self.activity.add("未开启自动挂载 Google 云盘，跳过云盘配置", "云盘")

        self._reset_monitor_to_auto(close_observer=True)
        self.update(runtime={"state": "starting"})

        def notify(message):
            with self.lock:
                current = copy.deepcopy(self.data.get("runtime") or {})
                if current.get("state") == "ready":
                    self.update(operation={"title": message, "running": True})
                    return
                next_rt = dict(current)
                next_rt["state"] = "starting"
                active_ep = self._active_endpoint()
                if active_ep and (not create_new or active_ep not in existing_sessions):
                    if not next_rt.get("id"):
                        next_rt["id"] = active_ep
                    rem = self._vault_remotes().get(active_ep) or {}
                    if rem.get("location") and not next_rt.get("location"):
                        next_rt["location"] = rem["location"]
                self.update(
                    runtime=next_rt,
                    operation={"title": message, "running": True},
                )

        def on_boot_telemetry(endpoint, location, sample):
            if not isinstance(sample, dict):
                return
            with self.lock:
                current = copy.deepcopy(self.data.get("runtime") or {})
                if current.get("state") != "starting":
                    return
                next_rt = dict(current)
                next_rt["state"] = "starting"
                if endpoint:
                    next_rt["id"] = endpoint
                if location:
                    next_rt["location"] = location
                if isinstance(sample.get("gpu"), dict):
                    next_rt["gpu"] = sample["gpu"]
                next_rt["resources"] = sample
                disk = sample.get("disk")
                if isinstance(disk, dict) and isinstance(
                    disk.get("free_bytes"), (int, float)
                ):
                    next_rt["disk_free"] = disk["free_bytes"]
                self.update(runtime=next_rt)

        try:
            start_kwargs = {}
            if ColabOneController._accepts_kwarg(self.provider.start, "on_telemetry"):
                start_kwargs["on_telemetry"] = on_boot_telemetry
            if ColabOneController._accepts_kwarg(self.provider.start, "on_log"):
                start_kwargs["on_log"] = self.activity.add
            if create_new:
                start_kwargs["create_new"] = True
            if rclone_conf:
                start_kwargs["rclone_conf"] = rclone_conf
            self.provider.start(target_gpu, notify, **start_kwargs)
            self.activity.add("正在拉取远端实例硬件与模型快照", "GPU")
            try:
                self.refresh_remote()
            except Exception:
                self.activity.add(
                    "首次获取远端快照未响应，1.5 秒后自动重试", "GPU", "warning"
                )
                time.sleep(1.5)
                self.refresh_remote()
            self.activity.add(
                f"正在同步空闲自动释放策略（{self.settings['idle_minutes']} 分钟）",
                "GPU",
            )
            self.configure_remote()
            self.activity.add("远端空闲自动释放策略已生效", "GPU")
            if not rclone_conf and self.data["runtime"].get("idle_minutes") != self.settings.get(
                "idle_minutes"
            ):
                self.refresh_remote()
            if rclone_conf:
                self.update(
                    operation={
                        "title": "正在自动挂载 Google 云盘 (ColabOne_Drive)",
                        "running": True,
                    }
                )
                try:
                    info = self._remote_long("POST", "drive/connect", timeout=180)
                    if info.get("mode") == "mount":
                        self.activity.add(
                            "已自动挂载 Google 云盘 (/content/drive/MyDrive → ColabOne_Drive)",
                            "云盘",
                        )
                    else:
                        self.activity.add(
                            "已自动连接 Google 云盘持久化目录 (ColabOne_Drive)",
                            "云盘",
                        )
                except Exception as drive_exc:
                    if self._is_quota_project_error(drive_exc):
                        inv_fn = getattr(
                            self.auth, "invalidate_drive_quota_project", None
                        )
                        if callable(inv_fn):
                            inv_fn()
                    self.activity.add(
                        "云盘自动挂载未就绪，实例仍可正常使用：" + safe_message(drive_exc),
                        "云盘",
                        "warning",
                    )
                try:
                    self.refresh_remote()
                    self.activity.add("已刷新云盘工作区与远端状态", "云盘")
                except Exception:
                    pass
            elif self.settings.get("auto_drive", True) and drive_reason in (
                "gcp_tos_required",
                "gcp_project_unavailable",
            ):
                try:
                    info = self._connect_colab_drivefs(silent_only=True)
                    if info and info.get("connected"):
                        self.refresh_remote()
                        self.activity.add(
                            "已通过 Google Colab 原生驱动自动挂载云盘 (/content/drive/MyDrive → ColabOne_Drive)",
                            "云盘",
                        )
                    else:
                        self.activity.add(
                            "点击「立即挂载云盘」即可在浏览器一键确认 Colab 原生云盘挂载 (ColabOne_Drive)",
                            "云盘",
                        )
                except Exception as drive_exc:
                    self.activity.add(
                        "云盘自动挂载未就绪，实例仍可正常使用：" + safe_message(drive_exc),
                        "云盘",
                        "warning",
                    )
            try:
                self.activity.add("正在刷新账号计费与活跃实例列表", "计费")
                acct = self.provider.account()
                self.update(account=acct)
                self.last_auth = time.time()
                if isinstance(acct, dict) and acct.get("logged_in"):
                    self.activity.add(
                        f"计费同步完成 · 余额 {float(acct.get('balance') or 0):.2f} CU · 费率 {float(acct.get('rate') or 0):.2f} CU/小时 · 活跃 {int(acct.get('assignments') or 0)} 台",
                        "计费",
                    )
            except Exception:
                pass
        except Exception:
            current_sessions = set(self._vault_sessions().keys())
            new_leftover = current_sessions - existing_sessions
            for ep in list(new_leftover):
                try:
                    self.activity.add(
                        f"正在清理残留实例：{ep}", "GPU", "warning"
                    )
                    if len(current_sessions) > 1:
                        self.provider.release(ep)
                    else:
                        self.provider.release()
                except Exception:
                    pass
            remaining_active = self._active_endpoint()
            if (
                create_new
                and previous_active
                and previous_active in self._vault_sessions()
            ):
                self._activate_cached(previous_active)
            else:
                self.update(
                    runtime={"state": "disconnected" if remaining_active else "stopped"}
                )
            raise

    def release(self, force=False, endpoint: str | None = None):
        active_ep = self._active_endpoint()
        target = endpoint or active_ep
        use_explicit = bool(endpoint and endpoint != active_ep)
        if not force:
            self.activity.add("正在通知远端停止任务、卸载模型并同步云盘", "GPU")
            if use_explicit:
                self.remote("POST", "prepare-release", endpoint=target)
            else:
                self.remote("POST", "prepare-release")
            self.activity.add("远端收尾与云盘同步已完成", "GPU")
        else:
            self.activity.add("已选择强制释放，跳过远端收尾检查", "GPU", "warning")
        if not use_explicit:
            self.update(runtime={"state": "releasing"})
        release_kwargs = (
            {"on_log": self.activity.add}
            if ColabOneController._accepts_kwarg(self.provider.release, "on_log")
            else {}
        )
        if use_explicit:
            self.provider.release(target, **release_kwargs)
        else:
            self.provider.release(**release_kwargs)
        if target:
            with self.lock:
                self.instance_states.pop(target, None)
        next_active = self._active_endpoint()
        if next_active and next_active != target:
            self._activate_cached(next_active)
            self.activity.add(f"已释放 {target}，当前切换至 {next_active}", "GPU")
        else:
            with self.lock:
                self.remote_workflow_versions = {}
            self.update(
                runtime={"state": "stopped"},
                models=self.presets(),
                workflows=self._merge_workflows(None),
                comfy_outputs=[],
            )
        try:
            self.activity.add("正在刷新账号计费与实例清单", "计费")
            account = self.provider.account()
            if isinstance(account, dict) and "logged_in" in account:
                self.update(account=account)
                self.last_auth = time.time()
                self._observer_wake.set()
                self.activity.add(
                    f"已刷新账号状态 · 活跃实例 {int(account.get('assignments') or 0)} 台 · 费率 {float(account.get('rate') or 0):.2f} CU/小时",
                    "计费",
                )
        except Exception:
            pass

    def preview_release_all(self):
        account = self.provider.account()
        self.update(account=account)
        enriched = self._enrich_instances(account)
        plan = {
            "id": secrets.token_hex(16),
            "instances": enriched,
            "account_sub": self.auth.profile()["sub"],
        }
        self.release_plan = plan
        return {"id": plan["id"], "instances": enriched}

    def release_all(self, plan_id, force=False, endpoints: list[str] | None = None):
        plan = self.release_plan
        if (
            not plan
            or plan["id"] != plan_id
            or plan["account_sub"] != self.auth.profile()["sub"]
        ):
            raise RuntimeError("实例清单已变化，请重新点击停止")
        reviewed = [item["id"] for item in plan["instances"]]
        if endpoints is not None:
            if not isinstance(endpoints, list) or not endpoints:
                raise ValueError("请至少勾选一个要停止的实例")
            requested = set(endpoints)
            if not requested.issubset(set(reviewed)):
                raise RuntimeError("所选实例不在已确认清单中，请重新打开停止窗口")
            targets = [ep for ep in reviewed if ep in requested]
            partial_selection = set(targets) != set(reviewed)
        else:
            targets = reviewed
            partial_selection = False

        sessions = self._vault_sessions()
        active_ep = self._active_endpoint()
        self.activity.add(f"开始按确认清单停止 {len(targets)} 个远端实例", "GPU")
        if not force:
            for ep in targets:
                if ep in sessions:
                    self.activity.add(f"正在停止远端任务并同步云盘（{ep}）", "GPU")
                    if ep == active_ep or len(sessions) <= 1:
                        self.remote("POST", "prepare-release")
                    else:
                        self.remote("POST", "prepare-release", endpoint=ep)
                    self.activity.add(f"远端收尾完成（{ep}）", "GPU")
        else:
            self.activity.add("已选择强制释放，跳过远端收尾检查", "GPU", "warning")
        try:
            self.provider.release_all(
                targets, lambda message: self.activity.add(message, "GPU")
            )
        finally:
            self.last_auth = 0
            remaining_sessions = self._vault_sessions()
            with self.lock:
                for ep in targets:
                    if ep not in remaining_sessions:
                        self.instance_states.pop(ep, None)
            with self.monitor_lock:
                for ep in targets:
                    self.external_observations.pop(ep, None)
                if self.monitor_endpoint in targets:
                    self.monitor_mode = "auto"
                    self.monitor_endpoint = ""
                    self.monitor_paused = False
            for ep in targets:
                self._close_external_observer(endpoint=ep, shutdown_kernel=False)
            next_active = self._active_endpoint()
            if not remaining_sessions or not next_active:
                with self.lock:
                    self.remote_workflow_versions = {}
                self.update(
                    runtime={"state": "stopped"},
                    models=self.presets(),
                    workflows=self._merge_workflows(None),
                    comfy_outputs=[],
                )
            else:
                self._activate_cached(next_active)
            self.update(account=self.provider.account())
            self._observer_wake.set()
        self.release_plan = None
        account = self.data["account"]
        if not partial_selection and account["assignments"]:
            raise RuntimeError(
                f"已处理确认的实例；当前账号仍有 {account['assignments']} 个实例，请再次停止全部"
            )
        if partial_selection:
            self.activity.add(
                f"已停止所选的 {len(targets)} 个实例；当前账号剩余 {account.get('assignments', 0)} 个运行实例",
                "计费",
            )
        else:
            self.activity.add(
                "已确认 0 个运行实例"
                + (
                    "，费率为 0 CU/小时"
                    if account["rate"] == 0
                    else "，等待 Google 更新计费费率"
                ),
                "计费",
            )

    def _ensure_remote_model(self, model_id: str):
        with self.lock:
            spec = copy.deepcopy(self.custom_models.get(model_id))
            on_remote = any(
                m.get("id") == model_id for m in (self.data.get("models") or [])
            )
        if spec and not on_remote:
            self.remote("POST", "models", spec)

    def import_model(self, payload):
        repo_input = str(payload.get("repo") or "").strip()
        self.activity.add(f"正在读取 Hugging Face 模型清单：{repo_input}", "模型")
        spec = resolve_import(
            repo_input,
            str(payload.get("filename") or "").strip(),
            str(payload.get("revision") or "main").strip() or "main",
            self.vault.get("hf", ""),
            custom_name=str(payload.get("name") or ""),
        )
        total_gib = sum(f.size for f in spec.files) / 1024**3
        self.activity.add(
            f"已解析模型清单：{spec.name}（{len(spec.files)} 个文件，{total_gib:.2f} GiB）",
            "模型",
        )
        dumped = spec.model_dump()
        with self.lock:
            self.custom_models[spec.id] = dumped
            if payload.get("common", True):
                self.common_ids.add(spec.id)
        self._save_library()
        self.activity.add(f"已保存到本地模型库：{spec.name}", "模型")
        if self._vault_sessions() and self.data["runtime"].get("state") == "ready":
            self.remote("POST", "models", dumped)
            self.refresh_remote()
            self.activity.add(f"已同步模型配置到远端实例：{spec.name}", "模型")
        else:
            self.update(models=self.presets())
        return spec

    def set_common_model(self, model_id: str, common: bool | None = None):
        if not re.fullmatch(r"[a-zA-Z0-9_-]{1,80}", model_id):
            raise ValueError("模型 ID 不合法")
        presets_list = self.presets()
        preset_ids = {m["id"] for m in presets_list}
        with self.lock:
            existing = (
                next(
                    (
                        m
                        for m in (self.data.get("models") or [])
                        if m.get("id") == model_id
                    ),
                    None,
                )
                or self.custom_models.get(model_id)
                or next((m for m in presets_list if m.get("id") == model_id), None)
            )
            files = (existing or {}).get("files") or []
            currently_common = model_id in self.common_ids
            target = (not currently_common) if common is None else bool(common)
            if target:
                self.common_ids.add(model_id)
                if (
                    existing
                    and files
                    and model_id not in self.custom_models
                    and model_id not in preset_ids
                ):
                    try:
                        clean = {
                            k: existing[k]
                            for k in ModelSpec.model_fields
                            if k in existing
                        }
                        self.custom_models[model_id] = ModelSpec.model_validate(
                            clean
                        ).model_dump()
                    except Exception:
                        pass
            else:
                self.common_ids.discard(model_id)
        self._save_library()
        return {"ok": True, "common": target}

    def save_common_repo(self, payload: dict):
        repo, filename, _ = parse_hf_input(
            str(payload.get("repo") or ""),
            str(payload.get("filename") or ""),
        )
        if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
            raise ValueError("请输入 owner/repository 或 Hugging Face 仓库链接")
        name = str(payload.get("name") or "").strip()[:120] or repo.split("/")[-1]
        note = str(payload.get("note") or "").strip()[:200]
        entry = {
            "repo": repo,
            "name": name,
            "note": note,
            "filename": filename[:200],
            "downloads": int(payload.get("downloads") or 0),
            "likes": int(payload.get("likes") or 0),
            "added_at": time.time(),
        }
        with self.lock:
            existing_idx = next(
                (
                    i
                    for i, b in enumerate(self.common_repos)
                    if b["repo"].lower() == repo.lower()
                ),
                None,
            )
            if payload.get("toggle") and existing_idx is not None:
                self.common_repos.pop(existing_idx)
                saved = False
            elif existing_idx is not None:
                entry["added_at"] = self.common_repos[existing_idx].get(
                    "added_at", entry["added_at"]
                )
                self.common_repos[existing_idx] = entry
                saved = True
            else:
                self.common_repos.insert(0, entry)
                saved = True
        self._save_library()
        if saved:
            self.activity.add(f"已将社区仓库加入我的模型：{repo}", "模型")
        else:
            self.activity.add(f"已从我的模型移除仓库：{repo}", "模型")
        return {"ok": True, "common": saved, "repo": repo}

    def remove_common_repo(self, repo: str):
        target = (repo or "").strip().lower()
        with self.lock:
            self.common_repos = [
                b for b in self.common_repos if b["repo"].lower() != target
            ]
        self._save_library()
        if target:
            self.activity.add(f"已从我的模型移除仓库：{repo.strip()}", "模型")
        return {"ok": True}

    def remove_local_model(self, model_id: str):
        mid = self.model_id({"id": model_id})
        with self.lock:
            current = next(
                (m for m in (self.data.get("models") or []) if m.get("id") == mid),
                None,
            )
            if current and (current.get("downloaded") or current.get("loaded")):
                raise RuntimeError("该模型已在远端下载或装载，请先删除远端文件后再移除本地记录")
            self.custom_models.pop(mid, None)
            self.common_ids.discard(mid)
            self.data["models"] = [
                m for m in (self.data.get("models") or []) if m.get("id") != mid
            ]
        self._save_library()
        self.activity.add(f"已从本地列表移除模型：{mid}", "模型")
        return {"ok": True}

    def save_settings(self, payload):
        public = {
            k: payload[k]
            for k in (
                "gpu",
                "idle_minutes",
                "auto_drive",
                "download_threads",
            )
            if k in payload
        }
        if public.get("gpu", self.settings["gpu"]) not in (
            "T4",
            "L4",
            "A100",
            "G4",
            "H100",
        ):
            raise ValueError("GPU 型号不合法")
        raw_idle = public.get("idle_minutes", self.settings["idle_minutes"])
        if isinstance(raw_idle, bool) or not 1 <= int(raw_idle) <= 240:
            raise ValueError("空闲时间必须在 1 至 240 分钟之间")
        if "idle_minutes" in public:
            public["idle_minutes"] = int(public["idle_minutes"])
        if public.get("download_threads", self.settings["download_threads"]) not in (
            16,
            32,
        ):
            raise ValueError("下载线程数请选择 16 或 32")
        if "auto_drive" in public and not isinstance(public["auto_drive"], bool):
            raise ValueError("自动挂载云盘设置不合法")
        if payload.get("hf_token"):
            self.vault.set("hf", payload["hf_token"])
        idle_changed = (
            "idle_minutes" in public
            and public["idle_minutes"] != self.settings.get("idle_minutes")
        )
        self.settings.update(public)
        atomic_json(self.directory / "settings.json", self.settings)
        summary_parts = [
            f"默认 GPU: {self.settings['gpu']}",
            f"空闲释放: {self.settings['idle_minutes']} 分钟",
            f"下载并发: {self.settings['download_threads']} 线程",
        ]
        if payload.get("hf_token"):
            summary_parts.append("已更新 Hugging Face Token")
        self.activity.add("已保存设置（" + " · ".join(summary_parts) + "）", "设置")
        self.last_config = 0
        with self.lock:
            for state in self.instance_states.values():
                state["last_config"] = 0
        sessions = list(self._vault_sessions().keys())
        if idle_changed and sessions and not self.operation_lock.locked():
            def _sync_idle_policy():
                for ep in sessions:
                    if self.operation_lock.locked() or ep not in self._vault_sessions():
                        continue
                    try:
                        self.configure_remote(ep)
                        self.activity.add(
                            f"已向实例 {ep} 同步空闲释放时长（{self.settings['idle_minutes']} 分钟）",
                            "GPU",
                        )
                        is_active = ep == self._active_endpoint()
                        rt_state = (
                            self.data["runtime"].get("state")
                            if is_active
                            else self._ensure_instance_state(ep)["runtime"].get("state")
                        )
                        if is_active and rt_state == "ready":
                            self.refresh_remote()
                        elif rt_state == "ready":
                            self.refresh_remote(ep)
                    except Exception:
                        pass

            threading.Thread(target=_sync_idle_policy, daemon=True).start()
        return {"ok": True}

    def send_activity(self):
        if not self.touch_lock.acquire(blocking=False):
            return
        try:
            if (
                self.operation_lock.locked()
                or not self.pending_touch
                or time.time() - self.last_touch < 5
                or self.data["runtime"].get("protocol", 0) < 2
            ):
                return
            self.pending_touch = False
            self.remote("POST", "activity", {})
            self.last_touch = time.time()
        except Exception:
            # A failed activity signal must not masquerade as a reset countdown.
            self.pending_touch = True
        finally:
            self.touch_lock.release()

    def command(self, action, payload):
        if action == "locale.set":
            raw_locale = str(payload.get("locale") or "").strip()
            persist = bool(payload.get("persist", False))
            valid_locales = ("zh-CN", "en-US", "zh-TW", "zh-HK", "ja-JP", "ko-KR")
            if raw_locale not in valid_locales:
                raise ValueError("不支持的语言选项")
            self.auth.locale = raw_locale
            if persist:
                self.settings["locale"] = raw_locale
                atomic_json(self.directory / "settings.json", self.settings)
            return {"ok": True, "locale": raw_locale, "persisted": persist}
        if action == "account.refresh":
            self.activity.add("正在刷新 Google 账号余额与运行实例状态", "计费")
            account = self.provider.account()
            self.update(account=account)
            self.last_auth = time.time()
            self._observer_wake.set()
            if isinstance(account, dict) and account.get("logged_in"):
                self.activity.add(
                    f"账号状态已刷新 · 余额 {float(account.get('balance') or 0):.2f} CU · 费率 {float(account.get('rate') or 0):.2f} CU/小时 · 活跃 {int(account.get('assignments') or 0)} 台",
                    "计费",
                )
            else:
                self.activity.add("账号状态已刷新（当前未登录）", "计费")
            return {"ok": True, "assignments": account.get("assignments", 0)}
        if action == "runtime.select":
            return self.select_instance(str(payload.get("endpoint") or ""))
        if action == "monitor.select":
            return self.select_monitor(str(payload.get("endpoint") or ""))
        if action == "monitor.pause":
            paused = payload.get("paused") if "paused" in payload else None
            return self.set_monitor_paused(paused)
        if action == "monitor.retry":
            return self.retry_monitor()
        if action == "api.key":
            api = self.vault.get("public_api", {})
            if not api.get("api_key"):
                api["api_key"] = "co_" + secrets.token_urlsafe(36)
                self.vault.set("public_api", api)
            return {"key": api["api_key"]}
        if action == "api.rotate":
            # Revoke the running gateway first. Never report a local-only rotation as successful.
            if self._vault_sessions():
                self.activity.add("正在停止运行中的对外 API 以吊销旧密钥", "API")
                self.remote("POST", "public-api/stop")
                self.refresh_remote()
            api = self.vault.get("public_api", {})
            api["api_key"] = "co_" + secrets.token_urlsafe(36)
            self.vault.set("public_api", api)
            self.activity.add("API Key 已更换；重新开启服务后使用新密钥", "API")
            return {"ok": True}
        if action in ("api.start", "api.save"):
            saved = self.vault.get("public_api", {})
            if not isinstance(saved, dict):
                saved = {}
            if payload.get("mode") not in (None, "", "named"):
                raise ValueError(
                    "已不再支持临时域名，请配置固定域名和 Cloudflare Tunnel Token"
                )
            try:
                config = ColabOnePublicConfig.model_validate(
                    {
                        "mode": "named",
                        "domain": payload["domain"]
                        if "domain" in payload
                        else saved.get("domain", ""),
                        "tunnel_token": payload.get("tunnel_token")
                        or saved.get("tunnel_token", ""),
                        "api_key": saved.get("api_key")
                        or "co_" + secrets.token_urlsafe(36),
                    }
                )
            except ValueError:
                raise ValueError(
                    "API 配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token"
                ) from None
            # Credential storage is acknowledged before enabling the gateway.
            stored = saved | {
                "mode": "named",
                "api_key": config.api_key,
                "domain": config.domain,
                "tunnel_token": config.tunnel_token,
            }
            self.vault.set("public_api", stored)
            if action == "api.save":
                self.activity.add("已保存对外 API 固定域名与隧道配置", "API")
                return {"ok": True}

            def start_api():
                self.activity.add("正在向远端下发对外 API 配置并启动网关", "API")
                self.remote("POST", "public-api/start", config.model_dump())
                self.activity.add("远端已受理对外 API 启动请求，正在刷新状态", "API")
                self.refresh_remote()

            return self.async_operation("开启远端对外 API", start_api)
        if action == "api.stop":

            def stop_api():
                self.activity.add("正在关闭远端对外 API 与 Cloudflare 隧道", "API")
                self.remote("POST", "public-api/stop")
                self.refresh_remote()
                self.activity.add("远端对外 API 已关闭", "API")

            return self.async_operation("关闭对外 API", stop_api)
        if action == "comfyui.start":
            self.activity.add("请求启动远端 ComfyUI 引擎", "ComfyUI")
            res = self.remote("POST", "comfyui/start")
            self.refresh_remote()
            self.activity.add("远端已受理 ComfyUI 启动任务", "ComfyUI")
            return res
        if action == "comfyui.stop":

            def stop_comfy():
                self.activity.add("正在停止远端 ComfyUI 引擎与画布隧道", "ComfyUI")
                self.remote("POST", "comfyui/stop")
                self.refresh_remote()
                self.activity.add("远端 ComfyUI 引擎已停止", "ComfyUI")

            return self.async_operation("停止远端 ComfyUI", stop_comfy)
        if action == "comfyui.free":
            self.activity.add("请求释放 ComfyUI 远端显存", "ComfyUI")
            res = self.remote("POST", "comfyui/free")
            self.refresh_remote()
            self.activity.add("已释放 ComfyUI 远端显存", "ComfyUI")
            return res
        if action in ("comfyui.canvas.save", "comfyui.canvas.start"):
            saved = self.vault.get("comfyui_canvas", {})
            if not isinstance(saved, dict):
                saved = {}
            if payload.get("mode") not in (None, "", "named"):
                raise ValueError(
                    "已不再支持临时域名，请配置固定域名和 Cloudflare Tunnel Token"
                )
            try:
                config = ComfyCanvasConfig.model_validate(
                    {
                        "mode": "named",
                        "domain": payload["domain"]
                        if "domain" in payload
                        else saved.get("domain", ""),
                        "tunnel_token": payload.get("tunnel_token")
                        or saved.get("tunnel_token", ""),
                    }
                )
            except ValueError:
                raise ValueError(
                    "ComfyUI 画布配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token"
                ) from None
            stored = saved | {
                "mode": "named",
                "domain": config.domain,
                "tunnel_token": config.tunnel_token,
            }
            self.vault.set("comfyui_canvas", stored)
            if action == "comfyui.canvas.save":
                self.activity.add("已保存 ComfyUI 原生画布固定域名与隧道配置", "ComfyUI")
                return {"ok": True}

            def start_canvas():
                self.activity.add("正在启动 ComfyUI 原生画布代理与隧道", "ComfyUI")
                self.remote("POST", "comfyui/canvas/start", config.model_dump())
                self.activity.add("远端已受理画布启动请求，等待公网连通检查", "ComfyUI")
                self.refresh_remote()

            return self.async_operation("开启 ComfyUI 原生画布", start_canvas)
        if action == "comfyui.canvas.stop":

            def stop_canvas():
                self.activity.add("正在关闭 ComfyUI 原生画布隧道", "ComfyUI")
                self.remote("POST", "comfyui/canvas/stop")
                self.refresh_remote()
                self.activity.add("ComfyUI 原生画布已关闭", "ComfyUI")

            return self.async_operation("关闭 ComfyUI 原生画布", stop_canvas)
        if action == "comfyui.canvas.url":
            return self.remote("GET", "comfyui/canvas/url")
        if action == "comfyui.workflow.import":
            req = WorkflowImportRequest.model_validate(payload)
            if self.data["runtime"].get("state") == "ready":
                item = self.remote("POST", "comfyui/workflows", req.model_dump())
                try:
                    stored_item = WorkflowTemplate.model_validate(item).model_dump()
                except Exception:
                    stored_item = dict(item)
                if stored_item.get("id"):
                    with self.lock:
                        self.custom_workflows[stored_item["id"]] = stored_item
                        self.remote_workflow_versions[stored_item["id"]] = float(
                            stored_item.get("updated_at") or 0
                        )
                    self._save_workflows()
                self.refresh_remote()
                self.activity.add(
                    f"已导入 ComfyUI 工作流：{stored_item.get('name') or stored_item.get('id', '')}",
                    "ComfyUI",
                )
                return stored_item
            now = time.time()
            wf_id = req.id or f"wf-{secrets.token_hex(6)}"
            item = WorkflowTemplate(
                id=wf_id,
                name=req.name,
                description=req.description,
                source="custom",
                created_at=now,
                updated_at=now,
                workflow=req.workflow,
            ).model_dump()
            with self.lock:
                self.custom_workflows[item["id"]] = item
                self.remote_workflow_versions.pop(item["id"], None)
                merged = self._merge_workflows(self.data.get("workflows"))
            self._save_workflows()
            self.update(workflows=merged)
            self.activity.add(
                f"已离线保存 ComfyUI 工作流：{item['name']}",
                "ComfyUI",
            )
            return item
        if action == "comfyui.workflow.delete":
            wid = str(payload.get("id") or "")
            if not re.fullmatch(r"[a-zA-Z0-9_-]{1,80}", wid):
                raise ValueError("工作流 ID 不合法")
            with self.lock:
                removed_local = self.custom_workflows.pop(wid, None)
                prev_ver = self.remote_workflow_versions.pop(wid, None)
            self._save_workflows()
            if self.data["runtime"].get("state") == "ready":
                try:
                    self.remote("DELETE", "comfyui/workflows/" + wid)
                except Exception as exc:
                    if not (
                        removed_local is not None and self._is_not_found_error(exc)
                    ):
                        if removed_local is not None:
                            with self.lock:
                                self.custom_workflows[wid] = removed_local
                                if prev_ver is not None:
                                    self.remote_workflow_versions[wid] = prev_ver
                            self._save_workflows()
                        raise
                self.refresh_remote()
            with self.lock:
                remaining = [
                    w for w in (self.data.get("workflows") or []) if w.get("id") != wid
                ]
                merged = self._merge_workflows(remaining)
            self.update(workflows=merged)
            self.activity.add(f"已删除 ComfyUI 工作流：{wid}", "ComfyUI")
            return {"ok": True}
        if action == "comfyui.input.upload":
            fname = str(payload.get("filename") or "")
            self.activity.add(f"正在上传 ComfyUI 输入素材：{fname or '文件'}", "ComfyUI")
            res = self.remote(
                "POST",
                "comfyui/inputs",
                {
                    "filename": fname,
                    "data": str(payload.get("data") or ""),
                },
            )
            self.activity.add(f"ComfyUI 输入素材上传完成：{fname or '文件'}", "ComfyUI")
            return res
        if action == "comfyui.run":
            req = WorkflowRunRequest.model_validate(payload)
            self._ensure_remote_workflow(req.workflow_id)
            self.activity.add(f"请求运行 ComfyUI 工作流：{req.workflow_id}", "ComfyUI")
            res = self.remote("POST", "comfyui/run", req.model_dump())
            self.refresh_remote()
            self.activity.add("远端已受理 ComfyUI 工作流任务", "ComfyUI")
            return res
        if action in ("comfyui.output.asset", "comfyui.output.delete"):
            identity = str(payload.get("id") or "")
            if not re.fullmatch(r"[a-f0-9]{32}", identity):
                raise ValueError("产物编号不合法")
            if action == "comfyui.output.delete":
                self.activity.add("正在删除远端 ComfyUI 作品文件", "ComfyUI")
                res = self.remote("DELETE", "comfyui/outputs/" + identity)
                self.refresh_remote()
                self.activity.add("已删除远端 ComfyUI 作品文件", "ComfyUI")
                return res
            raw_offset = payload.get("offset", 0)
            if isinstance(raw_offset, bool):
                raise ValueError("读取范围不合法")
            offset = int(raw_offset)
            if not 0 <= offset <= 512 * 1024**2:
                raise ValueError("读取范围不合法")
            return self.remote(
                "GET", f"comfyui/outputs/{identity}/asset?offset={offset}"
            )
        if action == "runtime.upgrade":
            return self.async_operation("更新远端管理服务", self.upgrade_service)
        if action == "runtime.activity":
            # Queue rather than blocking the UI behind a kernel RPC.
            if self.data["runtime"].get("state") == "ready":
                self.pending_touch = True
                threading.Thread(target=self.send_activity, daemon=True).start()
            return {"ok": True}

        if action == "hub.search":
            return search_models(
                payload.get("query", ""),
                payload.get("sort", "downloads"),
                payload.get("cursor", ""),
                self.vault.get("hf", ""),
                modality=payload.get("modality", "all"),
                family=payload.get("family", ""),
            )
        if action == "hub.files":
            repo, _, _ = parse_hf_input(str(payload.get("repo") or ""))
            return model_files(repo, self.vault.get("hf", ""))
        if action == "hub.install":
            want_download = bool(payload.get("download", True))
            want_load = bool(payload.get("load", False))

            def install():
                spec = self.import_model(payload)
                if want_download:
                    self._ensure_remote_model(spec.id)
                    threads = self.settings["download_threads"]
                    dl_body: dict = {
                        "hf_token": self.vault.get("hf", ""),
                        "threads": threads,
                    }
                    if want_load:
                        dl_body["load"] = True
                        for k in ("context_length", "temperature"):
                            if k in payload:
                                dl_body[k] = payload[k]
                    self.activity.add(
                        f"正在提交远端下载任务：{spec.name}（{threads} 线程"
                        + ("，完成后自动装载" if want_load else "")
                        + "）",
                        "模型",
                    )
                    self.remote("POST", "models/" + spec.id + "/download", dl_body)
                    self.refresh_remote()
                    self.activity.add(f"远端已启动模型下载：{spec.name}", "模型")

            op_title = (
                "添加模型并下载装载到远端显存"
                if (want_download and want_load)
                else "添加社区模型并下载到远端"
                if want_download
                else "加入我的模型"
            )
            return self.async_operation(op_title, install)
        if action == "model.common":
            return self.set_common_model(
                str(payload.get("id") or ""),
                payload.get("common") if "common" in payload else None,
            )
        if action == "model.remove_local":
            return self.remove_local_model(str(payload.get("id") or ""))
        if action == "common_repo.save":
            return self.save_common_repo(payload)
        if action == "common_repo.remove":
            return self.remove_common_repo(str(payload.get("repo") or ""))
        if action == "desktop.shortcuts":
            target = str(payload.get("target") or "start_menu")
            remove = bool(payload.get("remove", False))
            status = manage_shortcuts(
                self.root,
                target=target,
                remove=remove,
                sandbox_dir=self.shortcut_sandbox,
                locale=self.auth.locale,
            )
            label = (
                "开始菜单与桌面"
                if target == "both"
                else ("桌面" if target == "desktop" else "开始菜单")
            )
            self.activity.add(
                f"已移除{label}快捷方式" if remove else f"已更新{label}快捷方式"
            )
            return {"ok": True, **status}

        if action == "logs.clear":
            self.activity.clear()
            return {"ok": True}
        if action == "runtime.release.preview":
            return self.preview_release_all()
        if action == "runtime.release.all":
            endpoints = payload.get("endpoints")
            title = (
                "停止所选远端实例"
                if endpoints is not None
                else "停止账号下全部已确认实例"
            )
            return self.async_operation(
                title,
                lambda: self.release_all(
                    payload.get("plan_id"),
                    bool(payload.get("force")),
                    endpoints,
                ),
            )
        if action == "settings.save":
            return self.save_settings(payload)
        if action == "auth.colab.begin":
            result = self.auth.colab_begin()
            self.update(auth_url=result["url"])
            self.activity.add("已生成 Google 授权链接，等待完成登录", "登录")
            return result
        if action == "auth.colab.auto":

            def login():
                self.activity.add("已启动本地回调服务，等待浏览器完成 Google 授权", "登录")
                self.auth.colab_login(lambda url: self.update(auth_url=url))
                self.last_auth = 0
                self._reset_monitor_to_auto(close_observer=True, clear_observations=True)
                with self.lock:
                    self.remote_workflow_versions = {}
                self.update(
                    models=self.presets(),
                    workflows=self._merge_workflows(None),
                    comfy_outputs=[],
                    auth_url="",
                )
                self.activity.add("Google 登录已保存到本机，后续自动续期", "登录")

            return self.async_operation("等待 Google 登录", login)
        if action == "auth.colab.finish":

            def finish():
                self.activity.add("正在校验 Google 授权码并换取访问凭据", "登录")
                self.auth.colab_finish(payload["code"])
                self.last_auth = 0
                self._reset_monitor_to_auto(close_observer=True, clear_observations=True)
                with self.lock:
                    self.remote_workflow_versions = {}
                self.update(
                    models=self.presets(),
                    workflows=self._merge_workflows(None),
                    comfy_outputs=[],
                    auth_url="",
                )
                self.activity.add("Google 登录已保存到本机，后续自动续期", "登录")

            if self.operation_lock.locked():
                # A paste can finish a pending loopback login; its operation then
                # completes the login once the callback wait exits.
                self.activity.add("收到手动粘贴的 Google 授权内容，正在校验", "登录")
                self.auth.colab_finish(payload["code"])
                self.last_auth = 0
                self._reset_monitor_to_auto(close_observer=True, clear_observations=True)
                return {"ok": True}
            return self.async_operation("完成 Google 登录", finish)
        if action == "drive.connect":
            return self.async_operation("挂载 Google 云盘 (ColabOne_Drive)", self.connect_drive)
        if action == "drive.setup_headless":
            return self.async_operation(
                "检测并开启云盘免弹窗模式", self.setup_headless_drive
            )
        if action == "drive.cancel":
            self.drive_cancel.set()
            self.update(auth_url="")
            self.activity.add("已请求取消 Google 云盘挂载", "云盘")
            return {"ok": True}
        if action in ("drive.sync", "sync"):
            return self.async_operation("同步 Google 云盘 (ColabOne_Drive)", self.sync_drive)
        if action == "runtime.start":
            create_new = bool(payload.get("new"))
            gpu = payload.get("gpu")
            return self.async_operation(
                "新增启动远端 GPU" if create_new else "启动远端 GPU",
                lambda: self.start(create_new=create_new, gpu=gpu),
            )
        if action == "runtime.release":
            target_ep = payload.get("endpoint")
            return self.async_operation(
                f"释放远端实例 {target_ep}" if target_ep else "释放远端 GPU",
                lambda: self.release(bool(payload.get("force")), target_ep),
            )
        if action == "model.import":
            return self.async_operation(
                "读取 Hugging Face 模型清单", lambda: self.import_model(payload)
            )
        if action == "model.download":
            mid = self.model_id(payload)
            label = self._model_label(mid)
            threads = self.settings["download_threads"]
            self.activity.add(
                f"请求下载模型 {label} 到远端 GPU（{threads} 线程"
                + ("，完成后自动装载" if payload.get("load") else "")
                + "）",
                "模型",
            )
            self._ensure_remote_model(mid)
            dl_body: dict = {
                "hf_token": self.vault.get("hf", ""),
                "threads": threads,
            }
            if payload.get("load"):
                dl_body["load"] = True
                for key in ("context_length", "temperature"):
                    if key in payload:
                        dl_body[key] = payload[key]
            res = self.remote("POST", "models/" + mid + "/download", dl_body)
            self.activity.add(f"远端下载任务已启动：{label}", "模型")
            return res
        if action == "model.load":
            mid = self.model_id(payload)
            label = self._model_label(mid)
            self.activity.add(f"请求装载模型 {label} 到远端显存", "模型")
            self._ensure_remote_model(mid)
            options = {
                key: payload[key]
                for key in ("context_length", "temperature")
                if key in payload
            }
            res = self.remote("POST", "models/" + mid + "/load", options)
            self.activity.add(f"远端模型装载任务已启动：{label}", "模型")
            return res
        if action == "model.unload":
            self.activity.add("请求卸载当前远端模型并释放显存", "模型")
            res = self.remote("POST", "unload")
            self.activity.add("已卸载远端模型并释放显存", "模型")
            return res
        if action == "model.delete":
            mid = self.model_id(payload)
            label = self._model_label(mid)
            self.activity.add(f"请求删除远端模型文件：{label}", "模型")
            res = self.remote("DELETE", "models/" + mid)
            self.activity.add(f"已删除远端模型文件：{label}", "模型")
            return res
        if action == "task.cancel":
            if not re.fullmatch(r"[a-f0-9]{32}", payload["id"]):
                raise ValueError("任务 ID 不合法")
            self.activity.add("请求取消当前远端任务", "远端")
            res = self.remote("DELETE", "tasks/" + payload["id"])
            self.activity.add("已向远端发送任务取消指令", "远端")
            return res
        if action == "error.clear":
            self.update(error="")
            return {"ok": True}
        raise ValueError("不支持的操作")

    @staticmethod
    def model_id(payload):
        value = payload["id"]
        if not re.fullmatch(r"[a-zA-Z0-9_-]{1,80}", value):
            raise ValueError("模型 ID 不合法")
        return value

    def monitor(self):
        while not self.stop.is_set():
            try:
                now = time.time()
                if now - self.last_auth > 30:
                    self.update(account=self.provider.account())
                    self.last_auth = now
                    self._observer_wake.set()
                remotes = self._vault_remotes()
                active_ep = self._active_endpoint()
                remote = remotes.get(active_ep or "") or self.vault.get("remote")
                if remote and not self.operation_lock.locked():
                    # Renew the proxy before any RPC. A failed configuration
                    # request must not starve the very heartbeat that repairs it.
                    if now - self.last_heartbeat > 30:
                        self.provider.heartbeat()
                        self.last_heartbeat = now
                        if not self._vault_sessions():
                            with self.lock:
                                self.remote_workflow_versions = {}
                            self.update(
                                runtime={"state": "stopped"},
                                models=self.presets(),
                                workflows=self._merge_workflows(None),
                                comfy_outputs=[],
                            )
                            continue
                    if now - self.last_config > 600:
                        self.configure_remote()
                    try:
                        self.send_activity()
                        self.refresh_remote()
                        runtime = self.data["runtime"]
                        if (
                            runtime.get("protocol", 0) < 7
                            and remote["endpoint"] not in self.upgrade_attempted
                            and not runtime.get("loaded_model")
                            and not any(
                                t["state"] == "running"
                                for t in runtime.get("tasks", [])
                            )
                        ):
                            self.upgrade_attempted.add(remote["endpoint"])
                            self.async_operation(
                                "更新远端管理服务", self.upgrade_service
                            )
                    except Exception:
                        if (
                            remote["endpoint"] == self._active_endpoint()
                            and remote["endpoint"] in self._vault_sessions()
                        ):
                            self.update(
                                runtime={
                                    "state": "disconnected",
                                    "id": remote["endpoint"],
                                }
                            )
                # Keep any secondary managed instances alive and refreshed in parallel.
                if not self.operation_lock.locked():
                    for ep in list(remotes.keys()):
                        if ep == active_ep or ep not in self._vault_sessions():
                            continue
                        entry = self._ensure_instance_state(ep)
                        try:
                            if now - entry.get("last_heartbeat", 0) > 30:
                                self.provider.heartbeat(ep)
                                entry["last_heartbeat"] = now
                                if ep not in self._vault_sessions():
                                    self.instance_states.pop(ep, None)
                                    continue
                            if now - entry.get("last_config", 0) > 600:
                                self.configure_remote(ep)
                            self.refresh_remote(ep)
                        except Exception:
                            entry["runtime"] = {
                                "state": "disconnected",
                                "id": ep,
                            }
                if self.data.get("error", "").startswith("连接检查失败："):
                    self.update(error="")
            except Exception as exc:
                self.update(
                    error="连接检查失败："
                    + type(exc).__name__
                    + "。客户端会自动重试，请检查网络连接。"
                )
            self.stop.wait(2)
