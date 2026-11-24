// Package comfyui provides data-only ComfyUI workflow schemas, validation,
// field extraction, override application, and catalog models shared across ColabOne.
package comfyui

import (
	"bytes"
	"encoding/base64"
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
	"unicode"
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
	// MaxWorkflowNestingDepth is the maximum JSON container nesting depth allowed in a workflow.
	MaxWorkflowNestingDepth = 32

	maxWorkflowNodes         = 500
	maxAutoFields            = 32
	maxVoicePresetsPerEngine = 24
	maxVoicePresetSeed       = 2147483647
)

var errWorkflowNestingTooDeep = errors.New("工作流 JSON 嵌套层级超过 32 层上限")

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
	nodeIDRe            = regexp.MustCompile(`\A[a-zA-Z0-9_:-]{1,80}\z`)
	workflowIDRe        = regexp.MustCompile(`\A[a-zA-Z0-9_-]{1,80}\z`)
	voicePresetIDRe     = regexp.MustCompile(`\A[a-zA-Z0-9_-]{1,64}\z`)
	repoSlugRe          = regexp.MustCompile(`\A[0-9A-Za-z_.-]+/[0-9A-Za-z_.-]+\z`)
	githubRepoRe        = regexp.MustCompile(`\Ahttps://github\.com/([0-9A-Za-z_.-]+)/([0-9A-Za-z_.-]+?)(?:\.git)?/?\z`)
	assetIDRe           = regexp.MustCompile(`\A[a-f0-9]{32}\z`)
	pipPkgRe            = regexp.MustCompile(`\A[A-Za-z0-9](?:[A-Za-z0-9._\[\],<>=!~]|-[A-Za-z0-9._\[\],<>=!~]| +[A-Za-z0-9._\[\],<>=!~])*\z`)
	pipWhitespaceFlagRe = regexp.MustCompile(`\s-`)
	commitSHARe         = regexp.MustCompile(`\A[a-fA-F0-9]{40}\z`)
	sha256AnyRe         = regexp.MustCompile(`\A[a-fA-F0-9]{64}\z`)
	sha256LowerRe       = regexp.MustCompile(`\A[a-f0-9]{64}\z`)

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

	allowedNodePatchKinds = map[string]bool{
		"wav2vec2_hidden_states_hook": true,
	}

	allowedPipelineKinds = map[string]bool{
		"comfy_standard": true,
		"digital_human":  true,
	}

	allowedVideoBackends = map[string]bool{
		"infinitetalk":   true,
		"longcat_avatar": true,
		"custom":         true,
	}

	allowedUpscaleModes = map[string]bool{
		"none":    true,
		"ai_2x":   true,
		"lanczos": true,
	}

	allowedVoicePresetGenders = map[string]bool{
		"female":  true,
		"male":    true,
		"neutral": true,
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

	excludedTextInputNames = map[string]bool{
		"model_name":     true,
		"precision":      true,
		"quantization":   true,
		"device":         true,
		"load_device":    true,
		"base_precision": true,
		"attention_mode": true,
	}

	imageInputExtensions = map[string]bool{
		"png":  true,
		"jpg":  true,
		"jpeg": true,
		"webp": true,
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
	return validateWorkflowNumbersDepth(v, 0)
}

func validateWorkflowNumbersDepth(v any, depth int) error {
	if depth > MaxWorkflowNestingDepth {
		return errWorkflowNestingTooDeep
	}
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
		if depth >= MaxWorkflowNestingDepth {
			return errWorkflowNestingTooDeep
		}
		for _, item := range x {
			if err := validateWorkflowNumbersDepth(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case map[string]map[string]any:
		if depth >= MaxWorkflowNestingDepth {
			return errWorkflowNestingTooDeep
		}
		for _, item := range x {
			if err := validateWorkflowNumbersDepth(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case []any:
		if depth >= MaxWorkflowNestingDepth {
			return errWorkflowNestingTooDeep
		}
		for _, item := range x {
			if err := validateWorkflowNumbersDepth(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			if depth >= MaxWorkflowNestingDepth {
				return errWorkflowNestingTooDeep
			}
			for i := 0; i < rv.Len(); i++ {
				if err := validateWorkflowNumbersDepth(rv.Index(i).Interface(), depth+1); err != nil {
					return err
				}
			}
		case reflect.Map:
			if depth >= MaxWorkflowNestingDepth {
				return errWorkflowNestingTooDeep
			}
			iter := rv.MapRange()
			for iter.Next() {
				if err := validateWorkflowNumbersDepth(iter.Value().Interface(), depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func validateSafeNodeName(raw string) (string, error) {
	if utf8.RuneCountInString(raw) > 120 {
		return "", errors.New("自定义节点名称不合法")
	}
	name := strings.TrimSpace(raw)
	if name == "" ||
		strings.HasPrefix(name, ".") ||
		strings.Contains(name, "..") ||
		strings.ContainsAny(name, `/\:`+"\x00") ||
		utf8.RuneCountInString(name) > 120 {
		return "", errors.New("自定义节点名称不合法")
	}
	return name, nil
}

func validateSafePipPackages(pkgs []string) ([]string, error) {
	if len(pkgs) > 64 {
		return nil, errors.New("pip 依赖数量不能超过 64 个")
	}
	cleaned := make([]string, 0, len(pkgs))
	for _, item := range pkgs {
		pkg := strings.TrimSpace(item)
		if pkg == "" ||
			len(pkg) > 200 ||
			strings.HasPrefix(pkg, "-") ||
			strings.Contains(pkg, "--") ||
			pipWhitespaceFlagRe.MatchString(pkg) ||
			strings.Contains(pkg, "://") ||
			strings.Contains(pkg, "..") ||
			!pipPkgRe.MatchString(pkg) {
			return nil, fmt.Errorf("非法的 pip 依赖声明: %q", item)
		}
		cleaned = append(cleaned, pkg)
	}
	return cleaned, nil
}

func validateCleanHFToken(raw string) (string, error) {
	for _, ch := range raw {
		if ch == '\r' || ch == '\n' || ch < 0x20 || ch == 0x7f || unicode.IsControl(ch) {
			return "", errors.New("hf_token 包含非法空白或控制字符")
		}
	}
	trimmed := strings.Trim(raw, " ")
	for _, ch := range trimmed {
		if unicode.IsSpace(ch) {
			return "", errors.New("hf_token 包含非法空白或控制字符")
		}
	}
	if utf8.RuneCountInString(raw) > 512 || utf8.RuneCountInString(trimmed) > 512 {
		return "", errors.New("hf_token 长度不能超过 512")
	}
	return trimmed, nil
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

func isStrictIntValue(v any) bool {
	switch x := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
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

func coerceIntegralFloatToInt64(f float64) (int64, bool) {
	if !isFiniteFloat64(f) || math.Trunc(f) != f {
		return 0, false
	}
	if f < -9223372036854775808.0 || f >= 9223372036854775808.0 {
		return 0, false
	}
	return int64(f), true
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
	name, err := validateSafeNodeName(s.Name)
	if err != nil {
		return err
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

// ColabOneComfyNodePatch declares an allowlisted named runtime patch for a pinned custom node.
type ColabOneComfyNodePatch struct {
	NodeName string `json:"node_name"`
	Kind     string `json:"kind"`
}

// UnmarshalJSON decodes a ColabOneComfyNodePatch with unknown fields and nulls forbidden.
func (p *ColabOneComfyNodePatch) UnmarshalJSON(data []byte) error {
	type alias ColabOneComfyNodePatch
	var aux alias
	if err := decodeStrictJSONObject(data, &aux); err != nil {
		return err
	}
	*p = ColabOneComfyNodePatch(aux)
	return nil
}

// Validate normalizes and validates a ColabOneComfyNodePatch.
func (p *ColabOneComfyNodePatch) Validate() error {
	name, err := validateSafeNodeName(p.NodeName)
	if err != nil {
		return err
	}
	p.NodeName = name

	if !allowedNodePatchKinds[p.Kind] {
		return errors.New("节点补丁类型不合法")
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

// ColabOneComfyTtsVoicePreset declares a built-in named voice preset for a remote TTS engine.
type ColabOneComfyTtsVoicePreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Gender      string `json:"gender"`
	Style       string `json:"style"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Seed        int    `json:"seed"`
}

// UnmarshalJSON decodes a ColabOneComfyTtsVoicePreset with unknown fields and nulls
// forbidden and Pydantic-parity defaults for gender ("female") and seed (42).
func (p *ColabOneComfyTtsVoicePreset) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Gender      *string `json:"gender"`
		Style       string  `json:"style"`
		Description string  `json:"description"`
		Prompt      string  `json:"prompt"`
		Seed        *int    `json:"seed"`
	}
	if err := decodeStrictJSONObject(data, &raw); err != nil {
		return err
	}
	gender := "female"
	if raw.Gender != nil {
		if *raw.Gender == "" {
			return errors.New("内置音色性别不合法")
		}
		gender = *raw.Gender
	}
	seed := 42
	if raw.Seed != nil {
		seed = *raw.Seed
	}
	*p = ColabOneComfyTtsVoicePreset{
		ID:          raw.ID,
		Name:        raw.Name,
		Gender:      gender,
		Style:       raw.Style,
		Description: raw.Description,
		Prompt:      raw.Prompt,
		Seed:        seed,
	}
	return nil
}

// Validate normalizes and validates a ColabOneComfyTtsVoicePreset.
func (p *ColabOneComfyTtsVoicePreset) Validate() error {
	if !voicePresetIDRe.MatchString(p.ID) {
		return errors.New("内置音色 ID 不合法")
	}
	if utf8.RuneCountInString(p.Name) > 80 {
		return errors.New("内置音色名称长度不合法")
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return errors.New("内置音色名称不能为空")
	}
	p.Name = name

	if p.Gender == "" {
		p.Gender = "female"
	}
	if !allowedVoicePresetGenders[p.Gender] {
		return errors.New("内置音色性别不合法")
	}
	if utf8.RuneCountInString(p.Style) > 80 {
		return errors.New("内置音色风格长度不能超过 80")
	}
	if utf8.RuneCountInString(p.Description) > 240 {
		return errors.New("内置音色描述长度不能超过 240")
	}
	if utf8.RuneCountInString(p.Prompt) > 600 {
		return errors.New("内置音色提示词长度不能超过 600")
	}
	if p.Seed < 0 || p.Seed > maxVoicePresetSeed {
		return errors.New("内置音色随机种子必须在 0 到 2147483647 之间")
	}
	return nil
}

// ColabOneComfyTtsEngineSpec declares a pinned remote GPU TTS engine for digital-human pipelines.
type ColabOneComfyTtsEngineSpec struct {
	ID                  string                        `json:"id"`
	Name                string                        `json:"name"`
	Description         string                        `json:"description"`
	Repo                string                        `json:"repo"`
	Revision            string                        `json:"revision"`
	AuxRepo             string                        `json:"aux_repo"`
	AuxRevision         string                        `json:"aux_revision"`
	License             string                        `json:"license"`
	CommercialOK        bool                          `json:"commercial_ok"`
	LicenseNotice       string                        `json:"license_notice"`
	MinVRAMMib          int                           `json:"min_vram_mib"`
	SampleRate          int                           `json:"sample_rate"`
	SupportsVoiceClone  bool                          `json:"supports_voice_clone"`
	SupportsVoiceDesign bool                          `json:"supports_voice_design"`
	SupportsEmotionRef  bool                          `json:"supports_emotion_ref"`
	SupportsInlineTags  bool                          `json:"supports_inline_tags"`
	PipPackages         []string                      `json:"pip_packages"`
	SourceRepo          string                        `json:"source_repo"`
	SourceCommit        string                        `json:"source_commit"`
	VoicePresets        []ColabOneComfyTtsVoicePreset `json:"voice_presets"`
}

// UnmarshalJSON decodes a ColabOneComfyTtsEngineSpec with unknown fields forbidden and
// Pydantic-parity defaults for license, commercial_ok, sample_rate, supports_voice_clone, and voice_presets.
func (s *ColabOneComfyTtsEngineSpec) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID                  string                        `json:"id"`
		Name                string                        `json:"name"`
		Description         string                        `json:"description"`
		Repo                string                        `json:"repo"`
		Revision            string                        `json:"revision"`
		AuxRepo             string                        `json:"aux_repo"`
		AuxRevision         string                        `json:"aux_revision"`
		License             *string                       `json:"license"`
		CommercialOK        *bool                         `json:"commercial_ok"`
		LicenseNotice       string                        `json:"license_notice"`
		MinVRAMMib          int                           `json:"min_vram_mib"`
		SampleRate          *int                          `json:"sample_rate"`
		SupportsVoiceClone  *bool                         `json:"supports_voice_clone"`
		SupportsVoiceDesign bool                          `json:"supports_voice_design"`
		SupportsEmotionRef  bool                          `json:"supports_emotion_ref"`
		SupportsInlineTags  bool                          `json:"supports_inline_tags"`
		PipPackages         []*string                     `json:"pip_packages"`
		SourceRepo          string                        `json:"source_repo"`
		SourceCommit        string                        `json:"source_commit"`
		VoicePresets        []ColabOneComfyTtsVoicePreset `json:"voice_presets"`
	}
	if err := decodeStrictJSONObject(data, &raw); err != nil {
		return err
	}
	license := "Apache-2.0"
	if raw.License != nil {
		if strings.TrimSpace(*raw.License) == "" {
			return errors.New("许可协议名称不能为空")
		}
		license = *raw.License
	}
	commercialOK := true
	if raw.CommercialOK != nil {
		commercialOK = *raw.CommercialOK
	}
	sampleRate := 48000
	if raw.SampleRate != nil {
		if *raw.SampleRate == 0 {
			return errors.New("TTS 采样率必须在 8000 到 96000 之间")
		}
		sampleRate = *raw.SampleRate
	}
	supportsVoiceClone := true
	if raw.SupportsVoiceClone != nil {
		supportsVoiceClone = *raw.SupportsVoiceClone
	}
	pipPackages := make([]string, 0, len(raw.PipPackages))
	for _, pkg := range raw.PipPackages {
		if pkg == nil {
			return errors.New("字段 pip_packages 不能为空值")
		}
		pipPackages = append(pipPackages, *pkg)
	}
	voicePresets := raw.VoicePresets
	if voicePresets == nil {
		voicePresets = []ColabOneComfyTtsVoicePreset{}
	}
	*s = ColabOneComfyTtsEngineSpec{
		ID:                  raw.ID,
		Name:                raw.Name,
		Description:         raw.Description,
		Repo:                raw.Repo,
		Revision:            raw.Revision,
		AuxRepo:             raw.AuxRepo,
		AuxRevision:         raw.AuxRevision,
		License:             license,
		CommercialOK:        commercialOK,
		LicenseNotice:       raw.LicenseNotice,
		MinVRAMMib:          raw.MinVRAMMib,
		SampleRate:          sampleRate,
		SupportsVoiceClone:  supportsVoiceClone,
		SupportsVoiceDesign: raw.SupportsVoiceDesign,
		SupportsEmotionRef:  raw.SupportsEmotionRef,
		SupportsInlineTags:  raw.SupportsInlineTags,
		PipPackages:         pipPackages,
		SourceRepo:          raw.SourceRepo,
		SourceCommit:        raw.SourceCommit,
		VoicePresets:        voicePresets,
	}
	return nil
}

// Validate normalizes and validates a ColabOneComfyTtsEngineSpec.
func (s *ColabOneComfyTtsEngineSpec) Validate() error {
	if !workflowIDRe.MatchString(s.ID) {
		return errors.New("TTS 引擎 ID 不合法")
	}
	if utf8.RuneCountInString(s.Name) > 160 {
		return errors.New("TTS 引擎名称长度不合法")
	}
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return errors.New("TTS 引擎名称不能为空")
	}
	s.Name = name

	if utf8.RuneCountInString(s.Description) > 1000 {
		return errors.New("TTS 引擎描述长度不能超过 1000")
	}

	if utf8.RuneCountInString(s.Repo) > 300 {
		return errors.New("需要 Hugging Face 的 owner/repository")
	}
	repo, err := validateOwnerRepo(s.Repo, "需要 Hugging Face 的 owner/repository")
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(repo) > 300 {
		return errors.New("需要 Hugging Face 的 owner/repository")
	}
	s.Repo = repo

	if !commitSHARe.MatchString(s.Revision) {
		return errors.New("revision 必须为 40 位十六进制 SHA")
	}

	if s.AuxRepo != "" {
		if utf8.RuneCountInString(s.AuxRepo) > 300 {
			return errors.New("需要 Hugging Face 的 owner/repository")
		}
		auxRepo, err := validateOwnerRepo(s.AuxRepo, "需要 Hugging Face 的 owner/repository")
		if err != nil {
			return err
		}
		if utf8.RuneCountInString(auxRepo) > 300 {
			return errors.New("需要 Hugging Face 的 owner/repository")
		}
		s.AuxRepo = auxRepo
	}
	if s.AuxRevision != "" && !commitSHARe.MatchString(s.AuxRevision) {
		return errors.New("aux_revision 必须为 40 位十六进制 SHA")
	}
	if s.AuxRepo != "" && s.AuxRevision == "" {
		return errors.New("设置 aux_repo 时必须提供 40 位十六进制 aux_revision")
	}

	if s.License == "" {
		s.License = "Apache-2.0"
	}
	if utf8.RuneCountInString(s.License) > 160 {
		return errors.New("许可协议名称长度不合法")
	}
	lic := strings.TrimSpace(s.License)
	if lic == "" {
		return errors.New("许可协议名称不能为空")
	}
	s.License = lic

	if utf8.RuneCountInString(s.LicenseNotice) > 600 {
		return errors.New("许可协议提示长度不能超过 600")
	}
	if s.MinVRAMMib < 0 {
		return errors.New("最低显存需求不能为负数")
	}
	if s.SampleRate == 0 {
		s.SampleRate = 48000
	}
	if s.SampleRate < 8000 || s.SampleRate > 96000 {
		return errors.New("TTS 采样率必须在 8000 到 96000 之间")
	}

	cleanedPip, err := validateSafePipPackages(s.PipPackages)
	if err != nil {
		return err
	}
	s.PipPackages = cleanedPip

	if s.SourceRepo != "" {
		if utf8.RuneCountInString(s.SourceRepo) > 300 {
			return errors.New("GitHub 仓库地址不合法")
		}
		sourceRepo, err := validateGitHubOrSlug(s.SourceRepo)
		if err != nil {
			return err
		}
		if utf8.RuneCountInString(sourceRepo) > 300 {
			return errors.New("GitHub 仓库地址不合法")
		}
		s.SourceRepo = sourceRepo
	}
	if s.SourceCommit != "" && !commitSHARe.MatchString(s.SourceCommit) {
		return errors.New("source_commit 必须为 40 位十六进制 SHA")
	}

	if len(s.VoicePresets) > maxVoicePresetsPerEngine {
		return errors.New("内置音色预设数量不能超过 24 个")
	}
	for i := range s.VoicePresets {
		if err := s.VoicePresets[i].Validate(); err != nil {
			return err
		}
	}
	if s.VoicePresets == nil {
		s.VoicePresets = []ColabOneComfyTtsVoicePreset{}
	}
	return nil
}

// ColabOneComfyPipelineSpec declares optional multi-stage pipeline configuration for a workflow template.
type ColabOneComfyPipelineSpec struct {
	Kind                   string   `json:"kind"`
	VideoBackend           string   `json:"video_backend"`
	DefaultTtsEngine       string   `json:"default_tts_engine"`
	SupportedTtsEngines    []string `json:"supported_tts_engines"`
	MaxSegmentSeconds      float64  `json:"max_segment_seconds"`
	TailFrameOffsetSeconds float64  `json:"tail_frame_offset_seconds"`
	OutputFPS              int      `json:"output_fps"`
	TargetWidth            int      `json:"target_width"`
	TargetHeight           int      `json:"target_height"`
	UpscaleMode            string   `json:"upscale_mode"`
	AudioNodeKey           string   `json:"audio_node_key"`
	ImageNodeKey           string   `json:"image_node_key"`
	PromptNodeKey          string   `json:"prompt_node_key"`
	FramesNodeKey          string   `json:"frames_node_key"`
	WidthNodeKey           string   `json:"width_node_key"`
	HeightNodeKey          string   `json:"height_node_key"`
	FPSNodeKey             string   `json:"fps_node_key"`
	StepsNodeKey           string   `json:"steps_node_key"`
	SeedNodeKey            string   `json:"seed_node_key"`
}

// UnmarshalJSON decodes a ColabOneComfyPipelineSpec with unknown fields forbidden and
// Pydantic-parity defaults when fields are omitted.
func (p *ColabOneComfyPipelineSpec) UnmarshalJSON(data []byte) error {
	var raw struct {
		Kind                   *string   `json:"kind"`
		VideoBackend           *string   `json:"video_backend"`
		DefaultTtsEngine       *string   `json:"default_tts_engine"`
		SupportedTtsEngines    []*string `json:"supported_tts_engines"`
		MaxSegmentSeconds      *float64  `json:"max_segment_seconds"`
		TailFrameOffsetSeconds *float64  `json:"tail_frame_offset_seconds"`
		OutputFPS              *int      `json:"output_fps"`
		TargetWidth            *int      `json:"target_width"`
		TargetHeight           *int      `json:"target_height"`
		UpscaleMode            *string   `json:"upscale_mode"`
		AudioNodeKey           string    `json:"audio_node_key"`
		ImageNodeKey           string    `json:"image_node_key"`
		PromptNodeKey          string    `json:"prompt_node_key"`
		FramesNodeKey          string    `json:"frames_node_key"`
		WidthNodeKey           string    `json:"width_node_key"`
		HeightNodeKey          string    `json:"height_node_key"`
		FPSNodeKey             string    `json:"fps_node_key"`
		StepsNodeKey           string    `json:"steps_node_key"`
		SeedNodeKey            string    `json:"seed_node_key"`
	}
	if err := decodeStrictJSONObject(data, &raw); err != nil {
		return err
	}

	kind := "comfy_standard"
	if raw.Kind != nil {
		if *raw.Kind == "" {
			return errors.New("流水线类型不合法")
		}
		kind = *raw.Kind
	}
	videoBackend := "custom"
	if raw.VideoBackend != nil {
		if *raw.VideoBackend == "" {
			return errors.New("视频生成后端不合法")
		}
		videoBackend = *raw.VideoBackend
	}
	defaultTtsEngine := "voxcpm2"
	if raw.DefaultTtsEngine != nil {
		defaultTtsEngine = *raw.DefaultTtsEngine
	}
	supportedTtsEngines := make([]string, 0, len(raw.SupportedTtsEngines))
	for _, eng := range raw.SupportedTtsEngines {
		if eng == nil {
			return errors.New("字段 supported_tts_engines 不能为空值")
		}
		supportedTtsEngines = append(supportedTtsEngines, *eng)
	}
	maxSegmentSeconds := 45.0
	if raw.MaxSegmentSeconds != nil {
		if *raw.MaxSegmentSeconds == 0 {
			return errors.New("分段时长必须在 3.0 到 300.0 秒之间")
		}
		maxSegmentSeconds = *raw.MaxSegmentSeconds
	}
	tailFrameOffsetSeconds := 0.08
	if raw.TailFrameOffsetSeconds != nil {
		tailFrameOffsetSeconds = *raw.TailFrameOffsetSeconds
	}
	outputFPS := 25
	if raw.OutputFPS != nil {
		if *raw.OutputFPS == 0 {
			return errors.New("输出帧率必须在 1 到 60 之间")
		}
		outputFPS = *raw.OutputFPS
	}
	targetWidth := 1280
	if raw.TargetWidth != nil {
		if *raw.TargetWidth == 0 {
			return errors.New("目标宽度必须在 64 到 4096 之间")
		}
		targetWidth = *raw.TargetWidth
	}
	targetHeight := 720
	if raw.TargetHeight != nil {
		if *raw.TargetHeight == 0 {
			return errors.New("目标高度必须在 64 到 4096 之间")
		}
		targetHeight = *raw.TargetHeight
	}
	upscaleMode := "none"
	if raw.UpscaleMode != nil {
		if *raw.UpscaleMode == "" {
			return errors.New("超分模式不合法")
		}
		upscaleMode = *raw.UpscaleMode
	}

	*p = ColabOneComfyPipelineSpec{
		Kind:                   kind,
		VideoBackend:           videoBackend,
		DefaultTtsEngine:       defaultTtsEngine,
		SupportedTtsEngines:    supportedTtsEngines,
		MaxSegmentSeconds:      maxSegmentSeconds,
		TailFrameOffsetSeconds: tailFrameOffsetSeconds,
		OutputFPS:              outputFPS,
		TargetWidth:            targetWidth,
		TargetHeight:           targetHeight,
		UpscaleMode:            upscaleMode,
		AudioNodeKey:           raw.AudioNodeKey,
		ImageNodeKey:           raw.ImageNodeKey,
		PromptNodeKey:          raw.PromptNodeKey,
		FramesNodeKey:          raw.FramesNodeKey,
		WidthNodeKey:           raw.WidthNodeKey,
		HeightNodeKey:          raw.HeightNodeKey,
		FPSNodeKey:             raw.FPSNodeKey,
		StepsNodeKey:           raw.StepsNodeKey,
		SeedNodeKey:            raw.SeedNodeKey,
	}
	return nil
}

// Validate normalizes and validates a ColabOneComfyPipelineSpec.
func (p *ColabOneComfyPipelineSpec) Validate() error {
	if p.Kind == "" {
		p.Kind = "comfy_standard"
	}
	if !allowedPipelineKinds[p.Kind] {
		return errors.New("流水线类型不合法")
	}
	if p.VideoBackend == "" {
		p.VideoBackend = "custom"
	}
	if !allowedVideoBackends[p.VideoBackend] {
		return errors.New("视频生成后端不合法")
	}
	if p.DefaultTtsEngine != "" && !workflowIDRe.MatchString(p.DefaultTtsEngine) {
		return fmt.Errorf("非法的 TTS 引擎 ID: %q", p.DefaultTtsEngine)
	}
	if len(p.SupportedTtsEngines) > 16 {
		return errors.New("支持的 TTS 引擎数量不能超过 16 个")
	}
	for _, engineID := range p.SupportedTtsEngines {
		if !workflowIDRe.MatchString(engineID) {
			return fmt.Errorf("非法的 TTS 引擎 ID: %q", engineID)
		}
	}
	if p.SupportedTtsEngines == nil {
		p.SupportedTtsEngines = []string{}
	}
	if p.MaxSegmentSeconds == 0 {
		p.MaxSegmentSeconds = 45.0
	}
	if !isFiniteFloat64(p.MaxSegmentSeconds) || p.MaxSegmentSeconds < 3.0 || p.MaxSegmentSeconds > 300.0 {
		return errors.New("分段时长必须在 3.0 到 300.0 秒之间")
	}
	if !isFiniteFloat64(p.TailFrameOffsetSeconds) || p.TailFrameOffsetSeconds < 0.0 || p.TailFrameOffsetSeconds > 1.0 {
		return errors.New("尾帧回退偏移必须在 0.0 到 1.0 秒之间")
	}
	if p.OutputFPS == 0 {
		p.OutputFPS = 25
	}
	if p.OutputFPS < 1 || p.OutputFPS > 60 {
		return errors.New("输出帧率必须在 1 到 60 之间")
	}
	if p.TargetWidth == 0 {
		p.TargetWidth = 1280
	}
	if p.TargetWidth < 64 || p.TargetWidth > 4096 {
		return errors.New("目标宽度必须在 64 到 4096 之间")
	}
	if p.TargetHeight == 0 {
		p.TargetHeight = 720
	}
	if p.TargetHeight < 64 || p.TargetHeight > 4096 {
		return errors.New("目标高度必须在 64 到 4096 之间")
	}
	if p.UpscaleMode == "" {
		p.UpscaleMode = "none"
	}
	if !allowedUpscaleModes[p.UpscaleMode] {
		return errors.New("超分模式不合法")
	}

	for _, nodeKey := range []string{
		p.AudioNodeKey,
		p.ImageNodeKey,
		p.PromptNodeKey,
		p.FramesNodeKey,
		p.WidthNodeKey,
		p.HeightNodeKey,
		p.FPSNodeKey,
		p.StepsNodeKey,
		p.SeedNodeKey,
	} {
		if nodeKey != "" {
			if _, _, err := validateFieldKey(nodeKey); err != nil {
				return err
			}
		}
	}
	return nil
}

// ColabOneWorkflowField describes an editable parameter extracted from or declared on a workflow.
type ColabOneWorkflowField struct {
	Key          string            `json:"key"`
	Label        string            `json:"label"`
	NodeID       string            `json:"node_id"`
	InputName    string            `json:"input_name"`
	FieldType    string            `json:"field_type"`
	Default      any               `json:"default"`
	Options      []string          `json:"options"`
	MinValue     *float64          `json:"min_value"`
	MaxValue     *float64          `json:"max_value"`
	Step         *float64          `json:"step"`
	Description  string            `json:"description"`
	Group        string            `json:"group"`
	Required     bool              `json:"required"`
	VisibleWhen  map[string]any    `json:"visible_when"`
	OptionLabels map[string]string `json:"option_labels"`
}

// UnmarshalJSON decodes a ColabOneWorkflowField with unknown fields forbidden.
func (f *ColabOneWorkflowField) UnmarshalJSON(data []byte) error {
	var raw struct {
		Key          string             `json:"key"`
		Label        string             `json:"label"`
		NodeID       string             `json:"node_id"`
		InputName    string             `json:"input_name"`
		FieldType    string             `json:"field_type"`
		Default      any                `json:"default"`
		Options      []*string          `json:"options"`
		MinValue     *float64           `json:"min_value"`
		MaxValue     *float64           `json:"max_value"`
		Step         *float64           `json:"step"`
		Description  string             `json:"description"`
		Group        string             `json:"group"`
		Required     bool               `json:"required"`
		VisibleWhen  map[string]any     `json:"visible_when"`
		OptionLabels map[string]*string `json:"option_labels"`
	}
	if err := decodeStrictJSONObject(data, &raw, "default", "min_value", "max_value", "step", "visible_when"); err != nil {
		return err
	}
	options := make([]string, 0, len(raw.Options))
	for _, opt := range raw.Options {
		if opt == nil {
			return errors.New("字段 options 不能为空值")
		}
		options = append(options, *opt)
	}
	optionLabels := make(map[string]string, len(raw.OptionLabels))
	if len(raw.OptionLabels) > 0 {
		keys := make([]string, 0, len(raw.OptionLabels))
		for k := range raw.OptionLabels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := raw.OptionLabels[k]
			if v == nil {
				return errors.New("字段 option_labels 不能为空值")
			}
			optionLabels[k] = *v
		}
	}
	*f = ColabOneWorkflowField{
		Key:          raw.Key,
		Label:        raw.Label,
		NodeID:       raw.NodeID,
		InputName:    raw.InputName,
		FieldType:    raw.FieldType,
		Default:      raw.Default,
		Options:      options,
		MinValue:     raw.MinValue,
		MaxValue:     raw.MaxValue,
		Step:         raw.Step,
		Description:  raw.Description,
		Group:        raw.Group,
		Required:     raw.Required,
		VisibleWhen:  raw.VisibleWhen,
		OptionLabels: optionLabels,
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
	if utf8.RuneCountInString(f.Description) > 400 {
		return errors.New("字段描述长度不能超过 400")
	}
	if utf8.RuneCountInString(f.Group) > 64 {
		return errors.New("字段分组名称长度不能超过 64")
	}
	if f.VisibleWhen != nil {
		keys := make([]string, 0, len(f.VisibleWhen))
		for k := range f.VisibleWhen {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, _, err := validateFieldKey(k); err != nil {
				return err
			}
			switch f.VisibleWhen[k].(type) {
			case string, bool:
			default:
				return fmt.Errorf("visible_when 条件 %s 的值必须为字符串或布尔值", k)
			}
		}
	}
	if f.Key != f.NodeID+"."+f.InputName {
		return errors.New("字段 key 必须为 {node_id}.{input_name}")
	}
	parsedNodeID, parsedInputName, err := validateFieldKey(f.Key)
	if err != nil {
		return err
	}
	if parsedNodeID != f.NodeID || parsedInputName != f.InputName {
		return fmt.Errorf("非法字段键: %q", f.Key)
	}
	if f.MinValue != nil && f.MaxValue != nil && *f.MinValue > *f.MaxValue {
		return errors.New("min_value 不能大于 max_value")
	}
	if f.Options == nil {
		f.Options = []string{}
	}
	if f.OptionLabels == nil {
		f.OptionLabels = map[string]string{}
	}
	return nil
}

// ColabOneWorkflowTemplate represents a built-in or user-imported ComfyUI workflow template.
type ColabOneWorkflowTemplate struct {
	ID             string                     `json:"id"`
	Name           string                     `json:"name"`
	Description    string                     `json:"description"`
	Source         string                     `json:"source"`
	MinVRAMMib     int                        `json:"min_vram_mib"`
	MinDiskMib     int                        `json:"min_disk_mib"`
	RecommendedGPU string                     `json:"recommended_gpu"`
	License        string                     `json:"license"`
	CommercialOK   bool                       `json:"commercial_ok"`
	LicenseNotice  string                     `json:"license_notice"`
	CreatedAt      float64                    `json:"created_at"`
	UpdatedAt      float64                    `json:"updated_at"`
	CustomNodes    []ColabOneComfyNodeSpec    `json:"custom_nodes"`
	NodePatches    []ColabOneComfyNodePatch   `json:"node_patches"`
	PipPackages    []string                   `json:"pip_packages"`
	Models         []ColabOneComfyModelRef    `json:"models"`
	Pipeline       *ColabOneComfyPipelineSpec `json:"pipeline"`
	Workflow       map[string]any             `json:"workflow"`
	Fields         []ColabOneWorkflowField    `json:"fields"`
	NodeCount      int                        `json:"node_count"`
}

// UnmarshalJSON decodes a ColabOneWorkflowTemplate with unknown fields forbidden.
func (t *ColabOneWorkflowTemplate) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID             string                     `json:"id"`
		Name           string                     `json:"name"`
		Description    string                     `json:"description"`
		Source         *string                    `json:"source"`
		MinVRAMMib     int                        `json:"min_vram_mib"`
		MinDiskMib     int                        `json:"min_disk_mib"`
		RecommendedGPU string                     `json:"recommended_gpu"`
		License        string                     `json:"license"`
		CommercialOK   *bool                      `json:"commercial_ok"`
		LicenseNotice  string                     `json:"license_notice"`
		CreatedAt      float64                    `json:"created_at"`
		UpdatedAt      float64                    `json:"updated_at"`
		CustomNodes    []ColabOneComfyNodeSpec    `json:"custom_nodes"`
		NodePatches    []ColabOneComfyNodePatch   `json:"node_patches"`
		PipPackages    []*string                  `json:"pip_packages"`
		Models         []ColabOneComfyModelRef    `json:"models"`
		Pipeline       *ColabOneComfyPipelineSpec `json:"pipeline"`
		Workflow       map[string]any             `json:"workflow"`
		Fields         []ColabOneWorkflowField    `json:"fields"`
		NodeCount      int                        `json:"node_count"`
	}
	if err := decodeStrictJSONObject(data, &raw, "pipeline"); err != nil {
		return err
	}
	source := "custom"
	if raw.Source != nil {
		if *raw.Source == "" {
			return errors.New("工作流来源不合法")
		}
		source = *raw.Source
	}
	commercialOK := true
	if raw.CommercialOK != nil {
		commercialOK = *raw.CommercialOK
	}
	pipPackages := make([]string, 0, len(raw.PipPackages))
	for _, pkg := range raw.PipPackages {
		if pkg == nil {
			return errors.New("字段 pip_packages 不能为空值")
		}
		pipPackages = append(pipPackages, *pkg)
	}
	customNodes := raw.CustomNodes
	if customNodes == nil {
		customNodes = []ColabOneComfyNodeSpec{}
	}
	nodePatches := raw.NodePatches
	if nodePatches == nil {
		nodePatches = []ColabOneComfyNodePatch{}
	}
	models := raw.Models
	if models == nil {
		models = []ColabOneComfyModelRef{}
	}
	fields := raw.Fields
	if fields == nil {
		fields = []ColabOneWorkflowField{}
	}
	*t = ColabOneWorkflowTemplate{
		ID:             raw.ID,
		Name:           raw.Name,
		Description:    raw.Description,
		Source:         source,
		MinVRAMMib:     raw.MinVRAMMib,
		MinDiskMib:     raw.MinDiskMib,
		RecommendedGPU: raw.RecommendedGPU,
		License:        raw.License,
		CommercialOK:   commercialOK,
		LicenseNotice:  raw.LicenseNotice,
		CreatedAt:      raw.CreatedAt,
		UpdatedAt:      raw.UpdatedAt,
		CustomNodes:    customNodes,
		NodePatches:    nodePatches,
		PipPackages:    pipPackages,
		Models:         models,
		Pipeline:       raw.Pipeline,
		Workflow:       raw.Workflow,
		Fields:         fields,
		NodeCount:      raw.NodeCount,
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
	if t.MinDiskMib < 0 {
		return errors.New("最低磁盘空间需求不能为负数")
	}
	if utf8.RuneCountInString(t.RecommendedGPU) > 120 {
		return errors.New("推荐 GPU 描述长度不能超过 120")
	}
	if utf8.RuneCountInString(t.License) > 160 {
		return errors.New("工作流许可协议长度不能超过 160")
	}
	if utf8.RuneCountInString(t.LicenseNotice) > 600 {
		return errors.New("工作流许可协议提示长度不能超过 600")
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

	if len(t.NodePatches) > 16 {
		return errors.New("节点补丁数量不能超过 16 个")
	}
	for i := range t.NodePatches {
		if err := t.NodePatches[i].Validate(); err != nil {
			return err
		}
	}
	if t.NodePatches == nil {
		t.NodePatches = []ColabOneComfyNodePatch{}
	}

	cleanedPip, err := validateSafePipPackages(t.PipPackages)
	if err != nil {
		return err
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

	if t.Pipeline != nil {
		if err := t.Pipeline.Validate(); err != nil {
			return err
		}
	}

	if t.Source != "builtin" && (len(t.CustomNodes) > 0 || len(t.PipPackages) > 0 || len(t.NodePatches) > 0 || len(t.Models) > 0 || t.Pipeline != nil) {
		return errors.New("自定义工作流模板不允许声明远端依赖包、模型下载或流水线配置")
	}

	if len(t.NodePatches) > 0 {
		customNodeNames := make(map[string]struct{}, len(t.CustomNodes))
		for _, cn := range t.CustomNodes {
			customNodeNames[cn.Name] = struct{}{}
		}
		for _, patch := range t.NodePatches {
			if _, ok := customNodeNames[patch.NodeName]; !ok {
				return fmt.Errorf("节点补丁引用的自定义节点不存在: %s", patch.NodeName)
			}
		}
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
	HFToken    string            `json:"hf_token,omitempty"`
}

// String redacts HFToken so logging a ColabOneWorkflowRunRequest never leaks credentials.
func (r ColabOneWorkflowRunRequest) String() string {
	return fmt.Sprintf("ColabOneWorkflowRunRequest{WorkflowID:%q, Overrides:%v, Inputs:%v, HFToken:[redacted]}", r.WorkflowID, r.Overrides, r.Inputs)
}

// GoString redacts HFToken when formatted with %#v.
func (r ColabOneWorkflowRunRequest) GoString() string {
	return r.String()
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
	cleanedTok, err := validateCleanHFToken(r.HFToken)
	if err != nil {
		return err
	}
	r.HFToken = cleanedTok
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

// ColabOneComfyCatalog describes the pinned ComfyUI repository, ports, built-in templates, and TTS engines.
type ColabOneComfyCatalog struct {
	Repo       string                       `json:"repo"`
	Tag        string                       `json:"tag"`
	Commit     string                       `json:"commit"`
	Port       int                          `json:"port"`
	ProxyPort  int                          `json:"proxy_port"`
	Templates  []ColabOneWorkflowTemplate   `json:"templates"`
	TtsEngines []ColabOneComfyTtsEngineSpec `json:"tts_engines"`
}

// UnmarshalJSON decodes a ColabOneComfyCatalog with unknown fields forbidden and
// default ports 8188 / 8189 when omitted.
func (c *ColabOneComfyCatalog) UnmarshalJSON(data []byte) error {
	var raw struct {
		Repo       string                       `json:"repo"`
		Tag        string                       `json:"tag"`
		Commit     string                       `json:"commit"`
		Port       *int                         `json:"port"`
		ProxyPort  *int                         `json:"proxy_port"`
		Templates  []ColabOneWorkflowTemplate   `json:"templates"`
		TtsEngines []ColabOneComfyTtsEngineSpec `json:"tts_engines"`
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
	templates := raw.Templates
	if templates == nil {
		templates = []ColabOneWorkflowTemplate{}
	}
	ttsEngines := raw.TtsEngines
	if ttsEngines == nil {
		ttsEngines = []ColabOneComfyTtsEngineSpec{}
	}
	*c = ColabOneComfyCatalog{
		Repo:       raw.Repo,
		Tag:        raw.Tag,
		Commit:     raw.Commit,
		Port:       port,
		ProxyPort:  proxyPort,
		Templates:  templates,
		TtsEngines: ttsEngines,
	}
	return nil
}

// Validate normalizes and validates a ColabOneComfyCatalog, all embedded templates, and TTS engines.
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

	if c.TtsEngines == nil {
		c.TtsEngines = []ColabOneComfyTtsEngineSpec{}
	}
	seenEngineIDs := make(map[string]struct{}, len(c.TtsEngines))
	for i := range c.TtsEngines {
		if err := c.TtsEngines[i].Validate(); err != nil {
			return err
		}
		id := c.TtsEngines[i].ID
		if _, dup := seenEngineIDs[id]; dup {
			return errors.New("内置 TTS 引擎 ID 重复")
		}
		seenEngineIDs[id] = struct{}{}
	}

	if len(c.TtsEngines) > 0 {
		for _, tpl := range c.Templates {
			if tpl.Pipeline == nil {
				continue
			}
			if tpl.Pipeline.DefaultTtsEngine != "" {
				if _, ok := seenEngineIDs[tpl.Pipeline.DefaultTtsEngine]; !ok {
					return fmt.Errorf("工作流模板 %s 引用了未定义的默认 TTS 引擎: %s", tpl.ID, tpl.Pipeline.DefaultTtsEngine)
				}
			}
			for _, engineID := range tpl.Pipeline.SupportedTtsEngines {
				if _, ok := seenEngineIDs[engineID]; !ok {
					return fmt.Errorf("工作流模板 %s 引用了未定义的 TTS 引擎: %s", tpl.ID, engineID)
				}
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Core pure workflow functions
// ---------------------------------------------------------------------------

// ValidateComfyInputUpload validates a ComfyUI input media upload payload locally
// before transmission: safe basename (no path separators or traversal), supported
// extension in AllowedInputExtensions, valid non-empty base64 (optionally wrapped
// in a data: URI), and exact 16 MiB image / 64 MiB audio byte caps.
func ValidateComfyInputUpload(filename, dataB64 string) error {
	name := strings.TrimSpace(filename)
	if name == "" ||
		utf8.RuneCountInString(name) > 255 ||
		name == "." ||
		name == ".." ||
		strings.ContainsAny(name, `/\`+"\x00") {
		return errors.New("输入文件名不合法")
	}
	dotExt := path.Ext(name)
	ext := ""
	if dotExt != "" && len(name) > len(dotExt) {
		ext = strings.ToLower(strings.TrimPrefix(dotExt, "."))
	}
	if ext == "" || !AllowedInputExtensions[ext] {
		displayExt := ext
		if displayExt == "" {
			displayExt = "未知"
		}
		return fmt.Errorf("不支持的输入文件格式：%s", displayExt)
	}

	maxBytes := MaxAudioInputBytes
	if imageInputExtensions[ext] {
		maxBytes = MaxImageInputBytes
	}
	limitMB := maxBytes / (1024 * 1024)

	payload := strings.TrimSpace(dataB64)
	if payload == "" {
		return errors.New("输入文件内容为空")
	}
	if strings.HasPrefix(payload, "data:") {
		_, after, ok := strings.Cut(payload, ",")
		if !ok {
			return errors.New("输入文件编码格式无效")
		}
		payload = after
	}

	maxEncodedLen := maxBytes + maxBytes/2 + 4096
	if len(payload) > maxEncodedLen {
		return fmt.Errorf("输入文件超过大小限制（最大 %d MB）", limitMB)
	}

	clean := payload
	if strings.IndexFunc(payload, unicode.IsSpace) >= 0 {
		clean = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, payload)
	}
	if clean == "" {
		return errors.New("输入文件内容为空")
	}
	if len(clean)%4 != 0 {
		return errors.New("输入文件 Base64 编码无效")
	}

	const b64ChunkSize = 4096 // multiple of 4
	var decodeBuf [(b64ChunkSize / 4) * 3]byte
	totalDecoded := 0
	for offset := 0; offset < len(clean); offset += b64ChunkSize {
		end := offset + b64ChunkSize
		isLast := end >= len(clean)
		if isLast {
			end = len(clean)
		}
		chunk := clean[offset:end]
		if !isLast && strings.IndexByte(chunk, '=') >= 0 {
			return errors.New("输入文件 Base64 编码无效")
		}
		n, err := base64.StdEncoding.Decode(decodeBuf[:], []byte(chunk))
		if err != nil {
			return errors.New("输入文件 Base64 编码无效")
		}
		totalDecoded += n
		if totalDecoded > maxBytes {
			return fmt.Errorf("输入文件超过大小限制（最大 %d MB）", limitMB)
		}
	}
	if totalDecoded == 0 {
		return errors.New("输入文件内容为空")
	}
	return nil
}

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
		if errors.Is(err, errWorkflowNestingTooDeep) {
			return nil, err
		}
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
		if nodeID != "pipeline" && !nodeIDRe.MatchString(nodeID) {
			continue
		}
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
							Key:          key,
							Label:        trimLabel(title + " · 参考图片"),
							NodeID:       nodeID,
							InputName:    inputName,
							FieldType:    "image",
							Default:      strVal,
							Options:      []string{},
							OptionLabels: map[string]string{},
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
							Key:          key,
							Label:        trimLabel(title + " · 输入音频"),
							NodeID:       nodeID,
							InputName:    inputName,
							FieldType:    "audio",
							Default:      strVal,
							Options:      []string{},
							OptionLabels: map[string]string{},
						},
					})
					continue
				}

				if !excludedTextInputNames[nameLower] &&
					(strings.Contains(ctLower, "text") ||
						strings.Contains(ctLower, "prompt") ||
						strings.Contains(ctLower, "string") ||
						textInputNames[nameLower]) {
					label, priority := promptLabelAndPriority(title, ctLower, nameLower)
					nodeFields = append(nodeFields, candidateField{
						priority:  priority,
						inputName: inputName,
						field: ColabOneWorkflowField{
							Key:          key,
							Label:        label,
							NodeID:       nodeID,
							InputName:    inputName,
							FieldType:    "textarea",
							Default:      strVal,
							Options:      []string{},
							OptionLabels: map[string]string{},
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
							Key:          key,
							Label:        trimLabel(title + " · " + spec.cnName),
							NodeID:       nodeID,
							InputName:    inputName,
							FieldType:    spec.fieldType,
							Default:      defaultVal,
							Options:      []string{},
							MinValue:     copyFloatPtr(spec.minValue),
							MaxValue:     copyFloatPtr(spec.maxValue),
							Step:         copyFloatPtr(spec.step),
							OptionLabels: map[string]string{},
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
			existing := inputs[inputName]
			val := overrides[key]
			switch v := val.(type) {
			case bool:
				inputs[inputName] = v
			case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
				inputs[inputName] = v
			case float32:
				f := float64(v)
				if !isFiniteFloat64(f) {
					return nil, fmt.Errorf("字段 %s 包含非法浮点值", key)
				}
				if isStrictIntValue(existing) {
					if n, ok := coerceIntegralFloatToInt64(f); ok {
						inputs[inputName] = n
						break
					}
				}
				inputs[inputName] = v
			case float64:
				if !isFiniteFloat64(v) {
					return nil, fmt.Errorf("字段 %s 包含非法浮点值", key)
				}
				if isStrictIntValue(existing) {
					if n, ok := coerceIntegralFloatToInt64(v); ok {
						inputs[inputName] = n
						break
					}
				}
				inputs[inputName] = v
			case json.Number:
				if n, err := strconv.ParseInt(string(v), 10, 64); err == nil {
					inputs[inputName] = n
				} else if u, err := strconv.ParseUint(string(v), 10, 64); err == nil {
					inputs[inputName] = u
				} else if f, err := strconv.ParseFloat(string(v), 64); err == nil && isFiniteFloat64(f) {
					if isStrictIntValue(existing) {
						if n, ok := coerceIntegralFloatToInt64(f); ok {
							inputs[inputName] = n
							break
						}
					}
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
