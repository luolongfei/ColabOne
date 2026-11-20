import { describe, expect, it } from "vitest";
import {
  COLABONE_DEFAULT_IDLE_MINUTES,
  COLABONE_IDLE_RELEASE_OPTIONS,
  describeColabOneBilling,
  formatCountdownClock,
  formatIdlePolicyLabel,
  formatRemainingBalanceTime,
  friendlyErrorMessage,
  isTransientIpcError,
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
