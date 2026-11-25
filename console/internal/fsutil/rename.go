// Package fsutil provides cross-platform filesystem helpers shared across ColabOne.
package fsutil

import (
	"errors"
	"io/fs"
	mathrand "math/rand/v2"
	"os"
	"runtime"
	"syscall"
	"time"
)

const (
	// WindowsRenameMaxAttempts is the maximum number of rename attempts on Windows
	// before surfacing a persistent transient file-sharing error.
	WindowsRenameMaxAttempts = 20
	// WindowsRenameBaseDelay is the base linear backoff step and jitter span on Windows.
	WindowsRenameBaseDelay = 5 * time.Millisecond
	// WindowsRenameMaxDelay caps the base backoff delay before jitter is added.
	WindowsRenameMaxDelay = 50 * time.Millisecond

	// WinErrFileNotFound matches Windows ERROR_FILE_NOT_FOUND (2).
	WinErrFileNotFound syscall.Errno = 2
	// WinErrAccessDenied matches Windows ERROR_ACCESS_DENIED (5).
	WinErrAccessDenied syscall.Errno = 5
	// WinErrSharingViolation matches Windows ERROR_SHARING_VIOLATION (32).
	WinErrSharingViolation syscall.Errno = 32
	// WinErrLockViolation matches Windows ERROR_LOCK_VIOLATION (33).
	WinErrLockViolation syscall.Errno = 33
)

// IsTransientWindowsFileErr reports whether err looks like a transient Windows
// file-sharing or AV-scanner lock error that warrants bounded retry.
func IsTransientWindowsFileErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case WinErrFileNotFound,
			WinErrAccessDenied,
			WinErrSharingViolation,
			WinErrLockViolation:
			return true
		}
	}
	return false
}

// WindowsRetryDelay computes a bounded linear backoff with uniform jitter for the
// given 1-based retry attempt.
func WindowsRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := min(time.Duration(attempt)*WindowsRenameBaseDelay, WindowsRenameMaxDelay)
	return delay + time.Duration(mathrand.Int64N(int64(WindowsRenameBaseDelay)))
}

// RenameReplace renames src onto dst, retrying transient Windows file-sharing
// errors (such as concurrent reads without FILE_SHARE_DELETE or AV scanning)
// with bounded jittered backoff. When dst is an existing directory, it fails fast.
func RenameReplace(src, dst string) error {
	return RenameReplaceWith(
		src,
		dst,
		runtime.GOOS == "windows",
		os.Rename,
		os.Stat,
		time.Sleep,
	)
}

// RenameReplaceWith is the injectable core of RenameReplace for deterministic testing.
func RenameReplaceWith(
	src, dst string,
	isWindows bool,
	renameFn func(oldpath, newpath string) error,
	statFn func(name string) (os.FileInfo, error),
	sleepFn func(time.Duration),
) error {
	err := renameFn(src, dst)
	if err == nil || !isWindows {
		return err
	}
	for attempt := 1; attempt < WindowsRenameMaxAttempts; attempt++ {
		if info, statErr := statFn(dst); statErr == nil && info.IsDir() {
			return err
		}
		if !IsTransientWindowsFileErr(err) {
			return err
		}
		sleepFn(WindowsRetryDelay(attempt))
		err = renameFn(src, dst)
		if err == nil {
			return nil
		}
	}
	return err
}
