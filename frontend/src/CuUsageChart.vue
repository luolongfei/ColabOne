<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
  type CSSProperties,
} from "vue";
import {
  Badge as ABadge,
  Button as AButton,
  Card as ACard,
  Popover as APopover,
  Segmented as ASegmented,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhCalendarBlank,
  PhCaretDown,
  PhCaretLeft,
  PhCaretRight,
  PhChartLineUp,
  PhClockCounterClockwise,
  PhCoins,
  PhFlame,
  PhTimer,
  PhTrendUp,
} from "@phosphor-icons/vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { colabOneCommand } from "./api";
import {
  aggregateColabOneCuSeries,
  buildColabOneCalendarMonth,
  buildColabOneCuChartLayout,
  clampColabOneDateKey,
  formatColabOneActiveDuration,
  formatColabOneDateKey,
  parseColabOneDateKey,
  resolveColabOneCuHoverIndex,
  resolveColabOneCuUsageWindow,
  shiftColabOneDateKey,
  type ColabOneCuUsageMetric,
  type ColabOneCuUsagePoint,
  type ColabOneCuUsageRangeMode,
} from "./cuUsage";
import { colabOneLocaleCode, t } from "./i18n";
import { COLABONE_STORAGE_KEYS } from "./storage";
import type { ColabOneCuUsageResponse, ColabOneSnapshot } from "./types";

const props = defineProps<{
  account: ColabOneSnapshot["account"];
  setupColab: boolean;
  loading?: boolean;
  ipcDisconnected?: boolean;
  nowMs?: number;
  highlighted?: boolean;
}>();

const emit = defineEmits<{
  openSettings: [];
}>();

const uid = `co-cu-${Math.random().toString(36).slice(2, 8)}`;

function readStoredMetric(): ColabOneCuUsageMetric {
  try {
    const raw = globalThis.localStorage?.getItem(
      COLABONE_STORAGE_KEYS.cuUsageMetric,
    );
    if (raw === "rate" || raw === "balance" || raw === "consumed") {
      return raw;
    }
  } catch {
    // Ignore storage access errors in restricted environments.
  }
  return "consumed";
}

const initNowMs =
  typeof props.nowMs === "number" && Number.isFinite(props.nowMs)
    ? props.nowMs
    : Date.now();
const initDate = new Date(initNowMs);

const rangeMode = ref<ColabOneCuUsageRangeMode>("24h");
const metric = ref<ColabOneCuUsageMetric>(readStoredMetric());
const selectedDateKey = ref<string>(formatColabOneDateKey(initDate));
const anchorEndDateKey = ref<string>(formatColabOneDateKey(initDate));
const minuteBucketNowMs = ref<number>(initNowMs);
const calendarOpen = ref(false);
const calendarYear = ref<number>(initDate.getFullYear());
const calendarMonthIndex = ref<number>(initDate.getMonth());

const usageResponse = shallowRef<ColabOneCuUsageResponse | null>(null);
const cachedAvailableDates = shallowRef<string[]>([]);
const cachedActiveDates = shallowRef<string[]>([]);
let lastAccountEmail = (props.account.email || "").trim().toLowerCase();
const fetching = ref(false);
const hasFetchedOnce = ref(false);
let fetchSeq = 0;
let minuteTimer: ReturnType<typeof setInterval> | undefined;

const plotContainerRef = ref<HTMLElement | null>(null);
const plotWidth = ref<number>(760);
const PLOT_HEIGHT = 212;
let resizeObserver: ResizeObserver | null = null;

const hoveredIndex = ref<number | null>(null);

// Roll minuteBucketNowMs cleanly on exact minute boundaries so live 24h windows
// roll past hour and midnight boundaries immediately without re-computing every 1s.
watch(
  () => props.nowMs,
  (nextMs) => {
    if (typeof nextMs !== "number" || !Number.isFinite(nextMs)) return;
    if (
      Math.floor(nextMs / 60_000) !== Math.floor(minuteBucketNowMs.value / 60_000)
    ) {
      minuteBucketNowMs.value = nextMs;
    }
  },
);

const effectiveNowMs = computed<number>(() => minuteBucketNowMs.value);

const windowSpec = computed(() =>
  resolveColabOneCuUsageWindow({
    mode: rangeMode.value,
    selectedDateKey: selectedDateKey.value,
    anchorEndDateKey: anchorEndDateKey.value,
    nowMs: effectiveNowMs.value,
  }),
);

const rangeOptions = computed(() => [
  { value: "24h", label: t("cuUsage.range24h") },
  { value: "7d", label: t("cuUsage.range7d") },
  { value: "30d", label: t("cuUsage.range30d") },
]);

const metricOptions = computed(() => [
  { value: "consumed", label: t("cuUsage.metricConsumed") },
  { value: "rate", label: t("cuUsage.metricRate") },
  { value: "balance", label: t("cuUsage.metricBalance") },
]);

const activeSegmentedRange = computed<string>(() =>
  rangeMode.value === "day" ? "" : rangeMode.value,
);

const availableDatesSet = computed<Set<string>>(
  () => new Set(cachedAvailableDates.value),
);

const activeDatesSet = computed<Set<string>>(
  () => new Set(cachedActiveDates.value),
);

const calendarMonth = computed(() =>
  buildColabOneCalendarMonth({
    year: calendarYear.value,
    monthIndex: calendarMonthIndex.value,
    locale: colabOneLocaleCode.value,
    todayDateKey: windowSpec.value.todayDateKey,
    selectedDateKey:
      rangeMode.value === "day" ? windowSpec.value.selectedDateKey : "",
    availableDates: availableDatesSet.value,
    activeDates: activeDatesSet.value,
  }),
);

const mergedResponse = computed<ColabOneCuUsageResponse | null>(() => {
  const base = usageResponse.value;
  if (!props.setupColab && !props.account.logged_in) {
    return null;
  }
  const win = windowSpec.value;
  const windowMatches = Boolean(
    base && base.start === win.queryStartSec && base.end === win.queryEndSec,
  );
  const curBal =
    typeof props.account.balance === "number" &&
    Number.isFinite(props.account.balance)
      ? props.account.balance
      : base?.current_balance;
  const curRate =
    typeof props.account.rate === "number" &&
    Number.isFinite(props.account.rate)
      ? props.account.rate
      : base?.current_rate;
  if (!base || !windowMatches) {
    return {
      logged_in: Boolean(props.account.logged_in || props.setupColab),
      bucket_seconds: base?.bucket_seconds || 1800,
      retention_days: base?.retention_days || 365,
      start: win.queryStartSec,
      end: win.queryEndSec,
      current_balance: curBal,
      current_rate: curRate,
      checked_at: props.account.checked_at,
      available_dates: cachedAvailableDates.value,
      active_dates: cachedActiveDates.value,
      buckets: [],
    };
  }
  return {
    ...base,
    logged_in: Boolean(base.logged_in || props.account.logged_in || props.setupColab),
    current_balance: curBal,
    current_rate: curRate,
    checked_at: props.account.checked_at ?? base.checked_at,
  };
});

const seriesData = computed(() =>
  aggregateColabOneCuSeries(
    windowSpec.value,
    mergedResponse.value,
    effectiveNowMs.value,
  ),
);

const points = computed(() => seriesData.value.points);
const summary = computed(() => seriesData.value.summary);

const chartLayout = computed(() =>
  buildColabOneCuChartLayout({
    points: points.value,
    metric: metric.value,
    width: plotWidth.value,
    height: PLOT_HEIGHT,
  }),
);

const showInitialSkeleton = computed(
  () =>
    Boolean(props.loading) ||
    (props.setupColab && !hasFetchedOnce.value && fetching.value),
);

const showLoggedOutState = computed(
  () => !showInitialSkeleton.value && !props.setupColab && !props.account.logged_in,
);

const headerBadgeText = computed(() => {
  const win = windowSpec.value;
  if (win.mode === "24h") return t("cuUsage.badgeLive24h");
  if (win.mode === "day") {
    return t("cuUsage.badgeDay", { date: win.selectedDateKey });
  }
  return t("cuUsage.badgeRange", { range: win.spanLabel });
});

const dateTriggerLabel = computed(() => {
  const win = windowSpec.value;
  if (win.mode === "24h") return t("cuUsage.dateTriggerLive24h");
  if (win.mode === "day") return win.selectedDateKey;
  return win.spanLabel;
});

const prevStepTooltip = computed(() =>
  rangeMode.value === "24h" || rangeMode.value === "day"
    ? t("cuUsage.prevDayTooltip")
    : t("cuUsage.prevPeriodTooltip"),
);

const nextStepTooltip = computed(() =>
  rangeMode.value === "24h" || rangeMode.value === "day"
    ? t("cuUsage.nextDayTooltip")
    : t("cuUsage.nextPeriodTooltip"),
);

const isDrillingSupported = computed(
  () => rangeMode.value === "7d" || rangeMode.value === "30d",
);

const hoveredPoint = computed<ColabOneCuUsagePoint | null>(() => {
  const idx = hoveredIndex.value;
  if (idx === null || idx < 0 || idx >= points.value.length) return null;
  return points.value[idx];
});

const hoverMarkerX = computed<number | null>(() => {
  const idx = hoveredIndex.value;
  if (idx === null || idx < 0 || idx >= points.value.length) return null;
  if (metric.value === "consumed") {
    return chartLayout.value.bars[idx]?.cx ?? null;
  }
  const pt = chartLayout.value.points.find((p) => p.index === idx);
  if (pt) return pt.cx;
  return (
    chartLayout.value.plotLeft + (idx + 0.5) * chartLayout.value.stepWidth
  );
});

const hoverMarkerY = computed<number | null>(() => {
  const idx = hoveredIndex.value;
  if (idx === null || idx < 0 || idx >= points.value.length) return null;
  const pt = chartLayout.value.points.find((p) => p.index === idx);
  return pt ? pt.cy : null;
});

const tooltipStyle = computed<CSSProperties>(() => {
  const x = hoverMarkerX.value;
  if (x === null) return { display: "none" };
  const layout = chartLayout.value;
  const pad = 8;
  const offset = 12;
  const estWidth = Math.min(248, Math.max(180, layout.width - pad * 2));
  const maxLeft = Math.max(pad, layout.width - estWidth - pad);
  let leftPx: number;
  if (x <= layout.width * 0.56 && x + offset + estWidth <= layout.width - pad) {
    leftPx = x + offset;
  } else if (x - offset - estWidth >= pad) {
    leftPx = x - offset - estWidth;
  } else {
    leftPx = Math.max(pad, Math.min(maxLeft, x - estWidth / 2));
  }
  return {
    left: `${Math.round(leftPx)}px`,
    top: "10px",
  };
});

const kpiConsumedSub = computed(() => {
  const s = summary.value;
  if (s.totalTopup > 0) {
    return t("cuUsage.kpiPeriodConsumedSubTopup", {
      amount: s.totalTopup.toFixed(2),
    });
  }
  if (!s.hasAnyConsumption) {
    return t("cuUsage.kpiPeriodConsumedSubEmpty");
  }
  return t("cuUsage.kpiPeriodConsumedSubHours");
});

const kpiAvgRateValue = computed(() => {
  const s = summary.value;
  if (s.activeAvgRate > 0 && s.totalActiveSeconds > 0) {
    return s.activeAvgRate.toFixed(2);
  }
  if (s.windowAvgRate > 0) return s.windowAvgRate.toFixed(2);
  return "0.00";
});

const kpiAvgRateSub = computed(() => {
  const s = summary.value;
  if (s.activeAvgRate > 0 && s.totalActiveSeconds > 0) {
    return t("cuUsage.kpiAvgRateSubActive");
  }
  if (s.windowAvgRate > 0) {
    return t("cuUsage.kpiAvgRateSubWindow", {
      rate: s.windowAvgRate.toFixed(2),
    });
  }
  if (!s.hasAnySample) {
    return t("common.emptyData");
  }
  return t("cuUsage.kpiAvgRateSubIdle");
});

const kpiPeakValue = computed(() => {
  const s = summary.value;
  return s.peakConsumed > 0 ? s.peakConsumed.toFixed(2) : "0.00";
});

const kpiPeakSub = computed(() => {
  const s = summary.value;
  if (s.peakConsumed <= 0) {
    return t("cuUsage.kpiPeakSubEmpty");
  }
  if (s.peakRate > 0) {
    return t("cuUsage.kpiPeakSubRate", {
      label: s.peakConsumedLabel,
      rate: s.peakRate.toFixed(2),
    });
  }
  return t("cuUsage.kpiPeakSubLabel", { label: s.peakConsumedLabel });
});

const kpiActiveDurationText = computed(() =>
  formatColabOneActiveDuration(summary.value.totalActiveSeconds),
);

const kpiActiveBalanceSub = computed(() => {
  const s = summary.value;
  if (s.startBalance !== null && s.endBalance !== null) {
    if (Math.abs(s.startBalance - s.endBalance) >= 0.01) {
      return t("cuUsage.kpiBalanceChangeSub", {
        start: s.startBalance.toFixed(2),
        end: s.endBalance.toFixed(2),
      });
    }
    return t("cuUsage.kpiEndBalanceSub", {
      balance: s.endBalance.toFixed(2),
    });
  }
  if (s.endBalance !== null) {
    return t("cuUsage.kpiEndBalanceSub", {
      balance: s.endBalance.toFixed(2),
    });
  }
  if (!windowSpec.value.isAtPresent) {
    return t("common.emptyData");
  }
  return t("cuUsage.kpiBalanceUnreadSub");
});

const emptyOverlayHint = computed<string>(() => {
  if (showInitialSkeleton.value || showLoggedOutState.value || fetching.value) {
    return "";
  }
  const s = summary.value;
  if (metric.value === "balance") {
    if (s.hasAnyBalance) return "";
  } else if (metric.value === "rate") {
    if (s.peakRate > 0 || s.hasAnyConsumption) return "";
  } else {
    if (s.hasAnyConsumption) return "";
  }
  if (!windowSpec.value.isAtPresent) {
    return t("cuUsage.emptyNoDataDay");
  }
  return t("cuUsage.emptyCollectingHint");
});

const retentionDays = computed(
  () => usageResponse.value?.retention_days || 365,
);

function detectLocalTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch {
    return "";
  }
}

async function fetchUsage(force = false) {
  if (props.loading || props.ipcDisconnected) return;
  if (!props.setupColab && !props.account.logged_in) {
    fetchSeq++;
    usageResponse.value = null;
    cachedAvailableDates.value = [];
    cachedActiveDates.value = [];
    lastAccountEmail = "";
    hasFetchedOnce.value = false;
    fetching.value = false;
    return;
  }
  const nextEmail = (props.account.email || "").trim().toLowerCase();
  if (nextEmail && lastAccountEmail && nextEmail !== lastAccountEmail) {
    usageResponse.value = null;
    cachedAvailableDates.value = [];
    cachedActiveDates.value = [];
  }
  if (nextEmail) {
    lastAccountEmail = nextEmail;
  }
  const reqId = ++fetchSeq;
  const win = windowSpec.value;
  const windowChanged =
    !usageResponse.value ||
    usageResponse.value.start !== win.queryStartSec ||
    usageResponse.value.end !== win.queryEndSec;
  if (force || !hasFetchedOnce.value || windowChanged) {
    fetching.value = true;
  }
  const payload: Record<string, unknown> = {
    start: win.queryStartSec,
    end: win.queryEndSec,
  };
  const tz = detectLocalTimezone();
  if (tz) {
    payload.timezone = tz;
  } else {
    payload.utc_offset_seconds = -new Date().getTimezoneOffset() * 60;
  }
  try {
    const res = await colabOneCommand("billing.usage", payload);
    if (reqId !== fetchSeq) return;
    if (res && typeof res === "object" && Array.isArray(res.buckets)) {
      const typed = res as ColabOneCuUsageResponse;
      usageResponse.value = typed;
      if (Array.isArray(typed.available_dates)) {
        cachedAvailableDates.value = typed.available_dates;
      }
      if (Array.isArray(typed.active_dates)) {
        cachedActiveDates.value = typed.active_dates;
      }
    }
    hasFetchedOnce.value = true;
  } catch {
    if (reqId !== fetchSeq) return;
    hasFetchedOnce.value = true;
  } finally {
    if (reqId === fetchSeq) {
      fetching.value = false;
    }
  }
}

function onRangeSegmentChange(val: string | number) {
  const next = String(val);
  hoveredIndex.value = null;
  minuteBucketNowMs.value = Date.now();
  const today = formatColabOneDateKey(new Date(minuteBucketNowMs.value));
  if (next === "24h") {
    rangeMode.value = "24h";
    selectedDateKey.value = today;
    anchorEndDateKey.value = today;
  } else if (next === "7d" || next === "30d") {
    rangeMode.value = next;
    anchorEndDateKey.value = today;
  }
}

function onMetricChange(val: string | number) {
  const next = String(val);
  if (next !== "consumed" && next !== "rate" && next !== "balance") return;
  metric.value = next;
  try {
    globalThis.localStorage?.setItem(COLABONE_STORAGE_KEYS.cuUsageMetric, next);
  } catch {
    // Ignore storage access errors.
  }
}

function selectCalendarDate(dateKey: string) {
  minuteBucketNowMs.value = Date.now();
  const today = windowSpec.value.todayDateKey;
  const clamped = clampColabOneDateKey(dateKey, today);
  hoveredIndex.value = null;
  selectedDateKey.value = clamped;
  anchorEndDateKey.value = clamped;
  rangeMode.value = "day";
  calendarOpen.value = false;
}

function jumpToCurrentTime() {
  hoveredIndex.value = null;
  const prevStart = windowSpec.value.queryStartSec;
  const prevEnd = windowSpec.value.queryEndSec;
  minuteBucketNowMs.value = Date.now();
  const today = formatColabOneDateKey(new Date(minuteBucketNowMs.value));
  rangeMode.value = "24h";
  selectedDateKey.value = today;
  anchorEndDateKey.value = today;
  calendarOpen.value = false;
  // Only trigger an explicit fetch when the window bounds did not change;
  // otherwise the reactive window watcher fires a single fetch automatically.
  if (
    windowSpec.value.queryStartSec === prevStart &&
    windowSpec.value.queryEndSec === prevEnd
  ) {
    void fetchUsage(false);
  }
}

function stepPrev() {
  hoveredIndex.value = null;
  minuteBucketNowMs.value = Date.now();
  const win = windowSpec.value;
  if (win.mode === "24h") {
    const yesterday = shiftColabOneDateKey(win.todayDateKey, -1);
    selectedDateKey.value = yesterday;
    anchorEndDateKey.value = yesterday;
    rangeMode.value = "day";
    return;
  }
  if (win.mode === "day") {
    const prev = shiftColabOneDateKey(win.selectedDateKey, -1);
    selectedDateKey.value = prev;
    anchorEndDateKey.value = prev;
    return;
  }
  const delta = win.mode === "30d" ? -30 : -7;
  anchorEndDateKey.value = shiftColabOneDateKey(win.anchorEndDateKey, delta);
}

function stepNext() {
  minuteBucketNowMs.value = Date.now();
  const win = windowSpec.value;
  if (win.isAtPresent) return;
  hoveredIndex.value = null;
  if (win.mode === "day") {
    const next = clampColabOneDateKey(
      shiftColabOneDateKey(win.selectedDateKey, 1),
      win.todayDateKey,
    );
    selectedDateKey.value = next;
    anchorEndDateKey.value = next;
    return;
  }
  const delta = win.mode === "30d" ? 30 : 7;
  anchorEndDateKey.value = clampColabOneDateKey(
    shiftColabOneDateKey(win.anchorEndDateKey, delta),
    win.todayDateKey,
  );
}

function syncCalendarCursor() {
  const key =
    rangeMode.value === "day"
      ? windowSpec.value.selectedDateKey
      : windowSpec.value.anchorEndDateKey;
  const parsed = parseColabOneDateKey(key);
  if (parsed) {
    calendarYear.value = parsed.year;
    calendarMonthIndex.value = parsed.monthIndex;
  }
}

function onCalendarOpenChange(open: boolean) {
  if (open) {
    minuteBucketNowMs.value = Date.now();
    syncCalendarCursor();
  }
}

function stepCalendarMonth(delta: number) {
  if (delta > 0 && !calendarMonth.value.canGoNextMonth) return;
  const next = new Date(calendarYear.value, calendarMonthIndex.value + delta, 1);
  if (next.getFullYear() < 2000) return;
  calendarYear.value = next.getFullYear();
  calendarMonthIndex.value = next.getMonth();
}

function onChartPointerMove(event: PointerEvent) {
  const el = plotContainerRef.value;
  if (!el || showInitialSkeleton.value || showLoggedOutState.value) return;
  const rect = el.getBoundingClientRect();
  if (rect.width <= 0) return;
  const relX =
    ((event.clientX - rect.left) / rect.width) * chartLayout.value.width;
  hoveredIndex.value = resolveColabOneCuHoverIndex(
    relX,
    chartLayout.value,
    points.value.length,
  );
}

function onChartPointerLeave() {
  hoveredIndex.value = null;
}

function onChartClick(event?: MouseEvent) {
  if (!isDrillingSupported.value) return;
  const el = plotContainerRef.value;
  if (el && event) {
    const rect = el.getBoundingClientRect();
    if (rect.width > 0) {
      const relX =
        ((event.clientX - rect.left) / rect.width) * chartLayout.value.width;
      const idx = resolveColabOneCuHoverIndex(
        relX,
        chartLayout.value,
        points.value.length,
      );
      if (idx !== null) {
        hoveredIndex.value = idx;
      }
    }
  }
  const pt = hoveredPoint.value;
  if (!pt || pt.isFuture) return;
  selectCalendarDate(pt.dateKey);
}

function onChartKeydown(event: KeyboardEvent) {
  const total = points.value.length;
  if (total === 0) return;
  if (event.key === "ArrowLeft") {
    event.preventDefault();
    hoveredIndex.value =
      hoveredIndex.value === null
        ? total - 1
        : Math.max(0, hoveredIndex.value - 1);
  } else if (event.key === "ArrowRight") {
    event.preventDefault();
    hoveredIndex.value =
      hoveredIndex.value === null
        ? 0
        : Math.min(total - 1, hoveredIndex.value + 1);
  } else if (event.key === "Escape") {
    hoveredIndex.value = null;
  } else if (event.key === "Enter" && isDrillingSupported.value) {
    onChartClick();
  }
}

function closePopoverOnBlur() {
  calendarOpen.value = false;
}

function onVisibilityChange() {
  if (!document.hidden && !props.ipcDisconnected) {
    minuteBucketNowMs.value = Date.now();
  }
}

function measurePlotWidth() {
  const el = plotContainerRef.value;
  if (!el) return;
  const w = Math.floor(el.getBoundingClientRect().width);
  if (w >= 240 && w !== plotWidth.value) {
    plotWidth.value = w;
  }
}

watch(
  [
    () => windowSpec.value.queryStartSec,
    () => windowSpec.value.queryEndSec,
    () => props.setupColab,
    () => props.account.logged_in,
    () => props.account.checked_at,
    () => props.account.email,
    () => props.loading,
    () => props.ipcDisconnected,
  ],
  () => {
    void fetchUsage(false);
  },
  { immediate: true },
);

watch(
  () => plotContainerRef.value,
  (el) => {
    resizeObserver?.disconnect();
    resizeObserver = null;
    if (!el) return;
    measurePlotWidth();
    if (typeof ResizeObserver !== "undefined") {
      resizeObserver = new ResizeObserver(() => {
        measurePlotWidth();
      });
      resizeObserver.observe(el);
    }
  },
  { flush: "post" },
);

onMounted(() => {
  measurePlotWidth();
  window.addEventListener("blur", closePopoverOnBlur);
  document.addEventListener("visibilitychange", onVisibilityChange);
  minuteTimer = setInterval(() => {
    if (!document.hidden && !props.ipcDisconnected) {
      minuteBucketNowMs.value = Date.now();
    }
  }, 60_000);
});

onBeforeUnmount(() => {
  resizeObserver?.disconnect();
  resizeObserver = null;
  window.removeEventListener("blur", closePopoverOnBlur);
  document.removeEventListener("visibilitychange", onVisibilityChange);
  if (minuteTimer !== undefined) {
    clearInterval(minuteTimer);
    minuteTimer = undefined;
  }
});
</script>

<template>
  <ACard
    class="co-cu-usage-card"
    :class="{
      'is-highlighted': highlighted,
      'is-skeleton': showInitialSkeleton,
    }"
    :bordered="true"
    size="small"
    :aria-label="t('cuUsage.sectionAria')"
    :aria-busy="showInitialSkeleton || undefined"
  >
    <template #title>
      <div class="co-cu-head-left">
        <span class="co-cu-title-icon" aria-hidden="true">
          <PhChartLineUp :size="16" weight="duotone" />
        </span>
        <span class="co-cu-title">{{ t("cuUsage.title") }}</span>
        <ColabOneSkeleton
          v-if="showInitialSkeleton"
          variant="pill"
          :width="116"
          :height="24"
        />
        <ATag
          v-else
          :bordered="true"
          class="co-cu-window-tag co-sk-reveal"
          :class="{ 'is-live': windowSpec.isLive && setupColab }"
        >
          <ABadge
            v-if="windowSpec.isLive && setupColab"
            status="processing"
            color="var(--co-accent)"
            class="co-cu-live-dot"
            aria-hidden="true"
          />
          <PhCalendarBlank v-else :size="12" aria-hidden="true" />
          <span>{{ headerBadgeText }}</span>
        </ATag>
      </div>
    </template>

    <template #extra>
      <div class="co-cu-head-right">
        <ASegmented
          :value="metric"
          :options="metricOptions"
          size="small"
          class="co-cu-metric-segmented"
          :aria-label="t('cuUsage.metricSwitchAria')"
          @change="onMetricChange"
        />
      </div>
    </template>

    <!-- Toolbar: Range Presets + Date Picker & Current Time Controls -->
    <div class="co-cu-toolbar">
      <div class="co-cu-toolbar-left">
        <ASegmented
          :value="activeSegmentedRange"
          :options="rangeOptions"
          size="small"
          class="co-cu-range-segmented"
          :aria-label="t('cuUsage.rangeSwitchAria')"
          @change="onRangeSegmentChange"
        />
      </div>

      <div class="co-cu-toolbar-right">
        <div class="co-cu-date-group">
          <ColabOneTooltip :title="prevStepTooltip" placement="top">
            <AButton
              size="small"
              class="co-cu-step-btn"
              :aria-label="prevStepTooltip"
              :disabled="showInitialSkeleton"
              @click="stepPrev"
            >
              <template #icon>
                <PhCaretLeft :size="13" weight="bold" />
              </template>
            </AButton>
          </ColabOneTooltip>

          <APopover
            v-model:open="calendarOpen"
            trigger="click"
            placement="bottomRight"
            overlay-class-name="co-cu-calendar-popover"
            :destroy-tooltip-on-hide="true"
            @open-change="onCalendarOpenChange"
          >
            <template #content>
              <div
                class="co-cu-calendar"
                role="dialog"
                :aria-label="t('cuUsage.pickDateAria')"
                @keydown.esc.stop="calendarOpen = false"
              >
                <div class="co-cu-cal-head">
                  <button
                    type="button"
                    class="co-cu-cal-nav-btn"
                    :aria-label="t('cuUsage.calendarPrevMonthAria')"
                    @click="stepCalendarMonth(-1)"
                  >
                    <PhCaretLeft :size="14" weight="bold" />
                  </button>
                  <span class="co-cu-cal-month">{{
                    calendarMonth.monthLabel
                  }}</span>
                  <button
                    type="button"
                    class="co-cu-cal-nav-btn"
                    :aria-label="t('cuUsage.calendarNextMonthAria')"
                    :disabled="!calendarMonth.canGoNextMonth"
                    @click="stepCalendarMonth(1)"
                  >
                    <PhCaretRight :size="14" weight="bold" />
                  </button>
                </div>

                <div class="co-cu-cal-weekdays" aria-hidden="true">
                  <span
                    v-for="(wd, idx) in calendarMonth.weekdays"
                    :key="`wd-${idx}`"
                    class="co-cu-cal-weekday"
                  >
                    {{ wd }}
                  </span>
                </div>

                <div class="co-cu-cal-grid">
                  <button
                    v-for="cell in calendarMonth.cells"
                    :key="cell.dateKey"
                    type="button"
                    class="co-cu-cal-day"
                    :class="{
                      'is-outside': !cell.inCurrentMonth,
                      'is-today': cell.isToday,
                      'is-selected': cell.isSelected,
                      'is-future': cell.isFuture,
                      'has-usage': cell.hasUsage,
                      'has-record': cell.hasRecord && !cell.hasUsage,
                    }"
                    :disabled="cell.isFuture"
                    :data-date="cell.dateKey"
                    :aria-current="cell.isToday ? 'date' : undefined"
                    :aria-pressed="cell.isSelected ? 'true' : 'false'"
                    :title="
                      cell.hasUsage
                        ? t('cuUsage.calendarDayHasUsage', {
                            date: cell.dateKey,
                          })
                        : cell.hasRecord
                          ? t('cuUsage.calendarDayHasRecord', {
                              date: cell.dateKey,
                            })
                          : cell.dateKey
                    "
                    @click="selectCalendarDate(cell.dateKey)"
                  >
                    <span class="co-cu-cal-day-num">{{ cell.day }}</span>
                    <span
                      v-if="cell.hasUsage || cell.hasRecord"
                      class="co-cu-cal-dot"
                      :class="cell.hasUsage ? 'is-usage' : 'is-record'"
                      aria-hidden="true"
                    ></span>
                  </button>
                </div>

                <div class="co-cu-cal-legend">
                  <span class="co-cu-cal-legend-item">
                    <i class="co-cu-cal-dot is-usage"></i>
                    <span>{{ t("cuUsage.calendarLegendUsage") }}</span>
                  </span>
                  <span class="co-cu-cal-legend-item">
                    <i class="co-cu-cal-dot is-record"></i>
                    <span>{{ t("cuUsage.calendarLegendRecord") }}</span>
                  </span>
                </div>

                <div class="co-cu-cal-footer">
                  <AButton
                    size="small"
                    class="co-cu-cal-action-btn"
                    @click="selectCalendarDate(windowSpec.todayDateKey)"
                  >
                    {{ t("cuUsage.calendarTodayBtn") }}
                  </AButton>
                  <AButton
                    size="small"
                    type="primary"
                    class="co-cu-cal-action-btn"
                    @click="jumpToCurrentTime"
                  >
                    {{ t("cuUsage.calendarNow24hBtn") }}
                  </AButton>
                </div>
              </div>
            </template>

            <AButton
              size="small"
              class="co-cu-date-trigger"
              :class="{ 'is-day-mode': rangeMode === 'day' }"
              :aria-label="t('cuUsage.pickDateAria')"
              :disabled="showInitialSkeleton"
              data-testid="cu-date-trigger"
            >
              <span class="co-cu-date-trigger-inner">
                <PhCalendarBlank :size="13" aria-hidden="true" />
                <span class="co-cu-date-trigger-text co-cu-mono">{{
                  dateTriggerLabel
                }}</span>
                <PhCaretDown :size="11" class="co-cu-date-caret" aria-hidden="true" />
              </span>
            </AButton>
          </APopover>

          <ColabOneTooltip :title="nextStepTooltip" placement="top">
            <AButton
              size="small"
              class="co-cu-step-btn"
              :aria-label="nextStepTooltip"
              :disabled="showInitialSkeleton || windowSpec.isAtPresent"
              @click="stepNext"
            >
              <template #icon>
                <PhCaretRight :size="13" weight="bold" />
              </template>
            </AButton>
          </ColabOneTooltip>
        </div>

        <ColabOneTooltip :title="t('cuUsage.currentTimeTooltip')" placement="top">
          <AButton
            size="small"
            :type="rangeMode !== '24h' ? 'primary' : 'default'"
            :ghost="rangeMode !== '24h'"
            class="co-cu-now-btn"
            :class="{ 'is-live': rangeMode === '24h' }"
            :disabled="showInitialSkeleton"
            data-testid="cu-now-btn"
            @click="jumpToCurrentTime"
          >
            <template #icon>
              <PhClockCounterClockwise :size="13" />
            </template>
            <span>{{
              rangeMode === "24h"
                ? t("cuUsage.currentTimeBtn")
                : t("cuUsage.backToNowBtn")
            }}</span>
          </AButton>
        </ColabOneTooltip>
      </div>
    </div>

    <!-- Logged-out prompt -->
    <div v-if="showLoggedOutState" class="co-cu-logged-out">
      <div class="co-cu-logged-out-icon" aria-hidden="true">
        <PhChartLineUp :size="24" weight="duotone" />
      </div>
      <div class="co-cu-logged-out-copy">
        <h4>{{ t("cuUsage.emptyLoggedOutTitle") }}</h4>
        <p>{{ t("cuUsage.emptyLoggedOutDesc") }}</p>
      </div>
      <AButton type="primary" size="small" @click="emit('openSettings')">
        {{ t("billing.cardSignInGoogle") }}
      </AButton>
    </div>

    <!-- Main KPIs + Interactive SVG Chart -->
    <template v-else>
      <div class="co-cu-kpi-strip">
        <div class="co-cu-kpi-cell">
          <span class="co-cu-kpi-label">
            <PhCoins :size="13" aria-hidden="true" />
            <span>{{ t("cuUsage.kpiPeriodConsumed") }}</span>
          </span>
          <template v-if="showInitialSkeleton">
            <ColabOneSkeleton variant="metric" :width="92" :height="22" />
            <ColabOneSkeleton variant="text" :width="124" :height="12" :delay="40" />
          </template>
          <template v-else>
            <div class="co-cu-kpi-val co-cu-mono co-sk-reveal">
              <span>{{ summary.totalConsumed.toFixed(2) }}</span>
              <em class="co-cu-kpi-unit">CU</em>
            </div>
            <small class="co-cu-kpi-sub co-sk-reveal" :title="kpiConsumedSub">{{
              kpiConsumedSub
            }}</small>
          </template>
        </div>

        <div class="co-cu-kpi-cell">
          <span class="co-cu-kpi-label">
            <PhFlame :size="13" aria-hidden="true" />
            <span>{{ t("cuUsage.kpiAvgRate") }}</span>
          </span>
          <template v-if="showInitialSkeleton">
            <ColabOneSkeleton variant="metric" :width="88" :height="22" :delay="60" />
            <ColabOneSkeleton variant="text" :width="116" :height="12" :delay="100" />
          </template>
          <template v-else>
            <div class="co-cu-kpi-val co-cu-mono co-sk-reveal">
              <span>{{ kpiAvgRateValue }}</span>
              <em class="co-cu-kpi-unit">{{ t("billing.cuPerHour") }}</em>
            </div>
            <small class="co-cu-kpi-sub co-sk-reveal" :title="kpiAvgRateSub">{{
              kpiAvgRateSub
            }}</small>
          </template>
        </div>

        <div class="co-cu-kpi-cell">
          <span class="co-cu-kpi-label">
            <PhTrendUp :size="13" aria-hidden="true" />
            <span>{{ t("cuUsage.kpiPeak") }}</span>
          </span>
          <template v-if="showInitialSkeleton">
            <ColabOneSkeleton variant="metric" :width="84" :height="22" :delay="120" />
            <ColabOneSkeleton variant="text" :width="126" :height="12" :delay="160" />
          </template>
          <template v-else>
            <div class="co-cu-kpi-val co-cu-mono co-sk-reveal">
              <span>{{ kpiPeakValue }}</span>
              <em class="co-cu-kpi-unit">CU</em>
            </div>
            <small class="co-cu-kpi-sub co-sk-reveal" :title="kpiPeakSub">{{
              kpiPeakSub
            }}</small>
          </template>
        </div>

        <div class="co-cu-kpi-cell">
          <span class="co-cu-kpi-label">
            <PhTimer :size="13" aria-hidden="true" />
            <span>{{ t("cuUsage.kpiActiveTime") }}</span>
          </span>
          <template v-if="showInitialSkeleton">
            <ColabOneSkeleton variant="metric" :width="96" :height="22" :delay="180" />
            <ColabOneSkeleton variant="text" :width="120" :height="12" :delay="220" />
          </template>
          <template v-else>
            <div class="co-cu-kpi-val co-cu-kpi-val-text co-cu-mono co-sk-reveal">
              <span>{{ kpiActiveDurationText }}</span>
            </div>
            <small
              class="co-cu-kpi-sub co-sk-reveal"
              :title="kpiActiveBalanceSub"
              >{{ kpiActiveBalanceSub }}</small
            >
          </template>
        </div>
      </div>

      <!-- Interactive SVG Plot Stage -->
      <div
        ref="plotContainerRef"
        class="co-cu-plot-stage"
        :class="[
          `is-metric-${metric}`,
          {
            'is-drillable': isDrillingSupported && hoveredPoint && !hoveredPoint.isFuture,
          },
        ]"
        tabindex="0"
        role="img"
        :aria-label="t('cuUsage.chartAria')"
        @pointermove="onChartPointerMove"
        @pointerleave="onChartPointerLeave"
        @click="onChartClick"
        @keydown="onChartKeydown"
      >
        <div v-if="showInitialSkeleton" class="co-cu-plot-skeleton">
          <ColabOneSkeleton variant="rect" width="100%" :height="186" :radius="10" />
        </div>

        <svg
          v-else
          class="co-cu-svg"
          :viewBox="`0 0 ${chartLayout.width} ${chartLayout.height}`"
          :height="chartLayout.height"
          width="100%"
          aria-hidden="true"
        >
          <defs>
            <linearGradient
              :id="`${uid}-bar-grad`"
              x1="0"
              y1="0"
              x2="0"
              y2="1"
            >
              <stop offset="0%" stop-color="var(--co-cu-bar)" stop-opacity="0.92" />
              <stop offset="100%" stop-color="var(--co-cu-bar)" stop-opacity="0.46" />
            </linearGradient>
            <linearGradient
              :id="`${uid}-bar-current-grad`"
              x1="0"
              y1="0"
              x2="0"
              y2="1"
            >
              <stop offset="0%" stop-color="var(--co-cu-bar-active)" stop-opacity="0.98" />
              <stop offset="100%" stop-color="var(--co-cu-bar-active)" stop-opacity="0.58" />
            </linearGradient>
            <linearGradient
              :id="`${uid}-area-rate`"
              x1="0"
              y1="0"
              x2="0"
              y2="1"
            >
              <stop offset="0%" stop-color="var(--co-cu-rate)" stop-opacity="0.28" />
              <stop offset="100%" stop-color="var(--co-cu-rate)" stop-opacity="0.02" />
            </linearGradient>
            <linearGradient
              :id="`${uid}-area-balance`"
              x1="0"
              y1="0"
              x2="0"
              y2="1"
            >
              <stop offset="0%" stop-color="var(--co-cu-balance)" stop-opacity="0.24" />
              <stop offset="100%" stop-color="var(--co-cu-balance)" stop-opacity="0.02" />
            </linearGradient>
          </defs>

          <!-- Horizontal gridlines & Y ticks -->
          <g class="co-cu-grid-group">
            <template v-for="(tick, idx) in chartLayout.yTicks" :key="`yt-${idx}`">
              <line
                :x1="chartLayout.plotLeft"
                :y1="tick.pos"
                :x2="chartLayout.plotRight"
                :y2="tick.pos"
                :class="[
                  'co-cu-grid-line',
                  { 'is-baseline': idx === 0 },
                ]"
              />
              <text
                :x="chartLayout.plotLeft - 8"
                :y="tick.pos + 3.5"
                text-anchor="end"
                class="co-cu-axis-text"
              >
                {{ tick.label }}
              </text>
            </template>
          </g>

          <!-- Future zone backdrop (when viewing Today before midnight) -->
          <template v-for="pt in points" :key="`fut-${pt.index}`">
            <rect
              v-if="pt.isFuture"
              :x="chartLayout.plotLeft + pt.index * chartLayout.stepWidth + 1"
              :y="chartLayout.plotTop"
              :width="Math.max(1, chartLayout.stepWidth - 2)"
              :height="chartLayout.plotHeight"
              class="co-cu-future-band"
            />
          </template>

          <!-- Hover column highlight & vertical crosshair -->
          <g v-if="hoveredIndex !== null && hoverMarkerX !== null">
            <rect
              :x="chartLayout.plotLeft + hoveredIndex * chartLayout.stepWidth"
              :y="chartLayout.plotTop"
              :width="chartLayout.stepWidth"
              :height="chartLayout.plotHeight"
              class="co-cu-hover-band"
            />
            <line
              :x1="hoverMarkerX"
              :y1="chartLayout.plotTop"
              :x2="hoverMarkerX"
              :y2="chartLayout.plotBottom"
              class="co-cu-crosshair"
            />
          </g>

          <!-- METRIC 1: Consumed CU (bars + smooth trend curve) -->
          <g v-if="metric === 'consumed'">
            <rect
              v-for="bar in chartLayout.bars"
              :key="`bar-${bar.index}`"
              :x="bar.x"
              :y="bar.y"
              :width="bar.width"
              :height="bar.height"
              :rx="Math.min(4, bar.width / 2)"
              :fill="
                bar.isCurrent
                  ? `url(#${uid}-bar-current-grad)`
                  : `url(#${uid}-bar-grad)`
              "
              class="co-cu-bar"
              :class="{
                'is-hovered': hoveredIndex === bar.index,
                'is-current': bar.isCurrent,
              }"
            />
            <path
              v-if="chartLayout.linePath && summary.hasAnyConsumption"
              :d="chartLayout.linePath"
              class="co-cu-trend-stroke"
            />
            <!-- Top-up badges along baseline -->
            <template v-for="bar in chartLayout.bars" :key="`tu-${bar.index}`">
              <circle
                v-if="bar.topup > 0"
                :cx="bar.cx"
                :cy="chartLayout.plotTop + 5"
                r="3.5"
                class="co-cu-topup-marker"
              />
            </template>
          </g>

          <!-- METRIC 2: Burn Rate (amber area + average line + optional peak dashed line) -->
          <g v-else-if="metric === 'rate'">
            <path
              v-if="chartLayout.areaPath && (summary.peakRate > 0 || summary.hasAnyConsumption)"
              :d="chartLayout.areaPath"
              :fill="`url(#${uid}-area-rate)`"
            />
            <path
              v-if="chartLayout.secondaryLinePath"
              :d="chartLayout.secondaryLinePath"
              class="co-cu-rate-peak-stroke"
            />
            <path
              v-if="chartLayout.linePath"
              :d="chartLayout.linePath"
              class="co-cu-rate-stroke"
            />
            <circle
              v-if="chartLayout.points.length === 1 && chartLayout.points[0].value > 0"
              :cx="chartLayout.points[0].cx"
              :cy="chartLayout.points[0].cy"
              r="3.8"
              class="co-cu-rate-dot"
            />
          </g>

          <!-- METRIC 3: Remaining Balance (emerald area + solid/dashed balance line) -->
          <g v-else>
            <path
              v-if="chartLayout.areaPath && summary.hasAnyBalance"
              :d="chartLayout.areaPath"
              :fill="`url(#${uid}-area-balance)`"
            />
            <path
              v-for="(seg, idx) in chartLayout.balanceSegments"
              :key="`bseg-${idx}`"
              :d="seg.path"
              class="co-cu-balance-stroke"
              :class="{ 'is-dashed': seg.dashed }"
            />
            <template v-for="pt in chartLayout.points" :key="`bpt-${pt.index}`">
              <circle
                v-if="pt.observed && (chartLayout.points.length <= 1 || pt.topup > 0 || pt.isCurrent)"
                :cx="pt.cx"
                :cy="pt.cy"
                :r="pt.topup > 0 ? 4.2 : 3.2"
                :class="pt.topup > 0 ? 'co-cu-topup-marker' : 'co-cu-balance-dot'"
              />
            </template>
          </g>

          <!-- Hovered data point focus ring -->
          <circle
            v-if="
              hoveredIndex !== null &&
              hoverMarkerX !== null &&
              hoverMarkerY !== null
            "
            :cx="hoverMarkerX"
            :cy="hoverMarkerY"
            r="4.5"
            class="co-cu-hover-dot"
          />

          <!-- X-axis labels -->
          <g class="co-cu-xaxis-group">
            <text
              v-for="xt in chartLayout.xTicks"
              :key="`xt-${xt.index}`"
              :x="xt.x"
              :y="chartLayout.height - 8"
              text-anchor="middle"
              class="co-cu-axis-text"
            >
              {{ xt.label }}
            </text>
          </g>
        </svg>

        <!-- Empty / Collecting status pill overlay -->
        <div
          v-if="emptyOverlayHint && hoveredIndex === null"
          class="co-cu-empty-pill"
          role="status"
        >
          <span>{{ emptyOverlayHint }}</span>
        </div>

        <!-- Floating Hover Tooltip -->
        <div
          v-if="hoveredPoint && !showInitialSkeleton"
          class="co-cu-tooltip"
          :style="tooltipStyle"
          role="status"
        >
          <div class="co-cu-tip-head">
            <span class="co-cu-tip-time co-cu-mono">{{
              hoveredPoint.tooltipRangeLabel
            }}</span>
            <span
              v-if="hoveredPoint.isCurrent"
              class="co-cu-tip-badge is-current"
              >{{ t("cuUsage.tooltipCurrentTag") }}</span
            >
            <span
              v-else-if="hoveredPoint.isFuture"
              class="co-cu-tip-badge is-future"
              >{{ t("cuUsage.tooltipFutureTag") }}</span
            >
            <span
              v-else-if="hoveredPoint.estimated"
              class="co-cu-tip-badge is-est"
              >{{ t("cuUsage.tooltipEstimatedTag") }}</span
            >
          </div>

          <div class="co-cu-tip-rows">
            <div class="co-cu-tip-row">
              <span>{{ t("cuUsage.tooltipConsumed") }}</span>
              <b class="co-cu-mono">{{ hoveredPoint.consumed.toFixed(2) }} CU</b>
            </div>
            <div class="co-cu-tip-row">
              <span>{{ t("cuUsage.tooltipRateAvg") }}</span>
              <b class="co-cu-mono">{{
                hoveredPoint.rateAvg > 0
                  ? `${hoveredPoint.rateAvg.toFixed(2)} ${t("billing.cuPerHour")}`
                  : "--"
              }}</b>
            </div>
            <div
              v-if="hoveredPoint.rateMax > hoveredPoint.rateAvg + 0.05"
              class="co-cu-tip-row"
            >
              <span>{{ t("cuUsage.tooltipRatePeak") }}</span>
              <b class="co-cu-mono">{{
                `${hoveredPoint.rateMax.toFixed(2)} ${t("billing.cuPerHour")}`
              }}</b>
            </div>
            <div v-if="hoveredPoint.balanceEnd !== null" class="co-cu-tip-row">
              <span>{{ t("cuUsage.tooltipBalance") }}</span>
              <b class="co-cu-mono">{{
                hoveredPoint.balanceStart !== null &&
                Math.abs(hoveredPoint.balanceStart - hoveredPoint.balanceEnd) >= 0.01
                  ? t("cuUsage.tooltipBalanceRange", {
                      start: hoveredPoint.balanceStart.toFixed(2),
                      end: hoveredPoint.balanceEnd.toFixed(2),
                    })
                  : `${hoveredPoint.balanceEnd.toFixed(2)} CU`
              }}</b>
            </div>
            <div v-if="hoveredPoint.activeSeconds > 0" class="co-cu-tip-row">
              <span>{{ t("cuUsage.tooltipActiveTime") }}</span>
              <b class="co-cu-mono">{{
                formatColabOneActiveDuration(hoveredPoint.activeSeconds)
              }}</b>
            </div>
            <div v-if="hoveredPoint.maxInstances > 0" class="co-cu-tip-row">
              <span>{{ t("cuUsage.tooltipMaxInstances") }}</span>
              <b class="co-cu-mono"
                >{{ hoveredPoint.maxInstances }}{{
                  t("common.unitInstances") ? ` ${t("common.unitInstances")}` : ""
                }}</b
              >
            </div>
            <div v-if="hoveredPoint.topup > 0" class="co-cu-tip-row is-topup">
              <span>{{ t("cuUsage.tooltipTopup") }}</span>
              <b class="co-cu-mono">+{{ hoveredPoint.topup.toFixed(2) }} CU</b>
            </div>
          </div>

          <div
            v-if="isDrillingSupported && !hoveredPoint.isFuture"
            class="co-cu-tip-drill"
          >
            {{ t("cuUsage.tooltipDrillHint") }}
          </div>
        </div>
      </div>

      <!-- Compact Footer Legend & Retention Caption -->
      <div class="co-cu-footer">
        <div class="co-cu-legend">
          <template v-if="metric === 'consumed'">
            <span class="co-cu-legend-item">
              <i class="co-cu-swatch is-bar"></i>
              <span>{{ t("cuUsage.legendBarConsumed") }}</span>
            </span>
            <span class="co-cu-legend-item">
              <i class="co-cu-swatch is-trend"></i>
              <span>{{ t("cuUsage.legendTrendLine") }}</span>
            </span>
          </template>
          <template v-else-if="metric === 'rate'">
            <span class="co-cu-legend-item">
              <i class="co-cu-swatch is-rate"></i>
              <span>{{ t("cuUsage.legendRateAvg") }}</span>
            </span>
            <span class="co-cu-legend-item">
              <i class="co-cu-swatch is-rate-peak"></i>
              <span>{{ t("cuUsage.legendRatePeak") }}</span>
            </span>
          </template>
          <template v-else>
            <span class="co-cu-legend-item">
              <i class="co-cu-swatch is-balance"></i>
              <span>{{ t("cuUsage.legendBalanceSolid") }}</span>
            </span>
            <span class="co-cu-legend-item">
              <i class="co-cu-swatch is-balance-carry"></i>
              <span>{{ t("cuUsage.legendBalanceCarry") }}</span>
            </span>
          </template>
        </div>

        <span class="co-cu-footer-meta co-cu-mono">
          {{ windowSpec.spanLabel }} ·
          {{ t("cuUsage.footerRetention", { days: retentionDays }) }}
        </span>
      </div>
    </template>
  </ACard>
</template>

<style scoped>
.co-cu-usage-card {
  --co-cu-bar: #3b82f6;
  --co-cu-bar-active: #2563eb;
  --co-cu-trend: #f59e0b;
  --co-cu-rate: #f59e0b;
  --co-cu-balance: #10b981;
  --co-cu-grid: rgba(15, 23, 42, 0.075);
  --co-cu-grid-base: rgba(15, 23, 42, 0.16);
  --co-cu-future-bg: rgba(15, 23, 42, 0.032);
  --co-cu-hover-band: rgba(59, 130, 246, 0.065);
  container-type: inline-size;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  border-radius: 16px;
  box-shadow: var(--co-card-shadow);
  color: var(--co-text);
  overflow: hidden;
  transition:
    box-shadow 0.28s ease,
    border-color 0.28s ease;
}

:global(:root[data-theme="dark"]) .co-cu-usage-card {
  --co-cu-bar: #60a5fa;
  --co-cu-bar-active: #3b82f6;
  --co-cu-trend: #fbbf24;
  --co-cu-rate: #fbbf24;
  --co-cu-balance: #34d399;
  --co-cu-grid: rgba(255, 255, 255, 0.07);
  --co-cu-grid-base: rgba(255, 255, 255, 0.16);
  --co-cu-future-bg: rgba(255, 255, 255, 0.025);
  --co-cu-hover-band: rgba(96, 165, 250, 0.09);
}

.co-cu-usage-card.is-highlighted {
  border-color: var(--co-accent);
  box-shadow:
    0 0 0 3px var(--co-focus-ring),
    var(--co-card-shadow-hover);
}

.co-cu-usage-card :deep(.ant-card-head) {
  min-height: 50px;
  padding: 0 22px;
  background: transparent;
  border-bottom: 1px solid var(--co-border-secondary);
}

.co-cu-usage-card :deep(.ant-card-head-wrapper) {
  gap: 10px;
  flex-wrap: wrap;
}

.co-cu-usage-card :deep(.ant-card-head-title) {
  padding: 10px 0;
}

.co-cu-usage-card :deep(.ant-card-extra) {
  padding: 8px 0;
}

.co-cu-usage-card :deep(.ant-card-body) {
  padding: 16px 22px 18px;
}

.co-cu-mono {
  font-family: var(--co-font-mono);
  font-variant-numeric: tabular-nums;
}

.co-cu-head-left {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  min-width: 0;
}

.co-cu-title-icon {
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

.co-cu-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
  letter-spacing: -0.1px;
}

.co-cu-window-tag {
  margin: 0;
  height: 24px;
  padding: 0 10px;
  border-radius: 9999px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  font-weight: 560;
  line-height: 1;
  background: var(--co-track);
  color: var(--co-secondary-text);
  border: 1px solid var(--co-border);
  font-variant-numeric: tabular-nums;
}

.co-cu-window-tag.is-live {
  background: var(--co-selected);
  color: var(--co-accent-strong);
  border-color: rgba(59, 130, 246, 0.28);
}

.co-cu-live-dot :deep(.ant-badge-status-processing::after) {
  border-color: var(--co-accent);
}

.co-cu-head-right {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.co-cu-metric-segmented,
.co-cu-range-segmented {
  background: var(--co-subpill-track);
  padding: 2px;
}

.co-cu-metric-segmented :deep(.ant-segmented-item-selected),
.co-cu-range-segmented :deep(.ant-segmented-item-selected) {
  background: var(--co-subpill-active-bg);
  color: var(--co-subpill-active-text);
  box-shadow: var(--co-subpill-active-shadow);
}

/* Toolbar */
.co-cu-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px 14px;
  margin-bottom: 14px;
}

.co-cu-toolbar-left,
.co-cu-toolbar-right {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.co-cu-date-group {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-cu-step-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  border-radius: 8px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--co-secondary-text);
  background: var(--co-subtle);
  border-color: var(--co-border);
}

.co-cu-step-btn:hover:not(:disabled) {
  color: var(--co-text);
  border-color: var(--co-input-border);
  background: var(--co-soft-hover);
}

.co-cu-date-trigger {
  height: 28px;
  padding: 0 10px;
  border-radius: 8px;
  background: var(--co-subtle);
  border-color: var(--co-border);
  color: var(--co-text);
  font-size: 12.5px;
  font-weight: 550;
}

.co-cu-date-trigger.is-day-mode {
  background: var(--co-selected);
  border-color: rgba(59, 130, 246, 0.35);
  color: var(--co-accent-strong);
}

.co-cu-date-trigger-inner {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-cu-date-caret {
  opacity: 0.68;
}

.co-cu-now-btn {
  height: 28px;
  padding: 0 10px;
  border-radius: 8px;
  font-size: 12.5px;
  font-weight: 560;
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.co-cu-now-btn.is-live {
  background: var(--co-subtle);
  border-color: var(--co-border);
  color: var(--co-secondary-text);
}

.co-cu-now-btn.is-live:hover:not(:disabled) {
  color: var(--co-accent-strong);
  border-color: rgba(59, 130, 246, 0.35);
  background: var(--co-selected);
}

/* Calendar Popover */
.co-cu-calendar {
  width: 264px;
  user-select: none;
  color: var(--co-text);
}

.co-cu-cal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}

.co-cu-cal-month {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-cu-cal-nav-btn {
  width: 26px;
  height: 26px;
  border-radius: 7px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--co-secondary-text);
  display: inline-grid;
  place-items: center;
  cursor: pointer;
  transition: background-color 0.14s ease, color 0.14s ease;
}

.co-cu-cal-nav-btn:hover:not(:disabled) {
  background: var(--co-soft-hover);
  color: var(--co-text);
}

.co-cu-cal-nav-btn:disabled {
  opacity: 0.32;
  cursor: not-allowed;
}

.co-cu-cal-weekdays {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  margin-bottom: 4px;
}

.co-cu-cal-weekday {
  text-align: center;
  font-size: 11.5px;
  font-weight: 550;
  color: var(--co-muted);
  padding: 2px 0;
}

.co-cu-cal-grid {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 2px;
}

.co-cu-cal-day {
  position: relative;
  height: 34px;
  border: 1px solid transparent;
  border-radius: 8px;
  background: transparent;
  color: var(--co-text);
  font-size: 12.5px;
  font-variant-numeric: tabular-nums;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  cursor: pointer;
  padding: 0;
  transition:
    background-color 0.14s ease,
    border-color 0.14s ease,
    color 0.14s ease;
}

.co-cu-cal-day:hover:not(:disabled) {
  background: var(--co-soft-hover);
}

.co-cu-cal-day.is-outside {
  color: var(--co-muted);
  opacity: 0.52;
}

.co-cu-cal-day.is-future {
  opacity: 0.28;
  cursor: not-allowed;
}

.co-cu-cal-day.is-today {
  border-color: rgba(59, 130, 246, 0.55);
  color: var(--co-accent-strong);
  font-weight: 600;
}

.co-cu-cal-day.is-selected {
  background: var(--co-accent);
  border-color: var(--co-accent);
  color: var(--co-on-accent);
  font-weight: 650;
  opacity: 1;
}

.co-cu-cal-day.is-selected:hover {
  background: var(--co-accent-hover);
}

.co-cu-cal-day-num {
  line-height: 1;
}

.co-cu-cal-dot {
  display: inline-block;
  width: 4.5px;
  height: 4.5px;
  border-radius: 9999px;
}

.co-cu-cal-dot.is-usage {
  background: var(--co-accent);
}

.co-cu-cal-dot.is-record {
  background: var(--co-muted);
  opacity: 0.65;
}

.co-cu-cal-day.is-selected .co-cu-cal-dot {
  background: #ffffff;
  opacity: 0.92;
}

.co-cu-cal-legend {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 14px;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--co-border-secondary);
  font-size: 11.5px;
  color: var(--co-muted);
}

.co-cu-cal-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.co-cu-cal-footer {
  display: grid;
  grid-template-columns: 1fr 1.35fr;
  gap: 8px;
  margin-top: 10px;
}

.co-cu-cal-action-btn {
  width: 100%;
  font-size: 12px;
  border-radius: 8px;
}

/* Logged out state */
.co-cu-logged-out {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  padding: 18px 16px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px dashed var(--co-border);
}

.co-cu-logged-out-icon {
  width: 42px;
  height: 42px;
  border-radius: 11px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.co-cu-logged-out-copy {
  flex: 1 1 260px;
  min-width: 0;
}

.co-cu-logged-out-copy h4 {
  margin: 0 0 3px;
  font-size: 14px;
  font-weight: 600;
  color: var(--co-text);
}

.co-cu-logged-out-copy p {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--co-muted);
}

/* KPI Strip */
.co-cu-kpi-strip {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px 0;
  padding: 12px 14px;
  margin-bottom: 14px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-cu-kpi-cell {
  min-width: 0;
  padding: 0 14px;
  border-left: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-cu-kpi-cell:first-child {
  padding-left: 2px;
  border-left: 0;
}

.co-cu-kpi-cell:last-child {
  padding-right: 2px;
}

.co-cu-kpi-label {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  font-weight: 500;
  color: var(--co-muted);
}

.co-cu-kpi-val {
  font-size: 20px;
  font-weight: 700;
  letter-spacing: -0.025em;
  line-height: 1.2;
  color: var(--co-text);
  display: flex;
  align-items: baseline;
  gap: 4px;
}

.co-cu-kpi-val-text {
  font-size: 18px;
}

.co-cu-kpi-unit {
  font-style: normal;
  font-size: 11.5px;
  font-weight: 500;
  color: var(--co-muted);
  letter-spacing: 0;
}

.co-cu-kpi-sub {
  font-size: 11.5px;
  line-height: 1.35;
  color: var(--co-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Plot Stage */
.co-cu-plot-stage {
  position: relative;
  width: 100%;
  min-height: 212px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  padding: 4px 0 0;
  outline: none;
  user-select: none;
}

.co-cu-plot-stage.is-drillable {
  cursor: pointer;
}

.co-cu-plot-stage:focus-visible {
  border-color: var(--co-focus);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-cu-plot-skeleton {
  padding: 12px 14px;
}

.co-cu-svg {
  display: block;
  width: 100%;
  overflow: visible;
}

.co-cu-grid-line {
  stroke: var(--co-cu-grid);
  stroke-width: 1;
  stroke-dasharray: 3 3;
}

.co-cu-grid-line.is-baseline {
  stroke: var(--co-cu-grid-base);
  stroke-dasharray: none;
}

.co-cu-axis-text {
  font-family: var(--co-font-mono);
  font-size: 11px;
  fill: var(--co-muted);
  font-variant-numeric: tabular-nums;
}

.co-cu-future-band {
  fill: var(--co-cu-future-bg);
}

.co-cu-hover-band {
  fill: var(--co-cu-hover-band);
}

.co-cu-crosshair {
  stroke: var(--co-accent);
  stroke-width: 1;
  stroke-dasharray: 3 3;
  opacity: 0.72;
}

.co-cu-bar {
  transition: opacity 0.14s ease;
}

.co-cu-bar.is-hovered {
  filter: brightness(1.08);
}

.co-cu-trend-stroke {
  fill: none;
  stroke: var(--co-cu-trend);
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
  opacity: 0.92;
}

.co-cu-rate-stroke {
  fill: none;
  stroke: var(--co-cu-rate);
  stroke-width: 2.2;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.co-cu-rate-peak-stroke {
  fill: none;
  stroke: var(--co-danger);
  stroke-width: 1.4;
  stroke-dasharray: 4 3;
  opacity: 0.75;
}

.co-cu-balance-stroke {
  fill: none;
  stroke: var(--co-cu-balance);
  stroke-width: 2.2;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.co-cu-balance-stroke.is-dashed {
  stroke-dasharray: 5 4;
  opacity: 0.62;
}

.co-cu-rate-dot {
  fill: var(--co-cu-rate);
  stroke: var(--co-surface);
  stroke-width: 1.5;
}

.co-cu-balance-dot {
  fill: var(--co-cu-balance);
  stroke: var(--co-surface);
  stroke-width: 1.5;
}

.co-cu-topup-marker {
  fill: var(--co-success);
  stroke: var(--co-surface);
  stroke-width: 1.6;
}

.co-cu-hover-dot {
  fill: var(--co-surface);
  stroke: var(--co-accent);
  stroke-width: 2.4;
}

.co-cu-plot-stage.is-metric-rate .co-cu-hover-dot {
  stroke: var(--co-cu-rate);
}

.co-cu-plot-stage.is-metric-balance .co-cu-hover-dot {
  stroke: var(--co-cu-balance);
}

/* Empty Overlay Pill */
.co-cu-empty-pill {
  position: absolute;
  left: 50%;
  top: 46%;
  transform: translate(-50%, -50%);
  max-width: calc(100% - 88px);
  padding: 6px 14px;
  border-radius: 9999px;
  background: var(--co-surface);
  border: 1px solid var(--co-border);
  box-shadow: var(--co-card-shadow);
  font-size: 12px;
  color: var(--co-muted);
  text-align: center;
  pointer-events: none;
}

/* Floating Tooltip */
.co-cu-tooltip {
  position: absolute;
  z-index: 6;
  min-width: 180px;
  max-width: min(256px, calc(100% - 16px));
  padding: 10px 12px;
  border-radius: 11px;
  background: var(--co-toast-bg);
  border: 1px solid var(--co-border);
  box-shadow: var(--co-toast-shadow);
  backdrop-filter: blur(8px);
  pointer-events: none;
  color: var(--co-text);
}

.co-cu-tip-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding-bottom: 6px;
  margin-bottom: 6px;
  border-bottom: 1px solid var(--co-border-secondary);
}

.co-cu-tip-time {
  font-size: 12px;
  font-weight: 600;
  color: var(--co-text);
}

.co-cu-tip-badge {
  font-size: 10.5px;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 9999px;
  white-space: nowrap;
}

.co-cu-tip-badge.is-current {
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

.co-cu-tip-badge.is-future {
  background: var(--co-track);
  color: var(--co-muted);
}

.co-cu-tip-badge.is-est {
  background: var(--co-warning-bg);
  color: var(--co-warning-text);
}

.co-cu-tip-rows {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
}

.co-cu-tip-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.co-cu-tip-row span {
  color: var(--co-muted);
}

.co-cu-tip-row b {
  font-weight: 600;
  color: var(--co-text);
}

.co-cu-tip-row.is-topup b {
  color: var(--co-success-text);
}

.co-cu-tip-drill {
  margin-top: 7px;
  padding-top: 6px;
  border-top: 1px solid var(--co-border-secondary);
  font-size: 11px;
  color: var(--co-accent-strong);
  font-weight: 550;
}

/* Footer */
.co-cu-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 16px;
  margin-top: 12px;
  font-size: 11.5px;
  color: var(--co-muted);
}

.co-cu-legend {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 14px;
}

.co-cu-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-cu-swatch {
  display: inline-block;
  flex-shrink: 0;
}

.co-cu-swatch.is-bar {
  width: 10px;
  height: 10px;
  border-radius: 3px;
  background: var(--co-cu-bar);
  opacity: 0.85;
}

.co-cu-swatch.is-trend {
  width: 14px;
  height: 2.5px;
  border-radius: 9999px;
  background: var(--co-cu-trend);
}

.co-cu-swatch.is-rate {
  width: 14px;
  height: 2.5px;
  border-radius: 9999px;
  background: var(--co-cu-rate);
}

.co-cu-swatch.is-rate-peak {
  width: 14px;
  height: 0;
  border-top: 2px dashed var(--co-danger);
}

.co-cu-swatch.is-balance {
  width: 14px;
  height: 2.5px;
  border-radius: 9999px;
  background: var(--co-cu-balance);
}

.co-cu-swatch.is-balance-carry {
  width: 14px;
  height: 0;
  border-top: 2px dashed var(--co-cu-balance);
  opacity: 0.7;
}

.co-cu-footer-meta {
  font-size: 11.5px;
  color: var(--co-muted);
}

@container (max-width: 780px) {
  .co-cu-kpi-strip {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px 0;
  }

  .co-cu-kpi-cell:nth-child(odd) {
    padding-left: 2px;
    border-left: 0;
  }

  .co-cu-kpi-cell:nth-child(even) {
    padding-left: 14px;
    border-left: 1px solid var(--co-border-secondary);
  }
}

@media (max-width: 640px) {
  .co-cu-usage-card :deep(.ant-card-head) {
    padding: 0 14px;
  }

  .co-cu-usage-card :deep(.ant-card-body) {
    padding: 14px;
  }

  .co-cu-head-right {
    flex-wrap: wrap;
    max-width: 100%;
  }

  .co-cu-toolbar {
    gap: 8px;
  }

  .co-cu-toolbar-left,
  .co-cu-toolbar-right {
    width: 100%;
    max-width: 100%;
  }

  .co-cu-kpi-strip {
    grid-template-columns: 1fr;
    gap: 10px;
  }

  .co-cu-kpi-cell,
  .co-cu-kpi-cell:nth-child(even) {
    padding: 0 0 10px;
    border-left: 0;
    border-bottom: 1px solid var(--co-border-secondary);
  }

  .co-cu-kpi-cell:last-child {
    padding-bottom: 0;
    border-bottom: 0;
  }
}
</style>
