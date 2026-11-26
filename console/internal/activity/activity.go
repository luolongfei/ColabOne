// Package activity provides a bounded, credential-free activity feed and rotating
// disk log for ColabOne. Never record chat text or raw HTTP bodies.
package activity

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultLimit is the default maximum number of entries retained in memory.
	DefaultLimit = 300
	// DefaultMaxBytes is the default disk log rotation threshold (256 KiB).
	DefaultMaxBytes int64 = 256 * 1024
	// MinMaxBytes is the minimum enforced disk log rotation threshold (16 KiB).
	MinMaxBytes int64 = 16 * 1024

	// DefaultSource is used when an activity entry has no explicit source.
	DefaultSource = "客户端"
	// DefaultLevel is used when an activity entry has no explicit severity level.
	DefaultLevel = "info"

	maxMessageRunes = 600
	maxSourceRunes  = 40
	maxLevelRunes   = 16

	redactedURL        = "[链接已隐藏]"
	redactedBearer     = "Bearer [已隐藏]"
	redactedBasic      = "Basic [已隐藏]"
	redactedValue      = "[已隐藏]"
	redactedCredential = "[凭据已隐藏]"

	// unicodeSpace mirrors Python 3's Unicode-aware \s character class in Go RE2.
	unicodeSpace = `\t\n\v\f\r\x1c-\x1f\x85\p{Z}`
)

var (
	urlPattern = regexp.MustCompile(
		`(?i)(?:https?|wss?|rtmps?|socks5h?|socks4a?)://` +
			`(?:[^` + unicodeSpace + `<>"'/\?#]+@` +
			`(?:\[[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*(?:%[0-9A-Za-z._~%-]+)?\]` +
			`|[^` + unicodeSpace + `<>"'\)\]/\?#@:;,.][^` + unicodeSpace + `<>"'\)\]/\?#]*)` +
			`[^` + unicodeSpace + `<>"'\)\]]*` +
			`|\[[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*(?:%[0-9A-Za-z._~%-]+)?\][^` + unicodeSpace + `<>"'\)\]]*` +
			`|[^` + unicodeSpace + `<>"'\)\]]+)`,
	)
	basicTokenInQuotedPattern = regexp.MustCompile(
		`(?i)\bBasic[` + unicodeSpace + `]+[^` + unicodeSpace + `,;]+`,
	)
	credentialPattern = regexp.MustCompile(
		`(?:ya29\.[\w.-]+|1//[\w./+-]{12,}|hf_[\w-]+|co_web_[\w-]{20,}|co_[\w-]{40,}|\bsk-[A-Za-z0-9._-]{10,}\b|eyJ[A-Za-z0-9_+/=-]+\.eyJ[A-Za-z0-9_+/=-]+\.[A-Za-z0-9_+/=-]+|eyJ[A-Za-z0-9_+/=-]{20,}|4/[\w./+%-]{12,})`,
	)

	diskLineReplacer = strings.NewReplacer("\r", " ", "\n", " ")
)

// SafeMessage redacts URLs, Bearer/Basic auth credentials, key-value secrets,
// and known OAuth/HF/API/JWT credentials from message, then safely truncates
// to 600 runes.
func SafeMessage(message string) string {
	text := redactURLs(message)
	text = redactBearer(text)
	text = redactBasicAuth(text)
	text = redactKeyValueSecrets(text)
	text = redactCredentials(text)
	return truncateRunes(text, maxMessageRunes)
}

func redactCredentials(text string) string {
	if !strings.Contains(text, "ya29.") &&
		!strings.Contains(text, "1//") &&
		!strings.Contains(text, "hf_") &&
		!strings.Contains(text, "co_") &&
		!strings.Contains(text, "sk-") &&
		!strings.Contains(text, "eyJ") &&
		!strings.Contains(text, "4/") {
		return text
	}
	return credentialPattern.ReplaceAllLiteralString(text, redactedCredential)
}

// ColabOneSafeMessage is the ColabOne-branded alias of SafeMessage.
func ColabOneSafeMessage(message string) string {
	return SafeMessage(message)
}

func isUnicodeSpaceRune(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

func isASCIIUnicodeSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\v' || b == '\f' || b == '\r' || (b >= 0x1c && b <= 0x1f)
}

func skipUnicodeSpace(s string, pos int) int {
	for pos < len(s) {
		b := s[pos]
		if b < utf8.RuneSelf {
			if isASCIIUnicodeSpace(b) {
				pos++
				continue
			}
			break
		}
		r, size := utf8.DecodeRuneInString(s[pos:])
		if !isUnicodeSpaceRune(r) {
			break
		}
		pos += size
	}
	return pos
}

func isQuotedStart(s string, pos int) bool {
	if pos >= len(s) {
		return false
	}
	b := s[pos]
	if b == '"' || b == '\'' {
		return true
	}
	return b == '\\' && pos+1 < len(s) && (s[pos+1] == '"' || s[pos+1] == '\'')
}

// scanQuotedString parses a single quoted string literal starting at pos using
// double quotes ("), single quotes ('), or escaped quotes (\" / \').
// If the literal is terminated before EOL/EOF, closeDelim is non-empty and
// nextPos points right after closeDelim; otherwise closeDelim is empty and
// nextPos points to the truncation boundary.
func scanQuotedString(s string, pos int) (openDelim, content, closeDelim string, nextPos int, ok bool) {
	if pos >= len(s) {
		return "", "", "", pos, false
	}

	if s[pos] == '\\' && pos+1 < len(s) && (s[pos+1] == '"' || s[pos+1] == '\'') {
		q := s[pos+1]
		openDelim = s[pos : pos+2]
		start := pos + 2
		i := start
		for i < len(s) {
			b := s[i]
			if b == '\r' || b == '\n' || b == q {
				return openDelim, s[start:i], "", i, true
			}
			if b == '\\' {
				if i+3 < len(s) && s[i+1] == '\\' && s[i+2] == '\\' && (s[i+3] == '\\' || s[i+3] == q) {
					i += 4
					continue
				}
				if i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					continue
				}
				if i+1 < len(s) && s[i+1] == q {
					return openDelim, s[start:i], s[i : i+2], i + 2, true
				}
				if i+1 < len(s) && s[i+1] != '\r' && s[i+1] != '\n' {
					i += 2
					continue
				}
				return openDelim, s[start:i], "", i, true
			}
			i++
		}
		return openDelim, s[start:i], "", i, true
	}

	if s[pos] == '"' || s[pos] == '\'' {
		q := s[pos]
		openDelim = s[pos : pos+1]
		start := pos + 1
		i := start
		for i < len(s) {
			b := s[i]
			if b == '\r' || b == '\n' {
				return openDelim, s[start:i], "", i, true
			}
			if b == '\\' {
				if i+1 < len(s) && s[i+1] != '\r' && s[i+1] != '\n' {
					i += 2
					continue
				}
				return openDelim, s[start:i], "", i, true
			}
			if b == q {
				return openDelim, s[start:i], s[i : i+1], i + 1, true
			}
			i++
		}
		return openDelim, s[start:i], "", i, true
	}

	return "", "", "", pos, false
}

type bracketFrame struct {
	openIdx       int
	allowNewlines bool
	braceDepth    int
	startNLEpoch  int
}

type bracketOutcomeKind uint8

const (
	outcomeNone bracketOutcomeKind = iota
	outcomeMatched
	outcomeUnbalancedClose
	outcomeBraceClose
	outcomeNewline
	outcomeNewlineAtChild
	outcomeAbort
)

type bracketOutcome struct {
	kind            bracketOutcomeKind
	pos             int
	extraBraceDepth int
	hasNewline      bool
}

type drop1Entry struct {
	key   int64
	level int
}

type quoteScanCacheEntry struct {
	closed  bool
	nextPos int
}

// bracketMatcher finds matching ']' indices for '[' container openers in
// strictly linear O(N) amortized time across an input string while skipping
// quoted elements, tracking per-frame newline/brace rules, and memoizing
// root states, brace-drop transitions, and nested bracket outcomes.
type bracketMatcher struct {
	text       string
	rootResult map[int]int
	outcomes   map[int]bracketOutcome
	rootMemo   map[int64]int
	drop1Memo  map[int64]int
	quoteCache map[int]quoteScanCacheEntry
}

func matcherStateKey(pos int, allowNewlines, atElemStart bool) int64 {
	k := int64(pos) << 2
	if allowNewlines {
		k |= 2
	}
	if atElemStart {
		k |= 1
	}
	return k
}

func isJSONScalarBoundary(s string, pos int) bool {
	if pos >= len(s) {
		return true
	}
	b := s[pos]
	switch b {
	case ',', ']', '}':
		return true
	}
	if b < utf8.RuneSelf {
		return isASCIIUnicodeSpace(b)
	}
	r, _ := utf8.DecodeRuneInString(s[pos:])
	return isUnicodeSpaceRune(r)
}

// scanJSONBareScalar parses an RFC 8259 JSON bare scalar (null, true, false,
// or a JSON number) starting at pos and followed by a valid JSON boundary.
func scanJSONBareScalar(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return pos, false
	}
	for _, kw := range []string{"null", "true", "false"} {
		if strings.HasPrefix(s[pos:], kw) {
			end := pos + len(kw)
			if isJSONScalarBoundary(s, end) {
				return end, true
			}
			return pos, false
		}
	}

	i := pos
	if s[i] == '-' {
		i++
		if i >= len(s) {
			return pos, false
		}
	}
	if s[i] == '0' {
		i++
	} else if s[i] >= '1' && s[i] <= '9' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	} else {
		return pos, false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return pos, false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return pos, false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if !isJSONScalarBoundary(s, i) {
		return pos, false
	}
	return i, true
}

func containerAllowsNewlines(text string, openIdx int) bool {
	firstNonSpace := skipUnicodeSpace(text, openIdx+1)
	if isQuotedStart(text, firstNonSpace) {
		return true
	}
	if end, ok := scanJSONBareScalar(text, firstNonSpace); ok {
		after := skipUnicodeSpace(text, end)
		return after < len(text) && (text[after] == ',' || text[after] == ']')
	}
	if markerLen := matchRedactionPlaceholderPrefix(text[firstNonSpace:]); markerLen > 0 {
		after := skipUnicodeSpace(text, firstNonSpace+markerLen)
		return after < len(text) && (text[after] == ',' || text[after] == ']')
	}
	return false
}

func (m *bracketMatcher) setRootResult(openIdx, closeIdx int) {
	if m.rootResult == nil {
		m.rootResult = make(map[int]int)
	}
	m.rootResult[openIdx] = closeIdx
}

func (m *bracketMatcher) setOutcome(openIdx int, out bracketOutcome) {
	if m.outcomes == nil {
		m.outcomes = make(map[int]bracketOutcome)
	}
	m.outcomes[openIdx] = out
}

func (m *bracketMatcher) scanQuoteCached(pos int) (closed bool, nextPos int, ok bool) {
	if m.quoteCache != nil {
		if entry, exists := m.quoteCache[pos]; exists {
			return entry.closed, entry.nextPos, true
		}
	}
	_, _, closeDelim, nextPos, ok := scanQuotedString(m.text, pos)
	if !ok {
		return false, pos, false
	}
	closed = closeDelim != ""
	if nextPos-pos > 64 {
		if m.quoteCache == nil {
			m.quoteCache = make(map[int]quoteScanCacheEntry)
		}
		m.quoteCache[pos] = quoteScanCacheEntry{closed: closed, nextPos: nextPos}
	}
	return closed, nextPos, true
}

func (m *bracketMatcher) matchClose(openIdx int) int {
	if openIdx < 0 || openIdx >= len(m.text) || m.text[openIdx] != '[' {
		return -1
	}
	if m.rootResult != nil {
		if idx, ok := m.rootResult[openIdx]; ok {
			return idx
		}
	}
	if m.outcomes != nil {
		if out, ok := m.outcomes[openIdx]; ok {
			if out.kind == outcomeMatched {
				return out.pos
			}
			return -1
		}
	}

	rootAllowNewlines := containerAllowsNewlines(m.text, openIdx)
	stack := []bracketFrame{{
		openIdx:       openIdx,
		allowNewlines: rootAllowNewlines,
		startNLEpoch:  0,
	}}
	lowestFalseIdx := -1
	if !rootAllowNewlines {
		lowestFalseIdx = 0
	}
	atElemStart := true
	atCheckpoint := true
	newlineEpoch := 0
	usedShortcut := false

	var rootKeys []int64
	var drop1Trail []drop1Entry

	recordRoot := func(res int) {
		m.setRootResult(openIdx, res)
		if len(rootKeys) == 0 {
			return
		}
		if m.rootMemo == nil {
			m.rootMemo = make(map[int64]int)
		}
		for _, k := range rootKeys {
			m.rootMemo[k] = res
		}
	}
	failDrop1All := func() {
		if len(drop1Trail) == 0 {
			return
		}
		if m.drop1Memo == nil {
			m.drop1Memo = make(map[int64]int)
		}
		for _, e := range drop1Trail {
			m.drop1Memo[e.key] = -1
		}
		drop1Trail = drop1Trail[:0]
	}
	resolveDrop1Level := func(level, nextPos int) {
		if len(drop1Trail) == 0 {
			return
		}
		cut := len(drop1Trail)
		for cut > 0 && drop1Trail[cut-1].level == level {
			cut--
		}
		if cut < len(drop1Trail) {
			if m.drop1Memo == nil {
				m.drop1Memo = make(map[int64]int)
			}
			for _, e := range drop1Trail[cut:] {
				m.drop1Memo[e.key] = nextPos
			}
			drop1Trail = drop1Trail[:cut]
		}
	}
	cacheKilledFalseFrames := func(fromIdx, survivingChildIdx, nlPos int) {
		nextTIdx := survivingChildIdx
		accBD := 0
		for k := len(stack) - 1; k >= fromIdx; k-- {
			f := stack[k]
			if f.allowNewlines {
				nextTIdx = f.openIdx
				accBD = 0
				continue
			}
			accBD += f.braceDepth
			if k == 0 && usedShortcut {
				continue
			}
			if nextTIdx >= 0 {
				m.setOutcome(f.openIdx, bracketOutcome{
					kind:            outcomeNewlineAtChild,
					pos:             nextTIdx,
					extraBraceDepth: accBD,
				})
			} else {
				m.setOutcome(f.openIdx, bracketOutcome{
					kind:            outcomeNewline,
					pos:             nlPos,
					extraBraceDepth: accBD,
				})
			}
		}
	}
	killFalseFrames := func(survivingChildIdx, nlPos int) bool {
		if !stack[0].allowNewlines {
			cacheKilledFalseFrames(0, survivingChildIdx, nlPos)
			recordRoot(-1)
			failDrop1All()
			return false
		}
		if lowestFalseIdx >= 0 {
			cacheKilledFalseFrames(lowestFalseIdx, survivingChildIdx, nlPos)
			kept := stack[:lowestFalseIdx]
			for _, frame := range stack[lowestFalseIdx:] {
				if !frame.allowNewlines {
					if len(kept) > 0 {
						kept[len(kept)-1].braceDepth += frame.braceDepth
					}
					continue
				}
				kept = append(kept, frame)
			}
			stack = kept
			lowestFalseIdx = -1
		}
		return true
	}

	i := openIdx + 1
	for i < len(m.text) {
		b := m.text[i]
		isQuote := atElemStart && isQuotedStart(m.text, i)
		if len(stack) == 1 && (atCheckpoint || isQuote || b == '[' || b == ']' || b == '{' || b == '}' || b == '\r' || b == '\n') {
			k := matcherStateKey(i, stack[0].allowNewlines, atElemStart)
			if stack[0].braceDepth == 0 {
				if m.rootMemo != nil {
					if res, ok := m.rootMemo[k]; ok {
						recordRoot(res)
						return res
					}
				}
				rootKeys = append(rootKeys, k)
			} else {
				if m.drop1Memo != nil {
					if nextPos, ok := m.drop1Memo[k]; ok {
						usedShortcut = true
						if nextPos < 0 {
							recordRoot(-1)
							failDrop1All()
							return -1
						}
						oldLevel := stack[0].braceDepth
						stack[0].braceDepth--
						resolveDrop1Level(oldLevel, nextPos)
						i = nextPos
						atElemStart = false
						atCheckpoint = true
						continue
					}
				}
				drop1Trail = append(drop1Trail, drop1Entry{key: k, level: stack[0].braceDepth})
			}
		}
		atCheckpoint = false

		if b == '\r' || b == '\n' {
			newlineEpoch++
			if !killFalseFrames(-1, i) {
				return -1
			}
			atElemStart = true
			atCheckpoint = true
			i++
			continue
		}
		if isQuote {
			closed, nextPos, ok := m.scanQuoteCached(i)
			if ok {
				if !closed {
					if !stack[len(stack)-1].allowNewlines {
						atElemStart = false
						atCheckpoint = true
						i++
						continue
					}
					break
				}
				i = nextPos
				atElemStart = false
				atCheckpoint = true
				continue
			}
		}
		switch b {
		case '[':
			if m.outcomes != nil {
				if out, ok := m.outcomes[i]; ok {
					switch out.kind {
					case outcomeMatched:
						if out.hasNewline {
							newlineEpoch++
							if !killFalseFrames(i, -1) {
								return -1
							}
						}
						i = out.pos + 1
						atElemStart = false
						atCheckpoint = true
						continue
					case outcomeUnbalancedClose:
						if out.hasNewline {
							newlineEpoch++
							if !killFalseFrames(i, -1) {
								return -1
							}
						}
						stack[len(stack)-1].braceDepth += out.extraBraceDepth
						i = out.pos + 1
						atElemStart = false
						atCheckpoint = true
						continue
					case outcomeBraceClose:
						if out.hasNewline {
							newlineEpoch++
							if !killFalseFrames(i, -1) {
								return -1
							}
						}
						i = out.pos
						atElemStart = false
						atCheckpoint = true
						continue
					case outcomeNewline:
						newlineEpoch++
						stack[len(stack)-1].braceDepth += out.extraBraceDepth
						if !killFalseFrames(-1, out.pos) {
							return -1
						}
						i = out.pos + 1
						atElemStart = true
						atCheckpoint = true
						continue
					case outcomeNewlineAtChild:
						stack[len(stack)-1].braceDepth += out.extraBraceDepth
						if !stack[0].allowNewlines {
							cacheKilledFalseFrames(0, out.pos, -1)
							recordRoot(-1)
							failDrop1All()
							return -1
						}
						i = out.pos
						atElemStart = true
						atCheckpoint = true
						continue
					case outcomeAbort:
						for idx, frame := range stack {
							if idx == 0 && usedShortcut {
								continue
							}
							m.setOutcome(frame.openIdx, bracketOutcome{kind: outcomeAbort})
						}
						recordRoot(-1)
						failDrop1All()
						return -1
					}
				}
			}
			allowNewlines := containerAllowsNewlines(m.text, i)
			if !allowNewlines && lowestFalseIdx < 0 {
				lowestFalseIdx = len(stack)
			}
			stack = append(stack, bracketFrame{
				openIdx:       i,
				allowNewlines: allowNewlines,
				startNLEpoch:  newlineEpoch,
			})
			atElemStart = true
			atCheckpoint = true
			i++
		case ']':
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if lowestFalseIdx >= len(stack) {
				lowestFalseIdx = -1
			}
			frameHasNL := newlineEpoch != top.startNLEpoch
			if top.braceDepth > 0 {
				if len(stack) > 0 || !usedShortcut {
					m.setOutcome(top.openIdx, bracketOutcome{
						kind:            outcomeUnbalancedClose,
						pos:             i,
						extraBraceDepth: top.braceDepth,
						hasNewline:      frameHasNL,
					})
				}
				if len(stack) == 0 {
					recordRoot(-1)
					failDrop1All()
					return -1
				}
				stack[len(stack)-1].braceDepth += top.braceDepth
				atElemStart = false
				atCheckpoint = true
				i++
				continue
			}
			if len(stack) > 0 || !usedShortcut {
				m.setOutcome(top.openIdx, bracketOutcome{
					kind:       outcomeMatched,
					pos:        i,
					hasNewline: frameHasNL,
				})
			}
			if len(stack) == 0 {
				recordRoot(i)
				return i
			}
			atElemStart = false
			atCheckpoint = true
			i++
		case '{':
			stack[len(stack)-1].braceDepth++
			atElemStart = true
			atCheckpoint = true
			i++
		case '}':
			for len(stack) > 0 && stack[len(stack)-1].braceDepth == 0 {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if len(stack) > 0 || !usedShortcut {
					m.setOutcome(top.openIdx, bracketOutcome{
						kind:       outcomeBraceClose,
						pos:        i,
						hasNewline: newlineEpoch != top.startNLEpoch,
					})
				}
			}
			if lowestFalseIdx >= len(stack) {
				lowestFalseIdx = -1
			}
			if len(stack) == 0 {
				recordRoot(-1)
				failDrop1All()
				return -1
			}
			oldLevel := stack[len(stack)-1].braceDepth
			stack[len(stack)-1].braceDepth--
			if len(stack) == 1 {
				resolveDrop1Level(oldLevel, i+1)
			}
			atElemStart = false
			atCheckpoint = true
			i++
		case ',', ':':
			atElemStart = true
			i++
		default:
			if b < utf8.RuneSelf {
				atElemStart = isASCIIUnicodeSpace(b)
				i++
			} else {
				r, size := utf8.DecodeRuneInString(m.text[i:])
				atElemStart = isUnicodeSpaceRune(r)
				i += size
			}
		}
	}

	for idx, frame := range stack {
		if idx == 0 && usedShortcut {
			continue
		}
		if _, exists := m.outcomes[frame.openIdx]; !exists {
			m.setOutcome(frame.openIdx, bracketOutcome{kind: outcomeAbort})
		}
	}
	recordRoot(-1)
	failDrop1All()
	return -1
}

type quotedSpan struct {
	contentStart int
	contentEnd   int
}

func isValidQuotedSpanStart(s string, pos int, escaped bool) bool {
	if pos == 0 {
		return true
	}
	prev := s[pos-1]
	switch prev {
	case '[', '{', '(', ',', ':', '=', ';', '<':
		return true
	case '"', '\'':
		return escaped
	}
	if prev < utf8.RuneSelf {
		return isASCIIUnicodeSpace(prev)
	}
	r, _ := utf8.DecodeLastRuneInString(s[:pos])
	return isUnicodeSpaceRune(r)
}

func isValidQuotedSpanEnd(s string, nextPos int, escaped bool) bool {
	if nextPos >= len(s) {
		return true
	}
	next := s[nextPos]
	switch next {
	case ',', ']', '}', ')', ':', ';', '>':
		return true
	case '"', '\'':
		return escaped
	}
	if next < utf8.RuneSelf {
		return isASCIIUnicodeSpace(next)
	}
	r, _ := utf8.DecodeRuneInString(s[nextPos:])
	return isUnicodeSpaceRune(r)
}

func buildQuotedSpans(s string, escaped bool) []quotedSpan {
	var spans []quotedSpan
	i := 0
	for i < len(s) {
		var isQuote bool
		if escaped {
			isQuote = s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\'')
		} else {
			isQuote = s[i] == '"' || s[i] == '\''
		}
		if !isQuote {
			i++
			continue
		}
		if !isValidQuotedSpanStart(s, i, escaped) {
			if escaped {
				i += 2
			} else {
				i++
			}
			continue
		}
		openDelim, content, closeDelim, nextPos, ok := scanQuotedString(s, i)
		if ok && closeDelim != "" && isValidQuotedSpanEnd(s, nextPos, escaped) {
			contentStart := i + len(openDelim)
			contentEnd := contentStart + len(content)
			spans = append(spans, quotedSpan{
				contentStart: contentStart,
				contentEnd:   contentEnd,
			})
			i = nextPos
			continue
		}
		if escaped {
			i += 2
		} else {
			i++
		}
	}
	return spans
}

func findEnclosingSpan(spans []quotedSpan, pos int) (quotedSpan, bool) {
	lo, hi := 0, len(spans)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if spans[mid].contentEnd <= pos {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(spans) && spans[lo].contentStart <= pos && pos < spans[lo].contentEnd {
		return spans[lo], true
	}
	return quotedSpan{}, false
}

// bracketContext pairs O(N) bracketMatcher instances with enclosing quoted-string
// spans so an unclosed '[' inside a valid quoted string literal cannot escape
// past its closing quote into sibling elements.
type bracketContext struct {
	text             string
	initialized      bool
	outerSpans       []quotedSpan
	escapedSpans     []quotedSpan
	rootMatcher      *bracketMatcher
	lastOuterSpan    quotedSpan
	lastOuterMatcher *bracketMatcher
	lastEscSpan      quotedSpan
	lastEscMatcher   *bracketMatcher
	braceClose       map[int]int
}

func (c *bracketContext) init() {
	if c.initialized {
		return
	}
	c.initialized = true
	c.outerSpans = buildQuotedSpans(c.text, false)
	c.escapedSpans = buildQuotedSpans(c.text, true)
}

func (c *bracketContext) enclosingRange(openIdx int) (start, end int, usedEsc bool) {
	c.init()
	start, end = 0, len(c.text)
	if sp, ok := findEnclosingSpan(c.outerSpans, openIdx); ok {
		start, end = sp.contentStart, sp.contentEnd
	}
	if sp, ok := findEnclosingSpan(c.escapedSpans, openIdx); ok {
		if sp.contentStart >= start && sp.contentEnd <= end {
			start, end = sp.contentStart, sp.contentEnd
			usedEsc = true
		} else if sp.contentEnd < end {
			if sp.contentStart > start {
				start = sp.contentStart
			}
			end = sp.contentEnd
			usedEsc = true
		}
	}
	return start, end, usedEsc
}

func (c *bracketContext) matchClose(openIdx int) (closeIdx int, limit int) {
	start, end, usedEsc := c.enclosingRange(openIdx)
	if start == 0 && end == len(c.text) {
		if c.rootMatcher == nil {
			c.rootMatcher = &bracketMatcher{text: c.text}
		}
		return c.rootMatcher.matchClose(openIdx), len(c.text)
	}
	if end-openIdx <= 16 {
		local := bracketMatcher{text: c.text[start:end]}
		rel := local.matchClose(openIdx - start)
		if rel < 0 {
			return -1, end
		}
		return start + rel, end
	}
	sp := quotedSpan{contentStart: start, contentEnd: end}
	var m *bracketMatcher
	if usedEsc {
		if c.lastEscMatcher == nil || c.lastEscSpan != sp {
			c.lastEscSpan = sp
			c.lastEscMatcher = &bracketMatcher{text: c.text[start:end]}
		}
		m = c.lastEscMatcher
	} else {
		if c.lastOuterMatcher == nil || c.lastOuterSpan != sp {
			c.lastOuterSpan = sp
			c.lastOuterMatcher = &bracketMatcher{text: c.text[start:end]}
		}
		m = c.lastOuterMatcher
	}
	rel := m.matchClose(openIdx - start)
	if rel < 0 {
		return -1, end
	}
	return start + rel, end
}

// matchBraceClose finds the matching '}' for a JSON/dict object '{' at openIdx
// within its enclosing quoted span in amortized O(N) time.
func (c *bracketContext) matchBraceClose(openIdx int) (closeIdx int, limit int) {
	_, end, _ := c.enclosingRange(openIdx)
	if openIdx < 0 || openIdx >= end || c.text[openIdx] != '{' {
		return -1, end
	}
	if c.braceClose != nil {
		if res, ok := c.braceClose[openIdx]; ok {
			return res, end
		}
	} else {
		c.braceClose = make(map[int]int)
	}

	scoped := c.text[:end]
	first := skipUnicodeSpace(scoped, openIdx+1)
	if first >= end {
		c.braceClose[openIdx] = -1
		return -1, end
	}
	if scoped[first] == '}' {
		c.braceClose[openIdx] = first
		return first, end
	}
	if !isQuotedStart(scoped, first) {
		c.braceClose[openIdx] = -1
		return -1, end
	}

	stack := []int{openIdx}
	failAll := func() (int, int) {
		for _, idx := range stack {
			c.braceClose[idx] = -1
		}
		return -1, end
	}

	i := openIdx + 1
	for i < end {
		if isQuotedStart(scoped, i) {
			_, _, closeDelim, nextPos, ok := scanQuotedString(scoped, i)
			if ok {
				if closeDelim == "" {
					return failAll()
				}
				i = nextPos
				continue
			}
		}
		switch scoped[i] {
		case '{':
			if cached, ok := c.braceClose[i]; ok {
				if cached >= 0 {
					i = cached + 1
					continue
				}
				return failAll()
			}
			stack = append(stack, i)
			i++
		case '}':
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			c.braceClose[top] = i
			if len(stack) == 0 {
				return i, end
			}
			i++
		default:
			i++
		}
	}
	return failAll()
}

// scanAuthTokenTail scans the value portion of an unquoted Bearer or Basic
// credential token starting at pos, consuming balanced [...] brackets while
// stopping at any unmatched ']' (such as a closing Go %v slice bracket) or
// auth delimiter (whitespace, ',', ';', '"', '\'', '}', ')', '\').
func scanAuthTokenTail(s string, pos int) int {
	bracketDepth := 0
	for pos < len(s) {
		b := s[pos]
		if b == '[' {
			bracketDepth++
			pos++
			continue
		}
		if b == ']' {
			if bracketDepth > 0 {
				bracketDepth--
				pos++
				continue
			}
			break
		}
		switch b {
		case ',', ';', '"', '\'', '}', ')', '\\':
			return pos
		}
		if b < utf8.RuneSelf {
			if isASCIIUnicodeSpace(b) {
				return pos
			}
			pos++
		} else {
			r, size := utf8.DecodeRuneInString(s[pos:])
			if isUnicodeSpaceRune(r) {
				return pos
			}
			pos += size
		}
	}
	return pos
}

func equalFoldASCII(s, lower string) bool {
	if len(s) != len(lower) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

func skipUnicodeSpaceBackward(s string, floor, pos int) int {
	for pos > floor {
		b := s[pos-1]
		if b < utf8.RuneSelf {
			if isASCIIUnicodeSpace(b) {
				pos--
				continue
			}
			break
		}
		r, size := utf8.DecodeLastRuneInString(s[floor:pos])
		if !isUnicodeSpaceRune(r) {
			break
		}
		pos -= size
	}
	return pos
}

func findBearerPrefix(s string, from int) (matchStart, valStart int, ok bool) {
	for i := from; i+6 < len(s); i++ {
		c := s[i]
		if c != 'b' && c != 'B' {
			continue
		}
		if equalFoldASCII(s[i:i+6], "bearer") {
			wsEnd := skipUnicodeSpace(s, i+6)
			if wsEnd > i+6 {
				return i, wsEnd, true
			}
		}
	}
	return 0, 0, false
}

func matchBasicPrefixAt(s string, pos int) (int, bool) {
	if pos+5 < len(s) && equalFoldASCII(s[pos:pos+5], "basic") {
		wsEnd := skipUnicodeSpace(s, pos+5)
		if wsEnd > pos+5 {
			return wsEnd, true
		}
	}
	return pos, false
}

func findAuthKeyPrefix(s string, from int) (int, bool) {
	const kw = "authorization"
	for i := from; i+len(kw) < len(s); i++ {
		c := s[i]
		if c != 'a' && c != 'A' {
			continue
		}
		if !equalFoldASCII(s[i:i+len(kw)], kw) {
			continue
		}
		wordOK := i == 0 || !isASCIIWordByte(s[i-1])
		if !wordOK && i >= 6 && i-6 >= from &&
			(s[i-1] == '-' || s[i-1] == '_') &&
			equalFoldASCII(s[i-6:i-1], "proxy") &&
			(i == 6 || !isASCIIWordByte(s[i-7])) {
			wordOK = true
		}
		if !wordOK {
			continue
		}
		pos := i + len(kw)
		if pos < len(s) && (s[pos] == '"' || s[pos] == '\'') {
			pos++
		} else if pos+1 < len(s) && s[pos] == '\\' && (s[pos+1] == '"' || s[pos+1] == '\'') {
			pos += 2
		}
		ws1 := skipUnicodeSpace(s, pos)
		if ws1 < len(s) && (s[ws1] == '=' || s[ws1] == ':') {
			return skipUnicodeSpace(s, ws1+1), true
		}
		if ws1 > pos {
			return ws1, true
		}
	}
	return 0, false
}

var secretKeySuffixes = [...]string{
	"token",
	"password",
	"passwd",
	"client_secret",
	"client-secret",
	"secret_key",
	"secret-key",
	"stream_key",
	"api_key",
	"api-key",
	"secret",
	"code",
}

func hasSecretKeySuffix(s string, floor, end int) bool {
	avail := end - floor
	for _, kw := range secretKeySuffixes {
		if avail >= len(kw) && equalFoldASCII(s[end-len(kw):end], kw) {
			return true
		}
	}
	return false
}

func findSecretKeyPrefixEnd(s string, from int) (int, bool) {
	for i := from; i < len(s); i++ {
		c := s[i]
		var afterDelim int
		if c == '=' || c == ':' {
			afterDelim = i + 1
		} else if c == '%' && i+2 < len(s) && s[i+1] == '3' && (s[i+2] == 'd' || s[i+2] == 'D') {
			afterDelim = i + 3
		} else {
			continue
		}
		keyEnd := skipUnicodeSpaceBackward(s, from, i)
		if keyEnd > from && (s[keyEnd-1] == '"' || s[keyEnd-1] == '\'') {
			keyEnd--
			if keyEnd > from && s[keyEnd-1] == '\\' {
				keyEnd--
			}
		}
		if hasSecretKeySuffix(s, from, keyEnd) {
			return skipUnicodeSpace(s, afterDelim), true
		}
	}
	return 0, false
}

func redactBearer(text string) string {
	matchStart, valStart, ok := findBearerPrefix(text, 0)
	if !ok {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	cursor := 0
	for {
		if end := scanAuthTokenTail(text, valStart); end > valStart {
			b.WriteString(text[cursor:matchStart])
			b.WriteString(redactedBearer)
			cursor = end
		} else {
			b.WriteString(text[cursor:valStart])
			cursor = valStart
		}
		if cursor >= len(text) {
			break
		}
		matchStart, valStart, ok = findBearerPrefix(text, cursor)
		if !ok {
			b.WriteString(text[cursor:])
			break
		}
	}
	return b.String()
}

func findBasicTokenPrefix(s string, from int) (matchStart, valStart int, ok bool) {
	for i := from; i+5 < len(s); i++ {
		c := s[i]
		if c != 'b' && c != 'B' {
			continue
		}
		if (i == 0 || !isASCIIWordByte(s[i-1])) && equalFoldASCII(s[i:i+5], "basic") {
			wsEnd := skipUnicodeSpace(s, i+5)
			if wsEnd > i+5 {
				return i, wsEnd, true
			}
		}
	}
	return 0, 0, false
}

func redactBasicUnquoted(text string) string {
	matchStart, valStart, ok := findBasicTokenPrefix(text, 0)
	if !ok {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	cursor := 0
	for {
		if end := scanAuthTokenTail(text, valStart); end > valStart {
			b.WriteString(text[cursor:matchStart])
			b.WriteString(redactedBasic)
			cursor = end
		} else {
			b.WriteString(text[cursor:valStart])
			cursor = valStart
		}
		if cursor >= len(text) {
			break
		}
		matchStart, valStart, ok = findBasicTokenPrefix(text, cursor)
		if !ok {
			b.WriteString(text[cursor:])
			break
		}
	}
	return b.String()
}

// redactGoStringSlice redacts all non-empty quoted elements inside a Go
// `[]string{...}` literal starting right after `{`, preserving empty elements
// and slice syntax.
func redactGoStringSlice(text string, bodyStart int, redactElem func(string) string) (string, int) {
	var b strings.Builder
	pos := bodyStart
	for pos < len(text) {
		wsEnd := skipUnicodeSpace(text, pos)
		b.WriteString(text[pos:wsEnd])
		pos = wsEnd
		if pos >= len(text) {
			break
		}
		if text[pos] == '}' {
			b.WriteByte('}')
			return b.String(), pos + 1
		}
		openDelim, content, closeDelim, nextPos, ok := scanQuotedString(text, pos)
		if !ok {
			break
		}
		b.WriteString(openDelim)
		if content != "" {
			b.WriteString(redactElem(content))
		}
		b.WriteString(closeDelim)
		pos = nextPos
		if closeDelim == "" {
			break
		}
		wsEnd = skipUnicodeSpace(text, pos)
		b.WriteString(text[pos:wsEnd])
		pos = wsEnd
		if pos < len(text) && text[pos] == ',' {
			b.WriteByte(',')
			pos++
			continue
		}
		if pos < len(text) && text[pos] == '}' {
			b.WriteByte('}')
			return b.String(), pos + 1
		}
		break
	}
	return b.String(), pos
}

func redactBasicInQuoted(content string) string {
	return basicTokenInQuotedPattern.ReplaceAllLiteralString(content, redactedBasic)
}

func isASCIIWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func redactBasicAuth(text string) string {
	valStart, ok := findAuthKeyPrefix(text, 0)
	if !ok {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))
	bctx := &bracketContext{text: text}
	cursor := 0
	for {
		if strings.HasPrefix(text[valStart:], "[]string{") {
			b.WriteString(text[cursor : valStart+len("[]string{")])
			redacted, nextPos := redactGoStringSlice(text, valStart+len("[]string{"), redactBasicInQuoted)
			b.WriteString(redacted)
			cursor = nextPos
		} else if valStart < len(text) && text[valStart] == '[' {
			b.WriteString(text[cursor:valStart])
			redacted, nextPos := redactAuthBracketContainer(text, valStart, bctx)
			b.WriteString(redacted)
			cursor = nextPos
		} else if isQuotedStart(text, valStart) {
			openDelim, content, closeDelim, nextPos, ok := scanQuotedString(text, valStart)
			if ok && content != "" {
				if replaced := redactBasicInQuoted(content); replaced != content {
					b.WriteString(text[cursor:valStart])
					b.WriteString(openDelim)
					b.WriteString(replaced)
					b.WriteString(closeDelim)
					cursor = nextPos
				} else {
					b.WriteString(text[cursor:valStart])
					cursor = valStart
				}
			} else {
				b.WriteString(text[cursor:valStart])
				cursor = valStart
			}
		} else if tokStart, ok := matchBasicPrefixAt(text, valStart); ok {
			if tokEnd := scanAuthTokenTail(text, tokStart); tokEnd > tokStart {
				b.WriteString(text[cursor:valStart])
				b.WriteString(redactedBasic)
				cursor = tokEnd
			} else {
				b.WriteString(text[cursor:valStart])
				cursor = valStart
			}
		} else {
			b.WriteString(text[cursor:valStart])
			cursor = valStart
		}

		if cursor >= len(text) {
			break
		}
		valStart, ok = findAuthKeyPrefix(text, cursor)
		if !ok {
			b.WriteString(text[cursor:])
			break
		}
	}
	return b.String()
}

func redactAuthBracketContainer(text string, openIdx int, bctx *bracketContext) (string, int) {
	closeIdx, limit := bctx.matchClose(openIdx)
	if closeIdx >= 0 {
		body := text[openIdx+1 : closeIdx]
		var b strings.Builder
		b.Grow(len(body) + 16)
		b.WriteByte('[')
		pos := 0
		for pos < len(body) {
			if body[pos] == ',' {
				b.WriteByte(',')
				pos++
				continue
			}
			wsEnd := skipUnicodeSpace(body, pos)
			if wsEnd > pos {
				b.WriteString(body[pos:wsEnd])
				pos = wsEnd
				continue
			}
			if isQuotedStart(body, pos) {
				openDelim, content, closeDelim, nextPos, ok := scanQuotedString(body, pos)
				if ok {
					b.WriteString(openDelim)
					if content != "" {
						b.WriteString(redactBasicInQuoted(content))
					}
					b.WriteString(closeDelim)
					pos = nextPos
					continue
				}
			}
			segEnd := pos
			atStart := false
			for segEnd < len(body) {
				if atStart && isQuotedStart(body, segEnd) {
					break
				}
				ch := body[segEnd]
				if ch == ',' {
					atStart = true
					segEnd++
					continue
				}
				if ch < utf8.RuneSelf {
					atStart = isASCIIUnicodeSpace(ch)
					segEnd++
				} else {
					r, size := utf8.DecodeRuneInString(body[segEnd:])
					atStart = isUnicodeSpaceRune(r)
					segEnd += size
				}
			}
			b.WriteString(redactBasicUnquoted(body[pos:segEnd]))
			pos = segEnd
		}
		b.WriteByte(']')
		return b.String(), closeIdx + 1
	}

	return redactUnclosedAuthBracket(text[:limit], openIdx)
}

func redactUnclosedAuthBracket(text string, openIdx int) (string, int) {
	var b strings.Builder
	b.WriteByte('[')
	pos := openIdx + 1
	if containerAllowsNewlines(text, openIdx) {
		for pos < len(text) {
			wsEnd := skipUnicodeSpace(text, pos)
			b.WriteString(text[pos:wsEnd])
			pos = wsEnd
			if pos >= len(text) {
				break
			}
			if openDelim, content, closeDelim, nextPos, ok := scanQuotedString(text, pos); ok {
				b.WriteString(openDelim)
				if content != "" {
					b.WriteString(redactBasicInQuoted(content))
				}
				b.WriteString(closeDelim)
				pos = nextPos
				if closeDelim == "" {
					break
				}
			} else if scalarEnd, ok := scanJSONBareScalar(text, pos); ok {
				b.WriteString(text[pos:scalarEnd])
				pos = scalarEnd
			} else {
				break
			}
			wsEnd = skipUnicodeSpace(text, pos)
			b.WriteString(text[pos:wsEnd])
			pos = wsEnd
			if pos < len(text) && text[pos] == ',' {
				b.WriteByte(',')
				pos++
				continue
			}
			break
		}
		return b.String(), pos
	}

	end := pos
	for end < len(text) {
		ch := text[end]
		if ch == '\r' || ch == '\n' || ch == ';' || ch == '"' || ch == '\'' || ch == '}' {
			break
		}
		end++
	}
	b.WriteString(redactBasicUnquoted(text[pos:end]))
	return b.String(), end
}

func redactSecretElement(_ string) string {
	return redactedValue
}

func matchRedactionPlaceholderPrefix(s string) int {
	for _, marker := range []string{redactedValue, redactedURL, redactedCredential} {
		if strings.HasPrefix(s, marker) {
			return len(marker)
		}
	}
	return 0
}

func isUnquotedContainerTerminator(s string, pos int) bool {
	if pos >= len(s) {
		return true
	}
	b := s[pos]
	switch b {
	case ',', ';', '}', ']', ')', '"', '\'':
		return true
	case '\\':
		if pos+1 >= len(s) {
			return true
		}
		next := s[pos+1]
		return next == '"' || next == '\'' || next == '\r' || next == '\n'
	}
	if b < utf8.RuneSelf {
		return isASCIIUnicodeSpace(b)
	}
	r, _ := utf8.DecodeRuneInString(s[pos:])
	return isUnicodeSpaceRune(r)
}

func scanUnquotedSecretTail(s string, pos int) int {
	bracketDepth := 0
	for pos < len(s) {
		b := s[pos]
		if b == '[' {
			bracketDepth++
			pos++
			continue
		}
		if b == ']' {
			if bracketDepth > 0 {
				bracketDepth--
				pos++
				continue
			}
			break
		}
		if isUnquotedContainerTerminator(s, pos) {
			break
		}
		if b == '\\' {
			_, size := utf8.DecodeRuneInString(s[pos+1:])
			pos += 1 + size
			continue
		}
		if b < utf8.RuneSelf {
			pos++
		} else {
			_, size := utf8.DecodeRuneInString(s[pos:])
			pos += size
		}
	}
	return pos
}

func redactKeyValueSecrets(text string) string {
	valStart, ok := findSecretKeyPrefixEnd(text, 0)
	if !ok {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))
	bctx := &bracketContext{text: text}
	cursor := 0
	for {
		if strings.HasPrefix(text[valStart:], "[]string{") {
			b.WriteString(text[cursor : valStart+len("[]string{")])
			redacted, nextPos := redactGoStringSlice(text, valStart+len("[]string{"), redactSecretElement)
			b.WriteString(redacted)
			cursor = nextPos
		} else if markerLen := matchRedactionPlaceholderPrefix(text[valStart:]); markerLen > 0 {
			b.WriteString(text[cursor:valStart])
			b.WriteString(redactedValue)
			cursor = scanUnquotedSecretTail(text, valStart+markerLen)
		} else if valStart < len(text) && text[valStart] == '[' {
			closeIdx, limit := bctx.matchClose(valStart)
			if closeIdx >= 0 && !isUnquotedContainerTerminator(text[:limit], closeIdx+1) {
				b.WriteString(text[cursor:valStart])
				b.WriteString(redactedValue)
				cursor = scanUnquotedSecretTail(text[:limit], closeIdx+1)
			} else {
				b.WriteString(text[cursor:valStart])
				redacted, nextPos := redactSecretBracketContainer(text, valStart, closeIdx, limit)
				b.WriteString(redacted)
				cursor = nextPos
			}
		} else if valStart < len(text) && text[valStart] == '{' {
			closeIdx, limit := bctx.matchBraceClose(valStart)
			if closeIdx >= 0 {
				if redacted, ok := redactSecretJSONObject(text[valStart : closeIdx+1]); ok {
					b.WriteString(text[cursor:valStart])
					b.WriteString(redacted)
					cursor = closeIdx + 1
				} else if end := scanUnquotedSecretTail(text[:limit], valStart); end > valStart {
					b.WriteString(text[cursor:valStart])
					b.WriteString(redactedValue)
					cursor = end
				} else {
					b.WriteString(text[cursor:valStart])
					cursor = valStart
				}
			} else if redacted, nextPos, ok := redactUnclosedSecretJSONObject(text[:limit], valStart); ok {
				b.WriteString(text[cursor:valStart])
				b.WriteString(redacted)
				cursor = nextPos
			} else if end := scanUnquotedSecretTail(text[:limit], valStart); end > valStart {
				b.WriteString(text[cursor:valStart])
				b.WriteString(redactedValue)
				cursor = end
			} else {
				b.WriteString(text[cursor:valStart])
				cursor = valStart
			}
		} else if isQuotedStart(text, valStart) {
			openDelim, content, closeDelim, nextPos, ok := scanQuotedString(text, valStart)
			if ok {
				b.WriteString(text[cursor:valStart])
				b.WriteString(openDelim)
				if content != "" {
					b.WriteString(redactedValue)
				}
				b.WriteString(closeDelim)
				cursor = nextPos
			} else {
				b.WriteString(text[cursor:valStart])
				cursor = valStart
			}
		} else if end := scanUnquotedSecretTail(text, valStart); end > valStart {
			b.WriteString(text[cursor:valStart])
			b.WriteString(redactedValue)
			cursor = end
		} else {
			b.WriteString(text[cursor:valStart])
			cursor = valStart
		}

		if cursor >= len(text) {
			break
		}
		valStart, ok = findSecretKeyPrefixEnd(text, cursor)
		if !ok {
			b.WriteString(text[cursor:])
			break
		}
	}
	return b.String()
}

type jsonRedactState uint8

const (
	jsonStateObjKey jsonRedactState = iota
	jsonStateObjColon
	jsonStateObjValue
	jsonStateObjComma
	jsonStateArrValue
	jsonStateArrComma
)

// redactSecretJSONObject redacts all non-empty quoted string values inside a
// balanced JSON/Python-dict object bound to a secret key while preserving
// object keys, empty strings, bare JSON scalars, and structure.
func redactSecretJSONObject(s string) (string, bool) {
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	b.WriteByte('{')
	pos := 1
	stack := []jsonRedactState{jsonStateObjKey}

	for len(stack) > 0 && pos < len(s) {
		wsEnd := skipUnicodeSpace(s, pos)
		if wsEnd > pos {
			b.WriteString(s[pos:wsEnd])
			pos = wsEnd
		}
		if pos >= len(s) {
			break
		}
		topIdx := len(stack) - 1
		switch stack[topIdx] {
		case jsonStateObjKey:
			if s[pos] == '}' {
				b.WriteByte('}')
				pos++
				stack = stack[:topIdx]
				continue
			}
			openDelim, content, closeDelim, nextPos, ok := scanQuotedString(s, pos)
			if !ok || closeDelim == "" {
				return "", false
			}
			b.WriteString(openDelim)
			b.WriteString(content)
			b.WriteString(closeDelim)
			pos = nextPos
			stack[topIdx] = jsonStateObjColon

		case jsonStateObjColon:
			if s[pos] != ':' {
				return "", false
			}
			b.WriteByte(':')
			pos++
			stack[topIdx] = jsonStateObjValue

		case jsonStateObjValue:
			if s[pos] == '{' {
				b.WriteByte('{')
				pos++
				stack[topIdx] = jsonStateObjComma
				stack = append(stack, jsonStateObjKey)
			} else if s[pos] == '[' {
				b.WriteByte('[')
				pos++
				stack[topIdx] = jsonStateObjComma
				stack = append(stack, jsonStateArrValue)
			} else if openDelim, content, closeDelim, nextPos, ok := scanQuotedString(s, pos); ok && closeDelim != "" {
				b.WriteString(openDelim)
				if content != "" {
					b.WriteString(redactedValue)
				}
				b.WriteString(closeDelim)
				pos = nextPos
				stack[topIdx] = jsonStateObjComma
			} else if end, ok := scanJSONBareScalar(s, pos); ok {
				b.WriteString(s[pos:end])
				pos = end
				stack[topIdx] = jsonStateObjComma
			} else if end := scanUnquotedSecretTail(s, pos); end > pos {
				b.WriteString(redactedValue)
				pos = end
				stack[topIdx] = jsonStateObjComma
			} else {
				return "", false
			}

		case jsonStateObjComma:
			if s[pos] == ',' {
				b.WriteByte(',')
				pos++
				stack[topIdx] = jsonStateObjKey
			} else if s[pos] == '}' {
				b.WriteByte('}')
				pos++
				stack = stack[:topIdx]
			} else {
				return "", false
			}

		case jsonStateArrValue:
			if s[pos] == ']' {
				b.WriteByte(']')
				pos++
				stack = stack[:topIdx]
			} else if s[pos] == '{' {
				b.WriteByte('{')
				pos++
				stack[topIdx] = jsonStateArrComma
				stack = append(stack, jsonStateObjKey)
			} else if s[pos] == '[' {
				b.WriteByte('[')
				pos++
				stack[topIdx] = jsonStateArrComma
				stack = append(stack, jsonStateArrValue)
			} else if openDelim, content, closeDelim, nextPos, ok := scanQuotedString(s, pos); ok && closeDelim != "" {
				b.WriteString(openDelim)
				if content != "" {
					b.WriteString(redactedValue)
				}
				b.WriteString(closeDelim)
				pos = nextPos
				stack[topIdx] = jsonStateArrComma
			} else if end, ok := scanJSONBareScalar(s, pos); ok {
				b.WriteString(s[pos:end])
				pos = end
				stack[topIdx] = jsonStateArrComma
			} else if end := scanUnquotedSecretTail(s, pos); end > pos {
				b.WriteString(redactedValue)
				pos = end
				stack[topIdx] = jsonStateArrComma
			} else {
				return "", false
			}

		case jsonStateArrComma:
			if s[pos] == ',' {
				b.WriteByte(',')
				pos++
				stack[topIdx] = jsonStateArrValue
			} else if s[pos] == ']' {
				b.WriteByte(']')
				pos++
				stack = stack[:topIdx]
			} else {
				return "", false
			}
		}
	}
	if len(stack) == 0 && pos == len(s) {
		return b.String(), true
	}
	return "", false
}

// redactUnclosedSecretJSONObject redacts scalar string values inside a truncated
// JSON/dict object bound to a secret key without crossing its enclosing span.
func redactUnclosedSecretJSONObject(text string, openIdx int) (string, int, bool) {
	if openIdx >= len(text) || text[openIdx] != '{' {
		return "", openIdx, false
	}
	var b strings.Builder
	b.WriteByte('{')
	pos := openIdx + 1
	stack := []jsonRedactState{jsonStateObjKey}
	seenColon := false

	for len(stack) > 0 && pos < len(text) {
		wsEnd := skipUnicodeSpace(text, pos)
		if wsEnd > pos {
			b.WriteString(text[pos:wsEnd])
			pos = wsEnd
		}
		if pos >= len(text) {
			break
		}
		topIdx := len(stack) - 1
		switch stack[topIdx] {
		case jsonStateObjKey:
			if text[pos] == '}' {
				b.WriteByte('}')
				pos++
				stack = stack[:topIdx]
				continue
			}
			openDelim, content, closeDelim, nextPos, ok := scanQuotedString(text, pos)
			if !ok {
				goto done
			}
			b.WriteString(openDelim)
			b.WriteString(content)
			b.WriteString(closeDelim)
			pos = nextPos
			if closeDelim == "" {
				goto done
			}
			stack[topIdx] = jsonStateObjColon

		case jsonStateObjColon:
			if text[pos] != ':' {
				goto done
			}
			b.WriteByte(':')
			pos++
			seenColon = true
			stack[topIdx] = jsonStateObjValue

		case jsonStateObjValue:
			if text[pos] == '{' {
				b.WriteByte('{')
				pos++
				stack[topIdx] = jsonStateObjComma
				stack = append(stack, jsonStateObjKey)
			} else if text[pos] == '[' {
				b.WriteByte('[')
				pos++
				stack[topIdx] = jsonStateObjComma
				stack = append(stack, jsonStateArrValue)
			} else if openDelim, content, closeDelim, nextPos, ok := scanQuotedString(text, pos); ok {
				b.WriteString(openDelim)
				if content != "" {
					b.WriteString(redactedValue)
				}
				b.WriteString(closeDelim)
				pos = nextPos
				if closeDelim == "" {
					goto done
				}
				stack[topIdx] = jsonStateObjComma
			} else if end, ok := scanJSONBareScalar(text, pos); ok {
				b.WriteString(text[pos:end])
				pos = end
				stack[topIdx] = jsonStateObjComma
			} else {
				goto done
			}

		case jsonStateObjComma:
			if text[pos] == ',' {
				b.WriteByte(',')
				pos++
				stack[topIdx] = jsonStateObjKey
			} else if text[pos] == '}' {
				b.WriteByte('}')
				pos++
				stack = stack[:topIdx]
			} else {
				goto done
			}

		case jsonStateArrValue:
			if text[pos] == ']' {
				b.WriteByte(']')
				pos++
				stack = stack[:topIdx]
			} else if text[pos] == '{' {
				b.WriteByte('{')
				pos++
				stack[topIdx] = jsonStateArrComma
				stack = append(stack, jsonStateObjKey)
			} else if text[pos] == '[' {
				b.WriteByte('[')
				pos++
				stack[topIdx] = jsonStateArrComma
				stack = append(stack, jsonStateArrValue)
			} else if openDelim, content, closeDelim, nextPos, ok := scanQuotedString(text, pos); ok {
				b.WriteString(openDelim)
				if content != "" {
					b.WriteString(redactedValue)
				}
				b.WriteString(closeDelim)
				pos = nextPos
				if closeDelim == "" {
					goto done
				}
				stack[topIdx] = jsonStateArrComma
			} else if end, ok := scanJSONBareScalar(text, pos); ok {
				b.WriteString(text[pos:end])
				pos = end
				stack[topIdx] = jsonStateArrComma
			} else {
				goto done
			}

		case jsonStateArrComma:
			if text[pos] == ',' {
				b.WriteByte(',')
				pos++
				stack[topIdx] = jsonStateArrValue
			} else if text[pos] == ']' {
				b.WriteByte(']')
				pos++
				stack = stack[:topIdx]
			} else {
				goto done
			}
		}
	}

done:
	if !seenColon {
		return "", openIdx, false
	}
	return b.String(), pos, true
}

func scanUnquotedBracketToken(body string, pos int) int {
	end := pos
	for end < len(body) {
		b := body[end]
		if b == ',' {
			break
		}
		if b < utf8.RuneSelf {
			if isASCIIUnicodeSpace(b) {
				break
			}
			end++
		} else {
			r, size := utf8.DecodeRuneInString(body[end:])
			if isUnicodeSpaceRune(r) {
				break
			}
			end += size
		}
	}
	return end
}

func redactSecretBracketContainer(text string, openIdx, closeIdx, limit int) (string, int) {
	if closeIdx >= 0 {
		body := text[openIdx+1 : closeIdx]
		var b strings.Builder
		b.Grow(len(body) + 16)
		b.WriteByte('[')
		pos := 0
		for pos < len(body) {
			if body[pos] == ',' {
				b.WriteByte(',')
				pos++
				continue
			}
			wsEnd := skipUnicodeSpace(body, pos)
			if wsEnd > pos {
				b.WriteString(body[pos:wsEnd])
				pos = wsEnd
				continue
			}
			if isQuotedStart(body, pos) {
				openDelim, content, closeDelim, nextPos, ok := scanQuotedString(body, pos)
				if ok {
					b.WriteString(openDelim)
					if content != "" {
						b.WriteString(redactedValue)
					}
					b.WriteString(closeDelim)
					pos = nextPos
					continue
				}
			}
			end := scanUnquotedBracketToken(body, pos)
			if end == pos {
				_, size := utf8.DecodeRuneInString(body[pos:])
				b.WriteString(body[pos : pos+size])
				pos += size
				continue
			}
			b.WriteString(redactedValue)
			pos = end
		}
		b.WriteByte(']')
		return b.String(), closeIdx + 1
	}

	return redactUnclosedSecretBracket(text[:limit], openIdx)
}

func redactUnclosedSecretBracket(text string, openIdx int) (string, int) {
	var b strings.Builder
	b.WriteByte('[')
	pos := openIdx + 1
	if containerAllowsNewlines(text, openIdx) {
		for pos < len(text) {
			wsEnd := skipUnicodeSpace(text, pos)
			b.WriteString(text[pos:wsEnd])
			pos = wsEnd
			if pos >= len(text) {
				break
			}
			if openDelim, content, closeDelim, nextPos, ok := scanQuotedString(text, pos); ok {
				b.WriteString(openDelim)
				if content != "" {
					b.WriteString(redactedValue)
				}
				b.WriteString(closeDelim)
				pos = nextPos
				if closeDelim == "" {
					break
				}
			} else if markerLen := matchRedactionPlaceholderPrefix(text[pos:]); markerLen > 0 {
				b.WriteString(redactedValue)
				pos = scanUnquotedSecretTail(text, pos+markerLen)
			} else if scalarEnd, ok := scanJSONBareScalar(text, pos); ok {
				b.WriteString(redactedValue)
				pos = scalarEnd
			} else {
				break
			}
			wsEnd = skipUnicodeSpace(text, pos)
			b.WriteString(text[pos:wsEnd])
			pos = wsEnd
			if pos < len(text) && text[pos] == ',' {
				b.WriteByte(',')
				pos++
				continue
			}
			break
		}
		return b.String(), pos
	}

	for pos < len(text) {
		ch := text[pos]
		if ch == '\r' || ch == '\n' || ch == ';' || ch == '"' || ch == '\'' || ch == '}' {
			break
		}
		if markerLen := matchRedactionPlaceholderPrefix(text[pos:]); markerLen > 0 {
			b.WriteString(redactedValue)
			pos += markerLen
			continue
		}
		if ch == ',' || ch == '{' || ch == '[' || ch == ']' {
			b.WriteByte(ch)
			pos++
			continue
		}
		if ch < utf8.RuneSelf {
			if isASCIIUnicodeSpace(ch) {
				b.WriteByte(ch)
				pos++
				continue
			}
		} else {
			r, size := utf8.DecodeRuneInString(text[pos:])
			if isUnicodeSpaceRune(r) {
				b.WriteString(text[pos : pos+size])
				pos += size
				continue
			}
		}
		end := pos
		for end < len(text) {
			c := text[end]
			if c == '\r' || c == '\n' || c == ',' || c == ';' || c == '"' || c == '\'' || c == '{' || c == '}' || c == '[' || c == ']' {
				break
			}
			if c < utf8.RuneSelf {
				if isASCIIUnicodeSpace(c) {
					break
				}
				end++
			} else {
				r, size := utf8.DecodeRuneInString(text[end:])
				if isUnicodeSpaceRune(r) {
					break
				}
				end += size
			}
		}
		b.WriteString(redactedValue)
		pos = end
	}
	return b.String(), pos
}

// redactURLs replaces URLs matching (?i)(?:https?|wss?|rtmps?|socks5h?|socks4a?)://...
// (including bracketed IPv6 hosts with optional userinfo) while preserving
// outer brackets and trailing punctuation from [:;,.]* before whitespace,
// quotes, brackets, or EOF, matching Python's non-greedy lookahead behavior
// without RE2 lookahead support.
func redactURLs(text string) string {
	if !strings.Contains(text, "://") {
		return text
	}
	return urlPattern.ReplaceAllStringFunc(text, func(match string) string {
		schemeEnd := strings.Index(match, "://")
		if schemeEnd < 0 {
			return redactedURL
		}
		bodyStart := schemeEnd + 3
		end := len(match)
		for end > bodyStart+1 {
			switch match[end-1] {
			case ':', ';', ',', '.':
				end--
			default:
				return redactedURL + match[end:]
			}
		}
		return redactedURL + match[end:]
	})
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if len(s) <= maxRunes {
		return s
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

// ColabOneActivityEntry represents a single sanitized entry in the activity log.
type ColabOneActivityEntry struct {
	ID      int     `json:"id"`
	Time    float64 `json:"time"`
	Source  string  `json:"source"`
	Level   string  `json:"level"`
	Message string  `json:"message"`
}

// ColabOneActivityLog is a thread-safe bounded ring buffer of sanitized activity
// entries with optional rotating disk persistence.
type ColabOneActivityLog struct {
	mu       sync.Mutex
	items    []ColabOneActivityEntry
	head     int
	count    int
	limit    int
	sequence int
	logPath  string
	maxBytes int64
	closed   bool
	nowFn    func() time.Time
}

// NewColabOneActivityLog constructs a bounded activity log.
// When limit <= 0, DefaultLimit (300) is used.
// When maxBytes <= 0, DefaultMaxBytes (256 KiB) is used; positive values below
// MinMaxBytes (16 KiB) are clamped to MinMaxBytes.
// When logPath is empty, entries are kept only in memory.
func NewColabOneActivityLog(limit int, logPath string, maxBytes int64) *ColabOneActivityLog {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	} else if maxBytes < MinMaxBytes {
		maxBytes = MinMaxBytes
	}
	if strings.TrimSpace(logPath) == "" {
		logPath = ""
	}
	return &ColabOneActivityLog{
		items:    make([]ColabOneActivityEntry, limit),
		limit:    limit,
		logPath:  logPath,
		maxBytes: maxBytes,
		nowFn:    time.Now,
	}
}

// Add sanitizes and appends a new entry to the ring buffer and, if configured
// and not closed, appends a formatted line to the rotating disk log.
func (l *ColabOneActivityLog) Add(message, source, level string) {
	if l == nil {
		return
	}

	if strings.TrimSpace(source) == "" {
		source = DefaultSource
	}
	if strings.TrimSpace(level) == "" {
		level = DefaultLevel
	}

	cleanMsg := SafeMessage(message)
	cleanSrc := truncateRunes(SafeMessage(source), maxSourceRunes)
	cleanLvl := truncateRunes(strings.ToLower(level), maxLevelRunes)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.limit <= 0 {
		l.limit = DefaultLimit
		l.items = make([]ColabOneActivityEntry, l.limit)
		l.head = 0
		l.count = 0
	}
	if l.maxBytes <= 0 {
		l.maxBytes = DefaultMaxBytes
	} else if l.maxBytes < MinMaxBytes {
		l.maxBytes = MinMaxBytes
	}

	nowFn := l.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()

	l.sequence++
	entry := ColabOneActivityEntry{
		ID:      l.sequence,
		Time:    float64(now.UnixNano()) / 1e9,
		Source:  cleanSrc,
		Level:   cleanLvl,
		Message: cleanMsg,
	}

	if l.count < l.limit {
		idx := (l.head + l.count) % l.limit
		l.items[idx] = entry
		l.count++
	} else {
		l.items[l.head] = entry
		l.head = (l.head + 1) % l.limit
	}

	l.appendDiskLocked(now, cleanLvl, cleanSrc, cleanMsg)
}

// Snapshot returns a chronological copy of all entries currently in the ring buffer.
// The returned slice is always non-nil so JSON marshaling yields [] rather than null.
func (l *ColabOneActivityLog) Snapshot() []ColabOneActivityEntry {
	if l == nil {
		return []ColabOneActivityEntry{}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]ColabOneActivityEntry, l.count)
	if l.count == 0 {
		return out
	}

	first := min(l.count, l.limit-l.head)
	copy(out, l.items[l.head:l.head+first])
	if first < l.count {
		copy(out[first:], l.items[:l.count-first])
	}
	return out
}

// Clear removes all buffered in-memory entries while preserving the monotonic ID sequence.
func (l *ColabOneActivityLog) Clear() {
	if l == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	clear(l.items)
	l.head = 0
	l.count = 0
}

// Close disables further disk writes and releases any disk logging state.
// It is safe to call multiple times or on a nil receiver.
func (l *ColabOneActivityLog) Close() {
	if l == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.closed = true
}

func (l *ColabOneActivityLog) appendDiskLocked(now time.Time, level, source, message string) {
	if l.logPath == "" || l.closed {
		return
	}

	if err := os.MkdirAll(filepath.Dir(l.logPath), 0o755); err != nil {
		return
	}

	if info, err := os.Stat(l.logPath); err == nil && info.Size() >= l.maxBytes {
		backup := l.logPath + ".1"
		if err := os.Rename(l.logPath, backup); err != nil {
			_ = os.Remove(backup)
			if err := os.Rename(l.logPath, backup); err != nil {
				return
			}
		}
	}

	stamp := now.Local().Format("2006-01-02 15:04:05")
	lineMsg := diskLineReplacer.Replace(message)
	lineSrc := diskLineReplacer.Replace(source)
	lineLvl := strings.ToUpper(diskLineReplacer.Replace(level))
	line := fmt.Sprintf("%s [%s] [%s] %s\n", stamp, lineLvl, lineSrc, lineMsg)

	_, statErr := os.Stat(l.logPath)
	created := os.IsNotExist(statErr)

	f, err := os.OpenFile(l.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, writeErr := f.WriteString(line)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return
	}
	if created {
		_ = os.Chmod(l.logPath, 0o600)
	}
}
