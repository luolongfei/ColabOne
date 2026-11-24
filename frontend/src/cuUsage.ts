import { t } from "./i18n";
import type { ColabOneLocale } from "./i18n/flags";
import type { ColabOneCuUsageBucket, ColabOneCuUsageResponse } from "./types";

export type ColabOneCuUsageRangeMode = "24h" | "day" | "7d" | "30d";
export type ColabOneCuUsageMetric = "consumed" | "rate" | "balance";

export interface ColabOneCuUsageSlot {
  index: number;
  startSec: number;
  endSec: number;
  dateKey: string;
  axisLabel: string;
  shortLabel: string;
  tooltipRangeLabel: string;
  isCurrent: boolean;
  isFuture: boolean;
}

export interface ColabOneCuUsageWindow {
  mode: ColabOneCuUsageRangeMode;
  granularity: "hour" | "day";
  queryStartSec: number;
  queryEndSec: number;
  selectedDateKey: string;
  anchorEndDateKey: string;
  todayDateKey: string;
  isLive: boolean;
  isAtPresent: boolean;
  spanLabel: string;
  slots: ColabOneCuUsageSlot[];
}

export interface ColabOneCuUsagePoint extends ColabOneCuUsageSlot {
  consumed: number;
  topup: number;
  rateAvg: number;
  rateMax: number;
  activeSeconds: number;
  maxInstances: number;
  samples: number;
  estimated: boolean;
  balanceStart: number | null;
  balanceEnd: number | null;
  observedBalance: boolean;
  hasData: boolean;
}

export interface ColabOneCuUsageSummary {
  totalConsumed: number;
  totalTopup: number;
  totalActiveSeconds: number;
  activeAvgRate: number;
  windowAvgRate: number;
  peakConsumed: number;
  peakConsumedLabel: string;
  peakConsumedIndex: number;
  peakRate: number;
  peakRateLabel: string;
  startBalance: number | null;
  endBalance: number | null;
  maxInstances: number;
  hasAnySample: boolean;
  hasAnyConsumption: boolean;
  hasAnyBalance: boolean;
}

export interface ColabOneCuChartBar {
  index: number;
  x: number;
  y: number;
  width: number;
  height: number;
  cx: number;
  cy: number;
  value: number;
  isCurrent: boolean;
  isFuture: boolean;
  hasData: boolean;
  topup: number;
}

export interface ColabOneCuChartPointGeom {
  index: number;
  cx: number;
  cy: number;
  value: number;
  observed: boolean;
  isCurrent: boolean;
  isFuture: boolean;
  topup: number;
}

export interface ColabOneCuChartTick {
  value: number;
  pos: number;
  label: string;
}

export interface ColabOneCuChartXTick {
  index: number;
  x: number;
  label: string;
}

export interface ColabOneCuBalanceSegment {
  path: string;
  dashed: boolean;
}

export interface ColabOneCuChartLayout {
  width: number;
  height: number;
  plotLeft: number;
  plotTop: number;
  plotRight: number;
  plotBottom: number;
  plotWidth: number;
  plotHeight: number;
  yMin: number;
  yMax: number;
  yTicks: ColabOneCuChartTick[];
  xTicks: ColabOneCuChartXTick[];
  bars: ColabOneCuChartBar[];
  points: ColabOneCuChartPointGeom[];
  linePath: string;
  areaPath: string;
  secondaryLinePath: string;
  balanceSegments: ColabOneCuBalanceSegment[];
  stepWidth: number;
}

export interface ColabOneCalendarCell {
  dateKey: string;
  day: number;
  inCurrentMonth: boolean;
  isToday: boolean;
  isSelected: boolean;
  isFuture: boolean;
  hasUsage: boolean;
  hasRecord: boolean;
}

export interface ColabOneCalendarMonth {
  year: number;
  monthIndex: number;
  monthLabel: string;
  weekdays: string[];
  cells: ColabOneCalendarCell[];
  canGoNextMonth: boolean;
}

const HOUR_SEC = 3600;
const EPSILON = 1e-6;

function pad2(n: number): string {
  return String(Math.floor(n)).padStart(2, "0");
}

function round4(n: number): number {
  if (!Number.isFinite(n)) return 0;
  const r = Math.round(n * 10000) / 10000;
  return Math.abs(r) < EPSILON ? 0 : r;
}

function fmtCoord(n: number): string {
  return String(Math.round(n * 100) / 100);
}

/** Format a local Date into YYYY-MM-DD. */
export function formatColabOneDateKey(date: Date): string {
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
}

/** Parse a YYYY-MM-DD local date key into { year, monthIndex (0-11), day } or null. */
export function parseColabOneDateKey(
  raw: string | null | undefined,
): { year: number; monthIndex: number; day: number } | null {
  const m = String(raw ?? "")
    .trim()
    .match(/^(\d{4})-(\d{2})-(\d{2})$/);
  if (!m) return null;
  const year = Number(m[1]);
  const month = Number(m[2]);
  const day = Number(m[3]);
  if (month < 1 || month > 12 || day < 1 || day > 31) return null;
  const check = new Date(year, month - 1, day);
  if (
    check.getFullYear() !== year ||
    check.getMonth() !== month - 1 ||
    check.getDate() !== day
  ) {
    return null;
  }
  return { year, monthIndex: month - 1, day };
}

/** Shift a YYYY-MM-DD local date key by deltaDays (positive or negative). */
export function shiftColabOneDateKey(
  dateKey: string,
  deltaDays: number,
): string {
  const parsed = parseColabOneDateKey(dateKey);
  if (!parsed) return dateKey;
  const d = new Date(parsed.year, parsed.monthIndex, parsed.day + deltaDays);
  return formatColabOneDateKey(d);
}

/** Clamp a dateKey so it never exceeds maxDateKey (lexicographic YYYY-MM-DD). */
export function clampColabOneDateKey(
  dateKey: string,
  maxDateKey: string,
): string {
  if (!parseColabOneDateKey(dateKey)) return maxDateKey;
  return dateKey > maxDateKey ? maxDateKey : dateKey;
}

function formatMonthDay(date: Date): string {
  return `${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
}

function formatHourMinute(date: Date): string {
  return `${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
}

/**
 * Resolves the active time window and local-timezone bucket slots for:
 * - "24h": live rolling last 24 hours ending with the current hour
 * - "day": full local calendar day [00:00, next 00:00) (DST-safe)
 * - "7d" / "30d": 7 or 30 local calendar days ending on anchorEndDate
 */
export function resolveColabOneCuUsageWindow(options: {
  mode: ColabOneCuUsageRangeMode;
  selectedDateKey?: string;
  anchorEndDateKey?: string;
  nowMs?: number;
}): ColabOneCuUsageWindow {
  const nowMs =
    typeof options.nowMs === "number" && Number.isFinite(options.nowMs)
      ? options.nowMs
      : Date.now();
  const nowDate = new Date(nowMs);
  const nowSec = nowMs / 1000;
  const todayDateKey = formatColabOneDateKey(nowDate);
  const mode = options.mode;

  if (mode === "24h") {
    const currentHourStart = new Date(
      nowDate.getFullYear(),
      nowDate.getMonth(),
      nowDate.getDate(),
      nowDate.getHours(),
      0,
      0,
      0,
    );
    const currentHourMs = currentHourStart.getTime();
    const slots: ColabOneCuUsageSlot[] = [];
    for (let i = 0; i < 24; i++) {
      const startMs = currentHourMs - (23 - i) * HOUR_SEC * 1000;
      const endMs = startMs + HOUR_SEC * 1000;
      const sDate = new Date(startMs);
      const eDate = new Date(endMs);
      const hhmm = formatHourMinute(sDate);
      const rawEndHhmm = formatHourMinute(eDate);
      const endHhmm =
        rawEndHhmm === hhmm ? `${pad2((sDate.getHours() + 1) % 24)}:00` : rawEndHhmm;
      const isMidnight = sDate.getHours() === 0 && i > 0;
      const axisLabel = isMidnight ? formatMonthDay(sDate) : hhmm;
      slots.push({
        index: i,
        startSec: Math.floor(startMs / 1000),
        endSec: Math.floor(endMs / 1000),
        dateKey: formatColabOneDateKey(sDate),
        axisLabel,
        shortLabel: hhmm,
        tooltipRangeLabel: `${formatMonthDay(sDate)} ${hhmm} – ${endHhmm}`,
        isCurrent: i === 23,
        isFuture: false,
      });
    }
    const firstDate = new Date(slots[0].startSec * 1000);
    const spanLabel = `${formatMonthDay(firstDate)} ${formatHourMinute(firstDate)} – ${formatMonthDay(nowDate)} ${formatHourMinute(nowDate)}`;
    return {
      mode,
      granularity: "hour",
      queryStartSec: slots[0].startSec,
      queryEndSec: slots[slots.length - 1].endSec,
      selectedDateKey: todayDateKey,
      anchorEndDateKey: todayDateKey,
      todayDateKey,
      isLive: true,
      isAtPresent: true,
      spanLabel,
      slots,
    };
  }

  if (mode === "day") {
    const safeKey = clampColabOneDateKey(
      options.selectedDateKey || todayDateKey,
      todayDateKey,
    );
    const parsed = parseColabOneDateKey(safeKey)!;
    const dayStartMs = new Date(
      parsed.year,
      parsed.monthIndex,
      parsed.day,
      0,
      0,
      0,
      0,
    ).getTime();
    const nextDayStartMs = new Date(
      parsed.year,
      parsed.monthIndex,
      parsed.day + 1,
      0,
      0,
      0,
      0,
    ).getTime();

    const slots: ColabOneCuUsageSlot[] = [];
    let idx = 0;
    for (let ms = dayStartMs; ms < nextDayStartMs; ms += HOUR_SEC * 1000) {
      const endMs = Math.min(nextDayStartMs, ms + HOUR_SEC * 1000);
      const sDate = new Date(ms);
      const eDate = new Date(endMs);
      const startSec = Math.floor(ms / 1000);
      const endSec = Math.floor(endMs / 1000);
      const hhmm = formatHourMinute(sDate);
      const rawEndHhmm = formatHourMinute(eDate);
      const endHhmm =
        endMs === nextDayStartMs
          ? "24:00"
          : rawEndHhmm === hhmm
            ? `${pad2((sDate.getHours() + 1) % 24)}:00`
            : rawEndHhmm;
      const isCurrent = nowSec >= startSec && nowSec < endSec;
      const isFuture = startSec > nowSec;
      slots.push({
        index: idx++,
        startSec,
        endSec,
        dateKey: safeKey,
        axisLabel: hhmm,
        shortLabel: hhmm,
        tooltipRangeLabel: `${safeKey} ${hhmm} – ${endHhmm}`,
        isCurrent,
        isFuture,
      });
    }
    return {
      mode,
      granularity: "hour",
      queryStartSec: slots[0].startSec,
      queryEndSec: slots[slots.length - 1].endSec,
      selectedDateKey: safeKey,
      anchorEndDateKey: safeKey,
      todayDateKey,
      isLive: false,
      isAtPresent: safeKey >= todayDateKey,
      spanLabel: `${safeKey} 00:00 – 24:00`,
      slots,
    };
  }

  const dayCount = mode === "30d" ? 30 : 7;
  const endKey = clampColabOneDateKey(
    options.anchorEndDateKey || todayDateKey,
    todayDateKey,
  );
  const startKey = shiftColabOneDateKey(endKey, -(dayCount - 1));
  const startParsed = parseColabOneDateKey(startKey)!;
  const slots: ColabOneCuUsageSlot[] = [];

  for (let i = 0; i < dayCount; i++) {
    const dStart = new Date(
      startParsed.year,
      startParsed.monthIndex,
      startParsed.day + i,
      0,
      0,
      0,
      0,
    );
    const dEnd = new Date(
      startParsed.year,
      startParsed.monthIndex,
      startParsed.day + i + 1,
      0,
      0,
      0,
      0,
    );
    const dKey = formatColabOneDateKey(dStart);
    const md = formatMonthDay(dStart);
    const startSec = Math.floor(dStart.getTime() / 1000);
    const endSec = Math.floor(dEnd.getTime() / 1000);
    slots.push({
      index: i,
      startSec,
      endSec,
      dateKey: dKey,
      axisLabel: md,
      shortLabel: md,
      tooltipRangeLabel: dKey,
      isCurrent: dKey === todayDateKey,
      isFuture: dKey > todayDateKey,
    });
  }

  return {
    mode,
    granularity: "day",
    queryStartSec: slots[0].startSec,
    queryEndSec: slots[slots.length - 1].endSec,
    selectedDateKey: endKey,
    anchorEndDateKey: endKey,
    todayDateKey,
    isLive: endKey === todayDateKey,
    isAtPresent: endKey >= todayDateKey,
    spanLabel: `${startKey} – ${endKey}`,
    slots,
  };
}

/**
 * Aggregates raw 30-minute backend CU usage buckets into the window's hourly or daily slots,
 * computing carried-forward balances and period summary KPIs.
 */
export function aggregateColabOneCuSeries(
  window: ColabOneCuUsageWindow,
  response: ColabOneCuUsageResponse | null | undefined,
  nowMs: number = Date.now(),
): {
  points: ColabOneCuUsagePoint[];
  summary: ColabOneCuUsageSummary;
} {
  const rawBuckets: ColabOneCuUsageBucket[] = Array.isArray(response?.buckets)
    ? response!.buckets
    : [];

  interface Acc {
    consumed: number;
    topup: number;
    rateWeightSum: number;
    rateWeightSec: number;
    fallbackRateMax: number;
    rateMax: number;
    activeSeconds: number;
    maxInstances: number;
    samples: number;
    estimated: boolean;
    firstBalTs: number;
    lastBalTs: number;
    balanceStart: number | null;
    balanceEnd: number | null;
  }

  const accs: Acc[] = window.slots.map(() => ({
    consumed: 0,
    topup: 0,
    rateWeightSum: 0,
    rateWeightSec: 0,
    fallbackRateMax: 0,
    rateMax: 0,
    activeSeconds: 0,
    maxInstances: 0,
    samples: 0,
    estimated: false,
    firstBalTs: Infinity,
    lastBalTs: -Infinity,
    balanceStart: null,
    balanceEnd: null,
  }));

  for (const b of rawBuckets) {
    if (!b || !Number.isFinite(b.ts)) continue;
    const bStart = b.ts;
    const bDur = Number.isFinite(b.duration) && b.duration > 0 ? b.duration : 1800;
    const bEnd = bStart + bDur;

    for (let i = 0; i < window.slots.length; i++) {
      const slot = window.slots[i];
      if (slot.endSec <= bStart) continue;
      if (slot.startSec >= bEnd) break;
      const overlapStart = Math.max(bStart, slot.startSec);
      const overlapEnd = Math.min(bEnd, slot.endSec);
      const overlap = overlapEnd - overlapStart;
      if (overlap <= 0) continue;
      const frac = overlap / bDur;
      const acc = accs[i];

      if (Number.isFinite(b.consumed) && b.consumed > EPSILON) {
        acc.consumed += b.consumed * frac;
      }
      if (Number.isFinite(b.topup) && b.topup > EPSILON) {
        acc.topup += b.topup * frac;
      }
      const actSec =
        Number.isFinite(b.active_seconds) && b.active_seconds > 0
          ? b.active_seconds * frac
          : 0;
      acc.activeSeconds += actSec;

      if (Number.isFinite(b.rate_avg) && b.rate_avg > 0) {
        if (actSec > 0) {
          acc.rateWeightSum += b.rate_avg * actSec;
          acc.rateWeightSec += actSec;
        } else {
          acc.fallbackRateMax = Math.max(acc.fallbackRateMax, b.rate_avg);
        }
      }
      if (Number.isFinite(b.rate_max) && b.rate_max > acc.rateMax) {
        acc.rateMax = b.rate_max;
      }
      if (Number.isFinite(b.max_instances) && b.max_instances > acc.maxInstances) {
        acc.maxInstances = Math.floor(b.max_instances);
      }
      if (Number.isFinite(b.samples) && b.samples > 0) {
        acc.samples += Math.max(1, Math.round(b.samples * frac));
      }
      if (b.estimated && b.consumed > EPSILON) {
        acc.estimated = true;
      }
      if (b.has_balance) {
        if (
          typeof b.balance_start === "number" &&
          Number.isFinite(b.balance_start) &&
          bStart < acc.firstBalTs
        ) {
          acc.firstBalTs = bStart;
          acc.balanceStart = b.balance_start;
        }
        if (
          typeof b.balance_end === "number" &&
          Number.isFinite(b.balance_end) &&
          bEnd >= acc.lastBalTs
        ) {
          acc.lastBalTs = bEnd;
          acc.balanceEnd = b.balance_end;
        }
      }
    }
  }

  let runningBalance: number | null =
    typeof response?.initial_balance === "number" &&
    Number.isFinite(response.initial_balance)
      ? round4(response.initial_balance)
      : null;

  const liveBalance: number | null =
    typeof response?.current_balance === "number" &&
    Number.isFinite(response.current_balance)
      ? round4(response.current_balance)
      : null;

  const liveRate: number | null =
    typeof response?.current_rate === "number" &&
    Number.isFinite(response.current_rate) &&
    response.current_rate > 0
      ? round4(response.current_rate)
      : null;

  const points: ColabOneCuUsagePoint[] = [];
  let totalConsumed = 0;
  let totalTopup = 0;
  let totalActiveSeconds = 0;
  let totalRateWeightSum = 0;
  let totalRateWeightSec = 0;
  let peakConsumed = 0;
  let peakConsumedLabel = "";
  let peakConsumedIndex = -1;
  let peakRate = 0;
  let peakRateLabel = "";
  let maxInstances = 0;
  let startBalance: number | null = runningBalance;
  let endBalance: number | null = runningBalance;
  let hasAnySample = false;
  let hasAnyConsumption = false;
  let hasAnyBalance = runningBalance !== null;

  for (let i = 0; i < window.slots.length; i++) {
    const slot = window.slots[i];
    const acc = accs[i];
    const slotMaxSec = Math.max(0, slot.endSec - slot.startSec);
    const activeSeconds = Math.min(slotMaxSec, Math.round(acc.activeSeconds * 10) / 10);
    const consumed = round4(acc.consumed);
    const topup = round4(acc.topup);
    let rateAvg =
      acc.rateWeightSec > 0
        ? round4(acc.rateWeightSum / acc.rateWeightSec)
        : round4(acc.fallbackRateMax);
    let rateMax = round4(acc.rateMax);

    if (slot.isCurrent && liveRate !== null) {
      if (liveRate > rateMax) {
        rateMax = liveRate;
      }
      if (rateAvg === 0) {
        rateAvg = liveRate;
      }
    }

    let balanceStart = acc.balanceStart !== null ? round4(acc.balanceStart) : null;
    let balanceEnd = acc.balanceEnd !== null ? round4(acc.balanceEnd) : null;
    let observedBalance = balanceEnd !== null;
    const liveCheckedFresh =
      typeof response?.checked_at === "number" &&
      Number.isFinite(response.checked_at) &&
      response.checked_at >= slot.startSec;

    if (observedBalance) {
      if (balanceStart === null) {
        balanceStart = runningBalance ?? balanceEnd;
      }
      if (slot.isCurrent && liveBalance !== null && response?.logged_in && liveCheckedFresh) {
        balanceEnd = liveBalance;
      }
      runningBalance = balanceEnd;
      hasAnyBalance = true;
    } else if (slot.isCurrent && liveBalance !== null && response?.logged_in) {
      balanceStart = runningBalance ?? liveBalance;
      balanceEnd = liveBalance;
      observedBalance = true;
      runningBalance = liveBalance;
      hasAnyBalance = true;
    } else if (!slot.isFuture && runningBalance !== null) {
      balanceStart = runningBalance;
      balanceEnd = runningBalance;
      observedBalance = false;
    }

    if (startBalance === null && balanceStart !== null) {
      startBalance = balanceStart;
    }
    if (balanceEnd !== null && !slot.isFuture) {
      endBalance = balanceEnd;
    }

    const hasData =
      acc.samples > 0 || consumed > EPSILON || topup > EPSILON || observedBalance;
    if (acc.samples > 0 || (slot.isCurrent && liveBalance !== null && response?.logged_in)) {
      hasAnySample = true;
    }
    if (consumed > EPSILON) {
      hasAnyConsumption = true;
    }

    totalConsumed += consumed;
    totalTopup += topup;
    totalActiveSeconds += activeSeconds;
    if (acc.rateWeightSec > 0) {
      totalRateWeightSum += acc.rateWeightSum;
      totalRateWeightSec += acc.rateWeightSec;
    }
    if (consumed > peakConsumed) {
      peakConsumed = consumed;
      peakConsumedLabel = slot.tooltipRangeLabel;
      peakConsumedIndex = i;
    }
    if (rateMax > peakRate) {
      peakRate = rateMax;
      peakRateLabel = slot.tooltipRangeLabel;
    }
    if (acc.maxInstances > maxInstances) {
      maxInstances = acc.maxInstances;
    }

    points.push({
      ...slot,
      consumed,
      topup,
      rateAvg,
      rateMax,
      activeSeconds,
      maxInstances: acc.maxInstances,
      samples: acc.samples,
      estimated: acc.estimated,
      balanceStart,
      balanceEnd,
      observedBalance,
      hasData,
    });
  }

  const elapsedEndSec = Math.min(window.queryEndSec, nowMs / 1000);
  const elapsedHours = Math.max(
    1 / 60,
    (elapsedEndSec - window.queryStartSec) / HOUR_SEC,
  );
  const windowAvgRate = round4(totalConsumed / elapsedHours);
  let activeAvgRate = 0;
  if (totalRateWeightSec > 0) {
    activeAvgRate = round4(totalRateWeightSum / totalRateWeightSec);
  } else if (totalActiveSeconds > 0 && totalConsumed > 0) {
    activeAvgRate = round4(totalConsumed / (totalActiveSeconds / HOUR_SEC));
  }

  return {
    points,
    summary: {
      totalConsumed: round4(totalConsumed),
      totalTopup: round4(totalTopup),
      totalActiveSeconds: Math.round(totalActiveSeconds),
      activeAvgRate,
      windowAvgRate,
      peakConsumed: round4(peakConsumed),
      peakConsumedLabel,
      peakConsumedIndex,
      peakRate: round4(peakRate),
      peakRateLabel,
      startBalance,
      endBalance,
      maxInstances,
      hasAnySample,
      hasAnyConsumption,
      hasAnyBalance,
    },
  };
}

/** Computes a human-friendly nice step size (1, 2, 2.5, 5 × 10^k). */
export function niceColabOneAxisStep(rawRange: number, tickCount = 4): number {
  if (!Number.isFinite(rawRange) || rawRange <= 0) return 0.25;
  const rough = rawRange / Math.max(1, tickCount);
  const pow10 = Math.pow(10, Math.floor(Math.log10(rough)));
  const frac = rough / pow10;
  let niceFrac: number;
  if (frac <= 1) niceFrac = 1;
  else if (frac <= 2) niceFrac = 2;
  else if (frac <= 2.5) niceFrac = 2.5;
  else if (frac <= 5) niceFrac = 5;
  else niceFrac = 10;
  return niceFrac * pow10;
}

export function formatColabOneAxisValue(value: number, step: number): string {
  if (!Number.isFinite(value)) return "0";
  if (Math.abs(value) < EPSILON) return "0";
  if (step >= 10) return String(Math.round(value));
  if (step >= 1) {
    return Number.isInteger(Math.round(value * 10) / 10)
      ? String(Math.round(value))
      : value.toFixed(1);
  }
  if (step >= 0.1) return value.toFixed(1);
  return value.toFixed(2);
}

/**
 * Fritsch-Carlson monotone cubic Hermite spline SVG path.
 * Never overshoots below minY or creates unnatural loops between flat segments.
 */
function buildMonotoneSplinePath(
  pts: Array<{ x: number; y: number }>,
  minY: number,
  maxY: number,
): string {
  const n = pts.length;
  if (n === 0) return "";
  if (n === 1) return `M ${fmtCoord(pts[0].x)} ${fmtCoord(pts[0].y)}`;
  if (n === 2) {
    return `M ${fmtCoord(pts[0].x)} ${fmtCoord(pts[0].y)} L ${fmtCoord(pts[1].x)} ${fmtCoord(pts[1].y)}`;
  }

  const dx = new Array<number>(n - 1);
  const dy = new Array<number>(n - 1);
  const slope = new Array<number>(n - 1);
  for (let i = 0; i < n - 1; i++) {
    dx[i] = pts[i + 1].x - pts[i].x;
    dy[i] = pts[i + 1].y - pts[i].y;
    slope[i] = dx[i] !== 0 ? dy[i] / dx[i] : 0;
  }

  const tangents = new Array<number>(n);
  tangents[0] = slope[0];
  tangents[n - 1] = slope[n - 2];
  for (let i = 1; i < n - 1; i++) {
    if (slope[i - 1] * slope[i] <= 0) {
      tangents[i] = 0;
    } else {
      tangents[i] = (slope[i - 1] + slope[i]) / 2;
    }
  }

  for (let i = 0; i < n - 1; i++) {
    if (Math.abs(slope[i]) < 1e-9) {
      tangents[i] = 0;
      tangents[i + 1] = 0;
    } else {
      const a = tangents[i] / slope[i];
      const b = tangents[i + 1] / slope[i];
      const mag = Math.hypot(a, b);
      if (mag > 3) {
        const scale = 3 / mag;
        tangents[i] = scale * a * slope[i];
        tangents[i + 1] = scale * b * slope[i];
      }
    }
  }

  const clampY = (v: number) => Math.min(maxY, Math.max(minY, v));
  const cmds: string[] = [`M ${fmtCoord(pts[0].x)} ${fmtCoord(pts[0].y)}`];
  for (let i = 0; i < n - 1; i++) {
    const segDx = dx[i] / 3;
    const cp1x = pts[i].x + segDx;
    const cp1y = clampY(pts[i].y + tangents[i] * segDx);
    const cp2x = pts[i + 1].x - segDx;
    const cp2y = clampY(pts[i + 1].y - tangents[i + 1] * segDx);
    cmds.push(
      `C ${fmtCoord(cp1x)} ${fmtCoord(cp1y)}, ${fmtCoord(cp2x)} ${fmtCoord(cp2y)}, ${fmtCoord(pts[i + 1].x)} ${fmtCoord(pts[i + 1].y)}`,
    );
  }
  return cmds.join(" ");
}

/** Builds crisp pixel-exact SVG geometry for the CU consumption / rate / balance chart. */
export function buildColabOneCuChartLayout(options: {
  points: ColabOneCuUsagePoint[];
  metric: ColabOneCuUsageMetric;
  width: number;
  height: number;
}): ColabOneCuChartLayout {
  const width = Math.max(280, Math.round(options.width || 760));
  const height = Math.max(150, Math.round(options.height || 204));
  const points = options.points;
  const metric = options.metric;
  const count = Math.max(1, points.length);

  const plotTop = 14;
  const plotBottom = height - 28;
  const plotHeight = Math.max(60, plotBottom - plotTop);

  // 1) Resolve Y domain & nice ticks before computing plotLeft so labels never clip
  let yMin = 0;
  let yMax = 1;
  let yStep = 0.25;

  if (metric === "balance") {
    const vals = points
      .filter((p) => !p.isFuture && p.balanceEnd !== null)
      .map((p) => p.balanceEnd as number);
    if (vals.length === 0) {
      yMin = 0;
      yMax = 100;
      yStep = 25;
    } else {
      const minV = Math.min(...vals);
      const maxV = Math.max(...vals);
      const span = maxV - minV;
      if (span < 0.05) {
        const pad = Math.max(1, Math.round(Math.max(maxV * 0.04, 1) * 10) / 10);
        const low = Math.max(0, minV - pad);
        const high = maxV + pad;
        yStep = niceColabOneAxisStep(high - low, 4);
        yMin = Math.max(0, Math.floor(low / yStep) * yStep);
        yMax = Math.ceil(high / yStep) * yStep;
      } else {
        const pad = span * 0.16;
        const low = Math.max(0, minV - pad);
        const high = maxV + pad;
        yStep = niceColabOneAxisStep(high - low, 4);
        yMin = Math.max(0, Math.floor(low / yStep) * yStep);
        yMax = Math.ceil(high / yStep) * yStep;
      }
      if (yMax <= yMin) {
        yMax = yMin + 4;
        yStep = 1;
      }
    }
  } else {
    const rawMax =
      metric === "consumed"
        ? Math.max(0, ...points.map((p) => (p.isFuture ? 0 : p.consumed)))
        : Math.max(
            0,
            ...points.map((p) =>
              p.isFuture ? 0 : Math.max(p.rateAvg, p.rateMax),
            ),
          );
    if (rawMax <= EPSILON) {
      yMax = metric === "rate" ? 4 : 1;
      yStep = metric === "rate" ? 1 : 0.25;
    } else {
      const targetMax = rawMax * 1.15;
      yStep = niceColabOneAxisStep(targetMax, 4);
      yMax = Math.ceil(targetMax / yStep) * yStep;
    }
  }

  // Re-snap if floor+ceil expanded tick count above 6 so ticks stay on clean yStep multiples.
  if (Math.round((yMax - yMin) / yStep) > 6) {
    yStep = niceColabOneAxisStep(yMax - yMin, 4);
    yMin = metric === "balance" ? Math.max(0, Math.floor(yMin / yStep) * yStep) : 0;
    yMax = Math.ceil(yMax / yStep) * yStep;
  }

  const yDomain = Math.max(EPSILON, yMax - yMin);
  const valueToY = (v: number): number => {
    const clamped = Math.min(yMax, Math.max(yMin, v));
    return plotBottom - ((clamped - yMin) / yDomain) * plotHeight;
  };

  const yTicks: ColabOneCuChartTick[] = [];
  const tickSteps = Math.max(1, Math.min(6, Math.round(yDomain / yStep)));
  let maxYLabelLen = 3;
  for (let i = 0; i <= tickSteps; i++) {
    const val = round4(yMin + (yDomain * i) / tickSteps);
    const label = formatColabOneAxisValue(val, yStep);
    if (label.length > maxYLabelLen) {
      maxYLabelLen = label.length;
    }
    yTicks.push({
      value: val,
      pos: Math.round(valueToY(val) * 100) / 100,
      label,
    });
  }

  const plotLeft = Math.max(
    metric === "balance" ? 50 : 44,
    Math.min(76, maxYLabelLen * 7 + 16),
  );
  const plotRight = width - 20;
  const plotWidth = Math.max(100, plotRight - plotLeft);

  // 2) X coordinates
  const stepWidth = plotWidth / count;
  const barRatio = count <= 10 ? 0.54 : count <= 25 ? 0.6 : 0.66;
  const barWidth = Math.max(4, Math.min(34, stepWidth * barRatio));

  const bars: ColabOneCuChartBar[] = [];
  const geomPoints: ColabOneCuChartPointGeom[] = [];
  const primarySplinePts: Array<{ x: number; y: number }> = [];
  const secondarySplinePts: Array<{ x: number; y: number }> = [];

  for (let i = 0; i < points.length; i++) {
    const p = points[i];
    const cx = plotLeft + (i + 0.5) * stepWidth;
    if (metric === "consumed") {
      const val = p.isFuture ? 0 : p.consumed;
      const y = valueToY(val);
      const rawH = Math.max(0, plotBottom - y);
      const barH = val > EPSILON ? Math.max(2, rawH) : 0;
      const barY = plotBottom - barH;
      bars.push({
        index: i,
        x: cx - barWidth / 2,
        y: barY,
        width: barWidth,
        height: barH,
        cx,
        cy: barY,
        value: val,
        isCurrent: p.isCurrent,
        isFuture: p.isFuture,
        hasData: p.hasData,
        topup: p.topup,
      });
      if (!p.isFuture) {
        primarySplinePts.push({ x: cx, y });
        geomPoints.push({
          index: i,
          cx,
          cy: y,
          value: val,
          observed: p.hasData,
          isCurrent: p.isCurrent,
          isFuture: false,
          topup: p.topup,
        });
      }
    } else if (metric === "rate") {
      if (!p.isFuture) {
        const avgVal = p.rateAvg;
        const maxVal = p.rateMax;
        const yAvg = valueToY(avgVal);
        const yMaxVal = valueToY(maxVal);
        primarySplinePts.push({ x: cx, y: yAvg });
        secondarySplinePts.push({ x: cx, y: yMaxVal });
        geomPoints.push({
          index: i,
          cx,
          cy: yAvg,
          value: avgVal,
          observed: p.hasData,
          isCurrent: p.isCurrent,
          isFuture: false,
          topup: p.topup,
        });
      }
    } else {
      if (!p.isFuture && p.balanceEnd !== null) {
        const yBal = valueToY(p.balanceEnd);
        primarySplinePts.push({ x: cx, y: yBal });
        geomPoints.push({
          index: i,
          cx,
          cy: yBal,
          value: p.balanceEnd,
          observed: p.observedBalance,
          isCurrent: p.isCurrent,
          isFuture: false,
          topup: p.topup,
        });
      }
    }
  }

  let linePath = "";
  let areaPath = "";
  let secondaryLinePath = "";
  const balanceSegments: ColabOneCuBalanceSegment[] = [];

  if (metric === "balance") {
    if (geomPoints.length >= 2) {
      let runPts: Array<{ x: number; y: number }> = [
        { x: geomPoints[0].cx, y: geomPoints[0].cy },
      ];
      let runDashed = !geomPoints[0].observed;
      for (let i = 1; i < geomPoints.length; i++) {
        const prev = geomPoints[i - 1];
        const curr = geomPoints[i];
        const segDashed = !(prev.observed && curr.observed);
        if (i === 1) {
          runDashed = segDashed;
          runPts.push({ x: curr.cx, y: curr.cy });
        } else if (segDashed === runDashed) {
          runPts.push({ x: curr.cx, y: curr.cy });
        } else {
          balanceSegments.push({
            path: buildMonotoneSplinePath(runPts, plotTop, plotBottom),
            dashed: runDashed,
          });
          runDashed = segDashed;
          runPts = [
            { x: prev.cx, y: prev.cy },
            { x: curr.cx, y: curr.cy },
          ];
        }
      }
      if (runPts.length >= 2) {
        balanceSegments.push({
          path: buildMonotoneSplinePath(runPts, plotTop, plotBottom),
          dashed: runDashed,
        });
      }
      linePath = buildMonotoneSplinePath(primarySplinePts, plotTop, plotBottom);
      const firstPt = primarySplinePts[0];
      const lastPt = primarySplinePts[primarySplinePts.length - 1];
      areaPath = `${linePath} L ${fmtCoord(lastPt.x)} ${fmtCoord(plotBottom)} L ${fmtCoord(firstPt.x)} ${fmtCoord(plotBottom)} Z`;
    } else if (geomPoints.length === 1) {
      linePath = `M ${fmtCoord(geomPoints[0].cx)} ${fmtCoord(geomPoints[0].cy)}`;
    }
  } else {
    if (primarySplinePts.length >= 2) {
      linePath = buildMonotoneSplinePath(primarySplinePts, plotTop, plotBottom);
      const firstPt = primarySplinePts[0];
      const lastPt = primarySplinePts[primarySplinePts.length - 1];
      areaPath = `${linePath} L ${fmtCoord(lastPt.x)} ${fmtCoord(plotBottom)} L ${fmtCoord(firstPt.x)} ${fmtCoord(plotBottom)} Z`;
    }
    const hasPeakDiff =
      metric === "rate" &&
      points.some((p) => !p.isFuture && p.rateMax - p.rateAvg > 0.05);
    if (hasPeakDiff && secondarySplinePts.length >= 2) {
      secondaryLinePath = buildMonotoneSplinePath(
        secondarySplinePts,
        plotTop,
        plotBottom,
      );
    }
  }

  // 3) Smart X-axis tick thinning based on pixel gap so labels never collide
  const xTicks: ColabOneCuChartXTick[] = [];
  const minLabelGapPx = count <= 7 ? 48 : 56;
  const maxLabels = Math.max(2, Math.floor(plotWidth / minLabelGapPx));
  const stride = count <= maxLabels ? 1 : Math.ceil(count / maxLabels);

  for (let i = 0; i < points.length; i++) {
    if (i === 0 || i % stride === 0) {
      const x = plotLeft + (i + 0.5) * stepWidth;
      const prev = xTicks[xTicks.length - 1];
      if (!prev || x - prev.x >= minLabelGapPx * 0.85) {
        xTicks.push({
          index: i,
          x,
          label: points[i].axisLabel,
        });
      }
    }
  }
  if (points.length > 1) {
    const lastIdx = points.length - 1;
    const lastX = plotLeft + (lastIdx + 0.5) * stepWidth;
    const prev = xTicks[xTicks.length - 1];
    if (!prev || prev.index !== lastIdx) {
      if (prev && xTicks.length > 1 && lastX - prev.x < minLabelGapPx * 0.85) {
        xTicks[xTicks.length - 1] = {
          index: lastIdx,
          x: lastX,
          label: points[lastIdx].axisLabel,
        };
      } else {
        xTicks.push({
          index: lastIdx,
          x: lastX,
          label: points[lastIdx].axisLabel,
        });
      }
    }
  }

  return {
    width,
    height,
    plotLeft,
    plotTop,
    plotRight,
    plotBottom,
    plotWidth,
    plotHeight,
    yMin,
    yMax,
    yTicks,
    xTicks,
    bars,
    points: geomPoints,
    linePath,
    areaPath,
    secondaryLinePath,
    balanceSegments,
    stepWidth,
  };
}

/** Maps a pointer X coordinate relative to the SVG element to the nearest bucket index. */
export function resolveColabOneCuHoverIndex(
  relativeX: number,
  layout: ColabOneCuChartLayout,
  pointCount: number,
): number | null {
  if (pointCount <= 0 || layout.plotWidth <= 0) return null;
  if (relativeX < layout.plotLeft - 8 || relativeX > layout.plotRight + 8) {
    return null;
  }
  const clamped = Math.min(
    layout.plotRight - 0.01,
    Math.max(layout.plotLeft, relativeX),
  );
  const idx = Math.floor((clamped - layout.plotLeft) / layout.stepWidth);
  return Math.max(0, Math.min(pointCount - 1, idx));
}

/** Formats active billing seconds into a concise localized duration string. */
export function formatColabOneActiveDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return t("cuUsage.durationZero");
  }
  const totalMinutes = Math.round(seconds / 60);
  if (totalMinutes < 1) {
    return t("common.secondsShort", { value: Math.max(1, Math.round(seconds)) });
  }
  if (totalMinutes < 60) {
    return t("common.minutes", { value: totalMinutes });
  }
  const hours = Math.floor(totalMinutes / 60);
  const mins = totalMinutes % 60;
  if (hours >= 24) {
    const days = Math.floor(hours / 24);
    const remHours = hours % 24;
    return t("common.daysHours", { days, hours: remHours });
  }
  if (mins === 0) {
    return t("common.hours", { value: hours });
  }
  return t("common.hoursMinutesShort", { hours, minutes: mins });
}

/** Builds a 6x7 localized calendar grid for the CU date picker popover. */
export function buildColabOneCalendarMonth(options: {
  year: number;
  monthIndex: number;
  locale: ColabOneLocale;
  todayDateKey: string;
  selectedDateKey: string;
  availableDates: ReadonlySet<string>;
  activeDates: ReadonlySet<string>;
}): ColabOneCalendarMonth {
  const {
    year,
    monthIndex,
    locale,
    todayDateKey,
    selectedDateKey,
    availableDates,
    activeDates,
  } = options;
  const startOnMonday = locale.startsWith("zh");

  let monthLabel: string;
  try {
    monthLabel = new Intl.DateTimeFormat(locale, {
      year: "numeric",
      month: "long",
    }).format(new Date(year, monthIndex, 1));
  } catch {
    monthLabel = `${year}-${pad2(monthIndex + 1)}`;
  }

  // Build localized short weekday headers using a known reference week (2024-01-01 was Monday)
  const weekdays: string[] = [];
  const baseMondayDay = startOnMonday ? 1 : 7; // 2024-01-01 Mon, 2024-01-07 Sun
  for (let i = 0; i < 7; i++) {
    const d = new Date(2024, 0, baseMondayDay + i);
    try {
      weekdays.push(
        new Intl.DateTimeFormat(locale, { weekday: "narrow" }).format(d),
      );
    } catch {
      weekdays.push(String(i + 1));
    }
  }

  const firstOfMonth = new Date(year, monthIndex, 1);
  const firstDow = firstOfMonth.getDay(); // 0=Sun..6=Sat
  const leadingOffset = startOnMonday ? (firstDow + 6) % 7 : firstDow;

  const cells: ColabOneCalendarCell[] = [];
  for (let i = 0; i < 42; i++) {
    const d = new Date(year, monthIndex, 1 - leadingOffset + i);
    const dateKey = formatColabOneDateKey(d);
    cells.push({
      dateKey,
      day: d.getDate(),
      inCurrentMonth: d.getMonth() === monthIndex && d.getFullYear() === year,
      isToday: dateKey === todayDateKey,
      isSelected: dateKey === selectedDateKey,
      isFuture: dateKey > todayDateKey,
      hasUsage: activeDates.has(dateKey),
      hasRecord: availableDates.has(dateKey),
    });
  }

  const todayParsed = parseColabOneDateKey(todayDateKey);
  const canGoNextMonth = todayParsed
    ? year < todayParsed.year ||
      (year === todayParsed.year && monthIndex < todayParsed.monthIndex)
    : false;

  return {
    year,
    monthIndex,
    monthLabel,
    weekdays,
    cells,
    canGoNextMonth,
  };
}
