export const COLABONE_VISIBLE_POLL_MS = 600;
export const COLABONE_HIDDEN_POLL_MS = 3000;
export const COLABONE_LOG_WINDOW_VISIBLE_POLL_MS = 1000;

export interface ColabOneVisibilityPollerOptions {
  /** Poll cadence in ms while the page is visible. */
  visibleMs: number;
  /** Slower poll cadence in ms while the page is hidden/minimized. */
  hiddenMs: number;
  /** Async poll callback. Rejection is swallowed so polling never stalls. */
  tick: () => Promise<void> | void;
  /** Optional hook invoked immediately whenever the page transitions to visible. */
  onVisible?: () => void;
  /** Override visibility detection (defaults to `document.hidden`). */
  isHidden?: () => boolean;
  /** Override visibilitychange subscription (defaults to `document`). */
  subscribeVisibility?: (handler: () => void) => () => void;
}

export interface ColabOneVisibilityPoller {
  start: () => void;
  stop: () => void;
  isRunning: () => boolean;
}

function defaultIsHidden(): boolean {
  return typeof document !== "undefined" && Boolean(document.hidden);
}

function defaultSubscribeVisibility(handler: () => void): () => void {
  if (typeof document === "undefined") {
    return () => {};
  }
  document.addEventListener("visibilitychange", handler);
  return () => {
    document.removeEventListener("visibilitychange", handler);
  };
}

/**
 * Visibility-aware non-overlapping polling loop for ColabOne desktop windows.
 * Slows background polling when minimized/hidden to save CPU and battery, and
 * snaps immediately back to the foreground cadence on visibility restore.
 */
export function createColabOneVisibilityPoller(
  options: ColabOneVisibilityPollerOptions,
): ColabOneVisibilityPoller {
  const visibleMs = Math.max(50, Math.round(options.visibleMs));
  const hiddenMs = Math.max(visibleMs, Math.round(options.hiddenMs));
  const isHidden = options.isHidden ?? defaultIsHidden;
  const subscribeVisibility =
    options.subscribeVisibility ?? defaultSubscribeVisibility;

  let running = false;
  let inFlight = false;
  let sessionId = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let unsubscribeVisibility: (() => void) | null = null;

  function clearPendingTimer() {
    if (timer !== undefined) {
      clearTimeout(timer);
      timer = undefined;
    }
  }

  function nextIntervalMs(): number {
    return isHidden() ? hiddenMs : visibleMs;
  }

  function scheduleNext() {
    if (!running || inFlight) return;
    clearPendingTimer();
    timer = setTimeout(() => {
      timer = undefined;
      void runTick();
    }, nextIntervalMs());
  }

  async function runTick() {
    if (!running || inFlight) return;
    clearPendingTimer();
    const sid = sessionId;
    inFlight = true;
    try {
      await options.tick();
    } catch {
      // Caller owns error handling; keep the polling loop resilient.
    } finally {
      if (sid === sessionId) {
        inFlight = false;
        scheduleNext();
      }
    }
  }

  function handleVisibilityChange() {
    if (!running) return;
    if (!isHidden()) {
      options.onVisible?.();
      if (!inFlight) {
        void runTick();
      }
      return;
    }
    if (!inFlight && timer !== undefined) {
      scheduleNext();
    }
  }

  return {
    start() {
      if (running) return;
      running = true;
      sessionId += 1;
      unsubscribeVisibility = subscribeVisibility(handleVisibilityChange);
      void runTick();
    },
    stop() {
      if (!running) return;
      running = false;
      sessionId += 1;
      inFlight = false;
      clearPendingTimer();
      unsubscribeVisibility?.();
      unsubscribeVisibility = null;
    },
    isRunning() {
      return running;
    },
  };
}
