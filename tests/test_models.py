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
