<script setup lang="ts">
import { computed, ref, watch, type CSSProperties } from "vue";
import {
  highlightColabOneCode,
  normalizeColabOneCodeLanguage,
  type ColabOneHighlightedToken,
} from "./colabOneHighlight";
import {
  applyColabOnePrivacyToCodeLines,
  colabOnePrivacyContextKey,
  colabOnePrivacyPeekResetTick,
  type ColabOnePrivacyContext,
} from "./colabOnePrivacy";
import { t } from "./i18n";

const props = withDefaults(
  defineProps<{
    code: string;
    language?: string;
    maxHeight?: string;
    wrap?: boolean;
    privacyMask?: boolean;
    privacyContext?: ColabOnePrivacyContext;
  }>(),
  {
    language: "text",
    maxHeight: "",
    wrap: false,
    privacyMask: false,
    privacyContext: undefined,
  },
);

const normalizedLanguage = computed(() =>
  normalizeColabOneCodeLanguage(props.language),
);

const highlightedLines = computed(() =>
  highlightColabOneCode(props.code, props.language),
);

const renderedLines = computed(() =>
  applyColabOnePrivacyToCodeLines(
    highlightedLines.value,
    props.privacyMask,
    props.privacyContext,
  ),
);

const maskedAriaLabel = computed(() => t("privacy.maskedFieldAria"));

const hoveredTokenKey = ref<string | null>(null);
const peekingMinWidthPx = ref<number>(0);

function clearHover() {
  hoveredTokenKey.value = null;
  peekingMinWidthPx.value = 0;
}

function tokenKey(lineIdx: number, tokenIdx: number): string {
  return `${lineIdx}:${tokenIdx}`;
}

function isPeekingToken(lineIdx: number, tokenIdx: number): boolean {
  return hoveredTokenKey.value === tokenKey(lineIdx, tokenIdx);
}

function onTokenEnter(lineIdx: number, tokenIdx: number, event: MouseEvent) {
  const key = tokenKey(lineIdx, tokenIdx);
  if (hoveredTokenKey.value === key) return;
  const target = event.currentTarget as HTMLElement | null;
  const width = target?.getBoundingClientRect().width ?? 0;
  peekingMinWidthPx.value = width > 0 ? Math.ceil(width) : 0;
  hoveredTokenKey.value = key;
}

function onTokenLeave(lineIdx: number, tokenIdx: number) {
  if (hoveredTokenKey.value === tokenKey(lineIdx, tokenIdx)) {
    clearHover();
  }
}

const contextKey = computed(() =>
  colabOnePrivacyContextKey(props.privacyContext),
);

watch(
  [
    () => props.privacyMask,
    () => props.code,
    normalizedLanguage,
    contextKey,
    colabOnePrivacyPeekResetTick,
  ],
  clearHover,
);

function maskedTokenStyle(
  token: ColabOneHighlightedToken,
  lineIdx: number,
  tokenIdx: number,
): CSSProperties | undefined {
  const base = tokenStyle(token);
  if (!isPeekingToken(lineIdx, tokenIdx) || !peekingMinWidthPx.value) {
    return base;
  }
  return {
    ...(base || {}),
    minWidth: `${peekingMinWidthPx.value}px`,
  };
}

const preStyle = computed<CSSProperties | undefined>(() =>
  props.maxHeight ? { maxHeight: props.maxHeight } : undefined,
);

function tokenStyle(
  token: ColabOneHighlightedToken,
): CSSProperties | undefined {
  const style: CSSProperties = {};
  if (token.color) style.color = token.color;
  if (token.fontStyle) style.fontStyle = token.fontStyle;
  if (token.fontWeight) style.fontWeight = token.fontWeight;
  return Object.keys(style).length ? style : undefined;
}
</script>

<template>
  <pre
    class="co-code-block co-code-highlighted"
    :class="{ 'is-wrap': props.wrap }"
    :style="preStyle"
    :data-language="normalizedLanguage"
    tabindex="0"
  ><code><template v-for="(line, lineIdx) in renderedLines" :key="lineIdx"><template
      v-for="(token, tokenIdx) in line"
      :key="tokenIdx"
    ><span
      v-if="token.masked"
      class="co-code-tok co-privacy-mask co-privacy-mask-code"
      :class="{ 'is-peeking': isPeekingToken(lineIdx, tokenIdx) }"
      data-privacy-masked="true"
      :data-privacy-peeking="isPeekingToken(lineIdx, tokenIdx) ? 'true' : undefined"
      :aria-label="isPeekingToken(lineIdx, tokenIdx) ? undefined : maskedAriaLabel"
      :style="maskedTokenStyle(token, lineIdx, tokenIdx)"
      @mouseenter="onTokenEnter(lineIdx, tokenIdx, $event)"
      @mouseleave="onTokenLeave(lineIdx, tokenIdx)"
    ><span
      class="co-privacy-mask-glyphs"
      :aria-hidden="isPeekingToken(lineIdx, tokenIdx) ? undefined : 'true'"
    >{{
      isPeekingToken(lineIdx, tokenIdx)
        ? (token.original ?? token.content)
        : token.content
    }}</span></span><span
      v-else
      class="co-code-tok"
      :style="tokenStyle(token)"
    >{{ token.content }}</span></template><template v-if="lineIdx < renderedLines.length - 1">{{ "\n" }}</template></template></code></pre>
</template>

<style scoped>
.co-code-highlighted {
  padding: 14px 16px;
  border: 1px solid var(--co-code-border);
  border-radius: var(--co-radius-md);
  background: var(--co-code-bg);
  color: var(--co-code-text);
  box-shadow: var(--co-code-shadow);
  color-scheme: dark;
  font-family: var(--co-font-mono);
  font-size: 13px;
  line-height: 1.6;
  tab-size: 2;
  overflow: auto;
  white-space: pre;
  word-break: normal;
  transition: border-color 0.16s ease;
}

.co-code-highlighted.is-wrap {
  white-space: pre-wrap;
  word-break: break-word;
}

.co-code-highlighted:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 1px;
}

.co-code-highlighted code {
  display: block;
  font-family: inherit;
  font-size: inherit;
  line-height: inherit;
  background: transparent;
  padding: 0;
  border: 0;
}

.co-code-highlighted ::selection {
  background: var(--co-code-selection);
}
</style>
