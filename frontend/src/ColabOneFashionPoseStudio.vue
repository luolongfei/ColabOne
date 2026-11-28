<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  Modal as AModal,
  Select as ASelect,
  SelectOption as ASelectOption,
  Switch as ASwitch,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhImage,
  PhMagnifyingGlassPlus,
  PhPlus,
  PhSpinnerGap,
  PhSun,
  PhTextT,
  PhUploadSimple,
  PhUsersThree,
  PhX,
} from "@phosphor-icons/vue";
import {
  COLABONE_FASHION_MAX_POSE_REFS,
  COLABONE_FASHION_MAX_POSE_SHOTS,
  COLABONE_FASHION_POSE_CATEGORIES,
  COLABONE_FASHION_POSE_PACKS,
  COLABONE_FASHION_POSE_PRESETS,
  COLABONE_FASHION_POSE_PRESET_BY_ID,
  COLABONE_FASHION_POSE_VIEW_IDS,
  buildColabOneFashionPackShots,
  inferColabOneFashionPoseView,
  type ColabOneFashionPoseCategoryId,
  type ColabOneFashionPosePackId,
  type ColabOneFashionPosePreset,
  type ColabOneFashionPoseShot,
  type ColabOneFashionPoseShotView,
} from "./colabOneFashionPoseCatalog";
import { t } from "./i18n";

export interface ColabOneFashionPoseRefItem {
  key: string;
  filename: string;
  previewUrl?: string;
  uploading?: boolean;
  uploadedRemote?: boolean;
  hasPending?: boolean;
  hasDraft?: boolean;
}

const props = withDefaults(
  defineProps<{
    shots: ColabOneFashionPoseShot[];
    poseRefItems: ColabOneFashionPoseRefItem[];
    disabled?: boolean;
    active?: boolean;
    resetSeq?: number;
    studioId?: string;
  }>(),
  {
    disabled: false,
    active: true,
    resetSeq: 0,
    studioId: "co-fashion-pose-studio",
  },
);

const emit = defineEmits<{
  "update:shots": [shots: ColabOneFashionPoseShot[]];
  "add-ref-files": [files: File[], replaceIndex: number | null];
}>();

type PoseStudioTab = "library" | "reference" | "text";

const activeTab = ref<PoseStudioTab>("library");
const activeCategory = ref<ColabOneFashionPoseCategoryId>("all");
const editingShotIndex = ref<number | null>(null);
const textDraft = ref("");
const textView = ref<ColabOneFashionPoseShotView>("auto");
const isDragging = ref(false);
const fileInputRef = ref<HTMLInputElement | null>(null);
const previewRefKey = ref<string | null>(null);

const poseRefByKey = computed(() => {
  const map = new Map<string, ColabOneFashionPoseRefItem>();
  for (const item of props.poseRefItems) {
    map.set(item.key, item);
  }
  return map;
});

const uploadingRefItems = computed(() =>
  props.poseRefItems.filter((item) => item.uploading),
);

const previewEntry = computed(() => {
  if (!previewRefKey.value) return null;
  const item = poseRefByKey.value.get(previewRefKey.value);
  if (!item || !item.previewUrl) return null;
  const shotIdx = props.shots.findIndex(
    (s) => s.kind === "reference" && s.slot_key === item.key,
  );
  return { item, shotIdx };
});

watch(previewEntry, (entry) => {
  if (!entry && previewRefKey.value !== null) {
    previewRefKey.value = null;
  }
});

watch(
  () => props.active,
  (isActive) => {
    if (!isActive) {
      isDragging.value = false;
      previewRefKey.value = null;
      exitEditMode();
    }
  },
);

watch(
  () => props.resetSeq,
  () => {
    editingShotIndex.value = null;
    textDraft.value = "";
    textView.value = "auto";
    isDragging.value = false;
    previewRefKey.value = null;
  },
);

watch(
  () => props.shots.length,
  (len) => {
    if (editingShotIndex.value !== null && editingShotIndex.value >= len) {
      editingShotIndex.value = null;
    }
  },
);

watch(
  () => props.disabled,
  (disabled) => {
    if (disabled) {
      isDragging.value = false;
    }
  },
);

const editingShot = computed(() => {
  if (
    editingShotIndex.value === null ||
    editingShotIndex.value < 0 ||
    editingShotIndex.value >= props.shots.length
  ) {
    return null;
  }
  return props.shots[editingShotIndex.value];
});

const referenceShotEntries = computed(() =>
  props.shots
    .map((shot, idx) => ({ shot, idx }))
    .filter(
      (
        entry,
      ): entry is {
        shot: Extract<ColabOneFashionPoseShot, { kind: "reference" }>;
        idx: number;
      } => entry.shot.kind === "reference",
    ),
);

const textShotEntries = computed(() =>
  props.shots
    .map((shot, idx) => ({ shot, idx }))
    .filter(
      (
        entry,
      ): entry is {
        shot: Extract<ColabOneFashionPoseShot, { kind: "text" }>;
        idx: number;
      } => entry.shot.kind === "text",
    ),
);

const canAddMoreShots = computed(
  () =>
    !props.disabled &&
    (editingShotIndex.value !== null ||
      props.shots.length < COLABONE_FASHION_MAX_POSE_SHOTS),
);

const canReplaceUniqueRefSlot = computed(() => {
  const shot = editingShot.value;
  if (!shot || shot.kind !== "reference") return false;
  const refItem = poseRefByKey.value.get(shot.slot_key);
  if (refItem?.uploading) return false;
  let count = 0;
  for (const s of props.shots) {
    if (s.kind === "reference" && s.slot_key === shot.slot_key) {
      count += 1;
    }
  }
  return count === 1;
});

const canAddMoreRefs = computed(
  () =>
    !props.disabled &&
    canAddMoreShots.value &&
    (props.poseRefItems.length < COLABONE_FASHION_MAX_POSE_REFS ||
      canReplaceUniqueRefSlot.value),
);

const activePackId = computed<ColabOneFashionPosePackId | null>(() => {
  for (const pack of COLABONE_FASHION_POSE_PACKS) {
    if (props.shots.length !== pack.presetIds.length) continue;
    const match = props.shots.every(
      (s, i) => s.kind === "preset" && s.preset_id === pack.presetIds[i],
    );
    if (match) return pack.id;
  }
  return null;
});

const visiblePresets = computed(() => {
  if (activeCategory.value === "all") {
    return COLABONE_FASHION_POSE_PRESETS;
  }
  return COLABONE_FASHION_POSE_PRESETS.filter(
    (p) => p.category === activeCategory.value,
  );
});

const presetQueueIndices = computed(() => {
  const map = new Map<string, number[]>();
  props.shots.forEach((shot, idx) => {
    if (shot.kind === "preset") {
      const list = map.get(shot.preset_id) || [];
      list.push(idx + 1);
      map.set(shot.preset_id, list);
    }
  });
  return map;
});

const viewOptions = computed(() =>
  COLABONE_FASHION_POSE_VIEW_IDS.map((id) => ({
    id,
    label: viewLabel(id),
  })),
);

const textInspirationChips = computed(() => [
  t("comfy.fashionPoseChipWaist"),
  t("comfy.fashionPoseChipCollar"),
  t("comfy.fashionPoseChipBack"),
  t("comfy.fashionPoseChipWalk"),
  t("comfy.fashionPoseChipKidsSkirt"),
  t("comfy.fashionPoseChipKidsWave"),
]);

function categoryLabel(id: ColabOneFashionPoseCategoryId): string {
  switch (id) {
    case "front":
      return t("comfy.fashionPoseCatFront");
    case "side":
      return t("comfy.fashionPoseCatSide");
    case "back":
      return t("comfy.fashionPoseCatBack");
    case "dynamic":
      return t("comfy.fashionPoseCatDynamic");
    case "seated":
      return t("comfy.fashionPoseCatSeated");
    case "detail":
      return t("comfy.fashionPoseCatDetail");
    case "kids":
      return t("comfy.fashionPoseCatKids");
    default:
      return t("comfy.fashionPoseCatAll");
  }
}

function viewLabel(view: ColabOneFashionPoseShotView): string {
  switch (view) {
    case "front":
      return t("comfy.fashionPoseViewFront");
    case "side":
      return t("comfy.fashionPoseViewSide");
    case "back":
      return t("comfy.fashionPoseViewBack");
    case "detail":
      return t("comfy.fashionPoseViewDetail");
    default:
      return t("comfy.fashionPoseViewAuto");
  }
}

function sourceLabel(kind: ColabOneFashionPoseShot["kind"]): string {
  if (kind === "reference") return t("comfy.fashionPoseSourceReference");
  if (kind === "text") return t("comfy.fashionPoseSourceText");
  return t("comfy.fashionPoseSourcePreset");
}

function presetLabel(preset: ColabOneFashionPosePreset): string {
  return t(preset.labelKey) || preset.fallbackLabel;
}

function shotDisplayLabel(shot: ColabOneFashionPoseShot, idx: number): string {
  if (shot.kind === "preset") {
    const preset = COLABONE_FASHION_POSE_PRESET_BY_ID.get(shot.preset_id);
    return preset ? presetLabel(preset) : shot.preset_id;
  }
  if (shot.kind === "reference") {
    const refItem = poseRefByKey.value.get(shot.slot_key);
    const fname = refItem?.filename || "";
    const stem = fname.replace(/\.[^.]+$/, "").trim();
    return stem || t("comfy.fashionPoseRefFallbackLabel", { index: idx + 1 });
  }
  return shot.prompt.trim();
}

function shotDisplayView(shot: ColabOneFashionPoseShot): ColabOneFashionPoseShotView {
  if (shot.kind === "preset") {
    return COLABONE_FASHION_POSE_PRESET_BY_ID.get(shot.preset_id)?.view || "front";
  }
  return shot.view || "auto";
}

function shotSvgSpec(shot: ColabOneFashionPoseShot) {
  if (shot.kind !== "preset") return null;
  return COLABONE_FASHION_POSE_PRESET_BY_ID.get(shot.preset_id)?.svg || null;
}

function emitShots(next: ColabOneFashionPoseShot[]) {
  emit("update:shots", next.slice(0, COLABONE_FASHION_MAX_POSE_SHOTS));
}

function applyPack(packId: ColabOneFashionPosePackId) {
  if (props.disabled) return;
  exitEditMode();
  emitShots(buildColabOneFashionPackShots(packId));
}

function clearAllShots() {
  if (props.disabled) return;
  exitEditMode();
  emitShots([]);
}

function exitEditMode() {
  const current = editingShot.value;
  if (current && current.kind === "text" && textDraft.value === current.prompt) {
    textDraft.value = "";
    textView.value = "auto";
  }
  editingShotIndex.value = null;
}

function selectShotForEdit(idx: number) {
  if (props.disabled) return;
  if (editingShotIndex.value === idx) {
    exitEditMode();
    return;
  }
  editingShotIndex.value = idx;
  const target = props.shots[idx];
  if (!target) return;
  if (target.kind === "text") {
    textDraft.value = target.prompt;
    textView.value = target.view || "auto";
    activeTab.value = "text";
  } else if (target.kind === "reference") {
    activeTab.value = "reference";
  } else {
    activeTab.value = "library";
  }
}

function promoteShot(idx: number) {
  if (props.disabled || idx <= 0 || idx >= props.shots.length) return;
  const next = [...props.shots];
  const [moved] = next.splice(idx, 1);
  next.unshift(moved);
  if (editingShotIndex.value === idx) {
    editingShotIndex.value = 0;
  } else if (editingShotIndex.value !== null && editingShotIndex.value < idx) {
    editingShotIndex.value += 1;
  }
  emitShots(next);
}

function removeShot(idx: number) {
  if (props.disabled || idx < 0 || idx >= props.shots.length) return;
  const target = props.shots[idx];
  if (editingShotIndex.value === idx && target?.kind === "text" && textDraft.value === target.prompt) {
    textDraft.value = "";
    textView.value = "auto";
  }
  const next = props.shots.filter((_, i) => i !== idx);
  if (editingShotIndex.value === idx) {
    editingShotIndex.value = null;
  } else if (editingShotIndex.value !== null && editingShotIndex.value > idx) {
    editingShotIndex.value -= 1;
  }
  emitShots(next);
}

function onPresetCardClick(preset: ColabOneFashionPosePreset) {
  if (props.disabled) return;
  if (editingShotIndex.value !== null && editingShotIndex.value < props.shots.length) {
    const targetIdx = editingShotIndex.value;
    const next = [...props.shots];
    next[targetIdx] = { kind: "preset", preset_id: preset.id };
    exitEditMode();
    emitShots(next);
    return;
  }
  const existingIdx = props.shots.findIndex(
    (s) => s.kind === "preset" && s.preset_id === preset.id,
  );
  if (existingIdx >= 0) {
    removeShot(existingIdx);
    return;
  }
  if (props.shots.length >= COLABONE_FASHION_MAX_POSE_SHOTS) return;
  emitShots([...props.shots, { kind: "preset", preset_id: preset.id }]);
}

function updateReferenceShot(
  idx: number,
  patch: Partial<Pick<Extract<ColabOneFashionPoseShot, { kind: "reference" }>, "view" | "note" | "borrow_scene_lighting">>,
) {
  if (props.disabled) return;
  const current = props.shots[idx];
  if (!current || current.kind !== "reference") return;
  const next = [...props.shots];
  next[idx] = { ...current, ...patch };
  emitShots(next);
}

function openRefPicker() {
  if (!canAddMoreRefs.value) return;
  fileInputRef.value?.click();
}

function onRefInputChange(ev: Event) {
  const input = ev.target as HTMLInputElement | null;
  const files = input?.files ? Array.from(input.files) : [];
  if (input) {
    input.value = "";
  }
  if (files.length > 0 && !props.disabled) {
    const replaceIdx = editingShotIndex.value;
    exitEditMode();
    emit("add-ref-files", files, replaceIdx);
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
      ev.dataTransfer.dropEffect = canAddMoreRefs.value ? "copy" : "none";
    }
  }
  isDragging.value = Boolean(canAddMoreRefs.value && hasFiles);
}

function onDragLeave(ev: DragEvent) {
  const current = ev.currentTarget as HTMLElement | null;
  const related = ev.relatedTarget as Node | null;
  if (current && related && current.contains(related)) return;
  isDragging.value = false;
}

function onDrop(ev: DragEvent) {
  isDragging.value = false;
  if (!hasFileDragPayload(ev)) return;
  ev.preventDefault();
  if (!canAddMoreRefs.value) return;
  const files = ev.dataTransfer?.files ? Array.from(ev.dataTransfer.files) : [];
  if (files.length > 0) {
    const replaceIdx = editingShotIndex.value;
    exitEditMode();
    emit("add-ref-files", files, replaceIdx);
  }
}

function openRefPreview(slotKey: string) {
  const item = poseRefByKey.value.get(slotKey);
  if (!item?.previewUrl || item.uploading) return;
  previewRefKey.value = slotKey;
}

function closeRefPreview() {
  previewRefKey.value = null;
}

function submitTextPose() {
  if (props.disabled) return;
  const raw = textDraft.value.trim();
  if (!raw) return;

  if (editingShotIndex.value !== null && editingShotIndex.value < props.shots.length) {
    const singleLine = raw.replace(/\r?\n+/g, " ").trim();
    if (!singleLine) return;
    const resolvedView =
      textView.value === "auto"
        ? inferColabOneFashionPoseView(singleLine, "auto")
        : textView.value;
    const next = [...props.shots];
    next[editingShotIndex.value] = {
      kind: "text",
      prompt: singleLine,
      view: resolvedView,
    };
    editingShotIndex.value = null;
    textDraft.value = "";
    textView.value = "auto";
    emitShots(next);
    return;
  }

  const lines = raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  if (!lines.length) return;
  const remaining = COLABONE_FASHION_MAX_POSE_SHOTS - props.shots.length;
  if (remaining <= 0) return;

  const additions: ColabOneFashionPoseShot[] = lines
    .slice(0, remaining)
    .map((line) => ({
      kind: "text",
      prompt: line,
      view:
        textView.value === "auto"
          ? inferColabOneFashionPoseView(line, "auto")
          : textView.value,
    }));
  textDraft.value = "";
  textView.value = "auto";
  emitShots([...props.shots, ...additions]);
}

function applyTextChip(chip: string) {
  if (props.disabled) return;
  textDraft.value = chip;
}
</script>

<template>
  <div
    :id="studioId"
    tabindex="-1"
    class="co-comfy-field co-fashion-pose-studio"
    :class="{ 'is-disabled': disabled }"
    role="group"
    :aria-label="t('comfy.fashionPoseStudioTitle')"
    data-testid="comfy-fashion-pose-studio"
  >
    <input
      ref="fileInputRef"
      type="file"
      accept=".png,.jpg,.jpeg,.webp,image/png,image/jpeg,image/webp"
      :multiple="editingShot === null"
      class="co-pose-hidden-input"
      @change="onRefInputChange"
    />

    <!-- Header + Quick Shot Packs -->
    <div class="co-pose-head">
      <div class="co-pose-head-main">
        <span class="co-pose-title">{{ t("comfy.fashionPoseStudioTitle") }}</span>
        <span
          class="co-pose-count-pill"
          :class="{ 'is-empty': shots.length === 0 }"
          data-testid="comfy-fashion-pose-count"
        >
          {{
            t("comfy.fashionPoseCountBadge", {
              count: shots.length,
              max: COLABONE_FASHION_MAX_POSE_SHOTS,
            })
          }}
        </span>
      </div>

      <div class="co-pose-pack-bar">
        <button
          v-for="pack in COLABONE_FASHION_POSE_PACKS"
          :key="pack.id"
          type="button"
          class="co-pose-pack-btn"
          :class="{ 'is-active': activePackId === pack.id }"
          :disabled="disabled"
          :data-testid="`comfy-fashion-pack-${pack.id}`"
          @click="applyPack(pack.id)"
        >
          {{ t(pack.labelKey) }}
        </button>
        <button
          v-if="shots.length > 0"
          type="button"
          class="co-pose-clear-btn"
          :disabled="disabled"
          data-testid="comfy-fashion-pose-clear"
          @click="clearAllShots"
        >
          {{ t("comfy.fashionPoseClearBtn") }}
        </button>
      </div>
    </div>

    <!-- Unified Per-Shot Queue Strip -->
    <div class="co-pose-queue-wrap">
      <div
        v-if="shots.length === 0"
        class="co-pose-queue-empty"
        role="status"
        data-testid="comfy-fashion-pose-empty"
      >
        {{ t("comfy.fashionPoseQueueEmptyHint") }}
      </div>
      <div v-else class="co-pose-queue-strip" data-testid="comfy-fashion-pose-queue">
        <div
          v-for="(shot, idx) in shots"
          :key="`${shot.kind}-${idx}-${shot.kind === 'preset' ? shot.preset_id : shot.kind === 'reference' ? shot.slot_key : shot.prompt.slice(0, 12)}`"
          class="co-pose-shot-card"
          :class="{
            'is-editing': editingShotIndex === idx,
            'is-hero': idx === 0,
          }"
          role="button"
          tabindex="0"
          :aria-pressed="editingShotIndex === idx"
          :data-testid="`comfy-fashion-shot-${idx}`"
          @click="selectShotForEdit(idx)"
          @keydown.self.enter.prevent="selectShotForEdit(idx)"
          @keydown.self.space.prevent="selectShotForEdit(idx)"
        >
          <div class="co-pose-shot-top">
            <div class="co-pose-shot-meta">
              <span class="co-pose-shot-num" :class="{ 'is-hero': idx === 0 }">
                #{{ String(idx + 1).padStart(2, "0") }}
                <template v-if="idx === 0">{{ t("comfy.fashionPoseHeroShort") }}</template>
              </span>
              <span class="co-pose-shot-source" :class="`is-${shot.kind}`">
                {{ sourceLabel(shot.kind) }}
              </span>
              <span
                v-if="shot.kind === 'reference' && shot.borrow_scene_lighting"
                class="co-pose-shot-light-tag"
                :title="t('comfy.fashionPoseBorrowLightingLabel')"
              >
                <PhSun :size="11" weight="bold" aria-hidden="true" />
              </span>
            </div>
            <div class="co-pose-shot-actions">
              <button
                v-if="idx > 0"
                type="button"
                class="co-pose-mini-btn"
                :disabled="disabled"
                :title="t('comfy.fashionPosePromoteTitle')"
                @click.stop="promoteShot(idx)"
              >
                {{ t("comfy.fashionPromoteBtn") }}
              </button>
              <button
                type="button"
                class="co-pose-mini-btn is-danger"
                :disabled="disabled"
                :title="t('comfy.fashionPoseRemoveShotTitle')"
                :aria-label="`${t('comfy.fashionPoseRemoveShotTitle')} #${idx + 1}`"
                @click.stop="removeShot(idx)"
              >
                <PhX :size="11" weight="bold" aria-hidden="true" />
              </button>
            </div>
          </div>

          <div class="co-pose-shot-body">
            <div v-if="shot.kind === 'preset' && shotSvgSpec(shot)" class="co-pose-shot-visual">
              <svg viewBox="0 0 48 56" class="co-pose-svg co-pose-svg-sm" aria-hidden="true">
                <rect
                  v-for="(r, ri) in shotSvgSpec(shot)!.rects || []"
                  :key="`r-${ri}`"
                  :x="r.x"
                  :y="r.y"
                  :width="r.width"
                  :height="r.height"
                  :rx="r.rx || 0"
                  :stroke-dasharray="r.dashed ? '3 2' : undefined"
                  :stroke-opacity="r.muted ? 0.35 : 1"
                />
                <circle
                  v-for="(c, ci) in shotSvgSpec(shot)!.circles"
                  :key="`c-${ci}`"
                  :cx="c.cx"
                  :cy="c.cy"
                  :r="c.r"
                  :stroke-dasharray="c.dashed ? '2.5 2' : undefined"
                />
                <path
                  v-for="(p, pi) in shotSvgSpec(shot)!.paths"
                  :key="`p-${pi}`"
                  :d="p.d"
                  :stroke-dasharray="p.dashed ? '2.5 2' : undefined"
                  :stroke-opacity="p.muted ? 0.45 : 1"
                />
              </svg>
            </div>
            <div v-else-if="shot.kind === 'reference'" class="co-pose-shot-visual is-photo">
              <img
                v-if="poseRefByKey.get(shot.slot_key)?.previewUrl"
                :src="poseRefByKey.get(shot.slot_key)!.previewUrl"
                :alt="shotDisplayLabel(shot, idx)"
                class="co-pose-shot-photo-img"
                draggable="false"
              />
              <PhImage v-else :size="15" weight="duotone" aria-hidden="true" />
            </div>
            <div v-else class="co-pose-shot-visual is-text">
              <PhTextT :size="15" weight="bold" aria-hidden="true" />
            </div>

            <div class="co-pose-shot-copy">
              <div class="co-pose-shot-label" :title="shotDisplayLabel(shot, idx)">
                {{ shotDisplayLabel(shot, idx) }}
              </div>
              <div class="co-pose-shot-view">
                {{ viewLabel(shotDisplayView(shot)) }}
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Mode Indicator Banner -->
      <div class="co-pose-mode-banner" :class="{ 'is-editing': editingShot !== null }">
        <span class="co-pose-mode-text">
          <template v-if="editingShot !== null && editingShotIndex !== null">
            {{
              t("comfy.fashionPoseEditingBanner", {
                index: String(editingShotIndex + 1).padStart(2, "0"),
                label: shotDisplayLabel(editingShot, editingShotIndex),
              })
            }}
          </template>
          <template v-else>
            {{ t("comfy.fashionPoseAppendBanner") }}
          </template>
        </span>
        <button
          v-if="editingShot !== null"
          type="button"
          class="co-pose-exit-edit-btn"
          @click="exitEditMode"
        >
          {{ t("comfy.fashionPoseExitEditBtn") }}
        </button>
      </div>
    </div>

    <!-- 3-Tab Mode Selector -->
    <div class="co-pose-tabs" role="tablist">
      <button
        type="button"
        role="tab"
        class="co-pose-tab"
        :class="{ 'is-active': activeTab === 'library' }"
        :aria-selected="activeTab === 'library'"
        data-testid="comfy-fashion-pose-tab-library"
        @click="activeTab = 'library'"
      >
        <PhUsersThree :size="14" weight="duotone" aria-hidden="true" />
        <span>{{ t("comfy.fashionPoseTabLibrary", { count: COLABONE_FASHION_POSE_PRESETS.length }) }}</span>
      </button>
      <button
        type="button"
        role="tab"
        class="co-pose-tab"
        :class="{ 'is-active': activeTab === 'reference' }"
        :aria-selected="activeTab === 'reference'"
        data-testid="comfy-fashion-pose-tab-reference"
        @click="activeTab = 'reference'"
      >
        <PhImage :size="14" weight="duotone" aria-hidden="true" />
        <span>{{ t("comfy.fashionPoseTabReference") }}</span>
        <span v-if="referenceShotEntries.length > 0" class="co-pose-tab-badge">
          {{ referenceShotEntries.length }}
        </span>
      </button>
      <button
        type="button"
        role="tab"
        class="co-pose-tab"
        :class="{ 'is-active': activeTab === 'text' }"
        :aria-selected="activeTab === 'text'"
        data-testid="comfy-fashion-pose-tab-text"
        @click="activeTab = 'text'"
      >
        <PhTextT :size="14" weight="duotone" aria-hidden="true" />
        <span>{{ t("comfy.fashionPoseTabText") }}</span>
        <span v-if="textShotEntries.length > 0" class="co-pose-tab-badge">
          {{ textShotEntries.length }}
        </span>
      </button>
    </div>

    <!-- Tab 1: Curated 30-Pose SVG Library -->
    <div v-if="activeTab === 'library'" class="co-pose-panel" data-testid="comfy-fashion-pose-panel-library">
      <div class="co-pose-cats" role="group" :aria-label="t('comfy.fashionPoseCategoryAria')">
        <button
          v-for="cat in COLABONE_FASHION_POSE_CATEGORIES"
          :key="cat"
          type="button"
          class="co-pose-cat-pill"
          :class="{ 'is-active': activeCategory === cat }"
          :data-testid="`comfy-fashion-pose-cat-${cat}`"
          @click="activeCategory = cat"
        >
          {{ categoryLabel(cat) }}
        </button>
      </div>

      <div class="co-pose-preset-grid">
        <button
          v-for="preset in visiblePresets"
          :key="preset.id"
          type="button"
          class="co-pose-preset-card"
          :class="{
            'is-selected': (presetQueueIndices.get(preset.id)?.length || 0) > 0,
          }"
          :disabled="
            disabled ||
            (editingShotIndex === null &&
              !(presetQueueIndices.get(preset.id)?.length || 0) &&
              shots.length >= COLABONE_FASHION_MAX_POSE_SHOTS)
          "
          :data-testid="`comfy-fashion-preset-${preset.id}`"
          @click="onPresetCardClick(preset)"
        >
          <span
            v-if="presetQueueIndices.get(preset.id)?.length"
            class="co-pose-preset-badge"
          >
            #{{ presetQueueIndices.get(preset.id)![0] }}<template v-if="presetQueueIndices.get(preset.id)!.length > 1"> +{{ presetQueueIndices.get(preset.id)!.length - 1 }}</template>
          </span>
          <svg viewBox="0 0 48 56" class="co-pose-svg co-pose-svg-lg" aria-hidden="true">
            <rect
              v-for="(r, ri) in preset.svg.rects || []"
              :key="`r-${ri}`"
              :x="r.x"
              :y="r.y"
              :width="r.width"
              :height="r.height"
              :rx="r.rx || 0"
              :stroke-dasharray="r.dashed ? '3 2' : undefined"
              :stroke-opacity="r.muted ? 0.35 : 1"
            />
            <circle
              v-for="(c, ci) in preset.svg.circles"
              :key="`c-${ci}`"
              :cx="c.cx"
              :cy="c.cy"
              :r="c.r"
              :stroke-dasharray="c.dashed ? '2.5 2' : undefined"
            />
            <path
              v-for="(p, pi) in preset.svg.paths"
              :key="`p-${pi}`"
              :d="p.d"
              :stroke-dasharray="p.dashed ? '2.5 2' : undefined"
              :stroke-opacity="p.muted ? 0.45 : 1"
            />
          </svg>
          <span class="co-pose-preset-name" :title="presetLabel(preset)">
            {{ presetLabel(preset) }}
          </span>
        </button>
      </div>
    </div>

    <!-- Tab 2: Pose Reference Photos -->
    <div
      v-else-if="activeTab === 'reference'"
      class="co-pose-panel"
      :class="{ 'is-dragover': isDragging }"
      data-testid="comfy-fashion-pose-panel-reference"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <button
        type="button"
        class="co-pose-ref-dropzone"
        :disabled="!canAddMoreRefs"
        data-testid="comfy-fashion-pose-ref-upload"
        @click="openRefPicker"
      >
        <span class="co-pose-ref-drop-icon" aria-hidden="true">
          <PhUploadSimple :size="17" weight="duotone" />
        </span>
        <span class="co-pose-ref-drop-copy">
          <strong class="co-pose-ref-drop-title">
            {{
              editingShot !== null && editingShotIndex !== null
                ? t("comfy.fashionPoseRefReplaceTitle", {
                    index: String(editingShotIndex + 1).padStart(2, "0"),
                  })
                : t("comfy.fashionPoseRefUploadTitle")
            }}
          </strong>
          <span class="co-pose-ref-drop-sub">
            {{
              t("comfy.fashionPoseRefUploadDesc", {
                count: poseRefItems.length,
                max: COLABONE_FASHION_MAX_POSE_REFS,
              })
            }}
          </span>
        </span>
        <span class="co-pose-ref-drop-cta" aria-hidden="true">
          <PhPlus :size="14" weight="bold" />
        </span>
      </button>

      <!-- Uploading placeholders -->
      <div v-if="uploadingRefItems.length > 0" class="co-pose-ref-uploading">
        <div
          v-for="up in uploadingRefItems"
          :key="up.key"
          class="co-pose-ref-uploading-row"
          role="status"
        >
          <PhSpinnerGap :size="14" class="co-pose-spin" aria-hidden="true" />
          <span class="co-pose-ref-uploading-name">{{ up.filename }}</span>
        </div>
      </div>

      <!-- Uploaded pose reference shot controls -->
      <div v-if="referenceShotEntries.length > 0" class="co-pose-ref-list">
        <div
          v-for="{ shot, idx } in referenceShotEntries"
          :key="`${shot.slot_key}-${idx}`"
          class="co-pose-ref-row"
          :class="{ 'is-editing': editingShotIndex === idx }"
          :data-testid="`comfy-fashion-pose-ref-row-${shot.slot_key}`"
        >
          <button
            type="button"
            class="co-pose-ref-thumb-btn"
            :disabled="!poseRefByKey.get(shot.slot_key)?.previewUrl"
            :title="t('comfy.fashionPreviewClickHint')"
            @click="openRefPreview(shot.slot_key)"
          >
            <img
              v-if="poseRefByKey.get(shot.slot_key)?.previewUrl"
              :src="poseRefByKey.get(shot.slot_key)!.previewUrl"
              :alt="shotDisplayLabel(shot, idx)"
              class="co-pose-ref-thumb-img"
              draggable="false"
            />
            <PhImage v-else :size="18" weight="duotone" aria-hidden="true" />
            <span
              v-if="poseRefByKey.get(shot.slot_key)?.previewUrl"
              class="co-pose-ref-zoom"
              aria-hidden="true"
            >
              <PhMagnifyingGlassPlus :size="12" weight="bold" />
            </span>
          </button>

          <div class="co-pose-ref-fields">
            <div class="co-pose-ref-row-head">
              <div class="co-pose-ref-name-wrap">
                <span class="co-pose-shot-num">#{{ String(idx + 1).padStart(2, "0") }}</span>
                <span
                  class="co-pose-ref-filename"
                  :title="poseRefByKey.get(shot.slot_key)?.filename || shot.slot_key"
                >
                  {{ poseRefByKey.get(shot.slot_key)?.filename || shot.slot_key }}
                </span>
              </div>
              <button
                type="button"
                class="co-pose-mini-btn is-danger"
                :disabled="disabled"
                :title="t('comfy.fashionRemovePhotoTitle')"
                @click="removeShot(idx)"
              >
                <PhX :size="12" weight="bold" aria-hidden="true" />
              </button>
            </div>

            <div class="co-pose-ref-controls">
              <ASelect
                :value="shot.view"
                size="small"
                class="co-pose-ref-view-select"
                :disabled="disabled"
                :dropdown-match-select-width="false"
                @update:value="
                  (val) =>
                    typeof val === 'string' &&
                    updateReferenceShot(idx, {
                      view: val as ColabOneFashionPoseShotView,
                    })
                "
              >
                <ASelectOption
                  v-for="opt in viewOptions"
                  :key="opt.id"
                  :value="opt.id"
                >
                  {{ opt.label }}
                </ASelectOption>
              </ASelect>

              <input
                type="text"
                class="co-pose-ref-note-input"
                maxlength="500"
                :value="shot.note"
                :disabled="disabled"
                :placeholder="t('comfy.fashionPoseRefNotePlaceholder')"
                @input="
                  (ev) =>
                    updateReferenceShot(idx, {
                      note: (ev.target as HTMLInputElement).value,
                    })
                "
                @blur="
                  (ev) => {
                    const trimmed = (ev.target as HTMLInputElement).value.trim();
                    if (trimmed !== shot.note) {
                      updateReferenceShot(idx, { note: trimmed });
                    }
                  }
                "
              />
            </div>

            <label class="co-pose-ref-light-toggle">
              <ASwitch
                size="small"
                :checked="shot.borrow_scene_lighting"
                :disabled="disabled"
                @update:checked="
                  (checked) =>
                    updateReferenceShot(idx, {
                      borrow_scene_lighting: Boolean(checked),
                    })
                "
              />
              <span class="co-pose-ref-light-label">
                {{ t("comfy.fashionPoseBorrowLightingLabel") }}
              </span>
            </label>
          </div>
        </div>
      </div>

      <p class="co-pose-ref-foot-hint">
        {{ t("comfy.fashionPoseRefIsolationHint") }}
      </p>
    </div>

    <!-- Tab 3: Text Pose Description -->
    <div v-else class="co-pose-panel" data-testid="comfy-fashion-pose-panel-text">
      <div class="co-pose-text-composer">
        <textarea
          v-model="textDraft"
          rows="3"
          maxlength="2000"
          class="co-pose-textarea"
          :disabled="disabled"
          :placeholder="t('comfy.fashionPoseTextPlaceholder')"
          data-testid="comfy-fashion-pose-textarea"
          @keydown.ctrl.enter.prevent="submitTextPose"
          @keydown.meta.enter.prevent="submitTextPose"
        />
        <div class="co-pose-text-side">
          <ASelect
            :value="textView"
            size="small"
            class="co-pose-text-view-select"
            :disabled="disabled"
            @update:value="
              (val) => {
                if (typeof val === 'string') {
                  textView = val as ColabOneFashionPoseShotView;
                }
              }
            "
          >
            <ASelectOption
              v-for="opt in viewOptions"
              :key="opt.id"
              :value="opt.id"
            >
              {{ opt.label }}
            </ASelectOption>
          </ASelect>
          <button
            type="button"
            class="co-pose-text-submit"
            :disabled="disabled || !textDraft.trim() || !canAddMoreShots"
            data-testid="comfy-fashion-pose-text-submit"
            @click="submitTextPose"
          >
            {{
              editingShot !== null && editingShotIndex !== null
                ? t("comfy.fashionPoseTextReplaceBtn", {
                    index: String(editingShotIndex + 1).padStart(2, "0"),
                  })
                : t("comfy.fashionPoseTextAddBtn")
            }}
          </button>
        </div>
      </div>

      <div class="co-pose-chips-row">
        <span class="co-pose-chips-label">{{ t("comfy.fashionPoseChipsLabel") }}</span>
        <button
          v-for="(chip, ci) in textInspirationChips"
          :key="ci"
          type="button"
          class="co-pose-chip-btn"
          :disabled="disabled"
          @click="applyTextChip(chip)"
        >
          {{ chip }}
        </button>
      </div>

      <div v-if="textShotEntries.length > 0" class="co-pose-text-list">
        <div
          v-for="{ shot, idx } in textShotEntries"
          :key="`txt-${idx}`"
          class="co-pose-text-item"
          :class="{ 'is-editing': editingShotIndex === idx }"
        >
          <span class="co-pose-shot-num">#{{ String(idx + 1).padStart(2, "0") }}</span>
          <span class="co-pose-text-item-body" :title="shot.prompt">{{ shot.prompt }}</span>
          <span class="co-pose-text-item-view">{{ viewLabel(shot.view) }}</span>
          <button
            type="button"
            class="co-pose-mini-btn"
            :disabled="disabled"
            @click="selectShotForEdit(idx)"
          >
            {{ t("comfy.fashionPoseEditBtn") }}
          </button>
          <button
            type="button"
            class="co-pose-mini-btn is-danger"
            :disabled="disabled"
            :aria-label="`${t('comfy.fashionPoseRemoveShotTitle')} #${idx + 1}`"
            @click="removeShot(idx)"
          >
            <PhX :size="11" weight="bold" aria-hidden="true" />
          </button>
        </div>
      </div>
    </div>

    <!-- Full-Size Pose Reference Lightbox -->
    <AModal
      :open="!!previewEntry"
      :footer="null"
      :width="680"
      centered
      :z-index="1060"
      :title="t('comfy.fashionPoseRefPreviewTitle')"
      @cancel="closeRefPreview"
    >
      <div v-if="previewEntry" class="co-pose-preview-modal">
        <div class="co-pose-preview-meta">
          <ATag v-if="previewEntry.shotIdx >= 0" color="blue" :bordered="false">
            #{{ String(previewEntry.shotIdx + 1).padStart(2, "0") }}
          </ATag>
          <span class="co-pose-preview-filename" :title="previewEntry.item.filename">
            {{ previewEntry.item.filename }}
          </span>
        </div>
        <div class="co-pose-preview-stage">
          <img
            :src="previewEntry.item.previewUrl"
            :alt="previewEntry.item.filename"
            class="co-pose-preview-full-img"
          />
        </div>
      </div>
    </AModal>
  </div>
</template>

<style scoped>
.co-fashion-pose-studio {
  container-type: inline-size;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  border-radius: var(--co-radius-md, 12px);
  border: 1px solid var(--co-border);
  background: var(--co-subtle);
  outline: none;
}

.co-fashion-pose-studio:focus-visible {
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-pose-hidden-input {
  display: none;
}

.co-pose-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.co-pose-head-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.co-pose-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-pose-count-pill {
  font-size: 11px;
  font-weight: 600;
  padding: 1px 8px;
  border-radius: 9999px;
  font-variant-numeric: tabular-nums;
  color: var(--co-accent-strong);
  background: var(--co-selected);
  border: 1px solid color-mix(in srgb, var(--co-accent) 28%, transparent);
}

.co-pose-count-pill.is-empty {
  color: var(--co-danger, #dc2626);
  background: color-mix(in srgb, var(--co-danger, #dc2626) 10%, transparent);
  border-color: color-mix(in srgb, var(--co-danger, #dc2626) 30%, transparent);
}

.co-pose-pack-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.co-pose-pack-btn {
  font-size: 12px;
  line-height: 1.3;
  padding: 3px 9px;
  border-radius: 7px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  color: var(--co-text);
  cursor: pointer;
  transition:
    border-color 140ms ease,
    background-color 140ms ease,
    color 140ms ease;
}

.co-pose-pack-btn:hover:not(:disabled) {
  border-color: var(--co-accent);
  color: var(--co-accent-strong);
}

.co-pose-pack-btn.is-active {
  border-color: var(--co-accent);
  background: var(--co-selected);
  color: var(--co-accent-strong);
  font-weight: 600;
}

.co-pose-pack-btn:disabled,
.co-pose-clear-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.co-pose-clear-btn {
  font-size: 12px;
  padding: 3px 6px;
  border: none;
  background: transparent;
  color: var(--co-muted);
  cursor: pointer;
}

.co-pose-clear-btn:hover:not(:disabled) {
  color: var(--co-danger, #dc2626);
}

/* Shot Queue Strip */
.co-pose-queue-wrap {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-pose-queue-empty {
  padding: 14px 12px;
  border-radius: var(--co-radius, 10px);
  border: 1.5px dashed var(--co-input-border);
  background: var(--co-surface);
  color: var(--co-muted);
  font-size: 12px;
  text-align: center;
}

.co-pose-queue-strip {
  display: flex;
  align-items: stretch;
  gap: 8px;
  overflow-x: auto;
  padding-bottom: 4px;
}

.co-pose-shot-card {
  width: 146px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: 6px;
  padding: 8px;
  border-radius: var(--co-radius, 10px);
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  cursor: pointer;
  transition:
    border-color 150ms ease,
    box-shadow 150ms ease,
    background-color 150ms ease;
}

.co-pose-shot-card:hover {
  border-color: color-mix(in srgb, var(--co-accent) 60%, var(--co-border));
}

.co-pose-shot-card.is-editing {
  border-color: var(--co-accent);
  background: var(--co-selected);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-pose-shot-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
}

.co-pose-shot-meta {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.co-pose-shot-num {
  font-size: 11px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  color: var(--co-muted);
  white-space: nowrap;
}

.co-pose-shot-num.is-hero {
  color: var(--co-accent-strong);
}

.co-pose-shot-source {
  font-size: 10.5px;
  padding: 0 5px;
  border-radius: 5px;
  background: var(--co-track);
  color: var(--co-muted);
  white-space: nowrap;
}

.co-pose-shot-source.is-reference {
  color: var(--co-accent-strong);
  background: var(--co-selected);
}

.co-pose-shot-light-tag {
  display: inline-flex;
  align-items: center;
  color: var(--co-warning, #d97706);
}

.co-pose-shot-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.co-pose-mini-btn {
  border: none;
  background: transparent;
  color: var(--co-muted);
  font-size: 11px;
  line-height: 1;
  padding: 3px 4px;
  border-radius: 5px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.co-pose-mini-btn:hover:not(:disabled) {
  background: var(--co-track);
  color: var(--co-accent-strong);
}

.co-pose-mini-btn.is-danger:hover:not(:disabled) {
  color: var(--co-danger, #dc2626);
}

.co-pose-shot-body {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}

.co-pose-shot-visual {
  width: 28px;
  height: 34px;
  border-radius: 6px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border);
  color: var(--co-accent);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  overflow: hidden;
}

.co-pose-shot-visual.is-text {
  color: var(--co-text);
}

.co-pose-shot-photo-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.co-pose-shot-copy {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.co-pose-shot-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-pose-shot-view {
  font-size: 11px;
  color: var(--co-muted);
}

.co-pose-mode-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 10px;
  border-radius: 8px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  font-size: 12px;
  color: var(--co-muted);
}

.co-pose-mode-banner.is-editing {
  border-color: color-mix(in srgb, var(--co-accent) 45%, var(--co-border));
  background: var(--co-selected);
  color: var(--co-text);
}

.co-pose-mode-text {
  line-height: 1.45;
}

.co-pose-exit-edit-btn {
  flex-shrink: 0;
  font-size: 12px;
  font-weight: 500;
  padding: 3px 9px;
  border-radius: 6px;
  border: 1px solid var(--co-accent);
  background: var(--co-accent);
  color: #fff;
  cursor: pointer;
}

/* 3 Tabs */
.co-pose-tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  border-bottom: 1px solid var(--co-border);
}

.co-pose-tab {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 7px 11px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-muted);
  border: none;
  border-bottom: 2px solid transparent;
  background: transparent;
  cursor: pointer;
  margin-bottom: -1px;
  transition:
    color 140ms ease,
    border-color 140ms ease;
}

.co-pose-tab:hover {
  color: var(--co-text);
}

.co-pose-tab.is-active {
  color: var(--co-accent-strong);
  border-bottom-color: var(--co-accent);
  font-weight: 600;
}

.co-pose-tab-badge {
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  padding: 0 5px;
  border-radius: 9999px;
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

/* Panels */
.co-pose-panel {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-pose-panel.is-dragover .co-pose-ref-dropzone {
  border-color: var(--co-accent);
  background: var(--co-selected);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-pose-cats {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.co-pose-cat-pill {
  font-size: 12px;
  padding: 3px 9px;
  border-radius: 9999px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  color: var(--co-muted);
  cursor: pointer;
  transition:
    border-color 140ms ease,
    background-color 140ms ease,
    color 140ms ease;
}

.co-pose-cat-pill:hover {
  color: var(--co-text);
  border-color: color-mix(in srgb, var(--co-accent) 50%, var(--co-border));
}

.co-pose-cat-pill.is-active {
  background: var(--co-accent);
  border-color: var(--co-accent);
  color: #fff;
  font-weight: 500;
}

.co-pose-preset-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  max-height: 256px;
  overflow-y: auto;
  padding-right: 2px;
}

@container (max-width: 480px) {
  .co-pose-preset-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@container (max-width: 340px) {
  .co-pose-preset-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

.co-pose-preset-card {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 5px;
  padding: 9px 6px 8px;
  border-radius: var(--co-radius, 10px);
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  color: var(--co-text);
  cursor: pointer;
  transition:
    border-color 140ms ease,
    background-color 140ms ease,
    transform 120ms ease;
}

.co-pose-preset-card:hover:not(:disabled) {
  border-color: color-mix(in srgb, var(--co-accent) 65%, var(--co-border));
}

.co-pose-preset-card.is-selected {
  border-color: var(--co-accent);
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

.co-pose-preset-card:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.co-pose-preset-badge {
  position: absolute;
  top: 5px;
  right: 5px;
  font-size: 10.5px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  padding: 0 5px;
  border-radius: 9999px;
  background: var(--co-accent);
  color: #fff;
}

.co-pose-svg {
  stroke: currentColor;
  fill: none;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
  display: block;
}

.co-pose-svg-sm {
  width: 22px;
  height: 26px;
}

.co-pose-svg-lg {
  width: 38px;
  height: 46px;
}

.co-pose-preset-name {
  font-size: 12px;
  font-weight: 500;
  line-height: 1.25;
  text-align: center;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Tab 2: Pose Reference */
.co-pose-ref-dropzone {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 12px;
  border-radius: var(--co-radius, 10px);
  border: 1.5px dashed var(--co-input-border);
  background: var(--co-surface);
  color: var(--co-text);
  text-align: left;
  cursor: pointer;
  transition:
    border-color 140ms ease,
    background-color 140ms ease;
}

.co-pose-ref-dropzone:hover:not(:disabled) {
  border-color: var(--co-accent);
  background: var(--co-selected);
}

.co-pose-ref-dropzone:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.co-pose-ref-drop-icon {
  width: 34px;
  height: 34px;
  border-radius: 9px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-pose-ref-drop-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-pose-ref-drop-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-pose-ref-drop-sub {
  font-size: 11.5px;
  color: var(--co-muted);
}

.co-pose-ref-drop-cta {
  width: 26px;
  height: 26px;
  border-radius: 7px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border);
  color: var(--co-accent);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-pose-ref-uploading {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-pose-ref-uploading-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-radius: 8px;
  background: var(--co-surface);
  border: 1px solid var(--co-border);
  font-size: 12px;
  color: var(--co-muted);
}

.co-pose-spin {
  animation: co-pose-spin-kf var(--co-spin-duration, 4s) linear infinite;
}

@keyframes co-pose-spin-kf {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

.co-pose-ref-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-pose-ref-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 9px;
  border-radius: var(--co-radius, 10px);
  border: 1px solid var(--co-border);
  background: var(--co-surface);
}

.co-pose-ref-row.is-editing {
  border-color: var(--co-accent);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-pose-ref-thumb-btn {
  position: relative;
  width: 58px;
  height: 76px;
  padding: 0;
  border-radius: 8px;
  border: 1px solid var(--co-border);
  background: var(--co-track);
  color: var(--co-muted);
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  cursor: zoom-in;
}

.co-pose-ref-thumb-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.co-pose-ref-zoom {
  position: absolute;
  right: 4px;
  bottom: 4px;
  width: 20px;
  height: 20px;
  border-radius: 5px;
  background: color-mix(in srgb, var(--co-surface) 88%, transparent);
  color: var(--co-text);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  opacity: 0;
  transition: opacity 140ms ease;
}

.co-pose-ref-thumb-btn:hover .co-pose-ref-zoom {
  opacity: 1;
}

.co-pose-ref-fields {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-pose-ref-row-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}

.co-pose-ref-name-wrap {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.co-pose-ref-filename {
  font-size: 12px;
  font-weight: 500;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-pose-ref-controls {
  display: flex;
  align-items: center;
  gap: 6px;
}

.co-pose-ref-view-select {
  width: 104px;
  flex-shrink: 0;
}

.co-pose-ref-note-input {
  flex: 1;
  min-width: 0;
  height: 24px;
  padding: 2px 8px;
  font-size: 12px;
  border-radius: 6px;
  border: 1px solid var(--co-input-border);
  background: var(--co-subtle);
  color: var(--co-text);
}

.co-pose-ref-note-input:focus {
  outline: none;
  border-color: var(--co-accent);
  background: var(--co-surface);
}

.co-pose-ref-light-toggle {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  cursor: pointer;
  width: fit-content;
}

.co-pose-ref-light-label {
  font-size: 12px;
  color: var(--co-muted);
}

.co-pose-ref-foot-hint {
  margin: 0;
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--co-muted);
}

/* Tab 3: Text Pose */
.co-pose-text-composer {
  display: flex;
  gap: 8px;
  align-items: stretch;
}

.co-pose-textarea {
  flex: 1;
  min-width: 0;
  padding: 8px 10px;
  font-size: 12.5px;
  line-height: 1.5;
  border-radius: var(--co-radius, 10px);
  border: 1px solid var(--co-input-border);
  background: var(--co-surface);
  color: var(--co-text);
  resize: vertical;
  font-family: inherit;
}

.co-pose-textarea:focus {
  outline: none;
  border-color: var(--co-accent);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-pose-text-side {
  width: 112px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-pose-text-view-select {
  width: 100%;
}

.co-pose-text-submit {
  flex: 1;
  min-height: 32px;
  border: none;
  border-radius: 8px;
  background: var(--co-accent);
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  padding: 6px 8px;
  transition: opacity 140ms ease;
}

.co-pose-text-submit:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.co-pose-chips-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.co-pose-chips-label {
  font-size: 11.5px;
  color: var(--co-muted);
}

.co-pose-chip-btn {
  font-size: 11.5px;
  padding: 2px 8px;
  border-radius: 9999px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  color: var(--co-muted);
  cursor: pointer;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-pose-chip-btn:hover:not(:disabled) {
  border-color: var(--co-accent);
  color: var(--co-accent-strong);
}

.co-pose-text-list {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.co-pose-text-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  border-radius: 8px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  font-size: 12px;
}

.co-pose-text-item.is-editing {
  border-color: var(--co-accent);
  background: var(--co-selected);
}

.co-pose-text-item-body {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--co-text);
}

.co-pose-text-item-view {
  font-size: 11px;
  color: var(--co-muted);
  flex-shrink: 0;
}

/* Preview Modal */
.co-pose-preview-modal {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-pose-preview-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.co-pose-preview-filename {
  font-size: 12.5px;
  color: var(--co-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-pose-preview-stage {
  max-height: 70vh;
  overflow: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--co-subtle);
  border-radius: 10px;
  padding: 12px;
}

.co-pose-preview-full-img {
  max-width: 100%;
  max-height: 66vh;
  object-fit: contain;
  border-radius: 6px;
}
</style>
