<script setup lang="ts">
import { computed } from "vue";

export type ColabOneSkeletonVariant =
  | "text"
  | "metric"
  | "pill"
  | "tag"
  | "circle"
  | "button"
  | "rect"
  | "bar";

export type ColabOneSkeletonTone = "default" | "subtle" | "accent";

const props = withDefaults(
  defineProps<{
    width?: string | number;
    height?: string | number;
    radius?: string | number;
    variant?: ColabOneSkeletonVariant;
    tone?: ColabOneSkeletonTone;
    delay?: number;
    inline?: boolean;
  }>(),
  {
    width: "100%",
    height: undefined,
    radius: undefined,
    variant: "text",
    tone: "default",
    delay: 0,
    inline: false,
  },
);

function formatSize(val: string | number): string {
  return typeof val === "number" ? `${val}px` : val;
}

const DEFAULT_HEIGHT: Record<ColabOneSkeletonVariant, string> = {
  text: "14px",
  metric: "24px",
  pill: "22px",
  tag: "20px",
  circle: "16px",
  button: "32px",
  rect: "64px",
  bar: "6px",
};

const DEFAULT_RADIUS: Record<ColabOneSkeletonVariant, string> = {
  text: "4px",
  metric: "6px",
  pill: "9999px",
  tag: "6px",
  circle: "9999px",
  button: "8px",
  rect: "10px",
  bar: "9999px",
};

const resolvedStyle = computed<Record<string, string>>(() => {
  const height =
    props.height !== undefined
      ? formatSize(props.height)
      : DEFAULT_HEIGHT[props.variant];
  const width =
    props.variant === "circle" && props.width === "100%"
      ? height
      : formatSize(props.width);
  const radius =
    props.radius !== undefined
      ? formatSize(props.radius)
      : DEFAULT_RADIUS[props.variant];

  const style: Record<string, string> = {
    width,
    height,
    borderRadius: radius,
  };
  if (props.delay > 0) {
    style["--co-sk-delay"] = `${props.delay}ms`;
  }
  return style;
});
</script>

<template>
  <span
    class="co-skeleton"
    :class="[
      `co-skeleton--${variant}`,
      `co-skeleton--${tone}`,
      { 'is-inline': inline },
    ]"
    :style="resolvedStyle"
    aria-hidden="true"
  ></span>
</template>

<style scoped>
.co-skeleton {
  position: relative;
  display: block;
  max-width: 100%;
  box-sizing: border-box;
  overflow: hidden;
  flex-shrink: 0;
  background: var(--co-skeleton-base);
  pointer-events: none;
  user-select: none;
}

.co-skeleton.is-inline {
  display: inline-block;
  vertical-align: middle;
  align-self: center;
}

.co-skeleton--subtle {
  background: var(--co-skeleton-base-subtle);
}

.co-skeleton--accent {
  background: var(--co-skeleton-accent-base);
}

/* Seamless 2-period tiled wave shifted across -50% -> 0% on the GPU:
 * continuously alive at any aspect ratio without borders or dead pauses. */
.co-skeleton::after {
  content: "";
  position: absolute;
  inset: 0;
  width: 400%;
  background: linear-gradient(
    90deg,
    transparent 0%,
    transparent 9%,
    var(--co-skeleton-sheen-soft) 18%,
    var(--co-skeleton-sheen) 25%,
    var(--co-skeleton-sheen-soft) 32%,
    transparent 41%,
    transparent 59%,
    var(--co-skeleton-sheen-soft) 68%,
    var(--co-skeleton-sheen) 75%,
    var(--co-skeleton-sheen-soft) 82%,
    transparent 91%,
    transparent 100%
  );
  transform: translate3d(-50%, 0, 0);
  animation: co-skeleton-wave 1.55s linear infinite;
  animation-delay: calc(var(--co-sk-delay, 0ms) - 3100ms);
  will-change: transform;
}

/* Gemini-inspired soft iridescent blue/lavender wave for live AI & streaming states */
.co-skeleton--accent::after {
  background: linear-gradient(
    90deg,
    transparent 0%,
    transparent 6%,
    var(--co-skeleton-accent-mid) 16%,
    var(--co-skeleton-accent-sheen) 25%,
    var(--co-skeleton-accent-iris) 34%,
    transparent 44%,
    transparent 56%,
    var(--co-skeleton-accent-mid) 66%,
    var(--co-skeleton-accent-sheen) 75%,
    var(--co-skeleton-accent-iris) 84%,
    transparent 94%,
    transparent 100%
  );
  animation-duration: 1.75s;
  animation-delay: calc(var(--co-sk-delay, 0ms) - 3500ms);
}

@media (prefers-reduced-motion: reduce) {
  .co-skeleton::after {
    animation: none;
    transform: none;
    opacity: 0;
  }
}
</style>
