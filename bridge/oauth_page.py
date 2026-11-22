"""Self-contained, localized OAuth completion page served by ColabOne's loopback callback."""

from __future__ import annotations

import base64
import html
from functools import lru_cache
from pathlib import Path

_FALLBACK_ICON_SVG = (
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">'
    '<rect width="64" height="64" rx="16" fill="#111827"/>'
    '<circle cx="32" cy="32" r="14" fill="none" stroke="#ffffff" stroke-width="5"/>'
    '<circle cx="32" cy="32" r="4.5" fill="#3b82f6"/>'
    "</svg>"
)

_OAUTH_PAGE_COPY: dict[str, dict[str, str]] = {
    "zh-CN": {
        "page_title_ok": "授权成功 · ColabOne",
        "page_title_err": "授权未完成 · ColabOne",
        "heading_ok": "已授权登录 ColabOne",
        "heading_err": "登录未完成",
        "badge_ok": "安全凭据已保存至本机",
        "badge_err": "未保存任何凭据",
        "close_button": "关闭此页面",
        "return_button": "关闭并返回重试",
        "shortcut_prefix": "如页面未自动关闭，按 ",
        "shortcut_suffix": " 即可关闭此标签页",
        "footer": "ColabOne · 远端 GPU 工作空间",
    },
    "en-US": {
        "page_title_ok": "Signed In · ColabOne",
        "page_title_err": "Sign-in Incomplete · ColabOne",
        "heading_ok": "Signed in to ColabOne",
        "heading_err": "Sign-in was not completed",
        "badge_ok": "Credentials saved securely on this device",
        "badge_err": "No credentials were saved",
        "close_button": "Close this tab",
        "return_button": "Close and try again",
        "shortcut_prefix": "If the tab stays open, press ",
        "shortcut_suffix": " to close it",
        "footer": "ColabOne · Remote GPU Workspace",
    },
    "zh-TW": {
        "page_title_ok": "授權成功 · ColabOne",
        "page_title_err": "授權未完成 · ColabOne",
        "heading_ok": "已授權登入 ColabOne",
        "heading_err": "登入尚未完成",
        "badge_ok": "安全憑證已儲存至本機",
        "badge_err": "未儲存任何憑證",
        "close_button": "關閉此分頁",
        "return_button": "關閉並返回重試",
        "shortcut_prefix": "若分頁未自動關閉，按 ",
        "shortcut_suffix": " 即可關閉",
        "footer": "ColabOne · 遠端 GPU 工作區",
    },
    "zh-HK": {
        "page_title_ok": "授權成功 · ColabOne",
        "page_title_err": "授權未完成 · ColabOne",
        "heading_ok": "已授權登入 ColabOne",
        "heading_err": "登入尚未完成",
        "badge_ok": "安全憑證已儲存至本機",
        "badge_err": "未儲存任何憑證",
        "close_button": "關閉此分頁",
        "return_button": "關閉並返回再試",
        "shortcut_prefix": "如分頁未自動關閉，按 ",
        "shortcut_suffix": " 即可關閉",
        "footer": "ColabOne · 遠端 GPU 工作區",
    },
    "ja-JP": {
        "page_title_ok": "認証完了 · ColabOne",
        "page_title_err": "認証未完了 · ColabOne",
        "heading_ok": "ColabOne へのログイン完了",
        "heading_err": "ログインが完了しませんでした",
        "badge_ok": "認証情報はこの端末に安全に保存されました",
        "badge_err": "認証情報は保存されていません",
        "close_button": "このタブを閉じる",
        "return_button": "閉じてアプリで再試行",
        "shortcut_prefix": "自動で閉じない場合は ",
        "shortcut_suffix": " で閉じられます",
        "footer": "ColabOne · リモート GPU ワークスペース",
    },
    "ko-KR": {
        "page_title_ok": "인증 완료 · ColabOne",
        "page_title_err": "로그인 미완료 · ColabOne",
        "heading_ok": "ColabOne 로그인 완료",
        "heading_err": "로그인이 완료되지 않았습니다",
        "badge_ok": "인증 정보가 이 기기에 안전하게 저장되었습니다",
        "badge_err": "저장된 인증 정보가 없습니다",
        "close_button": "이 탭 닫기",
        "return_button": "닫고 다시 시도",
        "shortcut_prefix": "탭이 자동으로 닫히지 않으면 ",
        "shortcut_suffix": " 키로 닫을 수 있습니다",
        "footer": "ColabOne · 원격 GPU 워크스페이스",
    },
}


@lru_cache(maxsize=1)
def _colabone_icon_data_uri() -> str:
    """Return a self-contained data URI for ColabOne's brand icon."""
    candidates = (
        Path(__file__).resolve().parent / "assets" / "icon.svg",
        Path(__file__).resolve().parents[1] / "console" / "resources" / "icon.svg",
    )
    raw_svg = _FALLBACK_ICON_SVG
    for path in candidates:
        try:
            if path.is_file():
                text = path.read_text(encoding="utf-8")
                if "<svg" in text:
                    raw_svg = text
                    break
        except OSError:
            continue
    encoded = base64.b64encode(raw_svg.encode("utf-8")).decode("ascii")
    return f"data:image/svg+xml;base64,{encoded}"


def render_colabone_oauth_page(
    *,
    locale: str,
    ok: bool,
    message: str,
    email: str = "",
) -> str:
    """Render a clean, modern, light/dark-adaptive OAuth completion HTML page."""
    lang = locale if locale in _OAUTH_PAGE_COPY else "zh-CN"
    copy = _OAUTH_PAGE_COPY[lang]
    icon_uri = _colabone_icon_data_uri()

    page_title = html.escape(
        copy["page_title_ok"] if ok else copy["page_title_err"], quote=True
    )
    heading = html.escape(copy["heading_ok"] if ok else copy["heading_err"], quote=True)
    body_text = html.escape(str(message or ""), quote=True)
    badge_text = html.escape(
        (str(email).strip() if ok and str(email or "").strip() else (copy["badge_ok"] if ok else copy["badge_err"])),
        quote=True,
    )
    button_label = html.escape(
        copy["close_button"] if ok else copy["return_button"], quote=True
    )
    shortcut_prefix = html.escape(copy["shortcut_prefix"], quote=True)
    shortcut_suffix = html.escape(copy["shortcut_suffix"], quote=True)
    footer_text = html.escape(copy["footer"], quote=True)
    state_class = "is-success" if ok else "is-error"

    if ok:
        status_svg = (
            '<svg class="co-status-svg" viewBox="0 0 24 24" fill="none" aria-hidden="true">'
            '<path class="co-check-path" d="M5.5 12.6L9.9 17L18.5 7.5" '
            'stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>'
            "</svg>"
        )
        pill_icon = (
            '<svg class="co-pill-icon" viewBox="0 0 16 16" fill="none" aria-hidden="true">'
            '<path d="M8 1.75L13 3.75V7.65C13 10.8 10.9 13.45 8 14.25C5.1 13.45 3 10.8 3 7.65V3.75L8 1.75Z" '
            'stroke="currentColor" stroke-width="1.35" stroke-linejoin="round"/>'
            '<path d="M5.8 8.15L7.3 9.65L10.35 6.5" '
            'stroke="currentColor" stroke-width="1.35" stroke-linecap="round" stroke-linejoin="round"/>'
            "</svg>"
        )
    else:
        status_svg = (
            '<svg class="co-status-svg" viewBox="0 0 24 24" fill="none" aria-hidden="true">'
            '<path d="M8 8L16 16M16 8L8 16" '
            'stroke="currentColor" stroke-width="2.3" stroke-linecap="round"/>'
            "</svg>"
        )
        pill_icon = (
            '<svg class="co-pill-icon" viewBox="0 0 16 16" fill="none" aria-hidden="true">'
            '<circle cx="8" cy="8" r="5.75" stroke="currentColor" stroke-width="1.35"/>'
            '<path d="M8 5.2V8.4M8 10.85H8.01" '
            'stroke="currentColor" stroke-width="1.45" stroke-linecap="round"/>'
            "</svg>"
        )

    return f"""<!doctype html>
<html lang="{lang}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>{page_title}</title>
<link rel="icon" type="image/svg+xml" href="{icon_uri}">
<style>
:root {{
  color-scheme: light dark;
  --co-canvas: #fafbfc;
  --co-surface: #ffffff;
  --co-text: #111827;
  --co-secondary: #4b5563;
  --co-muted: #6b7280;
  --co-border: rgba(15, 23, 42, 0.08);
  --co-pill-bg: #f3f4f6;
  --co-pill-border: rgba(15, 23, 42, 0.06);
  --co-btn-bg: #111827;
  --co-btn-hover: #1f2937;
  --co-btn-text: #ffffff;
  --co-kbd-bg: #f3f4f6;
  --co-kbd-border: rgba(15, 23, 42, 0.14);
  --co-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), 0 18px 44px -16px rgba(15, 23, 42, 0.12);
  --co-success: #10b981;
  --co-success-bg: rgba(16, 185, 129, 0.11);
  --co-success-ring: rgba(16, 185, 129, 0.24);
  --co-success-glow: rgba(16, 185, 129, 0.13);
  --co-success-text: #047857;
  --co-error: #ef4444;
  --co-error-bg: rgba(239, 68, 68, 0.1);
  --co-error-ring: rgba(239, 68, 68, 0.24);
  --co-error-glow: rgba(239, 68, 68, 0.11);
  --co-error-text: #dc2626;
}}
@media (prefers-color-scheme: dark) {{
  :root {{
    --co-canvas: #0b0c0e;
    --co-surface: #15161b;
    --co-text: #f4f4f5;
    --co-secondary: #a1a1aa;
    --co-muted: #8b91a0;
    --co-border: rgba(255, 255, 255, 0.09);
    --co-pill-bg: rgba(255, 255, 255, 0.045);
    --co-pill-border: rgba(255, 255, 255, 0.08);
    --co-btn-bg: #f4f4f5;
    --co-btn-hover: #ffffff;
    --co-btn-text: #0b0c0e;
    --co-kbd-bg: rgba(255, 255, 255, 0.07);
    --co-kbd-border: rgba(255, 255, 255, 0.15);
    --co-shadow: 0 24px 64px -20px rgba(0, 0, 0, 0.62), 0 0 0 1px rgba(255, 255, 255, 0.03);
    --co-success-bg: rgba(16, 185, 129, 0.15);
    --co-success-ring: rgba(52, 211, 153, 0.32);
    --co-success-glow: rgba(16, 185, 129, 0.16);
    --co-success-text: #34d399;
    --co-error-bg: rgba(239, 68, 68, 0.15);
    --co-error-ring: rgba(248, 113, 113, 0.32);
    --co-error-glow: rgba(239, 68, 68, 0.14);
    --co-error-text: #f87171;
  }}
}}
*, *::before, *::after {{ box-sizing: border-box; }}
html, body {{
  margin: 0;
  padding: 0;
  min-height: 100%;
}}
body {{
  min-height: 100dvh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 28px 20px;
  background-color: var(--co-canvas);
  color: var(--co-text);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "Inter", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", system-ui, sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  position: relative;
  overflow-x: hidden;
}}
body::before {{
  content: "";
  position: fixed;
  inset: 0;
  pointer-events: none;
  background: radial-gradient(640px 360px at 50% 0%, var(--co-ambient), transparent 70%);
}}
body.is-success {{ --co-ambient: var(--co-success-glow); }}
body.is-error {{ --co-ambient: var(--co-error-glow); }}
.co-shell {{
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 420px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 20px;
  animation: co-rise 0.42s cubic-bezier(0.16, 1, 0.3, 1) both;
}}
.co-card {{
  width: 100%;
  background: var(--co-surface);
  border: 1px solid var(--co-border);
  border-radius: 24px;
  box-shadow: var(--co-shadow);
  padding: 36px 32px 30px;
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
}}
.co-brand {{
  display: inline-flex;
  align-items: center;
  gap: 9px;
  margin-bottom: 28px;
  user-select: none;
}}
.co-brand-logo {{
  width: 28px;
  height: 28px;
  border-radius: 8px;
  object-fit: cover;
  display: block;
  border: 1px solid var(--co-border);
  box-shadow: 0 1px 3px rgba(15, 23, 42, 0.08);
}}
.co-brand-name {{
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.02em;
  color: var(--co-text);
}}
.co-status-wrap {{
  position: relative;
  width: 68px;
  height: 68px;
  margin-bottom: 22px;
  display: flex;
  align-items: center;
  justify-content: center;
}}
.co-status-halo {{
  position: absolute;
  inset: -6px;
  border-radius: 999px;
  opacity: 0;
  animation: co-halo 0.9s cubic-bezier(0.16, 1, 0.3, 1) 0.14s forwards;
}}
.is-success .co-status-halo {{ background: radial-gradient(circle, var(--co-success-bg) 0%, transparent 72%); }}
.is-error .co-status-halo {{ background: radial-gradient(circle, var(--co-error-bg) 0%, transparent 72%); }}
.co-status-badge {{
  position: relative;
  width: 64px;
  height: 64px;
  border-radius: 999px;
  display: flex;
  align-items: center;
  justify-content: center;
  animation: co-pop 0.48s cubic-bezier(0.16, 1, 0.3, 1) 0.05s both;
}}
.is-success .co-status-badge {{
  background: var(--co-success-bg);
  border: 1px solid var(--co-success-ring);
  color: var(--co-success);
}}
.is-error .co-status-badge {{
  background: var(--co-error-bg);
  border: 1px solid var(--co-error-ring);
  color: var(--co-error);
}}
.co-status-svg {{
  width: 28px;
  height: 28px;
  display: block;
}}
.co-check-path {{
  stroke-dasharray: 28;
  stroke-dashoffset: 28;
  animation: co-draw 0.44s cubic-bezier(0.22, 1, 0.36, 1) 0.22s forwards;
}}
.co-title {{
  margin: 0 0 10px;
  font-size: 22px;
  font-weight: 650;
  line-height: 1.3;
  letter-spacing: -0.025em;
  color: var(--co-text);
}}
.co-desc {{
  margin: 0 0 20px;
  max-width: 348px;
  font-size: 14.5px;
  line-height: 1.65;
  text-wrap: balance;
  color: var(--co-secondary);
}}
.co-pill {{
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  padding: 6px 12px;
  margin-bottom: 26px;
  border-radius: 999px;
  background: var(--co-pill-bg);
  border: 1px solid var(--co-pill-border);
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-secondary);
}}
.is-success .co-pill-icon {{ color: var(--co-success-text); }}
.is-error .co-pill-icon {{ color: var(--co-error-text); }}
.co-pill-icon {{
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}}
.co-pill-text {{
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}}
.co-btn {{
  width: 100%;
  height: 44px;
  border: 0;
  border-radius: 12px;
  background: var(--co-btn-bg);
  color: var(--co-btn-text);
  font: inherit;
  font-size: 14.5px;
  font-weight: 600;
  letter-spacing: -0.01em;
  cursor: pointer;
  transition: background-color 0.16s ease, transform 0.12s ease, opacity 0.16s ease;
}}
.co-btn:hover {{ background: var(--co-btn-hover); }}
.co-btn:active {{ transform: scale(0.988); }}
.co-btn:focus-visible {{
  outline: 2px solid #3b82f6;
  outline-offset: 2px;
}}
.co-hint {{
  margin: 14px 0 0;
  min-height: 20px;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--co-muted);
  opacity: 0.82;
  transition: opacity 0.2s ease, color 0.2s ease;
}}
.co-hint.is-emphasized {{
  opacity: 1;
  color: var(--co-secondary);
}}
.co-kbd {{
  display: inline-block;
  padding: 1px 6px;
  margin: 0 2px;
  border-radius: 5px;
  background: var(--co-kbd-bg);
  border: 1px solid var(--co-kbd-border);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--co-text);
  vertical-align: baseline;
}}
.co-footer {{
  font-size: 12px;
  color: var(--co-muted);
  letter-spacing: 0.01em;
  opacity: 0.78;
}}
@keyframes co-rise {{
  from {{ opacity: 0; transform: translateY(10px); }}
  to {{ opacity: 1; transform: translateY(0); }}
}}
@keyframes co-pop {{
  from {{ opacity: 0; transform: scale(0.84); }}
  to {{ opacity: 1; transform: scale(1); }}
}}
@keyframes co-halo {{
  from {{ opacity: 0; transform: scale(0.8); }}
  to {{ opacity: 1; transform: scale(1); }}
}}
@keyframes co-draw {{
  to {{ stroke-dashoffset: 0; }}
}}
@media (prefers-reduced-motion: reduce) {{
  .co-shell, .co-status-badge, .co-status-halo, .co-check-path {{
    animation: none !important;
    stroke-dashoffset: 0 !important;
    opacity: 1 !important;
  }}
}}
@media (max-width: 460px) {{
  .co-card {{
    padding: 30px 22px 26px;
    border-radius: 20px;
  }}
}}
</style>
</head>
<body class="{state_class}">
  <main class="co-shell">
    <section class="co-card" role="status" aria-live="polite">
      <div class="co-brand">
        <img class="co-brand-logo" src="{icon_uri}" alt="ColabOne" width="28" height="28">
        <span class="co-brand-name">ColabOne</span>
      </div>
      <div class="co-status-wrap">
        <span class="co-status-halo" aria-hidden="true"></span>
        <div class="co-status-badge">{status_svg}</div>
      </div>
      <h1 class="co-title">{heading}</h1>
      <p class="co-desc">{body_text}</p>
      <div class="co-pill">
        {pill_icon}
        <span class="co-pill-text">{badge_text}</span>
      </div>
      <button id="co-close-btn" class="co-btn" type="button">{button_label}</button>
      <p id="co-hint" class="co-hint">{shortcut_prefix}<kbd id="co-kbd" class="co-kbd">Ctrl + W</kbd>{shortcut_suffix}</p>
    </section>
    <footer class="co-footer">{footer_text}</footer>
  </main>
<script>
(function () {{
  var isMac = /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent || "");
  var kbd = document.getElementById("co-kbd");
  if (kbd) {{
    kbd.textContent = isMac ? "\u2318 + W" : "Ctrl + W";
  }}
  var btn = document.getElementById("co-close-btn");
  var hint = document.getElementById("co-hint");
  if (btn) {{
    btn.addEventListener("click", function () {{
      try {{ window.open("", "_self"); }} catch (_e) {{}}
      try {{ window.close(); }} catch (_e) {{}}
      window.setTimeout(function () {{
        if (!window.closed && hint) {{
          hint.classList.add("is-emphasized");
        }}
      }}, 180);
    }});
  }}
}})();
</script>
</body>
</html>"""
