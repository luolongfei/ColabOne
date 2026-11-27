package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

const (
	colabOneCUUsageSchemaVersion     = 1
	colabOneCUBucketSeconds          = 1800 // 30-minute UTC buckets align cleanly with all timezones including :30 offsets
	colabOneCURetentionDays          = 365
	colabOneCUMaxBucketsPerAccount   = 20000
	colabOneCUOnlineGapMaxSec        = 900.0   // 15 minutes
	colabOneCUOfflineProrateMaxSec   = 86400.0 // 24 hours
	colabOneCUFlushMinIntervalSec    = 60.0
	colabOneCUEpsilon                = 1e-6
	colabOneCUMaxQueryRangeSeconds   = 366 * 86400
	colabOneCUMaxValidUnixSec        = 4102444800.0 // 2100-01-01T00:00:00Z
	colabOneCUMaxEstimateElapsedSec  = 30 * 86400.0 // 30 days max single-output window
	colabOneCUUsageFilename          = "cu_usage.json"
	colabOneCUDefaultAccountKey       = "default"
	colabOneCUMigrateDefaultMaxGapS   = 600.0
	colabOneCURunUnsettledCapSec      = 180.0 // cap unsettled projection at 3 minutes when Google balance hasn't ticked yet
	colabOneCURunDelayedSettleMaxSec  = 300.0 // fold trailing Google settlement within 5 minutes of stop
)

// ColabOneCUUsageBucket stores aggregated CU consumption and balance telemetry
// for one 30-minute UTC window [TS, TS+1800).
type ColabOneCUUsageBucket struct {
	TS           int64   `json:"ts"`
	Consumed     float64 `json:"consumed"`
	Topup        float64 `json:"topup,omitempty"`
	BalanceStart float64 `json:"balance_start,omitempty"`
	BalanceEnd   float64 `json:"balance_end,omitempty"`
	HasBalance   bool    `json:"has_balance,omitempty"`
	RateSum      float64 `json:"rate_sum,omitempty"`
	RateSec      float64 `json:"rate_sec,omitempty"`
	RateMax      float64 `json:"rate_max,omitempty"`
	ActiveSec    float64 `json:"active_sec,omitempty"`
	MaxInstances int     `json:"max_instances,omitempty"`
	Samples      int     `json:"samples"`
	Estimated    bool    `json:"estimated,omitempty"`
}

// ColabOneCUUsageAnchor remembers the last observed account poll so consecutive
// balance deltas and active durations can be computed accurately across restarts.
type ColabOneCUUsageAnchor struct {
	TS          float64 `json:"ts"`
	Balance     float64 `json:"balance"`
	HasBalance  bool    `json:"has_balance"`
	Rate        float64 `json:"rate"`
	Assignments int     `json:"assignments"`
}

// ColabOneCUUsageRun tracks one continuous billing run for a Google account,
// persisted locally alongside bucket history so restarts preserve in-flight and
// last-completed run totals.
type ColabOneCUUsageRun struct {
	Active          bool    `json:"active"`
	StartedAt       float64 `json:"started_at"`
	EndedAt         float64 `json:"ended_at,omitempty"`
	StartBalance    float64 `json:"start_balance,omitempty"`
	HasStartBalance bool    `json:"has_start_balance,omitempty"`
	EndBalance      float64 `json:"end_balance,omitempty"`
	HasEndBalance   bool    `json:"has_end_balance,omitempty"`
	Consumed        float64 `json:"consumed"`
	Unsettled       float64 `json:"unsettled,omitempty"`
	Topup           float64 `json:"topup,omitempty"`
	LastSampleAt    float64 `json:"last_sample_at"`
	LastRate        float64 `json:"last_rate,omitempty"`
	PeakRate        float64 `json:"peak_rate,omitempty"`
	MaxInstances    int     `json:"max_instances,omitempty"`
	Samples         int     `json:"samples"`
}

// ColabOneCUUsageAccountData holds the persisted history, active/last run summary, and latest anchor for one Google account.
type ColabOneCUUsageAccountData struct {
	LastAnchor *ColabOneCUUsageAnchor          `json:"last_anchor,omitempty"`
	CurrentRun *ColabOneCUUsageRun             `json:"current_run,omitempty"`
	Buckets    map[int64]*ColabOneCUUsageBucket `json:"buckets"`
}

// ColabOneCUUsageQuery configures a read query against ColabOneCUUsageStore.
type ColabOneCUUsageQuery struct {
	AccountEmail   string
	LoggedIn       bool
	CurrentBalance *float64
	CurrentRate    *float64
	CheckedAt      float64
	Start          int64
	End            int64
	Timezone       string
	UTCOffsetSec   *int
}

type colabOneCUUsageDiskFile struct {
	Version          int                                    `json:"version"`
	BucketSeconds    int                                    `json:"bucket_seconds"`
	LastAccountKey   string                                 `json:"last_account_key,omitempty"`
	Accounts         map[string]*colabOneCUUsageDiskAccount `json:"accounts"`
}

type colabOneCUUsageDiskAccount struct {
	LastAnchor *ColabOneCUUsageAnchor  `json:"last_anchor,omitempty"`
	CurrentRun *ColabOneCUUsageRun     `json:"current_run,omitempty"`
	Buckets    []ColabOneCUUsageBucket `json:"buckets"`
}

// ColabOneCUUsageStore persists per-account Compute Unit (CU) consumption history
// locally in the controller config directory. It uses its own mutex hierarchy
// (persistMu -> mu) independent of ColabOneController locks.
type ColabOneCUUsageStore struct {
	path  string
	nowFn func() float64

	persistMu        sync.Mutex
	mu               sync.Mutex
	accounts         map[string]*ColabOneCUUsageAccountData
	activeAccountKey string
	dirty            bool
	lastSavedAt      float64
}

// NewColabOneCUUsageStore initializes a CU usage store backed by <configDir>/cu_usage.json.
func NewColabOneCUUsageStore(configDir string, nowFn func() float64) *ColabOneCUUsageStore {
	if nowFn == nil {
		nowFn = nowUnixFloat
	}
	path := ""
	if trimmed := strings.TrimSpace(configDir); trimmed != "" {
		path = filepath.Join(trimmed, colabOneCUUsageFilename)
	}
	s := &ColabOneCUUsageStore{
		path:     path,
		nowFn:    nowFn,
		accounts: make(map[string]*ColabOneCUUsageAccountData),
	}
	s.load()
	return s
}

func colabOneCUAccountKey(email string) string {
	norm := strings.ToLower(strings.TrimSpace(email))
	if norm == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(norm))
	return "acct:" + hex.EncodeToString(sum[:])[:16]
}

func colabOneCUBucketStart(ts float64) int64 {
	sec := int64(math.Floor(ts))
	rem := sec % colabOneCUBucketSeconds
	if rem < 0 {
		rem += colabOneCUBucketSeconds
	}
	return sec - rem
}

func colabOneRoundCU(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	r := math.Round(v*10000) / 10000
	if math.Abs(r) < colabOneCUEpsilon {
		return 0
	}
	return r
}

func colabOneCleanFloat(v float64) (float64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func parseCUUsageDiskFile(disk *colabOneCUUsageDiskFile) (string, map[string]*ColabOneCUUsageAccountData) {
	accounts := make(map[string]*ColabOneCUUsageAccountData)
	if disk == nil {
		return "", accounts
	}
	activeKey := strings.TrimSpace(disk.LastAccountKey)
	for key, diskAcct := range disk.Accounts {
		if strings.TrimSpace(key) == "" || diskAcct == nil {
			continue
		}
		acct := &ColabOneCUUsageAccountData{
			Buckets: make(map[int64]*ColabOneCUUsageBucket, len(diskAcct.Buckets)),
		}
		if diskAcct.LastAnchor != nil && diskAcct.LastAnchor.TS > 0 {
			if _, ok := colabOneCleanFloat(diskAcct.LastAnchor.TS); ok {
				anchor := *diskAcct.LastAnchor
				if b, ok := colabOneCleanFloat(anchor.Balance); ok && b >= 0 {
					anchor.Balance = b
				} else {
					anchor.Balance = 0
					anchor.HasBalance = false
				}
				if r, ok := colabOneCleanFloat(anchor.Rate); ok && r >= 0 {
					anchor.Rate = r
				} else {
					anchor.Rate = 0
				}
				if anchor.Assignments < 0 {
					anchor.Assignments = 0
				}
				acct.LastAnchor = &anchor
			}
		}
		if diskAcct.CurrentRun != nil && diskAcct.CurrentRun.StartedAt > 0 {
			if startedAt, ok := colabOneCleanFloat(diskAcct.CurrentRun.StartedAt); ok && startedAt > 0 {
				run := *diskAcct.CurrentRun
				run.StartedAt = startedAt
				if ended, ok := colabOneCleanFloat(run.EndedAt); ok && ended >= startedAt {
					run.EndedAt = ended
				} else if !run.Active {
					run.EndedAt = startedAt
				} else {
					run.EndedAt = 0
				}
				if run.HasStartBalance {
					if sb, ok := colabOneCleanFloat(run.StartBalance); ok && sb >= 0 {
						run.StartBalance = sb
					} else {
						run.StartBalance = 0
						run.HasStartBalance = false
					}
				} else {
					run.StartBalance = 0
				}
				if run.HasEndBalance {
					if eb, ok := colabOneCleanFloat(run.EndBalance); ok && eb >= 0 {
						run.EndBalance = eb
					} else {
						run.EndBalance = 0
						run.HasEndBalance = false
					}
				} else {
					run.EndBalance = 0
				}
				if c, ok := colabOneCleanFloat(run.Consumed); ok && c >= 0 {
					run.Consumed = c
				} else {
					run.Consumed = 0
				}
				if u, ok := colabOneCleanFloat(run.Unsettled); ok && u >= 0 && run.Active {
					run.Unsettled = u
				} else {
					run.Unsettled = 0
				}
				if tp, ok := colabOneCleanFloat(run.Topup); ok && tp >= 0 {
					run.Topup = tp
				} else {
					run.Topup = 0
				}
				if ls, ok := colabOneCleanFloat(run.LastSampleAt); ok && ls > 0 {
					run.LastSampleAt = ls
				} else {
					run.LastSampleAt = startedAt
				}
				if lr, ok := colabOneCleanFloat(run.LastRate); ok && lr >= 0 && run.Active {
					run.LastRate = lr
				} else {
					run.LastRate = 0
				}
				if pr, ok := colabOneCleanFloat(run.PeakRate); ok && pr >= 0 {
					run.PeakRate = pr
				} else {
					run.PeakRate = 0
				}
				if run.MaxInstances < 0 {
					run.MaxInstances = 0
				}
				if run.Samples < 0 {
					run.Samples = 0
				}
				acct.CurrentRun = &run
			}
		}
		for _, b := range diskAcct.Buckets {
			if b.TS <= 0 {
				continue
			}
			aligned := colabOneCUBucketStart(float64(b.TS))
			cp := b
			cp.TS = aligned
			if cp.Consumed < 0 || math.IsNaN(cp.Consumed) || math.IsInf(cp.Consumed, 0) {
				cp.Consumed = 0
			}
			if cp.Topup < 0 || math.IsNaN(cp.Topup) || math.IsInf(cp.Topup, 0) {
				cp.Topup = 0
			}
			if cp.RateSum < 0 || math.IsNaN(cp.RateSum) || math.IsInf(cp.RateSum, 0) {
				cp.RateSum = 0
			}
			if cp.RateSec < 0 || math.IsNaN(cp.RateSec) || math.IsInf(cp.RateSec, 0) {
				cp.RateSec = 0
			}
			if cp.RateMax < 0 || math.IsNaN(cp.RateMax) || math.IsInf(cp.RateMax, 0) {
				cp.RateMax = 0
			}
			if cp.ActiveSec < 0 || math.IsNaN(cp.ActiveSec) || math.IsInf(cp.ActiveSec, 0) {
				cp.ActiveSec = 0
			}
			if cp.MaxInstances < 0 {
				cp.MaxInstances = 0
			}
			if cp.Samples < 0 {
				cp.Samples = 0
			}
			if cp.HasBalance {
				if bs, ok := colabOneCleanFloat(cp.BalanceStart); ok && bs >= 0 {
					cp.BalanceStart = bs
				} else {
					cp.HasBalance = false
				}
				if be, ok := colabOneCleanFloat(cp.BalanceEnd); ok && be >= 0 {
					cp.BalanceEnd = be
				} else {
					cp.HasBalance = false
				}
			}
			acct.Buckets[aligned] = &cp
		}
		accounts[key] = acct
	}
	return activeKey, accounts
}

func (s *ColabOneCUUsageStore) load() {
	if s.path == "" {
		return
	}
	info, statErr := os.Lstat(s.path)
	if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	var disk colabOneCUUsageDiskFile
	if decodeJSONWithNumbers(raw, &disk) != nil || disk.Accounts == nil {
		// Preserve corrupt file on disk instead of silently clobbering it on the next Flush().
		// Keep legacy .corrupt path when vacant for compatibility, otherwise allocate a timestamped
		// backup path and guard against symlinks and directories via os.Lstat.
		corruptPath := s.path + ".corrupt"
		if _, existsErr := os.Lstat(corruptPath); !errors.Is(existsErr, os.ErrNotExist) {
			corruptPath = fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().Unix())
			if _, tsExistsErr := os.Lstat(corruptPath); tsExistsErr == nil {
				corruptPath = fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().UnixNano())
			}
		}
		if cInfo, cErr := os.Lstat(corruptPath); cErr != nil || (cInfo.Mode()&os.ModeSymlink == 0 && !cInfo.IsDir()) {
			if colabOneRenameReplace(s.path, corruptPath) != nil && len(raw) > 0 {
				_ = os.WriteFile(corruptPath, raw, 0o600)
			}
		}
		return
	}
	activeKey, parsed := parseCUUsageDiskFile(&disk)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeAccountKey = activeKey
	s.accounts = parsed
}

func (s *ColabOneCUUsageStore) buildDiskSnapshotLocked() colabOneCUUsageDiskFile {
	disk := colabOneCUUsageDiskFile{
		Version:        colabOneCUUsageSchemaVersion,
		BucketSeconds:  colabOneCUBucketSeconds,
		LastAccountKey: s.activeAccountKey,
		Accounts:       make(map[string]*colabOneCUUsageDiskAccount, len(s.accounts)),
	}
	for key, acct := range s.accounts {
		if acct == nil {
			continue
		}
		keys := make([]int64, 0, len(acct.Buckets))
		for ts := range acct.Buckets {
			keys = append(keys, ts)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		list := make([]ColabOneCUUsageBucket, 0, len(keys))
		for _, ts := range keys {
			b := acct.Buckets[ts]
			if b == nil {
				continue
			}
			cp := *b
			cp.Consumed = colabOneRoundCU(cp.Consumed)
			cp.Topup = colabOneRoundCU(cp.Topup)
			cp.RateSum = colabOneRoundCU(cp.RateSum)
			cp.RateSec = math.Round(cp.RateSec*10) / 10
			cp.RateMax = colabOneRoundCU(cp.RateMax)
			cp.ActiveSec = math.Round(cp.ActiveSec*10) / 10
			if cp.HasBalance {
				cp.BalanceStart = colabOneRoundCU(cp.BalanceStart)
				cp.BalanceEnd = colabOneRoundCU(cp.BalanceEnd)
			}
			list = append(list, cp)
		}
		var anchorCopy *ColabOneCUUsageAnchor
		if acct.LastAnchor != nil {
			ac := *acct.LastAnchor
			anchorCopy = &ac
		}
		var runCopy *ColabOneCUUsageRun
		if acct.CurrentRun != nil && acct.CurrentRun.StartedAt > 0 {
			rc := *acct.CurrentRun
			rc.Consumed = colabOneRoundCU(rc.Consumed)
			rc.Unsettled = colabOneRoundCU(rc.Unsettled)
			rc.Topup = colabOneRoundCU(rc.Topup)
			rc.LastRate = colabOneRoundCU(rc.LastRate)
			rc.PeakRate = colabOneRoundCU(rc.PeakRate)
			if rc.HasStartBalance {
				rc.StartBalance = colabOneRoundCU(rc.StartBalance)
			}
			if rc.HasEndBalance {
				rc.EndBalance = colabOneRoundCU(rc.EndBalance)
			}
			runCopy = &rc
		}
		disk.Accounts[key] = &colabOneCUUsageDiskAccount{
			LastAnchor: anchorCopy,
			CurrentRun: runCopy,
			Buckets:    list,
		}
	}
	return disk
}

// ExportDiskMap returns a JSON-safe map snapshot of all persisted CU usage history.
func (s *ColabOneCUUsageStore) ExportDiskMap() map[string]any {
	if s == nil {
		return map[string]any{
			"version":        colabOneCUUsageSchemaVersion,
			"bucket_seconds": colabOneCUBucketSeconds,
			"accounts":       map[string]any{},
		}
	}
	s.mu.Lock()
	disk := s.buildDiskSnapshotLocked()
	s.mu.Unlock()
	raw, err := json.Marshal(disk)
	if err != nil {
		return map[string]any{
			"version":        colabOneCUUsageSchemaVersion,
			"bucket_seconds": colabOneCUBucketSeconds,
			"accounts":       map[string]any{},
		}
	}
	var out map[string]any
	if err := decodeJSONWithNumbers(raw, &out); err != nil || out == nil {
		return map[string]any{
			"version":        colabOneCUUsageSchemaVersion,
			"bucket_seconds": colabOneCUBucketSeconds,
			"accounts":       map[string]any{},
		}
	}
	return out
}

func validateCUUsageDiskMap(raw map[string]any) (*colabOneCUUsageDiskFile, error) {
	if len(raw) == 0 {
		return &colabOneCUUsageDiskFile{
			Version:       colabOneCUUsageSchemaVersion,
			BucketSeconds: colabOneCUBucketSeconds,
			Accounts:      map[string]*colabOneCUUsageDiskAccount{},
		}, nil
	}
	encoded, err := json.Marshal(SanitizeJSONValue(raw))
	if err != nil {
		return nil, errors.New("CU 算力消耗历史格式无效")
	}
	var disk colabOneCUUsageDiskFile
	if err := decodeJSONWithNumbers(encoded, &disk); err != nil {
		return nil, errors.New("CU 算力消耗历史格式无效")
	}
	if disk.Accounts == nil {
		disk.Accounts = map[string]*colabOneCUUsageDiskAccount{}
	}
	return &disk, nil
}

// ReplaceFromDiskMap validates and atomically replaces all stored CU usage data
// (both in memory and on disk), rolling back in-memory state if disk persistence fails.
func (s *ColabOneCUUsageStore) ReplaceFromDiskMap(raw map[string]any) error {
	if s == nil {
		return nil
	}
	disk, err := validateCUUsageDiskMap(raw)
	if err != nil {
		return err
	}
	activeKey, parsed := parseCUUsageDiskFile(disk)

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	prevAccounts := s.accounts
	prevActiveKey := s.activeAccountKey
	prevDirty := s.dirty
	prevSavedAt := s.lastSavedAt

	s.accounts = parsed
	s.activeAccountKey = activeKey
	nowSec := int64(math.Floor(s.nowFn()))
	for _, acct := range s.accounts {
		s.pruneAccountLocked(acct, nowSec)
	}
	nextDisk := s.buildDiskSnapshotLocked()
	s.dirty = false
	s.lastSavedAt = s.nowFn()
	s.mu.Unlock()

	if s.path != "" {
		if err := AtomicJSON(s.path, nextDisk); err != nil {
			s.mu.Lock()
			s.accounts = prevAccounts
			s.activeAccountKey = prevActiveKey
			s.dirty = prevDirty
			s.lastSavedAt = prevSavedAt
			s.mu.Unlock()
			return err
		}
	}
	return nil
}

// ResetActiveAccount clears the active account binding on re-login so the next
// account poll resolves cleanly to the newly signed-in account.
func (s *ColabOneCUUsageStore) ResetActiveAccount() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.activeAccountKey = ""
	s.mu.Unlock()
}

// RecordAccount extracts CU telemetry from a controller account snapshot map
// and records it when the user is signed in.
func (s *ColabOneCUUsageStore) RecordAccount(account map[string]any) {
	if s == nil || account == nil {
		return
	}
	if !asBool(account["logged_in"]) {
		return
	}
	bal, hasBal := asFloat(account["balance"])
	if hasBal {
		if clean, ok := colabOneCleanFloat(bal); ok && clean >= 0 {
			bal = clean
		} else {
			bal = 0
			hasBal = false
		}
	}
	rate, hasRate := asFloat(account["rate"])
	if hasRate {
		if clean, ok := colabOneCleanFloat(rate); ok && clean >= 0 {
			rate = clean
		} else {
			rate = 0
		}
	}
	if !hasBal && !hasRate {
		return
	}
	ts, hasTS := asFloat(account["checked_at"])
	if !hasTS || ts <= 0 || math.IsNaN(ts) || math.IsInf(ts, 0) {
		ts = s.nowFn()
	}
	assignments, _ := asInt(account["assignments"])
	if assignments < 0 {
		assignments = 0
	}
	email := asTrimmedString(account["email"])
	s.RecordSample(email, ts, bal, hasBal, rate, assignments)
}

// RecordSample records a single observed account balance/rate sample into the store.
func (s *ColabOneCUUsageStore) RecordSample(
	email string,
	ts float64,
	balance float64,
	hasBalance bool,
	rate float64,
	assignments int,
) {
	if s == nil {
		return
	}
	cleanTS, ok := colabOneCleanFloat(ts)
	if !ok || cleanTS <= 0 {
		return
	}
	if hasBalance {
		if b, ok := colabOneCleanFloat(balance); ok && b >= 0 {
			balance = b
		} else {
			balance = 0
			hasBalance = false
		}
	}
	if r, ok := colabOneCleanFloat(rate); ok && r >= 0 {
		rate = r
	} else {
		rate = 0
	}
	if assignments < 0 {
		assignments = 0
	}

	shouldFlush := false

	s.mu.Lock()
	accountKey := s.resolveAccountKeyLocked(email, cleanTS)
	acct := s.ensureAccountLocked(accountKey)

	finalBucketTS := colabOneCUBucketStart(cleanTS)
	_, hadFinalBucket := acct.Buckets[finalBucketTS]
	consumedTotal := 0.0
	topupTotal := 0.0
	dt := 0.0
	changed := false

	prev := acct.LastAnchor
	switch {
	case prev == nil || prev.TS <= 0:
		b := s.ensureBucketLocked(acct, finalBucketTS)
		b.Samples++
		if hasBalance {
			if !b.HasBalance {
				b.BalanceStart = balance
				b.HasBalance = true
			}
			b.BalanceEnd = balance
		}
		if rate > b.RateMax {
			b.RateMax = rate
		}
		if assignments > b.MaxInstances {
			b.MaxInstances = assignments
		}
		acct.LastAnchor = &ColabOneCUUsageAnchor{
			TS:          cleanTS,
			Balance:     balance,
			HasBalance:  hasBalance,
			Rate:        rate,
			Assignments: assignments,
		}
		changed = true

	case cleanTS < prev.TS:
		// Out-of-order or backwards clock jump: do not corrupt existing buckets.
		s.mu.Unlock()
		return

	case cleanTS == prev.TS:
		sameBal := hasBalance == prev.HasBalance && (!hasBalance || math.Abs(balance-prev.Balance) <= colabOneCUEpsilon)
		sameRate := math.Abs(rate-prev.Rate) <= colabOneCUEpsilon
		needRunInit := (rate > colabOneCUEpsilon || assignments > 0) && (acct.CurrentRun == nil || !acct.CurrentRun.Active)
		if sameBal && sameRate && assignments == prev.Assignments && !needRunInit {
			s.mu.Unlock()
			return
		}
		b := s.ensureBucketLocked(acct, finalBucketTS)
		if hasBalance && prev.HasBalance {
			diff := prev.Balance - balance
			if diff > colabOneCUEpsilon {
				b.Consumed += diff
				consumedTotal = diff
			} else if diff < -colabOneCUEpsilon {
				b.Topup += -diff
				topupTotal = -diff
			}
			if !b.HasBalance {
				b.BalanceStart = prev.Balance
				b.HasBalance = true
			}
			b.BalanceEnd = balance
		} else if hasBalance {
			if !b.HasBalance {
				b.BalanceStart = balance
				b.HasBalance = true
			}
			b.BalanceEnd = balance
		}
		if rate > b.RateMax {
			b.RateMax = rate
		}
		if assignments > b.MaxInstances {
			b.MaxInstances = assignments
		}
		b.Samples++
		acct.LastAnchor = &ColabOneCUUsageAnchor{
			TS:          cleanTS,
			Balance:     balance,
			HasBalance:  hasBalance,
			Rate:        rate,
			Assignments: assignments,
		}
		changed = true

	default:
		dt = cleanTS - prev.TS
		maxInst := assignments
		if prev.Assignments > maxInst {
			maxInst = prev.Assignments
		}

		if dt <= colabOneCUOnlineGapMaxSec {
			hasDiff := hasBalance && prev.HasBalance
			diff := 0.0
			if hasDiff {
				diff = prev.Balance - balance
			}

			activeSec := 0.0
			rateForInterval := 0.0
			intervalMaxRate := 0.0
			intervalMaxInst := 0

			switch {
			case prev.Rate > 0 && rate > 0:
				activeSec = dt
				rateForInterval = (prev.Rate + rate) / 2.0
				intervalMaxRate = math.Max(prev.Rate, rate)
				intervalMaxInst = maxInst
			case prev.Rate > 0 || rate > 0:
				transRate := rate
				transInst := assignments
				if prev.Rate > 0 {
					transRate = prev.Rate
					transInst = prev.Assignments
				}
				if hasDiff && diff > colabOneCUEpsilon && transRate > 0 {
					estSec := diff / transRate * 3600.0
					if estSec > dt {
						estSec = dt
					}
					if estSec > 0 {
						activeSec = estSec
						rateForInterval = transRate
						intervalMaxRate = transRate
						intervalMaxInst = transInst
					}
				} else if dt <= 120.0 {
					// Normal poll cadence across start/stop boundary: attribute midpoint active duration.
					activeSec = dt * 0.5
					rateForInterval = transRate
					intervalMaxRate = transRate
					intervalMaxInst = transInst
				}
			}

			if hasDiff {
				if diff > colabOneCUEpsilon {
					consumedTotal = diff
				} else if diff < -colabOneCUEpsilon {
					estBurn := 0.0
					if rateForInterval > 0 && activeSec > 0 {
						estBurn = rateForInterval * activeSec / 3600.0
					}
					consumedTotal = estBurn
					topupTotal = (-diff) + estBurn
				}
			}
			s.distributeIntervalLocked(
				acct,
				prev.TS,
				cleanTS,
				consumedTotal,
				activeSec,
				rateForInterval,
				intervalMaxRate,
				intervalMaxInst,
				prev.Balance,
				balance,
				hasDiff && topupTotal == 0,
				false,
			)
			finalB := s.ensureBucketLocked(acct, finalBucketTS)
			if topupTotal > colabOneCUEpsilon {
				finalB.Topup += topupTotal
			}
			if hasBalance {
				if !finalB.HasBalance {
					finalB.BalanceStart = balance
					finalB.HasBalance = true
				}
				finalB.BalanceEnd = balance
			}
			if rate > finalB.RateMax {
				finalB.RateMax = rate
			}
			if assignments > finalB.MaxInstances {
				finalB.MaxInstances = assignments
			}
			finalB.Samples++
			changed = true
		} else if dt <= colabOneCUOfflineProrateMaxSec {
			if hasBalance && prev.HasBalance {
				diff := prev.Balance - balance
				if diff > colabOneCUEpsilon {
					consumedTotal = diff
					offlineActive := 0.0
					offlineRate := 0.0
					offlineMaxRate := 0.0
					offlineMaxInst := 0
					if prev.Rate > 0 && rate > 0 {
						offlineActive = dt
						offlineRate = (prev.Rate + rate) / 2.0
						offlineMaxRate = math.Max(prev.Rate, rate)
						offlineMaxInst = maxInst
					}
					s.distributeIntervalLocked(
						acct,
						prev.TS,
						cleanTS,
						consumedTotal,
						offlineActive,
						offlineRate,
						offlineMaxRate,
						offlineMaxInst,
						prev.Balance,
						balance,
						true,
						true,
					)
				} else if diff < -colabOneCUEpsilon {
					topupTotal = -diff
				}
			}
			finalB := s.ensureBucketLocked(acct, finalBucketTS)
			if topupTotal > colabOneCUEpsilon {
				finalB.Topup += topupTotal
			}
			if hasBalance {
				if !finalB.HasBalance {
					finalB.BalanceStart = balance
					finalB.HasBalance = true
				}
				finalB.BalanceEnd = balance
			}
			if rate > finalB.RateMax {
				finalB.RateMax = rate
			}
			if assignments > finalB.MaxInstances {
				finalB.MaxInstances = assignments
			}
			finalB.Samples++
			changed = true
		} else {
			// Offline longer than 24h: start fresh baseline at cleanTS without distorting today's trend.
			finalB := s.ensureBucketLocked(acct, finalBucketTS)
			if hasBalance {
				if !finalB.HasBalance {
					finalB.BalanceStart = balance
					finalB.HasBalance = true
				}
				finalB.BalanceEnd = balance
			}
			if rate > finalB.RateMax {
				finalB.RateMax = rate
			}
			if assignments > finalB.MaxInstances {
				finalB.MaxInstances = assignments
			}
			finalB.Samples++
			changed = true
		}

		acct.LastAnchor = &ColabOneCUUsageAnchor{
			TS:          cleanTS,
			Balance:     balance,
			HasBalance:  hasBalance,
			Rate:        rate,
			Assignments: assignments,
		}
	}

	runChanged, runTransition := s.updateCurrentRunLocked(
		acct,
		prev,
		cleanTS,
		balance,
		hasBalance,
		rate,
		assignments,
		dt,
		consumedTotal,
		topupTotal,
	)
	if runChanged {
		changed = true
	}

	if changed {
		s.pruneAccountLocked(acct, int64(math.Floor(cleanTS)))
		s.dirty = true
		nowSec := s.nowFn()
		if !hadFinalBucket || runTransition || consumedTotal > colabOneCUEpsilon || topupTotal > colabOneCUEpsilon ||
			s.lastSavedAt == 0 || (nowSec-s.lastSavedAt) >= colabOneCUFlushMinIntervalSec {
			shouldFlush = true
		}
	}
	s.mu.Unlock()

	if shouldFlush {
		_ = s.Flush()
	}
}

// BeginRun seeds an active continuous billing run when a managed GPU is allocated
// if no active run is currently tracked for the account.
func (s *ColabOneCUUsageStore) BeginRun(email string, startedAt float64, startBalance float64, hasStartBalance bool) {
	if s == nil {
		return
	}
	cleanStart, ok := colabOneCleanFloat(startedAt)
	if !ok || cleanStart <= 0 {
		cleanStart = s.nowFn()
	}
	if hasStartBalance {
		if sb, ok := colabOneCleanFloat(startBalance); ok && sb >= 0 {
			startBalance = sb
		} else {
			startBalance = 0
			hasStartBalance = false
		}
	}

	s.mu.Lock()
	accountKey := s.resolveAccountKeyLocked(email, cleanStart)
	acct := s.ensureAccountLocked(accountKey)
	if acct.CurrentRun != nil && acct.CurrentRun.Active {
		s.mu.Unlock()
		return
	}
	run := &ColabOneCUUsageRun{
		Active:       true,
		StartedAt:    cleanStart,
		LastSampleAt: cleanStart,
		MaxInstances: 1,
		Samples:      0,
	}
	if hasStartBalance {
		run.StartBalance = startBalance
		run.HasStartBalance = true
		run.EndBalance = startBalance
		run.HasEndBalance = true
	} else if acct.LastAnchor != nil && acct.LastAnchor.HasBalance && math.Abs(cleanStart-acct.LastAnchor.TS) <= 180.0 {
		run.StartBalance = acct.LastAnchor.Balance
		run.HasStartBalance = true
		run.EndBalance = acct.LastAnchor.Balance
		run.HasEndBalance = true
	}
	acct.CurrentRun = run
	s.dirty = true
	s.mu.Unlock()

	_ = s.Flush()
}

// CurrentRunSnapshot returns a JSON-safe snapshot of the active or most recently
// completed continuous billing run for email, or nil if no run has been recorded.
func (s *ColabOneCUUsageStore) CurrentRunSnapshot(email string) map[string]any {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	accountKey := colabOneCUAccountKey(email)
	if accountKey == "" {
		accountKey = s.activeAccountKey
	}
	if accountKey == "" {
		s.mu.Unlock()
		return nil
	}
	acct := s.accounts[accountKey]
	if acct == nil || acct.CurrentRun == nil || acct.CurrentRun.StartedAt <= 0 {
		s.mu.Unlock()
		return nil
	}
	run := *acct.CurrentRun
	s.mu.Unlock()

	out := map[string]any{
		"active":         run.Active,
		"started_at":     run.StartedAt,
		"consumed":       colabOneRoundCU(run.Consumed),
		"unsettled":      colabOneRoundCU(run.Unsettled),
		"topup":          colabOneRoundCU(run.Topup),
		"last_sample_at": run.LastSampleAt,
		"last_rate":      colabOneRoundCU(run.LastRate),
		"peak_rate":      colabOneRoundCU(run.PeakRate),
		"max_instances":  run.MaxInstances,
		"samples":        run.Samples,
	}
	if !run.Active && run.EndedAt > 0 {
		out["ended_at"] = run.EndedAt
	}
	if run.HasStartBalance {
		out["start_balance"] = colabOneRoundCU(run.StartBalance)
	}
	if run.HasEndBalance {
		out["end_balance"] = colabOneRoundCU(run.EndBalance)
	}
	return out
}

func (s *ColabOneCUUsageStore) updateCurrentRunLocked(
	acct *ColabOneCUUsageAccountData,
	prev *ColabOneCUUsageAnchor,
	cleanTS float64,
	balance float64,
	hasBalance bool,
	rate float64,
	assignments int,
	dt float64,
	consumedTotal float64,
	topupTotal float64,
) (bool, bool) {
	sampleRunning := rate > colabOneCUEpsilon || assignments > 0
	run := acct.CurrentRun

	// Case 1: No currently active run.
	if run == nil || !run.Active {
		if sampleRunning {
			startedAt := cleanTS
			startBal := balance
			hasStartBal := hasBalance
			initConsumed := 0.0
			initTopup := 0.0
			if prev != nil && prev.TS > 0 && dt >= 0 && dt <= colabOneCUOnlineGapMaxSec {
				if prev.HasBalance {
					startBal = prev.Balance
					hasStartBal = true
				}
				if consumedTotal > colabOneCUEpsilon {
					initConsumed = consumedTotal
					if rate > colabOneCUEpsilon {
						estSec := math.Min(dt, consumedTotal/rate*3600.0)
						startedAt = cleanTS - estSec
					} else if dt <= 120.0 {
						startedAt = cleanTS - dt*0.5
					}
				}
				initTopup = topupTotal
			}
			acct.CurrentRun = &ColabOneCUUsageRun{
				Active:          true,
				StartedAt:       startedAt,
				StartBalance:    startBal,
				HasStartBalance: hasStartBal,
				EndBalance:      balance,
				HasEndBalance:   hasBalance,
				Consumed:        initConsumed,
				Unsettled:       0,
				Topup:           initTopup,
				LastSampleAt:    cleanTS,
				LastRate:        rate,
				PeakRate:        rate,
				MaxInstances:    assignments,
				Samples:         1,
			}
			return true, true
		}

		// Fold trailing Google billing settlement arriving shortly after run stop.
		if run != nil && !run.Active && run.EndedAt > 0 &&
			cleanTS >= run.EndedAt &&
			(cleanTS-run.EndedAt) <= colabOneCURunDelayedSettleMaxSec &&
			dt >= 0 && dt <= colabOneCUOnlineGapMaxSec &&
			consumedTotal > colabOneCUEpsilon {
			run.Consumed += consumedTotal
			if hasBalance {
				run.EndBalance = balance
				run.HasEndBalance = true
			}
			run.LastSampleAt = cleanTS
			return true, false
		}
		return false, false
	}

	// Case 2: An active run is currently tracked.
	if dt > colabOneCUOfflineProrateMaxSec {
		if sampleRunning {
			acct.CurrentRun = &ColabOneCUUsageRun{
				Active:          true,
				StartedAt:       cleanTS,
				StartBalance:    balance,
				HasStartBalance: hasBalance,
				EndBalance:      balance,
				HasEndBalance:   hasBalance,
				Consumed:        0,
				Unsettled:       0,
				Topup:           0,
				LastSampleAt:    cleanTS,
				LastRate:        rate,
				PeakRate:        rate,
				MaxInstances:    assignments,
				Samples:         1,
			}
			return true, true
		}
		endedAt := run.LastSampleAt
		if endedAt < run.StartedAt {
			endedAt = run.StartedAt
		}
		run.Active = false
		run.EndedAt = endedAt
		run.Unsettled = 0
		run.LastRate = 0
		return true, true
	}

	if sampleRunning {
		runConsumed := consumedTotal
		runTopup := topupTotal
		bridgeDT := dt

		// On the first sample after BeginRun(), if the previous anchor predates this
		// run by > 3 minutes (e.g. laptop woke from idle sleep), compute this run's
		// initial delta against BeginRun's start_balance rather than swallowing any
		// pre-run offline balance changes.
		if run.Samples == 0 {
			prevIsStale := prev == nil || prev.TS <= 0 || (run.StartedAt-prev.TS) > 180.0
			if cleanTS > run.StartedAt {
				if bridgeDT <= 0 || bridgeDT > (cleanTS-run.StartedAt) {
					bridgeDT = cleanTS - run.StartedAt
				}
			} else {
				bridgeDT = 0
			}
			if prevIsStale {
				runConsumed = 0
				runTopup = 0
				if run.HasStartBalance && hasBalance {
					diff := run.StartBalance - balance
					if diff > colabOneCUEpsilon {
						runConsumed = diff
					} else if diff < -colabOneCUEpsilon {
						estBurn := 0.0
						if rate > colabOneCUEpsilon && bridgeDT > 0 && bridgeDT <= colabOneCURunUnsettledCapSec {
							estBurn = rate * bridgeDT / 3600.0
						}
						runConsumed = estBurn
						runTopup = (-diff) + estBurn
					}
				}
			}
		}

		if runConsumed > colabOneCUEpsilon {
			run.Consumed += runConsumed
			run.Unsettled = 0
		} else if runTopup > colabOneCUEpsilon {
			run.Unsettled = 0
		} else if bridgeDT > 0 && bridgeDT <= colabOneCURunUnsettledCapSec &&
			rate > colabOneCUEpsilon && (!hasBalance || balance > colabOneCUEpsilon) {
			effRate := rate
			if run.Samples > 0 && prev != nil && prev.Rate > colabOneCUEpsilon {
				effRate = (prev.Rate + rate) / 2.0
			}
			run.Unsettled += effRate * bridgeDT / 3600.0
			maxUnsettled := effRate * colabOneCURunUnsettledCapSec / 3600.0
			if run.Unsettled > maxUnsettled {
				run.Unsettled = maxUnsettled
			}
			if hasBalance && run.Unsettled > balance {
				run.Unsettled = balance
			}
		}
		if hasBalance && balance <= colabOneCUEpsilon {
			run.Unsettled = 0
		}
		if runTopup > colabOneCUEpsilon {
			run.Topup += runTopup
		}
		if hasBalance {
			if !run.HasStartBalance {
				run.StartBalance = balance
				run.HasStartBalance = true
			}
			run.EndBalance = balance
			run.HasEndBalance = true
		}
		if rate > run.PeakRate {
			run.PeakRate = rate
		}
		if assignments > run.MaxInstances {
			run.MaxInstances = assignments
		}
		run.LastSampleAt = cleanTS
		run.LastRate = rate
		run.Samples++
		return true, false
	}

	// Ignore an in-flight pre-allocation poll timestamped earlier than BeginRun().
	if run.Samples == 0 && cleanTS < run.StartedAt {
		return false, false
	}

	// Active run ended (rate == 0 and assignments == 0).
	if consumedTotal > colabOneCUEpsilon {
		run.Consumed += consumedTotal
	}
	if topupTotal > colabOneCUEpsilon {
		run.Topup += topupTotal
	}
	if hasBalance {
		if !run.HasStartBalance {
			run.StartBalance = balance
			run.HasStartBalance = true
		}
		run.EndBalance = balance
		run.HasEndBalance = true
	}
	endedAt := cleanTS
	if prev != nil && prev.TS > 0 && dt > 120.0 {
		if prev.Rate > colabOneCUEpsilon && consumedTotal > colabOneCUEpsilon {
			estSec := math.Min(dt, consumedTotal/prev.Rate*3600.0)
			endedAt = prev.TS + estSec
		} else {
			endedAt = prev.TS
		}
	}
	if endedAt < run.StartedAt {
		endedAt = run.StartedAt
	}
	run.Active = false
	run.EndedAt = endedAt
	run.Unsettled = 0
	run.LastSampleAt = cleanTS
	run.LastRate = 0
	run.Samples++
	return true, true
}

func (s *ColabOneCUUsageStore) resolveAccountKeyLocked(
	email string,
	ts float64,
) string {
	hashed := colabOneCUAccountKey(email)
	if hashed != "" {
		// If early boot recorded a recent sample under "default" before Profile() resolved email,
		// seamlessly migrate that session into the hashed account bucket if it doesn't exist yet.
		if s.activeAccountKey == colabOneCUDefaultAccountKey {
			if defAcct, hasDef := s.accounts[colabOneCUDefaultAccountKey]; hasDef && defAcct != nil {
				if _, exists := s.accounts[hashed]; !exists {
					refTS := 0.0
					if defAcct.LastAnchor != nil && defAcct.LastAnchor.TS > 0 {
						refTS = defAcct.LastAnchor.TS
					} else if defAcct.CurrentRun != nil && defAcct.CurrentRun.StartedAt > 0 {
						refTS = defAcct.CurrentRun.StartedAt
					}
					if refTS > 0 && math.Abs(ts-refTS) <= colabOneCUMigrateDefaultMaxGapS {
						s.accounts[hashed] = defAcct
						delete(s.accounts, colabOneCUDefaultAccountKey)
					}
				}
			}
		}
		s.activeAccountKey = hashed
		return hashed
	}
	if s.activeAccountKey != "" {
		return s.activeAccountKey
	}
	s.activeAccountKey = colabOneCUDefaultAccountKey
	return colabOneCUDefaultAccountKey
}

func (s *ColabOneCUUsageStore) ensureAccountLocked(key string) *ColabOneCUUsageAccountData {
	acct, ok := s.accounts[key]
	if !ok || acct == nil {
		acct = &ColabOneCUUsageAccountData{
			Buckets: make(map[int64]*ColabOneCUUsageBucket),
		}
		s.accounts[key] = acct
	}
	if acct.Buckets == nil {
		acct.Buckets = make(map[int64]*ColabOneCUUsageBucket)
	}
	return acct
}

func (s *ColabOneCUUsageStore) ensureBucketLocked(acct *ColabOneCUUsageAccountData, bucketTS int64) *ColabOneCUUsageBucket {
	b, ok := acct.Buckets[bucketTS]
	if !ok || b == nil {
		b = &ColabOneCUUsageBucket{TS: bucketTS}
		acct.Buckets[bucketTS] = b
	}
	return b
}

func (s *ColabOneCUUsageStore) distributeIntervalLocked(
	acct *ColabOneCUUsageAccountData,
	startTS, endTS float64,
	consumed, activeSec, rateForInterval, maxRate float64,
	maxInst int,
	startBal, endBal float64,
	interpolateBal bool,
	estimated bool,
) {
	totalDur := endTS - startTS
	if totalDur <= 0 {
		return
	}
	cur := startTS
	for cur < endTS {
		bStart := colabOneCUBucketStart(cur)
		bEnd := float64(bStart + colabOneCUBucketSeconds)
		segEnd := math.Min(endTS, bEnd)
		segDur := segEnd - cur
		if segDur <= 0 {
			break
		}
		frac := segDur / totalDur
		b := s.ensureBucketLocked(acct, bStart)
		if consumed > colabOneCUEpsilon {
			b.Consumed += consumed * frac
		}
		segActive := 0.0
		if activeSec > 0 {
			segActive = activeSec * frac
			b.ActiveSec += segActive
			if b.ActiveSec > colabOneCUBucketSeconds {
				b.ActiveSec = colabOneCUBucketSeconds
			}
		}
		if rateForInterval > 0 && segActive > 0 {
			b.RateSum += rateForInterval * segActive
			b.RateSec += segActive
		}
		if maxRate > b.RateMax {
			b.RateMax = maxRate
		}
		if maxInst > b.MaxInstances {
			b.MaxInstances = maxInst
		}
		if estimated && consumed > colabOneCUEpsilon {
			b.Estimated = true
		}
		if interpolateBal {
			t0 := (cur - startTS) / totalDur
			t1 := (segEnd - startTS) / totalDur
			segStartBal := startBal + (endBal-startBal)*t0
			segEndBal := startBal + (endBal-startBal)*t1
			if !b.HasBalance {
				b.BalanceStart = segStartBal
				b.HasBalance = true
			}
			b.BalanceEnd = segEndBal
		}
		cur = segEnd
	}
}

func (s *ColabOneCUUsageStore) pruneAccountLocked(acct *ColabOneCUUsageAccountData, nowSec int64) {
	if acct == nil || len(acct.Buckets) == 0 {
		return
	}
	cutoff := nowSec - int64(colabOneCURetentionDays)*86400
	for ts := range acct.Buckets {
		if ts+colabOneCUBucketSeconds < cutoff {
			delete(acct.Buckets, ts)
		}
	}
	if len(acct.Buckets) <= colabOneCUMaxBucketsPerAccount {
		return
	}
	keys := make([]int64, 0, len(acct.Buckets))
	for ts := range acct.Buckets {
		keys = append(keys, ts)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	drop := len(keys) - colabOneCUMaxBucketsPerAccount
	for i := 0; i < drop; i++ {
		delete(acct.Buckets, keys[i])
	}
}

// Flush persists dirty in-memory CU usage history to disk atomically.
func (s *ColabOneCUUsageStore) Flush() error {
	if s == nil || s.path == "" {
		return nil
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	disk := s.buildDiskSnapshotLocked()
	s.dirty = false
	s.lastSavedAt = s.nowFn()
	s.mu.Unlock()

	if err := AtomicJSON(s.path, disk); err != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
		return err
	}
	return nil
}

// Query returns CU usage buckets within [req.Start, req.End) along with metadata
// and local calendar dates that contain recorded usage.
func (s *ColabOneCUUsageStore) Query(req ColabOneCUUsageQuery) map[string]any {
	nowFn := nowUnixFloat
	if s != nil && s.nowFn != nil {
		nowFn = s.nowFn
	}
	nowSec := int64(math.Floor(nowFn()))
	end := req.End
	if end <= 0 {
		end = nowSec
	}
	start := req.Start
	if start <= 0 || start >= end {
		start = end - 24*3600
	}
	if end-start > colabOneCUMaxQueryRangeSeconds {
		start = end - colabOneCUMaxQueryRangeSeconds
	}

	loc := time.Local
	if tz := strings.TrimSpace(req.Timezone); tz != "" {
		if loaded, err := time.LoadLocation(tz); err == nil && loaded != nil {
			loc = loaded
		} else if req.UTCOffsetSec != nil {
			off := *req.UTCOffsetSec
			if off >= -14*3600 && off <= 14*3600 {
				loc = time.FixedZone("", off)
			}
		}
	} else if req.UTCOffsetSec != nil {
		off := *req.UTCOffsetSec
		if off >= -14*3600 && off <= 14*3600 {
			loc = time.FixedZone("", off)
		}
	}

	out := map[string]any{
		"logged_in":       req.LoggedIn,
		"bucket_seconds":  colabOneCUBucketSeconds,
		"retention_days":  colabOneCURetentionDays,
		"start":           start,
		"end":             end,
		"checked_at":      req.CheckedAt,
		"earliest_ts":     int64(0),
		"latest_ts":       int64(0),
		"available_dates": []string{},
		"active_dates":    []string{},
		"buckets":         []map[string]any{},
	}
	if req.CurrentBalance != nil {
		if b, ok := colabOneCleanFloat(*req.CurrentBalance); ok {
			out["current_balance"] = colabOneRoundCU(b)
		}
	}
	if req.CurrentRate != nil {
		if r, ok := colabOneCleanFloat(*req.CurrentRate); ok {
			out["current_rate"] = colabOneRoundCU(r)
		}
	}

	if s == nil || !req.LoggedIn {
		return out
	}

	s.mu.Lock()
	accountKey := colabOneCUAccountKey(req.AccountEmail)
	if accountKey == "" {
		accountKey = s.activeAccountKey
	}
	if accountKey == "" {
		accountKey = colabOneCUDefaultAccountKey
	}
	acct := s.accounts[accountKey]
	if acct == nil || len(acct.Buckets) == 0 {
		s.mu.Unlock()
		return out
	}

	keys := make([]int64, 0, len(acct.Buckets))
	for ts := range acct.Buckets {
		keys = append(keys, ts)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	var earliestTS, latestTS int64
	var initialBalance *float64
	availDateSet := make(map[string]bool)
	activeDateSet := make(map[string]bool)
	resultBuckets := make([]map[string]any, 0)

	for _, ts := range keys {
		b := acct.Buckets[ts]
		if b == nil {
			continue
		}
		if earliestTS == 0 || ts < earliestTS {
			earliestTS = ts
		}
		if ts > latestTS {
			latestTS = ts
		}
		startDate := time.Unix(ts, 0).In(loc).Format("2006-01-02")
		endDate := time.Unix(ts+colabOneCUBucketSeconds-1, 0).In(loc).Format("2006-01-02")
		if b.Samples > 0 || b.Consumed > colabOneCUEpsilon || b.Topup > colabOneCUEpsilon || b.HasBalance {
			availDateSet[startDate] = true
			if endDate != startDate {
				availDateSet[endDate] = true
			}
		}
		if b.Consumed > colabOneCUEpsilon {
			activeDateSet[startDate] = true
			if endDate != startDate {
				activeDateSet[endDate] = true
			}
		}

		// Track last known balance before the queried window so the balance curve starts smoothly.
		if ts+colabOneCUBucketSeconds <= start && b.HasBalance {
			val := colabOneRoundCU(b.BalanceEnd)
			initialBalance = &val
		}

		if ts+colabOneCUBucketSeconds > start && ts < end {
			rateAvg := 0.0
			if b.RateSec > 0 {
				rateAvg = b.RateSum / b.RateSec
			} else if b.RateMax > 0 && b.Samples > 0 {
				rateAvg = b.RateMax
			}
			item := map[string]any{
				"ts":             b.TS,
				"duration":       colabOneCUBucketSeconds,
				"consumed":       colabOneRoundCU(b.Consumed),
				"topup":          colabOneRoundCU(b.Topup),
				"has_balance":    b.HasBalance,
				"rate_avg":       colabOneRoundCU(rateAvg),
				"rate_max":       colabOneRoundCU(b.RateMax),
				"active_seconds": math.Round(b.ActiveSec*10) / 10,
				"max_instances":  b.MaxInstances,
				"samples":        b.Samples,
				"estimated":      b.Estimated,
			}
			if b.HasBalance {
				item["balance_start"] = colabOneRoundCU(b.BalanceStart)
				item["balance_end"] = colabOneRoundCU(b.BalanceEnd)
			}
			resultBuckets = append(resultBuckets, item)
		}
	}
	s.mu.Unlock()

	availDates := make([]string, 0, len(availDateSet))
	for d := range availDateSet {
		availDates = append(availDates, d)
	}
	sort.Strings(availDates)

	activeDates := make([]string, 0, len(activeDateSet))
	for d := range activeDateSet {
		activeDates = append(activeDates, d)
	}
	sort.Strings(activeDates)

	out["earliest_ts"] = earliestTS
	out["latest_ts"] = latestTS
	out["available_dates"] = availDates
	out["active_dates"] = activeDates
	out["buckets"] = resultBuckets
	if initialBalance != nil {
		out["initial_balance"] = *initialBalance
	}
	return out
}

// EstimateWindowCU estimates the Compute Units (CU) consumed by a single GPU runtime
// over [startTS, endTS] using persisted 30-minute UTC rate/consumption buckets (with
// active/last run fallback when the window is very recent).
func (s *ColabOneCUUsageStore) EstimateWindowCU(
	email string,
	startTS, endTS float64,
) (cu float64, avgRate float64, estimated bool, ok bool) {
	if s == nil {
		return 0, 0, false, false
	}
	cleanStart, okStart := colabOneCleanFloat(startTS)
	cleanEnd, okEnd := colabOneCleanFloat(endTS)
	if !okStart || !okEnd || cleanStart <= 0 || cleanEnd <= cleanStart || cleanEnd > colabOneCUMaxValidUnixSec {
		return 0, 0, false, false
	}
	elapsed := cleanEnd - cleanStart
	if math.IsNaN(elapsed) || math.IsInf(elapsed, 0) || elapsed <= 0 || elapsed > colabOneCUMaxEstimateElapsedSec {
		return 0, 0, false, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	accountKey := colabOneCUAccountKey(email)
	if accountKey == "" {
		accountKey = s.activeAccountKey
	}
	if accountKey == "" {
		accountKey = colabOneCUDefaultAccountKey
	}
	acct := s.accounts[accountKey]
	if acct == nil {
		return 0, 0, false, false
	}

	weightedRateSum := 0.0
	coveredSec := 0.0
	anyEstimated := false

	matchingKeys := make([]int64, 0, 4)
	for bTS, b := range acct.Buckets {
		if b == nil || bTS <= 0 {
			continue
		}
		bStart := float64(bTS)
		bEnd := bStart + float64(colabOneCUBucketSeconds)
		if bEnd <= cleanStart || bStart >= cleanEnd {
			continue
		}
		matchingKeys = append(matchingKeys, bTS)
	}
	sort.Slice(matchingKeys, func(i, j int) bool {
		return matchingKeys[i] < matchingKeys[j]
	})

	for _, bTS := range matchingKeys {
		b := acct.Buckets[bTS]
		if b == nil {
			continue
		}
		bStart := float64(bTS)
		bEnd := bStart + float64(colabOneCUBucketSeconds)
		ovStart := math.Max(cleanStart, bStart)
		ovEnd := math.Min(cleanEnd, bEnd)
		ov := ovEnd - ovStart
		if ov <= colabOneCUEpsilon {
			continue
		}

		bucketRate := 0.0
		bucketEst := b.Estimated
		switch {
		case b.RateSec > colabOneCUEpsilon && b.RateSum > colabOneCUEpsilon:
			bucketRate = b.RateSum / b.RateSec
		case b.ActiveSec > colabOneCUEpsilon && b.Consumed > colabOneCUEpsilon:
			bucketRate = b.Consumed * 3600.0 / b.ActiveSec
		case b.RateMax > colabOneCUEpsilon:
			bucketRate = b.RateMax
			bucketEst = true
		}
		if bucketRate <= colabOneCUEpsilon {
			continue
		}
		if b.MaxInstances > 1 {
			bucketRate /= float64(b.MaxInstances)
			bucketEst = true
		}
		weightedRateSum += bucketRate * ov
		coveredSec += ov
		if bucketEst {
			anyEstimated = true
		}
	}

	if coveredSec > colabOneCUEpsilon {
		rate := weightedRateSum / coveredSec
		roundedCU := colabOneRoundCU(rate * elapsed / 3600.0)
		roundedRate := colabOneRoundCU(rate)
		if coveredSec < elapsed*0.8 {
			anyEstimated = true
		}
		if roundedCU > 0 {
			return roundedCU, roundedRate, anyEstimated, true
		}
	}

	// Fallback to the tracked continuous run when the window just completed before
	// the next 30-second balance poll flushed bucket rate telemetry.
	if run := acct.CurrentRun; run != nil && run.StartedAt > 0 {
		runEnd := run.EndedAt
		if run.Active || runEnd <= 0 {
			runEnd = math.Max(run.LastSampleAt, cleanEnd)
		}
		overlaps := cleanEnd >= run.StartedAt-60.0 && cleanStart <= runEnd+60.0
		if overlaps {
			runRate := 0.0
			switch {
			case run.LastRate > colabOneCUEpsilon:
				runRate = run.LastRate
			case run.PeakRate > colabOneCUEpsilon:
				runRate = run.PeakRate
			case run.Consumed > colabOneCUEpsilon && runEnd > run.StartedAt:
				runRate = run.Consumed * 3600.0 / (runEnd - run.StartedAt)
			}
			if runRate > colabOneCUEpsilon {
				if run.MaxInstances > 1 {
					runRate /= float64(run.MaxInstances)
				}
				roundedCU := colabOneRoundCU(runRate * elapsed / 3600.0)
				roundedRate := colabOneRoundCU(runRate)
				if roundedCU > 0 {
					return roundedCU, roundedRate, true, true
				}
			}
		}
	}

	return 0, 0, false, false
}

// ActiveRunStartedAt returns the Unix start timestamp of the currently active
// billing run for email, or 0 if no active run is tracked.
func (s *ColabOneCUUsageStore) ActiveRunStartedAt(email string) float64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	accountKey := colabOneCUAccountKey(email)
	if accountKey == "" {
		accountKey = s.activeAccountKey
	}
	if accountKey == "" {
		accountKey = colabOneCUDefaultAccountKey
	}
	acct := s.accounts[accountKey]
	if acct == nil || acct.CurrentRun == nil || !acct.CurrentRun.Active || acct.CurrentRun.StartedAt <= 0 {
		return 0
	}
	return acct.CurrentRun.StartedAt
}
