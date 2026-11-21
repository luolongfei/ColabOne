import { describe, expect, it, vi } from "vitest";
import {
  COLABONE_THEME_HIDE_TOOLTIPS_CLASS,
  COLABONE_THEME_TRANSITION_CLASS,
  COLABONE_THEME_TRANSITION_MS,
  COLABONE_THEME_TRANSITION_EASING,
  buildCircularRevealAnimation,
  buildCircularRevealKeyframes,
  computeCircularRevealRadius,
  isTopRightCornerOrigin,
  resolveThemeTransitionOrigin,
  runColabOneThemeTransition,
  type TransitionDocumentLike,
} from "./themeTransition";

function parseCircle(clipPath: string): { r: number; x: number; y: number } {
  const match = clipPath.match(
    /^circle\(([\d.]+)px at ([\d.]+)px ([\d.]+)px\)$/,
  );
  if (!match) {
    throw new Error(`Invalid circle clipPath: ${clipPath}`);
  }
  return {
    r: Number(match[1]),
    x: Number(match[2]),
    y: Number(match[3]),
  };
}

describe("themeTransition circular reveal helpers", () => {
  it("resolves exact pointer coordinates and falls back to button center on keyboard activation", () => {
    const viewport = { width: 1600, height: 900 };
    expect(
      resolveThemeTransitionOrigin({ clientX: 1412, clientY: 28 }, viewport),
    ).toEqual({ x: 1412, y: 28 });

    expect(
      resolveThemeTransitionOrigin(
        {
          clientX: 0,
          clientY: 0,
          currentTarget: {
            getBoundingClientRect: () => ({
              left: 1400,
              top: 14,
              width: 34,
              height: 34,
            }),
          },
        },
        viewport,
      ),
    ).toEqual({ x: 1417, y: 31 });

    expect(resolveThemeTransitionOrigin(null, viewport)).toEqual({
      x: 1552,
      y: 28,
    });
  });

  it("computes an end radius that reaches past the farthest viewport corner", () => {
    const origin = { x: 1420, y: 30 };
    const viewport = { width: 1600, height: 900 };
    const radius = computeCircularRevealRadius(origin, viewport);
    const farCornerDist = Math.hypot(1420, 870);
    expect(radius).toBeGreaterThanOrEqual(Math.ceil(farCornerDist));
    expect(buildCircularRevealKeyframes(origin, radius)).toEqual([
      `circle(0px at 1420px 30px)`,
      `circle(${radius}px at 1420px 30px)`,
    ]);
  });

  it("smoothly glides top-right toggle reveals into a clean corner arc with strict monotonicity", () => {
    const viewport = { width: 1920, height: 1080 };
    const origin = { x: 1710, y: 32 };
    expect(isTopRightCornerOrigin(origin, viewport)).toBe(true);

    const plan = buildCircularRevealAnimation(origin, viewport);
    expect(plan.easing).toBe("linear");
    expect(plan.keyframes.length).toBeGreaterThan(10);

    const first = parseCircle(String(plan.keyframes[0].clipPath));
    const last = parseCircle(
      String(plan.keyframes[plan.keyframes.length - 1].clipPath),
    );
    expect(first).toEqual({ r: 0, x: 1710, y: 32 });
    expect(last.x).toBe(1920);
    expect(last.y).toBe(0);
    expect(last.r).toBeGreaterThanOrEqual(Math.ceil(Math.hypot(1920, 1080)));

    // Strict geometric monotonicity: every consecutive circle must fully contain the previous circle (||ΔC|| <= Δr).
    let prev = first;
    for (let i = 1; i < plan.keyframes.length; i += 1) {
      const cur = parseCircle(String(plan.keyframes[i].clipPath));
      const dCenter = Math.hypot(cur.x - prev.x, cur.y - prev.y);
      const dRadius = cur.r - prev.r;
      expect(dRadius).toBeGreaterThan(0);
      expect(dCenter).toBeLessThanOrEqual(dRadius + 0.05);
      prev = cur;
    }

    // By the early-mid sweep (~25% time), the arc is already anchored cleanly at the top-right corner.
    const quarterFrame = parseCircle(
      String(plan.keyframes[Math.round(plan.keyframes.length * 0.35)].clipPath),
    );
    expect(quarterFrame.x).toBe(1920);
    expect(quarterFrame.y).toBe(0);
  });

  it("keeps non-corner origins (e.g. Settings segmented control) centered symmetrically on click point", () => {
    const viewport = { width: 1440, height: 900 };
    const origin = { x: 240, y: 660 };
    expect(isTopRightCornerOrigin(origin, viewport)).toBe(false);

    const plan = buildCircularRevealAnimation(origin, viewport);
    expect(plan.easing).toBe(COLABONE_THEME_TRANSITION_EASING);
    expect(plan.keyframes).toHaveLength(2);
    expect(parseCircle(String(plan.keyframes[0].clipPath))).toEqual({
      r: 0,
      x: 240,
      y: 660,
    });
    const end = parseCircle(String(plan.keyframes[1].clipPath));
    expect(end.x).toBe(240);
    expect(end.y).toBe(660);
    expect(end.r).toBeGreaterThanOrEqual(
      Math.ceil(Math.hypot(1440 - 240, 660)),
    );
  });

  it("expands ::view-transition-new(root) outward over ~1s and cleans up transitioning class", async () => {
    const classes = new Set<string>();
    const animate = vi.fn();
    let resolveReady!: () => void;
    let resolveFinished!: () => void;
    const ready = new Promise<void>((r) => {
      resolveReady = r;
    });
    const finished = new Promise<void>((r) => {
      resolveFinished = r;
    });
    let committed = false;
    let tooltipsHiddenBeforeCapture = false;
    const onReady = vi.fn();

    const doc: TransitionDocumentLike = {
      visibilityState: "visible",
      documentElement: {
        classList: {
          add: (c) => classes.add(c),
          remove: (c) => classes.delete(c),
        },
        offsetHeight: 900,
        animate,
      },
      startViewTransition: (cb) => {
        tooltipsHiddenBeforeCapture = classes.has(
          COLABONE_THEME_HIDE_TOOLTIPS_CLASS,
        );
        void cb();
        return { ready, finished };
      },
    };

    runColabOneThemeTransition({
      doc,
      viewport: { width: 1600, height: 900 },
      origin: { clientX: 1420, clientY: 30 },
      commit: async () => {
        committed = true;
      },
      onReady,
    });

    expect(tooltipsHiddenBeforeCapture).toBe(true);
    expect(committed).toBe(true);
    expect(classes.has(COLABONE_THEME_TRANSITION_CLASS)).toBe(true);
    expect(onReady).toHaveBeenCalledTimes(0);

    resolveReady();
    await Promise.resolve();
    await Promise.resolve();

    expect(animate).toHaveBeenCalledTimes(1);
    expect(onReady).toHaveBeenCalledTimes(1);
    const [keyframes, animOpts] = animate.mock.calls[0];
    expect(Array.isArray(keyframes)).toBe(true);
    expect(keyframes[0].clipPath).toBe("circle(0px at 1420px 30px)");
    expect(keyframes[keyframes.length - 1].clipPath).toMatch(
      /^circle\(\d+px at 1600px 0px\)$/,
    );
    expect(animOpts).toMatchObject({
      duration: COLABONE_THEME_TRANSITION_MS,
      easing: "linear",
      fill: "both",
      pseudoElement: "::view-transition-new(root)",
    });
    expect(COLABONE_THEME_TRANSITION_MS).toBe(1000);

    resolveFinished();
    await Promise.resolve();
    await Promise.resolve();
    expect(classes.has(COLABONE_THEME_TRANSITION_CLASS)).toBe(false);
    expect(classes.has(COLABONE_THEME_HIDE_TOOLTIPS_CLASS)).toBe(false);
  });

  it("handles rapid overlapping toggles without clearing active transitioning class prematurely", async () => {
    const classes = new Set<string>();
    const animate = vi.fn();
    const pending: Array<{
      resolveReady: () => void;
      resolveFinished: () => void;
    }> = [];

    const doc: TransitionDocumentLike = {
      visibilityState: "visible",
      documentElement: {
        classList: {
          add: (c) => classes.add(c),
          remove: (c) => classes.delete(c),
        },
        offsetHeight: 900,
        animate,
      },
      startViewTransition: (cb) => {
        void cb();
        let resolveReady!: () => void;
        let resolveFinished!: () => void;
        const ready = new Promise<void>((r) => {
          resolveReady = r;
        });
        const finished = new Promise<void>((r) => {
          resolveFinished = r;
        });
        pending.push({ resolveReady, resolveFinished });
        return { ready, finished };
      },
    };

    runColabOneThemeTransition({
      doc,
      viewport: { width: 1280, height: 800 },
      origin: { x: 1100, y: 28 },
      commit: () => {},
    });
    runColabOneThemeTransition({
      doc,
      viewport: { width: 1280, height: 800 },
      origin: { x: 1100, y: 28 },
      commit: () => {},
    });

    pending[0].resolveReady();
    pending[0].resolveFinished();
    await Promise.resolve();
    await Promise.resolve();
    expect(animate).toHaveBeenCalledTimes(0);
    expect(classes.has(COLABONE_THEME_TRANSITION_CLASS)).toBe(true);

    pending[1].resolveReady();
    await Promise.resolve();
    await Promise.resolve();
    expect(animate).toHaveBeenCalledTimes(1);

    pending[1].resolveFinished();
    await Promise.resolve();
    await Promise.resolve();
    expect(classes.has(COLABONE_THEME_TRANSITION_CLASS)).toBe(false);
  });

  it("commits immediately when startViewTransition is unavailable or page is hidden", () => {
    let calls = 0;
    runColabOneThemeTransition({
      doc: {
        visibilityState: "visible",
        documentElement: {
          classList: { add: () => {}, remove: () => {} },
        },
      },
      commit: () => {
        calls += 1;
      },
    });
    expect(calls).toBe(1);
  });
});
