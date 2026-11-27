import { ref } from "vue";
import { COLABONE_STORAGE_KEYS } from "./storage";
import type {
  ColabOneHighlightedLine,
  ColabOneHighlightedToken,
} from "./colabOneHighlight";
import type { ColabOneSnapshot } from "./types";

export interface ColabOnePrivacyContext {
  emails?: string[];
  hosts?: string[];
}

export interface ColabOnePrivacySegment {
  text: string;
  masked: boolean;
  original?: string;
}

export interface ColabOneMaskedHighlightedToken extends ColabOneHighlightedToken {
  masked?: boolean;
  original?: string;
}

export type ColabOneMaskedHighlightedLine = ColabOneMaskedHighlightedToken[];

interface PrivacyMatchSpan {
  start: number;
  end: number;
  replacement: string;
}

const DUMMY_ALNUM = "m8k2p9r4q6x3n7v5c1t0h8b4d2f6g9j3w5y7z1";
const DUMMY_DIGITS = "844392716053";

const PUBLIC_HOST_ALLOWLIST = new Set([
  "localhost",
  "127.0.0.1",
  "0.0.0.0",
  "::1",
  "your-api-host",
  "api.example.com",
  "comfy.example.com",
  "example.com",
  "example.org",
  "example.net",
  "github.com",
  "api.github.com",
  "raw.githubusercontent.com",
  "huggingface.co",
  "hf.co",
  "colab.research.google.com",
  "console.cloud.google.com",
  "accounts.google.com",
  "one.dash.cloudflare.com",
  "dash.cloudflare.com",
  "cloudflare.com",
]);

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

function buildDummyWord(length: number, offset = 0): string {
  const count = Math.max(1, Math.round(length));
  let out = "";
  for (let i = 0; i < count; i += 1) {
    out += DUMMY_ALNUM[(offset + i) % DUMMY_ALNUM.length];
  }
  return out;
}

function buildDummyDigits(length: number, offset = 0): string {
  const count = Math.max(1, Math.round(length));
  let out = "";
  for (let i = 0; i < count; i += 1) {
    out += DUMMY_DIGITS[(offset + i) % DUMMY_DIGITS.length];
  }
  return out;
}

function normalizeHostForCheck(raw: string): string {
  let host = raw.trim().toLowerCase();
  if (host.startsWith("[")) {
    const close = host.indexOf("]");
    if (close > 0) host = host.slice(1, close);
  } else {
    const colonIdx = host.lastIndexOf(":");
    if (colonIdx > 0 && /^\d+$/.test(host.slice(colonIdx + 1))) {
      host = host.slice(0, colonIdx);
    }
  }
  return host.replace(/\.+$/, "");
}

export function isColabOnePublicHost(raw: string): boolean {
  const host = normalizeHostForCheck(raw);
  if (!host) return true;
  if (PUBLIC_HOST_ALLOWLIST.has(host)) return true;
  if (
    host.endsWith(".example.com") ||
    host.endsWith(".example.org") ||
    host.endsWith(".example.net") ||
    host.endsWith(".invalid")
  ) {
    return true;
  }
  return false;
}

function isColabOnePlaceholderEmailDomain(raw: string): boolean {
  const domain = normalizeHostForCheck(raw);
  if (!domain) return true;
  return (
    domain === "example.com" ||
    domain === "example.org" ||
    domain === "example.net" ||
    domain === "invalid" ||
    domain.endsWith(".example.com") ||
    domain.endsWith(".example.org") ||
    domain.endsWith(".example.net") ||
    domain.endsWith(".invalid")
  );
}

export function extractColabOneHostFromInput(raw: string | null | undefined): string {
  const text = String(raw ?? "").trim();
  if (!text) return "";
  try {
    const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(text)
      ? text
      : `https://${text}`;
    const parsed = new URL(withScheme);
    return parsed.hostname.toLowerCase();
  } catch {
    const stripped = text
      .replace(/^[a-z][a-z0-9+.-]*:\/\//i, "")
      .replace(/^[^/@]+@/, "")
      .split(/[/?#]/)[0];
    return normalizeHostForCheck(stripped);
  }
}

/**
 * Generates a deterministic, zero-secret, length-matched synthetic email
 * so Gaussian-blurred rendering preserves layout width without exposing
 * original characters to deblurring, OCR, or DOM inspection.
 */
export function colabOneSynthesizeEmail(raw: string): string {
  const text = String(raw ?? "").trim();
  if (!text) return "";
  const atIdx = text.lastIndexOf("@");
  if (atIdx <= 0 || atIdx >= text.length - 1) {
    return buildDummyWord(clamp(text.length, 6, 18), 3);
  }
  const local = text.slice(0, atIdx);
  const domain = text.slice(atIdx + 1);
  const dummyLocal = buildDummyWord(clamp(local.length, 5, 16), 0);
  const labels = domain.split(".").filter(Boolean);
  if (!labels.length) {
    return `${dummyLocal}@${buildDummyWord(clamp(domain.length, 5, 12), 7)}`;
  }
  const dummyDomain = labels
    .map((label, idx) =>
      buildDummyWord(clamp(label.length, 2, 10), idx * 5 + 7),
    )
    .join(".");
  return `${dummyLocal}@${dummyDomain}`;
}

/**
 * Generates a deterministic, zero-secret, length-matched synthetic host or IP
 * (optionally preserving the `:port` shape with dummy digits).
 */
export function colabOneSynthesizeHost(raw: string): string {
  const text = String(raw ?? "").trim();
  if (!text) return "";

  let userinfoPrefix = "";
  let hostPort = text;
  const atIdx = hostPort.lastIndexOf("@");
  if (atIdx >= 0) {
    userinfoPrefix = `${buildDummyWord(clamp(atIdx, 4, 8), 2)}@`;
    hostPort = hostPort.slice(atIdx + 1);
  }

  let portSuffix = "";
  let hostOnly = hostPort;
  if (hostOnly.startsWith("[")) {
    const closeIdx = hostOnly.indexOf("]");
    if (closeIdx > 0) {
      const rest = hostOnly.slice(closeIdx + 1);
      if (rest.startsWith(":") && /^\d+$/.test(rest.slice(1))) {
        portSuffix = `:${buildDummyDigits(rest.length - 1, 1)}`;
      }
      return `${userinfoPrefix}[2001:db8::${buildDummyWord(4, 9)}]${portSuffix}`;
    }
  } else {
    const colonIdx = hostOnly.lastIndexOf(":");
    if (colonIdx > 0 && /^\d+$/.test(hostOnly.slice(colonIdx + 1))) {
      portSuffix = `:${buildDummyDigits(hostOnly.length - colonIdx - 1, 1)}`;
      hostOnly = hostOnly.slice(0, colonIdx);
    }
  }

  if (/^(?:\d{1,3}\.){3}\d{1,3}$/.test(hostOnly)) {
    return `${userinfoPrefix}198.51.100.42${portSuffix}`;
  }

  const labels = hostOnly.split(".").filter(Boolean);
  if (labels.length > 1) {
    const dummyHost = labels
      .map((label, idx) =>
        buildDummyWord(clamp(label.length, 2, 12), idx * 7 + 4),
      )
      .join(".");
    return `${userinfoPrefix}${dummyHost}${portSuffix}`;
  }

  return `${userinfoPrefix}${buildDummyWord(clamp(hostOnly.length, 6, 16), 4)}${portSuffix}`;
}

export function colabOneSynthesizeToken(raw: string): string {
  const text = String(raw ?? "").trim();
  if (!text) return "";
  return buildDummyWord(clamp(text.length, 6, 20), 11);
}

/**
 * Splits a domain or URL input value (e.g. `https://colabone.acme.dev` or
 * `comfy.example.org`) into unmasked scheme/path parts and a masked authority
 * segment so domain input veils match inline frosted-acrylic pills.
 */
export function segmentColabOneDomainInput(
  raw: string,
): ColabOnePrivacySegment[] {
  const text = String(raw ?? "").trim();
  if (!text) return [];
  const match = text.match(/^([a-z][a-z0-9+.-]*:\/\/)([^/?#]+)(.*)$/i);
  if (match) {
    const [, scheme, authority, tail] = match;
    const segments: ColabOnePrivacySegment[] = [];
    if (scheme) segments.push({ text: scheme, masked: false });
    segments.push({
      text: colabOneSynthesizeHost(authority),
      masked: true,
      original: authority,
    });
    if (tail) segments.push({ text: tail, masked: false });
    return segments;
  }
  const slashIdx = text.search(/[/?#]/);
  if (slashIdx > 0) {
    const authority = text.slice(0, slashIdx);
    const tail = text.slice(slashIdx);
    const segments: ColabOnePrivacySegment[] = [
      {
        text: colabOneSynthesizeHost(authority),
        masked: true,
        original: authority,
      },
    ];
    if (tail) segments.push({ text: tail, masked: false });
    return segments;
  }
  return [
    {
      text: colabOneSynthesizeHost(text),
      masked: true,
      original: text,
    },
  ];
}

/**
 * Masks a domain or URL input value (e.g. `https://colabone.acme.dev` or
 * `comfy.example.org`) while preserving scheme/path shape and length.
 */
export function colabOneSynthesizeDomainInput(raw: string): string {
  return segmentColabOneDomainInput(raw)
    .map((seg) => seg.text)
    .join("");
}

export interface ColabOneTextSelectionRange {
  start: number;
  end: number;
}

/**
 * Applies pasted text directly onto the real value of a privacy-masked input
 * when a paste event arrives while the synthetic placeholder is rendered (for
 * example, via an OS clipboard picker right as the window regains focus).
 * Ensures synthetic mask glyphs can never leak into the saved value.
 */
export function colabOneApplyPrivacyInputPaste(
  currentValue: string,
  pastedText: string,
  selection: ColabOneTextSelectionRange | null = null,
): { value: string; caret: number } {
  const real = String(currentValue ?? "");
  const pasted = String(pastedText ?? "");
  if (!selection) {
    return { value: pasted, caret: pasted.length };
  }
  const start = clamp(Math.trunc(selection.start), 0, real.length);
  const end = clamp(Math.trunc(selection.end), start, real.length);
  const value = real.slice(0, start) + pasted + real.slice(end);
  return { value, caret: start + pasted.length };
}

const COLABONE_SECRET_TOKEN_RE =
  /\b(?:co_wf_[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+|hf_[A-Za-z0-9]{10,}|eyJ[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{10,}(?:\.[A-Za-z0-9_-]+)?|sk-[A-Za-z0-9_-]{16,}|(?:sk|co)_[A-Za-z0-9_-]{16,})\b/g;

const COLABONE_SENSITIVE_QUERY_PARAM_RE =
  /([?&](?:k|ticket|token)=)([^&#\s"'<>)\]}]+)/gi;

function resolvePrivacyMatchSpans(
  matches: PrivacyMatchSpan[],
): PrivacyMatchSpan[] {
  if (!matches.length) return [];
  // Sort by start ascending, then longest span first so wider matches (e.g.
  // user:pass@host:port) beat narrower inner host matches.
  matches.sort((a, b) => a.start - b.start || b.end - a.end);
  const resolved: PrivacyMatchSpan[] = [];
  let lastEnd = -1;
  for (const span of matches) {
    if (span.start < lastEnd) continue;
    resolved.push(span);
    lastEnd = span.end;
  }
  return resolved;
}

function findColabOneSensitiveUrlTailMatches(tail: string): PrivacyMatchSpan[] {
  if (!tail) return [];
  const matches: PrivacyMatchSpan[] = [];
  for (const m of tail.matchAll(COLABONE_SENSITIVE_QUERY_PARAM_RE)) {
    if (m.index === undefined || !m[1] || !m[2]) continue;
    const start = m.index + m[1].length;
    matches.push({
      start,
      end: start + m[2].length,
      replacement: colabOneSynthesizeToken(m[2]),
    });
  }
  for (const m of tail.matchAll(COLABONE_SECRET_TOKEN_RE)) {
    if (m.index === undefined) continue;
    matches.push({
      start: m.index,
      end: m.index + m[0].length,
      replacement: colabOneSynthesizeToken(m[0]),
    });
  }
  return resolvePrivacyMatchSpans(matches);
}

function segmentColabOneUrlTail(tail: string): ColabOnePrivacySegment[] {
  if (!tail) return [];
  const spans = findColabOneSensitiveUrlTailMatches(tail);
  if (!spans.length) {
    return [{ text: tail, masked: false }];
  }
  const out: ColabOnePrivacySegment[] = [];
  let cursor = 0;
  for (const span of spans) {
    if (span.start > cursor) {
      out.push({ text: tail.slice(cursor, span.start), masked: false });
    }
    out.push({
      text: span.replacement,
      masked: true,
      original: tail.slice(span.start, span.end),
    });
    cursor = span.end;
  }
  if (cursor < tail.length) {
    out.push({ text: tail.slice(cursor), masked: false });
  }
  return out;
}

function buildUrlSegmentsWithTail(
  scheme: string,
  authority: string,
  tail: string,
  maskAuthority: boolean,
): ColabOnePrivacySegment[] {
  const tailSegments = segmentColabOneUrlTail(tail);
  if (maskAuthority) {
    const segments: ColabOnePrivacySegment[] = [];
    if (scheme) segments.push({ text: scheme, masked: false });
    segments.push({
      text: colabOneSynthesizeHost(authority),
      masked: true,
      original: authority,
    });
    if (tailSegments.length) segments.push(...tailSegments);
    return segments;
  }
  if (!tailSegments.some((seg) => seg.masked)) {
    return [{ text: `${scheme}${authority}${tail}`, masked: false }];
  }
  const prefix = `${scheme}${authority}`;
  if (tailSegments.length && !tailSegments[0].masked) {
    return [
      { text: `${prefix}${tailSegments[0].text}`, masked: false },
      ...tailSegments.slice(1),
    ];
  }
  return [{ text: prefix, masked: false }, ...tailSegments];
}

/**
 * Splits a URL (e.g. `https://colabone.acme.dev/v1`) into unmasked scheme/path
 * segments and masked host / sensitive token segments. Leaves generic
 * placeholder hosts intact while always masking share keys and auth tokens.
 */
export function segmentColabOneUrlText(url: string): ColabOnePrivacySegment[] {
  const text = String(url ?? "");
  if (!text) return [];
  const match = text.match(/^([a-z][a-z0-9+.-]*:\/\/)([^/?#\s]+)(.*)$/i);
  if (!match) {
    const slashIdx = text.search(/[/?#]/);
    const authority = slashIdx >= 0 ? text.slice(0, slashIdx) : text;
    const tail = slashIdx >= 0 ? text.slice(slashIdx) : "";
    const hasUserinfo = authority.includes("@");
    const host = extractColabOneHostFromInput(authority);
    const maskAuthority = hasUserinfo || (Boolean(host) && !isColabOnePublicHost(host));
    return buildUrlSegmentsWithTail("", authority, tail, maskAuthority);
  }
  const [, scheme, authority, tail] = match;
  const hasUserinfo = authority.includes("@");
  const host = extractColabOneHostFromInput(authority);
  const maskAuthority = hasUserinfo || (Boolean(host) && !isColabOnePublicHost(host));
  return buildUrlSegmentsWithTail(scheme, authority, tail, maskAuthority);
}

export function buildColabOnePrivacyContext(
  snapshot: Partial<ColabOneSnapshot> | null | undefined,
): ColabOnePrivacyContext {
  const emails = new Set<string>();
  const hosts = new Set<string>();

  const email = String(snapshot?.account?.email ?? "").trim();
  if (email) emails.add(email);

  const candidates = [
    snapshot?.api_settings?.domain,
    snapshot?.runtime?.public_api?.url,
    snapshot?.comfy_canvas_settings?.domain,
    snapshot?.runtime?.comfyui?.canvas?.url,
    snapshot?.assign_proxy_settings?.masked_url,
  ];
  for (const candidate of candidates) {
    const host = extractColabOneHostFromInput(candidate);
    if (host && !isColabOnePublicHost(host)) {
      hosts.add(host);
    }
  }

  return {
    emails: Array.from(emails),
    hosts: Array.from(hosts),
  };
}

export function colabOnePrivacyContextKey(
  context: ColabOnePrivacyContext | undefined,
): string {
  if (!context) return "";
  const emails = (context.emails || []).join("\u001f");
  const hosts = (context.hosts || []).join("\u001f");
  return `${emails}\u001e${hosts}`;
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function isBoundaryWordChar(ch: string | undefined): boolean {
  return Boolean(ch && /[A-Za-z0-9_-]/.test(ch));
}

function findColabOnePrivacyMatches(
  text: string,
  context?: ColabOnePrivacyContext,
): PrivacyMatchSpan[] {
  if (!text) return [];
  const matches: PrivacyMatchSpan[] = [];

  // 1. Known personal emails from active snapshot
  for (const email of context?.emails || []) {
    const needle = email.trim();
    if (!needle) continue;
    const re = new RegExp(escapeRegExp(needle), "gi");
    for (const m of text.matchAll(re)) {
      if (m.index === undefined) continue;
      matches.push({
        start: m.index,
        end: m.index + m[0].length,
        replacement: colabOneSynthesizeEmail(m[0]),
      });
    }
  }

  // 2. Generic emails
  const emailRe = /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/g;
  for (const m of text.matchAll(emailRe)) {
    if (m.index === undefined) continue;
    const raw = m[0];
    const domain = raw.slice(raw.lastIndexOf("@") + 1).toLowerCase();
    if (isColabOnePlaceholderEmailDomain(domain)) continue;
    matches.push({
      start: m.index,
      end: m.index + raw.length,
      replacement: colabOneSynthesizeEmail(raw),
    });
  }

  // 3. URLs (HTTP/HTTPS/SOCKS/WS) — mask authority (userinfo + host + port) when private.
  // Restrict userinfo to RFC-3986/masked chars so CJK text or punctuation after a URL can
  // never be swallowed into a later email address on the same line.
  const urlRe =
    /\b((?:https?|socks5h?|socks4a?|wss?):\/\/)((?:[A-Za-z0-9._~:%*+-]+@)?(?:\[[0-9a-fA-F:]+\]|[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*)(?::\d{1,5})?)/gi;
  for (const m of text.matchAll(urlRe)) {
    if (m.index === undefined) continue;
    const schemePrefix = m[1];
    const authority = m[2];
    if (!schemePrefix || !authority) continue;
    const hasUserinfo = authority.includes("@");
    const host = extractColabOneHostFromInput(authority);
    if (!hasUserinfo && isColabOnePublicHost(host)) continue;
    const start = m.index + schemePrefix.length;
    matches.push({
      start,
      end: start + authority.length,
      replacement: colabOneSynthesizeHost(authority),
    });
  }

  // 4. Known personal hosts from active snapshot (even without scheme)
  for (const host of context?.hosts || []) {
    const needle = host.trim();
    if (!needle || isColabOnePublicHost(needle)) continue;
    const re = new RegExp(`${escapeRegExp(needle)}(?::\\d{1,5})?`, "gi");
    for (const m of text.matchAll(re)) {
      if (m.index === undefined) continue;
      const before = text[m.index - 1];
      const after = text[m.index + m[0].length];
      if (isBoundaryWordChar(before) || isBoundaryWordChar(after)) continue;
      matches.push({
        start: m.index,
        end: m.index + m[0].length,
        replacement: colabOneSynthesizeHost(m[0]),
      });
    }
  }

  // 5. IPv4 addresses (excluding 127.0.0.1 and 0.0.0.0)
  const ipv4Re =
    /(?<![\d.])(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)(?::\d{1,5})?(?![\d.])/g;
  for (const m of text.matchAll(ipv4Re)) {
    if (m.index === undefined) continue;
    const ipOnly = m[0].split(":")[0];
    if (ipOnly === "127.0.0.1" || ipOnly === "0.0.0.0") continue;
    matches.push({
      start: m.index,
      end: m.index + m[0].length,
      replacement: colabOneSynthesizeHost(m[0]),
    });
  }

  // 6. GCP Drive quota project IDs in localized log entries
  const projectRe =
    /(?:配额项目|配額專案|配額項目|quota project|クォータプロジェクト|할당량 프로젝트)\s*[(\uff08]([A-Za-z0-9][A-Za-z0-9._-]{2,62})[)\uff09]/gi;
  for (const m of text.matchAll(projectRe)) {
    if (m.index === undefined || !m[1]) continue;
    const innerStart = m.index + m[0].length - m[1].length - 1;
    matches.push({
      start: innerStart,
      end: innerStart + m[1].length,
      replacement: colabOneSynthesizeToken(m[1]),
    });
  }

  // 7. Defense-in-depth secret tokens (including workflow share keys) if ever surfaced
  for (const m of text.matchAll(COLABONE_SECRET_TOKEN_RE)) {
    if (m.index === undefined) continue;
    matches.push({
      start: m.index,
      end: m.index + m[0].length,
      replacement: colabOneSynthesizeToken(m[0]),
    });
  }

  // 8. Sensitive URL query parameters (share keys, one-time tickets, auth tokens)
  for (const m of text.matchAll(COLABONE_SENSITIVE_QUERY_PARAM_RE)) {
    if (m.index === undefined || !m[1] || !m[2]) continue;
    const start = m.index + m[1].length;
    matches.push({
      start,
      end: start + m[2].length,
      replacement: colabOneSynthesizeToken(m[2]),
    });
  }

  return resolvePrivacyMatchSpans(matches);
}

export function segmentColabOnePrivacyText(
  text: string,
  context?: ColabOnePrivacyContext,
): ColabOnePrivacySegment[] {
  const raw = String(text ?? "");
  if (!raw) return [];
  const spans = findColabOnePrivacyMatches(raw, context);
  if (!spans.length) {
    return [{ text: raw, masked: false }];
  }
  const out: ColabOnePrivacySegment[] = [];
  let cursor = 0;
  for (const span of spans) {
    if (span.start > cursor) {
      out.push({ text: raw.slice(cursor, span.start), masked: false });
    }
    out.push({
      text: span.replacement,
      masked: true,
      original: raw.slice(span.start, span.end),
    });
    cursor = span.end;
  }
  if (cursor < raw.length) {
    out.push({ text: raw.slice(cursor), masked: false });
  }
  return out;
}

export function colabOneMaskPlainText(
  text: string,
  context?: ColabOnePrivacyContext,
): string {
  return segmentColabOnePrivacyText(text, context)
    .map((seg) => seg.text)
    .join("");
}

/**
 * Projects line-level privacy match spans onto Shiki-highlighted code tokens
 * while preserving syntax colors and line structure even when Shiki splits a
 * URL or domain across multiple tokens.
 */
export function applyColabOnePrivacyToCodeLines(
  lines: ColabOneHighlightedLine[],
  active: boolean,
  context?: ColabOnePrivacyContext,
): ColabOneMaskedHighlightedLine[] {
  if (!active) {
    return lines;
  }

  return lines.map((line) => {
    if (!line.length) return [];
    const joined = line.map((tok) => tok.content).join("");
    const spans = findColabOnePrivacyMatches(joined, context);
    if (!spans.length) return line;

    // Build character-offset table for each token on this line.
    const tokenRanges: Array<{
      token: ColabOneHighlightedToken;
      start: number;
      end: number;
    }> = [];
    let pos = 0;
    for (const token of line) {
      const end = pos + token.content.length;
      tokenRanges.push({ token, start: pos, end });
      pos = end;
    }

    const result: ColabOneMaskedHighlightedLine = [];
    let spanIdx = 0;

    const emitPlainSlice = (sliceStart: number, sliceEnd: number) => {
      if (sliceEnd <= sliceStart) return;
      for (const tr of tokenRanges) {
        if (tr.end <= sliceStart) continue;
        if (tr.start >= sliceEnd) break;
        const overlapStart = Math.max(tr.start, sliceStart);
        const overlapEnd = Math.min(tr.end, sliceEnd);
        if (overlapEnd > overlapStart) {
          result.push({
            ...tr.token,
            content: joined.slice(overlapStart, overlapEnd),
            masked: false,
          });
        }
      }
    };

    let cursor = 0;
    while (spanIdx < spans.length) {
      const span = spans[spanIdx];
      emitPlainSlice(cursor, span.start);

      const styleOwner =
        tokenRanges.find(
          (tr) => tr.end > span.start && tr.start < span.end,
        )?.token ?? line[0];

      result.push({
        content: span.replacement,
        original: joined.slice(span.start, span.end),
        color: styleOwner?.color,
        fontStyle: styleOwner?.fontStyle,
        fontWeight: styleOwner?.fontWeight,
        masked: true,
      });

      cursor = span.end;
      spanIdx += 1;
    }

    emitPlainSlice(cursor, joined.length);
    return result;
  });
}

// ---------------------------------------------------------------------------
// Reactive privacy mask state & cross-window synchronization
// ---------------------------------------------------------------------------

function isLogWindowView(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return new URLSearchParams(window.location.search).get("view") === "logs";
  } catch {
    return false;
  }
}

function parseBoolFlag(raw: string | null): boolean {
  return raw === "1" || raw === "true";
}

function readStoredPrivacyDefault(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return parseBoolFlag(
      window.localStorage.getItem(COLABONE_STORAGE_KEYS.privacyMaskDefault),
    );
  } catch {
    return false;
  }
}

function readStoredPrivacySession(): boolean | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(
      COLABONE_STORAGE_KEYS.privacyMaskSession,
    );
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    if (
      parsed &&
      typeof parsed === "object" &&
      typeof (parsed as { active?: unknown }).active === "boolean"
    ) {
      return (parsed as { active: boolean }).active;
    }
  } catch {
    // Ignore malformed session payload.
  }
  return null;
}

function writeStoredPrivacySession(active: boolean): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(
      COLABONE_STORAGE_KEYS.privacyMaskSession,
      JSON.stringify({ active, updatedAt: Date.now() }),
    );
  } catch {
    // Ignore restricted storage contexts.
  }
}

function writeStoredPrivacyDefault(enabled: boolean): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(
      COLABONE_STORAGE_KEYS.privacyMaskDefault,
      enabled ? "1" : "0",
    );
  } catch {
    // Ignore restricted storage contexts.
  }
}

export function initColabOnePrivacyState(options?: {
  isLogView?: boolean;
}): { defaultEnabled: boolean; active: boolean } {
  const nextDefault = readStoredPrivacyDefault();
  const inLogView =
    options?.isLogView !== undefined ? options.isLogView : isLogWindowView();
  const nextActive = inLogView
    ? (readStoredPrivacySession() ?? nextDefault)
    : nextDefault;

  colabOnePrivacyDefault.value = nextDefault;
  colabOnePrivacyActive.value = nextActive;

  if (!inLogView) {
    writeStoredPrivacySession(nextActive);
  }
  return { defaultEnabled: nextDefault, active: nextActive };
}

/**
 * Default-on-startup preference configured in Settings.
 * Persists across launches; does not mutate the active session when toggled.
 */
export const colabOnePrivacyDefault = ref<boolean>(false);

/**
 * Active privacy mask state for the current desktop session.
 * Initialized from `colabOnePrivacyDefault` on main window launch and synced
 * in real time to companion windows (e.g. standalone Logs window).
 */
export const colabOnePrivacyActive = ref<boolean>(false);

/**
 * Monotonic signal bumped whenever any hover-to-peek reveal should immediately
 * collapse (window blur, tab hidden, or mask toggle), avoiding per-element
 * global window event listeners across hundreds of log entries.
 */
export const colabOnePrivacyPeekResetTick = ref<number>(0);

export function resetColabOnePrivacyPeeks(): void {
  colabOnePrivacyPeekResetTick.value += 1;
}

initColabOnePrivacyState();

/**
 * Sets the active privacy mask state for the current session only.
 * Never mutates the saved "default on startup" preference.
 */
export function setColabOnePrivacyActive(next: boolean): void {
  const normalized = Boolean(next);
  colabOnePrivacyActive.value = normalized;
  resetColabOnePrivacyPeeks();
  writeStoredPrivacySession(normalized);
}

export function toggleColabOnePrivacyActive(): boolean {
  const next = !colabOnePrivacyActive.value;
  setColabOnePrivacyActive(next);
  return next;
}

/**
 * Updates the persisted "default on startup" setting without altering the
 * current session's active mask state.
 */
export function setColabOnePrivacyDefault(next: boolean): void {
  const normalized = Boolean(next);
  colabOnePrivacyDefault.value = normalized;
  writeStoredPrivacyDefault(normalized);
}

if (typeof window !== "undefined") {
  window.addEventListener("blur", resetColabOnePrivacyPeeks);
  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", () => {
      if (document.hidden) {
        resetColabOnePrivacyPeeks();
      }
    });
  }
  window.addEventListener("storage", (event) => {
    if (event.key === COLABONE_STORAGE_KEYS.privacyMaskDefault) {
      colabOnePrivacyDefault.value = parseBoolFlag(event.newValue);
      return;
    }
    if (event.key === COLABONE_STORAGE_KEYS.privacyMaskSession) {
      const next = readStoredPrivacySession();
      if (next !== null) {
        colabOnePrivacyActive.value = next;
        resetColabOnePrivacyPeeks();
      }
    }
  });
}
