<script setup lang="ts">
import {
  computed,
  defineAsyncComponent,
  defineComponent,
  h,
  nextTick,
  onMounted,
  onUnmounted,
  ref,
  shallowRef,
  watch,
  type AsyncComponentLoader,
  type Component,
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
  InputNumber as AInputNumber,
  Layout as ALayout,
  LayoutContent as ALayoutContent,
  Modal as AModal,
  Popconfirm as APopconfirm,
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
import ColabOneCloseModal from "./ColabOneCloseModal.vue";
import ColabOneMigrationModal from "./ColabOneMigrationModal.vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import ColabOneSecretInput from "./ColabOneSecretInput.vue";
import ColabOneSelectTooltipHost from "./ColabOneSelectTooltipHost.vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneToastHost from "./ColabOneToastHost.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import ColabOneUpdateModal from "./ColabOneUpdateModal.vue";
import ColabOneWebConsoleCard from "./ColabOneWebConsoleCard.vue";
import ColabOneWorkflowShareModal from "./ColabOneWorkflowShareModal.vue";
import {
  buildColabOnePrivacyContext,
  colabOneMaskPlainText,
  colabOnePrivacyActive,
  colabOnePrivacyDefault,
  extractColabOneHostFromInput,
  isColabOnePublicHost,
  setColabOnePrivacyDefault,
  toggleColabOnePrivacyActive,
} from "./colabOnePrivacy";
import { colabOneExtractHfToken } from "./colabOneSecretToken";
import {
  PhAppWindow,
  PhArrowSquareOut,
  PhArrowsClockwise,
  PhCaretDown,
  PhCaretRight,
  PhCaretUp,
  PhCheck,
  PhClock,
  PhCloud,
  PhCloudArrowDown,
  PhCopy,
  PhDesktop,
  PhDownloadSimple,
  PhEye,
  PhEyeSlash,
  PhFlowArrow,
  PhGearSix,
  PhGlobe,
  PhHardDrives,
  PhKey,
  PhMoon,
  PhPackage,
  PhPlay,
  PhPlus,
  PhQuestion,
  PhShieldCheck,
  PhSignOut,
  PhSlidersHorizontal,
  PhStop,
  PhSun,
  PhTerminalWindow,
  PhTray,
  PhUploadSimple,
  PhUserCircle,
} from "@phosphor-icons/vue";
import rawAppIcon from "../../console/resources/icon.svg?raw";
import {
  COLABONE_APP_VERSION,
  COLABONE_APP_VERSION_LABEL,
} from "./colabOneVersion";
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
import BillingBar from "./BillingBar.vue";
import BillingCard from "./BillingCard.vue";
import CuUsageChart from "./CuUsageChart.vue";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import LogDrawer from "./LogDrawer.vue";
import ResourceMonitor from "./ResourceMonitor.vue";
import FlagIcon from "./FlagIcon.vue";
import { locationFull, locationShort, locationTooltip } from "./location";
import { resolveColabOneMonitorFallback } from "./telemetry";
import {
  COLABONE_HIDDEN_POLL_MS,
  COLABONE_VISIBLE_POLL_MS,
  COLABONE_WEB_VISIBLE_POLL_MS,
  createColabOneVisibilityPoller,
} from "./colabOnePoller";
import {
  COLABONE_IDLE_RELEASE_OPTIONS,
  COLABONE_RELEASE_SELECT_CU_LIMIT,
  COLABONE_RELEASE_SELECT_NEVER,
  describeColabOneBilling,
  describeColabOneRunConsumption,
  encodeColabOneReleaseSelectValue,
  formatColabOneCompactRunDuration,
  formatColabOneCULimitValue,
  formatCountdownClock,
  formatIdlePolicyLabel,
  friendlyErrorMessage,
  isTransientIpcError,
  parseColabOneReleaseSelectValue,
  stripColabOneErrorPrefixes,
} from "./billing";
import {
  formatColabOneDuration,
  resolveColabOneTaskElapsedSeconds,
} from "./colabOneDuration";
import {
  COLABONE_WEB_AUTH_REQUIRED_EVENT,
  colabOneCancelUpdateInstall,
  colabOneCheckForUpdate,
  colabOneCommand,
  colabOneCreateOutputShare,
  colabOneCreateWebConsoleLoginTicket,
  colabOneCreateWorkflowShare,
  colabOneDeleteOutputShare,
  colabOneDeleteWorkflowShare,
  colabOneGetCloseBehavior,
  colabOneGetLogDrawerState,
  colabOneGetOutputSharesState,
  colabOneGetUpdateState,
  colabOneGetWebConsolePasscode,
  colabOneGetWebConsoleState,
  colabOneGetWorkflowSharesState,
  colabOneListRollbackVersions,
  colabOneOnCloseBehavior,
  colabOneOnCloseRequest,
  colabOneOnOutputSharesState,
  colabOneOnUpdateOpen,
  colabOneApplyMigrationPreview,
  colabOneCancelMigrationSession,
  colabOneDecryptMigrationPreview,
  colabOneExportMigrationFile,
  colabOneGetMigrationExportSummary,
  colabOneOnUpdateState,
  colabOneOnWebConsoleState,
  colabOneOnWorkflowSharesState,
  colabOneOpenURL,
  colabOnePickMigrationFile,
  colabOneReadClipboard,
  colabOneRelaunchForUpdate,
  colabOneResolveCloseRequest,
  colabOneRevokeOutputShare,
  colabOneRevokeWebConsoleSessions,
  colabOneRevokeWorkflowShare,
  colabOneRuntimeCapabilities,
  colabOneSetCloseBehavior,
  colabOneSetLogDrawer,
  colabOneSetUpdatePreferences,
  colabOneSetWebConsolePasscode,
  colabOneSkipUpdateVersion,
  colabOneSnapshotRaw,
  colabOneStartUpdateInstall,
  colabOneStartUpdateRollback,
  colabOneUpdateWebConsole,
  colabOneWebLogout,
  colabOneWebOpenDeferredTab,
  colabOneWebSessionExpired,
  colabOneWriteClipboard,
  createColabOneDefaultOutputShareListState,
  createColabOneDefaultWebConsoleState,
  createColabOneDefaultWorkflowShareListState,
  fetchColabOneWebCapabilities,
  isColabOneDesktopRuntime,
  normalizeColabOneCloseAction,
  normalizeColabOneCloseBehavior,
  normalizeColabOneOutputShareListState,
  normalizeColabOneWorkflowShareListState,
  validateColabOneWebExternalURL,
  type ColabOneCloseAction,
  type ColabOneCloseBehaviorState,
  type ColabOneMigrationExportSummary,
  type ColabOneMigrationPickResult,
  type ColabOneMigrationPreview,
  type ColabOneOutputShareCreateRequest,
  type ColabOneOutputShareListState,
  type ColabOneOutputShareSummary,
  type ColabOneRollbackVersion,
  type ColabOneUpdateState,
  type ColabOneWebConsoleState,
  type ColabOneWebConsoleUpdateRequest,
  type ColabOneWorkflowShareCreateRequest,
  type ColabOneWorkflowShareListState,
  type ColabOneWorkflowShareSummary,
} from "./api";
import {
  applyColabOneMigrationUIPrefs,
  collectColabOneMigrationUIPrefs,
  type ColabOneMigrationModuleId,
} from "./colabOneMigration";
import {
  COLABONE_RELEASES_URL,
  createColabOneUpdateState,
  hasColabOneActiveUpdatePill,
  hasColabOneAvailableUpdateDot,
  normalizeColabOneRollbackVersions,
  normalizeColabOneUpdateState,
  normalizeColabOneUpdateVersion,
  shouldColabOneAutoCheckOnPopoverOpen,
} from "./colabOneUpdater";
import {
  COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS,
  COLABONE_DEFAULT_GPU,
  COLABONE_DEFAULT_RELEASE_CU_LIMIT,
  COLABONE_DEFAULT_RELEASE_MODE,
  COLABONE_MAX_ASSIGN_MAX_ATTEMPTS,
  COLABONE_MAX_RELEASE_CU_LIMIT,
  COLABONE_MIN_ASSIGN_MAX_ATTEMPTS,
  COLABONE_MIN_RELEASE_CU_LIMIT,
  createColabOneSnapshot,
  COLABONE_STATUS_LABELS,
  evaluateColabOneRuntimeUpgrade,
  type AccountInstance,
  type ColabOneReleaseMode,
  type ColabOneSnapshot,
} from "./types";

const ASYNC_PANEL_MAX_RETRIES = 2;
const ASYNC_PANEL_RETRY_BASE_MS = 200;
const pendingAsyncPanelRetryTimers = new Set<ReturnType<typeof setTimeout>>();

type ColabOneAsyncPanelComponent<T extends Component = Component> = T & {
  __asyncLoader?: () => Promise<unknown>;
  __asyncResolved?: unknown;
};

const ColabOneAsyncPanelLoading = defineComponent({
  name: "ColabOneAsyncPanelLoading",
  inheritAttrs: false,
  setup() {
    return () =>
      h(
        "div",
        {
          class: "co-async-panel-shell",
          role: "status",
          "aria-busy": "true",
          "aria-label": t("common.loading"),
        },
        [
          h("div", { class: "co-async-panel-head" }, [
            h(ColabOneSkeleton, { width: 164, height: 24, variant: "metric" }),
            h(ColabOneSkeleton, {
              width: "46%",
              height: 14,
              variant: "text",
              tone: "subtle",
              delay: 60,
            }),
          ]),
          h(
            ACard,
            { class: "co-async-panel-card", bordered: true },
            () => [
              h("div", { class: "co-async-panel-card-body" }, [
                h(ColabOneSkeleton, {
                  width: "32%",
                  height: 18,
                  variant: "text",
                }),
                h(ColabOneSkeleton, {
                  width: "82%",
                  height: 14,
                  variant: "text",
                  tone: "subtle",
                  delay: 80,
                }),
                h(ColabOneSkeleton, {
                  width: "100%",
                  height: 76,
                  variant: "rect",
                  tone: "subtle",
                  delay: 140,
                }),
              ]),
            ],
          ),
        ],
      );
  },
});

function defineColabOneAsyncPanel<
  T extends Component = { new (): ComponentPublicInstance },
>(
  loader: AsyncComponentLoader<T>,
  onLoadFail?: () => void,
  onRetry?: () => void,
): ColabOneAsyncPanelComponent<T> {
  let autoRetries = 0;

  const errorComponent = defineComponent({
    name: "ColabOneAsyncPanelError",
    inheritAttrs: false,
    props: ["error"],
    setup(props) {
      onLoadFail?.();
      return () => {
        const rawErr: unknown = props.error;
        const detail =
          rawErr instanceof Error
            ? formatError(rawErr.message)
            : rawErr
              ? formatError(rawErr)
              : "";
        return h("div", { class: "co-async-panel-shell" }, [
          h(
            ACard,
            { class: "co-async-panel-error-card", bordered: true },
            () => [
              h(
                "div",
                { class: "co-async-panel-error-body", role: "alert" },
                [
                  h(
                    "div",
                    { class: "co-async-panel-error-title" },
                    t("status.failed"),
                  ),
                  detail
                    ? h("p", { class: "co-async-panel-error-desc" }, detail)
                    : null,
                  h(
                    AButton,
                    {
                      type: "primary",
                      onClick: () => {
                        autoRetries = 0;
                        onRetry?.();
                      },
                    },
                    () => t("common.retry"),
                  ),
                ],
              ),
            ],
          ),
        ]);
      };
    },
  });

  return defineAsyncComponent<T>({
    loader: () =>
      loader().then((mod) => {
        autoRetries = 0;
        return mod;
      }),
    delay: 120,
    loadingComponent: ColabOneAsyncPanelLoading,
    errorComponent,
    onError(_err, retry, fail) {
      if (stopped) {
        autoRetries = 0;
        fail();
        return;
      }
      if (autoRetries < ASYNC_PANEL_MAX_RETRIES) {
        autoRetries += 1;
        const delayMs = ASYNC_PANEL_RETRY_BASE_MS * autoRetries;
        const timer = setTimeout(() => {
          pendingAsyncPanelRetryTimers.delete(timer);
          if (stopped) {
            autoRetries = 0;
            fail();
            return;
          }
          retry();
        }, delayMs);
        pendingAsyncPanelRetryTimers.add(timer);
        return;
      }
      autoRetries = 0;
      onLoadFail?.();
      fail();
    },
  }) as ColabOneAsyncPanelComponent<T>;
}

const apiPanelRetryKey = ref(0);
const comfyPanelRetryKey = ref(0);
const loadApiPanel = () => import("./ApiPanel.vue");
const loadComfyPanel = () => import("./ComfyPanel.vue");
const ApiPanel = defineColabOneAsyncPanel(
  loadApiPanel,
  () => {
    modelsDrawerOpen.value = false;
  },
  () => {
    apiPanelRetryKey.value += 1;
  },
);
const ComfyPanel = defineColabOneAsyncPanel(
  loadComfyPanel,
  () => {
    resetComfyDrawerState();
  },
  () => {
    comfyPanelRetryKey.value += 1;
  },
);
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

const state = shallowRef(createColabOneSnapshot()),
  snapshotLoaded = ref(false),
  refreshingAccount = ref(false),
  page = ref("device"),
  ipcError = ref(""),
  ipcDisconnected = ref(false),
  busyCount = ref(0),
  busy = computed(() => busyCount.value > 0),
  savingIdle = ref(false);
const updateState = ref<ColabOneUpdateState>(createColabOneUpdateState());
const updateModalOpen = ref(false);
const checkingUpdate = ref(false);
const savingUpdatePrefs = ref(false);
const rollbackVersions = ref<ColabOneRollbackVersion[]>([]);
const loadingRollbackVersions = ref(false);
const rollbackVersionsLoaded = ref(false);
const rollbackVersionsError = ref("");
const startingRollback = ref(false);
let rollbackLoadSeq = 0;
let unsubUpdateState: (() => void) | null = null;
let unsubUpdateOpen: (() => void) | null = null;
let updateProgressPollTimer: ReturnType<typeof setInterval> | null = null;
let updatePollInFlight = false;
let lastUpdatePushAt = 0;
let updateStateGen = 0;
let ipcFailures = 0;
let lastSnapshotRaw: string | null = null;
let panelsPrefetched = false;
let cancelPanelPrefetch: (() => void) | null = null;

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
const observedRunningComfyTaskIds = new Set<string>();
const shownCompletedComfyTaskIds = new Set<string>();
const seededRuntimeTaskIds = new Set<string>();

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
  if (
    colabOneRuntimeCapabilities.value.runtime === "web" &&
    colabOneWebSessionExpired.value
  ) {
    return "";
  }
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
  lastSnapshotRaw = null;
  if (String(state.value.error ?? "").trim() === text) {
    state.value = { ...state.value, error: "" };
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
  if (
    isWebMode.value &&
    colabOneWebSessionExpired.value &&
    cleaned.toLowerCase() === "unauthorized"
  ) {
    return;
  }
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
  () =>
    [
      state.value.runtime.tasks,
      state.value.runtime.id,
      state.value.runtime.state,
      snapshotLoaded.value,
    ] as const,
  ([tasks, runtimeId, runtimeState, loaded]) => {
    if (!loaded) return;
    const rtId = String(runtimeId ?? "").trim();
    if (!rtId) {
      for (const item of tasks || []) {
        if (item.kind === "comfyui_run" && item.state === "running") {
          observedRunningComfyTaskIds.add(item.id);
        }
        if (item.state === "failed") {
          shownFailedTaskIds.add(item.id);
        }
      }
      return;
    }
    if (!seededRuntimeTaskIds.has(rtId)) {
      const hasTasks = Boolean(tasks && tasks.length > 0);
      if (runtimeState !== "ready" && !hasTasks) {
        return;
      }
      seededRuntimeTaskIds.add(rtId);
      for (const item of tasks || []) {
        if (item.kind === "comfyui_run" && item.state === "running") {
          observedRunningComfyTaskIds.add(item.id);
        }
        if (item.state === "failed") {
          shownFailedTaskIds.add(item.id);
        }
      }
      return;
    }
    if (!tasks?.length) return;
    for (const item of tasks) {
      if (item.kind !== "comfyui_run") continue;
      if (item.state === "running") {
        observedRunningComfyTaskIds.add(item.id);
      } else if (item.state === "failed" || item.state === "cancelled") {
        observedRunningComfyTaskIds.delete(item.id);
      }
    }
    const newlyCompletedComfy = tasks.filter(
      (item) =>
        item.kind === "comfyui_run" &&
        item.state === "completed" &&
        observedRunningComfyTaskIds.has(item.id) &&
        !shownCompletedComfyTaskIds.has(item.id),
    );
    for (const item of newlyCompletedComfy) {
      observedRunningComfyTaskIds.delete(item.id);
      shownCompletedComfyTaskIds.add(item.id);
      while (shownCompletedComfyTaskIds.size > 200) {
        const oldest = shownCompletedComfyTaskIds.values().next().value;
        if (oldest === undefined) break;
        shownCompletedComfyTaskIds.delete(oldest);
      }
      const wf = state.value.workflows.find((w) => w.id === item.model_id);
      const rawName = wf?.name || "";
      let elapsedSec = resolveColabOneTaskElapsedSeconds(
        item,
        state.value.runtime,
        clockNow.value,
        snapshotAt.value,
      );
      if (elapsedSec === null) {
        const hasTaskCreated =
          typeof item.created_at === "number" &&
          Number.isFinite(item.created_at) &&
          item.created_at > 0;
        const matchedOutput = (state.value.comfy_outputs || []).find(
          (o) =>
            o.workflow_id === item.model_id &&
            Number.isFinite(o.elapsed) &&
            (!hasTaskCreated ||
              !Number.isFinite(o.created_at) ||
              o.created_at >= (item.created_at as number) - 5),
        );
        if (matchedOutput) {
          elapsedSec = matchedOutput.elapsed;
        } else if (
          hasTaskCreated &&
          typeof state.value.runtime.idle?.server_time === "number" &&
          Number.isFinite(state.value.runtime.idle.server_time)
        ) {
          elapsedSec = Math.max(
            0,
            state.value.runtime.idle.server_time - (item.created_at as number),
          );
        }
      }
      const capturedElapsed = elapsedSec;
      toastManager.push({
        kind: "success",
        group: "task-completed",
        dedupeKey: `task-completed:${item.id}`,
        durationMs: 6500,
        resolveText: () => {
          const name = rawName ? tb(rawName) : t("comfy.defaultWorkflowName");
          if (capturedElapsed !== null) {
            return t("comfy.noticeRunCompleted", {
              name,
              duration: formatColabOneDuration(capturedElapsed, {
                precision: "auto",
                locale: colabOneLocaleCode.value,
              }),
            });
          }
          return t("comfy.noticeRunCompletedNoDuration", { name });
        },
      });
    }
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
  { immediate: true },
);

const nav = computed(() => [
  { id: "device", label: t("nav.device"), icon: PhHardDrives },
  { id: "api", label: t("nav.api"), icon: PhGlobe },
  { id: "comfyui", label: t("nav.comfy"), icon: PhFlowArrow },
  { id: "settings", label: t("nav.settings"), icon: PhGearSix },
]);

const modelsDrawerOpen = ref(false);
const comfyDrawerState = ref<{
  open: boolean;
  width: number;
  rootSelector: string;
}>({ open: false, width: 0, rootSelector: "" });

function resetComfyDrawerState() {
  comfyDrawerState.value = { open: false, width: 0, rootSelector: "" };
}

function onComfyDrawerState(next: {
  open: boolean;
  width: number;
  rootSelector: string;
}) {
  if (!next.open) {
    resetComfyDrawerState();
    return;
  }
  if (page.value !== "comfyui") return;
  comfyDrawerState.value = {
    open: true,
    width: next.width,
    rootSelector: next.rootSelector,
  };
}
const cuUsageHighlighted = ref(false);
let cuUsageHighlightTimer: ReturnType<typeof setTimeout> | null = null;

function scrollToCuUsage() {
  if (cuUsageHighlightTimer !== null) {
    clearTimeout(cuUsageHighlightTimer);
    cuUsageHighlightTimer = null;
  }
  const section = document.getElementById("co-cu-usage-section");
  const prefersReducedMotion =
    typeof window !== "undefined" &&
    window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
  section?.scrollIntoView({
    behavior: prefersReducedMotion ? "auto" : "smooth",
    block: "nearest",
  });
  cuUsageHighlighted.value = true;
  cuUsageHighlightTimer = setTimeout(() => {
    cuUsageHighlighted.value = false;
    cuUsageHighlightTimer = null;
  }, 1600);
}
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

let suppressNextPageScrollTop = false;

watch(page, (next) => {
  if (next !== "api") modelsDrawerOpen.value = false;
  if (next !== "comfyui") resetComfyDrawerState();
  if (next !== "settings") settingsSaveFloating.value = false;
});
watch(
  page,
  () => {
    if (suppressNextPageScrollTop) {
      suppressNextPageScrollTop = false;
      return;
    }
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

const webConsoleState = ref<ColabOneWebConsoleState>(
  createColabOneDefaultWebConsoleState(),
);

const privacyContext = computed(() => {
  const ctx = buildColabOnePrivacyContext(state.value);
  const extraHosts = [...(ctx.hosts || [])];
  for (const rawOrigin of [
    webConsoleState.value.public_origin,
    capabilities.value.public_origin,
  ]) {
    const webHost = extractColabOneHostFromInput(rawOrigin);
    if (
      webHost &&
      !isColabOnePublicHost(webHost) &&
      !extraHosts.includes(webHost)
    ) {
      extraHosts.push(webHost);
    }
  }
  if (extraHosts.length !== (ctx.hosts || []).length) {
    return { ...ctx, hosts: extraHosts };
  }
  return ctx;
});

function togglePrivacyMask() {
  toggleColabOnePrivacyActive();
}

function onPrivacyDefaultChange(raw: unknown) {
  const next = Boolean(raw);
  setColabOnePrivacyDefault(next);
  notify(
    next
      ? t("settings.privacyMaskDefaultEnabledToast")
      : t("settings.privacyMaskDefaultDisabledToast"),
    "success",
    "setting-privacy-default",
  );
}

const logsOpen = ref(false);
const logWindowStandalone = ref(false);
let savedLogWidth = Number.NaN;
try {
  savedLogWidth = Number(localStorage.getItem(COLABONE_STORAGE_KEYS.logsWidth));
} catch {
  // Ignore restricted storage contexts.
}
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
  try {
    localStorage.setItem(COLABONE_STORAGE_KEYS.logsWidth, String(width));
  } catch {
    // Ignore restricted storage contexts.
  }
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
    clockNow.value / 1000 - state.value.account.checked_at > 60,
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
          comfyui_tts_preview: t("comfy.voicePreviewTitle"),
          live_start: t("task.liveStart"),
          live_build_pack: t("task.liveBuildPack"),
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
const hasActiveComfyEngineTask = computed(
  () =>
    state.value.runtime.state !== "stopped" &&
    Boolean(
      state.value.runtime.tasks?.some(
        (item) =>
          item.state === "running" &&
          (item.kind === "comfyui_start" ||
            item.kind === "comfyui_run" ||
            item.kind === "comfyui_tts_preview" ||
            item.kind === "comfyui"),
      ),
    ),
);
const liveStudioRuntimeBusy = computed(
  () =>
    state.value.runtime.state !== "stopped" &&
    (Boolean(state.value.runtime.live_studio?.running) ||
      state.value.runtime.live_studio?.state === "starting" ||
      Boolean(
        state.value.runtime.tasks?.some(
          (item) =>
            item.state === "running" &&
            (item.kind === "live_start" || item.kind === "live_build_pack"),
        ),
      )),
);
const comfyEngineRuntimeBusy = computed(
  () =>
    state.value.runtime.state !== "stopped" &&
    (Boolean(state.value.runtime.comfyui?.running) ||
      hasActiveComfyEngineTask.value),
);
const comfyRuntimeBusy = computed(
  () => comfyEngineRuntimeBusy.value || liveStudioRuntimeBusy.value,
);

function isNavLive(id: string): boolean {
  if (id === "device") return ready.value;
  if (id === "api") return state.value.runtime.public_api?.state === "ready";
  if (id === "comfyui") return comfyRuntimeBusy.value;
  return false;
}
function navDotColor(id: string): string {
  if (id === "comfyui") return "var(--co-accent)";
  return "var(--co-success)";
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
function formatColabOneTopbarInstanceLabel(inst: AccountInstance): string {
  const parts = [inst.accelerator || "GPU"];
  if (inst.location?.country) {
    const loc = locationShort(inst.location);
    if (loc) parts.push(loc);
  }
  parts.push(inst.id);
  const base = parts.join(" · ");
  return inst.loaded_model ? `${base} (${inst.loaded_model})` : base;
}
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
      comfyui_tts_preview: t("comfy.voicePreviewTitle"),
      live_start: t("task.kindLiveStart"),
      live_build_pack: t("task.kindLiveBuildPack"),
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
    comfyui_tts_preview: t("comfy.voicePreviewTitle"),
    live_start: t("task.liveStart"),
    live_build_pack: t("task.liveBuildPack"),
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
  gpu: COLABONE_DEFAULT_GPU as string,
  idle_minutes: 30,
  idle_countdown_seconds: 60,
  release_mode: COLABONE_DEFAULT_RELEASE_MODE as ColabOneReleaseMode,
  release_cu_limit: COLABONE_DEFAULT_RELEASE_CU_LIMIT as number,
  auto_drive: true,
  download_threads: 16,
  assign_max_attempts: COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS as number,
  assign_proxy_enabled: false,
  assign_proxy: "",
  hf_token: "",
});
const cuLimitDraft = ref<number | undefined>(COLABONE_DEFAULT_RELEASE_CU_LIMIT);
const editingCuLimit = ref(false);
const cuLimitPendingActivation = ref(false);
watch(
  () => form.value.release_cu_limit,
  (val) => {
    if (!editingCuLimit.value && !cuLimitPendingActivation.value) {
      cuLimitDraft.value = val;
    }
  },
);
watch(
  () => form.value.release_mode,
  (mode) => {
    if (mode === "cu_limit") {
      cuLimitPendingActivation.value = false;
    }
  },
);
watch(
  [() => state.value.runtime.state === "ready", () => state.value.setup.colab],
  ([isReady, hasColab]) => {
    if (!isReady || !hasColab) {
      cuLimitPendingActivation.value = false;
    }
  },
);
const assignMaxAttemptsDraft = ref<number | undefined>(
  COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS,
);
const editingAssignMaxAttempts = ref(false);
watch(
  () => form.value.assign_max_attempts,
  (val) => {
    if (!editingAssignMaxAttempts.value) {
      assignMaxAttemptsDraft.value = val;
    }
  },
);
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
const currentRunOverview = computed(() =>
  describeColabOneRunConsumption(state.value.account, {
    setupColab: state.value.setup.colab,
    runtimeState: state.value.runtime.state,
    runtimeId:
      state.value.runtime.state === "stopped"
        ? undefined
        : state.value.runtime.id,
    nowMs: clockNow.value,
  }),
);
const currentRunConsumedCU = computed(() =>
  currentRunOverview.value.active ? currentRunOverview.value.consumed : 0,
);
const activeReleaseSelectValue = computed(() =>
  cuLimitPendingActivation.value
    ? COLABONE_RELEASE_SELECT_CU_LIMIT
    : encodeColabOneReleaseSelectValue(
        form.value.release_mode,
        form.value.idle_minutes,
      ),
);
const showCuLimitEditor = computed(
  () => activeReleaseSelectValue.value === COLABONE_RELEASE_SELECT_CU_LIMIT,
);
const normalizedCuLimitDraft = computed<number | null>(() => {
  const n = Number(cuLimitDraft.value);
  if (
    !Number.isFinite(n) ||
    n < COLABONE_MIN_RELEASE_CU_LIMIT ||
    n > COLABONE_MAX_RELEASE_CU_LIMIT
  ) {
    return null;
  }
  return Math.round(n * 100) / 100;
});
const cuLimitCanApply = computed(() => normalizedCuLimitDraft.value !== null);
const cuLimitNeedsApply = computed(() => {
  const next = normalizedCuLimitDraft.value;
  if (next === null) return false;
  if (cuLimitPendingActivation.value || form.value.release_mode !== "cu_limit") {
    return true;
  }
  return Math.abs(next - form.value.release_cu_limit) > 1e-6;
});
const cuLimitDraftAlreadyReached = computed(
  () =>
    ready.value &&
    normalizedCuLimitDraft.value !== null &&
    currentRunConsumedCU.value >= normalizedCuLimitDraft.value - 1e-6,
);
const cuLimitRemainingCU = computed(() =>
  Math.max(0, form.value.release_cu_limit - currentRunConsumedCU.value),
);
const isCuLimitUrgent = computed(
  () =>
    form.value.release_mode === "cu_limit" &&
    ready.value &&
    form.value.release_cu_limit > 0 &&
    (cuLimitRemainingCU.value <= 0.5 ||
      currentRunConsumedCU.value / form.value.release_cu_limit >= 0.9),
);
const activeDriveFolderName = computed(
  () =>
    state.value.runtime.drive?.remote_folder ||
    (state.value.runtime.drive?.mode === "colab_drivefs"
      ? "ColabOne_Drive_Mount"
      : "ColabOne_Drive"),
);

const code = ref(""),
  showAuthPaste = ref(false);

let stopped = false;
let clockTimer: ReturnType<typeof setInterval>;
let lastInteraction = 0;

const idle = computed(() => state.value.runtime.idle);
const snapshotAt = ref(Date.now());
const activeTaskElapsedLabel = computed(() => {
  const secs = resolveColabOneTaskElapsedSeconds(
    task.value,
    state.value.runtime,
    clockNow.value,
    snapshotAt.value,
  );
  return secs === null
    ? ""
    : formatColabOneDuration(secs, {
        precision: "integer",
        locale: colabOneLocaleCode.value,
      });
});
const dockTaskElapsedSeconds = computed(() =>
  resolveColabOneTaskElapsedSeconds(
    dockTask.value,
    state.value.runtime,
    clockNow.value,
    snapshotAt.value,
  ),
);
const dockTaskElapsedLabel = computed(() => {
  const secs = dockTaskElapsedSeconds.value;
  return secs === null
    ? ""
    : formatColabOneDuration(secs, {
        precision: "integer",
        locale: colabOneLocaleCode.value,
      });
});
const dockTaskCompactElapsedLabel = computed(() => {
  const secs = dockTaskElapsedSeconds.value;
  return secs === null
    ? ""
    : formatColabOneCompactRunDuration(secs, colabOneLocaleCode.value);
});
const COLABONE_COMPACT_DOCK_BREAKPOINT = 680;
const isCompactTaskDockViewport = ref(
  viewportWidth.value <= COLABONE_COMPACT_DOCK_BREAKPOINT,
);
let compactTaskDockMql: MediaQueryList | null = null;
const onCompactTaskDockMqlChange = (event: MediaQueryListEvent) => {
  isCompactTaskDockViewport.value = event.matches;
};
if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
  compactTaskDockMql = window.matchMedia(
    `(max-width: ${COLABONE_COMPACT_DOCK_BREAKPOINT}px)`,
  );
  isCompactTaskDockViewport.value = compactTaskDockMql.matches;
  if (typeof compactTaskDockMql.addEventListener === "function") {
    compactTaskDockMql.addEventListener("change", onCompactTaskDockMqlChange);
  } else if (typeof compactTaskDockMql.addListener === "function") {
    compactTaskDockMql.addListener(onCompactTaskDockMqlChange);
  }
} else {
  watch(
    () => viewportWidth.value <= COLABONE_COMPACT_DOCK_BREAKPOINT,
    (isCompact) => {
      isCompactTaskDockViewport.value = isCompact;
    },
  );
}
const dockTaskKey = computed(() => {
  const current = dockTask.value;
  if (!current) {
    return "";
  }
  return current.id || `${current.kind}:${current.created_at ?? ""}`;
});
const dockTaskHasProgress = computed(() => {
  const kind = dockTask.value?.kind;
  return (
    kind === "download" ||
    kind === "comfyui_start" ||
    kind === "comfyui_run" ||
    kind === "comfyui_tts_preview" ||
    kind === "live_start" ||
    kind === "live_build_pack"
  );
});
const dockTaskPercent = computed(() => {
  const raw = dockTask.value?.progress;
  if (typeof raw !== "number" || !Number.isFinite(raw)) {
    return 0;
  }
  return Math.max(0, Math.min(100, Math.round(raw * 100)));
});
const dockTaskMessageText = computed(() => {
  const raw = dockTask.value?.message ? tb(dockTask.value.message).trim() : "";
  if (!raw) return "";
  return colabOnePrivacyActive.value
    ? colabOneMaskPlainText(raw, privacyContext.value)
    : raw;
});
const dockTaskMiniSecondary = computed(() => {
  const dl = dockTask.value?.download;
  if (dl) {
    if (dl.phase === "verifying") {
      return t("device.downloadVerifying");
    }
    if (typeof dl.speed_bps === "number" && Number.isFinite(dl.speed_bps) && dl.speed_bps > 0) {
      return `${(dl.speed_bps / 1024 ** 2).toFixed(1)} MiB/s`;
    }
    if (
      typeof dl.total_bytes === "number" &&
      Number.isFinite(dl.total_bytes) &&
      dl.total_bytes > 0
    ) {
      const downloaded =
        typeof dl.downloaded_bytes === "number" &&
        Number.isFinite(dl.downloaded_bytes)
          ? Math.max(0, dl.downloaded_bytes)
          : 0;
      return `${bytes(downloaded)} / ${bytes(dl.total_bytes)}`;
    }
  }
  return dockTaskCompactElapsedLabel.value || "";
});
const dockTaskCompactSummary = computed(() => {
  if (!dockTask.value) {
    return "";
  }
  const title = taskBarTitle(dockTask.value.kind);
  const msg = dockTaskMessageText.value;
  const base = msg ? `${title} · ${msg}` : title;
  return dockTaskHasProgress.value ? `${base} (${dockTaskPercent.value}%)` : base;
});
const dockTaskCollapsed = ref(false);
const dockTaskCollapseOverrideKey = ref<string | null>(null);
const dockTaskExpandBtnRef = ref<HTMLButtonElement | null>(null);
const dockTaskCollapseBtnRef = ref<ComponentPublicInstance | null>(null);

function setDockTaskCollapsed(collapsed: boolean) {
  const activeEl =
    typeof document !== "undefined"
      ? (document.activeElement as HTMLElement | null)
      : null;
  let restoreFocus = false;
  if (activeEl && activeEl.closest(".co-task-dock")) {
    try {
      restoreFocus = activeEl.matches(":focus-visible");
    } catch {
      restoreFocus = false;
    }
  }
  dockTaskCollapsed.value = collapsed;
  dockTaskCollapseOverrideKey.value = dockTaskKey.value || null;
  if (restoreFocus) {
    void nextTick(() => {
      const target = collapsed
        ? dockTaskExpandBtnRef.value
        : ((dockTaskCollapseBtnRef.value?.$el as HTMLElement | null) ?? null);
      if (typeof target?.focus === "function") {
        target.focus({ preventScroll: true });
      }
    });
  }
}

watch(
  dockTaskKey,
  (nextKey, prevKey) => {
    if (!nextKey) {
      dockTaskCollapsed.value = false;
      dockTaskCollapseOverrideKey.value = null;
      return;
    }
    if (nextKey !== prevKey) {
      dockTaskCollapseOverrideKey.value = null;
      dockTaskCollapsed.value = isCompactTaskDockViewport.value;
    }
  },
  { immediate: true },
);

watch(isCompactTaskDockViewport, (isCompact) => {
  const key = dockTaskKey.value;
  if (!key || dockTaskCollapseOverrideKey.value === key) {
    return;
  }
  dockTaskCollapsed.value = isCompact;
});
const idleRemaining = computed(() => {
  const baseRemaining = idle.value?.remaining_seconds;
  if (typeof baseRemaining !== "number" || !Number.isFinite(baseRemaining)) {
    return 0;
  }
  const elapsedSec = Math.max(0, (clockNow.value - snapshotAt.value) / 1000);
  return Math.max(0, Math.ceil(baseRemaining - elapsedSec));
});
const countdown = computed(() => {
  if (!idle.value || idle.value.phase === "active") return false;
  const rawThreshold = idle.value.countdown_seconds;
  const threshold =
    typeof rawThreshold === "number" &&
    Number.isFinite(rawThreshold) &&
    rawThreshold > 0
      ? rawThreshold
      : 60;
  return idleRemaining.value <= threshold;
});
const duration = (seconds: number | null) => {
  if (seconds === null || !Number.isFinite(seconds)) {
    return t("common.calculating");
  }
  return formatCountdownClock(Math.ceil(Math.max(0, seconds)));
};

function interaction(event?: Event) {
  if (event && !event.isTrusted) return;
  if (!ready.value || Date.now() - lastInteraction < 5000) return;
  lastInteraction = Date.now();
  colabOneCommand("runtime.activity").catch(() => {});
}

function hasVisibleAntModalOverlay(minZIndex = -Infinity): boolean {
  if (typeof document === "undefined") return false;
  const modals = document.querySelectorAll<HTMLElement>(".ant-modal-wrap");
  for (const el of modals) {
    if (/\bant-[\w-]+-leave(?:-active|-to)?\b/.test(el.className)) {
      continue;
    }
    const dialog = el.querySelector<HTMLElement>(".ant-modal");
    if (
      dialog &&
      /\bant-[\w-]+-leave(?:-active|-to)?\b/.test(dialog.className)
    ) {
      continue;
    }
    const style = window.getComputedStyle(el);
    if (
      style.display !== "none" &&
      style.visibility !== "hidden" &&
      style.pointerEvents !== "none" &&
      el.getBoundingClientRect().height > 0
    ) {
      if (minZIndex > -Infinity) {
        const z = Number.parseInt(style.zIndex || "0", 10);
        if (!Number.isFinite(z) || z < minZIndex) {
          continue;
        }
      }
      return true;
    }
  }
  return false;
}

function hasVisibleAntPopoverOverlay(): boolean {
  if (typeof document === "undefined") return false;
  const overlays = document.querySelectorAll<HTMLElement>(
    ".ant-popover:not(.ant-popover-hidden), .ant-popconfirm:not(.ant-popover-hidden), .ant-dropdown:not(.ant-dropdown-hidden)",
  );
  for (const el of overlays) {
    if (/\bant-[\w-]+-leave(?:-active|-to)?\b/.test(el.className)) {
      continue;
    }
    const style = window.getComputedStyle(el);
    if (
      style.display !== "none" &&
      style.visibility !== "hidden" &&
      style.pointerEvents !== "none" &&
      el.getBoundingClientRect().height > 0
    ) {
      return true;
    }
  }
  return false;
}

function handleGlobalKeydown(e: KeyboardEvent) {
  if (e.key === "Escape") {
    if (closeModalOpen.value) {
      return;
    }
    if (updateModalOpen.value && !hasVisibleAntModalOverlay(1050)) {
      handleUpdatePopoverOpenChange(false);
      return;
    }
    if (
      showRelease.value &&
      !busy.value &&
      !showUpgradeConfirm.value &&
      !workflowShareModalOpen.value &&
      !migrationModalOpen.value &&
      !hasVisibleAntModalOverlay(1001) &&
      !hasVisibleAntPopoverOverlay()
    ) {
      showRelease.value = false;
    } else if (
      !activeOverlayDrawer.value.open &&
      !hasVisibleAntModalOverlay() &&
      !hasVisibleAntPopoverOverlay() &&
      !logWindowStandalone.value &&
      logsOpen.value
    ) {
      void setLogsOpen(false);
    }
    return;
  }
  if (
    (e.ctrlKey || e.metaKey) &&
    e.shiftKey &&
    !e.altKey &&
    (e.code === "KeyM" || e.key.toLowerCase() === "m")
  ) {
    if (closeModalOpen.value || e.repeat) {
      return;
    }
    e.preventDefault();
    togglePrivacyMask();
    return;
  }
  if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "j") {
    if (closeModalOpen.value) {
      return;
    }
    e.preventDefault();
    void setLogsOpen(!logsOpen.value);
    return;
  }
  if (
    e.altKey &&
    !e.ctrlKey &&
    !e.metaKey &&
    !e.shiftKey &&
    !activeOverlayDrawer.value.open &&
    !showRelease.value &&
    !updateModalOpen.value &&
    !closeModalOpen.value &&
    !hasVisibleAntModalOverlay()
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
const SYNCED_SETTING_KEYS = [
  "gpu",
  "idle_minutes",
  "idle_countdown_seconds",
  "release_mode",
  "release_cu_limit",
  "auto_drive",
  "download_threads",
  "assign_max_attempts",
  "assign_proxy_enabled",
] as const;
type SyncedSettingKey = (typeof SYNCED_SETTING_KEYS)[number];
const pendingSettings = new Map<SyncedSettingKey, number>();

function beginPendingSetting(keys: readonly SyncedSettingKey[]) {
  for (const key of keys) {
    pendingSettings.set(key, (pendingSettings.get(key) ?? 0) + 1);
  }
}

function endPendingSetting(
  keys: readonly SyncedSettingKey[],
  reconcile = false,
) {
  const settings = state.value.settings;
  let released = false;
  for (const key of keys) {
    const next = (pendingSettings.get(key) ?? 0) - 1;
    if (next <= 0) {
      pendingSettings.delete(key);
      released = true;
      if (
        reconcile &&
        snapshotLoaded.value &&
        settings &&
        settings[key] !== undefined &&
        settings[key] !== lastSyncedSettings[key]
      ) {
        (form.value as Record<string, unknown>)[key] = settings[key];
        (lastSyncedSettings as Record<string, unknown>)[key] = settings[key];
      }
    } else {
      pendingSettings.set(key, next);
    }
  }
  if (released) {
    lastSnapshotRaw = null;
  }
}

function syncFormFromSettings(next: typeof state.value.settings | undefined) {
  if (!next) return;
  if (!initialized) {
    for (const key of SYNCED_SETTING_KEYS) {
      if (next[key] !== undefined && !pendingSettings.has(key)) {
        (form.value as Record<string, unknown>)[key] = next[key];
        (lastSyncedSettings as Record<string, unknown>)[key] = next[key];
      }
    }
    initialized = true;
    return;
  }
  for (const key of SYNCED_SETTING_KEYS) {
    if (pendingSettings.has(key)) continue;
    if (next[key] !== undefined && next[key] !== lastSyncedSettings[key]) {
      (form.value as Record<string, unknown>)[key] = next[key];
      (lastSyncedSettings as Record<string, unknown>)[key] = next[key];
    }
  }
}

const bytes = (n = 0) => {
  const safe = typeof n === "number" && Number.isFinite(n) && n > 0 ? n : 0;
  return safe >= 1024 ** 3
    ? (safe / 1024 ** 3).toFixed(1) + " GB"
    : (safe / 1024 ** 2).toFixed(0) + " MB";
};

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

function prefetchAsyncPanel<T extends Component>(
  panel: ColabOneAsyncPanelComponent<T>,
  loader: AsyncComponentLoader<T>,
) {
  if (panel.__asyncResolved) return;
  void loader()
    .then(() => {
      if (stopped || panel.__asyncResolved) return;
      return panel.__asyncLoader?.();
    })
    .catch(() => {});
}

function scheduleSecondaryPanelPrefetch() {
  if (panelsPrefetched || stopped || typeof window === "undefined") return;
  panelsPrefetched = true;
  const run = () => {
    cancelPanelPrefetch = null;
    if (stopped) return;
    prefetchAsyncPanel(ApiPanel, loadApiPanel);
    prefetchAsyncPanel(ComfyPanel, loadComfyPanel);
  };
  if (typeof window.requestIdleCallback === "function") {
    const id = window.requestIdleCallback(run, { timeout: 1500 });
    cancelPanelPrefetch = () => window.cancelIdleCallback(id);
  } else {
    const id = setTimeout(run, 400);
    cancelPanelPrefetch = () => clearTimeout(id);
  }
}

async function syncSnapshot() {
  if (isWebMode.value && colabOneWebSessionExpired.value) return;
  const snapId = ++snapshotSeq;
  const raw = await colabOneSnapshotRaw();
  if (stopped || snapId < appliedSnapshotSeq) return;
  if (isWebMode.value && colabOneWebSessionExpired.value) return;
  const unchanged =
    snapshotLoaded.value &&
    !clearingStateError &&
    pendingSettings.size === 0 &&
    lastSnapshotRaw !== null &&
    raw === lastSnapshotRaw;
  let value: ColabOneSnapshot | null = null;
  if (!unchanged) {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      throw new Error("Invalid snapshot payload");
    }
    value = parsed as ColabOneSnapshot;
  }
  appliedSnapshotSeq = snapId;
  const wasDisconnected = ipcDisconnected.value;
  ipcFailures = 0;
  ipcDisconnected.value = false;
  if (ipcError.value) ipcError.value = "";
  toastManager.dismissGroup("ipc-transient");
  if (wasDisconnected) clockNow.value = Date.now();
  if (unchanged || !value) {
    return;
  }
  let mutatedLocally = false;
  const rawError = String(value.error ?? "").trim();
  if (clearingStateError) {
    if (rawError === clearingStateError.text) {
      if (
        clearingStateError.completedAtSeq === null ||
        snapId <= clearingStateError.completedAtSeq
      ) {
        value.error = "";
        mutatedLocally = true;
      } else {
        // Keep handledStateError intact so an error the user already dismissed
        // never zombie-repops while the backend still reports the same string,
        // while releasing clearingStateError so raw-snapshot caching resumes.
        clearingStateError = null;
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
    mutatedLocally = true;
    colabOneCommand("error.clear", { expected: staleErr }).catch(() => {});
  }
  const prevRunningTask = state.value.runtime.tasks?.find(
    (item) => item.state === "running",
  );
  const nextRunningTask = value.runtime.tasks?.find(
    (item) => item.state === "running",
  );
  if (
    value.runtime.idle?.server_time !== state.value.runtime.idle?.server_time ||
    value.operation?.retry_at !== state.value.operation?.retry_at ||
    nextRunningTask?.id !== prevRunningTask?.id ||
    nextRunningTask?.elapsed_seconds !== prevRunningTask?.elapsed_seconds ||
    nextRunningTask?.download?.elapsed_seconds !==
      prevRunningTask?.download?.elapsed_seconds
  ) {
    snapshotAt.value = Date.now();
    clockNow.value = Date.now();
  }
  lastSnapshotRaw = mutatedLocally || pendingSettings.size > 0 ? null : raw;
  state.value = value;
  snapshotLoaded.value = true;
  syncFormFromSettings(value.settings);
  hydrateColabOneLocaleFromSettings(value.settings?.locale);
  scheduleSecondaryPanelPrefetch();
}

async function syncSnapshotQuietly(): Promise<boolean> {
  try {
    await syncSnapshot();
    return !stopped;
  } catch {
    return false;
  }
}

const snapshotPoller = createColabOneVisibilityPoller({
  visibleMs: isColabOneDesktopRuntime()
    ? COLABONE_VISIBLE_POLL_MS
    : COLABONE_WEB_VISIBLE_POLL_MS,
  hiddenMs: COLABONE_HIDDEN_POLL_MS,
  tick: async () => {
    try {
      await syncSnapshot();
    } catch (e) {
      if (isWebMode.value && colabOneWebSessionExpired.value) {
        ipcFailures = 0;
        ipcDisconnected.value = false;
        ipcError.value = "";
        return;
      }
      ipcFailures += 1;
      if (ipcFailures >= 2) {
        ipcDisconnected.value = true;
        ipcError.value = String(e ?? "");
      }
    }
  },
  onVisible: () => {
    if (!ipcDisconnected.value) {
      clockNow.value = Date.now();
    }
  },
});

async function act(
  action: string,
  payload: Record<string, unknown> = {},
  options: { skipSync?: boolean } = {},
) {
  busyCount.value += 1;
  try {
    const res = await colabOneCommand(action, payload);
    clearActionErrors();
    if (!options.skipSync) {
      await syncSnapshotQuietly();
    }
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
    const total = Number(
      op.total_attempts ||
        form.value.assign_max_attempts ||
        COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS,
    );
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

function onCuLimitInput(value: unknown) {
  if (value === null || value === undefined || value === "") {
    cuLimitDraft.value = undefined;
    return;
  }
  const parsed = Number(value);
  if (Number.isFinite(parsed)) {
    cuLimitDraft.value = parsed;
  }
}

async function persistReleasePolicy(options: {
  mode: ColabOneReleaseMode;
  idleMinutes?: number;
  cuLimit?: number;
}) {
  const prevMode = form.value.release_mode;
  const prevIdle = form.value.idle_minutes;
  const prevLimit = form.value.release_cu_limit;

  const nextMode = options.mode;
  const nextIdle = options.idleMinutes ?? prevIdle;
  const nextLimit =
    options.cuLimit !== undefined
      ? Math.round(options.cuLimit * 100) / 100
      : prevLimit;

  const payload: Record<string, unknown> = { release_mode: nextMode };
  const keys: SyncedSettingKey[] = ["release_mode"];
  if (nextMode === "idle") {
    payload.idle_minutes = nextIdle;
    keys.push("idle_minutes");
  } else if (nextMode === "cu_limit") {
    payload.release_cu_limit = nextLimit;
    keys.push("release_cu_limit");
  }

  cuLimitPendingActivation.value = false;
  form.value.release_mode = nextMode;
  lastSyncedSettings.release_mode = nextMode;
  if (nextMode === "idle") {
    form.value.idle_minutes = nextIdle;
    lastSyncedSettings.idle_minutes = nextIdle;
  } else if (nextMode === "cu_limit") {
    form.value.release_cu_limit = nextLimit;
    lastSyncedSettings.release_cu_limit = nextLimit;
    cuLimitDraft.value = nextLimit;
  }

  savingIdle.value = true;
  beginPendingSetting(keys);
  try {
    await colabOneCommand("settings.save", payload);
    clearActionErrors();
    const policyLabel = formatIdlePolicyLabel(nextIdle, nextMode, nextLimit);
    notify(
      nextMode === "idle"
        ? t("device.toastIdleSet", { policy: policyLabel })
        : t("device.toastReleaseModeSet", { policy: policyLabel }),
      "success",
      "setting-idle-policy",
    );
  } catch (e) {
    if (form.value.release_mode === nextMode) {
      form.value.release_mode = prevMode;
    }
    if (lastSyncedSettings.release_mode === nextMode) {
      lastSyncedSettings.release_mode = prevMode;
    }
    if (nextMode === "idle") {
      if (form.value.idle_minutes === nextIdle) {
        form.value.idle_minutes = prevIdle;
      }
      if (lastSyncedSettings.idle_minutes === nextIdle) {
        lastSyncedSettings.idle_minutes = prevIdle;
      }
    } else if (nextMode === "cu_limit") {
      if (form.value.release_cu_limit === nextLimit) {
        form.value.release_cu_limit = prevLimit;
      }
      if (lastSyncedSettings.release_cu_limit === nextLimit) {
        lastSyncedSettings.release_cu_limit = prevLimit;
      }
      cuLimitDraft.value = prevLimit;
    }
    showError(e);
  } finally {
    const synced = await syncSnapshotQuietly();
    endPendingSetting(keys, synced);
    savingIdle.value = false;
  }
}

async function changeReleasePolicy(rawVal: unknown) {
  if (savingIdle.value) return;
  const parsed = parseColabOneReleaseSelectValue(rawVal);
  if (!parsed) return;
  if (parsed.mode === "idle") {
    await persistReleasePolicy({
      mode: "idle",
      idleMinutes: parsed.idleMinutes,
    });
    return;
  }
  if (parsed.mode === "never") {
    await persistReleasePolicy({ mode: "never" });
    return;
  }
  const savedLimit =
    Number.isFinite(form.value.release_cu_limit) &&
    form.value.release_cu_limit >= COLABONE_MIN_RELEASE_CU_LIMIT
      ? form.value.release_cu_limit
      : COLABONE_DEFAULT_RELEASE_CU_LIMIT;
  if (ready.value && currentRunConsumedCU.value >= savedLimit - 1e-6) {
    cuLimitPendingActivation.value = true;
    cuLimitDraft.value = Math.min(
      COLABONE_MAX_RELEASE_CU_LIMIT,
      Math.max(savedLimit, Math.ceil(currentRunConsumedCU.value + 5)),
    );
    notify(
      t("device.toastCuLimitConfirmNeeded", {
        consumed: currentRunConsumedCU.value.toFixed(2),
      }),
      "info",
      "setting-cu-limit-confirm",
    );
    return;
  }
  await persistReleasePolicy({ mode: "cu_limit", cuLimit: savedLimit });
}

async function commitReleaseCuLimit() {
  if (savingIdle.value) return;
  const next = normalizedCuLimitDraft.value;
  if (next === null) {
    showError(t("device.errInvalidCuLimit"));
    return;
  }
  editingCuLimit.value = false;
  await persistReleasePolicy({ mode: "cu_limit", cuLimit: next });
}

async function changeIdleMinutes(rawMinutes: unknown) {
  await changeReleasePolicy(rawMinutes);
}

let savingDownloadThreads = false;
let queuedDownloadThreads: number | null = null;

async function changeDownloadThreads(rawThreads: unknown) {
  const threads = Number(rawThreads);
  if (threads !== 16 && threads !== 32) return;
  if (savingDownloadThreads) {
    queuedDownloadThreads = threads;
    form.value.download_threads = threads;
    lastSyncedSettings.download_threads = threads;
    return;
  }
  let settled = form.value.download_threads;
  form.value.download_threads = threads;
  lastSyncedSettings.download_threads = threads;
  savingDownloadThreads = true;
  queuedDownloadThreads = threads;
  beginPendingSetting(["download_threads"]);
  let synced = false;
  try {
    while (!stopped && queuedDownloadThreads !== null) {
      let lastSucceeded: number | null = null;
      while (!stopped && queuedDownloadThreads !== null) {
        const next = queuedDownloadThreads;
        queuedDownloadThreads = null;
        if (next === settled && lastSucceeded === next) continue;
        try {
          await colabOneCommand("settings.save", { download_threads: next });
          settled = next;
          lastSucceeded = next;
          clearActionErrors();
        } catch (e) {
          lastSucceeded = null;
          if (queuedDownloadThreads === null) {
            if (form.value.download_threads === next) {
              form.value.download_threads = settled;
            }
            if (lastSyncedSettings.download_threads === next) {
              lastSyncedSettings.download_threads = settled;
            }
            showError(e);
          }
        }
      }
      if (!stopped && lastSucceeded !== null) {
        notify(
          t("device.toastThreadsSet", { threads: lastSucceeded }),
          "success",
          "setting-download-threads",
        );
      }
      synced = await syncSnapshotQuietly();
    }
  } finally {
    queuedDownloadThreads = null;
    savingDownloadThreads = false;
    endPendingSetting(["download_threads"], synced);
  }
}

function normalizeAssignMaxAttempts(raw: unknown): number | null {
  if (raw === null || raw === undefined || raw === "") return null;
  const num = Number(raw);
  if (!Number.isFinite(num)) return null;
  const rounded = Math.round(num);
  return Math.min(
    COLABONE_MAX_ASSIGN_MAX_ATTEMPTS,
    Math.max(COLABONE_MIN_ASSIGN_MAX_ATTEMPTS, rounded),
  );
}

function onAssignMaxAttemptsInput(raw: unknown) {
  if (raw === null || raw === undefined || raw === "") {
    assignMaxAttemptsDraft.value = undefined;
    return;
  }
  const num = Number(raw);
  if (Number.isFinite(num)) {
    assignMaxAttemptsDraft.value = num;
  }
}

function onAssignMaxAttemptsStep(raw: unknown) {
  const next = normalizeAssignMaxAttempts(raw);
  if (next === null) return;
  assignMaxAttemptsDraft.value = next;
  if (next !== form.value.assign_max_attempts) {
    void changeAssignMaxAttempts(next);
  }
}

function commitAssignMaxAttempts() {
  const next = normalizeAssignMaxAttempts(assignMaxAttemptsDraft.value);
  if (next === null) {
    assignMaxAttemptsDraft.value = form.value.assign_max_attempts;
    return;
  }
  assignMaxAttemptsDraft.value = next;
  if (next !== form.value.assign_max_attempts) {
    void changeAssignMaxAttempts(next);
  }
}

function onAssignMaxAttemptsBlur() {
  editingAssignMaxAttempts.value = false;
  commitAssignMaxAttempts();
}

let savingAssignMaxAttempts = false;
let queuedAssignMaxAttempts: number | null = null;

async function changeAssignMaxAttempts(rawAttempts: unknown) {
  const attempts = normalizeAssignMaxAttempts(rawAttempts);
  if (attempts === null) return;
  if (savingAssignMaxAttempts) {
    queuedAssignMaxAttempts = attempts;
    form.value.assign_max_attempts = attempts;
    assignMaxAttemptsDraft.value = attempts;
    lastSyncedSettings.assign_max_attempts = attempts;
    return;
  }
  let settled = form.value.assign_max_attempts;
  form.value.assign_max_attempts = attempts;
  assignMaxAttemptsDraft.value = attempts;
  lastSyncedSettings.assign_max_attempts = attempts;
  savingAssignMaxAttempts = true;
  queuedAssignMaxAttempts = attempts;
  beginPendingSetting(["assign_max_attempts"]);
  let synced = false;
  let lastSucceeded: number | null = null;
  try {
    while (!stopped && queuedAssignMaxAttempts !== null) {
      let batchSucceeded: number | null = null;
      while (!stopped && queuedAssignMaxAttempts !== null) {
        const next = queuedAssignMaxAttempts;
        queuedAssignMaxAttempts = null;
        if (next === settled && lastSucceeded === next) continue;
        try {
          await colabOneCommand("settings.save", { assign_max_attempts: next });
          settled = next;
          lastSucceeded = next;
          batchSucceeded = next;
          clearActionErrors();
        } catch (e) {
          lastSucceeded = null;
          batchSucceeded = null;
          if (queuedAssignMaxAttempts === null) {
            if (form.value.assign_max_attempts === next) {
              form.value.assign_max_attempts = settled;
            }
            if (
              !editingAssignMaxAttempts.value ||
              assignMaxAttemptsDraft.value === next
            ) {
              assignMaxAttemptsDraft.value = settled;
            }
            if (lastSyncedSettings.assign_max_attempts === next) {
              lastSyncedSettings.assign_max_attempts = settled;
            }
            showError(e);
          }
        }
      }
      if (!stopped && batchSucceeded !== null) {
        notify(
          t("device.toastAssignMaxAttemptsSet", { count: batchSucceeded }),
          "success",
          "setting-assign-max-attempts",
        );
      }
      synced = await syncSnapshotQuietly();
    }
  } finally {
    queuedAssignMaxAttempts = null;
    savingAssignMaxAttempts = false;
    endPendingSetting(["assign_max_attempts"], synced);
  }
}

function onGpuSelect(raw: unknown) {
  if (raw !== undefined && raw !== null) {
    form.value.gpu = String(raw);
  }
}

let savingDefaultGpu = false;
let queuedDefaultGpu: string | null = null;

async function changeDefaultGpu(raw: unknown) {
  if (raw === undefined || raw === null) return;
  const gpu = String(raw);
  if (savingDefaultGpu) {
    queuedDefaultGpu = gpu;
    form.value.gpu = gpu;
    lastSyncedSettings.gpu = gpu;
    return;
  }
  let settled = form.value.gpu;
  form.value.gpu = gpu;
  lastSyncedSettings.gpu = gpu;
  savingDefaultGpu = true;
  queuedDefaultGpu = gpu;
  beginPendingSetting(["gpu"]);
  let synced = false;
  try {
    while (!stopped && queuedDefaultGpu !== null) {
      let lastSucceeded: string | null = null;
      while (!stopped && queuedDefaultGpu !== null) {
        const next = queuedDefaultGpu;
        queuedDefaultGpu = null;
        if (next === settled && lastSucceeded === next) continue;
        try {
          await colabOneCommand("settings.save", { gpu: next });
          settled = next;
          lastSucceeded = next;
          clearActionErrors();
        } catch (e) {
          lastSucceeded = null;
          if (queuedDefaultGpu === null) {
            if (form.value.gpu === next) {
              form.value.gpu = settled;
            }
            if (lastSyncedSettings.gpu === next) {
              lastSyncedSettings.gpu = settled;
            }
            showError(e);
          }
        }
      }
      if (!stopped && lastSucceeded !== null) {
        notify(
          t("device.toastDefaultGpuSet", { gpu: lastSucceeded }),
          "success",
          "setting-default-gpu",
        );
      }
      synced = await syncSnapshotQuietly();
    }
  } finally {
    queuedDefaultGpu = null;
    savingDefaultGpu = false;
    endPendingSetting(["gpu"], synced);
  }
}

async function onAutoDriveChange(raw: unknown) {
  const next = Boolean(raw);
  const previous = form.value.auto_drive;
  form.value.auto_drive = next;
  lastSyncedSettings.auto_drive = next;
  beginPendingSetting(["auto_drive"]);
  try {
    const res = await act(
      "settings.save",
      { auto_drive: next },
      { skipSync: true },
    );
    if (res) {
      notify(t("device.toastSettingsSaved"), "success", "setting-auto-drive");
    } else {
      if (form.value.auto_drive === next) {
        form.value.auto_drive = previous;
      }
      if (lastSyncedSettings.auto_drive === next) {
        lastSyncedSettings.auto_drive = previous;
      }
    }
  } finally {
    const synced = await syncSnapshotQuietly();
    endPendingSetting(["auto_drive"], synced);
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
  if (!capabilities.value.can_loopback_oauth) {
    if (page.value !== "settings") {
      selectPage("settings");
    }
    await manualLogin();
    return;
  }
  await act("auth.colab.auto");
}
async function manualLogin() {
  if (!isColabOneDesktopRuntime()) {
    let result: { url?: string } | null = null;
    try {
      await colabOneWebOpenDeferredTab(async () => {
        result = await act("auth.colab.begin");
        if (!result) {
          return "";
        }
        showAuthPaste.value = true;
        return typeof result.url === "string" ? result.url : "";
      }, (rawUrl) => {
        if (!rawUrl || rawUrl === openedAuth) {
          throw new Error("__colabone_skip_open__");
        }
        const safeUrl = validateColabOneWebExternalURL(rawUrl);
        openedAuth = rawUrl;
        return safeUrl;
      });
    } catch (e) {
      if (!(e instanceof Error && e.message === "__colabone_skip_open__")) {
        showError(e);
      }
    }
    return;
  }
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
    if (!capabilities.value.can_loopback_oauth) {
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
  assignProxySettings.value?.masked_url && !colabOnePrivacyActive.value
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
    const res = await act("settings.save", payload, { skipSync: true });
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
      if (form.value.assign_proxy_enabled === next) {
        form.value.assign_proxy_enabled = previous;
      }
      if (lastSyncedSettings.assign_proxy_enabled === next) {
        lastSyncedSettings.assign_proxy_enabled = previous;
      }
    }
  } finally {
    const synced = await syncSnapshotQuietly();
    endPendingSetting(["assign_proxy_enabled"], synced);
    togglingAssignProxy.value = false;
  }
}

async function saveAssignProxy() {
  if (assignProxyMutating.value || testingAssignProxy.value) return;
  const raw = form.value.assign_proxy.trim();
  if (!raw) return;
  savingAssignProxy.value = true;
  beginPendingSetting(["assign_proxy_enabled"]);
  try {
    const res = await act(
      "settings.save",
      {
        assign_proxy: raw,
        assign_proxy_enabled: true,
      },
      { skipSync: true },
    );
    if (res) {
      form.value.assign_proxy = "";
      form.value.assign_proxy_enabled = true;
      lastSyncedSettings.assign_proxy_enabled = true;
      assignProxyExpanded.value = false;
      assignProxyInputRef.value?.hide();
      notify(t("settings.assignProxySavedToast"), "success", "setting-assign-proxy-save");
    }
  } finally {
    const synced = await syncSnapshotQuietly();
    endPendingSetting(["assign_proxy_enabled"], synced);
    savingAssignProxy.value = false;
  }
}

async function clearAssignProxy() {
  if (assignProxyMutating.value || testingAssignProxy.value) return;
  clearingAssignProxy.value = true;
  beginPendingSetting(["assign_proxy_enabled"]);
  try {
    const res = await act(
      "settings.save",
      { clear_assign_proxy: true },
      { skipSync: true },
    );
    if (res) {
      form.value.assign_proxy = "";
      form.value.assign_proxy_enabled = false;
      lastSyncedSettings.assign_proxy_enabled = false;
      assignProxyExpanded.value = false;
      assignProxyInputRef.value?.hide();
      notify(t("settings.assignProxyClearedToast"), "success", "setting-assign-proxy-clear");
    }
  } finally {
    const synced = await syncSnapshotQuietly();
    endPendingSetting(["assign_proxy_enabled"], synced);
    clearingAssignProxy.value = false;
  }
}

const canTestAssignProxy = computed(
  () =>
    capabilities.value.can_reveal_host_secrets ||
    capabilities.value.can_test_saved_assign_proxy,
);

const assignProxyTestNeedsSaveFirst = computed(
  () =>
    !capabilities.value.can_reveal_host_secrets &&
    Boolean(form.value.assign_proxy.trim()),
);

const assignProxyTestDisabled = computed(
  () =>
    busy.value ||
    assignProxyMutating.value ||
    (capabilities.value.can_reveal_host_secrets
      ? !form.value.assign_proxy.trim() && !hasSavedAssignProxy.value
      : !hasSavedAssignProxy.value || Boolean(form.value.assign_proxy.trim())),
);

async function testAssignProxy() {
  if (!canTestAssignProxy.value) return;
  if (assignProxyMutating.value || testingAssignProxy.value) return;
  const raw = form.value.assign_proxy.trim();
  if (!capabilities.value.can_reveal_host_secrets) {
    if (raw || !hasSavedAssignProxy.value) return;
  } else if (!raw && !hasSavedAssignProxy.value) {
    return;
  }
  testingAssignProxy.value = true;
  try {
    const res = await colabOneCommand(
      "settings.assign_proxy.test",
      capabilities.value.can_reveal_host_secrets && raw
        ? { assign_proxy: raw }
        : {},
    );
    if (stopped) return;
    const ms = Number(res?.latency_ms || 0);
    notify(
      t("settings.assignProxyTestOkToast", { ms: ms > 0 ? ms : 1 }),
      "success",
      "setting-assign-proxy-test",
    );
    await syncSnapshot().catch(() => {});
  } catch (err) {
    if (!stopped && !(isWebMode.value && colabOneWebSessionExpired.value)) {
      showError(err);
    }
  } finally {
    if (!stopped) testingAssignProxy.value = false;
  }
}

function onHfTokenExtracted() {
  notify(t("settings.hfTokenExtractedToast"), "success");
}

async function save() {
  const prevAttempts = form.value.assign_max_attempts;
  const normalizedAttempts = normalizeAssignMaxAttempts(
    assignMaxAttemptsDraft.value,
  );
  if (normalizedAttempts !== null) {
    form.value.assign_max_attempts = normalizedAttempts;
    assignMaxAttemptsDraft.value = normalizedAttempts;
  } else {
    assignMaxAttemptsDraft.value = form.value.assign_max_attempts;
  }
  const prevReleaseMode = form.value.release_mode;
  const prevReleaseCuLimit = form.value.release_cu_limit;
  const prevCuLimitDraft = cuLimitDraft.value;
  const prevCuLimitPending = cuLimitPendingActivation.value;
  let effectiveReleaseMode = form.value.release_mode;
  let effectiveCuLimit = form.value.release_cu_limit;
  if (showCuLimitEditor.value) {
    const parsedCu = normalizedCuLimitDraft.value;
    if (parsedCu === null) {
      showError(t("device.errInvalidCuLimit"));
      return;
    }
    effectiveReleaseMode = "cu_limit";
    effectiveCuLimit = parsedCu;
    form.value.release_mode = "cu_limit";
    form.value.release_cu_limit = parsedCu;
    cuLimitDraft.value = parsedCu;
    cuLimitPendingActivation.value = false;
  }
  const payload: Record<string, unknown> = {
    gpu: form.value.gpu,
    idle_minutes: form.value.idle_minutes,
    release_mode: effectiveReleaseMode,
    release_cu_limit: effectiveCuLimit,
    auto_drive: form.value.auto_drive,
    download_threads: form.value.download_threads,
    assign_max_attempts: form.value.assign_max_attempts,
  };
  if (form.value.hf_token.trim()) {
    payload.hf_token = form.value.hf_token.trim();
  }
  const pendingFields: SyncedSettingKey[] = [
    "gpu",
    "idle_minutes",
    "release_mode",
    "release_cu_limit",
    "auto_drive",
    "download_threads",
    "assign_max_attempts",
  ];
  beginPendingSetting(pendingFields);
  let ok = false;
  try {
    ok = Boolean(await act("settings.save", payload));
    if (ok) {
      lastSyncedSettings = {
        ...lastSyncedSettings,
        gpu: form.value.gpu,
        idle_minutes: form.value.idle_minutes,
        release_mode: effectiveReleaseMode,
        release_cu_limit: effectiveCuLimit,
        auto_drive: form.value.auto_drive,
        download_threads: form.value.download_threads,
        assign_max_attempts: form.value.assign_max_attempts,
      };
      form.value.hf_token = "";
      hfTokenInputRef.value?.hide();
      notify(t("device.toastSettingsSaved"));
    } else {
      form.value.assign_max_attempts = prevAttempts;
      assignMaxAttemptsDraft.value = prevAttempts;
      form.value.release_mode = prevReleaseMode;
      form.value.release_cu_limit = prevReleaseCuLimit;
      cuLimitDraft.value = prevCuLimitDraft;
      cuLimitPendingActivation.value = prevCuLimitPending;
    }
  } finally {
    endPendingSetting(pendingFields, ok);
  }
}

function resolveReleaseSettingsForSubmit(): {
  release_mode: ColabOneReleaseMode;
  release_cu_limit: number;
  rollback: () => void;
} | null {
  const prevReleaseMode = form.value.release_mode;
  const prevReleaseCuLimit = form.value.release_cu_limit;
  const prevCuLimitDraft = cuLimitDraft.value;
  const prevCuLimitPending = cuLimitPendingActivation.value;
  const rollback = () => {
    form.value.release_mode = prevReleaseMode;
    form.value.release_cu_limit = prevReleaseCuLimit;
    cuLimitDraft.value = prevCuLimitDraft;
    cuLimitPendingActivation.value = prevCuLimitPending;
  };
  if (!showCuLimitEditor.value) {
    return {
      release_mode: form.value.release_mode,
      release_cu_limit: form.value.release_cu_limit,
      rollback,
    };
  }
  const parsedCu = normalizedCuLimitDraft.value;
  if (parsedCu === null) {
    showError(t("device.errInvalidCuLimit"));
    return null;
  }
  form.value.release_mode = "cu_limit";
  form.value.release_cu_limit = parsedCu;
  cuLimitDraft.value = parsedCu;
  cuLimitPendingActivation.value = false;
  return {
    release_mode: "cu_limit",
    release_cu_limit: parsedCu,
    rollback,
  };
}

async function start() {
  const release = resolveReleaseSettingsForSubmit();
  if (!release) return;
  if (
    await act("settings.save", {
      gpu: form.value.gpu,
      idle_minutes: form.value.idle_minutes,
      release_mode: release.release_mode,
      release_cu_limit: release.release_cu_limit,
      auto_drive: form.value.auto_drive,
    })
  ) {
    lastSyncedSettings.release_mode = release.release_mode;
    lastSyncedSettings.release_cu_limit = release.release_cu_limit;
    await act("runtime.start");
  } else {
    release.rollback();
  }
}

async function startNew() {
  const release = resolveReleaseSettingsForSubmit();
  if (!release) return;
  if (
    await act("settings.save", {
      gpu: form.value.gpu,
      idle_minutes: form.value.idle_minutes,
      release_mode: release.release_mode,
      release_cu_limit: release.release_cu_limit,
      auto_drive: form.value.auto_drive,
    })
  ) {
    lastSyncedSettings.release_mode = release.release_mode;
    lastSyncedSettings.release_cu_limit = release.release_cu_limit;
    await act("runtime.start", { new: true, gpu: form.value.gpu });
  } else {
    release.rollback();
  }
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

const activeOverlayDrawer = computed(() => {
  if (modelsDrawerOpen.value) {
    return {
      open: true,
      width: undefined as number | undefined,
      rootSelector: ".co-model-drawer-root",
    };
  }
  if (page.value === "comfyui" && comfyDrawerState.value.open) {
    return {
      open: true,
      width:
        comfyDrawerState.value.width > 0
          ? comfyDrawerState.value.width
          : undefined,
      rootSelector: comfyDrawerState.value.rootSelector,
    };
  }
  return {
    open: false,
    width: undefined as number | undefined,
    rootSelector: "",
  };
});

const isMobileLogOverlayOpen = computed(
  () => !logWindowStandalone.value && logsOpen.value && viewportWidth.value <= 768,
);

const activeToastTopOffsetPx = computed(() =>
  activeOverlayDrawer.value.open
    ? drawerToastTopOffsetPx.value
    : isMobileLogOverlayOpen.value
      ? 60
      : toastTopOffsetPx.value,
);

const toastHorizontalBounds = computed(() =>
  resolveColabOneToastHorizontalBounds({
    viewportWidth: viewportWidth.value,
    modelDrawerOpen: activeOverlayDrawer.value.open,
    modelDrawerWidthPx: activeOverlayDrawer.value.width,
    inlineLogDrawerOpen:
      !logWindowStandalone.value && logsOpen.value && !isMobileLogOverlayOpen.value,
    inlineLogDrawerWidthPx: logDrawerWidth.value,
  }),
);

function updateDrawerToastTopOffset() {
  const { open, rootSelector } = activeOverlayDrawer.value;
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

function onApiPanelMounted() {
  if (modelsDrawerOpen.value) {
    scheduleDrawerToastTopMeasure();
  }
}

function updateToastLayout() {
  if (typeof window !== "undefined") {
    viewportWidth.value = window.innerWidth;
  }
  const el = resolveContentElement();
  if (el) {
    const top = Math.round(el.getBoundingClientRect().top);
    if (top > 0) {
      const maxTop =
        typeof window !== "undefined" && window.innerHeight > 0
          ? Math.max(96, Math.round(window.innerHeight * 0.55))
          : 260;
      toastTopOffsetPx.value = Math.min(maxTop, Math.max(72, top + 6));
    }
  }
  if (activeOverlayDrawer.value.open) {
    updateDrawerToastTopOffset();
  }
}

watch(
  [
    persistentBannerError,
    colabOneWebSessionExpired,
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
  () =>
    [
      activeOverlayDrawer.value.open,
      activeOverlayDrawer.value.rootSelector,
      activeOverlayDrawer.value.width,
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

function stopUpdateProgressPoll() {
  if (updateProgressPollTimer !== null) {
    clearInterval(updateProgressPollTimer);
    updateProgressPollTimer = null;
  }
}

function applyUpdateState(
  raw: Partial<ColabOneUpdateState> | null | undefined,
  fromPush = false,
  expectedGen?: number,
) {
  if (expectedGen !== undefined && expectedGen !== updateStateGen) {
    return;
  }
  updateStateGen += 1;
  if (fromPush) {
    lastUpdatePushAt = Date.now();
  }
  const prevIncludePre = updateState.value.include_prerelease;
  const normalized = normalizeColabOneUpdateState(raw);
  updateState.value = normalized;
  if (normalized.include_prerelease !== prevIncludePre) {
    rollbackLoadSeq += 1;
    loadingRollbackVersions.value = false;
    rollbackVersions.value = normalized.rollback_versions;
    rollbackVersionsLoaded.value = normalized.rollback_versions.length > 0;
    rollbackVersionsError.value = "";
  } else if (normalized.rollback_versions.length > 0) {
    rollbackVersions.value = normalized.rollback_versions;
    rollbackVersionsLoaded.value = true;
    rollbackVersionsError.value = "";
  }
  const inFlight =
    normalized.status === "downloading" || normalized.status === "installing";
  if (inFlight) {
    if (updateProgressPollTimer === null && !stopped) {
      updateProgressPollTimer = setInterval(() => {
        if (stopped) {
          stopUpdateProgressPoll();
          return;
        }
        // Skip redundant polling when native Go push events are actively arriving.
        if (updatePollInFlight || Date.now() - lastUpdatePushAt < 1000) {
          return;
        }
        updatePollInFlight = true;
        const pollGen = updateStateGen;
        void colabOneGetUpdateState()
          .then((next) => {
            if (!stopped) applyUpdateState(next, false, pollGen);
          })
          .catch(() => {})
          .finally(() => {
            updatePollInFlight = false;
          });
      }, 350);
    }
  } else {
    stopUpdateProgressPoll();
  }
}

const isCheckingUpdate = computed(
  () => checkingUpdate.value || updateState.value.status === "checking",
);

const showNavUpdateDot = computed(() =>
  hasColabOneAvailableUpdateDot(updateState.value),
);

const showNavUpdatePill = computed(() =>
  hasColabOneActiveUpdatePill(updateState.value),
);

const navVersionTooltip = computed(() => {
  const st = updateState.value;
  if (st.status === "ready_to_restart") {
    return t(
      st.is_rollback
        ? "update.navVersionTooltipRollbackRestart"
        : "update.navVersionTooltipRestart",
      {
        version: st.available_version || COLABONE_APP_VERSION,
      },
    );
  }
  if (st.status === "downloading" || st.status === "installing") {
    return t(
      st.is_rollback
        ? "update.navVersionTooltipRollbackDownloading"
        : "update.navVersionTooltipDownloading",
      {
        version: st.available_version || COLABONE_APP_VERSION,
        percent: Math.round(st.progress_percent),
      },
    );
  }
  if (st.status === "available" && st.available_version) {
    return t("update.navVersionTooltipAvailable", {
      version: st.available_version,
    });
  }
  return t("update.navVersionTooltipDefault", { version: appVersionLabel });
});

const navUpdateBadgeLabel = computed(() => {
  const st = updateState.value;
  if (!showNavUpdatePill.value) return "";
  if (st.status === "ready_to_restart") {
    return t(
      st.is_rollback ? "update.navRollbackBadge" : "update.navRestartBadge",
    );
  }
  if (st.status === "downloading" || st.status === "installing") {
    return t(
      st.is_rollback
        ? "update.navRollingBackBadge"
        : "update.navDownloadingBadge",
      {
        percent: Math.round(st.progress_percent),
      },
    );
  }
  return t("update.navUpdateBadge");
});

const updateStatusTag = computed<{ label: string; color?: string } | null>(
  () => {
    const st = updateState.value;
    if (st.status === "ready_to_restart") {
      return { label: t("update.statusReadyToRestart"), color: "success" };
    }
    if (st.status === "downloading") {
      return {
        label: t("update.statusDownloading", {
          percent: Math.round(st.progress_percent),
        }),
        color: "processing",
      };
    }
    if (st.status === "installing") {
      return { label: t("update.statusInstalling"), color: "processing" };
    }
    if (isCheckingUpdate.value) {
      return { label: t("update.statusChecking"), color: "processing" };
    }
    if (st.status === "available" && st.available_version) {
      return {
        label: t("update.statusAvailable", { version: st.available_version }),
        color: "blue",
      };
    }
    if (st.status === "error") {
      return { label: t("update.statusError"), color: "error" };
    }
    if (st.status === "disabled") {
      if (st.disabled_reason === "read_only_install") {
        return { label: t("update.statusReadOnly"), color: "warning" };
      }
      return { label: t("update.statusDevBuild") };
    }
    if (st.status === "up_to_date") {
      return { label: t("update.statusUpToDate"), color: "success" };
    }
    if (st.disabled_reason === "read_only_install") {
      return { label: t("update.statusReadOnly"), color: "warning" };
    }
    return null;
  },
);

const showUpdateReadOnlySecondaryTag = computed(
  () =>
    updateState.value.disabled_reason === "read_only_install" &&
    updateState.value.status !== "disabled" &&
    updateState.value.status !== "idle",
);

const updateLastCheckedText = computed(() => {
  const ts = updateState.value.last_checked_at;
  if (!ts || !Number.isFinite(ts) || ts <= 0) {
    return t("update.lastCheckedNever");
  }
  const ms = ts > 1e12 ? ts : ts * 1000;
  try {
    const formatted = new Intl.DateTimeFormat(colabOneLocaleCode.value, {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    }).format(new Date(ms));
    return t("update.lastCheckedAt", { time: formatted });
  } catch {
    return t("update.lastCheckedAt", { time: new Date(ms).toLocaleString() });
  }
});

async function handleCheckForUpdate(openModalOnResult = false) {
  if (checkingUpdate.value) return;
  checkingUpdate.value = true;
  try {
    const next = await colabOneCheckForUpdate(true);
    applyUpdateState(next);
    const status = updateState.value.status;
    if (
      status === "available" ||
      status === "ready_to_restart" ||
      status === "error" ||
      openModalOnResult
    ) {
      updateModalOpen.value = true;
    } else if (status === "up_to_date" && !updateModalOpen.value) {
      notify(
        t("update.toastUpToDate", {
          version: updateState.value.current_version || COLABONE_APP_VERSION,
        }),
        "success",
        "update-up-to-date",
      );
    }
  } catch (e) {
    const pollGen = updateStateGen;
    try {
      const latest = await colabOneGetUpdateState();
      applyUpdateState(latest, false, pollGen);
    } catch {
      // Ignore secondary state refresh error.
    }
    if (updateState.value.status !== "error") {
      applyUpdateState({
        ...updateState.value,
        status: "error",
        error_code: updateState.value.error_code || "unknown",
        error_message:
          normalizeErrorKey(e) || updateState.value.error_message,
      });
    }
    updateModalOpen.value = true;
  } finally {
    checkingUpdate.value = false;
  }
}

async function handleStartUpdateInstall() {
  try {
    const next = await colabOneStartUpdateInstall();
    applyUpdateState(next);
  } catch (e) {
    const pollGen = updateStateGen;
    try {
      const latest = await colabOneGetUpdateState();
      applyUpdateState(latest, false, pollGen);
    } catch {
      // Ignore secondary state refresh error.
    }
    showError(e);
  }
}

async function handleLoadRollbackVersions(forceRefresh = false) {
  if (!forceRefresh && (loadingRollbackVersions.value || rollbackVersionsLoaded.value)) {
    return;
  }
  const seq = ++rollbackLoadSeq;
  loadingRollbackVersions.value = true;
  rollbackVersionsError.value = "";
  try {
    const list = await colabOneListRollbackVersions(forceRefresh);
    if (stopped || seq !== rollbackLoadSeq) return;
    rollbackVersions.value = normalizeColabOneRollbackVersions(list);
    rollbackVersionsLoaded.value = true;
  } catch (e) {
    if (stopped || seq !== rollbackLoadSeq) return;
    rollbackVersionsError.value =
      normalizeErrorKey(e) || t("update.rollbackLoadError");
  } finally {
    if (!stopped && seq === rollbackLoadSeq) {
      loadingRollbackVersions.value = false;
    }
  }
}

async function handleStartUpdateRollback(version: string) {
  const targetVersion = normalizeColabOneUpdateVersion(version);
  if (!targetVersion || startingRollback.value) return;
  startingRollback.value = true;
  try {
    const next = await colabOneStartUpdateRollback(targetVersion);
    if (!stopped) applyUpdateState(next);
  } catch (e) {
    const pollGen = updateStateGen;
    try {
      const latest = await colabOneGetUpdateState();
      if (!stopped) applyUpdateState(latest, false, pollGen);
    } catch {
      // Ignore secondary state refresh error.
    }
    if (!stopped) showError(e);
  } finally {
    startingRollback.value = false;
  }
}

async function handleCancelUpdateInstall() {
  try {
    const next = await colabOneCancelUpdateInstall();
    applyUpdateState(next);
  } catch (e) {
    showError(e);
  }
}

async function handleRelaunchForUpdate() {
  if (state.value.operation) return;
  try {
    await colabOneRelaunchForUpdate();
  } catch (e) {
    showError(e);
  }
}

async function handleSkipUpdateVersion(version?: string) {
  const targetVersion = String(
    version || updateState.value.available_version || "",
  ).trim();
  if (!targetVersion) return;
  try {
    const next = await colabOneSkipUpdateVersion(targetVersion);
    applyUpdateState(next);
    updateModalOpen.value = false;
    notify(
      t("update.toastSkipped", { version: targetVersion }),
      "info",
      "update-skipped",
    );
  } catch (e) {
    showError(e);
  }
}

async function handleToggleUpdatePref(
  field: "auto_check" | "include_prerelease",
  value: boolean,
) {
  if (savingUpdatePrefs.value) return;
  const nextAuto =
    field === "auto_check" ? value : updateState.value.auto_check;
  const nextPre =
    field === "include_prerelease"
      ? value
      : updateState.value.include_prerelease;
  savingUpdatePrefs.value = true;
  try {
    const next = await colabOneSetUpdatePreferences(nextAuto, nextPre);
    if (field === "include_prerelease") {
      rollbackLoadSeq += 1;
      loadingRollbackVersions.value = false;
      rollbackVersionsLoaded.value = false;
      rollbackVersions.value = [];
      rollbackVersionsError.value = "";
    }
    applyUpdateState(next);
    notify(t("update.toastPrefsSaved"), "success", "update-prefs-saved");
  } catch (e) {
    showError(e);
  } finally {
    savingUpdatePrefs.value = false;
  }
}

async function handleOpenReleasePage(url?: string) {
  await openExternalURL(
    url || updateState.value.release_url || COLABONE_RELEASES_URL,
  );
}

function handleUpdatePopoverOpenChange(next: boolean) {
  if (!next && updateState.value.status === "installing") {
    return;
  }
  const wasOpen = updateModalOpen.value;
  updateModalOpen.value = next;
  if (
    next &&
    !wasOpen &&
    shouldColabOneAutoCheckOnPopoverOpen(
      updateState.value,
      isCheckingUpdate.value,
    )
  ) {
    void handleCheckForUpdate(false);
  }
}

function openUpdatePopover() {
  handleUpdatePopoverOpenChange(true);
}

const closeBehaviorState = ref<ColabOneCloseBehaviorState>({
  behavior: "ask",
  default_choice: "quit",
  can_background: true,
});
const savingCloseBehavior = ref(false);
const closeModalOpen = ref(false);
const closeModalDefaultChoice = ref<ColabOneCloseAction>("quit");
const closeModalSubmitting = ref(false);
const closeModalSuppressLeave = ref(false);
let unsubCloseRequest: (() => void) | null = null;
let unsubCloseBehavior: (() => void) | null = null;

const closeBehaviorSegmentedOptions = computed(() => [
  {
    value: "ask",
    title: t("settings.closeBehaviorAsk"),
    payload: { label: t("settings.closeBehaviorAsk") },
  },
  {
    value: "quit",
    title: t("settings.closeBehaviorQuit"),
    payload: { label: t("settings.closeBehaviorQuit") },
  },
  {
    value: "background",
    title: t("settings.closeBehaviorBackground"),
    disabled: !closeBehaviorState.value.can_background,
    payload: { label: t("settings.closeBehaviorBackground") },
  },
]);

const hasActiveCloseBlockingOp = computed(
  () =>
    Boolean(state.value.operation) ||
    updateState.value.status === "downloading" ||
    updateState.value.status === "installing",
);

function applyCloseBehaviorState(raw: ColabOneCloseBehaviorState) {
  closeBehaviorState.value = {
    behavior: normalizeColabOneCloseBehavior(raw?.behavior),
    default_choice: normalizeColabOneCloseAction(raw?.default_choice),
    can_background:
      typeof raw?.can_background === "boolean" ? raw.can_background : true,
  };
}

async function handleChangeCloseBehavior(raw: unknown) {
  if (raw !== "ask" && raw !== "quit" && raw !== "background") {
    return;
  }
  const next = raw;
  if (
    savingCloseBehavior.value ||
    next === closeBehaviorState.value.behavior
  ) {
    return;
  }
  if (next === "background" && !closeBehaviorState.value.can_background) {
    return;
  }
  const prev = closeBehaviorState.value.behavior;
  closeBehaviorState.value = {
    ...closeBehaviorState.value,
    behavior: next,
  };
  savingCloseBehavior.value = true;
  try {
    const saved = await colabOneSetCloseBehavior(next);
    applyCloseBehaviorState(saved);
    const toastKey =
      saved.behavior === "quit"
        ? "settings.closeBehaviorToastQuit"
        : saved.behavior === "background"
          ? "settings.closeBehaviorToastBackground"
          : "settings.closeBehaviorToastAsk";
    notify(t(toastKey), "success", "setting-close-behavior");
  } catch (e) {
    closeBehaviorState.value = {
      ...closeBehaviorState.value,
      behavior: prev,
    };
    showError(e);
  } finally {
    savingCloseBehavior.value = false;
  }
}

async function handleConfirmCloseModal(payload: {
  action: ColabOneCloseAction;
  remember: boolean;
}) {
  if (closeModalSubmitting.value) return;
  closeModalSubmitting.value = true;
  const isBackground = payload.action === "background";
  if (isBackground) {
    closeModalSuppressLeave.value = true;
    closeModalOpen.value = false;
    await nextTick();
  }
  try {
    await colabOneResolveCloseRequest(payload.action, payload.remember);
    if (payload.remember) {
      closeBehaviorState.value = {
        ...closeBehaviorState.value,
        behavior: payload.action,
      };
    }
    if (!isBackground) {
      closeModalOpen.value = false;
    }
  } catch (e) {
    if (isBackground) {
      closeModalSuppressLeave.value = false;
      closeModalOpen.value = true;
    }
    showError(e);
  } finally {
    closeModalSubmitting.value = false;
    closeModalSuppressLeave.value = false;
  }
}

// ---------------------------------------------------------------------------
// One-click Migration (Encrypted Export / Import)
// ---------------------------------------------------------------------------

const migrationModalOpen = ref(false);
const migrationModalMode = ref<"export" | "import">("export");
const migrationExportSummary = ref<ColabOneMigrationExportSummary | null>(null);
const migrationExportLoading = ref(false);
const migrationExportSubmitting = ref(false);
const migrationExportError = ref("");

const migrationImportStep = ref<"decrypt" | "preview">("decrypt");
const migrationPickedFile = ref<ColabOneMigrationPickResult | null>(null);
const migrationImportPreview = ref<ColabOneMigrationPreview | null>(null);
const migrationPickingFile = ref(false);
const migrationImportDecrypting = ref(false);
const migrationImportApplying = ref(false);
const migrationImportError = ref("");

const migrationUiSyncKey = ref(0);
const migrationApiSyncKey = ref(0);
const migrationComfySyncKey = ref(0);
let migrationExportReqSeq = 0;

function formatMigrationError(err: unknown): string {
  const raw = err instanceof Error ? err.message : String(err ?? "");
  return friendlyErrorMessage(raw);
}

function currentMigrationSessionId(): string {
  return (
    migrationImportPreview.value?.preview_id ||
    migrationPickedFile.value?.file_token ||
    ""
  );
}

function resetMigrationModalState(cancelRemoteSession = true) {
  migrationExportReqSeq += 1;
  const sessionId = currentMigrationSessionId();
  migrationExportLoading.value = false;
  migrationExportError.value = "";
  migrationImportError.value = "";
  migrationPickedFile.value = null;
  migrationImportPreview.value = null;
  migrationImportStep.value = "decrypt";
  if (cancelRemoteSession && sessionId) {
    void colabOneCancelMigrationSession(sessionId).catch(() => {});
  }
}

function handleMigrationModalOpenChange(nextOpen: boolean) {
  migrationModalOpen.value = nextOpen;
  if (!nextOpen) {
    resetMigrationModalState(true);
  }
}

async function openMigrationExport() {
  if (migrationExportSubmitting.value) return;
  resetMigrationModalState(true);
  const seq = ++migrationExportReqSeq;
  migrationModalMode.value = "export";
  migrationExportSummary.value = null;
  migrationExportError.value = "";
  migrationModalOpen.value = true;
  migrationExportLoading.value = true;
  try {
    const uiPrefs = collectColabOneMigrationUIPrefs(
      window.localStorage,
      colabOneAppearance.value,
    );
    const summary = await colabOneGetMigrationExportSummary(uiPrefs);
    if (seq !== migrationExportReqSeq || !migrationModalOpen.value || migrationModalMode.value !== "export") {
      return;
    }
    migrationExportSummary.value = summary;
  } catch (e) {
    if (seq !== migrationExportReqSeq || !migrationModalOpen.value || migrationModalMode.value !== "export") {
      return;
    }
    migrationExportError.value = formatMigrationError(e);
  } finally {
    if (seq === migrationExportReqSeq) {
      migrationExportLoading.value = false;
    }
  }
}

async function handleMigrationExport(payload: {
  modules: ColabOneMigrationModuleId[];
  password: string;
}) {
  if (migrationExportSubmitting.value) return;
  migrationExportSubmitting.value = true;
  migrationExportError.value = "";
  try {
    const uiPrefs = collectColabOneMigrationUIPrefs(
      window.localStorage,
      colabOneAppearance.value,
    );
    const res = await colabOneExportMigrationFile({
      modules: payload.modules,
      password: payload.password,
      ui_prefs: uiPrefs,
    });
    if (res.cancelled) {
      return;
    }
    migrationModalOpen.value = false;
    resetMigrationModalState(false);
    notify(
      t("migration.exportSuccessToast", {
        filename: res.filename || "ColabOne.colabone",
      }),
      "success",
      "migration-export",
    );
    void syncSnapshotQuietly();
  } catch (e) {
    migrationExportError.value = formatMigrationError(e);
  } finally {
    migrationExportSubmitting.value = false;
  }
}

async function openMigrationImport() {
  if (
    migrationPickingFile.value ||
    migrationImportDecrypting.value ||
    migrationImportApplying.value
  ) {
    return;
  }
  migrationPickingFile.value = true;
  migrationImportError.value = "";
  try {
    const picked = await colabOnePickMigrationFile();
    if (picked.cancelled || !picked.file_token) {
      return;
    }
    migrationModalMode.value = "import";
    migrationImportStep.value = "decrypt";
    migrationPickedFile.value = picked;
    migrationImportPreview.value = null;
    migrationImportError.value = "";
    migrationModalOpen.value = true;
  } catch (e) {
    if (migrationModalOpen.value && migrationModalMode.value === "import") {
      migrationImportError.value = formatMigrationError(e);
    } else {
      showError(e);
    }
  } finally {
    migrationPickingFile.value = false;
  }
}

async function handleMigrationDecrypt(payload: {
  fileToken: string;
  password: string;
}) {
  if (migrationImportDecrypting.value) return;
  migrationImportDecrypting.value = true;
  migrationImportError.value = "";
  try {
    const preview = await colabOneDecryptMigrationPreview(
      payload.fileToken,
      payload.password,
    );
    migrationImportPreview.value = preview;
    migrationImportStep.value = "preview";
    migrationImportError.value = "";
  } catch (e) {
    migrationImportError.value = formatMigrationError(e);
  } finally {
    migrationImportDecrypting.value = false;
  }
}

async function handleMigrationApply(payload: {
  previewId: string;
  modules: ColabOneMigrationModuleId[];
}) {
  if (migrationImportApplying.value) return;
  migrationImportApplying.value = true;
  migrationImportError.value = "";
  try {
    const res = await colabOneApplyMigrationPreview({
      preview_id: payload.previewId,
      modules: payload.modules,
    });

    if (res.applied_ui_prefs) {
      applyColabOneMigrationUIPrefs(res.applied_ui_prefs, {
        setAppearance: (mode) => {
          setColabOneAppearance(mode);
        },
        setLogsWidth: (width) => {
          logDrawerWidth.value = width;
        },
      });
    }

    const appliedSet = new Set(res.applied_modules || []);
    if (appliedSet.has("settings")) {
      editingAssignMaxAttempts.value = false;
      try {
        applyCloseBehaviorState(await colabOneGetCloseBehavior());
      } catch {
        // Ignore offline fallback errors.
      }
      const pollGen = updateStateGen;
      try {
        const latest = await colabOneGetUpdateState();
        if (!stopped) applyUpdateState(latest, false, pollGen);
      } catch {
        // Ignore offline fallback errors.
      }
    }

    if (appliedSet.has("credentials")) {
      form.value.hf_token = "";
      form.value.assign_proxy = "";
      assignProxyExpanded.value = false;
      hfTokenInputRef.value?.hide();
      assignProxyInputRef.value?.hide();
    }

    if (appliedSet.has("workflow_drafts")) {
      try {
        localStorage.removeItem(COLABONE_STORAGE_KEYS.comfyDrafts);
      } catch {
        // Ignore restricted storage contexts.
      }
    }

    // Force immediate full form & snapshot re-hydration on hot-apply.
    initialized = false;
    lastSyncedSettings = {};
    lastSnapshotRaw = null;
    await syncSnapshotQuietly();

    if (appliedSet.has("settings") || appliedSet.has("cu_usage")) {
      migrationUiSyncKey.value += 1;
    }
    if (appliedSet.has("credentials") || appliedSet.has("library_workflows")) {
      migrationApiSyncKey.value += 1;
    }
    if (
      appliedSet.has("workflow_drafts") ||
      appliedSet.has("library_workflows") ||
      appliedSet.has("credentials")
    ) {
      migrationComfySyncKey.value += 1;
    }

    migrationModalOpen.value = false;
    resetMigrationModalState(false);

    if (res.account_imported && res.account_logged_in) {
      notify(
        t("migration.importSuccessWithAccountToast", {
          email: res.account_email || t("migration.summarySignedIn"),
        }),
        "success",
        "migration-import",
      );
    } else {
      notify(
        t("migration.importSuccessToast", {
          count: res.applied_modules?.length || payload.modules.length,
        }),
        "success",
        "migration-import",
      );
    }
  } catch (e) {
    migrationImportError.value = formatMigrationError(e);
  } finally {
    migrationImportApplying.value = false;
  }
}

const capabilities = colabOneRuntimeCapabilities;
const isWebMode = computed(() => capabilities.value.runtime === "web");
const webConsoleCardRef = ref<{
  expandAndFocusPublicOrigin: () => Promise<void> | void;
} | null>(null);
const webConsoleLoaded = ref(false);
const webConsoleSaving = ref(false);
const webConsolePasscodeBusy = ref(false);
const webConsolePasscodeResetSeq = ref(0);
const webConsoleTicketBusy = ref(false);
const webConsoleRevoking = ref(false);
let webConsoleStateEpoch = 0;
let unsubWebConsole: (() => void) | null = null;

const workflowSharesState = ref<ColabOneWorkflowShareListState>(
  createColabOneDefaultWorkflowShareListState(),
);
const workflowSharesLoaded = ref(false);
const workflowShareModalOpen = ref(false);
const workflowShareCreating = ref(false);
const workflowShareRevokingId = ref("");
const workflowShareDeletingId = ref("");
const workflowShareLastCreated = ref<ColabOneWorkflowShareSummary | null>(null);
const workflowShareLastCreatedCopied = ref(false);
const workflowShareMutating = computed(
  () =>
    workflowShareCreating.value ||
    Boolean(workflowShareRevokingId.value) ||
    Boolean(workflowShareDeletingId.value),
);
let workflowSharesStateEpoch = 0;
let unsubWorkflowShares: (() => void) | null = null;

const outputSharesState = ref<ColabOneOutputShareListState>(
  createColabOneDefaultOutputShareListState(),
);
const outputShareCreating = ref(false);
const outputShareRevokingId = ref("");
const outputShareDeletingId = ref("");
const outputShareLastCreated = ref<ColabOneOutputShareSummary | null>(null);
const outputShareMutating = computed(
  () =>
    outputShareCreating.value ||
    Boolean(outputShareRevokingId.value) ||
    Boolean(outputShareDeletingId.value),
);
let outputSharesStateEpoch = 0;
let unsubOutputShares: (() => void) | null = null;

function isLoopbackOriginUrl(rawOrigin: string): boolean {
  try {
    const parsed = new URL(rawOrigin);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return false;
    }
    const host = parsed.hostname
      .toLowerCase()
      .replace(/^\[|\]$/g, "")
      .replace(/\.+$/, "");
    return (
      !host ||
      host === "localhost" ||
      host.endsWith(".localhost") ||
      /^127(?:\.\d{1,3}){3}$/.test(host) ||
      /^0(?:\.\d{1,3}){3}$/.test(host) ||
      host === "::1" ||
      host === "::" ||
      /^(?:0+:){7}0*1$/.test(host) ||
      /^(?:0+:){7}0+$/.test(host) ||
      /^::ffff:127(?:\.\d{1,3}){3}$/i.test(host) ||
      /^::ffff:0(?:\.\d{1,3}){3}$/i.test(host) ||
      /^::ffff:7f[0-9a-f]{2}:[0-9a-f]{1,4}$/i.test(host) ||
      /^::ffff:0{0,2}[0-9a-f]{1,2}:[0-9a-f]{1,4}$/i.test(host)
    );
  } catch {
    return false;
  }
}

const effectiveWebConsoleState = computed<ColabOneWebConsoleState>(() => {
  if (isColabOneDesktopRuntime()) {
    return webConsoleState.value;
  }
  const base = createColabOneDefaultWebConsoleState();
  const origin =
    typeof window !== "undefined" ? window.location.origin : "";
  const capPublicOrigin = (capabilities.value.public_origin || "").trim();
  const capLocalUrl = (capabilities.value.local_url || "").trim();
  const capLanUrls = Array.isArray(capabilities.value.lan_urls)
    ? capabilities.value.lan_urls
    : [];
  const capBindMode =
    capabilities.value.bind_mode === "lan" ? "lan" : base.bind_mode;
  return {
    ...base,
    enabled: true,
    running: true,
    bind_mode: capBindMode,
    public_origin: capPublicOrigin,
    local_url:
      capLocalUrl ||
      (origin && isLoopbackOriginUrl(origin) ? origin : "") ||
      base.local_url,
    lan_urls: capLanUrls,
  };
});

function applyOutputSharesState(
  next: ColabOneOutputShareListState,
  expectedEpoch?: number,
): boolean {
  if (
    expectedEpoch !== undefined &&
    expectedEpoch !== outputSharesStateEpoch
  ) {
    return false;
  }
  outputSharesStateEpoch += 1;
  outputSharesState.value = next;
  return true;
}

function refreshOutputSharesState() {
  if (isWebMode.value && colabOneWebSessionExpired.value) {
    return;
  }
  if (isColabOneDesktopRuntime()) {
    const wcEpoch = webConsoleStateEpoch;
    void colabOneGetWebConsoleState()
      .then((st) => {
        if (!stopped) applyWebConsoleState(st, wcEpoch);
      })
      .catch(() => {});
  } else {
    void fetchColabOneWebCapabilities().catch(() => {});
  }
  const osEpoch = outputSharesStateEpoch;
  void colabOneGetOutputSharesState()
    .then((st) => {
      if (!stopped) applyOutputSharesState(st, osEpoch);
    })
    .catch(() => {});
}

async function handleOutputShareCreate(req: ColabOneOutputShareCreateRequest) {
  if (outputShareMutating.value) return;
  outputShareCreating.value = true;
  const preCreateEpoch = outputSharesStateEpoch;
  try {
    const created = await colabOneCreateOutputShare(req);
    if (stopped) return;
    if (outputSharesStateEpoch === preCreateEpoch) {
      const merged = normalizeColabOneOutputShareListState({
        shares: [
          created,
          ...outputSharesState.value.shares.filter(
            (item) => item.id !== created.id,
          ),
        ],
      });
      applyOutputSharesState(merged, preCreateEpoch);
    }
    outputShareLastCreated.value = created;
    notify(t("outputShare.noticeCreated"), "success", "output-share-create");
  } catch (e) {
    if (!stopped && !(isWebMode.value && colabOneWebSessionExpired.value)) {
      showError(e);
    }
  } finally {
    if (!stopped) {
      outputShareCreating.value = false;
    }
  }
}

async function handleOutputShareRevoke(shareID: string) {
  if (outputShareMutating.value) return;
  outputShareRevokingId.value = shareID;
  try {
    const next = await colabOneRevokeOutputShare(shareID);
    if (stopped) return;
    applyOutputSharesState(next);
    if (outputShareLastCreated.value?.id === shareID) {
      outputShareLastCreated.value = null;
    }
    notify(t("outputShare.noticeRevoked"), "info", "output-share-revoke");
  } catch (e) {
    if (!stopped && !(isWebMode.value && colabOneWebSessionExpired.value)) {
      showError(e);
    }
  } finally {
    if (!stopped) {
      outputShareRevokingId.value = "";
    }
  }
}

async function handleOutputShareDelete(shareID: string) {
  if (outputShareMutating.value) return;
  outputShareDeletingId.value = shareID;
  try {
    const next = await colabOneDeleteOutputShare(shareID);
    if (stopped) return;
    applyOutputSharesState(next);
    if (outputShareLastCreated.value?.id === shareID) {
      outputShareLastCreated.value = null;
    }
    notify(t("outputShare.noticeDeleted"), "info", "output-share-delete");
  } catch (e) {
    if (!stopped && !(isWebMode.value && colabOneWebSessionExpired.value)) {
      showError(e);
    }
  } finally {
    if (!stopped) {
      outputShareDeletingId.value = "";
    }
  }
}

async function handleOutputShareCopyText(
  text: string,
  successToast?: string,
): Promise<boolean> {
  if (!text) return false;
  return copyWithToast(
    text,
    successToast || t("outputShare.noticeCopiedLink"),
    "output-share-copy",
  );
}

const WEB_WORKFLOW_SHARES_POLL_MS = 5000;
let webWorkflowSharesPollTimer: ReturnType<typeof setInterval> | null = null;
let webWorkflowSharesPollInFlight = false;

function stopWebWorkflowSharesPoll() {
  if (webWorkflowSharesPollTimer !== null) {
    clearInterval(webWorkflowSharesPollTimer);
    webWorkflowSharesPollTimer = null;
  }
}

function pollWebWorkflowSharesOnce() {
  if (
    stopped ||
    isColabOneDesktopRuntime() ||
    !workflowShareModalOpen.value ||
    !capabilities.value.can_manage_workflow_shares ||
    colabOneWebSessionExpired.value ||
    workflowShareMutating.value ||
    webWorkflowSharesPollInFlight ||
    (typeof document !== "undefined" && document.hidden)
  ) {
    return;
  }
  webWorkflowSharesPollInFlight = true;
  const wsEpoch = workflowSharesStateEpoch;
  void colabOneGetWorkflowSharesState()
    .then((st) => {
      if (!stopped && !workflowShareMutating.value) {
        applyWorkflowSharesState(st, wsEpoch);
      }
    })
    .catch(() => {})
    .finally(() => {
      webWorkflowSharesPollInFlight = false;
    });
}

watch(
  [
    workflowShareModalOpen,
    () => capabilities.value.can_manage_workflow_shares,
    colabOneWebSessionExpired,
  ],
  ([open, allowed, expired]) => {
    if (!allowed) {
      workflowShareModalOpen.value = false;
    }
    if (isColabOneDesktopRuntime() || !open || !allowed || expired) {
      stopWebWorkflowSharesPoll();
      return;
    }
    if (webWorkflowSharesPollTimer === null) {
      webWorkflowSharesPollTimer = setInterval(
        pollWebWorkflowSharesOnce,
        WEB_WORKFLOW_SHARES_POLL_MS,
      );
    }
  },
);

function applyWorkflowSharesState(
  next: ColabOneWorkflowShareListState,
  expectedEpoch?: number,
): boolean {
  if (
    expectedEpoch !== undefined &&
    expectedEpoch !== workflowSharesStateEpoch
  ) {
    return false;
  }
  workflowSharesStateEpoch += 1;
  workflowSharesState.value = next;
  workflowSharesLoaded.value = true;
  return true;
}

function refreshWorkflowSharesState() {
  if (!capabilities.value.can_manage_workflow_shares) {
    return;
  }
  if (isWebMode.value && colabOneWebSessionExpired.value) {
    return;
  }
  if (isColabOneDesktopRuntime()) {
    const wcEpoch = webConsoleStateEpoch;
    void colabOneGetWebConsoleState()
      .then((st) => {
        if (!stopped) applyWebConsoleState(st, wcEpoch);
      })
      .catch(() => {});
  } else {
    void fetchColabOneWebCapabilities().catch(() => {});
  }
  const wsEpoch = workflowSharesStateEpoch;
  void colabOneGetWorkflowSharesState()
    .then((st) => {
      if (!stopped) applyWorkflowSharesState(st, wsEpoch);
    })
    .catch(() => {
      if (!stopped) workflowSharesLoaded.value = true;
    });
}

function openWorkflowShareModal() {
  if (!capabilities.value.can_manage_workflow_shares) return;
  workflowShareLastCreated.value = null;
  workflowShareLastCreatedCopied.value = false;
  workflowShareModalOpen.value = true;
  refreshWorkflowSharesState();
}

async function handleWorkflowShareCreate(req: ColabOneWorkflowShareCreateRequest) {
  if (workflowShareMutating.value) return;
  workflowShareCreating.value = true;
  const preCreateEpoch = workflowSharesStateEpoch;
  try {
    const created = await colabOneCreateWorkflowShare(req);
    if (stopped) return;
    const alreadyPresent = workflowSharesState.value.shares.some(
      (item) => item.id === created.id,
    );
    if (workflowSharesStateEpoch === preCreateEpoch || !alreadyPresent) {
      const merged = normalizeColabOneWorkflowShareListState({
        shares: [
          created,
          ...workflowSharesState.value.shares.filter(
            (item) => item.id !== created.id,
          ),
        ],
      });
      applyWorkflowSharesState(merged);
    }
    const targetUrl = created.url || created.local_url || "";
    let copied = false;
    if (targetUrl) {
      try {
        await colabOneWriteClipboard(targetUrl);
        if (isColabOneDesktopRuntime()) {
          await navigator.clipboard?.writeText(targetUrl).catch(() => {});
        }
        copied = true;
      } catch {
        try {
          await navigator.clipboard.writeText(targetUrl);
          copied = true;
        } catch {
          copied = false;
        }
      }
    }
    if (stopped) return;
    workflowShareLastCreatedCopied.value = copied;
    workflowShareLastCreated.value = created;
    if (targetUrl) {
      notify(
        copied
          ? t("workflowShare.toastCreatedCopied")
          : t("workflowShare.toastCreatedOnly"),
        "success",
        "workflow-share-create",
      );
    } else {
      notify(
        t("workflowShare.toastCreatedOnly"),
        "success",
        "workflow-share-create",
      );
    }
  } catch (e) {
    if (!stopped && !(isWebMode.value && colabOneWebSessionExpired.value)) {
      showError(e);
    }
  } finally {
    if (!stopped) workflowShareCreating.value = false;
  }
}

async function handleWorkflowShareRevoke(shareID: string) {
  if (workflowShareMutating.value) return;
  workflowShareRevokingId.value = shareID;
  try {
    const next = await colabOneRevokeWorkflowShare(shareID);
    if (stopped) return;
    applyWorkflowSharesState(next);
    if (workflowShareLastCreated.value?.id === shareID) {
      workflowShareLastCreated.value = null;
      workflowShareLastCreatedCopied.value = false;
    }
    notify(t("workflowShare.toastRevoked"), "info", "workflow-share-revoke");
  } catch (e) {
    if (!stopped && !(isWebMode.value && colabOneWebSessionExpired.value)) {
      showError(e);
    }
  } finally {
    if (!stopped) workflowShareRevokingId.value = "";
  }
}

async function handleWorkflowShareDelete(shareID: string) {
  if (workflowShareMutating.value) return;
  workflowShareDeletingId.value = shareID;
  try {
    const next = await colabOneDeleteWorkflowShare(shareID);
    if (stopped) return;
    applyWorkflowSharesState(next);
    if (workflowShareLastCreated.value?.id === shareID) {
      workflowShareLastCreated.value = null;
      workflowShareLastCreatedCopied.value = false;
    }
    notify(t("workflowShare.toastDeleted"), "info", "workflow-share-delete");
  } catch (e) {
    if (!stopped && !(isWebMode.value && colabOneWebSessionExpired.value)) {
      showError(e);
    }
  } finally {
    if (!stopped) workflowShareDeletingId.value = "";
  }
}

async function handleWorkflowShareCopyLink(url: string): Promise<boolean> {
  if (!url) return false;
  return copyWithToast(
    url,
    t("workflowShare.toastLinkCopied"),
    "workflow-share-copy",
  );
}

function handleWorkflowShareEnableWebConsole() {
  void handleWebConsoleToggle({ enabled: true });
}

async function handleWorkflowShareOpenSettings() {
  workflowShareModalOpen.value = false;
  scrollController.cancel();
  if (page.value !== "settings") {
    suppressNextPageScrollTop = true;
    page.value = "settings";
  }
  await nextTick();
  await webConsoleCardRef.value?.expandAndFocusPublicOrigin();
}

function applyWebConsoleState(
  next: ColabOneWebConsoleState,
  expectedEpoch?: number,
): boolean {
  if (expectedEpoch !== undefined && expectedEpoch !== webConsoleStateEpoch) {
    return false;
  }
  webConsoleStateEpoch += 1;
  webConsoleState.value = next;
  webConsoleLoaded.value = true;
  return true;
}

async function copyWithToast(
  text: string,
  message: string,
  group?: string,
): Promise<boolean> {
  try {
    await colabOneWriteClipboard(text);
    await navigator.clipboard?.writeText(text).catch(() => {});
    notify(message, "success", group);
    return true;
  } catch {
    try {
      await navigator.clipboard.writeText(text);
      notify(message, "success", group);
      return true;
    } catch {
      showError(t("common.copyFailed"));
      return false;
    }
  }
}

async function loadSavedWebConsolePasscode(): Promise<string> {
  return colabOneGetWebConsolePasscode();
}

async function handleWebConsoleToggle(req: ColabOneWebConsoleUpdateRequest) {
  if (webConsoleSaving.value) return;
  webConsoleSaving.value = true;
  try {
    const next = await colabOneUpdateWebConsole(req);
    if (stopped) return;
    applyWebConsoleState(next);
    if (next.enabled && next.running) {
      notify(
        t("webConsole.toastEnabled", {
          addr: next.public_origin || next.local_url || next.listen_addr,
        }),
        "success",
        "web-console-toggle",
      );
    } else if (!next.enabled) {
      notify(t("webConsole.toastDisabled"), "info", "web-console-toggle");
    }
  } catch (e) {
    if (stopped) return;
    showError(e);
    const wcEpoch = webConsoleStateEpoch;
    void colabOneGetWebConsoleState()
      .then((st) => {
        if (!stopped) applyWebConsoleState(st, wcEpoch);
      })
      .catch(() => {});
  } finally {
    if (!stopped) webConsoleSaving.value = false;
  }
}

async function handleWebConsoleSaveConfig(req: ColabOneWebConsoleUpdateRequest) {
  if (webConsoleSaving.value) return;
  webConsoleSaving.value = true;
  try {
    const next = await colabOneUpdateWebConsole(req);
    if (stopped) return;
    applyWebConsoleState(next);
    notify(t("webConsole.toastConfigSaved"), "success", "web-console-config");
  } catch (e) {
    if (stopped) return;
    showError(e);
    const wcEpoch = webConsoleStateEpoch;
    void colabOneGetWebConsoleState()
      .then((st) => {
        if (!stopped) applyWebConsoleState(st, wcEpoch);
      })
      .catch(() => {});
  } finally {
    if (!stopped) webConsoleSaving.value = false;
  }
}

async function handleWebConsoleSetPasscode(passcode: string) {
  if (webConsolePasscodeBusy.value) return;
  const rotating = !passcode.trim();
  webConsolePasscodeBusy.value = true;
  try {
    const next = await colabOneSetWebConsolePasscode(passcode);
    if (stopped) return;
    applyWebConsoleState(next);
    webConsolePasscodeResetSeq.value += 1;
    notify(
      rotating
        ? t("webConsole.toastPasscodeRotated")
        : t("webConsole.toastPasscodeSaved"),
      "success",
      "web-console-passcode",
    );
  } catch (e) {
    if (!stopped) showError(e);
  } finally {
    if (!stopped) webConsolePasscodeBusy.value = false;
  }
}

async function handleWebConsoleCopyPasscode() {
  if (webConsolePasscodeBusy.value) return;
  webConsolePasscodeBusy.value = true;
  try {
    const pass = await colabOneGetWebConsolePasscode();
    if (stopped) return;
    if (!pass) {
      throw new Error(t("common.copyFailed"));
    }
    await copyWithToast(
      pass,
      t("webConsole.toastPasscodeCopied"),
      "web-console-copy-passcode",
    );
  } catch (e) {
    if (!stopped) showError(e);
  } finally {
    if (!stopped) webConsolePasscodeBusy.value = false;
  }
}

async function handleWebConsoleCreateTicket(singleUse = false) {
  if (webConsoleTicketBusy.value) return;
  webConsoleTicketBusy.value = true;
  try {
    const ticket = await colabOneCreateWebConsoleLoginTicket(singleUse);
    if (stopped) return;
    const targetUrl = ticket.url || ticket.local_url;
    if (!targetUrl) {
      throw new Error(t("common.copyFailed"));
    }
    const toastKey = ticket.single_use
      ? "webConsole.toastTicketCopiedSingleUse"
      : "webConsole.toastTicketCopied";
    await copyWithToast(
      targetUrl,
      t(toastKey, {
        seconds: ticket.ttl_seconds || 60,
      }),
      "web-console-ticket",
    );
  } catch (e) {
    if (!stopped) showError(e);
  } finally {
    if (!stopped) webConsoleTicketBusy.value = false;
  }
}

async function handleWebConsoleRevokeSessions() {
  if (webConsoleRevoking.value) return;
  webConsoleRevoking.value = true;
  try {
    const next = await colabOneRevokeWebConsoleSessions();
    if (stopped) return;
    applyWebConsoleState(next);
    notify(
      t("webConsole.toastSessionsRevoked"),
      "info",
      "web-console-revoke",
    );
  } catch (e) {
    if (!stopped) showError(e);
  } finally {
    if (!stopped) webConsoleRevoking.value = false;
  }
}

async function handleWebConsoleCopyText(text: string, labelKey: string) {
  if (!text) return;
  const label =
    labelKey === "webConsole.toastAddressCopied" ||
    labelKey === "webConsole.toastQuickCmdCopied"
      ? t(labelKey)
      : t("common.copied");
  await copyWithToast(text, label, "web-console-copy");
}

function handleWebAuthRequired() {
  if (stopped) return;
  colabOneWebSessionExpired.value = true;
  snapshotPoller.stop();
  ipcFailures = 0;
  ipcDisconnected.value = false;
  ipcError.value = "";
}

function handleWebRelogin() {
  if (typeof window !== "undefined") {
    window.location.assign("/_colabone_web/login");
  }
}

const webReconnecting = ref(false);

async function handleWebReconnect() {
  if (webReconnecting.value || stopped) return;
  webReconnecting.value = true;
  try {
    await fetchColabOneWebCapabilities();
    if (stopped) return;
    if (colabOneWebSessionExpired.value) {
      notify(t("webConsole.webSessionExpiredTitle"), "warning", "web-auth");
      return;
    }
    await syncSnapshot();
    if (stopped) return;
    if (colabOneWebSessionExpired.value) {
      notify(t("webConsole.webSessionExpiredTitle"), "warning", "web-auth");
      return;
    }
    refreshOutputSharesState();
    refreshWorkflowSharesState();
    ipcFailures = 0;
    ipcDisconnected.value = false;
    ipcError.value = "";
    snapshotPoller.start();
  } catch (e) {
    if (stopped) return;
    if (colabOneWebSessionExpired.value) {
      notify(t("webConsole.webSessionExpiredTitle"), "warning", "web-auth");
      return;
    }
    showError(e);
  } finally {
    if (!stopped) {
      webReconnecting.value = false;
      if (!colabOneWebSessionExpired.value && !snapshotPoller.isRunning()) {
        snapshotPoller.start();
      }
    }
  }
}

watch(colabOneWebSessionExpired, (expired, wasExpired) => {
  if (
    wasExpired &&
    !expired &&
    !stopped &&
    !webReconnecting.value &&
    !snapshotPoller.isRunning()
  ) {
    snapshotPoller.start();
  }
});

async function handleWebSignOut() {
  try {
    await colabOneWebLogout();
  } catch (e) {
    showError(e);
  }
}

onMounted(() => {
  snapshotPoller.start();
  if (isColabOneDesktopRuntime()) {
    void syncLogWindowState();
    unsubCloseRequest = colabOneOnCloseRequest((req) => {
      if (stopped || closeModalSubmitting.value) return;
      closeBehaviorState.value = {
        ...closeBehaviorState.value,
        default_choice: normalizeColabOneCloseAction(req.default_choice),
        can_background: req.can_background,
      };
      if (!closeModalOpen.value) {
        closeModalSuppressLeave.value = false;
        closeModalDefaultChoice.value = normalizeColabOneCloseAction(
          req.default_choice,
        );
        closeModalOpen.value = true;
      }
    });
    unsubCloseBehavior = colabOneOnCloseBehavior((st) => {
      if (!stopped) applyCloseBehaviorState(st);
    });
    void colabOneGetCloseBehavior()
      .then((st) => {
        if (!stopped) applyCloseBehaviorState(st);
      })
      .catch(() => {});
    unsubUpdateState = colabOneOnUpdateState((st) => {
      if (!stopped) applyUpdateState(st, true);
    });
    unsubUpdateOpen = colabOneOnUpdateOpen(() => {
      if (!stopped) updateModalOpen.value = true;
    });
    const initialUpdateGen = updateStateGen;
    void colabOneGetUpdateState()
      .then((st) => {
        if (!stopped && lastUpdatePushAt === 0) {
          applyUpdateState(st, false, initialUpdateGen);
        }
      })
      .catch(() => {});
    unsubWebConsole = colabOneOnWebConsoleState((st) => {
      if (!stopped) applyWebConsoleState(st);
    });
    const initialWcEpoch = webConsoleStateEpoch;
    void colabOneGetWebConsoleState()
      .then((st) => {
        if (!stopped) applyWebConsoleState(st, initialWcEpoch);
      })
      .catch(() => {
        if (!stopped) webConsoleLoaded.value = true;
      });
    unsubWorkflowShares = colabOneOnWorkflowSharesState((st) => {
      if (!stopped) applyWorkflowSharesState(st);
    });
    const initialWsEpoch = workflowSharesStateEpoch;
    void colabOneGetWorkflowSharesState()
      .then((st) => {
        if (!stopped) applyWorkflowSharesState(st, initialWsEpoch);
      })
      .catch(() => {
        if (!stopped) workflowSharesLoaded.value = true;
      });
    unsubOutputShares = colabOneOnOutputSharesState((st) => {
      if (!stopped) applyOutputSharesState(st);
    });
    const initialOsEpoch = outputSharesStateEpoch;
    void colabOneGetOutputSharesState()
      .then((st) => {
        if (!stopped) applyOutputSharesState(st, initialOsEpoch);
      })
      .catch(() => {});
  } else {
    webConsoleLoaded.value = true;
    window.addEventListener(
      COLABONE_WEB_AUTH_REQUIRED_EVENT,
      handleWebAuthRequired,
    );
    void fetchColabOneWebCapabilities().catch(() => {});
    const initialWebWsEpoch = workflowSharesStateEpoch;
    void colabOneGetWorkflowSharesState()
      .then((st) => {
        if (!stopped) applyWorkflowSharesState(st, initialWebWsEpoch);
      })
      .catch(() => {
        if (!stopped) workflowSharesLoaded.value = true;
      });
    const initialWebOsEpoch = outputSharesStateEpoch;
    void colabOneGetOutputSharesState()
      .then((st) => {
        if (!stopped) applyOutputSharesState(st, initialWebOsEpoch);
      })
      .catch(() => {});
  }
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
    if (!ipcDisconnected.value && !document.hidden) {
      clockNow.value = Date.now();
    }
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
  rollbackLoadSeq += 1;
  snapshotPoller.stop();
  unsubUpdateState?.();
  unsubUpdateState = null;
  unsubUpdateOpen?.();
  unsubUpdateOpen = null;
  unsubCloseRequest?.();
  unsubCloseRequest = null;
  unsubCloseBehavior?.();
  unsubCloseBehavior = null;
  unsubWebConsole?.();
  unsubWebConsole = null;
  unsubWorkflowShares?.();
  unsubWorkflowShares = null;
  unsubOutputShares?.();
  unsubOutputShares = null;
  window.removeEventListener(
    COLABONE_WEB_AUTH_REQUIRED_EVENT,
    handleWebAuthRequired,
  );
  stopUpdateProgressPoll();
  stopWebWorkflowSharesPoll();
  cancelPanelPrefetch?.();
  cancelPanelPrefetch = null;
  for (const timer of pendingAsyncPanelRetryTimers) {
    clearTimeout(timer);
  }
  pendingAsyncPanelRetryTimers.clear();
  clearConfirmDriveTimer();
  if (cuUsageHighlightTimer !== null) {
    clearTimeout(cuUsageHighlightTimer);
    cuUsageHighlightTimer = null;
  }
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
  if (compactTaskDockMql) {
    if (typeof compactTaskDockMql.removeEventListener === "function") {
      compactTaskDockMql.removeEventListener(
        "change",
        onCompactTaskDockMqlChange,
      );
    } else if (typeof compactTaskDockMql.removeListener === "function") {
      compactTaskDockMql.removeListener(onCompactTaskDockMqlChange);
    }
    compactTaskDockMql = null;
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
              <ColabOneUpdateModal
                v-if="capabilities.can_self_update"
                :open="updateModalOpen"
                :update-state="updateState"
                :checking="isCheckingUpdate"
                :rollback-versions="rollbackVersions"
                :loading-rollback="loadingRollbackVersions"
                :rollback-loaded="rollbackVersionsLoaded"
                :rollback-error="rollbackVersionsError"
                :starting-rollback="startingRollback"
                :has-active-runtime="
                  state.runtime.state === 'ready' ||
                  state.runtime.state === 'starting'
                "
                :has-active-operation="Boolean(state.operation)"
                @update:open="handleUpdatePopoverOpenChange"
                @check="handleCheckForUpdate(false)"
                @install="handleStartUpdateInstall"
                @load-rollback="handleLoadRollbackVersions"
                @rollback="handleStartUpdateRollback"
                @cancel="handleCancelUpdateInstall"
                @relaunch="handleRelaunchForUpdate"
                @skip="handleSkipUpdateVersion"
                @open-release-page="handleOpenReleasePage"
              >
                <span class="co-brand-version-anchor">
                  <ColabOneTooltip
                    :title="navVersionTooltip"
                    :disabled="updateModalOpen"
                    placement="bottom"
                  >
                    <button
                      type="button"
                      class="co-brand-version"
                      :class="{
                        'is-open': updateModalOpen,
                        'is-update-available': showNavUpdateDot,
                        'is-update-ready':
                          updateState.status === 'ready_to_restart',
                        'is-update-downloading':
                          updateState.status === 'downloading' ||
                          updateState.status === 'installing',
                      }"
                      :aria-expanded="updateModalOpen"
                      aria-haspopup="dialog"
                      data-testid="nav-version-btn"
                      :aria-label="navVersionTooltip"
                    >
                      <span class="co-brand-version-text">{{
                        appVersionLabel
                      }}</span>
                      <span
                        v-if="showNavUpdatePill"
                        class="co-nav-update-pill"
                        data-testid="nav-update-pill"
                      >
                        <span class="co-nav-update-dot" aria-hidden="true" />
                        <span>{{ navUpdateBadgeLabel }}</span>
                      </span>
                      <span
                        v-if="showNavUpdateDot"
                        class="co-brand-version-dot"
                        data-testid="nav-update-dot"
                        aria-hidden="true"
                      />
                    </button>
                  </ColabOneTooltip>
                </span>
              </ColabOneUpdateModal>
              <span v-else class="co-brand-web-meta">
                <span class="co-brand-version is-static" data-testid="nav-version-btn">
                  <span class="co-brand-version-text">{{ appVersionLabel }}</span>
                </span>
                <ATag
                  v-if="isWebMode"
                  color="blue"
                  :bordered="false"
                  class="co-web-mode-badge"
                  data-testid="nav-web-mode-badge"
                >
                  {{ t("webConsole.webBadge") }}
                </ATag>
              </span>
            </div>

            <div class="co-nav-center">
              <nav
                class="co-nav-pill-track"
                :aria-label="t('nav.mainAria')"
              >
                <button
                  v-for="(item, idx) in nav"
                  :key="item.id"
                  type="button"
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
                  colabOnePrivacyActive
                    ? t('nav.privacyDisableTooltip')
                    : t('nav.privacyEnableTooltip')
                "
                placement="bottom"
              >
                <button
                  type="button"
                  class="co-nav-circle-btn co-nav-privacy-btn"
                  :class="{ 'is-active': colabOnePrivacyActive }"
                  data-testid="nav-privacy-toggle"
                  :aria-pressed="colabOnePrivacyActive"
                  :aria-label="
                    colabOnePrivacyActive
                      ? t('nav.privacyDisableAria')
                      : t('nav.privacyEnableAria')
                  "
                  @click="togglePrivacyMask"
                >
                  <PhEyeSlash
                    v-if="colabOnePrivacyActive"
                    :size="17"
                    weight="duotone"
                  />
                  <PhEye v-else :size="17" />
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
                  :aria-controls="
                    logWindowStandalone ? undefined : 'activity-panel'
                  "
                  @click="setLogsOpen(!logsOpen)"
                >
                  <PhTerminalWindow :size="16" aria-hidden="true" />
                  <span class="co-logs-trigger-label">{{ t("nav.logs") }}</span>
                </button>
              </ColabOneTooltip>

              <APopconfirm
                v-if="isWebMode"
                :title="t('webConsole.webSignOutConfirm')"
                :ok-text="t('webConsole.webSignOutBtn')"
                :cancel-text="t('common.cancel')"
                placement="bottomRight"
                @confirm="handleWebSignOut"
              >
                <ColabOneTooltip
                  :title="t('webConsole.webSignOutBtn')"
                  placement="bottom"
                >
                  <button
                    type="button"
                    class="co-nav-circle-btn co-web-signout-btn"
                    data-testid="nav-web-signout-btn"
                    :aria-label="t('webConsole.webSignOutBtn')"
                  >
                    <PhSignOut :size="17" />
                  </button>
                </ColabOneTooltip>
              </APopconfirm>
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
                  :data-co-full-title="formatColabOneTopbarInstanceLabel(inst)"
                >
                  {{ formatColabOneTopbarInstanceLabel(inst) }}
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
                :release-mode="form.release_mode"
                :release-cu-limit="form.release_cu_limit"
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
          v-if="
            (isWebMode && colabOneWebSessionExpired) ||
            showUpgradeBanner ||
            persistentBannerError ||
            state.operation
          "
          class="co-banners"
        >
          <div class="co-banners-inner">
            <AAlert
              v-if="isWebMode && colabOneWebSessionExpired"
              type="warning"
              show-icon
              :message="t('webConsole.webSessionExpiredTitle')"
              :description="t('webConsole.webSessionExpiredDesc')"
              class="co-web-expired-banner"
              data-testid="web-session-expired-banner"
            >
              <template #action>
                <ASpace :size="8" wrap>
                  <AButton
                    size="small"
                    :loading="webReconnecting"
                    :disabled="webReconnecting"
                    @click="handleWebReconnect"
                  >
                    {{ t("webConsole.webReconnectBtn") }}
                  </AButton>
                  <AButton type="primary" size="small" @click="handleWebRelogin">
                    {{ t("webConsole.webReloginBtn") }}
                  </AButton>
                </ASpace>
              </template>
            </AAlert>

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
              @close="dismissPersistentError"
            >
              <template #message>
                <ColabOnePrivacyText
                  :text="persistentBannerError"
                  :context="privacyContext"
                />
              </template>
            </AAlert>

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
              <template v-if="showStartCancelButton" #action>
                <ASpace :size="8" class="co-op-actions">
                  <ColabOneTooltip
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
        <ALayoutContent
          ref="contentRef"
          class="co-content"
          :class="{ 'is-settings-page': page === 'settings' }"
        >
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
                    <AButton
                      v-if="capabilities.can_migrate"
                      :loading="migrationPickingFile"
                      :disabled="busy || !!state.operation"
                      data-testid="home-migration-import-btn"
                      @click="openMigrationImport"
                    >
                      <template #icon>
                        <PhDownloadSimple :size="15" />
                      </template>
                      {{ t("migration.homeImportBtn") }}
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
                      <template v-else-if="ready && comfyRuntimeBusy">
                        <code v-if="state.runtime.id" class="co-runtime-id">{{
                          state.runtime.id
                        }}</code>
                        <span>{{
                          liveStudioRuntimeBusy && !comfyEngineRuntimeBusy
                            ? t("device.liveStudioRunning")
                            : t("device.comfyRunning")
                        }}</span>
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
                      <div class="co-release-control-row">
                        <ASelect
                          :value="activeReleaseSelectValue"
                          :aria-label="t('device.idleReleaseAria')"
                          class="co-select-idle"
                          :dropdown-match-select-width="false"
                          :disabled="savingIdle"
                          @change="changeReleasePolicy"
                        >
                          <ASelectOption
                            v-if="!isPresetIdleMinutes(form.idle_minutes)"
                            :value="encodeColabOneReleaseSelectValue('idle', form.idle_minutes)"
                          >
                            {{ formatIdlePolicyLabel(form.idle_minutes) }}
                          </ASelectOption>
                          <ASelectOption
                            v-for="opt in COLABONE_IDLE_RELEASE_OPTIONS"
                            :key="opt.value"
                            :value="encodeColabOneReleaseSelectValue('idle', opt.value)"
                          >
                            {{ opt.label }}
                          </ASelectOption>
                          <ASelectOption :value="COLABONE_RELEASE_SELECT_NEVER">
                            {{ t("billing.releaseModeNever") }}
                          </ASelectOption>
                          <ASelectOption :value="COLABONE_RELEASE_SELECT_CU_LIMIT">
                            {{ t("billing.releaseModeCuLimitOption") }}
                          </ASelectOption>
                        </ASelect>

                        <div v-if="showCuLimitEditor" class="co-cu-limit-editor">
                          <AInputNumber
                            :value="cuLimitDraft"
                            :min="COLABONE_MIN_RELEASE_CU_LIMIT"
                            :max="COLABONE_MAX_RELEASE_CU_LIMIT"
                            :step="1"
                            :precision="2"
                            :aria-label="t('device.releaseCuLimitAria')"
                            class="co-cu-limit-input"
                            :disabled="savingIdle"
                            @update:value="onCuLimitInput"
                            @focus="editingCuLimit = true"
                            @blur="editingCuLimit = false"
                            @press-enter="commitReleaseCuLimit"
                          />
                          <span class="co-cu-limit-unit">CU</span>
                          <AButton
                            size="small"
                            :type="cuLimitNeedsApply ? 'primary' : 'default'"
                            :loading="savingIdle"
                            :disabled="savingIdle || !cuLimitCanApply"
                            class="co-cu-limit-apply-btn"
                            @click="commitReleaseCuLimit"
                          >
                            {{ t("common.apply") }}
                          </AButton>
                        </div>
                      </div>
                    </div>
                  </div>

                  <div class="co-device-actions co-device-action-group">
                    <template
                      v-if="
                        isStartOperationActive &&
                        state.runtime.state === 'starting' &&
                        showStartCancelButton
                      "
                    >
                      <ColabOneTooltip
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
                    <template v-if="cuLimitPendingActivation">
                      <div class="co-policy-live is-warning">
                        <ABadge status="warning" />
                        <span>
                          {{
                            t("device.releaseCuLimitPendingHint", {
                              consumed: currentRunConsumedCU.toFixed(2),
                            })
                          }}
                        </span>
                      </div>
                      <span class="co-policy-hint">
                        {{ t("device.releaseCuLimitHint") }}
                      </span>
                    </template>
                    <template v-else-if="form.release_mode === 'never'">
                      <div class="co-policy-live is-warning">
                        <ABadge status="warning" />
                        <span>
                          {{
                            ready
                              ? t("device.releaseNeverLive")
                              : t("device.releaseNeverSummary")
                          }}
                        </span>
                      </div>
                      <span class="co-policy-hint">
                        {{ t("device.releaseNeverHint") }}
                      </span>
                    </template>
                    <template v-else-if="form.release_mode === 'cu_limit'">
                      <div
                        v-if="ready"
                        class="co-policy-live"
                        :class="{ 'is-urgent': isCuLimitUrgent, 'is-active': !isCuLimitUrgent }"
                      >
                        <ABadge :status="isCuLimitUrgent ? 'error' : 'processing'" />
                        <span>
                          {{
                            t("device.releaseCuLimitLive", {
                              consumed: currentRunConsumedCU.toFixed(2),
                              limit: formatColabOneCULimitValue(form.release_cu_limit),
                              remaining: cuLimitRemainingCU.toFixed(2),
                            })
                          }}
                        </span>
                      </div>
                      <div v-else class="co-policy-live">
                        {{
                          t("device.releaseCuLimitSummary", {
                            limit: formatColabOneCULimitValue(form.release_cu_limit),
                          })
                        }}
                      </div>
                      <span class="co-policy-hint">
                        {{
                          cuLimitDraftAlreadyReached && cuLimitNeedsApply
                            ? t("device.releaseCuLimitImmediateWarn", {
                                consumed: currentRunConsumedCU.toFixed(2),
                              })
                            : t("device.releaseCuLimitHint")
                        }}
                      </span>
                    </template>
                    <template v-else-if="ready && idle">
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
                    <div class="co-drive-info">
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
                          <ColabOnePrivacyText
                            v-if="state.drive_auth_hint"
                            :text="tb(state.drive_auth_hint)"
                            :context="privacyContext"
                          />
                          <template v-else>{{
                            t("device.driveWaitingBrowserConfirm")
                          }}</template>
                        </template>
                        <template
                          v-else-if="
                            !state.setup.drive &&
                            state.operation?.kind === 'drive_connect' &&
                            (state.operation?.stage === 'mounting' ||
                              state.operation?.stage === 'initializing')
                          "
                        >
                          {{
                            state.operation?.interactive
                              ? t("device.driveMountingAfterAuth")
                              : state.operation?.title
                                ? tb(state.operation.title)
                                : t("comfy.driveModalMountingDirectTitle")
                          }}
                        </template>
                        <template v-else-if="state.setup.drive">
                          <ColabOnePrivacyText
                            v-if="state.runtime.drive?.error"
                            :text="tb(state.runtime.drive.error)"
                            :context="privacyContext"
                          />
                          <template v-else>{{
                            t("device.driveMountedFull", {
                              folder: activeDriveFolderName,
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
                          }}</template>
                        </template>
                        <template v-else-if="state.runtime.drive?.error">
                          <ColabOnePrivacyText
                            :text="tb(state.runtime.drive.error)"
                            :context="privacyContext"
                          />
                        </template>
                        <template v-else-if="state.setup.drive_authorized">
                          {{ t("device.driveAuthorizedHint") }}
                        </template>
                        <template v-else>
                          {{ t("device.driveNeedAuthHint") }}
                        </template>
                      </span>
                    </div>
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
                    <template
                      v-else-if="
                        !state.setup.drive &&
                        state.operation?.kind === 'drive_connect' &&
                        state.operation?.stage === 'mounting' &&
                        state.operation?.mode !== 'rclone' &&
                        !state.operation?.cancelling
                      "
                    >
                      <ASpace :size="6" wrap>
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
                  @view-usage-trend="scrollToCuUsage"
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
                  :key="`rm-${migrationUiSyncKey}`"
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

              <!-- Compute Unit (CU) Consumption Trend Chart -->
              <section
                id="co-cu-usage-section"
                class="co-cu-usage-wrap"
                :aria-label="t('cuUsage.sectionAria')"
              >
                <CuUsageChart
                  :key="`cu-${migrationUiSyncKey}`"
                  :account="state.account"
                  :setup-colab="state.setup.colab"
                  :loading="!snapshotLoaded"
                  :ipc-disconnected="ipcDisconnected"
                  :now-ms="clockNow"
                  :highlighted="cuUsageHighlighted"
                  @open-settings="
                    !busy && !state.operation
                      ? beginLogin()
                      : selectPage('settings')
                  "
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
                      :class="{ 'is-active': inst.active && inst.managed }"
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
                    ? t("device.footerDriveMountedFolder", {
                        folder: activeDriveFolderName,
                      })
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
              :key="`${apiPanelRetryKey}-${migrationApiSyncKey}`"
              v-model:models-open="modelsDrawerOpen"
              :state="state"
              :busy="busy"
              :loading="!snapshotLoaded"
              @vue:mounted="onApiPanelMounted"
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
              :key="`${comfyPanelRetryKey}-${migrationComfySyncKey}`"
              :state="state"
              :loading="!snapshotLoaded"
              :clock-now="clockNow"
              :snapshot-at="snapshotAt"
              :can-share-workflows="capabilities.can_manage_workflow_shares"
              :active-share-count="workflowSharesState.active_count"
              :web-console-state="effectiveWebConsoleState"
              :output-shares-state="outputSharesState"
              :can-enable-web-console="capabilities.can_manage_web_console"
              :enabling-web-console="webConsoleSaving"
              :output-share-creating="outputShareCreating"
              :output-share-revoking-id="outputShareRevokingId"
              :output-share-deleting-id="outputShareDeletingId"
              :output-share-last-created="outputShareLastCreated"
              :copy-text="handleOutputShareCopyText"
              @device="openDevice"
              @settings="selectPage('settings')"
              @upgrade="requestRuntimeUpgrade"
              @sync="onPanelSync"
              @notify="notify"
              @clear-error="clearActionErrors"
              @error="showError"
              @drawer-state="onComfyDrawerState"
              @open-share-modal="openWorkflowShareModal"
              @refresh-output-shares="refreshOutputSharesState"
              @create-output-share="handleOutputShareCreate"
              @revoke-output-share="handleOutputShareRevoke"
              @delete-output-share="handleOutputShareDelete"
              @clear-output-share-created="outputShareLastCreated = null"
              @enable-web-console="handleWorkflowShareEnableWebConsole"
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
                <AAlert
                  v-if="isWebMode"
                  type="info"
                  show-icon
                  :message="t('webConsole.webDesktopOnlyHint')"
                  :description="t('webConsole.webSecretsBlockedHint')"
                  class="co-web-settings-notice"
                  data-testid="settings-web-mode-notice"
                />

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
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-blue" aria-hidden="true">
                                <PhUserCircle :size="14" weight="duotone" />
                              </span>
                              <h3 class="co-sr-title">{{ t("settings.googleCardTitle") }}</h3>
                            </span>
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
                              <ColabOnePrivacyText
                                v-if="state.account.email"
                                :text="state.account.email"
                                kind="email"
                              />
                              <template v-else>{{
                                state.setup.colab
                                  ? t("settings.accountConnectedNoEmail")
                                  : t("settings.oauthBrowserHint")
                              }}</template>
                            </span>
                          </p>

                          <p v-if="!capabilities.can_loopback_oauth" class="co-sr-desc">
                            {{ t("webConsole.webOAuthManualHint") }}
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
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-emerald" aria-hidden="true">
                                <PhCloud :size="14" weight="duotone" />
                              </span>
                              <h3 class="co-sr-title">{{ t("settings.driveHeading") }}</h3>
                            </span>
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
                                  ? t("settings.driveTagMountedFolder", {
                                      folder: activeDriveFolderName,
                                    })
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
                            <code>My Drive / ColabOne_Drive</code>
                            {{ t("settings.driveDescNativeSep") }}
                            <code>My Drive / ColabOne_Drive_Mount</code>{{ t("settings.driveDescSuffix") }}
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
                              v-if="
                                ready &&
                                state.operation &&
                                !state.setup.drive &&
                                !state.operation.cancelling &&
                                (Boolean(state.auth_url) ||
                                  (state.operation.kind === 'drive_connect' &&
                                    state.operation.stage === 'mounting' &&
                                    state.operation.mode !== 'rclone'))
                              "
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
                      >
                        <template #message>
                          <ColabOnePrivacyText
                            :text="tb(state.drive_auth_hint)"
                            :context="privacyContext"
                          />
                        </template>
                      </AAlert>
                    </div>

                    <!-- Row 1.3: Hugging Face Token -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-violet" aria-hidden="true">
                                <PhKey :size="14" weight="duotone" />
                              </span>
                              <h3 class="co-sr-title">{{ t("settings.hfCardTitle") }}</h3>
                            </span>
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
                              :load-saved="
                                capabilities.can_reveal_host_secrets
                                  ? loadSavedHfToken
                                  : undefined
                              "
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
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-blue" aria-hidden="true">
                                <PhSlidersHorizontal :size="14" weight="duotone" />
                              </span>
                              <h3 class="co-sr-title">{{ t("settings.defaultGpuLabel") }}</h3>
                            </span>
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

                    <!-- Row 2.2: Auto-Release Policy -->
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
                            <template v-if="activeReleaseSelectValue === COLABONE_RELEASE_SELECT_NEVER">
                              {{ t("settings.releaseNeverDesc") }}
                            </template>
                            <template v-else-if="activeReleaseSelectValue === COLABONE_RELEASE_SELECT_CU_LIMIT">
                              {{
                                t("settings.releaseCuLimitDesc", {
                                  limit: formatColabOneCULimitValue(
                                    normalizedCuLimitDraft ?? form.release_cu_limit,
                                  ),
                                })
                              }}
                            </template>
                            <template v-else>
                              {{
                                t("settings.idleProtectionDesc", {
                                  policy: formatIdlePolicyLabel(form.idle_minutes),
                                })
                              }}
                            </template>
                          </p>
                          <p
                            v-if="
                              snapshotLoaded &&
                              showCuLimitEditor &&
                              (cuLimitPendingActivation ||
                                (cuLimitDraftAlreadyReached && cuLimitNeedsApply))
                            "
                            class="co-cu-limit-warn"
                          >
                            {{
                              cuLimitPendingActivation
                                ? t("device.releaseCuLimitPendingHint", {
                                    consumed: currentRunConsumedCU.toFixed(2),
                                  })
                                : t("device.releaseCuLimitImmediateWarn", {
                                    consumed: currentRunConsumedCU.toFixed(2),
                                  })
                            }}
                          </p>
                        </div>
                        <div class="co-sr-control co-sr-release-control">
                          <ColabOneSkeleton
                            v-if="!snapshotLoaded"
                            :width="184"
                            :height="32"
                            :radius="8"
                            variant="rect"
                            tone="subtle"
                            :delay="60"
                          />
                          <div v-else class="co-release-control-row co-sk-reveal">
                            <ASelect
                              :value="activeReleaseSelectValue"
                              :aria-label="t('device.idleReleaseAria')"
                              class="co-sr-select co-sr-select-idle"
                              :dropdown-match-select-width="false"
                              :disabled="savingIdle"
                              @change="changeReleasePolicy"
                            >
                              <ASelectOption
                                v-if="!isPresetIdleMinutes(form.idle_minutes)"
                                :value="encodeColabOneReleaseSelectValue('idle', form.idle_minutes)"
                              >
                                {{ formatIdlePolicyLabel(form.idle_minutes) }}
                              </ASelectOption>
                              <ASelectOption
                                v-for="opt in COLABONE_IDLE_RELEASE_OPTIONS"
                                :key="opt.value"
                                :value="encodeColabOneReleaseSelectValue('idle', opt.value)"
                              >
                                {{ opt.label }}
                              </ASelectOption>
                              <ASelectOption :value="COLABONE_RELEASE_SELECT_NEVER">
                                {{ t("billing.releaseModeNever") }}
                              </ASelectOption>
                              <ASelectOption :value="COLABONE_RELEASE_SELECT_CU_LIMIT">
                                {{ t("billing.releaseModeCuLimitOption") }}
                              </ASelectOption>
                            </ASelect>

                            <div v-if="showCuLimitEditor" class="co-cu-limit-editor">
                              <AInputNumber
                                id="settings-release-cu-limit"
                                :value="cuLimitDraft"
                                :min="COLABONE_MIN_RELEASE_CU_LIMIT"
                                :max="COLABONE_MAX_RELEASE_CU_LIMIT"
                                :step="1"
                                :precision="2"
                                :aria-label="t('device.releaseCuLimitAria')"
                                class="co-cu-limit-input"
                                :disabled="savingIdle"
                                @update:value="onCuLimitInput"
                                @focus="editingCuLimit = true"
                                @blur="editingCuLimit = false"
                                @press-enter="commitReleaseCuLimit"
                              />
                              <span class="co-cu-limit-unit">CU</span>
                              <AButton
                                size="small"
                                :type="cuLimitNeedsApply ? 'primary' : 'default'"
                                :loading="savingIdle"
                                :disabled="savingIdle || !cuLimitCanApply"
                                class="co-cu-limit-apply-btn"
                                @click="commitReleaseCuLimit"
                              >
                                {{ t("common.apply") }}
                              </AButton>
                            </div>
                          </div>
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

                    <!-- Row 2.5: GPU Allocation Max Attempts -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <h3 class="co-sr-title">{{ t("settings.assignMaxAttemptsLabel") }}</h3>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.assignMaxAttemptsDesc") }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <ColabOneSkeleton
                            v-if="!snapshotLoaded"
                            :width="116"
                            :height="32"
                            :radius="8"
                            variant="rect"
                            tone="subtle"
                            :delay="115"
                          />
                          <div v-else class="co-sr-attempts-wrap co-sk-reveal">
                            <AInputNumber
                              id="settings-assign-max-attempts"
                              :value="assignMaxAttemptsDraft"
                              :min="COLABONE_MIN_ASSIGN_MAX_ATTEMPTS"
                              :max="COLABONE_MAX_ASSIGN_MAX_ATTEMPTS"
                              :step="1"
                              :precision="0"
                              :aria-label="t('settings.assignMaxAttemptsAria')"
                              class="co-sr-attempts-input"
                              :disabled="busy"
                              @update:value="onAssignMaxAttemptsInput"
                              @step="onAssignMaxAttemptsStep"
                              @focus="editingAssignMaxAttempts = true"
                              @blur="onAssignMaxAttemptsBlur"
                              @press-enter="commitAssignMaxAttempts"
                            />
                            <span class="co-sr-unit">{{ t("settings.assignMaxAttemptsUnit") }}</span>
                          </div>
                        </div>
                      </div>
                    </div>

                    <!-- Row 2.6: GPU Allocation Proxy (Assign Only, Progressive Disclosure) -->
                    <div class="co-settings-row">
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-emerald" aria-hidden="true">
                                <PhShieldCheck :size="14" weight="duotone" />
                              </span>
                              <h3 class="co-sr-title">{{ t("settings.assignProxyTitle") }}</h3>
                            </span>
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
                            :load-saved="
                              capabilities.can_reveal_host_secrets
                                ? loadSavedAssignProxy
                                : undefined
                            "
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
                          <ColabOneTooltip
                            v-if="canTestAssignProxy"
                            :title="
                              assignProxyTestNeedsSaveFirst
                                ? t('settings.assignProxyTestSaveFirstHint')
                                : ''
                            "
                            :disabled="!assignProxyTestNeedsSaveFirst"
                            placement="top"
                          >
                            <span
                              class="co-assign-proxy-test-wrap"
                              data-testid="settings-assign-proxy-test-wrap"
                            >
                              <AButton
                                size="small"
                                :loading="testingAssignProxy"
                                :disabled="assignProxyTestDisabled"
                                data-testid="settings-assign-proxy-test-btn"
                                @click="testAssignProxy"
                              >
                                {{ t("settings.assignProxyTestBtn") }}
                              </AButton>
                            </span>
                          </ColabOneTooltip>
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
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-amber" aria-hidden="true">
                                <PhSun :size="14" weight="duotone" />
                              </span>
                              <h3 id="appearance-heading" class="co-sr-title">
                                {{ t("settings.appearanceLabel") }}
                              </h3>
                            </span>
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

                    <!-- Row 3.3: Privacy Mask Default on Startup -->
                    <div
                      class="co-settings-row"
                      data-testid="settings-privacy-default-row"
                      aria-labelledby="privacy-mask-heading"
                    >
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-violet" aria-hidden="true">
                                <PhEyeSlash :size="14" weight="duotone" />
                              </span>
                              <h3 id="privacy-mask-heading" class="co-sr-title">
                                {{ t("settings.privacyMaskDefaultTitle") }}
                              </h3>
                            </span>
                            <ATag
                              v-if="colabOnePrivacyActive"
                              color="blue"
                              data-testid="settings-privacy-active-tag"
                            >
                              {{ t("settings.privacyMaskActiveTag") }}
                            </ATag>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.privacyMaskDefaultDesc") }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <ASwitch
                            :checked="colabOnePrivacyDefault"
                            :aria-label="t('settings.privacyMaskDefaultTitle')"
                            data-testid="settings-privacy-default-switch"
                            @update:checked="onPrivacyDefaultChange"
                          />
                        </div>
                      </div>
                    </div>
                  </div>
                </section>

                <!-- Section 3.5: Remote Web Console (Desktop Host Only) -->
                <section
                  v-if="capabilities.can_manage_web_console"
                  class="co-settings-group-block"
                  aria-labelledby="settings-sec-web-console"
                >
                  <h2 id="settings-sec-web-console" class="co-settings-section-label">
                    {{ t("webConsole.sectionTitle") }}
                  </h2>
                  <div class="co-settings-list">
                    <ColabOneWebConsoleCard
                      ref="webConsoleCardRef"
                      :state="webConsoleState"
                      :loading="!webConsoleLoaded"
                      :saving="webConsoleSaving"
                      :passcode-busy="webConsolePasscodeBusy"
                      :passcode-reset-seq="webConsolePasscodeResetSeq"
                      :ticket-busy="webConsoleTicketBusy"
                      :revoking="webConsoleRevoking"
                      :workflow-share-active-count="workflowSharesState.active_count"
                      :load-saved-passcode="loadSavedWebConsolePasscode"
                      @toggle="handleWebConsoleToggle"
                      @save-config="handleWebConsoleSaveConfig"
                      @set-passcode="handleWebConsoleSetPasscode"
                      @copy-passcode="handleWebConsoleCopyPasscode"
                      @create-ticket="handleWebConsoleCreateTicket"
                      @revoke-sessions="handleWebConsoleRevokeSessions"
                      @open-workflow-shares="openWorkflowShareModal"
                      @copy-text="handleWebConsoleCopyText"
                      @open-url="openExternalURL"
                      @error="showError"
                    />
                  </div>
                </section>

                <!-- Section 4: Data & One-Click Migration -->
                <section
                  v-if="capabilities.can_migrate"
                  class="co-settings-group-block"
                  aria-labelledby="settings-sec-migration"
                >
                  <h2 id="settings-sec-migration" class="co-settings-section-label">
                    {{ t("settings.sectionMigration") }}
                  </h2>
                  <div class="co-settings-list">
                    <div
                      class="co-settings-row"
                      data-testid="settings-migration-row"
                      aria-labelledby="migration-heading"
                    >
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-blue" aria-hidden="true">
                                <PhPackage :size="14" weight="duotone" />
                              </span>
                              <h3 id="migration-heading" class="co-sr-title">
                                {{ t("settings.migrationTitle") }}
                              </h3>
                            </span>
                            <ATag color="blue" :bordered="false">
                              {{ t("settings.migrationEncryptedTag") }}
                            </ATag>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("settings.migrationDesc") }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <ASpace :size="8" wrap>
                            <AButton
                              type="primary"
                              :loading="migrationExportLoading"
                              :disabled="busy || !!state.operation"
                              data-testid="settings-migration-export-btn"
                              @click="openMigrationExport"
                            >
                              <template #icon>
                                <PhUploadSimple :size="15" />
                              </template>
                              {{ t("settings.migrationExportBtn") }}
                            </AButton>
                            <AButton
                              :loading="migrationPickingFile"
                              :disabled="busy || !!state.operation"
                              data-testid="settings-migration-import-btn"
                              @click="openMigrationImport"
                            >
                              <template #icon>
                                <PhDownloadSimple :size="15" />
                              </template>
                              {{ t("settings.migrationImportBtn") }}
                            </AButton>
                          </ASpace>
                        </div>
                      </div>
                    </div>
                  </div>
                </section>

                <!-- Section 5: Desktop -->
                <section
                  v-if="
                    capabilities.can_manage_close_behavior ||
                    capabilities.can_manage_shortcuts ||
                    capabilities.can_self_update
                  "
                  class="co-settings-group-block"
                  aria-labelledby="settings-sec-desktop"
                >
                  <h2 id="settings-sec-desktop" class="co-settings-section-label">
                    {{ t("settings.sectionDesktop") }}
                  </h2>
                  <div class="co-settings-list">
                    <!-- Row 4.1: Main Window Close Behavior -->
                    <div
                      v-if="capabilities.can_manage_close_behavior"
                      class="co-settings-row"
                      data-testid="settings-close-behavior-row"
                      aria-labelledby="close-behavior-heading"
                    >
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-slate" aria-hidden="true">
                                <PhAppWindow :size="14" weight="duotone" />
                              </span>
                              <h3 id="close-behavior-heading" class="co-sr-title">
                                {{ t("settings.closeBehaviorTitle") }}
                              </h3>
                            </span>
                          </div>
                          <p class="co-sr-desc">
                            {{
                              closeBehaviorState.can_background
                                ? t("settings.closeBehaviorDesc")
                                : t("settings.closeBehaviorDescNoTray")
                            }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <ASegmented
                            :value="closeBehaviorState.behavior"
                            :options="closeBehaviorSegmentedOptions"
                            :disabled="savingCloseBehavior"
                            :aria-label="t('settings.closeBehaviorAria')"
                            data-testid="settings-close-behavior-segmented"
                            @change="handleChangeCloseBehavior"
                          >
                            <template #label="{ value, title, payload }">
                              <span class="co-seg-item">
                                <PhQuestion v-if="value === 'ask'" :size="15" />
                                <PhSignOut
                                  v-else-if="value === 'quit'"
                                  :size="15"
                                />
                                <PhTray v-else :size="15" />
                                <span>{{ payload?.label ?? title ?? value }}</span>
                              </span>
                            </template>
                          </ASegmented>
                        </div>
                      </div>
                    </div>

                    <!-- Row 4.2: Desktop & Start Menu Shortcuts -->
                    <div
                      v-if="capabilities.can_manage_shortcuts"
                      class="co-settings-row"
                      aria-labelledby="shortcuts-heading"
                    >
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-slate" aria-hidden="true">
                                <PhDesktop :size="14" weight="duotone" />
                              </span>
                              <h3 id="shortcuts-heading" class="co-sr-title">
                                {{ t("settings.shortcutsTitle") }}
                              </h3>
                            </span>
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

                    <!-- Row 4.2: Software Updates -->
                    <div
                      v-if="capabilities.can_self_update"
                      class="co-settings-row"
                      data-testid="settings-update-row"
                      aria-labelledby="updates-heading"
                    >
                      <div class="co-sr-main">
                        <div class="co-sr-info">
                          <div class="co-sr-title-line">
                            <span class="co-sr-title-group">
                              <span class="co-sr-icon is-blue" aria-hidden="true">
                                <PhCloudArrowDown :size="14" weight="duotone" />
                              </span>
                              <h3 id="updates-heading" class="co-sr-title">
                                {{ t("update.settingsTitle") }}
                              </h3>
                            </span>
                            <ATag
                              v-if="updateStatusTag"
                              :color="updateStatusTag.color"
                              data-testid="settings-update-status-tag"
                            >
                              {{ updateStatusTag.label }}
                            </ATag>
                            <ATag
                              v-if="showUpdateReadOnlySecondaryTag"
                              color="warning"
                            >
                              {{ t("update.statusReadOnly") }}
                            </ATag>
                          </div>
                          <p class="co-sr-desc">
                            {{ t("update.settingsDesc") }}
                          </p>
                          <p
                            class="co-sr-meta"
                            data-testid="settings-update-last-checked"
                          >
                            {{ updateLastCheckedText }}
                          </p>
                        </div>
                        <div class="co-sr-control">
                          <ASpace :size="8" wrap>
                            <AButton
                              v-if="updateState.status === 'ready_to_restart'"
                              type="primary"
                              data-testid="settings-update-restart-btn"
                              @click.stop="openUpdatePopover"
                            >
                              {{
                                updateState.is_rollback
                                  ? t("update.btnRestartRollback")
                                  : t("update.btnRestartNow")
                              }}
                            </AButton>
                            <AButton
                              v-else-if="
                                updateState.status === 'downloading' ||
                                updateState.status === 'installing'
                              "
                              type="primary"
                              data-testid="settings-update-view-btn"
                              @click.stop="openUpdatePopover"
                            >
                              {{ t("update.btnViewUpdate") }}
                            </AButton>
                            <template
                              v-else-if="updateState.status === 'available'"
                            >
                              <AButton
                                type="primary"
                                data-testid="settings-update-now-btn"
                                @click.stop="openUpdatePopover"
                              >
                                {{ t("update.btnUpdateNow") }}
                              </AButton>
                              <AButton
                                :loading="isCheckingUpdate"
                                data-testid="settings-update-check-btn"
                                @click="handleCheckForUpdate(false)"
                              >
                                {{
                                  isCheckingUpdate
                                    ? t("update.btnChecking")
                                    : t("update.btnCheck")
                                }}
                              </AButton>
                            </template>
                            <AButton
                              v-else
                              :loading="isCheckingUpdate"
                              :disabled="updateState.status === 'disabled'"
                              data-testid="settings-update-check-btn"
                              @click="handleCheckForUpdate(false)"
                            >
                              {{
                                isCheckingUpdate
                                  ? t("update.btnChecking")
                                  : t("update.btnCheck")
                              }}
                            </AButton>
                            <AButton
                              type="link"
                              size="small"
                              data-testid="settings-update-releases-btn"
                              @click="handleOpenReleasePage()"
                            >
                              {{ t("update.btnOpenReleases") }}
                            </AButton>
                          </ASpace>
                        </div>
                      </div>

                      <div class="co-sr-subpanel co-update-prefs">
                        <div class="co-sr-toggle-row">
                          <div class="co-sr-toggle-copy">
                            <span class="co-sr-toggle-title">
                              {{ t("update.prefAutoCheckLabel") }}
                            </span>
                            <span class="co-sr-toggle-desc">
                              {{ t("update.prefAutoCheckDesc") }}
                            </span>
                          </div>
                          <ASwitch
                            size="small"
                            :checked="updateState.auto_check"
                            :aria-label="t('update.prefAutoCheckLabel')"
                            :disabled="
                              savingUpdatePrefs ||
                              updateState.status === 'disabled'
                            "
                            data-testid="settings-update-auto-check-switch"
                            @change="
                              (v) =>
                                handleToggleUpdatePref('auto_check', Boolean(v))
                            "
                          />
                        </div>
                        <div class="co-sr-toggle-row">
                          <div class="co-sr-toggle-copy">
                            <span class="co-sr-toggle-title">
                              {{ t("update.prefPrereleaseLabel") }}
                            </span>
                            <span class="co-sr-toggle-desc">
                              {{ t("update.prefPrereleaseDesc") }}
                            </span>
                          </div>
                          <ASwitch
                            size="small"
                            :checked="updateState.include_prerelease"
                            :aria-label="t('update.prefPrereleaseLabel')"
                            :disabled="
                              savingUpdatePrefs ||
                              updateState.status === 'disabled'
                            "
                            data-testid="settings-update-prerelease-switch"
                            @change="
                              (v) =>
                                handleToggleUpdatePref(
                                  'include_prerelease',
                                  Boolean(v),
                                )
                            "
                          />
                        </div>
                      </div>
                    </div>
                  </div>
                </section>

                <!-- Section 6: About -->
                <section
                  class="co-settings-group-block"
                  aria-labelledby="settings-sec-about"
                  data-testid="settings-about-section"
                >
                  <h2 id="settings-sec-about" class="co-settings-section-label">
                    {{ t("settings.sectionAbout") }}
                  </h2>
                  <div class="co-settings-list">
                    <div
                      class="co-settings-row co-about-card"
                      data-testid="settings-about-card"
                    >
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
                      <div
                        class="co-about-notice"
                        data-testid="settings-about-noncommercial-notice"
                      >
                        <div class="co-about-notice-title">
                          {{ t("settings.aboutNonCommercialTitle") }}
                        </div>
                        <p class="co-about-notice-desc">
                          {{ t("settings.aboutNonCommercialDesc") }}
                        </p>
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
        <div
          v-if="dockTask"
          :class="['co-task-dock', { 'is-collapsed': dockTaskCollapsed }]"
          data-testid="dock-task"
          :data-collapsed="dockTaskCollapsed ? 'true' : 'false'"
        >
          <ACard
            size="small"
            :class="[
              'co-task-card co-task-bar',
              { 'is-collapsed': dockTaskCollapsed },
            ]"
          >
            <button
              v-if="dockTaskCollapsed"
              ref="dockTaskExpandBtnRef"
              type="button"
              class="co-task-mini-bar"
              data-testid="dock-task-expand-btn"
              :aria-expanded="false"
              :aria-label="
                t('task.dockExpandAria', { title: dockTaskCompactSummary })
              "
              @click="setDockTaskCollapsed(false)"
            >
              <span class="co-task-mini-row">
                <ASpin
                  size="small"
                  class="co-task-mini-spin"
                  aria-hidden="true"
                />
                <span class="co-task-mini-copy">
                  <strong
                    :class="[
                      'co-task-mini-title',
                      {
                        'has-msg': Boolean(dockTaskMessageText),
                        'is-mobile-hidden': Boolean(dockTaskMessageText),
                      },
                    ]"
                  >
                    {{ taskBarTitle(dockTask.kind) }}
                  </strong>
                  <span
                    v-if="dockTaskMessageText"
                    class="co-task-mini-sep"
                    aria-hidden="true"
                    >·</span
                  >
                  <span v-if="dockTaskMessageText" class="co-task-mini-msg">
                    {{ dockTaskMessageText }}
                  </span>
                </span>
                <span class="co-task-mini-meta">
                  <span
                    v-if="dockTaskMiniSecondary"
                    class="co-task-mini-stat co-tabular"
                    data-testid="dock-task-mini-stat"
                  >
                    {{ dockTaskMiniSecondary }}
                  </span>
                  <span
                    v-if="dockTaskHasProgress"
                    class="co-task-mini-percent co-tabular"
                    data-testid="dock-task-mini-percent"
                  >
                    {{ dockTaskPercent }}%
                  </span>
                  <span class="co-task-mini-chevron" aria-hidden="true">
                    <PhCaretUp :size="13" weight="bold" />
                  </span>
                </span>
              </span>
              <span class="co-task-mini-progress-track" aria-hidden="true">
                <span
                  :class="[
                    'co-task-mini-progress-fill',
                    { 'is-indeterminate': !dockTaskHasProgress },
                  ]"
                  :style="
                    dockTaskHasProgress
                      ? { width: `${dockTaskPercent}%` }
                      : undefined
                  "
                />
              </span>
            </button>

            <div v-else class="co-task-row">
              <ASpin size="small" class="co-task-spin" />
              <div class="co-task-main">
                <div class="co-task-head">
                  <strong>{{ taskBarTitle(dockTask.kind) }}</strong>
                  <span v-if="dockTaskMessageText" class="co-muted co-task-msg">{{
                    dockTaskMessageText
                  }}</span>
                  <span
                    v-if="dockTaskElapsedLabel"
                    class="co-task-elapsed co-tabular"
                    data-testid="dock-task-elapsed"
                  >
                    <PhClock :size="13" aria-hidden="true" />
                    <span>{{
                      t("task.elapsedLive", { duration: dockTaskElapsedLabel })
                    }}</span>
                  </span>
                </div>
                <div v-if="dockTask.download" class="co-download-stats">
                  <b class="co-tabular">
                    {{
                      typeof dockTask.download.speed_bps === "number" &&
                      Number.isFinite(dockTask.download.speed_bps) &&
                      dockTask.download.speed_bps > 0
                        ? `${(dockTask.download.speed_bps / 1024 ** 2).toFixed(1)} MiB/s`
                        : "0.0 MiB/s"
                    }}
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
                  v-if="dockTaskHasProgress"
                  :percent="dockTaskPercent"
                  size="small"
                  status="active"
                />
              </div>
              <div class="co-task-actions">
                <ColabOneTooltip
                  :title="t('task.dockCollapse')"
                  :disabled="isCompactTaskDockViewport"
                  placement="top"
                >
                  <AButton
                    ref="dockTaskCollapseBtnRef"
                    size="small"
                    class="co-task-collapse-btn"
                    data-testid="dock-task-collapse-btn"
                    :aria-expanded="true"
                    :aria-label="t('task.dockCollapse')"
                    @click="setDockTaskCollapsed(true)"
                  >
                    <span class="co-task-collapse-inner">
                      <PhCaretDown :size="13" weight="bold" aria-hidden="true" />
                      <span class="co-task-collapse-label">{{
                        t("task.dockCollapseShort")
                      }}</span>
                    </span>
                  </AButton>
                </ColabOneTooltip>
                <AButton
                  danger
                  size="small"
                  :disabled="busy"
                  @click="act('task.cancel', { id: dockTask.id })"
                >
                  {{ t("common.cancel") }}
                </AButton>
              </div>
            </div>
          </ACard>
        </div>
        <!-- Mobile Fixed Bottom Tab Bar (visible only on narrow mobile viewports <= 680px) -->
        <nav
          class="co-mobile-bottom-nav"
          data-testid="mobile-bottom-nav"
          :aria-label="t('nav.mainAria')"
        >
          <button
            v-for="item in nav"
            :key="item.id"
            type="button"
            class="co-mobile-tab-item"
            :class="{ 'is-active': page === item.id }"
            :aria-current="page === item.id ? 'page' : undefined"
            :aria-label="item.label"
            @click="selectPage(item.id)"
          >
            <span class="co-mobile-tab-icon-wrap" aria-hidden="true">
              <component
                :is="item.icon"
                :size="20"
                :weight="page === item.id ? 'fill' : 'regular'"
                class="co-mobile-tab-icon"
              />
              <span
                v-if="isNavLive(item.id)"
                class="co-mobile-tab-live-dot"
                :style="{ backgroundColor: navDotColor(item.id) }"
              ></span>
            </span>
            <span class="co-mobile-tab-label">{{ item.label }}</span>
          </button>
        </nav>
      </ALayout>

      <!-- Inline Fallback Logs Column (only in non-desktop web previews when standalone OS window is unavailable) -->
      <LogDrawer
        v-if="!logWindowStandalone"
        :open="logsOpen"
        :width="logDrawerWidth"
        :mobile-overlay="viewportWidth <= 768"
        :loading="!snapshotLoaded"
        :logs="state.logs || []"
        :current-activity="currentActivity"
        :running="!!(state.operation || task)"
        :active-speed="activeApiSpeed"
        :active-elapsed="activeTaskElapsedLabel"
        :privacy-context="privacyContext"
        @update:width="onLogDrawerWidthChange"
        @close="setLogsOpen(false)"
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
        :privacy-context="privacyContext"
        @pause="toastManager.pause"
        @resume="toastManager.resume"
        @dismiss="toastManager.dismiss"
      />

      <!-- Global Hover Tooltip for Truncated Select Triggers & Options -->
      <ColabOneSelectTooltipHost />

      <!-- Modal: Selective Instance Release Review -->
      <AModal
        v-model:open="showRelease"
        :title="t('device.releaseModalTitle')"
        :width="580"
        :mask-closable="!busy"
        :closable="!busy"
        :keyboard="!busy"
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
                  'is-selected': selectedReleaseIds.includes(instance.id),
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
        :keyboard="!busy"
        :footer="null"
        :z-index="1050"
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

      <!-- Modal: Main Window Close Behavior Prompt -->
      <ColabOneCloseModal
        :open="closeModalOpen"
        :default-choice="closeModalDefaultChoice"
        :can-background="closeBehaviorState.can_background"
        :submitting="closeModalSubmitting"
        :has-active-operation="hasActiveCloseBlockingOp"
        :suppress-leave-transition="closeModalSuppressLeave"
        @update:open="closeModalOpen = $event"
        @confirm="handleConfirmCloseModal"
      />

      <!-- Modal: One-Click Encrypted Migration (Export / Import) -->
      <ColabOneMigrationModal
        :open="migrationModalOpen"
        :mode="migrationModalMode"
        :state="state"
        :export-summary="migrationExportSummary"
        :export-loading="migrationExportLoading"
        :export-submitting="migrationExportSubmitting"
        :export-error="migrationExportError"
        :import-step="migrationImportStep"
        :picked-file="migrationPickedFile"
        :import-preview="migrationImportPreview"
        :import-decrypting="migrationImportDecrypting"
        :import-applying="migrationImportApplying"
        :import-error="migrationImportError"
        @update:open="handleMigrationModalOpenChange"
        @export="handleMigrationExport"
        @repick="openMigrationImport"
        @decrypt="handleMigrationDecrypt"
        @apply="handleMigrationApply"
      />

      <!-- Modal: Workflow Web Share (Create & Manage) -->
      <ColabOneWorkflowShareModal
        v-if="capabilities.can_manage_workflow_shares"
        :open="workflowShareModalOpen"
        :workflows="state.workflows || []"
        :web-console-state="effectiveWebConsoleState"
        :shares-state="workflowSharesState"
        :can-manage-web-console="capabilities.can_manage_web_console"
        :loading="!workflowSharesLoaded"
        :creating="workflowShareCreating"
        :mutating="workflowShareMutating"
        :enabling-web-console="webConsoleSaving"
        :revoking-id="workflowShareRevokingId"
        :deleting-id="workflowShareDeletingId"
        :last-created-share="workflowShareLastCreated"
        :last-created-copied="workflowShareLastCreatedCopied"
        :copy-link="handleWorkflowShareCopyLink"
        @update:open="workflowShareModalOpen = $event"
        @create="handleWorkflowShareCreate"
        @revoke="handleWorkflowShareRevoke"
        @delete="handleWorkflowShareDelete"
        @enable-web-console="handleWorkflowShareEnableWebConsole"
        @open-web-console-settings="handleWorkflowShareOpenSettings"
      />
    </ALayout>
  </AConfigProvider>
</template>

<style scoped>
/* Root Shell — horizontal row so LogDrawer expands strictly to the right */
.co-shell {
  height: 100%;
  height: 100dvh;
  width: 100vw;
  max-width: 100%;
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
  height: 100%;
  height: 100dvh;
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
  overflow-x: clip;
  overflow-y: visible;
  padding: 4px;
  margin: -4px;
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

.co-brand-version-anchor {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
  padding: 2px 3px 2px 0;
}

.co-brand-version {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  flex-shrink: 0;
  margin-top: 3px;
  padding: 3px 8px;
  border-radius: var(--co-radius-pill);
  border: 1px solid var(--co-version-border);
  background: var(--co-version-bg);
  color: var(--co-version-text);
  font-family: inherit;
  font-size: 11px;
  font-weight: 600;
  line-height: 1.1;
  letter-spacing: 0.015em;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  user-select: none;
  cursor: pointer;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    color 0.16s ease,
    transform 0.12s ease;
}

.co-brand-version:hover,
.co-brand-version.is-open {
  background: var(--co-hover);
  border-color: var(--co-border);
  color: var(--co-text);
}

.co-brand-version:active {
  transform: scale(0.97);
}

.co-brand-version:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-brand-version-dot {
  position: absolute;
  top: -2px;
  right: -2px;
  width: 7px;
  height: 7px;
  border-radius: 9999px;
  background: var(--co-danger);
  box-shadow: 0 0 0 2px var(--co-canvas);
  pointer-events: none;
}

.co-brand-version.is-update-downloading {
  background: var(--co-selected);
  border-color: rgba(59, 130, 246, 0.34);
  color: var(--co-accent-strong);
}

.co-brand-version.is-update-downloading:hover {
  border-color: rgba(59, 130, 246, 0.56);
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

:root[data-theme="dark"] .co-brand-version.is-update-downloading {
  border-color: rgba(96, 165, 250, 0.42);
}

.co-brand-version.is-update-ready {
  background: var(--co-success-bg);
  border-color: var(--co-success-border);
  color: var(--co-success-text);
}

.co-brand-version.is-update-ready:hover {
  background: var(--co-success-bg);
  border-color: var(--co-success);
  color: var(--co-success-text);
}

.co-brand-version-text {
  line-height: 1.1;
}

.co-nav-update-pill {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 1.5px 6px;
  margin-right: -3px;
  border-radius: var(--co-radius-pill);
  background: var(--co-accent);
  color: var(--co-on-accent);
  font-size: 10px;
  font-weight: 600;
  line-height: 1.25;
  letter-spacing: 0.01em;
}

.co-brand-version.is-update-ready .co-nav-update-pill {
  background: var(--co-success);
  color: #ffffff;
}

.co-nav-update-dot {
  width: 5px;
  height: 5px;
  border-radius: 9999px;
  background: currentColor;
  flex-shrink: 0;
}

.co-brand-version.is-update-ready .co-nav-update-dot,
.co-brand-version.is-update-downloading .co-nav-update-dot {
  animation: co-update-pulse 1.8s ease-in-out infinite;
}

@keyframes co-update-pulse {
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

/* ColabOne Centered Floating Capsule Pill Navigation */
.co-nav-center {
  display: flex;
  justify-content: center;
  min-width: 0;
  flex-shrink: 0;
}

.co-nav-pill-track {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px;
  border-radius: 9999px;
  background: var(--co-track);
}

:root[data-theme="dark"] .co-nav-pill-track {
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

/* ColabOne NavMenu: clean text-only pills on desktop, icon-only pills below 960px */
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

:root[data-theme="dark"] .co-nav-circle-btn,
:root[data-theme="dark"] .co-logs-trigger {
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

.co-nav-privacy-btn.is-active {
  background: var(--co-selected);
  color: var(--co-accent-strong);
  box-shadow: inset 0 0 0 1px rgba(59, 130, 246, 0.34);
}

.co-nav-privacy-btn.is-active:hover:not(:disabled) {
  background: var(--co-selected);
  color: var(--co-accent-strong);
  box-shadow: inset 0 0 0 1px rgba(59, 130, 246, 0.54);
}

:root[data-theme="dark"] .co-nav-privacy-btn.is-active {
  border-color: rgba(96, 165, 250, 0.42);
  box-shadow: inset 0 0 0 1px rgba(96, 165, 250, 0.24);
}

:root[data-theme="dark"] .co-nav-privacy-btn.is-active:hover:not(:disabled) {
  border-color: rgba(96, 165, 250, 0.64);
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
  flex-wrap: wrap;
  gap: 4px 8px;
  min-width: 0;
  flex: 1 1 auto;
  overflow: visible;
}

.co-runtime-tag {
  margin-inline-end: 0;
  font-weight: 500;
  white-space: nowrap;
  flex-shrink: 0;
}

.co-gpu-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-secondary-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 72px;
  flex: 0 1 auto;
}

.co-location-tag {
  margin-inline-end: 0;
  white-space: nowrap;
  flex-shrink: 0;
}

.co-model-chip {
  height: 28px;
  max-width: 220px;
  min-width: 84px;
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

@container co-status-bar (max-width: 1420px) {
  .co-status-bar-inner {
    flex-wrap: wrap;
    row-gap: 6px;
  }

  .co-topbar-left {
    flex: 1 1 auto;
    min-width: 0;
  }

  .co-topbar-right {
    margin-left: auto;
  }
}

.co-instance-select {
  min-width: 130px;
  max-width: 240px;
  flex-shrink: 1;
}

.co-topbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  min-width: 0;
  max-width: 100%;
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

.co-upgrade-action-wrap {
  display: inline-flex;
}

.co-upgrade-action-wrap.is-disabled {
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

.co-content.is-settings-page {
  scroll-padding-bottom: 96px;
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
  container-type: inline-size;
  container-name: co-settings;
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

:root[data-theme="dark"] .co-settings-floating-bar {
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

@container co-navbar (1180px < width <= 1340px) {
  .co-navbar-inner:has(.co-nav-update-pill, .co-web-signout-btn) {
    display: flex;
    justify-content: space-between;
    gap: 12px;
  }

  .co-navbar-inner:has(.co-nav-update-pill, .co-web-signout-btn) .co-nav-left {
    flex: 0 1 auto;
  }

  .co-navbar-inner:has(.co-nav-update-pill, .co-web-signout-btn) .co-nav-center,
  .co-navbar-inner:has(.co-nav-update-pill, .co-web-signout-btn) .co-nav-right {
    flex: 0 0 auto;
  }

  .co-brand-version:has(.co-nav-update-pill) {
    padding: 2px 7px;
    gap: 5px;
  }

  .co-nav-update-pill {
    padding: 1px 5px;
    margin-right: -2px;
  }
}

@container co-navbar (1180px < width <= 1260px) {
  .co-navbar-inner:has(.co-nav-update-pill, .co-web-signout-btn) .co-logs-trigger {
    width: 40px;
    padding: 0;
    justify-content: center;
  }

  .co-navbar-inner:has(.co-nav-update-pill, .co-web-signout-btn) .co-logs-trigger-label {
    display: none;
  }
}

@container co-navbar (max-width: 1180px) {
  .co-navbar-inner {
    display: flex;
    justify-content: space-between;
    gap: 12px;
  }

  .co-nav-left {
    flex: 0 1 auto;
  }

  .co-nav-center {
    flex: 0 0 auto;
  }

  .co-nav-right {
    flex: 0 0 auto;
  }

  .co-brand {
    gap: 10px;
    font-size: 22px;
  }

  .co-brand-icon {
    width: 40px;
    height: 40px;
  }

  .co-brand-version:has(.co-nav-update-pill) {
    padding: 2px 7px;
    gap: 5px;
  }

  .co-nav-update-pill {
    padding: 1px 5px;
    margin-right: -2px;
  }

  .co-nav-pill-item {
    padding: 0 15px;
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

@container co-navbar (max-width: 1080px) {
  .co-navbar-inner {
    height: 64px;
  }

  .co-brand {
    gap: 8px;
    font-size: 20px;
  }

  .co-brand-icon {
    width: 36px;
    height: 36px;
  }

  .co-brand-version {
    margin-top: 2px;
    padding: 2px 7px;
    gap: 5px;
    font-size: 10.5px;
  }

  .co-nav-update-pill {
    padding: 1px 5px;
    font-size: 9.5px;
  }

  .co-web-mode-badge {
    display: none;
  }

  .co-nav-pill-item {
    padding: 0 13px;
  }

  .co-nav-right {
    gap: 6px;
  }
}

@container co-navbar (max-width: 960px) {
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

@container co-navbar (max-width: 860px) {
  .co-brand-version:has(.co-nav-update-pill) {
    padding: 2px 4px;
    gap: 0;
  }

  .co-brand-version:has(.co-nav-update-pill) .co-brand-version-text {
    display: none;
  }

  .co-brand-version:has(.co-nav-update-pill) .co-nav-update-pill {
    margin-right: 0;
  }
}

@container co-navbar (max-width: 800px) {
  .co-brand-version.is-static:not(.is-open) {
    display: none;
  }

  .co-nav-update-pill {
    padding: 1px 4px;
    gap: 3px;
    font-size: 9px;
  }
}

@container co-navbar (max-width: 760px) {
  .co-navbar-inner {
    padding-inline: 16px;
    gap: 10px;
  }

  .co-brand {
    font-size: 19px;
    gap: 8px;
  }

  .co-brand-icon {
    width: 34px;
    height: 34px;
  }

  .co-nav-pill-item {
    width: 36px;
    height: 36px;
  }

  .co-nav-right {
    gap: 6px;
  }

  .co-nav-circle-btn,
  .co-logs-trigger {
    width: 36px;
    height: 36px;
  }

  .co-nav-right :deep(.co-locale-trigger) {
    height: 36px;
    padding: 0 9px 0 10px;
    gap: 4px;
  }
}

@container co-navbar (max-width: 700px) {
  .co-brand-version:not(.is-open) {
    display: none;
  }
}

@container co-navbar (max-width: 680px) {
  .co-navbar-inner {
    gap: 8px;
    padding-inline: 14px;
  }

  .co-brand-text {
    display: none;
  }

  .co-nav-right {
    gap: 5px;
  }

  .co-nav-circle-btn,
  .co-logs-trigger {
    width: 34px;
    height: 34px;
  }

  .co-nav-right :deep(.co-locale-trigger) {
    height: 34px;
    padding: 0 8px 0 9px;
    gap: 4px;
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

.co-release-control-row {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-cu-limit-editor {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-cu-limit-input {
  width: 102px;
}

.co-cu-limit-input :deep(.ant-input-number-input) {
  font-variant-numeric: tabular-nums;
}

.co-cu-limit-unit {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-muted);
  white-space: nowrap;
}

.co-cu-limit-apply-btn {
  height: 30px;
  padding-inline: 10px;
}

.co-cu-limit-warn {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-warning-text, var(--co-warning));
}

.co-sr-release-control {
  flex-shrink: 0;
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

.co-policy-live.is-warning {
  color: var(--co-warning-text, var(--co-warning));
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

.co-drive-info {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  flex: 1 1 200px;
  min-width: 0;
}

.co-drive-icon {
  color: var(--co-accent);
  flex-shrink: 0;
  margin-top: 2px;
}

.co-drive-text {
  flex: 1;
  min-width: 0;
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

.co-instance-row.is-active {
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
  max-width: 100%;
  overflow-wrap: anywhere;
  word-break: break-word;
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
  flex-wrap: wrap;
  gap: 10px 18px;
}

.co-sr-main > .co-sr-info {
  flex: 1 1 240px;
}

.co-sr-info {
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

.co-sr-title-group {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
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

:root[data-theme="dark"] .co-sr-icon.is-violet {
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
  min-width: 0;
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
  flex: 0 1 auto;
  min-width: 0;
  max-width: 100%;
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

.co-sr-attempts-wrap {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.co-sr-attempts-input {
  width: 108px;
}

.co-sr-attempts-input :deep(.ant-input-number-input) {
  font-variant-numeric: tabular-nums;
}

.co-sr-unit {
  font-size: 12.5px;
  color: var(--co-muted);
  white-space: nowrap;
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

.co-sr-meta {
  margin: 2px 0 0;
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--co-muted);
  font-variant-numeric: tabular-nums;
}

.co-update-prefs {
  padding: 4px 14px;
  gap: 0;
}

.co-sr-toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 0;
}

.co-sr-toggle-row + .co-sr-toggle-row {
  border-top: 1px solid var(--co-border-secondary);
}

.co-sr-toggle-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-sr-toggle-title {
  font-size: 13px;
  font-weight: 500;
  line-height: 1.4;
  color: var(--co-text);
}

.co-sr-toggle-desc {
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-muted);
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

  .co-sr-main > .co-sr-info,
  .co-sr-info {
    flex: none;
  }

  .co-sr-control {
    justify-content: flex-start;
    flex-wrap: wrap;
  }

  .co-sr-control .co-release-control-row {
    display: flex;
    width: 100%;
  }

  .co-sr-control-input,
  .co-sr-select-gpu,
  .co-sr-select-idle {
    width: 100%;
  }
}

@container co-settings (max-width: 680px) {
  .co-sr-main {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .co-sr-main > .co-sr-info,
  .co-sr-info {
    flex: none;
  }

  .co-sr-control {
    justify-content: flex-start;
    flex-wrap: wrap;
  }

  .co-sr-control .co-release-control-row {
    display: flex;
    width: 100%;
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

:root[data-theme="dark"] .co-head-icon-badge.is-violet {
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

.co-assign-proxy-test-wrap {
  display: inline-flex;
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
  flex-wrap: wrap;
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

.co-about-notice {
  margin-top: 2px;
  padding-top: 12px;
  border-top: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-about-notice-title {
  font-size: 13px;
  font-weight: 600;
  line-height: 1.45;
  color: var(--co-text);
}

.co-about-notice-desc {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.65;
  color: var(--co-muted);
}

/* Active Task Dock */
.co-task-dock {
  flex-shrink: 0;
  padding: 10px var(--co-container-pad) 12px;
  background: var(--co-surface);
  border-top: 1px solid var(--co-border-secondary);
}

.co-task-dock.is-collapsed {
  padding: 6px var(--co-container-pad);
}

.co-task-card {
  max-width: calc(var(--co-container-max) - 2 * var(--co-container-pad));
  margin: 0 auto;
  border-color: var(--co-accent);
  background: var(--co-subtle);
}

.co-task-card.is-collapsed {
  overflow: hidden;
  border-radius: 10px;
}

.co-task-card.is-collapsed :deep(.ant-card-body) {
  padding: 0;
}

.co-task-mini-bar {
  position: relative;
  display: block;
  width: 100%;
  appearance: none;
  border: none;
  background: transparent;
  color: inherit;
  font-family: inherit;
  text-align: left;
  padding: 8px 10px 10px;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: background-color 0.14s ease;
}

.co-task-mini-bar:hover {
  background: var(--co-soft-hover);
}

.co-task-mini-bar:active {
  background: var(--co-hover);
}

.co-task-mini-bar:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: -2px;
  border-radius: inherit;
}

.co-task-mini-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  line-height: 1.25;
}

.co-task-mini-spin {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  transform: scale(0.84);
}

.co-task-mini-copy {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  overflow: hidden;
  white-space: nowrap;
}

.co-task-mini-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1 1 auto;
  min-width: 0;
}

.co-task-mini-title.has-msg {
  flex: 0 1 auto;
  max-width: 46%;
}

.co-task-mini-sep {
  color: var(--co-muted);
  flex-shrink: 0;
}

.co-task-mini-msg {
  font-size: 12.5px;
  color: var(--co-secondary-text);
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-task-mini-meta {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  margin-left: auto;
}

.co-task-mini-stat {
  font-size: 11.5px;
  color: var(--co-muted);
  white-space: nowrap;
}

.co-task-mini-percent {
  font-size: 12px;
  font-weight: 650;
  color: var(--co-accent-strong);
  white-space: nowrap;
}

.co-task-mini-chevron {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 9999px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-secondary-text);
  flex-shrink: 0;
  transition:
    color 0.14s ease,
    border-color 0.14s ease,
    background-color 0.14s ease;
}

.co-task-mini-bar:hover .co-task-mini-chevron {
  color: var(--co-accent-strong);
  border-color: var(--co-accent);
  background: var(--co-selected);
}

.co-task-mini-progress-track {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 3px;
  background: var(--co-track);
  overflow: hidden;
  pointer-events: none;
}

.co-task-mini-progress-fill {
  display: block;
  height: 100%;
  background: var(--co-accent);
  border-radius: 0 2px 2px 0;
  transition: width 0.28s cubic-bezier(0.22, 1, 0.36, 1);
}

.co-task-mini-progress-fill.is-indeterminate {
  width: 34%;
  border-radius: 2px;
  animation: co-task-mini-indeterminate 1.4s ease-in-out infinite;
}

@keyframes co-task-mini-indeterminate {
  0% {
    transform: translateX(-110%);
  }
  100% {
    transform: translateX(310%);
  }
}

.co-task-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.co-task-spin {
  flex-shrink: 0;
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

.co-task-msg {
  min-width: 0;
  overflow-wrap: anywhere;
}

.co-task-elapsed {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 1px 8px;
  border-radius: 9999px;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-secondary-text);
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  white-space: nowrap;
}

.co-download-stats {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-task-actions {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.co-task-collapse-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding-inline: 6px;
}

.co-task-collapse-inner {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  line-height: 1;
}

.co-task-collapse-label {
  display: none;
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

.co-release-item.is-selected {
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

.co-panel-host :deep(.co-async-panel-shell) {
  width: 100%;
  max-width: var(--co-container-max);
  margin: 0 auto;
  padding: 16px var(--co-container-pad) 32px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-panel-host :deep(.co-async-panel-head) {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-panel-host :deep(.co-async-panel-card) {
  border-radius: var(--co-radius-md);
  background: var(--co-skeleton-card-bg);
}

.co-panel-host :deep(.co-async-panel-card-body) {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-panel-host :deep(.co-async-panel-error-card) {
  border-radius: var(--co-radius-md);
  background: var(--co-surface);
}

.co-panel-host :deep(.co-async-panel-error-body) {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 4px;
}

.co-panel-host :deep(.co-async-panel-error-title) {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-panel-host :deep(.co-async-panel-error-desc) {
  margin: 0;
  font-size: 13px;
  color: var(--co-secondary-text);
  word-break: break-word;
}

.co-brand-web-meta {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-brand-version.is-static {
  cursor: default;
}

.co-web-mode-badge {
  margin: 0;
  font-size: 11px;
  line-height: 18px;
  border-radius: 999px;
  white-space: nowrap;
  flex-shrink: 0;
}

.co-web-settings-notice {
  border-radius: var(--co-radius-md);
}

@container co-navbar (max-width: 520px) {
  .co-navbar-inner {
    gap: 6px;
    padding-inline: 12px;
  }

  .co-nav-right {
    gap: 5px;
  }
}

/* Mobile Bottom Tab Bar — hidden on desktop (> 680px), native-feel bar on phones */
.co-mobile-bottom-nav {
  display: none;
}

@media (max-width: 680px) {
  .co-navbar {
    padding-top: var(--co-safe-top, 0px);
    border-bottom: 1px solid var(--co-border-secondary);
  }

  .co-navbar-inner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 54px;
    gap: 8px;
    padding: 0 12px;
  }

  .co-nav-left {
    flex: 0 1 auto;
    min-width: 0;
    overflow-x: clip;
    overflow-y: visible;
    padding: 4px;
    margin: -4px;
    gap: 6px;
  }

  .co-brand {
    gap: 7px;
    padding: 2px 2px 2px 0;
    font-size: 17px;
    flex-shrink: 0;
  }

  .co-brand-icon {
    width: 28px;
    height: 28px;
  }

  /* Keep ColabOne brand text crisp and visible on mobile now that nav moves to bottom bar */
  .co-brand-text {
    display: inline-block;
  }

  .co-brand-version:not(.is-open) {
    display: none;
  }

  .co-brand-version:has(.co-nav-update-pill) {
    padding: 2px 7px;
  }

  .co-brand-version .co-nav-update-pill {
    display: none;
  }

  .co-brand-version:has(.co-nav-update-pill) .co-brand-version-text {
    display: inline;
  }

  .co-web-mode-badge {
    display: inline-flex;
    font-size: 10.5px;
    padding: 0 6px;
    min-height: 19px;
    line-height: 17px;
    white-space: nowrap;
    flex-shrink: 0;
  }

  /* Primary navigation moves cleanly to the mobile bottom tab bar */
  .co-nav-center {
    display: none;
  }

  .co-nav-right {
    flex: 0 0 auto;
    gap: 5px;
  }

  .co-nav-circle-btn,
  .co-logs-trigger {
    width: 34px;
    height: 34px;
    padding: 0;
    justify-content: center;
  }

  .co-logs-trigger-label {
    display: none;
  }

  .co-mobile-bottom-nav {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    align-items: stretch;
    flex-shrink: 0;
    width: 100%;
    padding: 6px 6px calc(6px + var(--co-safe-bottom, 0px));
    box-sizing: border-box;
    background: color-mix(in srgb, var(--co-surface) 94%, transparent);
    backdrop-filter: blur(16px);
    -webkit-backdrop-filter: blur(16px);
    border-top: 1px solid var(--co-border-secondary);
    z-index: 25;
  }

  .co-mobile-tab-item {
    appearance: none;
    border: none;
    background: transparent;
    color: var(--co-muted);
    font-family: inherit;
    min-height: 46px;
    padding: 3px 2px;
    border-radius: 10px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 3px;
    cursor: pointer;
    min-width: 0;
    transition:
      color 0.14s ease,
      transform 0.12s ease;
  }

  .co-mobile-tab-item:active {
    transform: scale(0.95);
  }

  .co-mobile-tab-item:focus-visible {
    outline: 2px solid var(--co-focus);
    outline-offset: -2px;
  }

  .co-mobile-tab-item.is-active {
    color: var(--co-accent-strong);
  }

  .co-mobile-tab-icon-wrap {
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 46px;
    height: 26px;
    border-radius: 9999px;
    transition: background-color 0.16s ease;
  }

  .co-mobile-tab-item.is-active .co-mobile-tab-icon-wrap {
    background: var(--co-selected);
    color: var(--co-accent-strong);
  }

  .co-mobile-tab-live-dot {
    position: absolute;
    top: 3px;
    right: 9px;
    width: 6px;
    height: 6px;
    border-radius: 9999px;
    box-shadow: 0 0 0 2px var(--co-surface);
  }

  .co-mobile-tab-label {
    font-size: 11px;
    font-weight: 500;
    line-height: 1.15;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .co-mobile-tab-item.is-active .co-mobile-tab-label {
    font-weight: 650;
  }

  /* Status Bar: ultra-compact 2-row grid on mobile (Row 1: status/GPU/region + idle pill; Row 2: full-width single-line billing capsule) */
  .co-status-bar-inner {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
    gap: 5px 6px;
    padding: 5px var(--co-container-pad) 6px;
    min-height: 0;
    height: auto;
  }

  .co-topbar-left {
    grid-column: 1 / 2;
    grid-row: 1;
    flex: none;
    width: 100%;
    min-width: 0;
    flex-wrap: nowrap;
    overflow-x: auto;
    overflow-y: hidden;
    scrollbar-width: none;
    -webkit-overflow-scrolling: touch;
    gap: 5px;
    padding-right: 8px;
    box-sizing: border-box;
    -webkit-mask-image: linear-gradient(
      to right,
      #000 calc(100% - 10px),
      transparent 100%
    );
    mask-image: linear-gradient(
      to right,
      #000 calc(100% - 10px),
      transparent 100%
    );
  }

  .co-topbar-left::-webkit-scrollbar {
    display: none;
  }

  .co-status-bar-inner:not(:has(.co-idle-timer)) .co-topbar-left {
    grid-column: 1 / -1;
  }

  .co-runtime-tag,
  .co-location-tag {
    min-height: 21px;
    padding: 0 7px;
    font-size: 11px;
    line-height: 19px;
    white-space: nowrap;
    flex-shrink: 0;
  }

  .co-gpu-name {
    flex: 0 1 auto;
    min-width: 52px;
    max-width: 100%;
    font-size: 12px;
  }

  .co-model-chip {
    flex-shrink: 0;
    min-width: 0;
    max-width: 118px;
    height: 21px;
    padding-inline: 7px !important;
    font-size: 11px;
  }

  .co-model-chip-text {
    max-width: 92px;
  }

  .co-instance-select {
    flex-shrink: 0;
    min-width: 116px;
    max-width: 164px;
    width: auto;
  }

  .co-topbar-right {
    display: contents;
  }

  /* Banners & Page Shell mobile padding */
  .co-banners-inner {
    padding: 4px var(--co-container-pad) 4px;
  }

  .co-page-shell {
    padding: 12px var(--co-container-pad) 24px;
  }

  .co-page-inner {
    gap: 12px;
  }

  .co-settings-page {
    padding-bottom: 84px;
  }

  .co-settings-inner {
    gap: 14px;
  }

  .co-settings-stack {
    gap: 16px;
  }

  .co-settings-row {
    padding: 14px;
  }

  .co-settings-floating-wrap {
    padding: 12px var(--co-container-pad) 10px;
  }

  .co-settings-floating-bar {
    padding: 9px 12px;
    gap: 10px;
    border-radius: 12px;
  }

  /* Device card controls: full-width selects and balanced stacking */
  .co-device-title-main,
  .co-callout-copy,
  .co-about-copy {
    min-width: 0;
    max-width: 100%;
    flex: 1 1 180px;
  }

  .co-device-subtitle,
  .co-runtime-id,
  .co-sr-main,
  .co-sr-info,
  .co-sr-title-line {
    min-width: 0;
    max-width: 100%;
    overflow-wrap: anywhere;
  }

  .co-device-head {
    align-items: flex-start;
    gap: 10px 12px;
  }

  .co-device-icon {
    width: 38px;
    height: 38px;
    border-radius: 10px;
  }

  .co-device-title-main h2 {
    font-size: 16.5px;
  }

  .co-device-controls {
    flex-direction: column;
    align-items: stretch;
    gap: 12px;
  }

  .co-device-fields {
    flex-direction: column;
    align-items: stretch;
    width: 100%;
    gap: 10px;
  }

  .co-control-field {
    width: 100%;
    min-width: 0;
  }

  .co-select-gpu,
  .co-select-idle {
    width: 100% !important;
    min-width: 0 !important;
    max-width: 100%;
  }

  .co-device-actions {
    width: 100%;
  }

  .co-drive-strip {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }

  .co-drive-info {
    flex: none;
    width: 100%;
  }

  .co-drive-text {
    min-width: 0;
  }

  .co-instances-card :deep(.ant-card-head-wrapper) {
    flex-wrap: wrap;
    gap: 8px 10px;
  }

  .co-instances-card :deep(.ant-card-head-title) {
    flex: 1 1 180px;
    min-width: 0;
    white-space: normal;
  }

  .co-instances-card :deep(.ant-card-extra) {
    margin-inline-start: auto;
    padding: 0;
  }

  .co-instance-row {
    padding: 12px;
    gap: 10px;
  }

  .co-instance-main {
    flex: 1 1 220px;
  }

  .co-instance-actions {
    width: 100%;
    justify-content: flex-end;
  }

  .co-hf-row,
  .co-assign-proxy-input-row {
    flex-wrap: wrap;
  }

  .co-hf-row > :first-child,
  .co-assign-proxy-input-row > :first-child {
    flex: 1 1 180px;
    min-width: 0;
  }

  .co-task-dock {
    padding: 8px var(--co-container-pad);
  }

  .co-task-dock.is-collapsed {
    padding: 5px var(--co-container-pad);
  }

  .co-task-dock.is-collapsed .co-task-card {
    border-radius: 10px;
  }

  .co-task-mini-bar {
    padding: 7px 10px 9px;
  }

  .co-task-mini-row {
    gap: 6px;
  }

  .co-task-mini-title.is-mobile-hidden,
  .co-task-mini-sep {
    display: none;
  }

  .co-task-mini-title,
  .co-task-mini-msg {
    font-size: 12px;
    font-weight: 560;
    color: var(--co-text);
  }

  .co-task-mini-stat {
    font-size: 11px;
    max-width: 108px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .co-task-mini-percent {
    font-size: 11.5px;
  }

  .co-task-row {
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 8px;
  }

  .co-task-spin {
    margin-top: 2px;
  }

  .co-task-elapsed {
    margin-left: 0;
  }

  .co-task-actions {
    width: 100%;
    justify-content: space-between;
    padding-top: 2px;
  }

  .co-task-collapse-btn {
    padding-inline: 9px;
  }

  .co-task-collapse-label {
    display: inline;
  }

  .co-modal-footer {
    flex-wrap: nowrap;
    align-items: center;
  }

  .co-modal-footer > :first-child {
    flex-shrink: 0;
  }

  .co-modal-footer > :last-child {
    flex: 1 1 0;
    min-width: 0;
  }
}

@media (max-width: 680px) and (max-height: 500px) {
  .co-navbar-inner {
    height: 44px;
  }

  .co-mobile-bottom-nav {
    padding: 3px 6px calc(3px + var(--co-safe-bottom, 0px));
  }

  .co-mobile-tab-item {
    min-height: 38px;
    padding: 2px;
    gap: 1px;
  }

  .co-mobile-tab-icon-wrap {
    height: 22px;
  }
}

@media (max-width: 500px) {
  .co-web-mode-badge {
    display: none;
  }
}

@media (max-width: 480px) {
  .co-assign-proxy-input-row {
    flex-direction: column;
    align-items: stretch;
  }

  .co-assign-proxy-input-row > :first-child {
    flex: none;
    width: 100%;
  }
}

@container co-settings (max-width: 440px) {
  .co-assign-proxy-input-row {
    flex-direction: column;
    align-items: stretch;
  }

  .co-assign-proxy-input-row > :first-child {
    flex: none;
    width: 100%;
  }
}

@media (max-width: 420px) {
  .co-navbar-inner {
    padding: 0 10px;
    gap: 6px;
  }

  .co-brand {
    font-size: 15px;
    gap: 5px;
    flex-shrink: 1;
    min-width: 0;
  }

  .co-brand-icon {
    width: 24px;
    height: 24px;
  }

  .co-nav-left:has(.co-brand-version.is-open) .co-brand-text {
    display: none;
  }

  .co-nav-right {
    gap: 3px;
  }

  .co-nav-circle-btn,
  .co-logs-trigger {
    width: 30px;
    height: 30px;
  }

  .co-nav-right :deep(.co-locale-trigger) {
    height: 30px;
    padding: 0 5px 0 6px;
    gap: 2px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .co-nav-circle-btn.is-spinning svg,
  .co-brand-version.is-update-ready .co-nav-update-dot,
  .co-brand-version.is-update-downloading .co-nav-update-dot,
  .co-task-mini-progress-fill.is-indeterminate {
    animation: none;
  }

  .co-task-mini-progress-fill.is-indeterminate {
    width: 100%;
    opacity: 0.45;
    transform: none;
  }
}
</style>
