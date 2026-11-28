package notifier

import (
	"html"
	"strings"
	"time"
)

type colabOneSeverityPalette struct {
	PillBg       string
	PillText     string
	CalloutBg    string
	CalloutBar   string
	CalloutTitle string
}

func colabOnePaletteForSeverity(sev ColabOneNotificationSeverity) colabOneSeverityPalette {
	switch sev {
	case SeveritySuccess:
		return colabOneSeverityPalette{
			PillBg:       "#E8F8EE",
			PillText:     "#157A3E",
			CalloutBg:    "#F2FBF5",
			CalloutBar:   "#157A3E",
			CalloutTitle: "#157A3E",
		}
	case SeverityWarning:
		return colabOneSeverityPalette{
			PillBg:       "#FFF5E0",
			PillText:     "#B25E09",
			CalloutBg:    "#FFF9EE",
			CalloutBar:   "#B25E09",
			CalloutTitle: "#B25E09",
		}
	case SeverityError:
		return colabOneSeverityPalette{
			PillBg:       "#FDECEC",
			PillText:     "#C62828",
			CalloutBg:    "#FDF4F4",
			CalloutBar:   "#C62828",
			CalloutTitle: "#C62828",
		}
	default:
		return colabOneSeverityPalette{
			PillBg:       "#EBF4FF",
			PillText:     "#0066CC",
			CalloutBg:    "#F3F8FF",
			CalloutBar:   "#0066CC",
			CalloutTitle: "#0066CC",
		}
	}
}

// RenderEmailHTML renders an Apple-inspired, table-based, 100%-inline-CSS HTML
// email with zero external remote assets. When event.ImageBytes is non-empty,
// an inline preview card referencing cid:colabone-preview is included.
func RenderEmailHTML(event ColabOneNotificationEvent) string {
	locale := NormalizeLocale(event.Locale)
	msg := colabOneMessagesForLocale(locale)
	palette := colabOnePaletteForSeverity(event.Severity)
	statusLabel := colabOneSeverityLabel(event.Severity, locale)

	title := strings.TrimSpace(event.Title)
	if title == "" {
		title = "ColabOne"
	}
	subtitle := strings.TrimSpace(event.Subtitle)
	body := strings.TrimSpace(event.Body)
	actionHint := strings.TrimSpace(event.ActionHint)

	occurred := event.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now()
	}
	timeStr := occurred.Format("2006-01-02 15:04:05 MST")

	preheader := subtitle
	if preheader == "" {
		preheader = body
	}
	if preheader == "" {
		preheader = title
	}
	preheader = colabOneTruncateRunes(strings.ReplaceAll(strings.ReplaceAll(preheader, "\r", " "), "\n", " "), 120)

	const fontStack = `-apple-system,BlinkMacSystemFont,'SF Pro Display','SF Pro Text','Helvetica Neue',Helvetica,Arial,'PingFang SC','Hiragino Sans GB','Microsoft YaHei',sans-serif`

	var b strings.Builder
	b.Grow(4096)

	b.WriteString(`<!DOCTYPE html>`)
	b.WriteString(`<html lang="`)
	b.WriteString(html.EscapeString(locale))
	b.WriteString(`"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"><meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light"><title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title></head>`)
	b.WriteString(`<body style="margin:0;padding:0;background-color:#F5F5F7;font-family:`)
	b.WriteString(fontStack)
	b.WriteString(`;-webkit-font-smoothing:antialiased;color:#1D1D1F;">`)

	// Hidden inbox preheader text with zero-width padding so clients do not pull card text into inbox snippets.
	b.WriteString(`<div style="display:none;max-height:0;overflow:hidden;opacity:0;mso-hide:all;font-size:1px;line-height:1px;color:#F5F5F7;">`)
	b.WriteString(html.EscapeString(preheader))
	b.WriteString(strings.Repeat("&zwnj;&nbsp;", 30))
	b.WriteString(`</div>`)

	// Outer full-width background table.
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;background-color:#F5F5F7;border-collapse:collapse;">`)
	b.WriteString(`<tr><td align="center" style="padding:36px 16px 40px 16px;">`)

	// Outlook Desktop fixed-width container wrapper.
	b.WriteString(`<!--[if mso]><table role="presentation" align="center" width="580" cellpadding="0" cellspacing="0" border="0" style="width:580px;border-collapse:collapse;"><tr><td><![endif]-->`)

	// Centered card container (max-width: 580px).
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:580px;width:100%;background-color:#FFFFFF;border-radius:20px;border:1px solid #E5E5EA;border-collapse:separate;overflow:hidden;">`)

	// 1. Header lockup: ColabOne brand badge + wordmark + localized timestamp.
	b.WriteString(`<tr><td style="padding:26px 32px 20px 32px;border-bottom:1px solid #F0F0F4;">`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;border-collapse:collapse;"><tr>`)
	b.WriteString(`<td align="left" valign="middle">`)
	b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="border-collapse:collapse;"><tr>`)
	b.WriteString(`<td align="center" valign="middle" width="28" height="28" style="width:28px;height:28px;border-radius:8px;background-color:#1D1D1F;background:linear-gradient(135deg,#1D1D1F 0%,#3A3A3C 100%);color:#FFFFFF;font-size:13px;font-weight:700;line-height:28px;text-align:center;">C</td>`)
	b.WriteString(`<td style="padding-left:10px;font-size:16px;font-weight:600;letter-spacing:-0.02em;color:#1D1D1F;line-height:1;">ColabOne</td>`)
	b.WriteString(`</tr></table></td>`)
	b.WriteString(`<td align="right" valign="middle" style="font-size:12px;color:#86868B;white-space:nowrap;">`)
	b.WriteString(html.EscapeString(timeStr))
	b.WriteString(`</td></tr></table></td></tr>`)

	// 2. Hero section: status pill + title + subtitle + body.
	b.WriteString(`<tr><td style="padding:28px 32px 0 32px;">`)
	b.WriteString(`<span style="display:inline-block;padding:4px 12px;border-radius:999px;background-color:`)
	b.WriteString(palette.PillBg)
	b.WriteString(`;color:`)
	b.WriteString(palette.PillText)
	b.WriteString(`;font-size:12px;font-weight:600;line-height:1.4;letter-spacing:0.01em;">`)
	b.WriteString(html.EscapeString(statusLabel))
	b.WriteString(`</span>`)

	b.WriteString(`<h1 style="margin:14px 0 0 0;padding:0;font-size:24px;line-height:1.28;font-weight:700;letter-spacing:-0.025em;color:#1D1D1F;">`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</h1>`)

	if subtitle != "" {
		b.WriteString(`<p style="margin:6px 0 0 0;padding:0;font-size:14px;line-height:1.5;font-weight:500;color:#6E6E73;">`)
		b.WriteString(html.EscapeString(subtitle))
		b.WriteString(`</p>`)
	}

	if body != "" {
		b.WriteString(`<p style="margin:14px 0 0 0;padding:0;font-size:15px;line-height:1.65;color:#424245;">`)
		b.WriteString(colabOneEscapeHTMLWithBreaks(body))
		b.WriteString(`</p>`)
	}
	b.WriteString(`</td></tr>`)

	// 3. Optional inline preview image card (cid:colabone-preview).
	if len(event.ImageBytes) > 0 {
		captionText := msg.PreviewCaption
		if fn := strings.TrimSpace(event.ImageFilename); fn != "" {
			captionText = captionText + " · " + fn
		}
		b.WriteString(`<tr><td style="padding:22px 32px 0 32px;">`)
		b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;background-color:#F5F5F7;border-radius:14px;border:1px solid #E5E5EA;border-collapse:separate;overflow:hidden;">`)
		b.WriteString(`<tr><td style="padding:0;line-height:0;font-size:0;">`)
		b.WriteString(`<img src="cid:colabone-preview" width="514" alt="`)
		b.WriteString(html.EscapeString(msg.PreviewCaption))
		b.WriteString(`" style="display:block;width:100%;max-width:514px;height:auto;border:0;border-radius:13px 13px 0 0;" />`)
		b.WriteString(`</td></tr>`)
		b.WriteString(`<tr><td style="padding:10px 14px;font-size:12px;line-height:1.4;color:#6E6E73;background-color:#F5F5F7;border-top:1px solid #E5E5EA;">`)
		b.WriteString(html.EscapeString(captionText))
		b.WriteString(`</td></tr>`)
		b.WriteString(`</table></td></tr>`)
	}

	// 4. Apple-style hairline summary table.
	var validFields []ColabOneNotificationField
	for _, f := range event.Fields {
		if strings.TrimSpace(f.Value) != "" {
			validFields = append(validFields, f)
		}
	}
	if len(validFields) > 0 {
		b.WriteString(`<tr><td style="padding:22px 32px 0 32px;">`)
		b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;background-color:#F5F5F7;border-radius:14px;border:1px solid #E5E5EA;border-collapse:separate;">`)
		for i, f := range validFields {
			borderTop := ""
			if i > 0 {
				borderTop = "border-top:1px solid #E5E5EA;"
			}
			b.WriteString(`<tr>`)
			b.WriteString(`<td align="left" valign="top" style="padding:12px 12px 12px 16px;`)
			b.WriteString(borderTop)
			b.WriteString(`font-size:13px;line-height:1.45;color:#6E6E73;white-space:nowrap;">`)
			b.WriteString(html.EscapeString(strings.TrimSpace(f.Label)))
			b.WriteString(`</td>`)
			b.WriteString(`<td align="right" valign="top" style="padding:12px 16px 12px 12px;`)
			b.WriteString(borderTop)
			b.WriteString(`font-size:13px;line-height:1.45;font-weight:500;color:#1D1D1F;word-break:break-word;">`)
			b.WriteString(html.EscapeString(strings.TrimSpace(f.Value)))
			b.WriteString(`</td>`)
			b.WriteString(`</tr>`)
		}
		b.WriteString(`</table></td></tr>`)
	}

	// 5. Optional action-hint callout box.
	if actionHint != "" {
		b.WriteString(`<tr><td style="padding:22px 32px 0 32px;">`)
		b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;background-color:`)
		b.WriteString(palette.CalloutBg)
		b.WriteString(`;border-radius:12px;border-left:3px solid `)
		b.WriteString(palette.CalloutBar)
		b.WriteString(`;border-collapse:separate;">`)
		b.WriteString(`<tr><td style="padding:14px 16px;">`)
		b.WriteString(`<div style="font-size:12px;font-weight:600;line-height:1.4;color:`)
		b.WriteString(palette.CalloutTitle)
		b.WriteString(`;">`)
		b.WriteString(html.EscapeString(msg.ActionHintLabel))
		b.WriteString(`</div>`)
		b.WriteString(`<div style="margin-top:4px;font-size:13px;line-height:1.55;color:#424245;">`)
		b.WriteString(colabOneEscapeHTMLWithBreaks(actionHint))
		b.WriteString(`</div>`)
		b.WriteString(`</td></tr></table></td></tr>`)
	}

	// Bottom inner breathing room.
	b.WriteString(`<tr><td style="height:30px;line-height:30px;font-size:0;">&nbsp;</td></tr>`)
	b.WriteString(`</table>`)

	// Minimalist localized footer outside the card.
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:580px;width:100%;border-collapse:collapse;">`)
	b.WriteString(`<tr><td align="center" style="padding:20px 16px 0 16px;font-size:12px;line-height:1.6;color:#86868B;">`)
	b.WriteString(html.EscapeString(msg.EmailFooter))
	b.WriteString(`</td></tr></table>`)

	b.WriteString(`<!--[if mso]></td></tr></table><![endif]-->`)

	b.WriteString(`</td></tr></table></body></html>`)
	return b.String()
}

// RenderEmailPlainText renders a clean plain-text email companion matching the HTML template.
func RenderEmailPlainText(event ColabOneNotificationEvent) string {
	locale := NormalizeLocale(event.Locale)
	msg := colabOneMessagesForLocale(locale)
	statusLabel := colabOneSeverityLabel(event.Severity, locale)

	title := strings.TrimSpace(event.Title)
	if title == "" {
		title = "ColabOne"
	}
	occurred := event.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now()
	}

	var sb strings.Builder
	sb.WriteString("ColabOne [")
	sb.WriteString(statusLabel)
	sb.WriteString("] ")
	sb.WriteString(title)
	sb.WriteString("\n")
	if sub := strings.TrimSpace(event.Subtitle); sub != "" {
		sb.WriteString(sub)
		sb.WriteString("\n")
	}
	sb.WriteString(occurred.Format("2006-01-02 15:04:05 MST"))
	sb.WriteString("\n")

	if body := strings.TrimSpace(event.Body); body != "" {
		sb.WriteString("\n")
		sb.WriteString(body)
		sb.WriteString("\n")
	}

	if len(event.ImageBytes) > 0 {
		sb.WriteString("\n[")
		sb.WriteString(msg.PreviewCaption)
		if fn := strings.TrimSpace(event.ImageFilename); fn != "" {
			sb.WriteString(": ")
			sb.WriteString(fn)
		}
		sb.WriteString("]\n")
	}

	if len(event.Fields) > 0 {
		var lines []string
		for _, f := range event.Fields {
			lbl := strings.TrimSpace(f.Label)
			val := strings.TrimSpace(f.Value)
			if lbl == "" && val == "" {
				continue
			}
			if lbl != "" && val != "" {
				lines = append(lines, "- "+lbl+": "+val)
			} else if val != "" {
				lines = append(lines, "- "+val)
			}
		}
		if len(lines) > 0 {
			sb.WriteString("\n")
			sb.WriteString(strings.Join(lines, "\n"))
			sb.WriteString("\n")
		}
	}

	if hint := strings.TrimSpace(event.ActionHint); hint != "" {
		sb.WriteString("\n")
		sb.WriteString(msg.ActionHintLabel)
		sb.WriteString(": ")
		sb.WriteString(hint)
		sb.WriteString("\n")
	}

	sb.WriteString("\n-- \n")
	sb.WriteString(msg.EmailFooter)
	sb.WriteString("\n")
	return sb.String()
}

func colabOneEscapeHTMLWithBreaks(s string) string {
	escaped := html.EscapeString(s)
	escaped = strings.ReplaceAll(escaped, "\r\n", "\n")
	escaped = strings.ReplaceAll(escaped, "\r", "\n")
	return strings.ReplaceAll(escaped, "\n", "<br>")
}
