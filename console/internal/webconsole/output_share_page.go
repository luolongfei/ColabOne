package webconsole

import (
	"html"
	"strings"
)

const (
	outputShareErrMissing          = "missing"
	outputShareErrInvalid          = "invalid"
	outputShareErrExpired          = "expired"
	outputShareErrChallengeExpired = "challenge_expired"
	outputShareErrRevoked          = "revoked"
	outputShareErrRateLimited      = "rate_limited"
	outputShareErrSessionLimit     = "session_limit"
)

type colabOneOutputShareErrorCopy struct {
	pageTitle               string
	badge                   string
	missingHeading          string
	missingDesc             string
	invalidHeading          string
	invalidDesc             string
	expiredHeading          string
	expiredDesc             string
	challengeExpiredHeading string
	challengeExpiredDesc    string
	revokedHeading          string
	revokedDesc             string
	rateLimitHeading        string
	rateLimitDesc           string
	sessionLimitHeading     string
	sessionLimitDesc        string
	hint                    string
	footer                  string
}

var colabOneOutputShareErrorCopyByLocale = map[string]colabOneOutputShareErrorCopy{
	"zh-CN": {
		pageTitle:               "成果物相册不可用 · ColabOne",
		badge:                   "成果物相册分享",
		missingHeading:          "缺少有效的相册分享链接",
		missingDesc:             "当前页面未包含有效的相册分享凭证，请通过分享者提供的完整链接重新打开。",
		invalidHeading:          "相册分享链接无效",
		invalidDesc:             "该相册分享链接格式不正确或已失效，请检查链接是否完整，或联系分享者重新发送。",
		expiredHeading:          "相册分享链接已过期",
		expiredDesc:             "此相册分享链接已超过有效期，无法继续查看。如需再次访问，请联系分享者生成新的链接。",
		challengeExpiredHeading: "验证页已过期",
		challengeExpiredDesc:    "验证页已过期，请刷新页面后重试。",
		revokedHeading:          "相册分享链接已被撤销",
		revokedDesc:             "分享者已手动停止此相册分享链接的访问权限，原有会话及链接均已失效。",
		rateLimitHeading:        "验证尝试过于频繁",
		rateLimitDesc:           "短时间内验证失败次数过多，请稍候片刻后再试。",
		sessionLimitHeading:     "当前图集访问人数已达上限",
		sessionLimitDesc:        "当前图集访问人数已达上限，请稍后再试。",
		hint:                    "如需继续查看该组作品，请联系分享者获取新链接。",
		footer:                  "ColabOne · 远端 GPU 成果物相册",
	},
	"en-US": {
		pageTitle:               "Output Gallery Unavailable · ColabOne",
		badge:                   "Output Gallery",
		missingHeading:          "Missing Gallery Share Link",
		missingDesc:             "This page requires a valid output gallery link. Open the full link provided by the host.",
		invalidHeading:          "Invalid Gallery Share Link",
		invalidDesc:             "This output gallery link is invalid or malformed. Verify the full URL or ask the host for a new link.",
		expiredHeading:          "Gallery Share Link Expired",
		expiredDesc:             "This output gallery link has expired and can no longer be viewed. Ask the host to issue a fresh link.",
		challengeExpiredHeading: "Verification Page Expired",
		challengeExpiredDesc:    "The verification page has expired. Please refresh the page and try again.",
		revokedHeading:          "Gallery Share Link Revoked",
		revokedDesc:             "The host has revoked this output gallery link. Both the link and any previous sessions are no longer active.",
		rateLimitHeading:        "Too Many Attempts",
		rateLimitDesc:           "Too many failed verification attempts in a short period. Please wait a moment and try again.",
		sessionLimitHeading:     "Session Limit Reached",
		sessionLimitDesc:        "The maximum number of active gallery sessions has been reached. Please try again later.",
		hint:                    "Contact the host if you need a new output gallery link.",
		footer:                  "ColabOne · Remote GPU Output Gallery",
	},
	"zh-TW": {
		pageTitle:               "成果物相簿無法使用 · ColabOne",
		badge:                   "成果物相簿分享",
		missingHeading:          "缺少有效的相簿分享連結",
		missingDesc:             "目前頁面未包含有效的相簿分享憑證，請透過分享者提供的完整連結重新開啟。",
		invalidHeading:          "相簿分享連結無效",
		invalidDesc:             "此相簿分享連結格式不正確或已失效，請確認連結是否完整，或請分享者重新傳送。",
		expiredHeading:          "相簿分享連結已過期",
		expiredDesc:             "此相簿分享連結已超過有效期限，無法繼續檢視。如需再次存取，請聯絡分享者產生新連結。",
		challengeExpiredHeading: "驗證頁已過期",
		challengeExpiredDesc:    "驗證頁已過期，請重新整理頁面後再試。",
		revokedHeading:          "相簿分享連結已被撤銷",
		revokedDesc:             "分享者已手動停止此相簿分享連結的存取權限，原有工作階段與連結皆已失效。",
		rateLimitHeading:        "驗證嘗試過於頻繁",
		rateLimitDesc:           "短時間內驗證失敗次數過多，請稍候再試。",
		sessionLimitHeading:     "目前相簿存取人數已達上限",
		sessionLimitDesc:        "目前相簿存取人數已達上限，請稍後再試。",
		hint:                    "如需繼續檢視此組作品，請聯絡分享者取得新連結。",
		footer:                  "ColabOne · 遠端 GPU 成果物相簿",
	},
	"zh-HK": {
		pageTitle:               "成果物相簿不可用 · ColabOne",
		badge:                   "成果物相簿分享",
		missingHeading:          "缺少有效的相簿分享連結",
		missingDesc:             "目前頁面未包含有效的相簿分享憑證，請使用分享者提供的完整連結重新開啟。",
		invalidHeading:          "相簿分享連結無效",
		invalidDesc:             "此相簿分享連結格式不正確或已失效，請檢查連結是否完整，或聯絡分享者重新發送。",
		expiredHeading:          "相簿分享連結已過期",
		expiredDesc:             "此相簿分享連結已超出有效期，無法繼續瀏覽。如需再次查看，請聯絡分享者產生新連結。",
		challengeExpiredHeading: "驗證頁已過期",
		challengeExpiredDesc:    "驗證頁已過期，請重新整理頁面後再試。",
		revokedHeading:          "相簿分享連結已被撤銷",
		revokedDesc:             "分享者已手動停止此相簿分享連結的存取權限，原有工作階段及連結均已失效。",
		rateLimitHeading:        "驗證嘗試過於頻繁",
		rateLimitDesc:           "短時間內驗證失敗次數過多，請稍後再試。",
		sessionLimitHeading:     "目前相簿存取人數已達上限",
		sessionLimitDesc:        "目前相簿存取人數已達上限，請稍後再試。",
		hint:                    "如需繼續瀏覽此組作品，請聯絡分享者索取新連結。",
		footer:                  "ColabOne · 遠端 GPU 成果物相簿",
	},
	"ja-JP": {
		pageTitle:               "作品ギャラリーを利用できません · ColabOne",
		badge:                   "作品ギャラリー共有",
		missingHeading:          "有効な共有リンクがありません",
		missingDesc:             "このページを開くには有効な作品ギャラリーリンクが必要です。共有元から受け取った完全な URL を開いてください。",
		invalidHeading:          "無効な共有リンクです",
		invalidDesc:             "この作品ギャラリーリンクは形式が正しくないか無効です。URL を確認するか、共有元へ新しいリンクを依頼してください。",
		expiredHeading:          "共有リンクの有効期限が切れました",
		expiredDesc:             "この作品ギャラリーリンクは有効期限を過ぎているため閲覧できません。共有元へ再発行を依頼してください。",
		challengeExpiredHeading: "認証ページの有効期限が切れました",
		challengeExpiredDesc:    "認証ページの有効期限が切れました。ページを再読み込みしてから再試行してください。",
		revokedHeading:          "共有リンクは取り消されました",
		revokedDesc:             "共有元によりこの作品ギャラリーリンクのアクセス権限が停止されました。リンクおよび既存セッションは無効です。",
		rateLimitHeading:        "試行回数が多すぎます",
		rateLimitDesc:           "短時間に検証の失敗が繰り返されました。しばらく待ってから再度お試しください。",
		sessionLimitHeading:     "共有セッション数が上限に達しました",
		sessionLimitDesc:        "現在アクティブな共有セッション数が上限に達しています。しばらくしてから再度お試しください。",
		hint:                    "引き続き作品を閲覧するには、共有元へ新しいリンクを依頼してください。",
		footer:                  "ColabOne · リモート GPU 作品ギャラリー",
	},
	"ko-KR": {
		pageTitle:               "결과물 갤러리를 사용할 수 없습니다 · ColabOne",
		badge:                   "결과물 갤러리 공유",
		missingHeading:          "유효한 공유 링크가 없습니다",
		missingDesc:             "이 페이지를 열려면 유효한 결과물 갤러리 공유 링크가 필요합니다. 호스트가 제공한 전체 링크로 다시 열어 주세요.",
		invalidHeading:          "유효하지 않은 공유 링크입니다",
		invalidDesc:             "공유 링크 형식이 올바르지 않거나 사용할 수 없습니다. 전체 주소를 확인하거나 호스트에게 새 링크를 요청해 주세요.",
		expiredHeading:          "공유 링크가 만료되었습니다",
		expiredDesc:             "이 결과물 갤러리 링크는 유효 기간이 지나 더 이상 열람할 수 없습니다. 호스트에게 새 링크 발급을 요청해 주세요.",
		challengeExpiredHeading: "인증 페이지가 만료되었습니다",
		challengeExpiredDesc:    "인증 페이지가 만료되었습니다. 페이지를 새로고침한 후 다시 시도해 주세요.",
		revokedHeading:          "공유 링크가 취소되었습니다",
		revokedDesc:             "호스트가 이 결과물 갤러리 링크의 접근 권한을 중지했습니다. 링크와 기존 세션이 모두 만료되었습니다.",
		rateLimitHeading:        "시도 횟수가 너무 많습니다",
		rateLimitDesc:           "짧은 시간 동안 인증 실패가 너무 많았습니다. 잠시 후 다시 시도해 주세요.",
		sessionLimitHeading:     "현재 갤러리 접속 인원이 한도에 도달했습니다",
		sessionLimitDesc:        "현재 활성 공유 세션 수가 한도에 도달했습니다. 잠시 후 다시 시도해 주세요.",
		hint:                    "작품을 계속 보려면 호스트에게 새 공유 링크를 요청해 주세요.",
		footer:                  "ColabOne · 원격 GPU 결과물 갤러리",
	},
}

type colabOneOutputShareUnlockCopy struct {
	pageTitle            string
	badge                string
	heading              string
	description          string
	passwordLabel        string
	passwordPlaceholder  string
	showPassword         string
	hidePassword         string
	submitButton         string
	submittingButton     string
	emptyPasswordError   string
	invalidPasswordError string
	rateLimitError       string
	sessionLimitError    string
	expiredChallengeErr  string
	shareExpiredErr      string
	shareRevokedErr      string
	forbiddenError       string
	networkError         string
	genericError         string
	footer               string
}

var colabOneOutputShareUnlockCopyByLocale = map[string]colabOneOutputShareUnlockCopy{
	"zh-CN": {
		pageTitle:            "输入密码查看相册 · ColabOne",
		badge:                "受保护的成果物相册",
		heading:              "输入访问密码",
		description:          "此成果物相册已开启访问密码保护，请输入分享者提供的密码以查看作品。",
		passwordLabel:        "访问密码",
		passwordPlaceholder:  "请输入访问密码",
		showPassword:         "显示密码",
		hidePassword:         "隐藏密码",
		submitButton:         "解锁并查看相册",
		submittingButton:     "正在验证...",
		emptyPasswordError:   "请输入访问密码",
		invalidPasswordError: "访问密码错误，请重试",
		rateLimitError:       "验证尝试过于频繁，请稍后再试",
		sessionLimitError:    "当前图集访问人数已达上限，请稍后再试",
		expiredChallengeErr:  "验证页已过期，请刷新页面后重试",
		shareExpiredErr:      "相册分享链接已过期，请联系分享者获取新链接",
		shareRevokedErr:      "相册分享链接已被撤销，无法继续访问",
		forbiddenError:       "跨源请求已被拒绝",
		networkError:         "无法连接到 ColabOne 宿主机，请检查网络后重试",
		genericError:         "验证未通过，请重试",
		footer:               "ColabOne · 远端 GPU 成果物相册",
	},
	"en-US": {
		pageTitle:            "Unlock Output Gallery · ColabOne",
		badge:                "Protected Output Gallery",
		heading:              "Enter Access Password",
		description:          "This output gallery is password protected. Enter the password shared by the host to view the artifacts.",
		passwordLabel:        "Access Password",
		passwordPlaceholder:  "Enter access password",
		showPassword:         "Show password",
		hidePassword:         "Hide password",
		submitButton:         "Unlock Gallery",
		submittingButton:     "Verifying...",
		emptyPasswordError:   "Please enter the access password",
		invalidPasswordError: "Incorrect access password; please try again",
		rateLimitError:       "Too many attempts; please try again later",
		sessionLimitError:    "Gallery visitor limit reached; please try again later",
		expiredChallengeErr:  "Verification page expired; please refresh the page and try again",
		shareExpiredErr:      "This output gallery link has expired",
		shareRevokedErr:      "This output gallery link has been revoked",
		forbiddenError:       "Cross-origin request rejected",
		networkError:         "Unable to reach the ColabOne host. Check your connection and try again.",
		genericError:         "Verification failed. Please try again.",
		footer:               "ColabOne · Remote GPU Output Gallery",
	},
	"zh-TW": {
		pageTitle:            "輸入密碼檢視相簿 · ColabOne",
		badge:                "受保護的成果物相簿",
		heading:              "輸入存取密碼",
		description:          "此成果物相簿已開啟密碼保護，請輸入分享者提供的密碼以檢視作品。",
		passwordLabel:        "存取密碼",
		passwordPlaceholder:  "請輸入存取密碼",
		showPassword:         "顯示密碼",
		hidePassword:         "隱藏密碼",
		submitButton:         "解鎖並檢視相簿",
		submittingButton:     "正在驗證...",
		emptyPasswordError:   "請輸入存取密碼",
		invalidPasswordError: "存取密碼錯誤，請重試",
		rateLimitError:       "驗證嘗試過於頻繁，請稍後再試",
		sessionLimitError:    "目前相簿存取人數已達上限，請稍後再試",
		expiredChallengeErr:  "驗證頁已過期，請重新整理頁面後再試",
		shareExpiredErr:      "相簿分享連結已過期，請聯絡分享者取得新連結",
		shareRevokedErr:      "相簿分享連結已被撤銷，無法繼續存取",
		forbiddenError:       "跨來源請求已被拒絕",
		networkError:         "無法連線至 ColabOne 宿主機，請檢查網路後重試",
		genericError:         "驗證未通過，請重試",
		footer:               "ColabOne · 遠端 GPU 成果物相簿",
	},
	"zh-HK": {
		pageTitle:            "輸入密碼查看相簿 · ColabOne",
		badge:                "受保護的成果物相簿",
		heading:              "輸入存取密碼",
		description:          "此成果物相簿已啟用密碼保護，請輸入分享者提供的密碼以瀏覽作品。",
		passwordLabel:        "存取密碼",
		passwordPlaceholder:  "請輸入存取密碼",
		showPassword:         "顯示密碼",
		hidePassword:         "隱藏密碼",
		submitButton:         "解鎖並瀏覽相簿",
		submittingButton:     "正在驗證...",
		emptyPasswordError:   "請輸入存取密碼",
		invalidPasswordError: "存取密碼錯誤，請再試",
		rateLimitError:       "驗證嘗試過於頻繁，請稍後再試",
		sessionLimitError:    "目前相簿存取人數已達上限，請稍後再試",
		expiredChallengeErr:  "驗證頁已過期，請重新整理頁面後再試",
		shareExpiredErr:      "相簿分享連結已過期，請聯絡分享者索取新連結",
		shareRevokedErr:      "相簿分享連結已被撤銷，無法繼續存取",
		forbiddenError:       "跨來源請求已被拒絕",
		networkError:         "無法連接至 ColabOne 宿主機，請檢查網絡後再試",
		genericError:         "驗證失敗，請再試",
		footer:               "ColabOne · 遠端 GPU 成果物相簿",
	},
	"ja-JP": {
		pageTitle:            "パスワードを入力 · ColabOne",
		badge:                "パスワード保護された作品ギャラリー",
		heading:              "アクセスパスワードを入力",
		description:          "この作品ギャラリーはパスワードで保護されています。共有元から受け取ったパスワードを入力してください。",
		passwordLabel:        "アクセスパスワード",
		passwordPlaceholder:  "パスワードを入力",
		showPassword:         "パスワードを表示",
		hidePassword:         "パスワードを隠す",
		submitButton:         "ロック解除して閲覧",
		submittingButton:     "確認中...",
		emptyPasswordError:   "アクセスパスワードを入力してください",
		invalidPasswordError: "アクセスパスワードが正しくありません。再試行してください",
		rateLimitError:       "試行回数が多すぎます。しばらくしてから再試行してください",
		sessionLimitError:    "現在の閲覧セッション数が上限に達しています。しばらくしてから再試行してください",
		expiredChallengeErr:  "認証ページの有効期限が切れました。ページを再読み込みして再試行してください",
		shareExpiredErr:      "共有リンクの有効期限が切れました",
		shareRevokedErr:      "共有リンクは取り消されました",
		forbiddenError:       "クロスオリジンリクエストは拒否されました",
		networkError:         "ColabOne ホストに接続できません。ネットワークを確認してください。",
		genericError:         "認証に失敗しました。もう一度お試しください。",
		footer:               "ColabOne · リモート GPU 作品ギャラリー",
	},
	"ko-KR": {
		pageTitle:            "접근 비밀번호 입력 · ColabOne",
		badge:                "비밀번호로 보호된 결과물 갤러리",
		heading:              "접근 비밀번호 입력",
		description:          "이 결과물 갤러리는 접근 비밀번호로 보호되어 있습니다. 호스트가 공유한 비밀번호를 입력해 주세요.",
		passwordLabel:        "접근 비밀번호",
		passwordPlaceholder:  "접근 비밀번호를 입력해 주세요",
		showPassword:         "비밀번호 표시",
		hidePassword:         "비밀번호 숨기기",
		submitButton:         "잠금 해제 후 보기",
		submittingButton:     "확인 중...",
		emptyPasswordError:   "접근 비밀번호를 입력해 주세요.",
		invalidPasswordError: "접근 비밀번호가 올바르지 않습니다. 다시 시도해 주세요.",
		rateLimitError:       "시도 횟수가 너무 많습니다. 잠시 후 다시 시도해 주세요.",
		sessionLimitError:    "현재 갤러리 접속 인원이 한도에 도달했습니다. 잠시 후 다시 시도해 주세요.",
		expiredChallengeErr:  "인증 페이지가 만료되었습니다. 페이지를 새로고침한 후 다시 시도해 주세요.",
		shareExpiredErr:      "공유 링크가 만료되었습니다. 호스트에게 새 링크를 요청해 주세요.",
		shareRevokedErr:      "공유 링크가 취소되었습니다. 더 이상 접근할 수 없습니다.",
		forbiddenError:       "교차 출처 요청이 거부되었습니다.",
		networkError:         "ColabOne 호스트에 연결할 수 없습니다. 네트워크를 확인한 후 다시 시도해 주세요.",
		genericError:         "인증에 실패했습니다. 다시 시도해 주세요.",
		footer:               "ColabOne · 원격 GPU 결과물 갤러리",
	},
}

func resolveOutputShareErrorCopy(locale string) (string, colabOneOutputShareErrorCopy) {
	if copySet, ok := colabOneOutputShareErrorCopyByLocale[locale]; ok {
		return locale, copySet
	}
	return colabOneWebDefaultLocale, colabOneOutputShareErrorCopyByLocale[colabOneWebDefaultLocale]
}

func resolveOutputShareUnlockCopy(locale string) (string, colabOneOutputShareUnlockCopy) {
	if copySet, ok := colabOneOutputShareUnlockCopyByLocale[locale]; ok {
		return locale, copySet
	}
	return colabOneWebDefaultLocale, colabOneOutputShareUnlockCopyByLocale[colabOneWebDefaultLocale]
}

const colabOneOutputShareUnlockJS = `
(function () {
  var form = document.getElementById("co-gallery-unlock-form");
  var input = document.getElementById("co-gallery-password");
  var challenge = document.getElementById("co-gallery-challenge");
  var toggle = document.getElementById("co-toggle-btn");
  var submitBtn = document.getElementById("co-submit-btn");
  var alertBox = document.getElementById("co-alert");
  if (!form || !input || !challenge || !submitBtn || !alertBox) return;

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
    var password = input.value.trim();
    if (!password) {
      showError(msgEmpty);
      input.focus();
      return;
    }
    clearError();
    var redirecting = false;
    submitBtn.disabled = true;
    submitBtn.textContent = labelBusy;

    fetch("/gallery/unlock", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "Accept": "application/json"
      },
      body: JSON.stringify({ challenge: challenge.value, password: password })
    })
      .then(function (resp) {
        if (resp.ok) {
          redirecting = true;
          window.location.replace("/gallery");
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

func renderColabOneOutputShareErrorPage(locale, reason, nonce string) string {
	lang, copySet := resolveOutputShareErrorCopy(locale)

	heading := copySet.invalidHeading
	desc := copySet.invalidDesc
	switch reason {
	case outputShareErrMissing:
		heading = copySet.missingHeading
		desc = copySet.missingDesc
	case outputShareErrExpired:
		heading = copySet.expiredHeading
		desc = copySet.expiredDesc
	case outputShareErrChallengeExpired:
		heading = copySet.challengeExpiredHeading
		desc = copySet.challengeExpiredDesc
	case outputShareErrRevoked:
		heading = copySet.revokedHeading
		desc = copySet.revokedDesc
	case outputShareErrRateLimited:
		heading = copySet.rateLimitHeading
		desc = copySet.rateLimitDesc
	case outputShareErrSessionLimit:
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
	b.WriteString("</style>\n</head>\n<body>\n  <main class=\"co-shell\">\n    <section class=\"co-card\" data-gallery-error=\"")
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

func renderColabOneOutputShareUnlockPage(locale, challenge, errCode, nonce string) string {
	lang, copySet := resolveOutputShareUnlockCopy(locale)

	initialErr := ""
	switch errCode {
	case "invalid_password":
		initialErr = copySet.invalidPasswordError
	case "empty_password":
		initialErr = copySet.emptyPasswordError
	case "rate_limited":
		initialErr = copySet.rateLimitError
	case "expired_challenge":
		initialErr = copySet.expiredChallengeErr
	}
	alertClass := "co-alert is-hidden"
	if initialErr != "" {
		alertClass = "co-alert"
	}

	iconURI := colabOneWebIconDataURI()
	escNonce := html.EscapeString(nonce)

	var b strings.Builder
	b.Grow(len(colabOneWebLoginCSS) + len(colabOneOutputShareUnlockJS) + 2*len(iconURI) + 2048)
	b.WriteString("<!doctype html>\n<html lang=\"")
	b.WriteString(html.EscapeString(lang))
	b.WriteString("\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width,initial-scale=1,viewport-fit=cover\">\n<meta name=\"color-scheme\" content=\"light dark\">\n<meta name=\"robots\" content=\"noindex,nofollow\">\n<title>")
	b.WriteString(html.EscapeString(copySet.pageTitle))
	b.WriteString("</title>\n<link rel=\"icon\" type=\"image/svg+xml\" href=\"")
	b.WriteString(iconURI)
	b.WriteString("\">\n<link rel=\"alternate icon\" type=\"image/x-icon\" href=\"/favicon.ico\">\n<link rel=\"apple-touch-icon\" sizes=\"180x180\" href=\"/apple-touch-icon.png\">\n<style nonce=\"")
	b.WriteString(escNonce)
	b.WriteString("\">")
	b.WriteString(colabOneWebLoginCSS)
	b.WriteString("</style>\n</head>\n<body>\n  <main class=\"co-shell\">\n    <section class=\"co-card\" data-gallery-unlock=\"password\">\n      <div class=\"co-brand\">\n        <img class=\"co-brand-logo\" src=\"")
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
	b.WriteString("</div>\n      <form id=\"co-gallery-unlock-form\" class=\"co-form\" method=\"post\" action=\"")
	b.WriteString(OutputShareUnlockRoutePath)
	b.WriteString("\" autocomplete=\"off\" data-err-empty=\"")
	b.WriteString(html.EscapeString(copySet.emptyPasswordError))
	b.WriteString("\" data-err-net=\"")
	b.WriteString(html.EscapeString(copySet.networkError))
	b.WriteString("\" data-err-generic=\"")
	b.WriteString(html.EscapeString(copySet.genericError))
	b.WriteString("\">\n        <input id=\"co-gallery-challenge\" type=\"hidden\" name=\"challenge\" value=\"")
	b.WriteString(html.EscapeString(challenge))
	b.WriteString("\">\n        <div>\n          <label class=\"co-label\" for=\"co-gallery-password\">")
	b.WriteString(html.EscapeString(copySet.passwordLabel))
	b.WriteString("</label>\n          <div class=\"co-input-wrap\">\n            <input id=\"co-gallery-password\" class=\"co-input\" type=\"password\" name=\"password\" autocomplete=\"current-password\" placeholder=\"")
	b.WriteString(html.EscapeString(copySet.passwordPlaceholder))
	b.WriteString("\" required autofocus>\n            <button id=\"co-toggle-btn\" class=\"co-eye-btn\" type=\"button\" aria-pressed=\"false\" aria-label=\"")
	b.WriteString(html.EscapeString(copySet.showPassword))
	b.WriteString("\" data-label-show=\"")
	b.WriteString(html.EscapeString(copySet.showPassword))
	b.WriteString("\" data-label-hide=\"")
	b.WriteString(html.EscapeString(copySet.hidePassword))
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
	b.WriteString(colabOneOutputShareUnlockJS)
	b.WriteString("</script>\n</body>\n</html>")

	return b.String()
}
