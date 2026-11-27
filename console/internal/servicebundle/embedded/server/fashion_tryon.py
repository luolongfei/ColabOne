"""Remote multi-pose AI fashion try-on pipeline for ComfyUI v0.45+.

Runs exclusively on the remote GPU runtime:
1. Automatically resolves GPU VRAM precision (80-96 GB -> full BF16; 24 GB -> INT8 quantized).
2. Downloads pinned Hugging Face weights with exact SHA-256 verification via ColabOneComfyPackManager.
3. Optionally trims e-commerce margins and runs native ComfyUI Bria RMBG 3.0 soft-alpha matting
   to preserve translucent tulle/feathers while isolating the garment from hangers, backdrops,
   watermarks, or third-party model photos, then releases VRAM before generation.
4. Generates a Hero Master front look first via native ComfyUI MiniMax-H3-Image multi-reference
   conditioning (up to 16 references, 6-step distilled CFG=1.0), then expands into a cohesive
   multi-pose lookbook (default 6 poses) anchored to the Hero Master, original model photos,
   garment detail close-ups, and optional back-view references.

Notice: Both MiniMax-H3-Image (MiniMax-H3-Image Community License) and Bria RMBG 3.0
(CC BY-NC 4.0) are non-commercial open-weight models. Strictly for personal/research use.
"""

from __future__ import annotations

import inspect
import io
import json
import math
import os
import re
import tempfile
import threading
import time
from contextlib import nullcontext
from pathlib import Path
from typing import Any
from unittest.mock import NonCallableMock

from common.comfyui import WorkflowRunRequest, format_colabone_duration_zh

_FASHION_ENSURE_STATE = threading.local()


def _runtime_http(runtime):
    if runtime is None:
        return None
    getter = getattr(runtime, "_http_session", None)
    if callable(getter):
        return getter()
    return getattr(runtime, "http", None)


def _has_real_free_memory(comfy: Any) -> bool:
    if isinstance(comfy, NonCallableMock):
        raw = getattr(comfy, "__dict__", {}).get("free_memory")
        if raw is None:
            return False
        if (
            isinstance(raw, NonCallableMock)
            and raw.side_effect is None
            and isinstance(raw.return_value, NonCallableMock)
        ):
            return False
        return callable(raw)
    return callable(getattr(comfy, "free_memory", None))

COLABONE_FASHION_BF16_THRESHOLD_MIB = 65_536  # >= 64 GiB (A100 80 GB / H100 / G4 96 GB)
COLABONE_FASHION_MIN_VRAM_INT8_MIB = 22_528  # 22 GiB floor for 24 GB L4 / 3090 / 4090
COLABONE_FASHION_MIN_VRAM_BF16_MIB = 65_536
COLABONE_FASHION_IMAGE_EXTENSIONS = frozenset({"png", "jpg", "jpeg", "webp"})
COLABONE_FASHION_MAX_IMAGE_PIXELS = 16_777_216  # 16M pixels (4096 x 4096)
COLABONE_FASHION_MAX_USER_PROMPT_CHARS = 2000

MINIMAX_H3_REPO = "Comfy-Org/MiniMax-H3-Image"
MINIMAX_H3_REVISION = "a70325ac34ca5c0c83df578bb5e02237460c1d8d"

BRIA_RMBG3_REPO = "briaai/RMBG-3.0-Comfy"
BRIA_RMBG3_REVISION = "63880aa074782b09e7ae17184960dfd6de70ab85"

COLABONE_FASHION_MODEL_FILES: dict[str, dict[str, Any]] = {
    "minimax_dit_bf16": {
        "repo": MINIMAX_H3_REPO,
        "revision": MINIMAX_H3_REVISION,
        "path": "diffusion_models/minimax_h3_image_pruned_bf16.safetensors",
        "target_dir": "diffusion_models",
        "size": 40234657162,
        "sha256": "a767f5325db019588b3491a93d80288d336c826a7aeb87bec67675e554367222",
    },
    "minimax_dit_int8": {
        "repo": MINIMAX_H3_REPO,
        "revision": MINIMAX_H3_REVISION,
        "path": "diffusion_models/minimax_h3_image_pruned_int8_convrot.safetensors",
        "target_dir": "diffusion_models",
        "size": 20979314802,
        "sha256": "83eb140f1ff95c068f8a6886e6d3f3d0d4ab2e07bb2fe6d2ef992a51146ee622",
    },
    "minimax_clip_int8": {
        "repo": MINIMAX_H3_REPO,
        "revision": MINIMAX_H3_REVISION,
        "path": "text_encoders/qwen3vl_32b_minimax_h3_int8_convrot.safetensors",
        "target_dir": "text_encoders",
        "size": 27141342152,
        "sha256": "bc2ced0fbea64757fa9acddccfc0b3f4819d1dcf1da6c124d690d368be283923",
    },
    "minimax_clip_nvfp4": {
        "repo": MINIMAX_H3_REPO,
        "revision": MINIMAX_H3_REVISION,
        "path": "text_encoders/qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors",
        "target_dir": "text_encoders",
        "size": 15687142551,
        "sha256": "35a88d51044231fe332301d7a62aa81e3f2cba62febeb446e2c1e3e0ef76f2c6",
    },
    "minimax_vae": {
        "repo": MINIMAX_H3_REPO,
        "revision": MINIMAX_H3_REVISION,
        "path": "vae/minimax_h3_image_vae_fp16.safetensors",
        "target_dir": "vae",
        "size": 1168505312,
        "sha256": "d80e9c2e5cfc9844869961ff5a58c9a9e52182df6bfb30b26018dd7f348721af",
    },
    "rmbg3_dit_bf16": {
        "repo": BRIA_RMBG3_REPO,
        "revision": BRIA_RMBG3_REVISION,
        "path": "diffusion_models/rmbg3_bf16.safetensors",
        "target_dir": "diffusion_models",
        "size": 16452934194,
        "sha256": "a5df048f799025510f29a743c0dca0d4f1362478ec00f6f7c86ee66d57b4e5d9",
    },
    "rmbg3_dit_int8": {
        "repo": BRIA_RMBG3_REPO,
        "revision": BRIA_RMBG3_REVISION,
        "path": "diffusion_models/rmbg3_int8_convrot.safetensors",
        "target_dir": "diffusion_models",
        "size": 8408562474,
        "sha256": "bc29b9a87ed378596c5ca5bc0c5bdfaa8aabc8415d33f3c767f9a3cbd9fd566d",
    },
    "rmbg3_clip_bf16": {
        "repo": BRIA_RMBG3_REPO,
        "revision": BRIA_RMBG3_REVISION,
        "path": "text_encoders/qwen3vl_4b_bf16.safetensors",
        "target_dir": "text_encoders",
        "size": 8875719384,
        "sha256": "36f3ff447ef59201722e8f9ce6020c9819fdcfba6aa2608c4e09b1c0ce114e34",
    },
    "rmbg3_clip_int8": {
        "repo": BRIA_RMBG3_REPO,
        "revision": BRIA_RMBG3_REVISION,
        "path": "text_encoders/qwen3vl_4b_int8_convrot.safetensors",
        "target_dir": "text_encoders",
        "size": 4858559240,
        "sha256": "32de113cbf8282b02cbff0794b637b9d6676da09314a1db5a30bcea9bf4ee01c",
    },
    "rmbg3_vae": {
        "repo": BRIA_RMBG3_REPO,
        "revision": BRIA_RMBG3_REVISION,
        "path": "vae/flux2-vae.safetensors",
        "target_dir": "vae",
        "size": 336211292,
        "sha256": "868fe7b343cc8f3a19dbcfcafbc3d5f888802be3f89bd81b65b3621a066ce8f3",
    },
}

COLABONE_FASHION_ASPECT_PRESETS: dict[str, tuple[int, int]] = {
    "3x4_2k": (1728, 2304),
    "1x1_2k": (2048, 2048),
    "2x3_2k": (1664, 2496),
    "3x4_4k": (2304, 3072),
}

COLABONE_FASHION_SCENE_PROMPTS: dict[str, str] = {
    "studio_light_gray": (
        "Minimalist indoor fashion photography studio with a clean seamless light-gray "
        "matte cyclorama backdrop and matching light-gray floor, soft diffused natural "
        "window light coming gently from the left at 45 degrees, accurate neutral studio "
        "color temperature, subtle realistic floor contact shadow, zero distracting props."
    ),
    "warm_window_studio": (
        "Bright airy indoor studio with clean warm off-white plaster walls, subtle natural "
        "sunlight streaming softly through a side window, warm natural skin tones, gentle "
        "editorial shadows, uncluttered minimalist background."
    ),
    "pure_white_ecom": (
        "Clean seamless pure-white e-commerce photography cyclorama studio, bright even "
        "shadowless softbox lighting, crisp true-to-life garment color reproduction, "
        "subtle grounding shadow beneath shoes, completely uncluttered background."
    ),
    "soft_editorial_outdoor": (
        "Softly blurred bright outdoor garden terrace in gentle overcast diffused daylight, "
        "creamy shallow depth-of-field bokeh, fresh natural tones, keeping the subject and "
        "garment sharply focused with accurate fabric colors."
    ),
}

COLABONE_FASHION_BUILTIN_POSES: tuple[dict[str, Any], ...] = (
    {
        "id": "pose_1",
        "field_key": "pipeline.pose_1_enabled",
        "filename_stem": "01_正面站姿",
        "label": "正面标准站姿（主图）",
        "view": "front",
        "prompt": (
            "Full-body front-facing commercial lookbook portrait, model standing naturally "
            "centered facing the camera with relaxed arms and a gentle warm expression, "
            "head-to-toe framing occupying roughly 78% of the frame height so the entire "
            "garment silhouette, neckline, sleeves, waist, and full hemline are clearly shown."
        ),
    },
    {
        "id": "pose_2",
        "field_key": "pipeline.pose_2_enabled",
        "filename_stem": "02_轻提裙摆",
        "label": "轻提裙摆 / 自然互动",
        "view": "front",
        "prompt": (
            "Full-body front-facing joyful lookbook shot, model standing centered and gently "
            "holding out the sides of the skirt or resting hands gracefully on the outfit "
            "to showcase the full volume, layered fabric drape, and lower-hem patterns, "
            "natural happy smile looking at the camera, identical head-to-body ratio and scale."
        ),
    },
    {
        "id": "pose_3",
        "field_key": "pipeline.pose_3_enabled",
        "filename_stem": "03_左侧微侧",
        "label": "左侧微侧微笑",
        "view": "side",
        "prompt": (
            "Full-body three-quarter portrait turned roughly 30 degrees to the left while "
            "glancing softly toward the camera with a natural smile, clearly displaying the "
            "side drape of the sleeves, bodice fit, cape or layering, and skirt volume, "
            "keeping identical body height, proportions, and studio framing."
        ),
    },
    {
        "id": "pose_4",
        "field_key": "pipeline.pose_4_enabled",
        "filename_stem": "04_右侧回眸",
        "label": "右侧优雅回眸",
        "view": "side",
        "prompt": (
            "Full-body graceful three-quarter turn to the right with a gentle over-the-shoulder "
            "look back toward the camera, natural dynamic posture with realistic fabric folds "
            "and flowing sheer layers, keeping identical facial features, hairstyle, and proportions."
        ),
    },
    {
        "id": "pose_5",
        "field_key": "pipeline.pose_5_enabled",
        "filename_stem": "05_半身细节",
        "label": "半身工艺细节特写",
        "view": "detail",
        "prompt": (
            "Medium close-up upper-body editorial portrait framed from waist up, crisp shallow "
            "studio depth of field highlighting every intricate detail of the collar, bodice, "
            "embroidery, sequins, beads, gems, trims, fabric weave, and sleeve construction "
            "while keeping the exact same model face and natural studio lighting."
        ),
    },
    {
        "id": "pose_6",
        "field_key": "pipeline.pose_6_enabled",
        "filename_stem": "06_完整背面",
        "label": "完整背面展示",
        "view": "back",
        "prompt": (
            "Full-body clean rear-view studio photograph showing the complete back of the "
            "garment from head to toe, model facing away with head slightly turned in soft "
            "profile, accurately showing the back closure, zipper or back bodice finish, "
            "back drape of any cape or skirt panels, and consistent studio lighting."
        ),
    },
)

COLABONE_RMBG3_GARMENT_MATTE_INSTRUCTION = (
    "Generate a detailed grayscale alpha matte isolating ONLY the main clothing garment "
    "or dress outfit. Map all opaque parts of the garment (velvet, cotton, lining, bodice, "
    "sequins, pearls sewn on the dress, gems, trims, ribbons, embroidery, and fluffy "
    "feathers/fur trim) to pure white. Map translucent sheer fabric layers (tulle skirt, "
    "sheer mesh, star-printed sheer cape, organza) to smooth middle gray gradients preserving "
    "their delicate transparency. Map the entire background, room walls, mirrors, stairs, "
    "clothes hangers, pearls hanging on the hanger, wooden mannequin neck/hands, human "
    "hands/arms/face/hair, crowns, handbags, teddy bears, text banners, watermarks, and "
    "floor shadows to pure black."
)

_SAFE_STEM_RE = re.compile(r"[^\w\u4e00-\u9fff-]+")

COLABONE_FASHION_MAX_REFS_PER_GROUP = 6

COLABONE_FASHION_MODEL_SLOT_KEYS: tuple[str, ...] = (
    "1.image",
    "pipeline.model_photo_2",
    "pipeline.model_photo_3",
    "pipeline.model_photo_4",
    "pipeline.model_photo_5",
    "pipeline.model_photo_6",
)

COLABONE_FASHION_GARMENT_SLOT_KEYS: tuple[str, ...] = (
    "2.image",
    "pipeline.garment_photo_2",
    "pipeline.garment_photo_3",
    "pipeline.garment_photo_4",
    "pipeline.garment_photo_5",
    "pipeline.garment_photo_6",
)

COLABONE_FASHION_LEGACY_MODEL_SLOT_KEYS: tuple[str, ...] = (
    "pipeline.model_extra_photo_1",
    "pipeline.model_extra_photo_2",
)

COLABONE_FASHION_LEGACY_GARMENT_SLOT_ROLES: dict[str, str] = {
    "pipeline.garment_detail_photo": "detail",
    "pipeline.garment_back_photo": "back",
}

COLABONE_FASHION_MODEL_ROLE_HINTS: dict[str, str] = {
    "auto": "",
    "front": "front view",
    "side": "side / three-quarter profile",
    "back": "rear view & hairstyle",
    "full_body": "full-body height and proportions",
    "closeup": "close-up facial features",
}

COLABONE_FASHION_GARMENT_ROLE_HINTS: dict[str, str] = {
    "auto": "",
    "front": "front view",
    "back": "rear view",
    "side": "side drape & sleeve profile",
    "detail": "macro detail close-up",
    "flatlay": "flat-lay / fabric texture",
}


def parse_colabone_fashion_photo_roles(
    raw_value: Any,
    *,
    allowed_roles: tuple[str, ...] | set[str],
) -> tuple[list[str], dict[str, str]]:
    """Parse optional JSON metadata describing photo display order and role tags."""
    if not raw_value:
        return [], {}
    parsed: Any = raw_value
    if isinstance(raw_value, str):
        stripped = raw_value.strip()
        if not stripped:
            return [], {}
        try:
            parsed = json.loads(stripped)
        except Exception:
            return [], {}
    if not isinstance(parsed, dict):
        return [], {}

    allowed = set(allowed_roles)
    order: list[str] = []
    roles: dict[str, str] = {}

    raw_order = parsed.get("order")
    if isinstance(raw_order, list):
        seen: set[str] = set()
        for item in raw_order:
            if isinstance(item, str):
                k = item.strip()
                if k and k not in seen:
                    seen.add(k)
                    order.append(k)

    raw_roles = parsed.get("roles")
    if isinstance(raw_roles, dict):
        for k, v in raw_roles.items():
            if isinstance(k, str) and isinstance(v, str):
                rk = k.strip()
                rv = v.strip().lower()
                if rk and rv in allowed:
                    roles[rk] = rv
    else:
        # Also accept flat {slot_key: role} mapping.
        for k, v in parsed.items():
            if k in ("order", "roles"):
                continue
            if isinstance(k, str) and isinstance(v, str):
                rk = k.strip()
                rv = v.strip().lower()
                if rk and rv in allowed:
                    roles[rk] = rv

    return order, roles


def resolve_colabone_fashion_photo_entries(
    input_map: dict[str, tuple[str, Path]],
    overrides: dict[str, Any] | None = None,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    """Resolve ordered multi-reference model and garment photos with validated role tags.

    Supports up to 6 photos per group via canonical slots (1.image + pipeline.model_photo_2..6,
    2.image + pipeline.garment_photo_2..6) and remains backward-compatible with legacy keys.
    """
    ov = overrides or {}
    model_order, model_roles = parse_colabone_fashion_photo_roles(
        ov.get("pipeline.model_photo_roles"),
        allowed_roles=set(COLABONE_FASHION_MODEL_ROLE_HINTS),
    )
    garment_order, garment_roles = parse_colabone_fashion_photo_roles(
        ov.get("pipeline.garment_photo_roles"),
        allowed_roles=set(COLABONE_FASHION_GARMENT_ROLE_HINTS),
    )

    def _collect(
        canonical_slots: tuple[str, ...],
        legacy_slots: tuple[str, ...],
        custom_order: list[str],
        role_map: dict[str, str],
        legacy_default_roles: dict[str, str],
    ) -> list[dict[str, Any]]:
        allowed_keys = list(canonical_slots) + list(legacy_slots)
        allowed_set = set(allowed_keys)
        ordered_keys: list[str] = []
        seen: set[str] = set()
        for k in custom_order:
            if k in allowed_set and k in input_map and k not in seen:
                seen.add(k)
                ordered_keys.append(k)
        for k in allowed_keys:
            if k in input_map and k not in seen:
                seen.add(k)
                ordered_keys.append(k)

        entries: list[dict[str, Any]] = []
        for idx, key in enumerate(ordered_keys[:COLABONE_FASHION_MAX_REFS_PER_GROUP]):
            fname, fpath = input_map[key]
            default_role = legacy_default_roles.get(key, "front" if idx == 0 else "auto")
            role = role_map.get(key, default_role)
            entries.append(
                {
                    "key": key,
                    "filename": fname,
                    "path": fpath,
                    "role": role,
                }
            )
        return entries

    model_entries = _collect(
        COLABONE_FASHION_MODEL_SLOT_KEYS,
        COLABONE_FASHION_LEGACY_MODEL_SLOT_KEYS,
        model_order,
        model_roles,
        {},
    )
    garment_entries = _collect(
        COLABONE_FASHION_GARMENT_SLOT_KEYS,
        tuple(COLABONE_FASHION_LEGACY_GARMENT_SLOT_ROLES.keys()),
        garment_order,
        garment_roles,
        COLABONE_FASHION_LEGACY_GARMENT_SLOT_ROLES,
    )
    return model_entries, garment_entries


def _parse_bool(value: Any, default: bool) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return bool(value)
    if isinstance(value, str):
        low = value.strip().lower()
        if low in ("true", "1", "yes", "on"):
            return True
        if low in ("false", "0", "no", "off"):
            return False
    return default


def _parse_int(value: Any, default: int, *, min_val: int, max_val: int) -> int:
    try:
        if isinstance(value, bool) or value is None or str(value).strip() == "":
            return default
        fval = float(value)
        if not math.isfinite(fval):
            return default
        parsed = int(round(fval))
    except (TypeError, ValueError, OverflowError):
        return default
    return max(min_val, min(max_val, parsed))


def resolve_colabone_fashion_precision(
    precision_mode: str,
    gpu_total_mib: int | None = None,
) -> str:
    """Resolve effective precision tier ('bf16' or 'int8') from user override and GPU VRAM."""
    mode = str(precision_mode or "auto").strip().lower()
    if mode == "bf16":
        return "bf16"
    if mode == "int8":
        return "int8"
    if gpu_total_mib is not None and int(gpu_total_mib) >= COLABONE_FASHION_BF16_THRESHOLD_MIB:
        return "bf16"
    return "int8"


def resolve_colabone_fashion_models(
    precision: str,
    garment_cleanup: str = "rmbg3",
) -> tuple[list[dict[str, Any]], dict[str, str]]:
    """Return required HF model refs and exact ComfyUI filenames for the resolved precision."""
    tier = "bf16" if str(precision).strip().lower() == "bf16" else "int8"
    cleanup = str(garment_cleanup or "rmbg3").strip().lower()

    models: list[dict[str, Any]] = []
    filenames: dict[str, str] = {}

    if tier == "bf16":
        dit_ref = COLABONE_FASHION_MODEL_FILES["minimax_dit_bf16"]
        clip_ref = COLABONE_FASHION_MODEL_FILES["minimax_clip_int8"]
    else:
        dit_ref = COLABONE_FASHION_MODEL_FILES["minimax_dit_int8"]
        clip_ref = COLABONE_FASHION_MODEL_FILES["minimax_clip_nvfp4"]
    vae_ref = COLABONE_FASHION_MODEL_FILES["minimax_vae"]

    models.extend([dict(dit_ref), dict(clip_ref), dict(vae_ref)])
    filenames["minimax_dit"] = Path(dit_ref["path"]).name
    filenames["minimax_clip"] = Path(clip_ref["path"]).name
    filenames["minimax_vae"] = Path(vae_ref["path"]).name

    if cleanup == "rmbg3":
        if tier == "bf16":
            rmbg_dit = COLABONE_FASHION_MODEL_FILES["rmbg3_dit_bf16"]
            rmbg_clip = COLABONE_FASHION_MODEL_FILES["rmbg3_clip_bf16"]
        else:
            rmbg_dit = COLABONE_FASHION_MODEL_FILES["rmbg3_dit_int8"]
            rmbg_clip = COLABONE_FASHION_MODEL_FILES["rmbg3_clip_int8"]
        rmbg_vae = COLABONE_FASHION_MODEL_FILES["rmbg3_vae"]
        models.extend([dict(rmbg_dit), dict(rmbg_clip), dict(rmbg_vae)])
        filenames["rmbg3_dit"] = Path(rmbg_dit["path"]).name
        filenames["rmbg3_clip"] = Path(rmbg_clip["path"]).name
        filenames["rmbg3_vae"] = Path(rmbg_vae["path"]).name

    return models, filenames


def resolve_colabone_fashion_dimensions(aspect_preset: str) -> tuple[int, int]:
    key = str(aspect_preset or "3x4_2k").strip().lower()
    return COLABONE_FASHION_ASPECT_PRESETS.get(key, COLABONE_FASHION_ASPECT_PRESETS["3x4_2k"])


def plan_colabone_fashion_shots(overrides: dict[str, Any]) -> list[dict[str, Any]]:
    """Build the ordered list of lookbook shots from shot_pack / toggles / custom lines."""
    custom_raw = str(overrides.get("pipeline.custom_pose_prompts") or "").strip()
    if custom_raw:
        lines = [
            line.strip()[:COLABONE_FASHION_MAX_USER_PROMPT_CHARS]
            for line in custom_raw.splitlines()
            if line.strip()
        ]
        shots: list[dict[str, Any]] = []
        for idx, line in enumerate(lines[:12], 1):
            low = line.lower()
            view = "back" if any(tok in low for tok in ("背面", "后背", "後背", "背影", "back view", "rear view")) else (
                "detail" if any(tok in low for tok in ("特写", "特寫", "近景", "半身", "close-up", "closeup")) else "front"
            )
            short_name = _SAFE_STEM_RE.sub("", line[:12]).strip("_-") or f"姿势{idx}"
            shots.append(
                {
                    "id": f"custom_{idx}",
                    "filename_stem": f"{idx:02d}_{short_name}",
                    "label": f"自定义姿势 {idx}",
                    "view": view,
                    "prompt": (
                        f"Commercial fashion lookbook portrait. Pose & framing: {line}. "
                        "Keep identical model identity, facial features, head-to-body ratio, "
                        "exact garment details, and unified studio lighting."
                    ),
                }
            )
        if shots:
            return shots

    pack = str(overrides.get("pipeline.shot_pack") or "full_6").strip().lower()
    if pack == "hero_1":
        return [dict(COLABONE_FASHION_BUILTIN_POSES[0])]
    if pack == "core_4":
        return [
            dict(COLABONE_FASHION_BUILTIN_POSES[0]),
            dict(COLABONE_FASHION_BUILTIN_POSES[1]),
            dict(COLABONE_FASHION_BUILTIN_POSES[2]),
            dict(COLABONE_FASHION_BUILTIN_POSES[4]),
        ]
    if pack == "custom":
        selected = [
            dict(pose)
            for pose in COLABONE_FASHION_BUILTIN_POSES
            if _parse_bool(overrides.get(pose["field_key"]), True)
        ]
        if not selected:
            raise ValueError("自定义姿态模式下至少需要勾选 1 个姿势（或在高级设置中填写自定义姿势）")
        return selected
    return [dict(pose) for pose in COLABONE_FASHION_BUILTIN_POSES]


def _decode_garment_rgb_on_white(img_bytes: bytes):
    """Decode garment image bytes into upright 8-bit RGB composited cleanly over pure white.

    Handles EXIF orientation tags, 16-bit grayscale ('I' / 'I;16*') scaling, and
    RGBA/LA/PA/palette-tRNS transparency so downstream matting and lookbook nodes
    never see rotated images, clipped 16-bit whiteouts, or black transparent halos.
    """
    from PIL import Image, ImageChops, ImageOps

    with Image.open(io.BytesIO(img_bytes)) as src:
        src.load()
        orig_trns = src.info.get("transparency")
        im = ImageOps.exif_transpose(src)

        mode = str(getattr(im, "mode", "") or "")
        if mode == "I" or mode.startswith("I;16"):
            info = getattr(im, "info", None)
            trns = (
                info.get("transparency")
                if isinstance(info, dict) and "transparency" in info
                else orig_trns
            )
            as_i = im if mode == "I" else im.convert("I")
            l8 = as_i.point(lambda v: v * (1.0 / 256.0)).convert("L")
            if isinstance(trns, int) and not isinstance(trns, bool):
                gt = as_i.point(lambda v: v - trns).convert("L")
                lt = as_i.point(lambda v: trns - v).convert("L")
                alpha = ImageChops.lighter(gt, lt).point([0] + [255] * 255)
                im = Image.merge("RGBA", (l8, l8, l8, alpha))
            else:
                im = l8

        has_alpha = (
            "A" in im.getbands()
            or im.mode in ("RGBA", "LA", "PA")
            or "transparency" in im.info
            or (orig_trns is not None and im.mode in ("P", "L", "RGB"))
        )
        if has_alpha:
            if (
                "transparency" not in im.info
                and orig_trns is not None
                and im.mode in ("P", "L", "RGB")
            ):
                im.info["transparency"] = orig_trns
            rgba = im.convert("RGBA")
            rgb = Image.new("RGB", rgba.size, (255, 255, 255))
            rgb.paste(rgba, mask=rgba.getchannel("A"))
            return rgb
        return im.convert("RGB")


def _validate_fashion_image_input(path: Path, label: str) -> None:
    """Validate that a resolved fashion try-on input is a regular, PIL-decodable image."""
    from PIL import Image

    p = Path(path)
    if p.is_symlink():
        raise ValueError(f"{label}不能是符号链接")
    if not p.is_file():
        raise ValueError(f"{label}文件不存在或已失效，请重新上传")
    ext = p.suffix.lstrip(".").lower()
    if not ext or ext not in COLABONE_FASHION_IMAGE_EXTENSIONS:
        raise ValueError(
            f"{label}仅支持 PNG、JPEG 或 WebP 图片格式（当前为 {ext or '未知'}）"
        )
    try:
        if p.stat().st_size <= 0:
            raise ValueError(f"{label}图片文件为空，请重新上传")
    except OSError as exc:
        raise ValueError(f"{label}文件不可读取，请重新上传") from exc

    try:
        with Image.open(p) as im:
            fmt = str(getattr(im, "format", "") or "").upper()
            if fmt not in ("PNG", "JPEG", "WEBP"):
                raise ValueError(f"{label}仅支持有效的 PNG、JPEG 或 WebP 图片文件")
            w, h = im.size
            if w <= 0 or h <= 0:
                raise ValueError(f"{label}图片尺寸无效，请重新上传")
            if w * h > COLABONE_FASHION_MAX_IMAGE_PIXELS:
                raise ValueError(f"{label}图片分辨率超过上限（最多 1600 万像素）")
            im.load()
    except ValueError:
        raise
    except Image.DecompressionBombError as exc:
        raise ValueError(f"{label}图片分辨率超过上限（最多 1600 万像素）") from exc
    except Exception as exc:
        raise ValueError(
            f"{label}图片已损坏或无法解码，请重新上传有效的 PNG、JPEG 或 WebP 图片"
        ) from exc


def _prepare_opaque_rgb_upload(
    filename: str,
    src_path: Path,
    *,
    stem: str,
    work_dir: Path,
) -> tuple[str, Path]:
    """Ensure transparent, 16-bit, or EXIF-rotated inputs upload as upright white-flattened RGB PNGs.

    ComfyUI's LoadImage drops alpha via .convert('RGB') without compositing over white,
    which turns transparent cutout PNGs (e.g. cutout models or detail shots) pitch-black.
    Standard opaque upright 8-bit images pass straight through untouched.
    """
    from PIL import Image

    try:
        with Image.open(src_path) as im:
            fmt = str(getattr(im, "format", "") or "").upper()
            if fmt not in ("PNG", "JPEG", "WEBP"):
                raise ValueError(
                    f"输入图片「{filename}」仅支持有效的 PNG、JPEG 或 WebP 图片文件"
                )
            if im.width <= 0 or im.height <= 0:
                raise ValueError(f"输入图片「{filename}」尺寸无效")
            if im.width * im.height > COLABONE_FASHION_MAX_IMAGE_PIXELS:
                raise ValueError(
                    f"输入图片「{filename}」分辨率超过上限（最多 1600 万像素）"
                )
            mode = str(getattr(im, "mode", "") or "")
            orientation = 1
            try:
                orientation = int(im.getexif().get(274, 1) or 1)
            except Exception:
                orientation = 1
            needs_normalize = (
                mode not in ("RGB", "L")
                or "A" in im.getbands()
                or "transparency" in im.info
                or orientation != 1
            )
            im.load()
    except ValueError:
        raise
    except Image.DecompressionBombError as exc:
        raise ValueError(
            f"输入图片「{filename}」分辨率超过上限（最多 1600 万像素）"
        ) from exc
    except Exception as exc:
        raise ValueError(
            f"输入图片「{filename}」已损坏或无法解码，请重新上传有效的 PNG、JPEG 或 WebP 图片"
        ) from exc

    if not needs_normalize:
        return filename, src_path

    rgb = _decode_garment_rgb_on_white(src_path.read_bytes())
    safe_stem = _SAFE_STEM_RE.sub("", stem).strip("_-") or "ref"
    norm_path = work_dir / f"{safe_stem}_rgb.png"
    rgb.save(norm_path, format="PNG")
    return norm_path.name, norm_path


def auto_trim_ecommerce_borders(img_bytes: bytes) -> bytes:
    """Normalize orientation/alpha over white and trim near-uniform solid e-commerce margins.

    Inspects 100% of pixels across every candidate row/column (without coarse subsampling)
    matching each edge's corner background color within tight per-channel tolerance, requiring
    >= 99.5% background match and stopping immediately on any contiguous foreground run so
    white garments and thin-strap dresses remain intact. Always returns valid RGB PNG bytes.
    """
    from PIL import Image, ImageChops

    rgb = _decode_garment_rgb_on_white(img_bytes)
    w, h = rgb.size
    if w < 64 or h < 64:
        out = io.BytesIO()
        rgb.save(out, format="PNG")
        return out.getvalue()

    color_tol = 5
    corner_tol = 6
    min_match_ratio = 0.995
    mask_lut = [0] * (color_tol + 1) + [1] * (255 - color_tol)
    mask_cache: dict[tuple[int, int, int], tuple[Any, bytes]] = {}

    def _edge_bg(c1: tuple[int, int, int], c2: tuple[int, int, int]) -> tuple[int, int, int] | None:
        if max(abs(c1[0] - c2[0]), abs(c1[1] - c2[1]), abs(c1[2] - c2[2])) > corner_tol:
            return None
        return ((c1[0] + c2[0]) // 2, (c1[1] + c2[1]) // 2, (c1[2] + c2[2]) // 2)

    def _fg_mask_for(bg: tuple[int, int, int]) -> tuple[Any, bytes]:
        cached = mask_cache.get(bg)
        if cached is not None:
            return cached
        bg_im = Image.new("RGB", (w, h), bg)
        diff = ImageChops.difference(rgb, bg_im)
        dr, dg, db = diff.split()
        max_diff = ImageChops.lighter(ImageChops.lighter(dr, dg), db)
        fg_mask = max_diff.point(mask_lut)
        pair = (fg_mask, fg_mask.tobytes())
        mask_cache[bg] = pair
        return pair

    def _slice_is_solid_border(slice_bytes: bytes) -> bool:
        total = len(slice_bytes)
        if total <= 0:
            return False
        if b"\x01" not in slice_bytes:
            return True
        if b"\x01\x01" in slice_bytes:
            return False
        fg_count = slice_bytes.count(1)
        return ((total - fg_count) / total) >= min_match_ratio

    # Only trim up to 22% from top/bottom and 18% from left/right so actual garments stay intact.
    max_top = int(h * 0.22)
    max_bot = int(h * 0.22)
    top = 0
    top_bg = _edge_bg(rgb.getpixel((0, 0)), rgb.getpixel((w - 1, 0)))
    if top_bg is not None:
        _, top_bytes = _fg_mask_for(top_bg)
        while top < max_top and _slice_is_solid_border(top_bytes[top * w : (top + 1) * w]):
            top += 1

    bottom = h
    bot_bg = _edge_bg(rgb.getpixel((0, h - 1)), rgb.getpixel((w - 1, h - 1)))
    if bot_bg is not None:
        _, bot_bytes = _fg_mask_for(bot_bg)
        while (
            (h - bottom) < max_bot
            and bottom > top + 64
            and _slice_is_solid_border(bot_bytes[(bottom - 1) * w : bottom * w])
        ):
            bottom -= 1

    max_side = int(w * 0.18)
    left = 0
    left_bg = _edge_bg(rgb.getpixel((0, top)), rgb.getpixel((0, bottom - 1)))
    if left_bg is not None:
        _, left_bytes = _fg_mask_for(left_bg)
        while (
            left < max_side
            and _slice_is_solid_border(left_bytes[top * w + left : bottom * w + left : w])
        ):
            left += 1

    right = w
    right_bg = _edge_bg(rgb.getpixel((w - 1, top)), rgb.getpixel((w - 1, bottom - 1)))
    if right_bg is not None:
        _, right_bytes = _fg_mask_for(right_bg)
        while (
            (w - right) < max_side
            and right > left + 64
            and _slice_is_solid_border(
                right_bytes[top * w + (right - 1) : bottom * w + (right - 1) : w]
            )
        ):
            right -= 1

    # Refuse trimming edges whose background color matches the entire remaining interior (e.g. uniform images).
    if top > 0 and top_bg is not None:
        if _fg_mask_for(top_bg)[0].crop((left, top, right, bottom)).getbbox() is None:
            top = 0
    if bottom < h and bot_bg is not None:
        if _fg_mask_for(bot_bg)[0].crop((left, top, right, bottom)).getbbox() is None:
            bottom = h
    if left > 0 and left_bg is not None:
        if _fg_mask_for(left_bg)[0].crop((left, top, right, bottom)).getbbox() is None:
            left = 0
    if right < w and right_bg is not None:
        if _fg_mask_for(right_bg)[0].crop((left, top, right, bottom)).getbbox() is None:
            right = w

    # Require a meaningful trim (>= 2% of dimension) and safe remaining area.
    result = rgb
    if (
        (top > h * 0.02 or (h - bottom) > h * 0.02 or left > w * 0.02 or (w - right) > w * 0.02)
        and (right - left) >= w * 0.55
        and (bottom - top) >= h * 0.55
    ):
        result = rgb.crop((left, top, right, bottom))

    out = io.BytesIO()
    result.save(out, format="PNG")
    return out.getvalue()


def composite_soft_alpha_on_white(
    garment_bytes: bytes,
    matte_bytes: bytes,
) -> bytes:
    """Composite the original garment over clean neutral white using RMBG 3.0's 8-bit soft matte."""
    from PIL import Image, ImageOps

    fg = _decode_garment_rgb_on_white(garment_bytes)
    try:
        with Image.open(io.BytesIO(matte_bytes)) as matte_im:
            matte_im.load()
            transposed = ImageOps.exif_transpose(matte_im)
            mode = str(getattr(transposed, "mode", "") or "")
            if mode == "I" or mode.startswith("I;16"):
                as_i = transposed if mode == "I" else transposed.convert("I")
                alpha = as_i.point(lambda v: v * (1.0 / 256.0)).convert("L")
            else:
                alpha = transposed.convert("L")
    except Exception as exc:
        raise ValueError("RMBG 3.0 遮罩图像损坏或无法解码") from exc

    if alpha.size != fg.size:
        alpha = alpha.resize(fg.size, Image.Resampling.LANCZOS)

    # Safety guard: if the matte is almost completely black (< 2% foreground energy),
    # keep the prepared original image rather than returning a blank white canvas.
    hist = alpha.histogram()
    total_px = max(1, fg.size[0] * fg.size[1])
    fg_px = sum(hist[32:])
    if fg_px / total_px < 0.02:
        out = io.BytesIO()
        fg.save(out, format="PNG")
        return out.getvalue()

    bg = Image.new("RGB", fg.size, (255, 255, 255))
    composited = Image.composite(fg, bg, alpha)

    # Tightly center-pad around non-empty matte bounding box with comfortable 6% margin.
    bbox = alpha.point(lambda v: 255 if v >= 18 else 0).getbbox()
    if bbox:
        x0, y0, x1, y1 = bbox
        bw, bh = max(1, x1 - x0), max(1, y1 - y0)
        pad = max(16, int(max(bw, bh) * 0.06))
        cx0 = max(0, x0 - pad)
        cy0 = max(0, y0 - pad)
        cx1 = min(fg.size[0], x1 + pad)
        cy1 = min(fg.size[1], y1 + pad)
        if (cx1 - cx0) >= 64 and (cy1 - cy0) >= 64:
            composited = composited.crop((cx0, cy0, cx1, cy1))

    out = io.BytesIO()
    composited.save(out, format="PNG")
    return out.getvalue()


def build_rmbg3_garment_matte_prompt(
    garment_comfy_name: str,
    *,
    rmbg3_dit: str,
    rmbg3_clip: str,
    rmbg3_vae: str,
    garment_notes: str = "",
    seed: int = 42,
) -> dict[str, dict[str, Any]]:
    """Build native ComfyUI v0.46.0 Bria RMBG 3.0 soft-alpha garment matting workflow."""
    instruction = COLABONE_RMBG3_GARMENT_MATTE_INSTRUCTION
    clean_notes = str(garment_notes or "").strip()[:COLABONE_FASHION_MAX_USER_PROMPT_CHARS]
    if clean_notes:
        instruction = f"{instruction} Additional user garment notes: {clean_notes}"
    fibo_json_prompt = json.dumps({"edit_instruction": instruction}, ensure_ascii=False)

    return {
        "1": {
            "class_type": "UNETLoader",
            "_meta": {"title": "加载 Bria RMBG 3.0 抠图模型"},
            "inputs": {
                "unet_name": rmbg3_dit,
                "weight_dtype": "default",
            },
        },
        "2": {
            "class_type": "CLIPLoader",
            "_meta": {"title": "加载 RMBG 3.0 视觉文本编码器"},
            "inputs": {
                "clip_name": rmbg3_clip,
                "type": "fibo2",
                "device": "default",
            },
        },
        "3": {
            "class_type": "VAELoader",
            "_meta": {"title": "加载 FLUX.2 VAE"},
            "inputs": {
                "vae_name": rmbg3_vae,
            },
        },
        "4": {
            "class_type": "LoadImage",
            "_meta": {"title": "加载待清洗服装图"},
            "inputs": {
                "image": garment_comfy_name,
            },
        },
        "5": {
            "class_type": "TextEncodeFibo2",
            "_meta": {"title": "RMBG 3.0 软透明遮罩编码"},
            "inputs": {
                "clip": ["2", 0],
                "vae": ["3", 0],
                "prompt": fibo_json_prompt,
                "negative_prompt": "",
                "images.image_1": ["4", 0],
                "width": 0,
                "height": 0,
                "batch_size": 1,
            },
        },
        "6": {
            "class_type": "KSampler",
            "_meta": {"title": "RMBG 3.0 采样生成软遮罩"},
            "inputs": {
                "model": ["1", 0],
                "positive": ["5", 0],
                "negative": ["5", 1],
                "latent_image": ["5", 2],
                "seed": int(seed),
                "steps": 4,
                "cfg": 1.0,
                "sampler_name": "euler",
                "scheduler": "simple",
                "denoise": 1.0,
            },
        },
        "7": {
            "class_type": "VAEDecode",
            "_meta": {"title": "解码软透明通道"},
            "inputs": {
                "samples": ["6", 0],
                "vae": ["3", 0],
            },
        },
        "8": {
            "class_type": "SaveImage",
            "_meta": {"title": "输出服装软遮罩"},
            "inputs": {
                "images": ["7", 0],
                "filename_prefix": "colabone_rmbg3_matte",
            },
        },
    }


def build_minimax_h3_tryon_prompt_text(
    *,
    shot: dict[str, Any],
    ref_roles: list[tuple[int, str]] | list[tuple[int, str, str]],
    scene_preset: str,
    garment_notes: str = "",
    keep_accessories: bool = False,
    styling_match: str = "auto_match",
    color_fidelity: str = "strict",
    custom_style_prompt: str = "",
) -> str:
    """Compose a precise multi-reference instruction naming <Picture 1..N> roles and angles."""
    by_role: dict[str, list[tuple[int, str]]] = {}
    for item in ref_roles:
        if len(item) >= 3:
            pic_idx, role, angle = int(item[0]), str(item[1]), str(item[2] or "auto").strip().lower()
        else:
            pic_idx, role, angle = int(item[0]), str(item[1]), "auto"
        by_role.setdefault(role, []).append((pic_idx, angle))

    def _fmt_pics(entries: list[tuple[int, str]], hint_map: dict[str, str] | None = None) -> str:
        formatted: list[str] = []
        for idx, angle in entries:
            hint = hint_map.get(angle, "") if hint_map else ""
            if hint:
                formatted.append(f"<Picture {idx}> ({hint})")
            else:
                formatted.append(f"<Picture {idx}>")
        return " and ".join(formatted)

    parts: list[str] = []
    model_pics = by_role.get("model", [])
    hero_pics = by_role.get("hero", [])
    garment_front_pics = by_role.get("garment_front", [])
    garment_back_pics = by_role.get("garment_back", [])
    garment_detail_pics = by_role.get("garment_detail", [])
    scene_pics = by_role.get("scene_ref", [])

    model_desc = _fmt_pics(model_pics, COLABONE_FASHION_MODEL_ROLE_HINTS)
    garment_front_desc = _fmt_pics(garment_front_pics, COLABONE_FASHION_GARMENT_ROLE_HINTS)
    garment_back_desc = _fmt_pics(garment_back_pics, COLABONE_FASHION_GARMENT_ROLE_HINTS)
    garment_detail_desc = _fmt_pics(garment_detail_pics, None)

    if hero_pics:
        parts.append(
            f"Create a new studio lookbook photograph of the exact same model and outfit established in {_fmt_pics(hero_pics)}, "
            f"strictly preserving the face, age, hairstyle, skin tone, and exact body proportions of {model_desc}."
        )
    else:
        primary_garment_desc = garment_front_desc or garment_back_desc or garment_detail_desc
        parts.append(
            f"Create a high-resolution professional studio lookbook photograph dressing the exact model from {model_desc} "
            f"in the exact garment shown in {primary_garment_desc}."
        )

    view = str(shot.get("view") or "front").strip().lower()
    if view == "back" and garment_back_pics:
        back_clause = (
            f"BACK VIEW GARMENT FIDELITY (HARD CONSTRAINT): Follow {garment_back_desc} faithfully for the rear garment design, "
            "back bodice, zipper or closure, cape attachment, skirt layers, and rear pattern layout; do not copy front-only embellishments onto the back."
        )
        if garment_detail_pics:
            back_clause += (
                f" Match the fabric weave, stitching, and micro-details shown in {garment_detail_desc}."
            )
        parts.append(back_clause)
    elif garment_front_pics:
        garment_clause = (
            f"GARMENT FIDELITY (HARD CONSTRAINT): Reproduce the garment from {garment_front_desc} with 100% fidelity—"
            "exact silhouette, volume, cut, color, fabric material (velvet, tulle, mesh, lace, satin, feathers), prints, embroidery, "
            "sequins, pearls, gems, trim placements, and translucent layering must remain unchanged."
        )
        if garment_detail_pics:
            garment_clause += (
                f" Closely match the intricate micro-details, stitching, beading, sequins, and surface texture shown in {garment_detail_desc}."
            )
        if view == "back":
            garment_clause += (
                " For this rear view without a separate back reference photo, infer a clean, realistic rear closure and back drape fully consistent with the front silhouette, color, and fabric material."
            )
        parts.append(garment_clause)
    elif garment_detail_pics:
        parts.append(
            f"GARMENT FIDELITY (HARD CONSTRAINT): Match the exact garment construction, color, fabric weave, and intricate micro-details shown in {garment_detail_desc}."
        )

    parts.append(
        "IDENTITY & PROPORTION LOCK (HARD CONSTRAINT): Maintain the exact same person's facial identity, eye shape, nose, mouth, "
        "hairstyle, age appearance, shoulder width, limb proportions, and head-to-body ratio across every shot—never change the model's body size or height."
    )

    if str(color_fidelity or "strict").strip().lower() == "strict":
        parts.append(
            "COLOR & TEXTURE LOCK: Preserve the exact true hue, saturation, and fabric sheen of the reference garment with zero color cast or washed-out highlights."
        )

    if not keep_accessories:
        parts.append(
            "CLEAN STYLING: Exclude any unrelated props or non-garment items from source photos (such as clothes hangers, pearls attached to a hanger, "
            "watermarks, text banners, teddy bears, handbags, mirrors, or random crowns unless explicitly sewn into the outfit)."
        )

    normalized_styling = str(styling_match or "auto_match").strip().lower()
    if normalized_styling == "keep_model_original":
        parts.append(
            f"FOOTWEAR & LOWER-BODY MATCHING: Keep the model's own original shoes and any uncovered bottoms from {model_desc} intact whenever compatible; replace only the target reference garment."
        )
    else:
        parts.append(
            "FOOTWEAR & LOWER-BODY MATCHING: Pair the outfit with clean, understated footwear (and minimal coordinated bottoms if the reference garment is a standalone top) harmonizing with the garment's color palette and style without stealing focus."
        )

    clean_notes = str(garment_notes or "").strip()[:COLABONE_FASHION_MAX_USER_PROMPT_CHARS]
    if clean_notes:
        parts.append(f"SPECIFIC GARMENT NOTES: {clean_notes}")

    if scene_pics:
        parts.append(
            f"UNIFIED SCENE & LIGHTING: Place the model seamlessly into the clean studio setting, background tone, and soft natural lighting style of {_fmt_pics(scene_pics)}."
        )
    else:
        scene_text = COLABONE_FASHION_SCENE_PROMPTS.get(
            str(scene_preset or "studio_light_gray").strip().lower(),
            COLABONE_FASHION_SCENE_PROMPTS["studio_light_gray"],
        )
        parts.append(f"UNIFIED SCENE & LIGHTING: {scene_text}")

    parts.append(f"POSE & COMPOSITION: {shot['prompt']}")

    extra_style = str(custom_style_prompt or "").strip()[:COLABONE_FASHION_MAX_USER_PROMPT_CHARS]
    if extra_style:
        parts.append(f"ADDITIONAL ART DIRECTION: {extra_style}")

    parts.append(
        "Photorealistic editorial lookbook photography, sharp micro-texture on fabrics and skin, natural anatomical hands and five fingers, no text or watermark."
    )
    return "\n".join(parts)


def build_minimax_h3_tryon_workflow(
    *,
    reference_images: list[str],
    prompt_text: str,
    width: int,
    height: int,
    minimax_dit: str,
    minimax_clip: str,
    minimax_vae: str,
    steps: int = 6,
    seed: int = 42,
    filename_prefix: str = "colabone_fashion_tryon",
) -> dict[str, dict[str, Any]]:
    """Build native ComfyUI v0.46.0 MiniMax-H3-Image multi-reference try-on API graph."""
    if not reference_images:
        raise ValueError("MiniMax-H3-Image 至少需要 1 张参考图片")
    if len(reference_images) > 16:
        reference_images = reference_images[:16]

    # Snap dimensions to multiples of 32 (MiniMaxH3ImageConditioning CANVAS_MULTIPLE).
    aligned_w = max(512, min(4096, (int(width) // 32) * 32))
    aligned_h = max(512, min(4096, (int(height) // 32) * 32))

    wf: dict[str, dict[str, Any]] = {
        "1": {
            "class_type": "UNETLoader",
            "_meta": {"title": "加载 MiniMax-H3-Image 扩散主模型"},
            "inputs": {
                "unet_name": minimax_dit,
                "weight_dtype": "default",
            },
        },
        "2": {
            "class_type": "CLIPLoader",
            "_meta": {"title": "加载 Qwen3-VL-32B 视觉编码器"},
            "inputs": {
                "clip_name": minimax_clip,
                "type": "minimax",
                "device": "default",
            },
        },
        "3": {
            "class_type": "VAELoader",
            "_meta": {"title": "加载 MiniMax-H3 VAE"},
            "inputs": {
                "vae_name": minimax_vae,
            },
        },
    }

    cond_inputs: dict[str, Any] = {
        "clip": ["2", 0],
        "vae": ["3", 0],
        "prompt": prompt_text,
        "width": aligned_w,
        "height": aligned_h,
    }
    for idx, img_name in enumerate(reference_images):
        node_id = str(10 + idx)
        wf[node_id] = {
            "class_type": "LoadImage",
            "_meta": {"title": f"参考图 {idx + 1}"},
            "inputs": {
                "image": img_name,
            },
        }
        # MiniMaxH3ImageConditioning uses Autogrow TemplatePrefix(prefix="image_") -> 0-indexed.
        cond_inputs[f"images.image_{idx}"] = [node_id, 0]

    wf["30"] = {
        "class_type": "MiniMaxH3ImageConditioning",
        "_meta": {"title": "MiniMax-H3 多参考图条件编码"},
        "inputs": cond_inputs,
    }
    wf["31"] = {
        "class_type": "ConditioningZeroOut",
        "_meta": {"title": "空负向条件（CFG 1.0 蒸馏）"},
        "inputs": {
            "conditioning": ["30", 0],
        },
    }
    wf["32"] = {
        "class_type": "KSampler",
        "_meta": {"title": "MiniMax-H3 极速采样"},
        "inputs": {
            "model": ["1", 0],
            "positive": ["30", 0],
            "negative": ["31", 0],
            "latent_image": ["30", 1],
            "seed": int(seed),
            "steps": max(4, min(16, int(steps))),
            "cfg": 1.0,
            "sampler_name": "euler",
            "scheduler": "simple",
            "denoise": 1.0,
        },
    }
    wf["33"] = {
        "class_type": "VAEDecode",
        "_meta": {"title": "解码高清棚拍图像"},
        "inputs": {
            "samples": ["32", 0],
            "vae": ["3", 0],
        },
    }
    wf["34"] = {
        "class_type": "SaveImage",
        "_meta": {"title": "保存试穿棚拍成片"},
        "inputs": {
            "images": ["33", 0],
            "filename_prefix": filename_prefix,
        },
    }
    return wf


class ColabOneFashionTryOnRunner:
    """Remote multi-pose AI fashion try-on pipeline orchestrator backed by ComfyUI v0.45+."""

    def __init__(self, comfy_manager):
        self.comfy = comfy_manager
        self.runtime = comfy_manager.runtime

    def _detect_gpu_total_mib(self) -> int | None:
        gpu_fn = getattr(self.runtime, "gpu_info", None)
        if not callable(gpu_fn):
            return None
        try:
            info = gpu_fn() or {}
            total = info.get("total_mib")
            if isinstance(total, (int, float)) and total > 0:
                return int(total)
        except Exception:
            pass
        return None

    def ensure_ready(
        self,
        task,
        wf: dict,
        *,
        hf_token: str = "",
        overrides: dict[str, Any] | None = None,
        progress_range: tuple[float, float] | None = (0.02, 0.24),
    ) -> tuple[str, dict[str, str]]:
        """Preflight hardware, download/verify VRAM-matched HF weights, and ensure ComfyUI is running."""
        task.check()
        ov = dict(overrides or {})
        gpu_mib = self._detect_gpu_total_mib()
        precision = resolve_colabone_fashion_precision(
            str(ov.get("pipeline.precision_mode") or "auto"),
            gpu_total_mib=gpu_mib,
        )
        garment_cleanup = str(ov.get("pipeline.garment_cleanup") or "rmbg3").strip().lower()
        required_models, filenames = resolve_colabone_fashion_models(
            precision, garment_cleanup=garment_cleanup
        )

        # Dynamic preflight based on resolved precision & cleanup toggle.
        total_bytes = sum(int(m["size"]) for m in required_models)
        min_vram_mib = (
            COLABONE_FASHION_MIN_VRAM_BF16_MIB
            if precision == "bf16"
            else COLABONE_FASHION_MIN_VRAM_INT8_MIB
        )
        preflight_wf = dict(wf)
        preflight_wf["models"] = required_models
        preflight_wf["custom_nodes"] = list(wf.get("custom_nodes") or [])
        preflight_wf["node_patches"] = list(wf.get("node_patches") or [])
        preflight_wf["pip_packages"] = list(wf.get("pip_packages") or [])
        preflight_wf["min_vram_mib"] = min_vram_mib
        preflight_wf["min_disk_mib"] = (
            (int(total_bytes // (1024 * 1024)) + 4096)
            if total_bytes > 0
            else int(wf.get("min_disk_mib") or 0)
        )
        self.comfy.packs.check_preflight(preflight_wf)

        if not self.comfy.is_running():
            self.comfy.prepare(task)
            task.check()

        tier_label = (
            "满血 BF16 旗舰精度（80G/96G）"
            if precision == "bf16"
            else "INT8 轻量量化精度（24G+）"
        )
        cleanup_label = " + Bria RMBG 3.0 软透明抠衣" if garment_cleanup == "rmbg3" else ""
        task.note(f"AI 模特换装棚拍 · 已自动选择：{tier_label}{cleanup_label}")

        ensure_pack_fn = getattr(self.comfy.packs, "ensure_pack", None)
        ensure_pack_needs_restart = False
        if callable(ensure_pack_fn):
            ensure_kwargs: dict[str, Any] = {"hf_token": hf_token}
            if progress_range is not None:
                try:
                    sig = inspect.signature(ensure_pack_fn)
                    if "progress_range" in sig.parameters or any(
                        p.kind == inspect.Parameter.VAR_KEYWORD
                        for p in sig.parameters.values()
                    ):
                        ensure_kwargs["progress_range"] = progress_range
                except (TypeError, ValueError):
                    ensure_kwargs["progress_range"] = progress_range
            raw_restart = ensure_pack_fn(task, preflight_wf, **ensure_kwargs)
            ensure_pack_needs_restart = isinstance(raw_restart, bool) and raw_restart
        else:
            self.comfy.packs._ensure_models(
                task,
                required_models,
                hf_token=hf_token,
                progress_range=progress_range,
            )

        pip_restart_fn = getattr(self.comfy.packs, "needs_pip_restart", None)
        raw_pip_restart = pip_restart_fn() if callable(pip_restart_fn) else False
        pip_needs_restart = isinstance(raw_pip_restart, bool) and raw_pip_restart

        current_pack_sig = getattr(self.comfy.packs, "active_pack_signature", "")
        comfy_lock = getattr(self.comfy, "lock", None)
        with comfy_lock if hasattr(comfy_lock, "__enter__") else nullcontext():
            loaded_pack_sig = getattr(self.comfy, "_loaded_pack_signature", "")
        sig_mismatch = (
            isinstance(current_pack_sig, str)
            and isinstance(loaded_pack_sig, str)
            and loaded_pack_sig != current_pack_sig
        )

        if self.comfy.is_running() and (
            ensure_pack_needs_restart or pip_needs_restart or sig_mismatch
        ):
            task.note("自定义节点包或依赖环境已变更，正在重启远端 ComfyUI 引擎")
            self.comfy.stop_server()
            self.comfy.start_server(task)
        elif not self.comfy.is_running():
            self.comfy.start_server(task)
        else:
            self._free_comfy_vram(
                task, "正在释放 ComfyUI 现有显存以准备 AI 模特换装棚拍"
            )
        task.check()
        wf_id = str(wf.get("id") or "").strip()
        if wf_id:
            self.comfy.mark_native_pack_ready(wf_id)
        _FASHION_ENSURE_STATE.prepared = True
        return precision, filenames

    def _free_comfy_vram(self, task, note: str = "") -> None:
        if note:
            task.note(note)
        try:
            if _has_real_free_memory(self.comfy):
                try:
                    self.comfy.free_memory(
                        unload_models=True, release_tts_warm=True
                    )
                except TypeError:
                    self.comfy.free_memory(unload_models=True)
                return
            http = _runtime_http(self.runtime)
            resp = http.post(
                self.comfy.base_url + "/free",
                json={"unload_models": True, "free_memory": True},
                timeout=15,
            )
            status_code = getattr(resp, "status_code", None)
            if (
                isinstance(status_code, int)
                and not isinstance(status_code, bool)
                and status_code >= 400
            ):
                task.note(
                    f"释放 ComfyUI 显存请求返回异常状态（HTTP {status_code}）",
                    "warning",
                )
        except Exception as exc:
            err_msg = str(exc) or type(exc).__name__
            task.note(f"释放 ComfyUI 显存失败：{err_msg[:200]}", "warning")

    def _cleanup_comfy_io_inputs(self, uploaded_names: list[str] | None) -> None:
        """Safely unlink temporary uploaded input files inside ComfyUI io_in_dir."""
        io_in_dir = getattr(self.comfy, "io_in_dir", None)
        if not isinstance(io_in_dir, (str, os.PathLike)):
            return
        base_raw = Path(io_in_dir)
        if base_raw.is_symlink() or not base_raw.is_dir():
            return
        try:
            base_resolved = base_raw.resolve()
        except OSError:
            return
        seen: set[str] = set()
        for raw_name in uploaded_names or []:
            if not isinstance(raw_name, str):
                continue
            cleaned = raw_name.strip().replace("\\", "/").strip("/")
            if not cleaned or cleaned in seen or "\x00" in cleaned:
                continue
            seen.add(cleaned)
            parts = cleaned.split("/")
            if any(not p or p in (".", "..") for p in parts):
                continue
            candidate = base_raw.joinpath(*parts)
            try:
                if candidate.is_symlink():
                    continue
                cur = candidate.parent
                has_symlink_parent = False
                while cur != base_raw and cur != cur.parent:
                    if cur.is_symlink():
                        has_symlink_parent = True
                        break
                    cur = cur.parent
                if has_symlink_parent:
                    continue
                resolved = candidate.resolve()
                if base_resolved not in resolved.parents:
                    continue
                if candidate.is_file():
                    candidate.unlink(missing_ok=True)
            except OSError:
                continue

    def _cleanup_comfy_io_output(self, raw_items: list[dict[str, Any]] | None) -> None:
        """Safely unlink temporary files written inside ComfyUI io_out_dir after persistence."""
        io_out_dir = getattr(self.comfy, "io_out_dir", None)
        if not isinstance(io_out_dir, (str, os.PathLike)):
            return
        base_raw = Path(io_out_dir)
        if base_raw.is_symlink() or not base_raw.is_dir():
            return
        try:
            base_resolved = base_raw.resolve()
        except OSError:
            return
        for item in raw_items or []:
            if not isinstance(item, dict):
                continue
            if str(item.get("type") or "output").strip().lower() != "output":
                continue
            fname = str(item.get("filename") or "").strip()
            if (
                not fname
                or "/" in fname
                or "\\" in fname
                or fname in (".", "..")
                or "\x00" in fname
            ):
                continue
            subfolder = str(item.get("subfolder") or "").strip().strip("/\\")
            rel_parts: list[str] = []
            if subfolder:
                bad_sub = False
                for part in subfolder.replace("\\", "/").split("/"):
                    if not part or part in (".", "..") or "\x00" in part:
                        bad_sub = True
                        break
                    rel_parts.append(part)
                if bad_sub:
                    continue
            candidate = base_raw.joinpath(*rel_parts, fname)
            try:
                if candidate.is_symlink():
                    continue
                cur = candidate.parent
                has_symlink_parent = False
                while cur != base_raw and cur != cur.parent:
                    if cur.is_symlink():
                        has_symlink_parent = True
                        break
                    cur = cur.parent
                if has_symlink_parent:
                    continue
                resolved = candidate.resolve()
                if base_resolved not in resolved.parents:
                    continue
                if candidate.is_file():
                    candidate.unlink(missing_ok=True)
            except OSError:
                continue

    def _clean_garment_image(
        self,
        task,
        wf: dict,
        *,
        src_path: Path,
        label: str,
        file_stem: str = "",
        work_dir: Path,
        garment_cleanup: str,
        garment_notes: str,
        filenames: dict[str, str],
        seed: int,
        progress_range: tuple[float, float],
        uploaded_tracker: list[str] | None = None,
    ) -> Path:
        task.check()
        raw_bytes = src_path.read_bytes()
        cleanup = str(garment_cleanup or "rmbg3").strip().lower()
        if cleanup == "off":
            return src_path

        stem = _SAFE_STEM_RE.sub("", str(file_stem or label)).strip("_-") or "garment"
        if cleanup != "rmbg3":
            trimmed_bytes = auto_trim_ecommerce_borders(raw_bytes)
            trimmed_path = work_dir / f"{stem}_trimmed.png"
            trimmed_path.write_bytes(trimmed_bytes)
            return trimmed_path

        # Skip auto_trim_ecommerce_borders before RMBG 3.0 so white garments and thin straps
        # are never pre-clipped; only normalize orientation/alpha onto opaque RGB PNG.
        prepared_rgb = _decode_garment_rgb_on_white(raw_bytes)
        prepared_buf = io.BytesIO()
        prepared_rgb.save(prepared_buf, format="PNG")
        prepared_bytes = prepared_buf.getvalue()
        prepared_path = work_dir / f"{stem}_prepared.png"
        prepared_path.write_bytes(prepared_bytes)

        task.note(
            f"正在使用 Bria RMBG 3.0 对「{label}」执行软透明抠衣清洗（保留薄纱与羽毛细节）"
        )
        task.check()
        uploaded_name = self.comfy._upload_input_file(prepared_path.name, prepared_path)
        if uploaded_tracker is not None and uploaded_name:
            uploaded_tracker.append(uploaded_name)
        rmbg_prompt = build_rmbg3_garment_matte_prompt(
            uploaded_name,
            rmbg3_dit=filenames["rmbg3_dit"],
            rmbg3_clip=filenames["rmbg3_clip"],
            rmbg3_vae=filenames["rmbg3_vae"],
            garment_notes=garment_notes,
            seed=seed,
        )
        raw_results = self.comfy._execute_single_prompt(
            task,
            wf,
            rmbg_prompt,
            progress_range=progress_range,
            segment_label=f"智能抠衣清洗（{label}）",
            save_to_store=False,
            output_dir=work_dir,
        )
        try:
            task.check()
            if not raw_results:
                task.note(
                    f"Bria RMBG 3.0 未返回遮罩，已回退使用原图继续生成「{label}」",
                    "warning",
                )
                return prepared_path

            matte_path = Path(raw_results[0]["path"])
            try:
                cleaned_bytes = composite_soft_alpha_on_white(
                    prepared_bytes, matte_path.read_bytes()
                )
            except (OSError, ValueError) as exc:
                task.note(
                    f"Bria RMBG 3.0 返回的遮罩图像异常（{exc}），已回退使用原图继续生成「{label}」",
                    "warning",
                )
                return prepared_path
            cleaned_path = work_dir / f"{stem}_cleaned.png"
            cleaned_path.write_bytes(cleaned_bytes)
            return cleaned_path
        finally:
            self._cleanup_comfy_io_output(raw_results)

    def run(
        self,
        task,
        wf: dict,
        req: WorkflowRunRequest,
        resolved_inputs: list[tuple[str, str, Path]],
    ) -> list[dict]:
        started = time.monotonic()
        overrides = dict(req.overrides or {})
        input_map: dict[str, tuple[str, Path]] = {
            field_key: (fname, fpath) for field_key, fname, fpath in resolved_inputs
        }

        model_entries, garment_entries = resolve_colabone_fashion_photo_entries(
            input_map, overrides
        )
        if not model_entries:
            raise ValueError("请先上传「模特正面照片」后再开始生成")
        if not garment_entries:
            raise ValueError("请先上传「服装主图（正面）」后再开始生成")

        # Validate all input image files upfront BEFORE downloading 35-88 GiB weights.
        for entry in model_entries:
            _validate_fashion_image_input(entry["path"], f"模特参考图「{entry['filename']}」")
        for entry in garment_entries:
            _validate_fashion_image_input(entry["path"], f"服装参考图「{entry['filename']}」")
        if "pipeline.scene_ref_photo" in input_map:
            scene_fn, scene_fp = input_map["pipeline.scene_ref_photo"]
            _validate_fashion_image_input(scene_fp, f"棚拍场景参考图「{scene_fn}」")

        shots = plan_colabone_fashion_shots(overrides)
        width, height = resolve_colabone_fashion_dimensions(
            str(overrides.get("pipeline.aspect_preset") or "3x4_2k")
        )
        scene_preset = str(overrides.get("pipeline.scene_preset") or "studio_light_gray").strip().lower()
        garment_cleanup = str(overrides.get("pipeline.garment_cleanup") or "rmbg3").strip().lower()
        save_cleaned_garment = _parse_bool(overrides.get("pipeline.save_cleaned_garment"), False)
        garment_notes = str(overrides.get("pipeline.garment_notes") or "").strip()[
            :COLABONE_FASHION_MAX_USER_PROMPT_CHARS
        ]
        keep_accessories = _parse_bool(overrides.get("pipeline.keep_accessories"), False)
        styling_match = str(overrides.get("pipeline.styling_match") or "auto_match").strip().lower()
        color_fidelity = str(overrides.get("pipeline.color_fidelity") or "strict").strip().lower()
        custom_style_prompt = str(overrides.get("pipeline.custom_style_prompt") or "").strip()[
            :COLABONE_FASHION_MAX_USER_PROMPT_CHARS
        ]
        anchor_hero_shot = _parse_bool(overrides.get("pipeline.anchor_hero_shot"), True)
        steps = _parse_int(overrides.get("pipeline.steps"), 6, min_val=4, max_val=16)
        base_seed = _parse_int(overrides.get("pipeline.seed"), 42, min_val=0, max_val=2_147_483_647)

        # Only download/run RMBG 3.0 if at least one non-detail garment photo needs soft matting.
        has_matte_garment = any(entry["role"] != "detail" for entry in garment_entries)
        effective_cleanup_for_models = garment_cleanup if has_matte_garment else "off"
        ready_overrides = dict(overrides)
        ready_overrides["pipeline.garment_cleanup"] = effective_cleanup_for_models

        hf_token = str(getattr(req, "hf_token", "") or "")
        _FASHION_ENSURE_STATE.prepared = False
        _, filenames = self.ensure_ready(
            task,
            wf,
            hf_token=hf_token,
            overrides=ready_overrides,
            progress_range=(0.02, 0.24),
        )
        ensured_fresh_or_freed = bool(
            getattr(_FASHION_ENSURE_STATE, "prepared", False)
        )

        with tempfile.TemporaryDirectory(prefix="colabone-fashion-") as tmp_str:
            work_dir = Path(tmp_str)
            saved_records: list[dict] = []
            cutout_count = 0
            lookbook_count = 0
            task_lock = getattr(task, "lock", None)
            uploaded_io_inputs: list[str] = []

            try:
                # Stage 1: Clean garment photos (detail close-ups automatically skip matting to protect macro texture).
                clean_targets = [
                    (idx, entry)
                    for idx, entry in enumerate(garment_entries)
                    if entry["role"] != "detail"
                ]
                clean_total = max(1, len(clean_targets))
                clean_lo, clean_hi = 0.24, 0.37
                clean_span = clean_hi - clean_lo
                cleaned_garment_paths: dict[str, Path] = {}
                attempted_rmbg3 = False
                rmbg3_completed_ok = False
                clean_step_idx = 0
                back_total = sum(1 for e in garment_entries if e["role"] == "back")
                back_seen = 0
                front_seen = 0
                task_id_str = str(getattr(task, "id", "") or "")

                if (
                    not ensured_fresh_or_freed
                    and garment_cleanup == "rmbg3"
                    and clean_targets
                    and self.comfy.is_running()
                ):
                    self._free_comfy_vram(
                        task, "正在释放 ComfyUI 现有显存以加载 Bria RMBG 3.0 软透明抠衣模型"
                    )

                try:
                    for idx, entry in enumerate(garment_entries):
                        task.check()
                        slot_key = entry["key"]
                        role = entry["role"]
                        if role == "detail":
                            cleaned_garment_paths[slot_key] = entry["path"]
                            task.note(
                                f"服装细节特写「{entry['filename']}」已自动跳过抠图，完整保留微距纹理"
                            )
                            continue

                        step_lo = clean_lo + clean_span * (clean_step_idx / clean_total)
                        step_hi = clean_lo + clean_span * ((clean_step_idx + 1) / clean_total)
                        clean_step_idx += 1
                        if role == "back":
                            back_seen += 1
                            role_label = (
                                f"服装背面参考图 {back_seen}"
                                if back_total > 1
                                else "服装背面参考图"
                            )
                        else:
                            front_seen += 1
                            role_label = (
                                "服装正面主图"
                                if front_seen == 1
                                else f"服装参考图 {idx + 1}"
                            )
                        clean_started = time.monotonic()
                        if garment_cleanup == "rmbg3":
                            attempted_rmbg3 = True
                        cleaned_path = self._clean_garment_image(
                            task,
                            wf,
                            src_path=entry["path"],
                            label=role_label,
                            file_stem=f"garment_{idx + 1:02d}",
                            work_dir=work_dir,
                            garment_cleanup=garment_cleanup,
                            garment_notes=garment_notes,
                            filenames=filenames,
                            seed=(base_seed + idx * 101) % 2_147_483_648,
                            progress_range=(step_lo, step_hi),
                            uploaded_tracker=uploaded_io_inputs,
                        )
                        cleaned_garment_paths[slot_key] = cleaned_path
                        if save_cleaned_garment and cleaned_path != entry["path"]:
                            task.check()
                            safe_label = _SAFE_STEM_RE.sub("", role_label).strip("_-") or f"服装{idx + 1}"
                            inspect_record = self.comfy.store.save_output_file(
                                src_path=cleaned_path,
                                workflow_id=wf["id"],
                                workflow_name=wf["name"],
                                filename=f"00_抠衣质检_{safe_label}.png",
                                elapsed=max(0.0, time.monotonic() - clean_started),
                                prompt_id="",
                                task_id=task_id_str,
                            )
                            saved_records.append(inspect_record)
                            cutout_count += 1
                            if task_lock is not None:
                                with task_lock:
                                    task.outputs.append(inspect_record)
                            else:
                                task.outputs.append(inspect_record)
                    rmbg3_completed_ok = True
                finally:
                    if attempted_rmbg3:
                        free_note = (
                            "服装软透明抠图完成，正在释放 RMBG 3.0 显存以加载 MiniMax-H3-Image"
                            if rmbg3_completed_ok
                            else "服装软透明抠图中断，正在释放 RMBG 3.0 显存"
                        )
                        self._free_comfy_vram(task, free_note)

                # Upload all persistent reference images once to ComfyUI.
                task.check()
                task.note("正在向远端 ComfyUI 上传模特与多视角服装参考素材")
                uploaded_models: list[dict[str, str]] = []
                for m_idx, entry in enumerate(model_entries, 1):
                    task.check()
                    up_fname, up_path = _prepare_opaque_rgb_upload(
                        entry["filename"],
                        entry["path"],
                        stem=f"model_{m_idx:02d}",
                        work_dir=work_dir,
                    )
                    uploaded_name = self.comfy._upload_input_file(up_fname, up_path)
                    if uploaded_name:
                        uploaded_io_inputs.append(uploaded_name)
                    uploaded_models.append(
                        {
                            "key": entry["key"],
                            "role": entry["role"],
                            "comfy_name": uploaded_name,
                        }
                    )

                uploaded_garments: list[dict[str, str]] = []
                for g_idx, entry in enumerate(garment_entries, 1):
                    task.check()
                    cpath = cleaned_garment_paths[entry["key"]]
                    if cpath == entry["path"]:
                        upload_fname, upload_path = _prepare_opaque_rgb_upload(
                            entry["filename"],
                            cpath,
                            stem=f"garment_raw_{g_idx:02d}",
                            work_dir=work_dir,
                        )
                    else:
                        upload_fname, upload_path = cpath.name, cpath
                    uploaded_name = self.comfy._upload_input_file(upload_fname, upload_path)
                    if uploaded_name:
                        uploaded_io_inputs.append(uploaded_name)
                    uploaded_garments.append(
                        {
                            "key": entry["key"],
                            "role": entry["role"],
                            "comfy_name": uploaded_name,
                        }
                    )

                uploaded_scene: str | None = None
                if "pipeline.scene_ref_photo" in input_map:
                    task.check()
                    fn, fp = input_map["pipeline.scene_ref_photo"]
                    up_fn, up_fp = _prepare_opaque_rgb_upload(
                        fn, fp, stem="scene_ref", work_dir=work_dir
                    )
                    uploaded_scene = self.comfy._upload_input_file(up_fn, up_fp)
                    if uploaded_scene:
                        uploaded_io_inputs.append(uploaded_scene)

                back_garments = [g for g in uploaded_garments if g["role"] == "back"]
                detail_garments = [g for g in uploaded_garments if g["role"] == "detail"]
                front_like_garments = [
                    g for g in uploaded_garments if g["role"] not in ("back", "detail")
                ]

                # Stage 2: Generate Hero Master first, then multi-pose expansion.
                total_shots = len(shots)
                gen_lo, gen_hi = 0.38, 0.98
                gen_span = gen_hi - gen_lo
                hero_comfy_name: str | None = None
                hero_view = "front"

                for idx, shot in enumerate(shots):
                    task.check()
                    shot_num = idx + 1
                    shot_lo = gen_lo + gen_span * (idx / total_shots)
                    shot_hi = gen_lo + gen_span * ((idx + 1) / total_shots)
                    shot_view = str(shot.get("view") or "front").strip().lower()

                    ref_list: list[str] = []
                    ref_roles: list[tuple[int, str, str]] = []

                    def _push_ref(img_name: str, role: str, angle: str = "auto") -> None:
                        ref_list.append(img_name)
                        ref_roles.append((len(ref_list), role, angle))

                    # Anchor to Hero Master on follow-up poses for rock-solid consistency.
                    if anchor_hero_shot and hero_comfy_name:
                        _push_ref(hero_comfy_name, "hero", hero_view)

                    for m in uploaded_models:
                        _push_ref(m["comfy_name"], "model", m["role"])

                    if shot_view == "back" and back_garments:
                        for g in back_garments:
                            _push_ref(g["comfy_name"], "garment_back", "back")
                        for g in detail_garments:
                            _push_ref(g["comfy_name"], "garment_detail", "detail")
                    else:
                        primary_garments = front_like_garments or back_garments
                        for g in primary_garments:
                            _push_ref(g["comfy_name"], "garment_front", g["role"])
                        for g in detail_garments:
                            _push_ref(g["comfy_name"], "garment_detail", "detail")

                    if uploaded_scene:
                        _push_ref(uploaded_scene, "scene_ref", "auto")

                    prompt_text = build_minimax_h3_tryon_prompt_text(
                        shot=shot,
                        ref_roles=ref_roles,
                        scene_preset=scene_preset,
                        garment_notes=garment_notes,
                        keep_accessories=keep_accessories,
                        styling_match=styling_match,
                        color_fidelity=color_fidelity,
                        custom_style_prompt=custom_style_prompt,
                    )
                    shot_seed = (base_seed + idx) % 2_147_483_648
                    h3_graph = build_minimax_h3_tryon_workflow(
                        reference_images=ref_list,
                        prompt_text=prompt_text,
                        width=width,
                        height=height,
                        minimax_dit=filenames["minimax_dit"],
                        minimax_clip=filenames["minimax_clip"],
                        minimax_vae=filenames["minimax_vae"],
                        steps=steps,
                        seed=shot_seed,
                        filename_prefix=f"colabone_fashion_{shot['id']}",
                    )

                    shot_started = time.monotonic()
                    seg_label = f"第 {shot_num}/{total_shots} 张 · {shot['label']}"
                    raw_out = self.comfy._execute_single_prompt(
                        task,
                        wf,
                        h3_graph,
                        progress_range=(shot_lo, shot_hi),
                        segment_label=seg_label,
                        save_to_store=False,
                        output_dir=work_dir,
                    )
                    try:
                        task.check()
                        if not raw_out:
                            raise RuntimeError(f"第 {shot_num} 张「{shot['label']}」未返回生成图像")

                        generated_path = Path(raw_out[0]["path"])
                        shot_elapsed = max(0.0, time.monotonic() - shot_started)
                        friendly_filename = f"{shot['filename_stem']}.png"
                        record = self.comfy.store.save_output_file(
                            src_path=generated_path,
                            workflow_id=wf["id"],
                            workflow_name=wf["name"],
                            filename=friendly_filename,
                            elapsed=shot_elapsed,
                            prompt_id=str(raw_out[0].get("prompt_id") or ""),
                            task_id=task_id_str,
                        )
                        saved_records.append(record)
                        lookbook_count += 1
                        if task_lock is not None:
                            with task_lock:
                                task.outputs.append(record)
                        else:
                            task.outputs.append(record)

                        if (
                            hero_comfy_name is None
                            and shot_num < total_shots
                            and anchor_hero_shot
                            and shot_view in ("front", "side")
                        ):
                            task.check()
                            hero_comfy_name = self.comfy._upload_input_file(
                                "colabone_hero_master.png", generated_path
                            )
                            if hero_comfy_name:
                                uploaded_io_inputs.append(hero_comfy_name)
                            hero_view = shot_view
                            task.note(
                                f"第 {shot_num} 张定妆主图已锁定为视觉锚点，正在保持同人同衣同光影展开其余姿势"
                            )
                    finally:
                        self._cleanup_comfy_io_output(raw_out)
            finally:
                self._cleanup_comfy_io_inputs(uploaded_io_inputs)

            total_elapsed = format_colabone_duration_zh(
                max(0.0, time.monotonic() - started), subsecond=True
            )
            if task_lock is not None:
                with task_lock:
                    task.progress = 1
            else:
                task.progress = 1
            if cutout_count > 0:
                task.note(
                    f"AI 模特换装棚拍完成：已保存 {lookbook_count} 张高清多姿态成片与 {cutout_count} 张抠衣质检图（耗时 {total_elapsed}）"
                )
            else:
                task.note(
                    f"AI 模特换装棚拍完成：已保存 {lookbook_count} 张高清多姿态成片（耗时 {total_elapsed}）"
                )
            return saved_records
