import { describe, expect, it } from "vitest";
import {
  COLABONE_SELECT_FULL_TITLE_ATTR,
  COLABONE_SELECT_SAVED_TITLE_ATTR,
  computeColabOneFloatingTooltipPosition,
  extractColabOneSelectHoverInfo,
  isColabOneElementHorizontallyOverflowing,
  restoreColabOneNativeTitles,
  stashColabOneNativeTitles,
} from "./colabOneSelectOverflow";

interface MockElementOptions {
  classes?: string[];
  attrs?: Record<string, string>;
  text?: string;
  clientWidth?: number;
  scrollWidth?: number;
  children?: MockElement[];
}

class MockElement {
  readonly classSet: Set<string>;
  readonly attrs: Map<string, string>;
  readonly children: MockElement[] = [];
  parentElement: MockElement | null = null;
  clientWidth: number;
  scrollWidth: number;
  private ownText: string;

  constructor(options: MockElementOptions = {}) {
    this.classSet = new Set(options.classes ?? []);
    this.attrs = new Map(Object.entries(options.attrs ?? {}));
    this.ownText = options.text ?? "";
    this.clientWidth = options.clientWidth ?? 0;
    this.scrollWidth = options.scrollWidth ?? 0;
    for (const child of options.children ?? []) {
      this.appendChild(child);
    }
  }

  appendChild(child: MockElement): MockElement {
    child.parentElement = this;
    this.children.push(child);
    return child;
  }

  get classList() {
    return {
      contains: (cls: string) => this.classSet.has(cls),
      add: (cls: string) => this.classSet.add(cls),
      remove: (cls: string) => this.classSet.delete(cls),
    };
  }

  get textContent(): string {
    if (this.children.length === 0) return this.ownText;
    return [this.ownText, ...this.children.map((c) => c.textContent)]
      .filter(Boolean)
      .join(" ");
  }

  hasAttribute(name: string): boolean {
    return this.attrs.has(name);
  }

  getAttribute(name: string): string | null {
    return this.attrs.has(name) ? (this.attrs.get(name) ?? "") : null;
  }

  setAttribute(name: string, value: string): void {
    this.attrs.set(name, value);
  }

  removeAttribute(name: string): void {
    this.attrs.delete(name);
  }

  matches(selector: string): boolean {
    const trimmed = selector.trim();
    if (trimmed === "*") return true;
    if (trimmed.startsWith(".")) {
      return this.classSet.has(trimmed.slice(1));
    }
    if (trimmed.startsWith("[") && trimmed.endsWith("]")) {
      const attrName = trimmed.slice(1, -1);
      return this.attrs.has(attrName);
    }
    return false;
  }

  closest(selector: string): MockElement | null {
    let current: MockElement | null = this;
    while (current) {
      if (current.matches(selector)) return current;
      current = current.parentElement;
    }
    return null;
  }

  querySelectorAll(selector: string): MockElement[] {
    const out: MockElement[] = [];
    const visit = (node: MockElement) => {
      for (const child of node.children) {
        if (child.matches(selector)) {
          out.push(child);
        }
        visit(child);
      }
    };
    visit(this);
    return out;
  }

  querySelector(selector: string): MockElement | null {
    return this.querySelectorAll(selector)[0] ?? null;
  }

  asHtml(): HTMLElement {
    return this as unknown as HTMLElement;
  }
}

describe("isColabOneElementHorizontallyOverflowing", () => {
  it("detects true horizontal overflow while ignoring fitting, 1px rounding, and zero-width elements", () => {
    expect(isColabOneElementHorizontallyOverflowing(null)).toBe(false);

    const hidden = new MockElement({ clientWidth: 0, scrollWidth: 240 });
    expect(isColabOneElementHorizontallyOverflowing(hidden.asHtml())).toBe(
      false,
    );

    const exactFit = new MockElement({ clientWidth: 180, scrollWidth: 180 });
    expect(isColabOneElementHorizontallyOverflowing(exactFit.asHtml())).toBe(
      false,
    );

    const subpixelTolerance = new MockElement({
      clientWidth: 180,
      scrollWidth: 181,
    });
    expect(
      isColabOneElementHorizontallyOverflowing(subpixelTolerance.asHtml()),
    ).toBe(false);

    const overflowing = new MockElement({ clientWidth: 180, scrollWidth: 182 });
    expect(isColabOneElementHorizontallyOverflowing(overflowing.asHtml())).toBe(
      true,
    );
  });
});

describe("extractColabOneSelectHoverInfo", () => {
  it("returns hover info when a closed .ant-select-selection-item overflows", () => {
    const selectionItem = new MockElement({
      classes: ["ant-select-selection-item"],
      text: "  NVIDIA   A100-SXM4-80GB · us-central1 · m-s-abc123  ",
      clientWidth: 160,
      scrollWidth: 320,
    });
    const selector = new MockElement({
      classes: ["ant-select-selector"],
      children: [selectionItem],
    });
    new MockElement({
      classes: ["ant-select", "ant-select-single"],
      children: [selector],
    });

    const info = extractColabOneSelectHoverInfo(selectionItem.asHtml());
    expect(info).toEqual({
      anchor: selectionItem.asHtml(),
      text: "NVIDIA A100-SXM4-80GB · us-central1 · m-s-abc123",
    });

    // Hovering selector padding or chevron arrow on the closed select box also resolves it
    const arrow = new MockElement({ classes: ["ant-select-arrow"] });
    const rootWithArrow = new MockElement({
      classes: ["ant-select", "ant-select-single"],
      children: [selector, arrow],
    });
    expect(extractColabOneSelectHoverInfo(selector.asHtml())).toEqual({
      anchor: selectionItem.asHtml(),
      text: "NVIDIA A100-SXM4-80GB · us-central1 · m-s-abc123",
    });
    expect(extractColabOneSelectHoverInfo(arrow.asHtml())).toEqual({
      anchor: selectionItem.asHtml(),
      text: "NVIDIA A100-SXM4-80GB · us-central1 · m-s-abc123",
    });
    expect(extractColabOneSelectHoverInfo(rootWithArrow.asHtml())).toEqual({
      anchor: selectionItem.asHtml(),
      text: "NVIDIA A100-SXM4-80GB · us-central1 · m-s-abc123",
    });
  });

  it("returns null for .ant-select-selection-item when the parent .ant-select is open", () => {
    const selectionItem = new MockElement({
      classes: ["ant-select-selection-item"],
      text: "Very long truncated trigger text",
      clientWidth: 120,
      scrollWidth: 280,
    });
    const selector = new MockElement({
      classes: ["ant-select-selector"],
      children: [selectionItem],
    });
    new MockElement({
      classes: ["ant-select", "ant-select-open"],
      children: [selector],
    });

    expect(extractColabOneSelectHoverInfo(selectionItem.asHtml())).toBeNull();
  });

  it("returns null when .ant-select-selection-item fits without truncation", () => {
    const selectionItem = new MockElement({
      classes: ["ant-select-selection-item"],
      text: "T4 GPU",
      clientWidth: 180,
      scrollWidth: 180,
    });
    new MockElement({
      classes: ["ant-select"],
      children: [selectionItem],
    });

    expect(extractColabOneSelectHoverInfo(selectionItem.asHtml())).toBeNull();
  });

  it("returns hover info when .ant-select-item-option-content overflows and excludes option state glyph", () => {
    const optionContent = new MockElement({
      classes: ["ant-select-item-option-content"],
      text: "flux1-dev-fp8-e4m3fn-v2-checkpoint-very-long-filename.safetensors",
      clientWidth: 260,
      scrollWidth: 480,
    });
    const optionState = new MockElement({
      classes: ["ant-select-item-option-state"],
      text: "✓",
      clientWidth: 16,
      scrollWidth: 16,
    });
    const optionItem = new MockElement({
      classes: ["ant-select-item", "ant-select-item-option"],
      children: [optionContent, optionState],
    });

    const info = extractColabOneSelectHoverInfo(optionContent.asHtml());
    expect(info).toEqual({
      anchor: optionItem.asHtml(),
      text: "flux1-dev-fp8-e4m3fn-v2-checkpoint-very-long-filename.safetensors",
    });
  });

  it("returns null when .ant-select-item-option-content fits without overflow", () => {
    const optionContent = new MockElement({
      classes: ["ant-select-item-option-content"],
      text: "30 minutes",
      clientWidth: 180,
      scrollWidth: 96,
    });
    const optionItem = new MockElement({
      classes: ["ant-select-item", "ant-select-item-option"],
      children: [optionContent],
    });

    expect(extractColabOneSelectHoverInfo(optionItem.asHtml())).toBeNull();
  });

  it("respects data-co-full-title on custom multi-part option rows when a child span overflows", () => {
    const tagSpan = new MockElement({
      classes: ["co-rm-opt-tag"],
      text: "Local",
      clientWidth: 42,
      scrollWidth: 42,
    });
    const modelSpan = new MockElement({
      classes: ["co-rm-opt-model"],
      text: "Qwen2.5-72B-Instruct-AWQ-Long-Variant",
      clientWidth: 110,
      scrollWidth: 265,
    });
    const row = new MockElement({
      classes: ["co-rm-opt-row"],
      attrs: {
        [COLABONE_SELECT_FULL_TITLE_ATTR]:
          "Local · A100 · US · m-s-abc · Qwen2.5-72B-Instruct-AWQ-Long-Variant",
      },
      clientWidth: 480,
      scrollWidth: 480,
      children: [tagSpan, modelSpan],
    });
    const optionContent = new MockElement({
      classes: ["ant-select-item-option-content"],
      clientWidth: 480,
      scrollWidth: 480,
      children: [row],
    });
    const optionItem = new MockElement({
      classes: ["ant-select-item", "ant-select-item-option"],
      children: [optionContent],
    });

    // Hovering a non-overflowing sibling inside the same option row still detects child overflow
    const info = extractColabOneSelectHoverInfo(tagSpan.asHtml());
    expect(info).toEqual({
      anchor: optionItem.asHtml(),
      text: "Local · A100 · US · m-s-abc · Qwen2.5-72B-Instruct-AWQ-Long-Variant",
    });

    // Text-node-like target with parentElement also resolves cleanly
    const textNodeLike = { parentElement: modelSpan.asHtml() } as unknown as EventTarget;
    expect(extractColabOneSelectHoverInfo(textNodeLike)).toEqual({
      anchor: optionItem.asHtml(),
      text: "Local · A100 · US · m-s-abc · Qwen2.5-72B-Instruct-AWQ-Long-Variant",
    });
  });

  it("ignores .co-sr-only hidden children and non-select elements", () => {
    const srOnly = new MockElement({
      classes: ["co-sr-only"],
      text: "Screen reader label",
      clientWidth: 1,
      scrollWidth: 120,
    });
    const optionContent = new MockElement({
      classes: ["ant-select-item-option-content"],
      text: "Short",
      clientWidth: 160,
      scrollWidth: 60,
      children: [srOnly],
    });
    const optionItem = new MockElement({
      classes: ["ant-select-item", "ant-select-item-option"],
      children: [optionContent],
    });
    expect(extractColabOneSelectHoverInfo(optionItem.asHtml())).toBeNull();

    const plainDiv = new MockElement({
      classes: ["co-card"],
      text: "Truncated card title",
      clientWidth: 100,
      scrollWidth: 300,
    });
    expect(extractColabOneSelectHoverInfo(plainDiv.asHtml())).toBeNull();
    expect(extractColabOneSelectHoverInfo(null)).toBeNull();
  });
});

describe("stashColabOneNativeTitles & restoreColabOneNativeTitles", () => {
  it("temporarily stashes native titles to prevent duplicate OS tooltips while preserving full hover text", () => {
    const child = new MockElement({
      classes: ["ant-select-item-option-content"],
      attrs: { title: "child-native-title" },
      text: "Long overflowing checkpoint file name",
      clientWidth: 120,
      scrollWidth: 340,
    });
    const optionItem = new MockElement({
      classes: ["ant-select-item", "ant-select-item-option"],
      attrs: { title: "Long overflowing checkpoint file name" },
      children: [child],
    });

    const stashed = stashColabOneNativeTitles(optionItem.asHtml());
    expect(stashed).toHaveLength(2);
    expect(optionItem.hasAttribute("title")).toBe(false);
    expect(child.hasAttribute("title")).toBe(false);
    expect(optionItem.getAttribute(COLABONE_SELECT_SAVED_TITLE_ATTR)).toBe(
      "Long overflowing checkpoint file name",
    );

    // Still resolves the exact stashed title while hovered
    expect(extractColabOneSelectHoverInfo(child.asHtml())).toEqual({
      anchor: optionItem.asHtml(),
      text: "Long overflowing checkpoint file name",
    });

    // If Vue dynamically updates title while stashed, prefer the fresh title over stale saved attr
    optionItem.setAttribute("title", "Updated localized overflowing checkpoint file name");
    expect(extractColabOneSelectHoverInfo(child.asHtml())).toEqual({
      anchor: optionItem.asHtml(),
      text: "Updated localized overflowing checkpoint file name",
    });
    optionItem.removeAttribute("title");

    restoreColabOneNativeTitles(stashed);
    expect(optionItem.getAttribute("title")).toBe(
      "Long overflowing checkpoint file name",
    );
    expect(child.getAttribute("title")).toBe("child-native-title");
    expect(optionItem.hasAttribute(COLABONE_SELECT_SAVED_TITLE_ATTR)).toBe(
      false,
    );
    expect(child.hasAttribute(COLABONE_SELECT_SAVED_TITLE_ATTR)).toBe(false);
  });
});

describe("computeColabOneFloatingTooltipPosition", () => {
  it("centers horizontally and prefers placing above the anchor when space permits", () => {
    const pos = computeColabOneFloatingTooltipPosition(
      { top: 240, bottom: 272, left: 400, right: 600, width: 200, height: 32 },
      { width: 300, height: 38 },
      { width: 1280, height: 800 },
      10,
    );
    expect(pos).toEqual({
      left: 350,
      top: 194,
      placement: "top",
    });
  });

  it("flips below the anchor when near the top edge of the viewport", () => {
    const pos = computeColabOneFloatingTooltipPosition(
      { top: 20, bottom: 52, left: 400, right: 600, width: 200, height: 32 },
      { width: 280, height: 42 },
      { width: 1280, height: 800 },
      10,
    );
    expect(pos).toEqual({
      left: 360,
      top: 60,
      placement: "bottom",
    });
  });

  it("clamps horizontally near the left and right edges of the viewport", () => {
    const nearLeft = computeColabOneFloatingTooltipPosition(
      { top: 300, bottom: 332, left: 6, right: 86, width: 80, height: 32 },
      { width: 320, height: 36 },
      { width: 1000, height: 700 },
      12,
    );
    expect(nearLeft).toEqual({
      left: 12,
      top: 256,
      placement: "top",
    });

    const nearRight = computeColabOneFloatingTooltipPosition(
      { top: 300, bottom: 332, left: 910, right: 990, width: 80, height: 32 },
      { width: 320, height: 36 },
      { width: 1000, height: 700 },
      12,
    );
    expect(nearRight).toEqual({
      left: 668,
      top: 256,
      placement: "top",
    });
  });
});
