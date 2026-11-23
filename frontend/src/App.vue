<script setup lang="ts">
import {
  computed,
  onMounted,
  onUnmounted,
  ref,
  watch,
  type ComponentPublicInstance,
} from "vue";
import {
  Alert as AAlert,
  Badge as ABadge,
  Button as AButton,
  Card as ACard,
  Checkbox as ACheckbox,
  Collapse as ACollapse,
  CollapsePanel as ACollapsePanel,
  ConfigProvider as AConfigProvider,
  Divider as ADivider,
  Empty as AEmpty,
  Layout as ALayout,
  LayoutContent as ALayoutContent,
  Modal as AModal,
  Progress as AProgress,
  Segmented as ASegmented,
  Select as ASelect,
  SelectOption as ASelectOption,
  Space as ASpace,
  Spin as ASpin,
  Switch as ASwitch,
  Tag as ATag,
  Textarea as ATextarea,
  TypographyText as ATypographyText,
  TypographyTitle as ATypographyTitle,
} from "ant-design-vue";
import ColabOneSecretInput from "./ColabOneSecretInput.vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneToastHost from "./ColabOneToastHost.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { colabOneExtractHfToken } from "./colabOneSecretToken";
import {
  PhArrowSquareOut,
  PhArrowsClockwise,
  PhCaretRight,
  PhCheck,
  PhCloud,
  PhCopy,
  PhDesktop,
  PhFlowArrow,
  PhGearSix,
  PhGlobe,
  PhHardDrives,
  PhKey,
  PhMoon,
  PhPlay,
  PhPlus,
  PhShieldCheck,
  PhSlidersHorizontal,
  PhStop,
  PhSun,
  PhTerminalWindow,
  PhUserCircle,
} from "@phosphor-icons/vue";
import rawAppIcon from "../../console/resources/icon.svg?raw";
import { COLABONE_APP_VERSION_LABEL } from "./colabOneVersion";
import { buildColabOneTheme } from "./colabOneTheme";
import { COLABONE_STORAGE_KEYS } from "./storage";
import {
  colabOneAppearance,
  COLABONE_APPEARANCE_OPTIONS,
  colabOneEffectiveAppearance,
  setColabOneAppearance,
  type ColabOneAppearance,
} from "./appearance";
import {
  colabOneAntdLocale,
  colabOneLocaleCode,
  hydrateColabOneLocaleFromSettings,
  t,
  tb,
  type ColabOneLocaleMeta,
} from "./i18n";
import ApiPanel from "./ApiPanel.vue";
import ComfyPanel from "./ComfyPanel.vue";
import BillingBar from "./BillingBar.vue";
import BillingCard from "./BillingCard.vue";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import LogDrawer from "./LogDrawer.vue";
import ResourceMonitor from "./ResourceMonitor.vue";
import FlagIcon from "./FlagIcon.vue";
import { locationFull, locationShort, locationTooltip } from "./location";
import { resolveColabOneMonitorFallback } from "./telemetry";
import {
  COLABONE_IDLE_RELEASE_OPTIONS,
  describeColabOneBilling,
  formatCountdownClock,
  formatIdlePolicyLabel,
  friendlyErrorMessage,
  isTransientIpcError,
  stripColabOneErrorPrefixes,
} from "./billing";
import {
  colabOneCommand,
  colabOneGetLogDrawerState,
  colabOneOpenURL,
  colabOneReadClipboard,
  colabOneSetLogDrawer,
  colabOneSnapshot,
  colabOneWriteClipboard,
} from "./api";
import {
  createColabOneSnapshot,
  COLABONE_STATUS_LABELS,
  evaluateColabOneRuntimeUpgrade,
  type AccountInstance,
} from "./types";
import { createColabOneScrollController } from "./scrollTransition";
import {
  COLABONE_DRAWER_TOAST_TOP_FALLBACK_PX,
  createColabOneToastManager,
  isPersistentColabOneError,
  resolveColabOneToastHorizontalBounds,
  type ColabOneToastKind,
} from "./colabOneToast";

// Trim the outer desktop-icon gutter so the white rounded tile fills inline UI icon slots edge-to-edge.
const appIcon = rawAppIcon.replace(
  'viewBox="0 0 1254 1254"',
  'viewBox="120 120 1014 1014"',
);

const appVersionLabel = COLABONE_APP_VERSION_LABEL;

const colabOneTheme = computed(() =>
  buildColabOneTheme(colabOneEffectiveAppearance.value, colabOneLocaleCode.value),
);

const state = ref(createColabOneSnapshot()),
  snapshotLoaded = ref(false),
  refreshingAccount = ref(false),
  page = ref("device"),
  ipcError = ref(""),
  ipcDisconnected = ref(false),
  busyCount = ref(0),
  busy = computed(() => busyCount.value > 0),
  savingIdle = ref(false);
let ipcFailures = 0;

const toastManager = createColabOneToastManager({ maxVisible: 3 });
const viewportWidth = ref(
  typeof window !== "undefined" ? window.innerWidth : 1280,
);
const toastTopOffsetPx = ref(112);
const drawerToastTopOffsetPx = ref(COLABONE_DRAWER_TOAST_TOP_FALLBACK_PX);
let toastLayoutObserver: ResizeObserver | null = null;
let drawerHeaderMeasureFrame: number | null = null;
let handledStateError = "";
let snapshotSeq = 0;
let appliedSnapshotSeq = 0;
let clearingStateError: {
  text: string;
  startedAtSeq: number;
  completedAtSeq: number | null;
} | null = null;
const shownFailedTaskIds = new Set<string>();

function normalizeErrorKey(raw: unknown): string {
  return stripColabOneErrorPrefixes(raw);
}

function formatError(
  raw: unknown,
  source: "ipc" | "action" | "state" = "action",
): string {
  const cleaned = normalizeErrorKey(raw);
  if (!cleaned) return "";
  if (isTransientIpcError(cleaned)) {
    const isLocalTransport =
      source === "ipc" ||
      ipcDisconnected.value ||
      /failed to fetch|networkerror|ipc.*closed/i.test(cleaned);
    return isLocalTransport
      ? friendlyErrorMessage(cleaned)
      : t("status.remoteControlBriefDisconnect");
  }
  if (/mygo:\s*runtime not found/i.test(cleaned)) {
    return t("status.desktopInitializing");
  }
  if (/timed?\s*out/i.test(cleaned)) {
    return t("status.requestTimeout");
  }
  return friendlyErrorMessage(cleaned);
}

const persistentBannerError = computed(() => {
  if (ipcDisconnected.value && ipcError.value) {
    return formatError(ipcError.value, "ipc");
  }
  if (state.value.error && isPersistentColabOneError(state.value.error)) {
    return formatError(state.value.error, "state");
  }
  if (ipcError.value) {
    return formatError(ipcError.value, "ipc");
  }
  return "";
});

const hasSyncError = computed(() =>
  Boolean(persistentBannerError.value || state.value.error || ipcError.value),
);

function clearActionErrors() {
  toastManager.dismissGroup("action-error");
}

function clearBackendStateError(text: string) {
  const entry = {
    text,
    startedAtSeq: snapshotSeq,
    completedAtSeq: null as number | null,
  };
  clearingStateError = entry;
  if (String(state.value.error ?? "").trim() === text) {
    state.value.error = "";
  }
  colabOneCommand("error.clear", { expected: text })
    .then(() => {
      if (clearingStateError === entry) {
        entry.completedAtSeq = snapshotSeq;
      }
    })
    .catch(() => {
      if (clearingStateError === entry) {
        clearingStateError = null;
      }
    });
}

async function openExternalURL(url: string) {
  if (!url) return;
  try {
    await colabOneOpenURL(url);
  } catch (e) {
    showError(e);
  }
}

async function onPanelSync() {
  clearActionErrors();
  await syncSnapshot().catch(() => {});
}

function showError(raw: unknown, source: "action" | "state" = "action") {
  const cleaned = normalizeErrorKey(raw);
  if (!cleaned) return;
  const isTransient = isTransientIpcError(cleaned);
  toastManager.push({
    kind: "error",
    group: isTransient ? "ipc-transient" : "action-error",
    dedupeKey: `error:${source}:${cleaned}`,
    resolveText: () => formatError(cleaned, source),
  });
}

function dismissPersistentError() {
  ipcError.value = "";
  if (state.value.error && isPersistentColabOneError(state.value.error)) {
    clearBackendStateError(String(state.value.error).trim());
  }
}

const dismissedUpgradeKey = ref("");
const showUpgradeConfirm = ref(false);
const upgradeBanner = computed(() => evaluateColabOneRuntimeUpgrade(state.value));
const showUpgradeBanner = computed(
  () =>
    upgradeBanner.value.visible &&
    (!upgradeBanner.value.dismissible ||
      dismissedUpgradeKey.value !== upgradeBanner.value.dismissKey),
);

function dismissOptionalUpgrade() {
  if (upgradeBanner.value.dismissible && upgradeBanner.value.dismissKey) {
    dismissedUpgradeKey.value = upgradeBanner.value.dismissKey;
  }
}

async function requestRuntimeUpgrade() {
  if (!upgradeBanner.value.canUpgrade || busy.value) return;
  if (upgradeBanner.value.needsConfirm) {
    showUpgradeConfirm.value = true;
    return;
  }
  await act("runtime.upgrade");
}

async function confirmRuntimeUpgrade() {
  if (busy.value || state.value.operation) return;
  showUpgradeConfirm.value = false;
  await act("runtime.upgrade", { force: true, restore: true });
}

watch(
  () => upgradeBanner.value.visible,
  (visible) => {
    if (!visible) {
      showUpgradeConfirm.value = false;
    }
  },
);

watch(
  () => state.value.error,
  (raw) => {
    const text = String(raw ?? "").trim();
    if (!text) {
      if (!clearingStateError) {
        handledStateError = "";
      }
      toastManager.dismissGroup("state-error");
      return;
    }
    if (isPersistentColabOneError(text)) {
      return;
    }
    if (text === handledStateError) {
      return;
    }
    handledStateError = text;
    toastManager.push({
      kind: "error",
      group: "state-error",
      dedupeKey: `state-error:${normalizeErrorKey(text)}`,
      resolveText: () => formatError(text, "state"),
      onDismiss: () => {
        if (String(state.value.error ?? "").trim() === text) {
          clearBackendStateError(text);
        }
      },
    });
  },
  { immediate: true },
);

watch(
  () => state.value.runtime.tasks,
  (tasks) => {
    if (!tasks?.length) return;
    const unseenFailed = tasks.filter(
      (item) =>
        item.state === "failed" &&
        !shownFailedTaskIds.has(item.id) &&
        String(item.error ?? "").trim().length > 0,
    );
    if (!unseenFailed.length) return;
    for (const item of unseenFailed) {
      shownFailedTaskIds.add(item.id);
    }
    const latest = unseenFailed[unseenFailed.length - 1];
    const rawErr = String(latest.error ?? "").trim();
    toastManager.push({
      kind: "error",
      group: "task-error",
      dedupeKey: `task-error:${latest.id}`,
      resolveText: () => formatError(rawErr, "state"),
    });
  },
  { deep: true, immediate: true },
);

const nav = computed(() => [
  { id: "device", label: t("nav.device"), icon: PhHardDrives },
  { id: "api", label: t("nav.api"), icon: PhGlobe },
  { id: "comfyui", label: t("nav.comfy"), icon: PhFlowArrow },
  { id: "settings", label: t("nav.settings"), icon: PhGearSix },
]);

const modelsDrawerOpen = ref(false);
const contentRef = ref<ComponentPublicInstance | HTMLElement | null>(null);
const scrollController = createColabOneScrollController();
const settingsTopSaveSlotRef = ref<HTMLElement | null>(null);
const settingsTopSaveWidthPx = ref<number | null>(null);
const settingsSaveFloating = ref(false);
const showFloatingSettingsSave = computed(
  () => page.value === "settings" && settingsSaveFloating.value,
);
let settingsTopSaveObserver: IntersectionObserver | null = null;
let observedScrollContentEl: HTMLElement | null = null;

function resolveContentElement(): HTMLElement | null {
  const target = contentRef.value;
  if (!target) return null;
  if (target instanceof HTMLElement) return target;
  if ("$el" in target && target.$el instanceof HTMLElement) return target.$el;
  return null;
}

function updateSettingsFloatingSave() {
  if (page.value !== "settings") {
    settingsSaveFloating.value = false;
    return;
  }
  const container = resolveContentElement();
  const slot = settingsTopSaveSlotRef.value;
  if (!container || !slot) {
    settingsSaveFloating.value = false;
    return;
  }
  const containerRect = container.getBoundingClientRect();
  const slotRect = slot.getBoundingClientRect();
  if (containerRect.height <= 0 || slotRect.height <= 0) {
    settingsSaveFloating.value = false;
    return;
  }
  if (!settingsSaveFloating.value && slotRect.width > 1) {
    settingsTopSaveWidthPx.value = Math.ceil(slotRect.width);
  }
  settingsSaveFloating.value = slotRect.bottom <= containerRect.top + 1;
}

function bindSettingsTopSaveObserver() {
  settingsTopSaveObserver?.disconnect();
  settingsTopSaveObserver = null;
  if (page.value !== "settings") {
    settingsSaveFloating.value = false;
    return;
  }
  const container = resolveContentElement();
  const slot = settingsTopSaveSlotRef.value;
  if (!container || !slot) {
    settingsSaveFloating.value = false;
    return;
  }
  if (typeof IntersectionObserver !== "undefined") {
    settingsTopSaveObserver = new IntersectionObserver(
      () => {
        updateSettingsFloatingSave();
      },
      {
        root: container,
        threshold: [0],
      },
    );
    settingsTopSaveObserver.observe(slot);
  }
  updateSettingsFloatingSave();
}

function scrollWorkspaceToTop() {
  const el = resolveContentElement();
  if (el) {
    scrollController.scrollToTop(el);
  }
}

function selectPage(nextPage: string) {
  if (page.value === nextPage) {
    scrollWorkspaceToTop();
    return;
  }
  page.value = nextPage;
}

watch(page, (next) => {
  if (next !== "api") modelsDrawerOpen.value = false;
  if (next !== "settings") settingsSaveFloating.value = false;
});
watch(
  page,
  () => {
    scrollWorkspaceToTop();
  },
  { flush: "post" },
);
watch(
  [page, settingsTopSaveSlotRef],
  () => {
    bindSettingsTopSaveObserver();
  },
  { flush: "post" },
);

function toggleAppearance(event?: MouseEvent) {
  const next: ColabOneAppearance =
    colabOneEffectiveAppearance.value === "dark" ? "light" : "dark";
  setColabOneAppearance(next, event);
}

const logsOpen = ref(false);
const logWindowStandalone = ref(false);
const savedLogWidth = Number(
  localStorage.getItem(COLABONE_STORAGE_KEYS.logsWidth),
);
const logDrawerWidth = ref(
  Number.isFinite(savedLogWidth) && savedLogWidth >= 320 && savedLogWidth <= 640
    ? Math.round(savedLogWidth)
    : 420,
);
let logTransitionRunning = false;
let pendingLogTarget: boolean | null = null;

async function syncLogWindowState() {
  const res = await colabOneGetLogDrawerState();
  if (res.standalone) {
    logWindowStandalone.value = true;
    logsOpen.value = res.open;
  }
}

function handleLogWindowEvent(event: Event) {
  const detail = (event as CustomEvent<{ open?: boolean }>).detail;
  if (typeof detail?.open === "boolean") {
    logWindowStandalone.value = true;
    logsOpen.value = detail.open;
  }
}

async function applyLogWindowToggle(nextOpen: boolean) {
  const res = await colabOneSetLogDrawer(nextOpen, logDrawerWidth.value);
  logWindowStandalone.value = Boolean(res.standalone);
  logsOpen.value = res.standalone ? res.open : nextOpen;
}

async function setLogsOpen(nextOpen: boolean) {
  pendingLogTarget = nextOpen;
  if (logTransitionRunning) return;
  logTransitionRunning = true;
  try {
    while (pendingLogTarget !== null) {
      const target = pendingLogTarget;
      pendingLogTarget = null;
      await applyLogWindowToggle(target);
    }
  } finally {
    logTransitionRunning = false;
  }
}

function onLogDrawerWidthChange(width: number) {
  logDrawerWidth.value = width;
  localStorage.setItem(COLABONE_STORAGE_KEYS.logsWidth, String(width));
}

const activeApiRequest = computed(
  () =>
    state.value.runtime.public_api?.recent_requests?.find(
      (req) => req.state === "running",
    ) || null,
);
const activeApiSpeed = computed(() =>
  activeApiRequest.value
    ? {
        tokensPerSecond: activeApiRequest.value.tokens_per_second,
        completionTokens: activeApiRequest.value.completion_tokens,
        estimated: activeApiRequest.value.estimated,
      }
    : null,
);

const instanceCount = computed(
  () => state.value.account.assignments ?? (state.value.runtime.id ? 1 : 0),
);
const billing = computed(() => (state.value.account.rate ?? 0) > 0);
const accountStale = computed(
  () =>
    !state.value.account.checked_at ||
    Date.now() / 1000 - state.value.account.checked_at > 60,
);
const releasePlan = ref<{
  id: string;
  instances: AccountInstance[];
} | null>(null);
const selectedReleaseIds = ref<string[]>([]);

const ready = computed(() => state.value.runtime.state === "ready");
const task = computed(() =>
  state.value.runtime.tasks?.find((t) => t.state === "running"),
);
const dockTask = computed(() =>
  state.value.runtime.tasks?.find(
    (t) =>
      t.state === "running" &&
      t.kind !== "public_chat" &&
      t.kind !== "public_responses" &&
      t.kind !== "public_compact",
  ),
);

const runtimeBadgeStatus = computed<"success" | "processing" | "default">(
  () => {
    if (
      ipcDisconnected.value ||
      ["starting", "releasing", "disconnected"].includes(
        state.value.runtime.state,
      )
    ) {
      return "processing";
    }
    return ready.value ? "success" : "default";
  },
);
const runtimeTagColor = computed<"success" | "processing" | "default">(() => {
  if (
    ipcDisconnected.value ||
    ["starting", "releasing", "disconnected"].includes(
      state.value.runtime.state,
    )
  ) {
    return "processing";
  }
  return ready.value ? "success" : "default";
});

const currentActivity = computed(() =>
  operationBannerTitle.value
    ? operationBannerTitle.value
    : task.value
      ? {
          download: t("task.download"),
          load: t("task.load"),
          public_chat: t("task.publicChat"),
          public_responses: t("task.publicResponses"),
          public_compact: t("task.publicCompact"),
          comfyui_start: t("task.comfyStart"),
          comfyui_run: t("task.comfyRun"),
        }[task.value.kind] || t("task.runningGeneric")
      : ready.value
        ? t("task.gpuReadyIdle")
        : t("task.gpuWaitingStart"),
);

const showRelease = ref(false),
  force = ref(false);

const activeVramModel = computed(() =>
  state.value.runtime.state === "stopped"
    ? undefined
    : state.value.models.find(
        (m) => m.loaded || m.id === state.value.runtime.loaded_model,
      ),
);

function isNavLive(id: string): boolean {
  if (id === "device") return ready.value;
  if (id === "api") return state.value.runtime.public_api?.state === "ready";
  if (id === "comfyui") return !!state.value.runtime.comfyui?.running;
  return false;
}
function navDotColor(id: string): string {
  if (id === "comfyui") return "#3b82f6";
  return "#10b981";
}

const downloadThreadOptions = computed(() => [
  { label: t("settings.threadsOption", { count: 16 }), value: 16 },
  { label: t("settings.threadsOption", { count: 32 }), value: 32 },
]);
const appearanceSegmentedOptions = computed(() =>
  COLABONE_APPEARANCE_OPTIONS.map((option) => ({
    value: option.value,
    title: option.label,
    payload: { label: option.label },
  })),
);

const accountInstances = computed<AccountInstance[]>(() => {
  const raw = state.value.account.instances || [];
  if (raw.length) return raw;
  if (state.value.runtime.id && state.value.runtime.state !== "stopped") {
    return [
      {
        id: state.value.runtime.id,
        managed: true,
        active: true,
        accelerator: state.value.settings.gpu || "GPU",
        state: state.value.runtime.state,
        gpu_name: state.value.runtime.gpu?.name,
        vram_used_mib: state.value.runtime.gpu?.used_mib,
        vram_total_mib: state.value.runtime.gpu?.total_mib,
        disk_free: state.value.runtime.disk_free,
        loaded_model: activeVramModel.value?.name,
        active_task: task.value?.kind,
        drive_connected: state.value.setup.drive,
        location: state.value.runtime.location,
      },
    ];
  }
  return [];
});
const activeLocation = computed(() => {
  if (state.value.runtime.state === "stopped") return null;
  return (
    state.value.runtime.location ||
    accountInstances.value.find((i) => i.active && i.managed)?.location ||
    null
  );
});
const managedInstances = computed(() =>
  accountInstances.value.filter((i) => i.managed),
);
const externalInstances = computed(() =>
  accountInstances.value.filter((i) => !i.managed),
);
const showInstancesSkeleton = computed(
  () =>
    (accountInstances.value.length > 0 && refreshingAccount.value) ||
    (snapshotLoaded.value &&
      state.value.setup.colab &&
      !hasSyncError.value &&
      (!state.value.account.checked_at || refreshingAccount.value) &&
      (state.value.account.assignments ?? 0) > 0 &&
      !accountInstances.value.length),
);
const instanceSkeletonCount = computed(() =>
  Math.min(
    3,
    Math.max(
      1,
      accountInstances.value.length || state.value.account.assignments || 1,
    ),
  ),
);
const settingsEmailLoading = computed(
  () =>
    !snapshotLoaded.value ||
    (state.value.setup.colab &&
      !state.value.account.email &&
      (refreshingAccount.value ||
        (!state.value.account.checked_at && !hasSyncError.value))),
);
const shapeLabel = (shape?: string) =>
  shape === "HIGH_RAM"
    ? t("device.shapeHighRam")
    : shape === "STANDARD"
      ? t("device.shapeStandard")
      : "";
const taskKindLabel = (kind?: string) =>
  (kind &&
    {
      download: t("task.kindDownload"),
      load: t("task.kindLoad"),
      public_chat: t("task.kindPublicChat"),
      public_responses: t("task.kindPublicResponses"),
      public_compact: t("task.kindPublicCompact"),
      comfyui_start: t("task.kindComfyStart"),
      comfyui_run: t("task.kindComfyRun"),
    }[kind]) ||
  "";
const taskBarTitle = (kind: string) =>
  ({
    download: t("task.download"),
    load: t("task.loadRemote"),
    public_chat: t("task.publicChatShort"),
    public_responses: t("task.publicResponses"),
    public_compact: t("task.publicCompact"),
    comfyui_start: t("task.comfyStart"),
    comfyui_run: t("task.comfyRun"),
  })[kind] || t("task.executingGeneric");

const GPU_SPECS = computed(
  () =>
    [
      { id: "T4", label: t("device.gpuSpecT4") },
      { id: "L4", label: t("device.gpuSpecL4") },
      { id: "A100", label: t("device.gpuSpecA100") },
      { id: "H100", label: t("device.gpuSpecH100") },
      { id: "G4", label: t("device.gpuSpecG4") },
    ] as const,
);

const form = ref({
  gpu: "T4",
  idle_minutes: 30,
  idle_countdown_seconds: 60,
  auto_drive: true,
  download_threads: 16,
  assign_proxy_enabled: false,
  assign_proxy: "",
  hf_token: "",
});
const isPresetIdleMinutes = (m: number) =>
  COLABONE_IDLE_RELEASE_OPTIONS.some((opt) => opt.value === m);

const clockNow = ref(Date.now());
const billingOverview = computed(() =>
  describeColabOneBilling(
    state.value.account,
    state.value.runtime.state === "stopped"
      ? undefined
      : state.value.runtime.id,
    clockNow.value,
  ),
);

const code = ref(""),
  showAuthPaste = ref(false);

let timer: ReturnType<typeof setTimeout>,
  stopped = false;
let clockTimer: ReturnType<typeof setInterval>;
let lastInteraction = 0;

const idle = computed(() => state.value.runtime.idle);
const snapshotAt = ref(Date.now());
const idleRemaining = computed(() =>
  Math.max(
    0,
    Math.ceil(
      (idle.value?.remaining_seconds ?? 0) -
        (clockNow.value - snapshotAt.value) / 1000,
    ),
  ),
);
const countdown = computed(
  () =>
    idle.value &&
    idle.value.phase !== "active" &&
    idleRemaining.value <= idle.value.countdown_seconds,
);
const duration = (seconds: number | null) => {
  if (seconds === null) return t("common.calculating");
  const whole = Math.ceil(Math.max(0, seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
};

function interaction(event?: Event) {
  if (event && !event.isTrusted) return;
  if (!ready.value || Date.now() - lastInteraction < 5000) return;
  lastInteraction = Date.now();
  colabOneCommand("runtime.activity").catch(() => {});
}

function handleGlobalKeydown(e: KeyboardEvent) {
  if (e.key === "Escape") {
    if (showRelease.value && !busy.value) {
      showRelease.value = false;
    } else if (
      !modelsDrawerOpen.value &&
      !logWindowStandalone.value &&
      logsOpen.value
    ) {
      void setLogsOpen(false);
    }
    return;
  }
  if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "j") {
    e.preventDefault();
    void setLogsOpen(!logsOpen.value);
    return;
  }
  if (
    e.altKey &&
    !e.ctrlKey &&
    !e.metaKey &&
    !e.shiftKey &&
    !modelsDrawerOpen.value &&
    !showRelease.value
  ) {
    const map: Record<string, string> = {
      "1": "device",
      "2": "api",
      "3": "comfyui",
      "4": "settings",
    };
    const targetPage = map[e.key];
    if (targetPage) {
      e.preventDefault();
      selectPage(targetPage);
      return;
    }
  }
}

let initialized = false,
  openedAuth = "";
let lastSyncedSettings: Partial<typeof state.value.settings> = {};
const pendingSettings = new Map<string, number>();

function beginPendingSetting(keys: readonly string[]) {
  for (const key of keys) {
    pendingSettings.set(key, (pendingSettings.get(key) ?? 0) + 1);
  }
}

function endPendingSetting(keys: readonly string[]) {
  for (const key of keys) {
    const next = (pendingSettings.get(key) ?? 0) - 1;
    if (next <= 0) pendingSettings.delete(key);
    else pendingSettings.set(key, next);
  }
}

function syncFormFromSettings(next: typeof state.value.settings | undefined) {
  if (!next) return;
  const keys = [
    "gpu",
    "idle_minutes",
    "idle_countdown_seconds",
    "auto_drive",
    "download_threads",
    "assign_proxy_enabled",
  ] as const;
  if (!initialized) {
    for (const key of keys) {
      if (next[key] !== undefined && !pendingSettings.has(key)) {
        (form.value as Record<string, unknown>)[key] = next[key];
      }
    }
    lastSyncedSettings = { ...next };
    initialized = true;
    return;
  }
  for (const key of keys) {
    if (pendingSettings.has(key)) continue;
    if (next[key] !== undefined && next[key] !== lastSyncedSettings[key]) {
      (form.value as Record<string, unknown>)[key] = next[key];
      (lastSyncedSettings as Record<string, unknown>)[key] = next[key];
    }
  }
}

const bytes = (n = 0) =>
  n >= 1024 ** 3
    ? (n / 1024 ** 3).toFixed(1) + " GB"
    : (n / 1024 ** 2).toFixed(0) + " MB";

function notify(
  text: string,
  kind: ColabOneToastKind = "success",
  dedupeKey?: string,
) {
  toastManager.push({ kind, message: text, dedupeKey });
}

function onLocaleChanged(meta: ColabOneLocaleMeta) {
  notify(
    t("nav.languageSwitched", { label: meta.nativeLabel }),
    "success",
    "locale-switched",
  );
}

async function syncSnapshot() {
  const snapId = ++snapshotSeq;
  const value = await colabOneSnapshot();
  if (stopped || snapId < appliedSnapshotSeq) return;
  appliedSnapshotSeq = snapId;
  const wasDisconnected = ipcDisconnected.value;
  ipcFailures = 0;
  ipcDisconnected.value = false;
  if (ipcError.value) ipcError.value = "";
  toastManager.dismissGroup("ipc-transient");
  if (wasDisconnected) clockNow.value = Date.now();
  const rawError = String(value.error ?? "").trim();
  if (clearingStateError) {
    if (rawError === clearingStateError.text) {
      if (
        clearingStateError.completedAtSeq === null ||
        snapId <= clearingStateError.completedAtSeq
      ) {
        value.error = "";
      }
    } else {
      clearingStateError = null;
      if (!rawError) {
        handledStateError = "";
      }
    }
  }
  if (
    value.error &&
    isTransientIpcError(value.error) &&
    (value.runtime.state === "ready" ||
      (value.runtime.state === "stopped" &&
        !value.operation &&
        (value.account.assignments ?? 0) === 0))
  ) {
    const staleErr = String(value.error).trim();
    value.error = "";
    colabOneCommand("error.clear", { expected: staleErr }).catch(() => {});
  }
  if (
    value.runtime.idle?.server_time !== state.value.runtime.idle?.server_time ||
    value.operation?.retry_at !== state.value.operation?.retry_at
  ) {
    snapshotAt.value = Date.now();
    clockNow.value = Date.now();
  }
  state.value = value;
  snapshotLoaded.value = true;
  syncFormFromSettings(value.settings);
  hydrateColabOneLocaleFromSettings(value.settings?.locale);
}

async function refresh() {
  try {
    await syncSnapshot();
  } catch (e) {
    ipcFailures += 1;
    if (ipcFailures >= 2) {
      ipcDisconnected.value = true;
      ipcError.value = String(e ?? "");
    }
  }
  if (!stopped) timer = setTimeout(refresh, 600);
}

async function act(action: string, payload: Record<string, unknown> = {}) {
  busyCount.value += 1;
  try {
    const res = await colabOneCommand(action, payload);
    clearActionErrors();
    await syncSnapshot().catch(() => {});
    return res;
  } catch (e) {
    showError(e);
    return null;
  } finally {
    busyCount.value = Math.max(0, busyCount.value - 1);
  }
}

async function refreshAccount() {
  refreshingAccount.value = true;
  try {
    if (await act("account.refresh")) {
      notify(t("device.toastRefreshed"));
    }
  } finally {
    refreshingAccount.value = false;
  }
}

const checkingDriveHeadless = ref(false);

function finishHeadlessDriveCheck() {
  if (!checkingDriveHeadless.value) return;
  checkingDriveHeadless.value = false;
  if (state.value.setup.drive_headless && !state.value.error) {
    notify(t("settings.driveHeadlessEnabledToast"));
  }
}

async function setupHeadlessDrive() {
  checkingDriveHeadless.value = true;
  const res = await act("drive.setup_headless");
  if (!res) {
    checkingDriveHeadless.value = false;
    return;
  }
  if (!state.value.operation) {
    finishHeadlessDriveCheck();
  }
}

const confirmingDriveAuth = ref(false);
let lastDriveProbeAt = 0;

const cancellingStart = ref(false);
const retryingStart = ref(false);
const startCancelPendingToast = ref(false);

const isStartOperationActive = computed(
  () => state.value.operation?.kind === "runtime_start",
);
const isStartCancelling = computed(
  () =>
    cancellingStart.value ||
    Boolean(isStartOperationActive.value && state.value.operation?.cancelling),
);
const canCancelStart = computed(
  () =>
    isStartOperationActive.value &&
    Boolean(state.value.operation?.cancellable) &&
    !isStartCancelling.value,
);
const showStartCancelButton = computed(
  () =>
    isStartOperationActive.value &&
    (Boolean(state.value.operation?.cancellable) ||
      Boolean(state.value.operation?.cancelling) ||
      cancellingStart.value),
);
const canRetryStartNow = computed(
  () =>
    isStartOperationActive.value &&
    Boolean(state.value.operation?.waiting_retry) &&
    !isStartCancelling.value &&
    !retryingStart.value,
);
const showStartRetryButton = computed(
  () =>
    isStartOperationActive.value &&
    !isStartCancelling.value &&
    (Boolean(state.value.operation?.waiting_retry) ||
      Boolean(state.value.operation?.has_retried)),
);
const showStartOperationActions = computed(
  () => showStartRetryButton.value || showStartCancelButton.value,
);

const startRetryRemainingSeconds = computed<number | null>(() => {
  const op = state.value.operation;
  if (
    !op ||
    op.kind !== "runtime_start" ||
    !op.waiting_retry ||
    op.cancelling ||
    cancellingStart.value
  ) {
    return null;
  }
  const retryAt = Number(op.retry_at || 0);
  if (!Number.isFinite(retryAt) || retryAt <= 0) return null;
  return Math.max(0, Math.ceil(retryAt - clockNow.value / 1000));
});

const operationBannerTitle = computed(() => {
  const op = state.value.operation;
  if (!op) return "";
  if (
    op.kind === "runtime_start" &&
    op.waiting_retry &&
    !op.cancelling &&
    !cancellingStart.value
  ) {
    const gpu = op.gpu || form.value.gpu;
    const next = Number(op.next_attempt || (op.attempt ? op.attempt + 1 : 2));
    const total = Number(op.total_attempts || 10);
    const remaining = startRetryRemainingSeconds.value;
    const kind = op.retry_kind;
    if (remaining !== null && remaining > 0) {
      if (kind === "capacity") {
        return t("device.waitingRetryCapacity", {
          seconds: remaining,
          gpu,
          next,
          total,
        });
      }
      if (kind === "server_busy") {
        return t("device.waitingRetryServerBusy", {
          seconds: remaining,
          gpu,
          next,
          total,
        });
      }
      return t("device.waitingRetryNetwork", {
        seconds: remaining,
        gpu,
        next,
        total,
      });
    }
    if (kind === "capacity") {
      return t("device.waitingRetryCapacitySoon", { gpu, next, total });
    }
    if (kind === "server_busy") {
      return t("device.waitingRetryServerBusySoon", { gpu, next, total });
    }
    return t("device.waitingRetryNetworkSoon", { gpu, next, total });
  }
  return tb(op.title);
});

async function cancelStart() {
  if (!canCancelStart.value) return;
  cancellingStart.value = true;
  startCancelPendingToast.value = true;
  try {
    const res = await colabOneCommand("runtime.start.cancel", {});
    if (!res?.cancelled) {
      startCancelPendingToast.value = false;
    }
    await syncSnapshot().catch(() => {});
    if (!state.value.operation) {
      cancellingStart.value = false;
      if (startCancelPendingToast.value && !state.value.error) {
        startCancelPendingToast.value = false;
        notify(
          t("device.toastAllocationCancelled"),
          "info",
          "runtime-start-cancelled",
        );
      }
    }
  } catch (e) {
    startCancelPendingToast.value = false;
    cancellingStart.value = false;
    showError(e);
  } finally {
    if (!state.value.operation || !state.value.operation.cancelling) {
      cancellingStart.value = false;
    }
  }
}

async function retryStartNow() {
  if (!canRetryStartNow.value) return;
  retryingStart.value = true;
  try {
    const res = await colabOneCommand("runtime.start.retry", {});
    if (res?.triggered) {
      notify(
        t("device.toastRetryTriggered"),
        "info",
        "runtime-start-retry-now",
      );
    }
    await syncSnapshot().catch(() => {});
  } catch (e) {
    showError(e);
  } finally {
    retryingStart.value = false;
  }
}

function clearConfirmDriveTimer() {
  confirmingDriveAuth.value = false;
}

async function confirmDriveAuth() {
  if (confirmingDriveAuth.value) return;
  confirmingDriveAuth.value = true;
  try {
    await colabOneCommand("drive.confirm", {});
    await syncSnapshot().catch(() => {});
  } catch (e) {
    showError(e);
  } finally {
    confirmingDriveAuth.value = false;
  }
}

function probeDriveAuthOnReturn() {
  if (
    stopped ||
    !state.value.auth_url ||
    !state.value.operation ||
    state.value.setup.drive
  ) {
    return;
  }
  if (typeof document !== "undefined" && document.visibilityState === "hidden") {
    return;
  }
  const now = Date.now();
  if (now - lastDriveProbeAt < 1200) return;
  lastDriveProbeAt = now;
  void colabOneCommand("drive.probe", {}).catch(() => {});
}

watch(
  () => state.value.operation,
  (op) => {
    if (!op && checkingDriveHeadless.value) {
      finishHeadlessDriveCheck();
    }
    if (!op) {
      clearConfirmDriveTimer();
      cancellingStart.value = false;
      retryingStart.value = false;
      if (startCancelPendingToast.value) {
        startCancelPendingToast.value = false;
        if (!state.value.error) {
          notify(
            t("device.toastAllocationCancelled"),
            "info",
            "runtime-start-cancelled",
          );
        }
      }
    }
  },
);

watch(
  () => state.value.auth_url,
  (url) => {
    if (!url) {
      clearConfirmDriveTimer();
    }
  },
);

async function changeIdleMinutes(rawMinutes: unknown) {
  const minutes = Number(rawMinutes);
  if (!Number.isFinite(minutes) || minutes < 1) return;
  const previous = form.value.idle_minutes;
  form.value.idle_minutes = minutes;
  lastSyncedSettings.idle_minutes = minutes;
  savingIdle.value = true;
  beginPendingSetting(["idle_minutes"]);
  try {
    await colabOneCommand("settings.save", { idle_minutes: minutes });
    clearActionErrors();
    notify(
      t("device.toastIdleSet", { policy: formatIdlePolicyLabel(minutes) }),
      "success",
      "setting-idle-policy",
    );
  } catch (e) {
    form.value.idle_minutes = previous;
    lastSyncedSettings.idle_minutes = previous;
    showError(e);
  } finally {
    endPendingSetting(["idle_minutes"]);
    savingIdle.value = false;
    await syncSnapshot().catch(() => {});
  }
}

async function changeDownloadThreads(rawThreads: unknown) {
  const threads = Number(rawThreads);
  if (threads !== 16 && threads !== 32) return;
  const previous = form.value.download_threads;
  form.value.download_threads = threads;
  lastSyncedSettings.download_threads = threads;
  beginPendingSetting(["download_threads"]);
  try {
    await colabOneCommand("settings.save", { download_threads: threads });
    clearActionErrors();
    notify(
      t("device.toastThreadsSet", { threads }),
      "success",
      "setting-download-threads",
    );
  } catch (e) {
    form.value.download_threads = previous;
    lastSyncedSettings.download_threads = previous;
    showError(e);
  } finally {
    endPendingSetting(["download_threads"]);
    await syncSnapshot().catch(() => {});
  }
}

function onGpuSelect(raw: unknown) {
  if (raw !== undefined && raw !== null) {
    form.value.gpu = String(raw);
  }
}

async function changeDefaultGpu(raw: unknown) {
  if (raw === undefined || raw === null) return;
  const gpu = String(raw);
  const previous = form.value.gpu;
  form.value.gpu = gpu;
  lastSyncedSettings.gpu = gpu;
  beginPendingSetting(["gpu"]);
  try {
    await colabOneCommand("settings.save", { gpu });
    clearActionErrors();
    notify(
      t("device.toastDefaultGpuSet", { gpu }),
      "success",
      "setting-default-gpu",
    );
  } catch (e) {
    form.value.gpu = previous;
    lastSyncedSettings.gpu = previous;
    showError(e);
  } finally {
    endPendingSetting(["gpu"]);
    await syncSnapshot().catch(() => {});
  }
}

async function onAutoDriveChange(raw: unknown) {
  const next = Boolean(raw);
  const previous = form.value.auto_drive;
  form.value.auto_drive = next;
  lastSyncedSettings.auto_drive = next;
  beginPendingSetting(["auto_drive"]);
  try {
    const res = await act("settings.save", { auto_drive: next });
    if (res) {
      notify(t("device.toastSettingsSaved"), "success", "setting-auto-drive");
    } else {
      form.value.auto_drive = previous;
      lastSyncedSettings.auto_drive = previous;
    }
  } finally {
    endPendingSetting(["auto_drive"]);
    await syncSnapshot().catch(() => {});
  }
}

let lastAppearancePointer: { x: number; y: number; at: number } | null = null;

function rememberAppearancePointer(event: PointerEvent | MouseEvent) {
  if (event.clientX > 0 || event.clientY > 0) {
    lastAppearancePointer = {
      x: event.clientX,
      y: event.clientY,
      at: Date.now(),
    };
  }
}

function onAppearanceChange(raw: unknown) {
  const next = String(raw);
  if (next === "system" || next === "light" || next === "dark") {
    const origin =
      lastAppearancePointer && Date.now() - lastAppearancePointer.at < 600
        ? { x: lastAppearancePointer.x, y: lastAppearancePointer.y }
        : null;
    lastAppearancePointer = null;
    setColabOneAppearance(next as ColabOneAppearance, origin);
  }
}

function onAuthPasteToggle(keys: unknown) {
  showAuthPaste.value = Array.isArray(keys)
    ? keys.includes("paste")
    : keys === "paste";
}

async function beginLogin() {
  await act("auth.colab.auto");
}
async function manualLogin() {
  const result = await act("auth.colab.begin");
  if (result) {
    showAuthPaste.value = true;
    if (result.url && result.url !== openedAuth) {
      openedAuth = result.url;
      await openExternalURL(result.url);
    }
  }
}
async function finishLogin() {
  const reply = code.value.trim();
  if (!reply) return;
  if (await act("auth.colab.finish", { code: reply })) {
    code.value = "";
    notify(t("settings.verifyingAuthToast"));
  }
}
async function pasteLogin() {
  try {
    code.value = await colabOneReadClipboard();
    await finishLogin();
  } catch {
    showError(t("settings.clipboardReadFailed"));
  }
}
watch(
  () => state.value.auth_url,
  async (url) => {
    if (!url) {
      openedAuth = "";
      return;
    }
    if (url !== openedAuth) {
      openedAuth = url;
      try {
        await colabOneOpenURL(url);
      } catch (e) {
        showError(e);
      }
    }
  },
);

const hfTokenInputRef = ref<InstanceType<typeof ColabOneSecretInput> | null>(
  null,
);
const assignProxyInputRef = ref<InstanceType<
  typeof ColabOneSecretInput
> | null>(null);
const savingAssignProxy = ref(false);
const clearingAssignProxy = ref(false);
const togglingAssignProxy = ref(false);
const testingAssignProxy = ref(false);
const assignProxyExpanded = ref(false);
const assignProxyMutating = computed(
  () =>
    savingAssignProxy.value ||
    clearingAssignProxy.value ||
    togglingAssignProxy.value,
);

const assignProxySettings = computed(() => state.value.assign_proxy_settings);
const hasSavedAssignProxy = computed(() =>
  Boolean(assignProxySettings.value?.has_proxy),
);
const showAssignProxyEditor = computed(
  () => assignProxyExpanded.value || Boolean(form.value.assign_proxy.trim()),
);
const assignProxySavedPlaceholder = computed(() =>
  assignProxySettings.value?.masked_url
    ? t("settings.assignProxySavedPlaceholderWithUrl", {
        url: assignProxySettings.value.masked_url,
      })
    : t("settings.assignProxySavedPlaceholder"),
);

async function loadSavedHfToken(): Promise<string> {
  const res = await colabOneCommand("settings.hf_token");
  return typeof res?.token === "string" ? res.token : "";
}

async function loadSavedAssignProxy(): Promise<string> {
  const res = await colabOneCommand("settings.assign_proxy");
  return typeof res?.url === "string" ? res.url : "";
}

async function onAssignProxyEnabledChange(raw: unknown) {
  if (assignProxyMutating.value || testingAssignProxy.value) return;
  const next = Boolean(raw);
  const draft = form.value.assign_proxy.trim();
  if (next && !hasSavedAssignProxy.value && !draft) {
    notify(
      t("settings.assignProxyNeedUrlFirst"),
      "warning",
      "setting-assign-proxy-empty",
    );
    return;
  }
  const previous = form.value.assign_proxy_enabled;
  form.value.assign_proxy_enabled = next;
  lastSyncedSettings.assign_proxy_enabled = next;
  beginPendingSetting(["assign_proxy_enabled"]);
  togglingAssignProxy.value = true;
  try {
    const payload: Record<string, unknown> = { assign_proxy_enabled: next };
    if (next && !hasSavedAssignProxy.value && draft) {
      payload.assign_proxy = draft;
    }
    const res = await act("settings.save", payload);
    if (res) {
      if (payload.assign_proxy) {
        form.value.assign_proxy = "";
        assignProxyInputRef.value?.hide();
      }
      notify(
        next
          ? t("settings.assignProxyEnabledToast")
          : t("settings.assignProxyDisabledToast"),
        "success",
        "setting-assign-proxy-toggle",
      );
    } else {
      form.value.assign_proxy_enabled = previous;
      lastSyncedSettings.assign_proxy_enabled = previous;
    }
  } finally {
    togglingAssignProxy.value = false;
    endPendingSetting(["assign_proxy_enabled"]);
    await syncSnapshot().catch(() => {});
  }
}

async function saveAssignProxy() {
  if (assignProxyMutating.value || testingAssignProxy.value) return;
  const raw = form.value.assign_proxy.trim();
  if (!raw) return;
  savingAssignProxy.value = true;
  beginPendingSetting(["assign_proxy_enabled"]);
  try {
    const res = await act("settings.save", {
      assign_proxy: raw,
      assign_proxy_enabled: true,
    });
    if (res) {
      form.value.assign_proxy = "";
      form.value.assign_proxy_enabled = true;
      lastSyncedSettings.assign_proxy_enabled = true;
      assignProxyExpanded.value = false;
      assignProxyInputRef.value?.hide();
      notify(t("settings.assignProxySavedToast"), "success", "setting-assign-proxy-save");
    }
  } finally {
    savingAssignProxy.value = false;
    endPendingSetting(["assign_proxy_enabled"]);
    await syncSnapshot().catch(() => {});
  }
}

async function clearAssignProxy() {
  if (assignProxyMutating.value || testingAssignProxy.value) return;
  clearingAssignProxy.value = true;
  beginPendingSetting(["assign_proxy_enabled"]);
  try {
    const res = await act("settings.save", { clear_assign_proxy: true });
    if (res) {
      form.value.assign_proxy = "";
      form.value.assign_proxy_enabled = false;
      lastSyncedSettings.assign_proxy_enabled = false;
      assignProxyExpanded.value = false;
      assignProxyInputRef.value?.hide();
      notify(t("settings.assignProxyClearedToast"), "success", "setting-assign-proxy-clear");
    }
  } finally {
    clearingAssignProxy.value = false;
    endPendingSetting(["assign_proxy_enabled"]);
    await syncSnapshot().catch(() => {});
  }
}

async function testAssignProxy() {
  if (assignProxyMutating.value || testingAssignProxy.value) return;
  const raw = form.value.assign_proxy.trim();
  if (!raw && !hasSavedAssignProxy.value) return;
  testingAssignProxy.value = true;
  try {
    const res = await colabOneCommand(
      "settings.assign_proxy.test",
      raw ? { assign_proxy: raw } : {},
    );
    const ms = Number(res?.latency_ms || 0);
    notify(
      t("settings.assignProxyTestOkToast", { ms: ms > 0 ? ms : 1 }),
      "success",
      "setting-assign-proxy-test",
    );
    await syncSnapshot().catch(() => {});
  } catch (err) {
    showError(err);
  } finally {
    testingAssignProxy.value = false;
  }
}

function onHfTokenExtracted() {
  notify(t("settings.hfTokenExtractedToast"), "success");
}

async function save() {
  const payload: Record<string, unknown> = {
    gpu: form.value.gpu,
    idle_minutes: form.value.idle_minutes,
    auto_drive: form.value.auto_drive,
    download_threads: form.value.download_threads,
  };
  if (form.value.hf_token.trim()) {
    payload.hf_token = form.value.hf_token.trim();
  }
  if (await act("settings.save", payload)) {
    lastSyncedSettings = {
      ...lastSyncedSettings,
      gpu: form.value.gpu,
      idle_minutes: form.value.idle_minutes,
      auto_drive: form.value.auto_drive,
      download_threads: form.value.download_threads,
    };
    form.value.hf_token = "";
    hfTokenInputRef.value?.hide();
    notify(t("device.toastSettingsSaved"));
  }
}

async function start() {
  if (
    await act("settings.save", {
      gpu: form.value.gpu,
      idle_minutes: form.value.idle_minutes,
      auto_drive: form.value.auto_drive,
    })
  )
    await act("runtime.start");
}

async function startNew() {
  if (
    await act("settings.save", {
      gpu: form.value.gpu,
      idle_minutes: form.value.idle_minutes,
      auto_drive: form.value.auto_drive,
    })
  )
    await act("runtime.start", { new: true, gpu: form.value.gpu });
}

const monitorBusy = ref(false);
const monitorSwitching = ref(false);
const effectiveMonitor = computed(() =>
  resolveColabOneMonitorFallback(state.value, activeVramModel.value?.name || ""),
);

async function selectMonitorInstance(endpoint: string) {
  if (!endpoint || endpoint === effectiveMonitor.value.endpoint) return;
  monitorBusy.value = true;
  monitorSwitching.value = true;
  try {
    await colabOneCommand("monitor.select", { endpoint });
    clearActionErrors();
    await syncSnapshot().catch(() => {});
  } catch (e) {
    showError(e);
  } finally {
    monitorBusy.value = false;
    monitorSwitching.value = false;
  }
}

async function toggleMonitorPause(paused: boolean) {
  monitorBusy.value = true;
  try {
    await colabOneCommand("monitor.pause", { paused });
    clearActionErrors();
    await syncSnapshot().catch(() => {});
  } catch (e) {
    showError(e);
  } finally {
    monitorBusy.value = false;
  }
}

async function retryMonitor() {
  monitorBusy.value = true;
  monitorSwitching.value = true;
  try {
    await colabOneCommand("monitor.retry");
    clearActionErrors();
    await syncSnapshot().catch(() => {});
  } catch (e) {
    showError(e);
  } finally {
    monitorBusy.value = false;
    monitorSwitching.value = false;
  }
}

async function switchInstance(endpoint: string) {
  if (!endpoint || endpoint === state.value.runtime.id) return;
  await act("runtime.select", { endpoint });
}

const releaseTargetEndpoint = ref<string | undefined>(undefined);
let releasePreviewSeq = 0;

watch(showRelease, (open) => {
  if (!open) {
    releasePreviewSeq += 1;
    releasePlan.value = null;
    selectedReleaseIds.value = [];
    force.value = false;
    releaseTargetEndpoint.value = undefined;
  }
});

async function openRelease(onlyEndpoint?: string) {
  const seq = ++releasePreviewSeq;
  releaseTargetEndpoint.value = onlyEndpoint;
  showRelease.value = true;
  force.value = false;
  releasePlan.value = null;
  selectedReleaseIds.value = [];
  const plan = await act("runtime.release.preview");
  if (seq !== releasePreviewSeq || !showRelease.value) return;
  releasePlan.value = plan;
  if (plan?.instances?.length) {
    if (onlyEndpoint) {
      if (
        plan.instances.some((item: AccountInstance) => item.id === onlyEndpoint)
      ) {
        selectedReleaseIds.value = [onlyEndpoint];
      } else {
        // Target instance is no longer running; never fall back to selecting unrelated instances.
        selectedReleaseIds.value = [];
        notify(t("device.releaseTargetMissing"), "info");
      }
    } else {
      selectedReleaseIds.value = plan.instances.map(
        (item: AccountInstance) => item.id,
      );
    }
  }
}

const isAllReleaseSelected = computed(
  () =>
    !!releasePlan.value?.instances.length &&
    selectedReleaseIds.value.length === releasePlan.value.instances.length,
);

function toggleReleaseSelection(id: string, checked: boolean) {
  if (checked) {
    if (!selectedReleaseIds.value.includes(id)) {
      selectedReleaseIds.value = [...selectedReleaseIds.value, id];
    }
  } else {
    selectedReleaseIds.value = selectedReleaseIds.value.filter(
      (item) => item !== id,
    );
  }
}

function selectManagedReleaseOnly() {
  if (!releasePlan.value) return;
  selectedReleaseIds.value = releasePlan.value.instances
    .filter((item) => item.managed)
    .map((item) => item.id);
}

function selectAllRelease() {
  if (!releasePlan.value) return;
  selectedReleaseIds.value = releasePlan.value.instances.map((item) => item.id);
}

async function release() {
  if (!releasePlan.value || !selectedReleaseIds.value.length) return;
  const payload: Record<string, unknown> = {
    force: force.value,
    plan_id: releasePlan.value.id,
  };
  if (!isAllReleaseSelected.value) {
    payload.endpoints = [...selectedReleaseIds.value];
  }
  const result = await act("runtime.release.all", payload);
  if (result) showRelease.value = false;
}

function openModels() {
  selectPage("api");
  modelsDrawerOpen.value = true;
}

function openDevice() {
  modelsDrawerOpen.value = false;
  selectPage("device");
}

async function updateShortcut(
  target: "start_menu" | "desktop" | "both",
  remove = false,
) {
  const res = await act("desktop.shortcuts", { target, remove });
  if (res) {
    notify(
      remove
        ? t("settings.toastShortcutRemoved")
        : target === "desktop"
          ? t("settings.toastShortcutDesktop")
          : target === "both"
            ? t("settings.toastShortcutBoth")
            : t("settings.toastShortcutStartMenu"),
    );
  }
}

async function copy(text: string) {
  try {
    await colabOneWriteClipboard(text);
    await navigator.clipboard?.writeText(text).catch(() => {});
    notify(t("common.copied"));
  } catch {
    try {
      await navigator.clipboard.writeText(text);
      notify(t("common.copied"));
    } catch {
      showError(t("common.copyFailed"));
    }
  }
}

const activeToastTopOffsetPx = computed(() =>
  modelsDrawerOpen.value
    ? drawerToastTopOffsetPx.value
    : toastTopOffsetPx.value,
);

const toastHorizontalBounds = computed(() =>
  resolveColabOneToastHorizontalBounds({
    viewportWidth: viewportWidth.value,
    modelDrawerOpen: modelsDrawerOpen.value,
    inlineLogDrawerOpen: !logWindowStandalone.value && logsOpen.value,
    inlineLogDrawerWidthPx: logDrawerWidth.value,
  }),
);

function updateDrawerToastTopOffset() {
  if (!modelsDrawerOpen.value || typeof document === "undefined") return;
  const headerEl = document.querySelector<HTMLElement>(
    ".co-model-drawer-root .ant-drawer-header",
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
  const el = resolveContentElement();
  if (el) {
    const top = Math.round(el.getBoundingClientRect().top);
    if (top > 0) {
      toastTopOffsetPx.value = Math.min(260, Math.max(72, top + 6));
    }
  }
  if (modelsDrawerOpen.value) {
    updateDrawerToastTopOffset();
  }
}

watch(
  [
    persistentBannerError,
    () => state.value.operation,
    showUpgradeBanner,
    logsOpen,
    logDrawerWidth,
  ],
  () => {
    updateToastLayout();
  },
  { flush: "post" },
);

watch(
  modelsDrawerOpen,
  (isOpen) => {
    if (isOpen) {
      drawerToastTopOffsetPx.value = COLABONE_DRAWER_TOAST_TOP_FALLBACK_PX;
      scheduleDrawerToastTopMeasure();
    } else {
      updateToastLayout();
    }
  },
  { flush: "post" },
);

onMounted(() => {
  refresh();
  void syncLogWindowState();
  updateToastLayout();
  const contentEl = resolveContentElement();
  if (contentEl) {
    observedScrollContentEl = contentEl;
    contentEl.addEventListener("scroll", updateSettingsFloatingSave, {
      passive: true,
    });
    if (typeof ResizeObserver !== "undefined") {
      toastLayoutObserver = new ResizeObserver(() => {
        updateToastLayout();
        updateSettingsFloatingSave();
      });
      toastLayoutObserver.observe(contentEl);
    }
  }
  bindSettingsTopSaveObserver();
  window.addEventListener("resize", updateToastLayout, { passive: true });
  window.addEventListener("resize", updateSettingsFloatingSave, {
    passive: true,
  });
  clockTimer = setInterval(() => {
    if (!ipcDisconnected.value) clockNow.value = Date.now();
  }, 1000);
  for (const name of ["pointerdown", "keydown", "wheel"])
    window.addEventListener(name, interaction, { passive: true });
  window.addEventListener("keydown", handleGlobalKeydown);
  window.addEventListener("colabone:log-window-state", handleLogWindowEvent);
  window.addEventListener("focus", probeDriveAuthOnReturn);
  document.addEventListener("visibilitychange", probeDriveAuthOnReturn);
});
onUnmounted(() => {
  stopped = true;
  clearConfirmDriveTimer();
  scrollController.cancel();
  settingsTopSaveObserver?.disconnect();
  settingsTopSaveObserver = null;
  observedScrollContentEl?.removeEventListener(
    "scroll",
    updateSettingsFloatingSave,
  );
  observedScrollContentEl = null;
  toastLayoutObserver?.disconnect();
  toastLayoutObserver = null;
  if (drawerHeaderMeasureFrame !== null && typeof window !== "undefined") {
    window.cancelAnimationFrame(drawerHeaderMeasureFrame);
    drawerHeaderMeasureFrame = null;
  }
  window.removeEventListener("resize", updateToastLayout);
  window.removeEventListener("resize", updateSettingsFloatingSave);
  clearInterval(clockTimer);
  for (const name of ["pointerdown", "keydown", "wheel"])
    window.removeEventListener(name, interaction);
  window.removeEventListener("keydown", handleGlobalKeydown);
  window.removeEventListener("colabone:log-window-state", handleLogWindowEvent);
  window.removeEventListener("focus", probeDriveAuthOnReturn);
  document.removeEventListener("visibilitychange", probeDriveAuthOnReturn);
  clearTimeout(timer);
  toastManager.clear();
});
</script>

<template>
  <AConfigProvider :theme="colabOneTheme" :locale="colabOneAntdLocale">
    <ALayout
      class="co-shell co-app-shell"
      :class="{ 'co-logs-open': logsOpen }"
    >
      <!-- Main Workspace -->
      <ALayout class="co-workspace">
        <!-- ColabOne Top Floating Capsule Navbar -->
        <header class="co-navbar">
          <div class="co-navbar-inner">
            <div class="co-nav-left">
              <button
                type="button"
                class="co-brand"
                :aria-label="t('nav.brandAria')"
                @click="selectPage('device')"
              >
                <span
                  class="co-brand-icon"
                  aria-hidden="true"
                  v-html="appIcon"
                ></span>
                <span class="co-brand-text">ColabOne</span>
              </button>
              <span
                class="co-brand-version"
                :aria-label="t('nav.versionAria', { version: appVersionLabel })"
                :title="`ColabOne ${appVersionLabel}`"
              >
                {{ appVersionLabel }}
              </span>
            </div>

            <div class="co-nav-center">
              <nav
                class="co-nav-pill-track"
                :aria-label="t('nav.mainAria')"
                role="menubar"
              >
                <button
                  v-for="(item, idx) in nav"
                  :key="item.id"
                  type="button"
                  role="menuitem"
                  class="co-nav-pill-item"
                  :class="{ 'is-active': page === item.id }"
                  :aria-current="page === item.id ? 'page' : undefined"
                  :aria-label="item.label"
                  :title="`${item.label} (Alt+${idx + 1})`"
                  @click="selectPage(item.id)"
                >
                  <component
                    :is="item.icon"
                    :size="16"
                    class="co-nav-pill-icon"
                    aria-hidden="true"
                  />
                  <span class="co-nav-pill-label">{{ item.label }}</span>
                  <span
                    v-if="isNavLive(item.id)"
                    class="co-nav-live-dot"
                    :style="{ backgroundColor: navDotColor(item.id) }"
                    aria-hidden="true"
                  ></span>
                </button>
              </nav>
            </div>

            <div class="co-nav-right">
              <ColabOneTooltip
                :title="
                  accountStale
                    ? t('nav.refreshStaleTooltip')
                    : t('nav.refreshTooltip')
                "
                :disabled="!state.setup.colab || busy || !!state.operation"
                placement="bottom"
              >
                <button
                  type="button"
                  class="co-nav-circle-btn"
                  :class="{ 'is-spinning': refreshingAccount }"
                  :aria-label="t('nav.refreshAria')"
                  :disabled="!state.setup.colab || busy || !!state.operation"
                  @click="refreshAccount"
                >
                  <PhArrowsClockwise :size="17" />
                </button>
              </ColabOneTooltip>

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
                  class="co-nav-circle-btn"
                  :aria-label="
                    colabOneEffectiveAppearance === 'dark'
                      ? t('nav.switchLight')
                      : t('nav.switchDark')
                  "
                  @click="toggleAppearance($event)"
                >
                  <PhSun v-if="colabOneEffectiveAppearance === 'dark'" :size="17" />
                  <PhMoon v-else :size="17" />
                </button>
              </ColabOneTooltip>

              <LanguageSwitcher mode="topbar" @changed="onLocaleChanged" />

              <ColabOneTooltip
                :title="
                  logsOpen ? t('nav.logsOpenTooltip') : t('nav.logsClosedTooltip')
                "
                placement="bottom"
              >
                <button
                  type="button"
                  class="co-logs-trigger"
                  :class="{ 'is-active': logsOpen }"
                  :aria-label="
                    logsOpen ? t('nav.logsCollapseAria') : t('nav.logsExpandAria')
                  "
                  :aria-expanded="logsOpen"
                  aria-controls="activity-panel"
                  @click="setLogsOpen(!logsOpen)"
                >
                  <PhTerminalWindow :size="16" aria-hidden="true" />
                  <span class="co-logs-trigger-label">{{ t("nav.logs") }}</span>
                </button>
              </ColabOneTooltip>
            </div>
          </div>
        </header>

        <!-- Slim Runtime & Billing Status Sub-bar -->
        <div class="co-status-bar">
          <div class="co-status-bar-inner">
            <div class="co-topbar-left">
              <div
                v-if="!snapshotLoaded"
                class="co-topbar-sk"
                role="status"
                aria-busy="true"
              >
                <span class="co-sr-only">{{ t("common.loading") }}</span>
                <ColabOneSkeleton :width="68" :height="22" variant="pill" inline />
                <ColabOneSkeleton
                  :width="92"
                  :height="14"
                  variant="text"
                  tone="subtle"
                  :delay="70"
                  inline
                />
              </div>

              <template v-else>
                <ATag :color="runtimeTagColor" class="co-runtime-tag co-sk-reveal">
                  <ABadge :status="runtimeBadgeStatus" />
                  <span>{{
                    ipcDisconnected
                      ? t("status.ipcReconnecting")
                      : COLABONE_STATUS_LABELS[state.runtime.state] || state.runtime.state
                  }}</span>
                </ATag>

                <span class="co-gpu-name co-connection-gpu co-sk-reveal">
                  {{
                    state.runtime.state === "stopped"
                      ? t("status.preselectedGpu", { gpu: form.gpu })
                      : tb(state.runtime.gpu?.name) || form.gpu
                  }}
                </span>
              </template>

              <ColabOneTooltip
                v-if="
                  activeLocation?.country && state.runtime.state !== 'stopped'
                "
                :title="locationTooltip(activeLocation)"
                placement="bottom"
              >
                <ATag class="co-location-tag co-location-chip">
                  <FlagIcon :code="activeLocation.country" :size="14" />
                  <span>{{ locationShort(activeLocation) }}</span>
                </ATag>
              </ColabOneTooltip>

              <ColabOneTooltip
                v-if="activeVramModel && page !== 'api'"
                :title="t('status.loadedModelChipTitle', { name: activeVramModel.name })"
                placement="bottom"
              >
                <AButton
                  size="small"
                  class="co-model-chip co-topbar-model-chip"
                  @click="selectPage('api')"
                >
                  <ABadge status="success" />
                  <span class="co-model-chip-text">{{
                    activeVramModel.name
                  }}</span>
                </AButton>
              </ColabOneTooltip>

              <ASelect
                v-if="managedInstances.length > 1"
                size="small"
                class="co-instance-select co-topbar-instance-select"
                :aria-label="t('status.activeInstanceAria')"
                :value="state.runtime.id || state.active_endpoint"
                :disabled="busy || !!state.operation"
                :dropdown-match-select-width="false"
                @change="(val) => switchInstance(String(val ?? ''))"
              >
                <ASelectOption
                  v-for="inst in managedInstances"
                  :key="inst.id"
                  :value="inst.id"
                >
                  {{ inst.accelerator || "GPU" }} ·
                  {{
                    inst.location?.country
                      ? `${locationShort(inst.location)} · `
                      : ""
                  }}{{ inst.id }}
                  {{ inst.loaded_model ? `(${inst.loaded_model})` : "" }}
                </ASelectOption>
              </ASelect>
            </div>

            <div class="co-topbar-right co-header-right">
              <BillingBar
                :account="state.account"
                :setup-colab="state.setup.colab"
                :runtime-state="state.runtime.state"
                :runtime-id="state.runtime.id"
                :idle="idle"
                :idle-remaining="idleRemaining"
                :idle-minutes="form.idle_minutes"
                :now-ms="clockNow"
                :loading="!snapshotLoaded"
                :refreshing="refreshingAccount"
                :has-error="hasSyncError"
                @touch="interaction()"
              />
            </div>
          </div>
        </div>

        <!-- Global Persistent System & Operation Banners -->
        <div
          v-if="showUpgradeBanner || persistentBannerError || state.operation"
          class="co-banners"
        >
          <div class="co-banners-inner">
            <AAlert
              v-if="showUpgradeBanner"
              :key="upgradeBanner.dismissKey"
              type="info"
              show-icon
              :closable="upgradeBanner.dismissible"
              :message="upgradeBanner.message"
              class="co-upgrade-banner"
              @close="dismissOptionalUpgrade"
            >
              <template #action>
                <ColabOneTooltip
                  :title="
                    upgradeBanner.disabledReason ||
                    (busy ? t('status.upgradeBlockedOperation') : '')
                  "
                  placement="bottomRight"
                >
                  <span
                    class="co-upgrade-action-wrap"
                    :class="{ 'is-disabled': !upgradeBanner.canUpgrade || busy }"
                  >
                    <AButton
                      type="primary"
                      size="small"
                      :disabled="!upgradeBanner.canUpgrade || busy"
                      @click="requestRuntimeUpgrade"
                    >
                      {{ t("status.upgradeAction") }}
                    </AButton>
                  </span>
                </ColabOneTooltip>
              </template>
            </AAlert>

            <AAlert
              v-if="persistentBannerError"
              type="error"
              show-icon
              closable
              :message="persistentBannerError"
              @close="dismissPersistentError"
            />

            <AAlert
              v-if="state.operation"
              type="info"
              :show-icon="false"
              class="co-op-banner"
            >
              <template #message>
                <span class="co-op-message" role="status">
                  <ASpin size="small" />
                  <span class="co-op-message-text co-tabular">{{
                    operationBannerTitle
                  }}</span>
                </span>
              </template>
              <template v-if="showStartOperationActions" #action>
                <ASpace :size="8" class="co-op-actions">
                  <ColabOneTooltip
                    v-if="showStartRetryButton"
                    :title="
                      canRetryStartNow
                        ? t('device.retryAllocationNowTooltip')
                        : t('device.retryAllocationRequestingTooltip')
                    "
                    placement="bottomRight"
                  >
                    <span
                      class="co-op-action-wrap"
                      :class="{ 'is-disabled': !canRetryStartNow }"
                    >
                      <AButton
                        size="small"
                        class="co-op-retry-btn"
                        :loading="retryingStart || !canRetryStartNow"
                        :disabled="!canRetryStartNow"
                        @click="retryStartNow"
                      >
                        <template
                          v-if="canRetryStartNow && !retryingStart"
                          #icon
                        >
                          <PhArrowsClockwise :size="13" />
                        </template>
                        {{
                          canRetryStartNow
                            ? t("device.retryNow")
                            : t("device.retryingNow")
                        }}
                      </AButton>
                    </span>
                  </ColabOneTooltip>
                  <ColabOneTooltip
                    v-if="showStartCancelButton"
                    :title="t('device.cancelAllocationTooltip')"
                    placement="bottomRight"
                  >
                    <AButton
                      size="small"
                      danger
                      class="co-op-cancel-btn"
                      :loading="isStartCancelling"
                      :disabled="!canCancelStart"
                      @click="cancelStart"
                    >
                      <template v-if="!isStartCancelling" #icon>
                        <PhStop :size="13" weight="fill" />
                      </template>
                      {{
                        isStartCancelling
                          ? t("device.cancellingAllocation")
                          : t("device.cancelAllocation")
                      }}
                    </AButton>
                  </ColabOneTooltip>
                </ASpace>
              </template>
            </AAlert>
          </div>
        </div>

        <!-- Scrollable Workspace Content Stage -->
        <div class="co-content-stage">
        <ALayoutContent ref="contentRef" class="co-content">
          <!-- STAGE 1: Remote Device -->
          <main
            v-if="page === 'device'"
            key="device"
            class="co-page-shell co-page-motion co-device-page"
          >
            <div class="co-page-inner">
              <div class="co-page-header">
                <div>
                  <h1 class="co-page-title">{{ t("device.title") }}</h1>
                  <p class="co-page-subtitle">
                    {{ t("device.subtitle") }}
                  </p>
                </div>
                <ATag color="blue" :bordered="false">Google Colab</ATag>
              </div>

              <!-- Unauthenticated Google Callout -->
              <ACard
                v-if="snapshotLoaded && !state.setup.colab"
                class="co-setup-callout co-sk-reveal"
              >
                <div class="co-callout-row">
                  <div class="co-callout-icon" aria-hidden="true">
                    <PhUserCircle :size="26" weight="duotone" />
                  </div>
                  <div class="co-callout-copy">
                    <h3>{{ t("device.connectTitle") }}</h3>
                    <p>{{ t("device.connectDesc") }}</p>
                  </div>
                  <ASpace :size="8" wrap>
                    <AButton
                      type="primary"
                      :disabled="busy || !!state.operation"
                      @click="beginLogin"
                    >
                      <template #icon>
                        <PhArrowSquareOut :size="15" />
                      </template>
                      {{ t("device.signInGoogle") }}
                    </AButton>
                    <AButton @click="selectPage('settings')">
                      <span>{{ t("device.moreSettings") }}</span>
                      <PhCaretRight :size="14" />
                    </AButton>
                  </ASpace>
                </div>
              </ACard>

              <!-- Hero Grid: GPU Control + Billing Overview (side-by-side on wide fullscreen) -->
              <div class="co-device-hero-grid">
                <!-- Primary GPU Control Card -->
                <ACard class="co-device-card co-device-panel">
                <template v-if="!snapshotLoaded">
                  <div
                    class="co-device-head co-device-title"
                    role="status"
                    aria-busy="true"
                  >
                    <span class="co-sr-only">{{ t("common.loading") }}</span>
                    <div class="co-device-icon" aria-hidden="true">
                      <PhHardDrives :size="26" weight="duotone" />
                    </div>
                    <div class="co-device-title-main co-device-sk-title">
                      <ColabOneSkeleton :width="176" :height="24" variant="text" />
                      <ColabOneSkeleton
                        width="72%"
                        :height="16"
                        variant="text"
                        tone="subtle"
                        :delay="60"
                      />
                    </div>
                    <div class="co-device-badges">
                      <ColabOneSkeleton
                        :width="64"
                        :height="22"
                        variant="pill"
                        tone="subtle"
                        :delay="95"
                      />
                    </div>
                  </div>

                  <ADivider class="co-card-divider" />

                  <div class="co-device-controls" aria-hidden="true">
                    <div class="co-device-fields">
                      <div class="co-control-field">
                        <span class="co-field-label">{{ t("device.gpuModelLabel") }}</span>
                        <ColabOneSkeleton
                          :width="300"
                          :height="32"
                          :radius="8"
                          variant="rect"
                          :delay="75"
                        />
                      </div>
                      <div class="co-control-field">
                        <span class="co-field-label">{{ t("device.idleReleaseLabel") }}</span>
                        <ColabOneSkeleton
                          :width="172"
                          :height="32"
                          :radius="8"
                          variant="rect"
                          tone="subtle"
                          :delay="120"
                        />
                      </div>
                    </div>
                    <div class="co-device-actions">
                      <ColabOneSkeleton
                        :width="112"
                        :height="32"
                        variant="button"
                        :delay="150"
                      />
                    </div>
                  </div>

                  <div class="co-device-meta-strips" aria-hidden="true">
                    <div class="co-device-policy co-device-sk-policy">
                      <ColabOneSkeleton
                        :width="210"
                        :height="14"
                        variant="text"
                        tone="subtle"
                        :delay="175"
                      />
                      <ColabOneSkeleton
                        :width="280"
                        :height="12"
                        variant="text"
                        tone="subtle"
                        :delay="215"
                      />
                    </div>
                  </div>
                </template>

                <template v-else>
                <div class="co-device-head co-device-title co-sk-reveal">
                  <div class="co-device-icon" aria-hidden="true">
                    <PhHardDrives :size="26" weight="duotone" />
                  </div>
                  <div class="co-device-title-main">
                    <h2>{{ tb(state.runtime.gpu?.name) || t("device.fallbackGpuName") }}</h2>
                    <div class="co-device-subtitle">
                      <template v-if="ready && activeVramModel">
                        <code v-if="state.runtime.id" class="co-runtime-id">{{
                          state.runtime.id
                        }}</code>
                        <span>{{ t("device.loadedModel", { name: activeVramModel.name }) }}</span>
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link"
                          @click="selectPage('api')"
                        >
                          {{ t("device.goToApi") }}
                        </AButton>
                      </template>
                      <template v-else-if="ready && state.runtime.comfyui?.running">
                        <code v-if="state.runtime.id" class="co-runtime-id">{{
                          state.runtime.id
                        }}</code>
                        <span>{{ t("device.comfyRunning") }}</span>
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link"
                          @click="selectPage('comfyui')"
                        >
                          {{ t("device.goToComfy") }}
                        </AButton>
                      </template>
                      <template v-else-if="ready">
                        <code v-if="state.runtime.id" class="co-runtime-id">{{
                          state.runtime.id
                        }}</code>
                        <span>{{ t("device.readyNoModel") }}</span>
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link"
                          @click="openModels"
                        >
                          {{ t("device.chooseModel") }}
                        </AButton>
                      </template>
                      <template v-else-if="state.runtime.state === 'starting'">
                        <span>{{ t("device.startingEnv", { gpu: form.gpu }) }}</span>
                      </template>
                      <template v-else-if="state.runtime.id">
                        <code class="co-runtime-id">{{
                          state.runtime.id
                        }}</code>
                      </template>
                      <template v-else>
                        <span>{{ t("device.standbyHint") }}</span>
                      </template>
                    </div>
                  </div>
                  <div class="co-device-badges co-device-badges">
                    <ColabOneTooltip
                      v-if="
                        activeLocation?.country &&
                        state.runtime.state !== 'stopped'
                      "
                      :title="locationTooltip(activeLocation)"
                    >
                      <ATag class="co-location-tag">
                        <FlagIcon :code="activeLocation.country" :size="14" />
                        <span>{{ locationFull(activeLocation) }}</span>
                      </ATag>
                    </ColabOneTooltip>
                    <ATag
                      v-if="ready && state.setup.drive"
                      color="success"
                      :bordered="false"
                    >
                      <PhCloud :size="13" />
                      <span>{{ t("device.driveMounted") }}</span>
                    </ATag>
                    <ATag :color="ready ? 'success' : 'default'">
                      {{ COLABONE_STATUS_LABELS[state.runtime.state] || state.runtime.state }}
                    </ATag>
                  </div>
                </div>

                <ADivider class="co-card-divider" />

                <div class="co-device-controls co-device-controls">
                  <div class="co-device-fields">
                    <div class="co-control-field">
                      <span class="co-field-label">{{ t("device.gpuModelLabel") }}</span>
                      <ASelect
                        :value="form.gpu"
                        :aria-label="t('device.gpuModelAria')"
                        class="co-select-gpu"
                        :dropdown-match-select-width="false"
                        :disabled="busy || !!state.operation"
                        @change="onGpuSelect"
                      >
                        <ASelectOption
                          v-for="g in GPU_SPECS"
                          :key="g.id"
                          :value="g.id"
                        >
                          {{ g.label }}
                        </ASelectOption>
                      </ASelect>
                    </div>

                    <div class="co-control-field">
                      <span class="co-field-label">{{ t("device.idleReleaseLabel") }}</span>
                      <ASelect
                        :value="form.idle_minutes"
                        :aria-label="t('device.idleReleaseAria')"
                        class="co-select-idle"
                        :dropdown-match-select-width="false"
                        :disabled="savingIdle"
                        @change="changeIdleMinutes"
                      >
                        <ASelectOption
                          v-if="!isPresetIdleMinutes(form.idle_minutes)"
                          :value="form.idle_minutes"
                        >
                          {{ formatIdlePolicyLabel(form.idle_minutes) }}
                        </ASelectOption>
                        <ASelectOption
                          v-for="opt in COLABONE_IDLE_RELEASE_OPTIONS"
                          :key="opt.value"
                          :value="opt.value"
                        >
                          {{ opt.label }}
                        </ASelectOption>
                      </ASelect>
                    </div>
                  </div>

                  <div class="co-device-actions co-device-action-group">
                    <template
                      v-if="
                        isStartOperationActive &&
                        state.runtime.state === 'starting' &&
                        showStartOperationActions
                      "
                    >
                      <ColabOneTooltip
                        v-if="showStartRetryButton"
                        :title="
                          canRetryStartNow
                            ? t('device.retryAllocationNowTooltip')
                            : t('device.retryAllocationRequestingTooltip')
                        "
                      >
                        <span
                          class="co-op-action-wrap"
                          :class="{ 'is-disabled': !canRetryStartNow }"
                        >
                          <AButton
                            class="co-device-retry-btn"
                            :loading="retryingStart || !canRetryStartNow"
                            :disabled="!canRetryStartNow"
                            @click="retryStartNow"
                          >
                            <template
                              v-if="canRetryStartNow && !retryingStart"
                              #icon
                            >
                              <PhArrowsClockwise :size="14" />
                            </template>
                            {{
                              canRetryStartNow
                                ? t("device.retryNow")
                                : t("device.retryingNow")
                            }}
                          </AButton>
                        </span>
                      </ColabOneTooltip>
                      <ColabOneTooltip
                        v-if="showStartCancelButton"
                        :title="t('device.cancelAllocationTooltip')"
                      >
                        <AButton
                          type="primary"
                          danger
                          class="co-device-cancel-btn"
                          :loading="isStartCancelling"
                          :disabled="!canCancelStart"
                          @click="cancelStart"
                        >
                          <template v-if="!isStartCancelling" #icon>
                            <PhStop :size="14" weight="fill" />
                          </template>
                          {{
                            isStartCancelling
                              ? t("device.cancellingAllocation")
                              : t("device.cancelAllocation")
                          }}
                        </AButton>
                      </ColabOneTooltip>
                    </template>
                    <template v-else>
                    <ColabOneTooltip
                      v-if="state.runtime.state === 'stopped'"
                      :title="
                        !state.setup.colab
                          ? t('device.signInFirstTooltip')
                          : state.operation?.title
                            ? tb(state.operation.title)
                            : ''
                      "
                    >
                      <AButton
                        type="primary"
                        :disabled="
                          busy || !!state.operation || !state.setup.colab
                        "
                        @click="start"
                      >
                        <template #icon>
                          <PhPlay :size="14" weight="fill" />
                        </template>
                        {{ t("device.startGpu") }}
                      </AButton>
                    </ColabOneTooltip>
                    <AButton
                      v-else
                      type="primary"
                      :disabled="
                        busy || !!state.operation || !state.setup.colab
                      "
                      @click="startNew"
                    >
                      <template #icon>
                        <PhPlus :size="14" weight="bold" />
                      </template>
                      {{ t("device.addInstance") }}
                    </AButton>

                    <AButton
                      v-if="
                        state.runtime.state !== 'stopped' &&
                        state.runtime.id &&
                        instanceCount > 1
                      "
                      danger
                      :disabled="busy || !!state.operation"
                      @click="openRelease(state.runtime.id)"
                    >
                      <template #icon>
                        <PhStop :size="14" weight="fill" />
                      </template>
                      {{ t("device.stopCurrentOnly") }}
                    </AButton>

                    <AButton
                      v-if="
                        instanceCount > 0 ||
                        state.runtime.state !== 'stopped' ||
                        billing
                      "
                      type="primary"
                      danger
                      class="co-stop-all"
                      :disabled="busy || !!state.operation"
                      @click="openRelease()"
                    >
                      <template #icon>
                        <PhStop :size="14" weight="fill" />
                      </template>
                      {{
                        instanceCount > 1
                          ? t("device.stopAllCount", { count: instanceCount })
                          : instanceCount === 1
                            ? t("device.stopInstance")
                            : t("device.stopRemoteInstance")
                      }}
                    </AButton>
                    </template>
                  </div>
                </div>

                <!-- Idle Policy & Drive Status Strips -->
                <div class="co-device-meta-strips">
                  <div class="co-device-policy co-device-policy">
                    <template v-if="ready && idle">
                      <div
                        v-if="idle.phase === 'active'"
                        class="co-policy-live is-active"
                      >
                        <ABadge status="processing" />
                        <span>{{ t("device.idlePaused") }}</span>
                      </div>
                      <div
                        v-else
                        class="co-policy-live"
                        :class="{ 'is-urgent': countdown }"
                      >
                        <span>
                          {{ t("device.idleCountdownRemaining") }}
                          <b class="co-tabular">{{
                            formatCountdownClock(idleRemaining)
                          }}</b>
                          {{ t("device.idleCountdownSuffix") }}
                        </span>
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link"
                          @click="interaction()"
                        >
                          {{ t("device.idleReset") }}
                        </AButton>
                      </div>
                      <span class="co-policy-hint">
                        {{ t("device.idlePauseHint") }}
                      </span>
                    </template>
                    <template v-else>
                      <div class="co-policy-live">
                        {{
                          t("device.idlePolicySummary", {
                            policy: formatIdlePolicyLabel(form.idle_minutes),
                          })
                        }}
                      </div>
                      <span class="co-policy-hint">
                        {{ t("device.idlePolicyHint") }}
                      </span>
                    </template>
                  </div>

                  <!-- Drive Sync Bar inside Device Card when Ready -->
                  <div
                    v-if="ready && (state.setup.drive || state.setup.drive_authorized || state.setup.colab)"
                    class="co-drive-strip co-sync-note"
                  >
                    <PhCloud :size="16" class="co-drive-icon" />
                    <span
                      class="co-drive-text"
                      :class="{
                        'is-warning':
                          !!state.drive_auth_hint &&
                          !!state.auth_url &&
                          !!state.operation &&
                          !state.setup.drive,
                      }"
                    >
                      <template v-if="state.auth_url && state.operation && !state.setup.drive">
                        {{
                          state.drive_auth_hint
                            ? tb(state.drive_auth_hint)
                            : t("device.driveWaitingBrowserConfirm")
                        }}
                      </template>
                      <template v-else-if="state.setup.drive">
                        {{
                          state.runtime.drive?.error
                            ? tb(state.runtime.drive.error)
                            : t("device.driveMountedFull", {
                                folder:
                                  state.runtime.drive?.remote_folder ||
                                  "ColabOne_Drive",
                                free: state.runtime.drive?.free_bytes
                                  ? t("device.driveFreeSegment", {
                                      size: bytes(state.runtime.drive.free_bytes),
                                    })
                                  : "",
                                tail: state.runtime.drive?.pending
                                  ? t("device.drivePendingSync", {
                                      count: state.runtime.drive.pending,
                                    })
                                  : t("device.driveAutoPersist"),
                              })
                        }}
                      </template>
                      <template v-else-if="state.runtime.drive?.error">
                        {{ tb(state.runtime.drive.error) }}
                      </template>
                      <template v-else-if="state.setup.drive_authorized">
                        {{ t("device.driveAuthorizedHint") }}
                      </template>
                      <template v-else>
                        {{ t("device.driveNeedAuthHint") }}
                      </template>
                    </span>
                    <template v-if="state.auth_url && state.operation && !state.setup.drive">
                      <ASpace :size="6" wrap>
                        <AButton
                          size="small"
                          type="primary"
                          :loading="confirmingDriveAuth"
                          @click="confirmDriveAuth()"
                        >
                          {{ t("device.driveConfirmAuthorized") }}
                        </AButton>
                        <AButton
                          size="small"
                          @click="openExternalURL(state.auth_url)"
                        >
                          {{ t("device.driveReopenAuth") }}
                        </AButton>
                        <AButton
                          size="small"
                          :disabled="busy"
                          @click="act('drive.cancel')"
                        >
                          {{ t("device.driveCancelMount") }}
                        </AButton>
                      </ASpace>
                    </template>
                    <ASpace v-else :size="6" wrap>
                      <AButton
                        v-if="state.setup.drive_authorized && !state.setup.drive_headless"
                        size="small"
                        type="link"
                        class="co-drive-headless-link"
                        @click="selectPage('settings')"
                      >
                        {{ t("device.driveEnableHeadlessLink") }}
                      </AButton>
                      <AButton
                        size="small"
                        :disabled="busy || !!state.operation"
                        @click="act(state.setup.drive ? 'drive.sync' : 'drive.connect')"
                      >
                        {{
                          state.setup.drive
                            ? t("device.syncNow")
                            : t("device.mountDriveNow")
                        }}
                      </AButton>
                    </ASpace>
                  </div>
                </div>
                </template>
              </ACard>

              <!-- Billing Card -->
              <section
                class="co-billing-card-wrap"
                :aria-label="t('device.billingOverviewAria')"
              >
                <BillingCard
                  :account="state.account"
                  :setup-colab="state.setup.colab"
                  :runtime-state="state.runtime.state"
                  :runtime-id="state.runtime.id"
                  :idle="idle"
                  :idle-remaining="idleRemaining"
                  :idle-minutes="form.idle_minutes"
                  :busy="busy || !!state.operation || savingIdle"
                  :loading="!snapshotLoaded"
                  :refreshing="refreshingAccount"
                  :has-error="hasSyncError"
                  :now-ms="clockNow"
                  :show-idle-footer="false"
                  @refresh-account="refreshAccount"
                  @touch="interaction()"
                  @change-idle-minutes="changeIdleMinutes"
                  @open-settings="
                    !busy && !state.operation
                      ? beginLogin()
                      : selectPage('settings')
                  "
                />
              </section>
              </div>

              <!-- Real-time Hardware Resource Monitor -->
              <section
                class="co-resource-monitor-wrap"
                :aria-label="t('device.resourceMonitorAria')"
              >
                <ResourceMonitor
                  class="co-monitor"
                  :runtime="effectiveMonitor.runtime"
                  :drive-connected="effectiveMonitor.drive_connected"
                  :drive-free-bytes="effectiveMonitor.drive_free_bytes"
                  :loaded-model-name="effectiveMonitor.loaded_model_name"
                  :selected-gpu="form.gpu"
                  :operation-title="operationBannerTitle"
                  :ipc-disconnected="ipcDisconnected"
                  :loading="!snapshotLoaded"
                  :switching="monitorSwitching"
                  :monitor-options="effectiveMonitor.options"
                  :monitor-endpoint="effectiveMonitor.endpoint"
                  :monitor-source="effectiveMonitor.source"
                  :monitor-paused="effectiveMonitor.paused"
                  :monitor-error="effectiveMonitor.error"
                  :monitor-busy="monitorBusy"
                  @select-instance="selectMonitorInstance"
                  @toggle-pause="toggleMonitorPause"
                  @retry-monitor="retryMonitor"
                />
              </section>

              <!-- Multi-instance Management Card -->
              <section
                v-if="accountInstances.length || showInstancesSkeleton"
                class="co-instances-section"
                :aria-label="t('device.instancesAria')"
              >
                <ACard class="co-instances-card">
                  <template #title>
                    <div class="co-card-title-block">
                      <span>{{
                        t("device.instancesHeading", {
                          count:
                            accountInstances.length ||
                            state.account.assignments ||
                            0,
                        })
                      }}</span>
                      <ColabOneSkeleton
                        v-if="showInstancesSkeleton"
                        :width="148"
                        :height="13"
                        variant="text"
                        tone="subtle"
                      />
                      <span v-else class="co-card-title-sub">
                        {{
                          t("device.instancesBreakdown", {
                            managed: managedInstances.length,
                            external: externalInstances.length,
                          })
                        }}
                      </span>
                    </div>
                  </template>
                  <template #extra>
                    <AButton
                      size="small"
                      :loading="refreshingAccount"
                      :disabled="
                        busy || !!state.operation || !state.setup.colab
                      "
                      @click="refreshAccount"
                    >
                      <template #icon>
                        <PhArrowsClockwise :size="14" />
                      </template>
                      {{ t("device.refreshList") }}
                    </AButton>
                  </template>

                  <div
                    v-if="showInstancesSkeleton"
                    class="co-instance-list"
                    role="status"
                    aria-busy="true"
                  >
                    <span class="co-sr-only">{{ t("common.loading") }}</span>
                    <article
                      v-for="idx in instanceSkeletonCount"
                      :key="idx"
                      class="co-instance-row co-instance-card co-instance-skeleton"
                    >
                      <div class="co-instance-main">
                        <div class="co-instance-tags">
                          <ColabOneSkeleton
                            :width="62"
                            :height="22"
                            variant="tag"
                            :delay="(idx - 1) * 70"
                          />
                          <ColabOneSkeleton
                            :width="44"
                            :height="22"
                            variant="tag"
                            tone="subtle"
                            :delay="(idx - 1) * 70 + 35"
                          />
                          <ColabOneSkeleton
                            :width="84"
                            :height="22"
                            variant="tag"
                            tone="subtle"
                            :delay="(idx - 1) * 70 + 70"
                          />
                        </div>
                        <ColabOneSkeleton
                          :width="178"
                          :height="15"
                          variant="text"
                          :delay="(idx - 1) * 70 + 90"
                        />
                        <ColabOneSkeleton
                          width="76%"
                          :height="13"
                          variant="text"
                          tone="subtle"
                          :delay="(idx - 1) * 70 + 125"
                        />
                      </div>
                      <div class="co-instance-actions">
                        <ColabOneSkeleton
                          :width="72"
                          :height="24"
                          variant="button"
                          :radius="6"
                          tone="subtle"
                          :delay="(idx - 1) * 70 + 105"
                        />
                        <ColabOneSkeleton
                          :width="64"
                          :height="24"
                          variant="button"
                          :radius="6"
                          tone="subtle"
                          :delay="(idx - 1) * 70 + 140"
                        />
                      </div>
                    </article>
                  </div>

                  <div v-else class="co-instance-list co-sk-reveal">
                    <article
                      v-for="inst in accountInstances"
                      :key="inst.id"
                      class="co-instance-row co-instance-card"
                      :class="{ active: inst.active && inst.managed }"
                    >
                      <div class="co-instance-main">
                        <div class="co-instance-tags">
                          <ATag
                            v-if="inst.active && inst.managed"
                            color="success"
                          >
                            {{ t("device.tagCurrent") }}
                          </ATag>
                          <ATag v-else-if="inst.managed" color="blue">
                            {{ t("device.tagManaged") }}
                          </ATag>
                          <ATag v-else>{{ t("device.tagExternal") }}</ATag>
                          <ATag v-if="inst.accelerator">{{
                            inst.accelerator
                          }}</ATag>
                          <ATag v-if="shapeLabel(inst.shape)">{{
                            shapeLabel(inst.shape)
                          }}</ATag>
                          <ColabOneTooltip
                            v-if="inst.location?.country"
                            :title="locationTooltip(inst.location)"
                          >
                            <ATag class="co-location-tag">
                              <FlagIcon
                                :code="inst.location.country"
                                :size="13"
                              />
                              <span>{{ locationFull(inst.location) }}</span>
                            </ATag>
                          </ColabOneTooltip>
                          <ATag
                            v-if="inst.drive_connected"
                            color="success"
                            :bordered="false"
                          >
                            {{ t("device.driveMounted") }}
                          </ATag>
                          <ATag
                            v-if="
                              !inst.managed &&
                              effectiveMonitor.endpoint === inst.id
                            "
                            :color="
                              effectiveMonitor.paused ? 'default' : 'processing'
                            "
                            :bordered="false"
                          >
                            {{
                              effectiveMonitor.paused
                                ? t("device.tagMonitoringPaused")
                                : t("device.tagMonitoring")
                            }}
                          </ATag>
                        </div>

                        <code class="co-instance-endpoint">{{ inst.id }}</code>

                        <p v-if="inst.managed" class="co-instance-detail">
                          <span>
                            {{ COLABONE_STATUS_LABELS[inst.state || "ready"] || inst.state }}
                            <template v-if="inst.gpu_name">
                              · {{ tb(inst.gpu_name) }}
                            </template>
                          </span>
                          <span v-if="inst.vram_total_mib">
                            {{
                              t("device.vramSegment", {
                                used: ((inst.vram_used_mib || 0) / 1024).toFixed(1),
                                total: `${(inst.vram_total_mib / 1024).toFixed(0)} GB`,
                              })
                            }}
                          </span>
                          <span
                            v-else-if="inst.state === 'starting'"
                            class="co-instance-detail-sk"
                          >
                            <span aria-hidden="true">·</span>
                            <ColabOneSkeleton
                              :width="72"
                              :height="12"
                              variant="text"
                              tone="subtle"
                              inline
                            />
                          </span>
                          <span v-if="inst.disk_free">
                            {{
                              t("device.diskFreeSegment", {
                                size: bytes(inst.disk_free),
                              })
                            }}
                          </span>
                          <span>
                            ·
                            {{
                              inst.loaded_model
                                ? t("device.instLoadedModel", {
                                    model: inst.loaded_model,
                                  })
                                : t("device.instNoModel")
                            }}
                          </span>
                          <span v-if="taskKindLabel(inst.active_task)">
                            · {{ taskKindLabel(inst.active_task) }}
                          </span>
                        </p>
                        <p v-else class="co-instance-detail">
                          {{ t("device.instExternalHint") }}
                        </p>
                      </div>

                      <div class="co-instance-actions">
                        <AButton
                          v-if="effectiveMonitor.endpoint !== inst.id"
                          size="small"
                          :disabled="monitorBusy || ipcDisconnected"
                          @click="selectMonitorInstance(inst.id)"
                        >
                          {{ t("device.viewMonitor") }}
                        </AButton>
                        <AButton
                          v-if="inst.managed && !inst.active"
                          size="small"
                          :disabled="busy || !!state.operation"
                          @click="switchInstance(inst.id)"
                        >
                          {{ t("device.switchUse") }}
                        </AButton>
                        <AButton
                          danger
                          size="small"
                          :disabled="busy || !!state.operation"
                          @click="openRelease(inst.id)"
                        >
                          {{ t("device.stopSingle") }}
                        </AButton>
                      </div>
                    </article>
                  </div>
                </ACard>
              </section>

              <p class="co-footnote">
                {{ t("device.footerBase") }}
                {{
                  state.setup.drive
                    ? t("device.footerDriveMounted")
                    : form.auto_drive && state.setup.drive_authorized
                      ? ready
                        ? t("device.footerDriveUnmountedActive")
                        : t("device.footerDriveAuto")
                      : t("device.footerDriveOff")
                }}
              </p>
            </div>
          </main>

          <!-- STAGE 2: 对外 API -->
          <main
            v-else-if="page === 'api'"
            key="api"
            class="co-panel-wrap co-page-motion co-panel-host"
          >
            <ApiPanel
              v-model:models-open="modelsDrawerOpen"
              :state="state"
              :busy="busy"
              :loading="!snapshotLoaded"
              @device="openDevice"
              @comfyui="selectPage('comfyui')"
              @sync="onPanelSync"
              @notify="notify"
              @error="showError"
            />
          </main>

          <!-- STAGE 3: ComfyUI -->
          <main
            v-else-if="page === 'comfyui'"
            key="comfyui"
            class="co-panel-wrap co-page-motion co-panel-host"
          >
            <ComfyPanel
              :state="state"
              :loading="!snapshotLoaded"
              @device="openDevice"
              @sync="onPanelSync"
              @notify="notify"
              @clear-error="clearActionErrors"
              @error="showError"
            />
          </main>

          <!-- STAGE 4: Settings -->
          <main
            v-else
            key="settings"
            class="co-page-shell co-page-motion co-settings-page"
          >
            <div class="co-page-inner co-settings-inner">
              <div class="co-page-header">
                <div>
                  <h1 class="co-page-title">{{ t("settings.title") }}</h1>
                  <p class="co-page-subtitle">
                    {{ t("settings.subtitle") }}
                  </p>
                </div>
                <div
                  ref="settingsTopSaveSlotRef"
                  class="co-settings-top-save-slot"
                  :style="
                    showFloatingSettingsSave && settingsTopSaveWidthPx
                      ? { minWidth: `${settingsTopSaveWidthPx}px` }
                      : undefined
                  "
                >
                  <AButton
                    v-if="!showFloatingSettingsSave"
                    type="primary"
                    :disabled="busy"
                    @click="save"
                  >
                    {{ t("settings.saveSettings") }}
                  </AButton>
                </div>
              </div>

              <!-- Settings Native Single-Column Compact Grouped Rows -->
              <div class="co-settings-stack">
                <!-- Section 1: Accounts & Credentials -->
                <section
                  class="co-settings-group-block"
                  aria-labelledby="settings-sec-account"
                >
                  <h2 id="settings-sec-account" class="co-settings-section-label">
                    {{ t("settings.sectionAccount") }}
                  </h2>
                  <div class="co-settings-list">
                    <!-- Row 1.1: Google Account -->
                    <div class="co-settings-row co-settings-section">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-icon is-blue" aria-hidden="true">
                              <PhUserCircle :size="14" weight="duotone" />
                            </span>
                            <h3 class="co-sr-title">{{ t("settings.googleCardTitle") }}</h3>
                            <ColabOneSkeleton
                              v-if="!snapshotLoaded"
                              :width="52"
                              :height="20"
                              variant="tag"
                              inline
                            />
                            <ATag v-else-if="state.setup.colab" color="success">
                              {{ t("settings.connected") }}
                            </ATag>
                            <ATag v-else>{{ t("settings.disconnected") }}</ATag>
                          </div>

                          <p class="co-sr-desc">
                            <span
                              v-if="settingsEmailLoading"
                              class="co-settings-email-sk"
                              role="status"
                              aria-busy="true"
                            >
                              <ColabOneSkeleton :width="184" :height="13" variant="text" inline />
                              <span class="co-sr-only">{{
                                snapshotLoaded
                                  ? t("billing.statusSyncingAccount")
                                  : t("common.loading")
                              }}</span>
                            </span>
                            <span v-else class="co-sk-reveal">
                              {{
                                state.account.email ||
                                (state.setup.colab
                                  ? t("settings.accountConnectedNoEmail")
                                  : t("settings.oauthBrowserHint"))
                              }}
                            </span>
                          </p>

                          <p v-if="state.setup.colab" class="co-saved-login co-sr-saved-note">
                            <PhCheck :size="13" />
                            <span>{{
                              state.setup.persistent_login
                                ? t("settings.loginSavedAutoRenew")
                                : t("settings.loginSavedHint")
                            }}</span>
                          </p>
                        </div>

                        <div class="co-sr-control">
                          <div v-if="!snapshotLoaded" class="co-settings-btn-sk" aria-hidden="true">
                            <ColabOneSkeleton :width="96" :height="30" variant="button" :delay="40" />
                          </div>
                          <ASpace v-else :size="8" wrap>
                            <AButton
                              v-if="state.auth_url && state.operation && !ready"
                              type="link"
                              size="small"
                              @click="openExternalURL(state.auth_url)"
                            >
                              {{ t("settings.reopenLoginPage") }}
                            </AButton>
                            <AButton
                              :type="state.setup.colab ? 'default' : 'primary'"
                              :disabled="
                                busy ||
                                !!state.operation ||
                                state.runtime.state !== 'stopped'
                              "
                              @click="beginLogin"
                            >
                              <template #icon>
                                <PhArrowSquareOut :size="15" />
                              </template>
                              {{
                                state.setup.colab
                                  ? t("settings.relogin")
                                  : t("device.signInGoogle")
                              }}
                            </AButton>
                          </ASpace>
                        </div>
                      </div>

                      <ACollapse
                        :active-key="showAuthPaste ? ['paste'] : []"
                        ghost
                        class="co-inline-collapse co-sr-collapse"
                        @update:active-key="onAuthPasteToggle"
                      >
                        <ACollapsePanel
                          key="paste"
                          :header="t('settings.manualAuthHeader')"
                        >
                          <div class="co-collapse-body">
                            <p class="co-muted">
                              {{ t("settings.manualAuthDesc") }}
                            </p>
                            <ASpace :size="8" wrap>
                              <AButton
                                :disabled="busy || state.runtime.state !== 'stopped'"
                                @click="pasteLogin"
                              >
                                <template #icon>
                                  <PhCopy :size="15" />
                                </template>
                                {{ t("settings.pasteAndLogin") }}
                              </AButton>
                              <AButton
                                type="link"
                                :disabled="
                                  busy ||
                                  !!state.operation ||
                                  state.runtime.state !== 'stopped'
                                "
                                @click="manualLogin"
                              >
                                <template #icon>
                                  <PhArrowSquareOut :size="14" />
                                </template>
                                {{ t("settings.switchManualAuth") }}
                              </AButton>
                            </ASpace>
                            <ATextarea
                              v-model:value="code"
                              :rows="3"
                              autocomplete="off"
                              autocapitalize="off"
                              spellcheck="false"
                              :placeholder="t('settings.authInputPlaceholder')"
                              :aria-label="t('settings.authInputAria')"
                            />
                            <div>
                              <AButton
                                type="primary"
                                :disabled="
                                  !code.trim() ||
                                  busy ||
                                  state.runtime.state !== 'stopped'
                                "
                                @click="finishLogin"
                              >
                                {{ t("settings.autoParseAndLogin") }}
                              </AButton>
                            </div>
                          </div>
                        </ACollapsePanel>
                      </ACollapse>
                    </div>

                    <!-- Row 1.2: Google Drive -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-icon is-emerald" aria-hidden="true">
                              <PhCloud :size="14" weight="duotone" />
                            </span>
                            <h3 class="co-sr-title">{{ t("settings.driveHeading") }}</h3>
                            <ColabOneSkeleton
                              v-if="!snapshotLoaded"
                              :width="56"
                              :height="20"
                              variant="tag"
                              tone="subtle"
                              :delay="60"
                              inline
                            />
                            <ATag
                              v-else
                              :color="
                                state.setup.drive ||
                                state.setup.drive_headless ||
                                state.setup.drive_authorized
                                  ? 'success'
                                  : 'default'
                              "
                            >
                              {{
                                state.setup.drive
                                  ? t("settings.driveTagMounted")
                                  : state.setup.drive_headless
                                    ? t("settings.driveTagHeadlessReady")
                                    : state.setup.drive_authorized
                                      ? t("settings.driveTagAuthorized")
                                      : state.setup.colab
                                        ? t("settings.driveTagReauth")
                                        : t("settings.disconnected")
                              }}
                            </ATag>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.driveDescPrefix") }}
                            <code>My Drive / ColabOne_Drive</code>{{ t("settings.driveDescSuffix") }}
                          </p>
                        </div>

                        <div class="co-sr-control">
                          <div v-if="!snapshotLoaded" class="co-settings-btn-sk" aria-hidden="true">
                            <ColabOneSkeleton :width="108" :height="30" variant="button" tone="subtle" :delay="90" />
                          </div>
                          <ASpace v-else :size="8" wrap>
                            <AButton
                              :disabled="
                                busy ||
                                !ready ||
                                !!state.operation ||
                                !state.setup.colab
                              "
                              @click="
                                act(state.setup.drive ? 'drive.sync' : 'drive.connect')
                              "
                            >
                              <template #icon>
                                <PhCloud :size="15" />
                              </template>
                              {{
                                state.setup.drive
                                  ? t("settings.driveSyncNow")
                                  : t("settings.driveMountNow")
                              }}
                            </AButton>
                            <AButton
                              v-if="ready && state.auth_url && state.operation && !state.setup.drive"
                              type="primary"
                              :loading="confirmingDriveAuth"
                              @click="confirmDriveAuth()"
                            >
                              {{ t("device.driveConfirmAuthorized") }}
                            </AButton>
                            <AButton
                              v-if="ready && state.auth_url && state.operation && !state.setup.drive"
                              @click="openExternalURL(state.auth_url)"
                            >
                              {{ t("device.driveReopenAuth") }}
                            </AButton>
                            <AButton
                              v-if="ready && state.auth_url && state.operation && !state.setup.drive"
                              :disabled="busy"
                              @click="act('drive.cancel')"
                            >
                              {{ t("device.driveCancelMount") }}
                            </AButton>
                          </ASpace>
                        </div>
                      </div>

                      <AAlert
                        v-if="state.setup.colab && !state.setup.drive_authorized"
                        type="warning"
                        show-icon
                        :message="t('settings.driveNeedScopeAlert')"
                      />

                      <div
                        v-else-if="
                          snapshotLoaded &&
                          state.setup.colab &&
                          state.setup.drive_authorized &&
                          !state.setup.drive_headless
                        "
                        class="co-sr-callout"
                      >
                        <div class="co-sr-callout-copy">
                          <strong class="co-sr-callout-title">{{
                            t("settings.driveHeadlessTipTitle")
                          }}</strong>
                          <p class="co-sr-callout-desc">
                            {{ t("settings.driveHeadlessTipDesc") }}
                          </p>
                        </div>
                        <ASpace :size="8" wrap class="co-sr-callout-actions">
                          <AButton
                            size="small"
                            @click="openExternalURL('https://console.cloud.google.com/')"
                          >
                            {{ t("settings.driveOpenGcpConsole") }}
                          </AButton>
                          <AButton
                            size="small"
                            type="primary"
                            ghost
                            :loading="checkingDriveHeadless"
                            :disabled="busy || !!state.operation"
                            @click="setupHeadlessDrive"
                          >
                            {{ t("settings.driveCheckHeadless") }}
                          </AButton>
                        </ASpace>
                      </div>

                      <AAlert
                        v-if="
                          ready &&
                          state.auth_url &&
                          state.operation &&
                          !state.setup.drive &&
                          state.drive_auth_hint
                        "
                        type="warning"
                        show-icon
                        :message="tb(state.drive_auth_hint)"
                      />
                    </div>

                    <!-- Row 1.3: Hugging Face Token -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-icon is-violet" aria-hidden="true">
                              <PhKey :size="14" weight="duotone" />
                            </span>
                            <h3 class="co-sr-title">{{ t("settings.hfCardTitle") }}</h3>
                            <ColabOneSkeleton
                              v-if="!snapshotLoaded"
                              :width="46"
                              :height="20"
                              variant="tag"
                              tone="subtle"
                              :delay="80"
                              inline
                            />
                            <ATag v-else-if="state.setup.hf" color="success">
                              {{ t("settings.hfSavedTag") }}
                            </ATag>
                            <ATag v-else>{{ t("common.optional") }}</ATag>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.hfDesc") }}
                          </p>
                        </div>

                        <div class="co-sr-control co-sr-control-input">
                          <div class="co-hf-row">
                            <ColabOneSecretInput
                              ref="hfTokenInputRef"
                              id="settings-hf-token"
                              v-model:value="form.hf_token"
                              :has-saved="!!state.setup.hf"
                              :disabled="busy"
                              autocomplete="new-password"
                              :aria-label="t('settings.hfCardTitle')"
                              :placeholder="t('settings.hfEmptyPlaceholder')"
                              :saved-placeholder="t('settings.hfSavedPlaceholder')"
                              :load-saved="loadSavedHfToken"
                              :extract-secret="colabOneExtractHfToken"
                              @extracted="onHfTokenExtracted"
                              @error="showError($event)"
                            />
                            <AButton
                              type="primary"
                              :disabled="!form.hf_token.trim() || busy"
                              @click="save"
                            >
                              {{ t("settings.hfSaveBtn") }}
                            </AButton>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                </section>

                <!-- Section 2: Compute & Network -->
                <section
                  class="co-settings-group-block"
                  aria-labelledby="settings-sec-compute"
                >
                  <h2 id="settings-sec-compute" class="co-settings-section-label">
                    {{ t("settings.sectionCompute") }}
                  </h2>
                  <div class="co-settings-list">
                    <!-- Row 2.1: Default GPU -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-icon is-blue" aria-hidden="true">
                              <PhSlidersHorizontal :size="14" weight="duotone" />
                            </span>
                            <h3 class="co-sr-title">{{ t("settings.defaultGpuLabel") }}</h3>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.computeSyncNote") }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <ColabOneSkeleton
                            v-if="!snapshotLoaded"
                            :width="248"
                            :height="32"
                            :radius="8"
                            variant="rect"
                          />
                          <ASelect
                            v-else
                            :value="form.gpu"
                            :aria-label="t('settings.defaultGpuAria')"
                            class="co-sr-select co-sr-select-gpu co-sk-reveal"
                            :dropdown-match-select-width="false"
                            :disabled="busy || !!state.operation"
                            @change="changeDefaultGpu"
                          >
                            <ASelectOption
                              v-for="g in GPU_SPECS"
                              :key="g.id"
                              :value="g.id"
                            >
                              {{ g.label }}
                            </ASelectOption>
                          </ASelect>
                        </div>
                      </div>
                    </div>

                    <!-- Row 2.2: Idle Auto-Release -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <h3 class="co-sr-title">{{ t("settings.idleDurationLabel") }}</h3>
                          </div>
                          <div v-if="!snapshotLoaded" class="co-idle-policy-sk" aria-hidden="true">
                            <ColabOneSkeleton width="86%" :height="12" variant="text" tone="subtle" :delay="80" />
                          </div>
                          <p v-else class="co-sr-desc co-sk-reveal">
                            {{
                              t("settings.idleProtectionDesc", {
                                policy: formatIdlePolicyLabel(form.idle_minutes),
                              })
                            }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <ColabOneSkeleton
                            v-if="!snapshotLoaded"
                            :width="184"
                            :height="32"
                            :radius="8"
                            variant="rect"
                            tone="subtle"
                            :delay="60"
                          />
                          <ASelect
                            v-else
                            :value="form.idle_minutes"
                            :aria-label="t('device.idleReleaseAria')"
                            class="co-sr-select co-sr-select-idle co-sk-reveal"
                            :disabled="savingIdle"
                            @change="changeIdleMinutes"
                          >
                            <ASelectOption
                              v-if="!isPresetIdleMinutes(form.idle_minutes)"
                              :value="form.idle_minutes"
                            >
                              {{ formatIdlePolicyLabel(form.idle_minutes) }}
                            </ASelectOption>
                            <ASelectOption
                              v-for="opt in COLABONE_IDLE_RELEASE_OPTIONS"
                              :key="opt.value"
                              :value="opt.value"
                            >
                              {{ opt.label }}
                            </ASelectOption>
                          </ASelect>
                        </div>
                      </div>
                    </div>

                    <!-- Row 2.3: Download Concurrency -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <h3 class="co-sr-title">{{ t("settings.downloadThreadsLabel") }}</h3>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.downloadThreadsDesc") }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <ColabOneSkeleton
                            v-if="!snapshotLoaded"
                            :width="156"
                            :height="32"
                            :radius="9999"
                            variant="rect"
                            tone="subtle"
                            :delay="90"
                          />
                          <ASegmented
                            v-else
                            :value="form.download_threads"
                            :options="downloadThreadOptions"
                            :aria-label="t('settings.downloadThreadsAria')"
                            class="co-sk-reveal"
                            @change="changeDownloadThreads"
                          />
                        </div>
                      </div>
                    </div>

                    <!-- Row 2.4: Auto-Mount Drive -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <h3 class="co-sr-title">{{ t("settings.autoDriveLabel") }}</h3>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.autoDriveDesc") }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <ColabOneSkeleton
                            v-if="!snapshotLoaded"
                            :width="44"
                            :height="22"
                            variant="pill"
                            :delay="110"
                          />
                          <ASwitch
                            v-else
                            :checked="form.auto_drive"
                            :disabled="busy"
                            @update:checked="onAutoDriveChange"
                          />
                        </div>
                      </div>
                    </div>

                    <!-- Row 2.5: GPU Allocation Proxy (Assign Only, Progressive Disclosure) -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-icon is-emerald" aria-hidden="true">
                              <PhShieldCheck :size="14" weight="duotone" />
                            </span>
                            <h3 class="co-sr-title">{{ t("settings.assignProxyTitle") }}</h3>
                            <ColabOneSkeleton
                              v-if="!snapshotLoaded"
                              :width="60"
                              :height="20"
                              variant="tag"
                              tone="subtle"
                              :delay="120"
                              inline
                            />
                            <ATag
                              v-else-if="assignProxySettings?.enabled"
                              color="success"
                            >
                              {{ t("settings.assignProxyTagActive") }}
                            </ATag>
                            <ATag v-else-if="hasSavedAssignProxy">
                              {{ t("settings.assignProxyTagSaved") }}
                            </ATag>
                            <ATag v-else>{{ t("common.optional") }}</ATag>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.assignProxyDesc") }}
                          </p>
                        </div>

                        <div class="co-sr-control">
                          <ColabOneSkeleton
                            v-if="!snapshotLoaded"
                            :width="76"
                            :height="28"
                            variant="button"
                            tone="subtle"
                            :delay="130"
                          />
                          <template v-else>
                            <ASwitch
                              v-if="hasSavedAssignProxy"
                              :checked="form.assign_proxy_enabled"
                              :loading="togglingAssignProxy"
                              :aria-label="t('settings.assignProxySwitchLabel')"
                              :disabled="
                                busy ||
                                assignProxyMutating ||
                                testingAssignProxy ||
                                !!state.operation
                              "
                              @update:checked="onAssignProxyEnabledChange"
                            />
                            <AButton
                              size="small"
                              :disabled="busy || !!state.operation"
                              @click="assignProxyExpanded = !assignProxyExpanded"
                            >
                              {{
                                showAssignProxyEditor
                                  ? t("common.collapse")
                                  : hasSavedAssignProxy
                                    ? t("settings.assignProxyEditBtn")
                                    : t("settings.assignProxyConfigureBtn")
                              }}
                            </AButton>
                          </template>
                        </div>
                      </div>

                      <div
                        v-if="snapshotLoaded && showAssignProxyEditor"
                        class="co-sr-subpanel co-sk-reveal"
                      >
                        <div class="co-assign-proxy-input-row">
                          <ColabOneSecretInput
                            ref="assignProxyInputRef"
                            id="settings-assign-proxy"
                            v-model:value="form.assign_proxy"
                            :has-saved="hasSavedAssignProxy"
                            :disabled="
                              busy ||
                              assignProxyMutating ||
                              testingAssignProxy ||
                              !!state.operation
                            "
                            autocomplete="new-password"
                            :aria-label="t('settings.assignProxyTitle')"
                            :placeholder="t('settings.assignProxyPlaceholder')"
                            :saved-placeholder="assignProxySavedPlaceholder"
                            :load-saved="loadSavedAssignProxy"
                            @error="showError($event)"
                          />
                          <AButton
                            type="primary"
                            :loading="savingAssignProxy"
                            :disabled="
                              !form.assign_proxy.trim() ||
                              busy ||
                              assignProxyMutating ||
                              testingAssignProxy ||
                              !!state.operation
                            "
                            @click="saveAssignProxy"
                          >
                            {{ t("settings.assignProxySaveBtn") }}
                          </AButton>
                        </div>

                        <ASpace :size="8" wrap class="co-assign-proxy-actions">
                          <AButton
                            size="small"
                            :loading="testingAssignProxy"
                            :disabled="
                              (!form.assign_proxy.trim() && !hasSavedAssignProxy) ||
                              busy ||
                              assignProxyMutating
                            "
                            @click="testAssignProxy"
                          >
                            {{ t("settings.assignProxyTestBtn") }}
                          </AButton>
                          <AButton
                            v-if="hasSavedAssignProxy"
                            size="small"
                            type="link"
                            danger
                            :loading="clearingAssignProxy"
                            :disabled="
                              busy ||
                              assignProxyMutating ||
                              testingAssignProxy ||
                              !!state.operation
                            "
                            @click="clearAssignProxy"
                          >
                            {{ t("settings.assignProxyClearBtn") }}
                          </AButton>
                        </ASpace>
                      </div>
                    </div>
                  </div>
                </section>

                <!-- Section 3: Appearance & Language -->
                <section
                  class="co-settings-group-block"
                  aria-labelledby="settings-sec-appearance"
                >
                  <h2 id="settings-sec-appearance" class="co-settings-section-label">
                    {{ t("settings.sectionAppearance") }}
                  </h2>
                  <div
                    class="co-settings-list co-appearance-section"
                    aria-labelledby="appearance-heading"
                  >
                    <!-- Row 3.1: Appearance Theme -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-icon is-amber" aria-hidden="true">
                              <PhSun :size="14" weight="duotone" />
                            </span>
                            <h3 id="appearance-heading" class="co-sr-title">
                              {{ t("settings.appearanceLabel") }}
                            </h3>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.appearanceDesc") }}
                          </p>
                        </div>
                        <div
                          class="co-sr-control"
                          @pointerdown.capture="rememberAppearancePointer"
                        >
                          <ASegmented
                            :value="colabOneAppearance"
                            :options="appearanceSegmentedOptions"
                            :aria-label="t('settings.appearanceAria')"
                            @change="onAppearanceChange"
                          >
                            <template #label="{ value, title, payload }">
                              <span class="co-seg-item">
                                <PhDesktop v-if="value === 'system'" :size="15" />
                                <PhSun v-else-if="value === 'light'" :size="15" />
                                <PhMoon v-else :size="15" />
                                <span>{{ payload?.label ?? title ?? value }}</span>
                              </span>
                            </template>
                          </ASegmented>
                        </div>
                      </div>
                    </div>

                    <!-- Row 3.2: Interface Language -->
                    <div class="co-settings-row">
                      <div class="co-sr-info">
                        <div class="co-sr-title-line">
                          <h3 class="co-sr-title">{{ t("settings.languageLabel") }}</h3>
                        </div>
                        <p class="co-sr-desc">
                          {{ t("settings.languageDesc") }}
                        </p>
                      </div>
                      <LanguageSwitcher mode="settings" @changed="onLocaleChanged" />
                    </div>
                  </div>
                </section>

                <!-- Section 4: Desktop & About -->
                <section
                  class="co-settings-group-block"
                  aria-labelledby="settings-sec-desktop"
                >
                  <h2 id="settings-sec-desktop" class="co-settings-section-label">
                    {{ t("settings.sectionDesktop") }}
                  </h2>
                  <div class="co-settings-list">
                    <!-- Row 4.1: Desktop & Start Menu Shortcuts -->
                    <div
                      class="co-settings-row"
                      aria-labelledby="shortcuts-heading"
                    >
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-icon is-slate" aria-hidden="true">
                              <PhDesktop :size="14" weight="duotone" />
                            </span>
                            <h3 id="shortcuts-heading" class="co-sr-title">
                              {{ t("settings.shortcutsTitle") }}
                            </h3>
                            <ColabOneSkeleton
                              v-if="!snapshotLoaded"
                              :width="60"
                              :height="20"
                              variant="tag"
                              tone="subtle"
                              :delay="100"
                              inline
                            />
                            <ATag v-else-if="state.setup.start_menu_shortcut" color="success">
                              {{ t("settings.shortcutsInStartMenu") }}
                            </ATag>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.shortcutsDesc") }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <div v-if="!snapshotLoaded" class="co-settings-btn-sk" aria-hidden="true">
                            <ColabOneSkeleton :width="112" :height="30" variant="button" :delay="120" />
                            <ColabOneSkeleton :width="116" :height="30" variant="button" tone="subtle" :delay="150" />
                          </div>
                          <ASpace v-else :size="8" wrap>
                            <AButton
                              :type="
                                state.setup.start_menu_shortcut ? 'default' : 'primary'
                              "
                              :disabled="busy"
                              @click="updateShortcut('start_menu')"
                            >
                              {{
                                state.setup.start_menu_shortcut
                                  ? t("settings.shortcutsUpdateStartMenu")
                                  : t("settings.shortcutsAddStartMenu")
                              }}
                            </AButton>
                            <AButton
                              :disabled="busy"
                              @click="updateShortcut('desktop')"
                            >
                              {{
                                state.setup.desktop_shortcut
                                  ? t("settings.shortcutsUpdateDesktop")
                                  : t("settings.shortcutsAddDesktop")
                              }}
                            </AButton>
                            <AButton
                              v-if="
                                state.setup.start_menu_shortcut ||
                                state.setup.desktop_shortcut
                              "
                              type="link"
                              danger
                              size="small"
                              :disabled="busy"
                              @click="updateShortcut('both', true)"
                            >
                              {{ t("settings.shortcutsRemove") }}
                            </AButton>
                          </ASpace>
                        </div>
                      </div>
                    </div>

                    <!-- Row 4.2: About & Open-Source Licenses -->
                    <div class="co-settings-row co-about-card">
                      <div class="co-about-row">
                        <div class="co-about-copy">
                          <div class="co-about-head">
                            <span
                              class="co-about-icon"
                              aria-hidden="true"
                              v-html="appIcon"
                            ></span>
                            <ATypographyTitle :level="5" class="co-about-title">
                              {{ t("settings.aboutTitle") }}
                            </ATypographyTitle>
                            <span class="co-sr-version">{{ appVersionLabel }}</span>
                          </div>
                          <ATypographyText type="secondary" class="co-sr-about-desc">
                            {{ t("settings.aboutDesc") }}
                          </ATypographyText>
                        </div>
                        <div class="co-about-pills">
                          <ATag color="blue" :bordered="false">GPL-3.0</ATag>
                          <ATag>Apache-2.0</ATag>
                          <ATag>MIT</ATag>
                        </div>
                      </div>
                    </div>
                  </div>
                </section>
              </div>
            </div>
          </main>
        </ALayoutContent>

          <!-- Floating Bottom Save Bar (shown only once top Settings save button scrolls out of view) -->
          <div
            v-if="showFloatingSettingsSave"
            class="co-settings-floating-wrap"
            role="region"
            :aria-label="t('settings.floatingSaveAria')"
          >
            <div class="co-settings-floating-bar">
              <div class="co-settings-floating-info">
                <span class="co-sr-icon is-blue" aria-hidden="true">
                  <PhSlidersHorizontal :size="14" weight="duotone" />
                </span>
                <div class="co-settings-floating-copy">
                  <span class="co-settings-floating-title">{{
                    t("settings.title")
                  }}</span>
                  <span class="co-settings-floating-desc">{{
                    t("settings.floatingSaveHint")
                  }}</span>
                </div>
              </div>
              <AButton
                type="primary"
                class="co-settings-floating-btn"
                :disabled="busy"
                @click="save"
              >
                {{ t("settings.saveSettings") }}
              </AButton>
            </div>
          </div>
        </div>

        <!-- Pinned Active Remote Task Progress Bar (excludes API inference, handled inline per request) -->
        <div v-if="dockTask" class="co-task-dock">
          <ACard size="small" class="co-task-card co-task-bar">
            <div class="co-task-row">
              <ASpin size="small" />
              <div class="co-task-main">
                <div class="co-task-head">
                  <strong>{{ taskBarTitle(dockTask.kind) }}</strong>
                  <span class="co-muted">{{ tb(dockTask.message) }}</span>
                </div>
                <div v-if="dockTask.download" class="co-download-stats">
                  <b class="co-tabular">
                    {{ (dockTask.download.speed_bps / 1024 ** 2).toFixed(1) }} MiB/s
                  </b>
                  <span class="co-tabular">
                    {{ bytes(dockTask.download.downloaded_bytes) }} /
                    {{ bytes(dockTask.download.total_bytes) }}
                  </span>
                  <span>
                    {{
                      dockTask.download.phase === "verifying"
                        ? t("device.downloadVerifying")
                        : t("device.downloadEta", {
                            eta: duration(dockTask.download.eta_seconds),
                          })
                    }}
                  </span>
                  <span class="co-tabular">
                    {{
                      t("device.downloadMeta", {
                        conns: `${dockTask.download.connections}/${dockTask.download.threads}`,
                        idx: dockTask.download.file_index,
                        total: dockTask.download.file_count,
                      })
                    }}
                  </span>
                </div>
                <AProgress
                  v-if="
                    dockTask.kind === 'download' ||
                    dockTask.kind === 'comfyui_start' ||
                    dockTask.kind === 'comfyui_run'
                  "
                  :percent="Math.round((dockTask.progress || 0) * 100)"
                  size="small"
                  status="active"
                />
              </div>
              <AButton
                danger
                size="small"
                :disabled="busy"
                @click="act('task.cancel', { id: dockTask.id })"
              >
                {{ t("common.cancel") }}
              </AButton>
            </div>
          </ACard>
        </div>
      </ALayout>

      <!-- Inline Fallback Logs Column (only in non-desktop web previews when standalone OS window is unavailable) -->
      <LogDrawer
        v-if="!logWindowStandalone"
        :open="logsOpen"
        :width="logDrawerWidth"
        :loading="!snapshotLoaded"
        :logs="state.logs || []"
        :current-activity="currentActivity"
        :running="!!(state.operation || task)"
        :active-speed="activeApiSpeed"
        @update:width="onLogDrawerWidthChange"
        @copy="copy"
        @clear="act('logs.clear')"
      />

      <!-- Unified Floating Countdown Toast Stack -->
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

      <!-- Modal: Selective Instance Release Review -->
      <AModal
        v-model:open="showRelease"
        :title="t('device.releaseModalTitle')"
        :width="580"
        :mask-closable="!busy"
        :closable="!busy"
        :footer="null"
        centered
      >
        <div class="co-modal-stack">
          <p class="co-muted co-modal-lead">
            {{ t("device.releaseModalDesc") }}
          </p>

          <div
            v-if="!releasePlan && busy"
            class="co-release-skeleton"
            role="status"
            aria-busy="true"
          >
            <div class="co-release-list">
              <div
                v-for="idx in 2"
                :key="idx"
                class="co-release-item co-release-item-skeleton"
              >
                <div class="co-release-sk-row">
                  <ColabOneSkeleton
                    :width="16"
                    :height="16"
                    :radius="4"
                    variant="rect"
                    tone="subtle"
                    :delay="(idx - 1) * 75"
                  />
                  <div class="co-release-item-info co-release-sk-info">
                    <div class="co-release-item-top">
                      <ColabOneSkeleton
                        :width="idx === 1 ? 156 : 132"
                        :height="14"
                        variant="text"
                        :delay="(idx - 1) * 75 + 35"
                      />
                      <ColabOneSkeleton
                        :width="42"
                        :height="20"
                        variant="tag"
                        tone="subtle"
                        :delay="(idx - 1) * 75 + 70"
                      />
                      <ColabOneSkeleton
                        :width="66"
                        :height="20"
                        variant="tag"
                        tone="subtle"
                        :delay="(idx - 1) * 75 + 105"
                      />
                    </div>
                    <ColabOneSkeleton
                      :width="idx === 1 ? '74%' : '62%'"
                      :height="13"
                      variant="text"
                      tone="subtle"
                      :delay="(idx - 1) * 75 + 130"
                    />
                  </div>
                </div>
              </div>
            </div>
            <span class="co-release-sk-caption">{{ t("device.releaseLoading") }}</span>
          </div>

          <div v-else-if="!releasePlan" class="co-modal-loading">
            <span>{{ t("device.releaseLoadFailed") }}</span>
            <AButton
              size="small"
              :disabled="busy"
              @click="openRelease(releaseTargetEndpoint)"
            >
              {{ t("common.retry") }}
            </AButton>
          </div>

          <template v-else>
            <div
              v-if="releasePlan.instances.length > 1"
              class="co-release-toolbar"
            >
              <AButton
                type="link"
                size="small"
                @click="selectAllRelease"
              >
                {{
                  t("device.releaseSelectAll", {
                    count: releasePlan.instances.length,
                  })
                }}
              </AButton>
              <AButton
                v-if="
                  releasePlan.instances.some((i) => i.managed) &&
                  releasePlan.instances.some((i) => !i.managed)
                "
                type="link"
                size="small"
                @click="selectManagedReleaseOnly"
              >
                {{
                  t("device.releaseSelectManaged", {
                    count: releasePlan.instances.filter((i) => i.managed).length,
                  })
                }}
              </AButton>
            </div>

            <div class="co-release-list">
              <div
                v-for="instance in releasePlan.instances"
                :key="instance.id"
                class="co-release-item"
                :class="{
                  selected: selectedReleaseIds.includes(instance.id),
                }"
              >
                <ACheckbox
                  :checked="selectedReleaseIds.includes(instance.id)"
                  @update:checked="
                    (checked) =>
                      toggleReleaseSelection(instance.id, Boolean(checked))
                  "
                >
                  <div class="co-release-item-info">
                    <div class="co-release-item-top">
                      <code class="co-mono">{{ instance.id }}</code>
                      <ATag v-if="instance.accelerator">{{
                        instance.accelerator
                      }}</ATag>
                      <ATag v-if="shapeLabel(instance.shape)">{{
                        shapeLabel(instance.shape)
                      }}</ATag>
                      <ColabOneTooltip
                        v-if="instance.location?.country"
                        :title="locationTooltip(instance.location)"
                      >
                        <ATag class="co-location-tag">
                          <FlagIcon
                            :code="instance.location.country"
                            :size="13"
                          />
                          <span>{{ locationShort(instance.location) }}</span>
                        </ATag>
                      </ColabOneTooltip>
                    </div>
                    <span class="co-release-item-desc">{{
                      instance.managed
                        ? instance.active
                          ? t("device.releaseInstCurrent", {
                              model: instance.loaded_model
                                ? t("device.releaseInstModelSuffix", {
                                    model: instance.loaded_model,
                                  })
                                : "",
                            })
                          : t("device.releaseInstManaged", {
                              model: instance.loaded_model
                                ? t("device.releaseInstModelSuffix", {
                                    model: instance.loaded_model,
                                  })
                                : "",
                            })
                        : t("device.releaseInstExternal")
                    }}</span>
                  </div>
                </ACheckbox>
              </div>

              <AEmpty
                v-if="!releasePlan.instances.length"
                :description="t('device.releaseEmpty')"
              />
            </div>
          </template>

          <AAlert
            v-if="state.operation"
            type="info"
            show-icon
            :message="t('device.releaseBusyNotice')"
          />

          <ACheckbox v-model:checked="force">
            {{ t("device.releaseForceCheck") }}
          </ACheckbox>

          <div class="co-modal-footer">
            <AButton
              :disabled="busy"
              @click="showRelease = false"
            >
              {{ t("common.cancel") }}
            </AButton>
            <AButton
              type="primary"
              danger
              :loading="busy"
              :disabled="
                busy ||
                !!state.operation ||
                !releasePlan?.instances.length ||
                !selectedReleaseIds.length
              "
              @click="release"
            >
              {{
                isAllReleaseSelected
                  ? t("device.releaseConfirmAll", {
                      count: releasePlan?.instances.length || 0,
                    })
                  : t("device.releaseConfirmSelected", {
                      count: selectedReleaseIds.length,
                    })
              }}
            </AButton>
          </div>
        </div>
      </AModal>

      <!-- Modal: Confirm Hot-Upgrading Remote Service While Active -->
      <AModal
        v-model:open="showUpgradeConfirm"
        :title="t('status.upgradeConfirmTitle')"
        :width="480"
        :mask-closable="!busy"
        :closable="!busy"
        :footer="null"
        centered
      >
        <div class="co-modal-stack">
          <p class="co-muted co-modal-lead">
            {{
              upgradeBanner.activeItems.length
                ? t("status.upgradeConfirmLead")
                : t("status.upgradeOptionalReady")
            }}
          </p>

          <ul
            v-if="upgradeBanner.activeItems.length"
            class="co-upgrade-confirm-list"
          >
            <li
              v-for="item in upgradeBanner.activeItems"
              :key="item"
              class="co-upgrade-confirm-item"
            >
              <span class="co-upgrade-confirm-dot" aria-hidden="true" />
              <span>{{ item }}</span>
            </li>
          </ul>

          <AAlert
            type="info"
            show-icon
            :message="t('status.upgradeConfirmPreserveNote')"
          />

          <div class="co-modal-footer">
            <AButton
              :disabled="busy"
              @click="showUpgradeConfirm = false"
            >
              {{ t("common.cancel") }}
            </AButton>
            <AButton
              type="primary"
              :loading="busy"
              :disabled="busy || !!state.operation"
              @click="confirmRuntimeUpgrade"
            >
              {{ t("status.upgradeConfirmAction") }}
            </AButton>
          </div>
        </div>
      </AModal>
    </ALayout>
  </AConfigProvider>
</template>

<style scoped>
/* Root Shell — horizontal row so LogDrawer expands strictly to the right */
.co-shell {
  height: 100vh;
  width: 100vw;
  overflow: hidden;
  display: flex;
  flex-direction: row !important;
  background: var(--co-canvas);
}

/* Main Workspace */
.co-workspace {
  flex: 1;
  min-width: 0;
  min-height: 0;
  height: 100vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--co-canvas);
}

/* ColabOne Top Floating Capsule Navbar */
.co-navbar {
  container-type: inline-size;
  container-name: co-navbar;
  flex-shrink: 0;
  background: var(--co-canvas);
  user-select: none;
  z-index: 20;
}

.co-navbar-inner {
  max-width: var(--co-container-max);
  width: 100%;
  height: 72px;
  margin: 0 auto;
  padding: 0 var(--co-container-pad);
  box-sizing: border-box;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  align-items: center;
  gap: 16px;
}

.co-nav-left {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.co-brand {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  padding: 4px 6px 4px 0;
  border: none;
  background: transparent;
  color: var(--co-text);
  font-size: 26px;
  font-weight: 700;
  line-height: 1.1;
  letter-spacing: -0.028em;
  min-width: 0;
  cursor: pointer;
  transition: opacity 0.16s ease;
}

.co-brand:hover {
  opacity: 0.86;
}

.co-brand:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
  border-radius: 12px;
}

.co-brand-icon {
  width: 48px;
  height: 48px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition: transform 0.14s ease;
}

.co-brand:active .co-brand-icon {
  transform: scale(0.94);
}

.co-brand-icon :deep(svg) {
  width: 100%;
  height: 100%;
  display: block;
}

.co-brand-text {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.co-brand-version {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-top: 3px;
  padding: 3px 8px;
  border-radius: var(--co-radius-pill);
  border: 1px solid var(--co-version-border);
  background: var(--co-version-bg);
  color: var(--co-version-text);
  font-size: 11px;
  font-weight: 600;
  line-height: 1.1;
  letter-spacing: 0.015em;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  user-select: text;
}

/* ColabOne Centered Floating Capsule Pill Navigation */
.co-nav-center {
  display: flex;
  justify-content: center;
  min-width: 0;
}

.co-nav-pill-track {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px;
  border-radius: 9999px;
  background: var(--co-track);
}

:global(:root[data-theme="dark"]) .co-nav-pill-track {
  border: 1px solid var(--co-border-secondary);
}

.co-nav-pill-item {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 38px;
  padding: 0 22px;
  border: none;
  border-radius: 9999px;
  background: transparent;
  color: var(--co-secondary-text);
  font-family: inherit;
  font-size: 14px;
  font-weight: 500;
  line-height: 1;
  white-space: nowrap;
  cursor: pointer;
  transition: transform 0.12s ease;
}

.co-nav-pill-item:hover:not(.is-active) {
  background: var(--co-hover);
  color: var(--co-text);
}

.co-nav-pill-item:active {
  transform: scale(0.97);
}

.co-nav-pill-item:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-nav-pill-item.is-active {
  background: var(--co-pill-active-bg);
  color: var(--co-pill-active-text);
  font-weight: 600;
  box-shadow: var(--co-pill-active-shadow);
}

/* ColabOne NavMenu: clean text-only pills on desktop, icon-only pills below 800px */
.co-nav-pill-icon {
  display: none;
  flex-shrink: 0;
  opacity: 0.85;
}

.co-nav-pill-item.is-active .co-nav-pill-icon {
  opacity: 1;
}

.co-nav-live-dot {
  width: 5px;
  height: 5px;
  border-radius: 9999px;
  flex-shrink: 0;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.24);
}

/* Right Circular Utility Buttons & Logs Pill */
.co-nav-right {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  min-width: 0;
}

.co-nav-circle-btn {
  width: 40px;
  height: 40px;
  padding: 0;
  border: none;
  border-radius: 9999px;
  background: var(--co-track);
  color: var(--co-secondary-text);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
  transition:
    background-color 0.16s ease,
    color 0.16s ease,
    transform 0.12s ease;
}

:global(:root[data-theme="dark"]) .co-nav-circle-btn,
:global(:root[data-theme="dark"]) .co-logs-trigger {
  border: 1px solid var(--co-border-secondary);
}

.co-nav-circle-btn:hover:not(:disabled) {
  background: var(--co-hover);
  color: var(--co-text);
}

.co-nav-circle-btn:active:not(:disabled) {
  transform: scale(0.95);
}

.co-nav-circle-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.co-nav-circle-btn:focus-visible,
.co-logs-trigger:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-logs-trigger {
  height: 40px;
  padding: 0 15px;
  border: none;
  border-radius: 9999px;
  background: var(--co-track);
  color: var(--co-secondary-text);
  font-family: inherit;
  font-size: 13.5px;
  font-weight: 500;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
  cursor: pointer;
  flex-shrink: 0;
  transition: transform 0.12s ease;
}

.co-logs-trigger:hover:not(.is-active) {
  background: var(--co-hover);
  color: var(--co-text);
}

.co-logs-trigger:active {
  transform: scale(0.97);
}

.co-logs-trigger.is-active {
  background: var(--co-pill-active-bg);
  border-color: var(--co-pill-active-bg);
  color: var(--co-pill-active-text);
  font-weight: 600;
  box-shadow: var(--co-pill-active-shadow);
}

/* ColabOne Breathable Seamless Status & Billing Strip */
.co-status-bar {
  container-type: inline-size;
  container-name: co-status-bar;
  flex-shrink: 0;
  background: var(--co-canvas);
}

.co-status-bar-inner {
  max-width: var(--co-container-max);
  width: 100%;
  min-height: 36px;
  margin: 0 auto;
  padding: 0 var(--co-container-pad) 6px;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px 12px;
}

.co-topbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
}

.co-runtime-tag {
  margin-inline-end: 0;
  font-weight: 500;
  flex-shrink: 0;
}

.co-gpu-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-secondary-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
  flex: 0 1 auto;
}

.co-location-tag {
  margin-inline-end: 0;
  flex-shrink: 0;
}

.co-model-chip {
  height: 28px;
  max-width: 220px;
  min-width: 0;
  border-radius: 9999px !important;
  flex-shrink: 1;
  display: inline-flex;
  align-items: center;
  overflow: hidden;
}

.co-model-chip-text {
  display: inline-block;
  min-width: 0;
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@container co-status-bar (max-width: 1120px) {
  .co-status-bar-inner {
    flex-wrap: wrap;
    row-gap: 6px;
  }

  .co-topbar-left {
    flex: 1 1 260px;
  }

  .co-topbar-right {
    margin-left: auto;
  }
}

.co-instance-select {
  min-width: 170px;
  max-width: 240px;
  flex-shrink: 1;
}

.co-topbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

/* Global Banners */
.co-banners {
  flex-shrink: 0;
  background: var(--co-canvas);
}

.co-banners-inner {
  max-width: var(--co-container-max);
  width: 100%;
  margin: 0 auto;
  padding: 4px var(--co-container-pad) 6px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-upgrade-banner :deep(.ant-alert-action),
.co-op-banner :deep(.ant-alert-action) {
  margin-inline-start: 12px;
  flex-shrink: 0;
}

.co-op-banner {
  flex-wrap: wrap;
  row-gap: 8px;
}

.co-upgrade-action-wrap,
.co-op-action-wrap {
  display: inline-flex;
}

.co-upgrade-action-wrap.is-disabled,
.co-op-action-wrap.is-disabled {
  cursor: not-allowed;
}

.co-op-actions {
  flex-shrink: 0;
}

.co-op-message {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}

.co-op-message-text {
  line-height: 1.45;
}

/* Main Scrollable Content */
.co-content-stage {
  position: relative;
  flex: 1;
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.co-content {
  flex: 1;
  min-height: 0;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden;
  background: var(--co-canvas);
  display: flex;
  flex-direction: column;
}

.co-page-shell {
  width: 100%;
  max-width: var(--co-container-max);
  margin: 0 auto;
  flex: 1 0 auto;
  padding: 16px var(--co-container-pad) 32px;
  box-sizing: border-box;
}

.co-settings-page {
  padding-bottom: 92px;
}

.co-page-inner {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-settings-inner {
  width: 100%;
  max-width: 840px;
  margin-inline: auto;
  gap: 20px;
}

.co-settings-top-save-slot {
  min-height: 32px;
  min-width: 1px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
}

.co-settings-floating-wrap {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 15;
  pointer-events: none;
  padding: 20px var(--co-container-pad) 16px;
  background: linear-gradient(
    to bottom,
    transparent 0%,
    color-mix(in srgb, var(--co-canvas) 78%, transparent) 52%,
    var(--co-canvas) 100%
  );
}

.co-settings-floating-bar {
  pointer-events: auto;
  width: 100%;
  max-width: 840px;
  margin: 0 auto;
  box-sizing: border-box;
  padding: 11px 16px 11px 18px;
  border-radius: 14px;
  border: 1px solid var(--co-border);
  background: var(--co-toast-bg);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  box-shadow: var(--co-toast-shadow);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  animation: co-settings-floating-in 0.18s cubic-bezier(0.22, 1, 0.36, 1);
}

:global(:root[data-theme="dark"]) .co-settings-floating-bar {
  border-color: rgba(255, 255, 255, 0.11);
}

.co-settings-floating-info {
  display: flex;
  align-items: center;
  gap: 11px;
  min-width: 0;
  flex: 1;
}

.co-settings-floating-copy {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.co-settings-floating-title {
  font-size: 13.5px;
  font-weight: 600;
  line-height: 1.3;
  color: var(--co-text);
}

.co-settings-floating-desc {
  font-size: 12px;
  line-height: 1.4;
  color: var(--co-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.co-settings-floating-btn {
  flex-shrink: 0;
}

@keyframes co-settings-floating-in {
  from {
    opacity: 0;
    transform: translateY(10px) scale(0.992);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

@media (max-width: 520px) {
  .co-settings-floating-desc {
    display: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .co-settings-floating-bar {
    animation: none;
  }
}

.co-panel-wrap {
  width: 100%;
  flex: 1 0 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

@container co-navbar (max-width: 1120px) {
  .co-navbar-inner {
    display: flex;
    justify-content: space-between;
    gap: 12px;
  }

  .co-nav-left {
    flex: 0 1 auto;
  }

  .co-nav-center {
    flex: 0 1 auto;
  }

  .co-nav-right {
    flex: 0 0 auto;
  }

  .co-nav-pill-item {
    padding: 0 16px;
  }
}

@container co-navbar (max-width: 1020px) {
  .co-navbar-inner {
    height: 64px;
  }

  .co-brand {
    gap: 10px;
    font-size: 21px;
  }

  .co-brand-icon {
    width: 38px;
    height: 38px;
  }

  .co-brand-version {
    margin-top: 2px;
    padding: 2px 7px;
    font-size: 10.5px;
  }

  .co-logs-trigger {
    width: 40px;
    padding: 0;
    justify-content: center;
  }

  .co-logs-trigger-label {
    display: none;
  }
}

@container co-navbar (max-width: 820px) {
  .co-nav-pill-item {
    width: 38px;
    padding: 0;
  }

  .co-nav-pill-icon {
    display: inline-block;
  }

  .co-nav-pill-label {
    display: none;
  }

  .co-nav-live-dot {
    position: absolute;
    top: 6px;
    right: 6px;
  }
}

@container co-navbar (max-width: 680px) {
  .co-brand-version {
    display: none;
  }
}

@container co-navbar (max-width: 600px) {
  .co-brand-text {
    display: none;
  }
}

/* Setup Callout */
.co-setup-callout {
  border-color: var(--co-accent);
  background: var(--co-surface);
}

.co-callout-row {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.co-callout-icon {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  border: 1px solid rgba(59, 130, 246, 0.18);
}

.co-callout-copy {
  flex: 1;
  min-width: 220px;
}

.co-callout-copy h3 {
  margin: 0 0 2px;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-callout-copy p {
  margin: 0;
  font-size: 13px;
  color: var(--co-secondary-text);
}

/* Primary Device Hero Grid (GPU Control + Billing Overview side-by-side on wide fullscreen) */
.co-device-hero-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: 16px;
}

@media (min-width: 1380px) {
  .co-device-hero-grid {
    grid-template-columns: minmax(0, 1.15fr) minmax(0, 0.85fr);
    align-items: stretch;
  }

  .co-device-hero-grid > .co-device-card,
  .co-device-hero-grid > .co-billing-card-wrap {
    min-width: 0;
    display: flex;
    flex-direction: column;
  }

  .co-device-hero-grid > .co-device-card :deep(.ant-card-body),
  .co-device-hero-grid > .co-billing-card-wrap :deep(.co-billing-card) {
    flex: 1;
    display: flex;
    flex-direction: column;
  }

  .co-device-hero-grid > .co-billing-card-wrap :deep(.co-billing-card .ant-card-body) {
    flex: 1;
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .co-device-hero-grid > .co-device-card .co-device-meta-strips {
    margin-top: auto;
    padding-top: 14px;
  }
}

/* Primary Device Control Card */
.co-device-head {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.co-device-icon {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  background: var(--co-selected);
  border: 1px solid rgba(59, 130, 246, 0.18);
  color: var(--co-accent);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-device-title-main {
  flex: 1;
  min-width: 220px;
}

.co-device-title-main h2 {
  margin: 0 0 4px;
  font-size: 18px;
  font-weight: 600;
  line-height: 1.3;
  color: var(--co-text);
}

.co-device-subtitle {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--co-muted);
}

.co-runtime-id {
  font-family: var(--co-font-mono);
  font-size: 12px;
  padding: 1px 7px;
  border-radius: 4px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-secondary-text);
}

.co-device-badges {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.co-card-divider {
  margin: 14px 0 !important;
}

.co-device-controls {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px 16px;
  flex-wrap: wrap;
}

.co-device-fields {
  display: flex;
  align-items: flex-end;
  gap: 12px;
  flex-wrap: wrap;
}

.co-control-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-field-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--co-secondary-text);
  line-height: 1.3;
}

.co-select-gpu {
  width: 316px;
  max-width: 100%;
}

.co-select-idle {
  min-width: 172px;
  width: auto;
}

.co-device-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-device-meta-strips {
  margin-top: 16px;
  display: flex;
  flex-direction: column;
  gap: 0;
}

.co-device-policy {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  flex-wrap: wrap;
  padding: 12px 0 0;
  border-top: 1px solid var(--co-border-secondary);
  background: transparent;
}

.co-policy-live {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 500;
  color: var(--co-text);
}

.co-policy-live.is-active {
  color: var(--co-accent);
}

.co-policy-live.is-urgent {
  color: var(--co-danger-text);
}

.co-policy-hint {
  font-size: 12px;
  color: var(--co-muted);
}

.co-drive-strip {
  margin-top: 12px;
  padding: 12px 0 0;
  border-top: 1px solid var(--co-border-secondary);
  background: transparent;
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.co-drive-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-drive-text {
  flex: 1;
  min-width: 200px;
  font-size: 13px;
  color: var(--co-secondary-text);
}

.co-drive-text.is-warning {
  color: var(--co-warning-text);
}

.co-drive-hint-alert {
  margin-top: 12px;
}

/* Multi-Instance Card */
.co-card-title-block {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-card-title-sub {
  font-size: 13px;
  font-weight: 400;
  color: var(--co-muted);
}

.co-instance-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(520px, 100%), 1fr));
  gap: 12px;
}

.co-instance-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  flex-wrap: wrap;
  transition: border-color 0.16s ease, background-color 0.16s ease;
}

.co-instance-row.active {
  border-color: rgba(59, 130, 246, 0.45);
  background: var(--co-selected);
}

.co-instance-main {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  flex: 1;
}

.co-instance-tags {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.co-instance-endpoint {
  font-family: var(--co-font-mono);
  font-size: 13px;
  color: var(--co-text);
}

.co-instance-detail {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--co-secondary-text);
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 3px 4px;
}

.co-instance-detail-sk {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-instance-actions {
  display: flex;
  align-items: center;
  align-self: center;
  gap: 8px;
  flex-shrink: 0;
}

.co-footnote {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-muted);
}

/* Settings Stage — Native Single-Column Compact Grouped Rows (macOS / ChatGPT style) */
.co-settings-stack {
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.co-settings-group-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}

.co-settings-section-label {
  margin: 0;
  padding-inline: 4px;
  font-size: 12.5px;
  font-weight: 600;
  line-height: 1.4;
  letter-spacing: 0.01em;
  color: var(--co-muted);
}

.co-settings-list {
  border-radius: 14px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
  overflow: hidden;
}

.co-settings-row {
  padding: 15px 18px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-settings-row + .co-settings-row {
  border-top: 1px solid var(--co-border-secondary);
}

.co-sr-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
}

.co-sr-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.co-sr-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-sr-icon {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-sr-icon.is-blue {
  background: var(--co-selected);
  color: var(--co-accent);
}

.co-sr-icon.is-emerald {
  background: var(--co-success-bg);
  color: var(--co-success-text);
}

.co-sr-icon.is-amber {
  background: var(--co-warning-bg);
  color: var(--co-warning-text);
}

.co-sr-icon.is-violet {
  background: rgba(139, 92, 246, 0.12);
  color: #7c3aed;
}

:global(:root[data-theme="dark"]) .co-sr-icon.is-violet {
  background: rgba(139, 92, 246, 0.18);
  color: #a78bfa;
}

.co-sr-icon.is-slate {
  background: var(--co-track);
  color: var(--co-secondary-text);
}

.co-sr-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--co-text);
}

.co-sr-desc {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.55;
  color: var(--co-muted);
}

.co-sr-desc code {
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  font-size: 11.5px;
  color: var(--co-secondary-text);
}

.co-saved-login.co-sr-saved-note {
  margin: 2px 0 0;
  font-size: 12px;
  line-height: 1.45;
}

.co-sr-control {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  flex-shrink: 0;
}

.co-sr-control-input {
  width: 344px;
  max-width: 100%;
}

.co-sr-select-gpu {
  width: 248px;
}

.co-sr-select-idle {
  width: 184px;
}

.co-inline-collapse.co-sr-collapse {
  margin-top: -4px;
}

.co-inline-collapse.co-sr-collapse :deep(.ant-collapse-header) {
  padding: 2px 0 !important;
  font-size: 12.5px;
}

.co-sr-callout {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px 16px;
  flex-wrap: wrap;
  padding: 10px 13px;
  border-radius: 10px;
  background: var(--co-selected);
  border: 1px solid rgba(59, 130, 246, 0.22);
}

.co-sr-callout-copy {
  flex: 1 1 320px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-sr-callout-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-sr-callout-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-sr-callout-actions {
  flex-shrink: 0;
}

.co-sr-subpanel {
  padding: 12px 14px;
  border-radius: 10px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-sr-version {
  font-family: var(--co-font-mono);
  font-size: 11px;
  font-weight: 500;
  padding: 1px 7px;
  border-radius: 9999px;
  background: var(--co-track);
  color: var(--co-muted);
}

.co-sr-about-desc {
  font-size: 12.5px;
}

@media (max-width: 720px) {
  .co-sr-main {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .co-sr-control {
    justify-content: flex-start;
    flex-wrap: wrap;
  }

  .co-sr-control-input,
  .co-sr-select-gpu,
  .co-sr-select-idle {
    width: 100%;
  }
}

.co-card-head-title {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  color: var(--co-text);
}

.co-head-icon-badge {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-head-icon-badge.is-blue {
  background: var(--co-selected);
  color: var(--co-accent);
}

.co-head-icon-badge.is-emerald {
  background: var(--co-success-bg);
  color: var(--co-success-text);
}

.co-head-icon-badge.is-amber {
  background: var(--co-warning-bg);
  color: var(--co-warning-text);
}

.co-head-icon-badge.is-violet {
  background: rgba(139, 92, 246, 0.12);
  color: #7c3aed;
}

:global(:root[data-theme="dark"]) .co-head-icon-badge.is-violet {
  background: rgba(139, 92, 246, 0.18);
  color: #a78bfa;
}

.co-head-icon-badge.is-slate {
  background: var(--co-track);
  color: var(--co-secondary-text);
}

.co-card-head-title h2,
.co-card-head-title h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-settings-desc {
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

.co-settings-desc code {
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  font-size: 12px;
}

.co-saved-login {
  margin: 12px 0 0;
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--co-success-text);
}

.co-inline-collapse {
  margin-top: 8px;
}

.co-inline-collapse :deep(.ant-collapse-header) {
  padding: 6px 0 !important;
  color: var(--co-accent) !important;
  font-size: 13px;
  font-weight: 500;
}

.co-inline-collapse :deep(.ant-collapse-content-box) {
  padding: 6px 0 0 !important;
}

.co-collapse-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-collapse-body p {
  margin: 0;
  font-size: 13px;
}

.co-section-divider {
  margin: 16px 0 !important;
}

.co-sub-alert {
  margin-bottom: 12px;
}

.co-drive-headless-desc {
  margin: 0 0 8px;
  font-size: 12px;
  line-height: 1.55;
  color: var(--co-secondary-text);
}

.co-drive-headless-actions {
  display: flex;
  align-items: center;
}

.co-drive-headless-link {
  padding-inline: 4px !important;
  font-size: 12px !important;
}

.co-appearance-section :deep(.ant-segmented) {
  align-self: flex-start;
}

.co-language-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-language-desc {
  margin-bottom: 4px;
}

.co-seg-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  line-height: 1.2;
}

.co-seg-item svg {
  flex-shrink: 0;
}

.co-settings-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.38fr) minmax(0, 1fr);
  gap: 16px;
  margin-bottom: 14px;
}

@media (max-width: 640px) {
  .co-settings-grid {
    grid-template-columns: 1fr;
  }
}

.co-setting-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-full-width {
  width: 100%;
}

.co-switch-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 34px;
}

.co-switch-label {
  font-size: 13px;
  color: var(--co-secondary-text);
}

.co-idle-policy-box {
  padding: 14px 16px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  margin-bottom: 10px;
}

.co-idle-policy-box strong {
  display: block;
  margin-bottom: 4px;
  font-size: 13px;
  color: var(--co-text);
}

.co-idle-policy-box p {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

.co-hf-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.co-assign-proxy-controls,
.co-assign-proxy-sk {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-assign-proxy-input-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.co-assign-proxy-actions {
  margin-top: -2px;
}

.co-about-card :deep(.ant-card-body) {
  padding: 16px 22px;
}

.co-about-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.co-about-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.co-about-icon {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-about-icon :deep(svg) {
  width: 100%;
  height: 100%;
  display: block;
}

.co-about-copy {
  flex: 1;
  min-width: 240px;
}

.co-about-title {
  margin: 0 !important;
  font-size: 14px !important;
}

.co-about-pills {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

/* Active Task Dock */
.co-task-dock {
  flex-shrink: 0;
  padding: 10px var(--co-container-pad) 12px;
  background: var(--co-surface);
  border-top: 1px solid var(--co-border-secondary);
}

.co-task-card {
  max-width: calc(var(--co-container-max) - 2 * var(--co-container-pad));
  margin: 0 auto;
  border-color: var(--co-accent);
  background: var(--co-subtle);
}

.co-task-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.co-task-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-task-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 13px;
}

.co-download-stats {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--co-secondary-text);
}

/* Modals */
.co-modal-stack {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-top: 4px;
}

.co-modal-lead {
  margin: 0;
  font-size: 14px;
  line-height: 1.6;
}

.co-upgrade-confirm-list {
  list-style: none;
  margin: 0;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-upgrade-confirm-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--co-text);
}

.co-upgrade-confirm-item > span:last-child {
  min-width: 0;
  overflow-wrap: anywhere;
}

.co-upgrade-confirm-dot {
  width: 6px;
  height: 6px;
  margin-top: 7px;
  border-radius: 50%;
  background: var(--co-accent);
  flex-shrink: 0;
}

.co-modal-loading {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 0;
  color: var(--co-muted);
}

.co-release-toolbar {
  display: flex;
  align-items: center;
  gap: 4px;
}

.co-release-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 260px;
  overflow-y: auto;
}

.co-release-item {
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  transition: border-color 0.16s ease, background-color 0.16s ease;
}

.co-release-item.selected {
  border-color: var(--co-danger-border);
  background: var(--co-danger-bg);
}

.co-release-item-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-left: 4px;
}

.co-release-item-top {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.co-release-item-desc {
  font-size: 13px;
  color: var(--co-secondary-text);
}

.co-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 6px;
}

/* Skeleton Layout Alignment Helpers */
.co-topbar-sk {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 24px;
}

.co-device-sk-title {
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.co-device-sk-policy {
  min-height: 24px;
}

.co-settings-btn-sk {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-height: 32px;
}

.co-idle-policy-sk {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 6px;
}

.co-instance-skeleton {
  background: var(--co-skeleton-card-bg);
  pointer-events: none;
}

.co-settings-email-sk {
  display: inline-flex;
  align-items: center;
  min-height: 20px;
}

.co-release-skeleton {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-release-item-skeleton {
  background: var(--co-skeleton-card-bg);
  pointer-events: none;
}

.co-release-sk-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.co-release-sk-info {
  flex: 1;
  min-width: 0;
  gap: 6px;
}

.co-release-sk-caption {
  font-size: 12px;
  color: var(--co-muted);
}

.co-nav-circle-btn.is-spinning svg {
  animation: co-nav-spin var(--co-spin-duration, 4s) linear infinite;
}

@keyframes co-nav-spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .co-nav-circle-btn.is-spinning svg {
    animation: none;
  }
}
</style>
