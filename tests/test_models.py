import pytest
from pydantic import ValidationError
from common.models import (
    COLABONE_AUTO_API_MODEL_ID,
    ModelSpec,
    colabone_default_api_model_id,
    colabone_unique_api_model_id,
    resolve_colabone_api_model_id,
    resolve_import,
    validate_colabone_api_model_id,
)
from unittest.mock import Mock


@pytest.mark.parametrize(
    "field,value",
    [
        ("path", "../evil.gguf"),
        ("path", "/evil.gguf"),
        ("path", "x\\evil.gguf"),
        ("revision", "main"),
        ("repo", "https://evil.test/secret"),
        ("sha256", "bad"),
    ],
)
def test_reject_unsafe_manifest(spec, field, value):
    data = spec.model_dump()
    data["files"][0][field] = value
    with pytest.raises(ValidationError):
        ModelSpec.model_validate(data)


def test_source_code_is_not_a_model_field(spec):
    with pytest.raises(ValidationError):
        ModelSpec.model_validate(spec.model_dump() | {"code": 'print("unsafe")'})


@pytest.mark.parametrize(
    "override",
    [
        {"kind": "image"},
        {"kind": "video"},
        {"recipe": "z-image-turbo"},
    ],
)
def test_reject_removed_creative_model_kinds_and_recipes(spec, override):
    with pytest.raises(ValidationError):
        ModelSpec.model_validate(spec.model_dump() | override)


def test_reject_non_gguf_and_non_llm_roles(spec):
    bad_ext = spec.model_dump()
    bad_ext["files"][0]["path"] = "weights.safetensors"
    with pytest.raises(ValidationError):
        ModelSpec.model_validate(bad_ext)

    bad_role = spec.model_dump()
    bad_role["files"][0]["role"] = "vae"
    with pytest.raises(ValidationError):
        ModelSpec.model_validate(bad_role)


def test_import_pins_commit_and_all_shards():
    session = Mock()
    session.get.return_value.json.return_value = {
        "sha": "b" * 40,
        "siblings": [
            {
                "rfilename": f"model-{i:05}-of-00002.gguf",
                "lfs": {"sha256": str(i) * 64, "size": 16},
            }
            for i in (1, 2)
        ],
    }
    spec = resolve_import("org/model", "model-00002-of-00002.gguf", session=session)
    assert len(spec.files) == 2 and spec.files[0].role == "model"
    assert all(f.revision == "b" * 40 for f in spec.files)
    assert session.get.call_count == 1
    assert "/api/models/" in session.get.call_args.args[0]


def test_resolve_import_estimates_vram_and_blocks_auxiliary_or_comfy_models():
    session = Mock()
    session.get.return_value.json.return_value = {
        "sha": "c" * 40,
        "pipeline_tag": "text-generation",
        "siblings": [
            {
                "rfilename": "Qwen3-32B-Q8_0.gguf",
                "lfs": {"sha256": "a" * 64, "size": 32 * 1024**3},
            },
            {
                "rfilename": "imatrix_micro3.gguf",
                "lfs": {"sha256": "b" * 64, "size": 1024**3},
            },
        ],
    }
    spec = resolve_import("org/Qwen3-32B-GGUF", "Qwen3-32B-Q8_0.gguf", session=session)
    # Community imports leave min_vram_mib at 0 so runtime can use GPU+RAM hybrid --fit on when larger than VRAM.
    assert spec.min_vram_mib == 0
    assert "Q8_0" in spec.description

    with pytest.raises(ValueError, match="辅助文件"):
        resolve_import("org/Qwen3-32B-GGUF", "imatrix_micro3.gguf", session=session)

    session.get.return_value.json.return_value = {
        "sha": "d" * 40,
        "pipeline_tag": "text-to-video",
        "siblings": [
            {
                "rfilename": "wan2.1-Q4_K_M.gguf",
                "lfs": {"sha256": "e" * 64, "size": 8 * 1024**3},
            }
        ],
    }
    with pytest.raises(ValueError, match="ComfyUI"):
        resolve_import("city96/Wan2.1-T2V-14B-gguf", "wan2.1-Q4_K_M.gguf", session=session)


@pytest.mark.parametrize(
    "repo,filename,expected",
    [
        (
            "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
            "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00004.gguf",
            "colabone/qwen3.8-flash-next",
        ),
        (
            "huihui-ai/Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
            "DeepSeek-V4.1-Flash-Q4_K_M.gguf",
            "colabone/deepseek-v4.1-flash",
        ),
        (
            "mradermacher/gemma-4-31B-it-ARA-heretic-GGUF",
            "gemma-4-31B-it-ARA-heretic.Q8_0.gguf",
            "colabone/gemma-4-31b",
        ),
        (
            "0bserverx/Qwen3.8-27B-Heretic-Abliterated-Uncensored-GGUF",
            "RVN-BF16.gguf",
            "colabone/qwen3.8-27b",
        ),
        (
            "GCSA-AiLab/GLM-5.3-Flash-Uncensored-RCO-GSQ-GGUF",
            "Q4/GLM-5.3-Flash-Uncensored-GSQ-RCO-Q4.gguf",
            "colabone/glm-5.3-flash",
        ),
        (
            "unsloth/Qwen3-30B-A3B-Instruct-2507-GGUF",
            "Qwen3-30B-A3B-UD-Q4_K_XL.gguf",
            "colabone/qwen3-30b-a3b",
        ),
        (
            "bartowski/Llama-3.3-70B-Instruct-GGUF",
            "Llama-3.3-70B-Instruct-Q4_K_M.gguf",
            "colabone/llama-3.3-70b",
        ),
        (
            "org/Qwen3_32B_Q4_K_M_GGUF",
            "Qwen3_32B_Q4_K_M.gguf",
            "colabone/qwen3-32b",
        ),
        (
            "org/gguf",
            "model.gguf",
            "colabone/gguf",
        ),
    ],
)
def test_colabone_default_api_model_id_is_clean_and_readable(repo, filename, expected):
    assert colabone_default_api_model_id(repo=repo, filename=filename) == expected


def test_colabone_unique_api_model_id_disambiguates_only_on_collision():
    base = "colabone/qwen3.8-flash-next"
    assert (
        colabone_unique_api_model_id(
            base,
            [],
            repo="huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
            filename="Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
        )
        == base
    )
    second = colabone_unique_api_model_id(
        base,
        [base],
        repo="huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        filename="Qwen3.8-Flash-Next-Q8_0.gguf",
    )
    assert second == "colabone/qwen3.8-flash-next-q8"

    third = colabone_unique_api_model_id(
        base,
        [base, "colabone/qwen3.8-flash-next-q4"],
        repo="huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        filename="Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
    )
    assert third == "colabone/qwen3.8-flash-next-ud-q4_k_xl"

    fourth = colabone_unique_api_model_id(
        base,
        [base, "colabone/qwen3.8-flash-next-q4", "colabone/qwen3.8-flash-next-ud-q4_k_xl"],
        repo="huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        filename="Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
    )
    assert fourth == "colabone/qwen3.8-flash-next-huihui"


def test_validate_and_resolve_api_model_id(spec):
    assert validate_colabone_api_model_id("colabone/qwen3.8-flash-next") == "colabone/qwen3.8-flash-next"
    assert validate_colabone_api_model_id("my-coder") == "my-coder"
    assert validate_colabone_api_model_id("", allow_empty=True) == ""

    for bad in (COLABONE_AUTO_API_MODEL_ID, "COLABONE/AUTO", "../evil", "a//b", "colabone/", "colabone.", "bad id"):
        with pytest.raises(ValueError):
            validate_colabone_api_model_id(bad)

    # Non-hf spec without explicit api_id falls back to its id.
    assert resolve_colabone_api_model_id(spec) == "test-model"

    # Legacy hf-* spec without saved api_id auto-derives a readable colabone/* ID.
    legacy_hf = spec.model_copy(
        update={
            "id": "hf-82b42fca20f78bbf",
            "api_id": "",
            "name": "Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
            "files": [
                spec.files[0].model_copy(
                    update={
                        "repo": "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
                        "path": "Qwen3.8-Flash-Next-UD-Q4_K_XL.gguf",
                    }
                )
            ],
        }
    )
    assert resolve_colabone_api_model_id(legacy_hf) == "colabone/qwen3.8-flash-next"

    # Explicit custom api_id always wins.
    custom = legacy_hf.model_copy(update={"api_id": "colabone/my-qwen"})
    assert resolve_colabone_api_model_id(custom) == "colabone/my-qwen"


def test_resolve_import_populates_readable_api_id():
    session = Mock()
    session.get.return_value.json.return_value = {
        "sha": "e" * 40,
        "pipeline_tag": "text-generation",
        "siblings": [
            {
                "rfilename": "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00002.gguf",
                "lfs": {"sha256": "1" * 64, "size": 1024},
            },
            {
                "rfilename": "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00002-of-00002.gguf",
                "lfs": {"sha256": "2" * 64, "size": 1024},
            },
        ],
    }
    imported = resolve_import(
        "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        "unsloth-UD-Q4_K_XL-V2/Qwen3.8-Flash-Next-UD-Q4_K_XL-00001-of-00002.gguf",
        session=session,
    )
    assert imported.id.startswith("hf-")
    assert imported.api_id == "colabone/qwen3.8-flash-next"


def test_default_api_id_guards_reserved_auto_and_consecutive_dots_and_dot_segments():
    auto_id = colabone_default_api_model_id(
        repo="org/Auto-GGUF", filename="Auto-Q4_K_M.gguf"
    )
    assert auto_id != "colabone/auto"
    assert validate_colabone_api_model_id(auto_id) == auto_id

    dots_id = colabone_default_api_model_id(
        repo="org/foo..bar-GGUF", filename="foo..bar-Q4_K_M.gguf"
    )
    assert ".." not in dots_id
    assert validate_colabone_api_model_id(dots_id) == dots_id

    with pytest.raises(ValueError):
        validate_colabone_api_model_id("colabone/./foo")
    with pytest.raises(ValueError):
        validate_colabone_api_model_id("a/./b")
