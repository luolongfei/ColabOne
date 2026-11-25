// Package updater implements the pure-Go self-update state machine,
// preference persistence, bilingual release notes parser, and read-only
// GitHub Releases fallback checker for ColabOne.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	COLABONE_GITHUB_REPO      = "luolongfei/ColabOne"
	COLABONE_RELEASES_URL     = "https://github.com/luolongfei/ColabOne/releases"
	COLABONE_UPDATE_PREF_FILE = "updater.json"

	COLABONE_UPDATE_INITIAL_DELAY    = 1 * time.Second
	COLABONE_UPDATE_CHECK_INTERVAL   = 6 * time.Hour
	COLABONE_UPDATE_PROGRESS_MIN_GAP = 120 * time.Millisecond
	COLABONE_ROLLBACK_MAX_VERSIONS   = 3
	COLABONE_ROLLBACK_CACHE_TTL      = 5 * time.Minute

	colabOneCancelWaitTimeout   = 250 * time.Millisecond
	colabOneWinRenameAttempts   = 12
	colabOneWinRenameBaseDelay  = 5 * time.Millisecond
	colabOneWinErrFileNotFound  = syscall.Errno(2)
	colabOneWinErrAccessDenied  = syscall.Errno(5)
	colabOneWinErrShareViol     = syscall.Errno(32)
	colabOneWinErrLockViolation = syscall.Errno(33)
)

// Lifecycle states exposed to the desktop UI.
const (
	StatusIdle           = "idle"
	StatusChecking       = "checking"
	StatusUpToDate       = "up_to_date"
	StatusAvailable      = "available"
	StatusDownloading    = "downloading"
	StatusInstalling     = "installing"
	StatusReadyToRestart = "ready_to_restart"
	StatusDisabled       = "disabled"
	StatusError          = "error"
)

// Classified error codes exposed to the desktop UI for localized messaging.
const (
	ErrCodeNone        = ""
	ErrCodeProxy       = "proxy"
	ErrCodeTimeout     = "timeout"
	ErrCodeTLS         = "tls"
	ErrCodeNetwork     = "network"
	ErrCodeNotFound    = "not_found"
	ErrCodeRateLimited = "rate_limited"
	ErrCodeSignature   = "signature"
	ErrCodePermission  = "permission"
	ErrCodeCancelled   = "cancelled"
	ErrCodeUnknown     = "unknown"
)

// Reasons why automatic in-place updates are unavailable.
const (
	DisabledReasonNone            = ""
	DisabledReasonDevBuild        = "dev_build"
	DisabledReasonReadOnly        = "read_only_install"
	DisabledReasonManifestMissing = "manifest_missing"
)

// ErrNoReleasePublished indicates the repository currently has no published release.
var ErrNoReleasePublished = errors.New("no release is published")

// ColabOneRollbackVersion describes a recent older release available for rollback.
type ColabOneRollbackVersion struct {
	Version      string `json:"version"`
	Tag          string `json:"tag"`
	ReleaseDate  string `json:"release_date"`
	ReleaseURL   string `json:"release_url"`
	IsPrerelease bool   `json:"is_prerelease"`
	CanRollback  bool   `json:"can_rollback"`
}

// ColabOneUpdateState is the JSON-serializable state snapshot rendered by the UI.
type ColabOneUpdateState struct {
	Status            string                    `json:"status"`
	CurrentVersion    string                    `json:"current_version"`
	AvailableVersion  string                    `json:"available_version"`
	IsPrerelease      bool                      `json:"is_prerelease"`
	IsRollback        bool                      `json:"is_rollback"`
	ReleaseDate       string                    `json:"release_date"`
	ReleaseURL        string                    `json:"release_url"`
	NotesRaw          string                    `json:"notes_raw"`
	NotesZh           string                    `json:"notes_zh"`
	NotesEn           string                    `json:"notes_en"`
	DownloadedBytes   int64                     `json:"downloaded_bytes"`
	TotalBytes        int64                     `json:"total_bytes"`
	ProgressPercent   float64                   `json:"progress_percent"`
	SpeedBytesPerSec  float64                   `json:"speed_bps"`
	CanSelfUpdate     bool                      `json:"can_self_update"`
	DisabledReason    string                    `json:"disabled_reason"`
	AutoCheck         bool                      `json:"auto_check"`
	IncludePrerelease bool                      `json:"include_prerelease"`
	SkippedVersion    string                    `json:"skipped_version"`
	LastCheckedAt     int64                     `json:"last_checked_at"`
	ErrorCode         string                    `json:"error_code"`
	ErrorMessage      string                    `json:"error_message"`
	RollbackVersions  []ColabOneRollbackVersion `json:"rollback_versions"`
}

// ColabOneUpdatePreferences persists user updater choices in <configDir>/updater.json.
type ColabOneUpdatePreferences struct {
	AutoCheck         bool   `json:"auto_check"`
	IncludePrerelease bool   `json:"include_prerelease"`
	SkippedVersion    string `json:"skipped_version,omitempty"`
	LastCheckedAt     int64  `json:"last_checked_at,omitempty"`
}

// ColabOneBilingualNotes holds the parsed Chinese and English sections of a release note.
type ColabOneBilingualNotes struct {
	Raw string
	Zh  string
	En  string
}

// ColabOneReadOnlyRelease describes an update discovered from the public feed
// when the install directory is not writable for in-place replacement.
type ColabOneReadOnlyRelease struct {
	Version string
	Notes   string
	Date    time.Time
}

// ColabOneUpdateInstallable represents a verified update handle returned by mygo.Updater.Check.
type ColabOneUpdateInstallable interface {
	Version() string
	Notes() string
	Date() time.Time
	Install(ctx context.Context, progress func(downloaded, total int64)) error
}

// ColabOneUpdateBackend abstracts MyGo runtime calls for deterministic offline testing.
type ColabOneUpdateBackend interface {
	CurrentVersion() string
	IsDevBuild() bool
	CanSelfUpdate() bool
	SetPrereleases(on bool)
	CheckSelfUpdate(ctx context.Context) (ColabOneUpdateInstallable, error)
	CheckReadOnly(ctx context.Context, includePrerelease bool) (*ColabOneReadOnlyRelease, error)
	Relaunch() error
}

// colabOneReleaseMetadataBackend is an optional extension implemented by
// backends that can inspect GitHub Release metadata directly without re-fetching
// an already-404'd update-<target>.json manifest.
type colabOneReleaseMetadataBackend interface {
	CheckReleaseMetadata(ctx context.Context, includePrerelease bool) (*ColabOneReadOnlyRelease, error)
}

// colabOneRollbackBackend is an optional extension implemented by backends
// that support discovering recent older releases and installing Ed25519-signed
// historical release archives for version rollback.
type colabOneRollbackBackend interface {
	ListRollbackVersions(ctx context.Context, includePrerelease bool, currentVersion string, canSelfUpdate bool, maxCount int) ([]ColabOneRollbackVersion, error)
	PrepareRollback(ctx context.Context, targetVersion string) (ColabOneUpdateInstallable, error)
	RelaunchRollback() (bool, error)
}

// ColabOneUpdaterConfig configures a ColabOneUpdater instance.
type ColabOneUpdaterConfig struct {
	Backend       ColabOneUpdateBackend
	ConfigDir     string
	Repo          string
	InitialDelay  time.Duration
	CheckInterval time.Duration
	Now           func() time.Time
	OnStateChange func(ColabOneUpdateState)
	OnLog         func(message, source, level string)
}

// ColabOneUpdater orchestrates background/manual update checks, installation,
// cancellation, preference persistence, and restart handoff.
//
// Lock ordering: persistMu -> mu (never invert).
type ColabOneUpdater struct {
	backend       ColabOneUpdateBackend
	configDir     string
	repo          string
	initialDelay  time.Duration
	checkInterval time.Duration
	nowFn         func() time.Time
	onStateChange func(ColabOneUpdateState)
	onLog         func(message, source, level string)

	persistMu sync.Mutex
	mu        sync.Mutex
	state     ColabOneUpdateState
	prefs     ColabOneUpdatePreferences
	pending   ColabOneUpdateInstallable

	channelGen      uint64
	installGen      uint64
	checkInFlight   bool
	checkManual     bool
	checkWaiters    int
	checkCancel     context.CancelFunc
	checkDone       chan struct{}
	lastCheckErr    error
	installInFlight bool
	installCancel   context.CancelFunc
	installDone     chan struct{}
	rollbackCached   bool
	rollbackCacheAt  time.Time
	rollbackCacheKey string
	sessionChecked   bool
	bgStarted        bool
	closed           bool

	rootCtx    context.Context
	cancelRoot context.CancelFunc
	closeOnce  sync.Once
	wg         sync.WaitGroup
}

// NewColabOneUpdater constructs an updater initialized with persisted preferences.
func NewColabOneUpdater(cfg ColabOneUpdaterConfig) *ColabOneUpdater {
	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		repo = COLABONE_GITHUB_REPO
	}
	initDelay := cfg.InitialDelay
	if initDelay <= 0 {
		initDelay = COLABONE_UPDATE_INITIAL_DELAY
	}
	checkInterval := cfg.CheckInterval
	if checkInterval <= 0 {
		checkInterval = COLABONE_UPDATE_CHECK_INTERVAL
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	rootCtx, cancelRoot := context.WithCancel(context.Background())

	u := &ColabOneUpdater{
		backend:       cfg.Backend,
		configDir:     strings.TrimSpace(cfg.ConfigDir),
		repo:          repo,
		initialDelay:  initDelay,
		checkInterval: checkInterval,
		nowFn:         nowFn,
		onStateChange: cfg.OnStateChange,
		onLog:         cfg.OnLog,
		prefs: ColabOneUpdatePreferences{
			AutoCheck:         true,
			IncludePrerelease: false,
		},
		rootCtx:    rootCtx,
		cancelRoot: cancelRoot,
	}

	u.loadPreferences()
	u.initStateLocked()
	if u.backend != nil {
		u.backend.SetPrereleases(u.prefs.IncludePrerelease)
	}
	return u
}

func (u *ColabOneUpdater) initStateLocked() {
	current := ""
	canSelf := false
	isDev := true
	if u.backend != nil {
		current = NormalizeVersion(u.backend.CurrentVersion())
		isDev = u.backend.IsDevBuild()
		canSelf = u.backend.CanSelfUpdate()
	}

	disabledReason := DisabledReasonNone
	status := StatusIdle
	if isDev {
		disabledReason = DisabledReasonDevBuild
		status = StatusDisabled
	} else if !canSelf {
		disabledReason = DisabledReasonReadOnly
	}

	u.state = ColabOneUpdateState{
		Status:            status,
		CurrentVersion:    current,
		ReleaseURL:        ReleaseTagURL(u.repo, ""),
		CanSelfUpdate:     canSelf,
		DisabledReason:    disabledReason,
		AutoCheck:         u.prefs.AutoCheck,
		IncludePrerelease: u.prefs.IncludePrerelease,
		SkippedVersion:    u.prefs.SkippedVersion,
		LastCheckedAt:     u.prefs.LastCheckedAt,
		RollbackVersions:  []ColabOneRollbackVersion{},
	}
}

func cloneColabOneUpdateState(st ColabOneUpdateState) ColabOneUpdateState {
	if len(st.RollbackVersions) == 0 {
		st.RollbackVersions = []ColabOneRollbackVersion{}
	} else {
		cp := make([]ColabOneRollbackVersion, len(st.RollbackVersions))
		copy(cp, st.RollbackVersions)
		st.RollbackVersions = cp
	}
	return st
}

// State returns a thread-safe snapshot of the current update state.
func (u *ColabOneUpdater) State() ColabOneUpdateState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return cloneColabOneUpdateState(u.state)
}

// StartBackground launches the delayed startup check and periodic re-check loop.
func (u *ColabOneUpdater) StartBackground() {
	if u.backend == nil || u.backend.IsDevBuild() {
		return
	}
	u.mu.Lock()
	if u.bgStarted || u.closed {
		u.mu.Unlock()
		return
	}
	u.bgStarted = true
	u.wg.Add(1)
	u.mu.Unlock()
	go u.backgroundLoop()
}

// TriggerAsyncCheck schedules a lifecycle-tracked check bound to rootCtx so Close()
// cancels and awaits it cleanly.
func (u *ColabOneUpdater) TriggerAsyncCheck(manual bool) bool {
	if u.backend == nil || u.backend.IsDevBuild() {
		return false
	}
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return false
	}
	u.wg.Add(1)
	u.mu.Unlock()
	go func() {
		defer u.wg.Done()
		_, _ = u.Check(u.rootCtx, manual)
	}()
	return true
}

func (u *ColabOneUpdater) backgroundLoop() {
	defer u.wg.Done()

	initTimer := time.NewTimer(u.initialDelay)
	defer initTimer.Stop()

	select {
	case <-u.rootCtx.Done():
		return
	case <-initTimer.C:
	}

	u.maybeAutoCheck(true)

	ticker := time.NewTicker(u.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-u.rootCtx.Done():
			return
		case <-ticker.C:
			u.maybeAutoCheck(false)
		}
	}
}

func (u *ColabOneUpdater) maybeAutoCheck(initial bool) {
	u.mu.Lock()
	auto := u.prefs.AutoCheck
	skipInitial := initial && u.sessionChecked
	busy := u.closed || u.checkInFlight || u.installInFlight || u.state.Status == StatusReadyToRestart
	u.mu.Unlock()
	if !auto || busy || skipInitial {
		return
	}
	_, _ = u.Check(u.rootCtx, false)
}

// Close stops background checks and cancels any in-flight check or download.
func (u *ColabOneUpdater) Close() {
	u.closeOnce.Do(func() {
		u.mu.Lock()
		u.closed = true
		installCancel := u.installCancel
		u.installCancel = nil
		checkCancel := u.checkCancel
		u.checkCancel = nil
		u.mu.Unlock()
		if checkCancel != nil {
			checkCancel()
		}
		if installCancel != nil {
			installCancel()
		}
		u.cancelRoot()
		u.wg.Wait()
	})
}

// Check queries GitHub Releases for a newer version.
// When manual is false (background check), errors are logged silently without
// surfacing an error banner, and a previously skipped version is ignored.
// If a manual check arrives while a silent background check is already in flight,
// it promotes that check to manual and awaits its result.
func (u *ColabOneUpdater) Check(ctx context.Context, manual bool) (ColabOneUpdateState, error) {
	if ctx == nil {
		ctx = u.rootCtx
	}
	if u.backend == nil || u.backend.IsDevBuild() {
		return u.State(), nil
	}

	reusingFlight := false
	var doneCh chan struct{}
	var prevStatus string

	for {
		u.mu.Lock()
		if u.closed || u.installInFlight || u.state.Status == StatusReadyToRestart {
			closedNow := u.closed
			st := u.state
			if reusingFlight {
				u.checkInFlight = false
				u.checkManual = false
				u.checkCancel = nil
				u.checkDone = nil
				if closedNow {
					u.lastCheckErr = context.Canceled
				} else {
					u.lastCheckErr = nil
				}
			}
			u.mu.Unlock()
			if reusingFlight && doneCh != nil {
				close(doneCh)
			}
			if closedNow {
				return st, context.Canceled
			}
			return st, nil
		}
		if !reusingFlight && u.checkInFlight {
			waitCh := u.checkDone
			var promoteSnap *ColabOneUpdateState
			if manual && !u.checkManual {
				u.checkManual = true
				u.state.Status = StatusChecking
				u.state.ErrorCode = ErrCodeNone
				u.state.ErrorMessage = ""
				cp := u.state
				promoteSnap = &cp
			}
			if manual && waitCh != nil {
				u.checkWaiters++
			}
			st := u.state
			u.mu.Unlock()

			if promoteSnap != nil {
				u.emit(*promoteSnap)
				u.log("正在检查 ColabOne 客户端更新…", "更新", "info")
			}
			if !manual || waitCh == nil {
				return st, nil
			}
			select {
			case <-waitCh:
				u.mu.Lock()
				u.checkWaiters--
				finalSt := u.state
				finalErr := u.lastCheckErr
				u.mu.Unlock()
				return finalSt, finalErr
			case <-ctx.Done():
				u.mu.Lock()
				u.checkWaiters--
				finalSt := u.state
				u.mu.Unlock()
				return finalSt, ctx.Err()
			}
		}

		// Start (or continue on channel toggle) the single-flight check.
		startGen := u.channelGen
		startInstallGen := u.installGen
		if !reusingFlight {
			prevStatus = u.state.Status
			doneCh = make(chan struct{})
			u.checkInFlight = true
			u.checkManual = manual
			u.checkDone = doneCh
			u.lastCheckErr = nil
			reusingFlight = true
		}
		includePre := u.prefs.IncludePrerelease
		canSelf := u.backend.CanSelfUpdate()
		current := NormalizeVersion(u.backend.CurrentVersion())

		checkCtx, checkCancel := context.WithCancel(u.rootCtx)
		u.checkCancel = checkCancel

		u.state.CurrentVersion = current
		u.state.CanSelfUpdate = canSelf
		if canSelf {
			u.state.DisabledReason = DisabledReasonNone
		} else {
			u.state.DisabledReason = DisabledReasonReadOnly
		}
		emitChecking := manual && u.state.Status != StatusChecking
		if manual {
			if u.state.IsRollback {
				u.state.IsRollback = false
				u.state.AvailableVersion = ""
				u.state.IsPrerelease = false
				u.state.ReleaseDate = ""
				u.state.ReleaseURL = ReleaseTagURL(u.repo, "")
			}
			u.state.Status = StatusChecking
			u.state.ErrorCode = ErrCodeNone
			u.state.ErrorMessage = ""
		}
		checkingSnap := u.state
		u.mu.Unlock()

		// Forward caller context cancellation to checkCtx without leaking goroutines.
		stopForward := context.AfterFunc(ctx, checkCancel)

		if emitChecking {
			u.emit(checkingSnap)
			u.log("正在检查 ColabOne 客户端更新…", "更新", "info")
		}

		u.backend.SetPrereleases(includePre)

		baseDisabledReason := DisabledReasonNone
		if !canSelf {
			baseDisabledReason = DisabledReasonReadOnly
		}
		effectiveCanSelf := canSelf
		effectiveDisabledReason := baseDisabledReason

		var (
			foundVersion string
			foundNotes   string
			foundDate    time.Time
			installable  ColabOneUpdateInstallable
			checkErr     error
		)

		if canSelf {
			installable, checkErr = u.backend.CheckSelfUpdate(checkCtx)
			if checkErr == nil && installable != nil {
				foundVersion = NormalizeVersion(installable.Version())
				foundNotes = installable.Notes()
				foundDate = installable.Date()
			} else if checkErr != nil && IsManifestNotFoundError(checkErr) {
				// The latest GitHub Release does not publish update-<target>.json yet
				// (e.g. an older release published before signed auto-updates or a
				// manual/partial release). Fall back to GitHub Release metadata so
				// up-to-date users see StatusUpToDate and newer unsigned releases
				// offer one-click manual download instead of a false network error.
				var (
					ro    *ColabOneReadOnlyRelease
					roErr error
				)
				if metaBackend, ok := u.backend.(colabOneReleaseMetadataBackend); ok {
					ro, roErr = metaBackend.CheckReleaseMetadata(checkCtx, includePre)
				} else {
					ro, roErr = u.backend.CheckReadOnly(checkCtx, includePre)
				}
				switch {
				case roErr == nil:
					checkErr = nil
					if ro != nil {
						foundVersion = NormalizeVersion(ro.Version)
						foundNotes = ro.Notes
						foundDate = ro.Date
						effectiveCanSelf = false
						effectiveDisabledReason = DisabledReasonManifestMissing
					}
				case errors.Is(roErr, ErrNoReleasePublished):
					checkErr = nil
				default:
					checkErr = roErr
				}
			}
		} else {
			var ro *ColabOneReadOnlyRelease
			ro, checkErr = u.backend.CheckReadOnly(checkCtx, includePre)
			if checkErr == nil && ro != nil {
				foundVersion = NormalizeVersion(ro.Version)
				foundNotes = ro.Notes
				foundDate = ro.Date
			} else if errors.Is(checkErr, ErrNoReleasePublished) {
				checkErr = nil
			}
		}

		stopForward()
		checkCancel()

		nowSec := u.nowFn().Unix()

		u.mu.Lock()
		effectiveManual := manual || u.checkManual
		channelChanged := u.channelGen != startGen
		installStarted := u.installInFlight || u.state.Status == StatusReadyToRestart || u.installGen != startInstallGen
		closedNow := u.closed
		skipped := u.prefs.SkippedVersion

		if !closedNow && !installStarted && channelChanged && ctx.Err() == nil {
			// Prerelease channel toggled while this request was in flight:
			// discard stale results and immediately re-run on the new channel,
			// keeping checkInFlight=true and doneCh open so coalesced waiters
			// receive the fresh result without waking early.
			u.checkCancel = nil
			u.checkManual = effectiveManual
			u.mu.Unlock()
			manual = effectiveManual
			continue
		}

		u.checkInFlight = false
		u.checkManual = false
		u.checkCancel = nil
		u.checkDone = nil

		if closedNow {
			u.lastCheckErr = context.Canceled
			st := u.state
			u.mu.Unlock()
			close(doneCh)
			return st, context.Canceled
		}

		if installStarted {
			u.lastCheckErr = nil
			st := u.state
			u.mu.Unlock()
			close(doneCh)
			return st, nil
		}

		if checkErr != nil {
			u.lastCheckErr = checkErr
			code, detail := ClassifyUpdateError(checkErr)
			logMsg := formatUpdateErrorLog("检查客户端更新失败", code, detail)
			if effectiveManual && !closedNow {
				u.state.Status = StatusError
				u.state.ErrorCode = code
				u.state.ErrorMessage = detail
				u.state.LastCheckedAt = nowSec
				u.prefs.LastCheckedAt = nowSec
				st := u.state
				u.mu.Unlock()
				close(doneCh)
				u.savePreferences()
				u.emit(st)
				u.log(logMsg, "更新", "warning")
				return st, checkErr
			}
			// Silent background failure: preserve all live preferences/state fields.
			if u.state.Status == StatusChecking {
				u.state.Status = prevStatus
			}
			u.state.CurrentVersion = current
			u.state.CanSelfUpdate = canSelf
			st := u.state
			u.mu.Unlock()
			close(doneCh)
			if !closedNow {
				u.log(formatUpdateErrorLog("后台检查客户端更新失败", code, detail), "更新", "info")
			}
			return st, checkErr
		}

		u.sessionChecked = true
		u.prefs.LastCheckedAt = nowSec
		u.state.LastCheckedAt = nowSec
		u.state.ErrorCode = ErrCodeNone
		u.state.ErrorMessage = ""
		u.state.DownloadedBytes = 0
		u.state.TotalBytes = 0
		u.state.ProgressPercent = 0
		u.state.SpeedBytesPerSec = 0

		hasNewer := foundVersion != "" && (current == "" || CompareVersions(foundVersion, current) > 0)
		if !hasNewer {
			u.pending = nil
			u.state.Status = StatusUpToDate
			u.state.IsRollback = false
			u.state.CanSelfUpdate = canSelf
			u.state.DisabledReason = baseDisabledReason
			u.state.AvailableVersion = ""
			u.state.IsPrerelease = false
			u.state.ReleaseDate = ""
			u.state.ReleaseURL = ReleaseTagURL(u.repo, "")
			u.state.NotesRaw = ""
			u.state.NotesZh = ""
			u.state.NotesEn = ""
			st := cloneColabOneUpdateState(u.state)
			u.mu.Unlock()
			close(doneCh)
			u.savePreferences()
			u.emit(st)
			if effectiveManual {
				u.log("ColabOne 已是最新版本（v"+current+"）", "更新", "info")
			}
			return st, nil
		}

		if !effectiveManual && skipped != "" && CompareVersions(foundVersion, skipped) == 0 {
			// Honor user's "Skip This Version" choice for silent background checks.
			u.pending = nil
			u.state.Status = StatusUpToDate
			u.state.IsRollback = false
			u.state.CanSelfUpdate = canSelf
			u.state.DisabledReason = baseDisabledReason
			u.state.AvailableVersion = ""
			u.state.IsPrerelease = false
			u.state.ReleaseDate = ""
			u.state.ReleaseURL = ReleaseTagURL(u.repo, "")
			u.state.NotesRaw = ""
			u.state.NotesZh = ""
			u.state.NotesEn = ""
			st := cloneColabOneUpdateState(u.state)
			u.mu.Unlock()
			close(doneCh)
			u.savePreferences()
			u.emit(st)
			return st, nil
		}

		parsedNotes := ParseBilingualNotes(foundNotes)
		dateStr := ""
		if !foundDate.IsZero() {
			dateStr = foundDate.UTC().Format(time.RFC3339)
		}

		u.pending = installable
		u.state.Status = StatusAvailable
		u.state.IsRollback = false
		u.state.CanSelfUpdate = effectiveCanSelf
		u.state.DisabledReason = effectiveDisabledReason
		u.state.AvailableVersion = foundVersion
		u.state.IsPrerelease = IsPrereleaseVersion(foundVersion)
		u.state.ReleaseDate = dateStr
		u.state.ReleaseURL = ReleaseTagURL(u.repo, foundVersion)
		u.state.NotesRaw = parsedNotes.Raw
		u.state.NotesZh = parsedNotes.Zh
		u.state.NotesEn = parsedNotes.En
		st := cloneColabOneUpdateState(u.state)
		u.mu.Unlock()

		close(doneCh)
		u.savePreferences()
		u.emit(st)
		u.log("发现 ColabOne 新版本 v"+foundVersion, "更新", "info")
		return st, nil
	}
}

// ListRollbackVersions discovers up to COLABONE_ROLLBACK_MAX_VERSIONS recent
// published releases older than the running version. Results are cached for
// COLABONE_ROLLBACK_CACHE_TTL unless forceRefresh is true.
func (u *ColabOneUpdater) ListRollbackVersions(ctx context.Context, forceRefresh bool) ([]ColabOneRollbackVersion, error) {
	if ctx == nil {
		ctx = u.rootCtx
	}
	if u.backend == nil || u.backend.IsDevBuild() {
		return []ColabOneRollbackVersion{}, nil
	}
	rbBackend, ok := u.backend.(colabOneRollbackBackend)
	if !ok {
		return []ColabOneRollbackVersion{}, nil
	}

	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return []ColabOneRollbackVersion{}, context.Canceled
	}
	startGen := u.channelGen
	includePre := u.prefs.IncludePrerelease
	current := NormalizeVersion(u.backend.CurrentVersion())
	canSelf := u.backend.CanSelfUpdate()
	cacheKey := fmt.Sprintf("%s|%t|%t", current, includePre, canSelf)
	if !forceRefresh && u.rollbackCached && u.rollbackCacheKey == cacheKey && u.nowFn().Sub(u.rollbackCacheAt) < COLABONE_ROLLBACK_CACHE_TTL {
		out := make([]ColabOneRollbackVersion, len(u.state.RollbackVersions))
		copy(out, u.state.RollbackVersions)
		u.mu.Unlock()
		return out, nil
	}
	u.mu.Unlock()

	reqCtx, reqCancel := context.WithCancel(u.rootCtx)
	stopForward := context.AfterFunc(ctx, reqCancel)
	versions, err := rbBackend.ListRollbackVersions(reqCtx, includePre, current, canSelf, COLABONE_ROLLBACK_MAX_VERSIONS)
	stopForward()
	reqCancel()
	if err != nil {
		return nil, err
	}
	if versions == nil {
		versions = []ColabOneRollbackVersion{}
	}

	u.mu.Lock()
	if !u.closed && u.channelGen == startGen {
		cp := make([]ColabOneRollbackVersion, len(versions))
		copy(cp, versions)
		u.state.RollbackVersions = cp
		u.rollbackCached = true
		u.rollbackCacheAt = u.nowFn()
		u.rollbackCacheKey = cacheKey
		st := cloneColabOneUpdateState(u.state)
		u.mu.Unlock()
		u.emit(st)
		return versions, nil
	}
	u.mu.Unlock()
	return versions, nil
}

// StartRollback asynchronously fetches the signed manifest for an older version,
// verifies its Ed25519 signature, replaces the local app files, and transitions
// to StatusReadyToRestart with IsRollback=true.
func (u *ColabOneUpdater) StartRollback(version string) (ColabOneUpdateState, error) {
	norm := NormalizeVersion(version)
	if !IsValidSemVer(norm) {
		return u.State(), errors.New("回退版本号格式无效")
	}
	if u.backend == nil || u.backend.IsDevBuild() {
		return u.State(), errors.New("当前为开发构建，无法执行版本回退")
	}
	rbBackend, ok := u.backend.(colabOneRollbackBackend)
	if !ok {
		return u.State(), errors.New("当前更新后端不支持版本回退")
	}

	u.mu.Lock()
	if u.closed {
		st := cloneColabOneUpdateState(u.state)
		u.mu.Unlock()
		return st, errors.New("更新服务已关闭")
	}
	if u.installInFlight {
		st := cloneColabOneUpdateState(u.state)
		u.mu.Unlock()
		return st, nil
	}
	if u.state.Status == StatusReadyToRestart {
		st := cloneColabOneUpdateState(u.state)
		u.mu.Unlock()
		return st, nil
	}
	current := NormalizeVersion(u.backend.CurrentVersion())
	if current != "" && CompareVersions(norm, current) >= 0 {
		st := cloneColabOneUpdateState(u.state)
		u.mu.Unlock()
		return st, errors.New("只能回退到低于当前版本的历史版本")
	}
	canSelf := u.backend.CanSelfUpdate()
	if !canSelf {
		st := cloneColabOneUpdateState(u.state)
		u.mu.Unlock()
		return st, errors.New("当前安装目录缺少写入权限，无法自动回退，请前往 GitHub Releases 手动下载")
	}

	prevState := cloneColabOneUpdateState(u.state)
	installCtx, cancel := context.WithCancel(u.rootCtx)
	doneCh := make(chan struct{})
	u.installGen++
	u.installCancel = cancel
	u.installDone = doneCh
	u.installInFlight = true
	checkCancel := u.checkCancel

	u.state.Status = StatusDownloading
	u.state.CurrentVersion = current
	u.state.AvailableVersion = norm
	u.state.IsPrerelease = IsPrereleaseVersion(norm)
	u.state.IsRollback = true
	u.state.CanSelfUpdate = canSelf
	u.state.DisabledReason = DisabledReasonNone
	u.state.ReleaseURL = ReleaseTagURL(u.repo, norm)
	u.state.DownloadedBytes = 0
	u.state.TotalBytes = 0
	u.state.ProgressPercent = 0
	u.state.SpeedBytesPerSec = 0
	u.state.ErrorCode = ErrCodeNone
	u.state.ErrorMessage = ""
	startSnap := cloneColabOneUpdateState(u.state)
	u.wg.Add(1)
	u.mu.Unlock()

	if checkCancel != nil {
		checkCancel()
	}

	u.emit(startSnap)
	u.log("开始下载 ColabOne v"+norm+" 回退安装包…", "更新", "info")

	go func() {
		defer u.wg.Done()
		defer close(doneCh)
		pending, prepErr := rbBackend.PrepareRollback(installCtx, norm)
		if prepErr != nil {
			u.finishInstallError(installCtx, cancel, norm, true, prevState, prepErr)
			return
		}
		u.runInstall(installCtx, cancel, pending, norm, true, prevState)
	}()

	return startSnap, nil
}

// StartInstall asynchronously downloads, verifies (Ed25519), and applies the
// currently available update. It transitions state through downloading ->
// installing -> ready_to_restart (or error / available on cancel).
func (u *ColabOneUpdater) StartInstall() (ColabOneUpdateState, error) {
	u.mu.Lock()
	if u.closed {
		st := u.state
		u.mu.Unlock()
		return st, errors.New("更新服务已关闭")
	}
	if u.installInFlight {
		st := u.state
		u.mu.Unlock()
		return st, nil
	}
	if u.state.Status == StatusReadyToRestart {
		st := u.state
		u.mu.Unlock()
		return st, nil
	}
	if !u.state.CanSelfUpdate || u.pending == nil || u.state.AvailableVersion == "" {
		st := u.state
		u.mu.Unlock()
		return st, errors.New("当前无可自动安装的更新版本")
	}

	prevState := cloneColabOneUpdateState(u.state)
	installCtx, cancel := context.WithCancel(u.rootCtx)
	doneCh := make(chan struct{})
	u.installGen++
	u.installCancel = cancel
	u.installDone = doneCh
	u.installInFlight = true
	checkCancel := u.checkCancel
	pending := u.pending
	targetVersion := u.state.AvailableVersion
	u.state.Status = StatusDownloading
	u.state.IsRollback = false
	u.state.DownloadedBytes = 0
	u.state.TotalBytes = 0
	u.state.ProgressPercent = 0
	u.state.SpeedBytesPerSec = 0
	u.state.ErrorCode = ErrCodeNone
	u.state.ErrorMessage = ""
	startSnap := cloneColabOneUpdateState(u.state)
	u.wg.Add(1)
	u.mu.Unlock()

	if checkCancel != nil {
		checkCancel()
	}

	u.emit(startSnap)
	u.log("开始下载 ColabOne v"+targetVersion+" 更新包…", "更新", "info")

	go func() {
		defer u.wg.Done()
		defer close(doneCh)
		u.runInstall(installCtx, cancel, pending, targetVersion, false, prevState)
	}()

	return startSnap, nil
}

func (u *ColabOneUpdater) finishInstallError(
	installCtx context.Context,
	cancel context.CancelFunc,
	targetVersion string,
	isRollback bool,
	prevState ColabOneUpdateState,
	err error,
) {
	defer cancel()

	u.mu.Lock()
	u.installInFlight = false
	u.installCancel = nil
	u.installDone = nil

	if errors.Is(err, context.Canceled) || installCtx.Err() != nil {
		if isRollback {
			restoreStatus := prevState.Status
			if restoreStatus == "" ||
				restoreStatus == StatusChecking ||
				restoreStatus == StatusDownloading ||
				restoreStatus == StatusInstalling ||
				restoreStatus == StatusError ||
				(restoreStatus == StatusAvailable && strings.TrimSpace(prevState.AvailableVersion) == "") {
				restoreStatus = StatusUpToDate
			}
			u.state.Status = restoreStatus
			u.state.IsRollback = false
			if restoreStatus == StatusAvailable {
				u.state.AvailableVersion = prevState.AvailableVersion
				u.state.IsPrerelease = prevState.IsPrerelease
				u.state.ReleaseDate = prevState.ReleaseDate
				u.state.ReleaseURL = prevState.ReleaseURL
				u.state.NotesRaw = prevState.NotesRaw
				u.state.NotesZh = prevState.NotesZh
				u.state.NotesEn = prevState.NotesEn
			} else {
				u.state.AvailableVersion = ""
				u.state.IsPrerelease = false
				u.state.ReleaseDate = ""
				u.state.ReleaseURL = ReleaseTagURL(u.repo, "")
				u.state.NotesRaw = ""
				u.state.NotesZh = ""
				u.state.NotesEn = ""
			}
		} else {
			u.state.Status = StatusAvailable
			u.state.IsRollback = false
		}
		u.state.DownloadedBytes = 0
		u.state.TotalBytes = 0
		u.state.ProgressPercent = 0
		u.state.SpeedBytesPerSec = 0
		u.state.ErrorCode = ErrCodeNone
		u.state.ErrorMessage = ""
		snap := cloneColabOneUpdateState(u.state)
		u.mu.Unlock()
		u.emit(snap)
		if isRollback {
			u.log("已取消下载 ColabOne v"+targetVersion+" 回退包", "更新", "info")
		} else {
			u.log("已取消下载 ColabOne v"+targetVersion+" 更新", "更新", "info")
		}
		return
	}

	code, detail := ClassifyUpdateError(err)
	u.state.Status = StatusError
	u.state.IsRollback = isRollback
	u.state.SpeedBytesPerSec = 0
	u.state.ErrorCode = code
	u.state.ErrorMessage = detail
	snap := cloneColabOneUpdateState(u.state)
	u.mu.Unlock()
	u.emit(snap)
	if isRollback {
		u.log(formatUpdateErrorLog("回退 ColabOne 至 v"+targetVersion+" 失败", code, detail), "更新", "error")
	} else {
		u.log(formatUpdateErrorLog("安装 ColabOne v"+targetVersion+" 更新失败", code, detail), "更新", "error")
	}
}

func (u *ColabOneUpdater) runInstall(
	installCtx context.Context,
	cancel context.CancelFunc,
	pending ColabOneUpdateInstallable,
	targetVersion string,
	isRollback bool,
	prevState ColabOneUpdateState,
) {
	defer cancel()

	var (
		progMu        sync.Mutex
		windowStart   = u.nowFn()
		windowBytes   int64
		lastEmitAt    time.Time
		lastTotal     int64
		lastBytes     int64
		switchedPhase bool
	)

	onProgress := func(downloaded, total int64) {
		progMu.Lock()
		defer progMu.Unlock()

		now := u.nowFn()
		wasInstalling := switchedPhase
		// Detect delta -> full-archive fallback reset cleanly.
		if downloaded < lastBytes || (total > 0 && lastTotal > 0 && total != lastTotal) {
			windowStart = now
			windowBytes = downloaded
			switchedPhase = false
		}
		lastBytes = downloaded
		lastTotal = total

		elapsed := now.Sub(windowStart).Seconds()
		speed := 0.0
		if elapsed > 0.05 && downloaded >= windowBytes {
			speed = float64(downloaded-windowBytes) / elapsed
		}
		if elapsed > 3.0 {
			windowStart = now
			windowBytes = downloaded
		}

		pct := 0.0
		if total > 0 {
			pct = (float64(downloaded) / float64(total)) * 100.0
			if pct > 100 {
				pct = 100
			} else if pct < 0 {
				pct = 0
			}
		}

		if total > 0 && downloaded >= total {
			switchedPhase = true
		}
		phase := StatusDownloading
		if switchedPhase {
			phase = StatusInstalling
		}
		phaseChanged := switchedPhase != wasInstalling

		shouldEmit := phaseChanged || lastEmitAt.IsZero() || now.Sub(lastEmitAt) >= COLABONE_UPDATE_PROGRESS_MIN_GAP
		if !shouldEmit {
			return
		}
		lastEmitAt = now

		u.mu.Lock()
		if !u.installInFlight {
			u.mu.Unlock()
			return
		}
		u.state.Status = phase
		u.state.DownloadedBytes = downloaded
		u.state.TotalBytes = total
		u.state.ProgressPercent = pct
		u.state.SpeedBytesPerSec = speed
		snap := u.state
		u.mu.Unlock()

		u.emit(snap)
	}

	err := pending.Install(installCtx, onProgress)
	if err != nil {
		u.finishInstallError(installCtx, cancel, targetVersion, isRollback, prevState, err)
		return
	}

	u.mu.Lock()
	u.installInFlight = false
	u.installCancel = nil
	u.installDone = nil
	u.state.Status = StatusReadyToRestart
	u.state.IsRollback = isRollback
	if u.state.TotalBytes > 0 {
		u.state.DownloadedBytes = u.state.TotalBytes
	}
	u.state.ProgressPercent = 100
	u.state.SpeedBytesPerSec = 0
	u.state.ErrorCode = ErrCodeNone
	u.state.ErrorMessage = ""
	snap := cloneColabOneUpdateState(u.state)
	u.mu.Unlock()

	u.emit(snap)
	if isRollback {
		u.log("ColabOne v"+targetVersion+" 回退版本已就绪，重启客户端后生效", "更新", "info")
	} else {
		u.log("ColabOne v"+targetVersion+" 更新已就绪，重启客户端后生效", "更新", "info")
	}
}

// CancelInstall aborts an in-progress update download and waits briefly for the
// install goroutine to settle back into StatusAvailable.
func (u *ColabOneUpdater) CancelInstall() ColabOneUpdateState {
	u.mu.Lock()
	cancel := u.installCancel
	inFlight := u.installInFlight
	doneCh := u.installDone
	st := u.state
	u.mu.Unlock()

	if inFlight && cancel != nil {
		cancel()
		if doneCh != nil {
			timer := time.NewTimer(colabOneCancelWaitTimeout)
			defer timer.Stop()
			select {
			case <-doneCh:
			case <-timer.C:
			}
		}
		return u.State()
	}
	return st
}

// SkipVersion suppresses silent background notifications for the given version.
func (u *ColabOneUpdater) SkipVersion(version string) (ColabOneUpdateState, error) {
	norm := NormalizeVersion(version)
	if norm == "" {
		return u.State(), errors.New("版本号不能为空")
	}

	u.mu.Lock()
	if u.installInFlight || u.state.Status == StatusReadyToRestart {
		st := u.state
		u.mu.Unlock()
		return st, nil
	}
	u.prefs.SkippedVersion = norm
	u.state.SkippedVersion = norm
	if u.state.Status == StatusAvailable && CompareVersions(u.state.AvailableVersion, norm) == 0 {
		u.pending = nil
		u.state.Status = StatusUpToDate
		u.state.AvailableVersion = ""
		u.state.IsPrerelease = false
		u.state.ReleaseDate = ""
		u.state.ReleaseURL = ReleaseTagURL(u.repo, "")
		u.state.NotesRaw = ""
		u.state.NotesZh = ""
		u.state.NotesEn = ""
	}
	st := u.state
	u.mu.Unlock()

	u.savePreferences()
	u.emit(st)
	u.log("已跳过 ColabOne v"+norm+" 更新提醒", "更新", "info")
	return st, nil
}

// Preferences returns a thread-safe copy of the persisted updater preferences.
func (u *ColabOneUpdater) Preferences() ColabOneUpdatePreferences {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.prefs
}

// ApplyImportedPreferences replaces portable updater preferences from a
// migration archive and broadcasts the updated state immediately.
func (u *ColabOneUpdater) ApplyImportedPreferences(autoCheck, includePrerelease bool, skippedVersion string) (ColabOneUpdateState, bool, error) {
	normSkipped := NormalizeVersion(skippedVersion)
	u.mu.Lock()
	channelChanged := u.prefs.IncludePrerelease != includePrerelease
	u.prefs.AutoCheck = autoCheck
	u.prefs.IncludePrerelease = includePrerelease
	u.prefs.SkippedVersion = normSkipped
	u.state.AutoCheck = autoCheck
	u.state.IncludePrerelease = includePrerelease
	u.state.SkippedVersion = normSkipped
	var cancelCheck context.CancelFunc
	if channelChanged {
		u.channelGen++
		cancelCheck = u.checkCancel
		u.sessionChecked = false
		u.rollbackCached = false
		u.rollbackCacheKey = ""
		u.state.RollbackVersions = []ColabOneRollbackVersion{}
	}
	if normSkipped != "" && u.state.Status == StatusAvailable && CompareVersions(u.state.AvailableVersion, normSkipped) == 0 && !u.installInFlight {
		u.pending = nil
		u.state.Status = StatusUpToDate
		u.state.AvailableVersion = ""
		u.state.IsPrerelease = false
		u.state.ReleaseDate = ""
		u.state.ReleaseURL = ReleaseTagURL(u.repo, "")
		u.state.NotesRaw = ""
		u.state.NotesZh = ""
		u.state.NotesEn = ""
	}
	st := u.state
	u.mu.Unlock()

	if u.backend != nil {
		u.backend.SetPrereleases(includePrerelease)
	}
	if cancelCheck != nil {
		cancelCheck()
	}
	u.savePreferences()
	u.emit(st)
	return st, channelChanged, nil
}

// SetPreferences updates auto-check and prerelease preferences immediately.
// If includePrerelease changes while a check is in flight, the stale check is
// cancelled and retried automatically on the new channel.
func (u *ColabOneUpdater) SetPreferences(autoCheck, includePrerelease bool) (ColabOneUpdateState, bool, error) {
	u.mu.Lock()
	channelChanged := u.prefs.IncludePrerelease != includePrerelease
	u.prefs.AutoCheck = autoCheck
	u.prefs.IncludePrerelease = includePrerelease
	u.state.AutoCheck = autoCheck
	u.state.IncludePrerelease = includePrerelease
	var cancelCheck context.CancelFunc
	if channelChanged {
		u.channelGen++
		cancelCheck = u.checkCancel
		u.sessionChecked = false
		u.rollbackCached = false
		u.rollbackCacheKey = ""
		u.state.RollbackVersions = []ColabOneRollbackVersion{}
	}
	st := u.state
	u.mu.Unlock()

	if u.backend != nil {
		u.backend.SetPrereleases(includePrerelease)
	}
	if cancelCheck != nil {
		cancelCheck()
	}
	u.savePreferences()
	u.emit(st)
	return st, channelChanged, nil
}

// Relaunch triggers application restart once an update or rollback has been staged/replaced.
func (u *ColabOneUpdater) Relaunch() error {
	u.mu.Lock()
	ready := u.state.Status == StatusReadyToRestart
	targetVersion := u.state.AvailableVersion
	isRollback := u.state.IsRollback
	u.mu.Unlock()

	if !ready {
		return errors.New("更新尚未安装完成，暂无法重启")
	}
	if u.backend == nil {
		return errors.New("更新后端未初始化")
	}
	if isRollback {
		u.log("正在重启 ColabOne 以回退至 v"+targetVersion+"…", "更新", "info")
		if rbBackend, ok := u.backend.(colabOneRollbackBackend); ok {
			handled, err := rbBackend.RelaunchRollback()
			if err != nil {
				return err
			}
			if handled {
				return nil
			}
		}
	} else {
		u.log("正在重启 ColabOne 以切换到 v"+targetVersion+"…", "更新", "info")
	}
	return u.backend.Relaunch()
}

func (u *ColabOneUpdater) emit(st ColabOneUpdateState) {
	if u.onStateChange != nil {
		u.onStateChange(cloneColabOneUpdateState(st))
	}
}

func (u *ColabOneUpdater) log(message, source, level string) {
	if u.onLog != nil {
		u.onLog(message, source, level)
	}
}

func (u *ColabOneUpdater) prefsPath() string {
	if u.configDir == "" {
		return ""
	}
	return filepath.Join(u.configDir, COLABONE_UPDATE_PREF_FILE)
}

func (u *ColabOneUpdater) loadPreferences() {
	path := u.prefsPath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var parsed struct {
		AutoCheck         *bool  `json:"auto_check"`
		IncludePrerelease *bool  `json:"include_prerelease"`
		SkippedVersion    string `json:"skipped_version"`
		LastCheckedAt     int64  `json:"last_checked_at"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return
	}
	if parsed.AutoCheck != nil {
		u.prefs.AutoCheck = *parsed.AutoCheck
	}
	if parsed.IncludePrerelease != nil {
		u.prefs.IncludePrerelease = *parsed.IncludePrerelease
	}
	u.prefs.SkippedVersion = NormalizeVersion(parsed.SkippedVersion)
	if parsed.LastCheckedAt > 0 {
		u.prefs.LastCheckedAt = parsed.LastCheckedAt
	}
}

func (u *ColabOneUpdater) savePreferences() {
	path := u.prefsPath()
	if path == "" {
		return
	}
	// Lock ordering: persistMu -> mu so concurrent writers never overwrite newer prefs.
	u.persistMu.Lock()
	defer u.persistMu.Unlock()

	u.mu.Lock()
	snapshot := u.prefs
	u.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".updater-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := colabOneRenameFile(tmpPath, path); err != nil {
		return
	}
	ok = true
}

func colabOneIsTransientWinRenameErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case colabOneWinErrFileNotFound,
			colabOneWinErrAccessDenied,
			colabOneWinErrShareViol,
			colabOneWinErrLockViolation:
			return true
		}
	}
	return false
}

func colabOneRenameFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	for attempt := 1; attempt < colabOneWinRenameAttempts; attempt++ {
		if !colabOneIsTransientWinRenameErr(err) {
			return err
		}
		time.Sleep(time.Duration(attempt) * colabOneWinRenameBaseDelay)
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
	}
	return err
}

// ---------------------------------------------------------------------------
// Version, Bilingual Notes, and Error Helpers
// ---------------------------------------------------------------------------

// NormalizeVersion strips leading whitespace and optional v/V prefixes.
func NormalizeVersion(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimLeft(s, "vV")
	return strings.TrimSpace(s)
}

// IsPrereleaseVersion reports whether a SemVer string carries a prerelease suffix.
func IsPrereleaseVersion(raw string) bool {
	norm := NormalizeVersion(raw)
	if norm == "" {
		return false
	}
	norm, _, _ = strings.Cut(norm, "+")
	return strings.Contains(norm, "-")
}

// ReleaseTagURL returns the GitHub release URL for version (or the releases index when empty).
func ReleaseTagURL(repo, version string) string {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	if repo == "" {
		repo = COLABONE_GITHUB_REPO
	}
	norm := NormalizeVersion(version)
	if norm == "" {
		return "https://github.com/" + repo + "/releases"
	}
	return "https://github.com/" + repo + "/releases/tag/v" + norm
}

// TargetPlatform returns the MyGo manifest target key (e.g. "windows-amd64", "darwin-arm64").
func TargetPlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// CompareVersions compares two SemVer strings: returns -1, 0, or 1.
func CompareVersions(a, b string) int {
	a = NormalizeVersion(a)
	b = NormalizeVersion(b)
	a, _, _ = strings.Cut(a, "+")
	b, _, _ = strings.Cut(b, "+")
	aCore, aPre, _ := strings.Cut(a, "-")
	bCore, bPre, _ := strings.Cut(b, "-")
	if c := compareDotParts(aCore, bCore); c != 0 {
		return c
	}
	switch {
	case aPre == bPre:
		return 0
	case aPre == "":
		return 1
	case bPre == "":
		return -1
	default:
		return compareDotParts(aPre, bPre)
	}
}

func compareDotParts(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var x, y string
		if i < len(ap) {
			x = ap[i]
		}
		if i < len(bp) {
			y = bp[i]
		}
		if x == y {
			continue
		}
		if x == "" {
			return -1
		}
		if y == "" {
			return 1
		}
		xn, xErr := strconv.Atoi(x)
		yn, yErr := strconv.Atoi(y)
		switch {
		case xErr == nil && yErr == nil:
			if xn < yn {
				return -1
			}
			if xn > yn {
				return 1
			}
		case xErr == nil:
			return -1
		case yErr == nil:
			return 1
		default:
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ParseBilingualNotes splits ColabOne's bilingual release notes (Chinese above
// `---`, English below `---`) while stripping any redundant leading "ColabOne vX.Y.Z" title line.
func ParseBilingualNotes(raw string) ColabOneBilingualNotes {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ColabOneBilingualNotes{}
	}

	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 {
		first := strings.TrimSpace(lines[0])
		lowerFirst := strings.ToLower(first)
		if strings.HasPrefix(lowerFirst, "colabone v") || strings.HasPrefix(lowerFirst, "# colabone v") {
			lines = lines[1:]
		}
	}
	cleaned := strings.TrimSpace(strings.Join(lines, "\n"))
	if cleaned == "" {
		return ColabOneBilingualNotes{Raw: normalized}
	}

	var before []string
	var after []string
	foundSep := false
	for _, line := range strings.Split(cleaned, "\n") {
		trimmed := strings.TrimSpace(line)
		if !foundSep && (trimmed == "---" || trimmed == "***" || trimmed == "___") {
			foundSep = true
			continue
		}
		if !foundSep {
			before = append(before, line)
		} else {
			after = append(after, line)
		}
	}

	if foundSep {
		zh := strings.TrimSpace(strings.Join(before, "\n"))
		en := strings.TrimSpace(strings.Join(after, "\n"))
		return ColabOneBilingualNotes{
			Raw: cleaned,
			Zh:  zh,
			En:  en,
		}
	}

	return ColabOneBilingualNotes{
		Raw: cleaned,
		Zh:  cleaned,
		En:  cleaned,
	}
}

func hasHTTPStatusCode(lower, code string) bool {
	return strings.Contains(lower, ": "+code) ||
		strings.Contains(lower, " "+code+" ") ||
		strings.HasSuffix(lower, " "+code) ||
		strings.HasPrefix(lower, code+" ") ||
		lower == code
}

// IsManifestNotFoundError reports whether err represents a missing update
// manifest (HTTP 404) or unpublished release rather than a network outage.
func IsManifestNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoReleasePublished) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	return hasHTTPStatusCode(lower, "404") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no release is published")
}

var (
	reURLUserInfo   = regexp.MustCompile(`(https?://)[^/\s@]+@`)
	reURLQuery      = regexp.MustCompile(`(https?://[^\s"'<>]+?)\?[^\s"'<>:]*`)
	reSecretToken   = regexp.MustCompile(`(?i)\b(?:bearer\s+[A-Za-z0-9._\-+/=]+|gh[pousr]_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+)\b`)
	reWhitespaceRun = regexp.MustCompile(`\s+`)
)

func sanitizeUpdateErrorDetail(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = reWhitespaceRun.ReplaceAllString(s, " ")
	for _, prefix := range []string{
		"mygo: checking for updates: ",
		"mygo: installing the update: ",
	} {
		for strings.HasPrefix(s, prefix) {
			s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
		}
	}
	s = reURLQuery.ReplaceAllString(s, "$1")
	s = reURLUserInfo.ReplaceAllString(s, "$1<redacted>@")
	s = reSecretToken.ReplaceAllString(s, "<redacted>")
	s = strings.ReplaceAll(s, "https://", "")
	runes := []rune(s)
	if len(runes) > 220 {
		s = string(runes[:220]) + "…"
	}
	return s
}

func formatUpdateErrorLog(prefix, code, detail string) string {
	label := ""
	switch code {
	case ErrCodeProxy:
		label = "系统代理连接失败"
	case ErrCodeTimeout:
		label = "连接或下载超时"
	case ErrCodeTLS:
		label = "HTTPS/TLS 证书校验失败"
	case ErrCodeNetwork:
		label = "网络连接失败"
	case ErrCodeNotFound:
		label = "未找到当前平台的更新清单"
	case ErrCodeRateLimited:
		label = "GitHub 访问频率受限"
	case ErrCodeSignature:
		label = "更新补丁签名校验未通过（可能已被篡改或损坏）"
	case ErrCodePermission:
		label = "安装目录缺少写入权限"
	}
	if label != "" && detail != "" {
		return fmt.Sprintf("%s（%s）：%s", prefix, label, detail)
	}
	if detail != "" {
		return prefix + "：" + detail
	}
	if label != "" {
		return prefix + "：" + label
	}
	return prefix
}

// ClassifyUpdateError maps raw network/updater errors into granular UI error
// codes alongside a concise, sanitized technical diagnostic string.
func ClassifyUpdateError(err error) (string, string) {
	if err == nil {
		return ErrCodeNone, ""
	}
	if errors.Is(err, context.Canceled) {
		return ErrCodeCancelled, "已取消更新"
	}
	detail := sanitizeUpdateErrorDetail(err.Error())
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(lower, "not signed") || strings.Contains(lower, "signature") || strings.Contains(lower, "invalid update public key"):
		return ErrCodeSignature, detail
	case hasHTTPStatusCode(lower, "403") && (strings.Contains(lower, "rate limit") || strings.Contains(lower, "forbidden")),
		hasHTTPStatusCode(lower, "429") || strings.Contains(lower, "too many requests"):
		return ErrCodeRateLimited, detail
	case strings.Contains(lower, "cannot write next to the app") || strings.Contains(lower, "permission denied") || strings.Contains(lower, "access is denied") || strings.Contains(lower, "read-only"):
		return ErrCodePermission, detail
	case IsManifestNotFoundError(err):
		return ErrCodeNotFound, detail
	case strings.Contains(lower, "proxyconnect") || strings.Contains(lower, "proxy") || strings.Contains(lower, "socks"):
		return ErrCodeProxy, detail
	case strings.Contains(lower, "tls") || strings.Contains(lower, "x509") || strings.Contains(lower, "certificate"):
		return ErrCodeTLS, detail
	case strings.Contains(lower, "stalled") || strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return ErrCodeTimeout, detail
	case strings.Contains(lower, "dial ") || strings.Contains(lower, "eof") || strings.Contains(lower, "connection") ||
		strings.Contains(lower, "no such host") || strings.Contains(lower, "reset by peer") || strings.Contains(lower, "network") ||
		hasHTTPStatusCode(lower, "500") || hasHTTPStatusCode(lower, "502") || hasHTTPStatusCode(lower, "503") || hasHTTPStatusCode(lower, "504"):
		return ErrCodeNetwork, detail
	default:
		return ErrCodeUnknown, detail
	}
}
