import type {
  ColabOneRollbackVersion,
  ColabOneUpdateState,
} from "./colabOneBridge";
import { COLABONE_APP_VERSION } from "./colabOneVersion";

export type { ColabOneRollbackVersion, ColabOneUpdateState };

export const COLABONE_RELEASES_URL =
  "https://github.com/luolongfei/ColabOne/releases";

export interface ColabOneNoteBlock {
  kind: "heading" | "item" | "paragraph";
  text: string;
}

export interface ColabOneNoteInline {
  text: string;
  code: boolean;
}

const COLABONE_KIB = 1024;
const COLABONE_MIB = 1024 * 1024;
const COLABONE_GIB = 1024 * 1024 * 1024;

function toNonNegativeNumber(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    return 0;
  }
  return value;
}

function clampProgressPercent(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return 0;
  }
  if (value < 0) {
    return 0;
  }
  if (value > 100) {
    return 100;
  }
  return value;
}

function pickFirstNonBlank(...candidates: Array<string | undefined>): string {
  for (const candidate of candidates) {
    if (typeof candidate === "string" && candidate.trim().length > 0) {
      return candidate;
    }
  }
  return "";
}

function stripMarkdownBold(text: string): string {
  return text.replace(/\*\*(.+?)\*\*/g, "$1");
}

export function normalizeColabOneRollbackVersions(
  raw: ReadonlyArray<Partial<ColabOneRollbackVersion>> | null | undefined,
): ColabOneRollbackVersion[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const seen = new Set<string>();
  const out: ColabOneRollbackVersion[] = [];
  for (const item of raw) {
    if (!item || typeof item !== "object") {
      continue;
    }
    const version = normalizeColabOneUpdateVersion(item.version);
    if (!version || seen.has(version)) {
      continue;
    }
    seen.add(version);
    const tag =
      typeof item.tag === "string" && item.tag.trim()
        ? item.tag.trim()
        : `v${version}`;
    const releaseUrl =
      typeof item.release_url === "string" && item.release_url.trim()
        ? item.release_url.trim()
        : `${COLABONE_RELEASES_URL}/tag/v${version}`;
    out.push({
      version,
      tag,
      release_date:
        typeof item.release_date === "string" ? item.release_date.trim() : "",
      release_url: releaseUrl,
      is_prerelease: Boolean(item.is_prerelease),
      can_rollback: Boolean(item.can_rollback),
    });
  }
  return out;
}

export function formatColabOneRollbackDate(
  raw: string | null | undefined,
): string {
  const trimmed = String(raw ?? "").trim();
  if (!trimmed) {
    return "";
  }
  const parsed = new Date(trimmed);
  if (Number.isNaN(parsed.getTime())) {
    return "";
  }
  const year = parsed.getUTCFullYear();
  const month = String(parsed.getUTCMonth() + 1).padStart(2, "0");
  const day = String(parsed.getUTCDate()).padStart(2, "0");
  return `${year}/${month}/${day}`;
}

export function createColabOneUpdateState(
  currentVersion = COLABONE_APP_VERSION,
): ColabOneUpdateState {
  const resolvedVersion =
    typeof currentVersion === "string" && currentVersion.trim()
      ? currentVersion.trim()
      : COLABONE_APP_VERSION;
  return {
    status: "idle",
    current_version: resolvedVersion,
    available_version: "",
    is_prerelease: false,
    is_rollback: false,
    release_date: "",
    release_url: COLABONE_RELEASES_URL,
    notes_raw: "",
    notes_zh: "",
    notes_en: "",
    downloaded_bytes: 0,
    total_bytes: 0,
    progress_percent: 0,
    speed_bps: 0,
    can_self_update: false,
    disabled_reason: "",
    auto_check: true,
    include_prerelease: false,
    skipped_version: "",
    last_checked_at: 0,
    error_code: "",
    error_message: "",
    rollback_versions: [],
  };
}

export function normalizeColabOneUpdateState(
  raw: Partial<ColabOneUpdateState> | null | undefined,
): ColabOneUpdateState {
  const base = createColabOneUpdateState();
  if (!raw || typeof raw !== "object") {
    return base;
  }
  return {
    status:
      typeof raw.status === "string" && raw.status.trim()
        ? raw.status.trim()
        : base.status,
    current_version:
      typeof raw.current_version === "string" && raw.current_version.trim()
        ? raw.current_version.trim()
        : base.current_version,
    available_version:
      typeof raw.available_version === "string"
        ? raw.available_version.trim()
        : base.available_version,
    is_prerelease:
      typeof raw.is_prerelease === "boolean"
        ? raw.is_prerelease
        : base.is_prerelease,
    is_rollback:
      typeof raw.is_rollback === "boolean"
        ? raw.is_rollback
        : base.is_rollback,
    release_date:
      typeof raw.release_date === "string"
        ? raw.release_date
        : base.release_date,
    release_url:
      typeof raw.release_url === "string" && raw.release_url.trim()
        ? raw.release_url.trim()
        : base.release_url,
    notes_raw:
      typeof raw.notes_raw === "string" ? raw.notes_raw : base.notes_raw,
    notes_zh: typeof raw.notes_zh === "string" ? raw.notes_zh : base.notes_zh,
    notes_en: typeof raw.notes_en === "string" ? raw.notes_en : base.notes_en,
    downloaded_bytes: toNonNegativeNumber(raw.downloaded_bytes),
    total_bytes: toNonNegativeNumber(raw.total_bytes),
    progress_percent: clampProgressPercent(raw.progress_percent),
    speed_bps: toNonNegativeNumber(raw.speed_bps),
    can_self_update:
      typeof raw.can_self_update === "boolean"
        ? raw.can_self_update
        : base.can_self_update,
    disabled_reason:
      typeof raw.disabled_reason === "string"
        ? raw.disabled_reason
        : base.disabled_reason,
    auto_check:
      typeof raw.auto_check === "boolean" ? raw.auto_check : base.auto_check,
    include_prerelease:
      typeof raw.include_prerelease === "boolean"
        ? raw.include_prerelease
        : base.include_prerelease,
    skipped_version:
      typeof raw.skipped_version === "string"
        ? raw.skipped_version.trim()
        : base.skipped_version,
    last_checked_at: toNonNegativeNumber(raw.last_checked_at),
    error_code:
      typeof raw.error_code === "string" ? raw.error_code : base.error_code,
    error_message:
      typeof raw.error_message === "string"
        ? raw.error_message
        : base.error_message,
    rollback_versions: normalizeColabOneRollbackVersions(raw.rollback_versions),
  };
}

export function parseColabOneReleaseNotes(
  markdown: string,
): ColabOneNoteBlock[] {
  const normalized = String(markdown ?? "").replace(/\r\n?/g, "\n");
  if (!normalized.trim()) {
    return [];
  }

  const blocks: ColabOneNoteBlock[] = [];
  for (const rawLine of normalized.split("\n")) {
    const trimmed = rawLine.trim();
    if (!trimmed) {
      continue;
    }
    if (/^[-*_]{3,}$/.test(trimmed)) {
      continue;
    }
    if (/^(?:#{1,6}|[-*•])$/.test(trimmed)) {
      continue;
    }

    const headingMatch = trimmed.match(/^#{1,6}\s+(.+)$/);
    if (headingMatch) {
      const text = stripMarkdownBold(headingMatch[1].trim()).trim();
      if (text) {
        blocks.push({ kind: "heading", text });
      }
      continue;
    }

    const itemMatch = trimmed.match(/^(?:[-*•])\s+(.+)$/);
    if (itemMatch) {
      const text = stripMarkdownBold(itemMatch[1].trim()).trim();
      if (text) {
        blocks.push({ kind: "item", text });
      }
      continue;
    }

    const text = stripMarkdownBold(trimmed).trim();
    if (text) {
      blocks.push({ kind: "paragraph", text });
    }
  }
  return blocks;
}

export function splitColabOneInlineCode(text: string): ColabOneNoteInline[] {
  const input = String(text ?? "");
  if (!input) {
    return [];
  }

  const segments: ColabOneNoteInline[] = [];
  const pattern = /`([^`]+)`/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;

  while ((match = pattern.exec(input)) !== null) {
    if (match.index > lastIndex) {
      segments.push({
        text: input.slice(lastIndex, match.index),
        code: false,
      });
    }
    segments.push({
      text: match[1],
      code: true,
    });
    lastIndex = pattern.lastIndex;
  }

  if (lastIndex < input.length) {
    segments.push({
      text: input.slice(lastIndex),
      code: false,
    });
  }

  return segments;
}

export function selectColabOneUpdateNotes(
  state: ColabOneUpdateState,
  locale: string,
  showFull: boolean,
): string {
  if (showFull) {
    return pickFirstNonBlank(state.notes_raw, state.notes_zh, state.notes_en);
  }
  const normalizedLocale = String(locale ?? "").trim().toLowerCase();
  if (normalizedLocale.startsWith("zh")) {
    return pickFirstNonBlank(state.notes_zh, state.notes_raw, state.notes_en);
  }
  return pickFirstNonBlank(state.notes_en, state.notes_raw, state.notes_zh);
}

export function hasColabOneBilingualNotes(
  state: ColabOneUpdateState,
): boolean {
  const zh = String(state.notes_zh ?? "").trim();
  const en = String(state.notes_en ?? "").trim();
  return zh.length > 0 && en.length > 0 && zh !== en;
}

export function formatColabOneUpdateBytes(bytes: number): string {
  if (typeof bytes !== "number" || !Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  if (bytes < COLABONE_KIB) {
    return `${Math.round(bytes)} B`;
  }
  if (bytes < COLABONE_MIB) {
    return `${Math.round(bytes / COLABONE_KIB)} KB`;
  }
  if (bytes < COLABONE_GIB) {
    return `${(bytes / COLABONE_MIB).toFixed(1)} MB`;
  }
  return `${(bytes / COLABONE_GIB).toFixed(2)} GB`;
}

export function formatColabOneUpdateSpeed(bps: number): string {
  if (typeof bps !== "number" || !Number.isFinite(bps) || bps <= 0) {
    return "";
  }
  return `${formatColabOneUpdateBytes(bps)}/s`;
}

export function resolveColabOneUpdateErrorKey(errorCode: string): string {
  const code = String(errorCode ?? "").trim().toLowerCase();
  switch (code) {
    case "":
      return "";
    case "proxy":
      return "update.errProxy";
    case "timeout":
      return "update.errTimeout";
    case "tls":
      return "update.errTls";
    case "network":
      return "update.errNetwork";
    case "not_found":
      return "update.errNotFound";
    case "rate_limited":
      return "update.errRateLimited";
    case "signature":
      return "update.errSignature";
    case "permission":
      return "update.errPermission";
    default:
      return "update.errUnknown";
  }
}

export function normalizeColabOneUpdateVersion(
  version: string | null | undefined,
): string {
  return String(version ?? "")
    .trim()
    .replace(/^[vV]+/, "");
}

export function hasColabOneAvailableUpdateDot(
  state: ColabOneUpdateState,
): boolean {
  if (state.status !== "available") {
    return false;
  }
  const available = normalizeColabOneUpdateVersion(state.available_version);
  if (!available) {
    return false;
  }
  const skipped = normalizeColabOneUpdateVersion(state.skipped_version);
  if (skipped && available === skipped) {
    return false;
  }
  return true;
}

export function hasColabOneActiveUpdatePill(
  state: ColabOneUpdateState,
): boolean {
  return (
    state.status === "downloading" ||
    state.status === "installing" ||
    state.status === "ready_to_restart"
  );
}

export function hasColabOneVisibleUpdateBadge(
  state: ColabOneUpdateState,
): boolean {
  switch (state.status) {
    case "available":
      return String(state.available_version ?? "").trim().length > 0;
    case "downloading":
    case "installing":
    case "ready_to_restart":
      return true;
    default:
      return false;
  }
}

export function shouldColabOneAutoCheckOnPopoverOpen(
  state: ColabOneUpdateState,
  checking = false,
): boolean {
  if (checking) {
    return false;
  }
  switch (state.status) {
    case "available":
    case "downloading":
    case "installing":
    case "ready_to_restart":
    case "checking":
    case "disabled":
      return false;
    default:
      return true;
  }
}
