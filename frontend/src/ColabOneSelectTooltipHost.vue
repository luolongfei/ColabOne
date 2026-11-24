<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import {
  COLABONE_SELECT_TOOLTIP_ENTER_DELAY_MS,
  computeColabOneFloatingTooltipPosition,
  extractColabOneSelectHoverInfo,
  restoreColabOneNativeTitles,
  stashColabOneNativeTitles,
} from "./colabOneSelectOverflow";

defineOptions({
  name: "ColabOneSelectTooltipHost",
});

const visible = ref(false);
const positioned = ref(false);
const tooltipText = ref("");
const tooltipLeft = ref(0);
const tooltipTop = ref(0);
const tooltipPlacement = ref<"top" | "bottom">("top");
const tooltipRef = ref<HTMLElement | null>(null);

let activeAnchor: HTMLElement | null = null;
let pendingAnchor: HTMLElement | null = null;
let pendingText = "";
let enterTimer: ReturnType<typeof setTimeout> | null = null;
let stashedTitleElements: HTMLElement[] = [];
let renderToken = 0;
let lastPointerDownAt = 0;
let lastPointerDownAnchor: HTMLElement | null = null;

function clearEnterTimer() {
  if (enterTimer !== null) {
    clearTimeout(enterTimer);
    enterTimer = null;
  }
}

function restoreStashedTitles() {
  if (stashedTitleElements.length > 0) {
    restoreColabOneNativeTitles(stashedTitleElements);
    stashedTitleElements = [];
  }
}

function dismiss() {
  renderToken += 1;
  clearEnterTimer();
  pendingAnchor = null;
  pendingText = "";
  activeAnchor = null;
  visible.value = false;
  positioned.value = false;
  tooltipLeft.value = 0;
  tooltipTop.value = 0;
  tooltipText.value = "";
  restoreStashedTitles();
}

async function showTooltipFor(anchor: HTMLElement, fallbackText: string) {
  const token = ++renderToken;
  if (!anchor.isConnected) {
    dismiss();
    return;
  }

  const freshInfo = extractColabOneSelectHoverInfo(anchor);
  if (!freshInfo || freshInfo.anchor !== anchor) {
    dismiss();
    return;
  }

  const initialAnchorRect = anchor.getBoundingClientRect();
  if (initialAnchorRect.width <= 0 || initialAnchorRect.height <= 0) {
    dismiss();
    return;
  }

  activeAnchor = anchor;
  pendingAnchor = null;
  tooltipText.value = freshInfo.text || fallbackText;
  tooltipLeft.value = 0;
  tooltipTop.value = 0;
  positioned.value = false;
  visible.value = true;

  await nextTick();
  if (
    token !== renderToken ||
    !visible.value ||
    activeAnchor !== anchor ||
    !anchor.isConnected
  ) {
    return;
  }

  const tipEl = tooltipRef.value;
  if (!tipEl) return;

  const anchorRect = anchor.getBoundingClientRect();
  if (anchorRect.width <= 0 || anchorRect.height <= 0) {
    dismiss();
    return;
  }

  const pos = computeColabOneFloatingTooltipPosition(
    anchorRect,
    {
      width: tipEl.offsetWidth,
      height: tipEl.offsetHeight,
    },
    {
      width: window.innerWidth,
      height: window.innerHeight,
    },
  );
  tooltipLeft.value = pos.left;
  tooltipTop.value = pos.top;
  tooltipPlacement.value = pos.placement;
  positioned.value = true;
}

function onMouseOver(event: MouseEvent) {
  const info = extractColabOneSelectHoverInfo(event.target);
  if (!info) {
    const current = activeAnchor || pendingAnchor;
    const targetNode = event.target as Node | null;
    if (current && (!targetNode || !current.contains(targetNode))) {
      dismiss();
    }
    return;
  }

  if (
    lastPointerDownAt > 0 &&
    info.anchor === lastPointerDownAnchor &&
    performance.now() - lastPointerDownAt <
      COLABONE_SELECT_TOOLTIP_ENTER_DELAY_MS
  ) {
    return;
  }

  if (info.anchor === activeAnchor && visible.value) {
    const newlyStashed = stashColabOneNativeTitles(info.anchor);
    if (newlyStashed.length > 0) {
      stashedTitleElements = Array.from(
        new Set([...stashedTitleElements, ...newlyStashed]),
      );
    }
    if (info.text !== tooltipText.value) {
      void showTooltipFor(info.anchor, info.text);
    }
    return;
  }

  if (info.anchor === pendingAnchor && enterTimer !== null) {
    pendingText = info.text;
    return;
  }

  dismiss();
  pendingAnchor = info.anchor;
  pendingText = info.text;
  stashedTitleElements = stashColabOneNativeTitles(info.anchor);

  enterTimer = setTimeout(() => {
    enterTimer = null;
    const targetAnchor = pendingAnchor;
    const text = pendingText;
    if (!targetAnchor) return;
    void showTooltipFor(targetAnchor, text);
  }, COLABONE_SELECT_TOOLTIP_ENTER_DELAY_MS);
}

function onMouseOut(event: MouseEvent) {
  const current = activeAnchor || pendingAnchor;
  if (!current) return;
  const related = event.relatedTarget as Node | null;
  if (related && current.contains(related)) return;
  if (related && extractColabOneSelectHoverInfo(related)?.anchor === current) {
    return;
  }
  dismiss();
}

function onPointerDown(event: PointerEvent) {
  lastPointerDownAt = performance.now();
  lastPointerDownAnchor =
    extractColabOneSelectHoverInfo(event.target)?.anchor ?? activeAnchor;
  dismiss();
}

function onVisibilityChange() {
  if (document.visibilityState === "hidden") {
    dismiss();
  }
}

onMounted(() => {
  if (typeof window === "undefined" || typeof document === "undefined") return;
  document.addEventListener("mouseover", onMouseOver, { passive: true });
  document.addEventListener("mouseout", onMouseOut, { passive: true });
  window.addEventListener("pointerdown", onPointerDown, {
    capture: true,
    passive: true,
  });
  window.addEventListener("scroll", dismiss, { capture: true, passive: true });
  window.addEventListener("wheel", dismiss, { capture: true, passive: true });
  window.addEventListener("keydown", dismiss, { capture: true, passive: true });
  window.addEventListener("blur", dismiss);
  window.addEventListener("resize", dismiss, { passive: true });
  document.addEventListener("visibilitychange", onVisibilityChange);
  document.documentElement?.addEventListener("mouseleave", dismiss);
});

onBeforeUnmount(() => {
  dismiss();
  lastPointerDownAnchor = null;
  if (typeof window === "undefined" || typeof document === "undefined") return;
  document.removeEventListener("mouseover", onMouseOver);
  document.removeEventListener("mouseout", onMouseOut);
  window.removeEventListener("pointerdown", onPointerDown, true);
  window.removeEventListener("scroll", dismiss, true);
  window.removeEventListener("wheel", dismiss, true);
  window.removeEventListener("keydown", dismiss, true);
  window.removeEventListener("blur", dismiss);
  window.removeEventListener("resize", dismiss);
  document.removeEventListener("visibilitychange", onVisibilityChange);
  document.documentElement?.removeEventListener("mouseleave", dismiss);
});
</script>

<template>
  <Teleport to="body">
    <div
      v-if="visible"
      ref="tooltipRef"
      role="tooltip"
      class="co-select-overflow-tooltip"
      :class="[
        `is-${tooltipPlacement}`,
        { 'is-positioned': positioned },
      ]"
      :style="{
        left: `${tooltipLeft}px`,
        top: `${tooltipTop}px`,
      }"
    >
      {{ tooltipText }}
    </div>
  </Teleport>
</template>
