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

func TestColabOneCUUsageStoreCurrentRunLifecycleAndSettlement(t *testing.T) {
	dir := t.TempDir()
	base := 1763856000.0
	store := NewColabOneCUUsageStore(dir, func() float64 { return base + 600 })

	// 1) Initial idle sample -> no current run yet.
	store.RecordSample("alice@example.com", base, 100.00, true, 0.0, 0)
	if snap := store.CurrentRunSnapshot("alice@example.com"); snap != nil {
		t.Fatalf("expected nil current_run when idle with no prior runs, got %v", snap)
	}

	// 2) BeginRun right at GPU allocation (base+10), capturing pre-start balance 100.00.
	store.BeginRun("alice@example.com", base+10, 0, false)
	snap := store.CurrentRunSnapshot("alice@example.com")
	if snap == nil || !asBool(snap["active"]) {
		t.Fatalf("expected active current_run after BeginRun, got %v", snap)
	}
	if sb, _ := asFloat(snap["start_balance"]); !almostEqualCU(sb, 100.00, 1e-4) {
		t.Fatalf("expected start_balance 100.00, got %.4f", sb)
	}

	// An older in-flight idle poll timestamped BEFORE BeginRun must NOT close the new run.
	store.RecordSample("alice@example.com", base+5, 100.00, true, 0.0, 0)
	if snap2 := store.CurrentRunSnapshot("alice@example.com"); snap2 == nil || !asBool(snap2["active"]) {
		t.Fatalf("pre-allocation idle sample must not close newly started run: %v", snap2)
	}

	// 3) First active poll (base+40): rate=3.60 CU/hr, Google balance hasn't ticked yet (still 100.00).
	// Unsettled bridge should cover (base+40 - base+10) = 30s -> 0.03 CU without jumping back to 0.
	store.RecordSample("alice@example.com", base+40, 100.00, true, 3.60, 1)
	snap = store.CurrentRunSnapshot("alice@example.com")
	if !asBool(snap["active"]) {
		t.Fatalf("expected run still active")
	}
	if c, _ := asFloat(snap["consumed"]); !almostEqualCU(c, 0.0, 1e-4) {
		t.Fatalf("expected confirmed consumed 0.0 before Google ticks balance, got %.4f", c)
	}
	if u, _ := asFloat(snap["unsettled"]); !almostEqualCU(u, 0.03, 1e-3) {
		t.Fatalf("expected unsettled bridge ~0.03 CU for 30s @ 3.60 CU/h, got %.4f", u)
	}

	// Calling BeginRun while already active (e.g. adding a 2nd GPU) must NOT reset the run.
	store.BeginRun("alice@example.com", base+50, 99.95, true)
	if st, _ := asFloat(store.CurrentRunSnapshot("alice@example.com")["started_at"]); !almostEqualCU(st, base+10, 1e-4) {
		t.Fatalf("BeginRun must preserve active run started_at, got %.2f", st)
	}

	// 4) Next poll (base+70): Google ticks balance down to 99.94 (-0.06 CU) with 2 instances @ 7.20 CU/hr.
	// Confirmed becomes 0.06 and unsettled resets cleanly to 0.
	store.RecordSample("alice@example.com", base+70, 99.94, true, 7.20, 2)
	snap = store.CurrentRunSnapshot("alice@example.com")
	if c, _ := asFloat(snap["consumed"]); !almostEqualCU(c, 0.06, 1e-4) {
		t.Fatalf("expected confirmed consumed 0.06 CU, got %.4f", c)
	}
	if u, _ := asFloat(snap["unsettled"]); !almostEqualCU(u, 0.0, 1e-4) {
		t.Fatalf("expected unsettled reset to 0 after balance drop, got %.4f", u)
	}
	if mi, _ := asInt(snap["max_instances"]); mi != 2 {
		t.Fatalf("expected max_instances=2, got %d", mi)
	}
	if pr, _ := asFloat(snap["peak_rate"]); !almostEqualCU(pr, 7.20, 1e-4) {
		t.Fatalf("expected peak_rate=7.20, got %.4f", pr)
	}

	// 5) Mid-run top-up (+100 CU) at base+130 (60s @ 7.20 CU/hr -> 0.12 CU burned, net balance 199.82).
	store.RecordSample("alice@example.com", base+130, 199.82, true, 7.20, 2)
	snap = store.CurrentRunSnapshot("alice@example.com")
	if c, _ := asFloat(snap["consumed"]); !almostEqualCU(c, 0.18, 1e-3) {
		t.Fatalf("expected confirmed consumed ~0.18 CU across top-up, got %.4f", c)
	}
	if tp, _ := asFloat(snap["topup"]); !almostEqualCU(tp, 100.0, 1e-2) {
		t.Fatalf("expected topup ~100.0 CU, got %.4f", tp)
	}

	// 6) Reload from disk mid-run -> active run must survive client restart intact!
	if err := store.Flush(); err != nil {
		t.Fatalf("flush failed: %v", err)
	}
	reloaded := NewColabOneCUUsageStore(dir, func() float64 { return base + 600 })
	rSnap := reloaded.CurrentRunSnapshot("alice@example.com")
	if rSnap == nil || !asBool(rSnap["active"]) {
		t.Fatalf("expected active run persisted across reload, got %v", rSnap)
	}
	if c, _ := asFloat(rSnap["consumed"]); !almostEqualCU(c, 0.18, 1e-3) {
		t.Fatalf("expected reloaded consumed 0.18 CU, got %.4f", c)
	}

	// 7) Stop all instances at base+190 (balance drops 0.12 CU to 199.70, rate=0, assignments=0).
	reloaded.RecordSample("alice@example.com", base+190, 199.70, true, 0.0, 0)
	doneSnap := reloaded.CurrentRunSnapshot("alice@example.com")
	if doneSnap == nil || asBool(doneSnap["active"]) {
		t.Fatalf("expected completed (active=false) run preserved after stop, got %v", doneSnap)
	}
	if c, _ := asFloat(doneSnap["consumed"]); !almostEqualCU(c, 0.30, 1e-3) {
		t.Fatalf("expected final consumed 0.30 CU, got %.4f", c)
	}
	if ended, _ := asFloat(doneSnap["ended_at"]); !almostEqualCU(ended, base+190, 1e-4) {
		t.Fatalf("expected ended_at %.0f, got %.2f", base+190, ended)
	}

	// 8) Delayed Google settlement 30s after stop (balance drops final 0.02 CU to 199.68).
	reloaded.RecordSample("alice@example.com", base+220, 199.68, true, 0.0, 0)
	settledSnap := reloaded.CurrentRunSnapshot("alice@example.com")
	if c, _ := asFloat(settledSnap["consumed"]); !almostEqualCU(c, 0.32, 1e-3) {
		t.Fatalf("expected trailing settlement folded into completed run (0.32 CU), got %.4f", c)
	}

	// 9) Unrelated balance drop 10 minutes later (>5m window) must NOT alter the completed run.
	reloaded.RecordSample("alice@example.com", base+900, 199.50, true, 0.0, 0)
	unchangedSnap := reloaded.CurrentRunSnapshot("alice@example.com")
	if c, _ := asFloat(unchangedSnap["consumed"]); !almostEqualCU(c, 0.32, 1e-3) {
		t.Fatalf("expected late idle drop outside 5m window ignored (0.32 CU), got %.4f", c)
	}

	// 10) Starting a brand-new run later resets cleanly to 0 for the new run.
	reloaded.RecordSample("alice@example.com", base+1200, 199.50, true, 1.96, 1)
	nextSnap := reloaded.CurrentRunSnapshot("alice@example.com")
	if !asBool(nextSnap["active"]) {
		t.Fatalf("expected new active run")
	}
	if c, _ := asFloat(nextSnap["consumed"]); !almostEqualCU(c, 0.0, 1e-4) {
		t.Fatalf("expected fresh run starting at 0.00 CU, got %.4f", c)
	}

	// 11) Account isolation: another user never sees Alice's current_run.
	if bobSnap := reloaded.CurrentRunSnapshot("bob@example.com"); bobSnap != nil {
		t.Fatalf("expected nil current_run for bob, got %v", bobSnap)
	}
}

func TestControllerSnapshotExposesCurrentRunOnlyWhenSignedIn(t *testing.T) {
	h := newTestHarness(t, false)
	base := 1763856000.0

	// Signed out -> no current_run exposed.
	snapOut := h.ctrl.Snapshot()
	if _, hasRun := asMap(snapOut["account"])["current_run"]; hasRun {
		t.Fatalf("expected no current_run when signed out")
	}

	// Signed in with two active polls -> account.current_run exposed in Snapshot().
	h.ctrl.Update(map[string]any{
		"account": map[string]any{
			"logged_in":   true,
			"email":       "runner@example.com",
			"balance":     80.00,
			"rate":        2.40,
			"assignments": 1,
			"checked_at":  base,
		},
	})
	h.ctrl.Update(map[string]any{
		"account": map[string]any{
			"logged_in":   true,
			"email":       "runner@example.com",
			"balance":     79.60,
			"rate":        2.40,
			"assignments": 1,
			"checked_at":  base + 600,
		},
	})

	snapIn := h.ctrl.Snapshot()
	runMap := asMap(asMap(snapIn["account"])["current_run"])
	if runMap == nil || !asBool(runMap["active"]) {
		t.Fatalf("expected active account.current_run in Snapshot(), got %v", snapIn["account"])
	}
	if c, _ := asFloat(runMap["consumed"]); !almostEqualCU(c, 0.40, 1e-4) {
		t.Fatalf("expected 0.40 CU consumed in Snapshot(), got %.4f", c)
	}
}

func TestColabOneCUUsageStoreBeginRunIgnoresPreRunOfflineDropAndColdStartBridges(t *testing.T) {
	dir := t.TempDir()
	base := 1763856000.0
	store := NewColabOneCUUsageStore(dir, func() float64 { return base + 2000 })

	// 1) Idle anchor at base (100.00 CU), then 20-minute sleep during which an external notebook spent 1.00 CU.
	// User wakes laptop and starts GPU at base+1200 with verified pre-start balance 99.00 CU.
	store.RecordSample("sleep@example.com", base, 100.00, true, 0.0, 0)
	store.BeginRun("sleep@example.com", base+1200, 99.00, true)
	store.RecordSample("sleep@example.com", base+1230, 98.97, true, 3.60, 1)

	snap := store.CurrentRunSnapshot("sleep@example.com")
	if snap == nil || !asBool(snap["active"]) {
		t.Fatalf("expected active run after wake start, got %v", snap)
	}
	if c, _ := asFloat(snap["consumed"]); !almostEqualCU(c, 0.03, 1e-4) {
		t.Fatalf("expected 0.03 CU for new run (not 1.03 CU including pre-run offline drop), got %.4f", c)
	}
	if sb, _ := asFloat(snap["start_balance"]); !almostEqualCU(sb, 99.00, 1e-4) {
		t.Fatalf("expected start_balance 99.00, got %.4f", sb)
	}

	// 2) Same 20-minute stale anchor without verified start_balance -> cleanly anchors to current balance (0.00 CU).
	store.RecordSample("nobal@example.com", base, 100.00, true, 0.0, 0)
	store.BeginRun("nobal@example.com", base+1200, 0, false)
	store.RecordSample("nobal@example.com", base+1230, 98.50, true, 3.60, 1)
	snapNoBal := store.CurrentRunSnapshot("nobal@example.com")
	if c, _ := asFloat(snapNoBal["consumed"]); !almostEqualCU(c, 0.0, 1e-4) {
		t.Fatalf("expected 0.00 CU when stale anchor predates BeginRun without start_balance, got %.4f", c)
	}
	if sb, _ := asFloat(snapNoBal["start_balance"]); !almostEqualCU(sb, 98.50, 1e-4) {
		t.Fatalf("expected start_balance anchored at 98.50, got %.4f", sb)
	}

	// 3) Brand-new cold install (no prior anchor): BeginRun at base+10, first active sample at base+40 with unchanged balance -> bridges 30s.
	store.BeginRun("cold@example.com", base+10, 50.00, true)
	store.RecordSample("cold@example.com", base+40, 50.00, true, 3.60, 1)
	snapCold := store.CurrentRunSnapshot("cold@example.com")
	if u, _ := asFloat(snapCold["unsettled"]); !almostEqualCU(u, 0.03, 1e-3) {
		t.Fatalf("expected 0.03 CU unsettled bridge on cold install first sample, got %.4f", u)
	}
}
