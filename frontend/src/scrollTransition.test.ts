import { describe, expect, it, vi } from "vitest";
import {
  COLABONE_SCROLL_TOP_MAX_MS,
  COLABONE_SCROLL_TOP_MIN_MS,
  computeColabOneScrollDuration,
  createColabOneScrollController,
  evalColabOneScrollEase,
  type ScrollEventTargetLike,
  type ScrollableElementLike,
} from "./scrollTransition";

function createMockEventTarget() {
  const listeners = new Map<string, Set<EventListenerOrEventListenerObject>>();
  const target: ScrollEventTargetLike = {
    addEventListener(type, listener) {
      let set = listeners.get(type);
      if (!set) {
        set = new Set();
        listeners.set(type, set);
      }
      set.add(listener);
    },
    removeEventListener(type, listener) {
      listeners.get(type)?.delete(listener);
    },
  };
  return {
    target,
    listeners,
    dispatch(type: string, event: Partial<KeyboardEvent> = {}) {
      for (const listener of [...(listeners.get(type) ?? [])]) {
        if (typeof listener === "function") {
          listener(event as Event);
        } else {
          listener.handleEvent(event as Event);
        }
      }
    },
    listenerCount(type?: string) {
      if (type) return listeners.get(type)?.size ?? 0;
      let total = 0;
      for (const set of listeners.values()) total += set.size;
      return total;
    },
  };
}

function createFakeRaf() {
  let nextId = 1;
  const callbacks = new Map<number, FrameRequestCallback>();
  const requestAnimationFrame = vi.fn((cb: FrameRequestCallback) => {
    const id = nextId++;
    callbacks.set(id, cb);
    return id;
  });
  const cancelAnimationFrame = vi.fn((id: number) => {
    callbacks.delete(id);
  });
  return {
    requestAnimationFrame,
    cancelAnimationFrame,
    pendingCount: () => callbacks.size,
    flushFrame(timestamp: number) {
      const entries = [...callbacks.entries()];
      callbacks.clear();
      for (const [, cb] of entries) {
        cb(timestamp);
      }
    },
  };
}

describe("scrollTransition smooth scroll-to-top helpers", () => {
  it("evaluates ease-out cubic monotonically with quick start and gentle landing", () => {
    expect(evalColabOneScrollEase(-0.5)).toBe(0);
    expect(evalColabOneScrollEase(0)).toBe(0);
    expect(evalColabOneScrollEase(Number.NaN)).toBe(0);
    expect(evalColabOneScrollEase(1)).toBe(1);
    expect(evalColabOneScrollEase(1.5)).toBe(1);

    // Ease-out covers more than half the distance by halfway through duration.
    expect(evalColabOneScrollEase(0.5)).toBeCloseTo(0.875, 5);

    let prev = 0;
    for (let i = 1; i <= 20; i += 1) {
      const val = evalColabOneScrollEase(i / 20);
      expect(val).toBeGreaterThan(prev);
      prev = val;
    }
  });

  it("scales duration smoothly with distance and clamps within min/max bounds", () => {
    expect(computeColabOneScrollDuration(0)).toBe(0);
    expect(computeColabOneScrollDuration(-20)).toBe(0);
    expect(computeColabOneScrollDuration(Number.NaN)).toBe(0);

    const shortDur = computeColabOneScrollDuration(20);
    const mediumDur = computeColabOneScrollDuration(400);
    const tallDur = computeColabOneScrollDuration(2500);

    expect(shortDur).toBeGreaterThanOrEqual(COLABONE_SCROLL_TOP_MIN_MS);
    expect(mediumDur).toBeGreaterThan(shortDur);
    expect(tallDur).toBe(COLABONE_SCROLL_TOP_MAX_MS);
  });

  it("smoothly eases scrollTop from current offset to 0 and cleans up listeners", () => {
    const raf = createFakeRaf();
    const elEvents = createMockEventTarget();
    const keyEvents = createMockEventTarget();
    const el: ScrollableElementLike = {
      scrollTop: 600,
      addEventListener: elEvents.target.addEventListener,
      removeEventListener: elEvents.target.removeEventListener,
    };

    const controller = createColabOneScrollController({
      requestAnimationFrame: raf.requestAnimationFrame,
      cancelAnimationFrame: raf.cancelAnimationFrame,
      prefersReducedMotion: () => false,
      keyTarget: keyEvents.target,
    });

    controller.scrollToTop(el, { durationMs: 300 });
    expect(controller.isAnimating()).toBe(true);
    expect(elEvents.listenerCount()).toBe(3);
    expect(keyEvents.listenerCount("keydown")).toBe(1);

    const samples: number[] = [];
    raf.flushFrame(100);
    samples.push(el.scrollTop);
    expect(el.scrollTop).toBe(600);

    raf.flushFrame(200);
    samples.push(el.scrollTop);
    expect(el.scrollTop).toBeLessThan(600);
    expect(el.scrollTop).toBeGreaterThan(0);

    raf.flushFrame(300);
    samples.push(el.scrollTop);
    expect(el.scrollTop).toBeLessThan(samples[1]);
    expect(el.scrollTop).toBeGreaterThan(0);

    raf.flushFrame(400);
    samples.push(el.scrollTop);
    expect(el.scrollTop).toBe(0);
    expect(controller.isAnimating()).toBe(false);
    expect(raf.pendingCount()).toBe(0);
    expect(elEvents.listenerCount()).toBe(0);
    expect(keyEvents.listenerCount()).toBe(0);
  });

  it("no-ops when already at top and snaps subpixel offsets immediately", () => {
    const raf = createFakeRaf();
    const controller = createColabOneScrollController({
      requestAnimationFrame: raf.requestAnimationFrame,
      cancelAnimationFrame: raf.cancelAnimationFrame,
      prefersReducedMotion: () => false,
      keyTarget: null,
    });

    const atTop: ScrollableElementLike = { scrollTop: 0 };
    controller.scrollToTop(atTop);
    expect(raf.requestAnimationFrame).not.toHaveBeenCalled();
    expect(atTop.scrollTop).toBe(0);

    const tinyOffset: ScrollableElementLike = { scrollTop: 0.4 };
    controller.scrollToTop(tinyOffset);
    expect(raf.requestAnimationFrame).not.toHaveBeenCalled();
    expect(tinyOffset.scrollTop).toBe(0);
  });

  it("respects prefers-reduced-motion and instant option without scheduling frames", () => {
    const raf = createFakeRaf();
    const reducedController = createColabOneScrollController({
      requestAnimationFrame: raf.requestAnimationFrame,
      cancelAnimationFrame: raf.cancelAnimationFrame,
      prefersReducedMotion: () => true,
      keyTarget: null,
    });

    const el: ScrollableElementLike = { scrollTop: 520 };
    reducedController.scrollToTop(el);
    expect(el.scrollTop).toBe(0);
    expect(raf.requestAnimationFrame).not.toHaveBeenCalled();

    const normalController = createColabOneScrollController({
      requestAnimationFrame: raf.requestAnimationFrame,
      cancelAnimationFrame: raf.cancelAnimationFrame,
      prefersReducedMotion: () => false,
      keyTarget: null,
    });
    el.scrollTop = 480;
    normalController.scrollToTop(el, { instant: true });
    expect(el.scrollTop).toBe(0);
    expect(raf.requestAnimationFrame).not.toHaveBeenCalled();
  });

  it("cancels smoothly when the user interrupts via wheel or scroll key while ignoring Alt shortcuts", () => {
    const raf = createFakeRaf();
    const elEvents = createMockEventTarget();
    const keyEvents = createMockEventTarget();
    const el: ScrollableElementLike = {
      scrollTop: 500,
      addEventListener: elEvents.target.addEventListener,
      removeEventListener: elEvents.target.removeEventListener,
    };

    const controller = createColabOneScrollController({
      requestAnimationFrame: raf.requestAnimationFrame,
      cancelAnimationFrame: raf.cancelAnimationFrame,
      prefersReducedMotion: () => false,
      keyTarget: keyEvents.target,
    });

    controller.scrollToTop(el, { durationMs: 300 });
    raf.flushFrame(0);
    raf.flushFrame(100);
    const midScroll = el.scrollTop;
    expect(midScroll).toBeGreaterThan(0);
    expect(midScroll).toBeLessThan(500);

    // Modified shortcut keys like Alt+2 must not interrupt scrolling.
    keyEvents.dispatch("keydown", { key: "2", altKey: true });
    expect(controller.isAnimating()).toBe(true);

    // Wheel input immediately interrupts and cleans up listeners.
    elEvents.dispatch("wheel");
    expect(controller.isAnimating()).toBe(false);
    expect(raf.cancelAnimationFrame).toHaveBeenCalledTimes(1);
    expect(elEvents.listenerCount()).toBe(0);
    expect(keyEvents.listenerCount()).toBe(0);

    // Unmodified ArrowDown key also interrupts an active animation.
    el.scrollTop = 420;
    controller.scrollToTop(el, { durationMs: 300 });
    expect(controller.isAnimating()).toBe(true);
    keyEvents.dispatch("keydown", { key: "ArrowDown" });
    expect(controller.isAnimating()).toBe(false);
    expect(elEvents.listenerCount()).toBe(0);
    expect(keyEvents.listenerCount()).toBe(0);
  });

  it("replaces an in-flight animation cleanly on rapid consecutive calls", () => {
    const raf = createFakeRaf();
    const elEvents = createMockEventTarget();
    const keyEvents = createMockEventTarget();
    const el: ScrollableElementLike = {
      scrollTop: 600,
      addEventListener: elEvents.target.addEventListener,
      removeEventListener: elEvents.target.removeEventListener,
    };

    const controller = createColabOneScrollController({
      requestAnimationFrame: raf.requestAnimationFrame,
      cancelAnimationFrame: raf.cancelAnimationFrame,
      prefersReducedMotion: () => false,
      keyTarget: keyEvents.target,
    });

    controller.scrollToTop(el, { durationMs: 300 });
    raf.flushFrame(0);
    raf.flushFrame(60);

    // Rapid second tab switch while first animation is still running
    controller.scrollToTop(el, { durationMs: 240 });
    expect(raf.cancelAnimationFrame).toHaveBeenCalledTimes(1);
    expect(elEvents.listenerCount()).toBe(3);
    expect(keyEvents.listenerCount("keydown")).toBe(1);

    raf.flushFrame(100);
    raf.flushFrame(340);
    expect(el.scrollTop).toBe(0);
    expect(controller.isAnimating()).toBe(false);
    expect(elEvents.listenerCount()).toBe(0);
    expect(keyEvents.listenerCount()).toBe(0);
  });
});
