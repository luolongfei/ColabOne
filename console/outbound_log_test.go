package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/controller"
	"github.com/luolongfei/ColabOne/console/internal/outboundlog"
	"github.com/luolongfei/ColabOne/console/internal/provider"
)

func readOutboundLogFile(t *testing.T, path string) []outboundlog.ColabOneOutboundRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open outbound log %s: %v", path, err)
	}
	defer f.Close()
	var out []outboundlog.ColabOneOutboundRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec outboundlog.ColabOneOutboundRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode outbound record %q: %v", line, err)
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan outbound log: %v", err)
	}
	return out
}

func TestWiredControllerCreatesBounded20MiBOutboundLogAndRecordsRPC(t *testing.T) {
	cfgDir := t.TempDir()
	logPath := filepath.Join(cfgDir, "logs", outboundlog.COLABONE_OUTBOUND_LOG_FILENAME)
	logger := outboundlog.NewColabOneOutboundLogger(logPath, outboundlog.COLABONE_OUTBOUND_LOG_MAX_BYTES)
	defer logger.Close()

	if logger.MaxTotalBytes() != 20*1024*1024 {
		t.Fatalf("expected 20 MiB cap, got %d", logger.MaxTotalBytes())
	}

	ctrl := newColabOneWiredControllerWithOutboundLog("", cfgDir, nil, logger, false)
	defer ctrl.Close()

	// Exercise drive_api outbound HTTP logging via wrapped client against local httptest server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	wrapped := outboundlog.WrapHTTPClient(srv.Client(), logger, "drive_api", "")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/drive/v3/files?fields=id&access_token=ya29.topsecret", nil)
	req.Header.Set("Authorization", "Bearer ya29.headersecret")
	resp, err := wrapped.Do(req)
	if err != nil {
		t.Fatalf("wrapped HTTP call failed: %v", err)
	}
	_ = resp.Body.Close()

	// Exercise remote_rpc outbound logging format (including drive/sync ok=false error capture).
	ev := provider.ColabOneOutboundRPCEvent{
		Method:   "POST",
		Path:     "/api/v1/drive/sync",
		Endpoint: "m-s-demo",
		Duration: 125 * time.Millisecond,
		OK:       false,
		Detail:   "Google 云盘同步失败：rateLimitExceeded",
	}
	logger.Record(outboundlog.ColabOneOutboundRecord{
		Component:  "remote_rpc",
		Protocol:   "rpc",
		Method:     ev.Method,
		Scheme:     "remote",
		Host:       ev.Endpoint,
		Path:       ev.Path,
		OK:         ev.OK,
		DurationMs: ev.Duration.Milliseconds(),
		Endpoint:   ev.Endpoint,
		Detail:     ev.Detail,
	})

	recs := readOutboundLogFile(t, logPath)
	if len(recs) != 2 {
		t.Fatalf("expected 2 outbound records, got %d", len(recs))
	}
	if recs[0].Component != "drive_api" || !recs[0].OK || strings.Contains(recs[0].Query, "ya29.topsecret") {
		t.Fatalf("unexpected HTTP record: %+v", recs[0])
	}
	if recs[1].Component != "remote_rpc" || recs[1].Path != "/api/v1/drive/sync" || recs[1].OK || !strings.Contains(recs[1].Detail, "rateLimitExceeded") {
		t.Fatalf("unexpected RPC record: %+v", recs[1])
	}
}

func TestDriveSyncMethodTimeoutIs300Seconds(t *testing.T) {
	if got := colabOneMethodTimeout("drive.sync"); got != 300*time.Second {
		t.Fatalf("colabOneMethodTimeout(drive.sync) = %v, want 300s", got)
	}
	// Verify controller SyncDrive compiles and checks sessions cleanly.
	ctrl := controller.NewColabOneController(controller.ColabOneControllerConfig{
		ConfigDir:  t.TempDir(),
		Background: false,
	})
	defer ctrl.Close()
	if _, err := ctrl.SyncDrive(context.Background()); err == nil {
		t.Fatalf("expected error when no GPU runtime session is active")
	}
}
