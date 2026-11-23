package drivemount

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testConsentURI = "https://accounts.google.com/o/oauth2/v2/auth?client_id=947318989803"

type serverAuthorizedClient struct {
	domain string
	http   *http.Client
}

func (c *serverAuthorizedClient) ColabDomain() string {
	return c.domain
}

func (c *serverAuthorizedClient) DoAuthorized(ctx context.Context, req *http.Request) (*http.Response, error) {
	hc := c.http
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(req.WithContext(ctx))
}

type funcAuthorizedClient struct {
	domain string
	do     func(ctx context.Context, req *http.Request) (*http.Response, error)
}

func (c *funcAuthorizedClient) ColabDomain() string { return c.domain }

func (c *funcAuthorizedClient) DoAuthorized(ctx context.Context, req *http.Request) (*http.Response, error) {
	return c.do(ctx, req)
}

func makeResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type logEntry struct {
	msg    string
	source string
	level  string
}

func TestColabOneDriveMountConstantsAndDefaults(t *testing.T) {
	if COLABONE_DRIVEFS_MOUNT_ROOT != "/content/drive" {
		t.Fatalf("unexpected mount root: %q", COLABONE_DRIVEFS_MOUNT_ROOT)
	}
	if COLABONE_DRIVEFS_MYDRIVE != "/content/drive/MyDrive" {
		t.Fatalf("unexpected MyDrive path: %q", COLABONE_DRIVEFS_MYDRIVE)
	}

	m := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Endpoint:     "ep-1",
		AccountEmail: "  alice@example.com  ",
	})
	if m.timeout != 240*time.Second {
		t.Fatalf("expected default timeout 240s, got %v", m.timeout)
	}
	if m.pollInterval != 2500*time.Millisecond {
		t.Fatalf("expected default pollInterval 2.5s, got %v", m.pollInterval)
	}
	if m.accountEmail != "alice@example.com" {
		t.Fatalf("expected trimmed email, got %q", m.accountEmail)
	}
	if got := m.endpointURL(true); !strings.HasPrefix(got, "https://colab.research.google.com/tun/m/credentials-propagation/ep-1?") {
		t.Fatalf("unexpected default endpoint URL: %q", got)
	}

	clamped := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Timeout:      2 * time.Second,
		PollInterval: 5 * time.Millisecond,
	})
	if clamped.timeout != 15*time.Second {
		t.Fatalf("expected clamped min timeout 15s, got %v", clamped.timeout)
	}
	if clamped.pollInterval != 50*time.Millisecond {
		t.Fatalf("expected clamped min pollInterval 50ms, got %v", clamped.pollInterval)
	}
}

func TestCredentialsPropagationHookAndFreshXSRFTokens(t *testing.T) {
	var mu sync.Mutex
	getCounter := 0
	var postDryruns []string
	var postTokens []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tun/m/credentials-propagation/gpu-t4-test" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("X-Colab-Client-Agent") != "colab-cli" {
			t.Errorf("missing X-Colab-Client-Agent header: %q", r.Header.Get("X-Colab-Client-Agent"))
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("missing Accept header: %q", r.Header.Get("Accept"))
		}
		q := r.URL.Query()
		if q.Get("authuser") != "0" || q.Get("authtype") != "dfs_ephemeral" ||
			q.Get("version") != "2" || q.Get("propagate") != "true" || q.Get("record") != "false" {
			t.Errorf("unexpected query params: %v", q)
		}

		if r.Method == http.MethodGet {
			mu.Lock()
			getCounter++
			tok := "xsrf-tok-" + string(rune('0'+getCounter))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(")]}'\n" + `{"token":"` + tok + `"}`))
			return
		}

		if r.Method == http.MethodPost {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("expected multipart form body, got err: %v", err)
			} else {
				if got := r.FormValue("file_id"); got != "empty.ipynb" {
					t.Errorf("expected multipart file_id=empty.ipynb, got %q", got)
				}
				if len(r.MultipartForm.File) != 0 {
					t.Errorf("expected field-only multipart (no filename), got files: %v", r.MultipartForm.File)
				}
			}

			mu.Lock()
			postDryruns = append(postDryruns, q.Get("dryrun"))
			postTokens = append(postTokens, r.Header.Get("X-Goog-Colab-Token"))
			n := len(postDryruns)
			mu.Unlock()

			switch n {
			case 1:
				if q.Get("dryrun") != "true" {
					t.Errorf("expected first POST dryrun=true, got %q", q.Get("dryrun"))
				}
				body, _ := json.Marshal(map[string]any{
					"success":                   false,
					"unauthorized_redirect_uri": testConsentURI,
				})
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(append([]byte(")]}'\n"), body...))
			case 2:
				// First dryrun=false poll while user is still consenting returns HTTP 400 HTML.
				if q.Get("dryrun") != "false" {
					t.Errorf("expected second POST dryrun=false, got %q", q.Get("dryrun"))
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("<!DOCTYPE html><title>Error 400 (Bad Request)</title>"))
			default:
				if q.Get("dryrun") != "false" {
					t.Errorf("expected subsequent POST dryrun=false, got %q", q.Get("dryrun"))
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(")]}'\n" + `{"success":true}`))
			}
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	var urlMu sync.Mutex
	var seenURLs []string
	replyCh := make(chan map[string]any, 1)

	helper := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:   &serverAuthorizedClient{domain: srv.URL, http: srv.Client()},
		Endpoint: "gpu-t4-test",
		OnAuthURL: func(u string) {
			urlMu.Lock()
			seenURLs = append(seenURLs, u)
			urlMu.Unlock()
		},
		PollInterval: 50 * time.Millisecond,
	})

	handled := helper.HandleColabRequest(
		context.Background(),
		"colab_request",
		map[string]any{"request": map[string]any{"authType": "dfs_ephemeral"}},
		map[string]any{"colab_msg_id": "cm-1"},
		func(payload map[string]any) error {
			replyCh <- payload
			return nil
		},
	)
	if !handled {
		t.Fatalf("expected HandleColabRequest to intercept dfs_ephemeral")
	}

	select {
	case reply := <-replyCh:
		if reply["type"] != "colab_reply" {
			t.Fatalf("expected type=colab_reply, got %#v", reply)
		}
		if reply["colab_msg_id"] != "cm-1" {
			t.Fatalf("expected colab_msg_id=cm-1, got %#v", reply)
		}
		if _, hasErr := reply["error"]; hasErr {
			t.Fatalf("expected no error field on success reply, got %#v", reply)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for colab_reply")
	}

	if helper.Error() != "" {
		t.Fatalf("expected empty Error(), got %q", helper.Error())
	}

	urlMu.Lock()
	gotURLs := append([]string(nil), seenURLs...)
	urlMu.Unlock()
	if len(gotURLs) != 2 || gotURLs[0] != testConsentURI || gotURLs[1] != "" {
		t.Fatalf("expected seenURLs=[consentURI, \"\"], got %#v", gotURLs)
	}

	mu.Lock()
	gotTokens := append([]string(nil), postTokens...)
	mu.Unlock()
	wantTokens := []string{"xsrf-tok-1", "xsrf-tok-2", "xsrf-tok-3"}
	if len(gotTokens) != len(wantTokens) {
		t.Fatalf("expected tokens %v, got %v", wantTokens, gotTokens)
	}
	for i := range wantTokens {
		if gotTokens[i] != wantTokens[i] {
			t.Fatalf("token[%d]: want %q, got %q", i, wantTokens[i], gotTokens[i])
		}
	}
}

func TestConfirmAndProbeWakeImmediatelyWithAccountEmailHint(t *testing.T) {
	var step atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"token":"fresh-tok"}`))
			return
		}
		n := step.Add(1)
		switch n {
		case 1:
			body, _ := json.Marshal(map[string]any{
				"success":                   false,
				"unauthorized_redirect_uri": testConsentURI,
			})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		case 2:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<html>400</html>"))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()

	var mu sync.Mutex
	var hints []string
	var logs []logEntry
	var helper *ColabOneDriveMount

	helper = NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:       &serverAuthorizedClient{domain: srv.URL, http: srv.Client()},
		Endpoint:     "gpu-t4-test",
		AccountEmail: "alice@example.com",
		PollInterval: 30 * time.Second, // Long poll interval proves SignalConfirm & SignalProbe wake immediately.
		OnAuthURL: func(u string) {
			if u != "" {
				helper.SignalConfirm()
			}
		},
		OnHint: func(h string) {
			mu.Lock()
			hints = append(hints, h)
			mu.Unlock()
			if h != "" {
				helper.SignalProbe()
			}
		},
		OnLog: func(msg, source, level string) {
			mu.Lock()
			logs = append(logs, logEntry{msg: msg, source: source, level: level})
			mu.Unlock()
		},
	})

	start := time.Now()
	ok, err := helper.Propagate(context.Background())
	elapsed := time.Since(start)
	if err != nil || !ok {
		t.Fatalf("expected Propagate success, got ok=%v err=%v", ok, err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("expected immediate wakeup on confirm/probe, took %v", elapsed)
	}

	mu.Lock()
	defer mu.Unlock()
	wantHint := "暂未检测到 alice@example.com 的云盘授权确认：请确认浏览器中选择了同一 Google 账号，并在授权页勾选全部云盘权限后重试"
	foundHint := false
	for _, h := range hints {
		if h == wantHint {
			foundHint = true
			break
		}
	}
	if !foundHint {
		t.Fatalf("expected exact email hint %q in hints=%#v", wantHint, hints)
	}
	if len(hints) == 0 || hints[len(hints)-1] != "" {
		t.Fatalf("expected final hint to be cleared, got %#v", hints)
	}

	foundWarnLog := false
	foundEmailPromptLog := false
	for _, entry := range logs {
		if entry.msg == wantHint && entry.source == "云盘" && entry.level == "warning" {
			foundWarnLog = true
		}
		if entry.msg == "请在浏览器中使用 alice@example.com 勾选全部云盘权限并点击允许（仅本实例确认一次）" && entry.source == "云盘" && entry.level == "info" {
			foundEmailPromptLog = true
		}
	}
	if !foundWarnLog {
		t.Fatalf("expected warning log for unconfirmed hint, got logs=%#v", logs)
	}
	if !foundEmailPromptLog {
		t.Fatalf("expected browser consent prompt log with email, got logs=%#v", logs)
	}
}

func TestAlreadyAuthorizedFastPath(t *testing.T) {
	var mu sync.Mutex
	var getDryruns []string
	type postCall struct {
		dryrun string
		token  string
	}
	var postCalls []postCall

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dryrun := r.URL.Query().Get("dryrun")
		if r.Method == http.MethodGet {
			mu.Lock()
			getDryruns = append(getDryruns, dryrun)
			tok := "tok-" + string(rune('0'+len(getDryruns)))
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"token":"` + tok + `"}`))
			return
		}
		mu.Lock()
		postCalls = append(postCalls, postCall{dryrun: dryrun, token: r.Header.Get("X-Goog-Colab-Token")})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	var fastURLs []string
	var logs []logEntry
	m := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:   &serverAuthorizedClient{domain: srv.URL, http: srv.Client()},
		Endpoint: "gpu-t4-test",
		OnAuthURL: func(u string) {
			fastURLs = append(fastURLs, u)
		},
		OnLog: func(msg, source, level string) {
			logs = append(logs, logEntry{msg: msg, source: source, level: level})
		},
	})

	ok, err := m.Propagate(context.Background())
	if err != nil || !ok {
		t.Fatalf("expected fast-path success, got ok=%v err=%v", ok, err)
	}
	if len(fastURLs) != 0 {
		t.Fatalf("expected no auth URL emitted on fast path, got %#v", fastURLs)
	}
	if len(getDryruns) != 2 || getDryruns[0] != "true" || getDryruns[1] != "false" {
		t.Fatalf("expected GET dryruns [true false], got %v", getDryruns)
	}
	wantPosts := []postCall{
		{dryrun: "true", token: "tok-1"},
		{dryrun: "false", token: "tok-2"},
	}
	if len(postCalls) != 2 || postCalls[0] != wantPosts[0] || postCalls[1] != wantPosts[1] {
		t.Fatalf("expected POST calls %+v, got %+v", wantPosts, postCalls)
	}
	if len(logs) != 2 ||
		logs[0].msg != "已收到云盘授权，正在将凭据同步到远端 GPU 实例" ||
		logs[1].msg != "云盘凭据已同步至远端实例，正在完成 DriveFS 挂载" {
		t.Fatalf("unexpected fast-path logs: %#v", logs)
	}
}

func TestPhase1bFallbackToPhase2AndOmittedSuccess(t *testing.T) {
	// Case 1: Phase 1 dryrun=true says success=true, Phase 1b dryrun=false returns a redirect URI -> falls back to Phase 2.
	var step atomic.Int32
	client := &funcAuthorizedClient{
		domain: "https://colab.research.google.com",
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"t"}`), nil
			}
			switch step.Add(1) {
			case 1:
				return makeResponse(http.StatusOK, `{"success":true}`), nil
			case 2:
				return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
			default:
				// Omit `success` on HTTP 200 with no redirect URI -> treated as success.
				return makeResponse(http.StatusOK, `{"status":"ok"}`), nil
			}
		},
	}

	var seenURLs []string
	m := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:       client,
		Endpoint:     "ep-fb",
		PollInterval: 50 * time.Millisecond,
		OnAuthURL: func(u string) {
			seenURLs = append(seenURLs, u)
		},
	})

	ok, err := m.Propagate(context.Background())
	if err != nil || !ok {
		t.Fatalf("expected fallback to Phase 2 to succeed, got ok=%v err=%v", ok, err)
	}
	if len(seenURLs) != 2 || seenURLs[0] != testConsentURI || seenURLs[1] != "" {
		t.Fatalf("expected seenURLs=[consentURI, \"\"], got %#v", seenURLs)
	}

	// Case 2: Phase 1b retries once on transient HTTP 503 then succeeds.
	var step2 atomic.Int32
	client2 := &funcAuthorizedClient{
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"t"}`), nil
			}
			switch step2.Add(1) {
			case 1:
				return makeResponse(http.StatusOK, `{"success":true}`), nil
			case 2:
				return makeResponse(http.StatusServiceUnavailable, `<html>503</html>`), nil
			default:
				return makeResponse(http.StatusOK, `{"success":true}`), nil
			}
		},
	}
	ok, err = NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:       client2,
		Endpoint:     "ep-retry",
		PollInterval: 50 * time.Millisecond,
	}).Propagate(context.Background())
	if err != nil || !ok {
		t.Fatalf("expected Phase 1b 503 retry to succeed, got ok=%v err=%v", ok, err)
	}

	// Case 3: Phase 1b double transport failure surfaces exact Chinese error.
	var step3 atomic.Int32
	client3 := &funcAuthorizedClient{
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet && step3.Load() == 0 {
				return makeResponse(http.StatusOK, `{"token":"t"}`), nil
			}
			if req.Method == http.MethodPost && step3.Add(1) == 1 {
				return makeResponse(http.StatusOK, `{"success":true}`), nil
			}
			return nil, errors.New("connection reset")
		},
	}
	ok, err = NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:       client3,
		Endpoint:     "ep-net",
		PollInterval: 50 * time.Millisecond,
	}).Propagate(context.Background())
	if ok || err == nil || err.Error() != "向远端实例同步云盘凭据时网络异常，请检查网络或代理后重试" {
		t.Fatalf("unexpected Phase 1b network error: ok=%v err=%v", ok, err)
	}
}

func TestValidateConsentURLSecurity(t *testing.T) {
	validCases := []string{
		"",
		"   ",
		testConsentURI,
		"https://accounts.google.com/o/oauth2/auth",
		"HTTPS://ACCOUNTS.GOOGLE.COM/o/oauth2/v2/auth?x=1",
	}
	for _, raw := range validCases {
		got, err := validateConsentURL(raw)
		if err != nil {
			t.Fatalf("expected valid consent URL %q, got err: %v", raw, err)
		}
		if got != strings.TrimSpace(raw) {
			t.Fatalf("expected %q, got %q", strings.TrimSpace(raw), got)
		}
	}

	invalidCases := []string{
		"https://evil.example/steal",
		"http://accounts.google.com/o/oauth2/v2/auth",
		"https://accounts.google.com.evil.example/o/oauth2/v2/auth",
		"https://sub.accounts.google.com/o/oauth2/v2/auth",
		"https://accounts.google.com@evil.example/o/oauth2/v2/auth",
		"https://user@accounts.google.com/o/oauth2/v2/auth",
		"javascript:alert(1)",
		"://bad-url",
	}
	for _, raw := range invalidCases {
		_, err := validateConsentURL(raw)
		if err == nil || err.Error() != "Google Colab 返回了未受信任的云盘授权地址，已中止" {
			t.Fatalf("expected untrusted URL rejection for %q, got err=%v", raw, err)
		}
	}

	// Verify DryrunCheck rejects untrusted redirect URI immediately.
	badClient := &funcAuthorizedClient{
		domain: "https://colab.research.google.com",
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"t"}`), nil
			}
			return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"https://evil.example/steal"}`), nil
		},
	}
	authorized, uri, err := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:   badClient,
		Endpoint: "gpu-t4-test",
	}).DryrunCheck(context.Background())
	if authorized || uri != "" || err == nil || !strings.Contains(err.Error(), "未受信任") {
		t.Fatalf("expected DryrunCheck untrusted error, got authorized=%v uri=%q err=%v", authorized, uri, err)
	}
}

func TestCancellationAndStopSemantics(t *testing.T) {
	// 1) Pre-cancelled helper sends error colab_reply immediately.
	noopClient := &funcAuthorizedClient{
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			t.Error("HTTP request should not be issued after pre-cancel")
			return makeResponse(http.StatusOK, `{}`), nil
		},
	}
	helper := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:       noopClient,
		Endpoint:     "gpu-t4-test",
		PollInterval: 50 * time.Millisecond,
	})
	helper.SignalCancel()

	replyCh := make(chan map[string]any, 1)
	helper.HandleColabRequest(
		context.Background(),
		"colab_request",
		map[string]any{"request": map[string]any{"authType": "dfs_ephemeral"}},
		map[string]any{"colab_msg_id": "cm-2"},
		func(payload map[string]any) error {
			replyCh <- payload
			return nil
		},
	)
	select {
	case reply := <-replyCh:
		if reply["type"] != "colab_reply" || reply["colab_msg_id"] != "cm-2" {
			t.Fatalf("unexpected reply envelope: %#v", reply)
		}
		errStr, _ := reply["error"].(string)
		if errStr != "已取消 Google 云盘挂载" {
			t.Fatalf("expected cancel error, got %q", errStr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for cancel colab_reply")
	}
	if helper.Error() != "已取消 Google 云盘挂载" {
		t.Fatalf("expected Error() to record cancellation, got %q", helper.Error())
	}

	// 2) SignalCancel while in Phase 2 aborts immediately AND clears auth_url and hint.
	var cancelURLs []string
	var cancelHints []string
	var cancelMu sync.Mutex
	var mCancel *ColabOneDriveMount
	mCancel = NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client: &funcAuthorizedClient{
			do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return makeResponse(http.StatusOK, `{"token":"t"}`), nil
				}
				return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
			},
		},
		Endpoint:     "ep-cancel",
		PollInterval: 30 * time.Second,
		OnAuthURL: func(u string) {
			cancelMu.Lock()
			cancelURLs = append(cancelURLs, u)
			cancelMu.Unlock()
			if u != "" {
				go func() {
					time.Sleep(15 * time.Millisecond)
					mCancel.SignalCancel()
				}()
			}
		},
		OnHint: func(h string) {
			cancelMu.Lock()
			cancelHints = append(cancelHints, h)
			cancelMu.Unlock()
		},
	})
	ok, err := mCancel.Propagate(context.Background())
	if ok || err == nil || err.Error() != "已取消 Google 云盘挂载" {
		t.Fatalf("expected cancel error, got ok=%v err=%v", ok, err)
	}
	cancelMu.Lock()
	if len(cancelURLs) != 2 || cancelURLs[0] != testConsentURI || cancelURLs[1] != "" {
		t.Fatalf("expected SignalCancel to clear auth_url in defer, got %#v", cancelURLs)
	}
	if len(cancelHints) == 0 || cancelHints[len(cancelHints)-1] != "" {
		t.Fatalf("expected SignalCancel to clear hint in defer, got %#v", cancelHints)
	}
	cancelMu.Unlock()

	// 3) Stop() during Phase 2 suppresses clearing auth_url and hint in defer.
	var stopURLs []string
	var stopHints []string
	var stopMu sync.Mutex
	var mStop *ColabOneDriveMount
	mStop = NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client: &funcAuthorizedClient{
			do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return makeResponse(http.StatusOK, `{"token":"t"}`), nil
				}
				return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
			},
		},
		Endpoint:     "ep-stop",
		PollInterval: 30 * time.Second,
		OnAuthURL: func(u string) {
			stopMu.Lock()
			stopURLs = append(stopURLs, u)
			stopMu.Unlock()
			if u != "" {
				go func() {
					time.Sleep(15 * time.Millisecond)
					mStop.Stop()
				}()
			}
		},
		OnHint: func(h string) {
			stopMu.Lock()
			stopHints = append(stopHints, h)
			stopMu.Unlock()
		},
	})
	ok, err = mStop.Propagate(context.Background())
	if ok || err == nil || err.Error() != "已取消 Google 云盘挂载" {
		t.Fatalf("expected stop abort error, got ok=%v err=%v", ok, err)
	}
	stopMu.Lock()
	if len(stopURLs) != 1 || stopURLs[0] != testConsentURI {
		t.Fatalf("expected Stop() to suppress defer onAuthURL(\"\"), got %#v", stopURLs)
	}
	if len(stopHints) != 0 {
		t.Fatalf("expected Stop() to suppress defer onHint(\"\"), got %#v", stopHints)
	}
	stopMu.Unlock()

	// 4) In-flight HTTP call is aborted immediately by SignalCancel().
	inFlightStarted := make(chan struct{})
	mInFlight := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client: &funcAuthorizedClient{
			do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
				close(inFlightStarted)
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
		Endpoint: "ep-inflight",
	})
	doneCh := make(chan error, 1)
	go func() {
		_, err := mInFlight.Propagate(context.Background())
		doneCh <- err
	}()
	<-inFlightStarted
	start := time.Now()
	mInFlight.SignalCancel()
	select {
	case err := <-doneCh:
		if err == nil || err.Error() != "已取消 Google 云盘挂载" {
			t.Fatalf("expected cancel error for in-flight request, got %v", err)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("expected instant abort of in-flight request, took %v", time.Since(start))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request was not cancelled promptly")
	}
}

func TestDryrunCheckAllBranches(t *testing.T) {
	t.Run("authorized", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, ")]}'\n"+`{"token":"tok"}`), nil
			}
			return makeResponse(http.StatusOK, ")]}'\n"+`{"success":true}`), nil
		}}
		auth, uri, err := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"}).DryrunCheck(context.Background())
		if err != nil || !auth || uri != "" {
			t.Fatalf("expected (true, \"\", nil), got (%v, %q, %v)", auth, uri, err)
		}
	})

	t.Run("unauthorized with consent URL", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
		}}
		auth, uri, err := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"}).DryrunCheck(context.Background())
		if err != nil || auth || uri != testConsentURI {
			t.Fatalf("expected (false, consentURI, nil), got (%v, %q, %v)", auth, uri, err)
		}
	})

	t.Run("transport error on GET", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp failed")
		}}
		_, _, err := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"}).DryrunCheck(context.Background())
		if err == nil || err.Error() != "检查 Google 云盘授权状态时网络异常，请检查网络或代理后重试" {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("transport error on POST", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			return nil, errors.New("eof")
		}}
		_, _, err := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"}).DryrunCheck(context.Background())
		if err == nil || err.Error() != "检查 Google 云盘授权状态时网络异常，请检查网络或代理后重试" {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("GET non-200", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusBadGateway, `<html>502</html>`), nil
		}}
		_, _, err := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"}).DryrunCheck(context.Background())
		if err == nil || err.Error() != "未取得 Google Colab 云盘连接许可（HTTP 502），请重试" {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("GET invalid JSON", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusOK, `<html>not json</html>`), nil
		}}
		_, _, err := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"}).DryrunCheck(context.Background())
		if err == nil || err.Error() != "Google Colab 云盘授权接口返回异常状态（HTTP 200），请重试" {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("GET missing token", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusOK, `{"other":"val"}`), nil
		}}
		_, _, err := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"}).DryrunCheck(context.Background())
		if err == nil || err.Error() != "Google Colab 云盘授权握手缺少令牌，请重试" {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("POST non-200", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			return makeResponse(http.StatusInternalServerError, `{}`), nil
		}}
		_, _, err := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"}).DryrunCheck(context.Background())
		if err == nil || err.Error() != "检查 Google 云盘授权状态失败（HTTP 500），请重试" {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("POST invalid JSON", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			return makeResponse(http.StatusOK, `{}`), nil
		}}
		_, _, err := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"}).DryrunCheck(context.Background())
		if err == nil || err.Error() != "Google Colab 云盘授权接口返回异常状态（HTTP 200），请重试" {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func TestPhase1AndPhase2ErrorAndRetryBoundaries(t *testing.T) {
	t.Run("Phase 1 fails after 5 transport errors", func(t *testing.T) {
		var calls atomic.Int32
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("network down")
		}}
		m := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e", PollInterval: 50 * time.Millisecond})
		ok, err := m.Propagate(context.Background())
		if ok || err == nil || err.Error() != "连接 Google Colab 云盘授权接口网络异常，请检查网络或代理后重试" {
			t.Fatalf("unexpected result: ok=%v err=%v", ok, err)
		}
		if calls.Load() != 5 {
			t.Fatalf("expected 5 attempts (1 initial + 4 retries), got %d", calls.Load())
		}
	})

	t.Run("Phase 1 fails after 5 transient 503 responses", func(t *testing.T) {
		var calls atomic.Int32
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				calls.Add(1)
				return makeResponse(http.StatusServiceUnavailable, "<html>503</html>"), nil
			}
			return makeResponse(http.StatusOK, `{}`), nil
		}}
		m := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e", PollInterval: 50 * time.Millisecond})
		ok, err := m.Propagate(context.Background())
		if ok || err == nil || err.Error() != "Google 云盘授权确认失败（HTTP 503），请重试" {
			t.Fatalf("unexpected result: ok=%v err=%v", ok, err)
		}
		if calls.Load() != 5 {
			t.Fatalf("expected 5 attempts, got %d", calls.Load())
		}
	})

	t.Run("Phase 1 non-retryable HTTP status", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return makeResponse(http.StatusTeapot, `{}`), nil
		}}
		m := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"})
		ok, err := m.Propagate(context.Background())
		if ok || err == nil || err.Error() != "Google 云盘连接返回 HTTP 418，请重试" {
			t.Fatalf("unexpected result: ok=%v err=%v", ok, err)
		}
	})

	t.Run("Phase 1 empty consent URI rejected", func(t *testing.T) {
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			return makeResponse(http.StatusOK, `{"success":false}`), nil
		}}
		m := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"})
		ok, err := m.Propagate(context.Background())
		if ok || err == nil || err.Error() != "Google 未接受本次云盘授权请求，请稍后重试；无需重新登录 Google" {
			t.Fatalf("unexpected result: ok=%v err=%v", ok, err)
		}
	})

	t.Run("Phase 1 missing XSRF token aborts immediately without retry", func(t *testing.T) {
		var calls atomic.Int32
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			calls.Add(1)
			return makeResponse(http.StatusOK, `{}`), nil
		}}
		m := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"})
		ok, err := m.Propagate(context.Background())
		if ok || err == nil || err.Error() != "Google Colab 云盘授权握手缺少令牌，请重试" {
			t.Fatalf("unexpected result: ok=%v err=%v", ok, err)
		}
		if calls.Load() != 1 {
			t.Fatalf("expected immediate abort without retry, got %d calls", calls.Load())
		}
	})

	t.Run("Phase 2 auth_errors > 8 fails and resets on 400", func(t *testing.T) {
		var step atomic.Int32
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			n := step.Add(1)
			if n == 1 {
				return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
			}
			if n <= 6 {
				// 5 consecutive 403s
				return makeResponse(http.StatusForbidden, `{}`), nil
			}
			if n == 7 {
				// One 200 success=false resets auth_errors to 0
				return makeResponse(http.StatusOK, `{"success":false}`), nil
			}
			// Next 9 consecutive 403s trigger failure on the 9th (auth_errors > 8)
			return makeResponse(http.StatusForbidden, `{}`), nil
		}}
		m := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e", PollInterval: 50 * time.Millisecond})
		m.pollInterval = 5 * time.Millisecond // keep test fast
		ok, err := m.Propagate(context.Background())
		if ok || err == nil || err.Error() != "Google 云盘授权确认失败（HTTP 403），请重试" {
			t.Fatalf("unexpected result: ok=%v err=%v", ok, err)
		}
		// 1 (dryrun) + 5 (403) + 1 (reset) + 9 (403) = 16 POST calls
		if step.Load() != 16 {
			t.Fatalf("expected 16 POST calls proving auth_errors reset and >8 threshold, got %d", step.Load())
		}
	})

	t.Run("Phase 2 explicitConfirm transport warning and generic hint", func(t *testing.T) {
		var step atomic.Int32
		var logs []logEntry
		var hints []string
		var mu sync.Mutex
		var m *ColabOneDriveMount

		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				if step.Load() == 1 {
					step.Add(1)
					return nil, errors.New("temporary dns error")
				}
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			n := step.Add(1)
			switch n {
			case 1:
				return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
			case 3:
				return makeResponse(http.StatusOK, `{"success":false}`), nil
			case 4:
				return makeResponse(http.StatusBadGateway, `<html>502</html>`), nil
			default:
				return makeResponse(http.StatusOK, `{"success":true}`), nil
			}
		}}
		m = NewColabOneDriveMount(ColabOneDriveMountOptions{
			Client:   c,
			Endpoint: "e",
			OnAuthURL: func(u string) {
				if u != "" {
					m.SignalConfirm()
				}
			},
			OnHint: func(h string) {
				mu.Lock()
				hints = append(hints, h)
				mu.Unlock()
				if h != "" {
					m.SignalProbe()
				}
			},
			OnLog: func(msg, source, level string) {
				mu.Lock()
				logs = append(logs, logEntry{msg: msg, source: source, level: level})
				mu.Unlock()
				if msg == "核验云盘授权时网络暂时不可用，将自动重试" {
					m.SignalConfirm()
				}
			},
		})
		m.pollInterval = 10 * time.Millisecond

		ok, err := m.Propagate(context.Background())
		if err != nil || !ok {
			t.Fatalf("expected eventual success, got ok=%v err=%v", ok, err)
		}

		mu.Lock()
		defer mu.Unlock()
		wantNetWarn := false
		wantGenericPrompt := false
		for _, l := range logs {
			if l.msg == "核验云盘授权时网络暂时不可用，将自动重试" && l.source == "云盘" && l.level == "warning" {
				wantNetWarn = true
			}
			if l.msg == "请在浏览器中勾选全部云盘权限并点击允许，以确认当前实例挂载 Google 云盘（仅本实例确认一次）" && l.source == "云盘" {
				wantGenericPrompt = true
			}
		}
		if !wantNetWarn {
			t.Fatalf("expected network warning on explicit confirm, got logs=%#v", logs)
		}
		if !wantGenericPrompt {
			t.Fatalf("expected generic consent log without email, got logs=%#v", logs)
		}
		wantGenericHint := "暂未检测到 Google 云盘授权确认：请确认浏览器中选择了与 ColabOne 相同的 Google 账号，并在授权页勾选全部云盘权限后重试"
		foundGenericHint := false
		for _, h := range hints {
			if h == wantGenericHint {
				foundGenericHint = true
				break
			}
		}
		if !foundGenericHint {
			t.Fatalf("expected generic hint %q, got %#v", wantGenericHint, hints)
		}
	})

	t.Run("Phase 2 unexpected HTTP 404 fails immediately", func(t *testing.T) {
		var step atomic.Int32
		c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			if step.Add(1) == 1 {
				return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
			}
			return makeResponse(http.StatusNotFound, `not found`), nil
		}}
		m := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "e"})
		m.pollInterval = 5 * time.Millisecond
		ok, err := m.Propagate(context.Background())
		if ok || err == nil || err.Error() != "向远端实例同步云盘凭据失败（HTTP 404），请重试" {
			t.Fatalf("unexpected result: ok=%v err=%v", ok, err)
		}
	})

	t.Run("Timeout messages with and without AccountEmail", func(t *testing.T) {
		for _, tc := range []struct {
			email string
			want  string
		}{
			{
				email: "bob@example.com",
				want:  "等待浏览器确认 bob@example.com 的 Google 云盘授权超时。请确认浏览器中登录了同一 Google 账号并在授权页勾选全部云盘权限后重试；本机登录仍保留",
			},
			{
				email: "",
				want:  "等待浏览器确认 Google 云盘授权超时。请确认浏览器中选择了与 ColabOne 相同的 Google 账号并勾选全部云盘权限后重试；本机登录仍保留",
			},
		} {
			var step atomic.Int32
			c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
				}
				if step.Add(1) == 1 {
					return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
				}
				return makeResponse(http.StatusBadRequest, `<html>400</html>`), nil
			}}
			m := NewColabOneDriveMount(ColabOneDriveMountOptions{
				Client:       c,
				Endpoint:     "e",
				AccountEmail: tc.email,
			})
			m.timeout = 40 * time.Millisecond
			m.pollInterval = 15 * time.Millisecond
			ok, err := m.Propagate(context.Background())
			if ok || err == nil || err.Error() != tc.want {
				t.Fatalf("email=%q: want %q, got ok=%v err=%v", tc.email, tc.want, ok, err)
			}
		}
	})
}

func TestParseColabJSONLenient(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantKey string
		wantVal any
		empty   bool
	}{
		{name: "plain object", body: `{"token":"abc"}`, wantKey: "token", wantVal: "abc"},
		{name: "xssi prefix", body: ")]}'\n" + `{"success":true}`, wantKey: "success", wantVal: true},
		{name: "xssi multiline json", body: ")]}'\n{\n  \"token\": \"m1\"\n}", wantKey: "token", wantVal: "m1"},
		{name: "multiple prefix lines", body: ")]}'\nnoise\n{\"token\":\"m2\"}", wantKey: "token", wantVal: "m2"},
		{name: "html body", body: "<!DOCTYPE html><html>400</html>", empty: true},
		{name: "empty body", body: "", empty: true},
		{name: "json array", body: `[1,2,3]`, empty: true},
		{name: "json null", body: `null`, empty: true},
		{name: "json primitive", body: `"hello"`, empty: true},
		{name: "empty json object", body: `{}`, empty: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseColabJSONLenient([]byte(tc.body))
			if tc.empty {
				if len(got) != 0 {
					t.Fatalf("expected empty map, got %#v", got)
				}
				return
			}
			if got[tc.wantKey] != tc.wantVal {
				t.Fatalf("expected %s=%v, got %#v", tc.wantKey, tc.wantVal, got)
			}
		})
	}
}

func TestHandleColabRequestFilteringAndDeduplication(t *testing.T) {
	var httpCalls atomic.Int32
	c := &funcAuthorizedClient{do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
		httpCalls.Add(1)
		if req.Method == http.MethodGet {
			return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
		}
		return makeResponse(http.StatusOK, `{"success":true}`), nil
	}}

	m := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "ep"})

	// Non-colab_request msgType must be ignored.
	if m.HandleColabRequest(context.Background(), "input_request", map[string]any{
		"request": map[string]any{"authType": "dfs_ephemeral"},
	}, nil, nil) {
		t.Fatal("expected input_request to be ignored")
	}

	// Nil content or non-dfs_ephemeral authType must be ignored.
	if m.HandleColabRequest(context.Background(), "colab_request", nil, nil, nil) {
		t.Fatal("expected nil content to be ignored")
	}
	if m.HandleColabRequest(context.Background(), "colab_request", map[string]any{
		"request": map[string]any{"authType": "other_auth"},
	}, nil, nil) {
		t.Fatal("expected non-dfs_ephemeral authType to be ignored")
	}

	// Valid dfs_ephemeral request runs once; second call is deduplicated.
	replyCh := make(chan map[string]any, 2)
	send := func(payload map[string]any) error {
		replyCh <- payload
		return nil
	}
	if !m.HandleColabRequest(context.Background(), "colab_request", map[string]any{
		"request": map[string]any{"authType": "dfs_ephemeral"},
	}, map[string]any{"colab_msg_id": 42}, send) {
		t.Fatal("expected first dfs_ephemeral request to be handled")
	}
	if !m.HandleColabRequest(context.Background(), "colab_request", map[string]any{
		"request": map[string]any{"authType": "dfs_ephemeral"},
	}, map[string]any{"colab_msg_id": 43}, send) {
		t.Fatal("expected duplicate dfs_ephemeral request to return true")
	}

	select {
	case reply := <-replyCh:
		if reply["colab_msg_id"] != 42 {
			t.Fatalf("expected colab_msg_id=42, got %#v", reply)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply")
	}

	// Ensure no second reply arrived from the deduplicated call.
	select {
	case extra := <-replyCh:
		t.Fatalf("unexpected duplicate reply: %#v", extra)
	case <-time.After(80 * time.Millisecond):
	}

	// Stopped helper returns true without starting propagation.
	stopped := NewColabOneDriveMount(ColabOneDriveMountOptions{Client: c, Endpoint: "ep"})
	stopped.Stop()
	var called atomic.Bool
	if !stopped.HandleColabRequest(context.Background(), "colab_request", map[string]any{
		"request": map[string]any{"authType": "dfs_ephemeral"},
	}, map[string]any{"colab_msg_id": "cm-stopped"}, func(payload map[string]any) error {
		called.Store(true)
		return nil
	}) {
		t.Fatal("expected stopped helper to return true")
	}
	time.Sleep(50 * time.Millisecond)
	if called.Load() {
		t.Fatal("stopped helper must not spawn propagation or send reply")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read broken") }
func (failingReader) Close() error             { return nil }

func TestSilentProbeContextCancelBodyReadErrorAndNoGoroutineLeaks(t *testing.T) {
	// 1) SignalProbe wakes immediately but does NOT emit unconfirmed hint on HTTP 400.
	var probeStep atomic.Int32
	var probeHints []string
	var hintMu sync.Mutex
	var mProbe *ColabOneDriveMount
	mProbe = NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client: &funcAuthorizedClient{
			domain: "https://colab.research.google.com/",
			do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
				}
				switch probeStep.Add(1) {
				case 1:
					return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
				case 2:
					// Wake again silently for attempt 3.
					go func() {
						time.Sleep(10 * time.Millisecond)
						mProbe.SignalProbe()
					}()
					return makeResponse(http.StatusBadRequest, `<html>400</html>`), nil
				default:
					return makeResponse(http.StatusOK, `{"success":true}`), nil
				}
			},
		},
		Endpoint:     "ep-probe",
		PollInterval: 30 * time.Second,
		OnAuthURL: func(u string) {
			if u != "" {
				mProbe.SignalProbe()
			}
		},
		OnHint: func(h string) {
			hintMu.Lock()
			probeHints = append(probeHints, h)
			hintMu.Unlock()
		},
	})
	ok, err := mProbe.Propagate(nil) // also verifies nil context handling
	if err != nil || !ok {
		t.Fatalf("expected silent probe success, got ok=%v err=%v", ok, err)
	}
	hintMu.Lock()
	for _, h := range probeHints {
		if h != "" {
			t.Fatalf("expected SignalProbe not to emit non-empty hint on 400, got %#v", probeHints)
		}
	}
	hintMu.Unlock()

	// 2) Context cancellation aborts Phase 2 immediately with exact Chinese cancel error.
	ctx, cancel := context.WithCancel(context.Background())
	var mCtx *ColabOneDriveMount
	mCtx = NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client: &funcAuthorizedClient{
			do: func(reqCtx context.Context, req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
				}
				return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
			},
		},
		Endpoint:     "ep-ctx",
		PollInterval: 30 * time.Second,
		OnAuthURL: func(u string) {
			if u != "" {
				go func() {
					time.Sleep(10 * time.Millisecond)
					cancel()
				}()
			}
		},
	})
	ok, err = mCtx.Propagate(ctx)
	if ok || err == nil || err.Error() != "已取消 Google 云盘挂载" {
		t.Fatalf("expected context cancel error, got ok=%v err=%v", ok, err)
	}

	// 3) Response body read error is treated as a transport error in DryrunCheck.
	bodyErrClient := &funcAuthorizedClient{
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       failingReader{},
			}, nil
		},
	}
	_, _, err = NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:   bodyErrClient,
		Endpoint: "ep-body",
	}).DryrunCheck(nil)
	if err == nil || err.Error() != "检查 Google 云盘授权状态时网络异常，请检查网络或代理后重试" {
		t.Fatalf("expected transport error on body read failure, got %v", err)
	}

	// 4) Phase 1b untrusted redirect URI and non-retryable HTTP 404.
	var p1bStep atomic.Int32
	p1bBadClient := &funcAuthorizedClient{
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			if p1bStep.Add(1) == 1 {
				return makeResponse(http.StatusOK, `{"success":true}`), nil
			}
			return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"https://evil.example/x"}`), nil
		},
	}
	ok, err = NewColabOneDriveMount(ColabOneDriveMountOptions{Client: p1bBadClient, Endpoint: "e"}).Propagate(context.Background())
	if ok || err == nil || err.Error() != "Google Colab 返回了未受信任的云盘授权地址，已中止" {
		t.Fatalf("expected Phase 1b untrusted URL error, got ok=%v err=%v", ok, err)
	}

	var p1b404Step atomic.Int32
	p1b404Client := &funcAuthorizedClient{
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
			}
			if p1b404Step.Add(1) == 1 {
				return makeResponse(http.StatusOK, `{"success":true}`), nil
			}
			return makeResponse(http.StatusNotFound, `{}`), nil
		},
	}
	ok, err = NewColabOneDriveMount(ColabOneDriveMountOptions{Client: p1b404Client, Endpoint: "e"}).Propagate(context.Background())
	if ok || err == nil || err.Error() != "向远端实例同步云盘凭据失败（HTTP 404），请重试" {
		t.Fatalf("expected Phase 1b HTTP 404 error, got ok=%v err=%v", ok, err)
	}

	// 5) Concurrent signal stress test + goroutine leak check.
	beforeG := runtime.NumGoroutine()
	var stressStep atomic.Int32
	stressMount := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client: &funcAuthorizedClient{
			do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return makeResponse(http.StatusOK, `{"token":"tok"}`), nil
				}
				if stressStep.Add(1) == 1 {
					return makeResponse(http.StatusOK, `{"success":false,"unauthorized_redirect_uri":"`+testConsentURI+`"}`), nil
				}
				return makeResponse(http.StatusBadRequest, `{}`), nil
			},
		},
		Endpoint:     "ep-stress",
		PollInterval: 20 * time.Millisecond,
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = stressMount.Propagate(context.Background())
	}()
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if (id+j)%2 == 0 {
					stressMount.SignalConfirm()
				} else {
					stressMount.SignalProbe()
				}
				_ = stressMount.Error()
			}
		}(i)
	}
	time.Sleep(25 * time.Millisecond)
	stressMount.SignalCancel()
	stressMount.Stop()
	wg.Wait()

	// Allow runtime scheduler to settle and ensure no leaked watcher goroutines remain.
	time.Sleep(20 * time.Millisecond)
	afterG := runtime.NumGoroutine()
	if afterG > beforeG+2 {
		t.Fatalf("possible goroutine leak: before=%d after=%d", beforeG, afterG)
	}
}

type fakePermAuthErr struct{ msg string }

func (e fakePermAuthErr) Error() string              { return e.msg }
func (e fakePermAuthErr) IsPermanentAuthError() bool { return true }

func TestPermanentAuthErrorFailsFastWithoutRetryLoop(t *testing.T) {
	var calls atomic.Int32
	client := &funcAuthorizedClient{
		domain: "https://colab.research.google.com",
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, fakePermAuthErr{msg: "Google 授权已失效，请在设置中重新登录"}
		},
	}

	m := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:       client,
		Endpoint:     "ep-auth-fail",
		Timeout:      30 * time.Second,
		PollInterval: 50 * time.Millisecond,
	})
	ok, msg, err := m.DryrunCheck(context.Background())
	if ok || err == nil || !strings.Contains(err.Error(), "Google 授权已失效") {
		t.Fatalf("DryrunCheck should fail fast with auth error, got ok=%v msg=%q err=%v", ok, msg, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("DryrunCheck retried permanent auth error: calls=%d", calls.Load())
	}

	// Phase 1 propagation must also fail fast without 5x transport retry or 240s spin.
	calls.Store(0)
	started := time.Now()
	okProp, err := m.Propagate(context.Background())
	if okProp || err == nil || !strings.Contains(err.Error(), "Google 授权已失效") {
		t.Fatalf("Propagate should fail fast with auth error, got ok=%v err=%v", okProp, err)
	}
	if calls.Load() != 1 || time.Since(started) > 2*time.Second {
		t.Fatalf("Propagate retried permanent auth error: calls=%d elapsed=%v", calls.Load(), time.Since(started))
	}
}

func TestDryrunCheckRespectsCanceledCtxAndStopOnlyDoesNotSynthesizeCancelError(t *testing.T) {
	// 1. DryrunCheck must propagate context.Canceled instead of wrapping it as a Chinese network error.
	canceledClient := &funcAuthorizedClient{
		domain: "https://colab.research.google.com",
		do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
			return nil, ctx.Err()
		},
	}
	mDry := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client:   canceledClient,
		Endpoint: "ep-cancel",
	})
	cCtx, cCancel := context.WithCancel(context.Background())
	cCancel()
	if _, _, err := mDry.DryrunCheck(cCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("DryrunCheck swallowed context.Canceled: got %v", err)
	}

	// 2. HandleColabRequest torn down via Stop() (without SignalCancel) must drain worker
	//    and leave Error() empty instead of overwriting with "已取消 Google 云盘挂载".
	entered := make(chan struct{})
	mHook := NewColabOneDriveMount(ColabOneDriveMountOptions{
		Client: &funcAuthorizedClient{
			domain: "https://colab.research.google.com",
			do: func(ctx context.Context, req *http.Request) (*http.Response, error) {
				select {
				case <-entered:
				default:
					close(entered)
				}
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
		Endpoint: "ep-stop-only",
	})
	var replyCount atomic.Int32
	handled := mHook.HandleColabRequest(
		context.Background(),
		"colab_request",
		map[string]any{"request": map[string]any{"authType": "dfs_ephemeral"}},
		map[string]any{"colab_msg_id": "m1"},
		func(payload map[string]any) error {
			replyCount.Add(1)
			return nil
		},
	)
	if !handled {
		t.Fatal("expected HandleColabRequest to accept dfs_ephemeral")
	}
	<-entered
	mHook.Stop()
	if gotErr := mHook.Error(); gotErr != "" {
		t.Fatalf("Stop() without SignalCancel() synthesized spurious error: %q", gotErr)
	}
	if replyCount.Load() != 0 {
		t.Fatalf("Stop() teardown should not send spurious error reply, got %d", replyCount.Load())
	}
}

func TestPropagatePhase2CancelSuppressesOnAuthURL(t *testing.T) {
	t.Run("canceled_during_phase2_log_before_onAuthURL", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var authURLCalls []string
		var urlMu sync.Mutex
		var m *ColabOneDriveMount
		m = NewColabOneDriveMount(ColabOneDriveMountOptions{
			Client: &funcAuthorizedClient{
				domain: "https://colab.research.google.com",
				do: func(reqCtx context.Context, req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodGet {
						return makeResponse(http.StatusOK, `{"token":"xsrf-p2"}`), nil
					}
					body, _ := json.Marshal(map[string]any{
						"success":                   false,
						"unauthorized_redirect_uri": testConsentURI,
					})
					return makeResponse(http.StatusOK, string(body)), nil
				},
			},
			Endpoint:     "ep-p2-cancel-log",
			AccountEmail: "alice@example.com",
			PollInterval: 50 * time.Millisecond,
			OnLog: func(msg, src, lvl string) {
				if strings.Contains(msg, "请在浏览器中使用") {
					cancel()
					m.SignalCancel()
				}
			},
			OnAuthURL: func(u string) {
				urlMu.Lock()
				authURLCalls = append(authURLCalls, u)
				urlMu.Unlock()
			},
		})

		ok, err := m.Propagate(ctx)
		if ok || err == nil || !strings.Contains(err.Error(), "已取消 Google 云盘挂载") {
			t.Fatalf("expected cancel error, got ok=%v err=%v", ok, err)
		}
		urlMu.Lock()
		gotURLs := append([]string(nil), authURLCalls...)
		urlMu.Unlock()
		if len(gotURLs) != 0 {
			t.Fatalf("expected OnAuthURL suppressed when canceled before opening browser, got %#v", gotURLs)
		}
	})

	t.Run("canceled_via_ctx_only_before_phase2_entry_suppresses_onAuthURL_and_log", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var phase1Calls atomic.Int32
		var authURLCalls []string
		var logs []string
		var mu sync.Mutex

		m := NewColabOneDriveMount(ColabOneDriveMountOptions{
			Client: &funcAuthorizedClient{
				domain: "https://colab.research.google.com",
				do: func(reqCtx context.Context, req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodGet {
						return makeResponse(http.StatusOK, `{"token":"xsrf-entry"}`), nil
					}
					n := phase1Calls.Add(1)
					if n == 1 {
						// Phase 1 dryrun=true returns already-authorized, entering Phase 1b.
						return makeResponse(http.StatusOK, `{"success":true}`), nil
					}
					// Phase 1b dryrun=false returns consent URI; cancel ctx right inside Phase 1b's
					// response-processing window via OnLog? No — verify Phase 1b -> Phase 2 entry
					// directly by canceling ctx on the OnLog emitted before Phase 1b or during
					// Phase 2 log: here Phase 1b emits a log before dryrun=false, and we cancel
					// right when Phase 2's log runs if Phase 1b returns consent URI.
					body, _ := json.Marshal(map[string]any{
						"success":                   false,
						"unauthorized_redirect_uri": testConsentURI,
					})
					return makeResponse(http.StatusOK, string(body)), nil
				},
			},
			Endpoint:     "ep-p2-ctx-only",
			PollInterval: 50 * time.Millisecond,
			OnLog: func(msg, src, lvl string) {
				mu.Lock()
				logs = append(logs, msg)
				mu.Unlock()
				if strings.Contains(msg, "请在浏览器中勾选全部云盘权限") {
					// Cancel ONLY ctx (NOT Stop()!) — previously !m.isStopped() let onAuthURL slip through!
					cancel()
				}
			},
			OnAuthURL: func(u string) {
				mu.Lock()
				authURLCalls = append(authURLCalls, u)
				mu.Unlock()
			},
		})

		ok, err := m.Propagate(ctx)
		if ok || err == nil || !strings.Contains(err.Error(), "已取消 Google 云盘挂载") {
			t.Fatalf("expected cancel error, got ok=%v err=%v", ok, err)
		}
		mu.Lock()
		gotURLs := append([]string(nil), authURLCalls...)
		mu.Unlock()
		if len(gotURLs) != 0 {
			t.Fatalf("expected OnAuthURL suppressed on ctx-only cancel, got %#v", gotURLs)
		}
	})

	t.Run("canceled_before_phase2_entry_after_phase1_consent_uri_skips_phase2_completely", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var authURLCount atomic.Int32
		var phase2LogCount atomic.Int32
		m := NewColabOneDriveMount(ColabOneDriveMountOptions{
			Client: &funcAuthorizedClient{
				domain: "https://colab.research.google.com",
				do: func(reqCtx context.Context, req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodGet {
						return makeResponse(http.StatusOK, `{"token":"xsrf-pre-p2"}`), nil
					}
					body, _ := json.Marshal(map[string]any{
						"success":                   false,
						"unauthorized_redirect_uri": testConsentURI,
					})
					// Cancel ctx as the Phase 1 HTTP response completes (after body is produced).
					cancel()
					return makeResponse(http.StatusOK, string(body)), nil
				},
			},
			Endpoint:     "ep-p2-pre-entry",
			PollInterval: 50 * time.Millisecond,
			OnLog: func(msg, src, lvl string) {
				if strings.Contains(msg, "请在浏览器中") {
					phase2LogCount.Add(1)
				}
			},
			OnAuthURL: func(u string) {
				authURLCount.Add(1)
			},
		})

		ok, err := m.Propagate(ctx)
		if ok || err == nil || !strings.Contains(err.Error(), "已取消 Google 云盘挂载") {
			t.Fatalf("expected cancel error, got ok=%v err=%v", ok, err)
		}
		if authURLCount.Load() != 0 {
			t.Fatalf("expected 0 OnAuthURL calls, got %d", authURLCount.Load())
		}
		if phase2LogCount.Load() != 0 {
			t.Fatalf("expected 0 Phase-2 logs when canceled before Phase 2, got %d", phase2LogCount.Load())
		}
	})
}
