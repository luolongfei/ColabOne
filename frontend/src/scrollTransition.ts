export const COLABONE_SCROLL_TOP_MIN_MS = 180;
export const COLABONE_SCROLL_TOP_MAX_MS = 340;
export const COLABONE_SCROLL_SNAP_EPSILON_PX = 1;

const SCROLL_INTERRUPT_KEYS = new Set([
  "ArrowDown",
  "ArrowUp",
  "PageDown",
  "PageUp",
  "Home",
  "End",
  " ",
  "Spacebar",
]);

export interface ScrollableElementLike {
  scrollTop: number;
  addEventListener?: (
    type: string,
    listener: EventListenerOrEventListenerObject,
    options?: boolean | AddEventListenerOptions,
  ) => void;
  removeEventListener?: (
    type: string,
    listener: EventListenerOrEventListenerObject,
    options?: boolean | EventListenerOptions,
  ) => void;
}

export interface ScrollEventTargetLike {
  addEventListener: (
    type: string,
    listener: EventListenerOrEventListenerObject,
    options?: boolean | AddEventListenerOptions,
  ) => void;
  removeEventListener: (
    type: string,
    listener: EventListenerOrEventListenerObject,
    options?: boolean | EventListenerOptions,
  ) => void;
}

export interface ColabOneScrollControllerOptions {
  requestAnimationFrame?: (cb: FrameRequestCallback) => number;
  cancelAnimationFrame?: (id: number) => void;
  prefersReducedMotion?: () => boolean;
  keyTarget?: ScrollEventTargetLike | null;
}

export interface ColabOneScrollToTopOptions {
  instant?: boolean;
  durationMs?: number;
}

export interface ColabOneScrollController {
  scrollToTop: (
    el: ScrollableElementLike | null | undefined,
    options?: ColabOneScrollToTopOptions,
  ) => void;
  cancel: () => void;
  isAnimating: () => boolean;
}

export function evalColabOneScrollEase(t: number): number {
  if (!Number.isFinite(t) || t <= 0) return 0;
  if (t >= 1) return 1;
  const inv = 1 - t;
  return 1 - inv * inv * inv;
}

export function computeColabOneScrollDuration(distancePx: number): number {
  if (!Number.isFinite(distancePx) || distancePx <= 0) return 0;
  const raw = 160 + Math.sqrt(distancePx) * 7;
  return Math.round(
    Math.min(
      COLABONE_SCROLL_TOP_MAX_MS,
      Math.max(COLABONE_SCROLL_TOP_MIN_MS, raw),
    ),
  );
}

function defaultPrefersReducedMotion(): boolean {
  if (
    typeof window === "undefined" ||
    typeof window.matchMedia !== "function"
  ) {
    return false;
  }
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

export function createColabOneScrollController(
  options: ColabOneScrollControllerOptions = {},
): ColabOneScrollController {
  const raf =
    options.requestAnimationFrame ??
    (typeof window !== "undefined" &&
    typeof window.requestAnimationFrame === "function"
      ? (cb: FrameRequestCallback) => window.requestAnimationFrame(cb)
      : null);
  const caf =
    options.cancelAnimationFrame ??
    (typeof window !== "undefined" &&
    typeof window.cancelAnimationFrame === "function"
      ? (id: number) => window.cancelAnimationFrame(id)
      : null);
  const prefersReducedMotion =
    options.prefersReducedMotion ?? defaultPrefersReducedMotion;
  const keyTarget =
    options.keyTarget !== undefined
      ? options.keyTarget
      : typeof window !== "undefined"
        ? window
        : null;

  let activeToken = 0;
  let rafId: number | null = null;
  let cleanupListeners: (() => void) | null = null;

  function detachListeners() {
    if (cleanupListeners) {
      cleanupListeners();
      cleanupListeners = null;
    }
  }

  function cancel() {
    activeToken += 1;
    if (rafId !== null) {
      caf?.(rafId);
      rafId = null;
    }
    detachListeners();
  }

  function scrollToTop(
    el: ScrollableElementLike | null | undefined,
    scrollOptions: ColabOneScrollToTopOptions = {},
  ) {
    cancel();
    if (!el) return;

    const startTop = el.scrollTop;
    if (!Number.isFinite(startTop) || startTop <= 0) return;

    if (
      scrollOptions.instant ||
      startTop < COLABONE_SCROLL_SNAP_EPSILON_PX ||
      prefersReducedMotion() ||
      !raf
    ) {
      el.scrollTop = 0;
      return;
    }

    const durationMs =
      scrollOptions.durationMs !== undefined
        ? Math.max(0, scrollOptions.durationMs)
        : computeColabOneScrollDuration(startTop);

    if (durationMs <= 0) {
      el.scrollTop = 0;
      return;
    }

    const token = activeToken;
    const onPointerOrWheel = () => {
      if (token === activeToken) cancel();
    };
    const onKeydown = (event: Event) => {
      if (token !== activeToken) return;
      const ke = event as KeyboardEvent;
      if (ke.altKey || ke.ctrlKey || ke.metaKey) return;
      if (SCROLL_INTERRUPT_KEYS.has(ke.key)) {
        cancel();
      }
    };

    const elementEvents = ["wheel", "touchstart", "pointerdown"] as const;
    for (const type of elementEvents) {
      el.addEventListener?.(type, onPointerOrWheel, { passive: true });
    }
    keyTarget?.addEventListener("keydown", onKeydown, { passive: true });

    cleanupListeners = () => {
      for (const type of elementEvents) {
        el.removeEventListener?.(type, onPointerOrWheel);
      }
      keyTarget?.removeEventListener("keydown", onKeydown);
    };

    let startTime: number | null = null;
    const step: FrameRequestCallback = (timestamp: number) => {
      if (token !== activeToken) return;
      if (startTime === null) {
        startTime = timestamp;
      }
      const elapsed = Math.max(0, timestamp - startTime);
      const progress = Math.min(1, elapsed / durationMs);
      const eased = evalColabOneScrollEase(progress);
      const nextTop = progress >= 1 ? 0 : Math.max(0, startTop * (1 - eased));
      el.scrollTop = nextTop;

      if (progress >= 1 || nextTop <= 0.5 || el.scrollTop <= 0) {
        el.scrollTop = 0;
        rafId = null;
        detachListeners();
        return;
      }
      rafId = raf(step);
    };

    rafId = raf(step);
  }

  return {
    scrollToTop,
    cancel,
    isAnimating: () => rafId !== null,
  };
}
