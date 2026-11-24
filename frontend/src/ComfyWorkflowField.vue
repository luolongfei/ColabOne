<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  Button as AButton,
  Input as AInput,
  InputNumber as AInputNumber,
  Select as ASelect,
  SelectOption as ASelectOption,
  Space as ASpace,
  Switch as ASwitch,
  Tag as ATag,
  Textarea as ATextarea,
} from "ant-design-vue";
import { PhImage, PhWaveform } from "@phosphor-icons/vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import { t, tb } from "./i18n";
import type { ComfyTtsEngineSpec, WorkflowField } from "./types";

const props = defineProps<{
  field: WorkflowField;
  value: string | number | boolean | undefined;
  inputId: string;
  nodeMeta?: string;
  wide?: boolean;
  compact?: boolean;
  disabled?: boolean;
  forceRequired?: boolean;
  requiredHint?: string;
  uploading?: boolean;
  uploadedName?: string;
  hasPendingUpload?: boolean;
  localPreview?: string;
  showTtsStrip?: boolean;
  selectedTtsEngine?: ComfyTtsEngineSpec;
}>();

const emit = defineEmits<{
  "update:value": [string | number | boolean];
  "media-select": [field: WorkflowField, file: File];
  "media-clear": [field: WorkflowField];
}>();

const isDragging = ref(false);
const segmentedGroupRef = ref<HTMLElement | null>(null);

watch(
  [() => props.uploading, () => props.disabled, () => props.field.key],
  () => {
    isDragging.value = false;
  },
);

const isRequired = computed(() =>
  Boolean(props.field.required || props.forceRequired),
);

const OPTIONAL_SUFFIX_RE =
  /\s*(?:[（(]\s*(?:可选|可選|選填|任意|선택|optional)\s*[）)])\s*$/i;

const displayedLabel = computed(() => {
  const raw = tb(props.field.label);
  if (isRequired.value) {
    return raw.replace(OPTIONAL_SUFFIX_RE, "").trim() || raw;
  }
  return raw;
});

const isAudioSourceSegmented = computed(
  () =>
    props.field.key === "pipeline.audio_source" &&
    props.field.field_type === "select" &&
    (props.field.options?.length || 0) > 0,
);

const stringValue = computed(() =>
  props.value !== undefined && props.value !== null ? String(props.value) : "",
);

const numberValue = computed<number | undefined>(() => {
  if (props.value === "" || props.value === undefined || props.value === null) {
    return undefined;
  }
  const num = Number(props.value);
  return Number.isFinite(num) ? num : undefined;
});

const segmentedGridStyle = computed(() => {
  const count = Math.max(1, props.field.options?.length || 1);
  return { gridTemplateColumns: `repeat(${count}, minmax(0, 1fr))` };
});

function selectOptionLabel(opt: string): string {
  const mapped = props.field.option_labels?.[opt];
  return tb(mapped || opt);
}

function formatTtsSampleRateKhz(sampleRate: number): string {
  const khz = sampleRate / 1000;
  return Number.isInteger(khz)
    ? String(khz)
    : khz.toFixed(1).replace(/\.0$/, "");
}

function segmentedTabIndex(opt: string, idx: number): number {
  const opts = props.field.options || [];
  if (stringValue.value === opt) return 0;
  if (!opts.includes(stringValue.value) && idx === 0) return 0;
  return -1;
}

function onSegmentedKeydown(event: KeyboardEvent, idx: number) {
  if (props.disabled) return;
  const opts = props.field.options || [];
  if (opts.length <= 1) return;

  let nextIdx = -1;
  if (event.key === "ArrowRight" || event.key === "ArrowDown") {
    nextIdx = (idx + 1) % opts.length;
  } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
    nextIdx = (idx - 1 + opts.length) % opts.length;
  } else if (event.key === "Home") {
    nextIdx = 0;
  } else if (event.key === "End") {
    nextIdx = opts.length - 1;
  }
  if (nextIdx < 0) return;

  event.preventDefault();
  emit("update:value", opts[nextIdx]);
  const buttons =
    segmentedGroupRef.value?.querySelectorAll<HTMLButtonElement>(
      'button[role="radio"]',
    );
  buttons?.[nextIdx]?.focus();
}

function onNumberChange(val: string | number | null | undefined) {
  if (val === null || val === undefined || val === "") {
    emit("update:value", "");
    return;
  }
  const num = Number(val);
  if (Number.isFinite(num)) {
    emit(
      "update:value",
      props.field.field_type === "integer" ? Math.round(num) : num,
    );
  }
}

function onFileInputChange(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file || props.uploading || props.disabled) return;
  emit("media-select", props.field, file);
}

function onMediaDragOver() {
  if (!props.uploading && !props.disabled) {
    isDragging.value = true;
  }
}

function onMediaDragLeave(event: DragEvent) {
  const current = event.currentTarget as HTMLElement | null;
  const related = event.relatedTarget as Node | null;
  if (current && related && current.contains(related)) {
    return;
  }
  isDragging.value = false;
}

function onMediaDrop(event: DragEvent) {
  isDragging.value = false;
  if (props.uploading || props.disabled) return;
  const file = event.dataTransfer?.files?.[0];
  if (!file) return;
  emit("media-select", props.field, file);
}
</script>

<template>
  <div class="co-comfy-field" :class="{ 'is-wide': wide }">
    <!-- Sleek Segmented Pill Toggle for Audio Source -->
    <div v-if="isAudioSourceSegmented" class="co-comfy-field-control">
      <span :id="`${inputId}-label`" class="co-comfy-field-label-row">
        <span>
          {{ displayedLabel }}
          <span
            v-if="isRequired"
            class="co-comfy-field-required"
            aria-hidden="true"
            >*</span
          >
        </span>
      </span>
      <div
        ref="segmentedGroupRef"
        :id="inputId"
        class="co-comfy-segmented"
        :style="segmentedGridStyle"
        role="radiogroup"
        :aria-labelledby="`${inputId}-label`"
      >
        <button
          v-for="(opt, idx) in field.options || []"
          :key="opt"
          type="button"
          role="radio"
          class="co-comfy-segmented-option"
          :class="{ 'is-active': stringValue === opt }"
          :aria-checked="stringValue === opt"
          :tabindex="segmentedTabIndex(opt, idx)"
          :disabled="disabled"
          @click="emit('update:value', opt)"
          @keydown="onSegmentedKeydown($event, idx)"
        >
          {{ selectOptionLabel(opt) }}
        </button>
      </div>
      <small v-if="requiredHint" class="co-comfy-field-required-hint">
        {{ requiredHint }}
      </small>
      <small v-if="field.description" class="co-comfy-field-desc">
        {{ tb(field.description) }}
      </small>
    </div>

    <!-- Boolean Switch -->
    <div v-else-if="field.field_type === 'boolean'" class="co-comfy-switch-row">
      <div class="co-comfy-switch-copy">
        <strong>
          {{ displayedLabel }}
          <span
            v-if="isRequired"
            class="co-comfy-field-required"
            aria-hidden="true"
            >*</span
          >
        </strong>
        <small v-if="nodeMeta" class="co-comfy-field-node">
          {{ nodeMeta }}
        </small>
        <small v-if="requiredHint" class="co-comfy-field-required-hint">
          {{ requiredHint }}
        </small>
        <small v-if="field.description" class="co-comfy-field-desc">
          {{ tb(field.description) }}
        </small>
      </div>
      <ASwitch
        :id="inputId"
        :checked="Boolean(value)"
        :aria-label="displayedLabel"
        :disabled="disabled"
        @update:checked="(val) => emit('update:value', Boolean(val))"
      />
    </div>

    <!-- Image / Audio Drag-and-Drop Upload Box -->
    <div
      v-else-if="field.field_type === 'image' || field.field_type === 'audio'"
      class="co-comfy-media-field"
    >
      <div class="co-comfy-field-label-row">
        <span>
          {{ displayedLabel }}
          <span
            v-if="isRequired"
            class="co-comfy-field-required"
            aria-hidden="true"
            >*</span
          >
        </span>
        <small v-if="nodeMeta" class="co-comfy-field-node">
          {{ nodeMeta }}
        </small>
      </div>

      <label
        class="co-comfy-media-dropzone"
        :class="{
          'is-dragging': isDragging,
          'is-busy': !!uploading,
          'is-disabled': !!disabled,
        }"
        @dragover.prevent="onMediaDragOver"
        @dragleave="onMediaDragLeave"
        @drop.prevent="onMediaDrop"
      >
        <input
          :id="inputId"
          class="co-visually-hidden"
          type="file"
          :accept="
            field.field_type === 'audio'
              ? '.wav,.mp3,.flac,.ogg,.m4a'
              : '.png,.jpg,.jpeg,.webp'
          "
          :disabled="!!uploading || !!disabled"
          @change="onFileInputChange"
        />
        <ColabOneSkeleton
          v-if="uploading"
          :width="42"
          :height="42"
          :radius="8"
          variant="rect"
          tone="accent"
        />
        <img
          v-else-if="field.field_type === 'image' && localPreview"
          :src="localPreview"
          :alt="uploadedName || displayedLabel"
          class="co-comfy-media-thumb"
        />
        <span v-else class="co-comfy-media-icon-wrap">
          <component
            :is="field.field_type === 'audio' ? PhWaveform : PhImage"
            :size="20"
          />
        </span>
        <div class="co-comfy-media-dropzone-copy">
          <template v-if="uploading">
            <strong>{{ t("comfy.uploadingTitle") }}</strong>
            <small>{{ t("comfy.uploadingHint") }}</small>
          </template>
          <template v-else-if="uploadedName">
            <strong>{{ uploadedName }}</strong>
            <small>{{ t("comfy.replaceFileHint") }}</small>
          </template>
          <template v-else-if="stringValue">
            <strong>
              {{ t("comfy.currentFile", { name: stringValue }) }}
            </strong>
            <small>{{ t("comfy.replaceFileHint") }}</small>
          </template>
          <template v-else>
            <strong>
              {{
                field.field_type === "audio"
                  ? t("comfy.dropAudio")
                  : t("comfy.dropImage")
              }}
            </strong>
            <small>
              {{
                field.field_type === "audio"
                  ? t("comfy.audioFormats")
                  : t("comfy.imageFormats")
              }}
            </small>
          </template>
        </div>
      </label>

      <div v-if="uploadedName || hasPendingUpload" class="co-comfy-media-status">
        <ASpace :size="6" wrap>
          <ATag :color="hasPendingUpload ? 'warning' : 'success'">
            {{
              hasPendingUpload
                ? t("comfy.pendingUpload")
                : t("comfy.uploadedRemote")
            }}
          </ATag>
          <ATag v-if="uploadedName" class="co-comfy-filename-pill">
            {{ uploadedName }}
          </ATag>
        </ASpace>
        <AButton
          type="link"
          size="small"
          danger
          :disabled="!!uploading || !!disabled"
          @click="emit('media-clear', field)"
        >
          {{ t("common.clear") }}
        </AButton>
      </div>

      <small v-if="requiredHint" class="co-comfy-field-required-hint">
        {{ requiredHint }}
      </small>

      <small v-if="field.description" class="co-comfy-field-desc">
        {{ tb(field.description) }}
      </small>
    </div>

    <!-- Standard Labeled AntD Controls -->
    <div v-else class="co-comfy-field-control">
      <label :for="inputId" class="co-comfy-field-label-row">
        <span>
          {{ displayedLabel }}
          <span
            v-if="isRequired"
            class="co-comfy-field-required"
            aria-hidden="true"
            >*</span
          >
        </span>
        <small v-if="nodeMeta" class="co-comfy-field-node">
          {{ nodeMeta }}
        </small>
      </label>

      <ATextarea
        v-if="field.field_type === 'textarea'"
        :id="inputId"
        :value="stringValue"
        :auto-size="{ minRows: 3, maxRows: compact ? 7 : 8 }"
        :disabled="disabled"
        @update:value="(val) => emit('update:value', val)"
      />

      <ASelect
        v-else-if="field.field_type === 'select'"
        :id="inputId"
        :value="stringValue"
        :disabled="disabled"
        class="co-comfy-full-width"
        @update:value="(val) => emit('update:value', String(val ?? ''))"
      >
        <ASelectOption
          v-if="stringValue && !(field.options || []).includes(stringValue)"
          :value="stringValue"
        >
          {{ selectOptionLabel(stringValue) }}
        </ASelectOption>
        <ASelectOption
          v-for="opt in field.options || []"
          :key="opt"
          :value="opt"
        >
          {{ selectOptionLabel(opt) }}
        </ASelectOption>
      </ASelect>

      <AInputNumber
        v-else-if="
          field.field_type === 'integer' || field.field_type === 'number'
        "
        :id="inputId"
        :value="numberValue"
        :min="field.min_value ?? undefined"
        :max="field.max_value ?? undefined"
        :step="field.step ?? (field.field_type === 'integer' ? 1 : 0.1)"
        :precision="field.field_type === 'integer' ? 0 : undefined"
        :disabled="disabled"
        class="co-comfy-full-width"
        @update:value="onNumberChange"
      />

      <AInput
        v-else
        :id="inputId"
        :value="stringValue"
        :disabled="disabled"
        @update:value="(val) => emit('update:value', val)"
      />

      <small v-if="requiredHint" class="co-comfy-field-required-hint">
        {{ requiredHint }}
      </small>

      <small v-if="field.description" class="co-comfy-field-desc">
        {{ tb(field.description) }}
      </small>
    </div>
  </div>

  <!-- Remote TTS Engine Info Strip -->
  <div
    v-if="showTtsStrip && selectedTtsEngine"
    class="co-comfy-tts-strip"
    :class="{
      'is-wide': wide,
      'is-non-commercial': selectedTtsEngine.commercial_ok === false,
    }"
  >
    <div class="co-comfy-tts-strip-head">
      <div class="co-comfy-tts-strip-title">
        <PhWaveform :size="15" class="co-comfy-tts-strip-icon" />
        <span class="co-comfy-tts-strip-eyebrow">
          {{ t("comfy.ttsEngineCardTitle") }}
        </span>
        <strong class="co-comfy-tts-strip-name">
          {{ tb(selectedTtsEngine.name) }}
        </strong>
      </div>
      <ATag
        :color="selectedTtsEngine.commercial_ok ? 'green' : 'orange'"
        class="co-comfy-meta-tag"
      >
        {{
          selectedTtsEngine.commercial_ok
            ? t("comfy.licenseCommercialOk")
            : t("comfy.licenseNonCommercial")
        }}
      </ATag>
    </div>
    <p v-if="selectedTtsEngine.description" class="co-comfy-tts-strip-desc">
      {{ tb(selectedTtsEngine.description) }}
    </p>
    <div class="co-comfy-tts-strip-tags">
      <ATag
        v-if="selectedTtsEngine.sample_rate > 0"
        class="co-comfy-meta-tag co-comfy-mono"
      >
        {{
          t("comfy.ttsSampleRate", {
            khz: formatTtsSampleRateKhz(selectedTtsEngine.sample_rate),
          })
        }}
      </ATag>
      <ATag v-if="selectedTtsEngine.license" class="co-comfy-meta-tag">
        {{ tb(selectedTtsEngine.license) }}
      </ATag>
      <ATag
        v-if="selectedTtsEngine.supports_voice_clone"
        color="blue"
        class="co-comfy-meta-tag"
      >
        {{ t("comfy.ttsCapabilityClone") }}
      </ATag>
      <ATag
        v-if="selectedTtsEngine.supports_voice_design"
        color="purple"
        class="co-comfy-meta-tag"
      >
        {{ t("comfy.ttsCapabilityDesign") }}
      </ATag>
      <ATag
        v-if="selectedTtsEngine.supports_emotion_ref"
        color="cyan"
        class="co-comfy-meta-tag"
      >
        {{ t("comfy.ttsCapabilityEmotion") }}
      </ATag>
      <ATag
        v-if="selectedTtsEngine.supports_inline_tags"
        color="geekblue"
        class="co-comfy-meta-tag"
      >
        {{ t("comfy.ttsCapabilityTags") }}
      </ATag>
    </div>
  </div>
</template>

<style scoped>
.co-visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.co-comfy-mono {
  font-family: var(--co-font-mono);
  font-variant-numeric: tabular-nums;
}

.co-comfy-full-width {
  width: 100%;
}

.co-comfy-meta-tag {
  font-size: 12px;
  line-height: 18px;
  padding-inline: 8px;
}

.co-comfy-field {
  min-width: 0;
}

.co-comfy-field.is-wide,
.co-comfy-tts-strip.is-wide {
  grid-column: 1 / -1;
}

.co-comfy-field-control {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.co-comfy-field-label-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 13px;
  font-weight: 550;
  color: var(--co-text);
}

.co-comfy-field-required {
  color: var(--co-danger);
  margin-left: 3px;
  font-weight: 600;
}

.co-comfy-field-node {
  font-size: 12px;
  font-weight: 400;
  color: var(--co-muted);
  font-family: var(--co-font-mono);
}

.co-comfy-field-desc {
  display: block;
  font-size: 12px;
  line-height: 1.48;
  color: var(--co-muted);
  overflow-wrap: anywhere;
}

.co-comfy-field-required-hint {
  display: block;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-warning-text);
  overflow-wrap: anywhere;
}

/* Tactile Segmented Pill Toggle (Audio Source) */
.co-comfy-segmented {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px;
  padding: 3px;
  border-radius: 11px;
  background: var(--co-subpill-track);
  border: 1px solid var(--co-border-secondary);
}

.co-comfy-segmented-option {
  appearance: none;
  border: 1px solid transparent;
  border-radius: 8px;
  padding: 8px 12px;
  background: transparent;
  color: var(--co-secondary-text);
  font-family: inherit;
  font-size: 13px;
  font-weight: 500;
  line-height: 1.3;
  cursor: pointer;
  text-align: center;
  transition:
    background-color 0.16s ease,
    color 0.16s ease,
    border-color 0.16s ease,
    box-shadow 0.16s ease;
}

.co-comfy-segmented-option:hover:not(.is-active):not(:disabled) {
  color: var(--co-text);
}

.co-comfy-segmented-option:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-comfy-segmented-option:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.co-comfy-segmented-option.is-active {
  background: var(--co-subpill-active-bg);
  color: var(--co-subpill-active-text);
  font-weight: 600;
  border-color: var(--co-subpill-active-border);
  box-shadow: var(--co-subpill-active-shadow);
}

/* Remote TTS Engine Strip */
.co-comfy-tts-strip {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border-radius: 10px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  min-width: 0;
}

.co-comfy-tts-strip.is-non-commercial {
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
}

.co-comfy-tts-strip-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.co-comfy-tts-strip-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex-wrap: wrap;
}

.co-comfy-tts-strip-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-comfy-tts-strip-icon {
  color: var(--co-accent-strong);
}

.co-comfy-tts-strip.is-non-commercial .co-comfy-tts-strip-icon {
  color: var(--co-warning-text);
}

.co-comfy-tts-strip-eyebrow {
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-tts-strip-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-tts-strip-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-secondary-text);
  overflow-wrap: anywhere;
}

.co-comfy-tts-strip-tags {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

/* Boolean Switch */
.co-comfy-switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 10px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-comfy-switch-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.co-comfy-switch-copy strong {
  font-size: 13px;
  font-weight: 550;
  color: var(--co-text);
}

/* Image / Audio Drag-and-Drop Field */
.co-comfy-media-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-comfy-media-dropzone {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 10px;
  border: 1.5px dashed var(--co-border);
  background: var(--co-subtle);
  cursor: pointer;
  margin: 0;
  transition:
    border-color 0.16s ease,
    background 0.16s ease,
    box-shadow 0.16s ease;
}

.co-comfy-media-dropzone:hover:not(.is-disabled):not(.is-busy),
.co-comfy-media-dropzone.is-dragging {
  border-color: var(--co-accent);
  background: var(--co-soft-hover);
}

.co-comfy-media-dropzone:focus-within {
  border-color: var(--co-accent);
  box-shadow: 0 0 0 3px var(--co-focus-ring);
}

.co-comfy-media-dropzone.is-busy {
  opacity: 0.72;
  cursor: wait;
}

.co-comfy-media-dropzone.is-disabled {
  opacity: 0.62;
  cursor: not-allowed;
}

.co-comfy-media-icon-wrap {
  width: 40px;
  height: 40px;
  border-radius: 9px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-comfy-media-icon-wrap {
  color: var(--co-accent-strong);
}

.co-comfy-media-thumb {
  width: 44px;
  height: 44px;
  border-radius: 8px;
  object-fit: cover;
  border: 1px solid var(--co-border-secondary);
  flex-shrink: 0;
}

.co-comfy-media-dropzone-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.co-comfy-media-dropzone-copy strong {
  font-size: 13px;
  font-weight: 550;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-comfy-media-dropzone-copy small {
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-media-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.co-comfy-filename-pill {
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
