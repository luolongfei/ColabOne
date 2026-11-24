package colabclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStripXSSIPrefix(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []byte
	}{
		{
			name: "lf_prefix",
			in:   []byte(")]}'\n{\"ok\":true}"),
			want: []byte("{\"ok\":true}"),
		},
		{
			name: "crlf_prefix",
			in:   []byte(")]}'\r\n{\"ok\":true}"),
			want: []byte("{\"ok\":true}"),
		},
		{
			name: "no_prefix",
			in:   []byte("{\"ok\":true}"),
			want: []byte("{\"ok\":true}"),
		},
		{
			name: "empty",
			in:   []byte{},
			want: []byte{},
		},
		{
			name: "nil",
			in:   nil,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripXSSIPrefix(tc.in)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("StripXSSIPrefix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUUIDToWebSafeBase64(t *testing.T) {
	u := "550e8400-e29b-41d4-a716-446655440000"
	got := UUIDToWebSafeBase64(u)
	want := "550e8400_e29b_41d4_a716_446655440000........"
	if got != want {
		t.Fatalf("UUIDToWebSafeBase64(%q) = %q (len %d), want %q (len %d)", u, got, len(got), want, len(want))
	}
	if len(got) != 44 {
		t.Fatalf("expected length 44, got %d", len(got))
	}
	// Idempotent when called on an already-encoded nbh value.
	if again := UUIDToWebSafeBase64(got); again != want {
		t.Fatalf("UUIDToWebSafeBase64 not idempotent: got %q, want %q", again, want)
	}
	// Inputs longer than 44 chars must not panic.
	longInput := strings.Repeat("a", 50)
	if gotLong := UUIDToWebSafeBase64(longInput); gotLong != longInput {
		t.Fatalf("UUIDToWebSafeBase64(long) = %q, want %q", gotLong, longInput)
	}
}

func TestResolveAssignShape(t *testing.T) {
	cases := []struct {
		accel   string
		highMem bool
		want    string
	}{
		{"T4", true, "hm"},
		{"t4", true, "hm"},
		{"A100", true, "hm"},
		{"G4", true, "hm"},
		{"H100", true, "hm"},
		{"NONE", true, "hm"},
		{"L4", true, ""},
		{"l4", true, ""},
		{"V5E1", true, ""},
		{"v5e1", true, ""},
		{"V6E1", true, ""},
		{"v6e1", true, ""},
		{"T4", false, ""},
		{"A100", false, ""},
		{"L4", false, ""},
		{"", false, ""},
	}
	for _, tc := range cases {
		if got := ResolveAssignShape(tc.accel, tc.highMem); got != tc.want {
			t.Errorf("ResolveAssignShape(%q, %v) = %q, want %q", tc.accel, tc.highMem, got, tc.want)
		}
	}
	if got := ShapeDisplayLabel("HIGH_RAM"); got != "High-RAM" {
		t.Fatalf("ShapeDisplayLabel(HIGH_RAM) = %q", got)
	}
	if got := ShapeDisplayLabel("STANDARD"); got != "Standard" {
		t.Fatalf("ShapeDisplayLabel(STANDARD) = %q", got)
	}
}

func TestRuntimeProxyInfoAndExpiresAt(t *testing.T) {
	before := time.Now().UTC()
	var info ColabOneRuntimeProxyInfo
	raw := `{"token":"proxy-abc","tokenExpiresInSeconds":3600,"url":"https://rt.example.invalid"}`
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if info.Token != "proxy-abc" || info.TokenExpiresInSeconds != 3600 || info.URL != "https://rt.example.invalid" {
		t.Fatalf("unexpected info: %+v", info)
	}
	exp := info.ExpiresAt()
	after := time.Now().UTC()
	if exp.Before(before.Add(3599*time.Second)) || exp.After(after.Add(3601*time.Second)) {
		t.Fatalf("ExpiresAt() out of expected range: %v", exp)
	}
	if exp.Location() != time.UTC {
		t.Fatalf("ExpiresAt() should be UTC, got %v", exp.Location())
	}

	// String-encoded integer expiry is also accepted.
	var fromStr ColabOneRuntimeProxyInfo
	if err := json.Unmarshal([]byte(`{"token":"t","tokenExpiresInSeconds":"1800","url":"https://u"}`), &fromStr); err != nil {
		t.Fatalf("unmarshal string ttl failed: %v", err)
	}
	if fromStr.TokenExpiresInSeconds != 1800 {
		t.Fatalf("expected 1800, got %d", fromStr.TokenExpiresInSeconds)
	}
}

func TestListedAssignmentVariantAndShapeUnmarshal(t *testing.T) {
	cases := []struct {
		name      string
		payload   string
		wantVar   string
		wantShape string
		wantErr   bool
	}{
		{
			name:      "int_gpu_high_ram",
			payload:   `{"accelerator":"T4","endpoint":"m-s-1","variant":1,"machineShape":1,"runtimeProxyInfo":{"token":"tok","tokenExpiresInSeconds":3600,"url":"https://rt"}}`,
			wantVar:   "GPU",
			wantShape: "HIGH_RAM",
		},
		{
			name:      "int_default_standard",
			payload:   `{"accelerator":"NONE","endpoint":"m-s-2","variant":0,"machineShape":0,"runtimeProxyInfo":{"token":"tok","tokenExpiresInSeconds":3600,"url":"https://rt"}}`,
			wantVar:   "DEFAULT",
			wantShape: "STANDARD",
		},
		{
			name:      "int_tpu_standard",
			payload:   `{"accelerator":"V5E1","endpoint":"m-s-3","variant":2,"machineShape":0,"runtimeProxyInfo":{"token":"tok","tokenExpiresInSeconds":3600,"url":"https://rt"}}`,
			wantVar:   "TPU",
			wantShape: "STANDARD",
		},
		{
			name:      "string_gpu_high_ram",
			payload:   `{"accelerator":"A100","endpoint":"m-s-4","variant":"GPU","machineShape":"HIGH_RAM","runtimeProxyInfo":{"token":"tok","tokenExpiresInSeconds":3600,"url":"https://rt"}}`,
			wantVar:   "GPU",
			wantShape: "HIGH_RAM",
		},
		{
			name:      "string_tpu_standard",
			payload:   `{"accelerator":"V6E1","endpoint":"m-s-5","variant":"TPU","machineShape":"STANDARD","runtimeProxyInfo":{"token":"tok","tokenExpiresInSeconds":3600,"url":"https://rt"}}`,
			wantVar:   "TPU",
			wantShape: "STANDARD",
		},
		{
			name:      "string_default_prefixed_enum",
			payload:   `{"accelerator":"NONE","endpoint":"m-s-6","variant":"VARIANT_DEFAULT","machineShape":"SHAPE_HIGH_RAM","runtimeProxyInfo":{"token":"tok","tokenExpiresInSeconds":3600,"url":"https://rt"}}`,
			wantVar:   "DEFAULT",
			wantShape: "HIGH_RAM",
		},
		{
			name:      "omitted_variant_and_shape",
			payload:   `{"accelerator":"T4","endpoint":"m-s-7","runtimeProxyInfo":{"token":"tok","tokenExpiresInSeconds":3600,"url":"https://rt"}}`,
			wantVar:   "",
			wantShape: "",
		},
		{
			name:    "invalid_variant_object",
			payload: `{"accelerator":"T4","endpoint":"m-s-8","variant":{},"machineShape":0}`,
			wantErr: true,
		},
		{
			name:    "invalid_shape_bool",
			payload: `{"accelerator":"T4","endpoint":"m-s-9","variant":1,"machineShape":true}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var a ColabOneListedAssignment
			err := json.Unmarshal([]byte(tc.payload), &a)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (%+v)", a)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.Variant != tc.wantVar {
				t.Errorf("Variant = %q, want %q", a.Variant, tc.wantVar)
			}
			if a.MachineShape != tc.wantShape {
				t.Errorf("MachineShape = %q, want %q", a.MachineShape, tc.wantShape)
			}
		})
	}
}

func TestSessionStateJSONCompatibility(t *testing.T) {
	// Full Python colab_cli.state.SessionState.model_dump(mode="json") payload.
	pythonJSON := `{
		"name": "colabone",
		"token": "proxy-tok-123",
		"url": "https://abc.googleusercontent.com",
		"endpoint": "m-s-abc123",
		"token_expires_at": "2026-11-23T08:15:30.123456Z",
		"variant": "GPU",
		"accelerator": "L4",
		"machine_shape": "HIGH_RAM",
		"kernel_id": "kern-42",
		"session_id": "sess-99",
		"last_execution": null,
		"running": null
	}`
	var sess ColabOneSessionState
	if err := json.Unmarshal([]byte(pythonJSON), &sess); err != nil {
		t.Fatalf("unmarshal Python SessionState failed: %v", err)
	}
	if sess.Name != "colabone" || sess.Token != "proxy-tok-123" || sess.URL != "https://abc.googleusercontent.com" ||
		sess.Endpoint != "m-s-abc123" || sess.Variant != "GPU" || sess.Accelerator != "L4" ||
		sess.MachineShape != "HIGH_RAM" || sess.KernelID != "kern-42" || sess.SessionID != "sess-99" {
		t.Fatalf("unexpected SessionState: %+v", sess)
	}
	if sess.TokenExpiresAt == nil || sess.TokenExpiresAt.Year() != 2026 || sess.TokenExpiresAt.Minute() != 15 {
		t.Fatalf("unexpected TokenExpiresAt: %v", sess.TokenExpiresAt)
	}

	// Re-marshal and verify exact snake_case keys.
	marshaled, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal SessionState failed: %v", err)
	}
	var rawMap map[string]any
	if err := json.Unmarshal(marshaled, &rawMap); err != nil {
		t.Fatalf("unmarshal marshaled map failed: %v", err)
	}
	for _, key := range []string{"name", "token", "url", "endpoint", "token_expires_at", "variant", "accelerator", "machine_shape", "kernel_id", "session_id"} {
		if _, ok := rawMap[key]; !ok {
			t.Errorf("expected key %q in marshaled SessionState JSON: %s", key, string(marshaled))
		}
	}

	// Minimal vault payload applies Python defaults.
	minimalJSON := `{"name":"colabone","endpoint":"owned","token":"not-real","url":"https://runtime.invalid"}`
	var minimal ColabOneSessionState
	if err := json.Unmarshal([]byte(minimalJSON), &minimal); err != nil {
		t.Fatalf("unmarshal minimal SessionState failed: %v", err)
	}
	if minimal.Variant != "DEFAULT" || minimal.Accelerator != "NONE" || minimal.MachineShape != "STANDARD" {
		t.Fatalf("expected defaults DEFAULT/NONE/STANDARD, got %q/%q/%q", minimal.Variant, minimal.Accelerator, minimal.MachineShape)
	}
	if minimal.TokenExpiresAt != nil || minimal.KernelID != "" || minimal.SessionID != "" {
		t.Fatalf("expected zero optional fields, got %+v", minimal)
	}

	// Naive ISO timestamp and Unix epoch timestamps also parse cleanly.
	var naive ColabOneSessionState
	if err := json.Unmarshal([]byte(`{"name":"colabone","endpoint":"e","token":"t","url":"https://u","token_expires_at":"2026-11-23T08:15:30.5"}`), &naive); err != nil {
		t.Fatalf("unmarshal naive timestamp failed: %v", err)
	}
	if naive.TokenExpiresAt == nil || naive.TokenExpiresAt.Second() != 30 {
		t.Fatalf("unexpected naive TokenExpiresAt: %v", naive.TokenExpiresAt)
	}

	var epoch ColabOneSessionState
	if err := json.Unmarshal([]byte(`{"name":"colabone","endpoint":"e","token":"t","url":"https://u","token_expires_at":1700000000}`), &epoch); err != nil {
		t.Fatalf("unmarshal epoch timestamp failed: %v", err)
	}
	if epoch.TokenExpiresAt == nil || epoch.TokenExpiresAt.Unix() != 1700000000 {
		t.Fatalf("unexpected epoch TokenExpiresAt: %v", epoch.TokenExpiresAt)
	}
}

func TestConsumptionInfoUnmarshalVariants(t *testing.T) {
	var zeroBalance ColabOneConsumptionInfo
	if err := json.Unmarshal([]byte(`{"currentBalance":0,"paidComputeUnitsBalance":99,"consumptionRateHourly":0.5,"assignmentsCount":"3"}`), &zeroBalance); err != nil {
		t.Fatalf("unmarshal zero currentBalance failed: %v", err)
	}
	if zeroBalance.PaidComputeUnitsBalance != 0 || zeroBalance.ConsumptionRateHourly != 0.5 || zeroBalance.AssignmentsCount != 3 {
		t.Fatalf("unexpected zeroBalance: %+v", zeroBalance)
	}

	var paidOnly ColabOneConsumptionInfo
	if err := json.Unmarshal([]byte(`{"paidComputeUnitsBalance":42.5,"consumptionRateHourly":1.2,"assignmentsCount":1}`), &paidOnly); err != nil {
		t.Fatalf("unmarshal paidComputeUnitsBalance failed: %v", err)
	}
	if paidOnly.PaidComputeUnitsBalance != 42.5 || paidOnly.ConsumptionRateHourly != 1.2 || paidOnly.AssignmentsCount != 1 {
		t.Fatalf("unexpected paidOnly: %+v", paidOnly)
	}
}

func newTestColabClient(t *testing.T, server *httptest.Server, token string) *ColabOneColabClient {
	t.Helper()
	return NewColabOneColabClient(
		func(ctx context.Context) (string, error) { return token, nil },
		WithColabDomain(server.URL),
		WithHTTPClient(server.Client()),
	)
}

func assertStandardColabHeaders(t *testing.T, r *http.Request, wantToken string) {
	t.Helper()
	if got := r.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
	if got := r.Header.Get("X-Colab-Client-Agent"); got != "colab-cli" {
		t.Errorf("X-Colab-Client-Agent = %q, want colab-cli", got)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
		t.Errorf("Authorization = %q, want %q", got, "Bearer "+wantToken)
	}
	if got := r.URL.Query().Get("authuser"); got != "0" {
		t.Errorf("authuser query param = %q, want 0", got)
	}
}

func TestColabClientDefaultsAndOptions(t *testing.T) {
	c := NewColabOneColabClient(nil)
	if c.ColabDomain() != DefaultColabDomain {
		t.Fatalf("default ColabDomain() = %q, want %q", c.ColabDomain(), DefaultColabDomain)
	}
	if c.ColabAPIDomain() != DefaultColabAPIDomain {
		t.Fatalf("default ColabAPIDomain() = %q, want %q", c.ColabAPIDomain(), DefaultColabAPIDomain)
	}
	// Nil tokenProvider returns clean error instead of panicking.
	req, _ := http.NewRequest(http.MethodGet, DefaultColabDomain+"/tun/m/assignments", nil)
	if _, err := c.DoAuthorized(context.Background(), req); err == nil {
		t.Fatal("expected error when tokenProvider is nil")
	}

	custom := NewColabOneColabClient(
		func(ctx context.Context) (string, error) { return "tok", nil },
		WithColabDomain("https://colab.example.invalid/"),
		WithColabAPIDomain("https://api.example.invalid/"),
		WithTimeout(12*time.Second),
	)
	if custom.ColabDomain() != "https://colab.example.invalid" {
		t.Fatalf("ColabDomain() = %q", custom.ColabDomain())
	}
	if custom.ColabAPIDomain() != "https://api.example.invalid" {
		t.Fatalf("ColabAPIDomain() = %q", custom.ColabAPIDomain())
	}
}

func TestGetConsumptionUserInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertStandardColabHeaders(t, r, "oauth-tok-1")
		if r.Method != http.MethodGet || r.URL.Path != "/tun/m/ccu-info" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(")]}'\n{\"currentBalance\":94.25,\"consumptionRateHourly\":1.96,\"assignmentsCount\":2,\"extraField\":true}"))
	}))
	defer srv.Close()

	client := newTestColabClient(t, srv, "oauth-tok-1")
	if client.ColabDomain() != srv.URL {
		t.Fatalf("ColabDomain() = %q, want %q", client.ColabDomain(), srv.URL)
	}
	info, err := client.GetConsumptionUserInfo(context.Background())
	if err != nil {
		t.Fatalf("GetConsumptionUserInfo failed: %v", err)
	}
	if info.PaidComputeUnitsBalance != 94.25 {
		t.Errorf("PaidComputeUnitsBalance = %v, want 94.25", info.PaidComputeUnitsBalance)
	}
	if info.ConsumptionRateHourly != 1.96 {
		t.Errorf("ConsumptionRateHourly = %v, want 1.96", info.ConsumptionRateHourly)
	}
	if info.AssignmentsCount != 2 {
		t.Errorf("AssignmentsCount = %d, want 2", info.AssignmentsCount)
	}
}

func TestListAssignments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertStandardColabHeaders(t, r, "tok-list")
		if r.Method != http.MethodGet || r.URL.Path != "/tun/m/assignments" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(")]}'\n" + `{
			"assignments": [
				{
					"accelerator": "T4",
					"endpoint": "m-s-gpu1",
					"variant": 1,
					"machineShape": 1,
					"runtimeProxyInfo": {"token": "pt1", "tokenExpiresInSeconds": 3600, "url": "https://rt1.invalid"}
				},
				{
					"accelerator": "L4",
					"endpoint": "m-s-gpu2",
					"variant": "GPU",
					"machineShape": "STANDARD",
					"runtimeProxyInfo": {"token": "pt2", "tokenExpiresInSeconds": 1800, "url": "https://rt2.invalid"}
				}
			]
		}`))
	}))
	defer srv.Close()

	client := newTestColabClient(t, srv, "tok-list")
	list, err := client.ListAssignments(context.Background())
	if err != nil {
		t.Fatalf("ListAssignments failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	if list[0].Endpoint != "m-s-gpu1" || list[0].Variant != "GPU" || list[0].MachineShape != "HIGH_RAM" || list[0].RuntimeProxyInfo.Token != "pt1" {
		t.Errorf("unexpected list[0]: %+v", list[0])
	}
	if list[1].Endpoint != "m-s-gpu2" || list[1].Variant != "GPU" || list[1].MachineShape != "STANDARD" || list[1].RuntimeProxyInfo.URL != "https://rt2.invalid" {
		t.Errorf("unexpected list[1]: %+v", list[1])
	}
}

func TestListAssignmentsEmptyObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(")]}'\n{}"))
	}))
	defer srv.Close()

	client := newTestColabClient(t, srv, "tok-empty")
	list, err := client.ListAssignments(context.Background())
	if err != nil {
		t.Fatalf("ListAssignments failed: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("expected non-nil empty slice, got %#v", list)
	}
}

func TestAssignTwoPhaseSuccess(t *testing.T) {
	const rawUUID = "12345678-1234-5678-1234-567812345678"
	const wantNBH = "12345678_1234_5678_1234_567812345678........"

	var sawGet, sawPost bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertStandardColabHeaders(t, r, "tok-assign")
		if r.URL.Path != "/tun/m/assign" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("nbh") != wantNBH {
			t.Errorf("nbh = %q, want %q", q.Get("nbh"), wantNBH)
		}
		if q.Get("variant") != "GPU" || q.Get("accelerator") != "T4" || q.Get("shape") != "hm" {
			t.Errorf("unexpected assign params: %v", q)
		}

		switch r.Method {
		case http.MethodGet:
			sawGet = true
			_, _ = w.Write([]byte(")]}'\n" + `{"acc":"T4","nbh":"` + wantNBH + `","token":"xsrf-token-99","variant":"GPU"}`))
		case http.MethodPost:
			sawPost = true
			if got := r.Header.Get("X-Goog-Colab-Token"); got != "xsrf-token-99" {
				t.Errorf("X-Goog-Colab-Token = %q, want xsrf-token-99", got)
			}
			_, _ = w.Write([]byte(")]}'\n" + `{
				"accelerator": "T4",
				"endpoint": "m-hm-assigned1",
				"variant": 1,
				"runtimeProxyInfo": {"token": "rt-proxy-tok", "tokenExpiresInSeconds": 3600, "url": "https://assigned.invalid"}
			}`))
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
	}))
	defer srv.Close()

	client := newTestColabClient(t, srv, "tok-assign")
	shape := ResolveAssignShape("T4", true)
	already, xsrf, err := client.GetAssignment(context.Background(), rawUUID, "GPU", "T4", shape)
	if err != nil {
		t.Fatalf("GetAssignment failed: %v", err)
	}
	if already != nil {
		t.Fatalf("expected nil alreadyAssigned, got %+v", already)
	}
	if xsrf != "xsrf-token-99" {
		t.Fatalf("xsrf = %q, want xsrf-token-99", xsrf)
	}

	assigned, err := client.PostAssignment(context.Background(), rawUUID, xsrf, "GPU", "T4", shape)
	if err != nil {
		t.Fatalf("PostAssignment failed: %v", err)
	}
	if !sawGet || !sawPost {
		t.Fatalf("expected both GET and POST calls (sawGet=%v, sawPost=%v)", sawGet, sawPost)
	}
	if assigned.Endpoint != "m-hm-assigned1" || assigned.Accelerator != "T4" || assigned.Variant != "GPU" {
		t.Fatalf("unexpected assignment: %+v", assigned)
	}
	if assigned.RuntimeProxyInfo.Token != "rt-proxy-tok" || assigned.RuntimeProxyInfo.URL != "https://assigned.invalid" {
		t.Fatalf("unexpected RuntimeProxyInfo: %+v", assigned.RuntimeProxyInfo)
	}
}

func TestGetAssignmentAlreadyAssignedAndValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertStandardColabHeaders(t, r, "tok-already")
		if r.URL.Query().Has("shape") {
			t.Errorf("shape query param should be omitted for L4/STANDARD, got %q", r.URL.Query().Get("shape"))
		}
		if r.URL.Query().Get("accelerator") == "BAD" {
			_, _ = w.Write([]byte(")]}'\n{}"))
			return
		}
		_, _ = w.Write([]byte(")]}'\n" + `{
			"endpoint": "m-s-existing",
			"variant": 1,
			"acc": "L4",
			"runtimeProxyInfo": {"token": "existing-tok", "tokenExpiresInSeconds": 2400, "url": "https://existing.invalid"}
		}`))
	}))
	defer srv.Close()

	client := newTestColabClient(t, srv, "tok-already")
	already, xsrf, err := client.GetAssignment(
		context.Background(),
		"12345678-1234-5678-1234-567812345678",
		"GPU",
		"L4",
		ResolveAssignShape("L4", true),
	)
	if err != nil {
		t.Fatalf("GetAssignment failed: %v", err)
	}
	if xsrf != "" {
		t.Fatalf("expected empty xsrf when already assigned, got %q", xsrf)
	}
	if already == nil || already.Endpoint != "m-s-existing" || already.Variant != "GPU" || already.Accelerator != "L4" || already.RuntimeProxyInfo.Token != "existing-tok" {
		t.Fatalf("unexpected alreadyAssigned: %+v", already)
	}

	// Empty JSON response without endpoint or token fails cleanly.
	if _, _, err := client.GetAssignment(context.Background(), "12345678-1234-5678-1234-567812345678", "GPU", "BAD", "STANDARD"); err == nil {
		t.Fatal("expected error for response missing both assignment and token")
	}

	// Empty nbhUUID / xsrfToken fail validation before hitting network.
	if _, _, err := client.GetAssignment(context.Background(), "", "GPU", "T4", ""); err == nil {
		t.Fatal("expected error for empty nbhUUID")
	}
	if _, err := client.PostAssignment(context.Background(), "12345678-1234-5678-1234-567812345678", "", "GPU", "T4", ""); err == nil {
		t.Fatal("expected error for empty xsrfToken")
	}
}

func TestPostAssignmentHTTP412And503(t *testing.T) {
	t.Run("http_412_maps_to_ErrTooManyAssignments", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte("too many active runtimes"))
		}))
		defer srv.Close()

		client := newTestColabClient(t, srv, "tok-412")
		_, err := client.PostAssignment(context.Background(), "12345678-1234-5678-1234-567812345678", "xsrf", "GPU", "T4", "")
		if err == nil {
			t.Fatal("expected error on HTTP 412")
		}
		if !errors.Is(err, ErrTooManyAssignments) {
			t.Fatalf("expected errors.Is(err, ErrTooManyAssignments), got %v", err)
		}
		var reqErr *ColabOneRequestError
		if !errors.As(err, &reqErr) {
			t.Fatalf("expected ColabOneRequestError to also be wrapped, got %T", err)
		}
		if reqErr.StatusCode != 412 || reqErr.Method != http.MethodPost || reqErr.Body != "too many active runtimes" {
			t.Fatalf("unexpected ColabOneRequestError: %+v", reqErr)
		}
	})

	t.Run("http_503_returns_ColabOneRequestError", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("capacity exhausted"))
		}))
		defer srv.Close()

		client := newTestColabClient(t, srv, "tok-503")
		_, err := client.PostAssignment(context.Background(), "12345678-1234-5678-1234-567812345678", "xsrf", "GPU", "G4", "")
		if err == nil {
			t.Fatal("expected error on HTTP 503")
		}
		if errors.Is(err, ErrTooManyAssignments) {
			t.Fatalf("HTTP 503 must not match ErrTooManyAssignments")
		}
		var reqErr *ColabOneRequestError
		if !errors.As(err, &reqErr) || reqErr.StatusCode != 503 || reqErr.Reason != "Service Unavailable" {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestUnassignTwoStepXSRF(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertStandardColabHeaders(t, r, "tok-unassign")
		if r.URL.Path != "/tun/m/unassign/m-s-stopme" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		methods = append(methods, r.Method)
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(")]}'\n{\"token\":\"unassign-xsrf-77\"}"))
		case http.MethodPost:
			if got := r.Header.Get("X-Goog-Colab-Token"); got != "unassign-xsrf-77" {
				t.Errorf("X-Goog-Colab-Token = %q, want unassign-xsrf-77", got)
			}
			if len(methods) == 2 {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(")]}'\n{}"))
			}
		}
	}))
	defer srv.Close()

	client := newTestColabClient(t, srv, "tok-unassign")
	if err := client.Unassign(context.Background(), "m-s-stopme"); err != nil {
		t.Fatalf("Unassign (204) failed: %v", err)
	}
	if err := client.Unassign(context.Background(), "m-s-stopme"); err != nil {
		t.Fatalf("Unassign (200 with XSSI empty obj) failed: %v", err)
	}
	if len(methods) != 4 || methods[0] != http.MethodGet || methods[1] != http.MethodPost {
		t.Fatalf("unexpected methods sequence: %v", methods)
	}
	if err := client.Unassign(context.Background(), "   "); err == nil {
		t.Fatal("expected error for blank endpoint")
	}
}

func TestKeepAlive(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		assertStandardColabHeaders(t, r, "tok-ka")
		if r.URL.Path == "/tun/m/m-s-dead/keep-alive/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/tun/m/m-s-alive/keep-alive/" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Colab-Tunnel"); got != "Google" {
			t.Errorf("X-Colab-Tunnel = %q, want Google", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newTestColabClient(t, srv, "tok-ka")
	if err := client.KeepAlive(context.Background(), "m-s-alive"); err != nil {
		t.Fatalf("KeepAlive failed: %v", err)
	}
	if !hit {
		t.Fatal("keep-alive endpoint was not called")
	}
	err := client.KeepAlive(context.Background(), "m-s-dead")
	var reqErr *ColabOneRequestError
	if !errors.As(err, &reqErr) || reqErr.StatusCode != 404 {
		t.Fatalf("expected 404 ColabOneRequestError, got %v", err)
	}
	if err := client.KeepAlive(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty endpoint")
	}
}

func TestDoAuthorizedDriveFSAndExternalHost(t *testing.T) {
	colabSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertStandardColabHeaders(t, r, "tok-drivefs")
		if r.URL.Path != "/tun/m/credentials-propagation/m-s-ep1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("authtype") != "dfs_ephemeral" || r.URL.Query().Get("dryrun") != "false" {
			t.Errorf("query params lost: %v", r.URL.Query())
		}
		if r.Header.Get("X-Goog-Colab-Token") != "drive-xsrf" {
			t.Errorf("X-Goog-Colab-Token not preserved: %q", r.Header.Get("X-Goog-Colab-Token"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm failed: %v", err)
		}
		if got := r.FormValue("file_id"); got != "empty.ipynb" {
			t.Errorf("file_id = %q, want empty.ipynb", got)
		}
		_, _ = w.Write([]byte(")]}'\n{\"success\":true}"))
	}))
	defer colabSrv.Close()

	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-drivefs" {
			t.Errorf("missing Authorization header on external host")
		}
		if r.URL.Query().Has("authuser") {
			t.Errorf("authuser must NOT be injected for non-colabDomain host, got %v", r.URL.Query())
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer otherSrv.Close()

	client := newTestColabClient(t, colabSrv, "tok-drivefs")

	// Simulate DriveFS credentials-propagation multipart POST.
	var mp bytes.Buffer
	writer := multipart.NewWriter(&mp)
	if err := writer.WriteField("file_id", "empty.ipynb"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(
		http.MethodPost,
		client.ColabDomain()+"/tun/m/credentials-propagation/m-s-ep1?authtype=dfs_ephemeral&version=2&dryrun=false&propagate=true&record=false",
		&mp,
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Goog-Colab-Token", "drive-xsrf")

	resp, err := client.DoAuthorized(context.Background(), req)
	if err != nil {
		t.Fatalf("DoAuthorized colabSrv failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(StripXSSIPrefix(body)) != `{"success":true}` {
		t.Fatalf("unexpected DriveFS body: %q", string(body))
	}
	// Verify caller's original request was not mutated.
	if req.Header.Get("Authorization") != "" {
		t.Errorf("caller request Authorization header was mutated")
	}

	// Non-colabDomain host must not receive authuser=0.
	extReq, _ := http.NewRequest(http.MethodGet, otherSrv.URL+"/v1/other", nil)
	extResp, err := client.DoAuthorized(context.Background(), extReq)
	if err != nil {
		t.Fatalf("DoAuthorized otherSrv failed: %v", err)
	}
	extResp.Body.Close()
}

func TestColabClientTokenProviderErrorAndCancellation(t *testing.T) {
	wantErr := errors.New("oauth refresh failed")
	client := NewColabOneColabClient(func(ctx context.Context) (string, error) {
		return "", wantErr
	})
	if _, err := client.ListAssignments(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("expected token provider error %v, got %v", wantErr, err)
	}

	emptyTokClient := NewColabOneColabClient(func(ctx context.Context) (string, error) {
		return "   ", nil
	})
	if _, err := emptyTokClient.ListAssignments(context.Background()); err == nil {
		t.Fatal("expected error for blank access token")
	}

	unblock := make(chan struct{})
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-unblock:
		case <-r.Context().Done():
		}
	}))
	defer slowSrv.Close()
	defer close(unblock)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	slowClient := newTestColabClient(t, slowSrv, "tok-slow")
	if _, err := slowClient.ListAssignments(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestContentsClientUploadListDownloadDelete(t *testing.T) {
	type storedFile struct {
		format  string
		content string
	}
	var mu sync.Mutex
	files := map[string]storedFile{}
	var lastUploadEscapedPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("authuser") != "0" {
			t.Errorf("missing authuser=0: %v", r.URL.Query())
		}
		if r.URL.Query().Get("colab-runtime-proxy-token") != "proxy-secret-tok" {
			t.Errorf("wrong proxy token: %q", r.URL.Query().Get("colab-runtime-proxy-token"))
		}
		if r.Header.Get("X-Colab-Client-Agent") != "colab-cli" {
			t.Errorf("missing X-Colab-Client-Agent header")
		}
		rel := strings.TrimPrefix(r.URL.Path, "/api/contents/")

		switch r.Method {
		case http.MethodPut:
			var payload struct {
				Name    string `json:"name"`
				Path    string `json:"path"`
				Type    string `json:"type"`
				Format  string `json:"format"`
				Content string `json:"content"`
				Chunk   int    `json:"chunk"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode PUT payload failed: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if payload.Type != "file" || payload.Format != "base64" || payload.Chunk != 1 {
				t.Errorf("unexpected upload metadata: %+v", payload)
			}
			if payload.Name == "" || !strings.HasSuffix(payload.Path, payload.Name) {
				t.Errorf("unexpected name/path: name=%q path=%q", payload.Name, payload.Path)
			}
			mu.Lock()
			lastUploadEscapedPath = r.URL.EscapedPath()
			files[rel] = storedFile{format: payload.Format, content: payload.Content}
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"name": payload.Name, "path": payload.Path, "type": "file"})

		case http.MethodGet:
			if rel == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name":    "",
					"path":    "",
					"type":    "directory",
					"content": []any{},
				})
				return
			}
			if rel == "content/dir" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name":    "dir",
					"path":    "content/dir",
					"type":    "directory",
					"content": []any{map[string]any{"name": "a.txt", "type": "file"}},
				})
				return
			}
			if rel == "content/note.txt" && r.URL.Query().Get("content") == "1" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name":    "note.txt",
					"path":    "content/note.txt",
					"type":    "file",
					"format":  "text",
					"content": "hello 文本",
				})
				return
			}
			if rel == "content/nb.ipynb" && r.URL.Query().Get("content") == "1" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name":    "nb.ipynb",
					"path":    "content/nb.ipynb",
					"type":    "notebook",
					"format":  "json",
					"content": map[string]any{"nbformat": 4},
				})
				return
			}
			mu.Lock()
			f, ok := files[rel]
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such file or directory"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":    rel,
				"path":    rel,
				"type":    "file",
				"format":  f.format,
				"content": f.content,
			})

		case http.MethodDelete:
			mu.Lock()
			_, ok := files[rel]
			delete(files, rel)
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	cc := NewColabOneContentsClient(srv.Client())
	ctx := context.Background()

	// 1. Upload binary bytes with spaces, #, +, and UTF-8 in path.
	binaryPayload := []byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff, 0xfe, 0x42}
	remotePath := "content/my folder/bundle #1+2 中文.zip"
	if err := cc.UploadBytes(ctx, srv.URL+"/", "proxy-secret-tok", remotePath, binaryPayload, 5*time.Second); err != nil {
		t.Fatalf("UploadBytes failed: %v", err)
	}
	mu.Lock()
	gotEscaped := lastUploadEscapedPath
	mu.Unlock()
	if !strings.HasPrefix(gotEscaped, "/api/contents/content/my%20folder/") || !strings.Contains(gotEscaped, "%231%2B2%20") {
		t.Fatalf("unexpected escaped upload path: %q", gotEscaped)
	}

	// 2. Download base64 binary back and verify exact byte parity.
	gotBytes, err := cc.DownloadBytes(ctx, srv.URL, "proxy-secret-tok", remotePath)
	if err != nil {
		t.Fatalf("DownloadBytes(base64) failed: %v", err)
	}
	if !bytes.Equal(gotBytes, binaryPayload) {
		t.Fatalf("downloaded bytes = %x, want %x", gotBytes, binaryPayload)
	}

	// 3. Download text-format and json-format files.
	gotText, err := cc.DownloadBytes(ctx, srv.URL, "proxy-secret-tok", "/content/note.txt")
	if err != nil {
		t.Fatalf("DownloadBytes(text) failed: %v", err)
	}
	if string(gotText) != "hello 文本" {
		t.Fatalf("downloaded text = %q, want %q", string(gotText), "hello 文本")
	}
	gotNB, err := cc.DownloadBytes(ctx, srv.URL, "proxy-secret-tok", "content/nb.ipynb")
	if err != nil {
		t.Fatalf("DownloadBytes(json) failed: %v", err)
	}
	if string(gotNB) != `{"nbformat":4}` {
		t.Fatalf("downloaded json = %q, want {\"nbformat\":4}", string(gotNB))
	}

	// 4. ListDir returns directory object (both nested and root).
	listing, err := cc.ListDir(ctx, srv.URL, "proxy-secret-tok", "content/dir")
	if err != nil {
		t.Fatalf("ListDir failed: %v", err)
	}
	if listing["type"] != "directory" || listing["name"] != "dir" {
		t.Fatalf("unexpected ListDir response: %+v", listing)
	}
	rootListing, err := cc.ListDir(ctx, srv.URL, "proxy-secret-tok", "/")
	if err != nil {
		t.Fatalf("ListDir(root) failed: %v", err)
	}
	if rootListing["type"] != "directory" {
		t.Fatalf("unexpected root ListDir response: %+v", rootListing)
	}

	// 5. DownloadBytes on directory returns ErrIsDirectory.
	if _, err := cc.DownloadBytes(ctx, srv.URL, "proxy-secret-tok", "content/dir"); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("expected ErrIsDirectory, got %v", err)
	}

	// 6. DeleteFile removes uploaded file and subsequent Download returns ErrNotFound + os.ErrNotExist.
	if err := cc.DeleteFile(ctx, srv.URL, "proxy-secret-tok", remotePath); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
	_, err = cc.DownloadBytes(ctx, srv.URL, "proxy-secret-tok", remotePath)
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotFound and os.ErrNotExist, got %v", err)
	}
	var reqErr *ColabOneRequestError
	if !errors.As(err, &reqErr) || reqErr.StatusCode != 404 {
		t.Fatalf("expected wrapped 404 ColabOneRequestError, got %v", err)
	}
	if strings.Contains(reqErr.URL, "proxy-secret-tok") {
		t.Fatalf("ColabOneRequestError.URL leaked proxy token: %q", reqErr.URL)
	}
}

func TestContentsClientUploadTimeoutAndErrorStatus(t *testing.T) {
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "slow.bin") {
			select {
			case <-unblock:
			case <-r.Context().Done():
			}
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("token expired"))
	}))
	defer srv.Close()
	defer close(unblock)

	cc := NewColabOneContentsClient(nil)

	if err := cc.UploadBytes(context.Background(), srv.URL, "secret-tok", "content/slow.bin", []byte("abc"), 50*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded on slow upload, got %v", err)
	}

	err := cc.UploadBytes(context.Background(), srv.URL, "secret-tok", "content/auth.bin", []byte("abc"), 5*time.Second)
	var reqErr *ColabOneRequestError
	if !errors.As(err, &reqErr) || reqErr.StatusCode != 403 {
		t.Fatalf("expected HTTP 403 ColabOneRequestError, got %v", err)
	}
	if strings.Contains(reqErr.URL, "secret-tok") {
		t.Fatalf("proxy token leaked in error URL: %q", reqErr.URL)
	}

	// Zero-value ColabOneContentsClient is safe to use and rejects empty baseURL.
	var zeroCC ColabOneContentsClient
	if err := zeroCC.UploadBytes(context.Background(), "", "tok", "content/a", []byte("x"), time.Second); err == nil {
		t.Fatal("expected error for empty baseURL")
	}
}

func TestConcurrentClientSafety(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/tun/m/ccu-info":
			_, _ = w.Write([]byte(")]}'\n{\"currentBalance\":50.0,\"consumptionRateHourly\":1.0,\"assignmentsCount\":1}"))
		case r.URL.Path == "/tun/m/assignments":
			_, _ = w.Write([]byte(")]}'\n{\"assignments\":[]}"))
		case strings.HasPrefix(r.URL.Path, "/tun/m/") && strings.HasSuffix(r.URL.Path, "/keep-alive/"):
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/api/contents/"):
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"ok":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"type":"file","format":"base64","content":"` + base64.StdEncoding.EncodeToString([]byte("ok")) + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := newTestColabClient(t, srv, "tok-concurrent")
	contents := NewColabOneContentsClient(srv.Client())

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 10; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			if _, err := client.GetConsumptionUserInfo(context.Background()); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := client.ListAssignments(context.Background()); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			if err := client.KeepAlive(context.Background(), "m-s-conc"); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			if err := contents.UploadBytes(context.Background(), srv.URL, "ptok", "content/c.bin", []byte("hi"), 5*time.Second); err != nil {
				errs <- err
			}
			if _, err := contents.DownloadBytes(context.Background(), srv.URL, "ptok", "content/c.bin"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent call failed: %v", err)
	}
}

func TestContentsTransportErrorRedactsProxyTokenAndConsumptionFlexibleFloats(t *testing.T) {
	secretTok := "super-secret-runtime-proxy-tok-999"
	contents := NewColabOneContentsClient(&http.Client{Timeout: 500 * time.Millisecond})
	// Closed loopback port triggers *url.Error from http.Client.Do.
	err := contents.UploadBytes(context.Background(), "http://127.0.0.1:1", secretTok, "content/a.bin", []byte("x"), 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected transport error on closed port")
	}
	if strings.Contains(err.Error(), secretTok) {
		t.Fatalf("transport error leaked runtime proxy token: %v", err)
	}

	var info ColabOneConsumptionInfo
	if err := json.Unmarshal([]byte(`{"currentBalance":"123.5","consumptionRateHourly":"NaN","assignmentsCount":"2"}`), &info); err != nil {
		t.Fatalf("Unmarshal flexible consumption info failed: %v", err)
	}
	if info.PaidComputeUnitsBalance != 123.5 || info.ConsumptionRateHourly != 0 || info.AssignmentsCount != 2 {
		t.Fatalf("unexpected flexible consumption info: %+v", info)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestContentsMalformedBaseURLRedactsProxyToken(t *testing.T) {
	const sentinelToken = "sentinel-proxy-token-leak-check-8472"

	offlineTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("offline transport error for %s", r.URL.String())
	})
	offlineClient := NewColabOneContentsClient(&http.Client{Transport: offlineTransport})
	defaultClient := NewColabOneContentsClient(nil)

	malformedBases := []struct {
		name      string
		baseURL   string
		parseFail bool
	}{
		{name: "invalid_port", baseURL: "https://rt.invalid:badport", parseFail: true},
		{name: "control_char_del", baseURL: "https://rt.invalid\x7f", parseFail: true},
		{name: "space_in_host", baseURL: "https://rt.invalid bad", parseFail: true},
		{name: "missing_scheme", baseURL: "://missing-scheme", parseFail: true},
		{name: "invalid_escape", baseURL: "https://rt.invalid/%ZZ", parseFail: true},
		{name: "fragment_hides_query", baseURL: "https://rt.invalid#frag", parseFail: false},
	}

	assertNoTokenLeak := func(t *testing.T, op string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected error, got nil", op)
		}
		if strings.Contains(err.Error(), sentinelToken) {
			t.Fatalf("%s: error leaked proxy token: %v", op, err)
		}
		var uerr *url.Error
		if errors.As(err, &uerr) {
			if strings.Contains(uerr.URL, sentinelToken) {
				t.Fatalf("%s: *url.Error.URL leaked proxy token: %q", op, uerr.URL)
			}
			if uerr.URL != "[redacted]" {
				t.Fatalf("%s: expected *url.Error.URL == %q, got %q", op, "[redacted]", uerr.URL)
			}
		}
	}

	for _, tc := range malformedBases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cc := offlineClient
			if tc.parseFail {
				// Confirm default client also fails before network dial without leaking.
				assertNoTokenLeak(t, "default/UploadBytes", defaultClient.UploadBytes(ctx, tc.baseURL, sentinelToken, "content/a.bin", []byte("x"), time.Second))
			}

			assertNoTokenLeak(t, "UploadBytes", cc.UploadBytes(ctx, tc.baseURL, sentinelToken, "content/a.bin", []byte("x"), time.Second))
			_, err := cc.DownloadBytes(ctx, tc.baseURL, sentinelToken, "content/a.bin")
			assertNoTokenLeak(t, "DownloadBytes", err)
			_, err = cc.ListDir(ctx, tc.baseURL, sentinelToken, "content")
			assertNoTokenLeak(t, "ListDir", err)
			assertNoTokenLeak(t, "DeleteFile", cc.DeleteFile(ctx, tc.baseURL, sentinelToken, "content/a.bin"))
		})
	}

	// Non-*url.Error returned by a custom RoundTripper that echoes req.URL must also be redacted.
	t.Run("custom_roundtripper_echoing_url", func(t *testing.T) {
		err := offlineClient.UploadBytes(context.Background(), "https://rt.example.invalid", sentinelToken, "content/a.bin", []byte("x"), time.Second)
		if err == nil {
			t.Fatal("expected error from custom RoundTripper")
		}
		if strings.Contains(err.Error(), sentinelToken) {
			t.Fatalf("error leaked proxy token from RoundTripper message: %v", err)
		}
		if unwrapped := errors.Unwrap(err); unwrapped != nil && strings.Contains(unwrapped.Error(), sentinelToken) {
			t.Fatalf("unwrapped error leaked proxy token: %v", unwrapped)
		}
	})

	// HTTP status error when baseURL contains a fragment must not leak proxy token in ColabOneRequestError.URL.
	t.Run("http_error_with_fragment_baseURL", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("denied"))
		}))
		defer srv.Close()

		cc := NewColabOneContentsClient(srv.Client())
		err := cc.UploadBytes(context.Background(), srv.URL+"#frag", sentinelToken, "content/a.bin", []byte("x"), time.Second)
		var reqErr *ColabOneRequestError
		if !errors.As(err, &reqErr) || reqErr.StatusCode != http.StatusForbidden {
			t.Fatalf("expected HTTP 403 ColabOneRequestError, got %v", err)
		}
		if strings.Contains(reqErr.URL, sentinelToken) || reqErr.URL != "[redacted]" {
			t.Fatalf("expected [redacted] URL without token leak, got %q", reqErr.URL)
		}
	})
}

func TestSanitizeContentsURLErrorAndRedactURL(t *testing.T) {
	const tok = "secret-tok-99421"

	if got := redactProxyTokenURL("https://rt.invalid:badport/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok); got != "[redacted]" {
		t.Fatalf("redactProxyTokenURL(invalid port) = %q, want [redacted]", got)
	}
	if got := redactProxyTokenURL("https://rt.invalid\x7f/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok); got != "[redacted]" {
		t.Fatalf("redactProxyTokenURL(control char) = %q, want [redacted]", got)
	}
	if got := redactProxyTokenURL("https://rt.invalid/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok + "#colab-runtime-proxy-token=" + tok); got != "[redacted]" {
		t.Fatalf("redactProxyTokenURL(duplicate in fragment) = %q, want [redacted]", got)
	}
	if got := redactProxyTokenURL("https://rt.example.invalid/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok); strings.Contains(got, tok) || !strings.Contains(got, "colab-runtime-proxy-token=REDACTED") {
		t.Fatalf("redactProxyTokenURL(valid) = %q", got)
	}

	if sanitizeContentsURLError(nil) != nil {
		t.Fatal("sanitizeContentsURLError(nil) must return nil")
	}

	// Clean error without token is returned unchanged.
	cleanErr := errors.New("plain failure")
	if got := sanitizeContentsURLError(cleanErr); got != cleanErr {
		t.Fatalf("clean error should be returned unchanged, got %v", got)
	}

	// Wrapped malformed *url.Error preserves outer context & inner sentinel while redacting URL.
	malformedUErr := &url.Error{
		Op:  "parse",
		URL: "https://rt.invalid:badport/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok,
		Err: context.DeadlineExceeded,
	}
	wrapped := fmt.Errorf("upload failed: %w", malformedUErr)
	sanitizedWrapped := sanitizeContentsURLError(wrapped)
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

	// Nested *url.Error redacts both outer and inner URLs.
	nested := &url.Error{
		Op:  "Get",
		URL: "https://rt.example.invalid/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok,
		Err: &url.Error{
			Op:  "parse",
			URL: "https://rt.invalid\x7f/api/contents/a?authuser=0&colab-runtime-proxy-token=" + tok,
			Err: context.Canceled,
		},
	}
	sanitizedNested := sanitizeContentsURLError(nested)
	if strings.Contains(sanitizedNested.Error(), tok) {
		t.Fatalf("nested *url.Error leaked token: %v", sanitizedNested)
	}
	if !errors.Is(sanitizedNested, context.Canceled) {
		t.Fatalf("nested *url.Error lost context.Canceled: %v", sanitizedNested)
	}

	// Non-*url.Error containing raw or percent-encoded token (including UTF-8 İ byte-length edge case).
	plainWithTok := fmt.Errorf("İ proxy rejected https://rt.invalid/api/contents/a?authuser=0&colab-runtime-proxy-token=%s&content=1: %w", tok, context.Canceled)
	sanitizedPlain := sanitizeContentsURLError(plainWithTok)
	if strings.Contains(sanitizedPlain.Error(), tok) {
		t.Fatalf("non-*url.Error leaked token: %v", sanitizedPlain)
	}
	if !strings.Contains(sanitizedPlain.Error(), "colab-runtime-proxy-token=REDACTED&content=1") {
		t.Fatalf("unexpected non-*url.Error redaction: %v", sanitizedPlain)
	}
	if !errors.Is(sanitizedPlain, context.Canceled) {
		t.Fatalf("non-*url.Error lost wrapped sentinel: %v", sanitizedPlain)
	}

	encodedTokErr := errors.New("redirect target https%3A%2F%2Frt.invalid%3Fcolab-runtime-proxy-token%3D" + tok + "%26content%3D1 failed")
	sanitizedEncoded := sanitizeContentsURLError(encodedTokErr)
	if strings.Contains(sanitizedEncoded.Error(), tok) {
		t.Fatalf("encoded non-*url.Error leaked token: %v", sanitizedEncoded)
	}

	// errors.Join with both a sentinel error and a token-leaking error.
	sentinel := errors.New("sentinel cause")
	joined := errors.Join(sentinel, malformedUErr)
	sanitizedJoined := sanitizeContentsURLError(joined)
	if strings.Contains(sanitizedJoined.Error(), tok) {
		t.Fatalf("joined error leaked token: %v", sanitizedJoined)
	}
	if !errors.Is(sanitizedJoined, sentinel) || !errors.Is(sanitizedJoined, context.DeadlineExceeded) {
		t.Fatalf("joined error lost wrapped causes: %v", sanitizedJoined)
	}
}

type uncomparableContentsError struct {
	tags []string
	msg  string
}

func (e uncomparableContentsError) Error() string { return e.msg }

type hiddenInnerWrapError struct {
	msg string
	err error
}

func (e *hiddenInnerWrapError) Error() string { return e.msg }
func (e *hiddenInnerWrapError) Unwrap() error { return e.err }

type customMultiContentsError struct {
	msg  string
	errs []error
}

func (e *customMultiContentsError) Error() string   { return e.msg }
func (e *customMultiContentsError) Unwrap() []error { return e.errs }

type flakyEmptyContentsError struct {
	calls atomic.Int32
	first string
}

func (e *flakyEmptyContentsError) Error() string {
	if e.calls.Add(1) == 1 {
		return e.first
	}
	return ""
}

func TestSanitizeContentsURLErrorEdgeCases(t *testing.T) {
	const tok = "contents-hardened-secret-tok-7741"

	// 1. Uncomparable error struct (direct, inside *url.Error, inside errors.Join) must never panic.
	uncompClean := uncomparableContentsError{tags: []string{"a", "b"}, msg: "clean uncomparable"}
	if got := sanitizeContentsURLError(uncompClean); got == nil || got.Error() != "clean uncomparable" {
		t.Fatalf("clean uncomparable error = %v, want clean uncomparable", got)
	}
	uncompTok := uncomparableContentsError{
		tags: []string{"secret"},
		msg:  "failed https://rt.invalid/api/contents/a?colab-runtime-proxy-token=" + tok,
	}
	gotUncompTok := sanitizeContentsURLError(uncompTok)
	if strings.Contains(gotUncompTok.Error(), tok) || !strings.Contains(gotUncompTok.Error(), "REDACTED") {
		t.Fatalf("uncomparable token error not redacted: %v", gotUncompTok)
	}
	uerrUncompClean := &url.Error{Op: "Get", URL: "https://rt.example.invalid/clean", Err: uncompClean}
	if got := sanitizeContentsURLError(uerrUncompClean); got != uerrUncompClean {
		t.Fatalf("clean *url.Error wrapping uncomparable err should be returned unchanged, got %v", got)
	}
	uerrUncompTok := &url.Error{
		Op:  "Put",
		URL: "https://rt.example.invalid/api/contents/a?colab-runtime-proxy-token=" + tok,
		Err: uncompClean,
	}
	gotUErrUncomp := sanitizeContentsURLError(uerrUncompTok)
	if strings.Contains(gotUErrUncomp.Error(), tok) {
		t.Fatalf("*url.Error wrapping uncomparable err leaked token: %v", gotUErrUncomp)
	}
	joinedUncomp := errors.Join(uncompClean, &url.Error{
		Op:  "Get",
		URL: "https://rt.example.invalid/api/contents/a?colab-runtime-proxy-token=" + tok,
		Err: context.DeadlineExceeded,
	})
	gotJoinedUncomp := sanitizeContentsURLError(joinedUncomp)
	if strings.Contains(gotJoinedUncomp.Error(), tok) {
		t.Fatalf("errors.Join with uncomparable error leaked token: %v", gotJoinedUncomp)
	}
	if !errors.Is(gotJoinedUncomp, context.DeadlineExceeded) {
		t.Fatalf("errors.Join with uncomparable error lost DeadlineExceeded: %v", gotJoinedUncomp)
	}

	// 2. Typed-nil *url.Error (direct and nested) must never panic.
	var typedNil *url.Error
	if got := sanitizeContentsURLError(typedNil); got != nil {
		t.Fatalf("sanitizeContentsURLError((*url.Error)(nil)) = %v, want nil", got)
	}
	wrappedTypedNil := &hiddenInnerWrapError{
		msg: "outer colab-runtime-proxy-token=" + tok,
		err: typedNil,
	}
	gotWrappedTypedNil := sanitizeContentsURLError(wrappedTypedNil)
	if strings.Contains(gotWrappedTypedNil.Error(), tok) || !strings.Contains(gotWrappedTypedNil.Error(), "REDACTED") {
		t.Fatalf("wrapped typed-nil *url.Error failed redaction: %v", gotWrappedTypedNil)
	}
	multiTypedNil := &customMultiContentsError{
		msg:  "multi colab-runtime-proxy-token=" + tok,
		errs: []error{typedNil, context.DeadlineExceeded},
	}
	gotMultiTypedNil := sanitizeContentsURLError(multiTypedNil)
	if strings.Contains(gotMultiTypedNil.Error(), tok) || !errors.Is(gotMultiTypedNil, context.DeadlineExceeded) {
		t.Fatalf("multi typed-nil *url.Error failed redaction or lost cause: %v", gotMultiTypedNil)
	}
	uerrTypedNilInner := &url.Error{
		Op:  "Get",
		URL: "https://rt.example.invalid/api/contents/a?colab-runtime-proxy-token=" + tok,
		Err: typedNil,
	}
	gotUErrTypedNilInner := sanitizeContentsURLError(uerrTypedNilInner)
	if gotUErrTypedNilInner == nil {
		t.Fatal("expected sanitized *url.Error, got nil")
	}
	var extractedTypedNilOuter *url.Error
	if !errors.As(gotUErrTypedNilInner, &extractedTypedNilOuter) || strings.Contains(extractedTypedNilOuter.URL, tok) || extractedTypedNilOuter.Err != nil {
		t.Fatalf("unexpected sanitized *url.Error with typed-nil inner: %+v", extractedTypedNilOuter)
	}

	// 3. Multi-error with token in outer message (and clean children) redacts outer text
	//    while preserving errors.Is(err, context.DeadlineExceeded) and other wrapped causes.
	sentinel := errors.New("contents sentinel")
	multiOuterTok := fmt.Errorf(
		"upload https://rt.invalid/api/contents/a?colab-runtime-proxy-token=%s failed (%w; %w)",
		tok,
		sentinel,
		context.DeadlineExceeded,
	)
	gotMultiOuter := sanitizeContentsURLError(multiOuterTok)
	if strings.Contains(gotMultiOuter.Error(), tok) {
		t.Fatalf("multi-error with outer token leaked token: %v", gotMultiOuter)
	}
	if !strings.Contains(gotMultiOuter.Error(), "colab-runtime-proxy-token=REDACTED") {
		t.Fatalf("multi-error with outer token missing REDACTED marker: %v", gotMultiOuter)
	}
	if !errors.Is(gotMultiOuter, context.DeadlineExceeded) || !errors.Is(gotMultiOuter, sentinel) {
		t.Fatalf("multi-error with outer token lost wrapped causes: %v", gotMultiOuter)
	}

	// 4. Wrapped non-*url.Error hiding a token inside its inner error (outer .Error() omits it).
	hiddenInner := &hiddenInnerWrapError{
		msg: "contents operation failed",
		err: fmt.Errorf("transport saw colab-runtime-proxy-token=%s: %w", tok, context.Canceled),
	}
	gotHiddenInner := sanitizeContentsURLError(hiddenInner)
	if gotHiddenInner.Error() != "contents operation failed" {
		t.Fatalf("unexpected outer message for hidden-inner error: %q", gotHiddenInner.Error())
	}
	unwrappedInner := errors.Unwrap(gotHiddenInner)
	if unwrappedInner == nil {
		t.Fatal("expected non-nil unwrapped error")
	}
	if strings.Contains(unwrappedInner.Error(), tok) {
		t.Fatalf("unwrapped inner error leaked proxy token: %v", unwrappedInner)
	}
	if !strings.Contains(unwrappedInner.Error(), "colab-runtime-proxy-token=REDACTED") {
		t.Fatalf("unwrapped inner error missing REDACTED marker: %v", unwrappedInner)
	}
	if !errors.Is(gotHiddenInner, context.Canceled) {
		t.Fatalf("wrapped error hiding inner token lost context.Canceled: %v", gotHiddenInner)
	}

	// 5. *url.Error.Op containing a proxy token is also redacted.
	opTokUErr := &url.Error{
		Op:  "Get colab-runtime-proxy-token=" + tok,
		URL: "https://rt.example.invalid/api/contents/a",
		Err: context.DeadlineExceeded,
	}
	gotOpTok := sanitizeContentsURLError(opTokUErr)
	if strings.Contains(gotOpTok.Error(), tok) {
		t.Fatalf("*url.Error.Op leaked proxy token: %v", gotOpTok)
	}
	var extractedOpUErr *url.Error
	if !errors.As(gotOpTok, &extractedOpUErr) || strings.Contains(extractedOpUErr.Op, tok) || !strings.Contains(extractedOpUErr.Op, "REDACTED") {
		t.Fatalf("extracted *url.Error.Op not redacted: %+v", extractedOpUErr)
	}

	// 6. Empty inner .Error() never interleaves redacted text between runes.
	flakyWrap := &hiddenInnerWrapError{
		msg: "outer-text",
		err: &flakyEmptyContentsError{first: "proxy colab-runtime-proxy-token=" + tok},
	}
	gotFlaky := sanitizeContentsURLError(flakyWrap)
	if gotFlaky.Error() != "outer-text" {
		t.Fatalf("empty inner .Error() corrupted outer text: got %q, want %q", gotFlaky.Error(), "outer-text")
	}
	if inner := errors.Unwrap(gotFlaky); inner == nil || strings.Contains(inner.Error(), tok) {
		t.Fatalf("flaky inner error leaked token or was dropped: %v", inner)
	}

	// 7. Cyclic error chain terminates safely at depth limit without stack overflow.
	cyclic := &hiddenInnerWrapError{msg: "cyclic colab-runtime-proxy-token=" + tok}
	cyclic.err = cyclic
	gotCyclic := sanitizeContentsURLError(cyclic)
	if strings.Contains(gotCyclic.Error(), tok) || !strings.Contains(gotCyclic.Error(), "REDACTED") {
		t.Fatalf("cyclic error chain failed redaction: %v", gotCyclic)
	}
}

func TestColabClientDefaultRefusesRedirectWithoutLeakingBearer(t *testing.T) {
	const oauthSecret = "oauth-bearer-secret-no-redirect-leak"

	var targetHits atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		t.Errorf("redirect target must not be contacted; received %s %s with Authorization=%q", r.Method, r.URL.String(), r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	colabSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertStandardColabHeaders(t, r, oauthSecret)
		http.Redirect(w, r, redirectTarget.URL+"/steal", http.StatusFound)
	}))
	defer colabSrv.Close()

	// Default client (no WithHTTPClient) must refuse 302 redirects.
	defaultClient := NewColabOneColabClient(
		func(ctx context.Context) (string, error) { return oauthSecret, nil },
		WithColabDomain(colabSrv.URL),
	)

	_, err := defaultClient.GetConsumptionUserInfo(context.Background())
	var reqErr *ColabOneRequestError
	if !errors.As(err, &reqErr) || reqErr.StatusCode != http.StatusFound {
		t.Fatalf("expected HTTP 302 ColabOneRequestError, got %v", err)
	}
	if hits := targetHits.Load(); hits != 0 {
		t.Fatalf("redirect target received %d request(s), want 0", hits)
	}

	req, err := http.NewRequest(http.MethodGet, colabSrv.URL+"/tun/m/assignments", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	resp, err := defaultClient.DoAuthorized(context.Background(), req)
	if err != nil {
		t.Fatalf("DoAuthorized returned error on 302 instead of last response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("DoAuthorized status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if loc := resp.Header.Get("Location"); loc != redirectTarget.URL+"/steal" {
		t.Fatalf("Location = %q, want %q", loc, redirectTarget.URL+"/steal")
	}
	if hits := targetHits.Load(); hits != 0 {
		t.Fatalf("redirect target received %d request(s) after DoAuthorized, want 0", hits)
	}

	// Custom http.Client with nil CheckRedirect also refuses redirects without mutating caller's client.
	customHTTP := &http.Client{Timeout: 5 * time.Second}
	wrappedClient := NewColabOneColabClient(
		func(ctx context.Context) (string, error) { return oauthSecret, nil },
		WithColabDomain(colabSrv.URL),
		WithHTTPClient(customHTTP),
	)
	if customHTTP.CheckRedirect != nil {
		t.Fatal("WithHTTPClient must not mutate caller's *http.Client")
	}
	_, err = wrappedClient.ListAssignments(context.Background())
	if !errors.As(err, &reqErr) || reqErr.StatusCode != http.StatusFound {
		t.Fatalf("expected HTTP 302 ColabOneRequestError with custom client, got %v", err)
	}
	if hits := targetHits.Load(); hits != 0 {
		t.Fatalf("redirect target received %d request(s) with custom client, want 0", hits)
	}

	// Explicit caller CheckRedirect policy is preserved.
	var customCheckCalled atomic.Bool
	sentinelRedirectErr := errors.New("custom redirect policy blocked")
	explicitHTTP := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			customCheckCalled.Store(true)
			return sentinelRedirectErr
		},
	}
	explicitClient := NewColabOneColabClient(
		func(ctx context.Context) (string, error) { return oauthSecret, nil },
		WithColabDomain(colabSrv.URL),
		WithHTTPClient(explicitHTTP),
	)
	_, err = explicitClient.ListAssignments(context.Background())
	if !errors.Is(err, sentinelRedirectErr) || !customCheckCalled.Load() {
		t.Fatalf("expected explicit caller CheckRedirect error, got err=%v called=%v", err, customCheckCalled.Load())
	}
	if hits := targetHits.Load(); hits != 0 {
		t.Fatalf("redirect target received %d request(s) with explicit CheckRedirect, want 0", hits)
	}
}

func TestContentsClientDefaultRefusesRedirectWithoutLeakingProxyToken(t *testing.T) {
	const proxySecret = "runtime-proxy-secret-no-referer-leak"

	var targetHits atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		t.Errorf("redirect target must not be contacted; received %s %s with Referer=%q", r.Method, r.URL.String(), r.Header.Get("Referer"))
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	contentsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("colab-runtime-proxy-token"); got != proxySecret {
			t.Errorf("unexpected proxy token on origin request: %q", got)
		}
		http.Redirect(w, r, redirectTarget.URL+"/steal", http.StatusFound)
	}))
	defer contentsSrv.Close()

	assertRedirectRefused := func(t *testing.T, op string, err error) {
		t.Helper()
		var reqErr *ColabOneRequestError
		if !errors.As(err, &reqErr) || reqErr.StatusCode != http.StatusFound {
			t.Fatalf("%s: expected HTTP 302 ColabOneRequestError, got %v", op, err)
		}
		if strings.Contains(reqErr.URL, proxySecret) || strings.Contains(err.Error(), proxySecret) {
			t.Fatalf("%s: error leaked proxy token: %v", op, err)
		}
		if hits := targetHits.Load(); hits != 0 {
			t.Fatalf("%s: redirect target received %d request(s), want 0", op, hits)
		}
	}

	// Default NewColabOneContentsClient(nil) refuses 302 redirects on all operations.
	defaultCC := NewColabOneContentsClient(nil)
	ctx := context.Background()

	_, err := defaultCC.DownloadBytes(ctx, contentsSrv.URL, proxySecret, "content/secret.txt")
	assertRedirectRefused(t, "default/DownloadBytes", err)

	_, err = defaultCC.ListDir(ctx, contentsSrv.URL, proxySecret, "content")
	assertRedirectRefused(t, "default/ListDir", err)

	err = defaultCC.UploadBytes(ctx, contentsSrv.URL, proxySecret, "content/up.bin", []byte("payload"), 5*time.Second)
	assertRedirectRefused(t, "default/UploadBytes", err)

	err = defaultCC.DeleteFile(ctx, contentsSrv.URL, proxySecret, "content/up.bin")
	assertRedirectRefused(t, "default/DeleteFile", err)

	// Zero-value ColabOneContentsClient also refuses 302 redirects.
	var zeroCC ColabOneContentsClient
	_, err = zeroCC.DownloadBytes(ctx, contentsSrv.URL, proxySecret, "content/zero.txt")
	assertRedirectRefused(t, "zero/DownloadBytes", err)

	// Custom http.Client with nil CheckRedirect also refuses redirects without mutating caller's client.
	customHTTP := &http.Client{Timeout: 5 * time.Second}
	customCC := NewColabOneContentsClient(customHTTP)
	if customHTTP.CheckRedirect != nil {
		t.Fatal("NewColabOneContentsClient must not mutate caller's *http.Client")
	}
	_, err = customCC.ListDir(ctx, contentsSrv.URL, proxySecret, "content")
	assertRedirectRefused(t, "custom/ListDir", err)
}

func TestCredentialStructsRedactTokensInStringAndGoString(t *testing.T) {
	const (
		proxyTok = "super-secret-runtime-proxy-token-42"
		xsrfTok  = "super-secret-xsrf-token-99"
	)

	assertRedacted := func(t *testing.T, label string, v any, secrets ...string) {
		t.Helper()
		formats := []string{"%v", "%+v", "%#v"}
		for _, format := range formats {
			out := fmt.Sprintf(format, v)
			for _, secret := range secrets {
				if secret != "" && strings.Contains(out, secret) {
					t.Fatalf("%s formatted with %s leaked secret %q: %s", label, format, secret, out)
				}
			}
			if len(secrets) > 0 && !strings.Contains(out, "[REDACTED]") {
				t.Fatalf("%s formatted with %s expected [REDACTED] marker, got: %s", label, format, out)
			}
		}
	}

	proxyInfo := ColabOneRuntimeProxyInfo{
		Token:                 proxyTok,
		TokenExpiresInSeconds: 3600,
		URL:                   "https://rt.example.invalid",
	}
	assertRedacted(t, "ColabOneRuntimeProxyInfo(value)", proxyInfo, proxyTok)
	assertRedacted(t, "ColabOneRuntimeProxyInfo(ptr)", &proxyInfo, proxyTok)
	if !strings.Contains(proxyInfo.String(), "https://rt.example.invalid") || !strings.Contains(proxyInfo.GoString(), "3600") {
		t.Fatalf("ColabOneRuntimeProxyInfo lost non-secret fields: String=%q GoString=%q", proxyInfo.String(), proxyInfo.GoString())
	}

	// Empty token omits [REDACTED] marker and nil pointer formats safely without panic.
	emptyProxyInfo := ColabOneRuntimeProxyInfo{URL: "https://rt.example.invalid"}
	if strings.Contains(emptyProxyInfo.String(), "[REDACTED]") || strings.Contains(emptyProxyInfo.GoString(), "[REDACTED]") {
		t.Fatalf("empty token should not report [REDACTED]: %q", emptyProxyInfo.String())
	}
	var nilProxyInfo *ColabOneRuntimeProxyInfo
	if got := fmt.Sprintf("%v %+v %#v", nilProxyInfo, nilProxyInfo, nilProxyInfo); !strings.Contains(got, "<nil>") {
		t.Fatalf("unexpected nil pointer format: %q", got)
	}

	// Accidental proxy token inside URL field is also redacted.
	proxyInfoWithTokenURL := ColabOneRuntimeProxyInfo{
		Token: proxyTok,
		URL:   "https://rt.example.invalid/api?colab-runtime-proxy-token=" + proxyTok,
	}
	assertRedacted(t, "ColabOneRuntimeProxyInfo(tokenInURL)", proxyInfoWithTokenURL, proxyTok)

	// ColabOneSessionState redacts Token across String/GoString/%v/%+v/%#v while preserving JSON marshaling.
	exp := time.Date(2026, 11, 23, 8, 15, 30, 0, time.UTC)
	sess := ColabOneSessionState{
		Name:           "colabone",
		Token:          proxyTok,
		URL:            "https://rt.example.invalid",
		Endpoint:       "m-s-ep1",
		TokenExpiresAt: &exp,
		Variant:        VariantGPU,
		Accelerator:    AcceleratorL4,
		MachineShape:   ShapeHighRAM,
		KernelID:       "kern-1",
		SessionID:      "sess-1",
	}
	assertRedacted(t, "ColabOneSessionState(value)", sess, proxyTok)
	assertRedacted(t, "ColabOneSessionState(ptr)", &sess, proxyTok)
	if !strings.Contains(sess.String(), "m-s-ep1") || !strings.Contains(sess.GoString(), "2026-11-23T08:15:30Z") {
		t.Fatalf("ColabOneSessionState lost non-secret fields: %q", sess.String())
	}
	sessNilExpiry := sess
	sessNilExpiry.TokenExpiresAt = nil
	assertRedacted(t, "ColabOneSessionState(nilExpiry)", sessNilExpiry, proxyTok)
	rawJSON, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("json.Marshal(sess) failed: %v", err)
	}
	if !strings.Contains(string(rawJSON), proxyTok) {
		t.Fatalf("json.Marshal(sess) must still persist Token for vault serialization: %s", string(rawJSON))
	}

	// ColabOneGetAssignmentResponse redacts both XSRF Token and nested RuntimeProxyInfo.Token.
	getRespXSRF := ColabOneGetAssignmentResponse{
		Acc:     "T4",
		Nbh:     "nbh-123",
		Token:   xsrfTok,
		Variant: VariantGPU,
	}
	assertRedacted(t, "ColabOneGetAssignmentResponse(xsrf)", getRespXSRF, xsrfTok)
	assertRedacted(t, "ColabOneGetAssignmentResponse(xsrfPtr)", &getRespXSRF, xsrfTok)

	getRespAssigned := ColabOneGetAssignmentResponse{
		Token:            xsrfTok,
		Endpoint:         "m-s-existing",
		Accelerator:      "L4",
		Variant:          VariantGPU,
		RuntimeProxyInfo: &proxyInfo,
	}
	assertRedacted(t, "ColabOneGetAssignmentResponse(assigned)", getRespAssigned, xsrfTok, proxyTok)
	assertRedacted(t, "ColabOneGetAssignmentResponse(assignedPtr)", &getRespAssigned, xsrfTok, proxyTok)

	// ColabOneAssignment and ColabOneListedAssignment never leak nested RuntimeProxyInfo.Token.
	assignment := ColabOneAssignment{
		Endpoint:         "m-s-assigned",
		Accelerator:      "T4",
		Variant:          VariantGPU,
		RuntimeProxyInfo: proxyInfo,
	}
	assertRedacted(t, "ColabOneAssignment(value)", assignment, proxyTok)
	assertRedacted(t, "ColabOneAssignment(ptr)", &assignment, proxyTok)

	listed := ColabOneListedAssignment{
		Accelerator:      "A100",
		Endpoint:         "m-s-listed",
		Variant:          VariantGPU,
		MachineShape:     ShapeHighRAM,
		RuntimeProxyInfo: proxyInfo,
	}
	assertRedacted(t, "ColabOneListedAssignment(value)", listed, proxyTok)
	assertRedacted(t, "ColabOneListedAssignment(ptr)", &listed, proxyTok)
}

func TestContentsClientUploadBytesMultiChunkAndBoundaries(t *testing.T) {
	const (
		sixMiB   = 6 * 1024 * 1024
		proxyTok = "multi-chunk-proxy-secret-7731"
	)

	makeDeterministicBytes := func(size int) []byte {
		buf := make([]byte, size)
		for i := range buf {
			buf[i] = byte((i*31 + i/257 + 7) & 0xff)
		}
		return buf
	}

	type capturedChunk struct {
		name    string
		path    string
		typ     string
		format  string
		content string
		chunk   int
	}

	assertIntSliceEqual := func(t *testing.T, label string, got, want []int) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: chunks = %v (len %d), want %v (len %d)", label, got, len(got), want, len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: chunks = %v, want %v", label, got, want)
			}
		}
	}

	t.Run("exact_6MiB_single_put_chunk_1", func(t *testing.T) {
		data := makeDeterministicBytes(sixMiB)
		var (
			mu       sync.Mutex
			captured []capturedChunk
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("expected PUT, got %s", r.Method)
			}
			var payload contentsUploadPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode PUT payload failed: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			captured = append(captured, capturedChunk{
				name:    payload.Name,
				path:    payload.Path,
				typ:     payload.Type,
				format:  payload.Format,
				content: payload.Content,
				chunk:   payload.Chunk,
			})
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		cc := NewColabOneContentsClient(srv.Client())
		if err := cc.UploadBytes(context.Background(), srv.URL, proxyTok, "content/exact6m.bin", data, 15*time.Second); err != nil {
			t.Fatalf("UploadBytes(6 MiB) failed: %v", err)
		}

		if len(captured) != 1 {
			t.Fatalf("expected exactly 1 PUT for exact 6 MiB boundary, got %d", len(captured))
		}
		got := captured[0]
		if got.chunk != 1 || got.typ != "file" || got.format != "base64" || got.name != "exact6m.bin" || got.path != "content/exact6m.bin" {
			t.Fatalf("unexpected metadata for exact 6 MiB upload: name=%q path=%q type=%q format=%q chunk=%d", got.name, got.path, got.typ, got.format, got.chunk)
		}
		if strings.Contains(got.content, "=") {
			t.Fatalf("exact 6 MiB chunk base64 should be unpadded (multiple of 3), got trailing padding")
		}
		decoded, err := base64.StdEncoding.DecodeString(got.content)
		if err != nil {
			t.Fatalf("base64 decode failed: %v", err)
		}
		if !bytes.Equal(decoded, data) {
			t.Fatalf("exact 6 MiB decoded bytes mismatch (got len %d, want %d)", len(decoded), len(data))
		}
	})

	t.Run("multi_chunk_13MiB_chunks_1_2_neg1_and_reassembly", func(t *testing.T) {
		data := makeDeterministicBytes(13 * 1024 * 1024)
		var (
			mu       sync.Mutex
			captured []capturedChunk
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("expected PUT, got %s", r.Method)
			}
			var payload contentsUploadPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode PUT payload failed: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			captured = append(captured, capturedChunk{
				name:    payload.Name,
				path:    payload.Path,
				typ:     payload.Type,
				format:  payload.Format,
				content: payload.Content,
				chunk:   payload.Chunk,
			})
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		cc := NewColabOneContentsClient(srv.Client())
		if err := cc.UploadBytes(context.Background(), srv.URL, proxyTok, "content/media/audio13m.wav", data, 30*time.Second); err != nil {
			t.Fatalf("UploadBytes(13 MiB) failed: %v", err)
		}

		gotChunks := make([]int, len(captured))
		var perChunkReassembled bytes.Buffer
		var concatB64 strings.Builder
		for i, c := range captured {
			gotChunks[i] = c.chunk
			if c.name != "audio13m.wav" || c.path != "content/media/audio13m.wav" || c.typ != "file" || c.format != "base64" {
				t.Fatalf("chunk[%d] metadata mismatch: name=%q path=%q type=%q format=%q", i, c.name, c.path, c.typ, c.format)
			}
			if c.chunk > 0 {
				if strings.Contains(c.content, "=") {
					t.Fatalf("non-final chunk %d must be cleanly unpadded base64", c.chunk)
				}
				if len(c.content) != (sixMiB/3)*4 {
					t.Fatalf("non-final chunk %d base64 len = %d, want %d", c.chunk, len(c.content), (sixMiB/3)*4)
				}
			}
			part, err := base64.StdEncoding.DecodeString(c.content)
			if err != nil {
				t.Fatalf("chunk[%d] base64 decode failed: %v", i, err)
			}
			perChunkReassembled.Write(part)
			concatB64.WriteString(c.content)
		}
		assertIntSliceEqual(t, "13 MiB chunks", gotChunks, []int{1, 2, -1})

		if !bytes.Equal(perChunkReassembled.Bytes(), data) {
			t.Fatalf("13 MiB per-chunk reassembled bytes mismatch (got len %d, want %d)", perChunkReassembled.Len(), len(data))
		}
		// Because all intermediate chunks are exact multiples of 3 (unpadded), concatenating
		// all base64 chunks directly before decoding must also yield byte-identical output.
		concatDecoded, err := base64.StdEncoding.DecodeString(concatB64.String())
		if err != nil {
			t.Fatalf("concatenated base64 decode failed: %v", err)
		}
		if !bytes.Equal(concatDecoded, data) {
			t.Fatalf("13 MiB concatenated base64 bytes mismatch (got len %d, want %d)", len(concatDecoded), len(data))
		}
	})

	t.Run("exact_12MiB_chunks_1_neg1_no_empty_final_chunk", func(t *testing.T) {
		data := makeDeterministicBytes(12 * 1024 * 1024)
		var (
			mu       sync.Mutex
			captured []capturedChunk
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload contentsUploadPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode PUT payload failed: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			captured = append(captured, capturedChunk{
				name:    payload.Name,
				path:    payload.Path,
				typ:     payload.Type,
				format:  payload.Format,
				content: payload.Content,
				chunk:   payload.Chunk,
			})
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		cc := NewColabOneContentsClient(srv.Client())
		if err := cc.UploadBytes(context.Background(), srv.URL, proxyTok, "content/exact12m.bin", data, 30*time.Second); err != nil {
			t.Fatalf("UploadBytes(12 MiB) failed: %v", err)
		}

		gotChunks := make([]int, len(captured))
		var reassembled bytes.Buffer
		for i, c := range captured {
			gotChunks[i] = c.chunk
			if len(c.content) == 0 {
				t.Fatalf("chunk[%d] (chunk=%d) must not be empty", i, c.chunk)
			}
			if strings.Contains(c.content, "=") {
				t.Fatalf("exact 12 MiB chunk[%d] should be cleanly unpadded base64", i)
			}
			part, err := base64.StdEncoding.DecodeString(c.content)
			if err != nil {
				t.Fatalf("chunk[%d] decode failed: %v", i, err)
			}
			if len(part) != sixMiB {
				t.Fatalf("chunk[%d] decoded len = %d, want %d", i, len(part), sixMiB)
			}
			reassembled.Write(part)
		}
		assertIntSliceEqual(t, "12 MiB chunks", gotChunks, []int{1, -1})
		if !bytes.Equal(reassembled.Bytes(), data) {
			t.Fatalf("12 MiB reassembled bytes mismatch (got len %d, want %d)", reassembled.Len(), len(data))
		}
	})

	t.Run("mid_upload_http_500_on_chunk_2_aborts_before_chunk_neg1", func(t *testing.T) {
		data := makeDeterministicBytes(13 * 1024 * 1024)
		var (
			mu           sync.Mutex
			seenChunks   []int
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload contentsUploadPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode PUT payload failed: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			seenChunks = append(seenChunks, payload.Chunk)
			mu.Unlock()
			if payload.Chunk == 2 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("disk full during chunk 2"))
				return
			}
			if payload.Chunk == -1 {
				t.Errorf("UploadBytes continued to chunk -1 after HTTP 500 on chunk 2")
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		cc := NewColabOneContentsClient(srv.Client())
		err := cc.UploadBytes(context.Background(), srv.URL, proxyTok, "content/failchunk2.bin", data, 30*time.Second)
		if err == nil {
			t.Fatal("expected HTTP 500 error on chunk 2, got nil")
		}
		var reqErr *ColabOneRequestError
		if !errors.As(err, &reqErr) || reqErr.StatusCode != http.StatusInternalServerError || reqErr.Method != http.MethodPut {
			t.Fatalf("expected HTTP 500 PUT ColabOneRequestError, got %v", err)
		}
		if !strings.Contains(reqErr.Body, "disk full during chunk 2") {
			t.Fatalf("expected error body to propagate, got %q", reqErr.Body)
		}
		if strings.Contains(reqErr.URL, proxyTok) || strings.Contains(err.Error(), proxyTok) {
			t.Fatalf("HTTP 500 error leaked proxy token: %v", err)
		}
		assertIntSliceEqual(t, "chunks before chunk-2 HTTP 500 abort", seenChunks, []int{1, 2})
	})
}
