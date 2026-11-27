<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { Button as AButton, Input as AInput } from "ant-design-vue";
import {
  PhCheck,
  PhCopy,
  PhEye,
  PhEyeSlash,
  PhSpinnerGap,
} from "@phosphor-icons/vue";
import { colabOneWriteClipboard, isColabOneDesktopRuntime } from "./api";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { colabOnePrivacyActive } from "./colabOnePrivacy";
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
    maxlength?: number;
    autocomplete?: string;
    ariaLabel?: string;
    loadSaved?: () => Promise<string>;
    extractSecret?: ColabOneSecretExtractor;
    copyable?: boolean;
    copyLabel?: string;
    copiedDurationMs?: number;
    inputTestId?: string;
  }>(),
  {
    id: undefined,
    hasSaved: false,
    disabled: false,
    placeholder: "",
    savedPlaceholder: "",
    maxlength: undefined,
    autocomplete: "off",
    ariaLabel: undefined,
    loadSaved: undefined,
    extractSecret: undefined,
    copyable: false,
    copyLabel: "",
    copiedDurationMs: 1800,
    inputTestId: undefined,
  },
);

const emit = defineEmits<{
  "update:value": [string];
  extracted: [string];
  copied: [];
  error: [string];
  blur: [FocusEvent];
  "press-enter": [KeyboardEvent];
}>();

const inputCompRef = ref<{
  input?: HTMLInputElement | null;
  $el?: HTMLElement;
} | null>(null);
const visible = ref(false);
const showingSaved = ref(false);
const revealedSaved = ref("");
const loadingSaved = ref(false);
const copyingSaved = ref(false);
const copied = ref(false);
let revealSeq = 0;
let copySeq = 0;
let copyTimer: ReturnType<typeof setTimeout> | undefined;
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

const privacyLocked = computed(() => colabOnePrivacyActive.value);

const toggleDisabled = computed(
  () =>
    privacyLocked.value ||
    loadingSaved.value ||
    (props.disabled &&
      !(props.hasSaved && props.loadSaved) &&
      !props.value &&
      !showingSaved.value),
);

const toggleLabel = computed(() => {
  if (privacyLocked.value) return t("privacy.secretLockedTooltip");
  if (visible.value) return t("common.hideSecret");
  if (props.hasSaved && !props.value && props.loadSaved) {
    return t("common.revealSavedSecret");
  }
  return t("common.showSecret");
});

const hasCopyableSecret = computed(() => {
  if (props.value.trim()) return true;
  if (showingSaved.value && revealedSaved.value.trim()) return true;
  if (props.hasSaved && props.loadSaved) return true;
  return false;
});

const copyDisabled = computed(
  () => copyingSaved.value || !hasCopyableSecret.value,
);

const copyBaseLabel = computed(() => props.copyLabel || t("common.copy"));

const copyTooltipTitle = computed(() =>
  copied.value ? t("common.copied") : copyBaseLabel.value,
);

function clearCopyTimer() {
  if (copyTimer !== undefined) {
    clearTimeout(copyTimer);
    copyTimer = undefined;
  }
}

function resetCopiedState() {
  copySeq += 1;
  copyingSaved.value = false;
  copied.value = false;
  clearCopyTimer();
}

async function writeSecretToClipboard(text: string): Promise<boolean> {
  try {
    await colabOneWriteClipboard(text);
    if (isColabOneDesktopRuntime()) {
      await navigator.clipboard?.writeText(text).catch(() => {});
    }
    return true;
  } catch {
    return false;
  }
}

async function copySecret() {
  if (!props.copyable || copyDisabled.value) return;
  const seq = ++copySeq;

  let text = props.value.trim();
  if (!text && showingSaved.value) {
    text = revealedSaved.value.trim();
  }

  if (!text && props.hasSaved && props.loadSaved) {
    copyingSaved.value = true;
    try {
      const saved = await props.loadSaved();
      if (unmounted || seq !== copySeq) return;
      text = typeof saved === "string" ? saved.trim() : "";
    } catch (err) {
      if (!unmounted && seq === copySeq) {
        emit("error", String(err));
      }
      return;
    } finally {
      if (seq === copySeq) {
        copyingSaved.value = false;
      }
    }
  }

  if (!text) {
    if (!unmounted && seq === copySeq) {
      emit("error", t("common.copyFailed"));
    }
    return;
  }

  const ok = await writeSecretToClipboard(text);
  if (unmounted || seq !== copySeq) return;
  if (!ok) {
    emit("error", t("common.copyFailed"));
    return;
  }

  copied.value = true;
  clearCopyTimer();
  copyTimer = setTimeout(() => {
    if (!unmounted && seq === copySeq) {
      copied.value = false;
      copyTimer = undefined;
    }
  }, Math.max(400, props.copiedDurationMs));
  emit("copied");
}

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
      if (unmounted || seq !== revealSeq || privacyLocked.value || !props.hasSaved) {
        return;
      }
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
    } else if (next && next !== prev && showingSaved.value) {
      revealSeq += 1;
      loadingSaved.value = false;
      showingSaved.value = false;
      revealedSaved.value = "";
    }
    if (next !== prev && (copied.value || copyingSaved.value)) {
      resetCopiedState();
    }
  },
);

watch(
  () => props.copyable,
  (copyable) => {
    if (!copyable) {
      resetCopiedState();
    }
  },
);

watch(
  () => props.hasSaved,
  (hasSaved) => {
    if (!hasSaved) {
      if (showingSaved.value || loadingSaved.value) {
        hide();
      }
      if (!props.value.trim() && (copied.value || copyingSaved.value)) {
        resetCopiedState();
      }
    }
  },
);

watch(
  colabOnePrivacyActive,
  (masked) => {
    if (masked && (visible.value || showingSaved.value || loadingSaved.value)) {
      hide();
    }
  },
  { immediate: true },
);

watch(hasCopyableSecret, (available) => {
  if (!available && (copied.value || copyingSaved.value)) {
    resetCopiedState();
  }
});

onBeforeUnmount(() => {
  unmounted = true;
  resetCopiedState();
  hide();
});

function focus() {
  resolveNativeInput()?.focus();
}

defineExpose({ hide, focus });
</script>

<template>
  <div class="co-secret-field" :class="{ 'is-copyable': props.copyable }">
    <AInput
      ref="inputCompRef"
      :id="props.id"
      :value="displayValue"
      :type="visible ? 'text' : 'password'"
      :disabled="props.disabled && !isLockedReveal"
      :readonly="isLockedReveal"
      :maxlength="props.maxlength"
      :placeholder="effectivePlaceholder"
      :autocomplete="props.autocomplete"
      :aria-label="props.ariaLabel"
      :data-testid="props.inputTestId"
      spellcheck="false"
      class="co-secret-input"
      :class="{
        'is-revealed': visible && !!displayValue,
        'is-locked-reveal': isLockedReveal,
      }"
      @update:value="onUpdateValue"
      @paste="onPaste"
      @blur="(e) => emit('blur', e)"
      @press-enter="(e) => emit('press-enter', e)"
    >
      <template #suffix>
        <ColabOneTooltip
          :title="toggleLabel"
          :disabled="toggleDisabled && !privacyLocked"
        >
          <span class="co-secret-toggle-wrap">
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
          </span>
        </ColabOneTooltip>
      </template>
    </AInput>
    <ColabOneTooltip
      v-if="props.copyable"
      :title="copyTooltipTitle"
      :disabled="copyDisabled && !copied"
    >
      <span class="co-secret-copy-wrap">
        <AButton
          type="default"
          html-type="button"
          class="co-secret-copy-btn"
          :class="{ 'is-copied': copied }"
          :disabled="copyDisabled"
          :aria-label="copyBaseLabel"
          :data-copied="copied ? 'true' : undefined"
          @click="copySecret"
        >
          <template #icon>
            <PhSpinnerGap
              v-if="copyingSaved"
              :size="15"
              class="co-secret-spin"
              aria-hidden="true"
            />
            <PhCheck
              v-else-if="copied"
              :size="15"
              weight="bold"
              class="co-secret-copy-check"
              aria-hidden="true"
            />
            <PhCopy v-else :size="15" aria-hidden="true" />
          </template>
        </AButton>
      </span>
    </ColabOneTooltip>
  </div>
</template>

<style scoped>
.co-secret-field {
  display: flex;
  align-items: stretch;
  gap: 6px;
  width: 100%;
  min-width: 0;
}

.co-secret-input {
  flex: 1 1 auto;
  min-width: 0;
}

.co-secret-copy-wrap {
  display: inline-flex;
  align-items: stretch;
  flex-shrink: 0;
}

.co-secret-copy-btn {
  width: 32px;
  min-width: 32px;
  min-height: 32px;
  height: auto;
  padding: 0;
  color: var(--co-secondary-text);
}

.co-secret-copy-btn:hover:not(:disabled) {
  color: var(--co-text);
}

.co-secret-copy-btn.ant-btn.ant-btn-default.is-copied:not(:disabled),
.co-secret-copy-btn.ant-btn.ant-btn-default.is-copied:hover:not(:disabled),
:global(:root[data-theme="dark"])
  .co-secret-copy-btn.ant-btn.ant-btn-default.is-copied:not(:disabled),
:global(:root[data-theme="dark"])
  .co-secret-copy-btn.ant-btn.ant-btn-default.is-copied:hover:not(:disabled) {
  color: var(--co-success-text);
  background: var(--co-success-bg);
  border-color: var(--co-success-border);
}

.co-secret-copy-check {
  animation: co-secret-check-pop 0.2s cubic-bezier(0.22, 1, 0.36, 1);
}

@keyframes co-secret-check-pop {
  0% {
    transform: scale(0.72);
    opacity: 0.4;
  }
  100% {
    transform: scale(1);
    opacity: 1;
  }
}

@media (pointer: coarse) and (max-width: 768px) {
  .co-secret-copy-btn {
    width: 36px;
    min-width: 36px;
    min-height: 36px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .co-secret-copy-check {
    animation: none;
  }
}

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

.co-secret-toggle-wrap {
  display: inline-flex;
  align-items: center;
  justify-content: center;
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
