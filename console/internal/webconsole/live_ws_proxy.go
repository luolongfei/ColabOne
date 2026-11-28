package webconsole

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/luolongfei/ColabOne/console/internal/outboundlog"
)

const (
	colabOneLiveWSProxyPath      = "/api/live/ws"
	colabOneLiveWSUpstreamPath   = "/_colabone_live/ws"
	colabOneLiveWSReadLimitBytes = 16 * 1024 * 1024
	colabOneLiveWSDialTimeout    = 15 * time.Second
	colabOneLiveWSWriteTimeout   = 15 * time.Second
	colabOneLiveWSCloseTimeout   = 2 * time.Second
	colabOneLiveWSMaxConcurrent  = 8
)

type colabOneLiveWSConn struct {
	sessionDigest [32]byte
	cancel        func(reason websocket.StatusCode)
}

var (
	liveWSSafeClientOnce sync.Once
	liveWSSafeClientInst *http.Client
	cgnatPrefix          = netip.MustParsePrefix("100.64.0.0/10")
)

func isPublicLiveWSAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	unmapped := addr.Unmap()
	if unmapped.IsLoopback() ||
		unmapped.IsPrivate() ||
		unmapped.IsLinkLocalUnicast() ||
		unmapped.IsLinkLocalMulticast() ||
		unmapped.IsMulticast() ||
		unmapped.IsUnspecified() ||
		unmapped.IsInterfaceLocalMulticast() ||
		!unmapped.IsGlobalUnicast() {
		return false
	}
	if unmapped.Is4() && cgnatPrefix.Contains(unmapped) {
		return false
	}
	return true
}

func defaultLiveWSUpstreamClient() *http.Client {
	liveWSSafeClientOnce.Do(func() {
		dialer := &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		var tr *http.Transport
		if def, ok := http.DefaultTransport.(*http.Transport); ok {
			tr = def.Clone()
		} else {
			tr = &http.Transport{}
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil || host == "" || port == "" {
				return nil, errors.New("ComfyUI 固定隧道域名无效")
			}
			if ip, ipErr := netip.ParseAddr(strings.Trim(host, "[]")); ipErr == nil {
				if !isPublicLiveWSAddr(ip) {
					return nil, errors.New("ComfyUI 固定隧道域名无效")
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			}
			ips, lookupErr := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if lookupErr != nil || len(ips) == 0 {
				return nil, errors.New("连接远端实时演播 WebSocket 失败")
			}
			var lastErr error
			for _, ip := range ips {
				if !isPublicLiveWSAddr(ip) {
					return nil, errors.New("ComfyUI 固定隧道域名无效")
				}
			}
			for _, ip := range ips {
				conn, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
				if dErr == nil {
					return conn, nil
				}
				lastErr = dErr
			}
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, errors.New("连接远端实时演播 WebSocket 失败")
		}
		liveWSSafeClientInst = &http.Client{
			Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	})
	return liveWSSafeClientInst
}

// SetLiveWSOutboundLogger configures outbound request logging for upstream Live Studio WebSocket dials.
func (w *ColabOneWebConsole) SetLiveWSOutboundLogger(logger *outboundlog.ColabOneOutboundLogger) {
	if w == nil || logger == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	base := w.liveWSHTTPClient
	if base == nil {
		base = defaultLiveWSUpstreamClient()
	}
	w.liveWSHTTPClient = outboundlog.WrapHTTPClient(base, logger, "live_ws_proxy", "")
}

func (w *ColabOneWebConsole) liveWSUpstreamClient() *http.Client {
	w.mu.Lock()
	client := w.liveWSHTTPClient
	w.mu.Unlock()
	if client != nil {
		return client
	}
	return defaultLiveWSUpstreamClient()
}

func headerContainsToken(h http.Header, key, token string) bool {
	for _, raw := range h.Values(key) {
		for _, part := range strings.Split(raw, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func validateLiveWSUpgradeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !headerContainsToken(r.Header, "Connection", "upgrade") {
		return false
	}
	if !headerContainsToken(r.Header, "Upgrade", "websocket") {
		return false
	}
	if strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")) != "13" {
		return false
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(decoded) == 16
}

func isValidWireCloseCode(code websocket.StatusCode) bool {
	if code >= 1000 && code <= 1014 {
		return code != 1004 && code != websocket.StatusNoStatusRcvd && code != websocket.StatusAbnormalClosure
	}
	return code >= 3000 && code <= 4999
}

func extractCloseReason(err error) string {
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		return ""
	}
	reason := strings.TrimSpace(ce.Reason)
	if len(reason) > 123 {
		// RFC 6455 close frame reason payload must fit inside 123 UTF-8 bytes.
		b := []byte(reason[:123])
		for len(b) > 0 && (b[len(b)-1]&0xc0) == 0x80 {
			b = b[:len(b)-1]
		}
		reason = string(b)
	}
	return reason
}

func (w *ColabOneWebConsole) cancelLiveWSConnsForSession(digest [32]byte, reason websocket.StatusCode) {
	var cancels []func(websocket.StatusCode)
	w.mu.Lock()
	for id, entry := range w.liveWSConns {
		if entry.sessionDigest == digest {
			if entry.cancel != nil {
				cancels = append(cancels, entry.cancel)
			}
			delete(w.liveWSConns, id)
		}
	}
	w.mu.Unlock()
	for _, fn := range cancels {
		fn(reason)
	}
}

func (w *ColabOneWebConsole) cancelAllLiveWSConns(reason websocket.StatusCode) {
	var cancels []func(websocket.StatusCode)
	w.mu.Lock()
	for id, entry := range w.liveWSConns {
		if entry.cancel != nil {
			cancels = append(cancels, entry.cancel)
		}
		delete(w.liveWSConns, id)
	}
	w.mu.Unlock()
	for _, fn := range cancels {
		fn(reason)
	}
}

// resolveLiveStudioCanvasOrigin extracts and strictly validates the active ComfyUI
// canvas HTTPS origin from the controller snapshot (used for server-side Live Studio WS proxy).
func (w *ColabOneWebConsole) resolveLiveStudioCanvasOrigin(ctx context.Context) (string, error) {
	if w.opts.CallBridge == nil {
		return "", errors.New("控制器桥接未初始化")
	}
	snapRaw, err := w.opts.CallBridge(ctx, "snapshot", nil)
	if err != nil {
		return "", err
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(snapRaw), &snap); err != nil || snap == nil {
		return "", errors.New("无法读取实时演播隧道状态")
	}
	rt := shareAsMap(snap["runtime"])
	comfy := shareAsMap(rt["comfyui"])
	canvas := shareAsMap(comfy["canvas"])
	rawURL := strings.TrimSpace(shareAsString(canvas["url"]))
	if rawURL == "" {
		return "", errors.New("请先开启 ComfyUI 固定隧道")
	}
	if w.liveWSAllowLocal {
		u, parseErr := url.Parse(rawURL)
		if parseErr != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Opaque != "" || strings.TrimSpace(u.Hostname()) == "" {
			return "", errors.New("ComfyUI 固定隧道域名无效")
		}
		return u.Scheme + "://" + u.Host, nil
	}
	origin, normErr := normalizePublicOrigin(rawURL)
	if normErr != nil || origin == "" {
		return "", errors.New("ComfyUI 固定隧道域名无效")
	}
	// Pin to locally configured comfy_canvas_settings.domain when present so a
	// compromised remote payload cannot redirect the host WS proxy to an arbitrary origin.
	if settings := shareAsMap(snap["comfy_canvas_settings"]); settings != nil {
		if cfgDomain := strings.TrimSpace(shareAsString(settings["domain"])); cfgDomain != "" {
			pinnedOrigin, pinErr := normalizePublicOrigin(cfgDomain)
			if pinErr != nil || pinnedOrigin == "" || !strings.EqualFold(pinnedOrigin, origin) {
				return "", errors.New("ComfyUI 固定隧道域名无效")
			}
		}
	}
	return origin, nil
}

// handleLiveStudioWSProxy proxies the browser's same-origin WebSocket at /api/live/ws
// to the remote ComfyUI fixed tunnel wss://<canvas-domain>/_colabone_live/ws with
// Origin set to https://<canvas-domain>, enabling Live Studio in Web Console across
// loopback, LAN, and public-domain modes without weakening comfy_proxy Origin checks.
func (w *ColabOneWebConsole) handleLiveStudioWSProxy(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(rw, "GET")
		return
	}
	w.mu.RLock()
	unavailable := w.closed || w.stopping
	w.mu.RUnlock()
	if unavailable {
		writeJSONError(rw, http.StatusServiceUnavailable, "网页控制台服务已关闭")
		return
	}

	sessionDigest, ok := w.authenticateRequestWithDigest(rw, r)
	if !ok {
		writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
		return
	}
	if strings.TrimSpace(r.Header.Get("Origin")) == "" || !w.validateSameOriginRequest(r, false) {
		writeJSONError(rw, http.StatusForbidden, "跨源请求已被拒绝")
		return
	}
	for _, sfs := range r.Header.Values("Sec-Fetch-Site") {
		if trimmed := strings.TrimSpace(sfs); trimmed != "" && !strings.EqualFold(trimmed, "same-origin") {
			writeJSONError(rw, http.StatusForbidden, "跨源请求已被拒绝")
			return
		}
	}
	if !validateLiveWSUpgradeRequest(r) {
		writeJSONError(rw, http.StatusBadRequest, "无效的 WebSocket 升级请求")
		return
	}

	if w.liveWSSem != nil {
		select {
		case w.liveWSSem <- struct{}{}:
			defer func() { <-w.liveWSSem }()
		default:
			rw.Header().Set("Retry-After", "2")
			writeJSONError(rw, http.StatusTooManyRequests, "当前实时演播连接数已达上限，请稍后再试")
			return
		}
	}

	canvasOrigin, err := w.resolveLiveStudioCanvasOrigin(r.Context())
	if err != nil {
		writeJSONError(rw, http.StatusBadGateway, err.Error())
		return
	}
	var upstreamURL string
	if strings.HasPrefix(canvasOrigin, "http://") {
		upstreamURL = "ws://" + strings.TrimPrefix(canvasOrigin, "http://") + colabOneLiveWSUpstreamPath
	} else {
		upstreamURL = "wss://" + strings.TrimPrefix(canvasOrigin, "https://") + colabOneLiveWSUpstreamPath
	}

	dialCtx, cancelDial := context.WithTimeout(r.Context(), colabOneLiveWSDialTimeout)
	defer cancelDial()
	upstreamConn, resp, dialErr := websocket.Dial(dialCtx, upstreamURL, &websocket.DialOptions{
		HTTPClient: w.liveWSUpstreamClient(),
		HTTPHeader: http.Header{
			"Origin": []string{canvasOrigin},
		},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if dialErr != nil {
		writeJSONError(rw, http.StatusBadGateway, "连接远端实时演播 WebSocket 失败")
		return
	}
	defer upstreamConn.CloseNow()
	upstreamConn.SetReadLimit(colabOneLiveWSReadLimitBytes)

	// Re-verify session and service liveness after upstream dial, and register the
	// connection so logout, RevokeSessions, passcode rotation, disable, or Close()
	// immediately tears down hijacked WebSocket sessions with a clean Close frame.
	proxyCtx, cancelProxy := context.WithCancel(context.Background())
	defer cancelProxy()
	revokeCh := make(chan struct{})
	var revokeOnce sync.Once
	var revokeCode atomic.Int32
	triggerRevoke := func(code websocket.StatusCode) {
		revokeCode.CompareAndSwap(0, int32(code))
		revokeOnce.Do(func() { close(revokeCh) })
	}

	w.mu.Lock()
	if w.closed || w.stopping {
		w.mu.Unlock()
		writeJSONError(rw, http.StatusServiceUnavailable, "网页控制台服务已关闭")
		return
	}
	if _, sessValid := w.sessions[sessionDigest]; !sessValid {
		w.mu.Unlock()
		writeJSONError(rw, http.StatusUnauthorized, "未登录或会话已过期")
		return
	}
	w.liveWSNextID++
	connID := w.liveWSNextID
	if w.liveWSConns == nil {
		w.liveWSConns = make(map[uint64]colabOneLiveWSConn)
	}
	w.liveWSConns[connID] = colabOneLiveWSConn{
		sessionDigest: sessionDigest,
		cancel:        triggerRevoke,
	}
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.liveWSConns, connID)
		w.mu.Unlock()
	}()

	// Origin has already been strictly verified above.
	clientConn, acceptErr := websocket.Accept(rw, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if acceptErr != nil {
		return
	}
	defer clientConn.CloseNow()
	clientConn.SetReadLimit(colabOneLiveWSReadLimitBytes)

	// If the session was revoked between registration and Accept(), close immediately.
	if forced := websocket.StatusCode(revokeCode.Load()); forced != 0 {
		if !isValidWireCloseCode(forced) {
			forced = websocket.StatusGoingAway
		}
		_ = clientConn.Close(forced, "")
		_ = upstreamConn.Close(websocket.StatusGoingAway, "")
		return
	}

	type relayResult struct {
		fromUpstream bool
		writeFailed  bool
		err          error
	}
	doneCh := make(chan relayResult, 2)

	relay := func(src, dst *websocket.Conn, fromUpstream bool) {
		for {
			msgType, data, rErr := src.Read(proxyCtx)
			if rErr != nil {
				doneCh <- relayResult{fromUpstream: fromUpstream, writeFailed: false, err: rErr}
				return
			}
			wCtx, cancelWrite := context.WithTimeout(proxyCtx, colabOneLiveWSWriteTimeout)
			wErr := dst.Write(wCtx, msgType, data)
			cancelWrite()
			if wErr != nil {
				doneCh <- relayResult{fromUpstream: fromUpstream, writeFailed: true, err: wErr}
				return
			}
		}
	}

	go relay(clientConn, upstreamConn, false)
	go relay(upstreamConn, clientConn, true)

	var first relayResult
	select {
	case first = <-doneCh:
	case <-revokeCh:
		forced := websocket.StatusCode(revokeCode.Load())
		if !isValidWireCloseCode(forced) {
			forced = websocket.StatusGoingAway
		}
		// Send clean RFC 6455 Close frames to both peers BEFORE canceling proxyCtx
		// (canceling a Read context in coder/websocket hard-closes TCP and yields 1006).
		closeTimer := time.AfterFunc(colabOneLiveWSCloseTimeout, func() {
			cancelProxy()
			_ = clientConn.CloseNow()
			_ = upstreamConn.CloseNow()
		})
		var closeWG sync.WaitGroup
		closeWG.Add(2)
		go func() {
			defer closeWG.Done()
			_ = clientConn.Close(forced, "")
		}()
		go func() {
			defer closeWG.Done()
			_ = upstreamConn.Close(websocket.StatusGoingAway, "")
		}()
		closeWG.Wait()
		closeTimer.Stop()
		cancelProxy()
		_ = clientConn.CloseNow()
		_ = upstreamConn.CloseNow()
		<-doneCh
		<-doneCh
		return
	}

	if forced := websocket.StatusCode(revokeCode.Load()); forced != 0 {
		if !isValidWireCloseCode(forced) {
			forced = websocket.StatusGoingAway
		}
		closeTimer := time.AfterFunc(colabOneLiveWSCloseTimeout, func() {
			cancelProxy()
			_ = clientConn.CloseNow()
			_ = upstreamConn.CloseNow()
		})
		_ = clientConn.Close(forced, "")
		_ = upstreamConn.Close(websocket.StatusGoingAway, "")
		closeTimer.Stop()
		cancelProxy()
		_ = clientConn.CloseNow()
		_ = upstreamConn.CloseNow()
		<-doneCh
		return
	}

	// Identify which side failed and which side is the peer to receive a clean Close frame.
	// If reading from upstream failed (fromUpstream && !writeFailed), upstream failed -> close client.
	// If writing to upstream failed (!fromUpstream && writeFailed), upstream failed -> close client.
	// Otherwise client failed -> close upstream.
	peerIsClient := (first.fromUpstream && !first.writeFailed) || (!first.fromUpstream && first.writeFailed)
	var failedConn, peerConn *websocket.Conn
	if peerIsClient {
		failedConn = upstreamConn
		peerConn = clientConn
	} else {
		failedConn = clientConn
		peerConn = upstreamConn
	}

	closeCode := websocket.CloseStatus(first.err)
	closeReason := ""
	if !first.writeFailed && isValidWireCloseCode(closeCode) {
		closeReason = extractCloseReason(first.err)
	} else if peerIsClient {
		// Upstream dropped unexpectedly or sent a non-wire pseudo-code (-1/1005/1006):
		// forward 1014 (Bad Gateway) so the browser retries instead of treating it as terminal 1000.
		closeCode = websocket.StatusBadGateway
	} else {
		closeCode = websocket.StatusGoingAway
	}

	// Close the failed side immediately, send the RFC 6455 Close frame to the surviving peer
	// BEFORE calling cancelProxy() (which would hard-close the socket and turn the close into 1006),
	// guarded by a bounded fallback timer so peerConn.Close can never hang.
	_ = failedConn.CloseNow()
	closeTimer := time.AfterFunc(colabOneLiveWSCloseTimeout, func() {
		cancelProxy()
		_ = peerConn.CloseNow()
	})
	_ = peerConn.Close(closeCode, closeReason)
	closeTimer.Stop()

	cancelProxy()
	_ = peerConn.CloseNow()
	<-doneCh
}
