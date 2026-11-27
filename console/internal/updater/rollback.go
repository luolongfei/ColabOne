package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	colabOneMaxArchiveBytes        int64 = 1 << 30 // 1 GiB
	colabOneMaxExtractedBytes      int64 = 2 << 30 // 2 GiB guard against decompression bombs
	colabOneMyGoFileList                 = ".mygo-files"
	colabOneDefaultStallTimeout          = time.Minute
	colabOneMaxRetryAfterWait            = 30 * time.Second
)

var (
	colabOneDefaultRetryDelays = []time.Duration{time.Second, 3 * time.Second}
	colabOneStrictSemVerRe     = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)
)

// IsValidSemVer reports whether raw (with optional leading v/V) is a strict SemVer string.
func IsValidSemVer(raw string) bool {
	norm := NormalizeVersion(raw)
	return norm != "" && colabOneStrictSemVerRe.MatchString(norm)
}

// ColabOneSignedManifest mirrors MyGo's signed update-<target>.json manifest.
type ColabOneSignedManifest struct {
	Version   string                  `json:"version"`
	Notes     string                  `json:"notes,omitempty"`
	Date      string                  `json:"date,omitempty"`
	URL       string                  `json:"url"`
	Size      int64                   `json:"size"`
	Signature string                  `json:"signature"`
	Previous  []ColabOneSignedArchive `json:"previous,omitempty"`
}

// ColabOneSignedArchive describes an earlier release archive recorded in a manifest.
type ColabOneSignedArchive struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

// ListRollbackReleases fetches up to maxCount published releases strictly older
// than currentVersion, ordered newest-first, and marks whether each release has
// a signed update manifest for the running platform.
func (c *ColabOneReadOnlyFeedChecker) ListRollbackReleases(
	ctx context.Context,
	includePrerelease bool,
	currentVersion string,
	canSelfUpdate bool,
	maxCount int,
) ([]ColabOneRollbackVersion, error) {
	if maxCount <= 0 {
		maxCount = COLABONE_ROLLBACK_MAX_VERSIONS
	}
	repo, target, ghBase, apiBase, client := c.resolvedConfig()
	current := NormalizeVersion(currentVersion)
	allowPre := includePrerelease || IsPrereleaseVersion(current)
	manifestAssetName := "update-" + target + ".json"

	listURL := apiBase + "/repos/" + repo + "/releases?per_page=30"
	raw, err := c.fetchBytes(ctx, client, listURL, colabOneMaxReleasesBytes, true)
	if err != nil {
		// Under shared proxies GitHub's unauthenticated REST API can return 403/429.
		// Fall back to the signed latest manifest's `previous` archive entries which
		// are served from github.com with zero REST API quota.
		code, _ := ClassifyUpdateError(err)
		if code == ErrCodeRateLimited {
			if fallback, fbErr := c.listRollbackFromManifestPrevious(ctx, client, ghBase, repo, manifestAssetName, allowPre, current, canSelfUpdate, maxCount); fbErr == nil && len(fallback) > 0 {
				return fallback, nil
			}
		}
		return nil, err
	}

	var releases []struct {
		TagName     string `json:"tag_name"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
		CreatedAt   string `json:"created_at"`
		HTMLURL     string `json:"html_url"`
		Assets      []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(raw, &releases); err != nil {
		return nil, fmt.Errorf("parse GitHub releases: %w", err)
	}

	seen := make(map[string]struct{}, len(releases))
	candidates := make([]ColabOneRollbackVersion, 0, maxCount)

	for _, r := range releases {
		if r.Draft {
			continue
		}
		ver := NormalizeVersion(r.TagName)
		if ver == "" || ver[0] < '0' || ver[0] > '9' {
			continue
		}
		isPre := r.Prerelease || IsPrereleaseVersion(ver)
		if isPre && !allowPre {
			continue
		}
		if current != "" && CompareVersions(ver, current) >= 0 {
			continue
		}
		if _, exists := seen[ver]; exists {
			continue
		}
		seen[ver] = struct{}{}

		hasManifest := false
		for _, a := range r.Assets {
			if strings.EqualFold(strings.TrimSpace(a.Name), manifestAssetName) {
				hasManifest = true
				break
			}
		}

		releaseDate := strings.TrimSpace(r.PublishedAt)
		if releaseDate == "" {
			releaseDate = strings.TrimSpace(r.CreatedAt)
		}
		releaseURL := ReleaseTagURL(repo, ver)
		if u := strings.TrimSpace(r.HTMLURL); strings.HasPrefix(u, "https://") {
			releaseURL = u
		}
		tag := strings.TrimSpace(r.TagName)
		if tag == "" {
			tag = "v" + ver
		}

		candidates = append(candidates, ColabOneRollbackVersion{
			Version:      ver,
			Tag:          tag,
			ReleaseDate:  releaseDate,
			ReleaseURL:   releaseURL,
			IsPrerelease: isPre,
			CanRollback:  canSelfUpdate && hasManifest,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return CompareVersions(candidates[i].Version, candidates[j].Version) > 0
	})
	if len(candidates) > maxCount {
		candidates = candidates[:maxCount]
	}
	return candidates, nil
}

func (c *ColabOneReadOnlyFeedChecker) listRollbackFromManifestPrevious(
	ctx context.Context,
	client *http.Client,
	ghBase, repo, manifestAssetName string,
	allowPre bool,
	current string,
	canSelfUpdate bool,
	maxCount int,
) ([]ColabOneRollbackVersion, error) {
	manifestURL := ghBase + "/" + repo + "/releases/latest/download/" + url.PathEscape(manifestAssetName)
	raw, err := c.fetchBytes(ctx, client, manifestURL, colabOneMaxManifestBytes, false)
	if err != nil {
		return nil, err
	}
	var m ColabOneSignedManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(m.Previous))
	out := make([]ColabOneRollbackVersion, 0, maxCount)
	for _, prev := range m.Previous {
		ver := NormalizeVersion(prev.Version)
		if ver == "" || ver[0] < '0' || ver[0] > '9' {
			continue
		}
		isPre := IsPrereleaseVersion(ver)
		if isPre && !allowPre {
			continue
		}
		if current != "" && CompareVersions(ver, current) >= 0 {
			continue
		}
		if _, dup := seen[ver]; dup {
			continue
		}
		seen[ver] = struct{}{}
		signed := strings.TrimSpace(prev.URL) != "" && prev.Size > 0 && strings.TrimSpace(prev.Signature) != ""
		out = append(out, ColabOneRollbackVersion{
			Version:      ver,
			Tag:          "v" + ver,
			ReleaseURL:   ReleaseTagURL(repo, ver),
			IsPrerelease: isPre,
			CanRollback:  canSelfUpdate && signed,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return CompareVersions(out[i].Version, out[j].Version) > 0
	})
	if len(out) > maxCount {
		out = out[:maxCount]
	}
	return out, nil
}

type colabOneStagedMacOSBundle struct {
	work   string
	bundle string
	target string
	helper *exec.Cmd
}

// ColabOneRollbackInstaller resolves, verifies (Ed25519), extracts, and swaps
// signed historical MyGo release archives to perform safe version rollbacks.
type ColabOneRollbackInstaller struct {
	Repo         string
	Target       string
	PublicKey    string
	GitHubBase   string
	Client       *http.Client
	StartExe     string
	TargetDir    string          // Optional test override for install target directory.
	PlatformOS   string          // Optional test override (defaults to runtime.GOOS).
	AppPID       int             // Optional test override (defaults to os.Getpid()).
	StallTimeout time.Duration   // Optional override for body stall timeout (default 60s).
	RetryDelays  []time.Duration // Optional override for transient retry backoff delays.
	OnMacOSQuit  func()          // Called after writing the relaunch marker on macOS.

	// allowLoopbackHTTP is strictly for offline httptest.Server unit tests.
	allowLoopbackHTTP bool

	stageMu     sync.Mutex
	stagedMacOS *colabOneStagedMacOSBundle
}

func (r *ColabOneRollbackInstaller) checkRedirectFunc() func(*http.Request, []*http.Request) error {
	allowLoopback := r.allowLoopbackHTTP
	return func(req *http.Request, via []*http.Request) error {
		return colabOneCheckRedirectWithOptions(req, via, allowLoopback)
	}
}

func (r *ColabOneRollbackInstaller) validateURL(rawURL string) error {
	return validateColabOneUpdateURLWithOptions(rawURL, r.allowLoopbackHTTP)
}

func (r *ColabOneRollbackInstaller) resolvedConfig() (repo, target, ghBase, goos string, client *http.Client) {
	repo = strings.Trim(strings.TrimSpace(r.Repo), "/")
	if repo == "" {
		repo = COLABONE_GITHUB_REPO
	}
	target = strings.TrimSpace(r.Target)
	if target == "" {
		target = TargetPlatform()
	}
	ghBase = strings.TrimRight(strings.TrimSpace(r.GitHubBase), "/")
	if ghBase == "" {
		ghBase = "https://github.com"
	}
	goos = strings.TrimSpace(r.PlatformOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	client = r.Client
	if client == nil {
		client = &http.Client{
			Timeout:       0,
			CheckRedirect: r.checkRedirectFunc(),
		}
	} else if client.CheckRedirect == nil {
		copied := *client
		copied.CheckRedirect = r.checkRedirectFunc()
		client = &copied
	}
	return repo, target, ghBase, goos, client
}

// PrepareRollback fetches and validates the signed update manifest for an older
// release tag v<targetVersion>, returning an installable handle that verifies
// the archive with the app's Ed25519 public key before replacing any files.
func (r *ColabOneRollbackInstaller) PrepareRollback(ctx context.Context, targetVersion string) (ColabOneUpdateInstallable, error) {
	norm := NormalizeVersion(targetVersion)
	if !IsValidSemVer(norm) {
		return nil, errors.New("回退版本号格式无效")
	}
	pubKey, err := parseColabOneUpdatePublicKey(r.PublicKey)
	if err != nil {
		return nil, err
	}

	repo, target, ghBase, _, client := r.resolvedConfig()
	manifestName := "update-" + target + ".json"
	manifestURL := ghBase + "/" + repo + "/releases/download/" + url.PathEscape("v"+norm) + "/" + url.PathEscape(manifestName)

	body, err := r.httpGet(ctx, client, manifestURL)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, colabOneMaxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > colabOneMaxManifestBytes {
		return nil, fmt.Errorf("manifest from %s exceeds limit", manifestURL)
	}

	var manifest ColabOneSignedManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse rollback manifest: %w", err)
	}
	gotVer := NormalizeVersion(manifest.Version)
	if gotVer == "" || strings.TrimSpace(manifest.URL) == "" || strings.TrimSpace(manifest.Signature) == "" {
		return nil, errors.New("incomplete rollback update manifest")
	}
	if CompareVersions(gotVer, norm) != 0 {
		return nil, fmt.Errorf("rollback manifest version %q does not match requested %q", gotVer, norm)
	}
	if manifest.Size <= 0 || manifest.Size > colabOneMaxArchiveBytes {
		return nil, fmt.Errorf("invalid rollback archive size %d", manifest.Size)
	}
	if err := r.validateURL(manifest.URL); err != nil {
		return nil, err
	}

	dt, _ := time.Parse(time.RFC3339, manifest.Date)
	return &colabOneRollbackInstallable{
		installer: r,
		pubKey:    pubKey,
		manifest:  manifest,
		version:   gotVer,
		date:      dt,
	}, nil
}

// RelaunchRollback triggers relaunch for a staged macOS rollback bundle.
// On Windows/Linux it returns (false, nil) so the caller delegates to mygo.App.Relaunch.
func (r *ColabOneRollbackInstaller) RelaunchRollback() (bool, error) {
	_, _, _, goos, _ := r.resolvedConfig()
	if goos != "darwin" {
		return false, nil
	}
	r.stageMu.Lock()
	staged := r.stagedMacOS
	r.stageMu.Unlock()
	if staged == nil {
		return false, nil
	}
	if err := os.WriteFile(filepath.Join(staged.work, "relaunch"), []byte("1\n"), 0o644); err != nil {
		return true, err
	}
	if r.OnMacOSQuit != nil {
		r.OnMacOSQuit()
	}
	return true, nil
}

type colabOneRollbackInstallable struct {
	installer *ColabOneRollbackInstaller
	pubKey    ed25519.PublicKey
	manifest  ColabOneSignedManifest
	version   string
	date      time.Time
}

func (a *colabOneRollbackInstallable) Version() string { return a.version }
func (a *colabOneRollbackInstallable) Notes() string   { return a.manifest.Notes }
func (a *colabOneRollbackInstallable) Date() time.Time { return a.date }

func (a *colabOneRollbackInstallable) Install(ctx context.Context, progress func(downloaded, total int64)) error {
	return a.installer.installSignedArchive(ctx, a.manifest, a.pubKey, progress)
}

func (r *ColabOneRollbackInstaller) installTarget(goos string) (string, string, error) {
	startExe := strings.TrimSpace(r.StartExe)
	if startExe == "" {
		if exe, err := os.Executable(); err == nil {
			startExe = exe
		}
	}
	if target := strings.TrimSpace(r.TargetDir); target != "" {
		return target, startExe, nil
	}
	if startExe == "" {
		return "", "", errors.New("cannot resolve running executable path")
	}
	exe := startExe
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if goos == "darwin" {
		bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
		if !strings.HasSuffix(bundle, ".app") {
			return "", exe, errors.New("only macOS .app bundles can roll back in place")
		}
		return bundle, exe, nil
	}
	if os.Getenv("APPIMAGE") != "" {
		return "", exe, errors.New("AppImages cannot roll back in place")
	}
	return filepath.Dir(exe), exe, nil
}

func (r *ColabOneRollbackInstaller) installSignedArchive(
	ctx context.Context,
	m ColabOneSignedManifest,
	pub ed25519.PublicKey,
	progress func(downloaded, total int64),
) error {
	_, _, _, goos, client := r.resolvedConfig()
	target, startExe, err := r.installTarget(goos)
	if err != nil {
		return err
	}

	work, err := os.MkdirTemp(filepath.Dir(target), colabOneRollbackWorkPattern(target))
	if err != nil {
		return fmt.Errorf("cannot write next to the app: %w", err)
	}
	staged := false
	defer func() {
		if !staged {
			_ = os.RemoveAll(work)
		}
	}()

	archivePath := filepath.Join(work, "rollback.tar.gz")
	if err := r.downloadAndVerify(ctx, client, m.URL, m.Size, m.Signature, pub, archivePath, progress); err != nil {
		return err
	}

	unpacked := filepath.Join(work, "app")
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	extractErr := extractColabOneSignedArchive(f, unpacked)
	_ = f.Close()
	if extractErr != nil {
		return extractErr
	}

	if goos == "darwin" {
		if err := r.stageMacOSBundle(work, unpacked, target); err != nil {
			return err
		}
		staged = true
		return nil
	}
	return swapColabOneInstallDir(unpacked, target, startExe)
}

func (r *ColabOneRollbackInstaller) downloadAndVerify(
	ctx context.Context,
	client *http.Client,
	rawURL string,
	expectedSize int64,
	signature string,
	pub ed25519.PublicKey,
	dstPath string,
	progress func(downloaded, total int64),
) error {
	body, err := r.httpGet(ctx, client, rawURL)
	if err != nil {
		return err
	}
	defer body.Close()

	f, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	sum := sha256.New()
	pr := &colabOneProgressReader{
		r:     io.LimitReader(body, expectedSize+1),
		total: expectedSize,
		fn:    progress,
	}
	n, err := io.Copy(io.MultiWriter(f, sum), pr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading rollback archive: %w", err)
	}
	if n != expectedSize {
		return fmt.Errorf("downloaded %d bytes, want %d", n, expectedSize)
	}
	return verifyColabOneUpdateSignature(pub, sum.Sum(nil), signature)
}

func (r *ColabOneRollbackInstaller) httpGet(ctx context.Context, client *http.Client, rawURL string) (io.ReadCloser, error) {
	if err := r.validateURL(rawURL); err != nil {
		return nil, err
	}
	stallTimeout := r.StallTimeout
	if stallTimeout <= 0 {
		stallTimeout = colabOneDefaultStallTimeout
	}
	retries := r.RetryDelays
	if retries == nil {
		retries = colabOneDefaultRetryDelays
	}
	for try := 0; ; try++ {
		reqCtx, cancel := context.WithCancel(ctx)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("User-Agent", colabOneFeedUserAgent)
		resp, err := client.Do(req)
		var wait time.Duration
		switch {
		case err != nil:
			cancel()
			if ctx.Err() != nil || try >= len(retries) {
				return nil, err
			}
		case resp.StatusCode == http.StatusOK:
			return newColabOneStalledBody(resp.Body, cancel, stallTimeout), nil
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			retryAfter := parseColabOneRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			cancel()
			if !isColabOneTransientHTTPStatus(resp.StatusCode) || try >= len(retries) {
				return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
			}
			wait = retryAfter
		}
		if try < len(retries) && retries[try] > wait {
			wait = retries[try]
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
}

func isColabOneTransientHTTPStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func parseColabOneRetryAfter(raw string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0
	}
	d := time.Duration(n) * time.Second
	if d > colabOneMaxRetryAfterWait {
		return colabOneMaxRetryAfterWait
	}
	return d
}

type colabOneStalledBody struct {
	body    io.ReadCloser
	cancel  context.CancelFunc
	timeout time.Duration
	last    atomic.Int64
	stalled atomic.Bool

	mu    sync.Mutex
	done  bool
	timer *time.Timer
}

func newColabOneStalledBody(body io.ReadCloser, cancel context.CancelFunc, timeout time.Duration) *colabOneStalledBody {
	if timeout <= 0 {
		timeout = colabOneDefaultStallTimeout
	}
	b := &colabOneStalledBody{
		body:    body,
		cancel:  cancel,
		timeout: timeout,
	}
	b.last.Store(time.Now().UnixNano())
	interval := max(timeout/4, 10*time.Millisecond)
	b.mu.Lock()
	b.timer = time.AfterFunc(interval, b.check)
	b.mu.Unlock()
	return b
}

func (b *colabOneStalledBody) check() {
	b.mu.Lock()
	if b.done {
		b.mu.Unlock()
		return
	}
	if time.Since(time.Unix(0, b.last.Load())) >= b.timeout {
		b.stalled.Store(true)
		b.done = true
		b.mu.Unlock()
		b.cancel()
		return
	}
	interval := max(b.timeout/4, 10*time.Millisecond)
	if b.timer != nil {
		b.timer.Reset(interval)
	}
	b.mu.Unlock()
}

func (b *colabOneStalledBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.last.Store(time.Now().UnixNano())
	}
	if err != nil && !errors.Is(err, io.EOF) && b.stalled.Load() {
		return n, errors.New("the download stalled: the connection sent nothing")
	}
	return n, err
}

func (b *colabOneStalledBody) Close() error {
	b.mu.Lock()
	b.done = true
	if b.timer != nil {
		b.timer.Stop()
	}
	b.mu.Unlock()
	b.cancel()
	return b.body.Close()
}

func (r *ColabOneRollbackInstaller) stageMacOSBundle(work, unpacked, target string) error {
	entries, err := os.ReadDir(unpacked)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".app") {
		return errors.New("rollback archive holds no .app bundle")
	}
	pid := r.AppPID
	if pid <= 0 {
		pid = os.Getpid()
	}
	staged := &colabOneStagedMacOSBundle{
		work:   work,
		bundle: filepath.Join(unpacked, entries[0].Name()),
		target: target,
	}
	const script = `pid=$1 target=$2 new=$3 work=$4
trap '' HUP
while kill -0 "$pid" 2>/dev/null; do sleep 0.05; done
if mv "$target" "$work/old"; then
	mv "$new" "$target" || mv "$work/old" "$target"
fi
relaunch=
if [ -e "$work/relaunch" ]; then relaunch=1; fi
rm -rf "$work"
if [ -n "$relaunch" ]; then exec /usr/bin/open "$target"; fi
`
	staged.helper = exec.Command("/bin/sh", "-c", script, "colabone-rollback", strconv.Itoa(pid), staged.target, staged.bundle, staged.work)
	if err := staged.helper.Start(); err != nil {
		return err
	}
	r.stageMu.Lock()
	old := r.stagedMacOS
	r.stagedMacOS = staged
	r.stageMu.Unlock()
	if old != nil {
		if old.helper != nil && old.helper.Process != nil {
			_ = old.helper.Process.Kill()
			_ = old.helper.Wait()
		}
		_ = os.RemoveAll(old.work)
	}
	return nil
}

func parseColabOneUpdatePublicKey(raw string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("invalid update public key")
	}
	return ed25519.PublicKey(b), nil
}

func verifyColabOneUpdateSignature(pub ed25519.PublicKey, digest []byte, signature string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || !ed25519.Verify(pub, digest, sig) {
		return errors.New("the update is not signed with the app's key")
	}
	return nil
}

const (
	colabOneMaxUpdateRedirects = 10
	colabOneMaxSymlinkHops     = 40
)

var colabOneAllowedUpdateHosts = map[string]bool{
	"github.com":                           true,
	"api.github.com":                       true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
	"github-releases.githubusercontent.com": true,
}

func validateColabOneUpdateURL(rawURL string) error {
	return validateColabOneUpdateURLWithOptions(rawURL, false)
}

func validateColabOneUpdateURLWithOptions(rawURL string, allowLoopbackHTTP bool) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return err
	}
	if u.Opaque != "" || u.User != nil {
		return fmt.Errorf("refusing to download %s: invalid update URL", rawURL)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if allowLoopbackHTTP && (u.Scheme == "http" || u.Scheme == "https") && isColabOneLoopbackHost(host) {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing to download %s: updates need HTTPS", rawURL)
	}
	if p := u.Port(); p != "" && p != "443" {
		return fmt.Errorf("refusing to download %s: disallowed update port", rawURL)
	}
	if !colabOneAllowedUpdateHosts[host] {
		return fmt.Errorf("refusing to download %s: disallowed update host %q", rawURL, host)
	}
	return nil
}

// ColabOneUpdateCheckRedirect enforces HTTPS and the GitHub release host
// allowlist on every redirect hop, stripping credentials on cross-host hops.
func ColabOneUpdateCheckRedirect(req *http.Request, via []*http.Request) error {
	return colabOneCheckRedirectWithOptions(req, via, false)
}

func colabOneCheckRedirectWithOptions(req *http.Request, via []*http.Request, allowLoopbackHTTP bool) error {
	if len(via) >= colabOneMaxUpdateRedirects {
		return errors.New("stopped after too many update redirects")
	}
	if req == nil || req.URL == nil {
		return errors.New("invalid redirect target")
	}
	if err := validateColabOneUpdateURLWithOptions(req.URL.String(), allowLoopbackHTTP); err != nil {
		return err
	}
	if len(via) > 0 && via[0] != nil && via[0].URL != nil &&
		!strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
	}
	return nil
}

func isColabOneLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func colabOneRollbackWorkPattern(target string) string {
	name := filepath.Base(target)
	if name == "." || strings.ContainsFunc(name, func(r rune) bool { return r < 0x80 && os.IsPathSeparator(uint8(r)) }) {
		name = "app"
	}
	return "." + name + ".rollback-"
}

func extractColabOneSignedArchive(r io.Reader, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = resolved
	}
	root, err := os.OpenRoot(absDir)
	if err != nil {
		return err
	}
	defer root.Close()

	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var totalWritten int64
	var symlinks []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if strings.Contains(hdr.Name, "\\") {
			return fmt.Errorf("invalid path in update archive: %q", hdr.Name)
		}
		name := path.Clean(hdr.Name)
		if !fs.ValidPath(name) || name == "." {
			return fmt.Errorf("invalid path in update archive: %q", hdr.Name)
		}
		target := filepath.Join(absDir, filepath.FromSlash(name))
		if !isPathWithinDir(absDir, target) {
			return fmt.Errorf("invalid path in update archive: %q", hdr.Name)
		}
		mode := fs.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := ensureRootParentDirs(root, name); err != nil {
				return fmt.Errorf("invalid path in update archive: %q", hdr.Name)
			}
			if info, statErr := root.Lstat(name); statErr == nil {
				if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
					return fmt.Errorf("invalid path in update archive: %q", hdr.Name)
				}
			} else if errors.Is(statErr, fs.ErrNotExist) {
				if err := root.Mkdir(name, 0o755); err != nil {
					return err
				}
			} else {
				return statErr
			}
		case tar.TypeReg:
			if err := ensureRootParentDirs(root, name); err != nil {
				return fmt.Errorf("invalid path in update archive: %q", hdr.Name)
			}
			if info, statErr := root.Lstat(name); statErr == nil {
				if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return fmt.Errorf("invalid path in update archive: %q", hdr.Name)
				}
			} else if !errors.Is(statErr, fs.ErrNotExist) {
				return statErr
			}
			f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o200)
			if err != nil {
				return err
			}
			remaining := colabOneMaxExtractedBytes - totalWritten
			if remaining < 0 {
				_ = f.Close()
				return errors.New("update archive exceeds maximum extracted size")
			}
			n, copyErr := io.Copy(f, io.LimitReader(tr, remaining+1))
			totalWritten += n
			if copyErr == nil && totalWritten > colabOneMaxExtractedBytes {
				copyErr = errors.New("update archive exceeds maximum extracted size")
			}
			if copyErr == nil {
				copyErr = f.Chmod(mode)
			}
			if cerr := f.Close(); copyErr == nil {
				copyErr = cerr
			}
			if copyErr != nil {
				return copyErr
			}
		case tar.TypeSymlink:
			if hdr.Linkname == "" || strings.Contains(hdr.Linkname, "\\") {
				return fmt.Errorf("update archive link %q points outside the app", hdr.Name)
			}
			resolved := path.Join(path.Dir(name), hdr.Linkname)
			if path.IsAbs(hdr.Linkname) || !fs.ValidPath(resolved) {
				return fmt.Errorf("update archive link %q points outside the app", hdr.Name)
			}
			if err := ensureRootParentDirs(root, name); err != nil {
				return fmt.Errorf("update archive link %q points outside the app", hdr.Name)
			}
			if _, statErr := root.Lstat(name); statErr == nil {
				return fmt.Errorf("update archive link %q collides with existing entry", hdr.Name)
			} else if !errors.Is(statErr, fs.ErrNotExist) {
				return statErr
			}
			if err := root.Symlink(filepath.FromSlash(hdr.Linkname), name); err != nil {
				return err
			}
			symlinks = append(symlinks, name)
		default:
			return fmt.Errorf("unsupported entry in update archive: %q", hdr.Name)
		}
	}

	// Verify every extracted symlink chain stays strictly inside the extraction root.
	for _, sym := range symlinks {
		if err := verifyRootSymlinkChain(root, sym); err != nil {
			return fmt.Errorf("update archive link %q points outside the app: %w", sym, err)
		}
	}
	return nil
}

func ensureRootParentDirs(root *os.Root, relPath string) error {
	parent := path.Dir(relPath)
	if parent == "." || parent == "" {
		return nil
	}
	parts := strings.Split(parent, "/")
	cur := ""
	for _, part := range parts {
		if cur == "" {
			cur = part
		} else {
			cur += "/" + part
		}
		info, err := root.Lstat(cur)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("parent component %q is not a real directory", cur)
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := root.Mkdir(cur, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func normalizeReadlinkTarget(raw string, isWindows bool) (string, error) {
	if raw == "" || filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" {
		return "", errors.New("invalid symlink target")
	}
	if !isWindows && strings.Contains(raw, "\\") {
		return "", errors.New("invalid symlink target")
	}
	slashed := raw
	if isWindows {
		slashed = strings.ReplaceAll(raw, "\\", "/")
	}
	if path.IsAbs(slashed) || strings.HasPrefix(slashed, "//") {
		return "", errors.New("invalid symlink target")
	}
	if len(slashed) >= 2 && slashed[1] == ':' && ((slashed[0] >= 'a' && slashed[0] <= 'z') || (slashed[0] >= 'A' && slashed[0] <= 'Z')) {
		return "", errors.New("invalid symlink target")
	}
	return slashed, nil
}

func verifyRootSymlinkChain(root *os.Root, symPath string) error {
	rawTarget, err := root.Readlink(symPath)
	if err != nil {
		return err
	}
	linkTarget, err := normalizeReadlinkTarget(rawTarget, runtime.GOOS == "windows")
	if err != nil {
		return err
	}

	var stack []string
	if dir := path.Dir(symPath); dir != "." && dir != "" {
		stack = strings.Split(dir, "/")
	}
	queue := splitRelativePathParts(linkTarget)
	hops := 1
	for len(queue) > 0 {
		seg := queue[0]
		queue = queue[1:]
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return errors.New("symlink chain escapes extraction root")
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, seg)
			candidate := strings.Join(stack, "/")
			info, statErr := root.Lstat(candidate)
			if statErr != nil {
				if errors.Is(statErr, fs.ErrNotExist) {
					continue
				}
				return statErr
			}
			if info.Mode()&os.ModeSymlink != 0 {
				hops++
				if hops > colabOneMaxSymlinkHops {
					return errors.New("symlink chain exceeds maximum depth")
				}
				rawNext, readErr := root.Readlink(candidate)
				if readErr != nil {
					return readErr
				}
				nextTarget, normErr := normalizeReadlinkTarget(rawNext, runtime.GOOS == "windows")
				if normErr != nil {
					return errors.New("invalid nested symlink target")
				}
				// Resolve nextTarget relative to the directory containing candidate.
				stack = stack[:len(stack)-1]
				nextParts := splitRelativePathParts(nextTarget)
				queue = append(nextParts, queue...)
			}
		}
	}
	return nil
}

func splitRelativePathParts(p string) []string {
	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))
	for _, seg := range raw {
		if seg == "" || seg == "." {
			continue
		}
		out = append(out, seg)
	}
	return out
}

func isPathWithinDir(baseDir, candidate string) bool {
	rel, err := filepath.Rel(baseDir, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func swapColabOneInstallDir(dir, target, startExe string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	ours := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Name() != colabOneMyGoFileList {
			names = append(names, e.Name())
			ours[e.Name()] = true
		}
	}
	if len(names) == 0 {
		return errors.New("rollback archive is empty")
	}

	// Keep the .<name>.old-* pattern MyGo uses so cleanUpdateLeftovers
	// deletes any locked Windows executable backup on the next startup.
	suffix := fmt.Sprintf(".old-%d-%d", os.Getpid(), time.Now().UnixNano())
	var moved, placed [][2]string
	undo := func() {
		for i := len(placed) - 1; i >= 0; i-- {
			if colabOneRenameFile(placed[i][1], placed[i][0]) != nil {
				_ = os.RemoveAll(placed[i][1])
			}
		}
		for i := len(moved) - 1; i >= 0; i-- {
			_ = colabOneRenameFile(moved[i][1], moved[i][0])
		}
	}
	moveAside := func(dst string) error {
		aside := filepath.Join(target, "."+filepath.Base(dst)+suffix)
		if err := colabOneRenameFile(dst, aside); err != nil {
			return err
		}
		moved = append(moved, [2]string{dst, aside})
		return nil
	}

	running := colabOneRunningEntry(target, startExe)
	var left []string
	for _, name := range readColabOneMyGoFileList(target) {
		dst := filepath.Join(target, name)
		if ours[name] || name == running {
			continue
		}
		if _, err := os.Lstat(dst); err != nil {
			continue
		}
		if err := moveAside(dst); err != nil {
			left = append(left, name)
		}
	}

	if err := writeColabOneMyGoFileList(dir, append(names, left...)); err != nil {
		undo()
		return err
	}

	for _, name := range append(names, colabOneMyGoFileList) {
		src, dst := filepath.Join(dir, name), filepath.Join(target, name)
		if _, err := os.Lstat(dst); err == nil {
			if err := moveAside(dst); err != nil {
				undo()
				return err
			}
		}
		if err := colabOneRenameFile(src, dst); err != nil {
			undo()
			return err
		}
		placed = append(placed, [2]string{src, dst})
	}
	for _, m := range moved {
		_ = os.RemoveAll(m[1])
	}
	return nil
}

func colabOneRunningEntry(target, startExe string) string {
	if strings.TrimSpace(startExe) == "" {
		return ""
	}
	exe, dir := startExe, target
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if filepath.Dir(exe) != filepath.Clean(dir) {
		return ""
	}
	return filepath.Base(exe)
}

func readColabOneMyGoFileList(target string) []string {
	raw, err := os.ReadFile(filepath.Join(target, colabOneMyGoFileList))
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(raw), "\n") {
		n := strings.TrimRight(line, "\r\n")
		if isColabOneFileListName(n) {
			names = append(names, n)
		}
	}
	return names
}

func writeColabOneMyGoFileList(dir string, entries []string) error {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if isColabOneFileListName(e) {
			names = append(names, e)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(dir, colabOneMyGoFileList+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, err = tmp.WriteString(b.String())
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = colabOneRenameFile(tmpPath, filepath.Join(dir, colabOneMyGoFileList))
	}
	if err != nil {
		_ = os.Remove(tmpPath)
	}
	return err
}

func isColabOneFileListName(name string) bool {
	return name != "" && name != "." && name != ".." && name != colabOneMyGoFileList &&
		!strings.ContainsAny(name, "/\\\x00\r\n")
}

type colabOneProgressReader struct {
	r     io.Reader
	n     int64
	total int64
	fn    func(downloaded, total int64)
	last  time.Time
}

func (p *colabOneProgressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.fn != nil && (err != nil || p.n == p.total || time.Since(p.last) > 100*time.Millisecond) {
		p.last = time.Now()
		p.fn(p.n, p.total)
	}
	return n, err
}
