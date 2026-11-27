<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  Modal as AModal,
  Select as ASelect,
  SelectOption as ASelectOption,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhImage,
  PhMagnifyingGlassPlus,
  PhPlus,
  PhSparkle,
  PhSpinnerGap,
  PhUploadSimple,
  PhX,
} from "@phosphor-icons/vue";
import {
  COLABONE_FASHION_MAX_REFS_PER_GROUP,
  getColabOneFashionAllowedRoleIds,
  type ColabOneFashionPhotoGroupKind,
} from "./comfyui";
import { t } from "./i18n";

export interface ColabOneFashionPhotoItem {
  key: string;
  filename: string;
  role: string;
  previewUrl?: string;
  uploading?: boolean;
  uploadedRemote?: boolean;
  hasPending?: boolean;
  hasDraft?: boolean;
}

const props = withDefaults(
  defineProps<{
    group: ColabOneFashionPhotoGroupKind;
    items: ColabOneFashionPhotoItem[];
    maxCount?: number;
    disabled?: boolean;
    dropzoneId?: string;
    active?: boolean;
  }>(),
  {
    maxCount: COLABONE_FASHION_MAX_REFS_PER_GROUP,
    disabled: false,
    dropzoneId: "",
    active: true,
  },
);

const emit = defineEmits<{
  "add-files": [files: File[]];
  remove: [slotKey: string];
  promote: [slotKey: string];
  "change-role": [slotKey: string, role: string];
}>();

const fileInputRef = ref<HTMLInputElement | null>(null);
const isDragging = ref(false);
const previewSlotKey = ref<string | null>(null);

const previewEntry = computed(() => {
  if (!previewSlotKey.value) return null;
  const idx = props.items.findIndex((item) => item.key === previewSlotKey.value);
  if (idx < 0) return null;
  const item = props.items[idx];
  if (!item.previewUrl) return null;
  const roleObj = roleOptions.value.find((opt) => opt.id === item.role);
  return {
    item,
    idx,
    roleLabel: roleObj?.label || item.role,
  };
});

function openPreview(item: ColabOneFashionPhotoItem) {
  if (!item.previewUrl || item.uploading) return;
  previewSlotKey.value = item.key;
}

function closePreview() {
  previewSlotKey.value = null;
}

watch(previewEntry, (entry) => {
  if (!entry && previewSlotKey.value !== null) {
    previewSlotKey.value = null;
  }
});

watch(
  () => props.active,
  (isActive) => {
    if (!isActive) {
      isDragging.value = false;
      if (previewSlotKey.value !== null) {
        previewSlotKey.value = null;
      }
    }
  },
);

const isModel = computed(() => props.group === "model");
const canAddMore = computed(
  () => !props.disabled && props.items.length < props.maxCount,
);
const groupTitleId = computed(
  () => `${props.dropzoneId || `co-fashion-${props.group}`}-title`,
);
const groupDescId = computed(
  () => `${props.dropzoneId || `co-fashion-${props.group}`}-desc`,
);

watch(
  () => props.disabled,
  (disabled) => {
    if (disabled && isDragging.value) {
      isDragging.value = false;
    }
  },
);

const roleOptions = computed(() => {
  const ids = getColabOneFashionAllowedRoleIds(props.group);
  return ids.map((id) => {
    switch (id) {
      case "front":
        return {
          id,
          label: t("comfy.fashionRoleFront"),
          hint: isModel.value
            ? t("comfy.fashionRoleFrontHint")
            : t("comfy.fashionRoleGarmentFrontHint"),
        };
      case "side":
        return {
          id,
          label: t("comfy.fashionRoleSide"),
          hint: isModel.value
            ? t("comfy.fashionRoleSideHint")
            : t("comfy.fashionRoleGarmentSideHint"),
        };
      case "back":
        return {
          id,
          label: t("comfy.fashionRoleBack"),
          hint: isModel.value
            ? t("comfy.fashionRoleBackHint")
            : t("comfy.fashionRoleGarmentBackHint"),
        };
      case "full_body":
        return {
          id,
          label: t("comfy.fashionRoleFullBody"),
          hint: t("comfy.fashionRoleFullBodyHint"),
        };
      case "closeup":
        return {
          id,
          label: t("comfy.fashionRoleCloseup"),
          hint: t("comfy.fashionRoleCloseupHint"),
        };
      case "detail":
        return {
          id,
          label: t("comfy.fashionRoleDetail"),
          hint: t("comfy.fashionRoleDetailHint"),
        };
      case "flatlay":
        return {
          id,
          label: t("comfy.fashionRoleFlatlay"),
          hint: t("comfy.fashionRoleFlatlayHint"),
        };
      default:
        return {
          id: "auto",
          label: t("comfy.fashionRoleAuto"),
          hint: t("comfy.fashionRoleAutoHint"),
        };
    }
  });
});

const smartHints = computed<Array<{ tone: "accent" | "muted"; text: string }>>(
  () => {
    const count = props.items.length;
    if (count === 0) {
      return [];
    }
    if (isModel.value) {
      return [
        {
          tone: count > 1 ? "accent" : "muted",
          text:
            count === 1
              ? t("comfy.fashionModelTipSingle")
              : t("comfy.fashionModelTipMulti", { count }),
        },
      ];
    }
    const hasBack = props.items.some((item) => item.role === "back");
    const hasDetail = props.items.some((item) => item.role === "detail");
    const list: Array<{ tone: "accent" | "muted"; text: string }> = [];
    if (hasBack) {
      list.push({ tone: "accent", text: t("comfy.fashionGarmentTipBack") });
    }
    if (hasDetail) {
      list.push({ tone: "accent", text: t("comfy.fashionGarmentTipDetail") });
    }
    if (list.length === 0) {
      list.push({
        tone: count > 1 ? "accent" : "muted",
        text:
          count === 1
            ? t("comfy.fashionGarmentTipSingle")
            : t("comfy.fashionGarmentTipMulti", { count }),
      });
    }
    return list;
  },
);

function openPicker() {
  if (!canAddMore.value) return;
  fileInputRef.value?.click();
}

function onInputChange(ev: Event) {
  const input = ev.target as HTMLInputElement | null;
  const files = input?.files ? Array.from(input.files) : [];
  if (input) {
    input.value = "";
  }
  if (files.length > 0 && !props.disabled) {
    emit("add-files", files);
  }
}

function hasFileDragPayload(ev: DragEvent): boolean {
  const dt = ev.dataTransfer;
  if (!dt) return false;
  if (dt.types && dt.types.length > 0) {
    return Array.from(dt.types).includes("Files");
  }
  return Boolean(dt.files && dt.files.length > 0);
}

function onDragOver(ev: DragEvent) {
  const hasFiles = hasFileDragPayload(ev);
  if (hasFiles) {
    ev.preventDefault();
    if (ev.dataTransfer) {
      ev.dataTransfer.dropEffect = !props.disabled ? "copy" : "none";
    }
  }
  if (props.disabled || !hasFiles) {
    isDragging.value = false;
    return;
  }
  isDragging.value = true;
}

function onDragLeave(ev: DragEvent) {
  const current = ev.currentTarget as HTMLElement | null;
  const related = ev.relatedTarget as Node | null;
  if (current && related && current.contains(related)) {
    return;
  }
  isDragging.value = false;
}

function onDrop(ev: DragEvent) {
  isDragging.value = false;
  if (!hasFileDragPayload(ev)) return;
  ev.preventDefault();
  if (props.disabled) return;
  const files = ev.dataTransfer?.files ? Array.from(ev.dataTransfer.files) : [];
  if (files.length > 0) {
    emit("add-files", files);
  }
}

function onRoleChange(slotKey: string, val: unknown) {
  if (typeof val === "string" && val.trim()) {
    emit("change-role", slotKey, val.trim());
  }
}
</script>

<template>
  <div
    class="co-comfy-field co-fashion-photo-group"
    :class="{ 'is-dragover': isDragging, 'is-disabled': disabled }"
    role="group"
    :aria-labelledby="groupTitleId"
    :aria-describedby="groupDescId"
    :data-testid="`comfy-fashion-group-${group}`"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
    <input
      ref="fileInputRef"
      type="file"
      accept=".png,.jpg,.jpeg,.webp,image/png,image/jpeg,image/webp"
      multiple
      class="co-fashion-hidden-input"
      @change="onInputChange"
    />

    <div class="co-fashion-group-head">
      <div class="co-fashion-group-title-row">
        <span :id="groupTitleId" class="co-fashion-group-title">
          {{
            isModel
              ? t("comfy.fashionModelGroupTitle")
              : t("comfy.fashionGarmentGroupTitle")
          }}
        </span>
        <span class="co-fashion-required-pill">
          {{ t("comfy.fashionGroupRequiredBadge") }}
        </span>
      </div>
      <span class="co-fashion-count-text">
        {{
          t("comfy.fashionGroupCountBadge", {
            count: items.length,
            max: maxCount,
          })
        }}
      </span>
    </div>

    <p :id="groupDescId" class="co-fashion-group-desc">
      {{
        isModel
          ? t("comfy.fashionModelGroupDesc")
          : t("comfy.fashionGarmentGroupDesc")
      }}
    </p>

    <!-- Empty Initial Dropzone -->
    <button
      v-if="items.length === 0"
      :id="dropzoneId || undefined"
      type="button"
      class="co-fashion-empty-dropzone"
      :disabled="disabled"
      :data-testid="`comfy-fashion-empty-${group}`"
      @click="openPicker"
    >
      <span class="co-fashion-empty-icon" aria-hidden="true">
        <PhUploadSimple :size="18" weight="duotone" />
      </span>
      <span class="co-fashion-empty-copy">
        <strong class="co-fashion-empty-title">
          {{
            isModel
              ? t("comfy.fashionModelEmptyTitle")
              : t("comfy.fashionGarmentEmptyTitle")
          }}
        </strong>
        <span class="co-fashion-empty-sub">
          {{ t("comfy.fashionEmptySubHint") }}
        </span>
      </span>
    </button>

    <!-- Populated Thumbnail Strip + Persistent "+" Tile -->
    <div v-else class="co-fashion-grid">
      <div
        v-for="(item, idx) in items"
        :key="item.key"
        class="co-fashion-card"
        :class="{
          'is-primary': idx === 0,
          'is-special-role':
            !isModel && (item.role === 'back' || item.role === 'detail'),
        }"
        :data-testid="`comfy-fashion-card-${item.key}`"
      >
        <div class="co-fashion-thumb-box">
          <button
            v-if="item.previewUrl"
            type="button"
            class="co-fashion-thumb-trigger"
            :disabled="item.uploading"
            :title="t('comfy.fashionPreviewClickHint')"
            :aria-label="`${t('comfy.fashionPreviewClickHint')} #${idx + 1}: ${item.filename}`"
            @click="openPreview(item)"
          >
            <img
              :src="item.previewUrl"
              :alt="item.filename"
              draggable="false"
              class="co-fashion-thumb-img"
            />
            <span class="co-fashion-zoom-pill" aria-hidden="true">
              <PhMagnifyingGlassPlus :size="14" weight="bold" />
            </span>
          </button>
          <div v-else class="co-fashion-thumb-fallback" aria-hidden="true">
            <PhImage :size="22" weight="duotone" />
          </div>

          <div
            v-if="item.uploading"
            class="co-fashion-uploading-mask"
            role="status"
            :aria-label="t('common.loading')"
          >
            <PhSpinnerGap :size="18" class="co-fashion-spin" aria-hidden="true" />
          </div>

          <span class="co-fashion-order-badge">
            {{ idx === 0 ? t("comfy.fashionPrimaryTag") : `#${idx + 1}` }}
          </span>

          <div class="co-fashion-card-actions">
            <button
              v-if="idx > 0"
              type="button"
              class="co-fashion-promote-btn"
              :disabled="disabled || item.uploading"
              :title="t('comfy.fashionPromoteTitle')"
              :aria-label="`${t('comfy.fashionPromoteTitle')} #${idx + 1}: ${item.filename}`"
              @click.stop="emit('promote', item.key)"
            >
              {{ t("comfy.fashionPromoteBtn") }}
            </button>
            <button
              type="button"
              class="co-fashion-remove-btn"
              :disabled="disabled || item.uploading"
              :title="t('comfy.fashionRemovePhotoTitle')"
              :aria-label="`${t('comfy.fashionRemovePhotoTitle')} #${idx + 1}: ${item.filename}`"
              @click.stop="emit('remove', item.key)"
            >
              <PhX :size="12" weight="bold" />
            </button>
          </div>
        </div>

        <div class="co-fashion-card-foot">
          <div class="co-fashion-filename" :title="item.filename">
            {{ item.filename }}
          </div>
          <ASelect
            :value="item.role"
            size="small"
            class="co-fashion-role-select"
            :class="{
              'is-accent-role':
                !isModel && (item.role === 'back' || item.role === 'detail'),
            }"
            :aria-label="t('comfy.fashionRoleSelectAria', { name: item.filename })"
            :disabled="disabled || item.uploading"
            :dropdown-match-select-width="false"
            popup-class-name="co-fashion-role-dropdown"
            option-label-prop="label"
            @update:value="(val) => onRoleChange(item.key, val)"
          >
            <ASelectOption
              v-for="opt in roleOptions"
              :key="opt.id"
              :value="opt.id"
              :label="opt.label"
            >
              <div class="co-fashion-role-option">
                <div class="co-fashion-role-option-title">
                  <PhSparkle
                    v-if="opt.id === 'auto'"
                    :size="12"
                    weight="duotone"
                    class="co-fashion-role-sparkle"
                  />
                  <span>{{ opt.label }}</span>
                </div>
                <div class="co-fashion-role-option-hint">{{ opt.hint }}</div>
              </div>
            </ASelectOption>
          </ASelect>
        </div>
      </div>

      <!-- Dashed "+" Add More Tile -->
      <button
        v-if="items.length < maxCount"
        type="button"
        class="co-fashion-add-tile"
        :disabled="disabled"
        :data-testid="`comfy-fashion-add-${group}`"
        @click="openPicker"
      >
        <span class="co-fashion-add-plus" aria-hidden="true">
          <PhPlus :size="18" weight="bold" />
        </span>
        <span class="co-fashion-add-label">
          {{
            isModel
              ? t("comfy.fashionAddMoreModel")
              : t("comfy.fashionAddMoreGarment")
          }}
        </span>
        <span class="co-fashion-add-sub">
          {{ t("comfy.fashionAddMoreHint") }}
        </span>
      </button>
    </div>

    <!-- Smart Micro-Hints -->
    <div
      v-if="smartHints.length"
      class="co-fashion-smart-hints"
      role="status"
      aria-live="polite"
    >
      <div
        v-for="(hint, idx) in smartHints"
        :key="idx"
        class="co-fashion-smart-hint"
        :class="`is-${hint.tone}`"
      >
        {{ hint.text }}
      </div>
    </div>

    <!-- Full-Size Reference Photo Zoom Modal -->
    <AModal
      :open="!!previewEntry"
      :footer="null"
      :width="680"
      centered
      :z-index="1060"
      :title="t('comfy.fashionPreviewPhotoTitle')"
      @cancel="closePreview"
    >
      <div v-if="previewEntry" class="co-fashion-preview-modal">
        <div class="co-fashion-preview-meta">
          <ATag color="blue" :bordered="false">
            {{
              previewEntry.idx === 0
                ? t("comfy.fashionPrimaryTag")
                : `#${previewEntry.idx + 1}`
            }}
          </ATag>
          <ATag :bordered="false">{{ previewEntry.roleLabel }}</ATag>
          <span
            class="co-fashion-preview-filename"
            :title="previewEntry.item.filename"
          >
            {{ previewEntry.item.filename }}
          </span>
        </div>
        <div class="co-fashion-preview-stage">
          <img
            :src="previewEntry.item.previewUrl"
            :alt="previewEntry.item.filename"
            class="co-fashion-preview-full-img"
          />
        </div>
      </div>
    </AModal>
  </div>
</template>

<style scoped>
.co-fashion-photo-group {
  container-type: inline-size;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border-radius: var(--co-radius-md, 12px);
  border: 1px solid var(--co-border);
  background: var(--co-subtle);
  transition:
    border-color 160ms ease,
    background-color 160ms ease,
    box-shadow 160ms ease;
}

.co-fashion-photo-group.is-dragover {
  border-color: var(--co-accent);
  background: var(--co-selected);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-fashion-hidden-input {
  display: none;
}

.co-fashion-group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.co-fashion-group-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.co-fashion-group-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-fashion-required-pill {
  font-size: 11px;
  font-weight: 500;
  padding: 1px 7px;
  border-radius: 9999px;
  color: var(--co-accent-strong);
  background: var(--co-selected);
  border: 1px solid color-mix(in srgb, var(--co-accent) 28%, transparent);
}

.co-fashion-count-text {
  font-size: 12px;
  color: var(--co-muted);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}

.co-fashion-group-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-muted);
}

.co-fashion-empty-dropzone {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  border-radius: var(--co-radius, 10px);
  border: 1.5px dashed var(--co-input-border);
  background: var(--co-surface);
  color: var(--co-text);
  text-align: left;
  cursor: pointer;
  transition:
    border-color 150ms ease,
    background-color 150ms ease;
}

.co-fashion-empty-dropzone:hover:not(:disabled),
.co-fashion-empty-dropzone:focus-visible {
  border-color: var(--co-accent);
  background: var(--co-selected);
  outline: none;
}

.co-fashion-empty-dropzone:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.co-fashion-empty-icon {
  width: 38px;
  height: 38px;
  border-radius: 10px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-fashion-empty-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.co-fashion-empty-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-fashion-empty-sub {
  font-size: 12px;
  color: var(--co-muted);
}

.co-fashion-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

@media (max-width: 600px) {
  .co-fashion-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@container (max-width: 460px) {
  .co-fashion-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

.co-fashion-card {
  display: flex;
  flex-direction: column;
  border-radius: var(--co-radius, 10px);
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  overflow: hidden;
  transition: border-color 150ms ease;
}

.co-fashion-card:hover {
  border-color: color-mix(in srgb, var(--co-accent) 55%, var(--co-border));
}

.co-fashion-card.is-special-role {
  border-color: color-mix(in srgb, var(--co-accent) 45%, var(--co-border));
}

.co-fashion-thumb-box {
  position: relative;
  aspect-ratio: 4 / 3;
  background: var(--co-track);
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
}

.co-fashion-thumb-trigger {
  position: relative;
  width: 100%;
  height: 100%;
  padding: 0;
  border: none;
  background: transparent;
  cursor: zoom-in;
  display: block;
}

.co-fashion-thumb-trigger:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: -2px;
}

.co-fashion-thumb-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.co-fashion-zoom-pill {
  position: absolute;
  right: 6px;
  bottom: 6px;
  width: 24px;
  height: 24px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--co-surface) 88%, transparent);
  color: var(--co-text);
  border: 1px solid var(--co-border);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  opacity: 0;
  transition: opacity 150ms ease;
  backdrop-filter: blur(4px);
}

.co-fashion-thumb-trigger:hover .co-fashion-zoom-pill,
.co-fashion-thumb-trigger:focus-visible .co-fashion-zoom-pill {
  opacity: 1;
}

.co-fashion-preview-modal {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-fashion-preview-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.co-fashion-preview-filename {
  font-size: 12px;
  color: var(--co-muted);
  word-break: break-all;
}

.co-fashion-preview-stage {
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--co-track);
  border-radius: var(--co-radius, 10px);
  border: 1px solid var(--co-border);
  padding: 12px;
  max-height: 70vh;
  overflow: auto;
}

.co-fashion-preview-full-img {
  max-width: 100%;
  max-height: 64vh;
  object-fit: contain;
  border-radius: 6px;
  display: block;
}

.co-fashion-thumb-fallback {
  color: var(--co-muted);
  display: flex;
  align-items: center;
  justify-content: center;
}

.co-fashion-uploading-mask {
  position: absolute;
  inset: 0;
  background: var(--co-overlay);
  color: #ffffff;
  display: flex;
  align-items: center;
  justify-content: center;
}

.co-fashion-spin {
  animation: co-fashion-rotate var(--co-spin-duration, 4s) linear infinite;
}

@media (prefers-reduced-motion: reduce) {
  .co-fashion-spin {
    animation: none;
  }
}

@keyframes co-fashion-rotate {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

.co-fashion-order-badge {
  position: absolute;
  top: 6px;
  left: 6px;
  font-size: 11px;
  font-weight: 600;
  line-height: 1.2;
  padding: 2px 6px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--co-surface) 90%, transparent);
  color: var(--co-text);
  border: 1px solid var(--co-border);
  backdrop-filter: blur(4px);
}

.co-fashion-card-actions {
  position: absolute;
  top: 6px;
  right: 6px;
  display: flex;
  align-items: center;
  gap: 4px;
}

.co-fashion-promote-btn,
.co-fashion-remove-btn {
  border: 1px solid var(--co-border);
  background: color-mix(in srgb, var(--co-surface) 92%, transparent);
  color: var(--co-secondary-text);
  border-radius: 6px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  backdrop-filter: blur(4px);
  transition:
    color 140ms ease,
    border-color 140ms ease,
    background-color 140ms ease;
}

.co-fashion-promote-btn {
  font-size: 11px;
  font-weight: 500;
  padding: 2px 6px;
  line-height: 1.2;
}

.co-fashion-promote-btn:hover:not(:disabled) {
  color: var(--co-accent);
  border-color: var(--co-accent);
}

.co-fashion-remove-btn {
  width: 22px;
  height: 22px;
  padding: 0;
}

.co-fashion-remove-btn:hover:not(:disabled) {
  color: var(--co-danger-text);
  border-color: var(--co-danger);
}

.co-fashion-promote-btn:focus-visible,
.co-fashion-remove-btn:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 1px;
}

.co-fashion-promote-btn:disabled,
.co-fashion-remove-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

@media (hover: none), (pointer: coarse) {
  .co-fashion-card-actions {
    gap: 6px;
  }

  .co-fashion-promote-btn {
    min-height: 32px;
    padding: 4px 8px;
  }

  .co-fashion-remove-btn {
    min-width: 32px;
    min-height: 32px;
    width: 32px;
    height: 32px;
  }

  .co-fashion-zoom-pill {
    opacity: 0.85;
    width: 28px;
    height: 28px;
  }
}

.co-fashion-card-foot {
  padding: 6px 7px 7px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.co-fashion-filename {
  font-size: 11px;
  color: var(--co-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.co-fashion-role-select {
  width: 100%;
}

.co-fashion-role-select.is-accent-role :deep(.ant-select-selector) {
  border-color: color-mix(in srgb, var(--co-accent) 45%, var(--co-border)) !important;
  background: var(--co-selected) !important;
  color: var(--co-accent-strong) !important;
}

.co-fashion-role-option {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 2px 0;
}

.co-fashion-role-option-title {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  font-weight: 500;
  color: var(--co-text);
}

.co-fashion-role-sparkle {
  color: var(--co-accent);
}

.co-fashion-role-option-hint {
  font-size: 11px;
  color: var(--co-muted);
  white-space: normal;
  max-width: 220px;
}

.co-fashion-add-tile {
  min-height: 136px;
  border-radius: var(--co-radius, 10px);
  border: 1.5px dashed var(--co-input-border);
  background: var(--co-surface);
  padding: 10px 8px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 5px;
  text-align: center;
  cursor: pointer;
  color: var(--co-text);
  transition:
    border-color 150ms ease,
    background-color 150ms ease;
}

.co-fashion-add-tile:hover:not(:disabled),
.co-fashion-add-tile:focus-visible {
  border-color: var(--co-accent);
  background: var(--co-selected);
  outline: none;
}

.co-fashion-add-tile:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.co-fashion-add-plus {
  width: 32px;
  height: 32px;
  border-radius: 9999px;
  border: 1px solid var(--co-border);
  background: var(--co-subtle);
  color: var(--co-accent);
  display: flex;
  align-items: center;
  justify-content: center;
}

.co-fashion-add-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--co-text);
  line-height: 1.3;
}

.co-fashion-add-sub {
  font-size: 11px;
  color: var(--co-muted);
  line-height: 1.3;
}

.co-fashion-smart-hints {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding-top: 2px;
}

.co-fashion-smart-hint {
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-muted);
}

.co-fashion-smart-hint.is-accent {
  color: var(--co-accent-strong);
}
</style>

<style>
body .ant-select-dropdown.co-fashion-role-dropdown {
  min-width: 224px;
  max-width: min(300px, calc(100vw - 24px));
}

body
  .ant-select-dropdown.co-fashion-role-dropdown
  .ant-select-item-option-content {
  white-space: normal;
  overflow: visible;
  text-overflow: unset;
}
</style>
