// Package comfyui provides data-only ComfyUI workflow schemas, validation,
// field extraction, override application, and catalog models shared across ColabOne.
package comfyui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/luolongfei/ColabOne/console/internal/publicapi"
)

const (
	// MaxWorkflowBytes is the maximum serialized UTF-8 JSON size of a ComfyUI API workflow (2 MiB).
	MaxWorkflowBytes = 2 * 1024 * 1024
	// MaxOutputBytes is the maximum size of a single ComfyUI output artifact (512 MiB).
	MaxOutputBytes = 512 * 1024 * 1024
	// MaxImageInputBytes is the maximum size of an uploaded ComfyUI input image (16 MiB).
	MaxImageInputBytes = 16 * 1024 * 1024
	// MaxAudioInputBytes is the maximum size of an uploaded ComfyUI input audio file (64 MiB).
	MaxAudioInputBytes = 64 * 1024 * 1024

	maxWorkflowNodes = 500
	maxAutoFields    = 32
)

// ColabOneOutputMediaSpec describes the media kind and MIME type for a supported output extension.
type ColabOneOutputMediaSpec struct {
	Kind string
	MIME string
}

// AllowedOutputMIME maps allowed ComfyUI output extensions to their expected media kind and MIME type.
var AllowedOutputMIME = map[string]ColabOneOutputMediaSpec{
	"png":  {Kind: "image", MIME: "image/png"},
	"jpg":  {Kind: "image", MIME: "image/jpeg"},
	"jpeg": {Kind: "image", MIME: "image/jpeg"},
	"webp": {Kind: "image", MIME: "image/webp"},
	"gif":  {Kind: "image", MIME: "image/gif"},
	"mp4":  {Kind: "video", MIME: "video/mp4"},
	"webm": {Kind: "video", MIME: "video/webm"},
	"wav":  {Kind: "audio", MIME: "audio/wav"},
	"mp3":  {Kind: "audio", MIME: "audio/mpeg"},
	"flac": {Kind: "audio", MIME: "audio/flac"},
}

// AllowedInputExtensions enumerates supported input media file extensions.
var AllowedInputExtensions = map[string]bool{
	"png":  true,
	"jpg":  true,
	"jpeg": true,
	"webp": true,
	"wav":  true,
	"mp3":  true,
	"flac": true,
	"ogg":  true,
	"m4a":  true,
}

var defaultAllowedOutputMIME = map[string]ColabOneOutputMediaSpec{
	"png":  {Kind: "image", MIME: "image/png"},
	"jpg":  {Kind: "image", MIME: "image/jpeg"},
	"jpeg": {Kind: "image", MIME: "image/jpeg"},
	"webp": {Kind: "image", MIME: "image/webp"},
	"gif":  {Kind: "image", MIME: "image/gif"},
	"mp4":  {Kind: "video", MIME: "video/mp4"},
	"webm": {Kind: "video", MIME: "video/webm"},
	"wav":  {Kind: "audio", MIME: "audio/wav"},
	"mp3":  {Kind: "audio", MIME: "audio/mpeg"},
	"flac": {Kind: "audio", MIME: "audio/flac"},
}

var (
	nodeIDRe      = regexp.MustCompile(`\A[a-zA-Z0-9_:-]{1,80}\z`)
	workflowIDRe  = regexp.MustCompile(`\A[a-zA-Z0-9_-]{1,80}\z`)
	repoSlugRe    = regexp.MustCompile(`\A[0-9A-Za-z_.-]+/[0-9A-Za-z_.-]+\z`)
	githubRepoRe  = regexp.MustCompile(`\Ahttps://github\.com/([0-9A-Za-z_.-]+)/([0-9A-Za-z_.-]+?)(?:\.git)?/?\z`)
	assetIDRe     = regexp.MustCompile(`\A[a-f0-9]{32}\z`)
	pipPkgRe      = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._\-\[\],<>=!~ ]{0,199}\z`)
	commitSHARe   = regexp.MustCompile(`\A[a-fA-F0-9]{40}\z`)
	sha256AnyRe   = regexp.MustCompile(`\A[a-fA-F0-9]{64}\z`)
	sha256LowerRe = regexp.MustCompile(`\A[a-f0-9]{64}\z`)

	comfyModelExtensions = map[string]bool{
		".safetensors": true,
		".gguf":        true,
		".ckpt":        true,
		".pt":          true,
		".pth":         true,
		".bin":         true,
	}

	allowedModelTargetDirs = map[string]bool{
		"checkpoints":      true,
		"diffusion_models": true,
		"loras":            true,
		"vae":              true,
		"text_encoders":    true,
		"clip_vision":      true,
		"wav2vec2":         true,
		"controlnet":       true,
		"upscale_models":   true,
		"audio_encoders":   true,
	}

	allowedFieldTypes = map[string]bool{
		"text":     true,
		"textarea": true,
		"number":   true,
		"integer":  true,
		"boolean":  true,
		"select":   true,
		"image":    true,
		"audio":    true,
	}

	allowedOutputKinds = map[string]bool{
		"image": true,
		"video": true,
		"audio": true,
	}

	textInputNames = map[string]bool{
		"text":            true,
		"prompt":          true,
		"positive":        true,
		"negative":        true,
		"string":          true,
		"positive_prompt": true,
		"negative_prompt": true,
	}

	imageLoaderInputNames = map[string]bool{
		"image":      true,
		"image_path": true,
		"file":       true,
		"filename":   true,
	}

	audioLoaderInputNames = map[string]bool{
		"audio":      true,
		"audio_file": true,
		"audio_path": true,
		"file":       true,
		"filename":   true,
	}
)

type numericFieldSpec struct {
	fieldType string
	cnName    string
	priority  int
	minValue  *float64
	maxValue  *float64
	step      *float64
}

func floatPtr(v float64) *float64 {
	return &v
}

func copyFloatPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

var numericSpecs = map[string]numericFieldSpec{
	"width":       {fieldType: "integer", cnName: "宽度", priority: 10, minValue: floatPtr(64), maxValue: floatPtr(4096), step: floatPtr(8)},
	"height":      {fieldType: "integer", cnName: "高度", priority: 11, minValue: floatPtr(64), maxValue: floatPtr(4096), step: floatPtr(8)},
	"length":      {fieldType: "integer", cnName: "帧数", priority: 12, minValue: floatPtr(1)},
	"frame_count": {fieldType: "integer", cnName: "帧数", priority: 12, minValue: floatPtr(1)},
	"fps":         {fieldType: "number", cnName: "帧率 (FPS)", priority: 13, minValue: floatPtr(1), maxValue: floatPtr(60)},
	"batch_size":  {fieldType: "integer", cnName: "批次大小", priority: 14, minValue: floatPtr(1)},
	"steps":       {fieldType: "integer", cnName: "采样步数", priority: 20, minValue: floatPtr(1), maxValue: floatPtr(200)},
	"cfg":         {fieldType: "number", cnName: "CFG 引导系数", priority: 21, minValue: floatPtr(0), maxValue: floatPtr(30), step: floatPtr(0.1)},
	"denoise":     {fieldType: "number", cnName: "重绘幅度", priority: 22, minValue: floatPtr(0), maxValue: floatPtr(1), step: floatPtr(0.05)},
	"seed":        {fieldType: "integer", cnName: "随机种子", priority: 23, minValue: floatPtr(0)},
	"noise_seed":  {fieldType: "integer", cnName: "随机种子", priority: 23, minValue: floatPtr(0)},
}

// ---------------------------------------------------------------------------
// Private helpers (public domain / tunnel token / JSON / workflow utilities)
// ---------------------------------------------------------------------------

func decodeStrictJSON(data []byte, dst any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("JSON 不能为空值")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("JSON 包含多余内容")
	}
	return nil
}

// decodeStrictJSONObject enforces exact-case JSON field names (preventing Go's
// case-insensitive struct tag matching from bypassing strict schema checks) and
// rejects JSON null on every field not listed in nullableKeys, before delegating
// to decodeStrictJSON.
func decodeStrictJSONObject(data []byte, dst any, nullableKeys ...string) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("JSON 不能为空值")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err == nil && raw != nil {
		allowed := make(map[string]bool)
		t := reflect.TypeOf(dst)
		if t != nil && t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t != nil && t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				tag := t.Field(i).Tag.Get("json")
				if tag == "" || tag == "-" {
					continue
				}
				name, _, _ := strings.Cut(tag, ",")
				if name != "" {
					allowed[name] = true
				}
			}
		}
		nullable := make(map[string]bool, len(nullableKeys))
		for _, k := range nullableKeys {
			nullable[k] = true
		}
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(allowed) > 0 && !allowed[k] {
				return fmt.Errorf("未知字段: %s", k)
			}
			if !nullable[k] && bytes.Equal(bytes.TrimSpace(raw[k]), []byte("null")) {
				return fmt.Errorf("字段 %s 不能为空值", k)
			}
		}
	}
	return decodeStrictJSON(trimmed, dst)
}

func validateWorkflowNumbers(v any) error {
	switch x := v.(type) {
	case nil, bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return nil
	case float32:
		if !isFiniteFloat64(float64(x)) {
			return errors.New("non-finite float32")
		}
		return nil
	case float64:
		if !isFiniteFloat64(x) {
			return errors.New("non-finite float64")
		}
		return nil
	case json.Number:
		s := string(x)
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return nil
		}
		if _, err := strconv.ParseUint(s, 10, 64); err == nil {
			return nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || !isFiniteFloat64(f) {
			return errors.New("non-finite json.Number")
		}
		return nil
	case map[string]any:
		for _, item := range x {
			if err := validateWorkflowNumbers(item); err != nil {
				return err
			}
		}
		return nil
	case map[string]map[string]any:
		for _, item := range x {
			if err := validateWorkflowNumbers(item); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for _, item := range x {
			if err := validateWorkflowNumbers(item); err != nil {
				return err
			}
		}
		return nil
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			for i := 0; i < rv.Len(); i++ {
				if err := validateWorkflowNumbers(rv.Index(i).Interface()); err != nil {
					return err
				}
			}
		case reflect.Map:
			iter := rv.MapRange()
			for iter.Next() {
				if err := validateWorkflowNumbers(iter.Value().Interface()); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func validateOwnerRepo(value, message string) (string, error) {
	if message == "" {
		message = "需要 owner/repository"
	}
	v := strings.TrimSpace(value)
	if !repoSlugRe.MatchString(v) {
		return "", errors.New(message)
	}
	for _, part := range strings.Split(v, "/") {
		if part == "." || part == ".." {
			return "", errors.New(message)
		}
	}
	return v, nil
}

func validateGitHubOrSlug(value string) (string, error) {
	v := strings.TrimSpace(value)
	if m := githubRepoRe.FindStringSubmatch(v); m != nil {
		owner, repo := m[1], m[2]
		if owner == "." || owner == ".." || repo == "." || repo == ".." {
			return "", errors.New("GitHub 仓库地址不合法")
		}
		return v, nil
	}
	return validateOwnerRepo(v, "需要 https://github.com/owner/repo 或 owner/repo")
}

func validateFieldKey(key string) (string, string, error) {
	if key == "" || utf8.RuneCountInString(key) > 160 || !strings.Contains(key, ".") {
		return "", "", fmt.Errorf("非法字段键: %q", key)
	}
	nodeID, inputName, _ := strings.Cut(key, ".")
	if !nodeIDRe.MatchString(nodeID) || inputName == "" || utf8.RuneCountInString(inputName) > 80 {
		return "", "", fmt.Errorf("非法字段键: %q", key)
	}
	return nodeID, inputName, nil
}

func isFiniteFloat64(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func isIntValue(v any) bool {
	switch x := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float64:
		return isFiniteFloat64(x) && math.Trunc(x) == x
	case float32:
		f := float64(x)
		return isFiniteFloat64(f) && math.Trunc(f) == f
	case json.Number:
		if _, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return true
		}
		_, err := strconv.ParseUint(string(x), 10, 64)
		return err == nil
	default:
		return false
	}
}

func extractNumericValue(v any) (exactInt any, hasExactInt bool, floatVal float64, ok bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true, float64(x), true
	case int8:
		return int64(x), true, float64(x), true
	case int16:
		return int64(x), true, float64(x), true
	case int32:
		return int64(x), true, float64(x), true
	case int64:
		return x, true, float64(x), true
	case uint:
		if uint64(x) <= math.MaxInt64 {
			return int64(x), true, float64(x), true
		}
		return uint64(x), true, float64(x), true
	case uint8:
		return int64(x), true, float64(x), true
	case uint16:
		return int64(x), true, float64(x), true
	case uint32:
		return int64(x), true, float64(x), true
	case uint64:
		if x <= math.MaxInt64 {
			return int64(x), true, float64(x), true
		}
		return x, true, float64(x), true
	case float32:
		f := float64(x)
		if !isFiniteFloat64(f) {
			return nil, false, 0, false
		}
		return nil, false, f, true
	case float64:
		if !isFiniteFloat64(x) {
			return nil, false, 0, false
		}
		return nil, false, x, true
	case json.Number:
		if n, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return n, true, float64(n), true
		}
		if u, err := strconv.ParseUint(string(x), 10, 64); err == nil {
			return u, true, float64(u), true
		}
		if f, err := strconv.ParseFloat(string(x), 64); err == nil && isFiniteFloat64(f) {
			return nil, false, f, true
		}
		return nil, false, 0, false
	default:
		return nil, false, 0, false
	}
}

func isNodeLink(value any) bool {
	if value == nil {
		return false
	}
	switch items := value.(type) {
	case []any:
		if len(items) != 2 {
			return false
		}
		firstOK := false
		switch items[0].(type) {
		case string:
			firstOK = true
		default:
			firstOK = isIntValue(items[0])
		}
		return firstOK && isIntValue(items[1])
	case []int:
		return len(items) == 2
	case []int64:
		return len(items) == 2
	case []float64:
		return len(items) == 2 && isIntValue(items[0]) && isIntValue(items[1])
	default:
		return false
	}
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func compareASCIINumericStrings(a, b string) int {
	sa := strings.TrimLeft(a, "0")
	sb := strings.TrimLeft(b, "0")
	if len(sa) != len(sb) {
		if len(sa) < len(sb) {
			return -1
		}
		return 1
	}
	if sa != sb {
		if sa < sb {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func compareNodeID(a, b string) int {
	aNum := isASCIIDigits(a)
	bNum := isASCIIDigits(b)
	if aNum != bNum {
		if aNum {
			return -1
		}
		return 1
	}
	if aNum {
		return compareASCIINumericStrings(a, b)
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func nodeTitle(node map[string]any, classType string) string {
	if meta, ok := node["_meta"].(map[string]any); ok && meta != nil {
		if rawTitle, ok := meta["title"].(string); ok {
			if trimmed := strings.TrimSpace(rawTitle); trimmed != "" {
				return trimmed
			}
		}
	}
	return classType
}

func trimLabel(text string) string {
	runes := []rune(text)
	if len(runes) <= 160 {
		return text
	}
	return string(runes[:160])
}

func promptLabelAndPriority(title, classTypeLower, inputNameLower string) (string, int) {
	titleLower := strings.ToLower(title)
	if strings.Contains(titleLower, "negative") ||
		strings.Contains(title, "反向") ||
		strings.Contains(title, "负向") ||
		strings.Contains(inputNameLower, "negative") {
		return trimLabel(title + " · 反向提示词"), 4
	}
	if strings.Contains(titleLower, "positive") ||
		strings.Contains(title, "正向") ||
		strings.Contains(inputNameLower, "positive") ||
		strings.Contains(titleLower, "prompt") ||
		strings.Contains(inputNameLower, "prompt") ||
		strings.Contains(classTypeLower, "encode") {
		return trimLabel(title + " · 正向提示词"), 2
	}
	return trimLabel(title + " · 文本输入"), 3
}

func isSliceValue(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() != reflect.Uint8
}

func deepCopyAny(v any) any {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case map[string]any:
		return deepCopyMap(x)
	case map[string]map[string]any:
		if x == nil {
			return nil
		}
		out := make(map[string]map[string]any, len(x))
		for k, val := range x {
			out[k] = deepCopyMap(val)
		}
		return out
	case []any:
		if x == nil {
			return nil
		}
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = deepCopyAny(item)
		}
		return out
	case []string:
		if x == nil {
			return nil
		}
		out := make([]string, len(x))
		copy(out, x)
		return out
	case []int:
		if x == nil {
			return nil
		}
		out := make([]int, len(x))
		copy(out, x)
		return out
	case []float64:
		if x == nil {
			return nil
		}
		out := make([]float64, len(x))
		copy(out, x)
		return out
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice:
			if rv.IsNil() {
				return v
			}
			out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
			for i := 0; i < rv.Len(); i++ {
				copied := deepCopyAny(rv.Index(i).Interface())
				if copied != nil {
					cv := reflect.ValueOf(copied)
					if cv.Type().AssignableTo(rv.Type().Elem()) {
						out.Index(i).Set(cv)
						continue
					}
				}
				out.Index(i).Set(rv.Index(i))
			}
			return out.Interface()
		case reflect.Map:
			if rv.IsNil() {
				return v
			}
			out := reflect.MakeMapWithSize(rv.Type(), rv.Len())
			iter := rv.MapRange()
			for iter.Next() {
				copied := deepCopyAny(iter.Value().Interface())
				if copied != nil {
					cv := reflect.ValueOf(copied)
					if cv.Type().AssignableTo(rv.Type().Elem()) {
						out.SetMapIndex(iter.Key(), cv)
						continue
					}
				}
				out.SetMapIndex(iter.Key(), iter.Value())
			}
			return out.Interface()
		default:
			return v
		}
	}
}

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyAny(v)
	}
	return out
}

func normalizedNodesToAnyMap(nodes map[string]map[string]any) map[string]any {
	out := make(map[string]any, len(nodes))
	for k, v := range nodes {
		out[k] = v
	}
	return out
}

func encodeWorkflowJSON(workflow map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(workflow); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// ---------------------------------------------------------------------------
// Exported domain schemas
// ---------------------------------------------------------------------------

// ColabOneComfyNodeSpec declares a pinned ComfyUI custom node repository.
type ColabOneComfyNodeSpec struct {
	Name                string `json:"name"`
	Repo                string `json:"repo"`
	Commit              string `json:"commit"`
	InstallRequirements bool   `json:"install_requirements"`
}

// UnmarshalJSON decodes a ColabOneComfyNodeSpec with extra fields forbidden and
// install_requirements defaulting to true when omitted.
func (s *ColabOneComfyNodeSpec) UnmarshalJSON(data []byte) error {
	type alias ColabOneComfyNodeSpec
	aux := alias{InstallRequirements: true}
	if err := decodeStrictJSONObject(data, &aux); err != nil {
		return err
	}
	*s = ColabOneComfyNodeSpec(aux)
	return nil
}

// Validate normalizes and validates a ColabOneComfyNodeSpec.
func (s *ColabOneComfyNodeSpec) Validate() error {
	if utf8.RuneCountInString(s.Name) > 120 {
		return errors.New("自定义节点名称不合法")
	}
	name := strings.TrimSpace(s.Name)
	if name == "" ||
		name == "." ||
		name == ".." ||
		strings.Contains(name, "..") ||
		strings.ContainsAny(name, `/\:`+"\x00") ||
		utf8.RuneCountInString(name) > 120 {
		return errors.New("自定义节点名称不合法")
	}
	s.Name = name

	if utf8.RuneCountInString(s.Repo) > 300 {
		return errors.New("GitHub 仓库地址不合法")
	}
	repo, err := validateGitHubOrSlug(s.Repo)
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(repo) > 300 {
		return errors.New("GitHub 仓库地址不合法")
	}
	s.Repo = repo

	if !commitSHARe.MatchString(s.Commit) {
		return errors.New("commit 必须为 40 位十六进制 SHA")
	}
	return nil
}

// ColabOneComfyModelRef declares a pinned Hugging Face model file required by a workflow.
type ColabOneComfyModelRef struct {
	Repo      string `json:"repo"`
	Revision  string `json:"revision"`
	Path      string `json:"path"`
	TargetDir string `json:"target_dir"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

// UnmarshalJSON decodes a ColabOneComfyModelRef with unknown fields forbidden.
func (m *ColabOneComfyModelRef) UnmarshalJSON(data []byte) error {
	type alias ColabOneComfyModelRef
	var aux alias
	if err := decodeStrictJSONObject(data, &aux); err != nil {
		return err
	}
	*m = ColabOneComfyModelRef(aux)
	return nil
}

// Validate normalizes and validates a ColabOneComfyModelRef.
func (m *ColabOneComfyModelRef) Validate() error {
	repo, err := validateOwnerRepo(m.Repo, "需要 Hugging Face 的 owner/repository")
	if err != nil {
		return err
	}
	m.Repo = repo

	if !commitSHARe.MatchString(m.Revision) {
		return errors.New("revision 必须为 40 位十六进制 SHA")
	}

	if err := validateModelFilePath(m.Path); err != nil {
		return err
	}

	if !allowedModelTargetDirs[m.TargetDir] {
		return errors.New("模型目标目录不合法")
	}

	if m.Size <= 0 {
		return errors.New("模型文件大小必须大于 0")
	}

	if !sha256AnyRe.MatchString(m.SHA256) {
		return errors.New("模型文件 sha256 不合法")
	}
	m.SHA256 = strings.ToLower(m.SHA256)
	return nil
}

func validateModelFilePath(value string) error {
	if value == "" ||
		strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") ||
		strings.ContainsAny(value, `:\`+"\x00") ||
		utf8.RuneCountInString(value) > 512 {
		return errors.New("模型文件路径不合法")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." {
			return errors.New("模型文件路径不合法")
		}
	}
	base := path.Base(value)
	ext := strings.ToLower(path.Ext(base))
	if !comfyModelExtensions[ext] || len(base) <= len(ext) {
		return errors.New("模型文件路径不合法")
	}
	return nil
}

// ColabOneWorkflowField describes an editable parameter extracted from or declared on a workflow.
type ColabOneWorkflowField struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	NodeID    string   `json:"node_id"`
	InputName string   `json:"input_name"`
	FieldType string   `json:"field_type"`
	Default   any      `json:"default"`
	Options   []string `json:"options"`
	MinValue  *float64 `json:"min_value"`
	MaxValue  *float64 `json:"max_value"`
	Step      *float64 `json:"step"`
}

// UnmarshalJSON decodes a ColabOneWorkflowField with unknown fields forbidden.
func (f *ColabOneWorkflowField) UnmarshalJSON(data []byte) error {
	var raw struct {
		Key       string    `json:"key"`
		Label     string    `json:"label"`
		NodeID    string    `json:"node_id"`
		InputName string    `json:"input_name"`
		FieldType string    `json:"field_type"`
		Default   any       `json:"default"`
		Options   []*string `json:"options"`
		MinValue  *float64  `json:"min_value"`
		MaxValue  *float64  `json:"max_value"`
		Step      *float64  `json:"step"`
	}
	if err := decodeStrictJSONObject(data, &raw, "default", "min_value", "max_value", "step"); err != nil {
		return err
	}
	options := make([]string, 0, len(raw.Options))
	for _, opt := range raw.Options {
		if opt == nil {
			return errors.New("字段 options 不能为空值")
		}
		options = append(options, *opt)
	}
	*f = ColabOneWorkflowField{
		Key:       raw.Key,
		Label:     raw.Label,
		NodeID:    raw.NodeID,
		InputName: raw.InputName,
		FieldType: raw.FieldType,
		Default:   raw.Default,
		Options:   options,
		MinValue:  raw.MinValue,
		MaxValue:  raw.MaxValue,
		Step:      raw.Step,
	}
	return nil
}

// Validate normalizes and validates a ColabOneWorkflowField.
func (f *ColabOneWorkflowField) Validate() error {
	if n := utf8.RuneCountInString(f.Key); n < 1 || n > 160 {
		return fmt.Errorf("非法字段键: %q", f.Key)
	}
	if n := utf8.RuneCountInString(f.Label); n < 1 || n > 160 {
		return errors.New("字段标签长度不合法")
	}
	if n := utf8.RuneCountInString(f.NodeID); n < 1 || n > 80 {
		return fmt.Errorf("非法节点 ID: %q", f.NodeID)
	}
	if n := utf8.RuneCountInString(f.InputName); n < 1 || n > 80 {
		return fmt.Errorf("非法字段键: %q", f.Key)
	}
	if !allowedFieldTypes[f.FieldType] {
		return errors.New("字段类型不合法")
	}
	if f.Default != nil {
		switch v := f.Default.(type) {
		case string, bool:
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		case float32:
			if !isFiniteFloat64(float64(v)) {
				return fmt.Errorf("字段 %s 包含非法浮点值", f.Key)
			}
		case float64:
			if !isFiniteFloat64(v) {
				return fmt.Errorf("字段 %s 包含非法浮点值", f.Key)
			}
		case json.Number:
			if _, err := strconv.ParseInt(string(v), 10, 64); err != nil {
				if _, uerr := strconv.ParseUint(string(v), 10, 64); uerr != nil {
					fv, ferr := strconv.ParseFloat(string(v), 64)
					if ferr != nil || !isFiniteFloat64(fv) {
						return fmt.Errorf("字段 %s 包含非法浮点值", f.Key)
					}
				}
			}
		default:
			return errors.New("字段默认值类型不合法")
		}
	}
	if f.MinValue != nil && !isFiniteFloat64(*f.MinValue) {
		return fmt.Errorf("字段 %s 包含非法浮点值", f.Key)
	}
	if f.MaxValue != nil && !isFiniteFloat64(*f.MaxValue) {
		return fmt.Errorf("字段 %s 包含非法浮点值", f.Key)
	}
	if f.Step != nil && !isFiniteFloat64(*f.Step) {
		return fmt.Errorf("字段 %s 包含非法浮点值", f.Key)
	}
	if f.Key != f.NodeID+"."+f.InputName {
		return errors.New("字段 key 必须为 {node_id}.{input_name}")
	}
	if f.MinValue != nil && f.MaxValue != nil && *f.MinValue > *f.MaxValue {
		return errors.New("min_value 不能大于 max_value")
	}
	if f.Options == nil {
		f.Options = []string{}
	}
	return nil
}

// ColabOneWorkflowTemplate represents a built-in or user-imported ComfyUI workflow template.
type ColabOneWorkflowTemplate struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Source      string                  `json:"source"`
	MinVRAMMib  int                     `json:"min_vram_mib"`
	CreatedAt   float64                 `json:"created_at"`
	UpdatedAt   float64                 `json:"updated_at"`
	CustomNodes []ColabOneComfyNodeSpec `json:"custom_nodes"`
	PipPackages []string                `json:"pip_packages"`
	Models      []ColabOneComfyModelRef `json:"models"`
	Workflow    map[string]any          `json:"workflow"`
	Fields      []ColabOneWorkflowField `json:"fields"`
	NodeCount   int                     `json:"node_count"`
}

// UnmarshalJSON decodes a ColabOneWorkflowTemplate with unknown fields forbidden.
func (t *ColabOneWorkflowTemplate) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID          string                  `json:"id"`
		Name        string                  `json:"name"`
		Description string                  `json:"description"`
		Source      *string                 `json:"source"`
		MinVRAMMib  int                     `json:"min_vram_mib"`
		CreatedAt   float64                 `json:"created_at"`
		UpdatedAt   float64                 `json:"updated_at"`
		CustomNodes []ColabOneComfyNodeSpec `json:"custom_nodes"`
		PipPackages []string                `json:"pip_packages"`
		Models      []ColabOneComfyModelRef `json:"models"`
		Workflow    map[string]any          `json:"workflow"`
		Fields      []ColabOneWorkflowField `json:"fields"`
		NodeCount   int                     `json:"node_count"`
	}
	if err := decodeStrictJSONObject(data, &raw); err != nil {
		return err
	}
	source := "custom"
	if raw.Source != nil {
		if *raw.Source == "" {
			return errors.New("工作流来源不合法")
		}
		source = *raw.Source
	}
	*t = ColabOneWorkflowTemplate{
		ID:          raw.ID,
		Name:        raw.Name,
		Description: raw.Description,
		Source:      source,
		MinVRAMMib:  raw.MinVRAMMib,
		CreatedAt:   raw.CreatedAt,
		UpdatedAt:   raw.UpdatedAt,
		CustomNodes: raw.CustomNodes,
		PipPackages: raw.PipPackages,
		Models:      raw.Models,
		Workflow:    raw.Workflow,
		Fields:      raw.Fields,
		NodeCount:   raw.NodeCount,
	}
	return nil
}

// Validate normalizes and validates a ColabOneWorkflowTemplate, populating
// NodeCount and auto-extracting Fields if empty.
func (t *ColabOneWorkflowTemplate) Validate() error {
	if !workflowIDRe.MatchString(t.ID) {
		return errors.New("工作流 ID 不合法")
	}
	if utf8.RuneCountInString(t.Name) > 160 {
		return errors.New("工作流名称长度不合法")
	}
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return errors.New("工作流名称不能为空")
	}
	t.Name = name

	if utf8.RuneCountInString(t.Description) > 1000 {
		return errors.New("工作流描述长度不能超过 1000")
	}

	if t.Source == "" {
		t.Source = "custom"
	}
	if t.Source != "builtin" && t.Source != "custom" {
		return errors.New("工作流来源不合法")
	}

	if t.MinVRAMMib < 0 {
		return errors.New("最低显存需求不能为负数")
	}
	if !isFiniteFloat64(t.CreatedAt) || t.CreatedAt < 0 {
		return errors.New("创建时间不合法")
	}
	if !isFiniteFloat64(t.UpdatedAt) || t.UpdatedAt < 0 {
		return errors.New("更新时间不合法")
	}

	if len(t.CustomNodes) > 32 {
		return errors.New("自定义节点数量不能超过 32 个")
	}
	for i := range t.CustomNodes {
		if err := t.CustomNodes[i].Validate(); err != nil {
			return err
		}
	}
	if t.CustomNodes == nil {
		t.CustomNodes = []ColabOneComfyNodeSpec{}
	}

	if len(t.PipPackages) > 64 {
		return errors.New("pip 依赖数量不能超过 64 个")
	}
	cleanedPip := make([]string, 0, len(t.PipPackages))
	for _, item := range t.PipPackages {
		pkg := strings.TrimSpace(item)
		if pkg == "" ||
			strings.HasPrefix(pkg, "-") ||
			strings.Contains(pkg, "://") ||
			strings.Contains(pkg, "..") ||
			!pipPkgRe.MatchString(pkg) {
			return fmt.Errorf("非法的 pip 依赖声明: %q", item)
		}
		cleanedPip = append(cleanedPip, pkg)
	}
	t.PipPackages = cleanedPip

	if len(t.Models) > 32 {
		return errors.New("模型依赖数量不能超过 32 个")
	}
	for i := range t.Models {
		if err := t.Models[i].Validate(); err != nil {
			return err
		}
	}
	if t.Models == nil {
		t.Models = []ColabOneComfyModelRef{}
	}

	normalized, err := ValidateComfyAPIWorkflow(t.Workflow)
	if err != nil {
		return err
	}
	t.Workflow = normalizedNodesToAnyMap(normalized)

	if len(t.Fields) > 64 {
		return errors.New("字段数量不能超过 64 个")
	}
	for i := range t.Fields {
		if err := t.Fields[i].Validate(); err != nil {
			return err
		}
	}
	if t.NodeCount < 0 {
		return errors.New("节点数量不能为负数")
	}

	t.NodeCount = len(t.Workflow)
	if len(t.Fields) == 0 {
		t.Fields = ExtractWorkflowFields(t.Workflow)
	}
	return nil
}

// ColabOneWorkflowImportRequest represents a request to import or update a ComfyUI API workflow.
type ColabOneWorkflowImportRequest struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Workflow    map[string]any `json:"workflow"`
}

// UnmarshalJSON decodes a ColabOneWorkflowImportRequest with unknown fields forbidden.
func (r *ColabOneWorkflowImportRequest) UnmarshalJSON(data []byte) error {
	type alias ColabOneWorkflowImportRequest
	var aux alias
	if err := decodeStrictJSONObject(data, &aux); err != nil {
		return err
	}
	*r = ColabOneWorkflowImportRequest(aux)
	return nil
}

// Validate normalizes and validates a ColabOneWorkflowImportRequest.
func (r *ColabOneWorkflowImportRequest) Validate() error {
	if r.ID != "" && !workflowIDRe.MatchString(r.ID) {
		return errors.New("工作流 ID 不合法")
	}
	if utf8.RuneCountInString(r.Name) > 160 {
		return errors.New("工作流名称长度不合法")
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return errors.New("工作流名称不能为空")
	}
	r.Name = name

	if utf8.RuneCountInString(r.Description) > 1000 {
		return errors.New("工作流描述长度不能超过 1000")
	}
	normalized, err := ValidateComfyAPIWorkflow(r.Workflow)
	if err != nil {
		return err
	}
	r.Workflow = normalizedNodesToAnyMap(normalized)
	return nil
}

// ColabOneWorkflowRunRequest represents a request to execute a stored ComfyUI workflow.
type ColabOneWorkflowRunRequest struct {
	WorkflowID string            `json:"workflow_id"`
	Overrides  map[string]any    `json:"overrides"`
	Inputs     map[string]string `json:"inputs"`
}

// UnmarshalJSON decodes a ColabOneWorkflowRunRequest with unknown fields forbidden.
func (r *ColabOneWorkflowRunRequest) UnmarshalJSON(data []byte) error {
	type alias ColabOneWorkflowRunRequest
	var aux alias
	if err := decodeStrictJSONObject(data, &aux); err != nil {
		return err
	}
	*r = ColabOneWorkflowRunRequest(aux)
	return nil
}

// Validate normalizes and validates a ColabOneWorkflowRunRequest.
func (r *ColabOneWorkflowRunRequest) Validate() error {
	if !workflowIDRe.MatchString(r.WorkflowID) {
		return errors.New("工作流 ID 不合法")
	}
	if len(r.Overrides) > 128 {
		return errors.New("参数覆盖项数量不能超过 128 个")
	}
	if r.Overrides == nil {
		r.Overrides = map[string]any{}
	} else {
		keys := make([]string, 0, len(r.Overrides))
		for k := range r.Overrides {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, _, err := validateFieldKey(key); err != nil {
				return err
			}
			val := r.Overrides[key]
			switch v := val.(type) {
			case string, bool:
			case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			case float32:
				if !isFiniteFloat64(float64(v)) {
					return fmt.Errorf("字段 %s 包含非法浮点值", key)
				}
			case float64:
				if !isFiniteFloat64(v) {
					return fmt.Errorf("字段 %s 包含非法浮点值", key)
				}
			case json.Number:
				if _, err := strconv.ParseInt(string(v), 10, 64); err != nil {
					if _, uerr := strconv.ParseUint(string(v), 10, 64); uerr != nil {
						fv, ferr := strconv.ParseFloat(string(v), 64)
						if ferr != nil || !isFiniteFloat64(fv) {
							return fmt.Errorf("字段 %s 包含非法浮点值", key)
						}
					}
				}
			default:
				return fmt.Errorf("字段 %s 的覆盖值类型不合法", key)
			}
		}
	}

	if len(r.Inputs) > 32 {
		return errors.New("输入素材数量不能超过 32 个")
	}
	if r.Inputs == nil {
		r.Inputs = map[string]string{}
	} else {
		keys := make([]string, 0, len(r.Inputs))
		for k := range r.Inputs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, _, err := validateFieldKey(key); err != nil {
				return err
			}
			assetID := r.Inputs[key]
			if !assetIDRe.MatchString(assetID) {
				return fmt.Errorf("非法素材 ID: %q", assetID)
			}
		}
	}
	return nil
}

// ColabOneWorkflowOutputRecord describes a persisted output artifact generated by ComfyUI.
type ColabOneWorkflowOutputRecord struct {
	ID           string  `json:"id"`
	WorkflowID   string  `json:"workflow_id"`
	WorkflowName string  `json:"workflow_name"`
	Kind         string  `json:"kind"`
	Filename     string  `json:"filename"`
	Extension    string  `json:"extension"`
	MIME         string  `json:"mime"`
	Size         int64   `json:"size"`
	SHA256       string  `json:"sha256"`
	CreatedAt    float64 `json:"created_at"`
	Elapsed      float64 `json:"elapsed"`
	PromptID     string  `json:"prompt_id"`
}

// UnmarshalJSON decodes a ColabOneWorkflowOutputRecord with unknown fields forbidden.
func (r *ColabOneWorkflowOutputRecord) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID           string   `json:"id"`
		WorkflowID   string   `json:"workflow_id"`
		WorkflowName string   `json:"workflow_name"`
		Kind         string   `json:"kind"`
		Filename     string   `json:"filename"`
		Extension    string   `json:"extension"`
		MIME         string   `json:"mime"`
		Size         int64    `json:"size"`
		SHA256       string   `json:"sha256"`
		CreatedAt    *float64 `json:"created_at"`
		Elapsed      float64  `json:"elapsed"`
		PromptID     string   `json:"prompt_id"`
	}
	if err := decodeStrictJSONObject(data, &raw); err != nil {
		return err
	}
	if raw.CreatedAt == nil {
		return errors.New("创建时间不合法")
	}
	*r = ColabOneWorkflowOutputRecord{
		ID:           raw.ID,
		WorkflowID:   raw.WorkflowID,
		WorkflowName: raw.WorkflowName,
		Kind:         raw.Kind,
		Filename:     raw.Filename,
		Extension:    raw.Extension,
		MIME:         raw.MIME,
		Size:         raw.Size,
		SHA256:       raw.SHA256,
		CreatedAt:    *raw.CreatedAt,
		Elapsed:      raw.Elapsed,
		PromptID:     raw.PromptID,
	}
	return nil
}

// Validate checks all field constraints and format consistency of a ColabOneWorkflowOutputRecord.
func (r *ColabOneWorkflowOutputRecord) Validate() error {
	if !assetIDRe.MatchString(r.ID) {
		return errors.New("产物编号不合法")
	}
	if !workflowIDRe.MatchString(r.WorkflowID) {
		return errors.New("工作流 ID 不合法")
	}
	if n := utf8.RuneCountInString(r.WorkflowName); n < 1 || n > 160 {
		return errors.New("工作流名称长度不合法")
	}
	if !allowedOutputKinds[r.Kind] {
		return errors.New("输出文件格式与类型不匹配")
	}
	if n := utf8.RuneCountInString(r.Filename); n < 1 || n > 255 {
		return errors.New("输出文件名不合法")
	}
	if r.Filename == "." ||
		r.Filename == ".." ||
		strings.Contains(r.Filename, "..") ||
		strings.ContainsAny(r.Filename, `/\`+"\x00") {
		return errors.New("输出文件名不合法")
	}
	expected, ok := defaultAllowedOutputMIME[r.Extension]
	if !ok {
		return errors.New("输出文件格式与类型不匹配")
	}
	if n := utf8.RuneCountInString(r.MIME); n < 1 || n > 100 {
		return errors.New("输出文件格式与类型不匹配")
	}
	if r.Size <= 0 || r.Size > MaxOutputBytes {
		return errors.New("输出文件大小不合法")
	}
	if !sha256LowerRe.MatchString(r.SHA256) {
		return errors.New("输出文件 sha256 不合法")
	}
	if !isFiniteFloat64(r.CreatedAt) || r.CreatedAt < 0 {
		return errors.New("创建时间不合法")
	}
	if !isFiniteFloat64(r.Elapsed) || r.Elapsed < 0 {
		return errors.New("耗时数值不合法")
	}
	if utf8.RuneCountInString(r.PromptID) > 120 {
		return errors.New("prompt_id 长度不合法")
	}
	if expected.Kind != r.Kind || expected.MIME != r.MIME {
		return errors.New("输出文件格式与类型不匹配")
	}
	return nil
}

// ColabOneComfyCanvasConfig stores the named Cloudflare tunnel configuration for the ComfyUI canvas.
type ColabOneComfyCanvasConfig struct {
	Mode        string `json:"mode"`
	Domain      string `json:"domain"`
	TunnelToken string `json:"tunnel_token"`
}

// String redacts TunnelToken so logging a ColabOneComfyCanvasConfig never leaks credentials.
func (c ColabOneComfyCanvasConfig) String() string {
	return fmt.Sprintf("ColabOneComfyCanvasConfig{Mode:%q, Domain:%q, TunnelToken:[redacted]}", c.Mode, c.Domain)
}

// GoString redacts TunnelToken when formatted with %#v.
func (c ColabOneComfyCanvasConfig) GoString() string {
	return c.String()
}

// UnmarshalJSON decodes a ColabOneComfyCanvasConfig with unknown fields forbidden.
func (c *ColabOneComfyCanvasConfig) UnmarshalJSON(data []byte) error {
	var raw struct {
		Mode        *string `json:"mode"`
		Domain      string  `json:"domain"`
		TunnelToken string  `json:"tunnel_token"`
	}
	if err := decodeStrictJSONObject(data, &raw); err != nil {
		return err
	}
	mode := "named"
	if raw.Mode != nil {
		if *raw.Mode == "" {
			return errors.New("已不再支持临时域名，请配置固定域名和 Cloudflare Tunnel Token")
		}
		mode = *raw.Mode
	}
	*c = ColabOneComfyCanvasConfig{
		Mode:        mode,
		Domain:      raw.Domain,
		TunnelToken: raw.TunnelToken,
	}
	return nil
}

// Validate normalizes Domain and extracts TunnelToken on a ColabOneComfyCanvasConfig.
func (c *ColabOneComfyCanvasConfig) Validate() error {
	if c.Mode == "" {
		c.Mode = "named"
	}
	if c.Mode != "named" {
		return errors.New("已不再支持临时域名，请配置固定域名和 Cloudflare Tunnel Token")
	}
	if utf8.RuneCountInString(c.Domain) > publicapi.MaxDomainLength {
		return publicapi.ErrPublicOriginInvalid
	}
	if utf8.RuneCountInString(c.TunnelToken) > publicapi.MaxTunnelTokenLength {
		return publicapi.ErrTunnelTokenInvalid
	}
	domain, err := publicapi.ValidatePublicOrigin(c.Domain)
	if err != nil {
		return err
	}
	token, err := publicapi.ValidateTunnelToken(c.TunnelToken)
	if err != nil {
		return err
	}
	c.Domain = domain
	c.TunnelToken = token
	return nil
}

// ColabOneComfyCatalog describes the pinned ComfyUI repository, ports, and built-in templates.
type ColabOneComfyCatalog struct {
	Repo      string                     `json:"repo"`
	Tag       string                     `json:"tag"`
	Commit    string                     `json:"commit"`
	Port      int                        `json:"port"`
	ProxyPort int                        `json:"proxy_port"`
	Templates []ColabOneWorkflowTemplate `json:"templates"`
}

// UnmarshalJSON decodes a ColabOneComfyCatalog with unknown fields forbidden and
// default ports 8188 / 8189 when omitted.
func (c *ColabOneComfyCatalog) UnmarshalJSON(data []byte) error {
	var raw struct {
		Repo      string                     `json:"repo"`
		Tag       string                     `json:"tag"`
		Commit    string                     `json:"commit"`
		Port      *int                       `json:"port"`
		ProxyPort *int                       `json:"proxy_port"`
		Templates []ColabOneWorkflowTemplate `json:"templates"`
	}
	if err := decodeStrictJSONObject(data, &raw); err != nil {
		return err
	}
	port := 8188
	if raw.Port != nil {
		if *raw.Port == 0 {
			return errors.New("ComfyUI 端口必须在 1024 到 65535 之间")
		}
		port = *raw.Port
	}
	proxyPort := 8189
	if raw.ProxyPort != nil {
		if *raw.ProxyPort == 0 {
			return errors.New("ComfyUI 代理端口必须在 1024 到 65535 之间")
		}
		proxyPort = *raw.ProxyPort
	}
	*c = ColabOneComfyCatalog{
		Repo:      raw.Repo,
		Tag:       raw.Tag,
		Commit:    raw.Commit,
		Port:      port,
		ProxyPort: proxyPort,
		Templates: raw.Templates,
	}
	return nil
}

// Validate normalizes and validates a ColabOneComfyCatalog and all embedded templates.
func (c *ColabOneComfyCatalog) Validate() error {
	repo, err := validateGitHubOrSlug(c.Repo)
	if err != nil {
		return err
	}
	c.Repo = repo

	if n := utf8.RuneCountInString(c.Tag); n < 1 || n > 120 {
		return errors.New("ComfyUI 版本标签不合法")
	}
	if !commitSHARe.MatchString(c.Commit) {
		return errors.New("commit 必须为 40 位十六进制 SHA")
	}
	if c.Port == 0 {
		c.Port = 8188
	}
	if c.ProxyPort == 0 {
		c.ProxyPort = 8189
	}
	if c.Port < 1024 || c.Port > 65535 {
		return errors.New("ComfyUI 端口必须在 1024 到 65535 之间")
	}
	if c.ProxyPort < 1024 || c.ProxyPort > 65535 {
		return errors.New("ComfyUI 代理端口必须在 1024 到 65535 之间")
	}
	if c.Port == c.ProxyPort {
		return errors.New("ComfyUI 端口与代理端口不能相同")
	}

	if c.Templates == nil {
		c.Templates = []ColabOneWorkflowTemplate{}
	}
	seenIDs := make(map[string]struct{}, len(c.Templates))
	for i := range c.Templates {
		if err := c.Templates[i].Validate(); err != nil {
			return err
		}
		id := c.Templates[i].ID
		if _, dup := seenIDs[id]; dup {
			return errors.New("内置工作流模板 ID 重复")
		}
		seenIDs[id] = struct{}{}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Core pure workflow functions
// ---------------------------------------------------------------------------

// ValidateComfyAPIWorkflow validates a ComfyUI API-format workflow JSON object,
// rejects canvas-format exports, enforces node count and serialized size limits,
// rejects non-JSON-serializable values (including NaN/Inf), trims class_type,
// and returns a deep-copied normalized node map.
func ValidateComfyAPIWorkflow(workflow map[string]any) (map[string]map[string]any, error) {
	if workflow == nil {
		return nil, errors.New("工作流必须为 JSON 对象")
	}
	if len(workflow) == 0 {
		return nil, errors.New("工作流不能为空")
	}
	if nodesVal, hasNodes := workflow["nodes"]; hasNodes && isSliceValue(nodesVal) {
		_, hasLinks := workflow["links"]
		_, hasVersion := workflow["version"]
		if hasLinks || hasVersion {
			return nil, errors.New("检测到这是 ComfyUI 画布保存格式，请在 ComfyUI 中开启 Dev Mode 并使用「Save (API Format)」导出后再导入")
		}
	}
	if len(workflow) > maxWorkflowNodes {
		return nil, fmt.Errorf("工作流节点数量不能超过 %d 个", maxWorkflowNodes)
	}
	if err := validateWorkflowNumbers(workflow); err != nil {
		return nil, errors.New("工作流包含无法序列化的内容")
	}
	encoded, err := encodeWorkflowJSON(workflow)
	if err != nil {
		return nil, errors.New("工作流包含无法序列化的内容")
	}
	if len(encoded) > MaxWorkflowBytes {
		return nil, fmt.Errorf("工作流体积超过限制（最大 %d MiB）", MaxWorkflowBytes/(1024*1024))
	}

	nodeIDs := make([]string, 0, len(workflow))
	for nodeID := range workflow {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool {
		return compareNodeID(nodeIDs[i], nodeIDs[j]) < 0
	})

	normalized := make(map[string]map[string]any, len(workflow))
	for _, nodeID := range nodeIDs {
		if !nodeIDRe.MatchString(nodeID) {
			return nil, fmt.Errorf("非法节点 ID: %q", nodeID)
		}
		rawNode := workflow[nodeID]
		node, ok := rawNode.(map[string]any)
		if !ok || node == nil {
			return nil, fmt.Errorf("节点 %s 必须为对象", nodeID)
		}
		rawClassType, ok := node["class_type"].(string)
		trimmedClassType := strings.TrimSpace(rawClassType)
		if !ok || trimmedClassType == "" || utf8.RuneCountInString(trimmedClassType) > 160 {
			return nil, fmt.Errorf("节点 %s 缺少有效的 class_type", nodeID)
		}
		inputs, ok := node["inputs"].(map[string]any)
		if !ok || inputs == nil {
			return nil, fmt.Errorf("节点 %s 缺少有效的 inputs 对象", nodeID)
		}
		for k := range inputs {
			if k == "" {
				return nil, fmt.Errorf("节点 %s 包含非法输入名称", nodeID)
			}
		}
		nodeCopy := deepCopyMap(node)
		nodeCopy["class_type"] = trimmedClassType
		normalized[nodeID] = nodeCopy
	}
	return normalized, nil
}

// ExtractWorkflowFields inspects a ComfyUI API workflow and auto-extracts up to 32
// editable media, prompt, and numeric fields in deterministic node and priority order.
func ExtractWorkflowFields(workflow map[string]any) []ColabOneWorkflowField {
	extracted := make([]ColabOneWorkflowField, 0)
	if workflow == nil {
		return extracted
	}

	nodeIDs := make([]string, 0, len(workflow))
	for k := range workflow {
		nodeIDs = append(nodeIDs, k)
	}
	sort.Slice(nodeIDs, func(i, j int) bool {
		return compareNodeID(nodeIDs[i], nodeIDs[j]) < 0
	})

	type candidateField struct {
		priority  int
		inputName string
		field     ColabOneWorkflowField
	}

	for _, nodeID := range nodeIDs {
		node, ok := workflow[nodeID].(map[string]any)
		if !ok || node == nil {
			continue
		}
		rawClassType, ok := node["class_type"].(string)
		if !ok {
			continue
		}
		classType := strings.TrimSpace(rawClassType)
		inputs, ok := node["inputs"].(map[string]any)
		if classType == "" || !ok || inputs == nil {
			continue
		}
		ctLower := strings.ToLower(classType)
		title := nodeTitle(node, classType)

		nodeFields := make([]candidateField, 0)
		for inputName, val := range inputs {
			if inputName == "" {
				continue
			}
			if utf8.RuneCountInString(nodeID) > 80 || utf8.RuneCountInString(inputName) > 80 {
				continue
			}
			key := nodeID + "." + inputName
			if utf8.RuneCountInString(key) > 160 || isNodeLink(val) {
				continue
			}

			nameLower := strings.ToLower(inputName)

			if strVal, isStr := val.(string); isStr {
				if (strings.Contains(ctLower, "loadimage") &&
					nameLower != "upload" &&
					(imageLoaderInputNames[nameLower] || strings.Contains(nameLower, "image"))) ||
					(nameLower == "image" && strings.Contains(ctLower, "load")) {
					nodeFields = append(nodeFields, candidateField{
						priority:  0,
						inputName: inputName,
						field: ColabOneWorkflowField{
							Key:       key,
							Label:     trimLabel(title + " · 参考图片"),
							NodeID:    nodeID,
							InputName: inputName,
							FieldType: "image",
							Default:   strVal,
							Options:   []string{},
						},
					})
					continue
				}

				if (strings.Contains(ctLower, "loadaudio") &&
					nameLower != "upload" &&
					nameLower != "audioui" &&
					(audioLoaderInputNames[nameLower] || strings.Contains(nameLower, "audio"))) ||
					(nameLower == "audio" && strings.Contains(ctLower, "load")) {
					nodeFields = append(nodeFields, candidateField{
						priority:  1,
						inputName: inputName,
						field: ColabOneWorkflowField{
							Key:       key,
							Label:     trimLabel(title + " · 输入音频"),
							NodeID:    nodeID,
							InputName: inputName,
							FieldType: "audio",
							Default:   strVal,
							Options:   []string{},
						},
					})
					continue
				}

				if strings.Contains(ctLower, "text") ||
					strings.Contains(ctLower, "prompt") ||
					strings.Contains(ctLower, "string") ||
					textInputNames[nameLower] {
					label, priority := promptLabelAndPriority(title, ctLower, nameLower)
					nodeFields = append(nodeFields, candidateField{
						priority:  priority,
						inputName: inputName,
						field: ColabOneWorkflowField{
							Key:       key,
							Label:     label,
							NodeID:    nodeID,
							InputName: inputName,
							FieldType: "textarea",
							Default:   strVal,
							Options:   []string{},
						},
					})
					continue
				}
			}

			if spec, exists := numericSpecs[nameLower]; exists {
				if intVal, hasExactInt, floatVal, okNum := extractNumericValue(val); okNum {
					var defaultVal any
					if spec.fieldType == "integer" {
						if hasExactInt {
							defaultVal = intVal
						} else if floatVal >= -9223372036854775808.0 && floatVal < 9223372036854775808.0 {
							defaultVal = int64(floatVal)
						} else {
							defaultVal = floatVal
						}
					} else {
						defaultVal = floatVal
					}
					nodeFields = append(nodeFields, candidateField{
						priority:  spec.priority,
						inputName: inputName,
						field: ColabOneWorkflowField{
							Key:       key,
							Label:     trimLabel(title + " · " + spec.cnName),
							NodeID:    nodeID,
							InputName: inputName,
							FieldType: spec.fieldType,
							Default:   defaultVal,
							Options:   []string{},
							MinValue:  copyFloatPtr(spec.minValue),
							MaxValue:  copyFloatPtr(spec.maxValue),
							Step:      copyFloatPtr(spec.step),
						},
					})
				}
			}
		}

		sort.Slice(nodeFields, func(i, j int) bool {
			if nodeFields[i].priority != nodeFields[j].priority {
				return nodeFields[i].priority < nodeFields[j].priority
			}
			return nodeFields[i].inputName < nodeFields[j].inputName
		})

		for _, item := range nodeFields {
			extracted = append(extracted, item.field)
			if len(extracted) >= maxAutoFields {
				return extracted
			}
		}
	}

	return extracted
}

// ApplyWorkflowOverrides returns a deep copy of workflow with scalar parameter overrides
// and uploaded media filenames applied. Existing node links ([nodeId, outputIdx]) are
// never overwritten, and non-finite float overrides return an error.
func ApplyWorkflowOverrides(
	workflow map[string]any,
	overrides map[string]any,
	mediaMap map[string]string,
) (map[string]any, error) {
	result := deepCopyMap(workflow)
	if result == nil {
		result = map[string]any{}
	}

	targetInputs := func(key string) (map[string]any, string, bool) {
		if !strings.Contains(key, ".") {
			return nil, "", false
		}
		nodeID, inputName, _ := strings.Cut(key, ".")
		node, ok := result[nodeID].(map[string]any)
		if !ok || node == nil {
			return nil, "", false
		}
		inputs, ok := node["inputs"].(map[string]any)
		if !ok || inputs == nil {
			return nil, "", false
		}
		existing, exists := inputs[inputName]
		if !exists {
			return nil, "", false
		}
		if isNodeLink(existing) {
			return nil, "", false
		}
		return inputs, inputName, true
	}

	if len(overrides) > 0 {
		keys := make([]string, 0, len(overrides))
		for k := range overrides {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			inputs, inputName, ok := targetInputs(key)
			if !ok {
				continue
			}
			val := overrides[key]
			switch v := val.(type) {
			case bool:
				inputs[inputName] = v
			case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
				inputs[inputName] = v
			case float32:
				if !isFiniteFloat64(float64(v)) {
					return nil, fmt.Errorf("字段 %s 包含非法浮点值", key)
				}
				inputs[inputName] = v
			case float64:
				if !isFiniteFloat64(v) {
					return nil, fmt.Errorf("字段 %s 包含非法浮点值", key)
				}
				inputs[inputName] = v
			case json.Number:
				if n, err := strconv.ParseInt(string(v), 10, 64); err == nil {
					inputs[inputName] = n
				} else if u, err := strconv.ParseUint(string(v), 10, 64); err == nil {
					inputs[inputName] = u
				} else if f, err := strconv.ParseFloat(string(v), 64); err == nil && isFiniteFloat64(f) {
					inputs[inputName] = f
				} else {
					return nil, fmt.Errorf("字段 %s 包含非法浮点值", key)
				}
			case string:
				inputs[inputName] = v
			}
		}
	}

	if len(mediaMap) > 0 {
		keys := make([]string, 0, len(mediaMap))
		for k := range mediaMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			filename := mediaMap[key]
			if filename == "" {
				continue
			}
			inputs, inputName, ok := targetInputs(key)
			if !ok {
				continue
			}
			inputs[inputName] = filename
		}
	}

	return result, nil
}
