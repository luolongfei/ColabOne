package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/activity"
	"github.com/luolongfei/ColabOne/console/internal/controller"
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

// colabOneBridge hosts the in-process pure-Go ColabOneController, replacing the
// legacy Python subprocess pipe with zero-IPC direct Go calls.
type colabOneBridge struct {
	mu        sync.Mutex
	ctrl      *controller.ColabOneController
	factory   func() *controller.ColabOneController
	closeCtrl func(*controller.ColabOneController)
	starting  bool
	startDone chan struct{}
	closedCh  chan struct{}
	closed    bool
}

func colabOneMethodTimeout(method string) time.Duration {
	switch method {
	case "snapshot", "billing.usage", "logs.clear", "error.clear", "drive.cancel", "drive.confirm", "drive.probe",
		"runtime.start.cancel", "runtime.start.retry", "settings.save",
		"api.key", "api.tunnel_token", "comfyui.canvas.tunnel_token", "settings.hf_token",
		"settings.assign_proxy", "settings.assign_proxy.test",
		"shortcuts.status", "shortcuts.create", "shortcuts.remove", "desktop.shortcuts",
		"comfyui.draft.get", "comfyui.draft.save", "comfyui.draft.clear", "comfyui.draft.media.delete":
		return 30 * time.Second
	case "runtime.start":
		// Capped exponential-backoff Colab allocation attempts + remote boot.
		return 900 * time.Second
	case "runtime.release", "runtime.release.all", "runtime.upgrade",
		"drive.connect", "auth.colab.auto", "comfyui.input.upload", "comfyui.output.asset",
		"comfyui.output.export_drive", "comfyui.draft.media.save", "comfyui.draft.media.read":
		return 300 * time.Second
	case "comfyui.tts.preview", "comfyui.tts.preview.asset":
		// 120s preview RPC + RefreshRemote headroom, or multi-chunk WAV asset assembly.
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
				ctrl = newColabOneWiredController("", "", nil, true)
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
			if errors.Is(err, context.DeadlineExceeded) && callCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				return "", errors.New(colabOneT("err.bridgeTimeout"))
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
