<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { Input as AInput } from "ant-design-vue";
import { PhEye, PhEyeSlash, PhSpinnerGap } from "@phosphor-icons/vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { t } from "./i18n";
import {
  colabOneResolveSecretInput,
  colabOneResolveSecretPaste,
  type ColabOneSecretExtractor,
} from "./colabOneSecretToken";

defineOptions({
  name: "ColabOneSecretInput",
});

const props = withDefaults(
  defineProps<{
    id?: string;
    value: string;
    hasSaved?: boolean;
    disabled?: boolean;
    placeholder?: string;
    savedPlaceholder?: string;
    autocomplete?: string;
    ariaLabel?: string;
    loadSaved?: () => Promise<string>;
    extractSecret?: ColabOneSecretExtractor;
  }>(),
  {
    id: undefined,
    hasSaved: false,
    disabled: false,
    placeholder: "",
    savedPlaceholder: "",
    autocomplete: "off",
    ariaLabel: undefined,
    loadSaved: undefined,
    extractSecret: undefined,
  },
);

const emit = defineEmits<{
  "update:value": [string];
  extracted: [string];
  error: [string];
}>();

const inputCompRef = ref<{
  input?: HTMLInputElement | null;
  $el?: HTMLElement;
} | null>(null);
const visible = ref(false);
const showingSaved = ref(false);
const revealedSaved = ref("");
const loadingSaved = ref(false);
let revealSeq = 0;
let unmounted = false;

function resolveNativeInput(): HTMLInputElement | null {
  const inst = inputCompRef.value;
  if (inst?.input instanceof HTMLInputElement) return inst.input;
  const root = inst?.$el;
  if (root instanceof HTMLInputElement) return root;
  return root?.querySelector("input") ?? null;
}

const displayValue = computed(() =>
  showingSaved.value ? revealedSaved.value : props.value,
);

const effectivePlaceholder = computed(() =>
  props.hasSaved && !props.value && !showingSaved.value && props.savedPlaceholder
    ? props.savedPlaceholder
    : props.placeholder,
);

const isLockedReveal = computed(() =>
  Boolean(props.disabled && showingSaved.value),
);

const toggleDisabled = computed(
  () =>
    loadingSaved.value ||
    (props.disabled && !props.hasSaved && !props.value && !showingSaved.value),
);

const toggleLabel = computed(() => {
  if (visible.value) return t("common.hideSecret");
  if (props.hasSaved && !props.value && props.loadSaved) {
    return t("common.revealSavedSecret");
  }
  return t("common.showSecret");
});

function hide() {
  revealSeq += 1;
  loadingSaved.value = false;
  visible.value = false;
  showingSaved.value = false;
  revealedSaved.value = "";
}

async function toggleVisibility() {
  if (toggleDisabled.value) return;

  if (visible.value) {
    hide();
    return;
  }

  if (!props.value && props.hasSaved && props.loadSaved) {
    const seq = ++revealSeq;
    loadingSaved.value = true;
    try {
      const saved = await props.loadSaved();
      if (unmounted || seq !== revealSeq) return;
      if (props.value) {
        showingSaved.value = false;
        revealedSaved.value = "";
        visible.value = true;
        return;
      }
      if (saved) {
        revealedSaved.value = saved;
        showingSaved.value = true;
        visible.value = true;
      } else {
        showingSaved.value = false;
        revealedSaved.value = "";
        visible.value = true;
      }
    } catch (err) {
      if (!unmounted && seq === revealSeq) {
        emit("error", String(err));
      }
    } finally {
      if (seq === revealSeq) {
        loadingSaved.value = false;
      }
    }
    return;
  }

  visible.value = true;
}

function applyNormalizedValue(value: string, extractedFromWrapper: boolean) {
  emit("update:value", value);
  if (extractedFromWrapper) {
    emit("extracted", value);
  }
  nextTick(() => {
    const inputEl = resolveNativeInput();
    if (inputEl) {
      if (inputEl.value !== value) {
        inputEl.value = value;
      }
      const end = value.length;
      try {
        inputEl.setSelectionRange(end, end);
      } catch {
        // Ignore on unsupported input types.
      }
    }
  });
}

function onPaste(event: ClipboardEvent) {
  if (props.disabled) {
    event.preventDefault();
    return;
  }
  const pasted = event.clipboardData?.getData("text");
  if (!pasted) return;

  const inputEl = resolveNativeInput();
  const resolved = colabOneResolveSecretPaste(
    displayValue.value,
    inputEl?.selectionStart ?? null,
    inputEl?.selectionEnd ?? null,
    pasted,
    props.extractSecret,
  );
  if (!resolved) return;

  event.preventDefault();
  if (showingSaved.value) {
    revealSeq += 1;
    loadingSaved.value = false;
    showingSaved.value = false;
    revealedSaved.value = "";
  }
  applyNormalizedValue(resolved.value, resolved.extractedFromWrapper);
}

function onUpdateValue(raw: string) {
  if (props.disabled) return;
  if (showingSaved.value) {
    revealSeq += 1;
    loadingSaved.value = false;
    showingSaved.value = false;
    revealedSaved.value = "";
    if (!raw) {
      visible.value = false;
      emit("update:value", "");
      return;
    }
  }
  const normalized = colabOneResolveSecretInput(raw, props.extractSecret);
  if (normalized) {
    applyNormalizedValue(normalized.value, normalized.extractedFromWrapper);
    return;
  }
  emit("update:value", raw);
}

watch(
  () => props.value,
  (next, prev) => {
    if (prev && !next) {
      hide();
    }
  },
);

watch(
  () => props.hasSaved,
  (hasSaved) => {
    if (!hasSaved && showingSaved.value) {
      hide();
    }
  },
);

onBeforeUnmount(() => {
  unmounted = true;
  hide();
});

defineExpose({ hide });
</script>

<template>
  <AInput
    ref="inputCompRef"
    :id="props.id"
    :value="displayValue"
    :type="visible ? 'text' : 'password'"
    :disabled="props.disabled && !isLockedReveal"
    :readonly="isLockedReveal"
    :placeholder="effectivePlaceholder"
    :autocomplete="props.autocomplete"
    :aria-label="props.ariaLabel"
    spellcheck="false"
    class="co-secret-input"
    :class="{
      'is-revealed': visible && !!displayValue,
      'is-locked-reveal': isLockedReveal,
    }"
    @update:value="onUpdateValue"
    @paste="onPaste"
  >
    <template #suffix>
      <ColabOneTooltip :title="toggleLabel" :disabled="toggleDisabled">
        <button
          type="button"
          class="co-secret-toggle"
          :class="{ 'is-active': visible }"
          :disabled="toggleDisabled"
          :aria-label="toggleLabel"
          :aria-pressed="visible"
          @mousedown.prevent
          @mouseup.prevent
          @click.stop="toggleVisibility"
        >
          <PhSpinnerGap
            v-if="loadingSaved"
            :size="15"
            class="co-secret-spin"
            aria-hidden="true"
          />
          <PhEye v-else-if="visible" :size="15" aria-hidden="true" />
          <PhEyeSlash v-else :size="15" aria-hidden="true" />
        </button>
      </ColabOneTooltip>
    </template>
  </AInput>
</template>

<style scoped>
.co-secret-input.is-revealed :deep(input) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 13px;
  line-height: 22px;
}

.co-secret-input.is-locked-reveal {
  background-color: var(--co-subtle);
  border-color: var(--co-border);
}

.co-secret-input.is-locked-reveal :deep(input) {
  color: var(--co-secondary-text);
}

.co-secret-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  margin: -1px -3px -1px 0;
  padding: 0;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--co-muted);
  cursor: pointer;
  pointer-events: auto;
  transition:
    color 0.16s ease,
    background-color 0.16s ease;
}

.co-secret-toggle:hover:not(:disabled) {
  color: var(--co-text);
  background: var(--co-soft-hover);
}

.co-secret-toggle:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 1px;
}

.co-secret-toggle:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.co-secret-toggle.is-active {
  color: var(--co-accent);
}

.co-secret-spin {
  animation: co-secret-spin 0.8s linear infinite;
}

@keyframes co-secret-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
