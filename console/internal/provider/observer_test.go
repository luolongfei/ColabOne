package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/luolongfei/ColabOne/console/internal/jupyterws"
)

// ---------------------------------------------------------------------------
// Self-contained Offline Test Doubles for Observer Concurrency Tests
// ---------------------------------------------------------------------------

type observerTestVault struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newObserverTestVault() *observerTestVault {
	return &observerTestVault{data: make(map[string][]byte)}
}

func (v *observerTestVault) GetJSON(key string, dest any) (bool, error) {
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

func (v *observerTestVault) Set(key string, val any) error {
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.data[key] = raw
	v.mu.Unlock()
	return nil
}

func (v *observerTestVault) Delete(key string) error {
	v.mu.Lock()
	delete(v.data, key)
	v.mu.Unlock()
	return nil
}

func (v *observerTestVault) Has(key string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.data[key]
	return ok
}

type observerTestClient struct {
	listFn func(ctx context.Context) ([]ListedAssignment, error)
}

func (c *observerTestClient) ColabDomain() string { return "https://colab.research.google.com" }
func (c *observerTestClient) GetConsumptionUserInfo(ctx context.Context) (*ConsumptionInfo, error) {
	return &ConsumptionInfo{}, nil
}
func (c *observerTestClient) ListAssignments(ctx context.Context) ([]ListedAssignment, error) {
	if c.listFn != nil {
		return c.listFn(ctx)
	}
	return nil, nil
}
func (c *observerTestClient) GetAssignment(ctx context.Context, nbh, variant, accel, shape string) (*Assignment, string, error) {
	return nil, "", errors.New("unexpected GetAssignment")
}
func (c *observerTestClient) PostAssignment(ctx context.Context, nbh, token, variant, accel, shape string) (*Assignment, error) {
	return nil, errors.New("unexpected PostAssignment")
}
func (c *observerTestClient) Unassign(ctx context.Context, endpoint string) error  { return nil }
func (c *observerTestClient) KeepAlive(ctx context.Context, endpoint string) error { return nil }
func (c *observerTestClient) DoAuthorized(ctx context.Context, req *http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected DoAuthorized")
}

// observerTestKernel tracks kernel creation, execution, and Stop(shutdownKernel)
// calls so tests can deterministically assert zero orphaned remote kernels.
type observerTestKernel struct {
	mu             sync.Mutex
	opts           KernelOptions
	startCount     int
	execCount      int
	stopCalls      []bool
	kernelID       string
	sessionID      string
	kernelAlive    bool
	aborted        atomic.Bool
	abortCh        chan struct{}
	abortCloseOnce sync.Once

	startFn   func(ctx context.Context, k *observerTestKernel) error
	executeFn func(ctx context.Context, k *observerTestKernel, code string, opts ExecuteOptions) ([]KernelOutput, error)
	stopFn    func(ctx context.Context, k *observerTestKernel, shutdownKernel bool) error
}

func newObserverTestKernel(opts KernelOptions) *observerTestKernel {
	return &observerTestKernel{
		opts:        opts,
		kernelID:    opts.KernelID,
		sessionID:   opts.SessionID,
		kernelAlive: opts.KernelID != "",
		abortCh:     make(chan struct{}),
	}
}

func (k *observerTestKernel) Start(ctx context.Context) error {
	k.mu.Lock()
	k.startCount++
	fn := k.startFn
	k.mu.Unlock()
	if fn != nil {
		return fn(ctx, k)
	}
	k.markKernelLive("kid-auto", "sid-auto")
	return nil
}

func (k *observerTestKernel) markKernelLive(kid, sid string) {
	k.mu.Lock()
	if kid != "" {
		k.kernelID = kid
	}
	if sid != "" {
		k.sessionID = sid
	}
	k.kernelAlive = true
	onKernel := k.opts.OnKernelStarted
	onSession := k.opts.OnSessionStarted
	actualKID := k.kernelID
	actualSID := k.sessionID
	k.mu.Unlock()

	if onKernel != nil && actualKID != "" {
		onKernel(actualKID)
	}
	if onSession != nil && actualSID != "" {
		onSession(actualSID)
	}
}

func (k *observerTestKernel) ExecuteCode(ctx context.Context, code string, opts ExecuteOptions) ([]KernelOutput, error) {
	k.mu.Lock()
	k.execCount++
	fn := k.executeFn
	k.mu.Unlock()
	if fn != nil {
		return fn(ctx, k, code, opts)
	}
	return observerSampleOutputs(), nil
}

func (k *observerTestKernel) Abort() {
	k.aborted.Store(true)
	k.abortCloseOnce.Do(func() {
		if k.abortCh != nil {
			close(k.abortCh)
		}
	})
}

func (k *observerTestKernel) Stop(ctx context.Context, shutdownKernel bool) error {
	k.mu.Lock()
	k.stopCalls = append(k.stopCalls, shutdownKernel)
	if shutdownKernel {
		k.kernelAlive = false
	}
	fn := k.stopFn
	k.mu.Unlock()
	if fn != nil {
		return fn(ctx, k, shutdownKernel)
	}
	return nil
}

func (k *observerTestKernel) KernelID() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.kernelID
}

func (k *observerTestKernel) SessionID() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.sessionID
}

func (k *observerTestKernel) stats() (starts, execs int, stops []bool, alive bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	cp := make([]bool, len(k.stopCalls))
	copy(cp, k.stopCalls)
	return k.startCount, k.execCount, cp, k.kernelAlive
}

func (k *observerTestKernel) hasStopTrue() bool {
	_, _, stops, _ := k.stats()
	for _, s := range stops {
		if s {
			return true
		}
	}
	return false
}

func observerSampleOutputs() []KernelOutput {
	return []KernelOutput{{
		OutputType: "stream",
		Name:       "stdout",
		Text:       "\nCOLABONE_OBS=" + `{"source":"remote_readonly","resources":{"cpu":{"cores":4,"percent":12.5},"memory":{"total_bytes":16000000000,"percent":20.0},"disk":{"total_bytes":100000000000,"free_bytes":80000000000,"percent":20.0}},"disk_free":80000000000}` + "\n",
	}}
}

func waitChan(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func assertObserverNil(t *testing.T, p *ColabOneProvider) {
	t.Helper()
	p.observerMu.Lock()
	obs := p.observer
	p.observerMu.Unlock()
	if obs != nil {
		t.Fatalf("expected p.observer to be nil, got endpoint=%q", obs.endpoint)
	}
}

func newObserverTestProvider(assignments []ListedAssignment, factory KernelFactory) *ColabOneProvider {
	client := &observerTestClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			cp := make([]ListedAssignment, len(assignments))
			copy(cp, assignments)
			return cp, nil
		},
	}
	return NewColabOneProvider(ColabOneProviderConfig{
		Vault:         newObserverTestVault(),
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		KernelFactory: factory,
	})
}

// ---------------------------------------------------------------------------
// 1. CloseObserver while switching endpoints stops old runtime (original bug)
// ---------------------------------------------------------------------------

func TestObserverCloseBeforeStartDuringOldRuntimeStop(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	assignments := []ListedAssignment{
		{Endpoint: "ep-1", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep1.invalid", Token: "tok-1", ExpiresAt: exp}},
		{Endpoint: "ep-2", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep2.invalid", Token: "tok-2", ExpiresAt: exp}},
	}

	oldStopEntered := make(chan struct{})
	releaseOldStop := make(chan struct{})
	var stopBlockOnce sync.Once

	var mu sync.Mutex
	var created []*observerTestKernel

	p := newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
		fk := newObserverTestKernel(opts)
		mu.Lock()
		idx := len(created)
		created = append(created, fk)
		mu.Unlock()
		if idx == 0 {
			fk.stopFn = func(ctx context.Context, k *observerTestKernel, shutdown bool) error {
				stopBlockOnce.Do(func() {
					close(oldStopEntered)
					<-releaseOldStop
				})
				return nil
			}
		}
		return fk
	})

	// Seed live observer on ep-1.
	if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-1"); err != nil {
		t.Fatalf("initial observe ep-1 failed: %v", err)
	}

	// Switch to ep-2: this will block inside old runtime's Stop(true).
	errCh := make(chan error, 1)
	go func() {
		_, err := p.ObserveExternalTelemetry(context.Background(), "ep-2")
		errCh <- err
	}()

	waitChan(t, oldStopEntered, "oldStopEntered")

	// Concurrently call CloseObserver while old runtime's Stop(true) is in flight.
	p.CloseObserver("", true)
	close(releaseOldStop)

	var observeErr error
	select {
	case observeErr = <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("ObserveExternalTelemetry(ep-2) did not return")
	}

	if !errors.Is(observeErr, ErrExternalObserverClosed) {
		t.Fatalf("expected ErrExternalObserverClosed, got %v", observeErr)
	}

	mu.Lock()
	count := len(created)
	rt1 := created[0]
	mu.Unlock()

	if count != 1 {
		t.Fatalf("expected no second runtime to be created after CloseObserver during toStop, got %d runtimes", count)
	}
	if !rt1.hasStopTrue() {
		t.Fatalf("expected old runtime Stop(true) to be called")
	}
	_, _, _, alive1 := rt1.stats()
	if alive1 {
		t.Fatalf("expected old kernel to be shut down, still alive")
	}
	assertObserverNil(t, p)
}

// ---------------------------------------------------------------------------
// 2. CloseObserver during token-rotation toStop(false) shuts down preserved kernel
// ---------------------------------------------------------------------------

func TestObserverCloseDuringTokenRotationStopShutsDownPreservedKernel(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	assignments := []ListedAssignment{
		{Endpoint: "ep-1", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep1.invalid", Token: "tok-1", ExpiresAt: exp}},
	}

	rotStopEntered := make(chan struct{})
	releaseRotStop := make(chan struct{})
	var rotBlockOnce sync.Once

	var mu sync.Mutex
	var created []*observerTestKernel

	p := newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
		fk := newObserverTestKernel(opts)
		mu.Lock()
		idx := len(created)
		created = append(created, fk)
		mu.Unlock()
		if idx == 0 {
			fk.startFn = func(ctx context.Context, k *observerTestKernel) error {
				k.markKernelLive("kid-rot-1", "sid-rot-1")
				return nil
			}
			fk.stopFn = func(ctx context.Context, k *observerTestKernel, shutdown bool) error {
				if !shutdown {
					rotBlockOnce.Do(func() {
						close(rotStopEntered)
						<-releaseRotStop
					})
				}
				return nil
			}
		}
		return fk
	})

	if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-1"); err != nil {
		t.Fatalf("initial observe ep-1 failed: %v", err)
	}

	// Rotate proxy token in cache to trigger reconnect path (toStop with shutdown=false).
	p.proxyMu.Lock()
	rot := p.externalProxies["ep-1"]
	rot.token = "tok-1-rotated"
	p.externalProxies["ep-1"] = rot
	p.proxyMu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		_, err := p.ObserveExternalTelemetry(context.Background(), "ep-1")
		errCh <- err
	}()

	waitChan(t, rotStopEntered, "rotStopEntered")
	p.CloseObserver("", true)
	close(releaseRotStop)

	var observeErr error
	select {
	case observeErr = <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("ObserveExternalTelemetry(ep-1 rotated) did not return")
	}

	if !errors.Is(observeErr, ErrExternalObserverClosed) {
		t.Fatalf("expected ErrExternalObserverClosed, got %v", observeErr)
	}

	mu.Lock()
	count := len(created)
	rt1 := created[0]
	mu.Unlock()

	if count != 1 {
		t.Fatalf("expected no replacement runtime to be created after CloseObserver, got %d", count)
	}
	if !rt1.hasStopTrue() {
		_, _, stops, _ := rt1.stats()
		t.Fatalf("expected preserved kernel to be shut down via Stop(true), got stops=%v", stops)
	}
	_, _, _, alive := rt1.stats()
	if alive {
		t.Fatalf("preserved kernel was orphaned (still alive)")
	}
	assertObserverNil(t, p)
}

// ---------------------------------------------------------------------------
// 3. CloseObserver deterministically triggered right BEFORE rt.Start(ctx)
// ---------------------------------------------------------------------------

func TestObserverCloseRightBeforeStart(t *testing.T) {
	cases := []struct {
		name           string
		closeEndpoint  string
		shutdownKernel bool
	}{
		{name: "close_all_shutdown_true", closeEndpoint: "", shutdownKernel: true},
		{name: "close_endpoint_shutdown_false", closeEndpoint: "ep-1", shutdownKernel: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp := time.Now().Add(time.Hour)
			assignments := []ListedAssignment{
				{Endpoint: "ep-1", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep1.invalid", Token: "tok-1", ExpiresAt: exp}},
			}

			var fk *observerTestKernel
			p := newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
				fk = newObserverTestKernel(opts)
				return fk
			})

			beforeStartReached := make(chan struct{})
			closeDone := make(chan struct{})
			observerBeforeStartHooks.Store(p, func() {
				close(beforeStartReached)
				<-closeDone
			})
			t.Cleanup(func() { observerBeforeStartHooks.Delete(p) })

			errCh := make(chan error, 1)
			go func() {
				_, err := p.ObserveExternalTelemetry(context.Background(), "ep-1")
				errCh <- err
			}()

			waitChan(t, beforeStartReached, "beforeStartReached")
			p.CloseObserver(tc.closeEndpoint, tc.shutdownKernel)
			close(closeDone)

			var observeErr error
			select {
			case observeErr = <-errCh:
			case <-time.After(5 * time.Second):
				t.Fatal("ObserveExternalTelemetry did not return")
			}

			if !errors.Is(observeErr, ErrExternalObserverClosed) {
				t.Fatalf("expected ErrExternalObserverClosed, got %v", observeErr)
			}
			if fk == nil {
				t.Fatal("expected kernel to be created")
			}
			starts, execs, stops, alive := fk.stats()
			if starts != 0 {
				t.Fatalf("rt.Start must NOT be called when slot is already invalidated; startCount=%d", starts)
			}
			if execs != 0 {
				t.Fatalf("rt.ExecuteCode must NOT be called; execCount=%d", execs)
			}
			if !fk.hasStopTrue() {
				t.Fatalf("expected rt.Stop(true) to be called, stops=%v", stops)
			}
			if alive {
				t.Fatalf("expected no live/orphaned kernel")
			}
			assertObserverNil(t, p)
		})
	}
}

// ---------------------------------------------------------------------------
// 4. CloseObserver triggered inside KernelFactory before slot is published
// ---------------------------------------------------------------------------

func TestObserverCloseDuringFactoryBeforePublish(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	assignments := []ListedAssignment{
		{Endpoint: "ep-1", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep1.invalid", Token: "tok-1", ExpiresAt: exp}},
	}

	var p *ColabOneProvider
	var fk *observerTestKernel
	p = newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
		fk = newObserverTestKernel(opts)
		// Simulate CloseObserver arriving while factory is constructing runtime.
		p.CloseObserver("", true)
		return fk
	})

	_, err := p.ObserveExternalTelemetry(context.Background(), "ep-1")
	if !errors.Is(err, ErrExternalObserverClosed) {
		t.Fatalf("expected ErrExternalObserverClosed, got %v", err)
	}
	if fk == nil {
		t.Fatal("expected factory kernel")
	}
	starts, execs, stops, alive := fk.stats()
	if starts != 0 || execs != 0 {
		t.Fatalf("expected zero Start/ExecuteCode calls, got starts=%d execs=%d", starts, execs)
	}
	if !fk.hasStopTrue() {
		t.Fatalf("expected rt.Stop(true) cleanup, got stops=%v", stops)
	}
	if alive {
		t.Fatalf("expected no live kernel")
	}
	assertObserverNil(t, p)
}

// ---------------------------------------------------------------------------
// 5. CloseObserver deterministically triggered mid-rt.Start(ctx)
// ---------------------------------------------------------------------------

func TestObserverCloseMidStart(t *testing.T) {
	cases := []struct {
		name           string
		closeEndpoint  string
		shutdownKernel bool
		startErr       error
	}{
		{
			name:           "start_succeeds_after_close_shutdown_false",
			closeEndpoint:  "ep-1",
			shutdownKernel: false,
			startErr:       nil,
		},
		{
			name:           "start_succeeds_after_close_shutdown_true",
			closeEndpoint:  "",
			shutdownKernel: true,
			startErr:       nil,
		},
		{
			name:           "start_returns_error_after_close",
			closeEndpoint:  "",
			shutdownKernel: true,
			startErr:       errors.New("aborted mid-dial"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp := time.Now().Add(time.Hour)
			assignments := []ListedAssignment{
				{Endpoint: "ep-1", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep1.invalid", Token: "tok-1", ExpiresAt: exp}},
			}

			startEntered := make(chan struct{})
			closeDone := make(chan struct{})

			var fk *observerTestKernel
			p := newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
				fk = newObserverTestKernel(opts)
				fk.startFn = func(ctx context.Context, k *observerTestKernel) error {
					close(startEntered)
					<-closeDone
					if tc.startErr != nil {
						// Even if Start fails after creating a kernel, Stop(true) must tear it down.
						k.markKernelLive("kid-mid-start-err", "sid-mid-start-err")
						return tc.startErr
					}
					// Simulate the exact orphan bug: CloseObserver already ran Stop on the unstarted rt,
					// and Start then completes creating a brand-new remote kernel.
					k.markKernelLive("kid-mid-start-ok", "sid-mid-start-ok")
					return nil
				}
				return fk
			})

			errCh := make(chan error, 1)
			go func() {
				_, err := p.ObserveExternalTelemetry(context.Background(), "ep-1")
				errCh <- err
			}()

			waitChan(t, startEntered, "startEntered")
			p.CloseObserver(tc.closeEndpoint, tc.shutdownKernel)
			close(closeDone)

			var observeErr error
			select {
			case observeErr = <-errCh:
			case <-time.After(5 * time.Second):
				t.Fatal("ObserveExternalTelemetry did not return")
			}

			if !errors.Is(observeErr, ErrExternalObserverClosed) {
				t.Fatalf("expected ErrExternalObserverClosed, got %v", observeErr)
			}
			starts, execs, stops, alive := fk.stats()
			if starts != 1 {
				t.Fatalf("expected 1 Start call, got %d", starts)
			}
			if execs != 0 {
				t.Fatalf("ExecuteCode must NOT be called after mid-Start close; execs=%d", execs)
			}
			if !fk.hasStopTrue() {
				t.Fatalf("expected rt.Stop(true) to tear down remote kernel, got stops=%v", stops)
			}
			if alive {
				t.Fatalf("ORPHANED KERNEL DETECTED: kernel remained alive after mid-Start CloseObserver (stops=%v)", stops)
			}
			assertObserverNil(t, p)
		})
	}
}

// ---------------------------------------------------------------------------
// 6. CloseObserver deterministically triggered mid-rt.ExecuteCode(...)
// ---------------------------------------------------------------------------

func TestObserverCloseMidExecuteCode(t *testing.T) {
	cases := []struct {
		name           string
		reuseSlot      bool
		closeEndpoint  string
		shutdownKernel bool
		execErr        error
	}{
		{
			name:           "fresh_slot_exec_returns_sample_after_close_shutdown_false",
			reuseSlot:      false,
			closeEndpoint:  "ep-1",
			shutdownKernel: false,
			execErr:        nil,
		},
		{
			name:           "fresh_slot_exec_returns_error_after_close",
			reuseSlot:      false,
			closeEndpoint:  "",
			shutdownKernel: true,
			execErr:        errors.New("ws closed"),
		},
		{
			name:           "reused_slot_exec_returns_sample_after_close_shutdown_false",
			reuseSlot:      true,
			closeEndpoint:  "ep-1",
			shutdownKernel: false,
			execErr:        nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp := time.Now().Add(time.Hour)
			assignments := []ListedAssignment{
				{Endpoint: "ep-1", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep1.invalid", Token: "tok-1", ExpiresAt: exp}},
			}

			execEntered := make(chan struct{})
			closeDone := make(chan struct{})
			var blockExec atomic.Bool
			if !tc.reuseSlot {
				blockExec.Store(true)
			}

			var fk *observerTestKernel
			p := newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
				fk = newObserverTestKernel(opts)
				fk.executeFn = func(ctx context.Context, k *observerTestKernel, code string, eo ExecuteOptions) ([]KernelOutput, error) {
					if blockExec.Load() {
						close(execEntered)
						<-closeDone
						if tc.execErr != nil {
							return nil, tc.execErr
						}
					}
					return observerSampleOutputs(), nil
				}
				return fk
			})

			if tc.reuseSlot {
				// First call warms up and caches the live observer slot.
				if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-1"); err != nil {
					t.Fatalf("warmup observe failed: %v", err)
				}
				blockExec.Store(true)
			}

			errCh := make(chan error, 1)
			go func() {
				_, err := p.ObserveExternalTelemetry(context.Background(), "ep-1")
				errCh <- err
			}()

			waitChan(t, execEntered, "execEntered")
			p.CloseObserver(tc.closeEndpoint, tc.shutdownKernel)
			close(closeDone)

			var observeErr error
			select {
			case observeErr = <-errCh:
			case <-time.After(5 * time.Second):
				t.Fatal("ObserveExternalTelemetry did not return")
			}

			if !errors.Is(observeErr, ErrExternalObserverClosed) {
				t.Fatalf("expected ErrExternalObserverClosed, got %v", observeErr)
			}
			_, _, stops, alive := fk.stats()
			if !fk.hasStopTrue() {
				t.Fatalf("expected rt.Stop(true) to tear down remote kernel, got stops=%v", stops)
			}
			if alive {
				t.Fatalf("ORPHANED KERNEL DETECTED after mid-ExecuteCode CloseObserver (stops=%v)", stops)
			}
			assertObserverNil(t, p)
		})
	}
}

// ---------------------------------------------------------------------------
// 7. End-to-End Offline Regression with Real jupyterws.ColabOneKernelClient
// ---------------------------------------------------------------------------

type observerRealKernelAdapter struct {
	c           *jupyterws.ColabOneKernelClient
	beforeStart func()
	onStart     func(startErr error)
}

func (a *observerRealKernelAdapter) Start(ctx context.Context) error {
	if a.beforeStart != nil {
		a.beforeStart()
	}
	err := a.c.Start(ctx)
	if a.onStart != nil {
		a.onStart(err)
	}
	return err
}

func (a *observerRealKernelAdapter) ExecuteCode(ctx context.Context, code string, opts ExecuteOptions) ([]KernelOutput, error) {
	outs, err := a.c.ExecuteCode(ctx, code, jupyterws.ColabOneExecuteOptions{
		Timeout:    opts.Timeout,
		AllowStdin: opts.AllowStdin,
	})
	if err != nil {
		return nil, err
	}
	res := make([]KernelOutput, 0, len(outs))
	for _, o := range outs {
		res = append(res, KernelOutput{
			OutputType: o.OutputType,
			Name:       o.Name,
			Text:       o.Text,
			EName:      o.Ename,
			EValue:     o.Evalue,
		})
	}
	return res, nil
}

func (a *observerRealKernelAdapter) Abort() { a.c.Abort() }

func (a *observerRealKernelAdapter) Stop(ctx context.Context, shutdownKernel bool) error {
	return a.c.Stop(ctx, shutdownKernel)
}

func (a *observerRealKernelAdapter) KernelID() string  { return a.c.KernelID() }
func (a *observerRealKernelAdapter) SessionID() string { return a.c.SessionID() }

func TestObserverRealKernelClientCloseMidStartNoOrphan(t *testing.T) {
	cases := []struct {
		name           string
		closeBeforeCSt bool // trigger CloseObserver inside Start() BEFORE c.Start() runs (exact unstarted-Stop race)
	}{
		{name: "close_just_after_real_start", closeBeforeCSt: false},
		{name: "close_on_unstarted_real_client_before_dial", closeBeforeCSt: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var createdCount atomic.Int32
			var deletedCount atomic.Int32
			var wsWG sync.WaitGroup

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/kernels":
					id := fmt.Sprintf("kid-real-%d", createdCount.Add(1))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
					return

				case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/kernels/"):
					deletedCount.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return

				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels"):
					wsWG.Add(1)
					defer wsWG.Done()
					conn, err := websocket.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					for {
						if _, _, err := conn.Read(r.Context()); err != nil {
							return
						}
					}

				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			exp := time.Now().Add(time.Hour)
			assignments := []ListedAssignment{
				{Endpoint: "ep-real", RuntimeProxyInfo: &RuntimeProxyInfo{URL: srv.URL, Token: "tok-real", ExpiresAt: exp}},
			}

			var p *ColabOneProvider
			var realClient *jupyterws.ColabOneKernelClient
			p = newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
				realClient = jupyterws.NewColabOneKernelClient(jupyterws.ColabOneKernelOptions{
					ServerURL:           opts.ServerURL,
					ProxyToken:          opts.ProxyToken,
					KernelID:            opts.KernelID,
					SessionID:           opts.SessionID,
					ShutdownOnWSTimeout: opts.ShutdownOnWSTimeout,
					OnKernelStarted:     opts.OnKernelStarted,
					OnSessionStarted:    opts.OnSessionStarted,
					PingInterval:        50 * time.Millisecond,
					ConnectTimeout:      5 * time.Second,
					HTTPClient:          srv.Client(),
				})
				adapter := &observerRealKernelAdapter{c: realClient}
				if tc.closeBeforeCSt {
					// Reproduces the exact low-level race where CloseObserver runs Stop() on an
					// unstarted ColabOneKernelClient (which finishes immediately with aborted=false),
					// and then c.Start() proceeds to create a real kernel + readerLoop/pingLoop.
					// ObserveExternalTelemetry's post-Start invalidation check must immediately
					// call Stop(true) and delete the kernel so nothing is orphaned.
					adapter.beforeStart = func() {
						p.CloseObserver("", true)
					}
				} else {
					adapter.onStart = func(startErr error) {
						if startErr == nil {
							p.CloseObserver("ep-real", false)
						}
					}
				}
				return adapter
			})

			_, err := p.ObserveExternalTelemetry(context.Background(), "ep-real")
			if !errors.Is(err, ErrExternalObserverClosed) {
				t.Fatalf("expected ErrExternalObserverClosed, got %v", err)
			}
			if createdCount.Load() != 1 {
				t.Fatalf("expected 1 remote kernel created, got %d", createdCount.Load())
			}
			if deletedCount.Load() != 1 {
				t.Fatalf("expected remote kernel DELETE to be called once, got %d", deletedCount.Load())
			}
			if realClient == nil || realClient.IsConnected() {
				t.Fatalf("expected real kernel WebSocket client to be disconnected")
			}
			assertObserverNil(t, p)

			// Confirm server-side WebSocket handler exited promptly (client WS closed cleanly).
			wsDone := make(chan struct{})
			go func() {
				wsWG.Wait()
				close(wsDone)
			}()
			waitChan(t, wsDone, "server websocket handler exit")
		})
	}
}

// ---------------------------------------------------------------------------
// 8. Unrelated CloseObserver(ep-C) must NOT invalidate in-flight ep-B transition
// ---------------------------------------------------------------------------

func TestObserverUnrelatedCloseDoesNotInvalidateTransition(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	assignments := []ListedAssignment{
		{Endpoint: "ep-A", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://epa.invalid", Token: "tok-A", ExpiresAt: exp}},
		{Endpoint: "ep-B", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://epb.invalid", Token: "tok-B", ExpiresAt: exp}},
		{Endpoint: "ep-C", RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://epc.invalid", Token: "tok-C", ExpiresAt: exp}},
	}

	assertPendingEmpty := func(t *testing.T, p *ColabOneProvider) {
		t.Helper()
		p.observerMu.Lock()
		pending := p.observerPendingEndpoint
		p.observerMu.Unlock()
		if pending != "" {
			t.Fatalf("expected observerPendingEndpoint to be empty, got %q", pending)
		}
	}

	t.Run("unrelated_close_during_endpoint_switch_toStop", func(t *testing.T) {
		oldStopEntered := make(chan struct{})
		releaseOldStop := make(chan struct{})
		var stopOnce sync.Once

		var mu sync.Mutex
		var created []*observerTestKernel

		p := newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
			fk := newObserverTestKernel(opts)
			mu.Lock()
			idx := len(created)
			created = append(created, fk)
			mu.Unlock()
			if idx == 0 {
				fk.stopFn = func(ctx context.Context, k *observerTestKernel, shutdown bool) error {
					stopOnce.Do(func() {
						close(oldStopEntered)
						<-releaseOldStop
					})
					return nil
				}
			}
			return fk
		})

		if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-A"); err != nil {
			t.Fatalf("initial observe ep-A failed: %v", err)
		}

		resCh := make(chan error, 1)
		go func() {
			_, err := p.ObserveExternalTelemetry(context.Background(), "ep-B")
			resCh <- err
		}()

		waitChan(t, oldStopEntered, "oldStopEntered")

		// Unrelated ep-C (and already-stopping ep-A) must NOT invalidate ep-B's transition.
		p.CloseObserver("ep-C", true)
		p.CloseObserver("ep-A", false)
		close(releaseOldStop)

		select {
		case err := <-resCh:
			if err != nil {
				t.Fatalf("expected ObserveExternalTelemetry(ep-B) to succeed despite unrelated CloseObserver(ep-C), got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("ObserveExternalTelemetry(ep-B) did not return")
		}

		p.observerMu.Lock()
		obs := p.observer
		p.observerMu.Unlock()
		if obs == nil || obs.endpoint != "ep-B" {
			t.Fatalf("expected published observer on ep-B, got %+v", obs)
		}
		assertPendingEmpty(t, p)
	})

	t.Run("unrelated_close_during_factory_before_publish", func(t *testing.T) {
		var p *ColabOneProvider
		var factoryCalls atomic.Int32
		p = newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
			fk := newObserverTestKernel(opts)
			if factoryCalls.Add(1) == 2 {
				// Simulate unrelated CloseObserver("ep-C") arriving while ep-B's runtime is constructed.
				p.CloseObserver("ep-C", true)
			}
			return fk
		})

		if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-A"); err != nil {
			t.Fatalf("initial observe ep-A failed: %v", err)
		}
		if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-B"); err != nil {
			t.Fatalf("ObserveExternalTelemetry(ep-B) falsely invalidated by CloseObserver(ep-C): %v", err)
		}
		p.observerMu.Lock()
		obs := p.observer
		p.observerMu.Unlock()
		if obs == nil || obs.endpoint != "ep-B" {
			t.Fatalf("expected published observer on ep-B, got %+v", obs)
		}
		assertPendingEmpty(t, p)
	})

	t.Run("unrelated_close_during_token_rotation_preserves_kernel", func(t *testing.T) {
		rotStopEntered := make(chan struct{})
		releaseRotStop := make(chan struct{})
		var rotOnce sync.Once

		var mu sync.Mutex
		var created []*observerTestKernel

		p := newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
			fk := newObserverTestKernel(opts)
			mu.Lock()
			idx := len(created)
			created = append(created, fk)
			mu.Unlock()
			if idx == 0 {
				fk.startFn = func(ctx context.Context, k *observerTestKernel) error {
					k.markKernelLive("kid-keep-1", "sid-keep-1")
					return nil
				}
				fk.stopFn = func(ctx context.Context, k *observerTestKernel, shutdown bool) error {
					if !shutdown {
						rotOnce.Do(func() {
							close(rotStopEntered)
							<-releaseRotStop
						})
					}
					return nil
				}
			}
			return fk
		})

		if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-A"); err != nil {
			t.Fatalf("initial observe ep-A failed: %v", err)
		}

		p.proxyMu.Lock()
		rot := p.externalProxies["ep-A"]
		rot.token = "tok-A-rotated"
		p.externalProxies["ep-A"] = rot
		p.proxyMu.Unlock()

		resCh := make(chan error, 1)
		go func() {
			_, err := p.ObserveExternalTelemetry(context.Background(), "ep-A")
			resCh <- err
		}()

		waitChan(t, rotStopEntered, "rotStopEntered")
		p.CloseObserver("ep-C", true)
		close(releaseRotStop)

		select {
		case err := <-resCh:
			if err != nil {
				t.Fatalf("token rotation on ep-A falsely invalidated by CloseObserver(ep-C): %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("ObserveExternalTelemetry(ep-A rotated) did not return")
		}

		mu.Lock()
		if len(created) != 2 {
			mu.Unlock()
			t.Fatalf("expected 2 runtimes (initial + rotated), got %d", len(created))
		}
		rt1, rt2 := created[0], created[1]
		mu.Unlock()

		if rt1.hasStopTrue() {
			t.Fatal("preserved kernel on ep-A must not be shut down by unrelated CloseObserver(ep-C)")
		}
		if rt2.opts.KernelID != "kid-keep-1" {
			t.Fatalf("expected rotated runtime to preserve KernelID=kid-keep-1, got %q", rt2.opts.KernelID)
		}
		assertPendingEmpty(t, p)
	})

	t.Run("matching_pending_endpoint_close_still_invalidates", func(t *testing.T) {
		oldStopEntered := make(chan struct{})
		releaseOldStop := make(chan struct{})
		var stopOnce sync.Once

		var mu sync.Mutex
		var created []*observerTestKernel

		p := newObserverTestProvider(assignments, func(opts KernelOptions) KernelClient {
			fk := newObserverTestKernel(opts)
			mu.Lock()
			idx := len(created)
			created = append(created, fk)
			mu.Unlock()
			if idx == 0 {
				fk.stopFn = func(ctx context.Context, k *observerTestKernel, shutdown bool) error {
					stopOnce.Do(func() {
						close(oldStopEntered)
						<-releaseOldStop
					})
					return nil
				}
			}
			return fk
		})

		if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-A"); err != nil {
			t.Fatalf("initial observe ep-A failed: %v", err)
		}

		resCh := make(chan error, 1)
		go func() {
			_, err := p.ObserveExternalTelemetry(context.Background(), "ep-B")
			resCh <- err
		}()

		waitChan(t, oldStopEntered, "oldStopEntered")
		// Closing the exact pending target endpoint ep-B MUST invalidate the transition.
		p.CloseObserver("ep-B", false)
		close(releaseOldStop)

		select {
		case err := <-resCh:
			if !errors.Is(err, ErrExternalObserverClosed) {
				t.Fatalf("expected ErrExternalObserverClosed when CloseObserver targets pending ep-B, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("ObserveExternalTelemetry(ep-B) did not return")
		}

		mu.Lock()
		count := len(created)
		mu.Unlock()
		if count != 1 {
			t.Fatalf("expected no second runtime created after CloseObserver(ep-B), got %d", count)
		}
		assertObserverNil(t, p)
		assertPendingEmpty(t, p)
	})
}

// ---------------------------------------------------------------------------
// 9. Provider.Close() aborts active observer and rejects post-Close starts
// ---------------------------------------------------------------------------

func TestObserverProviderCloseAbortsActiveAndRejectsPostClose(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	var listCalls atomic.Int32
	client := &observerTestClient{
		listFn: func(ctx context.Context) ([]ListedAssignment, error) {
			listCalls.Add(1)
			return []ListedAssignment{{
				Endpoint:         "ep-1",
				RuntimeProxyInfo: &RuntimeProxyInfo{URL: "https://ep1.invalid", Token: "tok-1", ExpiresAt: exp},
			}}, nil
		},
	}

	execEntered := make(chan struct{})
	var fk *observerTestKernel
	var factoryCalls atomic.Int32
	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault:         newObserverTestVault(),
		ClientFactory: func(string) (ColabClient, error) { return client, nil },
		KernelFactory: func(opts KernelOptions) KernelClient {
			factoryCalls.Add(1)
			fk = newObserverTestKernel(opts)
			fk.executeFn = func(ctx context.Context, k *observerTestKernel, code string, eo ExecuteOptions) ([]KernelOutput, error) {
				close(execEntered)
				select {
				case <-k.abortCh:
					return nil, errors.New("observer aborted")
				case <-time.After(10 * time.Second):
					return nil, errors.New("timed out waiting for observer abort")
				}
			}
			return fk
		},
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := p.ObserveExternalTelemetry(context.Background(), "ep-1")
		errCh <- err
	}()

	waitChan(t, execEntered, "execEntered")

	started := time.Now()
	p.Close()
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Provider.Close() stalled on active observer for %v", elapsed)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrExternalObserverClosed) {
			t.Fatalf("expected ErrExternalObserverClosed, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ObserveExternalTelemetry did not unwind after Provider.Close()")
	}

	if fk == nil || !fk.aborted.Load() {
		t.Fatal("expected active observer kernel Abort() called by Provider.Close()")
	}
	if !fk.hasStopTrue() {
		_, _, stops, _ := fk.stats()
		t.Fatalf("expected active observer kernel Stop(true), got %v", stops)
	}
	assertObserverNil(t, p)

	// Post-Close() ObserveExternalTelemetry must be rejected immediately without network or factory calls.
	listsBefore := listCalls.Load()
	factoriesBefore := factoryCalls.Load()
	if _, err := p.ObserveExternalTelemetry(context.Background(), "ep-1"); !errors.Is(err, ErrExternalObserverClosed) {
		t.Fatalf("post-Close ObserveExternalTelemetry expected ErrExternalObserverClosed, got %v", err)
	}
	if listCalls.Load() != listsBefore || factoryCalls.Load() != factoriesBefore {
		t.Fatalf("post-Close ObserveExternalTelemetry must not invoke ListAssignments or KernelFactory")
	}
	assertObserverNil(t, p)
}
