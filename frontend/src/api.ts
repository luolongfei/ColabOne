import { ColabOneDesktop, type ColabOneLogDrawerState } from "./colabOneBridge";
import type { ColabOneSnapshot } from "./types";

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
