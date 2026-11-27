<script setup lang="ts">
import { computed } from "vue";
import { t } from "./i18n";
import {
  Button as AButton,
  Input as AInput,
  Slider as ASlider,
  Space as ASpace,
  Tag as ATag,
} from "ant-design-vue";
import { PhCheck, PhInfo, PhSparkle } from "@phosphor-icons/vue";
import {
  COLABONE_CONTEXT_OPTIONS,
  COLABONE_PARAM_PRESETS,
  describeColabOneRuntimeParams,
  estimateColabOneKvCacheMib,
  estimateColabOneVramMib,
  formatColabOneFileSize,
  formatColabOneGb,
  type ColabOneGpuTarget,
  type ColabOneParamPreset,
} from "./modelCompatibility";

const props = defineProps<{
  contextLength: number;
  temperature: number;
  modelBytes?: number;
  targetGpu?: ColabOneGpuTarget | null;
  customName?: string;
  customNamePlaceholder?: string;
  allowCustomName?: boolean;
}>();

const emit = defineEmits<{
  "update:contextLength": [number];
  "update:temperature": [number];
  "update:customName": [string];
}>();

const paramInfo = computed(() =>
  describeColabOneRuntimeParams(props.contextLength, props.temperature),
);

const activePresetId = computed(() => paramInfo.value.preset?.id || "");

const extraVramByCtx = computed(() => {
  const bytes = props.modelBytes || 0;
  if (bytes <= 0) return new Map<number, number>();
  const baseKv = estimateColabOneKvCacheMib(bytes, 8192);
  const map = new Map<number, number>();
  for (const opt of COLABONE_CONTEXT_OPTIONS) {
    const deltaMib = Math.max(
      0,
      estimateColabOneKvCacheMib(bytes, opt.value) - baseKv,
    );
    map.set(opt.value, deltaMib / 1024);
  }
  return map;
});

const estimatedTotalGb = computed(() => {
  if (!props.modelBytes || props.modelBytes <= 0) return 0;
  return estimateColabOneVramMib(props.modelBytes, props.contextLength) / 1024;
});

function applyPreset(preset: ColabOneParamPreset) {
  emit("update:contextLength", preset.context_length);
  emit("update:temperature", preset.temperature);
}

function setContext(value: number) {
  if (Number.isFinite(value) && value > 0) {
    emit("update:contextLength", value);
  }
}

function setTemp(raw: unknown) {
  const num = Array.isArray(raw) ? Number(raw[0]) : Number(raw);
  if (Number.isFinite(num)) {
    emit("update:temperature", Math.min(2, Math.max(0, Math.round(num * 10) / 10)));
  }
}
</script>

<template>
  <div class="co-param-presets-panel" :aria-label="t('models.paramPanelAria')">
    <div v-if="allowCustomName" class="co-param-name-row">
      <label class="co-param-field-label">{{ t("models.paramDisplayName") }}</label>
      <AInput
        :value="customName || ''"
        size="small"
        :placeholder="customNamePlaceholder || t('models.paramDisplayNamePlaceholder')"
        :aria-label="t('models.paramDisplayNameAria')"
        allow-clear
        @update:value="(v) => emit('update:customName', String(v || ''))"
      />
    </div>

    <div class="co-param-section">
      <div class="co-param-section-head">
        <span class="co-param-field-label">{{ t("models.paramQuickPresets") }}</span>
        <span class="co-param-sub-hint">{{ t("models.paramQuickHint") }}</span>
      </div>

      <div class="co-preset-grid" role="group" :aria-label="t('models.paramPresetGroupAria')">
        <button
          v-for="preset in COLABONE_PARAM_PRESETS"
          :key="preset.id"
          type="button"
          class="co-preset-card"
          :class="{ active: activePresetId === preset.id }"
          :aria-pressed="activePresetId === preset.id"
          @click="applyPreset(preset)"
        >
          <div class="co-preset-card-top">
            <span class="co-preset-name">{{ preset.label }}</span>
            <ATag
              v-if="preset.badge"
              color="blue"
              :bordered="false"
              class="co-preset-badge"
            >
              <PhSparkle :size="10" weight="fill" />
              <span>{{ preset.badge }}</span>
            </ATag>
            <PhCheck
              v-if="activePresetId === preset.id"
              :size="13"
              weight="bold"
              class="co-preset-check"
            />
          </div>
          <div class="co-preset-specs co-tabular">
            {{
              t("models.paramContextTempSummary", {
                ctx: Math.round(preset.context_length / 1024),
                temp: preset.temperature.toFixed(1),
              })
            }}
          </div>
          <div class="co-preset-summary">{{ preset.summary }}</div>
        </button>
      </div>
    </div>

    <div class="co-param-section">
      <div class="co-param-row-head">
        <span class="co-param-field-label">{{ t("models.paramContextLength") }}</span>
        <span class="co-param-live-note">{{ paramInfo.contextEffect }}</span>
      </div>
      <ASpace :size="6" wrap>
        <AButton
          v-for="opt in COLABONE_CONTEXT_OPTIONS"
          :key="opt.value"
          size="small"
          :type="contextLength === opt.value ? 'primary' : 'default'"
          :aria-pressed="contextLength === opt.value"
          @click="setContext(opt.value)"
        >
          {{ opt.label }}
        </AButton>
      </ASpace>
      <div
        v-if="extraVramByCtx.get(contextLength)"
        class="co-param-vram-delta co-tabular"
      >
        {{
          t("models.paramExtraVram", {
            gb: formatColabOneGb(extraVramByCtx.get(contextLength)!),
          })
        }}
      </div>
    </div>

    <div class="co-param-section">
      <div class="co-param-row-head">
        <span class="co-param-field-label">{{ t("models.paramTemperature") }}</span>
        <strong class="co-tabular co-param-temp-val">{{
          temperature.toFixed(1)
        }}</strong>
      </div>
      <ASlider
        :value="temperature"
        :min="0"
        :max="2"
        :step="0.1"
        @update:value="setTemp"
      />
      <div class="co-temp-scale">
        <span class="co-temp-tick-low">{{ t("models.paramTempLow") }}</span>
        <span class="co-temp-tick-mid">{{ t("models.paramTempMid") }}</span>
        <span class="co-temp-tick-high">{{ t("models.paramTempHigh") }}</span>
      </div>
    </div>

    <div class="co-param-effect-box" role="status">
      <PhInfo :size="14" class="co-param-effect-icon" />
      <div class="co-param-effect-text">
        <span>
          <strong>{{ t("models.paramCurrentEffect") }}</strong>{{ paramInfo.combinedEffect }}
        </span>
        <span v-if="estimatedTotalGb > 0" class="co-param-effect-vram co-tabular">
          {{ t("models.paramEstTotalVram", { gb: formatColabOneGb(estimatedTotalGb) }) }}<template v-if="targetGpu && targetGpu.vramGb > 0">
            / {{ targetGpu.vramGb }} GB ({{ targetGpu.shortLabel }})</template
          >
          <template v-if="modelBytes && modelBytes > 0">
            {{ " " }}{{ t("models.paramEstDisk", { gb: formatColabOneFileSize(modelBytes) }) }}<template
              v-if="targetGpu && (targetGpu.diskFreeGb > 0 || targetGpu.isLiveDisk)"
            >
              {{ " " }}{{
                targetGpu.isLiveDisk
                  ? t("models.pickerDiskAvailLive", {
                      avail: formatColabOneGb(targetGpu.diskFreeGb),
                    })
                  : t("models.pickerDiskAvailApprox", {
                      avail: formatColabOneGb(targetGpu.diskFreeGb),
                    })
              }}
            </template>
          </template>
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.co-param-presets-panel {
  container-type: inline-size;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-param-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.co-param-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-param-section-head,
.co-param-row-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.co-param-field-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--co-text);
  white-space: nowrap;
}

.co-param-sub-hint,
.co-param-live-note {
  font-size: 11px;
  color: var(--co-muted);
}

.co-preset-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

.co-preset-card {
  appearance: none;
  text-align: left;
  padding: 9px 10px;
  border-radius: 8px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  color: var(--co-text);
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
  max-width: 100%;
  box-sizing: border-box;
  transition:
    border-color 0.14s ease,
    background-color 0.14s ease,
    box-shadow 0.14s ease;
}

.co-preset-card:hover {
  border-color: var(--co-border);
  background: var(--co-soft-hover);
}

.co-preset-card.active {
  border-color: var(--co-accent);
  background: var(--co-selected);
}

.co-preset-card.active :deep(.ant-tag-blue) {
  background: var(--co-surface);
  border-color: rgba(59, 130, 246, 0.28);
}

:root[data-theme="dark"] .co-preset-card.active :deep(.ant-tag-blue) {
  background: rgba(59, 130, 246, 0.24);
  border-color: rgba(96, 165, 250, 0.42);
}

.co-preset-card-top {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
}

.co-preset-name {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-preset-badge {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  font-size: 10.5px;
  line-height: 1.3;
  padding: 0 5px;
  margin-inline-end: 0;
  white-space: nowrap;
  flex-shrink: 0;
}

.co-preset-check {
  margin-left: auto;
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-preset-specs {
  font-size: 11px;
  font-weight: 500;
  color: var(--co-accent-strong);
}

.co-preset-summary {
  font-size: 11px;
  line-height: 1.4;
  color: var(--co-muted);
}

.co-param-vram-delta {
  font-size: 11px;
  color: var(--co-warning-text);
}

.co-param-temp-val {
  font-size: 12px;
  color: var(--co-text);
}

.co-temp-scale {
  position: relative;
  height: 16px;
  font-size: 10.5px;
  line-height: 16px;
  color: var(--co-muted);
  margin-top: -4px;
}

.co-temp-tick-low {
  position: absolute;
  left: 0;
  white-space: nowrap;
}

.co-temp-tick-mid {
  position: absolute;
  left: 35%;
  transform: translateX(-50%);
  white-space: nowrap;
}

.co-temp-tick-high {
  position: absolute;
  right: 0;
  white-space: nowrap;
}

.co-param-effect-box {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  padding: 8px 10px;
  border-radius: 8px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-param-effect-icon {
  color: var(--co-accent);
  flex-shrink: 0;
  margin-top: 2px;
}

.co-param-effect-text {
  min-width: 0;
}

.co-param-effect-text strong {
  color: var(--co-text);
}

.co-param-effect-vram {
  color: var(--co-text);
  font-weight: 500;
}

@container (max-width: 340px) {
  .co-temp-scale {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    column-gap: 6px;
    row-gap: 2px;
    position: static;
    height: auto;
  }

  .co-temp-tick-low,
  .co-temp-tick-mid,
  .co-temp-tick-high {
    position: static;
    transform: none;
  }
}

@media (max-width: 480px) {
  .co-param-name-row {
    flex-direction: column;
    align-items: stretch;
    gap: 4px;
  }

  .co-preset-grid {
    grid-template-columns: 1fr;
  }
}
</style>
