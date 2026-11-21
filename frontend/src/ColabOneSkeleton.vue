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
  text: "6px",
  metric: "8px",
  pill: "9999px",
  tag: "9999px",
  circle: "9999px",
  button: "10px",
  rect: "12px",
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
  box-shadow: inset 0 0 0 1px var(--co-skeleton-border);
  pointer-events: none;
  user-select: none;
}

.co-skeleton.is-inline {
  display: inline-block;
  vertical-align: middle;
}

.co-skeleton--subtle {
  background: var(--co-skeleton-base-subtle);
}

.co-skeleton--accent {
  background: var(--co-skeleton-accent-base);
}

.co-skeleton::after {
  content: "";
  position: absolute;
  inset: 0;
  background: linear-gradient(
    105deg,
    transparent 14%,
    var(--co-skeleton-sheen) 46%,
    var(--co-skeleton-tint) 55%,
    transparent 86%
  );
  transform: translate3d(-130%, 0, 0);
  animation: co-skeleton-sweep 1.85s cubic-bezier(0.4, 0, 0.2, 1) infinite;
  animation-delay: var(--co-sk-delay, 0ms);
  will-change: transform;
}

@media (prefers-reduced-motion: reduce) {
  .co-skeleton::after {
    animation: none;
    transform: none;
    opacity: 0;
  }
}
</style>
