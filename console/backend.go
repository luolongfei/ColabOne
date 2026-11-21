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
	pending map[uint64]chan colabOneRPCReply
	next    atomic.Uint64
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
	// No credential-bearing subprocess output is written to a log file.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return err
	}
	b.cmd, b.stdin, b.pending = cmd, stdin, map[uint64]chan colabOneRPCReply{}
	go func() {
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
		b.cmd = nil
	}()
	return nil
}
func (b *colabOneBridge) call(ctx context.Context, method string, payload map[string]any) (string, error) {
	if err := b.start(); err != nil {
		return "", err
	}
	id := b.next.Add(1)
	ch := make(chan colabOneRPCReply, 1)
	b.mu.Lock()
	b.pending[id] = ch
	stdin := b.stdin
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, id); b.mu.Unlock() }()
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
	select {
	case reply := <-ch:
		if reply.Error != "" {
			return "", errors.New(reply.Error)
		}
		return string(reply.Result), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(90 * time.Second):
		return "", errors.New(colabOneT("err.bridgeTimeout"))
	}
}
func (b *colabOneBridge) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stdin != nil {
		_ = b.stdin.Close()
	}
}
