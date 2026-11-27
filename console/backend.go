package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/activity"
	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/vault"
)

const colabOneBridgeCloseTimeout = 2500 * time.Millisecond

// ErrBridgeClosed is matched via errors.Is when the bridge has been closed.
var ErrBridgeClosed = errors.New("colabone bridge closed")

type colabOneBridgeClosedError struct {
	msg string
}

func (e colabOneBridgeClosedError) Error() string { return e.msg }
func (e colabOneBridgeClosedError) Is(target error) bool {
	return target == ErrBridgeClosed
}

func newColabOneBridgeClosedError() error {
	return colabOneBridgeClosedError{msg: colabOneT("err.bridgeExited")}
}

type colabOneBridgeTimeoutError struct {
	msg string
}

func (e colabOneBridgeTimeoutError) Error() string { return e.msg }
func (e colabOneBridgeTimeoutError) Is(target error) bool {
	return target == context.DeadlineExceeded
}

func newColabOneBridgeTimeoutError() error {
	return colabOneBridgeTimeoutError{msg: colabOneT("err.bridgeTimeout")}
}

// colabOneBridge hosts the in-process pure-Go ColabOneController, replacing the
// legacy Python subprocess pipe with zero-IPC direct Go calls.
type colabOneBridge struct {
	mu            sync.Mutex
	ctrl           *controller.ColabOneController
	vault          *vault.ColabOneVault
	factory        func() *controller.ColabOneController
	closeCtrl      func(*controller.ColabOneController)
	resolveMediaFn       func(context.Context, string) (*controller.ColabOneOutputMediaMeta, error)
	serveMediaFn         func(context.Context, http.ResponseWriter, *http.Request, string)
	serveMediaWithOptsFn func(context.Context, http.ResponseWriter, *http.Request, string, controller.ColabOneOutputMediaOptions)
	streamMediaFn        func(context.Context, string, io.Writer, controller.ColabOneOutputMediaOptions) (*controller.ColabOneOutputMediaMeta, error)
	onFocusWindow        func()
	starting      bool
	startDone     chan struct{}
	closedCh      chan struct{}
	closed        bool
}

func colabOneMethodTimeout(method string) time.Duration {
	switch method {
	case "snapshot", "billing.usage", "logs.clear", "error.clear", "drive.cancel", "drive.confirm", "drive.probe",
		"runtime.start.cancel", "settings.save",
		"api.key", "api.tunnel_token", "comfyui.canvas.tunnel_token", "settings.hf_token",
		"settings.assign_proxy", "settings.assign_proxy.test",
		"desktop.shortcuts",
		"comfyui.draft.get", "comfyui.draft.save", "comfyui.draft.clear", "comfyui.draft.media.delete",
		"comfyui.tts.preview.cache.lookup", "comfyui.tts.preview.cache.read",
		"comfyui.tts.preview.prefetch", "comfyui.tts.preview.prefetch.cancel",
		"comfyui.auto_release",
		"comfyui.live.status", "comfyui.live.vram_check", "comfyui.live.session_token",
		"comfyui.live.llm.get", "comfyui.live.llm.save", "comfyui.live.llm.clear",
		"comfyui.live.avatars.list", "comfyui.live.driving.list":
		return 30 * time.Second
	case "comfyui.live.avatars.delete", "comfyui.live.driving.delete":
		// 30s DELETE RPC + RefreshRemote headroom.
		return 45 * time.Second
	case "runtime.start":
		// Capped exponential-backoff Colab allocation attempts + remote boot.
		return 900 * time.Second
	case "runtime.release", "runtime.release.all", "runtime.upgrade",
		"drive.connect", "auth.colab.auto", "comfyui.output.asset",
		"comfyui.output.export_drive", "comfyui.draft.media.save", "comfyui.draft.media.read":
		return 300 * time.Second
	case "comfyui.input.upload", "comfyui.live.avatars.clip.upload", "comfyui.live.driving.upload":
		// 300s upload RPC + RefreshRemote headroom.
		return 330 * time.Second
	case "comfyui.outputs.drive.sync":
		// Drive index sync RPC + RefreshRemote headroom.
		return 240 * time.Second
	case "comfyui.live.start":
		// Live Studio startup RPC + RefreshRemote headroom.
		return 180 * time.Second
	case "comfyui.tts.preview", "comfyui.tts.preview.asset",
		"comfyui.live.avatars.build_pack", "comfyui.live.llm.test":
		// 120s RPC + RefreshRemote headroom, or multi-chunk WAV asset assembly.
		return 150 * time.Second
	default:
		return 120 * time.Second
	}
}

func (b *colabOneBridge) closedChLocked() chan struct{} {
	if b.closedCh == nil {
		b.closedCh = make(chan struct{})
	}
	return b.closedCh
}

func (b *colabOneBridge) closeController(ctrl *controller.ColabOneController) {
	if ctrl == nil {
		return
	}
	b.mu.Lock()
	closeFn := b.closeCtrl
	b.mu.Unlock()
	if closeFn != nil {
		closeFn(ctrl)
		return
	}
	ctrl.Close()
}

func (b *colabOneBridge) start() error {
	_, err := b.ensureController(context.Background())
	return err
}

func (b *colabOneBridge) ensureController(ctx context.Context) (*controller.ColabOneController, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return nil, newColabOneBridgeClosedError()
		}
		if b.ctrl != nil {
			ctrl := b.ctrl
			b.mu.Unlock()
			return ctrl, nil
		}
		closedCh := b.closedChLocked()
		if b.starting {
			done := b.startDone
			b.mu.Unlock()
			select {
			case <-done:
				continue
			case <-closedCh:
				return nil, newColabOneBridgeClosedError()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		// Single-flight leader: construct the controller without holding b.mu so
		// local vault or disk I/O during startup cannot block OnWillQuit close().
		b.starting = true
		done := make(chan struct{})
		b.startDone = done
		factory := b.factory
		sharedVault := b.vault
		onFocusWindow := b.onFocusWindow
		b.mu.Unlock()

		var ctrl *controller.ColabOneController
		finished := false
		func() {
			defer func() {
				if !finished {
					b.mu.Lock()
					b.starting = false
					if b.startDone == done {
						b.startDone = nil
					}
					close(done)
					b.mu.Unlock()
				}
			}()
			if factory != nil {
				ctrl = factory()
			} else {
				ctrl = newColabOneWiredController("", "", sharedVault, true)
			}
			if ctrl != nil && onFocusWindow != nil {
				ctrl.SetFocusWindowHandler(onFocusWindow)
			}
			finished = true
		}()

		b.mu.Lock()
		b.starting = false
		if b.startDone == done {
			b.startDone = nil
		}
		if b.closed {
			close(done)
			b.mu.Unlock()
			if ctrl != nil {
				go b.closeController(ctrl)
			}
			return nil, newColabOneBridgeClosedError()
		}
		if ctrl == nil {
			close(done)
			b.mu.Unlock()
			return nil, errors.New(colabOneT("err.bridgeNotInitialized"))
		}
		b.ctrl = ctrl
		close(done)
		b.mu.Unlock()
		return ctrl, nil
	}
}

func encodeColabOneResultJSON(value any) (string, error) {
	var buf bytes.Buffer
	buf.Grow(4096)
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err == nil {
		return string(bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})), nil
	}
	// Fallback: sanitize non-finite floats (NaN, ±Inf) into JSON null before encoding.
	buf.Reset()
	enc = json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(controller.SanitizeJSONValue(value)); err != nil {
		return "", err
	}
	return string(bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})), nil
}

func truncateColabOneRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	n := 0
	for i := range s {
		if n == maxRunes {
			return s[:i]
		}
		n++
	}
	return s
}

func (b *colabOneBridge) call(ctx context.Context, method string, payload map[string]any) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if payload == nil {
		payload = map[string]any{}
	}

	ctrl, err := b.ensureController(ctx)
	if err != nil {
		return "", err
	}

	timeout := colabOneMethodTimeout(method)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var result any
	if method == "snapshot" {
		result = ctrl.Snapshot()
	} else {
		result, err = ctrl.Command(callCtx, method, payload)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if callCtx.Err() == context.DeadlineExceeded || ctx.Err() == context.DeadlineExceeded {
					return "", newColabOneBridgeTimeoutError()
				}
				return "", colabOneBridgeTimeoutError{msg: truncateColabOneRunes(activity.SafeMessage(err.Error()), 500)}
			}
			return "", errors.New(truncateColabOneRunes(activity.SafeMessage(err.Error()), 500))
		}
	}
	return encodeColabOneResultJSON(result)
}

func (b *colabOneBridge) logActivity(message, source, level string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	ctrl := b.ctrl
	closed := b.closed
	b.mu.Unlock()
	if ctrl == nil || closed {
		return
	}
	ctrl.LogActivity(message, source, level)
}

func (b *colabOneBridge) resolveOutputMedia(
	ctx context.Context,
	id string,
) (*controller.ColabOneOutputMediaMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	fn := b.resolveMediaFn
	b.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	ctrl, err := b.ensureController(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	meta, err := ctrl.ResolveOutputMediaMeta(callCtx, id)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			if callCtx.Err() == context.DeadlineExceeded || ctx.Err() == context.DeadlineExceeded {
				return nil, newColabOneBridgeTimeoutError()
			}
			return nil, colabOneBridgeTimeoutError{msg: truncateColabOneRunes(activity.SafeMessage(err.Error()), 500)}
		}
		return nil, errors.New(truncateColabOneRunes(activity.SafeMessage(err.Error()), 500))
	}
	return meta, nil
}

func writeColabOneMediaError(rw http.ResponseWriter, r *http.Request, status int, msg string) {
	safe := activity.SafeMessage(strings.TrimSpace(msg))
	if safe == "" {
		safe = http.StatusText(status)
	}
	h := rw.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-ColabOne-Error", strings.ReplaceAll(url.PathEscape(safe), "+", "%2B"))
	rw.WriteHeader(status)
	if r == nil || r.Method != http.MethodHead {
		_ = json.NewEncoder(rw).Encode(map[string]string{"error": safe})
	}
}

func (b *colabOneBridge) serveOutputMedia(
	ctx context.Context,
	rw http.ResponseWriter,
	r *http.Request,
	id string,
) {
	b.serveOutputMediaWithOptions(ctx, rw, r, id, controller.ColabOneOutputMediaOptions{})
}

func (b *colabOneBridge) serveOutputMediaWithOptions(
	ctx context.Context,
	rw http.ResponseWriter,
	r *http.Request,
	id string,
	opts controller.ColabOneOutputMediaOptions,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	fnWithOpts := b.serveMediaWithOptsFn
	fn := b.serveMediaFn
	b.mu.Unlock()
	if fnWithOpts != nil {
		fnWithOpts(ctx, rw, r, id, opts)
		return
	}
	if fn != nil && !opts.RuntimeOnly && !opts.SanitizeErrors {
		fn(ctx, rw, r, id)
		return
	}
	ctrl, err := b.ensureController(ctx)
	if err != nil {
		msg := err.Error()
		if opts.SanitizeErrors {
			msg = "作品暂离线或暂不可访问，请稍后再试"
		} else if opts.RuntimeOnly {
			msg = "当前无法访问该作品文件，请稍后再试"
		}
		writeColabOneMediaError(rw, r, http.StatusServiceUnavailable, msg)
		return
	}
	ctrl.ServeOutputMediaHTTPWithOptions(ctx, rw, r, id, opts)
}

type colabOneBridgeSanitizedError struct {
	msg string
	err error
}

func (e *colabOneBridgeSanitizedError) Error() string { return e.msg }
func (e *colabOneBridgeSanitizedError) Unwrap() error { return e.err }

type colabOneBridgeMetaReceiver interface {
	SetColabOneOutputMeta(id, filename, ext, mime string, size int64)
}

type colabOneBridgeTrackingWriter struct {
	w        io.Writer
	writeErr error
}

func (t *colabOneBridgeTrackingWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if err != nil && t.writeErr == nil {
		t.writeErr = err
	}
	return n, err
}

func (t *colabOneBridgeTrackingWriter) SetColabOneOutputMeta(id, filename, ext, mime string, size int64) {
	if recv, ok := t.w.(colabOneBridgeMetaReceiver); ok {
		recv.SetColabOneOutputMeta(id, filename, ext, mime, size)
	}
}

func (b *colabOneBridge) streamOutputMedia(
	ctx context.Context,
	id string,
	w io.Writer,
	opts controller.ColabOneOutputMediaOptions,
) (*controller.ColabOneOutputMediaMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	fn := b.streamMediaFn
	b.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, w, opts)
	}
	ctrl, err := b.ensureController(ctx)
	if err != nil {
		if opts.SanitizeErrors || opts.RuntimeOnly {
			return nil, controller.ErrOutputMediaOffline
		}
		return nil, err
	}
	tw := &colabOneBridgeTrackingWriter{w: w}
	meta, streamErr := ctrl.StreamOutputMediaToWriter(ctx, id, tw, opts)
	if streamErr == nil {
		return meta, nil
	}
	// Preserve caller writer errors (such as ZIP 1 GiB limit sentinels) untouched.
	if tw.writeErr != nil && errors.Is(streamErr, tw.writeErr) {
		return meta, streamErr
	}
	if errors.Is(streamErr, context.Canceled) {
		return meta, streamErr
	}
	if errors.Is(streamErr, context.DeadlineExceeded) {
		if ctx.Err() == context.DeadlineExceeded {
			return meta, newColabOneBridgeTimeoutError()
		}
		return meta, colabOneBridgeTimeoutError{msg: truncateColabOneRunes(activity.SafeMessage(streamErr.Error()), 500)}
	}
	safe := truncateColabOneRunes(activity.SafeMessage(streamErr.Error()), 500)
	if safe == "" {
		safe = streamErr.Error()
	}
	return meta, &colabOneBridgeSanitizedError{msg: safe, err: streamErr}
}

func (b *colabOneBridge) close() {
	b.closeWithTimeout(colabOneBridgeCloseTimeout)
}

func (b *colabOneBridge) closeWithTimeout(timeout time.Duration) {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		close(b.closedChLocked())
	}
	ctrl := b.ctrl
	b.ctrl = nil
	b.mu.Unlock()

	if ctrl == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.closeController(ctrl)
	}()
	if timeout <= 0 {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}
