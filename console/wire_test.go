package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/assignproxy"
	"github.com/luolongfei/ColabOne/console/internal/colabclient"
	"github.com/luolongfei/ColabOne/console/internal/provider"
	"github.com/luolongfei/ColabOne/console/internal/vault"
)

type fakeWireTimeoutNetErr struct {
	msg string
}

func (e fakeWireTimeoutNetErr) Error() string   { return e.msg }
func (e fakeWireTimeoutNetErr) Timeout() bool   { return true }
func (e fakeWireTimeoutNetErr) Temporary() bool { return true }

type noopWireCloser struct {
	closed atomic.Bool
}

func (c *noopWireCloser) Close() error {
	c.closed.Store(true)
	return nil
}

func startLoopbackConnectRejectProxy(t *testing.T, rawResponse string) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen loopback reject proxy: %v", err)
	}
	var hits atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				br := bufio.NewReader(c)
				for {
					line, rErr := br.ReadString('\n')
					if rErr != nil {
						return
					}
					if line == "\r\n" || line == "\n" {
						break
					}
				}
				hits.Add(1)
				_, _ = io.WriteString(c, rawResponse)
			}(conn)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
	return "http://" + ln.Addr().String(), &hits
}

func TestWireMapColabClientErrMappings(t *testing.T) {
	if got := mapColabClientErr(nil, false); got != nil {
		t.Fatalf("mapColabClientErr(nil, false) = %v, want nil", got)
	}
	if got := mapColabClientErr(nil, true); got != nil {
		t.Fatalf("mapColabClientErr(nil, true) = %v, want nil", got)
	}

	t.Run("too_many_assignments", func(t *testing.T) {
		wrapped := fmt.Errorf("%w: %w", colabclient.ErrTooManyAssignments, &colabclient.ColabOneRequestError{
			StatusCode: http.StatusPreconditionFailed,
			Method:     http.MethodPost,
			URL:        "https://colab.research.google.com/tun/m/assign",
			Reason:     "Precondition Failed",
			Body:       "quota exceeded",
		})
		for _, tc := range []struct {
			name          string
			err           error
			isAssignProxy bool
		}{
			{"direct_sentinel", colabclient.ErrTooManyAssignments, false},
			{"wrapped_412_assign_proxy", wrapped, true},
		} {
			got := mapColabClientErr(tc.err, tc.isAssignProxy)
			var tma *provider.TooManyAssignmentsError
			if !errors.As(got, &tma) {
				t.Fatalf("%s: expected *provider.TooManyAssignmentsError, got %T (%v)", tc.name, got, got)
			}
			if !errors.Is(got, provider.ErrTooManyAssignments) {
				t.Fatalf("%s: expected errors.Is(provider.ErrTooManyAssignments)", tc.name)
			}
			if !errors.Is(got, colabclient.ErrTooManyAssignments) {
				t.Fatalf("%s: expected errors.Is(colabclient.ErrTooManyAssignments)", tc.name)
			}
		}
	})

	t.Run("colab_request_error_preserves_fields", func(t *testing.T) {
		orig := &colabclient.ColabOneRequestError{
			StatusCode: http.StatusServiceUnavailable,
			Method:     http.MethodPost,
			URL:        "https://colab.research.google.com/tun/m/assign?nbh=abc",
			Reason:     "Service Unavailable",
			Body:       `{"error":"capacity exhausted"}`,
		}
		got := mapColabClientErr(orig, true)
		var cre *provider.ColabRequestError
		if !errors.As(got, &cre) {
			t.Fatalf("expected *provider.ColabRequestError, got %T (%v)", got, got)
		}
		if cre.StatusCode != orig.StatusCode ||
			cre.Method != orig.Method ||
			cre.URL != orig.URL ||
			cre.Reason != orig.Reason ||
			cre.ResponseBody != orig.Body {
			t.Fatalf("ColabRequestError field mismatch: got=%+v want=%+v", cre, orig)
		}
		if !errors.Is(got, orig) {
			t.Fatalf("expected wrapped original ColabOneRequestError")
		}
	})

	t.Run("assignproxy_proxy_error_kinds", func(t *testing.T) {
		cases := []struct {
			name     string
			kindErr  error
			message  string
			wantKind string
		}{
			{
				name:     "auth",
				kindErr:  assignproxy.ErrProxyAuth,
				message:  "GPU 分配代理认证失败，请检查代理用户名与密码后重试",
				wantKind: "auth",
			},
			{
				name:     "tls",
				kindErr:  assignproxy.ErrProxyTLS,
				message:  "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理",
				wantKind: "ssl",
			},
			{
				name:     "timeout",
				kindErr:  assignproxy.ErrProxyTimeout,
				message:  "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性",
				wantKind: "connect_timeout",
			},
			{
				name:     "connect",
				kindErr:  assignproxy.ErrProxyConnect,
				message:  "代理拒绝连接 (HTTP 502)",
				wantKind: "proxy",
			},
		}
		for _, tc := range cases {
			pErr := &assignproxy.ProxyError{
				Kind:    tc.kindErr,
				Message: tc.message,
			}
			// Simulate http.Client.Do wrapping the transport error in *url.Error.
			wrapped := &url.Error{
				Op:  "Post",
				URL: "https://colab.research.google.com/tun/m/assign",
				Err: pErr,
			}
			got := mapColabClientErr(wrapped, true)
			var apErr *provider.AssignProxyError
			if !errors.As(got, &apErr) {
				t.Fatalf("%s: expected *provider.AssignProxyError, got %T (%v)", tc.name, got, got)
			}
			if apErr.Kind != tc.wantKind {
				t.Fatalf("%s: AssignProxyError.Kind = %q, want %q", tc.name, apErr.Kind, tc.wantKind)
			}
			if apErr.Message != tc.message {
				t.Fatalf("%s: AssignProxyError.Message = %q, want %q", tc.name, apErr.Message, tc.message)
			}
			if !errors.Is(got, tc.kindErr) {
				t.Fatalf("%s: expected errors.Is(%v) through mapped error", tc.name, tc.kindErr)
			}
		}
	})

	t.Run("raw_upstream_read_timeout_is_not_fail_fast", func(t *testing.T) {
		rawDeadlineErr := &url.Error{
			Op:  "Post",
			URL: "https://colab.research.google.com/tun/m/assign",
			Err: context.DeadlineExceeded,
		}
		gotDeadline := mapColabClientErr(rawDeadlineErr, true)
		var apErr *provider.AssignProxyError
		if errors.As(gotDeadline, &apErr) {
			t.Fatalf("raw context.DeadlineExceeded must NOT map to AssignProxyError, got %+v", apErr)
		}
		if gotDeadline != rawDeadlineErr {
			t.Fatalf("expected original rawDeadlineErr returned unchanged, got %v", gotDeadline)
		}

		rawNetTimeoutErr := &url.Error{
			Op:  "Get",
			URL: "https://colab.research.google.com/tun/m/assign",
			Err: fakeWireTimeoutNetErr{msg: "read tcp 127.0.0.1:41000->127.0.0.1:7890: i/o timeout"},
		}
		gotNetTimeout := mapColabClientErr(rawNetTimeoutErr, true)
		if errors.As(gotNetTimeout, &apErr) {
			t.Fatalf("raw net timeout must NOT map to AssignProxyError, got %+v", apErr)
		}
		if gotNetTimeout != rawNetTimeoutErr {
			t.Fatalf("expected original rawNetTimeoutErr returned unchanged, got %v", gotNetTimeout)
		}

		// Also verify wireColabClientAdapter.mapErr sets isAssignProxy=true when closer!=nil
		// and still preserves raw read timeouts so provider.Allocate retries them.
		adapter := &wireColabClientAdapter{closer: &noopWireCloser{}}
		gotAdapterErr := adapter.mapErr(rawDeadlineErr)
		if errors.As(gotAdapterErr, &apErr) || gotAdapterErr != rawDeadlineErr {
			t.Fatalf("wireColabClientAdapter.mapErr unexpectedly transformed raw read timeout: %T (%v)", gotAdapterErr, gotAdapterErr)
		}
	})
}

func TestWireAssignProxyClientTimeoutBudgetAndConnectFailFastVsReadRetry(t *testing.T) {
	t.Run("client_timeout_budget_is_75s_not_60s", func(t *testing.T) {
		if assignproxy.AssignProxyConnectTimeout != 15*time.Second {
			t.Fatalf("AssignProxyConnectTimeout = %v, want 15s", assignproxy.AssignProxyConnectTimeout)
		}
		if assignproxy.AssignProxyReadTimeout != 60*time.Second {
			t.Fatalf("AssignProxyReadTimeout = %v, want 60s", assignproxy.AssignProxyReadTimeout)
		}
		budget := assignproxy.AssignProxyConnectTimeout + assignproxy.AssignProxyReadTimeout
		if budget != 75*time.Second {
			t.Fatalf("assign proxy timeout budget = %v, want 75s", budget)
		}

		spec, err := assignproxy.ParseAssignProxy("http://127.0.0.1:7890")
		if err != nil {
			t.Fatalf("ParseAssignProxy failed: %v", err)
		}
		tr, err := assignproxy.NewAssignOnlyTransport(spec, "https", "colab.research.google.com")
		if err != nil {
			t.Fatalf("NewAssignOnlyTransport failed: %v", err)
		}
		defer tr.Close()

		// Mirror console/wire.go ClientFactory construction.
		httpClient := tr.NewClient(assignproxy.AssignProxyConnectTimeout + assignproxy.AssignProxyReadTimeout)
		if httpClient.Timeout != 75*time.Second {
			t.Fatalf("httpClient.Timeout = %v, want 75s (must not default to 60s)", httpClient.Timeout)
		}
		if httpClient.Transport != tr {
			t.Fatalf("expected httpClient.Transport to be AssignOnlyTransport")
		}
		if httpClient.CheckRedirect == nil {
			t.Fatalf("expected httpClient.CheckRedirect to be non-nil")
		}

		adapter := &wireColabClientAdapter{
			c: colabclient.NewColabOneColabClient(
				func(context.Context) (string, error) { return "fake-token", nil },
				colabclient.WithHTTPClient(httpClient),
			),
			closer: tr,
		}
		if err := adapter.Close(); err != nil {
			t.Fatalf("adapter.Close failed: %v", err)
		}
	})

	t.Run("proxy_connect_and_auth_407_fail_fast_on_attempt_1", func(t *testing.T) {
		cases := []struct {
			name        string
			rawResp     string
			wantKind    string
			wantErrPart string
		}{
			{
				name:        "auth_407",
				rawResp:     "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"test\"\r\nContent-Length: 0\r\n\r\n",
				wantKind:    "auth",
				wantErrPart: "GPU 分配代理认证失败",
			},
			{
				name:        "connect_502",
				rawResp:     "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n",
				wantKind:    "proxy",
				wantErrPart: "GPU 分配代理连接失败",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				proxyURL, hits := startLoopbackConnectRejectProxy(t, tc.rawResp)
				const targetOrigin = "https://127.0.0.1:443"

				newAdapter := func(assignProxy string) (*wireColabClientAdapter, error) {
					spec, err := assignproxy.ParseAssignProxy(assignProxy)
					if err != nil {
						return nil, err
					}
					tr, err := assignproxy.NewAssignOnlyTransport(spec, "https", "127.0.0.1:443")
					if err != nil {
						return nil, err
					}
					httpClient := tr.NewClient(assignproxy.AssignProxyConnectTimeout + assignproxy.AssignProxyReadTimeout)
					c := colabclient.NewColabOneColabClient(
						func(context.Context) (string, error) { return "fake-oauth-token", nil },
						colabclient.WithColabDomain(targetOrigin),
						colabclient.WithHTTPClient(httpClient),
					)
					return &wireColabClientAdapter{c: c, closer: tr}, nil
				}

				// First verify wireColabClientAdapter maps the error directly to *provider.AssignProxyError.
				directAdapter, err := newAdapter(proxyURL)
				if err != nil {
					t.Fatalf("newAdapter failed: %v", err)
				}
				_, _, directErr := directAdapter.GetAssignment(context.Background(), "11111111-1111-4111-8111-111111111111", "GPU", "T4", "HIGH_RAM")
				_ = directAdapter.Close()
				var apErr *provider.AssignProxyError
				if !errors.As(directErr, &apErr) {
					t.Fatalf("expected *provider.AssignProxyError from wireColabClientAdapter, got %T (%v)", directErr, directErr)
				}
				if apErr.Kind != tc.wantKind {
					t.Fatalf("AssignProxyError.Kind = %q, want %q", apErr.Kind, tc.wantKind)
				}

				// Reset hit counter and verify provider.Allocate fails fast on attempt 1 without retrying.
				hits.Store(0)
				prov := provider.NewColabOneProvider(provider.ColabOneProviderConfig{
					Vault: &wireVaultAdapter{v: vault.NewMemoryVault()},
					ClientFactory: func(assignProxy string) (provider.ColabClient, error) {
						return newAdapter(assignProxy)
					},
					ParseAssignProxyFn: wireProviderParseAssignProxy,
					FormatProxyErrorFn: assignproxy.FormatProxyError,
					AssignRetryDelays:  []time.Duration{5 * time.Second, 10 * time.Second},
				})

				started := time.Now()
				_, allocErr := prov.Allocate(context.Background(), "T4", provider.AllocateOptions{
					AssignProxy: proxyURL,
				})
				elapsed := time.Since(started)

				if allocErr == nil || !strings.Contains(allocErr.Error(), tc.wantErrPart) {
					t.Fatalf("Allocate err = %v, want substring %q", allocErr, tc.wantErrPart)
				}
				if gotHits := hits.Load(); gotHits != 1 {
					t.Fatalf("proxy hit count = %d, want 1 (must fail fast on attempt 1 without retrying)", gotHits)
				}
				if elapsed > 2*time.Second {
					t.Fatalf("Allocate did not fail fast (elapsed=%v)", elapsed)
				}
			})
		}
	})

	t.Run("raw_upstream_read_timeout_retries_and_succeeds_on_attempt_2", func(t *testing.T) {
		testDone := make(chan struct{})
		var getCalls, postCalls, proxyCalls atomic.Int32

		upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/tun/m/assign" {
				http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
				return
			}
			if r.Header.Get("Authorization") != "Bearer fake-oauth-token" {
				http.Error(w, "missing auth", http.StatusUnauthorized)
				return
			}
			switch r.Method {
			case http.MethodGet:
				n := getCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, ")]}'\n{\"token\":\"xsrf-tok-"+strconv.Itoa(int(n))+"\"}")
			case http.MethodPost:
				n := postCalls.Add(1)
				if n == 1 {
					// Stall response headers on attempt 1 to trigger a real raw upstream read timeout.
					timer := time.NewTimer(2 * time.Second)
					defer timer.Stop()
					select {
					case <-r.Context().Done():
					case <-testDone:
					case <-timer.C:
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, ")]}'\n{\"endpoint\":\"m-s-wire-retry-ok\",\"accelerator\":\"T4\",\"variant\":\"GPU\",\"runtimeProxyInfo\":{\"url\":\"https://rt-wire-retry.invalid\",\"token\":\"tok-wire-retry\",\"tokenExpiresInSeconds\":3600}}")
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		}))

		upTransport := &http.Transport{Proxy: nil}
		proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proxyCalls.Add(1)
			fwdCtx, cancel := context.WithCancel(r.Context())
			defer cancel()
			go func() {
				select {
				case <-testDone:
					cancel()
				case <-fwdCtx.Done():
				}
			}()
			upReq, err := http.NewRequestWithContext(fwdCtx, r.Method, r.RequestURI, r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for k, vv := range r.Header {
				if strings.EqualFold(k, "Proxy-Authorization") || strings.EqualFold(k, "Proxy-Connection") {
					continue
				}
				for _, v := range vv {
					upReq.Header.Add(k, v)
				}
			}
			upClient := &http.Client{
				Transport: upTransport,
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
			resp, err := upClient.Do(upReq)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			for k, vv := range resp.Header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
		}))

		// Register cleanup so testDone closes FIRST (LIFO), immediately releasing any stalled handler.
		t.Cleanup(func() {
			proxySrv.Close()
			upstreamSrv.Close()
			upTransport.CloseIdleConnections()
		})
		t.Cleanup(func() {
			close(testDone)
		})

		upURL, err := url.Parse(upstreamSrv.URL)
		if err != nil {
			t.Fatalf("parse upstream URL: %v", err)
		}

		prov := provider.NewColabOneProvider(provider.ColabOneProviderConfig{
			Vault: &wireVaultAdapter{v: vault.NewMemoryVault()},
			ClientFactory: func(assignProxy string) (provider.ColabClient, error) {
				tokenFn := func(context.Context) (string, error) { return "fake-oauth-token", nil }
				trimmed := strings.TrimSpace(assignProxy)
				if trimmed == "" {
					c := colabclient.NewColabOneColabClient(tokenFn, colabclient.WithColabDomain(upstreamSrv.URL))
					return &wireColabClientAdapter{c: c}, nil
				}
				spec, err := assignproxy.ParseAssignProxy(trimmed)
				if err != nil {
					return nil, err
				}
				tr, err := assignproxy.NewAssignOnlyTransport(spec, upURL.Scheme, upURL.Host)
				if err != nil {
					return nil, err
				}
				// Short test timeout so the stalled upstream /tun/m/assign triggers a raw read timeout quickly.
				httpClient := tr.NewClient(60 * time.Millisecond)
				c := colabclient.NewColabOneColabClient(
					tokenFn,
					colabclient.WithColabDomain(upstreamSrv.URL),
					colabclient.WithHTTPClient(httpClient),
				)
				return &wireColabClientAdapter{c: c, closer: tr}, nil
			},
			ParseAssignProxyFn: wireProviderParseAssignProxy,
			FormatProxyErrorFn: assignproxy.FormatProxyError,
			AssignRetryDelays:  []time.Duration{5 * time.Millisecond, 5 * time.Millisecond},
			SleepFn:            func(context.Context, time.Duration) error { return nil },
		})

		var logs []string
		started := time.Now()
		sess, err := prov.Allocate(context.Background(), "T4", provider.AllocateOptions{
			AssignProxy: proxySrv.URL,
			OnLog: func(msg, src, lvl string) {
				logs = append(logs, msg)
			},
		})
		elapsed := time.Since(started)

		if err != nil {
			t.Fatalf("expected Allocate to retry after raw read timeout and succeed, got err=%v (logs=%v)", err, logs)
		}
		if sess == nil || sess.Endpoint != "m-s-wire-retry-ok" {
			t.Fatalf("unexpected session: %+v", sess)
		}
		if got := postCalls.Load(); got != 2 {
			t.Fatalf("POST /tun/m/assign calls = %d, want 2 (timeout on attempt 1, success on attempt 2)", got)
		}
		if got := getCalls.Load(); got != 2 {
			t.Fatalf("GET /tun/m/assign calls = %d, want 2 (attempt 1 + retry reconciliation)", got)
		}
		if got := proxyCalls.Load(); got != 4 {
			t.Fatalf("proxy forwarded calls = %d, want 4", got)
		}
		if elapsed > 500*time.Millisecond {
			t.Fatalf("retry test took too long: %v", elapsed)
		}
		sawTimeoutRetryLog := false
		for _, msg := range logs {
			if strings.Contains(msg, "请求超时") && strings.Contains(msg, "第 2/3 次") {
				sawTimeoutRetryLog = true
				break
			}
		}
		if !sawTimeoutRetryLog {
			t.Fatalf("expected timeout retry log on attempt 2/3, got logs=%v", logs)
		}
	})
}

func TestWireMapColabClientErrProxyConnectNotMisclassifiedAsAuth(t *testing.T) {
	const (
		connectMsg = "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试"
		authMsg    = "GPU 分配代理认证失败，请检查代理用户名与密码后重试"
		sslMsg     = "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理"
		timeoutMsg = "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性"
	)

	cases := []struct {
		name     string
		err      error
		wantKind string
		wantMsg  string
	}{
		{
			name:     "unclassified_proxyconnect_refused_maps_to_proxy_not_auth",
			err:      errors.New("proxyconnect tcp: dial tcp 127.0.0.1:7890: connect: connection refused"),
			wantKind: "proxy",
			wantMsg:  connectMsg,
		},
		{
			name:     "unclassified_proxyconnect_port_407_maps_to_proxy_not_auth",
			err:      errors.New("proxyconnect tcp: dial tcp 127.0.0.1:407: connect: connection refused"),
			wantKind: "proxy",
			wantMsg:  connectMsg,
		},
		{
			name:     "unclassified_proxyconnect_407_maps_to_auth",
			err:      errors.New("proxyconnect tcp: 407 Proxy Authentication Required"),
			wantKind: "auth",
			wantMsg:  authMsg,
		},
		{
			name:     "unclassified_proxyconnect_x509_maps_to_ssl",
			err:      errors.New("proxyconnect tcp: x509: certificate signed by unknown authority"),
			wantKind: "ssl",
			wantMsg:  sslMsg,
		},
		{
			name:     "unclassified_proxyconnect_timeout_maps_to_connect_timeout",
			err:      errors.New("proxyconnect tcp: dial tcp 127.0.0.1:7890: i/o timeout"),
			wantKind: "connect_timeout",
			wantMsg:  timeoutMsg,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := mapColabClientErr(tc.err, true)
			var apErr *provider.AssignProxyError
			if !errors.As(mapped, &apErr) {
				t.Fatalf("expected *provider.AssignProxyError, got %T (%v)", mapped, mapped)
			}
			if apErr.Kind != tc.wantKind {
				t.Fatalf("AssignProxyError.Kind = %q, want %q (msg=%q)", apErr.Kind, tc.wantKind, apErr.Message)
			}
			if apErr.Message != tc.wantMsg {
				t.Fatalf("AssignProxyError.Message = %q, want %q", apErr.Message, tc.wantMsg)
			}
			// Re-formatting through provider.FormatProxyError must preserve the exact message.
			if got := provider.FormatProxyError(mapped); got != tc.wantMsg {
				t.Fatalf("provider.FormatProxyError(mapped) = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}

func TestWireControllerProviderAdapterClearOrphanedRemotes(t *testing.T) {
	mem := vault.NewMemoryVault()
	va := &wireVaultAdapter{v: mem}
	prov := provider.NewColabOneProvider(provider.ColabOneProviderConfig{
		Vault: va,
	})
	adapter := &wireControllerProviderAdapter{p: prov}

	// Verify structural interface compatibility expected by controller.
	var cleaner interface{ ClearOrphanedRemotes() } = adapter

	// 1. Seed stale remote keys with NO managed sessions -> cleared atomically.
	_ = mem.Set("remote", map[string]any{"endpoint": "ep-legacy"})
	_ = mem.Set("remotes", map[string]any{"ep-map": map[string]any{"endpoint": "ep-map"}})
	_ = mem.Set("remote_ids", []string{"ep-stale"})
	_ = mem.Set("remote:ep-stale", map[string]any{"endpoint": "ep-stale"})

	cleaner.ClearOrphanedRemotes()

	for _, key := range []string{"remote", "remotes", "remote_ids", "remote:ep-stale"} {
		if va.Has(key) {
			t.Fatalf("expected orphaned vault key %q deleted", key)
		}
	}
	if len(adapter.RemotesMap()) != 0 {
		t.Fatalf("expected empty RemotesMap after ClearOrphanedRemotes, got %#v", adapter.RemotesMap())
	}

	// 2. When a live managed session exists, ClearOrphanedRemotes is a no-op.
	if err := prov.SaveSession(&provider.ColabOneSessionState{
		Endpoint: "ep-live",
		Token:    "tok-live",
		URL:      "https://live.invalid",
	}, true); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := prov.SaveRemote(map[string]any{"endpoint": "ep-live", "gpu": "T4"}, true); err != nil {
		t.Fatalf("SaveRemote: %v", err)
	}

	cleaner.ClearOrphanedRemotes()

	if len(adapter.SessionsMap()) != 1 || len(adapter.RemotesMap()) != 1 {
		t.Fatalf("ClearOrphanedRemotes must preserve live sessions and remotes: sessions=%#v remotes=%#v", adapter.SessionsMap(), adapter.RemotesMap())
	}
}

func TestSelfTestValidatesAllEmbeddedCatalogsAndRejectsInvalid(t *testing.T) {
	if err := runColabOneSelfTest(); err != nil {
		t.Fatalf("runColabOneSelfTest failed: %v", err)
	}

	// Reject malformed live_studio.json
	for _, bad := range []string{
		`{bad`,
		`{"port":0,"ws_path":"/ws","default_fps":25,"audio_input_sample_rate":16000,"audio_output_sample_rate":24000}`,
		`{"port":8190,"ws_path":"no-slash","default_fps":25,"audio_input_sample_rate":16000,"audio_output_sample_rate":24000}`,
		`{"port":8190,"ws_path":"/ws","default_fps":25,"audio_input_sample_rate":16000,"audio_output_sample_rate":24000,"asr_vad":[],"streaming_tts":[],"visual_engines":[],"default_actions":[]}`,
	} {
		if err := validateColabOneLiveStudioCatalog([]byte(bad)); err == nil {
			t.Fatalf("expected validateColabOneLiveStudioCatalog to reject %s", bad)
		}
	}

	// Reject malformed cloudflared.json / rclone.json
	for _, bad := range []string{
		`{bad`,
		`{"tag":"","sha256":{"amd64":"` + strings.Repeat("a", 64) + `","arm64":"` + strings.Repeat("b", 64) + `"}}`,
		`{"tag":"v1","sha256":{"amd64":"not-hex","arm64":"` + strings.Repeat("b", 64) + `"}}`,
		`{"tag":"v1","sha256":{"amd64":"` + strings.Repeat("a", 64) + `"}}`,
	} {
		if err := validateColabOneArchSHA256Catalog("cloudflared.json", []byte(bad)); err == nil {
			t.Fatalf("expected validateColabOneArchSHA256Catalog to reject %s", bad)
		}
	}

	// Reject malformed engine.json
	for _, bad := range []string{
		`{bad`,
		`{"tag":"b1","assets":[]}`,
		`{"tag":"b1","assets":[{"url":"http://insecure.example/a.tar.gz","sha256":"` + strings.Repeat("a", 64) + `"}]}`,
		`{"tag":"b1","assets":[{"url":"https://github.com/a/b.tar.gz","sha256":"short"}]}`,
	} {
		if err := validateColabOneEngineCatalog([]byte(bad)); err == nil {
			t.Fatalf("expected validateColabOneEngineCatalog to reject %s", bad)
		}
	}

	// Self-test exercises both embedded service bundle validation and an isolated
	// OS-level encrypted local vault round-trip (DPAPI/file-KEK + ACL/DACL + atomic replace).
	if err := runColabOneSelfTest(); err != nil {
		t.Fatalf("runColabOneSelfTest() failed: %v", err)
	}
}
