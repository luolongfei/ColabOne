import { ref } from "vue";
import { isColabOneInspectOutput } from "./colabOneOutputGallery";
import {
  COLABONE_WEB_CLIENT_HEADER,
  COLABONE_WEB_CLIENT_VALUE,
  decodeColabOneErrorHeader,
  getColabOneWebAuthEpoch,
  isColabOneDesktopRuntime,
  markColabOneWebSessionAlive,
  notifyColabOneWebAuthRequired,
} from "./colabOneWebConsole";

export const COLABONE_OUTPUT_SHARE_ROUTE_PATH = "/gallery";
export const COLABONE_OUTPUT_SHARE_CLIENT_HEADER = "X-ColabOne-Client";
export const COLABONE_OUTPUT_SHARE_CLIENT_HEADER_VALUE = "web-gallery";
export const COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT =
  "colabone:output-share-auth-required";

export type ColabOneOutputShareTTLMode = "24h" | "7d" | "30d" | "forever";
export type ColabOneOutputShareStatus = "active" | "expired" | "revoked";

export interface ColabOneOutputShareGroupInput {
  id?: string;
  title: string;
  workflow_id?: string;
  workflow_name?: string;
  task_id?: string;
  output_ids: string[];
}

export interface ColabOneOutputShareCreateRequest {
  title: string;
  note?: string;
  groups: ColabOneOutputShareGroupInput[];
  ttl_mode: ColabOneOutputShareTTLMode;
  allow_download: boolean;
  password?: string;
}

export interface ColabOneOutputShareSummary {
  id: string;
  title: string;
  note: string;
  ttl_mode: ColabOneOutputShareTTLMode;
  status: ColabOneOutputShareStatus;
  allow_download: boolean;
  has_password: boolean;
  password?: string;
  created_at: number;
  expires_at: number;
  revoked_at?: number;
  active_sessions: number;
  group_count: number;
  item_count: number;
  output_ids?: string[];
  url?: string;
  local_url?: string;
  uses_public_host: boolean;
  can_copy_link: boolean;
}

export interface ColabOneOutputShareListState {
  shares: ColabOneOutputShareSummary[];
  active_count: number;
  total_active_sessions: number;
}

export interface ColabOneOutputShareManifestItem {
  id: string;
  filename: string;
  extension?: string;
  kind: "image" | "video" | "audio";
  mime: string;
  size: number;
  workflow_id: string;
  workflow_name: string;
  created_at: number;
  task_id: string;
  is_inspect: boolean;
  available: boolean;
}

export interface ColabOneOutputShareManifestGroup {
  id: string;
  title: string;
  workflow_id: string;
  workflow_name: string;
  task_id: string;
  item_count?: number;
  available_count?: number;
  items: ColabOneOutputShareManifestItem[];
}

export interface ColabOneOutputShareManifest {
  runtime: string;
  version: string;
  share_id: string;
  title: string;
  allow_download: boolean;
  expires_at: number;
  created_at: number;
  group_count?: number;
  item_count?: number;
  available_count?: number;
  groups: ColabOneOutputShareManifestGroup[];
}

export const colabOneOutputShareMode = ref(false);
export const colabOneOutputShareSessionExpired = ref(false);

const VALID_TTL_MODES: ReadonlySet<ColabOneOutputShareTTLMode> = new Set([
  "24h",
  "7d",
  "30d",
  "forever",
]);
const VALID_STATUSES: ReadonlySet<ColabOneOutputShareStatus> = new Set([
  "active",
  "expired",
  "revoked",
]);
const VALID_MEDIA_KINDS: ReadonlySet<"image" | "video" | "audio"> = new Set([
  "image",
  "video",
  "audio",
]);

/**
 * Unambiguous alphanumeric alphabet (excludes 0/O/o and 1/I/i/l) for
 * human-readable share access passwords.
 */
const SHARE_PASSWORD_ALPHABET =
  "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789";

let galleryAuthEpoch = 0;

export function isColabOneOutputSharePath(pathname?: string): boolean {
  const raw =
    pathname !== undefined
      ? String(pathname)
      : typeof window !== "undefined"
        ? String(window.location?.pathname || "")
        : "";
  const cleanPath = raw.split(/[?#]/)[0] || "";
  return (
    cleanPath === COLABONE_OUTPUT_SHARE_ROUTE_PATH ||
    cleanPath.startsWith(`${COLABONE_OUTPUT_SHARE_ROUTE_PATH}/`)
  );
}

export function isColabOneOutputShareActive(): boolean {
  return (
    colabOneOutputShareMode.value ||
    (!isColabOneDesktopRuntime() && isColabOneOutputSharePath())
  );
}

export function createColabOneDefaultOutputShareListState(): ColabOneOutputShareListState {
  return {
    shares: [],
    active_count: 0,
    total_active_sessions: 0,
  };
}

function normalizeNonNegativeInt(raw: unknown): number {
  return typeof raw === "number" && Number.isFinite(raw) && raw > 0
    ? Math.floor(raw)
    : 0;
}

function normalizeNonNegativeNumber(raw: unknown): number {
  return typeof raw === "number" && Number.isFinite(raw) && raw > 0 ? raw : 0;
}

export function normalizeColabOneOutputShareSummary(
  raw: unknown,
): ColabOneOutputShareSummary | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const rec = raw as Record<string, unknown>;
  const id = typeof rec.id === "string" ? rec.id.trim() : "";
  if (!id) {
    return null;
  }

  const rawTTL = typeof rec.ttl_mode === "string" ? rec.ttl_mode.trim() : "";
  const ttlMode: ColabOneOutputShareTTLMode = VALID_TTL_MODES.has(
    rawTTL as ColabOneOutputShareTTLMode,
  )
    ? (rawTTL as ColabOneOutputShareTTLMode)
    : "7d";

  const rawStatus = typeof rec.status === "string" ? rec.status.trim() : "";
  const status: ColabOneOutputShareStatus = VALID_STATUSES.has(
    rawStatus as ColabOneOutputShareStatus,
  )
    ? (rawStatus as ColabOneOutputShareStatus)
    : "expired";

  const password =
    typeof rec.password === "string" && rec.password.trim()
      ? rec.password.trim()
      : "";
  const rawGroupCount =
    rec.group_count !== undefined
      ? normalizeNonNegativeInt(rec.group_count)
      : Array.isArray(rec.groups)
        ? rec.groups.length
        : 0;
  const rawItemCount =
    rec.item_count !== undefined
      ? normalizeNonNegativeInt(rec.item_count)
      : rec.output_count !== undefined
        ? normalizeNonNegativeInt(rec.output_count)
        : 0;

  const summary: ColabOneOutputShareSummary = {
    id,
    title: typeof rec.title === "string" ? rec.title.trim() : "",
    note: typeof rec.note === "string" ? rec.note.trim() : "",
    ttl_mode: ttlMode,
    status,
    allow_download:
      typeof rec.allow_download === "boolean" ? rec.allow_download : true,
    has_password:
      typeof rec.has_password === "boolean"
        ? rec.has_password
        : Boolean(password),
    created_at: normalizeNonNegativeNumber(rec.created_at),
    expires_at: normalizeNonNegativeNumber(rec.expires_at),
    active_sessions: normalizeNonNegativeInt(rec.active_sessions),
    group_count: rawGroupCount,
    item_count: rawItemCount,
    uses_public_host: Boolean(rec.uses_public_host),
    can_copy_link: Boolean(rec.can_copy_link),
  };

  if (Array.isArray(rec.output_ids)) {
    const outIds: string[] = [];
    const seenIds = new Set<string>();
    for (const rawOid of rec.output_ids) {
      if (typeof rawOid !== "string") continue;
      const trimmed = rawOid.trim();
      if (!trimmed || seenIds.has(trimmed)) continue;
      seenIds.add(trimmed);
      outIds.push(trimmed);
    }
    summary.output_ids = outIds;
  }
  if (password) {
    summary.password = password;
  }
  if (
    typeof rec.revoked_at === "number" &&
    Number.isFinite(rec.revoked_at) &&
    rec.revoked_at > 0
  ) {
    summary.revoked_at = rec.revoked_at;
  }
  if (typeof rec.url === "string" && rec.url.trim()) {
    summary.url = rec.url.trim();
  }
  if (typeof rec.local_url === "string" && rec.local_url.trim()) {
    summary.local_url = rec.local_url.trim();
  }

  return summary;
}

export function normalizeColabOneOutputShareListState(
  raw: unknown,
): ColabOneOutputShareListState {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return createColabOneDefaultOutputShareListState();
  }
  const rec = raw as Record<string, unknown>;
  const hasSharesArray = Array.isArray(rec.shares);
  const shares: ColabOneOutputShareSummary[] = [];
  if (hasSharesArray) {
    const seen = new Set<string>();
    for (const item of rec.shares as unknown[]) {
      const normalized = normalizeColabOneOutputShareSummary(item);
      if (!normalized || seen.has(normalized.id)) continue;
      seen.add(normalized.id);
      shares.push(normalized);
    }
  }

  const computedActiveCount = shares.filter(
    (item) => item.status === "active",
  ).length;
  const computedTotalSessions = shares.reduce(
    (sum, item) => sum + item.active_sessions,
    0,
  );

  const activeCount = hasSharesArray
    ? computedActiveCount
    : typeof rec.active_count === "number" &&
        Number.isFinite(rec.active_count) &&
        rec.active_count >= 0
      ? Math.floor(rec.active_count)
      : 0;

  const totalActiveSessions = hasSharesArray
    ? computedTotalSessions
    : typeof rec.total_active_sessions === "number" &&
        Number.isFinite(rec.total_active_sessions) &&
        rec.total_active_sessions >= 0
      ? Math.floor(rec.total_active_sessions)
      : 0;

  return {
    shares,
    active_count: activeCount,
    total_active_sessions: totalActiveSessions,
  };
}

function normalizeManifestItem(
  raw: unknown,
): ColabOneOutputShareManifestItem | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const rec = raw as Record<string, unknown>;
  const id = typeof rec.id === "string" ? rec.id.trim() : "";
  if (!id) {
    return null;
  }
  const rawKind = typeof rec.kind === "string" ? rec.kind.trim() : "";
  const kind: "image" | "video" | "audio" = VALID_MEDIA_KINDS.has(
    rawKind as "image" | "video" | "audio",
  )
    ? (rawKind as "image" | "video" | "audio")
    : "image";
  const filename = typeof rec.filename === "string" ? rec.filename.trim() : "";
  const extension =
    typeof rec.extension === "string" ? rec.extension.trim() : "";
  const isInspect =
    typeof rec.is_inspect === "boolean"
      ? rec.is_inspect
      : isColabOneInspectOutput({ filename });

  return {
    id,
    filename,
    ...(extension ? { extension } : {}),
    kind,
    mime: typeof rec.mime === "string" ? rec.mime.trim() : "",
    size: normalizeNonNegativeNumber(rec.size),
    workflow_id: typeof rec.workflow_id === "string" ? rec.workflow_id.trim() : "",
    workflow_name:
      typeof rec.workflow_name === "string" ? rec.workflow_name.trim() : "",
    created_at: normalizeNonNegativeNumber(rec.created_at),
    task_id: typeof rec.task_id === "string" ? rec.task_id.trim() : "",
    is_inspect: isInspect,
    available: typeof rec.available === "boolean" ? rec.available : true,
  };
}

function normalizeManifestGroup(
  raw: unknown,
  fallbackIndex: number,
): ColabOneOutputShareManifestGroup | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const rec = raw as Record<string, unknown>;
  const rawItems = Array.isArray(rec.items) ? rec.items : [];
  const items: ColabOneOutputShareManifestItem[] = [];
  const seenItemIds = new Set<string>();
  for (const rawItem of rawItems) {
    const normalized = normalizeManifestItem(rawItem);
    if (!normalized || seenItemIds.has(normalized.id)) continue;
    seenItemIds.add(normalized.id);
    items.push(normalized);
  }
  if (items.length === 0) {
    return null;
  }
  const id =
    typeof rec.id === "string" && rec.id.trim()
      ? rec.id.trim()
      : `group-${fallbackIndex + 1}`;
  const workflowId =
    (typeof rec.workflow_id === "string" ? rec.workflow_id.trim() : "") ||
    items[0].workflow_id;
  const workflowName =
    (typeof rec.workflow_name === "string" ? rec.workflow_name.trim() : "") ||
    items[0].workflow_name;
  const taskId =
    (typeof rec.task_id === "string" ? rec.task_id.trim() : "") ||
    items[0].task_id;
  const title =
    (typeof rec.title === "string" ? rec.title.trim() : "") ||
    workflowName ||
    items[0].filename ||
    id;

  const computedAvailableCount = items.filter(
    (item) => item.available !== false,
  ).length;
  const itemCount =
    typeof rec.item_count === "number" &&
    Number.isFinite(rec.item_count) &&
    rec.item_count >= 0
      ? Math.floor(rec.item_count)
      : items.length;
  const availableCount =
    typeof rec.available_count === "number" &&
    Number.isFinite(rec.available_count) &&
    rec.available_count >= 0
      ? Math.floor(rec.available_count)
      : computedAvailableCount;

  return {
    id,
    title,
    workflow_id: workflowId,
    workflow_name: workflowName,
    task_id: taskId,
    item_count: itemCount,
    available_count: availableCount,
    items,
  };
}

export function normalizeColabOneOutputShareManifest(
  raw: unknown,
): ColabOneOutputShareManifest {
  const rec =
    raw && typeof raw === "object" && !Array.isArray(raw)
      ? (raw as Record<string, unknown>)
      : {};
  const groups: ColabOneOutputShareManifestGroup[] = [];
  if (Array.isArray(rec.groups)) {
    const seenGroupIds = new Set<string>();
    for (let i = 0; i < rec.groups.length; i += 1) {
      const normalized = normalizeManifestGroup(rec.groups[i], i);
      if (!normalized || seenGroupIds.has(normalized.id)) continue;
      seenGroupIds.add(normalized.id);
      groups.push(normalized);
    }
  }
  const computedItemCount = groups.reduce((sum, g) => sum + g.items.length, 0);
  const computedAvailableCount = groups.reduce(
    (sum, g) => sum + g.items.filter((it) => it.available !== false).length,
    0,
  );
  const groupCount =
    typeof rec.group_count === "number" &&
    Number.isFinite(rec.group_count) &&
    rec.group_count >= 0
      ? Math.floor(rec.group_count)
      : groups.length;
  const itemCount =
    typeof rec.item_count === "number" &&
    Number.isFinite(rec.item_count) &&
    rec.item_count >= 0
      ? Math.floor(rec.item_count)
      : computedItemCount;
  const availableCount =
    typeof rec.available_count === "number" &&
    Number.isFinite(rec.available_count) &&
    rec.available_count >= 0
      ? Math.floor(rec.available_count)
      : computedAvailableCount;

  return {
    runtime:
      typeof rec.runtime === "string" && rec.runtime.trim()
        ? rec.runtime.trim()
        : COLABONE_OUTPUT_SHARE_CLIENT_HEADER_VALUE,
    version: typeof rec.version === "string" ? rec.version.trim() : "",
    share_id: typeof rec.share_id === "string" ? rec.share_id.trim() : "",
    title: typeof rec.title === "string" ? rec.title.trim() : "",
    allow_download:
      typeof rec.allow_download === "boolean" ? rec.allow_download : true,
    expires_at: normalizeNonNegativeNumber(rec.expires_at),
    created_at: normalizeNonNegativeNumber(rec.created_at),
    group_count: groupCount,
    item_count: itemCount,
    available_count: availableCount,
    groups,
  };
}

/**
 * Generates a cryptographically strong, easy-to-read password using an
 * unambiguous alphanumeric alphabet (excluding 0/O/o and 1/I/i/l) with
 * rejection sampling to eliminate modulo bias.
 */
export function generateColabOneSharePassword(length = 8): string {
  const targetLength = Math.max(4, Math.min(64, Math.floor(length || 8)));
  const alphabetLen = SHARE_PASSWORD_ALPHABET.length;
  const maxValidByte = Math.floor(256 / alphabetLen) * alphabetLen;
  const out: string[] = [];

  const cryptoApi =
    typeof globalThis !== "undefined" ? globalThis.crypto : undefined;
  if (cryptoApi && typeof cryptoApi.getRandomValues === "function") {
    const buf = new Uint8Array(Math.max(16, targetLength * 2));
    while (out.length < targetLength) {
      cryptoApi.getRandomValues(buf);
      for (let i = 0; i < buf.length && out.length < targetLength; i += 1) {
        if (buf[i] < maxValidByte) {
          out.push(SHARE_PASSWORD_ALPHABET[buf[i] % alphabetLen]);
        }
      }
    }
    return out.join("");
  }

  for (let i = 0; i < targetLength; i += 1) {
    out.push(
      SHARE_PASSWORD_ALPHABET[Math.floor(Math.random() * alphabetLen)],
    );
  }
  return out.join("");
}

function notifyColabOneOutputShareAuthRequired(): void {
  galleryAuthEpoch += 1;
  colabOneOutputShareSessionExpired.value = true;
  if (
    typeof window !== "undefined" &&
    typeof window.dispatchEvent === "function"
  ) {
    const evt =
      typeof CustomEvent === "function"
        ? new CustomEvent(COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT)
        : new Event(COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT);
    window.dispatchEvent(evt);
  }
}

async function extractResponseError(
  res: Response,
  signal?: AbortSignal,
): Promise<string> {
  try {
    const text = (await res.text()).trim();
    if (text) {
      try {
        const parsed = JSON.parse(text) as { error?: unknown };
        if (typeof parsed?.error === "string" && parsed.error.trim()) {
          return parsed.error.trim();
        }
      } catch {
        // Non-JSON payload; fall through.
      }
      if (!/^<!doctype\s+html|^<html/i.test(text)) {
        return text;
      }
    }
  } catch (err) {
    if (err instanceof Error && err.name === "AbortError") {
      throw err;
    }
    if (signal?.aborted) {
      throw new DOMException("Aborted", "AbortError");
    }
  }
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }
  const encodedHeaderErr = (res.headers?.get("X-ColabOne-Error") || "").trim();
  if (encodedHeaderErr) {
    const decoded = decodeColabOneErrorHeader(encodedHeaderErr);
    if (decoded) {
      return decoded;
    }
  }
  return `HTTP ${res.status}`;
}

function sanitizeDownloadFilename(raw: string): string {
  const cleaned = raw
    .replace(/[\x00-\x1f\x7f-\x9f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/g, "")
    .trim()
    .replace(/^["']+|["']+$/g, "")
    .trim()
    .split(/[\\/]/)
    .pop()
    ?.replace(/^["']+|["']+$/g, "")
    .trim();
  if (!cleaned || /^\.+$/.test(cleaned) || cleaned.startsWith(".")) {
    return "";
  }
  return cleaned;
}

export function sanitizeColabOneZipStem(raw: string, fallback: string): string {
  const safe = String(raw || "")
    .replace(/[\x00-\x1f\x7f-\x9f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/g, "")
    .replace(/[\\/:*?"<>|]+/g, "-")
    .replace(/^\.+|\.+$/g, "")
    .trim();
  return safe || fallback;
}

function parseContentDispositionFilename(header: string | null): string {
  if (!header) return "";
  const extMatch = header.match(
    /(?:^|[;\s])filename\*\s*=\s*(?:UTF-8|utf-8)'[^']*'([^;]+)/i,
  );
  if (extMatch?.[1]) {
    try {
      const decoded = decodeURIComponent(
        extMatch[1].trim().replace(/^["']|["']$/g, ""),
      );
      const safe = sanitizeDownloadFilename(decoded);
      if (safe) return safe;
    } catch {
      // Fall through to plain filename parameter.
    }
  }
  const quotedMatch = header.match(
    /(?:^|[;\s])filename\s*=\s*"((?:[^"\\]|\\.)+)"/i,
  );
  if (quotedMatch?.[1]) {
    const unescaped = quotedMatch[1].replace(/\\(.)/g, "$1");
    const safe = sanitizeDownloadFilename(unescaped);
    if (safe) return safe;
  }
  const bareMatch = header.match(/(?:^|[;\s])filename\s*=\s*([^;]+)/i);
  if (bareMatch?.[1]) {
    const safe = sanitizeDownloadFilename(bareMatch[1]);
    if (safe) return safe;
  }
  return "";
}

function clickHiddenDownloadAnchor(href: string, filename?: string): void {
  if (typeof document === "undefined") {
    return;
  }
  const link = document.createElement("a");
  link.href = href;
  if (filename) {
    link.download = filename;
  }
  link.rel = "noopener";
  link.style.display = "none";
  document.body?.appendChild(link);
  link.click();
  link.remove();
}

export async function fetchColabOneOutputShareManifest(
  signal?: AbortSignal,
): Promise<ColabOneOutputShareManifest> {
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }
  const reqAuthEpoch = galleryAuthEpoch;
  const res = await fetch("/api/gallery/manifest", {
    method: "GET",
    credentials: "same-origin",
    headers: {
      [COLABONE_OUTPUT_SHARE_CLIENT_HEADER]:
        COLABONE_OUTPUT_SHARE_CLIENT_HEADER_VALUE,
    },
    ...(signal ? { signal } : {}),
  });
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }

  if (res.status === 401) {
    notifyColabOneOutputShareAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractResponseError(res, signal));
  }

  const payload = (await res.json()) as unknown;
  const manifest = normalizeColabOneOutputShareManifest(payload);
  if (reqAuthEpoch === galleryAuthEpoch) {
    colabOneOutputShareSessionExpired.value = false;
  }
  return manifest;
}

export function colabOneOutputShareMediaURL(id: string): string {
  return `/api/gallery/outputs/${encodeURIComponent(String(id || "").trim())}/media`;
}

export function colabOneOutputShareDownloadURL(id: string): string {
  return `/api/gallery/outputs/${encodeURIComponent(String(id || "").trim())}/download`;
}

export function colabOneOutputShareZipURL(groupId?: string): string {
  const cleanGroup = typeof groupId === "string" ? groupId.trim() : "";
  if (!cleanGroup) {
    return "/api/gallery/download.zip";
  }
  return `/api/gallery/download.zip?group=${encodeURIComponent(cleanGroup)}`;
}

/**
 * Probes a gallery media URL with HEAD after a browser media element emits an
 * error event so mid-session 401 revocations and 503/404 offline items can be
 * distinguished cleanly.
 */
export async function probeColabOneOutputShareMediaError(
  id: string,
  signal?: AbortSignal,
): Promise<"unauthorized" | "offline" | "error"> {
  const cleanId = String(id || "").trim();
  if (!cleanId || signal?.aborted) return "error";
  try {
    const res = await fetch(colabOneOutputShareMediaURL(cleanId), {
      method: "HEAD",
      credentials: "same-origin",
      headers: {
        [COLABONE_OUTPUT_SHARE_CLIENT_HEADER]:
          COLABONE_OUTPUT_SHARE_CLIENT_HEADER_VALUE,
      },
      ...(signal ? { signal } : {}),
    });
    if (signal?.aborted) {
      return "error";
    }
    if (res.status === 401) {
      notifyColabOneOutputShareAuthRequired();
      return "unauthorized";
    }
    if (res.status === 503 || res.status === 404 || res.status === 410) {
      return "offline";
    }
    return "error";
  } catch {
    return "error";
  }
}

export interface ColabOneBrowserDownloadOptions {
  clientHeaderValue?: string;
  fallbackFilename?: string;
  signal?: AbortSignal;
}

/**
 * Triggers a verified browser file download, checking session validity (401)
 * via a lightweight HEAD preflight before initiating the download anchor.
 * Falls back cleanly to a GET blob download if the endpoint rejects HEAD (405).
 */
export async function triggerColabOneBrowserDownload(
  url: string,
  options?: ColabOneBrowserDownloadOptions,
): Promise<string> {
  const signal = options?.signal;
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }
  const cleanUrl = String(url || "").trim();
  if (!cleanUrl) {
    throw new Error("Download URL is empty");
  }
  const isGalleryEndpoint = cleanUrl.startsWith("/api/gallery/");
  const clientHeaderValue =
    options?.clientHeaderValue ||
    (isGalleryEndpoint
      ? COLABONE_OUTPUT_SHARE_CLIENT_HEADER_VALUE
      : COLABONE_WEB_CLIENT_VALUE);
  const reqAuthEpoch = galleryAuthEpoch;
  const reqWebEpoch = getColabOneWebAuthEpoch();

  const headRes = await fetch(cleanUrl, {
    method: "HEAD",
    credentials: "same-origin",
    headers: {
      [COLABONE_OUTPUT_SHARE_CLIENT_HEADER]: clientHeaderValue,
    },
    ...(signal ? { signal } : {}),
  });
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }

  if (headRes.status === 401) {
    if (isGalleryEndpoint) {
      notifyColabOneOutputShareAuthRequired();
    } else {
      notifyColabOneWebAuthRequired();
    }
    throw new Error("Unauthorized");
  }

  if (headRes.status === 405) {
    const getRes = await fetch(cleanUrl, {
      method: "GET",
      credentials: "same-origin",
      headers: {
        [COLABONE_OUTPUT_SHARE_CLIENT_HEADER]: clientHeaderValue,
      },
      ...(signal ? { signal } : {}),
    });
    if (signal?.aborted) {
      throw new DOMException("Aborted", "AbortError");
    }
    if (getRes.status === 401) {
      if (isGalleryEndpoint) {
        notifyColabOneOutputShareAuthRequired();
      } else {
        notifyColabOneWebAuthRequired();
      }
      throw new Error("Unauthorized");
    }
    if (!getRes.ok) {
      throw new Error(await extractResponseError(getRes, signal));
    }
    const safeFallback = options?.fallbackFilename
      ? sanitizeDownloadFilename(options.fallbackFilename)
      : "";
    const filename =
      parseContentDispositionFilename(
        getRes.headers.get("Content-Disposition"),
      ) ||
      safeFallback ||
      "ColabOne-download";
    const blob = await getRes.blob();
    if (signal?.aborted) {
      throw new DOMException("Aborted", "AbortError");
    }
    if (isGalleryEndpoint) {
      if (reqAuthEpoch !== galleryAuthEpoch || colabOneOutputShareSessionExpired.value) {
        throw new Error("Unauthorized");
      }
      colabOneOutputShareSessionExpired.value = false;
    } else {
      if (reqWebEpoch !== getColabOneWebAuthEpoch()) {
        throw new Error("Unauthorized");
      }
      markColabOneWebSessionAlive(reqWebEpoch);
    }
    if (
      typeof URL !== "undefined" &&
      typeof URL.createObjectURL === "function"
    ) {
      const objectUrl = URL.createObjectURL(blob);
      try {
        clickHiddenDownloadAnchor(objectUrl, filename);
      } finally {
        setTimeout(() => {
          URL.revokeObjectURL(objectUrl);
        }, 60_000);
      }
    }
    return filename;
  }

  if (!headRes.ok) {
    throw new Error(await extractResponseError(headRes, signal));
  }
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }

  if (isGalleryEndpoint) {
    if (reqAuthEpoch !== galleryAuthEpoch || colabOneOutputShareSessionExpired.value) {
      throw new Error("Unauthorized");
    }
    colabOneOutputShareSessionExpired.value = false;
  } else {
    if (reqWebEpoch !== getColabOneWebAuthEpoch()) {
      throw new Error("Unauthorized");
    }
    markColabOneWebSessionAlive(reqWebEpoch);
  }

  const safeFallback = options?.fallbackFilename
    ? sanitizeDownloadFilename(options.fallbackFilename)
    : "";
  const filename =
    parseContentDispositionFilename(
      headRes.headers.get("Content-Disposition"),
    ) ||
    safeFallback ||
    "";
  clickHiddenDownloadAnchor(cleanUrl, filename || undefined);
  return filename;
}

// ---------------------------------------------------------------------------
// Host Web Console API Helpers (browser mode without Desktop MyGo IPC)
// ---------------------------------------------------------------------------

async function colabOneWebOutputShareFetchJson(
  url: string,
  init: RequestInit,
): Promise<unknown> {
  const reqWebEpoch = getColabOneWebAuthEpoch();
  const res = await fetch(url, {
    ...init,
    credentials: "same-origin",
    headers: {
      [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE,
      ...(init.headers || {}),
    },
  });
  if (res.status === 401) {
    notifyColabOneWebAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractResponseError(res));
  }
  const data = (await res.json()) as unknown;
  markColabOneWebSessionAlive(reqWebEpoch);
  return data;
}

export async function colabOneWebGetOutputSharesState(): Promise<ColabOneOutputShareListState> {
  const payload = await colabOneWebOutputShareFetchJson("/api/output-shares", {
    method: "GET",
  });
  return normalizeColabOneOutputShareListState(payload);
}

export async function colabOneWebCreateOutputShare(
  req: ColabOneOutputShareCreateRequest,
): Promise<ColabOneOutputShareSummary> {
  const payload = await colabOneWebOutputShareFetchJson("/api/output-shares", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(req),
  });
  const normalized = normalizeColabOneOutputShareSummary(payload);
  if (!normalized) {
    throw new Error("Invalid output share response");
  }
  return normalized;
}

export async function colabOneWebRevokeOutputShare(
  id: string,
): Promise<ColabOneOutputShareListState> {
  const cleanId = encodeURIComponent(String(id || "").trim());
  const payload = await colabOneWebOutputShareFetchJson(
    `/api/output-shares/${cleanId}/revoke`,
    {
      method: "POST",
    },
  );
  return normalizeColabOneOutputShareListState(payload);
}

export async function colabOneWebDeleteOutputShare(
  id: string,
): Promise<ColabOneOutputShareListState> {
  const cleanId = encodeURIComponent(String(id || "").trim());
  const payload = await colabOneWebOutputShareFetchJson(
    `/api/output-shares/${cleanId}`,
    {
      method: "DELETE",
    },
  );
  return normalizeColabOneOutputShareListState(payload);
}

export function colabOneWebDownloadOutputsZipURL(
  ids: readonly string[],
  name?: string,
): string {
  const cleanIds: string[] = [];
  const seen = new Set<string>();
  for (const rawId of ids || []) {
    if (typeof rawId !== "string") continue;
    const trimmed = rawId.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    cleanIds.push(trimmed);
  }
  const params = new URLSearchParams();
  if (cleanIds.length > 0) {
    params.set("ids", cleanIds.join(","));
  }
  const cleanName = typeof name === "string" ? name.trim() : "";
  if (cleanName) {
    params.set("name", cleanName);
  }
  const qs = params.toString();
  return qs ? `/api/outputs/zip?${qs}` : "/api/outputs/zip";
}
