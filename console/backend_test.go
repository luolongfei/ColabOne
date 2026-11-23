package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"mime"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/luolongfei/ColabOne/console/internal/colabclient"
	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/provider"
	"github.com/luolongfei/ColabOne/console/internal/vault"
)

func TestPureGoSelfTestPassesOffline(t *testing.T) {
	if err := runColabOneSelfTest(); err != nil {
		t.Fatalf("runColabOneSelfTest failed: %v", err)
	}
}

func TestInProcessGoControllerSnapshotAndCommands(t *testing.T) {
	cfgDir := t.TempDir()
	var ctrl *controller.ColabOneController
	b := &colabOneBridge{
		factory: func() *controller.ColabOneController {
			ctrl = newColabOneWiredController("", cfgDir, vault.NewMemoryVault(), false)
			return ctrl
		},
	}
	defer b.close()

	raw, err := b.call(context.Background(), "snapshot", nil)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("invalid snapshot JSON: %v", err)
	}
	for _, key := range []string{
		"runtime", "account", "models", "workflows", "settings", "monitor", "logs",
		"setup", "api_settings", "comfy_canvas_settings", "assign_proxy_settings", "common_repos",
	} {
		if _, ok := snap[key]; !ok {
			t.Fatalf("snapshot missing %q key: %v", key, snap)
		}
	}
	if ctrl == nil || len(ctrl.ExpectedServiceRevision()) != 16 {
		t.Fatalf("expected 16-char service revision, got %q", func() string {
			if ctrl == nil {
				return ""
			}
			return ctrl.ExpectedServiceRevision()
		}())
	}

	// Verify command execution + clean close lifecycle.
	resRaw, err := b.call(context.Background(), "locale.set", map[string]any{"locale": "en-US"})
	if err != nil {
		t.Fatalf("locale.set failed: %v", err)
	}
	if !strings.Contains(resRaw, `"ok":true`) {
		t.Fatalf("unexpected locale.set response: %s", resRaw)
	}

	b.close()
	if _, err := b.call(context.Background(), "snapshot", nil); err == nil {
		t.Fatal("expected error calling closed bridge")
	}
}

func TestLargeResultEncodingAndSanitization(t *testing.T) {
	huge := map[string]any{
		"blob":   strings.Repeat("x", 17<<20),
		"url":    "https://accounts.google.com/o/oauth2/auth?a=1&b=2",
		"nanVal": math.NaN(),
		"infVal": math.Inf(1),
	}
	encoded, err := encodeColabOneResultJSON(huge)
	if err != nil {
		t.Fatalf("encodeColabOneResultJSON failed: %v", err)
	}
	if !json.Valid([]byte(encoded)) {
		t.Fatal("encoded JSON is invalid")
	}
	if !strings.Contains(encoded, "a=1&b=2") {
		t.Fatalf("expected raw '&' without HTML escaping in JSON")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded["nanVal"] != nil || decoded["infVal"] != nil {
		t.Fatalf("expected NaN/Inf sanitized to nil, got %v / %v", decoded["nanVal"], decoded["infVal"])
	}
}

func TestExternalURLsAreRestricted(t *testing.T) {
	d := ColabOneDesktop{}
	for _, url := range []string{"file:///etc/passwd", "http://example.com", "https://evil.test", "javascript:alert(1)"} {
		if d.OpenURL(url) == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	for _, url := range []string{"https://accounts.google.com/", "https://console.cloud.google.com/"} {
		if err := validateColabOneExternalURL(url); err != nil {
			t.Fatalf("rejected allowed URL %s: %v", url, err)
		}
	}
}

func TestValidateCanvasURL(t *testing.T) {
	valid := "https://comfy.example.com/_colabone_auth?token=abc"
	u, err := validateColabOneCanvasURL(valid)
	if err != nil {
		t.Fatalf("expected valid canvas URL %q, got error: %v", valid, err)
	}
	if u.Hostname() != "comfy.example.com" || u.Path != "/_colabone_auth" || u.Query().Get("token") != "abc" {
		t.Fatalf("unexpected parsed URL: %+v", u)
	}

	invalid := []string{
		"",
		"http://comfy.example.com/_colabone_auth?token=abc",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https://comfy.example.com/_colabone_auth",
		"https://comfy.example.com/_colabone_auth?token=",
		"https://comfy.example.com/wrong?token=abc",
		"https://foo.trycloudflare.com/_colabone_auth?token=abc",
		"https://trycloudflare.com/_colabone_auth?token=abc",
		"https://localhost/_colabone_auth?token=abc",
		"https://127.0.0.1/_colabone_auth?token=abc",
		"https://[::1]/_colabone_auth?token=abc",
		"https:///_colabone_auth?token=abc",
	}
	for _, raw := range invalid {
		if _, err := validateColabOneCanvasURL(raw); err == nil {
			t.Fatalf("accepted invalid canvas URL %q", raw)
		}
	}
}

func TestOpenComfyCanvasRejectsInvalidTarget(t *testing.T) {
	d := ColabOneDesktop{}
	if err := d.OpenComfyCanvas(context.Background(), "shell"); err == nil {
		t.Fatal("accepted invalid canvas target \"shell\"")
	}
}

func TestExportWorkflowOutputRejectsInvalidID(t *testing.T) {
	d := ColabOneDesktop{}
	for _, id := range []string{"", "not-hex", "../etc/passwd", "ABCDEF0123456789abcdef0123456789"} {
		if _, err := d.ExportWorkflowOutput(context.Background(), id); err == nil {
			t.Fatalf("accepted invalid output id %q", id)
		}
	}
}

func TestWoff2MimeTypeRegistered(t *testing.T) {
	if got := mime.TypeByExtension(".woff2"); !strings.HasPrefix(got, "font/woff2") {
		t.Fatalf("expected .woff2 MIME type font/woff2, got %q", got)
	}
}

func TestMethodTimeoutsAllowLongGPUAllocation(t *testing.T) {
	if got := colabOneMethodTimeout("runtime.start"); got < 15*time.Minute {
		t.Fatalf("runtime.start timeout too short for 10-attempt backoff: %v", got)
	}
	if got := colabOneMethodTimeout("snapshot"); got != 30*time.Second {
		t.Fatalf("snapshot timeout = %v, want 30s", got)
	}
	for _, fastMethod := range []string{
		"runtime.start.cancel",
		"runtime.start.retry",
		"settings.assign_proxy",
		"settings.assign_proxy.test",
	} {
		if got := colabOneMethodTimeout(fastMethod); got != 30*time.Second {
			t.Fatalf("%s timeout = %v, want 30s", fastMethod, got)
		}
	}
}

func TestExternalAndCanvasURLHardening(t *testing.T) {
	for _, raw := range []string{
		"https://user:pass@accounts.google.com/",
		"https://accounts.google.com:8443/",
	} {
		if err := validateColabOneExternalURL(raw); err == nil {
			t.Fatalf("accepted hardened external URL %q", raw)
		}
	}
	if err := validateColabOneExternalURL("https://ACCOUNTS.GOOGLE.COM./"); err != nil {
		t.Fatalf("rejected normalized allowed URL: %v", err)
	}
	for _, raw := range []string{
		"https://[::1%25lo0]/_colabone_auth?token=abc",
		"https://[::ffff:127.0.0.1]/_colabone_auth?token=abc",
	} {
		if _, err := validateColabOneCanvasURL(raw); err == nil {
			t.Fatalf("accepted loopback/zone canvas URL %q", raw)
		}
	}
}

func BenchmarkInProcessGoControllerSnapshot(b *testing.B) {
	cfgDir := b.TempDir()
	br := &colabOneBridge{
		factory: func() *controller.ColabOneController {
			return newColabOneWiredController("", cfgDir, vault.NewMemoryVault(), false)
		},
	}
	defer br.close()
	ctx := context.Background()
	// Warm up controller once before measuring per-call snapshot + JSON encoding.
	if _, err := br.call(ctx, "snapshot", nil); err != nil {
		b.Fatalf("warmup snapshot failed: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := br.call(ctx, "snapshot", nil); err != nil {
			b.Fatalf("snapshot failed: %v", err)
		}
	}
}

func TestBridgeRepeatEventDeliversMultiplePulsesAndWiringRegressions(t *testing.T) {
	// 1. bridgeRepeatEvent must deliver every Set() pulse without wedging after the first,
	//    and once done is closed it must exit cleanly without stealing subsequent pulses.
	ev := controller.NewColabOneEvent()
	done := make(chan struct{})
	var wg sync.WaitGroup
	ch := bridgeRepeatEvent(ev, done, &wg)
	for i := 0; i < 5; i++ {
		ev.Set()
		select {
		case <-ch:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("bridgeRepeatEvent wedged on pulse %d", i+1)
		}
	}
	close(done)
	wg.Wait()
	ev.Set()
	if !ev.IsSet() {
		t.Fatalf("closed bridgeRepeatEvent stole a pulse set after close(done)")
	}
	if ev.Consume(done) {
		t.Fatalf("Consume(closedDone) must return false without clearing event")
	}
	if !ev.IsSet() {
		t.Fatalf("Consume(closedDone) cleared event while done was already closed")
	}

	// 2. mapProxyInfo must keep ExpiresAt zero when TokenExpiresInSeconds == 0.
	mapped := mapProxyInfo(colabclient.ColabOneRuntimeProxyInfo{
		URL:                   "https://rt.invalid",
		Token:                 "tok",
		TokenExpiresInSeconds: 0,
	})
	if mapped == nil || !mapped.ExpiresAt.IsZero() {
		t.Fatalf("expected zero ExpiresAt when TokenExpiresInSeconds == 0, got %+v", mapped)
	}

	// 3. mapColabClientErr wraps raw proxyconnect errors into *provider.AssignProxyError when on assign proxy.
	mappedErr := mapColabClientErr(errors.New("proxyconnect tcp: EOF"), true)
	var apErr *provider.AssignProxyError
	if !errors.As(mappedErr, &apErr) {
		t.Fatalf("expected *provider.AssignProxyError for raw proxyconnect on assign proxy, got %T: %v", mappedErr, mappedErr)
	}

	// 4. structToMap preserves 64-bit integer precision via json.Number.
	m, err := structToMap(map[string]any{"seed": int64(1085739284759283741)})
	if err != nil {
		t.Fatalf("structToMap failed: %v", err)
	}
	if num, ok := m["seed"].(json.Number); !ok || num.String() != "1085739284759283741" {
		t.Fatalf("structToMap lost 64-bit integer precision: %#v (%T)", m["seed"], m["seed"])
	}

	// 5. Production wired controller (configDir == "") must NOT force shortcut sandbox under DefaultConfigDir().
	t.Setenv("COLABONE_SHORTCUT_DIR", "")
	tmpDefault := t.TempDir()
	t.Setenv("COLABONE_CONFIG_DIR", tmpDefault)
	prodCtrl := newColabOneWiredController("", "", vault.NewMemoryVault(), false)
	defer prodCtrl.Close()
	if prodCtrl.ConfigDir() != tmpDefault {
		t.Fatalf("expected default config dir %q, got %q", tmpDefault, prodCtrl.ConfigDir())
	}
}

func TestAllowColabOneComfyNavigate(t *testing.T) {
	const origin = "https://comfy.example.com"

	allowed := []struct {
		rawURL string
		origin string
	}{
		{"https://comfy.example.com/_colabone_auth?token=abc", origin},
		{"https://comfy.example.com/", origin},
		{"https://comfy.example.com/view?filename=ComfyUI_00001_.png", origin},
		{"HTTPS://COMFY.EXAMPLE.COM:443/api/prompt", origin},
		{"https://comfy.example.com./", origin},
		{"blob:https://comfy.example.com/550e8400-e29b-41d4-a716-446655440000", origin},
		{"BLOB:https://COMFY.EXAMPLE.COM:443/1234", origin},
		{"https://comfy.example.com:8443/", "https://comfy.example.com:8443"},
		{"https://[2001:db8::1]/", "https://[2001:db8::1]"},
	}
	for _, tc := range allowed {
		if !allowColabOneComfyNavigate(tc.rawURL, tc.origin) {
			t.Errorf("allowColabOneComfyNavigate(%q, %q) = false, want true", tc.rawURL, tc.origin)
		}
	}

	rejected := []struct {
		rawURL string
		origin string
	}{
		{"https://evil.example.com/", origin},
		{"https://comfy.example.com.evil.com/", origin},
		{"https://comfy.example.com@evil.example.com/", origin},
		{"https://user:pass@comfy.example.com/", origin},
		{"https://comfy.example.com:8443/", origin},
		{"http://comfy.example.com/", origin},
		{"http://comfy.example.com/_colabone_auth?token=abc", origin},
		{"blob:https://evil.example.com/550e8400-e29b-41d4-a716-446655440000", origin},
		{"blob:http://comfy.example.com/550e8400-e29b-41d4-a716-446655440000", origin},
		{"blob:null/550e8400-e29b-41d4-a716-446655440000", origin},
		{"blob:blob:https://comfy.example.com/1234", origin},
		{"blob://comfy.example.com/1234", origin},
		{"javascript:alert(1)", origin},
		{"file:///etc/passwd", origin},
		{"data:text/html,<script>alert(1)</script>", origin},
		{"about:blank", origin},
		{"", origin},
		{"https://comfy.example.com/", ""},
		{"https://comfy.example.com/", "http://comfy.example.com"},
		{"https://comfy.example.com/", "https://comfy.example.com/path"},
		{" https://comfy.example.com/", origin},
	}
	for _, tc := range rejected {
		if allowColabOneComfyNavigate(tc.rawURL, tc.origin) {
			t.Errorf("allowColabOneComfyNavigate(%q, %q) = true, want false", tc.rawURL, tc.origin)
		}
	}
}

func TestAllowColabOneComfyDownloadAndSanitize(t *testing.T) {
	const origin = "https://comfy.example.com"

	for _, tc := range []struct {
		url  string
		name string
	}{
		{"blob:https://comfy.example.com/550e8400-e29b-41d4-a716-446655440000", "workflow.json"},
		{"https://comfy.example.com/api/view?filename=ComfyUI_00001_.png", "ComfyUI_00001_.png"},
		{"data:image/png;base64,iVBORw0KGgo=", "canvas.png"},
	} {
		if !allowColabOneComfyDownload(tc.url, tc.name, origin) {
			t.Errorf("allowColabOneComfyDownload(%q, %q, %q) = false, want true", tc.url, tc.name, origin)
		}
	}

	for _, tc := range []struct {
		url  string
		name string
	}{
		{"https://evil.example.com/workflow.json", "workflow.json"},
		{"http://comfy.example.com/view?filename=out.png", "out.png"},
		{"file:///etc/passwd", "passwd"},
		{"blob:https://comfy.example.com/uuid", "payload.exe"},
		{"blob:https://comfy.example.com/uuid", "payload.exe. "},
		{"https://comfy.example.com/a.bat", "script.bat"},
		{"data:application/octet-stream;base64,AAAA", "installer.dmg"},
	} {
		if allowColabOneComfyDownload(tc.url, tc.name, origin) {
			t.Errorf("allowColabOneComfyDownload(%q, %q, %q) = true, want false", tc.url, tc.name, origin)
		}
	}

	if allowColabOneComfyDownload("https://comfy.example.com/a.png", "a.png", "") {
		t.Error("accepted download with empty allowed origin")
	}

	dlDir := t.TempDir()
	// Clean filename preserves MyGo's precomputed path.
	cleanEv := &mygo.DownloadEvent{
		URL:           "blob:https://comfy.example.com/1234",
		SuggestedName: "workflow.json",
		Path:          filepath.Join(dlDir, "workflow.json"),
	}
	applyColabOneComfyDownload(cleanEv, origin)
	if cleanEv.DefaultPrevented() || cleanEv.Path != filepath.Join(dlDir, "workflow.json") {
		t.Fatalf("clean download modified unexpectedly: prevented=%v path=%q", cleanEv.DefaultPrevented(), cleanEv.Path)
	}

	// Unsafe Windows characters and existing collisions are sanitized and numbered.
	existingPath := filepath.Join(dlDir, "my_workflow_.json")
	if err := os.WriteFile(existingPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirtyEv := &mygo.DownloadEvent{
		URL:           "blob:https://comfy.example.com/1234",
		SuggestedName: "my:workflow?.json",
		Path:          filepath.Join(dlDir, "my:workflow?.json"),
	}
	applyColabOneComfyDownload(dirtyEv, origin)
	if dirtyEv.DefaultPrevented() {
		t.Fatal("sanitizable download was unexpectedly prevented")
	}
	if want := filepath.Join(dlDir, "my_workflow_ (2).json"); dirtyEv.Path != want {
		t.Fatalf("sanitized download path = %q, want %q", dirtyEv.Path, want)
	}

	// Blocked download is prevented.
	blockedEv := &mygo.DownloadEvent{
		URL:           "https://evil.example.com/x.png",
		SuggestedName: "x.png",
		Path:          filepath.Join(dlDir, "x.png"),
	}
	applyColabOneComfyDownload(blockedEv, origin)
	if !blockedEv.DefaultPrevented() {
		t.Fatal("cross-origin download was not prevented")
	}
}

func assertColabOneDownloadNameInvariants(t *testing.T, input, got string) {
	t.Helper()
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeColabOneDownloadName(%q) = %q is not valid UTF-8", input, got)
	}
	if got == "" || got == "." || got == ".." {
		t.Fatalf("sanitizeColabOneDownloadName(%q) = %q, want non-empty non-dot filename", input, got)
	}
	if n := len(got); n > maxDownloadNameBytes {
		t.Fatalf("sanitizeColabOneDownloadName(%q) has %d bytes (> %d): %q", input, n, maxDownloadNameBytes, got)
	}
	first, _ := utf8.DecodeRuneInString(got)
	last, _ := utf8.DecodeLastRuneInString(got)
	if first == '.' || unicode.IsSpace(first) {
		t.Fatalf("sanitizeColabOneDownloadName(%q) = %q has leading dot or space", input, got)
	}
	if last == '.' || unicode.IsSpace(last) {
		t.Fatalf("sanitizeColabOneDownloadName(%q) = %q has trailing dot or space", input, got)
	}
	stem := got
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	if windowsReservedDownloadNames[strings.ToUpper(strings.TrimRightFunc(stem, unicode.IsSpace))] {
		t.Fatalf("sanitizeColabOneDownloadName(%q) = %q is a reserved Windows device name", input, got)
	}
	if again := sanitizeColabOneDownloadName(got); again != got {
		t.Fatalf("sanitizeColabOneDownloadName is not idempotent for %q: first=%q second=%q", input, got, again)
	}
}

func TestSanitizeColabOneDownloadNameRegressions(t *testing.T) {
	// Empty, root, and traversal inputs must fall back to "download" rather than "_".
	for _, raw := range []string{"", "/", `\`, ".", "..", "../..", "foo/..", `foo\..`, `///\\\`} {
		got := sanitizeColabOneDownloadName(raw)
		if got != "download" {
			t.Errorf("sanitizeColabOneDownloadName(%q) = %q, want %q", raw, got, "download")
		}
		assertColabOneDownloadNameInvariants(t, raw, got)
	}

	// Truncation that collapses spaces before the extension into a Win32 reserved name
	// must be checked AFTER truncation and prefixed safely.
	conPadded := "CON" + strings.Repeat(" ", 177) + ".png"
	gotCon := sanitizeColabOneDownloadName(conPadded)
	if gotCon == "CON.png" || gotCon != "colabone-CON.png" {
		t.Errorf("sanitizeColabOneDownloadName(CON+177spaces+.png) = %q, want %q", gotCon, "colabone-CON.png")
	}
	assertColabOneDownloadNameInvariants(t, conPadded, gotCon)

	// Long extension-fallback truncation must re-trim trailing dots/spaces and remain idempotent.
	longTrailingDot := strings.Repeat("a", 175) + ".png." + strings.Repeat("b", 180)
	gotLong := sanitizeColabOneDownloadName(longTrailingDot)
	wantLong := strings.Repeat("a", 175) + ".png"
	if gotLong != wantLong {
		t.Errorf("sanitizeColabOneDownloadName(longTrailingDot) = %q, want %q", gotLong, wantLong)
	}
	assertColabOneDownloadNameInvariants(t, longTrailingDot, gotLong)

	// Blocked extensions revealed by truncation + re-trimming must still be blocked.
	longBlockedExe := strings.Repeat("a", 175) + ".exe." + strings.Repeat("b", 180)
	if allowColabOneComfyDownload("https://comfy.example.com/file", longBlockedExe, "https://comfy.example.com") {
		t.Errorf("allowColabOneComfyDownload accepted truncated .exe filename")
	}

	// UTF-8 byte budget must cap multi-byte CJK & emoji names to <= 180 bytes while
	// preserving valid UTF-8 boundaries and short extensions like .png.
	longCJKPrefixed := "colabone-CON." + strings.Repeat("画", 167) + ".png"
	gotCJKPrefixed := sanitizeColabOneDownloadName(longCJKPrefixed)
	if !strings.HasSuffix(gotCJKPrefixed, ".png") {
		t.Errorf("sanitizeColabOneDownloadName(longCJKPrefixed) = %q, want .png preserved", gotCJKPrefixed)
	}
	assertColabOneDownloadNameInvariants(t, longCJKPrefixed, gotCJKPrefixed)

	longCJKReserved := "CON." + strings.Repeat("画", 167) + ".png"
	gotCJKReserved := sanitizeColabOneDownloadName(longCJKReserved)
	if !strings.HasPrefix(gotCJKReserved, reservedDownloadPrefix) || !strings.HasSuffix(gotCJKReserved, ".png") {
		t.Errorf("sanitizeColabOneDownloadName(longCJKReserved) = %q, want %q prefix and .png suffix", gotCJKReserved, reservedDownloadPrefix)
	}
	assertColabOneDownloadNameInvariants(t, longCJKReserved, gotCJKReserved)

	longEmojiWithExt := strings.Repeat("🎨", 200) + ".png"
	gotEmojiWithExt := sanitizeColabOneDownloadName(longEmojiWithExt)
	if !strings.HasSuffix(gotEmojiWithExt, ".png") {
		t.Errorf("sanitizeColabOneDownloadName(longEmojiWithExt) = %q, want .png preserved", gotEmojiWithExt)
	}
	assertColabOneDownloadNameInvariants(t, longEmojiWithExt, gotEmojiWithExt)

	longEmojiBare := strings.Repeat("🎨", 200)
	gotEmojiBare := sanitizeColabOneDownloadName(longEmojiBare)
	assertColabOneDownloadNameInvariants(t, longEmojiBare, gotEmojiBare)

	longCJKExt := "workflow." + strings.Repeat("画", 80)
	gotCJKExt := sanitizeColabOneDownloadName(longCJKExt)
	assertColabOneDownloadNameInvariants(t, longCJKExt, gotCJKExt)

	// Invalid UTF-8 bytes must be normalized into valid UTF-8 within the byte budget.
	invalidUTF8 := "img\xff\xfe" + strings.Repeat("画", 80) + ".png"
	gotInvalidUTF8 := sanitizeColabOneDownloadName(invalidUTF8)
	if !strings.HasSuffix(gotInvalidUTF8, ".png") {
		t.Errorf("sanitizeColabOneDownloadName(invalidUTF8) = %q, want .png preserved", gotInvalidUTF8)
	}
	assertColabOneDownloadNameInvariants(t, invalidUTF8, gotInvalidUTF8)

	// End-to-end check: a 500+-byte CJK name downloads cleanly into a real temp directory.
	dlDir := t.TempDir()
	cjkEv := &mygo.DownloadEvent{
		URL:           "https://comfy.example.com/view?filename=cjk.png",
		SuggestedName: longCJKPrefixed,
		Path:          filepath.Join(dlDir, longCJKPrefixed),
	}
	applyColabOneComfyDownload(cjkEv, "https://comfy.example.com")
	if cjkEv.DefaultPrevented() {
		t.Fatal("byte-capped CJK download was unexpectedly prevented")
	}
	if gotBase := filepath.Base(cjkEv.Path); gotBase != gotCJKPrefixed || len(gotBase) > maxDownloadNameBytes {
		t.Fatalf("byte-capped CJK download base = %q (len=%d), want %q", gotBase, len(gotBase), gotCJKPrefixed)
	}
	if err := os.WriteFile(cjkEv.Path, []byte("png"), 0o600); err != nil {
		t.Fatalf("failed to write sanitized CJK download path %q: %v", cjkEv.Path, err)
	}

	// Additional adversarial cases covering reserved-name prefix budget, spaces, dots, and UTF-8.
	adversarial := []string{
		"CON   .png",
		"CON." + strings.Repeat("b", 176),
		"CON." + strings.Repeat("b", 179),
		"CON." + strings.Repeat("画", 176),
		"CON." + strings.Repeat("画", 56),
		"CON." + strings.Repeat("画", 59),
		"CON." + strings.Repeat("b", 162) + "." + strings.Repeat("d", 9) + ".png",
		"CON." + strings.Repeat(" ", 175) + "x",
		"CON" + strings.Repeat(".", 176) + "x",
		"a" + strings.Repeat("🎨", 200) + ".png",
		"🎨." + strings.Repeat("画", 59),
		"lpt1.",
		".   NUL   .",
		"Aux.tar.gz",
		strings.Repeat("a", 179) + "." + strings.Repeat("b", 180),
		".   foo.png . ",
		"/foo/bar.png",
		`C:\Users\alice\CON.png`,
	}
	for _, raw := range adversarial {
		got := sanitizeColabOneDownloadName(raw)
		assertColabOneDownloadNameInvariants(t, raw, got)
	}
}

func TestComfyDownloadBlockedAndAllowedExtensions(t *testing.T) {
	const origin = "https://comfy.example.com"
	dlDir := t.TempDir()

	blockedExts := []string{
		".js", ".url", ".msix", ".appx", ".msixbundle", ".appxbundle",
		".desktop", ".sh", ".jar", ".scf", ".iso", ".vhd", ".vhdx",
		".scpt", ".terminal",
		".img", ".msp", ".chm", ".appref-ms", ".application", ".diagcab",
		".library-ms", ".search-ms", ".searchconnector-ms",
		".tool", ".workflow", ".action", ".inetloc", ".mpkg",
	}
	for _, ext := range blockedExts {
		for _, name := range []string{"payload" + ext, "PAYLOAD" + strings.ToUpper(ext) + ". "} {
			if allowColabOneComfyDownload("https://comfy.example.com/file", name, origin) {
				t.Errorf("allowColabOneComfyDownload accepted blocked extension %q (%q)", ext, name)
			}
			ev := &mygo.DownloadEvent{
				URL:           "blob:https://comfy.example.com/uuid",
				SuggestedName: name,
				Path:          filepath.Join(dlDir, name),
			}
			applyColabOneComfyDownload(ev, origin)
			if !ev.DefaultPrevented() {
				t.Errorf("applyColabOneComfyDownload did not prevent blocked extension %q (%q)", ext, name)
			}
		}
	}

	allowedExts := []string{".json", ".png", ".webp", ".mp4", ".wav"}
	for _, ext := range allowedExts {
		name := "output" + ext
		if !allowColabOneComfyDownload("https://comfy.example.com/view?filename="+name, name, origin) {
			t.Errorf("allowColabOneComfyDownload rejected allowed extension %q", ext)
		}
		ev := &mygo.DownloadEvent{
			URL:           "blob:https://comfy.example.com/uuid",
			SuggestedName: name,
			Path:          filepath.Join(dlDir, name),
		}
		applyColabOneComfyDownload(ev, origin)
		if ev.DefaultPrevented() {
			t.Errorf("applyColabOneComfyDownload prevented allowed extension %q", ext)
		}
	}
}

func TestUniqueColabOneDownloadPathStatErrorsAndPreventDefault(t *testing.T) {
	const origin = "https://comfy.example.com"
	dlDir := t.TempDir()

	// Normal collision numbering succeeds when os.ErrNotExist is reached.
	first := filepath.Join(dlDir, "render.png")
	second := filepath.Join(dlDir, "render (2).png")
	if err := os.WriteFile(first, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := uniqueColabOneDownloadPath(first)
	if err != nil {
		t.Fatalf("uniqueColabOneDownloadPath collision failed: %v", err)
	}
	if want := filepath.Join(dlDir, "render (3).png"); got != want {
		t.Fatalf("uniqueColabOneDownloadPath = %q, want %q", got, want)
	}

	// Overlong 300-byte component must return an error (ENAMETOOLONG / invalid name),
	// never treat a non-ErrNotExist stat failure as a free path.
	tooLongPath := filepath.Join(dlDir, strings.Repeat("a", 300)+".png")
	if got, err := uniqueColabOneDownloadPath(tooLongPath); err == nil {
		t.Fatalf("uniqueColabOneDownloadPath(%d-byte component) = %q, nil; want error", 304, got)
	} else if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uniqueColabOneDownloadPath(%d-byte component) returned ErrNotExist: %v", 304, err)
	}

	// applyColabOneComfyDownload must prevent download when e.Path fails stat with ENAMETOOLONG.
	tooLongEv := &mygo.DownloadEvent{
		URL:           "https://comfy.example.com/view?filename=render.png",
		SuggestedName: "render.png",
		Path:          tooLongPath,
	}
	applyColabOneComfyDownload(tooLongEv, origin)
	if !tooLongEv.DefaultPrevented() {
		t.Fatal("applyColabOneComfyDownload did not prevent download on overlong path stat error")
	}

	// Synthetic non-ErrNotExist stat errors on initial candidate and collision candidate.
	statPermErr := errors.New("simulated EACCES")
	if got, err := uniqueColabOneDownloadPathWith("/dl/a.png", func(string) (os.FileInfo, error) {
		return nil, statPermErr
	}); !errors.Is(err, statPermErr) || got != "" {
		t.Fatalf("initial non-ErrNotExist stat error: got (%q, %v), want (\"\", %v)", got, err, statPermErr)
	}

	statTooLongErr := errors.New("simulated ENAMETOOLONG on suffix")
	if got, err := uniqueColabOneDownloadPathWith("/dl/a.png", func(p string) (os.FileInfo, error) {
		if p == "/dl/a.png" {
			return nil, nil
		}
		return nil, statTooLongErr
	}); !errors.Is(err, statTooLongErr) || got != "" {
		t.Fatalf("collision non-ErrNotExist stat error: got (%q, %v), want (\"\", %v)", got, err, statTooLongErr)
	}

	// Real EACCES test on Unix when not running as root.
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		lockedDir := filepath.Join(dlDir, "locked")
		if err := os.Mkdir(lockedDir, 0o000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(lockedDir, 0o700)

		if got, err := uniqueColabOneDownloadPath(filepath.Join(lockedDir, "out.png")); err == nil || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("uniqueColabOneDownloadPath on 0000 dir = (%q, %v), want permission error", got, err)
		}

		// Dirty suggested name enters uniqueColabOneDownloadPath after os.MkdirAll returns nil on existing dir.
		dirtyLockedEv := &mygo.DownloadEvent{
			URL:           "https://comfy.example.com/view?filename=out.png",
			SuggestedName: "bad:out.png",
			Path:          filepath.Join(lockedDir, "bad:out.png"),
		}
		applyColabOneComfyDownload(dirtyLockedEv, origin)
		if !dirtyLockedEv.DefaultPrevented() {
			t.Fatal("applyColabOneComfyDownload did not prevent dirty download in unreadable directory")
		}

		cleanLockedEv := &mygo.DownloadEvent{
			URL:           "https://comfy.example.com/view?filename=out.png",
			SuggestedName: "out.png",
			Path:          filepath.Join(lockedDir, "out.png"),
		}
		applyColabOneComfyDownload(cleanLockedEv, origin)
		if !cleanLockedEv.DefaultPrevented() {
			t.Fatal("applyColabOneComfyDownload did not prevent clean download in unreadable directory")
		}
	}
}

func TestComfyCanvasEphemeralSessionAndOriginIsolation(t *testing.T) {
	opts1 := colabOneComfyCanvasPageOptions()
	opts2 := colabOneComfyCanvasPageOptions()
	if !opts1.Untrusted || !opts2.Untrusted {
		t.Fatalf("canvas page options must set Untrusted=true: opts1=%+v opts2=%+v", opts1, opts2)
	}
	if opts1.Session == nil || opts2.Session == nil {
		t.Fatal("canvas page options must attach a non-nil ephemeral session")
	}
	if opts1.Session == mygo.DefaultSession() || opts2.Session == mygo.DefaultSession() {
		t.Fatal("canvas page options must never reuse mygo.DefaultSession()")
	}
	if !opts1.Session.IsEphemeral() || !opts2.Session.IsEphemeral() {
		t.Fatal("canvas page session must be ephemeral")
	}
	if opts1.Session == opts2.Session {
		t.Fatal("each canvas window must receive a fresh ephemeral session instance")
	}

	// Verify window identity checks prevent an old window's delayed OnClosed or
	// late navigation/download callbacks from clobbering or riding a replacement window.
	d := &ColabOneDesktop{}
	oldWin := new(mygo.Window)
	newWin := new(mygo.Window)

	d.comfyWin = oldWin
	d.comfyOrigin = "https://old-runtime.example.com"
	if got := d.activeComfyOrigin(oldWin); got != "https://old-runtime.example.com" {
		t.Fatalf("activeComfyOrigin(oldWin) = %q, want old origin", got)
	}

	// Simulate cross-origin replacement: old window state cleared, then new window installed.
	d.clearComfyWindowIfMatch(oldWin)
	if d.comfyWin != nil || d.comfyOrigin != "" {
		t.Fatalf("clearComfyWindowIfMatch(oldWin) did not clear state: win=%p origin=%q", d.comfyWin, d.comfyOrigin)
	}
	d.comfyWin = newWin
	d.comfyOrigin = "https://new-runtime.example.com"

	// Delayed OnClosed from oldWin must not clobber newWin.
	d.clearComfyWindowIfMatch(oldWin)
	if d.comfyWin != newWin || d.comfyOrigin != "https://new-runtime.example.com" {
		t.Fatalf("delayed OnClosed for oldWin clobbered newWin: win=%p origin=%q", d.comfyWin, d.comfyOrigin)
	}
	// Late callbacks from oldWin must see empty origin and fail closed.
	if got := d.activeComfyOrigin(oldWin); got != "" {
		t.Fatalf("activeComfyOrigin(oldWin) after replacement = %q, want empty", got)
	}
	if allowColabOneComfyNavigate("https://old-runtime.example.com/", d.activeComfyOrigin(oldWin)) {
		t.Fatal("late navigation on replaced oldWin was unexpectedly allowed")
	}
	if got := d.activeComfyOrigin(newWin); got != "https://new-runtime.example.com" {
		t.Fatalf("activeComfyOrigin(newWin) = %q, want new origin", got)
	}

	// Closing newWin clears both fields.
	d.clearComfyWindowIfMatch(newWin)
	if d.comfyWin != nil || d.comfyOrigin != "" {
		t.Fatalf("clearComfyWindowIfMatch(newWin) did not clear state: win=%p origin=%q", d.comfyWin, d.comfyOrigin)
	}
}

func TestApplyColabOneComfyDownloadDoesNotEscapeDownloadsDir(t *testing.T) {
	const origin = "https://comfy.example.com"
	root := t.TempDir()
	dlDir := filepath.Join(root, "Downloads")
	if err := os.MkdirAll(dlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mygo.App.SetPath(mygo.PathDownloads, dlDir)
	defer mygo.App.SetPath(mygo.PathDownloads, "")

	// Simulate Windows mygo behavior where empty/root SuggestedName produces a sibling
	// path like <parent>/Downloads (2) or <parent>/Downloads outside PathDownloads.
	siblingPath := filepath.Join(root, "Downloads (2)")
	for _, tc := range []struct {
		name          string
		suggestedName string
		initialPath   string
		wantBase      string
	}{
		{"empty_with_sibling_path", "", siblingPath, "download"},
		{"slash_with_sibling_path", "/", siblingPath, "download"},
		{"backslash_with_sibling_path", `\`, siblingPath, "download"},
		{"backslash_with_downloads_dir_path", `\`, dlDir, "download"},
		{"dot_with_sibling_path", ".", siblingPath, "download"},
		{"dotdot_with_parent_path", "..", root, "download"},
		{"traversal_with_escaped_path", "../evil.png", filepath.Join(root, "evil.png"), "evil.png"},
		{"relative_path_falls_back_to_downloads", "workflow.json", "workflow.json", "workflow.json"},
		{"empty_path_falls_back_to_downloads", `\`, "", "download"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := &mygo.DownloadEvent{
				URL:           "blob:https://comfy.example.com/1234",
				SuggestedName: tc.suggestedName,
				Path:          tc.initialPath,
			}
			applyColabOneComfyDownload(ev, origin)
			if ev.DefaultPrevented() {
				t.Fatalf("download was unexpectedly prevented")
			}
			if gotDir := filepath.Dir(ev.Path); gotDir != dlDir {
				t.Fatalf("download escaped PathDownloads: dir=%q, want %q (full path=%q)", gotDir, dlDir, ev.Path)
			}
			if gotBase := filepath.Base(ev.Path); gotBase != tc.wantBase {
				t.Fatalf("download base = %q, want %q (full path=%q)", gotBase, tc.wantBase, ev.Path)
			}
		})
	}
}

func TestApplyColabOneComfyDownloadDoesNotOverwriteExistingCleanPath(t *testing.T) {
	const origin = "https://comfy.example.com"
	dlDir := t.TempDir()

	// Clean SuggestedName whose e.Path already exists on disk must be re-numbered
	// rather than left pointing at the existing file (which would silently overwrite it).
	existing := filepath.Join(dlDir, "render.png")
	if err := os.WriteFile(existing, []byte("original-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev := &mygo.DownloadEvent{
		URL:           "https://comfy.example.com/view?filename=render.png",
		SuggestedName: "render.png",
		Path:          existing,
	}
	applyColabOneComfyDownload(ev, origin)
	if ev.DefaultPrevented() {
		t.Fatal("clean download colliding with existing file was unexpectedly prevented")
	}
	want := filepath.Join(dlDir, "render (2).png")
	if ev.Path == existing || ev.Path != want {
		t.Fatalf("colliding clean download path = %q, want %q (must not point at existing %q)", ev.Path, want, existing)
	}

	// Subsequent collision when "render (2).png" also exists skips to "render (3).png".
	if err := os.WriteFile(want, []byte("second-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev2 := &mygo.DownloadEvent{
		URL:           "https://comfy.example.com/view?filename=render.png",
		SuggestedName: "render.png",
		Path:          existing,
	}
	applyColabOneComfyDownload(ev2, origin)
	if ev2.DefaultPrevented() {
		t.Fatal("second clean download collision was unexpectedly prevented")
	}
	if want3 := filepath.Join(dlDir, "render (3).png"); ev2.Path != want3 {
		t.Fatalf("second colliding clean download path = %q, want %q", ev2.Path, want3)
	}

	// Non-existing clean path is preserved unchanged.
	fresh := filepath.Join(dlDir, "fresh.png")
	freshEv := &mygo.DownloadEvent{
		URL:           "https://comfy.example.com/view?filename=fresh.png",
		SuggestedName: "fresh.png",
		Path:          fresh,
	}
	applyColabOneComfyDownload(freshEv, origin)
	if freshEv.DefaultPrevented() || freshEv.Path != fresh {
		t.Fatalf("non-existing clean download modified unexpectedly: prevented=%v path=%q want %q", freshEv.DefaultPrevented(), freshEv.Path, fresh)
	}

	// Existing dangling symlink at e.Path must also be detected via os.Lstat and re-numbered.
	if runtime.GOOS != "windows" {
		linkPath := filepath.Join(dlDir, "dangling.png")
		if err := os.Symlink(filepath.Join(dlDir, "missing-target.png"), linkPath); err != nil {
			t.Fatal(err)
		}
		linkEv := &mygo.DownloadEvent{
			URL:           "https://comfy.example.com/view?filename=dangling.png",
			SuggestedName: "dangling.png",
			Path:          linkPath,
		}
		applyColabOneComfyDownload(linkEv, origin)
		if linkEv.DefaultPrevented() {
			t.Fatal("clean download colliding with dangling symlink was unexpectedly prevented")
		}
		if wantLink := filepath.Join(dlDir, "dangling (2).png"); linkEv.Path != wantLink {
			t.Fatalf("dangling symlink collision path = %q, want %q", linkEv.Path, wantLink)
		}
	}
}

func TestClampColabOneDownloadNameForDir(t *testing.T) {
	utf16Len := func(s string) int {
		return len(utf16.Encode([]rune(s)))
	}

	deepDir := `C:\Users\very.long.username\OneDrive - Example Corporation\Documents\ComfyUI\Downloads`
	if n := utf16Len(deepDir); n < 80 {
		t.Fatalf("test setup expected deepDir >= 80 UTF-16 units, got %d", n)
	}

	cases := []struct {
		name     string
		safeName string
		wantExt  string
	}{
		{"ascii_180", strings.Repeat("a", 176) + ".png", ".png"},
		{"cjk_180", strings.Repeat("画", 176) + ".png", ".png"},
		{"emoji_180", strings.Repeat("🎨", 176) + ".png", ".png"},
		{"sanitized_cjk", sanitizeColabOneDownloadName(strings.Repeat("画", 176) + ".png"), ".png"},
		{"sanitized_emoji", sanitizeColabOneDownloadName(strings.Repeat("🎨", 176) + ".png"), ".png"},
		{"mixed_unicode", strings.Repeat("a画🎨", 60) + ".png", ".png"},
		{"reserved_after_clamp", "CON" + strings.Repeat(" ", 140) + "x.png", ".png"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, ok := clampColabOneDownloadNameForDir(tc.safeName, deepDir, true)
			if !ok {
				t.Fatalf("clampColabOneDownloadNameForDir(%q, deepDir, true) returned ok=false", tc.name)
			}
			if !utf8.ValidString(out) {
				t.Fatalf("clamped name is not valid UTF-8: %q", out)
			}
			if !strings.HasSuffix(out, tc.wantExt) {
				t.Fatalf("clamped name %q lost extension %q", out, tc.wantExt)
			}
			total := utf16Len(deepDir + `\` + out)
			if total > 228 {
				t.Fatalf("total UTF-16 path length = %d (> 228): dir=%d name=%d (%q)", total, utf16Len(deepDir), utf16Len(out), out)
			}
			if withSuffix := utf16Len(deepDir + `\` + out + " (999).crdownload"); withSuffix >= 248 {
				t.Fatalf("path with collision + .crdownload = %d (>= 248)", withSuffix)
			}
			if isWindowsReservedDownloadName(out) {
				t.Fatalf("clamped name %q is a Win32 reserved device name", out)
			}
			if isBlockedComfyDownloadExt(out) {
				t.Fatalf("clamped name %q has a blocked extension", out)
			}
			first, _ := utf8.DecodeRuneInString(out)
			last, _ := utf8.DecodeLastRuneInString(out)
			if first == '.' || unicode.IsSpace(first) || last == '.' || unicode.IsSpace(last) {
				t.Fatalf("clamped name %q has leading/trailing dot or space", out)
			}
			if again, ok2 := clampColabOneDownloadNameForDir(out, deepDir, true); !ok2 || again != out {
				t.Fatalf("clampColabOneDownloadNameForDir is not idempotent: first=%q second=(%q, %v)", out, again, ok2)
			}
		})
	}

	// Exact budget boundary: 240 - dirLen - 1 - 12 == 16 succeeds; == 15 fails.
	exactBudgetDir := `C:\` + strings.Repeat("d", 208) // 3 + 208 = 211 UTF-16 units -> budget = 16
	if got, ok := clampColabOneDownloadNameForDir(strings.Repeat("a", 50)+".png", exactBudgetDir, true); !ok {
		t.Fatalf("budget=16 unexpectedly failed")
	} else {
		if !strings.HasSuffix(got, ".png") || utf16Len(exactBudgetDir+`\`+got) > 228 {
			t.Fatalf("budget=16 returned unexpected name %q (total=%d)", got, utf16Len(exactBudgetDir+`\`+got))
		}
	}

	// Reserved name under tight budget=16 must still be prefixed and stay within budget.
	if got, ok := clampColabOneDownloadNameForDir("CON.abcdefghijkl", exactBudgetDir, true); !ok {
		t.Fatalf("reserved name at budget=16 unexpectedly failed")
	} else {
		if isWindowsReservedDownloadName(got) || !strings.HasPrefix(got, reservedDownloadPrefix) || utf16Len(exactBudgetDir+`\`+got) > 228 {
			t.Fatalf("reserved name at budget=16 returned unsafe name %q (total=%d)", got, utf16Len(exactBudgetDir+`\`+got))
		}
	}

	tooDeepDir := `C:\` + strings.Repeat("d", 209) // 212 UTF-16 units -> budget = 15 < 16
	if got, ok := clampColabOneDownloadNameForDir("output.png", tooDeepDir, true); ok || got != "" {
		t.Fatalf("budget=15 returned (%q, %v), want (\"\", false)", got, ok)
	}

	// Surrogate-pair emoji in dir counts as 2 UTF-16 units per rune.
	emojiTooDeepDir := `C:\` + strings.Repeat("🎨", 105) // 3 + 210 = 213 UTF-16 units -> budget = 14 < 16
	if got, ok := clampColabOneDownloadNameForDir("output.png", emojiTooDeepDir, true); ok || got != "" {
		t.Fatalf("emoji dir with budget=14 returned (%q, %v), want (\"\", false)", got, ok)
	}

	// Blocked extension revealed only after Windows MAX_PATH clamping must be rejected.
	budget45Dir := `C:\` + strings.Repeat("d", 179) // 182 UTF-16 units -> budget = 45
	hiddenExe := strings.Repeat("a", 40) + ".exe." + strings.Repeat("b", 120)
	if got, ok := clampColabOneDownloadNameForDir(hiddenExe, budget45Dir, true); ok || got != "" {
		t.Fatalf("clamping-exposed .exe returned (%q, %v), want (\"\", false)", got, ok)
	}

	// Non-Windows leaves valid names unclamped regardless of dir length.
	longName := strings.Repeat("a", 176) + ".png"
	if got, ok := clampColabOneDownloadNameForDir(longName, tooDeepDir, false); !ok || got != longName {
		t.Fatalf("non-Windows clamp returned (%q, %v), want (%q, true)", got, ok, longName)
	}

	// End-to-end check: when Windows MAX_PATH clamping shortens a clean SuggestedName,
	// applyColabOneComfyDownloadForOS must bypass the fast path and compute a unique clamped path.
	const origin = "https://comfy.example.com"
	baseTemp := t.TempDir()
	padLen := 170 - utf16Len(baseTemp) - 1
	if padLen < 1 {
		padLen = 1
	}
	deepTempDir := filepath.Join(baseTemp, strings.Repeat("d", padLen))
	if err := os.MkdirAll(deepTempDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cleanLongName := strings.Repeat("a", 160) + ".png"
	winEv := &mygo.DownloadEvent{
		URL:           "https://comfy.example.com/view?filename=long.png",
		SuggestedName: cleanLongName,
		Path:          filepath.Join(deepTempDir, cleanLongName),
	}
	applyColabOneComfyDownloadForOS(winEv, origin, true)
	if winEv.DefaultPrevented() {
		t.Fatal("clamped Windows download was unexpectedly prevented")
	}
	if filepath.Dir(winEv.Path) != deepTempDir {
		t.Fatalf("clamped Windows download dir = %q, want %q", filepath.Dir(winEv.Path), deepTempDir)
	}
	if filepath.Base(winEv.Path) == cleanLongName {
		t.Fatalf("clamped Windows download unexpectedly took fast path without shortening: %q", winEv.Path)
	}
	if !strings.HasSuffix(winEv.Path, ".png") || utf16Len(winEv.Path) > 228 {
		t.Fatalf("clamped Windows download path invalid: %q (utf16=%d)", winEv.Path, utf16Len(winEv.Path))
	}
	if err := os.WriteFile(winEv.Path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Second download of the same long name must re-number the clamped target instead of overwriting.
	winEv2 := &mygo.DownloadEvent{
		URL:           "https://comfy.example.com/view?filename=long.png",
		SuggestedName: cleanLongName,
		Path:          filepath.Join(deepTempDir, cleanLongName),
	}
	applyColabOneComfyDownloadForOS(winEv2, origin, true)
	if winEv2.DefaultPrevented() || winEv2.Path == winEv.Path || !strings.HasSuffix(winEv2.Path, " (2).png") {
		t.Fatalf("second clamped Windows download = (prevented=%v, path=%q), want re-numbered copy of %q", winEv2.DefaultPrevented(), winEv2.Path, winEv.Path)
	}

	// End-to-end check: dir leaving < 16 UTF-16 units prevents download on Windows.
	tooDeepTempDir := filepath.Join(baseTemp, strings.Repeat("x", 215))
	tooDeepEv := &mygo.DownloadEvent{
		URL:           "https://comfy.example.com/view?filename=out.png",
		SuggestedName: "out.png",
		Path:          filepath.Join(tooDeepTempDir, "out.png"),
	}
	applyColabOneComfyDownloadForOS(tooDeepEv, origin, true)
	if !tooDeepEv.DefaultPrevented() {
		t.Fatal("Windows download with dir budget < 16 was not prevented")
	}
}

func TestSanitizeColabOneCanvasErrorRedactsSecrets(t *testing.T) {
	secret := "super-secret-canvas-token-987654"
	rawErr := errors.New("shell open failed for https://comfy.example.com/_colabone_auth?token=" + secret + " (raw " + secret + ")")
	safeErr := sanitizeColabOneCanvasError(rawErr, secret)
	if safeErr == nil {
		t.Fatal("expected non-nil sanitized error")
	}
	if strings.Contains(safeErr.Error(), secret) || strings.Contains(safeErr.Error(), "comfy.example.com") {
		t.Fatalf("sanitized error leaked secret or URL: %q", safeErr.Error())
	}
}

func TestColabOneBridgeCloseWithTimeoutSlowClose(t *testing.T) {
	cfgDir := t.TempDir()
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	closeDone := make(chan struct{})

	b := &colabOneBridge{
		factory: func() *controller.ColabOneController {
			return newColabOneWiredController("", cfgDir, vault.NewMemoryVault(), false)
		},
		closeCtrl: func(c *controller.ColabOneController) {
			close(closeStarted)
			<-releaseClose
			c.Close()
			close(closeDone)
		},
	}

	if err := b.start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	start := time.Now()
	b.closeWithTimeout(40 * time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 400*time.Millisecond {
		t.Fatalf("closeWithTimeout blocked too long on slow Close(): %v", elapsed)
	}
	select {
	case <-closeStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected closeCtrl to be invoked")
	}

	close(releaseClose)
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("closeCtrl goroutine did not exit after release")
	}
}

func TestColabOneBridgeCloseWithTimeoutBlockedFactoryAndLateCleanup(t *testing.T) {
	cfgDir := t.TempDir()
	inFactory := make(chan struct{})
	releaseFactory := make(chan struct{})
	lateClosed := make(chan struct{})

	b := &colabOneBridge{
		factory: func() *controller.ColabOneController {
			close(inFactory)
			<-releaseFactory
			return newColabOneWiredController("", cfgDir, vault.NewMemoryVault(), false)
		},
		closeCtrl: func(c *controller.ColabOneController) {
			c.Close()
			close(lateClosed)
		},
	}

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- b.start()
	}()

	select {
	case <-inFactory:
	case <-time.After(2 * time.Second):
		t.Fatal("factory was not entered")
	}

	// Second caller waits on single-flight startup without blocking close().
	waiterErrCh := make(chan error, 1)
	go func() {
		_, err := b.call(context.Background(), "snapshot", nil)
		waiterErrCh <- err
	}()
	time.Sleep(15 * time.Millisecond)

	start := time.Now()
	b.closeWithTimeout(40 * time.Millisecond)
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Fatalf("closeWithTimeout blocked while factory was in flight: %v", elapsed)
	}

	// Waiting caller must unblock promptly with ErrBridgeClosed even before factory finishes.
	select {
	case err := <-waiterErrCh:
		if !errors.Is(err, ErrBridgeClosed) {
			t.Fatalf("waiter error = %v, want ErrBridgeClosed", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("single-flight waiter stayed blocked after closeWithTimeout")
	}

	// Release factory after close(): start() must return ErrBridgeClosed and clean up controller.
	close(releaseFactory)
	select {
	case err := <-startErrCh:
		if !errors.Is(err, ErrBridgeClosed) {
			t.Fatalf("start error = %v, want ErrBridgeClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("start() did not return after factory unblocked")
	}

	select {
	case <-lateClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("controller constructed after close() was not cleaned up")
	}
}

func TestColabOneBridgeSingleFlightDeduplicatesAndRespectsCancel(t *testing.T) {
	cfgDir := t.TempDir()
	inFactory := make(chan struct{})
	releaseFactory := make(chan struct{})
	var calls atomic.Int32

	b := &colabOneBridge{
		factory: func() *controller.ColabOneController {
			calls.Add(1)
			close(inFactory)
			<-releaseFactory
			return newColabOneWiredController("", cfgDir, vault.NewMemoryVault(), false)
		},
	}
	defer b.close()

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := b.call(context.Background(), "snapshot", nil)
			errs <- err
		}()
	}

	select {
	case <-inFactory:
	case <-time.After(2 * time.Second):
		t.Fatal("factory was not entered")
	}

	// A caller whose context cancels while waiting on single-flight init must return promptly.
	cancelCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := b.call(cancelCtx, "snapshot", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}

	close(releaseFactory)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent snapshot call failed: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("factory invoked %d times, want 1", got)
	}
}
