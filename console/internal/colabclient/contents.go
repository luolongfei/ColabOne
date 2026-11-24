package colabclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultContentsUploadTimeout  = 60 * time.Second
	defaultContentsRequestTimeout = 60 * time.Second
	defaultContentsDeleteTimeout  = 15 * time.Second
	maxContentsResponseBytes      = 64 << 20        // 64 MiB
	contentsUploadChunkBytes      = 6 * 1024 * 1024 // 6 MiB raw (exact multiple of 3 -> clean unpadded 8 MiB base64 per PUT)
)

// ColabOneContentsClient interacts with a Colab runtime's Jupyter Contents API
// (/api/contents/*) using runtime proxy token authentication.
// It is immutable after construction and safe for concurrent use.
type ColabOneContentsClient struct {
	httpClient      *http.Client
	hasCustomClient bool
}

var defaultNoRedirectContentsClient = &http.Client{
	CheckRedirect: refuseRedirects,
}

// NewColabOneContentsClient constructs a Contents API client. If httpClient is
// nil, an internal client with sensible dial/TLS timeouts and redirect refusal
// is used and per-call timeouts are enforced via context deadlines.
func NewColabOneContentsClient(httpClient *http.Client) *ColabOneContentsClient {
	if httpClient != nil {
		clientCopy := httpClient
		if httpClient.CheckRedirect == nil {
			copied := *httpClient
			copied.CheckRedirect = refuseRedirects
			clientCopy = &copied
		}
		return &ColabOneContentsClient{
			httpClient:      clientCopy,
			hasCustomClient: true,
		}
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &ColabOneContentsClient{
		httpClient: &http.Client{
			Transport:     transport,
			CheckRedirect: refuseRedirects,
		},
	}
}

type contentsUploadPayload struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Type    string `json:"type"`
	Format  string `json:"format"`
	Content string `json:"content"`
	Chunk   int    `json:"chunk"`
}

// UploadBytes uploads raw bytes to remotePath via PUT /api/contents/{quotedPath}
// encoded as base64 with query parameters authuser=0 and
// colab-runtime-proxy-token={proxyToken}. Payloads up to 6 MiB use a single
// PUT with chunk=1; larger payloads use standard Jupyter Contents API
// multi-chunk PUTs (chunk = 1, 2, ..., -1) with 6 MiB (multiple-of-3) chunks
// so every chunk encodes as clean unpadded base64 well under proxy/Tornado limits.
func (c *ColabOneContentsClient) UploadBytes(
	ctx context.Context,
	baseURL, proxyToken, remotePath string,
	data []byte,
	timeout time.Duration,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	} else if _, hasDeadline := ctx.Deadline(); !hasDeadline && !c.hasCustomClient {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultContentsUploadTimeout)
		defer cancel()
	}

	parts := strings.Split(remotePath, "/")
	filename := parts[len(parts)-1]

	if len(data) <= contentsUploadChunkBytes {
		payload := contentsUploadPayload{
			Name:    filename,
			Path:    remotePath,
			Type:    "file",
			Format:  "base64",
			Content: base64.StdEncoding.EncodeToString(data),
			Chunk:   1,
		}
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = c.doRequest(ctx, http.MethodPut, baseURL, proxyToken, remotePath, nil, bodyBytes, false)
		return err
	}

	chunkIdx := 1
	for offset := 0; offset < len(data); offset += contentsUploadChunkBytes {
		end := offset + contentsUploadChunkBytes
		chunkNum := chunkIdx
		if end >= len(data) {
			end = len(data)
			chunkNum = -1
		}
		payload := contentsUploadPayload{
			Name:    filename,
			Path:    remotePath,
			Type:    "file",
			Format:  "base64",
			Content: base64.StdEncoding.EncodeToString(data[offset:end]),
			Chunk:   chunkNum,
		}
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := c.doRequest(ctx, http.MethodPut, baseURL, proxyToken, remotePath, nil, bodyBytes, false); err != nil {
			return err
		}
		chunkIdx++
	}
	return nil
}

// DeleteFile deletes remotePath via DELETE /api/contents/{quotedPath}.
func (c *ColabOneContentsClient) DeleteFile(
	ctx context.Context,
	baseURL, proxyToken, remotePath string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && !c.hasCustomClient {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultContentsDeleteTimeout)
		defer cancel()
	}
	_, err := c.doRequest(ctx, http.MethodDelete, baseURL, proxyToken, remotePath, nil, nil, false)
	return err
}

// ListDir retrieves metadata and entries for remotePath via GET /api/contents/{quotedPath}.
func (c *ColabOneContentsClient) ListDir(
	ctx context.Context,
	baseURL, proxyToken, remotePath string,
) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && !c.hasCustomClient {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultContentsRequestTimeout)
		defer cancel()
	}
	body, err := c.doRequest(ctx, http.MethodGet, baseURL, proxyToken, remotePath, nil, nil, true)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("colabclient: failed to decode directory listing for %s: %w", remotePath, err)
	}
	return result, nil
}

// DownloadBytes fetches a remote file via GET /api/contents/{quotedPath}?content=1
// and decodes base64 or text payloads into raw bytes. Returns ErrIsDirectory
// if remotePath is a directory and ErrNotFound on HTTP 404.
func (c *ColabOneContentsClient) DownloadBytes(
	ctx context.Context,
	baseURL, proxyToken, remotePath string,
) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && !c.hasCustomClient {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultContentsRequestTimeout)
		defer cancel()
	}
	body, err := c.doRequest(
		ctx,
		http.MethodGet,
		baseURL,
		proxyToken,
		remotePath,
		map[string]string{"content": "1"},
		nil,
		true,
	)
	if err != nil {
		return nil, err
	}
	var entry struct {
		Type    string `json:"type"`
		Format  string `json:"format"`
		Content any    `json:"content"`
	}
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("colabclient: failed to decode downloaded file %s: %w", remotePath, err)
	}
	if entry.Type == "directory" {
		return nil, fmt.Errorf("%w: %s", ErrIsDirectory, remotePath)
	}
	if entry.Content == nil {
		return []byte{}, nil
	}
	if entry.Format == "base64" {
		s, ok := entry.Content.(string)
		if !ok {
			return nil, fmt.Errorf("colabclient: expected base64 string content for %s", remotePath)
		}
		decoded, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("colabclient: failed to decode base64 content for %s: %w", remotePath, err)
		}
		return decoded, nil
	}
	switch v := entry.Content.(type) {
	case string:
		return []byte(v), nil
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return encoded, nil
	}
}

func (c *ColabOneContentsClient) client() *http.Client {
	if c != nil && c.httpClient != nil {
		return c.httpClient
	}
	return defaultNoRedirectContentsClient
}

func (c *ColabOneContentsClient) doRequest(
	ctx context.Context,
	method, baseURL, proxyToken, remotePath string,
	extraParams map[string]string,
	jsonBody []byte,
	readBody bool,
) ([]byte, error) {
	cleanBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if cleanBase == "" {
		return nil, errors.New("colabclient: baseURL is required")
	}

	quotedPath := quoteContentsPath(remotePath)
	q := url.Values{}
	q.Set("authuser", "0")
	q.Set("colab-runtime-proxy-token", proxyToken)
	for k, v := range extraParams {
		q.Set(k, v)
	}
	fullURL := cleanBase + "/api/contents/" + quotedPath + "?" + q.Encode()

	var bodyReader io.Reader
	if jsonBody != nil {
		bodyReader = bytes.NewReader(jsonBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, sanitizeContentsURLError(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(HeaderColabClientAgent, ColabClientAgent)
	if jsonBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, sanitizeContentsURLError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxColabErrorBodyBytes))
		reqErr := &ColabOneRequestError{
			StatusCode: resp.StatusCode,
			Method:     method,
			URL:        redactProxyTokenURL(fullURL),
			Reason:     responseReason(resp),
			Body:       string(errBody),
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %w: %s: %w", ErrNotFound, os.ErrNotExist, remotePath, reqErr)
		}
		return nil, reqErr
	}

	if !readBody {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxContentsResponseBytes))
		return nil, nil
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxContentsResponseBytes))
	if err != nil {
		return nil, sanitizeContentsURLError(err)
	}
	return bytes.TrimSpace(StripXSSIPrefix(bodyBytes)), nil
}

// quoteContentsPath mirrors Python urllib.parse.quote(path.strip("/"), safe="/"):
// leading and trailing slashes are stripped, '/' separators are preserved, and
// all bytes outside RFC 3986 unreserved characters are percent-encoded.
func quoteContentsPath(remotePath string) string {
	trimmed := strings.Trim(remotePath, "/")
	if trimmed == "" {
		return ""
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(trimmed))
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if ch == '/' || isRFC3986Unreserved(ch) {
			b.WriteByte(ch)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[ch>>4])
			b.WriteByte(hex[ch&0x0F])
		}
	}
	return b.String()
}

func isRFC3986Unreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '~'
}

const (
	proxyTokenQueryKey       = "colab-runtime-proxy-token"
	redactedURLMarker        = "[redacted]"
	maxContentsSanitizeDepth = 64
)

type sanitizedContentsError struct {
	msg string
	err error
}

func (e *sanitizedContentsError) Error() string { return e.msg }
func (e *sanitizedContentsError) Unwrap() error { return e.err }

type sanitizedMultiError struct {
	msg  string
	errs []error
}

func (e *sanitizedMultiError) Error() string   { return e.msg }
func (e *sanitizedMultiError) Unwrap() []error { return e.errs }

func sanitizeContentsURLError(err error) error {
	sanitized, _ := sanitizeContentsURLErrorWithChanged(err, 0)
	return sanitized
}

func sanitizeContentsURLErrorWithChanged(err error, depth int) (error, bool) {
	if err == nil {
		return nil, false
	}
	if uerr, ok := err.(*url.Error); ok && uerr == nil {
		return nil, false
	}
	if depth >= maxContentsSanitizeDepth {
		rawMsg := err.Error()
		redactedMsg := redactProxyTokenInString(rawMsg)
		if redactedMsg != rawMsg {
			return &sanitizedContentsError{msg: redactedMsg}, true
		}
		return err, false
	}
	if uerr, ok := err.(*url.Error); ok {
		return sanitizeDirectURLError(uerr, depth+1)
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		errs := multi.Unwrap()
		changed := false
		sanitized := make([]error, len(errs))
		rawMsg := err.Error()
		redactedMsg := rawMsg
		for i, sub := range errs {
			s, subChanged := sanitizeContentsURLErrorWithChanged(sub, depth+1)
			sanitized[i] = s
			if subChanged {
				changed = true
				if sub != nil && s != nil {
					if oldMsg := sub.Error(); oldMsg != "" {
						redactedMsg = strings.ReplaceAll(redactedMsg, oldMsg, s.Error())
					}
				}
			}
		}
		redactedMsg = redactProxyTokenInString(redactedMsg)
		if changed || redactedMsg != rawMsg {
			return &sanitizedMultiError{msg: redactedMsg, errs: sanitized}, true
		}
		return err, false
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		inner := single.Unwrap()
		sanitizedInner, innerChanged := sanitizeContentsURLErrorWithChanged(inner, depth+1)
		rawMsg := err.Error()
		redactedMsg := rawMsg
		if innerChanged && inner != nil && sanitizedInner != nil {
			if oldMsg := inner.Error(); oldMsg != "" {
				redactedMsg = strings.ReplaceAll(redactedMsg, oldMsg, sanitizedInner.Error())
			}
		}
		redactedMsg = redactProxyTokenInString(redactedMsg)
		if innerChanged || redactedMsg != rawMsg {
			if sanitizedInner != nil && redactedMsg == sanitizedInner.Error() {
				return sanitizedInner, true
			}
			return &sanitizedContentsError{msg: redactedMsg, err: sanitizedInner}, true
		}
		return err, false
	}
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr != nil {
		sanitizedUErr, uChanged := sanitizeDirectURLError(uerr, depth+1)
		rawMsg := err.Error()
		redactedMsg := rawMsg
		if uChanged {
			if oldMsg := uerr.Error(); oldMsg != "" {
				redactedMsg = strings.ReplaceAll(redactedMsg, oldMsg, sanitizedUErr.Error())
			}
		}
		redactedMsg = redactProxyTokenInString(redactedMsg)
		if uChanged || redactedMsg != rawMsg {
			if redactedMsg == sanitizedUErr.Error() {
				return sanitizedUErr, true
			}
			return &sanitizedContentsError{msg: redactedMsg, err: sanitizedUErr}, true
		}
		return err, false
	}
	rawMsg := err.Error()
	redactedMsg := redactProxyTokenInString(rawMsg)
	if redactedMsg != rawMsg {
		return &sanitizedContentsError{msg: redactedMsg}, true
	}
	return err, false
}

func sanitizeDirectURLError(uerr *url.Error, depth int) (*url.Error, bool) {
	newOp := uerr.Op
	if containsProxyTokenParam(newOp) {
		newOp = redactProxyTokenInString(newOp)
	}
	newURL := uerr.URL
	if containsProxyTokenParam(newURL) {
		newURL = redactProxyTokenURL(newURL)
	}
	newErr, errChanged := sanitizeContentsURLErrorWithChanged(uerr.Err, depth)
	if !errChanged && newOp == uerr.Op && newURL == uerr.URL {
		return uerr, false
	}
	cloned := *uerr
	cloned.Op = newOp
	cloned.URL = newURL
	cloned.Err = newErr
	return &cloned, true
}

func redactProxyTokenURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return redactedURLMarker
	}
	q := parsed.Query()
	hasTokenQuery := q.Has(proxyTokenQueryKey)
	if hasTokenQuery {
		q.Set(proxyTokenQueryKey, "REDACTED")
		parsed.RawQuery = q.Encode()
	}
	redacted := parsed.String()
	if containsProxyTokenParam(rawURL) {
		if !hasTokenQuery {
			return redactedURLMarker
		}
		remaining := strings.Replace(redacted, proxyTokenQueryKey+"=REDACTED", "", 1)
		if containsProxyTokenParam(remaining) {
			return redactedURLMarker
		}
	}
	return redacted
}

func containsProxyTokenParam(s string) bool {
	lower := asciiLower(s)
	return strings.Contains(lower, proxyTokenQueryKey+"=") ||
		strings.Contains(lower, proxyTokenQueryKey+"%3d")
}

func redactProxyTokenInString(s string) string {
	if !containsProxyTokenParam(s) {
		return s
	}
	lower := asciiLower(s)
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		idxEq := strings.Index(lower[i:], proxyTokenQueryKey+"=")
		idxEsc := strings.Index(lower[i:], proxyTokenQueryKey+"%3d")

		idx := -1
		prefixLen := 0
		escaped := false
		if idxEq >= 0 && (idxEsc < 0 || idxEq < idxEsc) {
			idx = i + idxEq
			prefixLen = len(proxyTokenQueryKey) + 1
		} else if idxEsc >= 0 {
			idx = i + idxEsc
			prefixLen = len(proxyTokenQueryKey) + 3
			escaped = true
		}
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}

		valStart := idx + prefixLen
		b.WriteString(s[i:valStart])
		b.WriteString("REDACTED")

		valEnd := valStart
		for valEnd < len(s) {
			ch := s[valEnd]
			if isProxyTokenValueDelimiter(ch) {
				break
			}
			if escaped && ch == '%' && valEnd+2 < len(s) && isEscapedDelimiter(lower[valEnd:valEnd+3]) {
				break
			}
			valEnd++
		}
		i = valEnd
	}
	return b.String()
}

func isProxyTokenValueDelimiter(c byte) bool {
	switch c {
	case '&', '#', '"', '\'', '\\', ' ', '\t', '\r', '\n', ')', ']', '}', '>', ',', ';':
		return true
	default:
		return false
	}
}

func isEscapedDelimiter(seq string) bool {
	switch seq {
	case "%26", "%23", "%20", "%22", "%27", "%09", "%0a", "%0d":
		return true
	default:
		return false
	}
}

func asciiLower(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if b.Len() == 0 {
				b.Grow(len(s))
				b.WriteString(s[:i])
			}
			b.WriteByte(c + ('a' - 'A'))
		} else if b.Len() > 0 {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return s
	}
	return b.String()
}
