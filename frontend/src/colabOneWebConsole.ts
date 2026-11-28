import { ref } from "vue";

export const COLABONE_WEB_CONSOLE_DEFAULT_PORT = 3232;
export const COLABONE_WEB_CONSOLE_MIN_PORT = 1024;
export const COLABONE_WEB_CONSOLE_MAX_PORT = 65535;
export const COLABONE_WEB_CLIENT_HEADER = "X-ColabOne-Client";
export const COLABONE_WEB_CLIENT_VALUE = "web";
export const COLABONE_WEB_AUTH_REQUIRED_EVENT = "colabone:web-auth-required";

export type ColabOneWebConsoleBindMode = "loopback" | "lan";

export interface ColabOneWebConsoleState {
  enabled: boolean;
  running: boolean;
  bind_mode: string;
  listen_addr: string;
  port: number;
  local_url: string;
  lan_urls?: string[];
  public_origin: string;
  has_passcode: boolean;
  passcode_preview: string;
  active_sessions: number;
  last_error?: string;
}

export interface ColabOneWebConsoleUpdateRequest {
  enabled?: boolean;
  bind_mode?: string;
  port?: number;
  public_origin?: string;
}

export interface ColabOneWebConsoleTicket {
  url: string;
  local_url: string;
  expires_at: number;
  ttl_seconds: number;
  uses_public_host: boolean;
  single_use: boolean;
}

export interface ColabOneWebCapabilities {
  runtime: "desktop" | "web";
  version: string;
  public_origin?: string;
  bind_mode?: string;
  local_url?: string;
  lan_urls?: string[];
  can_open_native_canvas_window: boolean;
  can_standalone_log_window: boolean;
  can_manage_web_console: boolean;
  can_manage_workflow_shares: boolean;
  can_manage_close_behavior: boolean;
  can_manage_shortcuts: boolean;
  can_self_update: boolean;
  can_migrate: boolean;
  can_reveal_host_secrets: boolean;
  can_reveal_tunnel_tokens: boolean;
  can_test_saved_assign_proxy: boolean;
  can_loopback_oauth: boolean;
  can_download_output: boolean;
}

const COLABONE_ALLOWED_EXTERNAL_HOSTS: ReadonlySet<string> = new Set([
  "accounts.google.com",
  "colab.research.google.com",
  "console.cloud.google.com",
  "developers.google.com",
  "drive.google.com",
  "github.com",
  "huggingface.co",
  "one.dash.cloudflare.com",
  "dash.cloudflare.com",
  "developers.cloudflare.com",
]);

const COLABONE_OUTPUT_ID_RE = /^[a-f0-9]{32}$/;

let cachedWebSnapshotETag = "";
let cachedWebSnapshotRaw = "";
let webSnapshotReqSeq = 0;
let webSnapshotAppliedSeq = 0;
let webSnapshotEpoch = 0;
let webAuthEpoch = 0;

export function isColabOneDesktopRuntime(): boolean {
  return (
    typeof globalThis === "object" &&
    typeof (globalThis as { mygo?: { callWith?: unknown } }).mygo?.callWith ===
      "function"
  );
}

export function isColabOneWebRuntime(): boolean {
  return (
    !isColabOneDesktopRuntime() &&
    typeof window !== "undefined" &&
    (window.location?.protocol === "http:" ||
      window.location?.protocol === "https:")
  );
}

export function colabOneIsValidWebPort(port: unknown): boolean {
  return (
    typeof port === "number" &&
    Number.isInteger(port) &&
    port >= COLABONE_WEB_CONSOLE_MIN_PORT &&
    port <= COLABONE_WEB_CONSOLE_MAX_PORT
  );
}

export function createColabOneDefaultWebConsoleState(): ColabOneWebConsoleState {
  return {
    enabled: false,
    running: false,
    bind_mode: "loopback",
    listen_addr: `127.0.0.1:${COLABONE_WEB_CONSOLE_DEFAULT_PORT}`,
    port: COLABONE_WEB_CONSOLE_DEFAULT_PORT,
    local_url: `http://127.0.0.1:${COLABONE_WEB_CONSOLE_DEFAULT_PORT}`,
    lan_urls: [],
    public_origin: "",
    has_passcode: false,
    passcode_preview: "",
    active_sessions: 0,
    last_error: "",
  };
}

export function normalizeColabOneWebConsoleState(
  raw: unknown,
): ColabOneWebConsoleState {
  const defaults = createColabOneDefaultWebConsoleState();
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return defaults;
  }
  const rec = raw as Record<string, unknown>;
  const bindMode: ColabOneWebConsoleBindMode =
    rec.bind_mode === "lan" ? "lan" : "loopback";
  const port = colabOneIsValidWebPort(rec.port)
    ? (rec.port as number)
    : COLABONE_WEB_CONSOLE_DEFAULT_PORT;
  const fallbackHost = bindMode === "lan" ? "0.0.0.0" : "127.0.0.1";
  const listenAddr =
    typeof rec.listen_addr === "string" && rec.listen_addr.trim()
      ? rec.listen_addr.trim()
      : `${fallbackHost}:${port}`;
  const localUrl =
    typeof rec.local_url === "string" && rec.local_url.trim()
      ? rec.local_url.trim()
      : `http://127.0.0.1:${port}`;
  const lanUrls: string[] = [];
  if (Array.isArray(rec.lan_urls)) {
    const seen = new Set<string>();
    for (const item of rec.lan_urls) {
      if (typeof item !== "string") continue;
      const trimmed = item.trim();
      if (!trimmed || seen.has(trimmed)) continue;
      seen.add(trimmed);
      lanUrls.push(trimmed);
    }
  }
  const activeSessions =
    typeof rec.active_sessions === "number" &&
    Number.isFinite(rec.active_sessions) &&
    rec.active_sessions > 0
      ? Math.floor(rec.active_sessions)
      : 0;

  return {
    enabled: Boolean(rec.enabled),
    running: Boolean(rec.running),
    bind_mode: bindMode,
    listen_addr: listenAddr,
    port,
    local_url: localUrl,
    lan_urls: lanUrls,
    public_origin:
      typeof rec.public_origin === "string" ? rec.public_origin.trim() : "",
    has_passcode: Boolean(rec.has_passcode),
    passcode_preview:
      typeof rec.passcode_preview === "string" ? rec.passcode_preview : "",
    active_sessions: activeSessions,
    last_error: typeof rec.last_error === "string" ? rec.last_error : "",
  };
}

export function createColabOneCapabilities(
  runtime: "desktop" | "web",
): ColabOneWebCapabilities {
  if (runtime === "desktop") {
    return {
      runtime: "desktop",
      version: "",
      can_open_native_canvas_window: true,
      can_standalone_log_window: true,
      can_manage_web_console: true,
      can_manage_workflow_shares: true,
      can_manage_close_behavior: true,
      can_manage_shortcuts: true,
      can_self_update: true,
      can_migrate: true,
      can_reveal_host_secrets: true,
      can_reveal_tunnel_tokens: true,
      can_test_saved_assign_proxy: true,
      can_loopback_oauth: true,
      can_download_output: true,
    };
  }
  return {
    runtime: "web",
    version: "",
    can_open_native_canvas_window: false,
    can_standalone_log_window: false,
    can_manage_web_console: false,
    can_manage_workflow_shares: true,
    can_manage_close_behavior: false,
    can_manage_shortcuts: false,
    can_self_update: false,
    can_migrate: false,
    can_reveal_host_secrets: false,
    can_reveal_tunnel_tokens: true,
    can_test_saved_assign_proxy: true,
    can_loopback_oauth: false,
    can_download_output: true,
  };
}

export const colabOneRuntimeCapabilities = ref<ColabOneWebCapabilities>(
  createColabOneCapabilities(isColabOneDesktopRuntime() ? "desktop" : "web"),
);

export const colabOneWebSessionExpired = ref(false);

/**
 * Becomes true once the live Web Console backend responds to /api/web/capabilities,
 * letting modules like comfyDrafts distinguish an authenticated browser session
 * from an offline preview or unit test environment.
 */
export const colabOneWebBackendReady = ref(false);

function normalizeCapabilitiesPayload(raw: unknown): ColabOneWebCapabilities {
  const rec =
    raw && typeof raw === "object" && !Array.isArray(raw)
      ? (raw as Record<string, unknown>)
      : {};
  const runtime: "desktop" | "web" =
    rec.runtime === "desktop" ? "desktop" : "web";
  const base = createColabOneCapabilities(runtime);
  const boolOr = (key: keyof ColabOneWebCapabilities, fallback: boolean) =>
    typeof rec[key] === "boolean" ? (rec[key] as boolean) : fallback;
  const publicOrigin =
    typeof rec.public_origin === "string" ? rec.public_origin.trim() : "";
  const bindMode =
    typeof rec.bind_mode === "string" ? rec.bind_mode.trim() : "";
  const localUrl =
    typeof rec.local_url === "string" ? rec.local_url.trim() : "";
  const lanUrls = Array.isArray(rec.lan_urls)
    ? Array.from(
        new Set(
          rec.lan_urls
            .filter((u): u is string => typeof u === "string")
            .map((u) => u.trim())
            .filter(Boolean),
        ),
      )
    : [];
  return {
    runtime,
    version: typeof rec.version === "string" ? rec.version : base.version,
    ...(publicOrigin ? { public_origin: publicOrigin } : {}),
    ...(bindMode ? { bind_mode: bindMode } : {}),
    ...(localUrl ? { local_url: localUrl } : {}),
    ...(lanUrls.length ? { lan_urls: lanUrls } : {}),
    can_open_native_canvas_window: boolOr(
      "can_open_native_canvas_window",
      base.can_open_native_canvas_window,
    ),
    can_standalone_log_window: boolOr(
      "can_standalone_log_window",
      base.can_standalone_log_window,
    ),
    can_manage_web_console: boolOr(
      "can_manage_web_console",
      base.can_manage_web_console,
    ),
    can_manage_workflow_shares: boolOr(
      "can_manage_workflow_shares",
      base.can_manage_workflow_shares,
    ),
    can_manage_close_behavior: boolOr(
      "can_manage_close_behavior",
      base.can_manage_close_behavior,
    ),
    can_manage_shortcuts: boolOr(
      "can_manage_shortcuts",
      base.can_manage_shortcuts,
    ),
    can_self_update: boolOr("can_self_update", base.can_self_update),
    can_migrate: boolOr("can_migrate", base.can_migrate),
    can_reveal_host_secrets: boolOr(
      "can_reveal_host_secrets",
      base.can_reveal_host_secrets,
    ),
    can_reveal_tunnel_tokens: boolOr(
      "can_reveal_tunnel_tokens",
      base.can_reveal_tunnel_tokens,
    ),
    can_test_saved_assign_proxy: boolOr(
      "can_test_saved_assign_proxy",
      base.can_test_saved_assign_proxy,
    ),
    can_loopback_oauth: boolOr("can_loopback_oauth", base.can_loopback_oauth),
    can_download_output: boolOr(
      "can_download_output",
      base.can_download_output,
    ),
  };
}

export function getColabOneWebAuthEpoch(): number {
  return webAuthEpoch;
}

export function markColabOneWebSessionAlive(reqEpoch: number): void {
  // Never clear an expired session from non-snapshot endpoints while the
  // snapshot poller is stopped; only snapshot/capabilities recovery restores
  // both colabOneWebSessionExpired and colabOneWebBackendReady consistently.
  if (reqEpoch === webAuthEpoch && !colabOneWebSessionExpired.value) {
    colabOneWebSessionExpired.value = false;
  }
}

export function notifyColabOneWebAuthRequired(): void {
  cachedWebSnapshotETag = "";
  cachedWebSnapshotRaw = "";
  webSnapshotAppliedSeq = webSnapshotReqSeq;
  webSnapshotEpoch += 1;
  webAuthEpoch += 1;
  colabOneWebSessionExpired.value = true;
  colabOneWebBackendReady.value = false;
  if (typeof window !== "undefined" && typeof window.dispatchEvent === "function") {
    const evt =
      typeof CustomEvent === "function"
        ? new CustomEvent(COLABONE_WEB_AUTH_REQUIRED_EVENT)
        : new Event(COLABONE_WEB_AUTH_REQUIRED_EVENT);
    window.dispatchEvent(evt);
  }
}

export function decodeColabOneErrorHeader(encodedHeaderErr: string): string {
  const trimmed = String(encodedHeaderErr || "").trim();
  if (!trimmed) return "";
  try {
    return decodeURIComponent(trimmed).trim() || trimmed;
  } catch {
    return trimmed;
  }
}

async function extractWebResponseError(
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
    const decoded = decodeColabOneErrorHeader(encodedHeaderErr);
    if (decoded) {
      return decoded;
    }
  }
  return `HTTP ${res.status}`;
}

export function resetColabOneWebSnapshotCache(): void {
  cachedWebSnapshotETag = "";
  cachedWebSnapshotRaw = "";
  webSnapshotAppliedSeq = webSnapshotReqSeq;
  webSnapshotEpoch += 1;
}

export async function fetchColabOneWebCapabilities(): Promise<ColabOneWebCapabilities> {
  if (isColabOneDesktopRuntime()) {
    const desktopCaps = createColabOneCapabilities("desktop");
    colabOneRuntimeCapabilities.value = desktopCaps;
    colabOneWebSessionExpired.value = false;
    colabOneWebBackendReady.value = false;
    return desktopCaps;
  }

  const reqAuthEpoch = webAuthEpoch;
  const res = await fetch("/api/web/capabilities", {
    method: "GET",
    credentials: "same-origin",
    headers: {
      [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE,
    },
  });

  if (res.status === 401) {
    notifyColabOneWebAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractWebResponseError(res));
  }

  const payload = (await res.json()) as unknown;
  const caps = normalizeCapabilitiesPayload(payload);
  if (reqAuthEpoch === webAuthEpoch) {
    colabOneRuntimeCapabilities.value = caps;
    colabOneWebSessionExpired.value = false;
    colabOneWebBackendReady.value = caps.runtime === "web";
  }
  return caps;
}

export async function colabOneWebSnapshotRaw(): Promise<string> {
  const reqSeq = ++webSnapshotReqSeq;
  const epoch = webSnapshotEpoch;
  const reqAuthEpoch = webAuthEpoch;
  const sentETag = cachedWebSnapshotETag;
  const sentRaw = cachedWebSnapshotRaw;
  const headers: Record<string, string> = {
    [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE,
  };
  if (sentETag && sentRaw) {
    headers["If-None-Match"] = sentETag;
  }

  const res = await fetch("/api/snapshot", {
    method: "GET",
    credentials: "same-origin",
    headers,
  });

  if (res.status === 401) {
    notifyColabOneWebAuthRequired();
    throw new Error("Unauthorized");
  }
  if (res.status === 304 && sentRaw) {
    if (reqAuthEpoch === webAuthEpoch) {
      colabOneWebSessionExpired.value = false;
      colabOneWebBackendReady.value = true;
    }
    return sentRaw;
  }
  if (!res.ok) {
    throw new Error(await extractWebResponseError(res));
  }

  const raw = await res.text();
  const etag = (res.headers.get("ETag") || "").trim();
  if (epoch === webSnapshotEpoch && reqSeq > webSnapshotAppliedSeq) {
    webSnapshotAppliedSeq = reqSeq;
    cachedWebSnapshotRaw = raw;
    cachedWebSnapshotETag = etag;
  }
  if (reqAuthEpoch === webAuthEpoch) {
    colabOneWebSessionExpired.value = false;
    colabOneWebBackendReady.value = true;
  }
  return raw;
}

export async function colabOneWebCommandRaw(
  action: string,
  payload: Record<string, unknown> = {},
): Promise<string> {
  const reqAuthEpoch = webAuthEpoch;
  const res = await fetch("/api/command", {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE,
    },
    body: JSON.stringify({
      action,
      payload: payload ?? {},
    }),
  });

  if (res.status === 401) {
    notifyColabOneWebAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractWebResponseError(res));
  }

  const text = await res.text();
  markColabOneWebSessionAlive(reqAuthEpoch);
  return text;
}

export async function colabOneWebLogout(): Promise<void> {
  try {
    await fetch("/_colabone_web/logout", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE,
      },
    });
  } finally {
    resetColabOneWebSnapshotCache();
    colabOneWebBackendReady.value = false;
    if (typeof window !== "undefined" && window.location) {
      if (typeof window.location.assign === "function") {
        window.location.assign("/_colabone_web/login");
      } else {
        window.location.href = "/_colabone_web/login";
      }
    }
  }
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

export async function colabOneWebDownloadOutput(id: string): Promise<string> {
  const cleanId = String(id || "").trim().toLowerCase();
  if (!COLABONE_OUTPUT_ID_RE.test(cleanId)) {
    throw new Error("Invalid output artifact ID");
  }

  const reqAuthEpoch = webAuthEpoch;
  const downloadUrl = `/api/outputs/${cleanId}/download`;
  // Preflight via HEAD to verify auth, artifact metadata, and filename without
  // buffering potentially large (up to 512 MiB) videos into browser JS memory.
  const res = await fetch(downloadUrl, {
    method: "HEAD",
    credentials: "same-origin",
    headers: {
      [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE,
    },
  });

  if (res.status === 401) {
    notifyColabOneWebAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractWebResponseError(res));
  }

  markColabOneWebSessionAlive(reqAuthEpoch);

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

export async function colabOneWebOutputPreviewURL(
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

  const reqAuthEpoch = webAuthEpoch;
  const mediaUrl = `/api/outputs/${cleanId}/media`;
  const res = await fetch(mediaUrl, {
    method: "HEAD",
    credentials: "same-origin",
    headers: {
      [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE,
    },
    ...(signal ? { signal } : {}),
  });

  if (res.status === 401) {
    notifyColabOneWebAuthRequired();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    throw new Error(await extractWebResponseError(res, signal));
  }
  markColabOneWebSessionAlive(reqAuthEpoch);
  return mediaUrl;
}

function extractRawAuthority(raw: string): string {
  const withoutScheme = raw.replace(/^https:\/\//i, "");
  const cutIdx = withoutScheme.search(/[/?#]/);
  return cutIdx === -1 ? withoutScheme : withoutScheme.slice(0, cutIdx);
}

export function validateColabOneWebExternalURL(raw: string): string {
  if (typeof raw !== "string" || !raw || raw.trim() !== raw) {
    throw new Error("Unsupported external URL");
  }
  if (!/^https:\/\//i.test(raw)) {
    throw new Error("Only HTTPS URLs are allowed");
  }
  const authority = extractRawAuthority(raw);
  if (!authority || authority.includes("@") || authority.includes(":")) {
    throw new Error("Unsupported external URL");
  }
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    throw new Error("Unsupported external URL");
  }
  if (
    parsed.protocol !== "https:" ||
    parsed.username !== "" ||
    parsed.password !== "" ||
    parsed.port !== ""
  ) {
    throw new Error("Unsupported external URL");
  }
  const host = parsed.hostname.toLowerCase().replace(/\.$/, "");
  if (!COLABONE_ALLOWED_EXTERNAL_HOSTS.has(host)) {
    throw new Error("Unsupported external URL");
  }
  return parsed.toString();
}

export function colabOneWebOpenExternalURL(raw: string): void {
  const safeUrl = validateColabOneWebExternalURL(raw);
  if (typeof window !== "undefined" && typeof window.open === "function") {
    window.open(safeUrl, "_blank", "noopener,noreferrer");
  }
}

export type ColabOneWebOutputShareOriginConfig = Partial<
  Pick<ColabOneWebConsoleState, "public_origin" | "local_url" | "lan_urls">
>;

function isExactLoopbackHostname(hostname: string): boolean {
  const bare = hostname
    .trim()
    .toLowerCase()
    .replace(/^\[|\]$/g, "")
    .replace(/\.+$/, "");
  return bare === "127.0.0.1" || bare === "localhost" || bare === "::1";
}

export function validateColabOneWebOutputShareURL(
  raw: string,
  webConsoleState?: ColabOneWebOutputShareOriginConfig,
): string {
  const trimmed = typeof raw === "string" ? raw.trim() : "";
  if (!trimmed) {
    throw new Error("Unsupported output share URL");
  }
  const baseOrigin =
    typeof window !== "undefined" && window.location?.origin
      ? window.location.origin
      : "";
  let parsed: URL;
  try {
    parsed = baseOrigin ? new URL(trimmed, baseOrigin) : new URL(trimmed);
  } catch {
    throw new Error("Unsupported output share URL");
  }
  if (
    (parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
    parsed.username !== "" ||
    parsed.password !== ""
  ) {
    throw new Error("Unsupported output share URL");
  }
  if (
    parsed.pathname !== "/gallery" &&
    !parsed.pathname.startsWith("/gallery/")
  ) {
    throw new Error("Unsupported output share URL");
  }

  const allowedOrigins = new Set<string>();
  const addAllowedOrigin = (
    candidate: string | undefined,
    expandLoopbackAliases = false,
  ) => {
    const clean = typeof candidate === "string" ? candidate.trim() : "";
    if (!clean) return;
    try {
      const u = new URL(clean);
      if (u.protocol === "http:" || u.protocol === "https:") {
        allowedOrigins.add(u.origin);
        if (expandLoopbackAliases && isExactLoopbackHostname(u.hostname)) {
          const portSuffix = u.port ? `:${u.port}` : "";
          allowedOrigins.add(`${u.protocol}//127.0.0.1${portSuffix}`);
          allowedOrigins.add(`${u.protocol}//localhost${portSuffix}`);
          allowedOrigins.add(`${u.protocol}//[::1]${portSuffix}`);
        }
      }
    } catch {
      // Ignore malformed origin candidates.
    }
  };
  addAllowedOrigin(baseOrigin, true);
  addAllowedOrigin(webConsoleState?.public_origin, false);
  addAllowedOrigin(webConsoleState?.local_url, false);
  if (Array.isArray(webConsoleState?.lan_urls)) {
    for (const lanUrl of webConsoleState.lan_urls) {
      addAllowedOrigin(lanUrl, false);
    }
  }

  if (!allowedOrigins.has(parsed.origin)) {
    throw new Error("Unsupported output share URL");
  }
  return parsed.toString();
}

export function resolveColabOneWebOutputShareOpenURL(
  raw: string,
  webConsoleState?: ColabOneWebOutputShareOriginConfig,
): string {
  const safeUrl = validateColabOneWebOutputShareURL(raw, webConsoleState);
  const parsed = new URL(safeUrl);
  const baseOrigin =
    typeof window !== "undefined" && window.location?.origin
      ? window.location.origin
      : "";
  let baseUrl: URL | null = null;
  if (baseOrigin) {
    try {
      const candidate = new URL(baseOrigin);
      if (candidate.protocol === "http:" || candidate.protocol === "https:") {
        baseUrl = candidate;
      }
    } catch {
      baseUrl = null;
    }
  }
  let configuredPublicOrigin = "";
  const rawPublicOrigin =
    typeof webConsoleState?.public_origin === "string"
      ? webConsoleState.public_origin.trim()
      : "";
  if (rawPublicOrigin) {
    try {
      configuredPublicOrigin = new URL(rawPublicOrigin).origin;
    } catch {
      configuredPublicOrigin = "";
    }
  }
  const usesCustomPublicOrigin = Boolean(
    configuredPublicOrigin && parsed.origin === configuredPublicOrigin,
  );
  if (baseUrl && !usesCustomPublicOrigin && parsed.origin !== baseUrl.origin) {
    return `${baseUrl.origin}${parsed.pathname}${parsed.search}`;
  }
  return safeUrl;
}

export function colabOneWebOpenOutputShareURL(
  raw: string,
  webConsoleState?: ColabOneWebOutputShareOriginConfig,
): void {
  const targetUrl = resolveColabOneWebOutputShareOpenURL(raw, webConsoleState);
  if (typeof window !== "undefined" && typeof window.open === "function") {
    window.open(targetUrl, "_blank", "noopener,noreferrer");
  }
}

function isLoopbackOrUnspecifiedHost(host: string): boolean {
  if (!host) return true;
  if (host === "localhost" || host.endsWith(".localhost")) {
    return true;
  }
  // IPv4 loopback (127.0.0.0/8) or unspecified (0.0.0.0/8)
  if (/^127(?:\.\d{1,3}){3}$/.test(host) || /^0(?:\.\d{1,3}){3}$/.test(host)) {
    return true;
  }
  // IPv6 loopback (::1 / 0:0:0:0:0:0:0:1), unspecified (::), or IPv4-mapped loopback/unspecified
  if (host.includes(":")) {
    if (
      host === "::1" ||
      host === "::" ||
      /^(?:0+:){7}0*1$/.test(host) ||
      /^(?:0+:){7}0+$/.test(host)
    ) {
      return true;
    }
    if (
      /^::ffff:127(?:\.\d{1,3}){3}$/i.test(host) ||
      /^::ffff:0(?:\.\d{1,3}){3}$/i.test(host) ||
      /^::ffff:7f[0-9a-f]{2}:[0-9a-f]{1,4}$/i.test(host) ||
      /^::ffff:0{0,2}[0-9a-f]{1,2}:[0-9a-f]{1,4}$/i.test(host)
    ) {
      return true;
    }
  }
  return false;
}

export function validateColabOneWebCanvasURL(raw: string): string {
  if (typeof raw !== "string" || !raw || raw.trim() !== raw) {
    throw new Error("Invalid canvas URL");
  }
  if (!/^https:\/\//i.test(raw)) {
    throw new Error("Canvas URL must use HTTPS");
  }
  const authority = extractRawAuthority(raw);
  if (!authority || authority.includes("@") || authority.includes("%")) {
    throw new Error("Invalid canvas host");
  }
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    throw new Error("Invalid canvas URL");
  }
  if (
    parsed.protocol !== "https:" ||
    parsed.username !== "" ||
    parsed.password !== ""
  ) {
    throw new Error("Invalid canvas URL");
  }
  if (parsed.host.includes("%")) {
    throw new Error("Invalid canvas host");
  }
  const rawHostname = parsed.hostname.toLowerCase().replace(/\.$/, "");
  const host =
    rawHostname.startsWith("[") && rawHostname.endsWith("]")
      ? rawHostname.slice(1, -1)
      : rawHostname;
  if (!host) {
    throw new Error("Invalid canvas host");
  }
  if (isLoopbackOrUnspecifiedHost(host)) {
    throw new Error("Canvas URL cannot target a loopback address");
  }
  if (host === "trycloudflare.com" || host.endsWith(".trycloudflare.com")) {
    throw new Error("Temporary tunnel URL is not allowed for canvas");
  }
  if (parsed.pathname !== "/_colabone_auth") {
    throw new Error("Invalid canvas auth path");
  }
  const token = (parsed.searchParams.get("token") || "").trim();
  if (!token) {
    throw new Error("Missing canvas auth token");
  }
  return parsed.toString();
}

export function colabOneWebOpenCanvasURL(raw: string): void {
  const safeUrl = validateColabOneWebCanvasURL(raw);
  if (typeof window !== "undefined" && typeof window.open === "function") {
    window.open(safeUrl, "_blank", "noopener,noreferrer");
  }
}

/**
 * Opens a browser tab synchronously during a user click gesture before an async
 * fetch resolves (preventing iOS/macOS Safari and Firefox popup blockers),
 * immediately severs `opener` against reverse tabnabbing, validates the resolved
 * URL strictly, and closes the blank tab cleanly on failure.
 */
export async function colabOneWebOpenDeferredTab(
  loadUrl: () => Promise<string>,
  validateUrl: (raw: string) => string,
): Promise<string> {
  let popup: Window | null = null;
  if (typeof window !== "undefined" && typeof window.open === "function") {
    try {
      popup = window.open("about:blank", "_blank");
      if (popup) {
        popup.opener = null;
      }
    } catch {
      popup = null;
    }
  }
  try {
    const raw = await loadUrl();
    const safeUrl = validateUrl(raw);
    if (popup && !popup.closed) {
      if (typeof popup.location?.replace === "function") {
        popup.location.replace(safeUrl);
      } else {
        popup.location.href = safeUrl;
      }
    } else if (typeof window !== "undefined" && typeof window.open === "function") {
      window.open(safeUrl, "_blank", "noopener,noreferrer");
    }
    return safeUrl;
  } catch (err) {
    if (popup && !popup.closed) {
      try {
        popup.close();
      } catch {
        // Ignore popup close failure.
      }
    }
    throw err;
  }
}

export function colabOneBuildCloudflaredQuickCommand(port: number): string {
  const safePort = colabOneIsValidWebPort(port)
    ? Math.trunc(port)
    : COLABONE_WEB_CONSOLE_DEFAULT_PORT;
  return `cloudflared tunnel --url http://127.0.0.1:${safePort}`;
}

export function colabOneBuildCloudflaredServiceTarget(port: number): string {
  const safePort = colabOneIsValidWebPort(port)
    ? Math.trunc(port)
    : COLABONE_WEB_CONSOLE_DEFAULT_PORT;
  return `http://127.0.0.1:${safePort}`;
}

export function colabOneNormalizePublicOriginInput(raw: string): string {
  const trimmed = String(raw || "").trim().replace(/\/+$/, "");
  if (!trimmed) {
    return "";
  }
  const withScheme = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(trimmed)
    ? trimmed
    : `https://${trimmed}`;
  return withScheme.replace(/\/+$/, "");
}
