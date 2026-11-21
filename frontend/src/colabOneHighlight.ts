import {
  createHighlighterCoreSync,
  type HighlighterCore,
  type ThemedToken,
} from "@shikijs/core";
import { createJavaScriptRegexEngine } from "@shikijs/engine-javascript";
import langJson from "@shikijs/langs/json";
import langPython from "@shikijs/langs/python";
import langShellscript from "@shikijs/langs/shellscript";
import langToml from "@shikijs/langs/toml";
import themeOneDarkPro from "@shikijs/themes/one-dark-pro";
import themeOneLight from "@shikijs/themes/one-light";

export type ColabOneCodeLanguage =
  | "shellscript"
  | "python"
  | "toml"
  | "json"
  | "text";

export interface ColabOneHighlightedToken {
  content: string;
  lightColor?: string;
  darkColor?: string;
  lightFontStyle?: string;
  darkFontStyle?: string;
  lightFontWeight?: string;
  darkFontWeight?: string;
}

export type ColabOneHighlightedLine = ColabOneHighlightedToken[];

const LIGHT_THEME = "one-light";
const DARK_THEME = "one-dark-pro";

// Matching single-quote string delimiter colors from one-light / one-dark-pro.
const QUOTE_LIGHT_COLOR = "#50A14F";
const QUOTE_DARK_COLOR = "#98C379";
const MAX_HIGHLIGHT_CHARS = 65536;
const HIGHLIGHT_CACHE_LIMIT = 24;

let cachedHighlighter: HighlighterCore | null = null;
const highlightCache = new Map<string, ColabOneHighlightedLine[]>();

function getColabOneHighlighter(): HighlighterCore {
  if (!cachedHighlighter) {
    cachedHighlighter = createHighlighterCoreSync({
      themes: [themeOneLight, themeOneDarkPro],
      langs: [langShellscript, langPython, langToml, langJson],
      engine: createJavaScriptRegexEngine(),
    });
  }
  return cachedHighlighter;
}

export function normalizeColabOneCodeLanguage(
  language?: string,
): ColabOneCodeLanguage {
  const key = String(language ?? "")
    .trim()
    .toLowerCase();
  switch (key) {
    case "curl":
    case "bash":
    case "sh":
    case "shell":
    case "shellscript":
    case "zsh":
    case "cli":
    case "terminal":
      return "shellscript";
    case "python":
    case "py":
      return "python";
    case "codex":
    case "toml":
      return "toml";
    case "json":
    case "jsonc":
      return "json";
    default:
      return "text";
  }
}

function mapShikiToken(token: ThemedToken): ColabOneHighlightedToken {
  const style = token.htmlStyle ?? {};
  return {
    content: token.content,
    lightColor: style["--shiki-light"] || undefined,
    darkColor: style["--shiki-dark"] || undefined,
    lightFontStyle: style["--shiki-light-font-style"] || undefined,
    darkFontStyle: style["--shiki-dark-font-style"] || undefined,
    lightFontWeight: style["--shiki-light-font-weight"] || undefined,
    darkFontWeight: style["--shiki-dark-font-weight"] || undefined,
  };
}

function tokenizeWithShiki(
  code: string,
  lang: Exclude<ColabOneCodeLanguage, "text">,
): ColabOneHighlightedLine[] {
  const highlighter = getColabOneHighlighter();
  const { tokens } = highlighter.codeToTokens(code, {
    lang,
    themes: {
      light: LIGHT_THEME,
      dark: DARK_THEME,
    },
    defaultColor: false,
  });
  return tokens.map((line) => line.map(mapShikiToken));
}

interface ShellSegment {
  kind: "shell" | "quote" | "json";
  text: string;
}

function isEmbeddedJsonPayload(inner: string): boolean {
  const trimmed = inner.trim();
  if (trimmed.length < 2) return false;
  const isObject = trimmed.startsWith("{") && trimmed.endsWith("}");
  const isArray = trimmed.startsWith("[") && trimmed.endsWith("]");
  if (!isObject && !isArray) return false;
  try {
    const parsed = JSON.parse(trimmed);
    return typeof parsed === "object" && parsed !== null;
  } catch {
    return false;
  }
}

/**
 * Splits a shell command into POSIX shell segments and embedded single-quoted
 * JSON payloads (e.g. `curl -d '{ ... }'`), allowing Shiki's JSON grammar to
 * highlight request bodies while shellscript highlights commands, flags, URLs,
 * and environment variables.
 */
function splitShellEmbeddedJsonSegments(code: string): ShellSegment[] | null {
  const ranges: Array<{ quoteStart: number; quoteEnd: number }> = [];
  let mode: "normal" | "double" | "comment" = "normal";

  for (let i = 0; i < code.length; i += 1) {
    const ch = code[i];
    if (mode === "comment") {
      if (ch === "\n") mode = "normal";
      continue;
    }
    if (mode === "double") {
      if (ch === "\\") {
        i += 1;
      } else if (ch === '"') {
        mode = "normal";
      }
      continue;
    }
    if (ch === "\\") {
      i += 1;
      continue;
    }
    if (ch === "#") {
      const prev = i === 0 ? "\n" : code[i - 1];
      if (prev === "\n" || prev === " " || prev === "\t") {
        mode = "comment";
        continue;
      }
    }
    if (ch === '"') {
      mode = "double";
      continue;
    }
    if (ch === "'" && (i === 0 || code[i - 1] !== "$")) {
      const closeIdx = code.indexOf("'", i + 1);
      if (closeIdx === -1) break;
      const inner = code.slice(i + 1, closeIdx);
      if (isEmbeddedJsonPayload(inner)) {
        ranges.push({ quoteStart: i, quoteEnd: closeIdx });
      }
      i = closeIdx;
    }
  }

  if (!ranges.length) return null;

  const segments: ShellSegment[] = [];
  let cursor = 0;
  for (const range of ranges) {
    if (range.quoteStart > cursor) {
      segments.push({
        kind: "shell",
        text: code.slice(cursor, range.quoteStart),
      });
    }
    segments.push({ kind: "quote", text: "'" });
    segments.push({
      kind: "json",
      text: code.slice(range.quoteStart + 1, range.quoteEnd),
    });
    segments.push({ kind: "quote", text: "'" });
    cursor = range.quoteEnd + 1;
  }
  if (cursor < code.length) {
    segments.push({ kind: "shell", text: code.slice(cursor) });
  }
  return segments;
}

function appendHighlightedLines(
  target: ColabOneHighlightedLine[],
  addition: ColabOneHighlightedLine[],
) {
  if (!addition.length) return;
  if (!target.length) {
    for (const line of addition) {
      target.push([...line]);
    }
    return;
  }
  target[target.length - 1].push(...addition[0]);
  for (let i = 1; i < addition.length; i += 1) {
    target.push([...addition[i]]);
  }
}

function plainTextLines(code: string): ColabOneHighlightedLine[] {
  return code
    .split("\n")
    .map((line) => (line ? [{ content: line }] : []));
}

function rememberHighlight(
  cacheKey: string,
  lines: ColabOneHighlightedLine[],
): ColabOneHighlightedLine[] {
  if (highlightCache.size >= HIGHLIGHT_CACHE_LIMIT) {
    const oldestKey = highlightCache.keys().next().value;
    if (oldestKey !== undefined) {
      highlightCache.delete(oldestKey);
    }
  }
  highlightCache.set(cacheKey, lines);
  return lines;
}

/**
 * Synchronously tokenizes code into dual-theme (Light/Dark) structured tokens
 * using VS Code TextMate grammars via Shiki's zero-WASM JavaScript engine.
 */
export function highlightColabOneCode(
  rawCode: string,
  language?: string,
): ColabOneHighlightedLine[] {
  const code = String(rawCode ?? "").replace(/\r\n?/g, "\n");
  const normalized = normalizeColabOneCodeLanguage(language);

  if (!code) {
    return [[]];
  }
  if (normalized === "text" || code.length > MAX_HIGHLIGHT_CHARS) {
    return plainTextLines(code);
  }

  const cacheKey = `${normalized}\0${code}`;
  const cached = highlightCache.get(cacheKey);
  if (cached) {
    // Refresh recency for LRU eviction.
    highlightCache.delete(cacheKey);
    highlightCache.set(cacheKey, cached);
    return cached;
  }

  try {
    if (normalized === "shellscript") {
      const segments = splitShellEmbeddedJsonSegments(code);
      if (segments) {
        const merged: ColabOneHighlightedLine[] = [];
        for (const seg of segments) {
          if (!seg.text) continue;
          if (seg.kind === "quote") {
            appendHighlightedLines(merged, [
              [
                {
                  content: seg.text,
                  lightColor: QUOTE_LIGHT_COLOR,
                  darkColor: QUOTE_DARK_COLOR,
                },
              ],
            ]);
          } else if (seg.kind === "json") {
            appendHighlightedLines(
              merged,
              tokenizeWithShiki(seg.text, "json"),
            );
          } else {
            appendHighlightedLines(
              merged,
              tokenizeWithShiki(seg.text, "shellscript"),
            );
          }
        }
        return rememberHighlight(cacheKey, merged.length ? merged : [[]]);
      }
    }
    return rememberHighlight(cacheKey, tokenizeWithShiki(code, normalized));
  } catch {
    return plainTextLines(code);
  }
}
