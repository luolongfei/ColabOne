package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/notifier"
)

const (
	colabOneVaultNotificationSecretsKey = "notifications_secrets"
	colabOneMaxNotifyPreviewBytes       = 12 << 20 // 12 MiB cap for fetching image previews
	colabOneNotifyPreviewFetchTimeout   = 12 * time.Second
	colabOneAuthNotifyCooldown          = 15 * time.Minute
)

// ColabOneSystemNotificationHooks bridges the in-process controller to native
// desktop OS notifications, window focus detection, and default OS locale.
type ColabOneSystemNotificationHooks struct {
	Send            notifier.ColabOneSystemSender
	Supported       func() bool
	IsWindowFocused func() bool
	DefaultLocale   func() string
}

type pendingControllerNotification struct {
	event            notifier.ColabOneNotificationEvent
	previewOutputID  string
	previewFilename  string
	previewMime      string
}

type colabOneLimitedBuffer struct {
	buf   bytes.Buffer
	limit int64
	over  bool
}

func (b *colabOneLimitedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len())+int64(len(p)) > b.limit {
		b.over = true
		return 0, errors.New("preview image exceeds size limit")
	}
	return b.buf.Write(p)
}

// SetSystemNotificationHooks registers host desktop callbacks for native OS
// notifications, foreground focus checks, and OS locale fallback.
func (c *ColabOneController) SetSystemNotificationHooks(hooks ColabOneSystemNotificationHooks) {
	c.notifyHooksMu.Lock()
	c.notifyHooks = hooks
	c.notifyHooksMu.Unlock()
	c.refreshDefaultNotificationLocale()
}

func (c *ColabOneController) getNotificationHooks() ColabOneSystemNotificationHooks {
	c.notifyHooksMu.RLock()
	hooks := c.notifyHooks
	c.notifyHooksMu.RUnlock()
	return hooks
}

func (c *ColabOneController) refreshDefaultNotificationLocale() string {
	hooks := c.getNotificationHooks()
	loc := ""
	if hooks.DefaultLocale != nil {
		if fallback := strings.TrimSpace(hooks.DefaultLocale()); fallback != "" {
			loc = notifier.NormalizeLocale(fallback)
		}
	}
	c.cachedDefaultLocale.Store(loc)
	return loc
}

func (c *ColabOneController) cachedDefaultNotificationLocale() string {
	if raw := c.cachedDefaultLocale.Load(); raw != nil {
		if s, ok := raw.(string); ok && s != "" {
			return s
		}
	}
	return "zh-CN"
}

func (c *ColabOneController) notifierSystemSupported() bool {
	hooks := c.getNotificationHooks()
	if hooks.Supported != nil {
		return hooks.Supported()
	}
	return hooks.Send != nil
}

func (c *ColabOneController) isDesktopWindowFocused() bool {
	hooks := c.getNotificationHooks()
	if hooks.IsWindowFocused != nil {
		return hooks.IsWindowFocused()
	}
	return false
}

func (c *ColabOneController) sendDesktopSystemNotification(ctx context.Context, req notifier.ColabOneSystemNotificationRequest) error {
	hooks := c.getNotificationHooks()
	if hooks.Send == nil {
		return notifier.ErrSystemUnsupported
	}
	return hooks.Send(ctx, req)
}

func (c *ColabOneController) effectiveNotificationLocale() string {
	c.mu.Lock()
	loc := strings.TrimSpace(asString(c.settings["locale"]))
	c.mu.Unlock()
	if loc != "" {
		return notifier.NormalizeLocale(loc)
	}
	if fallback := c.refreshDefaultNotificationLocale(); fallback != "" {
		return fallback
	}
	return "zh-CN"
}

func (c *ColabOneController) effectiveNotificationLocaleLocked() string {
	loc := strings.TrimSpace(asString(c.settings["locale"]))
	if loc != "" {
		return notifier.NormalizeLocale(loc)
	}
	return c.cachedDefaultNotificationLocale()
}

func (c *ColabOneController) currentNotificationConfig() notifier.ColabOneNotificationConfig {
	// Maintain lock order vaultConfigMu -> mu so vault secrets and settings.json
	// public fields are always read as a consistent snapshot without torn reads.
	c.vaultConfigMu.Lock()
	var secMap map[string]any
	if c.vault != nil {
		if v, ok := c.vault.Get(colabOneVaultNotificationSecretsKey); ok {
			if m := asMap(v); m != nil {
				secMap = deepCopyMap(m)
			}
		}
	}
	c.mu.Lock()
	pubMap := deepCopyMap(asMap(c.settings["notifications"]))
	c.mu.Unlock()
	c.vaultConfigMu.Unlock()

	return notifier.LoadConfigFromMaps(pubMap, secMap)
}

func (c *ColabOneController) notificationPublicView() map[string]any {
	return c.currentNotificationConfig().PublicView(c.notifierSystemSupported())
}

// SaveNotificationSettings validates and persists public notification preferences
// into settings.json while isolating secret tokens/keys/passwords inside VaultStore.
func (c *ColabOneController) SaveNotificationSettings(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if payload == nil {
		payload = map[string]any{}
	}

	c.vaultConfigMu.Lock()
	vaultLocked := true
	unlockVault := func() {
		if vaultLocked {
			vaultLocked = false
			c.vaultConfigMu.Unlock()
		}
	}
	defer unlockVault()

	c.persistMu.Lock()
	persistLocked := true
	unlockPersist := func() {
		if persistLocked {
			persistLocked = false
			c.persistMu.Unlock()
		}
	}
	defer unlockPersist()

	var prevSecrets map[string]any
	prevSecretsExisted := false
	if c.vault != nil {
		if v, ok := c.vault.Get(colabOneVaultNotificationSecretsKey); ok {
			prevSecretsExisted = true
			if m := asMap(v); m != nil {
				prevSecrets = deepCopyMap(m)
			}
		}
	}

	c.mu.Lock()
	prevPublic := deepCopyMap(asMap(c.settings["notifications"]))
	c.mu.Unlock()

	curCfg := notifier.LoadConfigFromMaps(prevPublic, prevSecrets)
	nextCfg, err := notifier.ApplyPatch(curCfg, payload)
	if err != nil {
		return nil, err
	}

	nextSecrets := nextCfg.SecretsMap()
	if c.vault != nil {
		if len(nextSecrets) == 0 {
			if err := c.vault.Delete(colabOneVaultNotificationSecretsKey); err != nil {
				return nil, fmt.Errorf("清除通知凭据失败：%w", err)
			}
		} else {
			if err := c.vault.Set(colabOneVaultNotificationSecretsKey, nextSecrets); err != nil {
				return nil, fmt.Errorf("保存通知凭据失败：%w", err)
			}
		}
	}

	nextPublic := nextCfg.PublicSettingsMap()
	c.mu.Lock()
	c.settings["notifications"] = nextPublic
	snapshotSettings := deepCopyMap(c.settings)
	c.mu.Unlock()

	if saveErr := AtomicJSON(filepath.Join(c.directory, "settings.json"), snapshotSettings); saveErr != nil {
		// Roll back in-memory settings and vault on disk failure.
		c.mu.Lock()
		if prevPublic != nil {
			c.settings["notifications"] = prevPublic
		} else {
			delete(c.settings, "notifications")
		}
		c.mu.Unlock()
		if c.vault != nil {
			if prevSecretsExisted && len(prevSecrets) > 0 {
				_ = c.vault.Set(colabOneVaultNotificationSecretsKey, prevSecrets)
			} else {
				_ = c.vault.Delete(colabOneVaultNotificationSecretsKey)
			}
		}
		return nil, saveErr
	}

	// Release persistMu and vaultConfigMu before logging or querying host hooks.
	unlockPersist()
	unlockVault()

	enabledNames := make([]string, 0, 4)
	if nextCfg.System.Enabled {
		enabledNames = append(enabledNames, "系统通知")
	}
	if nextCfg.Telegram.Enabled {
		enabledNames = append(enabledNames, "Telegram")
	}
	if nextCfg.Bark.Enabled {
		enabledNames = append(enabledNames, "Bark")
	}
	if nextCfg.Email.Enabled {
		enabledNames = append(enabledNames, "邮件")
	}
	summary := "已关闭所有通知渠道"
	if len(enabledNames) > 0 {
		summary = "已启用通知渠道：" + strings.Join(enabledNames, "、")
	}
	c.log("已保存通知设置 · "+summary, "设置", "info")

	return map[string]any{
		"ok":                    true,
		"notification_settings": nextCfg.PublicView(c.notifierSystemSupported()),
	}, nil
}

// NotificationSecret returns a single saved notification secret for desktop host reveal.
func (c *ColabOneController) NotificationSecret(field string) (map[string]any, error) {
	clean := strings.TrimSpace(field)
	switch clean {
	case notifier.SecretTelegramBotToken,
		notifier.SecretTelegramProxyURL,
		notifier.SecretBarkDeviceKey,
		notifier.SecretEmailPassword:
	default:
		return nil, errors.New("不支持的通知凭据字段")
	}
	val := ""
	if c.vault != nil {
		c.vaultConfigMu.Lock()
		if v, ok := c.vault.Get(colabOneVaultNotificationSecretsKey); ok {
			if m := asMap(v); m != nil {
				val = asString(m[clean])
			}
		}
		c.vaultConfigMu.Unlock()
	}
	return map[string]any{"value": val}, nil
}

// TestNotificationChannel synchronously delivers a test notification to channelID.
func (c *ColabOneController) TestNotificationChannel(ctx context.Context, channelID string) (map[string]any, error) {
	ch := strings.ToLower(strings.TrimSpace(channelID))
	switch ch {
	case notifier.ChannelSystem, notifier.ChannelTelegram, notifier.ChannelBark, notifier.ChannelEmail:
	default:
		return nil, notifier.ErrUnknownChannel
	}
	if c.notifier == nil {
		return nil, errors.New("通知服务未初始化")
	}
	locale := c.effectiveNotificationLocale()
	if err := c.notifier.TestChannel(ctx, ch, locale); err != nil {
		return nil, err
	}
	c.log("通知渠道测试发送成功："+ch, "通知", "info")
	return map[string]any{
		"ok":      true,
		"channel": ch,
		"message": "测试通知已发送",
	}, nil
}

func (c *ColabOneController) emitNotificationEvent(event notifier.ColabOneNotificationEvent) {
	if c.notifier == nil || c.stop.IsSet() {
		return
	}
	if strings.TrimSpace(event.Locale) == "" {
		event.Locale = c.effectiveNotificationLocale()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	c.notifier.Notify(event)
}

func (c *ColabOneController) emitPendingNotifications(items []pendingControllerNotification) {
	if c.notifier == nil || len(items) == 0 || c.stop.IsSet() {
		return
	}
	locale := c.effectiveNotificationLocale()
	for _, item := range items {
		ev := item.event
		if strings.TrimSpace(ev.Locale) == "" {
			ev.Locale = locale
		}
		if ev.OccurredAt.IsZero() {
			ev.OccurredAt = time.Now()
		}
		if item.previewOutputID != "" && c.shouldFetchNotificationPreview() {
			outputID := item.previewOutputID
			filename := item.previewFilename
			mimeType := item.previewMime
			c.spawn(func() {
				fetchCtx, cancel := context.WithTimeout(c.rootCtx, colabOneNotifyPreviewFetchTimeout)
				defer cancel()
				imgBytes, resolvedMime, resolvedName := c.fetchOutputPreviewForNotification(fetchCtx, outputID, filename, mimeType)
				if len(imgBytes) > 0 {
					ev.ImageBytes = imgBytes
					ev.ImageMime = resolvedMime
					ev.ImageFilename = resolvedName
				}
				c.emitNotificationEvent(ev)
			})
			continue
		}
		c.emitNotificationEvent(ev)
	}
}

func (c *ColabOneController) shouldFetchNotificationPreview() bool {
	cfg := c.currentNotificationConfig()
	if !cfg.NotifyTask {
		return false
	}
	if cfg.System.Enabled && c.notifierSystemSupported() {
		return true
	}
	if cfg.Telegram.Enabled && cfg.TelegramConfigured() && cfg.Telegram.SendPreviewImage {
		return true
	}
	if cfg.Email.Enabled && cfg.EmailConfigured() && cfg.Email.EmbedPreviewImage {
		return true
	}
	return false
}

func (c *ColabOneController) fetchOutputPreviewForNotification(
	ctx context.Context,
	outputID, fallbackFilename, fallbackMime string,
) ([]byte, string, string) {
	cleanID := strings.ToLower(strings.TrimSpace(outputID))
	if !reHex32ID.MatchString(cleanID) {
		return nil, "", ""
	}
	buf := &colabOneLimitedBuffer{limit: colabOneMaxNotifyPreviewBytes}
	// Allow local SSD cache and Google Drive fallback so previews succeed even if
	// ComfyUI workflow auto-release tears down the VM immediately after Drive sync.
	meta, err := c.StreamOutputMediaToWriter(ctx, cleanID, buf, ColabOneOutputMediaOptions{RuntimeOnly: false})
	if err != nil || buf.over || buf.buf.Len() == 0 {
		return nil, "", ""
	}
	mimeType := strings.TrimSpace(fallbackMime)
	filename := strings.TrimSpace(fallbackFilename)
	if meta != nil {
		if strings.TrimSpace(meta.Mime) != "" {
			mimeType = strings.TrimSpace(meta.Mime)
		}
		if filename == "" && strings.TrimSpace(meta.Filename) != "" {
			filename = strings.TrimSpace(meta.Filename)
		}
	}
	return buf.buf.Bytes(), mimeType, filename
}

// ---------------------------------------------------------------------------
// Localized notification copy & event builders
// ---------------------------------------------------------------------------

func isNotificationImageOutput(item map[string]any) bool {
	if item == nil {
		return false
	}
	mimeType := strings.ToLower(strings.TrimSpace(asString(item["mime"])))
	if strings.HasPrefix(mimeType, "image/") && !strings.Contains(mimeType, "svg") {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(asString(item["extension"])), "."))
	switch ext {
	case "png", "jpg", "jpeg", "webp", "gif":
		return true
	}
	return false
}

func formatLocationSummaryForNotification(loc map[string]any) string {
	if len(loc) == 0 {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, key := range []string{"city", "region", "country_name", "country", "zone"} {
		v := strings.TrimSpace(asString(loc[key]))
		if v == "" {
			continue
		}
		dup := false
		for _, existing := range parts {
			if strings.EqualFold(existing, v) {
				dup = true
				break
			}
		}
		if !dup {
			parts = append(parts, v)
		}
		if len(parts) >= 2 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func notifyLocalePick(locale string, zhCN, zhTW, zhHK, enUS, jaJP, koKR string) string {
	switch notifier.NormalizeLocale(locale) {
	case "zh-TW":
		return zhTW
	case "zh-HK":
		return zhHK
	case "en-US":
		return enUS
	case "ja-JP":
		return jaJP
	case "ko-KR":
		return koKR
	default:
		return zhCN
	}
}

func formatDurationForNotification(loc string, sec float64) string {
	norm := notifier.NormalizeLocale(loc)
	if math.IsNaN(sec) || math.IsInf(sec, 0) || sec <= 0 {
		switch norm {
		case "en-US":
			return "0s"
		case "ja-JP":
			return "0 秒"
		case "ko-KR":
			return "0초"
		default:
			return "0 秒"
		}
	}
	if sec < 60 {
		roundedTenths := math.Round(sec*10) / 10
		if roundedTenths < 60 {
			switch norm {
			case "en-US":
				if roundedTenths >= 10 {
					return fmt.Sprintf("%ds", int64(math.Round(sec)))
				}
				return fmt.Sprintf("%.1fs", roundedTenths)
			case "ja-JP":
				return fmt.Sprintf("%.1f 秒", roundedTenths)
			case "ko-KR":
				return fmt.Sprintf("%.1f초", roundedTenths)
			default:
				return fmt.Sprintf("%.1f 秒", roundedTenths)
			}
		}
	}
	total := int64(math.Round(sec))
	hours := total / 3600
	mins := (total % 3600) / 60
	secs := total % 60
	switch norm {
	case "en-US":
		if hours == 0 {
			if secs == 0 {
				return fmt.Sprintf("%dm", mins)
			}
			return fmt.Sprintf("%dm %ds", mins, secs)
		}
		if mins == 0 && secs == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		if secs == 0 {
			return fmt.Sprintf("%dh %dm", hours, mins)
		}
		return fmt.Sprintf("%dh %dm %ds", hours, mins, secs)
	case "zh-TW", "zh-HK":
		if hours == 0 {
			if secs == 0 {
				return fmt.Sprintf("%d 分鐘", mins)
			}
			return fmt.Sprintf("%d 分 %d 秒", mins, secs)
		}
		if mins == 0 && secs == 0 {
			return fmt.Sprintf("%d 小時", hours)
		}
		if secs == 0 {
			return fmt.Sprintf("%d 小時 %d 分", hours, mins)
		}
		return fmt.Sprintf("%d 小時 %d 分 %d 秒", hours, mins, secs)
	case "ja-JP":
		if hours == 0 {
			if secs == 0 {
				return fmt.Sprintf("%d 分", mins)
			}
			return fmt.Sprintf("%d 分 %d 秒", mins, secs)
		}
		if mins == 0 && secs == 0 {
			return fmt.Sprintf("%d 時間", hours)
		}
		if secs == 0 {
			return fmt.Sprintf("%d 時間 %d 分", hours, mins)
		}
		return fmt.Sprintf("%d 時間 %d 分 %d 秒", hours, mins, secs)
	case "ko-KR":
		if hours == 0 {
			if secs == 0 {
				return fmt.Sprintf("%d분", mins)
			}
			return fmt.Sprintf("%d분 %d초", mins, secs)
		}
		if mins == 0 && secs == 0 {
			return fmt.Sprintf("%d시간", hours)
		}
		if secs == 0 {
			return fmt.Sprintf("%d시간 %d분", hours, mins)
		}
		return fmt.Sprintf("%d시간 %d분 %d초", hours, mins, secs)
	default:
		return formatColabOneDurationZh(sec, true)
	}
}

func isRuntimeWorkflowAutoReleasing(rt map[string]any) bool {
	if rt == nil {
		return false
	}
	comfy := asMap(rt["comfyui"])
	if comfy == nil {
		return false
	}
	ar := asMap(comfy["auto_release"])
	if ar == nil {
		return false
	}
	st := asString(ar["state"])
	return st == "releasing" || st == "released"
}

func (c *ColabOneController) notifyGPUAllocated(gpu, endpoint string, location map[string]any, attempts int, elapsed time.Duration, driveConnected bool) {
	loc := c.effectiveNotificationLocale()
	gpuLabel := strings.ToUpper(strings.TrimSpace(gpu))
	if gpuLabel == "" {
		gpuLabel = "GPU"
	}
	elapsedStr := formatDurationForNotification(loc, elapsed.Seconds())
	locText := formatLocationSummaryForNotification(location)

	title := fmt.Sprintf(
		notifyLocalePick(loc,
			"%s GPU 分配成功并已就绪",
			"%s GPU 分配成功並已就緒",
			"%s GPU 分配成功並已就緒",
			"%s GPU Ready",
			"%s GPU の割り当てが完了しました",
			"%s GPU 할당 완료",
		),
		gpuLabel,
	)
	subtitle := notifyLocalePick(loc,
		"远端算力实例已启动完成，可以开始使用",
		"遠端算力實例已啟動完成，可以開始使用",
		"遠端算力實例已啟動完成，可以開始使用",
		"Your remote GPU runtime is online and ready",
		"リモート GPU ランタイムの準備が整いました",
		"원격 GPU 런타임이 준비되었습니다",
	)
	body := fmt.Sprintf(
		notifyLocalePick(loc,
			"已为您成功分配并连接 %s GPU 实例（耗时 %s），可返回 ColabOne 开始加载模型或运行工作流。",
			"已為您成功分配並連線 %s GPU 實例（耗時 %s），可返回 ColabOne 開始載入模型或執行工作流。",
			"已為您成功分配並連線 %s GPU 實例（耗時 %s），可返回 ColabOne 開始載入模型或執行工作流。",
			"Successfully allocated and connected a %s GPU runtime in %s. You can return to ColabOne to load models or run workflows.",
			"%s GPU インスタンスを %s で割り当て・接続しました。ColabOne に戻ってモデル読込やワークフローを実行できます。",
			"%s GPU 인스턴스를 %s 만에 할당하고 연결했습니다. ColabOne으로 돌아가 모델을 불러오거나 워크플로를 실행할 수 있습니다.",
		),
		gpuLabel, elapsedStr,
	)

	fields := []notifier.ColabOneNotificationField{
		{
			Label: notifyLocalePick(loc, "GPU 型号", "GPU 型號", "GPU 型號", "GPU Model", "GPU モデル", "GPU 모델"),
			Value: gpuLabel,
		},
		{
			Label: notifyLocalePick(loc, "分配耗时", "分配耗時", "分配耗時", "Elapsed", "所要時間", "소요 시간"),
			Value: elapsedStr,
		},
	}
	if attempts > 1 {
		fields = append(fields, notifier.ColabOneNotificationField{
			Label: notifyLocalePick(loc, "尝试次数", "嘗試次數", "嘗試次數", "Attempts", "試行回数", "시도 횟수"),
			Value: fmt.Sprintf(notifyLocalePick(loc, "第 %d 次成功", "第 %d 次成功", "第 %d 次成功", "Succeeded on attempt %d", "%d 回目で成功", "%d회차 성공"), attempts),
		})
	}
	if locText != "" {
		fields = append(fields, notifier.ColabOneNotificationField{
			Label: notifyLocalePick(loc, "节点区域", "節點區域", "節點區域", "Region", "リージョン", "리전"),
			Value: locText,
		})
	}
	if driveConnected {
		fields = append(fields, notifier.ColabOneNotificationField{
			Label: notifyLocalePick(loc, "Google 云盘", "Google 雲端硬碟", "Google 雲端硬碟", "Google Drive", "Google ドライブ", "Google 드라이브"),
			Value: notifyLocalePick(loc, "已自动挂载", "已自動掛載", "已自動掛載", "Mounted", "マウント済み", "자동 마운트됨"),
		})
	}

	c.emitNotificationEvent(notifier.ColabOneNotificationEvent{
		ID:          "gpu-allocated-" + strings.TrimSpace(endpoint),
		Kind:        notifier.EventGPUAllocated,
		Category:    notifier.CategoryGPU,
		Severity:    notifier.SeveritySuccess,
		DedupeKey:   "gpu.allocated:" + strings.TrimSpace(endpoint),
		Title:       title,
		Subtitle:    subtitle,
		Body:        body,
		Fields:      fields,
		NavigateTab: "device",
		Locale:      loc,
	})
}

func (c *ColabOneController) notifyGPUAllocateFailed(gpu string, err error, attempts int) {
	if err == nil {
		return
	}
	loc := c.effectiveNotificationLocale()
	gpuLabel := strings.ToUpper(strings.TrimSpace(gpu))
	if gpuLabel == "" {
		gpuLabel = "GPU"
	}
	reason := truncateRunes(SafeMessage(err.Error()), 260)
	lower := strings.ToLower(reason)

	actionHint := notifyLocalePick(loc,
		"请打开 ColabOne 查看详情，可尝试切换其他 GPU 型号、检查网络连接后重新启动。",
		"請開啟 ColabOne 查看詳情，可嘗試切換其他 GPU 型號或檢查網路連線後重新啟動。",
		"請開啟 ColabOne 查看詳情，可嘗試切換其他 GPU 型號或檢查網絡連線後重新啟動。",
		"Open ColabOne to review details, switch GPU tiers, or verify network connectivity and retry.",
		"ColabOne を開いて詳細を確認し、別の GPU モデルへ切り替えるかネットワークを確認して再試行してください。",
		"ColabOne에서 상세 내용을 확인한 뒤 다른 GPU 모델로 변경하거나 네트워크 상태를 점검하고 다시 시도해 주세요.",
	)
	switch {
	case strings.Contains(lower, "412") || strings.Contains(reason, "上限") || strings.Contains(lower, "too many"):
		actionHint = notifyLocalePick(loc,
			"当前 Google 账号运行中的 Colab 实例数量已达上限，请先释放闲置实例后再重新分配。",
			"目前 Google 帳號執行中的 Colab 實例數量已達上限，請先釋放閒置實例後再重新分配。",
			"目前 Google 帳戶執行中的 Colab 實例數量已達上限，請先釋放閒置實例後再重新分配。",
			"Your Google account reached the concurrent Colab runtime limit. Release an unused instance and retry.",
			"Google アカウントの同時実行上限に達しています。不要なインスタンスを解放してから再試行してください。",
			"Google 계정의 동시 실행 한도에 도달했습니다. 사용하지 않는 인스턴스를 해제한 뒤 다시 시도해 주세요.",
		)
	case isAuthLikeErrorMessage(reason):
		actionHint = notifyLocalePick(loc,
			"Google 登录授权可能已失效，请前往 ColabOne 重新完成 Google 账号登录后再试。",
			"Google 登入授權可能已失效，請前往 ColabOne 重新完成 Google 帳號登入後再試。",
			"Google 登入授權可能已失效，請前往 ColabOne 重新完成 Google 帳戶登入後再試。",
			"Your Google authorization may have expired. Please sign in again in ColabOne.",
			"Google 認証の有効期限が切れている可能性があります。ColabOne で再ログインしてください。",
			"Google 인증이 만료되었을 수 있습니다. ColabOne에서 다시 로그인해 주세요.",
		)
	case strings.Contains(reason, "代理") || strings.Contains(lower, "proxy"):
		actionHint = notifyLocalePick(loc,
			"GPU 分配代理连接异常，请在「设置」中测试或调整分配代理配置后重试。",
			"GPU 分配代理連線異常，請在「設定」中測試或調整分配代理設定後重試。",
			"GPU 分配代理連線異常，請在「設定」中測試或調整分配代理設定後重試。",
			"The allocation proxy failed. Check or test your proxy settings in ColabOne Settings.",
			"割り当てプロキシの接続に失敗しました。設定画面でプロキシ構成を確認・テストしてください。",
			"할당 프록시 연결에 실패했습니다. 설정에서 프록시 구성을 확인하거나 테스트해 주세요.",
		)
	}

	title := fmt.Sprintf(
		notifyLocalePick(loc,
			"%s GPU 分配失败（需要人工处理）",
			"%s GPU 分配失敗（需要人工處理）",
			"%s GPU 分配失敗（需要人工處理）",
			"%s GPU Allocation Failed",
			"%s GPU の割り当てに失敗しました",
			"%s GPU 할당 실패",
		),
		gpuLabel,
	)
	fields := []notifier.ColabOneNotificationField{
		{
			Label: notifyLocalePick(loc, "GPU 型号", "GPU 型號", "GPU 型號", "GPU Model", "GPU モデル", "GPU 모델"),
			Value: gpuLabel,
		},
		{
			Label: notifyLocalePick(loc, "失败原因", "失敗原因", "失敗原因", "Reason", "原因", "원인"),
			Value: reason,
		},
	}
	if attempts > 0 {
		fields = append(fields, notifier.ColabOneNotificationField{
			Label: notifyLocalePick(loc, "已尝试次数", "已嘗試次數", "已嘗試次數", "Attempts", "試行回数", "시도 횟수"),
			Value: fmt.Sprintf("%d", attempts),
		})
	}

	c.emitNotificationEvent(notifier.ColabOneNotificationEvent{
		ID:          fmt.Sprintf("gpu-alloc-failed-%d", time.Now().UnixNano()),
		Kind:        notifier.EventGPUAllocateFailed,
		Category:    notifier.CategoryAlert,
		Severity:    notifier.SeverityError,
		DedupeKey:   "gpu.allocate_failed:" + gpuLabel + ":" + truncateRunes(reason, 80),
		Title:       title,
		Subtitle:    notifyLocalePick(loc, "分配流程已中止，请及时查看", "分配流程已中止，請及時查看", "分配流程已中止，請及時查看", "Allocation stopped and needs attention", "割り当てが停止しました。確認が必要です", "할당이 중단되어 확인이 필요합니다"),
		Body:        reason,
		Fields:      fields,
		ActionHint:  actionHint,
		NavigateTab: "device",
		Locale:      loc,
	})
}

func (c *ColabOneController) buildGPUDisconnectedNotification(endpoint, gpu string, loc string) pendingControllerNotification {
	gpuLabel := strings.ToUpper(strings.TrimSpace(gpu))
	if gpuLabel == "" {
		gpuLabel = "GPU"
	}
	title := fmt.Sprintf(
		notifyLocalePick(loc,
			"%s 远端实例连接已断开",
			"%s 遠端實例連線已中斷",
			"%s 遠端實例連線已中斷",
			"%s Runtime Disconnected",
			"%s リモート接続が切断されました",
			"%s 원격 연결이 끊어졌습니다",
		),
		gpuLabel,
	)
	body := notifyLocalePick(loc,
		"与远端 GPU 实例的控制连接已中断，该实例在 Google Colab 侧可能仍在计费，请尽快核查状态。",
		"與遠端 GPU 實例的控制連線已中斷，該實例在 Google Colab 端可能仍在計費，請儘快核查狀態。",
		"與遠端 GPU 實例的控制連線已中斷，該實例在 Google Colab 端可能仍在計費，請盡快核查狀態。",
		"Control connection to the remote GPU runtime was lost. The instance may still be billing on Google Colab.",
		"リモート GPU との制御接続が切断されました。Google Colab 側で課金が継続している可能性があるためご確認ください。",
		"원격 GPU 인스턴스와의 제어 연결이 끊어졌습니다. Google Colab에서 과금이 계속될 수 있으니 상태를 확인해 주세요.",
	)
	hint := notifyLocalePick(loc,
		"请返回 ColabOne 检查网络并尝试重连，如不再使用请及时手动释放以避免额外 CU 消耗。",
		"請返回 ColabOne 檢查網路並嘗試重連，如不再使用請及時手動釋放以避免額外 CU 消耗。",
		"請返回 ColabOne 檢查網絡並嘗試重連，如不再使用請及時手動釋放以避免額外 CU 消耗。",
		"Return to ColabOne to reconnect or manually release the runtime to stop CU consumption.",
		"ColabOne に戻って再接続するか、不要な場合は手動で解放して CU 消費を停止してください。",
		"ColabOne으로 돌아가 재연결하거나, 사용하지 않는 경우 수동으로 해제하여 CU 소모를 중단하세요.",
	)
	fields := []notifier.ColabOneNotificationField{
		{
			Label: notifyLocalePick(loc, "GPU 型号", "GPU 型號", "GPU 型號", "GPU Model", "GPU モデル", "GPU 모델"),
			Value: gpuLabel,
		},
	}
	if strings.TrimSpace(endpoint) != "" {
		fields = append(fields, notifier.ColabOneNotificationField{
			Label: notifyLocalePick(loc, "实例标识", "實例識別", "實例識別", "Instance", "インスタンス", "인스턴스"),
			Value: strings.TrimSpace(endpoint),
		})
	}
	return pendingControllerNotification{
		event: notifier.ColabOneNotificationEvent{
			ID:          "gpu-disconnected-" + strings.TrimSpace(endpoint),
			Kind:        notifier.EventGPUDisconnected,
			Category:    notifier.CategoryAlert,
			Severity:    notifier.SeverityWarning,
			DedupeKey:   "gpu.disconnected:" + strings.TrimSpace(endpoint),
			Title:       title,
			Subtitle:    notifyLocalePick(loc, "实例可能仍在计费，建议及时处理", "實例可能仍在計費，建議及時處理", "實例可能仍在計費，建議及時處理", "Instance may still be billing", "課金継続の可能性があります", "과금이 계속될 수 있습니다"),
			Body:        body,
			Fields:      fields,
			ActionHint:  hint,
			NavigateTab: "device",
			Locale:      loc,
		},
	}
}

func (c *ColabOneController) notifyGPUAutoReleased(reasonKind, detail string, fields []notifier.ColabOneNotificationField) {
	c.notifyGPUAutoReleasedForEndpoint("", "", reasonKind, detail, fields)
}

func (c *ColabOneController) notifyGPUAutoReleasedForEndpoint(endpoint, gpu, reasonKind, detail string, fields []notifier.ColabOneNotificationField) {
	loc := c.effectiveNotificationLocale()
	cleanEP := strings.TrimSpace(endpoint)
	cleanGPU := strings.ToUpper(strings.TrimSpace(gpu))
	var title, subtitle, defaultBody string
	switch reasonKind {
	case "cu_limit":
		title = notifyLocalePick(loc, "已达 CU 上限，GPU 实例已自动释放", "已達 CU 上限，GPU 實例已自動釋放", "已達 CU 上限，GPU 實例已自動釋放", "GPU Auto-Released at CU Limit", "CU 上限により GPU を自動解放しました", "CU 한도 도달로 GPU 자동 해제됨")
		subtitle = notifyLocalePick(loc, "已完成云盘同步并停止计费", "已完成雲端硬碟同步並停止計費", "已完成雲端硬碟同步並停止計費", "Drive synced and billing stopped", "ドライブ同期完了・課金停止済み", "드라이브 동기화 완료 및 과금 중단")
		defaultBody = notifyLocalePick(loc,
			"已达到设定的 CU 自动释放上限，已完成云盘同步并释放实例。",
			"已達到設定的 CU 自動釋放上限，已完成雲端硬碟同步並釋放實例。",
			"已達到設定的 CU 自動釋放上限，已完成雲端硬碟同步並釋放實例。",
			"The configured CU auto-release limit was reached. Drive data was saved and the runtime was released.",
			"設定した CU 自動解放上限に達したため、ドライブへ保存してインスタンスを解放しました。",
			"설정한 CU 자동 해제 한도에 도달하여 드라이브에 저장한 뒤 인스턴스를 해제했습니다.",
		)
	case "workflow_sync":
		title = notifyLocalePick(loc, "作品已同步云盘，GPU 已自动释放", "作品已同步雲端硬碟，GPU 已自動釋放", "作品已同步雲端硬碟，GPU 已自動釋放", "Outputs Synced & GPU Auto-Released", "作品をドライブへ同期し GPU を自動解放しました", "작품 드라이브 동기화 후 GPU 자동 해제됨")
		subtitle = notifyLocalePick(loc, "工作流完成并安全结束本次算力会话", "工作流完成並安全結束本次算力工作階段", "工作流完成並安全結束本次算力工作階段", "Workflow finished and runtime released safely", "ワークフロー完了後に安全に解放しました", "워크플로 완료 후 안전하게 해제되었습니다")
		defaultBody = notifyLocalePick(loc,
			"远端实例已完成作品同步至 Google 云盘并自动释放。",
			"遠端實例已完成作品同步至 Google 雲端硬碟並自動釋放。",
			"遠端實例已完成作品同步至 Google 雲端硬碟並自動釋放。",
			"Outputs have been synced to Google Drive and the remote GPU runtime was automatically released.",
			"作品の Google ドライブ同期が完了し、リモート GPU インスタンスを自動解放しました。",
			"결과물이 Google 드라이브에 동기화되었으며 원격 GPU 인스턴스가 자동 해제되었습니다.",
		)
	default:
		title = notifyLocalePick(loc, "GPU 实例已自动释放", "GPU 實例已自動釋放", "GPU 實例已自動釋放", "GPU Runtime Auto-Released", "GPU インスタンスが自動解放されました", "GPU 인스턴스가 자동 해제되었습니다")
		subtitle = notifyLocalePick(loc, "远端算力会话已结束并停止计费", "遠端算力工作階段已結束並停止計費", "遠端算力工作階段已結束並停止計費", "Remote compute session ended", "リモート計算セッションが終了しました", "원격 컴퓨팅 세션이 종료되었습니다")
		defaultBody = notifyLocalePick(loc,
			"远端 GPU 实例因空闲或远端超时已自动释放并停止计费。",
			"遠端 GPU 實例因閒置或遠端逾時已自動釋放並停止計費。",
			"遠端 GPU 實例因閒置或遠端逾時已自動釋放並停止計費。",
			"The remote GPU runtime was automatically released due to inactivity or timeout and billing has stopped.",
			"アイドルまたはタイムアウトにより、リモート GPU インスタンスが自動解放され課金が停止しました。",
			"유휴 상태 또는 시간 초과로 인해 원격 GPU 인스턴스가 자동 해제되어 과금이 중단되었습니다.",
		)
	}
	body := strings.TrimSpace(detail)
	if body == "" {
		body = defaultBody
	}
	outFields := make([]notifier.ColabOneNotificationField, 0, len(fields)+2)
	outFields = append(outFields, fields...)
	if cleanGPU != "" {
		outFields = append(outFields, notifier.ColabOneNotificationField{
			Label: notifyLocalePick(loc, "GPU 型号", "GPU 型號", "GPU 型號", "GPU Model", "GPU モデル", "GPU 모델"),
			Value: cleanGPU,
		})
	}
	if cleanEP != "" {
		outFields = append(outFields, notifier.ColabOneNotificationField{
			Label: notifyLocalePick(loc, "实例标识", "實例識別", "實例識別", "Instance", "インスタンス", "인스턴스"),
			Value: cleanEP,
		})
	}
	dedupeKey := "gpu.auto_released:" + reasonKind
	idSuffix := fmt.Sprintf("%s-%d", reasonKind, time.Now().UnixNano())
	if cleanEP != "" {
		dedupeKey += ":" + cleanEP
		idSuffix = reasonKind + "-" + cleanEP
	}
	c.emitNotificationEvent(notifier.ColabOneNotificationEvent{
		ID:          "gpu-autoreleased-" + idSuffix,
		Kind:        notifier.EventGPUAutoReleased,
		Category:    notifier.CategoryGPU,
		Severity:    notifier.SeverityInfo,
		DedupeKey:   dedupeKey,
		Title:       title,
		Subtitle:    subtitle,
		Body:        body,
		Fields:      outFields,
		NavigateTab: "device",
		Locale:      loc,
	})
}

func (c *ColabOneController) notifyGPUAutoReleasedCULimit(consumed float64, limitStr string) {
	loc := c.effectiveNotificationLocale()
	body := fmt.Sprintf(
		notifyLocalePick(loc,
			"本次运行已消耗 %.2f CU，达到设定的 %s CU 自动释放上限，已自动保存并释放实例。",
			"本次執行已消耗 %.2f CU，達到設定的 %s CU 自動釋放上限，已自動儲存並釋放實例。",
			"本次執行已消耗 %.2f CU，達到設定的 %s CU 自動釋放上限，已自動儲存並釋放實例。",
			"This session consumed %.2f CU, reaching the configured %s CU auto-release limit. Data was saved and the runtime was released.",
			"今回のセッションで %.2f CU を消費し、設定上限の %s CU に達したため、保存してインスタンスを自動解放しました。",
			"이번 세션에서 %.2f CU를 소모하여 설정한 %s CU 자동 해제 한도에 도달했으며, 데이터를 저장하고 인스턴스를 자동 해제했습니다.",
		),
		consumed, limitStr,
	)
	fields := []notifier.ColabOneNotificationField{
		{
			Label: notifyLocalePick(loc, "本次消耗", "本次消耗", "本次消耗", "Consumed", "消費量", "소모량"),
			Value: fmt.Sprintf("%.2f CU", consumed),
		},
		{
			Label: notifyLocalePick(loc, "自动释放上限", "自動釋放上限", "自動釋放上限", "CU Limit", "自動解放上限", "자동 해제 한도"),
			Value: limitStr + " CU",
		},
	}
	c.notifyGPUAutoReleasedForEndpoint("", "", "cu_limit", body, fields)
}

func (c *ColabOneController) notifyGPUCancelCleanupIncomplete(gpu string, endpoints []string) {
	loc := c.effectiveNotificationLocale()
	gpuLabel := strings.ToUpper(strings.TrimSpace(gpu))
	if gpuLabel == "" {
		gpuLabel = "GPU"
	}
	epList := strings.Join(endpoints, ", ")
	title := fmt.Sprintf(
		notifyLocalePick(loc,
			"取消启动后仍有残留 %s 实例待核查",
			"取消啟動後仍有殘留 %s 實例待核查",
			"取消啟動後仍有殘留 %s 實例待核查",
			"Lingering %s Runtime Needs Verification",
			"キャンセル後に残存 %s インスタンスの確認が必要です",
			"취소 후 남아 있는 %s 인스턴스 확인 필요",
		),
		gpuLabel,
	)
	subtitle := notifyLocalePick(loc,
		"未能确认释放刚分配的实例，可能仍在计费",
		"未能確認釋放剛分配的實例，可能仍在計費",
		"未能確認釋放剛分配的實例，可能仍在計費",
		"Auto-cleanup did not confirm release; instance may still bill",
		"自動解放を確認できませんでした。課金が継続している可能性があります",
		"자동 해제가 확인되지 않았습니다. 과금이 계속될 수 있습니다",
	)
	body := notifyLocalePick(loc,
		"您取消了 GPU 启动，但自动回滚未能确认释放刚分配的远端实例，请尽快在「远端设备」页核查并手动停止以避免持续消耗 CU。",
		"您取消了 GPU 啟動，但自動回滾未能確認釋放剛分配的遠端實例，請儘快在「遠端裝置」頁核查並手動停止以避免持續消耗 CU。",
		"您取消了 GPU 啟動，但自動回滾未能確認釋放剛分配的遠端實例，請盡快在「遠端裝置」頁核查並手動停止以避免持續消耗 CU。",
		"GPU startup was cancelled, but rollback could not confirm release of the newly allocated runtime. Please check Remote Devices and stop it manually to avoid extra CU billing.",
		"GPU 起動をキャンセルしましたが、割り当て済みインスタンスの自動解放を確認できませんでした。リモートデバイス画面で確認し、必要に応じて手動停止してください。",
		"GPU 시작을 취소했지만 새로 할당된 인스턴스의 자동 해제를 확인하지 못했습니다. 원격 장치 화면에서 상태를 확인하고 수동으로 중지해 주세요.",
	)
	hint := notifyLocalePick(loc,
		"请打开 ColabOne「远端设备」页确认实例状态并手动释放。",
		"請開啟 ColabOne「遠端裝置」頁確認實例狀態並手動釋放。",
		"請開啟 ColabOne「遠端裝置」頁確認實例狀態並手動釋放。",
		"Open Remote Devices in ColabOne to inspect and manually stop the runtime.",
		"ColabOne のリモートデバイス画面で状態を確認し、手動で解放してください。",
		"ColabOne 원격 장치 화면에서 인스턴스 상태를 확인하고 수동으로 해제하세요.",
	)
	fields := []notifier.ColabOneNotificationField{
		{
			Label: notifyLocalePick(loc, "GPU 型号", "GPU 型號", "GPU 型號", "GPU Model", "GPU モデル", "GPU 모델"),
			Value: gpuLabel,
		},
	}
	if epList != "" {
		fields = append(fields, notifier.ColabOneNotificationField{
			Label: notifyLocalePick(loc, "残留实例", "殘留實例", "殘留實例", "Lingering Instance", "残存インスタンス", "잔여 인스턴스"),
			Value: epList,
		})
	}
	c.emitNotificationEvent(notifier.ColabOneNotificationEvent{
		ID:          "gpu-cancel-leftover-" + firstNonEmpty(epList, gpuLabel),
		Kind:        notifier.EventGPUDisconnected,
		Category:    notifier.CategoryAlert,
		Severity:    notifier.SeverityWarning,
		DedupeKey:   "gpu.cancel_leftover:" + epList,
		Title:       title,
		Subtitle:    subtitle,
		Body:        body,
		Fields:      fields,
		ActionHint:  hint,
		NavigateTab: "device",
		Locale:      loc,
	})
}

func isAuthLikeErrorMessage(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return false
	}
	for _, needle := range []string{
		"invalid_grant",
		"unauthorized",
		"http 401",
		"re-login",
		"reauthenticate",
		"重新登录",
		"重新登入",
		"登录已过期",
		"授权已失效",
		"授权过期",
		"凭据已失效",
		"刷新 token 失败",
		"尚未登录",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (c *ColabOneController) notifyAuthInterventionRequired(categoryKey, reason string) {
	cleanReason := truncateRunes(SafeMessage(strings.TrimSpace(reason)), 260)
	if cleanReason == "" {
		return
	}
	now := time.Now()
	c.mu.Lock()
	if c.lastAuthNotifyAt == nil {
		c.lastAuthNotifyAt = make(map[string]time.Time)
	}
	if prev, ok := c.lastAuthNotifyAt[categoryKey]; ok && now.Sub(prev) < colabOneAuthNotifyCooldown {
		c.mu.Unlock()
		return
	}
	c.lastAuthNotifyAt[categoryKey] = now
	c.mu.Unlock()

	loc := c.effectiveNotificationLocale()
	title := notifyLocalePick(loc,
		"Google 授权需要重新确认",
		"Google 授權需要重新確認",
		"Google 授權需要重新確認",
		"Google Sign-In Attention Needed",
		"Google 認証の再確認が必要です",
		"Google 인증 재확인이 필요합니다",
	)
	hint := notifyLocalePick(loc,
		"请打开 ColabOne「设置」或主界面重新完成 Google 账号授权，以恢复自动状态同步与云盘操作。",
		"請開啟 ColabOne「設定」或主畫面重新完成 Google 帳號授權，以恢復自動狀態同步與雲端硬碟操作。",
		"請開啟 ColabOne「設定」或主畫面重新完成 Google 帳戶授權，以恢復自動狀態同步與雲端硬碟操作。",
		"Open ColabOne to sign in to Google again and restore automatic status sync and Drive persistence.",
		"ColabOne を開いて Google アカウントを再認証すると、自動同期とドライブ連携が再開されます。",
		"ColabOne을 열어 Google 계정에 다시 로그인하면 자동 동기화와 드라이브 연동이 복구됩니다.",
	)
	c.emitNotificationEvent(notifier.ColabOneNotificationEvent{
		ID:        "auth-required-" + categoryKey,
		Kind:      notifier.EventAuthRequired,
		Category:  notifier.CategoryAlert,
		Severity:  notifier.SeverityWarning,
		DedupeKey: "auth.required:" + categoryKey,
		Title:     title,
		Subtitle:  notifyLocalePick(loc, "需要人工介入以恢复服务", "需要人工介入以恢復服務", "需要人工介入以恢復服務", "Manual action required", "手動対応が必要です", "수동 조치가 필요합니다"),
		Body:      cleanReason,
		Fields: []notifier.ColabOneNotificationField{
			{
				Label: notifyLocalePick(loc, "详情", "詳情", "詳情", "Details", "詳細", "세부 정보"),
				Value: cleanReason,
			},
		},
		ActionHint:  hint,
		NavigateTab: "settings",
		Locale:      loc,
	})
}

func (c *ColabOneController) buildTaskNotificationLocked(
	endpoint string,
	task map[string]any,
	tKind, tState, tMsg, tErr string,
	serverTime float64,
	outputs []any,
	prevOutputs []any,
	loc string,
) (pendingControllerNotification, bool) {
	tid := asString(task["id"])
	if tid == "" || (tState != "completed" && tState != "failed") {
		return pendingControllerNotification{}, false
	}

	elapsedText := ""
	if elapsedSec, ok := resolveTaskElapsedSeconds(task, serverTime); ok {
		elapsedText = formatDurationForNotification(loc, elapsedSec)
	}

	switch tKind {
	case "comfy_run", "comfyui_run", "comfyui":
		if tState == "completed" {
			matched := make([]map[string]any, 0, 4)
			for _, raw := range outputs {
				item := asMap(raw)
				if item == nil {
					continue
				}
				if asString(item["task_id"]) == tid {
					matched = append(matched, item)
				}
			}
			// Fallback for older remote servers that do not attach task_id on outputs.
			if len(matched) == 0 && len(outputs) > 0 {
				startedAt, hasStarted := asFloat(task["started_at"])
				if hasStarted && !math.IsNaN(startedAt) && !math.IsInf(startedAt, 0) && startedAt > 0 {
					upperBound := math.Inf(1)
					if !math.IsNaN(serverTime) && !math.IsInf(serverTime, 0) && serverTime > 0 {
						upperBound = serverTime + 5
					} else if finishedAt, hasFin := asFloat(task["finished_at"]); hasFin && !math.IsNaN(finishedAt) && !math.IsInf(finishedAt, 0) && finishedAt > 0 {
						upperBound = finishedAt + 5
					}
					for _, raw := range outputs {
						item := asMap(raw)
						if item == nil {
							continue
						}
						if otherTID := strings.TrimSpace(asString(item["task_id"])); otherTID != "" && otherTID != tid {
							continue
						}
						createdAt, hasCreated := asFloat(item["created_at"])
						if hasCreated && !math.IsNaN(createdAt) && !math.IsInf(createdAt, 0) &&
							createdAt >= startedAt-2 && createdAt <= upperBound {
							matched = append(matched, item)
						}
					}
				} else {
					prevIDs := make(map[string]bool, len(prevOutputs))
					for _, rawPrev := range prevOutputs {
						if prevItem := asMap(rawPrev); prevItem != nil {
							if pid := strings.TrimSpace(asString(prevItem["id"])); pid != "" {
								prevIDs[pid] = true
							}
						}
					}
					for _, raw := range outputs {
						item := asMap(raw)
						if item == nil {
							continue
						}
						if otherTID := strings.TrimSpace(asString(item["task_id"])); otherTID != "" && otherTID != tid {
							continue
						}
						oid := strings.TrimSpace(asString(item["id"]))
						if oid != "" && !prevIDs[oid] {
							matched = append(matched, item)
						}
					}
				}
			}
			var previewOut map[string]any
			workflowName := ""
			for _, item := range matched {
				if workflowName == "" {
					workflowName = strings.TrimSpace(asString(item["workflow_name"]))
				}
				if previewOut == nil && isNotificationImageOutput(item) {
					previewOut = item
				}
			}
			if workflowName == "" {
				workflowName = strings.TrimSpace(asString(task["workflow_name"]))
			}
			if workflowName == "" {
				workflowName = strings.TrimSpace(asString(task["label"]))
			}
			count := len(matched)
			title := notifyLocalePick(loc, "作品生成已完成", "作品生成已完成", "作品生成已完成", "Output Ready", "作品の生成が完了しました", "작품 생성 완료")
			if workflowName != "" {
				title = fmt.Sprintf(
					notifyLocalePick(loc, "作品已生成：%s", "作品已生成：%s", "作品已生成：%s", "Output Ready: %s", "作品が完成しました：%s", "작품 생성 완료: %s"),
					truncateRunes(workflowName, 48),
				)
			}
			body := notifyLocalePick(loc, "ComfyUI 工作流已运行完成，请返回 ColabOne 查收作品。", "ComfyUI 工作流已執行完成，請返回 ColabOne 查收作品。", "ComfyUI 工作流已執行完成，請返回 ColabOne 查收作品。", "Your ComfyUI workflow finished. Open ColabOne to view or export your output.", "ComfyUI ワークフローが完了しました。ColabOne で作品を確認してください。", "ComfyUI 워크플로가 완료되었습니다. ColabOne에서 결과물을 확인하세요.")
			if count > 0 {
				body = fmt.Sprintf(
					notifyLocalePick(loc, "本次共生成 %d 件作品，已就绪可随时预览或导出保存。", "本次共生成 %d 件作品，已就緒可隨時預覽或匯出保存。", "本次共生成 %d 件作品，已就緒可隨時預覽或匯出保存。", "%d output(s) generated and ready to preview or export in ColabOne.", "%d 件の作品が生成されました。プレビューまたは保存できます。", "총 %d개의 결과물이 생성되어 미리보기 및 내보내기가 가능합니다."),
					count,
				)
			} else if tMsg != "" {
				body = truncateRunes(SafeMessage(tMsg), 220)
			}
			fields := make([]notifier.ColabOneNotificationField, 0, 4)
			if workflowName != "" {
				fields = append(fields, notifier.ColabOneNotificationField{
					Label: notifyLocalePick(loc, "工作流", "工作流", "工作流", "Workflow", "ワークフロー", "워크플로"),
					Value: workflowName,
				})
			}
			if count > 0 {
				fields = append(fields, notifier.ColabOneNotificationField{
					Label: notifyLocalePick(loc, "生成数量", "生成數量", "生成數量", "Outputs", "生成数", "생성 수량"),
					Value: fmt.Sprintf(notifyLocalePick(loc, "%d 件", "%d 件", "%d 件", "%d item(s)", "%d 件", "%d개"), count),
				})
			}
			if previewOut != nil && strings.TrimSpace(asString(previewOut["filename"])) != "" {
				fields = append(fields, notifier.ColabOneNotificationField{
					Label: notifyLocalePick(loc, "代表文件", "代表檔案", "代表檔案", "File", "ファイル", "파일"),
					Value: strings.TrimSpace(asString(previewOut["filename"])),
				})
			}
			if elapsedText != "" {
				fields = append(fields, notifier.ColabOneNotificationField{
					Label: notifyLocalePick(loc, "生成耗时", "生成耗時", "生成耗時", "Duration", "所要時間", "소요 시간"),
					Value: elapsedText,
				})
			}
			pn := pendingControllerNotification{
				event: notifier.ColabOneNotificationEvent{
					ID:          "output-ready-" + tid,
					Kind:        notifier.EventOutputReady,
					Category:    notifier.CategoryTask,
					Severity:    notifier.SeveritySuccess,
					DedupeKey:   "output.ready:" + endpoint + ":" + tid,
					Title:       title,
					Subtitle:    notifyLocalePick(loc, "成果物已生成，点击可直达作品区", "成果物已生成，點擊可直達作品區", "成果物已生成，點擊可直達作品區", "Ready to view in Outputs", "クリックして作品一覧を開く", "클릭하여 결과물 보기"),
					Body:        body,
					Fields:      fields,
					NavigateTab: "comfy",
					OpenOutputs: true,
					Locale:      loc,
				},
			}
			if previewOut != nil {
				pn.previewOutputID = strings.TrimSpace(asString(previewOut["id"]))
				pn.previewFilename = strings.TrimSpace(asString(previewOut["filename"]))
				pn.previewMime = strings.TrimSpace(asString(previewOut["mime"]))
			}
			return pn, true
		}

		// Workflow / output failed
		errFallback := notifyLocalePick(loc, "工作流执行失败", "工作流執行失敗", "工作流執行失敗", "Workflow execution failed", "ワークフローの実行に失敗しました", "워크플로 실행에 실패했습니다")
		errSummary := truncateRunes(SafeMessage(firstNonEmpty(tErr, tMsg, errFallback)), 240)
		fields := []notifier.ColabOneNotificationField{
			{
				Label: notifyLocalePick(loc, "错误信息", "錯誤訊息", "錯誤訊息", "Error", "エラー", "오류"),
				Value: errSummary,
			},
		}
		if elapsedText != "" {
			fields = append(fields, notifier.ColabOneNotificationField{
				Label: notifyLocalePick(loc, "已耗时", "已耗時", "已耗時", "Elapsed", "経過時間", "경과 시간"),
				Value: elapsedText,
			})
		}
		return pendingControllerNotification{
			event: notifier.ColabOneNotificationEvent{
				ID:          "output-failed-" + tid,
				Kind:        notifier.EventOutputFailed,
				Category:    notifier.CategoryAlert,
				Severity:    notifier.SeverityError,
				DedupeKey:   "output.failed:" + endpoint + ":" + tid,
				Title:       notifyLocalePick(loc, "作品生成失败", "作品生成失敗", "作品生成失敗", "Workflow Generation Failed", "作品の生成に失敗しました", "작품 생성 실패"),
				Subtitle:    notifyLocalePick(loc, "ComfyUI 工作流执行异常，需要检查", "ComfyUI 工作流執行異常，需要檢查", "ComfyUI 工作流執行異常，需要檢查", "ComfyUI workflow encountered an error", "ComfyUI ワークフローでエラーが発生しました", "ComfyUI 워크플로에서 오류가 발생했습니다"),
				Body:        errSummary,
				Fields:      fields,
				ActionHint:  notifyLocalePick(loc, "请返回 ColabOne 查看任务日志、显存占用或工作流参数配置。", "請返回 ColabOne 查看任務日誌、顯存佔用或工作流參數設定。", "請返回 ColabOne 查看任務日誌、顯存佔用或工作流參數設定。", "Open ColabOne to inspect logs, VRAM usage, or workflow inputs.", "ColabOne でログ、VRAM 使用状況、またはワークフロー設定をご確認ください。", "ColabOne에서 로그, VRAM 사용량 또는 워크플로 설정을 확인해 주세요."),
				NavigateTab: "comfy",
				Locale:      loc,
			},
		}, true

	case "download":
		if tState == "completed" {
			summaryFallback := notifyLocalePick(loc, "模型权重已下载到远端实例", "模型權重已下載到遠端實例", "模型權重已下載到遠端實例", "Model weights have been downloaded to the remote runtime.", "モデルウェイトをリモートインスタンスへダウンロードしました。", "모델 가중치가 원격 인스턴스에 다운로드되었습니다.")
			summary := truncateRunes(SafeMessage(firstNonEmpty(tMsg, summaryFallback)), 220)
			fields := []notifier.ColabOneNotificationField{
				{
					Label: notifyLocalePick(loc, "任务类型", "任務類型", "任務類型", "Task", "タスク", "작업"),
					Value: notifyLocalePick(loc, "模型下载", "模型下載", "模型下載", "Model Download", "モデルダウンロード", "모델 다운로드"),
				},
			}
			if elapsedText != "" {
				fields = append(fields, notifier.ColabOneNotificationField{
					Label: notifyLocalePick(loc, "耗时", "耗時", "耗時", "Duration", "所要時間", "소요 시간"),
					Value: elapsedText,
				})
			}
			return pendingControllerNotification{
				event: notifier.ColabOneNotificationEvent{
					ID:          "model-download-ok-" + tid,
					Kind:        notifier.EventModelDownloadCompleted,
					Category:    notifier.CategoryTask,
					Severity:    notifier.SeveritySuccess,
					DedupeKey:   "model.download_completed:" + endpoint + ":" + tid,
					Title:       notifyLocalePick(loc, "模型下载已完成", "模型下載已完成", "模型下載已完成", "Model Download Complete", "モデルのダウンロードが完了しました", "모델 다운로드 완료"),
					Subtitle:    notifyLocalePick(loc, "权重文件已就绪，可立即加载推理", "權重檔案已就緒，可立即載入推論", "權重檔案已就緒，可立即載入推論", "Weights are ready to load", "モデルを読み込む準備が整いました", "가중치 파일이 준비되었습니다"),
					Body:        summary,
					Fields:      fields,
					NavigateTab: "models",
					Locale:      loc,
				},
			}, true
		}
		errFallback := notifyLocalePick(loc, "模型下载失败", "模型下載失敗", "模型下載失敗", "Model download failed", "モデルのダウンロードに失敗しました", "모델 다운로드에 실패했습니다")
		errSummary := truncateRunes(SafeMessage(firstNonEmpty(tErr, tMsg, errFallback)), 240)
		return pendingControllerNotification{
			event: notifier.ColabOneNotificationEvent{
				ID:        "model-download-failed-" + tid,
				Kind:      notifier.EventModelDownloadFailed,
				Category:  notifier.CategoryAlert,
				Severity:  notifier.SeverityError,
				DedupeKey: "model.download_failed:" + endpoint + ":" + tid,
				Title:     notifyLocalePick(loc, "模型下载失败", "模型下載失敗", "模型下載失敗", "Model Download Failed", "モデルのダウンロードに失敗しました", "모델 다운로드 실패"),
				Subtitle:  notifyLocalePick(loc, "下载中断或校验失败，需要处理", "下載中斷或校驗失敗，需要處理", "下載中斷或校驗失敗，需要處理", "Download interrupted or failed", "ダウンロードが中断または失敗しました", "다운로드가 중단되었거나 실패했습니다"),
				Body:      errSummary,
				Fields: []notifier.ColabOneNotificationField{
					{
						Label: notifyLocalePick(loc, "失败原因", "失敗原因", "失敗原因", "Reason", "原因", "원인"),
						Value: errSummary,
					},
				},
				ActionHint:  notifyLocalePick(loc, "请检查 Hugging Face Token 权限、磁盘剩余空间或网络状态后重试。", "請檢查 Hugging Face Token 權限、磁碟剩餘空間或網路狀態後重試。", "請檢查 Hugging Face Token 權限、磁碟剩餘空間或網絡狀態後重試。", "Check Hugging Face token permissions, disk space, or network status and retry.", "Hugging Face トークン権限、ディスク容量、ネットワークを確認して再試行してください。", "Hugging Face 토큰 권한, 디스크 여유 공간 또는 네트워크를 확인하고 다시 시도하세요."),
				NavigateTab: "models",
				Locale:      loc,
			},
		}, true

	case "load":
		if tState == "completed" {
			summaryFallback := notifyLocalePick(loc, "模型已完成加载并准备就绪", "模型已完成載入並準備就緒", "模型已完成載入並準備就緒", "Model has finished loading and is ready.", "モデルの読み込みが完了し、利用可能になりました。", "모델 로드가 완료되어 사용할 준비가 되었습니다.")
			summary := truncateRunes(SafeMessage(firstNonEmpty(tMsg, summaryFallback)), 220)
			fields := []notifier.ColabOneNotificationField{
				{
					Label: notifyLocalePick(loc, "任务类型", "任務類型", "任務類型", "Task", "タスク", "작업"),
					Value: notifyLocalePick(loc, "模型加载", "模型載入", "模型載入", "Model Load", "モデル読込", "모델 로드"),
				},
			}
			if elapsedText != "" {
				fields = append(fields, notifier.ColabOneNotificationField{
					Label: notifyLocalePick(loc, "加载耗时", "載入耗時", "載入耗時", "Duration", "所要時間", "소요 시간"),
					Value: elapsedText,
				})
			}
			return pendingControllerNotification{
				event: notifier.ColabOneNotificationEvent{
					ID:          "model-load-ok-" + tid,
					Kind:        notifier.EventModelLoadCompleted,
					Category:    notifier.CategoryTask,
					Severity:    notifier.SeveritySuccess,
					DedupeKey:   "model.load_completed:" + endpoint + ":" + tid,
					Title:       notifyLocalePick(loc, "模型加载完成，已可开始对话", "模型載入完成，已可開始對話", "模型載入完成，已可開始對話", "Model Loaded & Ready", "モデルの読み込みが完了しました", "모델 로드 완료"),
					Subtitle:    notifyLocalePick(loc, "推理引擎已就绪", "推論引擎已就緒", "推論引擎已就緒", "Inference engine is online", "推論エンジンが稼働しました", "추론 엔진이 준비되었습니다"),
					Body:        summary,
					Fields:      fields,
					NavigateTab: "models",
					Locale:      loc,
				},
			}, true
		}
		errFallback := notifyLocalePick(loc, "模型加载失败", "模型載入失敗", "模型載入失敗", "Model failed to load", "モデルの読み込みに失敗しました", "모델 로드에 실패했습니다")
		errSummary := truncateRunes(SafeMessage(firstNonEmpty(tErr, tMsg, errFallback)), 240)
		return pendingControllerNotification{
			event: notifier.ColabOneNotificationEvent{
				ID:        "model-load-failed-" + tid,
				Kind:      notifier.EventModelLoadFailed,
				Category:  notifier.CategoryAlert,
				Severity:  notifier.SeverityError,
				DedupeKey: "model.load_failed:" + endpoint + ":" + tid,
				Title:     notifyLocalePick(loc, "模型加载失败", "模型載入失敗", "模型載入失敗", "Model Load Failed", "モデルの読み込みに失敗しました", "모델 로드 실패"),
				Subtitle:  notifyLocalePick(loc, "显存不足或启动参数异常，需要处理", "顯存不足或啟動參數異常，需要處理", "顯存不足或啟動參數異常，需要處理", "Model failed to load on GPU", "GPU へのモデル読み込みに失敗しました", "GPU에 모델을 불러오지 못했습니다"),
				Body:      errSummary,
				Fields: []notifier.ColabOneNotificationField{
					{
						Label: notifyLocalePick(loc, "失败原因", "失敗原因", "失敗原因", "Reason", "原因", "원인"),
						Value: errSummary,
					},
				},
				ActionHint:  notifyLocalePick(loc, "请检查显存占用、上下文长度设置或尝试更换更轻量的量化版本。", "請檢查顯存佔用、上下文長度設定或嘗試更換更輕量的量化版本。", "請檢查顯存佔用、上下文長度設定或嘗試更換更輕量的量化版本。", "Check VRAM usage, context length, or try a smaller quantization.", "VRAM 使用量やコンテキスト長を確認するか、軽量な量子化版をお試しください。", "VRAM 사용량과 컨텍스트 길이를 확인하거나 더 가벼운 양자화 버전을 사용해 보세요."),
				NavigateTab: "models",
				Locale:      loc,
			},
		}, true
	}

	return pendingControllerNotification{}, false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
