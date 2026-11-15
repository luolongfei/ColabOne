import { Desktop } from "./mygo";
import type { Snapshot } from "./types";
export async function snapshot(): Promise<Snapshot> {
  return JSON.parse(await Desktop.snapshot());
}
export async function command(
  action: string,
  payload: Record<string, unknown> = {},
): Promise<any> {
  return JSON.parse(await Desktop.command({ action, payload }));
}
export const openURL = (url: string) => Desktop.openURL(url);
export const exportConversation = (value: string) =>
  Desktop.exportConversation(value);
