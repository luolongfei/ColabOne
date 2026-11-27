<script setup lang="ts">
import {
  computed,
  onMounted,
  onUnmounted,
  ref,
  shallowRef,
  watch,
} from "vue";
import {
  Badge as ABadge,
  Button as AButton,
  ConfigProvider as AConfigProvider,
} from "ant-design-vue";
import {
  PhArrowsClockwise,
  PhHourglassMedium,
  PhImage,
  PhLinkBreak,
  PhMoon,
  PhSparkle,
  PhSun,
  PhWifiSlash,
} from "@phosphor-icons/vue";
import rawAppIcon from "../../console/resources/icon.svg?raw";
import ColabOneToastHost from "./ColabOneToastHost.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import ComfyPanel from "./ComfyPanel.vue";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import {
  COLABONE_SHARE_AUTH_REQUIRED_EVENT,
  colabOneSnapshotRaw,
  colabOneWorkflowShareCapabilities,
  colabOneWorkflowShareId,
  colabOneWorkflowShareMode,
  colabOneWorkflowShareSessionExpired,
  fetchColabOneWorkflowShareCapabilities,
  isColabOneWorkflowSharePath,
  resetColabOneWorkflowShareSnapshotCache,
  type ColabOneWorkflowShareCapabilities,
} from "./api";
import {
  colabOneEffectiveAppearance,
  setColabOneAppearance,
  type ColabOneAppearance,
} from "./appearance";
import {
  friendlyErrorMessage,
  isTransientIpcError,
  stripColabOneErrorPrefixes,
} from "./billing";
import {
  COLABONE_HIDDEN_POLL_MS,
  COLABONE_WEB_VISIBLE_POLL_MS,
  createColabOneVisibilityPoller,
} from "./colabOnePoller";
import { buildColabOneTheme } from "./colabOneTheme";
import {
  COLABONE_DRAWER_TOAST_TOP_FALLBACK_PX,
  createColabOneToastManager,
  resolveColabOneToastHorizontalBounds,
  type ColabOneToastKind,
} from "./colabOneToast";
import { buildColabOneStudioWorkflowEntries } from "./comfyui";
import { groupColabOneOutputs } from "./colabOneOutputGallery";
import {
  colabOneAntdLocale,
  colabOneLocaleCode,
  t,
} from "./i18n";
import { createColabOneSnapshot, type ColabOneSnapshot } from "./types";

const appIcon = rawAppIcon.replace(
  'viewBox="0 0 1254 1254"',
  'viewBox="120 120 1014 1014"',
);

const colabOneTheme = computed(() =>
  buildColabOneTheme(colabOneEffectiveAppearance.value, colabOneLocaleCode.value),
);

const snapshot = shallowRef<ColabOneSnapshot>(createColabOneSnapshot());
const hasSnapshot = ref(false);
const capabilities = ref<ColabOneWorkflowShareCapabilities | null>(null);
const initialLoading = ref(true);
const checkingSession = ref(false);
const retryingConnection = ref(false);
const sessionExpired = ref(false);
const pollFailures = ref(0);
const connectionLost = computed(() => pollFailures.value >= 2);
const activeShareId = computed(() =>
  (snapshot.value.share?.id || colabOneWorkflowShareId.value || "").trim(),
);
const clockNow = ref(Date.now());
const snapshotAt = ref(Date.now());

const headerRef = ref<HTMLElement | null>(null);
const viewportWidth = ref(
  typeof window !== "undefined" ? window.innerWidth : 1280,
);
const toastTopOffsetPx = ref(72);
const drawerToastTopOffsetPx = ref(COLABONE_DRAWER_TOAST_TOP_FALLBACK_PX);
const comfyDrawerState = ref<{
  open: boolean;
  width: number;
  rootSelector: string;
}>({ open: false, width: 0, rootSelector: "" });

const comfyPanelRef = ref<{
  openOutputsDrawer: () => void;
  closeOutputsDrawer: () => void;
  outputsCount: number;
  hasActiveRun: boolean;
} | null>(null);

const toastManager = createColabOneToastManager({ maxVisible: 2 });

let stopped = false;
let snapshotSeq = 0;
let appliedSnapshotSeq = 0;
let lastSnapshotRaw: string | null = null;
let clockTimer: ReturnType<typeof setInterval> | null = null;
let headerResizeObserver: ResizeObserver | null = null;
let drawerHeaderMeasureFrame: number | null = null;

function normalizeGuestShareSnapshot(
  raw: Record<string, unknown>,
): ColabOneSnapshot {
  const base = createColabOneSnapshot();
  const rawRuntime =
    raw.runtime &&
    typeof raw.runtime === "object" &&
    !Array.isArray(raw.runtime)
      ? (raw.runtime as Record<string, unknown>)
      : {};
  const rawGpu =
    rawRuntime.gpu &&
    typeof rawRuntime.gpu === "object" &&
    !Array.isArray(rawRuntime.gpu)
      ? (rawRuntime.gpu as Record<string, unknown>)
      : null;
  const rawIdle =
    rawRuntime.idle &&
    typeof rawRuntime.idle === "object" &&
    !Array.isArray(rawRuntime.idle)
      ? (rawRuntime.idle as Record<string, unknown>)
      : null;

  const runtime: ColabOneSnapshot["runtime"] = {
    ...base.runtime,
    ...(rawRuntime as Partial<ColabOneSnapshot["runtime"]>),
    state:
      typeof rawRuntime.state === "string" && rawRuntime.state.trim()
        ? rawRuntime.state
        : base.runtime.state,
    ...(rawGpu
      ? {
          gpu: {
            name: typeof rawGpu.name === "string" ? rawGpu.name : "",
            total_mib:
              typeof rawGpu.total_mib === "number" &&
              Number.isFinite(rawGpu.total_mib)
                ? rawGpu.total_mib
                : 0,
            used_mib:
              typeof rawGpu.used_mib === "number" &&
              Number.isFinite(rawGpu.used_mib)
                ? rawGpu.used_mib
                : 0,
            ...(rawGpu.util_percent !== undefined
              ? { util_percent: rawGpu.util_percent as number | null }
              : {}),
            ...(rawGpu.temp_c !== undefined
              ? { temp_c: rawGpu.temp_c as number | null }
              : {}),
            ...(rawGpu.power_w !== undefined
              ? { power_w: rawGpu.power_w as number | null }
              : {}),
            ...(rawGpu.power_limit_w !== undefined
              ? { power_limit_w: rawGpu.power_limit_w as number | null }
              : {}),
          },
        }
      : {}),
    ...(rawIdle
      ? {
          idle: {
            phase:
              typeof rawIdle.phase === "string" ? rawIdle.phase : "active",
            remaining_seconds:
              typeof rawIdle.remaining_seconds === "number" &&
              Number.isFinite(rawIdle.remaining_seconds)
                ? rawIdle.remaining_seconds
                : 0,
            countdown_seconds:
              typeof rawIdle.countdown_seconds === "number" &&
              Number.isFinite(rawIdle.countdown_seconds)
                ? rawIdle.countdown_seconds
                : 0,
            idle_seconds:
              typeof rawIdle.idle_seconds === "number" &&
              Number.isFinite(rawIdle.idle_seconds)
                ? rawIdle.idle_seconds
                : 0,
            server_time:
              typeof rawIdle.server_time === "number" &&
              Number.isFinite(rawIdle.server_time)
                ? rawIdle.server_time
                : 0,
          },
        }
      : {}),
  };

  return {
    ...base,
    ...(raw as Partial<ColabOneSnapshot>),
    logs: Array.isArray(raw.logs)
      ? (raw.logs as ColabOneSnapshot["logs"])
      : base.logs,
    runtime,
    models: Array.isArray(raw.models)
      ? (raw.models as ColabOneSnapshot["models"])
      : base.models,
    workflows: Array.isArray(raw.workflows)
      ? (raw.workflows as ColabOneSnapshot["workflows"])
      : base.workflows,
    tts_engines: Array.isArray(raw.tts_engines)
      ? (raw.tts_engines as ColabOneSnapshot["tts_engines"])
      : base.tts_engines,
    comfy_outputs: Array.isArray(raw.comfy_outputs)
      ? (raw.comfy_outputs as ColabOneSnapshot["comfy_outputs"])
      : base.comfy_outputs,
    account:
      raw.account &&
      typeof raw.account === "object" &&
      !Array.isArray(raw.account)
        ? {
            ...base.account,
            ...(raw.account as Partial<ColabOneSnapshot["account"]>),
          }
        : base.account,
    operation:
      raw.operation &&
      typeof raw.operation === "object" &&
      !Array.isArray(raw.operation)
        ? (raw.operation as ColabOneSnapshot["operation"])
        : null,
    error: typeof raw.error === "string" ? raw.error : "",
    auth_url: typeof raw.auth_url === "string" ? raw.auth_url : "",
    setup:
      raw.setup && typeof raw.setup === "object" && !Array.isArray(raw.setup)
        ? {
            ...base.setup,
            ...(raw.setup as Partial<ColabOneSnapshot["setup"]>),
          }
        : base.setup,
    settings:
      raw.settings &&
      typeof raw.settings === "object" &&
      !Array.isArray(raw.settings)
        ? {
            ...base.settings,
            ...(raw.settings as Partial<ColabOneSnapshot["settings"]>),
          }
        : base.settings,
  };
}

function resetComfyDrawerState() {
  comfyDrawerState.value = { open: false, width: 0, rootSelector: "" };
}

function onComfyDrawerState(next: {
  open: boolean;
  width: number;
  rootSelector: string;
}) {
  if (!next.open || sessionExpired.value) {
    resetComfyDrawerState();
    return;
  }
  comfyDrawerState.value = {
    open: true,
    width: next.width,
    rootSelector: next.rootSelector,
  };
}

const activeToastTopOffsetPx = computed(() =>
  comfyDrawerState.value.open
    ? drawerToastTopOffsetPx.value
    : toastTopOffsetPx.value,
);

const toastHorizontalBounds = computed(() =>
  resolveColabOneToastHorizontalBounds({
    viewportWidth: viewportWidth.value,
    modelDrawerOpen: comfyDrawerState.value.open,
    modelDrawerWidthPx:
      comfyDrawerState.value.width > 0
        ? comfyDrawerState.value.width
        : undefined,
    inlineLogDrawerOpen: false,
    inlineLogDrawerWidthPx: 0,
  }),
);

function updateDrawerToastTopOffset() {
  const { open, rootSelector } = comfyDrawerState.value;
  if (!open || !rootSelector || typeof document === "undefined") return;
  const headerEl = document.querySelector<HTMLElement>(
    `${rootSelector} .ant-drawer-header`,
  );
  if (!headerEl) return;
  const bottom = Math.round(headerEl.getBoundingClientRect().bottom);
  if (bottom > 0) {
    drawerToastTopOffsetPx.value = Math.min(140, Math.max(56, bottom + 10));
  }
}

function scheduleDrawerToastTopMeasure() {
  updateDrawerToastTopOffset();
  if (
    typeof window === "undefined" ||
    typeof window.requestAnimationFrame !== "function"
  ) {
    return;
  }
  if (drawerHeaderMeasureFrame !== null) {
    window.cancelAnimationFrame(drawerHeaderMeasureFrame);
  }
  drawerHeaderMeasureFrame = window.requestAnimationFrame(() => {
    drawerHeaderMeasureFrame = null;
    updateDrawerToastTopOffset();
  });
}

function updateToastLayout() {
  if (typeof window !== "undefined") {
    viewportWidth.value = window.innerWidth;
  }
  const el = headerRef.value;
  if (el) {
    const bottom = Math.round(el.getBoundingClientRect().bottom);
    if (bottom > 0) {
      toastTopOffsetPx.value = Math.min(160, Math.max(56, bottom + 10));
    }
  }
  if (comfyDrawerState.value.open) {
    updateDrawerToastTopOffset();
  }
}

watch(
  () =>
    [
      comfyDrawerState.value.open,
      comfyDrawerState.value.rootSelector,
      comfyDrawerState.value.width,
    ] as const,
  ([isOpen, rootSelector], prev) => {
    if (isOpen) {
      const [prevOpen, prevRootSelector] = prev ?? [false, ""];
      if (!prevOpen || rootSelector !== prevRootSelector) {
        drawerToastTopOffsetPx.value = COLABONE_DRAWER_TOAST_TOP_FALLBACK_PX;
      }
      scheduleDrawerToastTopMeasure();
    } else {
      updateToastLayout();
    }
  },
  { flush: "post" },
);

const displayTitle = computed(() => {
  const snapTitle = (snapshot.value.share?.title || "").trim();
  if (snapTitle) return snapTitle;
  const capTitle = (capabilities.value?.title || "").trim();
  if (capTitle) return capTitle;
  return t("workflowShare.guestDefaultTitle");
});

const gpuReady = computed(() => snapshot.value.runtime.state === "ready");

const activeTask = computed(() =>
  snapshot.value.runtime.state === "stopped"
    ? undefined
    : snapshot.value.runtime.tasks?.find((item) => item.state === "running"),
);

const hasGuestRunningTask = computed(() =>
  Boolean(
    gpuReady.value &&
      (activeTask.value?.kind === "comfyui_run" ||
        activeTask.value?.kind === "comfyui_start" ||
        activeTask.value?.kind === "comfyui_tts_preview" ||
        comfyPanelRef.value?.hasActiveRun),
  ),
);

const isBusyOther = computed(() =>
  Boolean(gpuReady.value && snapshot.value.share?.busy_other),
);

const shareDriveExportReady = computed(() =>
  Boolean(
    gpuReady.value &&
      (snapshot.value.share?.drive_export_ready ??
        snapshot.value.runtime?.drive_export_ready ??
        false),
  ),
);

const outputsCount = computed(
  () => groupColabOneOutputs(snapshot.value.comfy_outputs || []).length,
);

const studioWorkflowCount = computed(
  () => buildColabOneStudioWorkflowEntries(snapshot.value.workflows || []).length,
);

const headerStatus = computed(() => {
  if (connectionLost.value) {
    return {
      kind: "reconnecting" as const,
      label: t("workflowShare.guestStatusReconnecting"),
    };
  }
  if (!gpuReady.value) {
    return {
      kind: "offline" as const,
      label: t("workflowShare.guestStatusOffline"),
    };
  }
  if (hasGuestRunningTask.value) {
    return {
      kind: "running" as const,
      label: t("workflowShare.guestStatusRunning"),
    };
  }
  if (isBusyOther.value) {
    return {
      kind: "busy" as const,
      label: t("workflowShare.guestStatusBusyOther"),
    };
  }
  return {
    kind: "ready" as const,
    label: t("workflowShare.guestStatusReady"),
  };
});

function formatGuestError(raw: unknown): string {
  const text = raw instanceof Error ? raw.message : String(raw ?? "");
  const cleaned = stripColabOneErrorPrefixes(text);
  if (!cleaned) return "";
  if (isTransientIpcError(text) || isTransientIpcError(cleaned)) {
    return t("workflowShare.guestNetworkError");
  }
  return friendlyErrorMessage(cleaned);
}

function toggleAppearance(event?: MouseEvent) {
  const next: ColabOneAppearance =
    colabOneEffectiveAppearance.value === "dark" ? "light" : "dark";
  setColabOneAppearance(next, event);
}

function handleNotify(message: string, kind: ColabOneToastKind = "success") {
  if (!message) return;
  toastManager.push({
    kind,
    message,
    durationMs: kind === "error" ? undefined : 2600,
  });
}

function handleError(raw: string) {
  if (sessionExpired.value || colabOneWorkflowShareSessionExpired.value) {
    return;
  }
  const msg = formatGuestError(raw);
  if (msg) {
    toastManager.push({ kind: "error", message: msg });
  }
}

function handleClearError() {
  // No persistent error banner in share view; transient errors use toasts.
}

function openOutputsDrawer() {
  if (connectionLost.value && !hasSnapshot.value) return;
  comfyPanelRef.value?.openOutputsDrawer();
}

function markSessionExpired() {
  appliedSnapshotSeq = ++snapshotSeq;
  sessionExpired.value = true;
  initialLoading.value = false;
  pollFailures.value = 0;
  snapshot.value = createColabOneSnapshot();
  hasSnapshot.value = false;
  capabilities.value = null;
  colabOneWorkflowShareCapabilities.value = null;
  colabOneWorkflowShareId.value = "";
  lastSnapshotRaw = null;
  resetComfyDrawerState();
  resetColabOneWorkflowShareSnapshotCache();
  snapshotPoller.stop();
}

async function syncSnapshot() {
  if (stopped || sessionExpired.value) return;
  const seq = ++snapshotSeq;
  try {
    const raw = await colabOneSnapshotRaw();
    if (stopped || sessionExpired.value || seq < appliedSnapshotSeq) return;
    const unchanged =
      !initialLoading.value &&
      lastSnapshotRaw !== null &&
      raw === lastSnapshotRaw;
    let next: ColabOneSnapshot | null = null;
    if (!unchanged) {
      const parsed: unknown = JSON.parse(raw);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
        throw new Error(t("workflowShare.errInvalidSnapshotPayload"));
      }
      next = normalizeGuestShareSnapshot(parsed as Record<string, unknown>);
    }
    appliedSnapshotSeq = seq;
    pollFailures.value = 0;
    if (unchanged || !next) {
      initialLoading.value = false;
      return;
    }
    const prevRunning = snapshot.value.runtime.tasks?.find(
      (item) => item.state === "running",
    );
    const nextRunning = next.runtime.tasks?.find(
      (item) => item.state === "running",
    );
    if (
      next.runtime.idle?.server_time !==
        snapshot.value.runtime.idle?.server_time ||
      nextRunning?.id !== prevRunning?.id ||
      nextRunning?.elapsed_seconds !== prevRunning?.elapsed_seconds ||
      nextRunning?.download?.elapsed_seconds !==
        prevRunning?.download?.elapsed_seconds
    ) {
      snapshotAt.value = Date.now();
      clockNow.value = Date.now();
    }
    const prevShareId = (colabOneWorkflowShareId.value || "").trim();
    const nextShareId = (next.share?.id || "").trim();
    if (nextShareId) {
      colabOneWorkflowShareId.value = nextShareId;
    }
    lastSnapshotRaw = raw;
    snapshot.value = next;
    hasSnapshot.value = true;
    initialLoading.value = false;
    if (prevShareId && nextShareId && nextShareId !== prevShareId) {
      resetComfyDrawerState();
      void fetchColabOneWorkflowShareCapabilities()
        .then((caps) => {
          if (!stopped && !sessionExpired.value) {
            capabilities.value = caps;
          }
        })
        .catch(() => {
          if (!stopped && colabOneWorkflowShareSessionExpired.value) {
            markSessionExpired();
          }
        });
    }
  } catch {
    if (stopped) return;
    if (colabOneWorkflowShareSessionExpired.value) {
      markSessionExpired();
    } else if (!sessionExpired.value) {
      pollFailures.value += 1;
      if (pollFailures.value >= 2) {
        initialLoading.value = false;
      }
    }
  }
}

async function syncNow() {
  await syncSnapshot();
}

const snapshotPoller = createColabOneVisibilityPoller({
  visibleMs: COLABONE_WEB_VISIBLE_POLL_MS,
  hiddenMs: COLABONE_HIDDEN_POLL_MS,
  tick: syncSnapshot,
});

async function bootstrapShareSession(options?: { fromRecheck?: boolean }) {
  if (stopped) return;
  checkingSession.value = true;
  try {
    const caps = await fetchColabOneWorkflowShareCapabilities();
    if (stopped) return;
    capabilities.value = caps;
    if (caps.share_id) {
      colabOneWorkflowShareId.value = caps.share_id;
    }
    if (sessionExpired.value || !hasSnapshot.value) {
      initialLoading.value = true;
    }
    sessionExpired.value = false;
    pollFailures.value = 0;
    snapshotPoller.start();
  } catch (err) {
    if (stopped) return;
    if (colabOneWorkflowShareSessionExpired.value) {
      markSessionExpired();
      if (options?.fromRecheck) {
        toastManager.push({
          kind: "warning",
          dedupeKey: "workflow-share-still-expired",
          message: t("workflowShare.guestStillExpiredToast"),
        });
      }
    } else if (sessionExpired.value) {
      const msg = formatGuestError(err);
      if (msg) {
        toastManager.push({ kind: "error", message: msg });
      }
    } else {
      pollFailures.value += 1;
      if (pollFailures.value >= 2) {
        initialLoading.value = false;
      }
      // Transient network failure on initial load: start snapshot polling so it recovers automatically.
      snapshotPoller.start();
    }
  } finally {
    checkingSession.value = false;
  }
}

async function retryConnection() {
  if (retryingConnection.value || checkingSession.value || stopped) return;
  retryingConnection.value = true;
  try {
    if (!capabilities.value) {
      await bootstrapShareSession();
    }
    if (!stopped && !sessionExpired.value) {
      await syncSnapshot();
    }
  } finally {
    retryingConnection.value = false;
  }
}

async function recheckSessionStatus() {
  if (checkingSession.value) return;
  appliedSnapshotSeq = ++snapshotSeq;
  pollFailures.value = 0;
  snapshot.value = createColabOneSnapshot();
  hasSnapshot.value = false;
  capabilities.value = null;
  colabOneWorkflowShareCapabilities.value = null;
  colabOneWorkflowShareId.value = "";
  lastSnapshotRaw = null;
  resetComfyDrawerState();
  colabOneWorkflowShareSessionExpired.value = false;
  resetColabOneWorkflowShareSnapshotCache();
  await bootstrapShareSession({ fromRecheck: true });
}

function handleShareAuthRequired() {
  if (stopped) return;
  markSessionExpired();
}

watch(
  [displayTitle, colabOneLocaleCode],
  () => {
    if (typeof document !== "undefined") {
      document.title = `${displayTitle.value} · ColabOne`;
    }
  },
  { immediate: true },
);

colabOneWorkflowShareMode.value = true;

onMounted(() => {
  colabOneWorkflowShareMode.value = true;
  window.addEventListener(
    COLABONE_SHARE_AUTH_REQUIRED_EVENT,
    handleShareAuthRequired,
  );
  window.addEventListener("resize", updateToastLayout, { passive: true });
  updateToastLayout();
  if (headerRef.value && typeof ResizeObserver !== "undefined") {
    headerResizeObserver = new ResizeObserver(() => {
      updateToastLayout();
    });
    headerResizeObserver.observe(headerRef.value);
  }
  clockTimer = setInterval(() => {
    if (typeof document === "undefined" || !document.hidden) {
      clockNow.value = Date.now();
    }
  }, 1000);
  void bootstrapShareSession();
});

onUnmounted(() => {
  stopped = true;
  snapshotPoller.stop();
  if (clockTimer !== null) {
    clearInterval(clockTimer);
    clockTimer = null;
  }
  toastManager.clear();
  window.removeEventListener(
    COLABONE_SHARE_AUTH_REQUIRED_EVENT,
    handleShareAuthRequired,
  );
  window.removeEventListener("resize", updateToastLayout);
  if (drawerHeaderMeasureFrame !== null && typeof window !== "undefined") {
    window.cancelAnimationFrame(drawerHeaderMeasureFrame);
    drawerHeaderMeasureFrame = null;
  }
  headerResizeObserver?.disconnect();
  headerResizeObserver = null;
  colabOneWorkflowShareId.value = "";
  colabOneWorkflowShareCapabilities.value = null;
  if (!isColabOneWorkflowSharePath()) {
    colabOneWorkflowShareMode.value = false;
  }
});
</script>

<template>
  <AConfigProvider :theme="colabOneTheme" :locale="colabOneAntdLocale">
    <div class="co-share-shell">
      <div class="co-share-ambient" aria-hidden="true" />

      <!-- Minimal Guest Header (no sidebar, no logout, no account/settings/device links) -->
      <header ref="headerRef" class="co-share-header">
        <div class="co-share-header-inner">
          <div class="co-share-brand-group">
            <span
              class="co-share-brand-icon"
              aria-hidden="true"
              v-html="appIcon"
            />
            <div class="co-share-title-stack">
              <div class="co-share-title-row">
                <h1 class="co-share-title" data-testid="workflow-share-title">
                  {{ displayTitle }}
                </h1>
                <span
                  v-if="!sessionExpired && (!initialLoading || connectionLost)"
                  class="co-share-status-pill"
                  :class="`is-${headerStatus.kind}`"
                  data-testid="workflow-share-status-pill"
                >
                  <span class="co-share-status-dot" aria-hidden="true" />
                  <span>{{ headerStatus.label }}</span>
                </span>
              </div>
            </div>
          </div>

          <div class="co-share-header-actions">
            <AButton
              v-if="!sessionExpired"
              class="co-share-outputs-btn"
              :class="{
                'is-running': hasGuestRunningTask,
                'is-compact-icon': !hasGuestRunningTask && outputsCount === 0,
              }"
              :disabled="connectionLost && !hasSnapshot"
              :aria-label="t('workflowShare.guestOutputsBtn')"
              data-testid="workflow-share-outputs-btn"
              @click="openOutputsDrawer"
            >
              <template #icon><PhImage :size="15" /></template>
              <span class="co-share-outputs-label">
                {{ t("workflowShare.guestOutputsBtn") }}
              </span>
              <ABadge
                v-if="hasGuestRunningTask"
                status="processing"
                class="co-share-outputs-pulse"
              />
              <span
                v-if="outputsCount > 0"
                class="co-share-outputs-count co-share-mono"
              >
                {{ outputsCount }}
              </span>
            </AButton>

            <ColabOneTooltip
              :title="
                colabOneEffectiveAppearance === 'dark'
                  ? t('nav.switchLight')
                  : t('nav.switchDark')
              "
              placement="bottom"
            >
              <button
                type="button"
                class="co-share-icon-btn"
                :aria-label="
                  colabOneEffectiveAppearance === 'dark'
                    ? t('nav.switchLight')
                    : t('nav.switchDark')
                "
                data-testid="workflow-share-theme-toggle"
                @click="toggleAppearance($event)"
              >
                <PhSun
                  v-if="colabOneEffectiveAppearance === 'dark'"
                  :size="17"
                />
                <PhMoon v-else :size="17" />
              </button>
            </ColabOneTooltip>

            <LanguageSwitcher mode="topbar" />
          </div>
        </div>
      </header>

      <!-- Main Guest Surface -->
      <main class="co-share-main">
        <!-- Session Expired / Revoked Card -->
        <section
          v-if="sessionExpired"
          class="co-share-expired-wrap"
          role="alert"
          data-testid="workflow-share-expired-card"
        >
          <div class="co-share-expired-card">
            <div class="co-share-expired-emblem" aria-hidden="true">
              <PhLinkBreak :size="30" weight="duotone" />
            </div>
            <h2 class="co-share-expired-title">
              {{ t("workflowShare.guestExpiredTitle") }}
            </h2>
            <p class="co-share-expired-desc">
              {{ t("workflowShare.guestExpiredDesc") }}
            </p>
            <AButton
              type="primary"
              :loading="checkingSession"
              data-testid="workflow-share-recheck-btn"
              @click="recheckSessionStatus"
            >
              <template #icon><PhArrowsClockwise :size="15" /></template>
              {{ t("workflowShare.guestRefreshBtn") }}
            </AButton>
          </div>
        </section>

        <!-- Active Guest Workflow Studio -->
        <template v-else>
          <section class="co-share-hero">
            <p class="co-share-hero-subtitle">
              {{ t("workflowShare.guestSubtitle") }}
            </p>
            <span
              v-if="!initialLoading && studioWorkflowCount > 0"
              class="co-share-workflow-count-pill"
              data-testid="workflow-share-workflow-count"
            >
              <PhSparkle :size="13" weight="duotone" aria-hidden="true" />
              <span>{{
                t("workflowShare.guestWorkflowCount", {
                  count: studioWorkflowCount,
                })
              }}</span>
            </span>
          </section>

          <!-- Contextual Status Banner -->
          <div
            v-if="connectionLost"
            class="co-share-banner is-reconnecting"
            role="status"
            data-testid="workflow-share-disconnected-banner"
          >
            <div class="co-share-banner-icon" aria-hidden="true">
              <PhWifiSlash :size="19" weight="duotone" />
            </div>
            <div class="co-share-banner-copy">
              <strong>{{ t("workflowShare.guestDisconnectedTitle") }}</strong>
              <p>{{ t("workflowShare.guestDisconnectedDesc") }}</p>
            </div>
            <AButton
              size="small"
              class="co-share-banner-action"
              :loading="retryingConnection || checkingSession"
              data-testid="workflow-share-retry-btn"
              @click="retryConnection"
            >
              {{ t("common.retry") }}
            </AButton>
          </div>
          <div
            v-else-if="!initialLoading && !gpuReady"
            class="co-share-banner is-offline"
            role="status"
            data-testid="workflow-share-offline-banner"
          >
            <div class="co-share-banner-icon" aria-hidden="true">
              <PhWifiSlash :size="19" weight="duotone" />
            </div>
            <div class="co-share-banner-copy">
              <strong>{{ t("workflowShare.guestOfflineBannerTitle") }}</strong>
              <p>{{ t("workflowShare.guestOfflineBannerDesc") }}</p>
            </div>
          </div>
          <div
            v-else-if="!initialLoading && isBusyOther && !hasGuestRunningTask"
            class="co-share-banner is-busy"
            role="status"
            data-testid="workflow-share-busy-banner"
          >
            <div class="co-share-banner-icon" aria-hidden="true">
              <PhHourglassMedium :size="19" weight="duotone" />
            </div>
            <div class="co-share-banner-copy">
              <strong>{{ t("workflowShare.guestBusyOtherBannerTitle") }}</strong>
              <p>{{ t("workflowShare.guestBusyOtherBannerDesc") }}</p>
            </div>
          </div>

          <!-- Shared ComfyPanel (Workflow Cards + Parameter Drawer + Outputs Drawer) -->
          <ComfyPanel
            v-if="!(connectionLost && !hasSnapshot)"
            :key="activeShareId || 'pending'"
            ref="comfyPanelRef"
            :state="snapshot"
            :loading="initialLoading"
            :share-mode="true"
            :share-busy-other="isBusyOther"
            :share-drive-export-ready="shareDriveExportReady"
            :clock-now="clockNow"
            :snapshot-at="snapshotAt"
            @sync="syncNow"
            @notify="handleNotify"
            @error="handleError"
            @clear-error="handleClearError"
            @drawer-state="onComfyDrawerState"
          />
        </template>
      </main>

      <!-- Minimal Footer -->
      <footer class="co-share-footer">
        <span>{{ t("workflowShare.guestFooter") }}</span>
      </footer>

      <ColabOneToastHost
        :items="toastManager.items.value"
        placement="top"
        :top-offset-px="activeToastTopOffsetPx"
        :left-offset-px="toastHorizontalBounds.leftOffsetPx"
        :right-offset-px="toastHorizontalBounds.rightOffsetPx"
        @pause="toastManager.pause"
        @resume="toastManager.resume"
        @dismiss="toastManager.dismiss"
      />
    </div>
  </AConfigProvider>
</template>

<style scoped>
.co-share-shell {
  position: relative;
  width: 100%;
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  display: flex;
  flex-direction: column;
  background: var(--co-canvas);
  color: var(--co-text);
  isolation: isolate;
}

.co-share-ambient {
  position: fixed;
  inset: 0 0 auto 0;
  height: 360px;
  pointer-events: none;
  z-index: -1;
  background:
    radial-gradient(
      760px 280px at 18% -12%,
      rgba(59, 130, 246, 0.11),
      transparent 70%
    ),
    radial-gradient(
      620px 260px at 84% -8%,
      rgba(99, 102, 241, 0.08),
      transparent 72%
    );
}

:root[data-theme="dark"] .co-share-ambient {
  background:
    radial-gradient(
      760px 280px at 18% -12%,
      rgba(59, 130, 246, 0.16),
      transparent 70%
    ),
    radial-gradient(
      620px 260px at 84% -8%,
      rgba(129, 140, 248, 0.12),
      transparent 72%
    );
}

.co-share-mono {
  font-family: var(--co-font-mono);
  font-variant-numeric: tabular-nums;
}

/* Sticky Frosted Header */
.co-share-header {
  position: sticky;
  top: 0;
  z-index: 20;
  padding-top: var(--co-safe-top, 0px);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  background: color-mix(in srgb, var(--co-canvas) 82%, transparent);
  border-bottom: 1px solid var(--co-border-secondary);
}

.co-share-header-inner {
  width: 100%;
  max-width: 1120px;
  margin: 0 auto;
  padding: 12px clamp(16px, 3vw, 32px);
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
}

.co-share-brand-group {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  flex: 1 1 auto;
}

.co-share-brand-icon {
  width: 34px;
  height: 34px;
  border-radius: 10px;
  overflow: hidden;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  box-shadow:
    0 1px 3px rgba(15, 23, 42, 0.12),
    0 0 0 1px rgba(15, 23, 42, 0.06);
}

:root[data-theme="dark"] .co-share-brand-icon {
  box-shadow:
    0 2px 6px rgba(0, 0, 0, 0.4),
    0 0 0 1px rgba(255, 255, 255, 0.08);
}

.co-share-brand-icon :deep(svg) {
  width: 100%;
  height: 100%;
  display: block;
}

.co-share-title-stack {
  min-width: 0;
  flex: 1 1 auto;
}

.co-share-title-row {
  display: flex;
  align-items: center;
  flex-wrap: nowrap;
  gap: 8px 10px;
  min-width: 0;
}

.co-share-title {
  flex: 0 1 auto;
  min-width: 0;
  margin: 0;
  font-size: 17px;
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1.3;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 100%;
}

/* Status Pill */
.co-share-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 24px;
  padding: 0 10px;
  border-radius: 9999px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  color: var(--co-secondary-text);
  white-space: nowrap;
  flex-shrink: 0;
}

.co-share-status-dot {
  width: 7px;
  height: 7px;
  border-radius: 9999px;
  background: currentColor;
  flex-shrink: 0;
}

.co-share-status-pill.is-ready {
  color: var(--co-success-text);
  background: var(--co-success-bg);
  border-color: var(--co-success-border);
}

.co-share-status-pill.is-running {
  color: var(--co-accent-strong);
  background: var(--co-selected);
  border-color: rgba(59, 130, 246, 0.34);
}

.co-share-status-pill.is-running .co-share-status-dot {
  animation: co-share-pulse 1.5s ease-in-out infinite;
}

.co-share-status-pill.is-busy {
  color: var(--co-warning-text);
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
}

.co-share-status-pill.is-reconnecting {
  color: var(--co-warning-text);
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
}

.co-share-status-pill.is-reconnecting .co-share-status-dot {
  animation: co-share-pulse 1.5s ease-in-out infinite;
}

.co-share-status-pill.is-offline {
  color: var(--co-muted);
  background: var(--co-track);
  border-color: var(--co-border);
}

@keyframes co-share-pulse {
  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.45;
    transform: scale(0.8);
  }
}

/* Header Right Controls */
.co-share-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.co-share-header-actions :deep(.co-locale-trigger) {
  height: 38px;
}

.co-share-outputs-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 38px;
  padding: 0 14px;
  border-radius: 9999px;
  font-weight: 500;
}

.co-share-outputs-btn.is-running {
  border-color: rgba(59, 130, 246, 0.45);
}

.co-share-outputs-pulse {
  display: inline-flex;
  align-items: center;
}

.co-share-outputs-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 19px;
  height: 19px;
  padding: 0 6px;
  border-radius: 9999px;
  font-size: 11.5px;
  font-weight: 600;
  line-height: 1;
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

:root[data-theme="dark"] .co-share-outputs-count {
  background: rgba(59, 130, 246, 0.24);
  color: var(--co-accent-strong);
}

.co-share-icon-btn {
  width: 38px;
  height: 38px;
  border-radius: 9999px;
  border: 1px solid transparent;
  background: var(--co-track);
  color: var(--co-secondary-text);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    color 0.16s ease,
    transform 0.12s ease;
}

:root[data-theme="dark"] .co-share-icon-btn {
  border-color: var(--co-border-secondary);
}

.co-share-icon-btn:hover {
  background: var(--co-hover);
  color: var(--co-text);
}

.co-share-icon-btn:active {
  transform: scale(0.95);
}

.co-share-icon-btn:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

/* Main Content Area */
.co-share-main {
  width: 100%;
  max-width: 1120px;
  margin: 0 auto;
  padding: 24px clamp(16px, 3vw, 32px) 40px;
  box-sizing: border-box;
  flex: 1 0 auto;
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.co-share-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px 16px;
}

.co-share-hero-subtitle {
  margin: 0;
  font-size: 14px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

.co-share-workflow-count-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 11px;
  border-radius: 9999px;
  font-size: 12px;
  font-weight: 500;
  color: var(--co-accent-strong);
  background: var(--co-selected);
  border: 1px solid rgba(59, 130, 246, 0.22);
  white-space: nowrap;
}

/* Contextual Status Banner */
.co-share-banner {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border-radius: 14px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
}

.co-share-banner.is-offline {
  border-color: var(--co-border);
  background: var(--co-subtle);
}

.co-share-banner.is-busy,
.co-share-banner.is-reconnecting {
  border-color: var(--co-warning-border);
  background: var(--co-warning-bg);
}

.co-share-banner-icon {
  width: 34px;
  height: 34px;
  border-radius: 10px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  background: var(--co-track);
  color: var(--co-secondary-text);
}

.co-share-banner.is-busy .co-share-banner-icon,
.co-share-banner.is-reconnecting .co-share-banner-icon {
  background: rgba(245, 158, 11, 0.16);
  color: var(--co-warning-text);
}

.co-share-banner-copy {
  min-width: 0;
  flex: 1;
}

.co-share-banner-copy strong {
  display: block;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--co-text);
}

.co-share-banner.is-busy .co-share-banner-copy strong,
.co-share-banner.is-reconnecting .co-share-banner-copy strong {
  color: var(--co-warning-text);
}

.co-share-banner-copy p {
  margin: 3px 0 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--co-secondary-text);
}

.co-share-banner-action {
  flex-shrink: 0;
  align-self: center;
}

/* Expired / Revoked Centered Card */
.co-share-expired-wrap {
  flex: 1 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 36px 0 48px;
}

.co-share-expired-card {
  width: 100%;
  max-width: 440px;
  padding: 32px 28px;
  border-radius: 18px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.co-share-expired-emblem {
  width: 56px;
  height: 56px;
  border-radius: 16px;
  display: grid;
  place-items: center;
  background: var(--co-warning-bg);
  border: 1px solid var(--co-warning-border);
  color: var(--co-warning-text);
  margin-bottom: 4px;
}

.co-share-expired-title {
  margin: 0;
  font-size: 18px;
  font-weight: 700;
  color: var(--co-text);
}

.co-share-expired-desc {
  margin: 0 0 8px;
  font-size: 13.5px;
  line-height: 1.65;
  color: var(--co-secondary-text);
}

/* Minimal Footer */
.co-share-footer {
  width: 100%;
  max-width: 1120px;
  margin: 0 auto;
  padding: 14px clamp(16px, 3vw, 32px) calc(24px + var(--co-safe-bottom, 0px));
  box-sizing: border-box;
  text-align: center;
  font-size: 12px;
  color: var(--co-muted);
}

@media (max-width: 680px) {
  .co-share-header-inner {
    padding: 10px 14px;
    gap: 8px;
  }

  .co-share-brand-icon {
    width: 30px;
    height: 30px;
    border-radius: 9px;
  }

  .co-share-title-row {
    flex-wrap: wrap;
    gap: 4px 8px;
  }

  .co-share-title {
    font-size: 15px;
  }

  .co-share-outputs-btn {
    height: 34px;
    padding: 0 11px;
    font-size: 13px;
  }

  .co-share-icon-btn {
    width: 34px;
    height: 34px;
  }

  .co-share-header-actions :deep(.co-locale-trigger) {
    height: 34px;
  }

  .co-share-main {
    padding: 16px 14px 32px;
    gap: 14px;
  }
}

@media (max-width: 560px) {
  .co-share-banner {
    flex-wrap: wrap;
  }

  .co-share-banner-copy {
    flex: 1 1 calc(100% - 46px);
  }

  .co-share-banner-action {
    margin-left: 46px;
  }
}

@media (max-width: 520px) {
  .co-share-outputs-label {
    display: none;
  }

  .co-share-outputs-btn {
    padding-inline: 10px !important;
  }

  .co-share-outputs-btn.is-compact-icon {
    width: 34px;
    min-width: 34px;
    padding-inline: 0 !important;
    justify-content: center;
    gap: 0;
  }
}

@media (max-width: 440px) {
  .co-share-status-pill {
    height: 22px;
    padding: 0 8px;
    font-size: 11px;
  }
}

@media (max-width: 420px) {
  .co-share-outputs-btn {
    height: 32px;
  }

  .co-share-outputs-btn.is-compact-icon {
    width: 32px;
    min-width: 32px;
  }

  .co-share-icon-btn {
    width: 32px;
    height: 32px;
  }

  .co-share-header-actions :deep(.co-locale-trigger) {
    height: 32px;
  }
}
</style>
