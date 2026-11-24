// Package hub queries Hugging Face model metadata only; model weights are
// downloaded exclusively by the remote GPU runtime.
package hub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// DefaultBaseURL is the default Hugging Face Hub models metadata endpoint.
	DefaultBaseURL = "https://huggingface.co/api/models"
	// DefaultTimeout is the default HTTP request timeout for Hub metadata calls.
	DefaultTimeout = 20 * time.Second

	maxQueryRunes        = 200
	maxCursorRunes       = 4096
	maxFamilyRunes       = 64
	maxGGUFShards        = 128
	maxHubRedirects      = 5
	maxResponseBodyBytes = 32 << 20 // 32 MiB defensive cap for metadata JSON
	missingMetaReason    = "缺少完整分片或 SHA-256 元数据"
)

// Sentinel errors with exact user-facing messages expected by the frontend i18n table.
var (
	ErrInvalidSearchQuery    = errors.New("模型搜索条件不合法")
	ErrInvalidSearchCursor   = errors.New("搜索分页不合法")
	ErrInvalidModalityFilter = errors.New("模型类型筛选条件不合法")
	ErrInvalidFamilyFilter   = errors.New("模型系列筛选条件不合法")
	ErrInvalidRepo           = errors.New("模型仓库不合法")
	ErrHubAccessDenied       = errors.New("这个仓库需要访问权限，请在设置中保存有权限的 Hugging Face Token")
	ErrHubRateLimited        = errors.New("Hugging Face 请求较多，请稍后重试")
	ErrHubNetwork            = errors.New("无法连接 Hugging Face，请检查网络后重试")
	ErrHubInvalidResponse    = errors.New("模型社区返回了无效结果")
	ErrHubMissingRevision    = errors.New("模型社区未返回固定版本，请重试")
)

var (
	defaultHTTPClient = newDefaultHTTPClient()

	sortMapping = map[string]string{
		"downloads": "downloads",
		"updated":   "lastModified",
		"likes":     "likes",
	}

	validModalities = map[string]struct{}{
		"all":   {},
		"text":  {},
		"image": {},
		"video": {},
	}

	videoPipelines = map[string]struct{}{
		"text-to-video":       {},
		"image-to-video":      {},
		"image-text-to-video": {},
		"video-to-video":      {},
		"any-to-video":        {},
	}

	modalitySearchPipelines = map[string][]string{
		"image": {"text-to-image", "image-to-image"},
		"video": {"text-to-video", "image-to-video", "image-text-to-video", "video-to-video"},
	}

	compoundCursorPrefix = "c1:"

	imagePipelines = map[string]struct{}{
		"text-to-image":                  {},
		"image-to-image":                 {},
		"unconditional-image-generation": {},
		"inpainting":                     {},
	}

	textPipelines = map[string]struct{}{
		"text-generation":      {},
		"conversational":       {},
		"image-text-to-text":   {},
		"text2text-generation": {},
	}

	nonChatPipelines = map[string]struct{}{
		"feature-extraction":           {},
		"sentence-similarity":          {},
		"fill-mask":                    {},
		"token-classification":         {},
		"text-classification":          {},
		"automatic-speech-recognition": {},
		"text-to-speech":               {},
		"text-to-audio":                {},
		"audio-classification":         {},
		"image-classification":         {},
		"object-detection":             {},
	}

	minimaxImageRepoRe = regexp.MustCompile(
		`(?i)(?:^|[-_/.])minimax[-_]?h3[-_.]image(?:$|[-_/.])`,
	)
	videoRepoRe = regexp.MustCompile(
		`(?i)(?:^|[-_/.])(?:minimax[-_]?h3|hyperflow[-_]?h3|fasth3|` +
			`wan2(?:\.\d+)?|wan[-_]?2|wan[-_]?animate|ltx[-_]?video|ltx[-_]?\d(?:\.\d+)?|ltxv|` +
			`hunyuan[-_]?video|cogvideox?|mochi[-_]?1|skyreels|seedvr2?|scail|` +
			`viggle|liveavatar|longcat[-_]?video|framepack|` +
			`animatediff|t2v|i2v|v2v)(?:$|[-_/.])`,
	)
	imageRepoRe = regexp.MustCompile(
		`(?i)(?:^|[-_/.])(?:flux(?:\.?[123])?|sdxl|sd3(?:\.?5)?|stable[-_]?diffusion|` +
			`z[-_]?image|qwen[-_]?image|moss[-_]?image|minimax[-_]?h3[-_.]image|` +
			`hidream|lumina[-_]?image|kolors|pixart|` +
			`auraflow|chroma|kontext|omnigen)(?:$|[-_/.])`,
	)

	familyPatterns = []struct {
		label   string
		pattern *regexp.Regexp
	}{
		{"Qwen-Image", regexp.MustCompile(`(?i)(?:^|[-_/])qwen[-_]?image`)},
		{"Qwen", regexp.MustCompile(`(?i)qwen|qwq`)},
		{"DeepSeek", regexp.MustCompile(`(?i)deepseek`)},
		{"GLM", regexp.MustCompile(`(?i)(?:^|[-_/])glm[-_.\d]|chatglm`)},
		{"Gemma", regexp.MustCompile(`(?i)gemma`)},
		{"Llama", regexp.MustCompile(`(?i)llama`)},
		{"Mistral", regexp.MustCompile(`(?i)mistral|mixtral|pixtral|devstral|magistral`)},
		{"MiniMax-H3", regexp.MustCompile(`(?i)(?:^|[-_/])(?:minimax[-_]?h3|hyperflow[-_]?h3|fasth3)`)},
		{"FLUX", regexp.MustCompile(`(?i)(?:^|[-_/])flux(?:\.?[123]|[-_.]|$)`)},
		{"Z-Image", regexp.MustCompile(`(?i)(?:^|[-_/])z[-_]?image`)},
		{"MOSS-Image", regexp.MustCompile(`(?i)(?:^|[-_/])moss[-_]?image`)},
		{"HiDream", regexp.MustCompile(`(?i)hidream`)},
		{"Chroma", regexp.MustCompile(`(?i)(?:^|[-_/])chroma(?:[-_.]|$)`)},
		{"Wan", regexp.MustCompile(`(?i)(?:^|[-_/])wan(?:2(?:\.\d+)?|[-_]?2|[-_]?animate|[-_]?1\.3b)`)},
		{"LTX", regexp.MustCompile(`(?i)(?:^|[-_/])ltx(?:[-_]?(?:video|\d)|v)`)},
		{"Hunyuan", regexp.MustCompile(`(?i)hunyuan`)},
		{"SeedVR2", regexp.MustCompile(`(?i)(?:^|[-_/])seedvr2?`)},
		{"SkyReels", regexp.MustCompile(`(?i)skyreels`)},
		{"SCAIL", regexp.MustCompile(`(?i)(?:^|[-_/])scail`)},
		{"LiveAvatar", regexp.MustCompile(`(?i)liveavatar`)},
		{"Viggle", regexp.MustCompile(`(?i)(?:^|[-_/])viggle`)},
		{"Mochi", regexp.MustCompile(`(?i)(?:^|[-_/])mochi(?:[-_.]|$)`)},
	}

	// Matches parameter scale tokens such as 27B or 125B-A6B bounded by start/end or [-_/.].
	paramsRe = regexp.MustCompile(
		`(?i)(?:^|[-_/.])(\d+(?:\.\d+)?[BM](?:-A\d+(?:\.\d+)?[BM])?)(?:$|[-_/.])`,
	)

	// Quantization patterns anchored at ^ with trailing (?:$|[^A-Za-z0-9]) so
	// findAllQuantMatches emulates Python's (?<![A-Za-z0-9])(...)(?![A-Za-z0-9])
	// with exact backtracking and non-overlapping semantics.
	quantPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(UD-(?:IQ|Q|TQ)\d[A-Za-z0-9_]*)(?:$|[^A-Za-z0-9])`),
		regexp.MustCompile(`(?i)^((?:IQ|Q|TQ)\d(?:_[01K](?:_[A-Za-z0-9]+)?|_[A-Za-z]{1,4}))(?:$|[^A-Za-z0-9])`),
		regexp.MustCompile(`(?i)^(MXFP4(?:_MOE)?|FP8(?:_E4M3(?:FN)?|_E5M2)?|BF16|F16|F32)(?:$|[^A-Za-z0-9])`),
		regexp.MustCompile(`(?i)^(Q[2-8])(?:$|[^A-Za-z0-9])`),
	}

	ggufSuffixRe    = regexp.MustCompile(`(?i)(?:-\d{5}-of-\d{5})?\.gguf$`)
	auxProjectorRe  = regexp.MustCompile(`(?:^|[._-])(?:mmproj|projector|vision[-_]?proj)(?:[._-]|\.gguf$)`)
	auxImatrixPreRe = regexp.MustCompile(`^imatrix(?:[._\d-]|\.gguf$)`)
	auxImatrixSufRe = regexp.MustCompile(`[._-]imatrix(?:[._-]?(?:micro\d*|v\d+))?\.gguf$`)
	auxVocabRe      = regexp.MustCompile(`^(?:vocab|tokenizer|adapter)(?:[._-]|\.gguf$)`)
	auxLoraRe       = regexp.MustCompile(`[._-]lora\.gguf$`)

	repoRe      = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)
	commitSHARe = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
	sha256Re    = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	splitGGUFRe = regexp.MustCompile(`(?i)^(.+)-(\d{5})-of-(\d{5})\.gguf$`)
)

// ColabOneHubSearchOptions configures a Hugging Face GGUF repository search.
type ColabOneHubSearchOptions struct {
	Query    string `json:"query"`
	Sort     string `json:"sort"`
	Cursor   string `json:"cursor"`
	Token    string `json:"token,omitempty"`
	Modality string `json:"modality"`
	Family   string `json:"family"`
}

// String redacts Token so formatting or logging search options never leaks Hugging Face tokens.
func (o ColabOneHubSearchOptions) String() string {
	token := ""
	if o.Token != "" {
		token = "[REDACTED]"
	}
	return fmt.Sprintf(
		"ColabOneHubSearchOptions{Query:%q, Sort:%q, Cursor:%q, Token:%q, Modality:%q, Family:%q}",
		o.Query,
		o.Sort,
		o.Cursor,
		token,
		o.Modality,
		o.Family,
	)
}

// GoString redacts Token when formatted with %#v.
func (o ColabOneHubSearchOptions) GoString() string {
	return o.String()
}

// ColabOneHubModel represents a single repository entry returned by SearchModels.
type ColabOneHubModel struct {
	Repo      string `json:"repo"`
	Downloads int64  `json:"downloads"`
	Likes     int64  `json:"likes"`
	Updated   string `json:"updated"`
	Gated     bool   `json:"gated"`
	Pipeline  string `json:"pipeline"`
	Modality  string `json:"modality"`
	ComfyOnly bool   `json:"comfy_only"`
	Family    string `json:"family"`
	Params    string `json:"params"`
}

// ColabOneHubSearchResult holds paginated Hugging Face model search results.
type ColabOneHubSearchResult struct {
	Models     []ColabOneHubModel `json:"models"`
	NextCursor string             `json:"next_cursor"`
}

// ColabOneHubFile describes a runnable GGUF artifact (single file or grouped shards).
type ColabOneHubFile struct {
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	Shards       int    `json:"shards"`
	Quantization string `json:"quantization"`
	Supported    bool   `json:"supported"`
	Reason       string `json:"reason"`
}

// ColabOneHubModelFiles describes pinned commit metadata and runnable GGUF files of a repo.
type ColabOneHubModelFiles struct {
	Repo      string            `json:"repo"`
	Revision  string            `json:"revision"`
	Pipeline  string            `json:"pipeline"`
	Modality  string            `json:"modality"`
	ComfyOnly bool              `json:"comfy_only"`
	Family    string            `json:"family"`
	Params    string            `json:"params"`
	Files     []ColabOneHubFile `json:"files"`
}

// ColabOneHubClient queries Hugging Face Hub metadata APIs.
type ColabOneHubClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewColabOneHubClient creates a ColabOneHubClient with default base URL,
// timeout, and strict redirect policy.
func NewColabOneHubClient() *ColabOneHubClient {
	return &ColabOneHubClient{
		BaseURL:    DefaultBaseURL,
		HTTPClient: newDefaultHTTPClient(),
	}
}

func newDefaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       DefaultTimeout,
		CheckRedirect: colabOneHubCheckRedirect,
	}
}

func colabOneHubCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxHubRedirects {
		return errors.New("stopped after 5 redirects")
	}
	if req == nil || req.URL == nil {
		return errors.New("invalid redirect request")
	}
	req.Header.Del("Referer")
	if len(via) == 0 || via[0] == nil || via[0].URL == nil {
		req.Header.Del("Authorization")
		return nil
	}
	origURL := via[0].URL
	prevURL := via[len(via)-1].URL
	if (strings.EqualFold(origURL.Scheme, "https") || (prevURL != nil && strings.EqualFold(prevURL.Scheme, "https"))) &&
		!strings.EqualFold(req.URL.Scheme, "https") {
		return errors.New("refusing insecure redirect downgrade")
	}
	sameOrigin := strings.EqualFold(req.URL.Scheme, origURL.Scheme) &&
		strings.EqualFold(req.URL.Host, origURL.Host)
	if sameOrigin {
		for _, prev := range via[1:] {
			if prev == nil || prev.URL == nil ||
				!strings.EqualFold(prev.URL.Scheme, origURL.Scheme) ||
				!strings.EqualFold(prev.URL.Host, origURL.Host) {
				sameOrigin = false
				break
			}
		}
	}
	if !sameOrigin {
		req.Header.Del("Authorization")
	}
	return nil
}

// ExtractQuantization extracts a normalized GGUF quantization label from a filename
// or its parent directory path.
func ExtractQuantization(path string) string {
	if path == "" {
		return "GGUF"
	}
	rawParts := strings.Split(path, "/")
	parts := make([]string, 0, len(rawParts))
	for _, p := range rawParts {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "GGUF"
	}
	base := ggufSuffixRe.ReplaceAllString(parts[len(parts)-1], "")
	candidates := make([]string, 0, 2)
	candidates = append(candidates, base)
	if len(parts) > 1 {
		candidates = append(candidates, strings.Join(parts[:len(parts)-1], "/"))
	}
	for _, text := range candidates {
		for _, pattern := range quantPatterns {
			if last := findLastQuantMatch(pattern, text); last != "" {
				return strings.ToUpper(last)
			}
		}
	}
	return "GGUF"
}

// IsAuxiliaryGGUF returns true for non-runnable auxiliary GGUF artifacts
// (mmproj, projector, vision_proj, imatrix calibration files, vocab, tokenizer, adapter, lora).
func IsAuxiliaryGGUF(path string) bool {
	base := strings.ToLower(lastSlashSegment(path))
	if auxProjectorRe.MatchString(base) {
		return true
	}
	if auxImatrixPreRe.MatchString(base) {
		return true
	}
	if auxImatrixSufRe.MatchString(base) {
		stem := strings.TrimSuffix(base, ".gguf")
		if ExtractQuantization(stem) == "GGUF" {
			return true
		}
	}
	if auxVocabRe.MatchString(base) {
		return true
	}
	if auxLoraRe.MatchString(base) {
		return true
	}
	return false
}

// ClassifyModality classifies a Hugging Face repository as "text", "image", or "video".
func ClassifyModality(pipelineTag string, tags []string, repo string) string {
	pipeline := strings.ToLower(strings.TrimSpace(pipelineTag))
	if _, ok := videoPipelines[pipeline]; ok {
		return "video"
	}
	if _, ok := imagePipelines[pipeline]; ok {
		return "image"
	}
	if _, ok := textPipelines[pipeline]; ok {
		return "text"
	}
	hasImageTag := false
	for _, tag := range tags {
		norm := strings.ToLower(strings.TrimSpace(tag))
		if _, ok := videoPipelines[norm]; ok {
			return "video"
		}
		if _, ok := imagePipelines[norm]; ok {
			hasImageTag = true
		}
	}
	if hasImageTag {
		return "image"
	}
	repoName := strings.TrimSpace(repo)
	if minimaxImageRepoRe.MatchString(repoName) {
		return "image"
	}
	if videoRepoRe.MatchString(repoName) {
		return "video"
	}
	if imageRepoRe.MatchString(repoName) {
		return "image"
	}
	return "text"
}

// InferFamily infers a recognizable model family tag from a repository slug.
func InferFamily(repo string) string {
	short := lastSlashSegment(repo)
	for _, entry := range familyPatterns {
		if entry.pattern.MatchString(short) {
			return entry.label
		}
	}
	return ""
}

// InferParams infers a human-readable parameter scale badge (e.g. 27B, 125B-A6B) from a repo slug.
func InferParams(repo string) string {
	short := lastSlashSegment(repo)
	match := paramsRe.FindStringSubmatch(short)
	if len(match) < 2 {
		return ""
	}
	return strings.ToUpper(match[1])
}

// SearchModels queries the Hugging Face models API for GGUF repositories.
func (c *ColabOneHubClient) SearchModels(
	ctx context.Context,
	opts ColabOneHubSearchOptions,
) (*ColabOneHubSearchResult, error) {
	sortKey := opts.Sort
	if sortKey == "" {
		sortKey = "downloads"
	}
	sortParam, ok := sortMapping[sortKey]
	if utf8.RuneCountInString(opts.Query) > maxQueryRunes || !ok {
		return nil, ErrInvalidSearchQuery
	}
	if utf8.RuneCountInString(opts.Cursor) > maxCursorRunes {
		return nil, ErrInvalidSearchCursor
	}
	normModality := strings.ToLower(strings.TrimSpace(opts.Modality))
	if normModality == "" {
		normModality = "all"
	}
	if _, ok := validModalities[normModality]; !ok {
		return nil, ErrInvalidModalityFilter
	}
	if utf8.RuneCountInString(opts.Family) > maxFamilyRunes {
		return nil, ErrInvalidFamilyFilter
	}

	cleanQuery := strings.TrimSpace(opts.Query)
	cleanFamily := strings.TrimSpace(opts.Family)
	effectiveQuery := cleanQuery
	if cleanFamily != "" && !strings.Contains(strings.ToLower(cleanQuery), strings.ToLower(cleanFamily)) {
		effectiveQuery = strings.TrimSpace(cleanFamily + " " + cleanQuery)
	}

	if pipelines, multi := modalitySearchPipelines[normModality]; multi {
		return c.searchMultiPipelineModels(ctx, effectiveQuery, sortKey, sortParam, normModality, pipelines, opts.Cursor, opts.Token)
	}

	if strings.HasPrefix(opts.Cursor, compoundCursorPrefix) {
		return nil, ErrInvalidSearchCursor
	}

	limit := "20"
	if normModality == "text" {
		limit = "30"
	}
	models, nextCursor, err := c.fetchHubModelsPage(ctx, effectiveQuery, sortParam, normModality, "", limit, opts.Cursor, opts.Token)
	if err != nil {
		return nil, err
	}
	return &ColabOneHubSearchResult{
		Models:     models,
		NextCursor: nextCursor,
	}, nil
}

type pipelineTarget struct {
	pipeline string
	cursor   string
}

type pipelinePageResult struct {
	pipeline   string
	models     []ColabOneHubModel
	nextCursor string
	err        error
}

func encodeCompoundCursor(cursors map[string]string, priorityOrder []string) string {
	if len(cursors) == 0 {
		return ""
	}
	encode := func(m map[string]string) string {
		if len(m) == 0 {
			return ""
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return ""
		}
		return compoundCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
	}
	if encoded := encode(cursors); encoded != "" && utf8.RuneCountInString(encoded) <= maxCursorRunes {
		return encoded
	}
	// If the combined cursor exceeds maxCursorRunes, retain highest-priority pipelines that fit.
	trimmed := make(map[string]string, len(cursors))
	for _, p := range priorityOrder {
		v, ok := cursors[p]
		if !ok {
			continue
		}
		trimmed[p] = v
		if encoded := encode(trimmed); encoded == "" || utf8.RuneCountInString(encoded) > maxCursorRunes {
			delete(trimmed, p)
		}
	}
	return encode(trimmed)
}

func decodeCompoundCursor(cursor string, allowed []string) (map[string]string, error) {
	payload := strings.TrimPrefix(cursor, compoundCursorPrefix)
	if payload == "" {
		return nil, ErrInvalidSearchCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, ErrInvalidSearchCursor
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded) == 0 {
		return nil, ErrInvalidSearchCursor
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, p := range allowed {
		allowedSet[p] = struct{}{}
	}
	for k, v := range decoded {
		if _, ok := allowedSet[k]; !ok || strings.TrimSpace(v) == "" {
			return nil, ErrInvalidSearchCursor
		}
	}
	return decoded, nil
}

func (c *ColabOneHubClient) searchMultiPipelineModels(
	ctx context.Context,
	effectiveQuery string,
	sortKey string,
	sortParam string,
	normModality string,
	pipelines []string,
	cursor string,
	token string,
) (*ColabOneHubSearchResult, error) {
	limit := "15"
	if normModality == "video" {
		limit = "10"
	}

	var targets []pipelineTarget
	useCompoundCursor := true
	if cursor == "" {
		targets = make([]pipelineTarget, 0, len(pipelines))
		for _, p := range pipelines {
			targets = append(targets, pipelineTarget{pipeline: p})
		}
	} else if strings.HasPrefix(cursor, compoundCursorPrefix) {
		cursorMap, err := decodeCompoundCursor(cursor, pipelines)
		if err != nil {
			return nil, err
		}
		targets = make([]pipelineTarget, 0, len(cursorMap))
		for _, p := range pipelines {
			if cur, ok := cursorMap[p]; ok {
				targets = append(targets, pipelineTarget{pipeline: p, cursor: cur})
			}
		}
	} else {
		// Backward-compatible fallback for legacy raw Hugging Face cursors.
		useCompoundCursor = false
		targets = []pipelineTarget{{pipeline: pipelines[0], cursor: cursor}}
	}

	pages := make([]pipelinePageResult, len(targets))
	if len(targets) == 1 {
		t := targets[0]
		models, nextCur, err := c.fetchHubModelsPage(ctx, effectiveQuery, sortParam, normModality, t.pipeline, limit, t.cursor, token)
		pages[0] = pipelinePageResult{pipeline: t.pipeline, models: models, nextCursor: nextCur, err: err}
	} else {
		if ctx == nil {
			ctx = context.Background()
		}
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var wg sync.WaitGroup
		wg.Add(len(targets))
		for i, t := range targets {
			go func(idx int, target pipelineTarget) {
				defer wg.Done()
				models, nextCur, err := c.fetchHubModelsPage(reqCtx, effectiveQuery, sortParam, normModality, target.pipeline, limit, target.cursor, token)
				if errors.Is(err, ErrHubAccessDenied) || errors.Is(err, ErrHubRateLimited) {
					cancel()
				}
				pages[idx] = pipelinePageResult{
					pipeline:   target.pipeline,
					models:     models,
					nextCursor: nextCur,
					err:        err,
				}
			}(i, t)
		}
		wg.Wait()
	}

	var firstErr error
	succeeded := 0
	for _, page := range pages {
		if page.err != nil {
			if errors.Is(page.err, ErrHubAccessDenied) {
				return nil, page.err
			}
			if firstErr == nil {
				firstErr = page.err
			}
			continue
		}
		succeeded++
	}
	for _, page := range pages {
		if errors.Is(page.err, ErrHubRateLimited) {
			return nil, page.err
		}
	}
	if succeeded == 0 {
		return nil, firstErr
	}

	merged := make([]ColabOneHubModel, 0, len(pages)*10)
	seenIdx := make(map[string]int)
	nextMap := make(map[string]string)
	for i, page := range pages {
		if page.err != nil {
			// On page 2+, retain the failed sub-pipeline's previous cursor so the next
			// "Load More" retries it cleanly instead of silently losing its remaining pages.
			if targets[i].cursor != "" {
				nextMap[page.pipeline] = targets[i].cursor
			}
			continue
		}
		if page.nextCursor != "" {
			nextMap[page.pipeline] = page.nextCursor
		}
		for _, m := range page.models {
			if idx, exists := seenIdx[m.Repo]; exists {
				if m.Downloads > merged[idx].Downloads {
					merged[idx] = m
				}
				continue
			}
			seenIdx[m.Repo] = len(merged)
			merged = append(merged, m)
		}
	}

	sortHubModels(merged, sortKey)

	nextCursor := ""
	if useCompoundCursor {
		nextCursor = encodeCompoundCursor(nextMap, pipelines)
	} else if len(pages) == 1 && pages[0].err == nil {
		nextCursor = pages[0].nextCursor
	}
	return &ColabOneHubSearchResult{
		Models:     merged,
		NextCursor: nextCursor,
	}, nil
}

func parseHubTimestampNano(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts.UnixNano(), true
	}
	return 0, false
}

func sortHubModels(models []ColabOneHubModel, sortKey string) {
	var tsCache map[string]int64
	var tsValid map[string]bool
	if sortKey == "updated" && len(models) > 1 {
		tsCache = make(map[string]int64, len(models))
		tsValid = make(map[string]bool, len(models))
		for _, m := range models {
			if _, seen := tsValid[m.Updated]; !seen {
				ns, ok := parseHubTimestampNano(m.Updated)
				tsCache[m.Updated] = ns
				tsValid[m.Updated] = ok
			}
		}
	}
	sort.SliceStable(models, func(i, j int) bool {
		a, b := models[i], models[j]
		switch sortKey {
		case "likes":
			if a.Likes != b.Likes {
				return a.Likes > b.Likes
			}
			if a.Downloads != b.Downloads {
				return a.Downloads > b.Downloads
			}
		case "updated":
			aOk, bOk := tsValid[a.Updated], tsValid[b.Updated]
			if aOk && bOk {
				if tsCache[a.Updated] != tsCache[b.Updated] {
					return tsCache[a.Updated] > tsCache[b.Updated]
				}
			} else if aOk != bOk {
				return aOk
			} else if a.Updated != b.Updated {
				return a.Updated > b.Updated
			}
			if a.Downloads != b.Downloads {
				return a.Downloads > b.Downloads
			}
		default:
			if a.Downloads != b.Downloads {
				return a.Downloads > b.Downloads
			}
			if a.Likes != b.Likes {
				return a.Likes > b.Likes
			}
		}
		return a.Repo < b.Repo
	})
}

func (c *ColabOneHubClient) fetchHubModelsPage(
	ctx context.Context,
	effectiveQuery string,
	sortParam string,
	normModality string,
	pipelineTag string,
	limit string,
	cursor string,
	token string,
) ([]ColabOneHubModel, string, error) {
	params := url.Values{
		"search":    {effectiveQuery},
		"filter":    {"gguf"},
		"sort":      {sortParam},
		"direction": {"-1"},
		"limit":     {limit},
	}
	if pipelineTag != "" {
		params.Set("pipeline_tag", pipelineTag)
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	body, headers, err := c.doGet(ctx, "", params, token)
	if err != nil {
		return nil, "", err
	}

	rawItems, err := decodeJSONArray(body)
	if err != nil {
		return nil, "", ErrHubInvalidResponse
	}

	models := make([]ColabOneHubModel, 0, len(rawItems))
	for _, elem := range rawItems {
		item, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		repo := stringField(item["id"])
		if !isValidRepo(repo) {
			continue
		}
		pipeline := stringField(item["pipeline_tag"])
		tags := stringSliceField(item["tags"])
		itemModality := ClassifyModality(pipeline, tags, repo)
		switch normModality {
		case "image":
			itemModality = "image"
		case "video":
			itemModality = "video"
		case "text":
			if itemModality != "text" {
				continue
			}
			if _, nonChat := nonChatPipelines[strings.ToLower(strings.TrimSpace(pipeline))]; nonChat {
				continue
			}
		}
		models = append(models, ColabOneHubModel{
			Repo:      repo,
			Downloads: int64Field(item["downloads"]),
			Likes:     int64Field(item["likes"]),
			Updated:   stringField(item["lastModified"]),
			Gated:     truthyField(item["gated"]),
			Pipeline:  pipeline,
			Modality:  itemModality,
			ComfyOnly: itemModality == "image" || itemModality == "video",
			Family:    InferFamily(repo),
			Params:    InferParams(repo),
		})
	}

	nextCursor := extractTrustedNextCursor(headers.Values("Link"))
	return models, nextCursor, nil
}

// ModelFiles fetches the pinned main revision metadata and runnable GGUF file list for a repo.
func (c *ColabOneHubClient) ModelFiles(
	ctx context.Context,
	repo string,
	token string,
) (*ColabOneHubModelFiles, error) {
	if !isValidRepo(repo) {
		return nil, ErrInvalidRepo
	}
	params := url.Values{"blobs": {"true"}}
	body, _, err := c.doGet(ctx, "/"+repo+"/revision/main", params, token)
	if err != nil {
		return nil, err
	}

	info, err := decodeJSONObject(body)
	if err != nil {
		return nil, ErrHubInvalidResponse
	}

	revision := stringField(info["sha"])
	if !commitSHARe.MatchString(revision) {
		return nil, ErrHubMissingRevision
	}

	pipeline := stringField(info["pipeline_tag"])
	tags := stringSliceField(info["tags"])
	itemModality := ClassifyModality(pipeline, tags, repo)

	siblings := make(map[string]map[string]any)
	if rawSiblings, ok := info["siblings"].([]any); ok {
		for _, elem := range rawSiblings {
			item, ok := elem.(map[string]any)
			if !ok {
				continue
			}
			rfilename, ok := item["rfilename"].(string)
			if !ok || rfilename == "" {
				continue
			}
			siblings[rfilename] = item
		}
	}

	files := make([]ColabOneHubFile, 0, len(siblings))
	for name := range siblings {
		if !strings.HasSuffix(strings.ToLower(name), ".gguf") || IsAuxiliaryGGUF(name) {
			continue
		}
		match := splitGGUFRe.FindStringSubmatch(name)
		if match != nil && match[2] != "00001" {
			continue
		}
		count := 1
		if match != nil {
			n, convErr := strconv.Atoi(match[3])
			if convErr != nil {
				count = 0
			} else {
				count = n
			}
		}

		var group []string
		if match != nil && count >= 1 && count <= maxGGUFShards {
			suffix := name[len(name)-5:]
			group = make([]string, count)
			for i := 1; i <= count; i++ {
				group[i-1] = fmt.Sprintf("%s-%05d-of-%05d%s", match[1], i, count, suffix)
			}
		} else {
			group = []string{name}
		}

		supported := count > 0 && count <= maxGGUFShards
		var totalSize int64
		for _, path := range group {
			var lfs map[string]any
			if sib := siblings[path]; sib != nil {
				if rawLFS, ok := sib["lfs"].(map[string]any); ok {
					lfs = rawLFS
				}
			}
			size, sizeOK := parsePositiveJSONInt64(lfs["size"])
			if sizeOK {
				if size > math.MaxInt64-totalSize {
					supported = false
					totalSize = math.MaxInt64
				} else {
					totalSize += size
				}
			}
			sha, shaOK := lfs["sha256"].(string)
			if !sizeOK || !shaOK || !sha256Re.MatchString(sha) {
				supported = false
			}
		}

		reason := ""
		if !supported {
			reason = missingMetaReason
		}
		files = append(files, ColabOneHubFile{
			Filename:     name,
			Size:         totalSize,
			Shards:       count,
			Quantization: ExtractQuantization(name),
			Supported:    supported,
			Reason:       reason,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].Size != files[j].Size {
			return files[i].Size < files[j].Size
		}
		return files[i].Filename < files[j].Filename
	})

	return &ColabOneHubModelFiles{
		Repo:      repo,
		Revision:  revision,
		Pipeline:  pipeline,
		Modality:  itemModality,
		ComfyOnly: itemModality == "image" || itemModality == "video",
		Family:    InferFamily(repo),
		Params:    InferParams(repo),
		Files:     files,
	}, nil
}

func (c *ColabOneHubClient) baseURL() string {
	if c != nil {
		if trimmed := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/"); trimmed != "" {
			return trimmed
		}
	}
	return DefaultBaseURL
}

func (c *ColabOneHubClient) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		if c.HTTPClient.CheckRedirect != nil {
			return c.HTTPClient
		}
		clone := *c.HTTPClient
		clone.CheckRedirect = colabOneHubCheckRedirect
		return &clone
	}
	return defaultHTTPClient
}

func (c *ColabOneHubClient) doGet(
	ctx context.Context,
	path string,
	params url.Values,
	token string,
) ([]byte, http.Header, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	target, err := url.Parse(c.baseURL() + path)
	if err != nil {
		return nil, nil, ErrHubNetwork
	}
	if len(params) > 0 {
		q := target.Query()
		for k, vals := range params {
			q[k] = vals
		}
		target.RawQuery = q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, nil, ErrHubNetwork
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, nil, ErrHubNetwork
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, nil, ErrHubAccessDenied
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, nil, ErrHubRateLimited
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, nil, ErrHubNetwork
	}

	limited := io.LimitReader(resp.Body, maxResponseBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, nil, ErrHubNetwork
	}
	if int64(len(body)) > maxResponseBodyBytes {
		return nil, nil, ErrHubInvalidResponse
	}
	return body, resp.Header, nil
}

// findLastQuantMatch returns the last non-overlapping match of an anchored
// pattern (^(P)(?:$|[^A-Za-z0-9])) at positions not preceded by [A-Za-z0-9].
func findLastQuantMatch(pattern *regexp.Regexp, text string) string {
	last := ""
	for pos := 0; pos < len(text); {
		if pos == 0 || !isASCIIAlnum(text[pos-1]) {
			if loc := pattern.FindStringSubmatchIndex(text[pos:]); loc != nil && loc[3] > loc[2] {
				last = text[pos+loc[2] : pos+loc[3]]
				pos += loc[3]
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(text[pos:])
		if size <= 0 {
			size = 1
		}
		pos += size
	}
	return last
}

func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func lastSlashSegment(s string) string {
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		return s[idx+1:]
	}
	return s
}

func isValidRepo(repo string) bool {
	if !repoRe.MatchString(repo) {
		return false
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return false
	}
	return owner != "." && owner != ".." && name != "." && name != ".."
}

func extractTrustedNextCursor(linkHeaders []string) string {
	nextURL := parseNextLinkURL(linkHeaders)
	if nextURL == "" {
		return ""
	}
	parsed, err := url.Parse(nextURL)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "https" ||
		parsed.Host != "huggingface.co" ||
		parsed.Path != "/api/models" ||
		parsed.User != nil {
		return ""
	}
	return parsed.Query().Get("cursor")
}

func parseNextLinkURL(headers []string) string {
	nextURL := ""
	for _, header := range headers {
		s := header
		for len(s) > 0 {
			s = strings.TrimLeft(s, " \t,")
			if s == "" {
				break
			}
			if s[0] != '<' {
				_, s = splitNextLinkSegment(s)
				continue
			}
			end := strings.IndexByte(s, '>')
			if end < 0 {
				break
			}
			target := strings.TrimSpace(s[1:end])
			paramsPart, rest := splitNextLinkSegment(s[end+1:])
			s = rest
			if hasNextRel(paramsPart) {
				nextURL = target
			}
		}
	}
	return nextURL
}

func splitNextLinkSegment(s string) (segment, rest string) {
	inQuotes := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuotes {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inQuotes = false
			}
			continue
		}
		if c == '"' {
			inQuotes = true
			continue
		}
		if c == ',' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

func hasNextRel(paramsPart string) bool {
	s := paramsPart
	for len(s) > 0 {
		var part string
		part, s = splitParam(s)
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		key := strings.Trim(strings.TrimSpace(part[:eq]), `"'`)
		val := strings.Trim(strings.TrimSpace(part[eq+1:]), `"' `)
		if strings.EqualFold(key, "rel") && val == "next" {
			return true
		}
	}
	return false
}

func splitParam(s string) (param, rest string) {
	inQuotes := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuotes {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inQuotes = false
			}
			continue
		}
		if c == '"' {
			inQuotes = true
			continue
		}
		if c == ';' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

func decodeJSONArray(body []byte) ([]any, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, ErrHubInvalidResponse
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var items []any
	if err := dec.Decode(&items); err != nil {
		return nil, err
	}
	if err := ensureNoTrailingTokens(dec); err != nil {
		return nil, err
	}
	return items, nil
}

func decodeJSONObject(body []byte) (map[string]any, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, ErrHubInvalidResponse
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	if err := ensureNoTrailingTokens(dec); err != nil {
		return nil, err
	}
	return obj, nil
}

func ensureNoTrailingTokens(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return ErrHubInvalidResponse
	}
	return nil
}

func stringField(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "True"
		}
		return ""
	default:
		return ""
	}
}

func stringSliceField(v any) []string {
	raw, ok := v.([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, elem := range raw {
		if s, ok := elem.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

const (
	minInt64Float64 = -9223372036854775808.0 // -2^63 (exact in IEEE 754 float64)
	maxInt64Float64 = 9223372036854775808.0  //  2^63 (exact in IEEE 754 float64; exclusive upper bound)
)

func int64Field(v any) int64 {
	num, ok := v.(json.Number)
	if !ok {
		return 0
	}
	if n, err := strconv.ParseInt(num.String(), 10, 64); err == nil {
		return n
	} else if errors.Is(err, strconv.ErrRange) {
		return 0
	}
	if f, err := strconv.ParseFloat(num.String(), 64); err == nil {
		if math.IsNaN(f) || math.IsInf(f, 0) || f < minInt64Float64 || f >= maxInt64Float64 {
			return 0
		}
		return int64(f)
	}
	return 0
}

func parsePositiveJSONInt64(v any) (int64, bool) {
	num, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	s := num.String()
	if strings.ContainsAny(s, ".eE") {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func truthyField(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		if n, err := strconv.ParseInt(x.String(), 10, 64); err == nil {
			return n != 0
		}
		if f, err := strconv.ParseFloat(x.String(), 64); err == nil {
			return f != 0
		}
		return false
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return false
	}
}
