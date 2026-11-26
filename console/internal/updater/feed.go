package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	colabOneMaxManifestBytes = 1 << 20
	colabOneMaxReleasesBytes = 8 << 20
	colabOneFeedUserAgent    = "ColabOne-Updater"
)

// ColabOneFeedManifest mirrors the subset of MyGo's update-<target>.json
// needed for read-only update discovery when the install directory is not writable.
type ColabOneFeedManifest struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
	Date    string `json:"date"`
}

// ColabOneReadOnlyFeedChecker queries GitHub Releases without requiring write
// permissions to the application's install directory.
type ColabOneReadOnlyFeedChecker struct {
	Repo           string
	Target         string
	GitHubBase     string        // Defaults to "https://github.com"
	APIBase        string        // Defaults to "https://api.github.com"
	Client         *http.Client
	CurrentVersion func() string // Optional: avoids GitHub API metadata lookup when latest tag <= current version.

	// allowLoopbackHTTP is strictly for offline httptest.Server unit tests.
	allowLoopbackHTTP bool
}

func (c *ColabOneReadOnlyFeedChecker) checkRedirectFunc() func(*http.Request, []*http.Request) error {
	allowLoopback := c.allowLoopbackHTTP
	return func(req *http.Request, via []*http.Request) error {
		return colabOneCheckRedirectWithOptions(req, via, allowLoopback)
	}
}

func (c *ColabOneReadOnlyFeedChecker) resolvedConfig() (repo, target, ghBase, apiBase string, client *http.Client) {
	repo = strings.Trim(strings.TrimSpace(c.Repo), "/")
	if repo == "" {
		repo = COLABONE_GITHUB_REPO
	}
	target = strings.TrimSpace(c.Target)
	if target == "" {
		target = TargetPlatform()
	}
	ghBase = strings.TrimRight(strings.TrimSpace(c.GitHubBase), "/")
	if ghBase == "" {
		ghBase = "https://github.com"
	}
	apiBase = strings.TrimRight(strings.TrimSpace(c.APIBase), "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	client = c.Client
	if client == nil {
		client = &http.Client{
			Timeout:       20 * time.Second,
			CheckRedirect: c.checkRedirectFunc(),
		}
	} else if client.CheckRedirect == nil {
		copied := *client
		copied.CheckRedirect = c.checkRedirectFunc()
		client = &copied
	}
	return repo, target, ghBase, apiBase, client
}

// Check discovers the newest published version for Target on Repo.
// If update-<target>.json is absent (HTTP 404), it falls back to GitHub Release
// metadata so up-to-date users are not shown a false network error and newer
// unsigned releases still offer one-click manual download.
func (c *ColabOneReadOnlyFeedChecker) Check(ctx context.Context, includePrerelease bool) (*ColabOneReadOnlyRelease, error) {
	repo, target, ghBase, apiBase, client := c.resolvedConfig()
	manifestName := "update-" + target + ".json"

	if !includePrerelease {
		manifestURL := ghBase + "/" + repo + "/releases/latest/download/" + url.PathEscape(manifestName)
		manifest, err := c.fetchManifest(ctx, client, manifestURL)
		if err != nil {
			if IsManifestNotFoundError(err) {
				return c.fetchLatestStableMetadata(ctx, client, ghBase, apiBase, repo)
			}
			return nil, err
		}
		dt, _ := time.Parse(time.RFC3339, manifest.Date)
		return &ColabOneReadOnlyRelease{
			Version: NormalizeVersion(manifest.Version),
			Notes:   manifest.Notes,
			Date:    dt,
		}, nil
	}

	return c.fetchPrereleaseRelease(ctx, client, ghBase, apiBase, repo, manifestName, true)
}

// CheckMetadata queries GitHub Release metadata directly without requesting
// update-<target>.json first (used as fallback when mygo.Updater.Check already
// received HTTP 404 for the signed manifest).
func (c *ColabOneReadOnlyFeedChecker) CheckMetadata(ctx context.Context, includePrerelease bool) (*ColabOneReadOnlyRelease, error) {
	repo, target, ghBase, apiBase, client := c.resolvedConfig()
	if !includePrerelease {
		return c.fetchLatestStableMetadata(ctx, client, ghBase, apiBase, repo)
	}
	return c.fetchPrereleaseRelease(ctx, client, ghBase, apiBase, repo, "update-"+target+".json", false)
}

func (c *ColabOneReadOnlyFeedChecker) fetchLatestStableMetadata(
	ctx context.Context,
	client *http.Client,
	ghBase, apiBase, repo string,
) (*ColabOneReadOnlyRelease, error) {
	// Probe https://github.com/<repo>/releases/latest without following redirects.
	// This consumes zero GitHub REST API quota and tells us the latest stable tag.
	noRedirectClient := *client
	noRedirectClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	latestURL := ghBase + "/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", colabOneFeedUserAgent)
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNoReleasePublished
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc := strings.TrimSpace(resp.Header.Get("Location"))
		tag, ok := extractGitHubReleaseTag(loc)
		if !ok {
			if strings.HasSuffix(strings.TrimRight(loc, "/"), "/releases") {
				return nil, ErrNoReleasePublished
			}
			return c.fetchLatestStableAPI(ctx, client, apiBase, repo)
		}
		ver := NormalizeVersion(tag)
		if ver == "" {
			return nil, ErrNoReleasePublished
		}
		if c.CurrentVersion != nil {
			if cur := NormalizeVersion(c.CurrentVersion()); cur != "" && CompareVersions(ver, cur) <= 0 {
				return &ColabOneReadOnlyRelease{Version: ver}, nil
			}
		}
		// Newer release discovered without a signed manifest: best-effort fetch
		// bilingual notes & publish date from GitHub REST API.
		if detailed, apiErr := c.fetchReleaseByTagAPI(ctx, client, apiBase, repo, tag); apiErr == nil && detailed != nil {
			return detailed, nil
		}
		return &ColabOneReadOnlyRelease{Version: ver}, nil
	case resp.StatusCode == http.StatusOK:
		return c.fetchLatestStableAPI(ctx, client, apiBase, repo)
	default:
		return nil, fmt.Errorf("GET %s: %s", latestURL, resp.Status)
	}
}

func extractGitHubReleaseTag(location string) (string, bool) {
	const marker = "/releases/tag/"
	idx := strings.Index(location, marker)
	if idx < 0 {
		return "", false
	}
	rest := location[idx+len(marker):]
	if q := strings.IndexAny(rest, "?#"); q >= 0 {
		rest = rest[:q]
	}
	rest = strings.Trim(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	unescaped, err := url.PathUnescape(rest)
	if err != nil || strings.TrimSpace(unescaped) == "" {
		return "", false
	}
	return unescaped, true
}

func (c *ColabOneReadOnlyFeedChecker) fetchLatestStableAPI(
	ctx context.Context,
	client *http.Client,
	apiBase, repo string,
) (*ColabOneReadOnlyRelease, error) {
	apiURL := apiBase + "/repos/" + repo + "/releases/latest"
	raw, err := c.fetchBytes(ctx, client, apiURL, colabOneMaxManifestBytes, true)
	if err != nil {
		if IsManifestNotFoundError(err) {
			return nil, ErrNoReleasePublished
		}
		return nil, err
	}
	return parseGitHubReleaseJSON(raw)
}

func (c *ColabOneReadOnlyFeedChecker) fetchReleaseByTagAPI(
	ctx context.Context,
	client *http.Client,
	apiBase, repo, tag string,
) (*ColabOneReadOnlyRelease, error) {
	apiURL := apiBase + "/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
	raw, err := c.fetchBytes(ctx, client, apiURL, colabOneMaxManifestBytes, true)
	if err != nil {
		return nil, err
	}
	return parseGitHubReleaseJSON(raw)
}

func parseGitHubReleaseJSON(raw []byte) (*ColabOneReadOnlyRelease, error) {
	var r struct {
		TagName     string `json:"tag_name"`
		Draft       bool   `json:"draft"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse GitHub release: %w", err)
	}
	if r.Draft {
		return nil, ErrNoReleasePublished
	}
	ver := NormalizeVersion(r.TagName)
	if ver == "" {
		return nil, ErrNoReleasePublished
	}
	dt, _ := time.Parse(time.RFC3339, r.PublishedAt)
	return &ColabOneReadOnlyRelease{
		Version: ver,
		Notes:   r.Body,
		Date:    dt,
	}, nil
}

func (c *ColabOneReadOnlyFeedChecker) fetchPrereleaseRelease(
	ctx context.Context,
	client *http.Client,
	ghBase, apiBase, repo, manifestName string,
	tryManifest bool,
) (*ColabOneReadOnlyRelease, error) {
	// Prerelease channel: query GitHub Releases REST API and pick highest non-draft v* tag.
	listURL := apiBase + "/repos/" + repo + "/releases?per_page=30"
	raw, err := c.fetchBytes(ctx, client, listURL, colabOneMaxReleasesBytes, true)
	if err != nil {
		return nil, err
	}
	var releases []struct {
		TagName     string `json:"tag_name"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(raw, &releases); err != nil {
		return nil, fmt.Errorf("parse GitHub releases: %w", err)
	}

	bestIdx := -1
	bestVer := ""
	for i, r := range releases {
		if r.Draft || !strings.HasPrefix(r.TagName, "v") {
			continue
		}
		ver := NormalizeVersion(r.TagName)
		if ver == "" || ver[0] < '0' || ver[0] > '9' {
			continue
		}
		if bestIdx == -1 || CompareVersions(ver, bestVer) > 0 {
			bestIdx = i
			bestVer = ver
		}
	}
	if bestIdx == -1 {
		return nil, ErrNoReleasePublished
	}

	chosen := releases[bestIdx]
	if tryManifest {
		manifestURL := ghBase + "/" + repo + "/releases/download/" + url.PathEscape(chosen.TagName) + "/" + url.PathEscape(manifestName)
		if manifest, err := c.fetchManifest(ctx, client, manifestURL); err == nil && NormalizeVersion(manifest.Version) != "" {
			dt, _ := time.Parse(time.RFC3339, manifest.Date)
			notes := manifest.Notes
			if strings.TrimSpace(notes) == "" {
				notes = chosen.Body
			}
			return &ColabOneReadOnlyRelease{
				Version: NormalizeVersion(manifest.Version),
				Notes:   notes,
				Date:    dt,
			}, nil
		}
	}

	dt, _ := time.Parse(time.RFC3339, chosen.PublishedAt)
	return &ColabOneReadOnlyRelease{
		Version: bestVer,
		Notes:   chosen.Body,
		Date:    dt,
	}, nil
}

func (c *ColabOneReadOnlyFeedChecker) fetchManifest(ctx context.Context, client *http.Client, rawURL string) (*ColabOneFeedManifest, error) {
	raw, err := c.fetchBytes(ctx, client, rawURL, colabOneMaxManifestBytes, false)
	if err != nil {
		return nil, err
	}
	var m ColabOneFeedManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse update manifest: %w", err)
	}
	if NormalizeVersion(m.Version) == "" {
		return nil, errors.New("incomplete update manifest")
	}
	return &m, nil
}

func (c *ColabOneReadOnlyFeedChecker) fetchBytes(ctx context.Context, client *http.Client, rawURL string, maxBytes int64, githubAPI bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", colabOneFeedUserAgent)
	if githubAPI {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("response from %s exceeds limit", rawURL)
	}
	return data, nil
}
