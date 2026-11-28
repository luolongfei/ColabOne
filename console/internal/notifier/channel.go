package notifier

import (
	"context"
	"errors"
)

// Sentinel errors for notification channel operations.
var (
	ErrSystemUnsupported      = errors.New("当前系统环境暂不支持原生桌面通知")
	ErrSystemPermissionDenied = errors.New("系统桌面通知权限未开启")
	ErrUnknownChannel         = errors.New("未知的通知渠道")
	ErrChannelNotConfigured   = errors.New("该通知渠道尚未配置完成")
)

// ColabOneNotificationChannel represents a single delivery backend (System, Telegram, Bark, or Email).
type ColabOneNotificationChannel interface {
	ID() string
	IsRemote() bool
	Send(ctx context.Context, cfg ColabOneNotificationConfig, event ColabOneNotificationEvent) error
}

// ColabOneSystemNotificationRequest is delivered to the host OS desktop notification bridge.
type ColabOneSystemNotificationRequest struct {
	ID          string
	Title       string
	Subtitle    string
	Body        string
	Category    ColabOneNotificationCategory
	Severity    ColabOneNotificationSeverity
	NavigateTab string
	OpenOutputs bool
	ImageBytes  []byte
	ImageMime   string
	ImageExt    string
}

// ColabOneSystemSender dispatches a prepared desktop notification request to the host OS.
type ColabOneSystemSender func(ctx context.Context, req ColabOneSystemNotificationRequest) error
