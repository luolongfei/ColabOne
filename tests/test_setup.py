"""Offline contract tests for remote localization and shared location resolution."""

from __future__ import annotations

import re
import socket
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock


def test_comfy_proxy_401_html_respects_accept_language():
    from server.comfy_proxy import (
        ColabOneCanvasProxyServer,
        _UNAUTHORIZED_HTML_TEXT,
        _resolve_accept_language,
    )

    assert _resolve_accept_language("") == "en-US"
    assert _resolve_accept_language("fr-FR,fr;q=0.9") == "en-US"
    assert _resolve_accept_language("en-US,en;q=0.9,zh-CN;q=0.8") == "en-US"
    assert _resolve_accept_language("zh-TW,zh;q=0.9,en;q=0.8") == "zh-TW"
    assert _resolve_accept_language("zh-HK,zh-Hant;q=0.9") == "zh-HK"
    assert _resolve_accept_language("zh-CN,zh;q=0.9") == "zh-CN"
    assert _resolve_accept_language("ja-JP,ja;q=0.9") == "ja-JP"
    assert _resolve_accept_language("ko-KR,ko;q=0.9") == "ko-KR"
    assert set(_UNAUTHORIZED_HTML_TEXT.keys()) == {
        "zh-CN",
        "en-US",
        "zh-TW",
        "zh-HK",
        "ja-JP",
        "ko-KR",
    }

    # Verify live proxy 401 HTML output follows Accept-Language
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]

    proxy = ColabOneCanvasProxyServer(
        listen_port=port,
        upstream_port=1,
        canvas_token="tok-test",
    )
    proxy.start()
    try:
        for header, expected_lang, expected_text in [
            ("en-US,en;q=0.9", "en-US", "Unauthorized: please open ComfyUI Native Canvas"),
            ("ja-JP,ja;q=0.9", "ja-JP", "ColabOne アプリから"),
            ("zh-TW,zh;q=0.9", "zh-TW", "ColabOne 用戶端"),
            ("", "en-US", "Unauthorized: please open ComfyUI Native Canvas"),
        ]:
            with socket.create_connection(("127.0.0.1", port), timeout=5) as conn:
                req = (
                    "GET / HTTP/1.1\r\n"
                    f"Host: 127.0.0.1:{port}\r\n"
                    "Accept: text/html\r\n"
                    + (f"Accept-Language: {header}\r\n" if header else "")
                    + "Connection: close\r\n\r\n"
                )
                conn.sendall(req.encode("utf-8"))
                chunks = []
                while True:
                    buf = conn.recv(4096)
                    if not buf:
                        break
                    chunks.append(buf)
            resp = b"".join(chunks).decode("utf-8", errors="replace")
            assert "401 Unauthorized" in resp
            assert f"lang='{expected_lang}'" in resp
            assert expected_text in resp
    finally:
        proxy.stop()


def test_frontend_locations_cover_all_python_countries_and_subregions():
    from common.location import COUNTRY_NAMES_ZH, GCP_REGIONS, REGION_NAMES_ZH

    root = Path(__file__).resolve().parents[1]
    loc_ts = (root / "frontend" / "src" / "i18n" / "locations.ts").read_text(
        encoding="utf-8"
    )

    all_countries = set(COUNTRY_NAMES_ZH.keys()) | {
        code for code, _, _ in GCP_REGIONS.values()
    }
    for locale in ("zh-CN", "en-US", "zh-TW", "zh-HK", "ja-JP", "ko-KR"):
        m = re.search(rf'"{locale}":\s*\{{([^}}]+)\}}', loc_ts)
        assert m, f"Missing COUNTRY_OVERRIDES block for {locale}"
        present = set(re.findall(r"\b([A-Z]{2}):", m.group(1)))
        missing = sorted(all_countries - present)
        assert not missing, f"Missing country translations in {locale}: {missing}"

    all_subregions = {
        sub for _, _, sub in GCP_REGIONS.values() if sub
    } | set(REGION_NAMES_ZH.values())
    sub_block = loc_ts.split("SUBREGION_TRANSLATIONS")[1].split(
        "const ZH_COUNTRY_TO_CODE"
    )[0]
    missing_subs = [
        sub
        for sub in sorted(all_subregions)
        if not re.search(
            rf'(?:^|\s)"?{re.escape(sub)}"?\s*:', sub_block, re.MULTILINE
        )
    ]
    assert not missing_subs, (
        f"Missing subregion translations in locations.ts: {missing_subs}"
    )


def test_location_resolution_from_endpoint_metadata_ipinfo_and_trace(runtime):
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
