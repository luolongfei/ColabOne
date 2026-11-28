import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  COLABONE_TOOLTIP_LONG_PRESS_AUTO_DISMISS_MS,
  COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS,
  COLABONE_TOOLTIP_MOUSE_RECOVERY_GAP_MS,
  createColabOneTouchLongPressController,
  ensureColabOneTooltipInputTracker,
  getColabOneTooltipInputModality,
  isColabOneTouchInputActive,
  resetColabOneTooltipInputTrackerForTest,
  shouldSuppressColabOneHoverTooltip,
} from "./colabOneTooltipInput";

type Listener = (event: any) => void;

class FakeWindowTarget {
  private listeners = new Map<string, Set<Listener>>();

  addEventListener(type: string, fn: Listener) {
    let set = this.listeners.get(type);
    if (!set) {
      set = new Set();
      this.listeners.set(type, set);
    }
    set.add(fn);
  }

  removeEventListener(type: string, fn: Listener) {
    this.listeners.get(type)?.delete(fn);
  }

  dispatch(type: string, event: any = {}) {
    const set = this.listeners.get(type);
    if (!set) return;
    for (const fn of Array.from(set)) {
      fn(event);
    }
  }

  listenerCount(type: string): number {
    return this.listeners.get(type)?.size ?? 0;
  }
}

function makeTouchEvent(
  touches: Array<{ identifier: number; clientX: number; clientY: number }>,
  target: any = {},
): TouchEvent {
  return {
    touches: touches as unknown as TouchList,
    currentTarget: target,
    target,
  } as unknown as TouchEvent;
}

describe("colabOneTooltipInput", () => {
  let fakeWindow: FakeWindowTarget;
  let originalWindow: unknown;
  let nowMs: number;

  beforeEach(() => {
    vi.useFakeTimers();
    nowMs = 10_000;
    vi.spyOn(performance, "now").mockImplementation(() => nowMs);
    originalWindow = (globalThis as any).window;
    fakeWindow = new FakeWindowTarget();
    (globalThis as any).window = fakeWindow;
    resetColabOneTooltipInputTrackerForTest();
  });

  afterEach(() => {
    resetColabOneTooltipInputTrackerForTest();
    (globalThis as any).window = originalWindow;
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("tracks mouse vs touch modality and suppresses hover tooltips after touch taps", () => {
    ensureColabOneTooltipInputTracker();
    expect(getColabOneTooltipInputModality()).toBe("mouse");
    expect(isColabOneTouchInputActive(nowMs)).toBe(false);
    expect(shouldSuppressColabOneHoverTooltip(120, nowMs)).toBe(false);

    // Desktop mouse pointerdown suppresses only within enterDelayMs
    fakeWindow.dispatch("pointerdown", { pointerType: "mouse" });
    expect(getColabOneTooltipInputModality()).toBe("mouse");
    expect(shouldSuppressColabOneHoverTooltip(120, nowMs + 50)).toBe(true);
    expect(shouldSuppressColabOneHoverTooltip(120, nowMs + 150)).toBe(false);

    // Touch tap switches modality to touch and keeps suppressing hover even well past 120ms
    nowMs += 200;
    fakeWindow.dispatch("pointerdown", { pointerType: "touch" });
    fakeWindow.dispatch("touchstart", {});
    nowMs += 80;
    fakeWindow.dispatch("pointerup", { pointerType: "touch" });
    fakeWindow.dispatch("touchend", {});

    expect(getColabOneTooltipInputModality()).toBe("touch");
    expect(isColabOneTouchInputActive(nowMs + 120)).toBe(true);
    expect(shouldSuppressColabOneHoverTooltip(120, nowMs + 250)).toBe(true);
    expect(shouldSuppressColabOneHoverTooltip(120, nowMs + 5000)).toBe(true);

    // Real mouse pointermove within recovery gap is ignored; after gap resumes mouse hover immediately
    fakeWindow.dispatch("pointermove", { pointerType: "mouse" });
    expect(getColabOneTooltipInputModality()).toBe("touch");

    nowMs += COLABONE_TOOLTIP_MOUSE_RECOVERY_GAP_MS + 50;
    fakeWindow.dispatch("pointermove", { pointerType: "mouse" });
    expect(getColabOneTooltipInputModality()).toBe("mouse");
    expect(isColabOneTouchInputActive(nowMs)).toBe(false);
    expect(shouldSuppressColabOneHoverTooltip(120, nowMs)).toBe(false);
  });

  it("does not trigger long-press tooltip on a normal quick tap", () => {
    const onTrigger = vi.fn();
    const onDismiss = vi.fn();
    const ctrl = createColabOneTouchLongPressController({
      canTrigger: () => true,
      onTrigger,
      onDismiss,
    });
    const btn = { id: "btn-1" };

    ctrl.start(makeTouchEvent([{ identifier: 1, clientX: 50, clientY: 50 }], btn));
    vi.advanceTimersByTime(100);
    fakeWindow.dispatch("touchend", makeTouchEvent([], btn));
    vi.advanceTimersByTime(1000);

    expect(onTrigger).not.toHaveBeenCalled();
    expect(ctrl.isLongPressOpen()).toBe(false);

    // Trailing click from quick tap is NOT suppressed
    const clickEvent = {
      target: btn,
      preventDefault: vi.fn(),
      stopImmediatePropagation: vi.fn(),
    };
    fakeWindow.dispatch("click", clickEvent);
    expect(clickEvent.preventDefault).not.toHaveBeenCalled();
    expect(clickEvent.stopImmediatePropagation).not.toHaveBeenCalled();
    ctrl.cancel();
  });

  it("cancels long-press if finger moves beyond tolerance or page scrolls", () => {
    const onTrigger = vi.fn();
    const onDismiss = vi.fn();
    const ctrl = createColabOneTouchLongPressController({
      canTrigger: () => true,
      onTrigger,
      onDismiss,
    });
    const btn = { id: "btn-2" };

    ctrl.start(makeTouchEvent([{ identifier: 1, clientX: 50, clientY: 50 }], btn));
    vi.advanceTimersByTime(200);
    fakeWindow.dispatch(
      "touchmove",
      makeTouchEvent([{ identifier: 1, clientX: 50, clientY: 65 }], btn),
    );
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS);
    expect(onTrigger).not.toHaveBeenCalled();

    // Also cancels on scroll
    ctrl.start(makeTouchEvent([{ identifier: 2, clientX: 50, clientY: 50 }], btn));
    vi.advanceTimersByTime(200);
    fakeWindow.dispatch("scroll", {});
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS);
    expect(onTrigger).not.toHaveBeenCalled();

    // Also cancels when a second finger touches anywhere during the press
    ctrl.start(makeTouchEvent([{ identifier: 3, clientX: 50, clientY: 50 }], btn));
    vi.advanceTimersByTime(200);
    fakeWindow.dispatch(
      "touchstart",
      makeTouchEvent(
        [
          { identifier: 3, clientX: 50, clientY: 50 },
          { identifier: 4, clientX: 180, clientY: 220 },
        ],
        { id: "elsewhere" },
      ),
    );
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS);
    expect(onTrigger).not.toHaveBeenCalled();
    ctrl.cancel();
  });

  it("opens on steady long-press, suppresses trailing click and contextmenu, disarms on next touch, and auto-dismisses after lift", () => {
    const onTrigger = vi.fn();
    const onDismiss = vi.fn();
    const ctrl = createColabOneTouchLongPressController({
      canTrigger: () => true,
      onTrigger,
      onDismiss,
    });
    const btn = { id: "btn-3" } as unknown as EventTarget;

    ctrl.start(makeTouchEvent([{ identifier: 1, clientX: 100, clientY: 100 }], btn));
    expect(ctrl.shouldSuppressContextMenu(btn)).toBe(true);

    // Slight jitter within 10px tolerance still triggers
    vi.advanceTimersByTime(200);
    fakeWindow.dispatch(
      "touchmove",
      makeTouchEvent([{ identifier: 1, clientX: 104, clientY: 103 }], btn),
    );
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS - 200);

    expect(onTrigger).toHaveBeenCalledTimes(1);
    expect(onTrigger).toHaveBeenCalledWith(btn);
    expect(ctrl.isLongPressOpen()).toBe(true);

    // Lift finger: tooltip remains open for reading, trailing synthetic mousedown and click are intercepted
    fakeWindow.dispatch("touchend", makeTouchEvent([], btn));
    expect(ctrl.isLongPressOpen()).toBe(true);
    expect(onDismiss).not.toHaveBeenCalled();

    const syntheticMouseDown = {
      target: btn,
      preventDefault: vi.fn(),
      stopImmediatePropagation: vi.fn(),
    };
    fakeWindow.dispatch("mousedown", syntheticMouseDown);
    expect(syntheticMouseDown.preventDefault).toHaveBeenCalledTimes(1);
    expect(syntheticMouseDown.stopImmediatePropagation).toHaveBeenCalledTimes(1);

    const syntheticClick = {
      target: btn,
      preventDefault: vi.fn(),
      stopImmediatePropagation: vi.fn(),
    };
    fakeWindow.dispatch("click", syntheticClick);
    expect(syntheticClick.preventDefault).toHaveBeenCalledTimes(1);
    expect(syntheticClick.stopImmediatePropagation).toHaveBeenCalledTimes(1);

    // A subsequent genuine click is NOT suppressed
    const nextClick = {
      target: btn,
      preventDefault: vi.fn(),
      stopImmediatePropagation: vi.fn(),
    };
    fakeWindow.dispatch("click", nextClick);
    expect(nextClick.preventDefault).not.toHaveBeenCalled();

    // Auto-dismisses cleanly after configured post-lift duration
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_AUTO_DISMISS_MS);
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(ctrl.isLongPressOpen()).toBe(false);
    ctrl.cancel();
  });

  it("disarms click suppressor immediately on a fresh tap elsewhere so no genuine tap is ever swallowed", () => {
    const onTrigger = vi.fn();
    const onDismiss = vi.fn();
    const ctrl = createColabOneTouchLongPressController({
      canTrigger: () => true,
      onTrigger,
      onDismiss,
    });
    const btnA = { id: "btn-a" };

    ctrl.start(makeTouchEvent([{ identifier: 1, clientX: 20, clientY: 20 }], btnA));
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS);
    expect(onTrigger).toHaveBeenCalledTimes(1);

    // Lift finger (suppose browser did not emit synthetic click)
    fakeWindow.dispatch("touchend", makeTouchEvent([], btnA));

    // User immediately starts a fresh tap on btnA 100ms later
    vi.advanceTimersByTime(100);
    fakeWindow.dispatch("pointerdown", { pointerType: "touch" });
    const genuineClick = {
      target: btnA,
      preventDefault: vi.fn(),
      stopImmediatePropagation: vi.fn(),
    };
    fakeWindow.dispatch("click", genuineClick);
    expect(genuineClick.preventDefault).not.toHaveBeenCalled();
    expect(genuineClick.stopImmediatePropagation).not.toHaveBeenCalled();
    ctrl.cancel();
  });

  it("cleans up all window listeners on cancel", () => {
    ensureColabOneTooltipInputTracker();
    const baseTouchStart = fakeWindow.listenerCount("touchstart");
    const baseTouchEnd = fakeWindow.listenerCount("touchend");
    const ctrl = createColabOneTouchLongPressController({
      canTrigger: () => true,
      onTrigger: vi.fn(),
      onDismiss: vi.fn(),
    });
    const btn = { id: "btn-cleanup" };
    ctrl.start(makeTouchEvent([{ identifier: 1, clientX: 10, clientY: 10 }], btn));
    expect(fakeWindow.listenerCount("touchstart")).toBe(baseTouchStart + 1);
    expect(fakeWindow.listenerCount("touchmove")).toBe(1);
    expect(fakeWindow.listenerCount("touchend")).toBe(baseTouchEnd + 1);
    ctrl.cancel();
    expect(fakeWindow.listenerCount("touchstart")).toBe(baseTouchStart);
    expect(fakeWindow.listenerCount("touchmove")).toBe(0);
    expect(fakeWindow.listenerCount("touchend")).toBe(baseTouchEnd);
    expect(fakeWindow.listenerCount("mousedown")).toBe(0);
    expect(fakeWindow.listenerCount("click")).toBe(0);
    expect(ctrl.shouldSuppressContextMenu(btn as unknown as EventTarget)).toBe(
      false,
    );
  });

  it("rolls back long-press state without suppressing click if onTrigger rejects or dismisses synchronously", () => {
    const btn = { id: "btn-reject" } as unknown as EventTarget;
    const ctrl = createColabOneTouchLongPressController({
      canTrigger: () => true,
      onTrigger: () => false,
      onDismiss: vi.fn(),
    });
    ctrl.start(makeTouchEvent([{ identifier: 1, clientX: 30, clientY: 30 }], btn));
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS);
    expect(ctrl.isLongPressOpen()).toBe(false);
    fakeWindow.dispatch("touchend", makeTouchEvent([], btn));

    const clickEv = {
      target: btn,
      preventDefault: vi.fn(),
      stopImmediatePropagation: vi.fn(),
    };
    fakeWindow.dispatch("click", clickEv);
    expect(clickEv.preventDefault).not.toHaveBeenCalled();

    let ctrl2!: ReturnType<typeof createColabOneTouchLongPressController>;
    ctrl2 = createColabOneTouchLongPressController({
      canTrigger: () => true,
      onTrigger: () => {
        ctrl2.notifyDismissed();
      },
      onDismiss: vi.fn(),
    });
    ctrl2.start(makeTouchEvent([{ identifier: 2, clientX: 30, clientY: 30 }], btn));
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS);
    expect(ctrl2.isLongPressOpen()).toBe(false);
    fakeWindow.dispatch("touchend", makeTouchEvent([], btn));
    fakeWindow.dispatch("click", clickEv);
    expect(clickEv.preventDefault).not.toHaveBeenCalled();
    ctrl.cancel();
    ctrl2.cancel();
  });

  it("still suppresses trailing click on lift if finger slides after long-press already opened", () => {
    const onTrigger = vi.fn();
    let ctrl!: ReturnType<typeof createColabOneTouchLongPressController>;
    const onDismiss = vi.fn(() => {
      ctrl.notifyDismissed();
    });
    ctrl = createColabOneTouchLongPressController({
      canTrigger: () => true,
      onTrigger,
      onDismiss,
    });
    const btn = { id: "btn-slide-after-trigger" } as unknown as EventTarget;

    ctrl.start(makeTouchEvent([{ identifier: 1, clientX: 80, clientY: 80 }], btn));
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS);
    expect(onTrigger).toHaveBeenCalledTimes(1);
    expect(ctrl.isLongPressOpen()).toBe(true);

    // Thumb slides > 10px while still touching the screen -> tooltip dismisses immediately
    fakeWindow.dispatch(
      "touchmove",
      makeTouchEvent([{ identifier: 1, clientX: 96, clientY: 80 }], btn),
    );
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(ctrl.isLongPressOpen()).toBe(false);

    // Finger remains held past clickSuppressMs before lifting
    vi.advanceTimersByTime(800);
    fakeWindow.dispatch("touchend", makeTouchEvent([], btn));

    // Trailing synthetic mousedown + click at lift are still suppressed
    const syntheticMouseDown = {
      target: btn,
      preventDefault: vi.fn(),
      stopImmediatePropagation: vi.fn(),
    };
    const syntheticClick = {
      target: btn,
      preventDefault: vi.fn(),
      stopImmediatePropagation: vi.fn(),
    };
    fakeWindow.dispatch("mousedown", syntheticMouseDown);
    fakeWindow.dispatch("click", syntheticClick);
    expect(syntheticMouseDown.preventDefault).toHaveBeenCalledTimes(1);
    expect(syntheticClick.preventDefault).toHaveBeenCalledTimes(1);

    // No redundant auto-dismiss callback fires after lift
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_AUTO_DISMISS_MS * 2);
    expect(onDismiss).toHaveBeenCalledTimes(1);

    // Same guarantee when an external scroll dismiss listener notifies before handleTouchScroll
    const externalScrollDismiss = () => {
      ctrl.notifyDismissed();
    };
    fakeWindow.addEventListener("scroll", externalScrollDismiss);
    ctrl.start(makeTouchEvent([{ identifier: 2, clientX: 80, clientY: 80 }], btn));
    vi.advanceTimersByTime(COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS);
    expect(ctrl.isLongPressOpen()).toBe(true);
    fakeWindow.dispatch("scroll", {});
    expect(ctrl.isLongPressOpen()).toBe(false);
    fakeWindow.dispatch("touchend", makeTouchEvent([], btn));

    const postScrollClick = {
      target: btn,
      preventDefault: vi.fn(),
      stopImmediatePropagation: vi.fn(),
    };
    fakeWindow.dispatch("click", postScrollClick);
    expect(postScrollClick.preventDefault).toHaveBeenCalledTimes(1);
    fakeWindow.removeEventListener("scroll", externalScrollDismiss);
    ctrl.cancel();
  });
});
