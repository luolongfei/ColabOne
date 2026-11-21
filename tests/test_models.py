import pytest
from pydantic import ValidationError
from common.models import ModelSpec, resolve_import
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
