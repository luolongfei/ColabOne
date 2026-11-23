import subprocess
from unittest.mock import Mock
import pytest
from server.telemetry import ColabOneResourceSampler


def test_parse_nvidia_smi_line_full_optional_na_and_legacy(monkeypatch):
    full = ColabOneResourceSampler.parse_nvidia_smi_line(
        "NVIDIA A100-SXM4-40GB, 40960, 12450, 76, 58, 185.4, 400.0"
    )
    assert full == {
        "name": "NVIDIA A100-SXM4-40GB",
        "total_mib": 40960,
        "used_mib": 12450,
        "util_percent": pytest.approx(76.0),
        "temp_c": pytest.approx(58.0),
        "power_w": pytest.approx(185.4),
        "power_limit_w": pytest.approx(400.0),
    }

    na = ColabOneResourceSampler.parse_nvidia_smi_line(
        "Tesla T4, 15360, 2048, [N/A], [Not Supported], N/A, [N/A]"
    )
    assert na == {
        "name": "Tesla T4",
        "total_mib": 15360,
        "used_mib": 2048,
        "util_percent": None,
        "temp_c": None,
        "power_w": None,
        "power_limit_w": None,
    }

    legacy = ColabOneResourceSampler.parse_nvidia_smi_line("Tesla T4, 15360, 512")
    assert legacy == {
        "name": "Tesla T4",
        "total_mib": 15360,
        "used_mib": 512,
        "util_percent": None,
        "temp_c": None,
        "power_w": None,
        "power_limit_w": None,
    }

    with pytest.raises(ValueError):
        ColabOneResourceSampler.parse_nvidia_smi_line("")

    # Verify gpu_info() falls back cleanly when nvidia-smi is unavailable.
    monkeypatch.setattr(
        "server.telemetry.subprocess.run",
        Mock(side_effect=OSError("nvidia-smi not found")),
    )
    unavailable = ColabOneResourceSampler.gpu_info()
    assert unavailable["total_mib"] == 0
    assert unavailable["used_mib"] == 0
    assert unavailable["util_percent"] is None

    # Verify gpu_info() falls back from 7-column query to 3-column query on older drivers.
    calls = []

    def fake_run(cmd, **kwargs):
        calls.append(cmd)
        if len(calls) == 1:
            raise subprocess.CalledProcessError(2, cmd)
        return Mock(stdout="Tesla T4, 15360, 1024\n")

    monkeypatch.setattr("server.telemetry.subprocess.run", fake_run)
    fallback_gpu = ColabOneResourceSampler.gpu_info()
    assert len(calls) == 2
    assert fallback_gpu["name"] == "Tesla T4"
    assert fallback_gpu["total_mib"] == 15360
    assert fallback_gpu["used_mib"] == 1024
    assert fallback_gpu["util_percent"] is None


def test_memory_info_memavailable_fallback_and_missing(tmp_path):
    tmp_meminfo = tmp_path / "meminfo"
    tmp_meminfo.write_text(
        "MemTotal:       16777216 kB\n"
        "MemFree:         2097152 kB\n"
        "MemAvailable:   12582912 kB\n"
        "Buffers:          524288 kB\n"
        "Cached:          8388608 kB\n",
        encoding="utf-8",
    )
    info = ColabOneResourceSampler.memory_info(tmp_meminfo)
    assert info["total_bytes"] == 16777216 * 1024
    assert info["available_bytes"] == 12582912 * 1024
    assert info["used_bytes"] == (16777216 - 12582912) * 1024
    assert info["percent"] == pytest.approx(25.0)

    tmp_fallback = tmp_path / "meminfo_fallback"
    tmp_fallback.write_text(
        "MemTotal:        8388608 kB\n"
        "MemFree:         1048576 kB\n"
        "Buffers:          524288 kB\n"
        "Cached:          2621440 kB\n",
        encoding="utf-8",
    )
    fallback = ColabOneResourceSampler.memory_info(tmp_fallback)
    expected_avail = (1048576 + 524288 + 2621440) * 1024
    assert fallback["total_bytes"] == 8388608 * 1024
    assert fallback["available_bytes"] == expected_avail
    assert fallback["used_bytes"] == 8388608 * 1024 - expected_avail
    assert fallback["percent"] == pytest.approx(50.0)

    missing = ColabOneResourceSampler.memory_info(tmp_path / "nonexistent_meminfo")
    assert missing == {
        "total_bytes": 0,
        "used_bytes": 0,
        "available_bytes": 0,
        "percent": 0.0,
    }


def test_cpu_info_consecutive_stat_snapshots(tmp_path):
    sampler = ColabOneResourceSampler()
    tmp_stat = tmp_path / "stat"
    tmp_stat.write_text(
        "cpu  1000 0 500 8500 0 0 0 0 0 0\n"
        "cpu0 500 0 250 4250 0 0 0 0 0 0\n"
        "cpu1 500 0 250 4250 0 0 0 0 0 0\n",
        encoding="utf-8",
    )
    first = sampler.cpu_info(tmp_stat)
    assert first["cores"] >= 1
    assert first["percent"] == 0.0

    # Delta: +400 user, +100 system, +500 idle -> 500 busy out of 1000 total = 50.0%
    tmp_stat.write_text(
        "cpu  1400 0 600 9000 0 0 0 0 0 0\n"
        "cpu0 700 0 300 4500 0 0 0 0 0 0\n"
        "cpu1 700 0 300 4500 0 0 0 0 0 0\n",
        encoding="utf-8",
    )
    second = sampler.cpu_info(tmp_stat)
    assert second["cores"] >= 1
    assert second["percent"] == pytest.approx(50.0)

    missing = sampler.cpu_info(tmp_path / "missing_stat")
    assert missing["percent"] == 0.0


def test_disk_info_throughput_without_double_counting_partitions(tmp_path):
    sampler = ColabOneResourceSampler()
    tmp_diskstats = tmp_path / "diskstats"
    tmp_diskstats.write_text(
        "   8       0 sda 100 0 2000 50 80 0 1000 40 0 30 90\n"
        "   8       1 sda1 100 0 2000 50 80 0 1000 40 0 30 90\n"
        "   7       0 loop0 10 0 500 5 0 0 0 0 0 5 5\n",
        encoding="utf-8",
    )
    first = sampler.disk_info(tmp_path, diskstats_path=tmp_diskstats, now=100.0)
    assert first["total_bytes"] > 0
    assert first["read_bps"] == 0.0
    assert first["write_bps"] == 0.0

    # 2.0s later: +400 read sectors (204800 B -> 102400 B/s), +200 write sectors (102400 B -> 51200 B/s).
    # Both sda and sda1 increment identically; partition sda1 must NOT be double-counted.
    tmp_diskstats.write_text(
        "   8       0 sda 120 0 2400 60 95 0 1200 50 0 35 110\n"
        "   8       1 sda1 120 0 2400 60 95 0 1200 50 0 35 110\n"
        "   7       0 loop0 10 0 500 5 0 0 0 0 0 5 5\n",
        encoding="utf-8",
    )
    second = sampler.disk_info(tmp_path, diskstats_path=tmp_diskstats, now=102.0)
    assert second["read_bps"] == pytest.approx(400 * 512 / 2.0)
    assert second["write_bps"] == pytest.approx(200 * 512 / 2.0)


def test_sampler_sample_history_ring_buffer_and_throttling(tmp_path):
    sampler = ColabOneResourceSampler(history_limit=60, min_sample_interval=2.0)
    gpu = {
        "name": "Tesla T4",
        "total_mib": 15360,
        "used_mib": 3840,
        "util_percent": 42.0,
        "temp_c": 55.0,
        "power_w": 50.0,
        "power_limit_w": 70.0,
    }

    s1 = sampler.sample(tmp_path, gpu_override=gpu, now=1000.0)
    assert len(s1["history"]) == 1
    assert s1["history"][0]["vram_pct"] == pytest.approx(25.0)
    assert s1["history"][0]["gpu_util"] == pytest.approx(42.0)

    # Within min_sample_interval (1000.5 - 1000.0 < 2.0): throttled, no extra history point.
    s2 = sampler.sample(tmp_path, gpu_override=gpu, now=1000.5)
    assert len(s2["history"]) == 1

    # Feed 70 additional unthrottled samples (total 71 > 60) and verify ring buffer cap at 60.
    last = None
    for i in range(1, 71):
        last = sampler.sample(tmp_path, gpu_override=gpu, now=1000.0 + i * 3.0)
    assert last is not None
    assert len(last["history"]) == 60
    assert last["history"][0]["t"] == pytest.approx(1000.0 + 11 * 3.0)
    assert last["history"][-1]["t"] == pytest.approx(1000.0 + 70 * 3.0)


def test_runtime_status_exposes_resources_without_resetting_activity(
    runtime, monkeypatch
):
    runtime.last_active = 100
    monkeypatch.setattr("server.runtime.time.time", lambda: 300)
    fake_gpu = {
        "name": "NVIDIA L4",
        "total_mib": 24576,
        "used_mib": 6144,
        "util_percent": 30.0,
        "temp_c": 50.0,
        "power_w": 45.0,
        "power_limit_w": 72.0,
    }
    monkeypatch.setattr(ColabOneResourceSampler, "gpu_info", classmethod(lambda cls: dict(fake_gpu)))
    monkeypatch.setattr(runtime, "gpu_info", lambda: dict(fake_gpu))
    status = runtime.status()
    assert runtime.last_active == 100
    assert "resources" in status
    resources = status["resources"]
    for key in ("cpu", "memory", "disk", "gpu", "history"):
        assert key in resources
    assert resources["gpu"]["name"] == "NVIDIA L4"
    assert isinstance(resources["history"], list)
    assert len(resources["history"]) >= 1
    assert status["protocol"] == 7


def test_runtime_start_background_primes_telemetry_immediately(runtime, monkeypatch):
    import threading

    fake_gpu = {
        "name": "Tesla T4",
        "total_mib": 15360,
        "used_mib": 256,
        "util_percent": 12.0,
        "temp_c": 44.0,
        "power_w": 20.0,
        "power_limit_w": 70.0,
    }
    calls = []

    def fake_gpu_info():
        calls.append(1)
        return dict(fake_gpu)

    monkeypatch.setattr(runtime, "gpu_info", fake_gpu_info)
    runtime.stopping = threading.Event()
    try:
        runtime.start_background()
        # Immediate baseline sample is recorded synchronously before wait(1.0)
        assert len(runtime.telemetry._history) >= 1
        assert len(calls) == 1
        # status() reuses fresh background snapshot without calling gpu_info() under lock
        status = runtime.status()
        assert status["gpu"]["name"] == "Tesla T4"
        assert status["resources"]["gpu"]["name"] == "Tesla T4"
        assert len(calls) == 1
    finally:
        runtime.stopping.set()


def test_resource_sampler_latest_cache_expiry_and_isolation(tmp_path):
    sampler = ColabOneResourceSampler()
    assert sampler.latest() is None
    snap = sampler.sample(
        tmp_path,
        gpu_override={"name": "Tesla T4", "total_mib": 15360, "used_mib": 100},
        now=1000.0,
    )
    snap["gpu"]["name"] = "MUTATED"
    fresh = sampler.latest(max_age=2.5, now=1001.0)
    assert fresh is not None
    assert fresh["gpu"]["name"] == "Tesla T4"
    assert sampler.latest(max_age=2.5, now=1003.0) is None
