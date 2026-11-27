<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Input as AInput } from "ant-design-vue";
import { PhEye, PhEyeSlash } from "@phosphor-icons/vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import {
  colabOneApplyPrivacyInputPaste,
  colabOnePrivacyActive,
  colabOnePrivacyPeekResetTick,
  colabOneSynthesizeDomainInput,
  segmentColabOneDomainInput,
  type ColabOneTextSelectionRange,
} from "./colabOnePrivacy";
import { t } from "./i18n";

defineOptions({
  name: "ColabOnePrivacyInput",
});

const props = withDefaults(
  defineProps<{
    id?: string;
    value: string;
    disabled?: boolean;
    placeholder?: string;
    autocomplete?: string;
    ariaLabel?: string;
    allowClear?: boolean;
  }>(),
  {
    id: undefined,
    disabled: false,
    placeholder: "",
    autocomplete: "off",
    ariaLabel: undefined,
    allowClear: true,
  },
);

const emit = defineEmits<{
  "update:value": [string];
  "press-enter": [KeyboardEvent];
}>();

const hostRef = ref<HTMLElement | null>(null);
const inputCompRef = ref<{
  input?: unknown;
  $el?: HTMLElement;
} | null>(null);
const peeking = ref(false);
const focused = ref(false);
const savedSelection = ref<ColabOneTextSelectionRange | null>(null);

function resolveNativeInput(): HTMLInputElement | null {
  const host = hostRef.value;
  if (host) {
    const el = host.querySelector("input");
    if (el instanceof HTMLInputElement) return el;
  }
  const inst = inputCompRef.value;
  if (inst?.input instanceof HTMLInputElement) return inst.input;
  const nested = (inst?.input as { input?: unknown } | undefined)?.input;
  if (nested instanceof HTMLInputElement) return nested;
  const root = inst?.$el;
  if (root instanceof HTMLInputElement) return root;
  return root?.querySelector("input") ?? null;
}

const isMasked = computed(
  () => colabOnePrivacyActive.value && Boolean(props.value.trim()),
);

const isRevealed = computed(
  () => !isMasked.value || peeking.value || focused.value,
);

const displayedValue = computed(() =>
  isRevealed.value
    ? props.value
    : colabOneSynthesizeDomainInput(props.value),
);

const maskSegments = computed(() =>
  isMasked.value ? segmentColabOneDomainInput(props.value) : [],
);

const keepSuffixSlot = computed(
  () => isMasked.value || (!props.allowClear && colabOnePrivacyActive.value),
);

function syncNativeInputToReal(
  inputEl: HTMLInputElement,
  selection?: ColabOneTextSelectionRange | null,
) {
  const real = props.value;
  if (inputEl.value !== real) {
    inputEl.value = real;
  }
  if (selection) {
    const start = Math.max(0, Math.min(selection.start, real.length));
    const end = Math.max(start, Math.min(selection.end, real.length));
    try {
      inputEl.setSelectionRange(start, end);
    } catch {
      // Ignore on unsupported input types.
    }
  }
}

function syncNativeInputToSyntheticIfHidden() {
  if (isRevealed.value) return;
  const inputEl = resolveNativeInput();
  if (!inputEl) return;
  const expected = displayedValue.value;
  if (inputEl.value !== expected) {
    inputEl.value = expected;
  }
}

function onPeekEnter() {
  if (!isMasked.value) return;
  const wasRevealed = isRevealed.value;
  peeking.value = true;
  // Synchronously replace synthetic glyphs only when transitioning from hidden
  // to visible; never overwrite inputEl.value while already revealed (e.g.
  // active IME composition while focused).
  if (!wasRevealed) {
    const inputEl = resolveNativeInput();
    if (inputEl && inputEl.value !== props.value) {
      inputEl.value = props.value;
    }
  }
}

function onPeekLeave() {
  peeking.value = false;
  // Do not blur active focus on pointer leave so typing, right-click paste
  // context menus, and pointer drift never interrupt editing.
  syncNativeInputToSyntheticIfHidden();
}

function onDragLeave(event: DragEvent) {
  const nextTarget = event.relatedTarget as Node | null;
  if (nextTarget && hostRef.value?.contains(nextTarget)) return;
  peeking.value = false;
  syncNativeInputToSyntheticIfHidden();
}

function onFocus(event: FocusEvent) {
  if (props.disabled) return;
  const wasHidden = isMasked.value && !isRevealed.value;
  const prevSaved = savedSelection.value;
  focused.value = true;
  savedSelection.value = null;

  const inputEl =
    event.target instanceof HTMLInputElement
      ? event.target
      : resolveNativeInput();
  if (!inputEl || !isMasked.value) return;

  const hasSyntheticDomValue =
    inputEl.value !== props.value &&
    inputEl.value === colabOneSynthesizeDomainInput(props.value);
  if (!wasHidden && !hasSyntheticDomValue) return;

  const syntheticLen = inputEl.value.length;
  const curStart = inputEl.selectionStart;
  const curEnd = inputEl.selectionEnd;
  let nextSel: ColabOneTextSelectionRange | null = prevSaved;
  if (!nextSel && curStart !== null && curEnd !== null) {
    if (curStart === 0 && curEnd === syntheticLen && syntheticLen > 0) {
      nextSel = { start: 0, end: props.value.length };
    } else {
      nextSel = { start: curStart, end: curEnd };
    }
  }
  // Synchronously swap synthetic placeholder text for the real value before
  // any immediate keyboard paste or typing event arrives.
  syncNativeInputToReal(inputEl, nextSel);
}

function onBlur() {
  focused.value = false;
  savedSelection.value = null;
  syncNativeInputToSyntheticIfHidden();
}

function onPaste(event: ClipboardEvent) {
  if (props.disabled) return;
  const inputEl =
    event.target instanceof HTMLInputElement
      ? event.target
      : resolveNativeInput();
  const hasSyntheticDomValue =
    isMasked.value &&
    inputEl !== null &&
    inputEl.value !== props.value &&
    inputEl.value === colabOneSynthesizeDomainInput(props.value);

  // Normal path (empty, hovered, or focused with real DOM value): native paste
  // runs completely unrestricted.
  if (isRevealed.value && !hasSyntheticDomValue) return;

  const pasted =
    event.clipboardData?.getData("text/plain") ||
    event.clipboardData?.getData("text") ||
    "";
  if (!pasted) return;

  // Defensive fallback when paste arrives while synthetic placeholder text is
  // still in the DOM (e.g. OS clipboard picker firing right on window focus or
  // direct paste onto an unhovered masked input).
  event.preventDefault();
  const applied = colabOneApplyPrivacyInputPaste(
    props.value,
    pasted,
    savedSelection.value,
  );
  savedSelection.value = null;
  if (inputEl) {
    if (
      typeof document !== "undefined" &&
      document.activeElement !== inputEl
    ) {
      inputEl.focus({ preventScroll: true });
    }
    focused.value =
      typeof document !== "undefined" && document.activeElement === inputEl;
    if (inputEl.value !== applied.value) {
      inputEl.value = applied.value;
    }
  }
  emit("update:value", applied.value);
  nextTick(() => {
    const el = resolveNativeInput();
    if (!el) return;
    if (isRevealed.value && el.value !== applied.value) {
      el.value = applied.value;
    } else if (!isRevealed.value) {
      syncNativeInputToSyntheticIfHidden();
      return;
    }
    if (typeof document !== "undefined" && document.activeElement === el) {
      try {
        el.setSelectionRange(applied.caret, applied.caret);
      } catch {
        // Ignore on unsupported input types.
      }
    }
  });
}

function onUpdateValue(next: string) {
  if (props.disabled) return;
  if (!isRevealed.value) {
    if (next === colabOneSynthesizeDomainInput(props.value)) return;
    const inputEl = resolveNativeInput();
    if (
      typeof document !== "undefined" &&
      inputEl &&
      document.activeElement === inputEl
    ) {
      focused.value = true;
    }
  }
  emit("update:value", next);
}

function restoreFocusRevealIfActive() {
  if (typeof document === "undefined" || document.hidden || props.disabled) {
    return;
  }
  const inputEl = resolveNativeInput();
  if (!inputEl || document.activeElement !== inputEl) return;
  const prevSel = savedSelection.value;
  savedSelection.value = null;
  focused.value = true;
  if (isMasked.value) {
    syncNativeInputToReal(inputEl, prevSel);
  }
}

function onVisibilityChange() {
  if (
    typeof document !== "undefined" &&
    !document.hidden &&
    document.hasFocus?.()
  ) {
    restoreFocusRevealIfActive();
  }
}

watch(
  () => props.disabled,
  (disabled) => {
    if (disabled) {
      focused.value = false;
      savedSelection.value = null;
      syncNativeInputToSyntheticIfHidden();
    }
  },
);

watch(colabOnePrivacyActive, (active) => {
  peeking.value = false;
  if (active) {
    focused.value = false;
    savedSelection.value = null;
    const inputEl = resolveNativeInput();
    if (
      typeof document !== "undefined" &&
      inputEl &&
      document.activeElement === inputEl
    ) {
      inputEl.blur();
    }
    syncNativeInputToSyntheticIfHidden();
  }
});

watch(colabOnePrivacyPeekResetTick, () => {
  peeking.value = false;
  if (!isMasked.value) return;
  // Re-mask while window is blurred or hidden without dropping DOM focus so
  // OS clipboard pickers (such as Windows Win+V) can seamlessly paste on return.
  const inputEl = resolveNativeInput();
  if (
    typeof document !== "undefined" &&
    inputEl &&
    document.activeElement === inputEl &&
    focused.value
  ) {
    const start = inputEl.selectionStart ?? props.value.length;
    const end = inputEl.selectionEnd ?? start;
    savedSelection.value = { start, end };
  }
  focused.value = false;
  syncNativeInputToSyntheticIfHidden();
});

watch(isMasked, (masked) => {
  if (!masked) {
    peeking.value = false;
    savedSelection.value = null;
  }
});

onMounted(() => {
  if (typeof window !== "undefined") {
    window.addEventListener("focus", restoreFocusRevealIfActive);
  }
  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", onVisibilityChange);
  }
});

onBeforeUnmount(() => {
  if (typeof window !== "undefined") {
    window.removeEventListener("focus", restoreFocusRevealIfActive);
  }
  if (typeof document !== "undefined") {
    document.removeEventListener("visibilitychange", onVisibilityChange);
  }
});
</script>

<template>
  <div
    ref="hostRef"
    class="co-privacy-input-host"
    @mouseenter="onPeekEnter"
    @dragenter="onPeekEnter"
    @dragleave="onDragLeave"
    @mouseleave="onPeekLeave"
  >
    <AInput
      ref="inputCompRef"
      :id="props.id"
      :value="displayedValue"
      :disabled="props.disabled"
      :placeholder="props.placeholder"
      :autocomplete="props.autocomplete"
      :aria-label="props.ariaLabel"
      spellcheck="false"
      :allow-clear="props.allowClear"
      :class="{
        'co-privacy-masked-input': isMasked,
        'is-peeking': isMasked && isRevealed,
      }"
      :data-privacy-masked="isMasked ? 'true' : undefined"
      :data-privacy-peeking="isMasked && isRevealed ? 'true' : undefined"
      @focus="onFocus"
      @blur="onBlur"
      @paste="onPaste"
      @update:value="onUpdateValue"
      @press-enter="(event: KeyboardEvent) => emit('press-enter', event)"
    >
      <template v-if="keepSuffixSlot" #suffix>
        <ColabOneTooltip
          v-if="isMasked"
          :title="t('privacy.inputMaskedTooltip')"
        >
          <span class="co-privacy-input-badge" aria-hidden="true">
            <PhEye v-if="isRevealed" :size="15" />
            <PhEyeSlash v-else :size="15" />
          </span>
        </ColabOneTooltip>
        <span v-else class="co-privacy-input-spacer" aria-hidden="true" />
      </template>
    </AInput>
    <div
      v-if="isMasked"
      class="co-privacy-input-veil"
      :class="{
        'is-peeking': isRevealed,
        'is-disabled': props.disabled,
      }"
      aria-hidden="true"
    >
      <template v-for="(seg, idx) in maskSegments" :key="idx">
        <span v-if="seg.masked" class="co-privacy-mask">
          <span class="co-privacy-mask-glyphs">{{ seg.text }}</span>
        </span>
        <span v-else class="co-privacy-input-veil-plain">{{ seg.text }}</span>
      </template>
    </div>
  </div>
</template>
