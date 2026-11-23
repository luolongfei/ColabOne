package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type colabOneRPCReply struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}
type colabOneBridge struct {
	mu      sync.Mutex
	writer  sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	exitCh  chan struct{}
	closed  bool
	pending map[uint64]chan colabOneRPCReply
	next    atomic.Uint64
}

func colabOneMethodTimeout(method string) time.Duration {
	switch method {
	case "snapshot", "logs.clear", "error.clear", "drive.cancel", "drive.confirm", "drive.probe",
		"runtime.start.cancel", "runtime.start.retry", "settings.save",
		"api.key", "api.tunnel_token", "comfyui.canvas.tunnel_token", "settings.hf_token",
		"settings.assign_proxy", "settings.assign_proxy.test",
		"shortcuts.status", "shortcuts.create", "shortcuts.remove":
		return 30 * time.Second
	case "runtime.start":
		// Up to 10 exponential-backoff Colab allocation attempts + remote boot.
		return 900 * time.Second
	case "runtime.release", "runtime.release_all", "runtime.upgrade",
		"drive.connect", "auth.colab.auto", "comfyui.output.asset":
		return 300 * time.Second
	default:
		return 120 * time.Second
	}
}

// The child is a private, trusted JSON stream. Scanner's token cap used to
// deadlock both sides once a conversation snapshot exceeded 16 MiB: Go stopped
// reading but waited for Python, while Python was still writing the same reply.
func readColabOneReplies(reader io.Reader, accept func(colabOneRPCReply)) error {
	decoder := json.NewDecoder(reader)
	for {
		var reply colabOneRPCReply
		if err := decoder.Decode(&reply); err != nil {
			return err
		}
		accept(reply)
	}
}

func colabOneRootAt(executable, cwd string) (string, error) {
	candidates := []string{os.Getenv("COLABONE_ROOT"), filepath.Dir(executable), filepath.Join(filepath.Dir(executable), "../../.."), cwd, filepath.Dir(cwd)}
	for _, path := range candidates {
		if path == "" {
			continue
		}
		path, _ = filepath.Abs(path)
		if _, err := os.Stat(filepath.Join(path, "bridge", "main.py")); err == nil {
			return path, nil
		}
	}
	return "", errors.New(colabOneT("err.missingRuntimeFiles"))
}
func colabOnePythonAt(root string) (string, error) {
	candidates := []string{filepath.Join(root, "runtime", "bin", "python3"), filepath.Join(root, ".venv", "bin", "python")}
	if runtime.GOOS == "windows" {
		candidates = []string{filepath.Join(root, "runtime", "python.exe"), filepath.Join(root, ".venv", "Scripts", "python.exe")}
	}
	for _, p := range candidates {
		if _, e := os.Stat(p); e == nil {
			return p, nil
		}
	}
	return "", errors.New(colabOneT("err.missingPython"))
}
func (b *colabOneBridge) start() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.startLocked()
}

func (b *colabOneBridge) startLocked() error {
	if b.closed {
		return errors.New(colabOneT("err.bridgeExited"))
	}
	if b.cmd != nil {
		return nil
	}
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	root, err := colabOneRootAt(exe, cwd)
	if err != nil {
		return err
	}
	py, err := colabOnePythonAt(root)
	if err != nil {
		return err
	}
	cmd := exec.Command(py, "-u", filepath.Join(root, "bridge", "main.py"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONUNBUFFERED=1", "PYTHONPATH="+filepath.Join(root, "site-packages")+string(os.PathListSeparator)+root)
	hideProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	// Leave Stderr nil so os/exec binds the OS null device without a copy goroutine.
	cmd.Stderr = nil
	if err = cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	cleanupJob := attachProcessLifetime(cmd)
	exitCh := make(chan struct{})
	b.cmd, b.stdin, b.exitCh = cmd, stdin, exitCh
	if b.pending == nil {
		b.pending = map[uint64]chan colabOneRPCReply{}
	}
	go func() {
		defer close(exitCh)
		defer func() {
			if cleanupJob != nil {
				cleanupJob()
			}
		}()
		readErr := readColabOneReplies(stdout, func(reply colabOneRPCReply) {
			b.mu.Lock()
			ch := b.pending[reply.ID]
			delete(b.pending, reply.ID)
			b.mu.Unlock()
			if ch != nil {
				ch <- reply
			}
		})
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			// A broken protocol must fail pending requests, never wait indefinitely
			// for a child whose output we can no longer consume.
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		b.mu.Lock()
		defer b.mu.Unlock()
		for id, ch := range b.pending {
			ch <- colabOneRPCReply{Error: colabOneT("err.bridgeExited")}
			delete(b.pending, id)
		}
		if b.cmd == cmd {
			b.cmd = nil
			b.stdin = nil
			b.exitCh = nil
		}
	}()
	return nil
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
	id := b.next.Add(1)
	ch := make(chan colabOneRPCReply, 1)
	b.mu.Lock()
	if err := b.startLocked(); err != nil {
		b.mu.Unlock()
		return "", err
	}
	b.pending[id] = ch
	stdin := b.stdin
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, id); b.mu.Unlock() }()
	if stdin == nil {
		return "", errors.New(colabOneT("err.bridgeExited"))
	}
	body, err := json.Marshal(map[string]any{"id": id, "method": method, "payload": payload})
	if err != nil {
		return "", err
	}
	b.writer.Lock()
	_, err = stdin.Write(append(body, '\n'))
	b.writer.Unlock()
	if err != nil {
		return "", err
	}
	timer := time.NewTimer(colabOneMethodTimeout(method))
	defer timer.Stop()
	select {
	case reply := <-ch:
		if reply.Error != "" {
			return "", errors.New(reply.Error)
		}
		return string(reply.Result), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", errors.New(colabOneT("err.bridgeTimeout"))
	}
}

func (b *colabOneBridge) close() {
	b.mu.Lock()
	b.closed = true
	stdin := b.stdin
	cmd := b.cmd
	exitCh := b.exitCh
	b.stdin = nil
	b.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if exitCh != nil {
		select {
		case <-exitCh:
			return
		case <-time.After(2500 * time.Millisecond):
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-exitCh:
		case <-time.After(time.Second):
		}
	}
}
