<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Badge as ABadge,
  Button as AButton,
  Card as ACard,
  Divider as ADivider,
  Drawer as ADrawer,
  Empty as AEmpty,
  Input as AInput,
  Modal as AModal,
  Popconfirm as APopconfirm,
  Progress as AProgress,
  Space as ASpace,
  TabPane as ATabPane,
  Tabs as ATabs,
  Tag as ATag,
  Textarea as ATextarea,
} from "ant-design-vue";
import ColabOneDriveExportModal, {
  formatColabOneDrivePath,
  isColabOneDriveConnectOperation,
  type ColabOneDriveExportResult,
} from "./ColabOneDriveExportModal.vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import ColabOneSecretInput from "./ColabOneSecretInput.vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import ComfyWorkflowField from "./ComfyWorkflowField.vue";
import {
  buildColabOnePrivacyContext,
  colabOnePrivacyActive,
  colabOnePrivacyPeekResetTick,
  colabOneSynthesizeDomainInput,
  extractColabOneHostFromInput,
  isColabOnePublicHost,
  segmentColabOneDomainInput,
} from "./colabOnePrivacy";
import { colabOneExtractTunnelToken } from "./colabOneSecretToken";
import {
  PhAppWindow,
  PhArrowRight,
  PhArrowSquareOut,
  PhArrowsClockwise,
  PhBroom,
  PhBrowsers,
  PhCaretDown,
  PhCaretRight,
  PhCheck,
  PhCheckCircle,
  PhClock,
  PhCloud,
  PhCloudArrowDown,
  PhCloudArrowUp,
  PhCloudCheck,
  PhDownloadSimple,
  PhEye,
  PhEyeSlash,
  PhFileCode,
  PhGraph,
  PhHardDrives,
  PhImage,
  PhInfo,
  PhLink,
  PhPlay,
  PhPlus,
  PhShieldCheck,
  PhSlidersHorizontal,
  PhSparkle,
  PhStop,
  PhTrash,
  PhTreeStructure,
  PhUploadSimple,
  PhVideoCamera,
  PhWarningCircle,
  PhWaveform,
} from "@phosphor-icons/vue";
import {
  colabOneCommand,
  colabOneExportWorkflowOutput,
  colabOneOpenComfyCanvas,
  colabOneOpenURL,
  colabOneWriteClipboard,
} from "./api";
import { formatIdlePolicyLabel } from "./billing";
import {
  formatColabOneDuration,
  resolveColabOneTaskElapsedSeconds,
} from "./colabOneDuration";
import { colabOneLocaleCode, t, tb } from "./i18n";
import {
  clearColabOneWorkflowDraft,
  colabOneBase64ToBlob,
  deleteColabOneWorkflowDraftMedia,
  hasColabOneDraftContent,
  loadColabOneWorkflowDraft,
  mergeColabOneWorkflowDraftFields,
  readColabOneWorkflowDraftMedia,
  sanitizeColabOneDraftFieldsForSave,
  saveColabOneWorkflowDraft,
  saveColabOneWorkflowDraftMedia,
  type ColabOneWorkflowDraftMediaMeta,
} from "./comfyDrafts";
import {
  buildColabOneWorkflowOverrides,
  defaultFieldValues,
  extractPublicHost,
  formatOutputSize,
  hasNonCommercialNotice,
  isColabOneVoiceRefRequired,
  isWorkflowFieldVisible,
  missingRequiredWorkflowFields,
  outputBlobURL,
  parseWorkflowImportJSON,
  partitionWorkflowFields,
  remapColabOnePresetPromptFieldsForLocale,
  resolveSelectedTtsEngine,
  workflowModelsTotalBytes,
} from "./comfyui";
import {
  computeColabOneModelDrawerWidth,
  type ColabOneToastKind,
} from "./colabOneToast";
import type {
  ColabOneSnapshot,
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
}>();
const emit = defineEmits<{
  device: [];
  settings: [];
  upgrade: [];
  sync: [];
  notify: [string, ColabOneToastKind?];
  clearError: [];
  error: [string];
  "drawer-state": [{ open: boolean; width: number; rootSelector: string }];
}>();

const activeTab = ref<"studio" | "canvas">("studio");
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
  try {
    await colabOneOpenURL(url);
  } catch (e) {
    reportError(e);
  }
}

// Engine & runtime status
const gpuReady = computed(() => props.state.runtime.state === "ready");
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
  if (queueBusy.value) {
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
const engineActionBlocked = computed(
  () => busy.value || !!props.state.operation || !!activeTask.value,
);

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

const comfyTaskElapsedText = computed(() => {
  const secs = resolveColabOneTaskElapsedSeconds(
    comfyTask.value,
    props.state.runtime,
    effectiveClockNow.value,
    effectiveSnapshotAt.value,
  );
  return secs === null
    ? ""
    : formatColabOneDuration(secs, {
        precision: "integer",
        locale: colabOneLocaleCode.value,
      });
});

function formatOutputElapsed(elapsed: number): string {
  return formatColabOneDuration(elapsed, {
    precision: "auto",
    locale: colabOneLocaleCode.value,
  });
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
const fieldValues = ref<Record<string, string | number | boolean>>({});

const paramDrawerOpen = ref(false);
const outputsDrawerOpen = ref(false);
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

const isDigitalHumanWorkflow = computed(
  () => selectedWorkflow.value?.pipeline?.kind === "digital_human",
);

const visibleFields = computed(() =>
  (selectedWorkflow.value?.fields || []).filter((f) =>
    isWorkflowFieldVisible(f, fieldValues.value),
  ),
);
const fieldPartitions = computed(() =>
  partitionWorkflowFields(visibleFields.value),
);
const selectedTtsEngine = computed<ComfyTtsEngineSpec | undefined>(() =>
  resolveSelectedTtsEngine(
    selectedWorkflow.value,
    fieldValues.value,
    ttsEngines.value,
  ),
);
const indextts2RequiresVoiceRef = computed(() =>
  isColabOneVoiceRefRequired(selectedWorkflow.value, fieldValues.value),
);
const showTtsEngineStrip = computed(
  () =>
    isDigitalHumanWorkflow.value &&
    String(fieldValues.value["pipeline.audio_source"] ?? "tts").trim() ===
      "tts" &&
    !!selectedTtsEngine.value,
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
    const notices = Array.from(
      new Set(
        items.map((item) =>
          item.notice
            ? tb(item.notice)
            : item.license
              ? tb(item.license)
              : t("comfy.licenseNonCommercial"),
        ),
      ),
    ).join(" ");
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
  const minVramMib = wf.min_vram_mib || 0;
  const minDiskMib = wf.min_disk_mib || 0;
  const modelBytes = workflowModelsTotalBytes(wf);
  if (minVramMib <= 0 && minDiskMib <= 0 && !(wf.models?.length || 0)) {
    return [];
  }

  const isPackActive = gpuReady.value && comfy.value?.active_pack === wf.id;
  const items: WorkflowPreflightItem[] = [];
  if (gpuReady.value) {
    const primaryGpuMib = props.state.runtime.gpu?.total_mib;
    const fallbackGpuMib = props.state.runtime.resources?.gpu?.total_mib;
    const gpuTotalMib =
      typeof primaryGpuMib === "number" &&
      Number.isFinite(primaryGpuMib) &&
      primaryGpuMib > 0
        ? primaryGpuMib
        : typeof fallbackGpuMib === "number" &&
            Number.isFinite(fallbackGpuMib) &&
            fallbackGpuMib > 0
          ? fallbackGpuMib
          : undefined;
    if (
      minVramMib > 0 &&
      gpuTotalMib !== undefined &&
      gpuTotalMib < minVramMib
    ) {
      const requiredGb = Math.max(1, Math.ceil(minVramMib / 1024));
      items.push({
        tone: "warning",
        text: t("comfy.preflightVramInsufficient", {
          required: requiredGb,
          available: formatGiBFloor(gpuTotalMib / 1024),
          gpu: wf.recommended_gpu ? tb(wf.recommended_gpu) : `${requiredGb} GB+ GPU`,
        }),
      });
    }

    const diskFreeBytes =
      typeof props.state.runtime.disk_free === "number" &&
      Number.isFinite(props.state.runtime.disk_free) &&
      props.state.runtime.disk_free > 0
        ? props.state.runtime.disk_free
        : typeof props.state.runtime.resources?.disk?.free_bytes === "number" &&
            Number.isFinite(props.state.runtime.resources.disk.free_bytes) &&
            props.state.runtime.resources.disk.free_bytes > 0
          ? props.state.runtime.resources.disk.free_bytes
          : undefined;
    if (diskFreeBytes !== undefined) {
      const requiredDiskBytes = isPackActive
        ? PREFLIGHT_DISK_HEADROOM_BYTES
        : minDiskMib > 0
          ? minDiskMib * 1024 ** 2
          : 0;
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
    }
  }
  return items;
});

const hasWorkflowSpecs = computed(() => {
  const wf = selectedWorkflow.value;
  if (!wf) return false;
  return Boolean(
    wf.min_vram_mib > 0 ||
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
  const renderQuality = String(
    fieldValues.value["pipeline.render_quality"] ?? "",
  ).trim();
  if (renderQuality === "balanced_480p_ai2x") {
    parts.push("480p");
  } else if (renderQuality === "native_720p") {
    parts.push("720p");
  }
  const upscale = String(
    fieldValues.value["pipeline.upscale_mode"] ??
      selectedWorkflow.value?.pipeline?.upscale_mode ??
      "",
  );
  if (upscale === "ai_2x") {
    parts.push("AI 2x");
  } else if (upscale === "lanczos") {
    parts.push("Lanczos");
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

function formatWorkflowCardSpecs(wf: WorkflowTemplate): string {
  const parts: string[] = [];
  if (wf.source === "builtin" || wf.min_vram_mib > 0 || wf.models?.length) {
    if (wf.min_vram_mib > 0) {
      parts.push(`${Math.max(1, Math.round(wf.min_vram_mib / 1024))} GB+`);
    }
    const totalBytes = workflowModelsTotalBytes(wf);
    if (totalBytes > 0) {
      parts.push(formatComfyByteSize(totalBytes));
    }
    if (wf.license) {
      parts.push(tb(wf.license));
    }
  }
  if (parts.length === 0) {
    parts.push(t("comfy.nodeCount", { count: wf.node_count }));
    parts.push(t("comfy.fieldCount", { count: wf.fields.length }));
  }
  return parts.join(" · ");
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

function isFieldForceRequired(field: WorkflowField): boolean {
  return (
    field.key === "pipeline.voice_ref_audio" && indextts2RequiresVoiceRef.value
  );
}

function fieldRequiredHint(field: WorkflowField): string {
  if (isFieldForceRequired(field)) {
    return t("comfy.indextts2RefRequiredHint");
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
    field.key === "pipeline.render_quality" ||
    field.key === "pipeline.acceleration_profile" ||
    field.key === "pipeline.upscale_mode"
  );
}

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
const hasUploadingField = computed(() =>
  Object.values(uploadingFields.value).some(Boolean),
);
const uploadGen: Record<string, number> = {};
const submittingRun = ref(false);
let disposed = false;

// Real-time workflow draft persistence state
const DRAFT_SAVE_DEBOUNCE_MS = 350;
const draftMediaMeta = ref<Record<string, ColabOneWorkflowDraftMediaMeta>>({});
const draftStatus = ref<"idle" | "saving" | "saved" | "restored">("idle");
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
  const snapshot = { ...fieldValues.value };
  const sanitized = sanitizeColabOneDraftFieldsForSave(wf.fields, snapshot);
  if (
    !disposed &&
    !opts?.silent &&
    selectedWorkflowId.value === targetWorkflowId
  ) {
    draftStatus.value = "saving";
  }
  void saveColabOneWorkflowDraft(targetWorkflowId, sanitized)
    .then((saved) => {
      if (
        disposed ||
        loadGen !== draftLoadGen ||
        saveGen !== draftSaveGen ||
        selectedWorkflowId.value !== targetWorkflowId
      ) {
        return;
      }
      if (saved) {
        draftStatus.value = "saved";
      } else if (draftStatus.value === "saving") {
        draftStatus.value = "idle";
      }
    })
    .catch((err) => {
      if (
        disposed ||
        opts?.silent ||
        loadGen !== draftLoadGen ||
        saveGen !== draftSaveGen ||
        selectedWorkflowId.value !== targetWorkflowId
      ) {
        return;
      }
      if (draftStatus.value === "saving") {
        draftStatus.value = "idle";
      }
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
  fieldValues.value[field.key] = val;
  const wf = selectedWorkflow.value;
  const isLongCatWorkflow =
    wf?.id === "digital-human-longcat" ||
    wf?.pipeline?.video_backend === "longcat_avatar";
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
  const isDiscrete =
    field.field_type === "select" || field.field_type === "boolean";
  scheduleDraftSave(isDiscrete);
}

function onFieldBlur(_field: WorkflowField) {
  flushDraftSave();
}

async function hydrateDraftMediaField(
  wf: WorkflowTemplate,
  fieldKey: string,
  loadGen: number,
  options?: { keepPreview?: boolean },
) {
  const mediaGen = uploadGen[fieldKey] ?? 0;
  let payload = null;
  try {
    payload = await readColabOneWorkflowDraftMedia(wf.id, fieldKey);
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
    void deleteColabOneWorkflowDraftMedia(wf.id, fieldKey).catch(() => {});
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
  }
}

async function restoreWorkflowDraft(
  wf: WorkflowTemplate,
  loadGen: number,
  restoreGen: number,
) {
  let draft = null;
  try {
    draft = await loadColabOneWorkflowDraft(wf.id);
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
  if (
    !draft ||
    draftDirty ||
    hasUploadingField.value ||
    Object.keys(pendingUploads.value).length > 0 ||
    Object.keys(uploadedInputIds.value).length > 0 ||
    Object.keys(draftMediaMeta.value).length > 0
  ) {
    return;
  }

  const mediaFieldMap = new Map(
    wf.fields
      .filter((f) => f.field_type === "image" || f.field_type === "audio")
      .map((f) => [f.key, f]),
  );
  const validMedia: Record<string, ColabOneWorkflowDraftMediaMeta> = {};
  for (const [key, meta] of Object.entries(draft.media || {})) {
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

  const mergedFields = remapColabOnePresetPromptFieldsForLocale(
    wf.fields,
    mergeColabOneWorkflowDraftFields(
      wf.fields,
      draft.fields,
      colabOneLocaleCode.value,
      validMedia,
    ),
    colabOneLocaleCode.value,
  );
  const nextUploadedNames: Record<string, string> = {};
  for (const [key, meta] of Object.entries(validMedia)) {
    nextUploadedNames[key] = meta.filename;
    mergedFields[key] = meta.filename;
  }

  draftRestoring = true;
  try {
    fieldValues.value = mergedFields;
    draftMediaMeta.value = validMedia;
    uploadedNames.value = nextUploadedNames;
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
    draftStatus.value = "restored";
  }

  for (const fieldKey of mediaKeys) {
    void hydrateDraftMediaField(wf, fieldKey, loadGen);
  }
}

const runButtonDisabledTooltip = computed(() => {
  if (!gpuReady.value) {
    return gpuStarting.value
      ? t("api.gpuStartingTooltip")
      : t("api.startGpuFirstTooltip");
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
      selectedWorkflowId.value = list[0].id;
    }
  },
  { immediate: true },
);

watch(
  () => selectedWorkflow.value?.id,
  (newId, oldId) => {
    if (oldId && oldId !== newId && draftDirty && draftWorkflowId === oldId) {
      const prevWf =
        workflows.value.find((w) => w.id === oldId) ??
        (activeDraftWorkflow?.id === oldId ? activeDraftWorkflow : undefined);
      if (prevWf) {
        flushDraftSave(prevWf);
      }
    }
    const loadGen = ++draftLoadGen;
    const restoreGen = ++draftRestoreGen;
    ++draftSaveGen;
    cancelDraftSaveTimer();
    draftDirty = false;
    draftStatus.value = "idle";
    draftMediaMeta.value = {};
    advancedExpanded.value = false;
    for (const k of Object.keys(uploadGen)) {
      uploadGen[k] = (uploadGen[k] ?? 0) + 1;
    }
    uploadingFields.value = {};
    const wf = selectedWorkflow.value;
    activeDraftWorkflow = wf;
    draftWorkflowId = wf?.id || "";
    fieldValues.value = wf
      ? defaultFieldValues(wf.fields, colabOneLocaleCode.value)
      : {};
    pendingUploads.value = {};
    uploadedInputIds.value = {};
    uploadedNames.value = {};
    revokeAllLocalPreviews();
    if (wf) {
      void restoreWorkflowDraft(wf, loadGen, restoreGen);
    }
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
      const loadGen = draftLoadGen;
      for (const key of Object.keys(draftMediaMeta.value)) {
        if (!pendingUploads.value[key]) {
          void hydrateDraftMediaField(wf, key, loadGen, { keepPreview: true });
        }
      }
    }
    abortAllOutputFetches();
    failedOutputs.value = {};
    exportedDriveOutputs.value = {};
    const staleUrls = Object.values(blobUrls.value);
    blobUrls.value = {};
    revokeBlobUrlsDeferred(staleUrls);
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
  draftStatus.value = "idle";
  draftMediaMeta.value = {};
  for (const k of Object.keys(uploadGen)) {
    uploadGen[k] = (uploadGen[k] ?? 0) + 1;
  }
  uploadingFields.value = {};
  fieldValues.value = defaultFieldValues(wf.fields, colabOneLocaleCode.value);
  pendingUploads.value = {};
  uploadedInputIds.value = {};
  uploadedNames.value = {};
  revokeAllLocalPreviews();
  void clearColabOneWorkflowDraft(wf.id).catch((err) => {
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
const IMAGE_EXT_RE = /\.(png|jpe?g|webp)$/i;
const AUDIO_EXT_RE = /\.(wav|mp3|flac|ogg|m4a)$/i;

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

async function handleMediaFile(field: WorkflowField, file: File) {
  if (submittingRun.value || uploadingFields.value[field.key]) return;
  const kind: "image" | "audio" =
    field.field_type === "audio" ? "audio" : "image";
  const isImageOk = IMAGE_EXT_RE.test(file.name);
  const isAudioOk = AUDIO_EXT_RE.test(file.name);
  if ((kind === "image" && !isImageOk) || (kind === "audio" && !isAudioOk)) {
    reportError(
      t(
        kind === "audio"
          ? "comfy.errUnsupportedAudioFormat"
          : "comfy.errUnsupportedImageFormat",
      ),
    );
    return;
  }
  if (file.size <= 0) {
    reportError(t("comfy.errEmptyFile"));
    return;
  }
  const maxBytes =
    kind === "audio" ? MAX_AUDIO_INPUT_BYTES : MAX_IMAGE_INPUT_BYTES;
  if (file.size > maxBytes) {
    reportError(
      t(kind === "audio" ? "comfy.errAudioTooLarge" : "comfy.errImageTooLarge"),
    );
    return;
  }

  const gen = (uploadGen[field.key] = (uploadGen[field.key] ?? 0) + 1);
  ++draftRestoreGen;
  const loadGen = draftLoadGen;
  const targetWorkflowId = selectedWorkflowId.value;
  const targetWf = selectedWorkflow.value;
  uploadingFields.value = { ...uploadingFields.value, [field.key]: true };
  let nextPreviewUrl = "";
  try {
    const data = await readFileAsBase64(file);
    if (
      disposed ||
      loadGen !== draftLoadGen ||
      uploadGen[field.key] !== gen ||
      selectedWorkflowId.value !== targetWorkflowId
    ) {
      return;
    }
    if (kind === "image" && isImageOk) {
      nextPreviewUrl = URL.createObjectURL(file);
    }

    // Immediately persist media into local workflow draft (both online & offline)
    let savedDraftMeta: ColabOneWorkflowDraftMediaMeta | null = null;
    let draftSaveErr: unknown = null;
    if (targetWorkflowId) {
      try {
        savedDraftMeta = await saveColabOneWorkflowDraftMedia(
          targetWorkflowId,
          field.key,
          { filename: file.name, kind, data },
        );
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
      return;
    }

    const effectiveFilename = savedDraftMeta?.filename || file.name;
    if (savedDraftMeta) {
      draftMediaMeta.value = {
        ...draftMediaMeta.value,
        [field.key]: savedDraftMeta,
      };
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
      draftStatus.value = "saved";
      draftDirty = true;
      flushDraftSave(targetWf);
    }

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
        selectedWorkflowId.value !== targetWorkflowId ||
        props.state.runtime.id !== uploadRuntimeId
      ) {
        if (nextPreviewUrl) URL.revokeObjectURL(nextPreviewUrl);
        return;
      }
      const assetId = typeof res?.id === "string" ? res.id : "";
      if (!assetId) {
        throw new Error(t("comfy.errUploadNoId"));
      }
      if (!savedDraftMeta && draftMediaMeta.value[field.key]) {
        const nextMeta = { ...draftMediaMeta.value };
        delete nextMeta[field.key];
        draftMediaMeta.value = nextMeta;
        if (targetWorkflowId) {
          void deleteColabOneWorkflowDraftMedia(
            targetWorkflowId,
            field.key,
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
      if (draftSaveErr) {
        emit(
          "notify",
          t("comfy.noticeUploadedRemote", { name: effectiveFilename }),
          "success",
        );
        reportError(
          t("comfy.errSaveDraft", {
            detail: formatDraftErrorDetail(draftSaveErr),
          }),
        );
      } else {
        setNoticeKey("comfy.noticeUploadedRemote", { name: effectiveFilename });
      }
    } else {
      if (!savedDraftMeta) {
        if (draftMediaMeta.value[field.key]) {
          const nextMeta = { ...draftMediaMeta.value };
          delete nextMeta[field.key];
          draftMediaMeta.value = nextMeta;
          if (targetWorkflowId) {
            void deleteColabOneWorkflowDraftMedia(
              targetWorkflowId,
              field.key,
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
      if (draftSaveErr) {
        emit(
          "notify",
          t("comfy.noticeStagedUpload", { name: effectiveFilename }),
          "info",
        );
        reportError(
          t("comfy.errSaveDraft", {
            detail: formatDraftErrorDetail(draftSaveErr),
          }),
        );
      } else {
        setNoticeKey("comfy.noticeStagedUpload", { name: effectiveFilename });
      }
    }
  } catch (e) {
    if (nextPreviewUrl) URL.revokeObjectURL(nextPreviewUrl);
    if (!disposed && uploadGen[field.key] === gen) {
      reportError(e);
    }
  } finally {
    if (uploadGen[field.key] === gen) {
      const next = { ...uploadingFields.value };
      delete next[field.key];
      uploadingFields.value = next;
    }
  }
}

function clearMediaField(field: WorkflowField) {
  if (submittingRun.value || uploadingFields.value[field.key]) {
    return;
  }
  ++draftRestoreGen;
  uploadGen[field.key] = (uploadGen[field.key] ?? 0) + 1;
  delete pendingUploads.value[field.key];
  delete uploadedInputIds.value[field.key];
  delete uploadedNames.value[field.key];
  revokeLocalPreview(field.key);
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
    const loadGen = draftLoadGen;
    void deleteColabOneWorkflowDraftMedia(wfId, field.key).catch((err) => {
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
  busy.value = true;
  try {
    await colabOneCommand("comfyui.workflow.delete", { id: target.id });
    if (draftWorkflowId === target.id) {
      ++draftLoadGen;
      ++draftRestoreGen;
      ++draftSaveGen;
      cancelDraftSaveTimer();
      draftDirty = false;
      draftStatus.value = "idle";
      draftMediaMeta.value = {};
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
  await actEngine("comfyui.free", {}, "comfy.noticeFreeVram");
}

async function stopComfy() {
  await actEngine("comfyui.stop", {}, "comfy.noticeStoppingEngine");
}

async function cancelActiveTask() {
  if (!comfyTask.value) return;
  try {
    await colabOneCommand("task.cancel", { id: comfyTask.value.id });
    setNoticeKey("comfy.noticeCancelRequested");
  } catch (e) {
    reportError(e);
  }
}

async function runWorkflow() {
  if (
    submittingRun.value ||
    hasUploadingField.value ||
    engineActionBlocked.value
  ) {
    return;
  }
  if (!gpuReady.value || comfyStarting.value) return;
  if (!comfyRunning.value) {
    await startComfy();
    return;
  }
  if (!selectedWorkflow.value) {
    return;
  }

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
        (targetEl.closest(".co-comfy-field") || targetEl).scrollIntoView?.({
          behavior: "smooth",
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
    const activeFields = wf.fields.filter(
      (field) =>
        isWorkflowFieldVisible(field, fieldValues.value) ||
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
      let payload = null;
      let draftReadErr: unknown = null;
      try {
        payload = await readColabOneWorkflowDraftMedia(targetWorkflowId, key);
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
        void deleteColabOneWorkflowDraftMedia(targetWorkflowId, key).catch(
          () => {},
        );
        revokeLocalPreview(key);
        delete uploadedNames.value[key];
        const field = wf.fields.find((f) => f.key === key);
        fieldValues.value[key] =
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
const blobUrls = ref<Record<string, string>>({});
const fetchingOutputs = ref<Record<string, boolean>>({});
const failedOutputs = ref<Record<string, OutputPreviewError>>({});
const exportingOutputs = ref<Record<string, boolean>>({});
const exportingDriveOutputs = ref<Record<string, boolean>>({});
const exportedDriveOutputs = ref<Record<string, ColabOneDriveExportResult>>({});
const deletingOutputs = ref<Record<string, boolean>>({});
const deleteConfirmOutputId = ref("");
const outputFetchControllers = new Map<string, AbortController>();

// Google Drive setup & cloud-to-cloud export modal state
const driveModalOpen = ref(false);
const driveModalTarget = ref<WorkflowOutput | null>(null);
const driveModalAutoExport = ref(false);
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

const driveWaitingBrowserAuth = computed(
  () =>
    gpuReady.value &&
    !props.state.setup.drive &&
    Boolean(props.state.auth_url) &&
    isColabOneDriveConnectOperation(props.state.operation),
);
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
const driveModalExporting = computed(() =>
  Boolean(
    driveModalTarget.value &&
      exportingDriveOutputs.value[driveModalTarget.value.id],
  ),
);

function isOutputTooLargeForPreview(item: WorkflowOutput): boolean {
  return Number.isFinite(item.size) && item.size > MAX_INLINE_PREVIEW_BYTES;
}

function formatOutputPreviewError(
  err: OutputPreviewError | undefined,
): string {
  if (!err) return "";
  if (err.code === "media") {
    return t("comfy.mediaPreviewError");
  }
  return err.detail ? tb(err.detail) : t("comfy.mediaPreviewError");
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

function abortAllOutputFetches() {
  for (const ctrl of outputFetchControllers.values()) {
    ctrl.abort();
  }
  outputFetchControllers.clear();
  fetchingOutputs.value = {};
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
  revokeBlobUrlsDeferred(urlsToRevoke);
}

function onOutputMediaError(item: WorkflowOutput) {
  if (disposed || !outputsDrawerOpen.value || !blobUrls.value[item.id]) return;
  const url = blobUrls.value[item.id];
  delete blobUrls.value[item.id];
  revokeBlobUrlsDeferred([url]);
  failedOutputs.value[item.id] = { code: "media" };
}

function triggerRecentImagePreviews(list: WorkflowOutput[]) {
  if (!gpuReady.value || !outputsDrawerOpen.value || disposed) return;
  for (const item of list.slice(0, 6)) {
    if (
      item.kind === "image" &&
      !isOutputTooLargeForPreview(item) &&
      !blobUrls.value[item.id] &&
      !failedOutputs.value[item.id] &&
      !Object.values(fetchingOutputs.value).some(Boolean)
    ) {
      void loadOutputPreview(item);
      break;
    }
  }
}

async function loadOutputPreview(item: WorkflowOutput) {
  if (
    isOutputTooLargeForPreview(item) ||
    blobUrls.value[item.id] ||
    fetchingOutputs.value[item.id] ||
    !gpuReady.value ||
    !outputsDrawerOpen.value
  ) {
    return;
  }
  const targetRuntimeId = props.state.runtime.id;
  abortOutputFetch(item.id);
  const controller = new AbortController();
  outputFetchControllers.set(item.id, controller);
  fetchingOutputs.value[item.id] = true;
  delete failedOutputs.value[item.id];
  try {
    const url = await outputBlobURL(item.id, controller.signal);
    if (
      disposed ||
      controller.signal.aborted ||
      outputFetchControllers.get(item.id) !== controller ||
      !outputsDrawerOpen.value ||
      props.state.runtime.id !== targetRuntimeId ||
      !outputs.value.some((o) => o.id === item.id)
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
    if (!aborted && !disposed && props.state.runtime.id === targetRuntimeId) {
      failedOutputs.value[item.id] = { code: "fetch", detail: String(e) };
    }
  } finally {
    if (outputFetchControllers.get(item.id) === controller) {
      outputFetchControllers.delete(item.id);
      delete fetchingOutputs.value[item.id];
    }
    if (!disposed && outputsDrawerOpen.value && gpuReady.value) {
      triggerRecentImagePreviews(outputs.value);
    }
  }
}

watch(outputsDrawerOpen, (open) => {
  if (!open) {
    pauseOutputsDrawerMedia();
    abortAllOutputFetches();
    releaseHeavyOutputBlobs();
  }
});

watch(
  [outputs, gpuReady, outputsDrawerOpen],
  ([list, ready, drawerOpen]) => {
    const activeIds = new Set(list.map((item) => item.id));
    const staleUrls: string[] = [];
    for (const [id, url] of Object.entries(blobUrls.value)) {
      if (!activeIds.has(id)) {
        staleUrls.push(url);
        delete blobUrls.value[id];
      }
    }
    revokeBlobUrlsDeferred(staleUrls);
    for (const id of Object.keys(failedOutputs.value)) {
      if (!activeIds.has(id)) {
        delete failedOutputs.value[id];
      }
    }
    for (const id of Object.keys(exportedDriveOutputs.value)) {
      if (!activeIds.has(id)) {
        const next = { ...exportedDriveOutputs.value };
        delete next[id];
        exportedDriveOutputs.value = next;
      }
    }
    if (
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
    if (!ready || !drawerOpen) return;
    // Automatically load previews for recent image outputs without saturating the bridge.
    triggerRecentImagePreviews(list);
  },
  { immediate: true },
);

watch(
  () => props.state.runtime.id,
  (nextId, prevId) => {
    if (prevId && nextId !== prevId) {
      exportedDriveOutputs.value = {};
      if (driveModalOpen.value) {
        driveModalTarget.value = null;
        driveModalAutoExport.value = false;
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
    if (driveModalAutoExport.value && driveModalTarget.value) {
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
    driveModalAutoExport.value = false;
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
) {
  driveModalTarget.value = target;
  driveModalAutoExport.value = autoExport;
  driveModalResult.value = null;
  driveModalError.value = "";
  driveModalOpen.value = true;
}

async function runDriveExport(
  item: WorkflowOutput,
  opts: { fromModal: boolean },
) {
  if (
    !gpuReady.value ||
    exportingDriveOutputs.value[item.id] ||
    deletingOutputs.value[item.id]
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
        : `${props.state.runtime.drive?.remote_folder || "ColabOne_Drive"}/outputs`;
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

async function exportOutputToDrive(item: WorkflowOutput) {
  if (
    !gpuReady.value ||
    exportingDriveOutputs.value[item.id] ||
    deletingOutputs.value[item.id]
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
  if (connectingDriveInModal.value || props.state.operation) return;
  clearDriveConnectTimer();
  connectingDriveInModal.value = true;
  sawDriveConnectOpInModal.value = false;
  driveConnectBaselineError.value = String(props.state.error || "").trim();
  driveModalError.value = "";
  if (driveModalTarget.value) {
    driveModalAutoExport.value = true;
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
    driveModalError.value = String(e);
  }
}

async function confirmDriveAuthFromModal() {
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
  clearDriveConnectTimer();
  connectingDriveInModal.value = false;
  sawDriveConnectOpInModal.value = false;
  driveModalAutoExport.value = false;
  driveModalError.value = "";
  try {
    await colabOneCommand("drive.cancel", {});
    emit("sync");
  } catch (e) {
    driveModalError.value = String(e);
  }
}

async function setupHeadlessDriveFromModal() {
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
  if (!driveModalTarget.value) return;
  void runDriveExport(driveModalTarget.value, { fromModal: true });
}

function triggerUpgradeFromDriveModal() {
  driveModalOpen.value = false;
  emit("upgrade");
}

function openSettingsFromDriveModal() {
  driveModalOpen.value = false;
  outputsDrawerOpen.value = false;
  emit("settings");
}

async function exportOutput(item: WorkflowOutput) {
  if (exportingOutputs.value[item.id] || deletingOutputs.value[item.id]) return;
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
    deletingOutputs.value[target.id] ||
    exportingOutputs.value[target.id] ||
    exportingDriveOutputs.value[target.id]
  ) {
    return;
  }
  deletingOutputs.value = { ...deletingOutputs.value, [target.id]: true };
  try {
    abortOutputFetch(target.id);
    await colabOneCommand("comfyui.output.delete", { id: target.id });
    const url = blobUrls.value[target.id];
    if (url) {
      delete blobUrls.value[target.id];
      revokeBlobUrlsDeferred([url]);
    }
    delete failedOutputs.value[target.id];
    if (exportedDriveOutputs.value[target.id]) {
      const nextExported = { ...exportedDriveOutputs.value };
      delete nextExported[target.id];
      exportedDriveOutputs.value = nextExported;
    }
    emit("sync");
    setNoticeKey("comfy.noticeDeletedOutput");
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
  const draftHost = extractColabOneHostFromInput(canvasDomain.value);
  if (
    draftHost &&
    !isColabOnePublicHost(draftHost) &&
    !ctx.hosts?.includes(draftHost)
  ) {
    return { ...ctx, hosts: [...(ctx.hosts || []), draftHost] };
  }
  return ctx;
});
const isCanvasDomainMasked = computed(
  () => colabOnePrivacyActive.value && Boolean(canvasDomain.value.trim()),
);
const canvasDomainPeeking = ref(false);
const canvasDomainHostRef = ref<HTMLElement | null>(null);
const displayedCanvasDomain = computed(() =>
  isCanvasDomainMasked.value && !canvasDomainPeeking.value
    ? colabOneSynthesizeDomainInput(canvasDomain.value)
    : canvasDomain.value,
);
const canvasDomainMaskSegments = computed(() =>
  isCanvasDomainMasked.value
    ? segmentColabOneDomainInput(canvasDomain.value)
    : [],
);

function onCanvasDomainPeekEnter() {
  if (isCanvasDomainMasked.value) {
    canvasDomainPeeking.value = true;
  }
}

function clearCanvasDomainPeek() {
  canvasDomainPeeking.value = false;
  if (
    typeof document !== "undefined" &&
    canvasDomainHostRef.value &&
    document.activeElement instanceof HTMLElement &&
    canvasDomainHostRef.value.contains(document.activeElement)
  ) {
    document.activeElement.blur();
  }
}

watch([colabOnePrivacyActive, colabOnePrivacyPeekResetTick], clearCanvasDomainPeek);
watch(isCanvasDomainMasked, (masked) => {
  if (!masked) clearCanvasDomainPeek();
});

function onCanvasDomainUpdate(next: string) {
  if (colabOnePrivacyActive.value) return;
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
  activeTab.value = key === "canvas" ? "canvas" : "studio";
}

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
  const res = await colabOneCommand("comfyui.canvas.tunnel_token");
  return typeof res?.token === "string" ? res.token : "";
}

function onCanvasTokenExtracted() {
  emit("notify", t("api.tunnelTokenExtractedToast"), "success");
}

async function stopCanvas() {
  await actEngine("comfyui.canvas.stop", {}, "comfy.noticeStoppedCanvas");
}

async function launchCanvas(target: "window" | "browser") {
  try {
    await colabOneOpenComfyCanvas(target);
  } catch (e) {
    reportError(e);
  }
}

async function copyCanvasAuthUrl() {
  try {
    const res = await colabOneCommand("comfyui.canvas.url");
    const url =
      typeof res === "string"
        ? res
        : res?.url || res?.auth_url || canvas.value?.url || "";
    if (!url) {
      throw new Error(t("comfy.errNoAuthUrl"));
    }
    try {
      await colabOneWriteClipboard(url);
      await navigator.clipboard?.writeText(url).catch(() => {});
    } catch {
      await navigator.clipboard.writeText(url);
    }
    copiedCanvasUrl.value = true;
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copiedCanvasUrl.value = false;
    }, 2000);
    setNoticeKey("comfy.noticeCopiedAuthLink");
  } catch (e) {
    reportError(e);
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

function handlePanelKeydown(e: KeyboardEvent) {
  if (e.key !== "Escape") return;
  if (
    importOpen.value ||
    driveModalOpen.value ||
    deleteConfirmWorkflowId.value ||
    deleteConfirmOutputId.value
  ) {
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

watch(paramDrawerOpen, (open) => {
  if (!open) {
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
  pauseOutputsDrawerMedia();
  abortAllOutputFetches();
  Object.values(blobUrls.value).forEach((url) => URL.revokeObjectURL(url));
  revokeAllLocalPreviews();
  emit("drawer-state", { open: false, width: 0, rootSelector: "" });
});
</script>

<template>
  <div class="co-comfy-page">
    <div class="co-page-header">
      <div>
        <h1 class="co-page-title">ComfyUI</h1>
        <p class="co-page-subtitle">
          {{ t("comfy.subtitle") }}
        </p>
      </div>
    </div>

    <!-- Top Engine Status Card -->
    <ACard
      class="co-comfy-engine-card"
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
                t("comfy.runningIdleHint", {
                  policy: formatIdlePolicyLabel(props.state.settings.idle_minutes),
                })
              }}
            </template>
            <template v-else-if="comfyStarting">
              {{ t("comfy.preparingDesc") }}
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
                <PhArrowRight :size="14" />
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
            <strong>{{
              comfyTask.message
                ? tb(comfyTask.message)
                : t("comfy.taskRunningFallback")
            }}</strong>
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
              class="co-comfy-mono"
            >
              · {{ comfyTaskDownloadSummary.etaText }}
            </span>
            <span
              v-if="comfyTaskDownloadSummary.filename"
              class="co-comfy-task-download-file co-comfy-mono"
            >
              ·
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
            @click="cancelActiveTask"
          >
            {{ t("comfy.cancelTask") }}
          </AButton>
        </div>
      </div>
    </ACard>

    <!-- Dual-layer Workspace Tabs -->
    <div ref="tabsWrapRef" class="co-comfy-tabs-wrap">
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
                v-if="workflows.length"
                :count="workflows.length"
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
                    {{ t("comfy.studioWorkflowCount", { count: workflows.length }) }}
                  </ATag>
                </div>
                <p>{{ t("comfy.studioDesc") }}</p>
              </div>
              <ASpace :size="10" wrap class="co-comfy-studio-toolbar-actions">
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
                    v-if="outputs.length > 0"
                    class="co-comfy-outputs-count co-comfy-mono"
                  >
                    {{ outputs.length }}
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
                    :delay="(idx - 1) * 75"
                  />
                  <div class="co-comfy-wf-card-heading">
                    <ColabOneSkeleton
                      :width="idx === 1 ? '60%' : '52%'"
                      :height="16"
                      variant="text"
                      :delay="(idx - 1) * 75 + 35"
                    />
                    <ColabOneSkeleton
                      :width="42"
                      :height="20"
                      variant="tag"
                      tone="subtle"
                      :delay="(idx - 1) * 75 + 65"
                    />
                  </div>
                </div>
                <div class="co-comfy-wf-sk-desc">
                  <ColabOneSkeleton
                    :width="idx === 1 ? '92%' : '86%'"
                    :height="13"
                    variant="text"
                    tone="subtle"
                    :delay="(idx - 1) * 75 + 90"
                  />
                  <ColabOneSkeleton
                    :width="idx === 1 ? '68%' : '62%'"
                    :height="13"
                    variant="text"
                    tone="subtle"
                    :delay="(idx - 1) * 75 + 115"
                  />
                </div>
                <div class="co-comfy-wf-card-footer">
                  <ColabOneSkeleton
                    :width="128"
                    :height="13"
                    variant="text"
                    tone="subtle"
                    :delay="(idx - 1) * 75 + 140"
                  />
                  <ColabOneSkeleton
                    :width="76"
                    :height="14"
                    variant="text"
                    tone="subtle"
                    :delay="(idx - 1) * 75 + 165"
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
              role="listbox"
              :aria-label="t('comfy.selectWorkflowAria')"
            >
              <div
                v-for="wf in workflows"
                :key="wf.id"
                role="option"
                tabindex="0"
                class="co-comfy-workflow-item"
                :class="{
                  'is-selected': wf.id === selectedWorkflowId && paramDrawerOpen,
                  'is-disabled': submittingRun || hasUploadingField,
                }"
                :aria-selected="wf.id === selectedWorkflowId"
                :aria-disabled="submittingRun || hasUploadingField"
                @click="openWorkflowDrawer(wf.id)"
                @keydown.enter.prevent="openWorkflowDrawer(wf.id)"
                @keydown.space.prevent="openWorkflowDrawer(wf.id)"
              >
                <div class="co-comfy-wf-card-head">
                  <div
                    class="co-comfy-wf-emblem"
                    :class="{ 'is-pipeline': wf.pipeline?.kind === 'digital_human' }"
                    aria-hidden="true"
                  >
                    <PhVideoCamera
                      v-if="wf.pipeline?.kind === 'digital_human'"
                      :size="20"
                      weight="duotone"
                    />
                    <PhTreeStructure v-else :size="20" weight="duotone" />
                  </div>

                  <div class="co-comfy-wf-card-heading">
                    <strong class="co-comfy-wf-card-title">{{ tb(wf.name) }}</strong>
                    <ATag
                      :color="wf.source === 'builtin' ? 'blue' : 'default'"
                      :bordered="false"
                      class="co-comfy-source-tag"
                    >
                      {{
                        wf.source === "builtin"
                          ? t("comfy.sourceBuiltin")
                          : t("comfy.sourceCustom")
                      }}
                    </ATag>
                  </div>

                  <div
                    v-if="wf.source === 'custom'"
                    class="co-comfy-wf-actions"
                    @click.stop
                    @keydown.stop
                  >
                    <APopconfirm
                      :title="t('comfy.deleteWorkflowTitle', { name: tb(wf.name) })"
                      :description="t('comfy.deleteWorkflowDesc')"
                      :ok-text="t('common.confirmDelete')"
                      :cancel-text="t('common.cancel')"
                      :disabled="busy"
                      :ok-button-props="{ danger: true }"
                      placement="topRight"
                      @open-change="
                        (open) => {
                          if (open) {
                            deleteConfirmWorkflowId = wf.id;
                          } else if (deleteConfirmWorkflowId === wf.id) {
                            deleteConfirmWorkflowId = '';
                          }
                        }
                      "
                      @confirm="deleteWorkflow(wf)"
                    >
                      <span class="co-popconfirm-anchor">
                        <ColabOneTooltip
                          :title="t('comfy.deleteWorkflowTooltip')"
                          :disabled="deleteConfirmWorkflowId === wf.id"
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

                <p
                  v-if="wf.description"
                  class="co-comfy-workflow-desc"
                >
                  {{ tb(wf.description) }}
                </p>

                <div
                  v-if="wf.pipeline?.kind === 'digital_human'"
                  class="co-comfy-wf-io-strip"
                >
                  {{ t("comfy.ioSummaryDigitalHuman") }}
                </div>

                <div class="co-comfy-wf-card-footer">
                  <div class="co-comfy-wf-specs">
                    <span class="co-comfy-wf-spec-text">
                      {{ formatWorkflowCardSpecs(wf) }}
                    </span>
                    <ColabOneTooltip
                      v-if="wf.commercial_ok === false"
                      :title="
                        wf.license_notice
                          ? tb(wf.license_notice)
                          : t('comfy.licenseNonCommercial')
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
                    <PhArrowRight :size="13" class="co-comfy-wf-cta-arrow" />
                  </span>
                </div>
              </div>
            </div>
          </section>
        </ATabPane>

        <!-- TAB 2: Native Canvas -->
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
                    <div
                      ref="canvasDomainHostRef"
                      class="co-privacy-input-host"
                      @mouseenter="onCanvasDomainPeekEnter"
                      @mouseleave="clearCanvasDomainPeek"
                    >
                      <AInput
                        id="comfy-canvas-domain"
                        :value="displayedCanvasDomain"
                        :disabled="canvasEnabled || busy"
                        :readonly="colabOnePrivacyActive"
                        placeholder="comfy.example.com"
                        autocomplete="off"
                        spellcheck="false"
                        :allow-clear="!colabOnePrivacyActive"
                        :class="{
                          'co-privacy-masked-input': isCanvasDomainMasked,
                          'co-privacy-readonly-input': colabOnePrivacyActive,
                          'is-peeking':
                            isCanvasDomainMasked && canvasDomainPeeking,
                        }"
                        :data-privacy-masked="
                          isCanvasDomainMasked ? 'true' : undefined
                        "
                        :data-privacy-peeking="
                          isCanvasDomainMasked && canvasDomainPeeking
                            ? 'true'
                            : undefined
                        "
                        @update:value="onCanvasDomainUpdate"
                      >
                        <template v-if="colabOnePrivacyActive" #suffix>
                          <ColabOneTooltip
                            :title="t('privacy.inputReadonlyTooltip')"
                          >
                            <span
                              class="co-privacy-input-badge"
                              aria-hidden="true"
                            >
                              <PhEye
                                v-if="
                                  isCanvasDomainMasked && canvasDomainPeeking
                                "
                                :size="15"
                              />
                              <PhEyeSlash v-else :size="15" />
                            </span>
                          </ColabOneTooltip>
                        </template>
                      </AInput>
                      <div
                        v-if="isCanvasDomainMasked"
                        class="co-privacy-input-veil"
                        :class="{
                          'is-peeking': canvasDomainPeeking,
                          'is-disabled': canvasEnabled || busy,
                        }"
                        aria-hidden="true"
                      >
                        <template
                          v-for="(seg, idx) in canvasDomainMaskSegments"
                          :key="idx"
                        >
                          <span v-if="seg.masked" class="co-privacy-mask">
                            <span class="co-privacy-mask-glyphs">{{
                              seg.text
                            }}</span>
                          </span>
                          <span v-else class="co-privacy-input-veil-plain">{{
                            seg.text
                          }}</span>
                        </template>
                      </div>
                    </div>
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
                      :load-saved="loadSavedCanvasToken"
                      :extract-secret="colabOneExtractTunnelToken"
                      @extracted="onCanvasTokenExtracted"
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
                    type="primary"
                    block
                    @click="launchCanvas('window')"
                  >
                    <template #icon><PhAppWindow :size="15" /></template>
                    {{ t("comfy.openInWindow") }}
                  </AButton>

                  <div class="co-comfy-canvas-secondary-row">
                    <AButton
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
                        <PhArrowRight :size="13" />
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
                      <PhArrowRight :size="14" />
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
            :class="{ 'is-pipeline': isDigitalHumanWorkflow }"
            aria-hidden="true"
          >
            <PhVideoCamera
              v-if="isDigitalHumanWorkflow"
              :size="16"
              weight="duotone"
            />
            <PhTreeStructure v-else :size="16" weight="duotone" />
          </span>
          <span class="co-comfy-drawer-title-text">
            {{ tb(selectedWorkflow.name) }}
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
          <span class="co-comfy-beginner-guide-io">
            {{ t("comfy.ioSummaryDigitalHuman") }}
          </span>
          <span class="co-comfy-beginner-guide-hint">
            {{ t("comfy.beginnerPresetHint") }}
          </span>
        </div>
        <p
          v-else-if="selectedWorkflow.description"
          class="co-comfy-drawer-desc"
        >
          {{ tb(selectedWorkflow.description) }}
        </p>

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
              <ComfyWorkflowField
                v-for="field in fieldPartitions.primary"
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
                @update:value="(val) => onFieldValueUpdate(field, val)"
                @field-blur="onFieldBlur"
                @media-select="handleMediaFile"
                @media-clear="clearMediaField"
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
                @click="advancedExpanded = !advancedExpanded"
              >
                <span class="co-comfy-advanced-toggle-main">
                  <PhSlidersHorizontal
                    :size="15"
                    class="co-comfy-advanced-toggle-icon"
                  />
                  <strong class="co-comfy-advanced-toggle-title">
                    {{ t("comfy.advancedToggleTitle") }}
                  </strong>
                  <span class="co-comfy-advanced-summary-chip">
                    {{ advancedPresetSummary }}
                  </span>
                </span>
                <PhCaretDown
                  v-if="advancedExpanded"
                  :size="15"
                  class="co-comfy-advanced-caret"
                />
                <PhCaretRight
                  v-else
                  :size="15"
                  class="co-comfy-advanced-caret"
                />
              </button>

              <div v-if="advancedExpanded" class="co-comfy-advanced-body">
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
                    @update:value="(val) => onFieldValueUpdate(field, val)"
                    @field-blur="onFieldBlur"
                    @media-select="handleMediaFile"
                    @media-clear="clearMediaField"
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
                        selectedWorkflow.license_notice
                          ? tb(selectedWorkflow.license_notice)
                          : selectedWorkflow.commercial_ok === false
                            ? t('comfy.licenseNonCommercial')
                            : t('comfy.licenseCommercialOk')
                      "
                    >
                      <ATag
                        :color="
                          selectedWorkflow.commercial_ok === false
                            ? 'orange'
                            : 'green'
                        "
                        class="co-comfy-meta-tag"
                      >
                        {{
                          selectedWorkflow.commercial_ok === false
                            ? `${tb(selectedWorkflow.license)} · ${t("comfy.licenseNonCommercial")}`
                            : tb(selectedWorkflow.license)
                        }}
                      </ATag>
                    </ColabOneTooltip>
                  </div>
                </div>
              </div>
            </div>
          </template>
        </form>

        <!-- Always-Visible Non-Commercial License Warning -->
        <AAlert
          v-if="nonCommercialNotice"
          type="warning"
          show-icon
          class="co-comfy-license-alert"
          :message="t('comfy.licenseNoticeTitle')"
          :description="nonCommercialAlertDescription"
        />
      </div>

      <template #footer>
        <div v-if="selectedWorkflow" class="co-comfy-param-drawer-footer">
          <ASpace :size="8">
            <AButton @click="closeParamDrawer">
              {{ t("common.close") }}
            </AButton>
            <AButton
              v-if="comfyRunTask || comfyTask"
              danger
              @click="cancelActiveTask"
            >
              {{ t("comfy.cancelTask") }}
            </AButton>
          </ASpace>
          <div class="co-comfy-param-drawer-footer-right">
            <span
              v-if="draftStatusLabel"
              class="co-comfy-draft-status"
              :class="`is-${draftStatus}`"
              role="status"
              aria-live="polite"
            >
              <PhCheck
                v-if="draftStatus === 'saved' || draftStatus === 'restored'"
                :size="13"
                aria-hidden="true"
              />
              <span>{{ draftStatusLabel }}</span>
            </span>
            <ColabOneTooltip :title="runButtonDisabledTooltip">
              <AButton
                type="primary"
                class="co-comfy-drawer-run-btn"
                :loading="submittingRun || comfyStarting"
                :disabled="
                  !gpuReady ||
                  comfyStarting ||
                  submittingRun ||
                  hasUploadingField ||
                  engineActionBlocked
                "
                @click="runWorkflow"
              >
                <template #icon>
                  <PhSparkle v-if="comfyRunning" :size="15" />
                  <PhPlay v-else :size="14" weight="fill" />
                </template>
                {{
                  !gpuReady
                    ? t("comfy.runWorkflow")
                    : comfyStarting
                      ? t("comfy.preparingBtn")
                      : !comfyRunning
                        ? t("comfy.startComfyFirst")
                        : submittingRun
                          ? t("common.submitting")
                          : t("comfy.runWorkflow")
                }}
              </AButton>
            </ColabOneTooltip>
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
            {{ t("comfy.outputCount", { count: outputs.length }) }}
          </ATag>
        </div>
      </template>

      <template #extra>
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
        <ColabOneTooltip
          v-else-if="state.setup.drive"
          :title="
            t('comfy.outputsDriveConnectedTooltip', {
              folder: state.runtime.drive?.remote_folder || 'ColabOne_Drive',
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
      </template>

      <div
        class="co-comfy-outputs-drawer-body"
        role="region"
        :aria-label="t('comfy.outputsDrawerAria')"
      >
        <!-- Full-Width Unclipped Storage & Persistence Hint Strip -->
        <div
          class="co-comfy-outputs-storage-bar"
          :class="{
            'is-connected': state.setup.drive,
            'is-waiting': driveWaitingBrowserAuth,
          }"
          data-testid="comfy-outputs-storage-bar"
        >
          <PhCloudCheck
            v-if="state.setup.drive"
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
                : state.setup.drive
                  ? t("comfy.drivePersistHint")
                  : t("comfy.localExportHint")
            }}
          </span>
          <AButton
            v-if="state.setup.drive"
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
                : t("comfy.outputsDriveConfigureNow")
            }}
            <PhArrowRight :size="13" />
          </AButton>
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
                <ColabOneSkeleton
                  v-else
                  :width="58"
                  :height="18"
                  variant="tag"
                  tone="subtle"
                  :delay="100"
                />
                <ColabOneSkeleton
                  :width="48"
                  :height="18"
                  variant="tag"
                  tone="subtle"
                  :delay="140"
                />
              </div>
            </div>
          </article>

          <article
            v-for="item in outputs"
            :key="item.id"
            class="co-comfy-output-item"
          >
            <div class="co-comfy-output-frame">
              <img
                v-if="item.kind === 'image' && blobUrls[item.id]"
                :src="blobUrls[item.id]"
                :alt="item.filename"
                class="co-sk-reveal"
                @error="onOutputMediaError(item)"
              />
              <video
                v-else-if="item.kind === 'video' && blobUrls[item.id]"
                :src="blobUrls[item.id]"
                controls
                playsinline
                preload="metadata"
                class="co-sk-reveal"
                @error="onOutputMediaError(item)"
              />
              <div
                v-else-if="item.kind === 'audio' && blobUrls[item.id]"
                class="co-comfy-audio-box co-sk-reveal"
              >
                <PhWaveform :size="28" class="co-comfy-audio-icon" />
                <audio
                  :src="blobUrls[item.id]"
                  controls
                  preload="metadata"
                  @error="onOutputMediaError(item)"
                />
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
                    t("comfy.videoPreviewTooLarge", {
                      size: formatComfyByteSize(item.size),
                    })
                  }}
                </p>
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
              <button
                v-else
                type="button"
                class="co-comfy-preview-trigger"
                :disabled="!gpuReady"
                @click="loadOutputPreview(item)"
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
                        ? t("comfy.loadVideoPreview")
                        : item.kind === "audio"
                          ? t("comfy.loadAudioPreview")
                          : t("comfy.loadImagePreview")
                  }}
                </span>
              </button>
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

              <div class="co-comfy-output-meta-tags">
                <ATag class="co-comfy-meta-tag">
                  {{ formatOutputSize(item.size) }}
                </ATag>
                <ATag
                  v-if="Number.isFinite(item.elapsed)"
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
              </div>

              <AAlert
                v-if="failedOutputs[item.id]"
                type="error"
                class="co-comfy-output-error"
                :message="formatOutputPreviewError(failedOutputs[item.id])"
              />

              <div class="co-comfy-output-actions">
                <div class="co-comfy-output-export-group">
                  <AButton
                    size="small"
                    :loading="!!exportingOutputs[item.id]"
                    :disabled="!gpuReady || !!deletingOutputs[item.id]"
                    @click="exportOutput(item)"
                  >
                    <template #icon>
                      <PhDownloadSimple :size="14" />
                    </template>
                    {{ t("comfy.exportLocal") }}
                  </AButton>

                  <ColabOneTooltip
                    :title="
                      !gpuReady
                        ? gpuStarting
                          ? t('api.gpuStartingTooltip')
                          : t('api.startGpuFirstTooltip')
                        : exportedDriveOutputs[item.id]
                          ? t('comfy.exportDriveDoneTooltip', {
                              path: formatColabOneDrivePath(
                                exportedDriveOutputs[item.id].drivePath,
                              ),
                            })
                          : state.setup.drive
                            ? t('comfy.exportDriveReadyTooltip')
                            : t('comfy.exportDriveSetupTooltip')
                    "
                  >
                    <AButton
                      size="small"
                      class="co-comfy-export-drive-btn"
                      :class="{
                        'is-exported': !!exportedDriveOutputs[item.id],
                      }"
                      :loading="!!exportingDriveOutputs[item.id]"
                      :disabled="!gpuReady || !!deletingOutputs[item.id]"
                      data-testid="comfy-output-export-drive"
                      @click="exportOutputToDrive(item)"
                    >
                      <template #icon>
                        <PhCloudCheck
                          v-if="exportedDriveOutputs[item.id]"
                          :size="14"
                        />
                        <PhCloudArrowUp v-else :size="14" />
                      </template>
                      {{
                        exportedDriveOutputs[item.id]
                          ? t("comfy.exportedToDrive")
                          : t("comfy.exportDrive")
                      }}
                    </AButton>
                  </ColabOneTooltip>
                </div>

                <APopconfirm
                  :title="t('comfy.deleteOutputTitle')"
                  :description="
                    t('comfy.deleteOutputDesc', { name: item.filename })
                  "
                  :ok-text="t('common.confirmDelete')"
                  :cancel-text="t('common.cancel')"
                  :disabled="
                    !gpuReady ||
                    !!exportingOutputs[item.id] ||
                    !!exportingDriveOutputs[item.id] ||
                    !!deletingOutputs[item.id]
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
                  @confirm="deleteOutput(item)"
                >
                  <span class="co-popconfirm-anchor">
                    <ColabOneTooltip
                      :title="t('comfy.deleteOutputAria')"
                      :disabled="deleteConfirmOutputId === item.id"
                    >
                      <AButton
                        size="small"
                        danger
                        class="co-comfy-output-delete-btn"
                        :loading="!!deletingOutputs[item.id]"
                        :disabled="
                          !gpuReady ||
                          !!exportingOutputs[item.id] ||
                          !!exportingDriveOutputs[item.id]
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
          </article>
        </div>
      </div>
    </ADrawer>

    <!-- Google Drive Configuration & Export Modal -->
    <ColabOneDriveExportModal
      v-model:open="driveModalOpen"
      :state="state"
      :target-output="driveModalTarget"
      :exporting="driveModalExporting"
      :export-result="driveModalResult"
      :export-error="driveModalError"
      :connecting="connectingDriveInModal"
      :confirming-auth="confirmingDriveAuthInModal"
      :checking-headless="checkingHeadlessInModal"
      :saving-auto-drive="savingAutoDriveInModal"
      :needs-remote-upgrade="needsUpgradeForDriveExport"
      @connect="connectDriveFromModal"
      @confirm-auth="confirmDriveAuthFromModal"
      @cancel-auth="cancelDriveAuthFromModal"
      @setup-headless="setupHeadlessDriveFromModal"
      @export-now="exportTargetFromModal"
      @upgrade="triggerUpgradeFromDriveModal"
      @open-settings="openSettingsFromDriveModal"
      @toggle-auto-drive="toggleAutoDriveFromModal"
      @open-url="openExternalURL"
    />

    <!-- Import Workflow Modal -->
    <AModal
      v-model:open="importOpen"
      :title="t('comfy.importModalTitle')"
      :width="640"
      :centered="true"
      :destroy-on-close="true"
      :closable="!importing"
      :mask-closable="!importing"
      :keyboard="!importing"
      :z-index="1050"
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
        <ASpace :size="10">
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
        </ASpace>
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
  gap: 10px;
  font-size: 13px;
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
  flex-wrap: wrap;
  gap: 4px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--co-secondary-text);
}

.co-comfy-task-download-file {
  color: var(--co-muted);
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
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px 20px;
  border-radius: 14px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
  cursor: pointer;
  min-width: 0;
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    transform 0.16s ease;
}

.co-comfy-workflow-item:hover {
  border-color: var(--co-accent);
  box-shadow: var(--co-card-shadow-hover);
  transform: translateY(-1px);
}

.co-comfy-workflow-item:focus-visible {
  outline: none;
  border-color: var(--co-accent);
  box-shadow: 0 0 0 3px var(--co-focus-ring);
}

.co-comfy-workflow-item.is-selected {
  border-color: var(--co-accent);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-comfy-workflow-item.is-disabled {
  opacity: 0.62;
  cursor: not-allowed;
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
}

.co-comfy-wf-actions {
  flex-shrink: 0;
  margin-left: auto;
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
  padding: 7px 11px;
  border-radius: 9px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-secondary-text);
  overflow-wrap: anywhere;
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

.co-comfy-wf-cta-arrow {
  transition: transform 0.16s ease;
}

.co-comfy-workflow-item:hover .co-comfy-wf-cta,
.co-comfy-workflow-item.is-selected .co-comfy-wf-cta {
  color: var(--co-accent);
}

.co-comfy-workflow-item:hover .co-comfy-wf-cta-arrow {
  transform: translateX(2px);
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

:global(.co-comfy-param-drawer-root .ant-drawer-title),
:global(.co-comfy-outputs-drawer-root .ant-drawer-title) {
  min-width: 0;
  overflow: hidden;
}

:global(.co-comfy-outputs-drawer-root .ant-drawer-extra) {
  flex-shrink: 0;
}

:global(.co-comfy-param-drawer-root .ant-drawer-footer) {
  border-top: 1px solid var(--co-border-secondary);
  background: var(--co-elevated);
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
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-reset-btn {
  color: var(--co-muted);
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
  gap: 3px;
  padding: 11px 14px;
  border-radius: 11px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-comfy-beginner-guide-io {
  font-size: 12.5px;
  font-weight: 550;
  line-height: 1.45;
  color: var(--co-text);
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
  max-width: 100%;
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
}

.co-comfy-advanced-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  align-items: start;
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
}

.co-comfy-form-divider {
  margin: 16px 0 14px;
}

.co-comfy-license-alert {
  border-radius: 10px;
}

.co-comfy-param-drawer-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.co-comfy-param-drawer-footer-right {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  margin-left: auto;
  min-width: 0;
}

.co-comfy-draft-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 9px;
  border-radius: 9999px;
  font-size: 12px;
  line-height: 1.35;
  color: var(--co-muted);
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  white-space: nowrap;
  max-width: 190px;
  overflow: hidden;
  text-overflow: ellipsis;
  transition:
    color 0.16s ease,
    background-color 0.16s ease,
    border-color 0.16s ease;
}

.co-comfy-draft-status.is-saved {
  color: var(--co-success-text);
  background: var(--co-success-bg);
  border-color: var(--co-success-border);
}

.co-comfy-draft-status.is-restored {
  color: var(--co-secondary-text);
}

.co-comfy-drawer-run-btn {
  min-width: 148px;
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
}

.co-comfy-output-item {
  border-radius: 14px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.co-comfy-output-item:hover {
  border-color: var(--co-accent);
  box-shadow: var(--co-card-shadow-hover);
}

.co-comfy-output-frame {
  min-height: 164px;
  background: var(--co-subtle);
  border-bottom: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}

.co-comfy-output-frame img,
.co-comfy-output-frame video {
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
  background: linear-gradient(90deg, var(--co-accent), #60a5fa);
  transition: width 0.3s ease;
  z-index: 2;
}

.co-comfy-wf-skeleton {
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

.co-comfy-output-error {
  margin-top: 4px;
}

.co-comfy-output-actions {
  margin-top: auto;
  padding-top: 6px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}

.co-comfy-output-export-group {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
  flex: 1;
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
  padding-inline: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
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
  padding: 9px 14px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-comfy-host-box code {
  font-size: 13px;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-comfy-canvas-launch-actions {
  margin-top: 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-comfy-canvas-secondary-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
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

.co-comfy-json-input {
  font-family: var(--co-font-mono);
  font-size: 13px;
  line-height: 1.55;
}

@media (max-width: 860px) {
  .co-comfy-canvas-grid,
  .co-comfy-canvas-secondary-row,
  .co-comfy-canvas-metrics {
    grid-template-columns: 1fr;
  }

  .co-comfy-engine-actions {
    width: 100%;
    margin-left: 0;
  }
}

@media (max-width: 560px) {
  .co-comfy-advanced-grid {
    grid-template-columns: 1fr;
  }
}
</style>
