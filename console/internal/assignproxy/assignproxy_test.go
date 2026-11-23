package assignproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
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
	"testing"
	"time"
)

// --- Offline mock servers ---

type mockHTTPTarget struct {
	server *httptest.Server
	mu     sync.Mutex
	hits   []string
	auths  []string
}

func startMockHTTPTarget(t *testing.T, handler http.HandlerFunc) *mockHTTPTarget {
	t.Helper()
	mt := &mockHTTPTarget{}
	mt.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mt.mu.Lock()
		mt.hits = append(mt.hits, r.URL.RequestURI())
		mt.auths = append(mt.auths, r.Header.Get("Authorization"))
		mt.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("User-agent: *\nDisallow:\n"))
	}))
	t.Cleanup(mt.server.Close)
	return mt
}

func (m *mockHTTPTarget) getHits() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.hits))
	copy(out, m.hits)
	return out
}

func (m *mockHTTPTarget) getAuths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.auths))
	copy(out, m.auths)
	return out
}

type seenProxyReq struct {
	method string
	target string
	auth   string
}

type mockHTTPProxy struct {
	server     *httptest.Server
	port       int
	mu         sync.Mutex
	seen       []seenProxyReq
	custom407  string
	connectDst string
}

func startMockHTTPProxy(t *testing.T, reqUser, reqPass string, custom407Reason string) *mockHTTPProxy {
	t.Helper()
	mp := &mockHTTPProxy{custom407: custom407Reason}
	expectedAuth := ""
	if reqUser != "" || reqPass != "" {
		expectedAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(reqUser+":"+reqPass))
	}

	mp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHdr := r.Header.Get("Proxy-Authorization")
		mp.mu.Lock()
		mp.seen = append(mp.seen, seenProxyReq{
			method: r.Method,
			target: r.RequestURI,
			auth:   authHdr,
		})
		overrideDst := mp.connectDst
		mp.mu.Unlock()

		if expectedAuth != "" && authHdr != expectedAuth {
			if mp.custom407 != "" {
				hj, ok := w.(http.Hijacker)
				if ok {
					conn, rw, err := hj.Hijack()
					if err == nil {
						_, _ = rw.WriteString("HTTP/1.1 407 " + mp.custom407 + "\r\nProxy-Authenticate: Basic realm=\"test\"\r\nContent-Length: 0\r\n\r\n")
						_ = rw.Flush()
						_ = conn.Close()
						return
					}
				}
			}
			w.Header().Set("Proxy-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}

		if r.Method == http.MethodConnect {
			dst := r.Host
			if overrideDst != "" {
				dst = overrideDst
			}
			upstream, err := net.DialTimeout("tcp", dst, 5*time.Second)
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				_ = upstream.Close()
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			clientConn, rw, err := hj.Hijack()
			if err != nil {
				_ = upstream.Close()
				return
			}
			_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			_ = rw.Flush()

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, _ = io.Copy(upstream, rw)
				if tc, ok := upstream.(*net.TCPConn); ok {
					_ = tc.CloseWrite()
				}
			}()
			go func() {
				defer wg.Done()
				_, _ = io.Copy(clientConn, upstream)
				if tc, ok := clientConn.(*net.TCPConn); ok {
					_ = tc.CloseWrite()
				}
			}()
			wg.Wait()
			_ = upstream.Close()
			_ = clientConn.Close()
			return
		}

		// Plain HTTP forward (offline loopback).
		upReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.RequestURI, r.Body)
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
			Transport: &http.Transport{Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: 5 * time.Second,
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
	u, err := url.Parse(mp.server.URL)
	if err != nil {
		t.Fatalf("parse mock proxy url: %v", err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse mock proxy port: %v", err)
	}
	mp.port = p
	t.Cleanup(mp.server.Close)
	return mp
}

func (m *mockHTTPProxy) getSeen() []seenProxyReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]seenProxyReq, len(m.seen))
	copy(out, m.seen)
	return out
}

type socks5Record struct {
	atyp byte
	host string
	port int
}

type mockSOCKS5Options struct {
	targetAddr      string // loopback address to actually tunnel to
	reqUser         string
	reqPass         string
	requireAuth     bool
	replyCode       byte
	shortErrHeader  bool
	replyBNDType    byte // 0x01 default, 0x03 domain, 0x04 ipv6, or invalid
	overrideVer     byte // non-zero overrides greeting reply version
	overrideMethod  byte // non-zero overrides selected method
	overrideRepVer  byte // non-zero overrides connect reply version
	closeAfterGreet bool
	hangForever     bool
	replyDelay      time.Duration // optional delay before sending CONNECT 0x00 reply
	optimisticStall bool          // reply 0x00 without upstream then stall during target TLS
	optimisticClose bool          // reply 0x00 without upstream then immediately EOF
	optimisticReset bool          // reply 0x00 without upstream then TCP RST after ClientHello
}

type mockSOCKS5Server struct {
	ln      net.Listener
	port    int
	mu      sync.Mutex
	records []socks5Record
	wg      sync.WaitGroup
	stopCh  chan struct{}
}

func startMockSOCKS5Server(t *testing.T, opts mockSOCKS5Options) *mockSOCKS5Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock socks5: %v", err)
	}
	s := &mockSOCKS5Server{
		ln:     ln,
		port:   ln.Addr().(*net.TCPAddr).Port,
		stopCh: make(chan struct{}),
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func(c net.Conn) {
				defer s.wg.Done()
				s.handleConn(c, opts)
			}(conn)
		}
	}()

	t.Cleanup(func() {
		close(s.stopCh)
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *mockSOCKS5Server) handleConn(conn net.Conn, opts mockSOCKS5Options) {
	defer conn.Close()
	if opts.hangForever {
		<-s.stopCh
		return
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return
	}
	if hdr[0] != 0x05 {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}

	if opts.closeAfterGreet {
		return
	}
	if opts.overrideVer != 0 {
		_, _ = conn.Write([]byte{opts.overrideVer, 0x00})
		return
	}
	if opts.overrideMethod != 0 {
		_, _ = conn.Write([]byte{0x05, opts.overrideMethod})
		return
	}

	if opts.requireAuth {
		hasUserPass := false
		for _, m := range methods {
			if m == 0x02 {
				hasUserPass = true
				break
			}
		}
		if !hasUserPass {
			_, _ = conn.Write([]byte{0x05, 0xFF})
			return
		}
		_, _ = conn.Write([]byte{0x05, 0x02})
		authHdr := make([]byte, 2)
		if _, err := io.ReadFull(conn, authHdr); err != nil || authHdr[0] != 0x01 {
			return
		}
		uBuf := make([]byte, authHdr[1])
		if _, err := io.ReadFull(conn, uBuf); err != nil {
			return
		}
		pLen := make([]byte, 1)
		if _, err := io.ReadFull(conn, pLen); err != nil {
			return
		}
		pBuf := make([]byte, pLen[0])
		if _, err := io.ReadFull(conn, pBuf); err != nil {
			return
		}
		if string(uBuf) != opts.reqUser || string(pBuf) != opts.reqPass {
			_, _ = conn.Write([]byte{0x01, 0x01})
			return
		}
		_, _ = conn.Write([]byte{0x01, 0x00})
	} else {
		_, _ = conn.Write([]byte{0x05, 0x00})
	}

	reqHdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, reqHdr); err != nil {
		return
	}
	if reqHdr[0] != 0x05 || reqHdr[1] != 0x01 {
		return
	}

	atyp := reqHdr[3]
	var host string
	switch atyp {
	case 0x01:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return
		}
		dom := make([]byte, l[0])
		if _, err := io.ReadFull(conn, dom); err != nil {
			return
		}
		host = string(dom)
	case 0x04:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	default:
		_, _ = conn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return
	}
	port := int(binary.BigEndian.Uint16(portBuf))

	s.mu.Lock()
	s.records = append(s.records, socks5Record{atyp: atyp, host: host, port: port})
	s.mu.Unlock()

	if opts.overrideRepVer != 0 {
		_, _ = conn.Write([]byte{opts.overrideRepVer, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 80})
		return
	}

	if opts.replyCode != 0 {
		if opts.shortErrHeader {
			// 4-byte failure header followed by immediate close (no BND.ADDR/BND.PORT).
			_, _ = conn.Write([]byte{0x05, opts.replyCode, 0x00, 0x01})
			return
		}
		_, _ = conn.Write([]byte{0x05, opts.replyCode, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	if opts.replyBNDType != 0 && opts.replyBNDType != 0x01 && opts.replyBNDType != 0x03 && opts.replyBNDType != 0x04 {
		_, _ = conn.Write([]byte{0x05, 0x00, 0x00, opts.replyBNDType, 0, 0})
		return
	}

	if opts.replyDelay > 0 {
		timer := time.NewTimer(opts.replyDelay)
		select {
		case <-timer.C:
		case <-s.stopCh:
			timer.Stop()
			return
		}
	}

	if opts.optimisticStall || opts.optimisticClose || opts.optimisticReset {
		_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 80})
		_ = conn.SetDeadline(time.Time{})
		if opts.optimisticClose {
			return
		}
		if opts.optimisticReset {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 256)
			_, _ = conn.Read(buf)
			if tc, ok := conn.(*net.TCPConn); ok {
				_ = tc.SetLinger(0)
			}
			return
		}
		// optimisticStall: keep TCP open and never respond to target TLS ClientHello.
		<-s.stopCh
		return
	}

	upstream, err := net.DialTimeout("tcp", opts.targetAddr, 5*time.Second)
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()

	switch opts.replyBNDType {
	case 0x03:
		bnd := append([]byte{0x05, 0x00, 0x00, 0x03, 9}, []byte("localhost")...)
		bnd = append(bnd, 0x00, 0x50)
		_, _ = conn.Write(bnd)
	case 0x04:
		bnd := make([]byte, 4+16+2)
		bnd[0], bnd[1], bnd[2], bnd[3] = 0x05, 0x00, 0x00, 0x04
		bnd[19] = 1 // ::1
		bnd[21] = 80
		_, _ = conn.Write(bnd)
	default:
		_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 80})
	}

	_ = conn.SetDeadline(time.Time{})
	var pipeWG sync.WaitGroup
	pipeWG.Add(2)
	go func() {
		defer pipeWG.Done()
		_, _ = io.Copy(upstream, conn)
		if tc, ok := upstream.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	go func() {
		defer pipeWG.Done()
		_, _ = io.Copy(conn, upstream)
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	pipeWG.Wait()
}

func (s *mockSOCKS5Server) getRecords() []socks5Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]socks5Record, len(s.records))
	copy(out, s.records)
	return out
}

// --- Unit tests ---

func TestParseAndMaskAssignProxyURLs(t *testing.T) {
	spec, err := ParseAssignProxy("")
	if err != nil || spec != nil {
		t.Fatalf("ParseAssignProxy(\"\") = (%+v, %v), want (nil, nil)", spec, err)
	}
	norm, err := NormalizeAssignProxy("   ")
	if err != nil || norm != "" {
		t.Fatalf("NormalizeAssignProxy(\"   \") = (%q, %v), want (\"\", nil)", norm, err)
	}

	spec, err = ParseAssignProxy("SOCKS5://alice:s3cr%40et@us.proxy.test:1080/")
	if err != nil || spec == nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Scheme != "socks5" || spec.Host != "us.proxy.test" || spec.Port != 1080 {
		t.Fatalf("unexpected parsed fields: %+v", spec)
	}
	if spec.Username != "alice" || spec.Password != "s3cr@et" {
		t.Fatalf("unexpected credentials: user=%q pass=%q", spec.Username, spec.Password)
	}
	if !spec.HasAuth() || !spec.IsSOCKS5() {
		t.Fatalf("expected HasAuth=true and IsSOCKS5=true")
	}
	if spec.NormalizedURL != "socks5://alice:s3cr%40et@us.proxy.test:1080" {
		t.Fatalf("NormalizedURL = %q", spec.NormalizedURL)
	}
	if spec.MaskedURL != "socks5://us.proxy.test:1080" {
		t.Fatalf("MaskedURL = %q", spec.MaskedURL)
	}
	if spec.LogLabel != "SOCKS5 us.proxy.test:1080" {
		t.Fatalf("LogLabel = %q", spec.LogLabel)
	}
	if strings.Contains(spec.MaskedURL, "alice") || strings.Contains(spec.LogLabel, "s3cr") {
		t.Fatalf("credentials leaked into masked/log fields: %+v", spec)
	}

	if got := MaskAssignProxy("http://user:pw@127.0.0.1:7890"); got != "http://127.0.0.1:7890" {
		t.Fatalf("MaskAssignProxy = %q", got)
	}
	if got := ProxyLogLabel("socks5h://[::1]:1081"); got != "SOCKS5 [::1]:1081" {
		t.Fatalf("ProxyLogLabel = %q", got)
	}
	if got := MaskAssignProxy("invalid-proxy"); got != "" {
		t.Fatalf("MaskAssignProxy(invalid) = %q, want empty", got)
	}
	if got := ProxyLogLabel("invalid-proxy"); got != "" {
		t.Fatalf("ProxyLogLabel(invalid) = %q, want empty", got)
	}

	// Default ports and empty port suffix.
	for _, tc := range []struct {
		raw      string
		wantPort int
		wantS5   bool
	}{
		{"http://127.0.0.1", 80, false},
		{"http://127.0.0.1:", 80, false},
		{"http://127.0.0.1:00080", 80, false},
		{"https://proxy.example", 443, false},
		{"socks5://127.0.0.1", 1080, true},
		{"socks5h://127.0.0.1", 1080, true},
	} {
		s, err := ParseAssignProxy(tc.raw)
		if err != nil || s == nil {
			t.Fatalf("ParseAssignProxy(%q) error: %v", tc.raw, err)
		}
		if s.Port != tc.wantPort || s.IsSOCKS5() != tc.wantS5 {
			t.Fatalf("ParseAssignProxy(%q) = port %d socks5=%v, want %d %v", tc.raw, s.Port, s.IsSOCKS5(), tc.wantPort, tc.wantS5)
		}
	}

	// RFC 3986 re-encoding of raw '@' and ':' in password, and empty '@' / ':@' stripping.
	atSpec, err := ParseAssignProxy("http://alice:p@ss@127.0.0.1:8080")
	if err != nil || atSpec == nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atSpec.Password != "p@ss" || atSpec.NormalizedURL != "http://alice:p%40ss@127.0.0.1:8080" {
		t.Fatalf("raw '@' handling mismatch: %+v", atSpec)
	}

	colonPass, err := ParseAssignProxy("http://alice:p:ss@127.0.0.1:8080")
	if err != nil || colonPass == nil || colonPass.Password != "p:ss" || colonPass.NormalizedURL != "http://alice:p%3Ass@127.0.0.1:8080" {
		t.Fatalf("raw ':' in password mismatch: %+v err=%v", colonPass, err)
	}

	for _, rawEmpty := range []string{"http://@127.0.0.1:7890", "http://:@127.0.0.1:7890"} {
		emptyAt, err := ParseAssignProxy(rawEmpty)
		if err != nil || emptyAt == nil {
			t.Fatalf("ParseAssignProxy(%q) error: %v", rawEmpty, err)
		}
		if emptyAt.NormalizedURL != "http://127.0.0.1:7890" || emptyAt.HasAuth() {
			t.Fatalf("ParseAssignProxy(%q) = %+v", rawEmpty, emptyAt)
		}
	}

	// User-only vs user with empty password colon vs password-only.
	userOnly, err := ParseAssignProxy("http://alice@127.0.0.1:7890")
	if err != nil || userOnly.NormalizedURL != "http://alice@127.0.0.1:7890" || !userOnly.HasAuth() {
		t.Fatalf("userOnly = %+v err=%v", userOnly, err)
	}
	userColon, err := ParseAssignProxy("http://alice:@127.0.0.1:7890")
	if err != nil || userColon.NormalizedURL != "http://alice:@127.0.0.1:7890" || !userColon.HasAuth() {
		t.Fatalf("userColon = %+v err=%v", userColon, err)
	}
	passOnly, err := ParseAssignProxy("http://:pw@127.0.0.1:7890")
	if err != nil || passOnly.NormalizedURL != "http://:pw@127.0.0.1:7890" || !passOnly.HasAuth() {
		t.Fatalf("passOnly = %+v err=%v", passOnly, err)
	}

	// IPv6 canonicalization + IDNA domains + trailing dot.
	ipv6Spec, err := ParseAssignProxy("http://[0:0:0:0:0:0:0:1]:8080")
	if err != nil || ipv6Spec.Host != "::1" || ipv6Spec.NormalizedURL != "http://[::1]:8080" || ipv6Spec.LogLabel != "HTTP [::1]:8080" {
		t.Fatalf("ipv6Spec = %+v err=%v", ipv6Spec, err)
	}
	idna1, err := ParseAssignProxy("http://münchen.de:8080")
	if err != nil || idna1.Host != "xn--mnchen-3ya.de" || idna1.NormalizedURL != "http://xn--mnchen-3ya.de:8080" {
		t.Fatalf("idna1 = %+v err=%v", idna1, err)
	}
	idna2, err := ParseAssignProxy("http://例子.测试:8080")
	if err != nil || idna2.Host != "xn--fsqu00a.xn--0zwm56d" {
		t.Fatalf("idna2 = %+v err=%v", idna2, err)
	}
	trailingDot, err := ParseAssignProxy("http://Example.COM.:8080/?#")
	if err != nil || trailingDot.Host != "example.com" || trailingDot.NormalizedURL != "http://example.com:8080" {
		t.Fatalf("trailingDot = %+v err=%v", trailingDot, err)
	}

	// Exact 255-byte boundary for SOCKS5 username/password.
	u255 := strings.Repeat("u", 255)
	if _, err := ParseAssignProxy("socks5://" + u255 + ":" + strings.Repeat("p", 255) + "@127.0.0.1:1080"); err != nil {
		t.Fatalf("255-byte socks5 credentials should pass: %v", err)
	}
	// 127 x 'ü' = 254 UTF-8 bytes -> valid.
	if _, err := ParseAssignProxy("socks5://" + strings.Repeat("ü", 127) + ":pw@127.0.0.1:1080"); err != nil {
		t.Fatalf("254-byte utf-8 socks5 username should pass: %v", err)
	}
	// HTTP proxy is not limited by RFC 1929's 255-byte credential limit.
	if _, err := ParseAssignProxy("http://" + strings.Repeat("u", 300) + ":pw@127.0.0.1:8080"); err != nil {
		t.Fatalf("http proxy long username should pass: %v", err)
	}
}

func TestParseAssignProxyRejectsInvalidURLs(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr string
	}{
		{"127.0.0.1:7890", "代理地址请包含协议前缀，例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080"},
		{"ftp://127.0.0.1:21", "仅支持 http://、https://、socks5:// 或 socks5h:// 代理协议"},
		{"http://127.0.0.1:99999", "代理端口必须是 1 至 65535 之间的整数"},
		{"http://127.0.0.1:0", "代理端口必须是 1 至 65535 之间的整数"},
		{"http://127.0.0.1:-80", "代理端口必须是 1 至 65535 之间的整数"},
		{"http://127.0.0.1:+80", "代理端口必须是 1 至 65535 之间的整数"},
		{"http://127.0.0.1:abc", "代理端口必须是 1 至 65535 之间的整数"},
		{"http://127.0.0.1:7890/path", "代理地址不应包含路径或查询参数"},
		{"http://127.0.0.1:7890//", "代理地址不应包含路径或查询参数"},
		{"http://127.0.0.1:7890?a=1", "代理地址不应包含路径或查询参数"},
		{"http://127.0.0.1:7890#frag", "代理地址不应包含路径或查询参数"},
		{"socks5://127.0.0.1:1080 bad", "代理地址不能包含空格或控制字符"},
		{"socks5://127.0.0.1:1080\x00", "代理地址不能包含空格或控制字符"},
		{"socks5://127.0.0.1:1080\x7f", "代理地址不能包含空格或控制字符"},
		{"socks5://:1080", "代理地址缺少主机名或 IP"},
		{"http://..:8080", "代理地址缺少主机名或 IP"},
		{"http://-bad.example:8080", "代理地址缺少主机名或 IP"},
		{"http://bad-.example:8080", "代理地址缺少主机名或 IP"},
		{"http://a..b:8080", "代理地址缺少主机名或 IP"},
		{"http://[fe80::1%25lo0]:8080", "代理地址缺少主机名或 IP"},
		{"http://[fe80::1%lo0]:8080", "代理地址缺少主机名或 IP"},
		{"http://::1:8080", "代理地址缺少主机名或 IP"},
		{"http://[::1:8080", "代理地址缺少主机名或 IP"},
		{"socks5://" + strings.Repeat("u", 256) + ":pw@127.0.0.1:1080", "SOCKS5 代理用户名或密码长度不能超过 255 字节"},
		{"socks5://u:" + strings.Repeat("p", 256) + "@127.0.0.1:1080", "SOCKS5 代理用户名或密码长度不能超过 255 字节"},
		{"socks5h://" + strings.Repeat("ü", 128) + ":pw@127.0.0.1:1080", "SOCKS5 代理用户名或密码长度不能超过 255 字节"},
	}

	for _, tc := range cases {
		spec, err := ParseAssignProxy(tc.raw)
		if err == nil {
			t.Errorf("ParseAssignProxy(%q) succeeded (%+v), want error %q", tc.raw, spec, tc.wantErr)
			continue
		}
		if err.Error() != tc.wantErr {
			t.Errorf("ParseAssignProxy(%q) error = %q, want %q", tc.raw, err.Error(), tc.wantErr)
		}
	}
}

func TestHTTPProxyProbeAndAuthOffline(t *testing.T) {
	target := startMockHTTPTarget(t, nil)
	proxy := startMockHTTPProxy(t, "colabuser", "topsecret", "")

	// Empty proxy URL error.
	if _, err := TestAssignProxy(context.Background(), "   ", target.server.URL+"/robots.txt", 5*time.Second); err == nil || err.Error() != "请先填写分配代理地址" {
		t.Fatalf("empty proxy error = %v, want 请先填写分配代理地址", err)
	}

	probeURL := target.server.URL + "/robots.txt"
	res, err := TestAssignProxy(
		context.Background(),
		fmt.Sprintf("http://colabuser:topsecret@127.0.0.1:%d", proxy.port),
		probeURL,
		5*time.Second,
	)
	if err != nil {
		t.Fatalf("TestAssignProxy failed: %v", err)
	}
	if res["ok"] != true {
		t.Fatalf("ok = %v, want true", res["ok"])
	}
	if res["status_code"] != 200 {
		t.Fatalf("status_code = %v, want 200", res["status_code"])
	}
	wantLabel := fmt.Sprintf("HTTP 127.0.0.1:%d", proxy.port)
	if res["proxy"] != wantLabel {
		t.Fatalf("proxy = %v, want %q", res["proxy"], wantLabel)
	}
	wantMasked := fmt.Sprintf("http://127.0.0.1:%d", proxy.port)
	if res["masked_url"] != wantMasked {
		t.Fatalf("masked_url = %v, want %q", res["masked_url"], wantMasked)
	}
	if ms, ok := res["latency_ms"].(int); !ok || ms < 1 {
		t.Fatalf("latency_ms = %v, want int >= 1", res["latency_ms"])
	}
	if hits := target.getHits(); len(hits) != 1 || hits[0] != "/robots.txt" {
		t.Fatalf("target hits = %v, want [/robots.txt]", hits)
	}
	if auths := target.getAuths(); len(auths) != 1 || auths[0] != "" {
		t.Fatalf("probe must not send OAuth Authorization header, got %v", auths)
	}
	if seen := proxy.getSeen(); len(seen) != 1 {
		t.Fatalf("proxy seen count = %d, want 1", len(seen))
	}

	// Wrong password -> 407 auth failure.
	_, err = TestAssignProxy(
		context.Background(),
		fmt.Sprintf("http://colabuser:wrongpass@127.0.0.1:%d", proxy.port),
		probeURL,
		5*time.Second,
	)
	if err == nil {
		t.Fatalf("expected 407 auth error")
	}
	if err.Error() != "GPU 分配代理认证失败，请检查代理用户名与密码后重试" {
		t.Fatalf("unexpected 407 error: %q", err.Error())
	}
	if !errors.Is(err, ErrProxyAuth) {
		t.Fatalf("expected errors.Is(err, ErrProxyAuth)")
	}
}

func TestHTTPConnectTunnelTLSAndAuthOffline(t *testing.T) {
	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok-tls"))
	}))
	t.Cleanup(tlsTarget.Close)

	proxy := startMockHTTPProxy(t, "u1", "p@ss", "Custom Auth Needed")

	// 1) Untrusted self-signed cert MUST fail TLS verification (InsecureSkipVerify=false).
	_, err := TestAssignProxy(
		context.Background(),
		fmt.Sprintf("http://u1:p%%40ss@127.0.0.1:%d", proxy.port),
		tlsTarget.URL+"/robots.txt",
		5*time.Second,
	)
	if err == nil {
		t.Fatalf("expected TLS verification failure against self-signed server")
	}
	wantTLSMsg := "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理"
	if err.Error() != wantTLSMsg {
		t.Fatalf("TLS error = %q, want %q", err.Error(), wantTLSMsg)
	}
	if !errors.Is(err, ErrProxyTLS) {
		t.Fatalf("expected errors.Is(err, ErrProxyTLS)")
	}

	// 2) Trusted test CA pool succeeds end-to-end through HTTP CONNECT (still InsecureSkipVerify=false).
	pool := x509.NewCertPool()
	pool.AddCert(tlsTarget.Certificate())
	res, err := testAssignProxyWithRoots(
		context.Background(),
		fmt.Sprintf("http://u1:p%%40ss@127.0.0.1:%d", proxy.port),
		tlsTarget.URL+"/robots.txt",
		5*time.Second,
		pool,
	)
	if err != nil {
		t.Fatalf("trusted TLS through HTTP CONNECT failed: %v", err)
	}
	if res["ok"] != true || res["status_code"] != 200 {
		t.Fatalf("unexpected probe result: %+v", res)
	}

	// 3) Wrong password on HTTPS CONNECT with custom 407 reason still classified as ErrProxyAuth.
	_, err = testAssignProxyWithRoots(
		context.Background(),
		fmt.Sprintf("http://u1:bad@127.0.0.1:%d", proxy.port),
		tlsTarget.URL+"/robots.txt",
		5*time.Second,
		pool,
	)
	if err == nil || !errors.Is(err, ErrProxyAuth) {
		t.Fatalf("expected ErrProxyAuth on CONNECT 407, got: %v", err)
	}
	if err.Error() != "GPU 分配代理认证失败，请检查代理用户名与密码后重试" {
		t.Fatalf("unexpected CONNECT 407 error message: %q", err.Error())
	}
}

func TestSOCKS5ProxyProbeRemoteDNSAndAuthOffline(t *testing.T) {
	target := startMockHTTPTarget(t, nil)
	targetAddr := strings.TrimPrefix(target.server.URL, "http://")

	s5 := startMockSOCKS5Server(t, mockSOCKS5Options{
		targetAddr:  targetAddr,
		reqUser:     "u1",
		reqPass:     "p@ss",
		requireAuth: true,
	})

	// Use an unresolvable domain name to prove BOTH socks5:// and socks5h:// send
	// ATYP=0x03 (remote DNS) instead of resolving locally.
	for _, scheme := range []string{"socks5", "socks5h"} {
		res, err := TestAssignProxy(
			context.Background(),
			fmt.Sprintf("%s://u1:p%%40ss@127.0.0.1:%d", scheme, s5.port),
			"http://colab-offline-remote-dns.invalid:80/robots.txt",
			5*time.Second,
		)
		if err != nil {
			t.Fatalf("%s probe failed: %v", scheme, err)
		}
		if res["ok"] != true || res["status_code"] != 200 {
			t.Fatalf("%s unexpected result: %+v", scheme, res)
		}
		if res["proxy"] != fmt.Sprintf("SOCKS5 127.0.0.1:%d", s5.port) {
			t.Fatalf("%s proxy label = %v", scheme, res["proxy"])
		}
	}

	records := s5.getRecords()
	if len(records) != 2 {
		t.Fatalf("socks5 records = %+v, want 2", records)
	}
	for i, rec := range records {
		if rec.atyp != 0x03 || rec.host != "colab-offline-remote-dns.invalid" || rec.port != 80 {
			t.Fatalf("record[%d] = %+v, want atyp=3 host=colab-offline-remote-dns.invalid port=80", i, rec)
		}
	}
	if hits := target.getHits(); len(hits) != 2 || hits[0] != "/robots.txt" || hits[1] != "/robots.txt" {
		t.Fatalf("target hits = %v", hits)
	}

	// Wrong password fails with auth error.
	_, err := TestAssignProxy(
		context.Background(),
		fmt.Sprintf("socks5://u1:bad@127.0.0.1:%d", s5.port),
		"http://colab-offline-remote-dns.invalid:80/robots.txt",
		5*time.Second,
	)
	if err == nil || !errors.Is(err, ErrProxyAuth) || !strings.Contains(err.Error(), "认证失败") {
		t.Fatalf("expected SOCKS5 auth failure, got: %v", err)
	}

	// Missing credentials against auth-required server fails with auth error (method 0xFF).
	_, err = TestAssignProxy(
		context.Background(),
		fmt.Sprintf("socks5://127.0.0.1:%d", s5.port),
		"http://colab-offline-remote-dns.invalid:80/robots.txt",
		5*time.Second,
	)
	if err == nil || !errors.Is(err, ErrProxyAuth) || !strings.Contains(err.Error(), "认证失败") {
		t.Fatalf("expected SOCKS5 auth rejection, got: %v", err)
	}
}

func TestSOCKS5AddressTypesTLSAndBNDDraining(t *testing.T) {
	target := startMockHTTPTarget(t, nil)
	targetAddr := strings.TrimPrefix(target.server.URL, "http://")

	// Test IPv4 (0x01), IPv6 (0x04), and domain (0x03) target encoding + BND.ADDR types.
	for _, bndType := range []byte{0x01, 0x03, 0x04} {
		s5 := startMockSOCKS5Server(t, mockSOCKS5Options{
			targetAddr:   targetAddr,
			replyBNDType: bndType,
		})
		spec, err := ParseAssignProxy(fmt.Sprintf("socks5://127.0.0.1:%d", s5.port))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		for _, targetTC := range []struct {
			host     string
			port     int
			wantATYP byte
			wantHost string
		}{
			{"127.0.0.1", 80, 0x01, "127.0.0.1"},
			{"::1", 443, 0x04, "::1"},
			{"münchen.de", 443, 0x03, "xn--mnchen-3ya.de"},
		} {
			conn, err := dialSOCKS5(context.Background(), *spec, targetTC.host, targetTC.port, 5*time.Second)
			if err != nil {
				t.Fatalf("bndType=%d target=%s dialSOCKS5 failed: %v", bndType, targetTC.host, err)
			}
			_ = conn.Close()
		}
		recs := s5.getRecords()
		if len(recs) != 3 || recs[0].atyp != 0x01 || recs[1].atyp != 0x04 || recs[2].atyp != 0x03 || recs[2].host != "xn--mnchen-3ya.de" {
			t.Fatalf("bndType=%d unexpected records: %+v", bndType, recs)
		}
	}

	// TLS verification through SOCKS5.
	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(tlsTarget.Close)
	tlsAddr := strings.TrimPrefix(tlsTarget.URL, "https://")
	s5TLS := startMockSOCKS5Server(t, mockSOCKS5Options{targetAddr: tlsAddr})

	_, err := TestAssignProxy(
		context.Background(),
		fmt.Sprintf("socks5://127.0.0.1:%d", s5TLS.port),
		tlsTarget.URL+"/robots.txt",
		5*time.Second,
	)
	if err == nil || !errors.Is(err, ErrProxyTLS) {
		t.Fatalf("expected TLS verification error over SOCKS5, got: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(tlsTarget.Certificate())
	res, err := testAssignProxyWithRoots(
		context.Background(),
		fmt.Sprintf("socks5://127.0.0.1:%d", s5TLS.port),
		tlsTarget.URL+"/robots.txt",
		5*time.Second,
		pool,
	)
	if err != nil || res["ok"] != true {
		t.Fatalf("expected trusted TLS over SOCKS5 to succeed, got res=%+v err=%v", res, err)
	}
}

func TestSOCKS5ReplyReasonsShortErrorHeaderAndProtocolErrors(t *testing.T) {
	// Every RFC 1928 reply reason (0x01..0x08) + unknown code (0x09), tested with
	// short 4-byte error headers followed by immediate close.
	for code := byte(0x01); code <= 0x09; code++ {
		s5 := startMockSOCKS5Server(t, mockSOCKS5Options{
			replyCode:      code,
			shortErrHeader: true,
		})
		spec, err := ParseAssignProxy(fmt.Sprintf("socks5://127.0.0.1:%d", s5.port))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		conn, err := dialSOCKS5(context.Background(), *spec, "127.0.0.1", 9, 3*time.Second)
		if conn != nil {
			_ = conn.Close()
		}
		if err == nil {
			t.Fatalf("code 0x%02x: expected error", code)
		}
		wantReason, ok := socks5ReplyReasons[code]
		if !ok {
			wantReason = fmt.Sprintf("SOCKS5 代理拒绝连接 (代码 %d)", code)
		}
		if !strings.Contains(err.Error(), wantReason) {
			t.Errorf("code 0x%02x: error = %q, want substring %q", code, err.Error(), wantReason)
		}
	}

	// Protocol error edge cases.
	protoCases := []struct {
		name      string
		opts      mockSOCKS5Options
		wantMatch string
		wantKind  error
	}{
		{"eof_during_handshake", mockSOCKS5Options{closeAfterGreet: true}, "代理连接在握手完成前中断", ErrProxyConnect},
		{"bad_greet_ver", mockSOCKS5Options{overrideVer: 0x04}, "非法的 SOCKS5 协议版本响应：4", ErrProxyConnect},
		{"unsolicited_userpass", mockSOCKS5Options{overrideMethod: 0x02}, "SOCKS5 代理要求用户名与密码认证", ErrProxyAuth},
		{"unsupported_method", mockSOCKS5Options{overrideMethod: 0x03}, "不支持的 SOCKS5 认证方式：3", ErrProxyConnect},
		{"bad_reply_ver", mockSOCKS5Options{overrideRepVer: 0x04}, "非法的 SOCKS5 连接响应版本：4", ErrProxyConnect},
		{"bad_bnd_atyp", mockSOCKS5Options{replyBNDType: 0x09}, "非法的 SOCKS5 绑定地址类型：9", ErrProxyConnect},
	}
	for _, tc := range protoCases {
		t.Run(tc.name, func(t *testing.T) {
			s5 := startMockSOCKS5Server(t, tc.opts)
			spec, _ := ParseAssignProxy(fmt.Sprintf("socks5://127.0.0.1:%d", s5.port))
			conn, err := dialSOCKS5(context.Background(), *spec, "example.invalid", 443, 3*time.Second)
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantMatch)
			}
			if !strings.Contains(err.Error(), tc.wantMatch) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantMatch)
			}
			if !errors.Is(err, tc.wantKind) {
				t.Fatalf("expected errors.Is(err, %v), got %v", tc.wantKind, err)
			}
		})
	}
}

func TestSOCKS5TimeoutAndContextCancellation(t *testing.T) {
	s5 := startMockSOCKS5Server(t, mockSOCKS5Options{hangForever: true})
	spec, _ := ParseAssignProxy(fmt.Sprintf("socks5://127.0.0.1:%d", s5.port))

	// Timeout case.
	_, err := dialSOCKS5(context.Background(), *spec, "example.invalid", 443, 150*time.Millisecond)
	if err == nil || !errors.Is(err, ErrProxyTimeout) {
		t.Fatalf("expected ErrProxyTimeout, got: %v", err)
	}
	if formatted := FormatProxyError(err); formatted != "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性" {
		t.Fatalf("FormatProxyError(timeout) = %q", formatted)
	}

	// Explicit context cancellation unblocks promptly.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	_, err = dialSOCKS5(ctx, *spec, "example.invalid", 443, 5*time.Second)
	elapsed := time.Since(started)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("cancel took too long: %v", elapsed)
	}
}

func TestAssignOnlyTransportEnforcesOriginPathRedirectsTLSAndEnvIsolation(t *testing.T) {
	// Poison process proxy environment variables to guarantee complete isolation.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1")

	if _, err := NewAssignOnlyTransport(nil, "", ""); err == nil || err.Error() != "请先填写分配代理地址" {
		t.Fatalf("nil spec error = %v", err)
	}

	// Default origin enforcement: https://colab.research.google.com/tun/m/assign
	spec, err := ParseAssignProxy("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	defaultTr, err := NewAssignOnlyTransport(spec, "", "")
	if err != nil {
		t.Fatalf("NewAssignOnlyTransport: %v", err)
	}
	defer defaultTr.Close()
	if s, h := defaultTr.AllowedOrigin(); s != "https" || h != "colab.research.google.com" {
		t.Fatalf("AllowedOrigin = (%q, %q)", s, h)
	}

	forbiddenURLs := []string{
		"http://colab.research.google.com/tun/m/assign",
		"https://evil.example/tun/m/assign",
		"https://colab.research.google.com/tun/m/assignments",
		"https://colab.research.google.com/tun/m/unassign/m-s-123",
		"https://colab.research.google.com/tun/m/ccu-info",
		"https://colab.research.google.com/tun/m/m-s-123/keep-alive/",
		"https://runtime.invalid/api/kernels",
		"https://colab.research.google.com/tun/m/assign/../assignments",
	}
	client := defaultTr.NewClient(5 * time.Second)
	for _, forbidden := range forbiddenURLs {
		resp, err := client.Get(forbidden)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatalf("expected forbidden URL %q to be rejected", forbidden)
		}
		if !errors.Is(err, ErrForbiddenTarget) || !strings.Contains(err.Error(), "仅 GPU 分配请求允许使用分配代理") {
			t.Fatalf("forbidden URL %q error = %v, want ErrForbiddenTarget", forbidden, err)
		}
	}

	// Live offline mock server testing allowed path, query params, and redirect blocking.
	var otherHits int
	var mu sync.Mutex
	target := startMockHTTPTarget(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tun/m/assign", "/tun/m/assign/":
			switch r.URL.Query().Get("mode") {
			case "redirect-evil":
				http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
			case "redirect-other-path":
				http.Redirect(w, r, "/tun/m/ccu-info", http.StatusFound)
			case "redirect-assign":
				http.Redirect(w, r, "/tun/m/assign?nbh=final", http.StatusFound)
			default:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}
		default:
			mu.Lock()
			otherHits++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}
	})
	targetURL, _ := url.Parse(target.server.URL)
	targetAddr := targetURL.Host

	// Test BOTH HTTP proxy and SOCKS5 proxy transports.
	httpProxy := startMockHTTPProxy(t, "", "", "")
	s5Proxy := startMockSOCKS5Server(t, mockSOCKS5Options{targetAddr: targetAddr})

	proxyURLs := []string{
		fmt.Sprintf("http://127.0.0.1:%d", httpProxy.port),
		fmt.Sprintf("socks5://127.0.0.1:%d", s5Proxy.port),
	}

	for _, pURL := range proxyURLs {
		pSpec, err := ParseAssignProxy(pURL)
		if err != nil {
			t.Fatalf("parse %q: %v", pURL, err)
		}
		tr, err := NewAssignOnlyTransport(pSpec, "http", targetURL.Host)
		if err != nil {
			t.Fatalf("NewAssignOnlyTransport(%q): %v", pURL, err)
		}
		defer tr.Close()

		// Using plain &http.Client{Transport: tr} (even without setting CheckRedirect)
		// must allow /tun/m/assign and block any redirect leaving /tun/m/assign.
		c := &http.Client{Transport: tr, Timeout: 5 * time.Second}

		// Allowed: /tun/m/assign?nbh=abc
		resp, err := c.Get(target.server.URL + "/tun/m/assign?nbh=abc")
		if err != nil {
			t.Fatalf("%s allowed GET failed: %v", pURL, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != `{"ok":true}` {
			t.Fatalf("%s unexpected response: status=%d body=%q", pURL, resp.StatusCode, string(body))
		}

		// Allowed: trailing slash /tun/m/assign/
		resp, err = c.Get(target.server.URL + "/tun/m/assign/")
		if err != nil {
			t.Fatalf("%s trailing slash GET failed: %v", pURL, err)
		}
		_ = resp.Body.Close()

		// Allowed redirect staying on /tun/m/assign.
		resp, err = c.Get(target.server.URL + "/tun/m/assign?mode=redirect-assign")
		if err != nil {
			t.Fatalf("%s same-path redirect failed: %v", pURL, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || resp.Request.URL.RawQuery != "nbh=final" {
			t.Fatalf("%s unexpected redirect final URL: %v", pURL, resp.Request.URL)
		}

		// Blocked redirect to external host.
		resp, err = c.Get(target.server.URL + "/tun/m/assign?mode=redirect-evil")
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil || !errors.Is(err, ErrForbiddenTarget) {
			t.Fatalf("%s expected ErrForbiddenTarget on external redirect, got: %v", pURL, err)
		}

		// Blocked redirect to sibling path on same host.
		resp, err = c.Get(target.server.URL + "/tun/m/assign?mode=redirect-other-path")
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil || !errors.Is(err, ErrForbiddenTarget) {
			t.Fatalf("%s expected ErrForbiddenTarget on sibling-path redirect, got: %v", pURL, err)
		}
	}

	mu.Lock()
	if otherHits != 0 {
		t.Fatalf("non-assign path was hit %d times; redirects must never leave /tun/m/assign", otherHits)
	}
	mu.Unlock()

	// Verify TLS verification is strictly enforced on ColabOneAssignOnlyTransport.
	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(tlsTarget.Close)
	tlsURL, _ := url.Parse(tlsTarget.URL)
	httpSpec, _ := ParseAssignProxy(fmt.Sprintf("http://127.0.0.1:%d", httpProxy.port))
	tlsTr, err := NewAssignOnlyTransport(httpSpec, "https", tlsURL.Host)
	if err != nil {
		t.Fatalf("NewAssignOnlyTransport TLS: %v", err)
	}
	defer tlsTr.Close()
	tlsClient := tlsTr.NewClient(5 * time.Second)
	_, err = tlsClient.Get(tlsTarget.URL + "/tun/m/assign")
	if err == nil {
		t.Fatalf("expected TLS verification error on untrusted assign endpoint")
	}
	if FormatProxyError(err) != "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理" {
		t.Fatalf("unexpected formatted TLS error: %q (raw=%v)", FormatProxyError(err), err)
	}
	if !IsProxyFailFastError(err) {
		t.Fatalf("TLS cert error must be fail-fast")
	}
}

func TestFormatProxyErrorAndFailFastClassification(t *testing.T) {
	// TLS cert errors
	tlsErr := &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}
	if got := FormatProxyError(tlsErr); got != "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理" {
		t.Fatalf("FormatProxyError(tls) = %q", got)
	}
	if !IsProxyFailFastError(tlsErr) {
		t.Fatalf("TLS error should be fail-fast")
	}

	// Auth errors
	for _, err := range []error{
		&ProxyError{Kind: ErrProxyAuth, Message: "SOCKS5 代理用户名或密码认证失败"},
		errors.New("proxyconnect tcp: 407 Proxy Authentication Required"),
		errors.New("Proxy Authentication Required"),
		errors.New("代理认证失败"),
	} {
		if got := FormatProxyError(err); got != "GPU 分配代理认证失败，请检查代理用户名与密码后重试" {
			t.Fatalf("FormatProxyError(%v) = %q", err, got)
		}
	}

	// Timeout errors
	for _, err := range []error{
		&ProxyError{Kind: ErrProxyTimeout, Message: "连接 SOCKS5 代理超时"},
		context.DeadlineExceeded,
		errors.New("SOCKS5 代理连接超时 (TTL expired)"),
		errors.New("dial tcp 127.0.0.1:1080: i/o timeout"),
	} {
		if got := FormatProxyError(err); got != "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性" {
			t.Fatalf("FormatProxyError(%v) = %q", err, got)
		}
	}

	// Forbidden target preserved verbatim
	if got := FormatProxyError(&ProxyError{Kind: ErrForbiddenTarget, Message: ErrForbiddenTarget.Error()}); got != "仅 GPU 分配请求允许使用分配代理" {
		t.Fatalf("FormatProxyError(forbidden) = %q", got)
	}

	// Generic & nil fallback
	for _, err := range []error{nil, errors.New("connection refused")} {
		if got := FormatProxyError(err); got != "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试" {
			t.Fatalf("FormatProxyError(%v) = %q", err, got)
		}
	}

	// Fail-fast vs retryable read timeout distinction
	connectTimeoutErr := &url.Error{
		Op:  "Get",
		URL: "https://colab.research.google.com/tun/m/assign",
		Err: &ProxyError{Kind: ErrProxyTimeout, Message: "连接代理超时"},
	}
	if !IsProxyFailFastError(connectTimeoutErr) {
		t.Fatalf("proxy connect timeout must fail fast")
	}
	readTimeoutErr := &url.Error{
		Op:  "Get",
		URL: "https://colab.research.google.com/tun/m/assign",
		Err: errors.New("net/http: timeout awaiting response headers"),
	}
	if IsProxyFailFastError(readTimeoutErr) {
		t.Fatalf("upstream read timeout must remain retryable (not fail-fast)")
	}
}

func TestTestAssignProxyDoesNotFollowRedirects(t *testing.T) {
	target := startMockHTTPTarget(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/should-not-follow", http.StatusFound)
	})
	proxy := startMockHTTPProxy(t, "", "", "")

	res, err := TestAssignProxy(
		context.Background(),
		fmt.Sprintf("http://127.0.0.1:%d", proxy.port),
		target.server.URL+"/robots.txt",
		5*time.Second,
	)
	if err != nil {
		t.Fatalf("TestAssignProxy on 302 failed: %v", err)
	}
	if res["ok"] != true || res["status_code"] != http.StatusFound {
		t.Fatalf("unexpected 302 result: %+v", res)
	}
	if hits := target.getHits(); len(hits) != 1 || hits[0] != "/robots.txt" {
		t.Fatalf("probe must not follow redirects, hits=%v", hits)
	}
}

func TestSOCKS5DirectHTTPRequestLineOverTunnel(t *testing.T) {
	// Verify raw HTTP/1.1 request passes cleanly through dialSOCKS5 after deadline reset.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		line, _ := br.ReadString('\n')
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: " + strconv.Itoa(len(line)) + "\r\n\r\n" + line))
	}()

	s5 := startMockSOCKS5Server(t, mockSOCKS5Options{targetAddr: ln.Addr().String()})
	spec, _ := ParseAssignProxy(fmt.Sprintf("socks5://127.0.0.1:%d", s5.port))
	conn, err := dialSOCKS5(context.Background(), *spec, "colab-sentinel.invalid", 80, 3*time.Second)
	if err != nil {
		t.Fatalf("dialSOCKS5: %v", err)
	}
	defer conn.Close()

	_, err = conn.Write([]byte("GET /ping HTTP/1.1\r\nHost: colab-sentinel.invalid\r\n\r\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(body), "GET /ping HTTP/1.1") {
		t.Fatalf("unexpected raw tunnel response: %d %q", resp.StatusCode, string(body))
	}
}

func TestHTTPSProxyTLSAndTunnelOffline(t *testing.T) {
	target := startMockHTTPTarget(t, nil)

	// Run an HTTPS proxy (TLS between client and proxy).
	httpsProxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.RequestURI, r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		upClient := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
		resp, err := upClient.Do(upReq)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(httpsProxy.Close)

	// Untrusted HTTPS proxy cert MUST be rejected (InsecureSkipVerify=false).
	_, err := TestAssignProxy(context.Background(), httpsProxy.URL, target.server.URL+"/robots.txt", 5*time.Second)
	if err == nil || !errors.Is(err, ErrProxyTLS) {
		t.Fatalf("expected ErrProxyTLS for untrusted https proxy, got: %v", err)
	}
	if err.Error() != "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理" {
		t.Fatalf("unexpected untrusted https proxy error: %q", err.Error())
	}

	// Trusted HTTPS proxy cert succeeds.
	pool := x509.NewCertPool()
	pool.AddCert(httpsProxy.Certificate())
	res, err := testAssignProxyWithRoots(context.Background(), httpsProxy.URL, target.server.URL+"/robots.txt", 5*time.Second, pool)
	if err != nil || res["ok"] != true || res["status_code"] != 200 {
		t.Fatalf("trusted https proxy probe failed: res=%+v err=%v", res, err)
	}
	if !strings.HasPrefix(fmt.Sprint(res["proxy"]), "HTTPS 127.0.0.1:") {
		t.Fatalf("unexpected https proxy log label: %v", res["proxy"])
	}
}

func TestDomainLengthBoundariesAndManualSpecValidation(t *testing.T) {
	// 63-char label valid, 64-char label invalid.
	label63 := strings.Repeat("a", 63)
	if _, err := ParseAssignProxy("http://" + label63 + ".example:8080"); err != nil {
		t.Fatalf("63-char label should pass: %v", err)
	}
	label64 := strings.Repeat("a", 64)
	if _, err := ParseAssignProxy("http://" + label64 + ".example:8080"); err == nil || err.Error() != "代理地址缺少主机名或 IP" {
		t.Fatalf("64-char label error = %v", err)
	}

	// 253-char host valid, 254-char host invalid.
	// 4 * 62 + 3 dots = 251 + ".x" = 253.
	host253 := strings.Repeat(strings.Repeat("a", 62)+".", 4) + "x"
	if len(host253) != 253 {
		t.Fatalf("test setup error: len=%d", len(host253))
	}
	if _, err := ParseAssignProxy("http://" + host253 + ":8080"); err != nil {
		t.Fatalf("253-char host should pass: %v", err)
	}
	host254 := host253 + "y"
	if _, err := ParseAssignProxy("http://" + host254 + ":8080"); err == nil || err.Error() != "代理地址缺少主机名或 IP" {
		t.Fatalf("254-char host error = %v", err)
	}

	// International domain names (Japanese, Korean, Chinese, German).
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"http://münchen.de:8080", "xn--mnchen-3ya.de"},
		{"http://例子.测试:8080", "xn--fsqu00a.xn--0zwm56d"},
		{"http://日本語.jp:8080", "xn--wgv71a119e.jp"},
		{"http://bücher.example:8080", "xn--bcher-kva.example"},
	} {
		s, err := ParseAssignProxy(tc.raw)
		if err != nil || s.Host != tc.want {
			t.Fatalf("ParseAssignProxy(%q) = (%+v, %v), want host %q", tc.raw, s, err, tc.want)
		}
	}

	// Manually constructed spec (without NormalizedURL) validates properly in NewAssignOnlyTransport.
	manual := &ColabOneAssignProxySpec{Scheme: "socks5", Host: "127.0.0.1", Port: 1080}
	tr, err := NewAssignOnlyTransport(manual, "", "")
	if err != nil {
		t.Fatalf("manual spec transport error: %v", err)
	}
	defer tr.Close()
	if tr.Spec().LogLabel != "SOCKS5 127.0.0.1:1080" || tr.Spec().NormalizedURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("unexpected manual spec normalized fields: %+v", tr.Spec())
	}

	// Manually constructed invalid spec rejected.
	if _, err := NewAssignOnlyTransport(&ColabOneAssignProxySpec{Scheme: "ftp", Host: "127.0.0.1", Port: 21}, "", ""); err == nil {
		t.Fatalf("expected invalid scheme rejection")
	}
	if _, err := NewAssignOnlyTransport(&ColabOneAssignProxySpec{Scheme: "socks5", Host: "127.0.0.1", Port: 0}, "", ""); err == nil {
		t.Fatalf("expected invalid port rejection")
	}
}

func TestAssignOnlyTransportEdgeRedirectsAndConcurrentRequests(t *testing.T) {
	target := startMockHTTPTarget(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("case") {
		case "rel":
			w.Header().Set("Location", "../ccu-info")
			w.WriteHeader(http.StatusFound)
		case "proto-rel":
			w.Header().Set("Location", "//evil.example/tun/m/assign")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case "bad-loc":
			w.Header().Set("Location", "://bad-url")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}
	})
	targetURL, _ := url.Parse(target.server.URL)
	s5 := startMockSOCKS5Server(t, mockSOCKS5Options{targetAddr: targetURL.Host})
	spec, _ := ParseAssignProxy(fmt.Sprintf("socks5://127.0.0.1:%d", s5.port))
	tr, err := NewAssignOnlyTransport(spec, "http", targetURL.Host)
	if err != nil {
		t.Fatalf("NewAssignOnlyTransport: %v", err)
	}
	defer tr.Close()

	// Nil request and encoded path tricks rejected.
	if _, err := tr.RoundTrip(nil); !errors.Is(err, ErrForbiddenTarget) {
		t.Fatalf("nil req error = %v", err)
	}
	encodedReq, _ := http.NewRequest(http.MethodGet, target.server.URL+"/tun/m/%61ssign", nil)
	if _, err := tr.RoundTrip(encodedReq); !errors.Is(err, ErrForbiddenTarget) {
		t.Fatalf("encoded path req error = %v", err)
	}

	// Wrong port rejected when allowedHost includes explicit port.
	wrongPortReq, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/tun/m/assign", nil)
	if _, err := tr.RoundTrip(wrongPortReq); !errors.Is(err, ErrForbiddenTarget) {
		t.Fatalf("wrong port req error = %v", err)
	}

	// Edge redirect targets rejected.
	client := tr.NewClient(5 * time.Second)
	for _, q := range []string{"rel", "proto-rel", "bad-loc"} {
		resp, err := client.Get(target.server.URL + "/tun/m/assign?case=" + q)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if !errors.Is(err, ErrForbiddenTarget) {
			t.Fatalf("redirect case %q error = %v, want ErrForbiddenTarget", q, err)
		}
	}

	// Concurrent requests through shared ColabOneAssignOnlyTransport.
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := client.Get(fmt.Sprintf("%s/tun/m/assign?worker=%d", target.server.URL, idx))
			if err != nil {
				errs <- err
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				errs <- fmt.Errorf("worker %d status=%d", idx, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent assign request failed: %v", err)
	}
}

func TestTestAssignProxyTimeoutUnreachableAndInvalidURL(t *testing.T) {
	// Invalid proxy URL returns exact Chinese validation error untouched.
	_, err := TestAssignProxy(context.Background(), "ftp://127.0.0.1:21", "", 2*time.Second)
	if err == nil || err.Error() != "仅支持 http://、https://、socks5:// 或 socks5h:// 代理协议" {
		t.Fatalf("invalid proxy url error = %v", err)
	}

	// Hanging proxy times out with exact timeout message.
	hang := startMockSOCKS5Server(t, mockSOCKS5Options{hangForever: true})
	_, err = TestAssignProxy(
		context.Background(),
		fmt.Sprintf("socks5://127.0.0.1:%d", hang.port),
		"http://colab-timeout.invalid/robots.txt",
		150*time.Millisecond,
	)
	if err == nil || !errors.Is(err, ErrProxyTimeout) || err.Error() != "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性" {
		t.Fatalf("hanging proxy error = %v", err)
	}

	// Unreachable closed port fails fast with generic connection failure message.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	_, err = TestAssignProxy(
		context.Background(),
		fmt.Sprintf("http://127.0.0.1:%d", closedPort),
		"http://127.0.0.1:80/robots.txt",
		2*time.Second,
	)
	if err == nil || !errors.Is(err, ErrProxyConnect) {
		t.Fatalf("closed port error = %v", err)
	}
	if err.Error() != "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试" {
		t.Fatalf("unexpected closed port error message: %q", err.Error())
	}

	// Regression: TCP port numbers containing "407" must never be misclassified as HTTP 407 auth failures.
	for _, sample := range []string{
		"dial tcp 127.0.0.1:40712: connect: connection refused",
		"dial tcp 127.0.0.1:407: connect: connection refused",
		"dial tcp 127.0.0.1:54079: connect: connection refused",
	} {
		got := FormatProxyError(errors.New(sample))
		if got != "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试" {
			t.Fatalf("FormatProxyError(%q) = %q, want generic connect error", sample, got)
		}
	}
	if got := FormatProxyError(errors.New("proxyconnect tcp: 407 Proxy Authentication Required")); got != "GPU 分配代理认证失败，请检查代理用户名与密码后重试" {
		t.Fatalf("FormatProxyError(407) = %q, want auth error", got)
	}
}

func TestHTTPSProxyConnectTimeoutEOFAndPortPinning(t *testing.T) {
	// 1. Hanging HTTP CONNECT must respect short connectTimeout as ErrProxyTimeout (never 60s stdlib wait).
	hangLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen hang: %v", err)
	}
	defer hangLn.Close()
	go func() {
		for {
			c, acceptErr := hangLn.Accept()
			if acceptErr != nil {
				return
			}
			// Keep TCP open and never reply to CONNECT.
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 256)
				_, _ = conn.Read(buf)
				time.Sleep(2 * time.Second)
			}(c)
		}
	}()

	started := time.Now()
	_, err = TestAssignProxy(context.Background(), "http://"+hangLn.Addr().String(), "https://colab.research.google.com/robots.txt", 180*time.Millisecond)
	if err == nil || !errors.Is(err, ErrProxyTimeout) {
		t.Fatalf("expected ErrProxyTimeout for hanging CONNECT, got %v", err)
	}
	if time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("hanging CONNECT took too long: %v", time.Since(started))
	}

	// 2. Premature EOF during HTTP CONNECT returns typed ErrProxyConnect.
	eofLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen eof: %v", err)
	}
	defer eofLn.Close()
	go func() {
		for {
			c, acceptErr := eofLn.Accept()
			if acceptErr != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, err = TestAssignProxy(context.Background(), "http://"+eofLn.Addr().String(), "https://colab.research.google.com/robots.txt", 2*time.Second)
	if err == nil || !errors.Is(err, ErrProxyConnect) {
		t.Fatalf("expected ErrProxyConnect for CONNECT EOF, got %v", err)
	}

	// 3. Default allowedPort pinning rejects non-443 ports for https://colab.research.google.com.
	spec, err := ParseAssignProxy("http://127.0.0.1:7890")
	if err != nil {
		t.Fatalf("ParseAssignProxy: %v", err)
	}
	tr, err := NewAssignOnlyTransport(spec, "https", "colab.research.google.com")
	if err != nil {
		t.Fatalf("NewAssignOnlyTransport: %v", err)
	}
	badPortURL, _ := url.Parse("https://colab.research.google.com:8443/tun/m/assign")
	if err := tr.ValidateURL(badPortURL); !errors.Is(err, ErrForbiddenTarget) {
		t.Fatalf("expected ErrForbiddenTarget for :8443, got %v", err)
	}
	okPortURL, _ := url.Parse("https://colab.research.google.com/tun/m/assign")
	if err := tr.ValidateURL(okPortURL); err != nil {
		t.Fatalf("expected :443 default port allowed, got %v", err)
	}
}

func TestSOCKS5OptimisticTargetTLSStallAndResetFailFastSingleBudget(t *testing.T) {
	// 1. Optimistic SOCKS5 proxy replies 0x00 immediately then stalls during target TLS handshake.
	// Must return fail-fast *ProxyError (ErrProxyTimeout) within a single connectTimeout budget
	// (NOT falling through to readTimeout or doubling connectTimeout).
	stallProxy := startMockSOCKS5Server(t, mockSOCKS5Options{
		optimisticStall: true,
	})
	spec, err := ParseAssignProxy(fmt.Sprintf("socks5://127.0.0.1:%d", stallProxy.port))
	if err != nil {
		t.Fatalf("ParseAssignProxy: %v", err)
	}

	connectTimeout := 220 * time.Millisecond
	tr := buildIsolatedHTTPTransport(*spec, connectTimeout, 5*time.Second, nil)
	defer tr.CloseIdleConnections()

	req, err := http.NewRequest(http.MethodGet, "https://colab.research.google.com/tun/m/assign", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	started := time.Now()
	resp, err := tr.RoundTrip(req)
	elapsed := time.Since(started)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatalf("expected timeout error on optimistic SOCKS5 TLS stall")
	}
	var pe *ProxyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected typed *ProxyError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrProxyTimeout) {
		t.Fatalf("expected ErrProxyTimeout, got %v (kind=%v)", err, pe.Kind)
	}
	if !IsProxyFailFastError(err) {
		t.Fatalf("optimistic SOCKS5 TLS timeout must be fail-fast, got %v", err)
	}
	if elapsed > 1200*time.Millisecond {
		t.Fatalf("optimistic SOCKS5 TLS stall exceeded single connectTimeout budget: %v", elapsed)
	}

	// 2. Shared single-budget enforcement across SOCKS5 handshake + target TLS handshake:
	// SOCKS5 delays 250ms before replying 0x00, then stalls target TLS; with connectTimeout=500ms,
	// total time must stay around ~500ms (< 680ms), NOT 250ms + 500ms = 750ms.
	splitDelayProxy := startMockSOCKS5Server(t, mockSOCKS5Options{
		replyDelay:      250 * time.Millisecond,
		optimisticStall: true,
	})
	splitSpec, err := ParseAssignProxy(fmt.Sprintf("socks5://127.0.0.1:%d", splitDelayProxy.port))
	if err != nil {
		t.Fatalf("ParseAssignProxy: %v", err)
	}
	splitTr := buildIsolatedHTTPTransport(*splitSpec, 500*time.Millisecond, 5*time.Second, nil)
	defer splitTr.CloseIdleConnections()

	splitReq, _ := http.NewRequest(http.MethodGet, "https://colab.research.google.com/tun/m/assign", nil)
	splitStart := time.Now()
	splitResp, err := splitTr.RoundTrip(splitReq)
	splitElapsed := time.Since(splitStart)
	if splitResp != nil {
		_ = splitResp.Body.Close()
	}
	if err == nil || !errors.As(err, &pe) || !errors.Is(err, ErrProxyTimeout) || !IsProxyFailFastError(err) {
		t.Fatalf("expected fail-fast *ProxyError(ErrProxyTimeout) on split-delay stall, got %v", err)
	}
	if splitElapsed >= 700*time.Millisecond {
		t.Fatalf("SOCKS5 + TLS budget appears doubled: elapsed=%v (connectTimeout=500ms, replyDelay=250ms)", splitElapsed)
	}

	// 3. Context cancellation during stalled target TLS handshake unblocks promptly.
	cancelCtx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	cancelStart := time.Now()
	_, err = dialTLSOverSOCKS5(cancelCtx, *spec, "colab.research.google.com:443", 5*time.Second, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled during stalled target TLS, got %v", err)
	}
	if time.Since(cancelStart) > 1500*time.Millisecond {
		t.Fatalf("context cancel took too long: %v", time.Since(cancelStart))
	}

	// 4. Optimistic SOCKS5 proxy replying 0x00 then immediately closing (EOF) or resetting (TCP RST)
	// during target TLS handshake must return fail-fast *ProxyError (ErrProxyConnect) through
	// both raw transport and ColabOneAssignOnlyTransport.
	for _, tc := range []struct {
		name string
		opts mockSOCKS5Options
	}{
		{"eof_after_0x00", mockSOCKS5Options{optimisticClose: true}},
		{"tcp_rst_during_tls", mockSOCKS5Options{optimisticReset: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s5 := startMockSOCKS5Server(t, tc.opts)
			pSpec, err := ParseAssignProxy(fmt.Sprintf("socks5://127.0.0.1:%d", s5.port))
			if err != nil {
				t.Fatalf("ParseAssignProxy: %v", err)
			}
			assignTr, err := NewAssignOnlyTransport(pSpec, "https", "colab.research.google.com")
			if err != nil {
				t.Fatalf("NewAssignOnlyTransport: %v", err)
			}
			defer assignTr.Close()

			assignReq, _ := http.NewRequest(http.MethodGet, "https://colab.research.google.com/tun/m/assign", nil)
			resp, err := assignTr.RoundTrip(assignReq)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatalf("%s: expected error", tc.name)
			}
			var proxyErr *ProxyError
			if !errors.As(err, &proxyErr) {
				t.Fatalf("%s: expected typed *ProxyError, got %T: %v", tc.name, err, err)
			}
			if !errors.Is(err, ErrProxyConnect) {
				t.Fatalf("%s: expected ErrProxyConnect, got %v (kind=%v)", tc.name, err, proxyErr.Kind)
			}
			if !IsProxyFailFastError(err) {
				t.Fatalf("%s: expected IsProxyFailFastError=true, got false for %v", tc.name, err)
			}
		})
	}
}

func TestHTTPProxyOversizedConnectHeadersRejected(t *testing.T) {
	cases := []struct {
		name  string
		write func(conn net.Conn)
	}{
		{
			name: "oversized_headers_without_terminator",
			write: func(conn net.Conn) {
				_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n")
				line := "X-Spam: " + strings.Repeat("A", 1014) + "\r\n" // 1024 bytes per line
				for i := 0; i < 200; i++ {                              // > 200 KiB without \r\n\r\n
					if _, err := io.WriteString(conn, line); err != nil {
						return
					}
				}
			},
		},
		{
			name: "oversized_single_line_no_crlf",
			write: func(conn net.Conn) {
				chunk := strings.Repeat("B", 4096)
				for i := 0; i < 64; i++ { // 256 KiB without any CRLF
					if _, err := io.WriteString(conn, chunk); err != nil {
						return
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			var wg sync.WaitGroup
			t.Cleanup(func() {
				_ = ln.Close()
				wg.Wait()
			})

			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					c, acceptErr := ln.Accept()
					if acceptErr != nil {
						return
					}
					wg.Add(1)
					go func(conn net.Conn) {
						defer wg.Done()
						defer conn.Close()
						_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
						// Drain CONNECT request before streaming hostile response.
						br := bufio.NewReader(conn)
						for {
							line, readErr := br.ReadString('\n')
							if readErr != nil || line == "\r\n" || line == "\n" {
								break
							}
						}
						tc.write(conn)
					}(c)
				}
			}()

			proxyURL := "http://" + ln.Addr().String()
			spec, err := ParseAssignProxy(proxyURL)
			if err != nil {
				t.Fatalf("ParseAssignProxy: %v", err)
			}

			// Direct dialHTTPSViaHTTPProxy call must reject >64 KiB CONNECT headers with *ProxyError(ErrProxyConnect).
			started := time.Now()
			conn, err := dialHTTPSViaHTTPProxy(context.Background(), *spec, "colab.research.google.com:443", 3*time.Second, nil)
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil {
				t.Fatalf("expected error for oversized CONNECT response")
			}
			var pe *ProxyError
			if !errors.As(err, &pe) {
				t.Fatalf("expected typed *ProxyError, got %T: %v", err, err)
			}
			if !errors.Is(err, ErrProxyConnect) {
				t.Fatalf("expected ErrProxyConnect, got %v (kind=%v)", err, pe.Kind)
			}
			if !IsProxyFailFastError(err) {
				t.Fatalf("expected fail-fast error, got %v", err)
			}
			if time.Since(started) > 2*time.Second {
				t.Fatalf("oversized CONNECT rejection took too long: %v", time.Since(started))
			}

			// End-to-end TestAssignProxy must also classify cleanly as *ProxyError(ErrProxyConnect).
			_, err = TestAssignProxy(context.Background(), proxyURL, "https://colab.research.google.com/robots.txt", 3*time.Second)
			if err == nil || !errors.As(err, &pe) || !errors.Is(err, ErrProxyConnect) {
				t.Fatalf("TestAssignProxy oversized CONNECT error = %v, want *ProxyError(ErrProxyConnect)", err)
			}
		})
	}
}

type countingNetConn struct {
	net.Conn
	readCalls int
}

func (c *countingNetConn) Read(p []byte) (int, error) {
	c.readCalls++
	return c.Conn.Read(p)
}

func TestBufferedNetConnDrainsPrefixThenDelegatesDirectly(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() {
		_ = clientSide.Close()
		_ = serverSide.Close()
	})

	go func() {
		_, _ = serverSide.Write([]byte("UNDERLYING-PAYLOAD"))
		_ = serverSide.Close()
	}()

	underlying := &countingNetConn{Conn: clientSide}
	bc := &bufferedNetConn{
		Conn:   underlying,
		prefix: []byte("PREFIX-DATA"),
	}

	// 1. Partial drain reads strictly from prefix without touching underlying conn.
	buf := make([]byte, 6)
	n, err := bc.Read(buf)
	if err != nil || n != 6 || string(buf[:n]) != "PREFIX" {
		t.Fatalf("first read = (%d, %q, %v), want (6, \"PREFIX\", nil)", n, string(buf[:n]), err)
	}
	if underlying.readCalls != 0 {
		t.Fatalf("underlying Read called prematurely: %d", underlying.readCalls)
	}

	// 2. Zero-length read leaves remaining prefix intact and does not call underlying conn.
	n, err = bc.Read(nil)
	if err != nil || n != 0 {
		t.Fatalf("zero-length read = (%d, %v), want (0, nil)", n, err)
	}
	if underlying.readCalls != 0 {
		t.Fatalf("underlying Read called on zero-length read: %d", underlying.readCalls)
	}

	// 3. Remaining prefix drains completely and clears prefix slice to nil for GC.
	buf2 := make([]byte, 16)
	n, err = bc.Read(buf2)
	if err != nil || n != 5 || string(buf2[:n]) != "-DATA" {
		t.Fatalf("second read = (%d, %q, %v), want (5, \"-DATA\", nil)", n, string(buf2[:n]), err)
	}
	if bc.prefix != nil {
		t.Fatalf("expected prefix to be nil after draining, got len=%d", len(bc.prefix))
	}
	if underlying.readCalls != 0 {
		t.Fatalf("underlying Read called before prefix fully drained: %d", underlying.readCalls)
	}

	// 4. Subsequent reads delegate directly to underlying conn until EOF.
	rest, err := io.ReadAll(bc)
	if err != nil {
		t.Fatalf("io.ReadAll after prefix drain: %v", err)
	}
	if string(rest) != "UNDERLYING-PAYLOAD" {
		t.Fatalf("delegated payload = %q, want %q", string(rest), "UNDERLYING-PAYLOAD")
	}
	if underlying.readCalls == 0 {
		t.Fatalf("expected underlying conn Read to be called directly after prefix drain")
	}
}

func TestAssignProxySpecStringAndGoStringRedactCredentials(t *testing.T) {
	spec, err := ParseAssignProxy("socks5://alice:s3cr%40et@us.proxy.test:1080/")
	if err != nil || spec == nil {
		t.Fatalf("ParseAssignProxy: %v", err)
	}

	secrets := []string{
		"alice",
		"s3cr@et",
		"s3cr%40et",
		spec.NormalizedURL,
	}

	type wrapper struct {
		Val ColabOneAssignProxySpec
		Ptr *ColabOneAssignProxySpec
	}
	w := wrapper{Val: *spec, Ptr: spec}

	outputs := map[string]string{
		"String()":    spec.String(),
		"GoString()":  spec.GoString(),
		"%s ptr":      fmt.Sprintf("%s", spec),
		"%s val":      fmt.Sprintf("%s", *spec),
		"%v ptr":      fmt.Sprintf("%v", spec),
		"%v val":      fmt.Sprintf("%v", *spec),
		"%+v ptr":     fmt.Sprintf("%+v", spec),
		"%+v val":     fmt.Sprintf("%+v", *spec),
		"%#v ptr":     fmt.Sprintf("%#v", spec),
		"%#v val":     fmt.Sprintf("%#v", *spec),
		"wrapper %v":  fmt.Sprintf("%v", w),
		"wrapper %+v": fmt.Sprintf("%+v", w),
		"wrapper %#v": fmt.Sprintf("%#v", w),
	}

	for label, got := range outputs {
		for _, secret := range secrets {
			if strings.Contains(got, secret) {
				t.Errorf("%s leaked secret %q in output %q", label, secret, got)
			}
		}
	}

	if got := fmt.Sprintf("%v", spec); got != "socks5://us.proxy.test:1080" {
		t.Fatalf("%%v = %q, want %q", got, "socks5://us.proxy.test:1080")
	}
	if got := fmt.Sprintf("%+v", *spec); got != "socks5://us.proxy.test:1080" {
		t.Fatalf("%%+v = %q, want %q", got, "socks5://us.proxy.test:1080")
	}
	goStr := fmt.Sprintf("%#v", spec)
	for _, wantSub := range []string{
		`Scheme:"socks5"`,
		`Host:"us.proxy.test"`,
		`Port:1080`,
		`Username:"[REDACTED]"`,
		`Password:"[REDACTED]"`,
		`NormalizedURL:"[REDACTED]"`,
		`MaskedURL:"socks5://us.proxy.test:1080"`,
		`LogLabel:"SOCKS5 us.proxy.test:1080"`,
	} {
		if !strings.Contains(goStr, wantSub) {
			t.Errorf("%%#v = %q missing %q", goStr, wantSub)
		}
	}

	// Manually constructed spec without MaskedURL/LogLabel must still never leak credentials.
	manual := ColabOneAssignProxySpec{
		Scheme:   "http",
		Host:     "127.0.0.1",
		Port:     7890,
		Username: "bob",
		Password: "hunter2",
	}
	if got := manual.String(); got != "http://127.0.0.1:7890" {
		t.Fatalf("manual.String() = %q, want %q", got, "http://127.0.0.1:7890")
	}
	for _, formatted := range []string{
		fmt.Sprintf("%v", manual),
		fmt.Sprintf("%+v", manual),
		fmt.Sprintf("%#v", manual),
	} {
		if strings.Contains(formatted, "bob") || strings.Contains(formatted, "hunter2") {
			t.Fatalf("manual spec leaked credentials: %q", formatted)
		}
	}

	// NormalizedURL-only specs (valid URL and arbitrary secret string) must also redact cleanly.
	normOnly := ColabOneAssignProxySpec{NormalizedURL: "http://carol:topsecret@127.0.0.1:8080"}
	if got := normOnly.String(); got != "http://127.0.0.1:8080" {
		t.Fatalf("normOnly.String() = %q, want %q", got, "http://127.0.0.1:8080")
	}
	for _, formatted := range []string{
		fmt.Sprintf("%v", normOnly),
		fmt.Sprintf("%+v", normOnly),
		fmt.Sprintf("%#v", normOnly),
	} {
		if strings.Contains(formatted, "carol") || strings.Contains(formatted, "topsecret") {
			t.Fatalf("normOnly spec leaked credentials: %q", formatted)
		}
	}

	invalidNormOnly := ColabOneAssignProxySpec{NormalizedURL: "raw-secret-token"}
	for _, formatted := range []string{
		fmt.Sprintf("%v", invalidNormOnly),
		fmt.Sprintf("%+v", invalidNormOnly),
		fmt.Sprintf("%#v", invalidNormOnly),
	} {
		if strings.Contains(formatted, "raw-secret-token") {
			t.Fatalf("invalidNormOnly spec leaked NormalizedURL: %q", formatted)
		}
	}

	// Unauthenticated spec keeps Username/Password empty while still redacting NormalizedURL.
	noAuth, err := ParseAssignProxy("http://127.0.0.1:7890")
	if err != nil {
		t.Fatalf("ParseAssignProxy(noAuth): %v", err)
	}
	if got := fmt.Sprintf("%#v", noAuth); !strings.Contains(got, `Username:""`) || !strings.Contains(got, `Password:""`) || !strings.Contains(got, `NormalizedURL:"[REDACTED]"`) {
		t.Fatalf("unexpected unauthenticated %%#v: %q", got)
	}

	// Nil pointer formatting must not panic.
	var nilSpec *ColabOneAssignProxySpec
	if got := fmt.Sprintf("%v %#v", nilSpec, nilSpec); !strings.Contains(got, "nil") {
		t.Fatalf("unexpected nil formatting: %q", got)
	}
}

func TestFormatProxyErrorDoesNotMisclassifyConnectAsAuth(t *testing.T) {
	const (
		connectMsg = "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试"
		authMsg    = "GPU 分配代理认证失败，请检查代理用户名与密码后重试"
		timeoutMsg = "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性"
		tlsMsg     = "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理"
	)

	// Already-classified ErrProxyConnect whose message contains "认证信息" must NEVER
	// become ErrProxyAuth on double-formatting (direct or wrapped).
	connectErr := &ProxyError{
		Kind:    ErrProxyConnect,
		Message: connectMsg,
	}
	if isProxyAuthError(connectErr) {
		t.Fatalf("isProxyAuthError(ErrProxyConnect) = true, want false")
	}
	if got := FormatProxyError(connectErr); got != connectMsg {
		t.Fatalf("FormatProxyError(ErrProxyConnect) = %q, want %q", got, connectMsg)
	}
	wrappedConnect := fmt.Errorf("assign failed: %w", connectErr)
	if isProxyAuthError(wrappedConnect) {
		t.Fatalf("isProxyAuthError(wrapped ErrProxyConnect) = true, want false")
	}
	if got := FormatProxyError(wrappedConnect); got != connectMsg {
		t.Fatalf("FormatProxyError(wrapped ErrProxyConnect) = %q, want %q", got, connectMsg)
	}

	// Explicit ErrProxyTimeout / ErrProxyTLS must never be overridden by auth matching.
	timeoutErr := &ProxyError{Kind: ErrProxyTimeout, Message: "超时：认证失败 407"}
	if isProxyAuthError(timeoutErr) {
		t.Fatalf("isProxyAuthError(ErrProxyTimeout) = true, want false")
	}
	if got := FormatProxyError(timeoutErr); got != timeoutMsg {
		t.Fatalf("FormatProxyError(ErrProxyTimeout) = %q, want %q", got, timeoutMsg)
	}
	tlsErr := &ProxyError{Kind: ErrProxyTLS, Message: "TLS 证书校验失败（认证失败）"}
	if isProxyAuthError(tlsErr) {
		t.Fatalf("isProxyAuthError(ErrProxyTLS) = true, want false")
	}
	if got := FormatProxyError(tlsErr); got != tlsMsg {
		t.Fatalf("FormatProxyError(ErrProxyTLS) = %q, want %q", got, tlsMsg)
	}

	// Unclassified generic connect message containing "认证信息" must remain connect error.
	if got := FormatProxyError(errors.New(connectMsg)); got != connectMsg {
		t.Fatalf("FormatProxyError(unclassified connectMsg) = %q, want %q", got, connectMsg)
	}
	if got := FormatProxyError(errors.New("代理配置提示：请确认地址或认证信息")); got != connectMsg {
		t.Fatalf("FormatProxyError(unclassified 认证信息) = %q, want %q", got, connectMsg)
	}

	// Specific unclassified auth phrases and word-bounded 407 must still classify as auth.
	for _, sample := range []string{
		"SOCKS5 代理用户名或密码认证失败",
		"SOCKS5 代理拒绝认证方式或需要用户名与密码",
		"SOCKS5 代理要求用户名与密码认证",
		"代理需要用户名与密码",
		"proxyconnect tcp: 407 Proxy Authentication Required",
		"proxyconnect tcp: HTTP (407)",
	} {
		if !isProxyAuthError(errors.New(sample)) {
			t.Errorf("isProxyAuthError(%q) = false, want true", sample)
		}
		if got := FormatProxyError(errors.New(sample)); got != authMsg {
			t.Errorf("FormatProxyError(%q) = %q, want %q", sample, got, authMsg)
		}
	}
}

type customTypedProxyErr struct {
	kind string
	msg  string
}

func (e customTypedProxyErr) Error() string          { return e.msg }
func (e customTypedProxyErr) ProxyErrorType() string { return e.kind }

func TestProxyErrorTypeAndSingleSourceFormatting(t *testing.T) {
	const (
		connectMsg = "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试"
		timeoutMsg = "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性"
	)

	var nilPE *ProxyError
	if got := nilPE.ProxyErrorType(); got != "" {
		t.Fatalf("(*ProxyError)(nil).ProxyErrorType() = %q, want empty", got)
	}

	// Verify structural interface compatibility across packages.
	var _ interface{ ProxyErrorType() string } = (*ProxyError)(nil)

	cases := []struct {
		name string
		kind error
		want string
	}{
		{name: "auth", kind: ErrProxyAuth, want: "auth"},
		{name: "tls", kind: ErrProxyTLS, want: "ssl"},
		{name: "timeout", kind: ErrProxyTimeout, want: "connect_timeout"},
		{name: "connect", kind: ErrProxyConnect, want: "proxy"},
		{name: "forbidden", kind: ErrForbiddenTarget, want: "proxy"},
		{name: "nil_kind", kind: nil, want: "proxy"},
		{name: "unknown_kind", kind: errors.New("other"), want: "proxy"},
	}
	for _, tc := range cases {
		pe := &ProxyError{Kind: tc.kind, Message: "msg"}
		if got := pe.ProxyErrorType(); got != tc.want {
			t.Errorf("%s: ProxyErrorType() = %q, want %q", tc.name, got, tc.want)
		}
	}

	// SOCKS5 0x06 TTL expired wrapped as ErrProxyConnect must format as timeout.
	ttlErr := &ProxyError{
		Kind:    ErrProxyConnect,
		Message: "SOCKS5 代理连接失败：SOCKS5 代理连接超时 (TTL expired)",
	}
	if got := FormatProxyError(ttlErr); got != timeoutMsg {
		t.Fatalf("FormatProxyError(SOCKS5 TTL expired) = %q, want %q", got, timeoutMsg)
	}

	// Explicit ErrProxyConnect containing "407" must NOT be misclassified as auth.
	connectWith407 := &ProxyError{
		Kind:    ErrProxyConnect,
		Message: "代理拒绝连接 (HTTP 407)",
	}
	if got := FormatProxyError(connectWith407); got != connectMsg {
		t.Fatalf("FormatProxyError(ErrProxyConnect with 407) = %q, want %q", got, connectMsg)
	}

	// External ProxyErrorType()=="proxy" containing "407 认证失败" stays connect error.
	extProxyErr := customTypedProxyErr{kind: "proxy", msg: "407 认证失败"}
	if got := FormatProxyError(extProxyErr); got != connectMsg {
		t.Fatalf("FormatProxyError(custom proxy kind with 407) = %q, want %q", got, connectMsg)
	}
}
