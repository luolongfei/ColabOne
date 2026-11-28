"""Offline unit tests for the ColabOne AI Fashion Studio Try-On workflow (`fashion-tryon-h3`)."""

from __future__ import annotations

import io
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import MagicMock

import pytest
from PIL import Image

from common.comfyui import ComfyCatalog, WorkflowRunRequest
from server.comfyui import ColabOneComfyWorkflowStore
from server.fashion_tryon import (
    COLABONE_FASHION_BF16_THRESHOLD_MIB,
    COLABONE_FASHION_BUILTIN_POSES,
    COLABONE_FASHION_GARMENT_SLOT_KEYS,
    COLABONE_FASHION_MAX_POSE_SHOTS,
    COLABONE_FASHION_MAX_REFS_PER_GROUP,
    COLABONE_FASHION_MODEL_SLOT_KEYS,
    COLABONE_FASHION_POSE_BY_ID,
    COLABONE_FASHION_POSE_REF_SLOT_KEYS,
    COLABONE_FASHION_POSE_SHOTS_KEY,
    ColabOneFashionTryOnRunner,
    auto_trim_ecommerce_borders,
    build_minimax_h3_tryon_prompt_text,
    build_minimax_h3_tryon_workflow,
    build_rmbg3_garment_matte_prompt,
    composite_soft_alpha_on_white,
    parse_colabone_fashion_photo_roles,
    plan_colabone_fashion_shots,
    resolve_colabone_fashion_dimensions,
    resolve_colabone_fashion_models,
    resolve_colabone_fashion_photo_entries,
    resolve_colabone_fashion_precision,
)

ROOT = Path(__file__).resolve().parents[1]


def _make_png(width: int, height: int, color: tuple[int, int, int]) -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", (width, height), color=color).save(buf, format="PNG")
    return buf.getvalue()


def test_catalog_contains_valid_non_commercial_fashion_tryon_template():
    raw = json.loads((ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    catalog = ComfyCatalog.model_validate(raw)
    by_id = {tpl.id: tpl for tpl in catalog.templates}
    assert "fashion-tryon-h3" in by_id

    tpl = by_id["fashion-tryon-h3"]
    assert tpl.source == "builtin"
    assert tpl.commercial_ok is False
    assert "MiniMax-H3-Image" in tpl.license
    assert "RMBG 3.0" in tpl.license
    assert "非商" in tpl.license_notice
    assert tpl.pipeline is not None
    assert tpl.pipeline.kind == "fashion_tryon"
    assert tpl.min_vram_mib == 22528
    assert tpl.min_disk_mib == 53248

    basic_fields = [f for f in tpl.fields if f.group == "basic"]
    advanced_fields = [f for f in tpl.fields if f.group == "advanced"]
    basic_keys = [f.key for f in basic_fields]
    assert basic_keys == [
        *COLABONE_FASHION_MODEL_SLOT_KEYS,
        "pipeline.model_photo_roles",
        *COLABONE_FASHION_GARMENT_SLOT_KEYS,
        "pipeline.garment_photo_roles",
        COLABONE_FASHION_POSE_SHOTS_KEY,
        *COLABONE_FASHION_POSE_REF_SLOT_KEYS,
        "pipeline.scene_preset",
        "pipeline.shot_pack",
        "pipeline.aspect_preset",
    ]
    by_key = {f.key: f for f in tpl.fields}
    assert by_key["1.image"].required is True
    assert by_key["2.image"].required is True
    assert by_key["pipeline.model_photo_2"].required is False
    assert by_key["pipeline.garment_photo_6"].required is False
    assert by_key[COLABONE_FASHION_POSE_SHOTS_KEY].required is False
    assert by_key[COLABONE_FASHION_POSE_SHOTS_KEY].default == ""
    assert by_key["pipeline.pose_ref_1"].required is False
    assert by_key["pipeline.pose_ref_6"].required is False
    assert by_key["pipeline.scene_preset"].default == "studio_light_gray"
    assert by_key["pipeline.shot_pack"].default == "full_6"
    assert by_key["pipeline.aspect_preset"].default == "3x4_2k"
    # Old scattered advanced photo slots are consolidated into the basic + galleries.
    advanced_keys = {f.key for f in advanced_fields}
    for removed in (
        "pipeline.model_extra_photo_1",
        "pipeline.model_extra_photo_2",
        "pipeline.garment_detail_photo",
        "pipeline.garment_back_photo",
    ):
        assert removed not in advanced_keys
    assert "pipeline.scene_ref_photo" in advanced_keys
    assert len(advanced_fields) >= 15


def test_multi_ref_photo_entries_respect_order_roles_cap_and_legacy_fallback(tmp_path):
    dummy = tmp_path / "dummy.png"
    dummy.write_bytes(_make_png(32, 32, (200, 200, 200)))

    # Order + role parsing with promoted primary slot and 6-photo cap.
    input_map = {
        "1.image": ("front.png", dummy),
        "pipeline.model_photo_2": ("side.png", dummy),
        "pipeline.model_photo_3": ("closeup.png", dummy),
        "pipeline.garment_photo_2": ("back.png", dummy),
        "pipeline.garment_photo_3": ("detail.png", dummy),
        "2.image": ("dress_front.png", dummy),
    }
    overrides = {
        "pipeline.model_photo_roles": json.dumps(
            {
                "order": ["pipeline.model_photo_2", "1.image", "pipeline.model_photo_3"],
                "roles": {
                    "pipeline.model_photo_2": "side",
                    "1.image": "front",
                    "pipeline.model_photo_3": "closeup",
                },
            }
        ),
        "pipeline.garment_photo_roles": json.dumps(
            {
                "order": ["2.image", "pipeline.garment_photo_2", "pipeline.garment_photo_3"],
                "roles": {
                    "2.image": "front",
                    "pipeline.garment_photo_2": "back",
                    "pipeline.garment_photo_3": "detail",
                },
            }
        ),
    }
    models, garments = resolve_colabone_fashion_photo_entries(input_map, overrides)
    assert [(m["key"], m["role"]) for m in models] == [
        ("pipeline.model_photo_2", "side"),
        ("1.image", "front"),
        ("pipeline.model_photo_3", "closeup"),
    ]
    assert [(g["key"], g["role"]) for g in garments] == [
        ("2.image", "front"),
        ("pipeline.garment_photo_2", "back"),
        ("pipeline.garment_photo_3", "detail"),
    ]

    # Malformed metadata degrades cleanly without crashing.
    order, roles = parse_colabone_fashion_photo_roles("{bad json", allowed_roles={"auto", "front"})
    assert order == []
    assert roles == {}

    # Cap at COLABONE_FASHION_MAX_REFS_PER_GROUP (6) even if legacy keys also supplied.
    many_inputs = {k: (f"{k}.png", dummy) for k in COLABONE_FASHION_MODEL_SLOT_KEYS}
    many_inputs["pipeline.model_extra_photo_1"] = ("legacy.png", dummy)
    capped_models, _ = resolve_colabone_fashion_photo_entries(many_inputs, {})
    assert len(capped_models) == COLABONE_FASHION_MAX_REFS_PER_GROUP


def test_precision_and_model_resolution_adapt_to_gpu_vram_and_cleanup():
    assert resolve_colabone_fashion_precision("auto", gpu_total_mib=98304) == "bf16"
    assert (
        resolve_colabone_fashion_precision(
            "auto", gpu_total_mib=COLABONE_FASHION_BF16_THRESHOLD_MIB
        )
        == "bf16"
    )
    assert resolve_colabone_fashion_precision("auto", gpu_total_mib=24576) == "int8"
    assert resolve_colabone_fashion_precision("auto", gpu_total_mib=None) == "int8"
    assert resolve_colabone_fashion_precision("bf16", gpu_total_mib=24576) == "bf16"
    assert resolve_colabone_fashion_precision("int8", gpu_total_mib=98304) == "int8"

    bf16_models, bf16_names = resolve_colabone_fashion_models("bf16", garment_cleanup="rmbg3")
    assert len(bf16_models) == 6
    assert bf16_names["minimax_dit"] == "minimax_h3_image_pruned_bf16.safetensors"
    assert bf16_names["minimax_clip"] == "qwen3vl_32b_minimax_h3_int8_convrot.safetensors"
    assert bf16_names["rmbg3_dit"] == "rmbg3_bf16.safetensors"

    int8_models, int8_names = resolve_colabone_fashion_models("int8", garment_cleanup="rmbg3")
    assert len(int8_models) == 6
    assert int8_names["minimax_dit"] == "minimax_h3_image_pruned_int8_convrot.safetensors"
    assert int8_names["minimax_clip"] == "qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors"
    assert int8_names["rmbg3_dit"] == "rmbg3_int8_convrot.safetensors"

    no_rmbg_models, no_rmbg_names = resolve_colabone_fashion_models(
        "int8", garment_cleanup="crop_only"
    )
    assert len(no_rmbg_models) == 3
    assert "rmbg3_dit" not in no_rmbg_names


def test_shot_planner_supports_full_6_core_4_hero_1_custom_and_custom_lines():
    assert len(plan_colabone_fashion_shots({})) == 6
    assert len(plan_colabone_fashion_shots({"pipeline.shot_pack": "core_4"})) == 4
    assert len(plan_colabone_fashion_shots({"pipeline.shot_pack": "hero_1"})) == 1

    custom_selected = plan_colabone_fashion_shots(
        {
            "pipeline.shot_pack": "custom",
            "pipeline.pose_1_enabled": True,
            "pipeline.pose_2_enabled": False,
            "pipeline.pose_3_enabled": False,
            "pipeline.pose_4_enabled": False,
            "pipeline.pose_5_enabled": True,
            "pipeline.pose_6_enabled": True,
        }
    )
    assert [s["id"] for s in custom_selected] == ["pose_1", "pose_5", "pose_6"]

    with pytest.raises(ValueError, match="至少需要勾选 1 个姿势"):
        plan_colabone_fashion_shots(
            {
                "pipeline.shot_pack": "custom",
                "pipeline.pose_1_enabled": False,
                "pipeline.pose_2_enabled": False,
                "pipeline.pose_3_enabled": False,
                "pipeline.pose_4_enabled": False,
                "pipeline.pose_5_enabled": False,
                "pipeline.pose_6_enabled": False,
            }
        )

    lines = plan_colabone_fashion_shots(
        {"pipeline.custom_pose_prompts": "正面微笑站姿\n完整背面展示\n半身领口特写"}
    )
    assert [s["view"] for s in lines] == ["front", "back", "detail"]
    assert resolve_colabone_fashion_dimensions("3x4_2k") == (1728, 2304)
    assert resolve_colabone_fashion_dimensions("3x4_4k") == (2304, 3072)


def test_border_trim_and_soft_alpha_compositing():
    # Create an image with pure-white top/bottom promo margins surrounding a blue dress block.
    canvas = Image.new("RGB", (200, 300), (255, 255, 255))
    for y in range(45, 255):
        for x in range(20, 180):
            canvas.putpixel((x, y), (110, 160, 220))
    buf = io.BytesIO()
    canvas.save(buf, format="PNG")
    trimmed = auto_trim_ecommerce_borders(buf.getvalue())
    with Image.open(io.BytesIO(trimmed)) as out_im:
        assert out_im.height < 260
        assert out_im.width <= 200

    # Soft grayscale alpha matte preserves translucent semi-gray pixel smoothly over white.
    fg = _make_png(120, 120, (100, 140, 220))
    matte = Image.new("L", (120, 120), 0)
    for y in range(20, 100):
        for x in range(20, 100):
            matte.putpixel((x, y), 128)
    m_buf = io.BytesIO()
    matte.save(m_buf, format="PNG")
    cleaned = composite_soft_alpha_on_white(fg, m_buf.getvalue())
    with Image.open(io.BytesIO(cleaned)) as comp:
        center = comp.getpixel((comp.width // 2, comp.height // 2))
        # Mid-gray matte blends (100,140,220) with white (255,255,255) -> around (177,197,237).
        assert 160 <= center[0] <= 195
        assert 220 <= center[2] <= 248


def test_native_comfy_graph_builders_match_comfyui_v046_nodes():
    rmbg = build_rmbg3_garment_matte_prompt(
        "garment_front.png",
        rmbg3_dit="rmbg3_bf16.safetensors",
        rmbg3_clip="qwen3vl_4b_bf16.safetensors",
        rmbg3_vae="flux2-vae.safetensors",
        garment_notes="keep 6 gems, exclude hanger pearls",
    )
    assert rmbg["2"]["inputs"]["type"] == "fibo2"
    assert rmbg["5"]["class_type"] == "TextEncodeFibo2"
    assert rmbg["5"]["inputs"]["images.image_1"] == ["4", 0]
    assert rmbg["6"]["inputs"]["steps"] == 4
    assert rmbg["6"]["inputs"]["cfg"] == 1.0

    prompt_txt = build_minimax_h3_tryon_prompt_text(
        shot=plan_colabone_fashion_shots({"pipeline.shot_pack": "hero_1"})[0],
        ref_roles=[(1, "model"), (2, "garment_front"), (3, "garment_detail")],
        scene_preset="studio_light_gray",
        garment_notes="keep 6 crystal gems",
    )
    assert "<Picture 1>" in prompt_txt
    assert "<Picture 2>" in prompt_txt
    assert "<Picture 3>" in prompt_txt
    assert "light-gray" in prompt_txt
    assert "clean, understated footwear" in prompt_txt

    keep_orig_prompt = build_minimax_h3_tryon_prompt_text(
        shot=plan_colabone_fashion_shots({"pipeline.shot_pack": "full_6"})[5],
        ref_roles=[(1, "model", "front"), (2, "garment_front", "front")],
        scene_preset="studio_light_gray",
        styling_match="keep_model_original",
    )
    assert "original shoes" in keep_orig_prompt
    assert "separate back reference photo" in keep_orig_prompt

    h3 = build_minimax_h3_tryon_workflow(
        reference_images=["model.png", "garment.png", "detail.png"],
        prompt_text=prompt_txt,
        width=1728,
        height=2304,
        minimax_dit="minimax_h3_image_pruned_bf16.safetensors",
        minimax_clip="qwen3vl_32b_minimax_h3_int8_convrot.safetensors",
        minimax_vae="minimax_h3_image_vae_fp16.safetensors",
        steps=6,
        seed=42,
    )
    assert h3["2"]["inputs"]["type"] == "minimax"
    assert h3["30"]["class_type"] == "MiniMaxH3ImageConditioning"
    assert h3["30"]["inputs"]["images.image_0"] == ["10", 0]
    assert h3["30"]["inputs"]["images.image_1"] == ["11", 0]
    assert h3["30"]["inputs"]["images.image_2"] == ["12", 0]
    assert h3["31"]["class_type"] == "ConditioningZeroOut"
    assert h3["32"]["inputs"]["steps"] == 6
    assert h3["32"]["inputs"]["cfg"] == 1.0


def test_fashion_tryon_runner_end_to_end_6_shots_with_rmbg3_hero_anchor_and_back_view(
    tmp_path,
):
    raw = json.loads((ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    catalog = ComfyCatalog.model_validate(raw)
    wf = next(t.model_dump() for t in catalog.templates if t.id == "fashion-tryon-h3")

    store = ColabOneComfyWorkflowStore(tmp_path / "store", builtin_templates=catalog.templates)
    model_path = tmp_path / "model.png"
    garment_path = tmp_path / "garment.png"
    detail_path = tmp_path / "detail.png"
    back_path = tmp_path / "back.png"
    model_path.write_bytes(_make_png(128, 160, (230, 210, 200)))
    garment_path.write_bytes(_make_png(128, 160, (120, 175, 235)))
    detail_path.write_bytes(_make_png(128, 128, (140, 190, 245)))
    back_path.write_bytes(_make_png(128, 160, (115, 170, 230)))

    executed_prompts: list[dict] = []
    freed_calls: list[dict] = []
    downloaded_models: list[dict] = []

    fake_http = MagicMock()

    def _fake_post(url, json=None, timeout=None):
        if url.endswith("/free"):
            freed_calls.append(dict(json or {}))
        return SimpleNamespace(status_code=200, ok=True)

    fake_http.post.side_effect = _fake_post
    runtime = SimpleNamespace(
        gpu_info=lambda: {"name": "NVIDIA RTX PRO 6000", "total_mib": 98304},
        http=fake_http,
    )

    packs = MagicMock()
    packs.check_preflight.return_value = None
    packs.active_pack_signature = ""
    packs.needs_pip_restart.return_value = False

    def _fake_ensure_pack(task, wf_arg, hf_token="", progress_range=None):
        downloaded_models.extend(wf_arg.get("models") or [])
        return False

    packs.ensure_pack.side_effect = _fake_ensure_pack

    comfy = MagicMock()
    comfy.runtime = runtime
    comfy.store = store
    comfy.packs = packs
    comfy.base_url = "http://127.0.0.1:8188"
    comfy._loaded_pack_signature = ""
    comfy.io_out_dir = None
    comfy.is_running.return_value = True

    upload_counter = [0]

    def _fake_upload(fname, fpath):
        upload_counter[0] += 1
        return f"up_{upload_counter[0]}_{Path(fname).name}"

    comfy._upload_input_file.side_effect = _fake_upload

    def _fake_exec(task, wf_arg, prompt_payload, **kwargs):
        executed_prompts.append(prompt_payload)
        out_dir = Path(kwargs["output_dir"])
        out_path = out_dir / f"gen_{len(executed_prompts):02d}.png"
        if "5" in prompt_payload and prompt_payload["5"]["class_type"] == "TextEncodeFibo2":
            # Return bright soft matte for RMBG 3.0.
            Image.new("L", (128, 160), 220).save(out_path, format="PNG")
        else:
            out_path.write_bytes(_make_png(128, 160, (180, 190, 200)))
        return [{"path": str(out_path), "filename": out_path.name, "prompt_id": f"p{len(executed_prompts)}"}]

    comfy._execute_single_prompt.side_effect = _fake_exec

    notes_log: list[str] = []
    task = SimpleNamespace(
        progress=0.0,
        message="",
        outputs=[],
        lock=None,
        check=lambda: None,
        note=lambda msg, level="info": notes_log.append(str(msg)),
    )

    runner = ColabOneFashionTryOnRunner(comfy)
    req = WorkflowRunRequest(
        workflow_id="fashion-tryon-h3",
        overrides={
            "pipeline.scene_preset": "studio_light_gray",
            "pipeline.shot_pack": "full_6",
            "pipeline.aspect_preset": "3x4_2k",
            "pipeline.garment_cleanup": "rmbg3",
            "pipeline.save_cleaned_garment": True,
            "pipeline.styling_match": "keep_model_original",
            "pipeline.precision_mode": "auto",
            "pipeline.anchor_hero_shot": True,
            "pipeline.steps": 6,
            "pipeline.seed": 42,
            "pipeline.model_photo_roles": json.dumps(
                {
                    "order": ["1.image", "pipeline.model_photo_2"],
                    "roles": {"1.image": "front", "pipeline.model_photo_2": "side"},
                }
            ),
            "pipeline.garment_photo_roles": json.dumps(
                {
                    "order": [
                        "2.image",
                        "pipeline.garment_photo_2",
                        "pipeline.garment_photo_3",
                    ],
                    "roles": {
                        "2.image": "front",
                        "pipeline.garment_photo_2": "back",
                        "pipeline.garment_photo_3": "detail",
                    },
                }
            ),
        },
        inputs={},
    )
    model_side_path = tmp_path / "model_side.png"
    model_side_path.write_bytes(_make_png(128, 160, (225, 205, 195)))
    resolved_inputs = [
        ("1.image", "model.png", model_path),
        ("pipeline.model_photo_2", "model_side.png", model_side_path),
        ("2.image", "garment.png", garment_path),
        ("pipeline.garment_photo_2", "back.png", back_path),
        ("pipeline.garment_photo_3", "detail.png", detail_path),
    ]

    records = runner.run(task, wf, req, resolved_inputs)

    # 96 GB GPU auto-selected BF16 tier (3 MiniMax BF16 + 3 RMBG 3.0 BF16 = 6 weights).
    assert len(downloaded_models) == 6
    assert any("minimax_h3_image_pruned_bf16.safetensors" in m["path"] for m in downloaded_models)
    assert any("rmbg3_bf16.safetensors" in m["path"] for m in downloaded_models)

    # Detail close-up automatically skipped RMBG 3.0! So only 2 RMBG passes (front + back)
    # + VRAM /free before & after Stage 1 + 6 MiniMax-H3-Image shots = 8 prompts total.
    assert len(executed_prompts) == 8
    assert len(freed_calls) >= 2
    assert all(call == {"unload_models": True, "free_memory": True} for call in freed_calls)
    assert any("对「服装正面主图」执行软透明抠衣清洗" in m for m in notes_log)
    assert any("已保存 6 张高清多姿态成片与 2 张抠衣质检图" in m for m in notes_log)

    # Verify multi-angle model cues, styling option, Hero Master anchoring on shots 2..6, and back-view routing on shot 6.
    shot_1 = executed_prompts[2]
    shot_2 = executed_prompts[3]
    shot_6 = executed_prompts[7]
    assert shot_1["30"]["class_type"] == "MiniMaxH3ImageConditioning"
    assert "colabone_hero_master.png" not in shot_1["10"]["inputs"]["image"]
    assert "side / three-quarter profile" in shot_1["30"]["inputs"]["prompt"]
    assert "original shoes" in shot_1["30"]["inputs"]["prompt"]
    assert "colabone_hero_master.png" in shot_2["10"]["inputs"]["image"]
    assert "BACK VIEW GARMENT FIDELITY" in shot_6["30"]["inputs"]["prompt"]
    assert "detail.png" in shot_6["14"]["inputs"]["image"]

    # Verify 2 RMBG 3.0 cutout inspection previews (detail skipped!) + 6 high-res lookbook outputs.
    assert len(records) == 8
    assert len(task.outputs) == 8
    assert [r["filename"] for r in records] == [
        "00_抠衣质检_服装正面主图.png",
        "00_抠衣质检_服装背面参考图.png",
        "01_正面站姿.png",
        "02_轻提裙摆.png",
        "03_左侧微侧.png",
        "04_右侧回眸.png",
        "05_半身细节.png",
        "06_完整背面.png",
    ]
    assert task.progress == 1


def test_audit_regressions_multi_back_cold_start_rgba_and_inf_overrides(tmp_path):
    from server.fashion_tryon import _parse_int

    # 1. _parse_int handles non-finite strings safely without OverflowError.
    assert _parse_int("inf", 42, min_val=0, max_val=100) == 42
    assert _parse_int("-inf", 42, min_val=0, max_val=100) == 42
    assert _parse_int("1e400", 42, min_val=0, max_val=100) == 42

    # 2. RGBA transparent PNG with (0,0,0,0) exterior composites over white without black fringe.
    rgba = Image.new("RGBA", (120, 120), (0, 0, 0, 0))
    for y in range(40, 80):
        for x in range(40, 80):
            rgba.putpixel((x, y), (120, 170, 230, 255))
    rgba_buf = io.BytesIO()
    rgba.save(rgba_buf, format="PNG")
    # Matte intentionally extends 10px past the original opaque region into (0,0,0,0) transparent pixels.
    matte = Image.new("L", (120, 120), 0)
    for y in range(30, 90):
        for x in range(30, 90):
            matte.putpixel((x, y), 255)
    m_buf = io.BytesIO()
    matte.save(m_buf, format="PNG")
    cleaned = composite_soft_alpha_on_white(rgba_buf.getvalue(), m_buf.getvalue())
    with Image.open(io.BytesIO(cleaned)) as comp:
        # Check border pixel inside matte but outside original alpha -> must be white, not black.
        sample = comp.getpixel((comp.width // 2, 10))
        assert sample[0] >= 245 and sample[1] >= 245 and sample[2] >= 245

    # 3. Cold-start ensure_ready calls start_server BEFORE mark_native_pack_ready,
    #    and INT8 + crop_only uses exact model size + 4 GiB headroom instead of 48 GiB floor.
    raw = json.loads((ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    catalog = ComfyCatalog.model_validate(raw)
    wf = next(t.model_dump() for t in catalog.templates if t.id == "fashion-tryon-h3")
    call_order: list[str] = []
    preflight_seen: list[dict] = []

    comfy = MagicMock()
    comfy.runtime = SimpleNamespace(
        gpu_info=lambda: {"name": "NVIDIA L4", "total_mib": 24576},
        http=MagicMock(),
    )
    comfy.is_running.return_value = False
    comfy.packs.check_preflight.side_effect = lambda payload: preflight_seen.append(dict(payload))
    comfy.prepare.side_effect = lambda task: call_order.append("prepare")
    comfy.start_server.side_effect = lambda task: call_order.append("start_server")
    comfy.mark_native_pack_ready.side_effect = lambda wid: call_order.append(f"mark:{wid}")

    task = SimpleNamespace(
        id="task-audit-1",
        progress=0.0,
        outputs=[],
        lock=None,
        check=lambda: None,
        note=lambda msg, level="info": None,
    )
    runner = ColabOneFashionTryOnRunner(comfy)
    precision, _ = runner.ensure_ready(
        task,
        wf,
        overrides={"pipeline.precision_mode": "auto", "pipeline.garment_cleanup": "crop_only"},
    )
    assert precision == "int8"
    assert call_order.index("start_server") < call_order.index("mark:fashion-tryon-h3")
    assert preflight_seen[0]["min_disk_mib"] < 44_000

    # 4. Multiple back-view garments produce distinct cleaned files, uploads, and task_id-tagged inspection records.
    store = ColabOneComfyWorkflowStore(tmp_path / "store2", builtin_templates=catalog.templates)
    model_p = tmp_path / "m.png"
    front_p = tmp_path / "f.png"
    back1_p = tmp_path / "b1.png"
    back2_p = tmp_path / "b2.png"
    for p, col in (
        (model_p, (220, 200, 190)),
        (front_p, (100, 150, 220)),
        (back1_p, (80, 130, 210)),
        (back2_p, (60, 110, 200)),
    ):
        p.write_bytes(_make_png(96, 128, col))

    uploaded_names: list[str] = []
    comfy2 = MagicMock()
    comfy2.runtime = SimpleNamespace(
        gpu_info=lambda: {"name": "NVIDIA L4", "total_mib": 24576},
        http=MagicMock(),
    )
    comfy2.store = store
    comfy2.packs = MagicMock()
    comfy2.base_url = "http://127.0.0.1:8188"
    comfy2.is_running.return_value = True
    comfy2._upload_input_file.side_effect = lambda fn, fp: (uploaded_names.append(fn) or fn)

    def _fake_exec2(t, wf_arg, prompt_payload, **kwargs):
        out_dir = Path(kwargs["output_dir"])
        out_p = out_dir / f"out_{len(uploaded_names)}.png"
        out_p.write_bytes(_make_png(96, 128, (180, 180, 180)))
        return [{"path": str(out_p), "filename": out_p.name, "prompt_id": "px"}]

    comfy2._execute_single_prompt.side_effect = _fake_exec2
    runner2 = ColabOneFashionTryOnRunner(comfy2)
    req2 = WorkflowRunRequest(
        workflow_id="fashion-tryon-h3",
        overrides={
            "pipeline.shot_pack": "hero_1",
            "pipeline.garment_cleanup": "crop_only",
            "pipeline.save_cleaned_garment": True,
            "pipeline.garment_photo_roles": json.dumps(
                {
                    "order": ["2.image", "pipeline.garment_photo_2", "pipeline.garment_photo_3"],
                    "roles": {
                        "2.image": "front",
                        "pipeline.garment_photo_2": "back",
                        "pipeline.garment_photo_3": "back",
                    },
                }
            ),
        },
        inputs={},
    )
    recs2 = runner2.run(
        task,
        wf,
        req2,
        [
            ("1.image", "m.png", model_p),
            ("2.image", "f.png", front_p),
            ("pipeline.garment_photo_2", "b1.png", back1_p),
            ("pipeline.garment_photo_3", "b2.png", back2_p),
        ],
    )
    # 3 distinct trimmed garment uploads (garment_01_trimmed.png, garment_02_trimmed.png, garment_03_trimmed.png)
    trimmed_uploads = [u for u in uploaded_names if u.endswith("_trimmed.png")]
    assert len(set(trimmed_uploads)) == 3
    inspect_recs = [r for r in recs2 if r["filename"].startswith("00_抠衣质检_")]
    assert len(inspect_recs) == 3
    assert len({r["filename"] for r in inspect_recs}) == 3
    assert all(r["task_id"] == "task-audit-1" for r in recs2)


def test_self_review_regressions_image_preprocess_back_detail_and_hero_anchor(tmp_path):
    # 1. RGBA transparent PNG flattens over white (never black) and trims transparent margins.
    rgba = Image.new("RGBA", (200, 260), (0, 0, 0, 0))
    for y in range(45, 215):
        for x in range(25, 175):
            rgba.putpixel((x, y), (40, 60, 110, 255))
    rgba_buf = io.BytesIO()
    rgba.save(rgba_buf, format="PNG")
    trimmed_rgba = auto_trim_ecommerce_borders(rgba_buf.getvalue())
    with Image.open(io.BytesIO(trimmed_rgba)) as out_im:
        assert out_im.format == "PNG"
        assert out_im.mode == "RGB"
        assert out_im.height < 220
        # Corner/margin pixels must never turn black (0, 0, 0).
        corner = out_im.getpixel((0, 0))
        assert corner[0] >= 35 and (corner == (255, 255, 255) or corner[2] >= 100)

    # 2. Untrimmed JPEG input still outputs valid PNG bytes (prevents save_output_file crash).
    jpeg_im = Image.new("RGB", (120, 120), (80, 110, 170))
    jpeg_buf = io.BytesIO()
    jpeg_im.save(jpeg_buf, format="JPEG", quality=92)
    trimmed_jpeg = auto_trim_ecommerce_borders(jpeg_buf.getvalue())
    assert trimmed_jpeg.startswith(b"\x89PNG\r\n\x1a\n")

    # 3. EXIF Orientation=6 (stored 160x100 landscape -> upright 100x160 portrait) transposes cleanly.
    raw_sensor = Image.new("RGB", (160, 100), (90, 140, 210))
    exif = raw_sensor.getexif()
    exif[274] = 6  # Rotate 90 CW -> 100x160 upright
    exif_buf = io.BytesIO()
    raw_sensor.save(exif_buf, format="JPEG", exif=exif.tobytes())
    upright_trimmed = auto_trim_ecommerce_borders(exif_buf.getvalue())
    with Image.open(io.BytesIO(upright_trimmed)) as up_im:
        assert up_im.size == (100, 160)
    portrait_matte = Image.new("L", (100, 160), 255)
    pm_buf = io.BytesIO()
    portrait_matte.save(pm_buf, format="PNG")
    comp_exif = composite_soft_alpha_on_white(exif_buf.getvalue(), pm_buf.getvalue())
    with Image.open(io.BytesIO(comp_exif)) as ce_im:
        assert ce_im.size == (100, 160)

    # 4. Palette P + tRNS transparency composites over white without dark fringe; 16-bit I;16 scales cleanly.
    pal = Image.new("P", (100, 100), 0)
    pal.putpalette([0, 0, 0, 100, 150, 220] + [0] * (768 - 6))
    for y in range(25, 75):
        for x in range(25, 75):
            pal.putpixel((x, y), 1)
    pal_buf = io.BytesIO()
    pal.save(pal_buf, format="PNG", transparency=0)
    pal_trimmed = auto_trim_ecommerce_borders(pal_buf.getvalue())
    with Image.open(io.BytesIO(pal_trimmed)) as pt_im:
        assert pt_im.mode == "RGB"
        assert pt_im.getpixel((0, 0)) == (255, 255, 255) or pt_im.getpixel((0, 0)) == (100, 150, 220)

    im16 = Image.new("I;16", (100, 100), 128 * 256)
    im16_buf = io.BytesIO()
    im16.save(im16_buf, format="PNG")
    trimmed_16 = auto_trim_ecommerce_borders(im16_buf.getvalue())
    with Image.open(io.BytesIO(trimmed_16)) as t16_im:
        mid = t16_im.getpixel((50, 50))
        assert 115 <= mid[0] <= 140

    # 5. Back-tagged garment + detail close-up (no front garment) retains back garment on front shot;
    #    single-shot hero_1 never uploads colabone_hero_master.png or emits misleading follow-up log.
    raw = json.loads((ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    catalog = ComfyCatalog.model_validate(raw)
    wf = next(t.model_dump() for t in catalog.templates if t.id == "fashion-tryon-h3")
    store = ColabOneComfyWorkflowStore(tmp_path / "store3", builtin_templates=catalog.templates)

    model_p = tmp_path / "m.png"
    back_jpg = tmp_path / "back.jpg"
    detail_p = tmp_path / "detail.png"
    model_p.write_bytes(_make_png(96, 128, (220, 200, 190)))
    back_jpg.write_bytes(jpeg_buf.getvalue())
    detail_p.write_bytes(_make_png(96, 96, (140, 180, 230)))

    uploaded_names: list[str] = []
    prompts_seen: list[dict] = []
    notes_seen: list[str] = []

    comfy = MagicMock()
    comfy.runtime = SimpleNamespace(
        gpu_info=lambda: {"name": "NVIDIA L4", "total_mib": 24576},
        http=MagicMock(),
    )
    comfy.store = store
    comfy.packs = MagicMock()
    comfy.base_url = "http://127.0.0.1:8188"
    comfy.is_running.return_value = True
    comfy._upload_input_file.side_effect = lambda fn, fp: (uploaded_names.append(fn) or fn)

    def _fake_exec(t, wf_arg, prompt_payload, **kwargs):
        prompts_seen.append(prompt_payload)
        out_dir = Path(kwargs["output_dir"])
        out_p = out_dir / f"gen_{len(prompts_seen)}.png"
        out_p.write_bytes(_make_png(96, 128, (180, 190, 200)))
        return [{"path": str(out_p), "filename": out_p.name, "prompt_id": f"p{len(prompts_seen)}"}]

    comfy._execute_single_prompt.side_effect = _fake_exec
    task = SimpleNamespace(
        id="task-self-review-1",
        progress=0.0,
        outputs=[],
        lock=None,
        check=lambda: None,
        note=lambda msg, level="info": notes_seen.append(str(msg)),
    )
    runner = ColabOneFashionTryOnRunner(comfy)
    req = WorkflowRunRequest(
        workflow_id="fashion-tryon-h3",
        overrides={
            "pipeline.shot_pack": "hero_1",
            "pipeline.garment_cleanup": "crop_only",
            "pipeline.save_cleaned_garment": True,
            "pipeline.anchor_hero_shot": True,
            "pipeline.garment_photo_roles": json.dumps(
                {
                    "order": ["2.image", "pipeline.garment_photo_2"],
                    "roles": {"2.image": "back", "pipeline.garment_photo_2": "detail"},
                }
            ),
        },
        inputs={},
    )
    recs = runner.run(
        task,
        wf,
        req,
        [
            ("1.image", "m.png", model_p),
            ("2.image", "back.jpg", back_jpg),
            ("pipeline.garment_photo_2", "detail.png", detail_p),
        ],
    )
    # JPEG back garment trimmed & saved cleanly as PNG inspection cutout without crashing.
    assert any(r["filename"].startswith("00_抠衣质检_") for r in recs)
    # Front shot still received model + trimmed back garment + detail close-up (3 LoadImage refs).
    shot_graph = prompts_seen[0]
    load_imgs = [
        node["inputs"]["image"]
        for node in shot_graph.values()
        if node.get("class_type") == "LoadImage"
    ]
    assert len(load_imgs) == 3
    assert any("garment_01_trimmed.png" in name for name in load_imgs)
    assert any("detail.png" in name for name in load_imgs)
    # Single-shot hero_1 did NOT waste a colabone_hero_master.png upload or emit anchor log.
    assert "colabone_hero_master.png" not in uploaded_names
    assert not any("视觉锚点" in n for n in notes_seen)

    # 6. Custom mode starting with detail (pose_5) + back (pose_6) does NOT anchor pose_6 to waist-up detail.
    uploaded_names.clear()
    prompts_seen.clear()
    notes_seen.clear()
    req_custom = WorkflowRunRequest(
        workflow_id="fashion-tryon-h3",
        overrides={
            "pipeline.shot_pack": "custom",
            "pipeline.pose_1_enabled": False,
            "pipeline.pose_2_enabled": False,
            "pipeline.pose_3_enabled": False,
            "pipeline.pose_4_enabled": False,
            "pipeline.pose_5_enabled": True,
            "pipeline.pose_6_enabled": True,
            "pipeline.garment_cleanup": "off",
            "pipeline.anchor_hero_shot": True,
        },
        inputs={},
    )
    runner.run(
        task,
        wf,
        req_custom,
        [
            ("1.image", "m.png", model_p),
            ("2.image", "back.jpg", back_jpg),
        ],
    )
    assert len(prompts_seen) == 2
    assert "colabone_hero_master.png" not in uploaded_names
    assert not any("视觉锚点" in n for n in notes_seen)

    # 7. Transparent RGBA model & detail close-up PNGs normalize to upright opaque RGB on white
    #    before uploading to ComfyUI LoadImage (preventing black transparent backgrounds), while
    #    standard opaque RGB images pass straight through untouched.
    rgba_model = tmp_path / "cutout_model.png"
    rgba_detail = tmp_path / "cutout_detail.png"
    for p, fg_rgb in ((rgba_model, (210, 175, 160, 255)), (rgba_detail, (130, 180, 235, 255))):
        im = Image.new("RGBA", (80, 100), (0, 0, 0, 0))
        for y in range(20, 80):
            for x in range(20, 60):
                im.putpixel((x, y), fg_rgb)
        im.save(p, format="PNG")

    uploaded_payloads: list[tuple[str, bytes]] = []
    comfy_rgba = MagicMock()
    comfy_rgba.runtime = SimpleNamespace(
        gpu_info=lambda: {"name": "NVIDIA L4", "total_mib": 24576},
        http=MagicMock(),
    )
    comfy_rgba.store = store
    comfy_rgba.packs = MagicMock()
    comfy_rgba.base_url = "http://127.0.0.1:8188"
    comfy_rgba.is_running.return_value = True
    comfy_rgba._upload_input_file.side_effect = lambda fn, fp: (
        uploaded_payloads.append((fn, Path(fp).read_bytes())) or fn
    )
    comfy_rgba._execute_single_prompt.side_effect = _fake_exec
    runner_rgba = ColabOneFashionTryOnRunner(comfy_rgba)
    runner_rgba.run(
        task,
        wf,
        WorkflowRunRequest(
            workflow_id="fashion-tryon-h3",
            overrides={
                "pipeline.shot_pack": "hero_1",
                "pipeline.garment_cleanup": "off",
                "pipeline.garment_photo_roles": json.dumps(
                    {"order": ["2.image"], "roles": {"2.image": "detail"}}
                ),
            },
            inputs={},
        ),
        [
            ("1.image", "cutout_model.png", rgba_model),
            ("2.image", "cutout_detail.png", rgba_detail),
        ],
    )
    assert len(uploaded_payloads) == 2
    for fname, raw_up in uploaded_payloads:
        assert fname.endswith("_rgb.png")
        with Image.open(io.BytesIO(raw_up)) as up_im:
            assert up_im.mode == "RGB"
            assert up_im.getpixel((0, 0)) == (255, 255, 255)


def test_white_garment_and_thin_strap_dress_preserved_in_crop_only_and_rmbg3(tmp_path):
    # 1. White garment (246, 246, 246) on pure-white (255, 255, 255) background:
    #    crop_only trims only the pure-white outer margin and keeps 100% of the white dress intact.
    white_dress = Image.new("RGB", (300, 300), (255, 255, 255))
    for y in range(24, 276):
        for x in range(70, 230):
            white_dress.putpixel((x, y), (246, 246, 246))
    wd_buf = io.BytesIO()
    white_dress.save(wd_buf, format="PNG")
    wd_trimmed = auto_trim_ecommerce_borders(wd_buf.getvalue())
    with Image.open(io.BytesIO(wd_trimmed)) as out_wd:
        # Must preserve the full 252px vertical dress span (y=24..276) and 160px width (x=70..230).
        assert out_wd.height >= 252
        assert out_wd.width >= 160

    # 2. Thin-strap dress (two 2px dark straps starting at y=10 on a 400x300 white canvas):
    #    full-width border inspection without coarse subsampling stops immediately at y=10.
    strap_im = Image.new("RGB", (400, 300), (255, 255, 255))
    for y in range(10, 110):
        for x in (140, 141, 258, 259):
            strap_im.putpixel((x, y), (50, 50, 65))
    for y in range(110, 280):
        for x in range(110, 290):
            strap_im.putpixel((x, y), (90, 120, 190))
    st_buf = io.BytesIO()
    strap_im.save(st_buf, format="PNG")
    st_trimmed = auto_trim_ecommerce_borders(st_buf.getvalue())
    with Image.open(io.BytesIO(st_trimmed)) as out_st:
        # Top trim must NOT slice into the 2px straps starting at y=10 (height >= 270).
        assert out_st.height >= 270
        # Verify strap pixels survive near the top of the trimmed image.
        top_band = [
            out_st.getpixel((x, y))
            for y in range(min(15, out_st.height))
            for x in range(out_st.width)
        ]
        assert any(px[0] < 100 and px[1] < 100 for px in top_band)

    # 3. In rmbg3 mode, _clean_garment_image SKIPS auto_trim_ecommerce_borders completely and
    #    uploads the full upright RGB image (preserving full 400x300 size) to RMBG 3.0.
    strap_path = tmp_path / "thin_strap.png"
    strap_path.write_bytes(st_buf.getvalue())

    uploaded_sizes: list[tuple[int, int]] = []
    comfy = MagicMock()
    comfy.runtime = SimpleNamespace(http=MagicMock(), gpu_info=lambda: {"total_mib": 24576})
    comfy.io_out_dir = None

    def _capture_upload(fname, fpath):
        with Image.open(fpath) as up_im:
            uploaded_sizes.append(up_im.size)
        return fname

    def _fake_rmbg_exec(task, wf_arg, prompt_payload, **kwargs):
        out_p = Path(kwargs["output_dir"]) / "matte.png"
        Image.new("L", (400, 300), 255).save(out_p, format="PNG")
        return [{"path": str(out_p), "filename": out_p.name, "prompt_id": "rmbg-1"}]

    comfy._upload_input_file.side_effect = _capture_upload
    comfy._execute_single_prompt.side_effect = _fake_rmbg_exec
    runner = ColabOneFashionTryOnRunner(comfy)
    task = SimpleNamespace(check=lambda: None, note=lambda msg, level="info": None)
    runner._clean_garment_image(
        task,
        {"id": "fashion-tryon-h3", "name": "Fashion"},
        src_path=strap_path,
        label="服装正面主图",
        file_stem="garment_01",
        work_dir=tmp_path,
        garment_cleanup="rmbg3",
        garment_notes="",
        filenames={"rmbg3_dit": "d.sft", "rmbg3_clip": "c.sft", "rmbg3_vae": "v.sft"},
        seed=42,
        progress_range=(0.24, 0.37),
    )
    assert uploaded_sizes == [(400, 300)]


def test_upfront_input_validation_rejects_audio_and_corrupt_images_before_ensure_ready(tmp_path):
    from server.fashion_tryon import _prepare_opaque_rgb_upload

    raw = json.loads((ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    catalog = ComfyCatalog.model_validate(raw)
    wf = next(t.model_dump() for t in catalog.templates if t.id == "fashion-tryon-h3")

    good_png = tmp_path / "good.png"
    good_png.write_bytes(_make_png(64, 64, (120, 150, 200)))
    audio_wav = tmp_path / "voice.wav"
    audio_wav.write_bytes(b"RIFF\x24\x00\x00\x00WAVEfmt ")
    corrupt_png = tmp_path / "corrupt.png"
    corrupt_png.write_bytes(b"\x89PNG\r\n\x1a\nNOT_A_REAL_PNG_BODY")

    comfy = MagicMock()
    comfy.runtime = SimpleNamespace(http=MagicMock(), gpu_info=lambda: {"total_mib": 24576})
    runner = ColabOneFashionTryOnRunner(comfy)
    ensure_ready_calls: list[str] = []
    runner.ensure_ready = lambda *a, **kw: ensure_ready_calls.append("called")  # type: ignore[method-assign]

    task = SimpleNamespace(
        id="t-val",
        progress=0.0,
        outputs=[],
        lock=None,
        check=lambda: None,
        note=lambda msg, level="info": None,
    )
    req = WorkflowRunRequest(workflow_id="fashion-tryon-h3", overrides={}, inputs={})

    # Audio file in model slot rejected immediately before ensure_ready.
    with pytest.raises(ValueError, match="仅支持 PNG、JPEG 或 WebP"):
        runner.run(
            task,
            wf,
            req,
            [("1.image", "voice.wav", audio_wav), ("2.image", "good.png", good_png)],
        )
    assert ensure_ready_calls == []

    # Corrupt PNG in garment slot rejected immediately before ensure_ready.
    with pytest.raises(ValueError, match="损坏|无法解码"):
        runner.run(
            task,
            wf,
            req,
            [("1.image", "good.png", good_png), ("2.image", "corrupt.png", corrupt_png)],
        )
    assert ensure_ready_calls == []

    # Corrupt scene_ref_photo rejected immediately before ensure_ready.
    with pytest.raises(ValueError, match="损坏|无法解码"):
        runner.run(
            task,
            wf,
            req,
            [
                ("1.image", "good.png", good_png),
                ("2.image", "good.png", good_png),
                ("pipeline.scene_ref_photo", "corrupt.png", corrupt_png),
            ],
        )
    assert ensure_ready_calls == []

    # _prepare_opaque_rgb_upload must raise ValueError on corrupt images instead of passing raw bytes through.
    with pytest.raises(ValueError, match="损坏|无法解码"):
        _prepare_opaque_rgb_upload("corrupt.png", corrupt_png, stem="bad", work_dir=tmp_path)


def test_fashion_tryon_pack_routing_vram_eviction_finally_matte_fallback_io_cleanup_and_prompt_caps(
    tmp_path,
):
    raw = json.loads((ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    catalog = ComfyCatalog.model_validate(raw)
    wf = next(t.model_dump() for t in catalog.templates if t.id == "fashion-tryon-h3")
    store = ColabOneComfyWorkflowStore(tmp_path / "store_hard", builtin_templates=catalog.templates)

    # 1. ensure_ready routes through packs.ensure_pack(), restarts ComfyUI when pack/pip/sig changed,
    #    and calls _free_comfy_vram when already running with unchanged pack.
    events: list[str] = []
    comfy_r = MagicMock()
    comfy_r.base_url = "http://127.0.0.1:8188"
    comfy_r.is_running.return_value = True
    comfy_r._loaded_pack_signature = "old-dh-sig"
    comfy_r.lock = None
    comfy_r.packs = SimpleNamespace(
        check_preflight=lambda _wf: events.append("preflight"),
        ensure_pack=lambda task, wf_arg, hf_token="", progress_range=None: (
            events.append(f"ensure_pack:{len(wf_arg.get('models') or [])}") or True
        ),
        needs_pip_restart=lambda: False,
        active_pack_signature="new-fashion-sig",
    )
    comfy_r.stop_server.side_effect = lambda: events.append("stop_server")
    comfy_r.start_server.side_effect = lambda task: events.append("start_server")
    comfy_r.mark_native_pack_ready.side_effect = lambda wid: events.append(f"mark:{wid}")
    comfy_r.runtime = SimpleNamespace(
        gpu_info=lambda: {"total_mib": 24576},
        http=SimpleNamespace(post=lambda *a, **kw: (events.append("free") or SimpleNamespace(status_code=200))),
    )
    t_note: list[tuple[str, str]] = []
    task = SimpleNamespace(
        id="t-hard-1",
        progress=0.0,
        outputs=[],
        lock=None,
        check=lambda: None,
        note=lambda msg, level="info": t_note.append((str(msg), str(level))),
    )
    runner_r = ColabOneFashionTryOnRunner(comfy_r)
    runner_r.ensure_ready(task, wf, overrides={"pipeline.garment_cleanup": "crop_only"})
    assert events == ["preflight", "ensure_pack:3", "stop_server", "start_server", "mark:fashion-tryon-h3"]

    # Legacy stub lacking ensure_pack falls back to _ensure_models and calls /free on already-running server.
    events.clear()
    comfy_legacy = MagicMock()
    comfy_legacy.base_url = "http://127.0.0.1:8188"
    comfy_legacy.is_running.return_value = True
    comfy_legacy._loaded_pack_signature = ""
    comfy_legacy.lock = None
    comfy_legacy.packs = SimpleNamespace(
        check_preflight=lambda _wf: events.append("preflight"),
        _ensure_models=lambda task, models, hf_token="", progress_range=None: events.append(
            f"ensure_models:{len(models)}"
        ),
    )
    comfy_legacy.runtime = SimpleNamespace(
        gpu_info=lambda: {"total_mib": 24576},
        http=SimpleNamespace(post=lambda *a, **kw: (events.append("free") or SimpleNamespace(status_code=200))),
    )
    comfy_legacy.mark_native_pack_ready.side_effect = lambda wid: events.append(f"mark:{wid}")
    ColabOneFashionTryOnRunner(comfy_legacy).ensure_ready(
        task, wf, overrides={"pipeline.garment_cleanup": "crop_only"}
    )
    assert events == ["preflight", "ensure_models:3", "free", "mark:fashion-tryon-h3"]

    # 2. _free_comfy_vram logs warnings on non-2xx HTTP status and on exceptions.
    warn_notes: list[tuple[str, str]] = []
    warn_task = SimpleNamespace(
        check=lambda: None,
        note=lambda msg, level="info": warn_notes.append((str(msg), str(level))),
    )
    comfy_err = MagicMock()
    comfy_err.base_url = "http://127.0.0.1:8188"
    comfy_err.runtime = SimpleNamespace(
        http=SimpleNamespace(post=lambda *a, **kw: SimpleNamespace(status_code=503))
    )
    ColabOneFashionTryOnRunner(comfy_err)._free_comfy_vram(warn_task)
    assert any(lvl == "warning" and "503" in msg for msg, lvl in warn_notes)

    warn_notes.clear()
    comfy_err.runtime = SimpleNamespace(
        http=SimpleNamespace(post=lambda *a, **kw: (_ for _ in ()).throw(OSError("conn refused")))
    )
    ColabOneFashionTryOnRunner(comfy_err)._free_comfy_vram(warn_task)
    assert any(lvl == "warning" and "conn refused" in msg for msg, lvl in warn_notes)

    # 3. Stage 1 RMBG 3.0 calls /free BEFORE Stage 1 AND STILL calls /free in finally on failure.
    model_p = tmp_path / "m.png"
    garment_p = tmp_path / "g.png"
    model_p.write_bytes(_make_png(96, 128, (220, 200, 190)))
    garment_p.write_bytes(_make_png(96, 128, (100, 140, 210)))

    stage1_trace: list[str] = []
    comfy_abort = MagicMock()
    comfy_abort.base_url = "http://127.0.0.1:8188"
    comfy_abort.store = store
    comfy_abort.io_out_dir = None
    comfy_abort.is_running.return_value = True
    comfy_abort._loaded_pack_signature = ""
    comfy_abort.lock = None
    comfy_abort.packs = SimpleNamespace(
        check_preflight=lambda _wf: None,
        ensure_pack=lambda *a, **kw: False,
        needs_pip_restart=lambda: False,
        active_pack_signature="",
    )
    comfy_abort.runtime = SimpleNamespace(
        gpu_info=lambda: {"total_mib": 24576},
        http=SimpleNamespace(
            post=lambda url, **kw: (stage1_trace.append("free") or SimpleNamespace(status_code=200))
        ),
    )
    comfy_abort._upload_input_file.side_effect = lambda fn, fp: fn

    def _boom_rmbg(*a, **kw):
        stage1_trace.append("rmbg_boom")
        raise RuntimeError("simulated RMBG OOM")

    comfy_abort._execute_single_prompt.side_effect = _boom_rmbg
    with pytest.raises(RuntimeError, match="simulated RMBG OOM"):
        ColabOneFashionTryOnRunner(comfy_abort).run(
            task,
            wf,
            WorkflowRunRequest(
                workflow_id="fashion-tryon-h3",
                overrides={"pipeline.garment_cleanup": "rmbg3", "pipeline.shot_pack": "hero_1"},
                inputs={},
            ),
            [("1.image", "m.png", model_p), ("2.image", "g.png", garment_p)],
        )
    # Must have freed BEFORE rmbg_boom AND in finally AFTER rmbg_boom!
    boom_idx = stage1_trace.index("rmbg_boom")
    assert "free" in stage1_trace[:boom_idx]
    assert "free" in stage1_trace[boom_idx + 1 :]

    # 4. Corrupt RMBG matte falls back gracefully to prepared image AND io_out_dir files are cleaned up
    #    (while symlinks / outside files are never touched).
    io_out = tmp_path / "io_out"
    io_out.mkdir()
    outside_target = tmp_path / "keep_outside.png"
    outside_target.write_bytes(_make_png(32, 32, (10, 20, 30)))
    sub_dir = io_out / "sub"
    sub_dir.mkdir()
    rmbg_io_file = sub_dir / "rmbg_matte_temp.png"
    rmbg_io_file.write_bytes(b"corrupt-matte-bytes")
    shot_io_file = io_out / "shot_01_temp.png"
    shot_io_file.write_bytes(_make_png(96, 128, (150, 160, 170)))
    sym_io_file = io_out / "sym_outside.png"
    sym_io_file.symlink_to(outside_target)

    fallback_notes: list[tuple[str, str]] = []
    fb_task = SimpleNamespace(
        id="t-fb-1",
        progress=0.0,
        outputs=[],
        lock=None,
        check=lambda: None,
        note=lambda msg, level="info": fallback_notes.append((str(msg), str(level))),
    )
    comfy_fb = MagicMock()
    comfy_fb.base_url = "http://127.0.0.1:8188"
    comfy_fb.store = store
    comfy_fb.io_out_dir = io_out
    comfy_fb.is_running.return_value = True
    comfy_fb._loaded_pack_signature = ""
    comfy_fb.lock = None
    comfy_fb.packs = SimpleNamespace(
        check_preflight=lambda _wf: None,
        ensure_pack=lambda *a, **kw: False,
        needs_pip_restart=lambda: False,
        active_pack_signature="",
    )
    comfy_fb.runtime = SimpleNamespace(
        gpu_info=lambda: {"total_mib": 24576},
        http=SimpleNamespace(post=lambda *a, **kw: SimpleNamespace(status_code=200)),
    )
    comfy_fb._upload_input_file.side_effect = lambda fn, fp: fn

    call_n = [0]

    def _exec_corrupt_matte_then_ok(t, wf_arg, prompt_payload, **kwargs):
        call_n[0] += 1
        out_dir = Path(kwargs["output_dir"])
        if call_n[0] == 1:
            corrupt_matte = out_dir / "bad_matte.png"
            corrupt_matte.write_bytes(b"not-a-valid-png")
            return [
                {
                    "path": str(corrupt_matte),
                    "filename": "rmbg_matte_temp.png",
                    "subfolder": "sub",
                    "type": "output",
                    "prompt_id": "p-rmbg",
                }
            ]
        gen_png = out_dir / "good_shot.png"
        gen_png.write_bytes(_make_png(96, 128, (150, 160, 170)))
        return [
            {
                "path": str(gen_png),
                "filename": "shot_01_temp.png",
                "subfolder": "",
                "type": "output",
                "prompt_id": "p-shot",
            },
            {
                "path": str(gen_png),
                "filename": "sym_outside.png",
                "subfolder": "",
                "type": "output",
                "prompt_id": "p-shot",
            },
        ]

    comfy_fb._execute_single_prompt.side_effect = _exec_corrupt_matte_then_ok
    recs_fb = ColabOneFashionTryOnRunner(comfy_fb).run(
        fb_task,
        wf,
        WorkflowRunRequest(
            workflow_id="fashion-tryon-h3",
            overrides={"pipeline.garment_cleanup": "rmbg3", "pipeline.shot_pack": "hero_1"},
            inputs={},
        ),
        [("1.image", "m.png", model_p), ("2.image", "g.png", garment_p)],
    )
    assert len(recs_fb) == 1
    assert any(lvl == "warning" and "遮罩图像异常" in msg for msg, lvl in fallback_notes)
    assert not rmbg_io_file.exists()
    assert not shot_io_file.exists()
    assert sym_io_file.is_symlink()
    assert outside_target.is_file()

    # 5. Prompt length caps (2000 chars) on garment_notes, custom_style_prompt, and custom_pose_prompts.
    huge_notes = "N" * 5000
    huge_style = "S" * 5000
    huge_pose = "P" * 5000
    capped_shots = plan_colabone_fashion_shots({"pipeline.custom_pose_prompts": huge_pose})
    assert len(capped_shots) == 1
    assert "P" * 2000 in capped_shots[0]["prompt"]
    assert "P" * 2001 not in capped_shots[0]["prompt"]

    rmbg_wf = build_rmbg3_garment_matte_prompt(
        "g.png",
        rmbg3_dit="d.sft",
        rmbg3_clip="c.sft",
        rmbg3_vae="v.sft",
        garment_notes=huge_notes,
    )
    rmbg_instruction = json.loads(rmbg_wf["5"]["inputs"]["prompt"])["edit_instruction"]
    assert "N" * 2000 in rmbg_instruction
    assert "N" * 2001 not in rmbg_instruction

    tryon_prompt = build_minimax_h3_tryon_prompt_text(
        shot=capped_shots[0],
        ref_roles=[(1, "model"), (2, "garment_front")],
        scene_preset="studio_light_gray",
        garment_notes=huge_notes,
        custom_style_prompt=huge_style,
    )
    assert "N" * 2000 in tryon_prompt and "N" * 2001 not in tryon_prompt
    assert "S" * 2000 in tryon_prompt and "S" * 2001 not in tryon_prompt
    assert "POSE & COMPOSITION:" in tryon_prompt


def test_builtin_30_pose_catalog_and_mixed_pose_shots_queue(tmp_path):
    assert len(COLABONE_FASHION_BUILTIN_POSES) == 30
    assert len(COLABONE_FASHION_POSE_BY_ID) == 30
    categories = {p["category"] for p in COLABONE_FASHION_BUILTIN_POSES}
    assert categories == {"front", "side", "back", "dynamic", "seated", "detail", "kids"}
    for p in COLABONE_FASHION_BUILTIN_POSES:
        assert p["id"].startswith("pose_")
        assert p["view"] in {"front", "side", "back", "detail"}
        assert p["label"].strip()
        assert p["prompt"].strip()

    # Kids 4-shot quick pack works cleanly.
    kids_shots = plan_colabone_fashion_shots({"pipeline.shot_pack": "kids_4"})
    assert [s["id"] for s in kids_shots] == ["pose_27", "pose_28", "pose_29", "pose_30"]

    # Explicitly empty structured queue raises clear error.
    with pytest.raises(ValueError, match="出图姿势清单不能为空"):
        plan_colabone_fashion_shots({COLABONE_FASHION_POSE_SHOTS_KEY: "[]"})

    # Mixed queue: preset + reference (isolated) + reference (borrow lighting, back view) + text.
    ref1 = tmp_path / "street_walk.png"
    ref2 = tmp_path / "back_look.png"
    ref1.write_bytes(_make_png(48, 64, (110, 120, 130)))
    ref2.write_bytes(_make_png(48, 64, (130, 140, 150)))
    input_map = {
        "pipeline.pose_ref_1": ("street_walk.png", ref1),
        "pipeline.pose_ref_2": ("back_look.png", ref2),
    }
    mixed_spec = [
        {"kind": "preset", "preset_id": "pose_27"},
        {
            "kind": "reference",
            "slot_key": "pipeline.pose_ref_1",
            "view": "front",
            "note": "自然微笑看镜头",
            "borrow_scene_lighting": False,
        },
        {
            "kind": "reference",
            "slot_key": "pipeline.pose_ref_2",
            "view": "back",
            "note": "",
            "borrow_scene_lighting": True,
        },
        {
            "kind": "text",
            "prompt": "双手轻提裙摆微微歪头笑",
            "view": "front",
        },
        # Missing reference slot should be pruned gracefully when input_map is supplied.
        {
            "kind": "reference",
            "slot_key": "pipeline.pose_ref_6",
            "view": "auto",
            "borrow_scene_lighting": False,
        },
    ]
    planned = plan_colabone_fashion_shots(
        {COLABONE_FASHION_POSE_SHOTS_KEY: json.dumps(mixed_spec, ensure_ascii=False)},
        input_map=input_map,
    )
    assert len(planned) == 4
    assert planned[0]["kind"] == "preset" and planned[0]["filename_stem"].startswith("01_")
    assert planned[1]["kind"] == "reference" and planned[1]["pose_ref_slot"] == "pipeline.pose_ref_1"
    assert planned[1]["borrow_scene_lighting"] is False
    assert "自然微笑看镜头" in planned[1]["prompt"]
    assert planned[2]["kind"] == "reference" and planned[2]["view"] == "back"
    assert planned[2]["borrow_scene_lighting"] is True
    assert planned[3]["kind"] == "text" and planned[3]["filename_stem"].startswith("04_")

    # Hard cap at 12 shots.
    too_many = [{"kind": "preset", "preset_id": f"pose_{i}"} for i in range(1, 20)]
    capped = plan_colabone_fashion_shots(
        {COLABONE_FASHION_POSE_SHOTS_KEY: json.dumps(too_many)}
    )
    assert len(capped) == COLABONE_FASHION_MAX_POSE_SHOTS


def test_pose_ref_prompt_isolation_vs_lighting_borrow_and_runner_15_ref_budget(tmp_path):
    # Prompt isolation vs lighting borrowing clauses.
    shot_iso = {
        "view": "front",
        "prompt": "Match pose reference.",
    }
    p_iso = build_minimax_h3_tryon_prompt_text(
        shot=shot_iso,
        ref_roles=[(1, "hero", "front"), (2, "model", "front"), (3, "garment_front", "front"), (4, "pose_ref", "front")],
        scene_preset="studio_light_gray",
    )
    assert "POSE & FRAMING REFERENCE ONLY (HARD ISOLATION)" in p_iso
    assert "<Picture 4>" in p_iso
    assert "UNIFIED SCENE & LIGHTING:" in p_iso
    assert "COMPLETELY IGNORE AND DISCARD" in p_iso

    p_light = build_minimax_h3_tryon_prompt_text(
        shot={"view": "back", "prompt": "Match back pose and mood."},
        ref_roles=[(1, "hero", "front"), (2, "model", "front"), (3, "garment_back", "back"), (4, "pose_ref_lighting", "back")],
        scene_preset="studio_light_gray",
    )
    assert "POSE, FRAMING & ATMOSPHERE REFERENCE:" in p_light
    assert "BACK VIEW GARMENT FIDELITY" in p_light
    assert "lighting mood and scene ambiance to adapt naturally to <Picture 4>" in p_light
    assert "UNIFIED SCENE & LIGHTING:" not in p_light

    # Full runner test with max refs (6 models + 6 garments + 1 scene + 1 pose_ref + 1 hero = 15 <= 16).
    io_in = tmp_path / "io_in"
    io_out = tmp_path / "io_out"
    io_in.mkdir()
    io_out.mkdir()
    store = ColabOneComfyWorkflowStore(tmp_path / "store")
    raw_cat = json.loads((ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    wf = next(t for t in raw_cat["templates"] if t["id"] == "fashion-tryon-h3")

    resolved_inputs: list[tuple[str, str, Path]] = []
    for i, mkey in enumerate(COLABONE_FASHION_MODEL_SLOT_KEYS, 1):
        mp = tmp_path / f"m{i}.png"
        mp.write_bytes(_make_png(40, 60, (200, 180, 170)))
        resolved_inputs.append((mkey, f"m{i}.png", mp))
    for i, gkey in enumerate(COLABONE_FASHION_GARMENT_SLOT_KEYS, 1):
        gp = tmp_path / f"g{i}.png"
        gp.write_bytes(_make_png(40, 60, (120, 150, 210)))
        resolved_inputs.append((gkey, f"g{i}.png", gp))
    sp = tmp_path / "scene.png"
    sp.write_bytes(_make_png(40, 60, (235, 235, 235)))
    resolved_inputs.append(("pipeline.scene_ref_photo", "scene.png", sp))

    pr1 = tmp_path / "pose1.png"
    pr2 = tmp_path / "pose2.png"
    pr1.write_bytes(_make_png(40, 60, (90, 90, 100)))
    pr2.write_bytes(_make_png(40, 60, (80, 80, 95)))
    resolved_inputs.append(("pipeline.pose_ref_1", "pose1.png", pr1))
    resolved_inputs.append(("pipeline.pose_ref_2", "pose2.png", pr2))

    garment_roles = json.dumps(
        {
            "order": list(COLABONE_FASHION_GARMENT_SLOT_KEYS),
            "roles": {
                "2.image": "front",
                "pipeline.garment_photo_2": "front",
                "pipeline.garment_photo_3": "side",
                "pipeline.garment_photo_4": "flatlay",
                "pipeline.garment_photo_5": "detail",
                "pipeline.garment_photo_6": "back",
            },
        }
    )
    pose_shots = json.dumps(
        [
            {"kind": "reference", "slot_key": "pipeline.pose_ref_1", "view": "front", "borrow_scene_lighting": True},
            {"kind": "preset", "preset_id": "pose_1"},
            {"kind": "reference", "slot_key": "pipeline.pose_ref_2", "view": "back", "borrow_scene_lighting": False},
        ]
    )

    captured_graphs: list[dict] = []
    comfy = MagicMock()
    comfy.base_url = "http://127.0.0.1:8188"
    comfy.store = store
    comfy.io_in_dir = io_in
    comfy.io_out_dir = io_out
    comfy.is_running.return_value = True
    comfy._loaded_pack_signature = ""
    comfy.lock = None
    comfy.packs = SimpleNamespace(
        check_preflight=lambda _wf: None,
        ensure_pack=lambda *a, **kw: False,
        needs_pip_restart=lambda: False,
        active_pack_signature="",
    )
    comfy.runtime = SimpleNamespace(
        gpu_info=lambda: {"total_mib": 24576},
        http=SimpleNamespace(post=lambda *a, **kw: SimpleNamespace(status_code=200)),
    )

    def _upload(fn, fp):
        dst = io_in / fn
        dst.write_bytes(Path(fp).read_bytes())
        return fn

    comfy._upload_input_file.side_effect = _upload

    def _exec(t, wf_arg, prompt_payload, **kwargs):
        captured_graphs.append(prompt_payload)
        out_dir = Path(kwargs["output_dir"])
        out_png = out_dir / f"gen_{len(captured_graphs)}.png"
        out_png.write_bytes(_make_png(64, 96, (170, 175, 180)))
        return [{"path": str(out_png), "filename": out_png.name, "subfolder": "", "type": "output", "prompt_id": f"p{len(captured_graphs)}"}]

    comfy._execute_single_prompt.side_effect = _exec
    task = SimpleNamespace(
        id="t-pose-refs",
        progress=0.0,
        outputs=[],
        lock=None,
        check=lambda: None,
        note=lambda msg, level="info": None,
    )

    recs = ColabOneFashionTryOnRunner(comfy).run(
        task,
        wf,
        WorkflowRunRequest(
            workflow_id="fashion-tryon-h3",
            overrides={
                "pipeline.garment_cleanup": "off",
                "pipeline.garment_photo_roles": garment_roles,
                COLABONE_FASHION_POSE_SHOTS_KEY: pose_shots,
                "pipeline.anchor_hero_shot": True,
            },
            inputs={},
        ),
        resolved_inputs,
    )
    assert len(recs) == 3
    assert len(captured_graphs) == 3
    # Shot 1 had borrow_scene_lighting=True -> should NOT lock Hero Master for Shot 2.
    shot1_cond = captured_graphs[0]["30"]["inputs"]
    shot2_cond = captured_graphs[1]["30"]["inputs"]
    shot3_cond = captured_graphs[2]["30"]["inputs"]
    assert "POSE, FRAMING & ATMOSPHERE REFERENCE:" in shot1_cond["prompt"]
    assert "editorial fashion lookbook photograph" in shot1_cond["prompt"]
    assert "colabone_hero_master.png" not in json.dumps(captured_graphs[1])
    # Shot 2 (pose_1, front, no lighting borrow) anchors Hero Master for Shot 3 (back pose_ref_2).
    assert "colabone_hero_master.png" in json.dumps(captured_graphs[2])
    assert "POSE & FRAMING REFERENCE ONLY (HARD ISOLATION)" in shot3_cond["prompt"]
    assert "BACK VIEW GARMENT FIDELITY" in shot3_cond["prompt"]
    # Verify ref count never exceeds MiniMax-H3-Image's 16-ref limit.
    for graph in captured_graphs:
        ref_nodes = [k for k, v in graph.items() if v.get("class_type") == "LoadImage"]
        assert len(ref_nodes) <= 16
    # All temporary uploaded pose refs and hero anchor must be cleaned up.
    assert list(io_in.iterdir()) == []


def test_pose_view_word_boundaries_and_middot_label_sanitization(tmp_path):
    from server.fashion_tryon import (
        COLABONE_FASHION_POSE_SHOTS_KEY,
        _infer_pose_view_from_text,
    )

    # Word-boundary view inference must not misclassify unrelated substrings like rearrange/background/outside.
    assert _infer_pose_view_from_text("rearrange_pose.png") == "front"
    assert _infer_pose_view_from_text("white_background.png") == "front"
    assert _infer_pose_view_from_text("outside_walk.png") == "front"
    assert _infer_pose_view_from_text("pose_back.png") == "back"
    assert _infer_pose_view_from_text("studio_rear_view.png") == "back"
    assert _infer_pose_view_from_text("side_profile.png") == "side"
    assert _infer_pose_view_from_text("collar_closeup.png") == "detail"

    # Filenames containing middots must be sanitized in shot labels so progress i18n splits stay unambiguous.
    ref_p = tmp_path / "杂志 · 街拍.png"
    ref_p.write_bytes(_make_png(64, 64, (120, 130, 140)))
    shots = plan_colabone_fashion_shots(
        {
            COLABONE_FASHION_POSE_SHOTS_KEY: json.dumps(
                [
                    {
                        "kind": "reference",
                        "slot_key": "pipeline.pose_ref_1",
                        "view": "auto",
                        "note": "",
                        "borrow_scene_lighting": False,
                    }
                ]
            )
        },
        input_map={"pipeline.pose_ref_1": ("杂志 · 街拍.png", ref_p)},
    )
    assert len(shots) == 1
    assert shots[0]["label"] == "参考姿势 · 杂志 街拍"
    assert shots[0]["label"].count("·") == 1

    # Non-string garbage fields inside pose_shots JSON must never stringify into prompt text.
    sanitized_shots = plan_colabone_fashion_shots(
        {
            COLABONE_FASHION_POSE_SHOTS_KEY: json.dumps(
                [
                    {"kind": "text", "prompt": ["should", "ignore"], "view": 123},
                    {
                        "kind": "reference",
                        "slot_key": "pipeline.pose_ref_1",
                        "view": ["bad"],
                        "note": {"bad": True},
                        "borrow_scene_lighting": False,
                    },
                ]
            )
        },
        input_map={"pipeline.pose_ref_1": ("clean_ref.png", ref_p)},
    )
    assert len(sanitized_shots) == 1
    assert sanitized_shots[0]["pose_note"] == ""
    assert sanitized_shots[0]["view"] == "auto"
    assert "bad" not in sanitized_shots[0]["prompt"]




