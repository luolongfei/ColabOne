import {
  ColabOneDesktop,
  events,
  type ColabOneCloseBehaviorState,
  type ColabOneCloseRequest,
  type ColabOneLogDrawerState,
  type ColabOneMigrationAccountSummary,
  type ColabOneMigrationApplyRequest,
  type ColabOneMigrationApplyResult,
  type ColabOneMigrationCUUsageSummary,
  type ColabOneMigrationCredentialsSummary,
  type ColabOneMigrationDraftsSummary,
  type ColabOneMigrationExportRequest,
  type ColabOneMigrationExportResult,
  type ColabOneMigrationExportSummary,
  type ColabOneMigrationLibrarySummary,
  type ColabOneMigrationModuleSummary,
  type ColabOneMigrationPickResult,
  type ColabOneMigrationPreview,
  type ColabOneMigrationSettingsSummary,
  type ColabOneMigrationUIPrefs,
  type ColabOneRollbackVersion,
  type ColabOneUpdateState,
  type ColabOneWebConsoleState,
  type ColabOneWebConsoleTicket,
  type ColabOneWebConsoleUpdateRequest,
} from "./colabOneBridge";
import {
  COLABONE_WEB_AUTH_REQUIRED_EVENT,
  colabOneRuntimeCapabilities,
  colabOneWebCommandRaw,
  colabOneWebDownloadOutput,
  colabOneWebLogout,
  colabOneWebOpenDeferredTab,
  colabOneWebOpenExternalURL,
  colabOneWebOpenOutputShareURL,
  colabOneWebOutputPreviewURL,
  colabOneWebSessionExpired,
  colabOneWebSnapshotRaw,
  createColabOneDefaultWebConsoleState,
  fetchColabOneWebCapabilities,
  isColabOneDesktopRuntime,
  isColabOneWebRuntime,
  normalizeColabOneWebConsoleState,
  validateColabOneWebCanvasURL,
  validateColabOneWebExternalURL,
  validateColabOneWebOutputShareURL,
  type ColabOneWebCapabilities,
} from "./colabOneWebConsole";
import {
  COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT,
  COLABONE_OUTPUT_SHARE_CLIENT_HEADER,
  COLABONE_OUTPUT_SHARE_CLIENT_HEADER_VALUE,
  COLABONE_OUTPUT_SHARE_ROUTE_PATH,
  colabOneOutputShareMode,
  colabOneOutputShareSessionExpired,
  colabOneWebCreateOutputShare,
  colabOneWebDeleteOutputShare,
  colabOneWebDownloadOutputsZipURL,
  colabOneWebGetOutputSharesState,
  colabOneWebRevokeOutputShare,
  createColabOneDefaultOutputShareListState,
  isColabOneOutputShareActive,
  isColabOneOutputSharePath,
  normalizeColabOneOutputShareListState,
  normalizeColabOneOutputShareSummary,
  triggerColabOneBrowserDownload,
  type ColabOneOutputShareCreateRequest,
  type ColabOneOutputShareGroupInput,
  type ColabOneOutputShareListState,
  type ColabOneOutputShareManifest,
  type ColabOneOutputShareManifestGroup,
  type ColabOneOutputShareManifestItem,
  type ColabOneOutputShareStatus,
  type ColabOneOutputShareSummary,
  type ColabOneOutputShareTTLMode,
} from "./colabOneOutputShare";
import {
  COLABONE_SHARE_AUTH_REQUIRED_EVENT,
  COLABONE_SHARE_CLIENT_HEADER,
  COLABONE_SHARE_CLIENT_VALUE,
  colabOneWorkflowShareCapabilities,
  colabOneWorkflowShareCommandRaw,
  colabOneWorkflowShareDownloadOutput,
  colabOneWorkflowShareId,
  colabOneWorkflowShareMode,
  colabOneWorkflowShareOutputPreviewURL,
  colabOneWorkflowShareSessionExpired,
  colabOneWorkflowShareSnapshotRaw,
  createColabOneDefaultWorkflowShareListState,
  fetchColabOneWorkflowShareCapabilities,
  isColabOneWorkflowShareActive,
  isColabOneWorkflowSharePath,
  normalizeColabOneWorkflowShareListState,
  normalizeColabOneWorkflowShareSummary,
  resetColabOneWorkflowShareSnapshotCache,
  type ColabOneWorkflowShareCapabilities,
  type ColabOneWorkflowShareCreateRequest,
  type ColabOneWorkflowShareListState,
  type ColabOneWorkflowShareStatus,
  type ColabOneWorkflowShareSummary,
  type ColabOneWorkflowShareTTLMode,
} from "./colabOneWorkflowShare";
import type { ColabOneSnapshot } from "./types";

export {
  COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT,
  COLABONE_OUTPUT_SHARE_CLIENT_HEADER,
  COLABONE_OUTPUT_SHARE_CLIENT_HEADER_VALUE,
  COLABONE_OUTPUT_SHARE_ROUTE_PATH,
  COLABONE_SHARE_AUTH_REQUIRED_EVENT,
  COLABONE_SHARE_CLIENT_HEADER,
  COLABONE_SHARE_CLIENT_VALUE,
  COLABONE_WEB_AUTH_REQUIRED_EVENT,
  colabOneOutputShareMode,
  colabOneOutputShareSessionExpired,
  colabOneRuntimeCapabilities,
  colabOneWebLogout,
  colabOneWebOpenDeferredTab,
  colabOneWebSessionExpired,
  colabOneWorkflowShareCapabilities,
  colabOneWorkflowShareId,
  colabOneWorkflowShareMode,
  colabOneWorkflowShareSessionExpired,
  createColabOneDefaultOutputShareListState,
  createColabOneDefaultWebConsoleState,
  createColabOneDefaultWorkflowShareListState,
  fetchColabOneWebCapabilities,
  fetchColabOneWorkflowShareCapabilities,
  isColabOneDesktopRuntime,
  isColabOneOutputShareActive,
  isColabOneOutputSharePath,
  isColabOneWebRuntime,
  isColabOneWorkflowShareActive,
  isColabOneWorkflowSharePath,
  normalizeColabOneOutputShareListState,
  normalizeColabOneOutputShareSummary,
  normalizeColabOneWebConsoleState,
  normalizeColabOneWorkflowShareListState,
  normalizeColabOneWorkflowShareSummary,
  resetColabOneWorkflowShareSnapshotCache,
  validateColabOneWebExternalURL,
  validateColabOneWebOutputShareURL,
};

export type {
  ColabOneCloseBehaviorState,
  ColabOneCloseRequest,
  ColabOneMigrationAccountSummary,
  ColabOneMigrationApplyRequest,
  ColabOneMigrationApplyResult,
  ColabOneMigrationCUUsageSummary,
  ColabOneMigrationCredentialsSummary,
  ColabOneMigrationDraftsSummary,
  ColabOneMigrationExportRequest,
  ColabOneMigrationExportResult,
  ColabOneMigrationExportSummary,
  ColabOneMigrationLibrarySummary,
  ColabOneMigrationModuleSummary,
  ColabOneMigrationPickResult,
  ColabOneMigrationPreview,
  ColabOneMigrationSettingsSummary,
  ColabOneMigrationUIPrefs,
  ColabOneOutputShareCreateRequest,
  ColabOneOutputShareGroupInput,
  ColabOneOutputShareListState,
  ColabOneOutputShareManifest,
  ColabOneOutputShareManifestGroup,
  ColabOneOutputShareManifestItem,
  ColabOneOutputShareStatus,
  ColabOneOutputShareSummary,
  ColabOneOutputShareTTLMode,
  ColabOneRollbackVersion,
  ColabOneUpdateState,
  ColabOneWebCapabilities,
  ColabOneWebConsoleState,
  ColabOneWebConsoleTicket,
  ColabOneWebConsoleUpdateRequest,
  ColabOneWorkflowShareCapabilities,
  ColabOneWorkflowShareCreateRequest,
  ColabOneWorkflowShareListState,
  ColabOneWorkflowShareStatus,
  ColabOneWorkflowShareSummary,
  ColabOneWorkflowShareTTLMode,
};

export type ColabOneCloseBehavior = "ask" | "quit" | "background";
export type ColabOneCloseAction = "quit" | "background";

export async function colabOneSnapshotRaw(): Promise<string> {
  if (!isColabOneDesktopRuntime()) {
    if (isColabOneWorkflowShareActive()) {
      return colabOneWorkflowShareSnapshotRaw();
    }
    return colabOneWebSnapshotRaw();
  }
  return ColabOneDesktop.snapshot();
}

export async function colabOneSnapshot(): Promise<ColabOneSnapshot> {
  return JSON.parse(await colabOneSnapshotRaw());
}

export async function colabOneCommand<T = any>(
  action: string,
  payload: Record<string, unknown> = {},
): Promise<T> {
  if (!isColabOneDesktopRuntime()) {
    if (isColabOneWorkflowShareActive()) {
      return JSON.parse(
        await colabOneWorkflowShareCommandRaw(action, payload),
      ) as T;
    }
    return JSON.parse(await colabOneWebCommandRaw(action, payload)) as T;
  }
  return JSON.parse(await ColabOneDesktop.command({ action, payload })) as T;
}

export async function colabOneOpenURL(url: string): Promise<void> {
  if (!isColabOneDesktopRuntime()) {
    colabOneWebOpenExternalURL(url);
    return;
  }
  return ColabOneDesktop.openURL(url);
}

export async function colabOneOpenOutputShare(
  shareId: string,
  url: string,
  webConsoleState?: Partial<
    Pick<ColabOneWebConsoleState, "public_origin" | "local_url" | "lan_urls">
  >,
): Promise<void> {
  const cleanId = String(shareId || "").trim();
  if (isColabOneDesktopRuntime()) {
    if (!cleanId) {
      throw new Error("Missing output share ID");
    }
    return ColabOneDesktop.openOutputShare(cleanId);
  }
  colabOneWebOpenOutputShareURL(url, webConsoleState);
}

export async function colabOneReadClipboard(): Promise<string> {
  if (!isColabOneDesktopRuntime()) {
    if (typeof navigator !== "undefined" && navigator.clipboard?.readText) {
      return navigator.clipboard.readText();
    }
    throw new Error("Clipboard read unavailable in this browser");
  }
  return ColabOneDesktop.readClipboard();
}

export async function colabOneWriteClipboard(value: string): Promise<void> {
  if (!isColabOneDesktopRuntime()) {
    if (
      typeof navigator !== "undefined" &&
      navigator.clipboard &&
      typeof navigator.clipboard.writeText === "function"
    ) {
      try {
        await navigator.clipboard.writeText(value);
        return;
      } catch {
        // Fall through to execCommand fallback (e.g. non-HTTPS LAN web context).
      }
    }
    if (
      typeof document !== "undefined" &&
      document.body &&
      typeof document.execCommand === "function"
    ) {
      const previousActive =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null;
      const ta = document.createElement("textarea");
      ta.value = value;
      ta.setAttribute("readonly", "");
      ta.setAttribute("aria-hidden", "true");
      ta.style.position = "fixed";
      ta.style.top = "0";
      ta.style.left = "0";
      ta.style.width = "1px";
      ta.style.height = "1px";
      ta.style.padding = "0";
      ta.style.border = "0";
      ta.style.opacity = "0";
      ta.style.fontSize = "16px";
      ta.style.pointerEvents = "none";
      document.body.appendChild(ta);
      try {
        ta.focus({ preventScroll: true });
        ta.select();
        ta.setSelectionRange(0, ta.value.length);
        if (document.execCommand("copy")) {
          return;
        }
      } catch {
        // Fall through to normalized clipboard unavailable error below.
      } finally {
        if (ta.parentNode) {
          ta.parentNode.removeChild(ta);
        }
        if (previousActive && previousActive.isConnected) {
          previousActive.focus({ preventScroll: true });
        }
      }
    }
    throw new Error("Clipboard write unavailable in this browser");
  }
  return ColabOneDesktop.writeClipboard(value);
}

export async function colabOneExportWorkflowOutput(id: string): Promise<string> {
  if (!isColabOneDesktopRuntime()) {
    if (isColabOneWorkflowShareActive()) {
      return colabOneWorkflowShareDownloadOutput(id);
    }
    return colabOneWebDownloadOutput(id);
  }
  return ColabOneDesktop.exportWorkflowOutput(id);
}

export async function colabOneOutputPreviewURL(
  id: string,
  signal?: AbortSignal,
): Promise<string> {
  if (signal?.aborted) {
    throw new DOMException("Aborted", "AbortError");
  }
  if (!isColabOneDesktopRuntime()) {
    if (isColabOneWorkflowShareActive()) {
      return colabOneWorkflowShareOutputPreviewURL(id, signal);
    }
    return colabOneWebOutputPreviewURL(id, signal);
  }
  return ColabOneDesktop.outputPreviewURL(id);
}

export async function colabOneOpenComfyCanvas(
  target: "window" | "browser",
): Promise<void> {
  if (!isColabOneDesktopRuntime()) {
    await colabOneWebOpenDeferredTab(async () => {
      const res = await colabOneCommand<{ url?: string }>("comfyui.canvas.url");
      return typeof res?.url === "string" ? res.url : "";
    }, validateColabOneWebCanvasURL);
    return;
  }
  return ColabOneDesktop.openComfyCanvas(target);
}

export async function colabOneSetLocale(value: string): Promise<void> {
  if (!isColabOneDesktopRuntime()) {
    return;
  }
  return ColabOneDesktop.setLocale(value);
}

const DESKTOP_ONLY_WEB_ERROR = "该桌面专属操作仅支持在宿主机客户端执行";

export async function colabOneGetMigrationExportSummary(
  uiPrefs: ColabOneMigrationUIPrefs | null,
): Promise<ColabOneMigrationExportSummary> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.getMigrationExportSummary(uiPrefs);
}

export async function colabOneExportMigrationFile(
  req: ColabOneMigrationExportRequest,
): Promise<ColabOneMigrationExportResult> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.exportMigrationFile(req);
}

export async function colabOnePickMigrationFile(): Promise<ColabOneMigrationPickResult> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.pickMigrationFile();
}

export async function colabOneDecryptMigrationPreview(
  fileToken: string,
  password: string,
): Promise<ColabOneMigrationPreview> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.decryptMigrationPreview(fileToken, password);
}

export async function colabOneApplyMigrationPreview(
  req: ColabOneMigrationApplyRequest,
): Promise<ColabOneMigrationApplyResult> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.applyMigrationPreview(req);
}

export async function colabOneCancelMigrationSession(
  sessionID = "",
): Promise<void> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.cancelMigrationSession(sessionID);
}

export async function colabOneGetLogDrawerState(): Promise<ColabOneLogDrawerState> {
  if (!isColabOneDesktopRuntime()) {
    return { open: false, standalone: false, expanded: false, delta: 0 };
  }
  try {
    const res = await ColabOneDesktop.getLogDrawerState();
    if (res && typeof res.open === "boolean") {
      return {
        open: res.open,
        standalone: Boolean(res.standalone),
        expanded: Boolean(res.expanded),
        delta: Number(res.delta) || 0,
      };
    }
  } catch {
    // Non-desktop or offline mock runtime.
  }
  return { open: false, standalone: false, expanded: false, delta: 0 };
}

export async function colabOneSetLogDrawer(
  open: boolean,
  width: number,
): Promise<ColabOneLogDrawerState> {
  if (!isColabOneDesktopRuntime()) {
    return { open, standalone: false, expanded: false, delta: 0 };
  }
  try {
    const res = await ColabOneDesktop.setLogDrawer(open, Math.round(width));
    if (res && typeof res.open === "boolean") {
      return {
        open: res.open,
        standalone: Boolean(res.standalone),
        expanded: Boolean(res.expanded),
        delta: Number(res.delta) || 0,
      };
    }
  } catch {
    // Non-desktop or offline mock runtime: fall back to inline pane.
  }
  return { open, standalone: false, expanded: false, delta: 0 };
}

function fallbackColabOneUpdateState(): ColabOneUpdateState {
  return {
    status: "disabled",
    current_version: "",
    available_version: "",
    is_prerelease: false,
    is_rollback: false,
    release_date: "",
    release_url: "https://github.com/luolongfei/ColabOne/releases",
    notes_raw: "",
    notes_zh: "",
    notes_en: "",
    downloaded_bytes: 0,
    total_bytes: 0,
    progress_percent: 0,
    speed_bps: 0,
    can_self_update: false,
    disabled_reason: "dev_build",
    auto_check: true,
    include_prerelease: false,
    skipped_version: "",
    last_checked_at: 0,
    error_code: "",
    error_message: "",
    rollback_versions: [],
  };
}

export async function colabOneGetUpdateState(): Promise<ColabOneUpdateState> {
  if (!isColabOneDesktopRuntime()) {
    return fallbackColabOneUpdateState();
  }
  try {
    const res = await ColabOneDesktop.getUpdateState();
    if (res && typeof res.status === "string") {
      return res;
    }
  } catch {
    // Non-desktop or offline mock runtime.
  }
  return fallbackColabOneUpdateState();
}

export async function colabOneCheckForUpdate(
  manual: boolean,
): Promise<ColabOneUpdateState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.checkForUpdate(manual);
}

export async function colabOneStartUpdateInstall(): Promise<ColabOneUpdateState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.startUpdateInstall();
}

export async function colabOneListRollbackVersions(
  forceRefresh = false,
): Promise<ColabOneRollbackVersion[]> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  const res = await ColabOneDesktop.listRollbackVersions(forceRefresh);
  return Array.isArray(res) ? res : [];
}

export async function colabOneStartUpdateRollback(
  version: string,
): Promise<ColabOneUpdateState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.startUpdateRollback(version);
}

export async function colabOneCancelUpdateInstall(): Promise<ColabOneUpdateState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.cancelUpdateInstall();
}

export async function colabOneSkipUpdateVersion(
  version: string,
): Promise<ColabOneUpdateState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.skipUpdateVersion(version);
}

export async function colabOneSetUpdatePreferences(
  autoCheck: boolean,
  includePrerelease: boolean,
): Promise<ColabOneUpdateState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.setUpdatePreferences(autoCheck, includePrerelease);
}

export async function colabOneRelaunchForUpdate(): Promise<void> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.relaunchForUpdate();
}

function hasMyGoEventBridge(): boolean {
  return (
    typeof (globalThis as { mygo?: { on?: unknown } }).mygo?.on === "function"
  );
}

export function colabOneOnUpdateState(
  listener: (state: ColabOneUpdateState) => void,
): () => void {
  if (!hasMyGoEventBridge()) {
    return () => {};
  }
  try {
    const unsubscribe = events.colaboneUpdate.on(listener);
    return typeof unsubscribe === "function" ? unsubscribe : () => {};
  } catch {
    return () => {};
  }
}

export function colabOneOnUpdateOpen(listener: () => void): () => void {
  if (!hasMyGoEventBridge()) {
    return () => {};
  }
  try {
    const unsubscribe = events.colaboneUpdateOpen.on(() => listener());
    return typeof unsubscribe === "function" ? unsubscribe : () => {};
  } catch {
    return () => {};
  }
}

export function normalizeColabOneCloseBehavior(
  value: unknown,
): ColabOneCloseBehavior {
  if (value === "quit" || value === "background" || value === "ask") {
    return value;
  }
  return "ask";
}

export function normalizeColabOneCloseAction(
  value: unknown,
): ColabOneCloseAction {
  return value === "background" ? "background" : "quit";
}

function fallbackColabOneCloseBehaviorState(): ColabOneCloseBehaviorState {
  return {
    behavior: "ask",
    default_choice: "quit",
    can_background: true,
  };
}

export async function colabOneGetCloseBehavior(): Promise<ColabOneCloseBehaviorState> {
  if (!isColabOneDesktopRuntime()) {
    return fallbackColabOneCloseBehaviorState();
  }
  try {
    const res = await ColabOneDesktop.getCloseBehavior();
    if (res && typeof res.behavior === "string") {
      return {
        behavior: normalizeColabOneCloseBehavior(res.behavior),
        default_choice: normalizeColabOneCloseAction(res.default_choice),
        can_background:
          typeof res.can_background === "boolean" ? res.can_background : true,
      };
    }
  } catch {
    // Non-desktop or offline mock runtime.
  }
  return fallbackColabOneCloseBehaviorState();
}

export async function colabOneSetCloseBehavior(
  behavior: ColabOneCloseBehavior,
): Promise<ColabOneCloseBehaviorState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  const res = await ColabOneDesktop.setCloseBehavior(behavior);
  if (res && typeof res.behavior === "string") {
    return {
      behavior: normalizeColabOneCloseBehavior(res.behavior),
      default_choice: normalizeColabOneCloseAction(res.default_choice),
      can_background:
        typeof res.can_background === "boolean" ? res.can_background : true,
    };
  }
  return {
    behavior,
    default_choice: "quit",
    can_background: true,
  };
}

export async function colabOneResolveCloseRequest(
  action: ColabOneCloseAction,
  remember: boolean,
): Promise<void> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.resolveCloseRequest(action, remember);
}

export function colabOneOnCloseRequest(
  listener: (req: ColabOneCloseRequest) => void,
): () => void {
  if (!hasMyGoEventBridge()) {
    return () => {};
  }
  try {
    const unsubscribe = events.colaboneCloseRequest.on((req) =>
      listener({
        default_choice: normalizeColabOneCloseAction(req?.default_choice),
        can_background:
          typeof req?.can_background === "boolean" ? req.can_background : true,
      }),
    );
    return typeof unsubscribe === "function" ? unsubscribe : () => {};
  } catch {
    return () => {};
  }
}

export function colabOneOnCloseBehavior(
  listener: (state: ColabOneCloseBehaviorState) => void,
): () => void {
  if (!hasMyGoEventBridge()) {
    return () => {};
  }
  try {
    const unsubscribe = events.colaboneCloseBehavior.on((st) =>
      listener({
        behavior: normalizeColabOneCloseBehavior(st?.behavior),
        default_choice: normalizeColabOneCloseAction(st?.default_choice),
        can_background:
          typeof st?.can_background === "boolean" ? st.can_background : true,
      }),
    );
    return typeof unsubscribe === "function" ? unsubscribe : () => {};
  } catch {
    return () => {};
  }
}

export async function colabOneGetWebConsoleState(): Promise<ColabOneWebConsoleState> {
  if (!isColabOneDesktopRuntime()) {
    return createColabOneDefaultWebConsoleState();
  }
  const res = await ColabOneDesktop.getWebConsoleState();
  return normalizeColabOneWebConsoleState(res);
}

export async function colabOneUpdateWebConsole(
  req: ColabOneWebConsoleUpdateRequest,
): Promise<ColabOneWebConsoleState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  const res = await ColabOneDesktop.updateWebConsole(req);
  return normalizeColabOneWebConsoleState(res);
}

export async function colabOneGetWebConsolePasscode(): Promise<string> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.getWebConsolePasscode();
}

export async function colabOneSetWebConsolePasscode(
  passcode: string,
): Promise<ColabOneWebConsoleState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  const res = await ColabOneDesktop.setWebConsolePasscode(passcode);
  return normalizeColabOneWebConsoleState(res);
}

export async function colabOneCreateWebConsoleLoginTicket(
  singleUse = false,
): Promise<ColabOneWebConsoleTicket> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.createWebConsoleLoginTicket(Boolean(singleUse));
}

export async function colabOneRevokeWebConsoleSessions(): Promise<ColabOneWebConsoleState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  const res = await ColabOneDesktop.revokeWebConsoleSessions();
  return normalizeColabOneWebConsoleState(res);
}

export function colabOneOnWebConsoleState(
  listener: (state: ColabOneWebConsoleState) => void,
): () => void {
  if (!hasMyGoEventBridge()) {
    return () => {};
  }
  try {
    const unsubscribe = events.colaboneWebConsole.on((st) =>
      listener(normalizeColabOneWebConsoleState(st)),
    );
    return typeof unsubscribe === "function" ? unsubscribe : () => {};
  } catch {
    return () => {};
  }
}

export async function colabOneGetWorkflowSharesState(): Promise<ColabOneWorkflowShareListState> {
  if (!isColabOneDesktopRuntime()) {
    return createColabOneDefaultWorkflowShareListState();
  }
  const res = await ColabOneDesktop.getWorkflowSharesState();
  return normalizeColabOneWorkflowShareListState(res);
}

export async function colabOneCreateWorkflowShare(
  req: ColabOneWorkflowShareCreateRequest,
): Promise<ColabOneWorkflowShareSummary> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  const res = await ColabOneDesktop.createWorkflowShare(req);
  const normalized = normalizeColabOneWorkflowShareSummary(res);
  if (!normalized) {
    throw new Error("创建工作流分享返回数据无效");
  }
  return normalized;
}

export async function colabOneRevokeWorkflowShare(
  shareID: string,
): Promise<ColabOneWorkflowShareListState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  const res = await ColabOneDesktop.revokeWorkflowShare(shareID);
  return normalizeColabOneWorkflowShareListState(res);
}

export async function colabOneDeleteWorkflowShare(
  shareID: string,
): Promise<ColabOneWorkflowShareListState> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  const res = await ColabOneDesktop.deleteWorkflowShare(shareID);
  return normalizeColabOneWorkflowShareListState(res);
}

export function colabOneOnWorkflowSharesState(
  listener: (state: ColabOneWorkflowShareListState) => void,
): () => void {
  if (!hasMyGoEventBridge()) {
    return () => {};
  }
  try {
    const unsubscribe = events.colaboneWorkflowShares.on((st) =>
      listener(normalizeColabOneWorkflowShareListState(st)),
    );
    return typeof unsubscribe === "function" ? unsubscribe : () => {};
  } catch {
    return () => {};
  }
}

export async function colabOneExportWorkflowOutputsZip(
  outputIDs: string[],
  suggestedName = "",
): Promise<string> {
  const cleanIDs = outputIDs
    .map((id) => String(id || "").trim())
    .filter(Boolean);
  if (!cleanIDs.length) {
    throw new Error("请至少选择一项有效作品");
  }
  if (!isColabOneDesktopRuntime()) {
    if (isColabOneWorkflowShareActive()) {
      throw new Error(DESKTOP_ONLY_WEB_ERROR);
    }
    const url = colabOneWebDownloadOutputsZipURL(cleanIDs, suggestedName);
    await triggerColabOneBrowserDownload(url, {
      fallbackFilename: suggestedName
        ? `${suggestedName.replace(/\.zip$/i, "")}.zip`
        : "colabone-gallery.zip",
    });
    return suggestedName || "colabone-gallery.zip";
  }
  return ColabOneDesktop.exportWorkflowOutputsZip(cleanIDs, suggestedName);
}

export async function colabOneGetOutputSharesState(): Promise<ColabOneOutputShareListState> {
  if (!isColabOneDesktopRuntime()) {
    if (isColabOneWorkflowShareActive() || isColabOneOutputShareActive()) {
      return createColabOneDefaultOutputShareListState();
    }
    return colabOneWebGetOutputSharesState();
  }
  const res = await ColabOneDesktop.getOutputSharesState();
  return normalizeColabOneOutputShareListState(res);
}

export async function colabOneCreateOutputShare(
  req: ColabOneOutputShareCreateRequest,
): Promise<ColabOneOutputShareSummary> {
  if (!isColabOneDesktopRuntime()) {
    if (isColabOneWorkflowShareActive() || isColabOneOutputShareActive()) {
      throw new Error(DESKTOP_ONLY_WEB_ERROR);
    }
    return colabOneWebCreateOutputShare(req);
  }
  const res = await ColabOneDesktop.createOutputShare({
    title: req.title || "",
    note: req.note || "",
    ttl_mode: req.ttl_mode || "7d",
    allow_download: Boolean(req.allow_download),
    password: req.password,
    groups: (req.groups || []).map((g) => ({
      id: g.id || "",
      title: g.title || "",
      workflow_id: g.workflow_id,
      workflow_name: g.workflow_name,
      task_id: g.task_id,
      output_ids: g.output_ids || [],
    })),
  });
  const normalized = normalizeColabOneOutputShareSummary(res);
  if (!normalized) {
    throw new Error("创建成果物分享返回数据无效");
  }
  return normalized;
}

export async function colabOneRevokeOutputShare(
  shareID: string,
): Promise<ColabOneOutputShareListState> {
  if (!isColabOneDesktopRuntime()) {
    if (isColabOneWorkflowShareActive() || isColabOneOutputShareActive()) {
      throw new Error(DESKTOP_ONLY_WEB_ERROR);
    }
    return colabOneWebRevokeOutputShare(shareID);
  }
  const res = await ColabOneDesktop.revokeOutputShare(shareID);
  return normalizeColabOneOutputShareListState(res);
}

export async function colabOneDeleteOutputShare(
  shareID: string,
): Promise<ColabOneOutputShareListState> {
  if (!isColabOneDesktopRuntime()) {
    if (isColabOneWorkflowShareActive() || isColabOneOutputShareActive()) {
      throw new Error(DESKTOP_ONLY_WEB_ERROR);
    }
    return colabOneWebDeleteOutputShare(shareID);
  }
  const res = await ColabOneDesktop.deleteOutputShare(shareID);
  return normalizeColabOneOutputShareListState(res);
}

export function colabOneOnOutputSharesState(
  listener: (state: ColabOneOutputShareListState) => void,
): () => void {
  if (!hasMyGoEventBridge()) {
    return () => {};
  }
  try {
    const unsubscribe = events.colaboneOutputShares.on((st) =>
      listener(normalizeColabOneOutputShareListState(st)),
    );
    return typeof unsubscribe === "function" ? unsubscribe : () => {};
  } catch {
    return () => {};
  }
}
