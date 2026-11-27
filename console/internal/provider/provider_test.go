package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/assignproxy"
	"github.com/luolongfei/ColabOne/console/internal/jupyterws"
)

// ---------------------------------------------------------------------------
// Offline Test Doubles
// ---------------------------------------------------------------------------

type memoryVault struct {
	mu      sync.Mutex
	data    map[string][]byte
	failSet func(key string) error
}

func newMemoryVault() *memoryVault {
	return &memoryVault{data: make(map[string][]byte)}
}

func (v *memoryVault) GetJSON(key string, dest any) (bool, error) {
	v.mu.Lock()
	raw, ok := v.data[key]
	v.mu.Unlock()
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, err
	}
	return true, nil
}

func (v *memoryVault) Set(key string, val any) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failSet != nil {
		if err := v.failSet(key); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	v.data[key] = raw
	return nil
}

func (v *memoryVault) Delete(key string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.data, key)
	return nil
}

func (v *memoryVault) Has(key string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.data[key]
	return ok
}

type fakeAuth struct {
	profile map[string]string
	token   string
}

func (a *fakeAuth) Profile(ctx context.Context) (map[string]string, error) {
	if a.profile == nil {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(a.profile))
	for k, v := range a.profile {
		out[k] = v
	}
	return out, nil
}

func (a *fakeAuth) AccessToken(ctx context.Context, name string) (string, error) {
	return a.token, nil
}

type fakeLocationInferer struct{}

func (fakeLocationInferer) LocationFromZone(zone string) map[string]any {
	z := strings.ToLower(strings.TrimSpace(zone))
	switch {
	case strings.Contains(z, "asia-south1"):
		return map[string]any{"country": "IN", "country_name": "印度", "region": "孟买", "zone": "asia-south1-c"}
	case strings.Contains(z, "europe-west4"):
		return map[string]any{"country": "NL", "country_name": "荷兰", "region": "埃姆斯哈文", "zone": "europe-west4-a"}
	case strings.Contains(z, "us-west1"):
		return map[string]any{"country": "US", "country_name": "美国", "region": "俄勒冈", "zone": "us-west1-b"}
	default:
		return nil
	}
}

func (fakeLocationInferer) InferLocationFromEndpoint(ep, rawURL string) map[string]any {
	joined := strings.ToLower(ep + " " + rawURL)
	switch {
	case strings.Contains(joined, "usw1"):
		return map[string]any{"country": "US", "country_name": "美国", "region": "俄勒冈", "zone": "us-west1-b"}
	case strings.Contains(joined, "usw4"):
		return map[string]any{"country": "US", "country_name": "美国", "region": "内华达", "zone": "us-west4-b"}
	case strings.Contains(joined, "ass1"), strings.Contains(joined, "asso1"):
		return map[string]any{"country": "IN", "country_name": "印度", "region": "孟买", "zone": "asia-south1-a"}
	default:
		return nil
	}
}

func (fakeLocationInferer) NormalizeGeoPayload(country, region, city, zone string) map[string]any {
	c := strings.ToUpper(strings.TrimSpace(country))
	if c == "JP" {
		return map[string]any{"country": "JP", "country_name": "日本", "region": "", "zone": ""}
	}
	if c == "SG" {
		return map[string]any{"country": "SG", "country_name": "新加坡", "region": "", "zone": ""}
	}
	return nil
}

type fakeColabClient struct {
	mu          sync.Mutex
	domain      string
	closed      bool
	unassigned  []string
	keepAlives  []string
	consumption func(ctx context.Context) (*ConsumptionInfo, error)
	listFn      func(ctx context.Context) ([]ListedAssignment, error)
	getAssignFn func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error)
	postAssign  func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error)
	unassignFn  func(ctx context.Context, endpoint string) error
	keepAliveFn func(ctx context.Context, endpoint string) error
}

func (c *fakeColabClient) ColabDomain() string {
	if c.domain != "" {
		return c.domain
	}
	return "https://colab.research.google.com"
}

func (c *fakeColabClient) GetConsumptionUserInfo(ctx context.Context) (*ConsumptionInfo, error) {
	if c.consumption != nil {
		return c.consumption(ctx)
	}
	return &ConsumptionInfo{PaidComputeUnitsBalance: 100.0, ConsumptionRateHourly: 1.5}, nil
}

func (c *fakeColabClient) ListAssignments(ctx context.Context) ([]ListedAssignment, error) {
	if c.listFn != nil {
		return c.listFn(ctx)
	}
	return nil, nil
}

func (c *fakeColabClient) GetAssignment(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
	if c.getAssignFn != nil {
		return c.getAssignFn(ctx, nbh, variant, accel, shape)
	}
	return nil, "xsrf-default", nil
}

func (c *fakeColabClient) PostAssignment(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
	if c.postAssign != nil {
		return c.postAssign(ctx, nbh, token, variant, accel, shape)
	}
	return nil, errors.New("unexpected PostAssignment")
}

func (c *fakeColabClient) Unassign(ctx context.Context, endpoint string) error {
	c.mu.Lock()
	c.unassigned = append(c.unassigned, endpoint)
	c.mu.Unlock()
	if c.unassignFn != nil {
		return c.unassignFn(ctx, endpoint)
	}
	return nil
}

func (c *fakeColabClient) KeepAlive(ctx context.Context, endpoint string) error {
	c.mu.Lock()
	c.keepAlives = append(c.keepAlives, endpoint)
	c.mu.Unlock()
	if c.keepAliveFn != nil {
		return c.keepAliveFn(ctx, endpoint)
	}
	return nil
}

func (c *fakeColabClient) DoAuthorized(ctx context.Context, req *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeColabClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

type uploadRecord struct {
	baseURL    string
	proxyToken string
	remotePath string
	data       []byte
	timeout    time.Duration
}

type fakeContents struct {
	mu       sync.Mutex
	uploads  []uploadRecord
	deletes  []string
	uploadFn func(ctx context.Context, baseURL, proxyToken, remotePath string, data []byte, timeout time.Duration) error
	deleteFn func(ctx context.Context, baseURL, proxyToken, remotePath string) error
}

func (c *fakeContents) UploadBytes(ctx context.Context, baseURL, proxyToken, remotePath string, data []byte, timeout time.Duration) error {
	c.mu.Lock()
	c.uploads = append(c.uploads, uploadRecord{
		baseURL:    baseURL,
		proxyToken: proxyToken,
		remotePath: remotePath,
		data:       append([]byte(nil), data...),
		timeout:    timeout,
	})
	fn := c.uploadFn
	c.mu.Unlock()
	if fn != nil {
		return fn(ctx, baseURL, proxyToken, remotePath, data, timeout)
	}
	return nil
}

func (c *fakeContents) DeleteFile(ctx context.Context, baseURL, proxyToken, remotePath string) error {
	c.mu.Lock()
	c.deletes = append(c.deletes, remotePath)
	fn := c.deleteFn
	c.mu.Unlock()
	if fn != nil {
		return fn(ctx, baseURL, proxyToken, remotePath)
	}
	return nil
}

type fakeKernel struct {
	mu             sync.Mutex
	opts           KernelOptions
	startCount     int
	execCalls      []string
	execTimeouts   []time.Duration
	stopCalls      []bool
	aborted        atomic.Bool
	kernelID       string
	sessionID      string
	startFn        func(ctx context.Context) error
	executeFn      func(ctx context.Context, code string, opts ExecuteOptions) ([]KernelOutput, error)
	abortFn        func()
	stopFn         func(shutdownKernel bool)
	abortCh        chan struct{}
	abortCloseOnce sync.Once
}

func newFakeKernel(opts KernelOptions) *fakeKernel {
	return &fakeKernel{
		opts:      opts,
		kernelID:  opts.KernelID,
		sessionID: opts.SessionID,
		abortCh:   make(chan struct{}),
	}
}

func (k *fakeKernel) Start(ctx context.Context) error {
	k.mu.Lock()
	k.startCount++
	fn := k.startFn
	k.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

func (k *fakeKernel) ExecuteCode(ctx context.Context, code string, opts ExecuteOptions) ([]KernelOutput, error) {
	k.mu.Lock()
	k.execCalls = append(k.execCalls, code)
	k.execTimeouts = append(k.execTimeouts, opts.Timeout)
	fn := k.executeFn
	k.mu.Unlock()
	if fn != nil {
		return fn(ctx, code, opts)
	}
	return nil, nil
}

func (k *fakeKernel) Abort() {
	k.aborted.Store(true)
	k.mu.Lock()
	fn := k.abortFn
	k.mu.Unlock()
	if fn != nil {
		fn()
	}
	k.abortCloseOnce.Do(func() { close(k.abortCh) })
}

func (k *fakeKernel) Stop(ctx context.Context, shutdownKernel bool) error {
	k.mu.Lock()
	k.stopCalls = append(k.stopCalls, shutdownKernel)
	fn := k.stopFn
	k.mu.Unlock()
	if fn != nil {
		fn(shutdownKernel)
	}
	return nil
}

func (k *fakeKernel) KernelID() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.kernelID
}

func (k *fakeKernel) SessionID() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.sessionID
}

type fakeBundler struct {
	bootScript   string
	updateScript string
}

func (b *fakeBundler) BuildZip() ([]byte, int, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"colabone/server/vm_boot.py":   b.bootScript,
		"colabone/server/vm_update.py": b.updateScript,
		"colabone/server/app.py":       "# app\n",
	}
	for name, content := range files {
		if content == "" {
			content = "# stub\n"
		}
		w, err := zw.Create(name)
		if err != nil {
			return nil, 0, err
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), len(files), nil
}

type fakeDriveMounter struct {
	mu          sync.Mutex
	dryrunOK    bool
	dryrunURL   string
	dryrunErr   error
	hookCalled  bool
	stopped     bool
	err         error
	hookFn      func(msg map[string]any, reply func(payload map[string]any) error) bool
	stopFn      func()
	capturedOpt DriveMountOptions
}

func (m *fakeDriveMounter) DryrunCheck(ctx context.Context) (bool, string, error) {
	return m.dryrunOK, m.dryrunURL, m.dryrunErr
}

func (m *fakeDriveMounter) Hook(msg map[string]any, reply func(payload map[string]any) error) bool {
	m.mu.Lock()
	m.hookCalled = true
	fn := m.hookFn
	m.mu.Unlock()
	if fn != nil {
		return fn(msg, reply)
	}
	return true
}

func (m *fakeDriveMounter) Stop() {
	m.mu.Lock()
	m.stopped = true
	fn := m.stopFn
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (m *fakeDriveMounter) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

type fakeTimeoutErr struct{ msg string }

func (e fakeTimeoutErr) Error() string   { return e.msg }
func (e fakeTimeoutErr) Timeout() bool   { return true }
func (e fakeTimeoutErr) Temporary() bool { return true }

type fakeNetErr struct{ msg string }

func (e fakeNetErr) Error() string   { return e.msg }
func (e fakeNetErr) Timeout() bool   { return false }
func (e fakeNetErr) Temporary() bool { return true }

var _ net.Error = fakeTimeoutErr{}
var _ net.Error = fakeNetErr{}

func makeProxyInfo(endpoint string) *RuntimeProxyInfo {
	return &RuntimeProxyInfo{
		Token:                 "tok-" + endpoint,
		URL:                   "https://" + endpoint + ".invalid",
		TokenExpiresInSeconds: 3600,
	}
}

func storeLegacySession(vault *memoryVault, ep string) {
	_ = vault.Set("runtime_session", map[string]any{
		"name":     "colabone",
		"endpoint": ep,
		"token":    "not-real",
		"url":      "https://runtime.invalid",
	})
}

func makeReadyServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		}
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------------------
// 1. Release, Ownership & Multi-GPU Vault Storage Tests
// ---------------------------------------------------------------------------

func TestReleaseOnlyOwnedEndpoint(t *testing.T) {
	vault := newMemoryVault()
	storeLegacySession(vault, "owned")

	listCalls := 0
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			listCalls++
			if listCalls == 1 {
				return []ListedAssignment{{Endpoint: "owned"}, {Endpoint: "unrelated"}}, nil
			}
			return []ListedAssignment{{Endpoint: "unrelated"}}, nil
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:               vault,
		ClientFactory:       func(string) (ColabClient, error) { return client, nil },
		ReleasePollInterval: time.Millisecond,
	})

	res, err := p.Release(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if res["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", res)
	}
	if !reflect.DeepEqual(client.unassigned, []string{"owned"}) {
		t.Fatalf("expected unassign only [owned], got %#v", client.unassigned)
	}
	if vault.Has("runtime_session") {
		t.Fatalf("expected runtime_session cleared from vault")
	}
}

func TestNoAdoptionOfUnrelatedRuntime(t *testing.T) {
	vault := newMemoryVault()
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{Endpoint: "unrelated"}}, nil
		},
	}
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
	})

	sess, err := p.Session(context.Background(), true, "", false)
	if err != nil || sess != nil {
		t.Fatalf("expected nil session, got sess=%#v err=%v", sess, err)
	}
	if _, err := p.Release(context.Background(), "", nil); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if len(client.unassigned) != 0 {
		t.Fatalf("must never unassign unrelated runtime: %#v", client.unassigned)
	}
}

func TestReleaseFailureRetainsOwnership(t *testing.T) {
	vault := newMemoryVault()
	storeLegacySession(vault, "owned")

	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return nil, errors.New("offline")
		},
	}
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
	})

	if _, err := p.Release(context.Background(), "", nil); err == nil {
		t.Fatalf("expected error from Release when offline")
	}
	if p.ActiveEndpoint() != "owned" {
		t.Fatalf("ownership must be retained on release failure")
	}
}

func TestReleaseConfirmsDisappearanceWhenRemoteGuardWinsRace(t *testing.T) {
	vault := newMemoryVault()
	storeLegacySession(vault, "owned")

	calls := 0
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			calls++
			if calls == 1 {
				return []ListedAssignment{{Endpoint: "owned"}}, nil
			}
			return nil, nil
		},
		unassignFn: func(ctx context.Context, endpoint string) error {
			return errors.New("already removed")
		},
	}
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:               vault,
		ClientFactory:       func(string) (ColabClient, error) { return client, nil },
		ReleasePollInterval: time.Millisecond,
	})

	res, err := p.Release(context.Background(), "", nil)
	if err != nil || res["ok"] != true {
		t.Fatalf("expected ok release despite unassign error: res=%#v err=%v", res, err)
	}
	if vault.Has("runtime_session") {
		t.Fatalf("expected session cleared")
	}
}

func TestReleaseRetriesUnassignAtAttempts2And6(t *testing.T) {
	vault := newMemoryVault()
	storeLegacySession(vault, "owned")

	listCalls := 0
	unassignCalls := 0
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			listCalls++
			// Initial check + 4 poll iterations still present, disappears on 5th poll (attempt 4)
			if listCalls <= 5 {
				return []ListedAssignment{{Endpoint: "owned"}}, nil
			}
			return nil, nil
		},
		unassignFn: func(ctx context.Context, endpoint string) error {
			unassignCalls++
			if unassignCalls == 1 {
				return errors.New("first unassign lost")
			}
			return nil
		},
	}
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:               vault,
		ClientFactory:       func(string) (ColabClient, error) { return client, nil },
		ReleasePollInterval: time.Millisecond,
		SleepFn:             func(ctx context.Context, d time.Duration) error { return nil },
	})

	res, err := p.Release(context.Background(), "", nil)
	if err != nil || res["ok"] != true {
		t.Fatalf("expected release to succeed on retry: res=%#v err=%v", res, err)
	}
	if unassignCalls != 2 {
		t.Fatalf("expected initial unassign + retry at attempt 2 = 2 calls, got %d", unassignCalls)
	}
}

func TestExplicitReleaseAllTriesEveryReviewedInstanceAndLeavesNewOnes(t *testing.T) {
	vault := newMemoryVault()
	storeLegacySession(vault, "owned")

	listCalls := 0
	unassignCalls := 0
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			listCalls++
			if listCalls == 1 {
				return []ListedAssignment{
					{Endpoint: "owned"},
					{Endpoint: "other"},
					{Endpoint: "new-after-preview"},
				}, nil
			}
			return []ListedAssignment{{Endpoint: "new-after-preview"}}, nil
		},
		unassignFn: func(ctx context.Context, endpoint string) error {
			unassignCalls++
			if unassignCalls == 1 {
				return errors.New("lost response")
			}
			return nil
		},
	}
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:               vault,
		ClientFactory:       func(string) (ColabClient, error) { return client, nil },
		ReleasePollInterval: time.Millisecond,
		SleepFn:             func(ctx context.Context, d time.Duration) error { return nil },
	})

	res, err := p.ReleaseAll(context.Background(), []string{"owned", "other"}, nil)
	if err != nil {
		t.Fatalf("ReleaseAll failed: %v", err)
	}
	if res["remaining"] != 1 {
		t.Fatalf("expected remaining=1, got %#v", res)
	}
	if !reflect.DeepEqual(client.unassigned, []string{"other", "owned"}) {
		t.Fatalf("expected sorted unassign [other owned], got %#v", client.unassigned)
	}
	if vault.Has("runtime_session") {
		t.Fatalf("expected owned runtime_session cleared")
	}
}

func TestReleaseAllPartialFailureDoesNotClaimSuccess(t *testing.T) {
	vault := newMemoryVault()
	storeLegacySession(vault, "owned")

	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{Endpoint: "owned"}}, nil
		},
	}
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:               vault,
		ClientFactory:       func(string) (ColabClient, error) { return client, nil },
		ReleasePollInterval: time.Millisecond,
		SleepFn:             func(ctx context.Context, d time.Duration) error { return nil },
	})

	_, err := p.ReleaseAll(context.Background(), []string{"owned"}, nil)
	if err == nil || !strings.Contains(err.Error(), "仍有 1") {
		t.Fatalf("expected partial failure error containing '仍有 1', got %v", err)
	}
	if !vault.Has("runtime_session") {
		t.Fatalf("expected runtime_session retained on failure")
	}
}

func TestPerEndpointVaultStorageOrderAndLegacyMigration(t *testing.T) {
	vault := newMemoryVault()
	p := NewColabOneProvider(ColabOneProviderConfig{Vault: vault})

	for idx := 0; idx < 3; idx++ {
		ep := fmt.Sprintf("m-s-multi-%d", idx)
		err := p.SaveSession(&ColabOneSessionState{
			Name:         "colabone",
			Endpoint:     ep,
			Token:        "tok-" + ep,
			URL:          "https://" + ep + ".invalid",
			Variant:      "GPU",
			Accelerator:  "G4",
			MachineShape: "STANDARD",
		}, idx == 2)
		if err != nil {
			t.Fatalf("SaveSession(%s): %v", ep, err)
		}
	}

	var ids []string
	ok, _ := vault.GetJSON("runtime_session_ids", &ids)
	wantIDs := []string{"m-s-multi-0", "m-s-multi-1", "m-s-multi-2"}
	if !ok || !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("expected runtime_session_ids=%v, got %v", wantIDs, ids)
	}
	if vault.Has("runtime_sessions") {
		t.Fatalf("expected packed runtime_sessions key deleted")
	}
	for idx := 0; idx < 3; idx++ {
		ep := fmt.Sprintf("m-s-multi-%d", idx)
		var stored ColabOneSessionState
		if ok, err := vault.GetJSON("runtime_session:"+ep, &stored); err != nil || !ok || stored.Endpoint != ep {
			t.Fatalf("expected per-endpoint session %s, got ok=%v stored=%#v err=%v", ep, ok, stored, err)
		}
	}
	if len(p.SessionsMap()) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(p.SessionsMap()))
	}
	if p.ActiveEndpoint() != "m-s-multi-2" {
		t.Fatalf("expected active m-s-multi-2, got %s", p.ActiveEndpoint())
	}

	// SetActive switches cleanly and rejects unknown endpoint
	if ep, err := p.SetActive("m-s-multi-0"); err != nil || ep != "m-s-multi-0" {
		t.Fatalf("SetActive failed: ep=%s err=%v", ep, err)
	}
	if _, err := p.SetActive("missing-ep"); err == nil || !strings.Contains(err.Error(), "不存在或已释放") {
		t.Fatalf("expected missing endpoint error, got %v", err)
	}

	// Clearing active promotes next endpoint in deterministic order and cleans per-endpoint vault key
	if err := p.Clear("m-s-multi-0"); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	if vault.Has("runtime_session:m-s-multi-0") {
		t.Fatalf("expected deleted per-endpoint key")
	}
	if p.ActiveEndpoint() != "m-s-multi-1" {
		t.Fatalf("expected active promoted to m-s-multi-1, got %s", p.ActiveEndpoint())
	}
}

func TestClearEmptySessionsRemovesStaleRemote(t *testing.T) {
	vault := newMemoryVault()
	_ = vault.Set("remote", map[string]any{"endpoint": "stale-ep", "deadline": 0, "gpu": "G4"})
	p := NewColabOneProvider(ColabOneProviderConfig{Vault: vault})
	if err := p.Clear(""); err != nil {
		t.Fatalf("Clear('') failed: %v", err)
	}
	if vault.Has("remote") {
		t.Fatalf("expected stale remote removed")
	}
}

// ---------------------------------------------------------------------------
// 2. Account & DescribeAssignment Tests
// ---------------------------------------------------------------------------

func TestAccountLoggedOutAndManagedExternalSpecs(t *testing.T) {
	vault := newMemoryVault()
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:    vault,
		Auth:     &fakeAuth{profile: map[string]string{"email": "dev@example.com"}},
		Location: fakeLocationInferer{},
	})

	loggedOut, err := p.Account(context.Background())
	if err != nil {
		t.Fatalf("Account logged out err: %v", err)
	}
	if loggedOut["logged_in"] != false {
		t.Fatalf("expected logged_in=false, got %#v", loggedOut)
	}

	_ = vault.Set("colab", map[string]any{"token": "ok"})
	_ = vault.Set("runtime_sessions", map[string]any{
		"m-s-1": map[string]any{
			"name":     "colabone",
			"endpoint": "m-s-1",
			"token":    "t1",
			"url":      "https://r1.invalid",
		},
	})
	_ = vault.Set("active_endpoint", "m-s-1")

	client := &fakeColabClient{
		consumption: func(ctx context.Context) (*ConsumptionInfo, error) {
			return &ConsumptionInfo{PaidComputeUnitsBalance: 88.5, ConsumptionRateHourly: 3.14}, nil
		},
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{
				{
					Endpoint:     "m-s-1",
					Accelerator:  "T4",
					Variant:      "GPU",
					MachineShape: "HIGH_RAM",
				},
				{
					Endpoint:     "m-s-ext",
					Accelerator:  "A100",
					Variant:      "GPU",
					MachineShape: "STANDARD",
				},
				{
					Endpoint:         "gpu-t4-s-kkb-usw4b1-demo",
					Accelerator:      "T4",
					Variant:          "GPU",
					MachineShape:     "STANDARD",
					RuntimeProxyInfo: makeProxyInfo("gpu-t4-s-kkb-usw4b1-demo"),
				},
			}, nil
		},
	}
	p.clientFactory = func(string) (ColabClient, error) { return client, nil }

	acct, err := p.Account(context.Background())
	if err != nil {
		t.Fatalf("Account failed: %v", err)
	}
	if acct["logged_in"] != true || acct["email"] != "dev@example.com" || acct["assignments"] != 3 {
		t.Fatalf("unexpected account header: %#v", acct)
	}
	instances := acct["instances"].([]map[string]any)
	want0 := map[string]any{
		"id":          "m-s-1",
		"managed":     true,
		"active":      true,
		"accelerator": "T4",
		"variant":     "GPU",
		"shape":       "HIGH_RAM",
	}
	if !reflect.DeepEqual(instances[0], want0) {
		t.Fatalf("instance[0] mismatch:\ngot:  %#v\nwant: %#v", instances[0], want0)
	}
	want1 := map[string]any{
		"id":          "m-s-ext",
		"managed":     false,
		"active":      false,
		"accelerator": "A100",
		"variant":     "GPU",
		"shape":       "STANDARD",
	}
	if !reflect.DeepEqual(instances[1], want1) {
		t.Fatalf("instance[1] mismatch:\ngot:  %#v\nwant: %#v", instances[1], want1)
	}
	loc2, _ := instances[2]["location"].(map[string]any)
	if loc2["region"] != "内华达" || loc2["zone"] != "us-west4-b" {
		t.Fatalf("expected inferred location on instance[2], got %#v", instances[2])
	}
}

// ---------------------------------------------------------------------------
// 3. Allocate, Retry Backoff, Reconciliation, Cancel & Manual Retry-Now Tests
// ---------------------------------------------------------------------------

func TestAllocateRetries503WithReconciliationAndCongestionHintOnce(t *testing.T) {
	vault := newMemoryVault()
	var seenNBHs []string
	postCalls := 0
	var mu sync.Mutex
	clock := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	var timeline []string

	client := &fakeColabClient{
		getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
			seenNBHs = append(seenNBHs, nbh)
			return nil, fmt.Sprintf("xsrf-%d", len(seenNBHs)), nil
		},
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			postCalls++
			mu.Lock()
			if postCalls <= 3 {
				clock = clock.Add(30800 * time.Millisecond)
				mu.Unlock()
				return nil, &ColabRequestError{StatusCode: 503, Reason: "Service Unavailable"}
			}
			clock = clock.Add(3200 * time.Millisecond)
			mu.Unlock()
			return &Assignment{
				Endpoint:         "gpu-g4-s-euw4a4-queuedok",
				Accelerator:      "G4",
				Variant:          "GPU",
				RuntimeProxyInfo: makeProxyInfo("gpu-g4-s-euw4a4-queuedok"),
			}, nil
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		AssignRetryDelays: []time.Duration{
			6 * time.Second,
			12 * time.Second,
			20 * time.Second,
			30 * time.Second,
		},
		NowFn: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return clock
		},
		SleepFn: func(ctx context.Context, d time.Duration) error {
			mu.Lock()
			timeline = append(timeline, fmt.Sprintf("sleep:%d", int(d.Seconds())))
			clock = clock.Add(d)
			mu.Unlock()
			return nil
		},
	})

	var logs []string
	var stages []string
	sess, err := p.Allocate(context.Background(), "G4", AllocateOptions{
		OnLog: func(msg, src, lvl string) {
			logs = append(logs, msg)
		},
		OnStage: func(stage string) {
			stages = append(stages, stage)
			timeline = append(timeline, "stage:"+stage)
		},
	})
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}
	if sess.Endpoint != "gpu-g4-s-euw4a4-queuedok" {
		t.Fatalf("unexpected endpoint: %s", sess.Endpoint)
	}
	if postCalls != 4 {
		t.Fatalf("expected 4 post calls, got %d", postCalls)
	}
	if len(seenNBHs) != 4 {
		t.Fatalf("expected 4 getAssign calls, got %d", len(seenNBHs))
	}
	for _, nbh := range seenNBHs {
		if nbh != seenNBHs[0] {
			t.Fatalf("retries must reuse identical nbh, got %v", seenNBHs)
		}
	}

	retryLogs := 0
	congestionHints := 0
	for _, msg := range logs {
		if strings.Contains(msg, "Google Colab 当前 G4 算力紧张，暂未分配出 GPU（耗时 30.8 秒 · HTTP 503）") {
			retryLogs++
		}
		if strings.Contains(msg, "算力持续紧张") {
			congestionHints++
			if !strings.Contains(msg, "L4 / A100 / H100") {
				t.Fatalf("congestion hint missing alt GPU list: %s", msg)
			}
		}
	}
	if retryLogs != 3 {
		t.Fatalf("expected 3 retry logs, got %d (logs=%v)", retryLogs, logs)
	}
	if congestionHints != 1 {
		t.Fatalf("expected congestion hint emitted exactly once, got %d", congestionHints)
	}
	wantStages := []string{
		"Google 算力紧张，正在排队分配 G4（第 2/5 次）",
		"Google 算力紧张，正在排队分配 G4（第 3/5 次）",
		"Google 算力紧张，正在排队分配 G4（第 4/5 次）",
	}
	if !reflect.DeepEqual(stages, wantStages) {
		t.Fatalf("stages mismatch:\ngot:  %v\nwant: %v", stages, wantStages)
	}
	wantTimeline := []string{
		"sleep:6",
		"stage:Google 算力紧张，正在排队分配 G4（第 2/5 次）",
		"sleep:12",
		"stage:Google 算力紧张，正在排队分配 G4（第 3/5 次）",
		"sleep:20",
		"stage:Google 算力紧张，正在排队分配 G4（第 4/5 次）",
	}
	if !reflect.DeepEqual(timeline, wantTimeline) {
		t.Fatalf("timeline mismatch:\ngot:  %v\nwant: %v", timeline, wantTimeline)
	}
}

func TestAllocateReconcilesExistingAssignmentAfter503WithoutDoubleAllocating(t *testing.T) {
	vault := newMemoryVault()
	getCalls := 0
	postCalls := 0

	client := &fakeColabClient{
		getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
			getCalls++
			if getCalls == 1 {
				return nil, "xsrf-1", nil
			}
			return &Assignment{
				Endpoint:         "gpu-g4-s-asso1c1-reconciled",
				RuntimeProxyInfo: makeProxyInfo("gpu-g4-s-asso1c1-reconciled"),
			}, "", nil
		},
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			postCalls++
			return nil, &ColabRequestError{StatusCode: 503}
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:             vault,
		ClientFactory:     func(string) (ColabClient, error) { return client, nil },
		AssignRetryDelays: []time.Duration{0, 0},
	})

	var logs []string
	sess, err := p.Allocate(context.Background(), "G4", AllocateOptions{
		OnLog: func(msg, src, lvl string) { logs = append(logs, msg) },
	})
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}
	if sess.Endpoint != "gpu-g4-s-asso1c1-reconciled" || postCalls != 1 || getCalls != 2 {
		t.Fatalf("unexpected reconciliation result: ep=%s post=%d get=%d", sess.Endpoint, postCalls, getCalls)
	}
	found := false
	for _, l := range logs {
		if strings.Contains(l, "已通过状态核验确认 G4 GPU 实例分配成功") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected reconciliation log, got %v", logs)
	}
}

func TestAllocateFinalReconciliationAdoptsVMAfterLast503(t *testing.T) {
	vault := newMemoryVault()
	getCalls := 0
	postCalls := 0
	client := &fakeColabClient{
		getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
			getCalls++
			if getCalls <= 2 {
				return nil, fmt.Sprintf("xsrf-%d", getCalls), nil
			}
			return &Assignment{
				Endpoint:         "gpu-g4-s-usw4a1-final",
				RuntimeProxyInfo: makeProxyInfo("gpu-g4-s-usw4a1-final"),
			}, "", nil
		},
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			postCalls++
			return nil, &ColabRequestError{StatusCode: 503}
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:             vault,
		ClientFactory:     func(string) (ColabClient, error) { return client, nil },
		AssignRetryDelays: []time.Duration{0},
	})

	sess, err := p.Allocate(context.Background(), "G4", AllocateOptions{})
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}
	if sess.Endpoint != "gpu-g4-s-usw4a1-final" || postCalls != 2 || getCalls != 3 {
		t.Fatalf("unexpected final reconciliation: ep=%s post=%d get=%d", sess.Endpoint, postCalls, getCalls)
	}
}

func TestAllocateTranslatesExhausted503504412And400WithShapeFallback(t *testing.T) {
	makeFailing := func(status int) (*ColabOneProvider, *[]string) {
		var shapes []string
		client := &fakeColabClient{
			getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
				shapes = append(shapes, shape)
				return nil, "xsrf", nil
			},
			postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
				return nil, &ColabRequestError{StatusCode: status, Reason: "fail"}
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:             newMemoryVault(),
			ClientFactory:     func(string) (ColabClient, error) { return client, nil },
			AssignRetryDelays: []time.Duration{0, 0},
		})
		return p, &shapes
	}

	p503, _ := makeFailing(503)
	_, err := p503.Allocate(context.Background(), "G4", AllocateOptions{})
	if err == nil || !strings.Contains(err.Error(), "Google Colab 当前 G4 算力紧张，暂未分配出 GPU") ||
		!strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "已自动重试 2 次") {
		t.Fatalf("unexpected 503 error: %v", err)
	}

	p504, _ := makeFailing(504)
	_, err = p504.Allocate(context.Background(), "G4", AllocateOptions{})
	if err == nil || !strings.Contains(err.Error(), "分配服务繁忙或超时，暂未分配出 GPU") ||
		!strings.Contains(err.Error(), "HTTP 504") || !strings.Contains(err.Error(), "已自动重试 2 次") {
		t.Fatalf("unexpected 504 error: %v", err)
	}

	p412, _ := makeFailing(412)
	_, err = p412.Allocate(context.Background(), "G4", AllocateOptions{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 412") || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("unexpected 412 error: %v", err)
	}

	p400, shapes := makeFailing(400)
	_, err = p400.Allocate(context.Background(), "T4", AllocateOptions{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("unexpected 400 error: %v", err)
	}
	if !reflect.DeepEqual(*shapes, []string{"HIGH_RAM", ""}) {
		t.Fatalf("expected T4 to try HIGH_RAM then default fallback, got %v", *shapes)
	}

	// Unsupported GPU model
	if _, err := p400.Allocate(context.Background(), "V100", AllocateOptions{}); !errors.Is(err, ErrUnsupportedGPU) {
		t.Fatalf("expected ErrUnsupportedGPU, got %v", err)
	}
}

func TestAllocateDefaultBackoffScheduleAndCancelInterrupt(t *testing.T) {
	wantDelays := []time.Duration{
		2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		32 * time.Second, 32 * time.Second, 32 * time.Second, 32 * time.Second, 32 * time.Second,
		32 * time.Second, 32 * time.Second, 32 * time.Second, 32 * time.Second, 32 * time.Second,
		32 * time.Second, 32 * time.Second, 32 * time.Second, 32 * time.Second, 32 * time.Second,
	}
	if len(wantDelays) != COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS-1 {
		t.Fatalf("unexpected expected delay count: %d", len(wantDelays))
	}
	if !reflect.DeepEqual(DefaultAssignRetryDelays(), wantDelays) {
		t.Fatalf("default backoff mismatch: %v", DefaultAssignRetryDelays())
	}

	cancelCh := make(chan struct{})
	posts := 0
	var waited []int
	var timeline []string

	client := &fakeColabClient{
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			posts++
			return nil, fakeNetErr{msg: "remote closed"}
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         newMemoryVault(),
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		SleepFn: func(ctx context.Context, d time.Duration) error {
			sec := int(d.Seconds())
			waited = append(waited, sec)
			timeline = append(timeline, fmt.Sprintf("wait:%d", sec))
			if posts == 3 {
				close(cancelCh)
				return context.Canceled
			}
			return nil
		},
	})

	var logs []string
	var stages []string
	_, err := p.Allocate(context.Background(), "G4", AllocateOptions{
		OnLog: func(msg, src, lvl string) { logs = append(logs, msg) },
		OnStage: func(st string) {
			stages = append(stages, st)
			timeline = append(timeline, "stage:"+st)
		},
		CancelCh: cancelCh,
	})
	if !errors.Is(err, ErrAllocationCancelled) {
		t.Fatalf("expected ErrAllocationCancelled, got %v", err)
	}
	if !reflect.DeepEqual(waited, []int{2, 4, 8}) {
		t.Fatalf("expected waited=[2 4 8], got %v", waited)
	}
	wantStages := []string{"正在分配 G4（第 2/20 次）", "正在分配 G4（第 3/20 次）"}
	if !reflect.DeepEqual(stages, wantStages) {
		t.Fatalf("stages mismatch: got=%v want=%v", stages, wantStages)
	}
	wantTimeline := []string{
		"wait:2",
		"stage:正在分配 G4（第 2/20 次）",
		"wait:4",
		"stage:正在分配 G4（第 3/20 次）",
		"wait:8",
	}
	if !reflect.DeepEqual(timeline, wantTimeline) {
		t.Fatalf("timeline mismatch: got=%v want=%v", timeline, wantTimeline)
	}
	hasNetLog := false
	for _, m := range logs {
		if strings.Contains(m, "耗时") && strings.Contains(m, "网络异常") && strings.Contains(m, "第 2/20 次") {
			hasNetLog = true
		}
	}
	if !hasNetLog {
		t.Fatalf("missing network retry log: %v", logs)
	}
}

func TestAssignRetryDelaysScheduleAndCustomMaxAttempts(t *testing.T) {
	if got := AssignRetryDelays(0); len(got) != 0 {
		t.Fatalf("AssignRetryDelays(0) = %v, want empty", got)
	}
	if got := AssignRetryDelays(1); len(got) != 0 {
		t.Fatalf("AssignRetryDelays(1) = %v, want empty", got)
	}
	if got := AssignRetryDelays(2); !reflect.DeepEqual(got, []time.Duration{2 * time.Second}) {
		t.Fatalf("AssignRetryDelays(2) = %v, want [2s]", got)
	}
	want8 := []time.Duration{
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		32 * time.Second,
		32 * time.Second,
	}
	if got := AssignRetryDelays(8); !reflect.DeepEqual(got, want8) {
		t.Fatalf("AssignRetryDelays(8) = %v, want %v", got, want8)
	}

	// Custom MaxAttempts=7: verifies exponential backoff 2 -> 4 -> 8 -> 16 -> 32 -> 32 and exact stage denominators.
	posts := 0
	var waited []int
	var stages []string
	client := &fakeColabClient{
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			posts++
			return nil, &ColabRequestError{StatusCode: 503}
		},
	}
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         newMemoryVault(),
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		SleepFn: func(ctx context.Context, d time.Duration) error {
			waited = append(waited, int(d.Seconds()))
			return nil
		},
	})
	_, err := p.Allocate(context.Background(), "G4", AllocateOptions{
		MaxAttempts: 7,
		OnStage:     func(st string) { stages = append(stages, st) },
	})
	if err == nil || !strings.Contains(err.Error(), "已自动重试 6 次") {
		t.Fatalf("expected 6 retries exhausted error, got %v", err)
	}
	if posts != 7 {
		t.Fatalf("expected 7 POST attempts, got %d", posts)
	}
	// 6 backoff sleeps + 1 final 2s reconciliation pause before final GetAssignment.
	wantWaited := []int{2, 4, 8, 16, 32, 32, 2}
	if !reflect.DeepEqual(waited, wantWaited) {
		t.Fatalf("waited mismatch: got=%v want=%v", waited, wantWaited)
	}
	wantStages := []string{
		"Google 算力紧张，正在排队分配 G4（第 2/7 次）",
		"Google 算力紧张，正在排队分配 G4（第 3/7 次）",
		"Google 算力紧张，正在排队分配 G4（第 4/7 次）",
		"Google 算力紧张，正在排队分配 G4（第 5/7 次）",
		"Google 算力紧张，正在排队分配 G4（第 6/7 次）",
		"Google 算力紧张，正在排队分配 G4（第 7/7 次）",
	}
	if !reflect.DeepEqual(stages, wantStages) {
		t.Fatalf("stages mismatch: got=%v want=%v", stages, wantStages)
	}

	// Custom MaxAttempts=1: single attempt without retries.
	postsSingle := 0
	waitsSingle := 0
	clientSingle := &fakeColabClient{
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			postsSingle++
			return nil, &ColabRequestError{StatusCode: 503}
		},
	}
	pSingle := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         newMemoryVault(),
		ClientFactory: func(string) (ColabClient, error) { return clientSingle, nil },
		SleepFn: func(ctx context.Context, d time.Duration) error {
			waitsSingle++
			return nil
		},
	})
	_, errSingle := pSingle.Allocate(context.Background(), "G4", AllocateOptions{
		MaxAttempts: 1,
	})
	if errSingle == nil || strings.Contains(errSingle.Error(), "已自动重试") {
		t.Fatalf("expected single-attempt 503 error without retry count, got %v", errSingle)
	}
	if postsSingle != 1 || waitsSingle != 0 {
		t.Fatalf("expected 1 POST and 0 waits for MaxAttempts=1, got posts=%d waits=%d", postsSingle, waitsSingle)
	}
}

func TestAllocateEmitsBackoffWaitingRetryStateAndRecovers(t *testing.T) {
	cancelCh := make(chan struct{})
	postCalls := 0

	client := &fakeColabClient{
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			postCalls++
			if postCalls == 1 {
				return nil, &ColabRequestError{StatusCode: 503}
			}
			return &Assignment{
				Endpoint:         "gpu-g4-backoff-retry-ok",
				RuntimeProxyInfo: makeProxyInfo("gpu-g4-backoff-retry-ok"),
			}, nil
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:             newMemoryVault(),
		ClientFactory:     func(string) (ColabClient, error) { return client, nil },
		AssignRetryDelays: []time.Duration{20 * time.Millisecond, 40 * time.Millisecond},
	})

	var retryStates []map[string]any
	sess, err := p.Allocate(context.Background(), "G4", AllocateOptions{
		CancelCh: cancelCh,
		OnRetryState: func(info map[string]any) {
			retryStates = append(retryStates, cloneMap(info))
		},
	})
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}
	if sess.Endpoint != "gpu-g4-backoff-retry-ok" || postCalls != 2 {
		t.Fatalf("unexpected retry result: ep=%s postCalls=%d", sess.Endpoint, postCalls)
	}
	sawWaiting := false
	for _, st := range retryStates {
		if boolVal(st["waiting_retry"]) && floatVal(st["retry_at"]) > 0 && st["retry_kind"] == "capacity" && intVal(st["next_attempt"]) == 2 && intVal(st["total_attempts"]) == 3 {
			sawWaiting = true
		}
	}
	if !sawWaiting {
		t.Fatalf("expected waiting_retry backoff state: %#v", retryStates)
	}
	last := retryStates[len(retryStates)-1]
	if boolVal(last["waiting_retry"]) || intVal(last["attempt"]) != 2 {
		t.Fatalf("unexpected final retry state: %#v", last)
	}
}

// ---------------------------------------------------------------------------
// 4. Assign Proxy Parsing, Isolation, Fast-Fail & Rollback Tests
// ---------------------------------------------------------------------------

func TestParseAndMaskAssignProxyURLs(t *testing.T) {
	if spec, err := ParseAssignProxy(""); err != nil || spec != nil {
		t.Fatalf("expected nil for empty proxy, got spec=%#v err=%v", spec, err)
	}
	spec, err := ParseAssignProxy("SOCKS5://alice:s3cr%40et@us.proxy.test:1080/")
	if err != nil || spec == nil {
		t.Fatalf("ParseAssignProxy failed: %v", err)
	}
	if spec.Scheme != "socks5" || spec.Host != "us.proxy.test" || spec.Port != 1080 ||
		spec.Username != "alice" || spec.Password != "s3cr@et" || !spec.HasAuth() ||
		spec.NormalizedURL != "socks5://alice:s3cr%40et@us.proxy.test:1080" ||
		spec.MaskedURL != "socks5://us.proxy.test:1080" ||
		spec.LogLabel != "SOCKS5 us.proxy.test:1080" {
		t.Fatalf("unexpected parsed spec: %#v", spec)
	}

	atSpec, err := ParseAssignProxy("http://alice:p@ss@127.0.0.1:8080")
	if err != nil || atSpec.Password != "p@ss" || atSpec.NormalizedURL != "http://alice:p%40ss@127.0.0.1:8080" {
		t.Fatalf("raw @ in password failed: spec=%#v err=%v", atSpec, err)
	}
	emptyAt, err := ParseAssignProxy("http://@127.0.0.1:7890")
	if err != nil || emptyAt.NormalizedURL != "http://127.0.0.1:7890" || emptyAt.HasAuth() {
		t.Fatalf("empty @ failed: spec=%#v err=%v", emptyAt, err)
	}
	v6Spec, err := ParseAssignProxy("socks5h://[::1]:1081")
	if err != nil || v6Spec.LogLabel != "SOCKS5 [::1]:1081" {
		t.Fatalf("ipv6 proxy failed: spec=%#v err=%v", v6Spec, err)
	}

	badCases := []string{
		"127.0.0.1:7890",
		"ftp://127.0.0.1:21",
		"http://127.0.0.1:99999",
		"http://127.0.0.1:0",
		"http://127.0.0.1:7890/path",
		"http://127.0.0.1:7890?a=1",
		"socks5://127.0.0.1:1080 bad",
		"socks5://:1080",
		"http://..:8080",
		"http://-bad.example:8080",
		"http://[fe80::1%25lo0]:8080",
		"socks5://" + strings.Repeat("u", 256) + ":pw@127.0.0.1:1080",
	}
	for _, bad := range badCases {
		if _, err := ParseAssignProxy(bad); err == nil {
			t.Errorf("expected error for invalid proxy %q", bad)
		}
	}
}

func TestAssignProxyIsolationFastFailAndReadTimeoutRetry(t *testing.T) {
	// 1) Non-assign-proxy transient ProxyError keeps retrying
	attempts := 0
	p1 := NewColabOneProvider(ColabOneProviderConfig{
		Vault:             newMemoryVault(),
		AssignRetryDelays: []time.Duration{0, 0},
		ClientFactory: func(proxy string) (ColabClient, error) {
			return &fakeColabClient{
				getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
					attempts++
					if attempts == 1 {
						return nil, "", &AssignProxyError{Kind: "proxy", Message: "transient system proxy reset"}
					}
					return nil, "xsrf", nil
				},
				postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
					return &Assignment{
						Endpoint:         "m-s-recovered",
						RuntimeProxyInfo: makeProxyInfo("m-s-recovered"),
					}, nil
				},
			}, nil
		},
	})
	sess, err := p1.Allocate(context.Background(), "T4", AllocateOptions{})
	if err != nil || sess.Endpoint != "m-s-recovered" || attempts != 2 {
		t.Fatalf("expected non-assign-proxy transient proxy error to retry: sess=%#v attempts=%d err=%v", sess, attempts, err)
	}

	// 2) Assign-proxy ConnectTimeout fails fast, SSL fails fast, Auth fails fast, closes isolated client
	var proxyClient *fakeColabClient
	connectAttempts := 0
	p2 := NewColabOneProvider(ColabOneProviderConfig{
		Vault:             newMemoryVault(),
		AssignRetryDelays: []time.Duration{6 * time.Second, 12 * time.Second},
		ClientFactory: func(proxy string) (ColabClient, error) {
			proxyClient = &fakeColabClient{
				getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
					connectAttempts++
					return nil, "", &AssignProxyError{Kind: "connect_timeout", Message: "proxy blackhole"}
				},
			}
			return proxyClient, nil
		},
	})
	_, err = p2.Allocate(context.Background(), "T4", AllocateOptions{AssignProxy: "http://127.0.0.1:7890"})
	if err == nil || !strings.Contains(err.Error(), "连接 GPU 分配代理超时") || connectAttempts != 1 {
		t.Fatalf("expected ConnectTimeout fast-fail: err=%v attempts=%d", err, connectAttempts)
	}
	if !proxyClient.closed {
		t.Fatalf("expected isolated assign proxy client closed")
	}

	// SSL MITM error fails fast with clear message
	pSSL := NewColabOneProvider(ColabOneProviderConfig{
		Vault:             newMemoryVault(),
		AssignRetryDelays: []time.Duration{0, 0},
		ClientFactory: func(proxy string) (ColabClient, error) {
			return &fakeColabClient{
				getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
					return nil, "", x509.UnknownAuthorityError{}
				},
			}, nil
		},
	})
	_, err = pSSL.Allocate(context.Background(), "T4", AllocateOptions{AssignProxy: "http://127.0.0.1:7890"})
	if err == nil || !strings.Contains(err.Error(), "证书校验未通过") {
		t.Fatalf("expected SSL fast-fail message, got %v", err)
	}

	// 3) Assign-proxy ReadTimeout still retries!
	readAttempts := 0
	p3 := NewColabOneProvider(ColabOneProviderConfig{
		Vault:             newMemoryVault(),
		AssignRetryDelays: []time.Duration{0, 0},
		ClientFactory: func(proxy string) (ColabClient, error) {
			return &fakeColabClient{
				getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
					readAttempts++
					if readAttempts == 1 {
						return nil, "", &AssignProxyError{Kind: "read_timeout", Message: "google assign slow"}
					}
					return nil, "xsrf", nil
				},
				postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
					return &Assignment{
						Endpoint:         "m-s-after-read-timeout",
						RuntimeProxyInfo: makeProxyInfo("m-s-after-read-timeout"),
					}, nil
				},
			}, nil
		},
	})
	sess, err = p3.Allocate(context.Background(), "T4", AllocateOptions{AssignProxy: "http://127.0.0.1:7890"})
	if err != nil || sess.Endpoint != "m-s-after-read-timeout" || readAttempts != 2 {
		t.Fatalf("expected read_timeout to retry: sess=%#v attempts=%d err=%v", sess, readAttempts, err)
	}

	// 4) SaveSession failure rollback MUST use regular non-proxy client with a bounded 90s timeout context, never assign proxy client
	vaultFull := newMemoryVault()
	vaultFull.failSet = func(key string) error { return errors.New("keyring full") }
	var rollbackCtx context.Context
	var rollbackCtxErr error
	regularClient := &fakeColabClient{
		unassignFn: func(ctx context.Context, endpoint string) error {
			rollbackCtx = ctx
			rollbackCtxErr = ctx.Err()
			return nil
		},
	}
	assignClient := &fakeColabClient{
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			return &Assignment{
				Endpoint:         "m-s-rollback",
				RuntimeProxyInfo: makeProxyInfo("m-s-rollback"),
			}, nil
		},
		unassignFn: func(ctx context.Context, endpoint string) error {
			t.Fatalf("rollback unassign must NOT use assign-only proxy client")
			return nil
		},
	}
	pRollback := NewColabOneProvider(ColabOneProviderConfig{
		Vault: vaultFull,
		ClientFactory: func(proxy string) (ColabClient, error) {
			if proxy != "" {
				return assignClient, nil
			}
			return regularClient, nil
		},
	})
	_, err = pRollback.Allocate(context.Background(), "T4", AllocateOptions{AssignProxy: "http://127.0.0.1:7890"})
	if err == nil || !strings.Contains(err.Error(), "保存实例会话失败") {
		t.Fatalf("expected save session rollback error, got %v", err)
	}
	if !reflect.DeepEqual(regularClient.unassigned, []string{"m-s-rollback"}) {
		t.Fatalf("expected rollback unassign via regular client, got %v", regularClient.unassigned)
	}
	if rollbackCtx == nil {
		t.Fatalf("expected rollbackClient.Unassign to receive a non-nil context")
	}
	if rollbackCtxErr != nil {
		t.Fatalf("expected rollbackClient.Unassign context to be active during call, got %v", rollbackCtxErr)
	}
	if deadline, ok := rollbackCtx.Deadline(); !ok {
		t.Fatalf("expected rollbackClient.Unassign context to carry a bounded deadline (not context.Background())")
	} else if remaining := time.Until(deadline); remaining <= 0 || remaining > 95*time.Second {
		t.Fatalf("expected rollbackClient.Unassign deadline within (0, 95s], got remaining=%v", remaining)
	}
	if !assignClient.closed {
		t.Fatalf("expected assign proxy client closed")
	}
}

// ---------------------------------------------------------------------------
// 5. Start, Ready, Boot Streaming, Rollback & UpgradeService Tests
// ---------------------------------------------------------------------------

func TestStartStreamsTelemetryAndLogsIgnoresStderrAndSplitReady(t *testing.T) {
	srv := makeReadyServer(t, nil)
	vault := newMemoryVault()
	_ = vault.Set("colab", map[string]any{"token": "ok"})
	contents := &fakeContents{}
	var proxyURLs []string

	client := &fakeColabClient{
		consumption: func(ctx context.Context) (*ConsumptionInfo, error) {
			return &ConsumptionInfo{PaidComputeUnitsBalance: 100.0, ConsumptionRateHourly: 0}, nil
		},
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			return &Assignment{
				Endpoint: "gpu-t4-s-kkb-ass1a1-split",
				RuntimeProxyInfo: &RuntimeProxyInfo{
					URL:                   srv.URL,
					Token:                 "tok-split",
					TokenExpiresInSeconds: 3600,
				},
			}, nil
		},
	}

	sample := map[string]any{
		"cpu":     map[string]any{"cores": 2, "percent": 15.0},
		"history": []map[string]any{{"t": 1.0, "cpu_pct": 15.0}},
	}
	sampleBytes, _ := json.Marshal(sample)
	sampleLine := "\nCOLABONE_TELEMETRY=" + string(sampleBytes) + "\n"
	logLine := "\nCOLABONE_LOG=" + `{"stage":"安装远端服务依赖","message":"正在安装依赖","source":"远端","level":"info"}` + "\n"

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault: vault,
		Auth:  &fakeAuth{profile: map[string]string{"email": "dev@example.com"}},
		ClientFactory: func(proxy string) (ColabClient, error) {
			proxyURLs = append(proxyURLs, proxy)
			return client, nil
		},
		Contents: contents,
		Bundler:  &fakeBundler{bootScript: "# boot"},
		Location: fakeLocationInferer{},
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				if eo.OutputHook != nil {
					eo.OutputHook(KernelOutput{OutputType: "stream", Name: "stdout", Text: sampleLine[:25]})
					eo.OutputHook(KernelOutput{OutputType: "stream", Name: "stderr", Text: "WARNING: pip"})
					eo.OutputHook(KernelOutput{OutputType: "stream", Name: "stdout", Text: sampleLine[25:] + logLine})
				}
				return []KernelOutput{
					{OutputType: "stream", Name: "stdout", Text: "\nCOLABONE_"},
					{OutputType: "stream", Name: "stderr", Text: "noise"},
					{OutputType: "stream", Name: "stdout", Text: "READY=1\n"},
				}, nil
			}
			return fk
		},
	})

	var receivedTelemetry []map[string]any
	var stages []string
	var logs []string
	remote, err := p.Start(
		context.Background(),
		"T4",
		func(st string) { stages = append(stages, st) },
		StartOptions{
			RcloneConf:  "[colabone_drive]\ntype = drive\n",
			AssignProxy: "socks5://user:secretpw@127.0.0.1:1080",
			OnTelemetry: func(ep string, loc, s map[string]any) {
				if ep != "gpu-t4-s-kkb-ass1a1-split" || loc["country"] != "IN" {
					t.Errorf("unexpected telemetry metadata: ep=%s loc=%#v", ep, loc)
				}
				receivedTelemetry = append(receivedTelemetry, s)
			},
			OnLog: func(msg, src, lvl string) { logs = append(logs, msg) },
		},
	)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if remote["endpoint"] != "gpu-t4-s-kkb-ass1a1-split" || intVal(remote["deadline"]) != 0 {
		t.Fatalf("unexpected remote: %#v", remote)
	}
	if len(receivedTelemetry) != 1 {
		t.Fatalf("expected 1 streamed telemetry sample, got %d", len(receivedTelemetry))
	}
	if len(contents.uploads) != 3 {
		t.Fatalf("expected 3 uploaded files (zip, boot.json, rclone.conf), got %d", len(contents.uploads))
	}
	if contents.uploads[0].remotePath != "content/colabone.zip" ||
		contents.uploads[1].remotePath != "content/colabone-boot.json" ||
		contents.uploads[2].remotePath != "content/.colabone_rclone.conf" {
		t.Fatalf("unexpected upload paths: %#v", contents.uploads)
	}
	// Verify proxy used ONLY for allocation and secret never leaked into logs
	proxyCount := 0
	for _, pr := range proxyURLs {
		if pr != "" {
			proxyCount++
		}
	}
	if proxyCount != 1 {
		t.Fatalf("expected assign proxy used exactly once, got %v", proxyURLs)
	}
	for _, msg := range logs {
		if strings.Contains(msg, "secretpw") {
			t.Fatalf("proxy secret leaked in log: %s", msg)
		}
	}
	sawBootStage := false
	for _, st := range stages {
		if st == "安装远端服务依赖" {
			sawBootStage = true
		}
	}
	if !sawBootStage {
		t.Fatalf("expected streamed COLABONE_LOG stage, got %v", stages)
	}
}

func TestZeroRateStartsWithoutMaxCUAndSetsZeroDeadline(t *testing.T) {
	srv := makeReadyServer(t, nil)
	vault := newMemoryVault()
	_ = vault.Set("colab", map[string]any{"token": "ok"})
	contents := &fakeContents{}

	client := &fakeColabClient{
		consumption: func(ctx context.Context) (*ConsumptionInfo, error) {
			return &ConsumptionInfo{PaidComputeUnitsBalance: 100.0, ConsumptionRateHourly: 0}, nil
		},
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			return &Assignment{
				Endpoint: "new",
				RuntimeProxyInfo: &RuntimeProxyInfo{
					URL:                   srv.URL,
					Token:                 "tok-zero-rate",
					TokenExpiresInSeconds: 3600,
				},
			}, nil
		},
		unassignFn: func(ctx context.Context, endpoint string) error {
			t.Fatalf("unexpected Unassign(%q) during zero-rate Start", endpoint)
			return nil
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		Auth:          &fakeAuth{profile: map[string]string{"email": "free@example.com"}},
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		Contents:      contents,
		Bundler:       &fakeBundler{bootScript: "# boot"},
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				return []KernelOutput{
					{OutputType: "stream", Name: "stdout", Text: "COLABONE_READY=1\n"},
				}, nil
			}
			return fk
		},
	})

	var logs []string
	remote, err := p.Start(context.Background(), "T4", func(string) {}, StartOptions{
		OnLog: func(msg, src, lvl string) { logs = append(logs, msg) },
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if remote["endpoint"] != "new" {
		t.Fatalf("remote endpoint = %v, want %q", remote["endpoint"], "new")
	}
	if got := intVal(remote["deadline"]); got != 0 {
		t.Fatalf("remote deadline = %d, want 0", got)
	}
	if got := floatVal(remote["rate"]); got != 0 {
		t.Fatalf("remote rate = %v, want 0", got)
	}
	if _, hasMaxCU := remote["max_cu"]; hasMaxCU {
		t.Fatalf("remote must not contain max_cu key, got %#v", remote)
	}

	stored := p.RemoteInfo("new")
	if stored == nil {
		t.Fatalf("expected stored remote info for %q", "new")
	}
	if got := intVal(stored["deadline"]); got != 0 {
		t.Fatalf("stored deadline = %d, want 0", got)
	}
	if got := floatVal(stored["rate"]); got != 0 {
		t.Fatalf("stored rate = %v, want 0", got)
	}
	if _, hasMaxCU := stored["max_cu"]; hasMaxCU {
		t.Fatalf("stored remote must not contain max_cu key, got %#v", stored)
	}

	var bootCfg map[string]any
	foundBootJSON := false
	for _, up := range contents.uploads {
		if up.remotePath == "content/colabone-boot.json" {
			foundBootJSON = true
			if err := json.Unmarshal(up.data, &bootCfg); err != nil {
				t.Fatalf("unmarshal colabone-boot.json: %v", err)
			}
		}
	}
	if !foundBootJSON {
		t.Fatalf("expected content/colabone-boot.json upload, got %#v", contents.uploads)
	}
	if got := intVal(bootCfg["deadline"]); got != 0 {
		t.Fatalf("boot config deadline = %d, want 0", got)
	}
	if _, hasMaxCU := bootCfg["max_cu"]; hasMaxCU {
		t.Fatalf("boot config must not contain max_cu key, got %#v", bootCfg)
	}
	if len(client.unassigned) != 0 {
		t.Fatalf("expected no rollback unassign calls, got %v", client.unassigned)
	}
	for _, msg := range logs {
		if strings.Contains(msg, "已分配实例 new") && strings.Contains(msg, "CU/小时") {
			t.Fatalf("zero-rate allocation log should omit CU/小时: %q", msg)
		}
	}
}

func TestStartSurfacesBootErrorAndRollsBackRestoringPreviousActive(t *testing.T) {
	srv := makeReadyServer(t, nil)
	vault := newMemoryVault()
	_ = vault.Set("colab", map[string]any{"token": "ok"})

	assignments := []ListedAssignment{{Endpoint: "ep-existing"}}
	var mu sync.Mutex
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			mu.Lock()
			defer mu.Unlock()
			out := make([]ListedAssignment, len(assignments))
			copy(out, assignments)
			return out, nil
		},
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			mu.Lock()
			assignments = append(assignments, ListedAssignment{Endpoint: "gpu-g4-booterr"})
			mu.Unlock()
			return &Assignment{
				Endpoint: "gpu-g4-booterr",
				RuntimeProxyInfo: &RuntimeProxyInfo{
					URL:                   srv.URL,
					Token:                 "tok-booterr",
					TokenExpiresInSeconds: 3600,
				},
			}, nil
		},
		unassignFn: func(ctx context.Context, endpoint string) error {
			mu.Lock()
			filtered := assignments[:0]
			for _, a := range assignments {
				if a.Endpoint != endpoint {
					filtered = append(filtered, a)
				}
			}
			assignments = filtered
			mu.Unlock()
			return nil
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:               vault,
		ClientFactory:       func(string) (ColabClient, error) { return client, nil },
		Contents:            &fakeContents{},
		Bundler:             &fakeBundler{bootScript: "# boot"},
		ReleasePollInterval: time.Millisecond,
		SleepFn:             func(ctx context.Context, d time.Duration) error { return nil },
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				return []KernelOutput{
					{OutputType: "error", EName: "RuntimeError", EValue: "pip dependency wheel failed"},
				}, nil
			}
			return fk
		},
	})

	// Seed an existing managed GPU instance before adding a second one
	_ = p.SaveSession(&ColabOneSessionState{
		Endpoint: "ep-existing",
		Token:    "tok-existing",
		URL:      srv.URL,
	}, true)
	_ = p.SaveRemote(map[string]any{"endpoint": "ep-existing", "gpu": "T4"}, true)

	_, err := p.Start(context.Background(), "G4", nil, StartOptions{CreateNew: true})
	if err == nil || !strings.Contains(err.Error(), "远端初始化未完成：RuntimeError: pip dependency wheel failed") {
		t.Fatalf("expected surfaced boot error, got %v", err)
	}
	if !reflect.DeepEqual(client.unassigned, []string{"gpu-g4-booterr"}) {
		t.Fatalf("expected rollback release of newly allocated VM only, got %v", client.unassigned)
	}
	if p.ActiveEndpoint() != "ep-existing" {
		t.Fatalf("expected previous active endpoint restored, got %s", p.ActiveEndpoint())
	}
	if !reflect.DeepEqual(p.ManagedEndpoints(), []string{"ep-existing"}) {
		t.Fatalf("expected only ep-existing remaining, got %v", p.ManagedEndpoints())
	}
}

func TestUpgradeServiceStreamsLogsAndVerifiesUpdatedMarker(t *testing.T) {
	vault := newMemoryVault()
	exp := time.Now().Add(time.Hour)
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{
				Endpoint:         "ep-up",
				RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep-up.invalid", Token: "tok-up", ExpiresAt: exp},
			}}, nil
		},
	}
	contents := &fakeContents{}
	failUpdate := false

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		Contents:      contents,
		Bundler:       &fakeBundler{updateScript: "# update"},
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				if eo.OutputHook != nil {
					eo.OutputHook(KernelOutput{
						OutputType: "stream",
						Name:       "stdout",
						Text:       "COLABONE_LOG=" + `{"message":"新版服务健康检查通过","source":"远端","level":"info"}` + "\n",
					})
				}
				if failUpdate {
					return []KernelOutput{{OutputType: "stream", Name: "stdout", Text: "partial\n"}}, nil
				}
				return []KernelOutput{{OutputType: "stream", Name: "stdout", Text: "COLABONE_UPDATED=1\n"}}, nil
			}
			return fk
		},
	})
	_ = p.SaveSession(&ColabOneSessionState{
		Endpoint:       "ep-up",
		Token:          "tok-up",
		URL:            "https://ep-up.invalid",
		TokenExpiresAt: &exp,
	}, true)

	var logs []string
	if err := p.UpgradeService(context.Background(), "", func(msg, src, lvl string) { logs = append(logs, msg) }); err != nil {
		t.Fatalf("UpgradeService failed: %v", err)
	}
	if len(contents.uploads) != 1 || contents.uploads[0].remotePath != "content/colabone-update.zip" {
		t.Fatalf("expected upload of colabone-update.zip, got %#v", contents.uploads)
	}
	foundStreamLog := false
	for _, m := range logs {
		if m == "新版服务健康检查通过" {
			foundStreamLog = true
		}
	}
	if !foundStreamLog {
		t.Fatalf("expected streamed upgrade log, got %v", logs)
	}

	// Missing COLABONE_UPDATED=1 returns clear Chinese error
	failUpdate = true
	if err := p.UpgradeService(context.Background(), "", nil); err == nil || !strings.Contains(err.Error(), "远端服务更新未完成") {
		t.Fatalf("expected incomplete update error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 6. Kernel Reuse, Rotation, Late Callbacks & Proxy Refresh Race Regressions
// ---------------------------------------------------------------------------

func TestKernelConnectionReusedAndRotatedWithoutReplaying(t *testing.T) {
	vault := newMemoryVault()
	storeLegacySession(vault, "owned")

	var created []*fakeKernel
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault: vault,
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			if len(created) == 0 {
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					return []KernelOutput{{Text: "first"}}, nil
				}
			} else {
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					return nil, fakeTimeoutErr{msg: "uncertain mutation outcome"}
				}
			}
			created = append(created, fk)
			return fk
		},
	})

	sess, _ := p.Session(context.Background(), false, "", false)
	if _, err := p.Execute(context.Background(), sess, "pass", ExecuteOptions{}); err != nil {
		t.Fatalf("exec 1: %v", err)
	}
	if _, err := p.Execute(context.Background(), sess, "pass", ExecuteOptions{}); err != nil {
		t.Fatalf("exec 2: %v", err)
	}
	if len(created) != 1 || len(created[0].execCalls) != 2 {
		t.Fatalf("expected single reused kernel with 2 calls, got kernels=%d calls=%d", len(created), len(created[0].execCalls))
	}

	sess.Token = "rotated"
	if _, err := p.Execute(context.Background(), sess, "mutate()", ExecuteOptions{}); err == nil {
		t.Fatalf("expected timeout error on second kernel")
	}
	if len(created) != 2 {
		t.Fatalf("expected rotated second kernel")
	}
	if !reflect.DeepEqual(created[0].stopCalls, []bool{false}) {
		t.Fatalf("first kernel must stop without shutdown: %v", created[0].stopCalls)
	}
	if len(created[1].execCalls) != 1 || !reflect.DeepEqual(created[1].stopCalls, []bool{false}) {
		t.Fatalf("second kernel must not replay and must stop without shutdown: calls=%d stops=%v", len(created[1].execCalls), created[1].stopCalls)
	}
	if p.KernelRuntime() != nil {
		t.Fatalf("expected KernelRuntime nil after failure")
	}
}

func TestLateKernelCallbacksCannotRestoreStaleCredentialsOrReleasedSession(t *testing.T) {
	for _, released := range []bool{false, true} {
		vault := newMemoryVault()
		storeLegacySession(vault, "owned")
		var capturedCallback func(string)

		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault: vault,
			KernelFactory: func(opts KernelOptions) KernelClient {
				capturedCallback = opts.OnKernelStarted
				return newFakeKernel(opts)
			},
		})

		sess, _ := p.Session(context.Background(), false, "", false)
		if _, err := p.Execute(context.Background(), sess, "pass", ExecuteOptions{}); err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if released {
			_ = p.Clear("")
		} else {
			renewed, _ := p.Session(context.Background(), false, "", false)
			renewed.Token = "renewed-token"
			_ = p.SaveSession(renewed, false)
		}

		// Fire late callback after release or token renewal
		capturedCallback("kernel-id-late")

		if released {
			if vault.Has("runtime_session") {
				t.Fatalf("late callback resurrected released session!")
			}
		} else {
			cur, _ := p.Session(context.Background(), false, "", false)
			if cur == nil || cur.Token != "renewed-token" || cur.KernelID != "kernel-id-late" {
				t.Fatalf("expected renewed token preserved with new kernel_id, got %#v", cur)
			}
		}
	}
}

func TestProxyRefreshInFlightCannotResurrectReleasedSession(t *testing.T) {
	vault := newMemoryVault()
	storeLegacySession(vault, "owned")

	var p *ColabOneProvider
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			_ = p.Clear("owned")
			return []ListedAssignment{{
				Endpoint:         "owned",
				RuntimeProxyInfo: makeProxyInfo("owned"),
			}}, nil
		},
	}
	p = NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
	})

	sess, err := p.Session(context.Background(), true, "", false)
	if err != nil || sess != nil {
		t.Fatalf("expected nil session after concurrent release, got sess=%#v err=%v", sess, err)
	}
	if vault.Has("runtime_session") {
		t.Fatalf("in-flight proxy refresh resurrected released session!")
	}
}

// ---------------------------------------------------------------------------
// 7. RPC Tunnel, Token Pre-Refresh, Idempotent Retry & Ephemeral Cleanup Tests
// ---------------------------------------------------------------------------

func TestRPCRefreshesExpiredProxyBeforeSendingMutation(t *testing.T) {
	vault := newMemoryVault()
	expired := time.Now().Add(-time.Second)
	renewedExp := time.Now().Add(time.Hour)
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{
				Endpoint: "owned",
				RuntimeProxyInfo: &RuntimeProxyInfo{
					URL:       "https://new-runtime.invalid",
					Token:     "renewed-token",
					ExpiresAt: renewedExp,
				},
			}}, nil
		},
	}
	contents := &fakeContents{}
	var seenToken string
	execCount := 0

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		Contents:      contents,
		KernelFactory: func(opts KernelOptions) KernelClient {
			seenToken = opts.ProxyToken
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				execCount++
				return []KernelOutput{{Text: `COLABONE_RPC={"ok":true,"value":{"unloaded":true}}`}}, nil
			}
			return fk
		},
	})
	_ = p.SaveSession(&ColabOneSessionState{
		Endpoint:       "owned",
		Token:          "stale-token",
		URL:            "https://old-runtime.invalid",
		TokenExpiresAt: &expired,
	}, true)

	val, err := p.RPC(context.Background(), "POST", "unload", map[string]any{"force": true}, "", 75*time.Second)
	if err != nil {
		t.Fatalf("RPC failed: %v", err)
	}
	m, _ := val.(map[string]any)
	if m["unloaded"] != true {
		t.Fatalf("unexpected RPC value: %#v", val)
	}
	if len(contents.uploads) != 1 || contents.uploads[0].proxyToken != "renewed-token" {
		t.Fatalf("expected upload with renewed-token, got %#v", contents.uploads)
	}
	if seenToken != "renewed-token" || execCount != 1 {
		t.Fatalf("expected single execution with renewed-token, got token=%s count=%d", seenToken, execCount)
	}
}

func TestRPCRetriesIdempotentOnTransientDisconnectOnlyAndCleansEphemeralFile(t *testing.T) {
	vault := newMemoryVault()
	exp := time.Now().Add(50 * time.Minute)
	contents := &fakeContents{}
	execCalls := 0
	var seenTimeout time.Duration
	failAll := false

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:    vault,
		Contents: contents,
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				execCalls++
				seenTimeout = eo.Timeout
				if failAll || execCalls == 1 {
					return nil, errors.New("Connection was lost.")
				}
				return []KernelOutput{{Text: "COLABONE_RPC_PING=1\nCOLABONE_RPC={\"ok\":true,\"value\":{\"connected\":true}}\n"}}, nil
			}
			return fk
		},
	})
	_ = p.SaveSession(&ColabOneSessionState{
		Endpoint:       "ep1",
		Token:          "tok-valid",
		URL:            "https://runtime.invalid",
		TokenExpiresAt: &exp,
	}, true)

	// Idempotent POST drive/connect retries once cleanly
	res, err := p.RPC(context.Background(), "POST", "drive/connect", nil, "", 180*time.Second)
	if err != nil {
		t.Fatalf("idempotent RPC failed: %v", err)
	}
	if res.(map[string]any)["connected"] != true || execCalls != 2 || seenTimeout != 180*time.Second {
		t.Fatalf("unexpected idempotent RPC result: res=%#v calls=%d timeout=%v", res, execCalls, seenTimeout)
	}

	// Non-idempotent mutation must NOT retry and must clean up ephemeral uploaded body file!
	execCalls = 0
	failAll = true
	_, err = p.RPC(context.Background(), "POST", "models/m1/download", map[string]any{"id": "m1"}, "", 75*time.Second)
	if err == nil || !strings.Contains(err.Error(), "Connection was lost") {
		t.Fatalf("expected Connection was lost error, got %v", err)
	}
	if execCalls != 1 {
		t.Fatalf("non-idempotent RPC must not retry, got %d calls", execCalls)
	}
	if len(contents.uploads) != 1 || len(contents.deletes) != 1 || contents.uploads[0].remotePath != contents.deletes[0] {
		t.Fatalf("expected ephemeral RPC file cleaned up on error: uploads=%#v deletes=%#v", contents.uploads, contents.deletes)
	}
}

// ---------------------------------------------------------------------------
// 8. Heartbeat & One-Time Location Probe Tests
// ---------------------------------------------------------------------------

func TestHeartbeatKeepAliveOneTimeLocationProbeAndPass(t *testing.T) {
	vault := newMemoryVault()
	exp := time.Now().Add(time.Hour)
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{
				Endpoint:         "m-s-unknown-zone",
				RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://rt.invalid", Token: "tok", ExpiresAt: exp},
			}}, nil
		},
	}
	var executed []string
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		Location:      fakeLocationInferer{},
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				executed = append(executed, code)
				if strings.Contains(code, "COLABONE_LOC=") {
					return []KernelOutput{{Text: `COLABONE_LOC={"zone":"projects/1/zones/europe-west4-a"}` + "\n"}}, nil
				}
				return nil, nil
			}
			return fk
		},
	})
	_ = p.SaveSession(&ColabOneSessionState{
		Endpoint:       "m-s-unknown-zone",
		Token:          "tok",
		URL:            "https://rt.invalid",
		TokenExpiresAt: &exp,
	}, true)
	_ = p.SaveRemote(map[string]any{"endpoint": "m-s-unknown-zone", "gpu": "T4"}, true)

	// First heartbeat probes location and persists it into remote metadata
	if err := p.Heartbeat(context.Background(), ""); err != nil {
		t.Fatalf("Heartbeat 1 failed: %v", err)
	}
	rem := p.RemoteInfo("m-s-unknown-zone")
	loc, _ := rem["location"].(map[string]any)
	if loc["country"] != "NL" || loc["region"] != "埃姆斯哈文" {
		t.Fatalf("expected probed location saved, got %#v", rem)
	}

	// Second heartbeat executes lightweight "pass" instead of re-probing
	if err := p.Heartbeat(context.Background(), ""); err != nil {
		t.Fatalf("Heartbeat 2 failed: %v", err)
	}
	if len(client.keepAlives) != 2 {
		t.Fatalf("expected 2 KeepAlive calls, got %v", client.keepAlives)
	}
	if len(executed) != 2 || !strings.Contains(executed[0], "COLABONE_LOC=") || executed[1] != "pass" {
		t.Fatalf("expected [probe, pass], got %v", executed)
	}
}

// ---------------------------------------------------------------------------
// 9. External Observer Isolated Kernel Modes, Rotation & Cleanup Tests
// ---------------------------------------------------------------------------

func TestExternalObserverIsolatedKernelModesRotationAndCleanup(t *testing.T) {
	vault := newMemoryVault()
	_ = vault.Set("colab", map[string]any{"token": "ok"})

	proxies := map[string]*RuntimeProxyInfo{
		"gpu-g4-s-asso1c1-ext1":  {URL: "https://ext1.invalid", Token: "tok-1", TokenExpiresInSeconds: 3600},
		"gpu-a100-s-usw1b1-ext2": {URL: "https://ext2.invalid", Token: "tok-2", TokenExpiresInSeconds: 3600},
	}
	var proxiesMu sync.Mutex

	client := &fakeColabClient{
		consumption: func(ctx context.Context) (*ConsumptionInfo, error) {
			return &ConsumptionInfo{PaidComputeUnitsBalance: 2000.0, ConsumptionRateHourly: 10.05}, nil
		},
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			proxiesMu.Lock()
			defer proxiesMu.Unlock()
			out := make([]ListedAssignment, 0, len(proxies))
			for ep, info := range proxies {
				accel := "A100"
				if strings.Contains(ep, "g4") {
					accel = "G4"
				}
				cp := *info
				out = append(out, ListedAssignment{
					Endpoint:         ep,
					Accelerator:      accel,
					Variant:          "GPU",
					MachineShape:     "STANDARD",
					RuntimeProxyInfo: &cp,
				})
			}
			return out, nil
		},
	}

	var created []*fakeKernel
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		Auth:          &fakeAuth{profile: map[string]string{"email": "dev@example.com"}},
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		Location:      fakeLocationInferer{},
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			idx := len(created) + 1
			created = append(created, fk)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				if opts.OnKernelStarted != nil && opts.KernelID == "" {
					opts.OnKernelStarted(fmt.Sprintf("kid-%d", idx))
				}
				if opts.OnSessionStarted != nil && opts.SessionID == "" {
					opts.OnSessionStarted(fmt.Sprintf("sid-%d", idx))
				}
				var payload map[string]any
				if strings.Contains(opts.ServerURL, "ext1") {
					payload = map[string]any{
						"source": "remote_readonly",
						"gpu":    map[string]any{"name": "NVIDIA RTX PRO 6000", "total_mib": 98304, "used_mib": 24576},
						"resources": map[string]any{
							"cpu":    map[string]any{"cores": 12, "percent": 38.5},
							"memory": map[string]any{"total_bytes": 64 << 30, "percent": 25.0},
							"disk":   map[string]any{"total_bytes": 200 << 30, "free_bytes": 150 << 30, "percent": 25.0},
						},
						"disk_free": 150 << 30,
						"zone":      "projects/1/zones/asia-south1-c",
					}
				} else {
					payload = map[string]any{
						"source":            "remote_colabone",
						"loaded_model_name": "Qwen3.8-27B",
						"status": map[string]any{
							"state":        "ready",
							"loaded_model": "qwen38-27b",
							"gpu":          map[string]any{"name": "NVIDIA A100-SXM4-40GB", "total_mib": 40960, "used_mib": 18000},
							"drive":        map[string]any{"connected": true, "free_bytes": 50 << 30},
						},
					}
				}
				raw, _ := json.Marshal(payload)
				return []KernelOutput{{OutputType: "stream", Name: "stdout", Text: "\nCOLABONE_OBS=" + string(raw) + "\n"}}, nil
			}
			return fk
		},
	})

	// Populate proxy cache via Account() without adopting anything into vault
	acct, err := p.Account(context.Background())
	if err != nil || acct["assignments"] != 2 {
		t.Fatalf("Account failed: acct=%#v err=%v", acct, err)
	}
	if len(p.ManagedEndpoints()) != 0 {
		t.Fatalf("Account must never adopt external instances")
	}

	// 1) Generic Colab Web / external runtime observation
	obs1, err := p.ObserveExternalTelemetry(context.Background(), "gpu-g4-s-asso1c1-ext1")
	if err != nil {
		t.Fatalf("ObserveExternalTelemetry ext1 failed: %v", err)
	}
	if obs1["source"] != "remote_readonly" || obs1["drive_connected"] != false {
		t.Fatalf("unexpected obs1: %#v", obs1)
	}
	rt1 := obs1["runtime"].(map[string]any)
	if rt1["gpu"].(map[string]any)["name"] != "NVIDIA RTX PRO 6000" || rt1["location"].(map[string]any)["country"] != "IN" {
		t.Fatalf("unexpected obs1 runtime: %#v", rt1)
	}
	if created[0].opts.SessionName != "colabone-observer" || created[0].opts.KernelID != "" || !created[0].opts.ShutdownOnWSTimeout {
		t.Fatalf("unexpected observer kernel opts: %#v", created[0].opts)
	}
	if len(p.ManagedEndpoints()) != 0 {
		t.Fatalf("observer must never adopt external runtime")
	}

	// 2) Same endpoint with rotated token reconnects to our own observer kernel_id without shutting it down
	p.proxyMu.Lock()
	rotated := p.externalProxies["gpu-g4-s-asso1c1-ext1"]
	rotated.token = "tok-1-rotated"
	p.externalProxies["gpu-g4-s-asso1c1-ext1"] = rotated
	p.proxyMu.Unlock()

	if _, err := p.ObserveExternalTelemetry(context.Background(), "gpu-g4-s-asso1c1-ext1"); err != nil {
		t.Fatalf("ObserveExternalTelemetry rotated failed: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2nd observer kernel on token rotation, got %d", len(created))
	}
	if !reflect.DeepEqual(created[0].stopCalls, []bool{false}) {
		t.Fatalf("rotated token must NOT shut down observer kernel: %v", created[0].stopCalls)
	}
	if created[1].opts.KernelID != "kid-1" || created[1].opts.SessionID != "sid-1" {
		t.Fatalf("expected reconnect with kid-1/sid-1, got %#v", created[1].opts)
	}

	// 3) Switching to another external endpoint shuts down old observer kernel cleanly
	obs2, err := p.ObserveExternalTelemetry(context.Background(), "gpu-a100-s-usw1b1-ext2")
	if err != nil {
		t.Fatalf("ObserveExternalTelemetry ext2 failed: %v", err)
	}
	if obs2["source"] != "remote_colabone" || obs2["loaded_model_name"] != "Qwen3.8-27B" || obs2["drive_connected"] != true {
		t.Fatalf("unexpected obs2: %#v", obs2)
	}
	if !reflect.DeepEqual(created[1].stopCalls, []bool{true}) {
		t.Fatalf("switching endpoint must shut down old observer kernel: %v", created[1].stopCalls)
	}
	if created[2].opts.KernelID != "" {
		t.Fatalf("new endpoint must start fresh observer kernel, got %s", created[2].opts.KernelID)
	}

	// 4) Explicit CloseObserver shuts down active observer kernel
	p.CloseObserver("", true)
	if !reflect.DeepEqual(created[2].stopCalls, []bool{true}) {
		t.Fatalf("CloseObserver must shut down observer kernel: %v", created[2].stopCalls)
	}

	// 5) Disappeared assignment returns ErrExternalEndpointGone
	proxiesMu.Lock()
	delete(proxies, "gpu-a100-s-usw1b1-ext2")
	proxiesMu.Unlock()
	p.evictExternalProxy("gpu-a100-s-usw1b1-ext2")

	_, err = p.ObserveExternalTelemetry(context.Background(), "gpu-a100-s-usw1b1-ext2")
	if !errors.Is(err, ErrExternalEndpointGone) {
		t.Fatalf("expected ErrExternalEndpointGone, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 10. Drive Config Upload, CheckColabDriveFSAuth & MountColabDriveFS Tests
// ---------------------------------------------------------------------------

func TestDriveConfigUploadCheckAuthAndMountColabDriveFS(t *testing.T) {
	vault := newMemoryVault()
	exp := time.Now().Add(time.Hour)
	contents := &fakeContents{}
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{
				Endpoint:         "ep-drive",
				RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep-drive.invalid", Token: "tok-drive", ExpiresAt: exp},
			}}, nil
		},
	}

	mounter := &fakeDriveMounter{
		dryrunOK:  false,
		dryrunURL: "https://accounts.google.com/o/oauth2/v2/auth",
	}
	var mountKernel *fakeKernel
	mountOutputs := []KernelOutput{{OutputType: "stream", Name: "stdout", Text: "COLABONE_DRIVEFS_OK=1\n"}}
	var mountExecErr error

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		Contents:      contents,
		DriveMounterFactory: func(c ColabClient, ep string, opts DriveMountOptions) DriveMounter {
			mounter.capturedOpt = opts
			return mounter
		},
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			mountKernel = fk
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				if eo.ColabRequestHook != nil {
					eo.ColabRequestHook(
						map[string]any{"content": map[string]any{"request": map[string]any{"authType": "dfs_ephemeral"}}},
						func(payload map[string]any) error { return nil },
					)
				}
				if eo.StdinHook == nil || eo.StdinHook("prompt") != "" || !eo.AllowStdin {
					t.Errorf("expected AllowStdin=true with noop stdin hook")
				}
				if mountExecErr != nil {
					return nil, mountExecErr
				}
				return mountOutputs, nil
			}
			return fk
		},
	})
	_ = p.SaveSession(&ColabOneSessionState{
		Endpoint:       "ep-drive",
		Token:          "tok-drive",
		URL:            "https://ep-drive.invalid",
		TokenExpiresAt: &exp,
	}, true)

	// 1) UploadDriveConfig
	if err := p.UploadDriveConfig(context.Background(), "[colabone_drive]\ntype = drive\n", ""); err != nil {
		t.Fatalf("UploadDriveConfig failed: %v", err)
	}
	if len(contents.uploads) != 1 || contents.uploads[0].remotePath != "content/.colabone_rclone.conf" {
		t.Fatalf("unexpected drive upload: %#v", contents.uploads)
	}

	// 2) CheckColabDriveFSAuth
	ok, urlStr, err := p.CheckColabDriveFSAuth(context.Background(), "")
	if err != nil || ok || urlStr != "https://accounts.google.com/o/oauth2/v2/auth" {
		t.Fatalf("CheckColabDriveFSAuth unexpected: ok=%v url=%s err=%v", ok, urlStr, err)
	}

	// 3) MountColabDriveFS success
	res, err := p.MountColabDriveFS(context.Background(), "", DriveMountOptions{
		AccountEmail: "alice@example.com",
		Timeout:      120 * time.Second,
	})
	if err != nil {
		t.Fatalf("MountColabDriveFS failed: %v", err)
	}
	if res["ok"] != true || res["mode"] != "colab_drivefs" || res["mount_path"] != "/content/drive/MyDrive" {
		t.Fatalf("unexpected mount result: %#v", res)
	}
	if !mounter.hookCalled || !mounter.stopped || mountKernel.opts.SessionName != "colabone-drivefs" ||
		!reflect.DeepEqual(mountKernel.stopCalls, []bool{true}) {
		t.Fatalf("mount lifecycle mismatch: hook=%v stopped=%v session=%s stops=%v",
			mounter.hookCalled, mounter.stopped, mountKernel.opts.SessionName, mountKernel.stopCalls)
	}
	if mounter.capturedOpt.Timeout != 75*time.Second {
		t.Fatalf("expected authTimeout=120-45=75s, got %v", mounter.capturedOpt.Timeout)
	}

	// 4) MountColabDriveFS cancellation surfaces exact Chinese message
	cancelCh := make(chan struct{})
	close(cancelCh)
	mountExecErr = errors.New("ws aborted")
	_, err = p.MountColabDriveFS(context.Background(), "", DriveMountOptions{CancelCh: cancelCh})
	if err == nil || err.Error() != "已取消 Google 云盘挂载" {
		t.Fatalf("expected cancel message, got %v", err)
	}

	// 5) MountColabDriveFS kernel error output surfaces error
	mountExecErr = nil
	mountOutputs = []KernelOutput{{OutputType: "error", EName: "ValueError", EValue: "mountpoint busy"}}
	_, err = p.MountColabDriveFS(context.Background(), "", DriveMountOptions{})
	if err == nil || !strings.Contains(err.Error(), "Google Colab 原生云盘挂载失败：mountpoint busy") {
		t.Fatalf("expected mount error message, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 11. Built-in HTTP ContentsClient (401 Rotation + 503 Retry) & Ready Polling
// ---------------------------------------------------------------------------

func TestDefaultContentsClientAndReadyHTTPServer(t *testing.T) {
	var putTokens []string
	var deletedPaths []string
	var readyCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/kernels" {
			n := atomic.AddInt32(&readyCalls, 1)
			if r.Header.Get("X-Colab-Client-Agent") != "colab-cli" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if n < 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/contents/") {
			tok := r.URL.Query().Get("colab-runtime-proxy-token")
			if r.Method == http.MethodPut {
				putTokens = append(putTokens, tok)
				if tok == "old-tok" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if len(putTokens) == 2 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				var body map[string]any
				_ = json.Unmarshal(raw, &body)
				decoded, _ := base64.StdEncoding.DecodeString(strVal(body["content"]))
				if string(decoded) != "hello-world" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"ok":true}`))
				return
			}
			if r.Method == http.MethodDelete {
				deletedPaths = append(deletedPaths, r.URL.Path)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	vault := newMemoryVault()
	exp := time.Now().Add(time.Hour)
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{
				Endpoint:         "ep-http",
				RuntimeProxyInfo: &RuntimeProxyInfo{URL: srv.URL, Token: "fresh-tok", ExpiresAt: exp},
			}}, nil
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:             vault,
		ClientFactory:     func(string) (ColabClient, error) { return client, nil },
		HTTPClient:        srv.Client(),
		ReadyPollInterval: time.Millisecond,
		UploadRetryDelay:  time.Millisecond,
		SleepFn:           func(ctx context.Context, d time.Duration) error { return nil },
	})
	sess := &ColabOneSessionState{
		Endpoint:       "ep-http",
		Token:          "old-tok",
		URL:            srv.URL,
		TokenExpiresAt: &exp,
	}
	_ = p.SaveSession(sess, true)

	// Test Ready polling recovers from transient 503
	if err := p.Ready(context.Background(), sess, nil, nil); err != nil {
		t.Fatalf("Ready failed: %v", err)
	}
	if atomic.LoadInt32(&readyCalls) != 2 {
		t.Fatalf("expected 2 ready polls, got %d", readyCalls)
	}

	// Test uploadBytes rotates token on 401 and retries 503 using defaultContentsClient
	rotatedSess, err := p.uploadBytes(context.Background(), sess, "content/test.txt", []byte("hello-world"), 5*time.Second)
	if err != nil {
		t.Fatalf("uploadBytes failed: %v", err)
	}
	if rotatedSess.Token != "fresh-tok" {
		t.Fatalf("expected rotated session token fresh-tok, got %s", rotatedSess.Token)
	}
	if !reflect.DeepEqual(putTokens, []string{"old-tok", "fresh-tok", "fresh-tok"}) {
		t.Fatalf("expected [old-tok fresh-tok fresh-tok], got %v", putTokens)
	}

	p.deleteRemoteFile(context.Background(), rotatedSess, "content/test.txt")
	if !reflect.DeepEqual(deletedPaths, []string{"/api/contents/content/test.txt"}) {
		t.Fatalf("expected DELETE /api/contents/content/test.txt, got %v", deletedPaths)
	}
}

func TestBuildServiceZipFromRootDirFallback(t *testing.T) {
	tmp := t.TempDir()
	for _, dir := range []string{"server", "common", "catalog"} {
		if err := os.MkdirAll(filepath.Join(tmp, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(tmp, "server", "vm_boot.py"), []byte("print('boot')\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "server", "ignored.bin"), []byte{0, 1, 2}, 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "common", "files.py"), []byte("# files\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "catalog", "presets.json"), []byte("[]\n"), 0o644)

	p := NewColabOneProvider(ColabOneProviderConfig{RootDir: tmp})
	zipBytes, count, err := p.buildServiceZip()
	if err != nil {
		t.Fatalf("buildServiceZip failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 packaged files (excluding .bin), got %d", count)
	}
	bootCode, err := p.resolveVMBootScript(zipBytes)
	if err != nil || !strings.Contains(bootCode, "print('boot')") {
		t.Fatalf("resolveVMBootScript failed: code=%q err=%v", bootCode, err)
	}
}

// ---------------------------------------------------------------------------
// 12. Concurrency & Race-Safety Stress Test (-race)
// ---------------------------------------------------------------------------

func TestConcurrentExecutionAbortCloseAndObserverSafety(t *testing.T) {
	vault := newMemoryVault()
	exp := time.Now().Add(time.Hour)
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{
				{Endpoint: "ep-managed", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://m.invalid", Token: "tm", ExpiresAt: exp}},
				{Endpoint: "ep-ext", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://e.invalid", Token: "te", ExpiresAt: exp}},
			}, nil
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				if opts.SessionName == "colabone-observer" {
					return []KernelOutput{{
						OutputType: "stream",
						Name:       "stdout",
						Text:       `COLABONE_OBS={"source":"remote_readonly","resources":{"cpu":{"cores":2,"percent":10.0}}}` + "\n",
					}}, nil
				}
				return []KernelOutput{{Text: `COLABONE_RPC={"ok":true,"value":{"ok":true}}` + "\n"}}, nil
			}
			return fk
		},
	})
	_ = p.SaveSession(&ColabOneSessionState{
		Endpoint:       "ep-managed",
		Token:          "tm",
		URL:            "https://m.invalid",
		TokenExpiresAt: &exp,
	}, true)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				switch id % 4 {
				case 0:
					_, _ = p.RPC(context.Background(), "GET", "status", nil, "ep-managed", 30*time.Second)
				case 1:
					p.abortKernelExecution("ep-managed")
					_ = p.KernelRuntime()
				case 2:
					_, _ = p.ObserveExternalTelemetry(context.Background(), "ep-ext")
				case 3:
					p.CloseObserver("ep-ext", false)
					_, _ = p.Session(context.Background(), true, "ep-managed", false)
				}
			}
		}(i)
	}
	wg.Wait()
	p.CloseKernelChannel("", true)
	p.CloseObserver("", true)
}

func TestStartReuseExistingAndIncompleteDeploymentError(t *testing.T) {
	vault := newMemoryVault()
	exp := time.Now().Add(time.Hour)
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{
				Endpoint:         "ep-ready",
				RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep-ready.invalid", Token: "tok", ExpiresAt: exp},
			}}, nil
		},
	}
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
	})
	_ = p.SaveSession(&ColabOneSessionState{
		Endpoint:       "ep-ready",
		Token:          "tok",
		URL:            "https://ep-ready.invalid",
		TokenExpiresAt: &exp,
	}, true)

	// Session exists without remote -> clear Chinese error
	_, err := p.Start(context.Background(), "T4", nil, StartOptions{})
	if err == nil || !strings.Contains(err.Error(), "实例存在但部署未完成") {
		t.Fatalf("expected incomplete deployment error, got %v", err)
	}

	// Session + remote exists -> directly reuses without allocating
	_ = p.SaveRemote(map[string]any{"endpoint": "ep-ready", "gpu": "T4", "deadline": 0}, true)
	rem, err := p.Start(context.Background(), "T4", nil, StartOptions{})
	if err != nil || rem["endpoint"] != "ep-ready" {
		t.Fatalf("expected direct reuse of ep-ready, got rem=%#v err=%v", rem, err)
	}
}

func TestStartCancelMidBootAbortsKernelAndRollsBack(t *testing.T) {
	srv := makeReadyServer(t, nil)
	vault := newMemoryVault()
	_ = vault.Set("colab", map[string]any{"token": "ok"})

	var mu sync.Mutex
	assignments := []ListedAssignment{}
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			mu.Lock()
			defer mu.Unlock()
			out := make([]ListedAssignment, len(assignments))
			copy(out, assignments)
			return out, nil
		},
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			mu.Lock()
			assignments = append(assignments, ListedAssignment{Endpoint: "ep-cancel-boot"})
			mu.Unlock()
			return &Assignment{
				Endpoint: "ep-cancel-boot",
				RuntimeProxyInfo: &RuntimeProxyInfo{
					URL:                   srv.URL,
					Token:                 "tok-cancel",
					TokenExpiresInSeconds: 3600,
				},
			}, nil
		},
		unassignFn: func(ctx context.Context, endpoint string) error {
			mu.Lock()
			filtered := assignments[:0]
			for _, a := range assignments {
				if a.Endpoint != endpoint {
					filtered = append(filtered, a)
				}
			}
			assignments = filtered
			mu.Unlock()
			return nil
		},
	}

	cancelCh := make(chan struct{})
	var bootKernel *fakeKernel
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:               vault,
		ClientFactory:       func(string) (ColabClient, error) { return client, nil },
		Contents:            &fakeContents{},
		Bundler:             &fakeBundler{bootScript: "# boot"},
		ReleasePollInterval: time.Millisecond,
		SleepFn:             func(ctx context.Context, d time.Duration) error { return nil },
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			bootKernel = fk
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				go func() {
					time.Sleep(10 * time.Millisecond)
					close(cancelCh)
				}()
				<-fk.abortCh
				return nil, errors.New("aborted")
			}
			return fk
		},
	})

	_, err := p.Start(context.Background(), "G4", nil, StartOptions{CancelCh: cancelCh})
	if !errors.Is(err, ErrStartCancelled) {
		t.Fatalf("expected ErrStartCancelled, got %v", err)
	}
	if bootKernel == nil || !bootKernel.aborted.Load() {
		t.Fatalf("expected boot kernel Abort() called")
	}
	if !reflect.DeepEqual(client.unassigned, []string{"ep-cancel-boot"}) {
		t.Fatalf("expected rollback release of ep-cancel-boot, got %v", client.unassigned)
	}
	if len(p.ManagedEndpoints()) != 0 {
		t.Fatalf("expected no managed endpoints remaining, got %v", p.ManagedEndpoints())
	}
}

func TestRPC401RotationRetryErrorRedactionAndMissingOutput(t *testing.T) {
	vault := newMemoryVault()
	exp := time.Now().Add(time.Hour)
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{
				Endpoint:         "ep-rpc",
				RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://rpc.invalid", Token: "rotated-tok", ExpiresAt: exp},
			}}, nil
		},
	}

	var seenTokens []string
	mode := "401_then_ok"
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		SafeMessage: func(s string) string {
			return strings.ReplaceAll(s, "secret-tok", "[已隐藏]")
		},
		KernelFactory: func(opts KernelOptions) KernelClient {
			seenTokens = append(seenTokens, opts.ProxyToken)
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				switch mode {
				case "401_then_ok":
					if opts.ProxyToken == "old-tok" {
						return nil, &ColabRequestError{StatusCode: 401}
					}
					return []KernelOutput{{Text: "COLABONE_RPC={\"ok\":true,\"value\":{\"ok\":1}}\r\n"}}, nil
				case "rpc_error":
					return []KernelOutput{{Text: `COLABONE_RPC={"ok":false,"error":"bad secret-tok"}` + "\n"}}, nil
				default:
					return []KernelOutput{{Text: "no marker\n"}}, nil
				}
			}
			return fk
		},
	})
	_ = p.SaveSession(&ColabOneSessionState{
		Endpoint:       "ep-rpc",
		Token:          "old-tok",
		URL:            "https://rpc.invalid",
		TokenExpiresAt: &exp,
	}, true)

	// 1) Idempotent GET rotates token on 401 and succeeds on retry
	val, err := p.RPC(context.Background(), "GET", "status", nil, "", 30*time.Second)
	if err != nil || intVal(val.(map[string]any)["ok"]) != 1 {
		t.Fatalf("expected 401 rotation retry success: val=%#v err=%v", val, err)
	}
	if !reflect.DeepEqual(seenTokens, []string{"old-tok", "rotated-tok"}) {
		t.Fatalf("expected token rotation [old-tok rotated-tok], got %v", seenTokens)
	}

	// 2) Remote error passes through SafeMessageFn
	mode = "rpc_error"
	_, err = p.RPC(context.Background(), "GET", "status", nil, "", 30*time.Second)
	if err == nil || err.Error() != "bad [已隐藏]" {
		t.Fatalf("expected redacted RPC error, got %v", err)
	}

	// 3) Missing COLABONE_RPC= marker returns friendly Chinese error
	mode = "missing"
	_, err = p.RPC(context.Background(), "GET", "status", nil, "", 30*time.Second)
	if err == nil || err.Error() != "Colab 控制连接没有返回结果，请检查网络后重试" {
		t.Fatalf("expected missing RPC marker error, got %v", err)
	}
}

func TestLegacySessionJSONDecodingAndDescribeAssignmentEdgeCases(t *testing.T) {
	raw := []byte(`{
		"name": "colabone",
		"endpoint": "m-s-py",
		"token": "tok-py",
		"url": "https://py.invalid",
		"token_expires_at": "2026-11-23T08:00:00.123456+00:00",
		"kernel_id": null,
		"last_execution": ["pass", null, "ok"]
	}`)
	var s ColabOneSessionState
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("Unmarshal legacy session failed: %v", err)
	}
	if s.Endpoint != "m-s-py" || s.Variant != "DEFAULT" || s.Accelerator != "NONE" || s.MachineShape != "STANDARD" || s.TokenExpiresAt == nil {
		t.Fatalf("unexpected decoded legacy session: %#v", s)
	}

	cpuDesc := DescribeAssignment(ListedAssignment{
		Endpoint:     "cpu-1",
		Variant:      "DEFAULT",
		Accelerator:  "NONE",
		MachineShape: "0",
	}, map[string]bool{}, "", nil)
	if cpuDesc["variant"] != "CPU" || cpuDesc["accelerator"] != "CPU" || cpuDesc["shape"] != "STANDARD" {
		t.Fatalf("unexpected CPU assignment description: %#v", cpuDesc)
	}

	tpuDesc := DescribeAssignment(ListedAssignment{
		Endpoint:     "tpu-1",
		Variant:      "2",
		Accelerator:  "V5E1",
		MachineShape: "1",
	}, map[string]bool{"tpu-1": true}, "tpu-1", nil)
	if tpuDesc["variant"] != "TPU" || tpuDesc["accelerator"] != "V5E1" || tpuDesc["shape"] != "HIGH_RAM" || tpuDesc["managed"] != true || tpuDesc["active"] != true {
		t.Fatalf("unexpected TPU assignment description: %#v", tpuDesc)
	}
}

func TestReviewRegressionsRPCDetailTimeoutKeepAliveAndBareCR(t *testing.T) {
	// 1. RPC handles non-string JSON error detail and never double-retries kernel exec timeout.
	vault := newMemoryVault()
	var execCalls int
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{Endpoint: "ep-rpc", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://rt.invalid", Token: "tok"}}}, nil
		},
		keepAliveFn: func(ctx context.Context, endpoint string) error {
			return &ColabRequestError{StatusCode: 502, Method: "GET", URL: "https://colab/keep-alive/"}
		},
	}
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		Contents:      &fakeContents{},
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				execCalls++
				if strings.Contains(code, "COLABONE_RPC") {
					switch execCalls {
					case 1:
						return []KernelOutput{{Text: `COLABONE_RPC={"ok":false,"error":["bad","detail"]}`}}, nil
					default:
						return nil, errors.New("Colab 远端内核执行超时")
					}
				}
				return []KernelOutput{{Text: "ok"}}, nil
			}
			return fk
		},
	})
	_ = p.SaveSession(&ColabOneSessionState{
		Name:     "colabone",
		Endpoint: "ep-rpc",
		Token:    "tok",
		URL:      "https://rt.invalid",
	}, true)

	_, err := p.RPC(context.Background(), "GET", "status", nil, "ep-rpc", 0)
	if err == nil || !strings.Contains(err.Error(), `["bad","detail"]`) {
		t.Fatalf("expected stringified non-string RPC error detail, got %v", err)
	}

	execCalls = 1
	_, err = p.RPC(context.Background(), "GET", "status", nil, "ep-rpc", 0)
	if err == nil || !strings.Contains(err.Error(), "Colab 远端内核执行超时") {
		t.Fatalf("expected kernel execution timeout, got %v", err)
	}
	if execCalls != 2 {
		t.Fatalf("idempotent RPC wrongly double-retried kernel execution timeout: calls=%d", execCalls)
	}

	// 2. Heartbeat treats non-2xx /keep-alive/ as best-effort and runs kernel pass ping.
	if err := p.Heartbeat(context.Background(), "ep-rpc"); err != nil {
		t.Fatalf("Heartbeat failed on non-2xx /keep-alive/: %v", err)
	}

	// 3. splitAllLines splits bare \r lines (e.g. DriveFS progress + COLABONE_DRIVEFS_OK=1).
	lines := splitAllLines("Mounting...\rCOLABONE_DRIVEFS_OK=1\r\nDone")
	if len(lines) != 3 || lines[1] != "COLABONE_DRIVEFS_OK=1" {
		t.Fatalf("unexpected splitAllLines output: %#v", lines)
	}

	// 4. Ready() must return immediately on caller ctx deadline without busy-looping for 90s.
	var readyHits atomic.Int32
	readySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readyHits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer readySrv.Close()
	pReady := NewColabOneProvider(ColabOneProviderConfig{
		Vault:             newMemoryVault(),
		HTTPClient:        readySrv.Client(),
		ReadyTimeout:      30 * time.Second,
		ReadyPollInterval: 50 * time.Millisecond,
	})
	readySess := &ColabOneSessionState{
		Name:     "colabone",
		Endpoint: "ep-ready-dl",
		Token:    "tok",
		URL:      readySrv.URL,
	}
	_ = pReady.SaveSession(readySess, true)
	rCtx, rCancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer rCancel()
	started := time.Now()
	err = pReady.Ready(rCtx, readySess, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Ready() expected context.DeadlineExceeded, got %v", err)
	}
	if time.Since(started) > time.Second || readyHits.Load() > 5 {
		t.Fatalf("Ready() busy-looped after caller deadline: elapsed=%v hits=%d", time.Since(started), readyHits.Load())
	}

	// 5. RPC preserves 64-bit integer precision via json.Number.
	numClient := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{Endpoint: "ep-num", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://rt.invalid", Token: "tok"}}}, nil
		},
	}
	pNum := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         newMemoryVault(),
		ClientFactory: func(string) (ColabClient, error) { return numClient, nil },
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				return []KernelOutput{{Text: `COLABONE_RPC={"ok":true,"value":{"seed":1085739284759283741}}`}}, nil
			}
			return fk
		},
	})
	_ = pNum.SaveSession(&ColabOneSessionState{
		Name:     "colabone",
		Endpoint: "ep-num",
		Token:    "tok",
		URL:      "https://rt.invalid",
	}, true)
	val, err := pNum.RPC(context.Background(), "GET", "status", nil, "ep-num", 0)
	if err != nil {
		t.Fatalf("RPC 64-bit integer test failed: %v", err)
	}
	mVal, _ := val.(map[string]any)
	if num, ok := mVal["seed"].(json.Number); !ok || num.String() != "1085739284759283741" {
		t.Fatalf("RPC lost 64-bit integer precision: %#v (%T)", mVal["seed"], mVal["seed"])
	}
}

type fakeTransportErr struct {
	msg       string
	transport bool
}

func (e fakeTransportErr) Error() string          { return e.msg }
func (e fakeTransportErr) IsTransportError() bool { return e.transport }

func TestIsTransportErrorRetryReconciliationAndReleasePolling(t *testing.T) {
	// 1. Direct classification checks for interface{ IsTransportError() bool }.
	transientAuthErr := fakeTransportErr{msg: "Google 暂时无法续期，已保留本机登录，请稍后重试", transport: true}
	wrappedTransientErr := fmt.Errorf("do authorized: %w", transientAuthErr)
	nonTransportErr := fakeTransportErr{msg: "permanent auth failure", transport: false}

	if !isNetworkOrTransportError(transientAuthErr) || !isReleaseRetryableError(transientAuthErr) {
		t.Fatalf("expected IsTransportError()==true to be classified as transport/release-retryable")
	}
	if !isNetworkOrTransportError(wrappedTransientErr) || !isReleaseRetryableError(wrappedTransientErr) {
		t.Fatalf("expected wrapped IsTransportError()==true to be classified as transport/release-retryable")
	}
	if isNetworkOrTransportError(nonTransportErr) || isReleaseRetryableError(nonTransportErr) {
		t.Fatalf("expected IsTransportError()==false NOT to be classified as transport/release-retryable")
	}
	if isNetworkOrTransportError(errors.Join(context.Canceled, transientAuthErr)) {
		t.Fatalf("expected context.Canceled to take precedence over IsTransportError()")
	}

	// 2. assignRuntime retries and reconciles via GetAssignment on intermediate IsTransportError().
	{
		getCalls := 0
		postCalls := 0
		client := &fakeColabClient{
			getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
				getCalls++
				if getCalls == 1 {
					return nil, "xsrf-1", nil
				}
				return &Assignment{
					Endpoint:         "gpu-g4-transport-reconciled",
					RuntimeProxyInfo: makeProxyInfo("gpu-g4-transport-reconciled"),
				}, "", nil
			},
			postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
				postCalls++
				return nil, wrappedTransientErr
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:             newMemoryVault(),
			ClientFactory:     func(string) (ColabClient, error) { return client, nil },
			AssignRetryDelays: []time.Duration{0, 0},
		})
		sess, err := p.Allocate(context.Background(), "G4", AllocateOptions{})
		if err != nil {
			t.Fatalf("Allocate failed on retryable transport error: %v", err)
		}
		if sess.Endpoint != "gpu-g4-transport-reconciled" || postCalls != 1 || getCalls != 2 {
			t.Fatalf("expected intermediate GetAssignment reconciliation without double POST: ep=%s post=%d get=%d",
				sess.Endpoint, postCalls, getCalls)
		}
	}

	// 3. assignRuntime runs final GetAssignment reconciliation when the last attempt hits IsTransportError().
	{
		getCalls := 0
		postCalls := 0
		client := &fakeColabClient{
			getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
				getCalls++
				if getCalls <= 2 {
					return nil, fmt.Sprintf("xsrf-%d", getCalls), nil
				}
				return &Assignment{
					Endpoint:         "gpu-g4-transport-final",
					RuntimeProxyInfo: makeProxyInfo("gpu-g4-transport-final"),
				}, "", nil
			},
			postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
				postCalls++
				return nil, transientAuthErr
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:             newMemoryVault(),
			ClientFactory:     func(string) (ColabClient, error) { return client, nil },
			AssignRetryDelays: []time.Duration{0},
		})
		sess, err := p.Allocate(context.Background(), "G4", AllocateOptions{})
		if err != nil {
			t.Fatalf("Allocate final reconciliation failed: %v", err)
		}
		if sess.Endpoint != "gpu-g4-transport-final" || postCalls != 2 || getCalls != 3 {
			t.Fatalf("expected final GetAssignment reconciliation: ep=%s post=%d get=%d",
				sess.Endpoint, postCalls, getCalls)
		}
	}

	// 4. assignRuntime does NOT retry when IsTransportError() returns false.
	{
		getCalls := 0
		postCalls := 0
		client := &fakeColabClient{
			getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
				getCalls++
				return nil, "xsrf", nil
			},
			postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
				postCalls++
				return nil, nonTransportErr
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:             newMemoryVault(),
			ClientFactory:     func(string) (ColabClient, error) { return client, nil },
			AssignRetryDelays: []time.Duration{0, 0},
		})
		if _, err := p.Allocate(context.Background(), "G4", AllocateOptions{}); err == nil {
			t.Fatalf("expected error for non-transport error")
		}
		if postCalls != 1 || getCalls != 1 {
			t.Fatalf("non-transport error must not retry or reconcile: post=%d get=%d", postCalls, getCalls)
		}
	}

	// 5. Release continues stop-billing polling across transient IsTransportError() failures.
	{
		vault := newMemoryVault()
		storeLegacySession(vault, "ep-rel-transport")
		listCalls := 0
		client := &fakeColabClient{
			listFn: func(ctx context.Context) ([]ListedAssignment, error) {
				listCalls++
				switch listCalls {
				case 1:
					return []ListedAssignment{{Endpoint: "ep-rel-transport"}}, nil
				case 2:
					return nil, wrappedTransientErr
				default:
					return nil, nil
				}
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:               vault,
			ClientFactory:       func(string) (ColabClient, error) { return client, nil },
			ReleasePollInterval: time.Millisecond,
			SleepFn:             func(ctx context.Context, d time.Duration) error { return nil },
		})
		res, err := p.Release(context.Background(), "", nil)
		if err != nil || res["ok"] != true {
			t.Fatalf("Release aborted on retryable transport error: res=%#v err=%v", res, err)
		}
		if listCalls != 3 || vault.Has("runtime_session") {
			t.Fatalf("expected 3 ListAssignments calls and cleared session: calls=%d hasSession=%v",
				listCalls, vault.Has("runtime_session"))
		}
	}

	// 6. ReleaseAll continues stop-billing polling across transient IsTransportError() failures.
	{
		vault := newMemoryVault()
		storeLegacySession(vault, "ep-relall-transport")
		listCalls := 0
		client := &fakeColabClient{
			listFn: func(ctx context.Context) ([]ListedAssignment, error) {
				listCalls++
				switch listCalls {
				case 1:
					return []ListedAssignment{{Endpoint: "ep-relall-transport"}, {Endpoint: "ep-other"}}, nil
				case 2:
					return nil, transientAuthErr
				default:
					return []ListedAssignment{{Endpoint: "ep-other"}}, nil
				}
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:               vault,
			ClientFactory:       func(string) (ColabClient, error) { return client, nil },
			ReleasePollInterval: time.Millisecond,
			SleepFn:             func(ctx context.Context, d time.Duration) error { return nil },
		})
		res, err := p.ReleaseAll(context.Background(), []string{"ep-relall-transport"}, nil)
		if err != nil || res["ok"] != true || res["remaining"] != 1 {
			t.Fatalf("ReleaseAll aborted on retryable transport error: res=%#v err=%v", res, err)
		}
		if listCalls != 3 || vault.Has("runtime_session") {
			t.Fatalf("expected 3 ListAssignments calls and cleared owned session: calls=%d hasSession=%v",
				listCalls, vault.Has("runtime_session"))
		}
	}
}

func TestMountColabDriveFSAbortsKernelBeforeStoppingMounter(t *testing.T) {
	newMountProvider := func(
		t *testing.T,
		mounter *fakeDriveMounter,
		setupKernel func(fk *fakeKernel),
	) *ColabOneProvider {
		t.Helper()
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		client := &fakeColabClient{
			listFn: func(ctx context.Context) ([]ListedAssignment, error) {
				return []ListedAssignment{{
					Endpoint:         "ep-drive-order",
					RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep-drive-order.invalid", Token: "tok-drive", ExpiresAt: exp},
				}}, nil
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:         vault,
			ClientFactory: func(string) (ColabClient, error) { return client, nil },
			DriveMounterFactory: func(c ColabClient, ep string, opts DriveMountOptions) DriveMounter {
				return mounter
			},
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				setupKernel(fk)
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-drive-order",
			Token:          "tok-drive",
			URL:            "https://ep-drive-order.invalid",
			TokenExpiresAt: &exp,
		}, true)
		return p
	}

	firstIndex := func(events []string, target string) int {
		for i, ev := range events {
			if ev == target {
				return i
			}
		}
		return -1
	}

	// 1. Cancel via opts.CancelCh: cancelExec() -> rt.Abort() -> mountHelper.Stop().
	//    mounter.Stop blocks waiting on fk.abortCh (simulating a wedged ws.writeJSON);
	//    calling mountHelper.Stop() before rt.Abort() would deadlock until timeout.
	t.Run("CancelCh", func(t *testing.T) {
		var evMu sync.Mutex
		var events []string
		var capturedExecCtx context.Context
		var abortSawExecCanceled bool
		var stopSawAbortUnblocked bool
		record := func(ev string) {
			evMu.Lock()
			events = append(events, ev)
			evMu.Unlock()
		}

		execStarted := make(chan struct{})
		cancelCh := make(chan struct{})
		var kernel *fakeKernel
		mounter := &fakeDriveMounter{}
		mounter.stopFn = func() {
			select {
			case <-kernel.abortCh:
				evMu.Lock()
				stopSawAbortUnblocked = true
				evMu.Unlock()
			case <-time.After(2 * time.Second):
				t.Errorf("mounter.Stop blocked waiting for rt.Abort(); abort must run before mountHelper.Stop()")
			}
			record("mounter.Stop")
		}

		p := newMountProvider(t, mounter, func(fk *fakeKernel) {
			kernel = fk
			fk.abortFn = func() {
				evMu.Lock()
				if capturedExecCtx != nil && capturedExecCtx.Err() != nil {
					abortSawExecCanceled = true
				}
				evMu.Unlock()
				record("kernel.Abort")
			}
			fk.stopFn = func(shutdown bool) {
				record("kernel.Stop")
			}
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				evMu.Lock()
				capturedExecCtx = ctx
				evMu.Unlock()
				close(execStarted)
				<-fk.abortCh
				return nil, errors.New("ws aborted")
			}
		})

		done := make(chan error, 1)
		go func() {
			_, err := p.MountColabDriveFS(context.Background(), "", DriveMountOptions{CancelCh: cancelCh})
			done <- err
		}()

		<-execStarted
		close(cancelCh)

		select {
		case err := <-done:
			if err == nil || err.Error() != "已取消 Google 云盘挂载" {
				t.Fatalf("expected cancel message, got %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("MountColabDriveFS deadlocked on CancelCh teardown")
		}

		evMu.Lock()
		evCopy := append([]string(nil), events...)
		sawCanceled := abortSawExecCanceled
		sawUnblocked := stopSawAbortUnblocked
		evMu.Unlock()

		if !sawCanceled {
			t.Fatalf("expected cancelExec() before rt.Abort()")
		}
		if !sawUnblocked {
			t.Fatalf("expected rt.Abort() to unblock mounter.Stop()")
		}
		abortIdx := firstIndex(evCopy, "kernel.Abort")
		stopIdx := firstIndex(evCopy, "mounter.Stop")
		kStopIdx := firstIndex(evCopy, "kernel.Stop")
		if abortIdx < 0 || stopIdx < 0 || kStopIdx < 0 || !(abortIdx < stopIdx && stopIdx < kStopIdx) {
			t.Fatalf("expected kernel.Abort < mounter.Stop < kernel.Stop, got %v", evCopy)
		}
	})

	// 2. Cancel via parent ctx.Done(): cancelExec() -> rt.Abort() -> mountHelper.Stop().
	t.Run("ContextCancel", func(t *testing.T) {
		var evMu sync.Mutex
		var events []string
		var stopSawAbortUnblocked bool
		record := func(ev string) {
			evMu.Lock()
			events = append(events, ev)
			evMu.Unlock()
		}

		execStarted := make(chan struct{})
		parentCtx, cancelParent := context.WithCancel(context.Background())
		defer cancelParent()

		var kernel *fakeKernel
		mounter := &fakeDriveMounter{}
		mounter.stopFn = func() {
			select {
			case <-kernel.abortCh:
				evMu.Lock()
				stopSawAbortUnblocked = true
				evMu.Unlock()
			case <-time.After(2 * time.Second):
				t.Errorf("mounter.Stop blocked waiting for rt.Abort() on ctx.Done()")
			}
			record("mounter.Stop")
		}

		p := newMountProvider(t, mounter, func(fk *fakeKernel) {
			kernel = fk
			fk.abortFn = func() { record("kernel.Abort") }
			fk.stopFn = func(shutdown bool) { record("kernel.Stop") }
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				close(execStarted)
				<-fk.abortCh
				return nil, errors.New("ws aborted")
			}
		})

		done := make(chan error, 1)
		go func() {
			_, err := p.MountColabDriveFS(parentCtx, "", DriveMountOptions{})
			done <- err
		}()

		<-execStarted
		cancelParent()

		select {
		case err := <-done:
			if err == nil || err.Error() != "已取消 Google 云盘挂载" {
				t.Fatalf("expected cancel message, got %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("MountColabDriveFS deadlocked on ctx.Done() teardown")
		}

		evMu.Lock()
		evCopy := append([]string(nil), events...)
		sawUnblocked := stopSawAbortUnblocked
		evMu.Unlock()

		if !sawUnblocked {
			t.Fatalf("expected rt.Abort() to unblock mounter.Stop()")
		}
		abortIdx := firstIndex(evCopy, "kernel.Abort")
		stopIdx := firstIndex(evCopy, "mounter.Stop")
		kStopIdx := firstIndex(evCopy, "kernel.Stop")
		if abortIdx < 0 || stopIdx < 0 || kStopIdx < 0 || !(abortIdx < stopIdx && stopIdx < kStopIdx) {
			t.Fatalf("expected kernel.Abort < mounter.Stop < kernel.Stop, got %v", evCopy)
		}
	})

	// 3. Cleanup defer on non-cancel disconnect/timeout:
	//    rt.Abort() -> mountHelper.Stop() (while execCtx is still uncanceled so isStopOnly=true)
	//    -> cancelExec() -> rt.Stop(ctx, true).
	t.Run("CleanupDeferPreservesIsStopOnly", func(t *testing.T) {
		var evMu sync.Mutex
		var events []string
		var capturedExecCtx context.Context
		var mounterStopSawUncanceledExecCtx bool
		var kernelStopSawCanceledExecCtx bool
		record := func(ev string) {
			evMu.Lock()
			events = append(events, ev)
			evMu.Unlock()
		}

		var kernel *fakeKernel
		mounter := &fakeDriveMounter{}
		mounter.stopFn = func() {
			select {
			case <-kernel.abortCh:
			case <-time.After(2 * time.Second):
				t.Errorf("cleanup defer called mounter.Stop before rt.Abort()")
			}
			evMu.Lock()
			mounterStopSawUncanceledExecCtx = capturedExecCtx != nil && capturedExecCtx.Err() == nil
			evMu.Unlock()
			record("mounter.Stop")
		}

		p := newMountProvider(t, mounter, func(fk *fakeKernel) {
			kernel = fk
			fk.abortFn = func() { record("kernel.Abort") }
			fk.stopFn = func(shutdown bool) {
				evMu.Lock()
				kernelStopSawCanceledExecCtx = capturedExecCtx != nil && errors.Is(capturedExecCtx.Err(), context.Canceled)
				evMu.Unlock()
				record("kernel.Stop")
			}
			fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				evMu.Lock()
				capturedExecCtx = ctx
				evMu.Unlock()
				return nil, errors.New("ws read timeout")
			}
		})

		parentCtx, cancelParent := context.WithCancel(context.Background())
		defer cancelParent()

		_, err := p.MountColabDriveFS(parentCtx, "", DriveMountOptions{})
		if err == nil || !strings.Contains(err.Error(), "ws read timeout") {
			t.Fatalf("expected disconnect error, got %v", err)
		}

		evMu.Lock()
		evCopy := append([]string(nil), events...)
		stopUncanceled := mounterStopSawUncanceledExecCtx
		kStopCanceled := kernelStopSawCanceledExecCtx
		evMu.Unlock()

		want := []string{"kernel.Abort", "mounter.Stop", "kernel.Stop"}
		if !reflect.DeepEqual(evCopy, want) {
			t.Fatalf("cleanup defer order mismatch: got=%v want=%v", evCopy, want)
		}
		if !stopUncanceled {
			t.Fatalf("expected execCtx to remain uncanceled during mountHelper.Stop() so isStopOnly=true is preserved")
		}
		if !kStopCanceled {
			t.Fatalf("expected cancelExec() to run before rt.Stop()")
		}
	})
}

// ---------------------------------------------------------------------------
// 13. Security & Lifecycle Hardening Regression Tests
// ---------------------------------------------------------------------------

func TestClearClosesAndEvictsKernelChannelWithoutHoldingStateMu(t *testing.T) {
	t.Run("ClearSpecificEndpoint", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		var p *ColabOneProvider
		kernels := make(map[string]*fakeKernel)
		var kMu sync.Mutex

		var stopSawActive string
		var stopSawManaged []string

		p = NewColabOneProvider(ColabOneProviderConfig{
			Vault: vault,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				ep := strings.TrimPrefix(strings.TrimSuffix(opts.ServerURL, ".invalid"), "https://")
				if ep == "ep-a" {
					fk.stopFn = func(shutdownKernel bool) {
						// Acquiring p.stateMu inside Stop() would deadlock if Clear held stateMu
						// while calling CloseKernelChannel.
						stopSawActive = p.ActiveEndpoint()
						stopSawManaged = p.ManagedEndpoints()
					}
				}
				kMu.Lock()
				kernels[ep] = fk
				kMu.Unlock()
				return fk
			},
		})

		sessA := &ColabOneSessionState{
			Endpoint:       "ep-a",
			Token:          "tok-a",
			URL:            "https://ep-a.invalid",
			TokenExpiresAt: &exp,
		}
		sessB := &ColabOneSessionState{
			Endpoint:       "ep-b",
			Token:          "tok-b",
			URL:            "https://ep-b.invalid",
			TokenExpiresAt: &exp,
		}
		_ = p.SaveSession(sessA, true)
		_ = p.SaveSession(sessB, false)

		if _, err := p.Execute(context.Background(), sessA, "pass", ExecuteOptions{}); err != nil {
			t.Fatalf("Execute(ep-a) failed: %v", err)
		}
		if _, err := p.Execute(context.Background(), sessB, "pass", ExecuteOptions{}); err != nil {
			t.Fatalf("Execute(ep-b) failed: %v", err)
		}

		if err := p.Clear("ep-a"); err != nil {
			t.Fatalf("Clear(ep-a) failed: %v", err)
		}

		kMu.Lock()
		kA := kernels["ep-a"]
		kB := kernels["ep-b"]
		kMu.Unlock()

		if kA == nil || !reflect.DeepEqual(kA.stopCalls, []bool{false}) {
			t.Fatalf("expected ep-a kernel Stop(false) called, got %v", kA.stopCalls)
		}
		if stopSawActive != "ep-b" || !reflect.DeepEqual(stopSawManaged, []string{"ep-b"}) {
			t.Fatalf("expected Stop() to run after stateMu release with ep-b active, got active=%q managed=%v", stopSawActive, stopSawManaged)
		}
		if kB == nil || len(kB.stopCalls) != 0 {
			t.Fatalf("expected ep-b kernel left running, got stopCalls=%v", kB.stopCalls)
		}

		p.kernelMu.Lock()
		_, hasA := p.kernels["ep-a"]
		_, hasB := p.kernels["ep-b"]
		p.kernelMu.Unlock()
		if hasA {
			t.Fatalf("expected p.kernels[ep-a] evicted on Clear(ep-a)")
		}
		if !hasB {
			t.Fatalf("expected p.kernels[ep-b] retained after Clear(ep-a)")
		}
	})

	t.Run("ClearEmptyStateEvictsAllOrphanedKernels", func(t *testing.T) {
		vault := newMemoryVault()
		var created []*fakeKernel
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault: vault,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				created = append(created, fk)
				return fk
			},
		})
		// Create active kernel channels without corresponding vault sessions.
		if _, err := p.Execute(context.Background(), &ColabOneSessionState{
			Endpoint: "ep-orphan-1",
			Token:    "tok-1",
			URL:      "https://orphan1.invalid",
		}, "pass", ExecuteOptions{}); err != nil {
			t.Fatalf("Execute orphan-1 failed: %v", err)
		}
		if _, err := p.Execute(context.Background(), &ColabOneSessionState{
			Endpoint: "ep-orphan-2",
			Token:    "tok-2",
			URL:      "https://orphan2.invalid",
		}, "pass", ExecuteOptions{}); err != nil {
			t.Fatalf("Execute orphan-2 failed: %v", err)
		}

		if err := p.Clear(""); err != nil {
			t.Fatalf("Clear('') failed: %v", err)
		}
		if len(created) != 2 {
			t.Fatalf("expected 2 created kernels, got %d", len(created))
		}
		for i, fk := range created {
			if !reflect.DeepEqual(fk.stopCalls, []bool{false}) {
				t.Fatalf("expected orphan kernel %d stopped with shutdown=false, got %v", i, fk.stopCalls)
			}
		}
		p.kernelMu.Lock()
		remaining := len(p.kernels)
		p.kernelMu.Unlock()
		if remaining != 0 {
			t.Fatalf("expected all kernel slots evicted on Clear(''), got %d", remaining)
		}
	})

	t.Run("RPCPreRefreshClearDoesNotSelfDeadlock", func(t *testing.T) {
		vault := newMemoryVault()
		expired := time.Now().Add(-time.Minute)
		client := &fakeColabClient{
			listFn: func(ctx context.Context) ([]ListedAssignment, error) {
				// Endpoint disappeared on Colab while RPC holds slot.mu.
				return nil, nil
			},
		}
		var fk *fakeKernel
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:         vault,
			ClientFactory: func(string) (ColabClient, error) { return client, nil },
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk = newFakeKernel(opts)
				return fk
			},
		})
		sess := &ColabOneSessionState{
			Endpoint:       "ep-gone",
			Token:          "stale-tok",
			URL:            "https://gone.invalid",
			TokenExpiresAt: &expired,
		}
		_ = p.SaveSession(sess, true)
		if _, err := p.Execute(context.Background(), sess, "pass", ExecuteOptions{}); err != nil {
			t.Fatalf("initial Execute failed: %v", err)
		}

		done := make(chan error, 1)
		go func() {
			_, err := p.RPC(context.Background(), "GET", "status", nil, "ep-gone", 30*time.Second)
			done <- err
		}()

		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "请先启动远端 GPU") {
				t.Fatalf("expected missing GPU error after disappearance, got %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("RPC deadlocked when Session refresh cleared disappeared endpoint while holding slot.mu")
		}

		if fk == nil || !reflect.DeepEqual(fk.stopCalls, []bool{false}) {
			t.Fatalf("expected disappeared endpoint kernel stopped cleanly, got %v", fk.stopCalls)
		}
		p.kernelMu.Lock()
		_, stillPresent := p.kernels["ep-gone"]
		p.kernelMu.Unlock()
		if stillPresent {
			t.Fatalf("expected p.kernels[ep-gone] evicted after disappearance")
		}
	})
}

func TestProviderCloseStopsAllKernelsObserverAndIdleConnections(t *testing.T) {
	// Nil receiver must be a safe no-op.
	var nilProvider *ColabOneProvider
	nilProvider.Close()

	vault := newMemoryVault()
	exp := time.Now().Add(time.Hour)
	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			return []ListedAssignment{{
				Endpoint:         "ep-ext",
				RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ext.invalid", Token: "tok-ext", ExpiresAt: exp},
			}}, nil
		},
	}

	var managedKernels []*fakeKernel
	var observerKernel *fakeKernel
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         vault,
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk := newFakeKernel(opts)
			if opts.SessionName == "colabone-observer" {
				observerKernel = fk
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					return []KernelOutput{{
						OutputType: "stream",
						Name:       "stdout",
						Text:       `COLABONE_OBS={"source":"remote_readonly","resources":{"cpu":{"cores":2,"percent":5.0}}}` + "\n",
					}}, nil
				}
			} else {
				managedKernels = append(managedKernels, fk)
			}
			return fk
		},
	})

	sess1 := &ColabOneSessionState{Endpoint: "ep-1", Token: "tok-1", URL: "https://ep1.invalid", TokenExpiresAt: &exp}
	sess2 := &ColabOneSessionState{Endpoint: "ep-2", Token: "tok-2", URL: "https://ep2.invalid", TokenExpiresAt: &exp}
	_ = p.SaveSession(sess1, true)
	_ = p.SaveSession(sess2, false)

	if _, err := p.Execute(context.Background(), sess1, "pass", ExecuteOptions{}); err != nil {
		t.Fatalf("Execute(ep-1) failed: %v", err)
	}
	if _, err := p.Execute(context.Background(), sess2, "pass", ExecuteOptions{}); err != nil {
		t.Fatalf("Execute(ep-2) failed: %v", err)
	}
	if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-ext"); err != nil {
		t.Fatalf("ObserveExternalTelemetry(ep-ext) failed: %v", err)
	}

	p.Close()

	if len(managedKernels) != 2 {
		t.Fatalf("expected 2 managed kernels, got %d", len(managedKernels))
	}
	for i, fk := range managedKernels {
		if !reflect.DeepEqual(fk.stopCalls, []bool{false}) {
			t.Fatalf("managed kernel %d expected Stop(false), got %v", i, fk.stopCalls)
		}
	}
	if observerKernel == nil || !reflect.DeepEqual(observerKernel.stopCalls, []bool{true}) {
		t.Fatalf("observer kernel expected Stop(true), got %v", observerKernel)
	}

	p.kernelMu.Lock()
	kCount := len(p.kernels)
	p.kernelMu.Unlock()
	if kCount != 0 {
		t.Fatalf("expected all kernel slots evicted after Close(), got %d", kCount)
	}

	p.observerMu.Lock()
	obs := p.observer
	p.observerMu.Unlock()
	if obs != nil {
		t.Fatalf("expected observer cleared after Close(), got %+v", obs)
	}
}

func TestDefaultHTTPClientRefusesRedirectsWithoutLeakingProxyToken(t *testing.T) {
	var targetHits atomic.Int32
	var leakedMu sync.Mutex
	var leakedToken, leakedAuth, leakedReferer string

	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		leakedMu.Lock()
		leakedToken = r.Header.Get("X-Colab-Runtime-Proxy-Token")
		leakedAuth = r.Header.Get("Authorization")
		leakedReferer = r.Header.Get("Referer")
		leakedMu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer targetSrv.Close()

	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetSrv.URL+"/stolen", http.StatusFound)
	}))
	defer redirectSrv.Close()

	// HTTPClient intentionally omitted so ColabOneProvider builds its default httpClient.
	p := NewColabOneProvider(ColabOneProviderConfig{
		ReadyTimeout:      60 * time.Millisecond,
		ReadyPollInterval: 5 * time.Millisecond,
		SleepFn:           func(ctx context.Context, d time.Duration) error { return nil },
	})

	// 1) Direct request through default p.httpClient must return 302 without following redirect.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, redirectSrv.URL+"/api/kernels", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Colab-Runtime-Proxy-Token", "secret-proxy-header-token")
	req.Header.Set("Authorization", "Bearer secret-proxy-header-token")
	req.Header.Set("Referer", redirectSrv.URL+"/?colab-runtime-proxy-token=secret-referer-token")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		t.Fatalf("default httpClient.Do returned unexpected error on 302: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 Found without following redirect, got HTTP %d", resp.StatusCode)
	}

	// 2) Ready() polling against a 302 endpoint must never follow redirect to targetSrv.
	readyErr := p.Ready(context.Background(), &ColabOneSessionState{
		Endpoint: "ep-redir",
		URL:      redirectSrv.URL,
		Token:    "secret-ready-token",
	}, nil, nil)
	if readyErr == nil {
		t.Fatalf("Ready() must not treat 302 redirect target 200 as kernel readiness")
	}

	// 3) defaultContentsClient UploadBytes against a 302 endpoint must fail with 302 without following redirect or leaking token.
	const uploadTok = "secret-upload-token-888"
	upErr := p.contents.UploadBytes(context.Background(), redirectSrv.URL, uploadTok, "content/test.txt", []byte("hi"), 5*time.Second)
	if upErr == nil {
		t.Fatalf("UploadBytes expected 302 error, got nil")
	}
	var reqErr *ColabRequestError
	if !errors.As(upErr, &reqErr) || reqErr.StatusCode != http.StatusFound {
		t.Fatalf("expected ColabRequestError(302), got %v", upErr)
	}
	if strings.Contains(reqErr.URL, uploadTok) || strings.Contains(upErr.Error(), uploadTok) {
		t.Fatalf("UploadBytes 302 error leaked token: url=%q err=%v", reqErr.URL, upErr)
	}

	if hits := targetHits.Load(); hits != 0 {
		leakedMu.Lock()
		defer leakedMu.Unlock()
		t.Fatalf("redirect target was unexpectedly contacted %d times (token=%q auth=%q referer=%q)",
			hits, leakedToken, leakedAuth, leakedReferer)
	}
}

func TestStartRollbackUsesBoundedTimeoutContext(t *testing.T) {
	vault := newMemoryVault()
	_ = vault.Set("colab", map[string]any{"token": "ok"})

	var mu sync.Mutex
	assignments := []ListedAssignment{}
	var unassignHasDeadline bool
	var unassignRemaining time.Duration
	var unassignCtxErr error

	parentCtx, cancelParent := context.WithCancel(context.Background())

	client := &fakeColabClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			mu.Lock()
			defer mu.Unlock()
			out := make([]ListedAssignment, len(assignments))
			copy(out, assignments)
			return out, nil
		},
		postAssign: func(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
			mu.Lock()
			assignments = append(assignments, ListedAssignment{Endpoint: "ep-bounded-rb"})
			mu.Unlock()
			// Cancel Start's parent ctx immediately after allocation so rollback runs with a cancelled caller ctx.
			cancelParent()
			return &Assignment{
				Endpoint: "ep-bounded-rb",
				RuntimeProxyInfo: &RuntimeProxyInfo{
					URL:                   "https://ep-bounded-rb.invalid",
					Token:                 "tok-rb",
					TokenExpiresInSeconds: 3600,
				},
			}, nil
		},
		unassignFn: func(ctx context.Context, endpoint string) error {
			mu.Lock()
			if dl, ok := ctx.Deadline(); ok {
				unassignHasDeadline = true
				unassignRemaining = time.Until(dl)
			}
			unassignCtxErr = ctx.Err()
			assignments = nil
			mu.Unlock()
			return nil
		},
	}

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:               vault,
		ClientFactory:       func(string) (ColabClient, error) { return client, nil },
		ReleasePollInterval: time.Millisecond,
		SleepFn:             func(ctx context.Context, d time.Duration) error { return nil },
	})

	_, err := p.Start(parentCtx, "G4", nil, StartOptions{})
	if !errors.Is(err, ErrStartCancelled) {
		t.Fatalf("expected ErrStartCancelled, got %v", err)
	}

	mu.Lock()
	hasDL := unassignHasDeadline
	rem := unassignRemaining
	ctxErr := unassignCtxErr
	mu.Unlock()

	if !hasDL {
		t.Fatalf("expected Start rollback Release context to have a bounded deadline")
	}
	if ctxErr != nil {
		t.Fatalf("expected Start rollback Release context to remain active despite cancelled caller ctx, got %v", ctxErr)
	}
	if rem <= 60*time.Second || rem > 95*time.Second {
		t.Fatalf("expected Start rollback Release deadline ~90s, got remaining=%v", rem)
	}
}

func TestSensitiveStructStringAndGoStringRedaction(t *testing.T) {
	const (
		sessionTok   = "secret-runtime-proxy-tok-991"
		urlTok       = "secret-embedded-url-tok-772"
		proxyUser    = "proxySecretUser42"
		proxyPassRaw = "p@ssSecret99!"
		proxyPassEnc = "p%40ssSecret99%21"
	)

	exp := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	sess := ColabOneSessionState{
		Name:           "colabone",
		Token:          sessionTok,
		URL:            "https://rt.invalid/api?colab-runtime-proxy-token=" + urlTok,
		Endpoint:       "m-s-redact-1",
		TokenExpiresAt: &exp,
		Variant:        "GPU",
		Accelerator:    "L4",
		MachineShape:   "STANDARD",
		KernelID:       "kid-1",
		SessionID:      "sid-1",
	}

	for _, formatted := range []string{
		fmt.Sprintf("%v", sess),
		fmt.Sprintf("%+v", sess),
		fmt.Sprintf("%#v", sess),
		fmt.Sprintf("%v", &sess),
		fmt.Sprintf("%+v", &sess),
		fmt.Sprintf("%#v", &sess),
	} {
		if strings.Contains(formatted, sessionTok) || strings.Contains(formatted, urlTok) {
			t.Fatalf("ColabOneSessionState leaked secret token: %s", formatted)
		}
		if !strings.Contains(formatted, "m-s-redact-1") || !strings.Contains(formatted, "[REDACTED]") {
			t.Fatalf("ColabOneSessionState formatted string missing safe metadata/redaction marker: %s", formatted)
		}
	}

	var nilSess *ColabOneSessionState
	for _, formatted := range []string{
		fmt.Sprintf("%v", nilSess),
		fmt.Sprintf("%+v", nilSess),
		fmt.Sprintf("%#v", nilSess),
	} {
		if formatted == "" {
			t.Fatalf("nil *ColabOneSessionState formatted empty")
		}
	}

	spec, err := ParseAssignProxy("socks5h://" + proxyUser + ":" + proxyPassRaw + "@127.0.0.1:1080")
	if err != nil {
		t.Fatalf("ParseAssignProxy failed: %v", err)
	}
	if !strings.Contains(spec.NormalizedURL, proxyPassEnc) {
		t.Fatalf("expected NormalizedURL to contain encoded password for test precondition, got %q", spec.NormalizedURL)
	}

	for _, formatted := range []string{
		fmt.Sprintf("%v", *spec),
		fmt.Sprintf("%+v", *spec),
		fmt.Sprintf("%#v", *spec),
		fmt.Sprintf("%v", spec),
		fmt.Sprintf("%+v", spec),
		fmt.Sprintf("%#v", spec),
	} {
		for _, secret := range []string{proxyUser, proxyPassRaw, proxyPassEnc, spec.NormalizedURL} {
			if strings.Contains(formatted, secret) {
				t.Fatalf("ColabOneAssignProxySpec leaked %q in: %s", secret, formatted)
			}
		}
		if !strings.Contains(formatted, spec.MaskedURL) || !strings.Contains(formatted, "HasAuth:true") {
			t.Fatalf("ColabOneAssignProxySpec missing safe metadata: %s", formatted)
		}
	}

	var nilSpec *ColabOneAssignProxySpec
	for _, formatted := range []string{
		fmt.Sprintf("%v", nilSpec),
		fmt.Sprintf("%+v", nilSpec),
		fmt.Sprintf("%#v", nilSpec),
	} {
		if formatted == "" {
			t.Fatalf("nil *ColabOneAssignProxySpec formatted empty")
		}
	}
}

func TestRedactProviderURLErrorDeepSanitization(t *testing.T) {
	const tok = "sentinel-provider-token-4242"

	if redactProviderURLError(nil) != nil {
		t.Fatal("redactProviderURLError(nil) must return nil")
	}
	cleanErr := errors.New("plain network failure")
	if got := redactProviderURLError(cleanErr); got != cleanErr {
		t.Fatalf("clean error should be returned unchanged, got %v", got)
	}

	// 1) Direct malformed / fragment-duplicate / valid URLs.
	if got := redactProviderProxyTokenURL("https://rt.invalid:badport/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok); got != "[redacted]" {
		t.Fatalf("invalid port URL = %q, want [redacted]", got)
	}
	if got := redactProviderProxyTokenURL("https://rt.invalid\x7f/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok); got != "[redacted]" {
		t.Fatalf("control char URL = %q, want [redacted]", got)
	}
	if got := redactProviderProxyTokenURL("https://rt.invalid/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok + "#colab-runtime-proxy-token=" + tok); got != "[redacted]" {
		t.Fatalf("fragment duplicate URL = %q, want [redacted]", got)
	}
	if got := redactProviderProxyTokenURL("https://rt.example.invalid/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok); strings.Contains(got, tok) || !strings.Contains(got, "colab-runtime-proxy-token=REDACTED") {
		t.Fatalf("valid URL redaction failed: %q", got)
	}

	// 2) Wrapped *url.Error preserves outer message, inner sentinel, and redacts URL.
	wrapped := fmt.Errorf("upload failed: %w", &url.Error{
		Op:  "Put",
		URL: "https://rt.invalid:badport/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok,
		Err: context.DeadlineExceeded,
	})
	sanitizedWrapped := redactProviderURLError(wrapped)
	if strings.Contains(sanitizedWrapped.Error(), tok) {
		t.Fatalf("wrapped *url.Error leaked token: %v", sanitizedWrapped)
	}
	if !strings.Contains(sanitizedWrapped.Error(), "upload failed") {
		t.Fatalf("wrapped *url.Error lost outer context: %v", sanitizedWrapped)
	}
	if !errors.Is(sanitizedWrapped, context.DeadlineExceeded) {
		t.Fatalf("wrapped *url.Error lost context.DeadlineExceeded: %v", sanitizedWrapped)
	}
	var extractedUErr *url.Error
	if !errors.As(sanitizedWrapped, &extractedUErr) || extractedUErr.URL != "[redacted]" {
		t.Fatalf("extracted *url.Error = %+v, want URL=[redacted]", extractedUErr)
	}

	// 3) Nested *url.Error redacts outer URL, inner URL, and inner error message while preserving cause.
	nested := &url.Error{
		Op:  "Put",
		URL: "https://rt.example.invalid/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok,
		Err: &url.Error{
			Op:  "Get",
			URL: "https://rt.invalid\x7f/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok,
			Err: fmt.Errorf("redirect colab-runtime-proxy-token%%3D%s%%26x=1: %w", tok, context.Canceled),
		},
	}
	sanitizedNested := redactProviderURLError(nested)
	if strings.Contains(sanitizedNested.Error(), tok) {
		t.Fatalf("nested *url.Error leaked token: %v", sanitizedNested)
	}
	if !errors.Is(sanitizedNested, context.Canceled) {
		t.Fatalf("nested *url.Error lost context.Canceled: %v", sanitizedNested)
	}

	// 4) Non-*url.Error with multi-byte Unicode uppercase 'İ' and raw token.
	plainWithTok := fmt.Errorf("İ proxy rejected https://rt.invalid/api/contents/a?authuser=0&colab-runtime-proxy-token=%s&content=1: %w", tok, context.Canceled)
	sanitizedPlain := redactProviderURLError(plainWithTok)
	if strings.Contains(sanitizedPlain.Error(), tok) {
		t.Fatalf("non-*url.Error leaked token: %v", sanitizedPlain)
	}
	if !strings.Contains(sanitizedPlain.Error(), "colab-runtime-proxy-token=REDACTED&content=1") {
		t.Fatalf("unexpected non-*url.Error redaction: %v", sanitizedPlain)
	}
	if !errors.Is(sanitizedPlain, context.Canceled) {
		t.Fatalf("non-*url.Error lost wrapped sentinel: %v", sanitizedPlain)
	}

	// 5) Non-*url.Error with %3D / %3d percent-encoded tokens.
	for _, encoded := range []string{
		"redirect target https%3A%2F%2Frt.invalid%3Fcolab-runtime-proxy-token%3D" + tok + "%26content%3D1 failed",
		"redirect target https%3a%2f%2frt.invalid%3fCOLAB-RUNTIME-PROXY-TOKEN%3d" + tok + "%26content%3d1 failed",
	} {
		sanitizedEncoded := redactProviderURLError(errors.New(encoded))
		if strings.Contains(sanitizedEncoded.Error(), tok) {
			t.Fatalf("encoded non-*url.Error leaked token: %v", sanitizedEncoded)
		}
		if !strings.Contains(sanitizedEncoded.Error(), "REDACTED") || !strings.Contains(strings.ToLower(sanitizedEncoded.Error()), "%26content%3d1") {
			t.Fatalf("unexpected encoded redaction: %v", sanitizedEncoded)
		}
	}

	// 6) Multi-error (Unwrap() []error) via errors.Join sanitizes all branches and preserves causes.
	sentinel := errors.New("outer-cause")
	joined := errors.Join(
		sentinel,
		&url.Error{
			Op:  "Delete",
			URL: "https://rt.example.invalid/api/contents/a?colab-runtime-proxy-token=" + tok,
			Err: context.DeadlineExceeded,
		},
		errors.New("extra detail colab-runtime-proxy-token%3D"+tok+"%20end"),
	)
	sanitizedJoined := redactProviderURLError(joined)
	if strings.Contains(sanitizedJoined.Error(), tok) {
		t.Fatalf("joined error leaked token: %v", sanitizedJoined)
	}
	if !errors.Is(sanitizedJoined, sentinel) || !errors.Is(sanitizedJoined, context.DeadlineExceeded) {
		t.Fatalf("joined error lost wrapped causes: %v", sanitizedJoined)
	}
}

type uncomparableProviderErr struct {
	msg   string
	tags  []string // slices make the struct uncomparable under == / !=
	inner error
}

func (e uncomparableProviderErr) Error() string { return e.msg }
func (e uncomparableProviderErr) Unwrap() error { return e.inner }

type emptyMsgWrapperErr struct {
	inner error
}

func (e emptyMsgWrapperErr) Error() string { return "" }
func (e emptyMsgWrapperErr) Unwrap() error { return e.inner }

type customMultiProviderErr struct {
	msg  string
	errs []error
}

func (e customMultiProviderErr) Error() string   { return e.msg }
func (e customMultiProviderErr) Unwrap() []error { return e.errs }

type cyclicProviderErr struct {
	msg  string
	next error
}

func (e *cyclicProviderErr) Error() string { return e.msg }
func (e *cyclicProviderErr) Unwrap() error { return e.next }

func TestKernelSlotEvictionRacePreventsOrphanedRuntime(t *testing.T) {
	t.Run("CloseKernelChannelEvictionWhileExecuteWaitsOnSlotLock", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)

		stopEntered := make(chan struct{})
		releaseStop := make(chan struct{})
		var stopOnce sync.Once

		var kMu sync.Mutex
		var created []*fakeKernel

		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault: vault,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				kMu.Lock()
				idx := len(created)
				created = append(created, fk)
				kMu.Unlock()
				if idx == 0 {
					fk.stopFn = func(shutdownKernel bool) {
						stopOnce.Do(func() {
							close(stopEntered)
							<-releaseStop
						})
					}
				}
				return fk
			},
		})

		sess := &ColabOneSessionState{
			Endpoint:       "ep-race",
			Token:          "tok-race",
			URL:            "https://race.invalid",
			TokenExpiresAt: &exp,
		}
		_ = p.SaveSession(sess, true)

		// Seed initial slot1 + rt1.
		if _, err := p.Execute(context.Background(), sess, "pass", ExecuteOptions{}); err != nil {
			t.Fatalf("initial Execute failed: %v", err)
		}

		p.kernelMu.Lock()
		slot1 := p.kernels["ep-race"]
		p.kernelMu.Unlock()
		if slot1 == nil {
			t.Fatal("expected initial slot1 in p.kernels")
		}

		// CloseKernelChannel("ep-race", true) holds slot1.mu across rt1.Stop and eviction.
		closeDone := make(chan struct{})
		go func() {
			p.CloseKernelChannel("ep-race", true)
			close(closeDone)
		}()

		select {
		case <-stopEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for CloseKernelChannel to enter rt1.Stop")
		}

		// Start concurrent Execute while slot1 is still in p.kernels and slot1.mu is held.
		execErrCh := make(chan error, 1)
		go func() {
			_, err := p.Execute(context.Background(), sess, "pass", ExecuteOptions{})
			execErrCh <- err
		}()

		// Give Execute goroutine time to look up slot1 and block on slot1.mu.Lock().
		time.Sleep(20 * time.Millisecond)
		close(releaseStop)

		select {
		case <-closeDone:
		case <-time.After(3 * time.Second):
			t.Fatal("CloseKernelChannel did not complete")
		}
		select {
		case err := <-execErrCh:
			if err != nil {
				t.Fatalf("concurrent Execute failed: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent Execute did not complete")
		}

		slot1.mu.Lock()
		slot1Evicted := slot1.evicted
		slot1.mu.Unlock()
		if !slot1Evicted {
			t.Fatal("expected slot1.evicted to be true after eviction")
		}
		if slot1.getRuntime() != nil {
			t.Fatal("expected evicted slot1 to have nil runtime (must not host orphaned rt2)")
		}

		p.kernelMu.Lock()
		slot2 := p.kernels["ep-race"]
		p.kernelMu.Unlock()
		if slot2 == nil || slot2 == slot1 {
			t.Fatalf("expected fresh tracked slot2 != slot1 in p.kernels, got %p (slot1=%p)", slot2, slot1)
		}

		kMu.Lock()
		if len(created) != 2 {
			kMu.Unlock()
			t.Fatalf("expected 2 created kernels, got %d", len(created))
		}
		rt2 := created[1]
		kMu.Unlock()

		if slot2.getRuntime() != rt2 {
			t.Fatal("expected slot2 to track rt2")
		}

		// Subsequent CloseKernelChannel must cleanly stop rt2 and empty p.kernels.
		p.CloseKernelChannel("ep-race", true)
		if !reflect.DeepEqual(rt2.stopCalls, []bool{false}) {
			t.Fatalf("expected rt2 Stop(false) called on cleanup, got %v", rt2.stopCalls)
		}
		p.kernelMu.Lock()
		remaining := len(p.kernels)
		p.kernelMu.Unlock()
		if remaining != 0 {
			t.Fatalf("expected p.kernels empty after final CloseKernelChannel, got %d", remaining)
		}
	})

	t.Run("ExecuteLockedRejectsEvictedHeldSlot", func(t *testing.T) {
		var factoryCalls atomic.Int32
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault: newMemoryVault(),
			KernelFactory: func(opts KernelOptions) KernelClient {
				factoryCalls.Add(1)
				return newFakeKernel(opts)
			},
		})
		sess := &ColabOneSessionState{
			Endpoint: "ep-evicted",
			Token:    "tok",
			URL:      "https://evicted.invalid",
		}
		slot, err := p.lockKernelSlot(sess.Endpoint)
		if err != nil {
			t.Fatalf("lockKernelSlot failed: %v", err)
		}
		defer slot.mu.Unlock()

		p.closeKernelChannelWithHeldSlot(sess.Endpoint, true, slot)
		if !slot.evicted {
			t.Fatal("expected held slot to be marked evicted")
		}
		if _, err := p.executeLocked(context.Background(), slot, sess, "pass", ExecuteOptions{}); err == nil {
			t.Fatal("expected executeLocked to reject evicted slot")
		}
		if factoryCalls.Load() != 0 {
			t.Fatalf("expected zero KernelFactory calls on evicted slot, got %d", factoryCalls.Load())
		}
		if slot.getRuntime() != nil {
			t.Fatal("expected evicted slot runtime to remain nil")
		}
	})

	t.Run("RPCIdempotentRetryStopsIfHeldSlotEvictedMidOperation", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		sess := &ColabOneSessionState{
			Endpoint:       "ep-rpc-evict",
			Token:          "tok-old",
			URL:            "https://rpc-evict.invalid",
			TokenExpiresAt: &exp,
		}

		client := &fakeColabClient{
			listFn: func(ctx context.Context) ([]ListedAssignment, error) {
				// Endpoint disappeared on Colab during 401 refresh.
				return nil, nil
			},
		}

		var p *ColabOneProvider
		var factoryCalls atomic.Int32
		p = NewColabOneProvider(ColabOneProviderConfig{
			Vault:         vault,
			ClientFactory: func(string) (ColabClient, error) { return client, nil },
			KernelFactory: func(opts KernelOptions) KernelClient {
				n := factoryCalls.Add(1)
				fk := newFakeKernel(opts)
				if n == 1 {
					var execCount atomic.Int32
					fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
						if execCount.Add(1) == 1 {
							return []KernelOutput{{Text: "ok\n"}}, nil
						}
						// Trigger RPC 401 refresh path on attempt 0.
						return nil, &ColabRequestError{StatusCode: 401}
					}
					var recreatedOnce sync.Once
					fk.stopFn = func(shutdownKernel bool) {
						// While p.clear(..., slot) stops the kernel (after stateMu is released),
						// simulate a concurrent SaveSession for the same endpoint so RPC's attempt 1
						// would otherwise see a session if it didn't check slot.evicted.
						recreatedOnce.Do(func() {
							_ = p.SaveSession(&ColabOneSessionState{
								Endpoint:       "ep-rpc-evict",
								Token:          "tok-new",
								URL:            "https://rpc-evict.invalid",
								TokenExpiresAt: &exp,
							}, true)
						})
					}
				}
				return fk
			},
		})
		_ = p.SaveSession(sess, true)

		// Warm up slot with initial Execute.
		if _, err := p.Execute(context.Background(), sess, "pass", ExecuteOptions{}); err != nil {
			t.Fatalf("warmup Execute failed: %v", err)
		}

		_, err := p.RPC(context.Background(), "GET", "status", nil, "ep-rpc-evict", 30*time.Second)
		if err == nil || !strings.Contains(err.Error(), "请先启动远端 GPU") {
			t.Fatalf("expected missing GPU error when held slot was evicted mid-RPC, got %v", err)
		}
		if factoryCalls.Load() != 1 {
			t.Fatalf("RPC must not spawn a kernel on an evicted slot, got %d factory calls", factoryCalls.Load())
		}
	})

	t.Run("ConcurrentSlotEvictionStressNoOrphanedKernels", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		client := &fakeColabClient{
			listFn: func(ctx context.Context) ([]ListedAssignment, error) {
				return []ListedAssignment{{
					Endpoint:         "ep-stress",
					RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://stress.invalid", Token: "tok-s", ExpiresAt: exp},
				}}, nil
			},
		}

		var kMu sync.Mutex
		var allKernels []*fakeKernel
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:         vault,
			ClientFactory: func(string) (ColabClient, error) { return client, nil },
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					return []KernelOutput{{Text: `COLABONE_RPC={"ok":true,"value":{"ok":true}}` + "\n"}}, nil
				}
				kMu.Lock()
				allKernels = append(allKernels, fk)
				kMu.Unlock()
				return fk
			},
		})
		sess := &ColabOneSessionState{
			Endpoint:       "ep-stress",
			Token:          "tok-s",
			URL:            "https://stress.invalid",
			TokenExpiresAt: &exp,
		}
		_ = p.SaveSession(sess, true)

		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < 15; i++ {
					switch id % 4 {
					case 0:
						_, _ = p.Execute(context.Background(), sess, "pass", ExecuteOptions{})
					case 1:
						_, _ = p.RPC(context.Background(), "GET", "status", nil, "ep-stress", 30*time.Second)
					case 2:
						_ = p.Heartbeat(context.Background(), "ep-stress")
					case 3:
						p.CloseKernelChannel("ep-stress", true)
					}
				}
			}(g)
		}
		wg.Wait()
		p.Close()

		p.kernelMu.Lock()
		remaining := len(p.kernels)
		p.kernelMu.Unlock()
		if remaining != 0 {
			t.Fatalf("expected 0 remaining slots after Close(), got %d", remaining)
		}
		kMu.Lock()
		defer kMu.Unlock()
		for i, fk := range allKernels {
			fk.mu.Lock()
			stops := append([]bool(nil), fk.stopCalls...)
			fk.mu.Unlock()
			if len(stops) == 0 {
				t.Fatalf("kernel %d was never stopped (orphaned on evicted slot)", i)
			}
		}
	})
}

func TestProviderCloseAbortsActiveExecutionPromptlyAndRejectsPostClose(t *testing.T) {
	t.Run("CloseAbortsActiveExecutionAndClosesObserverPromptly", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		client := &fakeColabClient{
			listFn: func(ctx context.Context) ([]ListedAssignment, error) {
				return []ListedAssignment{{
					Endpoint:         "ep-ext",
					RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ext.invalid", Token: "tok-ext", ExpiresAt: exp},
				}}, nil
			},
		}

		execEntered := make(chan struct{})
		var managedKernel *fakeKernel
		var observerKernel *fakeKernel
		var factoryCalls atomic.Int32

		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:         vault,
			ClientFactory: func(string) (ColabClient, error) { return client, nil },
			KernelFactory: func(opts KernelOptions) KernelClient {
				factoryCalls.Add(1)
				fk := newFakeKernel(opts)
				if opts.SessionName == "colabone-observer" {
					observerKernel = fk
					fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
						return []KernelOutput{{
							OutputType: "stream",
							Name:       "stdout",
							Text:       `COLABONE_OBS={"source":"remote_readonly","resources":{"cpu":{"cores":2,"percent":8.0}}}` + "\n",
						}}, nil
					}
					return fk
				}
				managedKernel = fk
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					close(execEntered)
					select {
					case <-fk.abortCh:
						return nil, errors.New("Colab WebSocket connection aborted")
					case <-time.After(10 * time.Second):
						return nil, errors.New("timed out waiting for Close() to abort execution")
					}
				}
				return fk
			},
		})

		sess := &ColabOneSessionState{
			Endpoint:       "ep-long",
			Token:          "tok-long",
			URL:            "https://long.invalid",
			TokenExpiresAt: &exp,
		}
		_ = p.SaveSession(sess, true)

		// Warm up observer so Close() must also shut it down promptly.
		if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-ext"); err != nil {
			t.Fatalf("ObserveExternalTelemetry failed: %v", err)
		}

		execErrCh := make(chan error, 1)
		go func() {
			_, err := p.Execute(context.Background(), sess, "long_running()", ExecuteOptions{Timeout: 300 * time.Second})
			execErrCh <- err
		}()

		select {
		case <-execEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for ExecuteCode to start")
		}

		started := time.Now()
		p.Close()
		elapsed := time.Since(started)
		if elapsed > 2*time.Second {
			t.Fatalf("Close() stalled for %v on active execution", elapsed)
		}

		var execErr error
		select {
		case execErr = <-execErrCh:
		case <-time.After(3 * time.Second):
			t.Fatal("active Execute did not unwind after Close()")
		}
		if !errors.Is(execErr, ErrProviderClosed) {
			t.Fatalf("expected ErrProviderClosed from aborted Execute, got %v", execErr)
		}
		if managedKernel == nil || !managedKernel.aborted.Load() {
			t.Fatal("expected Close() to call Abort() on active managed kernel")
		}
		if !reflect.DeepEqual(managedKernel.stopCalls, []bool{false}) {
			t.Fatalf("expected managed kernel Stop(false), got %v", managedKernel.stopCalls)
		}
		if observerKernel == nil || !reflect.DeepEqual(observerKernel.stopCalls, []bool{true}) {
			t.Fatalf("expected observer kernel Stop(true), got %v", observerKernel)
		}

		// Verify post-Close() resurrection is rejected across all entrypoints without touching KernelFactory.
		callsAtClose := factoryCalls.Load()
		if _, err := p.Execute(context.Background(), sess, "pass", ExecuteOptions{}); !errors.Is(err, ErrProviderClosed) {
			t.Fatalf("post-Close Execute expected ErrProviderClosed, got %v", err)
		}
		if _, err := p.RPC(context.Background(), "GET", "status", nil, sess.Endpoint, 30*time.Second); !errors.Is(err, ErrProviderClosed) {
			t.Fatalf("post-Close RPC expected ErrProviderClosed, got %v", err)
		}
		if err := p.Heartbeat(context.Background(), sess.Endpoint); !errors.Is(err, ErrProviderClosed) {
			t.Fatalf("post-Close Heartbeat expected ErrProviderClosed, got %v", err)
		}
		if err := p.UpgradeService(context.Background(), sess.Endpoint, nil); !errors.Is(err, ErrProviderClosed) {
			t.Fatalf("post-Close UpgradeService expected ErrProviderClosed, got %v", err)
		}
		if _, err := p.Start(context.Background(), "T4", nil, StartOptions{}); !errors.Is(err, ErrProviderClosed) {
			t.Fatalf("post-Close Start expected ErrProviderClosed, got %v", err)
		}
		if _, err := p.MountColabDriveFS(context.Background(), sess.Endpoint, DriveMountOptions{}); !errors.Is(err, ErrProviderClosed) {
			t.Fatalf("post-Close MountColabDriveFS expected ErrProviderClosed, got %v", err)
		}
		if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-ext"); !errors.Is(err, ErrExternalObserverClosed) {
			t.Fatalf("post-Close ObserveExternalTelemetry expected ErrExternalObserverClosed, got %v", err)
		}
		if factoryCalls.Load() != callsAtClose {
			t.Fatalf("post-Close calls must not create new kernels; before=%d after=%d", callsAtClose, factoryCalls.Load())
		}

		p.kernelMu.Lock()
		kLen := len(p.kernels)
		p.kernelMu.Unlock()
		if kLen != 0 {
			t.Fatalf("expected 0 kernel slots after post-Close attempts, got %d", kLen)
		}
		p.observerMu.Lock()
		obs := p.observer
		p.observerMu.Unlock()
		if obs != nil {
			t.Fatalf("expected nil observer after post-Close attempts, got %+v", obs)
		}

		// Calling Close() again must be a safe idempotent no-op.
		p.Close()
	})

	t.Run("CloseAbortsInFlightKernelStartPromptly", func(t *testing.T) {
		startEntered := make(chan struct{})
		var fk *fakeKernel
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault: newMemoryVault(),
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk = newFakeKernel(opts)
				fk.startFn = func(ctx context.Context) error {
					close(startEntered)
					select {
					case <-fk.abortCh:
						return errors.New("start aborted")
					case <-time.After(10 * time.Second):
						return errors.New("timed out waiting for Close() to abort Start")
					}
				}
				return fk
			},
		})
		sess := &ColabOneSessionState{
			Endpoint: "ep-start",
			Token:    "tok-start",
			URL:      "https://start.invalid",
		}
		errCh := make(chan error, 1)
		go func() {
			_, err := p.Execute(context.Background(), sess, "pass", ExecuteOptions{})
			errCh <- err
		}()
		select {
		case <-startEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for rt.Start")
		}
		started := time.Now()
		p.Close()
		if time.Since(started) > 2*time.Second {
			t.Fatalf("Close() stalled on in-flight Start")
		}
		select {
		case err := <-errCh:
			if !errors.Is(err, ErrProviderClosed) {
				t.Fatalf("expected ErrProviderClosed, got %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Execute did not unwind after Close()")
		}
		if fk == nil || !fk.aborted.Load() {
			t.Fatal("expected Close() to abort in-flight Start kernel")
		}
	})
}

func TestCustomHTTPClientWithNilCheckRedirectBlocksRedirectsWithoutMutation(t *testing.T) {
	var targetHits atomic.Int32
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer targetSrv.Close()

	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetSrv.URL+"/stolen", http.StatusFound)
	}))
	defer redirectSrv.Close()

	t.Run("NilCheckRedirectClonedAndBlockedWithoutMutatingCaller", func(t *testing.T) {
		targetHits.Store(0)
		customClient := &http.Client{
			Transport: redirectSrv.Client().Transport,
			Timeout:   12 * time.Second,
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			HTTPClient:        customClient,
			ReadyTimeout:      60 * time.Millisecond,
			ReadyPollInterval: 5 * time.Millisecond,
			SleepFn:           func(ctx context.Context, d time.Duration) error { return nil },
		})

		if customClient.CheckRedirect != nil {
			t.Fatal("NewColabOneProvider mutated caller's HTTPClient.CheckRedirect")
		}
		if p.httpClient == customClient {
			t.Fatal("expected NewColabOneProvider to shallow-clone caller's HTTPClient when CheckRedirect is nil")
		}
		if p.httpClient.CheckRedirect == nil {
			t.Fatal("expected cloned HTTPClient to have CheckRedirect installed")
		}
		if p.httpClient.Timeout != 12*time.Second {
			t.Fatalf("expected cloned HTTPClient to preserve Timeout=12s, got %v", p.httpClient.Timeout)
		}

		// Direct request, Ready(), and defaultContentsClient.UploadBytes must all refuse 302.
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, redirectSrv.URL+"/api/kernels", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		resp, err := p.httpClient.Do(req)
		if err != nil {
			t.Fatalf("Do returned error: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("expected HTTP 302, got %d", resp.StatusCode)
		}

		if err := p.Ready(context.Background(), &ColabOneSessionState{
			Endpoint: "ep-custom-redir",
			URL:      redirectSrv.URL,
			Token:    "secret-tok",
		}, nil, nil); err == nil {
			t.Fatal("Ready() must not follow redirect to 200")
		}

		upErr := p.contents.UploadBytes(context.Background(), redirectSrv.URL, "secret-tok", "content/a.txt", []byte("x"), 5*time.Second)
		var cre *ColabRequestError
		if !errors.As(upErr, &cre) || cre.StatusCode != http.StatusFound {
			t.Fatalf("expected ColabRequestError(302), got %v", upErr)
		}
		if targetHits.Load() != 0 {
			t.Fatalf("redirect target contacted %d times", targetHits.Load())
		}
	})

	t.Run("ExplicitCallerCheckRedirectPreserved", func(t *testing.T) {
		var customCalled atomic.Bool
		customClient := &http.Client{
			Transport: redirectSrv.Client().Transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				customCalled.Store(true)
				return http.ErrUseLastResponse
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{HTTPClient: customClient})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, redirectSrv.URL+"/check", nil)
		resp, err := p.httpClient.Do(req)
		if err != nil {
			t.Fatalf("Do failed: %v", err)
		}
		_ = resp.Body.Close()
		if !customCalled.Load() {
			t.Fatal("expected caller-supplied CheckRedirect to be preserved")
		}
	})
}

func TestRedactProviderURLErrorSanitizerHardening(t *testing.T) {
	const tok = "secret-hardening-token-9988"

	// 1) Uncomparable error types must NEVER panic on interface comparison.
	t.Run("UncomparableErrorsDoNotPanic", func(t *testing.T) {
		cleanUncomp := uncomparableProviderErr{msg: "plain uncomparable", tags: []string{"a", "b"}}
		if got := redactProviderURLError(cleanUncomp); got.Error() != "plain uncomparable" {
			t.Fatalf("clean uncomparable error altered: %v", got)
		}

		leakingUncomp := uncomparableProviderErr{
			msg:   "failed url ?colab-runtime-proxy-token=" + tok + "&k=1",
			tags:  []string{"x"},
			inner: context.Canceled,
		}
		gotLeaking := redactProviderURLError(leakingUncomp)
		if strings.Contains(gotLeaking.Error(), tok) {
			t.Fatalf("uncomparable error leaked token: %v", gotLeaking)
		}
		if !errors.Is(gotLeaking, context.Canceled) {
			t.Fatalf("uncomparable error lost wrapped sentinel: %v", gotLeaking)
		}

		// Uncomparable inside *url.Error.Err (both clean and leaking).
		uClean := &url.Error{Op: "Put", URL: "https://rt.invalid/api", Err: cleanUncomp}
		if got := redactProviderURLError(uClean); got.Error() != uClean.Error() {
			t.Fatalf("clean *url.Error with uncomparable inner altered: %v", got)
		}
		uLeak := &url.Error{
			Op:  "Put",
			URL: "https://rt.invalid/api?colab-runtime-proxy-token=" + tok,
			Err: leakingUncomp,
		}
		gotULeak := redactProviderURLError(uLeak)
		if strings.Contains(gotULeak.Error(), tok) {
			t.Fatalf("*url.Error with uncomparable inner leaked token: %v", gotULeak)
		}
		if !errors.Is(gotULeak, context.Canceled) {
			t.Fatalf("*url.Error with uncomparable inner lost sentinel: %v", gotULeak)
		}

		// Uncomparable inside errors.Join (Unwrap() []error).
		joinedClean := errors.Join(cleanUncomp, context.DeadlineExceeded)
		if got := redactProviderURLError(joinedClean); !errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("clean joined uncomparable lost cause: %v", got)
		}
		joinedLeak := errors.Join(cleanUncomp, leakingUncomp, context.DeadlineExceeded)
		gotJoinedLeak := redactProviderURLError(joinedLeak)
		if strings.Contains(gotJoinedLeak.Error(), tok) {
			t.Fatalf("joined uncomparable leaked token: %v", gotJoinedLeak)
		}
		if !errors.Is(gotJoinedLeak, context.Canceled) || !errors.Is(gotJoinedLeak, context.DeadlineExceeded) {
			t.Fatalf("joined uncomparable lost causes: %v", gotJoinedLeak)
		}
	})

	// 2) Typed-nil *url.Error must never trigger a nil-pointer dereference panic.
	t.Run("TypedNilURLErrorDoesNotPanic", func(t *testing.T) {
		var typedNil *url.Error
		if got := redactProviderURLError(typedNil); got != nil {
			t.Fatalf("expected nil for typed-nil *url.Error, got %v", got)
		}

		wrappedNil := fmt.Errorf("outer wrap: %w", typedNil)
		if got := redactProviderURLError(wrappedNil); got == nil || !strings.Contains(got.Error(), "outer wrap") {
			t.Fatalf("wrapped typed-nil *url.Error failed: %v", got)
		}

		outerWithNilInner := &url.Error{
			Op:  "Delete",
			URL: "https://rt.example.invalid/api/contents/x?colab-runtime-proxy-token=" + tok,
			Err: typedNil,
		}
		gotOuter := redactProviderURLError(outerWithNilInner)
		if strings.Contains(gotOuter.Error(), tok) {
			t.Fatalf("outer *url.Error with typed-nil inner leaked token: %v", gotOuter)
		}

		multiWithTypedNil := customMultiProviderErr{
			msg: "multi: outer",
			errs: []error{
				typedNil,
				&url.Error{
					Op:  "Put",
					URL: "https://rt.example.invalid/api?colab-runtime-proxy-token=" + tok,
					Err: context.DeadlineExceeded,
				},
			},
		}
		gotMulti := redactProviderURLError(multiWithTypedNil)
		if strings.Contains(gotMulti.Error(), tok) {
			t.Fatalf("multi with typed-nil leaked token: %v", gotMulti)
		}
		if !errors.Is(gotMulti, context.DeadlineExceeded) {
			t.Fatalf("multi with typed-nil lost cause: %v", gotMulti)
		}
	})

	// 3) Multi-error (fmt.Errorf multi-%w and custom Unwrap() []error) preserves outer text and all causes.
	t.Run("MultiErrorPreservesOuterMessageAndCauses", func(t *testing.T) {
		sentinel := errors.New("outer-sentinel")
		leakingUErr := &url.Error{
			Op:  "Put",
			URL: "https://rt.invalid:badport/api?colab-runtime-proxy-token=" + tok,
			Err: context.DeadlineExceeded,
		}
		multiWrap := fmt.Errorf("upload envelope (%w): %w", sentinel, leakingUErr)
		sanitized := redactProviderURLError(multiWrap)
		if strings.Contains(sanitized.Error(), tok) {
			t.Fatalf("multi-%%w leaked token: %v", sanitized)
		}
		if !strings.Contains(sanitized.Error(), "upload envelope") {
			t.Fatalf("multi-%%w lost outer context message: %v", sanitized)
		}
		if !errors.Is(sanitized, sentinel) || !errors.Is(sanitized, context.DeadlineExceeded) {
			t.Fatalf("multi-%%w lost causes: %v", sanitized)
		}
		var extracted *url.Error
		if !errors.As(sanitized, &extracted) || extracted.URL != "[redacted]" {
			t.Fatalf("multi-%%w lost sanitized *url.Error: %+v", extracted)
		}

		// Clean children but leaking outer multi-error message still redacts and preserves causes.
		outerLeakingMulti := customMultiProviderErr{
			msg:  "batch failed at ?colab-runtime-proxy-token=" + tok + "&x=1",
			errs: []error{sentinel, context.Canceled},
		}
		sanitizedOuter := redactProviderURLError(outerLeakingMulti)
		if strings.Contains(sanitizedOuter.Error(), tok) {
			t.Fatalf("outer-leaking multi error leaked token: %v", sanitizedOuter)
		}
		if !errors.Is(sanitizedOuter, sentinel) || !errors.Is(sanitizedOuter, context.Canceled) {
			t.Fatalf("outer-leaking multi error lost causes: %v", sanitizedOuter)
		}
	})

	// 4) uerr.Op containing proxy token is scrubbed.
	t.Run("URLErrorOpRedacted", func(t *testing.T) {
		opLeak := &url.Error{
			Op:  "Put colab-runtime-proxy-token=" + tok,
			URL: "https://rt.example.invalid/api/contents/a",
			Err: context.DeadlineExceeded,
		}
		got := redactProviderURLError(opLeak)
		if strings.Contains(got.Error(), tok) {
			t.Fatalf("uerr.Op leaked token: %v", got)
		}
		var u *url.Error
		if !errors.As(got, &u) || strings.Contains(u.Op, tok) || !strings.Contains(u.Op, "REDACTED") {
			t.Fatalf("uerr.Op not redacted: %+v", u)
		}
		if !errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("uerr.Op redaction lost inner cause: %v", got)
		}
	})

	// 5) Empty oldMsg != "" guard prevents strings.ReplaceAll(s, "", rep) corruption.
	t.Run("EmptyOldMsgDoesNotCorruptOuterMessage", func(t *testing.T) {
		inner := emptyMsgWrapperErr{
			inner: &url.Error{
				Op:  "Get",
				URL: "https://rt.example.invalid/api?colab-runtime-proxy-token=" + tok,
				Err: context.Canceled,
			},
		}
		outerSingle := fmt.Errorf("wrapper-prefix: %w", inner)
		gotSingle := redactProviderURLError(outerSingle)
		if strings.Contains(gotSingle.Error(), tok) {
			t.Fatalf("empty oldMsg single wrapper leaked token: %v", gotSingle)
		}
		if gotSingle.Error() != "wrapper-prefix: " {
			t.Fatalf("empty oldMsg single wrapper corrupted message: %q", gotSingle.Error())
		}
		if !errors.Is(gotSingle, context.Canceled) {
			t.Fatalf("empty oldMsg single wrapper lost cause: %v", gotSingle)
		}

		outerMulti := customMultiProviderErr{
			msg:  "multi-prefix",
			errs: []error{inner, context.DeadlineExceeded},
		}
		gotMulti := redactProviderURLError(outerMulti)
		if gotMulti.Error() != "multi-prefix" {
			t.Fatalf("empty oldMsg multi wrapper corrupted message: %q", gotMulti.Error())
		}
		if !errors.Is(gotMulti, context.Canceled) || !errors.Is(gotMulti, context.DeadlineExceeded) {
			t.Fatalf("empty oldMsg multi wrapper lost causes: %v", gotMulti)
		}
	})

	// 6) Depth cap (64) terminates deep and cyclic error chains safely without stack overflow.
	t.Run("DepthCapHandlesDeepAndCyclicChains", func(t *testing.T) {
		var chain error = &url.Error{
			Op:  "Get",
			URL: "https://rt.example.invalid/api?colab-runtime-proxy-token=" + tok,
			Err: context.DeadlineExceeded,
		}
		for i := 0; i < 80; i++ {
			chain = fmt.Errorf("layer-%d: %w", i, chain)
		}
		gotDeep := redactProviderURLError(chain)
		if strings.Contains(gotDeep.Error(), tok) {
			t.Fatalf("deep error chain leaked token: %v", gotDeep)
		}

		cyc := &cyclicProviderErr{msg: "cycle colab-runtime-proxy-token=" + tok}
		cyc.next = cyc
		gotCyc := redactProviderURLError(cyc)
		if strings.Contains(gotCyc.Error(), tok) {
			t.Fatalf("cyclic error leaked token: %v", gotCyc)
		}
		if !strings.Contains(gotCyc.Error(), "REDACTED") {
			t.Fatalf("cyclic error missing REDACTED marker: %v", gotCyc)
		}
	})
}

func TestFormatProxyErrorStrictKindsTLSTypesAnd407Boundaries(t *testing.T) {
	const (
		connectMsg = "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试"
		authMsg    = "GPU 分配代理认证失败，请检查代理用户名与密码后重试"
		timeoutMsg = "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性"
		sslMsg     = "通过分配代理建立加密连接失败（证书校验未通过），请勿使用会解密 HTTPS 流量的代理"
	)

	// Explicit AssignProxyError kinds must short-circuit and NEVER fall through to auth substring matching.
	kindCases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "proxy_kind_with_connect_msg_containing_认证信息",
			err:  &AssignProxyError{Kind: "proxy", Message: connectMsg},
			want: connectMsg,
		},
		{
			name: "proxy_kind_even_with_407_or_认证失败_text",
			err:  &AssignProxyError{Kind: "proxy", Message: "407 认证失败"},
			want: connectMsg,
		},
		{
			name: "connect_timeout_kind_short_circuits",
			err:  &AssignProxyError{Kind: "connect_timeout", Message: "407 认证信息 超时"},
			want: timeoutMsg,
		},
		{
			name: "timeout_kind_short_circuits",
			err:  &AssignProxyError{Kind: "timeout", Message: "认证信息"},
			want: timeoutMsg,
		},
		{
			name: "ssl_kind_short_circuits",
			err:  &AssignProxyError{Kind: "ssl", Message: "认证失败"},
			want: sslMsg,
		},
		{
			name: "auth_kind_short_circuits",
			err:  &AssignProxyError{Kind: "auth", Message: "generic error"},
			want: authMsg,
		},
	}
	for _, tc := range kindCases {
		if got := FormatProxyError(tc.err); got != tc.want {
			t.Errorf("%s: FormatProxyError = %q, want %q", tc.name, got, tc.want)
		}
	}

	// Port :407: and unclassified "认证信息" must NOT trigger auth classification.
	for _, sample := range []string{
		"dial tcp 127.0.0.1:407: connect: connection refused",
		"dial tcp 127.0.0.1:40712: connect: connection refused",
		"dial tcp 127.0.0.1:54079: connect: connection refused",
		connectMsg,
	} {
		if got := FormatProxyError(errors.New(sample)); got != connectMsg {
			t.Errorf("FormatProxyError(%q) = %q, want %q", sample, got, connectMsg)
		}
	}

	// Specific unclassified auth errors and word-bounded 407 must classify as auth.
	for _, sample := range []string{
		"proxyconnect tcp: 407 Proxy Authentication Required",
		"proxyconnect tcp: HTTP (407)",
		"SOCKS5 代理用户名或密码认证失败",
		"SOCKS5 代理拒绝认证方式或需要用户名与密码",
		"SOCKS5 代理要求用户名与密码认证",
		"代理需要用户名与密码",
	} {
		if got := FormatProxyError(errors.New(sample)); got != authMsg {
			t.Errorf("FormatProxyError(%q) = %q, want %q", sample, got, authMsg)
		}
	}

	// Extended TLS/x509 error types must classify as SSL verification failures.
	tlsErrors := []error{
		&tls.CertificateVerificationError{Err: errors.New("verify failed")},
		x509.SystemRootsError{Err: errors.New("no system roots")},
		x509.InsecureAlgorithmError(0),
		tls.AlertError(42),
		errors.New("x509: certificate has expired"),
	}
	for _, te := range tlsErrors {
		if got := FormatProxyError(te); got != sslMsg {
			t.Errorf("FormatProxyError(%T) = %q, want %q", te, got, sslMsg)
		}
	}
}

func TestParseAssignProxyFallbackParity(t *testing.T) {
	// 1. Reject signed port numbers like "+8080" and "-80".
	for _, bad := range []string{
		"http://127.0.0.1:+8080",
		"http://127.0.0.1:-80",
		"socks5://[::1]:+1080",
	} {
		if _, err := ParseAssignProxy(bad); err == nil {
			t.Errorf("ParseAssignProxy(%q) expected error for signed port", bad)
		}
	}

	// 2. Support IDNA / non-ASCII hostnames cleanly.
	idnaSpec, err := ParseAssignProxy("http://bücher.example:8080")
	if err != nil || idnaSpec == nil {
		t.Fatalf("ParseAssignProxy(IDNA) failed: %v", err)
	}
	if idnaSpec.Host != "xn--bcher-kva.example" {
		t.Fatalf("IDNA host = %q, want %q", idnaSpec.Host, "xn--bcher-kva.example")
	}
	zhSpec, err := ParseAssignProxy("socks5://代理.example.com:1080")
	if err != nil || zhSpec == nil {
		t.Fatalf("ParseAssignProxy(CJK IDNA) failed: %v", err)
	}
	if !strings.HasPrefix(zhSpec.Host, "xn--") || !strings.HasSuffix(zhSpec.Host, ".example.com") {
		t.Fatalf("unexpected CJK IDNA host: %q", zhSpec.Host)
	}

	// 3. RFC 3986 unreserved-safe percent encoding keeps '~' unencoded (not %7E).
	tildeSpec, err := ParseAssignProxy("http://u~1:p~2@127.0.0.1:8080")
	if err != nil || tildeSpec == nil {
		t.Fatalf("ParseAssignProxy(tilde) failed: %v", err)
	}
	if tildeSpec.NormalizedURL != "http://u~1:p~2@127.0.0.1:8080" {
		t.Fatalf("tilde NormalizedURL = %q, want %q", tildeSpec.NormalizedURL, "http://u~1:p~2@127.0.0.1:8080")
	}

	// 4. Passwords containing literal '%' followed by non-hex characters alongside valid %HH.
	pctSpec, err := ParseAssignProxy("http://alice:p%ZZ%40x@127.0.0.1:8080")
	if err != nil || pctSpec == nil {
		t.Fatalf("ParseAssignProxy(literal percent) failed: %v", err)
	}
	if pctSpec.Password != "p%ZZ@x" {
		t.Fatalf("Password = %q, want %q", pctSpec.Password, "p%ZZ@x")
	}
	if pctSpec.NormalizedURL != "http://alice:p%25ZZ%40x@127.0.0.1:8080" {
		t.Fatalf("NormalizedURL = %q, want %q", pctSpec.NormalizedURL, "http://alice:p%25ZZ%40x@127.0.0.1:8080")
	}
}

func TestClearOrphanedRemotesAtomicPreservesLiveSessionsAndChannels(t *testing.T) {
	vault := newMemoryVault()
	var fk *fakeKernel
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault: vault,
		KernelFactory: func(opts KernelOptions) KernelClient {
			fk = newFakeKernel(opts)
			return fk
		},
	})

	// 1. Seed stale remotes across all vault layouts (remote, remotes, remote_ids, remote:<ep>) with no sessions.
	_ = vault.Set("remote", map[string]any{"endpoint": "ep-legacy"})
	_ = vault.Set("remotes", map[string]any{"ep-map": map[string]any{"endpoint": "ep-map"}})
	_ = vault.Set("remote_ids", []string{"ep-orphan", "ep-corrupt"})
	_ = vault.Set("remote:ep-orphan", map[string]any{"endpoint": "ep-orphan"})
	_ = vault.Set("remote:ep-corrupt", map[string]any{})
	_ = vault.Set("remote:ep-legacy", map[string]any{"endpoint": "ep-legacy"})
	_ = vault.Set("remote:ep-map", map[string]any{"endpoint": "ep-map"})

	// Even if a kernel slot exists in memory, ClearOrphanedRemotes must NOT touch kernel channels.
	sessNoVault := &ColabOneSessionState{Endpoint: "ep-mem-only", Token: "tok", URL: "https://mem.invalid"}
	if _, err := p.Execute(context.Background(), sessNoVault, "pass", ExecuteOptions{}); err != nil {
		t.Fatalf("Execute warmup failed: %v", err)
	}

	p.ClearOrphanedRemotes()

	for _, key := range []string{"remote", "remotes", "remote_ids", "remote:ep-orphan", "remote:ep-corrupt", "remote:ep-legacy", "remote:ep-map"} {
		if vault.Has(key) {
			t.Fatalf("expected orphaned key %q deleted", key)
		}
	}
	if len(p.RemotesMap()) != 0 {
		t.Fatalf("expected empty RemotesMap, got %#v", p.RemotesMap())
	}
	if fk == nil || fk.aborted.Load() || len(fk.stopCalls) != 0 {
		t.Fatalf("ClearOrphanedRemotes must NOT touch active kernel channels: aborted=%v stops=%v", fk.aborted.Load(), fk.stopCalls)
	}

	// 2. When a live managed session exists, ClearOrphanedRemotes is an atomic no-op.
	liveSess := &ColabOneSessionState{Endpoint: "ep-live", Token: "tok-live", URL: "https://live.invalid"}
	if err := p.SaveSession(liveSess, true); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := p.SaveRemote(map[string]any{"endpoint": "ep-live", "gpu": "L4"}, true); err != nil {
		t.Fatalf("SaveRemote: %v", err)
	}

	p.ClearOrphanedRemotes()

	if len(p.SessionsMap()) != 1 || len(p.RemotesMap()) != 1 {
		t.Fatalf("ClearOrphanedRemotes removed live session or remote: sessions=%#v remotes=%#v", p.SessionsMap(), p.RemotesMap())
	}
	if !vault.Has("remote:ep-live") || !vault.Has("remote_ids") || !vault.Has("remote") {
		t.Fatalf("expected live remote keys preserved in vault")
	}
}

func TestRPCDeleteRemoteFileSkippedOnCloseAndBoundedTimeout(t *testing.T) {
	t.Run("CloseSkipsDeleteRemoteFileAndReturnsPromptly", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		execEntered := make(chan struct{})
		contents := &fakeContents{
			deleteFn: func(ctx context.Context, baseURL, proxyToken, remotePath string) error {
				t.Errorf("DeleteFile must NOT be called after Close(), called with %q", remotePath)
				return nil
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:    vault,
			Contents: contents,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					close(execEntered)
					select {
					case <-fk.abortCh:
						return nil, errors.New("Colab WebSocket connection aborted")
					case <-time.After(10 * time.Second):
						return nil, errors.New("timed out waiting for Close abort")
					}
				}
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-rpc-close",
			Token:          "tok",
			URL:            "https://rpc-close.invalid",
			TokenExpiresAt: &exp,
		}, true)

		errCh := make(chan error, 1)
		go func() {
			_, err := p.RPC(context.Background(), "POST", "models/start", map[string]any{"id": "m1"}, "ep-rpc-close", 30*time.Second)
			errCh <- err
		}()

		select {
		case <-execEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for RPC ExecuteCode")
		}

		started := time.Now()
		p.Close()
		if time.Since(started) > 2*time.Second {
			t.Fatalf("Close() stalled: %v", time.Since(started))
		}
		select {
		case err := <-errCh:
			if !errors.Is(err, ErrProviderClosed) {
				t.Fatalf("expected ErrProviderClosed, got %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("RPC did not return after Close()")
		}
		contents.mu.Lock()
		delCount := len(contents.deletes)
		contents.mu.Unlock()
		if delCount != 0 {
			t.Fatalf("expected 0 DeleteFile calls when closed, got %d", delCount)
		}
	})

	t.Run("NonCloseFailureBoundsDeleteRemoteFileTo3sAndCancelsOnClose", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		var capturedRemaining time.Duration
		var hadDeadline bool
		deleteEntered := make(chan struct{})
		contents := &fakeContents{
			deleteFn: func(ctx context.Context, baseURL, proxyToken, remotePath string) error {
				if dl, ok := ctx.Deadline(); ok {
					hadDeadline = true
					capturedRemaining = time.Until(dl)
				}
				close(deleteEntered)
				<-ctx.Done()
				return ctx.Err()
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:    vault,
			Contents: contents,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					return nil, errors.New("remote execution boom")
				}
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-rpc-del-timeout",
			Token:          "tok",
			URL:            "https://rpc-del.invalid",
			TokenExpiresAt: &exp,
		}, true)

		errCh := make(chan error, 1)
		go func() {
			_, err := p.RPC(context.Background(), "POST", "models/start", map[string]any{"id": "m1"}, "ep-rpc-del-timeout", 30*time.Second)
			errCh <- err
		}()

		select {
		case <-deleteEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for DeleteFile")
		}
		if !hadDeadline || capturedRemaining <= 0 || capturedRemaining > 3500*time.Millisecond {
			t.Fatalf("expected DeleteFile bounded to ~3s timeout, hadDeadline=%v remaining=%v", hadDeadline, capturedRemaining)
		}

		// Calling Close() while DeleteFile is blocked cancels delCtx promptly instead of waiting 3s.
		started := time.Now()
		p.Close()
		if time.Since(started) > 1500*time.Millisecond {
			t.Fatalf("Close() stalled on in-flight DeleteFile: %v", time.Since(started))
		}
		select {
		case err := <-errCh:
			if !errors.Is(err, ErrProviderClosed) {
				t.Fatalf("expected ErrProviderClosed when Close interrupted DeleteFile, got %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("RPC did not return after Close() interrupted DeleteFile")
		}
	})
}

func TestClearAndReleaseAbortInFlightKernelExecutionPromptly(t *testing.T) {
	t.Run("ClearAbortsTargetInFlightRPCWithoutTouchingOtherEndpoint", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		exec1Entered := make(chan struct{})
		var k1, k2 *fakeKernel
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault: vault,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				if opts.ServerURL == "https://ep1.invalid" {
					k1 = fk
					fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
						close(exec1Entered)
						select {
						case <-fk.abortCh:
							return nil, errors.New("Colab WebSocket connection aborted")
						case <-time.After(10 * time.Second):
							return nil, errors.New("timed out waiting for Clear abort")
						}
					}
				} else {
					k2 = fk
				}
				return fk
			},
		})
		sess1 := &ColabOneSessionState{Endpoint: "ep-1", Token: "t1", URL: "https://ep1.invalid", TokenExpiresAt: &exp}
		sess2 := &ColabOneSessionState{Endpoint: "ep-2", Token: "t2", URL: "https://ep2.invalid", TokenExpiresAt: &exp}
		_ = p.SaveSession(sess1, true)
		_ = p.SaveSession(sess2, false)

		// Warm up ep-2 so it has an active kernel slot too.
		if _, err := p.Execute(context.Background(), sess2, "pass", ExecuteOptions{}); err != nil {
			t.Fatalf("ep-2 warmup failed: %v", err)
		}

		// Launch an in-flight idempotent GET RPC on ep-1 that hangs on WebSocket.
		rpcErrCh := make(chan error, 1)
		go func() {
			_, err := p.RPC(context.Background(), "GET", "status", nil, "ep-1", 30*time.Second)
			rpcErrCh <- err
		}()

		select {
		case <-exec1Entered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for ep-1 RPC to enter ExecuteCode")
		}

		started := time.Now()
		if err := p.Clear("ep-1"); err != nil {
			t.Fatalf("Clear(ep-1) failed: %v", err)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("Clear(ep-1) stalled on in-flight RPC: %v", elapsed)
		}

		select {
		case err := <-rpcErrCh:
			if err == nil {
				t.Fatal("expected aborted RPC error")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("in-flight RPC did not unwind after Clear")
		}
		if k1 == nil || !k1.aborted.Load() {
			t.Fatal("expected Clear(ep-1) to abort k1")
		}
		if k2 == nil || k2.aborted.Load() || len(k2.stopCalls) != 0 {
			t.Fatalf("Clear(ep-1) must NOT abort or stop unrelated ep-2: aborted=%v stops=%v", k2.aborted.Load(), k2.stopCalls)
		}
	})

	t.Run("ReleaseUnwindsInFlightRPCWithoutBlocking", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		execEntered := make(chan struct{})
		var fk *fakeKernel
		client := &fakeColabClient{
			listFn: func(ctx context.Context) ([]ListedAssignment, error) {
				// Report runtime already gone on Colab so Release proceeds straight to Clear.
				return nil, nil
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:         vault,
			ClientFactory: func(string) (ColabClient, error) { return client, nil },
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk = newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					close(execEntered)
					select {
					case <-fk.abortCh:
						return nil, errors.New("Colab WebSocket connection aborted")
					case <-time.After(10 * time.Second):
						return nil, errors.New("timed out waiting for Release abort")
					}
				}
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-rel",
			Token:          "tok",
			URL:            "https://rel.invalid",
			TokenExpiresAt: &exp,
		}, true)

		rpcDone := make(chan error, 1)
		go func() {
			_, err := p.RPC(context.Background(), "POST", "stop", nil, "ep-rel", 30*time.Second)
			rpcDone <- err
		}()
		select {
		case <-execEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for RPC ExecuteCode")
		}

		started := time.Now()
		if _, err := p.Release(context.Background(), "ep-rel", nil); err != nil {
			t.Fatalf("Release failed: %v", err)
		}
		if time.Since(started) > 2*time.Second {
			t.Fatalf("Release stalled on in-flight RPC: %v", time.Since(started))
		}
		select {
		case <-rpcDone:
		case <-time.After(3 * time.Second):
			t.Fatal("in-flight RPC did not unwind after Release")
		}
		if fk == nil || !fk.aborted.Load() {
			t.Fatal("expected Release to abort in-flight kernel execution")
		}
	})

	t.Run("ClearEmptyWithNoSessionsAbortsAllLiveSlots", func(t *testing.T) {
		vault := newMemoryVault()
		execEntered := make(chan struct{})
		var fk *fakeKernel
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault: vault,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk = newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					close(execEntered)
					select {
					case <-fk.abortCh:
						return nil, errors.New("Colab WebSocket connection aborted")
					case <-time.After(10 * time.Second):
						return nil, errors.New("timed out waiting for Clear('') abort")
					}
				}
				return fk
			},
		})
		// Execute directly without saving session in vault so Clear("") takes the empty-target path.
		done := make(chan error, 1)
		go func() {
			_, err := p.Execute(context.Background(), &ColabOneSessionState{
				Endpoint: "ep-orphan-slot",
				Token:    "tok",
				URL:      "https://orphan.invalid",
			}, "pass", ExecuteOptions{})
			done <- err
		}()
		select {
		case <-execEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for ExecuteCode")
		}

		started := time.Now()
		if err := p.Clear(""); err != nil {
			t.Fatalf("Clear('') failed: %v", err)
		}
		if time.Since(started) > 2*time.Second {
			t.Fatalf("Clear('') stalled: %v", time.Since(started))
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Execute did not unwind after Clear('')")
		}
		if fk == nil || !fk.aborted.Load() {
			t.Fatal("expected Clear('') to abort live orphan slot")
		}
	})
}

func TestCloseAbortsInFlightMountColabDriveFSAndNilKernelGuard(t *testing.T) {
	t.Run("NilKernelFactoryResultReturnsErrorInsteadOfPanicking", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:         vault,
			ClientFactory: func(string) (ColabClient, error) { return &fakeColabClient{}, nil },
			DriveMounterFactory: func(client ColabClient, endpoint string, opts DriveMountOptions) DriveMounter {
				return &fakeDriveMounter{}
			},
			KernelFactory: func(opts KernelOptions) KernelClient {
				return nil
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-nil-rt",
			Token:          "tok",
			URL:            "https://nil-rt.invalid",
			TokenExpiresAt: &exp,
		}, true)

		_, err := p.MountColabDriveFS(context.Background(), "ep-nil-rt", DriveMountOptions{})
		if err == nil || !strings.Contains(err.Error(), "未配置远端内核客户端工厂") {
			t.Fatalf("expected missing kernel factory error, got %v", err)
		}
	})

	t.Run("CloseAbortsInFlightMountColabDriveFSAndReturnsErrProviderClosed", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		execEntered := make(chan struct{})
		var fk *fakeKernel
		var dm *fakeDriveMounter

		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:         vault,
			ClientFactory: func(string) (ColabClient, error) { return &fakeColabClient{}, nil },
			DriveMounterFactory: func(client ColabClient, endpoint string, opts DriveMountOptions) DriveMounter {
				dm = &fakeDriveMounter{}
				dm.stopFn = func() {
					// Simulate drivemount setting a cancel error on Stop; MountColabDriveFS
					// must still return ErrProviderClosed when p.closed is true.
					dm.err = errors.New("已取消 Google 云盘挂载")
				}
				return dm
			},
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk = newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					close(execEntered)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-fk.abortCh:
						return nil, errors.New("aborted")
					case <-time.After(10 * time.Second):
						return nil, errors.New("timed out waiting for Close() to abort MountColabDriveFS")
					}
				}
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-drive-close",
			Token:          "tok",
			URL:            "https://drive-close.invalid",
			TokenExpiresAt: &exp,
		}, true)

		mountErrCh := make(chan error, 1)
		go func() {
			_, err := p.MountColabDriveFS(context.Background(), "ep-drive-close", DriveMountOptions{})
			mountErrCh <- err
		}()

		select {
		case <-execEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for MountColabDriveFS ExecuteCode")
		}

		started := time.Now()
		p.Close()
		var mountErr error
		select {
		case mountErr = <-mountErrCh:
		case <-time.After(3 * time.Second):
			t.Fatal("MountColabDriveFS did not unwind after Close()")
		}
		if time.Since(started) > 2*time.Second {
			t.Fatalf("MountColabDriveFS unwind took too long after Close(): %v", time.Since(started))
		}
		if !errors.Is(mountErr, ErrProviderClosed) {
			t.Fatalf("expected ErrProviderClosed from MountColabDriveFS after Close(), got %v", mountErr)
		}
		if fk == nil || !fk.aborted.Load() {
			t.Fatal("expected Close() to abort MountColabDriveFS kernel")
		}
		dm.mu.Lock()
		dmStopped := dm.stopped
		dm.mu.Unlock()
		if !dmStopped {
			t.Fatal("expected Close() to stop DriveMounter")
		}
	})

	t.Run("StructLiteralProviderCloseWithNilClosedChIsSafe", func(t *testing.T) {
		p := &ColabOneProvider{}
		p.Close()
		p.Close()
		if !p.closed.Load() {
			t.Fatal("expected closed == true")
		}
	})
}

func TestProxyErrorSingleSourceOfTruthFastFailAndFormatting(t *testing.T) {
	const (
		connectMsg   = "GPU 分配代理连接失败，请检查代理地址、端口、认证信息或服务状态后重试"
		timeoutMsg   = "连接 GPU 分配代理超时，请检查代理地址、端口或网络连通性"
		forbiddenMsg = "仅 GPU 分配请求允许使用分配代理"
	)

	t.Run("FastFailHandlesTimeoutKindAndDirectAssignProxyErrors", func(t *testing.T) {
		if !isAssignProxyFastFailError(&AssignProxyError{Kind: "timeout", Message: "proxy timed out"}) {
			t.Fatal("expected &AssignProxyError{Kind: \"timeout\"} to fail fast")
		}
		if isAssignProxyFastFailError(&AssignProxyError{Kind: "read_timeout", Message: "upstream slow"}) {
			t.Fatal("expected &AssignProxyError{Kind: \"read_timeout\"} NOT to fail fast")
		}
		directErrors := []error{
			&assignproxy.ProxyError{Kind: assignproxy.ErrProxyAuth, Message: "auth"},
			&assignproxy.ProxyError{Kind: assignproxy.ErrProxyTLS, Message: "tls"},
			&assignproxy.ProxyError{Kind: assignproxy.ErrProxyTimeout, Message: "timeout"},
			&assignproxy.ProxyError{Kind: assignproxy.ErrProxyConnect, Message: "connect"},
			&assignproxy.ProxyError{Kind: assignproxy.ErrForbiddenTarget, Message: forbiddenMsg},
			fmt.Errorf("wrapped: %w", &assignproxy.ProxyError{Kind: assignproxy.ErrProxyConnect, Message: "connect"}),
		}
		for _, err := range directErrors {
			if !isAssignProxyFastFailError(err) {
				t.Errorf("expected direct %v to fail fast", err)
			}
		}

		// End-to-end Allocate fast-fail on &AssignProxyError{Kind: "timeout"} (1 attempt, zero retries).
		timeoutAttempts := 0
		pTimeout := NewColabOneProvider(ColabOneProviderConfig{
			Vault:             newMemoryVault(),
			AssignRetryDelays: []time.Duration{6 * time.Second, 12 * time.Second},
			ClientFactory: func(string) (ColabClient, error) {
				return &fakeColabClient{
					getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
						timeoutAttempts++
						return nil, "", &AssignProxyError{Kind: "timeout", Message: "proxy dial timeout"}
					},
				}, nil
			},
		})
		_, err := pTimeout.Allocate(context.Background(), "T4", AllocateOptions{AssignProxy: "http://127.0.0.1:7890"})
		if err == nil || err.Error() != timeoutMsg || timeoutAttempts != 1 {
			t.Fatalf("expected timeout kind fast-fail: err=%v attempts=%d", err, timeoutAttempts)
		}

		// End-to-end Allocate fast-fail on direct *assignproxy.ProxyError (no AssignProxyError wrapper).
		directAttempts := 0
		pDirect := NewColabOneProvider(ColabOneProviderConfig{
			Vault:             newMemoryVault(),
			AssignRetryDelays: []time.Duration{6 * time.Second, 12 * time.Second},
			ClientFactory: func(string) (ColabClient, error) {
				return &fakeColabClient{
					getAssignFn: func(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
						directAttempts++
						return nil, "", &assignproxy.ProxyError{
							Kind:    assignproxy.ErrForbiddenTarget,
							Message: forbiddenMsg,
						}
					},
				}, nil
			},
		})
		_, err = pDirect.Allocate(context.Background(), "T4", AllocateOptions{AssignProxy: "http://127.0.0.1:7890"})
		if err == nil || err.Error() != forbiddenMsg || directAttempts != 1 {
			t.Fatalf("expected direct *assignproxy.ProxyError fast-fail: err=%v attempts=%d", err, directAttempts)
		}
	})

	t.Run("FormatProxyErrorMatchesAssignProxySingleSourceOfTruth", func(t *testing.T) {
		forbiddenPE := &assignproxy.ProxyError{
			Kind:    assignproxy.ErrForbiddenTarget,
			Message: assignproxy.ErrForbiddenTarget.Error(),
		}
		socksTTLPE := &assignproxy.ProxyError{
			Kind:    assignproxy.ErrProxyConnect,
			Message: "SOCKS5 代理连接失败：SOCKS5 代理连接超时 (TTL expired)",
		}
		connect407PE := &assignproxy.ProxyError{
			Kind:    assignproxy.ErrProxyConnect,
			Message: "代理拒绝连接 (HTTP 407)",
		}

		cases := []struct {
			name string
			err  error
			want string
		}{
			{
				name: "direct_forbidden_target",
				err:  forbiddenPE,
				want: forbiddenMsg,
			},
			{
				name: "wrapped_forbidden_target_in_assign_proxy_error",
				err:  &AssignProxyError{Kind: "proxy", Message: forbiddenPE.Error(), Err: forbiddenPE},
				want: forbiddenMsg,
			},
			{
				name: "direct_socks5_ttl_expired",
				err:  socksTTLPE,
				want: timeoutMsg,
			},
			{
				name: "wrapped_socks5_ttl_expired_in_assign_proxy_error",
				err:  &AssignProxyError{Kind: "proxy", Message: socksTTLPE.Error(), Err: socksTTLPE},
				want: timeoutMsg,
			},
			{
				name: "direct_connect_with_407_text",
				err:  connect407PE,
				want: connectMsg,
			},
			{
				name: "wrapped_connect_with_407_text_in_assign_proxy_error",
				err:  &AssignProxyError{Kind: "proxy", Message: connect407PE.Error(), Err: connect407PE},
				want: connectMsg,
			},
		}
		for _, tc := range cases {
			gotProvider := FormatProxyError(tc.err)
			gotAssignProxy := assignproxy.FormatProxyError(tc.err)
			if gotProvider != tc.want {
				t.Errorf("%s: provider.FormatProxyError = %q, want %q", tc.name, gotProvider, tc.want)
			}
			if gotProvider != gotAssignProxy {
				t.Errorf("%s: provider.FormatProxyError (%q) != assignproxy.FormatProxyError (%q)", tc.name, gotProvider, gotAssignProxy)
			}
		}
	})
}

func TestDriveFSNilMounterGuardsAndStopCleanup(t *testing.T) {
	newSessionProvider := func(kf KernelFactory, df DriveMounterFactory) *ColabOneProvider {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:               vault,
			ClientFactory:       func(string) (ColabClient, error) { return &fakeColabClient{}, nil },
			KernelFactory:       kf,
			DriveMounterFactory: df,
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-drive-guard",
			Token:          "tok",
			URL:            "https://drive-guard.invalid",
			TokenExpiresAt: &exp,
		}, true)
		return p
	}

	t.Run("MountColabDriveFSWithNilKernelDoesNotInvokeDriveMounterFactory", func(t *testing.T) {
		var mounterFactoryCalls atomic.Int32
		p := newSessionProvider(
			func(opts KernelOptions) KernelClient { return nil },
			func(client ColabClient, endpoint string, opts DriveMountOptions) DriveMounter {
				mounterFactoryCalls.Add(1)
				return &fakeDriveMounter{}
			},
		)
		_, err := p.MountColabDriveFS(context.Background(), "ep-drive-guard", DriveMountOptions{})
		if err == nil || err.Error() != "未配置远端内核客户端工厂" {
			t.Fatalf("expected missing kernel factory error, got %v", err)
		}
		if mounterFactoryCalls.Load() != 0 {
			t.Fatalf("expected driveMounterFactory NOT called when kernelFactory returns nil, got %d calls", mounterFactoryCalls.Load())
		}
	})

	t.Run("MountColabDriveFSWithNilMounterReturnsErrorWithoutPanicking", func(t *testing.T) {
		p := newSessionProvider(
			func(opts KernelOptions) KernelClient { return newFakeKernel(opts) },
			func(client ColabClient, endpoint string, opts DriveMountOptions) DriveMounter { return nil },
		)
		_, err := p.MountColabDriveFS(context.Background(), "ep-drive-guard", DriveMountOptions{})
		if err == nil || err.Error() != "未配置 Google 云盘挂载器" {
			t.Fatalf("expected missing drive mounter error, got %v", err)
		}
	})

	t.Run("CheckColabDriveFSAuthNilMounterAndStopCalled", func(t *testing.T) {
		pNil := newSessionProvider(
			nil,
			func(client ColabClient, endpoint string, opts DriveMountOptions) DriveMounter { return nil },
		)
		ok, urlStr, err := pNil.CheckColabDriveFSAuth(context.Background(), "ep-drive-guard")
		if ok || urlStr != "" || err == nil || err.Error() != "未配置 Google 云盘挂载器" {
			t.Fatalf("unexpected nil mounter CheckColabDriveFSAuth result: ok=%v url=%q err=%v", ok, urlStr, err)
		}

		// Success path calls Stop().
		dmOK := &fakeDriveMounter{dryrunOK: true, dryrunURL: ""}
		pOK := newSessionProvider(
			nil,
			func(client ColabClient, endpoint string, opts DriveMountOptions) DriveMounter { return dmOK },
		)
		ok, urlStr, err = pOK.CheckColabDriveFSAuth(context.Background(), "ep-drive-guard")
		if err != nil || !ok || urlStr != "" {
			t.Fatalf("unexpected CheckColabDriveFSAuth success: ok=%v url=%q err=%v", ok, urlStr, err)
		}
		dmOK.mu.Lock()
		stoppedOK := dmOK.stopped
		dmOK.mu.Unlock()
		if !stoppedOK {
			t.Fatal("expected CheckColabDriveFSAuth to call mount.Stop() on success")
		}

		// Error path also calls Stop().
		dmErr := &fakeDriveMounter{dryrunErr: errors.New("dryrun boom")}
		pErr := newSessionProvider(
			nil,
			func(client ColabClient, endpoint string, opts DriveMountOptions) DriveMounter { return dmErr },
		)
		_, _, err = pErr.CheckColabDriveFSAuth(context.Background(), "ep-drive-guard")
		if err == nil || !strings.Contains(err.Error(), "dryrun boom") {
			t.Fatalf("expected dryrun error, got %v", err)
		}
		dmErr.mu.Lock()
		stoppedErr := dmErr.stopped
		dmErr.mu.Unlock()
		if !stoppedErr {
			t.Fatal("expected CheckColabDriveFSAuth to call mount.Stop() on error")
		}
	})
}

func TestClearPerSlotClearingDekkerGuardAndAbortedWSNotTransient(t *testing.T) {
	waitDetached := func(t *testing.T, p *ColabOneProvider, ep string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			p.kernelMu.Lock()
			_, stillPresent := p.kernels[ep]
			p.kernelMu.Unlock()
			if !stillPresent {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("timed out waiting for Clear(%q) to detach slot from p.kernels", ep)
	}

	t.Run("ClearDuringUploadBeforeExecuteLockedSkipsFactoryStartAndExec", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		uploadEntered := make(chan struct{})
		releaseUpload := make(chan struct{})
		var factoryCalls atomic.Int32

		var deletedPath atomic.Value
		contents := &fakeContents{
			uploadFn: func(ctx context.Context, baseURL, proxyToken, remotePath string, data []byte, timeout time.Duration) error {
				close(uploadEntered)
				<-releaseUpload
				return nil
			},
			deleteFn: func(ctx context.Context, baseURL, proxyToken, remotePath string) error {
				deletedPath.Store(remotePath)
				return nil
			},
		}

		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:    vault,
			Contents: contents,
			KernelFactory: func(opts KernelOptions) KernelClient {
				factoryCalls.Add(1)
				fk := newFakeKernel(opts)
				fk.startFn = func(ctx context.Context) error {
					t.Errorf("rt.Start must NOT be called after Clear detached slot")
					return errors.New("unexpected Start")
				}
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					t.Errorf("rt.ExecuteCode must NOT be called after Clear detached slot")
					return nil, errors.New("unexpected ExecuteCode")
				}
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-pre-exec-upload",
			Token:          "tok",
			URL:            "https://pre-exec.invalid",
			TokenExpiresAt: &exp,
		}, true)

		rpcErrCh := make(chan error, 1)
		go func() {
			_, err := p.RPC(context.Background(), "POST", "models/start", map[string]any{"id": "m1"}, "ep-pre-exec-upload", 30*time.Second)
			rpcErrCh <- err
		}()

		select {
		case <-uploadEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for RPC uploadBytesWithSlot")
		}

		p.kernelMu.Lock()
		slot := p.kernels["ep-pre-exec-upload"]
		p.kernelMu.Unlock()
		if slot == nil {
			t.Fatal("expected slot present while RPC holds slot.mu in uploadBytesWithSlot")
		}

		clearDone := make(chan error, 1)
		go func() {
			clearDone <- p.Clear("ep-pre-exec-upload")
		}()
		waitDetached(t, p, "ep-pre-exec-upload")
		if !slot.clearing.Load() {
			t.Fatal("expected slot.clearing to be set immediately upon detach")
		}

		// Because Phase-1 upload is unlocked, Clear completes immediately BEFORE upload finishes.
		select {
		case err := <-clearDone:
			if err != nil {
				t.Fatalf("Clear failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Clear stalled waiting for unlocked Phase-1 upload")
		}

		// Release upload; RPC must observe detached slot, clean up the ephemeral RPC file outside slot.mu, and unwind immediately.
		started := time.Now()
		close(releaseUpload)

		select {
		case err := <-rpcErrCh:
			if err == nil || !strings.Contains(err.Error(), "请先启动远端 GPU") {
				t.Fatalf("expected missing GPU error from pre-exec RPC aborted by Clear, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("RPC stalled after Clear during upload")
		}
		if time.Since(started) > 1500*time.Millisecond {
			t.Fatalf("pre-exec unwind took too long: %v", time.Since(started))
		}
		if factoryCalls.Load() != 0 {
			t.Fatalf("expected zero KernelFactory calls when Clear raced during upload, got %d", factoryCalls.Load())
		}
		if slot.getRuntime() != nil {
			t.Fatal("expected cleared slot runtime to be nil")
		}
		gotDel, _ := deletedPath.Load().(string)
		if !strings.HasPrefix(gotDel, "content/colabone-rpc-") {
			t.Fatalf("expected ephemeral RPC file cleaned up after Clear during upload, got %q", gotDel)
		}
	})

	t.Run("ClearRacingBeforeSetRuntimeTriggersDekkerGuardWithoutCallingStart", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		factoryEntered := make(chan struct{})
		releaseFactory := make(chan struct{})
		var fk *fakeKernel
		var startCalls atomic.Int32
		var execCalls atomic.Int32

		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault: vault,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk = newFakeKernel(opts)
				fk.startFn = func(ctx context.Context) error {
					startCalls.Add(1)
					time.Sleep(5 * time.Second)
					return errors.New("Start should not have been called")
				}
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					execCalls.Add(1)
					time.Sleep(5 * time.Second)
					return nil, errors.New("ExecuteCode should not have been called")
				}
				close(factoryEntered)
				<-releaseFactory
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-dekker",
			Token:          "tok",
			URL:            "https://dekker.invalid",
			TokenExpiresAt: &exp,
		}, true)

		rpcErrCh := make(chan error, 1)
		go func() {
			_, err := p.RPC(context.Background(), "GET", "status", nil, "ep-dekker", 30*time.Second)
			rpcErrCh <- err
		}()

		select {
		case <-factoryEntered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for KernelFactory")
		}

		p.kernelMu.Lock()
		slot := p.kernels["ep-dekker"]
		p.kernelMu.Unlock()
		if slot == nil {
			t.Fatal("expected slot present before Clear")
		}

		// Clear runs while slot.runtime is still nil (before slot.setRuntime(rt)).
		clearDone := make(chan error, 1)
		go func() {
			clearDone <- p.Clear("ep-dekker")
		}()
		waitDetached(t, p, "ep-dekker")

		// Release KernelFactory so executeLocked calls slot.setRuntime(rt) and hits Dekker check.
		started := time.Now()
		close(releaseFactory)

		select {
		case err := <-rpcErrCh:
			if err == nil || !strings.Contains(err.Error(), "请先启动远端 GPU") {
				t.Fatalf("expected missing GPU error from Dekker guard, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("RPC stalled instead of unwinding at Dekker guard")
		}
		select {
		case err := <-clearDone:
			if err != nil {
				t.Fatalf("Clear failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Clear stalled waiting for Dekker unwind")
		}
		if time.Since(started) > 1500*time.Millisecond {
			t.Fatalf("Dekker unwind took too long: %v", time.Since(started))
		}
		if startCalls.Load() != 0 || execCalls.Load() != 0 {
			t.Fatalf("expected 0 Start and 0 ExecuteCode calls, got start=%d exec=%d", startCalls.Load(), execCalls.Load())
		}
		if slot.getRuntime() != nil {
			t.Fatal("expected slot.runtime cleared to nil")
		}
		if fk == nil || !reflect.DeepEqual(fk.stopCalls, []bool{false}) {
			t.Fatalf("expected rt.Stop(false) called on Dekker unwind, got %v", fk.stopCalls)
		}
	})

	t.Run("IsTransientWSErrorExcludesErrKernelAbortedAndSkipsIdempotentRetry", func(t *testing.T) {
		if isTransientWSError(jupyterws.ErrKernelAborted) {
			t.Fatal("isTransientWSError(jupyterws.ErrKernelAborted) = true, want false")
		}
		if isTransientWSError(fmt.Errorf("wrapped: %w", jupyterws.ErrKernelAborted)) {
			t.Fatal("isTransientWSError(wrapped ErrKernelAborted) = true, want false")
		}
		if isTransientWSError(errors.New("Colab WebSocket connection aborted")) {
			t.Fatal("isTransientWSError(\"Colab WebSocket connection aborted\") = true, want false")
		}
		if isTransientWSError(errors.New("COLAB WEBSOCKET CONNECTION ABORTED")) {
			t.Fatal("isTransientWSError(uppercase connection aborted) = true, want false")
		}
		if !isTransientWSError(jupyterws.ErrKernelDisconnected) {
			t.Fatal("isTransientWSError(jupyterws.ErrKernelDisconnected) = false, want true")
		}

		// Idempotent GET RPC receiving ErrKernelAborted must NOT retry.
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		var execAttempts atomic.Int32
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault: vault,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					execAttempts.Add(1)
					return nil, jupyterws.ErrKernelAborted
				}
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-aborted-rpc",
			Token:          "tok",
			URL:            "https://aborted.invalid",
			TokenExpiresAt: &exp,
		}, true)

		_, err := p.RPC(context.Background(), "GET", "status", nil, "ep-aborted-rpc", 30*time.Second)
		if !errors.Is(err, jupyterws.ErrKernelAborted) {
			t.Fatalf("expected ErrKernelAborted, got %v", err)
		}
		if execAttempts.Load() != 1 {
			t.Fatalf("expected idempotent GET RPC NOT to retry on ErrKernelAborted, got %d attempts", execAttempts.Load())
		}
	})
}

func TestRPCPhase2FailurePathsCleanUpEphemeralFile(t *testing.T) {
	t.Run("SlotReplacedWhileWaitingForLockCleansUpOutsideSlotMu", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		uploadEntered := make(chan struct{})
		releaseUpload := make(chan struct{})
		var p *ColabOneProvider
		var replacementSlot *kernelSlot
		var deleteUnlocked atomic.Bool

		contents := &fakeContents{
			uploadFn: func(ctx context.Context, baseURL, proxyToken, remotePath string, data []byte, timeout time.Duration) error {
				close(uploadEntered)
				<-releaseUpload
				return nil
			},
			deleteFn: func(ctx context.Context, baseURL, proxyToken, remotePath string) error {
				if replacementSlot != nil && replacementSlot.mu.TryLock() {
					deleteUnlocked.Store(true)
					replacementSlot.mu.Unlock()
				}
				return nil
			},
		}
		p = NewColabOneProvider(ColabOneProviderConfig{
			Vault:    vault,
			Contents: contents,
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-replaced",
			Token:          "tok-orig",
			URL:            "https://replaced.invalid",
			TokenExpiresAt: &exp,
		}, true)

		errCh := make(chan error, 1)
		go func() {
			_, err := p.RPC(context.Background(), "POST", "comfyui/run", map[string]any{"workflow_id": "wf1"}, "ep-replaced", 30*time.Second)
			errCh <- err
		}()

		<-uploadEntered
		p.kernelMu.Lock()
		originSlot := p.kernels["ep-replaced"]
		p.kernelMu.Unlock()
		if originSlot == nil {
			t.Fatal("expected originSlot to be registered")
		}

		// Hold originSlot.mu so RPC passes isSlotDetached and blocks inside lockKernelSlot.
		originSlot.mu.Lock()
		close(releaseUpload)
		time.Sleep(15 * time.Millisecond)

		// Evict originSlot and install a replacement slot in p.kernels before releasing originSlot.mu.
		replacementSlot = &kernelSlot{}
		p.kernelMu.Lock()
		originSlot.evicted = true
		p.kernels["ep-replaced"] = replacementSlot
		p.kernelMu.Unlock()
		originSlot.mu.Unlock()

		select {
		case err := <-errCh:
			if err == nil || !strings.Contains(err.Error(), "请先启动远端 GPU") {
				t.Fatalf("expected missing GPU error on slot replacement, got %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("RPC timed out after slot replacement")
		}
		contents.mu.Lock()
		uploads := append([]uploadRecord(nil), contents.uploads...)
		deletes := append([]string(nil), contents.deletes...)
		contents.mu.Unlock()
		if len(uploads) != 1 || len(deletes) != 1 || uploads[0].remotePath != deletes[0] {
			t.Fatalf("expected ephemeral RPC file cleaned up on slot replacement: uploads=%#v deletes=%#v", uploads, deletes)
		}
		if !deleteUnlocked.Load() {
			t.Fatal("expected DeleteFile to run outside slot.mu")
		}
	})

	t.Run("SessionNilInPhase2FallsBackToPrevSessionOutsideSlotMu", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		var p *ColabOneProvider
		var deleteToken string
		var deleteUnlocked atomic.Bool

		contents := &fakeContents{
			uploadFn: func(ctx context.Context, baseURL, proxyToken, remotePath string, data []byte, timeout time.Duration) error {
				// Remove session from vault while leaving kernel slot intact so Phase 2 p.session(ctx, false) returns nil.
				_ = vault.Delete("runtime_session:ep-sess-nil")
				_ = vault.Delete("runtime_session_ids")
				_ = vault.Delete("runtime_session")
				_ = vault.Delete("active_endpoint")
				return nil
			},
			deleteFn: func(ctx context.Context, baseURL, proxyToken, remotePath string) error {
				deleteToken = proxyToken
				p.kernelMu.Lock()
				s := p.kernels["ep-sess-nil"]
				p.kernelMu.Unlock()
				if s != nil && s.mu.TryLock() {
					deleteUnlocked.Store(true)
					s.mu.Unlock()
				}
				return nil
			},
		}
		p = NewColabOneProvider(ColabOneProviderConfig{
			Vault:    vault,
			Contents: contents,
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-sess-nil",
			Token:          "tok-prev",
			URL:            "https://sess-nil.invalid",
			TokenExpiresAt: &exp,
		}, true)

		_, err := p.RPC(context.Background(), "POST", "comfyui/run", map[string]any{"workflow_id": "wf1"}, "ep-sess-nil", 30*time.Second)
		if err == nil || !strings.Contains(err.Error(), "请先启动远端 GPU") {
			t.Fatalf("expected missing GPU error when session vanished, got %v", err)
		}
		contents.mu.Lock()
		uploads := append([]uploadRecord(nil), contents.uploads...)
		deletes := append([]string(nil), contents.deletes...)
		contents.mu.Unlock()
		if len(uploads) != 1 || len(deletes) != 1 || uploads[0].remotePath != deletes[0] {
			t.Fatalf("expected ephemeral RPC file cleaned up on nil session: uploads=%#v deletes=%#v", uploads, deletes)
		}
		if deleteToken != "tok-prev" {
			t.Fatalf("expected cleanup fallback to prevSession token %q, got %q", "tok-prev", deleteToken)
		}
		if !deleteUnlocked.Load() {
			t.Fatal("expected DeleteFile to run outside slot.mu")
		}
	})

	t.Run("ProxyTokenRefreshFailureInPhase2CleansUpOutsideSlotMu", func(t *testing.T) {
		vault := newMemoryVault()
		baseNow := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
		var nowMu sync.Mutex
		curNow := baseNow
		exp := baseNow.Add(10 * time.Minute)
		var p *ColabOneProvider
		var deleteToken string
		var deleteUnlocked atomic.Bool

		contents := &fakeContents{
			uploadFn: func(ctx context.Context, baseURL, proxyToken, remotePath string, data []byte, timeout time.Duration) error {
				// Advance clock so Phase 2 sees token expiring within 60s and triggers p.session(ctx, true).
				nowMu.Lock()
				curNow = exp.Add(-30 * time.Second)
				nowMu.Unlock()
				return nil
			},
			deleteFn: func(ctx context.Context, baseURL, proxyToken, remotePath string) error {
				deleteToken = proxyToken
				p.kernelMu.Lock()
				s := p.kernels["ep-refresh-fail"]
				p.kernelMu.Unlock()
				if s != nil && s.mu.TryLock() {
					deleteUnlocked.Store(true)
					s.mu.Unlock()
				}
				return nil
			},
		}
		p = NewColabOneProvider(ColabOneProviderConfig{
			Vault:    vault,
			Contents: contents,
			NowFn: func() time.Time {
				nowMu.Lock()
				defer nowMu.Unlock()
				return curNow
			},
			ClientFactory: func(string) (ColabClient, error) {
				return nil, errors.New("proxy refresh unavailable")
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-refresh-fail",
			Token:          "tok-before-refresh",
			URL:            "https://refresh-fail.invalid",
			TokenExpiresAt: &exp,
		}, true)

		_, err := p.RPC(context.Background(), "POST", "comfyui/run", map[string]any{"workflow_id": "wf1"}, "ep-refresh-fail", 30*time.Second)
		if err == nil || !strings.Contains(err.Error(), "proxy refresh unavailable") {
			t.Fatalf("expected proxy refresh error, got %v", err)
		}
		contents.mu.Lock()
		uploads := append([]uploadRecord(nil), contents.uploads...)
		deletes := append([]string(nil), contents.deletes...)
		contents.mu.Unlock()
		if len(uploads) != 1 || len(deletes) != 1 || uploads[0].remotePath != deletes[0] {
			t.Fatalf("expected ephemeral RPC file cleaned up on proxy refresh failure: uploads=%#v deletes=%#v", uploads, deletes)
		}
		if deleteToken != "tok-before-refresh" {
			t.Fatalf("expected cleanup fallback to prevSession token %q, got %q", "tok-before-refresh", deleteToken)
		}
		if !deleteUnlocked.Load() {
			t.Fatal("expected DeleteFile to run outside slot.mu")
		}
	})
}

func TestRPCMissingMarkerCleansUpUploadedFileAndClampsContextDeadline(t *testing.T) {
	t.Run("IdempotentMissingMarkerCleansUpOnRetryAndFinalError", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		contents := &fakeContents{}
		var execCalls atomic.Int32

		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:    vault,
			Contents: contents,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					execCalls.Add(1)
					return []KernelOutput{{Text: "unexpected stdout without marker\n"}}, nil
				}
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-missing-marker",
			Token:          "tok-valid",
			URL:            "https://missing-marker.invalid",
			TokenExpiresAt: &exp,
		}, true)

		// Idempotent POST configure retries once and must clean up BOTH uploaded request files.
		_, err := p.RPC(context.Background(), "POST", "configure", map[string]any{"idle_minutes": 30}, "ep-missing-marker", 30*time.Second)
		if err == nil || !strings.Contains(err.Error(), "Colab 控制连接没有返回结果") {
			t.Fatalf("expected missing marker error, got %v", err)
		}
		if execCalls.Load() != 2 {
			t.Fatalf("expected 2 attempts for idempotent missing marker, got %d", execCalls.Load())
		}
		contents.mu.Lock()
		uploads := append([]uploadRecord(nil), contents.uploads...)
		deletes := append([]string(nil), contents.deletes...)
		contents.mu.Unlock()
		if len(uploads) != 2 || len(deletes) != 2 {
			t.Fatalf("expected 2 uploads and 2 deletes, got uploads=%d deletes=%d (%v)", len(uploads), len(deletes), deletes)
		}
		if uploads[0].remotePath != deletes[0] || uploads[1].remotePath != deletes[1] {
			t.Fatalf("deleted paths %v do not match uploaded paths [%s, %s]", deletes, uploads[0].remotePath, uploads[1].remotePath)
		}
	})

	t.Run("NonIdempotentMissingMarkerCleansUpUploadedFile", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		contents := &fakeContents{}

		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:    vault,
			Contents: contents,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					return []KernelOutput{{Text: "no marker\n"}}, nil
				}
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-missing-nonidemp",
			Token:          "tok-valid",
			URL:            "https://missing-nonidemp.invalid",
			TokenExpiresAt: &exp,
		}, true)

		_, err := p.RPC(context.Background(), "POST", "models/m1/load", map[string]any{"id": "m1"}, "ep-missing-nonidemp", 30*time.Second)
		if err == nil || !strings.Contains(err.Error(), "Colab 控制连接没有返回结果") {
			t.Fatalf("expected missing marker error, got %v", err)
		}
		contents.mu.Lock()
		uploads := append([]uploadRecord(nil), contents.uploads...)
		deletes := append([]string(nil), contents.deletes...)
		contents.mu.Unlock()
		if len(uploads) != 1 || len(deletes) != 1 || uploads[0].remotePath != deletes[0] {
			t.Fatalf("expected uploaded file cleaned up on missing marker: uploads=%#v deletes=%#v", uploads, deletes)
		}
	})

	t.Run("ClampsPhase1UploadAndPhase2ExecTimeoutsToContextDeadline", func(t *testing.T) {
		vault := newMemoryVault()
		exp := time.Now().Add(time.Hour)
		var seenUploadTimeout time.Duration
		var seenExecTimeout time.Duration
		var seenCode string

		contents := &fakeContents{
			uploadFn: func(ctx context.Context, baseURL, proxyToken, remotePath string, data []byte, timeout time.Duration) error {
				seenUploadTimeout = timeout
				time.Sleep(150 * time.Millisecond)
				return nil
			},
		}
		p := NewColabOneProvider(ColabOneProviderConfig{
			Vault:    vault,
			Contents: contents,
			KernelFactory: func(opts KernelOptions) KernelClient {
				fk := newFakeKernel(opts)
				fk.executeFn = func(ctx context.Context, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					seenExecTimeout = eo.Timeout
					seenCode = code
					return []KernelOutput{{Text: `COLABONE_RPC={"ok":true,"value":{"clamped":true}}` + "\n"}}, nil
				}
				return fk
			},
		})
		_ = p.SaveSession(&ColabOneSessionState{
			Endpoint:       "ep-clamp",
			Token:          "tok-valid",
			URL:            "https://clamp.invalid",
			TokenExpiresAt: &exp,
		}, true)

		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		val, err := p.RPC(ctx, "POST", "models/m1/load", map[string]any{"id": "m1"}, "ep-clamp", 120*time.Second)
		if err != nil {
			t.Fatalf("RPC failed: %v", err)
		}
		if val.(map[string]any)["clamped"] != true {
			t.Fatalf("unexpected result: %#v", val)
		}
		if seenUploadTimeout <= 0 || seenUploadTimeout > 4*time.Second {
			t.Fatalf("Phase 1 upload timeout was not clamped to ctx deadline: got %v", seenUploadTimeout)
		}
		if seenExecTimeout <= 0 || seenExecTimeout > 4*time.Second {
			t.Fatalf("Phase 2 exec timeout was not clamped to ctx deadline: got %v", seenExecTimeout)
		}
		if seenExecTimeout >= seenUploadTimeout {
			t.Fatalf("expected Phase 2 remaining budget (%v) < Phase 1 budget (%v)", seenExecTimeout, seenUploadTimeout)
		}
		if strings.Contains(seenCode, "timeout=110") {
			t.Fatalf("remote Python HTTP timeout was not clamped against ctx deadline: %s", seenCode)
		}
	})
}

func TestProviderReadyWithNilContextDoesNotPanic(t *testing.T) {
	vault := newMemoryVault()
	p := NewColabOneProvider(ColabOneProviderConfig{Vault: vault})
	cancelCh := make(chan struct{})
	close(cancelCh)
	// Ready with nil context must default to context.Background() and handle cancellation without panicking.
	if err := p.Ready(nil, &ColabOneSessionState{URL: "https://example.invalid", Token: "tok"}, nil, cancelCh); err == nil {
		t.Fatal("expected cancelled Ready(nil, ...) to return an error")
	}
	if err := p.Ready(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("expected Ready with nil session to return an error without panicking")
	}
}
