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
