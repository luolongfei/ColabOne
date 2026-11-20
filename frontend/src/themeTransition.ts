export const COLABONE_THEME_TRANSITION_MS = 1000;
export const COLABONE_THEME_TRANSITION_EASING = "cubic-bezier(0.3, 0.08, 0.5, 1)";
export const COLABONE_THEME_TRANSITION_CLASS = "co-theme-transitioning";

// Analytical cubic-bezier(0.3, 0.08, 0.5, 1) evaluator used when sampling
// multi-keyframe circular reveals so center migration and radius expansion
// stay C1-continuous with zero velocity kinks.
const BEZIER_X1 = 0.3;
const BEZIER_Y1 = 0.08;
const BEZIER_X2 = 0.5;
const BEZIER_Y2 = 1.0;

function sampleCubicBezier(p1: number, p2: number, u: number): number {
  const inv = 1 - u;
  return 3 * inv * inv * u * p1 + 3 * inv * u * u * p2 + u * u * u;
}

function sampleCubicBezierDerivative(p1: number, p2: number, u: number): number {
  const inv = 1 - u;
  return (
    3 * inv * inv * p1 +
    6 * inv * u * (p2 - p1) +
    3 * u * u * (1 - p2)
  );
}

export function evalThemeEasing(t: number): number {
  if (t <= 0) return 0;
  if (t >= 1) return 1;
  let u = t;
  for (let i = 0; i < 6; i += 1) {
    const x = sampleCubicBezier(BEZIER_X1, BEZIER_X2, u) - t;
    const dx = sampleCubicBezierDerivative(BEZIER_X1, BEZIER_X2, u);
    if (Math.abs(x) < 1e-5 || Math.abs(dx) < 1e-6) break;
    u = Math.min(Math.max(u - x / dx, 0), 1);
  }
  return sampleCubicBezier(BEZIER_Y1, BEZIER_Y2, u);
}

export interface ThemeTransitionPoint {
  x: number;
  y: number;
}

export interface ViewportSize {
  width: number;
  height: number;
}

export interface PointerLikeEvent {
  clientX?: number;
  clientY?: number;
  currentTarget?: {
    getBoundingClientRect?: () => {
      left: number;
      top: number;
      width: number;
      height: number;
    };
  } | null;
}

export interface ViewTransitionLike {
  ready: Promise<void>;
  finished: Promise<void>;
  updateCallbackDone?: Promise<void>;
  skipTransition?: () => void;
}

export interface TransitionDocumentLike {
  visibilityState?: string;
  documentElement: {
    classList: {
      add: (cls: string) => void;
      remove: (cls: string) => void;
    };
    offsetHeight?: number;
    animate?: (
      keyframes: Keyframe[] | PropertyIndexedKeyframes | null,
      options?: KeyframeAnimationOptions,
    ) => unknown;
  };
  startViewTransition?: (
    cb: () => void | Promise<void>,
  ) => ViewTransitionLike;
}

export function resolveThemeTransitionOrigin(
  input: PointerLikeEvent | ThemeTransitionPoint | null | undefined,
  viewport: ViewportSize,
): ThemeTransitionPoint {
  const width = Math.max(1, viewport.width);
  const height = Math.max(1, viewport.height);
  const clamp = (x: number, y: number): ThemeTransitionPoint => ({
    x: Math.min(Math.max(x, 0), width),
    y: Math.min(Math.max(y, 0), height),
  });

  if (input && "x" in input && "y" in input) {
    if (Number.isFinite(input.x) && Number.isFinite(input.y)) {
      return clamp(input.x, input.y);
    }
  }

  if (
    input &&
    ("clientX" in input || "clientY" in input || "currentTarget" in input)
  ) {
    const cx = input.clientX ?? 0;
    const cy = input.clientY ?? 0;
    if (Number.isFinite(cx) && Number.isFinite(cy) && (cx > 0 || cy > 0)) {
      return clamp(cx, cy);
    }
    const rect = input.currentTarget?.getBoundingClientRect?.();
    if (rect && rect.width > 0 && rect.height > 0) {
      return clamp(rect.left + rect.width / 2, rect.top + rect.height / 2);
    }
  }

  return clamp(width - 48, 28);
}

export function computeCircularRevealRadius(
  origin: ThemeTransitionPoint,
  viewport: ViewportSize,
): number {
  const maxX = Math.max(origin.x, Math.max(0, viewport.width - origin.x));
  const maxY = Math.max(origin.y, Math.max(0, viewport.height - origin.y));
  // +2px prevents 1px subpixel edge artifacts at the farthest corner on HiDPI screens.
  return Math.ceil(Math.hypot(maxX, maxY)) + 2;
}

export function buildCircularRevealKeyframes(
  origin: ThemeTransitionPoint,
  endRadius: number,
): [string, string] {
  const x = Math.round(origin.x * 100) / 100;
  const y = Math.round(origin.y * 100) / 100;
  return [
    `circle(0px at ${x}px ${y}px)`,
    `circle(${endRadius}px at ${x}px ${y}px)`,
  ];
}

export function isTopRightCornerOrigin(
  origin: ThemeTransitionPoint,
  viewport: ViewportSize,
): boolean {
  const rightGap = viewport.width - origin.x;
  return (
    rightGap >= 0 &&
    rightGap <= Math.max(340, viewport.width * 0.28) &&
    origin.y >= 0 &&
    origin.y <= 110
  );
}

/**
 * Builds the clip-path animation frames for the circular theme reveal.
 *
 * When triggered from the top-right navbar toggle button, the circle starts at
 * the exact click point and monotonically glides its center into the top-right
 * corner (W, 0) as it opens, forming a clean corner arc flush with both the top
 * and right edges (no dangling semicircle belly curving back up on the right).
 * Because ||dC/dr|| <= 0.92 everywhere, every edge strictly expands monotonically.
 */
export function buildCircularRevealAnimation(
  origin: ThemeTransitionPoint,
  viewport: ViewportSize,
  fallbackEasing: string = COLABONE_THEME_TRANSITION_EASING,
): { keyframes: Keyframe[]; easing: string } {
  const round2 = (n: number) => Math.round(n * 100) / 100;

  if (!isTopRightCornerOrigin(origin, viewport)) {
    const endRadius = computeCircularRevealRadius(origin, viewport);
    const [from, to] = buildCircularRevealKeyframes(origin, endRadius);
    return {
      keyframes: [
        { offset: 0, clipPath: from },
        { offset: 1, clipPath: to },
      ],
      easing: fallbackEasing,
    };
  }

  const corner: ThemeTransitionPoint = { x: viewport.width, y: 0 };
  const dx = corner.x - origin.x;
  const dy = corner.y - origin.y;
  const distToCorner = Math.hypot(dx, dy);
  const endRadius = computeCircularRevealRadius(corner, viewport);

  if (distToCorner < 1) {
    const [from, to] = buildCircularRevealKeyframes(corner, endRadius);
    return {
      keyframes: [
        { offset: 0, clipPath: from },
        { offset: 1, clipPath: to },
      ],
      easing: fallbackEasing,
    };
  }

  // With easeOutQuad(u) = 1 - (1 - u)^2, max derivative at u=0 is 2/span.
  // Choosing span = distToCorner / 0.46 guarantees ||dC/dr|| <= 0.92 < 1 so
  // the revealing circle strictly expands in all directions while snapping smoothly
  // into a crisp top-right corner arc during the opening sweep.
  const migrateSpan = Math.min(endRadius * 0.65, distToCorner / 0.46);
  const steps = 28;
  const keyframes: Keyframe[] = [];

  for (let i = 0; i <= steps; i += 1) {
    const offset = i / steps;
    const progress = evalThemeEasing(offset);
    const r = progress * endRadius;
    const u = migrateSpan > 0 ? Math.min(1, r / migrateSpan) : 1;
    const m = 1 - (1 - u) * (1 - u);
    const cx = round2(origin.x + dx * m);
    const cy = round2(origin.y + dy * m);
    const radius = i === 0 ? 0 : i === steps ? endRadius : round2(r);
    keyframes.push({
      offset,
      clipPath: `circle(${radius}px at ${cx}px ${cy}px)`,
    });
  }

  return { keyframes, easing: "linear" };
}

let activeTransitionSeq = 0;

export function runColabOneThemeTransition(options: {
  commit: () => void | Promise<void>;
  origin?: PointerLikeEvent | ThemeTransitionPoint | null;
  doc?: TransitionDocumentLike;
  viewport?: ViewportSize;
  durationMs?: number;
  easing?: string;
}): void {
  const doc =
    options.doc ??
    (typeof document !== "undefined"
      ? (document as unknown as TransitionDocumentLike)
      : undefined);
  const viewport = options.viewport ?? {
    width: typeof window !== "undefined" ? window.innerWidth : 1280,
    height: typeof window !== "undefined" ? window.innerHeight : 800,
  };
  const durationMs = options.durationMs ?? COLABONE_THEME_TRANSITION_MS;
  const easing = options.easing ?? COLABONE_THEME_TRANSITION_EASING;

  if (
    !doc ||
    doc.visibilityState === "hidden" ||
    typeof doc.startViewTransition !== "function" ||
    typeof doc.documentElement?.animate !== "function"
  ) {
    void options.commit();
    return;
  }

  const origin = resolveThemeTransitionOrigin(options.origin, viewport);
  const plan = buildCircularRevealAnimation(origin, viewport, easing);

  activeTransitionSeq += 1;
  const seq = activeTransitionSeq;
  const root = doc.documentElement;

  let transition: ViewTransitionLike;
  try {
    transition = doc.startViewTransition(async () => {
      root.classList.add(COLABONE_THEME_TRANSITION_CLASS);
      await options.commit();
      // Force synchronous style/layout flush so CSS variables and Ant Design
      // CSS-in-JS styles commit immediately with transition: none before capture.
      void root.offsetHeight;
    });
  } catch {
    root.classList.remove(COLABONE_THEME_TRANSITION_CLASS);
    void options.commit();
    return;
  }

  transition.ready
    .then(() => {
      if (seq !== activeTransitionSeq) return;
      root.animate?.(plan.keyframes, {
        duration: durationMs,
        easing: plan.easing,
        fill: "both",
        pseudoElement: "::view-transition-new(root)",
      });
    })
    .catch(() => {
      /* Transition was skipped or aborted; theme commit already ran. */
    });

  transition.finished
    .catch(() => {})
    .finally(() => {
      if (seq === activeTransitionSeq) {
        root.classList.remove(COLABONE_THEME_TRANSITION_CLASS);
      }
    });
}
