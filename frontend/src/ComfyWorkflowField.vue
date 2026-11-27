<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import {
  Button as AButton,
  Input as AInput,
  InputNumber as AInputNumber,
  Select as ASelect,
  SelectOptGroup as ASelectOptGroup,
  SelectOption as ASelectOption,
  Switch as ASwitch,
  Tag as ATag,
  Textarea as ATextarea,
} from "ant-design-vue";
import {
  PhArrowCounterClockwise,
  PhImage,
  PhPlay,
  PhSpeakerHigh,
  PhStop,
  PhWarningCircle,
  PhWaveform,
} from "@phosphor-icons/vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import {
  formatColabOneDiskGiB,
  formatColabOneVramGiB,
  isColabOneTtsEngineNonCommercial,
  resolveColabOneSelectedVoicePreset,
  resolveColabOneVoicePresetGroups,
  resolveColabOneWorkflowFieldPlaceholder,
  type ColabOneVoicePresetGroupKey,
} from "./comfyui";
import { colabOneLocaleCode, t, tb } from "./i18n";
import type {
  ColabOneVoicePreviewMeta,
  ComfyTtsEngineSpec,
  WorkflowField,
} from "./types";

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
  hasUploadedRemote?: boolean;
  hasDraftMedia?: boolean;
  localPreview?: string;
  showTtsStrip?: boolean;
  selectedTtsEngine?: ComfyTtsEngineSpec;
  showVoicePreview?: boolean;
  voicePreviewText?: string;
  voicePreviewDefaultText?: string;
  voicePreviewBusy?: boolean;
  voicePreviewStatus?: string;
  voicePreviewError?: string;
  voicePreviewAudioUrl?: string;
  voicePreviewPlayToken?: number;
  voicePreviewMeta?: ColabOneVoicePreviewMeta | null;
  voicePreviewRemoteReady?: boolean;
  voicePreviewCanOfflinePlay?: boolean;
  voicePreviewDisabled?: boolean;
  readyVoicePresetIds?: string[];
  voicePreviewPrefetchActive?: boolean;
  voicePreviewPrefetchDone?: number;
  voicePreviewPrefetchTotal?: number;
  shareMode?: boolean;
}>();

const emit = defineEmits<{
  "update:value": [string | number | boolean];
  "field-blur": [field: WorkflowField];
  "media-select": [field: WorkflowField, file: File];
  "media-clear": [field: WorkflowField];
  "update:voice-preview-text": [string];
  "voice-preview-reset": [];
  "voice-preview-play": [];
  "voice-preview-prefetch-cancel": [];
}>();

const isDragging = ref(false);
const segmentedGroupRef = ref<HTMLElement | null>(null);
const voicePreviewAudioRef = ref<HTMLAudioElement | null>(null);
const voiceAudioPlaying = ref(false);

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

const fieldPlaceholder = computed(() =>
  resolveColabOneWorkflowFieldPlaceholder(
    props.field,
    colabOneLocaleCode.value,
  ),
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
  if (mapped) return tb(mapped);
  const preset = resolveColabOneSelectedVoicePreset(
    opt,
    props.selectedTtsEngine,
  );
  return tb(preset?.name || opt);
}

const isGroupedVoicePresetSelect = computed(
  () =>
    props.field.key === "pipeline.voice_preset" &&
    props.field.field_type === "select",
);

const voicePresetGroups = computed(() =>
  isGroupedVoicePresetSelect.value
    ? resolveColabOneVoicePresetGroups(props.field, props.selectedTtsEngine)
    : [],
);

const allVoicePresetOptionIds = computed(() => {
  const ids = new Set<string>();
  for (const group of voicePresetGroups.value) {
    for (const opt of group.options) {
      ids.add(opt.id);
    }
  }
  return ids;
});

const readyVoicePresetSet = computed(
  () => new Set(props.readyVoicePresetIds || []),
);

function isVoicePresetReady(presetId: string): boolean {
  return Boolean(presetId && presetId !== "custom" && readyVoicePresetSet.value.has(presetId));
}

function voiceGroupLabel(key: ColabOneVoicePresetGroupKey): string {
  switch (key) {
    case "female":
      return t("comfy.voiceGroupFemale");
    case "male":
      return t("comfy.voiceGroupMale");
    case "neutral":
      return t("comfy.voiceGroupNeutral");
    case "official":
      return t("comfy.voiceGroupOfficial");
    case "custom":
      return t("comfy.voiceGroupCustom");
  }
}

const selectedVoicePreset = computed(() =>
  resolveColabOneSelectedVoicePreset(
    stringValue.value,
    props.selectedTtsEngine,
  ),
);

const isCustomVoiceSelected = computed(
  () => stringValue.value.trim() === "custom",
);

const selectedVoiceGenderLabel = computed(() => {
  const gender = selectedVoicePreset.value?.gender;
  if (gender === "female") return t("comfy.voiceGenderFemale");
  if (gender === "male") return t("comfy.voiceGenderMale");
  if (gender === "neutral") return t("comfy.voiceGenderNeutral");
  return "";
});

const selectedVoiceSourceTag = computed<{ label: string; color: string }>(() => {
  if (isCustomVoiceSelected.value) {
    return { label: t("comfy.voiceSourceCustom"), color: "default" };
  }
  if (selectedVoicePreset.value?.speaker_id?.trim()) {
    return { label: t("comfy.voiceSourceOfficial"), color: "gold" };
  }
  return { label: t("comfy.voiceSourcePreset"), color: "blue" };
});

const voicePreviewTextValue = computed(() => props.voicePreviewText ?? "");

const canResetVoicePreviewText = computed(() => {
  const fallback = (props.voicePreviewDefaultText ?? "").trim();
  if (!fallback) return false;
  return voicePreviewTextValue.value.trim() !== fallback;
});

const isSelectedTtsNonCommercial = computed(() =>
  isColabOneTtsEngineNonCommercial(props.selectedTtsEngine),
);

const VOICE_PREVIEW_ALREADY_PREFIXED_RE =
  /^(?:音色试听(?:音频合成)?失败[:：]|音色試聽(?:音訊合成)?失敗[:：]|聲線試聽(?:音訊合成)?失敗[:：]|Voice preview(?: audio synthesis)? failed:|ボイス試聴(?:音声の合成)?に失敗しました[:：]|음색 미리듣기(?: 오디오 합성)? 실패[:：])/i;

const formattedVoicePreviewError = computed(() => {
  const raw = props.voicePreviewError?.trim() || "";
  if (!raw) return "";
  const localized = tb(raw).trim();
  if (!localized) return "";
  const templatePrefix = t("comfy.voicePreviewError", { error: "" }).trim();
  if (
    (templatePrefix && localized.startsWith(templatePrefix)) ||
    VOICE_PREVIEW_ALREADY_PREFIXED_RE.test(localized)
  ) {
    return localized;
  }
  return t("comfy.voicePreviewError", { error: localized });
});

const voicePreviewHint = computed(() => {
  if (!props.voicePreviewRemoteReady) {
    return t(
      props.shareMode
        ? "comfy.voicePreviewHintOfflineShare"
        : "comfy.voicePreviewHintOffline",
    );
  }
  if (isCustomVoiceSelected.value) {
    return t("comfy.voicePreviewHintCustom");
  }
  return t("comfy.voicePreviewHintIdle");
});

function formatTtsSampleRateKhz(sampleRate: number): string {
  const khz = sampleRate / 1000;
  return Number.isInteger(khz)
    ? String(khz)
    : khz.toFixed(1).replace(/\.0$/, "");
}

function formatPreviewDurationSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "0";
  return seconds.toFixed(1).replace(/\.0$/, "");
}

function stopVoicePreviewPlayback() {
  const el = voicePreviewAudioRef.value;
  if (el) {
    try {
      el.pause();
      if (el.readyState > 0) {
        el.currentTime = 0;
      }
    } catch {
      // Ignore uninitialized audio element.
    }
  }
  voiceAudioPlaying.value = false;
}

watch(
  () => props.voicePreviewAudioUrl,
  (nextUrl, prevUrl) => {
    if (!nextUrl || nextUrl !== prevUrl) {
      stopVoicePreviewPlayback();
    }
  },
);

watch(
  () => props.showVoicePreview,
  (visible) => {
    if (!visible) {
      stopVoicePreviewPlayback();
    }
  },
);

watch(
  () => props.voicePreviewPlayToken,
  async (token) => {
    if (!token || !props.voicePreviewAudioUrl || props.voicePreviewBusy) return;
    await nextTick();
    if (
      token !== props.voicePreviewPlayToken ||
      !props.voicePreviewAudioUrl ||
      props.voicePreviewBusy
    ) {
      return;
    }
    const el = voicePreviewAudioRef.value;
    if (!el) return;
    if (el.readyState > 0) {
      try {
        el.currentTime = 0;
      } catch {
        // Ignore seek failures before metadata loads.
      }
    }
    try {
      const playPromise = el.play();
      if (playPromise && typeof playPromise.catch === "function") {
        void playPromise.catch(() => {
          if (
            token === props.voicePreviewPlayToken &&
            voicePreviewAudioRef.value === el
          ) {
            voiceAudioPlaying.value = false;
          }
        });
      }
    } catch {
      if (
        token === props.voicePreviewPlayToken &&
        voicePreviewAudioRef.value === el
      ) {
        voiceAudioPlaying.value = false;
      }
    }
  },
);

watch(
  () => props.voicePreviewBusy,
  (busyNow) => {
    if (busyNow) {
      stopVoicePreviewPlayback();
    }
  },
);

onBeforeUnmount(() => {
  stopVoicePreviewPlayback();
});

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

function hasFileDragPayload(event: DragEvent): boolean {
  const dt = event.dataTransfer;
  if (!dt) return false;
  if (dt.types && dt.types.length > 0) {
    return Array.from(dt.types).includes("Files");
  }
  return Boolean(dt.files && dt.files.length > 0);
}

function onMediaDragOver(event: DragEvent) {
  const hasFiles = hasFileDragPayload(event);
  if (hasFiles && (props.uploading || props.disabled)) {
    event.preventDefault();
    if (event.dataTransfer) {
      event.dataTransfer.dropEffect = "none";
    }
    isDragging.value = false;
    return;
  }
  if (props.uploading || props.disabled || !hasFiles) {
    isDragging.value = false;
    return;
  }
  event.preventDefault();
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = "copy";
  }
  isDragging.value = true;
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
  if (!hasFileDragPayload(event)) return;
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
        <span :id="`${inputId}-label`">
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
        @dragover="onMediaDragOver"
        @dragleave="onMediaDragLeave"
        @drop.prevent="onMediaDrop"
      >
        <input
          :id="inputId"
          class="co-visually-hidden"
          type="file"
          :aria-labelledby="`${inputId}-label`"
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

      <div
        v-if="uploadedName || hasPendingUpload || hasDraftMedia"
        class="co-comfy-media-status"
      >
        <div class="co-comfy-media-status-tags">
          <ATag v-if="hasPendingUpload" color="warning">
            {{ t("comfy.pendingUpload") }}
          </ATag>
          <ATag
            v-else-if="hasUploadedRemote ?? Boolean(uploadedName)"
            color="success"
          >
            {{ t("comfy.uploadedRemote") }}
          </ATag>
          <ATag v-if="hasDraftMedia" color="blue">
            {{ t("comfy.draftLocalMediaTag") }}
          </ATag>
          <ColabOneTooltip v-if="uploadedName" :title="uploadedName">
            <ATag class="co-comfy-filename-pill">
              <span class="co-comfy-filename-pill-text">{{
                uploadedName
              }}</span>
            </ATag>
          </ColabOneTooltip>
        </div>
        <AButton
          type="link"
          size="small"
          danger
          class="co-comfy-media-clear-btn"
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
        :placeholder="fieldPlaceholder || undefined"
        :auto-size="{ minRows: 3, maxRows: compact ? 7 : 8 }"
        :disabled="disabled"
        @update:value="(val) => emit('update:value', val)"
        @blur="emit('field-blur', field)"
      />

      <ASelect
        v-else-if="field.field_type === 'select'"
        :id="inputId"
        :value="stringValue"
        :disabled="disabled"
        :dropdown-match-select-width="false"
        option-label-prop="label"
        popup-class-name="co-comfy-select-dropdown"
        class="co-comfy-full-width co-comfy-select"
        @update:value="(val) => emit('update:value', String(val ?? ''))"
        @blur="emit('field-blur', field)"
      >
        <template v-if="isGroupedVoicePresetSelect">
          <ASelectOption
            v-if="stringValue && !allVoicePresetOptionIds.has(stringValue)"
            :value="stringValue"
            :label="selectOptionLabel(stringValue)"
            :data-co-full-title="selectOptionLabel(stringValue)"
          >
            {{ selectOptionLabel(stringValue) }}
          </ASelectOption>
          <ASelectOptGroup
            v-for="group in voicePresetGroups"
            :key="group.key"
            :label="voiceGroupLabel(group.key)"
          >
            <ASelectOption
              v-for="opt in group.options"
              :key="opt.id"
              :value="opt.id"
              :label="tb(opt.label)"
              :data-co-full-title="tb(opt.label)"
            >
              <span class="co-comfy-voice-opt-row">
                <span class="co-comfy-voice-opt-label">{{ tb(opt.label) }}</span>
                <span
                  v-if="isVoicePresetReady(opt.id)"
                  class="co-comfy-voice-ready-pill"
                >
                  {{ t("comfy.voicePreviewReadyTag") }}
                </span>
              </span>
            </ASelectOption>
          </ASelectOptGroup>
        </template>
        <template v-else>
          <ASelectOption
            v-if="stringValue && !(field.options || []).includes(stringValue)"
            :value="stringValue"
            :label="selectOptionLabel(stringValue)"
            :data-co-full-title="selectOptionLabel(stringValue)"
          >
            {{ selectOptionLabel(stringValue) }}
          </ASelectOption>
          <ASelectOption
            v-for="opt in field.options || []"
            :key="opt"
            :value="opt"
            :label="selectOptionLabel(opt)"
            :data-co-full-title="selectOptionLabel(opt)"
          >
            {{ selectOptionLabel(opt) }}
          </ASelectOption>
        </template>
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
        @blur="emit('field-blur', field)"
      />

      <AInput
        v-else
        :id="inputId"
        :value="stringValue"
        :placeholder="fieldPlaceholder || undefined"
        :disabled="disabled"
        @update:value="(val) => emit('update:value', val)"
        @blur="emit('field-blur', field)"
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
      'is-non-commercial': isSelectedTtsNonCommercial,
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
        :color="isSelectedTtsNonCommercial ? 'orange' : 'green'"
        class="co-comfy-meta-tag"
      >
        {{
          isSelectedTtsNonCommercial
            ? t("comfy.licenseNonCommercial")
            : t("comfy.licenseCommercialOk")
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
      <ATag
        v-if="selectedTtsEngine.min_vram_mib > 0"
        class="co-comfy-meta-tag co-comfy-mono"
      >
        {{
          t("comfy.ttsMinVram", {
            vram: formatColabOneVramGiB(selectedTtsEngine.min_vram_mib),
          })
        }}
      </ATag>
      <ATag
        v-if="(selectedTtsEngine.min_disk_mib ?? 0) > 0"
        class="co-comfy-meta-tag co-comfy-mono"
      >
        {{
          t("comfy.ttsMinDisk", {
            disk: formatColabOneDiskGiB(selectedTtsEngine.min_disk_mib ?? 0),
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
        v-if="selectedTtsEngine.supports_custom_speakers"
        color="gold"
        class="co-comfy-meta-tag"
      >
        {{ t("comfy.ttsCapabilityCustomSpeakers") }}
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
    <div
      v-if="
        selectedTtsEngine.license_notice &&
        selectedTtsEngine.license_notice.trim()
      "
      class="co-comfy-tts-strip-notice"
      role="note"
    >
      <PhWarningCircle
        :size="14"
        weight="fill"
        class="co-comfy-tts-strip-notice-icon"
        aria-hidden="true"
      />
      <span>{{ tb(selectedTtsEngine.license_notice) }}</span>
    </div>
  </div>

  <!-- Voice Audition Preview Card -->
  <div
    v-if="showVoicePreview"
    class="co-comfy-voice-preview-card"
    :class="{ 'is-wide': wide }"
  >
    <div class="co-comfy-voice-preview-head">
      <div class="co-comfy-voice-preview-heading">
        <PhSpeakerHigh
          :size="15"
          class="co-comfy-voice-preview-icon"
          aria-hidden="true"
        />
        <strong class="co-comfy-voice-preview-title">
          {{ t("comfy.voicePreviewTitle") }}
        </strong>
      </div>
      <div class="co-comfy-voice-preview-badges">
        <ATag v-if="selectedVoiceGenderLabel" class="co-comfy-meta-tag">
          {{ selectedVoiceGenderLabel }}
        </ATag>
        <ATag
          v-if="selectedVoicePreset?.style"
          color="purple"
          class="co-comfy-meta-tag"
        >
          {{ tb(selectedVoicePreset.style) }}
        </ATag>
        <ATag
          :color="selectedVoiceSourceTag.color"
          class="co-comfy-meta-tag"
        >
          {{ selectedVoiceSourceTag.label }}
        </ATag>
      </div>
    </div>

    <p
      v-if="selectedVoicePreset?.description"
      class="co-comfy-voice-preview-desc"
    >
      {{ tb(selectedVoicePreset.description) }}
    </p>

    <div class="co-comfy-voice-preview-text-group">
      <div class="co-comfy-voice-preview-text-head">
        <label
          :for="`${inputId}-preview-text`"
          class="co-comfy-voice-preview-text-label"
        >
          {{ t("comfy.voicePreviewTextLabel") }}
        </label>
        <AButton
          v-if="canResetVoicePreviewText"
          type="link"
          size="small"
          class="co-comfy-voice-preview-reset-btn"
          :disabled="disabled || voicePreviewBusy"
          @click="emit('voice-preview-reset')"
        >
          <template #icon><PhArrowCounterClockwise :size="13" /></template>
          {{ t("comfy.voicePreviewResetText") }}
        </AButton>
      </div>
      <ATextarea
        :id="`${inputId}-preview-text`"
        :value="voicePreviewTextValue"
        :placeholder="t('comfy.voicePreviewTextPlaceholder')"
        :auto-size="{ minRows: 2, maxRows: 4 }"
        :maxlength="600"
        show-count
        :disabled="disabled || voicePreviewBusy"
        @update:value="(val) => emit('update:voice-preview-text', val)"
      />
    </div>

    <div class="co-comfy-voice-preview-actions">
      <AButton
        type="primary"
        :ghost="Boolean(voicePreviewAudioUrl) && !voicePreviewBusy"
        size="small"
        class="co-comfy-voice-preview-btn"
        :loading="voicePreviewBusy"
        :disabled="
          (!voicePreviewRemoteReady && !voicePreviewCanOfflinePlay) ||
          voicePreviewBusy ||
          disabled ||
          voicePreviewDisabled
        "
        @click="emit('voice-preview-play')"
      >
        <template #icon>
          <PhPlay :size="13" weight="fill" />
        </template>
        {{
          voicePreviewBusy
            ? t("comfy.voicePreviewBtnGenerating")
            : voicePreviewAudioUrl
              ? t("comfy.voicePreviewBtnReplay")
              : t("comfy.voicePreviewBtn")
        }}
      </AButton>
      <AButton
        v-if="voiceAudioPlaying"
        size="small"
        danger
        @click="stopVoicePreviewPlayback"
      >
        <template #icon><PhStop :size="13" weight="fill" /></template>
        {{ t("comfy.voicePreviewBtnStop") }}
      </AButton>
      <span
        v-if="voicePreviewBusy && voicePreviewStatus"
        class="co-comfy-voice-preview-status"
        role="status"
      >
        {{ tb(voicePreviewStatus) }}
      </span>
      <span
        v-else-if="
          voicePreviewAudioUrl && !voicePreviewBusy && !voicePreviewError
        "
        class="co-comfy-voice-preview-ready"
      >
        {{ t("comfy.voicePreviewReady") }}
      </span>
    </div>

    <div
      v-if="
        voicePreviewPrefetchActive &&
        (voicePreviewPrefetchTotal ?? 0) > 1
      "
      class="co-comfy-voice-preview-prefetch"
      role="status"
    >
      <span class="co-comfy-voice-preview-prefetch-dot" aria-hidden="true" />
      <span class="co-comfy-voice-preview-prefetch-text">
        {{
          t("comfy.voicePreviewPrefetchRunning", {
            done: voicePreviewPrefetchDone ?? 0,
            total: voicePreviewPrefetchTotal ?? 0,
          })
        }}
      </span>
      <AButton
        type="link"
        size="small"
        class="co-comfy-voice-preview-prefetch-cancel"
        @click="emit('voice-preview-prefetch-cancel')"
      >
        {{ t("comfy.voicePreviewPrefetchCancel") }}
      </AButton>
    </div>
    <div
      v-else-if="
        (voicePreviewPrefetchTotal ?? 0) > 1 &&
        (voicePreviewPrefetchDone ?? 0) >= (voicePreviewPrefetchTotal ?? 0)
      "
      class="co-comfy-voice-preview-prefetch is-done"
    >
      <span class="co-comfy-voice-preview-prefetch-text">
        {{
          t("comfy.voicePreviewPrefetchDone", {
            total: voicePreviewPrefetchTotal ?? 0,
          })
        }}
      </span>
    </div>

    <small class="co-comfy-voice-preview-hint">
      {{ voicePreviewHint }}
    </small>

    <div
      v-if="formattedVoicePreviewError"
      class="co-comfy-voice-preview-error"
      role="alert"
    >
      <PhWarningCircle
        :size="14"
        weight="fill"
        class="co-comfy-voice-preview-error-icon"
        aria-hidden="true"
      />
      <span>{{ formattedVoicePreviewError }}</span>
    </div>

    <div
      v-if="voicePreviewAudioUrl"
      class="co-comfy-voice-preview-player-box"
    >
      <audio
        :key="voicePreviewAudioUrl"
        ref="voicePreviewAudioRef"
        :src="voicePreviewAudioUrl"
        controls
        preload="metadata"
        class="co-comfy-voice-preview-audio"
        @play="voiceAudioPlaying = true"
        @pause="voiceAudioPlaying = false"
        @ended="voiceAudioPlaying = false"
        @error="voiceAudioPlaying = false"
        @abort="voiceAudioPlaying = false"
        @emptied="voiceAudioPlaying = false"
      />
      <div v-if="voicePreviewMeta" class="co-comfy-voice-preview-meta">
        <ATag
          v-if="
            typeof voicePreviewMeta.durationSeconds === 'number' &&
            Number.isFinite(voicePreviewMeta.durationSeconds) &&
            voicePreviewMeta.durationSeconds > 0
          "
          class="co-comfy-meta-tag co-comfy-mono"
        >
          {{
            t("comfy.voicePreviewDuration", {
              seconds: formatPreviewDurationSeconds(
                voicePreviewMeta.durationSeconds,
              ),
            })
          }}
        </ATag>
        <ATag
          v-if="
            typeof voicePreviewMeta.sampleRate === 'number' &&
            Number.isFinite(voicePreviewMeta.sampleRate) &&
            voicePreviewMeta.sampleRate > 0
          "
          class="co-comfy-meta-tag co-comfy-mono"
        >
          {{
            t("comfy.ttsSampleRate", {
              khz: formatTtsSampleRateKhz(voicePreviewMeta.sampleRate),
            })
          }}
        </ATag>
        <ATag
          v-if="voicePreviewMeta.localCached"
          color="success"
          class="co-comfy-meta-tag"
        >
          {{ t("comfy.voicePreviewLocalCachedTag") }}
        </ATag>
        <ATag
          v-else-if="voicePreviewMeta.cached"
          color="success"
          class="co-comfy-meta-tag"
        >
          {{ t("comfy.voicePreviewCachedTag") }}
        </ATag>
        <ATag
          v-if="voicePreviewMeta.engineName"
          class="co-comfy-meta-tag co-comfy-voice-preview-engine-tag"
          :title="tb(voicePreviewMeta.engineName)"
        >
          <span class="co-comfy-voice-preview-engine-text">{{
            tb(voicePreviewMeta.engineName)
          }}</span>
        </ATag>
      </div>
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
  max-width: 100%;
  min-width: 0;
}

.co-comfy-select :deep(.ant-select-selector) {
  min-width: 0;
}

.co-comfy-select :deep(.ant-select-selection-item) {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
.co-comfy-tts-strip.is-wide,
.co-comfy-voice-preview-card.is-wide {
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
  min-width: 0;
  max-width: 100%;
  box-sizing: border-box;
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
  min-width: 0;
  overflow-wrap: anywhere;
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
  max-width: 100%;
  box-sizing: border-box;
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
  min-width: 0;
  max-width: 100%;
}

.co-comfy-tts-strip-title {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  max-width: 100%;
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
  min-width: 0;
  overflow-wrap: anywhere;
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
  min-width: 0;
  max-width: 100%;
}

.co-comfy-tts-strip-notice {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-top: 2px;
  padding: 6px 9px;
  border-radius: 8px;
  background: var(--co-warning-bg);
  border: 1px solid var(--co-warning-border);
  color: var(--co-warning-text);
  font-size: 12px;
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.co-comfy-tts-strip.is-non-commercial .co-comfy-tts-strip-notice {
  background: color-mix(in srgb, var(--co-surface) 60%, var(--co-warning-bg));
}

.co-comfy-tts-strip-notice-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

/* Voice Audition Preview Card */
.co-comfy-voice-preview-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 13px;
  border-radius: 11px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  min-width: 0;
  max-width: 100%;
  box-sizing: border-box;
}

.co-comfy-voice-preview-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
  max-width: 100%;
}

.co-comfy-voice-preview-heading {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.co-comfy-voice-preview-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-comfy-voice-preview-icon {
  color: var(--co-accent-strong);
}

.co-comfy-voice-preview-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-voice-preview-badges {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
  max-width: 100%;
}

.co-comfy-voice-preview-badges :deep(.ant-tag),
.co-comfy-voice-preview-meta :deep(.ant-tag),
.co-comfy-tts-strip-tags :deep(.ant-tag) {
  margin-inline-end: 0;
}

.co-comfy-voice-preview-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.48;
  color: var(--co-secondary-text);
  overflow-wrap: anywhere;
}

.co-comfy-voice-preview-text-group {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.co-comfy-voice-preview-text-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.co-comfy-voice-preview-text-label {
  font-size: 12px;
  font-weight: 550;
  color: var(--co-secondary-text);
}

.co-comfy-voice-preview-reset-btn {
  height: auto;
  padding: 0 2px;
  font-size: 12px;
}

.co-comfy-voice-preview-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.co-comfy-voice-preview-btn {
  font-weight: 550;
}

.co-comfy-voice-preview-status {
  font-size: 12px;
  line-height: 1.4;
  color: var(--co-secondary-text);
  overflow-wrap: anywhere;
}

.co-comfy-voice-preview-ready {
  font-size: 12px;
  font-weight: 550;
  color: var(--co-success-text);
}

.co-comfy-voice-preview-prefetch {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  padding: 5px 9px;
  border-radius: 7px;
  background: var(--co-surface);
  border: 1px dashed var(--co-border-secondary);
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-comfy-voice-preview-prefetch.is-done {
  border-style: solid;
  color: var(--co-success-text);
}

.co-comfy-voice-preview-prefetch-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--co-accent);
  flex-shrink: 0;
  animation: co-voice-prefetch-pulse 1.4s ease-in-out infinite;
}

@keyframes co-voice-prefetch-pulse {
  0%,
  100% {
    opacity: 0.35;
    transform: scale(0.85);
  }
  50% {
    opacity: 1;
    transform: scale(1.1);
  }
}

.co-comfy-voice-preview-prefetch-text {
  line-height: 1.4;
}

.co-comfy-voice-preview-prefetch-cancel {
  height: auto;
  padding: 0 2px;
  margin-left: auto;
  font-size: 12px;
}

:global(.co-comfy-voice-opt-row) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  min-width: 0;
}

:global(.co-comfy-voice-opt-label) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:global(.co-comfy-voice-ready-pill) {
  flex-shrink: 0;
  font-size: 11px;
  line-height: 1.2;
  font-weight: 550;
  padding: 2px 6px;
  border-radius: 999px;
  color: var(--co-success-text, #16a34a);
  background: var(--co-success-bg, rgba(22, 163, 74, 0.12));
  border: 1px solid var(--co-success-border, rgba(22, 163, 74, 0.28));
}

.co-comfy-voice-preview-hint {
  display: block;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-muted);
  overflow-wrap: anywhere;
}

.co-comfy-voice-preview-error {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 7px 10px;
  border-radius: 8px;
  background: var(--co-danger-bg);
  border: 1px solid var(--co-danger-border);
  color: var(--co-danger-text);
  font-size: 12px;
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.co-comfy-voice-preview-error-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

.co-comfy-voice-preview-player-box {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 9px 10px;
  border-radius: 9px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  min-width: 0;
  max-width: 100%;
  box-sizing: border-box;
}

.co-comfy-voice-preview-audio {
  width: 100%;
  max-width: 100%;
  height: 36px;
  display: block;
}

.co-comfy-voice-preview-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
  max-width: 100%;
}

.co-comfy-voice-preview-meta :deep(.ant-tag) {
  margin-inline-end: 0;
  max-width: 100%;
}

.co-comfy-voice-preview-meta :deep(.co-comfy-voice-preview-engine-tag) {
  max-width: 100%;
  min-width: 0;
  padding-top: 0;
  padding-bottom: 0;
  line-height: 18px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  word-break: keep-all;
  overflow-wrap: normal;
  text-box-trim: none !important;
}

.co-comfy-voice-preview-meta .co-comfy-voice-preview-engine-text {
  display: block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  overflow-wrap: normal;
  text-box-trim: none !important;
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
  min-width: 0;
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
  flex-wrap: nowrap;
  min-width: 0;
}

.co-comfy-media-status-tags {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
  flex: 1 1 0;
}

.co-comfy-media-status-tags :deep(.ant-tag) {
  margin-inline-end: 0;
}

.co-comfy-filename-pill {
  max-width: min(240px, 100%);
  min-width: 0;
  justify-content: flex-start;
  overflow: hidden;
  text-box-trim: none !important;
}

.co-comfy-filename-pill-text {
  display: block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 1.4;
  text-box-trim: none !important;
}

.co-comfy-media-clear-btn {
  flex-shrink: 0;
}
</style>

<style>
body .ant-select-dropdown.co-comfy-select-dropdown {
  max-width: min(580px, calc(100vw - 24px));
}

body
  .ant-select-dropdown.co-comfy-select-dropdown
  .ant-select-item-option-content {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
