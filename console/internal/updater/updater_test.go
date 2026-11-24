package updater

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeInstallable struct {
	version string
	notes   string
	date    time.Time
	install func(ctx context.Context, progress func(downloaded, total int64)) error
}

func (f *fakeInstallable) Version() string { return f.version }
func (f *fakeInstallable) Notes() string   { return f.notes }
func (f *fakeInstallable) Date() time.Time { return f.date }
func (f *fakeInstallable) Install(ctx context.Context, progress func(downloaded, total int64)) error {
	if f.install != nil {
		return f.install(ctx, progress)
	}
	return nil
}

type fakeBackend struct {
	mu            sync.Mutex
	current       string
	dev           bool
	canSelf       bool
	prereleases   bool
	checkSelfFn   func(ctx context.Context) (ColabOneUpdateInstallable, error)
	checkReadFn   func(ctx context.Context, includePrerelease bool) (*ColabOneReadOnlyRelease, error)
	relaunchCount int
	relaunchErr   error
}

func (b *fakeBackend) CurrentVersion() string { return b.current }
func (b *fakeBackend) IsDevBuild() bool       { return b.dev }
func (b *fakeBackend) CanSelfUpdate() bool    { return b.canSelf }
func (b *fakeBackend) SetPrereleases(on bool) {
	b.mu.Lock()
	b.prereleases = on
	b.mu.Unlock()
}
func (b *fakeBackend) Prereleases() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.prereleases
}
func (b *fakeBackend) CheckSelfUpdate(ctx context.Context) (ColabOneUpdateInstallable, error) {
	if b.checkSelfFn != nil {
		return b.checkSelfFn(ctx)
	}
	return nil, nil
}
func (b *fakeBackend) CheckReadOnly(ctx context.Context, includePrerelease bool) (*ColabOneReadOnlyRelease, error) {
	if b.checkReadFn != nil {
		return b.checkReadFn(ctx, includePrerelease)
	}
	return nil, nil
}
func (b *fakeBackend) Relaunch() error {
	b.mu.Lock()
	b.relaunchCount++
	err := b.relaunchErr
	b.mu.Unlock()
	return err
}

func TestCompareVersionsAndPrerelease(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.6", "0.1.7", -1},
		{"v0.1.10", "v0.1.9", 1},
		{"0.2.0-rc.1", "0.2.0", -1},
		{"0.2.0-rc.2", "0.2.0-rc.10", -1},
		{"0.2.0-beta.1", "0.2.0-rc.1", -1},
		{"0.1.6", "v0.1.6", 0},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	if !IsPrereleaseVersion("v0.2.0-rc.1") {
		t.Fatal("expected v0.2.0-rc.1 to be prerelease")
	}
	if IsPrereleaseVersion("v0.2.0") {
		t.Fatal("expected v0.2.0 not to be prerelease")
	}
}

func TestParseBilingualNotes(t *testing.T) {
	raw := "ColabOne v0.1.7\r\n\r\n## 新增功能\r\n- 支持客户端一键更新\r\n\r\n---\r\n\r\n## New Features\r\n- Add one-click in-app auto-update\r\n"
	parsed := ParseBilingualNotes(raw)
	if parsed.Zh != "## 新增功能\n- 支持客户端一键更新" {
		t.Fatalf("unexpected zh notes: %q", parsed.Zh)
	}
	if parsed.En != "## New Features\n- Add one-click in-app auto-update" {
		t.Fatalf("unexpected en notes: %q", parsed.En)
	}
	if parsed.Raw == "" {
		t.Fatal("expected non-empty raw notes")
	}

	single := ParseBilingualNotes("## Changes\n- Single language notes")
	if single.Zh != single.En || single.Zh != "## Changes\n- Single language notes" {
		t.Fatalf("expected single-language fallback to populate both zh and en: %+v", single)
	}
}

func TestClassifyUpdateError(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{context.Canceled, ErrCodeCancelled},
		{errors.New("the update is not signed with the app's key"), ErrCodeSignature},
		{errors.New("GET https://api.github.com/...: 403 rate limit exceeded"), ErrCodeRateLimited},
		{errors.New("cannot write next to the app: access is denied"), ErrCodePermission},
		{errors.New("mygo: checking for updates: GET https://github.com/luolongfei/ColabOne/releases/latest/download/update-windows-amd64.json: 404 Not Found"), ErrCodeNotFound},
		{errors.New("proxyconnect tcp 127.0.0.1:7890: connection refused"), ErrCodeProxy},
		{errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority"), ErrCodeTLS},
		{errors.New("the download stalled: the connection sent nothing"), ErrCodeTimeout},
		{errors.New("dial tcp: lookup github.com: no such host"), ErrCodeNetwork},
		{errors.New("unexpected failure"), ErrCodeUnknown},
	}
	for _, tc := range tests {
		code, msg := ClassifyUpdateError(tc.err)
		if code != tc.want || msg == "" {
			t.Errorf("ClassifyUpdateError(%v) = (%q, %q), want code %q", tc.err, code, msg, tc.want)
		}
	}

	// Ensure secrets, URL userinfo, and query strings are redacted in sanitized error detail.
	_, detail := ClassifyUpdateError(errors.New("mygo: checking for updates: GET https://user:pass@github.com/repo?token=secret123: proxyconnect Bearer ghp_abc123XYZ"))
	for _, leaked := range []string{"user:pass", "secret123", "ghp_abc123XYZ", "mygo: checking for updates:"} {
		if contains := filepath.Base(detail) == leaked || len(detail) > 0 && stringContains(detail, leaked); contains {
			t.Fatalf("sanitized detail leaked %q: %q", leaked, detail)
		}
	}
}

func stringContains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexString(s, sub) >= 0
}

func indexString(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestUpdaterFullLifecycleAndRelaunch(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	advance := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}

	installDone := make(chan struct{})
	var seenStates []string
	var stateMu sync.Mutex

	backend := &fakeBackend{
		current: "0.1.6",
		canSelf: true,
		checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
			return &fakeInstallable{
				version: "0.1.7",
				notes:   "ColabOne v0.1.7\n\n## 新增功能\n- 自动更新\n\n---\n\n## New Features\n- Auto-update\n",
				date:    now,
				install: func(ctx context.Context, progress func(downloaded, total int64)) error {
					progress(200, 1000)
					advance(250 * time.Millisecond)
					// Simulate delta fallback reset to full archive.
					progress(100, 5000)
					advance(250 * time.Millisecond)
					progress(5000, 5000)
					return nil
				},
			}, nil
		},
	}

	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   backend,
		ConfigDir: dir,
		Now: func() time.Time {
			nowMu.Lock()
			defer nowMu.Unlock()
			return now
		},
		OnStateChange: func(st ColabOneUpdateState) {
			stateMu.Lock()
			seenStates = append(seenStates, st.Status)
			if st.Status == StatusReadyToRestart {
				select {
				case <-installDone:
				default:
					close(installDone)
				}
			}
			stateMu.Unlock()
		},
	})
	defer u.Close()

	st, err := u.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if st.Status != StatusAvailable || st.AvailableVersion != "0.1.7" {
		t.Fatalf("unexpected state after check: %+v", st)
	}
	if st.NotesZh != "## 新增功能\n- 自动更新" || st.NotesEn != "## New Features\n- Auto-update" {
		t.Fatalf("unexpected parsed notes: zh=%q en=%q", st.NotesZh, st.NotesEn)
	}
	if st.ReleaseURL != "https://github.com/luolongfei/ColabOne/releases/tag/v0.1.7" {
		t.Fatalf("unexpected release URL: %q", st.ReleaseURL)
	}

	if err := u.Relaunch(); err == nil {
		t.Fatal("expected Relaunch before install to fail")
	}

	if _, err := u.StartInstall(); err != nil {
		t.Fatalf("StartInstall failed: %v", err)
	}

	select {
	case <-installDone:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for install completion")
	}

	final := u.State()
	if final.Status != StatusReadyToRestart || final.ProgressPercent != 100 {
		t.Fatalf("unexpected final state: %+v", final)
	}
	if err := u.Relaunch(); err != nil {
		t.Fatalf("Relaunch failed: %v", err)
	}
	if backend.relaunchCount != 1 {
		t.Fatalf("expected 1 relaunch call, got %d", backend.relaunchCount)
	}

	stateMu.Lock()
	hasDownloading := false
	hasInstalling := false
	for _, s := range seenStates {
		if s == StatusDownloading {
			hasDownloading = true
		}
		if s == StatusInstalling {
			hasInstalling = true
		}
	}
	stateMu.Unlock()
	if !hasDownloading || !hasInstalling {
		t.Fatalf("expected downloading & installing transitions, saw %v", seenStates)
	}
}

func TestUpdaterCancelInstallAndSkipVersionAndPersistence(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	cancelledDone := make(chan struct{})

	backend := &fakeBackend{
		current: "0.1.6",
		canSelf: true,
		checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
			return &fakeInstallable{
				version: "0.1.7",
				notes:   "notes",
				install: func(ctx context.Context, progress func(downloaded, total int64)) error {
					close(started)
					<-ctx.Done()
					return ctx.Err()
				},
			}, nil
		},
	}

	var cancelledSeen atomic.Bool
	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   backend,
		ConfigDir: dir,
		OnStateChange: func(st ColabOneUpdateState) {
			if cancelledSeen.Load() && st.Status == StatusAvailable {
				select {
				case <-cancelledDone:
				default:
					close(cancelledDone)
				}
			}
		},
	})
	defer u.Close()

	if _, err := u.Check(context.Background(), true); err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if _, err := u.StartInstall(); err != nil {
		t.Fatalf("StartInstall failed: %v", err)
	}
	<-started
	cancelledSeen.Store(true)
	u.CancelInstall()

	select {
	case <-cancelledDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancel to return to available")
	}

	// Skip version 0.1.7: background check ignores it, manual check still shows it.
	skipped, err := u.SkipVersion("0.1.7")
	if err != nil {
		t.Fatalf("SkipVersion failed: %v", err)
	}
	if skipped.Status != StatusUpToDate || skipped.SkippedVersion != "0.1.7" {
		t.Fatalf("unexpected state after SkipVersion: %+v", skipped)
	}

	bg, err := u.Check(context.Background(), false)
	if err != nil || bg.Status != StatusUpToDate {
		t.Fatalf("background check should stay up_to_date for skipped version, got %+v err=%v", bg, err)
	}

	manual, err := u.Check(context.Background(), true)
	if err != nil || manual.Status != StatusAvailable || manual.AvailableVersion != "0.1.7" {
		t.Fatalf("manual check should still surface skipped version, got %+v err=%v", manual, err)
	}

	// Preferences persist across restarts.
	st, changed, err := u.SetPreferences(false, true)
	if err != nil || !changed || st.AutoCheck || !st.IncludePrerelease {
		t.Fatalf("SetPreferences mismatch: st=%+v changed=%v err=%v", st, changed, err)
	}
	if !backend.Prereleases() {
		t.Fatal("expected backend SetPrereleases(true) to be called")
	}

	u2 := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   backend,
		ConfigDir: dir,
	})
	defer u2.Close()
	loaded := u2.State()
	if loaded.AutoCheck || !loaded.IncludePrerelease || loaded.SkippedVersion != "0.1.7" {
		t.Fatalf("persisted preferences did not reload cleanly from %s: %+v", filepath.Join(dir, COLABONE_UPDATE_PREF_FILE), loaded)
	}
}

func TestUpdaterSilentBackgroundFailureAndDevBuild(t *testing.T) {
	devBackend := &fakeBackend{current: "0.1.6", dev: true, canSelf: false}
	devUpdater := NewColabOneUpdater(ColabOneUpdaterConfig{Backend: devBackend})
	defer devUpdater.Close()
	if st := devUpdater.State(); st.Status != StatusDisabled || st.DisabledReason != DisabledReasonDevBuild {
		t.Fatalf("expected disabled dev_build state, got %+v", st)
	}

	failBackend := &fakeBackend{
		current: "0.1.6",
		canSelf: true,
		checkSelfFn: func(ctx context.Context) (ColabOneUpdateInstallable, error) {
			return nil, errors.New("dial tcp: connection refused")
		},
	}
	u := NewColabOneUpdater(ColabOneUpdaterConfig{Backend: failBackend})
	defer u.Close()

	bgState, err := u.Check(context.Background(), false)
	if err == nil {
		t.Fatal("expected error from failing check")
	}
	if bgState.Status != StatusIdle {
		t.Fatalf("silent background failure should preserve idle status, got %+v", bgState)
	}

	manualState, err := u.Check(context.Background(), true)
	if err == nil || manualState.Status != StatusError || manualState.ErrorCode != ErrCodeNetwork {
		t.Fatalf("manual failure should surface network error, got %+v err=%v", manualState, err)
	}
}

func TestReadOnlyFeedCheckerStableAndPrerelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/luolongfei/ColabOne/releases/latest/download/update-windows-amd64.json":
			_ = json.NewEncoder(w).Encode(ColabOneFeedManifest{
				Version: "0.1.7",
				Notes:   "ColabOne v0.1.7\n\n## 修改点\n- 正式版\n\n---\n\n## Changes\n- Stable\n",
				Date:    "2026-11-23T12:00:00Z",
			})
		case "/repos/luolongfei/ColabOne/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"tag_name":     "v0.2.0-rc.2",
					"draft":        false,
					"prerelease":   true,
					"body":         "fallback rc notes",
					"published_at": "2026-11-23T15:00:00Z",
				},
				{
					"tag_name":     "v0.2.0-rc.1",
					"draft":        false,
					"prerelease":   true,
					"body":         "older rc",
					"published_at": "2026-11-23T14:00:00Z",
				},
				{
					"tag_name":     "v0.3.0",
					"draft":        true,
					"prerelease":   false,
					"body":         "draft must be ignored",
					"published_at": "2026-11-23T16:00:00Z",
				},
			})
		case "/luolongfei/ColabOne/releases/download/v0.2.0-rc.2/update-windows-amd64.json":
			_ = json.NewEncoder(w).Encode(ColabOneFeedManifest{
				Version: "0.2.0-rc.2",
				Notes:   "ColabOne v0.2.0-rc.2\n\n## 新增功能\n- 预发布测试\n\n---\n\n## New Features\n- Prerelease preview\n",
				Date:    "2026-11-23T15:00:00Z",
			})
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

	stable, err := checker.Check(context.Background(), false)
	if err != nil || stable == nil || stable.Version != "0.1.7" {
		t.Fatalf("unexpected stable result: %+v err=%v", stable, err)
	}

	pre, err := checker.Check(context.Background(), true)
	if err != nil || pre == nil || pre.Version != "0.2.0-rc.2" {
		t.Fatalf("unexpected prerelease result: %+v err=%v", pre, err)
	}

	// Verify read-only updater surfaces update with CanSelfUpdate=false.
	roBackend := &fakeBackend{
		current:     "0.1.6",
		dev:         false,
		canSelf:     false,
		checkReadFn: checker.Check,
	}
	u := NewColabOneUpdater(ColabOneUpdaterConfig{Backend: roBackend})
	defer u.Close()

	st, err := u.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("read-only check failed: %v", err)
	}
	if st.Status != StatusAvailable || st.CanSelfUpdate || st.DisabledReason != DisabledReasonReadOnly || st.AvailableVersion != "0.1.7" {
		t.Fatalf("unexpected read-only state: %+v", st)
	}
	if _, err := u.StartInstall(); err == nil {
		t.Fatal("expected StartInstall on read-only install to return error")
	}
}
