import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  computeColabOneToastDuration,
  createColabOneToastManager,
  isPersistentColabOneError,
  resolveColabOneToastText,
} from "./colabOneToast";

describe("ColabOne toast duration & classification", () => {
  it("classifies persistent connectivity errors separately from one-shot action errors", () => {
    expect(isPersistentColabOneError("Failed to fetch")).toBe(true);
    expect(isPersistentColabOneError("WebSocket connection was lost")).toBe(
      true,
    );
    expect(
      isPersistentColabOneError(
        "连接检查失败：ConnectionError。客户端会自动重试，请检查网络连接。",
      ),
    ).toBe(true);
    expect(isPersistentColabOneError("mygo: runtime not found")).toBe(true);

    expect(
      isPersistentColabOneError(
        "API 配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token",
      ),
    ).toBe(false);
    expect(isPersistentColabOneError("Error: 请先完成 Google 登录")).toBe(
      false,
    );
    expect(isPersistentColabOneError("服务返回 HTTP 403，请检查授权和配置")).toBe(
      false,
    );
    expect(isPersistentColabOneError("")).toBe(false);
  });

  it("scales reading duration by severity and message length within safe bounds", () => {
    const shortSuccess = computeColabOneToastDuration("success", "已复制");
    const shortError = computeColabOneToastDuration("error", "请先登录");
    const longApiError = computeColabOneToastDuration(
      "error",
      "API 配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token",
    );
    const hugeError = computeColabOneToastDuration("error", "x".repeat(500));

    expect(shortSuccess).toBe(3200);
    expect(shortError).toBe(6000);
    expect(longApiError).toBeGreaterThan(shortError);
    expect(longApiError).toBeLessThan(10000);
    expect(hugeError).toBe(10000);
  });
});

describe("ColabOne toast manager lifecycle", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-20T12:00:00Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("auto-dismisses after duration and invokes onDismiss once", () => {
    const manager = createColabOneToastManager({
      now: () => Date.now(),
    });
    const onDismiss = vi.fn();

    const id = manager.push({
      kind: "error",
      message: "API 配置无效",
      durationMs: 6500,
      onDismiss,
    });
    expect(id).not.toBeNull();
    expect(manager.items.value).toHaveLength(1);

    vi.advanceTimersByTime(6499);
    expect(manager.items.value).toHaveLength(1);
    expect(onDismiss).not.toHaveBeenCalled();

    vi.advanceTimersByTime(1);
    expect(manager.items.value).toHaveLength(0);
    expect(onDismiss).toHaveBeenCalledTimes(1);

    // Extra dismiss calls are idempotent
    manager.dismiss(id!);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("pauses countdown on hover/focus and resumes with exact remaining time", () => {
    const manager = createColabOneToastManager({
      now: () => Date.now(),
    });
    const id = manager.push({
      kind: "error",
      message: "请填写固定域名",
      durationMs: 6000,
    })!;

    vi.advanceTimersByTime(2200);
    manager.pause(id);
    expect(manager.items.value[0].paused).toBe(true);
    expect(manager.items.value[0].remainingMs).toBe(3800);

    // Stays visible indefinitely while paused
    vi.advanceTimersByTime(30000);
    expect(manager.items.value).toHaveLength(1);

    manager.resume(id);
    expect(manager.items.value[0].paused).toBe(false);

    vi.advanceTimersByTime(3799);
    expect(manager.items.value).toHaveLength(1);

    vi.advanceTimersByTime(1);
    expect(manager.items.value).toHaveLength(0);
  });

  it("deduplicates matching keys by bumping revision and resetting countdown", () => {
    const manager = createColabOneToastManager({
      now: () => Date.now(),
    });
    const firstId = manager.push({
      kind: "error",
      message: "API 配置无效",
      dedupeKey: "api-invalid",
      durationMs: 6000,
    })!;

    vi.advanceTimersByTime(4000);
    const secondId = manager.push({
      kind: "error",
      message: "API 配置无效",
      dedupeKey: "api-invalid",
      durationMs: 6000,
    })!;

    expect(secondId).toBe(firstId);
    expect(manager.items.value).toHaveLength(1);
    expect(manager.items.value[0].revision).toBe(2);

    // Original timer would have expired at +2000ms; refreshed timer lasts full 6000ms
    vi.advanceTimersByTime(5999);
    expect(manager.items.value).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(manager.items.value).toHaveLength(0);
  });

  it("respects maxVisible while preserving paused toasts and supports group dismissal", () => {
    const manager = createColabOneToastManager({
      maxVisible: 2,
      now: () => Date.now(),
    });
    const id1 = manager.push({
      kind: "error",
      message: "第一个错误",
      group: "state-error",
      durationMs: 8000,
    })!;
    manager.pause(id1);

    const id2 = manager.push({
      kind: "error",
      message: "第二个错误",
      group: "action-error",
      durationMs: 8000,
    })!;

    const id3 = manager.push({
      kind: "success",
      message: "第三个提示",
      durationMs: 4000,
    })!;

    // id1 is paused so id2 (oldest non-paused) was evicted
    expect(manager.items.value.map((item) => item.id)).toEqual([id1, id3]);
    expect(id2).toBeGreaterThan(0);

    manager.dismissGroup("state-error");
    expect(manager.items.value.map((item) => item.id)).toEqual([id3]);
  });

  it("keeps a paused duplicate toast paused on refresh and resumes for full refreshed duration", () => {
    const manager = createColabOneToastManager({
      now: () => Date.now(),
    });
    const id = manager.push({
      kind: "error",
      message: "API 配置无效",
      dedupeKey: "api-invalid",
      durationMs: 6000,
    })!;

    vi.advanceTimersByTime(3500);
    manager.pause(id);
    expect(manager.items.value[0].remainingMs).toBe(2500);

    // Duplicate error fires while user is hovering the toast
    manager.push({
      kind: "error",
      message: "API 配置无效",
      dedupeKey: "api-invalid",
      durationMs: 6000,
    });
    expect(manager.items.value[0].paused).toBe(true);
    expect(manager.items.value[0].revision).toBe(2);
    expect(manager.items.value[0].remainingMs).toBe(6000);

    // Must not expire while still paused
    vi.advanceTimersByTime(15000);
    expect(manager.items.value).toHaveLength(1);

    // Resuming runs the full refreshed 6000ms window
    manager.resume(id);
    vi.advanceTimersByTime(5999);
    expect(manager.items.value).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(manager.items.value).toHaveLength(0);
  });

  it("ignores blank messages safely without inserting empty toasts", () => {
    const manager = createColabOneToastManager({
      now: () => Date.now(),
    });
    expect(manager.push({ kind: "error", message: "   " })).toBeNull();
    expect(
      manager.push({ kind: "info", resolveText: () => "\n\t " }),
    ).toBeNull();
    expect(manager.items.value).toHaveLength(0);
  });

  it("resolves live reactive text getters for dynamic locale switching", () => {
    const manager = createColabOneToastManager({
      now: () => Date.now(),
    });
    let lang = "en";
    manager.push({
      kind: "error",
      dedupeKey: "live-err",
      resolveText: () => (lang === "en" ? "Sign in first" : "请先登录"),
      durationMs: 6000,
    });

    expect(resolveColabOneToastText(manager.items.value[0])).toBe(
      "Sign in first",
    );
    lang = "zh";
    expect(resolveColabOneToastText(manager.items.value[0])).toBe("请先登录");
  });
});
