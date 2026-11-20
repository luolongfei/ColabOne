<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Badge as ABadge,
  Card as ACard,
  Progress as AProgress,
  Segmented as ASegmented,
  Space as ASpace,
  Tag as ATag,
  Tooltip as ATooltip,
} from "ant-design-vue";
import {
  ColabOneTelemetryTracker,
  buildColabOneChartGeometry,
  formatMetricBytes,
  formatRateBps,
  type ColabOneChartGeometry,
  type ColabOneTelemetry,
} from "./telemetry";
import { COLABONE_STORAGE_KEYS } from "./storage";
import type { RuntimeStatus } from "./types";
import {
  PhCpu,
  PhGraphicsCard,
  PhHardDrives,
  PhLightning,
  PhMemory,
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
}>();

type LayoutMode = "grid" | "rows";
type MetricKey = "gpu" | "ram" | "disk" | "cpu";

const uid = `co-rm-${Math.random().toString(36).slice(2, 8)}`;

const layoutOptions = [
  { value: "grid", payload: { label: "四图网格" } },
  { value: "rows", payload: { label: "四行宽幅" } },
];

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
const telemetry = ref<ColabOneTelemetry>(
  tracker.pushSnapshot(props.runtime, Date.now() / 1000),
);

let tickTimer: ReturnType<typeof setInterval> | undefined;

watch(
  () => props.runtime,
  (nextRuntime) => {
    const nowMs = Date.now();
    nowTick.value = nowMs;
    telemetry.value = tracker.pushSnapshot(nextRuntime, nowMs / 1000);
  },
  { deep: true },
);

onMounted(() => {
  tickTimer = setInterval(() => {
    if (props.ipcDisconnected) return;
    const nowMs = Date.now();
    nowTick.value = nowMs;
    if (props.runtime.state === "ready" || telemetry.value.online) {
      telemetry.value = tracker.pushSnapshot(props.runtime, nowMs / 1000);
    }
  }, 1000);
});

onUnmounted(() => {
  clearHighlight();
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

const isStarting = computed<boolean>(() => props.runtime.state === "starting");
const isIpcPaused = computed<boolean>(() => Boolean(props.ipcDisconnected));
const isConnecting = computed<boolean>(
  () => isStarting.value && !telemetry.value.online,
);

const headerBadgeStatus = computed<
  "processing" | "warning" | "error" | "default"
>(() => {
  if (isIpcPaused.value || isConnecting.value) return "warning";
  if (telemetry.value.online) return "processing";
  if (props.runtime.state === "disconnected") return "error";
  return "default";
});

const headerBadgeText = computed<string>(() => {
  if (isIpcPaused.value) return "本地服务重连中";
  if (telemetry.value.online) {
    return isStarting.value ? "部署中 · 实时采样" : "实时 60s 采样";
  }
  if (isConnecting.value) return "正在连接";
  if (props.runtime.state === "disconnected") return "连接中断";
  return "待机预览";
});

const onlineSubtitle = computed<string>(() =>
  isIpcPaused.value
    ? "与本地后台服务的连接暂时中断，恢复连接后继续同步实时硬件曲线"
    : isStarting.value
      ? "已采集远端硬件实时曲线，正在完成管理服务部署，就绪后即可下载与装载模型"
      : "实时采集远端实例显存、内存、磁盘吞吐与处理器负载（每秒刷新）",
);

const offlineSubtitle = computed<string>(() => {
  if (isIpcPaused.value) {
    return "与本地后台服务的连接暂时中断，恢复连接后继续同步实时硬件曲线";
  }
  if (props.runtime.state === "starting") {
    return props.operationTitle
      ? `${props.operationTitle} · 正在采集显存、内存、磁盘与 CPU 实时状态…`
      : "正在连接远端 GPU，即将开始采集实时硬件曲线";
  }
  if (props.runtime.state === "disconnected") {
    return "远端连接暂时中断，恢复连接后继续同步实时硬件曲线";
  }
  return "远端 GPU 未启动，启动后将实时展示显存、内存、磁盘与 CPU 负载";
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

const activeGpuDisplayName = computed<string>(
  () => telemetry.value.gpuName || props.runtime.gpu?.name || "",
);

const gpuSubtitle = computed<string>(() => {
  if (telemetry.value.gpuName) return telemetry.value.gpuName;
  if (props.runtime.gpu?.name) return props.runtime.gpu.name;
  if (telemetry.value.online) return "Colab GPU";
  if (isConnecting.value) {
    return props.selectedGpu
      ? `正在初始化 ${props.selectedGpu} 加速器…`
      : "正在初始化远端加速器…";
  }
  return "待分配加速器";
});

const cpuSubtitle = computed<string>(() =>
  telemetry.value.cpuCores
    ? `${telemetry.value.cpuCores} 逻辑核心`
    : telemetry.value.online
      ? "远端计算核心"
      : isConnecting.value
        ? "正在初始化计算核心…"
        : "待分配计算核心",
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

const stackedRows = computed<StackedRowItem[]>(() => {
  const t = telemetry.value;

  const gpuStats: StackedRowStat[] = [];
  if (t.online && t.vramTotalBytes > 0) {
    const freeVram = Math.max(0, t.vramTotalBytes - t.vramUsedBytes);
    gpuStats.push({
      label: "剩余可用",
      value: formatMetricBytes(freeVram),
    });
  }
  gpuStats.push({
    label: "核心负载",
    value:
      t.online && t.gpuUtilPercent !== null
        ? `${Math.round(t.gpuUtilPercent)}% · 峰值 ${Math.round(seriesPeak(t.gpuUtilSeries))}%`
        : t.online
          ? "暂未上报"
          : "--",
  });
  gpuStats.push({
    label: "温度",
    value: t.online && t.gpuTempC !== null ? `${Math.round(t.gpuTempC)}°C` : "--",
  });
  if (t.online && t.gpuPowerW !== null) {
    gpuStats.push({
      label: "功耗",
      value:
        t.gpuPowerLimitW !== null && t.gpuPowerLimitW > 0
          ? `${Math.round(t.gpuPowerW)} / ${Math.round(t.gpuPowerLimitW)} W`
          : `${Math.round(t.gpuPowerW)} W`,
    });
  }
  gpuStats.push({
    label: "装载模型",
    value:
      props.loadedModelName ||
      props.runtime.loaded_model ||
      (t.online ? "未装载模型" : "--"),
  });

  const ramStats: StackedRowStat[] = !t.online
    ? [
        { label: "可用内存", value: "--" },
        { label: "60 秒峰值", value: isConnecting.value ? "--" : "0%" },
      ]
    : !t.hasRamTelemetry
      ? [{ label: "上报状态", value: "等待远端服务上报…", muted: true }]
      : [
          {
            label: "可用内存",
            value: formatMetricBytes(t.ramAvailableBytes),
          },
          {
            label: "60 秒平均",
            value: `${seriesAverage(t.ramSeries).toFixed(1)}%`,
          },
          {
            label: "60 秒峰值",
            value: `${Math.round(seriesPeak(t.ramSeries))}%`,
          },
        ];

  const diskStats: StackedRowStat[] = [
    {
      label: !t.online || t.hasDiskTotal ? "已用 / 总容量" : "可用空间",
      value: !t.online
        ? "-- / --"
        : t.hasDiskTotal
          ? formatBytePair(t.diskUsedBytes, t.diskTotalBytes)
          : formatMetricBytes(t.diskFreeBytes),
    },
    {
      label: "读取",
      value: t.online ? formatRateBps(t.diskReadBps) : "0 KB/s",
    },
    {
      label: "写入",
      value: t.online ? formatRateBps(t.diskWriteBps) : "0 KB/s",
    },
  ];

  const cpuStats: StackedRowStat[] = !t.online
    ? [
        { label: "60 秒平均", value: isConnecting.value ? "--" : "0.0%" },
        { label: "60 秒峰值", value: isConnecting.value ? "--" : "0%" },
      ]
    : t.cpuPercent === null
      ? [{ label: "上报状态", value: "等待远端服务上报…", muted: true }]
      : [
          {
            label: "60 秒平均",
            value: `${seriesAverage(t.cpuSeries).toFixed(1)}%`,
          },
          {
            label: "60 秒峰值",
            value: `${Math.round(seriesPeak(t.cpuSeries))}%`,
          },
        ];

  return [
    {
      key: "gpu",
      title: "GPU 显存",
      subtitle: gpuSubtitle.value,
      icon: PhGraphicsCard,
      heroValue: t.online
        ? `${t.vramPercent.toFixed(1)}%`
        : isConnecting.value
          ? "--"
          : "0.0%",
      heroSub:
        t.online && t.vramTotalBytes > 0
          ? formatBytePair(t.vramUsedBytes, t.vramTotalBytes)
          : "-- / --",
      progressPercent: gpuProgressPercent.value,
      progressColor: "var(--co-rm-gpu)",
      seriesLabel: "显存占用 (%)",
      secondaryLabel:
        t.online && t.gpuUtilPercent !== null ? "核心负载 (%)" : "",
      scaleTop: "100%",
      showArea: t.online,
      primaryChart: gpuRowChart.value,
      secondaryChart: gpuUtilRowChart.value,
      stats: gpuStats,
    },
    {
      key: "ram",
      title: "系统内存 (RAM)",
      subtitle: t.hasRamTelemetry
        ? `总计 ${formatMetricBytes(t.ramTotalBytes)}`
        : isConnecting.value
          ? "等待远端主机上报…"
          : "主机物理内存",
      icon: PhMemory,
      heroValue: !t.online
        ? isConnecting.value
          ? "--"
          : "0.0%"
        : t.hasRamTelemetry
          ? `${t.ramPercent.toFixed(1)}%`
          : "--",
      heroSub:
        t.online && t.hasRamTelemetry
          ? formatBytePair(t.ramUsedBytes, t.ramTotalBytes)
          : "-- / --",
      progressPercent: ramProgressPercent.value,
      progressColor: "var(--co-rm-ram)",
      seriesLabel: "内存使用 (%)",
      secondaryLabel: "",
      scaleTop: "100%",
      showArea: t.online && t.hasRamTelemetry,
      primaryChart: ramRowChart.value,
      secondaryChart: null,
      stats: ramStats,
    },
    {
      key: "disk",
      title: "远端磁盘",
      subtitle: t.hasDiskTotal
        ? `实例卷 ${formatMetricBytes(t.diskTotalBytes)}`
        : isConnecting.value
          ? "等待远端工作盘上报…"
          : "工作盘容量与吞吐",
      icon: PhHardDrives,
      heroValue: !t.online
        ? isConnecting.value
          ? "--"
          : "0.0%"
        : t.hasDiskTotal
          ? `${t.diskPercent.toFixed(1)}%`
          : t.diskFreeBytes > 0
            ? formatMetricBytes(t.diskFreeBytes)
            : "--",
      compactHero: t.online && !t.hasDiskTotal && t.diskFreeBytes > 0,
      heroSub: !t.online
        ? "-- / --"
        : t.hasDiskTotal
          ? `剩余 ${formatMetricBytes(t.diskFreeBytes)}`
          : "可用空间",
      progressPercent: diskProgressPercent.value,
      progressColor: "var(--co-rm-disk)",
      seriesLabel:
        !t.online || t.hasDiskTotal
          ? "磁盘占用 (%)"
          : "读写吞吐 (MB/s)",
      secondaryLabel: hasDiskIoActivity.value ? "I/O 吞吐" : "",
      scaleTop:
        !t.online || t.hasDiskTotal
          ? "100%"
          : `${Math.round(diskRowChart.value.maxScale)} MB/s`,
      showArea: t.online,
      primaryChart: diskRowChart.value,
      secondaryChart: diskIoRowChart.value,
      stats: diskStats,
      driveBadge: effectiveDriveConnected.value
        ? effectiveDriveFreeBytes.value !== null &&
          effectiveDriveFreeBytes.value > 0
          ? `云盘可用 ${formatMetricBytes(effectiveDriveFreeBytes.value)}`
          : "云盘已挂载"
        : "",
    },
    {
      key: "cpu",
      title: "处理器 (CPU)",
      subtitle: cpuSubtitle.value,
      icon: PhCpu,
      heroValue: !t.online
        ? isConnecting.value
          ? "--"
          : "0.0%"
        : t.cpuPercent !== null
          ? `${t.cpuPercent.toFixed(1)}%`
          : "--",
      heroSub: t.online
        ? t.cpuCores
          ? `${t.cpuCores} vCPU`
          : "Colab vCPU"
        : "-- vCPU",
      progressPercent: cpuProgressPercent.value,
      progressColor: "var(--co-rm-cpu)",
      seriesLabel: "处理器负载 (%)",
      secondaryLabel: "",
      scaleTop: "100%",
      showArea: t.online && t.cpuPercent !== null,
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
    :class="{ 'is-offline': !telemetry.online, 'is-connecting': isConnecting }"
    :bordered="true"
    size="small"
    aria-label="远端设备资源监视器"
  >
    <!-- Monitor Top Header -->
    <template #title>
      <div class="co-rm-header-title">
        <div class="co-rm-heading-row">
          <h3>实时硬件监控</h3>
          <ABadge
            :status="headerBadgeStatus"
            :text="headerBadgeText"
            class="co-rm-live-badge"
            :class="{
              live: telemetry.online && !isIpcPaused,
              connecting: isConnecting || isIpcPaused,
            }"
          />
          <ASpace v-if="telemetry.online" :size="6" wrap class="co-rm-hw-pills">
            <ATag
              v-if="activeGpuDisplayName"
              :bordered="false"
              class="co-rm-hw-tag co-rm-hw-gpu"
            >
              <PhGraphicsCard :size="12" aria-hidden="true" />
              <span>{{ activeGpuDisplayName }}</span>
            </ATag>
            <ATooltip
              v-if="telemetry.gpuTempC !== null"
              title="GPU 核心温度"
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
            </ATooltip>
            <ATooltip
              v-if="telemetry.gpuPowerW !== null"
              title="GPU 实时功耗"
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
            </ATooltip>
          </ASpace>
          <span v-if="!telemetry.online" class="co-rm-header-sub">
            {{ offlineSubtitle }}
          </span>
          <span
            v-else-if="isIpcPaused || isStarting"
            class="co-rm-header-sub"
          >
            {{ onlineSubtitle }}
          </span>
        </div>
      </div>
    </template>

    <template #extra>
      <ASegmented
        :value="layout"
        :options="layoutOptions"
        size="small"
        class="co-rm-layout-segmented"
        aria-label="监控图表布局切换"
        @change="onLayoutChange"
      >
        <template #label="{ value, payload }">
          <ATooltip :title="payload.label" placement="top">
            <span class="co-rm-seg-label">
              <PhSquaresFour
                v-if="value === 'grid'"
                :size="15"
                aria-hidden="true"
              />
              <PhRows v-else :size="15" aria-hidden="true" />
              <span class="co-rm-sr-only">{{ payload.label }}</span>
            </span>
          </ATooltip>
        </template>
      </ASegmented>
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
        aria-label="查看 GPU 显存详情图表"
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
              <h4>GPU 显存</h4>
              <small>{{ gpuSubtitle }}</small>
            </div>
          </div>
          <strong class="co-rm-live-val co-rm-mono">
            {{
              telemetry.online
                ? `${Math.round(telemetry.vramPercent)}%`
                : isConnecting
                  ? "--"
                  : "0%"
            }}
          </strong>
        </div>

        <AProgress
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
          </svg>
          <span class="co-rm-scale-cap co-rm-mono">100%</span>
        </div>

        <div class="co-rm-axis-row co-rm-mono" aria-hidden="true">
          <span>60 秒前</span>
          <span>现在</span>
        </div>

        <div class="co-rm-card-footer">
          <span class="co-rm-kv">
            <em>已用 / 总量</em>
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
            <em>核心负载</em>
            <b class="co-rm-mono">{{ Math.round(telemetry.gpuUtilPercent) }}%</b>
          </span>
          <span v-if="telemetry.gpuTempC !== null" class="co-rm-kv">
            <em>温度</em>
            <b class="co-rm-mono">{{ Math.round(telemetry.gpuTempC) }}°C</b>
          </span>
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
        aria-label="查看系统内存详情图表"
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
              <h4>系统内存 (RAM)</h4>
              <small>{{
                telemetry.hasRamTelemetry
                  ? `总计 ${formatMetricBytes(telemetry.ramTotalBytes)}`
                  : isConnecting
                    ? "等待远端主机上报…"
                    : "主机物理内存"
              }}</small>
            </div>
          </div>
          <strong class="co-rm-live-val co-rm-mono">
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

        <AProgress
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
            <path
              v-if="telemetry.online && telemetry.hasRamTelemetry"
              :d="ramGridChart.areaPath"
              :fill="`url(#${uid}-grid-ram)`"
            />
            <path :d="ramGridChart.linePath" class="co-rm-primary-stroke" />
          </svg>
          <span class="co-rm-scale-cap co-rm-mono">100%</span>
        </div>

        <div class="co-rm-axis-row co-rm-mono" aria-hidden="true">
          <span>60 秒前</span>
          <span>现在</span>
        </div>

        <div class="co-rm-card-footer">
          <template v-if="telemetry.online && telemetry.hasRamTelemetry">
            <span class="co-rm-kv">
              <em>使用中</em>
              <b class="co-rm-mono">{{
                formatBytePair(telemetry.ramUsedBytes, telemetry.ramTotalBytes)
              }}</b>
            </span>
            <span class="co-rm-kv">
              <em>可用内存</em>
              <b class="co-rm-mono">{{
                formatMetricBytes(telemetry.ramAvailableBytes)
              }}</b>
            </span>
          </template>
          <span
            v-else-if="telemetry.online && !telemetry.hasRamTelemetry"
            class="co-rm-waiting-note"
          >
            等待远端服务上报…
          </span>
          <template v-else>
            <span class="co-rm-kv">
              <em>使用中</em>
              <b class="co-rm-mono">-- / --</b>
            </span>
            <span class="co-rm-kv">
              <em>可用内存</em>
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
        aria-label="查看远端磁盘详情图表"
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
              <h4>远端磁盘</h4>
              <small>{{
                telemetry.online && telemetry.hasDiskTotal
                  ? `已用 ${Math.round(telemetry.diskPercent)}%`
                  : isConnecting
                    ? "等待远端工作盘上报…"
                    : "工作盘容量与吞吐"
              }}</small>
            </div>
          </div>
          <strong class="co-rm-live-val co-rm-disk-val co-rm-mono">
            {{
              telemetry.online && telemetry.diskFreeBytes > 0
                ? `剩余 ${formatMetricBytes(telemetry.diskFreeBytes)}`
                : telemetry.online && telemetry.hasDiskTotal
                  ? `${Math.round(telemetry.diskPercent)}%`
                  : isConnecting
                    ? "--"
                    : "0%"
            }}
          </strong>
        </div>

        <AProgress
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
          </svg>
          <span class="co-rm-scale-cap co-rm-mono">{{
            !telemetry.online || telemetry.hasDiskTotal
              ? "100%"
              : `${Math.round(diskGridChart.maxScale)} MB/s`
          }}</span>
        </div>

        <div class="co-rm-axis-row co-rm-mono" aria-hidden="true">
          <span>60 秒前</span>
          <span>现在</span>
        </div>

        <div class="co-rm-card-footer">
          <span v-if="telemetry.online && telemetry.hasDiskTotal" class="co-rm-kv">
            <em>已用 / 总容量</em>
            <b class="co-rm-mono">{{
              formatBytePair(telemetry.diskUsedBytes, telemetry.diskTotalBytes)
            }}</b>
          </span>
          <span
            v-else-if="telemetry.online && telemetry.diskFreeBytes > 0"
            class="co-rm-kv"
          >
            <em>可用空间</em>
            <b class="co-rm-mono">{{ formatMetricBytes(telemetry.diskFreeBytes) }}</b>
          </span>
          <span v-else class="co-rm-kv">
            <em>已用 / 总容量</em>
            <b class="co-rm-mono">-- / --</b>
          </span>

          <span class="co-rm-kv">
            <em class="co-rm-mono"
              >读取 {{ formatRateBps(telemetry.diskReadBps) }} · 写入
              {{ formatRateBps(telemetry.diskWriteBps) }}</em
            >
          </span>

          <ATag
            v-if="effectiveDriveConnected"
            :bordered="false"
            class="co-rm-drive-badge co-rm-mono"
          >
            云盘可用
            {{
              effectiveDriveFreeBytes !== null && effectiveDriveFreeBytes > 0
                ? formatMetricBytes(effectiveDriveFreeBytes)
                : "已挂载"
            }}
          </ATag>
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
        aria-label="查看处理器详情图表"
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
              <h4>处理器 (CPU)</h4>
              <small>{{ cpuSubtitle }}</small>
            </div>
          </div>
          <strong class="co-rm-live-val co-rm-mono">
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

        <AProgress
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
            <path
              v-if="telemetry.online && telemetry.cpuPercent !== null"
              :d="cpuGridChart.areaPath"
              :fill="`url(#${uid}-grid-cpu)`"
            />
            <path :d="cpuGridChart.linePath" class="co-rm-primary-stroke" />
          </svg>
          <span class="co-rm-scale-cap co-rm-mono">100%</span>
        </div>

        <div class="co-rm-axis-row co-rm-mono" aria-hidden="true">
          <span>60 秒前</span>
          <span>现在</span>
        </div>

        <div class="co-rm-card-footer">
          <template v-if="telemetry.online && telemetry.cpuPercent !== null">
            <span class="co-rm-kv">
              <em>实时利用率</em>
              <b class="co-rm-mono">{{ Math.round(telemetry.cpuPercent) }}%</b>
            </span>
            <span class="co-rm-kv">
              <em>规格</em>
              <b class="co-rm-mono">{{
                telemetry.cpuCores ? `${telemetry.cpuCores} vCPU` : "Colab vCPU"
              }}</b>
            </span>
          </template>
          <span
            v-else-if="telemetry.online && telemetry.cpuPercent === null"
            class="co-rm-waiting-note"
          >
            等待远端服务上报…
          </span>
          <template v-else>
            <span class="co-rm-kv">
              <em>实时利用率</em>
              <b class="co-rm-mono">{{ isConnecting ? "--" : "0%" }}</b>
            </span>
            <span class="co-rm-kv">
              <em>规格</em>
              <b class="co-rm-mono">-- vCPU</b>
            </span>
          </template>
        </div>
      </ACard>
    </div>

    <!-- MODE 2: 4-Row Stacked Simultaneous Wide Charts -->
    <div v-else class="co-rm-rows" aria-label="四项资源实时宽幅曲线">
      <section
        v-for="row in stackedRows"
        :id="`${uid}-row-${row.key}`"
        :key="row.key"
        class="co-rm-row"
        :class="[
          `co-metric-${row.key}`,
          { 'is-focused': highlightedMetric === row.key },
        ]"
        :aria-label="`${row.title}实时监控`"
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
                <strong
                  class="co-rm-row-hero co-rm-mono"
                  :class="{ 'is-compact': row.compactHero }"
                >
                  {{ row.heroValue }}
                </strong>
              </div>
              <div class="co-rm-row-rail-subline">
                <small class="co-rm-row-subtitle" :title="row.subtitle">{{
                  row.subtitle
                }}</small>
                <span class="co-rm-row-hero-sub co-rm-mono" :title="row.heroSub">
                  {{ row.heroSub }}
                </span>
              </div>
            </div>
          </div>
          <AProgress
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
            <div class="co-rm-row-stats" aria-label="硬件实时读数">
              <span
                v-for="stat in row.stats"
                :key="`${row.key}-${stat.label}`"
                class="co-rm-kv co-rm-row-stat"
              >
                <em>{{ stat.label }}</em>
                <b :class="stat.muted ? 'co-rm-waiting-note' : 'co-rm-mono'">{{
                  stat.value
                }}</b>
              </span>
              <ATag
                v-if="row.driveBadge"
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
              :aria-label="`${row.title} 60 秒实时曲线`"
            >
              <defs>
                <linearGradient
                  :id="`${uid}-row-${row.key}`"
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
              <path
                v-if="row.showArea"
                :d="row.primaryChart.areaPath"
                :fill="`url(#${uid}-row-${row.key})`"
              />
              <path
                v-if="row.secondaryChart"
                :d="row.secondaryChart.linePath"
                class="co-rm-secondary-stroke"
              />
              <path :d="row.primaryChart.linePath" class="co-rm-primary-stroke" />
            </svg>
            <span class="co-rm-scale-cap co-rm-mono">{{ row.scaleTop }}</span>
          </div>
        </div>
      </section>

      <!-- Shared Synchronized 60s Time Axis -->
      <div class="co-rm-rows-axis-bar" aria-hidden="true">
        <span class="co-rm-rows-axis-caption">60 秒同步采样时间轴</span>
        <div class="co-rm-rows-axis-ticks co-rm-mono">
          <span>60 秒前</span>
          <span>40 秒前</span>
          <span>20 秒前</span>
          <span>现在</span>
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
  min-height: 44px;
  padding: 8px 16px;
  border-bottom: 1px solid var(--co-border-secondary);
  background: transparent;
}

.co-monitor :deep(.ant-card-head-wrapper) {
  align-items: center;
  gap: 12px;
}

.co-monitor :deep(.ant-card-head-title) {
  padding: 0;
  white-space: normal;
}

.co-monitor :deep(.ant-card-extra) {
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
}

.co-rm-heading-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 10px;
}

.co-rm-heading-row h3 {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
  margin: 0;
  white-space: nowrap;
}

.co-rm-live-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 9999px;
  background: var(--co-track);
  border: 1px solid var(--co-border);
  line-height: 1.3;
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

.co-rm-hw-pills {
  display: inline-flex;
  align-items: center;
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
}

.co-rm-seg-label {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 26px;
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

.is-connecting .co-rm-plot-frame::after {
  content: "";
  position: absolute;
  inset: 0;
  background: linear-gradient(
    90deg,
    transparent 0%,
    rgba(22, 119, 255, 0.1) 50%,
    transparent 100%
  );
  transform: translateX(-100%);
  animation: rm-shimmer 1.8s ease-in-out infinite;
  pointer-events: none;
}

@keyframes rm-shimmer {
  100% {
    transform: translateX(100%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .is-connecting .co-rm-plot-frame::after {
    animation: none;
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
  right: 6px;
  font-size: 10.5px;
  line-height: 1;
  color: var(--co-muted);
  opacity: 0.82;
  pointer-events: none;
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
  align-items: baseline;
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
  gap: 1px;
}

.co-rm-row-rail-line,
.co-rm-row-rail-subline {
  display: flex;
  align-items: baseline;
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

:root[data-theme="dark"] .co-monitor.is-connecting .co-rm-plot-frame::after {
  background: linear-gradient(
    90deg,
    transparent 0%,
    rgba(60, 137, 232, 0.14) 50%,
    transparent 100%
  );
}
</style>
