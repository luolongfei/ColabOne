package webconsole

import (
	"html"
	"strings"
)

const colabOneShareErrorIconSVG = `<svg class="co-status-svg" viewBox="0 0 24 24" fill="none" aria-hidden="true">` +
	`<circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="1.9"/>` +
	`<path d="M12 8V12.6" stroke="currentColor" stroke-width="1.9" stroke-linecap="round"/>` +
	`<circle cx="12" cy="16.2" r="1.15" fill="currentColor"/>` +
	`</svg>`

const (
	workflowShareErrMissing      = "missing"
	workflowShareErrInvalid      = "invalid"
	workflowShareErrExpired      = "expired"
	workflowShareErrUsed         = "used"
	workflowShareErrRevoked      = "revoked"
	workflowShareErrRateLimited  = "rate_limited"
	workflowShareErrSessionLimit = "session_limit"
)

type colabOneShareErrorCopy struct {
	pageTitle           string
	badge               string
	missingHeading      string
	missingDesc         string
	invalidHeading      string
	invalidDesc         string
	expiredHeading      string
	expiredDesc         string
	usedHeading         string
	usedDesc            string
	revokedHeading      string
	revokedDesc         string
	rateLimitHeading    string
	rateLimitDesc       string
	sessionLimitHeading string
	sessionLimitDesc    string
	hint                string
	footer              string
}

var colabOneShareErrorCopyByLocale = map[string]colabOneShareErrorCopy{
	"zh-CN": {
		pageTitle:           "分享链接不可用 · ColabOne",
		badge:               "工作流分享",
		missingHeading:      "缺少有效的分享链接",
		missingDesc:         "当前页面未包含有效的分享凭证，请通过分享者提供的完整链接重新打开。",
		invalidHeading:      "分享链接无效",
		invalidDesc:         "该分享链接格式不正确或已失效，请检查链接是否完整，或联系分享者重新发送。",
		expiredHeading:      "分享链接已过期",
		expiredDesc:         "此分享链接已超过有效期，无法建立新的访问会话。如需继续使用，请联系分享者生成新的链接。",
		usedHeading:         "一次性分享链接已被使用",
		usedDesc:            "此链接仅允许首次打开使用，且已被激活。如需访问，请联系分享者生成新的分享链接。",
		revokedHeading:      "分享链接已被撤销",
		revokedDesc:         "分享者已手动停止此分享链接的访问权限，原有会话及链接均已失效。",
		rateLimitHeading:    "验证尝试过于频繁",
		rateLimitDesc:       "短时间内验证失败次数过多，请稍候片刻后再试。",
		sessionLimitHeading: "分享会话数量已达上限",
		sessionLimitDesc:    "当前分享会话数量已达上限，请稍后再试。",
		hint:                "如需获取新的工作流使用权限，请联系分享者。",
		footer:              "ColabOne · 远端 GPU 工作流分享",
	},
	"en-US": {
		pageTitle:           "Share Link Unavailable · ColabOne",
		badge:               "Workflow Share",
		missingHeading:      "Missing Share Link",
		missingDesc:         "This page requires a valid workflow share link. Open the full link provided by the host.",
		invalidHeading:      "Invalid Share Link",
		invalidDesc:         "This share link is invalid or malformed. Verify the full URL or ask the host for a new link.",
		expiredHeading:      "Share Link Expired",
		expiredDesc:         "This share link has expired and can no longer start new sessions. Ask the host to issue a fresh link.",
		usedHeading:         "One-Time Link Already Used",
		usedDesc:            "This single-use share link has already been claimed. Ask the host to generate a new share link.",
		revokedHeading:      "Share Link Revoked",
		revokedDesc:         "The host has revoked this share link. Both the link and any previous sessions are no longer active.",
		rateLimitHeading:    "Too Many Attempts",
		rateLimitDesc:       "Too many failed verification attempts in a short period. Please wait a moment and try again.",
		sessionLimitHeading: "Session Limit Reached",
		sessionLimitDesc:    "The maximum number of active share sessions has been reached. Please try again later.",
		hint:                "Contact the host if you need a new workflow share link.",
		footer:              "ColabOne · Remote GPU Workflow Share",
	},
	"zh-TW": {
		pageTitle:           "分享連結無法使用 · ColabOne",
		badge:               "工作流程分享",
		missingHeading:      "缺少有效的分享連結",
		missingDesc:         "目前頁面未包含有效的分享憑證，請透過分享者提供的完整連結重新開啟。",
		invalidHeading:      "分享連結無效",
		invalidDesc:         "此分享連結格式不正確或已失效，請確認連結是否完整，或請分享者重新傳送。",
		expiredHeading:      "分享連結已過期",
		expiredDesc:         "此分享連結已超過有效期限，無法建立新的工作階段。如需繼續使用，請聯絡分享者產生新連結。",
		usedHeading:         "一次性分享連結已被使用",
		usedDesc:            "此連結僅供首次開啟使用且已被啟用。如需存取，請聯絡分享者產生新的分享連結。",
		revokedHeading:      "分享連結已被撤銷",
		revokedDesc:         "分享者已手動停止此分享連結的存取權限，原有工作階段與連結皆已失效。",
		rateLimitHeading:    "驗證嘗試過於頻繁",
		rateLimitDesc:       "短時間內驗證失敗次數過多，請稍候再試。",
		sessionLimitHeading: "分享工作階段數量已達上限",
		sessionLimitDesc:    "目前分享工作階段數量已達上限，請稍後再試。",
		hint:                "如需取得新的工作流程使用權限，請聯絡分享者。",
		footer:              "ColabOne · 遠端 GPU 工作流程分享",
	},
	"zh-HK": {
		pageTitle:           "分享連結不可用 · ColabOne",
		badge:               "工作流分享",
		missingHeading:      "缺少有效的分享連結",
		missingDesc:         "目前頁面未包含有效的分享憑證，請使用分享者提供的完整連結重新開啟。",
		invalidHeading:      "分享連結無效",
		invalidDesc:         "此分享連結格式不正確或已失效，請檢查連結是否完整，或聯絡分享者重新發送。",
		expiredHeading:      "分享連結已過期",
		expiredDesc:         "此分享連結已超出有效期，無法建立新工作階段。如需繼續使用，請聯絡分享者產生新連結。",
		usedHeading:         "一次性分享連結已被使用",
		usedDesc:            "此連結只限首次開啟使用，現已被啟用。如需使用，請聯絡分享者產生新的分享連結。",
		revokedHeading:      "分享連結已被撤銷",
		revokedDesc:         "分享者已手動停止此分享連結的存取權限，原有工作階段及連結均已失效。",
		rateLimitHeading:    "驗證嘗試過於頻繁",
		rateLimitDesc:       "短時間內驗證失敗次數過多，請稍後再試。",
		sessionLimitHeading: "分享工作階段數量已達上限",
		sessionLimitDesc:    "目前分享工作階段數量已達上限，請稍後再試。",
		hint:                "如需獲取新的工作流使用權限，請聯絡分享者。",
		footer:              "ColabOne · 遠端 GPU 工作流分享",
	},
	"ja-JP": {
		pageTitle:           "共有リンクを利用できません · ColabOne",
		badge:               "ワークフロー共有",
		missingHeading:      "有効な共有リンクがありません",
		missingDesc:         "このページを開くには有効な共有リンクが必要です。共有元から受け取った完全な URL を開いてください。",
		invalidHeading:      "無効な共有リンクです",
		invalidDesc:         "この共有リンクは形式が正しくないか無効です。URL を確認するか、共有元へ新しいリンクを依頼してください。",
		expiredHeading:      "共有リンクの有効期限が切れました",
		expiredDesc:         "この共有リンクは有効期限を過ぎているため、新しいセッションを開始できません。共有元へ再発行を依頼してください。",
		usedHeading:         "ワンタイム共有リンクは使用済みです",
		usedDesc:            "この 1 回限りの共有リンクは既に使用されています。アクセスするには共有元から新しいリンクを受け取ってください。",
		revokedHeading:      "共有リンクは取り消されました",
		revokedDesc:         "共有元によりこの共有リンクのアクセス権限が停止されました。リンクおよび既存セッションは無効です。",
		rateLimitHeading:    "試行回数が多すぎます",
		rateLimitDesc:       "短時間に検証の失敗が繰り返されました。しばらく待ってから再度お試しください。",
		sessionLimitHeading: "共有セッション数が上限に達しました",
		sessionLimitDesc:    "現在アクティブな共有セッション数が上限に達しています。しばらくしてから再度お試しください。",
		hint:                "新しいワークフロー共有リンクが必要な場合は、共有元へご連絡ください。",
		footer:              "ColabOne · リモート GPU ワークフロー共有",
	},
	"ko-KR": {
		pageTitle:           "공유 링크를 사용할 수 없음 · ColabOne",
		badge:               "워크플로 공유",
		missingHeading:      "유효한 공유 링크가 없습니다",
		missingDesc:         "이 페이지를 열려면 유효한 워크플로 공유 링크가 필요합니다. 호스트가 제공한 전체 링크로 다시 열어 주세요.",
		invalidHeading:      "유효하지 않은 공유 링크",
		invalidDesc:         "공유 링크 형식이 올바르지 않거나 사용할 수 없습니다. 전체 주소를 확인하거나 호스트에게 새 링크를 요청해 주세요.",
		expiredHeading:      "공유 링크가 만료되었습니다",
		expiredDesc:         "이 공유 링크는 유효 기간이 지나 새 세션을 시작할 수 없습니다. 호스트에게 새 링크 발급을 요청해 주세요.",
		usedHeading:         "일회용 공유 링크가 이미 사용되었습니다",
		usedDesc:            "이 일회용 공유 링크는 이미 활성화되었습니다. 계속 이용하려면 호스트에게 새 링크를 요청해 주세요.",
		revokedHeading:      "공유 링크가 취소되었습니다",
		revokedDesc:         "호스트가 이 공유 링크의 접근 권한을 중지했습니다. 링크와 기존 세션이 모두 만료되었습니다.",
		rateLimitHeading:    "시도 횟수가 너무 많습니다",
		rateLimitDesc:       "짧은 시간 동안 인증 실패가 너무 많았습니다. 잠시 후 다시 시도해 주세요.",
		sessionLimitHeading: "공유 세션 한도에 도달했습니다",
		sessionLimitDesc:    "현재 활성 공유 세션 수가 한도에 도달했습니다. 잠시 후 다시 시도해 주세요.",
		hint:                "새 워크플로 공유 링크가 필요하면 호스트에게 문의해 주세요.",
		footer:              "ColabOne · 원격 GPU 워크플로 공유",
	},
}

type colabOneShareSwitchCopy struct {
	pageTitle    string
	badge        string
	heading      string
	desc         string
	currentLabel string
	targetLabel  string
	unnamedShare string
	confirmBtn   string
	keepBtn      string
	hint         string
	footer       string
}

var colabOneShareSwitchCopyByLocale = map[string]colabOneShareSwitchCopy{
	"zh-CN": {
		pageTitle:    "确认切换工作流分享 · ColabOne",
		badge:        "工作流分享",
		heading:      "确认切换到新的分享会话？",
		desc:         "当前浏览器已登录另一个工作流分享会话。确认切换后，将替换当前会话凭证并打开新的分享页面。",
		currentLabel: "当前分享",
		targetLabel:  "目标分享",
		unnamedShare: "未命名分享",
		confirmBtn:   "确认切换",
		keepBtn:      "保留当前分享",
		hint:         "如果这不是你主动打开的新链接，请选择「保留当前分享」。",
		footer:       "ColabOne · 远端 GPU 工作流分享",
	},
	"en-US": {
		pageTitle:    "Confirm Share Switch · ColabOne",
		badge:        "Workflow Share",
		heading:      "Switch to another workflow share?",
		desc:         "This browser already has an active workflow share session. Confirming will replace the current session and open the new share.",
		currentLabel: "Current share",
		targetLabel:  "New share",
		unnamedShare: "Untitled share",
		confirmBtn:   "Switch share",
		keepBtn:      "Keep current share",
		hint:         "If you did not intend to open this link, choose Keep current share.",
		footer:       "ColabOne · Remote GPU Workflow Share",
	},
	"zh-TW": {
		pageTitle:    "確認切換工作流程分享 · ColabOne",
		badge:        "工作流程分享",
		heading:      "確認切換至新的分享工作階段？",
		desc:         "此瀏覽器目前已登入另一個工作流程分享工作階段。確認切換後，將取代目前工作階段憑證並開啟新的分享頁面。",
		currentLabel: "目前分享",
		targetLabel:  "目標分享",
		unnamedShare: "未命名分享",
		confirmBtn:   "確認切換",
		keepBtn:      "保留目前分享",
		hint:         "若這不是您主動開啟的新連結，請選擇「保留目前分享」，必要時可聯絡分享者確認。",
		footer:       "ColabOne · 遠端 GPU 工作流程分享",
	},
	"zh-HK": {
		pageTitle:    "確認切換工作流分享 · ColabOne",
		badge:        "工作流分享",
		heading:      "確認切換至新的分享工作階段？",
		desc:         "目前瀏覽器已登入另一個工作流分享工作階段。確認切換後，將會替換目前工作階段憑證並開啟新的分享頁面。",
		currentLabel: "目前分享",
		targetLabel:  "目標分享",
		unnamedShare: "未命名分享",
		confirmBtn:   "確認切換",
		keepBtn:      "保留目前分享",
		hint:         "如非你主動開啟此新連結，請選擇「保留目前分享」。",
		footer:       "ColabOne · 遠端 GPU 工作流分享",
	},
	"ja-JP": {
		pageTitle:    "共有リンクの切り替え確認 · ColabOne",
		badge:        "ワークフロー共有",
		heading:      "別の共有セッションに切り替えますか？",
		desc:         "このブラウザーではすでに別のワークフロー共有セッションが有効です。切り替えると現在のセッションが置き換わります。",
		currentLabel: "現在の共有",
		targetLabel:  "新しい共有",
		unnamedShare: "無題の共有",
		confirmBtn:   "切り替える",
		keepBtn:      "現在の共有を維持",
		hint:         "意図せず開いたリンクの場合は「現在の共有を維持」を選択してください。",
		footer:       "ColabOne · リモート GPU ワークフロー共有",
	},
	"ko-KR": {
		pageTitle:    "워크플로 공유 전환 확인 · ColabOne",
		badge:        "워크플로 공유",
		heading:      "새 공유 세션으로 전환할까요?",
		desc:         "이 브라우저에는 이미 다른 워크플로 공유 세션이 활성화되어 있습니다. 전환을 확인하면 현재 세션이 새 공유로 교체됩니다.",
		currentLabel: "현재 공유",
		targetLabel:  "새 공유",
		unnamedShare: "제목 없는 공유",
		confirmBtn:   "전환 확인",
		keepBtn:      "현재 공유 유지",
		hint:         "직접 열지 않은 링크라면 '현재 공유 유지'를 선택해 주세요.",
		footer:       "ColabOne · 원격 GPU 워크플로 공유",
	},
}

func resolveWorkflowShareSwitchCopy(locale string) (string, colabOneShareSwitchCopy) {
	if copySet, ok := colabOneShareSwitchCopyByLocale[locale]; ok {
		return locale, copySet
	}
	return colabOneWebDefaultLocale, colabOneShareSwitchCopyByLocale[colabOneWebDefaultLocale]
}

func resolveWorkflowShareErrorCopy(locale string) (string, colabOneShareErrorCopy) {
	if copySet, ok := colabOneShareErrorCopyByLocale[locale]; ok {
		return locale, copySet
	}
	return colabOneWebDefaultLocale, colabOneShareErrorCopyByLocale[colabOneWebDefaultLocale]
}

const colabOneShareErrorCSS = `
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
  --co-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), 0 18px 44px -16px rgba(15, 23, 42, 0.12);
  --co-warn-bg: rgba(239, 68, 68, 0.09);
  --co-warn-ring: rgba(239, 68, 68, 0.22);
  --co-warn-text: #dc2626;
  --co-hint-bg: #f9fafb;
  --co-ambient: rgba(37, 99, 235, 0.08);
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
    --co-shadow: 0 24px 64px -20px rgba(0, 0, 0, 0.62), 0 0 0 1px rgba(255, 255, 255, 0.03);
    --co-warn-bg: rgba(239, 68, 68, 0.15);
    --co-warn-ring: rgba(248, 113, 113, 0.28);
    --co-warn-text: #f87171;
    --co-hint-bg: rgba(255, 255, 255, 0.035);
    --co-ambient: rgba(59, 130, 246, 0.12);
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
  animation: co-rise 0.36s cubic-bezier(0.16, 1, 0.3, 1) both;
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
.co-status-badge {
  width: 56px;
  height: 56px;
  border-radius: 999px;
  margin-bottom: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--co-warn-bg);
  border: 1px solid var(--co-warn-ring);
  color: var(--co-warn-text);
}
.co-status-svg {
  width: 26px;
  height: 26px;
  display: block;
}
.co-title {
  margin: 0 0 10px;
  font-size: 20px;
  font-weight: 650;
  line-height: 1.32;
  letter-spacing: -0.02em;
  color: var(--co-text);
}
.co-desc {
  margin: 0 0 20px;
  max-width: 340px;
  font-size: 14px;
  line-height: 1.65;
  color: var(--co-secondary);
}
.co-hint {
  width: 100%;
  padding: 12px 14px;
  border-radius: 12px;
  background: var(--co-hint-bg);
  border: 1px solid var(--co-border);
  font-size: 13px;
  line-height: 1.55;
  color: var(--co-muted);
}
.co-footer {
  font-size: 12px;
  color: var(--co-muted);
  letter-spacing: 0.01em;
  opacity: 0.8;
}
.co-switch-badge {
  width: 56px;
  height: 56px;
  border-radius: 999px;
  margin-bottom: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(37, 99, 235, 0.1);
  border: 1px solid rgba(37, 99, 235, 0.24);
  color: #2563eb;
}
@media (prefers-color-scheme: dark) {
  .co-switch-badge {
    background: rgba(59, 130, 246, 0.16);
    border-color: rgba(96, 165, 250, 0.28);
    color: #60a5fa;
  }
}
.co-switch-summary {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0 0 20px;
  padding: 14px 16px;
  border-radius: 14px;
  background: var(--co-hint-bg);
  border: 1px solid var(--co-border);
  text-align: left;
}
.co-switch-row {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.co-switch-label {
  font-size: 11.5px;
  font-weight: 500;
  color: var(--co-muted);
}
.co-switch-value {
  font-size: 14px;
  font-weight: 600;
  color: var(--co-text);
  word-break: break-word;
}
.co-switch-divider {
  height: 1px;
  background: var(--co-border);
}
.co-switch-actions {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0 0 16px;
}
.co-btn {
  width: 100%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 42px;
  padding: 10px 16px;
  border-radius: 12px;
  font-family: inherit;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.25;
  text-decoration: none;
  cursor: pointer;
  border: 1px solid transparent;
}
.co-btn-primary {
  background: #2563eb;
  color: #ffffff;
}
.co-btn-primary:hover {
  background: #1d4ed8;
}
.co-btn-secondary {
  background: var(--co-pill-bg);
  border-color: var(--co-border);
  color: var(--co-text);
}
.co-btn-secondary:hover {
  opacity: 0.9;
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
`

const colabOneShareSwitchIconSVG = `<svg class="co-status-svg" viewBox="0 0 24 24" fill="none" aria-hidden="true">` +
	`<path d="M7 7H17M17 7L14 4M17 7L14 10" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/>` +
	`<path d="M17 17H7M7 17L10 14M7 17L10 20" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/>` +
	`</svg>`

func renderColabOneWorkflowShareErrorPage(locale, reason, nonce string) string {
	lang, copySet := resolveWorkflowShareErrorCopy(locale)

	heading := copySet.invalidHeading
	desc := copySet.invalidDesc
	switch reason {
	case workflowShareErrMissing:
		heading = copySet.missingHeading
		desc = copySet.missingDesc
	case workflowShareErrExpired:
		heading = copySet.expiredHeading
		desc = copySet.expiredDesc
	case workflowShareErrUsed:
		heading = copySet.usedHeading
		desc = copySet.usedDesc
	case workflowShareErrRevoked:
		heading = copySet.revokedHeading
		desc = copySet.revokedDesc
	case workflowShareErrRateLimited:
		heading = copySet.rateLimitHeading
		desc = copySet.rateLimitDesc
	case workflowShareErrSessionLimit:
		heading = copySet.sessionLimitHeading
		desc = copySet.sessionLimitDesc
	}

	iconURI := colabOneWebIconDataURI()
	escNonce := html.EscapeString(nonce)

	var b strings.Builder
	b.Grow(len(colabOneShareErrorCSS) + 2*len(iconURI) + 1536)
	b.WriteString("<!doctype html>\n<html lang=\"")
	b.WriteString(html.EscapeString(lang))
	b.WriteString("\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width,initial-scale=1,viewport-fit=cover\">\n<meta name=\"color-scheme\" content=\"light dark\">\n<meta name=\"robots\" content=\"noindex,nofollow\">\n<title>")
	b.WriteString(html.EscapeString(copySet.pageTitle))
	b.WriteString("</title>\n<link rel=\"icon\" type=\"image/svg+xml\" href=\"")
	b.WriteString(iconURI)
	b.WriteString("\">\n<link rel=\"alternate icon\" type=\"image/x-icon\" href=\"/favicon.ico\">\n<link rel=\"apple-touch-icon\" sizes=\"180x180\" href=\"/apple-touch-icon.png\">\n<style nonce=\"")
	b.WriteString(escNonce)
	b.WriteString("\">")
	b.WriteString(colabOneShareErrorCSS)
	b.WriteString("</style>\n</head>\n<body>\n  <main class=\"co-shell\">\n    <section class=\"co-card\" data-share-error=\"")
	b.WriteString(html.EscapeString(reason))
	b.WriteString("\">\n      <div class=\"co-brand\">\n        <img class=\"co-brand-logo\" src=\"")
	b.WriteString(iconURI)
	b.WriteString("\" alt=\"ColabOne\" width=\"32\" height=\"32\">\n        <span class=\"co-brand-name\">ColabOne</span>\n      </div>\n      <div class=\"co-pill\">")
	b.WriteString(html.EscapeString(copySet.badge))
	b.WriteString("</div>\n      <div class=\"co-status-badge\">")
	b.WriteString(colabOneShareErrorIconSVG)
	b.WriteString("</div>\n      <h1 class=\"co-title\">")
	b.WriteString(html.EscapeString(heading))
	b.WriteString("</h1>\n      <p class=\"co-desc\">")
	b.WriteString(html.EscapeString(desc))
	b.WriteString("</p>\n      <div class=\"co-hint\">")
	b.WriteString(html.EscapeString(copySet.hint))
	b.WriteString("</div>\n    </section>\n    <footer class=\"co-footer\">")
	b.WriteString(html.EscapeString(copySet.footer))
	b.WriteString("</footer>\n  </main>\n</body>\n</html>")

	return b.String()
}

func renderColabOneWorkflowShareSwitchPage(locale, currentTitle, targetTitle, switchToken, nonce string) string {
	lang, copySet := resolveWorkflowShareSwitchCopy(locale)

	curDisplay := strings.TrimSpace(currentTitle)
	if curDisplay == "" {
		curDisplay = copySet.unnamedShare
	}
	targetDisplay := strings.TrimSpace(targetTitle)
	if targetDisplay == "" {
		targetDisplay = copySet.unnamedShare
	}

	iconURI := colabOneWebIconDataURI()
	escNonce := html.EscapeString(nonce)

	var b strings.Builder
	b.Grow(len(colabOneShareErrorCSS) + 2*len(iconURI) + 2048)
	b.WriteString("<!doctype html>\n<html lang=\"")
	b.WriteString(html.EscapeString(lang))
	b.WriteString("\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width,initial-scale=1,viewport-fit=cover\">\n<meta name=\"color-scheme\" content=\"light dark\">\n<meta name=\"robots\" content=\"noindex,nofollow\">\n<title>")
	b.WriteString(html.EscapeString(copySet.pageTitle))
	b.WriteString("</title>\n<link rel=\"icon\" type=\"image/svg+xml\" href=\"")
	b.WriteString(iconURI)
	b.WriteString("\">\n<link rel=\"alternate icon\" type=\"image/x-icon\" href=\"/favicon.ico\">\n<link rel=\"apple-touch-icon\" sizes=\"180x180\" href=\"/apple-touch-icon.png\">\n<style nonce=\"")
	b.WriteString(escNonce)
	b.WriteString("\">")
	b.WriteString(colabOneShareErrorCSS)
	b.WriteString("</style>\n</head>\n<body>\n  <main class=\"co-shell\">\n    <section class=\"co-card\" data-share-switch=\"confirm\">\n      <div class=\"co-brand\">\n        <img class=\"co-brand-logo\" src=\"")
	b.WriteString(iconURI)
	b.WriteString("\" alt=\"ColabOne\" width=\"32\" height=\"32\">\n        <span class=\"co-brand-name\">ColabOne</span>\n      </div>\n      <div class=\"co-pill\">")
	b.WriteString(html.EscapeString(copySet.badge))
	b.WriteString("</div>\n      <div class=\"co-switch-badge\">")
	b.WriteString(colabOneShareSwitchIconSVG)
	b.WriteString("</div>\n      <h1 class=\"co-title\">")
	b.WriteString(html.EscapeString(copySet.heading))
	b.WriteString("</h1>\n      <p class=\"co-desc\">")
	b.WriteString(html.EscapeString(copySet.desc))
	b.WriteString("</p>\n      <div class=\"co-switch-summary\">\n        <div class=\"co-switch-row\">\n          <span class=\"co-switch-label\">")
	b.WriteString(html.EscapeString(copySet.currentLabel))
	b.WriteString("</span>\n          <span class=\"co-switch-value\">")
	b.WriteString(html.EscapeString(curDisplay))
	b.WriteString("</span>\n        </div>\n        <div class=\"co-switch-divider\"></div>\n        <div class=\"co-switch-row\">\n          <span class=\"co-switch-label\">")
	b.WriteString(html.EscapeString(copySet.targetLabel))
	b.WriteString("</span>\n          <span class=\"co-switch-value\">")
	b.WriteString(html.EscapeString(targetDisplay))
	b.WriteString("</span>\n        </div>\n      </div>\n      <form class=\"co-switch-actions\" method=\"post\" action=\"")
	b.WriteString(workflowShareSwitchRoutePath)
	b.WriteString("\">\n        <input type=\"hidden\" name=\"token\" value=\"")
	b.WriteString(html.EscapeString(switchToken))
	b.WriteString("\">\n        <button class=\"co-btn co-btn-primary\" type=\"submit\">")
	b.WriteString(html.EscapeString(copySet.confirmBtn))
	b.WriteString("</button>\n        <a class=\"co-btn co-btn-secondary\" href=\"")
	b.WriteString(WorkflowShareRoutePath)
	b.WriteString("\">")
	b.WriteString(html.EscapeString(copySet.keepBtn))
	b.WriteString("</a>\n      </form>\n      <div class=\"co-hint\">")
	b.WriteString(html.EscapeString(copySet.hint))
	b.WriteString("</div>\n    </section>\n    <footer class=\"co-footer\">")
	b.WriteString(html.EscapeString(copySet.footer))
	b.WriteString("</footer>\n  </main>\n</body>\n</html>")

	return b.String()
}
