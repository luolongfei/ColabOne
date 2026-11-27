// Package oauthpage renders the self-contained, localized OAuth completion
// page served by ColabOne's loopback callback.
package oauthpage

import (
	"html"
	"strings"

	"github.com/luolongfei/ColabOne/console/internal/brandicon"
)

const colabOneDefaultLocale = "zh-CN"

type colabOneOAuthCopy struct {
	pageTitleOK    string
	pageTitleErr   string
	headingOK      string
	headingErr     string
	badgeOK        string
	badgeErr       string
	closeButton    string
	returnButton   string
	shortcutPrefix string
	shortcutSuffix string
	footer         string
}

var colabOneOAuthCopyByLocale = map[string]colabOneOAuthCopy{
	"zh-CN": {
		pageTitleOK:    "授权成功 · ColabOne",
		pageTitleErr:   "授权未完成 · ColabOne",
		headingOK:      "已授权登录 ColabOne",
		headingErr:     "登录未完成",
		badgeOK:        "安全凭据已保存至本机",
		badgeErr:       "未保存任何凭据",
		closeButton:    "关闭此页面",
		returnButton:   "关闭并返回重试",
		shortcutPrefix: "如页面未自动关闭，按 ",
		shortcutSuffix: " 即可关闭此标签页",
		footer:         "ColabOne · 远端 GPU 工作空间",
	},
	"en-US": {
		pageTitleOK:    "Signed In · ColabOne",
		pageTitleErr:   "Sign-in Incomplete · ColabOne",
		headingOK:      "Signed in to ColabOne",
		headingErr:     "Sign-in was not completed",
		badgeOK:        "Credentials saved securely on this device",
		badgeErr:       "No credentials were saved",
		closeButton:    "Close this tab",
		returnButton:   "Close and try again",
		shortcutPrefix: "If the tab stays open, press ",
		shortcutSuffix: " to close it",
		footer:         "ColabOne · Remote GPU Workspace",
	},
	"zh-TW": {
		pageTitleOK:    "授權成功 · ColabOne",
		pageTitleErr:   "授權未完成 · ColabOne",
		headingOK:      "已授權登入 ColabOne",
		headingErr:     "登入尚未完成",
		badgeOK:        "安全憑證已儲存至本機",
		badgeErr:       "未儲存任何憑證",
		closeButton:    "關閉此分頁",
		returnButton:   "關閉並返回重試",
		shortcutPrefix: "若分頁未自動關閉，按 ",
		shortcutSuffix: " 即可關閉",
		footer:         "ColabOne · 遠端 GPU 工作區",
	},
	"zh-HK": {
		pageTitleOK:    "授權成功 · ColabOne",
		pageTitleErr:   "授權未完成 · ColabOne",
		headingOK:      "已授權登入 ColabOne",
		headingErr:     "登入尚未完成",
		badgeOK:        "安全憑證已儲存至本機",
		badgeErr:       "未儲存任何憑證",
		closeButton:    "關閉此分頁",
		returnButton:   "關閉並返回再試",
		shortcutPrefix: "如分頁未自動關閉，按 ",
		shortcutSuffix: " 即可關閉",
		footer:         "ColabOne · 遠端 GPU 工作區",
	},
	"ja-JP": {
		pageTitleOK:    "認証完了 · ColabOne",
		pageTitleErr:   "認証未完了 · ColabOne",
		headingOK:      "ColabOne へのログイン完了",
		headingErr:     "ログインが完了しませんでした",
		badgeOK:        "認証情報はこの端末に安全に保存されました",
		badgeErr:       "認証情報は保存されていません",
		closeButton:    "このタブを閉じる",
		returnButton:   "閉じてアプリで再試行",
		shortcutPrefix: "自動で閉じない場合は ",
		shortcutSuffix: " で閉じられます",
		footer:         "ColabOne · リモート GPU ワークスペース",
	},
	"ko-KR": {
		pageTitleOK:    "인증 완료 · ColabOne",
		pageTitleErr:   "로그인 미완료 · ColabOne",
		headingOK:      "ColabOne 로그인 완료",
		headingErr:     "로그인이 완료되지 않았습니다",
		badgeOK:        "인증 정보가 이 기기에 안전하게 저장되었습니다",
		badgeErr:       "저장된 인증 정보가 없습니다",
		closeButton:    "이 탭 닫기",
		returnButton:   "닫고 다시 시도",
		shortcutPrefix: "탭이 자동으로 닫히지 않으면 ",
		shortcutSuffix: " 키로 닫을 수 있습니다",
		footer:         "ColabOne · 원격 GPU 워크스페이스",
	},
}

const (
	colabOneStatusSVGOK = `<svg class="co-status-svg" viewBox="0 0 24 24" fill="none" aria-hidden="true">` +
		`<path class="co-check-path" d="M5.5 12.6L9.9 17L18.5 7.5" ` +
		`stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>` +
		`</svg>`

	colabOnePillIconOK = `<svg class="co-pill-icon" viewBox="0 0 16 16" fill="none" aria-hidden="true">` +
		`<path d="M8 1.75L13 3.75V7.65C13 10.8 10.9 13.45 8 14.25C5.1 13.45 3 10.8 3 7.65V3.75L8 1.75Z" ` +
		`stroke="currentColor" stroke-width="1.35" stroke-linejoin="round"/>` +
		`<path d="M5.8 8.15L7.3 9.65L10.35 6.5" ` +
		`stroke="currentColor" stroke-width="1.35" stroke-linecap="round" stroke-linejoin="round"/>` +
		`</svg>`

	colabOneStatusSVGErr = `<svg class="co-status-svg" viewBox="0 0 24 24" fill="none" aria-hidden="true">` +
		`<path d="M8 8L16 16M16 8L8 16" ` +
		`stroke="currentColor" stroke-width="2.3" stroke-linecap="round"/>` +
		`</svg>`

	colabOnePillIconErr = `<svg class="co-pill-icon" viewBox="0 0 16 16" fill="none" aria-hidden="true">` +
		`<circle cx="8" cy="8" r="5.75" stroke="currentColor" stroke-width="1.35"/>` +
		`<path d="M8 5.2V8.4M8 10.85H8.01" ` +
		`stroke="currentColor" stroke-width="1.45" stroke-linecap="round"/>` +
		`</svg>`
)

const colabOneOAuthStyleAndBodyOpen = `">
<style>
:root {
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
}
@media (prefers-color-scheme: dark) {
  :root {
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
  }
}
*, *::before, *::after { box-sizing: border-box; }
html, body {
  margin: 0;
  padding: 0;
  min-height: 100%;
}
body {
  min-height: 100dvh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: max(28px, env(safe-area-inset-top)) max(20px, env(safe-area-inset-right)) max(28px, env(safe-area-inset-bottom)) max(20px, env(safe-area-inset-left));
  background-color: var(--co-canvas);
  color: var(--co-text);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "Inter", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", system-ui, sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  position: relative;
  overflow-x: hidden;
}
body::before {
  content: "";
  position: fixed;
  inset: 0;
  pointer-events: none;
  background: radial-gradient(640px 360px at 50% 0%, var(--co-ambient), transparent 70%);
}
body.is-success { --co-ambient: var(--co-success-glow); }
body.is-error { --co-ambient: var(--co-error-glow); }
.co-shell {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 420px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 20px;
  animation: co-rise 0.42s cubic-bezier(0.16, 1, 0.3, 1) both;
}
.co-card {
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
}
.co-brand {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  margin-bottom: 28px;
  user-select: none;
}
.co-brand-logo {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  object-fit: cover;
  display: block;
  background: #ffffff;
  border: 1px solid var(--co-border);
  box-shadow: 0 1px 3px rgba(15, 23, 42, 0.08);
}
.co-brand-name {
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.02em;
  color: var(--co-text);
}
.co-status-wrap {
  position: relative;
  width: 68px;
  height: 68px;
  margin-bottom: 22px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.co-status-halo {
  position: absolute;
  inset: -6px;
  border-radius: 999px;
  opacity: 0;
  animation: co-halo 0.9s cubic-bezier(0.16, 1, 0.3, 1) 0.14s forwards;
}
.is-success .co-status-halo { background: radial-gradient(circle, var(--co-success-bg) 0%, transparent 72%); }
.is-error .co-status-halo { background: radial-gradient(circle, var(--co-error-bg) 0%, transparent 72%); }
.co-status-badge {
  position: relative;
  width: 64px;
  height: 64px;
  border-radius: 999px;
  display: flex;
  align-items: center;
  justify-content: center;
  animation: co-pop 0.48s cubic-bezier(0.16, 1, 0.3, 1) 0.05s both;
}
.is-success .co-status-badge {
  background: var(--co-success-bg);
  border: 1px solid var(--co-success-ring);
  color: var(--co-success);
}
.is-error .co-status-badge {
  background: var(--co-error-bg);
  border: 1px solid var(--co-error-ring);
  color: var(--co-error);
}
.co-status-svg {
  width: 28px;
  height: 28px;
  display: block;
}
.co-check-path {
  stroke-dasharray: 28;
  stroke-dashoffset: 28;
  animation: co-draw 0.44s cubic-bezier(0.22, 1, 0.36, 1) 0.22s forwards;
}
.co-title {
  margin: 0 0 10px;
  max-width: 100%;
  font-size: 22px;
  font-weight: 650;
  line-height: 1.3;
  letter-spacing: -0.025em;
  overflow-wrap: break-word;
  color: var(--co-text);
}
.co-desc {
  margin: 0 0 20px;
  width: 100%;
  max-width: min(348px, 100%);
  font-size: 14.5px;
  line-height: 1.65;
  text-wrap: balance;
  overflow-wrap: anywhere;
  word-break: break-word;
  color: var(--co-secondary);
}
.co-pill {
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
}
.co-pill.is-status {
  border-radius: 14px;
  padding: 7px 14px;
}
.is-success .co-pill-icon { color: var(--co-success-text); }
.is-error .co-pill-icon { color: var(--co-error-text); }
.co-pill-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}
.co-pill-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.co-pill.is-status .co-pill-text {
  white-space: normal;
  overflow: visible;
  text-overflow: clip;
  overflow-wrap: break-word;
  text-align: left;
  line-height: 1.4;
}
.co-btn {
  width: 100%;
  min-height: 44px;
  padding: 10px 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 12px;
  background: var(--co-btn-bg);
  color: var(--co-btn-text);
  font: inherit;
  font-size: 14.5px;
  font-weight: 600;
  line-height: 1.25;
  letter-spacing: -0.01em;
  cursor: pointer;
  transition: background-color 0.16s ease, transform 0.12s ease, opacity 0.16s ease;
}
.co-btn:hover { background: var(--co-btn-hover); }
.co-btn:active { transform: scale(0.988); }
.co-btn:focus-visible {
  outline: 2px solid #3b82f6;
  outline-offset: 2px;
}
.co-hint {
  margin: 14px 0 0;
  min-height: 20px;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--co-muted);
  transition: opacity 0.2s ease, color 0.2s ease;
}
.co-hint.is-emphasized {
  color: var(--co-secondary);
}
.co-kbd {
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
  white-space: nowrap;
}
.co-footer {
  font-size: 12px;
  color: var(--co-muted);
  letter-spacing: 0.01em;
}
@keyframes co-rise {
  from { opacity: 0; transform: translateY(10px); }
  to { opacity: 1; transform: translateY(0); }
}
@keyframes co-pop {
  from { opacity: 0; transform: scale(0.84); }
  to { opacity: 1; transform: scale(1); }
}
@keyframes co-halo {
  from { opacity: 0; transform: scale(0.8); }
  to { opacity: 1; transform: scale(1); }
}
@keyframes co-draw {
  to { stroke-dashoffset: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .co-shell, .co-status-badge, .co-status-halo, .co-check-path {
    animation: none !important;
    stroke-dashoffset: 0 !important;
    opacity: 1 !important;
  }
}
@media (max-width: 460px) {
  .co-card {
    padding: 30px 22px 26px;
    border-radius: 20px;
  }
}
</style>
</head>
<body class="`

const colabOneOAuthScriptAndClose = `</footer>
  </main>
<script>
(function () {
  var isMac = /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent || "");
  var kbd = document.getElementById("co-kbd");
  if (kbd) {
    kbd.textContent = isMac ? "⌘ + W" : "Ctrl + W";
  }
  var btn = document.getElementById("co-close-btn");
  var hint = document.getElementById("co-hint");
  if (btn) {
    btn.addEventListener("click", function () {
      try { window.open("", "_self"); } catch (_e) {}
      try { window.close(); } catch (_e) {}
      window.setTimeout(function () {
        if (!window.closed && hint) {
          hint.classList.add("is-emphasized");
        }
      }, 180);
    });
  }
})();
</script>
</body>
</html>`

func colabOneIconDataURI() string {
	return brandicon.DataURI()
}

func colabOneIconDataURIFromBytes(raw []byte) string {
	return brandicon.DataURIFromSVGBytes(raw)
}

// RenderColabOneOAuthPage renders a clean, modern, light/dark-adaptive OAuth
// completion HTML page localized to one of the six supported ColabOne locales
// (falling back to zh-CN for unknown locales). All dynamic strings are
// HTML-escaped to prevent injection.
func RenderColabOneOAuthPage(locale string, ok bool, message, email string) string {
	lang := locale
	copySet, exists := colabOneOAuthCopyByLocale[lang]
	if !exists {
		lang = colabOneDefaultLocale
		copySet = colabOneOAuthCopyByLocale[lang]
	}
	iconURI := colabOneIconDataURI()

	var rawTitle, rawHeading, rawBadge, rawButton, stateClass, statusSVG, pillIcon string
	pillClass := "co-pill is-status"
	if ok {
		rawTitle = copySet.pageTitleOK
		rawHeading = copySet.headingOK
		if trimmedEmail := strings.TrimSpace(email); trimmedEmail != "" {
			rawBadge = trimmedEmail
			pillClass = "co-pill"
		} else {
			rawBadge = copySet.badgeOK
		}
		rawButton = copySet.closeButton
		stateClass = "is-success"
		statusSVG = colabOneStatusSVGOK
		pillIcon = colabOnePillIconOK
	} else {
		rawTitle = copySet.pageTitleErr
		rawHeading = copySet.headingErr
		rawBadge = copySet.badgeErr
		rawButton = copySet.returnButton
		stateClass = "is-error"
		statusSVG = colabOneStatusSVGErr
		pillIcon = colabOnePillIconErr
	}

	pageTitle := html.EscapeString(rawTitle)
	heading := html.EscapeString(rawHeading)
	bodyText := html.EscapeString(message)
	badgeText := html.EscapeString(rawBadge)
	buttonLabel := html.EscapeString(rawButton)
	shortcutPrefix := html.EscapeString(copySet.shortcutPrefix)
	shortcutSuffix := html.EscapeString(copySet.shortcutSuffix)
	footerText := html.EscapeString(copySet.footer)

	var b strings.Builder
	b.Grow(len(colabOneOAuthStyleAndBodyOpen) + len(colabOneOAuthScriptAndClose) + 2*len(iconURI) + 1024)

	b.WriteString("<!doctype html>\n<html lang=\"")
	b.WriteString(lang)
	b.WriteString("\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width,initial-scale=1,viewport-fit=cover\">\n<meta name=\"color-scheme\" content=\"light dark\">\n<title>")
	b.WriteString(pageTitle)
	b.WriteString("</title>\n<link rel=\"icon\" type=\"image/svg+xml\" href=\"")
	b.WriteString(iconURI)
	b.WriteString(colabOneOAuthStyleAndBodyOpen)
	b.WriteString(stateClass)
	b.WriteString("\">\n  <main class=\"co-shell\">\n    <section class=\"co-card\" role=\"status\" aria-live=\"polite\">\n      <div class=\"co-brand\">\n        <img class=\"co-brand-logo\" src=\"")
	b.WriteString(iconURI)
	b.WriteString("\" alt=\"ColabOne\" width=\"28\" height=\"28\">\n        <span class=\"co-brand-name\">ColabOne</span>\n      </div>\n      <div class=\"co-status-wrap\">\n        <span class=\"co-status-halo\" aria-hidden=\"true\"></span>\n        <div class=\"co-status-badge\">")
	b.WriteString(statusSVG)
	b.WriteString("</div>\n      </div>\n      <h1 class=\"co-title\">")
	b.WriteString(heading)
	b.WriteString("</h1>\n      <p class=\"co-desc\">")
	b.WriteString(bodyText)
	b.WriteString("</p>\n      <div class=\"")
	b.WriteString(pillClass)
	b.WriteString("\">\n        ")
	b.WriteString(pillIcon)
	b.WriteString("\n        <span class=\"co-pill-text\">")
	b.WriteString(badgeText)
	b.WriteString("</span>\n      </div>\n      <button id=\"co-close-btn\" class=\"co-btn\" type=\"button\">")
	b.WriteString(buttonLabel)
	b.WriteString("</button>\n      <p id=\"co-hint\" class=\"co-hint\">")
	b.WriteString(shortcutPrefix)
	b.WriteString(`<kbd id="co-kbd" class="co-kbd">Ctrl + W</kbd>`)
	b.WriteString(shortcutSuffix)
	b.WriteString("</p>\n    </section>\n    <footer class=\"co-footer\">")
	b.WriteString(footerText)
	b.WriteString(colabOneOAuthScriptAndClose)

	return b.String()
}
