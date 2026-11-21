<script setup lang="ts">
import { computed, reactive, watch } from "vue";
import {
  PhCheckCircle,
  PhInfo,
  PhWarningCircle,
  PhX,
  PhXCircle,
} from "@phosphor-icons/vue";
import { t } from "./i18n";
import {
  resolveColabOneToastText,
  type ColabOneToastItem,
} from "./colabOneToast";

const props = withDefaults(
  defineProps<{
    items: readonly ColabOneToastItem[];
    placement?: "top" | "bottom";
    topOffsetPx?: number;
    rightOffsetPx?: number;
  }>(),
  {
    placement: "top",
    topOffsetPx: 112,
    rightOffsetPx: 0,
  },
);

const emit = defineEmits<{
  pause: [id: number];
  resume: [id: number];
  dismiss: [id: number];
}>();

const hoveredIds = reactive(new Set<number>());
const focusedIds = reactive(new Set<number>());

watch(
  () => props.items,
  (list) => {
    const activeIds = new Set(list.map((item) => item.id));
    for (const id of Array.from(hoveredIds)) {
      if (!activeIds.has(id)) hoveredIds.delete(id);
    }
    for (const id of Array.from(focusedIds)) {
      if (!activeIds.has(id)) focusedIds.delete(id);
    }
  },
  { flush: "post" },
);

const hostStyle = computed(() => {
  const style: Record<string, string> = {};
  if (props.placement === "top" && Number.isFinite(props.topOffsetPx)) {
    style["--co-toast-top-offset"] = `${Math.max(12, Math.round(props.topOffsetPx))}px`;
  }
  if (props.rightOffsetPx > 0) {
    style["--co-toast-right-offset"] = `${Math.round(props.rightOffsetPx)}px`;
  }
  return Object.keys(style).length ? style : undefined;
});

function hasActiveItem(id: number): boolean {
  return props.items.some((item) => item.id === id);
}

function syncHoldState(id: number) {
  if (!hasActiveItem(id)) {
    hoveredIds.delete(id);
    focusedIds.delete(id);
    return;
  }
  if (hoveredIds.has(id) || focusedIds.has(id)) {
    emit("pause", id);
  } else {
    emit("resume", id);
  }
}

function onPointerEnter(id: number) {
  if (!hasActiveItem(id)) return;
  hoveredIds.add(id);
  syncHoldState(id);
}

function onPointerLeave(id: number) {
  hoveredIds.delete(id);
  syncHoldState(id);
}

function onFocusIn(id: number) {
  if (!hasActiveItem(id)) return;
  focusedIds.add(id);
  syncHoldState(id);
}

function onFocusOut(id: number, event: FocusEvent) {
  const current = event.currentTarget as HTMLElement | null;
  const next = event.relatedTarget as Node | null;
  if (current && next && current.contains(next)) return;
  focusedIds.delete(id);
  syncHoldState(id);
}

function onDismiss(id: number) {
  hoveredIds.delete(id);
  focusedIds.delete(id);
  emit("dismiss", id);
}

function isAssertive(item: ColabOneToastItem): boolean {
  return item.kind === "error" || item.kind === "warning";
}
</script>

<template>
  <section
    class="co-toast-host"
    :class="`is-${props.placement}`"
    :style="hostStyle"
    :aria-label="t('common.notificationsAria')"
  >
    <TransitionGroup name="co-toast-stack" tag="div" class="co-toast-stack">
      <div
        v-for="item in props.items"
        :key="item.id"
        class="co-toast"
        :class="[`is-${item.kind}`, { 'is-paused': item.paused }]"
        :data-toast-kind="item.kind"
        :data-toast-id="item.id"
        :data-toast-revision="item.revision"
        :data-paused="item.paused ? 'true' : 'false'"
        :role="isAssertive(item) ? 'alert' : 'status'"
        :aria-live="isAssertive(item) ? 'assertive' : 'polite'"
        @pointerenter="onPointerEnter(item.id)"
        @pointerleave="onPointerLeave(item.id)"
        @pointercancel="onPointerLeave(item.id)"
        @focusin="onFocusIn(item.id)"
        @focusout="onFocusOut(item.id, $event)"
      >
        <span class="co-toast-icon" aria-hidden="true">
          <PhXCircle v-if="item.kind === 'error'" :size="18" weight="fill" />
          <PhWarningCircle
            v-else-if="item.kind === 'warning'"
            :size="18"
            weight="fill"
          />
          <PhInfo v-else-if="item.kind === 'info'" :size="18" weight="fill" />
          <PhCheckCircle v-else :size="18" weight="fill" />
        </span>

        <span class="co-toast-message">{{ resolveColabOneToastText(item) }}</span>

        <button
          type="button"
          class="co-toast-close"
          :aria-label="t('common.close')"
          @click="onDismiss(item.id)"
        >
          <PhX :size="14" weight="bold" aria-hidden="true" />
        </button>

        <span class="co-toast-progress" aria-hidden="true">
          <span
            :key="`${item.id}-${item.revision}`"
            class="co-toast-progress-bar"
            :style="{
              animationDuration: `${item.durationMs}ms`,
              animationPlayState: item.paused ? 'paused' : 'running',
            }"
          />
        </span>
      </div>
    </TransitionGroup>
  </section>
</template>

<style scoped>
.co-toast-host {
  position: fixed;
  left: calc((100vw - var(--co-toast-right-offset, 0px)) / 2);
  transform: translateX(-50%);
  z-index: 1200;
  width: min(560px, calc(100vw - var(--co-toast-right-offset, 0px) - 32px));
  pointer-events: none;
  display: flex;
  flex-direction: column;
  align-items: center;
}

.co-toast-host.is-top {
  top: var(--co-toast-top-offset, 112px);
}

.co-toast-host.is-bottom {
  bottom: 62px;
}

.co-toast-stack {
  position: relative;
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

.co-toast {
  --co-toast-accent: var(--co-success);
  --co-toast-border: var(--co-success-border);
  --co-toast-track: rgba(16, 185, 129, 0.14);

  position: relative;
  pointer-events: auto;
  display: inline-flex;
  align-items: flex-start;
  gap: 10px;
  max-width: 100%;
  min-width: 220px;
  padding: 11px 12px 13px 14px;
  border-radius: 12px;
  background: var(--co-toast-bg);
  color: var(--co-text);
  border: 1px solid var(--co-toast-border);
  box-shadow: var(--co-toast-shadow);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  overflow: hidden;
  font-size: 13.5px;
  line-height: 1.5;
  font-weight: 500;
}

.co-toast.is-error {
  --co-toast-accent: var(--co-danger);
  --co-toast-border: var(--co-danger-border);
  --co-toast-track: rgba(239, 68, 68, 0.14);
}

.co-toast.is-warning {
  --co-toast-accent: var(--co-warning);
  --co-toast-border: var(--co-warning-border);
  --co-toast-track: rgba(245, 158, 11, 0.16);
}

.co-toast.is-info {
  --co-toast-accent: var(--co-accent);
  --co-toast-border: rgba(59, 130, 246, 0.32);
  --co-toast-track: rgba(59, 130, 246, 0.14);
}

.co-toast.is-success {
  --co-toast-accent: var(--co-success);
  --co-toast-border: var(--co-success-border);
  --co-toast-track: rgba(16, 185, 129, 0.14);
}

.co-toast-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-top: 1px;
  color: var(--co-toast-accent);
}

.co-toast-message {
  flex: 1 1 auto;
  min-width: 0;
  color: var(--co-text);
  word-break: break-word;
  user-select: text;
}

.co-toast-close {
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  margin: -1px -2px 0 2px;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--co-muted);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition:
    background-color 0.14s ease,
    color 0.14s ease;
}

.co-toast-close:hover {
  background: var(--co-soft-hover);
  color: var(--co-text);
}

.co-toast-close:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 1px;
}

.co-toast-progress {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 3px;
  background: var(--co-toast-track);
  pointer-events: none;
}

.co-toast-progress-bar {
  display: block;
  width: 100%;
  height: 100%;
  border-radius: 0 999px 999px 0;
  background: var(--co-toast-accent);
  transform-origin: left center;
  animation-name: co-toast-countdown;
  animation-timing-function: linear;
  animation-fill-mode: forwards;
  will-change: transform;
  opacity: 0.9;
}

.co-toast.is-paused .co-toast-progress-bar {
  opacity: 1;
}

@keyframes co-toast-countdown {
  from {
    transform: scaleX(1);
  }
  to {
    transform: scaleX(0);
  }
}

.co-toast-stack-enter-active,
.co-toast-stack-leave-active {
  transition:
    opacity 0.2s cubic-bezier(0.16, 1, 0.3, 1),
    transform 0.22s cubic-bezier(0.16, 1, 0.3, 1);
}

.co-toast-stack-leave-active {
  pointer-events: none;
  position: absolute;
}

.co-toast-stack-move {
  transition: transform 0.22s cubic-bezier(0.16, 1, 0.3, 1);
}

.co-toast-host.is-top .co-toast-stack-enter-from {
  opacity: 0;
  transform: translateY(-10px) scale(0.98);
}

.co-toast-host.is-top .co-toast-stack-leave-to {
  opacity: 0;
  transform: translateY(-6px) scale(0.98);
}

.co-toast-host.is-bottom .co-toast-stack-enter-from,
.co-toast-host.is-bottom .co-toast-stack-leave-to {
  opacity: 0;
  transform: translateY(8px) scale(0.98);
}

@media (prefers-reduced-motion: reduce) {
  .co-toast-stack-enter-active,
  .co-toast-stack-leave-active,
  .co-toast-stack-move {
    transition: opacity 0.14s ease;
  }

  .co-toast-host.is-top .co-toast-stack-enter-from,
  .co-toast-host.is-top .co-toast-stack-leave-to,
  .co-toast-host.is-bottom .co-toast-stack-enter-from,
  .co-toast-host.is-bottom .co-toast-stack-leave-to {
    transform: none;
  }
}
</style>
