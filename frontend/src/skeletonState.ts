import type { ColabOneSnapshot } from "./types";

export interface ColabOneBillingSkeletonState {
  /** Initial snapshot has not hydrated yet. */
  initial: boolean;
  /** Render the entire topbar billing capsule as a unified skeleton. */
  capsule: boolean;
  /** Header status tag skeleton. */
  status: boolean;
  /** Header sync-state caption skeleton. */
  caption: boolean;
  /** Remaining CU balance KPI skeleton. */
  balance: boolean;
  /** Burn rate KPI skeleton. */
  rate: boolean;
  /** Estimated endurance KPI skeleton. */
  endurance: boolean;
  /** Active instances KPI skeleton. */
  instances: boolean;
  /** Current/last run consumption footer strip skeleton. */
  run: boolean;
  /** Whether any billing field is currently loading. */
  any: boolean;
}

export interface ColabOneBillingSkeletonInput {
  account: ColabOneSnapshot["account"];
  setupColab: boolean;
  runtimeId?: string;
  loading?: boolean;
  refreshing?: boolean;
  hasError?: boolean;
}

export function colabOneBillingSkeletonState(
  input: ColabOneBillingSkeletonInput,
): ColabOneBillingSkeletonState {
  const initial = Boolean(input.loading);
  const refreshing = Boolean(input.refreshing && input.setupColab);
  const hasError = Boolean(input.hasError);
  const hasFiniteBalance =
    input.account.balance !== undefined &&
    Number.isFinite(input.account.balance);
  const hasFiniteRate =
    input.account.rate !== undefined && Number.isFinite(input.account.rate);
  const hasChecked = Boolean(input.account.checked_at);

  // Saved Google login exists on disk, waiting for the first background account() poll.
  // Exit hydration skeleton if a connection error occurred so users see fallback state + retry.
  const hydratingAccount =
    input.setupColab &&
    !hasError &&
    !hasChecked &&
    !hasFiniteBalance &&
    !hasFiniteRate;

  const pendingBalance =
    input.setupColab &&
    !hasError &&
    !hasFiniteBalance &&
    (!hasChecked || input.account.logged_in);
  const pendingRate =
    input.setupColab &&
    !hasError &&
    !hasFiniteRate &&
    (!hasChecked || input.account.logged_in);
  const pendingEndurance = pendingBalance || pendingRate;
  const pendingInstances =
    input.setupColab &&
    !hasError &&
    !hasChecked &&
    input.account.assignments === undefined &&
    !input.runtimeId;

  const balance = initial || refreshing || pendingBalance;
  const rate = initial || refreshing || pendingRate;
  const endurance = initial || refreshing || pendingEndurance;
  const instances = initial || refreshing || pendingInstances;
  const run = initial || refreshing || hydratingAccount;
  const status = initial;
  const caption = initial || refreshing || hydratingAccount;
  const capsule = initial || refreshing || hydratingAccount;
  const any =
    initial ||
    refreshing ||
    hydratingAccount ||
    balance ||
    rate ||
    endurance ||
    instances ||
    run;

  return {
    initial,
    capsule,
    status,
    caption,
    balance,
    rate,
    endurance,
    instances,
    run,
    any,
  };
}

export interface ColabOneMonitorSkeletonState {
  /** Initial app snapshot has not hydrated yet. */
  initial: boolean;
  /** Connecting to a starting/remote instance or switching monitored instance. */
  connecting: boolean;
  /** Show hardware pills skeleton in the monitor header. */
  headerPills: boolean;
  /** Full card/row skeleton for all 4 hardware panels during initial boot, connecting, or target switching. */
  panels: boolean;
  /** Online runtime awaiting first RAM subsystem telemetry sample. */
  ramPending: boolean;
  /** Online runtime awaiting first CPU subsystem telemetry sample. */
  cpuPending: boolean;
  /** Any monitor field is showing a skeleton state. */
  any: boolean;
}

export interface ColabOneMonitorSkeletonInput {
  loading?: boolean;
  switching?: boolean;
  isConnecting: boolean;
  isPaused: boolean;
  isRemoteError: boolean;
  online: boolean;
  hasRamTelemetry: boolean;
  hasCpuTelemetry: boolean;
}

export function colabOneMonitorSkeletonState(
  input: ColabOneMonitorSkeletonInput,
): ColabOneMonitorSkeletonState {
  const initial = Boolean(input.loading);
  const switching = Boolean(input.switching && !input.isPaused);
  const connecting =
    !input.isPaused &&
    !input.isRemoteError &&
    (input.isConnecting || switching);

  const panels = initial || connecting;
  const headerPills = switching && !input.isRemoteError;
  const ramPending =
    !panels && input.online && !input.isPaused && !input.hasRamTelemetry;
  const cpuPending =
    !panels && input.online && !input.isPaused && !input.hasCpuTelemetry;
  const any = panels || ramPending || cpuPending;

  return {
    initial,
    connecting,
    headerPills,
    panels,
    ramPending,
    cpuPending,
    any,
  };
}
