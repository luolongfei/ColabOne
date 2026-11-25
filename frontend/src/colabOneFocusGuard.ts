export const COLABONE_WINDOW_ACTIVATION_FOCUS_WINDOW_MS = 600;
export const COLABONE_USER_INPUT_FOCUS_GRACE_MS = 400;

const COLABONE_TABBABLE_SELECTOR = [
  "a[href]",
  "area[href]",
  "button",
  "input",
  "select",
  "textarea",
  "summary",
  "[tabindex]",
  "[contenteditable]",
].join(", ");

const COLABONE_OVERLAY_DIALOG_SELECTOR = [
  '[role="dialog"]',
  '[role="alertdialog"]',
  ".ant-modal",
  ".ant-modal-wrap",
  ".ant-drawer",
  ".ant-popover",
].join(", ");

const COLABONE_NON_TEXT_INPUT_TYPES = new Set([
  "button",
  "checkbox",
  "color",
  "file",
  "image",
  "radio",
  "range",
  "reset",
  "submit",
]);

const COLABONE_IGNORED_MODIFIER_KEYS = new Set([
  "Alt",
  "AltGraph",
  "CapsLock",
  "Control",
  "Dead",
  "Meta",
  "NumLock",
  "Process",
  "ScrollLock",
  "Shift",
  "Unidentified",
]);

export interface ColabOneFocusableElementLike {
  tagName?: string;
  type?: string;
  name?: string;
  checked?: boolean;
  disabled?: boolean;
  isContentEditable?: boolean;
  parentElement?: ColabOneFocusableElementLike | null;
  focus?: (options?: { preventScroll?: boolean }) => void;
  blur?: () => void;
  getAttribute?: (name: string) => string | null;
  setAttribute?: (name: string, value: string) => void;
  removeAttribute?: (name: string) => void;
  hasAttribute?: (name: string) => boolean;
  closest?: (selector: string) => unknown;
}

export interface ColabOneFocusGuardWindowLike {
  addEventListener: (
    type: string,
    listener: (event: any) => void,
    options?: boolean | AddEventListenerOptions,
  ) => void;
  removeEventListener: (
    type: string,
    listener: (event: any) => void,
    options?: boolean | EventListenerOptions,
  ) => void;
  getComputedStyle?: (
    el: any,
  ) => { display?: string; visibility?: string } | null;
}

export interface ColabOneFocusGuardDocumentLike {
  activeElement: unknown;
  body?: unknown;
  documentElement?: unknown;
  hidden?: boolean;
  hasFocus?: () => boolean;
  querySelectorAll?: (selector: string) => ArrayLike<unknown>;
  addEventListener: (
    type: string,
    listener: (event: any) => void,
    options?: boolean | AddEventListenerOptions,
  ) => void;
  removeEventListener: (
    type: string,
    listener: (event: any) => void,
    options?: boolean | EventListenerOptions,
  ) => void;
}

export interface ColabOneWindowFocusGuardOptions {
  win?: ColabOneFocusGuardWindowLike;
  doc?: ColabOneFocusGuardDocumentLike;
  now?: () => number;
  activationWindowMs?: number;
  userInputGraceMs?: number;
  scheduleMicrotask?: (cb: () => void) => void;
  setTimer?: (cb: () => void, ms: number) => ReturnType<typeof setTimeout>;
  clearTimer?: (id: ReturnType<typeof setTimeout>) => void;
}

export interface ColabOneWindowFocusGuard {
  /**
   * Call once after the root Vue component mounts so windows created already
   * visible (e.g. standalone log window or re-opened main window) also clear
   * any automatic WebKit initial focus without waiting for a new focus event.
   */
  onMounted: () => void;
  /** Unregisters all listeners and cancels pending fallback timers. */
  dispose: () => void;
}

function toFocusableElement(node: unknown): ColabOneFocusableElementLike | null {
  if (!node || typeof node !== "object") return null;
  const candidate = node as ColabOneFocusableElementLike;
  if (typeof candidate.blur !== "function") return null;
  return candidate;
}

function isControlElement(
  node: unknown,
  doc: ColabOneFocusGuardDocumentLike,
): node is ColabOneFocusableElementLike {
  const el = toFocusableElement(node);
  if (!el) return false;
  if (el === doc.body || el === doc.documentElement) return false;
  return true;
}

function readInputType(el: ColabOneFocusableElementLike): string {
  const raw = el.type || el.getAttribute?.("type") || "text";
  return String(raw).trim().toLowerCase() || "text";
}

function isEditableContentAttribute(el: ColabOneFocusableElementLike): boolean {
  if (el.isContentEditable === true) return true;
  const attr = el.getAttribute?.("contenteditable");
  return typeof attr === "string" && attr.trim().toLowerCase() !== "false";
}

export function isColabOneTextEntryElement(
  node: unknown,
): boolean {
  const el = toFocusableElement(node);
  if (!el) return false;
  const tag = String(el.tagName || "").toUpperCase();
  if (tag === "TEXTAREA") return true;
  if (isEditableContentAttribute(el)) return true;
  if (tag === "INPUT") {
    return !COLABONE_NON_TEXT_INPUT_TYPES.has(readInputType(el));
  }
  return false;
}

export function isColabOneKeyboardFocusIntent(event: {
  key?: string;
  metaKey?: boolean;
  ctrlKey?: boolean;
}): boolean {
  const key = String(event?.key || "");
  if (!key) return false;
  if (key === "Tab") return true;
  if (COLABONE_IGNORED_MODIFIER_KEYS.has(key)) return false;
  if (event.metaKey || event.ctrlKey) return false;
  return true;
}

function isElementVisibleForTabOrder(
  el: ColabOneFocusableElementLike,
  win: ColabOneFocusGuardWindowLike,
): boolean {
  if (el.closest?.("[hidden], [inert], fieldset[disabled]")) {
    return false;
  }
  if (typeof win.getComputedStyle !== "function") {
    return true;
  }
  let node: ColabOneFocusableElementLike | null | undefined = el;
  while (node) {
    const style = win.getComputedStyle(node);
    if (style) {
      if (style.display === "none") return false;
      if (
        node === el &&
        (style.visibility === "hidden" || style.visibility === "collapse")
      ) {
        return false;
      }
    }
    node = node.parentElement ?? null;
  }
  return true;
}

function isElementTabbable(
  el: ColabOneFocusableElementLike,
  win: ColabOneFocusGuardWindowLike,
): boolean {
  if (el.disabled === true || el.hasAttribute?.("disabled")) {
    return false;
  }
  const tag = String(el.tagName || "").toUpperCase();
  if (tag === "INPUT" && readInputType(el) === "hidden") {
    return false;
  }
  const tabIndexAttr = el.getAttribute?.("tabindex");
  if (tabIndexAttr !== null && tabIndexAttr !== undefined) {
    const parsed = Number.parseInt(tabIndexAttr.trim(), 10);
    if (Number.isNaN(parsed) || parsed < 0) {
      return false;
    }
  } else {
    const nativelyFocusable =
      tag === "A" ||
      tag === "AREA" ||
      tag === "BUTTON" ||
      tag === "INPUT" ||
      tag === "SELECT" ||
      tag === "TEXTAREA" ||
      tag === "SUMMARY";
    if (!nativelyFocusable && !isEditableContentAttribute(el)) {
      return false;
    }
  }
  return isElementVisibleForTabOrder(el, win);
}

function findFirstTabbableElement(
  doc: ColabOneFocusGuardDocumentLike,
  win: ColabOneFocusGuardWindowLike,
): ColabOneFocusableElementLike | null {
  if (typeof doc.querySelectorAll !== "function") {
    return null;
  }
  const candidates = doc.querySelectorAll(COLABONE_TABBABLE_SELECTOR);
  let checkedRadioNames: Set<string> | null = null;

  for (let i = 0; i < candidates.length; i += 1) {
    const el = toFocusableElement(candidates[i]);
    if (!el || !isElementTabbable(el, win)) continue;

    const tag = String(el.tagName || "").toUpperCase();
    if (tag === "INPUT" && readInputType(el) === "radio") {
      const radioName = String(el.name || el.getAttribute?.("name") || "");
      if (radioName && !el.checked) {
        if (!checkedRadioNames) {
          checkedRadioNames = new Set<string>();
          for (let j = 0; j < candidates.length; j += 1) {
            const other = toFocusableElement(candidates[j]);
            if (
              other &&
              String(other.tagName || "").toUpperCase() === "INPUT" &&
              readInputType(other) === "radio" &&
              other.checked &&
              isElementTabbable(other, win)
            ) {
              const otherName = String(
                other.name || other.getAttribute?.("name") || "",
              );
              if (otherName) checkedRadioNames.add(otherName);
            }
          }
        }
        if (checkedRadioNames.has(radioName)) {
          continue;
        }
      }
    }

    return el;
  }
  return null;
}

/**
 * Prevents macOS AppKit / WKWebView window-activation key-view traversal from
 * spontaneously focusing and lighting up the `:focus-visible` ring on the
 * document's first non-text control (such as the top-left `.co-brand` button)
 * when a window opens or re-activates without keyboard or pointer input.
 *
 * Real user Tab navigation, pointer clicks, text inputs, and dialog/modal focus
 * management are always preserved.
 */
export function installColabOneWindowFocusGuard(
  options: ColabOneWindowFocusGuardOptions = {},
): ColabOneWindowFocusGuard {
  const win: ColabOneFocusGuardWindowLike | undefined =
    options.win ?? (typeof window !== "undefined" ? window : undefined);
  const doc: ColabOneFocusGuardDocumentLike | undefined =
    options.doc ?? (typeof document !== "undefined" ? document : undefined);

  if (!win || !doc) {
    return {
      onMounted() {},
      dispose() {},
    };
  }

  const targetWin = win;
  const targetDoc = doc;

  const now = options.now ?? (() => Date.now());
  const activationWindowMs = Math.max(
    0,
    options.activationWindowMs ?? COLABONE_WINDOW_ACTIVATION_FOCUS_WINDOW_MS,
  );
  const userInputGraceMs = Math.max(
    0,
    options.userInputGraceMs ?? COLABONE_USER_INPUT_FOCUS_GRACE_MS,
  );
  const scheduleMicrotask =
    options.scheduleMicrotask ??
    ((cb: () => void) => {
      if (typeof queueMicrotask === "function") {
        queueMicrotask(cb);
      } else {
        void Promise.resolve().then(cb);
      }
    });
  const setTimer = options.setTimer ?? ((cb, ms) => setTimeout(cb, ms));
  const clearTimer = options.clearTimer ?? ((id) => clearTimeout(id));

  let disposed = false;
  let resettingSequentialFocus = false;
  let hasUserInteracted = false;
  let userInputSeq = 0;
  let lastUserInputAt = -Infinity;
  let hadControlFocusBeforeBlur = false;
  let pendingBlurTarget: ColabOneFocusableElementLike | null = null;
  let pendingBlurGen = -1;
  let pendingFallbackTimer: ReturnType<typeof setTimeout> | null = null;

  const isInitiallyFocused =
    Boolean(targetDoc.hasFocus?.()) && !targetDoc.hidden;
  let awaitingInitialActivation = !isInitiallyFocused;
  let activationSuppressionUntil = isInitiallyFocused
    ? now() + activationWindowMs
    : 0;

  function clearPendingFallbackTimer() {
    if (pendingFallbackTimer !== null) {
      clearTimer(pendingFallbackTimer);
      pendingFallbackTimer = null;
    }
  }

  function hasRecentUserInput(): boolean {
    return now() - lastUserInputAt <= userInputGraceMs;
  }

  function isSuppressionArmed(): boolean {
    if (hasRecentUserInput()) return false;
    return awaitingInitialActivation || now() <= activationSuppressionUntil;
  }

  function markUserInteraction() {
    hasUserInteracted = true;
    userInputSeq += 1;
    lastUserInputAt = now();
    awaitingInitialActivation = false;
    activationSuppressionUntil = 0;
    pendingBlurTarget = null;
    pendingBlurGen = -1;
    clearPendingFallbackTimer();
  }

  function shouldSuppressTarget(
    node: unknown,
    relatedTarget: unknown,
  ): node is ColabOneFocusableElementLike {
    if (disposed || resettingSequentialFocus || !isSuppressionArmed()) {
      return false;
    }
    if (relatedTarget !== null && relatedTarget !== undefined) return false;
    if (!isControlElement(node, targetDoc)) return false;
    if (node.hasAttribute?.("autofocus")) return false;
    if (isColabOneTextEntryElement(node)) return false;
    if (node.closest?.(COLABONE_OVERLAY_DIALOG_SELECTOR)) return false;
    return findFirstTabbableElement(targetDoc, targetWin) === node;
  }

  function resetSequentialFocusStart() {
    const body = toFocusableElement(targetDoc.body);
    if (
      !body ||
      typeof body.focus !== "function" ||
      typeof body.setAttribute !== "function" ||
      typeof body.removeAttribute !== "function"
    ) {
      return;
    }
    const hadTabIndex = Boolean(body.hasAttribute?.("tabindex"));
    const prevTabIndex = hadTabIndex ? body.getAttribute?.("tabindex") : null;
    resettingSequentialFocus = true;
    try {
      if (!hadTabIndex) {
        body.setAttribute("tabindex", "-1");
      }
      body.focus({ preventScroll: true });
      body.blur?.();
    } finally {
      if (!hadTabIndex) {
        body.removeAttribute("tabindex");
      } else if (prevTabIndex !== null && prevTabIndex !== undefined) {
        body.setAttribute("tabindex", prevTabIndex);
      }
      resettingSequentialFocus = false;
    }
  }

  function clearFocusFromTarget(target: ColabOneFocusableElementLike): boolean {
    if (targetDoc.activeElement === target) {
      target.blur?.();
    }
    if (targetDoc.activeElement === target) {
      return false;
    }
    // Reset the HTML sequential focus navigation starting point back to
    // document.body so the user's very first Tab press lands on the first
    // interactive control instead of skipping past it.
    resetSequentialFocusStart();
    return true;
  }

  function scheduleBlur(target: ColabOneFocusableElementLike) {
    const gen = userInputSeq;
    if (pendingBlurTarget === target && pendingBlurGen === gen) {
      return;
    }
    pendingBlurTarget = target;
    pendingBlurGen = gen;
    clearPendingFallbackTimer();

    // Defer via microtask so WebKit's Document::setFocusedElement finishes its
    // internal transition cleanly before blur() runs (still before next paint).
    scheduleMicrotask(() => {
      if (disposed || userInputSeq !== gen) {
        if (pendingBlurTarget === target) {
          pendingBlurTarget = null;
          pendingBlurGen = -1;
        }
        return;
      }
      const cleared = clearFocusFromTarget(target);
      if (!cleared) {
        pendingFallbackTimer = setTimer(() => {
          pendingFallbackTimer = null;
          if (pendingBlurTarget === target) {
            pendingBlurTarget = null;
            pendingBlurGen = -1;
          }
          if (disposed || userInputSeq !== gen) return;
          clearFocusFromTarget(target);
        }, 0);
      } else if (pendingBlurTarget === target) {
        pendingBlurTarget = null;
        pendingBlurGen = -1;
      }
    });
  }

  function checkActiveElement() {
    const active = targetDoc.activeElement;
    if (shouldSuppressTarget(active, null)) {
      scheduleBlur(active);
    }
  }

  function onKeyDown(event: KeyboardEvent) {
    if (isColabOneKeyboardFocusIntent(event)) {
      markUserInteraction();
    }
  }

  function onPointerDown() {
    markUserInteraction();
  }

  function onWindowBlur(event: Event) {
    if (event?.target && event.target !== targetWin) return;
    const active = targetDoc.activeElement;
    hadControlFocusBeforeBlur =
      isControlElement(active, targetDoc) && active !== pendingBlurTarget;
  }

  function onWindowFocus(event: Event) {
    if (event?.target && event.target !== targetWin) return;
    if (awaitingInitialActivation) {
      awaitingInitialActivation = false;
      if (!hasRecentUserInput()) {
        activationSuppressionUntil = Math.max(
          activationSuppressionUntil,
          now() + activationWindowMs,
        );
        checkActiveElement();
      }
      return;
    }
    if (!hadControlFocusBeforeBlur && !hasRecentUserInput()) {
      activationSuppressionUntil = Math.max(
        activationSuppressionUntil,
        now() + activationWindowMs,
      );
      checkActiveElement();
    }
  }

  function onVisibilityChange() {
    if (targetDoc.hidden || !awaitingInitialActivation || hasRecentUserInput()) {
      return;
    }
    if (targetDoc.hasFocus?.()) {
      awaitingInitialActivation = false;
      activationSuppressionUntil = Math.max(
        activationSuppressionUntil,
        now() + activationWindowMs,
      );
    }
    checkActiveElement();
  }

  function onFocusIn(event: FocusEvent) {
    const target = event?.target;
    const relatedTarget = event?.relatedTarget ?? null;
    if (shouldSuppressTarget(target, relatedTarget)) {
      scheduleBlur(target);
    }
  }

  targetWin.addEventListener("keydown", onKeyDown, true);
  targetWin.addEventListener("pointerdown", onPointerDown, true);
  targetWin.addEventListener("mousedown", onPointerDown, true);
  targetWin.addEventListener("touchstart", onPointerDown, true);
  targetWin.addEventListener("blur", onWindowBlur);
  targetWin.addEventListener("focus", onWindowFocus);
  targetDoc.addEventListener("visibilitychange", onVisibilityChange);
  targetDoc.addEventListener("focusin", onFocusIn, true);

  return {
    onMounted() {
      if (disposed || hasUserInteracted) return;
      if (Boolean(targetDoc.hasFocus?.()) && !targetDoc.hidden) {
        awaitingInitialActivation = false;
        activationSuppressionUntil = Math.max(
          activationSuppressionUntil,
          now() + activationWindowMs,
        );
        checkActiveElement();
      }
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      pendingBlurTarget = null;
      pendingBlurGen = -1;
      clearPendingFallbackTimer();
      targetWin.removeEventListener("keydown", onKeyDown, true);
      targetWin.removeEventListener("pointerdown", onPointerDown, true);
      targetWin.removeEventListener("mousedown", onPointerDown, true);
      targetWin.removeEventListener("touchstart", onPointerDown, true);
      targetWin.removeEventListener("blur", onWindowBlur);
      targetWin.removeEventListener("focus", onWindowFocus);
      targetDoc.removeEventListener("visibilitychange", onVisibilityChange);
      targetDoc.removeEventListener("focusin", onFocusIn, true);
    },
  };
}
