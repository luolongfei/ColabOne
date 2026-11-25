import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  COLABONE_WINDOW_ACTIVATION_FOCUS_WINDOW_MS,
  installColabOneWindowFocusGuard,
  isColabOneKeyboardFocusIntent,
  isColabOneTextEntryElement,
  type ColabOneFocusGuardDocumentLike,
  type ColabOneFocusGuardWindowLike,
  type ColabOneFocusableElementLike,
} from "./colabOneFocusGuard";

interface FakeElement extends ColabOneFocusableElementLike {
  id: string;
  attrs: Record<string, string>;
  matchesClosest: string[];
  display?: string;
  visibility?: string;
  blurCalls: number;
  ignoreNextBlurs: number;
}

function createHarness(opts: {
  initiallyFocused?: boolean;
  initiallyHidden?: boolean;
  elements?: FakeElement[];
} = {}) {
  let clockMs = 1000;
  let focused = opts.initiallyFocused ?? false;
  let hidden = opts.initiallyHidden ?? false;
  const elements: FakeElement[] = opts.elements ?? [];
  const bodyAttrs: Record<string, string> = {};
  const body = {
    id: "body",
    tagName: "BODY",
    focusCalls: 0,
    blurCalls: 0,
    focus: vi.fn(() => {
      body.focusCalls += 1;
      activeElement = body;
    }),
    blur: vi.fn(() => {
      body.blurCalls += 1;
    }),
    getAttribute: (name: string) =>
      Object.prototype.hasOwnProperty.call(bodyAttrs, name)
        ? bodyAttrs[name]
        : null,
    setAttribute: (name: string, value: string) => {
      bodyAttrs[name] = value;
    },
    removeAttribute: (name: string) => {
      delete bodyAttrs[name];
    },
    hasAttribute: (name: string) =>
      Object.prototype.hasOwnProperty.call(bodyAttrs, name),
  };
  const documentElement = { id: "html", tagName: "HTML", blur: vi.fn() };
  let activeElement: unknown = body;

  const winListeners = new Map<string, Set<(ev: any) => void>>();
  const docListeners = new Map<string, Set<(ev: any) => void>>();
  const microtasks: Array<() => void> = [];

  function addListener(
    map: Map<string, Set<(ev: any) => void>>,
    type: string,
    fn: (ev: any) => void,
  ) {
    let set = map.get(type);
    if (!set) {
      set = new Set();
      map.set(type, set);
    }
    set.add(fn);
  }

  function removeListener(
    map: Map<string, Set<(ev: any) => void>>,
    type: string,
    fn: (ev: any) => void,
  ) {
    map.get(type)?.delete(fn);
  }

  function emit(
    map: Map<string, Set<(ev: any) => void>>,
    type: string,
    ev: any,
  ) {
    for (const fn of Array.from(map.get(type) ?? [])) {
      fn(ev);
    }
  }

  const win: ColabOneFocusGuardWindowLike = {
    addEventListener(type, listener) {
      addListener(winListeners, type, listener);
    },
    removeEventListener(type, listener) {
      removeListener(winListeners, type, listener);
    },
    getComputedStyle(el: FakeElement) {
      return {
        display: el.display ?? "inline-flex",
        visibility: el.visibility ?? "visible",
      };
    },
  };

  const doc: ColabOneFocusGuardDocumentLike = {
    get activeElement() {
      return activeElement;
    },
    body,
    documentElement,
    get hidden() {
      return hidden;
    },
    hasFocus: () => focused,
    querySelectorAll: () => elements,
    addEventListener(type, listener) {
      addListener(docListeners, type, listener);
    },
    removeEventListener(type, listener) {
      removeListener(docListeners, type, listener);
    },
  };

  function makeElement(
    id: string,
    config: {
      tagName?: string;
      type?: string;
      name?: string;
      checked?: boolean;
      disabled?: boolean;
      isContentEditable?: boolean;
      attrs?: Record<string, string>;
      matchesClosest?: string[];
      parentElement?: FakeElement | null;
      display?: string;
      visibility?: string;
      ignoreNextBlurs?: number;
    } = {},
  ): FakeElement {
    const attrs = { ...(config.attrs ?? {}) };
    const el: FakeElement = {
      id,
      tagName: config.tagName ?? "BUTTON",
      type: config.type,
      name: config.name,
      checked: config.checked,
      disabled: config.disabled,
      isContentEditable: config.isContentEditable,
      parentElement: config.parentElement ?? null,
      display: config.display,
      visibility: config.visibility,
      attrs,
      matchesClosest: config.matchesClosest ?? [],
      blurCalls: 0,
      ignoreNextBlurs: config.ignoreNextBlurs ?? 0,
      getAttribute(name: string) {
        return Object.prototype.hasOwnProperty.call(attrs, name)
          ? attrs[name]
          : null;
      },
      hasAttribute(name: string) {
        return Object.prototype.hasOwnProperty.call(attrs, name);
      },
      closest(selector: string) {
        const parts = selector.split(",").map((s) => s.trim());
        for (const p of parts) {
          if (el.matchesClosest.includes(p)) return el;
        }
        return null;
      },
      blur() {
        el.blurCalls += 1;
        if (el.ignoreNextBlurs > 0) {
          el.ignoreNextBlurs -= 1;
          return;
        }
        if (activeElement === el) {
          activeElement = body;
        }
      },
    };
    return el;
  }

  function focusElement(el: FakeElement, relatedTarget: unknown = null) {
    activeElement = el;
    emit(docListeners, "focusin", { target: el, relatedTarget });
  }

  function flushMicrotasks() {
    while (microtasks.length > 0) {
      const fn = microtasks.shift();
      fn?.();
    }
  }

  const guard = installColabOneWindowFocusGuard({
    win,
    doc,
    now: () => clockMs,
    scheduleMicrotask: (cb) => {
      microtasks.push(cb);
    },
  });

  return {
    win,
    doc,
    body,
    elements,
    guard,
    makeElement,
    focusElement,
    flushMicrotasks,
    getActiveElement: () => activeElement,
    setActiveElement: (el: unknown) => {
      activeElement = el;
    },
    advanceNow: (ms: number) => {
      clockMs += ms;
    },
    emitWindowFocus: () => {
      focused = true;
      hidden = false;
      emit(winListeners, "focus", { target: win });
    },
    emitWindowBlur: () => {
      focused = false;
      emit(winListeners, "blur", { target: win });
    },
    emitKeyDown: (ev: {
      key: string;
      metaKey?: boolean;
      ctrlKey?: boolean;
      altKey?: boolean;
      shiftKey?: boolean;
    }) => {
      emit(winListeners, "keydown", ev);
    },
    emitPointerDown: () => {
      emit(winListeners, "pointerdown", {});
    },
    listenerCount: () => {
      let total = 0;
      for (const s of winListeners.values()) total += s.size;
      for (const s of docListeners.values()) total += s.size;
      return total;
    },
  };
}

describe("ColabOne window initial-focus guard", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("clears automatic WKWebView initial focus on the first brand button after splash reveal", () => {
    const h = createHarness({ initiallyFocused: false, initiallyHidden: true });
    const brandBtn = h.makeElement("co-brand");
    const versionBtn = h.makeElement("co-brand-version");
    h.elements.push(brandBtn, versionBtn);

    // Vue mounts while main window is hidden behind native splash.
    h.guard.onMounted();
    h.advanceNow(1200);

    // Splash reveals main window -> window gains focus -> WebKit focuses first button.
    h.emitWindowFocus();
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();

    expect(brandBtn.blurCalls).toBe(1);
    expect(h.getActiveElement()).toBe(h.body);
    expect(h.body.focus).toHaveBeenCalledWith({ preventScroll: true });
    expect(h.body.hasAttribute("tabindex")).toBe(false);

    // Secondary Focus() after splash fade-out destroy (~230ms later) also stays clean.
    h.advanceNow(230);
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();

    expect(brandBtn.blurCalls).toBe(2);
    expect(h.getActiveElement()).toBe(h.body);
  });

  it("handles focusin arriving before window focus during initial splash reveal", () => {
    const h = createHarness({ initiallyFocused: false, initiallyHidden: true });
    const brandBtn = h.makeElement("co-brand");
    h.elements.push(brandBtn);

    h.advanceNow(1200);
    h.focusElement(brandBtn, null);
    h.emitWindowFocus();
    h.flushMicrotasks();

    expect(brandBtn.blurCalls).toBe(1);
    expect(h.getActiveElement()).toBe(h.body);
  });

  it("clears initial auto-focus on windows that open already visible (such as standalone log window)", () => {
    const h = createHarness({ initiallyFocused: true, initiallyHidden: false });
    const firstRadio = h.makeElement("seg-all", {
      tagName: "INPUT",
      type: "radio",
      name: "log-level",
      checked: true,
    });
    const secondRadio = h.makeElement("seg-err", {
      tagName: "INPUT",
      type: "radio",
      name: "log-level",
      checked: false,
    });
    const copyBtn = h.makeElement("copy-btn");
    h.elements.push(firstRadio, secondRadio, copyBtn);

    // Simulate WebKit already having placed focus on the first control before Vue mount finishes.
    h.setActiveElement(firstRadio);
    h.guard.onMounted();
    h.flushMicrotasks();

    expect(firstRadio.blurCalls).toBe(1);
    expect(h.getActiveElement()).toBe(h.body);
  });

  it("uses next-turn timer fallback when WebKit ignores microtask blur during focus reentrancy", () => {
    const h = createHarness({ initiallyFocused: false });
    const brandBtn = h.makeElement("co-brand", { ignoreNextBlurs: 1 });
    h.elements.push(brandBtn);

    h.emitWindowFocus();
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();

    // First blur attempt was ignored by WebKit reentrancy guard.
    expect(brandBtn.blurCalls).toBe(1);
    expect(h.getActiveElement()).toBe(brandBtn);

    // Next-turn fallback completes the blur.
    vi.runAllTimers();
    expect(brandBtn.blurCalls).toBe(2);
    expect(h.getActiveElement()).toBe(h.body);
  });

  it("preserves genuine Tab keyboard navigation immediately after window reveal", () => {
    const h = createHarness({ initiallyFocused: false });
    const brandBtn = h.makeElement("co-brand");
    h.elements.push(brandBtn);

    // Initial auto-focus on reveal is cleared.
    h.emitWindowFocus();
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();
    expect(h.getActiveElement()).toBe(h.body);

    // 40ms later, user presses Tab -> must keep focus on brandBtn with :focus-visible.
    h.advanceNow(40);
    h.emitKeyDown({ key: "Tab" });
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();
    vi.runAllTimers();

    expect(h.getActiveElement()).toBe(brandBtn);
    expect(brandBtn.blurCalls).toBe(1);
  });

  it("preserves pointer click focus on the brand button immediately after reveal", () => {
    const h = createHarness({ initiallyFocused: false });
    const brandBtn = h.makeElement("co-brand");
    h.elements.push(brandBtn);

    h.emitWindowFocus();
    h.advanceNow(20);
    h.emitPointerDown();
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();

    expect(h.getActiveElement()).toBe(brandBtn);
    expect(brandBtn.blurCalls).toBe(0);
  });

  it("does not let bare Meta/Shift/Alt/Control keys (e.g. Cmd+Tab) disable suppression", () => {
    const h = createHarness({ initiallyFocused: true });
    const brandBtn = h.makeElement("co-brand");
    h.elements.push(brandBtn);

    h.advanceNow(COLABONE_WINDOW_ACTIVATION_FOCUS_WINDOW_MS + 100);
    // Nothing focused before leaving ColabOne.
    h.emitWindowBlur();
    // Returning via Cmd+Tab delivers a bare Meta keydown at most.
    h.emitKeyDown({ key: "Meta", metaKey: true });
    h.emitWindowFocus();
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();

    expect(h.getActiveElement()).toBe(h.body);
    expect(brandBtn.blurCalls).toBe(1);
  });

  it("preserves previously focused control across app switch blur and focus", () => {
    const h = createHarness({ initiallyFocused: true });
    const brandBtn = h.makeElement("co-brand");
    h.elements.push(brandBtn);

    // User intentionally tabbed to brandBtn.
    h.emitKeyDown({ key: "Tab" });
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();
    expect(h.getActiveElement()).toBe(brandBtn);

    // Switch away and back after grace window expires.
    h.advanceNow(2000);
    h.emitWindowBlur();
    h.advanceNow(1000);
    h.emitWindowFocus();
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();

    expect(h.getActiveElement()).toBe(brandBtn);
    expect(brandBtn.blurCalls).toBe(0);
  });

  it("never suppresses text inputs, dialog controls, autofocus controls, or non-first buttons", () => {
    const h = createHarness({ initiallyFocused: false });
    const brandBtn = h.makeElement("co-brand");
    const secondBtn = h.makeElement("second-btn");
    const dialogBtn = h.makeElement("close-confirm", {
      matchesClosest: ['[role="dialog"]', ".ant-modal"],
    });
    const passwordInput = h.makeElement("pw", {
      tagName: "INPUT",
      type: "password",
    });
    h.elements.push(brandBtn, secondBtn, dialogBtn, passwordInput);

    h.emitWindowFocus();

    // Non-first button (e.g. programmatic focus) stays focused.
    h.focusElement(secondBtn, null);
    h.flushMicrotasks();
    expect(h.getActiveElement()).toBe(secondBtn);

    // Dialog confirm button (e.g. native close button modal) stays focused.
    h.focusElement(dialogBtn, null);
    h.flushMicrotasks();
    expect(h.getActiveElement()).toBe(dialogBtn);

    // Text/password input stays focused even if it were first.
    h.elements.splice(0, h.elements.length, passwordInput);
    h.focusElement(passwordInput, null);
    h.flushMicrotasks();
    expect(h.getActiveElement()).toBe(passwordInput);

    // Explicit autofocus button stays focused.
    const autoBtn = h.makeElement("auto-btn", { attrs: { autofocus: "" } });
    h.elements.splice(0, h.elements.length, autoBtn);
    h.focusElement(autoBtn, null);
    h.flushMicrotasks();
    expect(h.getActiveElement()).toBe(autoBtn);
  });

  it("respects hidden ancestors and checked radio tab order when finding the first tabbable control", () => {
    const h = createHarness({ initiallyFocused: false });
    const hiddenParent = h.makeElement("hidden-wrap", {
      tagName: "DIV",
      display: "none",
    });
    const hiddenBtn = h.makeElement("hidden-btn", {
      parentElement: hiddenParent,
    });
    const uncheckedRadio = h.makeElement("radio-1", {
      tagName: "INPUT",
      type: "radio",
      name: "grp",
      checked: false,
    });
    const checkedRadio = h.makeElement("radio-2", {
      tagName: "INPUT",
      type: "radio",
      name: "grp",
      checked: true,
    });
    h.elements.push(hiddenBtn, uncheckedRadio, checkedRadio);

    h.emitWindowFocus();
    h.focusElement(checkedRadio, null);
    h.flushMicrotasks();

    expect(checkedRadio.blurCalls).toBe(1);
    expect(h.getActiveElement()).toBe(h.body);
  });

  it("classifies text-entry elements and keyboard focus intent accurately", () => {
    const h = createHarness();
    expect(
      isColabOneTextEntryElement(
        h.makeElement("t", { tagName: "INPUT", type: "text" }),
      ),
    ).toBe(true);
    expect(
      isColabOneTextEntryElement(
        h.makeElement("p", { tagName: "INPUT", type: "password" }),
      ),
    ).toBe(true);
    expect(
      isColabOneTextEntryElement(h.makeElement("ta", { tagName: "TEXTAREA" })),
    ).toBe(true);
    expect(
      isColabOneTextEntryElement(
        h.makeElement("ce", { tagName: "DIV", isContentEditable: true }),
      ),
    ).toBe(true);
    expect(
      isColabOneTextEntryElement(
        h.makeElement("r", { tagName: "INPUT", type: "radio" }),
      ),
    ).toBe(false);
    expect(
      isColabOneTextEntryElement(h.makeElement("b", { tagName: "BUTTON" })),
    ).toBe(false);

    expect(isColabOneKeyboardFocusIntent({ key: "Tab" })).toBe(true);
    expect(isColabOneKeyboardFocusIntent({ key: "Tab", metaKey: true })).toBe(
      true,
    );
    expect(isColabOneKeyboardFocusIntent({ key: "ArrowDown" })).toBe(true);
    expect(isColabOneKeyboardFocusIntent({ key: "Meta", metaKey: true })).toBe(
      false,
    );
    expect(isColabOneKeyboardFocusIntent({ key: "j", metaKey: true })).toBe(
      false,
    );
  });

  it("cleans up listeners and cancels pending fallback timers on dispose", () => {
    const h = createHarness({ initiallyFocused: false });
    const brandBtn = h.makeElement("co-brand", { ignoreNextBlurs: 1 });
    h.elements.push(brandBtn);

    expect(h.listenerCount()).toBeGreaterThan(0);

    h.emitWindowFocus();
    h.focusElement(brandBtn, null);
    h.flushMicrotasks();
    expect(brandBtn.blurCalls).toBe(1);

    h.guard.dispose();
    expect(h.listenerCount()).toBe(0);

    vi.runAllTimers();
    expect(brandBtn.blurCalls).toBe(1);
  });
});
