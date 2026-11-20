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


def test_boot_telemetry_streams_during_deploy_and_persists_through_drive_mount(
    tmp_path, monkeypatch
):
    import json
    from pathlib import Path
    from types import SimpleNamespace
    from bridge.auth import ColabOneAuth
    from bridge.controller import ColabOneController
    from bridge.provider import ColabOneProvider
    from colab_cli.state import SessionState
    from conftest import MemoryVault

    vault = MemoryVault()
    vault.set(
        "colab",
        {
            "token": "tok",
            "refresh_token": "rtok",
            "client_id": "cid",
            "client_secret": "csec",
            "scopes": ["https://www.googleapis.com/auth/drive.file"],
        },
    )
    root = Path(__file__).resolve().parents[1]
    c = ColabOneController(root, vault=vault, config_dir=tmp_path, background=False)
    c.auth.credentials = Mock(
        return_value=SimpleNamespace(
            to_json=lambda: json.dumps(vault.get("colab")),
            token="tok",
            refresh_token="rtok",
        )
    )
    monkeypatch.setattr(
        c.provider, "account", lambda: {"logged_in": True, "rate": 1.94, "balance": 100}
    )

    def fake_allocate(gpu):
        sess = SessionState(
            name="colabone",
            endpoint="gpu-t4-s-kkb-ass1a1-boot",
            token="tok-boot",
            url="https://boot.invalid",
            variant="GPU",
            accelerator=gpu,
            machine_shape="HIGH_RAM",
        )
        c.provider.save_session(sess, activate=True)
        return sess

    monkeypatch.setattr(c.provider, "allocate", fake_allocate)
    monkeypatch.setattr(c.provider, "ready", lambda _s: None)
    monkeypatch.setattr("bridge.provider.ContentsClient", Mock(return_value=Mock()))

    sample_payload = {
        "cpu": {"cores": 4, "percent": 32.5},
        "memory": {
            "total_bytes": 16 * 1024**3,
            "used_bytes": 4 * 1024**3,
            "available_bytes": 12 * 1024**3,
            "percent": 25.0,
        },
        "disk": {
            "total_bytes": 100 * 1024**3,
            "used_bytes": 28 * 1024**3,
            "free_bytes": 72 * 1024**3,
            "percent": 28.0,
            "read_bps": 1024.0,
            "write_bps": 2048.0,
        },
        "gpu": {
            "name": "Tesla T4",
            "total_mib": 15360,
            "used_mib": 128,
            "util_percent": 5.0,
            "temp_c": 41.0,
            "power_w": 15.0,
            "power_limit_w": 70.0,
        },
        "history": [
            {
                "t": 1000.0,
                "vram_pct": 0.8,
                "gpu_util": 5.0,
                "ram_pct": 25.0,
                "cpu_pct": 32.5,
                "disk_pct": 28.0,
                "disk_read_bps": 1024.0,
                "disk_write_bps": 2048.0,
            }
        ],
    }

    seen_during_boot = []
    seen_during_drive = []

    def fake_execute(session, code, timeout=300, hook=None, output_hook=None):
        line = "COLABONE_TELEMETRY=" + json.dumps(sample_payload) + "\n"
        # Simulate split stdout chunks over Jupyter websocket
        if output_hook is not None:
            output_hook({"text": line[:40]})
            output_hook({"text": line[40:]})
        seen_during_boot.append(c.snapshot()["runtime"])
        return [{"text": line + "\nCOLABONE_READY=1\n"}]

    monkeypatch.setattr(c.provider, "execute", fake_execute)

    def fake_remote(method, path, body=None, endpoint=None):
        if path == "snapshot":
            return {
                "runtime": {
                    "id": "gpu-t4-s-kkb-ass1a1-boot",
                    "state": "ready",
                    "protocol": 6,
                    "gpu": sample_payload["gpu"],
                    "resources": sample_payload,
                    "disk_free": sample_payload["disk"]["free_bytes"],
                    "tasks": [],
                    "drive": {"connected": False, "mode": "disabled"},
                },
                "models": c.presets(),
                "conversations": [],
                "creations": [],
            }
        if path == "drive/connect":
            seen_during_drive.append(c.snapshot()["runtime"])
            return {"connected": True, "mode": "mount", "remote_folder": "ColabOne_Drive"}
        return {"ok": True}

    c.remote = Mock(side_effect=fake_remote)
    c.start()

    assert len(seen_during_boot) == 1
    assert seen_during_boot[0]["state"] == "starting"
    assert seen_during_boot[0]["id"] == "gpu-t4-s-kkb-ass1a1-boot"
    assert seen_during_boot[0]["gpu"]["name"] == "Tesla T4"
    assert seen_during_boot[0]["resources"]["cpu"]["cores"] == 4
    assert seen_during_boot[0]["location"]["country"] == "IN"

    # By the time Drive mount starts, runtime must already be ready with live telemetry.
    assert len(seen_during_drive) == 1
    assert seen_during_drive[0]["state"] == "ready"
    assert seen_during_drive[0]["resources"]["memory"]["percent"] == 25.0

    # Enriched instance metadata exposes nested RAM/disk fields.
    inst = c.snapshot()["account"]["instances"][0]
    assert inst["ram_total_bytes"] == 16 * 1024**3
    assert inst["disk_free_bytes"] == 72 * 1024**3


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


def test_boot_output_hook_ignores_interleaved_stderr_and_split_ready(
    tmp_path, monkeypatch
):
    import json
    from pathlib import Path
    from types import SimpleNamespace
    from bridge.auth import ColabOneAuth
    from bridge.provider import ColabOneProvider
    from colab_cli.state import SessionState
    from conftest import MemoryVault

    vault = MemoryVault()
    vault.set("colab", {"token": "tok"})
    root = Path(__file__).resolve().parents[1]
    provider = ColabOneProvider(ColabOneAuth(vault), vault, root)
    monkeypatch.setattr(
        provider, "account", lambda: {"logged_in": True, "rate": 1.94, "balance": 100}
    )

    def fake_allocate(gpu):
        sess = SessionState(
            name="colabone",
            endpoint="gpu-t4-s-kkb-ass1a1-split",
            token="tok-split",
            url="https://split.invalid",
            variant="GPU",
            accelerator=gpu,
            machine_shape="HIGH_RAM",
        )
        provider.save_session(sess, activate=True)
        return sess

    monkeypatch.setattr(provider, "allocate", fake_allocate)
    monkeypatch.setattr(provider, "ready", lambda _s: None)
    monkeypatch.setattr("bridge.provider.ContentsClient", Mock(return_value=Mock()))

    sample = {"cpu": {"cores": 2, "percent": 15.0}, "history": [{"t": 1.0, "cpu_pct": 15.0}]}
    line = "\nCOLABONE_TELEMETRY=" + json.dumps(sample) + "\n"
    received = []

    def fake_execute(session, code, timeout=300, hook=None, output_hook=None):
        if output_hook:
            output_hook({"output_type": "stream", "name": "stdout", "text": line[:25]})
            output_hook({"output_type": "stream", "name": "stderr", "text": "WARNING: pip"})
            output_hook({"output_type": "stream", "name": "stdout", "text": line[25:]})
        return [
            {"output_type": "stream", "name": "stdout", "text": "\nCOLABONE_"},
            {"output_type": "stream", "name": "stderr", "text": "noise"},
            {"output_type": "stream", "name": "stdout", "text": "READY=1\n"},
        ]

    monkeypatch.setattr(provider, "execute", fake_execute)
    remote = provider.start(
        "T4",
        lambda _m: None,
        on_telemetry=lambda ep, loc, s: received.append((ep, s)),
    )
    assert remote["endpoint"] == "gpu-t4-s-kkb-ass1a1-split"
    assert len(received) == 1
    assert received[0][1]["cpu"]["cores"] == 2


def test_controller_start_compatible_with_two_arg_provider_stub(tmp_path):
    from pathlib import Path
    from types import SimpleNamespace
    from bridge.controller import ColabOneController
    from conftest import MemoryVault

    vault = MemoryVault()
    vault.set("colab", {"token": "tok"})
    root = Path(__file__).resolve().parents[1]
    c = ColabOneController(root, vault=vault, config_dir=tmp_path, background=False)
    c.auth.credentials = Mock(return_value=SimpleNamespace(token="tok"))
    c.settings["auto_drive"] = False

    called = []

    def legacy_start(gpu, notify):
        called.append(gpu)
        notify("部署远端管理服务")
        return {"endpoint": "ep-legacy"}

    c.provider.start = legacy_start
    c.provider.account = lambda: {"logged_in": True, "rate": 0, "balance": 10}
    c.remote = Mock(
        side_effect=lambda method, path, body=None, endpoint=None: (
            {
                "runtime": {"id": "ep-legacy", "state": "ready", "protocol": 6, "tasks": []},
                "models": c.presets(),
                "conversations": [],
                "creations": [],
            }
            if path == "snapshot"
            else {"ok": True}
        )
    )
    c.start()
    assert called == ["T4"]
    assert c.snapshot()["runtime"]["state"] == "ready"


def test_external_observer_isolated_kernel_modes_rotation_and_cleanup(
    tmp_path, monkeypatch
):
    import json
    from pathlib import Path
    from types import SimpleNamespace
    from bridge.auth import ColabOneAuth
    from bridge.provider import ColabOneProvider
    from conftest import MemoryVault

    vault = MemoryVault()
    vault.set("colab", {"token": "ok"})
    root = Path(__file__).resolve().parents[1]
    provider = ColabOneProvider(ColabOneAuth(vault), vault, root)
    provider.auth = Mock()
    provider.auth.profile.return_value = {"email": "dev@example.com", "sub": "u1"}

    proxies = {
        "gpu-g4-s-asso1c1-ext1": SimpleNamespace(
            url="https://ext1.invalid",
            token="tok-1",
            token_expires_in_seconds=3600,
        ),
        "gpu-a100-s-usw1b1-ext2": SimpleNamespace(
            url="https://ext2.invalid",
            token="tok-2",
            token_expires_in_seconds=3600,
        ),
    }

    def list_assignments():
        return [
            SimpleNamespace(
                endpoint=ep,
                accelerator=SimpleNamespace(value="G4" if "g4" in ep else "A100"),
                variant=SimpleNamespace(name="GPU"),
                machine_shape=SimpleNamespace(name="STANDARD"),
                runtime_proxy_info=info,
            )
            for ep, info in proxies.items()
        ]

    client = Mock()
    client.get_consumption_user_info.return_value = SimpleNamespace(
        paid_compute_units_balance=2000.0, consumption_rate_hourly=10.05
    )
    client.list_assignments.side_effect = list_assignments
    provider.client_factory = lambda: client

    # Populate proxy cache via account() without adopting anything into vault.
    acct = provider.account()
    assert acct["assignments"] == 2
    assert vault.get("runtime_sessions") is None
    assert vault.get("runtime_session") is None

    created_runtimes = []

    def fake_colab_runtime(url, token, **kwargs):
        rt = Mock()
        rt.url = url
        rt.token = token
        rt.init_kwargs = dict(kwargs)
        created_runtimes.append(rt)

        def exec_code(_code, **_ek):
            if kwargs.get("on_kernel_started") and not kwargs.get("kernel_id"):
                kwargs["on_kernel_started"](f"kid-{len(created_runtimes)}")
            if kwargs.get("on_session_started") and not kwargs.get("session_id"):
                kwargs["on_session_started"](f"sid-{len(created_runtimes)}")
            if "ext1" in url:
                payload = {
                    "source": "remote_readonly",
                    "gpu": {
                        "name": "NVIDIA RTX PRO 6000",
                        "total_mib": 98304,
                        "used_mib": 24576,
                        "util_percent": 64.0,
                        "temp_c": 61.0,
                        "power_w": 180.0,
                        "power_limit_w": 300.0,
                    },
                    "resources": {
                        "cpu": {"cores": 12, "percent": 38.5},
                        "memory": {
                            "total_bytes": 64 * 1024**3,
                            "used_bytes": 16 * 1024**3,
                            "available_bytes": 48 * 1024**3,
                            "percent": 25.0,
                        },
                        "disk": {
                            "total_bytes": 200 * 1024**3,
                            "used_bytes": 50 * 1024**3,
                            "free_bytes": 150 * 1024**3,
                            "percent": 25.0,
                            "read_bps": 4096.0,
                            "write_bps": 2048.0,
                        },
                        "gpu": {
                            "name": "NVIDIA RTX PRO 6000",
                            "total_mib": 98304,
                            "used_mib": 24576,
                            "util_percent": 64.0,
                            "temp_c": 61.0,
                            "power_w": 180.0,
                            "power_limit_w": 300.0,
                        },
                        "history": [{"t": 100.0, "vram_pct": 25.0, "ram_pct": 25.0, "disk_pct": 25.0}],
                    },
                    "disk_free": 150 * 1024**3,
                    "zone": "projects/1/zones/asia-south1-c",
                }
            else:
                payload = {
                    "source": "remote_colabone",
                    "loaded_model_name": "Qwen3.8-27B",
                    "status": {
                        "state": "ready",
                        "loaded_model": "qwen38-27b",
                        "gpu": {"name": "NVIDIA A100-SXM4-40GB", "total_mib": 40960, "used_mib": 18000},
                        "resources": {
                            "cpu": {"cores": 8, "percent": 15.0},
                            "memory": {"total_bytes": 80 * 1024**3, "used_bytes": 20 * 1024**3, "available_bytes": 60 * 1024**3, "percent": 25.0},
                            "disk": {"total_bytes": 160 * 1024**3, "used_bytes": 40 * 1024**3, "free_bytes": 120 * 1024**3, "percent": 25.0},
                            "gpu": {"name": "NVIDIA A100-SXM4-40GB", "total_mib": 40960, "used_mib": 18000},
                            "history": [{"t": 200.0, "vram_pct": 43.9, "ram_pct": 25.0, "disk_pct": 25.0}],
                        },
                        "drive": {"connected": True, "free_bytes": 50 * 1024**3},
                    },
                }
            return [{"output_type": "stream", "name": "stdout", "text": "\nCOLABONE_OBS=" + json.dumps(payload) + "\n"}]

        rt.execute_code.side_effect = exec_code
        return rt

    monkeypatch.setattr("bridge.provider.ColabRuntime", fake_colab_runtime)

    # 1) Generic Colab Web / external runtime observation
    obs1 = provider.observe_external_telemetry("gpu-g4-s-asso1c1-ext1")
    assert obs1["source"] == "remote_readonly"
    assert obs1["runtime"]["gpu"]["name"] == "NVIDIA RTX PRO 6000"
    assert obs1["runtime"]["location"]["country"] == "IN"
    assert obs1["drive_connected"] is False
    assert created_runtimes[0].init_kwargs["session_name"] == "colabone-observer"
    assert created_runtimes[0].init_kwargs["kernel_id"] is None
    # Still never adopted into managed sessions
    assert provider.managed_endpoints() == []

    # 2) Same endpoint with rotated token reconnects to our own observer kernel_id without shutting it down
    provider._external_proxies["gpu-g4-s-asso1c1-ext1"]["token"] = "tok-1-rotated"
    provider.observe_external_telemetry("gpu-g4-s-asso1c1-ext1")
    assert len(created_runtimes) == 2
    created_runtimes[0].stop.assert_called_once_with(shutdown_kernel=False)
    assert created_runtimes[1].init_kwargs["kernel_id"] == "kid-1"

    # 3) Switching to another external endpoint shuts down old observer kernel cleanly
    obs2 = provider.observe_external_telemetry("gpu-a100-s-usw1b1-ext2")
    assert obs2["source"] == "remote_colabone"
    assert obs2["loaded_model_name"] == "Qwen3.8-27B"
    assert obs2["drive_connected"] is True
    created_runtimes[1].stop.assert_called_once_with(shutdown_kernel=True)
    assert created_runtimes[2].init_kwargs["kernel_id"] is None

    # 4) Explicit close_observer shuts down active observer kernel
    provider.close_observer(shutdown_kernel=True)
    created_runtimes[2].stop.assert_called_once_with(shutdown_kernel=True)

    # 5) Disappeared assignment raises LookupError
    proxies.pop("gpu-a100-s-usw1b1-ext2")
    provider._external_proxies.pop("gpu-a100-s-usw1b1-ext2", None)
    with pytest.raises(LookupError):
        provider.observe_external_telemetry("gpu-a100-s-usw1b1-ext2")


def test_monitor_auto_selection_manual_switch_pause_and_local_start(tmp_path):
    from pathlib import Path
    from types import SimpleNamespace
    from colab_cli.state import SessionState
    from bridge.controller import ColabOneController
    from conftest import MemoryVault

    vault = MemoryVault()
    vault.set("colab", {"token": "ok"})
    root = Path(__file__).resolve().parents[1]
    c = ColabOneController(root, vault=vault, config_dir=tmp_path, background=False)
    c.auth.credentials = Mock(return_value=SimpleNamespace(token="ok"))
    c.settings["auto_drive"] = False

    # Scenario 1: Local stopped, remote has 1 CPU and 1 G4 -> auto-selects G4 (prefers GPU)
    c.update(
        account={
            "logged_in": True,
            "balance": 2116.33,
            "rate": 10.05,
            "assignments": 2,
            "instances": [
                {
                    "id": "cpu-s-usw1b1-1",
                    "managed": False,
                    "active": False,
                    "accelerator": "CPU",
                    "variant": "CPU",
                    "shape": "STANDARD",
                },
                {
                    "id": "gpu-g4-s-asso1c1-ext",
                    "managed": False,
                    "active": False,
                    "accelerator": "G4",
                    "variant": "GPU",
                    "shape": "STANDARD",
                    "location": {"country": "IN", "country_name": "印度", "region": "孟买"},
                },
            ],
        }
    )
    snap0 = c.snapshot()
    assert snap0["runtime"]["state"] == "stopped"
    assert snap0["monitor"]["mode"] == "auto"
    assert snap0["monitor"]["endpoint"] == "gpu-g4-s-asso1c1-ext"
    assert snap0["monitor"]["managed"] is False
    assert snap0["monitor"]["source"] == "remote_readonly"
    assert snap0["monitor"]["runtime"]["state"] == "connecting"
    assert [opt["id"] for opt in snap0["monitor"]["options"]] == [
        "gpu-g4-s-asso1c1-ext",
        "cpu-s-usw1b1-1",
    ]

    # Scenario 2: External observation poll populates live telemetry
    observe_mock = Mock(
        return_value={
            "endpoint": "gpu-g4-s-asso1c1-ext",
            "source": "remote_readonly",
            "runtime": {
                "id": "gpu-g4-s-asso1c1-ext",
                "state": "ready",
                "gpu": {
                    "name": "NVIDIA RTX PRO 6000",
                    "total_mib": 98304,
                    "used_mib": 12000,
                    "util_percent": 45.0,
                    "temp_c": 58.0,
                },
                "resources": {
                    "cpu": {"cores": 12, "percent": 20.0},
                    "memory": {"total_bytes": 64 * 1024**3, "used_bytes": 8 * 1024**3, "available_bytes": 56 * 1024**3, "percent": 12.5},
                    "disk": {"total_bytes": 200 * 1024**3, "used_bytes": 20 * 1024**3, "free_bytes": 180 * 1024**3, "percent": 10.0},
                    "gpu": {"name": "NVIDIA RTX PRO 6000", "total_mib": 98304, "used_mib": 12000, "util_percent": 45.0, "temp_c": 58.0},
                    "history": [{"t": 100.0, "vram_pct": 12.2, "ram_pct": 12.5, "disk_pct": 10.0, "cpu_pct": 20.0}],
                },
            },
            "loaded_model_name": "",
            "drive_connected": False,
            "drive_free_bytes": None,
        }
    )
    close_mock = Mock()
    c.provider.observe_external_telemetry = observe_mock
    c.provider.close_observer = close_mock

    assert c.poll_external_monitor_once(force=True) is True
    snap1 = c.snapshot()
    assert snap1["monitor"]["runtime"]["state"] == "ready"
    assert snap1["monitor"]["runtime"]["gpu"]["name"] == "NVIDIA RTX PRO 6000"
    assert snap1["monitor"]["error"] == ""

    # Scenario 3: Pausing external observation closes observer kernel and stops polling
    c.command("monitor.pause", {"paused": True})
    close_mock.assert_called_with(endpoint=None, shutdown_kernel=True)
    snap_paused = c.snapshot()
    assert snap_paused["monitor"]["paused"] is True
    assert snap_paused["monitor"]["runtime"]["gpu"]["name"] == "NVIDIA RTX PRO 6000"
    observe_mock.reset_mock()
    assert c.poll_external_monitor_once(force=True) is False
    observe_mock.assert_not_called()

    # Scenario 4: Resuming allows polling again, and failures stay scoped to monitor.error
    c.command("monitor.pause", {"paused": False})
    observe_mock.side_effect = RuntimeError("proxy timeout")
    assert c.poll_external_monitor_once(force=True) is False
    snap_err = c.snapshot()
    assert snap_err["error"] == ""
    assert "proxy timeout" in snap_err["monitor"]["error"]
    c.command("monitor.retry", {})
    assert c.snapshot()["monitor"]["error"] == ""

    # Scenario 5: Manual switch to CPU remote instance works, and starting local GPU auto-returns to local
    c.command("monitor.select", {"endpoint": "cpu-s-usw1b1-1"})
    assert c.snapshot()["monitor"]["mode"] == "manual"
    assert c.snapshot()["monitor"]["endpoint"] == "cpu-s-usw1b1-1"

    # Add a local managed session and verify auto mode prefers local active instance
    c.provider.save_session(
        SessionState(
            name="colabone",
            endpoint="gpu-t4-s-local-1",
            token="tok-local",
            url="https://local.invalid",
            variant="GPU",
            accelerator="T4",
            machine_shape="HIGH_RAM",
        ),
        activate=True,
    )
    c.provider.save_remote({"endpoint": "gpu-t4-s-local-1", "gpu": "T4", "deadline": 0}, activate=True)
    c.update(
        runtime={
            "id": "gpu-t4-s-local-1",
            "state": "ready",
            "gpu": {"name": "Tesla T4", "total_mib": 15360, "used_mib": 512},
        }
    )
    # Manual pin stays on cpu-s-usw1b1-1 until user switches back or starts/selects locally
    assert c.snapshot()["monitor"]["endpoint"] == "cpu-s-usw1b1-1"

    # Switching back to local active instance returns to auto mode and closes external observer
    close_mock.reset_mock()
    c.command("monitor.select", {"endpoint": "gpu-t4-s-local-1"})
    close_mock.assert_called_once_with(endpoint=None, shutdown_kernel=True)
    snap_local = c.snapshot()
    assert snap_local["monitor"]["mode"] == "auto"
    assert snap_local["monitor"]["managed"] is True
    assert snap_local["monitor"]["endpoint"] == "gpu-t4-s-local-1"
    assert snap_local["monitor"]["runtime"]["gpu"]["name"] == "Tesla T4"
    assert snap_local["monitor"]["options"][0]["id"] == "gpu-t4-s-local-1"

    # Manually selecting remote G4 does NOT change active_endpoint
    c.command("monitor.select", {"endpoint": "gpu-g4-s-asso1c1-ext"})
    assert c.snapshot()["active_endpoint"] == "gpu-t4-s-local-1"
    assert c.snapshot()["monitor"]["endpoint"] == "gpu-g4-s-asso1c1-ext"

    # If the manually selected remote G4 stops, monitor auto-falls back to local active instance
    c.update(account={"logged_in": True, "balance": 2116.33, "rate": 1.94, "assignments": 1, "instances": []})
    snap_fallback = c.snapshot()
    assert snap_fallback["monitor"]["mode"] == "auto"
    assert snap_fallback["monitor"]["endpoint"] == "gpu-t4-s-local-1"
    assert snap_fallback["monitor"]["managed"] is True
