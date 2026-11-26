"""Cross-language contract tests between the Go desktop binary and Python remote service.

All tests run 100% offline and verify:
- Byte-for-byte and SHA-256 revision parity between `server/`, `common/`, `catalog/`
  and the Go-embedded mirror at `console/internal/servicebundle/embedded/`.
- Schema validity of `catalog/presets.json`, `catalog/comfyui.json`, and pinned
  remote asset catalogs under upgraded Pydantic 2.14+.
- Offline FastAPI boot, Bearer auth, protocol 7 status/snapshot, and validation
  contracts of `server/app.py` under upgraded FastAPI 0.165+ / Pydantic 2.14+.
- Requirements pin/SHA-256 hash hygiene (`server/requirements.in` -> `server/requirements.txt`,
  `requirements-dev.txt`), removal of deleted `bridge/` references in `tools/`,
  documentation hygiene (zero `VPN` mentions), and `tools/live_acceptance.py`
  loopback `/api/v1/` route contracts.
"""

from __future__ import annotations

import ast
import base64
import hashlib
import io
import json
import os
import re
import urllib.parse
import urllib.request
from pathlib import Path

import fastapi
import pydantic
import pytest
from fastapi.testclient import TestClient
from PIL import Image
from starlette.routing import Match

from common.comfyui import ComfyCatalog
from common.files import (
    COLABONE_SERVICE_BUNDLE_FOLDERS,
    COLABONE_SERVICE_BUNDLE_SUFFIXES,
    colabone_service_revision,
)
from common.live_studio import (
    ColabOneLiveAvatarSaveRequest,
    ColabOneLiveBuildPackRequest,
    ColabOneLiveClipUploadRequest,
    ColabOneLiveDrivingUploadRequest,
    ColabOneLiveExternalLLMTestRequest,
    ColabOneLiveSessionConfig,
    ColabOneLiveStartRequest,
)
from common.models import (
    COLABONE_AUTO_API_MODEL_ID,
    ModelSpec,
    validate_colabone_api_model_id,
)
from server.app import create_colabone_app
from server.runtime import ColabOneRuntime
from tools import build_fonts, live_acceptance

ROOT = Path(__file__).resolve().parents[1]
EMBEDDED_ROOT = ROOT / "console" / "internal" / "servicebundle" / "embedded"
_HEX_40_RE = re.compile(r"^[a-f0-9]{40}$")
_HEX_64_RE = re.compile(r"^[a-f0-9]{64}$")
_HEX_16_RE = re.compile(r"^[a-f0-9]{16}$")
_REQ_PIN_RE = re.compile(r"^([A-Za-z0-9][A-Za-z0-9._-]*)==([^\s;#]+)$")
_REQ_SHA256_HASH_RE = re.compile(r"^--hash=sha256:[a-f0-9]{64}$")
_BRIDGE_WORD_RE = re.compile(r"\bbridge\b", re.IGNORECASE)


def _collect_bundle_files(base: Path) -> dict[str, bytes]:
    files: dict[str, bytes] = {}
    for folder in COLABONE_SERVICE_BUNDLE_FOLDERS:
        folder_dir = base / folder
        if not folder_dir.is_dir():
            continue
        for path in sorted(folder_dir.glob("*"), key=lambda p: p.name):
            if path.is_file() and path.suffix in COLABONE_SERVICE_BUNDLE_SUFFIXES:
                files[f"{folder}/{path.name}"] = path.read_bytes()
    return files


def _collect_all_embedded_files(base: Path) -> set[str]:
    result: set[str] = set()
    for path in base.rglob("*"):
        if not path.is_file() or "__pycache__" in path.parts:
            continue
        result.add(path.relative_to(base).as_posix())
    return result


def test_embedded_service_bundle_matches_source_tree_and_revision():
    assert EMBEDDED_ROOT.is_dir(), (
        "Missing embedded service bundle at console/internal/servicebundle/embedded/; "
        "run tools/sync_service_bundle.py"
    )

    source_files = _collect_bundle_files(ROOT)
    embedded_files = _collect_bundle_files(EMBEDDED_ROOT)
    all_embedded_paths = _collect_all_embedded_files(EMBEDDED_ROOT)

    assert source_files, "Expected remote service files under server/, common/, catalog/"
    assert set(embedded_files.keys()) == set(source_files.keys()), (
        "Embedded service bundle file list diverged from server/, common/, catalog/; "
        "run tools/sync_service_bundle.py"
    )
    assert all_embedded_paths == set(source_files.keys()), (
        "Unexpected extra or unsynced files inside console/internal/servicebundle/embedded/"
    )

    # Confirm non-bundle source files like requirements.in are excluded.
    assert "server/requirements.txt" in embedded_files
    assert "server/requirements.in" not in all_embedded_paths

    for rel, expected_bytes in source_files.items():
        actual_bytes = embedded_files[rel]
        assert actual_bytes == expected_bytes, (
            f"Embedded service file out of sync: {rel}; run tools/sync_service_bundle.py"
        )

    src_rev = colabone_service_revision(ROOT)
    emb_rev = colabone_service_revision(EMBEDDED_ROOT)
    assert _HEX_16_RE.fullmatch(src_rev)
    assert emb_rev == src_rev

    # Explicitly lock the exact Go/Python SHA-256 revision hashing format:
    # folder order ("server", "common", "catalog"), sorted filename order,
    # "<folder>/<name>\0<raw-bytes>\0", first 16 lowercase hex chars.
    hasher = hashlib.sha256()
    for folder in ("server", "common", "catalog"):
        folder_keys = sorted(
            k for k in source_files if k.startswith(f"{folder}/")
        )
        for rel in folder_keys:
            hasher.update(rel.encode("utf-8"))
            hasher.update(b"\0")
            hasher.update(source_files[rel])
            hasher.update(b"\0")
    assert src_rev == hasher.hexdigest()[:16]


def test_catalog_presets_validate_with_model_spec():
    presets_path = ROOT / "catalog" / "presets.json"
    raw = json.loads(presets_path.read_text(encoding="utf-8"))
    assert isinstance(raw, list) and len(raw) > 0

    seen_ids: set[str] = set()
    seen_api_ids: set[str] = set()
    for entry in raw:
        spec = ModelSpec.model_validate(entry)
        assert spec.id not in seen_ids, f"Duplicate preset model id: {spec.id}"
        seen_ids.add(spec.id)

        assert spec.api_id, f"Preset {spec.id} must define a default api_id"
        normalized_api_id = validate_colabone_api_model_id(spec.api_id)
        assert normalized_api_id.lower() != COLABONE_AUTO_API_MODEL_ID
        assert normalized_api_id.lower() not in seen_api_ids, (
            f"Duplicate preset api_id: {normalized_api_id}"
        )
        seen_api_ids.add(normalized_api_id.lower())

        assert spec.kind == "chat"
        assert sum(f.role == "model" for f in spec.files) == 1
        for mf in spec.files:
            assert mf.path.lower().endswith(".gguf")
            assert _HEX_40_RE.fullmatch(mf.revision.lower())
            assert _HEX_64_RE.fullmatch(mf.sha256)
            assert mf.size > 0


def test_catalog_comfyui_and_pinned_remote_assets_validate():
    comfy_raw = json.loads((ROOT / "catalog" / "comfyui.json").read_text(encoding="utf-8"))
    catalog = ComfyCatalog.model_validate(comfy_raw)
    assert catalog.port == 8188
    assert catalog.proxy_port == 8189
    assert _HEX_40_RE.fullmatch(catalog.commit.lower())
    assert catalog.tag.startswith("v")

    for name in ("cloudflared.json", "rclone.json"):
        data = json.loads((ROOT / "catalog" / name).read_text(encoding="utf-8"))
        assert isinstance(data.get("tag"), str) and data["tag"]
        hashes = data.get("sha256")
        assert isinstance(hashes, dict) and {"amd64", "arm64"}.issubset(hashes.keys())
        for arch, digest in hashes.items():
            assert _HEX_64_RE.fullmatch(str(digest).lower()), (
                f"Invalid sha256 for {name} ({arch})"
            )

    engine = json.loads((ROOT / "catalog" / "engine.json").read_text(encoding="utf-8"))
    assert isinstance(engine.get("tag"), str) and engine["tag"]
    assets = engine.get("assets")
    assert isinstance(assets, list) and len(assets) >= 1
    for asset in assets:
        assert str(asset.get("url", "")).startswith("https://github.com/ggml-org/llama.cpp/")
        assert _HEX_64_RE.fullmatch(str(asset.get("sha256", "")).lower())


def test_server_app_boots_offline_with_protocol_7_and_auth_contract(tmp_path, monkeypatch):
    with pytest.raises(ValueError):
        create_colabone_app(None, "too-short")

    runtime = ColabOneRuntime(tmp_path / "remote", ROOT / "catalog")
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "Tesla T4", "total_mib": 15360, "used_mib": 256, "utilization": 0},
    )
    token = "colabone-contract-test-token-0123456789abcdef"
    headers = {"Authorization": f"Bearer {token}"}
    app = create_colabone_app(runtime, token, background=False)

    with TestClient(app) as client:
        # Unauthenticated health endpoint.
        health = client.get("/health")
        assert health.status_code == 200
        assert health.json() == {"ok": True, "version": "0.1.0"}

        # Interactive docs/OpenAPI must stay disabled on remote control server.
        assert client.get("/docs").status_code == 404
        assert client.get("/openapi.json").status_code == 404

        # Control endpoints enforce constant-time Bearer token auth.
        assert client.get("/api/v1/status").status_code == 401
        assert (
            client.get("/api/v1/status", headers={"Authorization": "Bearer wrong"}).status_code
            == 401
        )
        assert client.get("/api/v1/snapshot").status_code == 401

        # Protocol 7 status payload contract.
        status_resp = client.get("/api/v1/status", headers=headers)
        assert status_resp.status_code == 200
        status = status_resp.json()
        assert status["protocol"] == 7
        assert status["service_revision"] == colabone_service_revision(ROOT)
        assert status["api_model_aliases"] is True
        assert status["capabilities"] == [
            "public_api",
            "comfyui",
            "comfyui_hf_token",
            "comfyui_drive_export",
            "comfyui_tts_preview",
            "comfyui_longcat_native",
            "comfyui_workflow_auto_release",
            "comfyui_live_studio",
        ]
        assert status["state"] == "ready"
        assert status["idle_minutes"] == 30
        assert status["idle"]["phase"] in ("watching", "countdown", "active")
        assert isinstance(status["public_api"], dict)
        assert isinstance(status["comfyui"], dict)
        assert {"gpu", "memory", "disk", "cpu"}.issubset(status["resources"].keys())

        # Snapshot payload contract.
        snapshot_resp = client.get("/api/v1/snapshot", headers=headers)
        assert snapshot_resp.status_code == 200
        snapshot = snapshot_resp.json()
        assert snapshot["runtime"]["protocol"] == 7
        expected_preset_ids = {
            item["id"]
            for item in json.loads((ROOT / "catalog" / "presets.json").read_text(encoding="utf-8"))
        }
        assert {m["id"] for m in snapshot["models"]} == expected_preset_ids
        assert isinstance(snapshot["workflows"], list)
        assert isinstance(snapshot["comfy_outputs"], list)

        # Pydantic v2 validation + FastAPI custom 422 error shape contract.
        ok_cfg = client.post(
            "/api/v1/configure",
            json={"idle_minutes": 45, "idle_countdown_seconds": 60, "deadline": 0},
            headers=headers,
        )
        assert ok_cfg.status_code == 200
        assert ok_cfg.json() == {"ok": True}
        assert runtime.idle_minutes == 45

        bad_cfg = client.post(
            "/api/v1/configure",
            json={"idle_minutes": 999},
            headers=headers,
        )
        assert bad_cfg.status_code == 422
        assert bad_cfg.json() == {"detail": "请求参数格式不合法"}

        ok_act = client.post(
            "/api/v1/activity",
            json={"hold_seconds": 30},
            headers=headers,
        )
        assert ok_act.status_code == 200
        assert ok_act.json()["phase"] == "active"

        bad_act = client.post(
            "/api/v1/activity",
            json={"hold_seconds": 9999},
            headers=headers,
        )
        assert bad_act.status_code == 422
        assert bad_act.json() == {"detail": "请求参数格式不合法"}


def test_remote_runtime_upgraded_dependencies_smoke():
    # Confirm upgraded FastAPI, Pydantic, and Pillow operate cleanly in-memory.
    assert tuple(int(p) for p in fastapi.__version__.split(".")[:2]) >= (0, 165)
    assert tuple(int(p) for p in pydantic.__version__.split(".")[:2]) >= (2, 14)

    buf = io.BytesIO()
    Image.new("RGB", (4, 4), color=(16, 64, 128)).save(buf, format="PNG")
    buf.seek(0)
    with Image.open(buf) as decoded:
        assert decoded.format == "PNG"
        assert decoded.size == (4, 4)


def _normalize_dist_name(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name.strip()).lower()


def _parse_logical_requirement_lines(path: Path) -> list[str]:
    logical: list[str] = []
    buf: list[str] = []
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        stripped = raw_line.strip()
        if not buf and (not stripped or stripped.startswith("#")):
            continue
        rstripped = raw_line.rstrip()
        if rstripped.endswith("\\"):
            part = rstripped[:-1].strip()
            if part:
                buf.append(part)
            continue
        if stripped:
            buf.append(stripped)
        combined = re.sub(r"(?:^|\s)#.*$", "", " ".join(buf)).strip()
        buf.clear()
        if combined:
            logical.append(combined)
    if buf:
        combined = re.sub(r"(?:^|\s)#.*$", "", " ".join(buf)).strip()
        if combined:
            logical.append(combined)
    return logical


def _parse_exact_pins(path: Path) -> dict[str, str]:
    pins: dict[str, str] = {}
    for entry in _parse_logical_requirement_lines(path):
        pin_part = entry.split(";", 1)[0].strip()
        match = _REQ_PIN_RE.fullmatch(pin_part)
        assert match is not None, (
            f"Expected exact == pin in {path.relative_to(ROOT).as_posix()}, got: {entry!r}"
        )
        pkg = _normalize_dist_name(match.group(1))
        assert pkg not in pins, (
            f"Duplicate requirement {pkg!r} in {path.relative_to(ROOT).as_posix()}"
        )
        pins[pkg] = match.group(2)
    return pins


def test_server_requirements_pin_and_hash_hygiene():
    req_in_path = ROOT / "server" / "requirements.in"
    req_txt_path = ROOT / "server" / "requirements.txt"
    req_dev_path = ROOT / "requirements-dev.txt"

    direct_pins = _parse_exact_pins(req_in_path)
    assert direct_pins, "Expected direct pinned dependencies in server/requirements.in"

    locked_entries = _parse_logical_requirement_lines(req_txt_path)
    assert len(locked_entries) >= len(direct_pins), (
        "server/requirements.txt has fewer entries than server/requirements.in"
    )

    locked_pins: dict[str, set[str]] = {}
    for entry in locked_entries:
        assert "--hash=" in entry, (
            f"Pinned package in server/requirements.txt is missing --hash=sha256: {entry!r}"
        )
        spec_part, hash_rest = entry.split("--hash=", 1)
        pin_part = spec_part.split(";", 1)[0].strip()
        match = _REQ_PIN_RE.fullmatch(pin_part)
        assert match is not None, (
            f"Expected exact == pin in server/requirements.txt, got: {spec_part!r}"
        )
        hash_tokens = ("--hash=" + hash_rest).split()
        assert hash_tokens, f"Pinned package {pin_part} has no --hash=sha256 entries"
        for tok in hash_tokens:
            assert _REQ_SHA256_HASH_RE.fullmatch(tok), (
                f"Invalid or non-sha256 hash entry for {pin_part}: {tok!r}"
            )
        locked_pins.setdefault(_normalize_dist_name(match.group(1)), set()).add(
            match.group(2)
        )

    for pkg, expected_version in direct_pins.items():
        assert pkg in locked_pins, (
            f"Direct requirement {pkg}=={expected_version} from server/requirements.in "
            "is missing from server/requirements.txt"
        )
        assert expected_version in locked_pins[pkg], (
            f"Direct requirement {pkg}=={expected_version} from server/requirements.in "
            f"does not match server/requirements.txt ({sorted(locked_pins[pkg])})"
        )

    dev_pins = _parse_exact_pins(req_dev_path)
    for pkg, expected_version in direct_pins.items():
        assert dev_pins.get(pkg) == expected_version, (
            f"requirements-dev.txt pin for {pkg} ({dev_pins.get(pkg)!r}) "
            f"diverged from server/requirements.in ({expected_version!r})"
        )

    retired_bridge_deps = {"google-colab-cli", "google-auth-oauthlib", "keyring"}
    assert not (retired_bridge_deps & direct_pins.keys())
    assert not (retired_bridge_deps & locked_pins.keys())
    assert not (retired_bridge_deps & dev_pins.keys())


def test_tools_have_no_deleted_bridge_references():
    assert not (ROOT / "bridge").exists(), "Deleted local Python bridge/ directory must stay absent"

    assert build_fonts.SCAN_DIRS, "tools/build_fonts.py SCAN_DIRS must be non-empty"
    for rel_dir in build_fonts.SCAN_DIRS:
        parts = Path(rel_dir).parts
        assert parts and parts[0].lower() != "bridge", (
            f"Stale bridge/ reference in tools/build_fonts.py SCAN_DIRS: {rel_dir!r}"
        )
        assert (ROOT / rel_dir).is_dir(), (
            f"Missing directory listed in tools/build_fonts.py SCAN_DIRS: {rel_dir!r}"
        )

    for rel_script in ("tools/build_fonts.py", "tools/install.py"):
        script_path = ROOT / rel_script
        text = script_path.read_text(encoding="utf-8")
        offenders = [
            f"{rel_script}:{lineno}: {line.strip()}"
            for lineno, line in enumerate(text.splitlines(), start=1)
            if _BRIDGE_WORD_RE.search(line)
        ]
        assert not offenders, (
            f"Unexpected deleted bridge/ reference(s) in {rel_script}:\n"
            + "\n".join(offenders)
        )


def test_docs_contain_no_vpn_mentions():
    readmes = sorted(ROOT.glob("README*.md"))
    doc_files = sorted((ROOT / "docs").glob("*.md"))
    extra_docs = [ROOT / "AGENTS.md", ROOT / "CONTRIBUTING.md"]
    assert readmes, "Expected README*.md files at repository root"
    assert doc_files, "Expected docs/*.md files"
    for path in extra_docs:
        assert path.is_file(), f"Missing required documentation file: {path.name}"

    offenders: list[str] = []
    for path in [*readmes, *doc_files, *extra_docs]:
        for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
            if "vpn" in line.lower():
                offenders.append(
                    f"{path.relative_to(ROOT).as_posix()}:{lineno}: {line.strip()}"
                )
    assert not offenders, "Unexpected VPN mention(s) in documentation:\n" + "\n".join(
        offenders
    )


def test_project_forbids_right_arrow_symbols_and_icons():
    forbidden_re = re.compile(
        "["
        "\u2192\u219d\u21a0\u21a3\u21a6\u21aa\u21ac\u21b3"
        "\u21c0\u21c1\u21c9\u21d2\u21db\u21dd\u21e2\u21e5\u21e8"
        "\u21f6\u21f8\u21fb\u21fe"
        "\u2794\u2799-\u279e\u27a0-\u27a4\u27f6\u27f9\u27fc\u27fe\u27ff\u2b95"
        "]"
        "|&ra" "rr;|&#85" "94;|&#x21" "92;"
        "|\\bPhArrow" "Right\\b",
        re.IGNORECASE,
    )
    skip_dirs = {
        ".git",
        ".venv",
        "venv",
        "node_modules",
        "dist",
        "work",
        "__pycache__",
        ".pytest_cache",
        ".mypy_cache",
        ".ruff_cache",
        "test-results",
        "playwright-report",
        "third_party",
    }
    text_suffixes = {
        ".py",
        ".go",
        ".ts",
        ".tsx",
        ".js",
        ".mjs",
        ".cjs",
        ".vue",
        ".css",
        ".html",
        ".json",
        ".md",
        ".yml",
        ".yaml",
        ".toml",
        ".sh",
        ".ps1",
        ".txt",
        ".in",
    }
    offenders: list[str] = []
    for dirpath, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = sorted(d for d in dirnames if d not in skip_dirs)
        base = Path(dirpath)
        for filename in sorted(filenames):
            if filename == "package-lock.json":
                continue
            path = base / filename
            if path.suffix.lower() not in text_suffixes and path.name not in {"NOTICE", "LICENSE"}:
                continue
            rel = path.relative_to(ROOT)
            try:
                content = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            for lineno, line in enumerate(content.splitlines(), start=1):
                if forbidden_re.search(line):
                    offenders.append(f"{rel.as_posix()}:{lineno}: {line.strip()[:160]}")
    assert not offenders, (
        "Forbidden right-arrow symbol(s) or right-arrow icon(s) found:\n"
        + "\n".join(offenders)
    )


def test_live_acceptance_targets_loopback_api_v1_routes(tmp_path, monkeypatch):
    # Loopback HTTP base URLs are accepted; HTTPS or non-loopback hosts are rejected.
    assert live_acceptance._validate_base_url("http://127.0.0.1:7860/") == "http://127.0.0.1:7860"
    assert (
        live_acceptance._validate_base_url("http://localhost:7860/api/v1/")
        == "http://localhost:7860/api/v1"
    )
    assert live_acceptance._validate_base_url("http://[::1]:7860") == "http://[::1]:7860"

    for invalid_url in (
        "https://example.trycloudflare.com",
        "https://127.0.0.1:7860",
        "http://192.168.1.10:7860",
    ):
        with pytest.raises(SystemExit):
            live_acceptance._validate_base_url(invalid_url)

    # URL normalization always resolves cleanly to /api/v1/<route> without duplication.
    assert (
        live_acceptance._build_api_url("http://127.0.0.1:7860", "status")
        == "http://127.0.0.1:7860/api/v1/status"
    )
    assert (
        live_acceptance._build_api_url("http://127.0.0.1:7860/", "/models/qwen25-05b/load")
        == "http://127.0.0.1:7860/api/v1/models/qwen25-05b/load"
    )
    assert (
        live_acceptance._build_api_url("http://127.0.0.1:7860/api/v1", "status")
        == "http://127.0.0.1:7860/api/v1/status"
    )
    assert (
        live_acceptance._build_api_url("http://127.0.0.1:7860/api/v1/", "/api/v1/status")
        == "http://127.0.0.1:7860/api/v1/status"
    )
    assert (
        live_acceptance._build_api_url("http://127.0.0.1:7860", "/api/v1/prepare-release")
        == "http://127.0.0.1:7860/api/v1/prepare-release"
    )

    # Verify _NO_PROXY_OPENER is built with an empty ProxyHandler({}) (proxies == {})
    # and never installs a ProxyHandler even when ambient HTTP_PROXY/ALL_PROXY are set.
    monkeypatch.setenv("HTTP_PROXY", "http://192.0.2.1:9999")
    monkeypatch.setenv("http_proxy", "http://192.0.2.1:9999")
    monkeypatch.setenv("ALL_PROXY", "http://192.0.2.1:9999")
    monkeypatch.setenv("all_proxy", "http://192.0.2.1:9999")
    monkeypatch.delenv("NO_PROXY", raising=False)
    monkeypatch.delenv("no_proxy", raising=False)

    passed_handlers: list[object] = []
    real_build_opener = urllib.request.build_opener

    def _spy_build_opener(*handlers):
        passed_handlers.extend(handlers)
        return real_build_opener(*handlers)

    monkeypatch.setattr(live_acceptance.urllib.request, "build_opener", _spy_build_opener)
    ambient_opener = live_acceptance._build_no_proxy_opener()
    passed_proxy_handlers = [
        handler for handler in passed_handlers if isinstance(handler, urllib.request.ProxyHandler)
    ]
    assert len(passed_proxy_handlers) == 1
    assert passed_proxy_handlers[0].proxies == {}

    for opener in (live_acceptance._NO_PROXY_OPENER, ambient_opener):
        assert not any(
            isinstance(handler, urllib.request.ProxyHandler) for handler in opener.handlers
        )
        assert not any(
            isinstance(handler, urllib.request.ProxyHandler)
            for handler in opener.handle_open.get("http", [])
        )

    # Verify _remote_request uses _NO_PROXY_OPENER (never urllib.request.urlopen)
    # and sends the normalized /api/v1/ URL and Bearer auth offline without proxy routing.
    captured: dict[str, object] = {}

    class _FakeResp:
        code = 200
        msg = "OK"

        def info(self):
            return {}

        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

        def read(self) -> bytes:
            return b'{"ok": true}'

    def _fail_urlopen(*args, **kwargs):
        raise AssertionError("live_acceptance._remote_request must not call urllib.request.urlopen")

    def _fake_open(req, timeout=60):
        captured["url"] = req.full_url
        captured["method"] = req.get_method()
        captured["auth"] = req.get_header("Authorization")
        captured["content_type"] = req.get_header("Content-type")
        captured["body"] = json.loads(req.data.decode("utf-8"))
        captured["timeout"] = timeout
        return _FakeResp()

    monkeypatch.setattr(live_acceptance.urllib.request, "urlopen", _fail_urlopen)
    monkeypatch.setattr(live_acceptance._NO_PROXY_OPENER, "open", _fake_open)
    resp = live_acceptance._remote_request(
        "http://127.0.0.1:7860",
        "tok-acceptance-test",
        "POST",
        "models",
        {"id": "qwen25-05b"},
    )
    assert resp == {"ok": True}
    assert captured == {
        "url": "http://127.0.0.1:7860/api/v1/models",
        "method": "POST",
        "auth": "Bearer tok-acceptance-test",
        "content_type": "application/json",
        "body": {"id": "qwen25-05b"},
        "timeout": 60,
    }

    # Also verify an opener built under ambient HTTP_PROXY/ALL_PROXY dispatches directly
    # to HTTPHandler for 127.0.0.1 without rewriting req.host to the proxy address.
    direct_req: dict[str, object] = {}
    http_handler = next(
        handler
        for handler in ambient_opener.handle_open["http"]
        if isinstance(handler, urllib.request.HTTPHandler)
    )

    def _fake_http_open(req):
        direct_req["host"] = req.host
        direct_req["full_url"] = req.full_url
        direct_req["auth"] = req.get_header("Authorization")
        return _FakeResp()

    monkeypatch.setattr(http_handler, "http_open", _fake_http_open)
    monkeypatch.setattr(live_acceptance, "_NO_PROXY_OPENER", ambient_opener)
    direct_resp = live_acceptance._remote_request(
        "http://127.0.0.1:7860",
        "tok-loopback-direct",
        "GET",
        "status",
    )
    assert direct_resp == {"ok": True}
    assert direct_req == {
        "host": "127.0.0.1:7860",
        "full_url": "http://127.0.0.1:7860/api/v1/status",
        "auth": "Bearer tok-loopback-direct",
    }

    # Verify every route invoked by live_acceptance matches a real /api/v1/ route on server/app.py.
    runtime = ColabOneRuntime(tmp_path / "remote", ROOT / "catalog")
    app = create_colabone_app(
        runtime, "colabone-contract-test-token-0123456789abcdef", background=False
    )
    acceptance_routes = (
        ("GET", "status"),
        ("POST", "models"),
        ("POST", "models/qwen25-05b/download"),
        ("POST", "models/qwen25-05b/load"),
        ("POST", "unload"),
        ("POST", "prepare-release"),
    )
    for method, rel_path in acceptance_routes:
        url = live_acceptance._build_api_url("http://127.0.0.1:7860", rel_path)
        route_path = urllib.parse.urlparse(url).path
        assert route_path.startswith("/api/v1/"), f"Expected /api/v1/ route, got {route_path}"
        scope = {"type": "http", "path": route_path, "method": method}
        matched = any(route.matches(scope)[0] == Match.FULL for route in app.router.routes)
        assert matched, f"live_acceptance route {method} {route_path} not registered on server/app.py"


def test_live_studio_go_python_payload_and_route_contracts(tmp_path, monkeypatch):
    """Validate canonical Go Live Studio payloads against Pydantic models and FastAPI routes offline."""
    sample_b64 = base64.b64encode(b"mock-live-studio-media").decode("ascii")
    valid_tunnel_token = base64.b64encode(
        b'{"a":"acct123","t":"tun456","s":"secret789"}'
    ).decode("ascii")

    # 1. Canonical payloads matching Go's strict validators (console/internal/live/live.go).
    vram_payload = {
        "visual_engine": "flashhead_lite",
        "personalive_enabled": True,
        "tts_engine": "qwen3_tts",
        "voice_preset": "shen_zhiyi",
        "llm_mode": "local_gguf",
        "local_llm_model_id": "qwen3-8b",
        "director_mode": "dialogue",
        "mic_mode": "vad",
        "avatar_id": "anchor_1",
        "target_fps": 25,
        "resolution": "720x1280",
        "enable_rtmp": False,
        "rtmp_url": "",
    }
    start_payload = {
        **vram_payload,
        "llm_mode": "external_api",
        "enable_rtmp": True,
        "rtmp_url": "rtmps://a.rtmp.youtube.com/live2/stream-key",
        "external_llm": {
            "base_url": "https://api.openai.com/v1",
            "model": "gpt-4o-mini",
            "api_key": "sk-contract-test-1234567890",
            "temperature": 0.6,
            "max_tokens": 512,
        },
        "hf_token": "hf_contract_test_token_123456",
        "portrait_input_id": "0123456789abcdef0123456789abcdef",
        "canvas_config": {
            "mode": "named",
            "domain": "https://comfy.example.com",
            "tunnel_token": valid_tunnel_token,
        },
    }
    llm_test_payload = {
        "base_url": "https://api.openai.com/v1",
        "model": "gpt-4o-mini",
        "api_key": "sk-contract-test-1234567890",
        "timeout_seconds": 15.0,
    }
    avatar_save_with_tag_payload = {
        "id": "anchor_1",
        "name": "数字主播小晴",
        "persona_prompt": "热情开朗的直播间主播",
        "portrait_input_id": "0123456789abcdef0123456789abcdef",
        "portrait_b64": sample_b64,
        "portrait_filename": "cover.png",
        "default_tts_engine": "qwen3_tts",
        "default_voice_preset": "shen_zhiyi",
        "actions": [
            {
                "id": "idle_loop",
                "label": "待机循环",
                "kind": "loop",
                "clip_path": "clips/idle.mp4",
                "bgm_path": "",
                "duration_seconds": 4.0,
                "trigger_keywords": ["待机"],
            }
        ],
        "products": [
            {
                "id": "prod_1",
                "name": "轻薄防晒霜",
                "price": "¥99",
                "tag": "爆款推荐",
                "highlights": "清爽不油腻",
                "script_hint": "夏日必囤",
                "image_input_id": "0123456789abcdef0123456789abcdef",
            }
        ],
    }
    avatar_save_empty_products_payload = {
        "id": "anchor_1",
        "name": "数字主播小晴",
        "actions": [],
        "products": [],
    }
    clip_upload_payload = {
        "action_id": "dance_01",
        "label": "热舞片段",
        "kind": "dance",
        "filename": "dance.mp4",
        "data": sample_b64,
        "bgm_filename": "track.m4a",
        "bgm_data": sample_b64,
        "duration_seconds": 12.5,
        "trigger_keywords": ["跳舞", "才艺"],
    }
    build_pack_payload = {
        "avatar_id": "anchor_1",
        "name": "数字主播小晴",
        "portrait_input_id": "0123456789abcdef0123456789abcdef",
        "portrait_b64": sample_b64,
        "portrait_filename": "cover.png",
        "actions": ["idle_loop", "talk_explain"],
        "dance_ids": ["dance_01"],
        "resolution": "720x1280",
        "fps": 25,
        "hf_token": "hf_contract_test_token_123456",
        "overwrite": True,
    }
    driving_upload_payload = {
        "id": "drive_01",
        "label": "点头驱动",
        "filename": "nod.mkv",
        "data": sample_b64,
        "bgm_filename": "bgm.ogg",
        "bgm_data": sample_b64,
        "duration_seconds": 8.0,
        "trigger_keywords": ["点头"],
    }

    # 2. Validate every canonical payload against common/live_studio.py strict Pydantic models.
    ColabOneLiveSessionConfig.model_validate(vram_payload)
    ColabOneLiveStartRequest.model_validate(start_payload)
    ColabOneLiveExternalLLMTestRequest.model_validate(llm_test_payload)
    parsed_avatar = ColabOneLiveAvatarSaveRequest.model_validate(avatar_save_with_tag_payload)
    assert parsed_avatar.products is not None and parsed_avatar.products[0].tag == "爆款推荐"
    parsed_empty = ColabOneLiveAvatarSaveRequest.model_validate(avatar_save_empty_products_payload)
    assert parsed_empty.products == []
    assert parsed_empty.actions == []
    ColabOneLiveClipUploadRequest.model_validate(clip_upload_payload)
    ColabOneLiveBuildPackRequest.model_validate(build_pack_payload)
    ColabOneLiveDrivingUploadRequest.model_validate(driving_upload_payload)

    # Confirm extra="forbid" rejects drift / legacy aliases that Go must never forward.
    with pytest.raises(pydantic.ValidationError):
        ColabOneLiveSessionConfig.model_validate({**vram_payload, "hf_token": "hf_bad"})
    with pytest.raises(pydantic.ValidationError):
        ColabOneLiveStartRequest.model_validate({**start_payload, "fps": 25})
    with pytest.raises(pydantic.ValidationError):
        ColabOneLiveAvatarSaveRequest.model_validate(
            {**avatar_save_with_tag_payload, "avatar_id": "anchor_1"}
        )
    with pytest.raises(pydantic.ValidationError):
        ColabOneLiveClipUploadRequest.model_validate(
            {**clip_upload_payload, "avatar_id": "anchor_1"}
        )
    with pytest.raises(pydantic.ValidationError):
        ColabOneLiveBuildPackRequest.model_validate(
            {**build_pack_payload, "clip_ids": ["idle_loop"]}
        )
    with pytest.raises(pydantic.ValidationError):
        ColabOneLiveDrivingUploadRequest.model_validate(
            {**driving_upload_payload, "action_id": "drive_01"}
        )

    # 3. Verify every Live Studio route exists on server/app.py and accepts the canonical payloads (HTTP 200, never 422).
    runtime = ColabOneRuntime(tmp_path / "remote", ROOT / "catalog")
    monkeypatch.setattr(
        runtime,
        "gpu_info",
        lambda: {"name": "NVIDIA L4", "total_mib": 24576, "used_mib": 512, "utilization": 0},
    )
    monkeypatch.setattr(runtime, "live_check_vram", lambda cfg: {"ok": True, "config": cfg})
    monkeypatch.setattr(runtime, "start_live_studio", lambda cfg: {"ok": True, "state": "starting"})
    monkeypatch.setattr(runtime, "stop_live_studio", lambda: {"ok": True, "state": "stopped"})
    monkeypatch.setattr(
        runtime,
        "live_session_token",
        lambda: {"token": "ephemeral", "ws_url": "wss://comfy.example.com/_colabone_live/ws"},
    )
    monkeypatch.setattr(runtime, "live_test_external_llm", lambda cfg: {"ok": True, "latency_ms": 42})
    monkeypatch.setattr(runtime, "save_live_avatar", lambda cfg: {"id": cfg.get("id") or "anchor_1"})
    monkeypatch.setattr(runtime, "delete_live_avatar", lambda avatar_id: None)
    monkeypatch.setattr(
        runtime,
        "upload_live_avatar_clip",
        lambda avatar_id, cfg: {"ok": True, "avatar_id": avatar_id, "action_id": cfg["action_id"]},
    )
    monkeypatch.setattr(
        runtime,
        "build_live_avatar_pack",
        lambda cfg: {"ok": True, "avatar_id": cfg["avatar_id"]},
    )
    monkeypatch.setattr(
        runtime,
        "upload_live_driving_motion",
        lambda cfg: {"ok": True, "id": cfg.get("id") or "drive_01"},
    )
    monkeypatch.setattr(runtime, "delete_live_driving_motion", lambda motion_id: None)

    token = "colabone-live-contract-token-0123456789abcdef"
    headers = {"Authorization": f"Bearer {token}"}
    app = create_colabone_app(runtime, token, background=False)

    expected_live_routes = (
        ("GET", "/api/v1/live/status"),
        ("GET", "/api/v1/live/catalog"),
        ("POST", "/api/v1/live/vram-check"),
        ("POST", "/api/v1/live/start"),
        ("POST", "/api/v1/live/stop"),
        ("POST", "/api/v1/live/session-token"),
        ("POST", "/api/v1/live/llm-test"),
        ("GET", "/api/v1/live/avatars"),
        ("POST", "/api/v1/live/avatars"),
        ("DELETE", "/api/v1/live/avatars/anchor_1"),
        ("POST", "/api/v1/live/avatars/anchor_1/clips"),
        ("POST", "/api/v1/live/avatars/build-pack"),
        ("GET", "/api/v1/live/driving-motions"),
        ("POST", "/api/v1/live/driving-motions"),
        ("DELETE", "/api/v1/live/driving-motions/drive_01"),
    )
    for method, route_path in expected_live_routes:
        scope = {"type": "http", "path": route_path, "method": method}
        matched = any(route.matches(scope)[0] == Match.FULL for route in app.router.routes)
        assert matched, f"Live Studio route {method} {route_path} not registered on server/app.py"

    with TestClient(app) as client:
        snap = client.get("/api/v1/snapshot", headers=headers)
        assert snap.status_code == 200
        snap_body = snap.json()
        assert isinstance(snap_body["live_avatars"], list)
        assert isinstance(snap_body["live_driving_motions"], list)
        assert isinstance(snap_body["live_catalog"], dict)

        for path, body in (
            ("/api/v1/live/vram-check", vram_payload),
            ("/api/v1/live/start", start_payload),
            ("/api/v1/live/llm-test", llm_test_payload),
            ("/api/v1/live/avatars", avatar_save_with_tag_payload),
            ("/api/v1/live/avatars", avatar_save_empty_products_payload),
            ("/api/v1/live/avatars/anchor_1/clips", clip_upload_payload),
            ("/api/v1/live/avatars/build-pack", build_pack_payload),
            ("/api/v1/live/driving-motions", driving_upload_payload),
        ):
            resp = client.post(path, json=body, headers=headers)
            assert resp.status_code == 200, (
                f"Canonical Go payload rejected by {path}: {resp.status_code} {resp.text}"
            )

        assert client.get("/api/v1/live/driving-motions", headers=headers).status_code == 200
        assert client.delete("/api/v1/live/driving-motions/drive_01", headers=headers).status_code == 200


def _extract_go_switch_case_strings(source: str, func_prefix: str) -> set[str]:
    start = source.index(func_prefix)
    brace_start = source.index("{", start)
    depth = 0
    end = brace_start
    for idx in range(brace_start, len(source)):
        ch = source[idx]
        if ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                end = idx
                break
    body = source[brace_start : end + 1]
    cmds: set[str] = set()
    for match in re.finditer(r"^\s*case\s+([^:]+):", body, re.MULTILINE):
        for lit in re.findall(r'"([a-z0-9._-]+)"', match.group(1)):
            cmds.add(lit)
    return cmds


def _extract_go_map_keys(source: str, var_prefix: str) -> set[str]:
    start = source.index(var_prefix)
    brace_start = source.index("{", start)
    brace_end = source.index("}", brace_start)
    body = source[brace_start : brace_end + 1]
    return set(re.findall(r'^\s*"([a-z0-9._-]+)"\s*:', body, re.MULTILINE))


def test_go_controller_command_parity_across_timeout_webconsole_and_frontend():
    commands_go = (ROOT / "console" / "internal" / "controller" / "commands.go").read_text(
        encoding="utf-8"
    )
    backend_go = (ROOT / "console" / "backend.go").read_text(encoding="utf-8")
    webconsole_go = (
        ROOT / "console" / "internal" / "webconsole" / "webconsole.go"
    ).read_text(encoding="utf-8")

    controller_cmds = _extract_go_switch_case_strings(
        commands_go, "func (c *ColabOneController) dispatchCommand("
    )
    timeout_cmds = _extract_go_switch_case_strings(
        backend_go, "func colabOneMethodTimeout("
    )
    blocked_web_cmds = _extract_go_map_keys(
        webconsole_go, "blockedWebCommands = map[string]string{"
    )

    frontend_cmds: set[str] = set()
    frontend_src = ROOT / "frontend" / "src"
    for path in frontend_src.rglob("*"):
        if path.suffix not in (".ts", ".vue") or path.name.endswith(".test.ts"):
            continue
        text = path.read_text(encoding="utf-8")
        for match in re.finditer(
            r"""colabOneCommand(?:<[^>]*>)?\(\s*['"]([a-z0-9._-]+)['"]""",
            text,
        ):
            frontend_cmds.add(match.group(1))

    assert controller_cmds, "Expected controller commands in commands.go"
    assert timeout_cmds, "Expected timeout entries in colabOneMethodTimeout"
    assert blocked_web_cmds, "Expected entries in blockedWebCommands"
    assert frontend_cmds, "Expected static colabOneCommand calls in frontend/src"

    assert sorted(timeout_cmds - controller_cmds) == [], (
        f"colabOneMethodTimeout references unknown commands: {sorted(timeout_cmds - controller_cmds)}"
    )
    assert sorted(blocked_web_cmds - controller_cmds) == [], (
        f"blockedWebCommands references unknown commands: {sorted(blocked_web_cmds - controller_cmds)}"
    )
    assert sorted(frontend_cmds - controller_cmds) == [], (
        f"frontend/src calls unknown commands: {sorted(frontend_cmds - controller_cmds)}"
    )

    assert "settings.assign_proxy.test" in blocked_web_cmds, (
        "settings.assign_proxy.test must be blocked in WebConsole"
    )

    phantom_shortcuts = {"shortcuts.status", "shortcuts.create", "shortcuts.remove"}
    assert phantom_shortcuts.isdisjoint(timeout_cmds), (
        f"Phantom shortcut commands still present in colabOneMethodTimeout: {phantom_shortcuts & timeout_cmds}"
    )
    assert phantom_shortcuts.isdisjoint(blocked_web_cmds), (
        f"Phantom shortcut commands still present in blockedWebCommands: {phantom_shortcuts & blocked_web_cmds}"
    )
    assert phantom_shortcuts.isdisjoint(controller_cmds), (
        f"Phantom shortcut commands still present in commands.go: {phantom_shortcuts & controller_cmds}"
    )


def test_vm_boot_and_vm_update_require_hashes_for_pip_install():
    for rel_path in ("server/vm_boot.py", "server/vm_update.py"):
        source = (ROOT / rel_path).read_text(encoding="utf-8")
        tree = ast.parse(source, filename=rel_path)
        pip_install_lists: list[list[str]] = []
        for node in ast.walk(tree):
            if isinstance(node, (ast.List, ast.Tuple)):
                str_items = [
                    elt.value
                    for elt in node.elts
                    if isinstance(elt, ast.Constant) and isinstance(elt.value, str)
                ]
                if "pip" in str_items and "install" in str_items and "-r" in str_items:
                    pip_install_lists.append(str_items)

        assert pip_install_lists, (
            f"Expected pip install -r invocation in {rel_path}"
        )
        for cmd_items in pip_install_lists:
            assert "--require-hashes" in cmd_items, (
                f"{rel_path} pip install invocation must pass --require-hashes: {cmd_items}"
            )


def test_service_bundle_directories_remain_flat():
    for folder in COLABONE_SERVICE_BUNDLE_FOLDERS:
        folder_dir = ROOT / folder
        assert folder_dir.is_dir(), f"Missing bundle directory: {folder}"
        nested_dirs = sorted(
            p.relative_to(ROOT).as_posix()
            for p in folder_dir.rglob("*")
            if p.is_dir() and "__pycache__" not in p.parts
        )
        assert nested_dirs == [], (
            f"{folder}/ must remain flat so tools/sync_service_bundle.py never misses nested files; "
            f"found subdirectories: {nested_dirs}"
        )
        nested_bundle_files = sorted(
            p.relative_to(ROOT).as_posix()
            for p in folder_dir.rglob("*")
            if p.is_file()
            and "__pycache__" not in p.parts
            and p.suffix in COLABONE_SERVICE_BUNDLE_SUFFIXES
            and p.parent != folder_dir
        )
        assert nested_bundle_files == [], (
            f"Found un-synced nested bundle files under {folder}/: {nested_bundle_files}"
        )


def test_webconsole_command_body_size_limits_and_upload_allowlist():
    webconsole_go = (
        ROOT / "console" / "internal" / "webconsole" / "webconsole.go"
    ).read_text(encoding="utf-8")

    def _parse_go_mib_const(name: str) -> int:
        shift_match = re.search(rf"\b{name}\s*=\s*(\d+)\s*<<\s*20\b", webconsole_go)
        if shift_match:
            return int(shift_match.group(1)) * (1 << 20)
        mul_match = re.search(
            rf"\b{name}\s*=\s*(\d+)\s*\*\s*1024\s*\*\s*1024\b", webconsole_go
        )
        if mul_match:
            return int(mul_match.group(1)) * 1024 * 1024
        lit_match = re.search(rf"\b{name}\s*=\s*(\d+)\b", webconsole_go)
        assert lit_match, f"Could not find {name} constant in webconsole.go"
        return int(lit_match.group(1))

    normal_cap = _parse_go_mib_const("maxCommandBodyBytes")
    media_cap = _parse_go_mib_const("maxMediaUploadCommandBodyBytes")

    assert normal_cap == 64 * 1024 * 1024, (
        f"Normal WebConsole commands must remain capped at 64 MiB, got {normal_cap}"
    )
    assert media_cap >= 228 * 1024 * 1024, (
        f"Media upload WebConsole commands must support >= 228 MiB payloads, got {media_cap}"
    )

    allowed_uploads = _extract_go_map_keys(
        webconsole_go, "mediaUploadWebCommands = map[string]bool{"
    )
    expected_uploads = {
        "comfyui.input.upload",
        "comfyui.draft.media.save",
        "comfyui.live.avatars.clip.upload",
        "comfyui.live.driving.upload",
    }
    assert allowed_uploads == expected_uploads, (
        f"mediaUploadWebCommands mismatch: expected {sorted(expected_uploads)}, got {sorted(allowed_uploads)}"
    )
    assert "if cr.n > normalLimit && !mediaUploadWebCommands[action]" in webconsole_go, (
        "handleCommand must enforce normalLimit for non-media-upload commands"
    )


def test_web_and_go_brand_icons_stay_in_sync_with_master_software_logo():
    master_svg = (ROOT / "console" / "resources" / "icon.svg").read_bytes()
    brandicon_dir = ROOT / "console" / "internal" / "brandicon"

    assert (brandicon_dir / "icon.svg").read_bytes() == master_svg, (
        "console/internal/brandicon/icon.svg drifted from console/resources/icon.svg; "
        "run `npm --prefix frontend run icons`"
    )

    expected_trimmed = master_svg.replace(
        b'viewBox="0 0 1254 1254"',
        b'viewBox="120 120 1014 1014"',
        1,
    )
    assert expected_trimmed != master_svg
    assert (brandicon_dir / "favicon.svg").read_bytes() == expected_trimmed, (
        "console/internal/brandicon/favicon.svg drifted from trimmed console/resources/icon.svg; "
        "run `npm --prefix frontend run icons`"
    )

    assert not (ROOT / "console" / "internal" / "oauthpage" / "icon.svg").exists(), (
        "oauthpage must share console/internal/brandicon instead of duplicating icon.svg"
    )

    login_page_go = (
        ROOT / "console" / "internal" / "webconsole" / "login_page.go"
    ).read_text(encoding="utf-8")
    assert "console/internal/brandicon" in login_page_go
    assert "brandicon.DataURI()" in login_page_go
    assert 'viewBox="0 0 64 64"' not in login_page_go

    index_html = (ROOT / "frontend" / "index.html").read_text(encoding="utf-8")
    assert "../console/internal/brandicon/favicon.svg" in index_html
    assert "../console/internal/brandicon/favicon.ico" in index_html
    assert "../console/internal/brandicon/apple-touch-icon.png" in index_html
