"""Thread-safe, zero-dependency Linux system telemetry collector for the remote Colab VM."""

from __future__ import annotations

import collections
import copy
import math
import os
import re
import shutil
import subprocess
import threading
import time
from pathlib import Path

_DISK_DEVICE_RE = re.compile(
    r"^(nvme\d+n\d+|sd[a-z]+|vd[a-z]+|xvd[a-z]+|mmcblk\d+)$"
)

_GPU_UNAVAILABLE: dict = {
    "name": "GPU 信息不可用",
    "total_mib": 0,
    "used_mib": 0,
    "util_percent": None,
    "temp_c": None,
    "power_w": None,
    "power_limit_w": None,
}


class ColabOneResourceSampler:
    """Collects CPU, RAM, disk, and GPU telemetry with bounded rolling history."""

    def __init__(
        self, history_limit: int = 60, min_sample_interval: float = 0.9
    ) -> None:
        self._lock = threading.Lock()
        self._history: collections.deque[dict] = collections.deque(
            maxlen=history_limit
        )
        self._min_interval: float = float(min_sample_interval)
        self._prev_cpu: tuple[int, int] | None = None
        self._prev_disk: tuple[float, int, int] | None = None
        self._last_history_mono: float | None = None
        self._last_snapshot: dict | None = None
        self._last_snapshot_mono: float | None = None

    @staticmethod
    def _parse_optional_float(raw: str | None) -> float | None:
        if raw is None:
            return None
        cleaned = raw.strip()
        if (
            not cleaned
            or cleaned.startswith("[")
            or cleaned.lower() in {"n/a", "not supported", "none", "-"}
        ):
            return None
        try:
            value = float(cleaned)
        except ValueError:
            return None
        if not math.isfinite(value):
            return None
        return value

    @staticmethod
    def _parse_required_int(raw: str) -> int:
        cleaned = raw.strip()
        if not cleaned or cleaned.startswith("["):
            raise ValueError(f"Invalid integer field: {raw!r}")
        value = float(cleaned)
        if not math.isfinite(value) or value < 0:
            raise ValueError(f"Invalid non-negative field: {raw!r}")
        return int(value)

    @staticmethod
    def parse_nvidia_smi_line(line: str) -> dict:
        first = next(
            (ln.strip() for ln in (line or "").splitlines() if ln.strip()), ""
        )
        if not first:
            raise ValueError("Empty nvidia-smi output")
        parts = [part.strip() for part in first.split(",")]
        if len(parts) < 3:
            raise ValueError(
                f"Expected at least 3 CSV columns from nvidia-smi, got {len(parts)}"
            )
        if len(parts) > 7:
            parts = [", ".join(parts[:-6]).strip(), *parts[-6:]]

        name = parts[0]
        if not name:
            raise ValueError("Missing GPU name in nvidia-smi output")
        total_mib = ColabOneResourceSampler._parse_required_int(parts[1])
        used_mib = ColabOneResourceSampler._parse_required_int(parts[2])
        util_percent = (
            ColabOneResourceSampler._parse_optional_float(parts[3])
            if len(parts) > 3
            else None
        )
        temp_c = (
            ColabOneResourceSampler._parse_optional_float(parts[4])
            if len(parts) > 4
            else None
        )
        power_w = (
            ColabOneResourceSampler._parse_optional_float(parts[5])
            if len(parts) > 5
            else None
        )
        power_limit_w = (
            ColabOneResourceSampler._parse_optional_float(parts[6])
            if len(parts) > 6
            else None
        )
        return {
            "name": name,
            "total_mib": total_mib,
            "used_mib": used_mib,
            "util_percent": util_percent,
            "temp_c": temp_c,
            "power_w": power_w,
            "power_limit_w": power_limit_w,
        }

    @classmethod
    def gpu_info(cls) -> dict:
        queries = (
            "name,memory.total,memory.used,utilization.gpu,temperature.gpu,power.draw,power.limit",
            "name,memory.total,memory.used",
        )
        for fields in queries:
            try:
                result = subprocess.run(
                    [
                        "nvidia-smi",
                        f"--query-gpu={fields}",
                        "--format=csv,noheader,nounits",
                    ],
                    capture_output=True,
                    text=True,
                    timeout=8,
                    check=True,
                )
                lines = [
                    ln.strip()
                    for ln in (result.stdout or "").splitlines()
                    if ln.strip()
                ]
                if not lines:
                    raise ValueError("Empty nvidia-smi output")
                return cls.parse_nvidia_smi_line(lines[0])
            except (subprocess.TimeoutExpired, FileNotFoundError):
                break
            except (OSError, ValueError, IndexError, subprocess.SubprocessError):
                continue
        return dict(_GPU_UNAVAILABLE)

    @staticmethod
    def memory_info(meminfo_path: Path = Path("/proc/meminfo")) -> dict:
        try:
            raw = Path(meminfo_path).read_text(encoding="utf-8", errors="replace")
        except OSError:
            return {
                "total_bytes": 0,
                "used_bytes": 0,
                "available_bytes": 0,
                "percent": 0.0,
            }

        fields: dict[str, int] = {}
        for line in raw.splitlines():
            if ":" not in line:
                continue
            key, rest = line.split(":", 1)
            key = key.strip()
            if key not in {
                "MemTotal",
                "MemAvailable",
                "MemFree",
                "Buffers",
                "Cached",
            }:
                continue
            tokens = rest.strip().split()
            if not tokens:
                continue
            try:
                fields[key] = max(0, int(tokens[0]))
            except ValueError:
                continue

        total_bytes = fields.get("MemTotal", 0) * 1024
        if "MemAvailable" in fields:
            available_bytes = fields["MemAvailable"] * 1024
        else:
            available_bytes = (
                fields.get("MemFree", 0)
                + fields.get("Buffers", 0)
                + fields.get("Cached", 0)
            ) * 1024
        used_bytes = max(0, total_bytes - available_bytes)
        percent = (
            round((used_bytes / total_bytes) * 100.0, 1)
            if total_bytes > 0
            else 0.0
        )
        return {
            "total_bytes": int(total_bytes),
            "used_bytes": int(used_bytes),
            "available_bytes": int(available_bytes),
            "percent": float(percent),
        }

    def cpu_info(self, stat_path: Path = Path("/proc/stat")) -> dict:
        cores = int(os.cpu_count() or 1)
        with self._lock:
            try:
                raw = Path(stat_path).read_text(encoding="utf-8", errors="replace")
            except OSError:
                return {"cores": cores, "percent": 0.0}

            parsed: tuple[int, int] | None = None
            for line in raw.splitlines():
                parts = line.split()
                if not parts or parts[0] != "cpu" or len(parts) < 5:
                    continue
                try:
                    values = [int(tok) for tok in parts[1:]]
                except ValueError:
                    continue
                total_jiffies = sum(values[:8])
                idle_jiffies = values[3] + (values[4] if len(values) > 4 else 0)
                parsed = (total_jiffies, idle_jiffies)
                break

            if parsed is None:
                return {"cores": cores, "percent": 0.0}

            total_jiffies, idle_jiffies = parsed
            prev = self._prev_cpu
            self._prev_cpu = (total_jiffies, idle_jiffies)

        if prev is None:
            return {"cores": cores, "percent": 0.0}

        delta_total = total_jiffies - prev[0]
        delta_idle = idle_jiffies - prev[1]
        if delta_total <= 0:
            percent = 0.0
        else:
            busy = max(0, delta_total - max(0, delta_idle))
            percent = min(100.0, max(0.0, (busy / delta_total) * 100.0))
        return {"cores": cores, "percent": float(round(percent, 1))}

    @staticmethod
    def _read_diskstats(diskstats_path: Path) -> tuple[int, int] | None:
        try:
            raw = Path(diskstats_path).read_text(encoding="utf-8", errors="replace")
        except OSError:
            return None

        read_sectors = 0
        write_sectors = 0
        for line in raw.splitlines():
            parts = line.split()
            if len(parts) < 10:
                continue
            if not _DISK_DEVICE_RE.match(parts[2]):
                continue
            try:
                read_sectors += max(0, int(parts[5]))
                write_sectors += max(0, int(parts[9]))
            except ValueError:
                continue
        return (read_sectors * 512, write_sectors * 512)

    def disk_info(
        self,
        root_path: Path,
        diskstats_path: Path = Path("/proc/diskstats"),
        now: float | None = None,
    ) -> dict:
        try:
            usage = shutil.disk_usage(Path(root_path))
            total = int(usage.total)
            used = int(usage.used)
            free = int(usage.free)
        except OSError:
            total = 0
            used = 0
            free = 0
        percent = round((used / total) * 100.0, 1) if total > 0 else 0.0

        read_bps = 0.0
        write_bps = 0.0
        with self._lock:
            mono_ts = time.monotonic() if now is None else float(now)
            disk_counters = self._read_diskstats(Path(diskstats_path))
            if disk_counters is not None:
                read_bytes, write_bytes = disk_counters
                prev = self._prev_disk
                self._prev_disk = (mono_ts, read_bytes, write_bytes)
                if prev is not None:
                    dt = mono_ts - prev[0]
                    if dt > 0:
                        read_bps = max(0, read_bytes - prev[1]) / dt
                        write_bps = max(0, write_bytes - prev[2]) / dt

        return {
            "total_bytes": total,
            "used_bytes": used,
            "free_bytes": free,
            "percent": float(percent),
            "read_bps": float(round(read_bps, 1)),
            "write_bps": float(round(write_bps, 1)),
        }

    def sample(
        self,
        root_path: Path,
        gpu_override: dict | None = None,
        now: float | None = None,
    ) -> dict:
        gpu = dict(gpu_override) if gpu_override is not None else self.gpu_info()
        mem = self.memory_info()
        cpu = self.cpu_info()
        disk = self.disk_info(root_path, now=now)

        total_mib = gpu.get("total_mib") or 0
        used_mib = max(0, gpu.get("used_mib") or 0)
        vram_pct = (
            float(round((used_mib / total_mib) * 100.0, 1))
            if total_mib > 0
            else 0.0
        )

        wall_ts = time.time() if now is None else float(now)
        mono_ts = time.monotonic() if now is None else float(now)

        with self._lock:
            if (
                not self._history
                or self._last_history_mono is None
                or (mono_ts - self._last_history_mono) >= (self._min_interval - 1e-9)
            ):
                self._history.append(
                    {
                        "t": float(round(wall_ts, 1)),
                        "vram_pct": vram_pct,
                        "gpu_util": float(gpu.get("util_percent") or 0.0),
                        "ram_pct": float(mem["percent"]),
                        "cpu_pct": float(cpu["percent"]),
                        "disk_pct": float(disk["percent"]),
                        "disk_read_bps": float(disk["read_bps"]),
                        "disk_write_bps": float(disk["write_bps"]),
                    }
                )
                self._last_history_mono = mono_ts
            history = [dict(item) for item in self._history]
            snapshot = {
                "cpu": cpu,
                "memory": mem,
                "disk": disk,
                "gpu": gpu,
                "history": history,
            }
            self._last_snapshot = copy.deepcopy(snapshot)
            self._last_snapshot_mono = mono_ts

        return snapshot

    def latest(self, max_age: float = 2.5, now: float | None = None) -> dict | None:
        mono_ts = time.monotonic() if now is None else float(now)
        with self._lock:
            if self._last_snapshot is None or self._last_snapshot_mono is None:
                return None
            age = mono_ts - self._last_snapshot_mono
            if age < 0.0 or age > max_age:
                return None
            return copy.deepcopy(self._last_snapshot)
