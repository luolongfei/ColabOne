package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// colabOneMediaOpenRangeWindowBytes caps open-ended HTTP Range requests (e.g.
	// "Range: bytes=0-") so <video> / <audio> elements start playing immediately
	// and stream progressively without pulling an entire multi-hundred-MB file at once.
	colabOneMediaOpenRangeWindowBytes int64 = 2 << 20
	// colabOneMediaMaxRangeBytes caps any single explicit HTTP Range response slice.
	colabOneMediaMaxRangeBytes int64 = 8 << 20
	// colabOneMediaRuntimeChunkBytes matches server/comfyui.py's max chunk read size.
	colabOneMediaRuntimeChunkBytes int64 = 512 << 10
	// colabOneMediaOnDemandSyncCooldownSec avoids repeated Drive directory scans
	// across consecutive Range requests when a file ID still cannot be resolved.
	colabOneMediaOnDemandSyncCooldownSec float64 = 15.0
)

var (
	// ErrOutputMediaOffline indicates an output cannot currently be streamed because
	// it is offline or archived in Drive without direct read access.
	ErrOutputMediaOffline = errors.New("该作品暂离线，请稍后再试")

	errUnsatisfiableMediaRange   = errors.New("作品读取范围不合法")
	errInvalidMediaRangeSyntax   = errors.New("invalid byte range")
	errInvalidOutputID           = errors.New("产物编号不合法")
	errDriveDirectReadRequired   = errors.New("该作品已保存至 Google 云盘，请点击「在 Google 云盘查看」或开启无感云盘 API 后直接预览")
	errRuntimeMediaUnavailable   = errors.New("当前算力服务暂未就绪，暂不可访问该作品文件")
	errGuestMediaAccessFailed    = errors.New("当前无法访问该作品文件，请稍后再试")
	errSanitizedMediaUnavailable = errors.New("作品暂离线或暂不可访问，请稍后再试")
	errSanitizedMediaNotFound    = errors.New("作品不存在")
)

type colabOneSanitizedMediaError struct {
	msg    string
	target error
}

func (e *colabOneSanitizedMediaError) Error() string {
	return e.msg
}

func (e *colabOneSanitizedMediaError) Unwrap() error {
	return e.target
}

type colabOneOutputMetaReceiver interface {
	SetColabOneOutputMeta(id, filename, ext, mime string, size int64)
}

func sanitizeOutputMediaError(err error, opts ColabOneOutputMediaOptions) error {
	if err == nil || (!opts.SanitizeErrors && !opts.RuntimeOnly) {
		return err
	}
	switch {
	case errors.Is(err, ErrNotFound) || isNotFoundError(err):
		return &colabOneSanitizedMediaError{
			msg:    errSanitizedMediaNotFound.Error(),
			target: ErrNotFound,
		}
	case errors.Is(err, errInvalidOutputID),
		errors.Is(err, errUnsatisfiableMediaRange),
		errors.Is(err, errRuntimeMediaUnavailable):
		return err
	case errors.Is(err, errDriveDirectReadRequired):
		return &colabOneSanitizedMediaError{
			msg:    ErrOutputMediaOffline.Error(),
			target: ErrOutputMediaOffline,
		}
	case errors.Is(err, context.Canceled):
		return context.Canceled
	default:
		if opts.SanitizeErrors {
			return &colabOneSanitizedMediaError{
				msg:    errSanitizedMediaUnavailable.Error(),
				target: ErrOutputMediaOffline,
			}
		}
		return errGuestMediaAccessFailed
	}
}

type colabOneUnsatisfiableMediaRangeError struct {
	totalSize int64
}

func (e *colabOneUnsatisfiableMediaRangeError) Error() string {
	return errUnsatisfiableMediaRange.Error()
}

func (e *colabOneUnsatisfiableMediaRangeError) Unwrap() error {
	return errUnsatisfiableMediaRange
}

// ColabOneOutputMediaOptions configures how output media is resolved and streamed.
type ColabOneOutputMediaOptions struct {
	// RuntimeOnly restricts media resolution and streaming strictly to the active
	// remote GPU runtime, never inspecting host Google Drive records or APIs.
	RuntimeOnly bool
	// SanitizeErrors maps upstream Google Drive, API, or host errors to clean
	// guest-safe messages while still allowing Drive-backed outputs to be served
	// seamlessly when RuntimeOnly is false.
	SanitizeErrors bool
}

// ColabOneOutputMediaMeta describes a resolved workflow output ready for HTTP
// Range streaming from either Google Drive API v3 or the active remote runtime.
type ColabOneOutputMediaMeta struct {
	ID           string
	Size         int64
	SHA256       string
	Mime         string
	Extension    string
	Filename     string
	Source       string // "drive" or "runtime"
	DriveFileID  string
	QuotaProject string
	HasRuntime   bool
}

func parseColabOneByteRange(hdr string, totalSize int64) (start, end int64, isRange bool, err error) {
	trimmed := strings.TrimSpace(hdr)
	if trimmed == "" {
		if totalSize <= 0 {
			return 0, 0, false, errUnsatisfiableMediaRange
		}
		return 0, totalSize - 1, false, nil
	}
	if totalSize <= 0 {
		return 0, 0, true, errUnsatisfiableMediaRange
	}
	const prefix = "bytes="
	if len(trimmed) < len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		// Per RFC 9110 §14.1.2, unknown range units are ignored and full representation served.
		return 0, totalSize - 1, false, nil
	}
	spec := strings.TrimSpace(trimmed[len(prefix):])
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, true, errInvalidMediaRangeSyntax
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, true, errInvalidMediaRangeSyntax
	}
	startStr := strings.TrimSpace(spec[:dash])
	endStr := strings.TrimSpace(spec[dash+1:])
	if startStr == "" && endStr == "" {
		return 0, 0, true, errInvalidMediaRangeSyntax
	}

	if startStr == "" {
		// Suffix byte range: bytes=-<suffixLen>
		suffixLen, parseErr := strconv.ParseInt(endStr, 10, 64)
		if parseErr != nil || suffixLen <= 0 {
			return 0, 0, true, errUnsatisfiableMediaRange
		}
		if suffixLen > totalSize {
			suffixLen = totalSize
		}
		if suffixLen > colabOneMediaMaxRangeBytes {
			suffixLen = colabOneMediaMaxRangeBytes
		}
		start = totalSize - suffixLen
		end = totalSize - 1
		return start, end, true, nil
	}

	s, parseErr := strconv.ParseInt(startStr, 10, 64)
	if parseErr != nil || s < 0 {
		return 0, 0, true, errInvalidMediaRangeSyntax
	}
	if s >= totalSize {
		return 0, 0, true, errUnsatisfiableMediaRange
	}

	var e int64
	if endStr == "" {
		// Open-ended range (bytes=X-): cap to streaming window so playback begins immediately.
		e = s + colabOneMediaOpenRangeWindowBytes - 1
		if e >= totalSize {
			e = totalSize - 1
		}
	} else {
		parsedEnd, endErr := strconv.ParseInt(endStr, 10, 64)
		if endErr != nil || parsedEnd < s {
			return 0, 0, true, errInvalidMediaRangeSyntax
		}
		e = parsedEnd
		if e >= totalSize {
			e = totalSize - 1
		}
		if e-s+1 > colabOneMediaMaxRangeBytes {
			e = s + colabOneMediaMaxRangeBytes - 1
		}
	}
	return s, e, true, nil
}

func (c *ColabOneController) withRootContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithCancel(ctx)
	if c.rootCtx != nil {
		if c.rootCtx.Err() != nil {
			cancel()
			return reqCtx, cancel
		}
		stop := context.AfterFunc(c.rootCtx, cancel)
		if c.rootCtx.Err() != nil {
			stop()
			cancel()
			return reqCtx, cancel
		}
		return reqCtx, func() {
			stop()
			cancel()
		}
	}
	return reqCtx, cancel
}

func (c *ColabOneController) driveStreamClient() *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.driveHTTPClient != nil && !c.driveHTTPClientOwned {
		// Preserve test-injected clients (e.g. httptest.Server TLS/custom transport).
		return c.driveHTTPClient
	}
	if c.driveStreamHTTPClient != nil {
		return c.driveStreamHTTPClient
	}
	if c.stop.IsSet() {
		// Controller is closing/closed: avoid allocating a new cloned transport pool
		// that will never be cached or closed.
		return &http.Client{
			Transport:     http.DefaultTransport,
			Timeout:       0,
			CheckRedirect: refuseDriveRedirects,
		}
	}
	// Cached pooled streaming client: reuses TLS keep-alive connections across
	// consecutive HTTP Range requests while bounding only response-header wait
	// (Timeout: 0 relies on request context cancellation instead of aborting slow streams).
	var transport http.RoundTripper
	if def, ok := http.DefaultTransport.(*http.Transport); ok {
		cloned := def.Clone()
		cloned.ResponseHeaderTimeout = 30 * time.Second
		cloned.MaxIdleConns = 16
		cloned.MaxIdleConnsPerHost = 8
		cloned.IdleConnTimeout = 90 * time.Second
		transport = cloned
	} else {
		transport = http.DefaultTransport
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       0,
		CheckRedirect: refuseDriveRedirects,
	}
	c.driveStreamHTTPClient = client
	return client
}

func (c *ColabOneController) probeDriveFileSize(
	ctx context.Context,
	token, quotaProject, fileID string,
) (int64, error) {
	u := c.driveAPIBase() + "/files/" + url.PathEscape(fileID) + "?fields=size"
	status, body, err := c.doDriveAPIRequest(ctx, http.MethodGet, u, token, quotaProject, nil, nil, 64*1024)
	if err != nil {
		return 0, err
	}
	if status == http.StatusNotFound {
		return 0, ErrNotFound
	}
	if status != http.StatusOK {
		return 0, &ColabOneHTTPError{
			StatusCode: status,
			Message:    fmt.Sprintf("从 Google 云盘读取作品失败（HTTP %d）", status),
		}
	}
	var parsed struct {
		Size string `json:"size"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(parsed.Size), 10, 64)
	if n <= 0 {
		return 0, errors.New("作品大小不合法")
	}
	return n, nil
}

// ResolveOutputMediaMeta resolves whether a workflow output can be streamed via
// HTTP Range requests from Google Drive API v3 or the active remote GPU runtime.
func (c *ColabOneController) ResolveOutputMediaMeta(
	ctx context.Context,
	identity string,
) (*ColabOneOutputMediaMeta, error) {
	return c.ResolveOutputMediaMetaWithOptions(ctx, identity, ColabOneOutputMediaOptions{})
}

// ResolveOutputMediaMetaWithOptions resolves a workflow output for HTTP Range streaming
// with optional RuntimeOnly isolation (for guest workflow share links).
func (c *ColabOneController) ResolveOutputMediaMetaWithOptions(
	ctx context.Context,
	identity string,
	opts ColabOneOutputMediaOptions,
) (*ColabOneOutputMediaMeta, error) {
	cleanID := strings.ToLower(strings.TrimSpace(identity))
	if !reHex32ID.MatchString(cleanID) {
		return nil, errInvalidOutputID
	}
	reqCtx, cancelReq := c.withRootContext(ctx)
	defer cancelReq()
	if err := reqCtx.Err(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	gpuReady := asString(asMap(c.data["runtime"])["state"]) == "ready"
	var liveRec *ColabOneDriveOutputRecord
	if gpuReady {
		for _, rawItem := range asAnySlice(c.data["comfy_outputs"]) {
			m := asMap(rawItem)
			if opts.RuntimeOnly && strings.EqualFold(strings.TrimSpace(asString(m["storage"])), "drive") {
				continue
			}
			if strings.EqualFold(asString(m["id"]), cleanID) {
				liveRec = driveOutputRecordFromMap(m)
				break
			}
		}
	}
	var driveRec *ColabOneDriveOutputRecord
	var acctKey string
	var lastSynced float64
	if !opts.RuntimeOnly {
		acctKey = c.currentDriveAccountKeyLocked()
		lastSynced = c.driveOutputsLastSynced
		if c.driveOutputsLastSyncAttempt > lastSynced {
			lastSynced = c.driveOutputsLastSyncAttempt
		}
		if acctKey != "" && c.driveOutputs != nil {
			if bucket := c.driveOutputs[acctKey]; bucket != nil {
				driveRec = bucket[cleanID].clone()
			}
		}
	}
	c.mu.Unlock()

	if opts.RuntimeOnly && !gpuReady {
		return nil, errRuntimeMediaUnavailable
	}

	// 1) Prefer direct Google Drive API streaming when the output is already stored in Drive
	//    with direct read enabled (host mode only; strictly skipped when RuntimeOnly is set).
	//    When the output is also live on the active GPU runtime without a resolved Drive file
	//    ID yet, skip blocking Drive sync and stream immediately from the live runtime instead.
	if !opts.RuntimeOnly && driveRec != nil {
		canDirectRead, quotaProject := c.driveDirectReadReady()
		if canDirectRead {
			fileID := driveRec.preferredDriveFileID()
			needOnDemandSync := fileID == "" &&
				(!gpuReady || liveRec == nil) &&
				driveRec.DriveMode != "colab_drivefs" &&
				(lastSynced <= 0 || nowUnixFloat()-lastSynced >= colabOneMediaOnDemandSyncCooldownSec)
			if needOnDemandSync {
				syncRes, syncErr := c.SyncDriveOutputs(reqCtx)
				if syncErr != nil {
					if reqCtx.Err() == nil && !c.isShuttingDown(reqCtx) {
						c.mu.Lock()
						if acctKey != "" && c.currentDriveAccountKeyLocked() == acctKey {
							c.driveOutputsLastSyncAttempt = nowUnixFloat()
						}
						c.mu.Unlock()
					}
					if !gpuReady || liveRec == nil {
						return nil, syncErr
					}
				} else if asBool(syncRes["synced"]) {
					driveRec = nil
					c.mu.Lock()
					if curAcct := c.currentDriveAccountKeyLocked(); curAcct != "" && c.driveOutputs != nil {
						if bucket := c.driveOutputs[curAcct]; bucket != nil && bucket[cleanID] != nil {
							driveRec = bucket[cleanID].clone()
						}
					}
					c.mu.Unlock()
					fileID = driveRec.preferredDriveFileID()
				}
			}
			if fileID != "" {
				ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(driveRec.Extension), "."))
				km, ok := allowedDriveOutputKindMIME[ext]
				if !ok {
					return nil, errors.New("不支持预览该文件格式")
				}
				size := driveRec.Size
				if size <= 0 {
					token, tokErr := c.driveAccessToken(reqCtx)
					if tokErr == nil {
						if probed, probeErr := c.probeDriveFileSize(reqCtx, token, quotaProject, fileID); probeErr == nil && probed > 0 {
							size = probed
							persistedSize := false
							c.mu.Lock()
							if curAcct := c.currentDriveAccountKeyLocked(); curAcct != "" && c.driveOutputs != nil {
								if bucket := c.driveOutputs[curAcct]; bucket != nil && bucket[cleanID] != nil && bucket[cleanID].Size <= 0 {
									bucket[cleanID].Size = probed
									persistedSize = true
								}
							}
							c.mu.Unlock()
							if persistedSize {
								_ = c.saveDriveOutputs()
							}
						} else if liveRec == nil || !gpuReady {
							if probeErr != nil {
								return nil, probeErr
							}
							return nil, errors.New("作品大小不合法")
						}
					} else if liveRec == nil || !gpuReady {
						return nil, tokErr
					}
				}
				if size > 0 {
					return &ColabOneOutputMediaMeta{
						ID:           cleanID,
						Size:         size,
						SHA256:       sanitizeDriveSHA256(driveRec.SHA256),
						Mime:         km[1],
						Extension:    ext,
						Filename:     fmt.Sprintf("ColabOne-%s.%s", cleanID[:8], ext),
						Source:       "drive",
						DriveFileID:  fileID,
						QuotaProject: quotaProject,
						HasRuntime:   gpuReady && liveRec != nil,
					}, nil
				}
			}
		}
	}

	// 2) Active remote GPU runtime output.
	if gpuReady && liveRec != nil {
		ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(liveRec.Extension), "."))
		km, ok := allowedDriveOutputKindMIME[ext]
		if !ok {
			return nil, errors.New("不支持预览该文件格式")
		}
		if liveRec.Size <= 0 {
			return nil, errors.New("作品大小不合法")
		}
		return &ColabOneOutputMediaMeta{
			ID:         cleanID,
			Size:       liveRec.Size,
			SHA256:     sanitizeDriveSHA256(liveRec.SHA256),
			Mime:       km[1],
			Extension:  ext,
			Filename:   fmt.Sprintf("ColabOne-%s.%s", cleanID[:8], ext),
			Source:     "runtime",
			HasRuntime: true,
		}, nil
	}

	// 3) Live runtime fallback when local snapshot hasn't refreshed yet.
	if gpuReady {
		res, err := c.remote(reqCtx, "GET", fmt.Sprintf("comfyui/outputs/%s/asset?offset=0", cleanID), nil, "", 0)
		if err == nil {
			m := asMap(res)
			sz, _ := asInt64(m["size"])
			ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(asString(m["extension"])), "."))
			if ext == "" {
				ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(asString(m["filename"])), "."))
			}
			km, ok := allowedDriveOutputKindMIME[ext]
			if ok && sz > 0 {
				return &ColabOneOutputMediaMeta{
					ID:         cleanID,
					Size:       sz,
					Mime:       km[1],
					Extension:  ext,
					Filename:   fmt.Sprintf("ColabOne-%s.%s", cleanID[:8], ext),
					Source:     "runtime",
					HasRuntime: true,
				}, nil
			}
		} else if opts.RuntimeOnly {
			if errors.Is(err, ErrNotFound) || isNotFoundError(err) {
				return nil, ErrNotFound
			}
			return nil, errGuestMediaAccessFailed
		} else if driveRec == nil {
			if errors.Is(err, ErrNotFound) || isNotFoundError(err) {
				return nil, ErrNotFound
			}
			return nil, err
		}
	}

	if !opts.RuntimeOnly && driveRec != nil {
		return nil, errDriveDirectReadRequired
	}
	return nil, ErrNotFound
}

func parseContentRangeSpec(hdr string) (start, end, total int64, ok bool) {
	s := strings.TrimSpace(hdr)
	const prefix = "bytes "
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return 0, 0, 0, false
	}
	rest := strings.TrimSpace(s[len(prefix):])
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash+1 >= len(rest) {
		return 0, 0, 0, false
	}
	rangePart := strings.TrimSpace(rest[:slash])
	totalPart := strings.TrimSpace(rest[slash+1:])
	dash := strings.IndexByte(rangePart, '-')
	if dash <= 0 || dash+1 >= len(rangePart) {
		return 0, 0, 0, false
	}
	rs, err1 := strconv.ParseInt(strings.TrimSpace(rangePart[:dash]), 10, 64)
	re, err2 := strconv.ParseInt(strings.TrimSpace(rangePart[dash+1:]), 10, 64)
	if err1 != nil || err2 != nil || rs < 0 || re < rs {
		return 0, 0, 0, false
	}
	var tot int64
	if totalPart != "*" {
		t, err3 := strconv.ParseInt(totalPart, 10, 64)
		if err3 != nil || t <= 0 {
			return 0, 0, 0, false
		}
		tot = t
	}
	return rs, re, tot, true
}

func writeOutputMediaHeaders(rw http.ResponseWriter, meta *ColabOneOutputMediaMeta, start, end int64, isRange bool) {
	h := rw.Header()
	h.Set("Content-Type", meta.Mime)
	h.Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, meta.Filename))
	h.Set("Accept-Ranges", "bytes")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	if isRange {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, meta.Size))
		h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	} else {
		h.Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	}
}

func writeOutputMediaError(rw http.ResponseWriter, r *http.Request, status int, msg string) {
	safe := SafeMessage(strings.TrimSpace(msg))
	if safe == "" {
		safe = http.StatusText(status)
	}
	h := rw.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-ColabOne-Error", strings.ReplaceAll(url.PathEscape(safe), "+", "%2B"))
	rw.WriteHeader(status)
	if r == nil || r.Method != http.MethodHead {
		_ = json.NewEncoder(rw).Encode(map[string]string{"error": safe})
	}
}

// ServeOutputMediaHTTP serves a workflow output over HTTP with full RFC 9110
// Range (206 Partial Content) streaming so <video> and <audio> elements play
// directly from Google Drive or the active remote GPU without full-file download.
func (c *ColabOneController) ServeOutputMediaHTTP(
	ctx context.Context,
	rw http.ResponseWriter,
	r *http.Request,
	identity string,
) {
	c.ServeOutputMediaHTTPWithOptions(ctx, rw, r, identity, ColabOneOutputMediaOptions{})
}

// ServeOutputMediaHTTPWithOptions serves a workflow output over HTTP with optional
// RuntimeOnly enforcement for guest workflow shares.
func (c *ColabOneController) ServeOutputMediaHTTPWithOptions(
	ctx context.Context,
	rw http.ResponseWriter,
	r *http.Request,
	identity string,
	opts ColabOneOutputMediaOptions,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		rw.Header().Set("Allow", "GET, HEAD")
		writeOutputMediaError(rw, r, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	meta, err := c.ResolveOutputMediaMetaWithOptions(ctx, identity, opts)
	if err != nil {
		sanitize := opts.SanitizeErrors || opts.RuntimeOnly
		status := http.StatusBadGateway
		msg := err.Error()
		switch {
		case errors.Is(err, ErrNotFound) || isNotFoundError(err):
			status = http.StatusNotFound
			if sanitize {
				msg = errSanitizedMediaNotFound.Error()
			}
		case errors.Is(err, errInvalidOutputID):
			status = http.StatusBadRequest
		case errors.Is(err, errDriveDirectReadRequired):
			if sanitize {
				status = http.StatusServiceUnavailable
				msg = ErrOutputMediaOffline.Error()
			} else {
				status = http.StatusForbidden
			}
		case errors.Is(err, errRuntimeMediaUnavailable):
			status = http.StatusServiceUnavailable
		default:
			if opts.SanitizeErrors {
				msg = errSanitizedMediaUnavailable.Error()
			} else if opts.RuntimeOnly {
				msg = errGuestMediaAccessFailed.Error()
			}
		}
		writeOutputMediaError(rw, r, status, msg)
		return
	}

	start, end, isRange, rangeErr := parseColabOneByteRange(r.Header.Get("Range"), meta.Size)
	if rangeErr != nil {
		rw.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
		rw.Header().Set("Accept-Ranges", "bytes")
		writeOutputMediaError(rw, r, http.StatusRequestedRangeNotSatisfiable, "作品读取范围不合法")
		return
	}

	if r.Method == http.MethodHead {
		writeOutputMediaHeaders(rw, meta, start, end, isRange)
		if isRange {
			rw.WriteHeader(http.StatusPartialContent)
		} else {
			rw.WriteHeader(http.StatusOK)
		}
		return
	}

	writeStreamError := func(streamErr error) {
		if errors.Is(streamErr, errUnsatisfiableMediaRange) {
			totalSize := meta.Size
			var unsatErr *colabOneUnsatisfiableMediaRangeError
			if errors.As(streamErr, &unsatErr) && unsatErr.totalSize > 0 {
				totalSize = unsatErr.totalSize
			}
			rw.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", totalSize))
			rw.Header().Set("Accept-Ranges", "bytes")
			writeOutputMediaError(rw, r, http.StatusRequestedRangeNotSatisfiable, streamErr.Error())
			return
		}
		sanitize := opts.SanitizeErrors || opts.RuntimeOnly
		status := http.StatusBadGateway
		msg := streamErr.Error()
		if errors.Is(streamErr, ErrNotFound) || isNotFoundError(streamErr) {
			status = http.StatusNotFound
			if sanitize {
				msg = errSanitizedMediaNotFound.Error()
			}
		} else if opts.SanitizeErrors {
			msg = errSanitizedMediaUnavailable.Error()
		} else if opts.RuntimeOnly {
			msg = errGuestMediaAccessFailed.Error()
		}
		writeOutputMediaError(rw, r, status, msg)
	}

	rawRange := r.Header.Get("Range")
	// Fast path: serve directly from verified local persistent LRU disk cache (<1ms).
	if f, _, hit := c.openLocalOutputCacheFileChecked(meta.ID, meta.Size, meta.SHA256); hit {
		defer f.Close()
		c.serveOutputMediaFromFile(rw, r, meta, f, rawRange, start, end, isRange)
		return
	}

	if meta.Source == "drive" && !opts.RuntimeOnly {
		// On full-file cache miss (<= 64 MiB), populate local cache via single-flight and serve from SSD.
		if !isRange && c.canCacheOutputSize(meta.Size) {
			f, _, hit, cacheErr := c.ensureOutputCached(ctx, meta, "drive")
			if hit {
				defer f.Close()
				c.serveOutputMediaFromFile(rw, r, meta, f, rawRange, start, end, isRange)
				return
			}
			if cacheErr != nil {
				if meta.HasRuntime && ctx.Err() == nil && !errors.Is(cacheErr, context.Canceled) {
					rtMeta := *meta
					rtMeta.Source = "runtime"
					if rtF, _, rtHit, rtCacheErr := c.ensureOutputCached(ctx, &rtMeta, "runtime"); rtHit {
						defer rtF.Close()
						c.serveOutputMediaFromFile(rw, r, &rtMeta, rtF, rawRange, start, end, isRange)
						return
					} else if rtCacheErr != nil {
						meta.Size = rtMeta.Size
						writeStreamError(rtCacheErr)
						return
					}
					wroteRT, rtErr := c.streamRuntimeOutputMedia(ctx, rw, &rtMeta, start, end, isRange)
					if rtErr == nil || wroteRT {
						return
					}
					meta.Size = rtMeta.Size
					writeStreamError(rtErr)
					return
				}
				writeStreamError(cacheErr)
				return
			}
		}

		wroteHeaders, streamErr := c.streamDriveOutputMedia(ctx, rw, meta, start, end, isRange)
		if streamErr == nil {
			return
		}
		if !wroteHeaders && meta.HasRuntime && ctx.Err() == nil && !errors.Is(streamErr, context.Canceled) {
			// Seamless fallback to active remote runtime if Drive file is unavailable.
			rtMeta := *meta
			rtMeta.Source = "runtime"
			const smallSyncRangeCacheThreshold = 4 * 1024 * 1024
			if c.canCacheOutputSize(rtMeta.Size) && (!isRange || rtMeta.Size <= smallSyncRangeCacheThreshold) {
				if rtF, _, rtHit, rtCacheErr := c.ensureOutputCached(ctx, &rtMeta, "runtime"); rtHit {
					defer rtF.Close()
					c.serveOutputMediaFromFile(rw, r, &rtMeta, rtF, rawRange, start, end, isRange)
					return
				} else if rtCacheErr != nil {
					meta.Size = rtMeta.Size
					writeStreamError(rtCacheErr)
					return
				}
			}
			wroteRT, rtErr := c.streamRuntimeOutputMedia(ctx, rw, &rtMeta, start, end, isRange)
			if rtErr == nil || wroteRT {
				return
			}
			meta.Size = rtMeta.Size
			writeStreamError(rtErr)
			return
		}
		if !wroteHeaders {
			writeStreamError(streamErr)
		}
		return
	}

	// Runtime output cache miss (<= 64 MiB): for full-file requests or small files (<= 4 MiB),
	// coalesce via single-flight parallel chunk fetcher; for larger Range requests (e.g. video
	// bytes=0-1 probes / progressive seeks), stream the requested range immediately without blocking headers.
	const smallSyncRangeCacheThreshold = 4 * 1024 * 1024
	if c.canCacheOutputSize(meta.Size) && (!isRange || meta.Size <= smallSyncRangeCacheThreshold) {
		f, _, hit, cacheErr := c.ensureOutputCached(ctx, meta, "runtime")
		if hit {
			defer f.Close()
			c.serveOutputMediaFromFile(rw, r, meta, f, rawRange, start, end, isRange)
			return
		}
		if cacheErr != nil {
			writeStreamError(cacheErr)
			return
		}
	}

	wroteHeaders, streamErr := c.streamRuntimeOutputMedia(ctx, rw, meta, start, end, isRange)
	if streamErr != nil && !wroteHeaders {
		writeStreamError(streamErr)
	}
}

func (c *ColabOneController) streamDriveOutputMedia(
	ctx context.Context,
	rw http.ResponseWriter,
	meta *ColabOneOutputMediaMeta,
	start, end int64,
	isRange bool,
) (wroteHeaders bool, err error) {
	reqCtx, cancelReq := c.withRootContext(ctx)
	defer cancelReq()
	token, err := c.driveAccessToken(reqCtx)
	if err != nil {
		return false, err
	}
	reqURL := c.driveAPIBase() + "/files/" + url.PathEscape(meta.DriveFileID) + "?alt=media"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if meta.QuotaProject != "" {
		req.Header.Set("X-Goog-User-Project", meta.QuotaProject)
	}
	if isRange {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	}

	if err := reqCtx.Err(); err != nil {
		return false, err
	}
	resp, err := c.driveStreamClient().Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, ErrNotFound
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		if total := parseContentRangeTotal(resp.Header.Get("Content-Range")); total > 0 {
			meta.Size = total
			return false, &colabOneUnsatisfiableMediaRangeError{totalSize: total}
		}
		return false, errUnsatisfiableMediaRange
	}
	if (!isRange && resp.StatusCode != http.StatusOK) ||
		(isRange && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK) {
		return false, &ColabOneHTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("从 Google 云盘读取作品失败（HTTP %d）", resp.StatusCode),
		}
	}
	if resp.ContentLength == 0 {
		return false, errors.New("作品大小不合法")
	}

	wantLen := end - start + 1
	respMeta := *meta
	authoritativeTotal := int64(0)
	var reader io.Reader = resp.Body
	if isRange && resp.StatusCode == http.StatusOK {
		if resp.ContentLength > 0 && resp.ContentLength != respMeta.Size {
			respMeta.Size = resp.ContentLength
			meta.Size = resp.ContentLength
			authoritativeTotal = resp.ContentLength
			if start >= respMeta.Size {
				return false, &colabOneUnsatisfiableMediaRangeError{totalSize: respMeta.Size}
			}
			if end >= respMeta.Size {
				end = respMeta.Size - 1
				wantLen = end - start + 1
			}
		} else if resp.ContentLength > 0 {
			authoritativeTotal = resp.ContentLength
		}
		// Upstream ignored Range and returned 200 OK: skip to start before copying.
		if start > 0 {
			if skipped, skipErr := io.CopyN(io.Discard, resp.Body, start); skipErr != nil {
				if errors.Is(skipErr, io.EOF) || errors.Is(skipErr, io.ErrUnexpectedEOF) {
					if skipped > 0 {
						meta.Size = skipped
						return false, &colabOneUnsatisfiableMediaRangeError{totalSize: skipped}
					}
					return false, errUnsatisfiableMediaRange
				}
				return false, skipErr
			}
		}
		reader = io.LimitReader(resp.Body, wantLen)
	} else if isRange {
		crHdr := resp.Header.Get("Content-Range")
		if crStart, crEnd, crTotal, parsed := parseContentRangeSpec(crHdr); parsed {
			if crTotal > 0 {
				respMeta.Size = crTotal
				meta.Size = crTotal
				authoritativeTotal = crTotal
				if start >= crTotal {
					return false, &colabOneUnsatisfiableMediaRangeError{totalSize: crTotal}
				}
				if end >= crTotal {
					end = crTotal - 1
					wantLen = end - start + 1
				}
			}
			if crStart != start {
				return false, errors.New("输出文件分片数据不完整")
			}
			if crEnd < end {
				end = crEnd
				wantLen = end - start + 1
			}
		} else if total := parseContentRangeTotal(crHdr); total > 0 {
			respMeta.Size = total
			meta.Size = total
			authoritativeTotal = total
			if start >= total {
				return false, &colabOneUnsatisfiableMediaRangeError{totalSize: total}
			}
			if end >= total {
				end = total - 1
				wantLen = end - start + 1
			}
		}
		if resp.ContentLength > 0 && resp.ContentLength < wantLen {
			wantLen = resp.ContentLength
			end = start + wantLen - 1
		}
		if wantLen <= 0 {
			if respMeta.Size > 0 {
				meta.Size = respMeta.Size
				return false, &colabOneUnsatisfiableMediaRangeError{totalSize: respMeta.Size}
			}
			return false, errUnsatisfiableMediaRange
		}
		reader = io.LimitReader(resp.Body, wantLen)
	} else {
		if resp.ContentLength > 0 {
			respMeta.Size = resp.ContentLength
			meta.Size = resp.ContentLength
			authoritativeTotal = resp.ContentLength
			end = resp.ContentLength - 1
		}
		wantLen = respMeta.Size
		reader = io.LimitReader(resp.Body, respMeta.Size)
	}
	if authoritativeTotal > 0 {
		c.backfillDriveOutputSize(meta.ID, authoritativeTotal)
	}

	writeOutputMediaHeaders(rw, &respMeta, start, end, isRange)
	if isRange {
		rw.WriteHeader(http.StatusPartialContent)
	} else {
		rw.WriteHeader(http.StatusOK)
	}
	wroteHeaders = true

	flusher, _ := rw.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, rErr := reader.Read(buf)
		if n > 0 {
			if _, wErr := rw.Write(buf[:n]); wErr != nil {
				return true, wErr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				// Trigger underlying http.Response.Body EOF so HTTP/2 & HTTP/1.1
				// keep-alive connections return cleanly to the idle pool.
				if resp.ContentLength > 0 && wantLen == resp.ContentLength {
					_, _ = io.CopyN(io.Discard, resp.Body, 1)
				}
				return true, nil
			}
			return true, rErr
		}
	}
}

func (c *ColabOneController) streamRuntimeOutputMedia(
	ctx context.Context,
	rw http.ResponseWriter,
	meta *ColabOneOutputMediaMeta,
	start, end int64,
	isRange bool,
) (wroteHeaders bool, err error) {
	flusher, _ := rw.(http.Flusher)
	respMeta := *meta
	fetchEnd := end
	if !isRange {
		fetchEnd = -1
	}
	actualSize, _, fetchErr := c.fetchRuntimeOutputChunks(ctx, respMeta.ID, start, fetchEnd, func(chunk []byte, total int64, _ map[string]any) error {
		if !wroteHeaders {
			if total > 0 {
				respMeta.Size = total
				if !isRange || end >= total {
					end = total - 1
				}
			}
			writeOutputMediaHeaders(rw, &respMeta, start, end, isRange)
			if isRange {
				rw.WriteHeader(http.StatusPartialContent)
			} else {
				rw.WriteHeader(http.StatusOK)
			}
			wroteHeaders = true
		}
		if _, wErr := rw.Write(chunk); wErr != nil {
			return wErr
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	})
	if fetchErr != nil {
		if !wroteHeaders && actualSize > 0 {
			meta.Size = actualSize
		}
		return wroteHeaders, fetchErr
	}
	return wroteHeaders, nil
}

type colabOneDestWriter struct {
	w        io.Writer
	writeErr error
}

func (d *colabOneDestWriter) Write(p []byte) (int, error) {
	n, err := d.w.Write(p)
	if err != nil && d.writeErr == nil {
		d.writeErr = err
	}
	return n, err
}

func copyCacheFileToWriter(
	ctx context.Context,
	f io.Reader,
	w io.Writer,
	meta *ColabOneOutputMediaMeta,
	expectedSize int64,
) (destErr error, readErr error) {
	metaRecv, _ := w.(colabOneOutputMetaReceiver)
	dest := &colabOneDestWriter{w: w}
	buf := make([]byte, 32*1024)
	wroteAny := false
	var written int64
	for {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		n, rErr := f.Read(buf)
		if n > 0 {
			if !wroteAny {
				if metaRecv != nil {
					metaRecv.SetColabOneOutputMeta(meta.ID, meta.Filename, meta.Extension, meta.Mime, meta.Size)
				}
				wroteAny = true
			}
			if _, wErr := dest.Write(buf[:n]); wErr != nil {
				return dest.writeErr, nil
			}
			written += int64(n)
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				if !wroteAny || (expectedSize > 0 && written != expectedSize) {
					return nil, errors.New("输出文件分片数据不完整")
				}
				meta.Size = written
				return nil, nil
			}
			return nil, rErr
		}
	}
}

// StreamOutputMediaToWriter resolves an output artifact and streams its full byte
// content directly into w in bounded chunks without buffering entire files in memory.
// When Drive-backed streaming fails before the first byte is written and the output
// is also present on an active runtime, it seamlessly falls back to runtime streaming.
func (c *ColabOneController) StreamOutputMediaToWriter(
	ctx context.Context,
	identity string,
	w io.Writer,
	opts ColabOneOutputMediaOptions,
) (*ColabOneOutputMediaMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if w == nil {
		return nil, errors.New("输出目标无效")
	}
	meta, err := c.ResolveOutputMediaMetaWithOptions(ctx, identity, opts)
	if err != nil {
		return nil, sanitizeOutputMediaError(err, opts)
	}

	// Fast path: stream directly from verified local persistent LRU disk cache.
	if f, entry, hit := c.openLocalOutputCacheFileChecked(meta.ID, meta.Size, meta.SHA256); hit {
		defer f.Close()
		if entry != nil && entry.Size > 0 {
			meta.Size = entry.Size
		}
		if destErr, readErr := copyCacheFileToWriter(ctx, f, w, meta, meta.Size); destErr != nil {
			return meta, destErr
		} else if readErr != nil {
			return meta, sanitizeOutputMediaError(readErr, opts)
		}
		return meta, nil
	}

	if meta.Source == "drive" && !opts.RuntimeOnly {
		if c.canCacheOutputSize(meta.Size) {
			f, entry, hit, cacheErr := c.ensureOutputCached(ctx, meta, "drive")
			if hit {
				defer f.Close()
				if entry != nil && entry.Size > 0 {
					meta.Size = entry.Size
				}
				if destErr, readErr := copyCacheFileToWriter(ctx, f, w, meta, meta.Size); destErr != nil {
					return meta, destErr
				} else if readErr != nil {
					return meta, sanitizeOutputMediaError(readErr, opts)
				}
				return meta, nil
			}
			if cacheErr != nil {
				if meta.HasRuntime && ctx.Err() == nil && !errors.Is(cacheErr, context.Canceled) {
					rtMeta := *meta
					rtMeta.Source = "runtime"
					if rtF, rtEntry, rtHit, rtCacheErr := c.ensureOutputCached(ctx, &rtMeta, "runtime"); rtHit {
						defer rtF.Close()
						if rtEntry != nil && rtEntry.Size > 0 {
							rtMeta.Size = rtEntry.Size
						}
						if destErr, readErr := copyCacheFileToWriter(ctx, rtF, w, &rtMeta, rtMeta.Size); destErr != nil {
							return &rtMeta, destErr
						} else if readErr != nil {
							return &rtMeta, sanitizeOutputMediaError(readErr, opts)
						}
						return &rtMeta, nil
					} else if rtCacheErr != nil {
						return &rtMeta, sanitizeOutputMediaError(rtCacheErr, opts)
					}
					_, destErr, rtErr := c.streamRuntimeOutputToWriter(ctx, w, &rtMeta)
					if destErr != nil {
						return &rtMeta, destErr
					}
					if rtErr == nil {
						return &rtMeta, nil
					}
					return &rtMeta, sanitizeOutputMediaError(rtErr, opts)
				}
				return meta, sanitizeOutputMediaError(cacheErr, opts)
			}
		}

		wroteAny, destErr, streamErr := c.streamDriveOutputToWriter(ctx, w, meta)
		if destErr != nil {
			return meta, destErr
		}
		if streamErr == nil {
			return meta, nil
		}
		if !wroteAny && meta.HasRuntime && ctx.Err() == nil && !errors.Is(streamErr, context.Canceled) {
			rtMeta := *meta
			rtMeta.Source = "runtime"
			_, rtDestErr, rtErr := c.streamRuntimeOutputToWriter(ctx, w, &rtMeta)
			if rtDestErr != nil {
				return &rtMeta, rtDestErr
			}
			if rtErr == nil {
				return &rtMeta, nil
			}
			return &rtMeta, sanitizeOutputMediaError(rtErr, opts)
		}
		return meta, sanitizeOutputMediaError(streamErr, opts)
	}

	if c.canCacheOutputSize(meta.Size) {
		f, entry, hit, cacheErr := c.ensureOutputCached(ctx, meta, "runtime")
		if hit {
			defer f.Close()
			if entry != nil && entry.Size > 0 {
				meta.Size = entry.Size
			}
			if destErr, readErr := copyCacheFileToWriter(ctx, f, w, meta, meta.Size); destErr != nil {
				return meta, destErr
			} else if readErr != nil {
				return meta, sanitizeOutputMediaError(readErr, opts)
			}
			return meta, nil
		}
		if cacheErr != nil {
			return meta, sanitizeOutputMediaError(cacheErr, opts)
		}
	}

	_, destErr, rtErr := c.streamRuntimeOutputToWriter(ctx, w, meta)
	if destErr != nil {
		return meta, destErr
	}
	if rtErr != nil {
		return meta, sanitizeOutputMediaError(rtErr, opts)
	}
	return meta, nil
}

func (c *ColabOneController) streamDriveOutputToWriter(
	ctx context.Context,
	w io.Writer,
	meta *ColabOneOutputMediaMeta,
) (wroteAny bool, destErr error, upstreamErr error) {
	reqCtx, cancelReq := c.withRootContext(ctx)
	defer cancelReq()
	token, err := c.driveAccessToken(reqCtx)
	if err != nil {
		return false, nil, err
	}
	reqURL := c.driveAPIBase() + "/files/" + url.PathEscape(meta.DriveFileID) + "?alt=media"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return false, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if meta.QuotaProject != "" {
		req.Header.Set("X-Goog-User-Project", meta.QuotaProject)
	}

	if err := reqCtx.Err(); err != nil {
		return false, nil, err
	}
	resp, err := c.driveStreamClient().Do(req)
	if err != nil {
		return false, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return false, nil, &ColabOneHTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("从 Google 云盘读取作品失败（HTTP %d）", resp.StatusCode),
		}
	}
	if resp.ContentLength > 0 {
		meta.Size = resp.ContentLength
	}

	metaRecv, _ := w.(colabOneOutputMetaReceiver)
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if err := reqCtx.Err(); err != nil {
			return wroteAny, nil, err
		}
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if !wroteAny {
				if metaRecv != nil {
					metaRecv.SetColabOneOutputMeta(meta.ID, meta.Filename, meta.Extension, meta.Mime, meta.Size)
				}
				wroteAny = true
			}
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				return true, wErr, nil
			}
			written += int64(n)
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				if !wroteAny || (resp.ContentLength > 0 && written != resp.ContentLength) {
					return wroteAny, nil, errors.New("输出文件分片数据不完整")
				}
				meta.Size = written
				return true, nil, nil
			}
			return wroteAny, nil, rErr
		}
	}
}

func (c *ColabOneController) streamRuntimeOutputToWriter(
	ctx context.Context,
	w io.Writer,
	meta *ColabOneOutputMediaMeta,
) (wroteAny bool, destErr error, upstreamErr error) {
	metaRecv, _ := w.(colabOneOutputMetaReceiver)
	var written int64
	actualSize, _, fetchErr := c.fetchRuntimeOutputChunks(ctx, meta.ID, 0, -1, func(chunk []byte, total int64, _ map[string]any) error {
		if !wroteAny {
			if total > 0 {
				meta.Size = total
			}
			if metaRecv != nil {
				metaRecv.SetColabOneOutputMeta(meta.ID, meta.Filename, meta.Extension, meta.Mime, meta.Size)
			}
			wroteAny = true
		}
		if _, wErr := w.Write(chunk); wErr != nil {
			destErr = wErr
			return wErr
		}
		written += int64(len(chunk))
		return nil
	})
	if destErr != nil {
		return wroteAny, destErr, nil
	}
	if fetchErr != nil {
		return wroteAny, nil, fetchErr
	}
	if !wroteAny || (actualSize > 0 && written != actualSize) {
		return wroteAny, nil, errors.New("输出文件分片数据不完整")
	}
	meta.Size = written
	return wroteAny, nil, nil
}
