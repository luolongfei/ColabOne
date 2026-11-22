import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock
import pytest
from bridge.auth import ColabOneAuth
from bridge.provider import ColabOneProvider
from bridge.controller import ColabOneController
from conftest import MemoryVault


def make_provider():
    vault = MemoryVault()
    provider = ColabOneProvider(ColabOneAuth(vault), vault, Path(__file__).resolve().parents[1])
    return provider, vault


def assignment(endpoint):
    return SimpleNamespace(endpoint=endpoint)


def store_session(vault):
    vault.set(
        "runtime_session",
        {
            "name": "colabone",
            "endpoint": "owned",
            "token": "not-real",
            "url": "https://runtime.invalid",
        },
    )


def test_release_only_owned_endpoint():
    p, vault = make_provider()
    store_session(vault)
    client = Mock()
    client.list_assignments.side_effect = [
        [assignment("owned"), assignment("unrelated")],
        [assignment("unrelated")],
    ]
    p.client_factory = lambda: client
    p.release()
    client.unassign.assert_called_once_with("owned")
    assert not vault.get("runtime_session")


def test_no_adoption_of_unrelated_runtime():
    p, vault = make_provider()
    client = Mock()
    p.client_factory = lambda: client
    assert p.session(refresh=True) is None
    p.release()
    client.unassign.assert_not_called()


def test_release_failure_retains_ownership():
    p, vault = make_provider()
    store_session(vault)
    client = Mock()
    client.list_assignments.side_effect = OSError("offline")
    p.client_factory = lambda: client
    with pytest.raises(OSError):
        p.release()
    assert vault.get("runtime_session")["endpoint"] == "owned"


def test_zero_rate_starts_without_max_cu_and_sets_zero_deadline(monkeypatch):
    p, vault = make_provider()
    monkeypatch.setattr(
        p, "account", lambda: {"logged_in": True, "rate": 0, "balance": 100}
    )

    def fake_allocate(gpu):
        sess = p_session("new", gpu, "HIGH_RAM")
        p.save_session(sess, activate=True)
        return sess

    monkeypatch.setattr(p, "allocate", fake_allocate)
    monkeypatch.setattr(p, "ready", lambda _session: None)
    monkeypatch.setattr("bridge.provider.ContentsClient", Mock(return_value=Mock()))
    monkeypatch.setattr(
        p, "execute", Mock(return_value=[{"text": "COLABONE_READY=1\n"}])
    )
    release = Mock()
    monkeypatch.setattr(p, "release", release)

    remote = p.start("T4", lambda _: None)
    assert remote["endpoint"] == "new"
    assert remote["deadline"] == 0
    assert "max_cu" not in remote
    stored = p.remote_info("new")
    assert stored is not None
    assert stored["deadline"] == 0
    assert "max_cu" not in stored
    release.assert_not_called()


def test_new_allocation_deploy_failure_rolls_back(monkeypatch):
    p, vault = make_provider()
    monkeypatch.setattr(
        p, "account", lambda: {"logged_in": True, "rate": 1, "balance": 100}
    )

    def fake_allocate(gpu):
        sess = p_session("new", gpu, "HIGH_RAM")
        p.save_session(sess, activate=True)
        return sess

    assignments = [assignment("new")]

    def unassign(ep):
        assignments[:] = [a for a in assignments if a.endpoint != ep]

    client = Mock()
    client.list_assignments.side_effect = lambda: list(assignments)
    client.unassign.side_effect = unassign
    p.client_factory = lambda: client

    monkeypatch.setattr(p, "allocate", fake_allocate)
    monkeypatch.setattr(p, "ready", Mock(side_effect=RuntimeError("boot failed")))
    release = Mock(wraps=p.release)
    monkeypatch.setattr(p, "release", release)
    with pytest.raises(RuntimeError, match="boot failed"):
        p.start("T4", lambda _: None)
    release.assert_called_once()
    client.unassign.assert_called_once_with("new")
    assert p.managed_endpoints() == []
    assert not vault.get("runtime_session")


def test_snapshot_and_catalog_do_not_allocate(tmp_path):
    root = Path(__file__).resolve().parents[1]
    c = ColabOneController(root, vault=MemoryVault(), config_dir=tmp_path, background=False)
    c.provider = Mock()
    assert c.snapshot()["runtime"]["state"] == "stopped"
    assert c.snapshot()["models"]
    c.provider.allocate.assert_not_called()


def test_settings_never_write_secret_to_plaintext(tmp_path):
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.save_settings(
        {
            "hf_token": "hf-test",
            "max_cu": 5,
        }
    )
    text = (tmp_path / "settings.json").read_text(encoding="utf-8")
    assert "secret-value" not in text and "hf-test" not in text
    assert c.vault.get("hf") == "hf-test"
    snap = c.snapshot()
    assert "max_cu" not in snap["settings"]
    assert "secret-value" not in str(snap) and "hf-test" not in str(snap)


def test_idle_policy_default_30_min_migration_and_live_dropdown_update(tmp_path):
    root = Path(__file__).resolve().parents[1]

    fresh = ColabOneController(
        root, vault=MemoryVault(), config_dir=tmp_path / "fresh", background=False
    )
    fresh_snap = fresh.snapshot()
    assert fresh_snap["settings"]["idle_minutes"] == 30
    assert "max_cu" not in fresh_snap["settings"]

    legacy_dir = tmp_path / "legacy"
    legacy_dir.mkdir(parents=True, exist_ok=True)
    (legacy_dir / "settings.json").write_text(
        json.dumps({"idle_minutes": 3, "idle_policy": 2, "max_cu": 5}),
        encoding="utf-8",
    )
    c = ColabOneController(root, vault=MemoryVault(), config_dir=legacy_dir, background=False)
    migrated_snap = c.snapshot()
    assert migrated_snap["settings"]["idle_minutes"] == 30
    assert "max_cu" not in migrated_snap["settings"]

    for choice in (15, 30, 60, 120, 45):
        c.last_config = 123.0
        assert c.command("settings.save", {"idle_minutes": choice}) == {"ok": True}
        assert c.snapshot()["settings"]["idle_minutes"] == choice
        assert c.last_config == 0
        reloaded = ColabOneController(
            root, vault=MemoryVault(), config_dir=legacy_dir, background=False
        )
        assert reloaded.snapshot()["settings"]["idle_minutes"] == choice
        assert "max_cu" not in reloaded.snapshot()["settings"]

    for invalid in (0, 999):
        with pytest.raises(ValueError):
            c.command("settings.save", {"idle_minutes": invalid})
    assert c.snapshot()["settings"]["idle_minutes"] == 45


def test_release_blocks_on_unsynced_data(tmp_path):
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.remote = Mock(side_effect=RuntimeError("会话尚未同步"))
    c.provider = Mock()
    with pytest.raises(RuntimeError, match="尚未同步"):
        c.release()
    c.provider.release.assert_not_called()
    c.release(force=True)
    c.provider.release.assert_called_once()


def test_kernel_connection_reused_and_rotated_without_replaying(monkeypatch):
    p, vault = make_provider()
    store_session(vault)
    s = p.session()
    first, second = Mock(), Mock()
    first.execute_code.return_value = [{"text": "first"}]
    factory = Mock(side_effect=[first, second])
    monkeypatch.setattr("bridge.provider.ColabRuntime", factory)
    p.execute(s, "pass")
    p.execute(s, "pass")
    assert factory.call_count == 1
    assert first.execute_code.call_count == 2
    s.token = "rotated"
    second.execute_code.side_effect = TimeoutError("uncertain mutation outcome")
    with pytest.raises(TimeoutError):
        p.execute(s, "mutate()")
    first.stop.assert_called_once_with(shutdown_kernel=False)
    second.execute_code.assert_called_once()
    second.stop.assert_called_once_with(shutdown_kernel=False)
    assert p.kernel_runtime is None


def test_explicit_stop_all_tries_every_reviewed_instance_and_leaves_new_ones(
    monkeypatch,
):
    p, vault = make_provider()
    store_session(vault)
    client = Mock()
    client.list_assignments.side_effect = [
        [assignment("owned"), assignment("other"), assignment("new-after-preview")],
        [assignment("new-after-preview")],
    ]
    client.unassign.side_effect = [OSError("lost response"), None]
    p.client_factory = lambda: client
    result = p.release_all(["owned", "other"], Mock())
    assert {call.args[0] for call in client.unassign.call_args_list} == {
        "owned",
        "other",
    }
    assert result["remaining"] == 1
    assert vault.get("runtime_session") is None


def test_stop_all_partial_failure_does_not_claim_success(monkeypatch):
    p, vault = make_provider()
    store_session(vault)
    client = Mock()
    client.list_assignments.return_value = [assignment("owned")]
    p.client_factory = lambda: client
    monkeypatch.setattr("bridge.provider.time.sleep", lambda _: None)
    with pytest.raises(RuntimeError, match="仍有 1"):
        p.release_all(["owned"], Mock())
    assert vault.get("runtime_session")


def test_stop_all_requires_current_account_preview_and_sync(tmp_path):
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.auth = Mock()
    c.auth.profile.return_value = {"sub": "same-account"}
    c.provider = Mock()
    c.provider.account.return_value = {
        "instances": [{"id": "owned", "managed": True}],
        "assignments": 1,
        "rate": 2,
    }
    c.remote = Mock(side_effect=RuntimeError("sync pending"))
    store_session(c.vault)
    with pytest.raises(RuntimeError, match="清单"):
        c.release_all("not-previewed")
    plan = c.preview_release_all()
    with pytest.raises(RuntimeError, match="sync pending"):
        c.release_all(plan["id"])
    c.provider.release_all.assert_not_called()
    c.auth.profile.return_value = {"sub": "changed-account"}
    with pytest.raises(RuntimeError, match="清单"):
        c.release_all(plan["id"], force=True)
    c.provider.release_all.assert_not_called()


def test_release_confirms_disappearance_when_remote_guard_wins_race():
    p, vault = make_provider()
    store_session(vault)
    client = Mock()
    client.list_assignments.side_effect = [[assignment("owned")], []]
    client.unassign.side_effect = RuntimeError("already removed")
    p.client_factory = lambda: client
    assert p.release()["ok"]
    assert not vault.get("runtime_session")


def test_account_describes_managed_and_external_instance_specs():
    p, vault = make_provider()
    vault.set("colab", {"token": "ok"})
    p.auth = Mock()
    p.auth.profile.return_value = {"email": "dev@example.com", "sub": "u1"}
    vault.set(
        "runtime_sessions",
        {
            "m-s-1": {
                "name": "colabone",
                "endpoint": "m-s-1",
                "token": "t1",
                "url": "https://r1.invalid",
            }
        },
    )
    vault.set("active_endpoint", "m-s-1")
    client = Mock()
    client.get_consumption_user_info.return_value = SimpleNamespace(
        paid_compute_units_balance=88.5, consumption_rate_hourly=3.14
    )
    client.list_assignments.return_value = [
        SimpleNamespace(
            endpoint="m-s-1",
            accelerator=SimpleNamespace(value="T4"),
            variant=SimpleNamespace(name="GPU"),
            machine_shape=SimpleNamespace(name="HIGH_RAM"),
        ),
        SimpleNamespace(
            endpoint="m-s-ext",
            accelerator=SimpleNamespace(value="A100"),
            variant=SimpleNamespace(name="GPU"),
            machine_shape=SimpleNamespace(name="STANDARD"),
        ),
    ]
    p.client_factory = lambda: client
    acct = p.account()
    assert acct["assignments"] == 2
    assert acct["instances"][0] == {
        "id": "m-s-1",
        "managed": True,
        "active": True,
        "accelerator": "T4",
        "variant": "GPU",
        "shape": "HIGH_RAM",
    }
    assert acct["instances"][1] == {
        "id": "m-s-ext",
        "managed": False,
        "active": False,
        "accelerator": "A100",
        "variant": "GPU",
        "shape": "STANDARD",
    }


def test_multiple_managed_instances_switch_and_selective_release(tmp_path):
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.auth = Mock()
    c.auth.profile.return_value = {"sub": "u1", "email": "dev@example.com"}
    c.provider.auth = c.auth
    c.provider.save_session(
        p_session("ep-t4", "T4", "HIGH_RAM"), activate=True
    )
    c.provider.save_remote(
        {"endpoint": "ep-t4", "deadline": 0, "gpu": "T4"},
        activate=True,
    )
    c.update(
        runtime={
            "state": "ready",
            "id": "ep-t4",
            "loaded_model": "qwen25-05b",
            "gpu": {"name": "NVIDIA T4", "total_mib": 15360, "used_mib": 1200},
        },
        comfy_outputs=[
            {
                "id": "a" * 32,
                "workflow_id": "wf-t4",
                "workflow_name": "T4 Workflow",
                "kind": "image",
                "filename": "t4.png",
                "extension": "png",
                "mime": "image/png",
                "size": 100,
                "sha256": "b" * 64,
                "created_at": 1,
                "elapsed": 1.0,
            }
        ],
    )
    c.provider.save_session(
        p_session("ep-a100", "A100", "HIGH_RAM"), activate=True
    )
    c.provider.save_remote(
        {"endpoint": "ep-a100", "deadline": 0, "gpu": "A100"},
        activate=True,
    )
    c.update(
        runtime={
            "state": "ready",
            "id": "ep-a100",
            "gpu": {"name": "NVIDIA A100", "total_mib": 40960, "used_mib": 800},
        },
        comfy_outputs=[
            {
                "id": "c" * 32,
                "workflow_id": "wf-a100",
                "workflow_name": "A100 Workflow",
                "kind": "image",
                "filename": "a100.png",
                "extension": "png",
                "mime": "image/png",
                "size": 200,
                "sha256": "d" * 64,
                "created_at": 2,
                "elapsed": 2.0,
            }
        ],
    )

    # Switch back to ep-t4 and verify isolated per-instance state is preserved.
    c.remote = Mock(side_effect=lambda method, path, body=None, endpoint=None: (
        {"ok": True}
        if path == "prepare-release"
        else {
            "runtime": c.instance_states[endpoint or c._active_endpoint()]["runtime"],
            "models": c.presets(),
            "workflows": [],
            "comfy_outputs": c.instance_states[endpoint or c._active_endpoint()][
                "comfy_outputs"
            ],
        }
    ))
    c.command("runtime.select", {"endpoint": "ep-t4"})
    assert c.snapshot()["runtime"]["id"] == "ep-t4"
    assert c.snapshot()["comfy_outputs"][0]["workflow_name"] == "T4 Workflow"

    # Selective stop of only ep-a100 leaves ep-t4 and external instance running.
    client = Mock()
    assignments = [
        assignment("ep-t4"),
        assignment("ep-a100"),
        assignment("external-other-client"),
    ]

    def unassign(ep):
        assignments[:] = [a for a in assignments if a.endpoint != ep]

    client.list_assignments.side_effect = lambda: list(assignments)
    client.unassign.side_effect = unassign
    client.get_consumption_user_info.return_value = SimpleNamespace(
        paid_compute_units_balance=90.0, consumption_rate_hourly=1.5
    )
    c.vault.set("colab", {"token": "ok"})
    c.provider.client_factory = lambda: client

    plan = c.preview_release_all()
    assert {item["id"] for item in plan["instances"]} == {
        "ep-t4",
        "ep-a100",
        "external-other-client",
    }
    c.release_all(plan["id"], endpoints=["ep-a100"])
    client.unassign.assert_called_once_with("ep-a100")
    assert c.provider.managed_endpoints() == ["ep-t4"]
    assert c.snapshot()["runtime"]["id"] == "ep-t4"
    assert c.snapshot()["account"]["assignments"] == 2


def p_session(endpoint: str, accel: str, shape: str):
    from colab_cli.state import SessionState

    return SessionState(
        name="colabone",
        endpoint=endpoint,
        token="tok-" + endpoint,
        url=f"https://{endpoint}.invalid",
        variant="GPU",
        accelerator=accel,
        machine_shape=shape,
    )


def test_location_resolution_from_endpoint_metadata_ipinfo_and_trace(runtime, tmp_path):
    from common.location import infer_location_from_endpoint, probe_vm_location

    assert infer_location_from_endpoint("gpu-t4-s-kkb-usw1b1-abc123") == {
        "country": "US",
        "country_name": "美国",
        "region": "俄勒冈",
        "zone": "us-west1-b",
    }
    assert infer_location_from_endpoint("m-s-kkb-ase1c1-999") == {
        "country": "TW",
        "country_name": "中国台湾",
        "region": "彰化",
        "zone": "asia-east1-c",
    }
    assert infer_location_from_endpoint("m-s-kkb-asse1a1-000") == {
        "country": "SG",
        "country_name": "新加坡",
        "region": "",
        "zone": "asia-southeast1-a",
    }
    assert infer_location_from_endpoint("m-s-1") is None

    # 1) GCP metadata server succeeds
    http_meta = Mock()
    http_meta.get.return_value = SimpleNamespace(
        status_code=200, text="projects/12345/zones/europe-west4-a"
    )
    runtime.http = http_meta
    detected = runtime.detect_location()
    assert detected == {
        "country": "NL",
        "country_name": "荷兰",
        "region": "埃姆斯哈文",
        "zone": "europe-west4-a",
    }
    assert runtime.status()["location"] == detected

    # 2) Metadata blocked -> ipinfo fallback deduplicates redundant Singapore region
    http_ipinfo = Mock()

    def fake_get(url, **kwargs):
        if "metadata.google.internal" in url:
            raise OSError("blocked")
        if "ipinfo.io" in url:
            return SimpleNamespace(
                status_code=200,
                json=lambda: {
                    "country": "SG",
                    "region": "Singapore",
                    "city": "Singapore",
                },
            )
        raise AssertionError("should not reach later fallbacks")

    http_ipinfo.get.side_effect = fake_get
    assert probe_vm_location(http_ipinfo) == {
        "country": "SG",
        "country_name": "新加坡",
        "region": "",
        "zone": "",
    }

    # 3) Cloudflare trace fallback
    http_trace = Mock()
    http_trace.get.side_effect = lambda url, **kwargs: (
        SimpleNamespace(status_code=200, text="ip=1.2.3.4\nloc=JP\ncolo=NRT\n")
        if "cloudflare.com" in url
        else (_ for _ in ()).throw(OSError("offline"))
    )
    assert probe_vm_location(http_trace) == {
        "country": "JP",
        "country_name": "日本",
        "region": "",
        "zone": "",
    }

    # 4) Assignment & controller enrichment propagate location
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path / "cfg",
        background=False,
    )
    c.provider.save_session(
        p_session("gpu-t4-s-kkb-usw4b1-demo", "T4", "HIGH_RAM"), activate=True
    )
    snap = c.snapshot()
    assert snap["account"]["instances"][0]["location"] == {
        "country": "US",
        "country_name": "美国",
        "region": "内华达",
        "zone": "us-west4-b",
    }


def _make_colab_request_error(status: int, reason: str = "Service Unavailable"):
    from colab_cli.client import ColabRequestError

    resp = SimpleNamespace(status_code=status, reason=reason, text="<html>503</html>")
    return ColabRequestError(
        f"Failed to issue request POST https://colab.research.google.com/tun/m/assign?nbh=abc: {reason}",
        request=SimpleNamespace(method="POST", url="https://colab.research.google.com/tun/m/assign"),
        response=resp,
        response_body=resp.text,
    )


def _make_proxy_info(endpoint: str):
    from datetime import datetime, timedelta, timezone

    return SimpleNamespace(
        token="tok-" + endpoint,
        url=f"https://{endpoint}.invalid",
        expires_at=lambda: datetime.now(timezone.utc) + timedelta(hours=1),
    )


def test_allocate_retries_503_with_reconciliation_and_logs_progress():
    from colab_cli.client import GetAssignmentResponse, PostAssignmentResponse, Accelerator, AssignmentVariant

    p, vault = make_provider()
    p._assign_retry_delays = (0, 0, 0)
    seen_nbhs = []
    post_calls = [0]

    class FakeTwoPhaseClient:
        def _get_assignment(self, nbh, variant, accelerator, shape):
            seen_nbhs.append(nbh)
            return GetAssignmentResponse(
                acc=accelerator.value,
                nbh=str(nbh),
                token=f"xsrf-{len(seen_nbhs)}",
                variant=variant,
            )

        def _post_assignment(self, nbh, token, variant, accelerator, shape):
            post_calls[0] += 1
            if post_calls[0] < 3:
                raise _make_colab_request_error(503)
            return SimpleNamespace(
                endpoint="gpu-g4-s-usw4a1-retryok",
                accelerator=Accelerator.G4,
                variant=AssignmentVariant.GPU,
                runtime_proxy_info=_make_proxy_info("gpu-g4-s-usw4a1-retryok"),
            )

    p.client_factory = lambda: FakeTwoPhaseClient()
    logs = []
    stages = []
    session = p.allocate(
        "G4",
        on_log=lambda msg, src="GPU", lvl="info": logs.append((lvl, src, msg)),
        on_stage=stages.append,
    )
    assert session.endpoint == "gpu-g4-s-usw4a1-retryok"
    assert post_calls[0] == 3
    # Every retry reuses the exact same notebook hash for safe reconciliation.
    assert len(seen_nbhs) == 3 and len(set(seen_nbhs)) == 1
    assert any("耗时" in msg and "HTTP 503" in msg and "第 2/4 次" in msg for _, _, msg in logs)
    assert any("耗时" in msg and "HTTP 503" in msg and "第 3/4 次" in msg for _, _, msg in logs)
    congestion_hints = [msg for _, _, msg in logs if "算力池较紧张" in msg]
    assert len(congestion_hints) == 1
    assert "L4 / A100 / H100" in congestion_hints[0]
    assert stages == [
        "正在分配 G4（第 2/4 次）",
        "正在排队分配 G4（算力池较紧张 · 第 3/4 次）",
    ]
    assert p.active_endpoint() == "gpu-g4-s-usw4a1-retryok"


def test_allocate_reconciles_existing_assignment_after_503_without_double_allocating():
    from colab_cli.client import Assignment, GetAssignmentResponse, RuntimeProxyInfo

    p, _ = make_provider()
    p._assign_retry_delays = (0, 0)
    get_calls = [0]
    post_calls = [0]

    class ReconcilingClient:
        def _get_assignment(self, nbh, variant, accelerator, shape):
            get_calls[0] += 1
            if get_calls[0] == 1:
                return GetAssignmentResponse(
                    acc=accelerator.value,
                    nbh=str(nbh),
                    token="xsrf-1",
                    variant=variant,
                )
            return Assignment(
                endpoint="gpu-g4-s-asso1c1-reconciled",
                runtimeProxyInfo=RuntimeProxyInfo(
                    token="tok-rec",
                    tokenExpiresInSeconds=3600,
                    url="https://reconciled.invalid",
                ),
            )

        def _post_assignment(self, nbh, token, variant, accelerator, shape):
            post_calls[0] += 1
            raise _make_colab_request_error(503)

    p.client_factory = lambda: ReconcilingClient()
    logs = []
    session = p.allocate(
        "G4", on_log=lambda msg, src="GPU", lvl="info": logs.append((lvl, src, msg))
    )
    assert session.endpoint == "gpu-g4-s-asso1c1-reconciled"
    assert post_calls[0] == 1
    assert get_calls[0] == 2
    assert any("已通过状态核验确认 G4 GPU 实例分配成功" in msg for _, _, msg in logs)


def test_allocate_final_reconciliation_adopts_vm_after_last_503():
    from colab_cli.client import Assignment, GetAssignmentResponse, RuntimeProxyInfo

    p, _ = make_provider()
    p._assign_retry_delays = (0,)
    get_calls = [0]
    post_calls = [0]

    class LastChanceReconcileClient:
        def _get_assignment(self, nbh, variant, accelerator, shape):
            get_calls[0] += 1
            if get_calls[0] <= 2:
                return GetAssignmentResponse(
                    acc=accelerator.value,
                    nbh=str(nbh),
                    token=f"xsrf-{get_calls[0]}",
                    variant=variant,
                )
            return Assignment(
                endpoint="gpu-g4-s-usw4a1-final",
                runtimeProxyInfo=RuntimeProxyInfo(
                    token="tok-final",
                    tokenExpiresInSeconds=3600,
                    url="https://final.invalid",
                ),
            )

        def _post_assignment(self, nbh, token, variant, accelerator, shape):
            post_calls[0] += 1
            raise _make_colab_request_error(503)

    p.client_factory = lambda: LastChanceReconcileClient()
    session = p.allocate("G4")
    assert session.endpoint == "gpu-g4-s-usw4a1-final"
    assert post_calls[0] == 2
    assert get_calls[0] == 3


def test_allocate_translates_exhausted_503_412_and_400_into_clear_errors():
    from colab_cli.client import GetAssignmentResponse

    p, _ = make_provider()
    p._assign_retry_delays = (0, 0)

    class AlwaysFailingClient:
        def __init__(self, status):
            self.status = status
            self.shapes = []

        def _get_assignment(self, nbh, variant, accelerator, shape):
            self.shapes.append(shape)
            return GetAssignmentResponse(
                acc=accelerator.value,
                nbh=str(nbh),
                token="xsrf",
                variant=variant,
            )

        def _post_assignment(self, nbh, token, variant, accelerator, shape):
            raise _make_colab_request_error(self.status)

    p.client_factory = lambda: AlwaysFailingClient(503)
    with pytest.raises(RuntimeError) as exc_503:
        p.allocate("G4")
    assert "HTTP 503" in str(exc_503.value)
    assert "已自动重试 2 次" in str(exc_503.value)
    assert "Failed to issue request" not in str(exc_503.value)

    p.client_factory = lambda: AlwaysFailingClient(412)
    with pytest.raises(RuntimeError) as exc_412:
        p.allocate("G4")
    assert "HTTP 412" in str(exc_412.value)
    assert "上限" in str(exc_412.value)

    c400 = AlwaysFailingClient(400)
    p.client_factory = lambda: c400
    with pytest.raises(RuntimeError) as exc_400:
        p.allocate("T4")
    # T4 tries HIGH_RAM first, then falls back to None before surfacing friendly 400.
    assert len(c400.shapes) == 2 and c400.shapes[1] is None
    assert "HTTP 400" in str(exc_400.value)


def test_start_surfaces_kernel_boot_error_details_before_rollback(monkeypatch):
    p, _ = make_provider()
    monkeypatch.setattr(
        p, "account", lambda: {"logged_in": True, "rate": 10.05, "balance": 100}
    )

    def fake_allocate(gpu, on_log=None, on_stage=None):
        sess = p_session("gpu-g4-booterr", gpu, "STANDARD")
        p.save_session(sess, activate=True)
        return sess

    monkeypatch.setattr(p, "allocate", fake_allocate)
    monkeypatch.setattr(p, "ready", lambda _s, on_log=None: None)
    monkeypatch.setattr("bridge.provider.ContentsClient", Mock(return_value=Mock()))
    monkeypatch.setattr(
        p,
        "execute",
        Mock(
            return_value=[
                {
                    "output_type": "error",
                    "ename": "RuntimeError",
                    "evalue": "pip dependency wheel failed",
                }
            ]
        ),
    )
    release = Mock()
    monkeypatch.setattr(p, "release", release)

    with pytest.raises(RuntimeError, match="远端初始化未完成：RuntimeError: pip dependency wheel failed"):
        p.start("G4", lambda _: None)
    release.assert_called_once()


def test_monitor_cleans_stale_remote_when_sessions_empty(tmp_path):
    vault = MemoryVault()
    vault.set("remote", {"endpoint": "stale-ep", "deadline": 0, "gpu": "G4"})
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=vault,
        config_dir=tmp_path,
        background=False,
    )
    c.last_auth = 1e12
    c.last_heartbeat = 1e12
    # Run one monitor iteration without blocking.
    c.stop.wait = lambda _timeout=None: c.stop.set()
    c.monitor()
    assert vault.get("remote") is None
    assert c.snapshot()["error"] == ""


def test_allocate_exponential_backoff_10_attempts_and_cancel_interrupt(monkeypatch):
    import threading
    import requests as _requests
    from colab_cli.client import GetAssignmentResponse
    from bridge.provider import _COLABONE_ASSIGN_RETRY_DELAYS

    assert _COLABONE_ASSIGN_RETRY_DELAYS == (6, 12, 20, 30, 30, 30, 30, 30, 30)
    assert len(_COLABONE_ASSIGN_RETRY_DELAYS) == 9  # 1 initial + 9 retries = 10 attempts

    p, _ = make_provider()
    timeline = []
    waited_delays = []
    cancel_ev = threading.Event()

    class NetworkDropClient:
        def __init__(self):
            self.posts = 0

        def _get_assignment(self, nbh, variant, accelerator, shape):
            return GetAssignmentResponse(
                acc=accelerator.value, nbh=str(nbh), token="xsrf", variant=variant
            )

        def _post_assignment(self, nbh, token, variant, accelerator, shape):
            self.posts += 1
            if self.posts == 3:
                cancel_ev.set()
            raise _requests.ConnectionError("remote closed")

    def fake_wait(timeout=None):
        waited_delays.append(timeout)
        timeline.append(("wait", timeout))
        return cancel_ev.is_set()

    monkeypatch.setattr(cancel_ev, "wait", fake_wait)
    p.client_factory = lambda: NetworkDropClient()
    logs = []
    stages = []
    with pytest.raises(RuntimeError, match="已取消 GPU 分配"):
        p.allocate(
            "G4",
            on_log=lambda msg, _lv="info": logs.append(msg),
            on_stage=lambda st: (stages.append(st), timeline.append(("stage", st))),
            cancel_event=cancel_ev,
        )
    assert waited_delays == [6, 12, 20]
    assert any("耗时" in m and "网络异常" in m and "第 2/10 次" in m for m in logs)
    # Stage notification must happen AFTER each backoff wait, never before,
    # and never for an attempt cancelled during the backoff wait.
    assert stages == ["正在分配 G4（第 2/10 次）", "正在分配 G4（第 3/10 次）"]
    assert timeline == [
        ("wait", 6),
        ("stage", "正在分配 G4（第 2/10 次）"),
        ("wait", 12),
        ("stage", "正在分配 G4（第 3/10 次）"),
        ("wait", 20),
    ]


def test_allocate_emits_congestion_hint_once_across_repeated_503s(monkeypatch):
    from colab_cli.client import GetAssignmentResponse, Accelerator, AssignmentVariant

    p, _ = make_provider()
    p._assign_retry_delays = (6, 12, 20, 30)
    post_calls = [0]
    timeline = []
    clock = [100.0]

    def fake_monotonic():
        return clock[0]

    def fake_sleep(sec):
        timeline.append(("sleep", sec))
        clock[0] += sec

    class SlowBusyClient:
        def _get_assignment(self, nbh, variant, accelerator, shape):
            return GetAssignmentResponse(
                acc=accelerator.value, nbh=str(nbh), token="xsrf", variant=variant
            )

        def _post_assignment(self, nbh, token, variant, accelerator, shape):
            post_calls[0] += 1
            if post_calls[0] <= 3:
                clock[0] += 30.8
                raise _make_colab_request_error(503)
            clock[0] += 3.2
            return SimpleNamespace(
                endpoint="gpu-g4-s-euw4a4-queuedok",
                accelerator=Accelerator.G4,
                variant=AssignmentVariant.GPU,
                runtime_proxy_info=_make_proxy_info("gpu-g4-s-euw4a4-queuedok"),
            )

    monkeypatch.setattr("bridge.provider.time.monotonic", fake_monotonic)
    monkeypatch.setattr("bridge.provider.time.sleep", fake_sleep)
    p.client_factory = lambda: SlowBusyClient()
    logs = []
    stages = []
    session = p.allocate(
        "G4",
        on_log=lambda msg, src="GPU", lvl="info": logs.append((lvl, src, msg)),
        on_stage=lambda st: (stages.append(st), timeline.append(("stage", st))),
    )
    assert session.endpoint == "gpu-g4-s-euw4a4-queuedok"
    assert post_calls[0] == 4
    retry_logs = [msg for _, _, msg in logs if "自动重试" in msg]
    assert len(retry_logs) == 3
    assert all("耗时 30.8 秒 · HTTP 503" in msg for msg in retry_logs)
    congestion_hints = [msg for _, _, msg in logs if "算力池较紧张" in msg]
    # Congestion guidance is emitted exactly once across 3 consecutive 503s.
    assert len(congestion_hints) == 1
    assert "L4 / A100 / H100" in congestion_hints[0]
    assert stages == [
        "正在分配 G4（第 2/5 次）",
        "正在排队分配 G4（算力池较紧张 · 第 3/5 次）",
        "正在排队分配 G4（算力池较紧张 · 第 4/5 次）",
    ]
    assert timeline == [
        ("sleep", 6),
        ("stage", "正在分配 G4（第 2/5 次）"),
        ("sleep", 12),
        ("stage", "正在排队分配 G4（算力池较紧张 · 第 3/5 次）"),
        ("sleep", 20),
        ("stage", "正在排队分配 G4（算力池较紧张 · 第 4/5 次）"),
    ]


def test_per_endpoint_vault_storage_avoids_packed_blob_limit():
    p, vault = make_provider()
    for idx in range(3):
        ep = f"m-s-multi-{idx}"
        p.save_session(p_session(ep, "G4", "STANDARD"), activate=(idx == 2))
    assert vault.get("runtime_session_ids") == [
        "m-s-multi-0",
        "m-s-multi-1",
        "m-s-multi-2",
    ]
    assert vault.get("runtime_sessions") is None
    for idx in range(3):
        assert vault.get(f"runtime_session:m-s-multi-{idx}")["endpoint"] == f"m-s-multi-{idx}"
    assert len(p._sessions_map()) == 3
    assert p.active_endpoint() == "m-s-multi-2"


def test_safe_message_redacts_quoted_urls_ws_and_refresh_tokens():
    from bridge.activity import safe_message

    raw = (
        "POST 'https://colab.example.com/tun/m/assign?authuser=0' failed; "
        'ws="wss://proxy.example.com/api/kernels/123/channels" '
        '"token": "ya29.super-secret-value" refresh=1//09AbCdEfGhIjKlMnOpQrStUvWxYz0123456789'
    )
    cleaned = safe_message(raw)
    assert "colab.example.com" not in cleaned
    assert "proxy.example.com" not in cleaned
    assert "ya29.super-secret-value" not in cleaned
    assert "09AbCdEfGhIjKlMnOpQrStUvWxYz" not in cleaned
    assert "[链接已隐藏]" in cleaned


def test_error_clear_respects_expected_and_select_guards_busy(tmp_path):
    c = ColabOneController(
        Path(__file__).resolve().parents[1],
        vault=MemoryVault(),
        config_dir=tmp_path,
        background=False,
    )
    c.data["error"] = "new error"
    c.command("error.clear", {"expected": "older error"})
    assert c.data["error"] == "new error"
    c.command("error.clear", {"expected": "new error"})
    assert c.data["error"] == ""

    assert c.operation_lock.acquire(blocking=False)
    try:
        with pytest.raises(RuntimeError, match="请等待当前操作完成后再切换实例"):
            c.command("runtime.select", {"endpoint": "ep-2"})
    finally:
        c.operation_lock.release()
