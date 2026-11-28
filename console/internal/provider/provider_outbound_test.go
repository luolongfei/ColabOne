package provider

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type outboundTestVault struct {
	mu   sync.Mutex
	data map[string]any
}

func (v *outboundTestVault) Get(key string) (any, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	val, ok := v.data[key]
	return val, ok
}

func (v *outboundTestVault) GetJSON(key string, dest any) (bool, error) {
	v.mu.Lock()
	val, ok := v.data[key]
	v.mu.Unlock()
	if !ok {
		return false, nil
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, dest)
}

func (v *outboundTestVault) Set(key string, value any) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.data == nil {
		v.data = make(map[string]any)
	}
	v.data[key] = value
	return nil
}

func (v *outboundTestVault) Delete(key string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.data, key)
	return nil
}

func (v *outboundTestVault) Has(key string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.data[key]
	return ok
}

func TestProviderRPCInvokesOutboundRPCLogForSuccessAndDriveSyncFailure(t *testing.T) {
	var mu sync.Mutex
	var events []ColabOneOutboundRPCEvent

	kernel := &rpcStubKernel{
		outputs: []KernelOutput{
			{
				OutputType: "stream",
				Name:       "stdout",
				Text:       `COLABONE_RPC={"ok":true,"value":{"ok":false,"error":"Google 云盘同步失败：rateLimitExceeded"}}` + "\n",
			},
		},
	}

	v := &outboundTestVault{}
	_ = v.Set("runtime_sessions", map[string]any{
		"m-s-test-outbound": map[string]any{
			"endpoint":         "m-s-test-outbound",
			"gpu":              "L4",
			"proxy_url":        "https://8080-m-s-test-outbound.prod.colab.dev",
			"proxy_token":      "secret-proxy-token-123",
			"token_expires_at": time.Now().Add(30 * time.Minute).Format(time.RFC3339Nano),
		},
	})

	p := NewColabOneProvider(ColabOneProviderConfig{
		Vault: v,
		KernelFactory: func(opts KernelOptions) KernelClient {
			return kernel
		},
		SafeMessage: func(s string) string { return s },
		OutboundRPCLog: func(ev ColabOneOutboundRPCEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	})
	defer p.Close()

	raw, err := p.RPC(context.Background(), "POST", "drive/sync", nil, "m-s-test-outbound", 30*time.Second)
	if err != nil {
		t.Fatalf("RPC returned unexpected error: %v", err)
	}
	resMap, ok := raw.(map[string]any)
	if !ok || resMap["ok"] != false {
		t.Fatalf("unexpected RPC result: %#v", raw)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("expected 1 outbound RPC event, got %d", len(events))
	}
	ev := events[0]
	if ev.Method != "POST" || ev.Path != "/api/v1/drive/sync" || ev.Endpoint != "m-s-test-outbound" {
		t.Fatalf("unexpected RPC event metadata: %+v", ev)
	}
	if ev.OK {
		t.Fatalf("expected ev.OK=false when remote drive/sync returns ok=false, got %+v", ev)
	}
	if !strings.Contains(ev.Detail, "rateLimitExceeded") {
		t.Fatalf("expected drive/sync error detail captured, got %+v", ev)
	}

	// Pre-flight call with no session must not emit a bogus outbound network log entry.
	emptyP := NewColabOneProvider(ColabOneProviderConfig{
		Vault: &outboundTestVault{},
		OutboundRPCLog: func(ev ColabOneOutboundRPCEvent) {
			t.Fatalf("unexpected outbound RPC log when no session exists: %+v", ev)
		},
	})
	defer emptyP.Close()
	if _, err := emptyP.RPC(context.Background(), "GET", "status", nil, "", 5*time.Second); err == nil {
		t.Fatalf("expected error when no session exists")
	}
}

type rpcStubKernel struct {
	outputs []KernelOutput
}

func (k *rpcStubKernel) Start(context.Context) error { return nil }
func (k *rpcStubKernel) ExecuteCode(context.Context, string, ExecuteOptions) ([]KernelOutput, error) {
	return k.outputs, nil
}
func (k *rpcStubKernel) Abort()                           {}
func (k *rpcStubKernel) Stop(context.Context, bool) error { return nil }
func (k *rpcStubKernel) KernelID() string                 { return "k-1" }
func (k *rpcStubKernel) SessionID() string                { return "s-1" }
