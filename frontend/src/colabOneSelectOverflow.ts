export const COLABONE_SELECT_FULL_TITLE_ATTR = "data-co-full-title";
export const COLABONE_SELECT_SAVED_TITLE_ATTR = "data-co-saved-title";
export const COLABONE_SELECT_TOOLTIP_ENTER_DELAY_MS = 120;
export const COLABONE_SELECT_TOOLTIP_GAP_PX = 8;
export const COLABONE_SELECT_TOOLTIP_DEFAULT_MARGIN_PX = 10;

export interface ColabOneSelectHoverInfo {
  anchor: HTMLElement;
  text: string;
}

export interface ColabOneRectLike {
  top: number;
  bottom: number;
  left: number;
  right: number;
  width: number;
  height: number;
}

export interface ColabOneSizeLike {
  width: number;
  height: number;
}

export interface ColabOneFloatingTooltipPosition {
  left: number;
  top: number;
  placement: "top" | "bottom";
}

function normalizeColabOneSelectText(raw: string | null | undefined): string {
  if (typeof raw !== "string") return "";
  return raw.replace(/\s+/g, " ").trim();
}

function toHtmlElement(node: unknown): HTMLElement | null {
  if (!node || typeof node !== "object") return null;
  if (typeof HTMLElement !== "undefined") {
    return node instanceof HTMLElement ? node : null;
  }
  const candidate = node as Partial<HTMLElement>;
  if (
    typeof candidate.getAttribute === "function" &&
    typeof candidate.closest === "function"
  ) {
    return candidate as HTMLElement;
  }
  return null;
}

function resolveClosestSource(target: EventTarget | null): Element | null {
  if (!target || typeof target !== "object") return null;
  const candidate = target as Partial<Element> & {
    parentElement?: Element | null;
  };
  if (typeof candidate.closest === "function") {
    return candidate as Element;
  }
  if (
    candidate.parentElement &&
    typeof candidate.parentElement.closest === "function"
  ) {
    return candidate.parentElement;
  }
  return null;
}

function isScreenReaderOnly(el: HTMLElement): boolean {
  return Boolean(el.classList?.contains("co-sr-only"));
}

function readFirstNonEmptyAttr(
  el: HTMLElement | null,
  attrs: readonly string[],
): string {
  if (!el || typeof el.getAttribute !== "function") return "";
  for (const attr of attrs) {
    const value = normalizeColabOneSelectText(el.getAttribute(attr));
    if (value) return value;
  }
  return "";
}

function findChildFullTitle(root: HTMLElement): string {
  if (typeof root.querySelector === "function") {
    const titled = toHtmlElement(
      root.querySelector(`[${COLABONE_SELECT_FULL_TITLE_ATTR}]`),
    );
    if (titled) {
      const value = normalizeColabOneSelectText(
        titled.getAttribute(COLABONE_SELECT_FULL_TITLE_ATTR),
      );
      if (value) return value;
    }
  }
  return "";
}

function hasColabOneHorizontalOverflow(root: HTMLElement): boolean {
  if (
    !isScreenReaderOnly(root) &&
    isColabOneElementHorizontallyOverflowing(root)
  ) {
    return true;
  }
  if (typeof root.querySelectorAll === "function") {
    const descendants = root.querySelectorAll("*");
    for (let i = 0; i < descendants.length; i += 1) {
      const child = toHtmlElement(descendants[i]);
      if (
        child &&
        !isScreenReaderOnly(child) &&
        isColabOneElementHorizontallyOverflowing(child)
      ) {
        return true;
      }
    }
    return false;
  }
  if (root.children) {
    const stack: unknown[] = Array.from(root.children);
    while (stack.length > 0) {
      const current = toHtmlElement(stack.pop());
      if (!current) continue;
      if (
        !isScreenReaderOnly(current) &&
        isColabOneElementHorizontallyOverflowing(current)
      ) {
        return true;
      }
      if (current.children) {
        for (let i = 0; i < current.children.length; i += 1) {
          stack.push(current.children[i]);
        }
      }
    }
  }
  return false;
}

export function isColabOneElementHorizontallyOverflowing(
  el: HTMLElement | null,
): boolean {
  if (!el) return false;
  const { clientWidth, scrollWidth } = el;
  return (
    Number.isFinite(clientWidth) &&
    Number.isFinite(scrollWidth) &&
    clientWidth > 0 &&
    scrollWidth > clientWidth + 1
  );
}

export function extractColabOneSelectHoverInfo(
  target: EventTarget | null,
): ColabOneSelectHoverInfo | null {
  const source = resolveClosestSource(target);
  if (!source) return null;

  const directSelectionItem = toHtmlElement(
    source.closest(".ant-select-selection-item"),
  );
  const selectRootForBox = !directSelectionItem
    ? toHtmlElement(source.closest(".ant-select"))
    : null;
  const selectionItem =
    directSelectionItem ??
    (selectRootForBox &&
    !selectRootForBox.classList?.contains("ant-select-open") &&
    typeof selectRootForBox.querySelector === "function"
      ? toHtmlElement(
          selectRootForBox.querySelector(".ant-select-selection-item"),
        )
      : null);
  if (selectionItem) {
    const selectRoot = selectRootForBox ?? selectionItem.closest(".ant-select");
    if (
      selectRoot?.classList?.contains("ant-select-open") ||
      Boolean(selectionItem.closest(".ant-select-open"))
    ) {
      return null;
    }
    if (!hasColabOneHorizontalOverflow(selectionItem)) {
      return null;
    }
    const text =
      readFirstNonEmptyAttr(selectionItem, [COLABONE_SELECT_FULL_TITLE_ATTR]) ||
      findChildFullTitle(selectionItem) ||
      readFirstNonEmptyAttr(selectionItem, [
        "title",
        COLABONE_SELECT_SAVED_TITLE_ATTR,
      ]) ||
      normalizeColabOneSelectText(selectionItem.textContent);
    if (!text) return null;
    return { anchor: selectionItem, text };
  }

  const optionItem = toHtmlElement(source.closest(".ant-select-item-option"));
  if (optionItem) {
    const optionContent =
      (typeof optionItem.querySelector === "function"
        ? toHtmlElement(
            optionItem.querySelector(".ant-select-item-option-content"),
          )
        : null) ?? optionItem;
    if (!hasColabOneHorizontalOverflow(optionContent)) {
      return null;
    }
    const text =
      readFirstNonEmptyAttr(optionItem, [COLABONE_SELECT_FULL_TITLE_ATTR]) ||
      findChildFullTitle(optionItem) ||
      readFirstNonEmptyAttr(optionItem, [
        "title",
        COLABONE_SELECT_SAVED_TITLE_ATTR,
      ]) ||
      readFirstNonEmptyAttr(optionContent, [
        "title",
        COLABONE_SELECT_SAVED_TITLE_ATTR,
      ]) ||
      normalizeColabOneSelectText(optionContent.textContent);
    if (!text) return null;
    return { anchor: optionItem, text };
  }

  return null;
}

export function computeColabOneFloatingTooltipPosition(
  anchorRect: ColabOneRectLike,
  tooltipSize: ColabOneSizeLike,
  viewport: ColabOneSizeLike,
  margin = COLABONE_SELECT_TOOLTIP_DEFAULT_MARGIN_PX,
): ColabOneFloatingTooltipPosition {
  const safeMargin = Math.max(
    0,
    Number.isFinite(margin) ? margin : COLABONE_SELECT_TOOLTIP_DEFAULT_MARGIN_PX,
  );
  const tipWidth = Math.max(
    0,
    Number.isFinite(tooltipSize.width) ? tooltipSize.width : 0,
  );
  const tipHeight = Math.max(
    0,
    Number.isFinite(tooltipSize.height) ? tooltipSize.height : 0,
  );
  const viewWidth = Math.max(
    0,
    Number.isFinite(viewport.width) ? viewport.width : 0,
  );
  const viewHeight = Math.max(
    0,
    Number.isFinite(viewport.height) ? viewport.height : 0,
  );

  const anchorCenterX = anchorRect.left + anchorRect.width / 2;
  const idealLeft = anchorCenterX - tipWidth / 2;
  const maxLeft = Math.max(safeMargin, viewWidth - tipWidth - safeMargin);
  const left = Math.round(Math.min(Math.max(idealLeft, safeMargin), maxLeft));

  const aboveTop = anchorRect.top - tipHeight - COLABONE_SELECT_TOOLTIP_GAP_PX;
  const belowTop = anchorRect.bottom + COLABONE_SELECT_TOOLTIP_GAP_PX;
  const fitsAbove = aboveTop >= safeMargin;
  const fitsBelow = belowTop + tipHeight <= viewHeight - safeMargin;

  let placement: "top" | "bottom";
  let rawTop: number;
  if (fitsAbove) {
    placement = "top";
    rawTop = aboveTop;
  } else if (fitsBelow) {
    placement = "bottom";
    rawTop = belowTop;
  } else {
    const spaceAbove = anchorRect.top - safeMargin;
    const spaceBelow = viewHeight - safeMargin - anchorRect.bottom;
    if (spaceBelow >= spaceAbove) {
      placement = "bottom";
      rawTop = belowTop;
    } else {
      placement = "top";
      rawTop = aboveTop;
    }
  }

  const maxTop = Math.max(safeMargin, viewHeight - tipHeight - safeMargin);
  const top = Math.round(Math.min(Math.max(rawTop, safeMargin), maxTop));

  return { left, top, placement };
}

export function stashColabOneNativeTitles(anchor: HTMLElement): HTMLElement[] {
  const targets = new Set<HTMLElement>([anchor]);
  if (typeof anchor.querySelectorAll === "function") {
    const descendants = anchor.querySelectorAll("[title]");
    for (let i = 0; i < descendants.length; i += 1) {
      const child = toHtmlElement(descendants[i]);
      if (child) targets.add(child);
    }
  }
  if (
    anchor.classList?.contains("ant-select-selection-item") &&
    typeof anchor.closest === "function"
  ) {
    const selector = toHtmlElement(anchor.closest(".ant-select-selector"));
    if (selector) targets.add(selector);
    const selectRoot = toHtmlElement(anchor.closest(".ant-select"));
    if (selectRoot) targets.add(selectRoot);
  }

  const stashed: HTMLElement[] = [];
  for (const el of targets) {
    if (typeof el.hasAttribute !== "function") continue;
    if (!el.hasAttribute("title")) continue;
    const raw = el.getAttribute("title") ?? "";
    el.setAttribute(COLABONE_SELECT_SAVED_TITLE_ATTR, raw);
    el.removeAttribute("title");
    stashed.push(el);
  }
  return stashed;
}

export function restoreColabOneNativeTitles(
  elements: readonly HTMLElement[],
): void {
  for (const el of elements) {
    if (typeof el.hasAttribute !== "function") continue;
    if (!el.hasAttribute(COLABONE_SELECT_SAVED_TITLE_ATTR)) continue;
    const saved = el.getAttribute(COLABONE_SELECT_SAVED_TITLE_ATTR) ?? "";
    if (!el.hasAttribute("title")) {
      el.setAttribute("title", saved);
    }
    el.removeAttribute(COLABONE_SELECT_SAVED_TITLE_ATTR);
  }
}
