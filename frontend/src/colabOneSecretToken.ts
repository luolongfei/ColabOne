export interface ColabOneExtractedSecret {
  /** Clean secret token extracted from the input text. */
  value: string;
  /** Whether the extracted value differs from the raw input text. */
  changed: boolean;
  /**
   * Whether surrounding command or wrapper text (beyond leading/trailing
   * whitespace) was stripped when extracting the token.
   */
  extractedFromWrapper: boolean;
}

export type ColabOneSecretExtractor = (
  text: string,
) => ColabOneExtractedSecret | null;

export interface ColabOneSecretPasteResolution {
  value: string;
  extractedFromWrapper: boolean;
}

const CLOUDFLARE_TUNNEL_TOKEN_RE = /eyJ[A-Za-z0-9_+/=-]{20,}/g;

/**
 * Shared token pattern for Hugging Face access tokens (`hf_...`), excluding
 * known non-secret HF library/env identifiers like `hf_transfer`, `hf_download`,
 * `hf_hub_download`, `hf_cache_home`, `hf_endpoint`, and `hf_xet`.
 */
export const COLABONE_HF_TOKEN_PATTERN =
  "hf_(?!(?:download|cache|hub|home|transfer|endpoint|xet|api_token|dl_cache|mirror|offline|repo|revision)(?:_|(?![A-Za-z0-9_-])))[A-Za-z0-9_-]{8,}";

const HUGGINGFACE_TOKEN_RE = new RegExp(
  `(?:(?<![A-Za-z0-9_-])|(?<=%[0-9A-Fa-f]{2}))(?:${COLABONE_HF_TOKEN_PATTERN})(?![A-Za-z0-9_-])`,
  "g",
);

/**
 * Shared token pattern for ColabOne workflow share keys (`co_wf_...`) and
 * output gallery share keys (`co_gal_...`).
 */
export const COLABONE_SHARE_TOKEN_PATTERN =
  "co_(?:wf|gal)_[A-Za-z0-9_-]+(?:\\.[A-Za-z0-9_-]+)+|co_(?:wf|gal)_[A-Za-z0-9_-]{8,}";

const COLABONE_SHARE_TOKEN_RE = new RegExp(
  `(?:(?<![A-Za-z0-9_-])|(?<=%[0-9A-Fa-f]{2}))(?:${COLABONE_SHARE_TOKEN_PATTERN})(?![A-Za-z0-9_-])`,
  "g",
);

const COLABONE_SHARE_TOKEN_EXACT_RE = new RegExp(
  `^(?:${COLABONE_SHARE_TOKEN_PATTERN})$`,
);

function decodeBase64JsonObject(token: string): Record<string, unknown> | null {
  try {
    const normalized = token.replace(/-/g, "+").replace(/_/g, "/");
    const padded = normalized + "=".repeat((4 - (normalized.length % 4)) % 4);
    const binary = atob(padded);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i += 1) {
      bytes[i] = binary.charCodeAt(i);
    }
    const decoded = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
    const parsed: unknown = JSON.parse(decoded);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return null;
    }
    return parsed as Record<string, unknown>;
  } catch {
    return null;
  }
}

/**
 * Returns true if the given standalone base64/base64url token decodes to a
 * Cloudflare Tunnel token JSON object containing non-empty `a`, `t`, and `s`
 * string fields.
 */
export function isColabOneCloudflareTunnelToken(candidate: string): boolean {
  if (typeof candidate !== "string" || !candidate.startsWith("eyJ")) {
    return false;
  }
  const payload = decodeBase64JsonObject(candidate);
  if (!payload) return false;
  for (const key of ["a", "t", "s"] as const) {
    const value = payload[key];
    if (typeof value !== "string" || !value) {
      return false;
    }
  }
  return true;
}

/**
 * Extracts a unique, validated Cloudflare Tunnel Token from a pasted install
 * command or token string. Matches the backend rules in `common/public_api.py`.
 */
export function colabOneExtractTunnelToken(
  raw: string,
): ColabOneExtractedSecret | null {
  if (typeof raw !== "string") return null;
  const trimmed = raw.trim();
  if (!trimmed) return null;

  const matches = Array.from(
    new Set(trimmed.match(CLOUDFLARE_TUNNEL_TOKEN_RE) ?? []),
  );
  if (matches.length !== 1) return null;

  const candidate = matches[0];
  const payload = decodeBase64JsonObject(candidate);
  if (!payload) return null;

  for (const key of ["a", "t", "s"] as const) {
    const value = payload[key];
    if (typeof value !== "string" || !value) {
      return null;
    }
  }

  return {
    value: candidate,
    changed: candidate !== raw,
    extractedFromWrapper: candidate !== trimmed,
  };
}

/**
 * Extracts a unique Hugging Face access token (`hf_...`) when pasted with
 * surrounding shell commands, quotes, Bearer prefixes, or whitespace.
 */
export function colabOneExtractHfToken(
  raw: string,
): ColabOneExtractedSecret | null {
  if (typeof raw !== "string") return null;
  const trimmed = raw.trim();
  if (!trimmed) return null;

  const matches = Array.from(new Set(trimmed.match(HUGGINGFACE_TOKEN_RE) ?? []));
  if (matches.length !== 1) return null;

  const candidate = matches[0];
  return {
    value: candidate,
    changed: candidate !== raw,
    extractedFromWrapper: candidate !== trimmed,
  };
}

/**
 * Returns true if the given standalone token matches a ColabOne workflow share
 * key (`co_wf_...`) or output gallery share key (`co_gal_...`).
 */
export function isColabOneShareToken(candidate: string): boolean {
  if (typeof candidate !== "string") return false;
  const trimmed = candidate.trim();
  if (!trimmed || trimmed !== candidate) return false;
  return COLABONE_SHARE_TOKEN_EXACT_RE.test(trimmed);
}

/**
 * Extracts a unique ColabOne share key (`co_wf_...` or `co_gal_...`) from a
 * pasted share URL or message string.
 */
export function colabOneExtractShareToken(
  raw: string,
): ColabOneExtractedSecret | null {
  if (typeof raw !== "string") return null;
  const trimmed = raw.trim();
  if (!trimmed) return null;

  const matches = Array.from(
    new Set(trimmed.match(COLABONE_SHARE_TOKEN_RE) ?? []),
  );
  if (matches.length !== 1) return null;

  const candidate = matches[0];
  return {
    value: candidate,
    changed: candidate !== raw,
    extractedFromWrapper: candidate !== trimmed,
  };
}

/**
 * Resolves a paste event into a clean extracted secret when the pasted text
 * (or the resulting merged string) contains a valid secret token.
 */
export function colabOneResolveSecretPaste(
  currentValue: string,
  selectionStart: number | null,
  selectionEnd: number | null,
  pastedText: string,
  extract?: ColabOneSecretExtractor,
): ColabOneSecretPasteResolution | null {
  if (!extract || !pastedText) return null;

  const direct = extract(pastedText);
  if (direct) {
    return {
      value: direct.value,
      extractedFromWrapper: direct.extractedFromWrapper,
    };
  }

  const start = selectionStart ?? currentValue.length;
  const end = selectionEnd ?? currentValue.length;
  const merged =
    currentValue.slice(0, start) + pastedText + currentValue.slice(end);
  const fromMerged = extract(merged);
  if (fromMerged && fromMerged.changed) {
    return {
      value: fromMerged.value,
      extractedFromWrapper: fromMerged.extractedFromWrapper,
    };
  }
  return null;
}

/**
 * Normalizes a non-paste input update (e.g. drag-drop, programmatic fill, or
 * clipboard managers that bypass `paste`) when wrapper text surrounds a valid token.
 */
export function colabOneResolveSecretInput(
  raw: string,
  extract?: ColabOneSecretExtractor,
): ColabOneSecretPasteResolution | null {
  if (!extract || !raw) return null;
  const result = extract(raw);
  if (!result || !result.changed) return null;
  return {
    value: result.value,
    extractedFromWrapper: result.extractedFromWrapper,
  };
}
