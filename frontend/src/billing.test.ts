import { describe, expect, it } from "vitest";
import {
  COLABONE_BILLING_COMPACT_RUN_BREAKPOINT,
  COLABONE_BILLING_TICKER_INTERVAL_MS,
  COLABONE_DEFAULT_IDLE_MINUTES,
  COLABONE_IDLE_RELEASE_OPTIONS,
  COLABONE_RELEASE_SELECT_CU_LIMIT,
  COLABONE_RELEASE_SELECT_NEVER,
  COLABONE_RUN_LIVE_INTERP_CAP_SEC,
  describeColabOneBilling,
  describeColabOneRunConsumption,
  encodeColabOneReleaseSelectValue,
  formatColabOneCompactRunDuration,
  formatColabOneCULimitValue,
  formatColabOneRunClock,
  formatCountdownClock,
  formatIdlePolicyLabel,
  formatRemainingBalanceTime,
  friendlyErrorMessage,
  isTransientIpcError,
  nextColabOneBillingTickerSlide,
  parseColabOneReleaseSelectValue,
  resolveColabOneBillingTickerSlides,
} from "./billing";
import { createColabOneSnapshot, type ColabOneSnapshot } from "./types";

describe("COLABONE_DEFAULT_IDLE_MINUTES & COLABONE_IDLE_RELEASE_OPTIONS", () => {
  it("sets default idle auto-release threshold to 30 minutes (half an hour)", () => {
    expect(COLABONE_DEFAULT_IDLE_MINUTES).toBe(30);
    expect(createColabOneSnapshot().settings.idle_minutes).toBe(COLABONE_DEFAULT_IDLE_MINUTES);
  });

  it("exposes preset release options covering 15, 30, 45, 60, and 120 minutes with Chinese labels", () => {
    const values = COLABONE_IDLE_RELEASE_OPTIONS.map((opt) => opt.value);
    expect(values).toEqual([15, 30, 45, 60, 120]);

    const byValue = new Map(
      COLABONE_IDLE_RELEASE_OPTIONS.map((opt) => [opt.value, opt]),
    );
    expect(byValue.get(15)?.shortLabel).toBe("15 分钟");
    expect(byValue.get(30)?.shortLabel).toBe("30 分钟");
    expect(byValue.get(30)?.label).toBe("30 分钟（默认）");
    expect(byValue.get(45)?.shortLabel).toBe("45 分钟");
    expect(byValue.get(60)?.shortLabel).toBe("1 小时");
    expect(byValue.get(120)?.shortLabel).toBe("2 小时");
  });
});

describe("formatRemainingBalanceTime", () => {
  it("calculates exact multi-day + hour endurance for screenshot values (2207.42 CU @ 1.96 CU/h)", () => {
    const res = formatRemainingBalanceTime(2207.42, 1.96);
    expect(res.hours).not.toBeNull();
    expect(res.hours!).toBeCloseTo(2207.42 / 1.96, 6);
    expect(res.daysPart).toBe(46);
    expect(res.hoursPart).toBe(22);
    expect(res.text).toContain("46 天 22 小时");
    expect(res.shortText).toBe("46 天 22 小时");
    expect(res.level).toBe("healthy");
  });

  it("handles exact multi-day boundaries cleanly without rounding artifacts", () => {
    const twoDays = formatRemainingBalanceTime(48, 1);
    expect(twoDays.hours).toBe(48);
    expect(twoDays.daysPart).toBe(2);
    expect(twoDays.hoursPart).toBe(0);
    expect(twoDays.text).toContain("2 天 0 小时");
    expect(twoDays.shortText).toBe("2 天 0 小时");
    expect(twoDays.level).toBe("healthy");

    const oneDay = formatRemainingBalanceTime(24, 1);
    expect(oneDay.daysPart).toBe(1);
    expect(oneDay.hoursPart).toBe(0);
    expect(oneDay.text).toContain("1 天 0 小时");
    expect(oneDay.level).toBe("healthy");
  });

  it("formats sub-day multi-hour endurance and marks < 12h as low", () => {
    const low = formatRemainingBalanceTime(11, 2); // 5.5 hours
    expect(low.hours).toBe(5.5);
    expect(low.daysPart).toBe(0);
    expect(low.hoursPart).toBe(5);
    expect(low.text).toContain("5 小时 30 分钟");
    expect(low.level).toBe("low");

    const exactHours = formatRemainingBalanceTime(15, 1); // 15 hours (>= 12h healthy)
    expect(exactHours.daysPart).toBe(0);
    expect(exactHours.hoursPart).toBe(15);
    expect(exactHours.text).toBe("约 15 小时");
    expect(exactHours.shortText).toBe("15 小时");
    expect(exactHours.level).toBe("healthy");
  });

  it("marks endurance below 2 hours as critical", () => {
    const criticalHours = formatRemainingBalanceTime(1.5, 1.0);
    expect(criticalHours.hours).toBe(1.5);
    expect(criticalHours.daysPart).toBe(0);
    expect(criticalHours.hoursPart).toBe(1);
    expect(criticalHours.text).toContain("1 小时 30 分钟");
    expect(criticalHours.level).toBe("critical");

    const oneHour = formatRemainingBalanceTime(1.0, 1.0);
    expect(oneHour.text).toBe("约 1 小时");
    expect(oneHour.level).toBe("critical");
  });

  it("formats sub-hour endurance in minutes and clamps tiny positive durations to at least 1 minute", () => {
    const halfHour = formatRemainingBalanceTime(0.5, 1.0);
    expect(halfHour.hours).toBe(0.5);
    expect(halfHour.daysPart).toBe(0);
    expect(halfHour.hoursPart).toBe(0);
    expect(halfHour.text).toBe("约 30 分钟");
    expect(halfHour.shortText).toBe("约 30 分钟");
    expect(halfHour.level).toBe("critical");

    const secondsLeft = formatRemainingBalanceTime(0.005, 1.0);
    expect(secondsLeft.text).toBe("约 1 分钟");
    expect(secondsLeft.level).toBe("critical");
  });

  it("reports exhausted balance when balance <= 0 while actively billing", () => {
    const zero = formatRemainingBalanceTime(0, 2.0);
    expect(zero.level).toBe("critical");
    expect(zero.hours).toBe(0);
    expect(zero.daysPart).toBe(0);
    expect(zero.hoursPart).toBe(0);
    expect(zero.text).toContain("耗尽");

    const negative = formatRemainingBalanceTime(-4.25, 1.96);
    expect(negative.level).toBe("critical");
    expect(negative.hours).toBe(0);
    expect(negative.text).toContain("耗尽");
  });

  it("reports idle (not billing) when rate <= 0", () => {
    const idle = formatRemainingBalanceTime(2207.42, 0);
    expect(idle.level).toBe("idle");
    expect(idle.hours).toBeNull();
    expect(idle.daysPart).toBe(0);
    expect(idle.hoursPart).toBe(0);
    expect(idle.text).toContain("未扣费");

    const negRate = formatRemainingBalanceTime(100, -0.5);
    expect(negRate.level).toBe("idle");
    expect(negRate.hours).toBeNull();
  });

  it("returns unknown when balance or rate is undefined or non-finite", () => {
    for (const [balance, rate] of [
      [undefined, 1.96],
      [2207.42, undefined],
      [undefined, undefined],
      [Number.NaN, 1.96],
      [2207.42, Number.POSITIVE_INFINITY],
    ] as const) {
      const unknown = formatRemainingBalanceTime(balance, rate);
      expect(unknown.level).toBe("unknown");
      expect(unknown.hours).toBeNull();
      expect(unknown.daysPart).toBe(0);
      expect(unknown.hoursPart).toBe(0);
    }
  });
});

describe("describeColabOneBilling", () => {
  it("describes a logged-out account accurately", () => {
    const overview = describeColabOneBilling(
      { logged_in: false },
      undefined,
      1_010_000,
    );
    expect(overview.loggedIn).toBe(false);
    expect(overview.active).toBe(false);
    expect(overview.rate).toBe(0);
    expect(overview.instanceCount).toBe(0);
    expect(overview.stale).toBe(true);
    expect(overview.statusText).toBe("未登录");
    expect(overview.rateText).toBe("-- CU/小时");
    expect(overview.balanceText).toBe("未读取");
    expect(overview.endurance.level).toBe("unknown");
  });

  it("describes a logged-in idle account when rate is 0 and no instances are assigned", () => {
    const account: ColabOneSnapshot["account"] = {
      logged_in: true,
      rate: 0,
      balance: 2207.42,
      assignments: 0,
      checked_at: 1000,
    };
    const overview = describeColabOneBilling(account, undefined, 1_010_000);
    expect(overview.loggedIn).toBe(true);
    expect(overview.active).toBe(false);
    expect(overview.stale).toBe(false);
    expect(overview.instanceCount).toBe(0);
    expect(overview.statusText).toBe("当前未扣费");
    expect(overview.rateText).toBe("0.00 CU/小时");
    expect(overview.balanceText).toBe("2207.42 CU");
    expect(overview.endurance.level).toBe("idle");
  });

  it("describes a logged-in actively billing account with remaining days and hours", () => {
    const account: ColabOneSnapshot["account"] = {
      logged_in: true,
      rate: 1.96,
      balance: 2207.42,
      assignments: 1,
      checked_at: 1000,
    };
    const overview = describeColabOneBilling(account, "rt-gpu-1", 1_010_000);
    expect(overview.loggedIn).toBe(true);
    expect(overview.active).toBe(true);
    expect(overview.stale).toBe(false);
    expect(overview.rate).toBe(1.96);
    expect(overview.balance).toBe(2207.42);
    expect(overview.instanceCount).toBe(1);
    expect(overview.statusText).toBe("正在扣费");
    expect(overview.rateText).toBe("1.96 CU/小时");
    expect(overview.balanceText).toBe("2207.42 CU");
    expect(overview.endurance.daysPart).toBe(46);
    expect(overview.endurance.hoursPart).toBe(22);
    expect(overview.endurance.level).toBe("healthy");
  });

  it("flags checked_at timestamps older than 60 seconds or missing as stale", () => {
    const baseAccount: ColabOneSnapshot["account"] = {
      logged_in: true,
      rate: 1.96,
      balance: 100,
      assignments: 1,
      checked_at: 1000,
    };

    // Exact 60s boundary is still fresh
    expect(describeColabOneBilling(baseAccount, "rt-1", 1_060_000).stale).toBe(
      false,
    );
    // > 60s becomes stale
    expect(describeColabOneBilling(baseAccount, "rt-1", 1_060_001).stale).toBe(
      true,
    );
    expect(describeColabOneBilling(baseAccount, "rt-1", 1_120_000).stale).toBe(
      true,
    );
    // Missing checked_at is stale
    expect(
      describeColabOneBilling(
        { logged_in: true, rate: 0, balance: 50 },
        undefined,
        1_010_000,
      ).stale,
    ).toBe(true);
  });

  it("distinguishes pending billing query vs. syncing rate when an instance is running", () => {
    const querying = describeColabOneBilling(
      { logged_in: true },
      undefined,
      1_010_000,
    );
    expect(querying.stale).toBe(true);
    expect(querying.statusText).toBe("计费查询中");

    const syncing = describeColabOneBilling(
      { logged_in: true, rate: 0, balance: 500, checked_at: 1000 },
      "runtime-abc",
      1_010_000,
    );
    expect(syncing.instanceCount).toBe(1);
    expect(syncing.active).toBe(false);
    expect(syncing.statusText).toBe("实例运行 · 费率同步中");
  });

  it("guards negative, NaN, and Infinity billing inputs cleanly without producing NaN displays", () => {
    const malformed = describeColabOneBilling(
      {
        logged_in: true,
        rate: -2.5,
        balance: Number.NaN,
        assignments: Number.POSITIVE_INFINITY,
        checked_at: Number.NaN,
      },
      undefined,
      Number.NaN,
    );
    expect(malformed.rate).toBe(0);
    expect(malformed.active).toBe(false);
    expect(malformed.balance).toBeUndefined();
    expect(malformed.instanceCount).toBe(0);
    expect(malformed.stale).toBe(true);
    expect(malformed.rateText).toBe("-- CU/小时");
    expect(malformed.balanceText).toBe("未读取");
    expect(malformed.endurance.level).toBe("unknown");

    // Negative rate is treated as invalid reading rather than fabricating a negative rate display
    const negRateWithReading = describeColabOneBilling(
      {
        logged_in: true,
        rate: -2.5,
        balance: 50,
        assignments: 0,
        checked_at: 1000,
      },
      undefined,
      1_010_000,
    );
    expect(negRateWithReading.rate).toBe(0);
    expect(negRateWithReading.active).toBe(false);
    expect(negRateWithReading.rateText).toBe("-- CU/小时");

    const badRun = describeColabOneRunConsumption(
      {
        logged_in: true,
        rate: -1.5,
        balance: Number.POSITIVE_INFINITY,
        assignments: Number.NaN,
        checked_at: Number.NaN,
        current_run: {
          active: true,
          started_at: Number.NaN,
          consumed: Number.NaN,
          unsettled: Number.POSITIVE_INFINITY,
          last_sample_at: Number.NaN,
          last_rate: -3,
          peak_rate: Number.NaN,
          max_instances: Number.NaN,
          samples: Number.NaN,
        },
      },
      { setupColab: true, nowMs: Number.NaN },
    );
    expect(Number.isFinite(badRun.consumed)).toBe(true);
    expect(badRun.consumedText).not.toContain("NaN");
    expect(badRun.maxInstances).toBe(0);

    expect(formatIdlePolicyLabel(Number.NaN)).toBe("30 分钟");
    expect(formatIdlePolicyLabel(Number.POSITIVE_INFINITY)).toBe("30 分钟");
  });
});

describe("formatIdlePolicyLabel & formatCountdownClock", () => {
  it("formats preset and custom idle policy durations cleanly", () => {
    expect(formatIdlePolicyLabel(15)).toBe("15 分钟");
    expect(formatIdlePolicyLabel(30)).toBe("30 分钟");
    expect(formatIdlePolicyLabel(45)).toBe("45 分钟");
    expect(formatIdlePolicyLabel(60)).toBe("1 小时");
    expect(formatIdlePolicyLabel(120)).toBe("2 小时");

    // Non-preset custom values
    expect(formatIdlePolicyLabel(20)).toBe("20 分钟");
    expect(formatIdlePolicyLabel(90)).toBe("1 小时 30 分钟");
    expect(formatIdlePolicyLabel(180)).toBe("3 小时");

    // Fallback on invalid or missing input
    expect(formatIdlePolicyLabel(undefined)).toBe("30 分钟");
    expect(formatIdlePolicyLabel(0)).toBe("30 分钟");
    expect(formatIdlePolicyLabel(-10)).toBe("30 分钟");
  });

  it("formats countdown clock values in mm:ss, h:mm:ss, or pending label", () => {
    expect(formatCountdownClock(1800)).toBe("30:00");
    expect(formatCountdownClock(45)).toBe("0:45");
    expect(formatCountdownClock(3661)).toBe("1:01:01");
    expect(formatCountdownClock(3600)).toBe("1:00:00");
    expect(formatCountdownClock(0)).toBe("0:00");
    expect(formatCountdownClock(-5)).toBe("0:00");
    expect(formatCountdownClock(44.2)).toBe("0:45");
    expect(formatCountdownClock(null)).toBe("计算中");
    expect(formatCountdownClock(undefined)).toBe("计算中");
    expect(formatCountdownClock(Number.NaN)).toBe("计算中");
  });
});

describe("isTransientIpcError & friendlyErrorMessage", () => {
  it("detects transient IPC disconnect strings and translates them into friendly Chinese", () => {
    for (const raw of [
      "Connection was lost.",
      "Error: Connection was lost.",
      "Error: Error: connection was lost",
      "WebSocket connection closed unexpectedly",
      "TypeError: Failed to fetch",
      "NetworkError when attempting to fetch resource.",
      "IPC channel closed",
    ]) {
      expect(isTransientIpcError(raw)).toBe(true);
      const friendly = friendlyErrorMessage(raw);
      expect(friendly).not.toContain("Connection was lost");
      expect(friendly).toContain("自动重连");
    }
  });

  it("preserves non-transient user-facing errors and strips raw Error: prefixes cleanly", () => {
    expect(isTransientIpcError("远端磁盘空间不足")).toBe(false);
    expect(friendlyErrorMessage("远端磁盘空间不足")).toBe("远端磁盘空间不足");
    expect(friendlyErrorMessage("Error: 远端磁盘空间不足")).toBe(
      "远端磁盘空间不足",
    );

    expect(isTransientIpcError("")).toBe(false);
    expect(friendlyErrorMessage("")).toBe("");
    expect(friendlyErrorMessage("   ")).toBe("");
    expect(friendlyErrorMessage("Error:   ")).toBe("");
  });
});

describe("describeColabOneRunConsumption", () => {
  it("reports logged_out when unauthenticated and idle_empty when signed in without run history", () => {
    const out = describeColabOneRunConsumption(
      { logged_in: false },
      { setupColab: false, nowMs: 1_000_000 },
    );
    expect(out.phase).toBe("logged_out");
    expect(out.active).toBe(false);
    expect(out.consumedValueText).toBe("--");
    expect(out.consumedText).toBe("-- CU");

    const idle = describeColabOneRunConsumption(
      { logged_in: true, balance: 100, rate: 0, assignments: 0, checked_at: 1000 },
      { setupColab: true, nowMs: 1_010_000 },
    );
    expect(idle.phase).toBe("idle_empty");
    expect(idle.active).toBe(false);
    expect(idle.consumed).toBe(0);
    expect(idle.consumedText).toBe("0.00 CU");
  });

  it("smoothly interpolates live consumption between polls, honors unsettled bridge, and caps runaway projection", () => {
    const account: ColabOneSnapshot["account"] = {
      logged_in: true,
      balance: 98.5,
      rate: 3.6, // 0.001 CU/sec
      assignments: 1,
      checked_at: 2000,
      current_run: {
        active: true,
        started_at: 1000,
        start_balance: 99.5,
        end_balance: 98.5,
        consumed: 1.0,
        unsettled: 0.02,
        last_sample_at: 2000,
        last_rate: 3.6,
        peak_rate: 3.6,
        max_instances: 1,
        samples: 30,
      },
    };

    // 30s after last poll: 1.0 confirmed + 0.02 unsettled + (3.6 * 30 / 3600 = 0.03) = 1.05 CU
    const at30s = describeColabOneRunConsumption(account, {
      setupColab: true,
      runtimeState: "ready",
      runtimeId: "gpu-1",
      nowMs: 2_030_000,
    });
    expect(at30s.phase).toBe("running");
    expect(at30s.active).toBe(true);
    expect(at30s.confirmedConsumed).toBeCloseTo(1.0, 6);
    expect(at30s.liveProjection).toBeCloseTo(0.05, 6);
    expect(at30s.consumed).toBeCloseTo(1.05, 6);
    expect(at30s.consumedText).toBe("1.05 CU");
    expect(at30s.elapsedSeconds).toBe(1030);
    expect(at30s.durationText).toContain("17 分 10 秒");
    expect(at30s.interpolating).toBe(true);
    expect(at30s.avgRate).not.toBeNull();

    // 600s stale gap must cap live interpolation at COLABONE_RUN_LIVE_INTERP_CAP_SEC (90s)
    const stale = describeColabOneRunConsumption(account, {
      setupColab: true,
      nowMs: 2_600_000,
    });
    const expectedMaxProj = 0.02 + (3.6 * COLABONE_RUN_LIVE_INTERP_CAP_SEC) / 3600;
    expect(stale.liveProjection).toBeCloseTo(expectedMaxProj, 6);
  });

  it("never extrapolates beyond remaining paid balance when balance is depleted", () => {
    const depleted: ColabOneSnapshot["account"] = {
      logged_in: true,
      balance: 0,
      rate: 3.6,
      assignments: 1,
      checked_at: 2000,
      current_run: {
        active: true,
        started_at: 1000,
        consumed: 2.5,
        unsettled: 0.05,
        last_sample_at: 2000,
        last_rate: 3.6,
      },
    };
    const res = describeColabOneRunConsumption(depleted, {
      setupColab: true,
      nowMs: 2_045_000,
    });
    expect(res.liveProjection).toBe(0);
    expect(res.consumed).toBe(2.5);
    expect(res.consumedText).toBe("2.50 CU");
    expect(res.balanceDepleted).toBe(true);
  });

  it("freezes stale offline persisted active runs and honors fresh idle polls instead of fabricating live burn", () => {
    const staleOffline: ColabOneSnapshot["account"] = {
      logged_in: false,
      current_run: {
        active: true,
        started_at: 1000,
        last_sample_at: 4600,
        last_rate: 2.0,
        consumed: 2.0,
        unsettled: 0.05,
      },
    };
    // Reopened 2 hours later while offline (setupColab=true, logged_in=false, no checked_at)
    const offlineRes = describeColabOneRunConsumption(staleOffline, {
      setupColab: true,
      runtimeState: "stopped",
      nowMs: 11_800_000,
    });
    expect(offlineRes.phase).toBe("completed");
    expect(offlineRes.active).toBe(false);
    expect(offlineRes.liveProjection).toBe(0);
    expect(offlineRes.consumed).toBe(2.0);
    expect(offlineRes.elapsedSeconds).toBe(3600);

    // Fresh account poll confirms idle after last_sample_at even if current_run.active was briefly true
    const confirmedIdle = describeColabOneRunConsumption(
      {
        logged_in: true,
        balance: 98.0,
        rate: 0,
        assignments: 0,
        checked_at: 4650,
        current_run: {
          active: true,
          started_at: 1000,
          last_sample_at: 4600,
          last_rate: 2.0,
          consumed: 2.0,
          unsettled: 0.04,
        },
      },
      { setupColab: true, runtimeState: "stopped", nowMs: 4_660_000 },
    );
    expect(confirmedIdle.phase).toBe("completed");
    expect(confirmedIdle.active).toBe(false);
    expect(confirmedIdle.liveProjection).toBe(0);
    expect(confirmedIdle.consumed).toBe(2.0);
  });

  it("preserves completed last-run totals with frozen duration once idle", () => {
    const completed: ColabOneSnapshot["account"] = {
      logged_in: true,
      balance: 95.48,
      rate: 0,
      assignments: 0,
      checked_at: 9000,
      current_run: {
        active: false,
        started_at: 1000,
        ended_at: 8200, // exactly 2 hours
        start_balance: 100.0,
        end_balance: 95.48,
        consumed: 4.52,
        topup: 0,
        last_sample_at: 8200,
        peak_rate: 2.5,
        max_instances: 1,
        samples: 240,
      },
    };
    const res = describeColabOneRunConsumption(completed, {
      setupColab: true,
      nowMs: 12_000_000,
    });
    expect(res.phase).toBe("completed");
    expect(res.active).toBe(false);
    expect(res.hasRecord).toBe(true);
    expect(res.consumed).toBe(4.52);
    expect(res.consumedText).toBe("4.52 CU");
    expect(res.elapsedSeconds).toBe(7200);
    expect(res.durationText).toBe("2 小时");
    expect(res.avgRate).toBeCloseTo(2.26, 4);
    expect(res.interpolating).toBe(false);
  });

  it("handles active instance gracefully when current_run is not yet present", () => {
    const fallback = describeColabOneRunConsumption(
      { logged_in: true, balance: 50, rate: 0, assignments: 1, checked_at: 1000 },
      { setupColab: true, runtimeState: "ready", runtimeId: "gpu-1", nowMs: 1_010_000 },
    );
    expect(fallback.phase).toBe("running");
    expect(fallback.active).toBe(true);
    expect(fallback.hasRecord).toBe(false);
    expect(fallback.syncingRate).toBe(true);
    expect(fallback.consumedText).toBe("0.00 CU");
  });
});

describe("COLABONE_BILLING_TICKER_INTERVAL_MS, formatColabOneCompactRunDuration & formatColabOneRunClock", () => {
  it("rotates the topbar billing ticker every 3 seconds", () => {
    expect(COLABONE_BILLING_TICKER_INTERVAL_MS).toBe(3000);
    expect(COLABONE_BILLING_COMPACT_RUN_BREAKPOINT).toBe(1080);
  });

  it("includes run consumption in the billing ticker whenever the standalone run column is hidden on narrow screens", () => {
    // Wide desktop (> 1080px): standalone run column is visible, ticker rotates rate and duration only
    expect(
      resolveColabOneBillingTickerSlides({
        rateLoading: false,
        runLoading: false,
        balanceLoading: false,
        runActive: true,
        hasCompactDuration: true,
        inlineRunHidden: false,
      }),
    ).toEqual(["rate", "duration"]);

    // Narrow viewport (<= 1080px, including mobile): standalone run column is hidden, ticker rotates all 3 items
    const narrowSlides = resolveColabOneBillingTickerSlides({
      rateLoading: false,
      runLoading: false,
      balanceLoading: false,
      runActive: true,
      hasCompactDuration: true,
      inlineRunHidden: true,
    });
    expect(narrowSlides).toEqual(["rate", "consumed", "duration"]);

    // Cycles cleanly through rate, consumed, duration, and back to rate
    expect(nextColabOneBillingTickerSlide(narrowSlides, "rate")).toBe("consumed");
    expect(nextColabOneBillingTickerSlide(narrowSlides, "consumed")).toBe("duration");
    expect(nextColabOneBillingTickerSlide(narrowSlides, "duration")).toBe("rate");

    // Still rotates rate + consumed on narrow screens even before duration is available
    expect(
      resolveColabOneBillingTickerSlides({
        rateLoading: false,
        runLoading: false,
        balanceLoading: false,
        runActive: true,
        hasCompactDuration: false,
        inlineRunHidden: true,
      }),
    ).toEqual(["rate", "consumed"]);

    // Suppresses consumed slide while balance/run skeleton is still loading
    expect(
      resolveColabOneBillingTickerSlides({
        rateLoading: false,
        runLoading: true,
        balanceLoading: false,
        runActive: true,
        hasCompactDuration: true,
        inlineRunHidden: true,
      }),
    ).toEqual(["rate", "duration"]);

    // Idle or loading states do not rotate
    expect(
      resolveColabOneBillingTickerSlides({
        rateLoading: false,
        runLoading: false,
        balanceLoading: false,
        runActive: false,
        hasCompactDuration: false,
        inlineRunHidden: true,
      }),
    ).toEqual(["rate"]);
    expect(
      resolveColabOneBillingTickerSlides({
        rateLoading: true,
        runLoading: false,
        balanceLoading: false,
        runActive: true,
        hasCompactDuration: true,
        inlineRunHidden: true,
      }),
    ).toEqual([]);
  });

  it("formats compact topbar run durations with zero-padded sub-units for stable ticker width", () => {
    expect(formatColabOneCompactRunDuration(0, "zh-CN")).toBe("0 秒");
    expect(formatColabOneCompactRunDuration(42.9, "zh-CN")).toBe("42 秒");
    expect(formatColabOneCompactRunDuration(60, "zh-CN")).toBe("1 分 00 秒");
    expect(formatColabOneCompactRunDuration(754, "zh-CN")).toBe("12 分 34 秒");
    expect(formatColabOneCompactRunDuration(7500, "zh-CN")).toBe("2 小时 05 分");
    expect(formatColabOneCompactRunDuration(7503, "zh-CN")).toBe("2 小时 05 分");
    expect(formatColabOneCompactRunDuration(93600, "zh-CN")).toBe("1 天 2 小时");

    expect(formatColabOneCompactRunDuration(65, "en-US")).toBe("1m 05s");
    expect(formatColabOneCompactRunDuration(7503, "en-US")).toBe("2h 05m");
    expect(formatColabOneCompactRunDuration(93600, "en-US")).toBe("1d 2h");

    expect(formatColabOneCompactRunDuration(null)).toBe("");
    expect(formatColabOneCompactRunDuration(undefined)).toBe("");
    expect(formatColabOneCompactRunDuration(Number.NaN)).toBe("");
  });

  it("formats same-day timestamps as HH:MM:SS and prior-day timestamps with date + time", () => {
    const now = new Date(2026, 10, 25, 14, 30, 0).getTime();
    const sameDayMidnight = new Date(2026, 10, 25, 0, 5, 9).getTime() / 1000;
    const sameDayAfternoon = new Date(2026, 10, 25, 13, 45, 7).getTime() / 1000;
    const prevDay = new Date(2026, 10, 24, 22, 15, 0).getTime() / 1000;

    expect(formatColabOneRunClock(sameDayMidnight, now, "zh-CN")).toBe(
      "00:05:09",
    );
    expect(formatColabOneRunClock(sameDayAfternoon, now, "zh-CN")).toBe(
      "13:45:07",
    );
    const prevFormatted = formatColabOneRunClock(prevDay, now, "zh-CN");
    expect(prevFormatted).toContain("11");
    expect(prevFormatted).toContain("24");
    expect(prevFormatted).toContain("22:15");

    expect(formatColabOneRunClock(undefined, now)).toBe("");
    expect(formatColabOneRunClock(null, now)).toBe("");
    expect(formatColabOneRunClock(0, now)).toBe("");
    expect(formatColabOneRunClock(-10, now)).toBe("");
    expect(formatColabOneRunClock(Number.NaN, now)).toBe("");
  });

  it("guards overflowed endurance division and clamps near-zero negative balance formatting", () => {
    const overflowed = formatRemainingBalanceTime(Number.MAX_VALUE, 1e-308);
    expect(overflowed.level).toBe("unknown");
    expect(overflowed.hours).toBeNull();

    const baseAccount = createColabOneSnapshot().account;
    const nearZeroNeg = describeColabOneBilling({
      ...baseAccount,
      logged_in: true,
      balance: -0.001,
      rate: 0,
      checked_at: Math.floor(Date.now() / 1000),
    });
    expect(nearZeroNeg.balanceText).toBe("0.00 CU");

    const negZero = describeColabOneBilling({
      ...baseAccount,
      logged_in: true,
      balance: -0,
      rate: 0,
      checked_at: Math.floor(Date.now() / 1000),
    });
    expect(negZero.balanceText).toBe("0.00 CU");

    const realNeg = describeColabOneBilling({
      ...baseAccount,
      logged_in: true,
      balance: -1.25,
      rate: 0,
      checked_at: Math.floor(Date.now() / 1000),
    });
    expect(realNeg.balanceText).toBe("-1.25 CU");
  });

  it("encodes, parses, and formats never and cu_limit auto-release policies cleanly", () => {
    expect(formatColabOneCULimitValue(10)).toBe("10");
    expect(formatColabOneCULimitValue(12.5)).toBe("12.5");
    expect(formatColabOneCULimitValue(12.346)).toBe("12.35");
    expect(formatColabOneCULimitValue(Number.NaN)).toBe("10");

    expect(encodeColabOneReleaseSelectValue("idle", 30)).toBe("idle:30");
    expect(encodeColabOneReleaseSelectValue("never", 30)).toBe(
      COLABONE_RELEASE_SELECT_NEVER,
    );
    expect(encodeColabOneReleaseSelectValue("cu_limit", 30)).toBe(
      COLABONE_RELEASE_SELECT_CU_LIMIT,
    );

    expect(parseColabOneReleaseSelectValue("idle:45")).toEqual({
      mode: "idle",
      idleMinutes: 45,
    });
    expect(parseColabOneReleaseSelectValue("45")).toEqual({
      mode: "idle",
      idleMinutes: 45,
    });
    expect(parseColabOneReleaseSelectValue(COLABONE_RELEASE_SELECT_NEVER)).toEqual({
      mode: "never",
    });
    expect(
      parseColabOneReleaseSelectValue(COLABONE_RELEASE_SELECT_CU_LIMIT),
    ).toEqual({
      mode: "cu_limit",
    });
    expect(parseColabOneReleaseSelectValue("bad")).toBeNull();

    expect(formatIdlePolicyLabel(30, "never", 10)).toBe("长期不释放");
    expect(formatIdlePolicyLabel(30, "cu_limit", 12.5)).toBe("消耗达 12.5 CU 释放");
    expect(formatIdlePolicyLabel(60, "idle", 10)).toBe("1 小时");
  });
});
