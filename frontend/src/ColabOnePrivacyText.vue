<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  colabOnePrivacyActive,
  colabOnePrivacyContextKey,
  colabOnePrivacyPeekResetTick,
  colabOneSynthesizeEmail,
  colabOneSynthesizeHost,
  isColabOnePublicHost,
  segmentColabOnePrivacyText,
  segmentColabOneUrlText,
  type ColabOnePrivacyContext,
  type ColabOnePrivacySegment,
} from "./colabOnePrivacy";
import { t } from "./i18n";

defineOptions({
  name: "ColabOnePrivacyText",
});

const props = withDefaults(
  defineProps<{
    text: string;
    kind?: "auto" | "email" | "host" | "url";
    context?: ColabOnePrivacyContext;
    active?: boolean;
  }>(),
  {
    kind: "auto",
    context: undefined,
    active: undefined,
  },
);

const isMasked = computed(() =>
  props.active !== undefined ? props.active : colabOnePrivacyActive.value,
);

const hoveredIdx = ref<number | null>(null);
const peekingMinWidthPx = ref<number>(0);

function clearHover() {
  hoveredIdx.value = null;
  peekingMinWidthPx.value = 0;
}

function onMaskEnter(idx: number, event: MouseEvent) {
  if (hoveredIdx.value === idx) return;
  const target = event.currentTarget as HTMLElement | null;
  const width = target?.getBoundingClientRect().width ?? 0;
  peekingMinWidthPx.value = width > 0 ? Math.ceil(width) : 0;
  hoveredIdx.value = idx;
}

function onMaskLeave(idx: number) {
  if (hoveredIdx.value === idx) {
    clearHover();
  }
}

const segments = computed<ColabOnePrivacySegment[]>(() => {
  const raw = String(props.text ?? "");
  if (!raw) return [];
  if (!isMasked.value) {
    return [{ text: raw, masked: false }];
  }
  if (props.kind === "email") {
    return [
      {
        text: colabOneSynthesizeEmail(raw),
        masked: true,
        original: raw,
      },
    ];
  }
  if (props.kind === "host") {
    if (isColabOnePublicHost(raw)) {
      return [{ text: raw, masked: false }];
    }
    return [
      {
        text: colabOneSynthesizeHost(raw),
        masked: true,
        original: raw,
      },
    ];
  }
  if (props.kind === "url") {
    return segmentColabOneUrlText(raw);
  }
  return segmentColabOnePrivacyText(raw, props.context);
});

const contextKey = computed(() => colabOnePrivacyContextKey(props.context));

watch(
  [isMasked, () => props.text, () => props.kind, contextKey, colabOnePrivacyPeekResetTick],
  clearHover,
);

const maskedAriaLabel = computed(() => t("privacy.maskedFieldAria"));
</script>

<template>
  <template v-for="(seg, idx) in segments" :key="idx">
    <span
      v-if="seg.masked"
      class="co-privacy-mask"
      :class="{ 'is-peeking': hoveredIdx === idx }"
      data-privacy-masked="true"
      :data-privacy-peeking="hoveredIdx === idx ? 'true' : undefined"
      :aria-label="hoveredIdx === idx ? undefined : maskedAriaLabel"
      :style="
        hoveredIdx === idx && peekingMinWidthPx
          ? { minWidth: `${peekingMinWidthPx}px` }
          : undefined
      "
      @mouseenter="onMaskEnter(idx, $event)"
      @mouseleave="onMaskLeave(idx)"
      ><span
        class="co-privacy-mask-glyphs"
        :aria-hidden="hoveredIdx === idx ? undefined : 'true'"
        >{{ hoveredIdx === idx ? (seg.original ?? seg.text) : seg.text }}</span
      ></span
    ><template v-else>{{ seg.text }}</template>
  </template>
</template>
