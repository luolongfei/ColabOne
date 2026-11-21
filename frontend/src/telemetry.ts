import { t } from "./i18n";
import type {
  AccountInstance,
  ColabOneSnapshot,
  MonitorOption,
  MonitorSnapshot,
  MonitorSource,
  ResourceHistoryPoint,
  RuntimeStatus,
} from "./types";

export const COLABONE_TELEMETRY_HISTORY_LENGTH = 60;

const KB = 1024;
const MB = 1024 ** 2;
const GB = 1024 ** 3;
const MIN_CLIENT_SAMPLE_INTERVAL_SEC = 0.9;
const CHART_INSET_PX = 1.5;

export interface ColabOneChartGeometry {
  linePath: string;
  areaPath: string;
  latestValue: number;
  maxScale: number;
  gridXOffsets: number[];
}

export interface ColabOneTelemetry {
  online: boolean;
  gpuName: string;
  vramUsedBytes: number;
  vramTotalBytes: number;
  vramPercent: number;
  gpuUtilPercent: number | null;
  gpuTempC: number | null;
  gpuPowerW: number | null;
  gpuPowerLimitW: number | null;
  ramUsedBytes: number;
  ramTotalBytes: number;
  ramAvailableBytes: number;
  ramPercent: number;
  hasRamTelemetry: boolean;
  diskUsedBytes: number;
  diskTotalBytes: number;
  diskFreeBytes: number;
  diskPercent: number;
  hasDiskTotal: boolean;
  diskReadBps: number;
  diskWriteBps: number;
  cpuCores: number | null;
  cpuPercent: number | null;
  vramSeries: number[];
  gpuUtilSeries: number[];
  ramSeries: number[];
  diskSeries: number[];
  diskIoSeries: number[];
  cpuSeries: number[];
  sampleTick: number;
}

export function clampPercent(value?: number | null): number {
  if (typeof value !== "number" || !Number.isFinite(value)) return 0;
  return Math.min(100, Math.max(0, value));
}

export function formatMetricBytes(bytes?: number | null): string {
  if (
    bytes === null ||
    bytes === undefined ||
    !Number.isFinite(bytes) ||
    bytes < 0
  ) {
    return "--";
  }
  if (bytes >= GB) {
    return `${(bytes / GB).toFixed(1)} GB`;
  }
  if (bytes >= MB) {
    return `${(bytes / MB).toFixed(0)} MB`;
  }
  if (bytes >= KB) {
    return `${(bytes / KB).toFixed(0)} KB`;
  }
  return `${bytes} B`;
}

export function formatRateBps(bps?: number | null): string {
  if (bps === null || bps === undefined || !Number.isFinite(bps) || bps <= 0) {
    return "0 KB/s";
  }
  if (bps >= MB) {
    return `${(bps / MB).toFixed(1)} MB/s`;
  }
  if (bps >= KB) {
    return `${(bps / KB).toFixed(0)} KB/s`;
  }
  return `${Math.round(bps)} B/s`;
}

function padSeries(
  values: number[],
  length: number = COLABONE_TELEMETRY_HISTORY_LENGTH,
): number[] {
  const sanitized = values.map((v) =>
    typeof v === "number" && Number.isFinite(v) && v > 0 ? v : 0,
  );
  if (sanitized.length >= length) {
    return sanitized.slice(sanitized.length - length);
  }
  return [
    ...new Array<number>(length - sanitized.length).fill(0),
    ...sanitized,
  ];
}

function formatCoord(value: number): string {
  return String(Math.round(value * 100) / 100);
}

export function buildColabOneChartGeometry(
  values: number[],
  width: number,
  height: number,
  sampleTick: number = 0,
  maxScale: number = 100,
): ColabOneChartGeometry {
  const safeWidth =
    typeof width === "number" && Number.isFinite(width) && width > 0
      ? width
      : 0;
  const safeHeight =
    typeof height === "number" && Number.isFinite(height) && height > 0
      ? height
      : 0;
  const safeScale =
    typeof maxScale === "number" && Number.isFinite(maxScale) && maxScale > 0
      ? maxScale
      : 100;
  const safeTick =
    typeof sampleTick === "number" && Number.isFinite(sampleTick)
      ? sampleTick
      : 0;

  const padded = padSeries(values, COLABONE_TELEMETRY_HISTORY_LENGTH);
  const effectiveMax = Math.max(safeScale, ...padded, 1);
  const latestValue = padded[COLABONE_TELEMETRY_HISTORY_LENGTH - 1] ?? 0;

  const inset = safeHeight > CHART_INSET_PX * 2 ? CHART_INSET_PX : 0;
  const usableHeight = Math.max(0, safeHeight - inset * 2);
  const lastIndex = Math.max(1, COLABONE_TELEMETRY_HISTORY_LENGTH - 1);

  const commands: string[] = new Array(COLABONE_TELEMETRY_HISTORY_LENGTH);
  for (let i = 0; i < COLABONE_TELEMETRY_HISTORY_LENGTH; i++) {
    const x = (i / lastIndex) * safeWidth;
    const clamped = Math.min(effectiveMax, Math.max(0, padded[i]));
    const y = safeHeight - inset - (clamped / effectiveMax) * usableHeight;
    commands[i] = `${i === 0 ? "M" : "L"} ${formatCoord(x)} ${formatCoord(y)}`;
  }

  const linePath = commands.join(" ");
  const areaPath = `${linePath} L ${formatCoord(safeWidth)} ${formatCoord(safeHeight)} L 0 ${formatCoord(safeHeight)} Z`;

  const gridXOffsets: number[] = [];
  if (safeWidth > 0) {
    const step = safeWidth / 6;
    const modTick = ((safeTick % 10) + 10) % 10;
    const shift = (modTick / 10) * step;
    for (let i = 1; i <= 6; i++) {
      const x = Math.round((i * step - shift) * 100) / 100;
      if (x > 0 && x < safeWidth) {
        gridXOffsets.push(x);
      }
    }
  }

  return {
    linePath,
    areaPath,
    latestValue,
    maxScale: effectiveMax,
    gridXOffsets,
  };
}

function createEmptyTelemetry(): ColabOneTelemetry {
  return {
    online: false,
    gpuName: "",
    vramUsedBytes: 0,
    vramTotalBytes: 0,
    vramPercent: 0,
    gpuUtilPercent: null,
    gpuTempC: null,
    gpuPowerW: null,
    gpuPowerLimitW: null,
    ramUsedBytes: 0,
    ramTotalBytes: 0,
    ramAvailableBytes: 0,
    ramPercent: 0,
    hasRamTelemetry: false,
    diskUsedBytes: 0,
    diskTotalBytes: 0,
    diskFreeBytes: 0,
    diskPercent: 0,
    hasDiskTotal: false,
    diskReadBps: 0,
    diskWriteBps: 0,
    cpuCores: null,
    cpuPercent: null,
    vramSeries: new Array<number>(COLABONE_TELEMETRY_HISTORY_LENGTH).fill(0),
    gpuUtilSeries: new Array<number>(COLABONE_TELEMETRY_HISTORY_LENGTH).fill(0),
    ramSeries: new Array<number>(COLABONE_TELEMETRY_HISTORY_LENGTH).fill(0),
    diskSeries: new Array<number>(COLABONE_TELEMETRY_HISTORY_LENGTH).fill(0),
    diskIoSeries: new Array<number>(COLABONE_TELEMETRY_HISTORY_LENGTH).fill(0),
    cpuSeries: new Array<number>(COLABONE_TELEMETRY_HISTORY_LENGTH).fill(0),
    sampleTick: 0,
  };
}

function finiteOrNull(value?: number | null): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function nonNegativeNumber(value?: number | null): number {
  return typeof value === "number" && Number.isFinite(value) && value > 0
    ? value
    : 0;
}

export class ColabOneTelemetryTracker {
  activeId = "";
  history: ResourceHistoryPoint[] = [];
  lastSampleTime = 0;
  sampleTick = 0;
  private lastServerTime = 0;

  reset(): void {
    this.activeId = "";
    this.history = [];
    this.lastSampleTime = 0;
    this.lastServerTime = 0;
    this.sampleTick = 0;
  }

  pushSnapshot(
    runtime: RuntimeStatus,
    nowSec: number = Date.now() / 1000,
  ): ColabOneTelemetry {
    if (!runtime || runtime.state === "stopped") {
      if (
        this.activeId !== "" ||
        this.history.length > 0 ||
        this.lastSampleTime !== 0 ||
        this.lastServerTime !== 0 ||
        this.sampleTick !== 0
      ) {
        this.reset();
      }
      return createEmptyTelemetry();
    }

    const hasIncomingPayload = Boolean(
      runtime.gpu?.total_mib ||
        runtime.gpu?.name?.trim() ||
        runtime.resources?.gpu?.total_mib ||
        runtime.resources?.gpu?.name?.trim() ||
        runtime.resources?.memory?.total_bytes ||
        runtime.resources?.disk?.total_bytes ||
        runtime.resources?.cpu?.cores ||
        (runtime.resources?.history?.length ?? 0) > 0,
    );

    if (
      !runtime.id &&
      runtime.state === "starting" &&
      !hasIncomingPayload &&
      this.activeId !== ""
    ) {
      this.reset();
    } else if (runtime.id && runtime.id !== this.activeId) {
      const switchingInstance = this.activeId !== "";
      this.activeId = runtime.id;
      if (switchingInstance) {
        this.history = [];
        this.lastSampleTime = 0;
        this.lastServerTime = 0;
        this.sampleTick = 0;
      }
    }

    const gpu = runtime.resources?.gpu || runtime.gpu;
    const gpuName = (gpu?.name || runtime.gpu?.name || "").trim();
    const vramTotalBytes =
      gpu && Number.isFinite(gpu.total_mib) && gpu.total_mib > 0
        ? Math.round(gpu.total_mib * MB)
        : 0;
    const vramUsedBytes =
      gpu && Number.isFinite(gpu.used_mib) && gpu.used_mib > 0
        ? Math.round(gpu.used_mib * MB)
        : 0;
    const vramPercent =
      vramTotalBytes > 0
        ? clampPercent((vramUsedBytes / vramTotalBytes) * 100)
        : 0;

    const rawGpuUtil = finiteOrNull(
      gpu?.util_percent ?? runtime.gpu?.util_percent,
    );
    const gpuUtilPercent =
      rawGpuUtil !== null ? clampPercent(rawGpuUtil) : null;
    const gpuTempC = finiteOrNull(gpu?.temp_c ?? runtime.gpu?.temp_c);
    const gpuPowerW = finiteOrNull(gpu?.power_w ?? runtime.gpu?.power_w);
    const gpuPowerLimitW = finiteOrNull(
      gpu?.power_limit_w ?? runtime.gpu?.power_limit_w,
    );

    const mem = runtime.resources?.memory;
    const hasRamTelemetry = Boolean(
      mem && Number.isFinite(mem.total_bytes) && mem.total_bytes > 0,
    );
    const ramTotalBytes = hasRamTelemetry
      ? nonNegativeNumber(mem?.total_bytes)
      : 0;
    const ramUsedBytes = hasRamTelemetry
      ? nonNegativeNumber(mem?.used_bytes)
      : 0;
    const ramAvailableBytes = hasRamTelemetry
      ? mem && Number.isFinite(mem.available_bytes)
        ? Math.max(0, mem.available_bytes)
        : Math.max(0, ramTotalBytes - ramUsedBytes)
      : 0;
    const ramPercent = hasRamTelemetry
      ? mem && Number.isFinite(mem.percent)
        ? clampPercent(mem.percent)
        : ramTotalBytes > 0
          ? clampPercent((ramUsedBytes / ramTotalBytes) * 100)
          : 0
      : 0;

    const disk = runtime.resources?.disk;
    const hasDiskTotal = Boolean(
      disk && Number.isFinite(disk.total_bytes) && disk.total_bytes > 0,
    );
    const diskTotalBytes = hasDiskTotal
      ? nonNegativeNumber(disk?.total_bytes)
      : 0;
    const diskUsedBytes = disk ? nonNegativeNumber(disk.used_bytes) : 0;
    const diskFreeBytes =
      disk && Number.isFinite(disk.free_bytes)
        ? nonNegativeNumber(disk.free_bytes)
        : nonNegativeNumber(runtime.disk_free);
    const diskPercent = disk
      ? Number.isFinite(disk.percent)
        ? clampPercent(disk.percent)
        : diskTotalBytes > 0
          ? clampPercent((diskUsedBytes / diskTotalBytes) * 100)
          : 0
      : 0;
    const diskReadBps = disk ? nonNegativeNumber(disk.read_bps) : 0;
    const diskWriteBps = disk ? nonNegativeNumber(disk.write_bps) : 0;

    const cpu = runtime.resources?.cpu;
    const cpuCores =
      cpu && Number.isFinite(cpu.cores) && cpu.cores > 0 ? cpu.cores : null;
    const rawCpuPct = finiteOrNull(cpu?.percent);
    const cpuPercent = rawCpuPct !== null ? clampPercent(rawCpuPct) : null;

    const hasTelemetryData = Boolean(
      vramTotalBytes > 0 ||
        gpuName ||
        hasRamTelemetry ||
        hasDiskTotal ||
        cpuCores !== null ||
        cpuPercent !== null ||
        (runtime.resources?.history?.length ?? 0) > 0,
    );
    const online =
      runtime.state === "ready" ||
      runtime.state === "running" ||
      (runtime.state !== "disconnected" &&
        runtime.state !== "failed" &&
        runtime.state !== "releasing" &&
        hasTelemetryData);

    const serverHistory = runtime.resources?.history;
    let activePoints: ResourceHistoryPoint[];

    if (serverHistory && serverHistory.length > 0) {
      const validServer = serverHistory.filter(
        (p) => p && typeof p.t === "number" && Number.isFinite(p.t),
      );
      if (validServer.length > 0) {
        if (this.history.length === 0) {
          this.history = validServer.slice(-COLABONE_TELEMETRY_HISTORY_LENGTH);
        } else {
          const byTime = new Map<number, ResourceHistoryPoint>();
          for (const pt of this.history) {
            byTime.set(pt.t, pt);
          }
          for (const pt of validServer) {
            byTime.set(pt.t, pt);
          }
          this.history = Array.from(byTime.values())
            .sort((a, b) => a.t - b.t)
            .slice(-COLABONE_TELEMETRY_HISTORY_LENGTH);
        }
        const latestT = this.history[this.history.length - 1]?.t ?? 0;
        if (Number.isFinite(latestT) && latestT > this.lastServerTime) {
          this.lastServerTime = latestT;
          this.sampleTick += 1;
        }
      }
      activePoints = this.history;
    } else {
      const safeNow = Number.isFinite(nowSec) ? nowSec : Date.now() / 1000;
      const hasAnyTelemetry = Boolean(
        online || gpu || runtime.resources || runtime.disk_free !== undefined,
      );
      if (
        hasAnyTelemetry &&
        (this.history.length === 0 ||
          safeNow - this.lastSampleTime >= MIN_CLIENT_SAMPLE_INTERVAL_SEC)
      ) {
        const point: ResourceHistoryPoint = {
          t: safeNow,
          vram_pct: vramPercent,
          ram_pct: ramPercent,
          disk_pct: diskPercent,
          disk_read_bps: diskReadBps,
          disk_write_bps: diskWriteBps,
          ...(gpuUtilPercent !== null ? { gpu_util: gpuUtilPercent } : {}),
          ...(cpuPercent !== null ? { cpu_pct: cpuPercent } : {}),
        };
        this.history.push(point);
        if (this.history.length > COLABONE_TELEMETRY_HISTORY_LENGTH) {
          this.history = this.history.slice(
            this.history.length - COLABONE_TELEMETRY_HISTORY_LENGTH,
          );
        }
        this.lastSampleTime = safeNow;
        this.sampleTick += 1;
      }
      activePoints = this.history;
    }

    const vramSeries = padSeries(
      activePoints.map((p) => clampPercent(p.vram_pct)),
    );
    const gpuUtilSeries = padSeries(
      activePoints.map((p) => clampPercent(p.gpu_util ?? 0)),
    );
    const ramSeries = padSeries(
      activePoints.map((p) => clampPercent(p.ram_pct)),
    );
    const diskSeries = padSeries(
      activePoints.map((p) => clampPercent(p.disk_pct)),
    );
    const diskIoSeries = padSeries(
      activePoints.map((p) => {
        const r = nonNegativeNumber(p.disk_read_bps);
        const w = nonNegativeNumber(p.disk_write_bps);
        return (r + w) / MB;
      }),
    );
    const cpuSeries = padSeries(
      activePoints.map((p) => clampPercent(p.cpu_pct ?? 0)),
    );

    return {
      online,
      gpuName,
      vramUsedBytes,
      vramTotalBytes,
      vramPercent,
      gpuUtilPercent,
      gpuTempC,
      gpuPowerW,
      gpuPowerLimitW,
      ramUsedBytes,
      ramTotalBytes,
      ramAvailableBytes,
      ramPercent,
      hasRamTelemetry,
      diskUsedBytes,
      diskTotalBytes,
      diskFreeBytes,
      diskPercent,
      hasDiskTotal,
      diskReadBps,
      diskWriteBps,
      cpuCores,
      cpuPercent,
      vramSeries,
      gpuUtilSeries,
      ramSeries,
      diskSeries,
      diskIoSeries,
      cpuSeries,
      sampleTick: this.sampleTick,
    };
  }
}

export function colabOneShortEndpoint(endpoint: string, maxLen = 18): string {
  const cleaned = (endpoint || "").trim();
  if (!cleaned || cleaned.length <= maxLen) return cleaned;
  const parts = cleaned.split("-");
  const tail = parts[parts.length - 1] || "";
  if (tail.length >= 6 && tail.length <= maxLen - 3) {
    return `…${tail.slice(-Math.min(tail.length, 10))}`;
  }
  return `${cleaned.slice(0, Math.max(4, maxLen - 7))}…${cleaned.slice(-6)}`;
}

export function colabOneMonitorSourceLabel(source: MonitorSource): string {
  if (source === "local") return t("monitor.sourceLocal");
  if (source === "remote_colabone") return t("monitor.sourceRemoteColabOne");
  if (source === "remote_readonly") return t("monitor.sourceRemoteReadonly");
  return "";
}

function isGpuAccountInstance(inst: AccountInstance): boolean {
  const variant = String(inst.variant || "").toUpperCase();
  const accel = String(inst.accelerator || "").toUpperCase();
  if (variant === "GPU") return true;
  return accel !== "" && accel !== "CPU" && accel !== "NONE" && accel !== "TPU";
}

export function resolveColabOneMonitorFallback(
  snapshot: ColabOneSnapshot,
  loadedModelName = "",
): MonitorSnapshot {
  if (snapshot.monitor) {
    return snapshot.monitor;
  }

  const rawInstances = snapshot.account?.instances || [];
  const activeId = snapshot.runtime?.id || snapshot.active_endpoint || "";
  const managed: AccountInstance[] = [];
  const extGpu: AccountInstance[] = [];
  const extOther: AccountInstance[] = [];
  const seen = new Set<string>();

  for (const item of rawInstances) {
    if (!item?.id || seen.has(item.id)) continue;
    seen.add(item.id);
    if (item.managed) managed.push(item);
    else if (isGpuAccountInstance(item)) extGpu.push(item);
    else extOther.push(item);
  }
  managed.sort((a, b) => {
    const aActive = a.active || a.id === activeId ? 0 : 1;
    const bActive = b.active || b.id === activeId ? 0 : 1;
    return aActive - bActive;
  });

  if (
    !seen.has(snapshot.runtime?.id || "") &&
    snapshot.runtime?.id &&
    snapshot.runtime.state !== "stopped"
  ) {
    managed.unshift({
      id: snapshot.runtime.id,
      managed: true,
      active: true,
      accelerator: snapshot.settings?.gpu || "GPU",
      variant: "GPU",
      shape: "",
      state: snapshot.runtime.state,
      gpu_name: snapshot.runtime.gpu?.name,
      loaded_model: loadedModelName,
      location: snapshot.runtime.location,
    });
  }

  const ordered = [...managed, ...extGpu, ...extOther];
  const options: MonitorOption[] = ordered.map((item) => ({
    id: item.id,
    managed: Boolean(item.managed),
    active: Boolean(item.managed && (item.active || item.id === activeId)),
    accelerator: item.accelerator || (item.variant === "CPU" ? "CPU" : "GPU"),
    variant: item.variant || "GPU",
    shape: item.shape || "",
    location: item.location ?? null,
    gpu_name: item.gpu_name || "",
    loaded_model: item.loaded_model || "",
    source: item.managed ? "local" : "remote_readonly",
    state: item.managed ? item.state || "ready" : "connecting",
  }));

  if (managed.length > 0 || snapshot.runtime?.state !== "stopped") {
    const targetId =
      (activeId && managed.some((m) => m.id === activeId) ? activeId : "") ||
      managed[0]?.id ||
      snapshot.runtime?.id ||
      "";
    return {
      mode: "auto",
      endpoint: targetId,
      managed: true,
      source: "local",
      paused: false,
      error: "",
      runtime: snapshot.runtime,
      loaded_model_name: loadedModelName,
      drive_connected: Boolean(
        snapshot.setup?.drive || snapshot.runtime?.drive?.connected,
      ),
      drive_free_bytes: snapshot.runtime?.drive?.free_bytes ?? null,
      options,
    };
  }

  const firstRemote = extGpu[0] || extOther[0];
  if (firstRemote) {
    return {
      mode: "auto",
      endpoint: firstRemote.id,
      managed: false,
      source: "remote_readonly",
      paused: false,
      error: "",
      runtime: {
        id: firstRemote.id,
        state: "connecting",
        location: firstRemote.location ?? null,
      },
      loaded_model_name: "",
      drive_connected: false,
      drive_free_bytes: null,
      options,
    };
  }

  return {
    mode: "auto",
    endpoint: "",
    managed: false,
    source: "none",
    paused: false,
    error: "",
    runtime: { state: "stopped" },
    loaded_model_name: "",
    drive_connected: false,
    drive_free_bytes: null,
    options,
  };
}
