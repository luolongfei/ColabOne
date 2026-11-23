package oauthpage

import (
	"encoding/base64"
	"strings"
	"sync"
	"testing"
)

func TestRenderColabOneOAuthPageAllLocalesSuccessAndError(t *testing.T) {
	cases := []struct {
		locale         string
		titleOK        string
		titleErr       string
		headingOK      string
		headingErr     string
		badgeOK        string
		badgeErr       string
		closeButton    string
		returnButton   string
		shortcutPrefix string
		shortcutSuffix string
		footer         string
	}{
		{
			locale:         "zh-CN",
			titleOK:        "授权成功 · ColabOne",
			titleErr:       "授权未完成 · ColabOne",
			headingOK:      "已授权登录 ColabOne",
			headingErr:     "登录未完成",
			badgeOK:        "安全凭据已保存至本机",
			badgeErr:       "未保存任何凭据",
			closeButton:    "关闭此页面",
			returnButton:   "关闭并返回重试",
			shortcutPrefix: "如页面未自动关闭，按 ",
			shortcutSuffix: " 即可关闭此标签页",
			footer:         "ColabOne · 远端 GPU 工作空间",
		},
		{
			locale:         "en-US",
			titleOK:        "Signed In · ColabOne",
			titleErr:       "Sign-in Incomplete · ColabOne",
			headingOK:      "Signed in to ColabOne",
			headingErr:     "Sign-in was not completed",
			badgeOK:        "Credentials saved securely on this device",
			badgeErr:       "No credentials were saved",
			closeButton:    "Close this tab",
			returnButton:   "Close and try again",
			shortcutPrefix: "If the tab stays open, press ",
			shortcutSuffix: " to close it",
			footer:         "ColabOne · Remote GPU Workspace",
		},
		{
			locale:         "zh-TW",
			titleOK:        "授權成功 · ColabOne",
			titleErr:       "授權未完成 · ColabOne",
			headingOK:      "已授權登入 ColabOne",
			headingErr:     "登入尚未完成",
			badgeOK:        "安全憑證已儲存至本機",
			badgeErr:       "未儲存任何憑證",
			closeButton:    "關閉此分頁",
			returnButton:   "關閉並返回重試",
			shortcutPrefix: "若分頁未自動關閉，按 ",
			shortcutSuffix: " 即可關閉",
			footer:         "ColabOne · 遠端 GPU 工作區",
		},
		{
			locale:         "zh-HK",
			titleOK:        "授權成功 · ColabOne",
			titleErr:       "授權未完成 · ColabOne",
			headingOK:      "已授權登入 ColabOne",
			headingErr:     "登入尚未完成",
			badgeOK:        "安全憑證已儲存至本機",
			badgeErr:       "未儲存任何憑證",
			closeButton:    "關閉此分頁",
			returnButton:   "關閉並返回再試",
			shortcutPrefix: "如分頁未自動關閉，按 ",
			shortcutSuffix: " 即可關閉",
			footer:         "ColabOne · 遠端 GPU 工作區",
		},
		{
			locale:         "ja-JP",
			titleOK:        "認証完了 · ColabOne",
			titleErr:       "認証未完了 · ColabOne",
			headingOK:      "ColabOne へのログイン完了",
			headingErr:     "ログインが完了しませんでした",
			badgeOK:        "認証情報はこの端末に安全に保存されました",
			badgeErr:       "認証情報は保存されていません",
			closeButton:    "このタブを閉じる",
			returnButton:   "閉じてアプリで再試行",
			shortcutPrefix: "自動で閉じない場合は ",
			shortcutSuffix: " で閉じられます",
			footer:         "ColabOne · リモート GPU ワークスペース",
		},
		{
			locale:         "ko-KR",
			titleOK:        "인증 완료 · ColabOne",
			titleErr:       "로그인 미완료 · ColabOne",
			headingOK:      "ColabOne 로그인 완료",
			headingErr:     "로그인이 완료되지 않았습니다",
			badgeOK:        "인증 정보가 이 기기에 안전하게 저장되었습니다",
			badgeErr:       "저장된 인증 정보가 없습니다",
			closeButton:    "이 탭 닫기",
			returnButton:   "닫고 다시 시도",
			shortcutPrefix: "탭이 자동으로 닫히지 않으면 ",
			shortcutSuffix: " 키로 닫을 수 있습니다",
			footer:         "ColabOne · 원격 GPU 워크스페이스",
		},
	}

	if len(colabOneOAuthCopyByLocale) != len(cases) {
		t.Fatalf("expected %d supported locales, got %d", len(cases), len(colabOneOAuthCopyByLocale))
	}

	for _, tc := range cases {
		t.Run(tc.locale, func(t *testing.T) {
			okHTML := RenderColabOneOAuthPage(tc.locale, true, "ok msg", "")
			for _, want := range []string{
				"<!doctype html>",
				`<html lang="` + tc.locale + `">`,
				`<meta name="color-scheme" content="light dark">`,
				"<title>" + tc.titleOK + "</title>",
				`<body class="is-success">`,
				`<h1 class="co-title">` + tc.headingOK + `</h1>`,
				`<p class="co-desc">ok msg</p>`,
				`<span class="co-pill-text">` + tc.badgeOK + `</span>`,
				`<button id="co-close-btn" class="co-btn" type="button">` + tc.closeButton + `</button>`,
				`<p id="co-hint" class="co-hint">` + tc.shortcutPrefix + `<kbd id="co-kbd" class="co-kbd">Ctrl + W</kbd>` + tc.shortcutSuffix + `</p>`,
				`<footer class="co-footer">` + tc.footer + `</footer>`,
				`class="co-check-path"`,
				`prefers-color-scheme: dark`,
				`prefers-reduced-motion: reduce`,
				`data:image/svg+xml;base64,`,
				`kbd.textContent = isMac ? "⌘ + W" : "Ctrl + W";`,
			} {
				if !strings.Contains(okHTML, want) {
					t.Errorf("ok HTML for %s missing %q", tc.locale, want)
				}
			}
			if strings.Contains(okHTML, `<body class="is-error">`) {
				t.Errorf("ok HTML for %s unexpectedly contains is-error class", tc.locale)
			}

			errHTML := RenderColabOneOAuthPage(tc.locale, false, "err msg", "")
			for _, want := range []string{
				`<html lang="` + tc.locale + `">`,
				"<title>" + tc.titleErr + "</title>",
				`<body class="is-error">`,
				`<h1 class="co-title">` + tc.headingErr + `</h1>`,
				`<p class="co-desc">err msg</p>`,
				`<span class="co-pill-text">` + tc.badgeErr + `</span>`,
				`<button id="co-close-btn" class="co-btn" type="button">` + tc.returnButton + `</button>`,
				`<footer class="co-footer">` + tc.footer + `</footer>`,
				`d="M8 8L16 16M16 8L8 16"`,
				`d="M8 5.2V8.4M8 10.85H8.01"`,
			} {
				if !strings.Contains(errHTML, want) {
					t.Errorf("error HTML for %s missing %q", tc.locale, want)
				}
			}
			if strings.Contains(errHTML, `class="co-check-path"`) {
				t.Errorf("error HTML for %s unexpectedly contains success check path", tc.locale)
			}
		})
	}
}

func TestRenderColabOneOAuthPageEmailBadgeBehavior(t *testing.T) {
	t.Run("success with trimmed email replaces default badge", func(t *testing.T) {
		page := RenderColabOneOAuthPage("en-US", true, "ok", "  demo@example.com  ")
		if !strings.Contains(page, `<span class="co-pill-text">demo@example.com</span>`) {
			t.Fatalf("expected trimmed email in pill badge")
		}
		if strings.Contains(page, "Credentials saved securely on this device") {
			t.Fatalf("default ok badge should not appear when email is supplied")
		}
	})

	t.Run("success with whitespace-only email uses default ok badge", func(t *testing.T) {
		page := RenderColabOneOAuthPage("en-US", true, "ok", "   \t\n  ")
		if !strings.Contains(page, `<span class="co-pill-text">Credentials saved securely on this device</span>`) {
			t.Fatalf("expected default ok badge when email is whitespace-only")
		}
	})

	t.Run("error state ignores supplied email and uses default err badge", func(t *testing.T) {
		page := RenderColabOneOAuthPage("en-US", false, "failed", "demo@example.com")
		if !strings.Contains(page, `<span class="co-pill-text">No credentials were saved</span>`) {
			t.Fatalf("expected default error badge on failure")
		}
		if strings.Contains(page, "demo@example.com") {
			t.Fatalf("email should not be displayed on error page")
		}
	})
}

func TestRenderColabOneOAuthPageFallbackLocale(t *testing.T) {
	for _, unknown := range []string{"", "fr-FR", "xx-XX", "zh", `" onclick="alert(1)`} {
		page := RenderColabOneOAuthPage(unknown, true, "done", "")
		if !strings.Contains(page, `<html lang="zh-CN">`) {
			t.Errorf("locale %q did not fall back to zh-CN", unknown)
		}
		if !strings.Contains(page, "已授权登录 ColabOne") {
			t.Errorf("locale %q did not render zh-CN heading", unknown)
		}
		if strings.Contains(page, `onclick="alert(1)`) {
			t.Errorf("locale %q leaked unescaped attribute into HTML", unknown)
		}
	}
}

func TestColabOneIconDataURIAndFallback(t *testing.T) {
	const prefix = "data:image/svg+xml;base64,"
	uri := colabOneIconDataURI()
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("icon URI missing prefix %q", prefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatalf("icon URI base64 decode failed: %v", err)
	}
	if !strings.Contains(string(decoded), "<svg") || !strings.Contains(string(decoded), "<title>ColabOne</title>") {
		t.Fatalf("decoded embedded icon does not look like ColabOne icon.svg")
	}

	page := RenderColabOneOAuthPage("zh-CN", true, "ok", "")
	if !strings.Contains(page, `<link rel="icon" type="image/svg+xml" href="`+uri+`">`) {
		t.Errorf("favicon link missing embedded icon data URI")
	}
	if !strings.Contains(page, `<img class="co-brand-logo" src="`+uri+`" alt="ColabOne" width="28" height="28">`) {
		t.Errorf("brand logo img missing embedded icon data URI")
	}

	fallbackExpected := prefix + base64.StdEncoding.EncodeToString([]byte(colabOneFallbackIconSVG))
	invalidInputs := [][]byte{
		nil,
		{},
		[]byte("not an svg at all"),
		{0xff, 0xfe, '<', 's', 'v', 'g', '>'}, // invalid UTF-8
	}
	for i, raw := range invalidInputs {
		got := colabOneIconDataURIFromBytes(raw)
		if got != fallbackExpected {
			t.Errorf("invalid input #%d did not fall back to default SVG URI", i)
		}
	}
}

func TestRenderColabOneOAuthPageXSSEscaping(t *testing.T) {
	payloadEmail := `user+<script>alert("x")</script>'&@example.com`
	payloadMsg := `err <tag attr="1" onclick='bad'> & <img src=x onerror=alert(1)>`

	for _, loc := range []string{"zh-CN", "en-US", "zh-TW", "zh-HK", "ja-JP", "ko-KR"} {
		okHTML := RenderColabOneOAuthPage(loc, true, payloadMsg, payloadEmail)
		for _, forbidden := range []string{
			`<script>alert`,
			`<tag attr=`,
			`<img src=x`,
			`onclick='bad'`,
		} {
			if strings.Contains(okHTML, forbidden) {
				t.Errorf("ok HTML (%s) contains unescaped payload %q", loc, forbidden)
			}
		}
		for _, expected := range []string{
			`&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;&#39;&amp;@example.com`,
			`&lt;tag attr=&#34;1&#34; onclick=&#39;bad&#39;&gt; &amp; &lt;img src=x onerror=alert(1)&gt;`,
		} {
			if !strings.Contains(okHTML, expected) {
				t.Errorf("ok HTML (%s) missing escaped sequence %q", loc, expected)
			}
		}

		errHTML := RenderColabOneOAuthPage(loc, false, "err <tag>", payloadEmail)
		if strings.Contains(errHTML, "err <tag>") {
			t.Errorf("error HTML (%s) contains unescaped <tag>", loc)
		}
		if !strings.Contains(errHTML, "err &lt;tag&gt;") {
			t.Errorf("error HTML (%s) missing escaped &lt;tag&gt;", loc)
		}
	}
}

func TestRenderColabOneOAuthPageConcurrentSafe(t *testing.T) {
	locales := []string{"zh-CN", "en-US", "zh-TW", "zh-HK", "ja-JP", "ko-KR", "unknown"}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			loc := locales[idx%len(locales)]
			htmlOut := RenderColabOneOAuthPage(loc, idx%2 == 0, "msg", "user@example.com")
			if !strings.HasPrefix(htmlOut, "<!doctype html>") || !strings.HasSuffix(htmlOut, "</html>") {
				t.Errorf("unexpected HTML envelope for goroutine %d", idx)
			}
		}(i)
	}
	wg.Wait()
}
