package notifier

import (
	"fmt"
	"strings"
	"time"
)

type colabOneLocaleMessages struct {
	StatusSuccess         string
	StatusWarning         string
	StatusError           string
	StatusInfo            string
	ActionHintLabel       string
	PreviewCaption        string
	EmailFooter           string
	EmailSubjectPrefix    string
	TestTitle             string
	TestSubtitle          string
	TestBody              string
	TestFieldAppLabel     string
	TestFieldChannelLabel string
	TestFieldStatusLabel  string
	TestFieldStatusValue  string
	TestFieldTimeLabel    string
	TestActionHint        string
	ChannelNameSystem     string
	ChannelNameTelegram   string
	ChannelNameBark       string
	ChannelNameEmail      string
}

var colabOneLocaleCatalog = map[string]colabOneLocaleMessages{
	"zh-CN": {
		StatusSuccess:         "已完成",
		StatusWarning:         "提醒",
		StatusError:           "异常",
		StatusInfo:            "通知",
		ActionHintLabel:       "建议操作",
		PreviewCaption:        "预览图像",
		EmailFooter:           "此邮件由 ColabOne 通知中心自动发送，可在客户端「设置 - 通知中心」随时调整推送渠道与触发规则。",
		EmailSubjectPrefix:    "ColabOne",
		TestTitle:             "通知渠道连接测试成功",
		TestSubtitle:          "ColabOne 统一通知引擎已就绪",
		TestBody:              "您已成功打通该通知渠道。后续当 GPU 算力分配完成、作品生成就绪、模型下载或加载结束，以及出现异常中断时，ColabOne 将第一时间向您推送状态与预览图。",
		TestFieldAppLabel:     "应用",
		TestFieldChannelLabel: "测试渠道",
		TestFieldStatusLabel:  "连通状态",
		TestFieldStatusValue:  "正常可用",
		TestFieldTimeLabel:    "发送时间",
		TestActionHint:        "无需额外操作。您可随时在 ColabOne 设置中按需开关各类事件或启用仅后台推送。",
		ChannelNameSystem:     "系统桌面通知",
		ChannelNameTelegram:   "Telegram 机器人",
		ChannelNameBark:       "Bark 推送",
		ChannelNameEmail:      "邮件通知",
	},
	"zh-TW": {
		StatusSuccess:         "已完成",
		StatusWarning:         "提醒",
		StatusError:           "異常",
		StatusInfo:            "通知",
		ActionHintLabel:       "建議操作",
		PreviewCaption:        "預覽影像",
		EmailFooter:           "此郵件由 ColabOne 通知中心自動發送，可於用戶端「設定 - 通知中心」隨時調整推送管道與觸發規則。",
		EmailSubjectPrefix:    "ColabOne",
		TestTitle:             "通知管道連線測試成功",
		TestSubtitle:          "ColabOne 統一通知引擎已就緒",
		TestBody:              "您已成功連接此通知管道。後續當 GPU 算力分配完成、作品生成就緒、模型下載或載入結束，或發生異常中斷時，ColabOne 將即時推送狀態與預覽圖。",
		TestFieldAppLabel:     "應用程式",
		TestFieldChannelLabel: "測試管道",
		TestFieldStatusLabel:  "連線狀態",
		TestFieldStatusValue:  "正常可用",
		TestFieldTimeLabel:    "發送時間",
		TestActionHint:        "無需額外操作。您可隨時在 ColabOne 設定中調整通知分類或僅於背景推送。",
		ChannelNameSystem:     "系統桌面通知",
		ChannelNameTelegram:   "Telegram 機器人",
		ChannelNameBark:       "Bark 推播",
		ChannelNameEmail:      "電子郵件通知",
	},
	"zh-HK": {
		StatusSuccess:         "已完成",
		StatusWarning:         "提醒",
		StatusError:           "異常",
		StatusInfo:            "通知",
		ActionHintLabel:       "建議操作",
		PreviewCaption:        "預覽圖片",
		EmailFooter:           "此電郵由 ColabOne 通知中心自動發出，可於客戶端「設定 - 通知中心」隨時更改推送渠道與觸發規則。",
		EmailSubjectPrefix:    "ColabOne",
		TestTitle:             "通知渠道連線測試成功",
		TestSubtitle:          "ColabOne 統一通知引擎已就緒",
		TestBody:              "您已成功接通此通知渠道。日後當 GPU 算力分配完成、作品生成就緒、模型下載或載入完成，或出現異常中斷時，ColabOne 會即時推送狀態與預覽圖。",
		TestFieldAppLabel:     "應用程式",
		TestFieldChannelLabel: "測試渠道",
		TestFieldStatusLabel:  "連線狀態",
		TestFieldStatusValue:  "正常可用",
		TestFieldTimeLabel:    "發送時間",
		TestActionHint:        "無需額外操作。您可隨時於 ColabOne 設定內按需要開關通知分類或設定僅背景推送。",
		ChannelNameSystem:     "系統桌面通知",
		ChannelNameTelegram:   "Telegram 機械人",
		ChannelNameBark:       "Bark 推送",
		ChannelNameEmail:      "電郵通知",
	},
	"en-US": {
		StatusSuccess:         "Completed",
		StatusWarning:         "Warning",
		StatusError:           "Error",
		StatusInfo:            "Notice",
		ActionHintLabel:       "Recommended Action",
		PreviewCaption:        "Preview",
		EmailFooter:           "Sent automatically by ColabOne Notification Center. Manage channels and triggers anytime in Settings.",
		EmailSubjectPrefix:    "ColabOne",
		TestTitle:             "Notification Channel Connected",
		TestSubtitle:          "ColabOne unified notification engine is ready",
		TestBody:              "Your notification channel is verified and active. ColabOne will alert you with status details and preview images when GPU runtimes are ready, outputs finish, models complete loading, or an error needs attention.",
		TestFieldAppLabel:     "Application",
		TestFieldChannelLabel: "Channel",
		TestFieldStatusLabel:  "Delivery Status",
		TestFieldStatusValue:  "Operational",
		TestFieldTimeLabel:    "Timestamp",
		TestActionHint:        "No further action needed. You can adjust category toggles or background-only delivery anytime in ColabOne Settings.",
		ChannelNameSystem:     "System Notification",
		ChannelNameTelegram:   "Telegram Bot",
		ChannelNameBark:       "Bark Push",
		ChannelNameEmail:      "Email",
	},
	"ja-JP": {
		StatusSuccess:         "完了",
		StatusWarning:         "注意",
		StatusError:           "エラー",
		StatusInfo:            "お知らせ",
		ActionHintLabel:       "推奨アクション",
		PreviewCaption:        "プレビュー画像",
		EmailFooter:           "このメールは ColabOne 通知センターから自動送信されました。設定画面からいつでも通知先と条件を変更できます。",
		EmailSubjectPrefix:    "ColabOne",
		TestTitle:             "通知チャネルの接続テストに成功しました",
		TestSubtitle:          "ColabOne 統合通知エンジンの準備が完了しました",
		TestBody:              "この通知チャネルは正常に動作しています。今後、GPU の割り当て完了、作品の生成完了、モデルのダウンロードや読み込み完了、エラー発生時にプレビュー付きでお知らせします。",
		TestFieldAppLabel:     "アプリ",
		TestFieldChannelLabel: "テスト対象",
		TestFieldStatusLabel:  "接続状態",
		TestFieldStatusValue:  "正常",
		TestFieldTimeLabel:    "送信時刻",
		TestActionHint:        "追加の操作は不要です。ColabOne の設定画面からカテゴリ別の通知やバックグラウンド時のみの通知を切り替えられます。",
		ChannelNameSystem:     "デスクトップ通知",
		ChannelNameTelegram:   "Telegram Bot",
		ChannelNameBark:       "Bark プッシュ",
		ChannelNameEmail:      "メール通知",
	},
	"ko-KR": {
		StatusSuccess:         "완료",
		StatusWarning:         "주의",
		StatusError:           "오류",
		StatusInfo:            "알림",
		ActionHintLabel:       "권장 조치",
		PreviewCaption:        "미리보기 이미지",
		EmailFooter:           "이 메일은 ColabOne 알림 센터에서 자동으로 발송되었습니다. 앱 설정에서 언제든 알림 채널과 규칙을 변경할 수 있습니다.",
		EmailSubjectPrefix:    "ColabOne",
		TestTitle:             "알림 채널 연결 테스트 성공",
		TestSubtitle:          "ColabOne 통합 알림 엔진이 준비되었습니다",
		TestBody:              "선택한 알림 채널이 정상적으로 연결되었습니다. 앞으로 GPU 할당 완료, 결과물 생성 완료, 모델 다운로드 및 로드 완료, 오류 발생 시 상태와 미리보기 이미지를 바로 받아볼 수 있습니다.",
		TestFieldAppLabel:     "애플리케이션",
		TestFieldChannelLabel: "테스트 채널",
		TestFieldStatusLabel:  "연결 상태",
		TestFieldStatusValue:  "정상 작동",
		TestFieldTimeLabel:    "발송 시각",
		TestActionHint:        "추가 조치가 필요하지 않습니다. ColabOne 설정에서 카테고리별 알림이나 백그라운드 전용 알림을 언제든 변경할 수 있습니다.",
		ChannelNameSystem:     "시스템 데스크톱 알림",
		ChannelNameTelegram:   "Telegram 봇",
		ChannelNameBark:       "Bark 푸시",
		ChannelNameEmail:      "이메일 알림",
	},
}

// NormalizeLocale normalizes arbitrary locale tags into one of ColabOne's 6
// supported locales (zh-CN, zh-TW, zh-HK, en-US, ja-JP, ko-KR), defaulting to zh-CN.
func NormalizeLocale(raw string) string {
	clean := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(raw, "_", "-")))
	if clean == "" {
		return "zh-CN"
	}
	switch {
	case clean == "zh-tw" || clean == "zh-hant-tw" || clean == "zh-hant":
		return "zh-TW"
	case clean == "zh-hk" || clean == "zh-mo" || clean == "zh-hant-hk" || clean == "zh-hant-mo":
		return "zh-HK"
	case clean == "zh" || strings.HasPrefix(clean, "zh-cn") || strings.HasPrefix(clean, "zh-sg") || strings.HasPrefix(clean, "zh-hans"):
		return "zh-CN"
	case clean == "en" || strings.HasPrefix(clean, "en-"):
		return "en-US"
	case clean == "ja" || strings.HasPrefix(clean, "ja-"):
		return "ja-JP"
	case clean == "ko" || strings.HasPrefix(clean, "ko-"):
		return "ko-KR"
	default:
		return "zh-CN"
	}
}

func colabOneMessagesForLocale(locale string) colabOneLocaleMessages {
	if msg, ok := colabOneLocaleCatalog[NormalizeLocale(locale)]; ok {
		return msg
	}
	return colabOneLocaleCatalog["zh-CN"]
}

func colabOneSeverityLabel(severity ColabOneNotificationSeverity, locale string) string {
	m := colabOneMessagesForLocale(locale)
	switch severity {
	case SeveritySuccess:
		return m.StatusSuccess
	case SeverityWarning:
		return m.StatusWarning
	case SeverityError:
		return m.StatusError
	default:
		return m.StatusInfo
	}
}

func colabOneChannelDisplayName(channelID, locale string) string {
	m := colabOneMessagesForLocale(locale)
	switch strings.ToLower(strings.TrimSpace(channelID)) {
	case ChannelSystem:
		return m.ChannelNameSystem
	case ChannelTelegram:
		return m.ChannelNameTelegram
	case ChannelBark:
		return m.ChannelNameBark
	case ChannelEmail:
		return m.ChannelNameEmail
	default:
		return channelID
	}
}

// BuildTestNotificationEvent builds a rich localized test event including a
// generated preview card PNG so users can verify end-to-end visual delivery.
func BuildTestNotificationEvent(channelID, locale string, now time.Time) ColabOneNotificationEvent {
	normLocale := NormalizeLocale(locale)
	msg := colabOneMessagesForLocale(normLocale)
	if now.IsZero() {
		now = time.Now()
	}
	chName := colabOneChannelDisplayName(channelID, normLocale)
	timeLabel := now.Format("2006-01-02 15:04:05")
	return ColabOneNotificationEvent{
		ID:       fmt.Sprintf("test-%s-%d", strings.ToLower(strings.TrimSpace(channelID)), now.UnixNano()),
		Kind:     EventTest,
		Category: CategoryAlert,
		Severity: SeveritySuccess,
		Title:    msg.TestTitle,
		Subtitle: msg.TestSubtitle,
		Body:     msg.TestBody,
		Fields: []ColabOneNotificationField{
			{Label: msg.TestFieldAppLabel, Value: "ColabOne"},
			{Label: msg.TestFieldChannelLabel, Value: chName},
			{Label: msg.TestFieldStatusLabel, Value: msg.TestFieldStatusValue},
			{Label: msg.TestFieldTimeLabel, Value: timeLabel},
		},
		ActionHint:    msg.TestActionHint,
		ImageBytes:    BuildTestPreviewCardPNG(),
		ImageMime:     "image/png",
		ImageFilename: "colabone-notification-test.png",
		Locale:        normLocale,
		OccurredAt:    now,
		IsTest:        true,
	}
}
