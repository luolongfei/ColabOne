package fsutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRenameReplaceRealFilesystem(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "target.txt")
	src1 := filepath.Join(dir, "src1.tmp")
	src2 := filepath.Join(dir, "src2.tmp")

	if err := os.WriteFile(src1, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write src1: %v", err)
	}
	if err := RenameReplace(src1, dst); err != nil {
		t.Fatalf("initial RenameReplace failed: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "v1" {
		t.Fatalf("after initial rename: got=%q err=%v", string(got), err)
	}

	if err := os.WriteFile(src2, []byte("v2"), 0o600); err != nil {
		t.Fatalf("write src2: %v", err)
	}
	if err := RenameReplace(src2, dst); err != nil {
		t.Fatalf("replace RenameReplace failed: %v", err)
	}
	got, err = os.ReadFile(dst)
	if err != nil || string(got) != "v2" {
		t.Fatalf("after replace rename: got=%q err=%v", string(got), err)
	}
	if _, err := os.Stat(src2); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected src2 moved away, stat err=%v", err)
	}
}

func TestRenameReplaceWithWindowsRetryPolicy(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "output.mp4")
	if err := os.WriteFile(filePath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	dirPath := filepath.Join(dir, "existing-dir")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir existing-dir: %v", err)
	}

	sharingErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: WinErrSharingViolation}
	accessErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: WinErrAccessDenied}
	lockErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: WinErrLockViolation}
	notFoundErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: WinErrFileNotFound}
	diskFullErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: syscall.Errno(112)}

	t.Run("non-windows fails fast without retry", func(t *testing.T) {
		calls := 0
		sleeps := 0
		statCalls := 0
		err := RenameReplaceWith(
			"tmp",
			filePath,
			false,
			func(string, string) error {
				calls++
				return sharingErr
			},
			func(name string) (os.FileInfo, error) {
				statCalls++
				return os.Stat(name)
			},
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, sharingErr) {
			t.Fatalf("got err=%v, want %v", err, sharingErr)
		}
		if calls != 1 || sleeps != 0 || statCalls != 0 {
			t.Fatalf("calls=%d sleeps=%d statCalls=%d, want 1/0/0", calls, sleeps, statCalls)
		}
	})

	t.Run("windows immediate success skips stat and sleep", func(t *testing.T) {
		calls := 0
		sleeps := 0
		statCalls := 0
		err := RenameReplaceWith(
			"tmp",
			filePath,
			true,
			func(string, string) error {
				calls++
				return nil
			},
			func(name string) (os.FileInfo, error) {
				statCalls++
				return os.Stat(name)
			},
			func(time.Duration) { sleeps++ },
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 1 || sleeps != 0 || statCalls != 0 {
			t.Fatalf("calls=%d sleeps=%d statCalls=%d, want 1/0/0", calls, sleeps, statCalls)
		}
	})

	t.Run("windows retries all transient errors with bounded jittered delays", func(t *testing.T) {
		transientSeq := []error{sharingErr, accessErr, lockErr, notFoundErr, fs.ErrPermission}
		calls := 0
		var delays []time.Duration
		err := RenameReplaceWith(
			"tmp",
			filePath,
			true,
			func(string, string) error {
				idx := calls
				calls++
				if idx < len(transientSeq) {
					return transientSeq[idx]
				}
				return nil
			},
			os.Stat,
			func(d time.Duration) { delays = append(delays, d) },
		)
		if err != nil {
			t.Fatalf("expected recovery after transient errors, got %v", err)
		}
		if calls != len(transientSeq)+1 || len(delays) != len(transientSeq) {
			t.Fatalf("calls=%d delays=%d, want %d/%d", calls, len(delays), len(transientSeq)+1, len(transientSeq))
		}
		maxExpected := WindowsRenameMaxDelay + WindowsRenameBaseDelay
		for i, d := range delays {
			if d < WindowsRenameBaseDelay || d >= maxExpected {
				t.Fatalf("delay[%d]=%v out of bounded range [%v, %v)", i, d, WindowsRenameBaseDelay, maxExpected)
			}
		}
	})

	t.Run("windows fails fast when target is existing directory", func(t *testing.T) {
		dirAccessErr := &os.LinkError{Op: "rename", Old: "tmp", New: dirPath, Err: WinErrAccessDenied}
		calls := 0
		sleeps := 0
		err := RenameReplaceWith(
			"tmp",
			dirPath,
			true,
			func(string, string) error {
				calls++
				return dirAccessErr
			},
			os.Stat,
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, dirAccessErr) {
			t.Fatalf("got err=%v, want %v", err, dirAccessErr)
		}
		if calls != 1 || sleeps != 0 {
			t.Fatalf("directory target must fail fast: calls=%d sleeps=%d, want 1/0", calls, sleeps)
		}
	})

	t.Run("windows fails fast on non-transient error", func(t *testing.T) {
		calls := 0
		sleeps := 0
		err := RenameReplaceWith(
			"tmp",
			filePath,
			true,
			func(string, string) error {
				calls++
				return diskFullErr
			},
			os.Stat,
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, diskFullErr) {
			t.Fatalf("got err=%v, want %v", err, diskFullErr)
		}
		if calls != 1 || sleeps != 0 {
			t.Fatalf("non-transient error must fail fast: calls=%d sleeps=%d, want 1/0", calls, sleeps)
		}
	})

	t.Run("windows stops after 20 attempts on persistent sharing violation", func(t *testing.T) {
		calls := 0
		sleeps := 0
		err := RenameReplaceWith(
			"tmp",
			filePath,
			true,
			func(string, string) error {
				calls++
				return sharingErr
			},
			os.Stat,
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, sharingErr) {
			t.Fatalf("got err=%v, want %v", err, sharingErr)
		}
		if calls != WindowsRenameMaxAttempts || sleeps != WindowsRenameMaxAttempts-1 {
			t.Fatalf("calls=%d sleeps=%d, want %d/%d", calls, sleeps, WindowsRenameMaxAttempts, WindowsRenameMaxAttempts-1)
		}
	})

	t.Run("IsTransientWindowsFileErr nil returns false", func(t *testing.T) {
		if IsTransientWindowsFileErr(nil) {
			t.Fatal("IsTransientWindowsFileErr(nil) = true, want false")
		}
		if d := WindowsRetryDelay(0); d < WindowsRenameBaseDelay || d >= 2*WindowsRenameBaseDelay {
			t.Fatalf("WindowsRetryDelay(0) = %v out of range", d)
		}
	})
}
