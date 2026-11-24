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
  type ColabOneUpdateState,
} from "./colabOneBridge";
import type { ColabOneSnapshot } from "./types";

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
  ColabOneUpdateState,
};

export type ColabOneCloseBehavior = "ask" | "quit" | "background";
export type ColabOneCloseAction = "quit" | "background";

export async function colabOneSnapshotRaw(): Promise<string> {
  return ColabOneDesktop.snapshot();
}

export async function colabOneSnapshot(): Promise<ColabOneSnapshot> {
  return JSON.parse(await colabOneSnapshotRaw());
}

export async function colabOneCommand(
  action: string,
  payload: Record<string, unknown> = {},
): Promise<any> {
  return JSON.parse(await ColabOneDesktop.command({ action, payload }));
}

export const colabOneOpenURL = (url: string) => ColabOneDesktop.openURL(url);
export const colabOneReadClipboard = () => ColabOneDesktop.readClipboard();
export const colabOneWriteClipboard = (value: string) =>
  ColabOneDesktop.writeClipboard(value);
export const colabOneExportWorkflowOutput = (id: string) =>
  ColabOneDesktop.exportWorkflowOutput(id);
export const colabOneOpenComfyCanvas = (target: "window" | "browser") =>
  ColabOneDesktop.openComfyCanvas(target);
export const colabOneSetLocale = (value: string) =>
  ColabOneDesktop.setLocale(value);

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
  };
}

export async function colabOneGetUpdateState(): Promise<ColabOneUpdateState> {
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

export function colabOneOnUpdateState(
  listener: (state: ColabOneUpdateState) => void,
): () => void {
  if (typeof (globalThis as any).mygo?.on !== "function") {
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
  if (typeof (globalThis as any).mygo?.on !== "function") {
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
  if (typeof (globalThis as any).mygo?.on !== "function") {
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
  if (typeof (globalThis as any).mygo?.on !== "function") {
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
