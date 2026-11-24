import { t, type ColabOneLocale } from "./i18n";
import type { RuntimeStatus, TaskStatus } from "./types";

export interface ColabOneDurationFormatOptions {
  /**
   * "integer": floors/rounds to whole seconds (ideal for live ticking clocks).
   * "auto": keeps one decimal place below 60 seconds (e.g. 4.2s), whole seconds at >= 60s.
   */
  precision?: "integer" | "auto";
  locale?: ColabOneLocale;
}

/**
 * Formats an elapsed duration in seconds into a human-friendly localized string
 * covering seconds, minutes, and hours (e.g. "42 秒", "12 分 34 秒", "1 小时 15 分 23 秒").
 */
export function formatColabOneDuration(
  seconds: number | null | undefined,
  options?: ColabOneDurationFormatOptions,
): string {
  if (typeof seconds !== "number" || !Number.isFinite(seconds)) {
    return "";
  }
  const safe = Math.max(0, seconds);
  const precision = options?.precision ?? "integer";
  const locale = options?.locale;

  if (precision === "auto" && safe < 60) {
    const roundedTenths = Math.round(safe * 10) / 10;
    if (roundedTenths < 60) {
      return t(
        "common.secondsShort",
        { value: roundedTenths.toFixed(1) },
        locale,
      );
    }
    // Promote 59.95..59.999 cleanly to 1 minute instead of "60.0s".
    return t("common.minutes", { value: 1 }, locale);
  }

  const totalSeconds =
    precision === "integer" ? Math.floor(safe) : Math.round(safe);

  if (totalSeconds < 60) {
    return t("common.secondsShort", { value: totalSeconds }, locale);
  }

  const hours = Math.floor(totalSeconds / 3600);
  const mins = Math.floor((totalSeconds % 3600) / 60);
  const secs = totalSeconds % 60;

  if (hours === 0) {
    if (secs === 0) {
      return t("common.minutes", { value: mins }, locale);
    }
    return t(
      "common.minutesSeconds",
      { minutes: mins, seconds: secs },
      locale,
    );
  }

  if (mins === 0 && secs === 0) {
    return t("common.hours", { value: hours }, locale);
  }
  if (secs === 0) {
    return t("common.hoursMinutesShort", { hours, minutes: mins }, locale);
  }
  return t(
    "common.hoursMinutesSeconds",
    { hours, minutes: mins, seconds: secs },
    locale,
  );
}

/**
 * Resolves elapsed seconds for a remote task without local-vs-remote clock skew.
 *
 * Works both with updated remote servers (`elapsed_seconds` / `finished_at`)
 * and already-running older remote servers (`runtime.idle.server_time - task.created_at`).
 */
export function resolveColabOneTaskElapsedSeconds(
  task: TaskStatus | null | undefined,
  runtime: Pick<RuntimeStatus, "idle"> | null | undefined,
  nowMs: number = Date.now(),
  snapshotAtMs: number = nowMs,
): number | null {
  if (!task) {
    return null;
  }

  const hasElapsedField =
    typeof task.elapsed_seconds === "number" &&
    Number.isFinite(task.elapsed_seconds) &&
    task.elapsed_seconds >= 0;
  const hasCreatedAt =
    typeof task.created_at === "number" &&
    Number.isFinite(task.created_at) &&
    task.created_at > 0;
  const hasFinishedAt =
    typeof task.finished_at === "number" &&
    Number.isFinite(task.finished_at) &&
    task.finished_at > 0;

  if (task.state !== "running") {
    if (hasElapsedField) {
      return task.elapsed_seconds as number;
    }
    if (hasFinishedAt && hasCreatedAt) {
      return Math.max(0, (task.finished_at as number) - (task.created_at as number));
    }
    return null;
  }

  const localDeltaSec =
    Number.isFinite(nowMs) && Number.isFinite(snapshotAtMs)
      ? Math.max(0, (nowMs - snapshotAtMs) / 1000)
      : 0;

  if (hasElapsedField) {
    return (task.elapsed_seconds as number) + localDeltaSec;
  }

  const serverTime = runtime?.idle?.server_time;
  const hasServerTime =
    typeof serverTime === "number" &&
    Number.isFinite(serverTime) &&
    serverTime > 0;

  if (hasCreatedAt) {
    if (hasServerTime) {
      return (
        Math.max(0, (serverTime as number) - (task.created_at as number)) +
        localDeltaSec
      );
    }
    if (Number.isFinite(nowMs)) {
      return Math.max(0, nowMs / 1000 - (task.created_at as number));
    }
  }

  const dlElapsed = task.download?.elapsed_seconds;
  if (typeof dlElapsed === "number" && Number.isFinite(dlElapsed) && dlElapsed >= 0) {
    return dlElapsed + localDeltaSec;
  }

  return null;
}
