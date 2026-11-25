import { afterEach, describe, expect, it } from "vitest";
import {
  aggregateColabOneCuSeries,
  buildColabOneCalendarMonth,
  buildColabOneCuChartLayout,
  clampColabOneDateKey,
  formatColabOneActiveDuration,
  formatColabOneDateKey,
  niceColabOneAxisStep,
  parseColabOneDateKey,
  resolveColabOneCuHoverIndex,
  resolveColabOneCuUsageWindow,
  shiftColabOneDateKey,
} from "./cuUsage";
import { setColabOneLocale } from "./i18n";
import type { ColabOneCuUsageResponse } from "./types";

afterEach(() => {
  setColabOneLocale("zh-CN", { persist: false, syncNative: false });
});

describe("ColabOne CU usage date helpers and windows", () => {
  it("parses, formats, shifts, and clamps local YYYY-MM-DD keys safely", () => {
    expect(parseColabOneDateKey("2026-11-23")).toEqual({
      year: 2026,
      monthIndex: 10,
      day: 23,
    });
    expect(parseColabOneDateKey("2026-02-30")).toBeNull();
    expect(parseColabOneDateKey("invalid")).toBeNull();
    expect(shiftColabOneDateKey("2026-03-01", -1)).toBe("2026-02-28");
    expect(shiftColabOneDateKey("2026-11-23", 1)).toBe("2026-11-24");
    expect(clampColabOneDateKey("2026-12-01", "2026-11-23")).toBe("2026-11-23");
    expect(clampColabOneDateKey("2026-11-20", "2026-11-23")).toBe("2026-11-20");
  });

  it("builds a live rolling 24-hour window ending on the current hour", () => {
    const now = new Date(2026, 10, 23, 16, 25, 0, 0).getTime();
    const win = resolveColabOneCuUsageWindow({ mode: "24h", nowMs: now });
    expect(win.granularity).toBe("hour");
    expect(win.isLive).toBe(true);
    expect(win.isAtPresent).toBe(true);
    expect(win.slots).toHaveLength(24);
    expect(win.slots[23].isCurrent).toBe(true);
    expect(win.slots[23].shortLabel).toBe("16:00");
    expect(win.slots[0].shortLabel).toBe("17:00");
    expect(win.queryEndSec - win.queryStartSec).toBe(24 * 3600);
  });

  it("builds a specific calendar day window marking current and future hours accurately", () => {
    const now = new Date(2026, 10, 23, 10, 15, 0, 0).getTime();
    const todayWin = resolveColabOneCuUsageWindow({
      mode: "day",
      selectedDateKey: "2026-11-23",
      nowMs: now,
    });
    expect(todayWin.isLive).toBe(false);
    expect(todayWin.isAtPresent).toBe(true);
    expect(todayWin.slots.length).toBeGreaterThanOrEqual(23);
    expect(todayWin.slots[10].isCurrent).toBe(true);
    expect(todayWin.slots[11].isFuture).toBe(true);
    expect(todayWin.slots[9].isFuture).toBe(false);

    const pastWin = resolveColabOneCuUsageWindow({
      mode: "day",
      selectedDateKey: "2026-11-20",
      nowMs: now,
    });
    expect(pastWin.isAtPresent).toBe(false);
    expect(pastWin.slots.every((s) => !s.isFuture && !s.isCurrent)).toBe(true);
  });

  it("builds 7d and 30d daily windows with exact date keys and future clamping", () => {
    const now = new Date(2026, 10, 23, 12, 0, 0, 0).getTime();
    const win7 = resolveColabOneCuUsageWindow({
      mode: "7d",
      anchorEndDateKey: "2026-11-30", // future date should clamp to today
      nowMs: now,
    });
    expect(win7.slots).toHaveLength(7);
    expect(win7.slots[0].dateKey).toBe("2026-11-17");
    expect(win7.slots[6].dateKey).toBe("2026-11-23");
    expect(win7.slots[6].isCurrent).toBe(true);
    expect(win7.isLive).toBe(true);

    const win30 = resolveColabOneCuUsageWindow({
      mode: "30d",
      anchorEndDateKey: "2026-11-20",
      nowMs: now,
    });
    expect(win30.slots).toHaveLength(30);
    expect(win30.slots[29].dateKey).toBe("2026-11-20");
    expect(win30.isLive).toBe(false);
  });
});

describe("ColabOne CU series aggregation and chart layout", () => {
  it("aggregates 30-minute backend buckets into hourly points with carried balance and summary KPIs", () => {
    const now = new Date(2026, 10, 23, 14, 30, 0, 0).getTime();
    const win = resolveColabOneCuUsageWindow({
      mode: "day",
      selectedDateKey: "2026-11-23",
      nowMs: now,
    });
    const h10 = win.slots[10].startSec;
    const h12 = win.slots[12].startSec;

    const response: ColabOneCuUsageResponse = {
      logged_in: true,
      bucket_seconds: 1800,
      retention_days: 365,
      start: win.queryStartSec,
      end: win.queryEndSec,
      current_balance: 194.2,
      current_rate: 0,
      initial_balance: 100.0,
      available_dates: ["2026-11-23"],
      active_dates: ["2026-11-23"],
      buckets: [
        {
          ts: h10,
          duration: 1800,
          consumed: 1.0,
          topup: 0,
          balance_start: 100.0,
          balance_end: 99.0,
          has_balance: true,
          rate_avg: 2.0,
          rate_max: 2.0,
          active_seconds: 1800,
          max_instances: 1,
          samples: 60,
          estimated: false,
        },
        {
          ts: h10 + 1800,
          duration: 1800,
          consumed: 1.5,
          topup: 0,
          balance_start: 99.0,
          balance_end: 97.5,
          has_balance: true,
          rate_avg: 3.0,
          rate_max: 4.0,
          active_seconds: 1800,
          max_instances: 2,
          samples: 60,
          estimated: false,
        },
        {
          ts: h12,
          duration: 1800,
          consumed: 3.3,
          topup: 100.0,
          balance_start: 97.5,
          balance_end: 194.2,
          has_balance: true,
          rate_avg: 6.6,
          rate_max: 6.6,
          active_seconds: 1800,
          max_instances: 1,
          samples: 30,
          estimated: false,
        },
      ],
    };

    const { points, summary } = aggregateColabOneCuSeries(win, response, now);
    expect(points[10].consumed).toBeCloseTo(2.5, 4);
    expect(points[10].rateAvg).toBeCloseTo(2.5, 4);
    expect(points[10].rateMax).toBeCloseTo(4.0, 4);
    expect(points[10].maxInstances).toBe(2);
    expect(points[10].balanceStart).toBeCloseTo(100.0, 4);
    expect(points[10].balanceEnd).toBeCloseTo(97.5, 4);
    expect(points[10].observedBalance).toBe(true);

    // Hour 11 had no sample -> balance carries forward 97.5 marked unobserved
    expect(points[11].balanceEnd).toBeCloseTo(97.5, 4);
    expect(points[11].observedBalance).toBe(false);

    // Hour 12 includes top-up
    expect(points[12].consumed).toBeCloseTo(3.3, 4);
    expect(points[12].topup).toBeCloseTo(100.0, 4);
    expect(points[12].balanceEnd).toBeCloseTo(194.2, 4);

    // Future hour 16 should NOT carry balance forward
    expect(points[16].isFuture).toBe(true);
    expect(points[16].balanceEnd).toBeNull();

    expect(summary.totalConsumed).toBeCloseTo(5.8, 4);
    expect(summary.totalTopup).toBeCloseTo(100.0, 4);
    expect(summary.totalActiveSeconds).toBe(5400);
    expect(summary.peakConsumed).toBeCloseTo(3.3, 4);
    expect(summary.peakConsumedIndex).toBe(12);
    expect(summary.peakRate).toBeCloseTo(6.6, 4);
    expect(summary.startBalance).toBeCloseTo(100.0, 4);
    expect(summary.endBalance).toBeCloseTo(194.2, 4);
    expect(summary.maxInstances).toBe(2);
  });

  it("builds valid SVG chart layouts for consumed, rate, and balance metrics and maps pointer hover accurately", () => {
    const now = new Date(2026, 10, 23, 16, 10, 0, 0).getTime();
    const win = resolveColabOneCuUsageWindow({ mode: "24h", nowMs: now });
    const resp: ColabOneCuUsageResponse = {
      logged_in: true,
      bucket_seconds: 1800,
      retention_days: 365,
      start: win.queryStartSec,
      end: win.queryEndSec,
      initial_balance: 150,
      current_balance: 146.5,
      available_dates: [formatColabOneDateKey(new Date(now))],
      active_dates: [formatColabOneDateKey(new Date(now))],
      buckets: [
        {
          ts: win.slots[20].startSec,
          duration: 1800,
          consumed: 1.5,
          topup: 0,
          balance_start: 150,
          balance_end: 148.5,
          has_balance: true,
          rate_avg: 3.0,
          rate_max: 5.0,
          active_seconds: 1800,
          max_instances: 1,
          samples: 60,
          estimated: false,
        },
        {
          ts: win.slots[22].startSec,
          duration: 1800,
          consumed: 2.0,
          topup: 0,
          balance_start: 148.5,
          balance_end: 146.5,
          has_balance: true,
          rate_avg: 4.0,
          rate_max: 4.0,
          active_seconds: 1800,
          max_instances: 1,
          samples: 60,
          estimated: false,
        },
      ],
    };
    const { points } = aggregateColabOneCuSeries(win, resp, now);

    const consumedLayout = buildColabOneCuChartLayout({
      points,
      metric: "consumed",
      width: 800,
      height: 210,
    });
    expect(consumedLayout.bars).toHaveLength(24);
    expect(consumedLayout.bars[20].height).toBeGreaterThan(0);
    expect(consumedLayout.bars[0].height).toBe(0);
    expect(consumedLayout.linePath.startsWith("M ")).toBe(true);
    expect(consumedLayout.yTicks.length).toBeGreaterThanOrEqual(2);
    expect(consumedLayout.xTicks.length).toBeGreaterThanOrEqual(3);

    const hoverFirst = resolveColabOneCuHoverIndex(
      consumedLayout.bars[0].cx,
      consumedLayout,
      points.length,
    );
    const hoverLast = resolveColabOneCuHoverIndex(
      consumedLayout.bars[23].cx,
      consumedLayout,
      points.length,
    );
    expect(hoverFirst).toBe(0);
    expect(hoverLast).toBe(23);
    expect(resolveColabOneCuHoverIndex(0, consumedLayout, points.length)).toBeNull();

    const rateLayout = buildColabOneCuChartLayout({
      points,
      metric: "rate",
      width: 800,
      height: 210,
    });
    expect(rateLayout.linePath.startsWith("M ")).toBe(true);
    expect(rateLayout.secondaryLinePath.startsWith("M ")).toBe(true);

    const balanceLayout = buildColabOneCuChartLayout({
      points,
      metric: "balance",
      width: 800,
      height: 210,
    });
    // Auto-zoomed Y-axis around [146.5, 150] should NOT start at 0
    expect(balanceLayout.yMin).toBeGreaterThan(100);
    expect(balanceLayout.balanceSegments.length).toBeGreaterThanOrEqual(2);
    expect(balanceLayout.balanceSegments.some((s) => s.dashed)).toBe(true);
  });

  it("produces nice axis steps, localized durations, and 42-cell localized calendar months", () => {
    expect(niceColabOneAxisStep(3.7, 4)).toBe(1);
    expect(niceColabOneAxisStep(18, 4)).toBe(5);
    expect(formatColabOneActiveDuration(0)).toBe("0 分钟");
    expect(formatColabOneActiveDuration(5400)).toContain("1");

    setColabOneLocale("en-US", { persist: false, syncNative: false });
    expect(formatColabOneActiveDuration(0)).toBe("0 min");

    const cal = buildColabOneCalendarMonth({
      year: 2026,
      monthIndex: 10, // November 2026
      locale: "zh-CN",
      todayDateKey: "2026-11-23",
      selectedDateKey: "2026-11-20",
      availableDates: new Set(["2026-11-19", "2026-11-20"]),
      activeDates: new Set(["2026-11-20"]),
    });
    expect(cal.cells).toHaveLength(42);
    expect(cal.weekdays).toHaveLength(7);
    expect(cal.canGoNextMonth).toBe(false);
    const nov20 = cal.cells.find((c) => c.dateKey === "2026-11-20")!;
    expect(nov20.isSelected).toBe(true);
    expect(nov20.hasUsage).toBe(true);
    expect(nov20.hasRecord).toBe(true);
    const nov24 = cal.cells.find((c) => c.dateKey === "2026-11-24")!;
    expect(nov24.isFuture).toBe(true);
  });

  it("handles offline-prorated zero-active-time KPIs, live current-slot updates, single-point day starts, and narrow-width axis spacing", () => {
    // 1) Offline-prorated consumption with active_seconds=0 must keep activeAvgRate=0 (not fake peakRate)
    const nowDay = new Date(2026, 10, 23, 0, 15, 0, 0).getTime();
    const todayStartWin = resolveColabOneCuUsageWindow({
      mode: "day",
      selectedDateKey: "2026-11-23",
      nowMs: nowDay,
    });
    const offlineResp: ColabOneCuUsageResponse = {
      logged_in: true,
      bucket_seconds: 1800,
      retention_days: 365,
      start: todayStartWin.queryStartSec,
      end: todayStartWin.queryEndSec,
      current_balance: 12345.6,
      current_rate: 5.37,
      checked_at: Math.floor(nowDay / 1000),
      available_dates: ["2026-11-23"],
      active_dates: ["2026-11-23"],
      buckets: [
        {
          ts: todayStartWin.slots[0].startSec,
          duration: 1800,
          consumed: 1.2,
          topup: 0,
          balance_start: 12347.0,
          balance_end: 12345.8,
          has_balance: true,
          rate_avg: 0,
          rate_max: 1.96,
          active_seconds: 0,
          max_instances: 0,
          samples: 0,
          estimated: true,
        },
      ],
    };
    const { points, summary } = aggregateColabOneCuSeries(
      todayStartWin,
      offlineResp,
      nowDay,
    );
    expect(summary.totalActiveSeconds).toBe(0);
    expect(summary.activeAvgRate).toBe(0);
    expect(summary.windowAvgRate).toBeGreaterThan(0);
    // Fresh checked_at updates current slot balanceEnd and higher liveRate updates rateMax
    expect(points[0].balanceEnd).toBeCloseTo(12345.6, 4);
    expect(points[0].rateMax).toBeCloseTo(5.37, 4);

    // 2) Single non-future point at 00:15 AM yields valid rate & balance geometry and expanded plotLeft for 5-digit balance
    const rateSingle = buildColabOneCuChartLayout({
      points,
      metric: "rate",
      width: 320,
      height: 210,
    });
    expect(rateSingle.points).toHaveLength(1);
    expect(rateSingle.points[0].value).toBeGreaterThan(0);

    const balLarge = buildColabOneCuChartLayout({
      points,
      metric: "balance",
      width: 320,
      height: 210,
    });
    expect(balLarge.plotLeft).toBeGreaterThanOrEqual(50);
    // Narrow 320px X-axis ticks must keep sufficient horizontal separation
    for (let i = 1; i < balLarge.xTicks.length; i++) {
      expect(balLarge.xTicks[i].x - balLarge.xTicks[i - 1].x).toBeGreaterThanOrEqual(
        40,
      );
    }
  });

  it("does not inject current_rate into the current slot when logged_in is false or checked_at is stale", () => {
    const nowMs = new Date(2026, 10, 23, 10, 25, 0, 0).getTime();
    const todayWin = resolveColabOneCuUsageWindow({
      mode: "day",
      selectedDateKey: "2026-11-23",
      nowMs,
    });
    const currentSlot = todayWin.slots[10]!;
    expect(currentSlot.isCurrent).toBe(true);

    const baseBucket = {
      ts: currentSlot.startSec,
      duration: 1800,
      consumed: 0.4,
      topup: 0,
      balance_start: 100.0,
      balance_end: 99.6,
      has_balance: true,
      rate_avg: 1.2,
      rate_max: 1.5,
      active_seconds: 600,
      max_instances: 1,
      samples: 5,
      estimated: false,
    };

    // 1) logged_in: false must ignore current_rate & current_balance even with fresh checked_at
    const loggedOutResp: ColabOneCuUsageResponse = {
      logged_in: false,
      bucket_seconds: 1800,
      retention_days: 365,
      start: todayWin.queryStartSec,
      end: todayWin.queryEndSec,
      current_balance: 50.0,
      current_rate: 9.9,
      checked_at: Math.floor(nowMs / 1000),
      available_dates: ["2026-11-23"],
      active_dates: ["2026-11-23"],
      buckets: [baseBucket],
    };
    const loggedOut = aggregateColabOneCuSeries(todayWin, loggedOutResp, nowMs);
    expect(loggedOut.points[10].rateMax).toBeCloseTo(1.5, 4);
    expect(loggedOut.points[10].rateAvg).toBeCloseTo(1.2, 4);
    expect(loggedOut.points[10].balanceEnd).toBeCloseTo(99.6, 4);

    // 2) Stale checked_at (< currentSlot.startSec) must NOT inject current_rate
    const staleResp: ColabOneCuUsageResponse = {
      ...loggedOutResp,
      logged_in: true,
      checked_at: currentSlot.startSec - 120,
    };
    const stale = aggregateColabOneCuSeries(todayWin, staleResp, nowMs);
    expect(stale.points[10].rateMax).toBeCloseTo(1.5, 4);
    expect(stale.points[10].rateAvg).toBeCloseTo(1.2, 4);
    expect(stale.points[10].balanceEnd).toBeCloseTo(99.6, 4);

    // 3) Control: logged_in: true with fresh checked_at applies current_rate & current_balance
    const freshResp: ColabOneCuUsageResponse = {
      ...loggedOutResp,
      logged_in: true,
      checked_at: currentSlot.startSec + 300,
    };
    const fresh = aggregateColabOneCuSeries(todayWin, freshResp, nowMs);
    expect(fresh.points[10].rateMax).toBeCloseTo(9.9, 4);
    expect(fresh.points[10].balanceEnd).toBeCloseTo(50.0, 4);
  });
});
