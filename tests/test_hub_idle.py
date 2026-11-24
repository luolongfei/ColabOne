from unittest.mock import Mock
import pytest
import requests
from fastapi.testclient import TestClient
from common.hub import search_models, model_files
from server.app import create_colabone_app
from server.runtime import ColabOneTask, ColabOneConflictError


def reply(value, links=None, code=200):
    return Mock(status_code=code, json=lambda: value, links=links or {})


def test_hub_search_passes_filters_and_keeps_only_trusted_pagination():
    http = Mock()
    http.get.return_value = reply(
        [{"id": "org/model", "downloads": 123}],
        {"next": {"url": "https://huggingface.co/api/models?cursor=next"}},
    )
    result = search_models("Qwen", "updated", session=http)
    assert result["models"][0]["repo"] == "org/model"
    assert result["next_cursor"] == "next"
    assert http.get.call_args.kwargs["params"]["filter"] == "gguf"
    assert http.get.call_args.kwargs["params"]["sort"] == "lastModified"
    http.get.return_value.links = {
        "next": {"url": "https://attacker.invalid/api/models?cursor=no"}
    }
    assert not search_models(session=http)["next_cursor"]
    with pytest.raises(ValueError):
        search_models(sort="arbitrary")
    assert all("/resolve/" not in c.args[0] for c in http.get.call_args_list)


def test_hub_groups_complete_shards_and_disables_missing_hashes():
    http = Mock()
    http.get.return_value = reply(
        {
            "sha": "a" * 40,
            "siblings": [
                {
                    "rfilename": f"model-Q4_K_M-{i:05d}-of-00002.gguf",
                    "lfs": {"size": 10, "sha256": "b" * 64},
                }
                for i in (1, 2)
            ]
            + [{"rfilename": "mmproj-f16.gguf"}, {"rfilename": "model-Q8_0.gguf"}],
        }
    )
    result = model_files("org/model", session=http)
    grouped = next(f for f in result["files"] if f["shards"] == 2)
    assert grouped["size"] == 20 and grouped["supported"]
    assert grouped["quantization"] == "Q4_K_M"
    assert len(result["files"]) == 2
    assert not next(f for f in result["files"] if f["shards"] == 1)["supported"]


def test_hub_filters_imatrix_and_extracts_modern_quantizations():
    http = Mock()
    http.get.return_value = reply(
        {
            "sha": "b" * 40,
            "pipeline_tag": "text-generation",
            "siblings": [
                {
                    "rfilename": "unsloth-UD-Q4_K_XL-V2/imatrix_micro3.gguf",
                    "lfs": {"size": 1_160_000_000, "sha256": "1" * 64},
                },
                {
                    "rfilename": "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00002.gguf",
                    "lfs": {"size": 40_000_000_000, "sha256": "2" * 64},
                },
                {
                    "rfilename": "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00002-of-00002.gguf",
                    "lfs": {"size": 30_000_000_000, "sha256": "3" * 64},
                },
                {
                    "rfilename": "Llama-3-8B-imatrix-Q4_K_M.gguf",
                    "lfs": {"size": 4_900_000_000, "sha256": "4" * 64},
                },
                {
                    "rfilename": "mmproj-model-f16.gguf",
                    "lfs": {"size": 800_000_000, "sha256": "5" * 64},
                },
                {
                    "rfilename": "adapter-lora.gguf",
                    "lfs": {"size": 200_000_000, "sha256": "6" * 64},
                },
                {
                    "rfilename": "Q4/model.gguf",
                    "lfs": {"size": 5_000_000_000, "sha256": "7" * 64},
                },
            ],
        }
    )
    result = model_files("huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF", session=http)
    names = [f["filename"] for f in result["files"]]
    assert "unsloth-UD-Q4_K_XL-V2/imatrix_micro3.gguf" not in names
    assert "mmproj-model-f16.gguf" not in names
    assert "adapter-lora.gguf" not in names
    assert "Llama-3-8B-imatrix-Q4_K_M.gguf" in names
    quants = {f["filename"]: f["quantization"] for f in result["files"]}
    assert quants["unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00002.gguf"] == "UD-Q4_K_XL"
    assert quants["Llama-3-8B-imatrix-Q4_K_M.gguf"] == "Q4_K_M"
    assert quants["Q4/model.gguf"] == "Q4"
    assert result["modality"] == "text"
    assert result["comfy_only"] is False
    assert result["family"] == "Qwen"


def test_hub_search_modality_and_family_filtering():
    def fake_get(_url, params=None, headers=None, timeout=20):
        pt = (params or {}).get("pipeline_tag", "")
        cur = (params or {}).get("cursor", "")
        if pt == "text-to-image":
            return reply(
                [{"id": "city96/FLUX.1-dev-gguf", "downloads": 9000, "pipeline_tag": "text-to-image"}],
                {"next": {"url": "https://huggingface.co/api/models?cursor=img-p2"}},
            )
        if pt == "image-to-image":
            return reply(
                [{"id": "unsloth/FLUX.2-klein-4B-GGUF", "downloads": 512000, "pipeline_tag": "image-to-image"}],
            )
        if pt == "text-to-video":
            if cur == "vid-t2v-p2":
                return reply(
                    [{"id": "city96/Wan2.1-T2V-14B-gguf", "downloads": 38000, "pipeline_tag": "text-to-video"}],
                )
            return reply(
                [{"id": "QuantStack/Wan2.2-T2V-A14B-GGUF", "downloads": 752000, "pipeline_tag": "text-to-video"}],
                {"next": {"url": "https://huggingface.co/api/models?cursor=vid-t2v-p2"}},
            )
        if pt == "image-to-video":
            return reply(
                [
                    {"id": "QuantStack/Wan2.2-I2V-A14B-GGUF", "downloads": 1269000, "pipeline_tag": "image-to-video"},
                    {"id": "QuantStack/Wan2.2-T2V-A14B-GGUF", "downloads": 752000, "pipeline_tag": "image-to-video"},
                ]
            )
        if pt == "image-text-to-video":
            if cur == "vid-it2v-p2":
                return reply(
                    [{"id": "Abiray/MiniMax-H3-Pruned-GGUF", "downloads": 925000, "pipeline_tag": "image-text-to-video"}],
                )
            return reply(
                [{"id": "unsloth/MiniMax-H3-GGUF", "downloads": 3563000, "pipeline_tag": "image-text-to-video"}],
                {"next": {"url": "https://huggingface.co/api/models?cursor=vid-it2v-p2"}},
            )
        if pt == "video-to-video":
            return reply(
                [{"id": "AInVFX/SeedVR2_comfyUI", "downloads": 211000, "pipeline_tag": "video-to-video"}],
            )
        return reply(
            [
                {"id": "unsloth/Qwen3-32B-GGUF", "downloads": 5000, "pipeline_tag": "text-generation"},
                {"id": "city96/FLUX.1-dev-gguf", "downloads": 9000, "pipeline_tag": "text-to-image"},
                {"id": "city96/Wan2.1-T2V-14B-gguf", "downloads": 7000, "pipeline_tag": "text-to-video"},
                {"id": "unsloth/MiniMax-H3-GGUF", "downloads": 3563000, "pipeline_tag": "image-text-to-video"},
                {"id": "Hearthscale/MiniMax-H3-Image-GGUF", "downloads": 677000, "pipeline_tag": "text-to-image"},
                {"id": "org/bge-m3-gguf", "downloads": 3000, "pipeline_tag": "feature-extraction"},
            ]
        )

    http = Mock()
    http.get.side_effect = fake_get

    text_res = search_models(query="32B", modality="text", family="Qwen", session=http)
    assert http.get.call_args.kwargs["params"]["search"] == "Qwen 32B"
    assert [m["repo"] for m in text_res["models"]] == ["unsloth/Qwen3-32B-GGUF"]
    assert text_res["models"][0]["family"] == "Qwen"
    assert text_res["models"][0]["params"] == "32B"
    assert text_res["models"][0]["comfy_only"] is False

    http.get.reset_mock()
    img_res = search_models(modality="image", family="FLUX", session=http)
    img_tags = {c.kwargs["params"]["pipeline_tag"] for c in http.get.call_args_list}
    assert img_tags == {"text-to-image", "image-to-image"}
    assert [m["repo"] for m in img_res["models"]] == [
        "unsloth/FLUX.2-klein-4B-GGUF",
        "city96/FLUX.1-dev-gguf",
    ]
    assert all(m["modality"] == "image" and m["comfy_only"] for m in img_res["models"])

    http.get.reset_mock()
    vid_res = search_models(modality="video", session=http)
    vid_tags = {c.kwargs["params"]["pipeline_tag"] for c in http.get.call_args_list}
    assert vid_tags == {
        "text-to-video",
        "image-to-video",
        "image-text-to-video",
        "video-to-video",
    }
    assert [m["repo"] for m in vid_res["models"]] == [
        "unsloth/MiniMax-H3-GGUF",
        "QuantStack/Wan2.2-I2V-A14B-GGUF",
        "QuantStack/Wan2.2-T2V-A14B-GGUF",
        "AInVFX/SeedVR2_comfyUI",
    ]
    assert vid_res["models"][0]["family"] == "MiniMax-H3"
    assert vid_res["next_cursor"].startswith("c1:")
    assert all(m["modality"] == "video" and m["comfy_only"] for m in vid_res["models"])

    vid_p2 = search_models(modality="video", cursor=vid_res["next_cursor"], session=http)
    assert [m["repo"] for m in vid_p2["models"]] == [
        "Abiray/MiniMax-H3-Pruned-GGUF",
        "city96/Wan2.1-T2V-14B-gguf",
    ]
    assert vid_p2["next_cursor"] == ""

    with pytest.raises(ValueError):
        search_models(modality="video", cursor="c1:!!!", session=http)
    with pytest.raises(ValueError):
        search_models(modality="video", cursor="c1:e30=", session=http)
    with pytest.raises(ValueError):
        search_models(modality="text", cursor=vid_res["next_cursor"], session=http)

    # Updated sorting parses mixed RFC3339 fractional/offset timestamps identically to Go.
    def fake_updated(_url, params=None, headers=None, timeout=20):
        pt = (params or {}).get("pipeline_tag", "")
        if pt == "text-to-image":
            return reply(
                [
                    {"id": "org/b", "downloads": 10, "lastModified": "2026-11-20T12:00:00Z", "pipeline_tag": "text-to-image"},
                    {"id": "org/d", "downloads": 999, "lastModified": "", "pipeline_tag": "text-to-image"},
                ]
            )
        return reply(
            [
                {"id": "org/a", "downloads": 10, "lastModified": "2026-11-20T12:00:00+00:00", "pipeline_tag": "image-to-image"},
                {"id": "org/c", "downloads": 5, "lastModified": "2026-11-20T12:00:00.500Z", "pipeline_tag": "image-to-image"},
            ]
        )

    http_upd = Mock()
    http_upd.get.side_effect = fake_updated
    upd_res = search_models(modality="image", sort="updated", session=http_upd)
    assert [m["repo"] for m in upd_res["models"]] == ["org/c", "org/a", "org/b", "org/d"]

    # Partial transient failure on page 2 preserves the failed pipeline's cursor for retry.
    it2v_attempts = {"n": 0}

    def fake_partial(_url, params=None, headers=None, timeout=20):
        pt = (params or {}).get("pipeline_tag", "")
        cur = (params or {}).get("cursor", "")
        if pt == "image-text-to-video" and cur == "vid-it2v-p2":
            it2v_attempts["n"] += 1
            if it2v_attempts["n"] == 1:
                bad = reply({}, code=502)
                bad.raise_for_status.side_effect = requests.HTTPError("502")
                return bad
            return reply(
                [{"id": "Abiray/MiniMax-H3-Pruned-GGUF", "downloads": 925000, "pipeline_tag": "image-text-to-video"}]
            )
        if pt == "text-to-video" and cur == "vid-t2v-p2":
            return reply(
                [{"id": "city96/Wan2.1-T2V-14B-gguf", "downloads": 38000, "pipeline_tag": "text-to-video"}]
            )
        return reply([])

    http_partial = Mock()
    http_partial.get.side_effect = fake_partial
    partial_p2 = search_models(modality="video", cursor=vid_res["next_cursor"], session=http_partial)
    assert [m["repo"] for m in partial_p2["models"]] == ["city96/Wan2.1-T2V-14B-gguf"]
    assert partial_p2["next_cursor"].startswith("c1:")
    retry_p3 = search_models(modality="video", cursor=partial_p2["next_cursor"], session=http_partial)
    assert [m["repo"] for m in retry_p3["models"]] == ["Abiray/MiniMax-H3-Pruned-GGUF"]
    assert retry_p3["next_cursor"] == ""

    all_res = search_models(modality="all", session=http)
    by_repo = {m["repo"]: m for m in all_res["models"]}
    assert by_repo["unsloth/Qwen3-32B-GGUF"]["modality"] == "text"
    assert by_repo["city96/FLUX.1-dev-gguf"]["modality"] == "image"
    assert by_repo["city96/FLUX.1-dev-gguf"]["comfy_only"] is True
    assert by_repo["Hearthscale/MiniMax-H3-Image-GGUF"]["modality"] == "image"
    assert by_repo["city96/Wan2.1-T2V-14B-gguf"]["modality"] == "video"
    assert by_repo["unsloth/MiniMax-H3-GGUF"]["modality"] == "video"
    assert by_repo["unsloth/MiniMax-H3-GGUF"]["family"] == "MiniMax-H3"
    assert by_repo["city96/Wan2.1-T2V-14B-gguf"]["comfy_only"] is True

    with pytest.raises(ValueError):
        search_models(modality="audio", session=http)


def test_hub_gated_error_is_actionable_without_leaking_token():
    http = Mock()
    http.get.return_value = reply({}, code=403)
    with pytest.raises(RuntimeError, match="Token") as error:
        search_models(token="secret-token", session=http)
    assert "secret-token" not in str(error.value)


def test_idle_grace_countdown_activity_and_polling(runtime, monkeypatch):
    assert runtime.idle_minutes == 30
    runtime.last_active = 100
    assert runtime.idle_status(100)["remaining_seconds"] == 1800
    watching = runtime.idle_status(1839)
    assert watching["phase"] == "watching"
    assert watching["remaining_seconds"] == 61
    countdown = runtime.idle_status(1840)
    assert countdown["phase"] == "countdown"
    assert countdown["remaining_seconds"] == 60
    assert not runtime.claim_auto_release(1899)
    monkeypatch.setattr("server.runtime.time.time", lambda: 300)
    # Read-only polling must not reset activity.
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "Tesla T4", "total_mib": 15360, "used_mib": 512},
    )
    runtime.status()
    runtime.list_models()
    runtime.comfy.store.list_workflows()
    runtime.comfy.store.list_outputs()
    assert runtime.last_active == 100
    runtime.touch()
    assert runtime.idle_status(300)["remaining_seconds"] == 1800

    token = "test-control-token-at-least-32-chars"
    headers = {"Authorization": "Bearer " + token}
    with TestClient(create_colabone_app(runtime, token)) as client:
        for minutes, expected in ((45, 2700), (60, 3600), (120, 7200)):
            resp = client.post(
                "/api/v1/configure",
                json={"idle_minutes": minutes},
                headers=headers,
            )
            assert resp.status_code == 200
            assert runtime.idle_minutes == minutes
            assert runtime.idle_status(300)["remaining_seconds"] == expected
        assert (
            client.post(
                "/api/v1/configure",
                json={"idle_minutes": 30},
                headers=headers,
            ).status_code
            == 200
        )

    assert runtime.idle_status(300)["remaining_seconds"] == 1800
    assert runtime.claim_auto_release(2100)
    with pytest.raises(ColabOneConflictError):
        runtime.touch()


def test_active_tasks_and_authorization_lease_protect_idle_but_not_budget(
    runtime, monkeypatch
):
    runtime.last_active = 0
    task = ColabOneTask("download")
    runtime.tasks[task.id] = task
    assert runtime.idle_status(2000)["phase"] == "active"
    assert not runtime.claim_auto_release(2000)
    task.state = "completed"
    monkeypatch.setattr("server.runtime.time.time", lambda: 500)
    runtime.touch(330)
    assert not runtime.claim_auto_release(800)
    runtime.deadline = 790
    assert runtime.claim_auto_release(800)


def test_activity_endpoint_requires_auth_and_validates_lease(runtime):
    token = "test-control-token-at-least-32-chars"
    with TestClient(create_colabone_app(runtime, token)) as client:
        assert client.post("/api/v1/activity", json={}).status_code == 401
        assert (
            client.post(
                "/api/v1/activity",
                json={"hold_seconds": 9999},
                headers={"Authorization": "Bearer " + token},
            ).status_code
            == 422
        )
        response = client.post(
            "/api/v1/activity", json={}, headers={"Authorization": "Bearer " + token}
        )
        assert response.json()["remaining_seconds"] > 1799


def test_auto_release_retries_and_posts_to_owned_runtime_without_client(
    runtime, monkeypatch
):
    import threading
    import requests

    runtime.last_active = 0
    monkeypatch.setattr("server.runtime.time.time", lambda: 2000)
    monkeypatch.setenv("TBE_RUNTIME_ADDR", "owned.invalid")
    finished = threading.Event()
    runtime.stopping = Mock()
    runtime.stopping.wait.side_effect = [False, False, True]
    runtime.http = Mock()

    def release(*args, **kwargs):
        if runtime.http.post.call_count == 1:
            raise requests.ConnectionError("temporary")
        finished.set()
        return Mock()

    runtime.http.post.side_effect = release
    runtime.start_background()
    assert finished.wait(2)
    assert runtime.http.post.call_count == 2
    assert all(
        c.args[0] == "http://owned.invalid/unassign"
        for c in runtime.http.post.call_args_list
    )
    assert runtime.quiescing
