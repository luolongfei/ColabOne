import { t, tb } from "./i18n";
import type { ColabOneSnapshot } from "./types";

export const COLABONE_DEFAULT_IDLE_MINUTES = 30;

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
    balance === undefined ||
    rate === undefined ||
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
    account.rate !== undefined && Number.isFinite(account.rate);
  const hasFiniteBalance =
    account.balance !== undefined && Number.isFinite(account.balance);

  const loggedIn = !!account.logged_in;
  const rate = hasFiniteRate ? (account.rate as number) : 0;
  const active = rate > 0;
  const instanceCount = account.assignments ?? (runtimeId ? 1 : 0);
  const stale = !account.checked_at || nowMs / 1000 - account.checked_at > 60;

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
    ? t("billing.rateValue", { rate: (account.rate as number).toFixed(2) })
    : t("billing.ratePlaceholder");
  const balanceText = hasFiniteBalance
    ? `${(account.balance as number).toFixed(2)} CU`
    : t("billing.balanceUnread");
  const endurance = formatRemainingBalanceTime(account.balance, account.rate);

  return {
    loggedIn,
    active,
    rate,
    balance: account.balance,
    instanceCount,
    stale,
    statusText,
    rateText,
    balanceText,
    endurance,
  };
}

export function formatIdlePolicyLabel(minutes?: number): string {
  const preset = COLABONE_IDLE_RELEASE_OPTIONS.find(
    (opt) => opt.value === minutes,
  );
  if (preset) {
    return preset.shortLabel;
  }
  if (minutes !== undefined && Number.isFinite(minutes) && minutes > 60) {
    if (minutes % 60 === 0) {
      return t("common.hours", { value: minutes / 60 });
    }
    return t("common.hoursMinutes", {
      hours: Math.floor(minutes / 60),
      minutes: minutes % 60,
    });
  }
  const safeMinutes =
    minutes !== undefined && Number.isFinite(minutes) && minutes > 0
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
