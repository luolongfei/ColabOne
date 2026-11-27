package controller

import (
	"context"
	"encoding/base64"
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
	errUnsatisfiableMediaRange = errors.New("作品读取范围不合法")
	errInvalidMediaRangeSyntax = errors.New("invalid byte range")
	errInvalidOutputID         = errors.New("产物编号不合法")
	errDriveDirectReadRequired = errors.New("该作品已保存至 Google 云盘，请点击「在 Google 云盘查看」或开启无感云盘 API 后直接预览")
	errRuntimeMediaUnavailable = errors.New("当前算力服务暂未就绪，暂不可访问该作品文件")
	errGuestMediaAccessFailed  = errors.New("当前无法访问该作品文件，请稍后再试")
)

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
}

// ColabOneOutputMediaMeta describes a resolved workflow output ready for HTTP
// Range streaming from either Google Drive API v3 or the active remote runtime.
type ColabOneOutputMediaMeta struct {
	ID           string
	Size         int64
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
	c.driveStreamHTTPClient = &http.Client{
		Transport:     transport,
		Timeout:       0,
		CheckRedirect: refuseDriveRedirects,
	}
	return c.driveStreamHTTPClient
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
	var lastSynced float64
	if !opts.RuntimeOnly {
		acctKey := c.currentDriveAccountKeyLocked()
		lastSynced = c.driveOutputsLastSynced
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
				if _, syncErr := c.SyncDriveOutputs(ctx); syncErr == nil {
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
					token, tokErr := c.driveAccessToken(ctx)
					if tokErr == nil {
						if probed, probeErr := c.probeDriveFileSize(ctx, token, quotaProject, fileID); probeErr == nil && probed > 0 {
							size = probed
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
			Mime:       km[1],
			Extension:  ext,
			Filename:   fmt.Sprintf("ColabOne-%s.%s", cleanID[:8], ext),
			Source:     "runtime",
			HasRuntime: true,
		}, nil
	}

	// 3) Live runtime fallback when local snapshot hasn't refreshed yet.
	if gpuReady {
		res, err := c.remote(ctx, "GET", fmt.Sprintf("comfyui/outputs/%s/asset?offset=0", cleanID), nil, "", 0)
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
	h.Set("X-ColabOne-Error", url.QueryEscape(safe))
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
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		rw.Header().Set("Allow", "GET, HEAD")
		writeOutputMediaError(rw, r, http.StatusMethodNotAllowed, "不支持的请求方法")
		return
	}

	meta, err := c.ResolveOutputMediaMetaWithOptions(ctx, identity, opts)
	if err != nil {
		status := http.StatusBadGateway
		msg := err.Error()
		switch {
		case errors.Is(err, ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, errInvalidOutputID):
			status = http.StatusBadRequest
		case errors.Is(err, errDriveDirectReadRequired):
			status = http.StatusForbidden
		case errors.Is(err, errRuntimeMediaUnavailable):
			status = http.StatusServiceUnavailable
		default:
			if opts.RuntimeOnly {
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
		status := http.StatusBadGateway
		if errors.Is(streamErr, ErrNotFound) || isNotFoundError(streamErr) {
			status = http.StatusNotFound
		}
		msg := streamErr.Error()
		if opts.RuntimeOnly && status == http.StatusBadGateway {
			msg = errGuestMediaAccessFailed.Error()
		}
		writeOutputMediaError(rw, r, status, msg)
	}

	if meta.Source == "drive" && !opts.RuntimeOnly {
		wroteHeaders, streamErr := c.streamDriveOutputMedia(ctx, rw, meta, start, end, isRange)
		if streamErr == nil {
			return
		}
		if !wroteHeaders && meta.HasRuntime {
			// Seamless fallback to active remote runtime if Drive file is unavailable.
			rtMeta := *meta
			rtMeta.Source = "runtime"
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
	token, err := c.driveAccessToken(ctx)
	if err != nil {
		return false, err
	}
	reqURL := c.driveAPIBase() + "/files/" + url.PathEscape(meta.DriveFileID) + "?alt=media"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
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
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return false, &ColabOneHTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("从 Google 云盘读取作品失败（HTTP %d）", resp.StatusCode),
		}
	}

	wantLen := end - start + 1
	respMeta := *meta
	var reader io.Reader = resp.Body
	if isRange && resp.StatusCode == http.StatusOK {
		if resp.ContentLength > 0 && resp.ContentLength < respMeta.Size {
			respMeta.Size = resp.ContentLength
			if start >= respMeta.Size {
				meta.Size = respMeta.Size
				return false, &colabOneUnsatisfiableMediaRangeError{totalSize: respMeta.Size}
			}
			if end >= respMeta.Size {
				end = respMeta.Size - 1
				wantLen = end - start + 1
			}
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
				if start >= crTotal {
					meta.Size = crTotal
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
			if start >= total {
				meta.Size = total
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
			end = resp.ContentLength - 1
		}
		wantLen = respMeta.Size
		reader = io.LimitReader(resp.Body, respMeta.Size)
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
	cur := start
	for cur <= end {
		if err := ctx.Err(); err != nil {
			return wroteHeaders, err
		}
		res, err := c.remote(ctx, "GET", fmt.Sprintf("comfyui/outputs/%s/asset?offset=%d", respMeta.ID, cur), nil, "", 0)
		if err != nil {
			return wroteHeaders, err
		}
		m := asMap(res)
		if rawOff, hasOff := m["offset"]; hasOff {
			if offVal, ok := asInt64(rawOff); !ok || offVal != cur {
				return wroteHeaders, errors.New("输出文件分片数据不完整")
			}
		}
		if !wroteHeaders {
			if actualSize, ok := asInt64(m["size"]); ok && actualSize > 0 {
				respMeta.Size = actualSize
				if cur >= actualSize {
					meta.Size = actualSize
					return false, &colabOneUnsatisfiableMediaRangeError{totalSize: actualSize}
				}
				if !isRange || end >= actualSize {
					end = actualSize - 1
				}
			}
		}
		rawB64 := asString(m["data"])
		if rawB64 == "" {
			return wroteHeaders, errors.New("输出文件分片数据不完整")
		}
		chunk, decErr := base64.StdEncoding.DecodeString(rawB64)
		if decErr != nil || len(chunk) == 0 || int64(len(chunk)) > colabOneMediaRuntimeChunkBytes {
			return wroteHeaders, errors.New("输出文件分片数据不完整")
		}
		remaining := end - cur + 1
		if int64(len(chunk)) > remaining {
			chunk = chunk[:remaining]
		}
		if !wroteHeaders {
			writeOutputMediaHeaders(rw, &respMeta, start, end, isRange)
			if isRange {
				rw.WriteHeader(http.StatusPartialContent)
			} else {
				rw.WriteHeader(http.StatusOK)
			}
			wroteHeaders = true
		}
		if _, wErr := rw.Write(chunk); wErr != nil {
			return true, wErr
		}
		if flusher != nil {
			flusher.Flush()
		}
		cur += int64(len(chunk))
	}
	return wroteHeaders, nil
}
