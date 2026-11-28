package notifier

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	colabOneDefaultQueueCap       = 64
	colabOneDefaultWorkerCount    = 4
	colabOneMaxDedupeEntries      = 1024
	colabOneDefaultDedupeWindow   = 60 * time.Second
	colabOneDefaultChannelTimeout = 20 * time.Second
	colabOneCloseGraceTimeout     = 2 * time.Second
	colabOneCloseHardTimeout      = 1 * time.Second
)

func colabOneNoRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// ColabOneNotifierConfig configures ColabOneNotifier's callbacks, timeouts, and dependencies.
type ColabOneNotifierConfig struct {
	ConfigProvider  func() ColabOneNotificationConfig
	IsWindowFocused func() bool
	SystemSender    ColabOneSystemSender
	SystemSupported func() bool
	HTTPClient      *http.Client
	OnLog           func(msg, source, level string)
	DedupeWindow    time.Duration
	ChannelTimeout  time.Duration
	NowFn           func() time.Time
}

// ColabOneNotifier coordinates asynchronous and synchronous fan-out across
// System, Telegram, Bark, and Email channels with category filters, deduplication,
// foreground focus suppression, and strict secret redaction.
type ColabOneNotifier struct {
	cfg            ColabOneNotifierConfig
	channels       map[string]ColabOneNotificationChannel
	channelOrder   []string
	systemCh       *colabOneSystemChannel
	dedupeWindow   time.Duration
	channelTimeout time.Duration
	nowFn          func() time.Time

	queue        chan ColabOneNotificationEvent
	stopCh       chan struct{}
	doneCh       chan struct{}
	workerCtx    context.Context
	workerCancel context.CancelFunc
	closeOnce    sync.Once

	mu     sync.Mutex
	dedupe map[string]time.Time
}

// NewColabOneNotifier constructs a ColabOneNotifier with all four built-in channels
// registered and starts its background dispatch worker.
func NewColabOneNotifier(cfg ColabOneNotifierConfig) *ColabOneNotifier {
	dedupeWindow := cfg.DedupeWindow
	if dedupeWindow <= 0 {
		dedupeWindow = colabOneDefaultDedupeWindow
	}
	channelTimeout := cfg.ChannelTimeout
	if channelTimeout <= 0 {
		channelTimeout = colabOneDefaultChannelTimeout
	}
	nowFn := cfg.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	var httpClient *http.Client
	if cfg.HTTPClient != nil {
		copied := *cfg.HTTPClient
		copied.CheckRedirect = colabOneNoRedirect
		httpClient = &copied
	} else {
		httpClient = &http.Client{
			Timeout:       channelTimeout,
			CheckRedirect: colabOneNoRedirect,
		}
	}

	sysCh := newColabOneSystemChannel(cfg.SystemSender, cfg.SystemSupported)
	tgCh := newColabOneTelegramChannel(httpClient)
	barkCh := newColabOneBarkChannel(httpClient)
	emailCh := newColabOneEmailChannel()

	workerCtx, workerCancel := context.WithCancel(context.Background())

	n := &ColabOneNotifier{
		cfg: cfg,
		channels: map[string]ColabOneNotificationChannel{
			ChannelSystem:   sysCh,
			ChannelTelegram: tgCh,
			ChannelBark:     barkCh,
			ChannelEmail:    emailCh,
		},
		channelOrder:   []string{ChannelSystem, ChannelTelegram, ChannelBark, ChannelEmail},
		systemCh:       sysCh,
		dedupeWindow:   dedupeWindow,
		channelTimeout: channelTimeout,
		nowFn:          nowFn,
		queue:          make(chan ColabOneNotificationEvent, colabOneDefaultQueueCap),
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
		workerCtx:      workerCtx,
		workerCancel:   workerCancel,
		dedupe:         make(map[string]time.Time),
	}

	var workerWG sync.WaitGroup
	for i := 0; i < colabOneDefaultWorkerCount; i++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			n.runWorker()
		}()
	}
	go func() {
		workerWG.Wait()
		close(n.doneCh)
	}()
	return n
}

func (n *ColabOneNotifier) runWorker() {
	for {
		select {
		case <-n.stopCh:
			return
		case event := <-n.queue:
			select {
			case <-n.stopCh:
				return
			default:
			}
			n.DispatchSync(n.workerCtx, event)
		}
	}
}

// Notify enqueues event for asynchronous delivery without blocking the caller.
// Events are dropped gracefully if the notifier is closed or the queue is full.
func (n *ColabOneNotifier) Notify(event ColabOneNotificationEvent) {
	if n == nil {
		return
	}
	select {
	case <-n.stopCh:
		return
	default:
	}
	if len(event.ImageBytes) > 0 {
		imgCopy := make([]byte, len(event.ImageBytes))
		copy(imgCopy, event.ImageBytes)
		event.ImageBytes = imgCopy
	}
	if len(event.Fields) > 0 {
		fieldsCopy := make([]ColabOneNotificationField, len(event.Fields))
		copy(fieldsCopy, event.Fields)
		event.Fields = fieldsCopy
	}
	select {
	case n.queue <- event:
	case <-n.stopCh:
	default:
		n.logf("warning", "通知队列已满，已跳过事件：%s", strings.TrimSpace(event.Title))
	}
}

// DispatchSync synchronously evaluates filtering rules and fans out event across
// all eligible enabled channels in parallel, returning a per-channel error map.
func (n *ColabOneNotifier) DispatchSync(
	ctx context.Context,
	event ColabOneNotificationEvent,
) map[string]error {
	results := make(map[string]error)
	if n == nil {
		return results
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := n.nowFn()
	if event.OccurredAt.IsZero() {
		event.OccurredAt = now
	}
	event.Locale = NormalizeLocale(event.Locale)
	if event.Severity == "" {
		event.Severity = SeverityInfo
	}
	if event.Category == "" {
		event.Category = colabOneInferCategory(event.Kind)
	}

	cfg := n.currentConfig()
	if !event.IsTest && !colabOneCategoryEnabled(cfg, event.Category) {
		return results
	}

	focused := n.isWindowFocused()
	eligible := n.selectEligibleChannels(cfg, event, focused)
	if len(eligible) == 0 {
		return results
	}

	if !event.IsTest && strings.TrimSpace(event.DedupeKey) != "" {
		if !n.checkAndRecordDedupe(strings.TrimSpace(event.DedupeKey), now) {
			return results
		}
	}

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for _, ch := range eligible {
		ch := ch
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicErr := colabOneRedactError(fmt.Errorf("通知渠道 %s 发生内部异常: %v", ch.ID(), r), cfg)
					n.logf("error", "通知渠道 %s 执行异常：%s", ch.ID(), panicErr.Error())
					mu.Lock()
					results[ch.ID()] = panicErr
					mu.Unlock()
				}
			}()
			sendCtx, cancel := context.WithTimeout(ctx, n.channelTimeout)
			defer cancel()

			err := ch.Send(sendCtx, cfg, event)
			if err != nil {
				redacted := colabOneRedactError(err, cfg)
				n.logf("warning", "通知渠道 %s 发送失败：%s", ch.ID(), redacted.Error())
				mu.Lock()
				results[ch.ID()] = redacted
				mu.Unlock()
				return
			}
			mu.Lock()
			results[ch.ID()] = nil
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}

func colabOneSafeChannelSend(
	ctx context.Context,
	ch ColabOneNotificationChannel,
	cfg ColabOneNotificationConfig,
	event ColabOneNotificationEvent,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("通知渠道 %s 发生内部异常: %v", ch.ID(), r)
		}
	}()
	return ch.Send(ctx, cfg, event)
}

// TestChannel synchronously delivers a rich localized test notification to the
// specified channel, bypassing category toggles, deduplication, and focus suppression.
func (n *ColabOneNotifier) TestChannel(ctx context.Context, channelID, locale string) error {
	if n == nil {
		return ErrUnknownChannel
	}
	if ctx == nil {
		ctx = context.Background()
	}

	id := strings.ToLower(strings.TrimSpace(channelID))
	ch, ok := n.channels[id]
	if !ok {
		return ErrUnknownChannel
	}

	cfg := n.currentConfig()
	switch id {
	case ChannelSystem:
		if !n.SystemSupported() {
			return ErrSystemUnsupported
		}
	case ChannelTelegram:
		if !cfg.TelegramConfigured() {
			return errors.New("请先填写 Telegram Bot Token 和 Chat ID")
		}
	case ChannelBark:
		if !cfg.BarkConfigured() {
			return errors.New("请先填写 Bark 推送地址或 Device Key")
		}
	case ChannelEmail:
		if !cfg.EmailConfigured() {
			return errors.New("请先完善邮件 SMTP 服务器、发件人账号与收件人邮箱")
		}
	}

	event := BuildTestNotificationEvent(id, locale, n.nowFn())
	sendCtx, cancel := context.WithTimeout(ctx, n.channelTimeout)
	defer cancel()

	if err := colabOneSafeChannelSend(sendCtx, ch, cfg, event); err != nil {
		redacted := colabOneRedactError(err, cfg)
		n.logf("warning", "测试通知渠道 %s 失败：%s", id, redacted.Error())
		return redacted
	}
	return nil
}

// SystemSupported reports whether native desktop notifications are available.
func (n *ColabOneNotifier) SystemSupported() bool {
	if n == nil || n.systemCh == nil {
		return false
	}
	return n.systemCh.isSupported()
}

// Close stops the background dispatch worker and waits for in-flight dispatches
// with a bounded timeout so shutdown never hangs.
func (n *ColabOneNotifier) Close() {
	if n == nil {
		return
	}
	n.closeOnce.Do(func() {
		close(n.stopCh)
		select {
		case <-n.doneCh:
			n.workerCancel()
			return
		case <-time.After(colabOneCloseGraceTimeout):
			n.workerCancel()
		}
		select {
		case <-n.doneCh:
		case <-time.After(colabOneCloseHardTimeout):
		}
	})
}

func (n *ColabOneNotifier) currentConfig() ColabOneNotificationConfig {
	if n.cfg.ConfigProvider != nil {
		return n.cfg.ConfigProvider()
	}
	return DefaultConfig()
}

func (n *ColabOneNotifier) isWindowFocused() bool {
	if n.cfg.IsWindowFocused != nil {
		return n.cfg.IsWindowFocused()
	}
	return false
}

func (n *ColabOneNotifier) selectEligibleChannels(
	cfg ColabOneNotificationConfig,
	event ColabOneNotificationEvent,
	focused bool,
) []ColabOneNotificationChannel {
	out := make([]ColabOneNotificationChannel, 0, len(n.channelOrder))
	for _, id := range n.channelOrder {
		ch, ok := n.channels[id]
		if !ok {
			continue
		}
		switch id {
		case ChannelSystem:
			if !cfg.System.Enabled || !n.SystemSupported() {
				continue
			}
			if !event.IsTest && cfg.SystemOnlyWhenUnfocused && focused {
				continue
			}
		case ChannelTelegram:
			if !cfg.Telegram.Enabled || !cfg.TelegramConfigured() {
				continue
			}
			if !event.IsTest && cfg.RemoteOnlyWhenUnfocused && focused {
				continue
			}
		case ChannelBark:
			if !cfg.Bark.Enabled || !cfg.BarkConfigured() {
				continue
			}
			if !event.IsTest && cfg.RemoteOnlyWhenUnfocused && focused {
				continue
			}
		case ChannelEmail:
			if !cfg.Email.Enabled || !cfg.EmailConfigured() {
				continue
			}
			if !event.IsTest && cfg.RemoteOnlyWhenUnfocused && focused {
				continue
			}
		default:
			continue
		}
		out = append(out, ch)
	}
	return out
}

func (n *ColabOneNotifier) checkAndRecordDedupe(key string, now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	for k, ts := range n.dedupe {
		age := now.Sub(ts)
		if age < 0 || age >= n.dedupeWindow {
			delete(n.dedupe, k)
		}
	}
	if prev, exists := n.dedupe[key]; exists {
		age := now.Sub(prev)
		if age >= 0 && age < n.dedupeWindow {
			return false
		}
	}
	for len(n.dedupe) >= colabOneMaxDedupeEntries {
		var (
			oldestKey string
			oldestTS  time.Time
			first     = true
		)
		for k, ts := range n.dedupe {
			if first || ts.Before(oldestTS) {
				oldestKey = k
				oldestTS = ts
				first = false
			}
		}
		if first {
			break
		}
		delete(n.dedupe, oldestKey)
	}
	n.dedupe[key] = now
	return true
}

func (n *ColabOneNotifier) logf(level, format string, args ...any) {
	if n == nil || n.cfg.OnLog == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	n.cfg.OnLog(msg, "通知", level)
}

func colabOneCategoryEnabled(cfg ColabOneNotificationConfig, cat ColabOneNotificationCategory) bool {
	switch cat {
	case CategoryGPU:
		return cfg.NotifyGPU
	case CategoryTask:
		return cfg.NotifyTask
	case CategoryAlert:
		return cfg.NotifyAlert
	default:
		return true
	}
}

func colabOneInferCategory(kind ColabOneNotificationEventKind) ColabOneNotificationCategory {
	switch kind {
	case EventGPUAllocated, EventGPUAllocateFailed, EventGPUDisconnected, EventGPUAutoReleased:
		return CategoryGPU
	case EventOutputReady, EventOutputFailed,
		EventModelDownloadCompleted, EventModelDownloadFailed,
		EventModelLoadCompleted, EventModelLoadFailed:
		return CategoryTask
	default:
		return CategoryAlert
	}
}

func colabOneRedactError(err error, cfg ColabOneNotificationConfig) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrSystemUnsupported) ||
		errors.Is(err, ErrSystemPermissionDenied) ||
		errors.Is(err, ErrUnknownChannel) ||
		errors.Is(err, ErrChannelNotConfigured) {
		return err
	}
	return errors.New(RedactSecrets(err.Error(), cfg))
}
