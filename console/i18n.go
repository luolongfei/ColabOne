package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	colabOneLocaleZhCN = "zh-CN"
	colabOneLocaleEnUS = "en-US"
	colabOneLocaleZhTW = "zh-TW"
	colabOneLocaleZhHK = "zh-HK"
	colabOneLocaleJaJP = "ja-JP"
	colabOneLocaleKoKR = "ko-KR"
)

var (
	colabOneLocaleMu sync.RWMutex
	colabOneLocale   = colabOneLocaleZhCN
)

// colabOneNormalizeLocale maps BCP-47 / POSIX locale tags onto one of the six
// supported ColabOne locales.
func colabOneNormalizeLocale(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	if idx := strings.IndexAny(s, ".@"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.ToLower(strings.ReplaceAll(s, "_", "-"))
	switch {
	case s == "zh-hk" || s == "zh-mo" || strings.HasPrefix(s, "zh-hant-hk") || strings.HasPrefix(s, "zh-hant-mo") || s == "yue" || strings.HasPrefix(s, "yue-"):
		return colabOneLocaleZhHK, true
	case s == "zh-tw" || strings.HasPrefix(s, "zh-hant"):
		return colabOneLocaleZhTW, true
	case s == "zh" || s == "zh-cn" || s == "zh-sg" || strings.HasPrefix(s, "zh-hans"):
		return colabOneLocaleZhCN, true
	case s == "ja" || strings.HasPrefix(s, "ja-"):
		return colabOneLocaleJaJP, true
	case s == "ko" || strings.HasPrefix(s, "ko-"):
		return colabOneLocaleKoKR, true
	case s == "en" || strings.HasPrefix(s, "en-"):
		return colabOneLocaleEnUS, true
	default:
		return "", false
	}
}

func colabOneConfigDir() string {
	if env := strings.TrimSpace(os.Getenv("COLABONE_CONFIG_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "windows":
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "ColabOne")
		}
		return filepath.Join(home, "AppData", "Local", "ColabOne")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "ColabOne")
	default:
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "ColabOne")
		}
		// Match bridge/controller.py colabone_config_dir() on Linux (~/.local/share/ColabOne).
		return filepath.Join(home, ".local", "share", "ColabOne")
	}
}

// colabOneResolveInitialLocale honors any explicit user-saved locale in
// settings.json first, then OS locale detection, falling back to en-US to
// match the frontend's detectColabOneSystemLocale().
func colabOneResolveInitialLocale() string {
	if dir := colabOneConfigDir(); dir != "" {
		if raw, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
			var cfg struct {
				Locale string `json:"locale"`
			}
			if json.Unmarshal(raw, &cfg) == nil {
				if norm, ok := colabOneNormalizeLocale(cfg.Locale); ok {
					return norm
				}
			}
		}
	}
	if raw := colabOneDetectOSLocale(); raw != "" {
		if norm, ok := colabOneNormalizeLocale(raw); ok {
			return norm
		}
	}
	return colabOneLocaleEnUS
}

func colabOneGetLocale() string {
	colabOneLocaleMu.RLock()
	loc := colabOneLocale
	colabOneLocaleMu.RUnlock()
	return loc
}

func colabOneSetLocaleState(raw string) (string, bool) {
	norm, ok := colabOneNormalizeLocale(raw)
	if !ok {
		return "", false
	}
	colabOneLocaleMu.Lock()
	colabOneLocale = norm
	colabOneLocaleMu.Unlock()
	return norm, true
}

var colabOneNativeCatalog = map[string]map[string]string{
	colabOneLocaleZhCN: {
		"tray.open":                  "打开 ColabOne",
		"tray.quit":                  "退出客户端（远端空闲与预算保护继续）",
		"window.logs":                "运行日志 · ColabOne",
		"window.comfyCanvas":         "ComfyUI 原生画布 · ColabOne",
		"err.unsupportedAppearance":  "不支持的外观模式",
		"err.unsupportedLocale":      "不支持的语言选项",
		"err.clipboardRead":          "未能读取剪贴板，请直接粘贴到输入框",
		"err.clipboardLimit":         "复制内容超过 1 MB",
		"err.httpsOnly":              "只允许 HTTPS 链接",
		"err.unsupportedURL":         "不支持的外部链接",
		"err.missingRuntimeFiles":    "找不到 ColabOne 运行文件，请保留完整安装目录",
		"err.missingPython":          "缺少 Python 运行时，请运行安装脚本或重新解压完整安装包",
		"err.bridgeExited":           "控制进程已退出，请重试",
		"err.bridgeTimeout":          "操作等待超时，请检查远端状态后重试",
		"err.invalidOutputID":        "输出文件编号不合法",
		"err.bridgeNotInitialized":   "控制进程未初始化",
		"err.invalidOutputMetadata":  "输出文件格式或大小不合法",
		"err.outputTransferChanged":  "输出文件传输状态发生变化",
		"err.outputTransferPartial":  "输出文件传输不完整",
		"err.outputHashMismatch":     "输出文件校验失败，请重试导出",
		"err.outputSaveFailedPrefix": "无法保存输出文件：",
		"err.canvasURLEmpty":         "画布地址不能为空",
		"err.canvasURLInvalid":       "画布地址格式无效",
		"err.canvasURLHTTPS":         "画布地址仅支持 HTTPS",
		"err.canvasURLUserInfo":      "画布地址不允许包含认证用户信息",
		"err.canvasURLHost":          "画布地址缺少有效主机名",
		"err.canvasURLLoopback":      "画布地址不允许指向本机回环地址",
		"err.canvasURLTemporary":     "画布地址不支持临时域名，请使用固定域名",
		"err.canvasURLPath":          "画布认证路径不合法",
		"err.canvasURLToken":         "画布认证地址缺少访问令牌",
		"err.canvasTarget":           "不支持的画布打开方式",
		"err.canvasResponse":         "画布地址响应无效",
	},
	colabOneLocaleEnUS: {
		"tray.open":                  "Open ColabOne",
		"tray.quit":                  "Quit ColabOne (remote idle & budget guards stay active)",
		"window.logs":                "Logs · ColabOne",
		"window.comfyCanvas":         "ComfyUI Native Canvas · ColabOne",
		"err.unsupportedAppearance":  "Unsupported appearance mode",
		"err.unsupportedLocale":      "Unsupported language selection",
		"err.clipboardRead":          "Could not read clipboard; please paste directly into the field",
		"err.clipboardLimit":         "Clipboard payload exceeds 1 MB",
		"err.httpsOnly":              "Only HTTPS links are allowed",
		"err.unsupportedURL":         "Unsupported external link",
		"err.missingRuntimeFiles":    "ColabOne runtime files not found; keep the full install folder intact",
		"err.missingPython":          "Python runtime not found; re-run the installer or extract the full package",
		"err.bridgeExited":           "Local control process exited; please try again",
		"err.bridgeTimeout":          "Operation timed out; check remote status and retry",
		"err.invalidOutputID":        "Invalid output artifact ID",
		"err.bridgeNotInitialized":   "Control process is not initialized",
		"err.invalidOutputMetadata":  "Invalid output file format or size",
		"err.outputTransferChanged":  "Output file transfer state changed unexpectedly",
		"err.outputTransferPartial":  "Output file transfer was incomplete",
		"err.outputHashMismatch":     "Output file checksum mismatch; please export again",
		"err.outputSaveFailedPrefix": "Failed to save output file: ",
		"err.canvasURLEmpty":         "Canvas URL cannot be empty",
		"err.canvasURLInvalid":       "Invalid canvas URL format",
		"err.canvasURLHTTPS":         "Canvas URL must use HTTPS",
		"err.canvasURLUserInfo":      "Canvas URL must not contain embedded credentials",
		"err.canvasURLHost":          "Canvas URL is missing a valid hostname",
		"err.canvasURLLoopback":      "Canvas URL cannot point to a loopback address",
		"err.canvasURLTemporary":     "Temporary tunnel domains are not supported; use a custom domain",
		"err.canvasURLPath":          "Invalid canvas authentication path",
		"err.canvasURLToken":         "Canvas authentication URL is missing an access token",
		"err.canvasTarget":           "Unsupported canvas open target",
		"err.canvasResponse":         "Invalid canvas URL response",
	},
	colabOneLocaleZhTW: {
		"tray.open":                  "開啟 ColabOne",
		"tray.quit":                  "結束用戶端（遠端閒置與預算保護將繼續運作）",
		"window.logs":                "執行日誌 · ColabOne",
		"window.comfyCanvas":         "ComfyUI 原生畫布 · ColabOne",
		"err.unsupportedAppearance":  "不支援的外觀模式",
		"err.unsupportedLocale":      "不支援的語言選項",
		"err.clipboardRead":          "無法讀取剪貼簿，請直接貼上至輸入欄位",
		"err.clipboardLimit":         "複製內容超過 1 MB",
		"err.httpsOnly":              "僅允許 HTTPS 連結",
		"err.unsupportedURL":         "不支援的外部連結",
		"err.missingRuntimeFiles":    "找不到 ColabOne 執行檔案，請保留完整安裝目錄",
		"err.missingPython":          "缺少 Python 執行環境，請重新執行安裝程式或解壓完整安裝包",
		"err.bridgeExited":           "本機控制程序已結束，請重試",
		"err.bridgeTimeout":          "操作逾時，請確認遠端狀態後重試",
		"err.invalidOutputID":        "輸出檔案編號無效",
		"err.bridgeNotInitialized":   "控制程序尚未初始化",
		"err.invalidOutputMetadata":  "輸出檔案格式或大小無效",
		"err.outputTransferChanged":  "輸出檔案傳輸狀態發生變更",
		"err.outputTransferPartial":  "輸出檔案傳輸不完整",
		"err.outputHashMismatch":     "輸出檔案雜湊驗證失敗，請重新匯出",
		"err.outputSaveFailedPrefix": "無法儲存輸出檔案：",
		"err.canvasURLEmpty":         "畫布網址不可為空",
		"err.canvasURLInvalid":       "畫布網址格式無效",
		"err.canvasURLHTTPS":         "畫布網址僅支援 HTTPS",
		"err.canvasURLUserInfo":      "畫布網址不可包含驗證帳號資訊",
		"err.canvasURLHost":          "畫布網址缺少有效主機名稱",
		"err.canvasURLLoopback":      "畫布網址不可指向本機迴路位址",
		"err.canvasURLTemporary":     "畫布網址不支援臨時網域，請使用固定網域",
		"err.canvasURLPath":          "畫布驗證路徑無效",
		"err.canvasURLToken":         "畫布驗證網址缺少存取權杖",
		"err.canvasTarget":           "不支援的畫布開啟方式",
		"err.canvasResponse":         "畫布網址回應無效",
	},
	colabOneLocaleZhHK: {
		"tray.open":                  "開啟 ColabOne",
		"tray.quit":                  "結束程式（遠端閒置與預算保護會繼續生效）",
		"window.logs":                "執行日誌 · ColabOne",
		"window.comfyCanvas":         "ComfyUI 原生畫布 · ColabOne",
		"err.unsupportedAppearance":  "不支援的外觀模式",
		"err.unsupportedLocale":      "不支援的語言選項",
		"err.clipboardRead":          "未能讀取剪貼簿，請直接貼上到輸入框",
		"err.clipboardLimit":         "複製內容超過 1 MB",
		"err.httpsOnly":              "只允許 HTTPS 連結",
		"err.unsupportedURL":         "不支援的外部連結",
		"err.missingRuntimeFiles":    "找不到 ColabOne 執行檔案，請保留完整安裝目錄",
		"err.missingPython":          "缺少 Python 執行環境，請重新執行安裝腳本或解壓完整安裝包",
		"err.bridgeExited":           "本地控制進程已結束，請重試",
		"err.bridgeTimeout":          "操作逾時，請檢查遠端狀態後重試",
		"err.invalidOutputID":        "輸出檔案編號無效",
		"err.bridgeNotInitialized":   "控制進程尚未初始化",
		"err.invalidOutputMetadata":  "輸出檔案格式或大小無效",
		"err.outputTransferChanged":  "輸出檔案傳輸狀態有變",
		"err.outputTransferPartial":  "輸出檔案傳輸不完整",
		"err.outputHashMismatch":     "輸出檔案校驗失敗，請重新匯出",
		"err.outputSaveFailedPrefix": "無法儲存輸出檔案：",
		"err.canvasURLEmpty":         "畫布網址不可為空",
		"err.canvasURLInvalid":       "畫布網址格式無效",
		"err.canvasURLHTTPS":         "畫布網址只支援 HTTPS",
		"err.canvasURLUserInfo":      "畫布網址不可包含認證用戶資料",
		"err.canvasURLHost":          "畫布網址缺少有效主機名稱",
		"err.canvasURLLoopback":      "畫布網址不可指向本地回環位址",
		"err.canvasURLTemporary":     "畫布網址不支援臨時域名，請使用固定域名",
		"err.canvasURLPath":          "畫布認證路徑無效",
		"err.canvasURLToken":         "畫布認證網址缺少存取令牌",
		"err.canvasTarget":           "不支援的畫布開啟方式",
		"err.canvasResponse":         "畫布網址回應無效",
	},
	colabOneLocaleJaJP: {
		"tray.open":                  "ColabOne を開く",
		"tray.quit":                  "終了（リモートのアイドル自動解放・予算保護は継続）",
		"window.logs":                "実行ログ · ColabOne",
		"window.comfyCanvas":         "ComfyUI ネイティブキャンバス · ColabOne",
		"err.unsupportedAppearance":  "サポートされていない外観モードです",
		"err.unsupportedLocale":      "サポートされていない言語設定です",
		"err.clipboardRead":          "クリップボードを読み取れませんでした。入力欄へ直接貼り付けてください",
		"err.clipboardLimit":         "コピー内容が 1 MB を超えています",
		"err.httpsOnly":              "HTTPS リンクのみ許可されています",
		"err.unsupportedURL":         "サポートされていない外部リンクです",
		"err.missingRuntimeFiles":    "ColabOne の実行ファイルが見つかりません。インストールフォルダをそのまま維持してください",
		"err.missingPython":          "Python ランタイムが見つかりません。インストーラーを再実行するかパッケージを再展開してください",
		"err.bridgeExited":           "ローカル制御プロセスが終了しました。再試行してください",
		"err.bridgeTimeout":          "操作がタイムアウトしました。リモート状態を確認して再試行してください",
		"err.invalidOutputID":        "出力ファイル ID が無効です",
		"err.bridgeNotInitialized":   "制御プロセスが初期化されていません",
		"err.invalidOutputMetadata":  "出力ファイルの形式またはサイズが無効です",
		"err.outputTransferChanged":  "出力ファイルの転送状態が変更されました",
		"err.outputTransferPartial":  "出力ファイルの転送が不完全です",
		"err.outputHashMismatch":     "出力ファイルの整合性チェックに失敗しました。再度エクスポートしてください",
		"err.outputSaveFailedPrefix": "出力ファイルを保存できませんでした: ",
		"err.canvasURLEmpty":         "キャンバス URL を空にすることはできません",
		"err.canvasURLInvalid":       "キャンバス URL の形式が無効です",
		"err.canvasURLHTTPS":         "キャンバス URL は HTTPS のみ対応しています",
		"err.canvasURLUserInfo":      "キャンバス URL に認証情報を含めることはできません",
		"err.canvasURLHost":          "キャンバス URL に有効なホスト名がありません",
		"err.canvasURLLoopback":      "キャンバス URL にループバックアドレスは指定できません",
		"err.canvasURLTemporary":     "一時ドメインは利用できません。固定ドメインを使用してください",
		"err.canvasURLPath":          "キャンバス認証パスが無効です",
		"err.canvasURLToken":         "キャンバス認証 URL にアクセストークンがありません",
		"err.canvasTarget":           "サポートされていないキャンバスの開き方です",
		"err.canvasResponse":         "キャンバス URL の応答が無効です",
	},
	colabOneLocaleKoKR: {
		"tray.open":                  "ColabOne 열기",
		"tray.quit":                  "앱 종료 (원격 유휴·예산 보호는 계속 유지)",
		"window.logs":                "실행 로그 · ColabOne",
		"window.comfyCanvas":         "ComfyUI 네이티브 캔버스 · ColabOne",
		"err.unsupportedAppearance":  "지원하지 않는 화면 테마입니다",
		"err.unsupportedLocale":      "지원하지 않는 언어 설정입니다",
		"err.clipboardRead":          "클립보드를 읽을 수 없습니다. 입력란에 직접 붙여넣어 주세요",
		"err.clipboardLimit":         "복사 내용이 1 MB를 초과합니다",
		"err.httpsOnly":              "HTTPS 링크만 허용됩니다",
		"err.unsupportedURL":         "지원하지 않는 외부 링크입니다",
		"err.missingRuntimeFiles":    "ColabOne 실행 파일을 찾을 수 없습니다. 설치 폴더를 그대로 유지해 주세요",
		"err.missingPython":          "Python 런타임을 찾을 수 없습니다. 설치 스크립트를 다시 실행하거나 전체 패키지를 다시 압축 해제해 주세요",
		"err.bridgeExited":           "로컬 제어 프로세스가 종료되었습니다. 다시 시도해 주세요",
		"err.bridgeTimeout":          "작업 시간이 초과되었습니다. 원격 상태를 확인한 뒤 다시 시도해 주세요",
		"err.invalidOutputID":        "출력 파일 ID가 올바르지 않습니다",
		"err.bridgeNotInitialized":   "제어 프로세스가 초기화되지 않았습니다",
		"err.invalidOutputMetadata":  "출력 파일 형식 또는 크기가 올바르지 않습니다",
		"err.outputTransferChanged":  "출력 파일 전송 상태가 변경되었습니다",
		"err.outputTransferPartial":  "출력 파일 전송이 완료되지 않았습니다",
		"err.outputHashMismatch":     "출력 파일 무결성 검증에 실패했습니다. 다시 내보내기 해 주세요",
		"err.outputSaveFailedPrefix": "출력 파일을 저장할 수 없습니다: ",
		"err.canvasURLEmpty":         "캔버스 URL은 비워 둘 수 없습니다",
		"err.canvasURLInvalid":       "캔버스 URL 형식이 올바르지 않습니다",
		"err.canvasURLHTTPS":         "캔버스 URL은 HTTPS만 지원합니다",
		"err.canvasURLUserInfo":      "캔버스 URL에는 인증 사용자 정보를 포함할 수 없습니다",
		"err.canvasURLHost":          "캔버스 URL에 유효한 호스트 이름이 없습니다",
		"err.canvasURLLoopback":      "캔버스 URL은 로컬 루프백 주소를 가리킬 수 없습니다",
		"err.canvasURLTemporary":     "임시 도메인은 지원하지 않습니다. 고정 도메인을 사용해 주세요",
		"err.canvasURLPath":          "캔버스 인증 경로가 올바르지 않습니다",
		"err.canvasURLToken":         "캔버스 인증 URL에 액세스 토큰이 없습니다",
		"err.canvasTarget":           "지원하지 않는 캔버스 열기 방식입니다",
		"err.canvasResponse":         "캔버스 URL 응답이 올바르지 않습니다",
	},
}

func colabOneText(locale, key string) string {
	if table, ok := colabOneNativeCatalog[locale]; ok {
		if text, exists := table[key]; exists {
			return text
		}
	}
	if text, exists := colabOneNativeCatalog[colabOneLocaleZhCN][key]; exists {
		return text
	}
	return key
}

func colabOneT(key string) string {
	return colabOneText(colabOneGetLocale(), key)
}
