package updater

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestFeedServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	var ln net.Listener
	for i := 0; i < 16; i++ {
		cand, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen loopback: %v", err)
		}
		addr := cand.Addr().String()
		if !strings.Contains(addr, "404") && !strings.Contains(addr, "429") {
			ln = cand
			break
		}
		_ = cand.Close()
	}
	if ln == nil {
		t.Fatal("failed to allocate loopback port without 404/429 digits")
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func waitForUpdateStatus(t *testing.T, events <-chan ColabOneUpdateState, want string) ColabOneUpdateState {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case st := <-events:
			if st.Status == want {
				return st
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for updater status %q", want)
		}
	}
}

func TestStartBackgroundAutoCheckAndCleanClose(t *testing.T) {
	t.Run("default_initial_delay_checks_early_on_startup", func(t *testing.T) {
		if COLABONE_UPDATE_INITIAL_DELAY != 1*time.Second {
			t.Fatalf("COLABONE_UPDATE_INITIAL_DELAY = %v, want 1s", COLABONE_UPDATE_INITIAL_DELAY)
		}
	})

	t.Run("manual_check_before_initial_delay_skips_duplicate_startup_check", func(t *testing.T) {
		var checks atomic.Int32
		secondCheck := make(chan struct{})
		var secondOnce sync.Once

		backend := &fakeBackend{
			current: "0.1.6",
			canSelf: true,
			checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
				if checks.Add(1) >= 2 {
					secondOnce.Do(func() { close(secondCheck) })
				}
				return &fakeInstallable{version: "0.1.7", notes: "notes"}, nil
			},
		}

		u := NewColabOneUpdater(ColabOneUpdaterConfig{
			Backend:       backend,
			ConfigDir:     t.TempDir(),
			InitialDelay:  40 * time.Millisecond,
			CheckInterval: 160 * time.Millisecond,
		})
		defer u.Close()

		u.StartBackground()
		if _, err := u.Check(context.Background(), true); err != nil {
			t.Fatalf("manual Check failed: %v", err)
		}
		if got := checks.Load(); got != 1 {
			t.Fatalf("expected 1 check after manual Check, got %d", got)
		}

		// Wait past InitialDelay (40ms) but well before the first periodic CheckInterval (40ms + 160ms = 200ms).
		time.Sleep(90 * time.Millisecond)
		if got := checks.Load(); got != 1 {
			t.Fatalf("expected startup timer to skip duplicate check after manual Check, got %d checks", got)
		}

		// Periodic CheckInterval tick must still run normally.
		select {
		case <-secondCheck:
		case <-time.After(2 * time.Second):
			t.Fatalf("expected periodic check after CheckInterval, got %d checks", checks.Load())
		}
	})

	t.Run("auto_check_enabled_fires_and_stops_on_close", func(t *testing.T) {
		var checks atomic.Int32
		twoChecks := make(chan struct{})
		var twoOnce sync.Once

		backend := &fakeBackend{
			current: "0.1.6",
			canSelf: true,
			checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
				if checks.Add(1) >= 2 {
					twoOnce.Do(func() { close(twoChecks) })
				}
				return nil, nil
			},
		}

		u := NewColabOneUpdater(ColabOneUpdaterConfig{
			Backend:       backend,
			ConfigDir:     t.TempDir(),
			InitialDelay:  15 * time.Millisecond,
			CheckInterval: 25 * time.Millisecond,
		})

		// Calling StartBackground twice must only spawn one loop.
		u.StartBackground()
		u.StartBackground()

		select {
		case <-twoChecks:
		case <-time.After(2 * time.Second):
			u.Close()
			t.Fatalf("timed out waiting for background auto-checks; got %d checks", checks.Load())
		}

		// Close must stop the loop cleanly and be idempotent.
		closeDone := make(chan struct{})
		go func() {
			u.Close()
			u.Close()
			close(closeDone)
		}()
		select {
		case <-closeDone:
		case <-time.After(2 * time.Second):
			t.Fatal("Close() did not return promptly")
		}

		countAfterClose := checks.Load()
		time.Sleep(75 * time.Millisecond)
		if got := checks.Load(); got != countAfterClose {
			t.Fatalf("expected no checks after Close(), before=%d after=%d", countAfterClose, got)
		}
	})

	t.Run("auto_check_disabled_does_not_fire_until_reenabled", func(t *testing.T) {
		var checks atomic.Int32
		firstCheck := make(chan struct{})
		var firstOnce sync.Once

		backend := &fakeBackend{
			current: "0.1.6",
			canSelf: true,
			checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
				checks.Add(1)
				firstOnce.Do(func() { close(firstCheck) })
				return nil, nil
			},
		}

		u := NewColabOneUpdater(ColabOneUpdaterConfig{
			Backend:       backend,
			ConfigDir:     t.TempDir(),
			InitialDelay:  15 * time.Millisecond,
			CheckInterval: 25 * time.Millisecond,
		})
		defer u.Close()

		if _, _, err := u.SetPreferences(false, false); err != nil {
			t.Fatalf("SetPreferences(false, false) failed: %v", err)
		}

		u.StartBackground()
		time.Sleep(90 * time.Millisecond)
		if got := checks.Load(); got != 0 {
			t.Fatalf("expected 0 checks while AutoCheck=false, got %d", got)
		}

		// Re-enabling AutoCheck should allow subsequent periodic ticks to run.
		if _, _, err := u.SetPreferences(true, false); err != nil {
			t.Fatalf("SetPreferences(true, false) failed: %v", err)
		}
		select {
		case <-firstCheck:
		case <-time.After(2 * time.Second):
			t.Fatal("expected background loop to fire after re-enabling AutoCheck")
		}
	})

	t.Run("close_before_initial_delay_exits_immediately", func(t *testing.T) {
		var checks atomic.Int32
		backend := &fakeBackend{
			current: "0.1.6",
			canSelf: true,
			checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
				checks.Add(1)
				return nil, nil
			},
		}

		u := NewColabOneUpdater(ColabOneUpdaterConfig{
			Backend:       backend,
			ConfigDir:     t.TempDir(),
			InitialDelay:  5 * time.Second,
			CheckInterval: 5 * time.Second,
		})
		u.StartBackground()

		done := make(chan struct{})
		go func() {
			u.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatal("Close() blocked waiting for InitialDelay")
		}
		if got := checks.Load(); got != 0 {
			t.Fatalf("expected 0 checks when closed before InitialDelay, got %d", got)
		}
	})

	t.Run("close_cancels_inflight_install_without_leaks", func(t *testing.T) {
		installStarted := make(chan struct{})
		installReturned := make(chan struct{})

		backend := &fakeBackend{
			current: "0.1.6",
			canSelf: true,
			checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
				return &fakeInstallable{
					version: "0.1.7",
					notes:   "notes",
					install: func(ctx context.Context, progress func(downloaded, total int64)) error {
						close(installStarted)
						<-ctx.Done()
						close(installReturned)
						return ctx.Err()
					},
				}, nil
			},
		}

		u := NewColabOneUpdater(ColabOneUpdaterConfig{
			Backend:   backend,
			ConfigDir: t.TempDir(),
		})
		if _, err := u.Check(context.Background(), true); err != nil {
			t.Fatalf("Check failed: %v", err)
		}
		if _, err := u.StartInstall(); err != nil {
			t.Fatalf("StartInstall failed: %v", err)
		}
		<-installStarted

		closeDone := make(chan struct{})
		go func() {
			u.Close()
			close(closeDone)
		}()

		select {
		case <-closeDone:
		case <-time.After(2 * time.Second):
			t.Fatal("Close() did not terminate in-flight install promptly")
		}
		select {
		case <-installReturned:
		default:
			t.Fatal("expected Close() to wait for install goroutine to exit")
		}
	})
}

func TestConcurrentCheckSingleFlightAndInstallNoOp(t *testing.T) {
	var checkCalls atomic.Int32
	var installCalls atomic.Int32
	checkEntered := make(chan struct{})
	releaseCheck := make(chan struct{})
	installStarted := make(chan struct{})
	releaseInstall := make(chan struct{})
	events := make(chan ColabOneUpdateState, 64)

	backend := &fakeBackend{
		current: "0.1.6",
		canSelf: true,
		checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
			n := checkCalls.Add(1)
			if n == 1 {
				close(checkEntered)
				<-releaseCheck
			}
			return &fakeInstallable{
				version: "0.1.7",
				notes:   "release notes",
				install: func(ctx context.Context, progress func(downloaded, total int64)) error {
					installCalls.Add(1)
					progress(100, 1000)
					close(installStarted)
					<-releaseInstall
					progress(1000, 1000)
					return nil
				},
			}, nil
		},
	}

	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   backend,
		ConfigDir: t.TempDir(),
		OnStateChange: func(st ColabOneUpdateState) {
			events <- st
		},
	})
	defer u.Close()

	// Start primary check and wait until it is definitely in flight.
	primaryDone := make(chan error, 1)
	go func() {
		_, err := u.Check(context.Background(), true)
		primaryDone <- err
	}()
	<-checkEntered

	// Concurrent background checks (manual=false) short-circuit immediately without hitting backend;
	// concurrent manual checks (manual=true) coalesce onto the single in-flight check and await its result.
	const bgCallers = 4
	var bgWG sync.WaitGroup
	bgWG.Add(bgCallers)
	bgErrs := make(chan error, bgCallers)
	for i := 0; i < bgCallers; i++ {
		go func() {
			defer bgWG.Done()
			st, err := u.Check(context.Background(), false)
			if err != nil {
				bgErrs <- err
				return
			}
			if st.Status != StatusChecking {
				bgErrs <- errors.New("expected checking state while primary check is in flight: " + st.Status)
			}
		}()
	}
	bgDone := make(chan struct{})
	go func() {
		bgWG.Wait()
		close(bgDone)
	}()
	select {
	case <-bgDone:
	case <-time.After(2 * time.Second):
		close(releaseCheck)
		t.Fatal("concurrent background Check callers blocked instead of short-circuiting")
	}
	close(bgErrs)
	for err := range bgErrs {
		t.Fatalf("concurrent background Check error: %v", err)
	}

	const manualCallers = 4
	var manualWG sync.WaitGroup
	manualWG.Add(manualCallers)
	manualErrs := make(chan error, manualCallers)
	for i := 0; i < manualCallers; i++ {
		go func() {
			defer manualWG.Done()
			st, err := u.Check(context.Background(), true)
			if err != nil {
				manualErrs <- err
				return
			}
			if st.Status != StatusAvailable || st.AvailableVersion != "0.1.7" {
				manualErrs <- errors.New("expected coalesced manual check to receive available 0.1.7: " + st.Status)
			}
		}()
	}

	// Wait deterministically until all manual callers have joined the in-flight check.
	waitDeadline := time.Now().Add(2 * time.Second)
	for {
		u.mu.Lock()
		waiters := u.checkWaiters
		u.mu.Unlock()
		if waiters == manualCallers {
			break
		}
		if time.Now().After(waitDeadline) {
			close(releaseCheck)
			t.Fatalf("timed out waiting for %d coalesced manual callers, got %d", manualCallers, waiters)
		}
		time.Sleep(time.Millisecond)
	}

	// Let primary check finish and verify all coalesced manual callers unblock with the single result.
	close(releaseCheck)
	if err := <-primaryDone; err != nil {
		t.Fatalf("primary Check failed: %v", err)
	}
	manualDone := make(chan struct{})
	go func() {
		manualWG.Wait()
		close(manualDone)
	}()
	select {
	case <-manualDone:
	case <-time.After(2 * time.Second):
		t.Fatal("coalesced manual Check callers did not unblock after primary check completed")
	}
	close(manualErrs)
	for err := range manualErrs {
		t.Fatalf("coalesced manual Check error: %v", err)
	}
	if got := checkCalls.Load(); got != 1 {
		t.Fatalf("single-flight broken: expected 1 backend CheckSelfUpdate call, got %d", got)
	}
	if st := u.State(); st.Status != StatusAvailable || st.AvailableVersion != "0.1.7" {
		t.Fatalf("unexpected state after primary Check: %+v", st)
	}

	// Start install and confirm Check & duplicate StartInstall are safe no-ops while downloading.
	if _, err := u.StartInstall(); err != nil {
		t.Fatalf("StartInstall failed: %v", err)
	}
	<-installStarted

	for _, manual := range []bool{true, false} {
		st, err := u.Check(context.Background(), manual)
		if err != nil {
			t.Fatalf("Check(manual=%v) while downloading returned error: %v", manual, err)
		}
		if st.Status != StatusDownloading {
			t.Fatalf("Check(manual=%v) while downloading should preserve downloading status, got %q", manual, st.Status)
		}
	}
	if st, err := u.StartInstall(); err != nil || st.Status != StatusDownloading {
		t.Fatalf("duplicate StartInstall while downloading should be a no-op, got st=%+v err=%v", st, err)
	}
	if got := checkCalls.Load(); got != 1 {
		t.Fatalf("Check during download should not hit backend: got %d calls", got)
	}
	if got := installCalls.Load(); got != 1 {
		t.Fatalf("duplicate StartInstall should not launch second install: got %d calls", got)
	}

	// Finish install and confirm Check & SkipVersion are safe no-ops in ready_to_restart.
	close(releaseInstall)
	ready := waitForUpdateStatus(t, events, StatusReadyToRestart)
	if ready.ProgressPercent != 100 {
		t.Fatalf("expected 100%% progress on ready_to_restart, got %v", ready.ProgressPercent)
	}

	for _, manual := range []bool{true, false} {
		st, err := u.Check(context.Background(), manual)
		if err != nil {
			t.Fatalf("Check(manual=%v) in ready_to_restart returned error: %v", manual, err)
		}
		if st.Status != StatusReadyToRestart {
			t.Fatalf("Check(manual=%v) in ready_to_restart should preserve status, got %q", manual, st.Status)
		}
	}
	if st, err := u.SkipVersion("0.1.7"); err != nil || st.Status != StatusReadyToRestart {
		t.Fatalf("SkipVersion in ready_to_restart should be a no-op, got st=%+v err=%v", st, err)
	}
	if got := checkCalls.Load(); got != 1 {
		t.Fatalf("Check in ready_to_restart should not hit backend: got %d calls", got)
	}
}

func TestCorruptedUpdaterPreferencesFallbackToSafeDefaults(t *testing.T) {
	cases := []struct {
		name              string
		payload           []byte
		wantAutoCheck     bool
		wantIncludePre    bool
		wantSkipped       string
		wantLastCheckedAt int64
	}{
		{
			name:          "invalid_json",
			payload:       []byte("{not-valid-json"),
			wantAutoCheck: true,
		},
		{
			name:          "empty_file",
			payload:       []byte(""),
			wantAutoCheck: true,
		},
		{
			name:          "json_array",
			payload:       []byte("[1, 2, 3]"),
			wantAutoCheck: true,
		},
		{
			name:          "wrong_field_types",
			payload:       []byte(`{"auto_check":"yes","include_prerelease":123}`),
			wantAutoCheck: true,
		},
		{
			name:          "binary_garbage",
			payload:       []byte{0xff, 0xfe, 0x00, '{', '}'},
			wantAutoCheck: true,
		},
		{
			name:              "partial_json_preserves_bool_defaults",
			payload:           []byte(`{"skipped_version":"v0.1.9","last_checked_at":1700000000}`),
			wantAutoCheck:     true,
			wantIncludePre:    false,
			wantSkipped:       "0.1.9",
			wantLastCheckedAt: 1700000000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			prefPath := filepath.Join(dir, COLABONE_UPDATE_PREF_FILE)
			if err := os.WriteFile(prefPath, tc.payload, 0o600); err != nil {
				t.Fatalf("write corrupt prefs: %v", err)
			}

			backend := &fakeBackend{current: "0.1.6", canSelf: true}
			u := NewColabOneUpdater(ColabOneUpdaterConfig{
				Backend:   backend,
				ConfigDir: dir,
			})
			defer u.Close()

			st := u.State()
			if st.AutoCheck != tc.wantAutoCheck {
				t.Fatalf("AutoCheck = %v, want %v", st.AutoCheck, tc.wantAutoCheck)
			}
			if st.IncludePrerelease != tc.wantIncludePre {
				t.Fatalf("IncludePrerelease = %v, want %v", st.IncludePrerelease, tc.wantIncludePre)
			}
			if st.SkippedVersion != tc.wantSkipped {
				t.Fatalf("SkippedVersion = %q, want %q", st.SkippedVersion, tc.wantSkipped)
			}
			if st.LastCheckedAt != tc.wantLastCheckedAt {
				t.Fatalf("LastCheckedAt = %d, want %d", st.LastCheckedAt, tc.wantLastCheckedAt)
			}
			if backend.Prereleases() != tc.wantIncludePre {
				t.Fatalf("backend Prereleases = %v, want %v", backend.Prereleases(), tc.wantIncludePre)
			}

			// Updating preferences afterwards must heal updater.json on disk.
			if _, _, err := u.SetPreferences(false, true); err != nil {
				t.Fatalf("SetPreferences failed: %v", err)
			}
			reloaded := NewColabOneUpdater(ColabOneUpdaterConfig{
				Backend:   &fakeBackend{current: "0.1.6", canSelf: true},
				ConfigDir: dir,
			})
			defer reloaded.Close()
			got := reloaded.State()
			if got.AutoCheck || !got.IncludePrerelease {
				t.Fatalf("healed preferences did not reload cleanly: %+v", got)
			}
		})
	}
}

func TestStartInstallSignatureAndRateLimitErrorsAllowRetry(t *testing.T) {
	cases := []struct {
		name        string
		firstErr    error
		wantErrCode string
	}{
		{
			name:        "signature_error_then_retry_succeeds",
			firstErr:    errors.New("the update is not signed with the app's key"),
			wantErrCode: ErrCodeSignature,
		},
		{
			name:        "rate_limit_429_then_retry_succeeds",
			firstErr:    errors.New("GET https://github.com/luolongfei/ColabOne/releases/download/v0.1.7/colabone.tar.gz: 429 Too Many Requests"),
			wantErrCode: ErrCodeRateLimited,
		},
		{
			name:        "rate_limit_403_then_retry_succeeds",
			firstErr:    errors.New("GET https://api.github.com/repos/luolongfei/ColabOne/releases: 403 rate limit exceeded"),
			wantErrCode: ErrCodeRateLimited,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			events := make(chan ColabOneUpdateState, 64)

			backend := &fakeBackend{
				current: "0.1.6",
				canSelf: true,
				checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
					return &fakeInstallable{
						version: "0.1.7",
						notes:   "## 新增\n- 修复\n\n---\n\n## Added\n- Fix",
						install: func(ctx context.Context, progress func(downloaded, total int64)) error {
							attempt := attempts.Add(1)
							progress(250, 1000)
							if attempt == 1 {
								return tc.firstErr
							}
							progress(1000, 1000)
							return nil
						},
					}, nil
				},
			}

			u := NewColabOneUpdater(ColabOneUpdaterConfig{
				Backend:   backend,
				ConfigDir: t.TempDir(),
				OnStateChange: func(st ColabOneUpdateState) {
					events <- st
				},
			})
			defer u.Close()

			if _, err := u.Check(context.Background(), true); err != nil {
				t.Fatalf("Check failed: %v", err)
			}

			// First install attempt fails with the classified error.
			if _, err := u.StartInstall(); err != nil {
				t.Fatalf("StartInstall (attempt 1) failed to start: %v", err)
			}
			errState := waitForUpdateStatus(t, events, StatusError)
			if errState.ErrorCode != tc.wantErrCode {
				t.Fatalf("ErrorCode = %q, want %q (msg=%q)", errState.ErrorCode, tc.wantErrCode, errState.ErrorMessage)
			}
			if errState.ErrorMessage == "" {
				t.Fatal("expected non-empty ErrorMessage on StatusError")
			}
			if errState.SpeedBytesPerSec != 0 {
				t.Fatalf("expected speed reset to 0 on error, got %v", errState.SpeedBytesPerSec)
			}
			if errState.AvailableVersion != "0.1.7" {
				t.Fatalf("expected AvailableVersion to remain 0.1.7 for retry, got %q", errState.AvailableVersion)
			}

			// Retry directly via StartInstall() without requiring a second Check().
			retrySnap, err := u.StartInstall()
			if err != nil {
				t.Fatalf("StartInstall retry failed: %v", err)
			}
			if retrySnap.Status != StatusDownloading || retrySnap.ErrorCode != ErrCodeNone || retrySnap.ErrorMessage != "" {
				t.Fatalf("retry StartInstall should clear prior error and enter downloading: %+v", retrySnap)
			}

			readyState := waitForUpdateStatus(t, events, StatusReadyToRestart)
			if readyState.ErrorCode != ErrCodeNone || readyState.ErrorMessage != "" {
				t.Fatalf("expected clean ready_to_restart after retry, got %+v", readyState)
			}
			if readyState.ProgressPercent != 100 || readyState.DownloadedBytes != 1000 {
				t.Fatalf("expected 100%% completion after retry, got %+v", readyState)
			}
			if got := attempts.Load(); got != 2 {
				t.Fatalf("expected 2 install attempts, got %d", got)
			}
			if err := u.Relaunch(); err != nil {
				t.Fatalf("Relaunch after successful retry failed: %v", err)
			}
		})
	}
}

func TestReadOnlyFeedCheckerErrorsAndPrereleaseFallback(t *testing.T) {
	t.Run("http_404_not_found", func(t *testing.T) {
		srv := newTestFeedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))

		checker := &ColabOneReadOnlyFeedChecker{
			Repo:       COLABONE_GITHUB_REPO,
			Target:     "windows-amd64",
			GitHubBase: srv.URL,
			APIBase:    srv.URL,
			Client:     srv.Client(),
		}

		_, err := checker.Check(context.Background(), false)
		if !errors.Is(err, ErrNoReleasePublished) {
			t.Fatalf("expected ErrNoReleasePublished when manifest & releases/latest both 404, got %v", err)
		}
		if code, _ := ClassifyUpdateError(err); code != ErrCodeNotFound {
			t.Fatalf("expected 404/unpublished to classify as %q, got %q (err=%v)", ErrCodeNotFound, code, err)
		}
	})

	t.Run("stable_manifest_404_up_to_date_via_redirect_zero_api_calls", func(t *testing.T) {
		var apiCalls atomic.Int32
		srv := newTestFeedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/luolongfei/ColabOne/releases/latest/download/update-windows-amd64.json":
				http.NotFound(w, r)
			case "/luolongfei/ColabOne/releases/latest":
				w.Header().Set("Location", "https://github.com/luolongfei/ColabOne/releases/tag/v0.1.6")
				w.WriteHeader(http.StatusFound)
			default:
				apiCalls.Add(1)
				http.NotFound(w, r)
			}
		}))

		checker := &ColabOneReadOnlyFeedChecker{
			Repo:           COLABONE_GITHUB_REPO,
			Target:         "windows-amd64",
			GitHubBase:     srv.URL,
			APIBase:        srv.URL,
			Client:         srv.Client(),
			CurrentVersion: func() string { return "0.1.6" },
		}

		rel, err := checker.Check(context.Background(), false)
		if err != nil {
			t.Fatalf("expected stable 404 redirect fallback to succeed, got %v", err)
		}
		if rel.Version != "0.1.6" {
			t.Fatalf("Version = %q, want %q", rel.Version, "0.1.6")
		}
		if got := apiCalls.Load(); got != 0 {
			t.Fatalf("expected 0 GitHub API calls when already up to date, got %d", got)
		}
	})

	t.Run("stable_manifest_404_newer_unsigned_release_enriches_notes", func(t *testing.T) {
		notes := "## 新增\n- 手动发布测试\n\n---\n\n## Added\n- Manual release test"
		srv := newTestFeedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/luolongfei/ColabOne/releases/latest/download/update-windows-amd64.json":
				http.NotFound(w, r)
			case "/luolongfei/ColabOne/releases/latest":
				w.Header().Set("Location", "https://github.com/luolongfei/ColabOne/releases/tag/v0.1.7")
				w.WriteHeader(http.StatusFound)
			case "/repos/luolongfei/ColabOne/releases/tags/v0.1.7":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"tag_name":     "v0.1.7",
					"draft":        false,
					"body":         notes,
					"published_at": "2026-11-24T02:00:00Z",
				})
			default:
				http.NotFound(w, r)
			}
		}))

		checker := &ColabOneReadOnlyFeedChecker{
			Repo:           COLABONE_GITHUB_REPO,
			Target:         "windows-amd64",
			GitHubBase:     srv.URL,
			APIBase:        srv.URL,
			Client:         srv.Client(),
			CurrentVersion: func() string { return "0.1.6" },
		}

		rel, err := checker.Check(context.Background(), false)
		if err != nil {
			t.Fatalf("expected newer unsigned fallback to succeed, got %v", err)
		}
		if rel.Version != "0.1.7" || rel.Notes != notes {
			t.Fatalf("unexpected release metadata: %+v", rel)
		}
	})

	t.Run("http_429_rate_limited", func(t *testing.T) {
		srv := newTestFeedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
		}))

		checker := &ColabOneReadOnlyFeedChecker{
			Repo:       COLABONE_GITHUB_REPO,
			Target:     "windows-amd64",
			GitHubBase: srv.URL,
			APIBase:    srv.URL,
			Client:     srv.Client(),
		}

		for _, includePre := range []bool{false, true} {
			_, err := checker.Check(context.Background(), includePre)
			if err == nil || !strings.HasSuffix(err.Error(), ": 429 Too Many Requests") {
				t.Fatalf("includePre=%v: expected 429 Too Many Requests error, got %v", includePre, err)
			}
			if code, _ := ClassifyUpdateError(err); code != ErrCodeRateLimited {
				t.Fatalf("includePre=%v: expected 429 to classify as %q, got %q", includePre, ErrCodeRateLimited, code)
			}
		}
	})

	t.Run("malformed_json_manifest_and_releases", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{malformed json"))
		}))
		defer srv.Close()

		checker := &ColabOneReadOnlyFeedChecker{
			Repo:       COLABONE_GITHUB_REPO,
			Target:     "darwin-arm64",
			GitHubBase: srv.URL,
			APIBase:    srv.URL,
			Client:     srv.Client(),
		}

		if _, err := checker.Check(context.Background(), false); err == nil || !strings.Contains(err.Error(), "parse update manifest") {
			t.Fatalf("expected parse update manifest error, got %v", err)
		}
		if _, err := checker.Check(context.Background(), true); err == nil || !strings.Contains(err.Error(), "parse GitHub releases") {
			t.Fatalf("expected parse GitHub releases error, got %v", err)
		}
	})

	t.Run("incomplete_manifest_empty_version", func(t *testing.T) {
		for _, payload := range []string{`{}`, `{"version":"","notes":"only notes"}`, `{"version":"  v  "}`} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(payload))
			}))

			checker := &ColabOneReadOnlyFeedChecker{
				Repo:       COLABONE_GITHUB_REPO,
				Target:     "windows-amd64",
				GitHubBase: srv.URL,
				APIBase:    srv.URL,
				Client:     srv.Client(),
			}
			_, err := checker.Check(context.Background(), false)
			srv.Close()
			if err == nil || !strings.Contains(err.Error(), "incomplete update manifest") {
				t.Fatalf("payload %s: expected incomplete update manifest error, got %v", payload, err)
			}
		}
	})

	t.Run("oversized_manifest_over_1mib", func(t *testing.T) {
		oversized, err := json.Marshal(ColabOneFeedManifest{
			Version: "0.1.8",
			Notes:   strings.Repeat("x", colabOneMaxManifestBytes+64),
			Date:    "2026-11-23T12:00:00Z",
		})
		if err != nil {
			t.Fatalf("marshal oversized manifest: %v", err)
		}
		if int64(len(oversized)) <= colabOneMaxManifestBytes {
			t.Fatalf("test fixture not oversized: %d bytes", len(oversized))
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(oversized)
		}))
		defer srv.Close()

		checker := &ColabOneReadOnlyFeedChecker{
			Repo:       COLABONE_GITHUB_REPO,
			Target:     "windows-amd64",
			GitHubBase: srv.URL,
			APIBase:    srv.URL,
			Client:     srv.Client(),
		}

		_, err = checker.Check(context.Background(), false)
		if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
			t.Fatalf("expected exceeds limit error for >1 MiB manifest, got %v", err)
		}
	})

	t.Run("prerelease_fallback_to_github_body_when_manifest_404", func(t *testing.T) {
		const fallbackBody = "ColabOne v0.2.0-rc.2\n\n## 新增功能\n- 预发布说明回退\n\n---\n\n## New Features\n- Prerelease body fallback\n"
		var (
			manifestRequested atomic.Bool
			seenUserAgent     string
			seenAccept        string
			headerMu          sync.Mutex
		)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/luolongfei/ColabOne/releases":
				headerMu.Lock()
				seenUserAgent = r.Header.Get("User-Agent")
				seenAccept = r.Header.Get("Accept")
				headerMu.Unlock()
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{
						"tag_name":     "v0.3.0",
						"draft":        true,
						"prerelease":   false,
						"body":         "draft must be skipped",
						"published_at": "2026-11-23T18:00:00Z",
					},
					{
						"tag_name":     "nightly",
						"draft":        false,
						"prerelease":   true,
						"body":         "non-semver tag must be skipped",
						"published_at": "2026-11-23T17:00:00Z",
					},
					{
						"tag_name":     "v-invalid",
						"draft":        false,
						"prerelease":   true,
						"body":         "malformed v tag must be skipped",
						"published_at": "2026-11-23T16:30:00Z",
					},
					{
						"tag_name":     "v0.2.0-rc.1",
						"draft":        false,
						"prerelease":   true,
						"body":         "older rc",
						"published_at": "2026-11-23T14:00:00Z",
					},
					{
						"tag_name":     "v0.2.0-rc.2",
						"draft":        false,
						"prerelease":   true,
						"body":         fallbackBody,
						"published_at": "2026-11-23T15:30:00Z",
					},
				})
			case "/luolongfei/ColabOne/releases/download/v0.2.0-rc.2/update-windows-amd64.json":
				manifestRequested.Store(true)
				http.NotFound(w, r)
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		checker := &ColabOneReadOnlyFeedChecker{
			Repo:       COLABONE_GITHUB_REPO,
			Target:     "windows-amd64",
			GitHubBase: srv.URL,
			APIBase:    srv.URL,
			Client:     srv.Client(),
		}

		rel, err := checker.Check(context.Background(), true)
		if err != nil {
			t.Fatalf("expected prerelease fallback to succeed on manifest 404, got err=%v", err)
		}
		if !manifestRequested.Load() {
			t.Fatal("expected checker to attempt fetching update-windows-amd64.json before falling back")
		}
		if rel.Version != "0.2.0-rc.2" {
			t.Fatalf("Version = %q, want %q", rel.Version, "0.2.0-rc.2")
		}
		if rel.Notes != fallbackBody {
			t.Fatalf("Notes = %q, want fallback release body", rel.Notes)
		}
		wantDate, _ := time.Parse(time.RFC3339, "2026-11-23T15:30:00Z")
		if !rel.Date.Equal(wantDate) {
			t.Fatalf("Date = %v, want %v", rel.Date, wantDate)
		}
		headerMu.Lock()
		ua, acc := seenUserAgent, seenAccept
		headerMu.Unlock()
		if ua != colabOneFeedUserAgent {
			t.Fatalf("User-Agent = %q, want %q", ua, colabOneFeedUserAgent)
		}
		if acc != "application/vnd.github+json" {
			t.Fatalf("Accept = %q, want application/vnd.github+json", acc)
		}
	})

	t.Run("prerelease_no_published_semver_releases", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"tag_name": "v0.2.0", "draft": true},
				{"tag_name": "snapshot", "draft": false},
			})
		}))
		defer srv.Close()

		checker := &ColabOneReadOnlyFeedChecker{
			Repo:       COLABONE_GITHUB_REPO,
			Target:     "windows-amd64",
			GitHubBase: srv.URL,
			APIBase:    srv.URL,
			Client:     srv.Client(),
		}

		_, err := checker.Check(context.Background(), true)
		if err == nil || !strings.Contains(err.Error(), "no release is published") {
			t.Fatalf("expected no release is published error, got %v", err)
		}
	})
}

func TestManualCheckPromotesInFlightBackgroundCheck(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32

	backend := &fakeBackend{
		current: "0.1.6",
		canSelf: true,
		checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
			calls.Add(1)
			close(started)
			<-release
			return &fakeInstallable{version: "0.1.7", notes: "## Added\n- Promoted"}, nil
		},
	}

	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   backend,
		ConfigDir: t.TempDir(),
	})
	defer u.Close()

	// Pre-skip 0.1.7 so a background-only check would normally hide it;
	// promoting to manual must surface 0.1.7 to the user.
	if _, err := u.SkipVersion("0.1.7"); err != nil {
		t.Fatalf("SkipVersion failed: %v", err)
	}

	bgDone := make(chan ColabOneUpdateState, 1)
	go func() {
		st, _ := u.Check(context.Background(), false)
		bgDone <- st
	}()
	<-started

	manualDone := make(chan ColabOneUpdateState, 1)
	go func() {
		st, err := u.Check(context.Background(), true)
		if err != nil {
			t.Errorf("manual coalesced Check unexpected err: %v", err)
		}
		manualDone <- st
	}()

	// Wait briefly until manual caller promotes in-flight status to StatusChecking.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u.State().Status == StatusChecking {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := u.State().Status; got != StatusChecking {
		t.Fatalf("expected manual promotion to StatusChecking, got %q", got)
	}

	close(release)

	manualState := <-manualDone
	<-bgDone

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected single-flight backend call count 1, got %d", got)
	}
	if manualState.Status != StatusAvailable || manualState.AvailableVersion != "0.1.7" {
		t.Fatalf("promoted manual check should surface skipped version 0.1.7, got %+v", manualState)
	}
}

func TestChannelToggleDuringInFlightCheckDiscardsStaleResult(t *testing.T) {
	firstStarted := make(chan struct{})
	var calls atomic.Int32

	backend := &fakeBackend{
		current: "0.1.6",
		canSelf: true,
	}
	backend.checkSelfFn = func(ctx context.Context) (ColabOneUpdateInstallable, error) {
		n := calls.Add(1)
		if n == 1 {
			close(firstStarted)
			// Block until cancelled by SetPreferences channel toggle.
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if !backend.Prereleases() {
			t.Errorf("second check should have prereleases=true")
		}
		return &fakeInstallable{version: "0.2.0-rc.1", notes: "## Added\n- RC"}, nil
	}

	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   backend,
		ConfigDir: t.TempDir(),
	})
	defer u.Close()

	checkDone := make(chan ColabOneUpdateState, 1)
	go func() {
		st, err := u.Check(context.Background(), true)
		if err != nil {
			t.Errorf("re-run Check failed: %v", err)
		}
		checkDone <- st
	}()

	<-firstStarted

	// Also join a coalesced manual waiter before toggling channels so we verify
	// it stays blocked across the channel retry and receives the fresh prerelease.
	waiterDone := make(chan ColabOneUpdateState, 1)
	go func() {
		st, err := u.Check(context.Background(), true)
		if err != nil {
			t.Errorf("coalesced waiter Check failed: %v", err)
		}
		waiterDone <- st
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		u.mu.Lock()
		w := u.checkWaiters
		u.mu.Unlock()
		if w == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for coalesced waiter")
		}
		time.Sleep(time.Millisecond)
	}

	if _, changed, err := u.SetPreferences(true, true); err != nil || !changed {
		t.Fatalf("SetPreferences failed: changed=%v err=%v", changed, err)
	}

	st := <-checkDone
	waiterSt := <-waiterDone
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 backend calls (stale cancelled + fresh prerelease), got %d", got)
	}
	if st.Status != StatusAvailable || st.AvailableVersion != "0.2.0-rc.1" || !st.IncludePrerelease {
		t.Fatalf("expected prerelease 0.2.0-rc.1 after channel switch, got %+v", st)
	}
	if waiterSt.Status != StatusAvailable || waiterSt.AvailableVersion != "0.2.0-rc.1" || !waiterSt.IncludePrerelease {
		t.Fatalf("coalesced waiter should receive prerelease 0.2.0-rc.1 after channel switch, got %+v", waiterSt)
	}
}

func TestStartInstallDuringInFlightCheckNeverClobbersDownload(t *testing.T) {
	secondCheckStarted := make(chan struct{})
	releaseSecondCheck := make(chan struct{})
	installStarted := make(chan struct{})
	releaseInstall := make(chan struct{})
	var checkCalls atomic.Int32

	backend := &fakeBackend{
		current: "0.1.6",
		canSelf: true,
		checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
			n := checkCalls.Add(1)
			if n == 1 {
				return &fakeInstallable{
					version: "0.1.7",
					notes:   "## Added\n- Initial discovery",
					install: func(ictx context.Context, progress func(downloaded, total int64)) error {
						close(installStarted)
						progress(500, 1000)
						<-releaseInstall
						progress(1000, 1000)
						return nil
					},
				}, nil
			}
			close(secondCheckStarted)
			// Even if the backend ignores ctx cancellation and returns a later result,
			// Check must discard it once StartInstall has started.
			<-releaseSecondCheck
			return &fakeInstallable{version: "0.1.8", notes: "## Added\n- Stale"}, nil
		},
	}

	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   backend,
		ConfigDir: t.TempDir(),
	})
	defer u.Close()

	if st, err := u.Check(context.Background(), true); err != nil || st.Status != StatusAvailable {
		t.Fatalf("initial Check failed: st=%+v err=%v", st, err)
	}

	secondDone := make(chan ColabOneUpdateState, 1)
	go func() {
		st, err := u.Check(context.Background(), false)
		if err != nil {
			t.Errorf("background Check returned unexpected err: %v", err)
		}
		secondDone <- st
	}()
	<-secondCheckStarted

	if st, err := u.StartInstall(); err != nil || st.Status != StatusDownloading {
		t.Fatalf("StartInstall during in-flight Check failed: st=%+v err=%v", st, err)
	}
	<-installStarted

	close(releaseSecondCheck)
	secondSt := <-secondDone
	if secondSt.Status != StatusDownloading || secondSt.AvailableVersion != "0.1.7" {
		t.Fatalf("stale Check clobbered active download: got %+v", secondSt)
	}
	if cur := u.State(); cur.Status != StatusDownloading || cur.AvailableVersion != "0.1.7" {
		t.Fatalf("current state clobbered by stale Check: got %+v", cur)
	}

	close(releaseInstall)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u.State().Status == StatusReadyToRestart {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st := u.State(); st.Status != StatusReadyToRestart || st.AvailableVersion != "0.1.7" {
		t.Fatalf("expected ready_to_restart for 0.1.7, got %+v", st)
	}
}

func TestBackgroundFailurePreservesConcurrentPreferencesAndProgressMonotonic(t *testing.T) {
	// 1. Silent background failure must not clobber concurrent SetPreferences/SkipVersion.
	started := make(chan struct{})
	release := make(chan struct{})
	failBackend := &fakeBackend{
		current: "0.1.6",
		canSelf: true,
		checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
			close(started)
			<-release
			return nil, errors.New("dial tcp: connection refused")
		},
	}
	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   failBackend,
		ConfigDir: t.TempDir(),
	})
	defer u.Close()

	done := make(chan struct{})
	go func() {
		_, _ = u.Check(context.Background(), false)
		close(done)
	}()
	<-started
	if _, _, err := u.SetPreferences(false, false); err != nil {
		t.Fatalf("SetPreferences failed: %v", err)
	}
	if _, err := u.SkipVersion("0.1.8"); err != nil {
		t.Fatalf("SkipVersion failed: %v", err)
	}
	close(release)
	<-done

	after := u.State()
	if after.AutoCheck || after.SkippedVersion != "0.1.8" {
		t.Fatalf("silent background failure clobbered concurrent preferences: %+v", after)
	}

	// 2. Repeated progress(total, total) stays StatusInstalling; delta->full fallback
	// immediately returns to StatusDownloading without waiting 120ms.
	now := time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var transitions []string
	readyCh := make(chan struct{})

	progBackend := &fakeBackend{
		current: "0.1.6",
		canSelf: true,
		checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
			return &fakeInstallable{
				version: "0.1.7",
				install: func(ctx context.Context, progress func(downloaded, total int64)) error {
					progress(1000, 1000) // delta reaches 100% -> StatusInstalling
					progress(1000, 1000) // repeated 100% -> must stay StatusInstalling
					progress(100, 5000)  // delta fallback to full -> immediate StatusDownloading
					progress(5000, 5000) // full reaches 100% -> StatusInstalling
					return nil
				},
			}, nil
		},
	}
	u2 := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   progBackend,
		ConfigDir: t.TempDir(),
		Now:       func() time.Time { return now }, // frozen clock tests immediate phase emissions
		OnStateChange: func(st ColabOneUpdateState) {
			mu.Lock()
			transitions = append(transitions, st.Status)
			if st.Status == StatusReadyToRestart {
				select {
				case <-readyCh:
				default:
					close(readyCh)
				}
			}
			mu.Unlock()
		},
	})
	defer u2.Close()

	if _, err := u2.Check(context.Background(), true); err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	mu.Lock()
	transitions = nil
	mu.Unlock()
	if _, err := u2.StartInstall(); err != nil {
		t.Fatalf("StartInstall failed: %v", err)
	}
	<-readyCh

	mu.Lock()
	got := append([]string(nil), transitions...)
	mu.Unlock()
	// Expected sequence: downloading (start) -> installing (delta 100%) ->
	// downloading (fallback to full) -> installing (full 100%) -> ready_to_restart
	want := []string{
		StatusDownloading,
		StatusInstalling,
		StatusDownloading,
		StatusInstalling,
		StatusReadyToRestart,
	}
	if len(got) != len(want) {
		t.Fatalf("progress transitions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("progress transitions[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}

	// 3. Close() cleanly rejects subsequent StartInstall & TriggerAsyncCheck.
	u2.Close()
	if u2.TriggerAsyncCheck(true) {
		t.Fatal("TriggerAsyncCheck should return false after Close()")
	}
	if _, err := u2.StartInstall(); err == nil {
		t.Fatal("StartInstall should return error after Close()")
	}
}

func TestClassifyUpdateErrorIgnoresLoopbackPortDigits(t *testing.T) {
	err := errors.New("GET http://127.0.0.1:42912/update.json: 404 Not Found")
	if code, _ := ClassifyUpdateError(err); code != ErrCodeNotFound {
		t.Fatalf("port 42912 with 404 must classify as %q, got %q", ErrCodeNotFound, code)
	}
}

func TestSelfUpdateManifest404FallsBackCleanly(t *testing.T) {
	t.Run("up_to_date_when_latest_release_lacks_manifest", func(t *testing.T) {
		backend := &fakeBackend{
			current: "0.1.6",
			canSelf: true,
			checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
				return nil, errors.New("mygo: checking for updates: GET https://github.com/luolongfei/ColabOne/releases/latest/download/update-windows-amd64.json: 404 Not Found")
			},
			checkReadFn: func(ctx context.Context, includePrerelease bool) (*ColabOneReadOnlyRelease, error) {
				return &ColabOneReadOnlyRelease{Version: "0.1.6"}, nil
			},
		}
		u := NewColabOneUpdater(ColabOneUpdaterConfig{
			Backend:   backend,
			ConfigDir: t.TempDir(),
		})
		defer u.Close()

		st, err := u.Check(context.Background(), true)
		if err != nil {
			t.Fatalf("Check returned unexpected error: %v", err)
		}
		if st.Status != StatusUpToDate {
			t.Fatalf("Status = %q, want %q (state=%+v)", st.Status, StatusUpToDate, st)
		}
		if !st.CanSelfUpdate || st.DisabledReason != DisabledReasonNone {
			t.Fatalf("up-to-date fallback must preserve CanSelfUpdate=true and empty DisabledReason: %+v", st)
		}
	})

	t.Run("newer_release_without_manifest_offers_manual_download", func(t *testing.T) {
		backend := &fakeBackend{
			current: "0.1.6",
			canSelf: true,
			checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
				return nil, errors.New("mygo: checking for updates: GET https://github.com/luolongfei/ColabOne/releases/latest/download/update-windows-amd64.json: 404 Not Found")
			},
			checkReadFn: func(ctx context.Context, includePrerelease bool) (*ColabOneReadOnlyRelease, error) {
				return &ColabOneReadOnlyRelease{
					Version: "0.1.7",
					Notes:   "## 更新\n- 仅手动安装包\n\n---\n\n## Changes\n- Manual package only",
					Date:    time.Date(2026, 11, 24, 2, 0, 0, 0, time.UTC),
				}, nil
			},
		}
		u := NewColabOneUpdater(ColabOneUpdaterConfig{
			Backend:   backend,
			ConfigDir: t.TempDir(),
		})
		defer u.Close()

		st, err := u.Check(context.Background(), true)
		if err != nil {
			t.Fatalf("Check returned unexpected error: %v", err)
		}
		if st.Status != StatusAvailable || st.AvailableVersion != "0.1.7" {
			t.Fatalf("expected StatusAvailable for 0.1.7, got %+v", st)
		}
		if st.CanSelfUpdate || st.DisabledReason != DisabledReasonManifestMissing {
			t.Fatalf("expected CanSelfUpdate=false & DisabledReason=%q, got %+v", DisabledReasonManifestMissing, st)
		}
		if st.NotesZh == "" || st.NotesEn == "" {
			t.Fatalf("expected bilingual notes from fallback release: %+v", st)
		}
	})
}
