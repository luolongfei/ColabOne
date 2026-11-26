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
  colabOneWebSessionExpired,
  colabOneWebSnapshotRaw,
  createColabOneDefaultWebConsoleState,
  fetchColabOneWebCapabilities,
  isColabOneDesktopRuntime,
  isColabOneWebRuntime,
  normalizeColabOneWebConsoleState,
  validateColabOneWebCanvasURL,
  validateColabOneWebExternalURL,
  type ColabOneWebCapabilities,
} from "./colabOneWebConsole";
import type { ColabOneSnapshot } from "./types";

export {
  COLABONE_WEB_AUTH_REQUIRED_EVENT,
  colabOneRuntimeCapabilities,
  colabOneWebLogout,
  colabOneWebOpenDeferredTab,
  colabOneWebSessionExpired,
  createColabOneDefaultWebConsoleState,
  fetchColabOneWebCapabilities,
  isColabOneDesktopRuntime,
  isColabOneWebRuntime,
  normalizeColabOneWebConsoleState,
  validateColabOneWebExternalURL,
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
  ColabOneRollbackVersion,
  ColabOneUpdateState,
  ColabOneWebCapabilities,
  ColabOneWebConsoleState,
  ColabOneWebConsoleTicket,
  ColabOneWebConsoleUpdateRequest,
};

export type ColabOneCloseBehavior = "ask" | "quit" | "background";
export type ColabOneCloseAction = "quit" | "background";

export async function colabOneSnapshotRaw(): Promise<string> {
  if (!isColabOneDesktopRuntime()) {
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
    if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
      return navigator.clipboard.writeText(value);
    }
    throw new Error("Clipboard write unavailable in this browser");
  }
  return ColabOneDesktop.writeClipboard(value);
}

export async function colabOneExportWorkflowOutput(id: string): Promise<string> {
  if (!isColabOneDesktopRuntime()) {
    return colabOneWebDownloadOutput(id);
  }
  return ColabOneDesktop.exportWorkflowOutput(id);
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

export const colabOneGetMigrationExportSummary = (
  uiPrefs: ColabOneMigrationUIPrefs | null,
) => ColabOneDesktop.getMigrationExportSummary(uiPrefs);

export const colabOneExportMigrationFile = (
  req: ColabOneMigrationExportRequest,
) => ColabOneDesktop.exportMigrationFile(req);

export const colabOnePickMigrationFile = () =>
  ColabOneDesktop.pickMigrationFile();

export const colabOneDecryptMigrationPreview = (
  fileToken: string,
  password: string,
) => ColabOneDesktop.decryptMigrationPreview(fileToken, password);

export const colabOneApplyMigrationPreview = (
  req: ColabOneMigrationApplyRequest,
) => ColabOneDesktop.applyMigrationPreview(req);

export const colabOneCancelMigrationSession = (sessionID = "") =>
  ColabOneDesktop.cancelMigrationSession(sessionID);

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
  return ColabOneDesktop.checkForUpdate(manual);
}

export async function colabOneStartUpdateInstall(): Promise<ColabOneUpdateState> {
  return ColabOneDesktop.startUpdateInstall();
}

export async function colabOneListRollbackVersions(
  forceRefresh = false,
): Promise<ColabOneRollbackVersion[]> {
  const res = await ColabOneDesktop.listRollbackVersions(forceRefresh);
  return Array.isArray(res) ? res : [];
}

export async function colabOneStartUpdateRollback(
  version: string,
): Promise<ColabOneUpdateState> {
  return ColabOneDesktop.startUpdateRollback(version);
}

export async function colabOneCancelUpdateInstall(): Promise<ColabOneUpdateState> {
  return ColabOneDesktop.cancelUpdateInstall();
}

export async function colabOneSkipUpdateVersion(
  version: string,
): Promise<ColabOneUpdateState> {
  return ColabOneDesktop.skipUpdateVersion(version);
}

export async function colabOneSetUpdatePreferences(
  autoCheck: boolean,
  includePrerelease: boolean,
): Promise<ColabOneUpdateState> {
  return ColabOneDesktop.setUpdatePreferences(autoCheck, includePrerelease);
}

export async function colabOneRelaunchForUpdate(): Promise<void> {
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
  try {
    const res = await ColabOneDesktop.getWebConsoleState();
    return normalizeColabOneWebConsoleState(res);
  } catch {
    // Non-desktop or offline mock runtime.
  }
  return createColabOneDefaultWebConsoleState();
}

const DESKTOP_ONLY_WEB_ERROR = "该桌面专属操作仅支持在宿主机客户端执行";

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

export async function colabOneCreateWebConsoleLoginTicket(): Promise<ColabOneWebConsoleTicket> {
  if (!isColabOneDesktopRuntime()) {
    throw new Error(DESKTOP_ONLY_WEB_ERROR);
  }
  return ColabOneDesktop.createWebConsoleLoginTicket();
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
