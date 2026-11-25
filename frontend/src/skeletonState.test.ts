import { describe, expect, it } from "vitest";
import {
  colabOneBillingSkeletonState,
  colabOneMonitorSkeletonState,
} from "./skeletonState";

describe("colabOneBillingSkeletonState", () => {
  it("shows full skeletons during initial snapshot hydration before first IPC reply", () => {
    const sk = colabOneBillingSkeletonState({
      account: { logged_in: false },
      setupColab: false,
      loading: true,
    });
    expect(sk).toEqual({
      initial: true,
      capsule: true,
      status: true,
      caption: true,
      balance: true,
      rate: true,
      endurance: true,
      instances: true,
      run: true,
      any: true,
    });
  });

  it("shows no skeletons when hydrated and signed out", () => {
    const sk = colabOneBillingSkeletonState({
      account: { logged_in: false },
      setupColab: false,
      loading: false,
    });
    expect(sk.any).toBe(false);
    expect(sk.capsule).toBe(false);
    expect(sk.balance).toBe(false);
    expect(sk.rate).toBe(false);
    expect(sk.endurance).toBe(false);
    expect(sk.instances).toBe(false);
    expect(sk.run).toBe(false);
  });

  it("shows hydration skeletons when Google login is saved locally before first account() poll finishes", () => {
    const sk = colabOneBillingSkeletonState({
      account: { logged_in: false },
      setupColab: true,
      loading: false,
    });
    expect(sk.initial).toBe(false);
    expect(sk.capsule).toBe(true);
    expect(sk.caption).toBe(true);
    expect(sk.balance).toBe(true);
    expect(sk.rate).toBe(true);
    expect(sk.endurance).toBe(true);
    expect(sk.instances).toBe(true);
    expect(sk.run).toBe(true);
    expect(sk.status).toBe(false);
  });

  it("shows per-field skeletons only for missing fields once account is partially loaded", () => {
    const sk = colabOneBillingSkeletonState({
      account: {
        logged_in: true,
        balance: 2207.42,
        assignments: 1,
        checked_at: 1_700_000_000,
      },
      setupColab: true,
      runtimeId: "rt-1",
    });
    expect(sk.capsule).toBe(false);
    expect(sk.balance).toBe(false);
    expect(sk.instances).toBe(false);
    expect(sk.rate).toBe(true);
    expect(sk.endurance).toBe(true);
    expect(sk.any).toBe(true);
  });

  it("clears all skeletons when full account billing payload is present and not refreshing", () => {
    const sk = colabOneBillingSkeletonState({
      account: {
        logged_in: true,
        balance: 2207.42,
        rate: 1.96,
        assignments: 1,
        checked_at: 1_700_000_000,
      },
      setupColab: true,
      runtimeId: "rt-1",
      loading: false,
      refreshing: false,
    });
    expect(sk.any).toBe(false);
  });

  it("activates refresh skeletons across metrics when manually refreshing a connected account", () => {
    const sk = colabOneBillingSkeletonState({
      account: {
        logged_in: true,
        balance: 2207.42,
        rate: 1.96,
        assignments: 1,
        checked_at: 1_700_000_000,
      },
      setupColab: true,
      refreshing: true,
    });
    expect(sk.capsule).toBe(true);
    expect(sk.balance).toBe(true);
    expect(sk.rate).toBe(true);
    expect(sk.endurance).toBe(true);
    expect(sk.instances).toBe(true);
    expect(sk.run).toBe(true);
    expect(sk.caption).toBe(true);
    expect(sk.status).toBe(false);
  });

  it("exits account hydration skeletons when a network or auth error is present (prevents infinite skeleton)", () => {
    const failed = colabOneBillingSkeletonState({
      account: { logged_in: false },
      setupColab: true,
      loading: false,
      hasError: true,
    });
    expect(failed.any).toBe(false);
    expect(failed.capsule).toBe(false);

    // Explicit manual refresh still shows skeleton until the command settles
    const retrying = colabOneBillingSkeletonState({
      account: { logged_in: false },
      setupColab: true,
      loading: false,
      hasError: true,
      refreshing: true,
    });
    expect(retrying.capsule).toBe(true);
    expect(retrying.balance).toBe(true);
  });
});

describe("colabOneMonitorSkeletonState", () => {
  it("shows full panel skeletons during cold start, connecting, or instance switching", () => {
    const cold = colabOneMonitorSkeletonState({
      loading: true,
      isConnecting: false,
      isPaused: false,
      isRemoteError: false,
      online: false,
      hasRamTelemetry: false,
      hasCpuTelemetry: false,
    });
    expect(cold.panels).toBe(true);
    expect(cold.headerPills).toBe(false);
    expect(cold.ramPending).toBe(false);
    expect(cold.cpuPending).toBe(false);

    const connecting = colabOneMonitorSkeletonState({
      loading: false,
      isConnecting: true,
      isPaused: false,
      isRemoteError: false,
      online: false,
      hasRamTelemetry: false,
      hasCpuTelemetry: false,
    });
    expect(connecting.panels).toBe(true);
    expect(connecting.connecting).toBe(true);
    expect(connecting.headerPills).toBe(false);

    const switching = colabOneMonitorSkeletonState({
      loading: false,
      switching: true,
      isConnecting: false,
      isPaused: false,
      isRemoteError: false,
      online: true,
      hasRamTelemetry: true,
      hasCpuTelemetry: true,
    });
    expect(switching.panels).toBe(true);
    expect(switching.connecting).toBe(true);
    expect(switching.headerPills).toBe(true);
  });

  it("never shows skeletons in calm standby or paused/error states", () => {
    const standby = colabOneMonitorSkeletonState({
      loading: false,
      isConnecting: false,
      isPaused: false,
      isRemoteError: false,
      online: false,
      hasRamTelemetry: false,
      hasCpuTelemetry: false,
    });
    expect(standby.any).toBe(false);

    const paused = colabOneMonitorSkeletonState({
      loading: false,
      switching: true,
      isConnecting: true,
      isPaused: true,
      isRemoteError: false,
      online: false,
      hasRamTelemetry: false,
      hasCpuTelemetry: false,
    });
    expect(paused.any).toBe(false);

    const errored = colabOneMonitorSkeletonState({
      loading: false,
      isConnecting: true,
      isPaused: false,
      isRemoteError: true,
      online: false,
      hasRamTelemetry: false,
      hasCpuTelemetry: false,
    });
    expect(errored.any).toBe(false);
  });

  it("flags subsystem-level RAM/CPU pending skeletons only when online without subsystem samples", () => {
    const partial = colabOneMonitorSkeletonState({
      loading: false,
      isConnecting: false,
      isPaused: false,
      isRemoteError: false,
      online: true,
      hasRamTelemetry: false,
      hasCpuTelemetry: false,
    });
    expect(partial.panels).toBe(false);
    expect(partial.ramPending).toBe(true);
    expect(partial.cpuPending).toBe(true);
    expect(partial.any).toBe(true);

    const full = colabOneMonitorSkeletonState({
      loading: false,
      isConnecting: false,
      isPaused: false,
      isRemoteError: false,
      online: true,
      hasRamTelemetry: true,
      hasCpuTelemetry: true,
    });
    expect(full.any).toBe(false);
  });
});
