package jupyterws

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func wsWriteJSON(t *testing.T, ctx context.Context, conn *websocket.Conn, msg map[string]any) {
	t.Helper()
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal server ws msg: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Errorf("server ws write failed: %v", err)
	}
}

func wsReadMsg(ctx context.Context, conn *websocket.Conn) (*ColabOneWSMessage, error) {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	return deserializeWSMessage(typ, data)
}

func makeJupyterMsg(channel, msgType, parentMsgID string, content map[string]any) map[string]any {
	return map[string]any{
		"channel":  channel,
		"msg_id":   colabOneNewUUID(),
		"msg_type": msgType,
		"header": map[string]any{
			"msg_id":   colabOneNewUUID(),
			"msg_type": msgType,
		},
		"parent_header": map[string]any{
			"msg_id": parentMsgID,
		},
		"metadata": map[string]any{},
		"content":  content,
	}
}

func encodeJupyterBinaryMsg(msg map[string]any, bufs ...[]byte) ([]byte, error) {
	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	allBufs := make([][]byte, 0, 1+len(bufs))
	allBufs = append(allBufs, jsonBytes)
	allBufs = append(allBufs, bufs...)

	nbufs := len(allBufs)
	offsets := make([]int, nbufs)
	offsets[0] = 4 * (nbufs + 1)
	for i := 1; i < nbufs; i++ {
		offsets[i] = offsets[i-1] + len(allBufs[i-1])
	}
	total := offsets[nbufs-1] + len(allBufs[nbufs-1])
	out := make([]byte, total)
	binary.BigEndian.PutUint32(out[0:4], uint32(nbufs))
	for i, off := range offsets {
		binary.BigEndian.PutUint32(out[4*(i+1):4*(i+2)], uint32(off))
	}
	for i, b := range allBufs {
		copy(out[offsets[i]:], b)
	}
	return out, nil
}

func TestStartCreatesKernelAndConnectsWithColabHeaders(t *testing.T) {
	const proxyToken = "proxy-secret-token-123"
	const kernelID = "kernel-abc-001"

	var postCount atomic.Int32
	var deleteCount atomic.Int32
	var wsCount atomic.Int32
	var gotWSPath atomic.Value
	var gotWSSession atomic.Value

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
			postCount.Add(1)
			if r.Header.Get("X-Colab-Client-Agent") != COLABONE_CLIENT_AGENT {
				http.Error(w, "missing agent", http.StatusBadRequest)
				return
			}
			if r.Header.Get("X-Colab-Runtime-Proxy-Token") != proxyToken {
				http.Error(w, "missing proxy header", http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("colab-runtime-proxy-token") != proxyToken {
				http.Error(w, "missing proxy query", http.StatusUnauthorized)
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			if body["name"] != "python3" {
				http.Error(w, "unexpected kernel name", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": kernelID, "name": "python3"})

		case r.Method == http.MethodGet && r.URL.Path == "/api/kernels/"+kernelID+"/channels":
			if r.Header.Get("X-Colab-Client-Agent") != COLABONE_CLIENT_AGENT ||
				r.Header.Get("X-Colab-Runtime-Proxy-Token") != proxyToken ||
				r.URL.Query().Get("colab-runtime-proxy-token") != proxyToken ||
				r.URL.Query().Get("session_id") == "" {
				http.Error(w, "bad ws auth", http.StatusUnauthorized)
				return
			}
			wsCount.Add(1)
			gotWSPath.Store(r.URL.Path)
			gotWSSession.Store(r.URL.Query().Get("session_id"))

			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}

		case r.Method == http.MethodDelete && r.URL.Path == "/api/kernels/"+kernelID:
			if r.Header.Get("X-Colab-Client-Agent") != COLABONE_CLIENT_AGENT ||
				r.Header.Get("X-Colab-Runtime-Proxy-Token") != proxyToken ||
				r.URL.Query().Get("colab-runtime-proxy-token") != proxyToken {
				http.Error(w, "bad delete auth", http.StatusUnauthorized)
				return
			}
			deleteCount.Add(1)
			w.WriteHeader(http.StatusNoContent)

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var startedKernels []string
	var startedSessions []string
	var cbMu sync.Mutex

	client := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL + "/",
		ProxyToken: proxyToken,
		OnKernelStarted: func(id string) {
			cbMu.Lock()
			startedKernels = append(startedKernels, id)
			cbMu.Unlock()
		},
		OnSessionStarted: func(sid string) {
			cbMu.Lock()
			startedSessions = append(startedSessions, sid)
			cbMu.Unlock()
		},
	})

	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	// Second Start must be a clean no-op.
	if err := client.Start(ctx); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}

	if !client.IsConnected() {
		t.Fatalf("expected IsConnected() == true")
	}
	if client.KernelID() != kernelID {
		t.Fatalf("KernelID() = %q, want %q", client.KernelID(), kernelID)
	}
	if len(client.SessionID()) != 36 {
		t.Fatalf("SessionID() = %q, expected UUID v4", client.SessionID())
	}
	if gotWSSession.Load() != client.SessionID() {
		t.Fatalf("WS session_id = %v, want %q", gotWSSession.Load(), client.SessionID())
	}
	if postCount.Load() != 1 || wsCount.Load() != 1 {
		t.Fatalf("postCount=%d wsCount=%d, want 1 and 1", postCount.Load(), wsCount.Load())
	}

	cbMu.Lock()
	if len(startedKernels) != 1 || startedKernels[0] != kernelID {
		t.Fatalf("startedKernels = %v, want [%s]", startedKernels, kernelID)
	}
	if len(startedSessions) != 1 || startedSessions[0] != client.SessionID() {
		t.Fatalf("startedSessions = %v, want [%s]", startedSessions, client.SessionID())
	}
	cbMu.Unlock()

	if err := client.Stop(ctx, true); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if client.IsConnected() {
		t.Fatalf("expected IsConnected() == false after Stop")
	}
	if deleteCount.Load() != 1 {
		t.Fatalf("deleteCount = %d, want 1", deleteCount.Load())
	}
	if client.KernelID() != "" {
		t.Fatalf("expected KernelID cleared after shutdownKernel=true, got %q", client.KernelID())
	}
}

func TestStartWithExistingKernelIDAndStale404Recovery(t *testing.T) {
	t.Run("reuses existing kernel and session without POST", func(t *testing.T) {
		var postCount atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/kernels" {
				postCount.Add(1)
				http.Error(w, "should not create kernel", http.StatusInternalServerError)
				return
			}
			if r.URL.Path == "/api/kernels/existing-k1/channels" {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for {
					if _, _, err := conn.Read(r.Context()); err != nil {
						return
					}
				}
			}
			http.NotFound(w, r)
		}))
		defer srv.Close()

		var kernelCB, sessionCB atomic.Int32
		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:        srv.URL,
			ProxyToken:       "tok",
			KernelID:         "existing-k1",
			SessionID:        "existing-s1",
			OnKernelStarted:  func(string) { kernelCB.Add(1) },
			OnSessionStarted: func(string) { sessionCB.Add(1) },
		})
		if err := client.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer client.Stop(context.Background(), false)

		if postCount.Load() != 0 {
			t.Fatalf("expected 0 POST /api/kernels calls, got %d", postCount.Load())
		}
		if kernelCB.Load() != 0 || sessionCB.Load() != 0 {
			t.Fatalf("callbacks should not fire for pre-supplied IDs: kernel=%d session=%d", kernelCB.Load(), sessionCB.Load())
		}
		if client.KernelID() != "existing-k1" || client.SessionID() != "existing-s1" {
			t.Fatalf("unexpected IDs: kernel=%q session=%q", client.KernelID(), client.SessionID())
		}
	})

	t.Run("stale pre-supplied kernel 404 creates fresh kernel", func(t *testing.T) {
		var postCount atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/kernels/stale-k/channels":
				http.Error(w, "kernel not found", http.StatusNotFound)
			case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
				postCount.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "fresh-k2"})
			case r.URL.Path == "/api/kernels/fresh-k2/channels":
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for {
					if _, _, err := conn.Read(r.Context()); err != nil {
						return
					}
				}
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		var notified string
		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:       srv.URL,
			ProxyToken:      "tok",
			KernelID:        "stale-k",
			RetryBaseDelay:  10 * time.Millisecond,
			OnKernelStarted: func(id string) { notified = id },
		})
		if err := client.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer client.Stop(context.Background(), false)

		if postCount.Load() != 1 || client.KernelID() != "fresh-k2" || notified != "fresh-k2" {
			t.Fatalf("postCount=%d kernelID=%q notified=%q", postCount.Load(), client.KernelID(), notified)
		}
	})
}

func TestStartRetriesTransient5xxAndFailsFastOn401(t *testing.T) {
	t.Run("retries 502/503/504 and reuses kernel", func(t *testing.T) {
		var postAttempts atomic.Int32
		var wsAttempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
				n := postAttempts.Add(1)
				if n == 1 {
					http.Error(w, "bad gateway", http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-retry"})
			case r.URL.Path == "/api/kernels/k-retry/channels":
				n := wsAttempts.Add(1)
				if n == 1 {
					http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for {
					if _, _, err := conn.Read(r.Context()); err != nil {
						return
					}
				}
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:      srv.URL,
			ProxyToken:     "tok",
			RetryBaseDelay: 10 * time.Millisecond,
		})
		if err := client.Start(context.Background()); err != nil {
			t.Fatalf("Start failed after transient errors: %v", err)
		}
		defer client.Stop(context.Background(), false)

		if postAttempts.Load() != 2 {
			t.Fatalf("expected 2 POST attempts (1 fail + 1 create reused on WS retry), got %d", postAttempts.Load())
		}
		if wsAttempts.Load() != 2 {
			t.Fatalf("expected 2 WS attempts, got %d", wsAttempts.Load())
		}
	})

	t.Run("fails fast on 401 and redacts token in error", func(t *testing.T) {
		const secretToken = "super-secret-proxy-token"
		var wsAttempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wsAttempts.Add(1)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		defer srv.Close()

		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:      srv.URL,
			ProxyToken:     secretToken,
			KernelID:       "k-auth",
			RetryBaseDelay: 10 * time.Millisecond,
		})
		err := client.Start(context.Background())
		if err == nil {
			t.Fatalf("expected 401 error, got nil")
		}
		var httpErr *ColabOneHTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected ColabOneHTTPError 401, got %T: %v", err, err)
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Fatalf("error message leaked proxy token: %s", err.Error())
		}
		if wsAttempts.Load() != 1 {
			t.Fatalf("expected no retries on 401, got %d attempts", wsAttempts.Load())
		}
	})
}

func TestStartWSTimeoutShutdownVsReuse(t *testing.T) {
	t.Run("ShutdownOnWSTimeout deletes leaked kernels", func(t *testing.T) {
		var createdCount atomic.Int32
		var deletedIDs []string
		var mu sync.Mutex
		releaseHandlers := make(chan struct{})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
				n := createdCount.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("leaked-%d", n)})
			case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/kernels/"):
				id := strings.TrimPrefix(r.URL.Path, "/api/kernels/")
				mu.Lock()
				deletedIDs = append(deletedIDs, id)
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			case strings.HasSuffix(r.URL.Path, "/channels"):
				select {
				case <-r.Context().Done():
				case <-releaseHandlers:
				}
			default:
				http.NotFound(w, r)
			}
		}))
		defer func() {
			close(releaseHandlers)
			srv.Close()
		}()

		var kernelNotified atomic.Int32
		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:           srv.URL,
			ProxyToken:          "tok",
			ConnectTimeout:      80 * time.Millisecond,
			RetryBaseDelay:      10 * time.Millisecond,
			MaxConnectRetries:   2,
			ShutdownOnWSTimeout: true,
			OnKernelStarted:     func(string) { kernelNotified.Add(1) },
		})

		err := client.Start(context.Background())
		if !errors.Is(err, ErrWSConnectTimeout) {
			t.Fatalf("expected ErrWSConnectTimeout, got %v", err)
		}
		if err.Error() != COLABONE_WS_CONNECT_TIMEOUT_MSG {
			t.Fatalf("unexpected error string: %q", err.Error())
		}
		if createdCount.Load() != 2 {
			t.Fatalf("expected 2 created kernels, got %d", createdCount.Load())
		}
		mu.Lock()
		gotDeleted := append([]string(nil), deletedIDs...)
		mu.Unlock()
		if len(gotDeleted) != 2 || gotDeleted[0] != "leaked-1" || gotDeleted[1] != "leaked-2" {
			t.Fatalf("expected both leaked kernels deleted, got %v", gotDeleted)
		}
		if client.KernelID() != "" || kernelNotified.Load() != 0 {
			t.Fatalf("expected empty kernelID and 0 notifications, got %q / %d", client.KernelID(), kernelNotified.Load())
		}
	})

	t.Run("without ShutdownOnWSTimeout reuses KernelID on retry", func(t *testing.T) {
		var createdCount atomic.Int32
		var deleteCount atomic.Int32
		var wsAttempts atomic.Int32
		releaseHandlers := make(chan struct{})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
				createdCount.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "reused-k1"})
			case r.Method == http.MethodDelete:
				deleteCount.Add(1)
				w.WriteHeader(http.StatusNoContent)
			case r.URL.Path == "/api/kernels/reused-k1/channels":
				if wsAttempts.Add(1) == 1 {
					select {
					case <-r.Context().Done():
					case <-releaseHandlers:
					}
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for {
					if _, _, err := conn.Read(r.Context()); err != nil {
						return
					}
				}
			default:
				http.NotFound(w, r)
			}
		}))
		defer func() {
			close(releaseHandlers)
			srv.Close()
		}()

		var notified []string
		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:           srv.URL,
			ProxyToken:          "tok",
			ConnectTimeout:      80 * time.Millisecond,
			RetryBaseDelay:      10 * time.Millisecond,
			ShutdownOnWSTimeout: false,
			OnKernelStarted:     func(id string) { notified = append(notified, id) },
		})

		if err := client.Start(context.Background()); err != nil {
			t.Fatalf("Start should succeed on 2nd attempt: %v", err)
		}
		defer client.Stop(context.Background(), false)

		if createdCount.Load() != 1 {
			t.Fatalf("expected exactly 1 kernel created, got %d", createdCount.Load())
		}
		if deleteCount.Load() != 0 {
			t.Fatalf("expected 0 kernel deletes, got %d", deleteCount.Load())
		}
		if len(notified) != 1 || notified[0] != "reused-k1" {
			t.Fatalf("notified = %v, want [reused-k1]", notified)
		}
	})
}

func TestExecuteCodeStreamingOutputsAndBothCompletionOrderings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/kernels" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-stream"})
			return
		}
		if r.URL.Path == "/api/kernels/k-stream/channels" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()

			for {
				req, err := wsReadMsg(r.Context(), conn)
				if err != nil {
					return
				}
				if req.Channel != "shell" || req.MsgType != "execute_request" {
					t.Errorf("unexpected client message: channel=%q type=%q", req.Channel, req.MsgType)
					return
				}
				// Verify exact execute_request content defaults.
				if req.Content["silent"] != false || req.Content["store_history"] != false || req.Content["stop_on_error"] != true {
					t.Errorf("unexpected execute_request content: %#v", req.Content)
				}
				code := stringFromMap(req.Content, "code")

				// Emit unrelated message for a different parent_header to ensure isolation.
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", "other-msg-id", map[string]any{
					"name": "stdout",
					"text": "NOISE\n",
				}))

				switch code {
				case "idle_then_reply":
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
						"execution_state": "busy",
					}))
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
						"name": "stdout",
						"text": "line-1\n",
					}))
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
						"name": "stderr",
						"text": "warn-1\n",
					}))
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "display_data", req.MsgID, map[string]any{
						"data": map[string]any{"text/plain": "disp"},
					}))
					// Also exercise binary default protocol deserialization.
					binMsg, err := encodeJupyterBinaryMsg(makeJupyterMsg("iopub", "execute_result", req.MsgID, map[string]any{
						"data": map[string]any{"text/plain": "42"},
					}), []byte("binbuf"))
					if err != nil {
						t.Errorf("encode binary msg: %v", err)
						return
					}
					_ = conn.Write(r.Context(), websocket.MessageBinary, binMsg)

					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
						"execution_state": "idle",
					}))
					time.Sleep(15 * time.Millisecond)
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("shell", "execute_reply", req.MsgID, map[string]any{
						"status":          "ok",
						"execution_count": 1,
					}))

				case "reply_then_idle":
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
						"execution_state": "busy",
					}))
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
						"name": "stdout",
						"text": "part-1\n",
					}))
					// execute_reply arrives BEFORE final stream chunk and status: idle!
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("shell", "execute_reply", req.MsgID, map[string]any{
						"status":          "ok",
						"execution_count": 2,
					}))
					time.Sleep(20 * time.Millisecond)
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
						"name": "stdout",
						"text": "part-2\n",
					}))
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
						"execution_state": "idle",
					}))
				}
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "tok",
	})
	defer client.Stop(context.Background(), false)

	// Ordering 1: status: idle -> execute_reply (also tests lazy Start via ExecuteCode).
	var streamed1 []ColabOneKernelOutput
	outs1, err := client.ExecuteCode(context.Background(), "idle_then_reply", ColabOneExecuteOptions{
		Timeout: 5 * time.Second,
		OutputHook: func(out ColabOneKernelOutput) {
			streamed1 = append(streamed1, out)
		},
	})
	if err != nil {
		t.Fatalf("ExecuteCode (idle_then_reply) failed: %v", err)
	}
	if len(outs1) != 4 || len(streamed1) != 4 {
		t.Fatalf("got %d outputs / %d streamed, want 4", len(outs1), len(streamed1))
	}
	if outs1[0].OutputType != "stream" || outs1[0].Name != "stdout" || outs1[0].Text != "line-1\n" {
		t.Fatalf("unexpected output[0]: %#v", outs1[0])
	}
	if outs1[1].OutputType != "stream" || outs1[1].Name != "stderr" || outs1[1].Text != "warn-1\n" {
		t.Fatalf("unexpected output[1]: %#v", outs1[1])
	}
	if outs1[2].OutputType != "display_data" || outs1[2].Data["text/plain"] != "disp" {
		t.Fatalf("unexpected output[2]: %#v", outs1[2])
	}
	if outs1[3].OutputType != "execute_result" || outs1[3].Data["text/plain"] != "42" {
		t.Fatalf("unexpected output[3]: %#v", outs1[3])
	}

	// Ordering 2: execute_reply -> trailing stream -> status: idle.
	var streamed2 []string
	outs2, err := client.ExecuteCode(context.Background(), "reply_then_idle", ColabOneExecuteOptions{
		Timeout: 5 * time.Second,
		OutputHook: func(out ColabOneKernelOutput) {
			streamed2 = append(streamed2, out.Text)
		},
	})
	if err != nil {
		t.Fatalf("ExecuteCode (reply_then_idle) failed: %v", err)
	}
	if len(outs2) != 2 || outs2[0].Text != "part-1\n" || outs2[1].Text != "part-2\n" {
		t.Fatalf("unexpected outs2: %#v", outs2)
	}
	if len(streamed2) != 2 || streamed2[1] != "part-2\n" {
		t.Fatalf("unexpected streamed2: %#v", streamed2)
	}
}

func TestExecuteCodeColabRequestHookAndInputReply(t *testing.T) {
	var receivedInputReply atomic.Value

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/kernels" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-drivefs"})
			return
		}
		if r.URL.Path == "/api/kernels/k-drivefs/channels" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()

			req, err := wsReadMsg(r.Context(), conn)
			if err != nil {
				return
			}
			if req.Content["allow_stdin"] != true {
				t.Errorf("expected allow_stdin=true, got %#v", req.Content["allow_stdin"])
			}

			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
				"execution_state": "busy",
			}))

			// Send Colab DriveFS dfs_ephemeral colab_request.
			colabReqHeader := map[string]any{
				"msg_id":   "colab-hdr-99",
				"msg_type": "colab_request",
			}
			wsWriteJSON(t, r.Context(), conn, map[string]any{
				"channel":       "stdin",
				"header":        colabReqHeader,
				"parent_header": map[string]any{"msg_id": req.MsgID},
				"metadata":      map[string]any{"colab_msg_id": "cm-drivefs-42"},
				"content": map[string]any{
					"request": map[string]any{"authType": "dfs_ephemeral"},
				},
			})

			// Wait for client's input_reply on stdin channel.
			reply, err := wsReadMsg(r.Context(), conn)
			if err != nil {
				t.Errorf("failed reading input_reply: %v", err)
				return
			}
			receivedInputReply.Store(reply)

			// Also test a standard stdin input_request fallback.
			wsWriteJSON(t, r.Context(), conn, map[string]any{
				"channel": "stdin",
				"header": map[string]any{
					"msg_id":   "stdin-req-1",
					"msg_type": "input_request",
				},
				"parent_header": map[string]any{"msg_id": req.MsgID},
				"metadata":      map[string]any{},
				"content":       map[string]any{"prompt": "Confirm? ", "password": false},
			})
			stdinReply, err := wsReadMsg(r.Context(), conn)
			if err != nil {
				t.Errorf("failed reading stdin input_reply: %v", err)
				return
			}
			if stdinReply.Channel != "stdin" || stdinReply.MsgType != "input_reply" || stdinReply.Content["value"] != "yes" {
				t.Errorf("unexpected stdin reply: %#v", stdinReply)
			}

			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
				"name": "stdout",
				"text": "COLABONE_DRIVEFS_OK=1\n",
			}))
			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
				"execution_state": "idle",
			}))
			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("shell", "execute_reply", req.MsgID, map[string]any{
				"status": "ok",
			}))
			// Keep open until client closes.
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	var hookDone sync.WaitGroup
	var hookErr atomic.Value

	client := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "tok",
		RequestHook: func(msg *ColabOneWSMessage, sendInputReply func(parentHeader map[string]any, value any) error) bool {
			reqMap, _ := msg.Content["request"].(map[string]any)
			if stringFromMap(reqMap, "authType") != "dfs_ephemeral" {
				return false
			}
			colabMsgID := stringFromMap(msg.Metadata, "colab_msg_id")
			// Simulate async propagation goroutine (exact DriveFS pattern).
			hookDone.Add(1)
			go func() {
				defer hookDone.Done()
				time.Sleep(25 * time.Millisecond)
				if err := sendInputReply(msg.Header, map[string]any{
					"type":         "colab_reply",
					"colab_msg_id": colabMsgID,
				}); err != nil {
					hookErr.Store(err)
				}
			}()
			return true
		},
	})
	defer client.Stop(context.Background(), false)

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	outs, err := client.ExecuteCode(context.Background(), "drive.mount('/content/drive')", ColabOneExecuteOptions{
		AllowStdin: true,
		Timeout:    5 * time.Second,
		StdinHook: func(prompt string, password bool) (string, error) {
			if prompt != "Confirm? " || password {
				return "", fmt.Errorf("unexpected prompt=%q password=%v", prompt, password)
			}
			return "yes", nil
		},
	})
	hookDone.Wait()
	if err != nil {
		t.Fatalf("ExecuteCode failed: %v", err)
	}
	if herr := hookErr.Load(); herr != nil {
		t.Fatalf("sendInputReply failed: %v", herr)
	}
	if len(outs) != 1 || outs[0].Text != "COLABONE_DRIVEFS_OK=1\n" {
		t.Fatalf("unexpected outputs: %#v", outs)
	}

	reply, ok := receivedInputReply.Load().(*ColabOneWSMessage)
	if !ok || reply == nil {
		t.Fatalf("server did not receive input_reply")
	}
	if reply.Channel != "stdin" || reply.MsgType != "input_reply" {
		t.Fatalf("unexpected reply channel=%q type=%q", reply.Channel, reply.MsgType)
	}
	if stringFromMap(reply.ParentHeader, "msg_id") != "colab-hdr-99" {
		t.Fatalf("unexpected reply parent_header: %#v", reply.ParentHeader)
	}
	val, _ := reply.Content["value"].(map[string]any)
	if stringFromMap(val, "type") != "colab_reply" || stringFromMap(val, "colab_msg_id") != "cm-drivefs-42" {
		t.Fatalf("unexpected reply value: %#v", reply.Content)
	}
}

func TestExecuteCodeHardTimeoutAndContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/kernels" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-hang"})
			return
		}
		if r.URL.Path == "/api/kernels/k-hang/channels" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()

			for {
				req, err := wsReadMsg(r.Context(), conn)
				if err != nil {
					return
				}
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
					"execution_state": "busy",
				}))
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
					"name": "stdout",
					"text": "partial\n",
				}))
				// Intentionally never send status:idle or execute_reply.
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "tok",
	})
	defer client.Stop(context.Background(), false)

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Hard timeout via opts.Timeout.
	started := time.Now()
	outs, err := client.ExecuteCode(context.Background(), "while True: pass", ColabOneExecuteOptions{
		Timeout: 120 * time.Millisecond,
	})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if err.Error() != COLABONE_KERNEL_EXEC_TIMEOUT_MSG {
		t.Fatalf("err.Error() = %q, want exact %q", err.Error(), COLABONE_KERNEL_EXEC_TIMEOUT_MSG)
	}
	if !errors.Is(err, ErrKernelTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected errors.Is(ErrKernelTimeout) and context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("timeout took too long (%v); possible stall/busy-loop", elapsed)
	}
	if len(outs) != 1 || outs[0].Text != "partial\n" {
		t.Fatalf("expected partial streamed output preserved, got %#v", outs)
	}

	// Explicit parent context cancel returns context.Canceled immediately.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()
	started = time.Now()
	_, err = client.ExecuteCode(ctx, "hang", ColabOneExecuteOptions{Timeout: 5 * time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if time.Since(started) > 1*time.Second {
		t.Fatalf("cancel took too long: %v", time.Since(started))
	}
}

func TestAbortUnblocksActiveExecuteCode(t *testing.T) {
	var deleteCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-abort"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/kernels/k-abort":
			deleteCount.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/kernels/k-abort/channels":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			req, err := wsReadMsg(r.Context(), conn)
			if err != nil {
				return
			}
			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
				"execution_state": "busy",
			}))
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "tok",
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	go func() {
		time.Sleep(60 * time.Millisecond)
		client.Abort()
	}()

	started := time.Now()
	_, err := client.ExecuteCode(context.Background(), "sleep(999)", ColabOneExecuteOptions{
		Timeout: 10 * time.Second,
	})
	if !errors.Is(err, ErrKernelAborted) {
		t.Fatalf("expected ErrKernelAborted, got %v", err)
	}
	if time.Since(started) > 1*time.Second {
		t.Fatalf("Abort did not unblock ExecuteCode promptly: %v", time.Since(started))
	}
	if client.IsConnected() {
		t.Fatalf("expected IsConnected() == false after Abort")
	}

	// ExecuteCode after Abort (without re-Start) returns ErrKernelAborted cleanly.
	if _, err := client.ExecuteCode(context.Background(), "pass", ColabOneExecuteOptions{}); !errors.Is(err, ErrKernelAborted) {
		t.Fatalf("expected ErrKernelAborted on aborted client, got %v", err)
	}

	if err := client.Stop(context.Background(), true); err != nil {
		t.Fatalf("Stop after Abort failed: %v", err)
	}
	if deleteCount.Load() != 1 {
		t.Fatalf("expected kernel DELETE after Stop(true), got %d", deleteCount.Load())
	}
}

func TestLargePayloadOver2MB(t *testing.T) {
	// Generate a 2.5 MiB stdout payload and a 1.5 MiB execute_result payload
	// (far exceeding coder/websocket's default 32 KiB limit).
	largeStream := strings.Repeat("COLABONE_CHUNK_0123456789ABCDEF\n", 80*1024) // ~2.56 MiB
	largeResult := strings.Repeat("R", 1536*1024)                               // 1.5 MiB
	largeCode := "# " + strings.Repeat("x", 128*1024)                           // 128 KiB request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/kernels" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-large"})
			return
		}
		if r.URL.Path == "/api/kernels/k-large/channels" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			conn.SetReadLimit(16 * 1024 * 1024)

			req, err := wsReadMsg(r.Context(), conn)
			if err != nil {
				t.Errorf("server failed reading large execute_request: %v", err)
				return
			}
			if stringFromMap(req.Content, "code") != largeCode {
				t.Errorf("code length mismatch: got %d want %d", len(stringFromMap(req.Content, "code")), len(largeCode))
			}

			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
				"execution_state": "busy",
			}))
			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
				"name": "stdout",
				"text": largeStream,
			}))
			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "execute_result", req.MsgID, map[string]any{
				"data": map[string]any{"text/plain": largeResult},
			}))
			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
				"execution_state": "idle",
			}))
			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("shell", "execute_reply", req.MsgID, map[string]any{
				"status": "ok",
			}))
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "tok",
	})
	defer client.Stop(context.Background(), false)

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	outs, err := client.ExecuteCode(context.Background(), largeCode, ColabOneExecuteOptions{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("ExecuteCode large payload failed: %v", err)
	}
	if len(outs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(outs))
	}
	if outs[0].Text != largeStream {
		t.Fatalf("large stream mismatch: got len=%d want len=%d", len(outs[0].Text), len(largeStream))
	}
	if outs[1].Data["text/plain"] != largeResult {
		t.Fatalf("large result mismatch: got len=%d want len=%d", len(fmt.Sprint(outs[1].Data["text/plain"])), len(largeResult))
	}
}

func TestExecuteCodeErrorRepliesAndSynthesis(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/kernels" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-err"})
			return
		}
		if r.URL.Path == "/api/kernels/k-err/channels" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()

			for {
				req, err := wsReadMsg(r.Context(), conn)
				if err != nil {
					return
				}
				code := stringFromMap(req.Content, "code")
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
					"execution_state": "busy",
				}))

				switch code {
				case "with_iopub_error":
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "error", req.MsgID, map[string]any{
						"ename":     "ValueError",
						"evalue":    "bad value",
						"traceback": []string{"tb1", "tb2"},
					}))
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
						"execution_state": "idle",
					}))
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("shell", "execute_reply", req.MsgID, map[string]any{
						"status":    "error",
						"ename":     "ValueError",
						"evalue":    "bad value",
						"traceback": []string{"tb1", "tb2"},
					}))

				case "reply_only_error":
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
						"execution_state": "idle",
					}))
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("shell", "execute_reply", req.MsgID, map[string]any{
						"status":    "error",
						"ename":     "RuntimeError",
						"evalue":    "boom",
						"traceback": []string{"line 1"},
					}))

				case "reply_only_defaults":
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
						"execution_state": "idle",
					}))
					wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("shell", "execute_reply", req.MsgID, map[string]any{
						"status": "error",
					}))
				}
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "tok",
	})
	defer client.Stop(context.Background(), false)

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 1) IOPub already sent error -> do NOT duplicate.
	outs, err := client.ExecuteCode(context.Background(), "with_iopub_error", ColabOneExecuteOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("with_iopub_error failed: %v", err)
	}
	if len(outs) != 1 || outs[0].OutputType != "error" || outs[0].Ename != "ValueError" || outs[0].Evalue != "bad value" || len(outs[0].Traceback) != 2 {
		t.Fatalf("unexpected outs for with_iopub_error: %#v", outs)
	}

	// 2) No IOPub error -> synthesize from execute_reply.
	outs, err = client.ExecuteCode(context.Background(), "reply_only_error", ColabOneExecuteOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("reply_only_error failed: %v", err)
	}
	if len(outs) != 1 || outs[0].OutputType != "error" || outs[0].Ename != "RuntimeError" || outs[0].Evalue != "boom" || len(outs[0].Traceback) != 1 || outs[0].Traceback[0] != "line 1" {
		t.Fatalf("unexpected outs for reply_only_error: %#v", outs)
	}

	// 3) Missing ename/evalue in execute_reply -> defaults "Error" / "Unknown error".
	outs, err = client.ExecuteCode(context.Background(), "reply_only_defaults", ColabOneExecuteOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("reply_only_defaults failed: %v", err)
	}
	if len(outs) != 1 || outs[0].OutputType != "error" || outs[0].Ename != "Error" || outs[0].Evalue != "Unknown error" || outs[0].Traceback == nil {
		t.Fatalf("unexpected outs for reply_only_defaults: %#v", outs)
	}
}

func TestKeepalivePingImmediateServerCloseDrainAndLeakFreeShutdown(t *testing.T) {
	var pingCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-ping"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/kernels/k-ping":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/kernels/k-ping/channels":
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
				OnPingReceived: func(_ context.Context, _ []byte) bool {
					pingCount.Add(1)
					return true
				},
			})
			if err != nil {
				return
			}
			defer conn.CloseNow()

			for {
				req, err := wsReadMsg(r.Context(), conn)
				if err != nil {
					if !errors.Is(err, io.EOF) {
						return
					}
					return
				}
				code := stringFromMap(req.Content, "code")
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
					"execution_state": "busy",
				}))
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
					"name": "stdout",
					"text": "ok:" + code,
				}))
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
					"execution_state": "idle",
				}))
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("shell", "execute_reply", req.MsgID, map[string]any{
					"status": "ok",
				}))
				if code == "close_immediately" {
					// Immediately close WS after sending final frames to test drain race.
					_ = conn.Close(websocket.StatusNormalClosure, "done")
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Warm up runtime and capture goroutine baseline.
	runtime.GC()
	time.Sleep(30 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for round := 0; round < 5; round++ {
		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:    srv.URL,
			ProxyToken:   "tok",
			PingInterval: 30 * time.Millisecond,
		})
		if err := client.Start(context.Background()); err != nil {
			t.Fatalf("round %d Start failed: %v", round, err)
		}

		// Wait long enough on round 0 to ensure keepalive pings actually fire.
		if round == 0 {
			deadline := time.Now().Add(1 * time.Second)
			for pingCount.Load() < 2 && time.Now().Before(deadline) {
				time.Sleep(15 * time.Millisecond)
			}
			if pingCount.Load() < 2 {
				t.Fatalf("expected >=2 keepalive pings, got %d", pingCount.Load())
			}
		}

		// Execute where server closes right after final frames.
		outs, err := client.ExecuteCode(context.Background(), "close_immediately", ColabOneExecuteOptions{
			Timeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("round %d ExecuteCode(close_immediately) failed: %v", round, err)
		}
		if len(outs) != 1 || outs[0].Text != "ok:close_immediately" {
			t.Fatalf("round %d unexpected outputs: %#v", round, outs)
		}

		// Reconnect cleanly, then test concurrent Abort + Stop.
		if err := client.Start(context.Background()); err != nil {
			t.Fatalf("round %d reconnect Start failed: %v", round, err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			client.Abort()
		}()
		go func() {
			defer wg.Done()
			_ = client.Stop(context.Background(), true)
		}()
		wg.Wait()
	}

	// Verify no goroutines leaked across 5 full connect/ping/reconnect/abort/stop cycles.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+4 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	if after > baseline+4 {
		t.Fatalf("possible goroutine leak: baseline=%d after=%d", baseline, after)
	}
}

func TestConcurrentExecuteIsolationAndReentrantCallbacks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/kernels" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-conc"})
			return
		}
		if r.URL.Path == "/api/kernels/k-conc/channels" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()

			var writeMu sync.Mutex
			send := func(msg map[string]any) {
				writeMu.Lock()
				defer writeMu.Unlock()
				wsWriteJSON(t, r.Context(), conn, msg)
			}

			var wg sync.WaitGroup
			defer wg.Wait()
			for {
				req, err := wsReadMsg(r.Context(), conn)
				if err != nil {
					return
				}
				code := stringFromMap(req.Content, "code")
				reqID := req.MsgID
				wg.Add(1)
				go func() {
					defer wg.Done()
					send(makeJupyterMsg("iopub", "status", reqID, map[string]any{"execution_state": "busy"}))
					for i := 0; i < 3; i++ {
						send(makeJupyterMsg("iopub", "stream", reqID, map[string]any{
							"name": "stdout",
							"text": fmt.Sprintf("%s:%d\n", code, i),
						}))
						time.Sleep(2 * time.Millisecond)
					}
					send(makeJupyterMsg("iopub", "status", reqID, map[string]any{"execution_state": "idle"}))
					send(makeJupyterMsg("shell", "execute_reply", reqID, map[string]any{"status": "ok"}))
				}()
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	var client *ColabOneKernelClient
	var reentrantKernelID, reentrantSessionID string
	client = NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "tok",
		OnKernelStarted: func(id string) {
			// Verify calling KernelID() from inside callback never deadlocks.
			reentrantKernelID = client.KernelID()
		},
		OnSessionStarted: func(sid string) {
			// Verify calling SessionID() from inside callback never deadlocks.
			reentrantSessionID = client.SessionID()
		},
	})
	defer client.Stop(context.Background(), false)

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if reentrantKernelID != "k-conc" || reentrantSessionID == "" {
		t.Fatalf("reentrant callbacks got kernel=%q session=%q", reentrantKernelID, reentrantSessionID)
	}

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		workerName := fmt.Sprintf("w%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs, err := client.ExecuteCode(context.Background(), workerName, ColabOneExecuteOptions{
				Timeout: 5 * time.Second,
			})
			if err != nil {
				errs <- fmt.Errorf("%s failed: %w", workerName, err)
				return
			}
			var combined strings.Builder
			for _, o := range outs {
				combined.WriteString(o.Text)
			}
			want := fmt.Sprintf("%s:0\n%s:1\n%s:2\n", workerName, workerName, workerName)
			if combined.String() != want {
				errs <- fmt.Errorf("%s output mismatch: got %q want %q", workerName, combined.String(), want)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestOutputHookAbortAndDynamicRequestHookAndStopCleanup(t *testing.T) {
	var deleteCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-dyn"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/kernels/k-dyn":
			deleteCalls.Add(1)
			// Return 404 on DELETE; Stop must treat 404 as cleanly shut down.
			http.Error(w, "already gone", http.StatusNotFound)
		case r.URL.Path == "/api/kernels/k-dyn/channels":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()

			req, err := wsReadMsg(r.Context(), conn)
			if err != nil {
				return
			}
			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
				"execution_state": "busy",
			}))
			// Send unhandled colab_request (hook returns false) followed by a stream chunk.
			wsWriteJSON(t, r.Context(), conn, map[string]any{
				"channel":       "stdin",
				"header":        map[string]any{"msg_id": "cr-ignore", "msg_type": "colab_request"},
				"parent_header": map[string]any{"msg_id": req.MsgID},
				"metadata":      map[string]any{},
				"content":       map[string]any{"request": map[string]any{"authType": "other"}},
			})
			wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
				"name": "stdout",
				"text": "abort-me\n",
			}))
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "tok",
	})

	var sawColabReq atomic.Bool
	client.SetRequestHook(func(msg *ColabOneWSMessage, _ func(map[string]any, any) error) bool {
		sawColabReq.Store(true)
		return false
	})

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Abort from inside OutputHook must never deadlock.
	outs, err := client.ExecuteCode(context.Background(), "run", ColabOneExecuteOptions{
		Timeout: 5 * time.Second,
		OutputHook: func(out ColabOneKernelOutput) {
			if out.Text == "abort-me\n" {
				client.Abort()
			}
		},
	})
	if !errors.Is(err, ErrKernelAborted) {
		t.Fatalf("expected ErrKernelAborted, got %v", err)
	}
	if !sawColabReq.Load() {
		t.Fatalf("expected dynamic SetRequestHook to observe colab_request")
	}
	if len(outs) != 1 || outs[0].Text != "abort-me\n" {
		t.Fatalf("unexpected outputs before abort: %#v", outs)
	}

	// Stop with an ALREADY-CANCELED context must still issue DELETE and ignore 404 cleanly.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Stop(canceledCtx, true); err != nil {
		t.Fatalf("Stop with canceled ctx and 404 DELETE should succeed, got %v", err)
	}
	if deleteCalls.Load() != 1 {
		t.Fatalf("expected DELETE /api/kernels/k-dyn to be issued even with canceled ctx, got %d", deleteCalls.Load())
	}
}

func TestStartInterruptedByAbortMidDialAndURLBuilding(t *testing.T) {
	// Verify https -> wss conversion and subpath/query preservation.
	c := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  "https://8080-gpu-t4.prod.colab.dev/base/",
		ProxyToken: "tok+1",
	})
	wsURL, err := c.buildWSURL("kid-1", "sid-1")
	if err != nil {
		t.Fatalf("buildWSURL failed: %v", err)
	}
	wantPrefix := "wss://8080-gpu-t4.prod.colab.dev/base/api/kernels/kid-1/channels?"
	if !strings.HasPrefix(wsURL, wantPrefix) ||
		!strings.Contains(wsURL, "session_id=sid-1") ||
		!strings.Contains(wsURL, "colab-runtime-proxy-token=tok%2B1") {
		t.Fatalf("unexpected wsURL: %s", wsURL)
	}

	// Verify Abort() immediately interrupts a hanging Start() dial.
	dialEntered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/kernels/k-hangdial/channels" {
			close(dialEntered)
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	client := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:      srv.URL,
		ProxyToken:     "tok",
		KernelID:       "k-hangdial",
		ConnectTimeout: 10 * time.Second,
	})
	defer client.Stop(context.Background(), false)

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Start(context.Background())
	}()

	<-dialEntered
	started := time.Now()
	client.Abort()
	startErr := <-errCh
	if !errors.Is(startErr, ErrKernelAborted) {
		t.Fatalf("expected ErrKernelAborted when Abort interrupts Start, got %v", startErr)
	}
	if time.Since(started) > 1*time.Second {
		t.Fatalf("Abort took too long to cancel Start: %v", time.Since(started))
	}
}

func TestStartShutsDownCreatedKernelOnRetryFailurePreStartAbortHeaderDateAndTokenRedaction(t *testing.T) {
	// 1. ShutdownOnWSTimeout must DELETE created kernel even if failure happens on retry attempt > 0.
	var deleteCalls atomic.Int32
	var wsAttempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"k-retry-leak"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/kernels/k-retry-leak":
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/kernels/k-retry-leak/channels":
			if wsAttempts.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:           srv.URL,
		ProxyToken:          "secret-ws-tok-abc",
		ShutdownOnWSTimeout: true,
		MaxConnectRetries:   3,
		RetryBaseDelay:      5 * time.Millisecond,
	})
	if err := c.Start(context.Background()); err == nil {
		t.Fatal("expected Start to fail on 403")
	}
	if deleteCalls.Load() != 1 {
		t.Fatalf("expected orphaned kernel DELETE after retryable WS failure, got %d", deleteCalls.Load())
	}

	// 2. Pre-Start Abort() must NOT be silently cleared by Start().
	preAborted := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "secret-ws-tok-abc",
	})
	preAborted.Abort()
	if err := preAborted.Start(context.Background()); !errors.Is(err, ErrKernelAborted) {
		t.Fatalf("pre-Start Abort() lost: got %v, want ErrKernelAborted", err)
	}

	// 3. Header date must use 6-digit microsecond precision for jupyter_client ISO8601_PAT.
	msg := preAborted.buildMessage("shell", "execute_request", nil, nil, map[string]any{"code": "1"})
	dateStr, _ := msg.Header["date"].(string)
	if !strings.HasSuffix(dateStr, "Z") || len(dateStr) != len("2006-01-02T15:04:05.000000Z") {
		t.Fatalf("unexpected header.date format: %q", dateStr)
	}

	// 4. Transport error from closed port must never leak colab-runtime-proxy-token.
	badClient := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:         "http://127.0.0.1:1",
		ProxyToken:        "secret-ws-tok-xyz",
		MaxConnectRetries: 1,
		ConnectTimeout:    500 * time.Millisecond,
	})
	if err := badClient.Start(context.Background()); err == nil || strings.Contains(err.Error(), "secret-ws-tok-xyz") {
		t.Fatalf("transport error leaked proxy token or succeeded: %v", err)
	}

	// 5. Stop() -> Abort() -> Start() must still respect the post-Stop Abort(),
	//    while Abort() -> Stop() clears aborted for a fresh subsequent Start().
	stopThenAbort := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:  srv.URL,
		ProxyToken: "secret-ws-tok-abc",
	})
	_ = stopThenAbort.Stop(context.Background(), false)
	stopThenAbort.Abort()
	if err := stopThenAbort.Start(context.Background()); !errors.Is(err, ErrKernelAborted) {
		t.Fatalf("Stop() -> Abort() -> Start() dropped Abort: got %v, want ErrKernelAborted", err)
	}
	// After Stop() completes, aborted is reset so a fresh Start() is allowed to attempt connection.
	_ = stopThenAbort.Stop(context.Background(), false)
	if err := stopThenAbort.Start(context.Background()); errors.Is(err, ErrKernelAborted) {
		t.Fatalf("Abort() -> Stop() -> Start() remained permanently aborted: %v", err)
	}
}

type wedgedWriteConn struct {
	net.Conn
	blockWrites *atomic.Bool
	unblockCh   chan struct{}
	closeOnce   sync.Once
}

func (w *wedgedWriteConn) Write(p []byte) (int, error) {
	if w.blockWrites.Load() {
		<-w.unblockCh
		return 0, net.ErrClosed
	}
	return w.Conn.Write(p)
}

func (w *wedgedWriteConn) Close() error {
	w.closeOnce.Do(func() {
		close(w.unblockCh)
	})
	return w.Conn.Close()
}

func TestExecuteCodeWriteTimeoutVsExecutionTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "k-wedged"})
		case r.URL.Path == "/api/kernels/k-wedged/channels":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()

			for {
				req, err := wsReadMsg(r.Context(), conn)
				if err != nil {
					return
				}
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
					"execution_state": "busy",
				}))
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "stream", req.MsgID, map[string]any{
					"name": "stdout",
					"text": "recovered\n",
				}))
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("iopub", "status", req.MsgID, map[string]any{
					"execution_state": "idle",
				}))
				wsWriteJSON(t, r.Context(), conn, makeJupyterMsg("shell", "execute_reply", req.MsgID, map[string]any{
					"status": "ok",
				}))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	newWedgedClient := func(t *testing.T, writeTimeout time.Duration) (*ColabOneKernelClient, *atomic.Bool) {
		t.Helper()
		var blockWrites atomic.Bool
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		tr := &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				rawConn, err := dialer.DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return &wedgedWriteConn{
					Conn:        rawConn,
					blockWrites: &blockWrites,
					unblockCh:   make(chan struct{}),
				}, nil
			},
		}
		t.Cleanup(tr.CloseIdleConnections)

		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:    srv.URL,
			ProxyToken:   "tok",
			KernelID:     "k-wedged",
			PingInterval: -1,
			HTTPClient:   &http.Client{Transport: tr},
		})
		client.writeTimeout = writeTimeout
		t.Cleanup(func() {
			blockWrites.Store(false)
			_ = client.Stop(context.Background(), false)
		})
		if err := client.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		return client, &blockWrites
	}

	t.Run("internal write timeout while execCtx active returns ErrKernelDisconnected and closes ws", func(t *testing.T) {
		client, blockWrites := newWedgedClient(t, 80*time.Millisecond)
		blockWrites.Store(true)

		started := time.Now()
		_, err := client.ExecuteCode(context.Background(), "print('rpc')", ColabOneExecuteOptions{
			Timeout: 5 * time.Second,
		})
		elapsed := time.Since(started)

		if err == nil {
			t.Fatalf("expected error on wedged socket write timeout, got nil")
		}
		if !errors.Is(err, ErrKernelDisconnected) {
			t.Fatalf("expected ErrKernelDisconnected on internal write timeout, got %v", err)
		}
		if errors.Is(err, ErrKernelTimeout) {
			t.Fatalf("internal write timeout must NOT be misclassified as ErrKernelTimeout: %v", err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("internal write timeout must NOT wrap context.DeadlineExceeded as exec timeout: %v", err)
		}
		if strings.Contains(err.Error(), COLABONE_KERNEL_EXEC_TIMEOUT_MSG) {
			t.Fatalf("error message must not contain kernel exec timeout text: %q", err.Error())
		}
		if client.IsConnected() {
			t.Fatalf("expected ws connection to be closed after internal write timeout")
		}
		if elapsed > 2*time.Second {
			t.Fatalf("internal write timeout took too long (%v); waited for opts.Timeout instead of writeTimeout", elapsed)
		}

		// After transport recovers, Start() must reconnect cleanly and ExecuteCode must succeed.
		blockWrites.Store(false)
		if err := client.Start(context.Background()); err != nil {
			t.Fatalf("reconnect Start after wedged write timeout failed: %v", err)
		}
		outs, err := client.ExecuteCode(context.Background(), "print('ok')", ColabOneExecuteOptions{
			Timeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("ExecuteCode after reconnect failed: %v", err)
		}
		if len(outs) != 1 || outs[0].Text != "recovered\n" {
			t.Fatalf("unexpected outputs after reconnect: %#v", outs)
		}
	})

	t.Run("actual opts.Timeout expiry during stalled write returns ErrKernelTimeout", func(t *testing.T) {
		client, blockWrites := newWedgedClient(t, 5*time.Second)
		blockWrites.Store(true)

		started := time.Now()
		_, err := client.ExecuteCode(context.Background(), "print('rpc')", ColabOneExecuteOptions{
			Timeout: 80 * time.Millisecond,
		})
		elapsed := time.Since(started)

		if err == nil {
			t.Fatalf("expected ErrKernelTimeout, got nil")
		}
		if !errors.Is(err, ErrKernelTimeout) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected ErrKernelTimeout / context.DeadlineExceeded, got %v", err)
		}
		if err.Error() != COLABONE_KERNEL_EXEC_TIMEOUT_MSG {
			t.Fatalf("err.Error() = %q, want %q", err.Error(), COLABONE_KERNEL_EXEC_TIMEOUT_MSG)
		}
		if errors.Is(err, ErrKernelDisconnected) {
			t.Fatalf("actual opts.Timeout expiry must NOT be misclassified as ErrKernelDisconnected: %v", err)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("opts.Timeout expiry took too long: %v", elapsed)
		}
		// Once ws.conn.Write was cancelled by opts.Timeout, coder/websocket closes the
		// underlying socket, so IsConnected() must be false immediately and an immediate
		// Start() must reconnect cleanly instead of skipping dial on a dead socket.
		if client.IsConnected() {
			t.Fatalf("expected IsConnected() == false immediately after stalled write timed out via opts.Timeout")
		}
		blockWrites.Store(false)
		if err := client.Start(context.Background()); err != nil {
			t.Fatalf("immediate reconnect Start after stalled write opts.Timeout failed: %v", err)
		}
		if !client.IsConnected() {
			t.Fatalf("expected IsConnected() == true after reconnect Start")
		}
		outs, err := client.ExecuteCode(context.Background(), "print('ok')", ColabOneExecuteOptions{
			Timeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("ExecuteCode after reconnect failed: %v", err)
		}
		if len(outs) != 1 || outs[0].Text != "recovered\n" {
			t.Fatalf("unexpected outputs after reconnect: %#v", outs)
		}
	})

	t.Run("parent ctx deadline expiry during stalled write returns ErrKernelTimeout", func(t *testing.T) {
		client, blockWrites := newWedgedClient(t, 5*time.Second)
		blockWrites.Store(true)

		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()

		_, err := client.ExecuteCode(ctx, "print('rpc')", ColabOneExecuteOptions{})
		if !errors.Is(err, ErrKernelTimeout) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected ErrKernelTimeout on parent ctx deadline, got %v", err)
		}
		if err.Error() != COLABONE_KERNEL_EXEC_TIMEOUT_MSG {
			t.Fatalf("err.Error() = %q, want %q", err.Error(), COLABONE_KERNEL_EXEC_TIMEOUT_MSG)
		}
		if errors.Is(err, ErrKernelDisconnected) {
			t.Fatalf("parent ctx deadline must NOT be misclassified as ErrKernelDisconnected: %v", err)
		}
		if client.IsConnected() {
			t.Fatalf("expected IsConnected() == false immediately after parent ctx deadline aborted stalled write")
		}
	})

	t.Run("parent ctx cancel during stalled write returns context.Canceled", func(t *testing.T) {
		client, blockWrites := newWedgedClient(t, 5*time.Second)
		blockWrites.Store(true)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(60 * time.Millisecond)
			cancel()
		}()

		_, err := client.ExecuteCode(ctx, "print('rpc')", ColabOneExecuteOptions{
			Timeout: 5 * time.Second,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if errors.Is(err, ErrKernelTimeout) || errors.Is(err, ErrKernelDisconnected) {
			t.Fatalf("explicit parent cancel must not be misclassified: %v", err)
		}
		if client.IsConnected() {
			t.Fatalf("expected IsConnected() == false immediately after parent ctx cancel aborted stalled write")
		}
	})

	t.Run("pre-write canceled parent ctx does not close healthy ws", func(t *testing.T) {
		client, _ := newWedgedClient(t, 5*time.Second)

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := client.ExecuteCode(canceledCtx, "print('rpc')", ColabOneExecuteOptions{
			Timeout: 5 * time.Second,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled on pre-canceled ctx, got %v", err)
		}
		if !client.IsConnected() {
			t.Fatalf("pre-write canceled ctx must leave healthy ws connected")
		}
		outs, err := client.ExecuteCode(context.Background(), "print('still-ok')", ColabOneExecuteOptions{
			Timeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("ExecuteCode on healthy ws after pre-canceled call failed: %v", err)
		}
		if len(outs) != 1 || outs[0].Text != "recovered\n" {
			t.Fatalf("unexpected outputs on healthy ws: %#v", outs)
		}
	})
}

func TestHTTP302RedirectNotFollowedOnRESTAndWebSocket(t *testing.T) {
	const secretToken = "redirect-secret-proxy-token-999"

	var targetHits atomic.Int32
	var leakedHeader atomic.Value
	var leakedReferer atomic.Value
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		if tok := r.Header.Get("X-Colab-Runtime-Proxy-Token"); tok != "" {
			leakedHeader.Store(tok)
		}
		if ref := r.Header.Get("Referer"); ref != "" {
			leakedReferer.Store(ref)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	t.Run("POST /api/kernels 302 is not followed", func(t *testing.T) {
		targetHits.Store(0)
		var originHits atomic.Int32

		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/kernels" {
				originHits.Add(1)
				http.Redirect(w, r, redirectTarget.URL+"/steal-rest", http.StatusFound)
				return
			}
			http.NotFound(w, r)
		}))
		defer origin.Close()

		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:      origin.URL,
			ProxyToken:     secretToken,
			RetryBaseDelay: 5 * time.Millisecond,
		})
		err := client.Start(context.Background())
		if err == nil {
			t.Fatalf("expected 302 error from POST /api/kernels, got nil")
		}
		var httpErr *ColabOneHTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusFound {
			t.Fatalf("expected ColabOneHTTPError 302, got %T: %v", err, err)
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Fatalf("302 error leaked proxy token: %s", err.Error())
		}
		if originHits.Load() != 1 {
			t.Fatalf("expected 1 POST attempt (no retries on 302), got %d", originHits.Load())
		}
		if targetHits.Load() != 0 {
			t.Fatalf("redirect target was contacted %d times (header=%v referer=%v)", targetHits.Load(), leakedHeader.Load(), leakedReferer.Load())
		}
	})

	for _, tc := range []struct {
		name     string
		location string
	}{
		{name: "http target", location: redirectTarget.URL + "/steal-ws"},
		{name: "ws target", location: "ws://" + strings.TrimPrefix(redirectTarget.URL, "http://") + "/steal-ws"},
	} {
		t.Run("GET /api/kernels/{id}/channels 302 is not followed ("+tc.name+")", func(t *testing.T) {
			targetHits.Store(0)
			var originHits atomic.Int32

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api/kernels/k-redir/channels" {
					originHits.Add(1)
					w.Header().Set("Location", tc.location)
					w.WriteHeader(http.StatusFound)
					return
				}
				http.NotFound(w, r)
			}))
			defer origin.Close()

			client := NewColabOneKernelClient(ColabOneKernelOptions{
				ServerURL:      origin.URL,
				ProxyToken:     secretToken,
				KernelID:       "k-redir",
				RetryBaseDelay: 5 * time.Millisecond,
			})
			err := client.Start(context.Background())
			if err == nil {
				t.Fatalf("expected 302 error from WS handshake, got nil")
			}
			var httpErr *ColabOneHTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusFound {
				t.Fatalf("expected ColabOneHTTPError 302, got %T: %v", err, err)
			}
			if strings.Contains(err.Error(), secretToken) {
				t.Fatalf("WS 302 error leaked proxy token: %s", err.Error())
			}
			if originHits.Load() != 1 {
				t.Fatalf("expected 1 WS attempt (no retries on 302), got %d", originHits.Load())
			}
			if targetHits.Load() != 0 {
				t.Fatalf("WS redirect target was contacted %d times (header=%v referer=%v)", targetHits.Load(), leakedHeader.Load(), leakedReferer.Load())
			}
		})
	}

	t.Run("DELETE /api/kernels/{id} 302 is not followed", func(t *testing.T) {
		targetHits.Store(0)
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && r.URL.Path == "/api/kernels/k-del-redir" {
				http.Redirect(w, r, redirectTarget.URL+"/steal-del", http.StatusFound)
				return
			}
			http.NotFound(w, r)
		}))
		defer origin.Close()

		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:  origin.URL,
			ProxyToken: secretToken,
			KernelID:   "k-del-redir",
		})
		err := client.Stop(context.Background(), true)
		if err == nil {
			t.Fatalf("expected 302 error from DELETE /api/kernels/{id}, got nil")
		}
		var httpErr *ColabOneHTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusFound {
			t.Fatalf("expected ColabOneHTTPError 302, got %T: %v", err, err)
		}
		if targetHits.Load() != 0 {
			t.Fatalf("DELETE redirect target was contacted %d times", targetHits.Load())
		}
	})

	t.Run("custom HTTPClient with nil CheckRedirect still blocks redirects without mutating caller client", func(t *testing.T) {
		targetHits.Store(0)
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, redirectTarget.URL+"/steal-custom", http.StatusFound)
		}))
		defer origin.Close()

		customClient := &http.Client{Transport: origin.Client().Transport}
		client := NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:  origin.URL,
			ProxyToken: secretToken,
			KernelID:   "k-custom",
			HTTPClient: customClient,
		})
		err := client.Start(context.Background())
		var httpErr *ColabOneHTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusFound {
			t.Fatalf("expected ColabOneHTTPError 302, got %T: %v", err, err)
		}
		if targetHits.Load() != 0 {
			t.Fatalf("redirect target contacted %d times with custom HTTPClient", targetHits.Load())
		}
		if customClient.CheckRedirect != nil {
			t.Fatalf("NewColabOneKernelClient mutated caller's HTTPClient.CheckRedirect")
		}
	})
}

func TestSanitizeURLErrorDeepRedactionAndErrorPreservation(t *testing.T) {
	const tok = "ws-super-secret-token-7788"

	if sanitizeURLError(nil) != nil {
		t.Fatal("sanitizeURLError(nil) must return nil")
	}

	// Clean error without proxy token is returned unchanged.
	cleanErr := errors.New("plain kernel error")
	if got := sanitizeURLError(cleanErr); got != cleanErr {
		t.Fatalf("clean error should be returned unchanged, got %v", got)
	}

	// Multi-layer wrapped *url.Error (exact coder/websocket.Dial wrapping shape)
	// preserves outer context, inner sentinel, and *url.Error for errors.Is / errors.As.
	wsDialUErr := &url.Error{
		Op:  "Get",
		URL: "https://8080-gpu-t4.prod.colab.dev/api/kernels/k1/channels?colab-runtime-proxy-token=" + tok + "&session_id=s1",
		Err: context.DeadlineExceeded,
	}
	wrappedDial := fmt.Errorf("failed to WebSocket dial: %w", fmt.Errorf("failed to send handshake request: %w", wsDialUErr))
	sanitizedDial := sanitizeURLError(wrappedDial)
	if strings.Contains(sanitizedDial.Error(), tok) {
		t.Fatalf("wrapped WS *url.Error leaked token: %v", sanitizedDial)
	}
	if !strings.Contains(sanitizedDial.Error(), "failed to WebSocket dial") ||
		!strings.Contains(sanitizedDial.Error(), "failed to send handshake request") {
		t.Fatalf("wrapped WS *url.Error lost outer context: %v", sanitizedDial)
	}
	if !errors.Is(sanitizedDial, context.DeadlineExceeded) {
		t.Fatalf("wrapped WS *url.Error lost context.DeadlineExceeded: %v", sanitizedDial)
	}
	if !isTimeoutError(sanitizedDial, nil) {
		t.Fatalf("wrapped WS *url.Error lost timeout classification: %v", sanitizedDial)
	}
	var extractedUErr *url.Error
	if !errors.As(sanitizedDial, &extractedUErr) || strings.Contains(extractedUErr.URL, tok) || extractedUErr.URL != "https://8080-gpu-t4.prod.colab.dev/api/kernels/k1/channels" {
		t.Fatalf("extracted *url.Error = %+v, want sanitized URL without query", extractedUErr)
	}

	// Wrapped malformed *url.Error (unparseable port / control char) scrubs URL cleanly.
	malformedUErr := &url.Error{
		Op:  "parse",
		URL: "https://rt.invalid:badport/api/kernels?COLAB-RUNTIME-PROXY-TOKEN=" + tok,
		Err: context.DeadlineExceeded,
	}
	wrappedMalformed := fmt.Errorf("dial kernel failed: %w", malformedUErr)
	sanitizedMalformed := sanitizeURLError(wrappedMalformed)
	if strings.Contains(sanitizedMalformed.Error(), tok) {
		t.Fatalf("wrapped malformed *url.Error leaked token: %v", sanitizedMalformed)
	}
	if !strings.Contains(sanitizedMalformed.Error(), "dial kernel failed") {
		t.Fatalf("wrapped malformed *url.Error lost outer context: %v", sanitizedMalformed)
	}
	if !errors.Is(sanitizedMalformed, context.DeadlineExceeded) {
		t.Fatalf("wrapped malformed *url.Error lost context.DeadlineExceeded: %v", sanitizedMalformed)
	}
	var extractedMalformed *url.Error
	if !errors.As(sanitizedMalformed, &extractedMalformed) || extractedMalformed.URL != "<invalid-url>" {
		t.Fatalf("extracted malformed *url.Error = %+v, want URL=<invalid-url>", extractedMalformed)
	}

	// Nested *url.Error inside uerr.Err (clean outer URL, leaking inner URL and inner plain error).
	nestedUErr := &url.Error{
		Op:  "Get",
		URL: "https://rt.example.invalid/api/kernels",
		Err: &url.Error{
			Op:  "parse",
			URL: "https://rt.invalid\x7f/api/kernels?colab-runtime-proxy-token=" + tok,
			Err: fmt.Errorf("proxy rejected COLAB-RUNTIME-PROXY-TOKEN=%s: %w", tok, context.Canceled),
		},
	}
	sanitizedNested := sanitizeURLError(nestedUErr)
	if strings.Contains(sanitizedNested.Error(), tok) {
		t.Fatalf("nested *url.Error leaked token: %v", sanitizedNested)
	}
	if !errors.Is(sanitizedNested, context.Canceled) {
		t.Fatalf("nested *url.Error lost inner context.Canceled: %v", sanitizedNested)
	}

	// Unwrap() []error via errors.Join and multi-%w fmt.Errorf preserve all causes and outer context.
	sentinel := errors.New("sentinel cause")
	joined := errors.Join(sentinel, malformedUErr)
	sanitizedJoined := sanitizeURLError(joined)
	if strings.Contains(sanitizedJoined.Error(), tok) {
		t.Fatalf("errors.Join error leaked token: %v", sanitizedJoined)
	}
	if !errors.Is(sanitizedJoined, sentinel) || !errors.Is(sanitizedJoined, context.DeadlineExceeded) {
		t.Fatalf("errors.Join lost causes: %v", sanitizedJoined)
	}

	multiWrap := fmt.Errorf("outer context (%w): %w", sentinel, malformedUErr)
	sanitizedMulti := sanitizeURLError(multiWrap)
	if strings.Contains(sanitizedMulti.Error(), tok) {
		t.Fatalf("multi-%%w error leaked token: %v", sanitizedMulti)
	}
	if !strings.Contains(sanitizedMulti.Error(), "outer context") {
		t.Fatalf("multi-%%w error lost outer context: %v", sanitizedMulti)
	}
	if !errors.Is(sanitizedMulti, sentinel) || !errors.Is(sanitizedMulti, context.DeadlineExceeded) {
		t.Fatalf("multi-%%w error lost wrapped causes: %v", sanitizedMulti)
	}

	// Arbitrary non-*url.Error strings containing raw or %3D-encoded token (case-insensitive,
	// including UTF-8 'İ' byte-length edge case) scrub tokens and preserve wrapped sentinels.
	rawPlain := fmt.Errorf("İ proxy rejected https://rt.invalid/api/kernels?Colab-Runtime-Proxy-Token=%s&session_id=s1: %w", tok, context.Canceled)
	sanitizedRaw := sanitizeURLError(rawPlain)
	if strings.Contains(sanitizedRaw.Error(), tok) {
		t.Fatalf("raw non-*url.Error leaked token: %v", sanitizedRaw)
	}
	if !strings.Contains(sanitizedRaw.Error(), "Colab-Runtime-Proxy-Token=REDACTED&session_id=s1") {
		t.Fatalf("unexpected raw non-*url.Error redaction: %v", sanitizedRaw)
	}
	if !errors.Is(sanitizedRaw, context.Canceled) {
		t.Fatalf("raw non-*url.Error lost wrapped sentinel: %v", sanitizedRaw)
	}

	encodedUpper := errors.New("redirect target https%3A%2F%2Frt.invalid%3Fcolab-runtime-proxy-token%3D" + tok + "%26session_id%3Ds1 failed")
	sanitizedEncUpper := sanitizeURLError(encodedUpper)
	if strings.Contains(sanitizedEncUpper.Error(), tok) {
		t.Fatalf("percent-encoded (%%3D) error leaked token: %v", sanitizedEncUpper)
	}
	if !strings.Contains(sanitizedEncUpper.Error(), "colab-runtime-proxy-token%3DREDACTED%26session_id%3Ds1") {
		t.Fatalf("unexpected %%3D redaction: %v", sanitizedEncUpper)
	}

	encodedMixed := fmt.Errorf("upstream COLAB-RUNTIME-PROXY-TOKEN%%3d%s%%20rejected: %w", tok, sentinel)
	sanitizedEncMixed := sanitizeURLError(encodedMixed)
	if strings.Contains(sanitizedEncMixed.Error(), tok) {
		t.Fatalf("mixed-case percent-encoded error leaked token: %v", sanitizedEncMixed)
	}
	if !errors.Is(sanitizedEncMixed, sentinel) {
		t.Fatalf("mixed-case percent-encoded error lost wrapped sentinel: %v", sanitizedEncMixed)
	}

	// Path-embedded token in sanitizeURLForError must also be scrubbed.
	if got := sanitizeURLForError("https://rt.invalid/api/kernels/colab-runtime-proxy-token=" + tok); strings.Contains(got, tok) {
		t.Fatalf("sanitizeURLForError leaked path-embedded token: %q", got)
	}

	// End-to-end WS dial transport error preserves coder/websocket outer context while scrubbing token.
	wsBadClient := NewColabOneKernelClient(ColabOneKernelOptions{
		ServerURL:         "http://127.0.0.1:1",
		ProxyToken:        tok,
		KernelID:          "k-closed-port",
		MaxConnectRetries: 1,
		ConnectTimeout:    500 * time.Millisecond,
	})
	dialErr := wsBadClient.Start(context.Background())
	if dialErr == nil {
		t.Fatal("expected WS dial error against closed port, got nil")
	}
	if strings.Contains(dialErr.Error(), tok) {
		t.Fatalf("end-to-end WS dial error leaked token: %v", dialErr)
	}
	if !strings.Contains(dialErr.Error(), "failed to WebSocket dial") {
		t.Fatalf("end-to-end WS dial error lost outer context: %v", dialErr)
	}
}

type emptyMsgSingleWrap struct {
	err error
}

func (e *emptyMsgSingleWrap) Error() string { return "" }
func (e *emptyMsgSingleWrap) Unwrap() error { return e.err }

type customOuterSingleWrap struct {
	msg string
	err error
}

func (e *customOuterSingleWrap) Error() string { return e.msg }
func (e *customOuterSingleWrap) Unwrap() error { return e.err }

type customOuterMultiWrap struct {
	msg  string
	errs []error
}

func (e *customOuterMultiWrap) Error() string   { return e.msg }
func (e *customOuterMultiWrap) Unwrap() []error { return e.errs }

type emptyOnSecondError struct {
	calls atomic.Int32
	first string
}

func (e *emptyOnSecondError) Error() string {
	if e.calls.Add(1) == 1 {
		return e.first
	}
	return ""
}

func TestSanitizeURLErrorEmptyInnerErrorDoesNotInterleave(t *testing.T) {
	const tok = "empty-inner-secret-token-31415"

	tokenUErr := &url.Error{
		Op:  "Get",
		URL: "https://rt.example.invalid/api/kernels/k1/channels?colab-runtime-proxy-token=" + tok,
		Err: context.DeadlineExceeded,
	}

	// Static empty inner .Error() inside single and multi wrappers must preserve outer text and causes.
	outerSingle := fmt.Errorf("outer single: %w", &emptyMsgSingleWrap{err: tokenUErr})
	gotSingle := sanitizeURLError(outerSingle)
	if strings.Contains(gotSingle.Error(), tok) {
		t.Fatalf("single empty-inner error leaked token: %v", gotSingle)
	}
	if gotSingle.Error() != "outer single: " {
		t.Fatalf("single empty-inner error corrupted outer text: got %q, want %q", gotSingle.Error(), "outer single: ")
	}
	if !errors.Is(gotSingle, context.DeadlineExceeded) {
		t.Fatalf("single empty-inner error lost DeadlineExceeded: %v", gotSingle)
	}
	var extracted *url.Error
	if !errors.As(gotSingle, &extracted) || strings.Contains(extracted.URL, tok) {
		t.Fatalf("single empty-inner error lost or leaked *url.Error: %+v", extracted)
	}

	sentinel := errors.New("sentinel")
	outerMulti := fmt.Errorf("outer multi (%w / %w)", sentinel, &emptyMsgSingleWrap{err: tokenUErr})
	gotMulti := sanitizeURLError(outerMulti)
	if strings.Contains(gotMulti.Error(), tok) {
		t.Fatalf("multi empty-inner error leaked token: %v", gotMulti)
	}
	if gotMulti.Error() != "outer multi (sentinel / )" {
		t.Fatalf("multi empty-inner error corrupted outer text: got %q", gotMulti.Error())
	}
	if !errors.Is(gotMulti, sentinel) || !errors.Is(gotMulti, context.DeadlineExceeded) {
		t.Fatalf("multi empty-inner error lost wrapped causes: %v", gotMulti)
	}

	// Inner error whose .Error() returns "" on re-read after sanitization must never trigger
	// strings.ReplaceAll(outer, "", redacted), which would interleave text between every rune.
	flakySingle := &customOuterSingleWrap{
		msg: "outer-single-msg",
		err: &emptyOnSecondError{first: "proxy colab-runtime-proxy-token=" + tok},
	}
	gotFlakySingle := sanitizeURLError(flakySingle)
	if strings.Contains(gotFlakySingle.Error(), tok) {
		t.Fatalf("flaky single empty-inner leaked token: %v", gotFlakySingle)
	}
	if gotFlakySingle.Error() != "outer-single-msg" {
		t.Fatalf("empty oldMsg interleaved into single outer text: got %q, want %q", gotFlakySingle.Error(), "outer-single-msg")
	}
	if inner := errors.Unwrap(gotFlakySingle); inner == nil || strings.Contains(inner.Error(), tok) || !strings.Contains(inner.Error(), "REDACTED") {
		t.Fatalf("expected sanitized inner error, got %v", inner)
	}

	flakyMulti := &customOuterMultiWrap{
		msg:  "outer-multi-msg",
		errs: []error{sentinel, &emptyOnSecondError{first: "proxy colab-runtime-proxy-token=" + tok}},
	}
	gotFlakyMulti := sanitizeURLError(flakyMulti)
	if strings.Contains(gotFlakyMulti.Error(), tok) {
		t.Fatalf("flaky multi empty-inner leaked token: %v", gotFlakyMulti)
	}
	if gotFlakyMulti.Error() != "outer-multi-msg" {
		t.Fatalf("empty oldMsg interleaved into multi outer text: got %q, want %q", gotFlakyMulti.Error(), "outer-multi-msg")
	}
	if !errors.Is(gotFlakyMulti, sentinel) {
		t.Fatalf("flaky multi empty-inner lost sentinel: %v", gotFlakyMulti)
	}
}

func TestColabOneKernelOptionsStringAndGoStringRedactProxyToken(t *testing.T) {
	const secretToken = "opts-secret-proxy-token-4242"
	const urlToken = "url-secret-proxy-token-5353"

	opts := ColabOneKernelOptions{
		ServerURL:         "https://8080-gpu-t4.prod.colab.dev?colab-runtime-proxy-token=" + urlToken,
		ProxyToken:        secretToken,
		KernelID:          "k-opt-1",
		SessionID:         "s-opt-1",
		ConnectTimeout:    10 * time.Second,
		MaxConnectRetries: 2,
	}

	formatted := []string{
		opts.String(),
		opts.GoString(),
		fmt.Sprintf("%s", opts),
		fmt.Sprintf("%v", opts),
		fmt.Sprintf("%+v", opts),
		fmt.Sprintf("%#v", opts),
		fmt.Sprintf("%s", &opts),
		fmt.Sprintf("%v", &opts),
		fmt.Sprintf("%+v", &opts),
		fmt.Sprintf("%#v", &opts),
	}
	for i, s := range formatted {
		if strings.Contains(s, secretToken) {
			t.Fatalf("format[%d] leaked ProxyToken: %s", i, s)
		}
		if strings.Contains(s, urlToken) {
			t.Fatalf("format[%d] leaked ServerURL token: %s", i, s)
		}
		if !strings.Contains(s, `ProxyToken:"[redacted]"`) {
			t.Fatalf("format[%d] missing redacted marker: %s", i, s)
		}
		if !strings.Contains(s, `KernelID:"k-opt-1"`) {
			t.Fatalf("format[%d] missing KernelID: %s", i, s)
		}
	}

	emptyOpts := ColabOneKernelOptions{
		ServerURL: "https://8080-gpu-t4.prod.colab.dev",
		KernelID:  "k-empty",
	}
	if got := emptyOpts.String(); !strings.Contains(got, `ProxyToken:""`) || strings.Contains(got, "[redacted]") {
		t.Fatalf("unexpected String() for empty ProxyToken: %s", got)
	}
	if got := emptyOpts.GoString(); !strings.Contains(got, `ProxyToken:""`) || strings.Contains(got, "[redacted]") {
		t.Fatalf("unexpected GoString() for empty ProxyToken: %s", got)
	}
}

type hookRoundTripper struct {
	base http.RoundTripper
	hook func(req *http.Request, resp *http.Response, err error)
}

func (h *hookRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := h.base.RoundTrip(req)
	if h.hook != nil {
		h.hook(req, resp, err)
	}
	return resp, err
}

func TestStartPostDialContextCancelAndDeadlineReturnContextErrNotAborted(t *testing.T) {
	newWSServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/kernels/k-post-dial/channels" {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for {
					if _, _, err := conn.Read(r.Context()); err != nil {
						return
					}
				}
			}
			http.NotFound(w, r)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	t.Run("context_canceled_after_dial", func(t *testing.T) {
		srv := newWSServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var client *ColabOneKernelClient
		var hookOnce sync.Once
		rt := &hookRoundTripper{
			base: srv.Client().Transport,
			hook: func(req *http.Request, resp *http.Response, err error) {
				if err == nil && resp != nil && resp.StatusCode == http.StatusSwitchingProtocols {
					hookOnce.Do(func() {
						// Hold client.mu across websocket.Dial return so Start deterministically
						// enters the post-dial check at c.mu.Lock() with ctx already canceled
						// (and aborted/stopped still false).
						client.mu.Lock()
						go func() {
							time.Sleep(15 * time.Millisecond)
							cancel()
							client.mu.Unlock()
						}()
					})
				}
			},
		}
		client = NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:  srv.URL,
			ProxyToken: "tok",
			KernelID:   "k-post-dial",
			SessionID:  "s-post-dial",
			HTTPClient: &http.Client{Transport: rt},
		})

		err := client.Start(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Start() error = %v, want context.Canceled (not ErrKernelAborted)", err)
		}
		if errors.Is(err, ErrKernelAborted) {
			t.Fatalf("Start() must NOT return ErrKernelAborted on caller context.Canceled")
		}
		if client.IsConnected() {
			t.Fatalf("expected IsConnected() == false after post-dial context cancel")
		}
	})

	t.Run("context_deadline_exceeded_after_dial", func(t *testing.T) {
		srv := newWSServer(t)
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()

		var client *ColabOneKernelClient
		var hookOnce sync.Once
		rt := &hookRoundTripper{
			base: srv.Client().Transport,
			hook: func(req *http.Request, resp *http.Response, err error) {
				if err == nil && resp != nil && resp.StatusCode == http.StatusSwitchingProtocols {
					hookOnce.Do(func() {
						client.mu.Lock()
						go func() {
							<-ctx.Done()
							client.mu.Unlock()
						}()
					})
				}
			},
		}
		client = NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:  srv.URL,
			ProxyToken: "tok",
			KernelID:   "k-post-dial",
			SessionID:  "s-post-dial",
			HTTPClient: &http.Client{Transport: rt},
		})

		err := client.Start(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Start() error = %v, want context.DeadlineExceeded (not ErrKernelAborted)", err)
		}
		if errors.Is(err, ErrKernelAborted) {
			t.Fatalf("Start() must NOT return ErrKernelAborted on caller context.DeadlineExceeded")
		}
		if client.IsConnected() {
			t.Fatalf("expected IsConnected() == false after post-dial deadline")
		}
	})

	t.Run("abort_after_dial_still_returns_err_kernel_aborted", func(t *testing.T) {
		srv := newWSServer(t)

		var client *ColabOneKernelClient
		var hookOnce sync.Once
		rt := &hookRoundTripper{
			base: srv.Client().Transport,
			hook: func(req *http.Request, resp *http.Response, err error) {
				if err == nil && resp != nil && resp.StatusCode == http.StatusSwitchingProtocols {
					hookOnce.Do(func() {
						client.mu.Lock()
						go func() {
							time.Sleep(15 * time.Millisecond)
							client.aborted = true
							client.mu.Unlock()
						}()
					})
				}
			},
		}
		client = NewColabOneKernelClient(ColabOneKernelOptions{
			ServerURL:  srv.URL,
			ProxyToken: "tok",
			KernelID:   "k-post-dial",
			SessionID:  "s-post-dial",
			HTTPClient: &http.Client{Transport: rt},
		})

		err := client.Start(context.Background())
		if !errors.Is(err, ErrKernelAborted) {
			t.Fatalf("Start() error = %v, want ErrKernelAborted", err)
		}
		if client.IsConnected() {
			t.Fatalf("expected IsConnected() == false after post-dial abort")
		}
	})
}
