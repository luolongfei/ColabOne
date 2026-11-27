package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func buildTestTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		data := []byte(content)
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(data)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("write tar body %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestListRollbackReleasesFiltersSortsCapsAndChecksManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/test/repo/releases" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"v0.2.25","draft":false,"prerelease":false,"published_at":"2026-11-24T00:00:00Z","assets":[{"name":"update-windows-amd64.json"}]},
			{"tag_name":"v0.2.24","draft":false,"prerelease":false,"published_at":"2026-11-22T00:00:00Z","assets":[{"name":"update-windows-amd64.json"}]},
			{"tag_name":"v0.2.23-rc.1","draft":false,"prerelease":true,"published_at":"2026-11-19T00:00:00Z","assets":[{"name":"update-windows-amd64.json"}]},
			{"tag_name":"v0.2.22","draft":false,"prerelease":false,"published_at":"2026-11-17T00:00:00Z","assets":[{"name":"update-windows-amd64.json"}]},
			{"tag_name":"v0.2.23","draft":false,"prerelease":false,"published_at":"2026-11-18T00:00:00Z","assets":[{"name":"update-windows-amd64.json"}]},
			{"tag_name":"v0.2.21","draft":false,"prerelease":false,"published_at":"2026-11-11T00:00:00Z","assets":[{"name":"ColabOne-0.2.21-setup.exe"}]},
			{"tag_name":"v0.2.20","draft":false,"prerelease":false,"published_at":"2026-11-05T00:00:00Z","assets":[{"name":"update-windows-amd64.json"}]},
			{"tag_name":"v0.2.19","draft":true,"prerelease":false,"published_at":"2026-11-01T00:00:00Z","assets":[{"name":"update-windows-amd64.json"}]}
		]`))
	}))
	defer srv.Close()

	checker := &ColabOneReadOnlyFeedChecker{
		Repo:       "test/repo",
		Target:     "windows-amd64",
		GitHubBase: srv.URL,
		APIBase:    srv.URL,
		Client:     srv.Client(),
	}

	got, err := checker.ListRollbackReleases(context.Background(), false, "0.2.24", true, 3)
	if err != nil {
		t.Fatalf("ListRollbackReleases failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3 (%+v)", len(got), got)
	}
	if got[0].Version != "0.2.23" || !got[0].CanRollback || got[0].ReleaseDate != "2026-11-18T00:00:00Z" {
		t.Fatalf("unexpected got[0]: %+v", got[0])
	}
	if got[1].Version != "0.2.22" || !got[1].CanRollback {
		t.Fatalf("unexpected got[1]: %+v", got[1])
	}
	if got[2].Version != "0.2.21" || got[2].CanRollback {
		t.Fatalf("expected v0.2.21 without manifest to have CanRollback=false: %+v", got[2])
	}

	// Read-only install disables one-click rollback even when manifest exists.
	roGot, err := checker.ListRollbackReleases(context.Background(), false, "0.2.24", false, 3)
	if err != nil {
		t.Fatalf("ListRollbackReleases read-only failed: %v", err)
	}
	for _, item := range roGot {
		if item.CanRollback {
			t.Fatalf("expected CanRollback=false on read-only install: %+v", item)
		}
	}

	// Including prereleases exposes v0.2.23-rc.1 in proper SemVer order.
	preGot, err := checker.ListRollbackReleases(context.Background(), true, "0.2.24", true, 3)
	if err != nil {
		t.Fatalf("ListRollbackReleases prerelease failed: %v", err)
	}
	if len(preGot) != 3 || preGot[0].Version != "0.2.23" || preGot[1].Version != "0.2.23-rc.1" || !preGot[1].IsPrerelease {
		t.Fatalf("unexpected prerelease order: %+v", preGot)
	}
}

func TestListRollbackReleasesFallsBackToManifestPreviousOnRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/repo/releases":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		case "/test/repo/releases/latest/download/update-windows-amd64.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"version": "0.1.13",
				"url": "https://example.invalid/0.1.13.tar.gz",
				"size": 100,
				"signature": "sig",
				"previous": [
					{"version":"0.1.12","url":"https://example.invalid/0.1.12.tar.gz","size":90,"signature":"sig12"},
					{"version":"0.1.11","url":"https://example.invalid/0.1.11.tar.gz","size":80,"signature":"sig11"}
				]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	checker := &ColabOneReadOnlyFeedChecker{
		Repo:       "test/repo",
		Target:     "windows-amd64",
		GitHubBase: srv.URL,
		APIBase:    srv.URL,
		Client:     srv.Client(),
	}

	got, err := checker.ListRollbackReleases(context.Background(), false, "0.1.13", true, 3)
	if err != nil {
		t.Fatalf("expected fallback to succeed on rate limit: %v", err)
	}
	if len(got) != 2 || got[0].Version != "0.1.12" || !got[0].CanRollback || got[1].Version != "0.1.11" {
		t.Fatalf("unexpected fallback rollback list: %+v", got)
	}
}

func TestRollbackInstallerVerifiesEd25519SignatureAndSwapsFiles(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	archiveBytes := buildTestTarGz(t, map[string]string{
		"ColabOne.exe": "binary-v0.1.12",
		"README.md":    "readme-v0.1.12",
	})
	sum := sha256.Sum256(archiveBytes)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:]))

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/test/repo/releases/download/v0.1.12/update-windows-amd64.json":
			_ = json.NewEncoder(w).Encode(ColabOneSignedManifest{
				Version:   "0.1.12",
				Date:      "2026-11-23T00:00:00Z",
				URL:       srv.URL + "/downloads/colabone-0.1.12.tar.gz",
				Size:      int64(len(archiveBytes)),
				Signature: sig,
			})
		case "/downloads/colabone-0.1.12.tar.gz":
			_, _ = w.Write(archiveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	parent := t.TempDir()
	installDir := filepath.Join(parent, "ColabOne")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "ColabOne.exe"), []byte("binary-v0.1.13"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "obsolete.dat"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "user-portable.txt"), []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, colabOneMyGoFileList), []byte("ColabOne.exe\nobsolete.dat\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	installer := &ColabOneRollbackInstaller{
		Repo:              "test/repo",
		Target:            "windows-amd64",
		PublicKey:         base64.StdEncoding.EncodeToString(pub),
		GitHubBase:        srv.URL,
		Client:            srv.Client(),
		TargetDir:         installDir,
		StartExe:          filepath.Join(installDir, "ColabOne.exe"),
		PlatformOS:        "linux",
		allowLoopbackHTTP: true,
	}

	handle, err := installer.PrepareRollback(context.Background(), "0.1.12")
	if err != nil {
		t.Fatalf("PrepareRollback failed: %v", err)
	}
	var lastDownloaded, lastTotal int64
	if err := handle.Install(context.Background(), func(downloaded, total int64) {
		lastDownloaded = downloaded
		lastTotal = total
	}); err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if lastDownloaded != int64(len(archiveBytes)) || lastTotal != int64(len(archiveBytes)) {
		t.Fatalf("progress = (%d, %d), want (%d, %d)", lastDownloaded, lastTotal, len(archiveBytes), len(archiveBytes))
	}

	gotBin, err := os.ReadFile(filepath.Join(installDir, "ColabOne.exe"))
	if err != nil || string(gotBin) != "binary-v0.1.12" {
		t.Fatalf("ColabOne.exe = %q, err=%v", string(gotBin), err)
	}
	gotReadme, err := os.ReadFile(filepath.Join(installDir, "README.md"))
	if err != nil || string(gotReadme) != "readme-v0.1.12" {
		t.Fatalf("README.md = %q, err=%v", string(gotReadme), err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "obsolete.dat")); !os.IsNotExist(err) {
		t.Fatalf("expected obsolete.dat to be removed, err=%v", err)
	}
	gotUser, err := os.ReadFile(filepath.Join(installDir, "user-portable.txt"))
	if err != nil || string(gotUser) != "keep-me" {
		t.Fatalf("expected untracked user file to survive rollback: %q, err=%v", string(gotUser), err)
	}
}

func TestRollbackInstallerRejectsTamperedSignatureAndPathTraversal(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	goodArchive := buildTestTarGz(t, map[string]string{"ColabOne.exe": "good"})
	evilArchive := buildTestTarGz(t, map[string]string{"../escape.txt": "pwned"})
	evilSum := sha256.Sum256(evilArchive)
	evilSig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, evilSum[:]))

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/test/repo/releases/download/v0.1.11/update-windows-amd64.json":
			_ = json.NewEncoder(w).Encode(ColabOneSignedManifest{
				Version:   "0.1.11",
				URL:       srv.URL + "/good.tar.gz",
				Size:      int64(len(goodArchive)),
				Signature: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, ed25519.SignatureSize)),
			})
		case "/test/repo/releases/download/v0.1.10/update-windows-amd64.json":
			_ = json.NewEncoder(w).Encode(ColabOneSignedManifest{
				Version:   "0.1.10",
				URL:       srv.URL + "/evil.tar.gz",
				Size:      int64(len(evilArchive)),
				Signature: evilSig,
			})
		case "/test/repo/releases/download/v0.1.9/update-windows-amd64.json":
			_ = json.NewEncoder(w).Encode(ColabOneSignedManifest{
				Version:   "0.1.8", // mismatch!
				URL:       srv.URL + "/good.tar.gz",
				Size:      int64(len(goodArchive)),
				Signature: evilSig,
			})
		case "/good.tar.gz":
			_, _ = w.Write(goodArchive)
		case "/evil.tar.gz":
			_, _ = w.Write(evilArchive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	parent := t.TempDir()
	installDir := filepath.Join(parent, "ColabOne")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "ColabOne.exe"), []byte("orig"), 0o755); err != nil {
		t.Fatal(err)
	}

	installer := &ColabOneRollbackInstaller{
		Repo:              "test/repo",
		Target:            "windows-amd64",
		PublicKey:         base64.StdEncoding.EncodeToString(pub),
		GitHubBase:        srv.URL,
		Client:            srv.Client(),
		TargetDir:         installDir,
		StartExe:          filepath.Join(installDir, "ColabOne.exe"),
		PlatformOS:        "linux",
		allowLoopbackHTTP: true,
	}

	// 1. Bad signature must fail with ErrCodeSignature.
	hBadSig, err := installer.PrepareRollback(context.Background(), "0.1.11")
	if err != nil {
		t.Fatalf("PrepareRollback 0.1.11: %v", err)
	}
	if err := hBadSig.Install(context.Background(), nil); err == nil {
		t.Fatal("expected tampered signature to fail")
	} else if code, _ := ClassifyUpdateError(err); code != ErrCodeSignature {
		t.Fatalf("error code = %q, want %q (err=%v)", code, ErrCodeSignature, err)
	}

	// 2. Path traversal in archive must be rejected.
	hEvil, err := installer.PrepareRollback(context.Background(), "0.1.10")
	if err != nil {
		t.Fatalf("PrepareRollback 0.1.10: %v", err)
	}
	if err := hEvil.Install(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Fatalf("expected invalid path error, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("escape.txt should not have been created")
	}

	// 3. Manifest version mismatch must be rejected during PrepareRollback.
	if _, err := installer.PrepareRollback(context.Background(), "0.1.9"); err == nil {
		t.Fatal("expected version mismatch manifest to fail")
	}

	// Original file untouched.
	gotBin, _ := os.ReadFile(filepath.Join(installDir, "ColabOne.exe"))
	if string(gotBin) != "orig" {
		t.Fatalf("original binary modified: %q", string(gotBin))
	}
}

type fakeRollbackBackend struct {
	mu                sync.Mutex
	current           string
	canSelf           bool
	listCalls         int
	versions          []ColabOneRollbackVersion
	prepareFn         func(ctx context.Context, version string) (ColabOneUpdateInstallable, error)
	relaunchedNormal  bool
	relaunchedMacRoll bool
}

func (b *fakeRollbackBackend) CurrentVersion() string { return b.current }
func (b *fakeRollbackBackend) IsDevBuild() bool       { return false }
func (b *fakeRollbackBackend) CanSelfUpdate() bool    { return b.canSelf }
func (b *fakeRollbackBackend) SetPrereleases(bool)    {}
func (b *fakeRollbackBackend) CheckSelfUpdate(context.Context) (ColabOneUpdateInstallable, error) {
	return nil, nil
}
func (b *fakeRollbackBackend) CheckReadOnly(context.Context, bool) (*ColabOneReadOnlyRelease, error) {
	return nil, nil
}
func (b *fakeRollbackBackend) Relaunch() error {
	b.mu.Lock()
	b.relaunchedNormal = true
	b.mu.Unlock()
	return nil
}
func (b *fakeRollbackBackend) ListRollbackVersions(context.Context, bool, string, bool, int) ([]ColabOneRollbackVersion, error) {
	b.mu.Lock()
	b.listCalls++
	out := make([]ColabOneRollbackVersion, len(b.versions))
	copy(out, b.versions)
	b.mu.Unlock()
	return out, nil
}
func (b *fakeRollbackBackend) PrepareRollback(ctx context.Context, version string) (ColabOneUpdateInstallable, error) {
	if b.prepareFn != nil {
		return b.prepareFn(ctx, version)
	}
	return &fakeInstallable{version: version}, nil
}
func (b *fakeRollbackBackend) RelaunchRollback() (bool, error) {
	b.mu.Lock()
	b.relaunchedMacRoll = true
	b.mu.Unlock()
	return true, nil
}

type blockingInstallable struct {
	version string
	started chan struct{}
}

func (b *blockingInstallable) Version() string { return b.version }
func (b *blockingInstallable) Notes() string   { return "" }
func (b *blockingInstallable) Date() time.Time { return time.Time{} }
func (b *blockingInstallable) Install(ctx context.Context, progress func(downloaded, total int64)) error {
	if progress != nil {
		progress(10, 100)
	}
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestColabOneUpdaterRollbackLifecycleCacheAndCancelRestore(t *testing.T) {
	started := make(chan struct{})
	cancelFirst := true
	backend := &fakeRollbackBackend{
		current: "0.1.13",
		canSelf: true,
		versions: []ColabOneRollbackVersion{
			{Version: "0.1.12", Tag: "v0.1.12", ReleaseDate: "2026-11-23T00:00:00Z", CanRollback: true},
			{Version: "0.1.11", Tag: "v0.1.11", ReleaseDate: "2026-11-22T00:00:00Z", CanRollback: true},
		},
	}
	backend.prepareFn = func(_ context.Context, version string) (ColabOneUpdateInstallable, error) {
		if cancelFirst {
			cancelFirst = false
			return &blockingInstallable{version: version, started: started}, nil
		}
		return &fakeInstallable{version: version}, nil
	}

	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   backend,
		ConfigDir: t.TempDir(),
		Now:       func() time.Time { return now },
	})
	defer u.Close()

	// Seed state as up_to_date.
	if st, err := u.Check(context.Background(), true); err != nil || st.Status != StatusUpToDate {
		t.Fatalf("Check: st=%+v err=%v", st, err)
	}

	// ListRollbackVersions caches results until forceRefresh or TTL expiry.
	list1, err := u.ListRollbackVersions(context.Background(), false)
	if err != nil || len(list1) != 2 {
		t.Fatalf("list1=%+v err=%v", list1, err)
	}
	list2, err := u.ListRollbackVersions(context.Background(), false)
	if err != nil || len(list2) != 2 || backend.listCalls != 1 {
		t.Fatalf("expected cached list, calls=%d", backend.listCalls)
	}
	if _, err := u.ListRollbackVersions(context.Background(), true); err != nil || backend.listCalls != 2 {
		t.Fatalf("expected forceRefresh to re-fetch, calls=%d err=%v", backend.listCalls, err)
	}

	// Reject version >= current.
	if _, err := u.StartRollback("0.1.13"); err == nil {
		t.Fatal("expected rollback to current version to be rejected")
	}
	if _, err := u.StartRollback("0.1.14"); err == nil {
		t.Fatal("expected rollback to newer version to be rejected")
	}

	// Start rollback, then cancel mid-download -> restores cleanly to up_to_date (not available!).
	if st, err := u.StartRollback("0.1.12"); err != nil || st.Status != StatusDownloading || !st.IsRollback {
		t.Fatalf("StartRollback: st=%+v err=%v", st, err)
	}
	<-started
	canceledSt := u.CancelInstall()
	if canceledSt.Status != StatusUpToDate || canceledSt.IsRollback || canceledSt.AvailableVersion != "" {
		t.Fatalf("expected cancel during rollback to restore up_to_date cleanly, got: %+v", canceledSt)
	}

	// Second rollback succeeds and transitions to ready_to_restart with IsRollback=true.
	if _, err := u.StartRollback("0.1.12"); err != nil {
		t.Fatalf("second StartRollback failed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u.State().Status == StatusReadyToRestart {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ready := u.State()
	if ready.Status != StatusReadyToRestart || !ready.IsRollback || ready.AvailableVersion != "0.1.12" {
		t.Fatalf("unexpected ready rollback state: %+v", ready)
	}
	if err := u.Relaunch(); err != nil {
		t.Fatalf("Relaunch rollback failed: %v", err)
	}
	if !backend.relaunchedMacRoll {
		t.Fatal("expected RelaunchRollback hook to be invoked for rollback restart")
	}
}

func TestRollbackStrictSemVerBackslashSymlinkEscapeRetryStallAndChannelReset(t *testing.T) {
	// 1. Strict SemVer validation rejects path/query injection strings.
	for _, bad := range []string{"", "0.1.12/../evil", "0.1.12?x=1", "0.1.12#frag", "v01.2.3", "not-a-version"} {
		if IsValidSemVer(bad) {
			t.Fatalf("expected IsValidSemVer(%q) = false", bad)
		}
	}
	for _, good := range []string{"0.1.12", "v0.2.23", "1.0.0-rc.1", "v2.3.4-beta.2"} {
		if !IsValidSemVer(good) {
			t.Fatalf("expected IsValidSemVer(%q) = true", good)
		}
	}

	// 2. Archive extraction rejects Windows-style backslash traversal and escaping symlinks.
	backslashTar := buildTestTarGz(t, map[string]string{`sub\..\..\escape.exe`: "bad"})
	if err := extractColabOneSignedArchive(bytes.NewReader(backslashTar), t.TempDir()); err == nil {
		t.Fatal("expected backslash traversal entry to be rejected")
	}

	var symBuf bytes.Buffer
	gz := gzip.NewWriter(&symBuf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: ".."})
	_ = tw.Close()
	_ = gz.Close()
	if err := extractColabOneSignedArchive(bytes.NewReader(symBuf.Bytes()), t.TempDir()); err == nil {
		t.Fatal("expected escaping symlink to be rejected")
	}

	// 3. Transient HTTP 503 retries succeed; stalled download aborts with ErrCodeTimeout.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes := buildTestTarGz(t, map[string]string{"ColabOne.exe": "v0.1.11"})
	sum := sha256.Sum256(archiveBytes)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:]))

	var attempts int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/test/repo/releases/download/v0.1.11/update-windows-amd64.json":
			attempts++
			if attempts == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(ColabOneSignedManifest{
				Version:   "0.1.11",
				URL:       srv.URL + "/ok.tar.gz",
				Size:      int64(len(archiveBytes)),
				Signature: sig,
			})
		case "/ok.tar.gz":
			_, _ = w.Write(archiveBytes)
		case "/test/repo/releases/download/v0.1.10/update-windows-amd64.json":
			_ = json.NewEncoder(w).Encode(ColabOneSignedManifest{
				Version:   "0.1.10",
				URL:       srv.URL + "/stall.tar.gz",
				Size:      int64(len(archiveBytes)),
				Signature: sig,
			})
		case "/stall.tar.gz":
			w.WriteHeader(http.StatusOK)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	installDir := filepath.Join(t.TempDir(), "ColabOne")
	_ = os.MkdirAll(installDir, 0o755)
	installer := &ColabOneRollbackInstaller{
		Repo:              "test/repo",
		Target:            "windows-amd64",
		PublicKey:         base64.StdEncoding.EncodeToString(pub),
		GitHubBase:        srv.URL,
		Client:            srv.Client(),
		TargetDir:         installDir,
		StartExe:          filepath.Join(installDir, "ColabOne.exe"),
		PlatformOS:        "linux",
		StallTimeout:      80 * time.Millisecond,
		RetryDelays:       []time.Duration{5 * time.Millisecond},
		allowLoopbackHTTP: true,
	}

	hRetry, err := installer.PrepareRollback(context.Background(), "0.1.11")
	if err != nil || attempts != 2 {
		t.Fatalf("expected transient 503 retry to succeed on attempt 2: attempts=%d err=%v", attempts, err)
	}
	if err := hRetry.Install(context.Background(), nil); err != nil {
		t.Fatalf("Install after retry failed: %v", err)
	}

	hStall, err := installer.PrepareRollback(context.Background(), "0.1.10")
	if err != nil {
		t.Fatalf("PrepareRollback stall: %v", err)
	}
	if err := hStall.Install(context.Background(), nil); err == nil {
		t.Fatal("expected stalled download to fail")
	} else if code, _ := ClassifyUpdateError(err); code != ErrCodeTimeout {
		t.Fatalf("expected stalled download to classify as %q, got %q (err=%v)", ErrCodeTimeout, code, err)
	}

	// 4. Channel toggle clears cached RollbackVersions, and cancelling a rollback
	// started from StatusError restores cleanly to StatusUpToDate (never empty error).
	started := make(chan struct{})
	rbBackend := &fakeRollbackBackend{
		current: "0.1.13",
		canSelf: true,
		versions: []ColabOneRollbackVersion{
			{Version: "0.1.12", Tag: "v0.1.12", CanRollback: true},
		},
		prepareFn: func(_ context.Context, version string) (ColabOneUpdateInstallable, error) {
			return &blockingInstallable{version: version, started: started}, nil
		},
	}
	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   rbBackend,
		ConfigDir: t.TempDir(),
	})
	defer u.Close()

	if list, err := u.ListRollbackVersions(context.Background(), false); err != nil || len(list) != 1 {
		t.Fatalf("ListRollbackVersions: %+v err=%v", list, err)
	}
	stPref, changed, err := u.SetPreferences(true, true)
	if err != nil || !changed || len(stPref.RollbackVersions) != 0 {
		t.Fatalf("expected channel toggle to clear RollbackVersions: changed=%v st=%+v err=%v", changed, stPref, err)
	}

	// Put updater into StatusError before starting rollback, then cancel rollback.
	u.mu.Lock()
	u.state.Status = StatusError
	u.state.ErrorCode = ErrCodeNetwork
	u.state.ErrorMessage = "dial tcp failed"
	u.mu.Unlock()

	if _, err := u.StartRollback("0.1.12"); err != nil {
		t.Fatalf("StartRollback from StatusError failed: %v", err)
	}
	<-started
	restored := u.CancelInstall()
	if restored.Status != StatusUpToDate || restored.IsRollback || restored.AvailableVersion != "" || restored.ErrorCode != "" {
		t.Fatalf("expected cancel after error state to restore clean up_to_date, got: %+v", restored)
	}
}

func buildCustomTarGz(t *testing.T, entries []struct {
	hdr  tar.Header
	body string
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		h := entry.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(entry.body))
			if h.Mode == 0 {
				h.Mode = 0o644
			}
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("write header %q: %v", h.Name, err)
		}
		if h.Typeflag == tar.TypeReg && len(entry.body) > 0 {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatalf("write body %q: %v", h.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractArchiveRejectsSymlinkChainEscapeAndSymlinkOverwrite(t *testing.T) {
	// 1. Intermediate symlink in parent directory path used to escape extraction root.
	intermediateChain := buildCustomTarGz(t, []struct {
		hdr  tar.Header
		body string
	}{
		{hdr: tar.Header{Name: "a/b", Typeflag: tar.TypeDir, Mode: 0o755}},
		{hdr: tar.Header{Name: "a/b/up1", Typeflag: tar.TypeSymlink, Linkname: "../.."}},
		{hdr: tar.Header{Name: "a/b/up1/up2", Typeflag: tar.TypeSymlink, Linkname: ".."}},
	})
	if err := extractColabOneSignedArchive(bytes.NewReader(intermediateChain), t.TempDir()); err == nil {
		t.Fatal("expected intermediate symlink-chain escape to be rejected")
	}

	// 2. Two-hop relative symlink chain escaping root (b/link2 -> ../a/link1/.. where a/link1 -> ..).
	twoHopChain := buildCustomTarGz(t, []struct {
		hdr  tar.Header
		body string
	}{
		{hdr: tar.Header{Name: "a", Typeflag: tar.TypeDir, Mode: 0o755}},
		{hdr: tar.Header{Name: "a/link1", Typeflag: tar.TypeSymlink, Linkname: ".."}},
		{hdr: tar.Header{Name: "b", Typeflag: tar.TypeDir, Mode: 0o755}},
		{hdr: tar.Header{Name: "b/link2", Typeflag: tar.TypeSymlink, Linkname: "../a/link1/.."}},
	})
	if err := extractColabOneSignedArchive(bytes.NewReader(twoHopChain), t.TempDir()); err == nil {
		t.Fatal("expected two-hop symlink-chain escape to be rejected")
	}

	// 3. Regular file writing through a pre-existing symlink at the exact target path.
	symlinkOverwrite := buildCustomTarGz(t, []struct {
		hdr  tar.Header
		body string
	}{
		{hdr: tar.Header{Name: "victim.txt", Typeflag: tar.TypeReg, Mode: 0o644}, body: "original"},
		{hdr: tar.Header{Name: "alias.txt", Typeflag: tar.TypeSymlink, Linkname: "victim.txt"}},
		{hdr: tar.Header{Name: "alias.txt", Typeflag: tar.TypeReg, Mode: 0o644}, body: "overwritten"},
	})
	outDir := t.TempDir()
	if err := extractColabOneSignedArchive(bytes.NewReader(symlinkOverwrite), outDir); err == nil {
		t.Fatal("expected regular file overwriting pre-existing symlink to be rejected")
	}
	gotVictim, _ := os.ReadFile(filepath.Join(outDir, "victim.txt"))
	if string(gotVictim) != "original" {
		t.Fatalf("victim.txt was modified through symlink: %q", string(gotVictim))
	}

	// 4. Legitimate internal macOS .app framework symlinks succeed cleanly.
	validFramework := buildCustomTarGz(t, []struct {
		hdr  tar.Header
		body string
	}{
		{hdr: tar.Header{Name: "ColabOne.app/Contents/Frameworks/Foo.framework/Versions/A/Foo", Typeflag: tar.TypeReg, Mode: 0o755}, body: "dylib"},
		{hdr: tar.Header{Name: "ColabOne.app/Contents/Frameworks/Foo.framework/Versions/Current", Typeflag: tar.TypeSymlink, Linkname: "A"}},
		{hdr: tar.Header{Name: "ColabOne.app/Contents/Frameworks/Foo.framework/Foo", Typeflag: tar.TypeSymlink, Linkname: "Versions/Current/Foo"}},
	})
	validDir := t.TempDir()
	if err := extractColabOneSignedArchive(bytes.NewReader(validFramework), validDir); err != nil {
		t.Fatalf("expected valid internal framework symlinks to succeed, got: %v", err)
	}

	// 5. Cross-platform Readlink normalization allows Windows relative backslashes while rejecting drive/UNC/absolute targets.
	if got, err := normalizeReadlinkTarget(`Versions\Current\Foo`, true); err != nil || got != "Versions/Current/Foo" {
		t.Fatalf("normalizeReadlinkTarget windows relative = (%q, %v), want (Versions/Current/Foo, nil)", got, err)
	}
	for _, bad := range []string{`\\server\share\x`, `\Windows\System32`, `C:\Windows\System32`, `C:foo`, `/etc/passwd`, ""} {
		if _, err := normalizeReadlinkTarget(bad, true); err == nil {
			t.Fatalf("expected windows normalizeReadlinkTarget(%q) to fail", bad)
		}
	}
	if _, err := normalizeReadlinkTarget(`Versions\Current\Foo`, false); err == nil {
		t.Fatal("expected non-windows normalizeReadlinkTarget to reject backslash target")
	}
}

func TestValidateUpdateURLAndCheckRedirectHardening(t *testing.T) {
	allowed := []string{
		"https://github.com/luolongfei/ColabOne/releases/download/v0.1.7/update-windows-amd64.json",
		"https://api.github.com/repos/luolongfei/ColabOne/releases",
		"https://objects.githubusercontent.com/github-production-release-asset-2e65be/archive.tar.gz",
		"https://release-assets.githubusercontent.com/github-production-release-asset/archive.tar.gz",
		"https://github-releases.githubusercontent.com/12345/archive.tar.gz",
	}
	for _, raw := range allowed {
		if err := validateColabOneUpdateURL(raw); err != nil {
			t.Fatalf("expected allowed production update URL %q, got error: %v", raw, err)
		}
	}

	rejected := []string{
		"http://127.0.0.1:8080/update.json",
		"http://localhost:8080/update.json",
		"http://[::1]:8080/update.json",
		"http://github.com/luolongfei/ColabOne/releases/download/v0.1.7/a.tar.gz",
		"https://evil.example.com/archive.tar.gz",
		"https://raw.githubusercontent.com/luolongfei/ColabOne/main/a.tar.gz",
		"https://github.com:8443/luolongfei/ColabOne/releases/download/v0.1.7/a.tar.gz",
		"https://user:pass@github.com/luolongfei/ColabOne/releases/download/v0.1.7/a.tar.gz",
	}
	for _, raw := range rejected {
		if err := validateColabOneUpdateURL(raw); err == nil {
			t.Fatalf("expected production update URL %q to be rejected", raw)
		}
	}

	// CheckRedirect validates every redirect hop and strips sensitive headers cross-host.
	reqCross, _ := http.NewRequest(http.MethodGet, "https://objects.githubusercontent.com/asset.tar.gz", nil)
	reqCross.Header.Set("Authorization", "Bearer secret")
	reqCross.Header.Set("Cookie", "sid=secret")
	reqOrigin, _ := http.NewRequest(http.MethodGet, "https://github.com/luolongfei/ColabOne/releases/download/v0.1.7/asset.tar.gz", nil)
	if err := ColabOneUpdateCheckRedirect(reqCross, []*http.Request{reqOrigin}); err != nil {
		t.Fatalf("expected valid GitHub asset redirect allowed, got: %v", err)
	}
	if reqCross.Header.Get("Authorization") != "" || reqCross.Header.Get("Cookie") != "" {
		t.Fatalf("expected cross-host redirect to strip Authorization/Cookie headers")
	}

	reqEvil, _ := http.NewRequest(http.MethodGet, "https://evil.example.com/payload.tar.gz", nil)
	if err := ColabOneUpdateCheckRedirect(reqEvil, []*http.Request{reqOrigin}); err == nil {
		t.Fatal("expected redirect to evil.example.com to be rejected")
	}
	reqDowngrade, _ := http.NewRequest(http.MethodGet, "http://objects.githubusercontent.com/asset.tar.gz", nil)
	if err := ColabOneUpdateCheckRedirect(reqDowngrade, []*http.Request{reqOrigin}); err == nil {
		t.Fatal("expected HTTP downgrade redirect to be rejected")
	}

	// End-to-end feed checker blocks redirect to disallowed external host.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example.com/manifest.json", http.StatusFound)
	}))
	defer srv.Close()

	checker := &ColabOneReadOnlyFeedChecker{
		Repo:       COLABONE_GITHUB_REPO,
		Target:     "windows-amd64",
		GitHubBase: srv.URL,
		APIBase:    srv.URL,
		Client:     srv.Client(),
	}
	if _, err := checker.Check(context.Background(), false); err == nil || !strings.Contains(err.Error(), "disallowed update host") {
		t.Fatalf("expected feed checker to block redirect to evil.example.com, got: %v", err)
	}

	// Production rollback installer without test option rejects http://127.0.0.1 manifest URL.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	prodInstaller := &ColabOneRollbackInstaller{
		Repo:       "test/repo",
		Target:     "windows-amd64",
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		GitHubBase: srv.URL,
		Client:     srv.Client(),
	}
	if _, err := prodInstaller.PrepareRollback(context.Background(), "0.1.12"); err == nil {
		t.Fatal("expected production rollback installer to reject loopback http URL without test option")
	}
}

func TestRollbackClearsStalePendingAndCloseSuppressesInstallCallbacks(t *testing.T) {
	// 1. After a failed rollback, StartInstall must NOT silently install a stale forward update.
	forwardInstallCalled := false
	rbBackend := &fakeRollbackBackend{
		current: "0.1.13",
		canSelf: true,
		prepareFn: func(_ context.Context, _ string) (ColabOneUpdateInstallable, error) {
			return nil, os.ErrPermission
		},
	}
	// Set u.pending via Check using a custom backend that returns a forward update and fails rollback.
	customBackend := &stalePendingTestBackend{
		fakeRollbackBackend: rbBackend,
		forwardInstall: &fakeInstallable{
			version: "0.1.14",
			install: func(context.Context, func(int64, int64)) error {
				forwardInstallCalled = true
				return nil
			},
		},
	}

	errCh := make(chan ColabOneUpdateState, 16)
	u := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend:   customBackend,
		ConfigDir: t.TempDir(),
		OnStateChange: func(st ColabOneUpdateState) {
			if st.Status == StatusError {
				select {
				case errCh <- st:
				default:
				}
			}
		},
	})
	defer u.Close()

	st, err := u.Check(context.Background(), true)
	if err != nil || st.Status != StatusAvailable || st.AvailableVersion != "0.1.14" {
		t.Fatalf("initial forward Check failed: st=%+v err=%v", st, err)
	}
	if _, err := u.StartRollback("0.1.12"); err != nil {
		t.Fatalf("StartRollback failed to start: %v", err)
	}
	select {
	case errSt := <-errCh:
		if !errSt.IsRollback || errSt.AvailableVersion != "0.1.12" {
			t.Fatalf("unexpected rollback error state: %+v", errSt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for rollback failure")
	}

	if _, err := u.StartInstall(); err == nil {
		t.Fatal("expected StartInstall to reject while in rollback error state")
	}
	if forwardInstallCalled {
		t.Fatal("stale forward pending update was unexpectedly installed after rollback failure")
	}

	// Verify u.pending was cleared on rollback failure and version mismatch is also rejected.
	u.mu.Lock()
	hasPending := u.pending != nil
	u.state.Status = StatusAvailable
	u.state.IsRollback = false
	u.state.AvailableVersion = "0.1.15"
	u.pending = &fakeInstallable{version: "0.1.14"}
	u.mu.Unlock()
	if hasPending {
		t.Fatal("expected u.pending to be cleared on rollback failure")
	}
	if _, err := u.StartInstall(); err == nil {
		t.Fatal("expected StartInstall to reject mismatched pending version vs AvailableVersion")
	}

	// 2. Close() during an active install must suppress OnStateChange and OnLog callbacks.
	started := make(chan struct{})
	var cbMu sync.Mutex
	closing := false
	callbacksAfterClose := 0
	logsAfterClose := 0

	u2 := NewColabOneUpdater(ColabOneUpdaterConfig{
		Backend: &stalePendingTestBackend{
			fakeRollbackBackend: &fakeRollbackBackend{current: "0.1.13", canSelf: true},
			forwardInstall:      &blockingInstallable{version: "0.1.14", started: started},
		},
		ConfigDir: t.TempDir(),
		OnStateChange: func(ColabOneUpdateState) {
			cbMu.Lock()
			if closing {
				callbacksAfterClose++
			}
			cbMu.Unlock()
		},
		OnLog: func(string, string, string) {
			cbMu.Lock()
			if closing {
				logsAfterClose++
			}
			cbMu.Unlock()
		},
	})
	if _, err := u2.Check(context.Background(), true); err != nil {
		t.Fatalf("u2.Check: %v", err)
	}
	if _, err := u2.StartInstall(); err != nil {
		t.Fatalf("u2.StartInstall: %v", err)
	}
	<-started

	cbMu.Lock()
	closing = true
	cbMu.Unlock()
	u2.Close()

	cbMu.Lock()
	gotCB, gotLogs := callbacksAfterClose, logsAfterClose
	cbMu.Unlock()
	if gotCB != 0 || gotLogs != 0 {
		t.Fatalf("expected 0 callbacks/logs after Close() during in-flight install, got state=%d logs=%d", gotCB, gotLogs)
	}
}

type stalePendingTestBackend struct {
	*fakeRollbackBackend
	forwardInstall ColabOneUpdateInstallable
}

func (b *stalePendingTestBackend) CheckSelfUpdate(context.Context) (ColabOneUpdateInstallable, error) {
	return b.forwardInstall, nil
}

type eofAfterDelayReader struct {
	data []byte
	done bool
}

func (r *eofAfterDelayReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, r.data)
	return n, io.EOF
}

func (r *eofAfterDelayReader) Close() error { return nil }

func TestColabOneStalledBodyPreservesEOFAndCloseRaceSafe(t *testing.T) {
	t.Run("EOF_is_not_masked_when_stalled_flag_is_set", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sb := newColabOneStalledBody(&eofAfterDelayReader{data: []byte("ok")}, cancel, 30*time.Millisecond)
		defer sb.Close()

		// Simulate stall flag flipping concurrently right as Read returns io.EOF.
		sb.stalled.Store(true)
		buf := make([]byte, 8)
		n, err := sb.Read(buf)
		if !errors.Is(err, io.EOF) || string(buf[:n]) != "ok" {
			t.Fatalf("Read() = (%q, %v), want (\"ok\", io.EOF)", string(buf[:n]), err)
		}
		_ = ctx
	})

	t.Run("Close_does_not_race_with_timer_check_or_rearm", func(t *testing.T) {
		for i := 0; i < 40; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			sb := newColabOneStalledBody(io.NopCloser(bytes.NewReader(nil)), cancel, 20*time.Millisecond)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				sb.check()
			}()
			go func() {
				defer wg.Done()
				_ = sb.Close()
			}()
			wg.Wait()
			sb.mu.Lock()
			done := sb.done
			sb.mu.Unlock()
			if !done {
				t.Fatal("expected sb.done to remain true after Close()")
			}
			_ = ctx
		}
	})
}

func TestRollbackWindowsCaseInsensitiveSwapAndRunningEntry(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "ColabOne")
	stageDir := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed existing files with mixed casing on disk.
	if err := os.WriteFile(filepath.Join(installDir, "ColabOne.exe"), []byte("old-bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "stale.dat"), []byte("old-stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, colabOneMyGoFileList), []byte("ColabOne.exe\nstale.dat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "colabone.exe"), []byte("new-bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Running entry check matches case-insensitively on Windows.
	gotRunning := colabOneRunningEntryForOS(installDir, filepath.Join(installDir, "COLABONE.EXE"), true)
	if !strings.EqualFold(gotRunning, "colabone.exe") {
		t.Fatalf("expected case-insensitive running entry match, got %q", gotRunning)
	}

	// Swap with Windows case-insensitive semantics (stageDir -> installDir):
	// ColabOne.exe in file list matches staged colabone.exe case-insensitively; stale.dat is removed.
	if err := swapColabOneInstallDirForOS(stageDir, installDir, filepath.Join(installDir, "COLABONE.EXE"), true); err != nil {
		t.Fatalf("swapColabOneInstallDirForOS failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "stale.dat")); !os.IsNotExist(err) {
		t.Fatalf("expected stale tracked file removed, stat err=%v", err)
	}
	gotNew, err := os.ReadFile(filepath.Join(installDir, "colabone.exe"))
	if err != nil || string(gotNew) != "new-bin" {
		t.Fatalf("expected staged colabone.exe installed, got %q err=%v", string(gotNew), err)
	}
}

func TestRollbackPreservesRenamedRunningExeAbsPathsAndWindowsFileList(t *testing.T) {
	// 1) Renamed running executable must be preserved in .mygo-files (left) so future updates clean it up.
	installDir := filepath.Join(t.TempDir(), "ColabOne")
	stageDir := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "ColabOne-new.exe"), []byte("running-exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, colabOneMyGoFileList), []byte("ColabOne-new.exe\n.MYGO-FILES\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "ColabOne-old.exe"), []byte("rolled-back-exe"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := swapColabOneInstallDirForOS(stageDir, installDir, filepath.Join(installDir, "ColabOne-new.exe"), true); err != nil {
		t.Fatalf("swapColabOneInstallDirForOS failed: %v", err)
	}
	fileList := readColabOneMyGoFileListForOS(installDir, true)
	foundRunning := false
	for _, n := range fileList {
		if strings.EqualFold(n, ".mygo-files") {
			t.Fatalf(".MYGO-FILES should be rejected on Windows, got %v", fileList)
		}
		if n == "ColabOne-new.exe" {
			foundRunning = true
		}
	}
	if !foundRunning {
		t.Fatalf("expected renamed running exe preserved in .mygo-files, got %v", fileList)
	}

	// 2) isColabOneFileListNameForOS rejects .MYGO-FILES on Windows.
	if isColabOneFileListNameForOS(".MYGO-FILES", true) {
		t.Fatal("expected .MYGO-FILES rejected when isWindows=true")
	}

	// 3) Relative target and startExe normalize via filepath.Abs.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(installDir)
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	gotRel := colabOneRunningEntryForOS("ColabOne", filepath.Join("ColabOne", "ColabOne-new.exe"), true)
	if gotRel != "ColabOne-new.exe" {
		t.Fatalf("expected relative paths resolved via filepath.Abs, got %q", gotRel)
	}
}
