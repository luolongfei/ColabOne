<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Button as AButton,
  Checkbox as ACheckbox,
  Modal as AModal,
} from "ant-design-vue";
import { PhPower, PhSignOut, PhTray } from "@phosphor-icons/vue";
import { t } from "./i18n";
import type { ColabOneCloseAction } from "./api";

const props = withDefaults(
  defineProps<{
    open: boolean;
    defaultChoice?: ColabOneCloseAction;
    canBackground?: boolean;
    submitting?: boolean;
    hasActiveOperation?: boolean;
    suppressLeaveTransition?: boolean;
  }>(),
  {
    defaultChoice: "quit",
    canBackground: true,
    submitting: false,
    hasActiveOperation: false,
    suppressLeaveTransition: false,
  },
);

const emit = defineEmits<{
  (e: "update:open", value: boolean): void;
  (
    e: "confirm",
    payload: { action: ColabOneCloseAction; remember: boolean },
  ): void;
}>();

const selectedAction = ref<ColabOneCloseAction>("quit");
const rememberChoice = ref(false);
const modalBodyRef = ref<HTMLElement | null>(null);
const quitOptionRef = ref<HTMLButtonElement | null>(null);
const backgroundOptionRef = ref<HTMLButtonElement | null>(null);
const confirmBtnWrapRef = ref<HTMLElement | null>(null);
let focusRafA: number | null = null;
let focusRafB: number | null = null;

function cancelScheduledFocus() {
  if (typeof window === "undefined") return;
  if (focusRafA !== null) {
    window.cancelAnimationFrame(focusRafA);
    focusRafA = null;
  }
  if (focusRafB !== null) {
    window.cancelAnimationFrame(focusRafB);
    focusRafB = null;
  }
}

function scheduleConfirmFocus() {
  cancelScheduledFocus();
  const focusConfirm = () => {
    if (!props.open) return;
    const active =
      typeof document !== "undefined" ? document.activeElement : null;
    if (active && modalBodyRef.value?.contains(active)) {
      return;
    }
    const btn = confirmBtnWrapRef.value?.querySelector<HTMLButtonElement>(
      "button:not([disabled])",
    );
    btn?.focus();
  };
  if (
    typeof window !== "undefined" &&
    typeof window.requestAnimationFrame === "function"
  ) {
    focusRafA = window.requestAnimationFrame(() => {
      focusRafA = null;
      focusRafB = window.requestAnimationFrame(() => {
        focusRafB = null;
        focusConfirm();
      });
    });
    return;
  }
  void nextTick(focusConfirm);
}

onUnmounted(() => {
  cancelScheduledFocus();
});

const modalWrapProps = {
  "data-testid": "close-modal",
};

const showActiveOpWarning = computed(
  () => props.hasActiveOperation && selectedAction.value === "quit",
);

const confirmLabel = computed(() =>
  selectedAction.value === "background"
    ? t("close.confirmBackgroundBtn")
    : t("close.confirmQuitBtn"),
);

watch(
  () => props.open,
  (isOpen, wasOpen) => {
    if (isOpen && !wasOpen) {
      selectedAction.value =
        props.defaultChoice === "background" && props.canBackground
          ? "background"
          : "quit";
      rememberChoice.value = false;
      scheduleConfirmFocus();
    } else if (!isOpen) {
      cancelScheduledFocus();
    }
  },
);

watch(
  () => props.canBackground,
  (canBg) => {
    if (!canBg && selectedAction.value === "background") {
      selectedAction.value = "quit";
    }
  },
);

function selectAction(action: ColabOneCloseAction) {
  if (props.submitting) return;
  if (action === "background" && !props.canBackground) return;
  selectedAction.value = action;
}

function handleOptionKeydown(e: KeyboardEvent, current: ColabOneCloseAction) {
  if (props.submitting) return;
  if (
    e.key === "ArrowDown" ||
    e.key === "ArrowUp" ||
    e.key === "ArrowLeft" ||
    e.key === "ArrowRight"
  ) {
    if (!props.canBackground) return;
    e.preventDefault();
    const next: ColabOneCloseAction =
      current === "quit" ? "background" : "quit";
    selectedAction.value = next;
    void nextTick(() => {
      const target =
        next === "background" ? backgroundOptionRef.value : quitOptionRef.value;
      target?.focus();
    });
    return;
  }
  if (e.key === "Enter") {
    e.preventDefault();
    handleConfirm();
  }
}

function handleOpenChange(next: boolean) {
  if (!next && props.submitting) {
    return;
  }
  emit("update:open", next);
}

function handleCancel() {
  handleOpenChange(false);
}

function handleConfirm() {
  if (props.submitting) return;
  const action: ColabOneCloseAction =
    selectedAction.value === "background" && props.canBackground
      ? "background"
      : "quit";
  emit("confirm", {
    action,
    remember: rememberChoice.value,
  });
}
</script>

<template>
  <AModal
    :open="props.open"
    :width="468"
    :closable="!props.submitting"
    :mask-closable="!props.submitting"
    :keyboard="!props.submitting"
    :footer="null"
    :transition-name="props.suppressLeaveTransition ? '' : undefined"
    :mask-transition-name="props.suppressLeaveTransition ? '' : undefined"
    destroy-on-close
    :wrap-props="modalWrapProps"
    wrap-class-name="co-close-modal-wrap"
    centered
    @update:open="handleOpenChange"
  >
    <template #title>
      <div class="co-close-title">
        <span class="co-close-title-icon" aria-hidden="true">
          <PhPower :size="18" weight="duotone" />
        </span>
        <span class="co-close-title-text">{{ t("close.modalTitle") }}</span>
      </div>
    </template>

    <div
      ref="modalBodyRef"
      class="co-close-modal-body"
      @focusin="cancelScheduledFocus"
    >
      <p class="co-close-lead">{{ t("close.modalLead") }}</p>

      <div
        class="co-close-options"
        role="radiogroup"
        :aria-label="t('close.modalLead')"
      >
        <button
          ref="quitOptionRef"
          type="button"
          role="radio"
          class="co-close-option"
          :class="{ 'is-selected': selectedAction === 'quit' }"
          :aria-checked="selectedAction === 'quit'"
          :tabindex="selectedAction === 'quit' ? 0 : -1"
          :disabled="props.submitting"
          data-testid="close-option-quit"
          @click="selectAction('quit')"
          @keydown="handleOptionKeydown($event, 'quit')"
        >
          <span class="co-close-radio" aria-hidden="true">
            <span class="co-close-radio-dot" />
          </span>
          <span class="co-close-option-icon is-quit" aria-hidden="true">
            <PhSignOut :size="17" weight="duotone" />
          </span>
          <span class="co-close-option-copy">
            <span class="co-close-option-head">
              <span class="co-close-option-title">{{
                t("close.optionQuitTitle")
              }}</span>
              <span class="co-close-default-pill">{{
                t("close.defaultTag")
              }}</span>
            </span>
            <span class="co-close-option-desc">{{
              t("close.optionQuitDesc")
            }}</span>
          </span>
        </button>

        <button
          v-if="props.canBackground"
          ref="backgroundOptionRef"
          type="button"
          role="radio"
          class="co-close-option"
          :class="{ 'is-selected': selectedAction === 'background' }"
          :aria-checked="selectedAction === 'background'"
          :tabindex="selectedAction === 'background' ? 0 : -1"
          :disabled="props.submitting"
          data-testid="close-option-background"
          @click="selectAction('background')"
          @keydown="handleOptionKeydown($event, 'background')"
        >
          <span class="co-close-radio" aria-hidden="true">
            <span class="co-close-radio-dot" />
          </span>
          <span class="co-close-option-icon is-background" aria-hidden="true">
            <PhTray :size="17" weight="duotone" />
          </span>
          <span class="co-close-option-copy">
            <span class="co-close-option-head">
              <span class="co-close-option-title">{{
                t("close.optionBackgroundTitle")
              }}</span>
            </span>
            <span class="co-close-option-desc">{{
              t("close.optionBackgroundDesc")
            }}</span>
          </span>
        </button>
      </div>

      <AAlert
        v-if="showActiveOpWarning"
        type="warning"
        show-icon
        :message="t('close.activeOpWarning')"
      />

      <div class="co-close-remember">
        <ACheckbox
          v-model:checked="rememberChoice"
          :disabled="props.submitting"
          data-testid="close-remember-checkbox"
        >
          <span class="co-close-remember-label">{{
            t("close.rememberLabel")
          }}</span>
        </ACheckbox>
        <p class="co-close-remember-hint">{{ t("close.rememberHint") }}</p>
      </div>

      <div class="co-close-footer">
        <AButton
          :disabled="props.submitting"
          data-testid="close-cancel-btn"
          @click="handleCancel"
        >
          {{ t("common.cancel") }}
        </AButton>
        <span ref="confirmBtnWrapRef" class="co-close-confirm-wrap">
          <AButton
            type="primary"
            :loading="props.submitting"
            data-testid="close-confirm-btn"
            @click="handleConfirm"
          >
            <template v-if="!props.submitting" #icon>
              <PhTray
                v-if="selectedAction === 'background'"
                :size="15"
                weight="bold"
              />
              <PhSignOut v-else :size="15" weight="bold" />
            </template>
            {{ confirmLabel }}
          </AButton>
        </span>
      </div>
    </div>
  </AModal>
</template>

<style scoped>
.co-close-title {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.co-close-title-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: 9px;
  background: var(--co-selected);
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-close-title-text {
  font-size: 16px;
  font-weight: 600;
  line-height: 1.3;
  color: var(--co-text);
}

.co-close-modal-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding-top: 6px;
}

.co-close-lead {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-close-options {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-close-option {
  width: 100%;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 13px 14px;
  border-radius: var(--co-radius-md);
  border: 1.5px solid var(--co-border);
  background: var(--co-surface);
  text-align: left;
  font-family: inherit;
  cursor: pointer;
  transition:
    border-color 0.16s ease,
    background-color 0.16s ease,
    box-shadow 0.16s ease,
    transform 0.12s ease;
}

.co-close-option:hover:not(:disabled):not(.is-selected) {
  border-color: var(--co-accent);
  background: var(--co-subtle);
}

.co-close-option:active:not(:disabled) {
  transform: scale(0.992);
}

.co-close-option:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-close-option:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.co-close-option.is-selected {
  border-color: var(--co-accent);
  background: var(--co-selected);
  box-shadow: 0 0 0 1px var(--co-focus-ring);
}

.co-close-radio {
  width: 18px;
  height: 18px;
  margin-top: 3px;
  border-radius: 9999px;
  border: 1.5px solid var(--co-input-border);
  background: var(--co-surface);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition:
    border-color 0.16s ease,
    background-color 0.16s ease;
}

.co-close-option.is-selected .co-close-radio {
  border-color: var(--co-accent);
  background: var(--co-accent);
}

.co-close-radio-dot {
  width: 6px;
  height: 6px;
  border-radius: 9999px;
  background: var(--co-on-accent);
  transform: scale(0);
  transition: transform 0.15s ease;
}

.co-close-option.is-selected .co-close-radio-dot {
  transform: scale(1);
}

.co-close-option-icon {
  width: 32px;
  height: 32px;
  border-radius: 9px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-top: 1px;
}

.co-close-option-icon.is-quit {
  background: var(--co-track);
  color: var(--co-secondary-text);
}

.co-close-option.is-selected .co-close-option-icon.is-quit {
  background: var(--co-surface);
  color: var(--co-accent);
}

.co-close-option-icon.is-background {
  background: var(--co-track);
  color: var(--co-secondary-text);
}

.co-close-option.is-selected .co-close-option-icon.is-background {
  background: var(--co-surface);
  color: var(--co-accent);
}

.co-close-option-copy {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
  flex: 1;
}

.co-close-option-head {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-close-option-title {
  font-size: 14px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--co-text);
}

.co-close-default-pill {
  display: inline-flex;
  align-items: center;
  padding: 1px 7px;
  border-radius: 9999px;
  font-size: 11px;
  font-weight: 600;
  line-height: 1.45;
  background: var(--co-surface);
  border: 1px solid var(--co-border);
  color: var(--co-secondary-text);
}

.co-close-option.is-selected .co-close-default-pill {
  border-color: rgba(59, 130, 246, 0.34);
  color: var(--co-accent-strong);
}

:root[data-theme="dark"]
  .co-close-option.is-selected
  .co-close-default-pill {
  border-color: rgba(96, 165, 250, 0.42);
}

.co-close-option-desc {
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-close-remember {
  padding: 10px 12px;
  border-radius: var(--co-radius-md);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-close-remember-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--co-text);
}

.co-close-remember-hint {
  margin: 0;
  padding-left: 24px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-muted);
}

.co-close-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 2px;
}

.co-close-confirm-wrap {
  display: inline-flex;
}

@media (max-width: 680px) {
  .co-close-footer {
    flex-wrap: nowrap;
  }

  .co-close-footer > :first-child {
    flex-shrink: 0;
  }

  .co-close-confirm-wrap {
    flex: 1 1 0;
    min-width: 0;
  }

  .co-close-confirm-wrap > .ant-btn {
    width: 100%;
    min-width: 0;
  }
}
</style>
