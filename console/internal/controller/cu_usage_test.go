package controller

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func almostEqualCU(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestColabOneCUUsageStoreContinuousAndBoundaryProration(t *testing.T) {
	dir := t.TempDir()
	now := 1763856000.0 // 2025-11-23 00:00:00 UTC (exact 1800s boundary)
	store := NewColabOneCUUsageStore(dir, func() float64 { return now })

	// Start 30s before the 00:30 UTC boundary, end 30s after -> 50/50 split across two 30m buckets.
	t0 := now + 1770.0
	t1 := now + 1830.0
	store.RecordSample("alice@example.com", t0, 100.00, true, 6.0, 1)
	store.RecordSample("alice@example.com", t1, 99.90, true, 6.0, 1)

	utcZero := 0
	res := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "alice@example.com",
		LoggedIn:     true,
		Start:        int64(now),
		End:          int64(now) + 3600,
		UTCOffsetSec: &utcZero,
	})

	buckets := asMapSlice(res["buckets"])
	if len(buckets) != 2 {
		t.Fatalf("expected 2 split buckets, got %d (%v)", len(buckets), buckets)
	}
	c0, _ := asFloat(buckets[0]["consumed"])
	c1, _ := asFloat(buckets[1]["consumed"])
	if !almostEqualCU(c0, 0.05, 1e-4) || !almostEqualCU(c1, 0.05, 1e-4) {
		t.Fatalf("expected 0.05/0.05 CU split, got %.4f and %.4f", c0, c1)
	}
	if !almostEqualCU(c0+c1, 0.10, 1e-4) {
		t.Fatalf("expected total 0.10 CU conserved, got %.4f", c0+c1)
	}
	be0, _ := asFloat(buckets[0]["balance_end"])
	be1, _ := asFloat(buckets[1]["balance_end"])
	if !almostEqualCU(be0, 99.95, 1e-4) || !almostEqualCU(be1, 99.90, 1e-4) {
		t.Fatalf("unexpected boundary balances: %.4f -> %.4f", be0, be1)
	}
	act0, _ := asFloat(buckets[0]["active_seconds"])
	act1, _ := asFloat(buckets[1]["active_seconds"])
	if !almostEqualCU(act0, 30, 0.2) || !almostEqualCU(act1, 30, 0.2) {
		t.Fatalf("expected 30s active in each bucket, got %.1f and %.1f", act0, act1)
	}
}

func TestColabOneCUUsageStoreTopupDetectionNoNegativeConsumption(t *testing.T) {
	dir := t.TempDir()
	base := 1763856000.0
	store := NewColabOneCUUsageStore(dir, func() float64 { return base + 60 })

	store.RecordSample("alice@example.com", base+10, 10.00, true, 3.6, 1)
	// 60 seconds later at 3.6 CU/h -> ~0.06 CU burned, plus 100 CU top-up -> net balance 109.94.
	store.RecordSample("alice@example.com", base+70, 109.94, true, 3.6, 1)

	res := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "alice@example.com",
		LoggedIn:     true,
		Start:        int64(base),
		End:          int64(base) + 1800,
	})
	buckets := asMapSlice(res["buckets"])
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	consumed, _ := asFloat(buckets[0]["consumed"])
	topup, _ := asFloat(buckets[0]["topup"])
	if !almostEqualCU(consumed, 0.06, 1e-3) {
		t.Fatalf("expected estimated consumption 0.06 CU during top-up interval, got %.4f", consumed)
	}
	if !almostEqualCU(topup, 100.0, 1e-2) {
		t.Fatalf("expected ~100.0 CU top-up recorded, got %.4f", topup)
	}
}

func TestColabOneCUUsageStoreOfflineGaps(t *testing.T) {
	dir := t.TempDir()
	base := 1763856000.0
	store := NewColabOneCUUsageStore(dir, func() float64 { return base + 200000 })

	// 1) 2-hour offline gap with 4.0 CU consumed -> evenly prorated across 4x 30m buckets (1.0 CU each, estimated=true).
	store.RecordSample("alice@example.com", base, 100.0, true, 2.0, 1)
	store.RecordSample("alice@example.com", base+7200, 96.0, true, 0.0, 0)

	// 2) 4-hour offline idle gap (no balance change) -> must not fabricate empty intermediate buckets.
	store.RecordSample("alice@example.com", base+7200+14400, 96.0, true, 0.0, 0)

	// 3) 48-hour offline gap (> 24h) -> resets baseline without dumping 15 CU into today's chart.
	store.RecordSample("alice@example.com", base+7200+14400+172800, 81.0, true, 0.0, 0)

	res2h := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "alice@example.com",
		LoggedIn:     true,
		Start:        int64(base),
		End:          int64(base) + 7200,
	})
	b2h := asMapSlice(res2h["buckets"])
	if len(b2h) != 4 {
		t.Fatalf("expected 4 prorated 30m buckets for 2h gap, got %d", len(b2h))
	}
	sum := 0.0
	for i, b := range b2h {
		c, _ := asFloat(b["consumed"])
		sum += c
		if !asBool(b["estimated"]) {
			t.Fatalf("expected bucket %d to be marked estimated", i)
		}
		// Intermediate offline buckets when one endpoint is idle (rate=0) must NOT carry fake peak rate/instances.
		if i > 0 {
			if rMax, _ := asFloat(b["rate_max"]); rMax != 0 {
				t.Fatalf("expected intermediate offline bucket %d rate_max=0 when idle on return, got %.4f", i, rMax)
			}
			if maxInst, _ := asInt(b["max_instances"]); maxInst != 0 {
				t.Fatalf("expected intermediate offline bucket %d max_instances=0 when idle on return, got %d", i, maxInst)
			}
		}
	}
	if !almostEqualCU(sum, 4.0, 1e-3) {
		t.Fatalf("expected prorated sum 4.0 CU, got %.4f", sum)
	}

	// Idle 4h span between base+9000 and base+21600 should have zero intermediate buckets.
	resIdle := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "alice@example.com",
		LoggedIn:     true,
		Start:        int64(base) + 9000,
		End:          int64(base) + 21600,
	})
	if len(asMapSlice(resIdle["buckets"])) != 0 {
		t.Fatalf("expected 0 intermediate buckets during idle offline gap, got %v", resIdle["buckets"])
	}

	// After >24h gap, final bucket records balance 81.0 with 0 consumed.
	longTS := int64(base) + 7200 + 14400 + 172800
	resLong := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "alice@example.com",
		LoggedIn:     true,
		Start:        longTS,
		End:          longTS + 1800,
	})
	bLong := asMapSlice(resLong["buckets"])
	if len(bLong) != 1 {
		t.Fatalf("expected 1 baseline bucket after >24h gap, got %d", len(bLong))
	}
	if c, _ := asFloat(bLong[0]["consumed"]); c != 0 {
		t.Fatalf("expected 0 consumed after >24h gap baseline reset, got %.4f", c)
	}
	if be, _ := asFloat(bLong[0]["balance_end"]); !almostEqualCU(be, 81.0, 1e-4) {
		t.Fatalf("expected balance_end 81.0, got %.4f", be)
	}
}

func TestColabOneCUUsageStoreAccountIsolationPrivacyAndPersistence(t *testing.T) {
	dir := t.TempDir()
	base := 1763856000.0
	store := NewColabOneCUUsageStore(dir, func() float64 { return base + 120 })

	// Early sample before profile email resolves migrates smoothly into Alice's hashed account.
	store.RecordSample("", base, 200.0, true, 1.5, 1)
	store.RecordSample("Alice@Example.com", base+60, 199.5, true, 1.5, 1)

	// Switch to Bob with lower balance: must NOT record 180 CU consumption!
	store.ResetActiveAccount()
	store.RecordSample("bob@example.com", base+90, 20.0, true, 1.0, 1)
	store.RecordSample("bob@example.com", base+150, 19.8, true, 1.0, 1)

	if err := store.Flush(); err != nil {
		t.Fatalf("flush failed: %v", err)
	}

	// Verify no plaintext emails exist on disk.
	raw, err := os.ReadFile(filepath.Join(dir, colabOneCUUsageFilename))
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	text := strings.ToLower(string(raw))
	if strings.Contains(text, "alice@example.com") || strings.Contains(text, "bob@example.com") {
		t.Fatalf("persisted cu_usage.json must not contain plaintext email addresses")
	}

	// Reload from disk and verify isolated histories + initial_balance carry-forward.
	reloaded := NewColabOneCUUsageStore(dir, func() float64 { return base + 7200 })

	aliceRes := reloaded.Query(ColabOneCUUsageQuery{
		AccountEmail: "alice@example.com",
		LoggedIn:     true,
		Start:        int64(base),
		End:          int64(base) + 1800,
	})
	aliceBuckets := asMapSlice(aliceRes["buckets"])
	if len(aliceBuckets) != 1 {
		t.Fatalf("expected 1 bucket for Alice, got %d", len(aliceBuckets))
	}
	if c, _ := asFloat(aliceBuckets[0]["consumed"]); !almostEqualCU(c, 0.5, 1e-4) {
		t.Fatalf("expected Alice consumed 0.50 CU, got %.4f", c)
	}

	bobRes := reloaded.Query(ColabOneCUUsageQuery{
		AccountEmail: "bob@example.com",
		LoggedIn:     true,
		Start:        int64(base),
		End:          int64(base) + 1800,
	})
	bobBuckets := asMapSlice(bobRes["buckets"])
	if len(bobBuckets) != 1 {
		t.Fatalf("expected 1 bucket for Bob, got %d", len(bobBuckets))
	}
	if c, _ := asFloat(bobBuckets[0]["consumed"]); !almostEqualCU(c, 0.2, 1e-4) {
		t.Fatalf("expected Bob consumed 0.20 CU, got %.4f", c)
	}

	// Query a later window with no new samples: initial_balance should carry Bob's last balance 19.8.
	bobLater := reloaded.Query(ColabOneCUUsageQuery{
		AccountEmail: "bob@example.com",
		LoggedIn:     true,
		Start:        int64(base) + 3600,
		End:          int64(base) + 7200,
	})
	initBal, ok := asFloat(bobLater["initial_balance"])
	if !ok || !almostEqualCU(initBal, 19.8, 1e-4) {
		t.Fatalf("expected initial_balance 19.8, got %v (ok=%v)", bobLater["initial_balance"], ok)
	}

	// Half-hour timezone offset (+05:30 = +19800s) local date computation.
	istOff := 5*3600 + 1800
	istRes := reloaded.Query(ColabOneCUUsageQuery{
		AccountEmail: "bob@example.com",
		LoggedIn:     true,
		Start:        int64(base),
		End:          int64(base) + 1800,
		UTCOffsetSec: &istOff,
	})
	activeDates := asAnySlice(istRes["active_dates"])
	if len(activeDates) != 1 || asString(activeDates[0]) != "2025-11-23" {
		t.Fatalf("unexpected IST active_dates: %v", activeDates)
	}
}

func TestColabOneCUUsageStoreBackwardsClockDuplicatesAndPruning(t *testing.T) {
	dir := t.TempDir()
	base := 1763856000.0
	store := NewColabOneCUUsageStore(dir, func() float64 { return base + 400*86400 })

	store.RecordSample("alice@example.com", base, 100.0, true, 2.0, 1)
	// Duplicate identical sample -> ignored.
	store.RecordSample("alice@example.com", base, 100.0, true, 2.0, 1)
	// Backwards clock sample -> ignored.
	store.RecordSample("alice@example.com", base-120, 50.0, true, 2.0, 1)
	// Valid next sample.
	store.RecordSample("alice@example.com", base+60, 99.7, true, 2.0, 1)

	res := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "alice@example.com",
		LoggedIn:     true,
		Start:        int64(base) - 1800,
		End:          int64(base) + 1800,
	})
	buckets := asMapSlice(res["buckets"])
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket after duplicate & backwards clock filtering, got %d", len(buckets))
	}
	if c, _ := asFloat(buckets[0]["consumed"]); !almostEqualCU(c, 0.3, 1e-4) {
		t.Fatalf("expected 0.30 CU, got %.4f", c)
	}

	// Recording a sample 400 days later should prune buckets older than 365 days.
	futureTS := base + 400*86400
	store.RecordSample("alice@example.com", futureTS, 90.0, true, 0.0, 0)
	resOld := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "alice@example.com",
		LoggedIn:     true,
		Start:        int64(base) - 1800,
		End:          int64(base) + 1800,
	})
	if len(asMapSlice(resOld["buckets"])) != 0 {
		t.Fatalf("expected old bucket (>365d) to be pruned, got %v", resOld["buckets"])
	}
}

func TestControllerBillingUsageCommandIntegration(t *testing.T) {
	h := newTestHarness(t, false)
	base := 1763856000.0

	// Logged out -> empty buckets, logged_in=false.
	outLoggedOut, err := h.ctrl.Command(context.Background(), "billing.usage", map[string]any{
		"start": int64(base),
		"end":   int64(base) + 86400,
	})
	if err != nil {
		t.Fatalf("billing.usage logged-out failed: %v", err)
	}
	mOut := asMap(outLoggedOut)
	if asBool(mOut["logged_in"]) {
		t.Fatalf("expected logged_in=false when signed out")
	}
	if len(asMapSlice(mOut["buckets"])) != 0 {
		t.Fatalf("expected 0 buckets when signed out")
	}

	// Record two account polls via controller.Update -> automatically captured by cuUsage store.
	h.ctrl.Update(map[string]any{
		"account": map[string]any{
			"logged_in":   true,
			"email":       "tester@example.com",
			"balance":     120.00,
			"rate":        1.96,
			"assignments": 1,
			"checked_at":  base + 300,
		},
	})
	h.ctrl.Update(map[string]any{
		"account": map[string]any{
			"logged_in":   true,
			"email":       "tester@example.com",
			"balance":     119.51,
			"rate":        1.96,
			"assignments": 1,
			"checked_at":  base + 1200,
		},
	})

	outLoggedIn, err := h.ctrl.Command(context.Background(), "billing.usage", map[string]any{
		"start":              int64(base),
		"end":                int64(base) + 3600,
		"utc_offset_seconds": 0,
	})
	if err != nil {
		t.Fatalf("billing.usage logged-in failed: %v", err)
	}
	mIn := asMap(outLoggedIn)
	if !asBool(mIn["logged_in"]) {
		t.Fatalf("expected logged_in=true")
	}
	buckets := asMapSlice(mIn["buckets"])
	if len(buckets) != 1 {
		t.Fatalf("expected 1 usage bucket, got %d (%v)", len(buckets), buckets)
	}
	if c, _ := asFloat(buckets[0]["consumed"]); !almostEqualCU(c, 0.49, 1e-4) {
		t.Fatalf("expected 0.49 CU consumed, got %.4f", c)
	}
	if curBal, _ := asFloat(mIn["current_balance"]); !almostEqualCU(curBal, 119.51, 1e-4) {
		t.Fatalf("expected current_balance 119.51, got %.4f", curBal)
	}
}

func TestColabOneCUUsageStoreNilSafetyCorruptPreservationAndTransitions(t *testing.T) {
	// 1) Nil receiver Query must never panic.
	var nilStore *ColabOneCUUsageStore
	nilRes := nilStore.Query(ColabOneCUUsageQuery{LoggedIn: true})
	if len(asMapSlice(nilRes["buckets"])) != 0 {
		t.Fatalf("expected empty buckets for nil store, got %v", nilRes["buckets"])
	}

	// 2) Corrupt cu_usage.json must be preserved as cu_usage.json.corrupt instead of silently overwritten.
	dir := t.TempDir()
	corruptRaw := []byte(`{"version":1,"accounts":{BROKEN`)
	if err := os.WriteFile(filepath.Join(dir, colabOneCUUsageFilename), corruptRaw, 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	base := 1763856000.0
	store := NewColabOneCUUsageStore(dir, func() float64 { return base + 60 })
	backupRaw, err := os.ReadFile(filepath.Join(dir, colabOneCUUsageFilename+".corrupt"))
	if err != nil {
		t.Fatalf("expected corrupt backup file to be preserved: %v", err)
	}
	if string(backupRaw) != string(corruptRaw) {
		t.Fatalf("corrupt backup contents mismatch: %q", string(backupRaw))
	}

	// 3) Online transition across 10-minute sleep (dt=600s > 120s) with zero balance delta:
	// must NOT attribute 600s of phantom active time or stamp fake rate onto the wake bucket.
	store.RecordSample("alice@example.com", base+1700, 100.0, true, 2.0, 1)
	store.RecordSample("alice@example.com", base+2300, 100.0, true, 0.0, 0)
	resSleep := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "alice@example.com",
		LoggedIn:     true,
		Start:        int64(base) + 1800,
		End:          int64(base) + 3600,
	})
	bSleep := asMapSlice(resSleep["buckets"])
	if len(bSleep) != 1 {
		t.Fatalf("expected 1 wake bucket, got %d", len(bSleep))
	}
	if act, _ := asFloat(bSleep[0]["active_seconds"]); act != 0 {
		t.Fatalf("expected 0 phantom active_seconds on 10m idle-stop sleep, got %.1f", act)
	}
	if rMax, _ := asFloat(bSleep[0]["rate_max"]); rMax != 0 {
		t.Fatalf("expected 0 rate_max on wake bucket after stop, got %.4f", rMax)
	}

	// 4) Short poll transition (dt=60s) with 0.02 CU consumed at 2.0 CU/h -> active_seconds = 36s, rate_avg = 2.0.
	tStart := base + 3600
	store.RecordSample("bob@example.com", tStart+10, 50.0, true, 2.0, 1)
	store.RecordSample("bob@example.com", tStart+70, 49.98, true, 0.0, 0)
	resTrans := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "bob@example.com",
		LoggedIn:     true,
		Start:        int64(tStart),
		End:          int64(tStart) + 1800,
	})
	bTrans := asMapSlice(resTrans["buckets"])
	if len(bTrans) != 1 {
		t.Fatalf("expected 1 transition bucket, got %d", len(bTrans))
	}
	if act, _ := asFloat(bTrans[0]["active_seconds"]); !almostEqualCU(act, 36.0, 0.5) {
		t.Fatalf("expected ~36s active_seconds from diff/rate, got %.1f", act)
	}
	if rAvg, _ := asFloat(bTrans[0]["rate_avg"]); !almostEqualCU(rAvg, 2.0, 1e-3) {
		t.Fatalf("expected rate_avg 2.0 weighted by active_seconds, got %.4f", rAvg)
	}
}

func TestColabOneCUUsageStoreDSTAwareTimezoneDatesAndColdStartProfile(t *testing.T) {
	dir := t.TempDir()
	// 2025-07-15 07:15:00 UTC is 2025-07-15 00:15:00 PDT (-07:00) in America/Los_Angeles,
	// whereas a fixed winter PST (-08:00) offset would wrongly place it on 2025-07-14 23:15!
	summerTS := 1752563700.0 // 2025-07-15 07:15:00 UTC
	store := NewColabOneCUUsageStore(dir, func() float64 { return summerTS + 60 })
	store.RecordSample("dst@example.com", summerTS, 80.0, true, 2.0, 1)
	store.RecordSample("dst@example.com", summerTS+60, 79.9, true, 2.0, 1)

	resLA := store.Query(ColabOneCUUsageQuery{
		AccountEmail: "dst@example.com",
		LoggedIn:     true,
		Start:        int64(summerTS) - 3600,
		End:          int64(summerTS) + 3600,
		Timezone:     "America/Los_Angeles",
	})
	activeLA := asAnySlice(resLA["active_dates"])
	if len(activeLA) != 1 || asString(activeLA[0]) != "2025-07-15" {
		t.Fatalf("expected DST-aware America/Los_Angeles date 2025-07-15, got %v", activeLA)
	}

	// Cold-start billing.usage resolves email from vault google_profile before first Account() poll.
	h := newTestHarness(t, false)
	if err := h.vault.Set("colab", map[string]any{"access_token": "tok"}); err != nil {
		t.Fatalf("set colab creds: %v", err)
	}
	if err := h.vault.Set("google_profile", map[string]any{"email": "cold@example.com", "sub": "sub-cold"}); err != nil {
		t.Fatalf("set google_profile: %v", err)
	}
	h.ctrl.cuUsage.RecordSample("cold@example.com", summerTS, 50.0, true, 1.5, 1)
	h.ctrl.cuUsage.RecordSample("cold@example.com", summerTS+60, 49.75, true, 1.5, 1)
	h.ctrl.cuUsage.ResetActiveAccount()

	outCold, err := h.ctrl.Command(context.Background(), "billing.usage", map[string]any{
		"start":    int64(summerTS) - 1800,
		"end":      int64(summerTS) + 1800,
		"timezone": "UTC",
	})
	if err != nil {
		t.Fatalf("cold-start billing.usage failed: %v", err)
	}
	bCold := asMapSlice(asMap(outCold)["buckets"])
	if len(bCold) != 1 {
		t.Fatalf("expected 1 bucket resolved via vault google_profile on cold start, got %d", len(bCold))
	}
	if c, _ := asFloat(bCold[0]["consumed"]); !almostEqualCU(c, 0.25, 1e-4) {
		t.Fatalf("expected 0.25 CU on cold-start query, got %.4f", c)
	}
}
