import { formatColabOneDuration } from "./colabOneDuration";
import { colabOneLocaleCode, t, tb, type ColabOneLocale } from "./i18n";
import type { ColabOneSnapshot } from "./types";

export const COLABONE_DEFAULT_IDLE_MINUTES = 30;
export const COLABONE_RUN_LIVE_INTERP_CAP_SEC = 90;
export const COLABONE_RUN_STALE_OFFLINE_SEC = 900;
export const COLABONE_BILLING_TICKER_INTERVAL_MS = 3000;
export const COLABONE_BILLING_COMPACT_RUN_BREAKPOINT = 1080;

export type ColabOneBillingTickerSlide = "rate" | "consumed" | "duration";

export function resolveColabOneBillingTickerSlides(options: {
  rateLoading: boolean;
  runLoading: boolean;
  balanceLoading: boolean;
  runActive: boolean;
  hasCompactDuration: boolean;
  inlineRunHidden: boolean;
}): ColabOneBillingTickerSlide[] {
  if (options.rateLoading) {
    return [];
  }
  const slides: ColabOneBillingTickerSlide[] = ["rate"];
  if (
    options.inlineRunHidden &&
    options.runActive &&
    !options.balanceLoading &&
    !options.runLoading
  ) {
    slides.push("consumed");
  }
  if (options.runActive && options.hasCompactDuration) {
    slides.push("duration");
  }
  return slides;
}

export function nextColabOneBillingTickerSlide(
  slides: readonly ColabOneBillingTickerSlide[],
  current: ColabOneBillingTickerSlide,
): ColabOneBillingTickerSlide {
  if (slides.length === 0) {
    return "rate";
  }
  const idx = slides.indexOf(current);
  if (idx < 0) {
    return slides[0];
  }
  return slides[(idx + 1) % slides.length];
}

export interface IdleReleaseOption {
  value: number;
  label: string;
  shortLabel: string;
}

export const COLABONE_IDLE_RELEASE_OPTIONS: readonly IdleReleaseOption[] = [
  {
    value: 15,
    get label() {
      return t("billing.idleOption15");
    },
    get shortLabel() {
      return t("billing.idleOption15");
    },
  },
  {
    value: 30,
    get label() {
      return t("billing.idleOption30Default");
    },
    get shortLabel() {
      return t("billing.idleOption30Short");
    },
  },
  {
    value: 45,
    get label() {
      return t("billing.idleOption45");
    },
    get shortLabel() {
      return t("billing.idleOption45");
    },
  },
  {
    value: 60,
    get label() {
      return t("billing.idleOption60");
    },
    get shortLabel() {
      return t("billing.idleOption60");
    },
  },
  {
    value: 120,
    get label() {
      return t("billing.idleOption120");
    },
    get shortLabel() {
      return t("billing.idleOption120");
    },
  },
];

export type EnduranceLevel =
  "idle" | "healthy" | "low" | "critical" | "unknown";

export interface BalanceEndurance {
  text: string;
  shortText: string;
  hours: number | null;
  daysPart: number;
  hoursPart: number;
  level: EnduranceLevel;
}

export function formatRemainingBalanceTime(
  balance?: number,
  rate?: number,
): BalanceEndurance {
  if (
    typeof balance !== "number" ||
    typeof rate !== "number" ||
    !Number.isFinite(balance) ||
    !Number.isFinite(rate)
  ) {
    return {
      text: t("billing.enduranceReading"),
      shortText: t("billing.enduranceReadingShort"),
      hours: null,
      daysPart: 0,
      hoursPart: 0,
      level: "unknown",
    };
  }

  if (rate <= 0) {
    return {
      text: t("billing.enduranceIdle"),
      shortText: t("billing.enduranceIdleShort"),
      hours: null,
      daysPart: 0,
      hoursPart: 0,
      level: "idle",
    };
  }

  if (balance <= 0) {
    return {
      text: t("billing.enduranceExhausted"),
      shortText: t("billing.enduranceExhaustedShort"),
      hours: 0,
      daysPart: 0,
      hoursPart: 0,
      level: "critical",
    };
  }

  const totalHours = balance / rate;
  if (!Number.isFinite(totalHours)) {
    return {
      text: t("billing.enduranceReading"),
      shortText: t("billing.enduranceReadingShort"),
      hours: null,
      daysPart: 0,
      hoursPart: 0,
      level: "unknown",
    };
  }
  const level: EnduranceLevel =
    totalHours < 2 ? "critical" : totalHours < 12 ? "low" : "healthy";
  const daysPart = Math.floor(totalHours / 24);
  const hoursPart = Math.floor(totalHours % 24);

  let text: string;
  let shortText: string;

  if (totalHours >= 24) {
    text = t("common.approxDaysHours", { days: daysPart, hours: hoursPart });
    shortText = t("common.daysHours", { days: daysPart, hours: hoursPart });
  } else if (totalHours >= 1) {
    const mins = Math.floor((totalHours - hoursPart) * 60);
    text =
      mins > 0
        ? t("common.approxHoursMinutes", { hours: hoursPart, minutes: mins })
        : t("common.approxHours", { hours: hoursPart });
    shortText =
      mins > 0
        ? t("common.hoursMinutesShort", { hours: hoursPart, minutes: mins })
        : t("common.hours", { value: hoursPart });
  } else {
    const mins = Math.max(1, Math.floor(totalHours * 60));
    text = t("common.approxMinutes", { minutes: mins });
    shortText = t("common.approxMinutes", { minutes: mins });
  }

  return {
    text,
    shortText,
    hours: totalHours,
    daysPart,
    hoursPart,
    level,
  };
}

export interface ColabOneBillingOverview {
  loggedIn: boolean;
  active: boolean;
  rate: number;
  balance?: number;
  instanceCount: number;
  stale: boolean;
  statusText: string;
  rateText: string;
  balanceText: string;
  endurance: BalanceEndurance;
}

export function describeColabOneBilling(
  account: ColabOneSnapshot["account"],
  runtimeId?: string,
  nowMs: number = Date.now(),
): ColabOneBillingOverview {
  const hasFiniteRate =
    typeof account.rate === "number" &&
    Number.isFinite(account.rate) &&
    account.rate >= 0;
  const hasFiniteBalance =
    typeof account.balance === "number" && Number.isFinite(account.balance);

  const loggedIn = !!account.logged_in;
  const rate = hasFiniteRate ? (account.rate as number) : 0;
  const active = rate > 0;
  const instanceCount =
    typeof account.assignments === "number" &&
    Number.isFinite(account.assignments)
      ? Math.max(0, Math.floor(account.assignments))
      : runtimeId
        ? 1
        : 0;
  const safeNowMs =
    typeof nowMs === "number" && Number.isFinite(nowMs) ? nowMs : Date.now();
  const checkedAt =
    typeof account.checked_at === "number" &&
    Number.isFinite(account.checked_at) &&
    account.checked_at > 0
      ? account.checked_at
      : 0;
  const stale = !checkedAt || safeNowMs / 1000 - checkedAt > 60;

  let statusText: string;
  if (!loggedIn) {
    statusText = t("billing.statusLoggedOut");
  } else if (active) {
    statusText = t("billing.statusActive");
  } else if (stale && !hasFiniteRate) {
    statusText = t("billing.statusQuerying");
  } else if (instanceCount > 0) {
    statusText = t("billing.statusSyncingRate");
  } else {
    statusText = t("billing.statusIdle");
  }

  const rateText = hasFiniteRate
    ? t("billing.rateValue", { rate: rate.toFixed(2) })
    : t("billing.ratePlaceholder");
  const displayBalance = hasFiniteBalance
    ? Math.abs(account.balance as number) < 0.005
      ? 0
      : (account.balance as number)
    : undefined;
  const balanceText = hasFiniteBalance
    ? `${(displayBalance as number).toFixed(2)} CU`
    : t("billing.balanceUnread");
  const endurance = formatRemainingBalanceTime(
    hasFiniteBalance ? account.balance : undefined,
    hasFiniteRate ? rate : undefined,
  );

  return {
    loggedIn,
    active,
    rate,
    balance: hasFiniteBalance ? account.balance : undefined,
    instanceCount,
    stale,
    statusText,
    rateText,
    balanceText,
    endurance,
  };
}

export type ColabOneRunPhase =
  | "logged_out"
  | "idle_empty"
  | "running"
  | "completed";

export interface ColabOneRunConsumptionOverview {
  phase: ColabOneRunPhase;
  active: boolean;
  hasRecord: boolean;
  consumed: number;
  confirmedConsumed: number;
  liveProjection: number;
  consumedValueText: string;
  consumedText: string;
  elapsedSeconds: number | null;
  durationText: string;
  compactDurationText: string;
  avgRate: number | null;
  currentRate: number;
  peakRate: number;
  topup: number;
  startBalance?: number;
  endBalance?: number;
  startedAt?: number;
  endedAt?: number;
  maxInstances: number;
  syncingRate: boolean;
  interpolating: boolean;
  balanceDepleted: boolean;
}

function finiteNonNeg(value: number | null | undefined): number | undefined {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    return undefined;
  }
  return value;
}

function finitePositive(value: number | null | undefined): number | undefined {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    return undefined;
  }
  return value;
}

/**
 * Computes real-time compute unit (CU) consumption for the current continuous
 * billing run (or the most recently completed run while idle).
 *
 * Confirmed balance deltas from Google Colab are combined with bounded live
 * rate projection between 30-second account polls so the readout updates
 * smoothly without running away during network outages or exceeding balance.
 */
export function describeColabOneRunConsumption(
  account: ColabOneSnapshot["account"],
  options?: {
    setupColab?: boolean;
    runtimeState?: string;
    runtimeId?: string;
    nowMs?: number;
  },
): ColabOneRunConsumptionOverview {
  const loggedIn = Boolean(options?.setupColab || account.logged_in);
  if (!loggedIn) {
    return {
      phase: "logged_out",
      active: false,
      hasRecord: false,
      consumed: 0,
      confirmedConsumed: 0,
      liveProjection: 0,
      consumedValueText: "--",
      consumedText: "-- CU",
      elapsedSeconds: null,
      durationText: "",
      compactDurationText: "",
      avgRate: null,
      currentRate: 0,
      peakRate: 0,
      topup: 0,
      maxInstances: 0,
      syncingRate: false,
      interpolating: false,
      balanceDepleted: false,
    };
  }

  const nowMs =
    typeof options?.nowMs === "number" && Number.isFinite(options.nowMs)
      ? options.nowMs
      : Date.now();
  const nowSec = nowMs / 1000;
  const liveAccountRate = finitePositive(account.rate) ?? 0;
  const currentBalance =
    typeof account.balance === "number" && Number.isFinite(account.balance)
      ? account.balance
      : undefined;
  const hasFiniteBalance = currentBalance !== undefined;
  const runtimeActive = Boolean(
    options?.runtimeState &&
      options.runtimeState !== "stopped" &&
      options.runtimeId,
  );
  const instanceCount =
    typeof account.assignments === "number" &&
    Number.isFinite(account.assignments)
      ? Math.max(0, Math.floor(account.assignments))
      : runtimeActive
        ? 1
        : 0;
  const accountRunning = liveAccountRate > 0 || instanceCount > 0;

  const rawRun = account.current_run;
  const run =
    rawRun && finitePositive(rawRun.started_at) !== undefined ? rawRun : null;

  const runAnchorTs = run
    ? (finitePositive(run.last_sample_at) ??
      finitePositive(account.checked_at) ??
      run.started_at)
    : 0;
  const accountCheckedAt = finitePositive(account.checked_at);
  const accountConfirmedIdle = Boolean(
    run &&
      run.active &&
      account.logged_in &&
      accountCheckedAt !== undefined &&
      accountCheckedAt >= runAnchorTs &&
      !accountRunning,
  );
  const staleOfflineActiveRun = Boolean(
    run &&
      run.active &&
      !account.logged_in &&
      !accountRunning &&
      nowSec - runAnchorTs > COLABONE_RUN_STALE_OFFLINE_SEC,
  );

  if (run && run.active && !accountConfirmedIdle && !staleOfflineActiveRun) {
    const confirmed = finiteNonNeg(run.consumed) ?? 0;
    const unsettled = finiteNonNeg(run.unsettled) ?? 0;
    const effectiveRate =
      liveAccountRate > 0 ? liveAccountRate : (finitePositive(run.last_rate) ?? 0);
    const syncingRate = effectiveRate <= 0;
    const balanceDepleted =
      hasFiniteBalance && currentBalance <= 1e-6 && effectiveRate > 0;

    const sinceAnchorSec = Math.max(0, nowSec - runAnchorTs);
    const canProjectLive =
      effectiveRate > 0 &&
      !balanceDepleted &&
      (account.logged_in || liveAccountRate > 0) &&
      sinceAnchorSec <= COLABONE_RUN_STALE_OFFLINE_SEC;

    let liveProjection = 0;
    if (canProjectLive) {
      const gapSec = Math.min(COLABONE_RUN_LIVE_INTERP_CAP_SEC, sinceAnchorSec);
      const rawProj = unsettled + (effectiveRate * gapSec) / 3600;
      liveProjection = hasFiniteBalance
        ? Math.min(rawProj, Math.max(0, currentBalance))
        : rawProj;
    }

    const consumed = confirmed + liveProjection;
    const elapsedSeconds = Math.max(0, nowSec - run.started_at);
    const avgRate =
      elapsedSeconds >= 60 && consumed > 1e-6
        ? consumed / (elapsedSeconds / 3600)
        : null;
    const startBalance = finiteNonNeg(run.start_balance);
    const endBalance = hasFiniteBalance
      ? currentBalance
      : finiteNonNeg(run.end_balance);
    const peakRate = Math.max(finiteNonNeg(run.peak_rate) ?? 0, effectiveRate);
    const maxInstances = Math.max(
      Math.floor(finiteNonNeg(run.max_instances) ?? 0),
      instanceCount,
    );

    return {
      phase: "running",
      active: true,
      hasRecord: true,
      consumed,
      confirmedConsumed: confirmed,
      liveProjection,
      consumedValueText: consumed.toFixed(2),
      consumedText: `${consumed.toFixed(2)} CU`,
      elapsedSeconds,
      durationText: formatColabOneDuration(elapsedSeconds, {
        precision: "integer",
      }),
      compactDurationText: formatColabOneCompactRunDuration(elapsedSeconds),
      avgRate,
      currentRate: effectiveRate,
      peakRate,
      topup: finiteNonNeg(run.topup) ?? 0,
      startBalance,
      endBalance,
      startedAt: run.started_at,
      maxInstances,
      syncingRate,
      interpolating: liveProjection > 1e-6,
      balanceDepleted,
    };
  }

  if (accountRunning) {
    const balanceDepleted =
      hasFiniteBalance && currentBalance <= 1e-6 && liveAccountRate > 0;
    return {
      phase: "running",
      active: true,
      hasRecord: false,
      consumed: 0,
      confirmedConsumed: 0,
      liveProjection: 0,
      consumedValueText: "0.00",
      consumedText: "0.00 CU",
      elapsedSeconds: null,
      durationText: "",
      compactDurationText: "",
      avgRate: null,
      currentRate: liveAccountRate,
      peakRate: liveAccountRate,
      topup: 0,
      startBalance: currentBalance,
      endBalance: currentBalance,
      maxInstances: instanceCount,
      syncingRate: liveAccountRate <= 0,
      interpolating: false,
      balanceDepleted,
    };
  }

  if (run) {
    const confirmed = finiteNonNeg(run.consumed) ?? 0;
    const endedAt =
      finitePositive(run.ended_at) ??
      finitePositive(run.last_sample_at) ??
      run.started_at;
    const elapsedSeconds = Math.max(0, endedAt - run.started_at);
    const avgRate =
      elapsedSeconds >= 60 && confirmed > 1e-6
        ? confirmed / (elapsedSeconds / 3600)
        : null;

    return {
      phase: "completed",
      active: false,
      hasRecord: true,
      consumed: confirmed,
      confirmedConsumed: confirmed,
      liveProjection: 0,
      consumedValueText: confirmed.toFixed(2),
      consumedText: `${confirmed.toFixed(2)} CU`,
      elapsedSeconds,
      durationText: formatColabOneDuration(elapsedSeconds, {
        precision: "integer",
      }),
      compactDurationText: formatColabOneCompactRunDuration(elapsedSeconds),
      avgRate,
      currentRate: 0,
      peakRate: finiteNonNeg(run.peak_rate) ?? 0,
      topup: finiteNonNeg(run.topup) ?? 0,
      startBalance: finiteNonNeg(run.start_balance),
      endBalance: finiteNonNeg(run.end_balance),
      startedAt: run.started_at,
      endedAt,
      maxInstances: Math.floor(finiteNonNeg(run.max_instances) ?? 0),
      syncingRate: false,
      interpolating: false,
      balanceDepleted: false,
    };
  }

  return {
    phase: "idle_empty",
    active: false,
    hasRecord: false,
    consumed: 0,
    confirmedConsumed: 0,
    liveProjection: 0,
    consumedValueText: "0.00",
    consumedText: "0.00 CU",
    elapsedSeconds: null,
    durationText: "",
    compactDurationText: "",
    avgRate: null,
    currentRate: 0,
    peakRate: 0,
    topup: 0,
    maxInstances: 0,
    syncingRate: false,
    interpolating: false,
    balanceDepleted: false,
  };
}

export function formatColabOneCompactRunDuration(
  seconds: number | null | undefined,
  locale?: ColabOneLocale,
): string {
  if (typeof seconds !== "number" || !Number.isFinite(seconds)) {
    return "";
  }
  const totalSeconds = Math.floor(Math.max(0, seconds));
  if (totalSeconds < 60) {
    return t("common.secondsShort", { value: totalSeconds }, locale);
  }
  if (totalSeconds < 3600) {
    const mins = Math.floor(totalSeconds / 60);
    const secs = totalSeconds % 60;
    return t(
      "common.minutesSeconds",
      { minutes: mins, seconds: String(secs).padStart(2, "0") },
      locale,
    );
  }
  if (totalSeconds < 86400) {
    const hours = Math.floor(totalSeconds / 3600);
    const mins = Math.floor((totalSeconds % 3600) / 60);
    return t(
      "common.hoursMinutesShort",
      { hours, minutes: String(mins).padStart(2, "0") },
      locale,
    );
  }
  const days = Math.floor(totalSeconds / 86400);
  const hours = Math.floor((totalSeconds % 86400) / 3600);
  return t("common.daysHours", { days, hours }, locale);
}

export function formatColabOneRunClock(
  epochSeconds: number | null | undefined,
  nowMs: number = Date.now(),
  locale: string = colabOneLocaleCode.value,
): string {
  if (
    typeof epochSeconds !== "number" ||
    !Number.isFinite(epochSeconds) ||
    epochSeconds <= 0
  ) {
    return "";
  }
  const date = new Date(epochSeconds * 1000);
  if (!Number.isFinite(date.getTime())) {
    return "";
  }
  const refDate = Number.isFinite(nowMs) ? new Date(nowMs) : new Date();
  const sameDay =
    date.getFullYear() === refDate.getFullYear() &&
    date.getMonth() === refDate.getMonth() &&
    date.getDate() === refDate.getDate();
  if (sameDay) {
    return date.toLocaleTimeString(locale, {
      hourCycle: "h23",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  }
  return date.toLocaleString(locale, {
    hourCycle: "h23",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function formatIdlePolicyLabel(minutes?: number): string {
  const preset = COLABONE_IDLE_RELEASE_OPTIONS.find(
    (opt) => opt.value === minutes,
  );
  if (preset) {
    return preset.shortLabel;
  }
  if (typeof minutes === "number" && Number.isFinite(minutes) && minutes > 60) {
    if (minutes % 60 === 0) {
      return t("common.hours", { value: minutes / 60 });
    }
    return t("common.hoursMinutes", {
      hours: Math.floor(minutes / 60),
      minutes: minutes % 60,
    });
  }
  const safeMinutes =
    typeof minutes === "number" && Number.isFinite(minutes) && minutes > 0
      ? minutes
      : COLABONE_DEFAULT_IDLE_MINUTES;
  return t("common.minutes", { value: safeMinutes });
}

export function formatCountdownClock(
  seconds: number | null | undefined,
): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds)) {
    return t("common.calculating");
  }
  const whole = Math.ceil(Math.max(0, seconds));
  if (whole >= 3600) {
    const hours = Math.floor(whole / 3600);
    const mins = Math.floor((whole % 3600) / 60);
    const secs = whole % 60;
    return `${hours}:${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
  }
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
}

const TRANSIENT_IPC_PATTERNS: readonly RegExp[] = [
  /connection was lost/i,
  /websocket.*closed/i,
  /failed to fetch/i,
  /networkerror/i,
  /ipc.*closed/i,
];

export function isTransientIpcError(raw: string): boolean {
  if (!raw) {
    return false;
  }
  return TRANSIENT_IPC_PATTERNS.some((pattern) => pattern.test(raw));
}

export function stripColabOneErrorPrefixes(raw: unknown): string {
  return String(raw ?? "")
    .trim()
    .replace(
      /^(?:(?:Error|CallError|RuntimeError|TypeError|ValueError):\s*)+/i,
      "",
    )
    .trim();
}

export function friendlyErrorMessage(raw: string): string {
  const cleaned = stripColabOneErrorPrefixes(raw);
  if (!cleaned) {
    return "";
  }
  if (isTransientIpcError(raw) || isTransientIpcError(cleaned)) {
    return t("status.ipcReconnectingDetail");
  }
  return tb(cleaned);
}
