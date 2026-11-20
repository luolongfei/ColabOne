import type { ColabOneSnapshot } from "./types";

export const COLABONE_DEFAULT_IDLE_MINUTES = 30;

export interface IdleReleaseOption {
  value: number;
  label: string;
  shortLabel: string;
}

export const COLABONE_IDLE_RELEASE_OPTIONS: readonly IdleReleaseOption[] = [
  { value: 15, label: "15 分钟", shortLabel: "15 分钟" },
  { value: 30, label: "30 分钟（默认）", shortLabel: "30 分钟" },
  { value: 45, label: "45 分钟", shortLabel: "45 分钟" },
  { value: 60, label: "1 小时", shortLabel: "1 小时" },
  { value: 120, label: "2 小时", shortLabel: "2 小时" },
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
      text: "正在读取余额…",
      shortText: "读取中",
      hours: null,
      daysPart: 0,
      hoursPart: 0,
      level: "unknown",
    };
  }

  if (rate <= 0) {
    return {
      text: "当前未扣费 · 余额不消耗",
      shortText: "未消耗",
      hours: null,
      daysPart: 0,
      hoursPart: 0,
      level: "idle",
    };
  }

  if (balance <= 0) {
    return {
      text: "0 天 0 小时（余额已耗尽）",
      shortText: "已耗尽",
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
    text = `约 ${daysPart} 天 ${hoursPart} 小时`;
    shortText = `${daysPart} 天 ${hoursPart} 小时`;
  } else if (totalHours >= 1) {
    const mins = Math.floor((totalHours - hoursPart) * 60);
    text =
      mins > 0 ? `约 ${hoursPart} 小时 ${mins} 分钟` : `约 ${hoursPart} 小时`;
    shortText = mins > 0 ? `${hoursPart} 小时 ${mins} 分` : `${hoursPart} 小时`;
  } else {
    const mins = Math.max(1, Math.floor(totalHours * 60));
    text = `约 ${mins} 分钟`;
    shortText = `约 ${mins} 分钟`;
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
    statusText = "未登录";
  } else if (active) {
    statusText = "正在扣费";
  } else if (stale && !hasFiniteRate) {
    statusText = "计费查询中";
  } else if (instanceCount > 0) {
    statusText = "实例运行 · 费率同步中";
  } else {
    statusText = "当前未扣费";
  }

  const rateText = hasFiniteRate
    ? `${(account.rate as number).toFixed(2)} CU/小时`
    : "-- CU/小时";
  const balanceText = hasFiniteBalance
    ? `${(account.balance as number).toFixed(2)} CU`
    : "未读取";
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
      return `${minutes / 60} 小时`;
    }
    return `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分钟`;
  }
  const safeMinutes =
    minutes !== undefined && Number.isFinite(minutes) && minutes > 0
      ? minutes
      : COLABONE_DEFAULT_IDLE_MINUTES;
  return `${safeMinutes} 分钟`;
}

export function formatCountdownClock(
  seconds: number | null | undefined,
): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds)) {
    return "计算中";
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

export function friendlyErrorMessage(raw: string): string {
  const cleaned = raw
    .trim()
    .replace(/^(?:Error:\s*)+/i, "")
    .trim();
  if (!cleaned) {
    return "";
  }
  if (isTransientIpcError(raw) || isTransientIpcError(cleaned)) {
    return "与本地后台服务的连接短暂中断，正在自动重连…";
  }
  return cleaned;
}
