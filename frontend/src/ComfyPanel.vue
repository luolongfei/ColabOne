<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Badge as ABadge,
  Button as AButton,
  Card as ACard,
  Checkbox as ACheckbox,
  Divider as ADivider,
  Drawer as ADrawer,
  Empty as AEmpty,
  Input as AInput,
  Modal as AModal,
  Popconfirm as APopconfirm,
  Popover as APopover,
  Progress as AProgress,
  Space as ASpace,
  Switch as ASwitch,
  TabPane as ATabPane,
  Tabs as ATabs,
  Tag as ATag,
  Textarea as ATextarea,
} from "ant-design-vue";
import ColabOneDriveExportModal, {
  formatColabOneDrivePath,
  getColabOneDriveConnectStage,
  isColabOneDriveConnectOperation,
  type ColabOneDriveExportResult,
} from "./ColabOneDriveExportModal.vue";
import ColabOneFashionPhotoGroup, {
  type ColabOneFashionPhotoItem,
} from "./ColabOneFashionPhotoGroup.vue";
import ColabOneOutputGalleryModal, {
  type ColabOneGalleryModalItem,
} from "./ColabOneOutputGalleryModal.vue";
import ColabOneOutputShareModal from "./ColabOneOutputShareModal.vue";
import ColabOnePrivacyInput from "./ColabOnePrivacyInput.vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import ColabOneSecretInput from "./ColabOneSecretInput.vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import ComfyWorkflowField from "./ComfyWorkflowField.vue";
import LiveStudioPanel from "./LiveStudioPanel.vue";
import {
  buildColabOnePrivacyContext,
  extractColabOneHostFromInput,
  isColabOnePublicHost,
} from "./colabOnePrivacy";
import { colabOneExtractTunnelToken } from "./colabOneSecretToken";
import {
  PhAppWindow,
  PhArrowSquareOut,
  PhArrowsClockwise,
  PhBroadcast,
  PhBroom,
  PhBrowsers,
  PhCalendarBlank,
  PhCaretDown,
  PhCaretRight,
  PhCheck,
  PhCheckCircle,
  PhClock,
  PhCloud,
  PhCloudArrowDown,
  PhCloudArrowUp,
  PhCloudCheck,
  PhCoins,
  PhDownloadSimple,
  PhFileArchive,
  PhFileCode,
  PhGraph,
  PhHardDrives,
  PhImage,
  PhImages,
  PhInfo,
  PhLink,
  PhMagnifyingGlassPlus,
  PhPlay,
  PhPlus,
  PhPower,
  PhSelectionAll,
  PhShareNetwork,
  PhShieldCheck,
  PhSlidersHorizontal,
  PhSparkle,
  PhStop,
  PhTShirt,
  PhTrash,
  PhTreeStructure,
  PhUploadSimple,
  PhVideoCamera,
  PhWarningCircle,
  PhWaveform,
  PhWifiSlash,
} from "@phosphor-icons/vue";
import {
  colabOneCommand,
  colabOneExportWorkflowOutput,
  colabOneExportWorkflowOutputsZip,
  colabOneOpenComfyCanvas,
  colabOneOpenOutputShare,
  colabOneOpenURL,
  colabOneOutputPreviewURL,
  colabOneRuntimeCapabilities,
  colabOneWriteClipboard,
  createColabOneDefaultOutputShareListState,
  createColabOneDefaultWebConsoleState,
  isColabOneDesktopRuntime,
  type ColabOneOutputShareCreateRequest,
  type ColabOneOutputShareListState,
  type ColabOneOutputShareSummary,
  type ColabOneWebConsoleState,
} from "./api";
import { formatColabOneCULimitValue, formatIdlePolicyLabel } from "./billing";
import {
  computeColabOneGroupCU,
  groupColabOneOutputs,
  isColabOneInspectOutput,
  type ColabOneOutputGroup,
} from "./colabOneOutputGallery";
import { colabOneWorkflowShareId } from "./colabOneWorkflowShare";
import {
  formatColabOneDuration,
  resolveColabOneTaskElapsedSeconds,
} from "./colabOneDuration";
import { colabOneLocaleCode, t, tb } from "./i18n";
import {
  clearColabOneWorkflowDraft,
  colabOneBase64ToBlob,
  createColabOneDraftStatusController,
  deleteColabOneWorkflowDraftMedia,
  hasColabOneDraftContent,
  loadColabOneWorkflowDraft,
  mergeColabOneWorkflowDraftFields,
  readColabOneWorkflowDraftMedia,
  sanitizeColabOneDraftFieldsForSave,
  saveColabOneWorkflowDraft,
  saveColabOneWorkflowDraftMedia,
  type ColabOneWorkflowDraft,
  type ColabOneWorkflowDraftMediaMeta,
  type ColabOneWorkflowDraftStatus,
} from "./comfyDrafts";
import {
  COLABONE_DIGITAL_HUMAN_GROUP_KEY,
  COLABONE_FASHION_MANAGED_FIELD_KEYS,
  COLABONE_FASHION_MAX_REFS_PER_GROUP,
  buildColabOneStudioWorkflowEntries,
  buildColabOneWorkflowOverrides,
  defaultFieldValues,
  extractPublicHost,
  formatOutputCU,
  formatOutputCUExact,
  formatOutputCreatedAt,
  formatOutputCreatedAtISO,
  formatOutputSize,
  getColabOneFashionRolesFieldKey,
  getColabOneFashionSlotKeys,
  guessColabOneFashionPhotoRole,
  hasNonCommercialNotice,
  isColabOneBuiltinDigitalHumanWorkflow,
  isColabOneFashionCustomPosesEmpty,
  isColabOneVoicePresetSupportedByEngine,
  isColabOneVoiceRefRequired,
  isColabOneVoiceRefTextRequired,
  isWorkflowFieldVisible,
  missingRequiredWorkflowFields,
  outputBlobURL,
  parseColabOneFashionPhotoMeta,
  parseWorkflowImportJSON,
  partitionWorkflowFields,
  pickColabOneNextFashionSlotKeys,
  readColabOnePreferredDigitalHumanEngine,
  reconcileColabOneFashionPhotoList,
  remapColabOnePresetPromptFieldsForLocale,
  resolveColabOneAudioSource,
  resolveColabOneDigitalHumanGpuContext,
  resolveColabOneFallbackVoicePreset,
  resolveColabOneFashionPreflight,
  resolveColabOneInitialDigitalHumanWorkflowId,
  resolveColabOneLongCatNativeSamplingMode,
  resolveColabOneSelectedVoicePreset,
  resolveColabOneSiblingVoicePresetsForPrefetch,
  resolveColabOneVoicePresetPrefetchGroupIds,
  resolveColabOneVoicePreviewDefaultText,
  resolveSelectedTtsEngine,
  serializeColabOneFashionPhotoMeta,
  transferColabOneDigitalHumanFields,
  verifyAndDecodeColabOneBase64AudioUrl,
  workflowModelsTotalBytes,
  writeColabOnePreferredDigitalHumanEngine,
  type ColabOneFashionPhotoGroupKind,
  type ColabOneStudioWorkflowEntry,
} from "./comfyui";
import {
  computeColabOneModelDrawerWidth,
  type ColabOneToastKind,
} from "./colabOneToast";
import type {
  ColabOneComfyTtsPreviewRequest,
  ColabOneSnapshot,
  ColabOneTtsPreviewResult,
  ColabOneVoicePreviewMeta,
  ComfyTtsEngineSpec,
  WorkflowField,
  WorkflowOutput,
  WorkflowTemplate,
} from "./types";

const props = defineProps<{
  state: ColabOneSnapshot;
  loading?: boolean;
  clockNow?: number;
  snapshotAt?: number;
  shareMode?: boolean;
  shareBusyOther?: boolean;
  shareDriveExportReady?: boolean;
  canShareWorkflows?: boolean;
  activeShareCount?: number;
  webConsoleState?: ColabOneWebConsoleState;
  outputSharesState?: ColabOneOutputShareListState;
  canEnableWebConsole?: boolean;
  enablingWebConsole?: boolean;
  outputShareCreating?: boolean;
  outputShareRevokingId?: string;
  outputShareDeletingId?: string;
  outputShareLastCreated?: ColabOneOutputShareSummary | null;
  copyText?: (text: string, successToast?: string) => Promise<boolean>;
}>();
const emit = defineEmits<{
  device: [];
  settings: [];
  upgrade: [];
  sync: [];
  openShareModal: [];
  refreshOutputShares: [];
  createOutputShare: [req: ColabOneOutputShareCreateRequest];
  revokeOutputShare: [id: string];
  deleteOutputShare: [id: string];
  clearOutputShareCreated: [];
  enableWebConsole: [];
  notify: [string, ColabOneToastKind?];
  clearError: [];
  error: [string];
  "drawer-state": [{ open: boolean; width: number; rootSelector: string }];
}>();

const isShareMode = computed(() => Boolean(props.shareMode));

const activeTab = ref<"studio" | "live" | "canvas">("studio");
const deleteConfirmWorkflowId = ref("");
const tabsWrapRef = ref<HTMLElement | null>(null);
const busy = ref(false);
const INFO_NOTICE_KEYS = new Set<Parameters<typeof t>[0]>([
  "comfy.noticeStartingEngine",
  "comfy.noticeStoppingEngine",
  "comfy.noticeStartingCanvas",
  "comfy.noticeCancelRequested",
  "comfy.noticeRunSubmitted",
  "comfy.noticeFreeVram",
  "comfy.noticeStagedUpload",
]);
function setNoticeKey(
  key: Parameters<typeof t>[0],
  params?: Record<string, string | number>,
) {
  if (key !== "comfy.noticeCopiedAuthLink") {
    emit("clearError");
  }
  emit("notify", t(key, params), INFO_NOTICE_KEYS.has(key) ? "info" : "success");
}
function reportError(err: unknown) {
  emit("error", String(err));
}

async function openExternalURL(url: string) {
  if (isShareMode.value) return;
  try {
    await colabOneOpenURL(url);
  } catch (e) {
    reportError(e);
  }
}

async function openOutputSharePreview(url: string, shareId = "") {
  if (isShareMode.value) return;
  try {
    await colabOneOpenOutputShare(
      shareId,
      url,
      resolvedWebConsoleState.value,
    );
  } catch (e) {
    reportError(e);
  }
}

// Engine & runtime status
const gpuReady = computed(() => props.state.runtime.state === "ready");
const activeDriveFolderName = computed(
  () =>
    props.state.runtime.drive?.remote_folder ||
    (props.state.runtime.drive?.mode === "colab_drivefs"
      ? "ColabOne_Drive_Mount"
      : "ColabOne_Drive"),
);
const gpuStarting = computed(() => props.state.runtime.state === "starting");
const comfy = computed(() => props.state.runtime.comfyui);
const canvas = computed(() => comfy.value?.canvas);
const comfyTag = computed(() => comfy.value?.tag || "v0.46.0");

const activeTask = computed(() =>
  props.state.runtime.state === "stopped"
    ? undefined
    : props.state.runtime.tasks?.find((t) => t.state === "running"),
);
const comfyTask = computed(() =>
  activeTask.value &&
  (activeTask.value.kind === "comfyui_start" ||
    activeTask.value.kind === "comfyui_run" ||
    activeTask.value.kind === "comfyui_tts_preview" ||
    activeTask.value.kind === "comfyui")
    ? activeTask.value
    : undefined,
);
const comfyStartTask = computed(() =>
  activeTask.value && activeTask.value.kind === "comfyui_start"
    ? activeTask.value
    : undefined,
);
const comfyRunTask = computed(() =>
  activeTask.value && activeTask.value.kind === "comfyui_run"
    ? activeTask.value
    : undefined,
);
const comfyTtsPreviewTask = computed(() =>
  activeTask.value && activeTask.value.kind === "comfyui_tts_preview"
    ? activeTask.value
    : undefined,
);

const comfyRunning = computed(
  () =>
    gpuReady.value &&
    !!(comfy.value?.running || comfy.value?.state === "ready"),
);
const comfyStarting = computed(
  () =>
    gpuReady.value &&
    (comfy.value?.state === "starting" || !!comfyStartTask.value),
);
const queueRunning = computed(() => comfy.value?.queue_running || 0);
const queuePending = computed(() => comfy.value?.queue_pending || 0);
const queueBusy = computed(
  () =>
    comfyRunning.value &&
    (queueRunning.value > 0 || queuePending.value > 0 || !!comfyRunTask.value),
);
const hasActiveRunTask = computed(() => gpuReady.value && !!comfyRunTask.value);

const engineStatus = computed(() => {
  if (!gpuReady.value) {
    return {
      label: t("comfy.tagStopped"),
      color: "default" as const,
      badge: "default" as const,
    };
  }
  if (comfyStarting.value) {
    return {
      label: t("comfy.tagPreparing"),
      color: "warning" as const,
      badge: "warning" as const,
    };
  }
  if (queueBusy.value || hasActiveRunTask.value) {
    return {
      label: t("comfy.tagQueueRunning"),
      color: "processing" as const,
      badge: "processing" as const,
    };
  }
  if (comfyRunning.value) {
    return {
      label: t("comfy.tagRunning"),
      color: "success" as const,
      badge: "success" as const,
    };
  }
  if (comfy.value?.state === "error") {
    return {
      label: t("comfy.tagFailed"),
      color: "error" as const,
      badge: "error" as const,
    };
  }
  return {
    label: t("comfy.tagStopped"),
    color: "default" as const,
    badge: "default" as const,
  };
});

const loadedModel = computed(() =>
  !gpuReady.value
    ? undefined
    : props.state.models.find(
        (m) => m.loaded || m.id === props.state.runtime.loaded_model,
      ),
);
const hasLoadedLLM = computed(
  () =>
    gpuReady.value &&
    (!!loadedModel.value ||
      !!props.state.runtime.loaded_model ||
      props.state.runtime.active_engine === "llama"),
);
const llmUnloadNotice = computed(() =>
  loadedModel.value
    ? t("comfy.llmMutexWithName", { name: loadedModel.value.name })
    : t("comfy.llmMutexGeneric"),
);
const voicePreviewSubmitting = ref(false);

const comfyTaskPercent = computed(() =>
  Math.max(0, Math.min(100, Math.round((comfyTask.value?.progress || 0) * 100))),
);

const fallbackClockNow = ref(Date.now());
const fallbackSnapshotAt = ref(Date.now());
let fallbackClockTimer: ReturnType<typeof setInterval> | null = null;

watch(
  () => [
    props.state.runtime.idle?.server_time,
    comfyTask.value?.id,
    comfyTask.value?.elapsed_seconds,
  ],
  () => {
    if (props.snapshotAt === undefined) {
      fallbackSnapshotAt.value = Date.now();
      fallbackClockNow.value = Date.now();
    }
  },
);

const effectiveClockNow = computed(
  () => props.clockNow ?? fallbackClockNow.value,
);
const effectiveSnapshotAt = computed(
  () => props.snapshotAt ?? fallbackSnapshotAt.value,
);

const comfyTaskElapsedSeconds = computed(() =>
  resolveColabOneTaskElapsedSeconds(
    comfyTask.value,
    props.state.runtime,
    effectiveClockNow.value,
    effectiveSnapshotAt.value,
  ),
);

const comfyTaskElapsedText = computed(() => {
  const secs = comfyTaskElapsedSeconds.value;
  return secs === null
    ? ""
    : formatColabOneDuration(secs, {
        precision: "integer",
        locale: colabOneLocaleCode.value,
      });
});

const comfyTaskLiveCuText = computed(() => {
  const secs = comfyTaskElapsedSeconds.value;
  if (secs === null || secs <= 0) return "";
  const rate = props.state.account?.rate;
  if (typeof rate !== "number" || !Number.isFinite(rate) || rate <= 0) {
    return "";
  }
  const assignments =
    typeof props.state.account?.assignments === "number" &&
    Number.isFinite(props.state.account.assignments) &&
    props.state.account.assignments > 1
      ? props.state.account.assignments
      : 1;
  const cu = ((rate / assignments) * secs) / 3600;
  const formatted = formatOutputCU(cu);
  if (!formatted) return "";
  return cu < 0.01 ? formatted : t("comfy.outputCuLive", { cu: formatted });
});

function formatOutputCUBadge(item: WorkflowOutput): string {
  const formatted = formatOutputCU(item.cu_consumed);
  if (!formatted) return "";
  if (
    typeof item.cu_consumed === "number" &&
    item.cu_consumed > 0 &&
    item.cu_consumed < 0.01
  ) {
    return formatted;
  }
  return item.cu_estimated
    ? t("comfy.outputCuEstimated", { cu: formatted })
    : formatted;
}

function formatOutputElapsed(elapsed: number): string {
  return formatColabOneDuration(elapsed, {
    precision: "auto",
    locale: colabOneLocaleCode.value,
  });
}

function formatOutputCreatedAtTooltip(
  epochSeconds: number | null | undefined,
): string {
  const compact = formatOutputCreatedAt(epochSeconds);
  if (!compact) return "";
  try {
    const raw = Number(epochSeconds);
    const ms = raw > 1e12 ? raw : raw * 1000;
    const localized = new Intl.DateTimeFormat(colabOneLocaleCode.value, {
      dateStyle: "medium",
      timeStyle: "medium",
    }).format(new Date(ms));
    return t("comfy.outputCreatedAtTooltip", { time: localized });
  } catch {
    return t("comfy.outputCreatedAtTooltip", { time: compact });
  }
}

function formatOutputCUTooltip(item: WorkflowOutput): string {
  const cuText = formatOutputCUExact(item.cu_consumed);
  if (!cuText) return "";
  const parts: string[] = [];
  if (item.gpu_name && item.gpu_name.trim()) {
    parts.push(item.gpu_name.trim());
  }
  if (
    typeof item.cu_rate === "number" &&
    Number.isFinite(item.cu_rate) &&
    item.cu_rate > 0
  ) {
    parts.push(t("billing.rateValue", { rate: item.cu_rate.toFixed(2) }));
  }
  if (Number.isFinite(item.elapsed) && item.elapsed > 0) {
    parts.push(
      t("task.elapsedTotal", { duration: formatOutputElapsed(item.elapsed) }),
    );
  }
  if (parts.length > 0) {
    const detail = parts.join(" · ");
    return item.cu_estimated
      ? t("comfy.outputCuTooltipEstimatedDetail", { cu: cuText, detail })
      : t("comfy.outputCuTooltipDetail", { cu: cuText, detail });
  }
  return t("comfy.outputCuTooltipSimple", { cu: cuText });
}

function formatComfyByteSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return formatOutputSize(0);
  }
  if (bytes >= 1024 ** 3) {
    return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
  }
  return formatOutputSize(bytes);
}

function formatComfyDownloadSpeed(speedBps: number): string {
  if (!Number.isFinite(speedBps) || speedBps <= 0) {
    return "0 MB/s";
  }
  if (speedBps >= 1024 ** 3) {
    return `${(speedBps / 1024 ** 3).toFixed(2)} GB/s`;
  }
  if (speedBps >= 1024 ** 2) {
    return `${(speedBps / 1024 ** 2).toFixed(1)} MB/s`;
  }
  if (speedBps >= 1024) {
    return `${(speedBps / 1024).toFixed(0)} KB/s`;
  }
  return `${Math.round(speedBps)} B/s`;
}

function formatComfyEta(seconds: number | null | undefined): string {
  if (
    seconds === null ||
    seconds === undefined ||
    !Number.isFinite(seconds) ||
    seconds < 0
  ) {
    return "";
  }
  const whole = Math.ceil(seconds);
  const mins = Math.floor(whole / 60);
  const secs = whole % 60;
  if (mins >= 60) {
    const hrs = Math.floor(mins / 60);
    const remMins = mins % 60;
    return `${hrs}:${String(remMins).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
  }
  return `${mins}:${String(secs).padStart(2, "0")}`;
}

const comfyTaskDownloadSummary = computed(() => {
  const dl = comfyTask.value?.download;
  if (
    !dl ||
    (!dl.total_bytes && !dl.downloaded_bytes && !dl.speed_bps && !dl.filename)
  ) {
    return null;
  }
  const primary = t("comfy.downloadProgressMeta", {
    done: formatComfyByteSize(dl.downloaded_bytes || 0),
    total: formatComfyByteSize(dl.total_bytes || 0),
    speed:
      dl.phase === "verifying"
        ? t("device.downloadVerifying")
        : formatComfyDownloadSpeed(dl.speed_bps || 0),
  });
  const etaStr = dl.phase !== "verifying" ? formatComfyEta(dl.eta_seconds) : "";
  const etaText = etaStr ? t("device.downloadEta", { eta: etaStr }) : "";
  const fileCounter =
    dl.file_count && dl.file_count > 1 && dl.file_index
      ? `${dl.file_index}/${dl.file_count}`
      : "";
  return {
    primary,
    etaText,
    filename: dl.filename || "",
    fileCounter,
  };
});

// Workflow Studio & Drawer state
const workflows = computed(() => props.state.workflows || []);
const ttsEngines = computed<ComfyTtsEngineSpec[]>(
  () => props.state.tts_engines || [],
);
const selectedWorkflowId = ref("");
const pendingWorkflowId = ref("");
const selectedWorkflow = computed(() =>
  workflows.value.find((w) => w.id === selectedWorkflowId.value),
);
const workflowNeedsComfyServer = computed(
  () => selectedWorkflow.value?.pipeline?.video_backend !== "longcat_avatar_native",
);
const studioEntries = computed(() =>
  buildColabOneStudioWorkflowEntries(workflows.value),
);
const builtinDigitalHumanGroup = computed(
  () => studioEntries.value.find((entry) => entry.isGroup) ?? null,
);
const activeDigitalHumanGroup = computed(() =>
  builtinDigitalHumanGroup.value &&
  selectedWorkflow.value &&
  builtinDigitalHumanGroup.value.workflows.some(
    (w) => w.id === selectedWorkflow.value!.id,
  )
    ? builtinDigitalHumanGroup.value
    : null,
);
const preferredDigitalHumanEngineId = ref<string>(
  readColabOnePreferredDigitalHumanEngine(),
);
watch(colabOneWorkflowShareId, () => {
  preferredDigitalHumanEngineId.value =
    readColabOnePreferredDigitalHumanEngine();
});
const digitalHumanGpuContext = computed(() =>
  resolveColabOneDigitalHumanGpuContext(
    props.state.runtime,
    props.state.settings?.gpu,
  ),
);
const digitalHumanSelection = computed(() =>
  resolveColabOneInitialDigitalHumanWorkflowId(
    builtinDigitalHumanGroup.value?.workflows,
    props.state.runtime,
    props.state.settings?.gpu,
    preferredDigitalHumanEngineId.value,
  ),
);
const recommendedDigitalHumanWorkflowId = computed(
  () => digitalHumanSelection.value.recommendedWorkflowId,
);
const fieldValues = ref<Record<string, string | number | boolean>>({});

const paramDrawerOpen = ref(false);
const outputsDrawerOpen = ref(false);
const mobileAutoReleasePopoverOpen = ref(false);
const advancedExpanded = ref(false);
const viewportWidth = ref(
  typeof window !== "undefined" ? window.innerWidth : 1280,
);
const paramDrawerWidth = computed(() =>
  Math.min(
    viewportWidth.value,
    Math.min(620, Math.max(420, viewportWidth.value - 32)),
  ),
);
const outputsDrawerWidth = computed(() =>
  computeColabOneModelDrawerWidth(viewportWidth.value),
);

function onWindowResize() {
  viewportWidth.value = window.innerWidth;
}

const activeDrawerState = computed(() => {
  if (paramDrawerOpen.value && selectedWorkflow.value) {
    return {
      open: true,
      width: paramDrawerWidth.value,
      rootSelector: ".co-comfy-param-drawer-root",
    };
  }
  if (outputsDrawerOpen.value) {
    return {
      open: true,
      width: outputsDrawerWidth.value,
      rootSelector: ".co-comfy-outputs-drawer-root",
    };
  }
  return { open: false, width: 0, rootSelector: "" };
});

watch(
  activeDrawerState,
  (next) => {
    emit("drawer-state", next);
  },
  { immediate: true },
);

interface PendingUpload {
  filename: string;
  data: string;
  kind: "image" | "audio";
}
const pendingUploads = ref<Record<string, PendingUpload>>({});
const uploadedInputIds = ref<Record<string, string>>({});
const uploadedNames = ref<Record<string, string>>({});
const localPreviews = ref<Record<string, string>>({});
const uploadingFields = ref<Record<string, boolean>>({});
const uploadingNames = ref<Record<string, string>>({});
const reservedFashionNames = ref<Record<string, string>>({});
const reservedFashionTokens: Record<
  ColabOneFashionPhotoGroupKind,
  Map<string, symbol>
> = {
  model: new Map<string, symbol>(),
  garment: new Map<string, symbol>(),
};
const fashionGroupUploadChains: Record<
  ColabOneFashionPhotoGroupKind,
  Promise<void>
> = {
  model: Promise.resolve(),
  garment: Promise.resolve(),
};
let fashionBatchErrorSeq = 0;
const hasUploadingField = computed(
  () =>
    Object.values(uploadingFields.value).some(Boolean) ||
    Object.keys(reservedFashionNames.value).length > 0,
);
const uploadGen: Record<string, number> = {};
const submittingRun = ref(false);
let disposed = false;

// Real-time workflow draft persistence state
const DRAFT_SAVE_DEBOUNCE_MS = 350;
const draftMediaMeta = ref<Record<string, ColabOneWorkflowDraftMediaMeta>>({});
const draftMediaSourceIds = ref<Record<string, string>>({});
const draftStatus = ref<ColabOneWorkflowDraftStatus>("idle");
const draftStatusController = createColabOneDraftStatusController({
  onStatusChange: (status) => {
    draftStatus.value = status;
  },
  isVisible: () => paramDrawerOpen.value && Boolean(selectedWorkflow.value),
});
function setDraftStatus(next: ColabOneWorkflowDraftStatus) {
  draftStatusController.setStatus(next);
}
const draftStatusLabel = computed(() => {
  if (draftStatus.value === "saving") return t("comfy.draftSavingBadge");
  if (draftStatus.value === "saved") return t("comfy.draftSavedBadge");
  if (draftStatus.value === "restored") return t("comfy.draftRestoredBadge");
  return "";
});
let draftSaveTimer: ReturnType<typeof setTimeout> | null = null;
let draftDirty = false;
let draftWorkflowId = "";
let activeDraftWorkflow: WorkflowTemplate | undefined;
let draftLoadGen = 0;
let draftRestoreGen = 0;
let draftSaveGen = 0;
let draftRestoring = false;
const draftFieldMutationEpochs: Record<string, number> = {};
const draftMediaMutationEpochs: Record<string, number> = {};
let digitalHumanPersistChain: Promise<unknown> = Promise.resolve();

function bumpDraftFieldEpoch(wfId: string): number {
  const next = (draftFieldMutationEpochs[wfId] ?? 0) + 1;
  draftFieldMutationEpochs[wfId] = next;
  return next;
}

function getDraftFieldEpoch(wfId: string): number {
  return draftFieldMutationEpochs[wfId] ?? 0;
}

function draftMediaEpochKey(wfId: string, fieldKey: string): string {
  return `${wfId}::${fieldKey}`;
}

function bumpDraftMediaEpoch(wfId: string, fieldKey: string): number {
  const key = draftMediaEpochKey(wfId, fieldKey);
  const next = (draftMediaMutationEpochs[key] ?? 0) + 1;
  draftMediaMutationEpochs[key] = next;
  return next;
}

function getDraftMediaEpoch(wfId: string, fieldKey: string): number {
  return draftMediaMutationEpochs[draftMediaEpochKey(wfId, fieldKey)] ?? 0;
}

function enqueueDigitalHumanPersist<T>(task: () => Promise<T>): Promise<T> {
  const run = digitalHumanPersistChain.then(task, task);
  digitalHumanPersistChain = run.then(
    () => undefined,
    () => undefined,
  );
  return run;
}

function resolveDraftMediaWorkflowId(
  fieldKey: string,
  fallbackWfId: string,
): string {
  return draftMediaSourceIds.value[fieldKey] || fallbackWfId;
}

const isDigitalHumanWorkflow = computed(
  () => selectedWorkflow.value?.pipeline?.kind === "digital_human",
);
const isFashionTryOnWorkflow = computed(
  () => selectedWorkflow.value?.pipeline?.kind === "fashion_tryon",
);
const isTtsAudioSource = computed(
  () =>
    isDigitalHumanWorkflow.value &&
    resolveColabOneAudioSource(fieldValues.value) === "tts",
);
const selectedTtsEngine = computed<ComfyTtsEngineSpec | undefined>(() =>
  resolveSelectedTtsEngine(
    selectedWorkflow.value,
    fieldValues.value,
    ttsEngines.value,
  ),
);
const visibleFields = computed(() => {
  const engine = isTtsAudioSource.value ? selectedTtsEngine.value : undefined;
  const isLongCatNative =
    selectedWorkflow.value?.pipeline?.video_backend === "longcat_avatar_native";
  const longCatNativeSamplingMode = isLongCatNative
    ? resolveColabOneLongCatNativeSamplingMode(fieldValues.value)
    : undefined;
  return (selectedWorkflow.value?.fields || []).filter((f) => {
    if (!isWorkflowFieldVisible(f, fieldValues.value)) {
      return false;
    }
    if (longCatNativeSamplingMode === "non_distill_cfg") {
      if (f.key === "pipeline.mix_steps" || f.key === "pipeline.mix_alpha") {
        return false;
      }
    } else if (longCatNativeSamplingMode === "distill_lora_mix") {
      if (
        f.key === "pipeline.audio_guidance_scale" ||
        f.key === "pipeline.text_guidance_scale" ||
        f.key === "pipeline.num_inference_steps"
      ) {
        return false;
      }
    }
    if (engine) {
      if (
        f.key === "pipeline.voice_prompt" &&
        engine.supports_voice_design === false
      ) {
        return false;
      }
      if (
        f.key === "pipeline.emotion_prompt" &&
        (engine.id === "moss_tts_v1_5" || engine.id === "voxcpm2")
      ) {
        return false;
      }
    }
    return true;
  });
});
const voiceRefTextRequired = computed(() =>
  isColabOneVoiceRefTextRequired(
    selectedWorkflow.value,
    fieldValues.value,
    uploadedInputIds.value,
    { ...draftMediaMeta.value, ...pendingUploads.value },
  ),
);
const fieldPartitions = computed(() => {
  const base = partitionWorkflowFields(visibleFields.value);
  if (!voiceRefTextRequired.value) {
    return base;
  }
  const advIdx = base.advanced.findIndex(
    (field) => field.key === "pipeline.voice_ref_text",
  );
  if (advIdx < 0) {
    return base;
  }
  const refTextField = base.advanced[advIdx];
  const nextAdvanced = [
    ...base.advanced.slice(0, advIdx),
    ...base.advanced.slice(advIdx + 1),
  ];
  const nextPrimary = [...base.primary];
  const refAudioIdx = nextPrimary.findIndex(
    (field) => field.key === "pipeline.voice_ref_audio",
  );
  if (refAudioIdx >= 0) {
    nextPrimary.splice(refAudioIdx + 1, 0, refTextField);
  } else {
    nextPrimary.push(refTextField);
  }
  return { primary: nextPrimary, advanced: nextAdvanced };
});
const standardPrimaryFields = computed(() => {
  const list = fieldPartitions.value.primary;
  if (!isFashionTryOnWorkflow.value) {
    return list;
  }
  return list.filter((field) => !COLABONE_FASHION_MANAGED_FIELD_KEYS.has(field.key));
});

function clearAllReservedFashionSlots() {
  reservedFashionTokens.model.clear();
  reservedFashionTokens.garment.clear();
  if (Object.keys(reservedFashionNames.value).length > 0) {
    reservedFashionNames.value = {};
  }
}

function releaseReservedFashionSlot(
  group: ColabOneFashionPhotoGroupKind,
  slotKey: string,
  token: symbol,
) {
  if (reservedFashionTokens[group].get(slotKey) !== token) {
    return;
  }
  reservedFashionTokens[group].delete(slotKey);
  if (slotKey in reservedFashionNames.value) {
    const next = { ...reservedFashionNames.value };
    delete next[slotKey];
    reservedFashionNames.value = next;
  }
}

function isFashionSlotUploading(key: string): boolean {
  return (
    Boolean(uploadingFields.value[key]) ||
    Boolean(reservedFashionNames.value[key])
  );
}

function settledFashionSlotKeys(group: ColabOneFashionPhotoGroupKind): string[] {
  const slots = getColabOneFashionSlotKeys(group);
  return slots.filter(
    (key) =>
      !isFashionSlotUploading(key) &&
      (Boolean(uploadedInputIds.value[key]) ||
        Boolean(pendingUploads.value[key]) ||
        Boolean(draftMediaMeta.value[key]) ||
        Boolean(localPreviews.value[key])),
  );
}

function activeFashionSlotKeys(group: ColabOneFashionPhotoGroupKind): string[] {
  const slots = getColabOneFashionSlotKeys(group);
  return slots.filter(
    (key) =>
      isFashionSlotUploading(key) ||
      Boolean(uploadedInputIds.value[key]) ||
      Boolean(pendingUploads.value[key]) ||
      Boolean(draftMediaMeta.value[key]) ||
      Boolean(localPreviews.value[key]),
  );
}

function buildFashionPhotoItems(
  group: ColabOneFashionPhotoGroupKind,
): ColabOneFashionPhotoItem[] {
  if (!isFashionTryOnWorkflow.value) {
    return [];
  }
  const activeKeys = activeFashionSlotKeys(group);
  const rolesFieldKey = getColabOneFashionRolesFieldKey(group);
  const parsedMeta = parseColabOneFashionPhotoMeta(
    fieldValues.value[rolesFieldKey],
    group,
  );
  const ordered = reconcileColabOneFashionPhotoList(
    group,
    activeKeys,
    fieldValues.value[rolesFieldKey],
  );
  return ordered.map(({ key, role }, idx) => {
    const filename =
      uploadedNames.value[key] ||
      uploadingNames.value[key] ||
      reservedFashionNames.value[key] ||
      pendingUploads.value[key]?.filename ||
      draftMediaMeta.value[key]?.filename ||
      String(fieldValues.value[key] || "") ||
      key;
    const uploading = isFashionSlotUploading(key);
    const effectiveRole =
      uploading && !parsedMeta.roles[key]
        ? guessColabOneFashionPhotoRole(group, filename, idx === 0).role
        : role;
    return {
      key,
      role: effectiveRole,
      filename,
      previewUrl: localPreviews.value[key],
      uploading,
      uploadedRemote: Boolean(uploadedInputIds.value[key]),
      hasPending: Boolean(pendingUploads.value[key]),
      hasDraft: Boolean(draftMediaMeta.value[key]),
    };
  });
}

const fashionModelPhotoItems = computed(() => buildFashionPhotoItems("model"));
const fashionGarmentPhotoItems = computed(() =>
  buildFashionPhotoItems("garment"),
);

const fashionModelDropzoneId = computed(() => {
  const field = selectedWorkflow.value?.fields.find((f) => f.key === "1.image");
  return field ? fieldInputId(field) : "co-fashion-model-dropzone";
});

const fashionGarmentDropzoneId = computed(() => {
  const field = selectedWorkflow.value?.fields.find((f) => f.key === "2.image");
  return field ? fieldInputId(field) : "co-fashion-garment-dropzone";
});

function persistFashionPhotoList(
  group: ColabOneFashionPhotoGroupKind,
  entries: Array<{ key: string; role: string }>,
) {
  const rolesFieldKey = getColabOneFashionRolesFieldKey(group);
  const roles: Record<string, string> = {};
  const order: string[] = [];
  for (const item of entries) {
    order.push(item.key);
    roles[item.key] = item.role;
  }
  fieldValues.value[rolesFieldKey] = serializeColabOneFashionPhotoMeta({
    order,
    roles,
  });
  draftDirty = true;
  scheduleDraftSave(true);
}

function pruneFashionPhotoRoles(
  wf: WorkflowTemplate | undefined,
  opts?: { persist?: boolean },
) {
  if (!wf || wf.pipeline?.kind !== "fashion_tryon") {
    return;
  }
  let changed = false;
  for (const group of ["model", "garment"] as const) {
    const rolesFieldKey = getColabOneFashionRolesFieldKey(group);
    const currentRaw = fieldValues.value[rolesFieldKey];
    const reconciled = reconcileColabOneFashionPhotoList(
      group,
      settledFashionSlotKeys(group),
      currentRaw,
    );
    const roles: Record<string, string> = {};
    for (const item of reconciled) {
      roles[item.key] = item.role;
    }
    const nextRaw = serializeColabOneFashionPhotoMeta({
      order: reconciled.map((item) => item.key),
      roles,
    });
    if (String(currentRaw ?? "") !== nextRaw) {
      fieldValues.value[rolesFieldKey] = nextRaw;
      changed = true;
    }
  }
  if (changed && opts?.persist) {
    draftDirty = true;
    scheduleDraftSave(true);
  }
}

async function handleFashionAddFiles(
  group: ColabOneFashionPhotoGroupKind,
  files: File[],
) {
  const wf = selectedWorkflow.value;
  if (!wf || !isFashionTryOnWorkflow.value || submittingRun.value || !files.length) {
    return;
  }
  // Validate format & size upfront before slicing to available capacity so invalid
  // files never displace valid files or trigger misleading max-limit notices.
  const validFiles: File[] = [];
  const validationErrors: string[] = [];
  const seenValidationErrors = new Set<string>();
  const addValidationError = (msg: string) => {
    if (msg && !seenValidationErrors.has(msg)) {
      seenValidationErrors.add(msg);
      validationErrors.push(msg);
    }
  };
  for (const file of files) {
    if (!IMAGE_EXT_RE.test(file.name)) {
      addValidationError(t("comfy.errUnsupportedImageFormat"));
      continue;
    }
    if (file.size <= 0) {
      addValidationError(t("comfy.errEmptyFile"));
      continue;
    }
    if (file.size > MAX_IMAGE_INPUT_BYTES) {
      addValidationError(t("comfy.errImageTooLarge"));
      continue;
    }
    validFiles.push(file);
  }
  if (validationErrors.length > 0) {
    fashionBatchErrorSeq += 1;
    reportError(validationErrors.join(" · "));
  }
  if (validFiles.length === 0) {
    return;
  }

  const loadGen = draftLoadGen;
  const batchStartErrorSeq = fashionBatchErrorSeq;
  const occupied = Array.from(
    new Set<string>([
      ...activeFashionSlotKeys(group),
      ...reservedFashionTokens[group].keys(),
    ]),
  );
  const freeSlots = pickColabOneNextFashionSlotKeys(
    group,
    occupied,
    validFiles.length,
  );
  if (freeSlots.length === 0) {
    emit(
      "notify",
      t("comfy.fashionMaxFullToast", {
        max: COLABONE_FASHION_MAX_REFS_PER_GROUP,
      }),
      "info",
    );
    return;
  }
  const acceptedFiles = validFiles.slice(0, freeSlots.length);
  const overflowTruncated = validFiles.length > acceptedFiles.length;
  const fieldByKey = new Map(wf.fields.map((f) => [f.key, f]));
  const rolesFieldKey = getColabOneFashionRolesFieldKey(group);

  // Reserve all picked slots synchronously with per-slot ownership tokens so rapid
  // drops cannot collide and aborted batches can never clobber newer reservations.
  const reservations: Array<{ slotKey: string; file: File; token: symbol }> = [];
  const nextReservedNames = { ...reservedFashionNames.value };
  for (let i = 0; i < acceptedFiles.length; i += 1) {
    const slotKey = freeSlots[i];
    const file = acceptedFiles[i];
    const token = Symbol(slotKey);
    reservedFashionTokens[group].set(slotKey, token);
    nextReservedNames[slotKey] = file.name;
    reservations.push({ slotKey, file, token });
  }
  reservedFashionNames.value = nextReservedNames;

  // Serialize upload-and-commit work per group via a FIFO promise chain so an
  // overlapping smaller drop can never finish ahead of Drop 1 and steal #1 Main.
  const runBatch = async () => {
    const batchErrors = [...validationErrors];
    const seenBatchErrors = new Set<string>(validationErrors);
    const addBatchError = (msg: string | undefined) => {
      const cleaned = typeof msg === "string" ? msg.trim() : "";
      if (cleaned && !seenBatchErrors.has(cleaned)) {
        seenBatchErrors.add(cleaned);
        batchErrors.push(cleaned);
      }
    };
    let uploadedCount = 0;
    let stagedCount = 0;
    let firstUploadedName = "";
    let firstStagedName = "";

    try {
      for (const { slotKey, file, token } of reservations) {
        if (
          disposed ||
          loadGen !== draftLoadGen ||
          selectedWorkflowId.value !== wf.id
        ) {
          return;
        }
        const slotField = fieldByKey.get(slotKey);
        if (!slotField) {
          releaseReservedFashionSlot(group, slotKey, token);
          continue;
        }
        try {
          const res = await handleMediaFile(slotField, file, {
            silentNotice: true,
          });
          if (
            disposed ||
            loadGen !== draftLoadGen ||
            selectedWorkflowId.value !== wf.id
          ) {
            return;
          }
          if (res.error) {
            addBatchError(res.error);
          }
          if (res.uploaded) {
            uploadedCount += 1;
            if (!firstUploadedName) firstUploadedName = file.name;
          } else if (res.staged) {
            stagedCount += 1;
            if (!firstStagedName) firstStagedName = file.name;
          }
          releaseReservedFashionSlot(group, slotKey, token);
          const liveSettled = settledFashionSlotKeys(group);
          if (!liveSettled.includes(slotKey)) {
            continue;
          }
          // Re-read the live reconciled list AFTER await so mid-upload promote/role/remove
          // operations on other photos in the group are never reverted.
          const otherSettled = liveSettled.filter((k) => k !== slotKey);
          const liveList = reconcileColabOneFashionPhotoList(
            group,
            otherSettled,
            fieldValues.value[rolesFieldKey],
          );
          const isFirst = liveList.length === 0;
          const { role } = guessColabOneFashionPhotoRole(
            group,
            file.name,
            isFirst,
          );
          liveList.push({ key: slotKey, role });
          persistFashionPhotoList(group, liveList);
        } finally {
          releaseReservedFashionSlot(group, slotKey, token);
        }
      }

      if (
        disposed ||
        loadGen !== draftLoadGen ||
        selectedWorkflowId.value !== wf.id
      ) {
        return;
      }

      const settledCount = uploadedCount + stagedCount;
      if (
        batchErrors.length === 0 &&
        settledCount > 0 &&
        fashionBatchErrorSeq === batchStartErrorSeq
      ) {
        emit("clearError");
      }

      if (uploadedCount > 0) {
        const displayName =
          settledCount > 1
            ? `${firstUploadedName} (+${settledCount - 1})`
            : firstUploadedName;
        emit(
          "notify",
          t("comfy.noticeUploadedRemote", { name: displayName }),
          "success",
        );
      } else if (stagedCount > 0) {
        const displayName =
          stagedCount > 1
            ? `${firstStagedName} (+${stagedCount - 1})`
            : firstStagedName;
        emit(
          "notify",
          t("comfy.noticeStagedUpload", { name: displayName }),
          "info",
        );
      }

      if (overflowTruncated && settledCount > 0) {
        emit(
          "notify",
          t("comfy.fashionMaxReachedToast", {
            max: COLABONE_FASHION_MAX_REFS_PER_GROUP,
            added: settledCount,
          }),
          "info",
        );
      }

      if (batchErrors.length > 0) {
        fashionBatchErrorSeq += 1;
        reportError(batchErrors.join(" · "));
      }
    } finally {
      for (const { slotKey, token } of reservations) {
        releaseReservedFashionSlot(group, slotKey, token);
      }
    }
  };

  const chained = fashionGroupUploadChains[group].then(runBatch, runBatch);
  fashionGroupUploadChains[group] = chained.then(
    () => undefined,
    () => undefined,
  );
  return chained;
}

function handleFashionRemovePhoto(
  group: ColabOneFashionPhotoGroupKind,
  slotKey: string,
) {
  const wf = selectedWorkflow.value;
  if (!wf || submittingRun.value || isFashionSlotUploading(slotKey)) return;
  const slotField = wf.fields.find((f) => f.key === slotKey);
  if (slotField) {
    clearMediaField(slotField);
  }
  const rolesFieldKey = getColabOneFashionRolesFieldKey(group);
  const currentList = reconcileColabOneFashionPhotoList(
    group,
    settledFashionSlotKeys(group).filter((k) => k !== slotKey),
    fieldValues.value[rolesFieldKey],
  );
  persistFashionPhotoList(group, currentList);
}

function handleFashionPromotePhoto(
  group: ColabOneFashionPhotoGroupKind,
  slotKey: string,
) {
  if (submittingRun.value || isFashionSlotUploading(slotKey)) return;
  const rolesFieldKey = getColabOneFashionRolesFieldKey(group);
  const currentList = reconcileColabOneFashionPhotoList(
    group,
    settledFashionSlotKeys(group),
    fieldValues.value[rolesFieldKey],
  );
  const idx = currentList.findIndex((item) => item.key === slotKey);
  if (idx <= 0) return;
  const [moved] = currentList.splice(idx, 1);
  currentList.unshift(moved);
  persistFashionPhotoList(group, currentList);
}

function handleFashionChangeRole(
  group: ColabOneFashionPhotoGroupKind,
  slotKey: string,
  role: string,
) {
  if (submittingRun.value || isFashionSlotUploading(slotKey)) return;
  const rolesFieldKey = getColabOneFashionRolesFieldKey(group);
  const currentList = reconcileColabOneFashionPhotoList(
    group,
    settledFashionSlotKeys(group),
    fieldValues.value[rolesFieldKey],
  );
  const target = currentList.find((item) => item.key === slotKey);
  if (!target || target.role === role) return;
  target.role = role;
  persistFashionPhotoList(group, currentList);
}
const voiceRefRequired = computed(() =>
  isColabOneVoiceRefRequired(selectedWorkflow.value, fieldValues.value),
);
const showTtsEngineStrip = computed(
  () => isTtsAudioSource.value && !!selectedTtsEngine.value,
);
const nonCommercialNotice = computed(() =>
  hasNonCommercialNotice(selectedWorkflow.value, selectedTtsEngine.value),
);
const nonCommercialAlertDescription = computed(() => {
  const notice = nonCommercialNotice.value;
  if (!notice) return "";
  const items = notice.items || [];
  if (notice.restrictedBy === "workflow" && items.length >= 1) {
    const item = items[0];
    return t("comfy.workflowNonCommercialAlert", {
      name: tb(item.name),
      notice: item.notice
        ? tb(item.notice)
        : item.license
          ? tb(item.license)
          : t("comfy.licenseNonCommercial"),
    });
  }
  if (notice.restrictedBy === "tts" && items.length >= 1) {
    const item = items[0];
    return t("comfy.ttsNonCommercialAlert", {
      name: tb(item.name),
      notice: item.notice
        ? tb(item.notice)
        : item.license
          ? tb(item.license)
          : t("comfy.licenseNonCommercial"),
    });
  }
  if (notice.restrictedBy === "both" && items.length >= 2) {
    const names = items.map((item) => tb(item.name)).join(" + ");
    const colon = colabOneLocaleCode.value.startsWith("zh-") ? "：" : ": ";
    const attributed = Array.from(
      new Set(
        items.map((item) => {
          const body = item.notice
            ? tb(item.notice)
            : item.license
              ? tb(item.license)
              : t("comfy.licenseNonCommercial");
          return `${tb(item.name)}${colon}${body}`;
        }),
      ),
    );
    const notices = `\n${attributed.join("\n")}`;
    return t("comfy.combinedNonCommercialAlert", { names, notices });
  }
  return t("comfy.ttsNonCommercialAlert", {
    name: tb(notice.sourceName),
    notice: notice.notice
      ? tb(notice.notice)
      : notice.license
        ? tb(notice.license)
        : t("comfy.licenseNonCommercial"),
  });
});

interface WorkflowPreflightItem {
  tone: "warning" | "ready" | "info";
  text: string;
}

const PREFLIGHT_DISK_HEADROOM_BYTES = 4 * 1024 ** 3;

function formatGiBFloor(gb: number): string {
  if (!Number.isFinite(gb) || gb <= 0) return "0";
  const floored = Math.floor(gb * 10) / 10;
  return floored.toFixed(1).replace(/\.0$/, "");
}

const workflowPreflightItems = computed<WorkflowPreflightItem[]>(() => {
  const wf = selectedWorkflow.value;
  if (!wf) return [];
  const primaryGpuMib = props.state.runtime.gpu?.total_mib;
  const fallbackGpuMib = props.state.runtime.resources?.gpu?.total_mib;
  const gpuTotalMib = gpuReady.value
    ? typeof primaryGpuMib === "number" &&
      Number.isFinite(primaryGpuMib) &&
      primaryGpuMib > 0
      ? primaryGpuMib
      : typeof fallbackGpuMib === "number" &&
          Number.isFinite(fallbackGpuMib) &&
          fallbackGpuMib > 0
        ? fallbackGpuMib
        : undefined
    : undefined;
  const estimatedGpuMib = gpuTotalMib ?? digitalHumanGpuContext.value.vramMib;
  const fashionPreflight = isFashionTryOnWorkflow.value
    ? resolveColabOneFashionPreflight(fieldValues.value, estimatedGpuMib)
    : null;
  const ttsEngine = isTtsAudioSource.value
    ? selectedTtsEngine.value
    : undefined;
  const effectiveWfMinVramMib = fashionPreflight
    ? fashionPreflight.minVramMib
    : wf.min_vram_mib || 0;
  const effectiveWfMinDiskMib = fashionPreflight
    ? fashionPreflight.minDiskMib
    : wf.min_disk_mib || 0;
  const minVramMib = Math.max(
    effectiveWfMinVramMib,
    ttsEngine?.min_vram_mib || 0,
  );
  const minDiskMib = Math.max(
    effectiveWfMinDiskMib,
    ttsEngine?.min_disk_mib || 0,
  );
  const modelBytes = fashionPreflight
    ? fashionPreflight.modelBytes
    : workflowModelsTotalBytes(wf);
  if (minVramMib <= 0 && minDiskMib <= 0 && modelBytes <= 0 && !(wf.models?.length || 0)) {
    return [];
  }

  // For fashion_tryon, active_pack === wf.id does not guarantee that the currently
  // selected precision_mode (BF16 vs INT8) or garment_cleanup (RMBG 3.0) variant weights
  // are cached locally, so always run dynamic fashion preflight disk/download checks.
  const isPackActive =
    gpuReady.value &&
    comfy.value?.active_pack === wf.id &&
    !isFashionTryOnWorkflow.value;
  const items: WorkflowPreflightItem[] = [];
  if (gpuReady.value) {
    if (
      minVramMib > 0 &&
      gpuTotalMib !== undefined &&
      gpuTotalMib < minVramMib
    ) {
      const requiredGb = Math.max(1, Math.ceil(minVramMib / 1024));
      const recommendedGpuText =
        ttsEngine &&
        (ttsEngine.min_vram_mib || 0) > effectiveWfMinVramMib
          ? `${requiredGb} GB+ GPU`
          : wf.recommended_gpu
            ? tb(wf.recommended_gpu)
            : `${requiredGb} GB+ GPU`;
      items.push({
        tone: "warning",
        text: t(
          isShareMode.value
            ? "comfy.preflightVramInsufficientShare"
            : "comfy.preflightVramInsufficient",
          {
            required: requiredGb,
            available: formatGiBFloor(gpuTotalMib / 1024),
            gpu: recommendedGpuText,
          },
        ),
      });
    }

    const resDisk = props.state.runtime.resources?.disk;
    const hasResDisk =
      typeof resDisk?.total_bytes === "number" &&
      Number.isFinite(resDisk.total_bytes) &&
      resDisk.total_bytes > 0 &&
      typeof resDisk?.free_bytes === "number" &&
      Number.isFinite(resDisk.free_bytes) &&
      resDisk.free_bytes >= 0;
    const rawDiskFree = props.state.runtime.disk_free;
    const hasRawDiskFree =
      typeof rawDiskFree === "number" &&
      Number.isFinite(rawDiskFree) &&
      rawDiskFree >= 0;
    const diskFreeBytes =
      hasRawDiskFree && rawDiskFree > 0
        ? rawDiskFree
        : hasResDisk
          ? resDisk!.free_bytes
          : hasRawDiskFree
            ? rawDiskFree
            : undefined;
    if (diskFreeBytes !== undefined) {
      const drive = props.state.runtime.drive;
      const isDriveWeightsMounted = Boolean(
        drive?.connected &&
          (drive.mode === "colab_drivefs" || drive.mode === "mount"),
      );
      const cachedTtsEngines = (
        props.state.runtime.comfyui as
          | { cached_tts_engines?: string[] }
          | undefined
      )?.cached_tts_engines;
      const isTtsEngineCached = Boolean(
        ttsEngine &&
          Array.isArray(cachedTtsEngines) &&
          cachedTtsEngines.includes(ttsEngine.id),
      );
      const wfDiskBytes = isPackActive
        ? PREFLIGHT_DISK_HEADROOM_BYTES
        : effectiveWfMinDiskMib > 0
          ? isDriveWeightsMounted
            ? PREFLIGHT_DISK_HEADROOM_BYTES
            : effectiveWfMinDiskMib * 1024 ** 2
          : 0;
      const uncachedTtsDiskBytes =
        ttsEngine && !isTtsEngineCached && (ttsEngine.min_disk_mib || 0) > 0
          ? (ttsEngine.min_disk_mib || 0) * 1024 ** 2
          : 0;
      const requiredDiskBytes = wfDiskBytes + uncachedTtsDiskBytes;
      if (requiredDiskBytes > 0 && diskFreeBytes < requiredDiskBytes) {
        const requiredGb = Math.max(1, Math.ceil(requiredDiskBytes / 1024 ** 3));
        items.push({
          tone: "warning",
          text: t("comfy.preflightDiskInsufficient", {
            required: requiredGb,
            available: formatGiBFloor(diskFreeBytes / 1024 ** 3),
          }),
        });
      }
    }
  }

  if (items.length === 0) {
    if (isPackActive) {
      items.push({
        tone: "ready",
        text: t("comfy.preflightPackReady"),
      });
    } else if (modelBytes > 0) {
      items.push({
        tone: "info",
        text: t("comfy.preflightPackWillDownload", {
          size: formatComfyByteSize(modelBytes),
        }),
      });
    } else if (effectiveWfMinDiskMib > 0) {
      items.push({
        tone: "info",
        text: t("comfy.preflightNativeDiskFootprint", {
          size: formatComfyByteSize(effectiveWfMinDiskMib * 1024 ** 2),
        }),
      });
    }
  }
  return items;
});

const hasWorkflowSpecs = computed(() => {
  const wf = selectedWorkflow.value;
  if (!wf) return false;
  return Boolean(
    wf.min_vram_mib > 0 ||
      (wf.min_disk_mib || 0) > 0 ||
      wf.recommended_gpu ||
      wf.models?.length ||
      wf.custom_nodes?.length ||
      wf.license,
  );
});

function formatTtsSampleRateKhz(sampleRate: number): string {
  const khz = sampleRate / 1000;
  return Number.isInteger(khz)
    ? String(khz)
    : khz.toFixed(1).replace(/\.0$/, "");
}

const advancedPresetSummary = computed(() => {
  if (isFashionTryOnWorkflow.value) {
    const parts: string[] = [];
    const cleanup = String(
      fieldValues.value["pipeline.garment_cleanup"] ?? "rmbg3",
    )
      .trim()
      .toLowerCase();
    if (cleanup === "rmbg3") {
      parts.push(t("comfy.fashionSummaryRmbg3"));
    } else if (cleanup === "crop_only") {
      parts.push(t("comfy.fashionSummaryCropOnly"));
    } else if (cleanup === "off") {
      parts.push(t("comfy.fashionSummaryCleanupOff"));
    }
    const precision = String(
      fieldValues.value["pipeline.precision_mode"] ?? "auto",
    )
      .trim()
      .toLowerCase();
    if (precision === "bf16") {
      parts.push(t("comfy.fashionSummaryPrecisionBf16"));
    } else if (precision === "int8") {
      parts.push(t("comfy.fashionSummaryPrecisionInt8"));
    } else {
      parts.push(t("comfy.fashionSummaryPrecisionAuto"));
    }
    if (fieldValues.value["pipeline.anchor_hero_shot"] !== false) {
      parts.push(t("comfy.fashionSummaryHeroAnchor"));
    }
    return parts.length > 0 ? parts.join(" · ") : t("comfy.advancedDefaultHint");
  }
  if (!isDigitalHumanWorkflow.value) {
    return t("comfy.advancedDefaultHint");
  }
  const parts: string[] = [];
  if (showTtsEngineStrip.value && selectedTtsEngine.value) {
    const rawName = selectedTtsEngine.value.name
      ? tb(selectedTtsEngine.value.name)
      : selectedTtsEngine.value.id;
    const shortName = rawName.split(/[（(]/)[0].trim() || rawName;
    if (selectedTtsEngine.value.sample_rate > 0) {
      parts.push(
        `${shortName} · ${t("comfy.ttsSampleRate", {
          khz: formatTtsSampleRateKhz(selectedTtsEngine.value.sample_rate),
        })}`,
      );
    } else {
      parts.push(shortName);
    }
  }
  if (
    selectedWorkflow.value?.pipeline?.video_backend === "longcat_avatar_native"
  ) {
    const qualityPreset = String(
      fieldValues.value["pipeline.quality_preset"] ?? "cinema_flagship",
    )
      .trim()
      .toLowerCase();
    if (qualityPreset === "cinema_flagship") {
      parts.push(t("comfy.qualityPresetCinema"));
    } else if (qualityPreset === "balanced_hd") {
      parts.push(t("comfy.qualityPresetBalanced"));
    } else if (qualityPreset === "longform_fast") {
      parts.push(t("comfy.qualityPresetLongform"));
    }
    const targetRes = String(
      fieldValues.value["pipeline.target_resolution"] ?? "1920x1080",
    )
      .trim()
      .toLowerCase();
    const resLabel =
      targetRes === "2560x1440" || targetRes === "1440x2560"
        ? "2K"
        : targetRes === "1920x1080" || targetRes === "1080x1920"
          ? "1080p"
          : targetRes === "1280x720" || targetRes === "720x1280"
            ? "720p"
            : "";
    const upscale = String(
      fieldValues.value["pipeline.upscale_mode"] ??
        selectedWorkflow.value?.pipeline?.upscale_mode ??
        "auto",
    )
      .trim()
      .toLowerCase();
    let srLabel = "";
    if (upscale === "seedvr2_7b_sharp") {
      srLabel = "SeedVR2 7B Sharp";
    } else if (upscale === "seedvr2_7b") {
      srLabel = "SeedVR2 7B";
    } else if (upscale === "flashvsr_plus") {
      srLabel = "FlashVSR+";
    } else if (upscale === "none") {
      srLabel = "720p";
    } else if (upscale === "auto" || !upscale) {
      if (resLabel === "720p") {
        srLabel = "720p";
      } else if (qualityPreset === "longform_fast") {
        srLabel = "FlashVSR+";
      } else if (qualityPreset === "balanced_hd") {
        srLabel = "SeedVR2 7B";
      } else {
        srLabel = "SeedVR2 7B Sharp";
      }
    }
    if (srLabel) {
      parts.push(srLabel);
    }
    if (resLabel && resLabel !== srLabel && upscale !== "none") {
      parts.push(resLabel);
    }
    const defaultSamplingMode = resolveColabOneLongCatNativeSamplingMode({
      "pipeline.quality_preset": qualityPreset,
      "pipeline.sampling_mode": "auto",
    });
    const effectiveSamplingMode = resolveColabOneLongCatNativeSamplingMode(
      fieldValues.value,
    );
    if (effectiveSamplingMode !== defaultSamplingMode) {
      if (effectiveSamplingMode === "non_distill_cfg") {
        const rawSteps = Number(
          fieldValues.value["pipeline.num_inference_steps"] ?? 50,
        );
        const steps =
          Number.isFinite(rawSteps) && rawSteps > 0 ? Math.round(rawSteps) : 50;
        parts.push(t("comfy.samplingModeShortCfg", { steps }));
      } else {
        parts.push(t("comfy.samplingModeShortLora"));
      }
    }
    return parts.length > 0 ? parts.join(" · ") : t("comfy.advancedDefaultHint");
  }
  const renderQuality = String(
    fieldValues.value["pipeline.render_quality"] ?? "",
  ).trim();
  if (renderQuality === "balanced_480p_ai2x") {
    parts.push("480p");
  } else if (renderQuality === "native_720p") {
    parts.push("720p");
  }
  const targetRes = String(
    fieldValues.value["pipeline.target_resolution"] ?? "",
  )
    .trim()
    .toLowerCase();
  const resLabel =
    targetRes === "2560x1440" || targetRes === "1440x2560"
      ? "2K"
      : targetRes === "1920x1080" || targetRes === "1080x1920"
        ? "1080p"
        : targetRes === "1280x720" || targetRes === "720x1280"
          ? "720p"
          : "";
  const upscale = String(
    fieldValues.value["pipeline.upscale_mode"] ??
      selectedWorkflow.value?.pipeline?.upscale_mode ??
      "",
  )
    .trim()
    .toLowerCase();
  if (upscale === "ai_2x") {
    parts.push("AI 2x");
    if (resLabel) {
      parts.push(resLabel);
    }
  } else if (upscale === "lanczos") {
    parts.push("Lanczos");
    if (resLabel) {
      parts.push(resLabel);
    }
  } else if (upscale === "none") {
    if (!renderQuality) {
      parts.push("480p");
    }
  } else if (resLabel) {
    parts.push(resLabel);
  }
  return parts.length > 0 ? parts.join(" · ") : t("comfy.advancedDefaultHint");
});

const runningWorkflowName = computed(() => {
  const runningId = comfyRunTask.value?.model_id;
  const matched = runningId
    ? workflows.value.find((w) => w.id === runningId)
    : undefined;
  const rawName = matched?.name || selectedWorkflow.value?.name || "";
  return rawName ? tb(rawName) : t("comfy.defaultWorkflowName");
});

const NON_COMMERCIAL_LICENSE_RE =
  /非商用|非商业|非商業|non-?commercial|비상업/i;

function isWorkflowNonCommercial(wf: WorkflowTemplate): boolean {
  return Boolean(hasNonCommercialNotice(wf, undefined));
}

function workflowNonCommercialTooltip(wf: WorkflowTemplate): string {
  if (wf.license_notice) {
    return tb(wf.license_notice);
  }
  const notice = hasNonCommercialNotice(wf, undefined);
  if (notice?.notice) {
    return tb(notice.notice);
  }
  return t("comfy.licenseNonCommercial");
}

function dedupeLicenseTokens(rawLicenses: string[]): string {
  const seen = new Set<string>();
  const tokens: string[] = [];
  for (const raw of rawLicenses) {
    for (const part of raw.split(/\s*\/\s*/)) {
      const trimmed = part.trim();
      if (!trimmed) continue;
      const key = trimmed.toLowerCase();
      if (seen.has(key)) continue;
      seen.add(key);
      tokens.push(trimmed);
    }
  }
  return tokens.join(" / ");
}

function formatWorkflowLicenseTag(wf: WorkflowTemplate): string {
  if (!wf.license) return "";
  const translated = dedupeLicenseTokens([tb(wf.license)]);
  if (!translated) return "";
  if (
    isWorkflowNonCommercial(wf) &&
    !NON_COMMERCIAL_LICENSE_RE.test(translated)
  ) {
    return `${translated} · ${t("comfy.licenseNonCommercial")}`;
  }
  return translated;
}

function formatWorkflowCardSpecs(wf: WorkflowTemplate): string {
  const parts: string[] = [];
  if (
    wf.source === "builtin" ||
    wf.min_vram_mib > 0 ||
    (wf.min_disk_mib || 0) > 0 ||
    wf.models?.length
  ) {
    if (wf.min_vram_mib > 0) {
      parts.push(`${Math.max(1, Math.round(wf.min_vram_mib / 1024))} GB+`);
    }
    const totalBytes = workflowModelsTotalBytes(wf);
    if (totalBytes > 0) {
      parts.push(formatComfyByteSize(totalBytes));
    } else if ((wf.min_disk_mib || 0) > 0) {
      parts.push(
        `~${Math.max(1, Math.round((wf.min_disk_mib || 0) / 1024))} GB`,
      );
    }
    if (wf.license) {
      const licenseText = dedupeLicenseTokens([tb(wf.license)]);
      if (licenseText) {
        parts.push(licenseText);
      }
    }
  }
  if (parts.length === 0) {
    parts.push(t("comfy.nodeCount", { count: wf.node_count }));
    parts.push(t("comfy.fieldCount", { count: wf.fields.length }));
  }
  return parts.join(" · ");
}

function formatStudioEntrySpecs(entry: ColabOneStudioWorkflowEntry): string {
  if (!entry.isGroup) {
    return formatWorkflowCardSpecs(entry.primary);
  }
  const parts: string[] = [];
  const vramGbs = entry.workflows
    .map((w) =>
      w.min_vram_mib > 0 ? Math.max(1, Math.round(w.min_vram_mib / 1024)) : 0,
    )
    .filter((gb) => gb > 0);
  if (vramGbs.length > 0) {
    const minGb = Math.min(...vramGbs);
    const maxGb = Math.max(...vramGbs);
    parts.push(minGb === maxGb ? `${minGb} GB+` : `${minGb}–${maxGb} GB+`);
  }
  parts.push(
    t("comfy.digitalHumanEngineCountSpec", { count: entry.workflows.length }),
  );
  if (entry.workflows.every((w) => !isWorkflowNonCommercial(w))) {
    parts.push(t("comfy.licenseCommercialOk"));
  } else {
    const licenseSummary = dedupeLicenseTokens(
      entry.workflows
        .map((w) => (w.license ? tb(w.license) : ""))
        .filter(Boolean),
    );
    if (licenseSummary) {
      parts.push(licenseSummary);
    }
  }
  return parts.join(" · ");
}

function isDigitalHumanEngineVramShort(engineWf: WorkflowTemplate): boolean {
  const gpuVramMib = digitalHumanGpuContext.value.vramMib;
  return Boolean(
    engineWf.min_vram_mib > 0 &&
      gpuVramMib > 0 &&
      gpuVramMib < engineWf.min_vram_mib,
  );
}

function isDigitalHumanEngineRecommendedBadgeShown(
  engineWf: WorkflowTemplate,
): boolean {
  if (engineWf.id !== recommendedDigitalHumanWorkflowId.value) {
    return false;
  }
  const ctx = digitalHumanGpuContext.value;
  const liveVramMib =
    ctx.isLive && Number.isFinite(ctx.vramMib) && ctx.vramMib > 0
      ? ctx.vramMib
      : null;
  return liveVramMib == null || liveVramMib >= (engineWf.min_vram_mib ?? 0);
}

function formatDigitalHumanEngineTitle(engineWf: WorkflowTemplate): string {
  const backend = engineWf.pipeline?.video_backend;
  if (
    backend === "infinitetalk" ||
    engineWf.id === "digital-human-infinitetalk"
  ) {
    return t("comfy.videoEngineInfiniteTalkTitle");
  }
  if (
    backend === "longcat_avatar" ||
    backend === "longcat_avatar_native" ||
    engineWf.id === "digital-human-longcat"
  ) {
    return t("comfy.videoEngineLongCatTitle");
  }
  return tb(engineWf.name);
}

function formatDigitalHumanEngineSummary(engineWf: WorkflowTemplate): string {
  const backend = engineWf.pipeline?.video_backend;
  if (
    backend === "infinitetalk" ||
    engineWf.id === "digital-human-infinitetalk"
  ) {
    return t("comfy.videoEngineInfiniteTalkSummary");
  }
  if (
    backend === "longcat_avatar" ||
    backend === "longcat_avatar_native" ||
    engineWf.id === "digital-human-longcat"
  ) {
    return t("comfy.videoEngineLongCatSummary");
  }
  return tb(engineWf.description || engineWf.name);
}

function formatDigitalHumanEngineSpecParts(
  engineWf: WorkflowTemplate,
): { text: string; isVram: boolean }[] {
  const parts: { text: string; isVram: boolean }[] = [];
  const backend = engineWf.pipeline?.video_backend;
  if (
    backend === "infinitetalk" ||
    engineWf.id === "digital-human-infinitetalk"
  ) {
    parts.push({ text: "InfiniteTalk", isVram: false });
  } else if (backend === "longcat_avatar_native") {
    parts.push({ text: "LongCat 1.6", isVram: false });
  } else if (
    backend === "longcat_avatar" ||
    engineWf.id === "digital-human-longcat"
  ) {
    parts.push({ text: "LongCat-Avatar", isVram: false });
  } else if (backend && backend !== "custom") {
    parts.push({ text: backend, isVram: false });
  }
  if (engineWf.min_vram_mib > 0) {
    parts.push({
      text: `${Math.max(1, Math.round(engineWf.min_vram_mib / 1024))} GB+`,
      isVram: true,
    });
  }
  const totalBytes = workflowModelsTotalBytes(engineWf);
  if (totalBytes > 0) {
    parts.push({ text: formatComfyByteSize(totalBytes), isVram: false });
  } else if ((engineWf.min_disk_mib || 0) > 0) {
    parts.push({
      text: `~${Math.max(1, Math.round((engineWf.min_disk_mib || 0) / 1024))} GB`,
      isVram: false,
    });
  }
  return parts;
}

function formatFieldNodeMeta(field: WorkflowField): string {
  if (field.node_id === "pipeline") {
    return "";
  }
  if (selectedWorkflow.value?.source === "builtin") {
    return "";
  }
  return `${t("comfy.nodePrefix", { id: field.node_id })} ${field.input_name}`;
}

function shouldRenderTtsStripAfter(field: WorkflowField): boolean {
  return showTtsEngineStrip.value && field.key === "pipeline.tts_engine";
}

function shouldRenderVoicePreviewAfter(field: WorkflowField): boolean {
  return (
    isTtsAudioSource.value &&
    !!selectedTtsEngine.value &&
    field.key === "pipeline.voice_preset"
  );
}

function voiceRefTextRequiredMessage(engineId: string): string {
  if (engineId === "fish_drama3") {
    return tb(
      "Fish Audio Drama 3 克隆参考音色时必须同时填写「参考音色对应文字」",
    );
  }
  if (engineId === "ming_3_tts") {
    return tb(
      "Ming-3.0-TTS 克隆参考音色时必须同时填写「参考音色对应文字」",
    );
  }
  return tb("Breeze TTS 2 克隆参考音色时必须同时填写「参考音色对应文字」");
}

function cloneOnlyVoiceRefMessage(
  engine: ComfyTtsEngineSpec | undefined,
): string {
  const engineId =
    engine?.id ||
    String(fieldValues.value["pipeline.tts_engine"] ?? "").trim() ||
    selectedWorkflow.value?.pipeline?.default_tts_engine ||
    "";
  if (engineId === "indextts2") {
    return t("comfy.indextts2RefRequiredHint");
  }
  const displayName =
    (engine?.name || "").split(/[（(]/)[0].trim() || engine?.name || engineId;
  return tb(
    `${displayName} 在自定义音色模式下需要上传音色参考音频才能进行语音合成`,
  );
}

function isFieldForceRequired(field: WorkflowField): boolean {
  if (field.key === "pipeline.voice_ref_audio") {
    return voiceRefRequired.value;
  }
  if (field.key === "pipeline.voice_ref_text") {
    return voiceRefTextRequired.value;
  }
  return false;
}

function fieldRequiredHint(field: WorkflowField): string {
  if (field.key === "pipeline.voice_ref_audio" && voiceRefRequired.value) {
    return cloneOnlyVoiceRefMessage(selectedTtsEngine.value);
  }
  if (field.key === "pipeline.voice_ref_text" && voiceRefTextRequired.value) {
    const engineId =
      selectedTtsEngine.value?.id ||
      String(fieldValues.value["pipeline.tts_engine"] ?? "").trim() ||
      selectedWorkflow.value?.pipeline?.default_tts_engine ||
      "";
    return voiceRefTextRequiredMessage(engineId);
  }
  return "";
}

function isWideAdvancedField(field: WorkflowField): boolean {
  return (
    field.field_type === "textarea" ||
    field.field_type === "image" ||
    field.field_type === "audio" ||
    field.field_type === "boolean" ||
    field.key === "pipeline.tts_engine" ||
    field.key === "pipeline.voice_preset" ||
    field.key === "pipeline.render_quality" ||
    field.key === "pipeline.acceleration_profile" ||
    field.key === "pipeline.upscale_mode" ||
    field.key === "pipeline.quality_preset" ||
    field.key === "pipeline.framing_style" ||
    field.key === "pipeline.sampling_mode" ||
    field.key === "pipeline.vocal_separation" ||
    field.key === "pipeline.garment_cleanup" ||
    field.key === "pipeline.styling_match" ||
    field.key === "pipeline.color_fidelity" ||
    field.key === "pipeline.precision_mode"
  );
}

function formatDraftErrorDetail(err: unknown): string {
  const raw =
    err instanceof Error
      ? err.message
      : typeof err === "string"
        ? err
        : String(err ?? "");
  const cleaned = raw
    .replace(/^(?:Error:\s*)+/i, "")
    .replace(/^not found:\s*/i, "")
    .trim();
  return cleaned ? tb(cleaned) : tb(raw);
}

// Voice audition preview state & remote synthesis flow
interface VoicePreviewCacheEntry {
  url: string;
  meta: ColabOneVoicePreviewMeta;
}

const VOICE_PREVIEW_CACHE_MAX_ENTRIES = 64;
const voicePreviewCache = new Map<string, VoicePreviewCacheEntry>();
const detachedVoicePreviewTaskIds = new Set<string>();
const detachedVoicePreviewVersion = ref(0);
const readyVoicePresetMap = ref<Record<string, string>>({});
const readyVoicePresetIds = computed(() =>
  Object.keys(readyVoicePresetMap.value),
);
const voicePreviewPrefetchStarting = ref(false);
const voicePreviewPrefetchLocalCancelled = ref(false);
const voicePreviewText = ref("");
const voicePreviewTextCustomEdited = ref(false);
const voicePreviewLookingUp = ref(false);
const voicePreviewFetchingAsset = ref(false);
const VOICE_PREVIEW_TASK_MISSING_GRACE_MS = 15_000;
const activeVoicePreviewTaskId = ref("");
const activeVoicePreviewId = ref("");
const activeVoicePreviewContextKey = ref("");
const activeVoicePreviewPresetId = ref("");
const activeVoicePreviewScopeKey = ref("");
const activeVoicePreviewAllowPresetReady = ref(true);
let activeVoicePreviewTaskObserved = false;
let activeVoicePreviewTaskStartedAt = 0;
const loadedVoicePreviewSignature = ref("");
const activeVoicePreviewEngineName = ref("");
const activeVoicePreviewVoiceLabel = ref("");
const activeVoicePreviewSampleRate = ref<number | undefined>(undefined);
const activeVoicePreviewRuntimeId = ref<string | undefined>(undefined);
const voicePreviewStatus = ref("");
const voicePreviewError = ref("");
const voicePreviewAudioUrl = ref("");
const voicePreviewMeta = ref<ColabOneVoicePreviewMeta | null>(null);
const voicePreviewPlayToken = ref(0);
let voicePreviewReqGen = 0;
let voicePreviewEphemeralSeq = 0;
let voicePreviewLookupGen = 0;
let voicePrefetchGen = 0;
let pendingAutoPlayVoicePresetId = "";
const activePrefetchJobId = ref("");
const activePrefetchJobScopeKey = ref("");
const prefetchPendingPreviewIds = new Set<string>();
const prefetchPullQueue: Array<{
  presetId: string;
  previewId: string;
  scopeKey: string;
  engineName: string;
  voiceLabel: string;
  sampleRate?: number;
}> = [];
let prefetchPullDraining = false;

const selectedVoicePresetId = computed(
  () =>
    String(fieldValues.value["pipeline.voice_preset"] ?? "shen_zhiyi").trim() ||
    "shen_zhiyi",
);
const selectedVoicePreset = computed(() =>
  resolveColabOneSelectedVoicePreset(
    selectedVoicePresetId.value,
    selectedTtsEngine.value,
  ),
);
const voicePreviewDefaultText = computed(() =>
  resolveColabOneVoicePreviewDefaultText(selectedVoicePreset.value),
);

const hasUploadedVoicePreviewRefAudio = computed(() => {
  const wf = selectedWorkflow.value;
  const hasVoiceRef = Boolean(
    uploadedInputIds.value["pipeline.voice_ref_audio"] ||
      pendingUploads.value["pipeline.voice_ref_audio"] ||
      draftMediaMeta.value["pipeline.voice_ref_audio"],
  );
  const emoField = wf?.fields.find(
    (f) => f.key === "pipeline.emotion_ref_audio",
  );
  const emoVisible = Boolean(
    emoField && isWorkflowFieldVisible(emoField, fieldValues.value),
  );
  const hasEmoRef =
    emoVisible &&
    Boolean(
      uploadedInputIds.value["pipeline.emotion_ref_audio"] ||
        pendingUploads.value["pipeline.emotion_ref_audio"] ||
        draftMediaMeta.value["pipeline.emotion_ref_audio"],
    );
  return hasVoiceRef || hasEmoRef;
});

const currentVoicePrefetchScopeKey = computed(() => {
  const wf = selectedWorkflow.value;
  const engine = selectedTtsEngine.value;
  if (!wf || !engine || !isTtsAudioSource.value) return "";
  const voicePromptVisible = engine.supports_voice_design !== false;
  const emotionPromptVisible =
    engine.id !== "moss_tts_v1_5" && engine.id !== "voxcpm2";
  return JSON.stringify([
    wf.id,
    engine.id,
    voicePreviewTextCustomEdited.value ? voicePreviewText.value.trim() : "",
    voicePromptVisible
      ? String(fieldValues.value["pipeline.voice_prompt"] ?? "").trim()
      : "",
    emotionPromptVisible
      ? String(fieldValues.value["pipeline.emotion_prompt"] ?? "").trim()
      : "",
    hasUploadedVoicePreviewRefAudio.value ? "ref" : "",
  ]);
});

const activeVoicePrefetchGroupIds = computed(() => {
  if (hasUploadedVoicePreviewRefAudio.value) return [];
  const wf = selectedWorkflow.value;
  const engine = selectedTtsEngine.value;
  if (!wf || !engine || !isTtsAudioSource.value) return [];
  const voiceField = wf.fields.find((f) => f.key === "pipeline.voice_preset");
  return resolveColabOneVoicePresetPrefetchGroupIds(
    selectedVoicePresetId.value,
    voiceField,
    engine,
  );
});

const voicePreviewPrefetchTotal = computed(
  () => activeVoicePrefetchGroupIds.value.length,
);

const voicePreviewPrefetchDone = computed(() => {
  const ready = readyVoicePresetMap.value;
  let count = 0;
  for (const pid of activeVoicePrefetchGroupIds.value) {
    if (ready[pid]) count += 1;
  }
  return count;
});

const voicePreviewPrefetchActive = computed(() => {
  if (
    voicePreviewPrefetchLocalCancelled.value ||
    hasUploadedVoicePreviewRefAudio.value
  ) {
    return false;
  }
  const engine = selectedTtsEngine.value;
  const scopeKey = currentVoicePrefetchScopeKey.value;
  if (!engine || !isTtsAudioSource.value || !scopeKey) return false;
  if (voicePreviewPrefetchStarting.value) return true;
  const remotePf = props.state.runtime.comfyui?.tts_preview_prefetch;
  if (!remotePf?.active || remotePf.engine_id !== engine.id) return false;
  if (activePrefetchJobScopeKey.value && activePrefetchJobScopeKey.value !== scopeKey) {
    return false;
  }
  if (
    activePrefetchJobId.value &&
    remotePf.job_id &&
    remotePf.job_id !== activePrefetchJobId.value
  ) {
    return false;
  }
  return true;
});

const voicePreviewCanOfflinePlay = computed(() => {
  if (hasUploadedVoicePreviewRefAudio.value) return false;
  const presetId = selectedVoicePresetId.value;
  if (!presetId || presetId === "custom") return false;
  return Boolean(readyVoicePresetMap.value[presetId]);
});
const activeVoicePreviewTask = computed(() => {
  void detachedVoicePreviewVersion.value;
  const tasks = props.state.runtime.tasks || [];
  if (activeVoicePreviewTaskId.value) {
    return tasks.find((task) => task.id === activeVoicePreviewTaskId.value);
  }
  return tasks.find(
    (task) =>
      task.kind === "comfyui_tts_preview" &&
      task.state === "running" &&
      !detachedVoicePreviewTaskIds.has(task.id),
  );
});
const hasBlockingNonPreviewTask = computed(() =>
  Boolean(
    activeTask.value && activeTask.value.id !== activeVoicePreviewTaskId.value,
  ),
);
const voicePreviewBusy = computed(
  () =>
    voicePreviewLookingUp.value ||
    voicePreviewSubmitting.value ||
    voicePreviewFetchingAsset.value ||
    Boolean(activeVoicePreviewTaskId.value) ||
    activeVoicePreviewTask.value?.state === "running",
);
const engineActionBlocked = computed(
  () =>
    busy.value ||
    submittingRun.value ||
    voicePreviewLookingUp.value ||
    voicePreviewSubmitting.value ||
    Boolean(activeVoicePreviewTaskId.value) ||
    voicePreviewFetchingAsset.value ||
    !!props.state.operation ||
    !!activeTask.value,
);
const displayedVoicePreviewStatus = computed(
  () =>
    voicePreviewStatus.value ||
    activeVoicePreviewTask.value?.message ||
    "",
);
const voicePreviewRemoteReady = computed(() => gpuReady.value);

function voicePreviewMediaMarker(fieldKey: string): string {
  const uploadedId = uploadedInputIds.value[fieldKey];
  if (typeof uploadedId === "string" && uploadedId.trim()) {
    return `id:${uploadedId.trim()}`;
  }
  const pending = pendingUploads.value[fieldKey];
  if (pending) {
    return `pending:${pending.filename}:${pending.data.length}:${pending.data.slice(0, 24)}:${pending.data.slice(-24)}`;
  }
  const draft = draftMediaMeta.value[fieldKey];
  if (draft) {
    return `draft:${draft.filename}:${draft.size}`;
  }
  return "";
}

function computeVoicePreviewSignature(params: {
  workflowId: string;
  engineId: string;
  voicePresetId: string;
  previewText: string;
  voicePrompt: string;
  emotionPrompt: string;
  refText: string;
  refAudioMarker: string;
  emotionRefMarker: string;
}): string {
  return JSON.stringify([
    params.workflowId,
    params.engineId,
    params.voicePresetId,
    params.previewText,
    params.voicePrompt,
    params.emotionPrompt,
    params.refText,
    params.refAudioMarker,
    params.emotionRefMarker,
  ]);
}

const currentVoicePreviewSignature = computed(() => {
  const wf = selectedWorkflow.value;
  const engine = selectedTtsEngine.value;
  const effectivePreviewText =
    voicePreviewText.value.trim() || voicePreviewDefaultText.value;
  const emotionRefField = wf?.fields.find(
    (f) => f.key === "pipeline.emotion_ref_audio",
  );
  const emotionRefVisible = Boolean(
    emotionRefField &&
      isWorkflowFieldVisible(emotionRefField, fieldValues.value),
  );
  const voicePromptVisible = Boolean(
    engine && engine.supports_voice_design !== false,
  );
  const emotionPromptVisible = Boolean(
    engine && engine.id !== "moss_tts_v1_5" && engine.id !== "voxcpm2",
  );
  return computeVoicePreviewSignature({
    workflowId: wf?.id || "",
    engineId: engine?.id || "",
    voicePresetId: selectedVoicePresetId.value,
    previewText: effectivePreviewText,
    voicePrompt: voicePromptVisible
      ? String(fieldValues.value["pipeline.voice_prompt"] ?? "").trim()
      : "",
    emotionPrompt: emotionPromptVisible
      ? String(fieldValues.value["pipeline.emotion_prompt"] ?? "").trim()
      : "",
    refText: String(fieldValues.value["pipeline.voice_ref_text"] ?? "").trim(),
    refAudioMarker: voicePreviewMediaMarker("pipeline.voice_ref_audio"),
    emotionRefMarker: emotionRefVisible
      ? voicePreviewMediaMarker("pipeline.emotion_ref_audio")
      : "",
  });
});

function clearActiveVoicePreviewDisplay() {
  voicePreviewError.value = "";
  voicePreviewAudioUrl.value = "";
  voicePreviewMeta.value = null;
  loadedVoicePreviewSignature.value = "";
}

function resetVoicePreviewTaskTracking() {
  ++voicePreviewReqGen;
  voicePreviewLookingUp.value = false;
  voicePreviewSubmitting.value = false;
  voicePreviewFetchingAsset.value = false;
  activeVoicePreviewTaskId.value = "";
  activeVoicePreviewId.value = "";
  activeVoicePreviewContextKey.value = "";
  activeVoicePreviewPresetId.value = "";
  activeVoicePreviewScopeKey.value = "";
  activeVoicePreviewAllowPresetReady.value = true;
  activeVoicePreviewTaskObserved = false;
  activeVoicePreviewTaskStartedAt = 0;
  activeVoicePreviewRuntimeId.value = undefined;
  voicePreviewStatus.value = "";
}

function detachActiveVoicePreviewRequest() {
  if (activeVoicePreviewTaskId.value) {
    detachedVoicePreviewTaskIds.add(activeVoicePreviewTaskId.value);
    detachedVoicePreviewVersion.value += 1;
  }
  resetVoicePreviewTaskTracking();
  clearActiveVoicePreviewDisplay();
}

function evictVoicePreviewCache(protectedUrl?: string) {
  if (voicePreviewCache.size <= VOICE_PREVIEW_CACHE_MAX_ENTRIES) {
    return;
  }
  const urlsToRevoke: string[] = [];
  for (const [key, entry] of Array.from(voicePreviewCache.entries())) {
    if (voicePreviewCache.size <= VOICE_PREVIEW_CACHE_MAX_ENTRIES) {
      break;
    }
    if (
      entry.url === voicePreviewAudioUrl.value ||
      (protectedUrl && entry.url === protectedUrl)
    ) {
      continue;
    }
    voicePreviewCache.delete(key);
    if (entry.url) {
      urlsToRevoke.push(entry.url);
    }
  }
  revokeBlobUrlsDeferred(urlsToRevoke);
}

function revokeAllVoicePreviewUrls(defer = false) {
  const urls: string[] = [];
  for (const entry of voicePreviewCache.values()) {
    if (entry.url) urls.push(entry.url);
  }
  voicePreviewCache.clear();
  prefetchPullQueue.length = 0;
  prefetchPendingPreviewIds.clear();
  voicePreviewAudioUrl.value = "";
  voicePreviewMeta.value = null;
  loadedVoicePreviewSignature.value = "";
  if (defer) {
    revokeBlobUrlsDeferred(urls);
  } else {
    urls.forEach((url) => URL.revokeObjectURL(url));
  }
}

function reconcileVoicePresetForEngine(
  engine: ComfyTtsEngineSpec | undefined,
): boolean {
  const wf = selectedWorkflow.value;
  if (!wf || wf.pipeline?.kind !== "digital_human") return false;
  const voiceField = wf.fields.find((f) => f.key === "pipeline.voice_preset");
  if (!voiceField) return false;
  const currentPreset = String(
    fieldValues.value["pipeline.voice_preset"] ?? "",
  ).trim();
  if (!currentPreset || currentPreset === "custom") return false;
  if (!engine?.voice_presets?.length) return false;
  if (
    isColabOneVoicePresetSupportedByEngine(currentPreset, voiceField, engine)
  ) {
    return false;
  }
  const fallback = resolveColabOneFallbackVoicePreset(voiceField, engine);
  fieldValues.value["pipeline.voice_preset"] = fallback;
  const fallbackPreset = resolveColabOneSelectedVoicePreset(fallback, engine);
  if (!voicePreviewTextCustomEdited.value || !voicePreviewText.value.trim()) {
    voicePreviewText.value =
      resolveColabOneVoicePreviewDefaultText(fallbackPreset);
    voicePreviewTextCustomEdited.value = false;
  }
  detachActiveVoicePreviewRequest();
  return true;
}

function onVoicePreviewTextUpdate(next: string) {
  voicePreviewText.value = next;
  voicePreviewTextCustomEdited.value =
    next.trim().length > 0 &&
    next.trim() !== voicePreviewDefaultText.value.trim();
  clearActiveVoicePreviewDisplay();
}

function onVoicePreviewReset() {
  voicePreviewText.value = voicePreviewDefaultText.value;
  voicePreviewTextCustomEdited.value = false;
  clearActiveVoicePreviewDisplay();
}

async function applyVoicePreviewAudio(
  previewId: string,
  base64Data: string,
  reqGen: number,
  targetRuntimeId: string | undefined,
  contextKey: string,
  info: {
    mime?: string;
    sha256?: string;
    sampleRate?: number;
    durationSeconds?: number;
    cached: boolean;
    localCached?: boolean;
    engineName: string;
    voiceLabel: string;
  },
): Promise<boolean> {
  const existing = previewId ? voicePreviewCache.get(previewId) : undefined;
  let url = existing?.url || "";
  const nextMeta: ColabOneVoicePreviewMeta = {
    sampleRate: info.sampleRate ?? existing?.meta.sampleRate,
    durationSeconds: info.durationSeconds ?? existing?.meta.durationSeconds,
    cached: info.cached,
    localCached: info.localCached ?? existing?.meta.localCached,
    engineName: info.engineName || existing?.meta.engineName,
    voiceLabel: info.voiceLabel || existing?.meta.voiceLabel,
  };
  let prevReplacedUrl = "";
  if (existing && previewId) {
    voicePreviewCache.delete(previewId);
    voicePreviewCache.set(previewId, { url: existing.url, meta: nextMeta });
  } else {
    url = await verifyAndDecodeColabOneBase64AudioUrl(
      base64Data,
      info.mime || "audio/wav",
      info.sha256,
    );
    if (
      disposed ||
      reqGen !== voicePreviewReqGen ||
      (targetRuntimeId !== undefined &&
        props.state.runtime.id !== targetRuntimeId)
    ) {
      URL.revokeObjectURL(url);
      return false;
    }
    const cacheKey = previewId || `ephemeral:${++voicePreviewEphemeralSeq}`;
    const prevCached = voicePreviewCache.get(cacheKey);
    if (prevCached && prevCached.url && prevCached.url !== url) {
      prevReplacedUrl = prevCached.url;
    }
    voicePreviewCache.delete(cacheKey);
    voicePreviewCache.set(cacheKey, { url, meta: nextMeta });
    evictVoicePreviewCache(url);
  }
  voicePreviewError.value = "";
  voicePreviewAudioUrl.value = url;
  voicePreviewMeta.value = nextMeta;
  loadedVoicePreviewSignature.value = contextKey;
  if (prevReplacedUrl) {
    revokeBlobUrlsDeferred([prevReplacedUrl]);
  }
  return true;
}

function markVoicePresetReadyInCurrentScope(
  presetId: string,
  previewId: string,
  expectedScopeKey?: string,
) {
  const pid = String(presetId || "").trim();
  const prevId = String(previewId || "").trim();
  if (!pid || pid === "custom" || !prevId) return;
  if (expectedScopeKey && currentVoicePrefetchScopeKey.value !== expectedScopeKey) {
    return;
  }
  if (readyVoicePresetMap.value[pid] === prevId) return;
  readyVoicePresetMap.value = {
    ...readyVoicePresetMap.value,
    [pid]: prevId,
  };
}

function buildVoicePreviewPromptsForEngine(engine: ComfyTtsEngineSpec) {
  const voicePromptVisible = engine.supports_voice_design !== false;
  const emotionPromptVisible =
    engine.id !== "moss_tts_v1_5" && engine.id !== "voxcpm2";
  return {
    voicePrompt: voicePromptVisible
      ? String(fieldValues.value["pipeline.voice_prompt"] ?? "").trim()
      : "",
    emotionPrompt: emotionPromptVisible
      ? String(fieldValues.value["pipeline.emotion_prompt"] ?? "").trim()
      : "",
  };
}

async function refreshLocalReadyVoicePresets() {
  const wf = selectedWorkflow.value;
  const engine = selectedTtsEngine.value;
  const scopeKey = currentVoicePrefetchScopeKey.value;
  if (
    !wf ||
    !engine ||
    !isTtsAudioSource.value ||
    hasUploadedVoicePreviewRefAudio.value ||
    !scopeKey
  ) {
    readyVoicePresetMap.value = {};
    return;
  }
  const presets = (engine.voice_presets || []).filter(
    (p) => p?.id && p.id !== "custom",
  );
  if (!presets.length) {
    readyVoicePresetMap.value = {};
    return;
  }
  const { voicePrompt, emotionPrompt } =
    buildVoicePreviewPromptsForEngine(engine);
  const customText = voicePreviewTextCustomEdited.value
    ? voicePreviewText.value.trim()
    : "";
  const items = presets.map((p) => ({
    voice_preset: p.id,
    preview_text: customText,
    voice_prompt: voicePrompt,
    emotion_prompt: emotionPrompt,
  }));
  const gen = ++voicePreviewLookupGen;
  try {
    const res = (await colabOneCommand("comfyui.tts.preview.cache.lookup", {
      engine_id: engine.id,
      items,
    })) as {
      items?: Array<{
        voice_preset?: string;
        preview_id?: string;
        cached?: boolean;
      }>;
    } | null;
    if (
      disposed ||
      gen !== voicePreviewLookupGen ||
      currentVoicePrefetchScopeKey.value !== scopeKey
    ) {
      return;
    }
    const nextMap: Record<string, string> = {};
    for (const entry of res?.items || []) {
      const vp = String(entry?.voice_preset || "").trim();
      const pid = String(entry?.preview_id || "").trim();
      if (vp && pid && entry?.cached) {
        nextMap[vp] = pid;
      }
    }
    // Preserve remote-ready entries ONLY when they belong to the active prefetch job for this exact scope.
    const remotePf = props.state.runtime.comfyui?.tts_preview_prefetch;
    const remoteMatchesScope =
      Boolean(remotePf && remotePf.engine_id === engine.id) &&
      activePrefetchJobScopeKey.value === scopeKey &&
      (!activePrefetchJobId.value ||
        !remotePf?.job_id ||
        remotePf.job_id === activePrefetchJobId.value);
    if (remoteMatchesScope && remotePf) {
      for (const item of remotePf.ready_items || []) {
        const vp = String(item?.voice_preset || "").trim();
        const pid = String(item?.preview_id || "").trim();
        if (vp && pid && !nextMap[vp]) {
          nextMap[vp] = pid;
        }
      }
    }
    readyVoicePresetMap.value = nextMap;
  } catch {
    // Offline/unsupported lookup is non-fatal.
  }
}

async function drainPrefetchPullQueue() {
  if (prefetchPullDraining) return;
  prefetchPullDraining = true;
  try {
    while (prefetchPullQueue.length > 0 && !disposed) {
      const next = prefetchPullQueue.shift();
      if (!next) continue;
      const pullGen = voicePrefetchGen;
      try {
        if (
          voicePreviewPrefetchLocalCancelled.value ||
          next.scopeKey !== currentVoicePrefetchScopeKey.value
        ) {
          continue;
        }
        if (voicePreviewCache.has(next.previewId)) {
          continue;
        }
        if (!gpuReady.value) {
          prefetchPullQueue.length = 0;
          prefetchPendingPreviewIds.clear();
          break;
        }
        const asset = (await colabOneCommand("comfyui.tts.preview.asset", {
          preview_id: next.previewId,
        })) as Partial<ColabOneTtsPreviewResult> | null;
        if (
          disposed ||
          pullGen !== voicePrefetchGen ||
          voicePreviewPrefetchLocalCancelled.value ||
          next.scopeKey !== currentVoicePrefetchScopeKey.value ||
          !asset ||
          typeof asset.data !== "string" ||
          !asset.data.trim()
        ) {
          continue;
        }
        if (voicePreviewCache.has(next.previewId)) {
          continue;
        }
        const url = await verifyAndDecodeColabOneBase64AudioUrl(
          asset.data,
          asset.mime || "audio/wav",
          asset.sha256,
        );
        if (
          disposed ||
          pullGen !== voicePrefetchGen ||
          voicePreviewPrefetchLocalCancelled.value ||
          next.scopeKey !== currentVoicePrefetchScopeKey.value ||
          voicePreviewCache.has(next.previewId)
        ) {
          URL.revokeObjectURL(url);
          continue;
        }
        voicePreviewCache.set(next.previewId, {
          url,
          meta: {
            sampleRate: asset.sample_rate ?? next.sampleRate,
            durationSeconds: asset.duration_seconds,
            cached: true,
            localCached: true,
            engineName: next.engineName,
            voiceLabel: next.voiceLabel,
          },
        });
        evictVoicePreviewCache(url);
      } catch {
        // Non-fatal; asset can still be fetched on demand when clicked.
      } finally {
        prefetchPendingPreviewIds.delete(next.previewId);
      }
    }
  } finally {
    prefetchPullDraining = false;
  }
}

function enqueuePrefetchAssetPull(
  presetId: string,
  previewId: string,
  scopeKey: string,
) {
  if (
    !previewId ||
    voicePreviewCache.has(previewId) ||
    prefetchPendingPreviewIds.has(previewId)
  ) {
    return;
  }
  const engine = selectedTtsEngine.value;
  const wf = selectedWorkflow.value;
  if (!engine || !wf) return;
  const preset = resolveColabOneSelectedVoicePreset(presetId, engine);
  const voiceField = wf.fields.find((f) => f.key === "pipeline.voice_preset");
  const voiceLabel =
    preset?.name || voiceField?.option_labels?.[presetId] || presetId;
  prefetchPendingPreviewIds.add(previewId);
  prefetchPullQueue.push({
    presetId,
    previewId,
    scopeKey,
    engineName: engine.name || engine.id,
    voiceLabel,
    sampleRate: engine.sample_rate,
  });
  void drainPrefetchPullQueue();
}

function cancelVoicePreviewPrefetch() {
  ++voicePrefetchGen;
  voicePreviewPrefetchLocalCancelled.value = true;
  voicePreviewPrefetchStarting.value = false;
  activePrefetchJobId.value = "";
  activePrefetchJobScopeKey.value = "";
  prefetchPullQueue.length = 0;
  prefetchPendingPreviewIds.clear();
  if (!gpuReady.value || isShareMode.value) return;
  void colabOneCommand("comfyui.tts.preview.prefetch.cancel", {}).catch(
    () => {},
  );
}

async function kickoffSiblingVoicePrefetch(activePresetId: string) {
  if (
    disposed ||
    isShareMode.value ||
    !gpuReady.value ||
    !paramDrawerOpen.value ||
    hasUploadedVoicePreviewRefAudio.value ||
    props.state.operation ||
    hasBlockingNonPreviewTask.value
  ) {
    return;
  }
  const wf = selectedWorkflow.value;
  const engine = selectedTtsEngine.value;
  const scopeKey = currentVoicePrefetchScopeKey.value;
  if (!wf || !engine || !isTtsAudioSource.value || !scopeKey) return;
  if (!activePresetId || activePresetId === "custom") return;

  const voiceField = wf.fields.find((f) => f.key === "pipeline.voice_preset");
  const siblingIds = resolveColabOneSiblingVoicePresetsForPrefetch(
    activePresetId,
    voiceField,
    engine,
  );
  const missingIds = siblingIds.filter((id) => !readyVoicePresetMap.value[id]);
  if (missingIds.length === 0) return;

  const { voicePrompt, emotionPrompt } =
    buildVoicePreviewPromptsForEngine(engine);
  const customText = voicePreviewTextCustomEdited.value
    ? voicePreviewText.value.trim()
    : "";
  const items = missingIds.map((id) => ({
    voice_preset: id,
    preview_text: customText,
    voice_prompt: voicePrompt,
    emotion_prompt: emotionPrompt,
  }));

  const pfGen = ++voicePrefetchGen;
  voicePreviewPrefetchLocalCancelled.value = false;
  voicePreviewPrefetchStarting.value = true;
  activePrefetchJobScopeKey.value = scopeKey;
  activePrefetchJobId.value = "";
  try {
    const res = (await colabOneCommand("comfyui.tts.preview.prefetch", {
      engine_id: engine.id,
      items,
    })) as {
      active?: boolean;
      job_id?: string;
      ready_items?: Array<{ voice_preset?: string; preview_id?: string }>;
    } | null;
    if (
      disposed ||
      pfGen !== voicePrefetchGen ||
      voicePreviewPrefetchLocalCancelled.value ||
      currentVoicePrefetchScopeKey.value !== scopeKey
    ) {
      return;
    }
    const returnedJobId =
      typeof res?.job_id === "string" ? res.job_id.trim() : "";
    if (returnedJobId) {
      activePrefetchJobId.value = returnedJobId;
      activePrefetchJobScopeKey.value = scopeKey;
    }
    for (const item of res?.ready_items || []) {
      const vp = String(item?.voice_preset || "").trim();
      const pid = String(item?.preview_id || "").trim();
      if (vp && pid) {
        markVoicePresetReadyInCurrentScope(vp, pid, scopeKey);
        enqueuePrefetchAssetPull(vp, pid, scopeKey);
      }
    }
    if (res?.active) {
      emit("sync");
    }
  } catch {
    // Background prefetch failures never disrupt foreground playback.
  } finally {
    if (pfGen === voicePrefetchGen) {
      voicePreviewPrefetchStarting.value = false;
    }
  }
}

async function tryPlayLocalOrCachedVoicePreview(
  presetId: string,
  opts: { autoPlay: boolean; triggerPrefetch: boolean; reqGen?: number },
): Promise<"played" | "miss" | "handled_or_superseded"> {
  if (hasUploadedVoicePreviewRefAudio.value || !presetId || presetId === "custom") {
    return "miss";
  }
  const wf = selectedWorkflow.value;
  const engine = selectedTtsEngine.value;
  if (!wf || !engine) return "miss";
  const scopeKey = currentVoicePrefetchScopeKey.value;
  const knownPreviewId = readyVoicePresetMap.value[presetId] || "";
  const engineName = engine.name || engine.id;
  const voiceField = wf.fields.find((f) => f.key === "pipeline.voice_preset");
  const preset = resolveColabOneSelectedVoicePreset(presetId, engine);
  const voiceLabel =
    preset?.name || voiceField?.option_labels?.[presetId] || presetId;
  const previewText =
    (voicePreviewTextCustomEdited.value && voicePreviewText.value.trim()) ||
    resolveColabOneVoicePreviewDefaultText(preset);
  const { voicePrompt, emotionPrompt } =
    buildVoicePreviewPromptsForEngine(engine);
  const contextKey = computeVoicePreviewSignature({
    workflowId: wf.id,
    engineId: engine.id,
    voicePresetId: presetId,
    previewText,
    voicePrompt,
    emotionPrompt,
    refText: String(fieldValues.value["pipeline.voice_ref_text"] ?? "").trim(),
    refAudioMarker: "",
    emotionRefMarker: "",
  });

  // L1: instant in-memory Blob URL cache (0 ms).
  if (knownPreviewId) {
    const existing = voicePreviewCache.get(knownPreviewId);
    if (existing) {
      voicePreviewCache.delete(knownPreviewId);
      voicePreviewCache.set(knownPreviewId, existing);
      voicePreviewError.value = "";
      voicePreviewAudioUrl.value = existing.url;
      voicePreviewMeta.value = {
        ...existing.meta,
        cached: true,
        engineName,
        voiceLabel,
      };
      loadedVoicePreviewSignature.value = contextKey;
      if (opts.autoPlay) {
        markVoicePreviewReadyAndPlay();
      }
      if (opts.triggerPrefetch) {
        void kickoffSiblingVoicePrefetch(presetId);
      }
      return "played";
    }
  }

  // L2: local desktop persistent disk cache (~1-2 ms, works offline!).
  const reqGen = opts.reqGen ?? ++voicePreviewReqGen;
  try {
    const localRes = (await colabOneCommand("comfyui.tts.preview.cache.read", {
      preview_id: knownPreviewId,
      engine_id: engine.id,
      voice_preset: presetId,
      preview_text: voicePreviewTextCustomEdited.value ? previewText : "",
      voice_prompt: voicePrompt,
      emotion_prompt: emotionPrompt,
    })) as Partial<ColabOneTtsPreviewResult> | null;
    if (
      disposed ||
      reqGen !== voicePreviewReqGen ||
      currentVoicePrefetchScopeKey.value !== scopeKey
    ) {
      return "handled_or_superseded";
    }
    if (
      localRes?.cached &&
      typeof localRes.data === "string" &&
      localRes.data.trim()
    ) {
      const pid = String(localRes.preview_id || knownPreviewId || "").trim();
      const applied = await applyVoicePreviewAudio(
        pid,
        localRes.data,
        reqGen,
        undefined,
        contextKey,
        {
          mime: localRes.mime,
          sha256: localRes.sha256,
          sampleRate: localRes.sample_rate ?? engine.sample_rate,
          durationSeconds: localRes.duration_seconds,
          cached: true,
          localCached: true,
          engineName,
          voiceLabel,
        },
      );
      if (applied) {
        if (pid) {
          markVoicePresetReadyInCurrentScope(presetId, pid, scopeKey);
        }
        if (opts.autoPlay) {
          markVoicePreviewReadyAndPlay();
        }
        if (opts.triggerPrefetch) {
          void kickoffSiblingVoicePrefetch(presetId);
        }
        return "played";
      }
      if (
        disposed ||
        reqGen !== voicePreviewReqGen ||
        currentVoicePrefetchScopeKey.value !== scopeKey
      ) {
        return "handled_or_superseded";
      }
    }
  } catch {
    if (
      disposed ||
      reqGen !== voicePreviewReqGen ||
      currentVoicePrefetchScopeKey.value !== scopeKey
    ) {
      return "handled_or_superseded";
    }
    // Fall through to remote fetch if local disk read failed.
  }

  // L3: remote prefetched asset is ready on remote disk; fetch once and auto-play.
  if (knownPreviewId && gpuReady.value && reqGen === voicePreviewReqGen) {
    activeVoicePreviewEngineName.value = engineName;
    activeVoicePreviewVoiceLabel.value = voiceLabel;
    activeVoicePreviewSampleRate.value = engine.sample_rate;
    activeVoicePreviewId.value = knownPreviewId;
    activeVoicePreviewContextKey.value = contextKey;
    activeVoicePreviewPresetId.value = presetId;
    activeVoicePreviewScopeKey.value = scopeKey;
    activeVoicePreviewAllowPresetReady.value = true;
    activeVoicePreviewRuntimeId.value = props.state.runtime.id;
    const fetched = await fetchVoicePreviewAsset(
      knownPreviewId,
      reqGen,
      props.state.runtime.id,
      contextKey,
      true,
      presetId,
      scopeKey,
      true,
    );
    if (fetched) {
      return "played";
    }
    if (
      !disposed &&
      reqGen === voicePreviewReqGen &&
      currentVoicePrefetchScopeKey.value === scopeKey &&
      gpuReady.value
    ) {
      if (
        Object.prototype.hasOwnProperty.call(readyVoicePresetMap.value, presetId)
      ) {
        const nextReadyMap = { ...readyVoicePresetMap.value };
        delete nextReadyMap[presetId];
        readyVoicePresetMap.value = nextReadyMap;
      }
      voicePreviewError.value = "";
      return "miss";
    }
    return "handled_or_superseded";
  }

  if (
    disposed ||
    reqGen !== voicePreviewReqGen ||
    currentVoicePrefetchScopeKey.value !== scopeKey
  ) {
    return "handled_or_superseded";
  }

  return "miss";
}

function markVoicePreviewReadyAndPlay() {
  voicePreviewLookingUp.value = false;
  voicePreviewSubmitting.value = false;
  voicePreviewFetchingAsset.value = false;
  activeVoicePreviewTaskId.value = "";
  activeVoicePreviewId.value = "";
  activeVoicePreviewContextKey.value = "";
  activeVoicePreviewPresetId.value = "";
  activeVoicePreviewScopeKey.value = "";
  activeVoicePreviewAllowPresetReady.value = true;
  activeVoicePreviewTaskObserved = false;
  activeVoicePreviewTaskStartedAt = 0;
  voicePreviewStatus.value = "";
  if (
    paramDrawerOpen.value &&
    activeTab.value === "studio" &&
    isTtsAudioSource.value
  ) {
    voicePreviewPlayToken.value += 1;
  }
}

async function fetchVoicePreviewAsset(
  previewId: string,
  reqGen: number,
  targetRuntimeId: string | undefined,
  contextKey: string,
  wasCached: boolean,
  presetId?: string,
  scopeKey?: string,
  allowPresetReady?: boolean,
): Promise<boolean> {
  const effectivePresetId = presetId || selectedVoicePresetId.value;
  const effectiveScopeKey = scopeKey || currentVoicePrefetchScopeKey.value;
  const canMarkPresetReady =
    (allowPresetReady ?? !hasUploadedVoicePreviewRefAudio.value) &&
    !hasUploadedVoicePreviewRefAudio.value;
  const existing = voicePreviewCache.get(previewId);
  if (existing) {
    voicePreviewCache.delete(previewId);
    voicePreviewCache.set(previewId, existing);
    voicePreviewError.value = "";
    voicePreviewAudioUrl.value = existing.url;
    voicePreviewMeta.value = {
      ...existing.meta,
      cached: true,
    };
    loadedVoicePreviewSignature.value = contextKey;
    if (canMarkPresetReady) {
      markVoicePresetReadyInCurrentScope(
        effectivePresetId,
        previewId,
        effectiveScopeKey,
      );
    }
    markVoicePreviewReadyAndPlay();
    if (canMarkPresetReady) {
      void kickoffSiblingVoicePrefetch(effectivePresetId);
    }
    return true;
  }

  voicePreviewFetchingAsset.value = true;
  try {
    const asset = (await colabOneCommand("comfyui.tts.preview.asset", {
      preview_id: previewId,
    })) as Partial<ColabOneTtsPreviewResult> | null;
    if (
      disposed ||
      reqGen !== voicePreviewReqGen ||
      props.state.runtime.id !== targetRuntimeId
    ) {
      return false;
    }
    if (!asset || typeof asset.data !== "string" || !asset.data.trim()) {
      throw new Error(t("comfy.errInvalidOutputTransfer"));
    }
    const applied = await applyVoicePreviewAudio(
      previewId,
      asset.data,
      reqGen,
      targetRuntimeId,
      contextKey,
      {
        mime: asset.mime,
        sha256: asset.sha256,
        sampleRate: asset.sample_rate ?? activeVoicePreviewSampleRate.value,
        durationSeconds: asset.duration_seconds,
        cached: wasCached || Boolean(asset.cached),
        engineName: activeVoicePreviewEngineName.value,
        voiceLabel: activeVoicePreviewVoiceLabel.value,
      },
    );
    if (applied) {
      if (canMarkPresetReady) {
        markVoicePresetReadyInCurrentScope(
          effectivePresetId,
          previewId,
          effectiveScopeKey,
        );
      }
      markVoicePreviewReadyAndPlay();
      if (canMarkPresetReady) {
        void kickoffSiblingVoicePrefetch(effectivePresetId);
      }
      return true;
    }
    return false;
  } catch (e) {
    if (!disposed && reqGen === voicePreviewReqGen) {
      voicePreviewError.value = formatDraftErrorDetail(e) || String(e);
      voicePreviewStatus.value = "";
    }
    return false;
  } finally {
    if (reqGen === voicePreviewReqGen) {
      voicePreviewFetchingAsset.value = false;
      activeVoicePreviewTaskId.value = "";
      activeVoicePreviewId.value = "";
      activeVoicePreviewContextKey.value = "";
      activeVoicePreviewPresetId.value = "";
      activeVoicePreviewScopeKey.value = "";
      activeVoicePreviewAllowPresetReady.value = true;
      activeVoicePreviewTaskObserved = false;
      activeVoicePreviewTaskStartedAt = 0;
    }
  }
}

async function ensureVoicePreviewInputUploaded(
  fieldKey: string,
  reqGen: number,
  targetRuntimeId: string | undefined,
  targetWorkflowId: string,
): Promise<string> {
  const existingId = uploadedInputIds.value[fieldKey];
  if (typeof existingId === "string" && existingId.trim()) {
    return existingId.trim();
  }
  const mediaGen = uploadGen[fieldKey] ?? 0;
  const loadGen = draftLoadGen;
  const isStale = () =>
    disposed ||
    reqGen !== voicePreviewReqGen ||
    loadGen !== draftLoadGen ||
    (uploadGen[fieldKey] ?? 0) !== mediaGen ||
    props.state.runtime.id !== targetRuntimeId ||
    selectedWorkflowId.value !== targetWorkflowId;

  if (!pendingUploads.value[fieldKey] && draftMediaMeta.value[fieldKey]) {
    const draftMediaWfId = resolveDraftMediaWorkflowId(
      fieldKey,
      targetWorkflowId,
    );
    let payload = null;
    let draftReadErr: unknown = null;
    try {
      payload = await readColabOneWorkflowDraftMedia(draftMediaWfId, fieldKey);
    } catch (readErr) {
      draftReadErr = readErr;
      payload = null;
    }
    if (isStale()) {
      return "";
    }
    if (payload && payload.data) {
      pendingUploads.value[fieldKey] = {
        filename: payload.filename,
        data: payload.data,
        kind: payload.kind,
      };
    } else {
      const nextMeta = { ...draftMediaMeta.value };
      delete nextMeta[fieldKey];
      draftMediaMeta.value = nextMeta;
      delete draftMediaSourceIds.value[fieldKey];
      void deleteColabOneWorkflowDraftMedia(draftMediaWfId, fieldKey).catch(
        () => {},
      );
      if (draftMediaWfId !== targetWorkflowId) {
        void deleteColabOneWorkflowDraftMedia(
          targetWorkflowId,
          fieldKey,
        ).catch(() => {});
      }
      revokeLocalPreview(fieldKey);
      delete uploadedNames.value[fieldKey];
      const field = selectedWorkflow.value?.fields.find(
        (f) => f.key === fieldKey,
      );
      fieldValues.value[fieldKey] =
        field && field.default !== undefined && field.default !== null
          ? String(field.default)
          : "";
      throw new Error(
        draftReadErr
          ? formatDraftErrorDetail(draftReadErr)
          : t("comfy.errReadLocalFile"),
      );
    }
  }
  const pending = pendingUploads.value[fieldKey];
  if (!pending) {
    return "";
  }
  uploadingFields.value = { ...uploadingFields.value, [fieldKey]: true };
  try {
    const res = await colabOneCommand("comfyui.input.upload", {
      filename: pending.filename,
      data: pending.data,
    });
    if (isStale()) {
      return "";
    }
    const assetId = typeof res?.id === "string" ? res.id.trim() : "";
    if (!assetId) {
      throw new Error(
        t("comfy.errUploadNamedFailed", { name: pending.filename }),
      );
    }
    uploadedInputIds.value[fieldKey] = assetId;
    fieldValues.value[fieldKey] = res?.filename || pending.filename;
    uploadedNames.value[fieldKey] = pending.filename;
    delete pendingUploads.value[fieldKey];
    return assetId;
  } finally {
    if ((uploadGen[fieldKey] ?? 0) === mediaGen) {
      const next = { ...uploadingFields.value };
      delete next[fieldKey];
      uploadingFields.value = next;
    }
  }
}

function reconcileActiveVoicePreviewTask(
  tasks: ColabOneSnapshot["runtime"]["tasks"],
  rtState: string,
) {
  if (rtState !== "ready") {
    if (detachedVoicePreviewTaskIds.size > 0) {
      detachedVoicePreviewTaskIds.clear();
      detachedVoicePreviewVersion.value += 1;
    }
    if (
      activeVoicePreviewTaskId.value ||
      voicePreviewSubmitting.value ||
      voicePreviewFetchingAsset.value
    ) {
      resetVoicePreviewTaskTracking();
      voicePreviewError.value = "";
    }
    return;
  }
  const taskList = tasks || [];
  if (detachedVoicePreviewTaskIds.size > 0) {
    const runningIds = new Set(
      taskList
        .filter((item) => item.state === "running")
        .map((item) => item.id),
    );
    let pruned = false;
    for (const detachedId of Array.from(detachedVoicePreviewTaskIds)) {
      if (!runningIds.has(detachedId)) {
        detachedVoicePreviewTaskIds.delete(detachedId);
        pruned = true;
      }
    }
    if (pruned) {
      detachedVoicePreviewVersion.value += 1;
    }
  }
  if (!activeVoicePreviewTaskId.value) {
    const runningExternal = taskList.find(
      (item) =>
        item.kind === "comfyui_tts_preview" &&
        item.state === "running" &&
        !detachedVoicePreviewTaskIds.has(item.id),
    );
    if (runningExternal) {
      activeVoicePreviewTaskId.value = runningExternal.id;
      activeVoicePreviewRuntimeId.value = props.state.runtime.id;
      activeVoicePreviewPresetId.value = "";
      activeVoicePreviewScopeKey.value = "";
      activeVoicePreviewAllowPresetReady.value = false;
      activeVoicePreviewTaskObserved = true;
      activeVoicePreviewTaskStartedAt = Date.now();
      voicePreviewStatus.value =
        runningExternal.message || t("comfy.voicePreviewBtnGenerating");
    }
    return;
  }
  const taskId = activeVoicePreviewTaskId.value;
  const matched = taskList.find((item) => item.id === taskId);
  if (!matched) {
    const graceExpired =
      activeVoicePreviewTaskStartedAt > 0 &&
      Date.now() - activeVoicePreviewTaskStartedAt >=
        VOICE_PREVIEW_TASK_MISSING_GRACE_MS;
    if (!activeVoicePreviewTaskObserved && !graceExpired) {
      return;
    }
    const previewId = activeVoicePreviewId.value;
    const reqGen = voicePreviewReqGen;
    const targetRuntimeId = activeVoicePreviewRuntimeId.value;
    const contextKey = activeVoicePreviewContextKey.value;
    const capturedPresetId = activeVoicePreviewPresetId.value || undefined;
    const capturedScopeKey = activeVoicePreviewScopeKey.value || undefined;
    const capturedAllowPresetReady = activeVoicePreviewAllowPresetReady.value;
    activeVoicePreviewTaskId.value = "";
    activeVoicePreviewTaskObserved = false;
    activeVoicePreviewTaskStartedAt = 0;
    if (previewId && !voicePreviewFetchingAsset.value) {
      void fetchVoicePreviewAsset(
        previewId,
        reqGen,
        targetRuntimeId,
        contextKey,
        false,
        capturedPresetId,
        capturedScopeKey,
        capturedAllowPresetReady,
      );
    } else if (!voicePreviewFetchingAsset.value) {
      activeVoicePreviewId.value = "";
      activeVoicePreviewContextKey.value = "";
      activeVoicePreviewPresetId.value = "";
      activeVoicePreviewScopeKey.value = "";
      activeVoicePreviewAllowPresetReady.value = true;
      voicePreviewStatus.value = "";
    }
    return;
  }
  activeVoicePreviewTaskObserved = true;
  if (matched.state === "running") {
    if (matched.message) {
      voicePreviewStatus.value = matched.message;
    }
    return;
  }
  const previewId = activeVoicePreviewId.value;
  const reqGen = voicePreviewReqGen;
  const targetRuntimeId = activeVoicePreviewRuntimeId.value;
  const contextKey = activeVoicePreviewContextKey.value;
  const capturedPresetId = activeVoicePreviewPresetId.value || undefined;
  const capturedScopeKey = activeVoicePreviewScopeKey.value || undefined;
  const capturedAllowPresetReady = activeVoicePreviewAllowPresetReady.value;
  if (matched.state === "completed") {
    activeVoicePreviewTaskId.value = "";
    activeVoicePreviewTaskObserved = false;
    activeVoicePreviewTaskStartedAt = 0;
    if (previewId && !voicePreviewFetchingAsset.value) {
      void fetchVoicePreviewAsset(
        previewId,
        reqGen,
        targetRuntimeId,
        contextKey,
        false,
        capturedPresetId,
        capturedScopeKey,
        capturedAllowPresetReady,
      );
    } else if (!voicePreviewFetchingAsset.value) {
      activeVoicePreviewId.value = "";
      activeVoicePreviewContextKey.value = "";
      activeVoicePreviewPresetId.value = "";
      activeVoicePreviewScopeKey.value = "";
      activeVoicePreviewAllowPresetReady.value = true;
      voicePreviewStatus.value = "";
    }
    return;
  }
  if (matched.state === "cancelled") {
    activeVoicePreviewTaskId.value = "";
    activeVoicePreviewId.value = "";
    activeVoicePreviewContextKey.value = "";
    activeVoicePreviewPresetId.value = "";
    activeVoicePreviewScopeKey.value = "";
    activeVoicePreviewAllowPresetReady.value = true;
    activeVoicePreviewTaskObserved = false;
    activeVoicePreviewTaskStartedAt = 0;
    voicePreviewStatus.value = "";
    voicePreviewError.value = "";
    return;
  }
  if (matched.state === "failed") {
    activeVoicePreviewTaskId.value = "";
    activeVoicePreviewId.value = "";
    activeVoicePreviewContextKey.value = "";
    activeVoicePreviewPresetId.value = "";
    activeVoicePreviewScopeKey.value = "";
    activeVoicePreviewAllowPresetReady.value = true;
    activeVoicePreviewTaskObserved = false;
    activeVoicePreviewTaskStartedAt = 0;
    voicePreviewStatus.value = "";
    voicePreviewError.value =
      matched.error || matched.message || t("status.failed");
  }
}

async function triggerVoicePreview() {
  if (
    voicePreviewBusy.value ||
    submittingRun.value ||
    hasUploadingField.value
  ) {
    return;
  }
  // Zero-RPC instant local replay when the exact loaded audition audio is still current
  if (
    voicePreviewAudioUrl.value &&
    loadedVoicePreviewSignature.value &&
    currentVoicePreviewSignature.value === loadedVoicePreviewSignature.value
  ) {
    voicePreviewError.value = "";
    markVoicePreviewReadyAndPlay();
    void kickoffSiblingVoicePrefetch(selectedVoicePresetId.value);
    return;
  }
  const wf = selectedWorkflow.value;
  const engine = selectedTtsEngine.value;
  if (!wf || !engine) {
    return;
  }

  // Try L1 in-memory cache or L2 local desktop disk cache first (0-2 ms, works even offline!).
  if (
    !hasUploadedVoicePreviewRefAudio.value &&
    selectedVoicePresetId.value &&
    selectedVoicePresetId.value !== "custom"
  ) {
    const lookupGen = ++voicePreviewReqGen;
    voicePreviewLookingUp.value = true;
    let localOutcome: "played" | "miss" | "handled_or_superseded" = "miss";
    try {
      localOutcome = await tryPlayLocalOrCachedVoicePreview(
        selectedVoicePresetId.value,
        { autoPlay: true, triggerPrefetch: true, reqGen: lookupGen },
      );
    } finally {
      if (lookupGen === voicePreviewReqGen) {
        voicePreviewLookingUp.value = false;
      }
    }
    if (
      localOutcome !== "miss" ||
      disposed ||
      lookupGen !== voicePreviewReqGen ||
      selectedWorkflow.value?.id !== wf.id ||
      selectedTtsEngine.value?.id !== engine.id
    ) {
      return;
    }
  }

  if (!gpuReady.value) {
    return;
  }
  if (isShareMode.value && props.shareBusyOther) {
    voicePreviewError.value = t("workflowShare.guestBusyOtherBannerTitle");
    return;
  }
  if (props.state.operation || hasBlockingNonPreviewTask.value) {
    voicePreviewError.value = t("comfy.runBlockedBusy");
    return;
  }

  // Snapshot all parameters before any async upload or validation awaits
  const targetRuntimeId = props.state.runtime.id;
  const targetWorkflowId = wf.id;
  const engineId = engine.id;
  const voicePresetId = selectedVoicePresetId.value;
  const engineName = engine.name || engine.id;
  const voiceField = wf.fields.find((f) => f.key === "pipeline.voice_preset");
  const voiceLabel =
    selectedVoicePreset.value?.name ||
    voiceField?.option_labels?.[voicePresetId] ||
    voicePresetId;
  const previewText =
    voicePreviewText.value.trim() || voicePreviewDefaultText.value;
  const voicePromptVisible = engine.supports_voice_design !== false;
  const emotionPromptVisible =
    engine.id !== "moss_tts_v1_5" && engine.id !== "voxcpm2";
  const voicePrompt = voicePromptVisible
    ? String(fieldValues.value["pipeline.voice_prompt"] ?? "").trim()
    : "";
  const emotionPrompt = emotionPromptVisible
    ? String(fieldValues.value["pipeline.emotion_prompt"] ?? "").trim()
    : "";
  const refText = String(
    fieldValues.value["pipeline.voice_ref_text"] ?? "",
  ).trim();
  const emotionRefField = wf.fields.find(
    (f) => f.key === "pipeline.emotion_ref_audio",
  );
  const emotionRefVisible = Boolean(
    emotionRefField &&
      isWorkflowFieldVisible(emotionRefField, fieldValues.value),
  );

  const hasVoiceRefAudio = Boolean(
    uploadedInputIds.value["pipeline.voice_ref_audio"] ||
      pendingUploads.value["pipeline.voice_ref_audio"] ||
      draftMediaMeta.value["pipeline.voice_ref_audio"],
  );
  const cloneOnlyMissingVoiceRef = voiceRefRequired.value && !hasVoiceRefAudio;
  if (cloneOnlyMissingVoiceRef) {
    voicePreviewError.value = cloneOnlyVoiceRefMessage(engine);
    return;
  }
  if (voiceRefTextRequired.value && !refText) {
    voicePreviewError.value = voiceRefTextRequiredMessage(engineId);
    return;
  }
  if (!previewText) {
    voicePreviewError.value = tb("试听文本不能为空");
    return;
  }
  if (Array.from(previewText).length > 600) {
    voicePreviewError.value = tb("试听文本长度不能超过 600");
    return;
  }
  if (Array.from(voicePrompt).length > 600) {
    voicePreviewError.value = tb("音色描述长度不能超过 600");
    return;
  }
  if (Array.from(refText).length > 600) {
    voicePreviewError.value = tb("参考音频文本长度不能超过 600");
    return;
  }
  if (Array.from(emotionPrompt).length > 240) {
    voicePreviewError.value = tb("情绪描述长度不能超过 240");
    return;
  }

  let contextKey = computeVoicePreviewSignature({
    workflowId: targetWorkflowId,
    engineId,
    voicePresetId,
    previewText,
    voicePrompt,
    emotionPrompt,
    refText,
    refAudioMarker: voicePreviewMediaMarker("pipeline.voice_ref_audio"),
    emotionRefMarker: emotionRefVisible
      ? voicePreviewMediaMarker("pipeline.emotion_ref_audio")
      : "",
  });

  if (!voicePreviewText.value.trim()) {
    voicePreviewText.value = previewText;
    voicePreviewTextCustomEdited.value = false;
  }

  const reqGen = ++voicePreviewReqGen;
  voicePreviewSubmitting.value = true;
  voicePreviewError.value = "";
  voicePreviewStatus.value = t("comfy.voicePreviewBtnGenerating");
  activeVoicePreviewTaskId.value = "";
  activeVoicePreviewId.value = "";
  activeVoicePreviewContextKey.value = contextKey;
  activeVoicePreviewEngineName.value = engineName;
  activeVoicePreviewVoiceLabel.value = voiceLabel;
  activeVoicePreviewSampleRate.value = engine.sample_rate;
  activeVoicePreviewRuntimeId.value = targetRuntimeId;

  try {
    const refAudioInputId = await ensureVoicePreviewInputUploaded(
      "pipeline.voice_ref_audio",
      reqGen,
      targetRuntimeId,
      targetWorkflowId,
    );
    if (
      disposed ||
      reqGen !== voicePreviewReqGen ||
      props.state.runtime.id !== targetRuntimeId
    ) {
      return;
    }
    const emotionRefInputId = emotionRefVisible
      ? await ensureVoicePreviewInputUploaded(
          "pipeline.emotion_ref_audio",
          reqGen,
          targetRuntimeId,
          targetWorkflowId,
        )
      : "";
    if (
      disposed ||
      reqGen !== voicePreviewReqGen ||
      props.state.runtime.id !== targetRuntimeId
    ) {
      return;
    }

    contextKey = computeVoicePreviewSignature({
      workflowId: targetWorkflowId,
      engineId,
      voicePresetId,
      previewText,
      voicePrompt,
      emotionPrompt,
      refText,
      refAudioMarker: refAudioInputId ? `id:${refAudioInputId}` : "",
      emotionRefMarker: emotionRefInputId ? `id:${emotionRefInputId}` : "",
    });
    activeVoicePreviewContextKey.value = contextKey;

    const payload: ColabOneComfyTtsPreviewRequest = {
      engine_id: engineId,
      voice_preset: voicePresetId,
      preview_text: previewText,
      voice_prompt: voicePrompt,
      emotion_prompt: emotionPrompt,
      ref_audio_input_id: refAudioInputId,
      ref_text: refText,
      emotion_ref_input_id: emotionRefInputId,
    };

    const res = (await colabOneCommand(
      "comfyui.tts.preview",
      payload as unknown as Record<string, unknown>,
    )) as ColabOneTtsPreviewResult | null;

    if (
      disposed ||
      reqGen !== voicePreviewReqGen ||
      props.state.runtime.id !== targetRuntimeId
    ) {
      if (
        !disposed &&
        reqGen !== voicePreviewReqGen &&
        props.state.runtime.id === targetRuntimeId
      ) {
        const staleTaskId =
          typeof res?.task?.id === "string" ? res.task.id.trim() : "";
        if (staleTaskId) {
          detachedVoicePreviewTaskIds.add(staleTaskId);
          detachedVoicePreviewVersion.value += 1;
          if (activeVoicePreviewTaskId.value === staleTaskId) {
            activeVoicePreviewTaskId.value = "";
            voicePreviewStatus.value = "";
          }
        }
      }
      return;
    }

    const previewId =
      typeof res?.preview_id === "string" ? res.preview_id.trim() : "";
    const targetScopeKey = currentVoicePrefetchScopeKey.value;

    if (res?.cached && typeof res.data === "string" && res.data.trim()) {
      const applied = await applyVoicePreviewAudio(
        previewId,
        res.data,
        reqGen,
        targetRuntimeId,
        contextKey,
        {
          mime: res.mime,
          sha256: res.sha256,
          sampleRate: res.sample_rate ?? engine.sample_rate,
          durationSeconds: res.duration_seconds,
          cached: true,
          localCached: Boolean(res.local_cached),
          engineName,
          voiceLabel,
        },
      );
      if (applied) {
        if (!refAudioInputId && !emotionRefInputId && previewId) {
          markVoicePresetReadyInCurrentScope(
            voicePresetId,
            previewId,
            targetScopeKey,
          );
        }
        markVoicePreviewReadyAndPlay();
        if (!refAudioInputId && !emotionRefInputId) {
          void kickoffSiblingVoicePrefetch(voicePresetId);
        }
      }
      return;
    }

    if (res?.cached && previewId) {
      const existing = voicePreviewCache.get(previewId);
      if (existing) {
        voicePreviewCache.delete(previewId);
        voicePreviewCache.set(previewId, existing);
        voicePreviewError.value = "";
        voicePreviewAudioUrl.value = existing.url;
        voicePreviewMeta.value = {
          ...existing.meta,
          cached: true,
        };
        loadedVoicePreviewSignature.value = contextKey;
        if (!refAudioInputId && !emotionRefInputId) {
          markVoicePresetReadyInCurrentScope(
            voicePresetId,
            previewId,
            targetScopeKey,
          );
        }
        markVoicePreviewReadyAndPlay();
        if (!refAudioInputId && !emotionRefInputId) {
          void kickoffSiblingVoicePrefetch(voicePresetId);
        }
        return;
      }
      const allowPresetReady = !refAudioInputId && !emotionRefInputId;
      activeVoicePreviewId.value = previewId;
      activeVoicePreviewPresetId.value = voicePresetId;
      activeVoicePreviewScopeKey.value = targetScopeKey;
      activeVoicePreviewAllowPresetReady.value = allowPresetReady;
      await fetchVoicePreviewAsset(
        previewId,
        reqGen,
        targetRuntimeId,
        contextKey,
        true,
        voicePresetId,
        targetScopeKey,
        allowPresetReady,
      );
      return;
    }

    const taskId = typeof res?.task?.id === "string" ? res.task.id.trim() : "";
    if (taskId && previewId) {
      const allowPresetReady = !refAudioInputId && !emotionRefInputId;
      if (detachedVoicePreviewTaskIds.delete(taskId)) {
        detachedVoicePreviewVersion.value += 1;
      }
      activeVoicePreviewId.value = previewId;
      activeVoicePreviewContextKey.value = contextKey;
      activeVoicePreviewPresetId.value = voicePresetId;
      activeVoicePreviewScopeKey.value = targetScopeKey;
      activeVoicePreviewAllowPresetReady.value = allowPresetReady;
      activeVoicePreviewSampleRate.value =
        res?.sample_rate ?? engine.sample_rate;
      voicePreviewStatus.value =
        res?.task?.message?.trim() || t("comfy.voicePreviewBtnGenerating");
      emit("sync");
      if (res?.task?.state === "completed") {
        await fetchVoicePreviewAsset(
          previewId,
          reqGen,
          targetRuntimeId,
          contextKey,
          false,
          voicePresetId,
          targetScopeKey,
          allowPresetReady,
        );
      } else if (res?.task?.state === "cancelled") {
        activeVoicePreviewTaskId.value = "";
        activeVoicePreviewId.value = "";
        activeVoicePreviewContextKey.value = "";
        activeVoicePreviewPresetId.value = "";
        activeVoicePreviewScopeKey.value = "";
        activeVoicePreviewAllowPresetReady.value = true;
        activeVoicePreviewTaskObserved = false;
        activeVoicePreviewTaskStartedAt = 0;
        voicePreviewStatus.value = "";
        voicePreviewError.value = "";
      } else if (res?.task?.state === "failed") {
        activeVoicePreviewTaskId.value = "";
        activeVoicePreviewId.value = "";
        activeVoicePreviewContextKey.value = "";
        activeVoicePreviewPresetId.value = "";
        activeVoicePreviewScopeKey.value = "";
        activeVoicePreviewAllowPresetReady.value = true;
        activeVoicePreviewTaskObserved = false;
        activeVoicePreviewTaskStartedAt = 0;
        voicePreviewStatus.value = "";
        voicePreviewError.value =
          res.task.error || res.task.message || t("status.failed");
      } else {
        activeVoicePreviewTaskObserved = false;
        activeVoicePreviewTaskStartedAt = Date.now();
        activeVoicePreviewTaskId.value = taskId;
        reconcileActiveVoicePreviewTask(
          props.state.runtime.tasks,
          props.state.runtime.state,
        );
      }
      return;
    }

    if (typeof res?.data === "string" && res.data.trim()) {
      const applied = await applyVoicePreviewAudio(
        previewId,
        res.data,
        reqGen,
        targetRuntimeId,
        contextKey,
        {
          mime: res.mime,
          sha256: res.sha256,
          sampleRate: res.sample_rate ?? engine.sample_rate,
          durationSeconds: res.duration_seconds,
          cached: Boolean(res.cached),
          localCached: Boolean(res.local_cached),
          engineName,
          voiceLabel,
        },
      );
      if (applied) {
        if (!refAudioInputId && !emotionRefInputId && previewId) {
          markVoicePresetReadyInCurrentScope(
            voicePresetId,
            previewId,
            targetScopeKey,
          );
        }
        markVoicePreviewReadyAndPlay();
        if (!refAudioInputId && !emotionRefInputId) {
          void kickoffSiblingVoicePrefetch(voicePresetId);
        }
      }
      return;
    }

    throw new Error(t("comfy.errInvalidOutputTransfer"));
  } catch (e) {
    if (!disposed && reqGen === voicePreviewReqGen) {
      activeVoicePreviewTaskId.value = "";
      activeVoicePreviewId.value = "";
      activeVoicePreviewContextKey.value = "";
      voicePreviewStatus.value = "";
      voicePreviewError.value = formatDraftErrorDetail(e) || String(e);
    }
  } finally {
    if (reqGen === voicePreviewReqGen) {
      voicePreviewSubmitting.value = false;
    }
  }
}

watch(
  voicePreviewDefaultText,
  (nextDefault) => {
    if (!voicePreviewTextCustomEdited.value || !voicePreviewText.value.trim()) {
      voicePreviewText.value = nextDefault;
    }
  },
  { immediate: true },
);

watch(selectedVoicePresetId, (nextId, prevId) => {
  if (nextId === prevId) return;
  if (!voicePreviewTextCustomEdited.value || !voicePreviewText.value.trim()) {
    voicePreviewText.value = voicePreviewDefaultText.value;
    voicePreviewTextCustomEdited.value = false;
  }
  detachActiveVoicePreviewRequest();
  const shouldAutoPlay =
    Boolean(pendingAutoPlayVoicePresetId) &&
    pendingAutoPlayVoicePresetId === nextId &&
    paramDrawerOpen.value &&
    activeTab.value === "studio" &&
    isTtsAudioSource.value &&
    !hasUploadedVoicePreviewRefAudio.value &&
    nextId !== "custom";
  pendingAutoPlayVoicePresetId = "";
  if (shouldAutoPlay && readyVoicePresetMap.value[nextId]) {
    void tryPlayLocalOrCachedVoicePreview(nextId, {
      autoPlay: true,
      triggerPrefetch: true,
    });
  }
});

watch(
  () => selectedTtsEngine.value?.id,
  (nextId, prevId) => {
    const engine = selectedTtsEngine.value;
    if (engine) {
      reconcileVoicePresetForEngine(engine);
    }
    if (prevId && nextId !== prevId) {
      detachActiveVoicePreviewRequest();
      cancelVoicePreviewPrefetch();
    }
  },
);

watch(
  () => fieldValues.value["pipeline.audio_source"],
  (nextSource, prevSource) => {
    if (prevSource !== undefined && nextSource !== prevSource) {
      detachActiveVoicePreviewRequest();
      if (nextSource !== "tts") {
        cancelVoicePreviewPrefetch();
      }
    }
  },
);

watch(
  [currentVoicePrefetchScopeKey, paramDrawerOpen],
  ([scopeKey, open], prev) => {
    const prevScopeKey = prev?.[0] || "";
    prefetchPullQueue.length = 0;
    if (prevScopeKey && prevScopeKey !== scopeKey) {
      const remotePf = props.state.runtime.comfyui?.tts_preview_prefetch;
      if (
        activePrefetchJobScopeKey.value === prevScopeKey ||
        remotePf?.active
      ) {
        cancelVoicePreviewPrefetch();
      } else {
        activePrefetchJobId.value = "";
        activePrefetchJobScopeKey.value = "";
      }
    }
    if (!open || !scopeKey) {
      return;
    }
    voicePreviewPrefetchLocalCancelled.value = false;
    void refreshLocalReadyVoicePresets();
  },
  { immediate: true },
);

watch(
  () => props.state.runtime.comfyui?.tts_preview_prefetch,
  (pf) => {
    if (!pf || !paramDrawerOpen.value || hasUploadedVoicePreviewRefAudio.value) {
      return;
    }
    const engine = selectedTtsEngine.value;
    const scopeKey = currentVoicePrefetchScopeKey.value;
    if (!engine || !scopeKey || pf.engine_id !== engine.id) return;
    if (activePrefetchJobScopeKey.value !== scopeKey) return;
    if (
      activePrefetchJobId.value &&
      pf.job_id &&
      pf.job_id !== activePrefetchJobId.value
    ) {
      return;
    }
    if (!pf.active) {
      voicePreviewPrefetchStarting.value = false;
    }
    for (const item of pf.ready_items || []) {
      const vp = String(item?.voice_preset || "").trim();
      const pid = String(item?.preview_id || "").trim();
      if (!vp || !pid) continue;
      markVoicePresetReadyInCurrentScope(vp, pid, scopeKey);
      if (gpuReady.value) {
        enqueuePrefetchAssetPull(vp, pid, scopeKey);
      }
    }
  },
  { deep: true },
);

watch(
  () =>
    [
      props.state.runtime.tasks,
      props.state.runtime.state,
      activeVoicePreviewTaskId.value,
      activeVoicePreviewTaskId.value ? props.clockNow : 0,
    ] as const,
  ([tasks, rtState]) => {
    reconcileActiveVoicePreviewTask(tasks, rtState);
  },
  { deep: true, immediate: true },
);

function cancelDraftSaveTimer() {
  if (draftSaveTimer !== null) {
    clearTimeout(draftSaveTimer);
    draftSaveTimer = null;
  }
}

function flushDraftSave(
  overrideWf?: WorkflowTemplate,
  opts?: { silent?: boolean },
) {
  cancelDraftSaveTimer();
  if (!draftDirty) {
    return;
  }
  const wf =
    overrideWf ??
    (draftWorkflowId
      ? (workflows.value.find((w) => w.id === draftWorkflowId) ??
        (activeDraftWorkflow?.id === draftWorkflowId
          ? activeDraftWorkflow
          : undefined))
      : undefined);
  draftDirty = false;
  if (!wf) {
    return;
  }
  const targetWorkflowId = wf.id;
  const loadGen = draftLoadGen;
  const saveGen = ++draftSaveGen;
  const fieldEpoch = bumpDraftFieldEpoch(targetWorkflowId);
  const snapshot = { ...fieldValues.value };
  const sanitized = sanitizeColabOneDraftFieldsForSave(wf.fields, snapshot);
  const isBuiltinDh = isColabOneBuiltinDigitalHumanWorkflow(wf);

  // If this digital human draft adopted media from a sibling workflow on restore,
  // materialize those files onto `targetWorkflowId` alongside the text save so a
  // newer `updated_at` on `targetWorkflowId` never orphans cross-sibling media.
  const crossSiblingMediaToMaterialize: {
    key: string;
    epoch: number;
    mediaGen: number;
    pending?: PendingUpload;
    sourceId: string;
  }[] = [];
  if (isBuiltinDh && draftWorkflowId === targetWorkflowId) {
    for (const [key, meta] of Object.entries(draftMediaMeta.value)) {
      const sourceId = draftMediaSourceIds.value[key];
      if (!meta || !sourceId || sourceId === targetWorkflowId) {
        continue;
      }
      const pending = pendingUploads.value[key];
      crossSiblingMediaToMaterialize.push({
        key,
        epoch: bumpDraftMediaEpoch(targetWorkflowId, key),
        mediaGen: uploadGen[key] ?? 0,
        pending: pending?.data ? { ...pending } : undefined,
        sourceId,
      });
    }
  }

  if (
    !disposed &&
    !opts?.silent &&
    selectedWorkflowId.value === targetWorkflowId
  ) {
    setDraftStatus("saving");
  }

  const performSave = async () => {
    if (getDraftFieldEpoch(targetWorkflowId) !== fieldEpoch) {
      return;
    }
    const saved = await saveColabOneWorkflowDraft(targetWorkflowId, sanitized);
    for (const item of crossSiblingMediaToMaterialize) {
      if (getDraftMediaEpoch(targetWorkflowId, item.key) !== item.epoch) {
        continue;
      }
      try {
        let mediaToSave: {
          filename: string;
          kind: "image" | "audio";
          data: string;
        } | null = null;
        if (item.pending?.data) {
          mediaToSave = {
            filename: item.pending.filename,
            kind: item.pending.kind,
            data: item.pending.data,
          };
        } else {
          const payload = await readColabOneWorkflowDraftMedia(
            item.sourceId,
            item.key,
          );
          if (
            getDraftMediaEpoch(targetWorkflowId, item.key) !== item.epoch ||
            !payload?.data
          ) {
            continue;
          }
          mediaToSave = {
            filename: payload.filename,
            kind: payload.kind,
            data: payload.data,
          };
        }
        if (getDraftMediaEpoch(targetWorkflowId, item.key) !== item.epoch) {
          continue;
        }
        const savedMeta = await saveColabOneWorkflowDraftMedia(
          targetWorkflowId,
          item.key,
          mediaToSave,
        );
        if (
          !disposed &&
          loadGen === draftLoadGen &&
          selectedWorkflowId.value === targetWorkflowId &&
          (uploadGen[item.key] ?? 0) === item.mediaGen &&
          savedMeta
        ) {
          draftMediaMeta.value = {
            ...draftMediaMeta.value,
            [item.key]: savedMeta,
          };
          draftMediaSourceIds.value = {
            ...draftMediaSourceIds.value,
            [item.key]: targetWorkflowId,
          };
        }
      } catch {
        // Retain original source workflow ID on transient failure.
      }
    }
    if (
      disposed ||
      loadGen !== draftLoadGen ||
      saveGen !== draftSaveGen ||
      selectedWorkflowId.value !== targetWorkflowId
    ) {
      return;
    }
    if (saved) {
      setDraftStatus("saved");
    } else {
      draftStatusController.clearIfSaving();
    }
  };

  const runPromise = isBuiltinDh
    ? enqueueDigitalHumanPersist(performSave)
    : performSave();
  void runPromise.catch((err) => {
    if (
      disposed ||
      opts?.silent ||
      loadGen !== draftLoadGen ||
      saveGen !== draftSaveGen ||
      selectedWorkflowId.value !== targetWorkflowId
    ) {
      return;
    }
    draftStatusController.clearIfSaving();
    reportError(
      t("comfy.errSaveDraft", { detail: formatDraftErrorDetail(err) }),
    );
  });
}

function scheduleDraftSave(immediate: boolean) {
  if (draftRestoring || !selectedWorkflow.value || !draftWorkflowId) {
    return;
  }
  draftDirty = true;
  if (immediate) {
    flushDraftSave();
    return;
  }
  cancelDraftSaveTimer();
  draftSaveTimer = setTimeout(() => {
    draftSaveTimer = null;
    flushDraftSave();
  }, DRAFT_SAVE_DEBOUNCE_MS);
}

function onFieldValueUpdate(
  field: WorkflowField,
  val: string | number | boolean,
) {
  ++draftRestoreGen;
  if (field.key === "pipeline.voice_preset") {
    pendingAutoPlayVoicePresetId = String(val ?? "").trim();
  }
  fieldValues.value[field.key] = val;
  const wf = selectedWorkflow.value;
  const isLongCatWorkflow =
    wf?.pipeline?.video_backend === "longcat_avatar" ||
    (wf?.id === "digital-human-longcat" &&
      wf?.pipeline?.video_backend !== "longcat_avatar_native");
  const upscaleField = wf?.fields.find(
    (f) => f.key === "pipeline.upscale_mode",
  );
  if (
    field.key === "pipeline.render_quality" &&
    isLongCatWorkflow &&
    upscaleField
  ) {
    const nextQuality = typeof val === "string" ? val.trim() : "";
    const currentUpscale = String(
      fieldValues.value["pipeline.upscale_mode"] ?? "",
    ).trim();
    const allowedUpscaleOptions = upscaleField.options || [];
    const supportsOption = (opt: string) =>
      allowedUpscaleOptions.length === 0 || allowedUpscaleOptions.includes(opt);
    if (
      nextQuality === "native_720p" &&
      currentUpscale === "ai_2x" &&
      supportsOption("lanczos")
    ) {
      fieldValues.value["pipeline.upscale_mode"] = "lanczos";
    } else if (
      nextQuality === "balanced_480p_ai2x" &&
      currentUpscale === "lanczos" &&
      supportsOption("ai_2x")
    ) {
      fieldValues.value["pipeline.upscale_mode"] = "ai_2x";
    }
  }
  const isLongCatNativeWorkflow =
    wf?.pipeline?.video_backend === "longcat_avatar_native";
  if (field.key === "pipeline.quality_preset" && isLongCatNativeWorkflow) {
    const samplingField = wf?.fields.find(
      (f) => f.key === "pipeline.sampling_mode",
    );
    if (
      samplingField &&
      (!samplingField.options?.length || samplingField.options.includes("auto"))
    ) {
      fieldValues.value["pipeline.sampling_mode"] = "auto";
    }
    if (
      upscaleField &&
      (!upscaleField.options?.length || upscaleField.options.includes("auto"))
    ) {
      fieldValues.value["pipeline.upscale_mode"] = "auto";
    }
  }
  if (field.key === "pipeline.tts_engine") {
    const nextEngineId = typeof val === "string" ? val.trim() : "";
    const nextEngine = ttsEngines.value.find(
      (engine) => engine.id === nextEngineId,
    );
    reconcileVoicePresetForEngine(nextEngine);
    detachActiveVoicePreviewRequest();
  } else if (
    field.key === "pipeline.voice_prompt" ||
    field.key === "pipeline.emotion_prompt" ||
    field.key === "pipeline.voice_ref_text"
  ) {
    if (
      activeVoicePreviewTaskId.value ||
      voicePreviewLookingUp.value ||
      voicePreviewSubmitting.value ||
      voicePreviewFetchingAsset.value
    ) {
      detachActiveVoicePreviewRequest();
    } else {
      clearActiveVoicePreviewDisplay();
    }
  }
  const isDiscrete =
    field.field_type === "select" || field.field_type === "boolean";
  scheduleDraftSave(isDiscrete);
}

function onFieldBlur(_field: WorkflowField) {
  flushDraftSave();
}

function extractValidDraftMedia(
  targetWf: WorkflowTemplate,
  rawMedia: Record<string, ColabOneWorkflowDraftMediaMeta> | undefined,
): Record<string, ColabOneWorkflowDraftMediaMeta> {
  const mediaFieldMap = new Map(
    targetWf.fields
      .filter((f) => f.field_type === "image" || f.field_type === "audio")
      .map((f) => [f.key, f]),
  );
  const validMedia: Record<string, ColabOneWorkflowDraftMediaMeta> = {};
  for (const [key, meta] of Object.entries(rawMedia || {})) {
    const field = mediaFieldMap.get(key);
    if (!field) continue;
    if (
      (field.field_type === "image" && meta.kind !== "image") ||
      (field.field_type === "audio" && meta.kind !== "audio")
    ) {
      continue;
    }
    validMedia[key] = meta;
  }
  return validMedia;
}

async function hydrateDraftMediaField(
  wf: WorkflowTemplate,
  fieldKey: string,
  loadGen: number,
  options?: { keepPreview?: boolean },
) {
  const mediaGen = uploadGen[fieldKey] ?? 0;
  const sourceWfId = resolveDraftMediaWorkflowId(fieldKey, wf.id);
  let payload = null;
  try {
    payload = await readColabOneWorkflowDraftMedia(sourceWfId, fieldKey);
  } catch {
    payload = null;
  }
  if (
    disposed ||
    loadGen !== draftLoadGen ||
    selectedWorkflowId.value !== wf.id ||
    (uploadGen[fieldKey] ?? 0) !== mediaGen
  ) {
    return;
  }

  let previewUrl = "";
  if (payload && payload.data) {
    if (
      payload.kind === "image" &&
      (!options?.keepPreview || !localPreviews.value[fieldKey])
    ) {
      try {
        const blob = colabOneBase64ToBlob(payload.data, payload.mime);
        previewUrl = URL.createObjectURL(blob);
      } catch {
        payload = null;
      }
    }
  }

  if (payload && payload.data) {
    if (previewUrl) {
      revokeLocalPreview(fieldKey);
      localPreviews.value[fieldKey] = previewUrl;
    }
    if (!uploadedInputIds.value[fieldKey]) {
      pendingUploads.value[fieldKey] = {
        filename: payload.filename,
        data: payload.data,
        kind: payload.kind,
      };
    }
    return;
  }

  if (draftMediaMeta.value[fieldKey]) {
    const nextMeta = { ...draftMediaMeta.value };
    delete nextMeta[fieldKey];
    draftMediaMeta.value = nextMeta;
    delete draftMediaSourceIds.value[fieldKey];
    void deleteColabOneWorkflowDraftMedia(sourceWfId, fieldKey).catch(() => {});
    if (sourceWfId !== wf.id) {
      void deleteColabOneWorkflowDraftMedia(wf.id, fieldKey).catch(() => {});
    }
  }
  delete pendingUploads.value[fieldKey];
  if (!uploadedInputIds.value[fieldKey]) {
    revokeLocalPreview(fieldKey);
    delete uploadedNames.value[fieldKey];
    const field = wf.fields.find((f) => f.key === fieldKey);
    fieldValues.value[fieldKey] =
      field && field.default !== undefined && field.default !== null
        ? String(field.default)
        : "";
    pruneFashionPhotoRoles(wf, { persist: true });
  }
}

async function restoreWorkflowDraft(
  wf: WorkflowTemplate,
  loadGen: number,
  restoreGen: number,
) {
  let chosenDraft: ColabOneWorkflowDraft | null = null;
  let sourceWf: WorkflowTemplate = wf;
  const siblings =
    isColabOneBuiltinDigitalHumanWorkflow(wf) &&
    builtinDigitalHumanGroup.value &&
    builtinDigitalHumanGroup.value.workflows.length >= 2
      ? builtinDigitalHumanGroup.value.workflows
      : null;

  if (siblings) {
    await digitalHumanPersistChain.catch(() => {});
    if (
      disposed ||
      loadGen !== draftLoadGen ||
      restoreGen !== draftRestoreGen ||
      selectedWorkflowId.value !== wf.id
    ) {
      return;
    }
    const loaded = await Promise.all(
      siblings.map(async (sibling) => ({
        wf: sibling,
        draft: await loadColabOneWorkflowDraft(sibling.id).catch(() => null),
      })),
    );
    if (
      disposed ||
      loadGen !== draftLoadGen ||
      restoreGen !== draftRestoreGen ||
      selectedWorkflowId.value !== wf.id
    ) {
      return;
    }
    // Sort ALL non-null sibling drafts by updated_at FIRST so a newer cleared
    // draft on one engine is respected instead of resurrecting an older sibling.
    const existing = loaded
      .filter(
        (item): item is { wf: WorkflowTemplate; draft: ColabOneWorkflowDraft } =>
          Boolean(item.draft),
      )
      .sort((a, b) => {
        const diff = (b.draft.updated_at || 0) - (a.draft.updated_at || 0);
        if (diff !== 0) return diff;
        if (a.wf.id === wf.id && b.wf.id !== wf.id) return -1;
        if (b.wf.id === wf.id && a.wf.id !== wf.id) return 1;
        return 0;
      });
    if (existing.length === 0) {
      return;
    }
    const freshest = existing[0];
    const freshestValidMedia = extractValidDraftMedia(
      freshest.wf,
      freshest.draft.media,
    );
    const freshestMediaKeys = Object.keys(freshestValidMedia);
    const freshestMerged = remapColabOnePresetPromptFieldsForLocale(
      freshest.wf.fields,
      mergeColabOneWorkflowDraftFields(
        freshest.wf.fields,
        freshest.draft.fields,
        colabOneLocaleCode.value,
        freshestValidMedia,
      ),
      colabOneLocaleCode.value,
    );
    if (
      freshestMediaKeys.length === 0 &&
      !hasColabOneDraftContent(
        freshest.wf.fields,
        freshestMerged,
        freshestMediaKeys,
        colabOneLocaleCode.value,
      )
    ) {
      return;
    }
    chosenDraft = freshest.draft;
    sourceWf = freshest.wf;
  } else {
    try {
      chosenDraft = await loadColabOneWorkflowDraft(wf.id);
    } catch {
      return;
    }
    if (
      disposed ||
      loadGen !== draftLoadGen ||
      restoreGen !== draftRestoreGen ||
      selectedWorkflowId.value !== wf.id
    ) {
      return;
    }
  }

  if (
    !chosenDraft ||
    draftDirty ||
    hasUploadingField.value ||
    Object.keys(pendingUploads.value).length > 0 ||
    Object.keys(uploadedInputIds.value).length > 0 ||
    Object.keys(draftMediaMeta.value).length > 0
  ) {
    return;
  }

  const validMedia = extractValidDraftMedia(wf, chosenDraft.media);
  const nextDraftMediaSourceIds: Record<string, string> = {};
  let mergedFields: Record<string, string | number | boolean>;

  if (sourceWf.id !== wf.id) {
    const sourceValidMedia = extractValidDraftMedia(sourceWf, chosenDraft.media);
    const mergedSourceFields = remapColabOnePresetPromptFieldsForLocale(
      sourceWf.fields,
      mergeColabOneWorkflowDraftFields(
        sourceWf.fields,
        chosenDraft.fields,
        colabOneLocaleCode.value,
        sourceValidMedia,
      ),
      colabOneLocaleCode.value,
    );
    mergedFields = transferColabOneDigitalHumanFields(
      sourceWf,
      wf,
      mergedSourceFields,
      colabOneLocaleCode.value,
    );
    for (const field of wf.fields) {
      if (
        (field.field_type === "image" || field.field_type === "audio") &&
        !validMedia[field.key]
      ) {
        mergedFields[field.key] =
          field.default !== undefined && field.default !== null
            ? String(field.default)
            : "";
      }
    }
    for (const key of Object.keys(validMedia)) {
      nextDraftMediaSourceIds[key] = sourceWf.id;
    }
  } else {
    mergedFields = remapColabOnePresetPromptFieldsForLocale(
      wf.fields,
      mergeColabOneWorkflowDraftFields(
        wf.fields,
        chosenDraft.fields,
        colabOneLocaleCode.value,
        validMedia,
      ),
      colabOneLocaleCode.value,
    );
  }

  const nextUploadedNames: Record<string, string> = {};
  for (const [key, meta] of Object.entries(validMedia)) {
    nextUploadedNames[key] = meta.filename;
    mergedFields[key] = meta.filename;
  }

  draftRestoring = true;
  try {
    fieldValues.value = mergedFields;
    draftMediaMeta.value = validMedia;
    draftMediaSourceIds.value = nextDraftMediaSourceIds;
    uploadedNames.value = nextUploadedNames;
    if (selectedTtsEngine.value) {
      reconcileVoicePresetForEngine(selectedTtsEngine.value);
    }
  } finally {
    draftRestoring = false;
  }

  const mediaKeys = Object.keys(validMedia);
  if (
    hasColabOneDraftContent(
      wf.fields,
      mergedFields,
      mediaKeys,
      colabOneLocaleCode.value,
    )
  ) {
    setDraftStatus("restored");
  }

  for (const fieldKey of mediaKeys) {
    void hydrateDraftMediaField(wf, fieldKey, loadGen);
  }
}

const runButtonDisabledTooltip = computed(() => {
  if (!gpuReady.value) {
    if (isShareMode.value) {
      return t("workflowShare.guestOfflineBannerTitle");
    }
    return gpuStarting.value
      ? t("api.gpuStartingTooltip")
      : t("api.startGpuFirstTooltip");
  }
  if (isShareMode.value && props.shareBusyOther) {
    return t("workflowShare.guestBusyOtherBannerTitle");
  }
  if (hasUploadingField.value) {
    return t("comfy.runBlockedUploading");
  }
  if (comfyStarting.value || submittingRun.value) {
    return "";
  }
  if (engineActionBlocked.value) {
    return t("comfy.runBlockedBusy");
  }
  return "";
});

function resetWorkflowEditorState(
  wf: WorkflowTemplate | undefined,
  opts?: { preserveAdvanced?: boolean },
) {
  const loadGen = ++draftLoadGen;
  const restoreGen = ++draftRestoreGen;
  ++draftSaveGen;
  cancelDraftSaveTimer();
  draftDirty = false;
  setDraftStatus("idle");
  draftMediaMeta.value = {};
  draftMediaSourceIds.value = {};
  if (!opts?.preserveAdvanced) {
    advancedExpanded.value = false;
  }
  for (const k of Object.keys(uploadGen)) {
    uploadGen[k] = (uploadGen[k] ?? 0) + 1;
  }
  uploadingFields.value = {};
  uploadingNames.value = {};
  clearAllReservedFashionSlots();
  activeDraftWorkflow = wf;
  draftWorkflowId = wf?.id || "";
  fieldValues.value = wf
    ? defaultFieldValues(wf.fields, colabOneLocaleCode.value)
    : {};
  pendingUploads.value = {};
  uploadedInputIds.value = {};
  uploadedNames.value = {};
  revokeAllLocalPreviews();
  voicePreviewTextCustomEdited.value = false;
  voicePreviewText.value = voicePreviewDefaultText.value;
  detachActiveVoicePreviewRequest();
  if (wf) {
    void restoreWorkflowDraft(wf, loadGen, restoreGen);
  }
}

function selectWorkflow(id: string) {
  if (submittingRun.value || hasUploadingField.value) return;
  pendingWorkflowId.value = "";
  selectedWorkflowId.value = id;
}

function openWorkflowDrawer(id: string) {
  if (submittingRun.value || hasUploadingField.value) return;
  selectWorkflow(id);
  outputsDrawerOpen.value = false;
  paramDrawerOpen.value = true;
}

function openStudioEntryDrawer(entry: ColabOneStudioWorkflowEntry) {
  if (submittingRun.value || hasUploadingField.value) return;
  const targetId = entry.isGroup
    ? digitalHumanSelection.value.workflowId || entry.primary.id
    : entry.primary.id;
  openWorkflowDrawer(targetId);
}

function switchDigitalHumanEngine(targetWorkflowId: string) {
  if (submittingRun.value || hasUploadingField.value) return;
  const groupWfs = activeDigitalHumanGroup.value?.workflows || [];
  const nextWf = groupWfs.find((w) => w.id === targetWorkflowId);
  if (!nextWf) return;
  if (targetWorkflowId === selectedWorkflowId.value) return;

  preferredDigitalHumanEngineId.value = targetWorkflowId;
  writeColabOnePreferredDigitalHumanEngine(targetWorkflowId);

  const prevWf = selectedWorkflow.value;
  if (!prevWf) {
    pendingWorkflowId.value = "";
    selectedWorkflowId.value = nextWf.id;
    resetWorkflowEditorState(nextWf, { preserveAdvanced: true });
    return;
  }

  const currentMediaKeys = Array.from(
    new Set<string>([
      ...Object.keys(pendingUploads.value),
      ...Object.keys(uploadedInputIds.value),
      ...Object.keys(draftMediaMeta.value),
    ]),
  );
  const hasCurrentContent =
    currentMediaKeys.length > 0 ||
    hasColabOneDraftContent(
      prevWf.fields,
      fieldValues.value,
      currentMediaKeys,
      colabOneLocaleCode.value,
    );

  if (!hasCurrentContent) {
    if (draftDirty && draftWorkflowId === prevWf.id) {
      flushDraftSave(prevWf, { silent: true });
    }
    pendingWorkflowId.value = "";
    selectedWorkflowId.value = nextWf.id;
    resetWorkflowEditorState(nextWf, { preserveAdvanced: true });
    return;
  }

  if (draftDirty && draftWorkflowId === prevWf.id) {
    flushDraftSave(prevWf, { silent: true });
  }

  const nextValues = transferColabOneDigitalHumanFields(
    prevWf,
    nextWf,
    fieldValues.value,
    colabOneLocaleCode.value,
  );
  const nextMediaFieldMap = new Map(
    nextWf.fields
      .filter((f) => f.field_type === "image" || f.field_type === "audio")
      .map((f) => [f.key, f]),
  );
  const prevMediaFieldMap = new Map(
    prevWf.fields
      .filter((f) => f.field_type === "image" || f.field_type === "audio")
      .map((f) => [f.key, f]),
  );
  const isCompatibleMediaKey = (key: string, kind?: "image" | "audio") => {
    const nextField = nextMediaFieldMap.get(key);
    if (!nextField) return false;
    const resolvedKind =
      kind ??
      (prevMediaFieldMap.get(key)?.field_type === "audio"
        ? "audio"
        : prevMediaFieldMap.get(key)?.field_type === "image"
          ? "image"
          : undefined);
    if (resolvedKind && nextField.field_type !== resolvedKind) {
      return false;
    }
    return true;
  };

  const nextPendingUploads: Record<string, PendingUpload> = {};
  for (const [key, item] of Object.entries(pendingUploads.value)) {
    if (isCompatibleMediaKey(key, item.kind)) {
      nextPendingUploads[key] = item;
    }
  }

  const nextUploadedInputIds: Record<string, string> = {};
  for (const [key, assetId] of Object.entries(uploadedInputIds.value)) {
    if (isCompatibleMediaKey(key)) {
      nextUploadedInputIds[key] = assetId;
    }
  }

  const nextUploadedNames: Record<string, string> = {};
  for (const [key, name] of Object.entries(uploadedNames.value)) {
    if (isCompatibleMediaKey(key)) {
      nextUploadedNames[key] = name;
    }
  }

  const nextLocalPreviews: Record<string, string> = {};
  const droppedPreviewUrls: string[] = [];
  for (const [key, url] of Object.entries(localPreviews.value)) {
    if (isCompatibleMediaKey(key, "image")) {
      nextLocalPreviews[key] = url;
    } else if (url) {
      droppedPreviewUrls.push(url);
    }
  }
  if (droppedPreviewUrls.length > 0) {
    revokeBlobUrlsDeferred(droppedPreviewUrls);
  }

  const nextDraftMediaMeta: Record<string, ColabOneWorkflowDraftMediaMeta> = {};
  const nextDraftMediaSourceIds: Record<string, string> = {};
  for (const [key, meta] of Object.entries(draftMediaMeta.value)) {
    if (isCompatibleMediaKey(key, meta.kind)) {
      nextDraftMediaMeta[key] = meta;
      nextDraftMediaSourceIds[key] =
        draftMediaSourceIds.value[key] || prevWf.id;
    }
  }

  for (const [key, field] of nextMediaFieldMap.entries()) {
    if (
      nextUploadedInputIds[key] ||
      nextPendingUploads[key] ||
      nextDraftMediaMeta[key]
    ) {
      const carriedName =
        nextUploadedNames[key] ||
        nextPendingUploads[key]?.filename ||
        nextDraftMediaMeta[key]?.filename ||
        "";
      if (carriedName && !String(nextValues[key] ?? "").trim()) {
        nextValues[key] = carriedName;
      }
    } else {
      nextValues[key] =
        field.default !== undefined && field.default !== null
          ? String(field.default)
          : "";
    }
  }

  const loadGen = ++draftLoadGen;
  ++draftRestoreGen;
  const saveGen = ++draftSaveGen;
  cancelDraftSaveTimer();
  draftDirty = false;
  for (const k of Object.keys(uploadGen)) {
    uploadGen[k] = (uploadGen[k] ?? 0) + 1;
  }
  const mediaGens: Record<string, number> = {};
  for (const key of nextMediaFieldMap.keys()) {
    mediaGens[key] = uploadGen[key] ?? 0;
  }
  uploadingFields.value = {};
  uploadingNames.value = {};
  clearAllReservedFashionSlots();

  pendingWorkflowId.value = "";
  activeDraftWorkflow = nextWf;
  draftWorkflowId = nextWf.id;
  selectedWorkflowId.value = nextWf.id;
  pendingUploads.value = nextPendingUploads;
  uploadedInputIds.value = nextUploadedInputIds;
  uploadedNames.value = nextUploadedNames;
  localPreviews.value = nextLocalPreviews;
  draftMediaMeta.value = nextDraftMediaMeta;
  draftMediaSourceIds.value = nextDraftMediaSourceIds;

  draftRestoring = true;
  try {
    fieldValues.value = nextValues;
    const nextTtsEngine = resolveSelectedTtsEngine(
      nextWf,
      fieldValues.value,
      ttsEngines.value,
    );
    if (nextTtsEngine) {
      reconcileVoicePresetForEngine(nextTtsEngine);
    }
  } finally {
    draftRestoring = false;
  }

  if (
    activeVoicePreviewTaskId.value ||
    voicePreviewLookingUp.value ||
    voicePreviewSubmitting.value ||
    voicePreviewFetchingAsset.value
  ) {
    detachActiveVoicePreviewRequest();
  }

  const targetWfId = nextWf.id;
  const carriedMediaKeySet = new Set<string>();
  for (const key of nextMediaFieldMap.keys()) {
    if (nextPendingUploads[key]?.data || nextDraftMediaMeta[key]) {
      carriedMediaKeySet.add(key);
    }
  }
  const hasTransferredContent = hasColabOneDraftContent(
    nextWf.fields,
    fieldValues.value,
    Array.from(carriedMediaKeySet),
    colabOneLocaleCode.value,
  );
  if (!hasTransferredContent) {
    setDraftStatus("idle");
    return;
  }

  const fieldsEpoch = bumpDraftFieldEpoch(targetWfId);
  const fieldsToSave = sanitizeColabOneDraftFieldsForSave(nextWf.fields, {
    ...fieldValues.value,
  });
  const carriedMediaEntries: {
    key: string;
    epoch: number;
    pending?: PendingUpload;
    sourceId?: string;
    alreadyPersistedOnTarget: boolean;
  }[] = [];
  for (const key of nextMediaFieldMap.keys()) {
    const sourceId = nextDraftMediaSourceIds[key] || prevWf.id;
    const alreadyOnTarget =
      Boolean(nextDraftMediaMeta[key]) && sourceId === targetWfId;
    if (nextPendingUploads[key]?.data) {
      carriedMediaEntries.push({
        key,
        epoch: bumpDraftMediaEpoch(targetWfId, key),
        pending: { ...nextPendingUploads[key] },
        sourceId,
        alreadyPersistedOnTarget: alreadyOnTarget,
      });
    } else if (nextDraftMediaMeta[key]) {
      carriedMediaEntries.push({
        key,
        epoch: bumpDraftMediaEpoch(targetWfId, key),
        sourceId,
        alreadyPersistedOnTarget: alreadyOnTarget,
      });
    }
  }
  const staleMediaEntries = Array.from(nextMediaFieldMap.keys())
    .filter((key) => !carriedMediaKeySet.has(key))
    .map((key) => ({
      key,
      epoch: bumpDraftMediaEpoch(targetWfId, key),
    }));
  setDraftStatus("saving");

  // If an image media field was restored on `prevWf` and switched before its
  // thumbnail finished hydrating, re-hydrate preview cleanly on `nextWf`.
  for (const key of carriedMediaKeySet) {
    const meta = nextDraftMediaMeta[key];
    if (meta?.kind === "image" && !nextLocalPreviews[key]) {
      void hydrateDraftMediaField(nextWf, key, loadGen, { keepPreview: true });
    }
  }

  void enqueueDigitalHumanPersist(async () => {
    let savedFieldsOk = false;
    if (getDraftFieldEpoch(targetWfId) === fieldsEpoch) {
      try {
        const saved = await saveColabOneWorkflowDraft(targetWfId, fieldsToSave);
        savedFieldsOk = Boolean(saved);
      } catch {
        savedFieldsOk = false;
      }
    }

    for (const stale of staleMediaEntries) {
      if (getDraftMediaEpoch(targetWfId, stale.key) !== stale.epoch) {
        continue;
      }
      await deleteColabOneWorkflowDraftMedia(targetWfId, stale.key).catch(
        () => {},
      );
    }

    for (const item of carriedMediaEntries) {
      if (
        item.alreadyPersistedOnTarget ||
        getDraftMediaEpoch(targetWfId, item.key) !== item.epoch
      ) {
        continue;
      }
      try {
        let mediaToSave: {
          filename: string;
          kind: "image" | "audio";
          data: string;
          mime?: string;
        } | null = null;
        if (item.pending?.data) {
          mediaToSave = {
            filename: item.pending.filename,
            kind: item.pending.kind,
            data: item.pending.data,
          };
        } else if (item.sourceId && item.sourceId !== targetWfId) {
          const payload = await readColabOneWorkflowDraftMedia(
            item.sourceId,
            item.key,
          );
          if (getDraftMediaEpoch(targetWfId, item.key) !== item.epoch) {
            continue;
          }
          if (payload && payload.data) {
            mediaToSave = {
              filename: payload.filename,
              kind: payload.kind,
              data: payload.data,
              mime: payload.mime,
            };
          }
        }
        if (
          !mediaToSave ||
          getDraftMediaEpoch(targetWfId, item.key) !== item.epoch
        ) {
          continue;
        }
        const savedMeta = await saveColabOneWorkflowDraftMedia(
          targetWfId,
          item.key,
          {
            filename: mediaToSave.filename,
            kind: mediaToSave.kind,
            data: mediaToSave.data,
          },
        );
        if (
          disposed ||
          loadGen !== draftLoadGen ||
          selectedWorkflowId.value !== targetWfId ||
          (uploadGen[item.key] ?? 0) !== mediaGens[item.key]
        ) {
          continue;
        }
        if (savedMeta) {
          draftMediaMeta.value = {
            ...draftMediaMeta.value,
            [item.key]: savedMeta,
          };
          draftMediaSourceIds.value = {
            ...draftMediaSourceIds.value,
            [item.key]: targetWfId,
          };
          if (
            mediaToSave.kind === "image" &&
            !localPreviews.value[item.key]
          ) {
            try {
              const blob = colabOneBase64ToBlob(
                mediaToSave.data,
                savedMeta.mime || mediaToSave.mime || "image/png",
              );
              localPreviews.value[item.key] = URL.createObjectURL(blob);
            } catch {
              // Ignore preview decode failures.
            }
          }
          if (
            !uploadedInputIds.value[item.key] &&
            !pendingUploads.value[item.key]
          ) {
            pendingUploads.value[item.key] = {
              filename: savedMeta.filename,
              data: mediaToSave.data,
              kind: savedMeta.kind,
            };
          }
        }
      } catch {
        // Keep draftMediaSourceIds pointing to the original source workflow ID
        // so subsequent reads continue working without data loss.
      }
    }

    if (
      !disposed &&
      loadGen === draftLoadGen &&
      saveGen === draftSaveGen &&
      selectedWorkflowId.value === targetWfId
    ) {
      if (savedFieldsOk && hasTransferredContent) {
        setDraftStatus("saved");
      } else {
        draftStatusController.clearIfSaving();
      }
    }
  });
}

function onDigitalHumanEngineKeydown(
  event: KeyboardEvent,
  currentIdx: number,
) {
  if (
    submittingRun.value ||
    hasUploadingField.value ||
    event.altKey ||
    event.ctrlKey ||
    event.metaKey ||
    event.shiftKey
  ) {
    return;
  }
  const list = activeDigitalHumanGroup.value?.workflows;
  if (!list || list.length < 2) return;
  let nextIdx = -1;
  if (event.key === "ArrowRight" || event.key === "ArrowDown") {
    nextIdx = (currentIdx + 1) % list.length;
  } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
    nextIdx = (currentIdx - 1 + list.length) % list.length;
  } else if (event.key === "Home") {
    nextIdx = 0;
  } else if (event.key === "End") {
    nextIdx = list.length - 1;
  } else {
    return;
  }
  event.preventDefault();
  const nextWf = list[nextIdx];
  if (!nextWf) return;
  switchDigitalHumanEngine(nextWf.id);
  const currentTarget = event.currentTarget as HTMLElement | null;
  const groupEl = currentTarget?.closest('[role="radiogroup"]');
  void nextTick(() => {
    if (
      disposed ||
      !paramDrawerOpen.value ||
      selectedWorkflowId.value !== nextWf.id
    ) {
      return;
    }
    const nextBtn = Array.from(
      groupEl?.querySelectorAll<HTMLButtonElement>('button[role="radio"]') ?? [],
    ).find((btn) => btn.dataset.engineId === nextWf.id);
    nextBtn?.focus();
  });
}

function closeParamDrawer() {
  flushDraftSave();
  paramDrawerOpen.value = false;
}

function openOutputsDrawer() {
  flushDraftSave();
  paramDrawerOpen.value = false;
  outputsDrawerOpen.value = true;
}

function closeOutputsDrawer() {
  outputsDrawerOpen.value = false;
}

function revokeLocalPreview(key: string) {
  const url = localPreviews.value[key];
  if (url) {
    URL.revokeObjectURL(url);
    delete localPreviews.value[key];
  }
}

function revokeAllLocalPreviews() {
  Object.values(localPreviews.value).forEach((url) => URL.revokeObjectURL(url));
  localPreviews.value = {};
}

watch(
  workflows,
  (list) => {
    if (!list.length) {
      selectedWorkflowId.value = "";
      paramDrawerOpen.value = false;
      return;
    }
    if (
      pendingWorkflowId.value &&
      list.some((w) => w.id === pendingWorkflowId.value)
    ) {
      selectedWorkflowId.value = pendingWorkflowId.value;
      pendingWorkflowId.value = "";
      return;
    }
    if (!list.some((w) => w.id === selectedWorkflowId.value)) {
      if (
        builtinDigitalHumanGroup.value &&
        isColabOneBuiltinDigitalHumanWorkflow(list[0])
      ) {
        selectedWorkflowId.value =
          digitalHumanSelection.value.workflowId || list[0].id;
      } else {
        selectedWorkflowId.value = list[0].id;
      }
    }
  },
  { immediate: true },
);

watch(
  () => digitalHumanSelection.value.workflowId,
  (nextWorkflowId) => {
    if (
      nextWorkflowId &&
      !digitalHumanSelection.value.fromUserPreference &&
      !paramDrawerOpen.value &&
      activeDigitalHumanGroup.value &&
      selectedWorkflowId.value !== nextWorkflowId &&
      !submittingRun.value &&
      !hasUploadingField.value
    ) {
      selectedWorkflowId.value = nextWorkflowId;
    }
  },
);

watch(
  () => selectedWorkflow.value?.id,
  (newId, oldId) => {
    if (newId && draftWorkflowId === newId && activeDraftWorkflow?.id === newId) {
      return;
    }
    if (oldId && oldId !== newId && draftDirty && draftWorkflowId === oldId) {
      const prevWf =
        workflows.value.find((w) => w.id === oldId) ??
        (activeDraftWorkflow?.id === oldId ? activeDraftWorkflow : undefined);
      if (prevWf) {
        flushDraftSave(prevWf);
      }
    }
    resetWorkflowEditorState(selectedWorkflow.value, {
      preserveAdvanced: false,
    });
  },
  { immediate: true },
);

watch(
  () => props.state.runtime.id,
  () => {
    const wf = selectedWorkflow.value;
    const fieldMap = new Map((wf?.fields || []).map((f) => [f.key, f]));
    const remoteOnlyKeys = new Set(
      Object.keys(uploadedInputIds.value).filter(
        (key) => !pendingUploads.value[key] && !draftMediaMeta.value[key],
      ),
    );
    for (const key of remoteOnlyKeys) {
      revokeLocalPreview(key);
      delete uploadedNames.value[key];
      const field = fieldMap.get(key);
      fieldValues.value[key] =
        field && field.default !== undefined && field.default !== null
          ? String(field.default)
          : "";
    }
    for (const key of Object.keys(uploadedNames.value)) {
      if (!pendingUploads.value[key] && !draftMediaMeta.value[key]) {
        revokeLocalPreview(key);
        delete uploadedNames.value[key];
      }
    }
    uploadedInputIds.value = {};
    if (wf) {
      pruneFashionPhotoRoles(wf, { persist: false });
      const loadGen = draftLoadGen;
      for (const key of Object.keys(draftMediaMeta.value)) {
        if (!pendingUploads.value[key]) {
          void hydrateDraftMediaField(wf, key, loadGen, { keepPreview: true });
        }
      }
    }
    abortAllOutputFetches();
    failedOutputs.value = {};
    streamFallbackToBlob.value = {};
    exportedDriveOutputs.value = {};
    const staleUrls = Object.values(blobUrls.value);
    blobUrls.value = {};
    if (isShareMode.value) {
      streamUrls.value = {};
      bufferingStreams.value = {};
    }
    revokeBlobUrlsDeferred(staleUrls);
    if (detachedVoicePreviewTaskIds.size > 0) {
      detachedVoicePreviewTaskIds.clear();
      detachedVoicePreviewVersion.value += 1;
    }
    resetVoicePreviewTaskTracking();
    revokeAllVoicePreviewUrls(true);
  },
);

function fieldInputId(field: WorkflowField): string {
  return `comfy-field-${field.key.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
}

function resetFields() {
  if (!selectedWorkflow.value || submittingRun.value || hasUploadingField.value) {
    return;
  }
  const wf = selectedWorkflow.value;
  ++draftLoadGen;
  ++draftRestoreGen;
  ++draftSaveGen;
  cancelDraftSaveTimer();
  draftDirty = false;
  setDraftStatus("idle");
  draftMediaMeta.value = {};
  draftMediaSourceIds.value = {};
  for (const k of Object.keys(uploadGen)) {
    uploadGen[k] = (uploadGen[k] ?? 0) + 1;
  }
  uploadingFields.value = {};
  uploadingNames.value = {};
  clearAllReservedFashionSlots();
  fieldValues.value = defaultFieldValues(wf.fields, colabOneLocaleCode.value);
  pendingUploads.value = {};
  uploadedInputIds.value = {};
  uploadedNames.value = {};
  revokeAllLocalPreviews();
  voicePreviewTextCustomEdited.value = false;
  voicePreviewText.value = voicePreviewDefaultText.value;
  detachActiveVoicePreviewRequest();
  const captureWorkflowClearEpochs = (targetWf: WorkflowTemplate) => {
    const fieldEpoch = bumpDraftFieldEpoch(targetWf.id);
    const mediaEpochs: { key: string; epoch: number }[] = [];
    for (const field of targetWf.fields) {
      if (field.field_type === "image" || field.field_type === "audio") {
        mediaEpochs.push({
          key: field.key,
          epoch: bumpDraftMediaEpoch(targetWf.id, field.key),
        });
      }
    }
    return { id: targetWf.id, fieldEpoch, mediaEpochs };
  };
  const isClearTargetFresh = (target: {
    id: string;
    fieldEpoch: number;
    mediaEpochs: { key: string; epoch: number }[];
  }) =>
    getDraftFieldEpoch(target.id) === target.fieldEpoch &&
    target.mediaEpochs.every(
      ({ key, epoch }) => getDraftMediaEpoch(target.id, key) === epoch,
    );
  const primaryClearTarget = captureWorkflowClearEpochs(wf);
  const siblingClearTargets: {
    id: string;
    fieldEpoch: number;
    mediaEpochs: { key: string; epoch: number }[];
  }[] = [];
  if (activeDigitalHumanGroup.value) {
    for (const sibling of activeDigitalHumanGroup.value.workflows) {
      if (sibling.id !== wf.id) {
        siblingClearTargets.push(captureWorkflowClearEpochs(sibling));
      }
    }
  }
  const clearTask = async () => {
    for (const siblingTarget of siblingClearTargets) {
      if (!isClearTargetFresh(siblingTarget)) {
        continue;
      }
      await clearColabOneWorkflowDraft(siblingTarget.id).catch(() => {});
    }
    if (!isClearTargetFresh(primaryClearTarget)) {
      return;
    }
    await clearColabOneWorkflowDraft(primaryClearTarget.id);
  };
  const clearPromise = isColabOneBuiltinDigitalHumanWorkflow(wf)
    ? enqueueDigitalHumanPersist(clearTask)
    : clearTask();
  void clearPromise.catch((err) => {
    if (!disposed && selectedWorkflowId.value === wf.id) {
      reportError(
        t("comfy.errSaveDraft", { detail: formatDraftErrorDetail(err) }),
      );
    }
  });
  setNoticeKey("comfy.noticeResetDefaults");
}

const MAX_IMAGE_INPUT_BYTES = 16 * 1024 ** 2;
const MAX_AUDIO_INPUT_BYTES = 64 * 1024 ** 2;
const IMAGE_EXT_RE = /[^/\\]\.(png|jpe?g|webp)$/i;
const AUDIO_EXT_RE = /\.(wav|mp3|flac|ogg|m4a)$/i;

interface ColabOneMediaUploadResult {
  ok: boolean;
  uploaded?: boolean;
  staged?: boolean;
  error?: string;
}

function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = String(reader.result || "");
      const idx = result.indexOf(",");
      resolve(idx >= 0 ? result.slice(idx + 1) : result);
    };
    reader.onerror = () => reject(new Error(t("comfy.errReadLocalFile")));
    reader.readAsDataURL(file);
  });
}

async function handleMediaFile(
  field: WorkflowField,
  file: File,
  opts?: { silentNotice?: boolean },
): Promise<ColabOneMediaUploadResult> {
  if (submittingRun.value || uploadingFields.value[field.key]) {
    return { ok: false };
  }
  const kind: "image" | "audio" =
    field.field_type === "audio" ? "audio" : "image";
  const isImageOk = IMAGE_EXT_RE.test(file.name);
  const isAudioOk = AUDIO_EXT_RE.test(file.name);
  if ((kind === "image" && !isImageOk) || (kind === "audio" && !isAudioOk)) {
    const errMsg = t(
      kind === "audio"
        ? "comfy.errUnsupportedAudioFormat"
        : "comfy.errUnsupportedImageFormat",
    );
    if (!opts?.silentNotice) {
      reportError(errMsg);
    }
    return { ok: false, error: errMsg };
  }
  if (file.size <= 0) {
    const errMsg = t("comfy.errEmptyFile");
    if (!opts?.silentNotice) {
      reportError(errMsg);
    }
    return { ok: false, error: errMsg };
  }
  const maxBytes =
    kind === "audio" ? MAX_AUDIO_INPUT_BYTES : MAX_IMAGE_INPUT_BYTES;
  if (file.size > maxBytes) {
    const errMsg = t(
      kind === "audio" ? "comfy.errAudioTooLarge" : "comfy.errImageTooLarge",
    );
    if (!opts?.silentNotice) {
      reportError(errMsg);
    }
    return { ok: false, error: errMsg };
  }

  const gen = (uploadGen[field.key] = (uploadGen[field.key] ?? 0) + 1);
  ++draftRestoreGen;
  const loadGen = draftLoadGen;
  const targetWorkflowId = selectedWorkflowId.value;
  const targetWf = selectedWorkflow.value;
  uploadingFields.value = { ...uploadingFields.value, [field.key]: true };
  uploadingNames.value = { ...uploadingNames.value, [field.key]: file.name };
  if (
    field.key === "pipeline.voice_ref_audio" ||
    field.key === "pipeline.emotion_ref_audio"
  ) {
    detachActiveVoicePreviewRequest();
  }
  let nextPreviewUrl = "";
  try {
    const data = await readFileAsBase64(file);
    if (
      disposed ||
      loadGen !== draftLoadGen ||
      uploadGen[field.key] !== gen ||
      selectedWorkflowId.value !== targetWorkflowId
    ) {
      return { ok: false };
    }
    if (kind === "image" && isImageOk) {
      nextPreviewUrl = URL.createObjectURL(file);
    }

    // Immediately persist media into local workflow draft (both online & offline)
    const isBuiltinDh = targetWf
      ? isColabOneBuiltinDigitalHumanWorkflow(targetWf)
      : false;
    let savedDraftMeta: ColabOneWorkflowDraftMediaMeta | null = null;
    let draftSaveErr: unknown = null;
    if (targetWorkflowId) {
      const mediaEpoch = bumpDraftMediaEpoch(targetWorkflowId, field.key);
      const saveMediaTask = async () => {
        if (getDraftMediaEpoch(targetWorkflowId, field.key) !== mediaEpoch) {
          return null;
        }
        return await saveColabOneWorkflowDraftMedia(
          targetWorkflowId,
          field.key,
          { filename: file.name, kind, data },
        );
      };
      try {
        savedDraftMeta = isBuiltinDh
          ? await enqueueDigitalHumanPersist(saveMediaTask)
          : await saveMediaTask();
      } catch (draftErr) {
        draftSaveErr = draftErr;
      }
    }
    if (
      disposed ||
      loadGen !== draftLoadGen ||
      uploadGen[field.key] !== gen ||
      selectedWorkflowId.value !== targetWorkflowId
    ) {
      if (nextPreviewUrl) URL.revokeObjectURL(nextPreviewUrl);
      return { ok: false };
    }

    const effectiveFilename = savedDraftMeta?.filename || file.name;
    const draftErrText = draftSaveErr
      ? t("comfy.errSaveDraft", {
          detail: formatDraftErrorDetail(draftSaveErr),
        })
      : undefined;
    if (savedDraftMeta) {
      draftMediaMeta.value = {
        ...draftMediaMeta.value,
        [field.key]: savedDraftMeta,
      };
      delete draftMediaSourceIds.value[field.key];
      revokeLocalPreview(field.key);
      if (nextPreviewUrl) {
        localPreviews.value[field.key] = nextPreviewUrl;
        nextPreviewUrl = "";
      }
      pendingUploads.value[field.key] = {
        filename: effectiveFilename,
        data,
        kind,
      };
      delete uploadedInputIds.value[field.key];
      fieldValues.value[field.key] = effectiveFilename;
      uploadedNames.value[field.key] = effectiveFilename;
      setDraftStatus("saved");
      draftDirty = true;
      flushDraftSave(targetWf);
    }

    let shouldStageOffline = !gpuReady.value;
    if (gpuReady.value) {
      const uploadRuntimeId = props.state.runtime.id;
      const res = await colabOneCommand("comfyui.input.upload", {
        filename: effectiveFilename,
        data,
      });
      if (
        disposed ||
        loadGen !== draftLoadGen ||
        uploadGen[field.key] !== gen ||
        selectedWorkflowId.value !== targetWorkflowId
      ) {
        if (nextPreviewUrl) URL.revokeObjectURL(nextPreviewUrl);
        return { ok: false };
      }
      if (props.state.runtime.id !== uploadRuntimeId) {
        if (!savedDraftMeta) {
          if (!gpuReady.value) {
            // Runtime went offline mid-upload (e.g. Guest Share Mode with no local
            // draft media): stage into pendingUploads with its local preview instead
            // of discarding the photo silently.
            shouldStageOffline = true;
          } else {
            if (nextPreviewUrl) URL.revokeObjectURL(nextPreviewUrl);
            const interruptedErr = t("comfy.errUploadNamedFailed", {
              name: effectiveFilename,
            });
            if (!opts?.silentNotice) {
              reportError(interruptedErr);
            }
            return { ok: false, error: interruptedErr };
          }
        } else {
          // Local draft media already staged pendingUploads & preview cleanly.
          if (nextPreviewUrl) URL.revokeObjectURL(nextPreviewUrl);
          shouldStageOffline = true;
        }
      } else {
        const assetId = typeof res?.id === "string" ? res.id : "";
        if (!assetId) {
          throw new Error(t("comfy.errUploadNoId"));
        }
        if (!savedDraftMeta && draftMediaMeta.value[field.key]) {
          const nextMeta = { ...draftMediaMeta.value };
          delete nextMeta[field.key];
          draftMediaMeta.value = nextMeta;
          delete draftMediaSourceIds.value[field.key];
          if (targetWorkflowId) {
            const delEpoch = bumpDraftMediaEpoch(targetWorkflowId, field.key);
            const delTask = async () => {
              if (getDraftMediaEpoch(targetWorkflowId, field.key) !== delEpoch) {
                return;
              }
              await deleteColabOneWorkflowDraftMedia(
                targetWorkflowId,
                field.key,
              );
            };
            void (isBuiltinDh
              ? enqueueDigitalHumanPersist(delTask)
              : delTask()
            ).catch(() => {});
          }
        }
        if (nextPreviewUrl) {
          revokeLocalPreview(field.key);
          localPreviews.value[field.key] = nextPreviewUrl;
          nextPreviewUrl = "";
        }
        uploadedInputIds.value[field.key] = assetId;
        const nextFieldVal = res?.filename || effectiveFilename;
        const fieldChanged = fieldValues.value[field.key] !== nextFieldVal;
        fieldValues.value[field.key] = nextFieldVal;
        uploadedNames.value[field.key] = effectiveFilename;
        delete pendingUploads.value[field.key];
        if (fieldChanged || !savedDraftMeta) {
          draftDirty = true;
          flushDraftSave(targetWf);
        }
        if (!opts?.silentNotice) {
          if (draftErrText) {
            emit(
              "notify",
              t("comfy.noticeUploadedRemote", { name: effectiveFilename }),
              "success",
            );
            reportError(draftErrText);
          } else {
            setNoticeKey("comfy.noticeUploadedRemote", {
              name: effectiveFilename,
            });
          }
        }
        return { ok: true, uploaded: true, error: draftErrText };
      }
    }

    if (shouldStageOffline) {
      if (!savedDraftMeta) {
        if (draftMediaMeta.value[field.key]) {
          const nextMeta = { ...draftMediaMeta.value };
          delete nextMeta[field.key];
          draftMediaMeta.value = nextMeta;
          delete draftMediaSourceIds.value[field.key];
          if (targetWorkflowId) {
            const delEpoch = bumpDraftMediaEpoch(targetWorkflowId, field.key);
            const delTask = async () => {
              if (getDraftMediaEpoch(targetWorkflowId, field.key) !== delEpoch) {
                return;
              }
              await deleteColabOneWorkflowDraftMedia(
                targetWorkflowId,
                field.key,
              );
            };
            void (isBuiltinDh
              ? enqueueDigitalHumanPersist(delTask)
              : delTask()
            ).catch(() => {});
          }
        }
        revokeLocalPreview(field.key);
        if (nextPreviewUrl) {
          localPreviews.value[field.key] = nextPreviewUrl;
          nextPreviewUrl = "";
        }
        pendingUploads.value[field.key] = {
          filename: effectiveFilename,
          data,
          kind,
        };
        delete uploadedInputIds.value[field.key];
        fieldValues.value[field.key] = effectiveFilename;
        uploadedNames.value[field.key] = effectiveFilename;
        draftDirty = true;
        flushDraftSave(targetWf);
      }
      if (!opts?.silentNotice) {
        if (draftErrText) {
          emit(
            "notify",
            t("comfy.noticeStagedUpload", { name: effectiveFilename }),
            "info",
          );
          reportError(draftErrText);
        } else {
          setNoticeKey("comfy.noticeStagedUpload", { name: effectiveFilename });
        }
      }
      return { ok: true, staged: true, error: draftErrText };
    }
    return { ok: false };
  } catch (e) {
    if (nextPreviewUrl) URL.revokeObjectURL(nextPreviewUrl);
    const errMsg = formatDraftErrorDetail(e) || String(e);
    if (!disposed && uploadGen[field.key] === gen) {
      if (!opts?.silentNotice) {
        reportError(e);
      }
    }
    return { ok: false, error: errMsg };
  } finally {
    if (uploadGen[field.key] === gen) {
      const next = { ...uploadingFields.value };
      delete next[field.key];
      uploadingFields.value = next;
      const nextNames = { ...uploadingNames.value };
      delete nextNames[field.key];
      uploadingNames.value = nextNames;
    }
  }
}

function clearMediaField(field: WorkflowField) {
  if (submittingRun.value || uploadingFields.value[field.key]) {
    return;
  }
  ++draftRestoreGen;
  uploadGen[field.key] = (uploadGen[field.key] ?? 0) + 1;
  if (
    field.key === "pipeline.voice_ref_audio" ||
    field.key === "pipeline.emotion_ref_audio"
  ) {
    detachActiveVoicePreviewRequest();
  }
  delete pendingUploads.value[field.key];
  delete uploadedInputIds.value[field.key];
  delete uploadedNames.value[field.key];
  revokeLocalPreview(field.key);
  const prevSourceWfId = draftMediaSourceIds.value[field.key];
  delete draftMediaSourceIds.value[field.key];
  if (draftMediaMeta.value[field.key]) {
    const nextMeta = { ...draftMediaMeta.value };
    delete nextMeta[field.key];
    draftMediaMeta.value = nextMeta;
  }
  fieldValues.value[field.key] =
    field.default !== undefined && field.default !== null
      ? String(field.default)
      : "";
  const wf = selectedWorkflow.value;
  const wfId = wf?.id || draftWorkflowId;
  if (wfId) {
    const primaryEpoch = bumpDraftMediaEpoch(wfId, field.key);
    const loadGen = draftLoadGen;
    const extraDeleteIds = new Set<string>();
    if (prevSourceWfId && prevSourceWfId !== wfId) {
      extraDeleteIds.add(prevSourceWfId);
    }
    if (activeDigitalHumanGroup.value) {
      for (const sibling of activeDigitalHumanGroup.value.workflows) {
        if (sibling.id !== wfId) {
          extraDeleteIds.add(sibling.id);
        }
      }
    }
    const extraDeleteEntries = Array.from(extraDeleteIds).map((extraId) => ({
      id: extraId,
      epoch: bumpDraftMediaEpoch(extraId, field.key),
    }));
    if (wf && isColabOneBuiltinDigitalHumanWorkflow(wf)) {
      void enqueueDigitalHumanPersist(async () => {
        for (const { id: extraId, epoch } of extraDeleteEntries) {
          if (getDraftMediaEpoch(extraId, field.key) !== epoch) {
            continue;
          }
          await deleteColabOneWorkflowDraftMedia(extraId, field.key).catch(
            () => {},
          );
        }
        if (getDraftMediaEpoch(wfId, field.key) !== primaryEpoch) {
          return;
        }
        await deleteColabOneWorkflowDraftMedia(wfId, field.key);
      }).catch((err) => {
        if (
          !disposed &&
          loadGen === draftLoadGen &&
          selectedWorkflowId.value === wfId
        ) {
          reportError(
            t("comfy.errSaveDraft", { detail: formatDraftErrorDetail(err) }),
          );
        }
      });
    } else {
      void (async () => {
        if (getDraftMediaEpoch(wfId, field.key) !== primaryEpoch) {
          return;
        }
        await deleteColabOneWorkflowDraftMedia(wfId, field.key);
      })().catch((err) => {
        if (
          !disposed &&
          loadGen === draftLoadGen &&
          selectedWorkflowId.value === wfId
        ) {
          reportError(
            t("comfy.errSaveDraft", { detail: formatDraftErrorDetail(err) }),
          );
        }
      });
      for (const { id: extraId, epoch } of extraDeleteEntries) {
        void (async () => {
          if (getDraftMediaEpoch(extraId, field.key) !== epoch) {
            return;
          }
          await deleteColabOneWorkflowDraftMedia(extraId, field.key);
        })().catch(() => {});
      }
    }
    draftDirty = true;
    flushDraftSave(wf);
  }
}

// Import modal
const MAX_IMPORT_JSON_BYTES = 2 * 1024 * 1024;
const importOpen = ref(false);
const importName = ref("");
const importDescription = ref("");
const importRaw = ref("");
const importing = ref(false);
const importDragging = ref(false);
const importFileInput = ref<HTMLInputElement | null>(null);

const importValidation = computed(() =>
  importRaw.value.trim() ? parseWorkflowImportJSON(importRaw.value) : null,
);
const importNodeCount = computed(() =>
  importValidation.value && importValidation.value.ok
    ? Object.keys(importValidation.value.workflow).length
    : 0,
);
const canSubmitImport = computed(
  () =>
    !importing.value &&
    !!importName.value.trim() &&
    !!importValidation.value?.ok,
);

function openImportModal() {
  if (isShareMode.value) return;
  flushDraftSave();
  paramDrawerOpen.value = false;
  outputsDrawerOpen.value = false;
  importOpen.value = true;
}

function closeImportModal() {
  if (importing.value) return;
  importOpen.value = false;
  importDragging.value = false;
}

function onImportDragLeave(event: DragEvent) {
  const current = event.currentTarget as HTMLElement | null;
  const related = event.relatedTarget as Node | null;
  if (current && related && current.contains(related)) {
    return;
  }
  importDragging.value = false;
}

async function readImportFile(file: File) {
  if (file.size > MAX_IMPORT_JSON_BYTES) {
    reportError(t("comfy.errImportTooLarge"));
    return;
  }
  try {
    const text = await file.text();
    importRaw.value = text;
    if (!importName.value.trim()) {
      importName.value = file.name.replace(/\.json$/i, "");
    }
  } catch (e) {
    reportError(e);
  }
}

async function onImportFileChange(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file) return;
  await readImportFile(file);
}

async function onImportFileDrop(event: DragEvent) {
  importDragging.value = false;
  if (importing.value) return;
  const file = event.dataTransfer?.files?.[0];
  if (!file) return;
  await readImportFile(file);
}

async function submitImport() {
  if (isShareMode.value) return;
  if (!canSubmitImport.value || !importValidation.value?.ok) return;
  importing.value = true;
  try {
    const res = await colabOneCommand("comfyui.workflow.import", {
      name: importName.value.trim(),
      description: importDescription.value.trim(),
      workflow: importValidation.value.workflow,
    });
    const createdId = res?.id || res?.workflow?.id;
    if (typeof createdId === "string" && createdId) {
      pendingWorkflowId.value = createdId;
      selectedWorkflowId.value = createdId;
    }
    importOpen.value = false;
    importName.value = "";
    importDescription.value = "";
    importRaw.value = "";
    outputsDrawerOpen.value = false;
    paramDrawerOpen.value = true;
    emit("sync");
    setNoticeKey("comfy.noticeImported");
  } catch (e) {
    reportError(e);
  } finally {
    importing.value = false;
  }
}

async function deleteWorkflow(target: WorkflowTemplate) {
  if (isShareMode.value) return;
  busy.value = true;
  try {
    await colabOneCommand("comfyui.workflow.delete", { id: target.id });
    if (draftWorkflowId === target.id) {
      ++draftLoadGen;
      ++draftRestoreGen;
      ++draftSaveGen;
      cancelDraftSaveTimer();
      draftDirty = false;
      setDraftStatus("idle");
      draftMediaMeta.value = {};
      draftMediaSourceIds.value = {};
    }
    bumpDraftFieldEpoch(target.id);
    for (const field of target.fields || []) {
      if (field.field_type === "image" || field.field_type === "audio") {
        bumpDraftMediaEpoch(target.id, field.key);
      }
    }
    void clearColabOneWorkflowDraft(target.id).catch(() => {});
    if (selectedWorkflowId.value === target.id) {
      paramDrawerOpen.value = false;
    }
    emit("sync");
    setNoticeKey("comfy.noticeDeletedWorkflow", { name: tb(target.name) });
  } catch (e) {
    reportError(e);
  } finally {
    busy.value = false;
  }
}

// Engine lifecycle actions
async function actEngine(
  action: string,
  payload: Record<string, unknown> = {},
  successNoticeKey?: Parameters<typeof t>[0],
) {
  busy.value = true;
  try {
    await colabOneCommand(action, payload);
    emit("sync");
    if (successNoticeKey) setNoticeKey(successNoticeKey);
    return true;
  } catch (e) {
    reportError(e);
    return false;
  } finally {
    busy.value = false;
  }
}

async function startComfy() {
  if (!gpuReady.value) return;
  await actEngine("comfyui.start", {}, "comfy.noticeStartingEngine");
}

async function freeComfyVram() {
  if (isShareMode.value) return;
  await actEngine("comfyui.free", {}, "comfy.noticeFreeVram");
}

async function stopComfy() {
  if (isShareMode.value) return;
  await actEngine("comfyui.stop", {}, "comfy.noticeStoppingEngine");
}

const cancellingTaskId = ref<string | null>(null);

async function cancelActiveTask() {
  const currentTask = comfyTask.value;
  if (!currentTask || cancellingTaskId.value !== null) return;
  cancellingTaskId.value = currentTask.id;
  try {
    await colabOneCommand("task.cancel", { id: currentTask.id });
    emit("sync");
    setNoticeKey("comfy.noticeCancelRequested");
  } catch (e) {
    reportError(e);
  } finally {
    if (cancellingTaskId.value === currentTask.id) {
      cancellingTaskId.value = null;
    }
  }
}

async function runWorkflow() {
  if (
    submittingRun.value ||
    hasUploadingField.value ||
    engineActionBlocked.value ||
    (isShareMode.value && props.shareBusyOther)
  ) {
    return;
  }
  if (!gpuReady.value || comfyStarting.value) return;
  if (workflowNeedsComfyServer.value && !comfyRunning.value) {
    await startComfy();
    return;
  }
  if (!selectedWorkflow.value) {
    return;
  }
  mobileAutoReleasePopoverOpen.value = false;
  cancelVoicePreviewPrefetch();

  const wf = selectedWorkflow.value;
  const missing = missingRequiredWorkflowFields(
    wf,
    fieldValues.value,
    uploadedInputIds.value,
    { ...draftMediaMeta.value, ...pendingUploads.value },
  );
  if (missing.length > 0) {
    const firstMissing = missing[0];
    const rawMissingLabel = tb(firstMissing.label);
    const missingLabel =
      rawMissingLabel
        .replace(
          /\s*(?:[（(]\s*(?:可选|可選|選填|任意|선택|optional)\s*[）)])\s*$/i,
          "",
        )
        .trim() || rawMissingLabel;
    reportError(t("comfy.requiredFieldHint", { label: missingLabel }));
    const isInAdvanced = fieldPartitions.value.advanced.some(
      (f) => f.key === firstMissing.key,
    );
    if (isInAdvanced && !advancedExpanded.value) {
      advancedExpanded.value = true;
    }
    await nextTick();
    if (!disposed) {
      const targetEl = document.getElementById(fieldInputId(firstMissing));
      if (targetEl) {
        const prefersReducedMotion =
          typeof window !== "undefined" &&
          typeof window.matchMedia === "function" &&
          window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        (targetEl.closest(".co-comfy-field") || targetEl).scrollIntoView?.({
          behavior: prefersReducedMotion ? "auto" : "smooth",
          block: "center",
        });
        targetEl.focus();
      }
    }
    return;
  }

  if (isColabOneFashionCustomPosesEmpty(wf, fieldValues.value)) {
    reportError(
      tb("自定义姿态模式下至少需要勾选 1 个姿势（或在高级设置中填写自定义姿势）"),
    );
    if (!advancedExpanded.value) {
      advancedExpanded.value = true;
    }
    await nextTick();
    if (!disposed) {
      const poseField = wf.fields.find((f) => f.key === "pipeline.pose_1_enabled");
      const targetEl = poseField
        ? document.getElementById(fieldInputId(poseField))
        : null;
      if (targetEl) {
        const prefersReducedMotion =
          typeof window !== "undefined" &&
          typeof window.matchMedia === "function" &&
          window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        (targetEl.closest(".co-comfy-field") || targetEl).scrollIntoView?.({
          behavior: prefersReducedMotion ? "auto" : "smooth",
          block: "center",
        });
        targetEl.focus();
      }
    }
    return;
  }

  const targetRuntimeId = props.state.runtime.id;
  const targetWorkflowId = wf.id;
  flushDraftSave(wf);
  submittingRun.value = true;
  try {
    const visibleFieldKeySet = new Set(
      visibleFields.value.map((field) => field.key),
    );
    const activeFields = wf.fields.filter(
      (field) =>
        visibleFieldKeySet.has(field.key) ||
        field.key === "pipeline.audio_source",
    );
    const activeFieldKeys = new Set(activeFields.map((field) => field.key));

    // Ensure any active draft-backed media field is hydrated before uploading
    for (const key of Object.keys(draftMediaMeta.value)) {
      if (
        !activeFieldKeys.has(key) ||
        uploadedInputIds.value[key] ||
        pendingUploads.value[key]
      ) {
        continue;
      }
      const sourceWfId = resolveDraftMediaWorkflowId(key, targetWorkflowId);
      let payload = null;
      let draftReadErr: unknown = null;
      try {
        payload = await readColabOneWorkflowDraftMedia(sourceWfId, key);
      } catch (readErr) {
        draftReadErr = readErr;
        payload = null;
      }
      if (
        disposed ||
        props.state.runtime.id !== targetRuntimeId ||
        selectedWorkflowId.value !== targetWorkflowId
      ) {
        return;
      }
      if (payload && payload.data) {
        pendingUploads.value[key] = {
          filename: payload.filename,
          data: payload.data,
          kind: payload.kind,
        };
      } else {
        const nextMeta = { ...draftMediaMeta.value };
        delete nextMeta[key];
        draftMediaMeta.value = nextMeta;
        delete draftMediaSourceIds.value[key];
        void deleteColabOneWorkflowDraftMedia(sourceWfId, key).catch(() => {});
        if (sourceWfId !== targetWorkflowId) {
          void deleteColabOneWorkflowDraftMedia(targetWorkflowId, key).catch(
            () => {},
          );
        }
        revokeLocalPreview(key);
        delete uploadedNames.value[key];
        const field = wf.fields.find((f) => f.key === key);
        fieldValues.value[key] =
          field && field.default !== undefined && field.default !== null
            ? String(field.default)
            : "";
        pruneFashionPhotoRoles(wf, { persist: true });
        throw new Error(
          draftReadErr
            ? formatDraftErrorDetail(draftReadErr)
            : t("comfy.errReadLocalFile"),
        );
      }
    }

    // Upload only queued media files for currently active/visible fields
    for (const [key, item] of Object.entries(pendingUploads.value)) {
      if (!activeFieldKeys.has(key)) {
        continue;
      }
      const res = await colabOneCommand("comfyui.input.upload", {
        filename: item.filename,
        data: item.data,
      });
      if (
        disposed ||
        props.state.runtime.id !== targetRuntimeId ||
        selectedWorkflowId.value !== targetWorkflowId
      ) {
        return;
      }
      const assetId = typeof res?.id === "string" ? res.id : "";
      if (!assetId) {
        throw new Error(t("comfy.errUploadNamedFailed", { name: item.filename }));
      }
      uploadedInputIds.value[key] = assetId;
      fieldValues.value[key] = res?.filename || item.filename;
      uploadedNames.value[key] = item.filename;
      delete pendingUploads.value[key];
    }

    const { overrides, inputs } = buildColabOneWorkflowOverrides(
      wf,
      fieldValues.value,
      activeFields,
      { uploadedInputIds: uploadedInputIds.value },
    );

    await colabOneCommand("comfyui.run", {
      workflow_id: wf.id,
      overrides,
      inputs,
      ...(!isShareMode.value && workflowAutoReleaseEnabled.value
        ? { auto_release_after_sync: true }
        : {}),
    });
    if (disposed || props.state.runtime.id !== targetRuntimeId) {
      return;
    }
    paramDrawerOpen.value = false;
    emit("sync");
    setNoticeKey("comfy.noticeRunSubmitted", { name: tb(wf.name) });
  } catch (e) {
    if (!disposed) {
      reportError(e);
    }
  } finally {
    submittingRun.value = false;
  }
}

// Output gallery & lazy Blob URL preview
const MAX_INLINE_PREVIEW_BYTES = 48 * 1024 * 1024;
const GOOGLE_DRIVE_HOME_URL = "https://drive.google.com/drive/my-drive";
interface OutputPreviewError {
  code: "media" | "fetch";
  detail?: string;
}
const outputs = computed(() => props.state.comfy_outputs || []);
const groupedOutputs = computed<ColabOneOutputGroup[]>(() =>
  groupColabOneOutputs(outputs.value),
);
const resolvedWebConsoleState = computed<ColabOneWebConsoleState>(
  () => props.webConsoleState || createColabOneDefaultWebConsoleState(),
);
const resolvedOutputSharesState = computed<ColabOneOutputShareListState>(
  () => props.outputSharesState || createColabOneDefaultOutputShareListState(),
);
const activeOutputShareCount = computed(
  () => resolvedOutputSharesState.value.active_count || 0,
);
const blobUrls = ref<Record<string, string>>({});
const streamUrls = ref<Record<string, string>>({});
const streamFallbackToBlob = ref<Record<string, boolean>>({});
const bufferingStreams = ref<Record<string, boolean>>({});
const inlineAutoplayIds = ref<Record<string, boolean>>({});
const fetchingOutputs = ref<Record<string, boolean>>({});
const failedOutputs = ref<Record<string, OutputPreviewError>>({});
const exportingOutputs = ref<Record<string, boolean>>({});
const exportingAlbumZips = ref<Record<string, boolean>>({});
const exportingDriveOutputs = ref<Record<string, boolean>>({});
const exportingAlbumDrives = ref<Record<string, boolean>>({});
const exportedDriveOutputs = ref<Record<string, ColabOneDriveExportResult>>({});
const deletingOutputs = ref<Record<string, boolean>>({});
const deletingAlbums = ref<Record<string, boolean>>({});
const deleteConfirmOutputId = ref("");
const outputFetchControllers = new Map<string, AbortController>();
const pendingPrefetchQueue: string[] = [];
const pendingPrefetchSet = new Set<string>();

// Output album lightbox, multi-select, and artifact share modal state
const MAX_CONCURRENT_IMAGE_FETCHES = 4;
const galleryModalOpen = ref(false);
const galleryModalGroupId = ref("");
const galleryModalTaskId = ref("");
const galleryModalAnchorItemId = ref("");
const galleryModalInitialItemId = ref("");
const galleryModalInitialMode = ref<"stage" | "grid">("stage");

const visibleOutputGroupIds = ref<Set<string>>(new Set());
const outputCardElements = new Map<string, HTMLElement>();
let outputCardObserver: IntersectionObserver | null = null;
let outputCardObserverRoot: Element | null = null;

function disconnectOutputCardObserver() {
  if (outputCardObserver) {
    outputCardObserver.disconnect();
    outputCardObserver = null;
  }
  outputCardObserverRoot = null;
  visibleOutputGroupIds.value = new Set();
}

function ensureOutputCardObserver(hintEl?: HTMLElement) {
  if (
    typeof IntersectionObserver !== "function" ||
    !outputsDrawerOpen.value
  ) {
    return;
  }
  const drawerRoot =
    hintEl?.closest(".ant-drawer-body") ??
    (typeof document !== "undefined"
      ? document.querySelector(".co-comfy-outputs-drawer-root .ant-drawer-body")
      : null);
  if (!drawerRoot) {
    return;
  }
  if (outputCardObserver && outputCardObserverRoot === drawerRoot) {
    return;
  }
  if (outputCardObserver) {
    outputCardObserver.disconnect();
    outputCardObserver = null;
    outputCardObserverRoot = null;
  }
  outputCardObserverRoot = drawerRoot;
  outputCardObserver = new IntersectionObserver(
    (entries) => {
      if (disposed || !outputsDrawerOpen.value) return;
      const next = new Set(visibleOutputGroupIds.value);
      let becameVisible = false;
      for (const entry of entries) {
        const el = entry.target as HTMLElement;
        const gid = el.dataset.outputGroupId || "";
        if (!gid) continue;
        if (entry.isIntersecting) {
          if (!next.has(gid)) {
            next.add(gid);
            becameVisible = true;
          }
        } else {
          next.delete(gid);
        }
      }
      visibleOutputGroupIds.value = next;
      if (becameVisible) {
        triggerRecentImagePreviews(outputs.value);
      }
    },
    { root: drawerRoot, rootMargin: "240px 0px" },
  );
  for (const el of outputCardElements.values()) {
    outputCardObserver.observe(el);
  }
}

function registerOutputCardEl(groupId: string, el: unknown) {
  const prev = outputCardElements.get(groupId);
  if (el instanceof HTMLElement) {
    const elDrawerRoot = el.closest(".ant-drawer-body");
    if (
      prev === el &&
      outputCardObserver &&
      (!elDrawerRoot || elDrawerRoot === outputCardObserverRoot)
    ) {
      return;
    }
    if (prev && prev !== el) {
      outputCardObserver?.unobserve(prev);
    }
    outputCardElements.set(groupId, el);
    if (outputsDrawerOpen.value) {
      ensureOutputCardObserver(el);
      outputCardObserver?.observe(el);
    }
    return;
  }
  if (prev) {
    outputCardObserver?.unobserve(prev);
    outputCardElements.delete(groupId);
  }
  if (visibleOutputGroupIds.value.has(groupId)) {
    const next = new Set(visibleOutputGroupIds.value);
    next.delete(groupId);
    visibleOutputGroupIds.value = next;
  }
}

const outputMultiSelectMode = ref(false);
const selectedOutputGroupIds = ref<string[]>([]);

const outputShareModalOpen = ref(false);
const outputShareModalInitialTab = ref<"create" | "manage">("create");
const outputShareModalInitialGroupIds = ref<string[]>([]);

const activeGalleryGroup = computed<ColabOneOutputGroup | null>(() => {
  if (!galleryModalOpen.value) return null;
  if (galleryModalGroupId.value) {
    const exact = groupedOutputs.value.find(
      (g) => g.id === galleryModalGroupId.value,
    );
    if (exact) return exact;
  }
  if (galleryModalTaskId.value) {
    const byTask = groupedOutputs.value.find(
      (g) => g.taskId && g.taskId === galleryModalTaskId.value,
    );
    if (byTask) return byTask;
  }
  if (galleryModalAnchorItemId.value) {
    const byItem = groupedOutputs.value.find((g) =>
      g.items.some((i) => i.id === galleryModalAnchorItemId.value),
    );
    if (byItem) return byItem;
  }
  return null;
});

const activeGalleryItems = computed<ColabOneGalleryModalItem[]>(() =>
  (activeGalleryGroup.value?.items || []).map((item) => ({
    id: item.id,
    filename: item.filename,
    kind: item.kind,
    size: item.size,
    created_at: item.created_at,
    workflow_name: item.workflow_name,
    is_inspect: isColabOneInspectOutput(item),
    available: canLoadOutputAsset(item),
    elapsed: item.elapsed,
  })),
);

const activeGalleryTitle = computed(() => {
  const g = activeGalleryGroup.value;
  if (!g) return "";
  return g.workflowName ? tb(g.workflowName) : g.title || t("comfy.defaultWorkflowName");
});

const selectedOutputGroupIdSet = computed(
  () => new Set(selectedOutputGroupIds.value),
);

const multiSelectSummaryCounts = computed(() => {
  const selected = groupedOutputs.value.filter((g) =>
    selectedOutputGroupIdSet.value.has(g.id),
  );
  const items = selected.reduce((sum, g) => sum + g.items.length, 0);
  return { groups: selected.length, items };
});

function formatGroupCUBadge(group: ColabOneOutputGroup): string {
  const summary = computeColabOneGroupCU(group.items);
  if (!summary.hasAny) return "";
  const formatted = formatOutputCU(summary.total);
  if (!formatted) return "";
  if (summary.total > 0 && summary.total < 0.01) return formatted;
  return summary.anyEstimated
    ? t("comfy.outputCuEstimated", { cu: formatted })
    : formatted;
}

function formatGroupCUTooltip(group: ColabOneOutputGroup): string {
  const summary = computeColabOneGroupCU(group.items);
  if (!summary.sample) return "";
  return formatOutputCUTooltip({
    ...summary.sample,
    cu_consumed: summary.total,
    cu_estimated: summary.anyEstimated,
    elapsed: group.totalElapsed,
  });
}

function isGroupFullyExportedToDrive(group: ColabOneOutputGroup): boolean {
  return group.items.length > 0 && group.items.every((item) => isOutputExportedToDrive(item));
}

function isGroupAnyDriveBacked(group: ColabOneOutputGroup): boolean {
  return group.items.some((item) => isOutputDriveBacked(item));
}

function isGroupAllDriveBacked(group: ColabOneOutputGroup): boolean {
  return (
    group.items.length > 0 &&
    group.items.every((item) => isOutputDriveBacked(item))
  );
}

function getGroupDriveOpenUrl(group: ColabOneOutputGroup): string {
  const withLink = group.items.find((item) => item.drive_web_view_link);
  return withLink?.drive_web_view_link || GOOGLE_DRIVE_HOME_URL;
}

function getGroupDrivePath(group: ColabOneOutputGroup): string {
  for (const item of group.items) {
    const p = getOutputDrivePath(item);
    if (p) return p;
  }
  return "";
}

function isGroupShared(group: ColabOneOutputGroup): boolean {
  return group.items.some((item) => Boolean(item.share_id));
}

function getGroupShareTooltip(group: ColabOneOutputGroup): string {
  const sharedItem = group.items.find((item) => Boolean(item.share_id));
  if (!sharedItem) return "";
  return sharedItem.share_label
    ? t("comfy.outputTagSharedTooltip", { label: sharedItem.share_label })
    : t("comfy.outputTagSharedTooltipAnonymous");
}

function canLoadAnyInGroup(group: ColabOneOutputGroup): boolean {
  return group.items.some((item) => canLoadOutputAsset(item));
}

function getDeletableGroupItems(group: ColabOneOutputGroup): WorkflowOutput[] {
  return group.items.filter(
    (item) => gpuReady.value || (!isShareMode.value && isOutputDriveBacked(item)),
  );
}

function canDeleteGroup(group: ColabOneOutputGroup): boolean {
  return getDeletableGroupItems(group).length > 0;
}

function isItemParentGroupBusy(itemId: string): boolean {
  const keys = new Set<string>();
  const group = groupedOutputs.value.find((g) =>
    g.items.some((it) => it.id === itemId),
  );
  if (group) {
    keys.add(group.id);
    if (group.taskId) {
      keys.add(`album:${group.taskId}`);
    }
  }
  const rawItem = outputs.value.find((o) => o.id === itemId);
  if (rawItem?.task_id) {
    keys.add(`album:${rawItem.task_id}`);
  }
  for (const key of keys) {
    if (
      exportingAlbumDrives.value[key] ||
      exportingAlbumZips.value[key] ||
      deletingAlbums.value[key]
    ) {
      return true;
    }
  }
  return false;
}

function isAnyGroupItemBusy(group: ColabOneOutputGroup): boolean {
  return group.items.some(
    (it) =>
      Boolean(deletingOutputs.value[it.id]) ||
      Boolean(exportingOutputs.value[it.id]) ||
      Boolean(exportingDriveOutputs.value[it.id]),
  );
}

function clearInlineAutoplay(id?: string) {
  if (id) {
    if (inlineAutoplayIds.value[id]) {
      const next = { ...inlineAutoplayIds.value };
      delete next[id];
      inlineAutoplayIds.value = next;
    }
    return;
  }
  if (Object.keys(inlineAutoplayIds.value).length > 0) {
    inlineAutoplayIds.value = {};
  }
}

function requestInlinePreview(item: WorkflowOutput) {
  if (
    !galleryModalOpen.value &&
    (item.kind === "video" || item.kind === "audio")
  ) {
    inlineAutoplayIds.value = {
      ...inlineAutoplayIds.value,
      [item.id]: true,
    };
  }
  void loadOutputPreview(item);
}

function openOutputGallery(
  group: ColabOneOutputGroup,
  initialItemId = "",
  initialMode: "stage" | "grid" = "stage",
) {
  if (!group.items.length) return;
  clearInlineAutoplay();
  pauseOutputsDrawerMedia();
  const targetId = initialItemId || group.items[0]!.id;
  galleryModalGroupId.value = group.id;
  galleryModalTaskId.value = group.taskId || "";
  galleryModalAnchorItemId.value = targetId;
  galleryModalInitialItemId.value = targetId;
  galleryModalInitialMode.value = initialMode;
  galleryModalOpen.value = true;
  // Load active stage item immediately and queue neighbors within 4-way cap
  const targetIdx = Math.max(
    0,
    group.items.findIndex((i) => i.id === targetId),
  );
  const activeCandidate = group.items[targetIdx];
  if (
    initialMode === "stage" &&
    activeCandidate &&
    canLoadOutputAsset(activeCandidate) &&
    !getOutputPreviewSrc(activeCandidate)
  ) {
    void loadOutputPreview(activeCandidate);
  }
  const neighborCandidates = [
    ...(initialMode === "grid" ? [activeCandidate] : []),
    group.items[targetIdx + 1],
    group.items[targetIdx - 1],
    group.items[targetIdx + 2],
  ].filter((it): it is WorkflowOutput => Boolean(it));
  for (const cand of neighborCandidates) {
    enqueueOutputPreviewPrefetch(cand);
  }
}

function closeOutputGallery() {
  const closingItems = activeGalleryGroup.value?.items;
  if (closingItems && closingItems.length) {
    for (const item of closingItems) {
      removePendingPrefetchId(item.id);
    }
  }
  galleryModalOpen.value = false;
  galleryModalGroupId.value = "";
  galleryModalTaskId.value = "";
  galleryModalAnchorItemId.value = "";
  galleryModalInitialItemId.value = "";
}

function handleGalleryActiveChange(itemId: string) {
  galleryModalAnchorItemId.value = itemId;
}

function handleGalleryRequestLoad(
  modalItem: ColabOneGalleryModalItem,
  priority?: "active" | "prefetch",
) {
  const real = outputs.value.find((o) => o.id === modalItem.id);
  if (!real) return;
  if (
    priority === "active" ||
    modalItem.id === galleryModalAnchorItemId.value
  ) {
    removePendingPrefetchId(real.id);
    void loadOutputPreview(real);
  } else {
    enqueueOutputPreviewPrefetch(real);
  }
}

function handleGalleryRetryLoad(modalItem: ColabOneGalleryModalItem) {
  const real = outputs.value.find((o) => o.id === modalItem.id);
  if (!real) return;
  delete failedOutputs.value[real.id];
  void loadOutputPreview(real);
}

function handleGalleryMediaError(modalItem: ColabOneGalleryModalItem) {
  const real = outputs.value.find((o) => o.id === modalItem.id);
  if (real) {
    onOutputMediaError(real);
  }
}

function handleGalleryDownloadItem(modalItem: ColabOneGalleryModalItem) {
  const real = outputs.value.find((o) => o.id === modalItem.id);
  if (real) {
    void exportOutput(real);
  }
}

function handleGalleryDeleteItem(modalItem: ColabOneGalleryModalItem) {
  const real = outputs.value.find((o) => o.id === modalItem.id);
  if (real) {
    void deleteOutput(real);
  }
}

function resolveGalleryModalMediaSrc(modalItem: ColabOneGalleryModalItem): string {
  return streamUrls.value[modalItem.id] || blobUrls.value[modalItem.id] || "";
}

function isGalleryModalItemLoading(modalItem: ColabOneGalleryModalItem): boolean {
  return Boolean(fetchingOutputs.value[modalItem.id]);
}

function isGalleryModalItemAvailable(modalItem: ColabOneGalleryModalItem): boolean {
  const real = outputs.value.find((o) => o.id === modalItem.id);
  return real ? canLoadOutputAsset(real) : Boolean(modalItem.available);
}

function getGalleryModalItemError(modalItem: ColabOneGalleryModalItem): string {
  if (failedOutputs.value[modalItem.id]) {
    return (
      formatOutputPreviewError(failedOutputs.value[modalItem.id]) ||
      t("outputGallery.loadFailedRetry")
    );
  }
  const real = outputs.value.find((o) => o.id === modalItem.id);
  if (real && isOutputTooLargeForPreview(real)) {
    return t(
      isShareMode.value
        ? "comfy.sharePreviewTooLarge"
        : "comfy.videoPreviewTooLarge",
      { size: formatComfyByteSize(real.size) },
    );
  }
  return "";
}

function canDeleteGalleryModalItem(modalItem: ColabOneGalleryModalItem): boolean {
  const real = outputs.value.find((o) => o.id === modalItem.id);
  if (
    !real ||
    isItemParentGroupBusy(real.id) ||
    exportingOutputs.value[real.id] ||
    exportingDriveOutputs.value[real.id] ||
    deletingOutputs.value[real.id]
  ) {
    return false;
  }
  return Boolean(
    gpuReady.value || (!isShareMode.value && isOutputDriveBacked(real)),
  );
}

function toggleOutputMultiSelect() {
  if (isShareMode.value) return;
  outputMultiSelectMode.value = !outputMultiSelectMode.value;
  if (!outputMultiSelectMode.value) {
    selectedOutputGroupIds.value = [];
  }
}

function toggleOutputGroupSelection(groupId: string) {
  if (selectedOutputGroupIdSet.value.has(groupId)) {
    selectedOutputGroupIds.value = selectedOutputGroupIds.value.filter(
      (id) => id !== groupId,
    );
  } else {
    selectedOutputGroupIds.value = [...selectedOutputGroupIds.value, groupId];
  }
}

function selectAllOutputGroups() {
  selectedOutputGroupIds.value = groupedOutputs.value.map((g) => g.id);
}

function clearOutputGroupSelection() {
  selectedOutputGroupIds.value = [];
}

function openOutputShareForGroups(groupIds: string[]) {
  if (isShareMode.value) return;
  emit("clearOutputShareCreated");
  emit("refreshOutputShares");
  outputShareModalInitialTab.value = "create";
  outputShareModalInitialGroupIds.value = [...groupIds];
  outputShareModalOpen.value = true;
}

function openOutputShareManage() {
  if (isShareMode.value) return;
  emit("clearOutputShareCreated");
  emit("refreshOutputShares");
  outputShareModalInitialTab.value = "manage";
  outputShareModalInitialGroupIds.value = [];
  outputShareModalOpen.value = true;
}

function openMultiSelectShare() {
  if (!selectedOutputGroupIds.value.length) return;
  const ids = [...selectedOutputGroupIds.value];
  outputMultiSelectMode.value = false;
  selectedOutputGroupIds.value = [];
  openOutputShareForGroups(ids);
}

function closeOutputShareModal() {
  outputShareModalOpen.value = false;
  emit("clearOutputShareCreated");
}

function handleShareModalCopyFallback(
  text: string,
  successToast?: string,
): Promise<boolean> {
  if (props.copyText) {
    return props.copyText(text, successToast);
  }
  return (async () => {
    try {
      await colabOneWriteClipboard(text);
      if (isColabOneDesktopRuntime()) {
        await navigator.clipboard?.writeText(text).catch(() => {});
      }
      emit("notify", successToast || t("outputShare.noticeCopiedLink"), "success");
      return true;
    } catch {
      emit("error", t("common.copyFailed"));
      return false;
    }
  })();
}

async function exportAlbumZip(group: ColabOneOutputGroup | null) {
  if (
    !group ||
    exportingAlbumZips.value[group.id] ||
    deletingAlbums.value[group.id] ||
    isAnyGroupItemBusy(group)
  ) {
    return;
  }
  const loadableIds = group.items
    .filter((item) => canLoadOutputAsset(item))
    .map((item) => item.id);
  if (!loadableIds.length) return;
  exportingAlbumZips.value = { ...exportingAlbumZips.value, [group.id]: true };
  try {
    const label = group.workflowName
      ? tb(group.workflowName)
      : group.title || "colabone-album";
    const saved = await colabOneExportWorkflowOutputsZip(loadableIds, label);
    if (saved) {
      setNoticeKey("outputGallery.noticeAlbumExported");
    }
  } catch (e) {
    reportError(e);
  } finally {
    const next = { ...exportingAlbumZips.value };
    delete next[group.id];
    exportingAlbumZips.value = next;
  }
}

async function saveAlbumToHostDrive(group: ColabOneOutputGroup) {
  if (
    !isShareMode.value ||
    !props.shareDriveExportReady ||
    !gpuReady.value ||
    exportingAlbumDrives.value[group.id] ||
    deletingAlbums.value[group.id] ||
    isAnyGroupItemBusy(group)
  ) {
    return;
  }
  const pending = group.items.filter((item) => !isOutputExportedToDrive(item));
  if (!pending.length) return;
  exportingAlbumDrives.value = { ...exportingAlbumDrives.value, [group.id]: true };
  const targetRuntimeId = props.state.runtime.id;
  let savedAny = false;
  try {
    for (const item of pending) {
      if (disposed || props.state.runtime.id !== targetRuntimeId) return;
      const res = await colabOneCommand("comfyui.output.export_drive", {
        id: item.id,
      });
      if (disposed || props.state.runtime.id !== targetRuntimeId) return;
      const alreadyExists = Boolean(res?.already_exists);
      const filename =
        typeof res?.filename === "string" && res.filename.trim()
          ? res.filename.trim()
          : item.filename;
      exportedDriveOutputs.value = {
        ...exportedDriveOutputs.value,
        [item.id]: {
          id: item.id,
          filename,
          remoteFolder: "",
          drivePath: "",
          alreadyExists,
          size: typeof res?.size === "number" ? res.size : item.size,
        },
      };
      savedAny = true;
    }
    if (savedAny) {
      setNoticeKey("comfy.noticeSavedToHostDrive");
    }
  } catch (e) {
    if (!disposed) {
      reportError(e);
    }
  } finally {
    if (!disposed && savedAny) {
      emit("sync");
    }
    const next = { ...exportingAlbumDrives.value };
    delete next[group.id];
    exportingAlbumDrives.value = next;
  }
}

function isDriveModalMatchingGroup(group: ColabOneOutputGroup): boolean {
  const targetId = driveModalTargetGroupId.value;
  if (!targetId) return false;
  if (targetId === group.id) return true;
  if (group.taskId && targetId === `album:${group.taskId}`) return true;
  return group.items.some((it) => targetId === `item:${it.id}`);
}

async function exportAlbumToDrive(
  group: ColabOneOutputGroup,
  opts: { fromModal?: boolean } = {},
) {
  if (isShareMode.value) {
    await saveAlbumToHostDrive(group);
    return;
  }
  if (
    !gpuReady.value ||
    exportingAlbumDrives.value[group.id] ||
    deletingAlbums.value[group.id] ||
    isAnyGroupItemBusy(group)
  ) {
    return;
  }
  const pending = group.items.filter((item) => !isOutputExportedToDrive(item));
  if (!pending.length) {
    if (
      opts.fromModal &&
      driveModalOpen.value &&
      isDriveModalMatchingGroup(group)
    ) {
      const refItem = group.items[0];
      if (refItem) {
        const existing = exportedDriveOutputs.value[refItem.id];
        const remoteFolder =
          existing?.remoteFolder ||
          `${activeDriveFolderName.value}/outputs`;
        const filename = existing?.filename || refItem.filename;
        const drivePath =
          existing?.drivePath ||
          getGroupDrivePath(group) ||
          `My Drive/${remoteFolder}/${filename}`;
        driveModalResult.value = {
          id: refItem.id,
          filename,
          remoteFolder,
          drivePath,
          alreadyExists: true,
          size: existing?.size ?? group.totalSize,
        };
      }
    }
    return;
  }
  if (!props.state.setup.drive || needsUpgradeForDriveExport.value) {
    openDriveSetupModal(
      pending[0]!,
      !props.state.setup.drive,
      "export",
      false,
      group.id,
    );
    return;
  }
  exportingAlbumDrives.value = { ...exportingAlbumDrives.value, [group.id]: true };
  if (opts.fromModal) {
    driveModalError.value = "";
  }
  const targetRuntimeId = props.state.runtime.id;
  let lastResult: ColabOneDriveExportResult | null = null;
  let anyNewlyExported = false;
  try {
    for (const item of pending) {
      if (disposed || props.state.runtime.id !== targetRuntimeId) return;
      const res = await colabOneCommand("comfyui.output.export_drive", {
        id: item.id,
      });
      if (disposed || props.state.runtime.id !== targetRuntimeId) return;
      const remoteFolder =
        typeof res?.remote_folder === "string" && res.remote_folder.trim()
          ? res.remote_folder.trim()
          : `${activeDriveFolderName.value}/outputs`;
      const filename =
        typeof res?.filename === "string" && res.filename.trim()
          ? res.filename.trim()
          : item.filename;
      const drivePath =
        typeof res?.drive_path === "string" && res.drive_path.trim()
          ? res.drive_path.trim()
          : `My Drive/${remoteFolder}/${filename}`;
      const itemAlreadyExists = Boolean(res?.already_exists);
      if (!itemAlreadyExists) {
        anyNewlyExported = true;
      }
      lastResult = {
        id: item.id,
        filename,
        remoteFolder,
        drivePath,
        alreadyExists: itemAlreadyExists,
        size: typeof res?.size === "number" ? res.size : item.size,
      };
      exportedDriveOutputs.value = {
        ...exportedDriveOutputs.value,
        [item.id]: lastResult,
      };
    }
    if (
      opts.fromModal &&
      driveModalOpen.value &&
      isDriveModalMatchingGroup(group) &&
      lastResult
    ) {
      driveModalResult.value = {
        ...lastResult,
        alreadyExists: !anyNewlyExported,
      };
    }
    emit("sync");
    setNoticeKey("outputGallery.noticeAlbumDriveExported");
  } catch (e) {
    if (!disposed) {
      emit("sync");
      if (
        opts.fromModal &&
        driveModalOpen.value &&
        isDriveModalMatchingGroup(group)
      ) {
        driveModalError.value = String(e);
      } else {
        reportError(e);
      }
    }
  } finally {
    const next = { ...exportingAlbumDrives.value };
    delete next[group.id];
    exportingAlbumDrives.value = next;
  }
}

async function deleteOutputGroup(group: ColabOneOutputGroup) {
  if (
    !canDeleteGroup(group) ||
    deletingAlbums.value[group.id] ||
    exportingAlbumZips.value[group.id] ||
    exportingAlbumDrives.value[group.id] ||
    isAnyGroupItemBusy(group)
  ) {
    return;
  }
  deletingAlbums.value = { ...deletingAlbums.value, [group.id]: true };
  const eligible = getDeletableGroupItems(group);
  let deletedCount = 0;
  let manualCloudCleanupCount = 0;
  let manualCloudCleanupMountCount = 0;
  let firstManualCloudCleanupReason = "";
  try {
    for (const target of eligible) {
      if (disposed) break;
      abortOutputFetch(target.id);
      const res = await colabOneCommand("comfyui.output.delete", { id: target.id });
      if (res?.manual_cloud_cleanup) {
        manualCloudCleanupCount += 1;
        const reason =
          typeof res.warning_reason === "string" ? res.warning_reason : "";
        if (!firstManualCloudCleanupReason && reason) {
          firstManualCloudCleanupReason = reason;
        }
        if (reason === "colab_drivefs") {
          manualCloudCleanupMountCount += 1;
        }
      }
      const url = blobUrls.value[target.id];
      if (url) {
        delete blobUrls.value[target.id];
        revokeBlobUrlsDeferred([url]);
      }
      delete streamUrls.value[target.id];
      delete streamFallbackToBlob.value[target.id];
      delete bufferingStreams.value[target.id];
      delete failedOutputs.value[target.id];
      if (exportedDriveOutputs.value[target.id]) {
        const nextExported = { ...exportedDriveOutputs.value };
        delete nextExported[target.id];
        exportedDriveOutputs.value = nextExported;
      }
      deletedCount += 1;
    }
    if (!disposed && deletedCount > 0) {
      if (deletedCount === group.items.length) {
        if (galleryModalGroupId.value === group.id) {
          closeOutputGallery();
        }
        if (manualCloudCleanupCount > 0) {
          emit("clearError");
          emit(
            "notify",
            formatDeleteOutputManualCloudWarning(
              firstManualCloudCleanupReason,
              group.items.length > 1,
              manualCloudCleanupCount,
              manualCloudCleanupMountCount,
            ),
            "warning",
          );
        } else {
          setNoticeKey("outputGallery.noticeAlbumDeleted");
        }
      } else if (manualCloudCleanupCount > 0) {
        emit("clearError");
        emit(
          "notify",
          formatDeleteOutputManualCloudWarning(
            firstManualCloudCleanupReason,
            manualCloudCleanupCount > 1,
            manualCloudCleanupCount,
            manualCloudCleanupMountCount,
          ),
          "warning",
        );
      } else {
        setNoticeKey("comfy.noticeDeletedOutput");
      }
    }
  } catch (e) {
    if (!disposed) {
      reportError(e);
    }
  } finally {
    if (!disposed && deletedCount > 0) {
      emit("sync");
    }
    const next = { ...deletingAlbums.value };
    delete next[group.id];
    deletingAlbums.value = next;
  }
}

// Google Drive setup & cloud-to-cloud export modal state
const driveModalOpen = ref(false);
const driveModalPurpose = ref<"config" | "export" | "auto_release">("config");
const driveModalTarget = ref<WorkflowOutput | null>(null);
const driveModalTargetGroupId = ref("");
const driveModalAutoExport = ref(false);
const driveModalAutoArmRelease = ref(false);
const driveModalResult = ref<ColabOneDriveExportResult | null>(null);
const driveModalError = ref("");
const connectingDriveInModal = ref(false);
const sawDriveConnectOpInModal = ref(false);
const driveConnectBaselineError = ref("");
const confirmingDriveAuthInModal = ref(false);
const checkingHeadlessInModal = ref(false);
const sawHeadlessOpInModal = ref(false);
const headlessBaselineError = ref("");
const savingAutoDriveInModal = ref(false);
const workflowAutoReleaseBusy = ref(false);
const dismissedAutoReleaseErrorAt = ref<number | null>(null);
const syncingDriveOutputs = ref(false);
let driveConnectReconcileTimer: ReturnType<typeof setTimeout> | null = null;
let headlessReconcileTimer: ReturnType<typeof setTimeout> | null = null;

function clearDriveConnectTimer() {
  if (driveConnectReconcileTimer !== null) {
    clearTimeout(driveConnectReconcileTimer);
    driveConnectReconcileTimer = null;
  }
}

function clearHeadlessTimer() {
  if (headlessReconcileTimer !== null) {
    clearTimeout(headlessReconcileTimer);
    headlessReconcileTimer = null;
  }
}

function clearDriveModalTimers() {
  clearDriveConnectTimer();
  clearHeadlessTimer();
}

const workflowAutoRelease = computed(
  () => props.state.runtime.comfyui?.auto_release || null,
);
const workflowAutoReleaseEnabled = computed(() =>
  Boolean(gpuReady.value && workflowAutoRelease.value?.enabled),
);
const workflowAutoReleaseState = computed(() =>
  gpuReady.value ? String(workflowAutoRelease.value?.state || "idle") : "idle",
);
const workflowAutoReleaseLocked = computed(
  () =>
    workflowAutoReleaseState.value === "releasing" ||
    workflowAutoReleaseState.value === "released",
);
const workflowAutoReleaseDriveDisconnected = computed(
  () => workflowAutoReleaseEnabled.value && !props.state.setup.drive,
);
const needsUpgradeForWorkflowAutoRelease = computed(() => {
  const caps = props.state.runtime.capabilities;
  return Boolean(
    gpuReady.value &&
      props.state.runtime.upgrade_available &&
      Array.isArray(caps) &&
      caps.length > 0 &&
      !caps.includes("comfyui_workflow_auto_release"),
  );
});
const driveOutputsCanDirectRead = computed(() =>
  Boolean(
    props.state.drive_outputs_status?.can_direct_read ??
      props.state.setup.drive_headless,
  ),
);
const isDriveOutputsSyncing = computed(
  () =>
    syncingDriveOutputs.value ||
    Boolean(props.state.drive_outputs_status?.syncing),
);

function isOutputDriveBacked(item: WorkflowOutput): boolean {
  return item.storage === "drive";
}

function isOutputMountModeDriveOnly(item: WorkflowOutput): boolean {
  return item.drive_mode === "colab_drivefs" && !item.drive_file_id;
}

function formatDeleteOutputManualCloudWarning(
  reason: string,
  isAlbum: boolean,
  count = 1,
  mountCount = 0,
): string {
  if (isAlbum) {
    if (mountCount > 0 && mountCount === count) {
      return t("outputGallery.noticeAlbumDeletedManualCloudMount", { count });
    }
    return t("outputGallery.noticeAlbumDeletedManualCloudGeneric", { count });
  }
  if (reason === "colab_drivefs") {
    return t("comfy.noticeDeletedOutputManualCloudMount");
  }
  if (reason === "drive_api_unavailable") {
    return t("comfy.noticeDeletedOutputManualCloudNoApi");
  }
  return t("comfy.noticeDeletedOutputManualCloudGeneric");
}

function canLoadOutputAsset(item: WorkflowOutput): boolean {
  if (isShareMode.value) {
    return Boolean(gpuReady.value && !isOutputDriveBacked(item));
  }
  return Boolean(
    (gpuReady.value && !isOutputDriveBacked(item)) || item.drive_previewable,
  );
}

function isOutputExportedToDrive(item: WorkflowOutput): boolean {
  return Boolean(item.drive_exported || exportedDriveOutputs.value[item.id]);
}

function getOutputDrivePath(item: WorkflowOutput): string {
  return exportedDriveOutputs.value[item.id]?.drivePath || item.drive_path || "";
}

function getOutputDriveOpenUrl(item: WorkflowOutput): string {
  return item.drive_web_view_link || GOOGLE_DRIVE_HOME_URL;
}

const driveWaitingBrowserAuth = computed(
  () =>
    gpuReady.value &&
    !props.state.setup.drive &&
    !props.state.operation?.cancelling &&
    Boolean(props.state.auth_url) &&
    isColabOneDriveConnectOperation(props.state.operation),
);
const driveMountingInProgress = computed(() => {
  if (!gpuReady.value || props.state.setup.drive || driveWaitingBrowserAuth.value) {
    return false;
  }
  const stage = getColabOneDriveConnectStage(props.state.operation);
  return stage === "mounting" || stage === "initializing";
});
const needsUpgradeForDriveExport = computed(() => {
  const caps = props.state.runtime.capabilities;
  return Boolean(
    gpuReady.value &&
      props.state.runtime.upgrade_available &&
      Array.isArray(caps) &&
      caps.length > 0 &&
      !caps.includes("comfyui_drive_export"),
  );
});
const driveModalNeedsUpgrade = computed(() =>
  driveModalPurpose.value === "auto_release"
    ? needsUpgradeForWorkflowAutoRelease.value
    : needsUpgradeForDriveExport.value,
);
const driveModalExporting = computed(() => {
  if (driveModalTargetGroupId.value) {
    if (exportingAlbumDrives.value[driveModalTargetGroupId.value]) {
      return true;
    }
    const liveGroup = groupedOutputs.value.find(
      (g) => g.id === driveModalTargetGroupId.value,
    );
    if (
      liveGroup?.taskId &&
      exportingAlbumDrives.value[`album:${liveGroup.taskId}`]
    ) {
      return true;
    }
    if (
      driveModalTarget.value?.task_id &&
      exportingAlbumDrives.value[`album:${driveModalTarget.value.task_id}`]
    ) {
      return true;
    }
  }
  return Boolean(
    driveModalTarget.value &&
      exportingDriveOutputs.value[driveModalTarget.value.id],
  );
});

const autoReleaseSwitchTooltip = computed(() => {
  if (!gpuReady.value) {
    return gpuStarting.value
      ? t("api.gpuStartingTooltip")
      : t("comfy.autoReleaseNeedGpuTooltip");
  }
  if (workflowAutoReleaseLocked.value) {
    return t("comfy.autoReleaseReleasingTooltip");
  }
  if (!props.state.setup.drive) {
    return t("comfy.autoReleaseNeedDriveTooltip");
  }
  if (workflowAutoReleaseEnabled.value) {
    return t("comfy.autoReleaseArmedTooltip");
  }
  return t("comfy.autoReleaseReadyTooltip");
});

watch(
  [
    paramDrawerOpen,
    selectedWorkflowId,
    () => Boolean(selectedWorkflow.value),
    driveModalOpen,
    viewportWidth,
  ],
  ([drawerOpen, wfId, hasWorkflow, driveOpen, width], [, prevWfId]) => {
    if (
      !drawerOpen ||
      !hasWorkflow ||
      driveOpen ||
      width > 680 ||
      wfId !== prevWfId
    ) {
      mobileAutoReleasePopoverOpen.value = false;
    }
  },
);

function getOutputPreviewSrc(item: WorkflowOutput): string {
  return streamUrls.value[item.id] || blobUrls.value[item.id] || "";
}

function canStreamOutputMedia(item: WorkflowOutput): boolean {
  return (
    (item.kind === "image" || item.kind === "video" || item.kind === "audio") &&
    canLoadOutputAsset(item)
  );
}

function isOutputTooLargeForPreview(item: WorkflowOutput): boolean {
  if (canStreamOutputMedia(item)) {
    return false;
  }
  return Number.isFinite(item.size) && item.size > MAX_INLINE_PREVIEW_BYTES;
}

function formatOutputPreviewError(
  err: OutputPreviewError | undefined,
): string {
  if (!err) return "";
  const fallbackKey = isShareMode.value
    ? "comfy.shareMediaPreviewError"
    : "comfy.mediaPreviewError";
  if (err.code === "media") {
    return t(fallbackKey);
  }
  return err.detail ? tb(err.detail) : t(fallbackKey);
}

function revokeBlobUrlsDeferred(urls: string[]) {
  if (!urls.length) return;
  void nextTick(() => {
    urls.forEach((url) => URL.revokeObjectURL(url));
  });
}

function abortOutputFetch(id: string) {
  const ctrl = outputFetchControllers.get(id);
  if (ctrl) {
    ctrl.abort();
    outputFetchControllers.delete(id);
  }
  delete fetchingOutputs.value[id];
}

function clearPendingPrefetchQueue() {
  pendingPrefetchQueue.length = 0;
  pendingPrefetchSet.clear();
}

function removePendingPrefetchId(id: string) {
  if (!pendingPrefetchSet.has(id)) return;
  pendingPrefetchSet.delete(id);
  const idx = pendingPrefetchQueue.indexOf(id);
  if (idx >= 0) {
    pendingPrefetchQueue.splice(idx, 1);
  }
}

function countInFlightOutputFetches(): number {
  let count = 0;
  for (const active of Object.values(fetchingOutputs.value)) {
    if (active) count += 1;
  }
  return count;
}

function enqueueOutputPreviewPrefetch(item: WorkflowOutput | undefined | null) {
  if (
    !item ||
    disposed ||
    !outputsDrawerOpen.value ||
    isOutputTooLargeForPreview(item) ||
    getOutputPreviewSrc(item) ||
    fetchingOutputs.value[item.id] ||
    failedOutputs.value[item.id] ||
    !canLoadOutputAsset(item)
  ) {
    return;
  }
  if (countInFlightOutputFetches() < MAX_CONCURRENT_IMAGE_FETCHES) {
    removePendingPrefetchId(item.id);
    void loadOutputPreview(item);
    return;
  }
  if (!pendingPrefetchSet.has(item.id)) {
    pendingPrefetchSet.add(item.id);
    pendingPrefetchQueue.push(item.id);
  }
}

function drainOutputPreviewPrefetchQueue() {
  if (disposed || !outputsDrawerOpen.value) return;
  while (
    pendingPrefetchQueue.length > 0 &&
    countInFlightOutputFetches() < MAX_CONCURRENT_IMAGE_FETCHES
  ) {
    const id = pendingPrefetchQueue.shift()!;
    pendingPrefetchSet.delete(id);
    const real = outputs.value.find((o) => o.id === id);
    if (
      !real ||
      isOutputTooLargeForPreview(real) ||
      getOutputPreviewSrc(real) ||
      fetchingOutputs.value[real.id] ||
      failedOutputs.value[real.id] ||
      !canLoadOutputAsset(real)
    ) {
      continue;
    }
    void loadOutputPreview(real);
  }
}

function abortAllOutputFetches() {
  clearPendingPrefetchQueue();
  for (const ctrl of outputFetchControllers.values()) {
    ctrl.abort();
  }
  outputFetchControllers.clear();
  fetchingOutputs.value = {};
}

function pauseParamDrawerMedia() {
  if (typeof document === "undefined") return;
  const root = document.querySelector(".co-comfy-param-drawer-root");
  if (!root) return;
  root.querySelectorAll<HTMLMediaElement>("video, audio").forEach((el) => {
    try {
      el.pause();
    } catch {
      // Ignore media elements not yet initialized.
    }
  });
}

function pauseOutputsDrawerMedia() {
  if (typeof document === "undefined") return;
  const root = document.querySelector(".co-comfy-outputs-drawer-root");
  if (!root) return;
  root.querySelectorAll<HTMLMediaElement>("video, audio").forEach((el) => {
    try {
      el.pause();
    } catch {
      // Ignore media elements not yet initialized.
    }
  });
}

function releaseHeavyOutputBlobs() {
  const heavyIds = new Set(
    outputs.value
      .filter((item) => item.kind === "video" || item.kind === "audio")
      .map((item) => item.id),
  );
  const urlsToRevoke: string[] = [];
  for (const [id, url] of Object.entries(blobUrls.value)) {
    if (heavyIds.has(id)) {
      urlsToRevoke.push(url);
      delete blobUrls.value[id];
    }
  }
  for (const id of Object.keys(streamUrls.value)) {
    if (heavyIds.has(id)) {
      delete streamUrls.value[id];
      delete bufferingStreams.value[id];
    }
  }
  for (const id of Object.keys(bufferingStreams.value)) {
    if (heavyIds.has(id)) {
      delete bufferingStreams.value[id];
    }
  }
  revokeBlobUrlsDeferred(urlsToRevoke);
}

function onOutputMediaReady(item: WorkflowOutput) {
  if (bufferingStreams.value[item.id]) {
    delete bufferingStreams.value[item.id];
  }
}

function onOutputMediaPlaying(item: WorkflowOutput) {
  onOutputMediaReady(item);
  clearInlineAutoplay(item.id);
}

function onOutputMediaError(item: WorkflowOutput) {
  if (disposed || !outputsDrawerOpen.value || !getOutputPreviewSrc(item)) return;
  clearInlineAutoplay(item.id);
  const hadStreamUrl = Boolean(streamUrls.value[item.id]);
  const canFallbackToBlob =
    hadStreamUrl &&
    !streamFallbackToBlob.value[item.id] &&
    item.kind === "image" &&
    (!Number.isFinite(item.size) || item.size <= MAX_INLINE_PREVIEW_BYTES) &&
    canLoadOutputAsset(item);
  const blobUrl = blobUrls.value[item.id];
  if (blobUrl) {
    delete blobUrls.value[item.id];
    revokeBlobUrlsDeferred([blobUrl]);
  }
  if (streamUrls.value[item.id]) {
    delete streamUrls.value[item.id];
  }
  if (bufferingStreams.value[item.id]) {
    delete bufferingStreams.value[item.id];
  }
  if (canFallbackToBlob) {
    streamFallbackToBlob.value[item.id] = true;
    if (item.id === galleryModalAnchorItemId.value) {
      void loadOutputPreview(item);
    } else {
      enqueueOutputPreviewPrefetch(item);
    }
    return;
  }
  failedOutputs.value[item.id] = { code: "media" };
}

function triggerRecentImagePreviews(_list?: WorkflowOutput[]) {
  if (!outputsDrawerOpen.value || disposed) return;
  let inFlight = 0;
  for (const active of Object.values(fetchingOutputs.value)) {
    if (active) inFlight += 1;
  }
  if (inFlight >= MAX_CONCURRENT_IMAGE_FETCHES) return;

  const prioritized: WorkflowOutput[] = [];
  const seen = new Set<string>();
  const enqueue = (item: WorkflowOutput | undefined) => {
    if (!item || seen.has(item.id)) return;
    seen.add(item.id);
    prioritized.push(item);
  };

  // 1. Active lightbox item + adjacent neighbors for instant photo flipping
  if (galleryModalOpen.value && activeGalleryGroup.value) {
    const items = activeGalleryGroup.value.items;
    const anchorIdx = Math.max(
      0,
      items.findIndex((it) => it.id === galleryModalAnchorItemId.value),
    );
    enqueue(items[anchorIdx]);
    if (items.length > 1) {
      enqueue(items[(anchorIdx + 1) % items.length]);
      enqueue(items[(anchorIdx - 1 + items.length) % items.length]);
    }
    if (items.length > 2) {
      enqueue(items[(anchorIdx + 2) % items.length]);
    }
  }

  // 2. Currently visible album covers / single images (via IntersectionObserver)
  if (visibleOutputGroupIds.value.size > 0) {
    for (const group of groupedOutputs.value) {
      if (!visibleOutputGroupIds.value.has(group.id)) continue;
      for (const cover of group.coverItems) {
        enqueue(cover);
      }
    }
  }

  // 3. Top 6 groups fallback so initial drawer open populates crisply
  for (const group of groupedOutputs.value.slice(0, 6)) {
    for (const cover of group.coverItems) {
      enqueue(cover);
    }
  }

  for (const item of prioritized) {
    if (inFlight >= MAX_CONCURRENT_IMAGE_FETCHES) break;
    if (
      item.kind === "image" &&
      canLoadOutputAsset(item) &&
      !isOutputTooLargeForPreview(item) &&
      !getOutputPreviewSrc(item) &&
      !fetchingOutputs.value[item.id] &&
      !failedOutputs.value[item.id]
    ) {
      inFlight += 1;
      void loadOutputPreview(item);
    }
  }
}

async function loadOutputPreview(item: WorkflowOutput) {
  if (
    isOutputTooLargeForPreview(item) ||
    getOutputPreviewSrc(item) ||
    fetchingOutputs.value[item.id] ||
    !canLoadOutputAsset(item) ||
    !outputsDrawerOpen.value
  ) {
    return;
  }
  const driveBacked = isOutputDriveBacked(item);
  const targetRuntimeId = props.state.runtime.id;
  abortOutputFetch(item.id);
  const controller = new AbortController();
  outputFetchControllers.set(item.id, controller);
  fetchingOutputs.value[item.id] = true;
  removePendingPrefetchId(item.id);
  delete failedOutputs.value[item.id];
  try {
    if (canStreamOutputMedia(item) && !streamFallbackToBlob.value[item.id]) {
      let streamUrl = "";
      let streamErr: unknown = null;
      try {
        streamUrl = await colabOneOutputPreviewURL(item.id, controller.signal);
      } catch (err) {
        const aborted =
          controller.signal.aborted ||
          (err instanceof DOMException && err.name === "AbortError");
        if (aborted) {
          throw err;
        }
        streamErr = err;
      }
      const latestStreamItem = outputs.value.find((o) => o.id === item.id);
      if (
        disposed ||
        controller.signal.aborted ||
        outputFetchControllers.get(item.id) !== controller ||
        !outputsDrawerOpen.value ||
        (!driveBacked && props.state.runtime.id !== targetRuntimeId) ||
        !latestStreamItem ||
        !canLoadOutputAsset(latestStreamItem)
      ) {
        return;
      }
      if (typeof streamUrl === "string" && streamUrl.trim()) {
        streamUrls.value[item.id] = streamUrl.trim();
        if (item.kind === "video" || item.kind === "audio") {
          bufferingStreams.value[item.id] = true;
        }
        return;
      }
      const canInlineFallback =
        !Number.isFinite(item.size) || item.size <= MAX_INLINE_PREVIEW_BYTES;
      if (streamErr && (item.kind !== "image" || !canInlineFallback)) {
        throw streamErr;
      }
      if (!canInlineFallback) {
        failedOutputs.value[item.id] = { code: "media" };
        return;
      }
    }
    const url = await outputBlobURL(item.id, controller.signal);
    const latestBlobItem = outputs.value.find((o) => o.id === item.id);
    if (
      disposed ||
      controller.signal.aborted ||
      outputFetchControllers.get(item.id) !== controller ||
      !outputsDrawerOpen.value ||
      (!driveBacked && props.state.runtime.id !== targetRuntimeId) ||
      !latestBlobItem ||
      !canLoadOutputAsset(latestBlobItem)
    ) {
      URL.revokeObjectURL(url);
    } else {
      const prevUrl = blobUrls.value[item.id];
      blobUrls.value[item.id] = url;
      if (prevUrl) {
        revokeBlobUrlsDeferred([prevUrl]);
      }
    }
  } catch (e) {
    const aborted =
      controller.signal.aborted ||
      (e instanceof DOMException && e.name === "AbortError");
    if (
      !aborted &&
      !disposed &&
      (driveBacked || props.state.runtime.id === targetRuntimeId)
    ) {
      failedOutputs.value[item.id] = { code: "fetch", detail: String(e) };
    }
  } finally {
    if (outputFetchControllers.get(item.id) === controller) {
      outputFetchControllers.delete(item.id);
      delete fetchingOutputs.value[item.id];
    }
    if (!disposed && outputsDrawerOpen.value) {
      triggerRecentImagePreviews(outputs.value);
      drainOutputPreviewPrefetchQueue();
    }
  }
}

async function syncDriveOutputsFromCloud(opts: { silent?: boolean } = {}) {
  if (isShareMode.value) return;
  if (syncingDriveOutputs.value || !props.state.setup.colab) return;
  syncingDriveOutputs.value = true;
  try {
    const res = await colabOneCommand("comfyui.outputs.drive.sync", {});
    emit("sync");
    if (!opts.silent && res?.synced) {
      setNoticeKey("comfy.noticeDriveOutputsSynced", {
        count: typeof res?.count === "number" ? res.count : outputs.value.length,
      });
    }
  } catch (e) {
    if (!opts.silent && !disposed) {
      reportError(e);
    }
  } finally {
    syncingDriveOutputs.value = false;
  }
}

async function setWorkflowAutoRelease(
  next: boolean,
  opts: { fromModal?: boolean } = {},
) {
  if (isShareMode.value) return;
  if (workflowAutoReleaseBusy.value || workflowAutoReleaseLocked.value) return;
  if (!gpuReady.value) return;
  if (next && !props.state.setup.drive) {
    openDriveSetupModal(null, false, "auto_release", true);
    return;
  }
  if (next && needsUpgradeForWorkflowAutoRelease.value) {
    openDriveSetupModal(null, false, "auto_release", false);
    return;
  }
  workflowAutoReleaseBusy.value = true;
  if (opts.fromModal) {
    driveModalError.value = "";
  }
  try {
    await colabOneCommand("comfyui.auto_release", { enabled: next });
    if (disposed) return;
    emit("sync");
    if (opts.fromModal && next) {
      driveModalOpen.value = false;
      emit("notify", t("comfy.autoReleaseArmedAfterDriveToast"), "success");
    } else {
      setNoticeKey(
        next
          ? "comfy.noticeAutoReleaseEnabled"
          : "comfy.noticeAutoReleaseDisabled",
      );
    }
  } catch (e) {
    if (!disposed) {
      emit("sync");
      if (opts.fromModal && driveModalOpen.value) {
        driveModalError.value = String(e);
      } else {
        reportError(e);
      }
    }
  } finally {
    workflowAutoReleaseBusy.value = false;
  }
}

function onWorkflowAutoReleaseToggle(checked: boolean | string | number) {
  void setWorkflowAutoRelease(Boolean(checked));
}

const workflowAutoReleaseErrorVisible = computed(() => {
  if (
    !gpuReady.value ||
    workflowAutoReleaseState.value !== "failed" ||
    !workflowAutoRelease.value?.error
  ) {
    return false;
  }
  const ts = Number(workflowAutoRelease.value?.updated_at || 0);
  if (
    dismissedAutoReleaseErrorAt.value !== null &&
    dismissedAutoReleaseErrorAt.value === ts
  ) {
    return false;
  }
  return true;
});

function dismissAutoReleaseErrorAlert() {
  dismissedAutoReleaseErrorAt.value = Number(
    workflowAutoRelease.value?.updated_at || 0,
  );
}

watch(gpuReady, (nextReady, prevReady) => {
  if (nextReady !== prevReady) {
    failedOutputs.value = {};
    if (
      !nextReady &&
      outputsDrawerOpen.value &&
      props.state.setup.colab &&
      driveOutputsCanDirectRead.value
    ) {
      void syncDriveOutputsFromCloud({ silent: true });
    }
  }
});

watch(outputsDrawerOpen, (open) => {
  if (!open) {
    disconnectOutputCardObserver();
    clearInlineAutoplay();
    pauseOutputsDrawerMedia();
    abortAllOutputFetches();
    releaseHeavyOutputBlobs();
    streamFallbackToBlob.value = {};
    outputMultiSelectMode.value = false;
    selectedOutputGroupIds.value = [];
    closeOutputGallery();
    if (outputShareModalOpen.value) {
      closeOutputShareModal();
    }
  } else {
    void nextTick(() => {
      if (!disposed && outputsDrawerOpen.value) {
        ensureOutputCardObserver();
      }
    });
    if (
      !gpuReady.value &&
      props.state.setup.colab &&
      driveOutputsCanDirectRead.value
    ) {
      void syncDriveOutputsFromCloud({ silent: true });
    }
  }
});

watch(groupedOutputs, (groups) => {
  const validGroupIds = new Set(groups.map((g) => g.id));
  if (selectedOutputGroupIds.value.length) {
    const filtered = selectedOutputGroupIds.value.filter((id) =>
      validGroupIds.has(id),
    );
    if (filtered.length !== selectedOutputGroupIds.value.length) {
      selectedOutputGroupIds.value = filtered;
    }
  }
  if (galleryModalOpen.value) {
    const resolved = activeGalleryGroup.value;
    if (!resolved) {
      closeOutputGallery();
    } else if (resolved.id !== galleryModalGroupId.value) {
      galleryModalGroupId.value = resolved.id;
      galleryModalTaskId.value = resolved.taskId || galleryModalTaskId.value;
    }
  }
});

watch(
  [outputs, gpuReady, outputsDrawerOpen],
  ([list, _ready, drawerOpen]) => {
    const activeIds = new Set(list.map((item) => item.id));
    const activeStreamableIds = new Set(
      list.filter((item) => canLoadOutputAsset(item)).map((item) => item.id),
    );
    const staleUrls: string[] = [];
    for (const [id, url] of Object.entries(blobUrls.value)) {
      if (!activeIds.has(id)) {
        staleUrls.push(url);
        delete blobUrls.value[id];
      }
    }
    for (const id of Object.keys(streamUrls.value)) {
      if (!activeStreamableIds.has(id)) {
        delete streamUrls.value[id];
        delete bufferingStreams.value[id];
      }
    }
    for (const id of Object.keys(bufferingStreams.value)) {
      if (!activeStreamableIds.has(id)) {
        delete bufferingStreams.value[id];
      }
    }
    revokeBlobUrlsDeferred(staleUrls);
    for (const id of Object.keys(failedOutputs.value)) {
      if (!activeIds.has(id)) {
        delete failedOutputs.value[id];
      }
    }
    for (const id of Object.keys(streamFallbackToBlob.value)) {
      if (!activeIds.has(id)) {
        delete streamFallbackToBlob.value[id];
      }
    }
    for (const id of Array.from(pendingPrefetchSet)) {
      if (!activeIds.has(id)) {
        removePendingPrefetchId(id);
      }
    }
    for (const id of Object.keys(exportedDriveOutputs.value)) {
      if (!activeIds.has(id)) {
        const next = { ...exportedDriveOutputs.value };
        delete next[id];
        exportedDriveOutputs.value = next;
      }
    }
    if (driveModalTargetGroupId.value) {
      let liveGroup = groupedOutputs.value.find(
        (g) => g.id === driveModalTargetGroupId.value,
      );
      if (!liveGroup) {
        let demotedGroup: ColabOneOutputGroup | undefined;
        if (driveModalTarget.value && activeIds.has(driveModalTarget.value.id)) {
          demotedGroup = groupedOutputs.value.find((g) =>
            g.items.some((it) => it.id === driveModalTarget.value!.id),
          );
        }
        if (
          !demotedGroup &&
          driveModalTargetGroupId.value.startsWith("album:")
        ) {
          const targetTaskId = driveModalTargetGroupId.value.slice(
            "album:".length,
          );
          if (targetTaskId) {
            demotedGroup = groupedOutputs.value.find(
              (g) => g.taskId === targetTaskId,
            );
          }
        }
        if (demotedGroup) {
          liveGroup = demotedGroup;
          driveModalTargetGroupId.value = demotedGroup.id;
        }
      }
      if (!liveGroup) {
        if (!driveModalResult.value) {
          driveModalTargetGroupId.value = "";
          driveModalTarget.value = null;
          driveModalAutoExport.value = false;
        }
      } else if (
        !driveModalTarget.value ||
        !activeIds.has(driveModalTarget.value.id)
      ) {
        driveModalTarget.value =
          liveGroup.items.find((it) => !isOutputExportedToDrive(it)) ||
          liveGroup.items[0] ||
          null;
      }
    } else if (
      driveModalTarget.value &&
      !activeIds.has(driveModalTarget.value.id) &&
      !driveModalResult.value
    ) {
      driveModalTarget.value = null;
      driveModalAutoExport.value = false;
    }
    for (const id of Array.from(outputFetchControllers.keys())) {
      if (!activeIds.has(id)) {
        abortOutputFetch(id);
      }
    }
    if (!drawerOpen) return;
    // Automatically load previews for recent image outputs (runtime or direct Drive API).
    triggerRecentImagePreviews(list);
  },
  { immediate: true },
);

watch(
  () => props.state.runtime.id,
  (nextId, prevId) => {
    if (prevId && nextId !== prevId) {
      exportedDriveOutputs.value = {};
      streamFallbackToBlob.value = {};
      dismissedAutoReleaseErrorAt.value = null;
      driveModalTargetGroupId.value = "";
      if (driveModalOpen.value) {
        driveModalPurpose.value = "config";
        driveModalTarget.value = null;
        driveModalAutoExport.value = false;
        driveModalAutoArmRelease.value = false;
        driveModalResult.value = null;
        driveModalError.value = "";
      }
    }
  },
);

function finishHeadlessCheckInModal() {
  clearHeadlessTimer();
  checkingHeadlessInModal.value = false;
  sawHeadlessOpInModal.value = false;
  const stateErr = String(props.state.error || "").trim();
  if (props.state.setup.drive_headless && !stateErr) {
    emit("notify", t("settings.driveHeadlessEnabledToast"), "success");
  } else if (stateErr) {
    driveModalError.value = stateErr;
  }
}

watch(
  () => [
    driveModalOpen.value,
    Boolean(props.state.setup.drive),
    Boolean(props.state.operation),
    isColabOneDriveConnectOperation(props.state.operation),
    props.state.runtime.state,
    String(props.state.error || "").trim(),
    Boolean(props.state.setup.drive_headless),
    needsUpgradeForDriveExport.value,
    needsUpgradeForWorkflowAutoRelease.value,
  ] as const,
  ([
    open,
    connected,
    opActive,
    driveOpActive,
    _rtState,
    stateErr,
    driveHeadless,
    needsUpgrade,
    needsAutoReleaseUpgrade,
  ]) => {
    if (connectingDriveInModal.value) {
      if (driveOpActive) {
        sawDriveConnectOpInModal.value = true;
        clearDriveConnectTimer();
      } else if (connected) {
        clearDriveConnectTimer();
        connectingDriveInModal.value = false;
        sawDriveConnectOpInModal.value = false;
      } else if (sawDriveConnectOpInModal.value) {
        clearDriveConnectTimer();
        connectingDriveInModal.value = false;
        sawDriveConnectOpInModal.value = false;
        driveModalAutoExport.value = false;
        driveModalAutoArmRelease.value = false;
        if (stateErr) {
          driveModalError.value = stateErr;
        }
      } else if (
        !opActive &&
        stateErr &&
        stateErr !== driveConnectBaselineError.value
      ) {
        clearDriveConnectTimer();
        connectingDriveInModal.value = false;
        driveModalAutoExport.value = false;
        driveModalAutoArmRelease.value = false;
        driveModalError.value = stateErr;
      }
    }
    if (checkingHeadlessInModal.value) {
      if (opActive && !driveOpActive) {
        sawHeadlessOpInModal.value = true;
        clearHeadlessTimer();
      } else if (sawHeadlessOpInModal.value && !opActive) {
        finishHeadlessCheckInModal();
      } else if (
        !opActive &&
        (driveHeadless ||
          (stateErr && stateErr !== headlessBaselineError.value))
      ) {
        finishHeadlessCheckInModal();
      }
    }
    if (!open || !connected || opActive) return;
    if (driveModalAutoArmRelease.value) {
      driveModalAutoArmRelease.value = false;
      if (!needsAutoReleaseUpgrade) {
        void setWorkflowAutoRelease(true, { fromModal: true });
      }
      return;
    }
    if (driveModalAutoExport.value && driveModalTarget.value) {
      const targetGroup = driveModalTargetGroupId.value
        ? groupedOutputs.value.find((g) => g.id === driveModalTargetGroupId.value)
        : null;
      if (targetGroup) {
        if (needsUpgrade) {
          driveModalAutoExport.value = false;
          return;
        }
        if (
          !exportingAlbumDrives.value[targetGroup.id] &&
          !driveModalResult.value
        ) {
          driveModalAutoExport.value = false;
          void exportAlbumToDrive(targetGroup, { fromModal: true });
        }
        return;
      }
      const target = driveModalTarget.value;
      if (!outputs.value.some((o) => o.id === target.id)) {
        driveModalAutoExport.value = false;
        return;
      }
      if (needsUpgrade) {
        driveModalAutoExport.value = false;
        return;
      }
      if (!exportingDriveOutputs.value[target.id] && !driveModalResult.value) {
        driveModalAutoExport.value = false;
        void runDriveExport(target, { fromModal: true });
      }
    }
  },
);

watch(
  () => props.state.auth_url,
  (url) => {
    if (!url) {
      confirmingDriveAuthInModal.value = false;
    }
  },
);

watch(driveModalOpen, (open) => {
  if (!open) {
    clearDriveModalTimers();
    driveModalPurpose.value = "config";
    driveModalTargetGroupId.value = "";
    driveModalAutoExport.value = false;
    driveModalAutoArmRelease.value = false;
    driveModalResult.value = null;
    driveModalError.value = "";
    connectingDriveInModal.value = false;
    sawDriveConnectOpInModal.value = false;
    checkingHeadlessInModal.value = false;
    sawHeadlessOpInModal.value = false;
  }
});

function openDriveSetupModal(
  target: WorkflowOutput | null = null,
  autoExport = false,
  purpose: "config" | "export" | "auto_release" = target ? "export" : "config",
  autoArmRelease = false,
  targetGroupId = "",
) {
  if (isShareMode.value) return;
  driveModalTargetGroupId.value = targetGroupId;
  driveModalPurpose.value = purpose;
  driveModalTarget.value = target;
  driveModalAutoExport.value = autoExport;
  driveModalAutoArmRelease.value = autoArmRelease;
  driveModalResult.value = null;
  driveModalError.value = "";
  driveModalOpen.value = true;
}

async function runDriveExport(
  item: WorkflowOutput,
  opts: { fromModal: boolean },
) {
  if (isShareMode.value) return;
  if (
    !gpuReady.value ||
    exportingDriveOutputs.value[item.id] ||
    deletingOutputs.value[item.id] ||
    isItemParentGroupBusy(item.id)
  ) {
    return;
  }
  exportingDriveOutputs.value = {
    ...exportingDriveOutputs.value,
    [item.id]: true,
  };
  if (opts.fromModal) {
    driveModalError.value = "";
  }
  const targetRuntimeId = props.state.runtime.id;
  try {
    const res = await colabOneCommand("comfyui.output.export_drive", {
      id: item.id,
    });
    if (disposed || props.state.runtime.id !== targetRuntimeId) {
      return;
    }
    const remoteFolder =
      typeof res?.remote_folder === "string" && res.remote_folder.trim()
        ? res.remote_folder.trim()
        : `${activeDriveFolderName.value}/outputs`;
    const filename =
      typeof res?.filename === "string" && res.filename.trim()
        ? res.filename.trim()
        : item.filename;
    const drivePath =
      typeof res?.drive_path === "string" && res.drive_path.trim()
        ? res.drive_path.trim()
        : `My Drive/${remoteFolder}/${filename}`;
    const result: ColabOneDriveExportResult = {
      id: item.id,
      filename,
      remoteFolder,
      drivePath,
      alreadyExists: Boolean(res?.already_exists),
      size: typeof res?.size === "number" ? res.size : item.size,
    };
    exportedDriveOutputs.value = {
      ...exportedDriveOutputs.value,
      [item.id]: result,
    };
    if (
      opts.fromModal &&
      driveModalOpen.value &&
      driveModalTarget.value?.id === item.id
    ) {
      driveModalResult.value = result;
    }
    setNoticeKey(
      result.alreadyExists
        ? "comfy.noticeExportedDriveExists"
        : "comfy.noticeExportedDrive",
      { path: formatColabOneDrivePath(drivePath) },
    );
  } catch (e) {
    if (!disposed) {
      const msg = String(e);
      emit("sync");
      if (opts.fromModal && driveModalOpen.value) {
        driveModalError.value = msg;
      } else {
        reportError(e);
      }
    }
  } finally {
    const next = { ...exportingDriveOutputs.value };
    delete next[item.id];
    exportingDriveOutputs.value = next;
  }
}

async function saveOutputToHostDrive(item: WorkflowOutput) {
  if (
    !isShareMode.value ||
    !props.shareDriveExportReady ||
    !gpuReady.value ||
    isOutputExportedToDrive(item) ||
    exportingDriveOutputs.value[item.id] ||
    deletingOutputs.value[item.id] ||
    isItemParentGroupBusy(item.id)
  ) {
    return;
  }
  exportingDriveOutputs.value = {
    ...exportingDriveOutputs.value,
    [item.id]: true,
  };
  const targetRuntimeId = props.state.runtime.id;
  try {
    const res = await colabOneCommand("comfyui.output.export_drive", {
      id: item.id,
    });
    if (disposed || props.state.runtime.id !== targetRuntimeId) {
      return;
    }
    const alreadyExists = Boolean(res?.already_exists);
    const filename =
      typeof res?.filename === "string" && res.filename.trim()
        ? res.filename.trim()
        : item.filename;
    exportedDriveOutputs.value = {
      ...exportedDriveOutputs.value,
      [item.id]: {
        id: item.id,
        filename,
        remoteFolder: "",
        drivePath: "",
        alreadyExists,
        size: typeof res?.size === "number" ? res.size : item.size,
      },
    };
    emit("sync");
    setNoticeKey(
      alreadyExists
        ? "comfy.noticeAlreadyInHostDrive"
        : "comfy.noticeSavedToHostDrive",
    );
  } catch (e) {
    if (!disposed) {
      emit("sync");
      reportError(e);
    }
  } finally {
    const next = { ...exportingDriveOutputs.value };
    delete next[item.id];
    exportingDriveOutputs.value = next;
  }
}

async function exportOutputToDrive(item: WorkflowOutput) {
  if (isShareMode.value) {
    await saveOutputToHostDrive(item);
    return;
  }
  if (
    !gpuReady.value ||
    exportingDriveOutputs.value[item.id] ||
    deletingOutputs.value[item.id] ||
    isItemParentGroupBusy(item.id)
  ) {
    return;
  }
  if (!props.state.setup.drive || needsUpgradeForDriveExport.value) {
    openDriveSetupModal(item, !props.state.setup.drive);
    return;
  }
  await runDriveExport(item, { fromModal: false });
}

async function connectDriveFromModal() {
  if (isShareMode.value) return;
  if (connectingDriveInModal.value || props.state.operation) return;
  clearDriveConnectTimer();
  connectingDriveInModal.value = true;
  sawDriveConnectOpInModal.value = false;
  driveConnectBaselineError.value = String(props.state.error || "").trim();
  driveModalError.value = "";
  if (driveModalTarget.value) {
    driveModalAutoExport.value = true;
  } else if (driveModalPurpose.value === "auto_release") {
    driveModalAutoArmRelease.value = true;
  }
  try {
    await colabOneCommand("drive.connect", {});
    emit("sync");
    driveConnectReconcileTimer = setTimeout(() => {
      driveConnectReconcileTimer = null;
      if (
        !disposed &&
        connectingDriveInModal.value &&
        !sawDriveConnectOpInModal.value &&
        !isColabOneDriveConnectOperation(props.state.operation)
      ) {
        connectingDriveInModal.value = false;
        if (!props.state.setup.drive) {
          driveModalAutoExport.value = false;
          driveModalAutoArmRelease.value = false;
          const stateErr = String(props.state.error || "").trim();
          if (stateErr && stateErr !== driveConnectBaselineError.value) {
            driveModalError.value = stateErr;
          }
        }
      }
    }, 2500);
  } catch (e) {
    clearDriveConnectTimer();
    connectingDriveInModal.value = false;
    sawDriveConnectOpInModal.value = false;
    driveModalAutoExport.value = false;
    driveModalAutoArmRelease.value = false;
    driveModalError.value = String(e);
  }
}

async function confirmDriveAuthFromModal() {
  if (isShareMode.value) return;
  if (confirmingDriveAuthInModal.value) return;
  confirmingDriveAuthInModal.value = true;
  driveModalError.value = "";
  try {
    await colabOneCommand("drive.confirm", {});
    emit("sync");
  } catch (e) {
    driveModalError.value = String(e);
  } finally {
    confirmingDriveAuthInModal.value = false;
  }
}

async function cancelDriveAuthFromModal() {
  if (isShareMode.value) return;
  clearDriveConnectTimer();
  connectingDriveInModal.value = false;
  sawDriveConnectOpInModal.value = false;
  driveModalAutoExport.value = false;
  driveModalAutoArmRelease.value = false;
  driveModalError.value = "";
  try {
    await colabOneCommand("drive.cancel", {});
    emit("sync");
  } catch (e) {
    driveModalError.value = String(e);
  }
}

async function setupHeadlessDriveFromModal() {
  if (isShareMode.value) return;
  if (checkingHeadlessInModal.value || props.state.operation) return;
  clearHeadlessTimer();
  checkingHeadlessInModal.value = true;
  sawHeadlessOpInModal.value = false;
  headlessBaselineError.value = String(props.state.error || "").trim();
  driveModalError.value = "";
  try {
    await colabOneCommand("drive.setup_headless", {});
    emit("sync");
    headlessReconcileTimer = setTimeout(() => {
      headlessReconcileTimer = null;
      if (
        !disposed &&
        checkingHeadlessInModal.value &&
        !sawHeadlessOpInModal.value &&
        !props.state.operation
      ) {
        finishHeadlessCheckInModal();
      }
    }, 2500);
  } catch (e) {
    clearHeadlessTimer();
    checkingHeadlessInModal.value = false;
    sawHeadlessOpInModal.value = false;
    driveModalError.value = String(e);
  }
}

async function toggleAutoDriveFromModal(next: boolean) {
  if (isShareMode.value) return;
  if (savingAutoDriveInModal.value) return;
  savingAutoDriveInModal.value = true;
  driveModalError.value = "";
  try {
    await colabOneCommand("settings.save", { auto_drive: next });
    emit("sync");
  } catch (e) {
    driveModalError.value = String(e);
  } finally {
    savingAutoDriveInModal.value = false;
  }
}

function exportTargetFromModal() {
  if (isShareMode.value) return;
  if (driveModalTargetGroupId.value) {
    const group = groupedOutputs.value.find(
      (g) => g.id === driveModalTargetGroupId.value,
    );
    if (group) {
      void exportAlbumToDrive(group, { fromModal: true });
      return;
    }
  }
  if (!driveModalTarget.value) return;
  void runDriveExport(driveModalTarget.value, { fromModal: true });
}

function armAutoReleaseFromModal() {
  if (isShareMode.value) return;
  void setWorkflowAutoRelease(true, { fromModal: true });
}

function triggerUpgradeFromDriveModal() {
  if (isShareMode.value) return;
  driveModalOpen.value = false;
  emit("upgrade");
}

function openSettingsFromDriveModal() {
  if (isShareMode.value) return;
  driveModalOpen.value = false;
  outputsDrawerOpen.value = false;
  emit("settings");
}

async function exportOutput(item: WorkflowOutput) {
  if (
    !canLoadOutputAsset(item) ||
    exportingOutputs.value[item.id] ||
    deletingOutputs.value[item.id] ||
    isItemParentGroupBusy(item.id)
  ) {
    return;
  }
  exportingOutputs.value = { ...exportingOutputs.value, [item.id]: true };
  try {
    const path = await colabOneExportWorkflowOutput(item.id);
    if (path) {
      setNoticeKey("comfy.noticeExported");
    }
  } catch (e) {
    reportError(e);
  } finally {
    const next = { ...exportingOutputs.value };
    delete next[item.id];
    exportingOutputs.value = next;
  }
}

async function deleteOutput(target: WorkflowOutput) {
  if (
    (!gpuReady.value && (isShareMode.value || !isOutputDriveBacked(target))) ||
    deletingOutputs.value[target.id] ||
    exportingOutputs.value[target.id] ||
    exportingDriveOutputs.value[target.id] ||
    isItemParentGroupBusy(target.id)
  ) {
    return;
  }
  deletingOutputs.value = { ...deletingOutputs.value, [target.id]: true };
  try {
    abortOutputFetch(target.id);
    const res = await colabOneCommand("comfyui.output.delete", { id: target.id });
    const url = blobUrls.value[target.id];
    if (url) {
      delete blobUrls.value[target.id];
      revokeBlobUrlsDeferred([url]);
    }
    delete streamUrls.value[target.id];
    delete streamFallbackToBlob.value[target.id];
    delete bufferingStreams.value[target.id];
    delete failedOutputs.value[target.id];
    if (exportedDriveOutputs.value[target.id]) {
      const nextExported = { ...exportedDriveOutputs.value };
      delete nextExported[target.id];
      exportedDriveOutputs.value = nextExported;
    }
    emit("sync");
    if (res?.manual_cloud_cleanup) {
      emit("clearError");
      const reason =
        typeof res.warning_reason === "string" ? res.warning_reason : "";
      emit(
        "notify",
        formatDeleteOutputManualCloudWarning(reason, false, 1),
        "warning",
      );
    } else {
      setNoticeKey("comfy.noticeDeletedOutput");
    }
  } catch (e) {
    reportError(e);
  } finally {
    const next = { ...deletingOutputs.value };
    delete next[target.id];
    deletingOutputs.value = next;
  }
}

function outputKindLabel(kind: WorkflowOutput["kind"]) {
  if (kind === "video") return t("comfy.kindVideo");
  if (kind === "audio") return t("comfy.kindAudio");
  return t("comfy.kindImage");
}

function outputKindColor(kind: WorkflowOutput["kind"]) {
  if (kind === "video") return "purple";
  if (kind === "audio") return "cyan";
  return "blue";
}

// Native Canvas tab state
const canvasDomain = ref(props.state.comfy_canvas_settings?.domain || "");
const privacyContext = computed(() => {
  const ctx = buildColabOnePrivacyContext(props.state);
  const extraHosts = [...(ctx.hosts || [])];
  for (const rawCandidate of [
    canvasDomain.value,
    resolvedWebConsoleState.value.public_origin,
  ]) {
    const candidateHost = extractColabOneHostFromInput(rawCandidate);
    if (
      candidateHost &&
      !isColabOnePublicHost(candidateHost) &&
      !extraHosts.includes(candidateHost)
    ) {
      extraHosts.push(candidateHost);
    }
  }
  if (extraHosts.length !== (ctx.hosts || []).length) {
    return { ...ctx, hosts: extraHosts };
  }
  return ctx;
});
function onCanvasDomainUpdate(next: string) {
  canvasDomain.value = next;
  canvasSettingsEdited.value = true;
}

const canvasTunnelToken = ref("");
const canvasTokenInputRef = ref<InstanceType<typeof ColabOneSecretInput> | null>(
  null,
);
const canvasSettingsEdited = ref(false);
const copiedCanvasUrl = ref(false);
let copyTimer: ReturnType<typeof setTimeout> | undefined;

watch(
  () => props.state.comfy_canvas_settings,
  (settings) => {
    if (!settings || canvasSettingsEdited.value) return;
    canvasDomain.value = settings.domain || "";
  },
  { deep: true },
);

function onTabChange(key: string | number) {
  if (key === "canvas") {
    activeTab.value = "canvas";
  } else if (key === "live") {
    activeTab.value = "live";
  } else {
    activeTab.value = "studio";
  }
}

const liveStudioRunning = computed(
  () => gpuReady.value && Boolean(props.state.runtime.live_studio?.running),
);

const canvasState = computed(() => canvas.value?.state || "stopped");
const canvasEnabled = computed(
  () =>
    !!canvas.value?.enabled ||
    canvasState.value === "starting" ||
    canvasState.value === "ready" ||
    canvasState.value === "reconnecting",
);
const canvasReady = computed(
  () => canvasState.value === "ready" && !!canvas.value?.url,
);
const canvasStatusLabel = computed(
  () =>
    ({
      stopped: t("api.statusStopped"),
      starting: t("api.statusStarting"),
      ready: t("api.statusReady"),
      reconnecting: t("api.statusReconnecting"),
      stopping: t("api.statusStopping"),
      failed: t("api.statusFailed"),
    })[canvasState.value] || t("api.statusStopped"),
);
const canvasTagColor = computed(() => {
  if (canvasState.value === "ready") return "success";
  if (
    canvasState.value === "starting" ||
    canvasState.value === "reconnecting"
  ) {
    return "warning";
  }
  if (canvasState.value === "failed") return "error";
  return "default";
});
const canvasBadgeStatus = computed(() => {
  if (canvasState.value === "ready") return "success";
  if (
    canvasState.value === "starting" ||
    canvasState.value === "reconnecting"
  ) {
    return "processing";
  }
  if (canvasState.value === "failed") return "error";
  return "default";
});
const canvasPublicHost = computed(() => extractPublicHost(canvas.value?.url));

async function startCanvas() {
  if (isShareMode.value) return;
  if (!gpuReady.value) return;
  if (!comfyRunning.value) {
    await startComfy();
    return;
  }
  const ok = await actEngine(
    "comfyui.canvas.start",
    {
      mode: "named",
      domain: canvasDomain.value.trim(),
      tunnel_token: canvasTunnelToken.value.trim(),
    },
    "comfy.noticeStartingCanvas",
  );
  if (ok) {
    canvasTunnelToken.value = "";
    canvasTokenInputRef.value?.hide();
    canvasSettingsEdited.value = false;
  }
}

async function saveCanvasSettings() {
  if (isShareMode.value) return;
  const ok = await actEngine(
    "comfyui.canvas.save",
    {
      mode: "named",
      domain: canvasDomain.value.trim(),
      tunnel_token: canvasTunnelToken.value.trim(),
    },
    "comfy.noticeSavedCanvasConfig",
  );
  if (ok) {
    canvasTunnelToken.value = "";
    canvasTokenInputRef.value?.hide();
    canvasSettingsEdited.value = false;
  }
}

async function loadSavedCanvasToken(): Promise<string> {
  if (isShareMode.value) return "";
  const res = await colabOneCommand("comfyui.canvas.tunnel_token");
  return typeof res?.token === "string" ? res.token : "";
}

function onCanvasTokenExtracted() {
  emit("notify", t("api.tunnelTokenExtractedToast"), "success");
}

function onCanvasTokenCopied() {
  emit("notify", t("api.tunnelTokenCopiedToast"), "success");
}

async function stopCanvas() {
  if (isShareMode.value) return;
  await actEngine("comfyui.canvas.stop", {}, "comfy.noticeStoppedCanvas");
}

async function launchCanvas(target: "window" | "browser") {
  if (isShareMode.value) return;
  try {
    await colabOneOpenComfyCanvas(target);
  } catch (e) {
    reportError(e);
  }
}

async function copyCanvasAuthUrl() {
  if (isShareMode.value) return;
  try {
    const res = await colabOneCommand("comfyui.canvas.url");
    if (disposed) return;
    const url =
      typeof res === "string"
        ? res
        : res?.url || res?.auth_url || canvas.value?.url || "";
    if (!url) {
      throw new Error(t("comfy.errNoAuthUrl"));
    }
    await colabOneWriteClipboard(url);
    if (isColabOneDesktopRuntime()) {
      await navigator.clipboard?.writeText(url).catch(() => {});
    }
    if (disposed) return;
    copiedCanvasUrl.value = true;
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copiedCanvasUrl.value = false;
    }, 2000);
    setNoticeKey("comfy.noticeCopiedAuthLink");
  } catch (e) {
    if (!disposed) {
      reportError(e);
    }
  }
}

function syncTabsAriaLabel() {
  tabsWrapRef.value
    ?.querySelector('.ant-tabs-nav[role="tablist"]')
    ?.setAttribute("aria-label", t("comfy.modeAria"));
}

function hasVisibleAntOverlay(): boolean {
  if (typeof document === "undefined") return false;
  const overlays = document.querySelectorAll<HTMLElement>(
    [
      ".ant-modal-wrap",
      ".ant-popconfirm:not(.ant-popover-hidden)",
      ".ant-popover:not(.ant-popover-hidden)",
      ".ant-dropdown:not(.ant-dropdown-hidden)",
      ".ant-select-dropdown:not(.ant-select-dropdown-hidden)",
    ].join(", "),
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

function hasVisibleAntModal(): boolean {
  if (typeof document === "undefined") return false;
  const modals = document.querySelectorAll<HTMLElement>(".ant-modal-wrap");
  for (const el of modals) {
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

watch(
  [
    activeTab,
    busy,
    importOpen,
    driveModalOpen,
    galleryModalOpen,
    outputShareModalOpen,
    workflows,
  ],
  () => {
    const id = deleteConfirmWorkflowId.value;
    if (!id) return;
    if (
      activeTab.value !== "studio" ||
      busy.value ||
      importOpen.value ||
      driveModalOpen.value ||
      galleryModalOpen.value ||
      outputShareModalOpen.value ||
      !workflows.value.some((w) => w.id === id)
    ) {
      deleteConfirmWorkflowId.value = "";
    }
  },
);

watch(
  [
    outputsDrawerOpen,
    outputMultiSelectMode,
    importOpen,
    driveModalOpen,
    galleryModalOpen,
    outputShareModalOpen,
    groupedOutputs,
    gpuReady,
    exportingOutputs,
    exportingDriveOutputs,
    deletingOutputs,
    exportingAlbumZips,
    exportingAlbumDrives,
    deletingAlbums,
  ],
  () => {
    const id = deleteConfirmOutputId.value;
    if (!id) return;
    if (
      !outputsDrawerOpen.value ||
      outputMultiSelectMode.value ||
      importOpen.value ||
      driveModalOpen.value ||
      galleryModalOpen.value ||
      outputShareModalOpen.value
    ) {
      deleteConfirmOutputId.value = "";
      return;
    }
    const albumGroup = groupedOutputs.value.find(
      (g) => g.isAlbum && g.id === id,
    );
    if (albumGroup) {
      if (
        !canDeleteGroup(albumGroup) ||
        exportingAlbumZips.value[albumGroup.id] ||
        exportingAlbumDrives.value[albumGroup.id] ||
        deletingAlbums.value[albumGroup.id] ||
        isAnyGroupItemBusy(albumGroup)
      ) {
        deleteConfirmOutputId.value = "";
      }
      return;
    }
    const singleGroup = groupedOutputs.value.find(
      (g) => !g.isAlbum && g.items.some((it) => it.id === id),
    );
    const singleItem = singleGroup?.items.find((it) => it.id === id);
    if (
      !singleItem ||
      (!gpuReady.value &&
        (isShareMode.value || !isOutputDriveBacked(singleItem))) ||
      exportingOutputs.value[singleItem.id] ||
      exportingDriveOutputs.value[singleItem.id] ||
      deletingOutputs.value[singleItem.id] ||
      isItemParentGroupBusy(singleItem.id)
    ) {
      deleteConfirmOutputId.value = "";
    }
  },
);

function handlePanelKeydown(e: KeyboardEvent) {
  if (e.key !== "Escape") return;
  if (
    importOpen.value ||
    driveModalOpen.value ||
    galleryModalOpen.value ||
    outputShareModalOpen.value ||
    hasVisibleAntModal()
  ) {
    return;
  }
  if (deleteConfirmWorkflowId.value || deleteConfirmOutputId.value) {
    e.stopImmediatePropagation();
    deleteConfirmWorkflowId.value = "";
    deleteConfirmOutputId.value = "";
    return;
  }
  if (mobileAutoReleasePopoverOpen.value) {
    e.stopImmediatePropagation();
    mobileAutoReleasePopoverOpen.value = false;
    return;
  }
  if (hasVisibleAntOverlay()) {
    return;
  }
  if (paramDrawerOpen.value && selectedWorkflow.value) {
    e.stopImmediatePropagation();
    closeParamDrawer();
  } else if (outputsDrawerOpen.value) {
    e.stopImmediatePropagation();
    closeOutputsDrawer();
  }
}

function onWindowPageHide() {
  flushDraftSave(undefined, { silent: true });
}

function onDocumentVisibilityChange() {
  if (
    typeof document !== "undefined" &&
    document.visibilityState === "hidden"
  ) {
    flushDraftSave(undefined, { silent: true });
  }
}

watch(colabOneLocaleCode, (nextLocale) => {
  syncTabsAriaLabel();
  const wf = selectedWorkflow.value;
  if (!wf) return;
  const current = fieldValues.value;
  const remapped = remapColabOnePresetPromptFieldsForLocale(
    wf.fields,
    current,
    nextLocale,
  );
  let changed = false;
  for (const key of Object.keys(remapped)) {
    if (remapped[key] !== current[key]) {
      changed = true;
      break;
    }
  }
  if (changed) {
    fieldValues.value = remapped;
  }
});

watch(
  () => paramDrawerOpen.value && Boolean(selectedWorkflow.value),
  (visible) => {
    draftStatusController.onVisibilityChange(visible);
  },
);

watch(paramDrawerOpen, (open) => {
  if (!open) {
    pauseParamDrawerMedia();
    cancelVoicePreviewPrefetch();
    flushDraftSave();
  }
});

onMounted(() => {
  syncTabsAriaLabel();
  if (props.clockNow === undefined) {
    fallbackClockTimer = setInterval(() => {
      if (typeof document === "undefined" || !document.hidden) {
        fallbackClockNow.value = Date.now();
      }
    }, 1000);
  }
  window.addEventListener("resize", onWindowResize);
  window.addEventListener("keydown", handlePanelKeydown, true);
  window.addEventListener("blur", onWindowPageHide);
  window.addEventListener("pagehide", onWindowPageHide);
  window.addEventListener("beforeunload", onWindowPageHide);
  document.addEventListener("visibilitychange", onDocumentVisibilityChange);
});

onUnmounted(() => {
  flushDraftSave(undefined, { silent: true });
  cancelDraftSaveTimer();
  draftStatusController.dispose();
  if (fallbackClockTimer !== null) {
    clearInterval(fallbackClockTimer);
    fallbackClockTimer = null;
  }
  window.removeEventListener("resize", onWindowResize);
  window.removeEventListener("keydown", handlePanelKeydown, true);
  window.removeEventListener("blur", onWindowPageHide);
  window.removeEventListener("pagehide", onWindowPageHide);
  window.removeEventListener("beforeunload", onWindowPageHide);
  document.removeEventListener("visibilitychange", onDocumentVisibilityChange);
  clearTimeout(copyTimer);
  clearDriveModalTimers();
  disposed = true;
  disconnectOutputCardObserver();
  outputCardElements.clear();
  clearInlineAutoplay();
  pauseParamDrawerMedia();
  pauseOutputsDrawerMedia();
  abortAllOutputFetches();
  Object.values(blobUrls.value).forEach((url) => URL.revokeObjectURL(url));
  streamUrls.value = {};
  streamFallbackToBlob.value = {};
  bufferingStreams.value = {};
  revokeAllVoicePreviewUrls();
  revokeAllLocalPreviews();
  emit("drawer-state", { open: false, width: 0, rootSelector: "" });
});

defineExpose({
  openOutputsDrawer,
  closeOutputsDrawer,
  outputsCount: computed(() => groupedOutputs.value.length),
  hasActiveRun: computed(() =>
    Boolean(comfyRunTask.value || submittingRun.value),
  ),
});
</script>

<template>
  <div class="co-comfy-page" :class="{ 'is-share-mode': isShareMode }">
    <div v-if="!isShareMode" class="co-page-header">
      <div>
        <h1 class="co-page-title">ComfyUI</h1>
        <p class="co-page-subtitle">
          {{ t("comfy.subtitle") }}
        </p>
      </div>
    </div>

    <!-- Top Engine Status Card -->
    <ACard
      v-if="!isShareMode"
      class="co-comfy-engine-card"
      role="region"
      :bordered="true"
      :aria-label="t('comfy.engineStatusAria')"
    >
      <div class="co-comfy-engine-head">
        <div class="co-comfy-engine-icon" aria-hidden="true">
          <PhTreeStructure :size="22" />
        </div>

        <div class="co-comfy-engine-meta">
          <div class="co-comfy-engine-title-row">
            <h2>{{ t("comfy.engineTitle") }}</h2>
            <template v-if="props.loading">
              <ColabOneSkeleton :width="72" :height="22" variant="pill" inline />
              <ColabOneSkeleton
                :width="64"
                :height="22"
                variant="tag"
                tone="subtle"
                :delay="60"
                inline
              />
            </template>
            <template v-else>
              <ATag :color="engineStatus.color" class="co-comfy-status-tag co-sk-reveal">
                <ABadge :status="engineStatus.badge" />
                <span>{{ engineStatus.label }}</span>
              </ATag>
              <ATag class="co-comfy-mono co-sk-reveal">{{ comfyTag }}</ATag>
              <ATag
                v-if="comfyRunning"
                :color="queueBusy ? 'processing' : 'default'"
                class="co-comfy-queue-tag co-sk-reveal"
              >
                {{ t("comfy.queueCounts", { running: queueRunning, pending: queuePending }) }}
              </ATag>
            </template>
          </div>

          <p class="co-comfy-engine-sub">
            <ColabOneSkeleton
              v-if="props.loading"
              width="66%"
              :height="14"
              variant="text"
              tone="subtle"
              :delay="85"
            />
            <template v-else-if="!gpuReady">
              {{
                gpuStarting
                  ? t("comfy.gpuStartingDesc")
                  : t("comfy.gpuStoppedDesc")
              }}
            </template>
            <template v-else-if="comfyRunning">
              {{
                props.state.settings.release_mode === "never"
                  ? t("comfy.runningReleaseNeverHint")
                  : props.state.settings.release_mode === "cu_limit"
                    ? t("comfy.runningReleaseCuLimitHint", {
                        limit: formatColabOneCULimitValue(
                          props.state.settings.release_cu_limit,
                        ),
                      })
                    : t("comfy.runningIdleHint", {
                        policy: formatIdlePolicyLabel(
                          props.state.settings.idle_minutes,
                        ),
                      })
              }}
            </template>
            <template v-else-if="comfyStarting">
              {{ t("comfy.preparingDesc") }}
            </template>
            <template v-else-if="comfyRunTask">
              {{
                t("comfy.nativeRunActiveDesc", {
                  name: runningWorkflowName,
                  policy: formatIdlePolicyLabel(
                    props.state.settings.idle_minutes,
                    props.state.settings.release_mode,
                    props.state.settings.release_cu_limit,
                  ),
                })
              }}
            </template>
            <template v-else>
              {{ t("comfy.readyToStartDesc") }}
            </template>
          </p>
        </div>

        <div class="co-comfy-engine-actions">
          <ColabOneSkeleton
            v-if="props.loading"
            :width="116"
            :height="32"
            variant="button"
            :delay="105"
          />
          <ASpace v-else wrap :size="8">
            <template v-if="comfyRunning">
              <AButton
                :disabled="engineActionBlocked"
                @click="freeComfyVram"
              >
                <template #icon><PhBroom :size="15" /></template>
                {{ t("comfy.freeVram") }}
              </AButton>
              <AButton
                danger
                :disabled="engineActionBlocked"
                @click="stopComfy"
              >
                <template #icon><PhStop :size="14" weight="fill" /></template>
                {{ t("comfy.stopComfy") }}
              </AButton>
            </template>
            <template v-else>
              <ColabOneTooltip
                :title="
                  !gpuReady
                    ? gpuStarting
                      ? t('api.gpuStartingTooltip')
                      : t('api.startGpuFirstTooltip')
                    : engineActionBlocked && !comfyStarting
                      ? t('comfy.runBlockedBusy')
                      : ''
                "
              >
                <AButton
                  type="primary"
                  :loading="comfyStarting"
                  :disabled="!gpuReady || engineActionBlocked || comfyStarting"
                  @click="startComfy"
                >
                  <template #icon><PhPlay :size="14" weight="fill" /></template>
                  {{ comfyStarting ? t("comfy.preparingBtn") : t("comfy.startComfy") }}
                </AButton>
              </ColabOneTooltip>
              <AButton
                v-if="!gpuReady"
                :type="gpuStarting ? 'default' : 'primary'"
                @click="emit('device')"
              >
                <template #icon><PhHardDrives :size="15" /></template>
                {{ gpuStarting ? t("api.viewGpuProgress") : t("api.goStartGpu") }}
              </AButton>
            </template>
          </ASpace>
        </div>
      </div>

      <!-- Notice when a GGUF LLM is currently loaded in VRAM -->
      <AAlert
        v-if="gpuReady && !comfyRunning && !comfyStarting && hasLoadedLLM"
        type="info"
        show-icon
        class="co-comfy-card-alert"
        :message="llmUnloadNotice"
      />

      <!-- Engine error banner -->
      <AAlert
        v-if="comfy?.error"
        type="error"
        show-icon
        class="co-comfy-card-alert"
      >
        <template #message>
          <ColabOnePrivacyText
            :text="tb(comfy.error)"
            :context="privacyContext"
          />
        </template>
      </AAlert>

      <!-- Active ComfyUI task progress bar -->
      <div v-if="comfyTask" class="co-comfy-task-banner" role="status">
        <div class="co-comfy-task-info">
          <div class="co-comfy-task-head">
            <div class="co-comfy-task-head-main">
              <ATag
                v-if="comfyTtsPreviewTask"
                color="blue"
                class="co-comfy-meta-tag"
              >
                {{ t("comfy.taskKindTtsPreview") }}
              </ATag>
              <strong>{{
                comfyTask.message
                  ? tb(comfyTask.message)
                  : comfyTtsPreviewTask
                    ? t("comfy.taskKindTtsPreview")
                    : t("comfy.taskRunningFallback")
              }}</strong>
            </div>
            <div class="co-comfy-task-metrics">
              <span
                v-if="comfyTaskElapsedText"
                class="co-comfy-task-elapsed co-comfy-mono"
                data-testid="comfy-task-elapsed"
              >
                <PhClock :size="13" aria-hidden="true" />
                <span>{{
                  t("task.elapsedLive", { duration: comfyTaskElapsedText })
                }}</span>
              </span>
              <span class="co-comfy-mono">{{ comfyTaskPercent }}%</span>
            </div>
          </div>
          <div
            v-if="comfyTaskDownloadSummary"
            class="co-comfy-task-download-meta"
          >
            <span class="co-comfy-mono">
              {{ comfyTaskDownloadSummary.primary }}
            </span>
            <span
              v-if="comfyTaskDownloadSummary.etaText"
              class="co-comfy-meta-dot"
              aria-hidden="true"
            >·</span>
            <span
              v-if="comfyTaskDownloadSummary.etaText"
              class="co-comfy-mono"
            >
              {{ comfyTaskDownloadSummary.etaText }}
            </span>
            <span
              v-if="comfyTaskDownloadSummary.filename"
              class="co-comfy-meta-dot"
              aria-hidden="true"
            >·</span>
            <span
              v-if="comfyTaskDownloadSummary.filename"
              class="co-comfy-task-download-file co-comfy-mono"
            >
              {{
                comfyTaskDownloadSummary.fileCounter
                  ? `[${comfyTaskDownloadSummary.fileCounter}] ${comfyTaskDownloadSummary.filename}`
                  : comfyTaskDownloadSummary.filename
              }}
            </span>
          </div>
          <AProgress
            :percent="comfyTaskPercent"
            status="active"
            :show-info="false"
            size="small"
          />
        </div>
        <div class="co-comfy-task-actions">
          <AButton
            v-if="comfyRunTask"
            size="small"
            class="co-comfy-task-outputs-btn"
            @click="openOutputsDrawer"
          >
            <template #icon><PhImage :size="14" /></template>
            {{ t("comfy.viewOutputsShort") }}
          </AButton>
          <AButton
            size="small"
            danger
            :loading="Boolean(comfyTask && cancellingTaskId === comfyTask.id)"
            :disabled="cancellingTaskId !== null"
            @click="cancelActiveTask"
          >
            {{ t("comfy.cancelTask") }}
          </AButton>
        </div>

        <!-- Running Workflow Auto-Release After Cloud Sync Toggle Strip -->
        <div
          v-if="comfyRunTask"
          class="co-comfy-autorelease-strip"
          :class="{
            'is-armed': workflowAutoReleaseEnabled,
            'is-warning': workflowAutoReleaseDriveDisconnected,
          }"
          data-testid="comfy-task-auto-release-strip"
        >
          <div class="co-comfy-autorelease-strip-left">
            <span class="co-comfy-autorelease-heading">
              <PhPower
                :size="15"
                weight="duotone"
                class="co-comfy-autorelease-icon"
                aria-hidden="true"
              />
              <span class="co-comfy-autorelease-label">
                {{ t("comfy.autoReleaseSwitchLabel") }}
              </span>
            </span>
            <ATag
              v-if="workflowAutoReleaseState === 'syncing'"
              color="processing"
              class="co-comfy-meta-tag"
            >
              {{ t("comfy.autoReleaseTagSyncing") }}
            </ATag>
            <ATag
              v-else-if="workflowAutoReleaseState === 'releasing'"
              color="success"
              class="co-comfy-meta-tag"
            >
              {{ t("comfy.autoReleaseTagReleasing") }}
            </ATag>
            <ATag
              v-else-if="workflowAutoReleaseDriveDisconnected"
              color="warning"
              class="co-comfy-meta-tag"
            >
              {{ t("comfy.autoReleaseTagDriveLost") }}
            </ATag>
            <ATag
              v-else-if="workflowAutoReleaseEnabled"
              color="success"
              class="co-comfy-meta-tag"
            >
              {{ t("comfy.autoReleaseTagArmed") }}
            </ATag>
            <span v-else class="co-comfy-autorelease-hint">
              {{
                state.setup.drive
                  ? t("comfy.autoReleaseStripHintReady")
                  : t("comfy.autoReleaseStripHintNeedDrive")
              }}
            </span>
            <AButton
              v-if="workflowAutoReleaseDriveDisconnected"
              type="link"
              size="small"
              class="co-inline-link"
              @click="openDriveSetupModal(null, false, 'auto_release', true)"
            >
              {{ t("comfy.autoReleaseReconnectDrive") }}
            </AButton>
          </div>
          <div class="co-comfy-autorelease-strip-right">
            <ColabOneTooltip :title="autoReleaseSwitchTooltip">
              <span class="co-comfy-autorelease-switch-wrap">
                <ASwitch
                  size="small"
                  :checked="workflowAutoReleaseEnabled"
                  :loading="workflowAutoReleaseBusy"
                  :disabled="!gpuReady || workflowAutoReleaseLocked"
                  data-testid="comfy-task-auto-release-switch"
                  @change="onWorkflowAutoReleaseToggle"
                />
              </span>
            </ColabOneTooltip>
          </div>
        </div>
      </div>

      <!-- Post-workflow auto-release status alert (releasing / sync failure kept alive) -->
      <AAlert
        v-if="workflowAutoReleaseErrorVisible"
        type="warning"
        show-icon
        closable
        class="co-comfy-card-alert"
        data-testid="comfy-auto-release-error-alert"
        @close="dismissAutoReleaseErrorAlert"
      >
        <template #message>
          {{ t("comfy.autoReleaseFailedKeptTitle") }}
        </template>
        <template #description>
          <ColabOnePrivacyText
            :text="tb(workflowAutoRelease?.error || '')"
            :context="privacyContext"
          />
        </template>
      </AAlert>
      <AAlert
        v-else-if="
          gpuReady &&
          !comfyTask &&
          (workflowAutoReleaseState === 'releasing' ||
            workflowAutoReleaseState === 'syncing')
        "
        type="info"
        show-icon
        class="co-comfy-card-alert"
        data-testid="comfy-auto-release-progress-alert"
      >
        <template #message>
          {{
            workflowAutoRelease?.message
              ? tb(workflowAutoRelease.message)
              : t("comfy.autoReleaseReleasingFallback")
          }}
        </template>
      </AAlert>
    </ACard>

    <!-- Guest Shared Workflow Workspace -->
    <div v-if="isShareMode" class="co-comfy-share-workspace">
      <!-- Active Guest Task Progress Card -->
      <div
        v-if="comfyTask"
        class="co-comfy-share-task-card"
        data-testid="comfy-share-task-card"
      >
        <div class="co-comfy-task-banner" role="status">
          <div class="co-comfy-task-info">
            <div class="co-comfy-task-head">
              <div class="co-comfy-task-head-main">
                <ATag
                  v-if="comfyTtsPreviewTask"
                  color="blue"
                  class="co-comfy-meta-tag"
                >
                  {{ t("comfy.taskKindTtsPreview") }}
                </ATag>
                <strong>{{
                  comfyTask.message
                    ? tb(comfyTask.message)
                    : comfyTtsPreviewTask
                      ? t("comfy.taskKindTtsPreview")
                      : t("comfy.taskRunningFallback")
                }}</strong>
              </div>
              <div class="co-comfy-task-metrics">
                <span
                  v-if="comfyTaskElapsedText"
                  class="co-comfy-task-elapsed co-comfy-mono"
                  data-testid="comfy-task-elapsed"
                >
                  <PhClock :size="13" aria-hidden="true" />
                  <span>{{
                    t("task.elapsedLive", { duration: comfyTaskElapsedText })
                  }}</span>
                </span>
                <span class="co-comfy-mono">{{ comfyTaskPercent }}%</span>
              </div>
            </div>
            <div
              v-if="comfyTaskDownloadSummary"
              class="co-comfy-task-download-meta"
            >
              <span class="co-comfy-mono">
                {{ comfyTaskDownloadSummary.primary }}
              </span>
              <span
                v-if="comfyTaskDownloadSummary.etaText"
                class="co-comfy-meta-dot"
                aria-hidden="true"
              >·</span>
              <span
                v-if="comfyTaskDownloadSummary.etaText"
                class="co-comfy-mono"
              >
                {{ comfyTaskDownloadSummary.etaText }}
              </span>
              <span
                v-if="comfyTaskDownloadSummary.filename"
                class="co-comfy-meta-dot"
                aria-hidden="true"
              >·</span>
              <span
                v-if="comfyTaskDownloadSummary.filename"
                class="co-comfy-task-download-file co-comfy-mono"
              >
                {{
                  comfyTaskDownloadSummary.fileCounter
                    ? `[${comfyTaskDownloadSummary.fileCounter}] ${comfyTaskDownloadSummary.filename}`
                    : comfyTaskDownloadSummary.filename
                }}
              </span>
            </div>
            <AProgress
              :percent="comfyTaskPercent"
              status="active"
              :show-info="false"
              size="small"
            />
          </div>
          <div class="co-comfy-task-actions">
            <AButton
              v-if="comfyRunTask"
              size="small"
              class="co-comfy-task-outputs-btn"
              @click="openOutputsDrawer"
            >
              <template #icon><PhImage :size="14" /></template>
              {{ t("comfy.viewOutputsShort") }}
            </AButton>
            <AButton
              size="small"
              danger
              :loading="Boolean(comfyTask && cancellingTaskId === comfyTask.id)"
              :disabled="cancellingTaskId !== null"
              @click="cancelActiveTask"
            >
              {{ t("comfy.cancelTask") }}
            </AButton>
          </div>
        </div>
      </div>

      <!-- Initial Cold-Start Skeleton -->
      <div
        v-if="props.loading && !workflows.length"
        class="co-comfy-workflow-list"
        role="status"
        aria-busy="true"
      >
        <span class="co-sr-only">{{ t("common.loading") }}</span>
        <div
          v-for="idx in 2"
          :key="idx"
          class="co-comfy-workflow-item co-comfy-wf-skeleton"
        >
          <div class="co-comfy-wf-card-head">
            <ColabOneSkeleton
              :width="40"
              :height="40"
              :radius="11"
              variant="rect"
              tone="subtle"
              :delay="(idx - 1) * 70"
            />
            <div class="co-comfy-wf-card-heading">
              <ColabOneSkeleton
                width="60%"
                :height="16"
                variant="text"
                :delay="(idx - 1) * 70 + 35"
              />
              <ColabOneSkeleton
                :width="42"
                :height="20"
                variant="tag"
                tone="subtle"
                :delay="(idx - 1) * 70 + 65"
              />
            </div>
          </div>
          <div class="co-comfy-wf-sk-desc">
            <ColabOneSkeleton
              width="92%"
              :height="13"
              variant="text"
              tone="subtle"
              :delay="(idx - 1) * 70 + 90"
            />
            <ColabOneSkeleton
              width="68%"
              :height="13"
              variant="text"
              tone="subtle"
              :delay="(idx - 1) * 70 + 115"
            />
          </div>
          <div class="co-comfy-wf-card-footer">
            <ColabOneSkeleton
              :width="128"
              :height="13"
              variant="text"
              tone="subtle"
              :delay="(idx - 1) * 70 + 140"
            />
            <ColabOneSkeleton
              :width="76"
              :height="14"
              variant="text"
              tone="subtle"
              :delay="(idx - 1) * 70 + 165"
            />
          </div>
        </div>
      </div>

      <!-- Empty state in share mode -->
      <ACard
        v-else-if="!workflows.length"
        class="co-comfy-card co-comfy-empty-card"
        data-testid="comfy-share-empty-workflows"
      >
        <AEmpty :description="t('workflowShare.guestEmptyWorkflows')">
          <template #image>
            <div class="co-comfy-empty-emblem">
              <PhTreeStructure :size="30" weight="duotone" />
            </div>
          </template>
        </AEmpty>
      </ACard>

      <!-- Populated Guest Workflow Card Grid -->
      <div
        v-else
        class="co-comfy-workflow-list co-sk-reveal"
        role="list"
        :aria-label="t('comfy.selectWorkflowAria')"
      >
        <div
          v-for="entry in studioEntries"
          :key="entry.key"
          role="listitem"
          class="co-comfy-workflow-item"
          :class="{
            'is-selected':
              paramDrawerOpen &&
              entry.workflows.some((w) => w.id === selectedWorkflowId),
            'is-disabled': submittingRun || hasUploadingField,
          }"
          :data-workflow-entry-key="
            entry.isGroup ? COLABONE_DIGITAL_HUMAN_GROUP_KEY : entry.key
          "
        >
          <button
            type="button"
            class="co-comfy-wf-card-trigger"
            :disabled="submittingRun || hasUploadingField"
            :aria-expanded="
              paramDrawerOpen &&
              entry.workflows.some((w) => w.id === selectedWorkflowId)
            "
            @click="openStudioEntryDrawer(entry)"
          >
            <div class="co-comfy-wf-card-head">
              <div
                class="co-comfy-wf-emblem"
                :class="{
                  'is-pipeline':
                    entry.primary.pipeline?.kind === 'digital_human' ||
                    entry.primary.pipeline?.kind === 'fashion_tryon',
                }"
                aria-hidden="true"
              >
                <PhVideoCamera
                  v-if="entry.primary.pipeline?.kind === 'digital_human'"
                  :size="20"
                  weight="duotone"
                />
                <PhTShirt
                  v-else-if="entry.primary.pipeline?.kind === 'fashion_tryon'"
                  :size="20"
                  weight="duotone"
                />
                <PhTreeStructure v-else :size="20" weight="duotone" />
              </div>

              <div class="co-comfy-wf-card-heading">
                <strong class="co-comfy-wf-card-title">{{
                  entry.isGroup
                    ? t("comfy.digitalHumanUnifiedName")
                    : tb(entry.primary.name)
                }}</strong>
                <ATag
                  :color="
                    entry.isGroup || entry.primary.source === 'builtin'
                      ? 'blue'
                      : 'default'
                  "
                  :bordered="false"
                  class="co-comfy-source-tag"
                >
                  {{
                    entry.isGroup || entry.primary.source === "builtin"
                      ? t("comfy.sourceBuiltin")
                      : t("comfy.sourceCustom")
                  }}
                </ATag>
              </div>
            </div>

            <p
              v-if="entry.isGroup || entry.primary.description"
              class="co-comfy-workflow-desc"
            >
              {{
                entry.isGroup
                  ? t("comfy.digitalHumanUnifiedDesc")
                  : tb(entry.primary.description)
              }}
            </p>

            <div
              v-if="entry.primary.pipeline?.kind === 'digital_human'"
              class="co-comfy-wf-io-strip"
            >
              <div class="co-comfy-wf-io-row">
                {{ t("comfy.ioInputDigitalHuman") }}
              </div>
              <div class="co-comfy-wf-io-row">
                {{ t("comfy.ioOutputDigitalHuman") }}
              </div>
            </div>
            <div
              v-else-if="entry.primary.pipeline?.kind === 'fashion_tryon'"
              class="co-comfy-wf-io-strip"
            >
              <div class="co-comfy-wf-io-row">
                {{ t("comfy.ioInputFashionTryOn") }}
              </div>
              <div class="co-comfy-wf-io-row">
                {{ t("comfy.ioOutputFashionTryOn") }}
              </div>
            </div>

            <div class="co-comfy-wf-card-footer">
              <div class="co-comfy-wf-specs">
                <span class="co-comfy-wf-spec-text">
                  {{ formatStudioEntrySpecs(entry) }}
                </span>
                <ColabOneTooltip
                  v-if="entry.workflows.some((w) => isWorkflowNonCommercial(w))"
                  :title="
                    entry.workflows
                      .filter((w) => isWorkflowNonCommercial(w))
                      .map((w) => workflowNonCommercialTooltip(w))
                      .join(' ') || t('comfy.licenseNonCommercial')
                  "
                >
                  <ATag
                    color="orange"
                    :bordered="false"
                    class="co-comfy-noncommercial-pill"
                  >
                    {{ t("comfy.licenseNonCommercial") }}
                  </ATag>
                </ColabOneTooltip>
              </div>
              <span class="co-comfy-wf-cta" aria-hidden="true">
                <PhSlidersHorizontal :size="14" />
                <span>{{ t("comfy.configureAndRun") }}</span>
              </span>
            </div>
          </button>
        </div>
      </div>
    </div>

    <!-- Dual-layer Workspace Tabs -->
    <div v-else ref="tabsWrapRef" class="co-comfy-tabs-wrap">
      <ATabs
        :active-key="activeTab"
        class="co-comfy-tabs"
        @update:active-key="onTabChange"
      >
        <!-- TAB 1: Workflow Studio -->
        <ATabPane key="studio">
          <template #tab>
            <span class="co-comfy-tab-label">
              <PhSlidersHorizontal :size="16" />
              <span>{{ t("comfy.tabStudio") }}</span>
              <ABadge
                v-if="studioEntries.length"
                :count="studioEntries.length"
                class="co-comfy-tab-badge"
              />
            </span>
          </template>

          <section class="co-comfy-studio-section" :aria-label="t('comfy.tabStudio')">
            <!-- Studio Action Bar -->
            <div class="co-comfy-studio-toolbar">
              <div class="co-comfy-studio-toolbar-copy">
                <div class="co-comfy-toolbar-title-row">
                  <h2>{{ t("comfy.tabStudio") }}</h2>
                  <ColabOneSkeleton
                    v-if="props.loading"
                    :width="62"
                    :height="22"
                    variant="tag"
                    tone="subtle"
                    inline
                  />
                  <ATag v-else class="co-comfy-count-tag co-sk-reveal">
                    {{
                      studioEntries.length === 1
                        ? t("comfy.studioWorkflowCountOne", { count: 1 })
                        : t("comfy.studioWorkflowCount", {
                            count: studioEntries.length,
                          })
                    }}
                  </ATag>
                </div>
                <p>{{ t("comfy.studioDesc") }}</p>
              </div>
              <ASpace :size="10" wrap class="co-comfy-studio-toolbar-actions">
                <AButton
                  v-if="props.canShareWorkflows"
                  class="co-comfy-share-trigger"
                  :class="{ 'is-active': (props.activeShareCount || 0) > 0 }"
                  data-testid="comfy-share-workflows-btn"
                  @click="emit('openShareModal')"
                >
                  <template #icon><PhShareNetwork :size="15" /></template>
                  <span>{{ t("comfy.shareWorkflowBtn") }}</span>
                  <span
                    v-if="(props.activeShareCount || 0) > 0"
                    class="co-comfy-share-count co-comfy-mono"
                  >
                    {{ props.activeShareCount }}
                  </span>
                </AButton>
                <AButton
                  class="co-comfy-outputs-trigger"
                  :class="{
                    'is-active': outputsDrawerOpen,
                    'is-running': !!(comfyRunTask || submittingRun),
                  }"
                  @click="openOutputsDrawer"
                >
                  <template #icon><PhImage :size="15" /></template>
                  <span>{{ t("comfy.openOutputsBtn") }}</span>
                  <ABadge
                    v-if="comfyRunTask || submittingRun"
                    status="processing"
                    class="co-comfy-outputs-pulse"
                  />
                  <span
                    v-if="groupedOutputs.length > 0"
                    class="co-comfy-outputs-count co-comfy-mono"
                  >
                    {{ groupedOutputs.length }}
                  </span>
                </AButton>
                <AButton
                  type="primary"
                  @click="openImportModal"
                >
                  <template #icon><PhPlus :size="15" /></template>
                  {{ t("comfy.importBtn") }}
                </AButton>
              </ASpace>
            </div>

            <!-- Initial Cold-Start Skeleton -->
            <div
              v-if="props.loading && !workflows.length"
              class="co-comfy-workflow-list"
              role="status"
              aria-busy="true"
            >
              <span class="co-sr-only">{{ t("common.loading") }}</span>
              <div class="co-comfy-workflow-item co-comfy-wf-skeleton">
                <div class="co-comfy-wf-card-head">
                  <ColabOneSkeleton
                    :width="40"
                    :height="40"
                    :radius="11"
                    variant="rect"
                    tone="subtle"
                  />
                  <div class="co-comfy-wf-card-heading">
                    <ColabOneSkeleton
                      width="60%"
                      :height="16"
                      variant="text"
                      :delay="35"
                    />
                    <ColabOneSkeleton
                      :width="42"
                      :height="20"
                      variant="tag"
                      tone="subtle"
                      :delay="65"
                    />
                  </div>
                </div>
                <div class="co-comfy-wf-sk-desc">
                  <ColabOneSkeleton
                    width="92%"
                    :height="13"
                    variant="text"
                    tone="subtle"
                    :delay="90"
                  />
                  <ColabOneSkeleton
                    width="68%"
                    :height="13"
                    variant="text"
                    tone="subtle"
                    :delay="115"
                  />
                </div>
                <div class="co-comfy-wf-card-footer">
                  <ColabOneSkeleton
                    :width="128"
                    :height="13"
                    variant="text"
                    tone="subtle"
                    :delay="140"
                  />
                  <ColabOneSkeleton
                    :width="76"
                    :height="14"
                    variant="text"
                    tone="subtle"
                    :delay="165"
                  />
                </div>
              </div>
            </div>

            <!-- Empty state when no workflows exist -->
            <ACard
              v-else-if="!workflows.length"
              class="co-comfy-card co-comfy-empty-card"
            >
              <AEmpty :description="t('comfy.emptyWorkflows')">
                <template #image>
                  <div class="co-comfy-empty-emblem">
                    <PhTreeStructure :size="30" weight="duotone" />
                  </div>
                </template>
                <p class="co-comfy-empty-copy">
                  {{ t("comfy.emptyWorkflowsDesc") }}
                </p>
                <ASpace wrap :size="10" class="co-comfy-empty-actions">
                  <AButton
                    type="primary"
                    @click="openImportModal"
                  >
                    <template #icon><PhUploadSimple :size="15" /></template>
                    {{ t("comfy.importBtnShort") }}
                  </AButton>
                  <AButton
                    @click="activeTab = 'canvas'"
                  >
                    <template #icon><PhGraph :size="15" /></template>
                    {{ t("comfy.goCanvasBuild") }}
                  </AButton>
                </ASpace>
              </AEmpty>
            </ACard>

            <!-- Populated Workflow Card Grid -->
            <div
              v-else
              class="co-comfy-workflow-list co-sk-reveal"
              role="list"
              :aria-label="t('comfy.selectWorkflowAria')"
            >
              <div
                v-for="entry in studioEntries"
                :key="entry.key"
                role="listitem"
                class="co-comfy-workflow-item"
                :class="{
                  'is-selected':
                    paramDrawerOpen &&
                    entry.workflows.some((w) => w.id === selectedWorkflowId),
                  'is-disabled': submittingRun || hasUploadingField,
                  'has-actions':
                    !entry.isGroup && entry.primary.source === 'custom',
                }"
                :data-workflow-entry-key="
                  entry.isGroup ? COLABONE_DIGITAL_HUMAN_GROUP_KEY : entry.key
                "
              >
                <button
                  type="button"
                  class="co-comfy-wf-card-trigger"
                  :disabled="submittingRun || hasUploadingField"
                  :aria-expanded="
                    paramDrawerOpen &&
                    entry.workflows.some((w) => w.id === selectedWorkflowId)
                  "
                  @click="openStudioEntryDrawer(entry)"
                >
                  <div class="co-comfy-wf-card-head">
                    <div
                      class="co-comfy-wf-emblem"
                      :class="{
                        'is-pipeline':
                          entry.primary.pipeline?.kind === 'digital_human' ||
                          entry.primary.pipeline?.kind === 'fashion_tryon',
                      }"
                      aria-hidden="true"
                    >
                      <PhVideoCamera
                        v-if="entry.primary.pipeline?.kind === 'digital_human'"
                        :size="20"
                        weight="duotone"
                      />
                      <PhTShirt
                        v-else-if="
                          entry.primary.pipeline?.kind === 'fashion_tryon'
                        "
                        :size="20"
                        weight="duotone"
                      />
                      <PhTreeStructure v-else :size="20" weight="duotone" />
                    </div>

                    <div class="co-comfy-wf-card-heading">
                      <strong class="co-comfy-wf-card-title">{{
                        entry.isGroup
                          ? t("comfy.digitalHumanUnifiedName")
                          : tb(entry.primary.name)
                      }}</strong>
                      <ATag
                        :color="
                          entry.isGroup || entry.primary.source === 'builtin'
                            ? 'blue'
                            : 'default'
                        "
                        :bordered="false"
                        class="co-comfy-source-tag"
                      >
                        {{
                          entry.isGroup || entry.primary.source === "builtin"
                            ? t("comfy.sourceBuiltin")
                            : t("comfy.sourceCustom")
                        }}
                      </ATag>
                    </div>
                  </div>

                  <p
                    v-if="entry.isGroup || entry.primary.description"
                    class="co-comfy-workflow-desc"
                  >
                    {{
                      entry.isGroup
                        ? t("comfy.digitalHumanUnifiedDesc")
                        : tb(entry.primary.description)
                    }}
                  </p>

                  <div
                    v-if="entry.primary.pipeline?.kind === 'digital_human'"
                    class="co-comfy-wf-io-strip"
                  >
                    <div class="co-comfy-wf-io-row">
                      {{ t("comfy.ioInputDigitalHuman") }}
                    </div>
                    <div class="co-comfy-wf-io-row">
                      {{ t("comfy.ioOutputDigitalHuman") }}
                    </div>
                  </div>
                  <div
                    v-else-if="entry.primary.pipeline?.kind === 'fashion_tryon'"
                    class="co-comfy-wf-io-strip"
                  >
                    <div class="co-comfy-wf-io-row">
                      {{ t("comfy.ioInputFashionTryOn") }}
                    </div>
                    <div class="co-comfy-wf-io-row">
                      {{ t("comfy.ioOutputFashionTryOn") }}
                    </div>
                  </div>

                  <div class="co-comfy-wf-card-footer">
                    <div class="co-comfy-wf-specs">
                      <span class="co-comfy-wf-spec-text">
                        {{ formatStudioEntrySpecs(entry) }}
                      </span>
                      <ColabOneTooltip
                        v-if="
                          entry.workflows.some((w) => isWorkflowNonCommercial(w))
                        "
                        :title="
                          entry.workflows
                            .filter((w) => isWorkflowNonCommercial(w))
                            .map((w) => workflowNonCommercialTooltip(w))
                            .join(' ') || t('comfy.licenseNonCommercial')
                        "
                      >
                        <ATag
                          color="orange"
                          :bordered="false"
                          class="co-comfy-noncommercial-pill"
                        >
                          {{ t("comfy.licenseNonCommercial") }}
                        </ATag>
                      </ColabOneTooltip>
                    </div>
                    <span class="co-comfy-wf-cta" aria-hidden="true">
                      <PhSlidersHorizontal :size="14" />
                      <span>{{ t("comfy.configureAndRun") }}</span>
                    </span>
                  </div>
                </button>

                <div
                  v-if="!entry.isGroup && entry.primary.source === 'custom'"
                  class="co-comfy-wf-actions"
                >
                  <APopconfirm
                    :open="deleteConfirmWorkflowId === entry.primary.id"
                    :title="
                      t('comfy.deleteWorkflowTitle', {
                        name: tb(entry.primary.name),
                      })
                    "
                    :description="t('comfy.deleteWorkflowDesc')"
                    :ok-text="t('common.confirmDelete')"
                    :cancel-text="t('common.cancel')"
                    :disabled="busy"
                    :ok-button-props="{ danger: true }"
                    placement="topRight"
                    @open-change="
                      (open) => {
                        if (open) {
                          deleteConfirmWorkflowId = entry.primary.id;
                        } else if (
                          deleteConfirmWorkflowId === entry.primary.id
                        ) {
                          deleteConfirmWorkflowId = '';
                        }
                      }
                    "
                    @confirm="
                      deleteConfirmWorkflowId = '';
                      deleteWorkflow(entry.primary);
                    "
                  >
                    <span class="co-popconfirm-anchor">
                      <ColabOneTooltip
                        :title="t('comfy.deleteWorkflowTooltip')"
                        :disabled="
                          deleteConfirmWorkflowId === entry.primary.id
                        "
                      >
                        <AButton
                          type="text"
                          size="small"
                          danger
                          class="co-comfy-wf-delete-btn"
                          :aria-label="t('comfy.deleteWorkflowTooltip')"
                          :disabled="busy"
                        >
                          <template #icon>
                            <PhTrash :size="15" />
                          </template>
                        </AButton>
                      </ColabOneTooltip>
                    </span>
                  </APopconfirm>
                </div>
              </div>
            </div>
          </section>
        </ATabPane>

        <!-- TAB 2: Live Studio -->
        <ATabPane key="live">
          <template #tab>
            <span class="co-comfy-tab-label">
              <PhBroadcast :size="16" />
              <span>{{ t("comfy.tabLive") }}</span>
              <ABadge
                v-if="liveStudioRunning"
                status="success"
                :aria-label="t('comfy.liveActiveAria')"
              />
            </span>
          </template>

          <LiveStudioPanel
            :state="state"
            :loading="loading"
            :active="activeTab === 'live'"
            @notify="(msg, kind) => emit('notify', msg, kind)"
            @error="(msg) => emit('error', msg)"
            @clearError="emit('clearError')"
            @sync="emit('sync')"
            @upgrade="emit('upgrade')"
            @open-canvas-tab="activeTab = 'canvas'"
          />
        </ATabPane>

        <!-- TAB 3: Native Canvas -->
        <ATabPane key="canvas">
          <template #tab>
            <span class="co-comfy-tab-label">
              <PhGraph :size="16" />
              <span>{{ t("comfy.tabCanvas") }}</span>
              <ABadge
                v-if="canvasReady"
                status="success"
                :aria-label="t('comfy.canvasActiveAria')"
              />
            </span>
          </template>

          <section class="co-comfy-canvas-section" :aria-label="t('comfy.canvasSectionAria')">
            <!-- Security & Idle-Protection Banner -->
            <AAlert type="info" show-icon class="co-comfy-canvas-security-alert">
              <template #icon>
                <PhShieldCheck :size="20" />
              </template>
              <template #message>
                <div class="co-comfy-canvas-banner-head">
                  <h2>{{ t("comfy.canvasHeading") }}</h2>
                  <ATag :color="canvasTagColor" class="co-comfy-status-tag">
                    <ABadge :status="canvasBadgeStatus" />
                    <span>{{ canvasStatusLabel }}</span>
                  </ATag>
                </div>
              </template>
              <template #description>
                {{ t("comfy.canvasDesc") }}
              </template>
            </AAlert>

            <AAlert
              v-if="canvas?.error"
              type="error"
              show-icon
            >
              <template #message>
                <ColabOnePrivacyText
                  :text="tb(canvas.error)"
                  :context="privacyContext"
                />
              </template>
            </AAlert>

            <div class="co-comfy-canvas-grid">
              <!-- Fixed-Domain Tunnel & Lifecycle Card -->
              <ACard class="co-comfy-card" :title="t('comfy.canvasConfigTitle')">
                <div class="co-comfy-named-fields">
                  <p class="co-comfy-canvas-help">
                    {{ t("comfy.canvasConfigDesc") }}
                  </p>

                  <div class="co-comfy-field-control">
                    <label
                      for="comfy-canvas-domain"
                      class="co-comfy-field-label-row"
                    >
                      <span>{{ t("comfy.canvasDomainLabel") }}</span>
                    </label>
                    <ColabOnePrivacyInput
                      id="comfy-canvas-domain"
                      :value="canvasDomain"
                      :disabled="canvasEnabled || busy"
                      placeholder="comfy.example.com"
                      @update:value="onCanvasDomainUpdate"
                    />
                  </div>

                  <div class="co-comfy-field-control">
                    <label
                      for="comfy-canvas-token"
                      class="co-comfy-field-label-row"
                    >
                      <span>{{ t("api.tunnelTokenLabel") }}</span>
                    </label>
                    <ColabOneSecretInput
                      ref="canvasTokenInputRef"
                      id="comfy-canvas-token"
                      v-model:value="canvasTunnelToken"
                      :has-saved="!!state.comfy_canvas_settings?.has_token"
                      :disabled="canvasEnabled || busy"
                      :placeholder="t('comfy.canvasTokenEmptyPlaceholder')"
                      :saved-placeholder="t('api.tunnelTokenSavedPlaceholder')"
                      :load-saved="
                        colabOneRuntimeCapabilities.can_reveal_host_secrets
                          ? loadSavedCanvasToken
                          : undefined
                      "
                      :extract-secret="colabOneExtractTunnelToken"
                      :copyable="true"
                      :copy-label="t('api.copyTunnelTokenTooltip')"
                      @extracted="onCanvasTokenExtracted"
                      @copied="onCanvasTokenCopied"
                      @error="reportError($event)"
                    />
                  </div>

                  <details class="co-comfy-canvas-guide">
                    <summary>{{ t("api.guideSummary") }}</summary>
                    <ol>
                      <li>
                        {{ t("comfy.canvasGuideStep1") }}
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link co-comfy-inline-link"
                          @click="openExternalURL('https://one.dash.cloudflare.com/')"
                        >
                          {{ t("api.openDashboard") }}
                          <PhArrowSquareOut :size="14" />
                        </AButton>
                      </li>
                      <li>
                        {{ t("api.guideStep2Prefix") }}
                        <code>http://127.0.0.1:8189</code>{{ t("comfy.canvasGuideStep2Suffix") }}
                      </li>
                      <li>
                        {{ t("comfy.canvasGuideStep3") }}
                      </li>
                    </ol>
                  </details>
                </div>

                <ADivider class="co-comfy-form-divider" />

                <ASpace wrap :size="10">
                  <ColabOneTooltip
                    v-if="!gpuReady"
                    :title="
                      gpuStarting
                        ? t('api.gpuStartingTooltip')
                        : t('api.startGpuFirstTooltip')
                    "
                  >
                    <AButton type="primary" disabled>
                      <template #icon>
                        <PhPlay :size="14" weight="fill" />
                      </template>
                      {{ t("comfy.startCanvas") }}
                    </AButton>
                  </ColabOneTooltip>
                  <AButton
                    v-else-if="!comfyRunning"
                    type="primary"
                    :loading="comfyStarting"
                    :disabled="engineActionBlocked || comfyStarting"
                    @click="startComfy"
                  >
                    <template #icon>
                      <PhPlay :size="14" weight="fill" />
                    </template>
                    {{
                      comfyStarting ? t("comfy.preparingBtn") : t("comfy.startComfyFirst")
                    }}
                  </AButton>
                  <AButton
                    v-else-if="canvasEnabled"
                    type="primary"
                    danger
                    :disabled="busy || !!state.operation"
                    @click="stopCanvas"
                  >
                    <template #icon>
                      <PhStop :size="14" weight="fill" />
                    </template>
                    {{ t("comfy.stopCanvas") }}
                  </AButton>
                  <AButton
                    v-else
                    type="primary"
                    :disabled="busy || !!state.operation"
                    @click="startCanvas"
                  >
                    <template #icon>
                      <PhPlay :size="14" weight="fill" />
                    </template>
                    {{ t("comfy.startCanvas") }}
                  </AButton>

                  <AButton
                    v-if="!canvasEnabled"
                    :disabled="busy"
                    @click="saveCanvasSettings"
                  >
                    {{ t("api.saveConfig") }}
                  </AButton>
                </ASpace>
              </ACard>

              <!-- Canvas Status & Launch Card -->
              <ACard
                class="co-comfy-card co-comfy-canvas-access"
                :title="t('comfy.canvasAccessTitle')"
              >
                <template #extra>
                  <ABadge
                    :status="canvasBadgeStatus"
                    :text="canvasStatusLabel"
                  />
                </template>

                <!-- Live Status Indicators -->
                <div class="co-comfy-canvas-metrics">
                  <div class="co-comfy-metric-tile">
                    <span>{{ t("comfy.canvasActiveConns") }}</span>
                    <strong class="co-comfy-mono">
                      {{ canvas?.active_connections ?? 0 }}
                    </strong>
                  </div>
                  <div class="co-comfy-metric-tile">
                    <span>{{ t("comfy.canvasQueue") }}</span>
                    <strong class="co-comfy-mono">
                      {{ t("comfy.queueCountsShort", { running: queueRunning, pending: queuePending }) }}
                    </strong>
                  </div>
                </div>

                <!-- Public Host Display (never renders secret auth token) -->
                <div class="co-comfy-host-field">
                  <span class="co-comfy-host-label">{{ t("comfy.canvasPublicHostLabel") }}</span>
                  <div class="co-comfy-host-box">
                    <span
                      v-if="
                        props.loading ||
                        ((canvasState === 'starting' ||
                          canvasState === 'reconnecting') &&
                          !canvasPublicHost)
                      "
                      class="co-comfy-host-sk"
                      role="status"
                      aria-busy="true"
                    >
                      <ColabOneSkeleton
                        width="62%"
                        :height="14"
                        variant="text"
                        :tone="props.loading ? 'default' : 'accent'"
                      />
                      <span class="co-sr-only">{{
                        props.loading ? t("common.loading") : canvasStatusLabel
                      }}</span>
                    </span>
                    <code
                      v-else
                      class="co-comfy-mono co-sk-reveal"
                      data-testid="comfy-canvas-public-host"
                    >
                      <ColabOnePrivacyText
                        v-if="canvasPublicHost"
                        :text="canvasPublicHost"
                        kind="host"
                      />
                      <template v-else>{{
                        t("comfy.canvasPublicHostEmpty")
                      }}</template>
                    </code>
                    <ATag v-if="canvasReady" color="success">HTTPS</ATag>
                  </div>
                </div>

                <!-- Launch Controls -->
                <div v-if="canvasReady" class="co-comfy-canvas-launch-actions co-sk-reveal">
                  <AButton
                    v-if="colabOneRuntimeCapabilities.can_open_native_canvas_window"
                    type="primary"
                    block
                    @click="launchCanvas('window')"
                  >
                    <template #icon><PhAppWindow :size="15" /></template>
                    {{ t("comfy.openInWindow") }}
                  </AButton>
                  <AButton
                    v-else
                    type="primary"
                    block
                    @click="launchCanvas('browser')"
                  >
                    <template #icon><PhBrowsers :size="15" /></template>
                    {{ t("webConsole.webOpenCanvasBrowserOnly") }}
                  </AButton>

                  <div class="co-comfy-canvas-secondary-row">
                    <AButton
                      v-if="colabOneRuntimeCapabilities.can_open_native_canvas_window"
                      block
                      @click="launchCanvas('browser')"
                    >
                      <template #icon><PhBrowsers :size="15" /></template>
                      {{ t("comfy.openInBrowser") }}
                    </AButton>
                    <AButton
                      block
                      @click="copyCanvasAuthUrl"
                    >
                      <template #icon>
                        <PhCheck v-if="copiedCanvasUrl" :size="15" />
                        <PhLink v-else :size="15" />
                      </template>
                      {{ copiedCanvasUrl ? t("comfy.copiedAuthLink") : t("comfy.copyAuthLink") }}
                    </AButton>
                  </div>
                </div>

                <div
                  v-else-if="
                    canvasState === 'starting' || canvasState === 'reconnecting'
                  "
                  class="co-comfy-canvas-connecting"
                  role="status"
                  aria-busy="true"
                >
                  <div class="co-comfy-canvas-connecting-note">
                    <ABadge status="processing" />
                    <span>{{ t("comfy.canvasConnectingHint") }}</span>
                  </div>
                  <div
                    class="co-comfy-canvas-launch-actions co-comfy-canvas-launch-skeleton"
                    aria-hidden="true"
                  >
                    <ColabOneSkeleton
                      width="100%"
                      :height="32"
                      variant="button"
                    />
                    <div class="co-comfy-canvas-secondary-row">
                      <ColabOneSkeleton
                        v-if="colabOneRuntimeCapabilities.can_open_native_canvas_window"
                        width="100%"
                        :height="32"
                        variant="button"
                        tone="subtle"
                        :delay="80"
                      />
                      <ColabOneSkeleton
                        width="100%"
                        :height="32"
                        variant="button"
                        tone="subtle"
                        :delay="140"
                      />
                    </div>
                  </div>
                </div>

                <div v-else class="co-comfy-canvas-waiting">
                  <p>
                    <template v-if="!gpuReady">
                      {{
                        gpuStarting
                          ? t("comfy.canvasGpuStartingHint")
                          : t("comfy.canvasGpuStoppedHint")
                      }}
                      <AButton
                        type="link"
                        size="small"
                        class="co-inline-link co-comfy-inline-link"
                        @click="emit('device')"
                      >
                        {{ gpuStarting ? t("api.viewGpuProgress") : t("comfy.goStartGpuAlt") }}
                      </AButton>
                    </template>
                    <template v-else-if="comfyStarting">
                      {{ t("comfy.preparingDesc") }}
                    </template>
                    <template v-else-if="!comfyRunning">
                      {{ t("comfy.canvasNeedEngineHint") }}
                    </template>
                    <template v-else>
                      {{ t("comfy.canvasReadyToEnableHint") }}
                    </template>
                  </p>
                </div>

                <!-- Dev Mode Save (API Format) Guidance -->
                <AAlert
                  type="info"
                  show-icon
                  class="co-comfy-canvas-dev-tip"
                  :message="t('comfy.canvasExportTipTitle')"
                >
                  <template #icon>
                    <PhFileCode :size="18" />
                  </template>
                  <template #description>
                    <p class="co-comfy-dev-tip-copy">
                      {{ t("comfy.canvasExportTipDesc") }}
                    </p>
                    <AButton
                      type="link"
                      size="small"
                      class="co-inline-link co-comfy-tip-link"
                      @click="
                        activeTab = 'studio';
                        openImportModal();
                      "
                    >
                      {{ t("comfy.importApiJsonNow") }}
                    </AButton>
                  </template>
                </AAlert>
              </ACard>
            </div>
          </section>
        </ATabPane>
      </ATabs>
    </div>

    <!-- Drawer 1: Workflow Parameter Configuration Drawer -->
    <ADrawer
      :open="paramDrawerOpen && !!selectedWorkflow"
      placement="right"
      :width="paramDrawerWidth"
      :mask="true"
      :mask-closable="true"
      :keyboard="false"
      :destroy-on-close="false"
      :z-index="1010"
      :header-style="{ padding: '14px 20px' }"
      :body-style="{ padding: '16px 20px 24px', overflowY: 'auto' }"
      :footer-style="{ padding: '12px 20px' }"
      root-class-name="co-comfy-param-drawer-root"
      @update:open="
        (val) => {
          if (!val) closeParamDrawer();
        }
      "
    >
      <template #title>
        <div v-if="selectedWorkflow" class="co-comfy-drawer-title">
          <span
            class="co-comfy-drawer-title-icon"
            :class="{
              'is-pipeline': isDigitalHumanWorkflow || isFashionTryOnWorkflow,
            }"
            aria-hidden="true"
          >
            <PhVideoCamera
              v-if="isDigitalHumanWorkflow"
              :size="16"
              weight="duotone"
            />
            <PhTShirt
              v-else-if="isFashionTryOnWorkflow"
              :size="16"
              weight="duotone"
            />
            <PhTreeStructure v-else :size="16" weight="duotone" />
          </span>
          <span class="co-comfy-drawer-title-text">
            {{
              activeDigitalHumanGroup
                ? t("comfy.digitalHumanUnifiedName")
                : tb(selectedWorkflow.name)
            }}
          </span>
          <ATag
            :color="selectedWorkflow.source === 'builtin' ? 'blue' : 'default'"
            :bordered="false"
            class="co-comfy-source-tag"
          >
            {{
              selectedWorkflow.source === "builtin"
                ? t("comfy.sourceBuiltin")
                : t("comfy.sourceCustom")
            }}
          </ATag>
        </div>
      </template>

      <template #extra>
        <AButton
          v-if="selectedWorkflow && selectedWorkflow.fields.length"
          type="text"
          size="small"
          class="co-comfy-reset-btn"
          :disabled="submittingRun || hasUploadingField"
          @click="resetFields"
        >
          <template #icon><PhArrowsClockwise :size="14" /></template>
          {{ t("comfy.resetDefaults") }}
        </AButton>
      </template>

      <div
        v-if="selectedWorkflow"
        class="co-comfy-param-drawer-body"
        role="region"
        :aria-label="t('comfy.paramDrawerAria')"
      >
        <!-- Clean Beginner Guide Strip -->
        <div v-if="isDigitalHumanWorkflow" class="co-comfy-beginner-guide">
          <div class="co-comfy-beginner-guide-io">
            <div class="co-comfy-beginner-guide-io-row">
              {{ t("comfy.ioInputDigitalHuman") }}
            </div>
            <div class="co-comfy-beginner-guide-io-row">
              {{ t("comfy.ioOutputDigitalHuman") }}
            </div>
          </div>
          <span class="co-comfy-beginner-guide-hint">
            {{ t("comfy.beginnerPresetHint") }}
          </span>
        </div>
        <div
          v-else-if="isFashionTryOnWorkflow"
          class="co-comfy-beginner-guide"
        >
          <div class="co-comfy-beginner-guide-io">
            <div class="co-comfy-beginner-guide-io-row">
              {{ t("comfy.ioInputFashionTryOn") }}
            </div>
            <div class="co-comfy-beginner-guide-io-row">
              {{ t("comfy.ioOutputFashionTryOn") }}
            </div>
          </div>
          <span class="co-comfy-beginner-guide-hint">
            {{ t("comfy.beginnerFashionPresetHint") }}
          </span>
        </div>
        <p
          v-else-if="selectedWorkflow.description"
          class="co-comfy-drawer-desc"
        >
          {{ tb(selectedWorkflow.description) }}
        </p>

        <!-- Dual-Option Video Engine Switcher (Built-in Digital Human) -->
        <div
          v-if="
            activeDigitalHumanGroup &&
            activeDigitalHumanGroup.workflows.length >= 2
          "
          class="co-comfy-engine-switcher"
          data-testid="comfy-digital-human-engine-switcher"
        >
          <div class="co-comfy-engine-switcher-head">
            <span
              id="co-comfy-engine-switcher-label"
              class="co-comfy-engine-switcher-label"
            >
              {{ t("comfy.videoEngineLabel") }}
            </span>
            <span
              v-if="digitalHumanGpuContext.shortLabel"
              id="co-comfy-engine-switcher-hint"
              class="co-comfy-engine-gpu-hint"
            >
              {{
                t("comfy.videoEngineGpuAutoHint", {
                  gpu: digitalHumanGpuContext.shortLabel,
                })
              }}
            </span>
          </div>
          <div
            class="co-comfy-engine-options"
            role="radiogroup"
            aria-labelledby="co-comfy-engine-switcher-label"
            :aria-describedby="
              digitalHumanGpuContext.shortLabel
                ? 'co-comfy-engine-switcher-hint'
                : undefined
            "
            :aria-disabled="submittingRun || hasUploadingField || undefined"
          >
            <button
              v-for="(engineWf, engineIdx) in activeDigitalHumanGroup.workflows"
              :key="engineWf.id"
              type="button"
              role="radio"
              class="co-comfy-engine-option"
              :class="{
                'is-selected': engineWf.id === selectedWorkflowId,
                'is-recommended':
                  isDigitalHumanEngineRecommendedBadgeShown(engineWf),
                'is-vram-short': isDigitalHumanEngineVramShort(engineWf),
              }"
              :tabindex="
                engineWf.id === selectedWorkflowId ||
                (!activeDigitalHumanGroup.workflows.some(
                  (w) => w.id === selectedWorkflowId,
                ) &&
                  engineIdx === 0)
                  ? 0
                  : -1
              "
              :aria-checked="engineWf.id === selectedWorkflowId"
              :data-engine-id="engineWf.id"
              :data-testid="'comfy-engine-option-' + engineWf.id"
              :disabled="submittingRun || hasUploadingField"
              @click="switchDigitalHumanEngine(engineWf.id)"
              @keydown="onDigitalHumanEngineKeydown($event, engineIdx)"
            >
              <span class="co-comfy-engine-option-head">
                <span class="co-comfy-engine-option-title-group">
                  <strong class="co-comfy-engine-option-title">
                    {{ formatDigitalHumanEngineTitle(engineWf) }}
                  </strong>
                  <span
                    v-if="isDigitalHumanEngineRecommendedBadgeShown(engineWf)"
                    class="co-comfy-engine-badge is-recommended"
                  >
                    {{ t("comfy.videoEngineRecommendedTag") }}
                  </span>
                  <span
                    v-if="gpuReady && comfy?.active_pack === engineWf.id"
                    class="co-comfy-engine-badge is-ready"
                  >
                    {{ t("comfy.videoEngineReadyTag") }}
                  </span>
                </span>
                <PhCheckCircle
                  v-if="engineWf.id === selectedWorkflowId"
                  :size="17"
                  weight="fill"
                  class="co-comfy-engine-check-icon"
                  aria-hidden="true"
                />
                <span
                  v-else
                  class="co-comfy-engine-radio-dot"
                  aria-hidden="true"
                />
              </span>
              <span class="co-comfy-engine-option-desc">
                {{ formatDigitalHumanEngineSummary(engineWf) }}
              </span>
              <span class="co-comfy-engine-option-spec co-comfy-mono">
                <template
                  v-for="(part, idx) in formatDigitalHumanEngineSpecParts(engineWf)"
                  :key="idx"
                ><span
                    v-if="idx > 0"
                    class="co-comfy-engine-spec-dot"
                    aria-hidden="true"
                  >·</span><span
                    :class="{ 'co-comfy-engine-spec-vram': part.isVram }"
                  >{{ part.text }}</span></template>
                <span
                  v-if="isDigitalHumanEngineVramShort(engineWf)"
                  class="co-sr-only"
                >
                  ({{ t("comfy.videoEngineVramShortSr") }})
                </span>
              </span>
            </button>
          </div>
        </div>

        <!-- Minimalist Preflight & Pack Readiness Strip -->
        <div
          v-if="workflowPreflightItems.length"
          class="co-comfy-preflight-list"
          role="status"
        >
          <div
            v-for="(pf, idx) in workflowPreflightItems"
            :key="idx"
            class="co-comfy-preflight-strip"
            :class="`is-${pf.tone}`"
          >
            <PhWarningCircle
              v-if="pf.tone === 'warning'"
              :size="15"
              weight="fill"
              class="co-comfy-preflight-icon"
              aria-hidden="true"
            />
            <PhCheckCircle
              v-else-if="pf.tone === 'ready'"
              :size="15"
              weight="fill"
              class="co-comfy-preflight-icon"
              aria-hidden="true"
            />
            <PhCloudArrowDown
              v-else
              :size="15"
              weight="duotone"
              class="co-comfy-preflight-icon"
              aria-hidden="true"
            />
            <span class="co-comfy-preflight-text">{{ pf.text }}</span>
          </div>
        </div>

        <form
          id="co-comfy-param-form"
          class="co-comfy-param-form"
          @submit.prevent="runWorkflow"
        >
          <AAlert
            v-if="!visibleFields.length"
            type="info"
            show-icon
            :message="t('comfy.noEditableFields')"
          />

          <template v-else>
            <!-- Primary Essential Inputs -->
            <div class="co-comfy-primary-fields">
              <template v-if="isFashionTryOnWorkflow">
                <ColabOneFashionPhotoGroup
                  group="model"
                  :items="fashionModelPhotoItems"
                  :max-count="COLABONE_FASHION_MAX_REFS_PER_GROUP"
                  :disabled="submittingRun"
                  :active="paramDrawerOpen"
                  :dropzone-id="fashionModelDropzoneId"
                  @add-files="(files) => handleFashionAddFiles('model', files)"
                  @remove="(key) => handleFashionRemovePhoto('model', key)"
                  @promote="(key) => handleFashionPromotePhoto('model', key)"
                  @change-role="
                    (key, role) => handleFashionChangeRole('model', key, role)
                  "
                />
                <ColabOneFashionPhotoGroup
                  group="garment"
                  :items="fashionGarmentPhotoItems"
                  :max-count="COLABONE_FASHION_MAX_REFS_PER_GROUP"
                  :disabled="submittingRun"
                  :active="paramDrawerOpen"
                  :dropzone-id="fashionGarmentDropzoneId"
                  @add-files="(files) => handleFashionAddFiles('garment', files)"
                  @remove="(key) => handleFashionRemovePhoto('garment', key)"
                  @promote="(key) => handleFashionPromotePhoto('garment', key)"
                  @change-role="
                    (key, role) => handleFashionChangeRole('garment', key, role)
                  "
                />
              </template>
              <ComfyWorkflowField
                v-for="field in standardPrimaryFields"
                :key="field.key"
                :field="field"
                :value="fieldValues[field.key]"
                :input-id="fieldInputId(field)"
                :node-meta="formatFieldNodeMeta(field)"
                :disabled="submittingRun"
                :force-required="isFieldForceRequired(field)"
                :required-hint="fieldRequiredHint(field)"
                :uploading="!!uploadingFields[field.key]"
                :uploaded-name="uploadedNames[field.key]"
                :has-pending-upload="!!pendingUploads[field.key]"
                :has-uploaded-remote="!!uploadedInputIds[field.key]"
                :has-draft-media="!!draftMediaMeta[field.key]"
                :local-preview="localPreviews[field.key]"
                :show-tts-strip="shouldRenderTtsStripAfter(field)"
                :selected-tts-engine="selectedTtsEngine"
                :show-voice-preview="shouldRenderVoicePreviewAfter(field)"
                :voice-preview-text="voicePreviewText"
                :voice-preview-default-text="voicePreviewDefaultText"
                :voice-preview-busy="voicePreviewBusy"
                :voice-preview-status="displayedVoicePreviewStatus"
                :voice-preview-error="voicePreviewError"
                :voice-preview-audio-url="voicePreviewAudioUrl"
                :voice-preview-play-token="voicePreviewPlayToken"
                :voice-preview-meta="voicePreviewMeta"
                :voice-preview-remote-ready="voicePreviewRemoteReady"
                :voice-preview-can-offline-play="voicePreviewCanOfflinePlay"
                :voice-preview-disabled="hasUploadingField || !selectedTtsEngine"
                :ready-voice-preset-ids="readyVoicePresetIds"
                :voice-preview-prefetch-active="voicePreviewPrefetchActive"
                :voice-preview-prefetch-done="voicePreviewPrefetchDone"
                :voice-preview-prefetch-total="voicePreviewPrefetchTotal"
                :share-mode="isShareMode"
                @update:value="(val) => onFieldValueUpdate(field, val)"
                @field-blur="onFieldBlur"
                @media-select="handleMediaFile"
                @media-clear="clearMediaField"
                @update:voice-preview-text="onVoicePreviewTextUpdate"
                @voice-preview-reset="onVoicePreviewReset"
                @voice-preview-play="triggerVoicePreview"
                @voice-preview-prefetch-cancel="cancelVoicePreviewPrefetch"
              />
            </div>

            <!-- Collapsible Advanced Settings Panel (collapsed by default) -->
            <div
              v-if="fieldPartitions.advanced.length || hasWorkflowSpecs"
              class="co-comfy-advanced-panel"
              :class="{ 'is-open': advancedExpanded }"
            >
              <button
                type="button"
                class="co-comfy-advanced-toggle"
                :aria-expanded="advancedExpanded"
                :aria-controls="
                  advancedExpanded ? 'co-comfy-advanced-body' : undefined
                "
                @click="advancedExpanded = !advancedExpanded"
              >
                <span class="co-comfy-advanced-toggle-main">
                  <PhSlidersHorizontal
                    :size="15"
                    class="co-comfy-advanced-toggle-icon"
                    aria-hidden="true"
                  />
                  <strong class="co-comfy-advanced-toggle-title">
                    {{ t("comfy.advancedToggleTitle") }}
                  </strong>
                  <span
                    class="co-comfy-advanced-summary-chip"
                    :title="advancedPresetSummary"
                  >
                    {{ advancedPresetSummary }}
                  </span>
                </span>
                <PhCaretDown
                  v-if="advancedExpanded"
                  :size="15"
                  class="co-comfy-advanced-caret"
                  aria-hidden="true"
                />
                <PhCaretRight
                  v-else
                  :size="15"
                  class="co-comfy-advanced-caret"
                  aria-hidden="true"
                />
              </button>

              <div
                v-if="advancedExpanded"
                id="co-comfy-advanced-body"
                class="co-comfy-advanced-body"
              >
                <div
                  v-if="fieldPartitions.advanced.length"
                  class="co-comfy-advanced-grid"
                >
                  <ComfyWorkflowField
                    v-for="field in fieldPartitions.advanced"
                    :key="field.key"
                    :field="field"
                    :value="fieldValues[field.key]"
                    :input-id="fieldInputId(field)"
                    :node-meta="formatFieldNodeMeta(field)"
                    :wide="isWideAdvancedField(field)"
                    :compact="true"
                    :disabled="submittingRun"
                    :force-required="isFieldForceRequired(field)"
                    :required-hint="fieldRequiredHint(field)"
                    :uploading="!!uploadingFields[field.key]"
                    :uploaded-name="uploadedNames[field.key]"
                    :has-pending-upload="!!pendingUploads[field.key]"
                    :has-uploaded-remote="!!uploadedInputIds[field.key]"
                    :has-draft-media="!!draftMediaMeta[field.key]"
                    :local-preview="localPreviews[field.key]"
                    :show-tts-strip="shouldRenderTtsStripAfter(field)"
                    :selected-tts-engine="selectedTtsEngine"
                    :show-voice-preview="shouldRenderVoicePreviewAfter(field)"
                    :voice-preview-text="voicePreviewText"
                    :voice-preview-default-text="voicePreviewDefaultText"
                    :voice-preview-busy="voicePreviewBusy"
                    :voice-preview-status="displayedVoicePreviewStatus"
                    :voice-preview-error="voicePreviewError"
                    :voice-preview-audio-url="voicePreviewAudioUrl"
                    :voice-preview-play-token="voicePreviewPlayToken"
                    :voice-preview-meta="voicePreviewMeta"
                    :voice-preview-remote-ready="voicePreviewRemoteReady"
                    :voice-preview-can-offline-play="voicePreviewCanOfflinePlay"
                    :voice-preview-disabled="
                      hasUploadingField || !selectedTtsEngine
                    "
                    :ready-voice-preset-ids="readyVoicePresetIds"
                    :voice-preview-prefetch-active="voicePreviewPrefetchActive"
                    :voice-preview-prefetch-done="voicePreviewPrefetchDone"
                    :voice-preview-prefetch-total="voicePreviewPrefetchTotal"
                    :share-mode="isShareMode"
                    @update:value="(val) => onFieldValueUpdate(field, val)"
                    @field-blur="onFieldBlur"
                    @media-select="handleMediaFile"
                    @media-clear="clearMediaField"
                    @update:voice-preview-text="onVoicePreviewTextUpdate"
                    @voice-preview-reset="onVoicePreviewReset"
                    @voice-preview-play="triggerVoicePreview"
                    @voice-preview-prefetch-cancel="cancelVoicePreviewPrefetch"
                  />
                </div>

                <!-- Compact Hardware & Dependency Specs Footer -->
                <div v-if="hasWorkflowSpecs" class="co-comfy-specs-box">
                  <div class="co-comfy-specs-title">
                    {{ t("comfy.specsDetailTitle") }}
                  </div>
                  <div class="co-comfy-specs-pills">
                    <ATag
                      v-if="selectedWorkflow.min_vram_mib > 0"
                      class="co-comfy-meta-tag"
                    >
                      {{
                        t("comfy.recommendedVram", {
                          vram: `${Math.max(1, Math.round(selectedWorkflow.min_vram_mib / 1024))} GB+`,
                        })
                      }}
                    </ATag>
                    <ATag
                      v-if="selectedWorkflow.recommended_gpu"
                      class="co-comfy-meta-tag"
                    >
                      {{
                        t("comfy.recommendedGpu", {
                          gpu: tb(selectedWorkflow.recommended_gpu),
                        })
                      }}
                    </ATag>
                    <ATag
                      v-if="selectedWorkflow.models?.length"
                      class="co-comfy-meta-tag"
                    >
                      {{
                        t("comfy.modelDepsTag", {
                          count: selectedWorkflow.models.length,
                          size: formatComfyByteSize(
                            workflowModelsTotalBytes(selectedWorkflow),
                          ),
                        })
                      }}
                    </ATag>
                    <ATag
                      v-else-if="(selectedWorkflow.min_disk_mib || 0) > 0"
                      class="co-comfy-meta-tag"
                    >
                      {{
                        t("comfy.recommendedDisk", {
                          disk: `~${Math.max(1, Math.round((selectedWorkflow.min_disk_mib || 0) / 1024))} GB`,
                        })
                      }}
                    </ATag>
                    <ATag
                      v-if="selectedWorkflow.custom_nodes?.length"
                      class="co-comfy-meta-tag"
                    >
                      {{
                        t("comfy.packNodesTag", {
                          count: selectedWorkflow.custom_nodes.length,
                        })
                      }}
                    </ATag>
                    <ColabOneTooltip
                      v-if="selectedWorkflow.license"
                      :title="
                        isWorkflowNonCommercial(selectedWorkflow)
                          ? workflowNonCommercialTooltip(selectedWorkflow)
                          : selectedWorkflow.license_notice
                            ? tb(selectedWorkflow.license_notice)
                            : t('comfy.licenseCommercialOk')
                      "
                    >
                      <ATag
                        :color="
                          isWorkflowNonCommercial(selectedWorkflow)
                            ? 'orange'
                            : 'green'
                        "
                        class="co-comfy-meta-tag"
                      >
                        {{ formatWorkflowLicenseTag(selectedWorkflow) }}
                      </ATag>
                    </ColabOneTooltip>
                  </div>
                </div>
              </div>
            </div>
          </template>
        </form>

        <!-- Always-Visible Non-Commercial License & TTS Suite Disclaimer Banner -->
        <AAlert
          v-if="isTtsAudioSource"
          type="warning"
          show-icon
          class="co-comfy-license-alert co-comfy-tts-suite-alert"
          :message="
            nonCommercialNotice &&
            (nonCommercialNotice.restrictedBy === 'workflow' ||
              nonCommercialNotice.restrictedBy === 'both')
              ? t('comfy.licenseNoticeTitle')
              : t('comfy.ttsNonCommercialSuiteTitle')
          "
        >
          <template #description>
            <p v-if="nonCommercialNotice" class="co-comfy-license-alert-line">
              {{ nonCommercialAlertDescription }}
            </p>
            <p class="co-comfy-license-alert-line">
              {{ t("comfy.ttsNonCommercialSuiteDesc") }}
            </p>
          </template>
        </AAlert>
        <AAlert
          v-else-if="nonCommercialNotice"
          type="warning"
          show-icon
          class="co-comfy-license-alert"
          :message="t('comfy.licenseNoticeTitle')"
          :description="nonCommercialAlertDescription"
        />
      </div>

      <template #footer>
        <div v-if="selectedWorkflow" class="co-comfy-param-drawer-footer-shell">
          <Transition name="co-draft-status-fade">
            <div v-if="draftStatusLabel" class="co-comfy-draft-status-bar">
              <span
                class="co-comfy-draft-status"
                :class="`is-${draftStatus}`"
                role="status"
                aria-live="polite"
              >
                {{ draftStatusLabel }}
              </span>
            </div>
          </Transition>
          <div class="co-comfy-param-drawer-footer">
            <div class="co-comfy-param-drawer-footer-left">
              <AButton
                class="co-comfy-drawer-close-btn"
                data-testid="comfy-param-drawer-close-btn"
                @click="closeParamDrawer"
              >
                {{ t("common.close") }}
              </AButton>
              <APopover
                v-if="!isShareMode"
                v-model:open="mobileAutoReleasePopoverOpen"
                trigger="click"
                placement="topLeft"
                :z-index="1030"
                overlay-class-name="co-comfy-mobile-autorelease-popover"
              >
                <template #content>
                  <div
                    class="co-comfy-mobile-ar-sheet"
                    data-testid="comfy-drawer-auto-release-mobile-sheet"
                  >
                    <div class="co-comfy-mobile-ar-head">
                      <div class="co-comfy-mobile-ar-title">
                        <PhPower
                          :size="15"
                          weight="duotone"
                          class="co-comfy-autorelease-icon"
                          :class="{ 'is-armed': workflowAutoReleaseEnabled }"
                          aria-hidden="true"
                        />
                        <span>{{ t("comfy.autoReleaseSwitchShort") }}</span>
                      </div>
                      <ASwitch
                        size="small"
                        :checked="workflowAutoReleaseEnabled"
                        :loading="workflowAutoReleaseBusy"
                        :disabled="!gpuReady || workflowAutoReleaseLocked"
                        data-testid="comfy-drawer-auto-release-mobile-switch"
                        @change="onWorkflowAutoReleaseToggle"
                      />
                    </div>
                    <p class="co-comfy-mobile-ar-desc">
                      {{ autoReleaseSwitchTooltip }}
                    </p>
                  </div>
                </template>
                <AButton
                  class="co-comfy-drawer-autorelease-mobile-btn"
                  :class="{
                    'is-armed': workflowAutoReleaseEnabled,
                    'is-disabled': !gpuReady || workflowAutoReleaseLocked,
                  }"
                  :aria-label="t('comfy.autoReleaseSwitchShort')"
                  :aria-expanded="mobileAutoReleasePopoverOpen"
                  :aria-pressed="workflowAutoReleaseEnabled"
                  data-testid="comfy-drawer-auto-release-mobile-trigger"
                >
                  <PhPower
                    :size="16"
                    :weight="workflowAutoReleaseEnabled ? 'bold' : 'duotone'"
                    class="co-comfy-autorelease-icon"
                    aria-hidden="true"
                  />
                  <span
                    v-if="workflowAutoReleaseEnabled"
                    class="co-comfy-mobile-ar-dot"
                    aria-hidden="true"
                  />
                </AButton>
              </APopover>
              <AButton
                v-if="comfyRunTask || comfyTask"
                danger
                class="co-comfy-drawer-cancel-btn"
                :loading="Boolean(comfyTask && cancellingTaskId === comfyTask.id)"
                :disabled="cancellingTaskId !== null"
                @click="cancelActiveTask"
              >
                {{ t("comfy.cancelTask") }}
              </AButton>
            </div>
            <div class="co-comfy-param-drawer-footer-right">
              <ColabOneTooltip v-if="!isShareMode" :title="autoReleaseSwitchTooltip">
                <div
                  class="co-comfy-drawer-autorelease-pill"
                  :class="{
                    'is-armed': workflowAutoReleaseEnabled,
                    'is-disabled': !gpuReady || workflowAutoReleaseLocked,
                  }"
                  data-testid="comfy-drawer-auto-release"
                >
                  <PhPower
                    :size="14"
                    weight="duotone"
                    class="co-comfy-autorelease-icon"
                    aria-hidden="true"
                  />
                  <span class="co-comfy-drawer-autorelease-text">
                    {{ t("comfy.autoReleaseSwitchShort") }}
                  </span>
                  <ASwitch
                    size="small"
                    :checked="workflowAutoReleaseEnabled"
                    :loading="workflowAutoReleaseBusy"
                    :disabled="!gpuReady || workflowAutoReleaseLocked"
                    data-testid="comfy-drawer-auto-release-switch"
                    @change="onWorkflowAutoReleaseToggle"
                  />
                </div>
              </ColabOneTooltip>
              <div class="co-comfy-drawer-run-wrap">
                <ColabOneTooltip :title="runButtonDisabledTooltip">
                  <AButton
                    type="primary"
                    class="co-comfy-drawer-run-btn"
                    :loading="submittingRun || comfyStarting"
                    :disabled="
                      !gpuReady ||
                      (isShareMode && Boolean(props.shareBusyOther)) ||
                      comfyStarting ||
                      submittingRun ||
                      hasUploadingField ||
                      engineActionBlocked
                    "
                    @click="runWorkflow"
                  >
                    <template #icon>
                      <PhSparkle
                        v-if="comfyRunning || !workflowNeedsComfyServer"
                        :size="15"
                      />
                      <PhPlay v-else :size="14" weight="fill" />
                    </template>
                    {{
                      !gpuReady
                        ? t("comfy.runWorkflow")
                        : comfyStarting
                          ? t("comfy.preparingBtn")
                          : workflowNeedsComfyServer && !comfyRunning
                            ? t("comfy.startComfyFirst")
                            : submittingRun
                              ? t("common.submitting")
                              : t("comfy.runWorkflow")
                    }}
                  </AButton>
                </ColabOneTooltip>
              </div>
            </div>
          </div>
        </div>
      </template>
    </ADrawer>

    <!-- Drawer 2: Output Results Drawer -->
    <ADrawer
      :open="outputsDrawerOpen"
      placement="right"
      :width="outputsDrawerWidth"
      :mask="true"
      :mask-closable="true"
      :keyboard="false"
      :destroy-on-close="false"
      :z-index="1010"
      :header-style="{ padding: '14px 20px' }"
      :body-style="{ padding: '16px 20px 24px', overflowY: 'auto' }"
      :footer-style="{ padding: '12px 20px' }"
      root-class-name="co-comfy-outputs-drawer-root"
      @update:open="
        (val) => {
          if (!val) closeOutputsDrawer();
        }
      "
    >
      <template #title>
        <div class="co-comfy-drawer-title">
          <PhImage
            :size="18"
            weight="duotone"
            class="co-comfy-drawer-accent-icon"
          />
          <span class="co-comfy-drawer-title-text">{{ t("comfy.galleryTitle") }}</span>
          <ColabOneSkeleton
            v-if="props.loading"
            :width="42"
            :height="22"
            variant="tag"
            tone="subtle"
            inline
          />
          <ATag v-else class="co-comfy-count-tag co-sk-reveal">
            {{ t("comfy.outputCount", { count: groupedOutputs.length }) }}
          </ATag>
        </div>
      </template>

      <template #extra>
        <ASpace v-if="!isShareMode" :size="6" wrap>
          <AButton
            v-if="groupedOutputs.length > 0"
            size="small"
            :type="outputMultiSelectMode ? 'primary' : 'default'"
            :ghost="outputMultiSelectMode"
            data-testid="comfy-outputs-multi-select-toggle"
            @click="toggleOutputMultiSelect"
          >
            <template #icon><PhSelectionAll :size="14" /></template>
            {{
              outputMultiSelectMode
                ? t("outputGallery.multiSelectCancel")
                : t("outputGallery.multiSelectBtn")
            }}
          </AButton>
          <AButton
            size="small"
            class="co-comfy-outputs-shares-btn"
            data-testid="comfy-outputs-shares-manage"
            @click="openOutputShareManage"
          >
            <template #icon><PhShareNetwork :size="14" /></template>
            <span>{{ t("outputGallery.sharesManageBtn") }}</span>
            <span
              v-if="activeOutputShareCount > 0"
              class="co-comfy-share-count-pill"
            >
              {{ activeOutputShareCount }}
            </span>
          </AButton>
          <ColabOneTooltip
            v-if="!gpuReady && state.setup.colab && driveOutputsCanDirectRead"
            :title="t('comfy.outputsDriveRefreshTooltip')"
          >
            <AButton
              size="small"
              :loading="isDriveOutputsSyncing"
              data-testid="comfy-outputs-drive-refresh"
              @click="syncDriveOutputsFromCloud()"
            >
              <template #icon><PhArrowsClockwise :size="14" /></template>
              {{ t("comfy.outputsDriveRefreshBtn") }}
            </AButton>
          </ColabOneTooltip>
          <AButton
            v-if="driveWaitingBrowserAuth"
            size="small"
            class="co-comfy-outputs-drive-pill is-warning"
            data-testid="comfy-outputs-drive-pill"
            @click="openDriveSetupModal(null, false)"
          >
            <template #icon><PhCloud :size="14" weight="duotone" /></template>
            {{ t("comfy.outputsDriveWaitingPill") }}
          </AButton>
          <AButton
            v-else-if="driveMountingInProgress"
            size="small"
            loading
            class="co-comfy-outputs-drive-pill is-active"
            data-testid="comfy-outputs-drive-pill"
            @click="openDriveSetupModal(null, false)"
          >
            {{
              state.operation?.cancelling
                ? t("comfy.driveModalCancellingBtn")
                : t("comfy.driveModalMountingBtn")
            }}
          </AButton>
          <ColabOneTooltip
            v-else-if="state.setup.drive"
            :title="
              t('comfy.outputsDriveConnectedTooltip', {
                folder: activeDriveFolderName,
              })
            "
          >
            <AButton
              size="small"
              class="co-comfy-outputs-drive-pill is-connected"
              data-testid="comfy-outputs-drive-pill"
              @click="openDriveSetupModal(null, false)"
            >
              <template #icon><PhCloudCheck :size="14" weight="duotone" /></template>
              {{ t("comfy.outputsDriveConnectedPill") }}
            </AButton>
          </ColabOneTooltip>
          <ColabOneTooltip
            v-else-if="!gpuReady && state.setup.colab && (driveOutputsCanDirectRead || outputs.length > 0)"
            :title="t('comfy.outputsDriveOfflineLinkedTooltip')"
          >
            <AButton
              size="small"
              class="co-comfy-outputs-drive-pill is-connected"
              data-testid="comfy-outputs-drive-pill"
              @click="openExternalURL(GOOGLE_DRIVE_HOME_URL)"
            >
              <template #icon><PhCloudCheck :size="14" weight="duotone" /></template>
              {{ t("comfy.outputsDriveOfflineLinkedPill") }}
            </AButton>
          </ColabOneTooltip>
          <ColabOneTooltip
            v-else
            :title="
              !gpuReady
                ? gpuStarting
                  ? t('api.gpuStartingTooltip')
                  : t('api.startGpuFirstTooltip')
                : t('comfy.outputsDriveSetupTooltip')
            "
          >
            <AButton
              size="small"
              class="co-comfy-outputs-drive-pill"
              :disabled="!gpuReady"
              data-testid="comfy-outputs-drive-pill"
              @click="openDriveSetupModal(null, false)"
            >
              <template #icon><PhCloud :size="14" weight="duotone" /></template>
              {{ t("comfy.outputsDriveSetupPill") }}
            </AButton>
          </ColabOneTooltip>
        </ASpace>
      </template>

      <div
        class="co-comfy-outputs-drawer-body"
        role="region"
        :aria-label="t('comfy.outputsDrawerAria')"
      >
        <!-- Full-Width Unclipped Storage & Persistence Hint Strip -->
        <div
          v-if="!isShareMode"
          class="co-comfy-outputs-storage-bar"
          :class="{
            'is-connected':
              state.setup.drive ||
              (!gpuReady &&
                state.setup.colab &&
                (driveOutputsCanDirectRead || outputs.length > 0)),
            'is-waiting': driveWaitingBrowserAuth,
            'is-active': driveMountingInProgress || isDriveOutputsSyncing,
          }"
          data-testid="comfy-outputs-storage-bar"
        >
          <PhCloudCheck
            v-if="
              state.setup.drive ||
              (!gpuReady &&
                state.setup.colab &&
                (driveOutputsCanDirectRead || outputs.length > 0))
            "
            :size="16"
            weight="duotone"
            class="co-comfy-outputs-storage-icon"
            aria-hidden="true"
          />
          <PhInfo
            v-else
            :size="16"
            weight="duotone"
            class="co-comfy-outputs-storage-icon"
            aria-hidden="true"
          />
          <span class="co-comfy-outputs-storage-text">
            {{
              driveWaitingBrowserAuth
                ? t("comfy.driveWaitingHint")
                : driveMountingInProgress
                  ? state.operation?.cancelling
                    ? t("comfy.driveModalCancellingTitle")
                    : state.operation?.interactive
                      ? t("device.driveMountingAfterAuth")
                      : state.operation?.title
                        ? tb(state.operation.title)
                        : t("comfy.driveModalMountingDirectTitle")
                  : state.setup.drive
                    ? t("comfy.drivePersistHintFolder", {
                        folder: activeDriveFolderName,
                      })
                    : !gpuReady && state.setup.colab
                      ? driveOutputsCanDirectRead
                        ? t("comfy.driveOfflineDirectHint")
                        : outputs.length > 0
                          ? t("comfy.driveOfflineRecordHint")
                          : t("comfy.localExportHint")
                      : t("comfy.localExportHint")
            }}
          </span>
          <AButton
            v-if="
              state.setup.drive ||
              (!gpuReady &&
                state.setup.colab &&
                (driveOutputsCanDirectRead || outputs.length > 0))
            "
            type="link"
            size="small"
            class="co-inline-link co-comfy-outputs-storage-action"
            @click="openExternalURL(GOOGLE_DRIVE_HOME_URL)"
          >
            {{ t("comfy.driveOpenGoogleDriveShort") }}
            <PhArrowSquareOut :size="13" />
          </AButton>
          <AButton
            v-else-if="gpuReady"
            type="link"
            size="small"
            class="co-inline-link co-comfy-outputs-storage-action"
            @click="openDriveSetupModal(null, false)"
          >
            {{
              driveWaitingBrowserAuth
                ? t("comfy.outputsDriveContinueAuth")
                : driveMountingInProgress
                  ? t("comfy.outputsDriveViewProgress")
                  : t("comfy.outputsDriveConfigureNow")
            }}
          </AButton>
        </div>

        <AAlert
          v-if="
            !isShareMode &&
            !gpuReady &&
            state.setup.colab &&
            state.drive_outputs_status?.error &&
            !isDriveOutputsSyncing
          "
          type="warning"
          show-icon
          class="co-comfy-card-alert"
          data-testid="comfy-outputs-drive-sync-error"
        >
          <template #message>
            {{ t("comfy.outputsDriveSyncErrorTitle") }}
          </template>
          <template #description>
            <ColabOnePrivacyText
              :text="tb(state.drive_outputs_status.error)"
              :context="privacyContext"
            />
          </template>
          <template #action>
            <AButton
              size="small"
              type="link"
              @click="syncDriveOutputsFromCloud()"
            >
              {{ t("comfy.outputsDriveSyncRetryBtn") }}
            </AButton>
          </template>
        </AAlert>

        <!-- Multi-Select Combined Sharing Bar -->
        <div
          v-if="!isShareMode && outputMultiSelectMode && groupedOutputs.length > 0"
          class="co-comfy-multiselect-bar"
          data-testid="comfy-outputs-multiselect-bar"
        >
          <span class="co-comfy-multiselect-summary">
            {{
              t("outputGallery.multiSelectSummary", {
                groups: multiSelectSummaryCounts.groups,
                items: multiSelectSummaryCounts.items,
              })
            }}
          </span>
          <ASpace :size="6" wrap>
            <AButton size="small" @click="selectAllOutputGroups">
              {{ t("outputGallery.multiSelectAll") }}
            </AButton>
            <AButton
              size="small"
              :disabled="!selectedOutputGroupIds.length"
              @click="clearOutputGroupSelection"
            >
              {{ t("outputGallery.multiSelectClear") }}
            </AButton>
            <AButton
              size="small"
              type="primary"
              :disabled="!selectedOutputGroupIds.length"
              data-testid="comfy-outputs-multiselect-share-btn"
              @click="openMultiSelectShare"
            >
              <template #icon><PhShareNetwork :size="14" /></template>
              {{ t("outputGallery.multiSelectCreateBtn") }}
            </AButton>
          </ASpace>
        </div>

        <div
          v-if="props.loading && !outputs.length"
          class="co-comfy-output-grid"
          role="status"
          aria-busy="true"
        >
          <span class="co-sr-only">{{ t("common.loading") }}</span>
          <article
            v-for="idx in 2"
            :key="idx"
            class="co-comfy-output-item co-comfy-output-skeleton"
          >
            <div class="co-comfy-output-frame co-comfy-output-frame-sk">
              <ColabOneSkeleton
                width="100%"
                height="100%"
                :radius="0"
                variant="rect"
                :delay="(idx - 1) * 90"
              />
            </div>
            <div class="co-comfy-output-body">
              <div class="co-comfy-output-title-row">
                <ColabOneSkeleton
                  :width="idx === 1 ? '62%' : '54%'"
                  :height="15"
                  variant="text"
                  :delay="(idx - 1) * 90 + 40"
                />
                <ColabOneSkeleton
                  :width="42"
                  :height="20"
                  variant="tag"
                  tone="subtle"
                  :delay="(idx - 1) * 90 + 70"
                />
              </div>
              <ColabOneSkeleton
                width="76%"
                :height="12"
                variant="text"
                tone="subtle"
                :delay="(idx - 1) * 90 + 100"
              />
              <div class="co-comfy-output-meta-tags">
                <ColabOneSkeleton
                  :width="54"
                  :height="20"
                  variant="tag"
                  tone="subtle"
                  :delay="(idx - 1) * 90 + 130"
                />
                <ColabOneSkeleton
                  :width="48"
                  :height="20"
                  variant="tag"
                  tone="subtle"
                  :delay="(idx - 1) * 90 + 160"
                />
              </div>
              <div class="co-comfy-output-actions">
                <div class="co-comfy-output-export-group">
                  <ColabOneSkeleton
                    :width="82"
                    :height="24"
                    variant="button"
                    :radius="6"
                    tone="subtle"
                    :delay="(idx - 1) * 90 + 185"
                  />
                  <ColabOneSkeleton
                    :width="82"
                    :height="24"
                    variant="button"
                    :radius="6"
                    tone="subtle"
                    :delay="(idx - 1) * 90 + 205"
                  />
                </div>
                <ColabOneSkeleton
                  :width="26"
                  :height="24"
                  variant="button"
                  :radius="6"
                  tone="subtle"
                  :delay="(idx - 1) * 90 + 225"
                />
              </div>
            </div>
          </article>
        </div>

        <div
          v-else-if="!outputs.length && !comfyRunTask && !submittingRun"
          class="co-comfy-gallery-empty-wrap"
        >
          <AEmpty :description="t('comfy.emptyOutputs')">
            <template #image>
              <div class="co-comfy-empty-emblem">
                <PhImage :size="30" />
              </div>
            </template>
            <p class="co-comfy-empty-copy">
              {{ t("comfy.emptyOutputsDesc") }}
            </p>
          </AEmpty>
        </div>

        <div v-else class="co-comfy-output-grid co-sk-reveal">
          <!-- Active Workflow Generating Artwork Skeleton -->
          <article
            v-if="comfyRunTask || submittingRun"
            class="co-comfy-output-item co-comfy-output-generating"
            role="status"
            aria-busy="true"
          >
            <div class="co-comfy-output-frame co-comfy-gen-frame">
              <ColabOneSkeleton
                width="100%"
                height="100%"
                :radius="0"
                variant="rect"
                tone="accent"
              />
              <div class="co-comfy-preview-pill">
                <PhSparkle
                  :size="14"
                  weight="duotone"
                  class="co-comfy-gen-sparkle"
                />
                <span>
                  {{
                    comfyRunTask
                      ? `${t("comfy.tagQueueRunning")} · ${comfyTaskPercent}%`
                      : t("common.submitting")
                  }}
                </span>
              </div>
              <span
                class="co-comfy-gen-bar"
                :style="{ width: `${Math.max(8, comfyTaskPercent)}%` }"
                aria-hidden="true"
              />
            </div>
            <div class="co-comfy-output-body">
              <div class="co-comfy-output-title-row">
                <strong>{{ runningWorkflowName }}</strong>
                <ATag color="processing" class="co-comfy-status-tag">
                  <ABadge status="processing" />
                  <span>{{ t("comfy.tagQueueRunning") }}</span>
                </ATag>
              </div>
              <p
                v-if="comfyRunTask?.message"
                class="co-comfy-gen-message"
              >
                {{ tb(comfyRunTask.message) }}
              </p>
              <ColabOneSkeleton
                v-else
                width="74%"
                :height="12"
                variant="text"
                tone="subtle"
                :delay="60"
              />
              <div class="co-comfy-output-meta-tags">
                <ATag
                  v-if="comfyTaskElapsedText"
                  class="co-comfy-meta-tag co-comfy-elapsed-tag"
                  data-testid="comfy-gen-elapsed"
                >
                  <PhClock :size="12" aria-hidden="true" />
                  <span>{{
                    t("task.elapsedLive", { duration: comfyTaskElapsedText })
                  }}</span>
                </ATag>
                <ATag
                  v-if="!isShareMode && comfyTaskLiveCuText"
                  class="co-comfy-meta-tag co-comfy-cu-tag"
                  data-testid="comfy-gen-cu"
                >
                  <PhCoins :size="12" weight="duotone" aria-hidden="true" />
                  <span class="co-comfy-mono">{{ comfyTaskLiveCuText }}</span>
                </ATag>
                <ColabOneSkeleton
                  v-if="!comfyTaskElapsedText"
                  :width="58"
                  :height="18"
                  variant="tag"
                  tone="subtle"
                  :delay="100"
                />
                <ColabOneSkeleton
                  v-if="!isShareMode && !comfyTaskLiveCuText"
                  :width="48"
                  :height="18"
                  variant="tag"
                  tone="subtle"
                  :delay="140"
                />
              </div>
            </div>
          </article>

          <template v-for="group in groupedOutputs" :key="group.id">
            <!-- Grouped Multi-Output Album Card -->
            <article
              v-if="group.isAlbum"
              :ref="(el) => registerOutputCardEl(group.id, el)"
              :data-output-group-id="group.id"
              class="co-comfy-output-item co-comfy-album-item"
              :class="{
                'is-selectable': outputMultiSelectMode,
                'is-selected': selectedOutputGroupIdSet.has(group.id),
              }"
              data-testid="comfy-output-album-card"
            >
              <div
                class="co-comfy-output-frame co-comfy-album-frame"
                :role="outputMultiSelectMode ? 'checkbox' : 'button'"
                :aria-checked="
                  outputMultiSelectMode
                    ? selectedOutputGroupIdSet.has(group.id)
                    : undefined
                "
                tabindex="0"
                :aria-label="
                  outputMultiSelectMode
                    ? group.workflowName
                      ? tb(group.workflowName)
                      : group.title || t('comfy.defaultWorkflowName')
                    : t('outputGallery.openAlbum')
                "
                @click="
                  outputMultiSelectMode
                    ? toggleOutputGroupSelection(group.id)
                    : openOutputGallery(group)
                "
                @keydown.enter.prevent="
                  outputMultiSelectMode
                    ? toggleOutputGroupSelection(group.id)
                    : openOutputGallery(group)
                "
                @keydown.space.prevent="
                  outputMultiSelectMode
                    ? toggleOutputGroupSelection(group.id)
                    : openOutputGallery(group)
                "
              >
                <div
                  v-if="!isShareMode && outputMultiSelectMode"
                  class="co-comfy-select-overlay"
                  aria-hidden="true"
                >
                  <ACheckbox
                    :checked="selectedOutputGroupIdSet.has(group.id)"
                    :tabindex="-1"
                  />
                </div>

                <div
                  class="co-comfy-album-collage"
                  :class="`is-count-${Math.min(4, Math.max(1, group.coverItems.length))}`"
                >
                  <div
                    v-for="(coverItem, tileIdx) in group.coverItems"
                    :key="coverItem.id"
                    class="co-comfy-album-tile"
                  >
                    <img
                      v-if="
                        coverItem.kind === 'image' &&
                        getOutputPreviewSrc(coverItem)
                      "
                      :src="getOutputPreviewSrc(coverItem)"
                      :alt="coverItem.filename"
                      class="co-sk-reveal"
                      @load="onOutputMediaReady(coverItem)"
                      @error="onOutputMediaError(coverItem)"
                    />
                    <div
                      v-else-if="coverItem.kind === 'video'"
                      class="co-comfy-album-tile-media-fallback"
                    >
                      <PhVideoCamera :size="22" weight="duotone" />
                    </div>
                    <div
                      v-else-if="coverItem.kind === 'audio'"
                      class="co-comfy-album-tile-media-fallback"
                    >
                      <PhWaveform :size="22" weight="duotone" />
                    </div>
                    <ColabOneSkeleton
                      v-else-if="fetchingOutputs[coverItem.id]"
                      width="100%"
                      height="100%"
                      :radius="0"
                      variant="rect"
                      tone="subtle"
                    />
                    <div
                      v-else
                      class="co-comfy-album-tile-media-fallback"
                    >
                      <PhWifiSlash
                        v-if="!canLoadOutputAsset(coverItem) && isShareMode"
                        :size="20"
                        weight="duotone"
                      />
                      <PhCloudCheck
                        v-else-if="!canLoadOutputAsset(coverItem) && !isShareMode"
                        :size="20"
                        weight="duotone"
                      />
                      <PhImage v-else :size="20" weight="duotone" />
                    </div>

                    <div
                      v-if="
                        group.coverItems.length > 1 &&
                        tileIdx === group.coverItems.length - 1 &&
                        group.extraCount > 0
                      "
                      class="co-comfy-album-more-overlay"
                    >
                      +{{ group.extraCount }}
                    </div>
                  </div>
                </div>

                <div class="co-comfy-album-count-pill">
                  <PhImages :size="13" weight="duotone" aria-hidden="true" />
                  <span>
                    {{
                      group.hasVideo || group.hasAudio
                        ? t("outputGallery.albumMixedTag", {
                            count: group.items.length,
                          })
                        : t("outputGallery.albumTag", {
                            count: group.items.length,
                          })
                    }}
                  </span>
                </div>

                <div
                  v-if="!outputMultiSelectMode"
                  class="co-comfy-album-hover-pill"
                >
                  <PhMagnifyingGlassPlus :size="13" aria-hidden="true" />
                  <span>{{ t("outputGallery.openAlbum") }}</span>
                </div>
              </div>

              <div class="co-comfy-output-body">
                <div class="co-comfy-output-title-row">
                  <ColabOneTooltip
                    :title="
                      group.workflowName
                        ? tb(group.workflowName)
                        : t('comfy.defaultWorkflowName')
                    "
                  >
                    <strong>{{
                      group.workflowName
                        ? tb(group.workflowName)
                        : t("comfy.defaultWorkflowName")
                    }}</strong>
                  </ColabOneTooltip>
                </div>

                <ColabOneTooltip
                  v-if="formatOutputCreatedAt(group.createdAt)"
                  :title="formatOutputCreatedAtTooltip(group.createdAt)"
                >
                  <div
                    class="co-comfy-output-time"
                    data-testid="comfy-output-created-at"
                  >
                    <PhCalendarBlank :size="12" aria-hidden="true" />
                    <time
                      class="co-comfy-mono"
                      :datetime="formatOutputCreatedAtISO(group.createdAt)"
                    >
                      {{ formatOutputCreatedAt(group.createdAt) }}
                    </time>
                  </div>
                </ColabOneTooltip>

                <div class="co-comfy-output-meta-tags">
                  <ATag class="co-comfy-meta-tag">
                    {{ formatOutputSize(group.totalSize) }}
                  </ATag>
                  <ATag
                    v-if="
                      Number.isFinite(group.totalElapsed) &&
                      group.totalElapsed > 0
                    "
                    class="co-comfy-meta-tag co-comfy-elapsed-tag"
                    data-testid="comfy-output-elapsed"
                  >
                    <PhClock :size="12" aria-hidden="true" />
                    <span>{{
                      t("task.elapsedTotal", {
                        duration: formatOutputElapsed(group.totalElapsed),
                      })
                    }}</span>
                  </ATag>
                  <ATag
                    v-if="group.inspectCount > 0"
                    color="gold"
                    class="co-comfy-meta-tag"
                  >
                    {{
                      t("outputGallery.inspectIncludedHint", {
                        count: group.inspectCount,
                      })
                    }}
                  </ATag>
                  <ColabOneTooltip
                    v-if="!isShareMode && formatGroupCUBadge(group)"
                    :title="formatGroupCUTooltip(group)"
                  >
                    <ATag
                      class="co-comfy-meta-tag co-comfy-cu-tag"
                      data-testid="comfy-output-cu"
                    >
                      <PhCoins :size="12" weight="duotone" aria-hidden="true" />
                      <span class="co-comfy-mono">{{
                        formatGroupCUBadge(group)
                      }}</span>
                    </ATag>
                  </ColabOneTooltip>
                  <ColabOneTooltip
                    v-if="!isShareMode && isGroupShared(group)"
                    :title="getGroupShareTooltip(group)"
                  >
                    <ATag
                      color="purple"
                      class="co-comfy-meta-tag co-comfy-shared-tag"
                      data-testid="comfy-output-shared-tag"
                    >
                      <PhShareNetwork :size="12" aria-hidden="true" />
                      <span>{{ t("comfy.outputTagShared") }}</span>
                    </ATag>
                  </ColabOneTooltip>
                  <ColabOneTooltip
                    v-if="
                      !isShareMode &&
                      (isGroupFullyExportedToDrive(group) ||
                        group.anyDriveSynced)
                    "
                    :title="
                      getGroupDrivePath(group)
                        ? formatColabOneDrivePath(getGroupDrivePath(group))
                        : t('comfy.outputDriveSyncedTooltipFolder', {
                            folder: group.items.some((x) => x.drive_mode === 'colab_drivefs')
                              ? 'ColabOne_Drive_Mount'
                              : activeDriveFolderName,
                          })
                    "
                  >
                    <ATag
                      :color="
                        isGroupFullyExportedToDrive(group) ? 'success' : 'blue'
                      "
                      class="co-comfy-meta-tag co-comfy-drive-linked-tag"
                      data-testid="comfy-output-drive-tag"
                    >
                      <PhCloudCheck :size="12" aria-hidden="true" />
                      <span>{{
                        isGroupFullyExportedToDrive(group)
                          ? t("comfy.outputTagSavedInDrive")
                          : t("comfy.outputTagSyncedInDrive")
                      }}</span>
                    </ATag>
                  </ColabOneTooltip>
                </div>

                <div class="co-comfy-output-actions">
                  <div class="co-comfy-action-primary">
                    <ColabOneTooltip :title="t('outputGallery.openAlbum')">
                      <AButton
                        size="small"
                        type="primary"
                        ghost
                        class="co-comfy-primary-action-btn"
                        data-testid="comfy-album-open-btn"
                        :aria-label="t('outputGallery.openAlbum')"
                        @click="openOutputGallery(group)"
                      >
                        <template #icon><PhImages :size="14" /></template>
                        <span class="co-comfy-action-label">{{
                          t("outputGallery.openAlbumShort")
                        }}</span>
                      </AButton>
                    </ColabOneTooltip>
                    <ColabOneTooltip
                      v-if="!isShareMode"
                      :title="t('outputGallery.shareBtn')"
                    >
                      <AButton
                        size="small"
                        class="co-comfy-primary-action-btn"
                        data-testid="comfy-output-share-btn"
                        :aria-label="t('outputGallery.shareBtn')"
                        @click="openOutputShareForGroups([group.id])"
                      >
                        <template #icon><PhShareNetwork :size="14" /></template>
                        <span class="co-comfy-action-label">{{
                          t("outputGallery.shareBtn")
                        }}</span>
                      </AButton>
                    </ColabOneTooltip>
                  </div>

                  <div class="co-comfy-action-utils">
                    <ColabOneTooltip
                      v-if="!isShareMode"
                      :title="t('outputGallery.exportAlbumZip')"
                    >
                      <AButton
                        size="small"
                        class="co-comfy-icon-action-btn"
                        :loading="!!exportingAlbumZips[group.id]"
                        :disabled="
                          !canLoadAnyInGroup(group) ||
                          !!deletingAlbums[group.id] ||
                          isAnyGroupItemBusy(group)
                        "
                        data-testid="comfy-album-export-zip-btn"
                        @click="exportAlbumZip(group)"
                      >
                        <template #icon><PhFileArchive :size="14" /></template>
                        <span class="co-sr-only">{{
                          t("outputGallery.exportAlbumZip")
                        }}</span>
                      </AButton>
                    </ColabOneTooltip>

                    <template v-if="isShareMode">
                      <ColabOneTooltip
                        v-if="isGroupFullyExportedToDrive(group)"
                        :title="t('comfy.savedToHostDriveBtn')"
                      >
                        <span
                          role="img"
                          class="co-comfy-icon-badge is-saved"
                          data-testid="comfy-share-output-saved-host-drive"
                          :aria-label="t('comfy.savedToHostDriveBtn')"
                        >
                          <PhCloudCheck :size="14" aria-hidden="true" />
                          <span class="co-sr-only">{{
                            t("comfy.savedToHostDriveBtn")
                          }}</span>
                        </span>
                      </ColabOneTooltip>
                      <ColabOneTooltip
                        v-else-if="props.shareDriveExportReady"
                        :title="t('comfy.saveToHostDriveBtn')"
                      >
                        <AButton
                          size="small"
                          class="co-comfy-icon-action-btn co-comfy-export-drive-btn"
                          :loading="!!exportingAlbumDrives[group.id]"
                          :disabled="
                            !gpuReady ||
                            !!deletingAlbums[group.id] ||
                            isAnyGroupItemBusy(group)
                          "
                          data-testid="comfy-share-output-save-host-drive"
                          @click="saveAlbumToHostDrive(group)"
                        >
                          <template #icon>
                            <PhCloudArrowUp :size="14" />
                          </template>
                          <span class="co-sr-only">{{
                            t("comfy.saveToHostDriveBtn")
                          }}</span>
                        </AButton>
                      </ColabOneTooltip>
                    </template>

                    <ColabOneTooltip
                      v-else-if="!gpuReady || isGroupAllDriveBacked(group)"
                      :title="
                        getGroupDrivePath(group)
                          ? formatColabOneDrivePath(getGroupDrivePath(group))
                          : t('comfy.openOutputInDriveTooltip')
                      "
                    >
                      <AButton
                        size="small"
                        class="co-comfy-icon-action-btn co-comfy-export-drive-btn is-exported"
                        data-testid="comfy-output-open-drive"
                        @click="openExternalURL(getGroupDriveOpenUrl(group))"
                      >
                        <template #icon>
                          <PhArrowSquareOut :size="14" />
                        </template>
                        <span class="co-sr-only">{{
                          t("comfy.openOutputInDrive")
                        }}</span>
                      </AButton>
                    </ColabOneTooltip>

                    <ColabOneTooltip
                      v-else
                      :title="
                        isGroupFullyExportedToDrive(group)
                          ? t('comfy.exportedToDrive')
                          : state.setup.drive
                            ? t('outputGallery.exportAlbumDrive')
                            : t('comfy.exportDriveSetupTooltip')
                      "
                    >
                      <AButton
                        size="small"
                        class="co-comfy-icon-action-btn co-comfy-export-drive-btn"
                        :class="{
                          'is-exported': isGroupFullyExportedToDrive(group),
                        }"
                        :loading="!!exportingAlbumDrives[group.id]"
                        :disabled="
                          !gpuReady ||
                          isGroupFullyExportedToDrive(group) ||
                          !!deletingAlbums[group.id] ||
                          isAnyGroupItemBusy(group)
                        "
                        data-testid="comfy-output-export-drive"
                        @click="exportAlbumToDrive(group)"
                      >
                        <template #icon>
                          <PhCloudCheck
                            v-if="isGroupFullyExportedToDrive(group)"
                            :size="14"
                          />
                          <PhCloudArrowUp v-else :size="14" />
                        </template>
                        <span class="co-sr-only">{{
                          isGroupFullyExportedToDrive(group)
                            ? t("comfy.exportedToDrive")
                            : t("outputGallery.exportAlbumDrive")
                        }}</span>
                      </AButton>
                    </ColabOneTooltip>

                    <APopconfirm
                      :open="deleteConfirmOutputId === group.id"
                      :title="t('outputGallery.deleteAlbum')"
                      :description="
                        !isShareMode &&
                        (isGroupAnyDriveBacked(group) || !gpuReady)
                          ? t('outputGallery.deleteAlbumDriveConfirm', {
                              count: getDeletableGroupItems(group).length,
                            })
                          : t('outputGallery.deleteAlbumConfirm', {
                              count: getDeletableGroupItems(group).length,
                            })
                      "
                      :ok-text="t('common.confirmDelete')"
                      :cancel-text="t('common.cancel')"
                      :disabled="
                        !canDeleteGroup(group) ||
                        !!exportingAlbumZips[group.id] ||
                        !!exportingAlbumDrives[group.id] ||
                        !!deletingAlbums[group.id] ||
                        isAnyGroupItemBusy(group)
                      "
                      :ok-button-props="{ danger: true }"
                      placement="topRight"
                      @open-change="
                        (open) => {
                          if (open) {
                            deleteConfirmOutputId = group.id;
                          } else if (deleteConfirmOutputId === group.id) {
                            deleteConfirmOutputId = '';
                          }
                        }
                      "
                      @confirm="
                        deleteConfirmOutputId = '';
                        deleteOutputGroup(group);
                      "
                    >
                      <span class="co-popconfirm-anchor">
                        <ColabOneTooltip
                          :title="t('outputGallery.deleteAlbum')"
                          :disabled="deleteConfirmOutputId === group.id"
                        >
                          <AButton
                            size="small"
                            danger
                            class="co-comfy-icon-action-btn co-comfy-output-delete-btn"
                            :loading="!!deletingAlbums[group.id]"
                            :disabled="
                              !canDeleteGroup(group) ||
                              !!exportingAlbumZips[group.id] ||
                              !!exportingAlbumDrives[group.id] ||
                              isAnyGroupItemBusy(group)
                            "
                            :aria-label="t('outputGallery.deleteAlbum')"
                          >
                            <template #icon><PhTrash :size="14" /></template>
                          </AButton>
                        </ColabOneTooltip>
                      </span>
                    </APopconfirm>
                  </div>
                </div>
              </div>
            </article>

            <!-- Single-Item Output Card -->
            <article
              v-for="item in group.isAlbum ? [] : group.items"
              :key="item.id"
              :ref="(el) => registerOutputCardEl(group.id, el)"
              :data-output-group-id="group.id"
              class="co-comfy-output-item"
              :class="{
                'is-selectable': outputMultiSelectMode,
                'is-selected': selectedOutputGroupIdSet.has(group.id),
              }"
            >
              <div
                class="co-comfy-output-frame"
                :class="{
                  'is-zoomable':
                    item.kind === 'image' && Boolean(getOutputPreviewSrc(item)),
                }"
                :role="
                  outputMultiSelectMode
                    ? 'checkbox'
                    : item.kind === 'image' && Boolean(getOutputPreviewSrc(item))
                      ? 'button'
                      : undefined
                "
                :aria-checked="
                  outputMultiSelectMode
                    ? selectedOutputGroupIdSet.has(group.id)
                    : undefined
                "
                :tabindex="
                  outputMultiSelectMode ||
                  (item.kind === 'image' && Boolean(getOutputPreviewSrc(item)))
                    ? 0
                    : undefined
                "
                :aria-label="
                  outputMultiSelectMode
                    ? item.workflow_name
                      ? tb(item.workflow_name)
                      : item.filename
                    : item.kind === 'image' &&
                        Boolean(getOutputPreviewSrc(item))
                      ? t('outputGallery.viewFullImage')
                      : undefined
                "
                @click="
                  outputMultiSelectMode
                    ? toggleOutputGroupSelection(group.id)
                    : item.kind === 'image' && getOutputPreviewSrc(item)
                      ? openOutputGallery(group, item.id, 'stage')
                      : undefined
                "
                @keydown.enter.prevent="
                  outputMultiSelectMode
                    ? toggleOutputGroupSelection(group.id)
                    : item.kind === 'image' && getOutputPreviewSrc(item)
                      ? openOutputGallery(group, item.id, 'stage')
                      : undefined
                "
                @keydown.space.prevent="
                  outputMultiSelectMode
                    ? toggleOutputGroupSelection(group.id)
                    : item.kind === 'image' && getOutputPreviewSrc(item)
                      ? openOutputGallery(group, item.id, 'stage')
                      : undefined
                "
              >
                <div
                  v-if="!isShareMode && outputMultiSelectMode"
                  class="co-comfy-select-overlay"
                  aria-hidden="true"
                >
                  <ACheckbox
                    :checked="selectedOutputGroupIdSet.has(group.id)"
                    :tabindex="-1"
                  />
                </div>

                <template
                  v-if="item.kind === 'image' && getOutputPreviewSrc(item)"
                >
                  <img
                    :src="getOutputPreviewSrc(item)"
                    :alt="item.filename"
                    class="co-sk-reveal"
                    @load="onOutputMediaReady(item)"
                    @error="onOutputMediaError(item)"
                  />
                  <div
                    v-if="!outputMultiSelectMode"
                    class="co-comfy-album-hover-pill"
                  >
                    <PhMagnifyingGlassPlus :size="13" aria-hidden="true" />
                    <span>{{ t("outputGallery.viewFullImage") }}</span>
                  </div>
                </template>
                <video
                  v-else-if="item.kind === 'video' && getOutputPreviewSrc(item)"
                  :src="getOutputPreviewSrc(item)"
                  controls
                  :autoplay="Boolean(inlineAutoplayIds[item.id])"
                  playsinline
                  preload="auto"
                  class="co-sk-reveal"
                  @loadedmetadata="onOutputMediaReady(item)"
                  @loadeddata="onOutputMediaReady(item)"
                  @canplay="onOutputMediaReady(item)"
                  @playing="onOutputMediaPlaying(item)"
                  @ended="clearInlineAutoplay(item.id)"
                  @error="onOutputMediaError(item)"
                />
                <div
                  v-else-if="item.kind === 'audio' && getOutputPreviewSrc(item)"
                  class="co-comfy-audio-box co-sk-reveal"
                >
                  <PhWaveform :size="28" class="co-comfy-audio-icon" />
                  <audio
                    :src="getOutputPreviewSrc(item)"
                    controls
                    :autoplay="Boolean(inlineAutoplayIds[item.id])"
                    preload="auto"
                    @loadedmetadata="onOutputMediaReady(item)"
                    @loadeddata="onOutputMediaReady(item)"
                    @canplay="onOutputMediaReady(item)"
                    @playing="onOutputMediaPlaying(item)"
                    @ended="clearInlineAutoplay(item.id)"
                    @error="onOutputMediaError(item)"
                  />
                </div>
                <div
                  v-else-if="fetchingOutputs[item.id]"
                  class="co-comfy-preview-loading co-comfy-preview-skeleton"
                  role="status"
                  aria-busy="true"
                >
                  <ColabOneSkeleton
                    width="100%"
                    height="100%"
                    :radius="0"
                    variant="rect"
                    tone="subtle"
                  />
                  <div class="co-comfy-preview-pill">
                    <component
                      :is="
                        item.kind === 'video'
                          ? PhVideoCamera
                          : item.kind === 'audio'
                            ? PhWaveform
                            : PhImage
                      "
                      :size="15"
                    />
                    <span>{{ t("comfy.loadingOutput") }}</span>
                  </div>
                </div>
                <div
                  v-else-if="!canLoadOutputAsset(item) && isShareMode"
                  class="co-comfy-share-offline-frame"
                  data-testid="comfy-share-output-offline-frame"
                >
                  <PhWifiSlash :size="26" weight="duotone" />
                  <span>{{ t("workflowShare.guestOfflineBannerTitle") }}</span>
                </div>
                <div
                  v-else-if="!canLoadOutputAsset(item)"
                  class="co-comfy-drive-offline-frame"
                  data-testid="comfy-output-drive-offline-frame"
                >
                  <PhCloudCheck
                    :size="28"
                    weight="duotone"
                    class="co-comfy-drive-offline-icon"
                  />
                  <span class="co-comfy-drive-offline-title">
                    {{ t("comfy.outputStoredInDriveLabel") }}
                  </span>
                  <ColabOneTooltip
                    v-if="getOutputDrivePath(item)"
                    :title="formatColabOneDrivePath(getOutputDrivePath(item))"
                  >
                    <code class="co-comfy-drive-offline-path">
                      {{ formatColabOneDrivePath(getOutputDrivePath(item)) }}
                    </code>
                  </ColabOneTooltip>
                  <AButton
                    size="small"
                    type="primary"
                    ghost
                    :tabindex="outputMultiSelectMode ? -1 : undefined"
                    :aria-hidden="outputMultiSelectMode ? 'true' : undefined"
                    @click.stop="
                      outputMultiSelectMode
                        ? toggleOutputGroupSelection(group.id)
                        : openExternalURL(getOutputDriveOpenUrl(item))
                    "
                  >
                    <template #icon><PhArrowSquareOut :size="14" /></template>
                    {{ t("comfy.openOutputInDrive") }}
                  </AButton>
                </div>
                <div
                  v-else-if="isOutputTooLargeForPreview(item)"
                  class="co-comfy-preview-large-notice"
                >
                  <component
                    :is="
                      item.kind === 'video'
                        ? PhVideoCamera
                        : item.kind === 'audio'
                          ? PhWaveform
                          : PhImage
                    "
                    :size="26"
                    class="co-comfy-preview-large-icon"
                  />
                  <p>
                    {{
                      t(
                        isShareMode
                          ? "comfy.sharePreviewTooLarge"
                          : "comfy.videoPreviewTooLarge",
                        {
                          size: formatComfyByteSize(item.size),
                        },
                      )
                    }}
                  </p>
                </div>
                <button
                  v-else
                  type="button"
                  class="co-comfy-preview-trigger"
                  :disabled="!outputMultiSelectMode && !canLoadOutputAsset(item)"
                  :tabindex="outputMultiSelectMode ? -1 : undefined"
                  :aria-hidden="outputMultiSelectMode ? 'true' : undefined"
                  @click.stop="
                    outputMultiSelectMode
                      ? toggleOutputGroupSelection(group.id)
                      : requestInlinePreview(item)
                  "
                >
                  <component
                    :is="
                      item.kind === 'video'
                        ? PhVideoCamera
                        : item.kind === 'audio'
                          ? PhWaveform
                          : PhImage
                    "
                    :size="28"
                  />
                  <span>
                    {{
                      failedOutputs[item.id]
                        ? t("comfy.retryPreview")
                        : item.kind === "video"
                          ? !isShareMode &&
                            (isOutputDriveBacked(item) ||
                              isOutputExportedToDrive(item) ||
                              item.drive_synced)
                            ? t("comfy.previewDriveVideo")
                            : t("comfy.loadVideoPreview")
                          : item.kind === "audio"
                            ? !isShareMode &&
                              (isOutputDriveBacked(item) ||
                                isOutputExportedToDrive(item) ||
                                item.drive_synced)
                              ? t("comfy.previewDriveAudio")
                              : t("comfy.loadAudioPreview")
                            : t("comfy.loadImagePreview")
                    }}
                  </span>
                </button>
                <div
                  v-if="
                    bufferingStreams[item.id] &&
                    (item.kind === 'video' || item.kind === 'audio') &&
                    getOutputPreviewSrc(item)
                  "
                  class="co-comfy-preview-pill co-comfy-stream-buffering-pill"
                  data-testid="comfy-output-stream-buffering"
                  role="status"
                  aria-live="polite"
                >
                  <component
                    :is="item.kind === 'video' ? PhVideoCamera : PhWaveform"
                    :size="14"
                  />
                  <span>{{ t("comfy.loadingOutput") }}</span>
                </div>
              </div>

              <div class="co-comfy-output-body">
                <div class="co-comfy-output-title-row">
                  <ColabOneTooltip
                    :title="
                      item.workflow_name
                        ? tb(item.workflow_name)
                        : t('comfy.defaultWorkflowName')
                    "
                  >
                    <strong>{{
                      item.workflow_name
                        ? tb(item.workflow_name)
                        : t("comfy.defaultWorkflowName")
                    }}</strong>
                  </ColabOneTooltip>
                  <ATag :color="outputKindColor(item.kind)">
                    {{ outputKindLabel(item.kind) }}
                  </ATag>
                </div>

                <ColabOneTooltip :title="item.filename">
                  <small class="co-comfy-mono co-comfy-output-filename">
                    {{ item.filename }}
                  </small>
                </ColabOneTooltip>

                <ColabOneTooltip
                  v-if="formatOutputCreatedAt(item.created_at)"
                  :title="formatOutputCreatedAtTooltip(item.created_at)"
                >
                  <div
                    class="co-comfy-output-time"
                    data-testid="comfy-output-created-at"
                  >
                    <PhCalendarBlank :size="12" aria-hidden="true" />
                    <time
                      class="co-comfy-mono"
                      :datetime="formatOutputCreatedAtISO(item.created_at)"
                    >
                      {{ formatOutputCreatedAt(item.created_at) }}
                    </time>
                  </div>
                </ColabOneTooltip>

                <div class="co-comfy-output-meta-tags">
                  <ATag class="co-comfy-meta-tag">
                    {{ formatOutputSize(item.size) }}
                  </ATag>
                  <ATag
                    v-if="Number.isFinite(item.elapsed) && item.elapsed > 0"
                    class="co-comfy-meta-tag co-comfy-elapsed-tag"
                    data-testid="comfy-output-elapsed"
                  >
                    <PhClock :size="12" aria-hidden="true" />
                    <span>{{
                      t("task.elapsedTotal", {
                        duration: formatOutputElapsed(item.elapsed),
                      })
                    }}</span>
                  </ATag>
                  <ColabOneTooltip
                    v-if="!isShareMode && formatOutputCUBadge(item)"
                    :title="formatOutputCUTooltip(item)"
                  >
                    <ATag
                      class="co-comfy-meta-tag co-comfy-cu-tag"
                      data-testid="comfy-output-cu"
                    >
                      <PhCoins :size="12" weight="duotone" aria-hidden="true" />
                      <span class="co-comfy-mono">{{
                        formatOutputCUBadge(item)
                      }}</span>
                    </ATag>
                  </ColabOneTooltip>
                  <ColabOneTooltip
                    v-if="!isShareMode && item.share_id"
                    :title="
                      item.share_label
                        ? t('comfy.outputTagSharedTooltip', {
                            label: item.share_label,
                          })
                        : t('comfy.outputTagSharedTooltipAnonymous')
                    "
                  >
                    <ATag
                      color="purple"
                      class="co-comfy-meta-tag co-comfy-shared-tag"
                      data-testid="comfy-output-shared-tag"
                    >
                      <PhShareNetwork :size="12" aria-hidden="true" />
                      <span>{{ t("comfy.outputTagShared") }}</span>
                    </ATag>
                  </ColabOneTooltip>
                  <ColabOneTooltip
                    v-if="
                      !isShareMode &&
                      (isOutputExportedToDrive(item) || item.drive_synced)
                    "
                    :title="
                      getOutputDrivePath(item)
                        ? formatColabOneDrivePath(getOutputDrivePath(item))
                        : t('comfy.outputDriveSyncedTooltipFolder', {
                            folder:
                              item.drive_mode === 'colab_drivefs'
                                ? 'ColabOne_Drive_Mount'
                                : activeDriveFolderName,
                          })
                    "
                  >
                    <ATag
                      :color="isOutputExportedToDrive(item) ? 'success' : 'blue'"
                      class="co-comfy-meta-tag co-comfy-drive-linked-tag"
                      data-testid="comfy-output-drive-tag"
                    >
                      <PhCloudCheck :size="12" aria-hidden="true" />
                      <span>{{
                        isOutputExportedToDrive(item)
                          ? t("comfy.outputTagSavedInDrive")
                          : t("comfy.outputTagSyncedInDrive")
                      }}</span>
                    </ATag>
                  </ColabOneTooltip>
                </div>

                <AAlert
                  v-if="failedOutputs[item.id]"
                  type="error"
                  class="co-comfy-output-error"
                  :message="formatOutputPreviewError(failedOutputs[item.id])"
                />

                <div class="co-comfy-output-actions">
                  <div class="co-comfy-action-primary">
                    <ColabOneTooltip
                      :title="
                        !canLoadOutputAsset(item)
                          ? isShareMode
                            ? t('workflowShare.guestOfflineBannerTitle')
                            : t('comfy.exportLocalNeedDriveApiTooltip')
                          : t('comfy.exportLocal')
                      "
                    >
                      <AButton
                        size="small"
                        class="co-comfy-primary-action-btn"
                        :loading="!!exportingOutputs[item.id]"
                        :disabled="
                          !canLoadOutputAsset(item) ||
                          !!deletingOutputs[item.id] ||
                          isItemParentGroupBusy(item.id)
                        "
                        :aria-label="t('comfy.exportLocal')"
                        @click="exportOutput(item)"
                      >
                        <template #icon>
                          <PhDownloadSimple :size="14" />
                        </template>
                        <span class="co-comfy-action-label">{{
                          t("comfy.exportLocal")
                        }}</span>
                      </AButton>
                    </ColabOneTooltip>
                    <ColabOneTooltip
                      v-if="!isShareMode"
                      :title="t('outputGallery.shareBtn')"
                    >
                      <AButton
                        size="small"
                        class="co-comfy-primary-action-btn"
                        data-testid="comfy-output-share-btn"
                        :aria-label="t('outputGallery.shareBtn')"
                        @click="openOutputShareForGroups([group.id])"
                      >
                        <template #icon><PhShareNetwork :size="14" /></template>
                        <span class="co-comfy-action-label">{{
                          t("outputGallery.shareBtn")
                        }}</span>
                      </AButton>
                    </ColabOneTooltip>
                  </div>

                  <div class="co-comfy-action-utils">
                    <template v-if="isShareMode">
                      <ColabOneTooltip
                        v-if="isOutputExportedToDrive(item)"
                        :title="t('comfy.savedToHostDriveBtn')"
                      >
                        <span
                          role="img"
                          class="co-comfy-icon-badge is-saved"
                          data-testid="comfy-share-output-saved-host-drive"
                          :aria-label="t('comfy.savedToHostDriveBtn')"
                        >
                          <PhCloudCheck :size="14" aria-hidden="true" />
                          <span class="co-sr-only">{{
                            t("comfy.savedToHostDriveBtn")
                          }}</span>
                        </span>
                      </ColabOneTooltip>
                      <ColabOneTooltip
                        v-else-if="props.shareDriveExportReady"
                        :title="t('comfy.saveToHostDriveBtn')"
                      >
                        <AButton
                          size="small"
                          class="co-comfy-icon-action-btn co-comfy-export-drive-btn"
                          :loading="!!exportingDriveOutputs[item.id]"
                          :disabled="
                            !gpuReady ||
                            !!deletingOutputs[item.id] ||
                            isItemParentGroupBusy(item.id)
                          "
                          data-testid="comfy-share-output-save-host-drive"
                          @click="saveOutputToHostDrive(item)"
                        >
                          <template #icon>
                            <PhCloudArrowUp :size="14" />
                          </template>
                          <span class="co-sr-only">{{
                            t("comfy.saveToHostDriveBtn")
                          }}</span>
                        </AButton>
                      </ColabOneTooltip>
                    </template>

                    <ColabOneTooltip
                      v-else-if="!gpuReady || isOutputDriveBacked(item)"
                      :title="
                        getOutputDrivePath(item)
                          ? formatColabOneDrivePath(getOutputDrivePath(item))
                          : t('comfy.openOutputInDriveTooltip')
                      "
                    >
                      <AButton
                        size="small"
                        class="co-comfy-icon-action-btn co-comfy-export-drive-btn is-exported"
                        data-testid="comfy-output-open-drive"
                        @click="openExternalURL(getOutputDriveOpenUrl(item))"
                      >
                        <template #icon>
                          <PhArrowSquareOut :size="14" />
                        </template>
                        <span class="co-sr-only">{{
                          t("comfy.openOutputInDrive")
                        }}</span>
                      </AButton>
                    </ColabOneTooltip>

                    <ColabOneTooltip
                      v-else
                      :title="
                        isOutputExportedToDrive(item)
                          ? t('comfy.exportDriveDoneTooltip', {
                              path: formatColabOneDrivePath(
                                getOutputDrivePath(item) ||
                                  `My Drive/${item.drive_mode === 'colab_drivefs' ? 'ColabOne_Drive_Mount' : 'ColabOne_Drive'}/outputs/${item.filename}`,
                              ),
                            })
                          : state.setup.drive
                            ? t('comfy.exportDriveReadyTooltipFolder', {
                                folder: activeDriveFolderName,
                              })
                            : t('comfy.exportDriveSetupTooltip')
                      "
                    >
                      <AButton
                        size="small"
                        class="co-comfy-icon-action-btn co-comfy-export-drive-btn"
                        :class="{
                          'is-exported': isOutputExportedToDrive(item),
                        }"
                        :loading="!!exportingDriveOutputs[item.id]"
                        :disabled="
                          !gpuReady ||
                          !!deletingOutputs[item.id] ||
                          isItemParentGroupBusy(item.id)
                        "
                        data-testid="comfy-output-export-drive"
                        @click="exportOutputToDrive(item)"
                      >
                        <template #icon>
                          <PhCloudCheck
                            v-if="isOutputExportedToDrive(item)"
                            :size="14"
                          />
                          <PhCloudArrowUp v-else :size="14" />
                        </template>
                        <span class="co-sr-only">{{
                          isOutputExportedToDrive(item)
                            ? t("comfy.exportedToDrive")
                            : t("comfy.exportDrive")
                        }}</span>
                      </AButton>
                    </ColabOneTooltip>

                    <APopconfirm
                      :open="deleteConfirmOutputId === item.id"
                      :title="t('comfy.deleteOutputTitle')"
                      :description="
                        !isShareMode &&
                        (isOutputDriveBacked(item) || !gpuReady)
                          ? isOutputMountModeDriveOnly(item)
                            ? t('comfy.deleteDriveMountOutputDesc', {
                                name: item.filename,
                              })
                            : t('comfy.deleteDriveOutputDesc', {
                                name: item.filename,
                              })
                          : t('comfy.deleteOutputDesc', { name: item.filename })
                      "
                      :ok-text="t('common.confirmDelete')"
                      :cancel-text="t('common.cancel')"
                      :disabled="
                        (!gpuReady &&
                          (isShareMode || !isOutputDriveBacked(item))) ||
                        !!exportingOutputs[item.id] ||
                        !!exportingDriveOutputs[item.id] ||
                        !!deletingOutputs[item.id] ||
                        isItemParentGroupBusy(item.id)
                      "
                      :ok-button-props="{ danger: true }"
                      placement="topRight"
                      @open-change="
                        (open) => {
                          if (open) {
                            deleteConfirmOutputId = item.id;
                          } else if (deleteConfirmOutputId === item.id) {
                            deleteConfirmOutputId = '';
                          }
                        }
                      "
                      @confirm="
                        deleteConfirmOutputId = '';
                        deleteOutput(item);
                      "
                    >
                      <span class="co-popconfirm-anchor">
                        <ColabOneTooltip
                          :title="t('comfy.deleteOutputAria')"
                          :disabled="deleteConfirmOutputId === item.id"
                        >
                          <AButton
                            size="small"
                            danger
                            class="co-comfy-icon-action-btn co-comfy-output-delete-btn"
                            :loading="!!deletingOutputs[item.id]"
                            :disabled="
                              (!gpuReady &&
                                (isShareMode || !isOutputDriveBacked(item))) ||
                              !!exportingOutputs[item.id] ||
                              !!exportingDriveOutputs[item.id] ||
                              isItemParentGroupBusy(item.id)
                            "
                            :aria-label="t('comfy.deleteOutputAria')"
                          >
                            <template #icon>
                              <PhTrash :size="14" />
                            </template>
                          </AButton>
                        </ColabOneTooltip>
                      </span>
                    </APopconfirm>
                  </div>
                </div>
              </div>
            </article>
          </template>
        </div>
      </div>

      <template #footer>
        <div class="co-comfy-outputs-drawer-footer">
          <AButton
            class="co-comfy-drawer-close-btn"
            data-testid="comfy-outputs-drawer-close-btn"
            @click="closeOutputsDrawer"
          >
            {{ t("common.close") }}
          </AButton>
        </div>
      </template>
    </ADrawer>

    <!-- Full-Window Album & Zoomable Lightbox Modal -->
    <ColabOneOutputGalleryModal
      :open="galleryModalOpen"
      :title="activeGalleryTitle"
      :items="activeGalleryItems"
      :initial-item-id="galleryModalInitialItemId"
      :initial-mode="galleryModalInitialMode"
      :resolve-media-src="resolveGalleryModalMediaSrc"
      :is-item-loading="isGalleryModalItemLoading"
      :is-item-available="isGalleryModalItemAvailable"
      :get-item-error="getGalleryModalItemError"
      :allow-download="true"
      :allow-zip-download="
        !isShareMode &&
        Boolean(activeGalleryGroup && canLoadAnyInGroup(activeGalleryGroup))
      "
      :allow-delete="true"
      :can-delete-item="canDeleteGalleryModalItem"
      :is-item-downloading="(it) => Boolean(exportingOutputs[it.id])"
      :downloading-zip="
        Boolean(activeGalleryGroup && exportingAlbumZips[activeGalleryGroup.id])
      "
      :is-item-deleting="(it) => Boolean(deletingOutputs[it.id])"
      @close="closeOutputGallery"
      @active-change="handleGalleryActiveChange"
      @request-load="handleGalleryRequestLoad"
      @retry-load="handleGalleryRetryLoad"
      @media-error="handleGalleryMediaError"
      @download-item="handleGalleryDownloadItem"
      @download-zip="exportAlbumZip(activeGalleryGroup)"
      @delete-item="handleGalleryDeleteItem"
    />

    <!-- Output Artifact & Album Sharing Modal (Host Mode) -->
    <ColabOneOutputShareModal
      v-if="!isShareMode"
      :open="outputShareModalOpen"
      :initial-tab="outputShareModalInitialTab"
      :all-groups="groupedOutputs"
      :initial-group-ids="outputShareModalInitialGroupIds"
      :shares-state="resolvedOutputSharesState"
      :web-console-state="resolvedWebConsoleState"
      :privacy-context="privacyContext"
      :resolve-media-src="getOutputPreviewSrc"
      :can-enable-web-console="Boolean(props.canEnableWebConsole)"
      :enabling-web-console="Boolean(props.enablingWebConsole)"
      :creating="Boolean(props.outputShareCreating)"
      :revoking-id="props.outputShareRevokingId || ''"
      :deleting-id="props.outputShareDeletingId || ''"
      :last-created-share="props.outputShareLastCreated ?? null"
      :copy-text="handleShareModalCopyFallback"
      @close="closeOutputShareModal"
      @enable-web-console="emit('enableWebConsole')"
      @create="(req) => emit('createOutputShare', req)"
      @revoke="(id) => emit('revokeOutputShare', id)"
      @delete="(id) => emit('deleteOutputShare', id)"
      @open-url="(url, shareId) => openOutputSharePreview(url, shareId)"
      @request-load="(item) => enqueueOutputPreviewPrefetch(item)"
    />

    <!-- Google Drive Configuration & Export Modal -->
    <ColabOneDriveExportModal
      v-if="!isShareMode"
      v-model:open="driveModalOpen"
      :state="state"
      :purpose="driveModalPurpose"
      :target-output="driveModalTarget"
      :exporting="driveModalExporting"
      :export-result="driveModalResult"
      :export-error="driveModalError"
      :connecting="connectingDriveInModal"
      :confirming-auth="confirmingDriveAuthInModal"
      :checking-headless="checkingHeadlessInModal"
      :saving-auto-drive="savingAutoDriveInModal"
      :arming-auto-release="workflowAutoReleaseBusy"
      :needs-remote-upgrade="driveModalNeedsUpgrade"
      @connect="connectDriveFromModal"
      @confirm-auth="confirmDriveAuthFromModal"
      @cancel-auth="cancelDriveAuthFromModal"
      @setup-headless="setupHeadlessDriveFromModal"
      @export-now="exportTargetFromModal"
      @arm-auto-release="armAutoReleaseFromModal"
      @upgrade="triggerUpgradeFromDriveModal"
      @open-settings="openSettingsFromDriveModal"
      @toggle-auto-drive="toggleAutoDriveFromModal"
      @open-url="openExternalURL"
    />

    <!-- Import Workflow Modal -->
    <AModal
      v-if="!isShareMode"
      v-model:open="importOpen"
      :title="t('comfy.importModalTitle')"
      :width="640"
      :centered="true"
      :destroy-on-close="true"
      :closable="!importing"
      :mask-closable="!importing"
      :keyboard="!importing"
      :z-index="1050"
      wrap-class-name="co-comfy-import-modal-wrap"
      @cancel="closeImportModal"
    >
      <div class="co-comfy-import-body">
        <p class="co-comfy-import-intro">
          {{ t("comfy.importModalDesc") }}
        </p>

        <!-- Drag/Drop or Choose .json File Box -->
        <div
          class="co-comfy-import-dropzone"
          :class="{ 'is-dragging': importDragging, 'is-disabled': importing }"
          @click="!importing && importFileInput?.click()"
          @dragover.prevent="if (!importing) importDragging = true;"
          @dragleave="onImportDragLeave"
          @drop.prevent="onImportFileDrop"
        >
          <input
            ref="importFileInput"
            type="file"
            accept=".json,application/json"
            class="co-visually-hidden"
            tabindex="-1"
            aria-hidden="true"
            :disabled="importing"
            @change="onImportFileChange"
          />
          <PhUploadSimple :size="22" class="co-comfy-import-drop-icon" />
          <div class="co-comfy-import-drop-copy">
            <strong>{{ t("comfy.importDropTitle") }}</strong>
            <small>{{ t("comfy.importDropHint") }}</small>
          </div>
          <AButton
            size="small"
            :disabled="importing"
            @click.stop="importFileInput?.click()"
          >
            {{ t("comfy.importPickFile") }}
          </AButton>
        </div>

        <div class="co-comfy-field-control">
          <label for="comfy-import-name" class="co-comfy-field-label-row">
            <span>{{ t("comfy.importNameLabel") }}</span>
          </label>
          <AInput
            id="comfy-import-name"
            v-model:value="importName"
            :placeholder="t('comfy.importNamePlaceholder')"
            :disabled="importing"
            allow-clear
          />
        </div>

        <div class="co-comfy-field-control">
          <label for="comfy-import-desc" class="co-comfy-field-label-row">
            <span>{{ t("comfy.importDescLabel") }}</span>
          </label>
          <AInput
            id="comfy-import-desc"
            v-model:value="importDescription"
            :placeholder="t('comfy.importDescPlaceholder')"
            :disabled="importing"
            allow-clear
          />
        </div>

        <div class="co-comfy-field-control">
          <label for="comfy-import-json" class="co-comfy-field-label-row">
            <span>{{ t("comfy.importJsonLabel") }}</span>
          </label>
          <ATextarea
            id="comfy-import-json"
            v-model:value="importRaw"
            :rows="7"
            class="co-comfy-json-input"
            spellcheck="false"
            placeholder='{"3": {"class_type": "KSampler", "inputs": {...}}}'
            :disabled="importing"
          />
        </div>

        <AAlert
          v-if="importValidation"
          :type="importValidation.ok ? 'success' : 'error'"
          :role="importValidation.ok ? 'status' : 'alert'"
          show-icon
          :message="
            importValidation.ok
              ? t('comfy.importValidSummary', { count: importNodeCount })
              : importValidation.error
          "
        />
      </div>

      <template #footer>
        <div class="co-comfy-import-modal-footer">
          <AButton
            :disabled="importing"
            @click="closeImportModal"
          >
            {{ t("common.cancel") }}
          </AButton>
          <AButton
            type="primary"
            :loading="importing"
            :disabled="!canSubmitImport"
            @click="submitImport"
          >
            {{ importing ? t("comfy.importingBtn") : t("comfy.importSubmitBtn") }}
          </AButton>
        </div>
      </template>
    </AModal>
  </div>
</template>

<style scoped>
.co-comfy-page {
  width: 100%;
  max-width: var(--co-container-max);
  margin: 0 auto;
  padding: 16px var(--co-container-pad) 32px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-comfy-page.is-share-mode {
  max-width: none;
  padding: 0;
  gap: 16px;
}

.co-comfy-share-workspace {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-comfy-share-task-card {
  border-radius: 14px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  padding: 14px 16px;
  box-shadow: var(--co-card-shadow);
}

.co-comfy-share-task-card .co-comfy-task-banner {
  margin-top: 0;
}

.co-comfy-share-offline-frame {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 16px;
  text-align: center;
  color: var(--co-muted);
  font-size: 12.5px;
}

.co-visually-hidden {
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

.co-comfy-mono {
  font-family: var(--co-font-mono);
  font-variant-numeric: tabular-nums;
}

.co-popconfirm-anchor {
  display: inline-flex;
  align-items: center;
}

/* Engine Status Header Card */
.co-comfy-engine-card {
  border-radius: 16px;
  flex-shrink: 0;
}

.co-comfy-engine-head {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.co-comfy-engine-icon {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  border: 1px solid rgba(59, 130, 246, 0.18);
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-comfy-engine-icon {
  color: var(--co-accent-strong);
}

.co-comfy-engine-meta {
  min-width: 0;
  flex: 1 1 280px;
}

.co-comfy-engine-title-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.co-comfy-engine-title-row h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  letter-spacing: -0.01em;
  line-height: 1.35;
  color: var(--co-text);
}

.co-comfy-queue-tag {
  font-variant-numeric: tabular-nums;
}

.co-comfy-engine-sub {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--co-muted);
  line-height: 1.55;
}

.co-comfy-engine-actions {
  margin-left: auto;
  flex-shrink: 0;
}

.co-comfy-card-alert {
  margin-top: 12px;
  border-radius: 8px;
}

.co-comfy-task-banner {
  margin-top: 12px;
  padding: 12px 14px;
  border-radius: 8px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.co-comfy-task-info {
  flex: 1 1 220px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-comfy-task-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 6px 10px;
  font-size: 13px;
}

.co-comfy-task-head-main {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  flex: 1 1 260px;
  min-width: 0;
  flex-wrap: wrap;
}

.co-comfy-task-head-main :deep(.ant-tag) {
  margin-inline-end: 0;
}

.co-comfy-task-head strong {
  color: var(--co-text);
  font-weight: 550;
  overflow-wrap: anywhere;
}

.co-comfy-task-metrics {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.co-comfy-task-elapsed {
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

.co-comfy-elapsed-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-variant-numeric: tabular-nums;
}

.co-comfy-task-download-meta {
  display: flex;
  align-items: center;
  flex-wrap: nowrap;
  gap: 4px 6px;
  min-width: 0;
  font-size: 12px;
  line-height: 1.4;
  color: var(--co-secondary-text);
}

.co-comfy-task-download-meta > :not(.co-comfy-task-download-file) {
  flex-shrink: 0;
}

.co-comfy-meta-dot {
  color: var(--co-muted);
  user-select: none;
}

.co-comfy-task-download-file {
  color: var(--co-muted);
  flex: 0 1 auto;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-comfy-task-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.co-comfy-task-outputs-btn {
  font-weight: 500;
}

/* Dual-layer Workspace Tabs */
.co-comfy-tabs-wrap {
  min-width: 0;
}

.co-comfy-tabs :deep(.ant-tabs-nav) {
  margin-bottom: 16px;
}

.co-comfy-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  line-height: 1.2;
  font-weight: 550;
}

.co-comfy-tab-label svg {
  flex-shrink: 0;
}

.co-comfy-tab-badge {
  display: inline-flex;
  align-items: center;
  line-height: 1;
}

.co-comfy-tab-badge :deep(.ant-badge-count) {
  background: var(--co-selected);
  color: var(--co-accent-strong);
  box-shadow: none;
  font-weight: 600;
  min-width: 20px;
  height: 18px;
  line-height: 18px;
  padding: 0 6px;
}

.co-comfy-tabs :deep(.ant-tabs-tab-active .co-comfy-tab-badge .ant-badge-count) {
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

:root[data-theme="dark"]
  .co-comfy-tabs
  :deep(.ant-tabs-tab-active .co-comfy-tab-badge .ant-badge-count) {
  background: rgba(59, 130, 246, 0.24);
  color: var(--co-accent-strong);
}

/* Tab 1: Workflow Studio */
.co-comfy-studio-section {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-comfy-studio-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.co-comfy-studio-toolbar-copy {
  min-width: 0;
}

.co-comfy-toolbar-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-comfy-studio-toolbar h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-studio-toolbar p {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--co-muted);
}

.co-comfy-count-tag {
  font-variant-numeric: tabular-nums;
}

.co-comfy-studio-toolbar-actions {
  flex-shrink: 0;
}

.co-comfy-share-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-comfy-share-trigger.is-active {
  border-color: rgba(139, 92, 246, 0.42);
}

.co-comfy-share-count {
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
  background: rgba(139, 92, 246, 0.14);
  color: #6d28d9;
}

:root[data-theme="dark"] .co-comfy-share-count {
  background: rgba(167, 139, 250, 0.22);
  color: #c4b5fd;
}

.co-comfy-outputs-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-comfy-outputs-trigger.is-active,
.co-comfy-outputs-trigger.is-running {
  border-color: rgba(59, 130, 246, 0.42);
}

.co-comfy-outputs-pulse {
  display: inline-flex;
  align-items: center;
}

.co-comfy-outputs-count {
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

:root[data-theme="dark"] .co-comfy-outputs-count {
  background: rgba(59, 130, 246, 0.24);
  color: var(--co-accent-strong);
}

.co-comfy-card {
  border-radius: 16px;
  min-width: 0;
}

.co-comfy-empty-card {
  padding: 28px 16px;
}

.co-comfy-empty-card :deep(.ant-card-body) {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.co-comfy-empty-card :deep(.ant-card-body::before),
.co-comfy-empty-card :deep(.ant-card-body::after) {
  display: none;
}

.co-comfy-empty-emblem {
  width: 54px;
  height: 54px;
  margin: 0 auto;
  border-radius: 14px;
  border: 1px solid rgba(59, 130, 246, 0.18);
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
}

:root[data-theme="dark"] .co-comfy-empty-emblem {
  color: var(--co-accent-strong);
}

.co-comfy-empty-copy {
  max-width: 400px;
  margin: 0 auto 14px;
  font-size: 13px;
  line-height: 1.65;
  color: var(--co-muted);
}

.co-comfy-empty-hint {
  display: block;
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-empty-actions {
  justify-content: center;
}

/* Full-Width Spacious Workflow Card Grid */
.co-comfy-workflow-list {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  align-items: stretch;
}

@media (max-width: 820px) {
  .co-comfy-workflow-list {
    grid-template-columns: minmax(0, 1fr);
  }
}

.co-comfy-workflow-item {
  position: relative;
  display: flex;
  flex-direction: column;
  border-radius: 14px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
  min-width: 0;
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    transform 0.16s ease;
}

.co-comfy-workflow-item:hover:not(.is-disabled) {
  border-color: var(--co-accent);
  box-shadow: var(--co-card-shadow-hover);
  transform: translateY(-1px);
}

.co-comfy-workflow-item.is-selected {
  border-color: var(--co-accent);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-comfy-workflow-item:has(.co-comfy-wf-card-trigger:focus-visible),
.co-comfy-workflow-item.is-selected:has(.co-comfy-wf-card-trigger:focus-visible) {
  border-color: var(--co-focus);
  box-shadow: 0 0 0 3px var(--co-focus-ring);
}

.co-comfy-workflow-item.is-disabled {
  opacity: 0.62;
  cursor: not-allowed;
}

.co-comfy-wf-card-trigger {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
  flex: 1;
  padding: 18px 20px;
  border: 0;
  border-radius: inherit;
  background: transparent;
  color: inherit;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  min-width: 0;
}

.co-comfy-wf-card-trigger:focus-visible {
  outline: none;
}

.co-comfy-wf-card-trigger:disabled {
  cursor: not-allowed;
}

.co-comfy-workflow-item.has-actions .co-comfy-wf-card-head {
  padding-right: 34px;
}

.co-comfy-wf-card-head {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.co-comfy-wf-emblem {
  width: 40px;
  height: 40px;
  border-radius: 11px;
  border: 1px solid rgba(59, 130, 246, 0.14);
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-comfy-wf-emblem {
  border-color: rgba(59, 130, 246, 0.26);
  color: var(--co-accent-strong);
}

.co-comfy-wf-card-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 7px;
  min-width: 0;
  flex: 1;
}

.co-comfy-wf-card-title {
  font-size: 15px;
  font-weight: 600;
  letter-spacing: -0.01em;
  line-height: 1.38;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-comfy-source-tag {
  font-size: 11.5px;
  line-height: 18px;
  min-height: 20px;
  padding: 0 8px;
  flex-shrink: 0;
  white-space: nowrap;
}

.co-comfy-wf-actions {
  position: absolute;
  top: 18px;
  right: 16px;
  z-index: 2;
  flex-shrink: 0;
}

.co-comfy-wf-delete-btn {
  opacity: 0.72;
  transition: opacity 0.15s ease;
}

.co-comfy-workflow-item:hover .co-comfy-wf-delete-btn,
.co-comfy-wf-delete-btn:focus-visible {
  opacity: 1;
}

.co-comfy-workflow-desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.co-comfy-wf-io-strip {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 8px 11px;
  border-radius: 9px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-secondary-text);
  overflow-wrap: anywhere;
}

.co-comfy-wf-io-row {
  display: block;
}

.co-comfy-wf-card-footer {
  margin-top: auto;
  padding-top: 12px;
  border-top: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}

.co-comfy-wf-specs {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 7px;
  min-width: 0;
}

.co-comfy-wf-spec-text {
  font-size: 12px;
  line-height: 1.4;
  color: var(--co-muted);
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}

.co-comfy-noncommercial-pill {
  font-size: 11px;
  line-height: 17px;
  min-height: 19px;
  padding: 0 7px;
}

.co-comfy-wf-cta {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12.5px;
  font-weight: 550;
  color: var(--co-secondary-text);
  flex-shrink: 0;
  transition: color 0.16s ease;
}

.co-comfy-workflow-item:hover .co-comfy-wf-cta,
.co-comfy-workflow-item.is-selected .co-comfy-wf-cta {
  color: var(--co-accent);
}

.co-comfy-meta-tag {
  font-size: 12px;
  line-height: 18px;
  padding-inline: 8px;
}

/* Shared Drawer Header & Footer Polish */
:global(.co-comfy-param-drawer-root .ant-drawer-header),
:global(.co-comfy-outputs-drawer-root .ant-drawer-header) {
  gap: 12px;
}

:global(.ant-drawer.co-comfy-outputs-drawer-root .ant-drawer-header) {
  flex-wrap: wrap;
  row-gap: 8px;
}

:global(.ant-drawer.co-comfy-outputs-drawer-root .ant-drawer-header-title) {
  flex: 1 0 230px;
  min-width: 210px;
}

:global(.co-comfy-param-drawer-root .ant-drawer-title),
:global(.co-comfy-outputs-drawer-root .ant-drawer-title) {
  min-width: 0;
  overflow: hidden;
}

:global(.ant-drawer.co-comfy-outputs-drawer-root .ant-drawer-extra) {
  min-width: 0;
  flex: 0 1 auto;
}

:global(.ant-drawer.co-comfy-param-drawer-root .ant-drawer-wrapper-body:has(> .ant-drawer-footer) .ant-drawer-body) {
  padding-bottom: 42px !important;
}

:global(.ant-drawer.co-comfy-param-drawer-root .ant-drawer-footer),
:global(.ant-drawer.co-comfy-outputs-drawer-root .ant-drawer-footer) {
  position: relative;
  border-top: 1px solid var(--co-border-secondary);
  background: var(--co-elevated);
}

.co-comfy-outputs-drawer-footer {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 8px;
  width: 100%;
}

.co-comfy-drawer-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
}

.co-comfy-drawer-title-icon {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-comfy-drawer-title-icon {
  color: var(--co-accent-strong);
}

.co-comfy-drawer-accent-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-comfy-drawer-accent-icon {
  color: var(--co-accent-strong);
}

.co-comfy-drawer-title-text {
  min-width: 0;
  flex: 0 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-drawer-title :deep(.ant-tag) {
  flex-shrink: 0;
  white-space: nowrap;
  word-break: keep-all;
  overflow-wrap: normal;
  padding-top: 0;
  padding-bottom: 0;
  line-height: 18px;
}

.co-comfy-reset-btn {
  color: var(--co-muted);
  white-space: nowrap;
}

.co-comfy-reset-btn:hover {
  color: var(--co-text);
}

/* Drawer 1: Workflow Parameter Configuration Drawer */
.co-comfy-param-drawer-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
}

.co-comfy-beginner-guide {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 11px 14px;
  border-radius: 11px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-comfy-beginner-guide-io {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12.5px;
  font-weight: 550;
  line-height: 1.45;
  color: var(--co-text);
}

.co-comfy-beginner-guide-io-row {
  display: block;
}

.co-comfy-beginner-guide-hint {
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-muted);
}

.co-comfy-drawer-desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

/* Dual-Option Video Engine Switcher (Built-in Digital Human) */
.co-comfy-engine-switcher {
  container-type: inline-size;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  width: 100%;
  box-sizing: border-box;
}

.co-comfy-engine-switcher-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 6px 10px;
}

.co-comfy-engine-switcher-label {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-engine-gpu-hint {
  font-size: 12px;
  line-height: 1.4;
  color: var(--co-muted);
}

.co-comfy-engine-options {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  align-items: stretch;
}

@container (max-width: 435px) {
  .co-comfy-engine-options {
    grid-template-columns: minmax(0, 1fr);
  }
}

.co-comfy-engine-option {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 5px;
  padding: 11px 12px;
  border-radius: 11px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  color: var(--co-text);
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  min-width: 0;
  width: 100%;
  box-sizing: border-box;
  transition:
    border-color 0.16s ease,
    background-color 0.16s ease,
    box-shadow 0.16s ease;
}

.co-comfy-engine-option:hover:not(:disabled):not(.is-selected) {
  border-color: var(--co-accent);
  background: var(--co-soft-hover);
}

.co-comfy-engine-option.is-selected {
  border-color: var(--co-accent);
  background: var(--co-selected);
  box-shadow: 0 0 0 1px var(--co-accent);
}

:root[data-theme="dark"] .co-comfy-engine-option.is-selected {
  box-shadow: 0 0 0 1px var(--co-focus-ring);
}

.co-comfy-engine-option:focus-visible,
.co-comfy-engine-option.is-selected:focus-visible,
:root[data-theme="dark"] .co-comfy-engine-option.is-selected:focus-visible {
  outline: 2px solid transparent;
  outline-offset: 2px;
  border-color: var(--co-focus);
  box-shadow:
    0 0 0 1px var(--co-accent),
    0 0 0 4px var(--co-focus-ring);
}

.co-comfy-engine-option:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.co-comfy-engine-option-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
}

.co-comfy-engine-option-title-group {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
  min-width: 0;
}

.co-comfy-engine-option-title {
  font-size: 13.5px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-comfy-engine-badge {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  box-sizing: border-box;
  padding: 1px 6px;
  border-radius: 9999px;
  font-size: 11px;
  font-weight: 550;
  line-height: 1.45;
  white-space: normal;
  overflow-wrap: anywhere;
}

.co-comfy-engine-badge.is-recommended {
  background: var(--co-selected);
  color: var(--co-accent-strong);
  border: 1px solid var(--co-focus-ring);
}

.co-comfy-engine-option.is-selected .co-comfy-engine-badge.is-recommended {
  background: var(--co-surface);
}

.co-comfy-engine-badge.is-ready {
  background: var(--co-success-bg);
  color: var(--co-success-text);
  border: 1px solid var(--co-success-border);
}

.co-comfy-engine-check-icon {
  color: var(--co-accent);
  flex-shrink: 0;
  margin-top: 1px;
}

:root[data-theme="dark"] .co-comfy-engine-check-icon {
  color: var(--co-accent-strong);
}

.co-comfy-engine-radio-dot {
  width: 15px;
  height: 15px;
  border-radius: 9999px;
  border: 1.5px solid var(--co-input-border);
  background: transparent;
  flex-shrink: 0;
  margin-top: 2px;
  transition: border-color 0.16s ease;
}

.co-comfy-engine-option:hover:not(:disabled) .co-comfy-engine-radio-dot {
  border-color: var(--co-accent);
}

.co-comfy-engine-option-desc {
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-secondary-text);
  overflow-wrap: anywhere;
}

.co-comfy-engine-option-spec {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 2px 5px;
  margin-top: auto;
  padding-top: 2px;
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--co-muted);
  overflow-wrap: anywhere;
}

.co-comfy-engine-option-spec > span {
  white-space: nowrap;
}

.co-comfy-engine-spec-dot {
  user-select: none;
}

@container (max-width: 560px) {
  .co-comfy-engine-option-spec {
    gap: 2px 10px;
  }

  .co-comfy-engine-spec-dot {
    display: none;
  }
}

.co-comfy-engine-option.is-selected .co-comfy-engine-option-spec {
  color: var(--co-secondary-text);
}

.co-comfy-engine-option.is-vram-short .co-comfy-engine-spec-vram {
  color: var(--co-warning-text);
  font-weight: 600;
}

@media (prefers-reduced-motion: reduce) {
  .co-comfy-workflow-item,
  .co-comfy-engine-option,
  .co-comfy-engine-radio-dot,
  .co-comfy-draft-status,
  .co-draft-status-fade-enter-active,
  .co-draft-status-fade-leave-active {
    transition: none;
  }

  .co-comfy-workflow-item:hover:not(.is-disabled) {
    transform: none;
  }
}

/* Minimalist Preflight & Pack Readiness Strip */
.co-comfy-preflight-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-comfy-preflight-strip {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 9px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  font-size: 12.5px;
  line-height: 1.48;
  color: var(--co-secondary-text);
}

.co-comfy-preflight-strip.is-warning {
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
  color: var(--co-warning-text);
}

.co-comfy-preflight-strip.is-ready {
  background: var(--co-success-bg);
  border-color: var(--co-success-border);
  color: var(--co-success-text);
}

.co-comfy-preflight-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

.co-comfy-preflight-text {
  min-width: 0;
  overflow-wrap: anywhere;
}

.co-comfy-param-form {
  display: flex;
  flex-direction: column;
  gap: 18px;
  min-width: 0;
}

.co-comfy-primary-fields {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
}

.co-comfy-primary-fields :deep(.co-comfy-tts-strip),
.co-comfy-primary-fields :deep(.co-comfy-voice-preview-card) {
  margin-top: -6px;
}

.co-comfy-field-control {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.co-comfy-field-label-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 13px;
  font-weight: 550;
  color: var(--co-text);
}

/* Collapsible Advanced Settings Accordion */
.co-comfy-advanced-panel {
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  overflow: hidden;
  min-width: 0;
  max-width: 100%;
  box-sizing: border-box;
  transition:
    border-color 0.16s ease,
    background-color 0.16s ease;
}

.co-comfy-advanced-panel.is-open {
  border-color: var(--co-border);
  background: var(--co-surface);
}

.co-comfy-advanced-toggle {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 12px 14px;
  border: 0;
  background: transparent;
  cursor: pointer;
  text-align: left;
  font-family: inherit;
  color: var(--co-text);
  transition: background-color 0.15s ease;
}

.co-comfy-advanced-toggle:hover {
  background: var(--co-soft-hover);
}

.co-comfy-advanced-toggle:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 2px var(--co-focus-ring);
}

.co-comfy-advanced-toggle-main {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
  flex: 1;
}

.co-comfy-advanced-toggle-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-comfy-advanced-toggle-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
  flex-shrink: 0;
}

.co-comfy-advanced-summary-chip {
  font-size: 12px;
  line-height: 1.35;
  color: var(--co-muted);
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  padding: 2px 9px;
  border-radius: 9999px;
  font-variant-numeric: tabular-nums;
  min-width: 0;
  flex: 0 1 auto;
  max-width: 100%;
  box-sizing: border-box;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-comfy-advanced-panel.is-open .co-comfy-advanced-summary-chip {
  background: var(--co-subtle);
}

.co-comfy-advanced-caret {
  color: var(--co-muted);
  flex-shrink: 0;
}

.co-comfy-advanced-body {
  padding: 14px;
  border-top: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
  max-width: 100%;
  box-sizing: border-box;
}

.co-comfy-advanced-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  align-items: start;
  min-width: 0;
}

.co-comfy-advanced-grid :deep(.is-wide) {
  grid-column: 1 / -1;
}

.co-comfy-specs-box {
  padding-top: 12px;
  border-top: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
}

.co-comfy-specs-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--co-muted);
}

.co-comfy-specs-pills {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
  max-width: 100%;
}

.co-comfy-form-divider {
  margin: 16px 0 14px;
}

.co-comfy-license-alert {
  border-radius: 10px;
}

.co-comfy-license-alert :deep(.ant-alert-description) {
  white-space: pre-line;
}

.co-comfy-license-alert-line {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.5;
  white-space: pre-line;
}

.co-comfy-license-alert-line + .co-comfy-license-alert-line {
  margin-top: 6px;
}

.co-comfy-param-drawer-footer-shell {
  width: 100%;
}

.co-comfy-draft-status-bar {
  position: absolute;
  left: 16px;
  right: 16px;
  bottom: calc(100% + 8px);
  z-index: 4;
  pointer-events: none;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  min-width: 0;
}

.co-comfy-param-drawer-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.co-comfy-param-drawer-footer :deep(.ant-btn:not(.ant-btn-icon-only):not(.ant-btn-link):not(.ant-btn-text)) {
  padding-inline: 11px;
}

.co-comfy-param-drawer-footer-left {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.co-comfy-drawer-autorelease-mobile-btn {
  display: none;
}

.co-comfy-param-drawer-footer-right {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  min-width: 0;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.co-comfy-drawer-run-wrap {
  display: inline-flex;
  align-items: center;
}

.co-comfy-autorelease-strip {
  flex: 1 0 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: nowrap;
  margin-top: 2px;
  padding: 8px 12px;
  border-radius: 7px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  transition:
    border-color 0.2s ease,
    background-color 0.2s ease;
}

.co-comfy-autorelease-strip.is-armed {
  border-color: color-mix(in srgb, var(--co-success) 42%, var(--co-border-secondary));
  background: color-mix(in srgb, var(--co-success) 6%, var(--co-surface));
}

.co-comfy-autorelease-strip.is-warning {
  border-color: color-mix(in srgb, var(--co-warning) 55%, var(--co-border-secondary));
  background: color-mix(in srgb, var(--co-warning) 8%, var(--co-surface));
}

.co-comfy-autorelease-strip-left {
  display: flex;
  align-items: center;
  gap: 4px 8px;
  flex-wrap: wrap;
  flex: 1 1 0;
  min-width: 0;
  font-size: 12.5px;
}

.co-comfy-autorelease-strip-left :deep(.ant-tag) {
  margin-inline-end: 0;
}

.co-comfy-autorelease-heading {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.co-comfy-autorelease-strip-right {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.co-comfy-autorelease-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-comfy-autorelease-strip.is-armed .co-comfy-autorelease-icon,
.co-comfy-drawer-autorelease-pill.is-armed .co-comfy-autorelease-icon,
.co-comfy-drawer-autorelease-mobile-btn.is-armed .co-comfy-autorelease-icon,
.co-comfy-autorelease-icon.is-armed {
  color: var(--co-success);
}

.co-comfy-autorelease-label {
  font-weight: 500;
  color: var(--co-text);
}

.co-comfy-autorelease-hint {
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-autorelease-switch-wrap {
  display: inline-flex;
  align-items: center;
}

.co-comfy-drawer-autorelease-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px;
  border-radius: 8px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  font-size: 12.5px;
  color: var(--co-text);
  user-select: none;
  transition:
    border-color 0.2s ease,
    background-color 0.2s ease;
}

.co-comfy-drawer-autorelease-pill.is-armed {
  border-color: color-mix(in srgb, var(--co-success) 45%, var(--co-border-secondary));
  background: color-mix(in srgb, var(--co-success) 8%, var(--co-subtle));
}

.co-comfy-drawer-autorelease-pill.is-disabled {
  opacity: 0.65;
}

.co-comfy-drawer-autorelease-text {
  font-weight: 500;
  white-space: nowrap;
}

.co-comfy-drive-offline-frame {
  width: 100%;
  min-height: 164px;
  padding: 18px 16px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  text-align: center;
  background: color-mix(in srgb, var(--co-accent) 4%, var(--co-subtle));
}

.co-comfy-drive-offline-icon {
  color: var(--co-success);
  flex-shrink: 0;
}

.co-comfy-drive-offline-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-drive-offline-path {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  padding: 2px 7px;
  border-radius: 5px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-secondary-text);
}

.co-comfy-drive-linked-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-comfy-draft-status {
  display: inline-block;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.35;
  color: var(--co-muted);
  background: var(--co-elevated);
  border: 1px solid var(--co-border);
  border-radius: var(--co-radius-pill);
  padding: 2px 10px;
  box-shadow: var(--co-card-shadow);
  white-space: nowrap;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  user-select: none;
  transition:
    color 0.18s ease,
    opacity 0.28s ease;
}

.co-comfy-draft-status.is-saving {
  color: var(--co-muted);
}

.co-comfy-draft-status.is-saved {
  color: var(--co-success-text);
}

.co-comfy-draft-status.is-restored {
  color: var(--co-accent-strong);
}

.co-draft-status-fade-enter-active {
  transition: opacity 0.18s ease;
}

.co-draft-status-fade-leave-active {
  transition: opacity 0.3s ease;
}

.co-draft-status-fade-enter-from,
.co-draft-status-fade-leave-to {
  opacity: 0;
}

.co-comfy-drawer-run-btn {
  min-width: 112px;
  padding-inline: 12px !important;
}

.co-comfy-mobile-ar-sheet {
  width: min(296px, calc(100vw - 44px));
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-comfy-mobile-ar-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.co-comfy-mobile-ar-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
  min-width: 0;
}

.co-comfy-mobile-ar-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-muted);
}

:global(.co-comfy-mobile-autorelease-popover .ant-popover-inner) {
  padding: 12px 14px;
  border-radius: 12px;
}

.co-comfy-import-modal-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
}

/* Drawer 2: Output Results Drawer */
.co-comfy-outputs-drawer-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-height: 100%;
  min-width: 0;
}

.co-comfy-outputs-drive-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border-radius: 9999px;
  font-size: 12px;
  font-weight: 500;
  padding-inline: 10px;
}

.co-comfy-outputs-drive-pill.is-connected {
  color: var(--co-success-text);
  background: var(--co-success-bg);
  border-color: var(--co-success-border);
}

.co-comfy-outputs-drive-pill.is-connected:hover:not(:disabled) {
  color: var(--co-success-text);
  border-color: var(--co-success);
}

.co-comfy-outputs-drive-pill.is-warning {
  color: var(--co-warning-text);
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
}

.co-comfy-outputs-drive-pill.is-active {
  color: var(--co-accent);
  background: var(--co-selected);
  border-color: rgba(59, 130, 246, 0.28);
}

.co-comfy-outputs-storage-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 10px;
  padding: 10px 13px;
  border-radius: 11px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  color: var(--co-secondary-text);
  font-size: 12.5px;
  line-height: 1.5;
}

.co-comfy-outputs-storage-bar.is-connected {
  background: var(--co-success-bg);
  border-color: var(--co-success-border);
  color: var(--co-success-text);
}

.co-comfy-outputs-storage-bar.is-waiting {
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
  color: var(--co-warning-text);
}

.co-comfy-outputs-storage-bar.is-active {
  background: var(--co-selected);
  border-color: rgba(59, 130, 246, 0.28);
  color: var(--co-text);
}

.co-comfy-outputs-storage-icon {
  flex-shrink: 0;
}

.co-comfy-outputs-storage-text {
  flex: 1 1 220px;
  min-width: 0;
  white-space: normal;
  overflow-wrap: break-word;
}

.co-comfy-outputs-storage-action {
  padding-inline: 0;
  height: auto;
  font-size: 12.5px;
  font-weight: 550;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-comfy-gallery-empty-wrap {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px 16px;
}

.co-comfy-output-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(276px, 100%), 1fr));
  gap: 14px;
  align-items: stretch;
  min-width: 0;
}

.co-comfy-output-item {
  border-radius: 14px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  overflow: hidden;
  min-width: 0;
  max-width: 100%;
  display: flex;
  flex-direction: column;
  container-type: inline-size;
  container-name: output-card;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.co-comfy-output-item:hover {
  border-color: var(--co-accent);
  box-shadow: var(--co-card-shadow-hover);
}

.co-comfy-output-item.is-selectable .co-comfy-output-frame {
  cursor: pointer;
}

.co-comfy-output-item.is-selectable .co-comfy-output-frame video,
.co-comfy-output-item.is-selectable .co-comfy-output-frame audio {
  pointer-events: none;
}

.co-comfy-output-item.is-selected {
  border-color: var(--co-accent);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--co-accent) 24%, transparent);
}

.co-comfy-outputs-shares-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.co-comfy-share-count-pill {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 17px;
  height: 17px;
  padding: 0 5px;
  border-radius: 9999px;
  background: var(--co-accent);
  color: #ffffff;
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
}

.co-comfy-multiselect-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  padding: 10px 14px;
  margin-bottom: 12px;
  border-radius: 12px;
  border: 1px solid color-mix(in srgb, var(--co-accent) 36%, var(--co-border));
  background: color-mix(in srgb, var(--co-accent) 7%, var(--co-surface));
  position: sticky;
  top: 0;
  z-index: 6;
  backdrop-filter: blur(10px);
  -webkit-backdrop-filter: blur(10px);
}

.co-comfy-multiselect-summary {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-select-overlay {
  position: absolute;
  top: 8px;
  left: 8px;
  z-index: 4;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 4px 6px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--co-surface) 90%, transparent);
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.14);
  pointer-events: none;
}

.co-comfy-output-frame {
  position: relative;
  min-height: 164px;
  background: var(--co-subtle);
  border-bottom: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}

.co-comfy-output-frame.is-zoomable,
.co-comfy-album-frame {
  cursor: pointer;
}

.co-comfy-album-frame {
  height: 220px;
  padding: 0;
  display: block;
}

.co-comfy-album-collage {
  width: 100%;
  height: 100%;
  display: grid;
  gap: 2px;
  background: var(--co-border-secondary);
}

.co-comfy-album-collage.is-count-1 {
  grid-template-columns: 1fr;
  grid-template-rows: 1fr;
}

.co-comfy-album-collage.is-count-2 {
  grid-template-columns: 1fr 1fr;
  grid-template-rows: 1fr;
}

.co-comfy-album-collage.is-count-3 {
  grid-template-columns: 1.35fr 1fr;
  grid-template-rows: 1fr 1fr;
}

.co-comfy-album-collage.is-count-3 .co-comfy-album-tile:first-child {
  grid-row: 1 / span 2;
}

.co-comfy-album-collage.is-count-4 {
  grid-template-columns: 1fr 1fr;
  grid-template-rows: 1fr 1fr;
}

.co-comfy-album-tile {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: var(--co-subtle);
}

.co-comfy-album-tile img {
  width: 100%;
  height: 100%;
  max-height: none;
  object-fit: cover;
  object-position: center top;
  display: block;
  transition: transform 0.24s ease;
}

.co-comfy-album-frame:hover .co-comfy-album-tile img {
  transform: scale(1.03);
}

.co-comfy-album-tile-media-fallback {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--co-muted);
  background: var(--co-subtle);
}

.co-comfy-album-more-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(15, 23, 42, 0.54);
  color: #ffffff;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.02em;
  backdrop-filter: blur(2px);
  -webkit-backdrop-filter: blur(2px);
}

.co-comfy-album-count-pill {
  position: absolute;
  top: 8px;
  right: 8px;
  z-index: 3;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 9px;
  border-radius: 9999px;
  background: rgba(15, 23, 42, 0.72);
  color: #ffffff;
  font-size: 11.5px;
  font-weight: 600;
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
  pointer-events: none;
}

.co-comfy-album-hover-pill {
  position: absolute;
  right: 8px;
  bottom: 8px;
  z-index: 3;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 10px;
  border-radius: 9999px;
  background: rgba(15, 23, 42, 0.76);
  color: #ffffff;
  font-size: 11.5px;
  font-weight: 500;
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
  opacity: 0;
  transform: translateY(3px);
  transition: opacity 0.16s ease, transform 0.16s ease;
  pointer-events: none;
}

.co-comfy-album-frame:hover .co-comfy-album-hover-pill,
.co-comfy-album-frame:focus-visible .co-comfy-album-hover-pill,
.co-comfy-output-frame.is-zoomable:hover .co-comfy-album-hover-pill,
.co-comfy-output-frame.is-zoomable:focus-visible .co-comfy-album-hover-pill {
  opacity: 1;
  transform: translateY(0);
}

.co-comfy-album-frame:focus-visible,
.co-comfy-output-frame.is-zoomable:focus-visible {
  outline: 2px solid var(--co-accent);
  outline-offset: -2px;
}

.co-comfy-output-frame:not(.co-comfy-album-frame) > img,
.co-comfy-output-frame:not(.co-comfy-album-frame) > video {
  width: 100%;
  max-height: 240px;
  object-fit: contain;
  display: block;
}

.co-comfy-audio-box {
  width: 100%;
  padding: 20px 14px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
}

.co-comfy-audio-icon {
  color: var(--co-accent);
}

:root[data-theme="dark"] .co-comfy-audio-icon {
  color: var(--co-accent-strong);
}

.co-comfy-preview-large-notice {
  width: 100%;
  min-height: 164px;
  padding: 18px 16px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  text-align: center;
  color: var(--co-secondary-text);
}

.co-comfy-preview-large-notice p {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.5;
  max-width: 240px;
}

.co-comfy-preview-large-icon {
  color: var(--co-muted);
  flex-shrink: 0;
}

.co-comfy-audio-box audio {
  width: 100%;
}

.co-comfy-preview-loading {
  position: relative;
  width: 100%;
  min-height: 164px;
  aspect-ratio: 16 / 10;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  font-size: 12.5px;
  color: var(--co-secondary-text);
}

.co-comfy-output-skeleton {
  background: var(--co-skeleton-card-bg);
  pointer-events: none;
}

.co-comfy-output-skeleton:hover {
  border-color: var(--co-border-secondary);
  box-shadow: none;
}

.co-comfy-output-frame-sk,
.co-comfy-gen-frame {
  position: relative;
  aspect-ratio: 16 / 10;
}

.co-comfy-output-generating {
  border-color: var(--co-accent);
  background: var(--co-skeleton-card-bg);
}

.co-comfy-preview-pill {
  position: absolute;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 11px;
  border-radius: 9999px;
  border: 1px solid var(--co-border-secondary);
  background: color-mix(in srgb, var(--co-surface) 84%, transparent);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  color: var(--co-text);
  font-size: 12px;
  font-weight: 500;
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.08);
  pointer-events: none;
}

.co-comfy-stream-buffering-pill {
  top: 10px;
  left: 50%;
  transform: translateX(-50%);
}

.co-comfy-gen-sparkle {
  color: var(--co-accent);
}

.co-comfy-gen-message {
  margin: 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-secondary-text);
  overflow-wrap: anywhere;
}

.co-comfy-gen-bar {
  position: absolute;
  left: 0;
  bottom: 0;
  height: 3px;
  background: linear-gradient(90deg, var(--co-accent), var(--co-accent-hover));
  transition: width 0.3s ease;
  z-index: 2;
}

.co-comfy-wf-skeleton {
  gap: 12px;
  padding: 18px 20px;
  background: var(--co-skeleton-card-bg);
  cursor: default;
  pointer-events: none;
  box-shadow: none;
}

.co-comfy-wf-skeleton:hover {
  border-color: var(--co-border-secondary);
  box-shadow: none;
  transform: none;
}

.co-comfy-wf-sk-desc {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-comfy-host-sk {
  flex: 1;
  display: inline-flex;
  align-items: center;
  min-height: 20px;
}

.co-comfy-canvas-connecting {
  margin-top: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-comfy-canvas-connecting-note {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 12px;
  border-radius: 10px;
  border: 1px dashed var(--co-border);
  background: var(--co-subtle);
  font-size: 12.5px;
  color: var(--co-secondary-text);
}

.co-comfy-canvas-launch-skeleton {
  margin-top: 0;
}

.co-comfy-preview-trigger {
  width: 100%;
  min-height: 164px;
  border: 0;
  background: transparent;
  color: var(--co-secondary-text);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 18px;
  font-size: 13px;
  cursor: pointer;
  transition:
    background 0.15s ease,
    color 0.15s ease;
}

.co-comfy-preview-trigger:hover:not(:disabled) {
  background: var(--co-hover);
  color: var(--co-accent);
}

.co-comfy-preview-trigger:focus-visible {
  outline: 2px solid var(--co-accent);
  outline-offset: -2px;
}

.co-comfy-preview-trigger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.co-comfy-output-body {
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  flex: 1;
  gap: 6px;
  min-width: 0;
}

.co-comfy-output-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
}

.co-comfy-output-title-row strong {
  font-size: 14px;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
}

.co-comfy-output-title-row :deep(.ant-tag) {
  margin-inline-end: 0;
  flex-shrink: 0;
  white-space: nowrap;
}

.co-comfy-output-filename {
  display: block;
  max-width: 100%;
  font-size: 12px;
  color: var(--co-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-comfy-output-time {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  width: fit-content;
  max-width: 100%;
  font-size: 11.5px;
  line-height: 1.35;
  color: var(--co-secondary-text);
  font-variant-numeric: tabular-nums;
}

.co-comfy-output-time svg {
  flex-shrink: 0;
  color: var(--co-muted);
}

.co-comfy-output-meta-tags {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 2px;
}

.co-comfy-output-meta-tags :deep(.ant-tag) {
  margin-inline-end: 0;
}

.co-comfy-cu-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--co-warning-text);
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
  font-variant-numeric: tabular-nums;
}

.co-comfy-output-error {
  margin-top: 4px;
}

.co-comfy-output-actions {
  margin-top: auto;
  padding-top: 8px;
  border-top: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  flex-wrap: nowrap;
  min-width: 0;
}

.co-comfy-action-primary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex-shrink: 1;
}

.co-comfy-action-utils {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.co-comfy-primary-action-btn {
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding-inline: 8px !important;
  white-space: nowrap;
  flex-shrink: 0;
}

.co-comfy-icon-action-btn {
  width: 28px;
  min-width: 28px;
  height: 28px;
  padding: 0 !important;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-comfy-icon-action-btn :deep(.ant-btn-icon) {
  margin-inline-end: 0 !important;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.co-comfy-icon-badge {
  position: relative;
  width: 28px;
  height: 28px;
  border-radius: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  user-select: none;
}

.co-comfy-icon-badge.is-saved {
  color: var(--co-success-text);
  background: var(--co-success-bg);
  border: 1px solid var(--co-success-border);
}

.co-comfy-output-export-group {
  display: flex;
  align-items: center;
  flex-wrap: nowrap;
  gap: 6px;
  min-width: 0;
  flex: 1;
}

.co-comfy-shared-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-comfy-share-saved-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 28px;
  padding: 0 9px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  color: var(--co-success-text);
  background: var(--co-success-bg);
  border: 1px solid var(--co-success-border);
  user-select: none;
  white-space: nowrap;
}

.co-comfy-export-drive-btn.is-exported {
  color: var(--co-success-text);
  background: var(--co-success-bg);
  border-color: var(--co-success-border);
}

.co-comfy-export-drive-btn.is-exported:hover:not(:disabled) {
  color: var(--co-success-text);
  border-color: var(--co-success);
}

.co-comfy-output-delete-btn {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  padding-inline: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

@container output-card (max-width: 308px) {
  .co-comfy-action-label {
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

  .co-comfy-primary-action-btn {
    width: 28px;
    min-width: 28px;
    padding-inline: 0 !important;
    gap: 0;
  }

  .co-comfy-primary-action-btn :deep(.ant-btn-icon) {
    margin-inline-end: 0 !important;
  }
}

@container output-card (max-width: 192px) {
  .co-comfy-output-actions {
    flex-wrap: wrap;
    row-gap: 6px;
  }
}

/* Tab 2: Native Canvas */
.co-comfy-canvas-section {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-comfy-canvas-security-alert {
  border-radius: 10px;
}

.co-comfy-canvas-banner-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.co-comfy-canvas-banner-head h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-canvas-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
  align-items: stretch;
}

.co-comfy-canvas-help {
  margin: 0;
  font-size: 13px;
  line-height: 1.65;
  color: var(--co-muted);
}

.co-comfy-named-fields {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-comfy-canvas-guide {
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  font-size: 13px;
  color: var(--co-secondary-text);
}

.co-comfy-canvas-guide summary {
  cursor: pointer;
  font-weight: 500;
  color: var(--co-text);
}

.co-comfy-canvas-guide ol {
  margin: 8px 0 0;
  padding-left: 18px;
  line-height: 1.7;
}

.co-comfy-canvas-guide code {
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  font-family: var(--co-font-mono);
  font-size: 12px;
}

.co-comfy-canvas-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 14px;
}

.co-comfy-metric-tile {
  padding: 12px 14px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-comfy-metric-tile span {
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-metric-tile strong {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-host-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-comfy-host-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--co-secondary-text);
}

.co-comfy-host-box {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
  padding: 9px 14px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-comfy-host-box code {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 13px;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-comfy-host-box :deep(.ant-tag) {
  flex-shrink: 0;
  white-space: nowrap;
  word-break: keep-all;
  overflow-wrap: normal;
  margin-inline-end: 0;
}

.co-comfy-canvas-launch-actions {
  margin-top: 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-comfy-canvas-secondary-row {
  display: grid;
  grid-auto-flow: column;
  grid-auto-columns: minmax(0, 1fr);
  gap: 8px;
}

.co-comfy-canvas-waiting {
  margin-top: 14px;
  padding: 14px;
  border-radius: 8px;
  background: var(--co-subtle);
  border: 1px dashed var(--co-border);
  display: flex;
  align-items: center;
  gap: 12px;
}

.co-comfy-canvas-waiting p {
  margin: 0;
  font-size: 13px;
  line-height: 1.65;
  color: var(--co-secondary-text);
}

.co-comfy-canvas-dev-tip {
  margin-top: 16px;
  border-radius: 8px;
}

.co-comfy-dev-tip-copy {
  margin: 0 0 6px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

/* Import Modal */
:global(.co-comfy-import-modal-wrap .ant-modal-content) {
  display: flex;
  flex-direction: column;
  max-height: calc(100dvh - 24px);
}

:global(.co-comfy-import-modal-wrap .ant-modal-header),
:global(.co-comfy-import-modal-wrap .ant-modal-footer) {
  flex-shrink: 0;
}

:global(.co-comfy-import-modal-wrap .ant-modal-body) {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.co-comfy-import-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-top: 4px;
}

.co-comfy-import-intro {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-muted);
}

.co-comfy-import-dropzone {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 8px;
  border: 1.5px dashed var(--co-border);
  background: var(--co-subtle);
  cursor: pointer;
  transition:
    border-color 0.16s ease,
    background 0.16s ease;
}

.co-comfy-import-dropzone:hover,
.co-comfy-import-dropzone.is-dragging {
  border-color: var(--co-accent);
  background: var(--co-hover);
}

.co-comfy-import-dropzone.is-disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.co-comfy-import-drop-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-comfy-import-drop-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-comfy-import-drop-copy strong {
  font-size: 14px;
  font-weight: 550;
  color: var(--co-text);
}

.co-comfy-import-drop-copy small {
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-import-dropzone :deep(.ant-btn) {
  flex-shrink: 0;
}

.co-comfy-json-input {
  font-family: var(--co-font-mono);
  font-size: 13px;
  line-height: 1.55;
}

@media (max-width: 860px) {
  .co-comfy-canvas-grid,
  .co-comfy-canvas-metrics {
    grid-template-columns: 1fr;
  }

  .co-comfy-canvas-secondary-row {
    grid-auto-flow: row;
    grid-template-columns: 1fr;
  }

  .co-comfy-task-download-meta {
    flex-wrap: wrap;
    gap: 4px 10px;
  }

  .co-comfy-meta-dot {
    display: none;
  }

  .co-comfy-task-download-file {
    flex: 1 0 100%;
  }

  .co-comfy-engine-actions {
    width: 100%;
    margin-left: 0;
  }
}

@media (max-width: 680px) {
  .co-comfy-task-head-main {
    flex: 1 1 100%;
  }

  .co-comfy-task-metrics {
    width: 100%;
    justify-content: space-between;
  }

  .co-comfy-tabs :deep(.ant-tabs-nav .ant-tabs-tab) {
    padding: 5px 9px !important;
    font-size: 12.5px;
  }

  .co-comfy-tab-label {
    gap: 5px;
  }

  .co-comfy-import-dropzone {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }

  .co-comfy-import-drop-copy {
    width: 100%;
  }

  .co-comfy-import-dropzone :deep(.ant-btn) {
    width: 100%;
  }

  :global(.co-comfy-import-modal-wrap #comfy-import-json) {
    max-height: 150px;
  }

  :global(.ant-drawer.co-comfy-param-drawer-root .ant-drawer-header) {
    gap: 6px;
  }

  :global(.ant-drawer.co-comfy-param-drawer-root .ant-drawer-header-title) {
    flex: 1 1 0;
    min-width: 0;
    overflow: hidden;
  }

  :global(.ant-drawer.co-comfy-param-drawer-root .ant-drawer-extra) {
    flex: 0 0 auto;
    max-width: none;
  }

  .co-comfy-reset-btn {
    padding-inline: 8px !important;
    font-size: 12px;
  }

  :global(.ant-drawer.co-comfy-outputs-drawer-root .ant-drawer-header) {
    flex-wrap: wrap;
    row-gap: 8px;
  }

  :global(.ant-drawer.co-comfy-outputs-drawer-root .ant-drawer-header-title) {
    flex: 1 1 100%;
    min-width: 0;
  }

  :global(.ant-drawer.co-comfy-outputs-drawer-root .ant-drawer-extra) {
    width: 100%;
    max-width: 100%;
    flex: 0 0 100%;
  }

  :global(.ant-drawer.co-comfy-outputs-drawer-root .ant-drawer-extra .ant-space) {
    justify-content: flex-start;
  }

  .co-comfy-draft-status-bar {
    justify-content: flex-start;
  }

  .co-comfy-param-drawer-footer {
    flex-wrap: nowrap;
    gap: 6px;
    width: 100%;
  }

  .co-comfy-drawer-autorelease-pill {
    display: none !important;
  }

  .co-comfy-param-drawer-footer-left {
    gap: 6px;
  }

  .co-comfy-drawer-close-btn {
    height: 36px;
    flex-shrink: 0;
    padding-inline: 10px !important;
    white-space: nowrap;
  }

  .co-comfy-drawer-autorelease-mobile-btn {
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    padding: 0 !important;
    flex-shrink: 0;
    border-radius: var(--co-radius);
    border-color: var(--co-border-secondary);
    background: var(--co-subtle);
  }

  .co-comfy-drawer-autorelease-mobile-btn.ant-btn-default.is-armed,
  .co-comfy-drawer-autorelease-mobile-btn.ant-btn-default.is-armed:hover:not(:disabled),
  :root[data-theme="dark"] .co-comfy-drawer-autorelease-mobile-btn.ant-btn-default.is-armed,
  :root[data-theme="dark"] .co-comfy-drawer-autorelease-mobile-btn.ant-btn-default.is-armed:hover:not(:disabled) {
    border-color: color-mix(in srgb, var(--co-success) 52%, var(--co-border-secondary));
    background: color-mix(in srgb, var(--co-success) 12%, var(--co-subtle));
  }

  .co-comfy-drawer-autorelease-mobile-btn.is-disabled .co-comfy-autorelease-icon {
    opacity: 0.55;
  }

  .co-comfy-mobile-ar-dot {
    position: absolute;
    top: 4px;
    right: 4px;
    width: 7px;
    height: 7px;
    border-radius: 9999px;
    background: var(--co-success);
    box-shadow: 0 0 0 1.5px var(--co-elevated);
  }

  .co-comfy-drawer-cancel-btn {
    height: 36px;
    flex-shrink: 0;
    padding-inline: 9px !important;
    white-space: nowrap;
  }

  .co-comfy-param-drawer-footer-right {
    flex: 1 1 0;
    min-width: 0;
    margin-left: 0;
    flex-wrap: nowrap;
    gap: 6px;
  }

  .co-comfy-drawer-run-wrap {
    flex: 1 1 0;
    min-width: 0;
    display: flex;
  }

  .co-comfy-drawer-run-wrap > * {
    width: 100% !important;
    max-width: 100%;
    min-width: 0;
  }

  .co-comfy-drawer-run-btn {
    width: 100%;
    min-width: 0;
    height: 36px;
    padding-inline: 10px !important;
    overflow: hidden;
  }

  .co-comfy-drawer-run-btn :deep(> span:not(.ant-btn-icon)) {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .co-comfy-import-modal-footer {
    width: 100%;
    flex-wrap: nowrap;
    gap: 8px;
  }

  .co-comfy-import-modal-footer > :first-child {
    flex-shrink: 0;
  }

  .co-comfy-import-modal-footer > :last-child {
    flex: 1 1 0;
    min-width: 0;
  }
}

@media (max-width: 560px) {
  .co-comfy-engine-option-spec {
    gap: 2px 10px;
  }

  .co-comfy-engine-spec-dot {
    display: none;
  }

  .co-comfy-advanced-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 420px) {
  :global(.ant-drawer.co-comfy-param-drawer-root .ant-drawer-close) {
    margin-inline-end: 8px;
  }

  .co-comfy-drawer-title {
    gap: 5px;
  }

  .co-comfy-reset-btn {
    padding-inline: 6px !important;
  }
}
</style>
