import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  COLABONE_HIDDEN_POLL_MS,
  COLABONE_VISIBLE_POLL_MS,
  createColabOneVisibilityPoller,
} from "./colabOnePoller";

describe("ColabOne visibility-aware snapshot poller", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("polls at 600ms while visible, slows to 3000ms while hidden, and snaps immediately on restore", async () => {
    let hidden = false;
    const visibility: { listener: (() => void) | null } = { listener: null };
    const onVisible = vi.fn();
    const tick = vi.fn(async () => {});

    const poller = createColabOneVisibilityPoller({
      visibleMs: COLABONE_VISIBLE_POLL_MS,
      hiddenMs: COLABONE_HIDDEN_POLL_MS,
      tick,
      onVisible,
      isHidden: () => hidden,
      subscribeVisibility: (handler) => {
        visibility.listener = handler;
        return () => {
          visibility.listener = null;
        };
      },
    });

    poller.start();
    await Promise.resolve();
    expect(tick).toHaveBeenCalledTimes(1);

    // Foreground cadence: 600ms
    await vi.advanceTimersByTimeAsync(599);
    expect(tick).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(tick).toHaveBeenCalledTimes(2);

    // Transition to hidden: pending 600ms timer reschedules to 3000ms
    hidden = true;
    visibility.listener?.();

    await vi.advanceTimersByTimeAsync(2999);
    expect(tick).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(tick).toHaveBeenCalledTimes(3);

    // Still hidden: next tick is another 3000ms later
    await vi.advanceTimersByTimeAsync(1500);
    expect(tick).toHaveBeenCalledTimes(3);

    // Transition back to visible: immediate tick + onVisible hook + back to 600ms
    hidden = false;
    visibility.listener?.();
    await Promise.resolve();
    expect(onVisible).toHaveBeenCalledTimes(1);
    expect(tick).toHaveBeenCalledTimes(4);

    await vi.advanceTimersByTimeAsync(600);
    expect(tick).toHaveBeenCalledTimes(5);

    poller.stop();
    expect(visibility.listener).toBeNull();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(tick).toHaveBeenCalledTimes(5);
  });

  it("never spawns overlapping ticks when visibility changes during an in-flight poll", async () => {
    let hidden = true;
    const visibility: { listener: (() => void) | null } = { listener: null };
    const pendingResolvers: Array<() => void> = [];
    const onVisible = vi.fn();
    const tick = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          pendingResolvers.push(resolve);
        }),
    );

    const poller = createColabOneVisibilityPoller({
      visibleMs: 600,
      hiddenMs: 3000,
      tick,
      onVisible,
      isHidden: () => hidden,
      subscribeVisibility: (handler) => {
        visibility.listener = handler;
        return () => {
          visibility.listener = null;
        };
      },
    });

    poller.start();
    expect(tick).toHaveBeenCalledTimes(1);

    // Become visible while first tick is still in flight
    hidden = false;
    visibility.listener?.();
    expect(onVisible).toHaveBeenCalledTimes(1);
    expect(tick).toHaveBeenCalledTimes(1);

    // Complete first tick -> schedules next at foreground 600ms
    pendingResolvers.shift()?.();
    await Promise.resolve();
    await Promise.resolve();

    await vi.advanceTimersByTimeAsync(599);
    expect(tick).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(tick).toHaveBeenCalledTimes(2);

    // Stop while second tick is in flight -> completing it must not schedule another
    poller.stop();
    pendingResolvers.shift()?.();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(5000);
    expect(tick).toHaveBeenCalledTimes(2);
  });

  it("keeps polling cleanly even if a tick throws", async () => {
    let attempts = 0;
    const tick = vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new Error("transient failure");
    });

    const poller = createColabOneVisibilityPoller({
      visibleMs: 600,
      hiddenMs: 3000,
      tick,
      isHidden: () => false,
      subscribeVisibility: () => () => {},
    });

    poller.start();
    await Promise.resolve();
    expect(tick).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(600);
    expect(tick).toHaveBeenCalledTimes(2);

    poller.stop();
  });

  it("restarts cleanly after stop() while an earlier tick promise is still in flight", async () => {
    const pendingResolvers: Array<() => void> = [];
    const visibility: { listener: (() => void) | null } = { listener: null };
    const tick = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          pendingResolvers.push(resolve);
        }),
    );

    const poller = createColabOneVisibilityPoller({
      visibleMs: 600,
      hiddenMs: 3000,
      tick,
      isHidden: () => false,
      subscribeVisibility: (handler) => {
        visibility.listener = handler;
        return () => {
          visibility.listener = null;
        };
      },
    });

    // Session 1: tick #1 starts and stays pending
    poller.start();
    expect(poller.isRunning()).toBe(true);
    expect(tick).toHaveBeenCalledTimes(1);
    const resolveStaleTick1 = pendingResolvers.shift()!;

    // Stop and immediately restart while tick #1 is still pending
    poller.stop();
    expect(poller.isRunning()).toBe(false);
    poller.start();
    expect(poller.isRunning()).toBe(true);

    // Must immediately launch tick #2 instead of deadlocking on stale inFlight
    expect(tick).toHaveBeenCalledTimes(2);
    const resolveActiveTick2 = pendingResolvers.shift()!;

    // Resolving stale tick #1 must NOT clear active tick #2's inFlight flag or schedule prematurely
    resolveStaleTick1();
    await Promise.resolve();
    await Promise.resolve();

    visibility.listener?.();
    await vi.advanceTimersByTimeAsync(1200);
    expect(tick).toHaveBeenCalledTimes(2);

    // Resolving active tick #2 schedules tick #3 at the normal 600ms cadence
    resolveActiveTick2();
    await Promise.resolve();
    await Promise.resolve();

    await vi.advanceTimersByTimeAsync(599);
    expect(tick).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(tick).toHaveBeenCalledTimes(3);

    // Clean up pending tick #3
    poller.stop();
    pendingResolvers.shift()?.();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(5000);
    expect(tick).toHaveBeenCalledTimes(3);
  });

  it("survives a throwing onVisible hook and non-finite interval inputs", async () => {
    let hidden = true;
    let onVisibilityChange: (() => void) | null = null;
    const tick = vi.fn(async () => {});
    const onVisible = vi.fn(() => {
      throw new Error("visibility hook boom");
    });

    const poller = createColabOneVisibilityPoller({
      visibleMs: Number.NaN,
      hiddenMs: Number.POSITIVE_INFINITY,
      tick,
      onVisible,
      isHidden: () => hidden,
      subscribeVisibility: (handler) => {
        onVisibilityChange = handler;
        return () => {
          onVisibilityChange = null;
        };
      },
    });

    poller.start();
    await Promise.resolve();
    expect(tick).toHaveBeenCalledTimes(1);

    // Restoring visibility must invoke onVisible safely and still trigger immediate tick
    hidden = false;
    expect(() => onVisibilityChange?.()).not.toThrow();
    await Promise.resolve();
    expect(onVisible).toHaveBeenCalledTimes(1);
    expect(tick).toHaveBeenCalledTimes(2);

    // Falls back to finite default visible interval (600ms) instead of NaN/0ms spin
    await vi.advanceTimersByTimeAsync(599);
    expect(tick).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(tick).toHaveBeenCalledTimes(3);

    poller.stop();
  });
});
