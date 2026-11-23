<script setup lang="ts">
import { computed, type CSSProperties } from "vue";
import {
  highlightColabOneCode,
  normalizeColabOneCodeLanguage,
  type ColabOneHighlightedToken,
} from "./colabOneHighlight";

const props = withDefaults(
  defineProps<{
    code: string;
    language?: string;
    maxHeight?: string;
    wrap?: boolean;
  }>(),
  {
    language: "text",
    maxHeight: "",
    wrap: false,
  },
);

const normalizedLanguage = computed(() =>
  normalizeColabOneCodeLanguage(props.language),
);

const highlightedLines = computed(() =>
  highlightColabOneCode(props.code, props.language),
);

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
  ><code><template v-for="(line, lineIdx) in highlightedLines" :key="lineIdx"><span
      v-for="(token, tokenIdx) in line"
      :key="tokenIdx"
      class="co-code-tok"
      :style="tokenStyle(token)"
    >{{ token.content }}</span><template v-if="lineIdx < highlightedLines.length - 1">{{ "\n" }}</template></template></code></pre>
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
