package notifier

import (
	"context"
	"errors"
	"os"
	"strings"
)

type colabOneSystemChannel struct {
	sender    ColabOneSystemSender
	supported func() bool
}

func newColabOneSystemChannel(sender ColabOneSystemSender, supported func() bool) *colabOneSystemChannel {
	return &colabOneSystemChannel{
		sender:    sender,
		supported: supported,
	}
}

func (c *colabOneSystemChannel) ID() string {
	return ChannelSystem
}

func (c *colabOneSystemChannel) IsRemote() bool {
	return false
}

func (c *colabOneSystemChannel) isSupported() bool {
	if c == nil || c.sender == nil {
		return false
	}
	if c.supported != nil {
		return c.supported()
	}
	return true
}

func (c *colabOneSystemChannel) Send(
	ctx context.Context,
	_ ColabOneNotificationConfig,
	event ColabOneNotificationEvent,
) error {
	if !c.isSupported() {
		return ErrSystemUnsupported
	}

	req := ColabOneSystemNotificationRequest{
		ID:          event.ID,
		Title:       strings.TrimSpace(event.Title),
		Subtitle:    strings.TrimSpace(event.Subtitle),
		Body:        colabOneBuildSystemBody(event),
		Category:    event.Category,
		Severity:    event.Severity,
		NavigateTab: event.NavigateTab,
		OpenOutputs: event.OpenOutputs,
	}
	if req.Title == "" {
		req.Title = "ColabOne"
	}

	if len(event.ImageBytes) > 0 {
		if imgBytes, mimeType, ext, _, _, err := PreparePreviewImage(event.ImageBytes, 512, 1<<20, true); err == nil && len(imgBytes) > 0 {
			req.ImageBytes = imgBytes
			req.ImageMime = mimeType
			req.ImageExt = ext
		}
	}

	if err := c.sender(ctx, req); err != nil {
		return colabOneMapSystemError(err)
	}
	return nil
}

func colabOneBuildSystemBody(event ColabOneNotificationEvent) string {
	body := strings.TrimSpace(event.Body)
	if body != "" {
		return body
	}
	var parts []string
	for _, f := range event.Fields {
		label := strings.TrimSpace(f.Label)
		val := strings.TrimSpace(f.Value)
		if label == "" && val == "" {
			continue
		}
		if label != "" && val != "" {
			parts = append(parts, label+": "+val)
		} else if val != "" {
			parts = append(parts, val)
		}
	}
	if hint := strings.TrimSpace(event.ActionHint); hint != "" {
		parts = append(parts, hint)
	}
	return strings.Join(parts, " | ")
}

func colabOneMapSystemError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrSystemUnsupported) || errors.Is(err, ErrSystemPermissionDenied) {
		return err
	}
	if errors.Is(err, os.ErrPermission) {
		return err
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "unsupported") || strings.Contains(lower, "not supported") {
		return ErrSystemUnsupported
	}
	if strings.Contains(lower, "notifications denied") ||
		strings.Contains(lower, "notification permission") ||
		strings.Contains(lower, "notifications disabled") ||
		strings.Contains(lower, "not authorized for notifications") ||
		strings.Contains(lower, "unauthorizationstatusdenied") {
		return ErrSystemPermissionDenied
	}
	return err
}
