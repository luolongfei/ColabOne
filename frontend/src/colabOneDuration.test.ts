import { afterEach, describe, expect, it } from "vitest";
import {
  formatColabOneDuration,
  resolveColabOneTaskElapsedSeconds,
} from "./colabOneDuration";
import { setColabOneLocale, type ColabOneLocale } from "./i18n";
import type { TaskStatus } from "./types";

const ALL_LOCALES: ColabOneLocale[] = [
  "zh-CN",
  "en-US",
  "zh-TW",
  "zh-HK",
  "ja-JP",
  "ko-KR",
];

afterEach(() => {
  setColabOneLocale("zh-CN", { persist: false, syncNative: false });
});

describe("formatColabOneDuration", () => {
  it("formats seconds, minutes, and hours cleanly across all 6 locales", () => {
    expect(formatColabOneDuration(0, { locale: "zh-CN" })).toBe("0 秒");
    expect(formatColabOneDuration(42.9, { locale: "zh-CN" })).toBe("42 秒");
    expect(formatColabOneDuration(60, { locale: "zh-CN" })).toBe("1 分钟");
    expect(formatColabOneDuration(754, { locale: "zh-CN" })).toBe("12 分 34 秒");
    expect(formatColabOneDuration(3600, { locale: "zh-CN" })).toBe("1 小时");
    expect(formatColabOneDuration(4500, { locale: "zh-CN" })).toBe("1 小时 15 分");
    expect(formatColabOneDuration(4523, { locale: "zh-CN" })).toBe(
      "1 小时 15 分 23 秒",
    );
    expect(formatColabOneDuration(3605, { locale: "zh-CN" })).toBe(
      "1 小时 0 分 5 秒",
    );

    // English
    expect(formatColabOneDuration(42, { locale: "en-US" })).toBe("42s");
    expect(formatColabOneDuration(60, { locale: "en-US" })).toBe("1 min");
    expect(formatColabOneDuration(754, { locale: "en-US" })).toBe("12m 34s");
    expect(formatColabOneDuration(3600, { locale: "en-US" })).toBe("1 hr");
    expect(formatColabOneDuration(4500, { locale: "en-US" })).toBe("1h 15m");
    expect(formatColabOneDuration(4523, { locale: "en-US" })).toBe("1h 15m 23s");

    // Traditional Chinese (TW/HK), Japanese, Korean
    expect(formatColabOneDuration(4523, { locale: "zh-TW" })).toBe(
      "1 小時 15 分 23 秒",
    );
    expect(formatColabOneDuration(4523, { locale: "zh-HK" })).toBe(
      "1 小時 15 分 23 秒",
    );
    expect(formatColabOneDuration(4523, { locale: "ja-JP" })).toBe(
      "1 時間 15 分 23 秒",
    );
    expect(formatColabOneDuration(4523, { locale: "ko-KR" })).toBe(
      "1시간 15분 23초",
    );
  });

  it("supports auto sub-second precision below 60s and promotes 59.96s cleanly to 1 minute", () => {
    expect(
      formatColabOneDuration(4.24, { precision: "auto", locale: "zh-CN" }),
    ).toBe("4.2 秒");
    expect(
      formatColabOneDuration(4.24, { precision: "auto", locale: "en-US" }),
    ).toBe("4.2s");
    expect(
      formatColabOneDuration(59.96, { precision: "auto", locale: "zh-CN" }),
    ).toBe("1 分钟");
    expect(
      formatColabOneDuration(59.96, { precision: "auto", locale: "en-US" }),
    ).toBe("1 min");
    expect(
      formatColabOneDuration(4523.4, { precision: "auto", locale: "zh-CN" }),
    ).toBe("1 小时 15 分 23 秒");
    expect(
      formatColabOneDuration(4523.6, { precision: "auto", locale: "zh-CN" }),
    ).toBe("1 小时 15 分 24 秒");
  });

  it("handles invalid or negative inputs safely across all locales", () => {
    expect(formatColabOneDuration(null)).toBe("");
    expect(formatColabOneDuration(undefined)).toBe("");
    expect(formatColabOneDuration(Number.NaN)).toBe("");
    expect(formatColabOneDuration(Number.POSITIVE_INFINITY)).toBe("");
    expect(formatColabOneDuration(-15, { locale: "zh-CN" })).toBe("0 秒");
    for (const loc of ALL_LOCALES) {
      expect(formatColabOneDuration(754, { locale: loc }).length).toBeGreaterThan(
        0,
      );
      expect(
        formatColabOneDuration(4523, { locale: loc }).length,
      ).toBeGreaterThan(0);
    }
  });
});

describe("resolveColabOneTaskElapsedSeconds", () => {
  function makeTask(overrides: Partial<TaskStatus>): TaskStatus {
    return {
      id: "task-default",
      kind: "comfyui_run",
      model_id: "digital-human-longcat",
      state: "running",
      progress: 0.5,
      message: "",
      error: "",
      ...overrides,
    };
  }

  it("resolves live elapsed seconds from modern elapsed_seconds without clock skew", () => {
    const task = makeTask({
      id: "task-1",
      progress: 0.6,
      created_at: 1_700_000_000,
      elapsed_seconds: 1234.5,
    });
    // Even if local wall clock is wildly skewed relative to created_at, only (nowMs - snapshotAtMs) is added.
    const resolved = resolveColabOneTaskElapsedSeconds(
      task,
      { idle: { server_time: 1_700_001_234.5 } as any },
      1_800_000_005_000,
      1_800_000_000_000,
    );
    expect(resolved).toBeCloseTo(1239.5, 4);
  });

  it("resolves live elapsed seconds on already-running older remote servers via idle.server_time - created_at", () => {
    const legacyTask = makeTask({
      id: "task-legacy",
      created_at: 1_700_000_000,
    });
    const resolved = resolveColabOneTaskElapsedSeconds(
      legacyTask,
      { idle: { server_time: 1_700_003_720 } as any },
      50_000,
      47_000,
    );
    expect(resolved).toBeCloseTo(3723, 4);
  });

  it("falls back to local clock delta or download elapsed_seconds when server_time is unavailable", () => {
    const createdTask = makeTask({
      id: "task-local-fallback",
      progress: 0.2,
      created_at: 1_700_000_000,
    });
    expect(
      resolveColabOneTaskElapsedSeconds(
        createdTask,
        {},
        1_700_000_120_000,
        1_700_000_120_000,
      ),
    ).toBeCloseTo(120, 4);

    const downloadTask = makeTask({
      id: "task-dl",
      kind: "download",
      progress: 0.4,
      download: {
        downloaded_bytes: 512,
        total_bytes: 1024,
        speed_bps: 100,
        eta_seconds: 5,
        elapsed_seconds: 88.2,
        resumed_bytes: 0,
        threads: 4,
        connections: 4,
        phase: "downloading",
        filename: "model.safetensors",
        file_index: 1,
        file_count: 1,
      },
    });
    expect(
      resolveColabOneTaskElapsedSeconds(downloadTask, {}, 20_000, 18_000),
    ).toBeCloseTo(90.2, 4);
  });

  it("freezes elapsed seconds for terminal tasks using elapsed_seconds or finished_at - created_at", () => {
    const doneModern = makeTask({
      id: "task-done-1",
      state: "completed",
      progress: 1,
      created_at: 1_700_000_000,
      finished_at: 1_700_001_000,
      elapsed_seconds: 999.4,
    });
    expect(
      resolveColabOneTaskElapsedSeconds(doneModern, {}, 9_999_999_000, 1_000),
    ).toBeCloseTo(999.4, 4);

    const doneFinishedAt = makeTask({
      id: "task-done-2",
      state: "completed",
      progress: 1,
      created_at: 1_700_000_000,
      finished_at: 1_700_000_425,
    });
    expect(
      resolveColabOneTaskElapsedSeconds(doneFinishedAt, {}, 9_999_999_000, 1_000),
    ).toBeCloseTo(425, 4);

    const doneLegacyNoTiming = makeTask({
      id: "task-done-3",
      state: "completed",
      progress: 1,
      created_at: 1_700_000_000,
    });
    expect(
      resolveColabOneTaskElapsedSeconds(doneLegacyNoTiming, {}, 9_999_999_000, 1_000),
    ).toBeNull();
  });
});
