// Package jupyterws provides a pure Go Jupyter Server Kernel REST and
// WebSocket client tailored for Google Colab runtime proxies.
package jupyterws

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// COLABONE_DEFAULT_PING_INTERVAL is the default WebSocket keepalive ping interval.
	COLABONE_DEFAULT_PING_INTERVAL = 15 * time.Second
	// COLABONE_DEFAULT_CONNECT_TIMEOUT is the default per-attempt HTTP/WebSocket connect timeout.
	COLABONE_DEFAULT_CONNECT_TIMEOUT = 25 * time.Second
	// COLABONE_DEFAULT_READ_LIMIT is the maximum WebSocket message size (64 MiB),
	// well above coder/websocket's 32 KiB default so large outputs and snapshots never fail.
	COLABONE_DEFAULT_READ_LIMIT int64 = 64 * 1024 * 1024
	// COLABONE_CLIENT_AGENT is the client agent header sent to Colab runtime proxies.
	COLABONE_CLIENT_AGENT = "colab-cli"
	// COLABONE_KERNEL_EXEC_TIMEOUT_MSG is the exact user-facing timeout message matched by frontend i18n.
	COLABONE_KERNEL_EXEC_TIMEOUT_MSG = "Colab 远端内核执行超时"
	// COLABONE_WS_CONNECT_TIMEOUT_MSG is returned when WebSocket dial times out across retries.
	COLABONE_WS_CONNECT_TIMEOUT_MSG = "Colab WebSocket connection timed out"

	colabOneDefaultRetryBaseDelay = 2 * time.Second
	colabOneDefaultMaxRetries     = 3
	colabOneWriteTimeout          = 15 * time.Second
	colabOneShutdownTimeout       = 10 * time.Second
	colabOneExecBufferSize        = 512
)

type colabOneTimeoutError struct{}

func (colabOneTimeoutError) Error() string { return COLABONE_KERNEL_EXEC_TIMEOUT_MSG }
func (colabOneTimeoutError) Timeout() bool { return true }
func (colabOneTimeoutError) Unwrap() error { return context.DeadlineExceeded }

type colabOneWSConnectTimeoutError struct{}

func (colabOneWSConnectTimeoutError) Error() string { return COLABONE_WS_CONNECT_TIMEOUT_MSG }
func (colabOneWSConnectTimeoutError) Timeout() bool { return true }
func (colabOneWSConnectTimeoutError) Unwrap() error { return context.DeadlineExceeded }

var (
	// ErrKernelTimeout is returned when ExecuteCode exceeds its deadline or timeout.
	// Its error text matches COLABONE_KERNEL_EXEC_TIMEOUT_MSG exactly for i18n parity.
	ErrKernelTimeout error = colabOneTimeoutError{}
	// ErrWSConnectTimeout is returned when establishing the Jupyter WebSocket times out.
	ErrWSConnectTimeout error = colabOneWSConnectTimeoutError{}
	// ErrKernelAborted is returned when an in-flight execution is aborted via Abort().
	ErrKernelAborted = errors.New("Colab WebSocket connection aborted")
	// ErrKernelDisconnected is returned when the WebSocket connection drops unexpectedly.
	ErrKernelDisconnected = errors.New("Colab WebSocket connection was lost")
)

// ColabOneHTTPError represents a non-2xx HTTP response from the Jupyter kernel proxy.
type ColabOneHTTPError struct {
	StatusCode int
	Method     string
	URL        string
	Body       string
}

func (e *ColabOneHTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.Body != "" {
		return fmt.Sprintf("Jupyter kernel HTTP %d (%s %s): %s", e.StatusCode, e.Method, e.URL, e.Body)
	}
	return fmt.Sprintf("Jupyter kernel HTTP %d (%s %s)", e.StatusCode, e.Method, e.URL)
}

// ColabOneKernelOutput represents a normalized Jupyter nbformat execution output.
type ColabOneKernelOutput struct {
	OutputType string         `json:"output_type"`
	Name       string         `json:"name,omitempty"`
	Text       string         `json:"text,omitempty"`
	Ename      string         `json:"ename,omitempty"`
	Evalue     string         `json:"evalue,omitempty"`
	Traceback  []string       `json:"traceback,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// ColabOneWSMessage represents a Jupyter WebSocket wire message.
type ColabOneWSMessage struct {
	Header       map[string]any `json:"header"`
	ParentHeader map[string]any `json:"parent_header"`
	Metadata     map[string]any `json:"metadata"`
	Content      map[string]any `json:"content"`
	Channel      string         `json:"channel,omitempty"`
	MsgType      string         `json:"msg_type,omitempty"`
	MsgID        string         `json:"msg_id,omitempty"`
	Buffers      [][]byte       `json:"-"`
}

// ColabOneRequestHook intercepts `msg_type == "colab_request"` messages arriving on the
// kernel WebSocket (such as Colab DriveFS `dfs_ephemeral` authorization requests).
// Returning true intercepts the message so it is not dispatched further.
type ColabOneRequestHook func(msg *ColabOneWSMessage, sendInputReply func(parentHeader map[string]any, value any) error) bool

// ColabOneKernelOptions configures a ColabOneKernelClient.
type ColabOneKernelOptions struct {
	ServerURL           string
	ProxyToken          string
	KernelID            string
	SessionID           string
	PingInterval        time.Duration
	ConnectTimeout      time.Duration
	ShutdownOnWSTimeout bool
	OnKernelStarted     func(string)
	OnSessionStarted    func(string)
	RequestHook         ColabOneRequestHook
	HTTPClient          *http.Client
	ReadLimit           int64
	RetryBaseDelay      time.Duration
	MaxConnectRetries   int
}

// String redacts ProxyToken so logging ColabOneKernelOptions never leaks credentials.
func (o ColabOneKernelOptions) String() string {
	proxyToken := ""
	if o.ProxyToken != "" {
		proxyToken = "[redacted]"
	}
	return fmt.Sprintf(
		"ColabOneKernelOptions{ServerURL:%q, ProxyToken:%q, KernelID:%q, SessionID:%q, PingInterval:%s, ConnectTimeout:%s, ShutdownOnWSTimeout:%t, ReadLimit:%d, RetryBaseDelay:%s, MaxConnectRetries:%d, HasHTTPClient:%t}",
		redactProxyTokenInString(o.ServerURL),
		proxyToken,
		o.KernelID,
		o.SessionID,
		o.PingInterval,
		o.ConnectTimeout,
		o.ShutdownOnWSTimeout,
		o.ReadLimit,
		o.RetryBaseDelay,
		o.MaxConnectRetries,
		o.HTTPClient != nil,
	)
}

// GoString redacts ProxyToken when formatted with %#v.
func (o ColabOneKernelOptions) GoString() string {
	return o.String()
}

// ColabOneExecuteOptions configures a single ExecuteCode invocation.
type ColabOneExecuteOptions struct {
	AllowStdin   bool
	Silent       bool
	StoreHistory bool
	Timeout      time.Duration
	OutputHook   func(ColabOneKernelOutput)
	StdinHook    func(prompt string, password bool) (string, error)
	RequestHook  ColabOneRequestHook
}

type wsConnection struct {
	conn         *websocket.Conn
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	wg           sync.WaitGroup
	writeMu      sync.Mutex
	errMu        sync.Mutex
	closeErr     error
	closed       bool
	closeOnce    sync.Once
	writeTimeout time.Duration
}

func newWSConnection(conn *websocket.Conn) *wsConnection {
	ctx, cancel := context.WithCancel(context.Background())
	return &wsConnection{
		conn:         conn,
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		writeTimeout: colabOneWriteTimeout,
	}
}

func (ws *wsConnection) close(err error) {
	ws.closeOnce.Do(func() {
		ws.errMu.Lock()
		ws.closed = true
		if err != nil {
			ws.closeErr = err
		}
		ws.errMu.Unlock()
		ws.cancel()
		if ws.conn != nil {
			_ = ws.conn.CloseNow()
		}
	})
}

func (ws *wsConnection) isClosed() bool {
	ws.errMu.Lock()
	defer ws.errMu.Unlock()
	return ws.closed
}

func (ws *wsConnection) err() error {
	ws.errMu.Lock()
	defer ws.errMu.Unlock()
	return ws.closeErr
}

func (ws *wsConnection) closedErr() error {
	closeErr := ws.err()
	if closeErr == nil {
		return ErrKernelDisconnected
	}
	if errors.Is(closeErr, ErrKernelAborted) {
		return ErrKernelAborted
	}
	if errors.Is(closeErr, ErrKernelDisconnected) {
		return closeErr
	}
	return fmt.Errorf("%w: %v", ErrKernelDisconnected, closeErr)
}

func (ws *wsConnection) writeJSON(parentCtx context.Context, msg *ColabOneWSMessage) error {
	if ws.isClosed() {
		return ws.closedErr()
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal jupyter message: %w", err)
	}

	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()

	if ws.isClosed() {
		return ws.closedErr()
	}
	if parentCtx != nil && parentCtx != ws.ctx && parentCtx.Err() != nil {
		return parentCtx.Err()
	}

	writeTimeout := ws.writeTimeout
	if writeTimeout <= 0 {
		writeTimeout = colabOneWriteTimeout
	}
	writeCtx, cancel := context.WithTimeout(ws.ctx, writeTimeout)
	defer cancel()
	if parentCtx != nil && parentCtx != ws.ctx {
		stop := context.AfterFunc(parentCtx, cancel)
		defer stop()
	}

	if err := ws.conn.Write(writeCtx, websocket.MessageText, payload); err != nil {
		if errors.Is(ws.err(), ErrKernelAborted) {
			return ErrKernelAborted
		}
		if parentCtx != nil && parentCtx != ws.ctx && parentCtx.Err() != nil {
			ws.close(ErrKernelDisconnected)
			return parentCtx.Err()
		}
		if errors.Is(writeCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			ws.close(ErrKernelDisconnected)
			return fmt.Errorf("%w: %v", ErrKernelDisconnected, err)
		}
		if ws.isClosed() {
			return ws.closedErr()
		}
		ws.close(err)
		return fmt.Errorf("%w: %v", ErrKernelDisconnected, err)
	}
	return nil
}

type pendingExecution struct {
	msgID       string
	msgCh       chan *ColabOneWSMessage
	done        chan struct{}
	requestHook ColabOneRequestHook
}

// ColabOneKernelClient manages a Jupyter Server Kernel REST + WebSocket connection.
type ColabOneKernelClient struct {
	opts              ColabOneKernelOptions
	serverURL         string
	proxyToken        string
	pingInterval      time.Duration
	connectTimeout    time.Duration
	writeTimeout      time.Duration
	readLimit         int64
	retryBaseDelay    time.Duration
	maxConnectRetries int
	httpClient        *http.Client

	startMu sync.Mutex

	mu               sync.Mutex
	kernelID         string
	notifiedKernelID string
	sessionID        string
	notifiedSession  bool
	requestHook      ColabOneRequestHook
	conn             *wsConnection
	trackedConns     []*wsConnection
	dialCancel       context.CancelFunc
	startedOnce      bool
	aborted          bool
	stopped          bool
	stoppingCount    int

	pendingMu sync.RWMutex
	pending   map[string]*pendingExecution
}

// NewColabOneKernelClient constructs a new kernel client with safe defaults.
func NewColabOneKernelClient(opts ColabOneKernelOptions) *ColabOneKernelClient {
	pingInterval := opts.PingInterval
	if pingInterval == 0 {
		pingInterval = COLABONE_DEFAULT_PING_INTERVAL
	}
	connectTimeout := opts.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = COLABONE_DEFAULT_CONNECT_TIMEOUT
	}
	readLimit := opts.ReadLimit
	if readLimit <= 0 {
		readLimit = COLABONE_DEFAULT_READ_LIMIT
	}
	retryBase := opts.RetryBaseDelay
	if retryBase <= 0 {
		retryBase = colabOneDefaultRetryBaseDelay
	}
	maxRetries := opts.MaxConnectRetries
	if maxRetries <= 0 {
		maxRetries = colabOneDefaultMaxRetries
	}
	var httpClient *http.Client
	if opts.HTTPClient == nil {
		httpClient = &http.Client{
			CheckRedirect: colabOneNoRedirect,
		}
	} else {
		cloned := *opts.HTTPClient
		if cloned.CheckRedirect == nil {
			cloned.CheckRedirect = colabOneNoRedirect
		}
		httpClient = &cloned
	}

	return &ColabOneKernelClient{
		opts:              opts,
		serverURL:         strings.TrimRight(strings.TrimSpace(opts.ServerURL), "/"),
		proxyToken:        strings.TrimSpace(opts.ProxyToken),
		pingInterval:      pingInterval,
		connectTimeout:    connectTimeout,
		writeTimeout:      colabOneWriteTimeout,
		readLimit:         readLimit,
		retryBaseDelay:    retryBase,
		maxConnectRetries: maxRetries,
		httpClient:        httpClient,
		kernelID:          strings.TrimSpace(opts.KernelID),
		notifiedKernelID:  strings.TrimSpace(opts.KernelID),
		sessionID:         strings.TrimSpace(opts.SessionID),
		notifiedSession:   strings.TrimSpace(opts.SessionID) != "",
		requestHook:       opts.RequestHook,
		pending:           make(map[string]*pendingExecution),
	}
}

// KernelID returns the currently associated remote kernel ID, if any.
func (c *ColabOneKernelClient) KernelID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.kernelID
}

// SessionID returns the currently associated Jupyter session ID, if any.
func (c *ColabOneKernelClient) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

// IsConnected reports whether the kernel WebSocket connection is currently active.
func (c *ColabOneKernelClient) IsConnected() bool {
	c.mu.Lock()
	ws := c.conn
	c.mu.Unlock()
	return ws != nil && !ws.isClosed()
}

// SetRequestHook updates the client-level colab_request interceptor safely.
func (c *ColabOneKernelClient) SetRequestHook(hook ColabOneRequestHook) {
	c.mu.Lock()
	c.requestHook = hook
	c.mu.Unlock()
}

// Start creates a remote kernel (if KernelID is empty), ensures a SessionID,
// and dials the Jupyter channels WebSocket with bounded retries.
func (c *ColabOneKernelClient) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.serverURL == "" {
		return errors.New("Colab kernel server URL is empty")
	}

	c.startMu.Lock()
	defer c.startMu.Unlock()

	if c.IsConnected() {
		return nil
	}

	// Reap any previously aborted/closed background connections before dialing fresh.
	c.reapClosedConns()

	startCtx, startCancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.stoppingCount > 0 || c.aborted {
		c.mu.Unlock()
		startCancel()
		return ErrKernelAborted
	}
	c.dialCancel = startCancel
	c.aborted = false
	c.stopped = false
	curKernelID := c.kernelID
	curSessionID := c.sessionID
	c.mu.Unlock()

	createdKernelID := ""
	connected := false
	defer func() {
		startCancel()
		c.mu.Lock()
		c.dialCancel = nil
		c.mu.Unlock()
		if !connected && createdKernelID != "" && c.opts.ShutdownOnWSTimeout {
			c.bestEffortShutdownKernel(createdKernelID)
		}
	}()

	var lastErr error
	recoveredStaleKernel := false

	for attempt := 0; attempt < c.maxConnectRetries; attempt++ {
		if err := startCtx.Err(); err != nil {
			if c.wasAbortedOrStopped() {
				return ErrKernelAborted
			}
			return err
		}

		if curKernelID == "" {
			createdID, err := c.createKernelHTTP(startCtx)
			if err != nil {
				if startCtx.Err() != nil {
					if c.wasAbortedOrStopped() {
						return ErrKernelAborted
					}
					return startCtx.Err()
				}
				lastErr = err
				if isRetryableConnectError(err) && attempt < c.maxConnectRetries-1 {
					if sleepErr := sleepWithContext(startCtx, c.retryDelay(attempt)); sleepErr != nil {
						if c.wasAbortedOrStopped() {
							return ErrKernelAborted
						}
						return sleepErr
					}
					continue
				}
				return err
			}
			curKernelID = createdID
			createdKernelID = createdID
			if !c.opts.ShutdownOnWSTimeout {
				c.recordKernelID(curKernelID)
			}
		}

		if curSessionID == "" {
			curSessionID = colabOneNewUUID()
		}

		wsURL, err := c.buildWSURL(curKernelID, curSessionID)
		if err != nil {
			return err
		}

		dialCtx, dialCancel := context.WithTimeout(startCtx, c.connectTimeout)
		dialOpts := &websocket.DialOptions{
			HTTPClient: c.httpClient,
			HTTPHeader: c.wsHeaders(),
		}
		conn, resp, dialErr := websocket.Dial(dialCtx, wsURL, dialOpts)
		dialCancel()

		if dialErr != nil {
			dialErr = sanitizeURLError(dialErr)
			if startCtx.Err() != nil {
				if c.wasAbortedOrStopped() {
					return ErrKernelAborted
				}
				return startCtx.Err()
			}

			// Handle stale pre-supplied kernel ID (404) by creating a fresh kernel once.
			if resp != nil && resp.StatusCode == http.StatusNotFound && createdKernelID == "" && !recoveredStaleKernel {
				recoveredStaleKernel = true
				curKernelID = ""
				c.clearKernelID()
				attempt-- // do not consume retry budget for stale kernel recovery
				continue
			}

			// Check for WebSocket dial timeout (per-attempt deadline exceeded).
			if isTimeoutError(dialErr, dialCtx) {
				if c.opts.ShutdownOnWSTimeout {
					if curKernelID != "" {
						c.bestEffortShutdownKernel(curKernelID)
						if createdKernelID == curKernelID {
							createdKernelID = ""
						}
						curKernelID = ""
						c.clearKernelID()
					}
				} else if curKernelID != "" {
					// Reuse already-created kernel on retry instead of leaking duplicates.
					c.recordKernelID(curKernelID)
				}
				lastErr = ErrWSConnectTimeout
				if attempt < c.maxConnectRetries-1 {
					if sleepErr := sleepWithContext(startCtx, c.retryDelay(attempt)); sleepErr != nil {
						if c.wasAbortedOrStopped() {
							return ErrKernelAborted
						}
						return sleepErr
					}
					continue
				}
				return ErrWSConnectTimeout
			}

			if resp != nil {
				httpErr := &ColabOneHTTPError{
					StatusCode: resp.StatusCode,
					Method:     http.MethodGet,
					URL:        sanitizeURLForError(wsURL),
					Body:       readBodySnippet(resp.Body),
				}
				lastErr = httpErr
				if isRetryableHTTPStatus(resp.StatusCode) && attempt < c.maxConnectRetries-1 {
					if sleepErr := sleepWithContext(startCtx, c.retryDelay(attempt)); sleepErr != nil {
						if c.wasAbortedOrStopped() {
							return ErrKernelAborted
						}
						return sleepErr
					}
					continue
				}
				return httpErr
			}

			lastErr = dialErr
			if isRetryableConnectError(dialErr) && attempt < c.maxConnectRetries-1 {
				if sleepErr := sleepWithContext(startCtx, c.retryDelay(attempt)); sleepErr != nil {
					if c.wasAbortedOrStopped() {
						return ErrKernelAborted
					}
					return sleepErr
				}
				continue
			}
			return dialErr
		}

		// Configure large read limit before starting reader goroutine.
		conn.SetReadLimit(c.readLimit)

		ws := newWSConnection(conn)
		if c.writeTimeout > 0 {
			ws.writeTimeout = c.writeTimeout
		}
		ws.wg.Add(1)
		startPing := c.pingInterval > 0
		if startPing {
			ws.wg.Add(1)
		}

		c.mu.Lock()
		abortedOrStopped := c.aborted || c.stopped
		if startCtx.Err() != nil || abortedOrStopped {
			c.mu.Unlock()
			closeErr := startCtx.Err()
			if abortedOrStopped {
				closeErr = ErrKernelAborted
			}
			ws.close(closeErr)
			return closeErr
		}
		connected = true
		c.conn = ws
		c.trackedConns = append(c.trackedConns, ws)
		c.startedOnce = true

		notifyKernel := ""
		if curKernelID != "" {
			c.kernelID = curKernelID
			if curKernelID != c.notifiedKernelID {
				c.notifiedKernelID = curKernelID
				notifyKernel = curKernelID
			}
		}

		notifySession := ""
		if curSessionID != "" {
			c.sessionID = curSessionID
			if !c.notifiedSession {
				c.notifiedSession = true
				notifySession = curSessionID
			}
		}
		c.mu.Unlock()

		go c.readerLoop(ws)
		if startPing {
			go c.pingLoop(ws)
		}

		if notifyKernel != "" && c.opts.OnKernelStarted != nil {
			c.opts.OnKernelStarted(notifyKernel)
		}
		if notifySession != "" && c.opts.OnSessionStarted != nil {
			c.opts.OnSessionStarted(notifySession)
		}
		return nil
	}

	if lastErr != nil {
		return lastErr
	}
	return ErrWSConnectTimeout
}

// ExecuteCode sends a Jupyter `execute_request` on the "shell" channel and collects
// streamed IOPub outputs until both `execute_reply` and `status: idle` arrive,
// or until ctx / opts.Timeout / Abort interrupts execution.
func (c *ColabOneKernelClient) ExecuteCode(ctx context.Context, code string, opts ColabOneExecuteOptions) ([]ColabOneKernelOutput, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	execCtx := ctx
	var execCancel context.CancelFunc
	if opts.Timeout > 0 {
		execCtx, execCancel = context.WithTimeout(ctx, opts.Timeout)
		defer execCancel()
	}

	ws, err := c.ensureConnectedForExec(execCtx)
	if err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) && (opts.Timeout > 0 || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			return nil, ErrKernelTimeout
		}
		return nil, err
	}

	storeHistory := opts.StoreHistory && !opts.Silent
	reqMsg := c.buildMessage(
		"shell",
		"execute_request",
		nil,
		nil,
		map[string]any{
			"code":             code,
			"silent":           opts.Silent,
			"store_history":    storeHistory,
			"user_expressions": map[string]any{},
			"allow_stdin":      opts.AllowStdin,
			"stop_on_error":    true,
		},
	)

	exec := &pendingExecution{
		msgID:       reqMsg.MsgID,
		msgCh:       make(chan *ColabOneWSMessage, colabOneExecBufferSize),
		done:        make(chan struct{}),
		requestHook: opts.RequestHook,
	}

	c.registerPending(exec)
	defer func() {
		c.unregisterPending(exec.msgID)
		close(exec.done)
	}()

	if err := ws.writeJSON(execCtx, reqMsg); err != nil {
		if errors.Is(err, ErrKernelAborted) {
			return nil, ErrKernelAborted
		}
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) && (opts.Timeout > 0 || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			return nil, ErrKernelTimeout
		}
		if errors.Is(err, context.DeadlineExceeded) {
			ws.close(ErrKernelDisconnected)
			return nil, fmt.Errorf("%w: %v", ErrKernelDisconnected, err)
		}
		return nil, err
	}

	outputs := make([]ColabOneKernelOutput, 0)
	gotIdle := false
	gotReply := false
	var replyContent map[string]any

	processMsg := func(msg *ColabOneWSMessage) (finished bool, execErr error) {
		switch msg.MsgType {
		case "stream":
			if msg.Channel == "" || msg.Channel == "iopub" {
				out := ColabOneKernelOutput{
					OutputType: "stream",
					Name:       stringFromMap(msg.Content, "name"),
					Text:       stringOrJoinedFromAny(msg.Content["text"]),
				}
				outputs = append(outputs, out)
				if opts.OutputHook != nil {
					opts.OutputHook(out)
				}
			}
		case "execute_result", "display_data":
			if msg.Channel == "" || msg.Channel == "iopub" {
				out := ColabOneKernelOutput{
					OutputType: msg.MsgType,
					Data:       mapFromAny(msg.Content["data"]),
					Metadata:   mapFromAny(msg.Content["metadata"]),
				}
				outputs = append(outputs, out)
				if opts.OutputHook != nil {
					opts.OutputHook(out)
				}
			}
		case "error":
			if msg.Channel == "" || msg.Channel == "iopub" {
				out := ColabOneKernelOutput{
					OutputType: "error",
					Ename:      stringFromMap(msg.Content, "ename"),
					Evalue:     stringFromMap(msg.Content, "evalue"),
					Traceback:  stringSliceFromAny(msg.Content["traceback"]),
				}
				outputs = append(outputs, out)
				if opts.OutputHook != nil {
					opts.OutputHook(out)
				}
			}
		case "clear_output":
			if msg.Channel == "" || msg.Channel == "iopub" {
				outputs = outputs[:0]
			}
		case "status":
			if (msg.Channel == "" || msg.Channel == "iopub") && stringFromMap(msg.Content, "execution_state") == "idle" {
				gotIdle = true
			}
		case "execute_reply":
			if msg.Channel == "" || msg.Channel == "shell" {
				gotReply = true
				replyContent = msg.Content
			}
		case "input_request":
			if opts.AllowStdin && (msg.Channel == "" || msg.Channel == "stdin") {
				prompt := stringFromMap(msg.Content, "prompt")
				password, _ := msg.Content["password"].(bool)
				replyVal := ""
				if opts.StdinHook != nil {
					v, stdinErr := opts.StdinHook(prompt, password)
					if stdinErr != nil {
						return true, stdinErr
					}
					replyVal = v
				}
				if sendErr := c.sendInputReplyOnConn(ws, msg.Header, replyVal); sendErr != nil {
					return true, sendErr
				}
			}
		}
		return gotIdle && gotReply, nil
	}

	finalize := func() ([]ColabOneKernelOutput, error) {
		status := stringFromMap(replyContent, "status")
		if status == "error" {
			hasErrOutput := false
			for _, out := range outputs {
				if out.OutputType == "error" {
					hasErrOutput = true
					break
				}
			}
			if !hasErrOutput {
				ename := stringFromMap(replyContent, "ename")
				if ename == "" {
					ename = "Error"
				}
				evalue := stringFromMap(replyContent, "evalue")
				if evalue == "" {
					evalue = "Unknown error"
				}
				tb := stringSliceFromAny(replyContent["traceback"])
				if tb == nil {
					tb = []string{}
				}
				outputs = append(outputs, ColabOneKernelOutput{
					OutputType: "error",
					Ename:      ename,
					Evalue:     evalue,
					Traceback:  tb,
				})
			}
		} else if status == "abort" {
			return outputs, ErrKernelAborted
		}
		return outputs, nil
	}

	for {
		select {
		case msg := <-exec.msgCh:
			finished, procErr := processMsg(msg)
			if procErr != nil {
				return outputs, procErr
			}
			if finished {
				return finalize()
			}
		case <-execCtx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return outputs, context.Canceled
			}
			return outputs, ErrKernelTimeout
		case <-ws.done:
			// Drain any already-buffered frames before reporting disconnect so
			// a server that closes right after sending status:idle + execute_reply
			// still completes cleanly.
			for {
				select {
				case msg := <-exec.msgCh:
					finished, procErr := processMsg(msg)
					if procErr != nil {
						return outputs, procErr
					}
					if finished {
						return finalize()
					}
				default:
					return outputs, ws.closedErr()
				}
			}
		}
	}
}

// SendInputReply sends a Jupyter `input_reply` message on the "stdin" channel using the active connection.
func (c *ColabOneKernelClient) SendInputReply(parentHeader map[string]any, value any) error {
	c.mu.Lock()
	ws := c.conn
	c.mu.Unlock()
	if ws == nil || ws.isClosed() {
		return ErrKernelDisconnected
	}
	return c.sendInputReplyOnConn(ws, parentHeader, value)
}

// Abort immediately force-closes the active WebSocket connection (and cancels any
// in-progress dial) to unblock active ExecuteCode or Start calls cleanly.
func (c *ColabOneKernelClient) Abort() {
	c.mu.Lock()
	c.aborted = true
	dialCancel := c.dialCancel
	ws := c.conn
	c.conn = nil
	c.mu.Unlock()

	if dialCancel != nil {
		dialCancel()
	}
	if ws != nil {
		ws.close(ErrKernelAborted)
	}
}

// Stop closes the WebSocket connection, waits for all background goroutines to exit,
// and optionally deletes the remote kernel via HTTP DELETE when shutdownKernel is true.
func (c *ColabOneKernelClient) Stop(ctx context.Context, shutdownKernel bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	c.stopped = true
	c.stoppingCount++
	dialCancel := c.dialCancel
	active := c.conn
	c.conn = nil
	c.mu.Unlock()

	if dialCancel != nil {
		dialCancel()
	}
	if active != nil {
		active.close(ErrKernelDisconnected)
	}

	// Wait for any in-flight Start() attempt to finish unwinding now that dialCancel fired.
	c.startMu.Lock()
	c.mu.Lock()
	c.stoppingCount--
	c.aborted = false
	if c.conn != nil {
		c.conn.close(ErrKernelDisconnected)
		c.conn = nil
	}
	tracked := c.trackedConns
	c.trackedConns = nil
	kernelID := c.kernelID
	c.mu.Unlock()
	c.startMu.Unlock()

	for _, ws := range tracked {
		ws.close(ErrKernelDisconnected)
		ws.wg.Wait()
	}

	if shutdownKernel && kernelID != "" {
		err := c.shutdownKernelHTTP(ctx, kernelID)
		c.clearKernelID()
		return err
	}
	return nil
}

func (c *ColabOneKernelClient) ensureConnectedForExec(ctx context.Context) (*wsConnection, error) {
	c.mu.Lock()
	ws := c.conn
	startedOnce := c.startedOnce
	aborted := c.aborted
	stopped := c.stopped
	c.mu.Unlock()

	if ws != nil && !ws.isClosed() {
		return ws, nil
	}
	if aborted {
		return nil, ErrKernelAborted
	}
	if stopped || startedOnce {
		return nil, ErrKernelDisconnected
	}
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	ws = c.conn
	c.mu.Unlock()
	if ws == nil || ws.isClosed() {
		return nil, ErrKernelDisconnected
	}
	return ws, nil
}

func (c *ColabOneKernelClient) readerLoop(ws *wsConnection) {
	defer ws.wg.Done()
	defer close(ws.done)
	defer func() {
		c.mu.Lock()
		if c.conn == ws {
			c.conn = nil
		}
		c.mu.Unlock()
	}()

	for {
		msgType, data, err := ws.conn.Read(ws.ctx)
		if err != nil {
			ws.close(err)
			return
		}

		msg, parseErr := deserializeWSMessage(msgType, data)
		if parseErr != nil {
			continue
		}

		if msg.MsgType == "colab_request" {
			hook := c.resolveRequestHook(msg)
			if hook != nil {
				sendReply := func(parentHeader map[string]any, value any) error {
					return c.sendInputReplyOnConn(ws, parentHeader, value)
				}
				if hook(msg, sendReply) {
					continue
				}
			}
		}

		parentID := stringFromMap(msg.ParentHeader, "msg_id")
		if parentID == "" {
			continue
		}

		exec := c.lookupPending(parentID)
		if exec == nil {
			continue
		}

		select {
		case exec.msgCh <- msg:
		case <-exec.done:
		case <-ws.ctx.Done():
			return
		}
	}
}

func (c *ColabOneKernelClient) pingLoop(ws *wsConnection) {
	defer ws.wg.Done()

	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()

	pingTimeout := c.pingInterval
	if pingTimeout < 5*time.Second {
		pingTimeout = 5 * time.Second
	}
	if pingTimeout > 15*time.Second {
		pingTimeout = 15 * time.Second
	}

	for {
		select {
		case <-ws.ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ws.ctx, pingTimeout)
			err := ws.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				if ws.ctx.Err() == nil {
					ws.close(fmt.Errorf("Colab WebSocket ping failed: %w", err))
				}
				return
			}
		}
	}
}

func (c *ColabOneKernelClient) sendInputReplyOnConn(ws *wsConnection, parentHeader map[string]any, value any) error {
	if ws == nil || ws.isClosed() {
		return ErrKernelDisconnected
	}
	msg := c.buildMessage(
		"stdin",
		"input_reply",
		parentHeader,
		nil,
		map[string]any{
			"value": value,
		},
	)
	return ws.writeJSON(ws.ctx, msg)
}

func (c *ColabOneKernelClient) buildMessage(channel, msgType string, parentHeader, metadata, content map[string]any) *ColabOneWSMessage {
	msgID := colabOneNewUUID()
	sessionID := c.SessionID()
	if parentHeader == nil {
		parentHeader = map[string]any{}
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	if content == nil {
		content = map[string]any{}
	}
	return &ColabOneWSMessage{
		Header: map[string]any{
			"msg_id":   msgID,
			"msg_type": msgType,
			"username": "colabone",
			"session":  sessionID,
			"date":     time.Now().UTC().Format("2006-01-02T15:04:05.000000Z"),
			"version":  "5.3",
		},
		ParentHeader: parentHeader,
		Metadata:     metadata,
		Content:      content,
		Channel:      channel,
		MsgType:      msgType,
		MsgID:        msgID,
	}
}

func (c *ColabOneKernelClient) resolveRequestHook(msg *ColabOneWSMessage) ColabOneRequestHook {
	parentID := stringFromMap(msg.ParentHeader, "msg_id")

	c.pendingMu.RLock()
	if parentID != "" {
		if exec := c.pending[parentID]; exec != nil && exec.requestHook != nil {
			hook := exec.requestHook
			c.pendingMu.RUnlock()
			return hook
		}
	}
	for _, exec := range c.pending {
		if exec.requestHook != nil {
			hook := exec.requestHook
			c.pendingMu.RUnlock()
			return hook
		}
	}
	c.pendingMu.RUnlock()

	c.mu.Lock()
	hook := c.requestHook
	c.mu.Unlock()
	return hook
}

func (c *ColabOneKernelClient) registerPending(exec *pendingExecution) {
	c.pendingMu.Lock()
	c.pending[exec.msgID] = exec
	c.pendingMu.Unlock()
}

func (c *ColabOneKernelClient) unregisterPending(msgID string) {
	c.pendingMu.Lock()
	delete(c.pending, msgID)
	c.pendingMu.Unlock()
}

func (c *ColabOneKernelClient) lookupPending(msgID string) *pendingExecution {
	c.pendingMu.RLock()
	exec := c.pending[msgID]
	c.pendingMu.RUnlock()
	return exec
}

func (c *ColabOneKernelClient) recordKernelID(id string) {
	if id == "" {
		return
	}
	var notify string
	c.mu.Lock()
	c.kernelID = id
	if id != c.notifiedKernelID {
		c.notifiedKernelID = id
		notify = id
	}
	c.mu.Unlock()
	if notify != "" && c.opts.OnKernelStarted != nil {
		c.opts.OnKernelStarted(notify)
	}
}

func (c *ColabOneKernelClient) clearKernelID() {
	c.mu.Lock()
	c.kernelID = ""
	c.notifiedKernelID = ""
	c.mu.Unlock()
}

func (c *ColabOneKernelClient) wasAbortedOrStopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.aborted || c.stopped
}

func (c *ColabOneKernelClient) reapClosedConns() {
	c.mu.Lock()
	var remaining []*wsConnection
	var closed []*wsConnection
	for _, ws := range c.trackedConns {
		if ws.isClosed() {
			closed = append(closed, ws)
		} else {
			remaining = append(remaining, ws)
		}
	}
	c.trackedConns = remaining
	c.mu.Unlock()

	for _, ws := range closed {
		ws.wg.Wait()
	}
}

func (c *ColabOneKernelClient) retryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 6 {
		attempt = 6
	}
	return c.retryBaseDelay * time.Duration(1<<uint(attempt))
}

func (c *ColabOneKernelClient) restHeaders(includeContentType bool) http.Header {
	h := http.Header{}
	h.Set("Accept", "application/json")
	h.Set("X-Colab-Client-Agent", COLABONE_CLIENT_AGENT)
	if includeContentType {
		h.Set("Content-Type", "application/json")
	}
	if c.proxyToken != "" {
		h.Set("X-Colab-Runtime-Proxy-Token", c.proxyToken)
		h.Set("Authorization", "Bearer "+c.proxyToken)
	}
	return h
}

func (c *ColabOneKernelClient) wsHeaders() http.Header {
	h := http.Header{}
	h.Set("X-Colab-Client-Agent", COLABONE_CLIENT_AGENT)
	if c.proxyToken != "" {
		h.Set("X-Colab-Runtime-Proxy-Token", c.proxyToken)
	}
	return h
}

func (c *ColabOneKernelClient) buildRESTURL(pathSuffix string) (string, error) {
	u, err := url.Parse(c.serverURL)
	if err != nil {
		return "", sanitizeURLError(fmt.Errorf("parse kernel server URL: %w", err))
	}
	u.Path = strings.TrimRight(u.Path, "/") + pathSuffix
	q := u.Query()
	if c.proxyToken != "" {
		q.Set("colab-runtime-proxy-token", c.proxyToken)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *ColabOneKernelClient) buildWSURL(kernelID, sessionID string) (string, error) {
	u, err := url.Parse(c.serverURL)
	if err != nil {
		return "", sanitizeURLError(fmt.Errorf("parse kernel server URL: %w", err))
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("unsupported kernel server URL scheme: %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/kernels/" + url.PathEscape(kernelID) + "/channels"
	q := u.Query()
	q.Set("session_id", sessionID)
	if c.proxyToken != "" {
		q.Set("colab-runtime-proxy-token", c.proxyToken)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *ColabOneKernelClient) createKernelHTTP(ctx context.Context) (string, error) {
	reqURL, err := c.buildRESTURL("/api/kernels")
	if err != nil {
		return "", err
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.connectTimeout)
	defer cancel()

	body := bytes.NewReader([]byte(`{"name":"python3"}`))
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, body)
	if err != nil {
		return "", sanitizeURLError(err)
	}
	req.Header = c.restHeaders(true)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", sanitizeURLError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &ColabOneHTTPError{
			StatusCode: resp.StatusCode,
			Method:     http.MethodPost,
			URL:        sanitizeURLForError(reqURL),
			Body:       readBodySnippet(resp.Body),
		}
	}

	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode create kernel response: %w", err)
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return "", errors.New("create kernel response missing id")
	}
	return strings.TrimSpace(parsed.ID), nil
}

func (c *ColabOneKernelClient) shutdownKernelHTTP(ctx context.Context, kernelID string) error {
	if strings.TrimSpace(kernelID) == "" || c.serverURL == "" {
		return nil
	}
	reqURL, err := c.buildRESTURL("/api/kernels/" + url.PathEscape(kernelID))
	if err != nil {
		return err
	}

	reqCtx := ctx
	if reqCtx == nil || reqCtx.Err() != nil {
		reqCtx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(reqCtx, colabOneShutdownTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return sanitizeURLError(err)
	}
	req.Header = c.restHeaders(false)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return sanitizeURLError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	// Ignore already-gone / transient gateway errors on shutdown (matches Jupyter manager semantics).
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone, http.StatusBadGateway, http.StatusServiceUnavailable:
		return nil
	default:
		return &ColabOneHTTPError{
			StatusCode: resp.StatusCode,
			Method:     http.MethodDelete,
			URL:        sanitizeURLForError(reqURL),
			Body:       readBodySnippet(resp.Body),
		}
	}
}

func (c *ColabOneKernelClient) bestEffortShutdownKernel(kernelID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.shutdownKernelHTTP(ctx, kernelID)
}

func deserializeWSMessage(msgType websocket.MessageType, data []byte) (*ColabOneWSMessage, error) {
	var jsonBytes []byte
	var buffers [][]byte

	switch msgType {
	case websocket.MessageText:
		jsonBytes = data
	case websocket.MessageBinary:
		if len(data) < 8 {
			return nil, errors.New("binary jupyter message too short")
		}
		nbufs := int(binary.BigEndian.Uint32(data[0:4]))
		if nbufs < 1 || len(data) < 4*(nbufs+1) {
			return nil, errors.New("invalid jupyter binary buffer count")
		}
		offsets := make([]int, nbufs)
		for i := 0; i < nbufs; i++ {
			start := 4 * (i + 1)
			offsets[i] = int(binary.BigEndian.Uint32(data[start : start+4]))
		}
		jsonStart := offsets[0]
		jsonStop := len(data)
		if nbufs >= 2 {
			jsonStop = offsets[1]
		}
		if jsonStart < 0 || jsonStop < jsonStart || jsonStop > len(data) {
			return nil, errors.New("invalid jupyter binary JSON offsets")
		}
		jsonBytes = data[jsonStart:jsonStop]
		if nbufs > 1 {
			buffers = make([][]byte, 0, nbufs-1)
			for i := 1; i < nbufs; i++ {
				bStart := offsets[i]
				bStop := len(data)
				if i+1 < nbufs {
					bStop = offsets[i+1]
				}
				if bStart < 0 || bStop < bStart || bStop > len(data) {
					return nil, errors.New("invalid jupyter binary chunk offsets")
				}
				chunk := make([]byte, bStop-bStart)
				copy(chunk, data[bStart:bStop])
				buffers = append(buffers, chunk)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported websocket message type: %v", msgType)
	}

	var msg ColabOneWSMessage
	if err := json.Unmarshal(jsonBytes, &msg); err != nil {
		return nil, err
	}
	if msg.Header == nil {
		msg.Header = map[string]any{}
	}
	if msg.ParentHeader == nil {
		msg.ParentHeader = map[string]any{}
	}
	if msg.Metadata == nil {
		msg.Metadata = map[string]any{}
	}
	if msg.Content == nil {
		msg.Content = map[string]any{}
	}
	if msg.MsgType == "" {
		msg.MsgType = stringFromMap(msg.Header, "msg_type")
	}
	if msg.MsgID == "" {
		msg.MsgID = stringFromMap(msg.Header, "msg_id")
	}
	msg.Buffers = buffers
	return &msg, nil
}

func colabOneNewUUID() string {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		// Fallback using timestamp entropy if crypto/rand ever fails.
		binary.BigEndian.PutUint64(b[0:8], uint64(time.Now().UnixNano()))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func isRetryableHTTPStatus(status int) bool {
	return status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

func isTimeoutError(err error, attemptCtx context.Context) bool {
	if err == nil {
		return false
	}
	if attemptCtx != nil && errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

func isRetryableConnectError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *ColabOneHTTPError
	if errors.As(err, &httpErr) {
		return isRetryableHTTPStatus(httpErr.StatusCode)
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	low := strings.ToLower(err.Error())
	for _, needle := range []string{
		"timed out",
		"connection reset",
		"connection refused",
		"broken pipe",
		"unexpected eof",
		"eof",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
	} {
		if strings.Contains(low, needle) {
			return true
		}
	}
	return false
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const (
	colabOneProxyTokenQueryKey = "colab-runtime-proxy-token"
	colabOneMaxSanitizeDepth   = 64
)

func colabOneNoRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

type sanitizedKernelError struct {
	msg string
	err error
}

func (e *sanitizedKernelError) Error() string { return e.msg }
func (e *sanitizedKernelError) Unwrap() error { return e.err }

type sanitizedMultiError struct {
	msg  string
	errs []error
}

func (e *sanitizedMultiError) Error() string   { return e.msg }
func (e *sanitizedMultiError) Unwrap() []error { return e.errs }

func sanitizeURLError(err error) error {
	sanitized, _ := sanitizeURLErrorWithChanged(err, 0)
	return sanitized
}

func sanitizeURLErrorWithChanged(err error, depth int) (error, bool) {
	if err == nil {
		return nil, false
	}
	if uerr, ok := err.(*url.Error); ok && uerr == nil {
		return nil, false
	}
	if depth >= colabOneMaxSanitizeDepth {
		rawMsg := err.Error()
		redactedMsg := redactProxyTokenInString(rawMsg)
		if redactedMsg != rawMsg {
			return &sanitizedKernelError{msg: redactedMsg}, true
		}
		return err, false
	}
	if uerr, ok := err.(*url.Error); ok {
		return sanitizeDirectURLError(uerr, depth+1)
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		errs := multi.Unwrap()
		changed := false
		sanitized := make([]error, len(errs))
		rawMsg := err.Error()
		redactedMsg := rawMsg
		for i, sub := range errs {
			s, subChanged := sanitizeURLErrorWithChanged(sub, depth+1)
			sanitized[i] = s
			if subChanged {
				changed = true
				if sub != nil && s != nil {
					if oldMsg := sub.Error(); oldMsg != "" {
						redactedMsg = strings.ReplaceAll(redactedMsg, oldMsg, s.Error())
					}
				}
			}
		}
		redactedMsg = redactProxyTokenInString(redactedMsg)
		if changed || redactedMsg != rawMsg {
			return &sanitizedMultiError{msg: redactedMsg, errs: sanitized}, true
		}
		return err, false
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		inner := single.Unwrap()
		sanitizedInner, innerChanged := sanitizeURLErrorWithChanged(inner, depth+1)
		rawMsg := err.Error()
		redactedMsg := rawMsg
		if innerChanged && inner != nil && sanitizedInner != nil {
			if oldMsg := inner.Error(); oldMsg != "" {
				redactedMsg = strings.ReplaceAll(redactedMsg, oldMsg, sanitizedInner.Error())
			}
		}
		redactedMsg = redactProxyTokenInString(redactedMsg)
		if innerChanged || redactedMsg != rawMsg {
			if sanitizedInner != nil && redactedMsg == sanitizedInner.Error() {
				return sanitizedInner, true
			}
			return &sanitizedKernelError{msg: redactedMsg, err: sanitizedInner}, true
		}
		return err, false
	}
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr != nil {
		sanitizedUErr, uChanged := sanitizeDirectURLError(uerr, depth+1)
		rawMsg := err.Error()
		redactedMsg := rawMsg
		if uChanged {
			if oldMsg := uerr.Error(); oldMsg != "" {
				redactedMsg = strings.ReplaceAll(redactedMsg, oldMsg, sanitizedUErr.Error())
			}
		}
		redactedMsg = redactProxyTokenInString(redactedMsg)
		if uChanged || redactedMsg != rawMsg {
			if redactedMsg == sanitizedUErr.Error() {
				return sanitizedUErr, true
			}
			return &sanitizedKernelError{msg: redactedMsg, err: sanitizedUErr}, true
		}
		return err, false
	}
	rawMsg := err.Error()
	redactedMsg := redactProxyTokenInString(rawMsg)
	if redactedMsg != rawMsg {
		return &sanitizedKernelError{msg: redactedMsg}, true
	}
	return err, false
}

func sanitizeDirectURLError(uerr *url.Error, depth int) (*url.Error, bool) {
	newOp := uerr.Op
	if containsProxyTokenParam(newOp) {
		newOp = redactProxyTokenInString(newOp)
	}
	newURL := uerr.URL
	if containsProxyTokenParam(newURL) {
		newURL = sanitizeURLForError(newURL)
	}
	newErr, errChanged := sanitizeURLErrorWithChanged(uerr.Err, depth)
	if !errChanged && newOp == uerr.Op && newURL == uerr.URL {
		return uerr, false
	}
	cloned := *uerr
	cloned.Op = newOp
	cloned.URL = newURL
	cloned.Err = newErr
	return &cloned, true
}

func sanitizeURLForError(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	cleaned := u.String()
	if containsProxyTokenParam(cleaned) {
		return redactProxyTokenInString(cleaned)
	}
	return cleaned
}

func containsProxyTokenParam(s string) bool {
	lower := asciiLower(s)
	return strings.Contains(lower, colabOneProxyTokenQueryKey+"=") ||
		strings.Contains(lower, colabOneProxyTokenQueryKey+"%3d")
}

func redactProxyTokenInString(s string) string {
	if !containsProxyTokenParam(s) {
		return s
	}
	lower := asciiLower(s)
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		idxEq := strings.Index(lower[i:], colabOneProxyTokenQueryKey+"=")
		idxEsc := strings.Index(lower[i:], colabOneProxyTokenQueryKey+"%3d")

		idx := -1
		prefixLen := 0
		escaped := false
		if idxEq >= 0 && (idxEsc < 0 || idxEq < idxEsc) {
			idx = i + idxEq
			prefixLen = len(colabOneProxyTokenQueryKey) + 1
		} else if idxEsc >= 0 {
			idx = i + idxEsc
			prefixLen = len(colabOneProxyTokenQueryKey) + 3
			escaped = true
		}
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}

		valStart := idx + prefixLen
		b.WriteString(s[i:valStart])
		b.WriteString("REDACTED")

		valEnd := valStart
		for valEnd < len(s) {
			ch := s[valEnd]
			if isProxyTokenValueDelimiter(ch) {
				break
			}
			if escaped && ch == '%' && valEnd+2 < len(s) && isEscapedDelimiter(lower[valEnd:valEnd+3]) {
				break
			}
			valEnd++
		}
		i = valEnd
	}
	return b.String()
}

func isProxyTokenValueDelimiter(c byte) bool {
	switch c {
	case '&', '#', '"', '\'', '\\', ' ', '\t', '\r', '\n', ')', ']', '}', '>', ',', ';':
		return true
	default:
		return false
	}
}

func isEscapedDelimiter(seq string) bool {
	switch seq {
	case "%26", "%23", "%20", "%22", "%27", "%09", "%0a", "%0d":
		return true
	default:
		return false
	}
}

func asciiLower(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if b.Len() == 0 {
				b.Grow(len(s))
				b.WriteString(s[:i])
			}
			b.WriteByte(c + ('a' - 'A'))
		} else if b.Len() > 0 {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return s
	}
	return b.String()
}

func readBodySnippet(r io.Reader) string {
	if r == nil {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	return redactProxyTokenInString(strings.TrimSpace(string(b)))
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func stringOrJoinedFromAny(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var b strings.Builder
		for _, item := range t {
			if item != nil {
				b.WriteString(fmt.Sprintf("%v", item))
			}
		}
		return b.String()
	case []string:
		return strings.Join(t, "")
	default:
		return fmt.Sprintf("%v", v)
	}
}

func stringSliceFromAny(v any) []string {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		out := make([]string, len(t))
		copy(out, t)
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if item == nil {
				continue
			}
			if s, ok := item.(string); ok {
				out = append(out, s)
			} else {
				out = append(out, fmt.Sprintf("%v", item))
			}
		}
		return out
	default:
		return nil
	}
}

func mapFromAny(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}
