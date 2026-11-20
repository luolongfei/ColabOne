<script setup lang="ts">
import { computed } from "vue";
import {
  Badge as ABadge,
  Button as AButton,
  Card as ACard,
  Progress as AProgress,
  Statistic as AStatistic,
  Tag as ATag,
  Tooltip as ATooltip,
} from "ant-design-vue";
import {
  COLABONE_IDLE_RELEASE_OPTIONS,
  describeColabOneBilling,
  formatCountdownClock,
  formatIdlePolicyLabel,
} from "./billing";
import type { RuntimeStatus, ColabOneSnapshot } from "./types";
import {
  PhArrowsClockwise,
  PhClock,
  PhCoins,
  PhLightning,
  PhShieldCheck,
  PhTimer,
} from "@phosphor-icons/vue";

const props = defineProps<{
  account: ColabOneSnapshot["account"];
  setupColab: boolean;
  runtimeState: string;
  runtimeId?: string;
  idle?: RuntimeStatus["idle"];
  idleRemaining: number;
  idleMinutes: number;
  busy?: boolean;
  nowMs?: number;
  showIdleFooter?: boolean;
}>();

const emit = defineEmits<{
  refreshAccount: [];
  touch: [];
  changeIdleMinutes: [minutes: number];
  openSettings: [];
}>();

const overview = computed(() =>
  describeColabOneBilling(props.account, props.runtimeId, props.nowMs),
);

const hasFiniteRate = computed(
  () => props.account.rate !== undefined && Number.isFinite(props.account.rate),
);

const hasFiniteBalance = computed(
  () =>
    props.account.balance !== undefined &&
    Number.isFinite(props.account.balance),
);

const isSyncing = computed(
  () =>
    props.setupColab &&
    !overview.value.active &&
    overview.value.instanceCount > 0,
);

const statusPillText = computed(() => {
  if (!props.setupColab) return "当前未扣费";
  if (overview.value.active) return "正在扣费";
  if (overview.value.loggedIn && overview.value.instanceCount > 0) {
    return "实例运行 · 费率同步中";
  }
  if (!overview.value.loggedIn) return "同步账户中";
  return overview.value.statusText;
});

const statusSubtitle = computed(() => {
  if (!props.setupColab) {
    return "未连接账户 · 0 个运行实例";
  }
  const syncWord = overview.value.stale ? "待同步" : "已同步";
  const count = overview.value.instanceCount;
  if (overview.value.active) {
    return count > 0
      ? `${syncWord} · ${count} 个计费实例`
      : `${syncWord} · 费率结算中`;
  }
  return `${syncWord} · ${count} 个运行实例`;
});

const rateValueText = computed(() => {
  if (hasFiniteRate.value) {
    return overview.value.rate.toFixed(2);
  }
  return props.setupColab ? "--" : "0.00";
});

const rateSubtitle = computed(() => {
  if (overview.value.active) return "按运行中实例实时消耗";
  if (props.setupColab && overview.value.instanceCount > 0) {
    return "等待 Google 回传实时费率";
  }
  return "当前无实例消耗";
});

const balanceValueText = computed(() =>
  hasFiniteBalance.value ? (props.account.balance as number).toFixed(2) : "--",
);

const balanceSubtitle = computed(() => {
  if (!props.setupColab) return "连接账户后同步实时 CU 余额";
  if (!hasFiniteBalance.value) return "点击右上角刷新同步账户余额";
  if (overview.value.stale) {
    return `${props.account.email || "Google Colab 计算单元"} · 超过 60 秒未同步`;
  }
  return props.account.email || "Google Colab 计算单元";
});

const endurancePrimaryText = computed(() => {
  if (!props.setupColab) return "当前未扣费 · 余额不消耗";
  return overview.value.endurance.text;
});

const enduranceTone = computed(() => {
  if (!props.setupColab) return "idle";
  return overview.value.endurance.level;
});

const enduranceSubtitle = computed(() => {
  if (!props.setupColab) {
    return "登录后按实时费率折算可用天数与小时";
  }
  const { level, hours } = overview.value.endurance;
  if (level === "unknown") {
    return "同步余额与费率后自动折算";
  }
  if (level === "idle") {
    if ((props.account.balance ?? 0) > 0) {
      return "启动实例后按实时费率折算可用天数与小时";
    }
    return "当前账户余额已用尽";
  }
  if (level === "critical") {
    return (props.account.balance ?? 0) > 0
      ? "可用时长不足 2 小时，请及时留意任务保存"
      : "余额已耗尽，实例可能随时被回收";
  }
  if (level === "low") {
    return `可用不足 12 小时 · 按 ${overview.value.rate.toFixed(2)} CU/小时测算`;
  }
  if (hours !== null && hours >= 24) {
    return `共约 ${Math.floor(hours)} 小时 · 按当前费率连续运行`;
  }
  return `按当前 ${overview.value.rate.toFixed(2)} CU/小时连续运行测算`;
});

const showEnduranceBar = computed(
  () =>
    props.setupColab &&
    overview.value.active &&
    overview.value.endurance.hours !== null,
);

const enduranceProgressPercent = computed(() => {
  const hours = overview.value.endurance.hours;
  if (hours === null || hours <= 0) return 0;
  return Math.min(100, Math.max(6, Math.round((hours / 48) * 100)));
});

const enduranceStrokeColor = computed(() => {
  switch (enduranceTone.value) {
    case "critical":
      return "var(--co-danger)";
    case "low":
      return "var(--co-warning)";
    default:
      return "var(--co-success)";
  }
});

const activeInstanceCount = computed(() =>
  props.setupColab ? overview.value.instanceCount : 0,
);

const instanceSubtitle = computed(() => {
  if (!props.setupColab) return "未登录 Google 账户";
  if (overview.value.active && overview.value.instanceCount > 0) {
    return "正在按实时费率扣费";
  }
  if (overview.value.active) return "实例费率结算中";
  if (overview.value.instanceCount > 0) return "实例在线 · 等待费率回传";
  return "无运行实例 · 零消耗";
});

const selectedIdleMinutes = computed(() =>
  Number.isFinite(props.idleMinutes) && props.idleMinutes > 0
    ? props.idleMinutes
    : 30,
);

const hasCustomIdleOption = computed(
  () =>
    !COLABONE_IDLE_RELEASE_OPTIONS.some(
      (opt) => opt.value === selectedIdleMinutes.value,
    ),
);

const isIdleCountdownActive = computed(
  () =>
    props.runtimeState === "ready" &&
    !!props.idle &&
    props.idle.phase !== "active",
);

const isUrgentCountdown = computed(
  () =>
    isIdleCountdownActive.value &&
    !!props.idle &&
    (props.idle.phase === "countdown" ||
      props.idleRemaining <= (props.idle.countdown_seconds || 60)),
);

const totalIdleWindowSeconds = computed(() => {
  const baseSeconds = Math.max(60, selectedIdleMinutes.value * 60);
  const reportedTotal =
    (props.idle?.idle_seconds ?? 0) + (props.idle?.remaining_seconds ?? 0);
  return Math.max(baseSeconds, reportedTotal > 0 ? reportedTotal : baseSeconds);
});

const idleProgressPercent = computed(() => {
  if (props.runtimeState !== "ready" || !props.idle) return 100;
  if (props.idle.phase === "active") return 100;
  const ratio =
    (Math.max(0, props.idleRemaining) / totalIdleWindowSeconds.value) * 100;
  return Math.min(100, Math.max(0, ratio));
});

function onIdleMinutesChange(event: Event) {
  const minutes = Number((event.target as HTMLSelectElement).value);
  if (Number.isFinite(minutes) && minutes > 0) {
    emit("changeIdleMinutes", minutes);
  }
}
</script>

<template>
  <ACard
    class="co-billing-card"
    :bordered="true"
    size="small"
    aria-label="账户计费、余额与自动释放概览"
  >
    <template #title>
      <div class="co-bc-head-left">
        <span
          class="co-bc-title-icon"
          :class="{ active: overview.active }"
          aria-hidden="true"
        >
          <PhCoins :size="15" weight="duotone" />
        </span>
        <span class="co-bc-title">算力计费与余额</span>
        <ATag
          :bordered="true"
          :class="[
            'co-bc-status-tag',
            {
              billing: overview.active,
              syncing: isSyncing,
            },
          ]"
        >
          <ABadge
            v-if="overview.active"
            status="processing"
            color="var(--co-danger)"
            class="co-bc-status-dot"
            aria-hidden="true"
          />
          <PhLightning
            v-if="overview.active"
            class="co-pulse-icon"
            :size="12"
            weight="fill"
            aria-hidden="true"
          />
          <ABadge
            v-else-if="isSyncing"
            status="processing"
            color="var(--co-accent)"
            class="co-bc-status-dot"
            aria-hidden="true"
          />
          <PhShieldCheck
            v-else
            :size="13"
            weight="duotone"
            aria-hidden="true"
          />
          <span>{{ statusPillText }}</span>
        </ATag>
        <ATag
          v-if="setupColab && overview.stale"
          :bordered="false"
          class="co-bc-stale-tag"
        >
          待同步
        </ATag>
      </div>
    </template>

    <template #extra>
      <div class="co-bc-head-right">
        <span class="co-bc-sync-caption">{{ statusSubtitle }}</span>
        <ATooltip title="刷新账户余额与计费状态" placement="top">
          <AButton
            size="small"
            type="text"
            class="co-bc-refresh-btn"
            aria-label="刷新账户余额与计费状态"
            :disabled="!setupColab || busy"
            @click="emit('refreshAccount')"
          >
            <template #icon>
              <PhArrowsClockwise :size="14" :class="{ spinning: busy }" />
            </template>
          </AButton>
        </ATooltip>
      </div>
    </template>

    <div class="co-billing-grid">
      <!-- 1. 剩余 CU 余额 -->
      <div class="co-metric-cell">
        <template v-if="!setupColab">
          <span class="co-metric-label">剩余 CU 余额</span>
          <div class="co-metric-login-wrap">
            <AButton
              type="primary"
              size="small"
              class="co-bc-login-btn"
              :disabled="busy"
              @click="emit('openSettings')"
            >
              立即登录 Google 账户
            </AButton>
          </div>
        </template>
        <AStatistic
          v-else
          title="剩余 CU 余额"
          :value="balanceValueText"
          group-separator=""
          :class="['co-bc-stat', { 'is-muted': !hasFiniteBalance }]"
        >
          <template v-if="hasFiniteBalance" #suffix>
            <span class="co-bc-stat-unit">CU</span>
          </template>
        </AStatistic>
        <small class="co-metric-sub" :title="account.email || undefined">{{
          balanceSubtitle
        }}</small>
      </div>

      <!-- 2. 当前消耗速率 -->
      <div class="co-metric-cell">
        <AStatistic
          title="当前消耗速率"
          :value="rateValueText"
          group-separator=""
          :class="['co-bc-stat', { 'is-billing': overview.active }]"
        >
          <template #suffix>
            <span class="co-bc-stat-unit">CU/小时</span>
          </template>
        </AStatistic>
        <small class="co-metric-sub">{{ rateSubtitle }}</small>
      </div>

      <!-- 3. 预计可用时长 -->
      <div class="co-metric-cell">
        <AStatistic
          :value="endurancePrimaryText"
          group-separator=""
          :class="['co-bc-stat', 'co-bc-stat-endurance', `co-tone-${enduranceTone}`]"
        >
          <template #title>
            <span class="co-metric-label-with-icon">
              <PhClock :size="13" aria-hidden="true" />
              <span>预计可用时长</span>
            </span>
          </template>
        </AStatistic>
        <AProgress
          v-if="showEnduranceBar"
          :percent="enduranceProgressPercent"
          :show-info="false"
          size="small"
          :stroke-color="enduranceStrokeColor"
          trail-color="var(--co-subtle)"
          class="co-bc-endurance-progress"
        />
        <small :class="['co-metric-sub', `co-sub-${enduranceTone}`]">{{
          enduranceSubtitle
        }}</small>
      </div>

      <!-- 4. 活跃实例数 -->
      <div class="co-metric-cell">
        <AStatistic
          title="活跃实例"
          :value="activeInstanceCount"
          group-separator=""
          :class="[
            'co-bc-stat',
            { 'is-billing': overview.active && activeInstanceCount > 0 },
          ]"
        >
          <template #suffix>
            <span class="co-bc-stat-unit">台</span>
          </template>
        </AStatistic>
        <small class="co-metric-sub">{{ instanceSubtitle }}</small>
      </div>
    </div>

    <!-- 空闲自动释放策略与实时倒计时（仅在显式开启时渲染，避免与设备控制栏重复下拉框） -->
    <div
      v-if="showIdleFooter"
      class="co-idle-footer"
      :class="{
        urgent: isUrgentCountdown,
        paused: runtimeState === 'ready' && idle?.phase === 'active',
      }"
    >
      <div class="co-idle-status">
        <span class="co-idle-badge-icon" aria-hidden="true">
          <PhShieldCheck
            v-if="runtimeState === 'ready' && idle?.phase === 'active'"
            :size="17"
            weight="duotone"
          />
          <PhTimer
            v-else
            :size="17"
            :weight="isUrgentCountdown ? 'fill' : 'duotone'"
          />
        </span>

        <div class="co-idle-copy">
          <template v-if="runtimeState === 'ready' && idle">
            <div v-if="idle.phase === 'active'" class="co-idle-line">
              <strong>任务执行中 · 自动释放已暂停</strong>
              <span class="co-idle-note"
                >远端任务运行期间暂停倒计时，完成后自动恢复</span
              >
            </div>
            <div v-else class="co-idle-countdown-wrap">
              <div class="co-idle-line">
                <strong :class="{ 'co-urgent-text': isUrgentCountdown }">
                  剩余
                  <span class="co-countdown-digits">{{
                    formatCountdownClock(idleRemaining)
                  }}</span>
                  自动释放
                </strong>
                <ATag
                  v-if="isUrgentCountdown"
                  :bordered="true"
                  class="co-urgent-tag"
                >
                  即将自动释放
                </ATag>
              </div>
              <div
                class="co-idle-progress-wrap"
                role="progressbar"
                aria-label="空闲自动释放剩余时间"
                :aria-valuenow="Math.max(0, Math.ceil(idleRemaining))"
                :aria-valuemin="0"
                :aria-valuemax="totalIdleWindowSeconds"
              >
                <AProgress
                  :percent="Math.round(idleProgressPercent)"
                  :show-info="false"
                  size="small"
                  :stroke-color="
                    isUrgentCountdown ? 'var(--co-danger)' : 'var(--co-accent)'
                  "
                  trail-color="var(--co-border)"
                  class="co-idle-ant-progress"
                />
              </div>
            </div>
          </template>

          <div v-else-if="runtimeState === 'starting'" class="co-idle-line">
            <strong
              >实例启动中 · 就绪后空闲满
              {{ formatIdlePolicyLabel(selectedIdleMinutes) }}
              自动释放资源</strong
            >
          </div>

          <div v-else-if="runtimeState === 'releasing'" class="co-idle-line">
            <strong>正在释放远端实例资源…</strong>
          </div>

          <div v-else class="co-idle-line">
            <strong
              >空闲无操作满
              {{ formatIdlePolicyLabel(selectedIdleMinutes) }}
              自动释放资源</strong
            >
            <span class="co-idle-note"
              >下载、装载与推理期间自动暂停，避免白白消耗计算单元</span
            >
          </div>
        </div>
      </div>

      <div class="co-idle-actions">
        <AButton
          v-if="runtimeState === 'ready' && idle && idle.phase !== 'active'"
          size="small"
          :danger="isUrgentCountdown"
          class="co-reset-btn"
          :class="{ urgent: isUrgentCountdown }"
          :disabled="busy"
          @click="emit('touch')"
        >
          <template #icon>
            <PhArrowsClockwise :size="13" />
          </template>
          重置倒计时
        </AButton>

        <label class="co-idle-select-group">
          <span class="co-idle-select-label">空闲自动释放</span>
          <select
            class="co-idle-select"
            :value="selectedIdleMinutes"
            aria-label="空闲自动释放时长"
            :disabled="busy"
            @change="onIdleMinutesChange"
          >
            <option v-if="hasCustomIdleOption" :value="selectedIdleMinutes">
              {{ formatIdlePolicyLabel(selectedIdleMinutes) }}（当前）
            </option>
            <option
              v-for="opt in COLABONE_IDLE_RELEASE_OPTIONS"
              :key="opt.value"
              :value="opt.value"
            >
              {{ opt.label }}
            </option>
          </select>
        </label>
      </div>
    </div>
  </ACard>
</template>

<style scoped>
.co-billing-card {
  container-type: inline-size;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  border-radius: 16px;
  box-shadow: var(--co-card-shadow);
  color: var(--co-text);
  overflow: hidden;
}

.co-billing-card :deep(.ant-card-head) {
  min-height: 50px;
  padding: 0 22px;
  background: transparent;
  border-bottom: 1px solid var(--co-border-secondary);
}

.co-billing-card :deep(.ant-card-head-wrapper) {
  gap: 10px;
}

.co-billing-card :deep(.ant-card-head-title) {
  padding: 10px 0;
}

.co-billing-card :deep(.ant-card-extra) {
  padding: 8px 0;
}

.co-billing-card :deep(.ant-card-body) {
  padding: 20px 22px;
}

.co-bc-head-left {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  min-width: 0;
}

.co-bc-title-icon {
  width: 32px;
  height: 32px;
  border-radius: 10px;
  background: var(--co-selected);
  border: 1px solid rgba(59, 130, 246, 0.18);
  color: var(--co-accent);
  display: inline-grid;
  place-items: center;
  flex-shrink: 0;
}

.co-bc-title-icon.active {
  background: var(--co-danger-bg);
  border-color: var(--co-danger-border);
  color: var(--co-danger-text);
}

.co-bc-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
  letter-spacing: -0.1px;
}

.co-bc-status-tag {
  margin: 0;
  height: 24px;
  padding: 0 10px;
  border-radius: 9999px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
  background: var(--co-track);
  color: var(--co-secondary-text);
  border: 1px solid var(--co-border);
}

.co-bc-status-tag.billing {
  background: var(--co-danger-bg);
  color: var(--co-danger-text);
  border-color: var(--co-danger-border);
}

.co-bc-status-tag.syncing {
  background: var(--co-selected);
  color: var(--co-accent-strong);
  border-color: rgba(59, 130, 246, 0.28);
}

.co-bc-status-dot {
  display: inline-flex;
  align-items: center;
  line-height: 1;
}

.co-bc-status-dot :deep(.ant-badge-status-dot) {
  width: 6px;
  height: 6px;
  top: 0;
}

.co-bc-status-tag.billing .co-bc-status-dot :deep(.ant-badge-status-processing::after) {
  border-color: var(--co-danger);
}

.co-bc-status-tag.syncing .co-bc-status-dot :deep(.ant-badge-status-processing::after) {
  border-color: var(--co-accent);
}

.co-bc-stale-tag {
  margin: 0;
  height: 24px;
  padding: 0 9px;
  border-radius: 9999px;
  font-size: 12px;
  font-weight: 550;
  line-height: 22px;
  background: var(--co-warning-bg);
  border: 1px solid var(--co-warning-border);
  color: var(--co-warning-text);
}

.co-bc-head-right {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.co-bc-sync-caption {
  font-size: 12px;
  color: var(--co-muted);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.co-bc-refresh-btn {
  width: 30px;
  height: 30px;
  border-radius: 9999px !important;
  color: var(--co-muted);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-bc-refresh-btn:hover:not(:disabled) {
  background: var(--co-soft-hover);
  color: var(--co-text);
}

.spinning {
  animation: spin-icon 0.9s linear infinite;
}

@keyframes spin-icon {
  to {
    transform: rotate(360deg);
  }
}

.co-pulse-icon {
  flex-shrink: 0;
  animation: pulse-fade 1.6s ease-in-out infinite;
}

@keyframes pulse-fade {
  50% {
    opacity: 0.35;
  }
}

.co-billing-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px 0;
}

.co-metric-cell {
  min-width: 0;
  padding: 2px 20px;
  border-left: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
}

.co-metric-cell:first-child {
  padding-left: 0;
  border-left: 0;
}

.co-metric-cell:last-child {
  padding-right: 0;
}

.co-metric-label {
  display: block;
  font-size: 13px;
  color: var(--co-muted);
  font-weight: 500;
  margin-bottom: 6px;
}

.co-metric-label-with-icon {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.co-metric-login-wrap {
  min-height: 32px;
  display: flex;
  align-items: center;
}

.co-bc-login-btn {
  font-size: 13px;
  font-weight: 550;
}

.co-bc-stat :deep(.ant-statistic-title) {
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--co-muted);
  line-height: 1.4;
}

.co-bc-stat :deep(.ant-statistic-content) {
  font-family: var(--co-font-mono);
  font-size: 26px;
  font-weight: 700;
  letter-spacing: -0.03em;
  color: var(--co-text);
  font-variant-numeric: tabular-nums;
  line-height: 1.2;
  display: flex;
  align-items: baseline;
  flex-wrap: nowrap;
}

.co-bc-stat.is-billing :deep(.ant-statistic-content) {
  color: var(--co-danger-text);
}

.co-bc-stat.is-muted :deep(.ant-statistic-content) {
  color: var(--co-muted);
  font-size: 20px;
}

.co-bc-stat-unit {
  font-size: 13px;
  font-weight: 500;
  color: var(--co-muted);
  margin-left: 4px;
  letter-spacing: 0;
}

.co-bc-stat-endurance :deep(.ant-statistic-content) {
  font-size: 20px;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.co-bc-stat-endurance.co-tone-idle :deep(.ant-statistic-content),
.co-bc-stat-endurance.co-tone-unknown :deep(.ant-statistic-content) {
  font-size: 15px;
  font-weight: 550;
  letter-spacing: 0;
  color: var(--co-secondary-text);
}

.co-bc-stat-endurance.co-tone-low :deep(.ant-statistic-content) {
  color: var(--co-warning-text);
}

.co-bc-stat-endurance.co-tone-critical :deep(.ant-statistic-content) {
  color: var(--co-danger-text);
  font-size: 16px;
}

.co-bc-endurance-progress {
  margin: 6px 0 0;
  line-height: 1;
}

.co-bc-endurance-progress :deep(.ant-progress-outer) {
  display: block;
}

.co-metric-sub {
  display: block;
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-metric-sub.co-sub-critical {
  color: var(--co-danger-text);
}

.co-metric-sub.co-sub-low {
  color: var(--co-warning-text);
}

/* Integrated Idle Auto-Release Policy Footer */
.co-idle-footer {
  margin-top: 16px;
  padding: 12px 16px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px 18px;
  flex-wrap: wrap;
}

.co-idle-footer.urgent {
  background: var(--co-danger-bg);
  border-color: var(--co-danger-border);
}

.co-idle-footer.paused {
  background: var(--co-selected);
  border-color: rgba(59, 130, 246, 0.28);
}

.co-idle-status {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex: 1 1 280px;
}

.co-idle-badge-icon {
  width: 30px;
  height: 30px;
  border-radius: 8px;
  background: var(--co-surface);
  border: 1px solid var(--co-border);
  color: var(--co-secondary-text);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.co-idle-footer.paused .co-idle-badge-icon {
  color: var(--co-accent);
}

.co-idle-footer.urgent .co-idle-badge-icon {
  color: var(--co-danger-text);
  border-color: var(--co-danger-border);
}

.co-idle-copy {
  min-width: 0;
  flex: 1;
}

.co-idle-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 10px;
  font-size: 14px;
  color: var(--co-secondary-text);
}

.co-idle-line strong {
  font-weight: 580;
  color: var(--co-text);
}

.co-idle-line strong.co-urgent-text {
  color: var(--co-danger-text);
}

.co-countdown-digits {
  font-variant-numeric: tabular-nums;
  font-family: var(--co-font-mono);
  font-weight: 650;
}

.co-urgent-tag {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  padding: 0 9px;
  line-height: 22px;
  border-radius: 9999px;
  background: var(--co-danger-bg);
  color: var(--co-danger-text);
  border-color: var(--co-danger-border);
}

.co-idle-note {
  font-size: 13px;
  color: var(--co-muted);
}

.co-idle-countdown-wrap {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 14px;
}

.co-idle-progress-wrap {
  width: 150px;
  max-width: 100%;
  flex-shrink: 0;
}

.co-idle-ant-progress {
  margin: 0;
  line-height: 1;
}

.co-idle-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  flex-shrink: 0;
}

.co-reset-btn {
  font-size: 13.5px;
  font-weight: 550;
}

.co-reset-btn.urgent {
  border-color: var(--co-danger-border);
  color: var(--co-danger-text);
}

.co-idle-select-group {
  display: inline-flex;
  flex-direction: row;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  color: var(--co-secondary-text);
  margin: 0;
}

.co-idle-select-label {
  white-space: nowrap;
  color: var(--co-muted);
  font-weight: 500;
}

.co-idle-select {
  padding: 6px 12px;
  border-radius: 10px;
  font-size: 13.5px;
  font-weight: 550;
  background: var(--co-surface);
  color: var(--co-text);
  border: 1px solid var(--co-input-border);
  min-width: 136px;
  cursor: pointer;
}

@container (max-width: 820px) {
  .co-billing-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 18px 20px;
  }

  .co-metric-cell {
    padding: 0;
    border-left: 0;
  }

  .co-metric-cell:nth-child(even) {
    padding-left: 18px;
    border-left: 1px solid var(--co-border-secondary);
  }
}

@media (max-width: 1120px) {
  .co-billing-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 16px 20px;
  }

  .co-metric-cell {
    padding: 0;
    border-left: 0;
  }

  .co-metric-cell:nth-child(even) {
    padding-left: 18px;
    border-left: 1px solid var(--co-border);
  }
}

@media (max-width: 640px) {
  .co-billing-card :deep(.ant-card-body) {
    padding: 14px 16px;
  }

  .co-bc-sync-caption {
    display: none;
  }

  .co-billing-grid {
    grid-template-columns: 1fr;
    gap: 14px;
  }

  .co-metric-cell,
  .co-metric-cell:nth-child(even) {
    padding: 0 0 12px;
    border-left: 0;
    border-bottom: 1px solid var(--co-border);
  }

  .co-metric-cell:last-child {
    padding-bottom: 0;
    border-bottom: 0;
  }

  .co-idle-actions {
    width: 100%;
    justify-content: space-between;
  }
}

@media (prefers-reduced-motion: reduce) {
  .spinning,
  .co-pulse-icon {
    animation: none;
  }
}
</style>
