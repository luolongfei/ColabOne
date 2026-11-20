import { describe, expect, it } from "vitest";
import {
  COLABONE_TELEMETRY_HISTORY_LENGTH,
  ColabOneTelemetryTracker,
  buildColabOneChartGeometry,
  clampPercent,
  formatMetricBytes,
  formatRateBps,
} from "./telemetry";
import type { RuntimeStatus } from "./types";

const KB = 1024;
const MB = 1024 ** 2;
const GB = 1024 ** 3;

describe("Telemetry unit formatting & clamping", () => {
  it("clamps percentages into [0, 100] and handles non-finite inputs", () => {
    expect(clampPercent(null)).toBe(0);
    expect(clampPercent(undefined)).toBe(0);
    expect(clampPercent(Number.NaN)).toBe(0);
    expect(clampPercent(Number.POSITIVE_INFINITY)).toBe(0);
    expect(clampPercent(-12.5)).toBe(0);
    expect(clampPercent(0)).toBe(0);
    expect(clampPercent(47.25)).toBe(47.25);
    expect(clampPercent(100)).toBe(100);
    expect(clampPercent(145)).toBe(100);
  });

  it("formats metric byte counts across null, 0, B, KB, MB, and GB", () => {
    expect(formatMetricBytes(null)).toBe("--");
    expect(formatMetricBytes(undefined)).toBe("--");
    expect(formatMetricBytes(Number.NaN)).toBe("--");
    expect(formatMetricBytes(-1)).toBe("--");

    expect(formatMetricBytes(0)).toBe("0 B");
    expect(formatMetricBytes(512)).toBe("512 B");
    expect(formatMetricBytes(KB)).toBe("1 KB");
    expect(formatMetricBytes(640 * KB)).toBe("640 KB");
    expect(formatMetricBytes(MB)).toBe("1 MB");
    expect(formatMetricBytes(512 * MB)).toBe("512 MB");
    expect(formatMetricBytes(GB)).toBe("1.0 GB");
    expect(formatMetricBytes(15.36 * GB)).toBe("15.4 GB");
  });

  it("formats throughput rates across null, 0, B/s, KB/s, and MB/s", () => {
    expect(formatRateBps(null)).toBe("0 KB/s");
    expect(formatRateBps(undefined)).toBe("0 KB/s");
    expect(formatRateBps(Number.NaN)).toBe("0 KB/s");
    expect(formatRateBps(-100)).toBe("0 KB/s");
    expect(formatRateBps(0)).toBe("0 KB/s");

    expect(formatRateBps(512.4)).toBe("512 B/s");
    expect(formatRateBps(KB)).toBe("1 KB/s");
    expect(formatRateBps(320 * KB)).toBe("320 KB/s");
    expect(formatRateBps(MB)).toBe("1.0 MB/s");
    expect(formatRateBps(42.75 * MB)).toBe("42.8 MB/s");
  });
});

describe("buildColabOneChartGeometry SVG path & grid generation", () => {
  it("builds valid 60-point M/L linePath and closed Z areaPath for empty arrays", () => {
    const geom = buildColabOneChartGeometry([], 240, 63, 0, 100);

    expect(geom.latestValue).toBe(0);
    expect(geom.maxScale).toBe(100);
    expect(geom.linePath).toMatch(/^M 0 61\.5(?: L \d+(?:\.\d+)? 61\.5){59}$/);
    expect(geom.linePath.endsWith("L 240 61.5")).toBe(true);
    expect(geom.areaPath).toBe(`${geom.linePath} L 240 63 L 0 63 Z`);
    expect(geom.gridXOffsets).toEqual([40, 80, 120, 160, 200]);
  });

  it("right-aligns partial series and maps coordinates accurately", () => {
    const geom = buildColabOneChartGeometry([25, 50, 100], 240, 63, 5, 100);

    expect(geom.latestValue).toBe(100);
    expect(geom.maxScale).toBe(100);

    const segments = geom.linePath.split(/(?= [ML] )/).map((s) => s.trim());
    expect(segments).toHaveLength(COLABONE_TELEMETRY_HISTORY_LENGTH);
    // First 57 padded slots remain at baseline y = 61.5
    expect(segments[0]).toBe("M 0 61.5");
    expect(segments[56]?.endsWith(" 61.5")).toBe(true);
    // Last 3 slots correspond to 25% (y=46.5), 50% (y=31.5), and 100% (y=1.5)
    expect(segments[57]?.endsWith(" 46.5")).toBe(true);
    expect(segments[58]?.endsWith(" 31.5")).toBe(true);
    expect(segments[59]).toBe("L 240 1.5");
    expect(geom.areaPath.endsWith("L 240 63 L 0 63 Z")).toBe(true);

    // Scrolling grid offsets stay strictly within (0, width)
    expect(geom.gridXOffsets).toEqual([20, 60, 100, 140, 180, 220]);
    for (const x of geom.gridXOffsets) {
      expect(x).toBeGreaterThan(0);
      expect(x).toBeLessThan(240);
    }
  });

  it("handles full 60-point arrays, dynamic scale expansion, and scrolling ticks", () => {
    const fullSeries = Array.from({ length: COLABONE_TELEMETRY_HISTORY_LENGTH }, (_, idx) =>
      idx === COLABONE_TELEMETRY_HISTORY_LENGTH - 1 ? 250 : idx * 2,
    );
    const geom = buildColabOneChartGeometry(fullSeries, 300, 83, 13, 100);

    expect(geom.latestValue).toBe(250);
    expect(geom.maxScale).toBe(250);
    expect(geom.linePath.startsWith("M 0 81.5 L ")).toBe(true);
    expect(geom.linePath.endsWith("L 300 1.5")).toBe(true);
    expect(geom.areaPath).toBe(`${geom.linePath} L 300 83 L 0 83 Z`);

    expect(geom.gridXOffsets.length).toBeGreaterThan(0);
    for (const x of geom.gridXOffsets) {
      expect(x).toBeGreaterThan(0);
      expect(x).toBeLessThan(300);
    }
  });
});

describe("ColabOneTelemetryTracker state normalization & history tracking", () => {
  it("returns online: false and 60-zero series when runtime.state === 'stopped'", () => {
    const tracker = new ColabOneTelemetryTracker();
    const snap = tracker.pushSnapshot({ state: "stopped" }, 1000);

    expect(COLABONE_TELEMETRY_HISTORY_LENGTH).toBe(60);
    expect(snap.online).toBe(false);
    expect(snap.gpuName).toBe("");
    expect(snap.vramPercent).toBe(0);
    expect(snap.gpuUtilPercent).toBeNull();
    expect(snap.cpuPercent).toBeNull();
    expect(snap.hasRamTelemetry).toBe(false);
    expect(snap.hasDiskTotal).toBe(false);

    const expectedZeros = new Array<number>(60).fill(0);
    expect(snap.vramSeries).toEqual(expectedZeros);
    expect(snap.gpuUtilSeries).toEqual(expectedZeros);
    expect(snap.ramSeries).toEqual(expectedZeros);
    expect(snap.diskSeries).toEqual(expectedZeros);
    expect(snap.diskIoSeries).toEqual(expectedZeros);
    expect(snap.cpuSeries).toEqual(expectedZeros);
  });

  it("uses runtime.resources (cpu, memory, disk, gpu, history) when provided by upgraded remote runtime", () => {
    const tracker = new ColabOneTelemetryTracker();
    const runtime: RuntimeStatus = {
      id: "ep-upgraded",
      state: "ready",
      resources: {
        cpu: { cores: 8, percent: 36.5 },
        memory: {
          total_bytes: 50 * GB,
          used_bytes: 20 * GB,
          available_bytes: 30 * GB,
          percent: 40,
        },
        disk: {
          total_bytes: 100 * GB,
          used_bytes: 35 * GB,
          free_bytes: 65 * GB,
          percent: 35,
          read_bps: 12 * MB,
          write_bps: 4 * MB,
        },
        gpu: {
          name: "NVIDIA A100-SXM4-40GB",
          total_mib: 40960,
          used_mib: 10240,
          util_percent: 82,
          temp_c: 64,
          power_w: 215,
          power_limit_w: 400,
        },
        history: [
          {
            t: 100,
            vram_pct: 20,
            gpu_util: 55,
            ram_pct: 38,
            cpu_pct: 22,
            disk_pct: 34,
            disk_read_bps: 8 * MB,
            disk_write_bps: 2 * MB,
          },
          {
            t: 102,
            vram_pct: 25,
            gpu_util: 82,
            ram_pct: 40,
            cpu_pct: 36.5,
            disk_pct: 35,
            disk_read_bps: 12 * MB,
            disk_write_bps: 4 * MB,
          },
        ],
      },
    };

    const snap = tracker.pushSnapshot(runtime, 102);

    expect(snap.online).toBe(true);
    expect(snap.gpuName).toBe("NVIDIA A100-SXM4-40GB");
    expect(snap.vramTotalBytes).toBe(40960 * MB);
    expect(snap.vramUsedBytes).toBe(10240 * MB);
    expect(snap.vramPercent).toBe(25);
    expect(snap.gpuUtilPercent).toBe(82);
    expect(snap.gpuTempC).toBe(64);
    expect(snap.gpuPowerW).toBe(215);
    expect(snap.gpuPowerLimitW).toBe(400);

    expect(snap.hasRamTelemetry).toBe(true);
    expect(snap.ramTotalBytes).toBe(50 * GB);
    expect(snap.ramUsedBytes).toBe(20 * GB);
    expect(snap.ramAvailableBytes).toBe(30 * GB);
    expect(snap.ramPercent).toBe(40);

    expect(snap.hasDiskTotal).toBe(true);
    expect(snap.diskTotalBytes).toBe(100 * GB);
    expect(snap.diskUsedBytes).toBe(35 * GB);
    expect(snap.diskFreeBytes).toBe(65 * GB);
    expect(snap.diskPercent).toBe(35);
    expect(snap.diskReadBps).toBe(12 * MB);
    expect(snap.diskWriteBps).toBe(4 * MB);

    expect(snap.cpuCores).toBe(8);
    expect(snap.cpuPercent).toBe(36.5);

    expect(snap.vramSeries.slice(-3)).toEqual([0, 20, 25]);
    expect(snap.gpuUtilSeries.slice(-3)).toEqual([0, 55, 82]);
    expect(snap.ramSeries.slice(-3)).toEqual([0, 38, 40]);
    expect(snap.cpuSeries.slice(-3)).toEqual([0, 22, 36.5]);
    expect(snap.diskSeries.slice(-3)).toEqual([0, 34, 35]);
    expect(snap.diskIoSeries.slice(-3)).toEqual([0, 10, 16]);
    expect(snap.sampleTick).toBe(1);

    // Pushing identical server timestamp does not double-increment sampleTick
    const duplicate = tracker.pushSnapshot(runtime, 102.2);
    expect(duplicate.sampleTick).toBe(1);
  });

  it("falls back cleanly to runtime.gpu + runtime.disk_free and accumulates client-side rolling samples", () => {
    const tracker = new ColabOneTelemetryTracker();

    const first = tracker.pushSnapshot(
      {
        id: "ep-legacy",
        state: "ready",
        gpu: {
          name: "Tesla T4",
          total_mib: 15360,
          used_mib: 3840,
        },
        disk_free: 48 * GB,
      },
      1000,
    );

    expect(first.online).toBe(true);
    expect(first.gpuName).toBe("Tesla T4");
    expect(first.vramTotalBytes).toBe(15360 * MB);
    expect(first.vramUsedBytes).toBe(3840 * MB);
    expect(first.vramPercent).toBe(25);
    expect(first.hasRamTelemetry).toBe(false);
    expect(first.hasDiskTotal).toBe(false);
    expect(first.diskFreeBytes).toBe(48 * GB);
    expect(first.cpuPercent).toBeNull();
    expect(first.vramSeries.slice(-2)).toEqual([0, 25]);
    expect(first.sampleTick).toBe(1);

    // Sub-interval poll (< 0.9s) reuses previous client sample
    const throttled = tracker.pushSnapshot(
      {
        id: "ep-legacy",
        state: "ready",
        gpu: { name: "Tesla T4", total_mib: 15360, used_mib: 7680 },
        disk_free: 47 * GB,
      },
      1000.4,
    );
    expect(throttled.sampleTick).toBe(1);
    expect(throttled.vramSeries.slice(-2)).toEqual([0, 25]);

    // Next sample after >= 0.9s appends to rolling client history
    const second = tracker.pushSnapshot(
      {
        id: "ep-legacy",
        state: "ready",
        gpu: { name: "Tesla T4", total_mib: 15360, used_mib: 7680 },
        disk_free: 47 * GB,
      },
      1001.2,
    );
    expect(second.sampleTick).toBe(2);
    expect(second.vramPercent).toBe(50);
    expect(second.vramSeries.slice(-3)).toEqual([0, 25, 50]);
  });

  it("resets local history cleanly when switching runtime.id from 'ep-1' to 'ep-2'", () => {
    const tracker = new ColabOneTelemetryTracker();

    tracker.pushSnapshot(
      {
        id: "ep-1",
        state: "ready",
        gpu: { name: "Tesla T4", total_mib: 16000, used_mib: 8000 },
      },
      10,
    );
    const ep1Second = tracker.pushSnapshot(
      {
        id: "ep-1",
        state: "ready",
        gpu: { name: "Tesla T4", total_mib: 16000, used_mib: 12000 },
      },
      12,
    );
    expect(ep1Second.vramSeries.slice(-3)).toEqual([0, 50, 75]);
    expect(ep1Second.sampleTick).toBe(2);

    const ep2First = tracker.pushSnapshot(
      {
        id: "ep-2",
        state: "ready",
        gpu: { name: "NVIDIA L4", total_mib: 24000, used_mib: 2400 },
      },
      13,
    );
    expect(tracker.activeId).toBe("ep-2");
    expect(ep2First.gpuName).toBe("NVIDIA L4");
    expect(ep2First.sampleTick).toBe(1);
    // Previous ep-1 samples (50, 75) must be cleared completely
    expect(ep2First.vramSeries.slice(-3)).toEqual([0, 0, 10]);

    // Stopping runtime also resets activeId and history
    const stopped = tracker.pushSnapshot({ id: "ep-2", state: "stopped" }, 15);
    expect(stopped.online).toBe(false);
    expect(tracker.activeId).toBe("");
    expect(tracker.history).toEqual([]);
  });

  it("merges boot telemetry seamlessly into server history for the same instance without resetting", () => {
    const tracker = new ColabOneTelemetryTracker();
    const boot = tracker.pushSnapshot(
      {
        id: "ep-boot",
        state: "starting",
        resources: {
          cpu: { cores: 4, percent: 45 },
          memory: {
            total_bytes: 16 * GB,
            used_bytes: 4 * GB,
            available_bytes: 12 * GB,
            percent: 25,
          },
          disk: {
            total_bytes: 100 * GB,
            used_bytes: 30 * GB,
            free_bytes: 70 * GB,
            percent: 30,
            read_bps: 10 * MB,
            write_bps: 5 * MB,
          },
          gpu: {
            name: "Tesla T4",
            total_mib: 15360,
            used_mib: 0,
            util_percent: 0,
            temp_c: 40,
            power_w: 12,
            power_limit_w: 70,
          },
          history: [
            {
              t: 500,
              vram_pct: 0,
              gpu_util: 0,
              ram_pct: 22,
              cpu_pct: 30,
              disk_pct: 30,
              disk_read_bps: 4 * MB,
              disk_write_bps: 2 * MB,
            },
            {
              t: 501.5,
              vram_pct: 0,
              gpu_util: 0,
              ram_pct: 25,
              cpu_pct: 45,
              disk_pct: 30,
              disk_read_bps: 10 * MB,
              disk_write_bps: 5 * MB,
            },
          ],
        },
      },
      501.5,
    );

    expect(boot.online).toBe(true);
    expect(boot.gpuName).toBe("Tesla T4");
    expect(boot.ramPercent).toBe(25);
    expect(boot.cpuPercent).toBe(45);
    expect(boot.ramSeries.slice(-3)).toEqual([0, 22, 25]);
    expect(boot.sampleTick).toBe(1);

    // Server starts fresh with only 1 newer sample; boot points must remain intact
    const ready = tracker.pushSnapshot(
      {
        id: "ep-boot",
        state: "ready",
        resources: {
          cpu: { cores: 4, percent: 18 },
          memory: {
            total_bytes: 16 * GB,
            used_bytes: 4.8 * GB,
            available_bytes: 11.2 * GB,
            percent: 30,
          },
          disk: {
            total_bytes: 100 * GB,
            used_bytes: 31 * GB,
            free_bytes: 69 * GB,
            percent: 31,
            read_bps: 0,
            write_bps: 0,
          },
          gpu: {
            name: "Tesla T4",
            total_mib: 15360,
            used_mib: 512,
            util_percent: 5,
            temp_c: 41,
            power_w: 15,
            power_limit_w: 70,
          },
          history: [
            {
              t: 504,
              vram_pct: 3.3,
              gpu_util: 5,
              ram_pct: 30,
              cpu_pct: 18,
              disk_pct: 31,
              disk_read_bps: 0,
              disk_write_bps: 0,
            },
          ],
        },
      },
      504,
    );

    expect(ready.online).toBe(true);
    expect(ready.ramSeries.slice(-4)).toEqual([0, 22, 25, 30]);
    expect(ready.cpuSeries.slice(-4)).toEqual([0, 30, 45, 18]);
    expect(ready.sampleTick).toBe(2);
  });

  it("clears previous instance history when starting a new instance with empty starting state", () => {
    const tracker = new ColabOneTelemetryTracker();
    tracker.pushSnapshot(
      {
        id: "ep-old",
        state: "ready",
        gpu: { name: "Tesla T4", total_mib: 16000, used_mib: 8000 },
      },
      10,
    );
    expect(tracker.activeId).toBe("ep-old");
    expect(tracker.history.length).toBe(1);

    // Starting a new instance emits { state: "starting" } before the new id is allocated
    const connecting = tracker.pushSnapshot({ state: "starting" }, 12);
    expect(connecting.online).toBe(false);
    expect(tracker.activeId).toBe("");
    expect(tracker.history).toEqual([]);
    expect(connecting.vramSeries.every((v) => v === 0)).toBe(true);

    // Empty gpu/resources placeholders during starting must not flip online to true
    const emptyStarting = tracker.pushSnapshot(
      { state: "starting", gpu: {} as any },
      13,
    );
    expect(emptyStarting.online).toBe(false);
  });
});
