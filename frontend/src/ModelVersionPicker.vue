<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { t, tb } from "./i18n";
import {
  Alert as AAlert,
  Button as AButton,
  Empty as AEmpty,
  Space as ASpace,
  Tag as ATag,
} from "ant-design-vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import {
  PhArrowRight,
  PhArrowSquareOut,
  PhCaretDown,
  PhCaretUp,
  PhCheck,
  PhDownloadSimple,
  PhFlowArrow,
  PhInfo,
  PhPlay,
  PhPushPin,
  PhSlidersHorizontal,
  PhSparkle,
} from "@phosphor-icons/vue";
import RuntimeParamPresets from "./RuntimeParamPresets.vue";
import { colabOneOpenURL } from "./api";
import {
  describeColabOneRuntimeParams,
  evaluateColabOneTargetDiskFit,
  evaluateColabOneVramFit,
  formatColabOneFileSize,
  formatColabOneGb,
  hasLiveRuntimeGpu,
  planColabOneVersionOptions,
  resolveColabOneGpuTarget,
  type ColabOneFileDiskState,
  type ColabOneGpuTarget,
  type ColabOneGpuTargetId,
  type ColabOneHubFile,
  type ColabOneVersionOption,
} from "./modelCompatibility";
import type { RuntimeStatus } from "./types";

const props = defineProps<{
  repo: string;
  files: ColabOneHubFile[];
  loading: boolean;
  error: string;
  modality?: string;
  comfyOnly?: boolean;
  targetGpu: ColabOneGpuTarget;
  runtime?: RuntimeStatus;
  ready: boolean;
  disabled: boolean;
  installing: boolean;
  selectedFilename: string;
  preferredFilename?: string;
  contextLength: number;
  temperature: number;
  customName?: string;
  allowCustomName?: boolean;
  showSaveButton?: boolean;
  showUseLoaded?: boolean;
  savedTagForFile?: (filename: string) => string;
  fileDiskState?: (filename: string) => ColabOneFileDiskState | undefined;
  downloadDisabledReason?: string;
}>();

const emit = defineEmits<{
  "update:selectedFilename": [string];
  "update:contextLength": [number];
  "update:temperature": [number];
  "update:customName": [string];
  retry: [];
  install: [mode: "load" | "download" | "save"];
  useLoaded: [];
  switchGpu: [ColabOneGpuTargetId];
  goComfyui: [];
}>();

const showAllVersions = ref(false);
const showParams = ref(false);
const userManuallyPicked = ref(false);

const PICKER_SKELETON_CARDS = [
  { titleW: 84, quantW: 62, fitW: 68, subW: "72%" },
  { titleW: 96, quantW: 54, fitW: 72, subW: "64%" },
  { titleW: 76, quantW: 58, fitW: 64, subW: "58%" },
] as const;

const isComfyOnly = computed(
  () =>
    !!props.comfyOnly ||
    props.modality === "image" ||
    props.modality === "video",
);

const savedFilenames = computed(() => {
  const set = new Set<string>();
  if (!props.savedTagForFile) return set;
  for (const f of props.files) {
    if (props.savedTagForFile(f.filename)) set.add(f.filename);
  }
  return set;
});

const fileDiskStates = computed(() => {
  const map = new Map<string, ColabOneFileDiskState>();
  if (!props.fileDiskState) return map;
  for (const f of props.files) {
    const st = props.fileDiskState(f.filename);
    if (st) map.set(f.filename, st);
  }
  return map;
});

const plan = computed(() =>
  planColabOneVersionOptions(
    props.files,
    props.targetGpu,
    props.ready ? props.contextLength : 8192,
    props.selectedFilename,
    savedFilenames.value,
    isComfyOnly.value,
    fileDiskStates.value,
  ),
);

const visibleOptions = computed(() =>
  showAllVersions.value ? plan.value.all : plan.value.curated,
);

const selectedOption = computed<ColabOneVersionOption | undefined>(() =>
  plan.value.all.find((o) => o.file.filename === props.selectedFilename),
);

// Reset local state before auto-selecting when switching repositories
watch(
  () => props.repo,
  () => {
    showAllVersions.value = false;
    showParams.value = false;
    userManuallyPicked.value = false;
  },
);

// Auto-select recommended version on load or GPU change unless user manually picked a still-valid version
watch(
  () => [plan.value, isComfyOnly.value] as const,
  ([nextPlan, comfy]) => {
    if (comfy) {
      if (props.selectedFilename) emit("update:selectedFilename", "");
      return;
    }
    const current = nextPlan.all.find(
      (o) => o.file.filename === props.selectedFilename,
    );
    if (!current || current.disabled || !userManuallyPicked.value) {
      if (current?.disabled) userManuallyPicked.value = false;
      const preferredOpt = props.preferredFilename
        ? nextPlan.all.find(
            (o) => !o.disabled && o.file.filename === props.preferredFilename,
          )
        : undefined;
      const nextName =
        preferredOpt?.file.filename || nextPlan.recommendedFilename || "";
      if (nextName !== props.selectedFilename) {
        emit("update:selectedFilename", nextName);
      }
    }
  },
  { immediate: true },
);

const liveGpuTarget = computed(() =>
  hasLiveRuntimeGpu(props.runtime)
    ? resolveColabOneGpuTarget("CURRENT", props.runtime)
    : null,
);

const activeEvaluationGpu = computed(() =>
  props.ready && liveGpuTarget.value ? liveGpuTarget.value : props.targetGpu,
);

const activeSelectedFit = computed(() => {
  if (!selectedOption.value) return null;
  if (props.ready && liveGpuTarget.value) {
    return evaluateColabOneVramFit(
      selectedOption.value.fit.estimatedVramMib,
      liveGpuTarget.value,
      isComfyOnly.value,
    );
  }
  return selectedOption.value.fit;
});

const hybridNotice = computed<{
  type: "info" | "warning";
  message: string;
} | null>(() => {
  const fit = activeSelectedFit.value;
  const gpu = activeEvaluationGpu.value;
  if (!fit || !fit.requiresHybrid || gpu.vramMib <= 0) return null;
  if (fit.status === "hybrid") {
    return {
      type: "info",
      message:
        fit.overflowGb > 0
          ? t("models.pickerHybridAlert", {
              gpu: gpu.shortLabel,
              overflow: formatColabOneGb(fit.overflowGb),
            })
          : t("models.pickerHybridNearLimitAlert", {
              gpu: gpu.shortLabel,
            }),
    };
  }
  return {
    type: "warning",
    message: t("models.pickerHeavyHybridAlert", {
      gb: formatColabOneGb(fit.estimatedVramGb),
      gpu: gpu.shortLabel,
      minGpu: fit.minGpu.label,
    }),
  };
});

const activeSelectedDiskFit = computed(() => {
  if (!selectedOption.value) return null;
  if (props.ready && liveGpuTarget.value) {
    return evaluateColabOneTargetDiskFit(
      selectedOption.value.file.size,
      liveGpuTarget.value,
      fileDiskStates.value.get(selectedOption.value.file.filename),
    );
  }
  return selectedOption.value.disk;
});

const diskNotice = computed<{
  type: "error" | "warning";
  message: string;
} | null>(() => {
  const disk = activeSelectedDiskFit.value;
  if (!disk || !disk.detail) return null;
  if (disk.status === "exceeds_vm") {
    return { type: "error", message: disk.detail };
  }
  if (disk.status === "insufficient_live" || disk.status === "tight") {
    return { type: "warning", message: disk.detail };
  }
  return null;
});

const paramSummary = computed(() =>
  describeColabOneRuntimeParams(props.contextLength, props.temperature),
);

const effectiveLoadDisabledReason = computed(() => {
  if (!selectedOption.value) return t("models.pickerSelectVersionHint");
  return props.downloadDisabledReason || "";
});

const canDownloadOrLoad = computed(
  () =>
    props.ready &&
    !props.disabled &&
    !props.installing &&
    !!selectedOption.value &&
    !selectedOption.value.disabled,
);

const canSave = computed(
  () =>
    !props.installing &&
    !!selectedOption.value &&
    !selectedOption.value.disabled,
);

function selectOption(opt: ColabOneVersionOption) {
  if (opt.disabled || isComfyOnly.value) return;
  userManuallyPicked.value = true;
  emit("update:selectedFilename", opt.file.filename);
}

function fitTagColor(opt: ColabOneVersionOption): string {
  if (!opt.file.supported) return "default";
  if (opt.fit.status === "comfortable") return "success";
  if (opt.fit.status === "tight") return "warning";
  if (opt.fit.status === "hybrid" || opt.fit.status === "heavy_hybrid") {
    return "default";
  }
  return "blue";
}

function diskTagColor(opt: ColabOneVersionOption): string {
  if (opt.disk.status === "exceeds_vm") return "error";
  if (opt.disk.status === "insufficient_live" || opt.disk.status === "tight") {
    return "warning";
  }
  return "default";
}

function savedTag(filename: string): string {
  return props.savedTagForFile ? props.savedTagForFile(filename) : "";
}
</script>

<template>
  <div class="co-version-picker" :aria-label="t('models.pickerAria')">
    <div
      v-if="loading"
      class="co-vp-skeleton"
      role="status"
      aria-busy="true"
    >
      <span class="co-sr-only">{{ t("models.pickerLoading") }}</span>
      <div class="co-vp-header-bar">
        <ColabOneSkeleton :width="180" :height="13" variant="text" tone="subtle" />
        <ColabOneSkeleton :width="68" :height="13" variant="text" tone="subtle" :delay="60" />
      </div>
      <div class="co-version-grid">
        <div
          v-for="(card, idx) in PICKER_SKELETON_CARDS"
          :key="idx"
          class="co-version-card co-version-card-skeleton"
        >
          <div class="co-vc-top">
            <ColabOneSkeleton
              :width="16"
              :height="16"
              variant="circle"
              tone="subtle"
              :delay="idx * 70"
            />
            <ColabOneSkeleton
              :width="card.titleW"
              :height="14"
              variant="text"
              :delay="idx * 70 + 30"
            />
            <ColabOneSkeleton
              :width="card.quantW"
              :height="18"
              variant="tag"
              :delay="idx * 70 + 60"
            />
            <ColabOneSkeleton
              :width="card.fitW"
              :height="18"
              variant="tag"
              tone="subtle"
              :delay="idx * 70 + 90"
            />
          </div>
          <div class="co-vc-metrics">
            <ColabOneSkeleton
              :width="card.subW"
              :height="12"
              variant="text"
              tone="subtle"
              :delay="idx * 70 + 110"
            />
          </div>
        </div>
      </div>
    </div>

    <AAlert
      v-else-if="error"
      type="error"
      show-icon
      :message="tb(error)"
      role="alert"
    >
      <template #action>
        <AButton size="small" @click="emit('retry')">{{ t("common.retry") }}</AButton>
      </template>
    </AAlert>

    <template v-else-if="isComfyOnly">
      <AAlert
        type="warning"
        show-icon
        :message="
          modality === 'video'
            ? t('models.pickerComfyVideoTitle')
            : t('models.pickerComfyImageTitle')
        "
        :description="t('models.pickerComfyDesc')"
        class="co-vp-comfy-alert"
      />

      <div class="co-vp-comfy-actions">
        <AButton type="primary" size="small" @click="emit('goComfyui')">
          <template #icon><PhFlowArrow :size="14" /></template>
          {{ t("models.pickerGoComfyWorkspace") }}
          <PhArrowRight :size="13" />
        </AButton>
        <AButton
          type="link"
          size="small"
          class="co-inline-link"
          @click="colabOneOpenURL('https://huggingface.co/' + repo)"
        >
          {{ t("models.pickerRepoHome") }}
          <PhArrowSquareOut :size="12" />
        </AButton>
      </div>

      <div v-if="plan.totalCount > 0" class="co-vp-readonly-block">
        <div class="co-vp-header-bar">
          <span class="co-vp-header-meta">
            {{
              t("models.pickerComfyFileCount", {
                count: plan.totalCount,
                gpu: targetGpu.shortLabel,
              })
            }}
          </span>
        </div>
        <div class="co-version-grid">
          <div
            v-for="opt in visibleOptions"
            :key="opt.file.filename"
            class="co-version-card is-readonly"
            :class="{ 'is-hybrid': opt.fit.requiresHybrid }"
          >
            <div class="co-vc-top">
              <strong class="co-vc-title">{{ opt.quant.title }}</strong>
              <ATag color="blue" :bordered="false" class="co-vc-quant">
                {{ opt.quant.code }}
              </ATag>
              <ATag
                :color="fitTagColor(opt)"
                :bordered="false"
                class="co-vc-fit-tag"
              >
                {{ opt.fit.label }}
              </ATag>
              <ATag
                v-if="opt.file.supported && opt.disk.label"
                :color="diskTagColor(opt)"
                :bordered="false"
                class="co-vc-disk-tag"
              >
                {{ opt.disk.label }}
              </ATag>
            </div>
            <div class="co-vc-metrics co-tabular">
              <span
                class="co-vc-disk"
                :class="{
                  'is-disk-danger': opt.disk.status === 'exceeds_vm',
                  'is-disk-warn':
                    opt.disk.status === 'insufficient_live' ||
                    opt.disk.status === 'tight',
                }"
              >
                {{ t("models.pickerDiskLabel") }}
                <strong>{{ formatColabOneFileSize(opt.file.size) }}</strong><template v-if="opt.disk.status === 'cached'">{{ t("models.pickerDiskCachedInline") }}</template><template
                  v-else-if="
                    opt.file.size > 0 &&
                    (targetGpu.diskFreeGb > 0 || targetGpu.isLiveDisk)
                  "
                >{{ " " }}{{
                    targetGpu.isLiveDisk
                      ? t("models.pickerDiskAvailLive", {
                          avail: formatColabOneGb(targetGpu.diskFreeGb),
                        })
                      : t("models.pickerDiskAvailApprox", {
                          avail: formatColabOneGb(targetGpu.diskFreeGb),
                        })
                  }}</template>
              </span>
              <span>·</span>
              <span>
                {{ t("models.pickerEstVramLabel") }} ~{{
                  formatColabOneGb(opt.fit.estimatedVramGb)
                }}<template v-if="targetGpu.vramGb > 0">
                  / {{ targetGpu.vramGb }} GB</template
                >
              </span>
              <span>·</span>
              <span>
                {{
                  opt.file.shards > 1
                    ? t("models.pickerShards", { count: opt.file.shards })
                    : t("models.pickerSingleFile")
                }}
              </span>
            </div>
            <div class="co-vc-file" :title="opt.file.filename">
              {{ opt.file.filename }}
            </div>
          </div>
        </div>
        <AButton
          v-if="plan.hasHidden"
          type="link"
          size="small"
          class="co-vp-expand-btn"
          @click="showAllVersions = !showAllVersions"
        >
          <PhCaretUp v-if="showAllVersions" :size="13" />
          <PhCaretDown v-else :size="13" />
          <span>
            {{
              showAllVersions
                ? t("models.pickerCollapseFiles")
                : t("models.pickerExpandAllFiles", { count: plan.totalCount })
            }}
          </span>
        </AButton>
      </div>
    </template>

    <template v-else-if="plan.totalCount > 0">
      <div class="co-vp-header-bar">
        <div class="co-vp-header-meta">
          <span>{{
            plan.totalCount === 1
              ? t("models.pickerTotalVersionsSingle", { count: plan.totalCount })
              : t("models.pickerTotalVersions", { count: plan.totalCount })
          }}</span>
          <span>·</span>
          <span>
            {{ t("models.pickerEvalBy", { gpu: targetGpu.shortLabel }) }}
          </span>
          <template v-if="targetGpu.vramMib > 0">
            <span>·</span>
            <span
              v-if="plan.fullVramCount > 0"
              class="co-vp-fit-ok"
            >
              {{ t("models.pickerFullVramCount", { count: plan.fullVramCount }) }}
            </span>
            <template v-if="plan.fullVramCount > 0 && plan.hybridCount > 0">
              <span>/</span>
            </template>
            <span
              v-if="plan.hybridCount > 0"
              class="co-vp-fit-hybrid"
            >
              {{ t("models.pickerHybridCount", { count: plan.hybridCount }) }}
            </span>
            <template
              v-if="
                (plan.fullVramCount > 0 || plan.hybridCount > 0) &&
                plan.diskExceedsVmCount > 0
              "
            >
              <span>/</span>
            </template>
            <span
              v-if="plan.diskExceedsVmCount > 0"
              class="co-vp-fit-disk-warn"
            >
              {{
                t("models.pickerDiskExceedsCount", {
                  count: plan.diskExceedsVmCount,
                })
              }}
            </span>
          </template>
        </div>

        <AButton
          type="link"
          size="small"
          class="co-inline-link"
          :aria-label="t('models.pickerRepoHomeAria', { repo })"
          @click="colabOneOpenURL('https://huggingface.co/' + repo)"
        >
          {{ t("models.pickerRepoHomeShort") }}
          <PhArrowSquareOut :size="12" />
        </AButton>
      </div>

      <!-- Warning banner when all versions exceed target GPU disk capacity -->
      <div
        v-if="
          plan.diskFittingCount === 0 &&
          plan.diskExceedsVmCount > 0 &&
          (targetGpu.diskFreeGb > 0 || targetGpu.isLiveDisk)
        "
        class="co-vp-hybrid-banner co-vp-disk-banner"
        role="status"
      >
        <div class="co-vp-hybrid-banner-text">
          <PhInfo :size="15" class="co-vp-info-icon is-warn" />
          <span>
            {{
              t("models.pickerAllDiskOverflowNotice", {
                gpu: targetGpu.label,
                avail: formatColabOneGb(targetGpu.diskFreeGb),
              })
            }}
          </span>
        </div>
        <ASpace v-if="plan.suggestedDiskGpuId" :size="6" wrap>
          <AButton
            size="small"
            @click="emit('switchGpu', plan.suggestedDiskGpuId)"
          >
            {{
              t("models.pickerSwitchLargerDisk", {
                gpu: plan.suggestedDiskGpuLabel,
                avail: formatColabOneGb(plan.suggestedDiskAvailGb),
              })
            }}
          </AButton>
        </ASpace>
      </div>

      <!-- Helpful tip when all versions exceed pure VRAM but are runnable via hybrid GPU+RAM offload -->
      <div
        v-else-if="
          plan.fullVramCount === 0 &&
          plan.hybridCount > 0 &&
          targetGpu.vramMib > 0
        "
        class="co-vp-hybrid-banner"
        role="status"
      >
        <div class="co-vp-hybrid-banner-text">
          <PhInfo :size="15" class="co-vp-info-icon" />
          <span>
            {{ t("models.pickerAllHybridNotice", { gpu: targetGpu.label }) }}
          </span>
        </div>
        <ASpace v-if="plan.suggestedGpuId" :size="6" wrap>
          <AButton
            size="small"
            @click="emit('switchGpu', plan.suggestedGpuId)"
          >
            {{
              t("models.pickerSwitchFullVram", { gpu: plan.suggestedGpuLabel })
            }}
          </AButton>
        </ASpace>
      </div>

      <!-- Beginner-Friendly Curated / Full Version Cards -->
      <div
        class="co-version-grid co-sk-reveal"
        role="radiogroup"
        :aria-label="t('models.pickerQuantListAria')"
      >
        <button
          v-for="opt in visibleOptions"
          :key="opt.file.filename"
          type="button"
          role="radio"
          class="co-version-card"
          :class="{
            'is-selected': selectedFilename === opt.file.filename,
            'is-recommended': opt.recommended,
            'is-tight': opt.fit.status === 'tight' && !opt.disabled,
            'is-hybrid':
              opt.fit.status === 'hybrid' &&
              !opt.disabled &&
              selectedFilename !== opt.file.filename,
            'is-heavy-hybrid':
              opt.fit.status === 'heavy_hybrid' &&
              !opt.disabled &&
              selectedFilename !== opt.file.filename,
            'is-disk-overflow':
              opt.disk.exceedsVm &&
              !opt.disabled &&
              selectedFilename !== opt.file.filename,
            'is-disabled': opt.disabled,
          }"
          :disabled="opt.disabled"
          :aria-checked="selectedFilename === opt.file.filename"
          :aria-disabled="opt.disabled"
          @click="selectOption(opt)"
        >
          <div class="co-vc-top">
            <span
              class="co-vc-radio"
              :class="{
                checked: selectedFilename === opt.file.filename,
                disabled: opt.disabled,
              }"
              aria-hidden="true"
            >
              <PhCheck
                v-if="selectedFilename === opt.file.filename"
                :size="10"
                weight="bold"
              />
            </span>

            <strong class="co-vc-title">{{ opt.quant.title }}</strong>

            <ATag color="blue" :bordered="false" class="co-vc-quant">
              {{ opt.quant.code }}
            </ATag>

            <ATag
              v-if="opt.recommended"
              color="processing"
              :bordered="false"
              class="co-vc-rec-tag"
            >
              <PhSparkle :size="10" weight="fill" />
              <span>{{ t("common.recommended") }}</span>
            </ATag>

            <ATag
              :color="fitTagColor(opt)"
              :bordered="false"
              class="co-vc-fit-tag"
            >
              {{
                opt.file.supported
                  ? opt.fit.label
                  : t("models.pickerUnavailable")
              }}
            </ATag>

            <ATag
              v-if="opt.file.supported && opt.disk.label"
              :color="diskTagColor(opt)"
              :bordered="false"
              class="co-vc-disk-tag"
            >
              {{ opt.disk.label }}
            </ATag>

            <ATag
              v-if="savedTag(opt.file.filename)"
              color="cyan"
              :bordered="false"
              class="co-vc-saved-tag"
            >
              {{ savedTag(opt.file.filename) }}
            </ATag>
          </div>

          <div class="co-vc-metrics co-tabular">
            <span
              class="co-vc-size co-vc-disk"
              :class="{
                'is-disk-danger': opt.disk.status === 'exceeds_vm',
                'is-disk-warn':
                  opt.disk.status === 'insufficient_live' ||
                  opt.disk.status === 'tight',
              }"
            >
              {{ t("models.pickerDiskLabel") }}
              <strong>{{ formatColabOneFileSize(opt.file.size) }}</strong><template v-if="opt.disk.status === 'cached'">{{ t("models.pickerDiskCachedInline") }}</template><template
                v-else-if="
                  opt.file.size > 0 &&
                  (targetGpu.diskFreeGb > 0 || targetGpu.isLiveDisk)
                "
              >{{ " " }}{{
                  targetGpu.isLiveDisk
                    ? t("models.pickerDiskAvailLive", {
                        avail: formatColabOneGb(targetGpu.diskFreeGb),
                      })
                    : t("models.pickerDiskAvailApprox", {
                        avail: formatColabOneGb(targetGpu.diskFreeGb),
                      })
                }}</template>
            </span>
            <span>·</span>
            <span class="co-vc-vram">
              {{ t("models.pickerEstVramLabel") }}
              <strong>~{{ formatColabOneGb(opt.fit.estimatedVramGb) }}</strong>
              <template v-if="targetGpu.vramGb > 0">
                / {{ targetGpu.vramGb }} GB
                <template v-if="opt.fit.requiresHybrid">
                  {{
                    opt.fit.overflowGb > 0
                      ? t("models.pickerOverflowToRamInline", {
                          overflow: formatColabOneGb(opt.fit.overflowGb),
                        })
                      : t("models.pickerNearLimitToRamInline")
                  }}
                </template>
              </template>
            </span>
            <span>·</span>
            <span>
              {{
                opt.file.shards > 1
                  ? t("models.pickerShardsTotal", { count: opt.file.shards })
                  : t("models.pickerSingleFile")
              }}
            </span>
          </div>

          <div class="co-vc-bottom">
            <span
              class="co-vc-desc"
              :class="{
                'is-danger': opt.disk.status === 'exceeds_vm' && !opt.disabled,
                'is-warn':
                  (opt.disk.status === 'insufficient_live' ||
                    opt.fit.status === 'tight' ||
                    opt.fit.status === 'heavy_hybrid') &&
                  !opt.disabled &&
                  opt.disk.status !== 'exceeds_vm',
                'is-hybrid-note':
                  opt.fit.status === 'hybrid' &&
                  !opt.disk.insufficient &&
                  !opt.disabled,
                'is-disabled-reason': opt.disabled,
              }"
            >
              {{
                opt.disabled
                  ? tb(opt.disabledReason)
                  : opt.disk.insufficient
                    ? opt.disk.detail
                    : opt.fit.requiresHybrid
                      ? opt.fit.detail
                      : opt.fit.status === "tight"
                        ? t("models.pickerTightSuffix", { desc: opt.quant.desc })
                        : opt.quant.desc
              }}
            </span>
            <span class="co-vc-file" :title="opt.file.filename">
              {{ opt.file.filename }}
            </span>
          </div>
        </button>
      </div>

      <!-- Expand/Collapse All Versions Toggle -->
      <div v-if="plan.hasHidden" class="co-vp-expand-row">
        <AButton
          type="link"
          size="small"
          class="co-vp-expand-btn"
          @click="showAllVersions = !showAllVersions"
        >
          <PhCaretUp v-if="showAllVersions" :size="13" />
          <PhCaretDown v-else :size="13" />
          <span>
            {{
              showAllVersions
                ? t("models.pickerCollapseCurated", {
                    count: plan.curated.length,
                  })
                : plan.hiddenHybridCount
                  ? t("models.pickerExpandVersionsHybrid", {
                      count: plan.totalCount,
                      hidden: plan.hiddenHybridCount,
                    })
                  : t("models.pickerExpandVersions", {
                      count: plan.totalCount,
                    })
            }}
          </span>
        </AButton>
      </div>

      <!-- Selected Summary + Load-Time Parameter Preset Toggle (only shown when GPU ready) -->
      <div v-if="selectedOption" class="co-hub-download-summary co-vp-summary">
        <div class="co-summary-meta">
          <ATag color="blue" :bordered="false">
            {{ selectedOption.quant.code }}
          </ATag>
          <span
            class="co-tabular"
            :class="{
              'is-disk-danger': activeSelectedDiskFit?.status === 'exceeds_vm',
              'is-disk-warn':
                activeSelectedDiskFit?.status === 'insufficient_live' ||
                activeSelectedDiskFit?.status === 'tight',
            }"
          >
            {{ t("models.pickerDiskLabel") }}
            <strong>{{
              formatColabOneFileSize(selectedOption.file.size)
            }}</strong><template v-if="activeSelectedDiskFit?.status === 'cached'">{{ t("models.pickerDiskCachedInline") }}</template><template
              v-else-if="
                selectedOption.file.size > 0 &&
                (activeEvaluationGpu.diskFreeGb > 0 ||
                  activeEvaluationGpu.isLiveDisk)
              "
            >{{ " " }}{{
                activeEvaluationGpu.isLiveDisk
                  ? t("models.pickerDiskAvailLive", {
                      avail: formatColabOneGb(activeEvaluationGpu.diskFreeGb),
                    })
                  : t("models.pickerDiskAvailApprox", {
                      avail: formatColabOneGb(activeEvaluationGpu.diskFreeGb),
                    })
              }}</template>
          </span>
          <span>·</span>
          <span>
            {{
              selectedOption.file.shards > 1
                ? t("models.pickerFilesTotal", {
                    count: selectedOption.file.shards,
                  })
                : t("models.pickerSingleFileTotal")
            }}
          </span>
          <span>·</span>
          <span class="co-tabular">
            {{
              t("models.estimatedVram", {
                gb: formatColabOneGb(selectedOption.fit.estimatedVramGb),
              })
            }}
          </span>
          <ATag
            v-if="savedTag(selectedOption.file.filename)"
            color="cyan"
            :bordered="false"
          >
            {{ savedTag(selectedOption.file.filename) }}
          </ATag>
        </div>

        <AButton
          v-if="ready"
          type="link"
          size="small"
          class="co-inline-link co-advanced-toggle"
          @click="showParams = !showParams"
        >
          <PhSlidersHorizontal :size="13" />
          <span>
            {{
              showParams
                ? t("models.pickerCollapseParams")
                : t("models.pickerLoadPreset", {
                    title: paramSummary.presetTitle,
                  })
            }}
          </span>
        </AButton>
      </div>

      <!-- Load-Time Runtime Parameters (ONLY when GPU is ready, collapsed to recommended preset by default) -->
      <div
        v-if="ready && showParams && selectedOption"
        class="co-vp-param-drawer"
      >
        <RuntimeParamPresets
          :context-length="contextLength"
          :temperature="temperature"
          :model-bytes="selectedOption.file.size"
          :target-gpu="liveGpuTarget || targetGpu"
          :custom-name="customName"
          :custom-name-placeholder="repo.split('/').pop()"
          :allow-custom-name="allowCustomName"
          @update:context-length="(v) => emit('update:contextLength', v)"
          @update:temperature="(v) => emit('update:temperature', v)"
          @update:custom-name="(v) => emit('update:customName', v)"
        />
      </div>

      <!-- Clear hybrid-offload notice (never blocks selection or loading!) -->
      <AAlert
        v-if="hybridNotice"
        :type="hybridNotice.type"
        show-icon
        :message="hybridNotice.message"
        class="co-vp-live-warn"
      />

      <!-- Remote disk capacity notice -->
      <AAlert
        v-if="diskNotice"
        :type="diskNotice.type"
        show-icon
        :message="diskNotice.message"
        class="co-vp-disk-warn"
      />

      <!-- Action Buttons -->
      <ASpace wrap :size="8" class="co-hub-install-actions">
        <template v-if="ready">
          <ColabOneTooltip :title="effectiveLoadDisabledReason">
            <AButton
              type="primary"
              size="small"
              :loading="installing"
              :disabled="!canDownloadOrLoad"
              @click="emit('install', 'load')"
            >
              <template #icon><PhPlay :size="13" weight="fill" /></template>
              {{
                installing
                  ? t("common.submitting")
                  : t("models.downloadAndLoad")
              }}
            </AButton>
          </ColabOneTooltip>
          <ColabOneTooltip :title="effectiveLoadDisabledReason">
            <AButton
              size="small"
              :disabled="!canDownloadOrLoad"
              @click="emit('install', 'download')"
            >
              <template #icon><PhDownloadSimple :size="14" /></template>
              {{ t("models.downloadOnly") }}
            </AButton>
          </ColabOneTooltip>
          <AButton
            v-if="showSaveButton"
            size="small"
            :disabled="!canSave"
            @click="emit('install', 'save')"
          >
            <template #icon><PhPushPin :size="14" /></template>
            {{ t("models.addToMine") }}
          </AButton>
          <AButton
            v-if="showUseLoaded"
            type="primary"
            ghost
            size="small"
            @click="emit('useLoaded')"
          >
            <template #icon><PhCheck :size="13" /></template>
            {{ t("models.useThisModel") }}
          </AButton>
        </template>
        <template v-else>
          <template v-if="showSaveButton">
            <AButton
              type="primary"
              size="small"
              :loading="installing"
              :disabled="!canSave"
              @click="emit('install', 'save')"
            >
              <template #icon><PhPushPin :size="14" /></template>
              {{ t("models.addToMine") }}
            </AButton>
          </template>
          <template v-else>
            <ColabOneTooltip
              :title="
                effectiveLoadDisabledReason || t('models.disabledStartGpu')
              "
            >
              <AButton
                type="primary"
                size="small"
                :loading="installing"
                :disabled="true"
                @click="emit('install', 'load')"
              >
                <template #icon><PhPlay :size="13" weight="fill" /></template>
                {{ t("models.downloadAndLoad") }}
              </AButton>
            </ColabOneTooltip>
          </template>
        </template>
      </ASpace>
    </template>

    <AEmpty
      v-else
      :description="t('models.pickerNoGguf')"
      class="co-picker-empty"
    />
  </div>
</template>

<style scoped>
.co-version-picker {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-vp-skeleton {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-version-card-skeleton {
  cursor: default;
  pointer-events: none;
}

.co-vp-header-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.co-vp-header-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
  font-size: 12px;
  color: var(--co-muted);
}

.co-vp-header-meta strong {
  color: var(--co-text);
  font-weight: 600;
}

.co-vp-fit-ok {
  color: var(--co-success-text);
  font-weight: 500;
}

.co-vp-fit-hybrid {
  color: var(--co-secondary-text);
  font-weight: 500;
}

.co-vp-fit-disk-warn {
  color: var(--co-warning-text);
  font-weight: 500;
}

.co-vp-disk-banner {
  border-color: var(--co-warning-border, var(--co-border));
}

.co-vp-info-icon.is-warn {
  color: var(--co-warning-text);
}

.co-version-card.is-disk-overflow {
  background: var(--co-subtle);
  border-style: dashed;
  opacity: 0.7;
}

.co-vc-disk.is-disk-warn,
.co-vc-disk.is-disk-warn strong,
.co-summary-meta .is-disk-warn,
.co-summary-meta .is-disk-warn strong {
  color: var(--co-warning-text);
}

.co-vc-disk.is-disk-danger,
.co-vc-disk.is-disk-danger strong,
.co-summary-meta .is-disk-danger,
.co-summary-meta .is-disk-danger strong,
.co-vc-desc.is-danger {
  color: var(--co-danger, #cf1322);
}

.co-vc-disk-tag {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 1.4;
  padding: 0 6px;
}

.co-vp-hybrid-banner {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 8px;
  border: 1px solid var(--co-border);
  background: var(--co-subtle);
  font-size: 12px;
  color: var(--co-text);
}

.co-vp-hybrid-banner-text {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  line-height: 1.45;
}

.co-vp-info-icon {
  color: var(--co-accent);
  flex-shrink: 0;
  margin-top: 2px;
}

.co-version-grid {
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.co-version-card {
  appearance: none;
  width: 100%;
  text-align: left;
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  color: var(--co-text);
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 5px;
  transition:
    border-color 0.14s ease,
    background-color 0.14s ease,
    box-shadow 0.14s ease,
    opacity 0.14s ease;
}

.co-version-card:hover:not(:disabled):not(.is-readonly):not(.is-selected) {
  border-color: var(--co-border);
  background: var(--co-soft-hover);
  opacity: 1;
}

.co-version-card.is-hybrid {
  background: var(--co-subtle);
  border-style: dashed;
  opacity: 0.88;
}

.co-version-card.is-heavy-hybrid {
  background: var(--co-subtle);
  border-style: dashed;
  opacity: 0.74;
}

.co-version-card.is-selected {
  border-color: var(--co-accent);
  border-style: solid;
  background: var(--co-selected);
  box-shadow: 0 0 0 1px var(--co-focus-ring);
  opacity: 1;
}

.co-version-card.is-disabled {
  opacity: 0.5;
  background: var(--co-subtle);
  border-style: dashed;
  cursor: not-allowed;
}

.co-version-card.is-readonly {
  cursor: default;
  background: var(--co-subtle);
}

.co-vc-top {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.co-vc-radio {
  width: 16px;
  height: 16px;
  border-radius: 9999px;
  border: 1.5px solid var(--co-input-border);
  background: var(--co-surface);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: var(--co-on-accent);
}

.co-vc-radio.checked {
  border-color: var(--co-accent);
  background: var(--co-accent);
}

.co-vc-radio.disabled {
  border-color: var(--co-border);
  background: var(--co-track);
}

.co-vc-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-vc-quant,
.co-vc-rec-tag,
.co-vc-fit-tag,
.co-vc-saved-tag {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 1.4;
  padding: 0 6px;
}

.co-vc-rec-tag {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-weight: 600;
}

.co-vc-metrics {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
  font-size: 12px;
  color: var(--co-secondary-text);
  padding-left: 22px;
}

.co-version-card.is-readonly .co-vc-metrics,
.co-version-card.is-readonly .co-vc-file {
  padding-left: 0;
}

.co-vc-metrics strong {
  color: var(--co-text);
  font-weight: 600;
}

.co-vc-bottom {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-left: 22px;
  min-width: 0;
}

.co-vc-desc {
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--co-muted);
}

.co-vc-desc.is-warn {
  color: var(--co-warning-text);
}

.co-vc-desc.is-hybrid-note {
  color: var(--co-secondary-text);
}

.co-vc-desc.is-disabled-reason {
  color: var(--co-muted);
}

.co-vc-file {
  font-size: 11px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  color: var(--co-muted);
  opacity: 0.78;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.co-vp-expand-row {
  display: flex;
  justify-content: flex-start;
  margin-top: -2px;
}

.co-vp-expand-btn {
  padding: 0 4px !important;
  height: auto !important;
  font-size: 12px !important;
  display: inline-flex !important;
  align-items: center;
  gap: 4px;
}

.co-vp-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 12px;
  color: var(--co-secondary-text);
  padding-top: 2px;
}

.co-summary-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.co-summary-meta strong {
  color: var(--co-text);
  font-weight: 600;
}

.co-advanced-toggle {
  font-size: 12px !important;
  color: var(--co-accent-strong);
  display: inline-flex !important;
  align-items: center;
  gap: 4px;
}

.co-vp-param-drawer {
  padding: 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-vp-live-warn,
.co-vp-comfy-alert {
  border-radius: 8px;
}

.co-vp-comfy-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.co-vp-readonly-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-hub-install-actions {
  padding-top: 2px;
}

.co-picker-empty {
  margin: 8px 0;
}
</style>
