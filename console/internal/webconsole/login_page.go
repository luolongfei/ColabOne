package webconsole

import (
	"html"
	"strings"

	"github.com/luolongfei/ColabOne/console/internal/brandicon"
)

const colabOneWebDefaultLocale = "zh-CN"

const (
	colabOneWebLockSVG = `<svg class="co-lock-svg" viewBox="0 0 24 24" fill="none" aria-hidden="true">` +
		`<rect x="5" y="11" width="14" height="10" rx="2.6" stroke="currentColor" stroke-width="1.9"/>` +
		`<path d="M8.5 11V7.8C8.5 5.7 10.07 4 12 4C13.93 4 15.5 5.7 15.5 7.8V11" stroke="currentColor" stroke-width="1.9" stroke-linecap="round"/>` +
		`<circle cx="12" cy="15.8" r="1.3" fill="currentColor"/>` +
		`</svg>`

	colabOneWebEyeSVG = `<svg class="co-eye-svg" viewBox="0 0 24 24" fill="none" aria-hidden="true">` +
		`<path d="M2.5 12C4.6 7.8 8 5.5 12 5.5C16 5.5 19.4 7.8 21.5 12C19.4 16.2 16 18.5 12 18.5C8 18.5 4.6 16.2 2.5 12Z" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"/>` +
		`<circle cx="12" cy="12" r="3" stroke="currentColor" stroke-width="1.8"/>` +
		`</svg>`

	colabOneWebEyeOffSVG = `<svg class="co-eye-svg" viewBox="0 0 24 24" fill="none" aria-hidden="true">` +
		`<path d="M3 3L21 21" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>` +
		`<path d="M10.4 5.7C10.9 5.57 11.45 5.5 12 5.5C16 5.5 19.4 7.8 21.5 12C20.5 14 19.1 15.6 17.5 16.7M14.1 14.1C13.55 14.65 12.82 15 12 15C10.34 15 9 13.66 9 12C9 11.18 9.35 10.45 9.9 9.9" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>` +
		`<path d="M6.4 7.4C4.8 8.5 3.5 10.1 2.5 12C4.6 16.2 8 18.5 12 18.5C13.2 18.5 14.3 18.3 15.4 17.9" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>` +
		`</svg>`
)

func colabOneWebIconDataURI() string {
	return brandicon.DataURI()
}

type colabOneWebLoginCopy struct {
	pageTitle            string
	badge                string
	heading              string
	description          string
	passcodeLabel        string
	passcodePlaceholder  string
	showPasscode         string
	hidePasscode         string
	submitButton         string
	submittingButton     string
	emptyPasscodeError   string
	invalidPasscodeError string
	rateLimitError       string
	badRequestError      string
	bodyTooLargeError    string
	forbiddenError       string
	sessionError         string
	networkError         string
	genericError         string
	ticketExpiredError   string
	footer               string
}

var colabOneWebLoginCopyByLocale = map[string]colabOneWebLoginCopy{
	"zh-CN": {
		pageTitle:            "远程控制台解锁 · ColabOne",
		badge:                "远程网页控制台",
		heading:              "解锁 ColabOne 控制台",
		description:          "请输入宿主机 ColabOne 设置中显示的访问口令以继续访问。",
		passcodeLabel:        "访问口令",
		passcodePlaceholder:  "请输入访问口令",
		showPasscode:         "显示访问口令",
		hidePasscode:         "隐藏访问口令",
		submitButton:         "解锁并进入控制台",
		submittingButton:     "正在验证...",
		emptyPasscodeError:   "请输入访问口令",
		invalidPasscodeError: "访问口令错误，请重试",
		rateLimitError:       "尝试次数过多，请稍后再试",
		badRequestError:      "请求格式无效",
		bodyTooLargeError:    "请求体超出限制",
		forbiddenError:       "跨源请求已被拒绝",
		sessionError:         "无法创建登录会话，请重试",
		networkError:         "无法连接到 ColabOne 宿主机，请检查网络后重试",
		genericError:         "验证未通过，请重试",
		ticketExpiredError:   "快捷登录链接已失效或已使用，请重新生成或输入访问口令",
		footer:               "ColabOne · 远端 GPU 工作空间",
	},
	"en-US": {
		pageTitle:            "Unlock Web Console · ColabOne",
		badge:                "Remote Web Console",
		heading:              "Unlock ColabOne Console",
		description:          "Enter the access passcode shown in ColabOne Desktop Settings to continue.",
		passcodeLabel:        "Access Passcode",
		passcodePlaceholder:  "Enter access passcode",
		showPasscode:         "Show passcode",
		hidePasscode:         "Hide passcode",
		submitButton:         "Unlock Console",
		submittingButton:     "Verifying...",
		emptyPasscodeError:   "Please enter the access passcode",
		invalidPasscodeError: "Incorrect access passcode; please try again",
		rateLimitError:       "Too many attempts; please try again later",
		badRequestError:      "Invalid request format",
		bodyTooLargeError:    "Request body exceeds the size limit",
		forbiddenError:       "Cross-origin request rejected",
		sessionError:         "Unable to create a session; please try again",
		networkError:         "Unable to reach the ColabOne host. Check your connection and try again.",
		genericError:         "Verification failed. Please try again.",
		ticketExpiredError:   "Sign-in link has expired or was already used; generate a new link or enter your access passcode",
		footer:               "ColabOne · Remote GPU Workspace",
	},
	"zh-TW": {
		pageTitle:            "遠端主控台解鎖 · ColabOne",
		badge:                "遠端網頁主控台",
		heading:              "解鎖 ColabOne 主控台",
		description:          "請輸入宿主機 ColabOne 設定中顯示的存取通行碼以繼續存取。",
		passcodeLabel:        "存取通行碼",
		passcodePlaceholder:  "請輸入存取通行碼",
		showPasscode:         "顯示存取通行碼",
		hidePasscode:         "隱藏存取通行碼",
		submitButton:         "解鎖並進入主控台",
		submittingButton:     "正在驗證...",
		emptyPasscodeError:   "請輸入存取通行碼",
		invalidPasscodeError: "存取通行碼錯誤，請重試",
		rateLimitError:       "嘗試次數過多，請稍後再試",
		badRequestError:      "請求格式無效",
		bodyTooLargeError:    "請求內容超出限制",
		forbiddenError:       "跨來源請求已被拒絕",
		sessionError:         "無法建立登入工作階段，請重試",
		networkError:         "無法連線至 ColabOne 宿主機，請檢查網路後重試",
		genericError:         "驗證未通過，請重試",
		ticketExpiredError:   "快速登入連結已失效或已使用，請重新產生或輸入存取通行碼",
		footer:               "ColabOne · 遠端 GPU 工作區",
	},
	"zh-HK": {
		pageTitle:            "遠端控制台解鎖 · ColabOne",
		badge:                "遠端網頁控制台",
		heading:              "解鎖 ColabOne 控制台",
		description:          "請輸入宿主機 ColabOne 設定中顯示的存取通行碼以繼續使用。",
		passcodeLabel:        "存取通行碼",
		passcodePlaceholder:  "請輸入存取通行碼",
		showPasscode:         "顯示存取通行碼",
		hidePasscode:         "隱藏存取通行碼",
		submitButton:         "解鎖並進入控制台",
		submittingButton:     "正在驗證...",
		emptyPasscodeError:   "請輸入存取通行碼",
		invalidPasscodeError: "存取通行碼錯誤，請重試",
		rateLimitError:       "嘗試次數過多，請稍後再試",
		badRequestError:      "請求格式無效",
		bodyTooLargeError:    "請求內容超出限制",
		forbiddenError:       "跨來源請求已被拒絕",
		sessionError:         "無法建立登入工作階段，請重試",
		networkError:         "無法連接至 ColabOne 宿主機，請檢查網絡後再試",
		genericError:         "驗證失敗，請再試",
		ticketExpiredError:   "快速登入連結已失效或已使用，請重新產生或輸入存取通行碼",
		footer:               "ColabOne · 遠端 GPU 工作區",
	},
	"ja-JP": {
		pageTitle:            "Web コンソールロック解除 · ColabOne",
		badge:                "リモート Web コンソール",
		heading:              "ColabOne コンソールをロック解除",
		description:          "ホスト側 ColabOne の設定に表示されているアクセスパスコードを入力してください。",
		passcodeLabel:        "アクセスパスコード",
		passcodePlaceholder:  "パスコードを入力",
		showPasscode:         "パスコードを表示",
		hidePasscode:         "パスコードを隠す",
		submitButton:         "ロック解除して開く",
		submittingButton:     "確認中...",
		emptyPasscodeError:   "アクセスパスコードを入力してください",
		invalidPasscodeError: "アクセスパスコードが正しくありません。再試行してください",
		rateLimitError:       "試行回数が多すぎます。しばらくしてから再試行してください",
		badRequestError:      "リクエスト形式が無効です",
		bodyTooLargeError:    "リクエストボディが上限を超えています",
		forbiddenError:       "クロスオリジンリクエストは拒否されました",
		sessionError:         "セッションを作成できませんでした。もう一度お試しください",
		networkError:         "ColabOne ホストに接続できません。ネットワークを確認してください。",
		genericError:         "認証に失敗しました。もう一度お試しください。",
		ticketExpiredError:   "ワンタイムまたはクイックログインリンクは失効しているか使用済みです。再生成するかアクセスパスコードを入力してください",
		footer:               "ColabOne · リモート GPU ワークスペース",
	},
	"ko-KR": {
		pageTitle:            "웹 콘솔 잠금 해제 · ColabOne",
		badge:                "원격 웹 콘솔",
		heading:              "ColabOne 콘솔 잠금 해제",
		description:          "호스트 ColabOne 설정에 표시된 접속 암호를 입력하세요.",
		passcodeLabel:        "접속 암호",
		passcodePlaceholder:  "접속 암호 입력",
		showPasscode:         "접속 암호 표시",
		hidePasscode:         "접속 암호 숨기기",
		submitButton:         "잠금 해제 후 열기",
		submittingButton:     "확인 중...",
		emptyPasscodeError:   "접속 암호를 입력해 주세요",
		invalidPasscodeError: "접속 암호가 올바르지 않습니다. 다시 시도해 주세요",
		rateLimitError:       "시도 횟수가 너무 많습니다. 잠시 후 다시 시도해 주세요",
		badRequestError:      "요청 형식이 올바르지 않습니다",
		bodyTooLargeError:    "요청 본문이 허용 크기를 초과했습니다",
		forbiddenError:       "교차 출처 요청이 거부되었습니다",
		sessionError:         "세션을 생성하지 못했습니다. 다시 시도해 주세요",
		networkError:         "ColabOne 호스트에 연결할 수 없습니다. 네트워크를 확인한 후 다시 시도하세요.",
		genericError:         "인증에 실패했습니다. 다시 시도해 주세요.",
		ticketExpiredError:   "빠른 로그인 링크가 만료되었거나 이미 사용되었습니다. 새로 생성하거나 접속 암호를 입력해 주세요",
		footer:               "ColabOne · 원격 GPU 워크스페이스",
	},
}

func localeFromAcceptLanguage(header string) string {
	lower := strings.ToLower(strings.TrimSpace(header))
	if lower == "" {
		return colabOneWebDefaultLocale
	}
	for _, token := range strings.Split(lower, ",") {
		tag := strings.ReplaceAll(strings.TrimSpace(strings.Split(token, ";")[0]), "_", "-")
		switch {
		case strings.HasPrefix(tag, "zh-hk"),
			strings.HasPrefix(tag, "zh-mo"),
			strings.HasPrefix(tag, "zh-hant-hk"),
			strings.HasPrefix(tag, "zh-hant-mo"),
			strings.HasPrefix(tag, "yue"):
			return "zh-HK"
		case strings.HasPrefix(tag, "zh-tw"), strings.HasPrefix(tag, "zh-hant"):
			return "zh-TW"
		case strings.HasPrefix(tag, "zh"):
			return "zh-CN"
		case strings.HasPrefix(tag, "en"):
			return "en-US"
		case strings.HasPrefix(tag, "ja"):
			return "ja-JP"
		case strings.HasPrefix(tag, "ko"):
			return "ko-KR"
		}
	}
	return colabOneWebDefaultLocale
}

func resolveWebLoginCopy(locale string) (string, colabOneWebLoginCopy) {
	if copySet, ok := colabOneWebLoginCopyByLocale[locale]; ok {
		return locale, copySet
	}
	return colabOneWebDefaultLocale, colabOneWebLoginCopyByLocale[colabOneWebDefaultLocale]
}

const colabOneWebLoginCSS = `
:root {
  color-scheme: light dark;
  --co-canvas: #fafbfc;
  --co-surface: #ffffff;
  --co-text: #111827;
  --co-secondary: #4b5563;
  --co-muted: #6b7280;
  --co-border: rgba(15, 23, 42, 0.09);
  --co-pill-bg: #f3f4f6;
  --co-pill-border: rgba(15, 23, 42, 0.06);
  --co-input-bg: #f9fafb;
  --co-input-border: rgba(15, 23, 42, 0.14);
  --co-input-focus: #2563eb;
  --co-focus-ring: rgba(37, 99, 235, 0.18);
  --co-btn-bg: #111827;
  --co-btn-hover: #1f2937;
  --co-btn-text: #ffffff;
  --co-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), 0 18px 44px -16px rgba(15, 23, 42, 0.12);
  --co-accent-bg: rgba(37, 99, 235, 0.1);
  --co-accent-ring: rgba(37, 99, 235, 0.22);
  --co-accent-text: #2563eb;
  --co-ambient: rgba(37, 99, 235, 0.09);
  --co-error-bg: rgba(239, 68, 68, 0.09);
  --co-error-border: rgba(239, 68, 68, 0.24);
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
    --co-pill-bg: rgba(255, 255, 255, 0.05);
    --co-pill-border: rgba(255, 255, 255, 0.09);
    --co-input-bg: rgba(255, 255, 255, 0.04);
    --co-input-border: rgba(255, 255, 255, 0.14);
    --co-input-focus: #60a5fa;
    --co-focus-ring: rgba(96, 165, 250, 0.24);
    --co-btn-bg: #f4f4f5;
    --co-btn-hover: #ffffff;
    --co-btn-text: #0b0c0e;
    --co-shadow: 0 24px 64px -20px rgba(0, 0, 0, 0.62), 0 0 0 1px rgba(255, 255, 255, 0.03);
    --co-accent-bg: rgba(59, 130, 246, 0.16);
    --co-accent-ring: rgba(96, 165, 250, 0.3);
    --co-accent-text: #60a5fa;
    --co-ambient: rgba(59, 130, 246, 0.14);
    --co-error-bg: rgba(239, 68, 68, 0.15);
    --co-error-border: rgba(248, 113, 113, 0.3);
    --co-error-text: #f87171;
  }
}
*, *::before, *::after { box-sizing: border-box; }
html, body { margin: 0; padding: 0; min-height: 100%; }
body {
  min-height: 100dvh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: max(24px, env(safe-area-inset-top)) max(18px, env(safe-area-inset-right)) max(28px, env(safe-area-inset-bottom)) max(18px, env(safe-area-inset-left));
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
  background: radial-gradient(640px 360px at 50% 0%, var(--co-ambient), transparent 72%);
}
.co-shell {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 420px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 20px;
  animation: co-rise 0.38s cubic-bezier(0.16, 1, 0.3, 1) both;
}
.co-card {
  width: 100%;
  background: var(--co-surface);
  border: 1px solid var(--co-border);
  border-radius: 24px;
  box-shadow: var(--co-shadow);
  padding: 34px 30px 30px;
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
}
.co-brand {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  margin-bottom: 14px;
  user-select: none;
}
.co-brand-logo {
  width: 32px;
  height: 32px;
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
.co-pill {
  display: inline-flex;
  align-items: center;
  padding: 5px 12px;
  margin-bottom: 22px;
  border-radius: 999px;
  background: var(--co-pill-bg);
  border: 1px solid var(--co-pill-border);
  font-size: 12px;
  font-weight: 500;
  color: var(--co-secondary);
}
.co-lock-badge {
  width: 56px;
  height: 56px;
  border-radius: 999px;
  margin-bottom: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--co-accent-bg);
  border: 1px solid var(--co-accent-ring);
  color: var(--co-accent-text);
}
.co-lock-svg {
  width: 25px;
  height: 25px;
  display: block;
}
.co-title {
  margin: 0 0 8px;
  font-size: 21px;
  font-weight: 650;
  line-height: 1.3;
  letter-spacing: -0.025em;
  color: var(--co-text);
}
.co-desc {
  margin: 0 0 22px;
  max-width: 340px;
  font-size: 14px;
  line-height: 1.6;
  color: var(--co-secondary);
}
.co-alert {
  width: 100%;
  margin: 0 0 16px;
  padding: 10px 13px;
  border-radius: 12px;
  background: var(--co-error-bg);
  border: 1px solid var(--co-error-border);
  color: var(--co-error-text);
  font-size: 13px;
  line-height: 1.5;
  text-align: left;
}
.co-alert.is-hidden {
  display: none;
}
.co-form {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 14px;
  text-align: left;
}
.co-label {
  display: block;
  margin: 0 0 6px;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-secondary);
}
.co-input-wrap {
  position: relative;
  width: 100%;
}
.co-input {
  width: 100%;
  height: 44px;
  padding: 0 44px 0 14px;
  border-radius: 12px;
  border: 1px solid var(--co-input-border);
  background: var(--co-input-bg);
  color: var(--co-text);
  font: inherit;
  font-size: 14.5px;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}
.co-input:focus {
  outline: none;
  border-color: var(--co-input-focus);
  box-shadow: 0 0 0 3px var(--co-focus-ring);
}
.co-eye-btn {
  position: absolute;
  top: 50%;
  right: 6px;
  transform: translateY(-50%);
  width: 34px;
  height: 34px;
  padding: 0;
  border: 0;
  border-radius: 9px;
  background: transparent;
  color: var(--co-muted);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: color 0.15s ease, background-color 0.15s ease;
}
.co-eye-btn:hover {
  color: var(--co-text);
  background: var(--co-pill-bg);
}
.co-eye-btn:focus-visible {
  outline: 2px solid var(--co-input-focus);
  outline-offset: 1px;
}
.co-eye-svg {
  width: 18px;
  height: 18px;
  display: block;
}
.co-icon-hide { display: none; }
.co-eye-btn.is-visible .co-icon-show { display: none; }
.co-eye-btn.is-visible .co-icon-hide { display: block; }
.co-btn {
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
}
.co-btn:hover:not(:disabled) { background: var(--co-btn-hover); }
.co-btn:active:not(:disabled) { transform: scale(0.988); }
.co-btn:disabled { opacity: 0.65; cursor: not-allowed; }
.co-btn:focus-visible {
  outline: 2px solid var(--co-input-focus);
  outline-offset: 2px;
}
.co-footer {
  font-size: 12px;
  color: var(--co-muted);
  letter-spacing: 0.01em;
  opacity: 0.8;
}
@keyframes co-rise {
  from { opacity: 0; transform: translateY(10px); }
  to { opacity: 1; transform: translateY(0); }
}
@media (prefers-reduced-motion: reduce) {
  .co-shell { animation: none !important; opacity: 1 !important; }
}
@media (max-width: 460px) {
  .co-card { padding: 28px 22px 24px; border-radius: 20px; }
}
@media (pointer: coarse), (max-width: 720px) {
  .co-input { font-size: 16px; }
}
`

const colabOneWebLoginJS = `
(function () {
  var form = document.getElementById("co-login-form");
  var input = document.getElementById("co-passcode");
  var toggle = document.getElementById("co-toggle-btn");
  var submitBtn = document.getElementById("co-submit-btn");
  var alertBox = document.getElementById("co-alert");
  if (!form || !input || !submitBtn || !alertBox) return;

  var msgEmpty = form.getAttribute("data-err-empty") || "";
  var msgNet = form.getAttribute("data-err-net") || "";
  var msgGeneric = form.getAttribute("data-err-generic") || "";
  var labelSubmit = submitBtn.getAttribute("data-label-idle") || submitBtn.textContent;
  var labelBusy = submitBtn.getAttribute("data-label-busy") || labelSubmit;

  function showError(msg) {
    alertBox.textContent = msg || msgGeneric;
    alertBox.classList.remove("is-hidden");
  }

  function clearError() {
    alertBox.textContent = "";
    alertBox.classList.add("is-hidden");
  }

  if (toggle) {
    var labelShow = toggle.getAttribute("data-label-show") || "";
    var labelHide = toggle.getAttribute("data-label-hide") || "";
    toggle.addEventListener("click", function () {
      var visible = input.type === "text";
      input.type = visible ? "password" : "text";
      toggle.classList.toggle("is-visible", !visible);
      toggle.setAttribute("aria-pressed", visible ? "false" : "true");
      toggle.setAttribute("aria-label", visible ? labelShow : labelHide);
      input.focus();
    });
  }

  form.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var passcode = input.value.trim();
    if (!passcode) {
      showError(msgEmpty);
      input.focus();
      return;
    }
    clearError();
    var redirecting = false;
    submitBtn.disabled = true;
    submitBtn.textContent = labelBusy;

    fetch("/_colabone_web/login", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-ColabOne-Client": "web"
      },
      body: JSON.stringify({ passcode: passcode })
    })
      .then(function (resp) {
        if (resp.ok) {
          redirecting = true;
          window.location.replace("/");
          return null;
        }
        var status = resp.status;
        return resp.json().catch(function () { return {}; }).then(function (data) {
          var errMsg = (data && data.error) ? String(data.error) : msgGeneric;
          if (status === 429 && data && typeof data.retry_after === "number" && data.retry_after > 0) {
            errMsg = errMsg + " (" + Math.ceil(data.retry_after) + "s)";
          }
          showError(errMsg);
          if (status !== 429) {
            input.focus();
            input.select();
          }
        });
      })
      .catch(function () {
        showError(msgNet);
      })
      .finally(function () {
        if (!redirecting) {
          submitBtn.disabled = false;
          submitBtn.textContent = labelSubmit;
        }
      });
  });
})();
`

func renderColabOneWebLoginPage(locale, errCode, nonce string) string {
	lang, copySet := resolveWebLoginCopy(locale)

	initialErr := ""
	if errCode == "ticket_expired" {
		initialErr = copySet.ticketExpiredError
	}
	alertClass := "co-alert is-hidden"
	if initialErr != "" {
		alertClass = "co-alert"
	}

	iconURI := colabOneWebIconDataURI()
	escNonce := html.EscapeString(nonce)
	var b strings.Builder
	b.Grow(len(colabOneWebLoginCSS) + len(colabOneWebLoginJS) + 2*len(iconURI) + 2048)

	b.WriteString("<!doctype html>\n<html lang=\"")
	b.WriteString(html.EscapeString(lang))
	b.WriteString("\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width,initial-scale=1,viewport-fit=cover\">\n<meta name=\"color-scheme\" content=\"light dark\">\n<title>")
	b.WriteString(html.EscapeString(copySet.pageTitle))
	b.WriteString("</title>\n<link rel=\"icon\" type=\"image/svg+xml\" href=\"")
	b.WriteString(iconURI)
	b.WriteString("\">\n<link rel=\"alternate icon\" type=\"image/x-icon\" href=\"/favicon.ico\">\n<link rel=\"apple-touch-icon\" sizes=\"180x180\" href=\"/apple-touch-icon.png\">\n<style nonce=\"")
	b.WriteString(escNonce)
	b.WriteString("\">")
	b.WriteString(colabOneWebLoginCSS)
	b.WriteString("</style>\n</head>\n<body>\n  <main class=\"co-shell\">\n    <section class=\"co-card\">\n      <div class=\"co-brand\">\n        <img class=\"co-brand-logo\" src=\"")
	b.WriteString(iconURI)
	b.WriteString("\" alt=\"ColabOne\" width=\"32\" height=\"32\">\n        <span class=\"co-brand-name\">ColabOne</span>\n      </div>\n      <div class=\"co-pill\">")
	b.WriteString(html.EscapeString(copySet.badge))
	b.WriteString("</div>\n      <div class=\"co-lock-badge\">")
	b.WriteString(colabOneWebLockSVG)
	b.WriteString("</div>\n      <h1 class=\"co-title\">")
	b.WriteString(html.EscapeString(copySet.heading))
	b.WriteString("</h1>\n      <p class=\"co-desc\">")
	b.WriteString(html.EscapeString(copySet.description))
	b.WriteString("</p>\n      <div id=\"co-alert\" class=\"")
	b.WriteString(alertClass)
	b.WriteString("\" role=\"alert\" aria-live=\"polite\">")
	b.WriteString(html.EscapeString(initialErr))
	b.WriteString("</div>\n      <form id=\"co-login-form\" class=\"co-form\" autocomplete=\"off\" data-err-empty=\"")
	b.WriteString(html.EscapeString(copySet.emptyPasscodeError))
	b.WriteString("\" data-err-net=\"")
	b.WriteString(html.EscapeString(copySet.networkError))
	b.WriteString("\" data-err-generic=\"")
	b.WriteString(html.EscapeString(copySet.genericError))
	b.WriteString("\">\n        <div>\n          <label class=\"co-label\" for=\"co-passcode\">")
	b.WriteString(html.EscapeString(copySet.passcodeLabel))
	b.WriteString("</label>\n          <div class=\"co-input-wrap\">\n            <input id=\"co-passcode\" class=\"co-input\" type=\"password\" name=\"passcode\" autocomplete=\"current-password\" placeholder=\"")
	b.WriteString(html.EscapeString(copySet.passcodePlaceholder))
	b.WriteString("\" required autofocus>\n            <button id=\"co-toggle-btn\" class=\"co-eye-btn\" type=\"button\" aria-pressed=\"false\" aria-label=\"")
	b.WriteString(html.EscapeString(copySet.showPasscode))
	b.WriteString("\" data-label-show=\"")
	b.WriteString(html.EscapeString(copySet.showPasscode))
	b.WriteString("\" data-label-hide=\"")
	b.WriteString(html.EscapeString(copySet.hidePasscode))
	b.WriteString("\">\n              <span class=\"co-icon-show\">")
	b.WriteString(colabOneWebEyeSVG)
	b.WriteString("</span>\n              <span class=\"co-icon-hide\">")
	b.WriteString(colabOneWebEyeOffSVG)
	b.WriteString("</span>\n            </button>\n          </div>\n        </div>\n        <button id=\"co-submit-btn\" class=\"co-btn\" type=\"submit\" data-label-idle=\"")
	b.WriteString(html.EscapeString(copySet.submitButton))
	b.WriteString("\" data-label-busy=\"")
	b.WriteString(html.EscapeString(copySet.submittingButton))
	b.WriteString("\">")
	b.WriteString(html.EscapeString(copySet.submitButton))
	b.WriteString("</button>\n      </form>\n    </section>\n    <footer class=\"co-footer\">")
	b.WriteString(html.EscapeString(copySet.footer))
	b.WriteString("</footer>\n  </main>\n<script nonce=\"")
	b.WriteString(escNonce)
	b.WriteString("\">")
	b.WriteString(colabOneWebLoginJS)
	b.WriteString("</script>\n</body>\n</html>")

	return b.String()
}
