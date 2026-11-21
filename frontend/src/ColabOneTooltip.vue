<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  ref,
  useAttrs,
  useSlots,
  watch,
} from "vue";
import { Tooltip as ATooltip } from "ant-design-vue";
import type { TooltipPlacement } from "ant-design-vue/es/tooltip";

defineOptions({
  name: "ColabOneTooltip",
  inheritAttrs: false,
});

let lastGlobalPointerDownAt = 0;
let globalPointerTrackerBound = false;

function ensureGlobalPointerTracker() {
  if (globalPointerTrackerBound || typeof window === "undefined") return;
  globalPointerTrackerBound = true;
  window.addEventListener(
    "pointerdown",
    () => {
      lastGlobalPointerDownAt = performance.now();
    },
    { capture: true, passive: true },
  );
}

ensureGlobalPointerTracker();

const props = withDefaults(
  defineProps<{
    title?: string;
    placement?: TooltipPlacement;
    disabled?: boolean;
    mouseEnterDelay?: number;
    mouseLeaveDelay?: number;
    overlayClassName?: string;
  }>(),
  {
    title: "",
    placement: "top",
    disabled: false,
    mouseEnterDelay: 0.12,
    mouseLeaveDelay: 0.04,
    overlayClassName: "",
  },
);

const attrs = useAttrs();
const slots = useSlots();
const isOpen = ref(false);

const hasTitle = computed(
  () => Boolean(slots.title) || Boolean(props.title && props.title.trim()),
);

const mergedOpen = computed(
  () => !props.disabled && hasTitle.value && isOpen.value,
);

const mergedOverlayClass = computed(() =>
  props.overlayClassName
    ? `co-tooltip-overlay ${props.overlayClassName}`
    : "co-tooltip-overlay",
);

function dismiss() {
  if (isOpen.value) {
    isOpen.value = false;
  }
}

function onOpenChange(next: boolean) {
  if (!next) {
    isOpen.value = false;
    return;
  }
  if (props.disabled || !hasTitle.value) {
    isOpen.value = false;
    return;
  }
  const enterDelayMs = Math.max(0, props.mouseEnterDelay * 1000);
  if (
    enterDelayMs > 0 &&
    lastGlobalPointerDownAt > 0 &&
    performance.now() - lastGlobalPointerDownAt < enterDelayMs
  ) {
    isOpen.value = false;
    return;
  }
  isOpen.value = true;
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") {
    dismiss();
  }
}

function onVisibilityChange() {
  if (document.visibilityState === "hidden") {
    dismiss();
  }
}

let listenersAttached = false;

function attachDismissListeners() {
  if (listenersAttached || typeof window === "undefined") return;
  listenersAttached = true;
  window.addEventListener("pointerdown", dismiss, {
    capture: true,
    passive: true,
  });
  window.addEventListener("scroll", dismiss, { capture: true, passive: true });
  window.addEventListener("blur", dismiss);
  window.addEventListener("keydown", onKeydown, { capture: true });
  document.documentElement?.addEventListener("mouseleave", dismiss);
  document.addEventListener("visibilitychange", onVisibilityChange);
}

function detachDismissListeners() {
  if (!listenersAttached || typeof window === "undefined") return;
  listenersAttached = false;
  window.removeEventListener("pointerdown", dismiss, true);
  window.removeEventListener("scroll", dismiss, true);
  window.removeEventListener("blur", dismiss);
  window.removeEventListener("keydown", onKeydown, true);
  document.documentElement?.removeEventListener("mouseleave", dismiss);
  document.removeEventListener("visibilitychange", onVisibilityChange);
}

// Always reset internal open state when suppressed so AntD's Tooltip can never
// latch a stale popup and unexpectedly re-flash when title/enabled returns.
watch(
  () => props.disabled || !hasTitle.value,
  (suppressed) => {
    if (suppressed) {
      dismiss();
    }
  },
);

watch(mergedOpen, (visible) => {
  if (visible) {
    attachDismissListeners();
  } else {
    detachDismissListeners();
  }
});

onBeforeUnmount(() => {
  dismiss();
  detachDismissListeners();
});
</script>

<template>
  <ATooltip
    v-bind="attrs"
    :open="mergedOpen"
    :title="props.title || undefined"
    :placement="props.placement"
    :mouse-enter-delay="props.mouseEnterDelay"
    :mouse-leave-delay="props.mouseLeaveDelay"
    :overlay-class-name="mergedOverlayClass"
    :destroy-tooltip-on-hide="true"
    @update:open="onOpenChange"
  >
    <template v-if="slots.title" #title>
      <slot name="title" />
    </template>
    <slot />
  </ATooltip>
</template>
