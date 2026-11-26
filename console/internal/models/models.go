// Package models provides data-only model manifests, OpenAI API model ID
// normalization/deduplication, and Hugging Face metadata import resolution
// shared by the ColabOne desktop controller and remote runtime.
package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/hub"
)

const (
	// COLABONE_API_MODEL_PREFIX is the default namespace prefix for public OpenAI API model IDs.
	COLABONE_API_MODEL_PREFIX = "colabone/"
	// COLABONE_AUTO_API_MODEL_ID is the reserved alias routing to the currently loaded model.
	COLABONE_AUTO_API_MODEL_ID = "colabone/auto"

	defaultHFBaseURL     = "https://huggingface.co"
	defaultHTTPTimeout   = 30 * time.Second
	maxHFRedirects       = 5
	maxHFResponseBytes   = 32 << 20 // 32 MiB safety cap for HF repo revision metadata JSON
	defaultContextLength = 8192
	defaultTemperature   = 0.7
	defaultSource        = "Hugging Face"
	defaultKind          = "chat"
	defaultFileRole      = "model"
)

var (
	defaultHFHTTPClient = newDefaultHFHTTPClient()

	colabOneAPIModelIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,119}$`)
	modelSpecIDRe        = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
	hfRepoRe             = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)
	hfRevisionRe         = regexp.MustCompile(`^[\w.-]+$`)
	commitSHARe          = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
	sha256HexRe          = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	hfURLRe              = regexp.MustCompile(`(?i)^https?://(?:www\.)?huggingface\.co/([\w.-]+/[\w.-]+)(?:/(?:blob|resolve|tree)/([^/?#]+)(?:/([^?#]+))?)?/?(?:\?[^#]*)?(?:#.*)?$`)
	ggufSuffixRe         = regexp.MustCompile(`(?i)(?:-\d{5}-of-\d{5})?\.gguf$`)
	shardFilenameRe      = regexp.MustCompile(`(?i)^(.+)-\d{5}-of-(\d{5})\.gguf$`)

	nonSlugCharsRe    = regexp.MustCompile(`[^a-zA-Z0-9.-]+`)
	multiDotsRe       = regexp.MustCompile(`\.{2,}`)
	multiHyphensRe    = regexp.MustCompile(`-{2,}`)
	tokenSplitRe      = regexp.MustCompile(`[^a-zA-Z0-9.]+`)
	ownerLowerSplitRe = regexp.MustCompile(`[^a-z0-9]+`)
	ownerAlnumSplitRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	fullQuantSlugRe   = regexp.MustCompile(`[^a-z0-9_-]+`)
	embeddedQuantBody = regexp.MustCompile(`(?i)^((?:ud[-_])?(?:i|t)?q[1-8](?:_[0-9a-z]+)*)(?:$|[-_\p{Z}\t\n\v\f\r])`)
	quantTokenRe      = regexp.MustCompile(`(?i)^(?:(?:i|t)?q[1-8](?:_[0-9a-z]+)*|k_(?:s|m|l|xl|xxl))$`)
	dateTagRe         = regexp.MustCompile(`^(?:(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])|2[3-9](?:0[1-9]|1[0-2])|20\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01]))$`)
	repackVersionRe   = regexp.MustCompile(`(?i)^v\d+(?:\.\d+)?$`)
	compactQuantRe    = regexp.MustCompile(`(?i)(mxfp4|nvfp4|bf16|fp16|f16|f32|fp8|(?:i|t)?q[1-8])`)

	// Private helper regexes matching common/hub.py semantics (RE2-safe equivalents of lookarounds).
	hubQuantAnchoredPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(UD-(?:IQ|Q|TQ)\d[A-Za-z0-9_]*)(?:$|[^A-Za-z0-9])`),
		regexp.MustCompile(`(?i)^((?:IQ|Q|TQ)\d(?:_[01K](?:_[A-Za-z0-9]+)?|_[A-Za-z]{1,4}))(?:$|[^A-Za-z0-9])`),
		regexp.MustCompile(`(?i)^(MXFP4(?:_MOE)?|FP8(?:_E4M3(?:FN)?|_E5M2)?|BF16|F16|F32)(?:$|[^A-Za-z0-9])`),
		regexp.MustCompile(`(?i)^(Q[2-8])(?:$|[^A-Za-z0-9])`),
	}
	auxMMProjRe    = regexp.MustCompile(`(?:^|[._-])(?:mmproj|projector|vision[-_]?proj)(?:[._-]|\.gguf$)`)
	auxIMatrixHead = regexp.MustCompile(`^imatrix(?:[._\d-]|\.gguf$)`)
	auxIMatrixTail = regexp.MustCompile(`[._-]imatrix(?:[._-]?(?:micro\d*|v\d+))?\.gguf$`)
	auxVocabHead   = regexp.MustCompile(`^(?:vocab|tokenizer|adapter)(?:[._-]|\.gguf$)`)
	auxLoraTail    = regexp.MustCompile(`[._-]lora\.gguf$`)
)

var (
	knownPackagerPrefixes = map[string]struct{}{
		"huihui":        {},
		"unsloth":       {},
		"bartowski":     {},
		"mradermacher":  {},
		"lmstudio":      {},
		"maziyarpanahi": {},
		"quantfactory":  {},
		"tensorblock":   {},
		"ggml":          {},
		"bullerwins":    {},
	}
	formatNoiseTokens = map[string]struct{}{
		"gguf":      {},
		"ggml":      {},
		"imatrix":   {},
		"exl2":      {},
		"awq":       {},
		"gptq":      {},
		"mlx":       {},
		"onnx":      {},
		"bnb":       {},
		"4bit":      {},
		"8bit":      {},
		"fp8":       {},
		"fp16":      {},
		"bf16":      {},
		"f16":       {},
		"f32":       {},
		"quant":     {},
		"quantized": {},
		"ud":        {},
		"mxfp4":     {},
		"nvfp4":     {},
	}
	repackModifierTokens = map[string]struct{}{
		"abliterated": {},
		"uncensored":  {},
		"heretic":     {},
		"ara":         {},
		"rvn":         {},
		"rco":         {},
		"gsq":         {},
		"lorablated":  {},
		"obliterated": {},
		"unfiltered":  {},
		"decensored":  {},
	}
	chatSuffixTokens = map[string]struct{}{
		"instruct": {},
		"it":       {},
	}
	allowedModelFileKeys = map[string]struct{}{
		"repo":     {},
		"revision": {},
		"path":     {},
		"size":     {},
		"sha256":   {},
		"role":     {},
	}
	allowedModelSpecKeys = map[string]struct{}{
		"id":             {},
		"api_id":         {},
		"name":           {},
		"description":    {},
		"min_vram_mib":   {},
		"context_length": {},
		"temperature":    {},
		"cpu_moe_layers": {},
		"source":         {},
		"kind":           {},
		"files":          {},
	}
)

// ColabOneHTTPStatusError represents a non-2xx HTTP response from Hugging Face
// without exposing request URLs or credentials.
type ColabOneHTTPStatusError struct {
	StatusCode int
	Status     string
}

func (e *ColabOneHTTPStatusError) Error() string {
	return fmt.Sprintf("服务返回 HTTP %d，请检查授权和配置", e.StatusCode)
}

// ColabOneModelFile describes one pinned GGUF file (entry model or shard) in a model manifest.
type ColabOneModelFile struct {
	Repo     string `json:"repo"`
	Revision string `json:"revision"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Role     string `json:"role"`
}

// ApplyDefaults populates default fields and normalizes the SHA-256 digest to lowercase.
func (f *ColabOneModelFile) ApplyDefaults() {
	if f == nil {
		return
	}
	if f.Role == "" {
		f.Role = defaultFileRole
	}
	if f.SHA256 != "" {
		f.SHA256 = strings.ToLower(f.SHA256)
	}
}

// Validate checks all ColabOneModelFile invariants and normalizes SHA256 to lowercase.
func (f *ColabOneModelFile) Validate() error {
	if f == nil {
		return errors.New("模型文件配置不能为空")
	}
	if !isValidHFRepo(f.Repo) {
		return errors.New("需要 Hugging Face 的 owner/repository")
	}
	if !commitSHARe.MatchString(f.Revision) {
		return errors.New("模型必须固定到完整 commit SHA")
	}
	if !isValidRelativeGGUFPath(f.Path) {
		return errors.New("模型文件路径不合法")
	}
	if f.Size <= 0 {
		return errors.New("文件大小必须大于 0")
	}
	if !sha256HexRe.MatchString(f.SHA256) {
		return errors.New("文件需要可信的 SHA-256")
	}
	f.SHA256 = strings.ToLower(f.SHA256)
	if f.Role != "model" && f.Role != "shard" {
		return errors.New("聊天模型只能包含 GGUF 模型和分片")
	}
	return nil
}

// UnmarshalJSON performs strict JSON decoding for ColabOneModelFile, rejecting
// unknown fields, null values, and invalid manifests while applying default role.
func (f *ColabOneModelFile) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return errors.New("模型文件配置必须为 JSON 对象")
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &rawMap); err != nil {
		return err
	}
	for k := range rawMap {
		if _, ok := allowedModelFileKeys[k]; !ok {
			return fmt.Errorf("模型文件配置包含不支持的字段: %s", k)
		}
	}
	tmp := ColabOneModelFile{
		Role: defaultFileRole,
	}
	for k, raw := range rawMap {
		if isJSONNull(raw) {
			return fmt.Errorf("模型文件字段 %s 不能为空值", k)
		}
		var err error
		switch k {
		case "repo":
			err = json.Unmarshal(raw, &tmp.Repo)
		case "revision":
			err = json.Unmarshal(raw, &tmp.Revision)
		case "path":
			err = json.Unmarshal(raw, &tmp.Path)
		case "size":
			err = json.Unmarshal(raw, &tmp.Size)
		case "sha256":
			err = json.Unmarshal(raw, &tmp.SHA256)
		case "role":
			err = json.Unmarshal(raw, &tmp.Role)
		}
		if err != nil {
			return err
		}
	}
	if err := tmp.Validate(); err != nil {
		return err
	}
	*f = tmp
	return nil
}

// ColabOneModelSpec is the data-only manifest describing a chat GGUF model.
type ColabOneModelSpec struct {
	ID            string              `json:"id"`
	APIID         string              `json:"api_id"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	MinVRAMMib    int                 `json:"min_vram_mib"`
	ContextLength int                 `json:"context_length"`
	Temperature   float64             `json:"temperature"`
	CPUMoELayers  int                 `json:"cpu_moe_layers"`
	Source        string              `json:"source"`
	Kind          string              `json:"kind"`
	Files         []ColabOneModelFile `json:"files"`
}

// NewColabOneModelSpec constructs a ColabOneModelSpec initialized with
// Pydantic-equivalent default field values (including Temperature = 0.7).
func NewColabOneModelSpec() ColabOneModelSpec {
	return ColabOneModelSpec{
		ContextLength: defaultContextLength,
		Temperature:   defaultTemperature,
		Source:        defaultSource,
		Kind:          defaultKind,
	}
}

// ApplyDefaults fills in zero-string/context defaults on a Go-constructed spec
// without clobbering a valid explicit Temperature of 0 (greedy decoding).
func (s *ColabOneModelSpec) ApplyDefaults() {
	if s == nil {
		return
	}
	if s.ContextLength == 0 {
		s.ContextLength = defaultContextLength
	}
	if s.Source == "" {
		s.Source = defaultSource
	}
	if s.Kind == "" {
		s.Kind = defaultKind
	}
	s.APIID = strings.TrimSpace(s.APIID)
	for i := range s.Files {
		s.Files[i].ApplyDefaults()
	}
}

// Clone returns a deep copy of the model spec (including its Files slice).
func (s ColabOneModelSpec) Clone() ColabOneModelSpec {
	out := s
	if s.Files != nil {
		out.Files = make([]ColabOneModelFile, len(s.Files))
		copy(out.Files, s.Files)
	}
	return out
}

// Validate enforces all ColabOneModelSpec field and cross-file constraints.
func (s *ColabOneModelSpec) Validate() error {
	if s == nil {
		return errors.New("模型配置不能为空")
	}
	if !modelSpecIDRe.MatchString(s.ID) {
		return errors.New("模型 ID 不合法")
	}
	cleanAPIID, err := ValidateAPIModelID(s.APIID, true)
	if err != nil {
		return err
	}
	s.APIID = cleanAPIID

	nameLen := utf8.RuneCountInString(s.Name)
	if nameLen < 1 || nameLen > 200 {
		return errors.New("模型名称长度需在 1 到 200 之间")
	}
	if s.MinVRAMMib < 0 {
		return errors.New("最小显存要求不能为负数")
	}
	if s.ContextLength < 512 || s.ContextLength > 262144 {
		return errors.New("上下文长度需在 512 到 262144 之间")
	}
	if math.IsNaN(s.Temperature) || math.IsInf(s.Temperature, 0) || s.Temperature < 0 || s.Temperature > 2 {
		return errors.New("采样温度需在 0 到 2 之间")
	}
	if s.CPUMoELayers < 0 || s.CPUMoELayers > 256 {
		return errors.New("CPU MoE 层数需在 0 到 256 之间")
	}
	if s.Kind != "chat" {
		return errors.New("模型类型仅支持 chat")
	}
	if len(s.Files) < 1 || len(s.Files) > 128 {
		return errors.New("模型文件数量需在 1 到 128 之间")
	}
	for i := range s.Files {
		if err := s.Files[i].Validate(); err != nil {
			return err
		}
	}
	modelCount := 0
	seenPaths := make(map[string]struct{}, len(s.Files))
	for _, f := range s.Files {
		if f.Role == "model" {
			modelCount++
		}
		seenPaths[f.Path] = struct{}{}
	}
	if modelCount != 1 {
		return errors.New("需要且只能有一个入口模型文件")
	}
	if len(seenPaths) != len(s.Files) {
		return errors.New("文件路径重复")
	}
	for _, f := range s.Files {
		if (f.Role != "model" && f.Role != "shard") || !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			return errors.New("聊天模型只能包含 GGUF 模型和分片")
		}
	}
	return nil
}

// UnmarshalJSON performs strict JSON decoding for ColabOneModelSpec, applying
// Pydantic-compatible defaults for omitted fields while rejecting unknown keys,
// non-nullable null values, and invalid constraints.
func (s *ColabOneModelSpec) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return errors.New("模型配置必须为 JSON 对象")
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &rawMap); err != nil {
		return err
	}
	for k := range rawMap {
		if _, ok := allowedModelSpecKeys[k]; !ok {
			return fmt.Errorf("模型配置包含不支持的字段: %s", k)
		}
	}
	tmp := ColabOneModelSpec{
		ContextLength: defaultContextLength,
		Temperature:   defaultTemperature,
		Source:        defaultSource,
		Kind:          defaultKind,
	}
	for k, raw := range rawMap {
		if k == "api_id" {
			if isJSONNull(raw) {
				tmp.APIID = ""
				continue
			}
			if err := json.Unmarshal(raw, &tmp.APIID); err != nil {
				return errors.New("模型 API ID 必须为字符串")
			}
			continue
		}
		if isJSONNull(raw) {
			return fmt.Errorf("模型配置字段 %s 不能为空值", k)
		}
		var err error
		switch k {
		case "id":
			err = json.Unmarshal(raw, &tmp.ID)
		case "name":
			err = json.Unmarshal(raw, &tmp.Name)
		case "description":
			err = json.Unmarshal(raw, &tmp.Description)
		case "min_vram_mib":
			err = json.Unmarshal(raw, &tmp.MinVRAMMib)
		case "context_length":
			err = json.Unmarshal(raw, &tmp.ContextLength)
		case "temperature":
			err = json.Unmarshal(raw, &tmp.Temperature)
		case "cpu_moe_layers":
			err = json.Unmarshal(raw, &tmp.CPUMoELayers)
		case "source":
			err = json.Unmarshal(raw, &tmp.Source)
		case "kind":
			err = json.Unmarshal(raw, &tmp.Kind)
		case "files":
			err = json.Unmarshal(raw, &tmp.Files)
		}
		if err != nil {
			return err
		}
	}
	if err := tmp.Validate(); err != nil {
		return err
	}
	*s = tmp
	return nil
}

// ParseModelSpec decodes and strictly validates a single ColabOneModelSpec JSON object.
func ParseModelSpec(data []byte) (*ColabOneModelSpec, error) {
	var spec ColabOneModelSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// ValidateAPIModelID validates and trims a public OpenAI API model identifier.
func ValidateAPIModelID(value string, allowEmpty bool) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		if allowEmpty {
			return "", nil
		}
		return "", errors.New("模型 API ID 不能为空")
	}
	if strings.ToLower(raw) == COLABONE_AUTO_API_MODEL_ID {
		return "", errors.New("colabone/auto 为系统保留别名，请使用其他模型 ID")
	}
	if len(raw) > 120 ||
		!colabOneAPIModelIDRe.MatchString(raw) ||
		strings.Contains(raw, "..") ||
		strings.Contains(raw, "//") ||
		hasDotPathSegment(raw) ||
		strings.HasSuffix(raw, "/") ||
		strings.HasSuffix(raw, ".") {
		return "", errors.New("模型 API ID 仅支持字母、数字、点、下划线、连字符和斜杠，且不能以斜杠或点结尾")
	}
	return raw, nil
}

// DefaultAPIModelID builds a clean, human-readable `colabone/<model>` API identifier.
func DefaultAPIModelID(repo, filename, name string) string {
	repoClean := strings.Trim(strings.TrimSpace(repo), "/")
	owner := ""
	rawSource := ""
	if idx := strings.IndexByte(repoClean, '/'); idx >= 0 {
		owner = repoClean[:idx]
		rawSource = repoClean[idx+1:]
	} else if repoClean != "" {
		rawSource = repoClean
	} else if trimmedName := strings.TrimSpace(name); trimmedName != "" {
		rawSource = trimmedName
	} else if trimmedFile := strings.TrimSpace(filename); trimmedFile != "" {
		rawSource = posixPathName(trimmedFile)
	}

	rawSource = ggufSuffixRe.ReplaceAllString(rawSource, "")
	// Remove composite quantization suffixes (e.g. UD-Q4_K_XL, Q8_0) before splitting underscores.
	strippedQuant := subEmbeddedQuant(rawSource)
	splitTokens := tokenSplitRe.Split(strings.ToLower(strippedQuant), -1)
	rawTokens := make([]string, 0, len(splitTokens))
	for _, tok := range splitTokens {
		cleaned := strings.Trim(tok, ".")
		if cleaned != "" {
			rawTokens = append(rawTokens, cleaned)
		}
	}

	ownerPrefixes := make(map[string]struct{}, len(knownPackagerPrefixes)+2)
	for k := range knownPackagerPrefixes {
		ownerPrefixes[k] = struct{}{}
	}
	if owner != "" {
		ownerLow := strings.ToLower(owner)
		ownerPrefixes[ownerLow] = struct{}{}
		firstOwnerSeg := ownerLowerSplitRe.Split(ownerLow, -1)[0]
		if firstOwnerSeg != "" {
			ownerPrefixes[firstOwnerSeg] = struct{}{}
		}
	}

	tokens := make([]string, len(rawTokens))
	copy(tokens, rawTokens)
	for len(tokens) > 1 {
		if _, ok := ownerPrefixes[tokens[0]]; !ok {
			break
		}
		tokens = tokens[1:]
	}

	kept := make([]string, 0, len(tokens))
	seenRepack := false
	for idx, tok := range tokens {
		if _, noise := formatNoiseTokens[tok]; noise || quantTokenRe.MatchString(tok) {
			continue
		}
		if _, repack := repackModifierTokens[tok]; repack {
			seenRepack = true
			continue
		}
		if _, chat := chatSuffixTokens[tok]; chat {
			continue
		}
		if idx > 0 && dateTagRe.MatchString(tok) {
			continue
		}
		if seenRepack && repackVersionRe.MatchString(tok) {
			continue
		}
		kept = append(kept, tok)
	}

	if len(kept) == 0 {
		for _, tok := range tokens {
			if _, noise := formatNoiseTokens[tok]; !noise && !quantTokenRe.MatchString(tok) {
				kept = append(kept, tok)
			}
		}
	}
	if len(kept) == 0 {
		kept = rawTokens
	}

	slug := sanitizeSlugPart(strings.Join(kept, "-"), 56)
	if slug == "" {
		fallback := rawSource
		if fallback == "" {
			fallback = name
		}
		if fallback == "" {
			fallback = "model"
		}
		slug = sanitizeSlugPart(fallback, 56)
		if slug == "" {
			slug = "model"
		}
	}
	if slug == "auto" {
		slug = "auto-model"
	}
	return COLABONE_API_MODEL_PREFIX + slug
}

// CompactQuantTag returns a concise lowercase quant suffix (e.g. q4, q8, bf16) for collision fallback.
func CompactQuantTag(filename string) string {
	quant := extractQuantization(filename)
	if quant == "" || quant == "GGUF" {
		return ""
	}
	if match := compactQuantRe.FindStringSubmatch(quant); len(match) > 1 {
		return strings.ToLower(match[1])
	}
	return sanitizeSlugPart(quant, 16)
}

// UniqueAPIModelID returns baseAPIID cleanly, appending a concise disambiguator only on collision.
func UniqueAPIModelID(baseAPIID string, existingIDs []string, repo, filename string) (string, error) {
	base, err := ValidateAPIModelID(baseAPIID, false)
	if err != nil {
		return "", err
	}
	taken := make(map[string]struct{}, len(existingIDs)+1)
	for _, item := range existingIDs {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			taken[strings.ToLower(trimmed)] = struct{}{}
		}
	}
	taken[COLABONE_AUTO_API_MODEL_ID] = struct{}{}
	if _, exists := taken[strings.ToLower(base)]; !exists {
		return base, nil
	}

	var candidates []string
	compactQ := CompactQuantTag(filename)
	if compactQ != "" {
		candidates = append(candidates, base+"-"+compactQ)
	}
	if filename != "" {
		fullQ := extractQuantization(filename)
		if fullQ != "" && fullQ != "GGUF" {
			fullQSlug := strings.Trim(fullQuantSlugRe.ReplaceAllString(strings.ToLower(fullQ), "-"), "-_")
			if fullQSlug != "" && fullQSlug != compactQ {
				candidates = append(candidates, base+"-"+fullQSlug)
			}
		}
	}
	if idx := strings.IndexByte(repo, '/'); idx >= 0 {
		firstSeg := ownerAlnumSplitRe.Split(repo[:idx], -1)[0]
		ownerSeg := sanitizeSlugPart(firstSeg, 16)
		if ownerSeg != "" && len(ownerSeg) >= 2 && !strings.Contains(strings.ToLower(base), ownerSeg) {
			candidates = append(candidates, base+"-"+ownerSeg)
		}
	}

	for _, cand := range candidates {
		if len(cand) > 120 {
			cand = cand[:120]
		}
		cand = strings.TrimRight(cand, "-.")
		if _, exists := taken[strings.ToLower(cand)]; !exists {
			if valid, verr := ValidateAPIModelID(cand, false); verr == nil {
				return valid, nil
			}
		}
	}

	for idx := 2; idx < 1000; idx++ {
		suffix := "-" + strconv.Itoa(idx)
		maxPrefix := 120 - len(suffix)
		prefix := base
		if len(prefix) > maxPrefix {
			prefix = prefix[:maxPrefix]
		}
		cand := strings.TrimRight(prefix, "-.") + suffix
		if _, exists := taken[strings.ToLower(cand)]; !exists {
			return cand, nil
		}
	}
	return base, nil
}

// ModelOwner extracts the Hugging Face owner prefix for a model spec, defaulting to "colabone".
func ModelOwner(spec ColabOneModelSpec) string {
	firstRepo := ""
	if len(spec.Files) > 0 {
		firstRepo = spec.Files[0].Repo
	}
	return ownerFromRepo(firstRepo)
}

// ModelOwnerFromMap extracts the Hugging Face owner prefix from a serialized model map, defaulting to "colabone".
func ModelOwnerFromMap(m map[string]any) string {
	if m == nil {
		return "colabone"
	}
	firstRepo, _ := firstFileRepoAndPathFromMap(m)
	if firstRepo == "" {
		firstRepo = stringFromAny(m["repo"])
	}
	return ownerFromRepo(firstRepo)
}

// ResolveAPIModelID resolves the public OpenAI API model ID for a model spec.
func ResolveAPIModelID(spec ColabOneModelSpec) string {
	if explicit := strings.TrimSpace(spec.APIID); explicit != "" {
		return explicit
	}
	internalID := strings.TrimSpace(spec.ID)
	if strings.HasPrefix(internalID, "hf-") {
		firstRepo := ""
		firstPath := ""
		if len(spec.Files) > 0 {
			firstRepo = spec.Files[0].Repo
			firstPath = spec.Files[0].Path
		}
		return DefaultAPIModelID(firstRepo, firstPath, spec.Name)
	}
	return internalID
}

// ResolveAPIModelIDFromMap resolves the public OpenAI API model ID from a serialized model map.
func ResolveAPIModelIDFromMap(m map[string]any) string {
	if m == nil {
		return ""
	}
	if explicit := strings.TrimSpace(stringFromAny(m["api_id"])); explicit != "" {
		return explicit
	}
	internalID := strings.TrimSpace(stringFromAny(m["id"]))
	if strings.HasPrefix(internalID, "hf-") {
		firstRepo, firstPath := firstFileRepoAndPathFromMap(m)
		if firstRepo == "" {
			firstRepo = stringFromAny(m["repo"])
		}
		name := stringFromAny(m["name"])
		return DefaultAPIModelID(firstRepo, firstPath, name)
	}
	return internalID
}

// ParseHFInput normalizes a Hugging Face owner/repo or blob/resolve/tree URL into (repo, filename, revision).
func ParseHFInput(repoOrURL, filename, revision string) (string, string, string) {
	raw := strings.TrimSpace(repoOrURL)
	fn := strings.TrimSpace(filename)
	rev := strings.TrimSpace(revision)
	if rev == "" {
		rev = "main"
	}
	if match := hfURLRe.FindStringSubmatch(raw); len(match) == 4 {
		raw = match[1]
		if match[2] != "" && rev == "main" {
			rev = match[2]
		}
		if match[3] != "" && fn == "" {
			candidate := percentUnescape(match[3])
			if strings.HasSuffix(strings.ToLower(candidate), ".gguf") {
				fn = candidate
			}
		}
	}
	return strings.Trim(raw, "/"), fn, rev
}

// ColabOneResolveImportOptions configures ResolveImport metadata fetching and hooks.
type ColabOneResolveImportOptions struct {
	Repo       string
	Filename   string
	Revision   string
	Token      string
	CustomName string

	// HTTPClient overrides the HTTP client used to query Hugging Face metadata.
	HTTPClient *http.Client
	// BaseURL overrides "https://huggingface.co" (primarily for offline tests).
	BaseURL string

	// Optional pure helper overrides (defaulting to built-in common/hub.py-compatible implementations).
	ExtractQuantization func(path string) string
	ClassifyModality    func(pipelineTag string, tags []string, repo string) string
	IsAuxiliaryGGUF     func(path string) bool
}

// String redacts Token so formatting or logging import options never leaks Hugging Face tokens.
func (o ColabOneResolveImportOptions) String() string {
	token := ""
	if o.Token != "" {
		token = "[REDACTED]"
	}
	return fmt.Sprintf(
		"ColabOneResolveImportOptions{Repo:%q, Filename:%q, Revision:%q, Token:%q, CustomName:%q, BaseURL:%q, HasHTTPClient:%t}",
		o.Repo,
		o.Filename,
		o.Revision,
		token,
		o.CustomName,
		o.BaseURL,
		o.HTTPClient != nil,
	)
}

// GoString redacts Token when formatted with %#v.
func (o ColabOneResolveImportOptions) GoString() string {
	return o.String()
}

func newDefaultHFHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       defaultHTTPTimeout,
		CheckRedirect: colabOneHFCheckRedirect,
	}
}

func colabOneHFCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxHFRedirects {
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

// ResolveImport resolves Hugging Face GGUF metadata into a pinned ColabOneModelSpec.
// The desktop client only reads metadata and never downloads model weights.
func ResolveImport(ctx context.Context, opts ColabOneResolveImportOptions) (*ColabOneModelSpec, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	repo, filename, revision := ParseHFInput(opts.Repo, opts.Filename, opts.Revision)
	if !isValidHFRepo(repo) {
		return nil, errors.New("请输入 owner/repository 或 Hugging Face 仓库链接")
	}
	if filename == "" {
		return nil, errors.New("请选择或输入 GGUF 模型文件")
	}
	if !hfRevisionRe.MatchString(revision) || strings.Contains(revision, "..") || revision == "." {
		return nil, errors.New("revision 不合法")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultHFBaseURL
	}
	endpoint := fmt.Sprintf("%s/api/models/%s/revision/%s?blobs=true", baseURL, repo, revision)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}

	client := opts.HTTPClient
	if client == nil {
		client = defaultHFHTTPClient
	} else if client.CheckRedirect == nil {
		clone := *client
		clone.CheckRedirect = colabOneHFCheckRedirect
		client = &clone
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &ColabOneHTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
		}
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxHFResponseBytes))
	if err != nil {
		return nil, err
	}
	info, err := decodeJSONObjectWithNumbers(bodyBytes)
	if err != nil {
		return nil, errors.New("模型社区返回了无效结果")
	}

	extractQuantFn := opts.ExtractQuantization
	if extractQuantFn == nil {
		extractQuantFn = extractQuantization
	}
	classifyModalityFn := opts.ClassifyModality
	if classifyModalityFn == nil {
		classifyModalityFn = classifyModality
	}
	isAuxFn := opts.IsAuxiliaryGGUF
	if isAuxFn == nil {
		isAuxFn = isAuxiliaryGGUF
	}

	if isAuxFn(filename) {
		return nil, errors.New("该文件是量化校准或辅助文件，不是可直接装载的模型权重")
	}

	pipelineTag := stringFromAny(info["pipeline_tag"])
	var tags []string
	if rawTags, ok := info["tags"].([]any); ok {
		tags = make([]string, 0, len(rawTags))
		for _, item := range rawTags {
			if s, ok := item.(string); ok {
				tags = append(tags, s)
			}
		}
	}
	modality := classifyModalityFn(pipelineTag, tags, repo)
	if modality == "image" || modality == "video" {
		return nil, errors.New("该模型属于图片/视频生成类（ComfyUI 专用），无法在「对外 API」文本引擎中装载，请前往 ComfyUI 工作区使用")
	}

	siblings := make(map[string]map[string]any)
	if rawSiblings, ok := info["siblings"].([]any); ok {
		for _, item := range rawSiblings {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			rfilename, ok := entry["rfilename"].(string)
			if !ok {
				continue
			}
			siblings[rfilename] = entry
		}
	}

	selected := []string{filename}
	if match := shardFilenameRe.FindStringSubmatch(filename); len(match) == 3 {
		count, convErr := strconv.Atoi(match[2])
		if convErr != nil || count < 1 || count > 128 {
			return nil, errors.New("分片数量需在 1 到 128 之间")
		}
		suffix := filename[len(filename)-5:]
		selected = make([]string, 0, count)
		for i := 1; i <= count; i++ {
			selected = append(selected, fmt.Sprintf("%s-%05d-of-%05d%s", match[1], i, count, suffix))
		}
	}

	commitSHA := stringFromAny(info["sha"])
	files := make([]ColabOneModelFile, 0, len(selected))
	for _, name := range selected {
		item := siblings[name]
		var lfs map[string]any
		if item != nil {
			if rawLFS, ok := item["lfs"].(map[string]any); ok {
				lfs = rawLFS
			}
		}
		var size int64
		var sha string
		if lfs != nil {
			if num, ok := lfs["size"].(json.Number); ok {
				if parsed, nerr := num.Int64(); nerr == nil {
					size = parsed
				}
			}
			if s, ok := lfs["sha256"].(string); ok {
				sha = s
			}
		}
		role := "shard"
		if len(files) == 0 {
			role = "model"
		}
		mf := ColabOneModelFile{
			Repo:     repo,
			Revision: commitSHA,
			Path:     name,
			Size:     size,
			SHA256:   sha,
			Role:     role,
		}
		if err := mf.Validate(); err != nil {
			return nil, err
		}
		files = append(files, mf)
	}

	sum := sha256.Sum256([]byte(fmt.Sprintf("%s@%s:%s", repo, commitSHA, selected[0])))
	identity := hex.EncodeToString(sum[:])[:16]

	displayName := truncateRunes(strings.TrimSpace(opts.CustomName), 200)
	if displayName == "" {
		repoParts := strings.Split(repo, "/")
		displayName = repoParts[len(repoParts)-1]
	}

	quant := extractQuantFn(selected[0])
	description := selected[0]
	if quant != "" && quant != "GGUF" {
		description = quant + " · " + selected[0]
	}
	defaultAPIID := DefaultAPIModelID(repo, selected[0], displayName)

	spec := &ColabOneModelSpec{
		ID:            "hf-" + identity,
		APIID:         defaultAPIID,
		Name:          displayName,
		Description:   description,
		MinVRAMMib:    0,
		ContextLength: defaultContextLength,
		Temperature:   defaultTemperature,
		CPUMoELayers:  0,
		Source:        defaultSource,
		Kind:          defaultKind,
		Files:         files,
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return spec, nil
}

// ---------------------------------------------------------------------------
// Private pure helper copies matching common/hub.py
// ---------------------------------------------------------------------------

func extractQuantization(p string) string {
	if p == "" {
		return "GGUF"
	}
	rawParts := strings.Split(p, "/")
	parts := make([]string, 0, len(rawParts))
	for _, seg := range rawParts {
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	if len(parts) == 0 {
		return "GGUF"
	}
	base := ggufSuffixRe.ReplaceAllString(parts[len(parts)-1], "")
	candidates := []string{base}
	if len(parts) > 1 {
		candidates = append(candidates, strings.Join(parts[:len(parts)-1], "/"))
	}
	for _, text := range candidates {
		for _, pattern := range hubQuantAnchoredPatterns {
			matches := findAllAlnumBoundedMatches(pattern, text)
			if len(matches) > 0 {
				return strings.ToUpper(matches[len(matches)-1])
			}
		}
	}
	return "GGUF"
}

func isAuxiliaryGGUF(p string) bool {
	base := p
	if idx := strings.LastIndexByte(p, '/'); idx >= 0 {
		base = p[idx+1:]
	}
	base = strings.ToLower(base)
	if auxMMProjRe.MatchString(base) {
		return true
	}
	if auxIMatrixHead.MatchString(base) {
		return true
	}
	if auxIMatrixTail.MatchString(base) {
		stem := strings.TrimSuffix(base, ".gguf")
		if extractQuantization(stem) == "GGUF" {
			return true
		}
	}
	if auxVocabHead.MatchString(base) {
		return true
	}
	if auxLoraTail.MatchString(base) {
		return true
	}
	return false
}

func classifyModality(pipelineTag string, tags []string, repo string) string {
	return hub.ClassifyModality(pipelineTag, tags, repo)
}

// ---------------------------------------------------------------------------
// Internal utilities
// ---------------------------------------------------------------------------

func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// findAllAlnumBoundedMatches replicates Python's (?<![A-Za-z0-9])(BODY)(?![A-Za-z0-9])
// without consuming the trailing boundary character so adjacent matches both trigger.
func findAllAlnumBoundedMatches(anchoredRe *regexp.Regexp, text string) []string {
	var matches []string
	for i := 0; i < len(text); {
		if i > 0 && isASCIIAlnum(text[i-1]) {
			i++
			continue
		}
		loc := anchoredRe.FindStringSubmatchIndex(text[i:])
		if loc == nil {
			i++
			continue
		}
		mStart, mEnd := i+loc[2], i+loc[3]
		matches = append(matches, text[mStart:mEnd])
		if mEnd > i {
			i = mEnd
		} else {
			i++
		}
	}
	return matches
}

// subEmbeddedQuant replicates Python's:
// _EMBEDDED_QUANT_RE = re.compile(r"(?:^|[-_\s])(?:ud[-_])?(?:i|t)?q[1-8](?:_[0-9a-z]+)*(?=$|[-_\s])", re.IGNORECASE)
// _EMBEDDED_QUANT_RE.sub("-", s)
func subEmbeddedQuant(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if i == 0 {
			if loc := embeddedQuantBody.FindStringSubmatchIndex(s); loc != nil && loc[3] > 0 {
				b.WriteByte('-')
				i = loc[3]
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '-' || r == '_' || unicode.IsSpace(r) {
			next := i + size
			if next < len(s) {
				if loc := embeddedQuantBody.FindStringSubmatchIndex(s[next:]); loc != nil && loc[3] > 0 {
					b.WriteByte('-')
					i = next + loc[3]
					continue
				}
			}
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

func sanitizeSlugPart(text string, maxLen int) string {
	cleaned := nonSlugCharsRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(text)), "-")
	cleaned = multiDotsRe.ReplaceAllString(cleaned, ".")
	cleaned = strings.Trim(multiHyphensRe.ReplaceAllString(cleaned, "-"), "-.")
	if len(cleaned) > maxLen {
		cleaned = strings.TrimRight(cleaned[:maxLen], "-.")
	}
	return cleaned
}

func posixPathName(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return ""
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" {
		return ""
	}
	return path.Base(cleaned)
}

func isValidHFRepo(repo string) bool {
	if !hfRepoRe.MatchString(repo) {
		return false
	}
	for _, part := range strings.Split(repo, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func isValidRelativeGGUFPath(p string) bool {
	if p == "" ||
		strings.HasPrefix(p, "/") ||
		strings.Contains(p, ":") ||
		strings.Contains(p, "\\") ||
		strings.ContainsRune(p, '\x00') ||
		utf8.RuneCountInString(p) > 512 ||
		!strings.HasSuffix(strings.ToLower(p), ".gguf") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func hasDotPathSegment(raw string) bool {
	for _, seg := range strings.Split(raw, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

func ownerFromRepo(repo string) string {
	if idx := strings.IndexByte(repo, '/'); idx >= 0 {
		if owner := strings.TrimSpace(repo[:idx]); owner != "" {
			return owner
		}
	}
	return "colabone"
}

func firstFileRepoAndPathFromMap(m map[string]any) (string, string) {
	rawFiles, ok := m["files"]
	if !ok || rawFiles == nil {
		return "", ""
	}
	switch files := rawFiles.(type) {
	case []ColabOneModelFile:
		if len(files) > 0 {
			return files[0].Repo, files[0].Path
		}
	case []map[string]any:
		if len(files) > 0 && files[0] != nil {
			return stringFromAny(files[0]["repo"]), stringFromAny(files[0]["path"])
		}
	case []any:
		if len(files) == 0 {
			return "", ""
		}
		switch first := files[0].(type) {
		case map[string]any:
			return stringFromAny(first["repo"]), stringFromAny(first["path"])
		case ColabOneModelFile:
			return first.Repo, first.Path
		case *ColabOneModelFile:
			if first != nil {
				return first.Repo, first.Path
			}
		}
	}
	return "", ""
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case bool:
		if !val {
			return ""
		}
		return "true"
	case int:
		if val == 0 {
			return ""
		}
		return strconv.Itoa(val)
	case int64:
		if val == 0 {
			return ""
		}
		return strconv.FormatInt(val, 10)
	case float64:
		if val == 0 {
			return ""
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case json.Number:
		s := val.String()
		if s == "0" || s == "" {
			return ""
		}
		return s
	default:
		return fmt.Sprint(v)
	}
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func decodeJSONObjectWithNumbers(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	// Ensure there is no trailing non-whitespace garbage.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	obj, ok := raw.(map[string]any)
	if !ok || obj == nil {
		return nil, errors.New("expected JSON object")
	}
	return obj, nil
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// percentUnescape matches Python's urllib.parse.unquote: decodes valid %XX
// byte sequences without converting '+' to space, and preserves malformed '%'.
func percentUnescape(s string) string {
	if !strings.ContainsRune(s, '%') {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				out = append(out, hi<<4|lo)
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}
