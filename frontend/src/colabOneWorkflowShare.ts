import { ref } from "vue";
import {
  COLABONE_WEB_CLIENT_HEADER,
  COLABONE_WEB_CLIENT_VALUE,
  getColabOneWebAuthEpoch,
  isColabOneDesktopRuntime,
  markColabOneWebSessionAlive,
  notifyColabOneWebAuthRequired,
} from "./colabOneWebConsole";

export type ColabOneWorkflowShareTTLMode = "once" | "24h" | "7d" | "forever";
export type ColabOneWorkflowShareStatus =
  | "active"
  | "expired"
  | "used"
  | "revoked";

export interface ColabOneWorkflowShareCreateRequest {
  label: string;
  title: string;
  workflow_ids: string[];
  ttl_mode: ColabOneWorkflowShareTTLMode;
}

export interface ColabOneWorkflowShareSummary {
  id: string;
  label: string;
  title: string;
  workflow_ids: string[];
  ttl_mode: ColabOneWorkflowShareTTLMode;
  single_use: boolean;
  status: ColabOneWorkflowShareStatus;
  created_at: number;
  expires_at: number;
  revoked_at?: number;
  consumed_at?: number;
  active_sessions: number;
  output_count: number;
  url?: string;
  local_url?: string;
  uses_public_host: boolean;
  can_copy_link: boolean;
}

export interface ColabOneWorkflowShareListState {
  shares: ColabOneWorkflowShareSummary[];
  active_count: number;
  total_active_sessions: number;
}

export interface ColabOneWorkflowShareCapabilities {
  runtime: "web-share";
  version: string;
  share_id: string;
  title: string;
  allowed_workflow_ids: string[];
  can_download_output: boolean;
  can_delete_output: boolean;
  can_export_to_host_drive: boolean;
}

export const COLABONE_SHARE_CLIENT_HEADER = "X-ColabOne-Client";
export const COLABONE_SHARE_CLIENT_VALUE = "web-share";
export const COLABONE_SHARE_AUTH_REQUIRED_EVENT =
  "colabone:workflow-share-auth-required";

export const colabOneWorkflowShareMode = ref(false);
export const colabOneWorkflowShareSessionExpired = ref(false);
export const colabOneWorkflowShareId = ref("");
export const colabOneWorkflowShareCapabilities =
  ref<ColabOneWorkflowShareCapabilities | null>(null);

const COLABONE_OUTPUT_ID_RE = /^[a-f0-9]{32}$/;
const VALID_TTL_MODES: ReadonlySet<ColabOneWorkflowShareTTLMode> = new Set([
  "once",
  "24h",
  "7d",
  "forever",
]);
const VALID_STATUSES: ReadonlySet<ColabOneWorkflowShareStatus> = new Set([
  "active",
  "expired",
  "used",
  "revoked",
]);

let cachedShareSnapshotETag = "";
let cachedShareSnapshotRaw = "";
let shareSnapshotReqSeq = 0;
let shareSnapshotAppliedSeq = 0;
let shareSnapshotEpoch = 0;
let shareAuthEpoch = 0;

export function isColabOneWorkflowSharePath(pathname?: string): boolean {
  const raw =
    pathname !== undefined
      ? String(pathname)
      : typeof window !== "undefined"
        ? String(window.location?.pathname || "")
        : "";
  const cleanPath = raw.split(/[?#]/)[0] || "";
  return cleanPath === "/share" || cleanPath.startsWith("/share/");
}

export function isColabOneWorkflowShareActive(): boolean {
  return (
    colabOneWorkflowShareMode.value ||
    (!isColabOneDesktopRuntime() && isColabOneWorkflowSharePath())
  );
}

export function createColabOneDefaultWorkflowShareListState(): ColabOneWorkflowShareListState {
  return {
    shares: [],
    active_count: 0,
    total_active_sessions: 0,
  };
}

function normalizeStringList(raw: unknown): string[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  const seen = new Set<string>();
  const result: string[] = [];
  for (const item of raw) {
    if (typeof item !== "string") continue;
    const trimmed = item.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    result.push(trimmed);
  }
  return result;
}

function normalizeNonNegativeInt(raw: unknown): number {
  return typeof raw === "number" && Number.isFinite(raw) && raw > 0
    ? Math.floor(raw)
    : 0;
}

function normalizeNonNegativeNumber(raw: unknown): number {
  return typeof raw === "number" && Number.isFinite(raw) && raw > 0 ? raw : 0;
}

export function normalizeColabOneWorkflowShareSummary(
  raw: unknown,
): ColabOneWorkflowShareSummary | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const rec = raw as Record<string, unknown>;
  const id = typeof rec.id === "string" ? rec.id.trim() : "";
  if (!id) {
    return null;
  }

  const rawTTL = typeof rec.ttl_mode === "string" ? rec.ttl_mode.trim() : "";
  const ttlMode: ColabOneWorkflowShareTTLMode = VALID_TTL_MODES.has(
    rawTTL as ColabOneWorkflowShareTTLMode,
  )
    ? (rawTTL as ColabOneWorkflowShareTTLMode)
    : "24h";

  const rawStatus = typeof rec.status === "string" ? rec.status.trim() : "";
  const status: ColabOneWorkflowShareStatus = VALID_STATUSES.has(
    rawStatus as ColabOneWorkflowShareStatus,
  )
    ? (rawStatus as ColabOneWorkflowShareStatus)
    : "expired";

  const summary: ColabOneWorkflowShareSummary = {
    id,
    label: typeof rec.label === "string" ? rec.label.trim() : "",
    title: typeof rec.title === "string" ? rec.title.trim() : "",
    workflow_ids: normalizeStringList(rec.workflow_ids),
    ttl_mode: ttlMode,
    single_use:
      typeof rec.single_use === "boolean" ? rec.single_use : ttlMode === "once",
    status,
    created_at: normalizeNonNegativeNumber(rec.created_at),
    expires_at: normalizeNonNegativeNumber(rec.expires_at),
    active_sessions: normalizeNonNegativeInt(rec.active_sessions),
    output_count: normalizeNonNegativeInt(rec.output_count),
    uses_public_host: Boolean(rec.uses_public_host),
    can_copy_link: Boolean(rec.can_copy_link),
  };

  if (
    typeof rec.revoked_at === "number" &&
    Number.isFinite(rec.revoked_at) &&
    rec.revoked_at > 0
  ) {
    summary.revoked_at = rec.revoked_at;
  }
  if (
    typeof rec.consumed_at === "number" &&
    Number.isFinite(rec.consumed_at) &&
    rec.consumed_at > 0
  ) {
    summary.consumed_at = rec.consumed_at;
  }
  if (typeof rec.url === "string" && rec.url.trim()) {
    summary.url = rec.url.trim();
  }
  if (typeof rec.local_url === "string" && rec.local_url.trim()) {
    summary.local_url = rec.local_url.trim();
  }

  return summary;
}

export function normalizeColabOneWorkflowShareListState(
  raw: unknown,
): ColabOneWorkflowShareListState {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return createColabOneDefaultWorkflowShareListState();
  }
  const rec = raw as Record<string, unknown>;
  const hasSharesArray = Array.isArray(rec.shares);
  const shares: ColabOneWorkflowShareSummary[] = [];
  if (hasSharesArray) {
    const seen = new Set<string>();
    for (const item of rec.shares as unknown[]) {
      const normalized = normalizeColabOneWorkflowShareSummary(item);
      if (!normalized || seen.has(normalized.id)) continue;
      seen.add(normalized.id);
      shares.push(normalized);
    }
  }

  const computedActiveCount = shares.filter((s) => s.status === "active").length;
  const computedTotalSessions = shares.reduce(
    (sum, s) => sum + s.active_sessions,
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

function normalizeShareCapabilitiesPayload(
  raw: unknown,
): ColabOneWorkflowShareCapabilities {
  const rec =
    raw && typeof raw === "object" && !Array.isArray(raw)
      ? (raw as Record<string, unknown>)
      : {};
  return {
    runtime: "web-share",
    version: typeof rec.version === "string" ? rec.version : "",
    share_id: typeof rec.share_id === "string" ? rec.share_id.trim() : "",
    title: typeof rec.title === "string" ? rec.title.trim() : "",
    allowed_workflow_ids: normalizeStringList(rec.allowed_workflow_ids),
    can_download_output:
      typeof rec.can_download_output === "boolean"
        ? rec.can_download_output
        : true,
    can_delete_output:
      typeof rec.can_delete_output === "boolean" ? rec.can_delete_output : true,
    can_export_to_host_drive: Boolean(rec.can_export_to_host_drive),
  };
}

function notifyColabOneShareAuthRequired(): void {
  cachedShareSnapshotETag = "";
  cachedShareSnapshotRaw = "";
  shareSnapshotAppliedSeq = shareSnapshotReqSeq;
  shareSnapshotEpoch += 1;
  shareAuthEpoch += 1;
  colabOneWorkflowShareSessionExpired.value = true;
  if (
    typeof window !== "undefined" &&
    typeof window.dispatchEvent === "function"
  ) {
    const evt =
      typeof CustomEvent === "function"
        ? new CustomEvent(COLABONE_SHARE_AUTH_REQUIRED_EVENT)
        : new Event(COLABONE_SHARE_AUTH_REQUIRED_EVENT);
    window.dispatchEvent(evt);
  }
}

async function extractShareResponseError(
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
        // Not JSON; fall back to plain text when not an HTML error document.
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
    // Ignore body read failures and fall back to error header or status code.
  }
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }
  const encodedHeaderErr = (res.headers?.get("X-ColabOne-Error") || "").trim();
  if (encodedHeaderErr) {
    try {
      const decoded = decodeURIComponent(encodedHeaderErr).trim();
      if (decoded) {
        return decoded;
      }
    } catch {
      return encodedHeaderErr;
    }
  }
  return `HTTP ${res.status}`;
}

function sanitizeDownloadFilename(raw: string): string {
  const cleaned = raw
    .replace(/[\x00-\x1f\x7f]/g, "")
    .trim()
    .replace(/^["']|["']$/g, "")
    .trim()
    .split(/[\\/]/)
    .pop()
    ?.trim();
  return cleaned && cleaned !== "." && cleaned !== ".." ? cleaned : "";
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

export function resetColabOneWorkflowShareSnapshotCache(): void {
  cachedShareSnapshotETag = "";
  cachedShareSnapshotRaw = "";
  shareSnapshotAppliedSeq = shareSnapshotReqSeq;
  shareSnapshotEpoch += 1;
}

export async function fetchColabOneWorkflowShareCapabilities(): Promise<ColabOneWorkflowShareCapabilities> {
  const reqAuthEpoch = shareAuthEpoch;
  const res = await fetch("/api/share/capabilities", {
    method: "GET",
    credentials: "same-origin",
    headers: {
      [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
    },
  });

  if (res.status === 401) {
    notifyColabOneShareAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractShareResponseError(res));
  }

  const payload = (await res.json()) as unknown;
  const caps = normalizeShareCapabilitiesPayload(payload);
  if (reqAuthEpoch === shareAuthEpoch) {
    colabOneWorkflowShareCapabilities.value = caps;
    if (caps.share_id) {
      colabOneWorkflowShareId.value = caps.share_id;
    }
    colabOneWorkflowShareSessionExpired.value = false;
  }
  return caps;
}

export async function colabOneWorkflowShareSnapshotRaw(): Promise<string> {
  const reqSeq = ++shareSnapshotReqSeq;
  const epoch = shareSnapshotEpoch;
  const reqAuthEpoch = shareAuthEpoch;
  const sentETag = cachedShareSnapshotETag;
  const sentRaw = cachedShareSnapshotRaw;
  const headers: Record<string, string> = {
    [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
  };
  if (sentETag && sentRaw) {
    headers["If-None-Match"] = sentETag;
  }

  const res = await fetch("/api/share/snapshot", {
    method: "GET",
    credentials: "same-origin",
    headers,
  });

  if (res.status === 401) {
    notifyColabOneShareAuthRequired();
    throw new Error("Unauthorized");
  }
  if (res.status === 304 && sentRaw) {
    if (reqAuthEpoch === shareAuthEpoch) {
      colabOneWorkflowShareSessionExpired.value = false;
    }
    return sentRaw;
  }
  if (!res.ok) {
    throw new Error(await extractShareResponseError(res));
  }

  const raw = await res.text();
  const etag = (res.headers.get("ETag") || "").trim();
  if (epoch === shareSnapshotEpoch && reqSeq > shareSnapshotAppliedSeq) {
    shareSnapshotAppliedSeq = reqSeq;
    cachedShareSnapshotRaw = raw;
    cachedShareSnapshotETag = etag;
  }
  if (reqAuthEpoch === shareAuthEpoch) {
    colabOneWorkflowShareSessionExpired.value = false;
  }
  return raw;
}

export async function colabOneWorkflowShareCommandRaw(
  action: string,
  payload: Record<string, unknown> = {},
): Promise<string> {
  const reqAuthEpoch = shareAuthEpoch;
  const res = await fetch("/api/share/command", {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
    },
    body: JSON.stringify({
      action,
      payload: payload ?? {},
    }),
  });

  if (res.status === 401) {
    notifyColabOneShareAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractShareResponseError(res));
  }

  if (reqAuthEpoch === shareAuthEpoch) {
    colabOneWorkflowShareSessionExpired.value = false;
  }
  return res.text();
}

export async function colabOneWorkflowShareOutputPreviewURL(
  id: string,
  signal?: AbortSignal,
): Promise<string> {
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }
  const cleanId = String(id || "").trim().toLowerCase();
  if (!COLABONE_OUTPUT_ID_RE.test(cleanId)) {
    throw new Error("Invalid output artifact ID");
  }

  const reqAuthEpoch = shareAuthEpoch;
  const mediaUrl = `/api/share/outputs/${cleanId}/media`;
  const res = await fetch(mediaUrl, {
    method: "HEAD",
    credentials: "same-origin",
    headers: {
      [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
    },
    ...(signal ? { signal } : {}),
  });

  if (res.status === 401) {
    notifyColabOneShareAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractShareResponseError(res, signal));
  }
  if (reqAuthEpoch === shareAuthEpoch) {
    colabOneWorkflowShareSessionExpired.value = false;
  }
  return mediaUrl;
}

export async function colabOneWorkflowShareDownloadOutput(
  id: string,
): Promise<string> {
  const cleanId = String(id || "").trim().toLowerCase();
  if (!COLABONE_OUTPUT_ID_RE.test(cleanId)) {
    throw new Error("Invalid output artifact ID");
  }

  const reqAuthEpoch = shareAuthEpoch;
  const downloadUrl = `/api/share/outputs/${cleanId}/download`;
  const res = await fetch(downloadUrl, {
    method: "HEAD",
    credentials: "same-origin",
    headers: {
      [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
    },
  });

  if (res.status === 401) {
    notifyColabOneShareAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractShareResponseError(res));
  }

  if (reqAuthEpoch === shareAuthEpoch) {
    colabOneWorkflowShareSessionExpired.value = false;
  }
  const filename =
    parseContentDispositionFilename(res.headers.get("Content-Disposition")) ||
    `ColabOne-${cleanId.slice(0, 8)}`;

  if (typeof document !== "undefined") {
    const link = document.createElement("a");
    link.href = downloadUrl;
    link.download = filename;
    link.rel = "noopener";
    link.style.display = "none";
    document.body?.appendChild(link);
    link.click();
    link.remove();
  }
  return filename;
}

// ---------------------------------------------------------------------------
// Host Web Console API Helpers (browser mode without Desktop MyGo IPC)
// ---------------------------------------------------------------------------

async function colabOneWebWorkflowShareFetchJson(
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
    throw new Error(await extractShareResponseError(res));
  }
  const data = (await res.json()) as unknown;
  markColabOneWebSessionAlive(reqWebEpoch);
  return data;
}

export async function colabOneWebGetWorkflowSharesState(): Promise<ColabOneWorkflowShareListState> {
  const payload = await colabOneWebWorkflowShareFetchJson(
    "/api/workflow-shares",
    {
      method: "GET",
    },
  );
  return normalizeColabOneWorkflowShareListState(payload);
}

export async function colabOneWebCreateWorkflowShare(
  req: ColabOneWorkflowShareCreateRequest,
): Promise<ColabOneWorkflowShareSummary> {
  const payload = await colabOneWebWorkflowShareFetchJson(
    "/api/workflow-shares",
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify(req),
    },
  );
  const normalized = normalizeColabOneWorkflowShareSummary(payload);
  if (!normalized) {
    throw new Error("创建工作流分享返回数据无效");
  }
  return normalized;
}

export async function colabOneWebRevokeWorkflowShare(
  id: string,
): Promise<ColabOneWorkflowShareListState> {
  const cleanId = encodeURIComponent(String(id || "").trim());
  const payload = await colabOneWebWorkflowShareFetchJson(
    `/api/workflow-shares/${cleanId}/revoke`,
    {
      method: "POST",
    },
  );
  return normalizeColabOneWorkflowShareListState(payload);
}

export async function colabOneWebDeleteWorkflowShare(
  id: string,
): Promise<ColabOneWorkflowShareListState> {
  const cleanId = encodeURIComponent(String(id || "").trim());
  const payload = await colabOneWebWorkflowShareFetchJson(
    `/api/workflow-shares/${cleanId}`,
    {
      method: "DELETE",
    },
  );
  return normalizeColabOneWorkflowShareListState(payload);
}
