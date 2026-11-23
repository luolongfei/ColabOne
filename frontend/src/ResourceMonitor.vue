<script setup lang="ts">
import {
  computed,
  nextTick,
  onMounted,
  onUnmounted,
  ref,
  shallowRef,
  watch,
} from "vue";
import {
  Badge as ABadge,
  Button as AButton,
  Card as ACard,
  Progress as AProgress,
  Segmented as ASegmented,
  Select as ASelect,
  SelectOption as ASelectOption,
  Tag as ATag,
} from "ant-design-vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { colabOneMonitorSkeletonState } from "./skeletonState";
import {
  ColabOneTelemetryTracker,
  buildColabOneChartGeometry,
  colabOneShortEndpoint,
  formatMetricBytes,
  formatRateBps,
  type ColabOneChartGeometry,
  type ColabOneTelemetry,
} from "./telemetry";
import { COLABONE_STORAGE_KEYS } from "./storage";
import { locationShort } from "./location";
import { t, tb } from "./i18n";
import FlagIcon from "./FlagIcon.vue";
import type { MonitorOption, MonitorSource, RuntimeStatus } from "./types";
import {
  PhArrowsClockwise,
  PhCpu,
  PhGraphicsCard,
  PhHardDrives,
  PhLightning,
  PhMemory,
  PhPause,
  PhPlay,
  PhRows,
  PhSquaresFour,
  PhThermometer,
} from "@phosphor-icons/vue";

const props = defineProps<{
  runtime: RuntimeStatus;
  driveConnected?: boolean;
  driveFreeBytes?: number | null;
  loadedModelName?: string;
  selectedGpu?: string;
  operationTitle?: string;
  ipcDisconnected?: boolean;
  monitorOptions?: MonitorOption[];
  monitorEndpoint?: string;
  monitorSource?: MonitorSource;
  monitorPaused?: boolean;
  monitorError?: string;
  monitorBusy?: boolean;
  loading?: boolean;
  switching?: boolean;
}>();

const emit = defineEmits<{
  (e: "select-instance", endpoint: string): void;
  (e: "toggle-pause", paused: boolean): void;
  (e: "retry-monitor"): void;
}>();

type LayoutMode = "grid" | "rows";
type MetricKey = "gpu" | "ram" | "disk" | "cpu";

const uid = `co-rm-${Math.random().toString(36).slice(2, 8)}`;

const layoutOptions = computed(() => [
  { value: "grid", payload: { label: t("monitor.layoutGrid") } },
  { value: "rows", payload: { label: t("monitor.layoutRows") } },
]);

function readStoredLayout(): LayoutMode {
  try {
    const raw = globalThis.localStorage?.getItem(
      COLABONE_STORAGE_KEYS.monitorLayout,
    );
    return raw === "rows" || raw === "split" ? "rows" : "grid";
  } catch {
    return "grid";
  }
}

const layout = ref<LayoutMode>(readStoredLayout());
const highlightedMetric = ref<MetricKey | null>(null);
let highlightTimer: ReturnType<typeof setTimeout> | undefined;

function clearHighlight(): void {
  if (highlightTimer !== undefined) {
    clearTimeout(highlightTimer);
    highlightTimer = undefined;
  }
  highlightedMetric.value = null;
}

function setLayout(mode: LayoutMode): void {
  layout.value = mode;
  if (mode !== "rows") {
    clearHighlight();
  }
  try {
    globalThis.localStorage?.setItem(COLABONE_STORAGE_KEYS.monitorLayout, mode);
  } catch {
    // Ignore storage access errors in restricted environments
  }
}

function onLayoutChange(value: string | number): void {
  clearHighlight();
  setLayout(value === "rows" ? "rows" : "grid");
}

function openMetricDetail(metric: MetricKey): void {
  setLayout("rows");
  clearHighlight();
  highlightedMetric.value = metric;
  highlightTimer = setTimeout(() => {
    highlightedMetric.value = null;
    highlightTimer = undefined;
  }, 1800);
  void nextTick(() => {
    const rowEl = document.getElementById(`${uid}-row-${metric}`);
    rowEl?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  });
}

const tracker = new ColabOneTelemetryTracker();
const nowTick = ref<number>(Date.now());
const telemetry = shallowRef<ColabOneTelemetry>(
  tracker.pushSnapshot(props.runtime, Date.now() / 1000),
);

let tickTimer: ReturnType<typeof setInterval> | undefined;

function tickTelemetry() {
  if (props.ipcDisconnected || isPaused.value || document.hidden) return;
  const nowMs = Date.now();
  nowTick.value = nowMs;
  if (props.runtime.state === "ready" || telemetry.value.online) {
    telemetry.value = tracker.pushSnapshot(props.runtime, nowMs / 1000);
  }
}

function handleVisibilityChange() {
  if (!document.hidden) {
    tickTelemetry();
  }
}

watch(
  () => props.runtime,
  (nextRuntime) => {
    const nowMs = Date.now();
    nowTick.value = nowMs;
    if (isPaused.value && nextRuntime.id && nextRuntime.id === tracker.activeId) {
      return;
    }
    telemetry.value = tracker.pushSnapshot(nextRuntime, nowMs / 1000);
  },
  { deep: true },
);

onMounted(() => {
  tickTimer = setInterval(tickTelemetry, 1000);
  document.addEventListener("visibilitychange", handleVisibilityChange);
});

onUnmounted(() => {
  clearHighlight();
  document.removeEventListener("visibilitychange", handleVisibilityChange);
  if (tickTimer !== undefined) {
    clearInterval(tickTimer);
    tickTimer = undefined;
  }
});

const GB = 1024 ** 3;

function formatBytePair(usedBytes: number, totalBytes: number): string {
  if (!Number.isFinite(totalBytes) || totalBytes <= 0) {
    return "-- / --";
  }
  const safeUsed = Number.isFinite(usedBytes) && usedBytes > 0 ? usedBytes : 0;
  if (totalBytes >= GB) {
    return `${(safeUsed / GB).toFixed(1)} / ${(totalBytes / GB).toFixed(1)} GB`;
  }
  return `${formatMetricBytes(safeUsed)} / ${formatMetricBytes(totalBytes)}`;
}

function seriesPeak(values: number[]): number {
  let max = 0;
  for (const v of values) {
    if (Number.isFinite(v) && v > max) max = v;
  }
  return max;
}

function seriesAverage(values: number[]): number {
  const active = values.filter((v) => Number.isFinite(v) && v > 0);
  if (!active.length) return 0;
  const sum = active.reduce((acc, v) => acc + v, 0);
  return sum / active.length;
}

const effectiveDriveConnected = computed<boolean>(() =>
  Boolean(props.driveConnected ?? props.runtime.drive?.connected),
);

const effectiveDriveFreeBytes = computed<number | null>(() => {
  const candidate = props.driveFreeBytes ?? props.runtime.drive?.free_bytes;
  return typeof candidate === "number" && Number.isFinite(candidate)
    ? candidate
    : null;
});

const monitorOptionList = computed<MonitorOption[]>(
  () => props.monitorOptions || [],
);

const selectedMonitorOption = computed<MonitorOption | null>(
  () =>
    monitorOptionList.value.find((opt) => opt.id === props.monitorEndpoint) ||
    null,
);

const effectiveSource = computed<MonitorSource>(() => {
  if (props.monitorSource) return props.monitorSource;
  return props.runtime.state !== "stopped" ? "local" : "none";
});

const isRemoteReadonly = computed<boolean>(
  () =>
    effectiveSource.value === "remote_readonly" ||
    effectiveSource.value === "remote_colabone",
);
const isRemoteColabOne = computed<boolean>(
  () => effectiveSource.value === "remote_colabone",
);
const isPaused = computed<boolean>(
  () => Boolean(props.monitorPaused && isRemoteReadonly.value),
);
const isStarting = computed<boolean>(() => props.runtime.state === "starting");
const isIpcPaused = computed<boolean>(() => Boolean(props.ipcDisconnected));
const isRemoteError = computed<boolean>(
  () => isRemoteReadonly.value && !isPaused.value && Boolean(props.monitorError),
);
const isRemoteConnecting = computed<boolean>(
  () =>
    isRemoteReadonly.value &&
    !isPaused.value &&
    !isRemoteError.value &&
    !telemetry.value.online &&
    props.runtime.state === "connecting",
);
const isConnecting = computed<boolean>(
  () => (isStarting.value || isRemoteConnecting.value) && !telemetry.value.online,
);
const monitorSk = computed(() =>
  colabOneMonitorSkeletonState({
    loading: props.loading,
    switching: props.switching,
    isConnecting: isConnecting.value,
    isPaused: isPaused.value,
    isRemoteError: isRemoteError.value,
    online: telemetry.value.online,
    hasRamTelemetry: telemetry.value.hasRamTelemetry,
    hasCpuTelemetry: telemetry.value.cpuPercent !== null,
  }),
);
const SK_GRID_WAVE_PATHS: Record<MetricKey, string> = {
  gpu: "M 0 54 C 42 52 72 34 114 38 C 156 42 184 22 228 28 C 266 33 292 20 320 24",
  ram: "M 0 46 C 48 44 82 40 128 36 C 176 32 214 34 260 28 C 288 25 304 27 320 26",
  disk: "M 0 56 C 54 55 96 48 142 46 C 188 44 234 40 278 38 C 296 37 310 38 320 37",
  cpu: "M 0 52 C 36 46 68 24 108 32 C 148 40 182 18 222 30 C 260 41 290 22 320 28",
};
const SK_ROW_WAVE_PATHS: Record<MetricKey, string> = {
  gpu: "M 0 36 C 95 34 165 22 260 26 C 355 30 435 14 530 19 C 615 23 670 15 720 17",
  ram: "M 0 32 C 110 30 195 27 305 24 C 415 21 510 23 615 19 C 668 17 698 19 720 18",
  disk: "M 0 38 C 120 37 220 32 330 31 C 440 30 555 27 650 25 C 685 24 705 25 720 24",
  cpu: "M 0 35 C 85 30 160 15 250 22 C 340 29 425 12 520 20 C 605 27 665 14 720 19",
};
const isCpuOnlyOnline = computed<boolean>(
  () =>
    telemetry.value.online &&
    telemetry.value.vramTotalBytes === 0 &&
    !telemetry.value.gpuName &&
    (selectedMonitorOption.value?.variant === "CPU" ||
      selectedMonitorOption.value?.accelerator === "CPU" ||
      isRemoteReadonly.value),
);

function formatMonitorTriggerLabel(opt: MonitorOption): string {
  const prefix = opt.managed
    ? t("monitor.sourceLocal")
    : opt.source === "remote_colabone"
      ? t("monitor.sourceRemoteColabOne")
      : t("monitor.sourceRemoteReadonly");
  const accel = opt.accelerator || opt.variant || "GPU";
  const loc = opt.location?.country ? locationShort(opt.location) : "";
  const shortId = colabOneShortEndpoint(opt.id, 14);
  return loc ? `${prefix} · ${accel} · ${loc}` : `${prefix} · ${accel} · ${shortId}`;
}

function onMonitorSelect(raw: unknown): void {
  const endpoint = String(raw ?? "").trim();
  if (!endpoint || endpoint === props.monitorEndpoint) return;
  emit("select-instance", endpoint);
}

const headerBadgeStatus = computed<
  "processing" | "warning" | "error" | "default"
>(() => {
  if (isIpcPaused.value || isConnecting.value) return "warning";
  if (isPaused.value) return "default";
  if (isRemoteError.value && !telemetry.value.online) return "error";
  if (telemetry.value.online) return "processing";
  if (props.runtime.state === "disconnected") return "error";
  return "default";
});

const headerBadgeText = computed<string>(() => {
  if (isIpcPaused.value) return t("monitor.badgeIpcPaused");
  if (isPaused.value) return t("monitor.badgePaused");
  if (isRemoteError.value && !telemetry.value.online) return t("monitor.badgeError");
  if (telemetry.value.online) {
    if (isStarting.value) return t("monitor.badgeStarting");
    if (isRemoteColabOne.value) return t("monitor.badgeRemoteColabOne");
    if (isRemoteReadonly.value) return t("monitor.badgeRemoteReadonly");
    return t("monitor.badgeLive");
  }
  if (isRemoteConnecting.value) return t("monitor.badgeRemoteConnecting");
  if (isConnecting.value) return t("monitor.badgeConnecting");
  if (props.runtime.state === "disconnected") return t("monitor.badgeDisconnected");
  return t("monitor.badgeStandby");
});

const onlineSubtitle = computed<string>(() => {
  if (isIpcPaused.value) {
    return t("monitor.subIpcPaused");
  }
  if (isPaused.value) {
    return t("monitor.subPaused");
  }
  if (isRemoteError.value) {
    return props.monitorError
      ? tb(props.monitorError)
      : t("monitor.subRemoteError");
  }
  if (isStarting.value) {
    return t("monitor.subStarting");
  }
  return t("monitor.subLive");
});

const offlineSubtitle = computed<string>(() => {
  if (isIpcPaused.value) {
    return t("monitor.subIpcPaused");
  }
  if (isPaused.value) {
    return t("monitor.subPaused");
  }
  if (isRemoteError.value) {
    return props.monitorError
      ? tb(props.monitorError)
      : t("monitor.subRemoteOfflineError");
  }
  if (isRemoteConnecting.value) {
    return t("monitor.subRemoteConnecting");
  }
  if (props.runtime.state === "starting") {
    return props.operationTitle
      ? t("monitor.subConnectingWithOp", { op: tb(props.operationTitle) })
      : t("monitor.subConnecting");
  }
  if (props.runtime.state === "disconnected") {
    return t("monitor.subDisconnected");
  }
  return t("monitor.subStandby");
});

// Compact grid (320x72) geometries
const GRID_W = 320;
const GRID_H = 72;
const GRID_Y_LINES = [GRID_H * 0.25, GRID_H * 0.5, GRID_H * 0.75];

const gpuGridChart = computed(() =>
  buildColabOneChartGeometry(
    telemetry.value.vramSeries,
    GRID_W,
    GRID_H,
    telemetry.value.sampleTick,
    100,
  ),
);

const gpuUtilGridChart = computed(() =>
  buildColabOneChartGeometry(
    telemetry.value.gpuUtilSeries,
    GRID_W,
    GRID_H,
    telemetry.value.sampleTick,
    100,
  ),
);

const ramGridChart = computed(() =>
  buildColabOneChartGeometry(
    telemetry.value.ramSeries,
    GRID_W,
    GRID_H,
    telemetry.value.sampleTick,
    100,
  ),
);

const diskPrimarySeries = computed<number[]>(() =>
  telemetry.value.hasDiskTotal
    ? telemetry.value.diskSeries
    : telemetry.value.diskIoSeries,
);

const diskPrimaryScale = computed<number>(() =>
  telemetry.value.hasDiskTotal ? 100 : 20,
);

const diskGridChart = computed(() =>
  buildColabOneChartGeometry(
    diskPrimarySeries.value,
    GRID_W,
    GRID_H,
    telemetry.value.sampleTick,
    diskPrimaryScale.value,
  ),
);

const diskIoGridChart = computed(() =>
  buildColabOneChartGeometry(
    telemetry.value.diskIoSeries,
    GRID_W,
    GRID_H,
    telemetry.value.sampleTick,
    20,
  ),
);

const hasDiskIoActivity = computed<boolean>(
  () =>
    telemetry.value.online &&
    telemetry.value.hasDiskTotal &&
    (telemetry.value.diskReadBps > 0 ||
      telemetry.value.diskWriteBps > 0 ||
      seriesPeak(telemetry.value.diskIoSeries) > 0),
);

const cpuGridChart = computed(() =>
  buildColabOneChartGeometry(
    telemetry.value.cpuSeries,
    GRID_W,
    GRID_H,
    telemetry.value.sampleTick,
    100,
  ),
);

// Compact wide 4-row stacked charts (720x48) for low-height simultaneous timeline comparison
const ROW_W = 720;
const ROW_H = 48;
const ROW_Y_LINES = [ROW_H / 3, (ROW_H * 2) / 3];

const gpuRowChart = computed(() =>
  buildColabOneChartGeometry(
    telemetry.value.vramSeries,
    ROW_W,
    ROW_H,
    telemetry.value.sampleTick,
    100,
  ),
);

const gpuUtilRowChart = computed(() =>
  telemetry.value.online && telemetry.value.gpuUtilPercent !== null
    ? buildColabOneChartGeometry(
        telemetry.value.gpuUtilSeries,
        ROW_W,
        ROW_H,
        telemetry.value.sampleTick,
        100,
      )
    : null,
);

const ramRowChart = computed(() =>
  buildColabOneChartGeometry(
    telemetry.value.ramSeries,
    ROW_W,
    ROW_H,
    telemetry.value.sampleTick,
    100,
  ),
);

const diskRowChart = computed(() =>
  buildColabOneChartGeometry(
    diskPrimarySeries.value,
    ROW_W,
    ROW_H,
    telemetry.value.sampleTick,
    diskPrimaryScale.value,
  ),
);

const diskIoRowChart = computed(() =>
  hasDiskIoActivity.value
    ? buildColabOneChartGeometry(
        telemetry.value.diskIoSeries,
        ROW_W,
        ROW_H,
        telemetry.value.sampleTick,
        20,
      )
    : null,
);

const cpuRowChart = computed(() =>
  buildColabOneChartGeometry(
    telemetry.value.cpuSeries,
    ROW_W,
    ROW_H,
    telemetry.value.sampleTick,
    100,
  ),
);

const activeGpuDisplayName = computed<string>(() =>
  telemetry.value.online
    ? tb(telemetry.value.gpuName || props.runtime.gpu?.name || "")
    : "",
);

const gpuSubtitle = computed<string>(() => {
  if (telemetry.value.online && telemetry.value.gpuName) {
    return tb(telemetry.value.gpuName);
  }
  if (telemetry.value.online && props.runtime.gpu?.name) {
    return tb(props.runtime.gpu.name);
  }
  if (isCpuOnlyOnline.value) return t("monitor.gpuNoAccelerator");
  if (telemetry.value.online) return t("device.fallbackGpuName");
  if (isRemoteConnecting.value) {
    const accel = selectedMonitorOption.value?.accelerator;
    return accel
      ? t("monitor.gpuConnectingAccel", { accel })
      : t("monitor.gpuConnectingGeneric");
  }
  if (isConnecting.value) {
    return props.selectedGpu
      ? t("monitor.gpuInitAccel", { gpu: props.selectedGpu })
      : t("monitor.gpuInitGeneric");
  }
  return t("monitor.gpuStandby");
});

const cpuSubtitle = computed<string>(() =>
  telemetry.value.online && telemetry.value.cpuCores
    ? t("monitor.cpuCores", { count: telemetry.value.cpuCores })
    : telemetry.value.online
      ? t("monitor.cpuRemoteCore")
      : isConnecting.value
        ? t("monitor.cpuInit")
        : t("monitor.cpuStandby"),
);

const gpuProgressPercent = computed<number>(() =>
  telemetry.value.online ? Math.round(telemetry.value.vramPercent) : 0,
);

const ramProgressPercent = computed<number>(() =>
  telemetry.value.online && telemetry.value.hasRamTelemetry
    ? Math.round(telemetry.value.ramPercent)
    : 0,
);

const diskProgressPercent = computed<number>(() =>
  telemetry.value.online && telemetry.value.hasDiskTotal
    ? Math.round(telemetry.value.diskPercent)
    : 0,
);

const cpuProgressPercent = computed<number>(() =>
  telemetry.value.online && telemetry.value.cpuPercent !== null
    ? Math.round(telemetry.value.cpuPercent)
    : 0,
);

interface StackedRowStat {
  label: string;
  value: string;
  muted?: boolean;
}

interface StackedRowItem {
  key: MetricKey;
  title: string;
  subtitle: string;
  icon: typeof PhGraphicsCard;
  heroValue: string;
  compactHero?: boolean;
  heroSub: string;
  progressPercent: number;
  progressColor: string;
  seriesLabel: string;
  secondaryLabel: string;
  scaleTop: string;
  showArea: boolean;
  primaryChart: ColabOneChartGeometry;
  secondaryChart: ColabOneChartGeometry | null;
  stats: StackedRowStat[];
  driveBadge?: string;
}

const gpuLivePercentLabel = computed<string>(() =>
  telemetry.value.online
    ? isCpuOnlyOnline.value
      ? "--"
      : `${telemetry.value.vramPercent.toFixed(1)}%`
    : isConnecting.value
      ? "--"
      : "0.0%",
);

const ramLivePercentLabel = computed<string>(() =>
  !telemetry.value.online
    ? isConnecting.value
      ? "--"
      : "0.0%"
    : telemetry.value.hasRamTelemetry
      ? `${telemetry.value.ramPercent.toFixed(1)}%`
      : "--",
);

const diskLivePercentLabel = computed<string>(() =>
  !telemetry.value.online
    ? isConnecting.value
      ? "--"
      : "0.0%"
    : telemetry.value.hasDiskTotal
      ? `${telemetry.value.diskPercent.toFixed(1)}%`
      : formatRateBps(
          telemetry.value.diskReadBps + telemetry.value.diskWriteBps,
        ),
);

const cpuLivePercentLabel = computed<string>(() =>
  !telemetry.value.online
    ? isConnecting.value
      ? "--"
      : "0.0%"
    : telemetry.value.cpuPercent !== null
      ? `${telemetry.value.cpuPercent.toFixed(1)}%`
      : "--",
);

const stackedRows = computed<StackedRowItem[]>(() => {
  const tel = telemetry.value;

  const gpuStats: StackedRowStat[] = [];
  if (tel.online && tel.vramTotalBytes > 0) {
    const freeVram = Math.max(0, tel.vramTotalBytes - tel.vramUsedBytes);
    gpuStats.push({
      label: t("monitor.statFree"),
      value: formatMetricBytes(freeVram),
    });
  }
  gpuStats.push({
    label: t("monitor.statCoreLoad"),
    value:
      tel.online && tel.gpuUtilPercent !== null
        ? t("monitor.statPeakValue", {
            cur: Math.round(tel.gpuUtilPercent),
            peak: Math.round(seriesPeak(tel.gpuUtilSeries)),
          })
        : tel.online
          ? t("monitor.statUnreported")
          : "--",
  });
  gpuStats.push({
    label: t("monitor.statTemp"),
    value: tel.online && tel.gpuTempC !== null ? `${Math.round(tel.gpuTempC)}°C` : "--",
  });
  if (tel.online && tel.gpuPowerW !== null) {
    gpuStats.push({
      label: t("monitor.statPower"),
      value:
        tel.gpuPowerLimitW !== null && tel.gpuPowerLimitW > 0
          ? `${Math.round(tel.gpuPowerW)} / ${Math.round(tel.gpuPowerLimitW)} W`
          : `${Math.round(tel.gpuPowerW)} W`,
    });
  }
  if (!isRemoteReadonly.value || isRemoteColabOne.value || props.loadedModelName || props.runtime.loaded_model) {
    gpuStats.push({
      label: t("monitor.statLoadedModel"),
      value: tel.online
        ? props.loadedModelName ||
          props.runtime.loaded_model ||
          t("monitor.statNoModel")
        : "--",
    });
  } else {
    gpuStats.push({
      label: t("monitor.statSource"),
      value: t("monitor.sourceRemoteReadonly"),
    });
  }

  const ramStats: StackedRowStat[] = !tel.online
    ? [
        { label: t("monitor.statAvailableRam"), value: "--" },
        { label: t("monitor.statPeak60s"), value: isConnecting.value ? "--" : "0%" },
      ]
    : !tel.hasRamTelemetry
      ? [{ label: t("monitor.statReportStatus"), value: t("monitor.statWaitingReport"), muted: true }]
      : [
          {
            label: t("monitor.statAvailableRam"),
            value: formatMetricBytes(tel.ramAvailableBytes),
          },
          {
            label: t("monitor.statAvg60s"),
            value: `${seriesAverage(tel.ramSeries).toFixed(1)}%`,
          },
          {
            label: t("monitor.statPeak60s"),
            value: `${Math.round(seriesPeak(tel.ramSeries))}%`,
          },
        ];

  const diskStats: StackedRowStat[] = [
    {
      label: !tel.online || tel.hasDiskTotal ? t("monitor.statUsedTotal") : t("monitor.statFreeSpace"),
      value: !tel.online
        ? "-- / --"
        : tel.hasDiskTotal
          ? formatBytePair(tel.diskUsedBytes, tel.diskTotalBytes)
          : formatMetricBytes(tel.diskFreeBytes),
    },
    {
      label: t("monitor.statRead"),
      value: tel.online ? formatRateBps(tel.diskReadBps) : "0 KB/s",
    },
    {
      label: t("monitor.statWrite"),
      value: tel.online ? formatRateBps(tel.diskWriteBps) : "0 KB/s",
    },
  ];

  const cpuStats: StackedRowStat[] = !tel.online
    ? [
        { label: t("monitor.statAvg60s"), value: isConnecting.value ? "--" : "0.0%" },
        { label: t("monitor.statPeak60s"), value: isConnecting.value ? "--" : "0%" },
      ]
    : tel.cpuPercent === null
      ? [{ label: t("monitor.statReportStatus"), value: t("monitor.statWaitingReport"), muted: true }]
      : [
          {
            label: t("monitor.statAvg60s"),
            value: `${seriesAverage(tel.cpuSeries).toFixed(1)}%`,
          },
          {
            label: t("monitor.statPeak60s"),
            value: `${Math.round(seriesPeak(tel.cpuSeries))}%`,
          },
        ];

  return [
    {
      key: "gpu",
      title: t("monitor.gpuTitle"),
      subtitle: gpuSubtitle.value,
      icon: PhGraphicsCard,
      heroValue: tel.online
        ? isCpuOnlyOnline.value
          ? "--"
          : `${tel.vramPercent.toFixed(1)}%`
        : isConnecting.value
          ? "--"
          : "0.0%",
      heroSub:
        tel.online && tel.vramTotalBytes > 0
          ? formatBytePair(tel.vramUsedBytes, tel.vramTotalBytes)
          : "-- / --",
      progressPercent: gpuProgressPercent.value,
      progressColor: "var(--co-rm-gpu)",
      seriesLabel: t("monitor.gpuSeriesVram"),
      secondaryLabel:
        tel.online && tel.gpuUtilPercent !== null ? t("monitor.gpuSeriesUtil") : "",
      scaleTop: gpuLivePercentLabel.value,
      showArea: tel.online,
      primaryChart: gpuRowChart.value,
      secondaryChart: gpuUtilRowChart.value,
      stats: gpuStats,
    },
    {
      key: "ram",
      title: t("monitor.ramTitle"),
      subtitle: tel.hasRamTelemetry
        ? t("monitor.ramTotal", { size: formatMetricBytes(tel.ramTotalBytes) })
        : isConnecting.value
          ? t("monitor.ramWaiting")
          : t("monitor.ramStandby"),
      icon: PhMemory,
      heroValue: !tel.online
        ? isConnecting.value
          ? "--"
          : "0.0%"
        : tel.hasRamTelemetry
          ? `${tel.ramPercent.toFixed(1)}%`
          : "--",
      heroSub:
        tel.online && tel.hasRamTelemetry
          ? formatBytePair(tel.ramUsedBytes, tel.ramTotalBytes)
          : "-- / --",
      progressPercent: ramProgressPercent.value,
      progressColor: "var(--co-rm-ram)",
      seriesLabel: t("monitor.ramSeries"),
      secondaryLabel: "",
      scaleTop: ramLivePercentLabel.value,
      showArea: tel.online && tel.hasRamTelemetry,
      primaryChart: ramRowChart.value,
      secondaryChart: null,
      stats: ramStats,
    },
    {
      key: "disk",
      title: t("monitor.diskTitle"),
      subtitle: tel.hasDiskTotal
        ? t("monitor.diskVolume", { size: formatMetricBytes(tel.diskTotalBytes) })
        : isConnecting.value
          ? t("monitor.diskWaiting")
          : t("monitor.diskStandby"),
      icon: PhHardDrives,
      heroValue: !tel.online
        ? isConnecting.value
          ? "--"
          : "0.0%"
        : tel.hasDiskTotal
          ? `${tel.diskPercent.toFixed(1)}%`
          : tel.diskFreeBytes > 0
            ? formatMetricBytes(tel.diskFreeBytes)
            : "--",
      compactHero: tel.online && !tel.hasDiskTotal && tel.diskFreeBytes > 0,
      heroSub: !tel.online
        ? "-- / --"
        : tel.hasDiskTotal
          ? t("monitor.diskFree", { size: formatMetricBytes(tel.diskFreeBytes) })
          : t("monitor.statFreeSpace"),
      progressPercent: diskProgressPercent.value,
      progressColor: "var(--co-rm-disk)",
      seriesLabel:
        !tel.online || tel.hasDiskTotal
          ? t("monitor.diskSeriesUsage")
          : t("monitor.diskSeriesIo"),
      secondaryLabel: hasDiskIoActivity.value ? t("monitor.diskIoLegend") : "",
      scaleTop: diskLivePercentLabel.value,
      showArea: tel.online,
      primaryChart: diskRowChart.value,
      secondaryChart: diskIoRowChart.value,
      stats: diskStats,
      driveBadge: effectiveDriveConnected.value
        ? effectiveDriveFreeBytes.value !== null &&
          effectiveDriveFreeBytes.value > 0
          ? t("monitor.driveFree", { size: formatMetricBytes(effectiveDriveFreeBytes.value) })
          : t("monitor.driveMounted")
        : "",
    },
    {
      key: "cpu",
      title: t("monitor.cpuTitle"),
      subtitle: cpuSubtitle.value,
      icon: PhCpu,
      heroValue: !tel.online
        ? isConnecting.value
          ? "--"
          : "0.0%"
        : tel.cpuPercent !== null
          ? `${tel.cpuPercent.toFixed(1)}%`
          : "--",
      heroSub: tel.online
        ? tel.cpuCores
          ? `${tel.cpuCores} vCPU`
          : t("monitor.cpuRemoteCore")
        : "-- vCPU",
      progressPercent: cpuProgressPercent.value,
      progressColor: "var(--co-rm-cpu)",
      seriesLabel: t("monitor.cpuSeries"),
      secondaryLabel: "",
      scaleTop: cpuLivePercentLabel.value,
      showArea: tel.online && tel.cpuPercent !== null,
      primaryChart: cpuRowChart.value,
      secondaryChart: null,
      stats: cpuStats,
    },
  ];
});
</script>

<template>
  <ACard
    class="co-monitor"
    :class="{
      'is-offline': !telemetry.online,
      'is-connecting': isConnecting || monitorSk.connecting,
      'is-skeleton': monitorSk.panels,
    }"
    :bordered="true"
    size="small"
    :aria-label="t('monitor.sectionAria')"
    :aria-busy="monitorSk.panels || undefined"
  >
    <!-- Monitor Top Header -->
    <template #title>
      <div class="co-rm-header-title">
        <div class="co-rm-heading-row">
          <div class="co-rm-heading-primary">
            <h3>{{ t("monitor.title") }}</h3>
            <ColabOneSkeleton
              v-if="monitorSk.initial"
              variant="pill"
              :width="88"
              :height="24"
            />
            <ABadge
              v-else
              :status="headerBadgeStatus"
              :text="headerBadgeText"
              class="co-rm-live-badge"
              :class="{
                live: telemetry.online && !isIpcPaused && !isPaused,
                remote: isRemoteReadonly && telemetry.online && !isPaused && !isIpcPaused,
                paused: isPaused,
                error: isRemoteError && !telemetry.online,
                connecting: isConnecting || isIpcPaused,
              }"
            />
          </div>
          <div
            v-if="monitorSk.headerPills"
            class="co-rm-hw-pills co-rm-hw-pills-sk"
            role="status"
          >
            <ColabOneSkeleton variant="pill" :width="118" :height="24" :delay="0" />
            <ColabOneSkeleton variant="pill" :width="56" :height="24" :delay="80" />
            <ColabOneSkeleton variant="pill" :width="74" :height="24" :delay="160" />
            <span class="co-sr-only">{{ offlineSubtitle }}</span>
          </div>
          <div v-else-if="telemetry.online" class="co-rm-hw-pills co-sk-reveal">
            <ATag
              v-if="activeGpuDisplayName"
              :bordered="false"
              class="co-rm-hw-tag co-rm-hw-gpu"
            >
              <PhGraphicsCard :size="12" aria-hidden="true" />
              <span>{{ activeGpuDisplayName }}</span>
            </ATag>
            <ColabOneTooltip
              v-if="telemetry.gpuTempC !== null"
              :title="t('monitor.gpuTempTooltip')"
              placement="top"
            >
              <ATag
                :bordered="false"
                :class="[
                  'co-rm-hw-tag',
                  telemetry.gpuTempC >= 80
                    ? 'co-rm-hw-hot'
                    : telemetry.gpuTempC >= 68
                      ? 'co-rm-hw-warm'
                      : 'co-rm-hw-cool',
                ]"
              >
                <PhThermometer :size="12" aria-hidden="true" />
                <span class="co-rm-mono">{{ Math.round(telemetry.gpuTempC) }}°C</span>
              </ATag>
            </ColabOneTooltip>
            <ColabOneTooltip
              v-if="telemetry.gpuPowerW !== null"
              :title="t('monitor.gpuPowerTooltip')"
              placement="top"
            >
              <ATag :bordered="false" class="co-rm-hw-tag co-rm-hw-power">
                <PhLightning :size="12" weight="fill" aria-hidden="true" />
                <span class="co-rm-mono">{{
                  telemetry.gpuPowerLimitW !== null &&
                  telemetry.gpuPowerLimitW > 0
                    ? `${Math.round(telemetry.gpuPowerW)} / ${Math.round(telemetry.gpuPowerLimitW)} W`
                    : `${Math.round(telemetry.gpuPowerW)} W`
                }}</span>
              </ATag>
            </ColabOneTooltip>
          </div>
          <ColabOneSkeleton
            v-if="monitorSk.initial"
            variant="text"
            :width="180"
            :height="13"
            :delay="120"
          />
          <span v-else-if="!telemetry.online" class="co-rm-header-sub">
            {{ offlineSubtitle }}
          </span>
          <span
            v-else-if="isIpcPaused || isStarting || isPaused || isRemoteError"
            class="co-rm-header-sub"
          >
            {{ onlineSubtitle }}
          </span>
          <AButton
            v-if="isRemoteError"
            type="link"
            size="small"
            class="co-rm-retry-btn"
            :disabled="Boolean(monitorBusy || isIpcPaused)"
            @click="emit('retry-monitor')"
          >
            <template #icon>
              <PhArrowsClockwise :size="13" />
            </template>
            {{ t("common.retry") }}
          </AButton>
        </div>
      </div>
    </template>

    <template #extra>
      <div class="co-rm-header-controls">
        <ASelect
          v-if="monitorOptionList.length > 0"
          size="small"
          class="co-rm-instance-select"
          :aria-label="t('monitor.selectTargetAria')"
          :value="monitorEndpoint || undefined"
          :disabled="Boolean(monitorBusy || isIpcPaused)"
          :dropdown-match-select-width="false"
          option-label-prop="label"
          @change="onMonitorSelect"
        >
          <ASelectOption
            v-for="opt in monitorOptionList"
            :key="opt.id"
            :value="opt.id"
            :label="formatMonitorTriggerLabel(opt)"
          >
            <div class="co-rm-opt-row">
              <span
                class="co-rm-opt-tag"
                :class="
                  opt.managed
                    ? 'is-local'
                    : opt.source === 'remote_colabone'
                      ? 'is-remote-co'
                      : 'is-remote'
                "
              >
                {{
                  opt.managed
                    ? opt.active
                      ? t("monitor.sourceLocalCurrent")
                      : t("monitor.sourceLocal")
                    : opt.source === "remote_colabone"
                      ? t("monitor.sourceRemoteColabOne")
                      : t("monitor.sourceRemoteReadonly")
                }}
              </span>
              <span class="co-rm-opt-accel">{{
                opt.accelerator || opt.variant || "GPU"
              }}</span>
              <span v-if="opt.location?.country" class="co-rm-opt-loc">
                <FlagIcon :code="opt.location.country" :size="12" />
                <span>{{ locationShort(opt.location) }}</span>
              </span>
              <code class="co-rm-opt-id">{{ opt.id }}</code>
              <span v-if="opt.loaded_model" class="co-rm-opt-model">{{
                opt.loaded_model
              }}</span>
            </div>
          </ASelectOption>
        </ASelect>

        <ColabOneTooltip
          v-if="isRemoteReadonly && monitorOptionList.length > 0"
          :title="
            isPaused
              ? t('monitor.resumeTooltip')
              : t('monitor.pauseTooltip')
          "
          placement="top"
        >
          <AButton
            size="small"
            class="co-rm-pause-btn"
            :class="{ 'is-paused': isPaused }"
            :disabled="Boolean(monitorBusy || isIpcPaused)"
            :aria-label="isPaused ? t('monitor.resumeAria') : t('monitor.pauseAria')"
            @click="emit('toggle-pause', !isPaused)"
          >
            <template #icon>
              <PhPlay v-if="isPaused" :size="12" weight="fill" />
              <PhPause v-else :size="12" weight="fill" />
            </template>
            <span>{{ isPaused ? t("monitor.resumeBtn") : t("monitor.pauseBtn") }}</span>
          </AButton>
        </ColabOneTooltip>

        <ASegmented
          :value="layout"
          :options="layoutOptions"
          size="small"
          class="co-rm-layout-segmented"
          :aria-label="t('monitor.layoutSwitchAria')"
          @change="onLayoutChange"
        >
          <template #label="{ value, payload }">
            <ColabOneTooltip :title="payload.label" placement="top">
              <span class="co-rm-seg-label">
                <PhSquaresFour
                  v-if="value === 'grid'"
                  :size="15"
                  aria-hidden="true"
                />
                <PhRows v-else :size="15" aria-hidden="true" />
                <span class="co-rm-sr-only">{{ payload.label }}</span>
              </span>
            </ColabOneTooltip>
          </template>
        </ASegmented>
      </div>
    </template>

    <!-- MODE 1: 2x2 ColabOne Resource Monitor Grid -->
    <div v-if="layout === 'grid'" class="co-rm-grid">
      <!-- 1. GPU VRAM & Core -->
      <ACard
        hoverable
        size="small"
        :bordered="true"
        class="co-rm-card co-metric-gpu"
        role="button"
        tabindex="0"
        :aria-label="t('monitor.gpuDetailAria')"
        @click="openMetricDetail('gpu')"
        @keydown.enter.prevent="openMetricDetail('gpu')"
        @keydown.space.prevent="openMetricDetail('gpu')"
      >
        <div class="co-rm-card-head">
          <div class="co-rm-card-title-group">
            <span class="co-rm-metric-icon" aria-hidden="true">
              <PhGraphicsCard :size="16" />
            </span>
            <div class="co-rm-card-titles">
              <h4>{{ t("monitor.gpuTitle") }}</h4>
              <ColabOneSkeleton
                v-if="monitorSk.initial"
                variant="text"
                :width="92"
                :height="12"
                class="co-rm-sub-sk"
              />
              <small v-else>{{ gpuSubtitle }}</small>
            </div>
          </div>
          <ColabOneSkeleton
            v-if="monitorSk.panels"
            variant="metric"
            :width="44"
            :height="19"
            :delay="0"
          />
          <strong v-else class="co-rm-live-val co-rm-mono co-sk-reveal">
            {{
              telemetry.online
                ? isCpuOnlyOnline
                  ? "--"
                  : `${Math.round(telemetry.vramPercent)}%`
                : isConnecting
                  ? "--"
                  : "0%"
            }}
          </strong>
        </div>

        <ColabOneSkeleton
          v-if="monitorSk.panels"
          variant="bar"
          width="100%"
          :height="6"
          :delay="0"
          class="co-rm-sk-progress"
        />
        <AProgress
          v-else
          :percent="gpuProgressPercent"
          :show-info="false"
          size="small"
          stroke-color="var(--co-rm-gpu)"
          trail-color="var(--co-subtle)"
          class="co-rm-metric-progress"
        />

        <div class="co-rm-plot-frame">
          <svg
            class="co-rm-svg"
            :viewBox="`0 0 ${GRID_W} ${GRID_H}`"
            preserveAspectRatio="none"
            aria-hidden="true"
          >
            <defs>
              <linearGradient
                :id="`${uid}-grid-gpu`"
                x1="0"
                y1="0"
                x2="0"
                y2="1"
              >
                <stop
                  offset="0%"
                  stop-color="var(--co-rm-gpu)"
                  stop-opacity="0.28"
                />
                <stop
                  offset="100%"
                  stop-color="var(--co-rm-gpu)"
                  stop-opacity="0.02"
                />
              </linearGradient>
            </defs>
            <line
              v-for="y in GRID_Y_LINES"
              :key="`gy-${y}`"
              x1="0"
              :y1="y"
              :x2="GRID_W"
              :y2="y"
              class="co-rm-grid-line"
            />
            <line
              v-for="(x, idx) in gpuGridChart.gridXOffsets"
              :key="`gx-${idx}`"
              :x1="x"
              y1="0"
              :x2="x"
              :y2="GRID_H"
              class="co-rm-grid-line"
            />
            <template v-if="monitorSk.panels">
              <path
                :d="`${SK_GRID_WAVE_PATHS.gpu} L ${GRID_W} ${GRID_H} L 0 ${GRID_H} Z`"
                :fill="`url(#${uid}-grid-gpu)`"
                class="co-rm-sk-area"
              />
              <path :d="SK_GRID_WAVE_PATHS.gpu" class="co-rm-sk-wave" />
            </template>
            <template v-else>
              <path
                v-if="telemetry.online"
                :d="gpuGridChart.areaPath"
                :fill="`url(#${uid}-grid-gpu)`"
              />
              <path
                v-if="telemetry.online && telemetry.gpuUtilPercent !== null"
                :d="gpuUtilGridChart.linePath"
                class="co-rm-secondary-stroke"
              />
              <path :d="gpuGridChart.linePath" class="co-rm-primary-stroke" />
            </template>
          </svg>
          <span v-if="monitorSk.panels" class="co-rm-scale-cap co-rm-scale-cap-sk">
            <ColabOneSkeleton variant="text" :width="30" :height="11" :delay="40" />
          </span>
          <span v-else class="co-rm-scale-cap co-rm-mono">{{ gpuLivePercentLabel }}</span>
        </div>

        <div class="co-rm-axis-row co-rm-mono" aria-hidden="true">
          <span>{{ t("monitor.axis60sAgo") }}</span>
          <span>{{ t("monitor.axisNow") }}</span>
        </div>

        <div class="co-rm-card-footer">
          <template v-if="monitorSk.panels">
            <span class="co-rm-kv">
              <em>{{ t("monitor.statUsedTotalShort") }}</em>
              <ColabOneSkeleton variant="text" :width="68" :height="12" :delay="40" inline />
            </span>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statCoreLoad") }}</em>
              <ColabOneSkeleton variant="text" :width="28" :height="12" :delay="100" inline />
            </span>
          </template>
          <template v-else>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statUsedTotalShort") }}</em>
              <b class="co-rm-mono">{{
                telemetry.online && telemetry.vramTotalBytes > 0
                  ? formatBytePair(
                      telemetry.vramUsedBytes,
                      telemetry.vramTotalBytes,
                    )
                  : "-- / --"
              }}</b>
            </span>
            <span v-if="telemetry.gpuUtilPercent !== null" class="co-rm-kv">
              <em>{{ t("monitor.statCoreLoad") }}</em>
              <b class="co-rm-mono">{{ Math.round(telemetry.gpuUtilPercent) }}%</b>
            </span>
            <span v-if="telemetry.gpuTempC !== null" class="co-rm-kv">
              <em>{{ t("monitor.statTemp") }}</em>
              <b class="co-rm-mono">{{ Math.round(telemetry.gpuTempC) }}°C</b>
            </span>
          </template>
        </div>
      </ACard>

      <!-- 2. System Memory (RAM) -->
      <ACard
        hoverable
        size="small"
        :bordered="true"
        class="co-rm-card co-metric-ram"
        role="button"
        tabindex="0"
        :aria-label="t('monitor.ramDetailAria')"
        @click="openMetricDetail('ram')"
        @keydown.enter.prevent="openMetricDetail('ram')"
        @keydown.space.prevent="openMetricDetail('ram')"
      >
        <div class="co-rm-card-head">
          <div class="co-rm-card-title-group">
            <span class="co-rm-metric-icon" aria-hidden="true">
              <PhMemory :size="16" />
            </span>
            <div class="co-rm-card-titles">
              <h4>{{ t("monitor.ramTitle") }}</h4>
              <ColabOneSkeleton
                v-if="monitorSk.initial"
                variant="text"
                :width="84"
                :height="12"
                :delay="80"
                class="co-rm-sub-sk"
              />
              <small v-else>{{
                telemetry.hasRamTelemetry
                  ? t("monitor.ramTotal", { size: formatMetricBytes(telemetry.ramTotalBytes) })
                  : isConnecting
                    ? t("monitor.ramWaiting")
                    : t("monitor.ramStandby")
              }}</small>
            </div>
          </div>
          <ColabOneSkeleton
            v-if="monitorSk.panels || monitorSk.ramPending"
            variant="metric"
            :width="44"
            :height="19"
            :delay="80"
          />
          <strong v-else class="co-rm-live-val co-rm-mono co-sk-reveal">
            {{
              !telemetry.online
                ? isConnecting
                  ? "--"
                  : "0%"
                : telemetry.hasRamTelemetry
                  ? `${Math.round(telemetry.ramPercent)}%`
                  : "--"
            }}
          </strong>
        </div>

        <ColabOneSkeleton
          v-if="monitorSk.panels"
          variant="bar"
          width="100%"
          :height="6"
          :delay="80"
          class="co-rm-sk-progress"
        />
        <AProgress
          v-else
          :percent="ramProgressPercent"
          :show-info="false"
          size="small"
          stroke-color="var(--co-rm-ram)"
          trail-color="var(--co-subtle)"
          class="co-rm-metric-progress"
        />

        <div class="co-rm-plot-frame">
          <svg
            class="co-rm-svg"
            :viewBox="`0 0 ${GRID_W} ${GRID_H}`"
            preserveAspectRatio="none"
            aria-hidden="true"
          >
            <defs>
              <linearGradient
                :id="`${uid}-grid-ram`"
                x1="0"
                y1="0"
                x2="0"
                y2="1"
              >
                <stop
                  offset="0%"
                  stop-color="var(--co-rm-ram)"
                  stop-opacity="0.28"
                />
                <stop
                  offset="100%"
                  stop-color="var(--co-rm-ram)"
                  stop-opacity="0.02"
                />
              </linearGradient>
            </defs>
            <line
              v-for="y in GRID_Y_LINES"
              :key="`ry-${y}`"
              x1="0"
              :y1="y"
              :x2="GRID_W"
              :y2="y"
              class="co-rm-grid-line"
            />
            <line
              v-for="(x, idx) in ramGridChart.gridXOffsets"
              :key="`rx-${idx}`"
              :x1="x"
              y1="0"
              :x2="x"
              :y2="GRID_H"
              class="co-rm-grid-line"
            />
            <template v-if="monitorSk.panels">
              <path
                :d="`${SK_GRID_WAVE_PATHS.ram} L ${GRID_W} ${GRID_H} L 0 ${GRID_H} Z`"
                :fill="`url(#${uid}-grid-ram)`"
                class="co-rm-sk-area"
              />
              <path :d="SK_GRID_WAVE_PATHS.ram" class="co-rm-sk-wave" />
            </template>
            <template v-else>
              <path
                v-if="telemetry.online && telemetry.hasRamTelemetry"
                :d="ramGridChart.areaPath"
                :fill="`url(#${uid}-grid-ram)`"
              />
              <path :d="ramGridChart.linePath" class="co-rm-primary-stroke" />
            </template>
          </svg>
          <span
            v-if="monitorSk.panels || monitorSk.ramPending"
            class="co-rm-scale-cap co-rm-scale-cap-sk"
          >
            <ColabOneSkeleton variant="text" :width="30" :height="11" :delay="120" />
          </span>
          <span v-else class="co-rm-scale-cap co-rm-mono">{{ ramLivePercentLabel }}</span>
        </div>

        <div class="co-rm-axis-row co-rm-mono" aria-hidden="true">
          <span>{{ t("monitor.axis60sAgo") }}</span>
          <span>{{ t("monitor.axisNow") }}</span>
        </div>

        <div class="co-rm-card-footer">
          <template v-if="monitorSk.panels">
            <span class="co-rm-kv">
              <em>{{ t("monitor.statInUse") }}</em>
              <ColabOneSkeleton variant="text" :width="68" :height="12" :delay="120" inline />
            </span>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statAvailableRam") }}</em>
              <ColabOneSkeleton variant="text" :width="42" :height="12" :delay="180" inline />
            </span>
          </template>
          <template v-else-if="telemetry.online && telemetry.hasRamTelemetry">
            <span class="co-rm-kv">
              <em>{{ t("monitor.statInUse") }}</em>
              <b class="co-rm-mono">{{
                formatBytePair(telemetry.ramUsedBytes, telemetry.ramTotalBytes)
              }}</b>
            </span>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statAvailableRam") }}</em>
              <b class="co-rm-mono">{{
                formatMetricBytes(telemetry.ramAvailableBytes)
              }}</b>
            </span>
          </template>
          <span
            v-else-if="telemetry.online && !telemetry.hasRamTelemetry"
            class="co-rm-kv"
          >
            <template v-if="monitorSk.ramPending">
              <em>{{ t("monitor.statInUse") }}</em>
              <ColabOneSkeleton variant="text" :width="68" :height="12" :delay="80" inline />
              <span class="co-sr-only">{{ t("monitor.statWaitingReport") }}</span>
            </template>
            <span v-else class="co-rm-waiting-note">
              {{ t("monitor.statWaitingReport") }}
            </span>
          </span>
          <template v-else>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statInUse") }}</em>
              <b class="co-rm-mono">-- / --</b>
            </span>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statAvailableRam") }}</em>
              <b class="co-rm-mono">--</b>
            </span>
          </template>
        </div>
      </ACard>

      <!-- 3. Remote Disk & I/O Throughput -->
      <ACard
        hoverable
        size="small"
        :bordered="true"
        class="co-rm-card co-metric-disk"
        role="button"
        tabindex="0"
        :aria-label="t('monitor.diskDetailAria')"
        @click="openMetricDetail('disk')"
        @keydown.enter.prevent="openMetricDetail('disk')"
        @keydown.space.prevent="openMetricDetail('disk')"
      >
        <div class="co-rm-card-head">
          <div class="co-rm-card-title-group">
            <span class="co-rm-metric-icon" aria-hidden="true">
              <PhHardDrives :size="16" />
            </span>
            <div class="co-rm-card-titles">
              <h4>{{ t("monitor.diskTitle") }}</h4>
              <ColabOneSkeleton
                v-if="monitorSk.initial"
                variant="text"
                :width="88"
                :height="12"
                :delay="160"
                class="co-rm-sub-sk"
              />
              <small v-else>{{
                telemetry.online && telemetry.hasDiskTotal
                  ? t("monitor.diskUsedPct", { pct: Math.round(telemetry.diskPercent) })
                  : isConnecting
                    ? t("monitor.diskWaiting")
                    : t("monitor.diskStandby")
              }}</small>
            </div>
          </div>
          <ColabOneSkeleton
            v-if="monitorSk.panels"
            variant="metric"
            :width="56"
            :height="19"
            :delay="160"
          />
          <strong v-else class="co-rm-live-val co-rm-disk-val co-rm-mono co-sk-reveal">
            {{
              telemetry.online && telemetry.diskFreeBytes > 0
                ? t("monitor.diskFree", { size: formatMetricBytes(telemetry.diskFreeBytes) })
                : telemetry.online && telemetry.hasDiskTotal
                  ? `${Math.round(telemetry.diskPercent)}%`
                  : isConnecting
                    ? "--"
                    : "0%"
            }}
          </strong>
        </div>

        <ColabOneSkeleton
          v-if="monitorSk.panels"
          variant="bar"
          width="100%"
          :height="6"
          :delay="160"
          class="co-rm-sk-progress"
        />
        <AProgress
          v-else
          :percent="diskProgressPercent"
          :show-info="false"
          size="small"
          stroke-color="var(--co-rm-disk)"
          trail-color="var(--co-subtle)"
          class="co-rm-metric-progress"
        />

        <div class="co-rm-plot-frame">
          <svg
            class="co-rm-svg"
            :viewBox="`0 0 ${GRID_W} ${GRID_H}`"
            preserveAspectRatio="none"
            aria-hidden="true"
          >
            <defs>
              <linearGradient
                :id="`${uid}-grid-disk`"
                x1="0"
                y1="0"
                x2="0"
                y2="1"
              >
                <stop
                  offset="0%"
                  stop-color="var(--co-rm-disk)"
                  stop-opacity="0.28"
                />
                <stop
                  offset="100%"
                  stop-color="var(--co-rm-disk)"
                  stop-opacity="0.02"
                />
              </linearGradient>
            </defs>
            <line
              v-for="y in GRID_Y_LINES"
              :key="`dy-${y}`"
              x1="0"
              :y1="y"
              :x2="GRID_W"
              :y2="y"
              class="co-rm-grid-line"
            />
            <line
              v-for="(x, idx) in diskGridChart.gridXOffsets"
              :key="`dx-${idx}`"
              :x1="x"
              y1="0"
              :x2="x"
              :y2="GRID_H"
              class="co-rm-grid-line"
            />
            <template v-if="monitorSk.panels">
              <path
                :d="`${SK_GRID_WAVE_PATHS.disk} L ${GRID_W} ${GRID_H} L 0 ${GRID_H} Z`"
                :fill="`url(#${uid}-grid-disk)`"
                class="co-rm-sk-area"
              />
              <path :d="SK_GRID_WAVE_PATHS.disk" class="co-rm-sk-wave" />
            </template>
            <template v-else>
              <path
                v-if="telemetry.online"
                :d="diskGridChart.areaPath"
                :fill="`url(#${uid}-grid-disk)`"
              />
              <path
                v-if="hasDiskIoActivity"
                :d="diskIoGridChart.linePath"
                class="co-rm-secondary-stroke"
              />
              <path :d="diskGridChart.linePath" class="co-rm-primary-stroke" />
            </template>
          </svg>
          <span v-if="monitorSk.panels" class="co-rm-scale-cap co-rm-scale-cap-sk">
            <ColabOneSkeleton variant="text" :width="30" :height="11" :delay="200" />
          </span>
          <span v-else class="co-rm-scale-cap co-rm-mono">{{ diskLivePercentLabel }}</span>
        </div>

        <div class="co-rm-axis-row co-rm-mono" aria-hidden="true">
          <span>{{ t("monitor.axis60sAgo") }}</span>
          <span>{{ t("monitor.axisNow") }}</span>
        </div>

        <div class="co-rm-card-footer">
          <template v-if="monitorSk.panels">
            <span class="co-rm-kv">
              <em>{{ t("monitor.statUsedTotal") }}</em>
              <ColabOneSkeleton variant="text" :width="72" :height="12" :delay="200" inline />
            </span>
            <ColabOneSkeleton variant="text" :width="84" :height="12" :delay="240" inline />
          </template>
          <template v-else>
            <span v-if="telemetry.online && telemetry.hasDiskTotal" class="co-rm-kv">
              <em>{{ t("monitor.statUsedTotal") }}</em>
              <b class="co-rm-mono">{{
                formatBytePair(telemetry.diskUsedBytes, telemetry.diskTotalBytes)
              }}</b>
            </span>
            <span
              v-else-if="telemetry.online && telemetry.diskFreeBytes > 0"
              class="co-rm-kv"
            >
              <em>{{ t("monitor.statFreeSpace") }}</em>
              <b class="co-rm-mono">{{ formatMetricBytes(telemetry.diskFreeBytes) }}</b>
            </span>
            <span v-else class="co-rm-kv">
              <em>{{ t("monitor.statUsedTotal") }}</em>
              <b class="co-rm-mono">-- / --</b>
            </span>

            <span class="co-rm-kv">
              <em class="co-rm-mono">{{
                t("monitor.statReadWriteSummary", {
                  read: formatRateBps(telemetry.diskReadBps),
                  write: formatRateBps(telemetry.diskWriteBps),
                })
              }}</em>
            </span>

            <ATag
              v-if="effectiveDriveConnected"
              :bordered="false"
              class="co-rm-drive-badge co-rm-mono"
            >
              {{
                effectiveDriveFreeBytes !== null && effectiveDriveFreeBytes > 0
                  ? t("monitor.driveFree", {
                      size: formatMetricBytes(effectiveDriveFreeBytes),
                    })
                  : t("monitor.driveMounted")
              }}
            </ATag>
          </template>
        </div>
      </ACard>

      <!-- 4. Processor (CPU) -->
      <ACard
        hoverable
        size="small"
        :bordered="true"
        class="co-rm-card co-metric-cpu"
        role="button"
        tabindex="0"
        :aria-label="t('monitor.cpuDetailAria')"
        @click="openMetricDetail('cpu')"
        @keydown.enter.prevent="openMetricDetail('cpu')"
        @keydown.space.prevent="openMetricDetail('cpu')"
      >
        <div class="co-rm-card-head">
          <div class="co-rm-card-title-group">
            <span class="co-rm-metric-icon" aria-hidden="true">
              <PhCpu :size="16" />
            </span>
            <div class="co-rm-card-titles">
              <h4>{{ t("monitor.cpuTitle") }}</h4>
              <ColabOneSkeleton
                v-if="monitorSk.initial"
                variant="text"
                :width="82"
                :height="12"
                :delay="240"
                class="co-rm-sub-sk"
              />
              <small v-else>{{ cpuSubtitle }}</small>
            </div>
          </div>
          <ColabOneSkeleton
            v-if="monitorSk.panels || monitorSk.cpuPending"
            variant="metric"
            :width="44"
            :height="19"
            :delay="240"
          />
          <strong v-else class="co-rm-live-val co-rm-mono co-sk-reveal">
            {{
              !telemetry.online
                ? isConnecting
                  ? "--"
                  : "0%"
                : telemetry.cpuPercent !== null
                  ? `${Math.round(telemetry.cpuPercent)}%`
                  : "--"
            }}
          </strong>
        </div>

        <ColabOneSkeleton
          v-if="monitorSk.panels"
          variant="bar"
          width="100%"
          :height="6"
          :delay="240"
          class="co-rm-sk-progress"
        />
        <AProgress
          v-else
          :percent="cpuProgressPercent"
          :show-info="false"
          size="small"
          stroke-color="var(--co-rm-cpu)"
          trail-color="var(--co-subtle)"
          class="co-rm-metric-progress"
        />

        <div class="co-rm-plot-frame">
          <svg
            class="co-rm-svg"
            :viewBox="`0 0 ${GRID_W} ${GRID_H}`"
            preserveAspectRatio="none"
            aria-hidden="true"
          >
            <defs>
              <linearGradient
                :id="`${uid}-grid-cpu`"
                x1="0"
                y1="0"
                x2="0"
                y2="1"
              >
                <stop
                  offset="0%"
                  stop-color="var(--co-rm-cpu)"
                  stop-opacity="0.28"
                />
                <stop
                  offset="100%"
                  stop-color="var(--co-rm-cpu)"
                  stop-opacity="0.02"
                />
              </linearGradient>
            </defs>
            <line
              v-for="y in GRID_Y_LINES"
              :key="`cy-${y}`"
              x1="0"
              :y1="y"
              :x2="GRID_W"
              :y2="y"
              class="co-rm-grid-line"
            />
            <line
              v-for="(x, idx) in cpuGridChart.gridXOffsets"
              :key="`cx-${idx}`"
              :x1="x"
              y1="0"
              :x2="x"
              :y2="GRID_H"
              class="co-rm-grid-line"
            />
            <template v-if="monitorSk.panels">
              <path
                :d="`${SK_GRID_WAVE_PATHS.cpu} L ${GRID_W} ${GRID_H} L 0 ${GRID_H} Z`"
                :fill="`url(#${uid}-grid-cpu)`"
                class="co-rm-sk-area"
              />
              <path :d="SK_GRID_WAVE_PATHS.cpu" class="co-rm-sk-wave" />
            </template>
            <template v-else>
              <path
                v-if="telemetry.online && telemetry.cpuPercent !== null"
                :d="cpuGridChart.areaPath"
                :fill="`url(#${uid}-grid-cpu)`"
              />
              <path :d="cpuGridChart.linePath" class="co-rm-primary-stroke" />
            </template>
          </svg>
          <span
            v-if="monitorSk.panels || monitorSk.cpuPending"
            class="co-rm-scale-cap co-rm-scale-cap-sk"
          >
            <ColabOneSkeleton variant="text" :width="30" :height="11" :delay="280" />
          </span>
          <span v-else class="co-rm-scale-cap co-rm-mono">{{ cpuLivePercentLabel }}</span>
        </div>

        <div class="co-rm-axis-row co-rm-mono" aria-hidden="true">
          <span>{{ t("monitor.axis60sAgo") }}</span>
          <span>{{ t("monitor.axisNow") }}</span>
        </div>

        <div class="co-rm-card-footer">
          <template v-if="monitorSk.panels">
            <span class="co-rm-kv">
              <em>{{ t("monitor.statLiveUtil") }}</em>
              <ColabOneSkeleton variant="text" :width="32" :height="12" :delay="260" inline />
            </span>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statSpec") }}</em>
              <ColabOneSkeleton variant="text" :width="52" :height="12" :delay="310" inline />
            </span>
          </template>
          <template v-else-if="telemetry.online && telemetry.cpuPercent !== null">
            <span class="co-rm-kv">
              <em>{{ t("monitor.statLiveUtil") }}</em>
              <b class="co-rm-mono">{{ Math.round(telemetry.cpuPercent) }}%</b>
            </span>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statSpec") }}</em>
              <b class="co-rm-mono">{{
                telemetry.cpuCores
                  ? `${telemetry.cpuCores} vCPU`
                  : t("monitor.cpuRemoteCore")
              }}</b>
            </span>
          </template>
          <span
            v-else-if="telemetry.online && telemetry.cpuPercent === null"
            class="co-rm-kv"
          >
            <template v-if="monitorSk.cpuPending">
              <em>{{ t("monitor.statLiveUtil") }}</em>
              <ColabOneSkeleton variant="text" :width="34" :height="12" :delay="120" inline />
              <span class="co-sr-only">{{ t("monitor.statWaitingReport") }}</span>
            </template>
            <span v-else class="co-rm-waiting-note">
              {{ t("monitor.statWaitingReport") }}
            </span>
          </span>
          <template v-else>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statLiveUtil") }}</em>
              <b class="co-rm-mono">{{ isConnecting ? "--" : "0%" }}</b>
            </span>
            <span class="co-rm-kv">
              <em>{{ t("monitor.statSpec") }}</em>
              <b class="co-rm-mono">-- vCPU</b>
            </span>
          </template>
        </div>
      </ACard>
    </div>

    <!-- MODE 2: 4-Row Stacked Simultaneous Wide Charts -->
    <div v-else class="co-rm-rows" :aria-label="t('monitor.rowsAria')">
      <section
        v-for="(row, rowIdx) in stackedRows"
        :id="`${uid}-row-${row.key}`"
        :key="row.key"
        class="co-rm-row"
        :class="[
          `co-metric-${row.key}`,
          { 'is-focused': highlightedMetric === row.key },
        ]"
        :aria-label="t('monitor.rowMonitorAria', { title: row.title })"
      >
        <!-- Left Compact Summary Rail -->
        <div class="co-rm-row-rail">
          <div class="co-rm-row-rail-top">
            <span class="co-rm-metric-icon co-rm-row-icon" aria-hidden="true">
              <component :is="row.icon" :size="14" />
            </span>
            <div class="co-rm-row-rail-info">
              <div class="co-rm-row-rail-line">
                <h4 class="co-rm-row-title">{{ row.title }}</h4>
                <ColabOneSkeleton
                  v-if="
                    monitorSk.panels ||
                    (row.key === 'ram' && monitorSk.ramPending) ||
                    (row.key === 'cpu' && monitorSk.cpuPending)
                  "
                  variant="metric"
                  :width="46"
                  :height="17"
                  :delay="rowIdx * 80"
                />
                <strong
                  v-else
                  class="co-rm-row-hero co-rm-mono co-sk-reveal"
                  :class="{ 'is-compact': row.compactHero }"
                >
                  {{ row.heroValue }}
                </strong>
              </div>
              <div class="co-rm-row-rail-subline">
                <ColabOneSkeleton
                  v-if="monitorSk.initial"
                  variant="text"
                  :width="76"
                  :height="11"
                  :delay="rowIdx * 80"
                />
                <small v-else class="co-rm-row-subtitle" :title="row.subtitle">{{
                  row.subtitle
                }}</small>
                <ColabOneSkeleton
                  v-if="monitorSk.panels"
                  variant="text"
                  :width="62"
                  :height="11"
                  :delay="rowIdx * 80 + 40"
                />
                <span v-else class="co-rm-row-hero-sub co-rm-mono" :title="row.heroSub">
                  {{ row.heroSub }}
                </span>
              </div>
            </div>
          </div>
          <ColabOneSkeleton
            v-if="monitorSk.panels"
            variant="bar"
            width="100%"
            :height="6"
            :delay="rowIdx * 80"
            class="co-rm-sk-progress co-rm-row-progress"
          />
          <AProgress
            v-else
            :percent="row.progressPercent"
            :show-info="false"
            size="small"
            :stroke-color="row.progressColor"
            trail-color="var(--co-subtle)"
            class="co-rm-metric-progress co-rm-row-progress"
          />
        </div>

        <!-- Right Compact Full-Width 60s Chart + Single-Line Meta Strip -->
        <div class="co-rm-row-main">
          <div class="co-rm-row-meta-bar">
            <div class="co-rm-detail-legends">
              <span class="co-rm-legend-primary">{{ row.seriesLabel }}</span>
              <span v-if="row.secondaryLabel" class="co-rm-legend-secondary">
                {{ row.secondaryLabel }}
              </span>
            </div>
            <div class="co-rm-row-stats" :aria-label="t('monitor.rowStatsAria')">
              <span
                v-for="(stat, sIdx) in row.stats"
                :key="`${row.key}-${stat.label}`"
                class="co-rm-kv co-rm-row-stat"
              >
                <em>{{ stat.label }}</em>
                <ColabOneSkeleton
                  v-if="monitorSk.panels"
                  variant="text"
                  :width="sIdx === 0 ? 58 : 42"
                  :height="12"
                  :delay="rowIdx * 80 + sIdx * 50"
                  inline
                />
                <b v-else :class="stat.muted ? 'co-rm-waiting-note' : 'co-rm-mono'">{{
                  stat.value
                }}</b>
              </span>
              <ATag
                v-if="!monitorSk.panels && row.driveBadge"
                :bordered="false"
                class="co-rm-drive-badge co-rm-mono"
              >
                {{ row.driveBadge }}
              </ATag>
            </div>
          </div>

          <div class="co-rm-plot-frame co-rm-row-plot">
            <svg
              class="co-rm-svg"
              :viewBox="`0 0 ${ROW_W} ${ROW_H}`"
              preserveAspectRatio="none"
              role="img"
              :aria-label="t('monitor.rowChartAria', { title: row.title })"
            >
              <defs>
                <linearGradient
                  :id="`${uid}-row-grad-${row.key}`"
                  x1="0"
                  y1="0"
                  x2="0"
                  y2="1"
                >
                  <stop
                    offset="0%"
                    stop-color="var(--co-metric-accent)"
                    stop-opacity="0.28"
                  />
                  <stop
                    offset="100%"
                    stop-color="var(--co-metric-accent)"
                    stop-opacity="0.02"
                  />
                </linearGradient>
              </defs>
              <line
                v-for="y in ROW_Y_LINES"
                :key="`ry-${row.key}-${y}`"
                x1="0"
                :y1="y"
                :x2="ROW_W"
                :y2="y"
                class="co-rm-grid-line"
              />
              <line
                v-for="(x, idx) in row.primaryChart.gridXOffsets"
                :key="`rx-${row.key}-${idx}`"
                :x1="x"
                y1="0"
                :x2="x"
                :y2="ROW_H"
                class="co-rm-grid-line"
              />
              <template v-if="monitorSk.panels">
                <path
                  :d="`${SK_ROW_WAVE_PATHS[row.key]} L ${ROW_W} ${ROW_H} L 0 ${ROW_H} Z`"
                  :fill="`url(#${uid}-row-grad-${row.key})`"
                  class="co-rm-sk-area"
                />
                <path :d="SK_ROW_WAVE_PATHS[row.key]" class="co-rm-sk-wave" />
              </template>
              <template v-else>
                <path
                  v-if="row.showArea"
                  :d="row.primaryChart.areaPath"
                  :fill="`url(#${uid}-row-grad-${row.key})`"
                />
                <path
                  v-if="row.secondaryChart"
                  :d="row.secondaryChart.linePath"
                  class="co-rm-secondary-stroke"
                />
                <path :d="row.primaryChart.linePath" class="co-rm-primary-stroke" />
              </template>
            </svg>
            <span
              v-if="
                monitorSk.panels ||
                (row.key === 'ram' && monitorSk.ramPending) ||
                (row.key === 'cpu' && monitorSk.cpuPending)
              "
              class="co-rm-scale-cap co-rm-scale-cap-sk"
            >
              <ColabOneSkeleton variant="text" :width="30" :height="11" :delay="rowIdx * 80 + 40" />
            </span>
            <span v-else class="co-rm-scale-cap co-rm-mono">{{ row.scaleTop }}</span>
          </div>
        </div>
      </section>

      <!-- Shared Synchronized 60s Time Axis -->
      <div class="co-rm-rows-axis-bar" aria-hidden="true">
        <span class="co-rm-rows-axis-caption">{{ t("monitor.axisCaption") }}</span>
        <div class="co-rm-rows-axis-ticks co-rm-mono">
          <span>{{ t("monitor.axis60sAgo") }}</span>
          <span>{{ t("monitor.axis40sAgo") }}</span>
          <span>{{ t("monitor.axis20sAgo") }}</span>
          <span>{{ t("monitor.axisNow") }}</span>
        </div>
      </div>
    </div>
  </ACard>
</template>

<style scoped>
.co-monitor {
  /* ColabOne Semantic Chart Palette */
  --co-rm-gpu: #3b82f6;
  --co-rm-gpu-border: rgba(59, 130, 246, 0.34);
  --co-rm-gpu-grid: rgba(59, 130, 246, 0.14);
  --co-rm-gpu-tint: rgba(59, 130, 246, 0.1);

  --co-rm-ram: #8b5cf6;
  --co-rm-ram-border: rgba(139, 92, 246, 0.34);
  --co-rm-ram-grid: rgba(139, 92, 246, 0.14);
  --co-rm-ram-tint: rgba(139, 92, 246, 0.1);

  --co-rm-disk: #10b981;
  --co-rm-disk-border: rgba(16, 185, 129, 0.36);
  --co-rm-disk-grid: rgba(16, 185, 129, 0.15);
  --co-rm-disk-tint: rgba(16, 185, 129, 0.1);

  --co-rm-cpu: #f59e0b;
  --co-rm-cpu-border: rgba(245, 158, 11, 0.36);
  --co-rm-cpu-grid: rgba(245, 158, 11, 0.15);
  --co-rm-cpu-tint: rgba(245, 158, 11, 0.1);

  --co-rm-plot-bg: #fafbfc;
  --co-rm-offline-grid: rgba(148, 163, 184, 0.22);
  --co-rm-offline-stroke: rgba(148, 163, 184, 0.45);

  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  border-radius: 16px;
  box-shadow: var(--co-card-shadow);
  color: var(--co-text);
  overflow: hidden;
  font-variant-numeric: tabular-nums;
}

.co-monitor :deep(.ant-card-head) {
  min-height: 48px;
  padding: 10px 16px;
  margin-bottom: 0;
  border-bottom: 1px solid var(--co-border-secondary);
  background: transparent;
}

.co-monitor :deep(.ant-card-head-wrapper) {
  align-items: flex-start;
  gap: 12px;
}

.co-monitor :deep(.ant-card-head-title) {
  display: flex;
  align-items: center;
  padding: 0;
  white-space: normal;
  line-height: 1.25;
}

.co-monitor :deep(.ant-card-extra) {
  display: flex;
  align-items: center;
  min-height: 28px;
  padding: 0;
  margin-inline-start: auto;
}

.co-monitor :deep(.ant-card-body) {
  padding: 0;
}

/* Metric scoped variable mapping */
.co-metric-gpu {
  --co-metric-accent: var(--co-rm-gpu);
  --co-metric-border: var(--co-rm-gpu-border);
  --co-metric-grid: var(--co-rm-gpu-grid);
  --co-metric-tint: var(--co-rm-gpu-tint);
}

.co-metric-ram {
  --co-metric-accent: var(--co-rm-ram);
  --co-metric-border: var(--co-rm-ram-border);
  --co-metric-grid: var(--co-rm-ram-grid);
  --co-metric-tint: var(--co-rm-ram-tint);
}

.co-metric-disk {
  --co-metric-accent: var(--co-rm-disk);
  --co-metric-border: var(--co-rm-disk-border);
  --co-metric-grid: var(--co-rm-disk-grid);
  --co-metric-tint: var(--co-rm-disk-tint);
}

.co-metric-cpu {
  --co-metric-accent: var(--co-rm-cpu);
  --co-metric-border: var(--co-rm-cpu-border);
  --co-metric-grid: var(--co-rm-cpu-grid);
  --co-metric-tint: var(--co-rm-cpu-tint);
}

.co-rm-mono {
  font-family: var(--co-font-mono);
  font-variant-numeric: tabular-nums;
}

/* Header */
.co-rm-header-title {
  min-width: 0;
  width: 100%;
}

.co-rm-heading-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  row-gap: 8px;
  column-gap: 10px;
}

.co-rm-heading-primary {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 28px;
}

.co-rm-heading-row h3 {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.25;
  color: var(--co-text);
  margin: 0;
  white-space: nowrap;
}

.co-rm-live-badge {
  display: inline-flex;
  align-items: center;
  height: 24px;
  padding: 0 10px;
  border-radius: 9999px;
  background: var(--co-track);
  border: 1px solid var(--co-border);
  line-height: 1;
}

.co-rm-live-badge :deep(.ant-badge-status-text) {
  font-size: 12px;
  font-weight: 550;
  color: var(--co-muted);
  margin-inline-start: 6px;
}

.co-rm-live-badge.live {
  background: var(--co-selected);
  border-color: rgba(59, 130, 246, 0.28);
}

.co-rm-live-badge.live :deep(.ant-badge-status-text) {
  color: var(--co-accent-strong);
}

.co-rm-live-badge.connecting {
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
}

.co-rm-live-badge.connecting :deep(.ant-badge-status-text) {
  color: var(--co-warning-text);
}

.co-rm-live-badge.remote {
  background: rgba(14, 165, 233, 0.1);
  border-color: rgba(14, 165, 233, 0.3);
}

.co-rm-live-badge.remote :deep(.ant-badge-status-text) {
  color: #0284c7;
}

.co-rm-live-badge.paused {
  background: var(--co-track);
  border-color: var(--co-border);
}

.co-rm-live-badge.error {
  background: var(--co-danger-bg);
  border-color: var(--co-danger-border);
}

.co-rm-live-badge.error :deep(.ant-badge-status-text) {
  color: var(--co-danger-text);
}

.co-rm-retry-btn {
  padding: 0 6px;
  height: 22px;
  font-size: 12px;
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.co-rm-header-controls {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.co-rm-instance-select {
  min-width: 196px;
  max-width: 260px;
}

.co-rm-instance-select :deep(.ant-select-selector) {
  border-radius: 8px !important;
  font-size: 12px;
  height: 28px !important;
  align-items: center;
}

.co-rm-instance-select :deep(.ant-select-selection-item) {
  line-height: 26px !important;
}

.co-rm-pause-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 28px;
  padding: 0 9px;
  font-size: 12px;
  border-radius: 8px;
}

.co-rm-pause-btn.is-paused {
  border-color: var(--co-accent);
  color: var(--co-accent-strong);
}

.co-rm-hw-pills {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-height: 24px;
}

.co-rm-hw-tag {
  margin: 0;
  height: 24px;
  padding: 0 10px;
  border-radius: 9999px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  font-weight: 560;
  line-height: 1;
}

.co-rm-hw-gpu {
  background: var(--co-surface);
  border: 1px solid var(--co-rm-gpu-border);
  color: var(--co-rm-gpu);
}

.co-rm-hw-cool {
  background: var(--co-surface);
  border: 1px solid var(--co-rm-disk-border);
  color: var(--co-rm-disk);
}

.co-rm-hw-warm {
  background: var(--co-surface);
  border: 1px solid var(--co-rm-cpu-border);
  color: var(--co-rm-cpu);
}

.co-rm-hw-hot {
  background: var(--co-surface);
  border: 1px solid var(--co-danger-border);
  color: var(--co-danger-text);
}

.co-rm-hw-power {
  background: var(--co-surface);
  border: 1px solid var(--co-rm-ram-border);
  color: var(--co-rm-ram);
}

.co-rm-header-sub {
  font-size: 12.5px;
  line-height: 1.4;
  color: var(--co-muted);
  font-weight: 400;
}

.co-rm-layout-segmented {
  flex-shrink: 0;
}

.co-rm-layout-segmented :deep(.ant-segmented-item-label) {
  padding: 0;
  min-height: 20px;
}

.co-rm-seg-label {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 20px;
}

.co-rm-seg-label svg {
  flex-shrink: 0;
}

.co-rm-sr-only {
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

/* Compact 2x2 ColabOne Resource Monitor Grid -> 4x1 panoramic row on wide fullscreen monitors */
.co-rm-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  padding: 10px 16px 12px;
}

.co-rm-card {
  border: 1px solid var(--co-border-secondary);
  border-radius: 12px;
  background: var(--co-surface);
  color: var(--co-text);
  cursor: pointer;
  transition: border-color 0.16s ease, box-shadow 0.16s ease, transform 0.15s ease;
}

.co-rm-card :deep(.ant-card-body) {
  display: flex;
  flex-direction: column;
  padding: 10px 13px;
}

.co-rm-card:hover {
  border-color: var(--co-metric-border);
  box-shadow: var(--co-card-shadow-hover);
  transform: translateY(-1px);
}

.co-rm-card:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-rm-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
}

.co-rm-card-title-group {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.co-rm-metric-icon {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  display: grid;
  place-items: center;
  background: var(--co-metric-tint);
  border: 1px solid var(--co-metric-border);
  color: var(--co-metric-accent);
  flex-shrink: 0;
}

.is-offline .co-rm-metric-icon {
  background: var(--co-surface);
  border-color: var(--co-border-secondary);
  color: var(--co-muted);
}

.co-rm-card-titles {
  min-width: 0;
}

.co-rm-card-titles h4 {
  margin: 0;
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
  line-height: 1.25;
}

.co-rm-card-titles small {
  display: block;
  margin-top: 1px;
  font-size: 11.5px;
  color: var(--co-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-rm-live-val {
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.02em;
  color: var(--co-metric-accent);
  line-height: 1.15;
  flex-shrink: 0;
}

.co-rm-disk-val {
  font-size: 14px;
}

.is-offline .co-rm-live-val {
  color: var(--co-muted);
}

.co-rm-metric-progress {
  margin: 0 0 6px;
  line-height: 1;
}

.co-rm-metric-progress :deep(.ant-progress-outer) {
  display: block;
}

.co-rm-metric-progress :deep(.ant-progress-inner) {
  border-radius: 999px;
}

/* ColabOne SVG Plot Frame */
.co-rm-plot-frame {
  position: relative;
  height: 58px;
  border: 1px solid var(--co-metric-border);
  border-radius: 8px;
  background: var(--co-rm-plot-bg);
  overflow: hidden;
}

.is-offline .co-rm-plot-frame {
  height: 40px;
  border-color: var(--co-border);
}

.is-connecting .co-rm-plot-frame::after,
.is-skeleton .co-rm-plot-frame::after {
  content: "";
  position: absolute;
  inset: 0;
  width: 400%;
  background: linear-gradient(
    90deg,
    transparent 0%,
    transparent 10%,
    var(--co-skeleton-sheen-soft) 19%,
    var(--co-skeleton-sheen) 25%,
    var(--co-metric-tint) 31%,
    transparent 40%,
    transparent 60%,
    var(--co-skeleton-sheen-soft) 69%,
    var(--co-skeleton-sheen) 75%,
    var(--co-metric-tint) 81%,
    transparent 90%,
    transparent 100%
  );
  transform: translate3d(-50%, 0, 0);
  animation: co-skeleton-wave 1.65s linear infinite;
  pointer-events: none;
  will-change: transform;
}

.co-rm-sub-sk {
  margin-top: 3px;
}

.co-rm-sk-progress {
  margin: 0 0 6px;
}

.co-rm-sk-wave {
  fill: none;
  stroke: var(--co-metric-accent);
  stroke-width: 1.5;
  stroke-opacity: 0.26;
  stroke-dasharray: 4 3;
  stroke-linecap: round;
  vector-effect: non-scaling-stroke;
}

.co-rm-sk-area {
  opacity: 0.36;
}

.co-rm-scale-cap-sk {
  display: inline-flex;
  align-items: center;
  padding: 2px 4px;
}

.is-skeleton .co-rm-plot-frame {
  border-color: var(--co-metric-border);
}

@media (prefers-reduced-motion: reduce) {
  .is-connecting .co-rm-plot-frame::after,
  .is-skeleton .co-rm-plot-frame::after {
    animation: none;
    transform: none;
    opacity: 0;
  }
}

.co-rm-svg {
  display: block;
  width: 100%;
  height: 100%;
}

.co-rm-grid-line {
  stroke: var(--co-metric-grid);
  stroke-width: 1;
  stroke-dasharray: 2 2;
  vector-effect: non-scaling-stroke;
}

.is-offline .co-rm-grid-line {
  stroke: var(--co-rm-offline-grid);
}

.co-rm-primary-stroke {
  fill: none;
  stroke: var(--co-metric-accent);
  stroke-width: 1.8;
  stroke-linejoin: round;
  stroke-linecap: round;
  vector-effect: non-scaling-stroke;
}

.is-offline .co-rm-primary-stroke {
  stroke: var(--co-rm-offline-stroke);
  stroke-width: 1.4;
}

.co-rm-secondary-stroke {
  fill: none;
  stroke: var(--co-metric-accent);
  stroke-width: 1.3;
  stroke-dasharray: 4 3;
  stroke-opacity: 0.72;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}

.co-rm-scale-cap {
  position: absolute;
  top: 3px;
  right: 5px;
  padding: 1px 5px;
  border-radius: 4px;
  background: rgba(250, 251, 252, 0.88);
  font-size: 10.5px;
  font-weight: 600;
  line-height: 1.2;
  color: var(--co-metric-accent);
  pointer-events: none;
}

.is-offline .co-rm-scale-cap {
  color: var(--co-muted);
  font-weight: 500;
  opacity: 0.82;
}

.co-rm-axis-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 3px;
  font-size: 11px;
  color: var(--co-muted);
}

/* Card Footer Key-Value Stats */
.co-rm-card-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 10px;
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px solid var(--co-border-secondary);
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-rm-kv {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-rm-kv em {
  font-style: normal;
  color: var(--co-muted);
}

.co-rm-kv b {
  font-weight: 600;
  color: var(--co-text);
}

.is-offline .co-rm-kv b {
  color: var(--co-muted);
  font-weight: 500;
}

.co-rm-waiting-note {
  color: var(--co-muted);
  font-size: 12px;
}

.co-rm-drive-badge {
  margin: 0;
  display: inline-flex;
  align-items: center;
  min-height: 18px;
  height: 18px;
  padding: 0 6px;
  border-radius: 5px;
  font-size: 11px;
  font-weight: 560;
  line-height: 1;
  background: var(--co-surface);
  border: 1px solid var(--co-rm-disk-border);
  color: var(--co-rm-disk);
}

/* Compact 4-Row Stacked Simultaneous Wide Charts */
.co-rm-rows {
  --co-rm-rail-w: 244px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 16px 10px;
}

.co-rm-row {
  position: relative;
  display: grid;
  grid-template-columns: var(--co-rm-rail-w) minmax(0, 1fr);
  gap: 14px;
  align-items: center;
  padding: 7px 12px 7px 14px;
  border: 1px solid var(--co-border-secondary);
  border-radius: 11px;
  background: var(--co-surface);
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    background-color 0.16s ease;
}

.co-rm-row::before {
  content: "";
  position: absolute;
  left: 0;
  top: 8px;
  bottom: 8px;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: var(--co-metric-accent);
  opacity: 0.85;
  transition: opacity 0.16s ease;
}

.is-offline .co-rm-row::before {
  opacity: 0.28;
}

.co-rm-row:hover {
  border-color: var(--co-metric-border);
  box-shadow: var(--co-card-shadow-hover);
}

.co-rm-row.is-focused {
  border-color: var(--co-metric-accent);
  box-shadow: 0 0 0 2px var(--co-metric-tint);
}

.co-rm-row-rail {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 5px;
  padding-right: 14px;
  border-right: 1px solid var(--co-border-secondary);
  min-width: 0;
}

.co-rm-row-rail-top {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.co-rm-row-icon {
  width: 26px;
  height: 26px;
  border-radius: 7px;
}

.co-rm-row-rail-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-rm-row-rail-line,
.co-rm-row-rail-subline {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
}

.co-rm-row-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
  line-height: 1.25;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.co-rm-row-subtitle {
  display: block;
  font-size: 11.5px;
  line-height: 1.25;
  color: var(--co-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.co-rm-row-hero {
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.02em;
  line-height: 1.1;
  color: var(--co-metric-accent);
  flex-shrink: 0;
}

.co-rm-row-hero.is-compact {
  font-size: 14px;
}

.is-offline .co-rm-row-hero {
  color: var(--co-muted);
}

.co-rm-row-hero-sub {
  font-size: 11.5px;
  line-height: 1.25;
  color: var(--co-secondary-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: right;
  flex-shrink: 0;
}

.co-rm-row-progress {
  margin: 0;
}

.co-rm-row-main {
  display: flex;
  flex-direction: column;
  justify-content: center;
  min-width: 0;
}

.co-rm-row-meta-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 2px 12px;
  margin-bottom: 4px;
  font-size: 11.5px;
  line-height: 1.3;
  color: var(--co-muted);
}

.co-rm-detail-legends {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  flex-shrink: 0;
}

.co-rm-legend-primary,
.co-rm-legend-secondary {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.co-rm-legend-primary::before {
  content: "";
  width: 10px;
  height: 2px;
  background: var(--co-metric-accent);
  border-radius: 1px;
}

.is-offline .co-rm-legend-primary::before {
  background: var(--co-rm-offline-stroke);
}

.co-rm-legend-secondary::before {
  content: "";
  width: 14px;
  height: 2px;
  background: repeating-linear-gradient(
    90deg,
    var(--co-metric-accent) 0 4px,
    transparent 4px 7px
  );
  opacity: 0.85;
}

.co-rm-row-plot {
  height: 40px;
}

.is-offline .co-rm-row-plot {
  height: 30px;
}

.co-rm-row-stats {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 2px 12px;
  font-size: 11.5px;
  color: var(--co-secondary-text);
  min-width: 0;
}

.co-rm-row-stat {
  gap: 4px;
  min-width: 0;
  max-width: 100%;
}

.co-rm-row-stat b {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-rm-rows-axis-bar {
  display: grid;
  grid-template-columns: var(--co-rm-rail-w) minmax(0, 1fr);
  gap: 14px;
  align-items: center;
  padding: 0 13px 0 15px;
  margin-top: -1px;
}

.co-rm-rows-axis-caption {
  font-size: 11px;
  color: var(--co-muted);
  padding-right: 14px;
}

.co-rm-rows-axis-ticks {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 11px;
  color: var(--co-muted);
}

@media (min-width: 1440px) {
  .co-rm-grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 12px;
  }

  .co-rm-plot-frame {
    height: 66px;
  }

  .is-offline .co-rm-plot-frame {
    height: 44px;
  }

  .co-rm-rows {
    --co-rm-rail-w: 264px;
  }

  .co-rm-row-plot {
    height: 42px;
  }

  .is-offline .co-rm-row-plot {
    height: 32px;
  }
}

@media (max-width: 860px) {
  .co-rm-grid {
    grid-template-columns: 1fr;
  }

  .co-rm-rows {
    padding: 8px 12px 10px;
  }

  .co-rm-row {
    grid-template-columns: 1fr;
    gap: 6px;
    padding: 8px 10px 8px 12px;
  }

  .co-rm-row-rail {
    padding-right: 0;
    padding-bottom: 6px;
    border-right: 0;
    border-bottom: 1px solid var(--co-border-secondary);
  }

  .co-rm-row-stats {
    justify-content: flex-start;
  }

  .co-rm-rows-axis-bar {
    grid-template-columns: 1fr;
    padding: 0 11px 0 13px;
  }

  .co-rm-rows-axis-caption {
    display: none;
  }
}

@media (max-width: 640px) {
  .co-monitor :deep(.ant-card-head-wrapper) {
    flex-direction: column;
    align-items: flex-start;
  }

  .co-monitor :deep(.ant-card-extra) {
    margin-inline-start: 0;
  }
}
</style>

<style>
:root[data-theme="dark"] .co-monitor {
  /* ColabOne Dark Semantic Chart Palette */
  --co-rm-gpu: #60a5fa;
  --co-rm-gpu-border: rgba(96, 165, 250, 0.38);
  --co-rm-gpu-grid: rgba(96, 165, 250, 0.16);
  --co-rm-gpu-tint: rgba(59, 130, 246, 0.15);

  --co-rm-ram: #a78bfa;
  --co-rm-ram-border: rgba(167, 139, 250, 0.38);
  --co-rm-ram-grid: rgba(167, 139, 250, 0.16);
  --co-rm-ram-tint: rgba(139, 92, 246, 0.15);

  --co-rm-disk: #34d399;
  --co-rm-disk-border: rgba(52, 211, 153, 0.38);
  --co-rm-disk-grid: rgba(52, 211, 153, 0.16);
  --co-rm-disk-tint: rgba(16, 185, 129, 0.15);

  --co-rm-cpu: #fbbf24;
  --co-rm-cpu-border: rgba(251, 191, 36, 0.38);
  --co-rm-cpu-grid: rgba(251, 191, 36, 0.16);
  --co-rm-cpu-tint: rgba(245, 158, 11, 0.15);

  --co-rm-plot-bg: #0f1117;
  --co-rm-offline-grid: rgba(160, 160, 170, 0.12);
  --co-rm-offline-stroke: rgba(160, 160, 170, 0.32);
}

:root[data-theme="dark"] .co-monitor.is-connecting .co-rm-plot-frame::after,
:root[data-theme="dark"] .co-monitor.is-skeleton .co-rm-plot-frame::after {
  background: linear-gradient(
    90deg,
    transparent 0%,
    transparent 10%,
    rgba(255, 255, 255, 0.05) 19%,
    rgba(255, 255, 255, 0.12) 25%,
    var(--co-metric-tint) 31%,
    transparent 40%,
    transparent 60%,
    rgba(255, 255, 255, 0.05) 69%,
    rgba(255, 255, 255, 0.12) 75%,
    var(--co-metric-tint) 81%,
    transparent 90%,
    transparent 100%
  );
}

:root[data-theme="dark"] .co-monitor .co-rm-live-badge.remote {
  background: rgba(56, 189, 248, 0.14);
  border-color: rgba(56, 189, 248, 0.34);
}

:root[data-theme="dark"] .co-monitor .co-rm-live-badge.remote .ant-badge-status-text {
  color: #38bdf8;
}

:root[data-theme="dark"] .co-monitor .co-rm-scale-cap {
  background: rgba(15, 17, 23, 0.84);
}

.co-rm-opt-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  line-height: 1.4;
  padding: 1px 0;
}

.co-rm-opt-tag {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 9999px;
  font-size: 11px;
  font-weight: 560;
  white-space: nowrap;
  flex-shrink: 0;
}

.co-rm-opt-tag.is-local {
  background: rgba(16, 185, 129, 0.12);
  color: #059669;
}

.co-rm-opt-tag.is-remote-co {
  background: rgba(59, 130, 246, 0.12);
  color: #2563eb;
}

.co-rm-opt-tag.is-remote {
  background: rgba(14, 165, 233, 0.12);
  color: #0284c7;
}

.co-rm-opt-accel {
  font-weight: 600;
  color: var(--co-text);
  white-space: nowrap;
}

.co-rm-opt-loc {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--co-secondary-text);
  white-space: nowrap;
}

.co-rm-opt-id {
  font-family: var(--co-font-mono);
  font-size: 11px;
  color: var(--co-muted);
  white-space: nowrap;
}

.co-rm-opt-model {
  font-size: 11px;
  color: var(--co-accent-strong);
  white-space: nowrap;
}

:root[data-theme="dark"] .co-rm-opt-tag.is-local {
  background: rgba(52, 211, 153, 0.16);
  color: #34d399;
}

:root[data-theme="dark"] .co-rm-opt-tag.is-remote-co {
  background: rgba(96, 165, 250, 0.18);
  color: #60a5fa;
}

:root[data-theme="dark"] .co-rm-opt-tag.is-remote {
  background: rgba(56, 189, 248, 0.16);
  color: #38bdf8;
}
</style>
