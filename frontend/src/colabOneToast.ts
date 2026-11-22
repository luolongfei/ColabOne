import { ref, type Ref } from "vue";
import { isTransientIpcError, stripColabOneErrorPrefixes } from "./billing";

export type ColabOneToastKind = "success" | "error" | "warning" | "info";

export interface ColabOneToastInput {
  kind?: ColabOneToastKind;
  message?: string;
  resolveText?: () => string;
  durationMs?: number;
  dedupeKey?: string;
  group?: string;
  onDismiss?: () => void;
}

export interface ColabOneToastItem {
  id: number;
  kind: ColabOneToastKind;
  message: string;
  resolveText?: () => string;
  durationMs: number;
  remainingMs: number;
  paused: boolean;
  revision: number;
  dedupeKey: string;
  group?: string;
}

export interface ColabOneToastManager {
  items: Ref<ColabOneToastItem[]>;
  push: (input: ColabOneToastInput) => number | null;
  pause: (id: number) => void;
  resume: (id: number) => void;
  dismiss: (id: number) => void;
  dismissGroup: (group: string) => void;
  clear: () => void;
}

const PERSISTENT_ERROR_PATTERNS: readonly RegExp[] = [
  /^连接检查失败[：:]/,
  /mygo:\s*runtime not found/i,
];

export const COLABONE_MODEL_DRAWER_MIN_WIDTH = 380;
export const COLABONE_MODEL_DRAWER_MAX_WIDTH = 720;
export const COLABONE_MODEL_DRAWER_GUTTER = 48;
export const COLABONE_DRAWER_TOAST_TOP_FALLBACK_PX = 68;

export interface ColabOneToastHorizontalBounds {
  leftOffsetPx: number;
  rightOffsetPx: number;
}

function clamp(min: number, value: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

/**
 * Computes the right-docked ModelDrawer width for a given viewport width.
 * Shared between ModelDrawer and the unified toast host so drawer-scoped
 * toasts stay centered strictly within the drawer panel across all viewports.
 */
export function computeColabOneModelDrawerWidth(viewportWidth: number): number {
  const safeViewport =
    Number.isFinite(viewportWidth) && viewportWidth > 0
      ? Math.round(viewportWidth)
      : 1280;
  return Math.min(
    safeViewport,
    Math.min(
      COLABONE_MODEL_DRAWER_MAX_WIDTH,
      Math.max(
        COLABONE_MODEL_DRAWER_MIN_WIDTH,
        safeViewport - COLABONE_MODEL_DRAWER_GUTTER,
      ),
    ),
  );
}

/**
 * Resolves the active horizontal surface bounds (`leftOffsetPx`, `rightOffsetPx`)
 * for the floating toast host. When the right-hand overlay ModelDrawer is open,
 * toasts center within the drawer region (taking precedence over any inline
 * LogDrawer behind the backdrop). Otherwise they center over the main workspace.
 */
export function resolveColabOneToastHorizontalBounds(options: {
  viewportWidth: number;
  modelDrawerOpen: boolean;
  modelDrawerWidthPx?: number;
  inlineLogDrawerOpen: boolean;
  inlineLogDrawerWidthPx: number;
}): ColabOneToastHorizontalBounds {
  const safeViewport =
    Number.isFinite(options.viewportWidth) && options.viewportWidth > 0
      ? Math.round(options.viewportWidth)
      : 1280;

  if (options.modelDrawerOpen) {
    const rawDrawerWidth =
      options.modelDrawerWidthPx !== undefined &&
      Number.isFinite(options.modelDrawerWidthPx) &&
      options.modelDrawerWidthPx > 0
        ? Math.round(options.modelDrawerWidthPx)
        : computeColabOneModelDrawerWidth(safeViewport);
    const clampedDrawerWidth = Math.min(safeViewport, rawDrawerWidth);
    return {
      leftOffsetPx: Math.max(0, safeViewport - clampedDrawerWidth),
      rightOffsetPx: 0,
    };
  }

  if (options.inlineLogDrawerOpen) {
    const safeLogWidth =
      Number.isFinite(options.inlineLogDrawerWidthPx) &&
      options.inlineLogDrawerWidthPx > 0
        ? Math.min(
            Math.max(0, safeViewport - 160),
            Math.round(options.inlineLogDrawerWidthPx),
          )
        : 0;
    return {
      leftOffsetPx: 0,
      rightOffsetPx: safeLogWidth,
    };
  }

  return {
    leftOffsetPx: 0,
    rightOffsetPx: 0,
  };
}

/**
 * Classifies whether a raw backend/IPC error represents an ongoing
 * system-connectivity condition (persistent inline banner) rather than a
 * transient one-shot action/operation failure (auto-dismissing toast).
 */
export function isPersistentColabOneError(raw: unknown): boolean {
  const cleaned = stripColabOneErrorPrefixes(raw);
  if (!cleaned) return false;
  if (isTransientIpcError(cleaned)) return true;
  return PERSISTENT_ERROR_PATTERNS.some((pattern) => pattern.test(cleaned));
}

/**
 * Computes a comfortable reading window scaled by severity and message length.
 * Short confirmations disappear swiftly; longer actionable errors allow ample
 * reading time without lingering indefinitely.
 */
export function computeColabOneToastDuration(
  kind: ColabOneToastKind,
  text: string,
): number {
  const len = text.trim().length;
  switch (kind) {
    case "error":
      return clamp(6000, Math.round(4800 + len * 45), 10000);
    case "warning":
      return clamp(5500, Math.round(4500 + len * 45), 8500);
    case "info":
      return clamp(4200, Math.round(3500 + len * 40), 6500);
    case "success":
    default:
      return clamp(3200, Math.round(2800 + len * 35), 5000);
  }
}

export function resolveColabOneToastText(item: ColabOneToastItem): string {
  return item.resolveText ? item.resolveText() : item.message;
}

interface ToastRuntimeRecord {
  timer?: ReturnType<typeof setTimeout>;
  startedAt: number;
  onDismiss?: () => void;
  dismissed: boolean;
}

export function createColabOneToastManager(options?: {
  maxVisible?: number;
  now?: () => number;
}): ColabOneToastManager {
  const maxVisible = Math.max(1, options?.maxVisible ?? 3);
  const now = options?.now ?? (() => Date.now());
  const items = ref<ColabOneToastItem[]>([]);
  const records = new Map<number, ToastRuntimeRecord>();
  let nextId = 1;

  function clearTimer(id: number) {
    const rec = records.get(id);
    if (rec?.timer !== undefined) {
      clearTimeout(rec.timer);
      rec.timer = undefined;
    }
  }

  function schedule(id: number, delayMs: number) {
    clearTimer(id);
    const rec = records.get(id);
    if (!rec || rec.dismissed) return;
    const safeDelay = Math.max(0, Math.round(delayMs));
    rec.startedAt = now();
    if (safeDelay === 0) {
      dismiss(id);
      return;
    }
    rec.timer = setTimeout(() => {
      dismiss(id);
    }, safeDelay);
  }

  function dismiss(id: number) {
    clearTimer(id);
    const rec = records.get(id);
    records.delete(id);
    const idx = items.value.findIndex((item) => item.id === id);
    if (idx !== -1) {
      items.value = [
        ...items.value.slice(0, idx),
        ...items.value.slice(idx + 1),
      ];
    }
    if (rec && !rec.dismissed) {
      rec.dismissed = true;
      rec.onDismiss?.();
    }
  }

  function push(input: ColabOneToastInput): number | null {
    const kind: ColabOneToastKind = input.kind ?? "success";
    const resolved = (
      input.resolveText ? input.resolveText() : (input.message ?? "")
    ).trim();
    if (!resolved) return null;

    const durationMs =
      input.durationMs !== undefined &&
      Number.isFinite(input.durationMs) &&
      input.durationMs > 0
        ? Math.round(input.durationMs)
        : computeColabOneToastDuration(kind, resolved);
    const dedupeKey = input.dedupeKey || `${kind}:${resolved}`;

    const existing = items.value.find((item) => item.dedupeKey === dedupeKey);
    if (existing) {
      const updated: ColabOneToastItem = {
        ...existing,
        kind,
        message: input.message ?? resolved,
        resolveText: input.resolveText,
        durationMs,
        remainingMs: durationMs,
        revision: existing.revision + 1,
        group: input.group ?? existing.group,
      };
      items.value = items.value.map((item) =>
        item.id === existing.id ? updated : item,
      );
      const rec = records.get(existing.id);
      if (rec) {
        if (input.onDismiss) rec.onDismiss = input.onDismiss;
        if (!updated.paused) {
          schedule(existing.id, durationMs);
        } else {
          clearTimer(existing.id);
        }
      }
      return existing.id;
    }

    while (items.value.length >= maxVisible) {
      const evictCandidate =
        items.value.find((item) => !item.paused) ?? items.value[0];
      if (!evictCandidate) break;
      dismiss(evictCandidate.id);
    }

    const id = nextId++;
    const item: ColabOneToastItem = {
      id,
      kind,
      message: input.message ?? resolved,
      resolveText: input.resolveText,
      durationMs,
      remainingMs: durationMs,
      paused: false,
      revision: 1,
      dedupeKey,
      group: input.group,
    };
    records.set(id, {
      startedAt: now(),
      onDismiss: input.onDismiss,
      dismissed: false,
    });
    items.value = [...items.value, item];
    schedule(id, durationMs);
    return id;
  }

  function pause(id: number) {
    const item = items.value.find((entry) => entry.id === id);
    const rec = records.get(id);
    if (!item || !rec || item.paused) return;
    const elapsed = Math.max(0, now() - rec.startedAt);
    const remaining = Math.max(0, item.remainingMs - elapsed);
    clearTimer(id);
    items.value = items.value.map((entry) =>
      entry.id === id
        ? { ...entry, paused: true, remainingMs: remaining }
        : entry,
    );
  }

  function resume(id: number) {
    const item = items.value.find((entry) => entry.id === id);
    const rec = records.get(id);
    if (!item || !rec || !item.paused) return;
    if (item.remainingMs <= 0) {
      dismiss(id);
      return;
    }
    items.value = items.value.map((entry) =>
      entry.id === id ? { ...entry, paused: false } : entry,
    );
    schedule(id, item.remainingMs);
  }

  function dismissGroup(group: string) {
    const targets = items.value
      .filter((entry) => entry.group === group)
      .map((entry) => entry.id);
    for (const id of targets) {
      dismiss(id);
    }
  }

  function clear() {
    for (const id of Array.from(records.keys())) {
      clearTimer(id);
    }
    records.clear();
    items.value = [];
  }

  return {
    items,
    push,
    pause,
    resume,
    dismiss,
    dismissGroup,
    clear,
  };
}
