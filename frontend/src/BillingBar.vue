<script setup lang="ts">
import { computed } from "vue";
import {
  Badge as ABadge,
  Button as AButton,
  Divider as ADivider,
  Tag as ATag,
  Tooltip as ATooltip,
} from "ant-design-vue";
import {
  describeColabOneBilling,
  formatCountdownClock,
  formatIdlePolicyLabel,
} from "./billing";
import type { RuntimeStatus, ColabOneSnapshot } from "./types";
import { PhLightning, PhClock, PhCoins } from "@phosphor-icons/vue";

const props = defineProps<{
  account: ColabOneSnapshot["account"];
  runtimeState: string;
  runtimeId?: string;
  idle?: RuntimeStatus["idle"];
  idleRemaining: number;
  idleMinutes: number;
  nowMs?: number;
}>();
const emit = defineEmits<{
  touch: [];
}>();

const effectiveRuntimeId = computed(() =>
  props.runtimeState === "stopped" ? undefined : props.runtimeId,
);

const overview = computed(() =>
  describeColabOneBilling(props.account, effectiveRuntimeId.value, props.nowMs),
);

const showIdle = computed(
  () => props.runtimeState === "ready" && Boolean(props.idle),
);

const showBilling = computed(
  () =>
    overview.value.loggedIn ||
    overview.value.active ||
    props.runtimeState !== "stopped",
);

const isIdleActive = computed(() => props.idle?.phase === "active");

const countdownThreshold = computed(() => props.idle?.countdown_seconds || 60);

const isImminentRelease = computed(
  () =>
    Boolean(props.idle) &&
    !isIdleActive.value &&
    props.idleRemaining <= countdownThreshold.value,
);

const countdownClock = computed(() =>
  formatCountdownClock(props.idleRemaining),
);

const idlePolicyLabel = computed(() =>
  formatIdlePolicyLabel(props.idleMinutes),
);

const idleTooltip = computed(() => {
  if (isIdleActive.value) {
    return `远端任务运行中，已暂停空闲自动释放（空闲策略：${idlePolicyLabel.value}）`;
  }
  if (isImminentRelease.value) {
    return `即将自动释放远端资源（剩余 ${countdownClock.value}）；点击立即续期并重置 ${idlePolicyLabel.value} 倒计时`;
  }
  return `超过 ${idlePolicyLabel.value} 无操作或任务将自动释放远端资源；点击可重置倒计时`;
});

const idleAriaLabel = computed(() => {
  if (isIdleActive.value) {
    return "任务运行中 · 暂停自动释放";
  }
  if (isImminentRelease.value) {
    return `即将释放 ${countdownClock.value} · 点击续期`;
  }
  return `空闲释放 ${countdownClock.value}，点击重置倒计时`;
});

const isSyncing = computed(
  () =>
    !overview.value.active &&
    overview.value.loggedIn &&
    (overview.value.instanceCount > 0 ||
      (overview.value.stale && props.account.rate === undefined)),
);

const isStaleData = computed(
  () => overview.value.loggedIn && overview.value.stale,
);

const enduranceLabel = computed(() => {
  const { active, endurance } = overview.value;
  if (!active) {
    return endurance.shortText;
  }
  if (endurance.hours === 0) {
    return "余额已耗尽";
  }
  if (endurance.text.startsWith("约")) {
    return `可用${endurance.text}`;
  }
  return endurance.text;
});

const billingAriaLabel = computed(() => {
  const o = overview.value;
  return `${o.statusText} · ${o.rateText} · 余额 ${o.balanceText} · ${enduranceLabel.value}（${o.instanceCount} 个实例）`;
});
</script>

<template>
  <div
    v-if="showIdle || showBilling"
    class="co-billing-bar"
    role="group"
    aria-label="计费与自动释放状态"
  >
    <ATooltip v-if="showIdle" :title="idleTooltip" placement="bottom">
      <AButton
        size="small"
        :danger="isImminentRelease"
        :class="[
          'co-idle-timer',
          { counting: isImminentRelease, busy: isIdleActive },
        ]"
        :aria-label="idleAriaLabel"
        @click="emit('touch')"
      >
        <span class="co-idle-timer-inner">
          <PhClock
            :size="13"
            :weight="isImminentRelease ? 'fill' : 'regular'"
            :class="['co-idle-icon', { 'co-clock-pulse': isImminentRelease }]"
            aria-hidden="true"
          />
          <span v-if="isIdleActive">任务运行中 · 暂停自动释放</span>
          <span v-else-if="isImminentRelease"
            >即将释放 {{ countdownClock }} · 点击续期</span
          >
          <span v-else>空闲释放 {{ countdownClock }}</span>
        </span>
      </AButton>
    </ATooltip>

    <ATooltip v-if="showBilling" placement="bottom">
      <template #title>
        <div class="co-billing-tip">
          <div class="co-billing-tip-title">
            {{ overview.statusText }}（运行实例：{{ overview.instanceCount }} 台）
          </div>
          <div class="co-billing-tip-row">
            <span>消耗速率</span>
            <b>{{ overview.rateText }}</b>
          </div>
          <div class="co-billing-tip-row">
            <span>剩余余额</span>
            <b>{{ overview.balanceText }}</b>
          </div>
          <div class="co-billing-tip-row">
            <span>预计可用</span>
            <b>{{ overview.endurance.text }}</b>
          </div>
          <div v-if="isStaleData" class="co-billing-tip-stale">
            计费状态超过 60 秒未同步，点击刷新获取最新余额
          </div>
        </div>
      </template>

      <div
        :class="[
          'co-billing-strip',
          {
            'is-active': overview.active,
            'is-idle': !overview.active,
            'is-stale': isStaleData,
          },
        ]"
        role="status"
        :aria-label="billingAriaLabel"
      >
        <span class="co-billing-strip-inner">
          <ATag
            :bordered="false"
            :class="[
              'co-billing-pill',
              { active: overview.active, syncing: isSyncing },
            ]"
          >
            <ABadge
              :status="overview.active || isSyncing ? 'processing' : 'default'"
              :color="
                overview.active
                  ? 'var(--co-danger)'
                  : isSyncing
                    ? 'var(--co-accent)'
                    : undefined
              "
              class="co-billing-dot-badge"
              aria-hidden="true"
            />
            <PhLightning
              v-if="overview.active"
              class="co-billing-pulse"
              :size="12"
              weight="fill"
              aria-hidden="true"
            />
            <span>{{ overview.statusText }}</span>
          </ATag>

          <span class="co-billing-metric co-billing-rate">{{
            overview.rateText
          }}</span>

          <ADivider type="vertical" class="co-strip-divider" aria-hidden="true" />

          <span class="co-billing-metric co-billing-balance">
            <PhCoins
              class="co-coins-icon"
              :size="13"
              weight="duotone"
              aria-hidden="true"
            />
            <span
              >余额 <b>{{ overview.balanceText }}</b></span
            >
          </span>

          <ATag
            :bordered="false"
            :class="['co-endurance-tag', `co-level-${overview.endurance.level}`]"
          >
            {{ enduranceLabel }}
          </ATag>

          <ABadge
            v-if="isStaleData"
            status="warning"
            class="co-stale-dot-badge"
            aria-label="计费数据待同步"
          />
        </span>
      </div>
    </ATooltip>
  </div>
</template>

<style scoped>
.co-billing-bar {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex-shrink: 0;
}

.co-billing-bar .co-idle-timer {
  height: 30px;
  padding: 0 12px;
  border-radius: 9999px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  color: var(--co-secondary-text);
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  box-shadow: var(--co-card-shadow);
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    color 0.16s ease;
}

.co-billing-bar .co-idle-timer:hover {
  background: var(--co-soft-hover);
  border-color: var(--co-input-border);
  color: var(--co-text);
}

.co-billing-bar .co-idle-timer.busy {
  background: var(--co-selected);
  border-color: rgba(59, 130, 246, 0.32);
  color: var(--co-accent-strong);
}

.co-billing-bar .co-idle-timer.busy:hover {
  background: var(--co-selected);
  border-color: var(--co-accent);
  color: var(--co-accent-strong);
}

.co-billing-bar .co-idle-timer.counting {
  background: var(--co-danger-bg);
  border-color: var(--co-danger-border);
  color: var(--co-danger-text);
  font-weight: 600;
}

.co-billing-bar .co-idle-timer.counting:hover {
  background: var(--co-danger-bg);
  border-color: var(--co-danger);
  color: var(--co-danger-text);
}

.co-idle-timer-inner {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  line-height: 1;
}

.co-idle-icon {
  flex-shrink: 0;
}

.co-billing-bar .co-billing-strip {
  height: 30px;
  padding: 0 6px 0 5px;
  border-radius: 9999px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  color: var(--co-secondary-text);
  font-size: 12px;
  line-height: 1;
  box-shadow: var(--co-card-shadow);
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
  cursor: default;
  user-select: none;
}

.co-billing-bar .co-billing-strip.is-active {
  background: var(--co-surface);
  border-color: var(--co-danger-border);
}

.co-billing-strip-inner {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  line-height: 1;
}

.co-billing-bar .co-billing-pill {
  margin: 0;
  height: 22px;
  padding: 0 9px;
  border-radius: 9999px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  font-weight: 560;
  line-height: 1;
  color: var(--co-secondary-text);
  background: var(--co-track);
  border: none;
  white-space: nowrap;
  flex-shrink: 0;
}

.co-billing-bar .co-billing-pill.syncing {
  background: var(--co-selected);
  color: var(--co-accent);
}

.co-billing-bar .co-billing-pill.active {
  background: var(--co-danger-bg);
  color: var(--co-danger-text);
  font-weight: 600;
}

.co-billing-dot-badge {
  display: inline-flex;
  align-items: center;
  line-height: 1;
}

.co-billing-dot-badge :deep(.ant-badge-status-dot) {
  width: 6px;
  height: 6px;
  top: 0;
}

.co-billing-pill.active .co-billing-dot-badge :deep(.ant-badge-status-processing::after) {
  border-color: var(--co-danger);
}

.co-billing-pill.syncing .co-billing-dot-badge :deep(.ant-badge-status-processing::after) {
  border-color: var(--co-accent);
}

.co-billing-pulse,
.co-clock-pulse {
  animation: status-pulse 1.6s ease-in-out infinite;
  flex-shrink: 0;
}

@keyframes status-pulse {
  50% {
    opacity: 0.4;
  }
}

.co-billing-metric {
  display: inline-flex;
  align-items: center;
  font-variant-numeric: tabular-nums;
}

.co-billing-rate {
  font-weight: 560;
  color: var(--co-secondary-text);
}

.co-billing-strip.is-active .co-billing-rate {
  color: var(--co-danger-text);
  font-weight: 600;
}

.co-strip-divider {
  margin: 0;
  height: 13px;
  border-inline-start-color: var(--co-border);
  flex-shrink: 0;
}

.co-billing-balance {
  gap: 4px;
  color: var(--co-muted);
}

.co-billing-balance b {
  color: var(--co-text);
  font-weight: 600;
}

.co-coins-icon {
  color: var(--co-muted);
  flex-shrink: 0;
}

.co-endurance-tag {
  margin: 0;
  height: 22px;
  padding: 0 9px;
  border-radius: 9999px;
  display: inline-flex;
  align-items: center;
  font-size: 12px;
  line-height: 1;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}

.co-endurance-tag.co-level-idle,
.co-endurance-tag.co-level-unknown {
  background: var(--co-track);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-muted);
  font-weight: 500;
}

.co-endurance-tag.co-level-healthy {
  background: var(--co-selected);
  color: var(--co-accent-strong);
  font-weight: 600;
}

.co-endurance-tag.co-level-low {
  background: var(--co-danger-bg);
  border: 1px solid var(--co-danger-border);
  color: var(--co-danger-text);
  font-weight: 600;
}

.co-endurance-tag.co-level-critical {
  background: var(--co-danger);
  color: var(--co-on-danger);
  font-weight: 650;
}

.co-stale-dot-badge {
  display: inline-flex;
  align-items: center;
  margin-left: -2px;
  line-height: 1;
}

.co-stale-dot-badge :deep(.ant-badge-status-dot) {
  width: 6px;
  height: 6px;
  top: 0;
}

.co-billing-tip {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 13px;
  min-width: 204px;
  font-variant-numeric: tabular-nums;
}

.co-billing-tip-title {
  font-weight: 600;
  padding-bottom: 4px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.16);
}

.co-billing-tip-row {
  display: flex;
  justify-content: space-between;
  gap: 14px;
}

.co-billing-tip-row span {
  opacity: 0.78;
}

.co-billing-tip-stale {
  margin-top: 2px;
  color: #fbbf24;
  font-size: 12px;
}

@media (max-width: 1180px) {
  .co-billing-bar {
    gap: 6px;
  }

  .co-billing-bar .co-idle-timer {
    padding: 0 8px;
  }

  .co-billing-bar .co-billing-strip {
    padding: 0 5px 0 3px;
  }

  .co-billing-strip-inner {
    gap: 5px;
  }

  .co-billing-bar .co-billing-pill,
  .co-endurance-tag {
    padding: 0 6px;
  }

  .co-coins-icon {
    display: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .co-billing-pulse,
  .co-clock-pulse {
    animation: none;
  }
}
</style>
