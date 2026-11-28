export const COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS = 480;
export const COLABONE_TOOLTIP_LONG_PRESS_MOVE_TOLERANCE_PX = 10;
export const COLABONE_TOOLTIP_LONG_PRESS_CLICK_SUPPRESS_MS = 500;
export const COLABONE_TOOLTIP_LONG_PRESS_AUTO_DISMISS_MS = 2200;
export const COLABONE_TOOLTIP_MOUSE_RECOVERY_GAP_MS = 400;

export type ColabOneTooltipInputModality = "mouse" | "touch";

interface ColabOneTooltipInputState {
  bound: boolean;
  modality: ColabOneTooltipInputModality;
  lastTouchAt: number;
  lastPointerDownAt: number;
}

const inputState: ColabOneTooltipInputState = {
  bound: false,
  modality: "mouse",
  lastTouchAt: 0,
  lastPointerDownAt: 0,
};

function currentTimeMs(): number {
  return typeof performance !== "undefined" ? performance.now() : Date.now();
}

function handleGlobalPointerDown(event: PointerEvent) {
  const now = currentTimeMs();
  inputState.lastPointerDownAt = now;
  if (event.pointerType === "touch") {
    inputState.lastTouchAt = now;
    inputState.modality = "touch";
  } else if (event.pointerType === "mouse") {
    inputState.modality = "mouse";
    inputState.lastTouchAt = 0;
  }
}

function handleGlobalPointerUpOrCancel(event: PointerEvent) {
  if (event.pointerType === "touch") {
    inputState.lastTouchAt = currentTimeMs();
    inputState.modality = "touch";
  }
}

function handleGlobalPointerMove(event: PointerEvent) {
  if (event.pointerType !== "mouse") return;
  const now = currentTimeMs();
  if (
    inputState.lastTouchAt > 0 &&
    now - inputState.lastTouchAt < COLABONE_TOOLTIP_MOUSE_RECOVERY_GAP_MS
  ) {
    return;
  }
  inputState.modality = "mouse";
  inputState.lastTouchAt = 0;
}

function handleGlobalTouchActivity() {
  inputState.lastTouchAt = currentTimeMs();
  inputState.modality = "touch";
}

export function ensureColabOneTooltipInputTracker(): void {
  if (inputState.bound || typeof window === "undefined") return;
  inputState.bound = true;
  window.addEventListener("pointerdown", handleGlobalPointerDown, {
    capture: true,
    passive: true,
  });
  window.addEventListener("pointerup", handleGlobalPointerUpOrCancel, {
    capture: true,
    passive: true,
  });
  window.addEventListener("pointercancel", handleGlobalPointerUpOrCancel, {
    capture: true,
    passive: true,
  });
  window.addEventListener("pointermove", handleGlobalPointerMove, {
    capture: true,
    passive: true,
  });
  window.addEventListener("touchstart", handleGlobalTouchActivity, {
    capture: true,
    passive: true,
  });
  window.addEventListener("touchend", handleGlobalTouchActivity, {
    capture: true,
    passive: true,
  });
  window.addEventListener("touchcancel", handleGlobalTouchActivity, {
    capture: true,
    passive: true,
  });
}

export function getColabOneTooltipInputModality(): ColabOneTooltipInputModality {
  return inputState.modality;
}

export function isColabOneTouchInputActive(now = currentTimeMs()): boolean {
  if (inputState.modality === "touch") {
    return true;
  }
  return (
    inputState.lastTouchAt > 0 &&
    now - inputState.lastTouchAt < COLABONE_TOOLTIP_MOUSE_RECOVERY_GAP_MS
  );
}

export function shouldSuppressColabOneHoverTooltip(
  mouseEnterDelayMs: number,
  now = currentTimeMs(),
): boolean {
  if (isColabOneTouchInputActive(now)) {
    return true;
  }
  if (
    mouseEnterDelayMs > 0 &&
    inputState.lastPointerDownAt > 0 &&
    now - inputState.lastPointerDownAt < mouseEnterDelayMs
  ) {
    return true;
  }
  return false;
}

export function resetColabOneTooltipInputTrackerForTest(): void {
  if (inputState.bound && typeof window !== "undefined") {
    window.removeEventListener("pointerdown", handleGlobalPointerDown, true);
    window.removeEventListener(
      "pointerup",
      handleGlobalPointerUpOrCancel,
      true,
    );
    window.removeEventListener(
      "pointercancel",
      handleGlobalPointerUpOrCancel,
      true,
    );
    window.removeEventListener("pointermove", handleGlobalPointerMove, true);
    window.removeEventListener("touchstart", handleGlobalTouchActivity, true);
    window.removeEventListener("touchend", handleGlobalTouchActivity, true);
    window.removeEventListener("touchcancel", handleGlobalTouchActivity, true);
  }
  inputState.bound = false;
  inputState.modality = "mouse";
  inputState.lastTouchAt = 0;
  inputState.lastPointerDownAt = 0;
}

export interface ColabOneTouchLongPressOptions {
  delayMs?: number;
  moveTolerancePx?: number;
  clickSuppressMs?: number;
  autoDismissMs?: number;
  canTrigger: (target?: EventTarget | null) => boolean;
  onTrigger: (target: EventTarget | null) => boolean | void;
  onDismiss: () => void;
}

export interface ColabOneTouchLongPressController {
  start: (event: TouchEvent, targetOverride?: EventTarget | null) => void;
  shouldSuppressContextMenu: (target?: EventTarget | null) => boolean;
  isLongPressOpen: () => boolean;
  cancel: () => void;
  notifyDismissed: () => void;
}

function findTouchById(
  touches: TouchList | undefined,
  identifier: number,
): Touch | null {
  if (!touches) return null;
  for (let i = 0; i < touches.length; i += 1) {
    if (touches[i].identifier === identifier) {
      return touches[i];
    }
  }
  return null;
}

function targetMatchesNode(
  origin: EventTarget | null,
  eventTarget: EventTarget | null,
): boolean {
  if (!origin || !eventTarget) return false;
  if (origin === eventTarget) return true;
  if (
    typeof Node !== "undefined" &&
    origin instanceof Node &&
    eventTarget instanceof Node
  ) {
    return origin.contains(eventTarget);
  }
  return false;
}

export function createColabOneTouchLongPressController(
  options: ColabOneTouchLongPressOptions,
): ColabOneTouchLongPressController {
  const delayMs = options.delayMs ?? COLABONE_TOOLTIP_LONG_PRESS_DELAY_MS;
  const moveTolerancePx =
    options.moveTolerancePx ?? COLABONE_TOOLTIP_LONG_PRESS_MOVE_TOLERANCE_PX;
  const clickSuppressMs =
    options.clickSuppressMs ?? COLABONE_TOOLTIP_LONG_PRESS_CLICK_SUPPRESS_MS;
  const autoDismissMs =
    options.autoDismissMs ?? COLABONE_TOOLTIP_LONG_PRESS_AUTO_DISMISS_MS;

  let pressTimer: ReturnType<typeof setTimeout> | null = null;
  let autoDismissTimer: ReturnType<typeof setTimeout> | null = null;
  let clickSuppressTimer: ReturnType<typeof setTimeout> | null = null;
  let clickSuppressTarget: EventTarget | null = null;
  let activeTouchId: number | null = null;
  let startX = 0;
  let startY = 0;
  let pressTarget: EventTarget | null = null;
  let triggeredForCurrentPress = false;
  let openFromLongPress = false;
  let inOnTrigger = false;
  let lastTriggeredAt = 0;
  let lastTriggeredTarget: EventTarget | null = null;
  let sessionListenersBound = false;
  let clickSuppressListenersBound = false;

  function clearPressTimer() {
    if (pressTimer !== null) {
      clearTimeout(pressTimer);
      pressTimer = null;
    }
  }

  function clearAutoDismissTimer() {
    if (autoDismissTimer !== null) {
      clearTimeout(autoDismissTimer);
      autoDismissTimer = null;
    }
  }

  function disarmClickSuppressor() {
    if (clickSuppressTimer !== null) {
      clearTimeout(clickSuppressTimer);
      clickSuppressTimer = null;
    }
    clickSuppressTarget = null;
    if (clickSuppressListenersBound && typeof window !== "undefined") {
      clickSuppressListenersBound = false;
      window.removeEventListener("mousedown", handleSuppressedMouseDown, true);
      window.removeEventListener("click", handleSuppressedClick, true);
      window.removeEventListener(
        "pointerdown",
        handleNextPointerDisarmsClickSuppressor,
        true,
      );
      window.removeEventListener(
        "touchstart",
        handleNextPointerDisarmsClickSuppressor,
        true,
      );
    }
  }

  function handleSuppressedMouseDown(event: MouseEvent) {
    if (!clickSuppressTarget) {
      disarmClickSuppressor();
      return;
    }
    if (targetMatchesNode(clickSuppressTarget, event.target)) {
      event.preventDefault();
      event.stopImmediatePropagation();
    }
  }

  function handleSuppressedClick(event: MouseEvent) {
    if (!clickSuppressTarget) {
      disarmClickSuppressor();
      return;
    }
    if (targetMatchesNode(clickSuppressTarget, event.target)) {
      event.preventDefault();
      event.stopImmediatePropagation();
      disarmClickSuppressor();
    }
  }

  function handleNextPointerDisarmsClickSuppressor() {
    disarmClickSuppressor();
  }

  function armClickSuppressor(target: EventTarget | null) {
    disarmClickSuppressor();
    if (!target || typeof window === "undefined") return;
    clickSuppressTarget = target;
    clickSuppressListenersBound = true;
    window.addEventListener("mousedown", handleSuppressedMouseDown, true);
    window.addEventListener("click", handleSuppressedClick, true);
    window.addEventListener(
      "pointerdown",
      handleNextPointerDisarmsClickSuppressor,
      { capture: true, passive: true },
    );
    window.addEventListener(
      "touchstart",
      handleNextPointerDisarmsClickSuppressor,
      { capture: true, passive: true },
    );
    clickSuppressTimer = setTimeout(() => {
      lastTriggeredAt = 0;
      lastTriggeredTarget = null;
      disarmClickSuppressor();
    }, clickSuppressMs);
  }

  function detachSessionListeners() {
    if (!sessionListenersBound || typeof window === "undefined") return;
    sessionListenersBound = false;
    window.removeEventListener("touchstart", handleWindowTouchStart, true);
    window.removeEventListener("touchmove", handleTouchMove, true);
    window.removeEventListener("touchend", handleTouchEnd, true);
    window.removeEventListener("touchcancel", handleTouchCancel, true);
    window.removeEventListener("scroll", handleTouchScroll, true);
  }

  function handleWindowTouchStart(event: TouchEvent) {
    if (activeTouchId === null) return;
    if (!event.touches || event.touches.length > 1) {
      if (triggeredForCurrentPress) {
        dismissOpenTooltipForActivePress();
      } else {
        abortPress(true);
      }
    }
  }

  function dismissOpenTooltipForActivePress() {
    if (!openFromLongPress) return;
    openFromLongPress = false;
    clearAutoDismissTimer();
    options.onDismiss();
  }

  function handleTouchMove(event: TouchEvent) {
    if (activeTouchId === null) return;
    const touch = findTouchById(event.touches, activeTouchId);
    if (!touch) {
      const wasTriggered = triggeredForCurrentPress;
      const target = pressTarget;
      abortPress(true);
      if (wasTriggered) {
        armClickSuppressor(target);
      }
      return;
    }
    if (event.touches.length !== 1) {
      if (triggeredForCurrentPress) {
        dismissOpenTooltipForActivePress();
      } else {
        abortPress(true);
      }
      return;
    }
    const dx = touch.clientX - startX;
    const dy = touch.clientY - startY;
    if (Math.hypot(dx, dy) > moveTolerancePx) {
      if (triggeredForCurrentPress) {
        dismissOpenTooltipForActivePress();
      } else {
        abortPress(true);
      }
    }
  }

  function handleTouchScroll() {
    if (triggeredForCurrentPress && activeTouchId !== null) {
      dismissOpenTooltipForActivePress();
      return;
    }
    abortPress(true);
  }

  function handleTouchEnd(event: TouchEvent) {
    if (activeTouchId === null) return;
    const stillActive = findTouchById(event.touches, activeTouchId);
    if (stillActive) return;

    const wasTriggered = triggeredForCurrentPress;
    const target = pressTarget;
    clearPressTimer();
    detachSessionListeners();
    activeTouchId = null;
    pressTarget = null;
    triggeredForCurrentPress = false;

    if (wasTriggered) {
      armClickSuppressor(target);
      clearAutoDismissTimer();
      if (openFromLongPress && autoDismissMs > 0) {
        autoDismissTimer = setTimeout(() => {
          autoDismissTimer = null;
          if (openFromLongPress) {
            openFromLongPress = false;
            options.onDismiss();
          }
        }, autoDismissMs);
      }
    }
  }

  function handleTouchCancel() {
    abortPress(true);
  }

  function abortPress(dismissIfOpen: boolean) {
    clearPressTimer();
    detachSessionListeners();
    activeTouchId = null;
    pressTarget = null;
    triggeredForCurrentPress = false;
    if (dismissIfOpen && openFromLongPress) {
      openFromLongPress = false;
      clearAutoDismissTimer();
      options.onDismiss();
    }
  }

  function start(event: TouchEvent, targetOverride?: EventTarget | null) {
    ensureColabOneTooltipInputTracker();
    handleGlobalTouchActivity();
    if (!event.touches || event.touches.length !== 1) {
      abortPress(false);
      return;
    }

    const touch = event.touches[0];
    const resolvedTarget = targetOverride ?? event.currentTarget ?? event.target;
    if (!options.canTrigger(resolvedTarget)) {
      abortPress(false);
      return;
    }
    clearPressTimer();
    clearAutoDismissTimer();
    detachSessionListeners();

    activeTouchId = touch.identifier;
    startX = touch.clientX;
    startY = touch.clientY;
    pressTarget = resolvedTarget;
    triggeredForCurrentPress = false;

    if (typeof window !== "undefined") {
      sessionListenersBound = true;
      window.addEventListener("touchstart", handleWindowTouchStart, {
        capture: true,
        passive: true,
      });
      window.addEventListener("touchmove", handleTouchMove, {
        capture: true,
        passive: true,
      });
      window.addEventListener("touchend", handleTouchEnd, {
        capture: true,
        passive: true,
      });
      window.addEventListener("touchcancel", handleTouchCancel, {
        capture: true,
        passive: true,
      });
      window.addEventListener("scroll", handleTouchScroll, {
        capture: true,
        passive: true,
      });
    }

    pressTimer = setTimeout(() => {
      pressTimer = null;
      if (activeTouchId === null || !options.canTrigger(pressTarget)) {
        abortPress(false);
        return;
      }
      if (
        typeof Node !== "undefined" &&
        pressTarget instanceof Node &&
        !pressTarget.isConnected
      ) {
        abortPress(false);
        return;
      }
      const triggeredTarget = pressTarget;
      triggeredForCurrentPress = true;
      openFromLongPress = true;
      lastTriggeredAt = currentTimeMs();
      lastTriggeredTarget = triggeredTarget;
      let accepted: boolean | void;
      inOnTrigger = true;
      try {
        accepted = options.onTrigger(triggeredTarget);
      } finally {
        inOnTrigger = false;
      }
      if (accepted === false) {
        triggeredForCurrentPress = false;
        openFromLongPress = false;
        lastTriggeredAt = 0;
        lastTriggeredTarget = null;
        abortPress(false);
      }
    }, delayMs);
  }

  function shouldSuppressContextMenu(target?: EventTarget | null): boolean {
    if (activeTouchId !== null && pressTarget !== null) {
      return !target || targetMatchesNode(pressTarget, target);
    }
    if (lastTriggeredAt > 0) {
      if (currentTimeMs() - lastTriggeredAt < clickSuppressMs) {
        return !target || targetMatchesNode(lastTriggeredTarget, target);
      }
      lastTriggeredAt = 0;
      lastTriggeredTarget = null;
    }
    return false;
  }

  function isLongPressOpen(): boolean {
    return openFromLongPress;
  }

  function notifyDismissed(): void {
    openFromLongPress = false;
    if (inOnTrigger) {
      triggeredForCurrentPress = false;
    }
    clearAutoDismissTimer();
  }

  function cancel(): void {
    abortPress(false);
    openFromLongPress = false;
    lastTriggeredAt = 0;
    lastTriggeredTarget = null;
    clearAutoDismissTimer();
    disarmClickSuppressor();
  }

  return {
    start,
    shouldSuppressContextMenu,
    isLongPressOpen,
    cancel,
    notifyDismissed,
  };
}
